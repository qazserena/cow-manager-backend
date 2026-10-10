// Package portalinvite 是官网公测邀请计划(cow-portal 库 u_invite_*)的管理与报表模块。
//
// 这块业务属于官网(cow-portal-backend),目前官网没有管理后端,临时挂在 GMS 里。
// 为了将来整体迁走,模块只依赖 GMS 的通用小件(httpx / auth / perm / query),
// 不碰其它业务模块;数据库连接、权限码、路由前缀 /portal/invite 全部在本包内声明。
//
// 积分口径(各档分值、里程碑、等级门槛)的事实来源是官网后端
// cow-portal-backend/internal/model/invite_program.go,本模块只读数据、做管理动作,
// 不复制那套常量;等级划分在 levelOf 里只用于报表分桶,与官网保持同一组门槛。
package portalinvite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"cow-manager-backend/internal/auth"
	"cow-manager-backend/internal/httpx"
	"cow-manager-backend/internal/perm"
	"cow-manager-backend/internal/portalapi"
	"cow-manager-backend/internal/query"
)

// 权限:只读 = feature/portal-invite/view(含导出);标记 / 恢复 / 调分 = feature/portal-invite/edit。
var (
	PermView   = perm.PortalInviteView
	PermExport = perm.PortalInviteView
	PermFlag   = perm.PortalInviteEdit
	PermAdjust = perm.PortalInviteEdit
)

// 管理动作写入 u_invite_point_log 的 kind;官网结算只认 bind/activate/valid/joined/milestone,不会碰这类流水。
const kindAdminAdjust = "admin_adjust"

// 管理员手动标记的 flag 值;官网侧只认 flag 是否为空,所以可以安全扩展。
const flagManual = "manual"

// Service 官网邀请计划管理服务。
type Service struct {
	db      *sqlx.DB // cow-portal 库
	api     *portalapi.Client
	loc     *time.Location
	labeler query.EnumLabeler
}

// NewService 创建服务。api 用来拉官网的积分口径(等级门槛等),保证报表分桶与官网一致。
func NewService(portal *sqlx.DB, api *portalapi.Client, loc *time.Location, labeler query.EnumLabeler) *Service {
	return &Service{db: portal, api: api, loc: loc, labeler: labeler}
}

// levelOf 积分对应等级(门槛来自官网 /invite/rules)。
func (s *Service) levelOf(ctx context.Context, points int64) int {
	return s.api.LevelOf(ctx, points).Level
}

// ---------------------------------------------------------------------------
// 只读列表(复用 query.Spec:过滤 / 排序 / 分页 / CSV)
// ---------------------------------------------------------------------------

// UserSpec 邀请用户表 u_invite_user。
var UserSpec = &query.Spec{
	Table: "u_invite_user", DefaultSort: "`points` DESC, `points_at` ASC",
	Columns: []query.Column{
		{Name: "address", Label: "地址", Kind: query.String, Filter: true},
		{Name: "code", Label: "邀请码", Kind: query.String, Filter: true},
		{Name: "inviter", Label: "邀请人", Kind: query.String, Filter: true},
		{Name: "bind_ip", Label: "绑定 IP", Kind: query.String, Filter: true},
		{Name: "bind_at", Label: "绑定时间", Kind: query.String, Filter: true, Sort: true},
		{Name: "flag", Label: "标记", Kind: query.String, Filter: true},
		{Name: "stage", Label: "阶段", Kind: query.Int, Filter: true, Sort: true},
		{Name: "points", Label: "积分", Kind: query.Int, Filter: true, Sort: true},
		{Name: "points_at", Label: "最近加分", Kind: query.String, Sort: true},
		{Name: "invite_total", Label: "邀请人数", Kind: query.Int, Filter: true, Sort: true},
		{Name: "invite_valid", Label: "有效邀请", Kind: query.Int, Filter: true, Sort: true},
		{Name: "created_at", Label: "首次访问", Kind: query.String, Filter: true, Sort: true},
	},
}

// PointLogSpec 积分流水 u_invite_point_log。
var PointLogSpec = &query.Spec{
	Table: "u_invite_point_log", DefaultSort: "`id` DESC",
	Columns: []query.Column{
		{Name: "id", Label: "ID", Kind: query.Int, Filter: true, Sort: true},
		{Name: "address", Label: "地址", Kind: query.String, Filter: true},
		{Name: "kind", Label: "类型", Kind: query.String, Filter: true},
		{Name: "ref", Label: "关联", Kind: query.String, Filter: true},
		{Name: "points", Label: "积分", Kind: query.Int, Filter: true, Sort: true},
		{Name: "created_at", Label: "时间", Kind: query.String, Filter: true, Sort: true},
	},
}

