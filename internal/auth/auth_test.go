package auth

import (
	"encoding/json"
	"testing"
)

func TestTokenRoundTrip(t *testing.T) {
	for _, uid := range []int64{1, 42, 100000} {
		tok := GenerateToken(uid)
		got, err := DecodeUID(tok)
		if err != nil || got != uid {
			t.Fatalf("DecodeUID(%q) = %d, %v; want %d", tok, got, err, uid)
		}
	}
	// 与 Java TokenFactory 的编码一致:uid=1 → base64 "MQ==" → hex "4d513d3d"
	if tok := GenerateToken(1); tok[:9] != "4d513d3d-" {
		t.Fatalf("token 前缀 = %q, want 4d513d3d-", tok[:9])
	}
	if _, err := DecodeUID("garbage"); err == nil {
		t.Fatal("非法 token 应报错")
	}
}

func TestPassword(t *testing.T) {
	salt := NewSalt()
	if len(salt) != 16 {
		t.Fatalf("盐长度 = %d", len(salt))
	}
	hash := HashPassword("secret", salt)
	if len(hash) != 40 {
		t.Fatalf("sha1 hex 长度 = %d", len(hash))
	}
	if !VerifyPassword("secret", salt, hash) || VerifyPassword("wrong", salt, hash) {
		t.Fatal("密码校验结果错误")
	}
	// 固定向量:sha1("abc" + "salt")
	if got := HashPassword("abc", "salt"); got != "99198dfc48e034c66356183f854eb322f607c1dd" {
		t.Fatalf("HashPassword 固定向量不符: %s", got)
	}
}

func TestPermissionTree(t *testing.T) {
	admin, _ := ParseTree(`{"code":"", "wildcard":true}`)
	if !admin.Check("service/gms-ranch/sync") || !admin.Check("anything") {
		t.Fatal("根通配应放行一切")
	}
	all, _ := ParseTree(`{"code":"","wildcard":false,"children":{"function":{"code":"function","wildcard":true,"children":{}},"service":{"code":"service","wildcard":true,"children":{}}}}`)
	if !all.Check("service/task-runner/TaskController/list") || !all.Check("function/BasicPermission/MANAGE_USER") {
		t.Fatal("子树通配应放行")
	}
	if all.Check("game/ranch/prod") {
		t.Fatal("未授权命名空间应拒绝")
	}
	leaf, _ := ParseTree(`{"code":"","children":{"service":{"code":"service","children":{"gms-ranch":{"code":"gms-ranch","children":{"sync":{"code":"sync"}}}}}}}`)
	if !leaf.Check("service/gms-ranch/sync") || leaf.Check("service/gms-ranch/approval") {
		t.Fatal("精确路径匹配错误")
	}
	merged := MergeTrees(leaf, all)
	if !merged.Check("function/x/y") || !merged.Check("service/gms-ranch/approval") {
		t.Fatal("合并后应取并集")
	}
}

func TestRegistryTree(t *testing.T) {
	r := NewRegistry()
	r.Register("service/gms-ranch/sync", "同步", "牧场管理")
	r.Register("service/gms-ranch/approval", "审核")
	tree := r.Tree()
	b, _ := json.Marshal(tree)
	s := string(b)
	for _, want := range []string{`"code":"service"`, `"name":"服务"`, `"name":"牧场管理"`, `"name":"同步"`, `"name":"审核"`} {
		if !contains(s, want) {
			t.Fatalf("定义树缺少 %s: %s", want, s)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
