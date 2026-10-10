// Package portalsocial 是官网三个运营渠道(X / Telegram / Discord)的运营看板:
//
//   - 站内数据(cow-portal 库):社交任务的绑定漏斗、留存档位、取关、反作弊标记、积分发放、
//     账号质量(粉丝数 / 账号年龄)、同 IP 报告、复查健康度,以及账号列表与导出;
//   - 官方渠道数据(直连平台 API):官方 X 账号的粉丝 / 推文数、官方 Telegram 群成员数、
//     Discord 服务器成员 / 在线数,10 分钟缓存,并每日落一条快照(u_social_channel_stat)画趋势。
//
// 与 portalinvite / portaluser 一样属于官网业务,临时挂在 GMS;只依赖 httpx / auth / perm / query,
// 库连接、凭证、权限码、路由前缀 /portal/social 全在本包内,迁走时整个目录带走。
package portalsocial

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"cow-manager-backend/internal/auth"
	"cow-manager-backend/internal/httpx"
	"cow-manager-backend/internal/perm"
	"cow-manager-backend/internal/query"
)

// 权限:只读 = feature/portal-social/view(含导出);刷新 / 快照 = feature/portal-social/edit。
var (
	PermView   = perm.PortalSocialView
	PermExport = perm.PortalSocialView
	PermManage = perm.PortalSocialEdit
)

// Platforms 展示顺序,与官网 model.SocialPlatforms 一致。
var Platforms = []string{"x", "telegram", "discord"}

const liveTTL = 10 * time.Minute

// Service 社交媒体运营服务。
type Service struct {
	db      *sqlx.DB // cow-portal
	social  socialConf
	loc     *time.Location
	labeler query.EnumLabeler

	liveMu sync.Mutex
	live   map[string]*ChannelLive
	liveAt map[string]time.Time
}

// NewService 创建服务。
func NewService(portal *sqlx.DB, social socialConf, loc *time.Location, labeler query.EnumLabeler) *Service {
	return &Service{db: portal, social: social, loc: loc, labeler: labeler, live: map[string]*ChannelLive{}, liveAt: map[string]time.Time{}}
}

// AccountSpec 社交账号列表 u_social_account(不暴露 token 列)。
var AccountSpec = &query.Spec{
	Table: "u_social_account", DefaultSort: "`id` DESC",
	Columns: []query.Column{
		{Name: "id", Label: "ID", Kind: query.Int, Filter: true, Sort: true},
		{Name: "platform", Label: "平台", Kind: query.String, Filter: true},
		{Name: "social_id", Label: "平台账号ID", Kind: query.String, Filter: true},
		{Name: "handle", Label: "账号", Kind: query.String, Filter: true},
		{Name: "name", Label: "昵称", Kind: query.String, Filter: true},
		{Name: "avatar", Label: "头像", Kind: query.String},
		{Name: "address", Label: "钱包地址", Kind: query.String, Filter: true},
		{Name: "status", Label: "状态", Kind: query.String, Filter: true},
		{Name: "flag", Label: "标记", Kind: query.String, Filter: true},
		{Name: "bind_ip", Label: "绑定 IP", Kind: query.String, Filter: true},
		{Name: "bound_at", Label: "绑定时间", Kind: query.String, Filter: true, Sort: true},
		{Name: "verified_at", Label: "确认关注", Kind: query.String, Sort: true},
		{Name: "keep7_at", Label: "满 7 天", Kind: query.String, Sort: true},
		{Name: "keep30_at", Label: "满 30 天", Kind: query.String, Sort: true},
		{Name: "revoked_at", Label: "取关时间", Kind: query.String, Sort: true},
		{Name: "last_check_at", Label: "最近复查", Kind: query.String, Sort: true},
		{Name: "last_check_err", Label: "复查错误", Kind: query.String, Filter: true},
		{Name: "meta", Label: "账号信号", Kind: query.JSON},
	},
}

