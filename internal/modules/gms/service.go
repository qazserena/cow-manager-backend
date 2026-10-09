package gms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"cow-manager-backend/internal/config"
	"cow-manager-backend/internal/httpx"
	"cow-manager-backend/internal/query"
)

// Service 牧场运营管理。
type Service struct {
	cfg     *config.Config
	gms     *sqlx.DB
	tpl     *sqlx.DB
	game    *GameServer
	loc     *time.Location
	labeler query.EnumLabeler
}

// NewService 创建服务。labeler 用于导出时把枚举值翻译成文案。
func NewService(cfg *config.Config, gmsDB, tplDB *sqlx.DB, loc *time.Location, labeler query.EnumLabeler) *Service {
	return &Service{
		cfg: cfg, gms: gmsDB, tpl: tplDB, loc: loc, labeler: labeler,
		game: NewGameServer(cfg.Ranch.GameServerAddress, cfg.Ranch.GameServerApiKey),
	}
}

// GameServer 暴露客户端(任务模块等复用)。
func (s *Service) GameServer() *GameServer { return s.game }

// ---------- 区域信息 / 道具模板 ----------

// ServerInfo 前端 RegionInfo.servers 元素。
type ServerInfo struct {
	ServerID      int    `json:"serverId"`
	ServerName    string `json:"serverName"`
	Version       string `json:"version"`
	MergedTs      int64  `json:"mergedTs"`
	MergedTo      int64  `json:"mergedTo"`
	ConditionTest bool   `json:"conditionTest"`
}

// RegionInfo GET /config/info 响应。
type RegionInfo struct {
	RegionCode      string       `json:"regionCode"`
	HubURL          string       `json:"hubURL"`
	DefaultTimeZone string       `json:"defaultTimeZone"`
	Channels        []string     `json:"channels"`
	Servers         []ServerInfo `json:"servers"`
	Versions        []string     `json:"versions"`
}

// Region 组装区域信息。
func (s *Service) Region() *RegionInfo {
	channels := s.cfg.Ranch.Channels
	if channels == nil {
		channels = []string{}
	}
	return &RegionInfo{
		RegionCode:      s.cfg.Ranch.RegionCode,
		DefaultTimeZone: s.cfg.Ranch.DefaultTimeZone,
		Channels:        channels,
		Servers:         []ServerInfo{{ServerID: s.cfg.Ranch.ServerID, ServerName: s.cfg.Ranch.ServerName, Version: "1"}},
		Versions:        []string{},
	}
}

// TemplateItem 道具模板(ranch_tpl.tpl_rewards),附件选择器的数据源。
type TemplateItem struct {
	TplID       int64  `db:"tplId" json:"tplId"`
	Name        string `db:"name" json:"name"`
	ItemType    int    `db:"itemType" json:"itemType"`
	Type        int    `db:"-" json:"type"`
	QualityType int    `db:"quality" json:"qualityType"`
	FixedValue  string `db:"fixedValue" json:"fixedValue"`
}

// TemplateItems 读取全部奖励模板。
func (s *Service) TemplateItems(ctx context.Context) ([]TemplateItem, error) {
	list := []TemplateItem{}
	if err := s.tpl.SelectContext(ctx, &list, "SELECT tplId, name, itemType, quality, fixedValue FROM tpl_rewards ORDER BY itemType, tplId"); err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Type = list[i].ItemType
	}
	return list, nil
}

// ---------- 运营配置 CRUD ----------

func (s *Service) getMail(ctx context.Context, id int64) (*Mail, error) {
	m := &Mail{}
	err := s.gms.GetContext(ctx, m, "SELECT * FROM gms_ranch_mail WHERE id = ? AND deletedTime = 0", id)
	return m, notFound(err)
}

func (s *Service) getGroupMail(ctx context.Context, id int64) (*GroupMail, error) {
	m := &GroupMail{}
	err := s.gms.GetContext(ctx, m, "SELECT * FROM gms_ranch_group_mail WHERE id = ? AND deletedTime = 0", id)
	return m, notFound(err)
}

