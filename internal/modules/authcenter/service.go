package authcenter

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"cow-manager-backend/internal/auth"
	"cow-manager-backend/internal/httpx"
)

// totpIssuer 认证器 App 里显示的签发方。
const totpIssuer = "CowGalaxy GMS"

// pendingTOTP 正在绑定、尚未用验证码确认的密钥(只在内存,确认后才落库)。
type pendingTOTP struct {
	secret string
	exp    time.Time
}

// Service 登录与会话逻辑;同时实现 auth.Loader。
type Service struct {
	repo       *Repo
	tokenTTL   time.Duration
	refreshTTL time.Duration
	sessions   *auth.Manager

	totpMu      sync.Mutex
	totpPending map[int64]pendingTOTP

	// scopeCode 本部署要求的区域权限(game/ranch/{region});登录时没有它直接拒绝
	scopeCode, scopeName string
}

// SetScope 设置本部署的区域权限要求。
func (s *Service) SetScope(code, name string) { s.scopeCode, s.scopeName = code, name }

var (
	errBeyondScope  = httpx.NewError(httpx.CodePermissionDenied, "不能授予超出自己权限范围的角色或权限")
	errManageHigher = httpx.NewError(httpx.CodePermissionDenied, "不能管理权限范围超出自己的账号或角色")
	errSelfRoles    = httpx.NewError(httpx.CodeOperationFailed, "不能修改自己的角色")
	errSelfLock     = httpx.NewError(httpx.CodeOperationFailed, "不能锁定自己")
)

// coversRoles 操作者的权限是否覆盖这些角色合并后的权限(超级管理员恒为 true)。
func (s *Service) coversRoles(ctx context.Context, op *auth.Session, roles []string) (bool, error) {
	if op == nil {
		return false, nil
	}
	if op.IsSuperAdmin() {
		return true, nil
	}
	tree, err := s.buildTree(ctx, roles)
	if err != nil {
		return false, err
	}
	return op.Tree.Covers(tree), nil
}

// mustManage 操作者必须能管理该账号(目标账号的权限 ⊆ 操作者的权限),否则 errManageHigher。
func (s *Service) mustManage(ctx context.Context, op *auth.Session, target *User) error {
	ok, err := s.coversRoles(ctx, op, target.Roles)
	if err != nil {
		return err
	}
	if !ok {
		return errManageHigher
	}
	return nil
}

func sameRoles(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, r := range a {
		set[r] = true
	}
	for _, r := range b {
		if !set[r] {
			return false
		}
	}
	return true
}

// NewService 创建服务;sessions 在 New 之后由 main 注入(存在相互依赖)。
func NewService(db *sqlx.DB, tokenTTL, refreshTTL time.Duration) *Service {
	return &Service{repo: &Repo{db: db}, tokenTTL: tokenTTL, refreshTTL: refreshTTL, totpPending: map[int64]pendingTOTP{}}
}

// Repo 暴露数据访问,供其他模块(如导出枚举翻译)复用。
func (s *Service) Repo() *Repo { return s.repo }

// SetSessions 注入会话缓存。
func (s *Service) SetSessions(m *auth.Manager) { s.sessions = m }

// buildTree 合并用户全部角色的权限树;未知角色忽略。
func (s *Service) buildTree(ctx context.Context, roles []string) (*auth.Tree, error) {
	list, err := s.repo.RolesByNames(ctx, roles)
	if err != nil {
		return nil, err
	}
	trees := make([]*auth.Tree, 0, len(list))
	for _, rl := range list {
		t, err := auth.ParseTree(string(rl.PermissionTree))
		if err != nil {
			log.Printf("authcenter: 角色 %s 的权限树解析失败: %v", rl.Role, err)
			continue
		}
		trees = append(trees, t)
	}
	return auth.MergeTrees(trees...), nil
}