// Register 挂载路由。
func (s *Service) Register(mux *http.ServeMux) {
	h := query.Handlers{Spec: AccountSpec, DB: s.db, Loc: s.loc, Labeler: s.labeler}
	mux.HandleFunc("GET /portal/social/accounts", httpx.H(auth.Require(PermView, h.List)))
	mux.HandleFunc("GET /portal/social/accounts/export", httpx.H(auth.Require(PermExport, h.Export)))

	mux.HandleFunc("GET /portal/social/overview", httpx.H(auth.Require(PermView, s.overview)))
	mux.HandleFunc("GET /portal/social/trend", httpx.H(auth.Require(PermView, s.trend)))
	mux.HandleFunc("GET /portal/social/quality", httpx.H(auth.Require(PermView, s.quality)))
	mux.HandleFunc("GET /portal/social/ips", httpx.H(auth.Require(PermView, s.ipReport)))
	mux.HandleFunc("GET /portal/social/channels", httpx.H(auth.Require(PermView, s.channels)))
	mux.HandleFunc("GET /portal/social/channels/history", httpx.H(auth.Require(PermView, s.channelHistory)))
	mux.HandleFunc("POST /portal/social/channels/snapshot", httpx.H(auth.Require(PermManage, s.snapshotNow)))
}

// Start 启动每日快照巡检:启动 30 秒后检查一次,之后每小时检查当天是否已有快照,没有就补。
func (s *Service) Start(ctx context.Context) {
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		for {
			s.ensureTodaySnapshot(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Hour):
			}
		}
	}()
}

// ---------------------------------------------------------------------------
// 概览
// ---------------------------------------------------------------------------

// PlatformOverview 一个平台的站内数据。
type PlatformOverview struct {
	Platform   string `json:"platform"`
	Configured bool   `json:"configured"` // GMS 是否能直连该平台(官方渠道数据)

	// 漏斗
	Started   int64            `json:"started"`   // 发起绑定(OAuth / 深链 state 数)
	Completed int64            `json:"completed"` // 回调完成(state 已消费)
	Accounts  int64            `json:"accounts"`  // 当前绑定记录数(含各状态)
	Bound     int64            `json:"bound"`     // 已绑定未确认
	Verified  int64            `json:"verified"`  // 当前仍在关注 / 群内
	AuthLost  int64            `json:"authLost"`  // 授权失效
	Revoked   int64            `json:"revoked"`   // 已取关(已扣分)
	Keep7     int64            `json:"keep7"`     // 达到 7 天
	Keep30    int64            `json:"keep30"`    // 达到 30 天
	Counted   int64            `json:"counted"`   // verified 且未被标记(真正计分的)
	Flagged   map[string]int64 `json:"flagged"`

	// 积分
	PointsTotal  int64            `json:"pointsTotal"`
	PointsByKind map[string]int64 `json:"pointsByKind"`

	// 今日
	TodayBound    int64 `json:"todayBound"`
	TodayVerified int64 `json:"todayVerified"`
	TodayRevoked  int64 `json:"todayRevoked"`

	// 复查健康度
	LastCheckAt string `json:"lastCheckAt"`
	StaleCount  int64  `json:"staleCount"` // verified 但超过 48 小时没复查过
	ErrorCount  int64  `json:"errorCount"` // 最近一次复查报错的账号数
	LastError   string `json:"lastError"`
}

// Overview 概览。
type Overview struct {
	Platforms     []*PlatformOverview `json:"platforms"`
	UniqueWallets int64               `json:"uniqueWallets"` // 绑过任一平台的钱包数
	AllThree      int64               `json:"allThree"`      // 三个平台都 verified 的钱包数
	PointsTotal   int64               `json:"pointsTotal"`
	GeneratedAt   int64               `json:"generatedAt"`
}

