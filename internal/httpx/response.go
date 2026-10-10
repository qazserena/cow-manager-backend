// Package httpx 是 HTTP 层的小工具:统一错误码、JSON 响应、参数解析与中间件。
//
// 错误响应与原 Java 版保持一致:非 2xx + {"code":int,"message":string};
// 101(令牌过期)/100(权限不足)用 403,参数类/业务类错误用 400,其余 500。
// 成功响应为裸 JSON(不包 Result 壳),前端据此直接取数据。
package httpx

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
)

// 错误码,与 Java common ErrorCode / AuthErrorCode 对齐。
const (
	CodeOK               = 0
	CodeBadArgument      = 9
	CodeInternal         = 10
	CodeDuplicate        = 11
	CodeOperationFailed  = 12
	CodeUniqueConflict   = 13
	CodeUnsupported      = 50
	CodePermissionDenied = 100
	CodeTokenExpired     = 101
	CodeApiGuardDenied   = 102
	CodeUserNotExist     = 200
	CodePasswordMismatch = 202
	CodeAccountLocked    = 203
	CodeAccountExpired   = 204
	CodeUsernameExist    = 205
	CodeOtpRequired      = 206 // 已开启二步验证,登录需带验证码
	CodeOtpInvalid       = 207 // 验证码错误
	CodeRoleExist        = 210
)

// Error 业务错误。
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Status 按错误码映射 HTTP 状态。
func (e *Error) Status() int {
	switch e.Code {
	case CodePermissionDenied, CodeTokenExpired, CodeApiGuardDenied:
		return http.StatusForbidden
	case CodeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusBadRequest
	}
}

// NewError 构造业务错误。
func NewError(code int, msg string) *Error { return &Error{Code: code, Message: msg} }

// BadRequest 参数错误。
func BadRequest(msg string) *Error { return NewError(CodeBadArgument, msg) }

// Forbidden 权限不足。
func Forbidden() *Error { return NewError(CodePermissionDenied, "权限不足") }

// TokenExpired 令牌无效或过期。
func TokenExpired() *Error { return NewError(CodeTokenExpired, "令牌已过期") }

// NotFound 记录不存在(按参数错误返回 400,前端只看 message)。
func NotFound(msg string) *Error { return NewError(CodeOperationFailed, msg) }

// JSON 写 JSON 响应。
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("httpx: 写响应失败: %v", err)
	}
}

// OK 200 + JSON。
func OK(w http.ResponseWriter, v any) { JSON(w, http.StatusOK, v) }

// NoContent 200 空体(前端对多数写接口不读响应)。
func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) }

// Text 纯文本响应。
func Text(w http.ResponseWriter, status int, s string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(s))
}

// WriteError 把 error 写成统一错误响应。
func WriteError(w http.ResponseWriter, err error) {
	var e *Error
	if errors.As(err, &e) {
		JSON(w, e.Status(), e)
		return
	}
	log.Printf("httpx: 内部错误: %v", err)
	JSON(w, http.StatusInternalServerError, &Error{Code: CodeInternal, Message: err.Error()})
}

// HandlerFunc 返回 error 的处理函数,由 H 统一写错误。
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// H 把 HandlerFunc 适配成 http.HandlerFunc。
func H(f HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := f(w, r); err != nil {
			WriteError(w, err)
		}
	}
}

// Page 分页响应,字段名沿用 Spring Page 的 content / totalElements。
type Page struct {
	Content       any   `json:"content"`
	TotalElements int64 `json:"totalElements"`
}

// ApiResponse task 服务沿用的包装结构(前端判 code === 0)。
type ApiResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
