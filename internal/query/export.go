package query

import (
	"net/http"
	"net/url"
	"time"

	"github.com/jmoiron/sqlx"

	"cow-manager-backend-go/internal/httpx"
)

// Timezone 解析 timezone 参数,失败则用默认时区。
func Timezone(q url.Values, def *time.Location) *time.Location {
	if tz := q.Get("timezone"); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return def
}

// ContentDisposition 设置附件下载头(RFC 5987,文件名可含中文)。
func ContentDisposition(w http.ResponseWriter, filename string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filename))
}

// Handlers 把一个 Spec 变成三个常用处理函数:分页列表、CSV 导出、按主键取一行。
type Handlers struct {
	Spec    *Spec
	DB      *sqlx.DB
	Loc     *time.Location
	Labeler EnumLabeler
}

// List 分页列表:GET ?page&size&sort&<过滤参数>。
func (h Handlers) List(w http.ResponseWriter, r *http.Request) error {
	page, size := httpx.Pagination(r)
	p, err := h.Spec.Page(r.Context(), h.DB, r.URL.Query(), page, size)
	if err != nil {
		return err
	}
	httpx.OK(w, p)
	return nil
}

// Export CSV 导出:GET ?<过滤参数>&timezone=。
func (h Handlers) Export(w http.ResponseWriter, r *http.Request) error {
	loc := Timezone(r.URL.Query(), h.Loc)
	ContentDisposition(w, h.Spec.Table+"-"+time.Now().In(loc).Format("20060102-150405")+".csv")
	return h.Spec.ExportCSV(r.Context(), h.DB, r.URL.Query(), w, loc, h.Labeler)
}

// One 按路径参数 {id} 对应的列取一行。
func (h Handlers) One(col string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		row, err := h.Spec.One(r.Context(), h.DB, col, r.PathValue("id"))
		if err != nil {
			return err
		}
		if row == nil {
			return httpx.NotFound("记录不存在")
		}
		httpx.OK(w, row)
		return nil
	}
}