// AdminLogSpec 管理操作日志 u_invite_admin_log。
var AdminLogSpec = &query.Spec{
	Table: "u_invite_admin_log", DefaultSort: "`id` DESC",
	Columns: []query.Column{
		{Name: "id", Label: "ID", Kind: query.Int, Sort: true},
		{Name: "admin_uid", Label: "管理员ID", Kind: query.Int, Filter: true},
		{Name: "admin_name", Label: "管理员", Kind: query.String, Filter: true},
		{Name: "action", Label: "动作", Kind: query.String, Filter: true},
		{Name: "target", Label: "对象地址", Kind: query.String, Filter: true},
		{Name: "points", Label: "积分变动", Kind: query.Int, Sort: true},
		{Name: "reason", Label: "原因", Kind: query.String},
		{Name: "created_at", Label: "时间", Kind: query.String, Filter: true, Sort: true},
	},
}

// Register 挂载路由。
func (s *Service) Register(mux *http.ServeMux) {
	s.mountList(mux, "/portal/invite/users", UserSpec)
	s.mountList(mux, "/portal/invite/point-logs", PointLogSpec)
	s.mountList(mux, "/portal/invite/admin-logs", AdminLogSpec)

	mux.HandleFunc("GET /portal/invite/rules", httpx.H(auth.Require(PermView, s.rules)))
	mux.HandleFunc("GET /portal/invite/overview", httpx.H(auth.Require(PermView, s.overview)))
	mux.HandleFunc("GET /portal/invite/trend", httpx.H(auth.Require(PermView, s.trend)))
	mux.HandleFunc("GET /portal/invite/leaderboard", httpx.H(auth.Require(PermView, s.leaderboard)))
	mux.HandleFunc("GET /portal/invite/ips", httpx.H(auth.Require(PermView, s.ipReport)))
	mux.HandleFunc("GET /portal/invite/users/{address}", httpx.H(auth.Require(PermView, s.userDetail)))

	mux.HandleFunc("POST /portal/invite/users/{address}/flag", httpx.H(auth.Require(PermFlag, s.flag)))
	mux.HandleFunc("POST /portal/invite/users/{address}/unflag", httpx.H(auth.Require(PermFlag, s.unflag)))
	mux.HandleFunc("POST /portal/invite/users/{address}/adjust", httpx.H(auth.Require(PermAdjust, s.adjust)))
}

func (s *Service) mountList(mux *http.ServeMux, prefix string, spec *query.Spec) {
	h := query.Handlers{Spec: spec, DB: s.db, Loc: s.loc, Labeler: s.labeler}
	mux.HandleFunc("GET "+prefix, httpx.H(auth.Require(PermView, h.List)))
	mux.HandleFunc("GET "+prefix+"/export", httpx.H(auth.Require(PermExport, h.Export)))
}

// ---------------------------------------------------------------------------
// 概览 / 趋势 / 榜单 / IP
// ---------------------------------------------------------------------------

// Overview 概览数字。
type Overview struct {
	Participants   int64    `json:"participants"`   // 领过邀请码的地址数
	Scorers        int64    `json:"scorers"`        // 有积分的地址数
	Bound          int64    `json:"bound"`          // 已绑定邀请人的地址数(含被标记)
	Activated      int64    `json:"activated"`      // 未标记且已进游戏
	Valid          int64    `json:"valid"`          // 未标记且已完成任务(有效邀请)
	Flagged        int64    `json:"flagged"`        // 被标记(不计分)
	PointsTotal    int64    `json:"pointsTotal"`    // 累计发放积分(流水净额)
	PointsToday    int64    `json:"pointsToday"`    // 今日发放
	BindsToday     int64    `json:"bindsToday"`     // 今日新绑定
	ActiveInviters int64    `json:"activeInviters"` // 至少有 1 个未标记被邀请人的邀请人数
	LevelBuckets   []Bucket `json:"levelBuckets"`   // 等级分布(有积分的地址)
	FlagBuckets    []Bucket `json:"flagBuckets"`    // 标记原因分布
	StageBuckets   []Bucket `json:"stageBuckets"`   // 已绑定者阶段分布(未标记)
	GeneratedAt    int64    `json:"generatedAt"`
}

