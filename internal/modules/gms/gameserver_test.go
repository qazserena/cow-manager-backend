package gms

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"testing"
)

// 与游戏服 login_test.go 的向量一致:
// md5('/admin/config/sync' + 'mode' + 'group-mail' + ts + random + apiKey)
func TestSignatureMatchesGameServer(t *testing.T) {
	apiKey := "e45542c65a1f11199ad3303bd8c8fe356a2aa0a1"
	sum := md5.Sum([]byte("/admin/config/sync" + "mode" + "group-mail" + "1700000000" + "rand123" + apiKey))
	want := hex.EncodeToString(sum[:])
	got := Signature(apiKey, "1700000000", "rand123", "/admin/config/sync", url.Values{"mode": {"group-mail"}})
	if got != want {
		t.Fatalf("Signature = %s, want %s", got, want)
	}
}

func TestSignatureSortsQueryKeys(t *testing.T) {
	q := url.Values{"b": {"2"}, "a": {"1", "x"}}
	sum := md5.Sum([]byte("/p" + "a" + "1x" + "b" + "2" + "1" + "r" + "k"))
	if got := Signature("k", "1", "r", "/p", q); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("query 应按 key 排序且 value 直接拼接: %s", got)
	}
}
