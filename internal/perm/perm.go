// Package perm 集中定义全部权限码,并在启动时注册到权限定义树。
//
// 权限模型(2026-10 重做):以「功能」为单位,每个功能只有两档 ——
//
//	feature/{key}/view   只读:看列表 / 详情 / 报表,导出
//	feature/{key}/edit   可更改:新建 / 修改 / 删除 / 审核 / 同步 / 标记 / 调分等一切写操作
//
// 再加一类区域可见性:
//
//	game/ranch/{region}  能看到哪个区域(前端按它过滤可切换的区域)
//
// 角色的权限树里勾 feature/{key} 通配即「查看 + 可更改」,勾 feature 通配即全部功能;
// 根通配(ADMIN)拥有一切。老的 service/* table/* function/* 三类码已废弃,不再有任何接口校验它们。
package perm

import "cow-manager-backend/internal/auth"

// Registry 全局权限定义树。
var Registry = auth.NewRegistry()

// Feature 一个受权限控制的功能(对应一个菜单项)。
type Feature struct {
	Key   string `json:"key"`   // feature/{Key}
	Name  string `json:"name"`  // 功能名
	Group string `json:"group"` // 所属菜单组
	// Editable 为 false 的功能只有 view 一档(纯查询 / 报表)
	Editable bool `json:"editable"`
}

// Features 全部功能,顺序即前端角色编辑器里的展示顺序。
var Features = []Feature{
	{Key: "ranch-user", Name: "用户查询", Group: "数据查询"},
	{Key: "ranch-guild", Name: "公会查询", Group: "数据查询"},
	{Key: "mail", Name: "邮件", Group: "运营管理", Editable: true},
	{Key: "group-mail", Name: "群邮件", Group: "运营管理", Editable: true},
	{Key: "game-config", Name: "游戏配置(签到 / 游戏服参数)", Group: "运营管理", Editable: true},
	{Key: "guild-battle", Name: "公会战", Group: "运营管理", Editable: true},
	{Key: "analysis", Name: "数据分析(全部报表)", Group: "数据分析"},
	{Key: "portal-user", Name: "官网用户管理", Group: "官网运营", Editable: true},
	{Key: "portal-social", Name: "社交媒体运营", Group: "官网运营", Editable: true},
	{Key: "portal-invite", Name: "邀请计划", Group: "官网运营", Editable: true},
	{Key: "task", Name: "定时任务", Group: "运维", Editable: true},
	{Key: "system-setting", Name: "基础设置(多语言)", Group: "系统", Editable: true},
	{Key: "system-user", Name: "用户与角色", Group: "系统", Editable: true},
}

// 功能权限码常量(view / edit)。
var (
	RanchUserView     = View("ranch-user")
	RanchGuildView    = View("ranch-guild")
	MailView          = View("mail")
	MailEdit          = Edit("mail")
	GroupMailView     = View("group-mail")
	GroupMailEdit     = Edit("group-mail")
	GameConfigView    = View("game-config")
	GameConfigEdit    = Edit("game-config")
	GuildBattleView   = View("guild-battle")
	GuildBattleEdit   = Edit("guild-battle")
	AnalysisView      = View("analysis")
	PortalUserView    = View("portal-user")
	PortalUserEdit    = Edit("portal-user")
	PortalSocialView  = View("portal-social")
	PortalSocialEdit  = Edit("portal-social")
	PortalInviteView  = View("portal-invite")
	PortalInviteEdit  = Edit("portal-invite")
	TaskView          = View("task")
	TaskEdit          = Edit("task")
	SystemSettingView = View("system-setting")
	SystemSettingEdit = Edit("system-setting")
	SystemUserView    = View("system-user")
	SystemUserEdit    = Edit("system-user")
)

func init() {
	for _, f := range Features {
		Registry.Register("feature/"+f.Key+"/view", "查看", f.Group+" · "+f.Name)
		if f.Editable {
			Registry.Register("feature/"+f.Key+"/edit", "可更改", f.Group+" · "+f.Name)
		}
	}
}

// View 功能的只读权限码。
func View(key string) string { return "feature/" + key + "/view" }

// Edit 功能的可更改权限码。
func Edit(key string) string { return "feature/" + key + "/edit" }

// Game 区域权限码。
func Game(region string) string {
	return Registry.Register("game/ranch/"+region, region, "ranch")
}
