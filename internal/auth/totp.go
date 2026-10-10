package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TOTP(RFC 6238):HMAC-SHA1、6 位、30 秒步长,与 Google Authenticator / Microsoft Authenticator / 1Password 等兼容。

const (
	totpDigits = 6
	totpPeriod = 30
	// totpSkew 校验时允许前后各一个时间片(±30 秒),吸收手机与服务器的时钟偏差
	totpSkew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret 生成 20 字节随机密钥,base32 编码(认证器 App 的通用格式)。
func NewTOTPSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return b32.EncodeToString(buf), nil
}

// TOTPURI 生成认证器扫码用的 otpauth 链接。
func TOTPURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", strconv.Itoa(totpDigits))
	q.Set("period", strconv.Itoa(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

func totpCode(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset])&0x7f)<<24 | uint32(sum[offset+1])<<16 | uint32(sum[offset+2])<<8 | uint32(sum[offset+3])
	code %= 1000000
	return fmt.Sprintf("%06d", code)
}

// GenerateTOTP 生成 now 时刻的验证码(测试 / 排障用;线上校验走 VerifyTOTP)。
func GenerateTOTP(secret string, now time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	return totpCode(key, uint64(now.Unix()/totpPeriod)), nil
}

// VerifyTOTP 校验 6 位验证码;now 允许注入便于测试。
func VerifyTOTP(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(key) == 0 {
		return false
	}
	counter := uint64(now.Unix() / totpPeriod)
	for d := -totpSkew; d <= totpSkew; d++ {
		c := counter + uint64(d)
		if d < 0 {
			c = counter - uint64(-d)
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(key, c)), []byte(code)) == 1 {
			return true
		}
	}
	return false
}