func (s *Service) overview(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	today := time.Now().In(s.loc).Format("2006-01-02")
	out := &Overview{GeneratedAt: time.Now().Unix()}

	type row struct {
		Platform  string `db:"platform"`
		Accounts  int64  `db:"accounts"`
		Bound     int64  `db:"bound"`
		Verified  int64  `db:"verified"`
		AuthLost  int64  `db:"auth_lost"`
		Revoked   int64  `db:"revoked"`
		Keep7     int64  `db:"keep7"`
		Keep30    int64  `db:"keep30"`
		Counted   int64  `db:"counted"`
		TodayB    int64  `db:"today_bound"`
		TodayV    int64  `db:"today_verified"`
		TodayR    int64  `db:"today_revoked"`
		LastCheck string `db:"last_check"`
		Stale     int64  `db:"stale"`
		Errors    int64  `db:"errors"`
	}
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, `
		SELECT platform, COUNT(*) AS accounts,
		  COALESCE(SUM(status = 'bound'), 0) AS bound,
		  COALESCE(SUM(status = 'verified'), 0) AS verified,
		  COALESCE(SUM(status = 'auth_lost'), 0) AS auth_lost,
		  COALESCE(SUM(status = 'revoked'), 0) AS revoked,
		  COALESCE(SUM(keep7_at IS NOT NULL), 0) AS keep7,
		  COALESCE(SUM(keep30_at IS NOT NULL), 0) AS keep30,
		  COALESCE(SUM(status = 'verified' AND flag = ''), 0) AS counted,
		  COALESCE(SUM(DATE(bound_at) = ?), 0) AS today_bound,
		  COALESCE(SUM(DATE(verified_at) = ?), 0) AS today_verified,
		  COALESCE(SUM(DATE(revoked_at) = ?), 0) AS today_revoked,
		  COALESCE(DATE_FORMAT(MAX(last_check_at), '%Y-%m-%d %H:%i:%s'), '') AS last_check,
		  COALESCE(SUM(status = 'verified' AND (last_check_at IS NULL OR last_check_at < NOW() - INTERVAL 48 HOUR)), 0) AS stale,
		  COALESCE(SUM(last_check_err <> ''), 0) AS errors
		FROM u_social_account GROUP BY platform`, today, today, today); err != nil {
		return err
	}
	byP := map[string]row{}
	for _, x := range rows {
		byP[x.Platform] = x
	}

	var flags []struct {
		Platform string `db:"platform"`
		Flag     string `db:"flag"`
		Cnt      int64  `db:"cnt"`
	}
	_ = s.db.SelectContext(ctx, &flags, "SELECT platform, flag, COUNT(*) AS cnt FROM u_social_account WHERE flag <> '' GROUP BY platform, flag")

	var starts []struct {
		Platform  string `db:"platform"`
		Total     int64  `db:"total"`
		Completed int64  `db:"completed"`
	}
	_ = s.db.SelectContext(ctx, &starts, "SELECT platform, COUNT(*) AS total, COALESCE(SUM(used_at IS NOT NULL), 0) AS completed FROM u_social_oauth_state GROUP BY platform")

	var pts []struct {
		Ref  string `db:"ref"`
		Kind string `db:"kind"`
		Pts  int64  `db:"pts"`
	}
	_ = s.db.SelectContext(ctx, &pts, "SELECT ref, kind, COALESCE(SUM(points), 0) AS pts FROM u_invite_point_log WHERE kind LIKE 'social%' GROUP BY ref, kind")

	var lastErrs []struct {
		Platform string `db:"platform"`
		Err      string `db:"err"`
	}
	_ = s.db.SelectContext(ctx, &lastErrs, `SELECT a.platform, a.last_check_err AS err FROM u_social_account a
		JOIN (SELECT platform, MAX(last_check_at) AS t FROM u_social_account WHERE last_check_err <> '' GROUP BY platform) m
		  ON m.platform = a.platform AND m.t = a.last_check_at WHERE a.last_check_err <> ''`)

	for _, p := range Platforms {
		x := byP[p]
		po := &PlatformOverview{
			Platform: p, Configured: s.configured(p),
			Accounts: x.Accounts, Bound: x.Bound, Verified: x.Verified, AuthLost: x.AuthLost, Revoked: x.Revoked,
			Keep7: x.Keep7, Keep30: x.Keep30, Counted: x.Counted,
			TodayBound: x.TodayB, TodayVerified: x.TodayV, TodayRevoked: x.TodayR,
			LastCheckAt: x.LastCheck, StaleCount: x.Stale, ErrorCount: x.Errors,
			Flagged: map[string]int64{}, PointsByKind: map[string]int64{},
		}
		for _, f := range flags {
			if f.Platform == p {
				po.Flagged[f.Flag] = f.Cnt
			}
		}
		for _, st := range starts {
			if st.Platform == p {
				po.Started, po.Completed = st.Total, st.Completed
			}
		}
		for _, pt := range pts {
			if pt.Ref == p {
				po.PointsByKind[pt.Kind] = pt.Pts
				po.PointsTotal += pt.Pts
			}
		}
		for _, le := range lastErrs {
			if le.Platform == p {
				po.LastError = le.Err
			}
		}
		out.PointsTotal += po.PointsTotal
		out.Platforms = append(out.Platforms, po)
	}
	_ = s.db.GetContext(ctx, &out.UniqueWallets, "SELECT COUNT(DISTINCT address) FROM u_social_account WHERE status IN ('bound','verified')")
	_ = s.db.GetContext(ctx, &out.AllThree, "SELECT COUNT(*) FROM (SELECT address FROM u_social_account WHERE status = 'verified' GROUP BY address HAVING COUNT(DISTINCT platform) >= 3) t")
	httpx.OK(w, out)
	return nil
}

