// gms 是统一游戏管理平台(GMS)后端:原 auth-center / gms-ranch / task-runner
// 三个 Java 服务合并为一个 Go 进程,同一端口提供全部接口。
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cow-manager-backend/internal/auth"
	"cow-manager-backend/internal/config"
	"cow-manager-backend/internal/db"
	"cow-manager-backend/internal/httpx"
	"cow-manager-backend/internal/modules/analysis"
	"cow-manager-backend/internal/modules/authcenter"
	"cow-manager-backend/internal/modules/gms"
	"cow-manager-backend/internal/modules/portalinvite"
	"cow-manager-backend/internal/modules/ranchdata"
	"cow-manager-backend/internal/modules/task"
	"cow-manager-backend/internal/perm"
	"cow-manager-backend/internal/query"
)

func main() {
	configPath := flag.String("config", envOr("GMS_CONFIG", "config/dev.json"), "配置文件路径")
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}
	loc, err := time.LoadLocation(cfg.Ranch.DefaultTimeZone)
	if err != nil {
		log.Fatalf("时区 %q 非法: %v", cfg.Ranch.DefaultTimeZone, err)
	}
	taskLoc, err := time.LoadLocation(cfg.Task.TimeZone)
	if err != nil {
		log.Fatalf("任务时区 %q 非法: %v", cfg.Task.TimeZone, err)
	}

	conns, err := db.OpenAll(cfg)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer conns.Close()

	// 区域权限码(前端按 game/ranch/{region} 过滤可见区域)
	perm.Game(cfg.Ranch.RegionCode)

	// 授权中心 + 会话
	authSvc := authcenter.NewService(conns.Auth,
		time.Duration(cfg.Auth.TokenTTLHours)*time.Hour,
		time.Duration(cfg.Auth.TokenRefreshHours)*time.Hour)
	sessions := auth.NewManager(authSvc)
	authSvc.SetSessions(sessions)

	// 导出时的枚举翻译(meta_enum_item)
	var labeler query.EnumLabeler = func(code string, v int64) (string, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return authSvc.Repo().EnumLabel(ctx, code, v)
	}

	gmsSvc := gms.NewService(cfg, conns.Gms, conns.Tpl, loc, labeler)
	dataSvc := ranchdata.NewService(conns.Game, conns.Log, loc, labeler)
	analysisSvc := analysis.NewService(conns.Gms, loc, labeler)

	scheduler := task.NewScheduler(conns.Gms, taskLoc, cfg.Task.DefaultCron)
	for _, t := range task.NewRanchTasks(conns.Log, conns.Gms, cfg.Ranch.ServerID, taskLoc) {
		scheduler.Add(t)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.Text(w, http.StatusOK, "ok")
	})
	authSvc.Register(mux)
	gmsSvc.Register(mux)
	dataSvc.Register(mux)
	analysisSvc.Register(mux)
	scheduler.Register(mux)
	// 官网公测邀请计划(临时挂在 GMS;将来随 internal/modules/portalinvite 一起迁到官网管理后端)
	if conns.Portal != nil {
		portalinvite.NewService(conns.Portal, loc, labeler).Register(mux)
	} else {
		log.Printf("未配置 databases.portal,官网邀请计划管理接口(/portal/invite/*)未挂载")
	}

	handler := httpx.Chain(mux, httpx.Recover, httpx.Logging, httpx.CORS, sessions.Authenticate)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.Task.Enabled {
		if err := scheduler.Start(ctx); err != nil {
			log.Fatalf("启动任务调度失败: %v", err)
		}
		defer scheduler.Stop()
	} else {
		// 不跑定时,但仍要加载状态以便前端查看/手动补跑
		if err := scheduler.Start(ctx); err != nil {
			log.Printf("加载任务状态失败: %v", err)
		}
		log.Printf("task.enabled=false,仅支持手动补跑")
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Printf("GMS 后端启动,监听 %s,区域 %s", cfg.Listen, cfg.Ranch.RegionCode)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP 服务异常退出: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("收到退出信号,正在关闭...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