// Bucket 分桶计数。
type Bucket struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// RulesInfo 积分口径 + 来源标记(前端据此显示等级名与门槛,不写死)。
type RulesInfo struct {
	*portalapi.InviteRules
	FromPortal bool   `json:"fromPortal"`
	Source     string `json:"source"`
}

func (s *Service) rules(w http.ResponseWriter, r *http.Request) error {
	rules := s.api.Rules(r.Context())
	httpx.OK(w, &RulesInfo{InviteRules: rules, FromPortal: s.api.RulesFromPortal(), Source: s.api.Base() + "/invite/rules"})
	return nil
}

func (s *Service) overview(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	out := &Overview{GeneratedAt: time.Now().Unix()}
	today := time.Now().In(s.loc).Format("2006-01-02")

	type agg struct {
		Participants   int64 `db:"participants"`
		Scorers        int64 `db:"scorers"`
		Bound          int64 `db:"bound"`
		Activated      int64 `db:"activated"`
		Valid          int64 `db:"valid"`
		Flagged        int64 `db:"flagged"`
		BindsToday     int64 `db:"binds_today"`
		ActiveInviters int64 `db:"active_inviters"`
	}
	a := &agg{}
	if err := s.db.GetContext(ctx, a, `
		SELECT
		  COUNT(*)                                                     AS participants,
		  COALESCE(SUM(points > 0), 0)                                 AS scorers,
		  COALESCE(SUM(inviter <> ''), 0)                              AS bound,
		  COALESCE(SUM(inviter <> '' AND flag = '' AND stage >= 1), 0) AS activated,
		  COALESCE(SUM(inviter <> '' AND flag = '' AND stage >= 2), 0) AS valid,
		  COALESCE(SUM(inviter <> '' AND flag <> ''), 0)               AS flagged,
		  COALESCE(SUM(inviter <> '' AND DATE(bind_at) = ?), 0)        AS binds_today,
		  (SELECT COUNT(DISTINCT inviter) FROM u_invite_user WHERE inviter <> '' AND flag = '') AS active_inviters
		FROM u_invite_user`, today); err != nil {
		return err
	}
	out.Participants, out.Scorers, out.Bound = a.Participants, a.Scorers, a.Bound
	out.Activated, out.Valid, out.Flagged = a.Activated, a.Valid, a.Flagged
	out.BindsToday, out.ActiveInviters = a.BindsToday, a.ActiveInviters

	type pts struct {
		Total int64 `db:"total"`
		Today int64 `db:"today"`
	}
	p := &pts{}
	if err := s.db.GetContext(ctx, p, `
		SELECT COALESCE(SUM(points), 0) AS total,
		       COALESCE(SUM(CASE WHEN DATE(created_at) = ? THEN points ELSE 0 END), 0) AS today
		FROM u_invite_point_log`, today); err != nil {
		return err
	}
	out.PointsTotal, out.PointsToday = p.Total, p.Today

	// 等级分布:门槛从官网 /invite/rules 拉取(缓存 10 分钟,失败用兜底值),CASE 表达式按门槛动态生成
	var levels []struct {
		Level int   `db:"lv"`
		Count int64 `db:"cnt"`
	}
	if err := s.db.SelectContext(ctx, &levels, `
		SELECT `+s.api.LevelCaseSQL(ctx, "points")+` AS lv, COUNT(*) AS cnt
		FROM u_invite_user WHERE points > 0 GROUP BY lv ORDER BY lv`); err != nil {
		return err
	}
	ruleLevels := s.api.Rules(ctx).Levels
	out.LevelBuckets = make([]Bucket, 0, len(ruleLevels))
	byLevel := map[int]int64{}
	for _, l := range levels {
		byLevel[l.Level] = l.Count
	}
	for _, lv := range ruleLevels {
		out.LevelBuckets = append(out.LevelBuckets, Bucket{Key: fmt.Sprintf("%d", lv.Level), Count: byLevel[lv.Level]})
	}

	if err := s.db.SelectContext(ctx, &out.FlagBuckets, `
		SELECT flag AS `+"`key`"+`, COUNT(*) AS count FROM u_invite_user
		WHERE inviter <> '' AND flag <> '' GROUP BY flag ORDER BY count DESC`); err != nil {
		return err
	}
	if out.FlagBuckets == nil {
		out.FlagBuckets = []Bucket{}
	}
	if err := s.db.SelectContext(ctx, &out.StageBuckets, `
		SELECT CAST(stage AS CHAR) AS `+"`key`"+`, COUNT(*) AS count FROM u_invite_user
		WHERE inviter <> '' AND flag = '' GROUP BY stage ORDER BY stage`); err != nil {
		return err
	}
	if out.StageBuckets == nil {
		out.StageBuckets = []Bucket{}
	}
	httpx.OK(w, out)
	return nil
}