// ---------------------------------------------------------------------------
// 趋势
// ---------------------------------------------------------------------------

// TrendPoint 某天某平台的数字。
type TrendPoint struct {
	Date     string `json:"date"`
	Platform string `json:"platform"`
	Bound    int64  `json:"bound"`
	Verified int64  `json:"verified"`
	Revoked  int64  `json:"revoked"`
	Keep7    int64  `json:"keep7"`
	Keep30   int64  `json:"keep30"`
	Points   int64  `json:"points"`
	Started  int64  `json:"started"`
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
	key := func(d, p string) string { return d + "|" + p }
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		for _, p := range Platforms {
			points[key(d, p)] = &TrendPoint{Date: d, Platform: p}
		}
	}
	type dp struct {
		D   string `db:"d"`
		P   string `db:"p"`
		Cnt int64  `db:"cnt"`
	}
	fill := func(sqlStr string, set func(tp *TrendPoint, n int64)) {
		var rows []dp
		if err := s.db.SelectContext(ctx, &rows, sqlStr, startStr); err != nil {
			log.Printf("portalsocial: trend query failed: %v", err)
			return
		}
		for _, x := range rows {
			if tp, ok := points[key(x.D, x.P)]; ok {
				set(tp, x.Cnt)
			}
		}
	}
	fill("SELECT DATE(bound_at) AS d, platform AS p, COUNT(*) AS cnt FROM u_social_account WHERE bound_at >= ? GROUP BY d, p", func(tp *TrendPoint, n int64) { tp.Bound = n })
	fill("SELECT DATE(verified_at) AS d, platform AS p, COUNT(*) AS cnt FROM u_social_account WHERE verified_at >= ? GROUP BY d, p", func(tp *TrendPoint, n int64) { tp.Verified = n })
	fill("SELECT DATE(revoked_at) AS d, platform AS p, COUNT(*) AS cnt FROM u_social_account WHERE revoked_at >= ? GROUP BY d, p", func(tp *TrendPoint, n int64) { tp.Revoked = n })
	fill("SELECT DATE(keep7_at) AS d, platform AS p, COUNT(*) AS cnt FROM u_social_account WHERE keep7_at >= ? GROUP BY d, p", func(tp *TrendPoint, n int64) { tp.Keep7 = n })
	fill("SELECT DATE(keep30_at) AS d, platform AS p, COUNT(*) AS cnt FROM u_social_account WHERE keep30_at >= ? GROUP BY d, p", func(tp *TrendPoint, n int64) { tp.Keep30 = n })
	fill("SELECT DATE(created_at) AS d, platform AS p, COUNT(*) AS cnt FROM u_social_oauth_state WHERE created_at >= ? GROUP BY d, p", func(tp *TrendPoint, n int64) { tp.Started = n })
	fill("SELECT DATE(created_at) AS d, ref AS p, COALESCE(SUM(points), 0) AS cnt FROM u_invite_point_log WHERE kind LIKE 'social%' AND created_at >= ? GROUP BY d, p", func(tp *TrendPoint, n int64) { tp.Points = n })

	out := make([]*TrendPoint, 0, days*len(Platforms))
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		for _, p := range Platforms {
			out = append(out, points[key(d, p)])
		}
	}
	httpx.OK(w, out)
	return nil
}

