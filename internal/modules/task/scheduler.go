package task

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/robfig/cron/v3"

	"cow-manager-backend/internal/httpx"
)

// VO 任务列表项,字段与前端 TaskList 一致(时间为秒)。
type VO struct {
	Version                int    `json:"version"`
	Description            string `json:"description"`
	ClassName              string `json:"className"`
	ExecutedCount          int64  `json:"executedCount"`
	CronTrigger            string `json:"cronTrigger"`
	Enable                 bool   `json:"enable"`
	LastExecuteStartTime   int64  `json:"lastExecuteStartTime"`
	LastExecuteEndTime     int64  `json:"lastExecuteEndTime"`
	LastExecuteSuccess     bool   `json:"lastExecuteSuccess"`
	LastExecuteCostSeconds int64  `json:"lastExecuteCostSeconds"`
	LastLogSummary         string `json:"lastLogSummary"`
}

type entry struct {
	task    Task
	status  *Status
	entryID cron.EntryID
	running sync.Mutex
}

// Scheduler 任务调度器。
type Scheduler struct {
	store       *store
	loc         *time.Location
	defaultCron string
	cron        *cron.Cron
	parser      cron.Parser

	mu      sync.Mutex
	entries map[string]*entry
	order   []string
}

// NewScheduler 创建调度器;cron 表达式为 6 段(秒 分 时 日 月 周),与 Spring 一致。
func NewScheduler(gmsDB *sqlx.DB, loc *time.Location, defaultCron string) *Scheduler {
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	return &Scheduler{
		store:       &store{db: gmsDB},
		loc:         loc,
		defaultCron: defaultCron,
		cron:        cron.New(cron.WithParser(parser), cron.WithLocation(loc), cron.WithChain(cron.Recover(cron.DefaultLogger))),
		parser:      parser,
		entries:     map[string]*entry{},
	}
}

// Add 注册任务(需在 Start 前调用)。
func (s *Scheduler) Add(t Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[t.Name()] = &entry{task: t}
	s.order = append(s.order, t.Name())
}

// Start 加载状态并启动 cron。
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range s.order {
		e := s.entries[name]
		st, err := s.store.loadStatus(ctx, name, s.defaultCron)
		if err != nil {
			return fmt.Errorf("加载任务状态 %s: %w", name, err)
		}
		e.status = st
		if st.Enable {
			if err := s.scheduleLocked(e); err != nil {
				log.Printf("task: 任务 %s 的 cron %q 非法,已跳过: %v", name, st.CronTrigger, err)
			}
		}
	}
	s.cron.Start()
	log.Printf("task: 调度器已启动,%d 个任务", len(s.order))
	return nil
}

// Stop 停止 cron(等待运行中的任务完成)。
func (s *Scheduler) Stop() {
	<-s.cron.Stop().Done()
}

func (s *Scheduler) scheduleLocked(e *entry) error {
	if e.entryID != 0 {
		s.cron.Remove(e.entryID)
		e.entryID = 0
	}
	spec := strings.TrimSpace(e.status.CronTrigger)
	id, err := s.cron.AddFunc(spec, func() { s.runScheduled(e) })
	if err != nil {
		return err
	}
	e.entryID = id
	return nil
}

func (s *Scheduler) get(name string) (*entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[name]
	if !ok {
		return nil, httpx.NotFound("任务不存在: " + name)
	}
	return e, nil
}

// runScheduled 定时触发:统计昨天 [昨日 0 点, 今日 0 点)。
func (s *Scheduler) runScheduled(e *entry) {
	today := dayStart(time.Now(), s.loc)
	begin := today.AddDate(0, 0, -1)
	s.execute(e, begin.Unix(), today.Unix(), false, "@system")
}

