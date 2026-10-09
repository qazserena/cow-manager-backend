// Package ranchdata 提供游戏库(ranch_game)与日志库(ranch_log)的只读查询与导出:
// 玩家、公会、公会字典、公会战记录、斗牛场日志。
package ranchdata

import (
	"net/http"
	"time"

	"github.com/jmoiron/sqlx"

	"cow-manager-backend-go/internal/auth"
	"cow-manager-backend-go/internal/httpx"
	"cow-manager-backend-go/internal/perm"
	"cow-manager-backend-go/internal/query"
)

// Service 只读数据服务。
type Service struct {
	game    *sqlx.DB
	log     *sqlx.DB
	loc     *time.Location
	labeler query.EnumLabeler
}

// NewService 创建服务。
func NewService(game, log *sqlx.DB, loc *time.Location, labeler query.EnumLabeler) *Service {
	return &Service{game: game, log: log, loc: loc, labeler: labeler}
}

// UserSpec 玩家主表 ranch_user。
var UserSpec = &query.Spec{
	Table: "ranch_user", DefaultSort: "`uid` ASC",
	Columns: []query.Column{
		{Name: "uid", Label: "用户ID", Kind: query.Int, Filter: true, Sort: true},
		{Name: "account", Label: "账号", Kind: query.String, Filter: true},
		{Name: "address", Label: "地址", Kind: query.String, Filter: true},
		{Name: "guildId", Label: "公会ID", Kind: query.Int, Filter: true, Sort: true},
		{Name: "fedPlanetId", Label: "联邦星球", Kind: query.Int, Filter: true},
		{Name: "linkEnergy", Label: "累计充能", Kind: query.Int, Sort: true},
		{Name: "costEnergy", Label: "累计消耗能量", Kind: query.Int, Sort: true},
		{Name: "motto", Label: "个性签名", Kind: query.String},
		{Name: "headBoxId", Label: "头像框", Kind: query.Int},
		{Name: "maleCowSkinId", Label: "公牛皮肤ID", Kind: query.Int},
		{Name: "maleCowSkinTokenId", Label: "公牛皮肤Token", Kind: query.Int},
		{Name: "femaleCowSkinId", Label: "母牛皮肤ID", Kind: query.Int},
		{Name: "femaleCowSkinTokenId", Label: "母牛皮肤Token", Kind: query.Int},
		{Name: "registerTs", Label: "注册时间", Kind: query.TsSec, Filter: true, Sort: true},
		{Name: "joinGuildTs", Label: "加入公会时间", Kind: query.TsSec, Filter: true, Sort: true},
		{Name: "lastLoginIP", Label: "最后登录IP", Kind: query.String, Filter: true},
		{Name: "lastLogoutTs", Label: "最后登出时间", Kind: query.TsSec, Filter: true, Sort: true},
	},
}

// GuildSpec 公会表 ranch_guild。
var GuildSpec = &query.Spec{
	Table: "ranch_guild", DefaultSort: "`guildId` ASC",
	Columns: []query.Column{
		{Name: "guildId", Label: "公会ID", Kind: query.Int, Filter: true, Sort: true},
		{Name: "ownerUid", Label: "会长", Kind: query.Int, Filter: true, Sort: true},
		{Name: "ownerAddress", Label: "会长地址", Kind: query.String, Filter: true},
		{Name: "members", Label: "成员", Kind: query.String},
		{Name: "gvgPoints", Label: "公会积分", Kind: query.Int, Sort: true},
	},
}

// GuildDictSpec 公会字典 ranch_guild_dict(福利/铠甲等 JSON)。
var GuildDictSpec = &query.Spec{
	Table: "ranch_guild_dict", DefaultSort: "`guild_id` ASC, `k` ASC",
	Columns: []query.Column{
		{Name: "guild_id", Label: "公会ID", Kind: query.Int, Filter: true, Sort: true},
		{Name: "k", Label: "键", Kind: query.String, Filter: true},
		{Name: "v", Label: "值", Kind: query.JSON},
		{Name: "updateTime", Label: "更新时间", Kind: query.String, Sort: true},
	},
}

