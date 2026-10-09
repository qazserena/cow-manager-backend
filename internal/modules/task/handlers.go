package task

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"cow-manager-backend-go/internal/auth"
	"cow-manager-backend-go/internal/httpx"
	"cow-manager-backend-go/internal/perm"
)

// Register 挂载路由。路径与响应形态与原 task-runner 保持一致,前端无需改动。
func (s *Scheduler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /task/list", httpx.H(auth.Require(perm.TaskList, func(w http.ResponseWriter, r *http.Request) error {
		list, err := s.List(r.Context())
		if err != nil {
			return err
		}
		httpx.OK(w, list)
		return nil
	})))

	mux.HandleFunc("GET /task/log", httpx.H(auth.Require(perm.TaskLog, func(w http.ResponseWriter, r *http.Request) error {
		page, size := httpx.Pagination(r)
		if r.URL.Query().Get("size") == "" {
			size = 50
		}
		list, err := s.Logs(r.Context(), httpx.FormValue(r, "className"), page, size)
		if err != nil {
			return err
		}
		httpx.OK(w, list)
		return nil
	})))

	mux.HandleFunc("POST /task/enableOrDisableTask", httpx.H(auth.Require(perm.TaskSwitch, func(w http.ResponseWriter, r *http.Request) error {
		enable := strings.EqualFold(httpx.FormValue(r, "enable"), "true") || httpx.FormValue(r, "enable") == "1"
		if err := s.SetEnable(r.Context(), httpx.FormValue(r, "className"), enable); err != nil {
			return err
		}
		httpx.NoContent(w)
		return nil
	})))

	mux.HandleFunc("POST /task/updateCronTrigger", httpx.H(auth.Require(perm.TaskCron, func(w http.ResponseWriter, r *http.Request) error {
		if err := s.SetCron(r.Context(), httpx.FormValue(r, "className"), httpx.FormValue(r, "cronExpress")); err != nil {
			return err
		}
		httpx.OK(w, httpx.ApiResponse{Code: 0, Message: "ok"})
		return nil
	})))

	mux.HandleFunc("POST /task/manualSchedule", httpx.H(auth.Require(perm.TaskManual, func(w http.ResponseWriter, r *http.Request) error {
		begin, err := parseDay(httpx.FormValue(r, "begin"), s.loc)
		if err != nil {
			return err
		}
		end, err := parseDay(httpx.FormValue(r, "end"), s.loc)
		if err != nil {
			return err
		}
		sess := auth.SessionFrom(r.Context())
		if err := s.ManualSchedule(httpx.FormValue(r, "className"), begin, end, strconv.FormatInt(sess.UID, 10)); err != nil {
			return err
		}
		httpx.OK(w, httpx.ApiResponse{Code: 0, Message: "已提交后台补跑"})
		return nil
	})))

	mux.HandleFunc("POST /task/upload", httpx.H(auth.Require(perm.TaskUpload, func(w http.ResponseWriter, r *http.Request) error {
		httpx.OK(w, httpx.ApiResponse{Code: httpx.CodeUnsupported, Message: "任务已内置于服务,无需上传"})
		return nil
	})))

	mux.HandleFunc("GET /task/reload", httpx.H(auth.Require(perm.TaskList, func(w http.ResponseWriter, _ *http.Request) error {
		httpx.OK(w, httpx.ApiResponse{Code: 0, Message: "ok"})
		return nil
	})))

	mux.HandleFunc("POST /configure/reload", httpx.H(auth.Require(perm.ConfigReload, func(w http.ResponseWriter, _ *http.Request) error {
		httpx.NoContent(w)
		return nil
	})))
}

// parseDay 解析 YYYY/MM/DD 或 YYYY-MM-DD。
func parseDay(v string, loc *time.Location) (time.Time, error) {
	v = strings.TrimSpace(v)
	for _, layout := range []string{"2006/01/02", "2006-01-02", "2006/1/2", "2006-1-2"} {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, httpx.BadRequest("日期格式应为 YYYY/MM/DD: " + v)
}