// ---------------------------------------------------------------------------
// 账号质量(来自官网绑定时记录的 meta:createdAt / followers)
// ---------------------------------------------------------------------------

// Bucket 分桶。
type Bucket struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// Quality 一个平台的账号质量分布。
type Quality struct {
	Platform      string   `json:"platform"`
	WithMeta      int64    `json:"withMeta"`
	Followers     []Bucket `json:"followers"`  // X 才有
	AccountAge    []Bucket `json:"accountAge"` // X / Discord
	LowQuality    int64    `json:"lowQuality"` // flag=low_quality
	IPLimited     int64    `json:"ipLimited"`  // flag=ip_limited
	MedianFollow  int64    `json:"medianFollowers"`
	MedianAgeDays int64    `json:"medianAgeDays"`
}

// edge 分桶上界(含)。
type edge struct {
	Label string
	Max   int64
}

var followerEdges = []edge{{"0", 0}, {"1-9", 9}, {"10-49", 49}, {"50-199", 199}, {"200-999", 999}, {"1000+", 1<<62 - 1}}
var ageEdges = []edge{{"<30天", 29}, {"30-180天", 180}, {"180天-1年", 365}, {"1-3年", 1095}, {"3年+", 1<<62 - 1}}

func bucketOf(edges []edge, v int64) string {
	for _, e := range edges {
		if v <= e.Max {
			return e.Label
		}
	}
	return edges[len(edges)-1].Label
}

func median(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	return xs[len(xs)/2]
}

func (s *Service) quality(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var rows []struct {
		Platform string `db:"platform"`
		Meta     string `db:"meta"`
		Flag     string `db:"flag"`
	}
	if err := s.db.SelectContext(ctx, &rows, "SELECT platform, COALESCE(meta, '') AS meta, flag FROM u_social_account"); err != nil {
		return err
	}
	now := time.Now()
	out := make([]*Quality, 0, len(Platforms))
	for _, p := range Platforms {
		q := &Quality{Platform: p, Followers: []Bucket{}, AccountAge: []Bucket{}}
		fb := map[string]int64{}
		ab := map[string]int64{}
		var fs, ages []int64
		for _, x := range rows {
			if x.Platform != p {
				continue
			}
			switch x.Flag {
			case "low_quality":
				q.LowQuality++
			case "ip_limited":
				q.IPLimited++
			}
			if strings.TrimSpace(x.Meta) == "" {
				continue
			}
			var m struct {
				CreatedAt int64 `json:"createdAt"`
				Followers int64 `json:"followers"`
			}
			if json.Unmarshal([]byte(x.Meta), &m) != nil {
				continue
			}
			q.WithMeta++
			if p == "x" {
				fb[bucketOf(followerEdges, m.Followers)]++
				fs = append(fs, m.Followers)
			}
			if m.CreatedAt > 0 {
				days := int64(now.Sub(time.Unix(m.CreatedAt, 0)).Hours() / 24)
				ab[bucketOf(ageEdges, days)]++
				ages = append(ages, days)
			}
		}
		if p == "x" {
			for _, e := range followerEdges {
				q.Followers = append(q.Followers, Bucket{Key: e.Label, Count: fb[e.Label]})
			}
		}
		for _, e := range ageEdges {
			q.AccountAge = append(q.AccountAge, Bucket{Key: e.Label, Count: ab[e.Label]})
		}
		q.MedianFollow, q.MedianAgeDays = median(fs), median(ages)
		out = append(out, q)
	}
	httpx.OK(w, out)
	return nil
}

