// Package portaluser 是官网用户(以钱包地址为主键)的管理视图:把官网库(cow-portal)、
// 游戏库(ranch_game)、日志库(ranch_log)里与一个地址有关的数据汇到一处查看。
//
// 与 portalinvite 一样属于官网业务,临时挂在 GMS。只依赖 httpx / auth / perm / query 与
// 官网公开接口客户端 portalapi;库连接、权限码、路由前缀 /portal/users 都在本包内声明,
// 迁走时整个目录带走即可。
package portaluser

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"cow-manager-backend/internal/auth"
	"cow-manager-backend/internal/httpx"
	"cow-manager-backend/internal/perm"
	"cow-manager-backend/internal/portalapi"
	"cow-manager-backend/internal/query"
)

// 权限:只读 = feature/portal-user/view(含导出);分配邀请码等 = feature/portal-user/edit。
var (
	PermView   = perm.PortalUserView
	PermExport = perm.PortalUserView
	PermManage = perm.PortalUserEdit
)

// Service 官网用户服务。
type Service struct {
	portal *sqlx.DB // cow-portal
	game   *sqlx.DB // ranch_game(可为 nil)
	logdb  *sqlx.DB // ranch_log(可为 nil)
	// gameSchema 官网库与游戏库同实例时为游戏库名,列表里可以直接 JOIN;否则为空
	gameSchema   string
	imageHosting string
	siteURL      string
	api          *portalapi.Client
	loc          *time.Location
	labeler      query.EnumLabeler
	listSpec     *query.Spec
}

// NewService 创建服务。
func NewService(portal, game, logdb *sqlx.DB, gameSchema, imageHosting, siteURL string, api *portalapi.Client, loc *time.Location, labeler query.EnumLabeler) *Service {
	s := &Service{portal: portal, game: game, logdb: logdb, gameSchema: gameSchema, imageHosting: imageHosting, siteURL: siteURL, api: api, loc: loc, labeler: labeler}
	s.listSpec = buildListSpec(gameSchema)
	return s
}

// ---------------------------------------------------------------------------
// 列表:u_profile 为基表(官网里凡是连过钱包的地址都有一行),LEFT JOIN 邀请 / 星球 / 游戏账号
// ---------------------------------------------------------------------------

const planetIDSub = "(SELECT pm.planet_id FROM u_planet_member pm WHERE pm.player = p.address ORDER BY pm.id DESC LIMIT 1)"

func buildListSpec(gameSchema string) *query.Spec {
	from := "u_profile p LEFT JOIN u_invite_user i ON i.address = p.address"
	cols := []query.Column{
		{Name: "address", Label: "地址", Kind: query.String, Filter: true, Expr: "p.address"},
		{Name: "name", Label: "昵称", Kind: query.String, Filter: true, Expr: "CASE WHEN p.name = p.address THEN '' ELSE p.name END"},
		{Name: "avatar", Label: "头像编号", Kind: query.String, Expr: "p.avatar"},
		{Name: "gender", Label: "性别", Kind: query.Int, Filter: true, Expr: "p.gender"},
		{Name: "invite_code", Label: "邀请码", Kind: query.String, Filter: true, Expr: "COALESCE(i.code, '')"},
		{Name: "inviter", Label: "邀请人", Kind: query.String, Filter: true, Expr: "COALESCE(i.inviter, '')"},
		{Name: "points", Label: "积分", Kind: query.Int, Filter: true, Sort: true, Expr: "COALESCE(i.points, 0)"},
		{Name: "invite_valid", Label: "有效邀请", Kind: query.Int, Sort: true, Expr: "COALESCE(i.invite_valid, 0)"},
		{Name: "cattle_count", Label: "牛牛数", Kind: query.Int, Filter: true, Sort: true, Expr: "(SELECT COUNT(*) FROM u_cattle c WHERE c.owner = p.address)"},
		{Name: "planet_id", Label: "所在星球", Kind: query.Int, Filter: true, Expr: "COALESCE(" + planetIDSub + ", 0)"},
		{Name: "planet_name", Label: "星球名", Kind: query.String, Expr: "COALESCE((SELECT pl.name FROM u_planet pl WHERE pl.id = " + planetIDSub + "), '')"},
		{Name: "planets_owned", Label: "名下星球", Kind: query.Int, Sort: true, Expr: "(SELECT COUNT(*) FROM u_planet x WHERE x.master = p.address)"},
		{Name: "listings", Label: "在售挂单", Kind: query.Int, Sort: true, Expr: "(SELECT COUNT(*) FROM u_market m WHERE m.seller = p.address)"},
		{Name: "social", Label: "社交绑定", Kind: query.String, Filter: true, Expr: "COALESCE((SELECT GROUP_CONCAT(s.platform ORDER BY s.platform) FROM u_social_account s WHERE s.address = p.address AND s.status IN ('bound','verified')), '')"},
		{Name: "beta_claims", Label: "公测领取次数", Kind: query.Int, Sort: true, Expr: "(SELECT COUNT(*) FROM u_beta_claim_log b WHERE b.address = p.address)"},
		{Name: "update_at", Label: "资料更新", Kind: query.String, Sort: true, Expr: "p.update_at"},
	}
	if gameSchema != "" {
		from += " LEFT JOIN `" + gameSchema + "`.ranch_user g ON g.address = LOWER(p.address)"
		cols = append(cols,
			query.Column{Name: "game_uid", Label: "游戏UID", Kind: query.Int, Filter: true, Sort: true, Expr: "COALESCE(g.uid, 0)"},
			query.Column{Name: "game_guild", Label: "游戏公会", Kind: query.Int, Filter: true, Expr: "COALESCE(g.guildId, 0)"},
			query.Column{Name: "register_ts", Label: "游戏注册", Kind: query.TsSec, Filter: true, Sort: true, Expr: "COALESCE(g.registerTs, 0)"},
			query.Column{Name: "last_login_ip", Label: "最后登录IP", Kind: query.String, Filter: true, Expr: "COALESCE(g.lastLoginIP, '')"},
			query.Column{Name: "last_logout_ts", Label: "最后登出", Kind: query.TsSec, Filter: true, Sort: true, Expr: "COALESCE(g.lastLogoutTs, 0)"},
		)
	}
	return &query.Spec{Table: "u_profile", From: from, Columns: cols, DefaultSort: "COALESCE(i.points, 0) DESC, p.update_at DESC, p.address ASC"}
}