// GuildBattleSpec 公会战记录 ranch_guild_battle。
var GuildBattleSpec = &query.Spec{
	Table: "ranch_guild_battle", DefaultSort: "`id` DESC",
	Columns: []query.Column{
		{Name: "id", Label: "ID", Kind: query.Int, Filter: true, Sort: true},
		{Name: "yearWeek", Label: "年-周", Kind: query.String, Filter: true, Sort: true},
		{Name: "guildId", Label: "公会ID", Kind: query.Int, Filter: true, Sort: true},
		{Name: "ownerUid", Label: "会长", Kind: query.Int, Filter: true},
		{Name: "ownerAddress", Label: "会长地址", Kind: query.String, Filter: true},
		{Name: "appliedMembers", Label: "报名成员", Kind: query.String},
		{Name: "applyTs", Label: "报名时间", Kind: query.TsSec, Filter: true, Sort: true},
		{Name: "division", Label: "分区", Kind: query.Int, Filter: true},
		{Name: "protector", Label: "守护者", Kind: query.JSON},
		{Name: "protectorArmor", Label: "守护者铠甲", Kind: query.JSON},
		{Name: "opponent", Label: "对手公会", Kind: query.Int, Filter: true},
		{Name: "membersContribution", Label: "成员贡献", Kind: query.JSON},
		{Name: "settleStatus", Label: "结算状态", Kind: query.Int, Filter: true, Enum: "BattleSettleStatusType"},
		{Name: "settleGvgPoints", Label: "结算积分", Kind: query.Int},
		{Name: "settleRanking", Label: "结算排名", Kind: query.Int},
		{Name: "settleOpponentRanking", Label: "对手排名", Kind: query.Int},
		{Name: "settleTs", Label: "结算时间", Kind: query.TsSec, Filter: true, Sort: true},
	},
}

// BullringLogSpec 斗牛场日志 log_bullring(isValid/param1-3 为内部字段,不对外)。
var BullringLogSpec = &query.Spec{
	Table: "log_bullring", DefaultSort: "`id` DESC",
	Columns: []query.Column{
		{Name: "id", Label: "ID", Kind: query.Int, Filter: true, Sort: true},
		{Name: "uid", Label: "用户ID", Kind: query.Int, Filter: true, Sort: true},
		{Name: "address", Label: "地址", Kind: query.String, Filter: true},
		{Name: "gameID", Label: "对局ID", Kind: query.Int, Filter: true},
		{Name: "cowId", Label: "牛牛ID", Kind: query.Int, Filter: true},
		{Name: "gameType", Label: "游戏类型", Kind: query.Int, Filter: true, Enum: "GameType"},
		{Name: "turnsUsed", Label: "回合数", Kind: query.Int},
		{Name: "isWinning", Label: "是否获胜", Kind: query.Bool, Filter: true},
		{Name: "dailyPlayed", Label: "当日场次", Kind: query.Int},
		{Name: "dailyWon", Label: "当日胜场", Kind: query.Int},
		{Name: "createTs", Label: "时间", Kind: query.TsSec, Filter: true, Sort: true},
	},
}

// Register 挂载路由。列表仅需登录,导出需 table/{表}/export。
func (s *Service) Register(mux *http.ServeMux) {
	s.mount(mux, "/ranch/users", UserSpec, s.game, "uid", "用户")
	s.mount(mux, "/ranch/guilds", GuildSpec, s.game, "guildId", "公会")
	s.mount(mux, "/ranch/guild-dict", GuildDictSpec, s.game, "", "公会字典")
	s.mount(mux, "/ranch/guild-battles", GuildBattleSpec, s.game, "id", "公会战")
	s.mount(mux, "/ranch/bullring-logs", BullringLogSpec, s.log, "id", "斗牛场日志")
}

func (s *Service) mount(mux *http.ServeMux, prefix string, spec *query.Spec, db *sqlx.DB, idCol, label string) {
	h := query.Handlers{Spec: spec, DB: db, Loc: s.loc, Labeler: s.labeler}
	mux.HandleFunc("GET "+prefix, httpx.H(auth.RequireLogin(h.List)))
	mux.HandleFunc("GET "+prefix+"/export", httpx.H(auth.Require(perm.Table(spec.Table, "export", label), h.Export)))
	if idCol != "" {
		mux.HandleFunc("GET "+prefix+"/{id}", httpx.H(auth.RequireLogin(h.One(idCol))))
	}
}
