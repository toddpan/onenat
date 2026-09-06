package dashboard

// crypto.go — at-rest encryption for application upstream credentials.
//
// Design (see docs/app-management-design.md §3.3):
//   - AES-256-GCM (authenticated encryption, stdlib only)
//   - sealed format: "enc:v1:<base64(nonce)>:<base64(ciphertext+tag)>"
//   - master key: 32 random bytes in a 0600 key file next to the data file,
//     or injected via ONENAT_SECRET_KEY (hex or base64) for containers
//   - versioned prefix allows future key rotation (v2 = re-encrypt in place)

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const sealedPrefixV1 = "enc:v1:"

// errNotSealed marks values that are not in sealed form (legacy plaintext).
var errNotSealed = errors.New("value is not sealed")

// LoadOrCreateMasterKey returns the 32-byte credential master key.
// Precedence: ONENAT_SECRET_KEY env (hex/base64/raw-32) > key file
// (auto-created with 0600 when missing).
func LoadOrCreateMasterKey(keyFile string) ([]byte, error) {
	if env := strings.TrimSpace(os.Getenv("ONENAT_SECRET_KEY")); env != "" {
		k, err := decodeMasterKey(env)
		if err != nil {
			return nil, fmt.Errorf("ONENAT_SECRET_KEY: %v", err)
		}
		return k, nil
	}
	if b, err := os.ReadFile(keyFile); err == nil {
		k, err := decodeMasterKey(strings.TrimSpace(string(b)))
		if err != nil {
			return nil, fmt.Errorf("secret key file %s: %v", keyFile, err)
		}
		return k, nil
	}
	k := randBytes(32)
	if err := os.MkdirAll(dirOf(keyFile), 0700); err != nil {
		return nil, err
	}
	// 0600: only the service account may read the master key
	if err := os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString(k)), 0600); err != nil {
		return nil, err
	}
	return k, nil
}

func decodeMasterKey(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := hexDecode(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if len(s) == 32 {
		return []byte(s), nil
	}
	return nil, fmt.Errorf("need 32 bytes of key material (hex/base64), got %d bytes", len(s))
}

func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errors.New("bad hex length")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi, ok1 := hexVal(s[2*i])
		lo, ok2 := hexVal(s[2*i+1])
		if !ok1 || !ok2 {
			return nil, errors.New("bad hex digit")
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// SealSecret encrypts plaintext with the master key. Empty input seals to ""
// (no credential configured) so we can distinguish "unset" from "empty".
func SealSecret(key []byte, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := randBytes(gcm.NonceSize())
	ct := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return sealedPrefixV1 + base64.StdEncoding.EncodeToString(nonce) + ":" +
		base64.StdEncoding.EncodeToString(ct), nil
}

// OpenSecret decrypts a sealed value. Values without the sealed prefix return
// errNotSealed so callers can decide how to treat legacy data.
func OpenSecret(key []byte, sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	if !strings.HasPrefix(sealed, sealedPrefixV1) {
		return "", errNotSealed
	}
	body := strings.TrimPrefix(sealed, sealedPrefixV1)
	parts := strings.SplitN(body, ":", 2)
	if len(parts) != 2 {
		return "", errors.New("malformed sealed value")
	}
	nonce, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("nonce: %v", err)
	}
	ct, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("ciphertext: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", errors.New("解密失败: 密钥不匹配或数据被篡改")
	}
	return string(pt), nil
}

// MustSeal seals and panics on failure; used where key errors are already
// fatal at startup.
func MustSeal(key []byte, plaintext string) string {
	s, err := SealSecret(key, plaintext)
	if err != nil {
		panic(err)
	}
	return s
}

// MaskSecret renders a credential for UI display: "••••" + last 4 chars.
// Short/empty values are fully masked so nothing meaningful leaks.
func MaskSecret(s string) string {
	if s == "" {
		return ""
	}
	const dot = "••••"
	if len(s) <= 4 {
		return dot
	}
	return dot + s[len(s)-4:]
}

// ConstantTimeEqual is a defensive helper for comparing revealed secrets.
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// secretKeyPathDefault places the master key next to the dashboard data file.
func secretKeyPathDefault(dataPath string) string {
	return filepath.Join(dirOf(dataPath), "onenat-secret.key")
}
