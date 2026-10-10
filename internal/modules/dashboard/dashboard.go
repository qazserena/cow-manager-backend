// Package dashboard 提供「概览」页的核心指标:把游戏库 / 日志库 / GMS 库 / 官网库里
// 最能反映系统状态的几十个数字和近 30 天趋势一次性汇总出来。
//
// 只做轻量 COUNT / 分组查询,每一节独立容错(某个库不可用或查询失败只置空、不影响其它节),
// 结果缓存 60 秒,避免概览页被频繁刷新打爆数据库。
package dashboard

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"cow-manager-backend/internal/auth"
	"cow-manager-backend/internal/httpx"
	"cow-manager-backend/internal/perm"
)

// Service 概览服务。portal 可为 nil(未配置官网库)。
type Service struct {
	game   *sqlx.DB
	logdb  *sqlx.DB
	gms    *sqlx.DB
	portal *sqlx.DB
	loc    *time.Location

	mu     sync.Mutex
	cached *Summary
	at     time.Time
}

const cacheTTL = 60 * time.Second

// NewService 创建服务。
func NewService(game, logdb, gms, portal *sqlx.DB, loc *time.Location) *Service {
	return &Service{game: game, logdb: logdb, gms: gms, portal: portal, loc: loc}
}

// Register 挂载路由(登录即可看,但只下发有「查看」权限的板块)。
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /dashboard/summary", httpx.H(auth.RequireLogin(func(w http.ResponseWriter, r *http.Request) error {
		httpx.OK(w, s.summary(r.Context(), httpx.QueryBool(r, "refresh")).forSession(auth.SessionFrom(r.Context())))
		return nil
	})))
}

// auditFeature 待审核模块 → 功能 key。
var auditFeature = map[string]string{"mail": "mail", "group-mail": "group-mail", "check-in": "game-config", "guild-battle": "guild-battle"}

// forSession 按权限裁剪副本:没有相应功能「查看」权限的板块置空 / 归零,缓存里的原件不动。
func (sum *Summary) forSession(sess *auth.Session) *Summary {
	anyOf := func(codes ...string) bool {
		for _, c := range codes {
			if sess.Has(c) {
				return true
			}
		}
		return false
	}
	game := anyOf(perm.RanchUserView, perm.RanchGuildView, perm.AnalysisView)
	portal := anyOf(perm.PortalUserView, perm.PortalSocialView, perm.PortalInviteView)

	out := *sum
	if !game {
		out.Game = nil
	}
	if !portal {
		out.Portal = nil
	}
	if sum.Ops != nil {
		ops := *sum.Ops
		ops.PendingAudits = map[string]int64{}
		for k, v := range sum.Ops.PendingAudits {
			if sess.Has(perm.View(auditFeature[k])) {
				ops.PendingAudits[k] = v
			}
		}
		if !sess.Has(perm.TaskView) {
			ops.Tasks = []TaskHealth{}
		}
		if !sess.Has(perm.PortalSocialView) {
			ops.SocialErrors = 0
		}
		if !sess.Has(perm.PortalInviteView) {
			ops.InviteFlagged, ops.InviteFlaggedToday = 0, 0
		}
		out.Ops = &ops
	}
	if !game || !portal {
		trend := make([]*TrendPoint, 0, len(sum.Trend))
		for _, t := range sum.Trend {
			c := *t
			if !game {
				c.DNU, c.DAU, c.Games = 0, 0, 0
			}
			if !portal {
				c.Trades, c.InviteBinds, c.SocialBinds, c.Points = 0, 0, 0, 0
			}
			trend = append(trend, &c)
		}
		out.Trend = trend
	}
	return &out
}

// Summary 概览全部数据。
type Summary struct {
	Game     *GameMetrics   `json:"game"`
	Portal   *PortalMetrics `json:"portal"` // 官网库未配置为 nil
	Ops      *OpsMetrics    `json:"ops"`
	Trend    []*TrendPoint  `json:"trend"` // 近 30 天
	Errors   []string       `json:"errors,omitempty"`
	CachedAt int64          `json:"cachedAt"`
}

