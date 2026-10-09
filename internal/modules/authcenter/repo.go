package authcenter

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jmoiron/sqlx"

	"cow-manager-backend/internal/httpx"
)

// Repo auth_center 库的数据访问。
type Repo struct{ db *sqlx.DB }

// ---------- 用户 ----------

func (r *Repo) userByWhere(ctx context.Context, where string, arg any) (*User, error) {
	u := &User{}
	err := r.db.GetContext(ctx, u, "SELECT "+userColumns+" FROM `user` WHERE "+where, arg)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.Roles = parseRoles(u.RolesRaw)
	return u, nil
}

// UserByUsername 不存在返回 nil。
func (r *Repo) UserByUsername(ctx context.Context, username string) (*User, error) {
	return r.userByWhere(ctx, "username = ?", username)
}

// UserByUID 不存在返回 nil。
func (r *Repo) UserByUID(ctx context.Context, uid int64) (*User, error) {
	return r.userByWhere(ctx, "uid = ?", uid)
}

// ListUsers 分页列出用户(username 模糊)。
func (r *Repo) ListUsers(ctx context.Context, usernameLike string, page, size int, order string) ([]*User, int64, error) {
	where := ""
	var args []any
	if usernameLike != "" {
		where = " WHERE username LIKE ?"
		args = append(args, "%"+usernameLike+"%")
	}
	var total int64
	if err := r.db.GetContext(ctx, &total, "SELECT COUNT(*) FROM `user`"+where, args...); err != nil {
		return nil, 0, err
	}
	list := []*User{}
	q := "SELECT " + userColumns + " FROM `user`" + where + order + " LIMIT ? OFFSET ?"
	if err := r.db.SelectContext(ctx, &list, q, append(args, size, page*size)...); err != nil {
		return nil, 0, err
	}
	for _, u := range list {
		u.Roles = parseRoles(u.RolesRaw)
		u.Token = ""
	}
	return list, total, nil
}

