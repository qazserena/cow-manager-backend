package portalsocial

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cow-manager-backend/internal/config"
)

// ChannelLive 官方渠道的实时指标(直连平台 API)。
type ChannelLive struct {
	Platform   string `json:"platform"`
	Configured bool   `json:"configured"` // GMS 是否配置了该平台的凭证
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	FetchedAt  int64  `json:"fetchedAt"`

	Title  string `json:"title"`
	Handle string `json:"handle"`
	URL    string `json:"url"`
	// Members 核心规模指标:X 粉丝数 / Telegram 群成员数 / Discord 成员数
	Members int64 `json:"members"`
	// Secondary 次要指标:X 关注数 / Telegram 管理员数 / Discord 在线数
	Secondary int64 `json:"secondary"`
	// Posts X 推文数(其它平台为 0)
	Posts int64 `json:"posts"`
	// Listed X 被列入列表数
	Listed int64             `json:"listed"`
	Extra  map[string]any    `json:"extra,omitempty"`
	Labels map[string]string `json:"labels"` // members / secondary / posts 的显示名
	// Credits X 开发者平台(pay-per-use)余额;仅 X 有,其它平台为 nil
	Credits *XCredits `json:"credits,omitempty"`
}

// XCredits X 开发者平台余额(USD),来自 GET /2/usage/credits。
// Total = max(0, Prepaid + Free);Prepaid 透支时可为负。余额为 0 后 X API 会拒绝请求,
// 社媒任务的关注校验、官方渠道数据都会随之失败,所以放到后台首页盯着。
type XCredits struct {
	Total   float64 `json:"total"`
	Prepaid float64 `json:"prepaid"`
	Free    float64 `json:"free"`
	// FreeExpiresAt 最早一笔赠送额度的到期时间(RFC3339);赠送额度不会过期或未知时为空
	FreeExpiresAt string `json:"freeExpiresAt,omitempty"`
	// Error 余额接口单独失败时记录原因;不影响粉丝等主指标
	Error string `json:"error,omitempty"`
}

var httpClient = &http.Client{Timeout: 8 * time.Second}

// xAPIBase 便于单测用 httptest 替换。
var xAPIBase = "https://api.x.com"

func getJSON(ctx context.Context, rawURL string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("http %d: %s", resp.StatusCode, msg)
	}
	return json.Unmarshal(body, out)
}

func (s *Service) configured(platform string) bool {
	c := s.social
	switch platform {
	case "x":
		return c.X.BearerToken != "" && c.X.Target != ""
	case "telegram":
		return c.Telegram.BotToken != "" && c.Telegram.ChatID != ""
	case "discord":
		return c.Discord.BotToken != "" && c.Discord.GuildID != ""
	}
	return false
}

// fetchLive 拉某平台的实时指标;凭证缺失返回 Configured=false 的占位。
func (s *Service) fetchLive(ctx context.Context, platform string) *ChannelLive {
	out := &ChannelLive{Platform: platform, Configured: s.configured(platform), FetchedAt: time.Now().Unix(), Labels: liveLabels(platform)}
	if !out.Configured {
		out.Error = "GMS 未配置该平台凭证(config portal.social)"
		return out
	}
	var err error
	switch platform {
	case "x":
		err = s.fetchX(ctx, out)
	case "telegram":
		err = s.fetchTelegram(ctx, out)
	case "discord":
		err = s.fetchDiscord(ctx, out)
	}
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.OK = true
	return out
}

func liveLabels(platform string) map[string]string {
	switch platform {
	case "x":
		return map[string]string{"members": "粉丝", "secondary": "关注中", "posts": "推文", "listed": "被列入列表"}
	case "telegram":
		return map[string]string{"members": "群成员", "secondary": "管理员"}
	case "discord":
		return map[string]string{"members": "成员", "secondary": "在线"}
	}
	return map[string]string{}
}

// ---------- X ----------

func (s *Service) fetchX(ctx context.Context, out *ChannelLive) error {
	c := s.social.X
	var resp struct {
		Data struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Username      string `json:"username"`
			CreatedAt     string `json:"created_at"`
			Verified      bool   `json:"verified"`
			PublicMetrics struct {
				Followers int64 `json:"followers_count"`
				Following int64 `json:"following_count"`
				Tweets    int64 `json:"tweet_count"`
				Listed    int64 `json:"listed_count"`
			} `json:"public_metrics"`
		} `json:"data"`
		Errors []struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	u := xAPIBase + "/2/users/by/username/" + url.PathEscape(c.Target) + "?user.fields=public_metrics,created_at,verified"
	if err := getJSON(ctx, u, map[string]string{"Authorization": "Bearer " + c.BearerToken}, &resp); err != nil {
		return err
	}
	if len(resp.Errors) > 0 && resp.Data.ID == "" {
		return fmt.Errorf("%s: %s", resp.Errors[0].Title, resp.Errors[0].Detail)
	}
	out.Title = resp.Data.Name
	out.Handle = "@" + resp.Data.Username
	out.URL = "https://x.com/" + resp.Data.Username
	out.Members = resp.Data.PublicMetrics.Followers
	out.Secondary = resp.Data.PublicMetrics.Following
	out.Posts = resp.Data.PublicMetrics.Tweets
	out.Listed = resp.Data.PublicMetrics.Listed
	out.Extra = map[string]any{"createdAt": resp.Data.CreatedAt, "verified": resp.Data.Verified, "id": resp.Data.ID}
	// 余额单独拉,失败只记在 Credits.Error 里,不让主指标跟着挂
	out.Credits = s.fetchXCredits(ctx)
	return nil
}