// Login 用户名密码登录,返回带 token 与权限树的用户。
// 已开启二步验证的账号必须同时提供 otp:缺失返回 CodeOtpRequired(前端据此弹出验证码框),错误返回 CodeOtpInvalid。
func (s *Service) Login(ctx context.Context, username, password, otp string) (*User, error) {
	u, err := s.repo.UserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, httpx.NewError(httpx.CodeUserNotExist, "用户不存在")
	}
	if !auth.VerifyPassword(password, u.PasswordSalt, u.PasswordHash) {
		return nil, httpx.NewError(httpx.CodePasswordMismatch, "密码错误")
	}
	now := time.Now().UnixMilli()
	if u.LockUntilTick > now {
		return nil, httpx.NewError(httpx.CodeAccountLocked, "账号已锁定")
	}
	if u.ExpireTick > 0 && u.ExpireTick <= now {
		return nil, httpx.NewError(httpx.CodeAccountExpired, "账号已过期")
	}
	if u.TwoStepSecret != "" {
		if strings.TrimSpace(otp) == "" {
			return nil, httpx.NewError(httpx.CodeOtpRequired, "请输入认证器验证码")
		}
		if !auth.VerifyTOTP(u.TwoStepSecret, otp, time.Now()) {
			return nil, httpx.NewError(httpx.CodeOtpInvalid, "验证码错误或已过期")
		}
	}
	// 与 Java 一致:token 为空或剩余有效期不足一半时才换新 token
	if u.Token == "" || u.TokenExpireTick-now < s.refreshTTL.Milliseconds() {
		u.Token = auth.GenerateToken(u.UID)
		u.TokenExpireTick = now + s.tokenTTL.Milliseconds()
		if err := s.repo.UpdateToken(ctx, u.UID, u.Token, u.TokenExpireTick); err != nil {
			return nil, err
		}
		s.sessions.InvalidateUser(u.UID)
	}
	if u.PermissionTree, err = s.buildTree(ctx, u.Roles); err != nil {
		return nil, err
	}
	if s.scopeCode != "" && !u.PermissionTree.Check(s.scopeCode) {
		return nil, httpx.NewError(httpx.CodePermissionDenied, "该账号没有区域 "+s.scopeName+" 的访问权限")
	}
	return u, nil
}

// CurrentUser 按会话重新加载用户(/login-with-token)。
func (s *Service) CurrentUser(ctx context.Context, sess *auth.Session) (*User, error) {
	u, err := s.repo.UserByUID(ctx, sess.UID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, httpx.TokenExpired()
	}
	u.PermissionTree = sess.Tree
	return u, nil
}

// LoadSession 实现 auth.Loader:从 token 反解 uid 并核对库中 token。
func (s *Service) LoadSession(ctx context.Context, token string) (*auth.Session, error) {
	uid, err := auth.DecodeUID(token)
	if err != nil {
		return nil, nil
	}
	u, err := s.repo.UserByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	if u == nil || u.Token == "" || u.Token != token || u.TokenExpireTick <= time.Now().UnixMilli() {
		return nil, nil
	}
	tree, err := s.buildTree(ctx, u.Roles)
	if err != nil {
		return nil, err
	}
	return &auth.Session{
		UID: u.UID, Username: u.Username, Roles: u.Roles,
		Token: u.Token, TokenExpireTick: u.TokenExpireTick, Tree: tree,
	}, nil
}

// Logout 清掉库里的 token,使其立即失效(Java 版只清内存缓存)。
func (s *Service) Logout(ctx context.Context, sess *auth.Session) error {
	s.sessions.InvalidateToken(sess.Token)
	return s.repo.UpdateToken(ctx, sess.UID, "", 0)
}

// ChangePassword 校验旧密码后换盐重哈希。
func (s *Service) ChangePassword(ctx context.Context, uid int64, oldPassword, newPassword string) error {
	u, err := s.repo.UserByUID(ctx, uid)
	if err != nil {
		return err
	}
	if u == nil {
		return httpx.TokenExpired()
	}
	if !auth.VerifyPassword(oldPassword, u.PasswordSalt, u.PasswordHash) {
		return httpx.NewError(httpx.CodePasswordMismatch, "旧密码错误")
	}
	return s.setPassword(ctx, uid, newPassword)
}

