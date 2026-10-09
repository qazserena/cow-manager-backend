// Package db 打开各 MySQL 库的连接池。
package db

import (
	"context"
	"fmt"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"cow-manager-backend/internal/config"
)

// Conns 各库的连接池。Portal 为可选(未配置时为 nil)。
type Conns struct {
	Auth   *sqlx.DB // auth_center
	Gms    *sqlx.DB // gms-ranch
	Game   *sqlx.DB // ranch_game
	Log    *sqlx.DB // ranch_log
	Tpl    *sqlx.DB // ranch_tpl
	Portal *sqlx.DB // cow-portal(官网邀请计划,可选)
}

// Open 打开单个库并 ping。
func Open(cfg config.DB) (*sqlx.DB, error) {
	d, err := sqlx.Open("mysql", cfg.DSN())
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(8)
	d.SetMaxIdleConns(2)
	d.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := d.PingContext(ctx); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("连接 %s@%s/%s: %w", cfg.User, cfg.Host, cfg.Database, err)
	}
	return d, nil
}

// OpenAll 按配置打开全部库。
func OpenAll(cfg *config.Config) (*Conns, error) {
	c := &Conns{}
	var err error
	if c.Auth, err = Open(cfg.Databases.Auth); err != nil {
		return nil, err
	}
	if c.Gms, err = Open(cfg.Databases.Gms); err != nil {
		return nil, err
	}
	if c.Game, err = Open(cfg.Databases.Game); err != nil {
		return nil, err
	}
	if c.Log, err = Open(cfg.Databases.Log); err != nil {
		return nil, err
	}
	if c.Tpl, err = Open(cfg.Databases.Tpl); err != nil {
		return nil, err
	}
	// 官网库可选:没配 database 就跳过,连不上也只记日志不阻塞启动
	if cfg.Databases.Portal.Database != "" {
		if c.Portal, err = Open(cfg.Databases.Portal); err != nil {
			log.Printf("官网库(portal)连接失败,/portal/invite 路由不挂载: %v", err)
			c.Portal = nil
		}
	}
	return c, nil
}

// Close 关闭全部连接池。
func (c *Conns) Close() {
	for _, d := range []*sqlx.DB{c.Auth, c.Gms, c.Game, c.Log, c.Tpl, c.Portal} {
		if d != nil {
			_ = d.Close()
		}
	}
}