// GameMetrics 游戏核心指标。
type GameMetrics struct {
	Users          int64 `json:"users"`          // 注册玩家
	NewToday       int64 `json:"newToday"`       // 今日新增
	New7d          int64 `json:"new7d"`          // 近 7 天新增
	DAU            int64 `json:"dau"`            // 今日活跃(有登录)
	WAU            int64 `json:"wau"`            // 近 7 天活跃
	MAU            int64 `json:"mau"`            // 近 30 天活跃
	Online         int64 `json:"online"`         // 当前在线(有登录未登出且 2 小时内)
	Guilds         int64 `json:"guilds"`         // 公会数
	InGuild        int64 `json:"inGuild"`        // 已入会玩家
	GamesToday     int64 `json:"gamesToday"`     // 今日斗牛场对局
	Games7d        int64 `json:"games7d"`        // 近 7 天对局
	TasksToday     int64 `json:"tasksToday"`     // 今日完成任务数
	RewardsPending int64 `json:"rewardsPending"` // 奖励中心未领取
	MailsUnread    int64 `json:"mailsUnread"`    // 未读邮件
}

// PortalMetrics 官网 / 链上资产指标。
type PortalMetrics struct {
	Profiles         int64 `json:"profiles"`         // 官网用户(连过钱包)
	CattleAlive      int64 `json:"cattleAlive"`      // 存活牛牛
	CattleTotal      int64 `json:"cattleTotal"`      // 牛牛总数(含死亡)
	Planets          int64 `json:"planets"`          // 星球数
	PlanetMembers    int64 `json:"planetMembers"`    // 星球成员数
	Listings         int64 `json:"listings"`         // 市场在售
	TradesToday      int64 `json:"tradesToday"`      // 今日成交
	Trades7d         int64 `json:"trades7d"`         // 近 7 天成交
	InviteUsers      int64 `json:"inviteUsers"`      // 领过邀请码
	InviteScorers    int64 `json:"inviteScorers"`    // 有积分
	InviteBindsToday int64 `json:"inviteBindsToday"` // 今日新绑定
	InviteValid      int64 `json:"inviteValid"`      // 有效邀请总数
	PointsTotal      int64 `json:"pointsTotal"`      // 累计发放积分
	PointsToday      int64 `json:"pointsToday"`      // 今日发放
	SocialVerified   int64 `json:"socialVerified"`   // 社交绑定(关注中)
	SocialWallets    int64 `json:"socialWallets"`    // 绑过社交的钱包
	BetaClaimsToday  int64 `json:"betaClaimsToday"`  // 今日公测领取签发
	BetaClaimers     int64 `json:"betaClaimers"`     // 领过公测资产的地址
	// 官方渠道最近一次快照(members):x / telegram / discord,没有为 0
	Channels map[string]int64 `json:"channels"`
}

// OpsMetrics 运营待办 / 系统健康。
type OpsMetrics struct {
	PendingAudits      map[string]int64 `json:"pendingAudits"` // mail / group-mail / check-in / guild-battle 待审核
	Tasks              []TaskHealth     `json:"tasks"`
	SocialErrors       int64            `json:"socialErrors"`       // 社交复查报错账号数
	InviteFlaggedToday int64            `json:"inviteFlaggedToday"` // 今日被反作弊标记的绑定
	InviteFlagged      int64            `json:"inviteFlagged"`
}

// TaskHealth 定时任务最近一次执行。
type TaskHealth struct {
	Name          string `json:"name"`
	Enable        bool   `json:"enable"`
	ProcessedDate string `json:"processedDate"`
	LastRunAt     int64  `json:"lastRunAt"`
	LastSuccess   bool   `json:"lastSuccess"`
	LastSummary   string `json:"lastSummary"`
}

// TrendPoint 某一天。
type TrendPoint struct {
	Date        string `json:"date"`
	DNU         int64  `json:"dnu"`
	DAU         int64  `json:"dau"`
	Games       int64  `json:"games"`
	Trades      int64  `json:"trades"`
	InviteBinds int64  `json:"inviteBinds"`
	SocialBinds int64  `json:"socialBinds"`
	Points      int64  `json:"points"`
}