// ---------------------------------------------------------------------------
// 同 IP
// ---------------------------------------------------------------------------

// IPRow 同一 IP 的社交绑定情况。
type IPRow struct {
	IP        string `json:"ip"`
	Bindings  int64  `json:"bindings"`
	Wallets   int64  `json:"wallets"`
	Platforms int64  `json:"platforms"`
	Flagged   int64  `json:"flagged"`
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
		Bindings  int64  `db:"bindings"`
		Wallets   int64  `db:"wallets"`
		Platforms int64  `db:"platforms"`
		Flagged   int64  `db:"flagged"`
		First     string `db:"first_bind"`
		Last      string `db:"last_bind"`
	}
	if err := s.db.SelectContext(r.Context(), &rows, `
		SELECT bind_ip AS ip, COUNT(*) AS bindings, COUNT(DISTINCT address) AS wallets, COUNT(DISTINCT platform) AS platforms,
		       COALESCE(SUM(flag <> ''), 0) AS flagged,
		       COALESCE(DATE_FORMAT(MIN(bound_at), '%Y-%m-%d %H:%i:%s'), '') AS first_bind,
		       COALESCE(DATE_FORMAT(MAX(bound_at), '%Y-%m-%d %H:%i:%s'), '') AS last_bind
		FROM u_social_account WHERE bind_ip <> ''
		GROUP BY bind_ip HAVING wallets >= ? ORDER BY wallets DESC, bindings DESC LIMIT ?`, min, limit); err != nil {
		return err
	}
	out := make([]*IPRow, 0, len(rows))
	for _, x := range rows {
		out = append(out, &IPRow{IP: x.IP, Bindings: x.Bindings, Wallets: x.Wallets, Platforms: x.Platforms, Flagged: x.Flagged, FirstBind: x.First, LastBind: x.Last})
	}
	httpx.OK(w, out)
	return nil
}

// ---------------------------------------------------------------------------
// 官方渠道:实时 + 快照
// ---------------------------------------------------------------------------

// channels GET /portal/social/channels?refresh=1 —— refresh 需要 manage 权限,否则用 10 分钟缓存。
func (s *Service) channels(w http.ResponseWriter, r *http.Request) error {
	refresh := httpx.QueryBool(r, "refresh")
	if refresh {
		if sess := auth.SessionFrom(r.Context()); sess == nil || !sess.Has(PermManage) {
			refresh = false
		}
	}
	out := make([]*ChannelLive, 0, len(Platforms))
	for _, p := range Platforms {
		out = append(out, s.liveCached(r.Context(), p, refresh))
	}
	httpx.OK(w, out)
	return nil
}

func (s *Service) liveCached(ctx context.Context, platform string, refresh bool) *ChannelLive {
	s.liveMu.Lock()
	if c, ok := s.live[platform]; ok && !refresh && time.Since(s.liveAt[platform]) < liveTTL {
		s.liveMu.Unlock()
		return c
	}
	s.liveMu.Unlock()
	c := s.fetchLive(ctx, platform)
	s.liveMu.Lock()
	// 拉失败时保留上一次成功的数据,但带上错误信息
	if !c.OK && c.Configured {
		if prev, ok := s.live[platform]; ok && prev.OK {
			keep := *prev
			keep.Error = c.Error
			keep.FetchedAt = prev.FetchedAt
			c = &keep
		}
	}
	s.live[platform] = c
	s.liveAt[platform] = time.Now()
	s.liveMu.Unlock()
	return c
}

