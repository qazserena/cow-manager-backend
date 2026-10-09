// Package task 实现定时统计任务:调度(cron)、执行日志、手动补跑,
// 以及三个牧场离线统计任务(斗牛场、留存、用户)。
//
// 任务名沿用原 Java 类全名,task_status / task_log 既有数据无需迁移。
package task

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Task 一个可按天执行的统计任务。
type Task interface {
	Name() string
	Description() string
	Version() int
	// Run 统计 [begin, end) 秒级区间;日志写入 lg。
	Run(ctx context.Context, lg *Logger, begin, end int64) error
}

// LogLine 一条详细日志(tick 为毫秒)。
type LogLine struct {
	Level string `json:"level"`
	Tick  int64  `json:"tick"`
	Log   string `json:"log"`
}

// Logger 收集任务执行过程中的日志。
type Logger struct {
	mu    sync.Mutex
	lines []LogLine
}

func (l *Logger) add(level, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, LogLine{Level: level, Tick: time.Now().UnixMilli(), Log: fmt.Sprintf(format, args...)})
}

// Info 信息日志。
func (l *Logger) Info(format string, args ...any) { l.add("INFO", format, args...) }

// Warn 警告日志。
func (l *Logger) Warn(format string, args ...any) { l.add("WARN", format, args...) }

// Error 错误日志。
func (l *Logger) Error(format string, args ...any) { l.add("ERROR", format, args...) }

// JSON 序列化全部日志。
func (l *Logger) JSON() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.lines) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(l.lines)
	return string(b)
}

// Summary 最后一条日志文本(作为摘要)。
func (l *Logger) Summary() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.lines) == 0 {
		return ""
	}
	s := l.lines[len(l.lines)-1].Log
	if len(s) > 1000 {
		s = s[:1000]
	}
	return s
}

// dayStart 返回 t 所在日 0 点(按 loc)。
func dayStart(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}