// ---------- 二步验证(TOTP) ----------

// TwoFactorSetup 生成一把待确认的密钥,返回给前端出二维码;10 分钟内不确认即作废,不影响现有设置。
func (s *Service) TwoFactorSetup(ctx context.Context, sess *auth.Session) (secret, uri string, err error) {
	secret, err = auth.NewTOTPSecret()
	if err != nil {
		return "", "", err
	}
	s.totpMu.Lock()
	s.totpPending[sess.UID] = pendingTOTP{secret: secret, exp: time.Now().Add(10 * time.Minute)}
	s.totpMu.Unlock()
	return secret, auth.TOTPURI(totpIssuer, sess.Username, secret), nil
}

// TwoFactorEnable 用认证器当前验证码确认绑定,确认通过才把密钥落库。
func (s *Service) TwoFactorEnable(ctx context.Context, sess *auth.Session, code string) error {
	s.totpMu.Lock()
	p, ok := s.totpPending[sess.UID]
	s.totpMu.Unlock()
	if !ok || time.Now().After(p.exp) {
		return httpx.NewError(httpx.CodeOperationFailed, "绑定已过期,请重新生成二维码")
	}
	if !auth.VerifyTOTP(p.secret, code, time.Now()) {
		return httpx.NewError(httpx.CodeOtpInvalid, "验证码错误,请确认手机时间准确后重试")
	}
	if err := s.repo.UpdateTwoStepSecret(ctx, sess.UID, p.secret); err != nil {
		return err
	}
	s.totpMu.Lock()
	delete(s.totpPending, sess.UID)
	s.totpMu.Unlock()
	return nil
}

// TwoFactorDisable 本人关闭二步验证:只认当前验证码(密码不能当替代,否则偷到 token + 密码就能拆掉第二道锁);
// 认证器丢失走管理员 ResetTwoFactor。
func (s *Service) TwoFactorDisable(ctx context.Context, sess *auth.Session, code string) error {
	u, err := s.repo.UserByUID(ctx, sess.UID)
	if err != nil {
		return err
	}
	if u == nil {
		return httpx.TokenExpired()
	}
	if u.TwoStepSecret == "" {
		return nil
	}
	if !auth.VerifyTOTP(u.TwoStepSecret, code, time.Now()) {
		return httpx.NewError(httpx.CodeOtpInvalid, "验证码错误或已过期")
	}
	return s.repo.UpdateTwoStepSecret(ctx, sess.UID, "")
}

// ResetTwoFactor 管理员为他人重置(清空)二步验证,用于用户丢失认证器;重置后对方下次登录只需密码。
// 只能重置权限不高于自己的账号。
func (s *Service) ResetTwoFactor(ctx context.Context, op *auth.Session, username string) error {
	u, err := s.repo.UserByUsername(ctx, username)
	if err != nil {
		return err
	}
	if u == nil {
		return httpx.NewError(httpx.CodeUserNotExist, "用户不存在")
	}
	if err := s.mustManage(ctx, op, u); err != nil {
		return err
	}
	if err := s.repo.UpdateTwoStepSecret(ctx, u.UID, ""); err != nil {
		return err
	}
	s.sessions.InvalidateUser(u.UID)
	return nil
}

// LockUser 锁定 / 解锁账号(until=0 解锁)。不能锁自己,也不能动权限高于自己的账号。
func (s *Service) LockUser(ctx context.Context, op *auth.Session, username string, until int64) error {
	u, err := s.repo.UserByUsername(ctx, username)
	if err != nil {
		return err
	}
	if u == nil {
		return httpx.NewError(httpx.CodeUserNotExist, "用户不存在")
	}
	if until != 0 && u.UID == op.UID {
		return errSelfLock
	}
	if err := s.mustManage(ctx, op, u); err != nil {
		return err
	}
	if err := s.repo.UpdateLock(ctx, username, until); err != nil {
		return err
	}
	s.sessions.InvalidateUser(u.UID)
	return nil
}