// TrendPoint 某一天的数字。
type TrendPoint struct {
	Date      string `json:"date"`
	Binds     int64  `json:"binds"`     // 当日新绑定(含被标记)
	Flagged   int64  `json:"flagged"`   // 当日新绑定里被标记的
	Activated int64  `json:"activated"` // 当日结算的"进游戏"
	Valid     int64  `json:"valid"`     // 当日结算的"有效邀请"
	Points    int64  `json:"points"`    // 当日发放积分净额
	NewUsers  int64  `json:"newUsers"`  // 当日首次领码
}

func (s *Service) trend(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	days := httpx.QueryInt(r, "days", 30)
	if days < 1 {
		days = 1
	}
	if days > 180 {
		days = 180
	}
	end := time.Now().In(s.loc)
	start := end.AddDate(0, 0, -(days - 1))
	startStr := start.Format("2006-01-02")

	points := map[string]*TrendPoint{}
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		points[d] = &TrendPoint{Date: d}
	}

	var binds []struct {
		D       string `db:"d"`
		Binds   int64  `db:"binds"`
		Flagged int64  `db:"flagged"`
	}
	if err := s.db.SelectContext(ctx, &binds, `
		SELECT DATE(bind_at) AS d, COUNT(*) AS binds, COALESCE(SUM(flag <> ''), 0) AS flagged
		FROM u_invite_user WHERE inviter <> '' AND bind_at >= ? GROUP BY d`, startStr); err != nil {
		return err
	}
	for _, b := range binds {
		if p, ok := points[b.D]; ok {
			p.Binds, p.Flagged = b.Binds, b.Flagged
		}
	}

	var news []struct {
		D   string `db:"d"`
		Cnt int64  `db:"cnt"`
	}
	if err := s.db.SelectContext(ctx, &news, `
		SELECT DATE(created_at) AS d, COUNT(*) AS cnt FROM u_invite_user WHERE created_at >= ? GROUP BY d`, startStr); err != nil {
		return err
	}
	for _, n := range news {
		if p, ok := points[n.D]; ok {
			p.NewUsers = n.Cnt
		}
	}

	var logs []struct {
		D         string `db:"d"`
		Activated int64  `db:"activated"`
		Valid     int64  `db:"valid"`
		Points    int64  `db:"pts"`
	}
	if err := s.db.SelectContext(ctx, &logs, `
		SELECT DATE(created_at) AS d,
		       COALESCE(SUM(kind = 'activate'), 0) AS activated,
		       COALESCE(SUM(kind = 'valid'), 0)    AS valid,
		       COALESCE(SUM(points), 0)            AS pts
		FROM u_invite_point_log WHERE created_at >= ? GROUP BY d`, startStr); err != nil {
		return err
	}
	for _, l := range logs {
		if p, ok := points[l.D]; ok {
			p.Activated, p.Valid, p.Points = l.Activated, l.Valid, l.Points
		}
	}

	out := make([]*TrendPoint, 0, days)
	for i := 0; i < days; i++ {
		out = append(out, points[start.AddDate(0, 0, i).Format("2006-01-02")])
	}
	httpx.OK(w, out)
	return nil
}

// LeaderboardRow 榜单一行(带官网昵称)。
type LeaderboardRow struct {
	Rank        int    `json:"rank"`
	Address     string `json:"address"`
	Name        string `json:"name"`
	Code        string `json:"code"`
	Points      int64  `json:"points"`
	Level       int    `json:"level"`
	InviteTotal int64  `json:"inviteTotal"`
	InviteValid int64  `json:"inviteValid"`
	PointsAt    string `json:"pointsAt"`
}