// Register 挂载路由。
func (s *Service) Register(mux *http.ServeMux) {
	h := query.Handlers{Spec: s.listSpec, DB: s.portal, Loc: s.loc, Labeler: s.labeler}
	mux.HandleFunc("GET /portal/users", httpx.H(auth.Require(PermView, s.list(h))))
	mux.HandleFunc("GET /portal/users/export", httpx.H(auth.Require(PermExport, h.Export)))
	mux.HandleFunc("GET /portal/users/stats", httpx.H(auth.Require(PermView, s.stats)))
	mux.HandleFunc("GET /portal/users/{address}", httpx.H(auth.Require(PermView, s.detail)))
	mux.HandleFunc("POST /portal/users/{address}/invite-code", httpx.H(auth.Require(PermManage, s.ensureInviteCode)))
}

// list 在通用分页之上把头像编号解析成 URL。
func (s *Service) list(h query.Handlers) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		page, size := httpx.Pagination(r)
		p, err := h.Spec.Page(r.Context(), h.DB, r.URL.Query(), page, size)
		if err != nil {
			return err
		}
		if rows, ok := p.Content.([]query.Row); ok {
			for _, row := range rows {
				code, _ := row["avatar"].(string)
				row["avatar_url"] = s.avatarURL(r.Context(), code)
			}
		}
		httpx.OK(w, p)
		return nil
	}
}

// defaultAvatarPath 官网前端的默认头像(未设置头像时门户也显示这张)。
const defaultAvatarPath = "/picture/profile/0.png"

// avatarURL 头像编号 → 图床 URL。官网规则:纯数字编号查 s_profile_photo;"Bovine Hero #<id>" 走牛牛头像,这里不解析。
// 没设置或解析不出来都回落到默认头像,避免前端用首字母占位(地址都以 0x 开头,会满屏 "0")。
func (s *Service) avatarURL(ctx context.Context, code string) string {
	fallback := s.imageHosting + defaultAvatarPath
	code = strings.TrimSpace(code)
	if code == "" {
		return fallback
	}
	id, err := strconv.ParseUint(code, 10, 32)
	if err != nil {
		return fallback
	}
	var image string
	if err := s.portal.GetContext(ctx, &image, "SELECT image FROM s_profile_photo WHERE id = ?", id); err != nil || image == "" {
		return fallback
	}
	return s.imageHosting + image
}

