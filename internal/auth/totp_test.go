package auth

import (
	"testing"
	"time"
)

// RFC 6238 附录 B 的测试向量(SHA1,密钥 "12345678901234567890"),截取 6 位。
func TestTOTPVectors(t *testing.T) {
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	cases := []struct {
		at   int64
		code string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, c := range cases {
		if !VerifyTOTP(secret, c.code, time.Unix(c.at, 0)) {
			t.Errorf("code %s at %d should verify", c.code, c.at)
		}
		if VerifyTOTP(secret, "000000", time.Unix(c.at, 0)) && c.code != "000000" {
			t.Errorf("wrong code accepted at %d", c.at)
		}
	}
	// 时钟偏差 ±30 秒内接受,超出拒绝
	if !VerifyTOTP(secret, "287082", time.Unix(89, 0)) {
		t.Error("code within +1 step should verify")
	}
	if VerifyTOTP(secret, "287082", time.Unix(150, 0)) {
		t.Error("code 3 steps later must be rejected")
	}
}

func TestTOTPSecretAndURI(t *testing.T) {
	s, err := NewTOTPSecret()
	if err != nil || len(s) != 32 {
		t.Fatalf("unexpected secret %q err %v", s, err)
	}
	uri := TOTPURI("GMS", "admin", s)
	if uri[:15] != "otpauth://totp/" {
		t.Fatalf("bad uri %s", uri)
	}
}