func (s *Service) leaderboard(w http.ResponseWriter, r *http.Request) error {
	limit := httpx.QueryInt(r, "limit", 50)
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	var rows []struct {
		Address     string  `db:"address"`
		Code        string  `db:"code"`
		Points      int64   `db:"points"`
		InviteTotal int64   `db:"invite_total"`
		InviteValid int64   `db:"invite_valid"`
		PointsAt    *string `db:"points_at"`
		Name        *string `db:"name"`
	}
	if err := s.db.SelectContext(r.Context(), &rows, `
		SELECT u.address, u.code, u.points, u.invite_total, u.invite_valid, u.points_at,
		       CASE WHEN p.name IS NULL OR p.name = u.address THEN NULL ELSE p.name END AS name
		FROM u_invite_user u LEFT JOIN u_profile p ON p.address = u.address
		WHERE u.points > 0
		ORDER BY u.points DESC, u.points_at ASC, u.address ASC
		LIMIT ?`, limit); err != nil {
		return err
	}
	out := make([]*LeaderboardRow, 0, len(rows))
	for i, x := range rows {
		row := &LeaderboardRow{
			Rank: i + 1, Address: x.Address, Code: x.Code, Points: x.Points, Level: s.levelOf(r.Context(), x.Points),
			InviteTotal: x.InviteTotal, InviteValid: x.InviteValid,
		}
		if x.PointsAt != nil {
			row.PointsAt = *x.PointsAt
		}
		if x.Name != nil {
			row.Name = *x.Name
		}
		out = append(out, row)
	}
	httpx.OK(w, out)
	return nil
}

// IPRow 同一 IP 绑定情况。
type IPRow struct {
	IP        string `json:"ip"`
	Total     int64  `json:"total"`
	Flagged   int64  `json:"flagged"`
	Counted   int64  `json:"counted"`
	Inviters  int64  `json:"inviters"`
	FirstBind string `json:"firstBind"`
	LastBind  string `json:"lastBind"`
}

func (s *Service) ipReport(w http.ResponseWriter, r *http.Request) error {
	min := httpx.QueryInt(r, "min", 2)
	if min < 1 {
		min = 1
	}
	limit := httpx.QueryInt(r, "limit", 100)
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	var rows []struct {
		IP        string `db:"ip"`
		Total     int64  `db:"total"`
		Flagged   int64  `db:"flagged"`
		Inviters  int64  `db:"inviters"`
		FirstBind string `db:"first_bind"`
		LastBind  string `db:"last_bind"`
	}
	if err := s.db.SelectContext(r.Context(), &rows, `
		SELECT bind_ip AS ip, COUNT(*) AS total, COALESCE(SUM(flag <> ''), 0) AS flagged,
		       COUNT(DISTINCT inviter) AS inviters,
		       COALESCE(MIN(bind_at), '') AS first_bind, COALESCE(MAX(bind_at), '') AS last_bind
		FROM u_invite_user WHERE inviter <> '' AND bind_ip <> ''
		GROUP BY bind_ip HAVING total >= ?
		ORDER BY total DESC, flagged DESC LIMIT ?`, min, limit); err != nil {
		return err
	}
	out := make([]*IPRow, 0, len(rows))
	for _, x := range rows {
		out = append(out, &IPRow{IP: x.IP, Total: x.Total, Flagged: x.Flagged, Counted: x.Total - x.Flagged,
			Inviters: x.Inviters, FirstBind: x.FirstBind, LastBind: x.LastBind})
	}
	httpx.OK(w, out)
	return nil
}

// ---------------------------------------------------------------------------
// 详情
// ---------------------------------------------------------------------------

type userRow struct {
	Address     string  `db:"address" json:"address"`
	Code        string  `db:"code" json:"code"`
	Inviter     string  `db:"inviter" json:"inviter"`
	BindIP      string  `db:"bind_ip" json:"bindIp"`
	BindAt      *string `db:"bind_at" json:"bindAt"`
	Flag        string  `db:"flag" json:"flag"`
	Stage       int     `db:"stage" json:"stage"`
	Points      int64   `db:"points" json:"points"`
	PointsAt    *string `db:"points_at" json:"pointsAt"`
	InviteTotal int64   `db:"invite_total" json:"inviteTotal"`
	InviteValid int64   `db:"invite_valid" json:"inviteValid"`
	CreatedAt   string  `db:"created_at" json:"createdAt"`
	UpdatedAt   string  `db:"updated_at" json:"updatedAt"`
}

const userCols = "address, code, inviter, bind_ip, bind_at, flag, stage, points, points_at, invite_total, invite_valid, created_at, updated_at"

