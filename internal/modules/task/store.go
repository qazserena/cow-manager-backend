package task

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
)

// Status task_status 一行。
type Status struct {
	TaskName      string `db:"taskName"`
	CronTrigger   string `db:"cronTrigger"`
	Enable        bool   `db:"enable"`
	ProcessedDate string `db:"processedDate"`
	ExecutedCount int64  `db:"executedCount"`
}

// Log task_log 一行(字段名与前端 TaskDetailLog 一致;时间为秒)。
type Log struct {
	ID             int64  `db:"id" json:"id"`
	TaskName       string `db:"taskName" json:"taskName"`
	Version        int    `db:"version" json:"version"`
	StartTime      int64  `db:"startTime" json:"startTime"`
	EndTime        int64  `db:"endTime" json:"endTime"`
	TimeRangeStart int64  `db:"timeRangeStart" json:"timeRangeStart"`
	TimeRangeEnd   int64  `db:"timeRangeEnd" json:"timeRangeEnd"`
	ManualSchedule bool   `db:"manualSchedule" json:"manualSchedule"`
	FromUser       string `db:"fromUser" json:"fromUser"`
	Success        bool   `db:"success" json:"success"`
	LogSummary     string `db:"logSummary" json:"logSummary"`
	DetailLogs     string `db:"detailLogs" json:"detailLogs"`
}

type store struct{ db *sqlx.DB }

// loadStatus 读取任务状态;不存在则按默认值插入。
func (s *store) loadStatus(ctx context.Context, name, defaultCron string) (*Status, error) {
	st := &Status{}
	err := s.db.GetContext(ctx, st, "SELECT taskName, cronTrigger, enable, IFNULL(DATE_FORMAT(processedDate, '%Y-%m-%d'), '') AS processedDate, executedCount FROM task_status WHERE taskName = ?", name)
	if errors.Is(err, sql.ErrNoRows) {
		st = &Status{TaskName: name, CronTrigger: defaultCron, Enable: false}
		_, err = s.db.ExecContext(ctx, "INSERT INTO task_status (taskName, cronTrigger, enable, processedDate, executedCount) VALUES (?, ?, 0, NULL, 0)", name, defaultCron)
		return st, err
	}
	if err != nil {
		return nil, err
	}
	if st.CronTrigger == "" {
		st.CronTrigger = defaultCron
	}
	return st, nil
}

func (s *store) setEnable(ctx context.Context, name string, enable bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE task_status SET enable = ? WHERE taskName = ?", enable, name)
	return err
}

func (s *store) setCron(ctx context.Context, name, cron string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE task_status SET cronTrigger = ? WHERE taskName = ?", cron, name)
	return err
}

// bumpExecuted 累计执行次数并记录处理到的日期(手动补跑不推进 processedDate)。
func (s *store) bumpExecuted(ctx context.Context, name, processedDate string) error {
	if processedDate == "" {
		_, err := s.db.ExecContext(ctx, "UPDATE task_status SET executedCount = executedCount + 1 WHERE taskName = ?", name)
		return err
	}
	_, err := s.db.ExecContext(ctx, "UPDATE task_status SET executedCount = executedCount + 1, processedDate = ? WHERE taskName = ?", processedDate, name)
	return err
}

func (s *store) writeLog(ctx context.Context, l *Log) error {
	_, err := s.db.NamedExecContext(ctx, `INSERT INTO task_log (taskName, version, startTime, endTime, timeRangeStart, timeRangeEnd, manualSchedule, fromUser, success, logSummary, detailLogs)
		VALUES (:taskName, :version, :startTime, :endTime, :timeRangeStart, :timeRangeEnd, :manualSchedule, :fromUser, :success, :logSummary, :detailLogs)`, l)
	return err
}

func (s *store) logs(ctx context.Context, name string, page, size int) ([]Log, error) {
	list := []Log{}
	err := s.db.SelectContext(ctx, &list, "SELECT id, taskName, version, startTime, endTime, timeRangeStart, timeRangeEnd, manualSchedule, fromUser, success, logSummary, IFNULL(detailLogs, '') AS detailLogs FROM task_log WHERE taskName = ? ORDER BY id DESC LIMIT ? OFFSET ?",
		name, size, page*size)
	return list, err
}

func (s *store) lastLog(ctx context.Context, name string) (*Log, error) {
	l := &Log{}
	err := s.db.GetContext(ctx, l, "SELECT id, taskName, version, startTime, endTime, timeRangeStart, timeRangeEnd, manualSchedule, fromUser, success, logSummary, '' AS detailLogs FROM task_log WHERE taskName = ? ORDER BY id DESC LIMIT 1", name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return l, err
}