func (s *Service) summary(ctx context.Context, refresh bool) *Summary {
	s.mu.Lock()
	if s.cached != nil && !refresh && time.Since(s.at) < cacheTTL {
		c := s.cached
		s.mu.Unlock()
		return c
	}
	s.mu.Unlock()

	out := &Summary{CachedAt: time.Now().Unix(), Trend: []*TrendPoint{}}
	fail := func(section string, err error) {
		log.Printf("dashboard: %s failed: %v", section, err)
		out.Errors = append(out.Errors, section)
	}
	var err error
	if out.Game, err = s.gameMetrics(ctx); err != nil {
		fail("game", err)
	}
	if s.portal != nil {
		if out.Portal, err = s.portalMetrics(ctx); err != nil {
			fail("portal", err)
		}
	}
	if out.Ops, err = s.opsMetrics(ctx); err != nil {
		fail("ops", err)
	}
	if out.Trend, err = s.trend(ctx, 30); err != nil {
		fail("trend", err)
	}

	s.mu.Lock()
	s.cached, s.at = out, time.Now()
	s.mu.Unlock()
	return out
}

func (s *Service) dayBounds(daysAgo int) (int64, int64) {
	now := time.Now().In(s.loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.loc).AddDate(0, 0, -daysAgo)
	return start.Unix(), start.AddDate(0, 0, daysAgo+1).Unix()
}

func (s *Service) gameMetrics(ctx context.Context) (*GameMetrics, error) {
	m := &GameMetrics{}
	todayStart, _ := s.dayBounds(0)
	d7Start, _ := s.dayBounds(6)
	d30Start, _ := s.dayBounds(29)
	now := time.Now().Unix()
	q := func(db *sqlx.DB, dst *int64, sqlStr string, args ...any) {
		if db == nil {
			return
		}
		if err := db.GetContext(ctx, dst, sqlStr, args...); err != nil {
			log.Printf("dashboard: %s: %v", sqlStr[:min(60, len(sqlStr))], err)
		}
	}
	q(s.game, &m.Users, "SELECT COUNT(*) FROM ranch_user")
	q(s.game, &m.NewToday, "SELECT COUNT(*) FROM ranch_user WHERE registerTs >= ?", todayStart)
	q(s.game, &m.New7d, "SELECT COUNT(*) FROM ranch_user WHERE registerTs >= ?", d7Start)
	q(s.game, &m.Guilds, "SELECT COUNT(*) FROM ranch_guild")
	q(s.game, &m.InGuild, "SELECT COUNT(*) FROM ranch_user WHERE guildId > 0")
	q(s.game, &m.TasksToday, "SELECT COUNT(*) FROM ranch_task WHERE finishTs >= ?", todayStart)
	q(s.game, &m.RewardsPending, "SELECT COUNT(*) FROM ranch_rewards_center WHERE drawTs = 0")
	q(s.game, &m.MailsUnread, "SELECT COUNT(*) FROM ranch_mail WHERE readTs = 0 AND deleteTs = 0")
	q(s.logdb, &m.DAU, "SELECT COUNT(DISTINCT uid) FROM log_login WHERE loginTs >= ?", todayStart)
	q(s.logdb, &m.WAU, "SELECT COUNT(DISTINCT uid) FROM log_login WHERE loginTs >= ?", d7Start)
	q(s.logdb, &m.MAU, "SELECT COUNT(DISTINCT uid) FROM log_login WHERE loginTs >= ?", d30Start)
	q(s.logdb, &m.Online, "SELECT COUNT(DISTINCT uid) FROM log_login WHERE logoutTs = 0 AND loginTs >= ?", now-2*3600)
	q(s.logdb, &m.GamesToday, "SELECT COUNT(DISTINCT gameID) FROM log_bullring WHERE createTs >= ?", todayStart)
	q(s.logdb, &m.Games7d, "SELECT COUNT(DISTINCT gameID) FROM log_bullring WHERE createTs >= ?", d7Start)
	return m, nil
}

