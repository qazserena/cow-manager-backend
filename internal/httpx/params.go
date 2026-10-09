package httpx

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// QueryInt 读整数 query 参数,缺失或非法返回默认值。
func QueryInt(r *http.Request, name string, def int) int {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// QueryInt64 读 int64 query 参数。
func QueryInt64(r *http.Request, name string, def int64) int64 {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// QueryBool 读布尔 query 参数("true"/"1" 为真)。
func QueryBool(r *http.Request, name string) bool {
	v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get(name)))
	return v == "true" || v == "1"
}

// Pagination 读 page(0 起)与 size,size 默认 10、上限 1000。
func Pagination(r *http.Request) (page, size int) {
	page = QueryInt(r, "page", 0)
	size = QueryInt(r, "size", 10)
	if page < 0 {
		page = 0
	}
	if size <= 0 {
		size = 10
	}
	if size > 1000 {
		size = 1000
	}
	return page, size
}

// DecodeJSON 解析 JSON 请求体。
func DecodeJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return BadRequest("读取请求体失败")
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return BadRequest("请求体为空")
	}
	if err := json.Unmarshal(body, v); err != nil {
		return BadRequest("请求体不是合法 JSON: " + err.Error())
	}
	return nil
}

// FormValue 读 query 或表单字段(POST form 与 query 都支持)。
func FormValue(r *http.Request, name string) string {
	return strings.TrimSpace(r.FormValue(name))
}

// FormValues 读重复 key 的 query/form 字段,如 ids=1&ids=2;
// 同时兼容单值里用逗号分隔的写法。
func FormValues(r *http.Request, name string) []string {
	_ = r.ParseForm()
	var out []string
	for _, v := range r.Form[name] {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// FormInt64s 读重复 key 的整数列表。
func FormInt64s(r *http.Request, name string) ([]int64, error) {
	var out []int64
	for _, v := range FormValues(r, name) {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, BadRequest("参数 " + name + " 非法: " + v)
		}
		out = append(out, n)
	}
	return out, nil
}

// PathInt64 读路径参数并转 int64。
func PathInt64(r *http.Request, name string) (int64, error) {
	v := r.PathValue(name)
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, BadRequest("路径参数 " + name + " 非法: " + v)
	}
	return n, nil
}
