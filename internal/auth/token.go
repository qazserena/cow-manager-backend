package auth

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

// GenerateToken 生成 hex(base64(uid)) + "-" + 32 位随机字母的令牌,
// 与 Java TokenFactory.generate 同构,可直接从令牌反解 uid。
func GenerateToken(uid int64) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(uid, 10)))
	return hex.EncodeToString([]byte(encoded)) + "-" + randomLetters(32)
}

// DecodeUID 从令牌反解 uid;格式非法返回错误。
func DecodeUID(token string) (int64, error) {
	head, _, ok := strings.Cut(token, "-")
	if !ok || head == "" {
		return 0, errors.New("bad token")
	}
	raw, err := hex.DecodeString(head)
	if err != nil {
		return 0, errors.New("bad token")
	}
	b64, err := base64.StdEncoding.DecodeString(string(raw))
	if err != nil {
		return 0, errors.New("bad token")
	}
	uid, err := strconv.ParseInt(string(b64), 10, 64)
	if err != nil || uid <= 0 {
		return 0, errors.New("bad token")
	}
	return uid, nil
}