// ChannelStat 一条每日快照。
type ChannelStat struct {
	Platform  string `db:"platform" json:"platform"`
	Dt        string `db:"dt" json:"date"`
	Members   int64  `db:"members" json:"members"`
	Secondary int64  `db:"secondary" json:"secondary"`
	Posts     int64  `db:"posts" json:"posts"`
	Verified  int64  `db:"verified" json:"verified"` // 快照时站内 verified 数
}

func (s *Service) channelHistory(w http.ResponseWriter, r *http.Request) error {
	days := httpx.QueryInt(r, "days", 90)
	if days < 1 || days > 730 {
		days = 90
	}
	start := time.Now().In(s.loc).AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	out := []ChannelStat{}
	if err := s.db.SelectContext(r.Context(), &out, `SELECT platform, DATE_FORMAT(dt, '%Y-%m-%d') AS dt, members, secondary, posts, verified
		FROM u_social_channel_stat WHERE dt >= ? ORDER BY dt, platform`, start); err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

func (s *Service) snapshotNow(w http.ResponseWriter, r *http.Request) error {
	res := s.snapshot(r.Context(), true)
	httpx.OK(w, res)
	return nil
}

// ensureTodaySnapshot 当天还没有快照的平台补一条(只对配置了凭证的平台)。
func (s *Service) ensureTodaySnapshot(ctx context.Context) {
	today := time.Now().In(s.loc).Format("2006-01-02")
	var have []string
	if err := s.db.SelectContext(ctx, &have, "SELECT platform FROM u_social_channel_stat WHERE dt = ?", today); err != nil {
		log.Printf("portalsocial: query today's snapshot failed: %v", err)
		return
	}
	done := map[string]bool{}
	for _, p := range have {
		done[p] = true
	}
	missing := false
	for _, p := range Platforms {
		if s.configured(p) && !done[p] {
			missing = true
		}
	}
	if missing {
		s.snapshot(ctx, false)
	}
}

// snapshot 对配置了凭证的平台各拉一次实时数据并写入当天快照;force=true 时覆盖当天已有值。
func (s *Service) snapshot(ctx context.Context, force bool) map[string]any {
	today := time.Now().In(s.loc).Format("2006-01-02")
	res := map[string]any{"date": today}
	for _, p := range Platforms {
		if !s.configured(p) {
			res[p] = "not configured"
			continue
		}
		live := s.liveCached(ctx, p, force)
		if !live.OK {
			res[p] = "fetch failed: " + live.Error
			continue
		}
		var verified int64
		_ = s.db.GetContext(ctx, &verified, "SELECT COUNT(*) FROM u_social_account WHERE platform = ? AND status = 'verified'", p)
		raw, _ := json.Marshal(live.Extra)
		sqlStr := `INSERT INTO u_social_channel_stat (platform, dt, members, secondary, posts, verified, raw) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE members = VALUES(members), secondary = VALUES(secondary), posts = VALUES(posts), verified = VALUES(verified), raw = VALUES(raw)`
		if !force {
			sqlStr = "INSERT IGNORE INTO u_social_channel_stat (platform, dt, members, secondary, posts, verified, raw) VALUES (?, ?, ?, ?, ?, ?, ?)"
		}
		if _, err := s.db.ExecContext(ctx, sqlStr, p, today, live.Members, live.Secondary, live.Posts, verified, string(raw)); err != nil {
			log.Printf("portalsocial: write snapshot %s failed: %v", p, err)
			res[p] = "write failed: " + err.Error()
			continue
		}
		res[p] = map[string]any{"members": live.Members, "secondary": live.Secondary, "posts": live.Posts, "verified": verified}
	}
	log.Printf("portalsocial: snapshot %s done", today)
	return res
}
