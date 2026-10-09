// Package gms 实现牧场运营管理:邮件/群邮件/签到/公会战配置的审核与同步、
// 游戏服配置代理、区域信息与道具模板。
package gms

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GameServer 游戏服 login 进程 /admin 接口客户端。
//
// 签名算法与原 Java GameServerSigner 逐字节一致,游戏服侧见
// cow-game-server/services/login/internal/login/handlers.go VerifyGmsSignature:
//
//	md5hex( path + Σ(按 key 排序的 query: key + values 直接拼接) + ts(秒) + random + apiKey )
//
// header: x-api-ts / x-api-random(32 位无连字符 uuid)/ x-api-signature。
type GameServer struct {
	base   string
	apiKey string
	client *http.Client
}

// NewGameServer 创建客户端。
func NewGameServer(base, apiKey string) *GameServer {
	return &GameServer{
		base:   strings.TrimRight(base, "/"),
		apiKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Signature 计算签名。
func Signature(apiKey, ts, random, path string, query url.Values) string {
	var sb strings.Builder
	sb.WriteString(path)
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString(strings.Join(query[k], ""))
	}
	sb.WriteString(ts)
	sb.WriteString(random)
	sb.WriteString(apiKey)
	sum := md5.Sum([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

func randomHex32() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Sign 给请求加签名头。
func (g *GameServer) Sign(h http.Header, path string, query url.Values) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	random := randomHex32()
	h.Set("x-api-ts", ts)
	h.Set("x-api-random", random)
	h.Set("x-api-signature", Signature(g.apiKey, ts, random, path, query))
}

// Do 发送已签名请求并返回状态码与响应体。
func (g *GameServer) Do(ctx context.Context, method, path string, query url.Values, body []byte, contentType string) (int, []byte, string, error) {
	if g.base == "" {
		return 0, nil, "", fmt.Errorf("未配置游戏服地址 ranch.gameServerAddress")
	}
	u := g.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return 0, nil, "", err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	g.Sign(req.Header, path, query)
	resp, err := g.client.Do(req)
	if err != nil {
		return 0, nil, "", fmt.Errorf("请求游戏服失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, nil, "", err
	}
	return resp.StatusCode, data, resp.Header.Get("Content-Type"), nil
}

// Sync POST /admin/config/sync?mode={mode},body 为 JSON 数组。
func (g *GameServer) Sync(ctx context.Context, mode string, body []byte) (string, error) {
	status, data, _, err := g.Do(ctx, http.MethodPost, "/admin/config/sync", url.Values{"mode": {mode}}, body, "application/json")
	if err != nil {
		return "", err
	}
	if status/100 != 2 {
		return "", fmt.Errorf("游戏服返回 %d: %s", status, strings.TrimSpace(string(data)))
	}
	return string(data), nil
}

// Proxy 把请求原样转发到游戏服(去掉 /proxy 前缀),重新签名,透传响应。
func (g *GameServer) Proxy(w http.ResponseWriter, r *http.Request, targetPath string) error {
	var body []byte
	if r.Body != nil && r.Method != http.MethodGet {
		b, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			return err
		}
		body = b
	}
	ct := r.Header.Get("Content-Type")
	if body != nil && ct == "" {
		ct = "application/json"
	}
	status, data, respCT, err := g.Do(r.Context(), r.Method, targetPath, r.URL.Query(), body, ct)
	if err != nil {
		return err
	}
	if respCT == "" {
		respCT = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", respCT)
	w.WriteHeader(status)
	_, _ = w.Write(data)
	return nil
}
