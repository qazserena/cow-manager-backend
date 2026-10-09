// Package auth 提供密码哈希、令牌、权限树与会话鉴权中间件。
//
// 密码与令牌格式与原 Java auth-center 逐字节兼容,库里的既有账号与 token 无需迁移。
package auth

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/hex"
	"math/big"
)

const saltLength = 16

var saltAlphabet = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randomLetters(n int) string {
	out := make([]rune, n)
	max := big.NewInt(int64(len(saltAlphabet)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		out[i] = saltAlphabet[idx.Int64()]
	}
	return string(out)
}

// NewSalt 生成 16 位随机英文字母盐(与 RandomStringUtils.randomAlphabetic(16) 一致)。
func NewSalt() string { return randomLetters(saltLength) }

// HashPassword = sha1Hex(password + salt),小写十六进制。
func HashPassword(password, salt string) string {
	sum := sha1.Sum([]byte(password + salt))
	return hex.EncodeToString(sum[:])
}

// VerifyPassword 常量时间比较。
func VerifyPassword(password, salt, hash string) bool {
	computed := HashPassword(password, salt)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(hash)) == 1
}