// fetchXCredits GET /2/usage/credits,用同一个 Bearer Token。
func (s *Service) fetchXCredits(ctx context.Context) *XCredits {
	c := s.social.X
	var resp struct {
		Data struct {
			TotalBalance   float64 `json:"total_balance"`
			PrepaidBalance float64 `json:"prepaid_balance"`
			FreeBalance    float64 `json:"free_balance"`
			FreeGrants     []struct {
				Amount    float64 `json:"amount"`
				ExpiresAt string  `json:"expires_at"`
			} `json:"free_grants"`
		} `json:"data"`
		Errors []struct {
			Title  string `json:"title"`
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	cr := &XCredits{}
	u := xAPIBase + "/2/usage/credits"
	if err := getJSON(ctx, u, map[string]string{"Authorization": "Bearer " + c.BearerToken}, &resp); err != nil {
		cr.Error = err.Error()
		return cr
	}
	if len(resp.Errors) > 0 {
		cr.Error = fmt.Sprintf("%s: %s", resp.Errors[0].Title, resp.Errors[0].Detail)
		return cr
	}
	cr.Total = resp.Data.TotalBalance
	cr.Prepaid = resp.Data.PrepaidBalance
	cr.Free = resp.Data.FreeBalance
	// X 返回的 free_grants 按到期时间升序,取第一笔有到期时间的
	for _, g := range resp.Data.FreeGrants {
		if g.ExpiresAt != "" {
			cr.FreeExpiresAt = g.ExpiresAt
			break
		}
	}
	return cr
}

// ---------- Telegram ----------

func (s *Service) tgCall(ctx context.Context, method string, out any) error {
	c := s.social.Telegram
	u := "https://api.telegram.org/bot" + c.BotToken + "/" + method + "?chat_id=" + url.QueryEscape(c.ChatID)
	var env struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := getJSON(ctx, u, nil, &env); err != nil {
		return err
	}
	if !env.OK {
		return fmt.Errorf("telegram %s: %s", method, env.Description)
	}
	return json.Unmarshal(env.Result, out)
}

func (s *Service) fetchTelegram(ctx context.Context, out *ChannelLive) error {
	var chat struct {
		ID          int64  `json:"id"`
		Title       string `json:"title"`
		Username    string `json:"username"`
		Type        string `json:"type"`
		Description string `json:"description"`
		InviteLink  string `json:"invite_link"`
		IsForum     bool   `json:"is_forum"`
	}
	if err := s.tgCall(ctx, "getChat", &chat); err != nil {
		return err
	}
	var count int64
	if err := s.tgCall(ctx, "getChatMemberCount", &count); err != nil {
		return err
	}
	var admins []json.RawMessage
	_ = s.tgCall(ctx, "getChatAdministrators", &admins)

	out.Title = chat.Title
	if chat.Username != "" {
		out.Handle = "@" + chat.Username
		out.URL = "https://t.me/" + chat.Username
	} else {
		out.URL = chat.InviteLink
	}
	out.Members = count
	out.Secondary = int64(len(admins))
	out.Extra = map[string]any{"type": chat.Type, "isForum": chat.IsForum, "description": chat.Description, "id": chat.ID}
	return nil
}

// ---------- Discord ----------

func (s *Service) fetchDiscord(ctx context.Context, out *ChannelLive) error {
	c := s.social.Discord
	var g struct {
		ID                       string `json:"id"`
		Name                     string `json:"name"`
		Description              string `json:"description"`
		ApproximateMemberCount   int64  `json:"approximate_member_count"`
		ApproximatePresenceCount int64  `json:"approximate_presence_count"`
		PremiumSubscriptionCount int64  `json:"premium_subscription_count"`
		VanityURLCode            string `json:"vanity_url_code"`
	}
	u := "https://discord.com/api/v10/guilds/" + url.PathEscape(c.GuildID) + "?with_counts=true"
	if err := getJSON(ctx, u, map[string]string{"Authorization": "Bot " + c.BotToken}, &g); err != nil {
		return err
	}
	out.Title = g.Name
	if g.VanityURLCode != "" {
		out.URL = "https://discord.gg/" + g.VanityURLCode
	}
	out.Members = g.ApproximateMemberCount
	out.Secondary = g.ApproximatePresenceCount
	out.Extra = map[string]any{"boosts": g.PremiumSubscriptionCount, "description": g.Description, "id": g.ID}
	return nil
}

// socialConf 便于测试替换。
type socialConf = config.PortalSocial
