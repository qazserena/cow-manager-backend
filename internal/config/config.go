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
		// Portal cow-portal:官网公测邀请计划(u_invite_*)。可选,不配则不挂 /portal/invite 路由;
		// 这块业务属于官网,将来迁到官网管理后端时连同 internal/modules/portalinvite 一起拿走
		Portal DB `json:"portal"`
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

	// Portal 官网相关(临时承载在 GMS 的官网模块使用;迁走时整段删除)
	Portal struct {
		// APIBase 官网后端公开接口根地址,用来拉邀请积分口径等;缺省 https://cow-portal-backend.cowgalaxy.com
		APIBase string `json:"apiBase"`
		// ImageHosting 官网图床前缀,用来把头像编号解析成 URL;缺省 https://oss.cowgalaxy.com
		ImageHosting string `json:"imageHosting"`
		// SiteURL 官网站点前缀,用来拼邀请链接;缺省 https://cowgalaxy.com
		SiteURL string `json:"siteUrl"`
		// Social 官方社交渠道的直连凭证(运营看板拉官方账号 / 群的实时数据与每日快照)。
		// 与 cow-portal-backend configs/system.toml [social.*] 保持一致;某平台留空则该平台只看站内绑定数据
		Social PortalSocial `json:"social"`
	} `json:"portal"`
}

// PortalSocial 三个运营渠道的直连配置。
type PortalSocial struct {
	X struct {
		// Target 官方账号(不带 @),缺省 cowgalaxy2026
		Target string `json:"target"`
		// BearerToken X Developer Portal → 应用 → Keys and tokens → Bearer Token(app-only,读公开指标用)
		BearerToken string `json:"bearerToken"`
	} `json:"x"`
	Telegram struct {
		BotToken string `json:"botToken"`
		// ChatID 官方群:@username 或 -100 开头数字 id;bot 需在群内
		ChatID string `json:"chatId"`
	} `json:"telegram"`
	Discord struct {
		BotToken string `json:"botToken"`
		GuildID  string `json:"guildId"`
	} `json:"discord"`
}

// GameSchemaForPortal 官网库与游戏库在同一 MySQL 实例时返回游戏库名(可在同一条 SQL 里跨库 JOIN),
// 否则返回空串,调用方应退化为分步查询。
func (c *Config) GameSchemaForPortal() string {
	p, g := c.Databases.Portal, c.Databases.Game
	if p.Database == "" || g.Database == "" {
		return ""
	}
	pp, gp := p.Port, g.Port
	if pp == 0 {
		pp = 3306
	}
	if gp == 0 {
		gp = 3306
	}
	if !strings.EqualFold(p.Host, g.Host) || pp != gp {
		return ""
	}
	return g.Database
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
	if v := os.Getenv("GMS_PORTAL_API_BASE"); v != "" {
		c.Portal.APIBase = v
	}
	if v := os.Getenv("GMS_PORTAL_X_BEARER_TOKEN"); v != "" {
		c.Portal.Social.X.BearerToken = v
	}
	if v := os.Getenv("GMS_PORTAL_TG_BOT_TOKEN"); v != "" {
		c.Portal.Social.Telegram.BotToken = v
	}
	if v := os.Getenv("GMS_PORTAL_TG_CHAT_ID"); v != "" {
		c.Portal.Social.Telegram.ChatID = v
	}
	if v := os.Getenv("GMS_PORTAL_DC_BOT_TOKEN"); v != "" {
		c.Portal.Social.Discord.BotToken = v
	}
	if v := os.Getenv("GMS_PORTAL_DC_GUILD_ID"); v != "" {
		c.Portal.Social.Discord.GuildID = v
	}
	dbs := []*DB{&c.Databases.Auth, &c.Databases.Gms, &c.Databases.Game, &c.Databases.Log, &c.Databases.Tpl, &c.Databases.Portal}
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
	if c.Portal.APIBase == "" {
		c.Portal.APIBase = "https://cow-portal-backend.cowgalaxy.com"
	}
	if c.Portal.ImageHosting == "" {
		c.Portal.ImageHosting = "https://oss.cowgalaxy.com"
	}
	if c.Portal.SiteURL == "" {
		c.Portal.SiteURL = "https://cowgalaxy.com"
	}
	c.Portal.APIBase = strings.TrimRight(c.Portal.APIBase, "/")
	c.Portal.ImageHosting = strings.TrimRight(c.Portal.ImageHosting, "/")
	c.Portal.SiteURL = strings.TrimRight(c.Portal.SiteURL, "/")
	if c.Portal.Social.X.Target == "" {
		c.Portal.Social.X.Target = "cowgalaxy2026"
	}
	c.Portal.Social.X.Target = strings.TrimPrefix(c.Portal.Social.X.Target, "@")
}
