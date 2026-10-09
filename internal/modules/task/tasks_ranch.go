package task

import (
	"context"
	"math/big"
	"time"

	"github.com/jmoiron/sqlx"
)

// 任务名沿用 Java 类全名,保证 task_status / task_log 历史数据连续。
const (
	NameBullring  = "com.brilljoy.olap.task.ranch.bullring.TaskBullringRanch"
	NameRetention = "com.brilljoy.olap.task.ranch.retention.TaskRetentionRanch"
	NameUser      = "com.brilljoy.olap.task.ranch.user.TaskUserRanch"
)

// ranchDeps 三个统计任务共用的依赖:日志库(源)、gms 库(目标)、服务器 ID、时区。
type ranchDeps struct {
	logDB    *sqlx.DB
	gmsDB    *sqlx.DB
	serverID int
	loc      *time.Location
}

// NewRanchTasks 构造三个牧场统计任务。
func NewRanchTasks(logDB, gmsDB *sqlx.DB, serverID int, loc *time.Location) []Task {
	d := &ranchDeps{logDB: logDB, gmsDB: gmsDB, serverID: serverID, loc: loc}
	return []Task{&bullringTask{d}, &retentionTask{d}, &userTask{d}}
}

// ---------- 斗牛场:场次 + 奖励 ----------

type bullringTask struct{ *ranchDeps }

func (t *bullringTask) Name() string        { return NameBullring }
func (t *bullringTask) Description() string { return "斗牛场统计(场次/胜场/奖励)" }
func (t *bullringTask) Version() int        { return 1 }

// gameType → 页签:PVE(1)→3, LADDERS(2)→1, MATCH_1/3/5(3,4,5)→2。
func bullringTab(gameType int64) (int64, bool) {
	switch gameType {
	case 1:
		return 3, true
	case 2:
		return 1, true
	case 3, 4, 5:
		return 2, true
	}
	return 0, false
}

