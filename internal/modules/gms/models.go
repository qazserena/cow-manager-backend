package gms

import (
	"encoding/json"
	"strings"

	"cow-manager-backend/internal/httpx"
	"cow-manager-backend/internal/query"
)

// Mail 个人邮件(gms_ranch_mail)。attachment 为 JSON 文本,代币数量按"个"填写,
// 游戏服同步时自动 ×10^18。
type Mail struct {
	ID         int64  `db:"id" json:"id"`
	Address    string `db:"address" json:"address"`
	Title      string `db:"title" json:"title"`
	Content    string `db:"content" json:"content"`
	Attachment string `db:"attachment" json:"attachment"`
	Auditable
}

// GroupMail 群邮件(gms_ranch_group_mail)。openTime/closeTime 为秒。
type GroupMail struct {
	ID                    int64  `db:"id" json:"id"`
	GroupMailKey          string `db:"groupMailKey" json:"groupMailKey"`
	Title                 string `db:"title" json:"title"`
	Content               string `db:"content" json:"content"`
	Attachment            string `db:"attachment" json:"attachment"`
	OpenTime              int64  `db:"openTime" json:"openTime"`
	CloseTime             int64  `db:"closeTime" json:"closeTime"`
	ConditionRegisterTime string `db:"conditionRegisterTime" json:"conditionRegisterTime"`
	Auditable
}

// CheckIn 签到奖励(gms_ranch_check_in)。
type CheckIn struct {
	ID     int64  `db:"id" json:"id"`
	Reward string `db:"reward" json:"reward"`
	Auditable
}

// GuildBattle 公会战排期配置(gms_ranch_guild_battle)。effectiveFrom/To 为秒,0 不限。
type GuildBattle struct {
	ID              int64 `db:"id" json:"id"`
	ApplyStartDay   int   `db:"applyStartDay" json:"applyStartDay"`
	ApplyStartHour  int   `db:"applyStartHour" json:"applyStartHour"`
	ApplyEndDay     int   `db:"applyEndDay" json:"applyEndDay"`
	ApplyEndHour    int   `db:"applyEndHour" json:"applyEndHour"`
	BattleStartDay  int   `db:"battleStartDay" json:"battleStartDay"`
	BattleStartHour int   `db:"battleStartHour" json:"battleStartHour"`
	BattleEndDay    int   `db:"battleEndDay" json:"battleEndDay"`
	BattleEndHour   int   `db:"battleEndHour" json:"battleEndHour"`
	EffectiveFrom   int64 `db:"effectiveFrom" json:"effectiveFrom"`
	EffectiveTo     int64 `db:"effectiveTo" json:"effectiveTo"`
	Auditable
}

// normalizeJSON 校验 JSON 列文本;空值用默认值。
func normalizeJSON(raw, def string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return def, nil
	}
	if !json.Valid([]byte(raw)) {
		return "", httpx.BadRequest("附件不是合法 JSON")
	}
	return raw, nil
}

var auditColumns = []query.Column{
	{Name: "createdBy", Label: "创建人", Kind: query.Int, Filter: true},
	{Name: "createdTime", Label: "创建时间", Kind: query.TsMs, Filter: true, Sort: true},
	{Name: "auditStatus", Label: "审核状态", Kind: query.Int, Filter: true, Enum: "AuditStatus"},
	{Name: "auditBy", Label: "审核人", Kind: query.Int, Filter: true},
	{Name: "auditTime", Label: "审核时间", Kind: query.TsMs, Sort: true},
	{Name: "active", Label: "是否激活", Kind: query.Bool, Filter: true},
}

func withAudit(cols ...query.Column) []query.Column {
	return append(cols, auditColumns...)
}

// 列表/导出用的只读声明。
var (
	mailSpec = &query.Spec{
		Table: "gms_ranch_mail", DefaultSort: "`id` DESC", BaseWhere: "deletedTime = 0",
		Columns: withAudit(
			query.Column{Name: "id", Label: "ID", Kind: query.Int, Filter: true, Sort: true},
			query.Column{Name: "address", Label: "收件地址", Kind: query.String, Filter: true},
			query.Column{Name: "title", Label: "标题", Kind: query.String, Filter: true},
			query.Column{Name: "content", Label: "内容", Kind: query.String, Filter: true},
			query.Column{Name: "attachment", Label: "附件", Kind: query.JSON},
		),
	}
	groupMailSpec = &query.Spec{
		Table: "gms_ranch_group_mail", DefaultSort: "`id` DESC", BaseWhere: "deletedTime = 0",
		Columns: withAudit(
			query.Column{Name: "id", Label: "ID", Kind: query.Int, Filter: true, Sort: true},
			query.Column{Name: "groupMailKey", Label: "群邮件Key", Kind: query.String, Filter: true},
			query.Column{Name: "title", Label: "标题", Kind: query.String, Filter: true},
			query.Column{Name: "content", Label: "内容", Kind: query.String, Filter: true},
			query.Column{Name: "attachment", Label: "附件", Kind: query.JSON},
			query.Column{Name: "openTime", Label: "开启时间", Kind: query.TsSec, Filter: true, Sort: true},
			query.Column{Name: "closeTime", Label: "结束时间", Kind: query.TsSec, Filter: true, Sort: true},
			query.Column{Name: "conditionRegisterTime", Label: "条件:注册时间", Kind: query.String},
		),
	}
	checkInSpec = &query.Spec{
		Table: "gms_ranch_check_in", DefaultSort: "`id` DESC", BaseWhere: "deletedTime = 0",
		Columns: withAudit(
			query.Column{Name: "id", Label: "ID", Kind: query.Int, Filter: true, Sort: true},
			query.Column{Name: "reward", Label: "奖励", Kind: query.JSON},
		),
	}
	guildBattleSpec = &query.Spec{
		Table: "gms_ranch_guild_battle", DefaultSort: "`id` DESC", BaseWhere: "deletedTime = 0",
		Columns: withAudit(
			query.Column{Name: "id", Label: "ID", Kind: query.Int, Filter: true, Sort: true},
			query.Column{Name: "applyStartDay", Label: "报名开始日", Kind: query.Int},
			query.Column{Name: "applyStartHour", Label: "报名开始小时", Kind: query.Int},
			query.Column{Name: "applyEndDay", Label: "报名结束日", Kind: query.Int},
			query.Column{Name: "applyEndHour", Label: "报名结束小时", Kind: query.Int},
			query.Column{Name: "battleStartDay", Label: "战斗开始日", Kind: query.Int},
			query.Column{Name: "battleStartHour", Label: "战斗开始小时", Kind: query.Int},
			query.Column{Name: "battleEndDay", Label: "战斗结束日", Kind: query.Int},
			query.Column{Name: "battleEndHour", Label: "战斗结束小时", Kind: query.Int},
			query.Column{Name: "effectiveFrom", Label: "生效开始", Kind: query.TsSec, Filter: true, Sort: true},
			query.Column{Name: "effectiveTo", Label: "生效结束", Kind: query.TsSec, Filter: true, Sort: true},
		),
	}
)

var moduleSpecs = map[string]*query.Spec{
	"mail":         mailSpec,
	"group-mail":   groupMailSpec,
	"check-in":     checkInSpec,
	"guild-battle": guildBattleSpec,
}
