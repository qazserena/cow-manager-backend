// Package config 读取 JSON 配置文件并允许少量环境变量覆盖。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// DB 单个 MySQL 库的连接参数。
type DB struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
}

// DSN 生成 go-sql-driver/mysql 连接串。不开 parseTime:日期/时间列按字符串返回,
// 便于直接透传给前端与导出。
func (d DB) DSN() string {
	port := d.Port
	if port == 0 {
		port = 3306
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&collation=utf8mb4_unicode_ci&timeout=5s&readTimeout=120s&writeTimeout=60s",
		d.User, d.Password, d.Host, port, d.Database)
}

// Config 服务配置。
type Config struct {
	// Listen HTTP 监听地址,如 ":8080"
	Listen string `json:"listen"`

	Databases struct {
		Auth DB `json:"auth"` // auth_center:管理员、角色、枚举、多语言
		Gms  DB `json:"gms"`  // gms-ranch:运营配置、统计表、任务
		Game DB `json:"game"` // ranch_game:玩家、公会
		Log  DB `json:"log"`  // ranch_log:登录、斗牛场日志
		Tpl  DB `json:"tpl"`  // ranch_tpl:策划模板
	} `json:"databases"`

	Ranch struct {
		RegionCode        string   `json:"regionCode"`
		Channels          []string `json:"channels"`
		DefaultTimeZone   string   `json:"defaultTimeZone"`
		GameServerAddress string   `json:"gameServerAddress"`
		GameServerApiKey  string   `json:"gameServerApiKey"`
		ServerID          int      `json:"serverId"`
		ServerName        string   `json:"serverName"`
	} `json:"ranch"`

	Auth struct {
		TokenTTLHours     int `json:"tokenTTLHours"`
		TokenRefreshHours int `json:"tokenRefreshHours"`
	} `json:"auth"`

	Task struct {
		Enabled     bool   `json:"enabled"`
		DefaultCron string `json:"defaultCron"`
		TimeZone    string `json:"timeZone"`
	} `json:"task"`
}

// Load 读取配置并应用默认值与环境变量覆盖:
//
//	GMS_LISTEN                 监听地址
//	GMS_DB_PASSWORD            覆盖所有库的密码
//	GMS_DB_HOST                覆盖所有库的主机
//	GMS_GAME_SERVER_ADDRESS    游戏服 login 地址
//	GMS_GAME_SERVER_API_KEY    游戏服 /admin 签名密钥
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	cfg.applyEnv()
	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv("GMS_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("GMS_GAME_SERVER_ADDRESS"); v != "" {
		c.Ranch.GameServerAddress = v
	}
	if v := os.Getenv("GMS_GAME_SERVER_API_KEY"); v != "" {
		c.Ranch.GameServerApiKey = v
	}
	dbs := []*DB{&c.Databases.Auth, &c.Databases.Gms, &c.Databases.Game, &c.Databases.Log, &c.Databases.Tpl}
	if v := os.Getenv("GMS_DB_PASSWORD"); v != "" {
		for _, d := range dbs {
			d.Password = v
		}
	}
	if v := os.Getenv("GMS_DB_HOST"); v != "" {
		for _, d := range dbs {
			d.Host = v
		}
	}
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.Ranch.RegionCode == "" {
		c.Ranch.RegionCode = "dev"
	}
	if c.Ranch.DefaultTimeZone == "" {
		c.Ranch.DefaultTimeZone = "Asia/Shanghai"
	}
	if c.Ranch.ServerID == 0 {
		c.Ranch.ServerID = 1
	}
	if c.Ranch.ServerName == "" {
		c.Ranch.ServerName = "ranch_server"
	}
	c.Ranch.GameServerAddress = strings.TrimRight(c.Ranch.GameServerAddress, "/")
	if c.Auth.TokenTTLHours <= 0 {
		c.Auth.TokenTTLHours = 24
	}
	if c.Auth.TokenRefreshHours <= 0 {
		c.Auth.TokenRefreshHours = 12
	}
	if c.Task.DefaultCron == "" {
		c.Task.DefaultCron = "0 40 1 * * *"
	}
	if c.Task.TimeZone == "" {
		c.Task.TimeZone = c.Ranch.DefaultTimeZone
	}
}