// Stats 用户池概览。
type Stats struct {
	Profiles     int64 `json:"profiles"`     // 连过钱包的地址数
	Named        int64 `json:"named"`        // 改过昵称
	WithInvite   int64 `json:"withInvite"`   // 领过邀请码
	WithCattle   int64 `json:"withCattle"`   // 持有牛牛
	InPlanet     int64 `json:"inPlanet"`     // 已加入星球
	WithSocial   int64 `json:"withSocial"`   // 绑过社交账号
	BetaClaimers int64 `json:"betaClaimers"` // 领过公测资产
	GameUsers    int64 `json:"gameUsers"`    // 游戏账号数(游戏库不可用时为 -1)
	UpdatedToday int64 `json:"updatedToday"` // 今日有资料更新(u_profile 没有创建时间,只能看 update_at)
}

func (s *Service) stats(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	out := &Stats{GameUsers: -1}
	today := time.Now().In(s.loc).Format("2006-01-02")
	q := func(dst *int64, sqlStr string, args ...any) error {
		return s.portal.GetContext(ctx, dst, sqlStr, args...)
	}
	steps := []func() error{
		func() error { return q(&out.Profiles, "SELECT COUNT(*) FROM u_profile") },
		func() error {
			return q(&out.Named, "SELECT COUNT(*) FROM u_profile WHERE name <> address AND name <> ''")
		},
		func() error { return q(&out.WithInvite, "SELECT COUNT(*) FROM u_invite_user") },
		func() error { return q(&out.WithCattle, "SELECT COUNT(DISTINCT owner) FROM u_cattle") },
		func() error { return q(&out.InPlanet, "SELECT COUNT(DISTINCT player) FROM u_planet_member") },
		func() error {
			return q(&out.WithSocial, "SELECT COUNT(DISTINCT address) FROM u_social_account WHERE status IN ('bound','verified')")
		},
		func() error { return q(&out.BetaClaimers, "SELECT COUNT(DISTINCT address) FROM u_beta_claim_log") },
		func() error {
			return q(&out.UpdatedToday, "SELECT COUNT(*) FROM u_profile WHERE DATE(update_at) = ?", today)
		},
	}
	for _, st := range steps {
		if err := st(); err != nil {
			return err
		}
	}
	if s.game != nil {
		_ = s.game.GetContext(ctx, &out.GameUsers, "SELECT COUNT(*) FROM ranch_user")
	}
	httpx.OK(w, out)
	return nil
}

// ---------------------------------------------------------------------------
// 生成邀请码:给还没来过 /betaInvite 的用户(如 KOL)预先分配一个,规则与官网一致
// ---------------------------------------------------------------------------

const codeAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

func genCode() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, 12)
	for i, b := range buf {
		out[i] = codeAlphabet[int(b)%len(codeAlphabet)]
	}
	return string(out), nil
}

func (s *Service) ensureInviteCode(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	address, err := normAddress(r.PathValue("address"))
	if err != nil {
		return err
	}
	var exists int
	if err := s.portal.GetContext(ctx, &exists, "SELECT COUNT(*) FROM u_profile WHERE address = ?", address); err != nil {
		return err
	}
	if exists == 0 {
		return httpx.NotFound("该地址没有官网资料(从未连接过钱包)")
	}
	var code string
	err = s.portal.GetContext(ctx, &code, "SELECT code FROM u_invite_user WHERE address = ?", address)
	if err == nil {
		httpx.OK(w, map[string]any{"code": code, "created": false, "link": s.siteURL + "/betaInvite?ref=" + code})
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for i := 0; i < 5; i++ {
		c, err := genCode()
		if err != nil {
			return err
		}
		res, err := s.portal.ExecContext(ctx, "INSERT IGNORE INTO u_invite_user (address, code) VALUES (?, ?)", address, c)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			httpx.OK(w, map[string]any{"code": c, "created": true, "link": s.siteURL + "/betaInvite?ref=" + c})
			return nil
		}
		// 并发下地址已被建好:直接返回已有的
		if err := s.portal.GetContext(ctx, &code, "SELECT code FROM u_invite_user WHERE address = ?", address); err == nil {
			httpx.OK(w, map[string]any{"code": code, "created": false, "link": s.siteURL + "/betaInvite?ref=" + code})
			return nil
		}
	}
	return fmt.Errorf("生成邀请码失败,请重试")
}

func isAddress(s string) bool {
	return len(s) == 42 && strings.HasPrefix(strings.ToLower(s), "0x")
}

func normAddress(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !isAddress(s) {
		return "", httpx.BadRequest("地址格式不正确")
	}
	return s, nil
}