func (s *Service) getCheckIn(ctx context.Context, id int64) (*CheckIn, error) {
	m := &CheckIn{}
	err := s.gms.GetContext(ctx, m, "SELECT * FROM gms_ranch_check_in WHERE id = ? AND deletedTime = 0", id)
	return m, notFound(err)
}

func (s *Service) getGuildBattle(ctx context.Context, id int64) (*GuildBattle, error) {
	m := &GuildBattle{}
	err := s.gms.GetContext(ctx, m, "SELECT * FROM gms_ranch_guild_battle WHERE id = ? AND deletedTime = 0", id)
	return m, notFound(err)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.NotFound("记录不存在")
	}
	return err
}

// editable 已激活的记录同步后不可再改(游戏服只在首次同步插入)。
func editable(a Auditable) error {
	if a.Active {
		return httpx.NewError(httpx.CodeOperationFailed, "已激活的记录不允许修改,请先停用或新建一条")
	}
	return nil
}

// CreateMail 新建个人邮件。
func (s *Service) CreateMail(ctx context.Context, m *Mail, uid int64) (*Mail, error) {
	m.Address = strings.TrimSpace(m.Address)
	if m.Address == "" || m.Title == "" {
		return nil, httpx.BadRequest("收件地址与标题不能为空")
	}
	att, err := normalizeJSON(m.Attachment, "[]")
	if err != nil {
		return nil, err
	}
	m.Attachment = att
	m.Auditable = newAuditable(uid)
	res, err := s.gms.NamedExecContext(ctx, `INSERT INTO gms_ranch_mail (address, title, content, attachment, createdBy, createdTime, deletedBy, deletedTime, auditStatus, auditBy, auditTime, active)
		VALUES (:address, :title, :content, :attachment, :createdBy, :createdTime, 0, 0, :auditStatus, 0, 0, 0)`, m)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.getMail(ctx, id)
}

