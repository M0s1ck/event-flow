// Package auth содержит хэширование паролей и генерацию токенов сессий (D-01).
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	pbkdf2Iterations = 600_000
	saltLen          = 16
	keyLen           = 32
	tokenLen         = 32
)

var b64 = base64.RawStdEncoding

// HashPassword возвращает строку вида pbkdf2_sha256$<iter>$<salt>$<hash>.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, keyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", pbkdf2Iterations, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// CheckPassword сравнивает пароль с хэшем за постоянное время.
func CheckPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err := b64.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := b64.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash используется для несуществующих логинов, чтобы время ответа не выдавало их отсутствие.
var dummyHash, _ = HashPassword("dummy-password-for-timing")

// BurnTime выполняет такую же работу, как CheckPassword для настоящего пользователя.
func BurnTime(password string) {
	CheckPassword(password, dummyHash)
}

// NewToken генерирует случайный токен сессии и его SHA-256 для хранения в БД.
func NewToken() (token string, hash []byte, err error) {
	buf := make([]byte, tokenLen)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, HashToken(token), nil
}

// HashToken — то, что хранится в таблице sessions вместо самого токена.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

var ErrNoToken = errors.New("no bearer token")

// BearerToken достаёт токен из заголовка Authorization.
func BearerToken(header string) (string, error) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", ErrNoToken
	}
	return strings.TrimSpace(header[len(prefix):]), nil
}
