package portalsocial

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 用 httptest 顶替 api.x.com,覆盖粉丝接口 + 余额接口的组合解析,以及余额接口单独失败的降级。
func TestFetchXWithCredits(t *testing.T) {
	const token = "test-bearer"
	creditsStatus := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/2/users/by/username/cowgalaxy2026":
			_, _ = w.Write([]byte(`{"data":{"id":"1","name":"CowGalaxy","username":"cowgalaxy2026","created_at":"2026-01-01T00:00:00Z","verified":false,"public_metrics":{"followers_count":69,"following_count":53,"tweet_count":92,"listed_count":0}}}`))
		case "/2/usage/credits":
			w.WriteHeader(creditsStatus)
			if creditsStatus == http.StatusOK {
				_, _ = w.Write([]byte(`{"data":{"free_balance":19.95,"free_grants":[{"amount":19.95,"expires_at":"2027-01-08T07:54:50.000Z"}],"prepaid_balance":5.0,"total_balance":24.95}}`))
			} else {
				_, _ = w.Write([]byte(`{"title":"Forbidden","detail":"no usage scope"}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	old := xAPIBase
	xAPIBase = srv.URL
	defer func() { xAPIBase = old }()

	var c socialConf
	c.X.BearerToken = token
	c.X.Target = "cowgalaxy2026"
	s := NewService(nil, c, time.UTC, nil)

	live := s.fetchLive(context.Background(), "x")
	if !live.OK {
		t.Fatalf("fetch x failed: %s", live.Error)
	}
	if live.Members != 69 || live.Posts != 92 {
		t.Fatalf("unexpected metrics: %+v", live)
	}
	if live.Credits == nil || live.Credits.Error != "" {
		t.Fatalf("credits should be present: %+v", live.Credits)
	}
	if live.Credits.Total != 24.95 || live.Credits.Prepaid != 5 || live.Credits.Free != 19.95 {
		t.Fatalf("unexpected credits: %+v", live.Credits)
	}
	if live.Credits.FreeExpiresAt != "2027-01-08T07:54:50.000Z" {
		t.Fatalf("unexpected free expiry: %q", live.Credits.FreeExpiresAt)
	}

	// 余额接口失败不应拖垮主指标
	creditsStatus = http.StatusForbidden
	live = s.fetchLive(context.Background(), "x")
	if !live.OK {
		t.Fatalf("fetch x should still succeed when credits fail: %s", live.Error)
	}
	if live.Credits == nil || live.Credits.Error == "" {
		t.Fatalf("credits error should be recorded: %+v", live.Credits)
	}
	if live.Credits.Total != 0 {
		t.Fatalf("credits total should stay zero on failure: %+v", live.Credits)
	}
}

// 其它平台不应带 credits 字段。
func TestCreditsOnlyForX(t *testing.T) {
	s := NewService(nil, socialConf{}, time.UTC, nil)
	for _, p := range []string{"telegram", "discord"} {
		if live := s.fetchLive(context.Background(), p); live.Credits != nil {
			t.Fatalf("%s should not have credits", p)
		}
	}
}
