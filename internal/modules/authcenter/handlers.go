package authcenter

import (
	"net/http"
	"strconv"
	"strings"

	"cow-manager-backend/internal/auth"
	"cow-manager-backend/internal/httpx"
	"cow-manager-backend/internal/perm"
)

// Register 挂载路由。
func (s *Service) Register(mux *http.ServeMux) {
	// 登录 / 会话
	mux.HandleFunc("POST /login", httpx.H(s.handleLogin))
	mux.HandleFunc("POST /login-with-token", httpx.H(auth.RequireLogin(s.handleLoginWithToken)))
	mux.HandleFunc("POST /logout", httpx.H(s.handleLogout))

	// 个人中心
	mux.HandleFunc("POST /me/change-password", httpx.H(auth.RequireLogin(s.handleChangePassword)))
	mux.HandleFunc("POST /me/update-profile", httpx.H(auth.RequireLogin(s.handleUpdateProfile)))
	mux.HandleFunc("POST /me/update-settings", httpx.H(auth.RequireLogin(s.handleUpdateSettings)))
	// 二步验证(认证器)
	mux.HandleFunc("POST /me/2fa/setup", httpx.H(auth.RequireLogin(s.handleTwoFactorSetup)))
	mux.HandleFunc("POST /me/2fa/enable", httpx.H(auth.RequireLogin(s.handleTwoFactorEnable)))
	mux.HandleFunc("POST /me/2fa/disable", httpx.H(auth.RequireLogin(s.handleTwoFactorDisable)))

	// 权限定义树
	// 权限定义(功能清单 + 定义树):角色编辑器用,有「用户与角色 · 查看」即可
	mux.HandleFunc("GET /permission/tree", httpx.H(auth.Require(perm.SystemUserView, s.handlePermissionTree)))
	mux.HandleFunc("GET /permission/features", httpx.H(auth.Require(perm.SystemUserView, s.handleFeatures)))

	// 管理员(列表仅需登录:运营表格的"创建人/审核人"列要把 uid 翻译成用户名;不含密码与 token)
	// 管理员:完整列表(含手机号 / 锁定 / 2FA 状态)要「用户与角色 · 查看」;
	// 运营表格把 createdBy / auditBy 翻译成用户名只用 /options(仅 uid / 用户名 / 昵称),登录即可
	mux.HandleFunc("GET /system/users/options", httpx.H(auth.RequireLogin(s.handleUserOptions)))
	mux.HandleFunc("GET /system/users", httpx.H(auth.Require(perm.SystemUserView, s.handleListUsers)))
	mux.HandleFunc("POST /system/users", httpx.H(auth.Require(perm.SystemUserEdit, s.handleCreateUser)))
	mux.HandleFunc("PUT /system/users/{uid}", httpx.H(auth.Require(perm.SystemUserEdit, s.handleUpdateUser)))
	mux.HandleFunc("DELETE /system/users/{uid}", httpx.H(auth.Require(perm.SystemUserEdit, s.handleDeleteUser)))
	mux.HandleFunc("POST /system/users/{username}/lock", httpx.H(auth.Require(perm.SystemUserEdit, s.handleLockUser)))
	mux.HandleFunc("POST /system/users/{username}/unlock", httpx.H(auth.Require(perm.SystemUserEdit, s.handleUnlockUser)))
	mux.HandleFunc("POST /system/users/{username}/update_role", httpx.H(auth.Require(perm.SystemUserEdit, s.handleUpdateUserRoles)))
	mux.HandleFunc("POST /system/users/{username}/reset_2fa", httpx.H(auth.Require(perm.SystemUserEdit, s.handleResetTwoFactor)))

	// 角色(列表仅需登录:用户编辑器要用角色下拉)
	mux.HandleFunc("GET /system/roles", httpx.H(auth.Require(perm.SystemUserView, s.handleListRoles)))
	mux.HandleFunc("POST /system/roles", httpx.H(auth.Require(perm.SystemUserEdit, s.handleCreateRole)))
	mux.HandleFunc("PUT /system/roles/{role}", httpx.H(auth.Require(perm.SystemUserEdit, s.handleUpdateRole)))
	mux.HandleFunc("DELETE /system/roles/{role}", httpx.H(auth.Require(perm.SystemUserEdit, s.handleDeleteRole)))

	// 多语言
	mux.HandleFunc("GET /locale/language/languages", httpx.H(auth.RequireLogin(s.handleLanguages)))
	mux.HandleFunc("GET /locale/language/query", httpx.H(auth.RequireLogin(s.handleLanguageQuery)))
	mux.HandleFunc("GET /locale/language", httpx.H(auth.Require(perm.SystemSettingView, s.handleLanguageList)))
	mux.HandleFunc("POST /locale/language", httpx.H(auth.Require(perm.SystemSettingEdit, s.handleLanguageCreate)))
	mux.HandleFunc("PUT /locale/language/{langKey}", httpx.H(auth.Require(perm.SystemSettingEdit, s.handleLanguageUpdate)))
	mux.HandleFunc("DELETE /locale/language/{langKey}", httpx.H(auth.Require(perm.SystemSettingEdit, s.handleLanguageDelete)))

	// 枚举元数据(读取仅需登录)
	mux.HandleFunc("GET /meta/enum", httpx.H(auth.RequireLogin(s.handleEnumList)))
	mux.HandleFunc("POST /meta/enum", httpx.H(auth.Require(perm.SystemSettingEdit, s.handleEnumCreate)))
	mux.HandleFunc("PUT /meta/enum/{code}", httpx.H(auth.Require(perm.SystemSettingEdit, s.handleEnumUpdate)))
	mux.HandleFunc("DELETE /meta/enum/{code}", httpx.H(auth.Require(perm.SystemSettingEdit, s.handleEnumDelete)))
	mux.HandleFunc("GET /meta/enum/{code}", httpx.H(auth.RequireLogin(s.handleEnumItems)))
	mux.HandleFunc("POST /meta/enum/{code}", httpx.H(auth.Require(perm.SystemSettingEdit, s.handleEnumItemCreate)))
	mux.HandleFunc("PUT /meta/enum/{code}/{itemCode}", httpx.H(auth.Require(perm.SystemSettingEdit, s.handleEnumItemUpdate)))
	mux.HandleFunc("DELETE /meta/enum/{code}/{itemCode}", httpx.H(auth.Require(perm.SystemSettingEdit, s.handleEnumItemDelete)))
}