// execute 执行一次并落日志;返回是否成功。
func (s *Scheduler) execute(e *entry, begin, end int64, manual bool, fromUser string) bool {
	e.running.Lock()
	defer e.running.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	lg := &Logger{}
	start := time.Now()
	lg.Info("开始执行 %s,区间 [%s, %s)", e.task.Description(),
		time.Unix(begin, 0).In(s.loc).Format("2006-01-02 15:04:05"), time.Unix(end, 0).In(s.loc).Format("2006-01-02 15:04:05"))
	err := e.task.Run(ctx, lg, begin, end)
	success := err == nil
	if err != nil {
		lg.Error("执行失败: %v", err)
	} else {
		lg.Info("执行完成,耗时 %s", time.Since(start).Round(time.Millisecond))
	}

	processedDate := ""
	if !manual && success {
		processedDate = time.Unix(begin, 0).In(s.loc).Format("2006-01-02")
	}
	if err := s.store.bumpExecuted(ctx, e.task.Name(), processedDate); err != nil {
		log.Printf("task: 更新执行次数失败 %s: %v", e.task.Name(), err)
	}
	s.mu.Lock()
	if e.status != nil {
		e.status.ExecutedCount++
		if processedDate != "" {
			e.status.ProcessedDate = processedDate
		}
	}
	s.mu.Unlock()

	rec := &Log{
		TaskName: e.task.Name(), Version: e.task.Version(),
		StartTime: start.Unix(), EndTime: time.Now().Unix(),
		TimeRangeStart: begin, TimeRangeEnd: end,
		ManualSchedule: manual, FromUser: fromUser, Success: success,
		LogSummary: lg.Summary(), DetailLogs: lg.JSON(),
	}
	if err := s.store.writeLog(ctx, rec); err != nil {
		log.Printf("task: 写执行日志失败 %s: %v", e.task.Name(), err)
	}
	log.Printf("task: %s [%d,%d) manual=%v success=%v", e.task.Name(), begin, end, manual, success)
	return success
}

// ManualSchedule 手动补跑 [begin 当日, end 当日] 的每一天,在后台顺序执行。
func (s *Scheduler) ManualSchedule(name string, begin, end time.Time, fromUser string) error {
	e, err := s.get(name)
	if err != nil {
		return err
	}
	first := dayStart(begin, s.loc)
	last := dayStart(end, s.loc)
	if last.Before(first) {
		return httpx.BadRequest("结束日期早于开始日期")
	}
	if last.Sub(first) > 400*24*time.Hour {
		return httpx.BadRequest("补跑区间不能超过 400 天")
	}
	go func() {
		for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
			s.execute(e, day.Unix(), day.AddDate(0, 0, 1).Unix(), true, fromUser)
		}
	}()
	return nil
}

// SetEnable 启停任务并重新调度。
func (s *Scheduler) SetEnable(ctx context.Context, name string, enable bool) error {
	e, err := s.get(name)
	if err != nil {
		return err
	}
	if err := s.store.setEnable(ctx, name, enable); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e.status.Enable = enable
	if enable {
		return s.scheduleLocked(e)
	}
	if e.entryID != 0 {
		s.cron.Remove(e.entryID)
		e.entryID = 0
	}
	return nil
}

// SetCron 修改定时规则(先校验表达式)。
func (s *Scheduler) SetCron(ctx context.Context, name, expr string) error {
	e, err := s.get(name)
	if err != nil {
		return err
	}
	expr = strings.TrimSpace(expr)
	if _, err := s.parser.Parse(expr); err != nil {
		return httpx.BadRequest("cron 表达式非法(需 6 段:秒 分 时 日 月 周): " + err.Error())
	}
	if err := s.store.setCron(ctx, name, expr); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e.status.CronTrigger = expr
	if e.status.Enable {
		return s.scheduleLocked(e)
	}
	return nil
}

// List 任务列表。
func (s *Scheduler) List(ctx context.Context) ([]VO, error) {
	s.mu.Lock()
	names := append([]string{}, s.order...)
	s.mu.Unlock()
	sort.Strings(names)
	out := make([]VO, 0, len(names))
	for _, name := range names {
		e := s.entries[name]
		s.mu.Lock()
		st := *e.status
		s.mu.Unlock()
		vo := VO{
			Version: e.task.Version(), Description: e.task.Description(), ClassName: name,
			ExecutedCount: st.ExecutedCount, CronTrigger: st.CronTrigger, Enable: st.Enable,
		}
		if last, err := s.store.lastLog(ctx, name); err == nil && last != nil {
			vo.LastExecuteStartTime = last.StartTime
			vo.LastExecuteEndTime = last.EndTime
			vo.LastExecuteSuccess = last.Success
			vo.LastExecuteCostSeconds = last.EndTime - last.StartTime
			vo.LastLogSummary = last.LogSummary
		}
		out = append(out, vo)
	}
	return out, nil
}

// Logs 执行日志(倒序分页)。
func (s *Scheduler) Logs(ctx context.Context, name string, page, size int) ([]Log, error) {
	if _, err := s.get(name); err != nil {
		return nil, err
	}
	return s.store.logs(ctx, name, page, size)
}
