package dashboard

// audit.go — append-only JSONL audit trail for sensitive operations.
//
// Every credential reveal, credential update, app/skill mutation and AI
// credential read lands here: {ts, actor, action, target, ip, detail}.
// Best-effort: audit failures never break the request path, but they are
// logged to the main log.

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditEntry is one JSONL line in the audit log.
type AuditEntry struct {
	TS     string            `json:"ts"` // RFC3339
	Actor  string            `json:"actor"`       // "user:admin" / "apikey:onk-…前8位"
	Action string            `json:"action"`      // "app.create" / "cred.reveal" / ...
	Target string            `json:"target"`      // 对象 id (app-*/sk-*/tun-*)
	IP     string            `json:"ip,omitempty"`
	Result string            `json:"result"` // "ok" / "denied" / error text
	Detail map[string]string `json:"detail,omitempty"`
}

// AuditLog is an append-only JSONL writer. nil *AuditLog = auditing disabled
// (all Dashboard.Audit* calls become no-ops).
type AuditLog struct {
	mu   sync.Mutex
	path string
}

// NewAuditLog creates the audit writer; the directory is created lazily.
func NewAuditLog(path string) *AuditLog {
	return &AuditLog{path: path}
}

// Write appends one entry. Timestamps are UTC for unambiguous ordering.
func (a *AuditLog) Write(e AuditEntry) {
	if a == nil {
		return
	}
	if e.TS == "" {
		e.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if e.Result == "" {
		e.Result = "ok"
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(a.path), 0700); err != nil {
		return
	}
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

// clientIP extracts the remote IP (no proxy trust by default; XFF is trivially
// spoofable and this log is for accountability, not rate limiting keying).
func clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// dashboard.Audit* wrappers ------------------------------------------------

func actorName(u *User) string {
	if u == nil {
		return "user:?"
	}
	return "user:" + u.Username
}

func actorApiKey(k *ApiKey) string {
	if k == nil {
		return "apikey:?"
	}
	prefix := k.Key
	if len(prefix) > 12 {
		prefix = prefix[:12] // 只留前缀, 完整 KEY 不进审计日志
	}
	return "apikey:" + prefix
}

// AuditUser records an action performed by a dashboard user.
func (d *Dashboard) AuditUser(r *http.Request, u *User, action, target, result string, detail map[string]string) {
	d.audit.Write(AuditEntry{
		Actor: actorName(u), Action: action, Target: target,
		IP: clientIP(r), Result: result, Detail: detail,
	})
}

// AuditKey records an action performed via an AI API key.
func (d *Dashboard) AuditKey(r *http.Request, k *ApiKey, action, target, result string, detail map[string]string) {
	d.audit.Write(AuditEntry{
		Actor: actorApiKey(k), Action: action, Target: target,
		IP: clientIP(r), Result: result, Detail: detail,
	})
}

// ---------- tiny rate limiter (reveal / credential endpoints) ----------
//
// Same shape as the deploy endpoint counter in dashboard.go, but keyed by
// "bucket" (e.g. "reveal:<appID>") so per-object budgets are independent.

type hitWindow struct {
	start int64
	count int
}

type limiter struct {
	mu   sync.Mutex
	hits map[string]*hitWindow
}

func newLimiter() *limiter { return &limiter{hits: map[string]*hitWindow{}} }

// allow reports whether one more hit fits the budget (max per 60s window).
func (l *limiter) allow(key string, max int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := nowUnix()
	h, ok := l.hits[key]
	if !ok || now-h.start >= 60 {
		l.hits[key] = &hitWindow{start: now, count: 1}
		return true
	}
	h.count++
	return h.count <= max
}

// revealMaxPerMinute bounds plaintext credential exposure attempts.
const revealMaxPerMinute = 5
