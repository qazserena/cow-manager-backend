// Package perm 集中定义全部权限码,并在启动时注册到权限定义树。
//
// 权限码沿用原 Java 版的四类命名空间,既有角色(ADMIN 根通配、ALL 四类子树通配)无需改动:
//
//	service/{服务}/{动作}        接口权限
//	function/{组}/{功能}         功能权限
//	table/{表}/{create|update|delete|export}
//	game/ranch/{region}         区域可见性
package perm

import "cow-manager-backend/internal/auth"

// Registry 全局权限定义树。
var Registry = auth.NewRegistry()

func reg(code, name string, mids ...string) string { return Registry.Register(code, name, mids...) }

// auth-center
var (
	PermissionTree = reg("service/auth-center/permission_tree", "管理权限树", "授权中心")
	MetaEnum       = reg("service/auth-center/meta_enum", "管理枚举元数据", "授权中心")
	Language       = reg("service/auth-center/language", "管理多语言", "授权中心")
	ManageUser     = reg("function/BasicPermission/MANAGE_USER", "用户管理", "基础权限")
	ManageRole     = reg("function/BasicPermission/MANAGE_ROLE", "角色管理", "基础权限")
)

// gms-ranch 运营动作
var (
	GmsSync     = reg("service/gms-ranch/sync", "同步到游戏服", "牧场管理")
	GmsApproval = reg("service/gms-ranch/approval", "审核通过", "牧场管理")
	GmsReject   = reg("service/gms-ranch/reject", "审核驳回", "牧场管理")
	GmsEnable   = reg("service/gms-ranch/enable", "激活", "牧场管理")
	GmsDisable  = reg("service/gms-ranch/disable", "停用", "牧场管理")
	GmsGet      = reg("service/gms-ranch/get", "代理游戏服 GET", "牧场管理")
	GmsPost     = reg("service/gms-ranch/post", "代理游戏服 POST", "牧场管理")
)

// task-runner
var (
	TaskList     = reg("service/task-runner/TaskController/list", "任务列表", "定时任务", "任务管理")
	TaskLog      = reg("service/task-runner/TaskController/log", "任务日志", "定时任务", "任务管理")
	TaskSwitch   = reg("service/task-runner/TaskController/enableOrDisableTask", "启停任务", "定时任务", "任务管理")
	TaskCron     = reg("service/task-runner/TaskController/updateCronTrigger", "修改定时规则", "定时任务", "任务管理")
	TaskManual   = reg("service/task-runner/TaskController/manualSchedule", "手动补跑", "定时任务", "任务管理")
	TaskUpload   = reg("service/task-runner/TaskController/upload", "上传任务", "定时任务", "任务管理")
	ConfigReload = reg("service/task-runner/ConfigureController/reload", "重载配置", "定时任务", "任务配置")
)

// Table 数据表权限码。
func Table(table, action, label string) string {
	return reg("table/"+table+"/"+action, actionName(action), label)
}

func actionName(action string) string {
	switch action {
	case "create":
		return "创建"
	case "update":
		return "更新"
	case "delete":
		return "删除"
	case "export":
		return "导出"
	}
	return action
}

// Game 区域权限码。
func Game(region string) string {
	return reg("game/ranch/"+region, region, "ranch")
}
