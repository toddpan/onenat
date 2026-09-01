package dashboard

import (
	"net/http/httptest"
	"testing"
)

func TestSessionsRoundtrip(t *testing.T) {
	s := NewSessions([]byte("test-secret"))
	rec := httptest.NewRecorder()
	s.Issue(rec, "admin")

	cookie := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("session cookie must be set")
	}

	user, ok := s.Verify(cookie)
	if !ok || user != "admin" {
		t.Fatalf("valid cookie must verify, got %q ok=%v", user, ok)
	}

	parts := splitPipe(cookie, 3)
	if len(parts) != 3 {
		t.Fatalf("cookie must have 3 parts, got %d", len(parts))
	}
	if _, ok := s.Verify(cookie + "x"); ok {
		t.Fatal("tampered mac must fail")
	}
	if _, ok := s.Verify("evil|" + parts[1] + "|" + parts[2]); ok {
		t.Fatal("forged username must fail")
	}
	if _, ok := s.Verify(parts[0] + "|1|" + parts[2]); ok {
		t.Fatal("expired timestamp must fail")
	}
	if _, ok := s.Verify(""); ok {
		t.Fatal("empty value must fail")
	}

	// keys are isolated per secret
	if _, ok := NewSessions([]byte("other-secret")).Verify(cookie); ok {
		t.Fatal("cookie signed with another secret must fail")
	}
}

func splitPipe(s string, n int) []string {
	out := make([]string, 0, n)
	start := 0
	for i := 0; i < len(s) && len(out) < n-1; i++ {
		if s[i] == '|' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
