package dashboard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ---------- password hashing (PBKDF2-HMAC-SHA256, stdlib-only) ----------

const pbkdf2Iterations = 10000

func pbkdf2Sha256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	numBlocks := (keyLen + prf.Size() - 1) / prf.Size()
	var out []byte
	buf := make([]byte, 4)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(buf, uint32(block))
		prf.Write(buf)
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

// HashPassword returns "pbkdf2$sha256$<iter>$<salt-hex>$<hash-hex>".
func HashPassword(password string) string {
	salt := randBytes(16)
	h := pbkdf2Sha256([]byte(password), salt, pbkdf2Iterations, 32)
	return fmt.Sprintf("pbkdf2$sha256$%d$%s$%s", pbkdf2Iterations, hex.EncodeToString(salt), hex.EncodeToString(h))
}

func VerifyPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 5 || parts[0] != "pbkdf2" {
		return false
	}
	iter, err := strconv.Atoi(parts[2])
	if err != nil || iter < 1 || iter > 10_000_000 {
		return false
	}
	salt, err := hex.DecodeString(parts[3])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[4])
	if err != nil {
		return false
	}
	got := pbkdf2Sha256([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---------- sessions (HMAC-signed cookie: user|expiry|mac) ----------

const sessionCookieName = "onenat_dash"
const sessionTTL = 7 * 24 * time.Hour

type Sessions struct {
	secret []byte
}

func NewSessions(secret []byte) *Sessions {
	return &Sessions{secret: secret}
}

func (s *Sessions) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// Issue sets a fresh session cookie for the username.
func (s *Sessions) Issue(w http.ResponseWriter, username string, secure bool) {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := fmt.Sprintf("%s|%d", username, exp)
	value := payload + "|" + s.sign(payload)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

// Clear expires the session cookie.
func (s *Sessions) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// Verify returns the username for a valid, unexpired session cookie value.
func (s *Sessions) Verify(value string) (string, bool) {
	parts := strings.Split(value, "|")
	if len(parts) != 3 {
		return "", false
	}
	payload := parts[0] + "|" + parts[1]
	if subtle.ConstantTimeCompare([]byte(parts[2]), []byte(s.sign(payload))) != 1 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	return parts[0], true
}

// UserFromRequest resolves the session cookie to a store User (nil if anonymous).
func (d *Dashboard) UserFromRequest(r *http.Request) *User {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	username, ok := d.sessions.Verify(c.Value)
	if !ok {
		return nil
	}
	u := d.store.UserByName(username)
	if u == nil {
		return nil // deleted user => session invalid
	}
	return u
}

// randomHex is used for deploy-request rate limiting tokens etc.
func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