func (t *bullringTask) Run(ctx context.Context, lg *Logger, begin, end int64) error {
	rows, err := t.logDB.QueryxContext(ctx, `SELECT gameType, COUNT(DISTINCT gameID) AS cnt, IFNULL(SUM(isWinning), 0) AS wins
		FROM log_bullring WHERE createTs >= ? AND createTs < ? AND gameType >= 1 AND gameType <= 5 GROUP BY gameType`, begin, end)
	if err != nil {
		return err
	}
	type agg struct{ count, wins int64 }
	tabs := map[int64]*agg{}
	for rows.Next() {
		var gameType, cnt, wins int64
		if err := rows.Scan(&gameType, &cnt, &wins); err != nil {
			rows.Close()
			return err
		}
		tab, ok := bullringTab(gameType)
		if !ok {
			continue
		}
		a := tabs[tab]
		if a == nil {
			a = &agg{}
			tabs[tab] = a
		}
		a.count += cnt
		a.wins += wins
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for tab, a := range tabs {
		if _, err := t.gmsDB.ExecContext(ctx, `INSERT INTO da_ranch_bullring_count (dateTs, serverID, gameTabType, count, wins) VALUES (?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE count = VALUES(count), wins = VALUES(wins)`, begin, t.serverID, tab, a.count, a.wins); err != nil {
			return err
		}
	}
	lg.Info("场次统计写入 %d 个页签", len(tabs))

	rrows, err := t.logDB.QueryxContext(ctx, `SELECT gameTabType, tokenT, tokenG FROM log_bullring_rewards WHERE createTs >= ? AND createTs < ?`, begin, end)
	if err != nil {
		return err
	}
	type sum struct{ t, g *big.Int }
	rewards := map[int64]*sum{}
	for rrows.Next() {
		var tab int64
		var tokenT, tokenG string
		if err := rrows.Scan(&tab, &tokenT, &tokenG); err != nil {
			rrows.Close()
			return err
		}
		s := rewards[tab]
		if s == nil {
			s = &sum{t: new(big.Int), g: new(big.Int)}
			rewards[tab] = s
		}
		if v, ok := new(big.Int).SetString(tokenT, 10); ok {
			s.t.Add(s.t, v)
		}
		if v, ok := new(big.Int).SetString(tokenG, 10); ok {
			s.g.Add(s.g, v)
		}
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return err
	}
	for tab, s := range rewards {
		if _, err := t.gmsDB.ExecContext(ctx, `INSERT INTO da_ranch_bullring_rewards (dateTs, serverID, gameTabType, tokenT, tokenG) VALUES (?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE tokenT = VALUES(tokenT), tokenG = VALUES(tokenG)`, begin, t.serverID, tab, s.t.String(), s.g.String()); err != nil {
			return err
		}
	}
	lg.Info("奖励统计写入 %d 个页签", len(rewards))
	return nil
}

// ---------- 留存 ----------

type retentionTask struct{ *ranchDeps }

func (t *retentionTask) Name() string        { return NameRetention }
func (t *retentionTask) Description() string { return "用户留存统计(按加入公会日)" }
func (t *retentionTask) Version() int        { return 1 }

func (t *retentionTask) Run(ctx context.Context, lg *Logger, begin, end int64) error {
	rows, err := t.logDB.QueryxContext(ctx, `SELECT DISTINCT uid, joinGuildTs FROM log_login
		WHERE (loginTs >= ? AND loginTs < ? AND joinGuildTs > 0) OR (logoutTs >= ? AND logoutTs < ? AND joinGuildTs > 0)`, begin, end, begin, end)
	if err != nil {
		return err
	}
	counts := map[int64]int64{} // 距加入公会的天数(0~6) → 人数
	total := 0
	for rows.Next() {
		var uid, joinTs int64
		if err := rows.Scan(&uid, &joinTs); err != nil {
			rows.Close()
			return err
		}
		total++
		joinDay := dayStart(time.Unix(joinTs, 0), t.loc).Unix()
		days := (begin - joinDay) / 86400
		if days >= 0 && days <= 6 {
			counts[days]++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for days, n := range counts {
		col := "r" + string(rune('1'+days))
		dt := time.Unix(begin-days*86400, 0).In(t.loc).Format("2006-01-02")
		if _, err := t.gmsDB.ExecContext(ctx, "INSERT INTO da_ranch_retention (dt, serverID, `"+col+"`) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE `"+col+"` = VALUES(`"+col+"`)",
			dt, t.serverID, n); err != nil {
			return err
		}
	}
	lg.Info("活跃用户 %d 人,回写 %d 个留存列", total, len(counts))
	return nil
}

// ---------- 用户:新增 / 活跃 / 七日流失 ----------

type userTask struct{ *ranchDeps }

func (t *userTask) Name() string        { return NameUser }
func (t *userTask) Description() string { return "用户统计(新增/活跃/七日流失)" }
func (t *userTask) Version() int        { return 1 }

func (t *userTask) distinctUIDs(ctx context.Context, begin, end int64) (map[int64]struct{}, error) {
	rows, err := t.logDB.QueryxContext(ctx, "SELECT DISTINCT uid FROM log_login WHERE loginTs >= ? AND loginTs < ? AND joinGuildTs > 0", begin, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[int64]struct{}{}
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		set[uid] = struct{}{}
	}
	return set, rows.Err()
}

func (t *userTask) Run(ctx context.Context, lg *Logger, begin, end int64) error {
	var dnu, dau int64
	if err := t.logDB.GetContext(ctx, &dnu, "SELECT COUNT(DISTINCT uid) FROM log_login WHERE loginTs >= ? AND loginTs < ? AND joinGuildTs >= ? AND joinGuildTs < ?", begin, end, begin, end); err != nil {
		return err
	}
	if err := t.logDB.GetContext(ctx, &dau, "SELECT COUNT(DISTINCT uid) FROM log_login WHERE loginTs >= ? AND loginTs < ? AND joinGuildTs > 0", begin, end); err != nil {
		return err
	}
	week := int64(7 * 86400)
	before, err := t.distinctUIDs(ctx, begin-week, end-week) // 7 天前活跃
	if err != nil {
		return err
	}
	recent, err := t.distinctUIDs(ctx, end-week, end) // 最近 7 天活跃
	if err != nil {
		return err
	}
	churn := int64(0)
	for uid := range before {
		if _, ok := recent[uid]; !ok {
			churn++
		}
	}
	if _, err := t.gmsDB.ExecContext(ctx, `INSERT INTO da_ranch_user (dateTs, serverID, dnu, dau, churn7) VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE dnu = VALUES(dnu), dau = VALUES(dau), churn7 = VALUES(churn7)`, begin, t.serverID, dnu, dau, churn); err != nil {
		return err
	}
	lg.Info("dnu=%d dau=%d churn7=%d", dnu, dau, churn)
	return nil
}