// ---------- 登录 ----------

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Otp      string `json:"otp"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if body.Username == "" || body.Password == "" {
		return httpx.BadRequest("用户名或密码为空")
	}
	u, err := s.Login(r.Context(), body.Username, body.Password, body.Otp)
	if err != nil {
		return err
	}
	httpx.OK(w, u)
	return nil
}

func (s *Service) handleLoginWithToken(w http.ResponseWriter, r *http.Request) error {
	u, err := s.CurrentUser(r.Context(), auth.SessionFrom(r.Context()))
	if err != nil {
		return err
	}
	httpx.OK(w, u)
	return nil
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) error {
	if sess := auth.SessionFrom(r.Context()); sess != nil {
		if err := s.Logout(r.Context(), sess); err != nil {
			return err
		}
	}
	httpx.NoContent(w)
	return nil
}

// ---------- 个人中心 ----------

func (s *Service) handleChangePassword(w http.ResponseWriter, r *http.Request) error {
	sess := auth.SessionFrom(r.Context())
	if err := s.ChangePassword(r.Context(), sess.UID, httpx.FormValue(r, "oldPassword"), httpx.FormValue(r, "newPassword")); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handleUpdateProfile(w http.ResponseWriter, r *http.Request) error {
	sess := auth.SessionFrom(r.Context())
	if err := s.repo.UpdateProfile(r.Context(), sess.UID, httpx.FormValue(r, "nickname"), httpx.FormValue(r, "avatar"), httpx.FormValue(r, "phoneNumber")); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handleUpdateSettings(w http.ResponseWriter, r *http.Request) error {
	sess := auth.SessionFrom(r.Context())
	settings := httpx.FormValue(r, "settings")
	if len(settings) > 1024 {
		return httpx.BadRequest("settings 过长")
	}
	if err := s.repo.UpdateSettings(r.Context(), sess.UID, settings); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handleTwoFactorSetup(w http.ResponseWriter, r *http.Request) error {
	secret, uri, err := s.TwoFactorSetup(r.Context(), auth.SessionFrom(r.Context()))
	if err != nil {
		return err
	}
	httpx.OK(w, map[string]string{"secret": secret, "uri": uri, "issuer": totpIssuer})
	return nil
}

func (s *Service) handleTwoFactorEnable(w http.ResponseWriter, r *http.Request) error {
	if err := s.TwoFactorEnable(r.Context(), auth.SessionFrom(r.Context()), httpx.FormValue(r, "code")); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handleTwoFactorDisable(w http.ResponseWriter, r *http.Request) error {
	if err := s.TwoFactorDisable(r.Context(), auth.SessionFrom(r.Context()), httpx.FormValue(r, "code")); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handleResetTwoFactor(w http.ResponseWriter, r *http.Request) error {
	if err := s.ResetTwoFactor(r.Context(), auth.SessionFrom(r.Context()), r.PathValue("username")); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handlePermissionTree(w http.ResponseWriter, _ *http.Request) error {
	httpx.OK(w, perm.Registry.Tree())
	return nil
}

// handleFeatures 功能清单(角色编辑器按「查看 / 可更改」矩阵展示)。
func (s *Service) handleFeatures(w http.ResponseWriter, _ *http.Request) error {
	httpx.OK(w, perm.Features)
	return nil
}

// ---------- 管理员 ----------

func (s *Service) handleUserOptions(w http.ResponseWriter, r *http.Request) error {
	list, err := s.repo.ListUserOptions(r.Context())
	if err != nil {
		return err
	}
	httpx.OK(w, list)
	return nil
}

func (s *Service) handleListUsers(w http.ResponseWriter, r *http.Request) error {
	page, size := httpx.Pagination(r)
	order := " ORDER BY uid ASC"
	for _, sv := range r.URL.Query()["sort"] {
		name, dir, _ := strings.Cut(sv, ",")
		switch name {
		case "uid", "username", "registerTime", "expireTick", "lockUntilTick":
			d := "ASC"
			if strings.HasPrefix(strings.ToLower(dir), "desc") {
				d = "DESC"
			}
			order = " ORDER BY " + name + " " + d
		}
	}
	list, total, err := s.repo.ListUsers(r.Context(), httpx.FormValue(r, "username_like"), page, size, order)
	if err != nil {
		return err
	}
	httpx.OK(w, &httpx.Page{Content: list, TotalElements: total})
	return nil
}

type userBody struct {
	User
	Password string `json:"password"`
}

func (s *Service) handleCreateUser(w http.ResponseWriter, r *http.Request) error {
	var body userBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	u, err := s.CreateUser(r.Context(), auth.SessionFrom(r.Context()), &body.User, body.Password)
	if err != nil {
		return err
	}
	u.Token = ""
	httpx.OK(w, u)
	return nil
}

func (s *Service) handleUpdateUser(w http.ResponseWriter, r *http.Request) error {
	uid, err := httpx.PathInt64(r, "uid")
	if err != nil {
		return err
	}
	var body userBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	body.UID = uid
	u, err := s.UpdateUser(r.Context(), auth.SessionFrom(r.Context()), &body.User, body.Password)
	if err != nil {
		return err
	}
	u.Token = ""
	httpx.OK(w, u)
	return nil
}

func (s *Service) handleDeleteUser(w http.ResponseWriter, r *http.Request) error {
	uid, err := httpx.PathInt64(r, "uid")
	if err != nil {
		return err
	}
	if err := s.DeleteUser(r.Context(), auth.SessionFrom(r.Context()), uid); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handleLockUser(w http.ResponseWriter, r *http.Request) error {
	until, err := strconv.ParseInt(httpx.FormValue(r, "lockUntilTime"), 10, 64)
	if err != nil {
		return httpx.BadRequest("lockUntilTime 非法")
	}
	if err := s.LockUser(r.Context(), auth.SessionFrom(r.Context()), r.PathValue("username"), until); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handleUnlockUser(w http.ResponseWriter, r *http.Request) error {
	if err := s.LockUser(r.Context(), auth.SessionFrom(r.Context()), r.PathValue("username"), 0); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handleUpdateUserRoles(w http.ResponseWriter, r *http.Request) error {
	var roles []string
	if err := httpx.DecodeJSON(r, &roles); err != nil {
		return err
	}
	if err := s.UpdateRoles(r.Context(), auth.SessionFrom(r.Context()), r.PathValue("username"), roles); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

// ---------- 角色 ----------

// handleListRoles 角色不多,始终整表返回;按分页形态包装以便前端表格组件直接消费。
func (s *Service) handleListRoles(w http.ResponseWriter, r *http.Request) error {
	list, err := s.repo.ListRoles(r.Context())
	if err != nil {
		return err
	}
	httpx.OK(w, &httpx.Page{Content: list, TotalElements: int64(len(list))})
	return nil
}

func (s *Service) handleCreateRole(w http.ResponseWriter, r *http.Request) error {
	var rl Role
	if err := httpx.DecodeJSON(r, &rl); err != nil {
		return err
	}
	if err := s.SaveRole(r.Context(), auth.SessionFrom(r.Context()), &rl, true); err != nil {
		return err
	}
	httpx.OK(w, rl)
	return nil
}

func (s *Service) handleUpdateRole(w http.ResponseWriter, r *http.Request) error {
	var rl Role
	if err := httpx.DecodeJSON(r, &rl); err != nil {
		return err
	}
	rl.Role = r.PathValue("role")
	if err := s.SaveRole(r.Context(), auth.SessionFrom(r.Context()), &rl, false); err != nil {
		return err
	}
	httpx.OK(w, rl)
	return nil
}

func (s *Service) handleDeleteRole(w http.ResponseWriter, r *http.Request) error {
	if err := s.DeleteRole(r.Context(), auth.SessionFrom(r.Context()), r.PathValue("role")); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

// ---------- 多语言 ----------

func (s *Service) handleLanguages(w http.ResponseWriter, _ *http.Request) error {
	httpx.OK(w, languageColumns)
	return nil
}

func (s *Service) handleLanguageQuery(w http.ResponseWriter, r *http.Request) error {
	list, err := s.repo.LanguageQuery(r.Context(), httpx.FormValue(r, "lang"))
	if err != nil {
		return err
	}
	httpx.OK(w, list)
	return nil
}

func (s *Service) handleLanguageList(w http.ResponseWriter, r *http.Request) error {
	list, err := s.repo.LanguageList(r.Context(), httpx.FormValue(r, "langKey_like"))
	if err != nil {
		return err
	}
	httpx.OK(w, list)
	return nil
}

func (s *Service) handleLanguageCreate(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	key, _ := body["langKey"].(string)
	key = strings.TrimSpace(key)
	if key == "" {
		return httpx.BadRequest("langKey 不能为空")
	}
	values := languageValues(body)
	var err error
	if len(values) > 0 {
		err = s.repo.LanguageUpsert(r.Context(), key, values)
	} else {
		err = s.repo.LanguageCreate(r.Context(), key)
	}
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handleLanguageUpdate(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if err := s.repo.LanguageUpsert(r.Context(), r.PathValue("langKey"), languageValues(body)); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func languageValues(body map[string]any) map[string]string {
	values := map[string]string{}
	for k, v := range body {
		if !isLanguage(k) {
			continue
		}
		if sv, ok := v.(string); ok {
			values[k] = sv
		} else if v == nil {
			values[k] = ""
		}
	}
	return values
}

func (s *Service) handleLanguageDelete(w http.ResponseWriter, r *http.Request) error {
	if err := s.repo.LanguageDelete(r.Context(), r.PathValue("langKey")); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

// ---------- 枚举 ----------

func (s *Service) handleEnumList(w http.ResponseWriter, r *http.Request) error {
	list, err := s.repo.EnumList(r.Context())
	if err != nil {
		return err
	}
	httpx.OK(w, list)
	return nil
}

func (s *Service) handleEnumCreate(w http.ResponseWriter, r *http.Request) error {
	var e MetaEnum
	if err := httpx.DecodeJSON(r, &e); err != nil {
		return err
	}
	e.Code = strings.TrimSpace(e.Code)
	if e.Code == "" {
		return httpx.BadRequest("code 不能为空")
	}
	if err := s.repo.EnumCreate(r.Context(), &e); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) editableEnum(r *http.Request, code string) error {
	e, err := s.repo.EnumByCode(r.Context(), code)
	if err != nil {
		return err
	}
	if e == nil {
		return httpx.NotFound("枚举不存在")
	}
	if !e.Editable {
		return httpx.NewError(httpx.CodeOperationFailed, "该枚举由程序维护,不可编辑")
	}
	return nil
}

func (s *Service) handleEnumUpdate(w http.ResponseWriter, r *http.Request) error {
	code := r.PathValue("code")
	if err := s.editableEnum(r, code); err != nil {
		return err
	}
	var e MetaEnum
	if err := httpx.DecodeJSON(r, &e); err != nil {
		return err
	}
	if err := s.repo.EnumUpdate(r.Context(), code, e.Label); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handleEnumDelete(w http.ResponseWriter, r *http.Request) error {
	code := r.PathValue("code")
	if err := s.editableEnum(r, code); err != nil {
		return err
	}
	if err := s.repo.EnumDelete(r.Context(), code); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handleEnumItems(w http.ResponseWriter, r *http.Request) error {
	items, err := s.repo.EnumItems(r.Context(), r.PathValue("code"))
	if err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (s *Service) handleEnumItemCreate(w http.ResponseWriter, r *http.Request) error {
	code := r.PathValue("code")
	if err := s.editableEnum(r, code); err != nil {
		return err
	}
	var it MetaEnumItem
	if err := httpx.DecodeJSON(r, &it); err != nil {
		return err
	}
	it.EnumCode = code
	if strings.TrimSpace(it.Code) == "" {
		return httpx.BadRequest("枚举项 code 不能为空")
	}
	if err := s.repo.EnumItemCreate(r.Context(), &it); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handleEnumItemUpdate(w http.ResponseWriter, r *http.Request) error {
	code := r.PathValue("code")
	if err := s.editableEnum(r, code); err != nil {
		return err
	}
	var it MetaEnumItem
	if err := httpx.DecodeJSON(r, &it); err != nil {
		return err
	}
	if strings.TrimSpace(it.Code) == "" {
		it.Code = r.PathValue("itemCode")
	}
	if err := s.repo.EnumItemUpdate(r.Context(), code, r.PathValue("itemCode"), &it); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (s *Service) handleEnumItemDelete(w http.ResponseWriter, r *http.Request) error {
	code := r.PathValue("code")
	if err := s.editableEnum(r, code); err != nil {
		return err
	}
	if err := s.repo.EnumItemDelete(r.Context(), code, r.PathValue("itemCode")); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