func (s *Service) portalMetrics(ctx context.Context) (*PortalMetrics, error) {
	m := &PortalMetrics{Channels: map[string]int64{}}
	today := time.Now().In(s.loc).Format("2006-01-02")
	todayStart, _ := s.dayBounds(0)
	d7Start, _ := s.dayBounds(6)
	nowSec := time.Now().Unix()
	q := func(dst *int64, sqlStr string, args ...any) {
		if err := s.portal.GetContext(ctx, dst, sqlStr, args...); err != nil {
			log.Printf("dashboard: %s: %v", sqlStr[:min(60, len(sqlStr))], err)
		}
	}
	q(&m.Profiles, "SELECT COUNT(*) FROM u_profile")
	q(&m.CattleTotal, "SELECT COUNT(*) FROM u_cattle")
	q(&m.CattleAlive, "SELECT COUNT(*) FROM u_cattle WHERE dead_at = 0 OR dead_at > ?", nowSec)
	q(&m.Planets, "SELECT COUNT(*) FROM u_planet")
	q(&m.PlanetMembers, "SELECT COUNT(*) FROM u_planet_member")
	q(&m.Listings, "SELECT COUNT(*) FROM u_market")
	q(&m.TradesToday, "SELECT COUNT(*) FROM r_market WHERE block_time >= ?", todayStart)
	q(&m.Trades7d, "SELECT COUNT(*) FROM r_market WHERE block_time >= ?", d7Start)
	q(&m.InviteUsers, "SELECT COUNT(*) FROM u_invite_user")
	q(&m.InviteScorers, "SELECT COUNT(*) FROM u_invite_user WHERE points > 0")
	q(&m.InviteBindsToday, "SELECT COUNT(*) FROM u_invite_user WHERE inviter <> '' AND DATE(bind_at) = ?", today)
	q(&m.InviteValid, "SELECT COUNT(*) FROM u_invite_user WHERE inviter <> '' AND flag = '' AND stage >= 2")
	q(&m.PointsTotal, "SELECT COALESCE(SUM(points), 0) FROM u_invite_point_log")
	q(&m.PointsToday, "SELECT COALESCE(SUM(points), 0) FROM u_invite_point_log WHERE DATE(created_at) = ?", today)
	q(&m.SocialVerified, "SELECT COUNT(*) FROM u_social_account WHERE status = 'verified'")
	q(&m.SocialWallets, "SELECT COUNT(DISTINCT address) FROM u_social_account WHERE status IN ('bound','verified')")
	q(&m.BetaClaimsToday, "SELECT COUNT(*) FROM u_beta_claim_log WHERE DATE(created_at) = ?", today)
	q(&m.BetaClaimers, "SELECT COUNT(DISTINCT address) FROM u_beta_claim_log")

	var ch []struct {
		Platform string `db:"platform"`
		Members  int64  `db:"members"`
	}
	if err := s.portal.SelectContext(ctx, &ch, `SELECT c.platform, c.members FROM u_social_channel_stat c
		JOIN (SELECT platform, MAX(dt) AS dt FROM u_social_channel_stat GROUP BY platform) m ON m.platform = c.platform AND m.dt = c.dt`); err == nil {
		for _, x := range ch {
			m.Channels[x.Platform] = x.Members
		}
	}
	return m, nil
}

func (s *Service) opsMetrics(ctx context.Context) (*OpsMetrics, error) {
	m := &OpsMetrics{PendingAudits: map[string]int64{}, Tasks: []TaskHealth{}}
	if s.gms != nil {
		for key, table := range map[string]string{"mail": "gms_ranch_mail", "group-mail": "gms_ranch_group_mail", "check-in": "gms_ranch_check_in", "guild-battle": "gms_ranch_guild_battle"} {
			var n int64
			if err := s.gms.GetContext(ctx, &n, "SELECT COUNT(*) FROM `"+table+"` WHERE deletedTime = 0 AND auditStatus = 0"); err == nil {
				m.PendingAudits[key] = n
			}
		}
		var st []struct {
			TaskName      string `db:"taskName"`
			Enable        bool   `db:"enable"`
			ProcessedDate string `db:"processedDate"`
		}
		if err := s.gms.SelectContext(ctx, &st, "SELECT taskName, enable, IFNULL(DATE_FORMAT(processedDate, '%Y-%m-%d'), '') AS processedDate FROM task_status"); err == nil {
			for _, t := range st {
				th := TaskHealth{Name: t.TaskName, Enable: t.Enable, ProcessedDate: t.ProcessedDate}
				var last struct {
					EndTime    int64  `db:"endTime"`
					Success    bool   `db:"success"`
					LogSummary string `db:"logSummary"`
				}
				if err := s.gms.GetContext(ctx, &last, "SELECT endTime, success, IFNULL(logSummary, '') AS logSummary FROM task_log WHERE taskName = ? ORDER BY id DESC LIMIT 1", t.TaskName); err == nil {
					th.LastRunAt, th.LastSuccess, th.LastSummary = last.EndTime, last.Success, last.LogSummary
					if len(th.LastSummary) > 120 {
						th.LastSummary = th.LastSummary[:120]
					}
				}
				m.Tasks = append(m.Tasks, th)
			}
		}
	}
	if s.portal != nil {
		today := time.Now().In(s.loc).Format("2006-01-02")
		_ = s.portal.GetContext(ctx, &m.SocialErrors, "SELECT COUNT(*) FROM u_social_account WHERE last_check_err <> ''")
		_ = s.portal.GetContext(ctx, &m.InviteFlaggedToday, "SELECT COUNT(*) FROM u_invite_user WHERE flag <> '' AND DATE(bind_at) = ?", today)
		_ = s.portal.GetContext(ctx, &m.InviteFlagged, "SELECT COUNT(*) FROM u_invite_user WHERE flag <> ''")
	}
	return m, nil
}

