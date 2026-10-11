package portalsocial

import (
	"net/http"
	"strings"
	"time"

	"cow-manager-backend/internal/httpx"
)

// 社交任务「率」报表：转化率 / 留存率 / 撤销率 + 按周 cohort + 撤销明细。
//
// 口径（分母都写清楚，避免概览页只有计数时各人口算不一致）：
//   - 绑定转化率   = 回调完成的 state 数 / 发起绑定的 state 数（u_social_oauth_state，Telegram 深链也算）
//   - 关注转化率   = 曾确认关注 / 入群（verified_at 非空）/ 绑定记录数
//   - 7 天留存率   = 到达 7 天时仍在 / 曾确认且确认距今 ≥ 7 天
//     「到达 7 天时仍在」= keep7_at 非空（官网到期复查发了留存分，被过滤的账号不会有）或 当前仍 verified
//   - 30 天留存率  = 同上，30 天
//   - 撤销率       = 已取关 / 退群（revoked）/ 曾确认关注
//   - 扣回积分     = social_revoke 流水绝对值之和
//
// cohort 按「确认关注所在周（周一起算）」分组，看每一批进来的人 7 / 30 天后还剩多少。

// PlatformRates 一个平台的转化 / 留存 / 撤销。
type PlatformRates struct {
	Platform     string `json:"platform"`
	Started      int64  `json:"started"`
	Completed    int64  `json:"completed"`
	Accounts     int64  `json:"accounts"`
	EverVerified int64  `json:"everVerified"`
	Verified     int64  `json:"verified"`
	AuthLost     int64  `json:"authLost"`
	Revoked      int64  `json:"revoked"`
	Eligible7    int64  `json:"eligible7"`
	Kept7        int64  `json:"kept7"`
	Eligible30   int64  `json:"eligible30"`
	Kept30       int64  `json:"kept30"`
	ClawedBack   int64  `json:"clawedBack"`
	// 比率 0~1，分母为 0 时为 -1（前端显示 "-"）
	BindRate   float64 `json:"bindRate"`
	VerifyRate float64 `json:"verifyRate"`
	Keep7Rate  float64 `json:"keep7Rate"`
	Keep30Rate float64 `json:"keep30Rate"`
	RevokeRate float64 `json:"revokeRate"`
	// 平均持有天数：revoked 账号从确认到取关的平均天数（看"领完就跑"集中在哪一档）
	AvgHeldDaysRevoked float64 `json:"avgHeldDaysRevoked"`
}

// Cohort 某平台某一周确认关注的一批账号的留存。
type Cohort struct {
	Week       string  `json:"week"` // 周一日期
	Platform   string  `json:"platform"`
	Size       int64   `json:"size"`
	Eligible7  int64   `json:"eligible7"`
	Kept7      int64   `json:"kept7"`
	Eligible30 int64   `json:"eligible30"`
	Kept30     int64   `json:"kept30"`
	Revoked    int64   `json:"revoked"`
	Keep7Rate  float64 `json:"keep7Rate"`
	Keep30Rate float64 `json:"keep30Rate"`
	RevokeRate float64 `json:"revokeRate"`
}

// RatesReport GET /portal/social/rates
type RatesReport struct {
	Platforms   []*PlatformRates `json:"platforms"`
	Cohorts     []*Cohort        `json:"cohorts"`
	Weeks       int              `json:"weeks"`
	GeneratedAt int64            `json:"generatedAt"`
}

func ratio(a, b int64) float64 {
	if b <= 0 {
		return -1
	}
	return float64(a) / float64(b)
}

