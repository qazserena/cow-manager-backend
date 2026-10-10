// Package analysis 提供离线统计表(gms 库 da_ranch_*)的只读查询与导出。
package analysis

import (
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"

	"cow-manager-backend/internal/auth"
	"cow-manager-backend/internal/httpx"
	"cow-manager-backend/internal/perm"
	"cow-manager-backend/internal/query"
)

// Service 统计数据服务。
type Service struct {
	gms     *sqlx.DB
	loc     *time.Location
	labeler query.EnumLabeler
}

// NewService 创建服务。
func NewService(gms *sqlx.DB, loc *time.Location, labeler query.EnumLabeler) *Service {
	return &Service{gms: gms, loc: loc, labeler: labeler}
}

// BullringCountSpec 斗牛场场次 da_ranch_bullring_count。
var BullringCountSpec = &query.Spec{
	Table: "da_ranch_bullring_count", DefaultSort: "`dateTs` DESC, `gameTabType` ASC",
	Columns: []query.Column{
		{Name: "id", Label: "ID", Kind: query.Int, Sort: true},
		{Name: "dateTs", Label: "日期", Kind: query.TsSec, Filter: true, Sort: true},
		{Name: "serverID", Label: "服务器", Kind: query.Int, Filter: true},
		{Name: "gameTabType", Label: "页签", Kind: query.Int, Filter: true, Enum: "BullringTabType"},
		{Name: "count", Label: "场次", Kind: query.Int, Sort: true},
		{Name: "wins", Label: "胜场", Kind: query.Int, Sort: true},
	},
}

// BullringRewardsSpec 斗牛场奖励 da_ranch_bullring_rewards。
var BullringRewardsSpec = &query.Spec{
	Table: "da_ranch_bullring_rewards", DefaultSort: "`dateTs` DESC, `gameTabType` ASC",
	Columns: []query.Column{
		{Name: "id", Label: "ID", Kind: query.Int, Sort: true},
		{Name: "dateTs", Label: "日期", Kind: query.TsSec, Filter: true, Sort: true},
		{Name: "serverID", Label: "服务器", Kind: query.Int, Filter: true},
		{Name: "gameTabType", Label: "页签", Kind: query.Int, Filter: true, Enum: "BullringTabType"},
		{Name: "tokenT", Label: "主代币(wei)", Kind: query.String},
		{Name: "tokenG", Label: "副代币(wei)", Kind: query.String},
	},
}

// UserStatsSpec 用户统计 da_ranch_user。
var UserStatsSpec = &query.Spec{
	Table: "da_ranch_user", DefaultSort: "`dateTs` DESC",
	Columns: []query.Column{
		{Name: "id", Label: "ID", Kind: query.Int, Sort: true},
		{Name: "dateTs", Label: "日期", Kind: query.TsSec, Filter: true, Sort: true},
		{Name: "serverID", Label: "服务器", Kind: query.Int, Filter: true},
		{Name: "dnu", Label: "新增用户", Kind: query.Int, Sort: true},
		{Name: "dau", Label: "活跃用户", Kind: query.Int, Sort: true},
		{Name: "churn7", Label: "七日流失", Kind: query.Int, Sort: true},
	},
}

// RetentionSpec 留存 da_ranch_retention。
var RetentionSpec = &query.Spec{
	Table: "da_ranch_retention", DefaultSort: "`dt` DESC",
	Columns: []query.Column{
		{Name: "id", Label: "ID", Kind: query.Int, Sort: true},
		{Name: "dt", Label: "日期", Kind: query.Date, Filter: true, Sort: true},
		{Name: "serverID", Label: "服务器", Kind: query.Int, Filter: true},
		{Name: "r1", Label: "当日新增", Kind: query.Int},
		{Name: "r2", Label: "次日留存", Kind: query.Int},
		{Name: "r3", Label: "3日留存", Kind: query.Int},
		{Name: "r4", Label: "4日留存", Kind: query.Int},
		{Name: "r5", Label: "5日留存", Kind: query.Int},
		{Name: "r6", Label: "6日留存", Kind: query.Int},
		{Name: "r7", Label: "7日留存", Kind: query.Int},
	},
}

// Register 挂载路由。
func (s *Service) Register(mux *http.ServeMux) {
	s.mount(mux, "/analysis/bullring-count", BullringCountSpec, "斗牛场场次")
	s.mount(mux, "/analysis/bullring-rewards", BullringRewardsSpec, "斗牛场奖励")
	s.mount(mux, "/analysis/user", UserStatsSpec, "用户统计")
	s.mount(mux, "/analysis/retention", RetentionSpec, "用户留存")
}

// mount 列表与导出都只读,统一走「数据分析 · 查看」。
func (s *Service) mount(mux *http.ServeMux, prefix string, spec *query.Spec, _ string) {
	h := query.Handlers{Spec: spec, DB: s.gms, Loc: s.loc, Labeler: s.labeler}
	mux.HandleFunc("GET "+prefix, httpx.H(auth.Require(perm.AnalysisView, h.List)))
	mux.HandleFunc("GET "+prefix+"/export", httpx.H(auth.Require(perm.AnalysisView, h.Export)))
}
