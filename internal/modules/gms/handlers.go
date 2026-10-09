package gms

import (
	"net/http"
	"strings"

	"cow-manager-backend-go/internal/auth"
	"cow-manager-backend-go/internal/httpx"
	"cow-manager-backend-go/internal/perm"
	"cow-manager-backend-go/internal/query"
)

// Register 挂载路由。
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /config/info", httpx.H(auth.RequireLogin(func(w http.ResponseWriter, _ *http.Request) error {
		httpx.OK(w, s.Region())
		return nil
	})))
	mux.HandleFunc("GET /template/item", httpx.H(auth.RequireLogin(func(w http.ResponseWriter, r *http.Request) error {
		list, err := s.TemplateItems(r.Context())
		if err != nil {
			return err
		}
		httpx.OK(w, list)
		return nil
	})))

	// 游戏服透传:/proxy/admin/config/queryAllConfig 等
	mux.HandleFunc("/proxy/", httpx.H(func(w http.ResponseWriter, r *http.Request) error {
		code := perm.GmsGet
		if r.Method != http.MethodGet {
			code = perm.GmsPost
		}
		return auth.Require(code, func(w http.ResponseWriter, r *http.Request) error {
			target := strings.TrimPrefix(r.URL.Path, "/proxy")
			if !strings.HasPrefix(target, "/admin/") {
				return httpx.Forbidden()
			}
			return s.game.Proxy(w, r, target)
		})(w, r)
	}))

	for module, spec := range moduleSpecs {
		s.registerModule(mux, module, spec)
	}
}

// registerModule 为一个运营模块挂载列表/增删改/审核/激活/同步/导出。
func (s *Service) registerModule(mux *http.ServeMux, module string, spec *query.Spec) {
	table := auditTables[module]
	label := spec.Table
	h := query.Handlers{Spec: spec, DB: s.gms, Loc: s.loc, Labeler: s.labeler}

	mux.HandleFunc("GET /"+module, httpx.H(auth.RequireLogin(h.List)))
	mux.HandleFunc("GET /"+module+"/export", httpx.H(auth.Require(perm.Table(table, "export", label), h.Export)))
	mux.HandleFunc("GET /"+module+"/{id}", httpx.H(auth.RequireLogin(h.One("id"))))
	mux.HandleFunc("POST /"+module, httpx.H(auth.Require(perm.Table(table, "create", label), s.createHandler(module))))
	mux.HandleFunc("PUT /"+module+"/{id}", httpx.H(auth.Require(perm.Table(table, "update", label), s.updateHandler(module))))
	mux.HandleFunc("DELETE /"+module+"/{id}", httpx.H(auth.Require(perm.Table(table, "delete", label), func(w http.ResponseWriter, r *http.Request) error {
		id, err := httpx.PathInt64(r, "id")
		if err != nil {
			return err
		}
		if err := s.Delete(r.Context(), module, id, auth.SessionFrom(r.Context()).UID); err != nil {
			return err
		}
		httpx.NoContent(w)
		return nil
	})))

	mux.HandleFunc("POST /"+module+"/sync", httpx.H(auth.Require(perm.GmsSync, func(w http.ResponseWriter, r *http.Request) error {
		resp, err := s.Sync(r.Context(), module)
		if err != nil {
			return httpx.NewError(httpx.CodeOperationFailed, "同步失败: "+err.Error())
		}
		httpx.Text(w, http.StatusOK, resp)
		return nil
	})))
	audit := func(status int, code string) httpx.HandlerFunc {
		return auth.Require(code, func(w http.ResponseWriter, r *http.Request) error {
			ids, err := httpx.FormInt64s(r, "ids")
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				// 兼容 /{module}/{id}/approval 风格由单独路由处理;这里要求 ids
				return httpx.BadRequest("ids 不能为空")
			}
			if err := s.Audit(r.Context(), module, ids, status, auth.SessionFrom(r.Context()).UID); err != nil {
				return err
			}
			httpx.NoContent(w)
			return nil
		})
	}
	mux.HandleFunc("POST /"+module+"/approval", httpx.H(audit(AuditApproval, perm.GmsApproval)))
	mux.HandleFunc("POST /"+module+"/reject", httpx.H(audit(AuditReject, perm.GmsReject)))
	auditOne := func(status int, code string) httpx.HandlerFunc {
		return auth.Require(code, func(w http.ResponseWriter, r *http.Request) error {
			id, err := httpx.PathInt64(r, "id")
			if err != nil {
				return err
			}
			if err := s.Audit(r.Context(), module, []int64{id}, status, auth.SessionFrom(r.Context()).UID); err != nil {
				return err
			}
			httpx.NoContent(w)
			return nil
		})
	}
	mux.HandleFunc("POST /"+module+"/{id}/approval", httpx.H(auditOne(AuditApproval, perm.GmsApproval)))
	mux.HandleFunc("POST /"+module+"/{id}/reject", httpx.H(auditOne(AuditReject, perm.GmsReject)))
	active := func(on bool, code string) httpx.HandlerFunc {
		return auth.Require(code, func(w http.ResponseWriter, r *http.Request) error {
			id, err := httpx.PathInt64(r, "id")
			if err != nil {
				return err
			}
			if err := s.SetActive(r.Context(), module, id, on); err != nil {
				return err
			}
			httpx.NoContent(w)
			return nil
		})
	}
	mux.HandleFunc("POST /"+module+"/{id}/enable", httpx.H(active(true, perm.GmsEnable)))
	mux.HandleFunc("POST /"+module+"/{id}/disable", httpx.H(active(false, perm.GmsDisable)))
}