func (s *Service) rates(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	weeks := httpx.QueryInt(r, "weeks", 12)
	if weeks < 1 {
		weeks = 1
	}
	if weeks > 52 {
		weeks = 52
	}
	out := &RatesReport{Weeks: weeks, GeneratedAt: time.Now().Unix()}

	type row struct {
		Platform     string  `db:"platform"`
		Accounts     int64   `db:"accounts"`
		EverVerified int64   `db:"ever_verified"`
		Verified     int64   `db:"verified"`
		AuthLost     int64   `db:"auth_lost"`
		Revoked      int64   `db:"revoked"`
		Eligible7    int64   `db:"eligible7"`
		Kept7        int64   `db:"kept7"`
		Eligible30   int64   `db:"eligible30"`
		Kept30       int64   `db:"kept30"`
		AvgHeld      float64 `db:"avg_held"`
	}
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, `
		SELECT platform, COUNT(*) AS accounts,
		  COALESCE(SUM(verified_at IS NOT NULL), 0) AS ever_verified,
		  COALESCE(SUM(status = 'verified'), 0) AS verified,
		  COALESCE(SUM(status = 'auth_lost'), 0) AS auth_lost,
		  COALESCE(SUM(status = 'revoked'), 0) AS revoked,
		  COALESCE(SUM(verified_at IS NOT NULL AND verified_at <= NOW() - INTERVAL 7 DAY), 0) AS eligible7,
		  COALESCE(SUM(verified_at IS NOT NULL AND verified_at <= NOW() - INTERVAL 7 DAY AND (keep7_at IS NOT NULL OR status = 'verified')), 0) AS kept7,
		  COALESCE(SUM(verified_at IS NOT NULL AND verified_at <= NOW() - INTERVAL 30 DAY), 0) AS eligible30,
		  COALESCE(SUM(verified_at IS NOT NULL AND verified_at <= NOW() - INTERVAL 30 DAY AND (keep30_at IS NOT NULL OR status = 'verified')), 0) AS kept30,
		  COALESCE(AVG(CASE WHEN status = 'revoked' AND verified_at IS NOT NULL AND revoked_at IS NOT NULL
		                    THEN TIMESTAMPDIFF(HOUR, verified_at, revoked_at) / 24 END), 0) AS avg_held
		FROM u_social_account GROUP BY platform`); err != nil {
		return err
	}
	byP := map[string]row{}
	for _, x := range rows {
		byP[x.Platform] = x
	}

	var starts []struct {
		Platform  string `db:"platform"`
		Total     int64  `db:"total"`
		Completed int64  `db:"completed"`
	}
	_ = s.db.SelectContext(ctx, &starts, "SELECT platform, COUNT(*) AS total, COALESCE(SUM(used_at IS NOT NULL), 0) AS completed FROM u_social_oauth_state GROUP BY platform")

	var claws []struct {
		Ref string `db:"ref"`
		Pts int64  `db:"pts"`
	}
	_ = s.db.SelectContext(ctx, &claws, "SELECT ref, COALESCE(SUM(-points), 0) AS pts FROM u_invite_point_log WHERE kind = 'social_revoke' GROUP BY ref")

	for _, p := range Platforms {
		x := byP[p]
		pr := &PlatformRates{
			Platform: p, Accounts: x.Accounts, EverVerified: x.EverVerified, Verified: x.Verified,
			AuthLost: x.AuthLost, Revoked: x.Revoked,
			Eligible7: x.Eligible7, Kept7: x.Kept7, Eligible30: x.Eligible30, Kept30: x.Kept30,
			AvgHeldDaysRevoked: x.AvgHeld,
		}
		for _, st := range starts {
			if st.Platform == p {
				pr.Started, pr.Completed = st.Total, st.Completed
			}
		}
		for _, c := range claws {
			if c.Ref == p {
				pr.ClawedBack = c.Pts
			}
		}
		pr.BindRate = ratio(pr.Completed, pr.Started)
		pr.VerifyRate = ratio(pr.EverVerified, pr.Accounts)
		pr.Keep7Rate = ratio(pr.Kept7, pr.Eligible7)
		pr.Keep30Rate = ratio(pr.Kept30, pr.Eligible30)
		pr.RevokeRate = ratio(pr.Revoked, pr.EverVerified)
		out.Platforms = append(out.Platforms, pr)
	}

	// 按周 cohort：周一为一周起点
	type crow struct {
		Week       string `db:"wk"`
		Platform   string `db:"platform"`
		Size       int64  `db:"size"`
		Eligible7  int64  `db:"eligible7"`
		Kept7      int64  `db:"kept7"`
		Eligible30 int64  `db:"eligible30"`
		Kept30     int64  `db:"kept30"`
		Revoked    int64  `db:"revoked"`
	}
	var crows []crow
	since := time.Now().In(s.loc).AddDate(0, 0, -7*weeks).Format("2006-01-02")
	if err := s.db.SelectContext(ctx, &crows, `
		SELECT DATE_FORMAT(DATE_SUB(DATE(verified_at), INTERVAL WEEKDAY(verified_at) DAY), '%Y-%m-%d') AS wk, platform,
		  COUNT(*) AS size,
		  COALESCE(SUM(verified_at <= NOW() - INTERVAL 7 DAY), 0) AS eligible7,
		  COALESCE(SUM(verified_at <= NOW() - INTERVAL 7 DAY AND (keep7_at IS NOT NULL OR status = 'verified')), 0) AS kept7,
		  COALESCE(SUM(verified_at <= NOW() - INTERVAL 30 DAY), 0) AS eligible30,
		  COALESCE(SUM(verified_at <= NOW() - INTERVAL 30 DAY AND (keep30_at IS NOT NULL OR status = 'verified')), 0) AS kept30,
		  COALESCE(SUM(status = 'revoked'), 0) AS revoked
		FROM u_social_account
		WHERE verified_at IS NOT NULL AND verified_at >= ?
		GROUP BY wk, platform ORDER BY wk ASC, platform ASC`, since); err != nil {
		return err
	}
	out.Cohorts = make([]*Cohort, 0, len(crows))
	for _, c := range crows {
		out.Cohorts = append(out.Cohorts, &Cohort{
			Week: c.Week, Platform: c.Platform, Size: c.Size,
			Eligible7: c.Eligible7, Kept7: c.Kept7, Eligible30: c.Eligible30, Kept30: c.Kept30, Revoked: c.Revoked,
			Keep7Rate: ratio(c.Kept7, c.Eligible7), Keep30Rate: ratio(c.Kept30, c.Eligible30), RevokeRate: ratio(c.Revoked, c.Size),
		})
	}
	httpx.OK(w, out)
	return nil
}

