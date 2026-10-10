//go:build integration

package portalsocial

import (
	"context"
	"os"
	"testing"
	"time"
)

// 真网络调用,默认不跑:
//
//	GMS_PORTAL_TG_BOT_TOKEN=... GMS_PORTAL_TG_CHAT_ID=@cowgalaxy go test -tags=integration ./internal/modules/portalsocial/ -run TestLive -v
func TestLiveTelegram(t *testing.T) {
	var c socialConf
	c.Telegram.BotToken = os.Getenv("GMS_PORTAL_TG_BOT_TOKEN")
	c.Telegram.ChatID = os.Getenv("GMS_PORTAL_TG_CHAT_ID")
	if c.Telegram.BotToken == "" || c.Telegram.ChatID == "" {
		t.Skip("telegram credentials not set")
	}
	s := NewService(nil, c, time.UTC, nil)
	live := s.fetchLive(context.Background(), "telegram")
	if !live.OK {
		t.Fatalf("telegram fetch failed: %s", live.Error)
	}
	if live.Members <= 0 || live.Title == "" {
		t.Fatalf("unexpected telegram result: %+v", live)
	}
	t.Logf("telegram: %s %s members=%d admins=%d", live.Title, live.Handle, live.Members, live.Secondary)
}

func TestLiveX(t *testing.T) {
	var c socialConf
	c.X.BearerToken = os.Getenv("GMS_PORTAL_X_BEARER_TOKEN")
	c.X.Target = "cowgalaxy2026"
	if c.X.BearerToken == "" {
		t.Skip("x bearer token not set")
	}
	s := NewService(nil, c, time.UTC, nil)
	live := s.fetchLive(context.Background(), "x")
	if !live.OK {
		t.Fatalf("x fetch failed: %s", live.Error)
	}
	t.Logf("x: %s %s followers=%d following=%d tweets=%d", live.Title, live.Handle, live.Members, live.Secondary, live.Posts)
}

func TestNotConfigured(t *testing.T) {
	s := NewService(nil, socialConf{}, time.UTC, nil)
	for _, p := range Platforms {
		live := s.fetchLive(context.Background(), p)
		if live.Configured || live.OK {
			t.Fatalf("%s should be reported as not configured", p)
		}
	}
}