// UpdateMail 修改个人邮件(改动后回到待审核)。
func (s *Service) UpdateMail(ctx context.Context, id int64, in *Mail) (*Mail, error) {
	cur, err := s.getMail(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := editable(cur.Auditable); err != nil {
		return nil, err
	}
	att, err := normalizeJSON(in.Attachment, "[]")
	if err != nil {
		return nil, err
	}
	_, err = s.gms.ExecContext(ctx, "UPDATE gms_ranch_mail SET address = ?, title = ?, content = ?, attachment = ?, auditStatus = ?, auditBy = 0, auditTime = 0 WHERE id = ?",
		strings.TrimSpace(in.Address), in.Title, in.Content, att, AuditWait, id)
	if err != nil {
		return nil, err
	}
	return s.getMail(ctx, id)
}

// CreateGroupMail 新建群邮件。
func (s *Service) CreateGroupMail(ctx context.Context, m *GroupMail, uid int64) (*GroupMail, error) {
	m.GroupMailKey = strings.TrimSpace(m.GroupMailKey)
	if m.GroupMailKey == "" || m.Title == "" {
		return nil, httpx.BadRequest("群邮件Key 与标题不能为空")
	}
	att, err := normalizeJSON(m.Attachment, "[]")
	if err != nil {
		return nil, err
	}
	m.Attachment = att
	m.Auditable = newAuditable(uid)
	res, err := s.gms.NamedExecContext(ctx, `INSERT INTO gms_ranch_group_mail (groupMailKey, title, content, attachment, openTime, closeTime, conditionRegisterTime, createdBy, createdTime, deletedBy, deletedTime, auditStatus, auditBy, auditTime, active)
		VALUES (:groupMailKey, :title, :content, :attachment, :openTime, :closeTime, :conditionRegisterTime, :createdBy, :createdTime, 0, 0, :auditStatus, 0, 0, 0)`, m)
	if err != nil {
		if strings.Contains(err.Error(), "Error 1062") {
			return nil, httpx.NewError(httpx.CodeUniqueConflict, "群邮件Key 已存在")
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.getGroupMail(ctx, id)
}

// UpdateGroupMail 修改群邮件。
func (s *Service) UpdateGroupMail(ctx context.Context, id int64, in *GroupMail) (*GroupMail, error) {
	cur, err := s.getGroupMail(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := editable(cur.Auditable); err != nil {
		return nil, err
	}
	att, err := normalizeJSON(in.Attachment, "[]")
	if err != nil {
		return nil, err
	}
	_, err = s.gms.ExecContext(ctx, `UPDATE gms_ranch_group_mail SET title = ?, content = ?, attachment = ?, openTime = ?, closeTime = ?, conditionRegisterTime = ?, auditStatus = ?, auditBy = 0, auditTime = 0 WHERE id = ?`,
		in.Title, in.Content, att, in.OpenTime, in.CloseTime, in.ConditionRegisterTime, AuditWait, id)
	if err != nil {
		return nil, err
	}
	return s.getGroupMail(ctx, id)
}

// CreateCheckIn 新建签到奖励。
func (s *Service) CreateCheckIn(ctx context.Context, m *CheckIn, uid int64) (*CheckIn, error) {
	reward, err := normalizeJSON(m.Reward, "[]")
	if err != nil {
		return nil, err
	}
	m.Reward = reward
	m.Auditable = newAuditable(uid)
	res, err := s.gms.NamedExecContext(ctx, `INSERT INTO gms_ranch_check_in (reward, createdBy, createdTime, deletedBy, deletedTime, auditStatus, auditBy, auditTime, active)
		VALUES (:reward, :createdBy, :createdTime, 0, 0, :auditStatus, 0, 0, 0)`, m)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.getCheckIn(ctx, id)
}

// UpdateCheckIn 修改签到奖励。
func (s *Service) UpdateCheckIn(ctx context.Context, id int64, in *CheckIn) (*CheckIn, error) {
	cur, err := s.getCheckIn(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := editable(cur.Auditable); err != nil {
		return nil, err
	}
	reward, err := normalizeJSON(in.Reward, "[]")
	if err != nil {
		return nil, err
	}
	if _, err := s.gms.ExecContext(ctx, "UPDATE gms_ranch_check_in SET reward = ?, auditStatus = ?, auditBy = 0, auditTime = 0 WHERE id = ?", reward, AuditWait, id); err != nil {
		return nil, err
	}
	return s.getCheckIn(ctx, id)
}

func validateGuildBattle(g *GuildBattle) error {
	day := func(v int) bool { return v >= 1 && v <= 7 }
	hour := func(v, max int) bool { return v >= 0 && v <= max }
	if !day(g.ApplyStartDay) || !day(g.ApplyEndDay) || !day(g.BattleStartDay) || !day(g.BattleEndDay) {
		return httpx.BadRequest("星期必须为 1~7")
	}
	if !hour(g.ApplyStartHour, 23) || !hour(g.BattleStartHour, 23) || !hour(g.ApplyEndHour, 24) || !hour(g.BattleEndHour, 24) {
		return httpx.BadRequest("小时取值非法")
	}
	if g.EffectiveTo > 0 && g.EffectiveFrom > g.EffectiveTo {
		return httpx.BadRequest("生效开始时间不能晚于结束时间")
	}
	return nil
}

// CreateGuildBattle 新建公会战排期。
func (s *Service) CreateGuildBattle(ctx context.Context, g *GuildBattle, uid int64) (*GuildBattle, error) {
	if err := validateGuildBattle(g); err != nil {
		return nil, err
	}
	g.Auditable = newAuditable(uid)
	res, err := s.gms.NamedExecContext(ctx, `INSERT INTO gms_ranch_guild_battle (applyStartDay, applyStartHour, applyEndDay, applyEndHour, battleStartDay, battleStartHour, battleEndDay, battleEndHour, effectiveFrom, effectiveTo, createdBy, createdTime, deletedBy, deletedTime, auditStatus, auditBy, auditTime, active)
		VALUES (:applyStartDay, :applyStartHour, :applyEndDay, :applyEndHour, :battleStartDay, :battleStartHour, :battleEndDay, :battleEndHour, :effectiveFrom, :effectiveTo, :createdBy, :createdTime, 0, 0, :auditStatus, 0, 0, 0)`, g)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.getGuildBattle(ctx, id)
}

// UpdateGuildBattle 修改公会战排期。
func (s *Service) UpdateGuildBattle(ctx context.Context, id int64, in *GuildBattle) (*GuildBattle, error) {
	cur, err := s.getGuildBattle(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := editable(cur.Auditable); err != nil {
		return nil, err
	}
	if err := validateGuildBattle(in); err != nil {
		return nil, err
	}
	in.ID = id
	in.AuditStatus = AuditWait
	_, err = s.gms.NamedExecContext(ctx, `UPDATE gms_ranch_guild_battle SET applyStartDay = :applyStartDay, applyStartHour = :applyStartHour, applyEndDay = :applyEndDay, applyEndHour = :applyEndHour,
		battleStartDay = :battleStartDay, battleStartHour = :battleStartHour, battleEndDay = :battleEndDay, battleEndHour = :battleEndHour,
		effectiveFrom = :effectiveFrom, effectiveTo = :effectiveTo, auditStatus = :auditStatus, auditBy = 0, auditTime = 0 WHERE id = :id`, in)
	if err != nil {
		return nil, err
	}
	return s.getGuildBattle(ctx, id)
}

// Delete 软删除任一模块的记录。
func (s *Service) Delete(ctx context.Context, module string, id, uid int64) error {
	table, ok := auditTables[module]
	if !ok {
		return httpx.BadRequest("未知模块: " + module)
	}
	return softDelete(ctx, s.gms, table, id, uid)
}

// Audit 批量审核。
func (s *Service) Audit(ctx context.Context, module string, ids []int64, status int, uid int64) error {
	table, ok := auditTables[module]
	if !ok {
		return httpx.BadRequest("未知模块: " + module)
	}
	return setAuditStatus(ctx, s.gms, table, ids, status, uid)
}

// SetActive 激活/停用。
func (s *Service) SetActive(ctx context.Context, module string, id int64, active bool) error {
	table, ok := auditTables[module]
	if !ok {
		return httpx.BadRequest("未知模块: " + module)
	}
	return setActive(ctx, s.gms, table, id, active)
}

// ---------- 同步到游戏服 ----------

// syncList 审核通过且未删除的记录;公会战另要求未过期。
func (s *Service) syncList(ctx context.Context, module string) (any, error) {
	switch module {
	case "mail":
		list := []Mail{}
		err := s.gms.SelectContext(ctx, &list, "SELECT * FROM gms_ranch_mail WHERE deletedTime = 0 AND auditStatus = ? ORDER BY id", AuditApproval)
		return list, err
	case "group-mail":
		list := []GroupMail{}
		err := s.gms.SelectContext(ctx, &list, "SELECT * FROM gms_ranch_group_mail WHERE deletedTime = 0 AND auditStatus = ? ORDER BY id", AuditApproval)
		return list, err
	case "check-in":
		list := []CheckIn{}
		err := s.gms.SelectContext(ctx, &list, "SELECT * FROM gms_ranch_check_in WHERE deletedTime = 0 AND auditStatus = ? ORDER BY id", AuditApproval)
		return list, err
	case "guild-battle":
		list := []GuildBattle{}
		err := s.gms.SelectContext(ctx, &list, "SELECT * FROM gms_ranch_guild_battle WHERE deletedTime = 0 AND auditStatus = ? AND (effectiveTo = 0 OR effectiveTo > ?) ORDER BY id",
			AuditApproval, time.Now().Unix())
		return list, err
	}
	return nil, httpx.BadRequest("未知模块: " + module)
}

// Sync 把审核通过的记录推送到游戏服。
func (s *Service) Sync(ctx context.Context, module string) (string, error) {
	list, err := s.syncList(ctx, module)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	return s.game.Sync(ctx, module, body)
}