func (s *Service) getUser(ctx context.Context, address string) (*userRow, error) {
	u := &userRow{}
	err := s.db.GetContext(ctx, u, "SELECT "+userCols+" FROM u_invite_user WHERE address = ?", address)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UserDetail 详情:本人 + 邀请人 + 名下被邀请人 + 积分流水 + 出现过的 IP。
type UserDetail struct {
	User      *userRow      `json:"user"`
	Name      string        `json:"name"`
	Level     int           `json:"level"`
	Rank      int64         `json:"rank"`
	Inviter   *userRow      `json:"inviter"`
	Invitees  []*userRow    `json:"invitees"`
	PointLogs []pointLogRow `json:"pointLogs"`
	IPs       []ipLogRow    `json:"ips"`
	AdminLogs []adminLogRow `json:"adminLogs"`
	Counts    struct {
		Total     int64 `json:"total"`
		Activated int64 `json:"activated"`
		Valid     int64 `json:"valid"`
		Flagged   int64 `json:"flagged"`
	} `json:"counts"`
}

type pointLogRow struct {
	ID        int64  `db:"id" json:"id"`
	Kind      string `db:"kind" json:"kind"`
	Ref       string `db:"ref" json:"ref"`
	Points    int64  `db:"points" json:"points"`
	CreatedAt string `db:"created_at" json:"createdAt"`
}

type ipLogRow struct {
	IP     string `db:"ip" json:"ip"`
	SeenAt string `db:"seen_at" json:"seenAt"`
}

type adminLogRow struct {
	ID        int64  `db:"id" json:"id"`
	AdminUID  int64  `db:"admin_uid" json:"adminUid"`
	AdminName string `db:"admin_name" json:"adminName"`
	Action    string `db:"action" json:"action"`
	Target    string `db:"target" json:"target"`
	Points    int64  `db:"points" json:"points"`
	Reason    string `db:"reason" json:"reason"`
	CreatedAt string `db:"created_at" json:"createdAt"`
}

func (s *Service) userDetail(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	key := strings.TrimSpace(r.PathValue("address"))
	var u *userRow
	if isAddress(key) {
		var err error
		if u, err = s.getUser(ctx, key); err != nil {
			return err
		}
	} else if len(key) == 12 {
		// 也允许用邀请码查
		row := &userRow{}
		err := s.db.GetContext(ctx, row, "SELECT "+userCols+" FROM u_invite_user WHERE code = ?", strings.ToUpper(key))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			u = row
		}
	}
	if u == nil {
		return httpx.NotFound("地址或邀请码不存在")
	}
	address := u.Address
	out := &UserDetail{User: u, Level: s.levelOf(ctx, u.Points), Invitees: []*userRow{}, PointLogs: []pointLogRow{}, IPs: []ipLogRow{}, AdminLogs: []adminLogRow{}}

	var name *string
	_ = s.db.GetContext(ctx, &name, "SELECT CASE WHEN name = address THEN NULL ELSE name END FROM u_profile WHERE address = ?", address)
	if name != nil {
		out.Name = *name
	}
	if u.Points > 0 {
		at := ""
		if u.PointsAt != nil {
			at = *u.PointsAt
		}
		var ahead int64
		if err := s.db.GetContext(ctx, &ahead, `SELECT COUNT(*) FROM u_invite_user
			WHERE points > ? OR (points = ? AND (points_at < ? OR (points_at = ? AND address < ?)))`,
			u.Points, u.Points, at, at, address); err == nil {
			out.Rank = ahead + 1
		}
	}
	if u.Inviter != "" {
		out.Inviter, _ = s.getUser(ctx, u.Inviter)
	}
	if err := s.db.SelectContext(ctx, &out.Invitees, "SELECT "+userCols+" FROM u_invite_user WHERE inviter = ? ORDER BY bind_at DESC LIMIT 200", address); err != nil {
		return err
	}
	for _, iv := range out.Invitees {
		if iv.Flag != "" {
			out.Counts.Flagged++
			continue
		}
		out.Counts.Total++
		if iv.Stage >= 1 {
			out.Counts.Activated++
		}
		if iv.Stage >= 2 {
			out.Counts.Valid++
		}
	}
	if err := s.db.SelectContext(ctx, &out.PointLogs, "SELECT id, kind, ref, points, created_at FROM u_invite_point_log WHERE address = ? ORDER BY id DESC LIMIT 200", address); err != nil {
		return err
	}
	if err := s.db.SelectContext(ctx, &out.IPs, "SELECT ip, seen_at FROM u_invite_ip_log WHERE address = ? ORDER BY seen_at DESC LIMIT 50", address); err != nil {
		return err
	}
	if err := s.db.SelectContext(ctx, &out.AdminLogs, "SELECT id, admin_uid, admin_name, action, target, points, reason, created_at FROM u_invite_admin_log WHERE target = ? ORDER BY id DESC LIMIT 50", address); err != nil {
		// 日志表尚未建时不影响详情
		out.AdminLogs = []adminLogRow{}
	}
	httpx.OK(w, out)
	return nil
}