// CreateUser 插入用户,返回 uid。
func (r *Repo) CreateUser(ctx context.Context, u *User) (int64, error) {
	u.RolesRaw = encodeRoles(u.Roles)
	res, err := r.db.NamedExecContext(ctx, `INSERT INTO `+"`user`"+` (username, nickname, avatar, passwordHash, passwordSalt, note, roles, expireTick, lockUntilTick, phoneNumber, settings)
		VALUES (:username, :nickname, :avatar, :passwordHash, :passwordSalt, :note, :roles, :expireTick, :lockUntilTick, :phoneNumber, :settings)`, u)
	if err != nil {
		if isDuplicate(err) {
			return 0, httpx.NewError(httpx.CodeUsernameExist, "用户名已存在")
		}
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateUser 更新资料类字段(不含密码与 token)。
func (r *Repo) UpdateUser(ctx context.Context, u *User) error {
	u.RolesRaw = encodeRoles(u.Roles)
	_, err := r.db.NamedExecContext(ctx, `UPDATE `+"`user`"+` SET username = :username, nickname = :nickname, avatar = :avatar, note = :note,
		roles = :roles, expireTick = :expireTick, lockUntilTick = :lockUntilTick, phoneNumber = :phoneNumber WHERE uid = :uid`, u)
	if err != nil && isDuplicate(err) {
		return httpx.NewError(httpx.CodeUsernameExist, "用户名已存在")
	}
	return err
}

// DeleteUser 删除用户。
func (r *Repo) DeleteUser(ctx context.Context, uid int64) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM `user` WHERE uid = ?", uid)
	return err
}

// UpdatePassword 写入新盐与哈希。
func (r *Repo) UpdatePassword(ctx context.Context, uid int64, hash, salt string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE `user` SET passwordHash = ?, passwordSalt = ? WHERE uid = ?", hash, salt, uid)
	return err
}

// UpdateToken 写入会话 token 与过期时间(毫秒)。
func (r *Repo) UpdateToken(ctx context.Context, uid int64, token string, expireTick int64) error {
	_, err := r.db.ExecContext(ctx, "UPDATE `user` SET token = ?, tokenExpireTick = ? WHERE uid = ?", token, expireTick, uid)
	return err
}

// UpdateProfile 更新昵称/头像/手机号。
func (r *Repo) UpdateProfile(ctx context.Context, uid int64, nickname, avatar, phone string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE `user` SET nickname = ?, avatar = ?, phoneNumber = ? WHERE uid = ?", nickname, avatar, phone, uid)
	return err
}

// UpdateSettings 更新个人设置。
func (r *Repo) UpdateSettings(ctx context.Context, uid int64, settings string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE `user` SET settings = ? WHERE uid = ?", settings, uid)
	return err
}

// UpdateLock 更新锁定截止(毫秒,0 为解锁)。
func (r *Repo) UpdateLock(ctx context.Context, username string, lockUntilTick int64) error {
	res, err := r.db.ExecContext(ctx, "UPDATE `user` SET lockUntilTick = ? WHERE username = ?", lockUntilTick, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.NewError(httpx.CodeUserNotExist, "用户不存在")
	}
	return nil
}

// UpdateRoles 更新角色列表。
func (r *Repo) UpdateRoles(ctx context.Context, uid int64, roles []string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE `user` SET roles = ? WHERE uid = ?", encodeRoles(roles), uid)
	return err
}

// ---------- 角色 ----------

// ListRoles 全部角色。
func (r *Repo) ListRoles(ctx context.Context) ([]Role, error) {
	list := []Role{}
	err := r.db.SelectContext(ctx, &list, "SELECT role, name, permissionTree FROM role ORDER BY role")
	return list, err
}

// RoleByName 不存在返回 nil。
func (r *Repo) RoleByName(ctx context.Context, role string) (*Role, error) {
	rl := &Role{}
	err := r.db.GetContext(ctx, rl, "SELECT role, name, permissionTree FROM role WHERE role = ?", role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return rl, err
}

// RolesByNames 批量取角色。
func (r *Repo) RolesByNames(ctx context.Context, names []string) ([]Role, error) {
	if len(names) == 0 {
		return nil, nil
	}
	q, args, err := sqlx.In("SELECT role, name, permissionTree FROM role WHERE role IN (?)", names)
	if err != nil {
		return nil, err
	}
	list := []Role{}
	err = r.db.SelectContext(ctx, &list, r.db.Rebind(q), args...)
	return list, err
}

// CreateRole 新建角色。
func (r *Repo) CreateRole(ctx context.Context, rl *Role) error {
	_, err := r.db.ExecContext(ctx, "INSERT INTO role (role, name, permissionTree) VALUES (?, ?, ?)", rl.Role, rl.Name, string(rl.PermissionTree))
	if err != nil && isDuplicate(err) {
		return httpx.NewError(httpx.CodeRoleExist, "角色已存在")
	}
	return err
}

// UpdateRole 更新角色。
func (r *Repo) UpdateRole(ctx context.Context, rl *Role) error {
	res, err := r.db.ExecContext(ctx, "UPDATE role SET name = ?, permissionTree = ? WHERE role = ?", rl.Name, string(rl.PermissionTree), rl.Role)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if existing, _ := r.RoleByName(ctx, rl.Role); existing == nil {
			return httpx.NotFound("角色不存在")
		}
	}
	return nil
}

// DeleteRole 删除角色。
func (r *Repo) DeleteRole(ctx context.Context, role string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM role WHERE role = ?", role)
	return err
}

// ---------- 多语言 ----------

// LanguageQuery 返回 [{langKey, <lang>}] 列表。
func (r *Repo) LanguageQuery(ctx context.Context, lang string) ([]Localization, error) {
	if !isLanguage(lang) {
		return nil, httpx.BadRequest("不支持的语言: " + lang)
	}
	rows, err := r.db.QueryxContext(ctx, "SELECT langKey, IFNULL(`"+lang+"`, '') AS `"+lang+"` FROM localization ORDER BY langKey")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLocalization(rows)
}

// LanguageList 全部语言行。
func (r *Repo) LanguageList(ctx context.Context, keyLike string) ([]Localization, error) {
	cols := "langKey"
	for _, c := range languageColumns {
		cols += ", IFNULL(`" + c + "`, '') AS `" + c + "`"
	}
	q := "SELECT " + cols + " FROM localization"
	var args []any
	if keyLike != "" {
		q += " WHERE langKey LIKE ?"
		args = append(args, "%"+keyLike+"%")
	}
	rows, err := r.db.QueryxContext(ctx, q+" ORDER BY langKey", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLocalization(rows)
}

func scanLocalization(rows *sqlx.Rows) ([]Localization, error) {
	out := []Localization{}
	for rows.Next() {
		m := map[string]any{}
		if err := rows.MapScan(m); err != nil {
			return nil, err
		}
		for k, v := range m {
			if b, ok := v.([]byte); ok {
				m[k] = string(b)
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// LanguageCreate 插入空行。
func (r *Repo) LanguageCreate(ctx context.Context, langKey string) error {
	_, err := r.db.ExecContext(ctx, "INSERT INTO localization (langKey) VALUES (?)", langKey)
	if err != nil && isDuplicate(err) {
		return httpx.NewError(httpx.CodeUniqueConflict, "langKey 已存在")
	}
	return err
}

// LanguageUpsert 写入各语言文案。
func (r *Repo) LanguageUpsert(ctx context.Context, langKey string, values map[string]string) error {
	cols := []string{"langKey"}
	marks := []string{"?"}
	args := []any{langKey}
	var updates []string
	for _, c := range languageColumns {
		if v, ok := values[c]; ok {
			cols = append(cols, "`"+c+"`")
			marks = append(marks, "?")
			args = append(args, v)
			updates = append(updates, "`"+c+"` = VALUES(`"+c+"`)")
		}
	}
	q := "INSERT INTO localization (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(marks, ", ") + ")"
	if len(updates) > 0 {
		q += " ON DUPLICATE KEY UPDATE " + strings.Join(updates, ", ")
	} else {
		q += " ON DUPLICATE KEY UPDATE langKey = VALUES(langKey)"
	}
	_, err := r.db.ExecContext(ctx, q, args...)
	return err
}

// LanguageDelete 删除。
func (r *Repo) LanguageDelete(ctx context.Context, langKey string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM localization WHERE langKey = ?", langKey)
	return err
}

// ---------- 枚举元数据 ----------

// EnumList 全部枚举及其项。
func (r *Repo) EnumList(ctx context.Context) ([]MetaEnum, error) {
	enums := []MetaEnum{}
	if err := r.db.SelectContext(ctx, &enums, "SELECT code, label, editable FROM meta_enum ORDER BY code"); err != nil {
		return nil, err
	}
	items := []MetaEnumItem{}
	if err := r.db.SelectContext(ctx, &items, "SELECT code, IFNULL(itemCode, '') AS itemCode, itemValue, itemLabel FROM meta_enum_item ORDER BY code, itemValue"); err != nil {
		return nil, err
	}
	byCode := map[string][]MetaEnumItem{}
	for _, it := range items {
		byCode[it.EnumCode] = append(byCode[it.EnumCode], it)
	}
	for i := range enums {
		enums[i].Items = byCode[enums[i].Code]
		if enums[i].Items == nil {
			enums[i].Items = []MetaEnumItem{}
		}
	}
	return enums, nil
}

// EnumByCode 不存在返回 nil。
func (r *Repo) EnumByCode(ctx context.Context, code string) (*MetaEnum, error) {
	e := &MetaEnum{}
	err := r.db.GetContext(ctx, e, "SELECT code, label, editable FROM meta_enum WHERE code = ?", code)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

// EnumCreate 新建枚举。
func (r *Repo) EnumCreate(ctx context.Context, e *MetaEnum) error {
	_, err := r.db.ExecContext(ctx, "INSERT INTO meta_enum (code, label, editable) VALUES (?, ?, 1)", e.Code, e.Label)
	if err != nil && isDuplicate(err) {
		return httpx.NewError(httpx.CodeUniqueConflict, "枚举已存在")
	}
	return err
}

// EnumUpdate 更新枚举名称。
func (r *Repo) EnumUpdate(ctx context.Context, code, label string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE meta_enum SET label = ? WHERE code = ?", label, code)
	return err
}

// EnumDelete 删除枚举及其项。
func (r *Repo) EnumDelete(ctx context.Context, code string) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM meta_enum_item WHERE code = ?", code); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM meta_enum WHERE code = ?", code); err != nil {
		return err
	}
	return tx.Commit()
}

// EnumItems 某枚举的全部项。
func (r *Repo) EnumItems(ctx context.Context, code string) ([]MetaEnumItem, error) {
	items := []MetaEnumItem{}
	err := r.db.SelectContext(ctx, &items, "SELECT code, IFNULL(itemCode, '') AS itemCode, itemValue, itemLabel FROM meta_enum_item WHERE code = ? ORDER BY itemValue", code)
	return items, err
}

// EnumItemCreate 新建枚举项。
func (r *Repo) EnumItemCreate(ctx context.Context, it *MetaEnumItem) error {
	_, err := r.db.ExecContext(ctx, "INSERT INTO meta_enum_item (code, itemCode, itemValue, itemLabel) VALUES (?, ?, ?, ?)", it.EnumCode, it.Code, it.Value, it.Label)
	if err != nil && isDuplicate(err) {
		return httpx.NewError(httpx.CodeUniqueConflict, "枚举项已存在")
	}
	return err
}

// EnumItemUpdate 更新枚举项。
func (r *Repo) EnumItemUpdate(ctx context.Context, enumCode, itemCode string, it *MetaEnumItem) error {
	_, err := r.db.ExecContext(ctx, "UPDATE meta_enum_item SET itemCode = ?, itemValue = ?, itemLabel = ? WHERE code = ? AND itemCode = ?", it.Code, it.Value, it.Label, enumCode, itemCode)
	if err != nil && isDuplicate(err) {
		return httpx.NewError(httpx.CodeUniqueConflict, "枚举项已存在")
	}
	return err
}

// EnumItemDelete 删除枚举项。
func (r *Repo) EnumItemDelete(ctx context.Context, enumCode, itemCode string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM meta_enum_item WHERE code = ? AND itemCode = ?", enumCode, itemCode)
	return err
}

// EnumLabel 查某枚举值的文案(导出翻译用)。
func (r *Repo) EnumLabel(ctx context.Context, enumCode string, value int64) (string, bool) {
	var label string
	err := r.db.GetContext(ctx, &label, "SELECT itemLabel FROM meta_enum_item WHERE code = ? AND itemValue = ? LIMIT 1", enumCode, value)
	if err != nil {
		return "", false
	}
	return label, true
}

func isDuplicate(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Error 1062")
}