// Revocation 一条撤销记录：谁、哪个平台、持有了多久、扣回多少。
type Revocation struct {
	Platform   string `json:"platform"`
	Address    string `json:"address"`
	Handle     string `json:"handle"`
	Name       string `json:"name"`
	Flag       string `json:"flag"`
	BindIP     string `json:"bindIp"`
	VerifiedAt string `json:"verifiedAt"`
	RevokedAt  string `json:"revokedAt"`
	HeldDays   int64  `json:"heldDays"`
	ClawedBack int64  `json:"clawedBack"` // 扣回积分（正数）；被过滤的账号没分可扣，为 0
	Keep7      bool   `json:"keep7"`      // 取关前是否已拿到 7 天留存分
	Keep30     bool   `json:"keep30"`
}

// revocations GET /portal/social/revocations?platform=&limit=
func (s *Service) revocations(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	limit := httpx.QueryInt(r, "limit", 50)
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	platform := strings.TrimSpace(r.URL.Query().Get("platform"))
	args := []interface{}{}
	where := "a.status = 'revoked'"
	if platform != "" {
		where += " AND a.platform = ?"
		args = append(args, platform)
	}
	args = append(args, limit)

	var rows []struct {
		Platform   string `db:"platform"`
		Address    string `db:"address"`
		Handle     string `db:"handle"`
		Name       string `db:"name"`
		Flag       string `db:"flag"`
		BindIP     string `db:"bind_ip"`
		VerifiedAt string `db:"verified_at"`
		RevokedAt  string `db:"revoked_at"`
		HeldDays   int64  `db:"held_days"`
		Clawed     int64  `db:"clawed"`
		Keep7      int64  `db:"keep7"`
		Keep30     int64  `db:"keep30"`
	}
	if err := s.db.SelectContext(ctx, &rows, `
		SELECT a.platform, a.address, a.handle, a.name, a.flag, a.bind_ip,
		  COALESCE(DATE_FORMAT(a.verified_at, '%Y-%m-%d %H:%i:%s'), '') AS verified_at,
		  COALESCE(DATE_FORMAT(a.revoked_at, '%Y-%m-%d %H:%i:%s'), '') AS revoked_at,
		  COALESCE(TIMESTAMPDIFF(DAY, a.verified_at, a.revoked_at), 0) AS held_days,
		  COALESCE((SELECT SUM(-l.points) FROM u_invite_point_log l
		            WHERE l.address = a.address AND l.ref = a.platform AND l.kind = 'social_revoke'), 0) AS clawed,
		  (a.keep7_at IS NOT NULL) AS keep7, (a.keep30_at IS NOT NULL) AS keep30
		FROM u_social_account a
		WHERE `+where+`
		ORDER BY a.revoked_at DESC
		LIMIT ?`, args...); err != nil {
		return err
	}
	out := make([]*Revocation, 0, len(rows))
	for _, x := range rows {
		out = append(out, &Revocation{
			Platform: x.Platform, Address: x.Address, Handle: x.Handle, Name: x.Name, Flag: x.Flag, BindIP: x.BindIP,
			VerifiedAt: x.VerifiedAt, RevokedAt: x.RevokedAt, HeldDays: x.HeldDays, ClawedBack: x.Clawed,
			Keep7: x.Keep7 == 1, Keep30: x.Keep30 == 1,
		})
	}
	httpx.OK(w, out)
	return nil
}