func (s *Service) trend(ctx context.Context, days int) ([]*TrendPoint, error) {
	start, _ := s.dayBounds(days - 1)
	startDay := time.Unix(start, 0).In(s.loc)
	points := map[string]*TrendPoint{}
	order := make([]string, 0, days)
	for i := 0; i < days; i++ {
		d := startDay.AddDate(0, 0, i).Format("2006-01-02")
		points[d] = &TrendPoint{Date: d}
		order = append(order, d)
	}
	type dp struct {
		D string `db:"d"`
		N int64  `db:"n"`
	}
	fill := func(db *sqlx.DB, sqlStr string, arg any, set func(p *TrendPoint, n int64)) {
		if db == nil {
			return
		}
		var rows []dp
		if err := db.SelectContext(ctx, &rows, sqlStr, arg); err != nil {
			log.Printf("dashboard: trend %s: %v", sqlStr[:min(60, len(sqlStr))], err)
			return
		}
		for _, r := range rows {
			if p, ok := points[r.D]; ok {
				set(p, r.N)
			}
		}
	}
	// 秒级时间戳按本地时区换成日期
	tz := time.Now().In(s.loc).Format("-07:00")
	dateOf := func(col string) string {
		return "DATE(CONVERT_TZ(FROM_UNIXTIME(" + col + "), @@session.time_zone, '" + tz + "'))"
	}
	fill(s.game, "SELECT "+dateOf("registerTs")+" AS d, COUNT(*) AS n FROM ranch_user WHERE registerTs >= ? GROUP BY d", start, func(p *TrendPoint, n int64) { p.DNU = n })
	fill(s.logdb, "SELECT "+dateOf("loginTs")+" AS d, COUNT(DISTINCT uid) AS n FROM log_login WHERE loginTs >= ? GROUP BY d", start, func(p *TrendPoint, n int64) { p.DAU = n })
	fill(s.logdb, "SELECT "+dateOf("createTs")+" AS d, COUNT(DISTINCT gameID) AS n FROM log_bullring WHERE createTs >= ? GROUP BY d", start, func(p *TrendPoint, n int64) { p.Games = n })
	if s.portal != nil {
		startStr := startDay.Format("2006-01-02")
		fill(s.portal, "SELECT "+dateOf("block_time")+" AS d, COUNT(*) AS n FROM r_market WHERE block_time >= ? GROUP BY d", start, func(p *TrendPoint, n int64) { p.Trades = n })
		fill(s.portal, "SELECT DATE(bind_at) AS d, COUNT(*) AS n FROM u_invite_user WHERE inviter <> '' AND bind_at >= ? GROUP BY d", startStr, func(p *TrendPoint, n int64) { p.InviteBinds = n })
		fill(s.portal, "SELECT DATE(verified_at) AS d, COUNT(*) AS n FROM u_social_account WHERE verified_at >= ? GROUP BY d", startStr, func(p *TrendPoint, n int64) { p.SocialBinds = n })
		fill(s.portal, "SELECT DATE(created_at) AS d, COALESCE(SUM(points), 0) AS n FROM u_invite_point_log WHERE created_at >= ? GROUP BY d", startStr, func(p *TrendPoint, n int64) { p.Points = n })
	}
	out := make([]*TrendPoint, 0, days)
	for _, d := range order {
		out = append(out, points[d])
	}
	return out, nil
}