func (s *Service) setPassword(ctx context.Context, uid int64, password string) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	salt := auth.NewSalt()
	return s.repo.UpdatePassword(ctx, uid, auth.HashPassword(password, salt), salt)
}

func validatePassword(p string) error {
	if len(p) < 5 || len(p) > 30 {
		return httpx.BadRequest("密码长度须为 5~30 位")
	}
	return nil
}

func validateUsername(u string) error {
	u = strings.TrimSpace(u)
	if u == "" || len(u) > 32 {
		return httpx.BadRequest("用户名不能为空且不超过 32 位")
	}
	return nil
}

// protectedAccounts 不允许改角色/删除的内置账号。
func protectedAccount(username string) bool {
	return username == "admin" || username == "root"
}

// CreateUser 新建管理员;分配的角色不能超出操作者自己的权限范围。
func (s *Service) CreateUser(ctx context.Context, op *auth.Session, u *User, password string) (*User, error) {
	if err := validateUsername(u.Username); err != nil {
		return nil, err
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	if err := s.ensureRolesExist(ctx, u.Roles); err != nil {
		return nil, err
	}
	if ok, err := s.coversRoles(ctx, op, u.Roles); err != nil {
		return nil, err
	} else if !ok {
		return nil, errBeyondScope
	}
	u.PasswordSalt = auth.NewSalt()
	u.PasswordHash = auth.HashPassword(password, u.PasswordSalt)
	uid, err := s.repo.CreateUser(ctx, u)
	if err != nil {
		return nil, err
	}
	return s.repo.UserByUID(ctx, uid)
}

// UpdateUser 更新管理员;password 非空时同时重置密码。
// 边界:只能管理权限不高于自己的账号;不能改自己的角色;新角色不能超出自己的权限范围;不能锁定自己。
func (s *Service) UpdateUser(ctx context.Context, op *auth.Session, u *User, password string) (*User, error) {
	existing, err := s.repo.UserByUID(ctx, u.UID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, httpx.NewError(httpx.CodeUserNotExist, "用户不存在")
	}
	if err := validateUsername(u.Username); err != nil {
		return nil, err
	}
	if err := s.mustManage(ctx, op, existing); err != nil {
		return nil, err
	}
	if protectedAccount(existing.Username) {
		u.Username = existing.Username
		u.Roles = existing.Roles
	}
	if u.Roles == nil {
		u.Roles = existing.Roles
	}
	if u.UID == op.UID {
		if !sameRoles(u.Roles, existing.Roles) {
			return nil, errSelfRoles
		}
		if u.LockUntilTick > time.Now().UnixMilli() && u.LockUntilTick != existing.LockUntilTick {
			return nil, errSelfLock
		}
	}
	if err := s.ensureRolesExist(ctx, u.Roles); err != nil {
		return nil, err
	}
	if ok, err := s.coversRoles(ctx, op, u.Roles); err != nil {
		return nil, err
	} else if !ok {
		return nil, errBeyondScope
	}
	if err := s.repo.UpdateUser(ctx, u); err != nil {
		return nil, err
	}
	if password != "" {
		if err := s.setPassword(ctx, u.UID, password); err != nil {
			return nil, err
		}
	}
	s.sessions.InvalidateUser(u.UID)
	return s.repo.UserByUID(ctx, u.UID)
}

// DeleteUser 删除管理员(内置账号、自己、权限高于自己的账号不可删)。
func (s *Service) DeleteUser(ctx context.Context, operator *auth.Session, uid int64) error {
	u, err := s.repo.UserByUID(ctx, uid)
	if err != nil {
		return err
	}
	if u == nil {
		return nil
	}
	if protectedAccount(u.Username) || uid == operator.UID {
		return httpx.NewError(httpx.CodeOperationFailed, "该账号不允许删除")
	}
	if err := s.mustManage(ctx, operator, u); err != nil {
		return err
	}
	s.sessions.InvalidateUser(uid)
	return s.repo.DeleteUser(ctx, uid)
}

// UpdateRoles 修改用户角色:不能改自己的;目标账号与新角色都不能超出操作者的权限范围。
func (s *Service) UpdateRoles(ctx context.Context, op *auth.Session, username string, roles []string) error {
	u, err := s.repo.UserByUsername(ctx, username)
	if err != nil {
		return err
	}
	if u == nil {
		return httpx.NewError(httpx.CodeUserNotExist, "用户不存在")
	}
	if protectedAccount(username) {
		return httpx.NewError(httpx.CodeOperationFailed, "内置账号不允许修改角色")
	}
	if u.UID == op.UID {
		return errSelfRoles
	}
	if err := s.mustManage(ctx, op, u); err != nil {
		return err
	}
	if err := s.ensureRolesExist(ctx, roles); err != nil {
		return err
	}
	if ok, err := s.coversRoles(ctx, op, roles); err != nil {
		return err
	} else if !ok {
		return errBeyondScope
	}
	if err := s.repo.UpdateRoles(ctx, u.UID, roles); err != nil {
		return err
	}
	s.sessions.InvalidateUser(u.UID)
	return nil
}

func (s *Service) ensureRolesExist(ctx context.Context, roles []string) error {
	if len(roles) == 0 {
		return nil
	}
	list, err := s.repo.RolesByNames(ctx, roles)
	if err != nil {
		return err
	}
	found := map[string]bool{}
	for _, rl := range list {
		found[rl.Role] = true
	}
	for _, r := range roles {
		if !found[r] {
			return httpx.BadRequest("角色不存在: " + r)
		}
	}
	return nil
}

// SaveRole 新建或更新角色,并让相关会话失效。
// 边界:ADMIN 角色只有超级管理员能改;新权限树不能超出操作者自己的权限;
// 更新时原角色的权限也不能超出操作者(否则低权限的人可以削掉高权限角色把别人锁在外面)。
func (s *Service) SaveRole(ctx context.Context, op *auth.Session, rl *Role, create bool) error {
	rl.Role = strings.TrimSpace(rl.Role)
	if rl.Role == "" {
		return httpx.BadRequest("角色代码不能为空")
	}
	if len(rl.PermissionTree) == 0 {
		rl.PermissionTree = []byte(`{"code":"","wildcard":false,"children":{}}`)
	}
	newTree, err := auth.ParseTree(string(rl.PermissionTree))
	if err != nil {
		return httpx.BadRequest("权限树 JSON 非法: " + err.Error())
	}
	if rl.Role == "ADMIN" && !op.IsSuperAdmin() {
		return httpx.NewError(httpx.CodePermissionDenied, "ADMIN 角色只有超级管理员可以修改")
	}
	if !op.IsSuperAdmin() {
		if !op.Tree.Covers(newTree) {
			return errBeyondScope
		}
		if !create {
			existing, err := s.repo.RoleByName(ctx, rl.Role)
			if err != nil {
				return err
			}
			if existing != nil {
				oldTree, err := auth.ParseTree(string(existing.PermissionTree))
				if err == nil && !op.Tree.Covers(oldTree) {
					return errManageHigher
				}
			}
		}
	}
	if create {
		err = s.repo.CreateRole(ctx, rl)
	} else {
		err = s.repo.UpdateRole(ctx, rl)
	}
	if err != nil {
		return err
	}
	s.sessions.InvalidateRole(rl.Role)
	return nil
}

// DeleteRole 删除角色(ADMIN 不可删;权限超出操作者的角色不可删)。
func (s *Service) DeleteRole(ctx context.Context, op *auth.Session, role string) error {
	if role == "ADMIN" {
		return httpx.NewError(httpx.CodeOperationFailed, "ADMIN 角色不允许删除")
	}
	if !op.IsSuperAdmin() {
		existing, err := s.repo.RoleByName(ctx, role)
		if err != nil {
			return err
		}
		if existing != nil {
			oldTree, err := auth.ParseTree(string(existing.PermissionTree))
			if err == nil && !op.Tree.Covers(oldTree) {
				return errManageHigher
			}
		}
	}
	if err := s.repo.DeleteRole(ctx, role); err != nil {
		return err
	}
	s.sessions.InvalidateRole(role)
	return nil
}
