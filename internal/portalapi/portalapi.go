// Package portalapi 是官网后端(cow-portal-backend)公开接口的小客户端,供 GMS 里临时承载的
// 官网模块(internal/modules/portalinvite / portaluser)使用。
//
// 目前只拉一样东西:公测邀请计划的积分口径 GET /invite/rules(分值 / 里程碑 / 等级门槛)。
// 等级门槛的事实来源在官网,这里缓存 10 分钟并带一份离线兜底,保证官网改口径后 GMS 报表自动跟上,
// 官网不可达时也能出数。迁到官网管理后端后本包可直接删除,改读本地 model。
package portalapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Level 等级定义(与官网 model.InviteLevel 同形)。
type Level struct {
	Level int    `json:"level"`
	Key   string `json:"key"`
	Min   int64  `json:"min"`
}

// Milestone 有效邀请里程碑。
type Milestone struct {
	Count  int64 `json:"count"`
	Points int64 `json:"points"`
}

// InviteRules 官网 GET /invite/rules 的返回。
type InviteRules struct {
	JoinedPoints   int64       `json:"joinedPoints"`
	BindPoints     int64       `json:"bindPoints"`
	ActivatePoints int64       `json:"activatePoints"`
	ValidPoints    int64       `json:"validPoints"`
	Milestones     []Milestone `json:"milestones"`
	Levels         []Level     `json:"levels"`
	MaxBindPerIP   int64       `json:"maxBindPerIp"`
	IPWindowHours  int64       `json:"ipWindowHours"`
}

// defaultRules 官网不可达时的兜底,与 cow-portal-backend/internal/model/invite_program.go 当前值一致。
var defaultRules = InviteRules{
	JoinedPoints: 30, BindPoints: 20, ActivatePoints: 50, ValidPoints: 100,
	Milestones: []Milestone{{5, 200}, {10, 500}, {25, 1500}, {50, 4000}, {100, 10000}},
	Levels: []Level{
		{1, "calf", 0}, {2, "rancher", 300}, {3, "herder", 1200}, {4, "ranchBoss", 3000},
		{5, "starPioneer", 8000}, {6, "galaxyLegend", 20000}, {7, "cosmicOverlord", 50000},
	},
	MaxBindPerIP: 3, IPWindowHours: 720,
}

const rulesTTL = 10 * time.Minute

// Client 官网公开接口客户端。
type Client struct {
	base string
	http *http.Client

	mu      sync.Mutex
	rules   *InviteRules
	rulesAt time.Time
	rulesOk bool // 最近一次是否来自官网(否则为兜底)
}

// New 创建客户端;base 为官网后端根地址,如 https://cow-portal-backend.cowgalaxy.com。
func New(base string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 5 * time.Second}}
}

// Base 官网后端根地址。
func (c *Client) Base() string { return c.base }

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("portal %s: http %d", path, resp.StatusCode)
	}
	env := &envelope{}
	if err := json.NewDecoder(resp.Body).Decode(env); err != nil {
		return err
	}
	if env.Code != 1 {
		return fmt.Errorf("portal %s: code %d %s", path, env.Code, env.Message)
	}
	return json.Unmarshal(env.Data, out)
}

// Rules 积分口径:10 分钟缓存,拉取失败退回上次结果或兜底值。返回值不要修改。
func (c *Client) Rules(ctx context.Context) *InviteRules {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rules != nil && time.Since(c.rulesAt) < rulesTTL {
		return c.rules
	}
	r := &InviteRules{}
	if c.base != "" {
		if err := c.getJSON(ctx, "/invite/rules", r); err == nil && len(r.Levels) > 0 {
			sort.Slice(r.Levels, func(i, j int) bool { return r.Levels[i].Min < r.Levels[j].Min })
			c.rules, c.rulesAt, c.rulesOk = r, time.Now(), true
			return c.rules
		}
	}
	if c.rules == nil {
		d := defaultRules
		c.rules, c.rulesOk = &d, false
	}
	// 失败也刷新时间戳,避免每个请求都去打一次官网
	c.rulesAt = time.Now()
	return c.rules
}

// RulesFromPortal 最近一次口径是否来自官网(false = 兜底值)。
func (c *Client) RulesFromPortal() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rulesOk
}

// LevelOf 积分对应的等级。
func (c *Client) LevelOf(ctx context.Context, points int64) Level {
	levels := c.Rules(ctx).Levels
	cur := levels[0]
	for _, lv := range levels {
		if points >= lv.Min {
			cur = lv
		}
	}
	return cur
}

// LevelCaseSQL 生成按等级分桶的 CASE 表达式(门槛全是整数,直接内联安全)。
func (c *Client) LevelCaseSQL(ctx context.Context, col string) string {
	levels := c.Rules(ctx).Levels
	var b strings.Builder
	b.WriteString("CASE")
	for i := len(levels) - 1; i >= 1; i-- {
		fmt.Fprintf(&b, " WHEN %s >= %d THEN %d", col, levels[i].Min, levels[i].Level)
	}
	fmt.Fprintf(&b, " ELSE %d END", levels[0].Level)
	return b.String()
}