func (s *Service) createHandler(module string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		uid := auth.SessionFrom(r.Context()).UID
		var out any
		var err error
		switch module {
		case "mail":
			var m Mail
			if err = httpx.DecodeJSON(r, &m); err == nil {
				out, err = s.CreateMail(r.Context(), &m, uid)
			}
		case "group-mail":
			var m GroupMail
			if err = httpx.DecodeJSON(r, &m); err == nil {
				out, err = s.CreateGroupMail(r.Context(), &m, uid)
			}
		case "check-in":
			var m CheckIn
			if err = httpx.DecodeJSON(r, &m); err == nil {
				out, err = s.CreateCheckIn(r.Context(), &m, uid)
			}
		case "guild-battle":
			var m GuildBattle
			if err = httpx.DecodeJSON(r, &m); err == nil {
				out, err = s.CreateGuildBattle(r.Context(), &m, uid)
			}
		default:
			err = httpx.BadRequest("未知模块")
		}
		if err != nil {
			return err
		}
		httpx.OK(w, out)
		return nil
	}
}

func (s *Service) updateHandler(module string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := httpx.PathInt64(r, "id")
		if err != nil {
			return err
		}
		var out any
		switch module {
		case "mail":
			var m Mail
			if err = httpx.DecodeJSON(r, &m); err == nil {
				out, err = s.UpdateMail(r.Context(), id, &m)
			}
		case "group-mail":
			var m GroupMail
			if err = httpx.DecodeJSON(r, &m); err == nil {
				out, err = s.UpdateGroupMail(r.Context(), id, &m)
			}
		case "check-in":
			var m CheckIn
			if err = httpx.DecodeJSON(r, &m); err == nil {
				out, err = s.UpdateCheckIn(r.Context(), id, &m)
			}
		case "guild-battle":
			var m GuildBattle
			if err = httpx.DecodeJSON(r, &m); err == nil {
				out, err = s.UpdateGuildBattle(r.Context(), id, &m)
			}
		default:
			err = httpx.BadRequest("未知模块")
		}
		if err != nil {
			return err
		}
		httpx.OK(w, out)
		return nil
	}
}
