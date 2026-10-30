// Package auth generates and verifies one-time login codes and session tokens.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/mail"
	"strings"
	"time"
)

// Tunables for the login flow.
const (
	CodeLength      = 8
	CodeTTL         = 10 * time.Minute
	CodeMaxAttempts = 5
	SessionTTL      = 30 * 24 * time.Hour
	CookieName      = "makers_session"
	// LoginCookieName holds the nonce binding a login code to the browser
	// that requested it.
	LoginCookieName = "makers_login"
	LoginCookieTTL  = CodeTTL + 5*time.Minute
)

var codeSpace = big.NewInt(100_000_000) // 10^CodeLength

// NewCode returns a random CodeLength-digit code.
func NewCode() string {
	n, err := rand.Int(rand.Reader, codeSpace)
	if err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return fmt.Sprintf("%0*d", CodeLength, n.Int64())
}

// FormatCode groups a code for readability, e.g. "1234 5678".
func FormatCode(code string) string {
	if len(code) != CodeLength {
		return code
	}
	return code[:4] + " " + code[4:]
}

// HashCode binds a code to an email with a server secret, so a leaked
// database does not allow brute-forcing the short codes offline.
func HashCode(secret []byte, email, code string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(email))
	m.Write([]byte{0})
	m.Write([]byte(code))
	return hex.EncodeToString(m.Sum(nil))
}

// CheckCode compares a code against a stored hash in constant time.
func CheckCode(secret []byte, email, code, hash string) bool {
	return hmac.Equal([]byte(HashCode(secret, email, code)), []byte(hash))
}

// NewToken returns a random session token for the cookie.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the value stored in the database for a session token
// or a login nonce.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NormalizeEmail validates an address and returns its canonical form.
func NormalizeEmail(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > 254 {
		return "", false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || !strings.Contains(s[strings.LastIndexByte(s, '@'):], ".") {
		return "", false
	}
	return s, true
}

// NormalizeCode strips spaces and dashes users may type into the code.
func NormalizeCode(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}