// ---------------------------------------------------------------------------
// 管理动作:标记 / 恢复 / 调分
// ---------------------------------------------------------------------------

type reasonBody struct {
	Reason string `json:"reason"`
	Points int64  `json:"points"`
}

func isAddress(s string) bool {
	return len(s) == 42 && strings.HasPrefix(strings.ToLower(s), "0x")
}

// normAddress 管理动作只接受钱包地址(与库里 checksum 写法一致,按原样匹配)。
func normAddress(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !isAddress(s) {
		return "", httpx.BadRequest("地址格式不正确")
	}
	return s, nil
}

// flag 手动标记某条邀请关系(address 是被邀请人):flag 置为 manual,并把双方因这条关系拿到的积分扣回。
func (s *Service) flag(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	sess := auth.SessionFrom(ctx)
	address, err := normAddress(r.PathValue("address"))
	if err != nil {
		return err
	}
	body := &reasonBody{}
	_ = httpx.DecodeJSON(r, body)
	u, err := s.getUser(ctx, address)
	if err != nil {
		return err
	}
	if u == nil || u.Inviter == "" {
		return httpx.NotFound("该地址没有绑定邀请人")
	}
	if u.Flag != "" {
		return httpx.NewError(httpx.CodeOperationFailed, "该关系已被标记("+u.Flag+")")
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "UPDATE u_invite_user SET flag = ? WHERE address = ? AND flag = ''", flagManual, address); err != nil {
		return err
	}
	stamp := time.Now().UnixMilli()
	// 邀请人因这个被邀请人拿到的:bind / activate / valid
	var earnedInviter int64
	if err := tx.GetContext(ctx, &earnedInviter, `SELECT COALESCE(SUM(points), 0) FROM u_invite_point_log
		WHERE address = ? AND ref = ? AND kind IN ('bind','activate','valid')`, u.Inviter, address); err != nil {
		return err
	}
	// 被邀请人自己拿到的新人奖励:joined
	var earnedInvitee int64
	if err := tx.GetContext(ctx, &earnedInvitee, `SELECT COALESCE(SUM(points), 0) FROM u_invite_point_log
		WHERE address = ? AND ref = ? AND kind = 'joined'`, address, u.Inviter); err != nil {
		return err
	}
	if earnedInviter != 0 {
		if err := addPoints(ctx, tx, u.Inviter, -earnedInviter, fmt.Sprintf("flag:%s:%d", address, stamp)); err != nil {
			return err
		}
	}
	if earnedInvitee != 0 {
		if err := addPoints(ctx, tx, address, -earnedInvitee, fmt.Sprintf("flag:%s:%d", address, stamp)); err != nil {
			return err
		}
	}
	if err := writeAdminLog(ctx, tx, sess, "flag", address, -(earnedInviter + earnedInvitee), body.Reason); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	httpx.OK(w, map[string]any{"revokedInviter": earnedInviter, "revokedInvitee": earnedInvitee})
	return nil
}

