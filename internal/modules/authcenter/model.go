// Package authcenter 实现登录、会话、管理员/角色、权限树、枚举元数据与多语言。
package authcenter

import (
	"encoding/json"
	"strings"

	"cow-manager-backend-go/internal/auth"
)

// User 管理员(auth_center.user)。
type User struct {
	UID             int64  `db:"uid" json:"uid"`
	Username        string `db:"username" json:"username"`
	Nickname        string `db:"nickname" json:"nickname"`
	Avatar          string `db:"avatar" json:"avatar"`
	PasswordHash    string `db:"passwordHash" json:"-"`
	PasswordSalt    string `db:"passwordSalt" json:"-"`
	Note            string `db:"note" json:"note"`
	RolesRaw        string `db:"roles" json:"-"`
	RegisterTime    string `db:"registerTime" json:"registerTime"`
	ExpireTick      int64  `db:"expireTick" json:"expireTick"`
	LockUntilTick   int64  `db:"lockUntilTick" json:"lockUntilTick"`
	Token           string `db:"token" json:"token,omitempty"`
	TokenExpireTick int64  `db:"tokenExpireTick" json:"tokenExpireTick"`
	PhoneNumber     string `db:"phoneNumber" json:"phoneNumber"`
	Settings        string `db:"settings" json:"settings"`

	Roles          []string   `db:"-" json:"roles"`
	PermissionTree *auth.Tree `db:"-" json:"permissionTree,omitempty"`
}

// 用户表的查询列(registerTime 可能为 NULL)。
const userColumns = "uid, username, nickname, avatar, passwordHash, passwordSalt, note, roles, " +
	"IFNULL(DATE_FORMAT(registerTime, '%Y-%m-%d %H:%i:%s'), '') AS registerTime, " +
	"expireTick, lockUntilTick, token, tokenExpireTick, phoneNumber, settings"

// parseRoles 兼容 JSON 数组与逗号分隔两种历史写法。
func parseRoles(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	if strings.HasPrefix(raw, "[") {
		var list []string
		if err := json.Unmarshal([]byte(raw), &list); err == nil {
			return list
		}
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(strings.Trim(p, `"`)); p != "" {
			out = append(out, p)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func encodeRoles(roles []string) string {
	if roles == nil {
		roles = []string{}
	}
	b, _ := json.Marshal(roles)
	return string(b)
}

// Role 角色(auth_center.role)。permissionTree 为权限树 JSON。
type Role struct {
	Role           string          `db:"role" json:"role"`
	Name           string          `db:"name" json:"name"`
	PermissionTree json.RawMessage `db:"permissionTree" json:"permissionTree"`
}

// Localization 多语言行,键为 langKey,其余为各语言列。
type Localization = map[string]any

// 支持的语言列,与 localization 表列名一致。
var languageColumns = []string{"zhCN", "enUS", "ruRU", "deDE", "frFR"}

func isLanguage(col string) bool {
	for _, c := range languageColumns {
		if c == col {
			return true
		}
	}
	return false
}

// MetaEnum 枚举元数据(auth_center.meta_enum)。
type MetaEnum struct {
	Code     string         `db:"code" json:"code"`
	Label    string         `db:"label" json:"label"`
	Editable bool           `db:"editable" json:"editable"`
	Items    []MetaEnumItem `db:"-" json:"items"`
}

// MetaEnumItem 枚举项(auth_center.meta_enum_item)。前端字段名为 code/value/label。
type MetaEnumItem struct {
	EnumCode string `db:"code" json:"-"`
	Code     string `db:"itemCode" json:"code"`
	Value    int64  `db:"itemValue" json:"value"`
	Label    string `db:"itemLabel" json:"label"`
}
