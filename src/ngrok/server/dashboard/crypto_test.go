package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealOpenRoundtrip(t *testing.T) {
	key := randBytes(32)
	plain := "p@ssw0rd-密码-🔑"
	sealed, err := SealSecret(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, "enc:v1:") {
		t.Fatalf("sealed value must carry version prefix, got %q", sealed)
	}
	if sealed == plain {
		t.Fatal("sealed must differ from plaintext")
	}
	got, err := OpenSecret(key, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if got != plain {
		t.Fatalf("roundtrip mismatch: %q", got)
	}
	// empty stays empty (unset vs set distinction)
	if s, _ := SealSecret(key, ""); s != "" {
		t.Fatal("empty must seal to empty")
	}
	if g, _ := OpenSecret(key, ""); g != "" {
		t.Fatal("empty must open to empty")
	}
}

func TestOpenSecretTamperDetection(t *testing.T) {
	key := randBytes(32)
	sealed, _ := SealSecret(key, "secret-value")
	// flip one char of the ciphertext
	bad := sealed[:len(sealed)-2] + "AA"
	if _, err := OpenSecret(key, bad); err == nil {
		t.Fatal("tampered ciphertext must fail to open")
	}
	// wrong key
	if _, err := OpenSecret(randBytes(32), sealed); err == nil {
		t.Fatal("wrong key must fail to open")
	}
	// non-sealed value
	if _, err := OpenSecret(key, "plaintext"); err != errNotSealed {
		t.Fatalf("plaintext must return errNotSealed, got %v", err)
	}
}

func TestLoadOrCreateMasterKey(t *testing.T) {
	dir := t.TempDir()
	kf := filepath.Join(dir, "secret.key")
	k1, err := LoadOrCreateMasterKey(kf)
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != 32 {
		t.Fatalf("key must be 32 bytes, got %d", len(k1))
	}
	// file created with 0600
	fi, _ := os.Stat(kf)
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("key file must be 0600, got %v", fi.Mode().Perm())
	}
	// second load returns the same key
	k2, err := LoadOrCreateMasterKey(kf)
	if err != nil {
		t.Fatal(err)
	}
	if string(k1) != string(k2) {
		t.Fatal("master key must be stable across loads")
	}
}

func TestMaskSecret(t *testing.T) {
	if got := MaskSecret(""); got != "" {
		t.Fatalf("empty mask = empty, got %q", got)
	}
	if got := MaskSecret("abc"); got != "••••" {
		t.Fatalf("short values fully masked, got %q", got)
	}
	if got := MaskSecret("super-long-api-key-9999"); got != "••••9999" {
		t.Fatalf("mask keeps last 4, got %q", got)
	}
}

func TestSealedValueNeverContainsPlaintext(t *testing.T) {
	key := randBytes(32)
	secret := "ThunderSoft-top-secret-88"
	sealed, _ := SealSecret(key, secret)
	if strings.Contains(sealed, secret) {
		t.Fatal("sealed value must not embed plaintext")
	}
	// JSON serialization of an app with sealed creds never contains plaintext
	a := &App{ID: NewAppID(), Name: "t", Auth: AppAuth{PasswordEnc: sealed}}
	b, _ := json.Marshal(a)
	if strings.Contains(string(b), secret) {
		t.Fatal("JSON of sealed app must not contain plaintext")
	}
}