// unflag 恢复:flag 清空,把 flag 动作当时扣掉的积分原数退回。
// 只恢复 manual / same_ip / ip_limited 任一标记;自动标记的关系之前本就没发过分,退回额为 0。
func (s *Service) unflag(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	sess := auth.SessionFrom(ctx)
	address, err := normAddress(r.PathValue("address"))
	if err != nil {
		return err
	}
	body := &reasonBody{}
	_ = httpx.DecodeJSON(r, body)
	u, err := s.getUser(ctx, address)
	if err != nil {
		return err
	}
	if u == nil || u.Inviter == "" {
		return httpx.NotFound("该地址没有绑定邀请人")
	}
	if u.Flag == "" {
		return httpx.NewError(httpx.CodeOperationFailed, "该关系未被标记")
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "UPDATE u_invite_user SET flag = '' WHERE address = ?", address); err != nil {
		return err
	}
	// 之前 flag 时扣的分(ref = flag:<address>:*),按人汇总后原数退回
	var revoked []struct {
		Address string `db:"address"`
		Sum     int64  `db:"s"`
	}
	if err := tx.SelectContext(ctx, &revoked, `SELECT address, COALESCE(SUM(points), 0) AS s FROM u_invite_point_log
		WHERE kind = ? AND ref LIKE ? GROUP BY address`, kindAdminAdjust, "flag:"+address+":%"); err != nil {
		return err
	}
	stamp := time.Now().UnixMilli()
	var restored int64
	for _, rv := range revoked {
		if rv.Sum == 0 {
			continue
		}
		if err := addPoints(ctx, tx, rv.Address, -rv.Sum, fmt.Sprintf("unflag:%s:%d", address, stamp)); err != nil {
			return err
		}
		restored += -rv.Sum
	}
	// 自动标记(same_ip / ip_limited)的关系从未发过 bind / joined,恢复后由官网结算补 activate / valid,
	// bind / joined 这两档在这里按官网口径补发(INSERT IGNORE 保证不会重复)
	// 分值取官网当前口径(/invite/rules),与官网结算保持一致
	rules := s.api.Rules(ctx)
	if err := grantIfMissing(ctx, tx, u.Inviter, "bind", address, rules.BindPoints); err != nil {
		return err
	}
	if err := grantIfMissing(ctx, tx, address, "joined", u.Inviter, rules.JoinedPoints); err != nil {
		return err
	}
	if err := writeAdminLog(ctx, tx, sess, "unflag", address, restored, body.Reason); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	httpx.OK(w, map[string]any{"restored": restored})
	return nil
}

// adjust 手动加 / 扣分,必须填原因。
func (s *Service) adjust(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	sess := auth.SessionFrom(ctx)
	address, err := normAddress(r.PathValue("address"))
	if err != nil {
		return err
	}
	body := &reasonBody{}
	if err := httpx.DecodeJSON(r, body); err != nil {
		return err
	}
	if body.Points == 0 {
		return httpx.BadRequest("points 不能为 0")
	}
	if body.Points > 100000 || body.Points < -100000 {
		return httpx.BadRequest("单次调整不能超过 ±100000")
	}
	if strings.TrimSpace(body.Reason) == "" {
		return httpx.BadRequest("请填写调整原因")
	}
	u, err := s.getUser(ctx, address)
	if err != nil {
		return err
	}
	if u == nil {
		return httpx.NotFound("地址不存在")
	}
	if u.Points+body.Points < 0 {
		return httpx.BadRequest(fmt.Sprintf("扣减后积分为负(当前 %d)", u.Points))
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := addPoints(ctx, tx, address, body.Points, fmt.Sprintf("manual:%d:%d", sess.UID, time.Now().UnixMilli())); err != nil {
		return err
	}
	if err := writeAdminLog(ctx, tx, sess, "adjust", address, body.Points, body.Reason); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	httpx.OK(w, map[string]any{"points": u.Points + body.Points})
	return nil
}

// addPoints 写一条 admin_adjust 流水并同步汇总;ref 带时间戳,不会撞唯一键。
func addPoints(ctx context.Context, tx *sqlx.Tx, address string, points int64, ref string) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO u_invite_point_log (address, kind, ref, points) VALUES (?, ?, ?, ?)",
		address, kindAdminAdjust, ref, points); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE u_invite_user SET points = points + ?, points_at = NOW() WHERE address = ?", points, address)
	return err
}

// grantIfMissing 按官网口径补发某档(流水唯一键保证幂等),补发成功才同步汇总。
func grantIfMissing(ctx context.Context, tx *sqlx.Tx, address, kind, ref string, points int64) error {
	res, err := tx.ExecContext(ctx, "INSERT IGNORE INTO u_invite_point_log (address, kind, ref, points) VALUES (?, ?, ?, ?)",
		address, kind, ref, points)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, "UPDATE u_invite_user SET points = points + ?, points_at = NOW() WHERE address = ?", points, address)
	return err
}

func writeAdminLog(ctx context.Context, tx *sqlx.Tx, sess *auth.Session, action, target string, points int64, reason string) error {
	var uid int64
	name := ""
	if sess != nil {
		uid, name = sess.UID, sess.Username
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO u_invite_admin_log (admin_uid, admin_name, action, target, points, reason)
		VALUES (?, ?, ?, ?, ?, ?)`, uid, name, action, target, points, strings.TrimSpace(reason))
	return err
}
