package auth

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"cow-manager-backend/internal/httpx"
)

// TokenHeader 前端携带令牌的请求头。
const TokenHeader = "x-api-token"

// Session 已登录用户的会话。
type Session struct {
	UID             int64    `json:"uid"`
	Username        string   `json:"username"`
	Roles           []string `json:"roles"`
	Token           string   `json:"token"`
	TokenExpireTick int64    `json:"tokenExpireTick"`
	Tree            *Tree    `json:"permissionTree"`
}

// Has 是否拥有权限码。
func (s *Session) Has(code string) bool { return s != nil && s.Tree.Check(code) }

// IsSuperAdmin 根通配。
func (s *Session) IsSuperAdmin() bool { return s != nil && s.Tree.IsSuperAdmin() }

// Expired 令牌是否过期(毫秒时间戳)。
func (s *Session) Expired(now time.Time) bool {
	return s.TokenExpireTick > 0 && s.TokenExpireTick <= now.UnixMilli()
}

// Loader 按令牌从存储加载会话;令牌无效时返回 (nil, nil)。
type Loader interface {
	LoadSession(ctx context.Context, token string) (*Session, error)
}

// Manager 会话缓存。单进程部署,角色/用户变更时直接失效缓存即可即时生效。
type Manager struct {
	loader  Loader
	mu      sync.RWMutex
	byToken map[string]*Session
}

// NewManager 创建会话管理器。
func NewManager(loader Loader) *Manager {
	return &Manager{loader: loader, byToken: map[string]*Session{}}
}

// Resolve 解析令牌为会话;无效返回 (nil, nil)。
func (m *Manager) Resolve(ctx context.Context, token string) (*Session, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil
	}
	now := time.Now()
	m.mu.RLock()
	s, ok := m.byToken[token]
	m.mu.RUnlock()
	if ok {
		if s.Expired(now) {
			m.InvalidateToken(token)
			return nil, nil
		}
		return s, nil
	}
	s, err := m.loader.LoadSession(ctx, token)
	if err != nil || s == nil {
		return nil, err
	}
	m.mu.Lock()
	m.byToken[token] = s
	if len(m.byToken) > 2000 {
		for k, v := range m.byToken {
			if v.Expired(now) {
				delete(m.byToken, k)
			}
		}
	}
	m.mu.Unlock()
	return s, nil
}

// InvalidateToken 移除某令牌。
func (m *Manager) InvalidateToken(token string) {
	m.mu.Lock()
	delete(m.byToken, token)
	m.mu.Unlock()
}

// InvalidateUser 移除某用户的全部缓存会话。
func (m *Manager) InvalidateUser(uid int64) {
	m.mu.Lock()
	for k, v := range m.byToken {
		if v.UID == uid {
			delete(m.byToken, k)
		}
	}
	m.mu.Unlock()
}

// InvalidateRole 移除拥有某角色的用户会话(角色权限树变更后调用)。
func (m *Manager) InvalidateRole(role string) {
	m.mu.Lock()
	for k, v := range m.byToken {
		for _, r := range v.Roles {
			if r == role {
				delete(m.byToken, k)
				break
			}
		}
	}
	m.mu.Unlock()
}

// InvalidateAll 清空缓存。
func (m *Manager) InvalidateAll() {
	m.mu.Lock()
	m.byToken = map[string]*Session{}
	m.mu.Unlock()
}

type ctxKey struct{}

// WithSession 把会话放进 context。
func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// SessionFrom 取当前请求的会话,未登录返回 nil。
func SessionFrom(ctx context.Context) *Session {
	s, _ := ctx.Value(ctxKey{}).(*Session)
	return s
}

// TokenFrom 从请求头(或 query 参数 token,供直接下载链接使用)取令牌。
func TokenFrom(r *http.Request) string {
	if t := r.Header.Get(TokenHeader); t != "" {
		return t
	}
	return r.URL.Query().Get("token")
}

// Authenticate 中间件:令牌有效则把会话挂到 context,无效不报错(由各路由决定是否必须登录)。
func (m *Manager) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := TokenFrom(r)
		if token != "" {
			s, err := m.Resolve(r.Context(), token)
			if err != nil {
				httpx.WriteError(w, err)
				return
			}
			if s != nil {
				r = r.WithContext(WithSession(r.Context(), s))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireScope 全局中间件:已登录的请求必须拥有 code(本部署的区域权限 game/ranch/{region}),否则 403。
// 这样区域权限在后端也是硬约束,而不只是前端藏起区域切换项(dev / prod 共用授权库,token 通用)。
// 未登录的请求放行给各路由自己处理(登录 / 健康检查);exempt 前缀(登出、个人中心等账号级接口)不检查。
func RequireScope(code string, exempt ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s := SessionFrom(r.Context()); s != nil && !s.Has(code) {
				skip := false
				for _, prefix := range exempt {
					if strings.HasPrefix(r.URL.Path, prefix) {
						skip = true
						break
					}
				}
				if !skip {
					httpx.WriteError(w, httpx.NewError(httpx.CodePermissionDenied, "该账号没有本区域的访问权限"))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireLogin 要求已登录。
func RequireLogin(h httpx.HandlerFunc) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if SessionFrom(r.Context()) == nil {
			return httpx.TokenExpired()
		}
		return h(w, r)
	}
}

// Require 要求已登录且拥有权限码。
func Require(code string, h httpx.HandlerFunc) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		s := SessionFrom(r.Context())
		if s == nil {
			return httpx.TokenExpired()
		}
		if !s.Has(code) {
			return httpx.Forbidden()
		}
		return h(w, r)
	}
}
