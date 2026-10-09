package authcenter

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"cow-manager-backend-go/internal/auth"
	"cow-manager-backend-go/internal/httpx"
)

// Service 登录与会话逻辑;同时实现 auth.Loader。
type Service struct {
	repo       *Repo
	tokenTTL   time.Duration
	refreshTTL time.Duration
	sessions   *auth.Manager
}

// NewService 创建服务;sessions 在 New 之后由 main 注入(存在相互依赖)。
func NewService(db *sqlx.DB, tokenTTL, refreshTTL time.Duration) *Service {
	return &Service{repo: &Repo{db: db}, tokenTTL: tokenTTL, refreshTTL: refreshTTL}
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
func (s *Service) Login(ctx context.Context, username, password string) (*User, error) {
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

// CreateUser 新建管理员。
func (s *Service) CreateUser(ctx context.Context, u *User, password string) (*User, error) {
	if err := validateUsername(u.Username); err != nil {
		return nil, err
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	if err := s.ensureRolesExist(ctx, u.Roles); err != nil {
		return nil, err
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
func (s *Service) UpdateUser(ctx context.Context, u *User, password string) (*User, error) {
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
	if protectedAccount(existing.Username) {
		u.Username = existing.Username
		u.Roles = existing.Roles
	}
	if err := s.ensureRolesExist(ctx, u.Roles); err != nil {
		return nil, err
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

// DeleteUser 删除管理员(内置账号与自己不可删)。
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
	s.sessions.InvalidateUser(uid)
	return s.repo.DeleteUser(ctx, uid)
}

// UpdateRoles 修改用户角色。
func (s *Service) UpdateRoles(ctx context.Context, username string, roles []string) error {
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
	if err := s.ensureRolesExist(ctx, roles); err != nil {
		return err
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
func (s *Service) SaveRole(ctx context.Context, rl *Role, create bool) error {
	rl.Role = strings.TrimSpace(rl.Role)
	if rl.Role == "" {
		return httpx.BadRequest("角色代码不能为空")
	}
	if len(rl.PermissionTree) == 0 {
		rl.PermissionTree = []byte(`{"code":"","wildcard":false,"children":{}}`)
	}
	if _, err := auth.ParseTree(string(rl.PermissionTree)); err != nil {
		return httpx.BadRequest("权限树 JSON 非法: " + err.Error())
	}
	var err error
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

// DeleteRole 删除角色。
func (s *Service) DeleteRole(ctx context.Context, role string) error {
	if role == "ADMIN" {
		return httpx.NewError(httpx.CodeOperationFailed, "ADMIN 角色不允许删除")
	}
	if err := s.repo.DeleteRole(ctx, role); err != nil {
		return err
	}
	s.sessions.InvalidateRole(role)
	return nil
}
