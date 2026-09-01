// Package gate implements the ngrok client-side access gateway: a lightweight
// authentication handshake inserted between the tunnel's proxy connection and
// the local backend (e.g. sshd).
//
// A peer must present a valid access code as the very first line of the
// connection ("AUTH <code>") before any bytes are relayed to the backend.
// Unauthenticated bytes (port scans, brute force attempts, SSH banners) never
// reach the local service. After a successful handshake the connection becomes
// fully transparent, so standard ssh/scp work unchanged on the far side.
package gate

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"ngrok/conn"
)

const (
	// AuthTimeout is how long a peer has to present the access code.
	AuthTimeout = 5 * time.Second
	// MaxLineLen bounds the first line to something sane.
	MaxLineLen = 256
	// FailThreshold consecutive failures trigger CooldownPeriod.
	FailThreshold = 5
	// CooldownPeriod rejects every connection after too many failures.
	CooldownPeriod = 60 * time.Second
	// PreviousGrace keeps the previous access code valid briefly after a
	// rotation so connections prepared with the old manual don't break.
	PreviousGrace = 90 * time.Second
)

var (
	ErrBadCode  = errors.New("bad access code")
	ErrNoCode   = errors.New("no access code received")
	ErrCooldown = errors.New("rate limited after repeated failures")
)

// Policy configures a Gate.
type Policy struct {
	// TTL is the lifetime of one access code. 0 means the code is valid for
	// the lifetime of the process (session-static).
	TTL time.Duration
}

// Gate issues and verifies access codes for one tunnel.
type Gate struct {
	mu            sync.Mutex
	current       string
	previous      string
	previousUntil time.Time
	expiresAt     time.Time
	ttl           time.Duration
	fails         int
	cooldownUntil time.Time
	stop          chan struct{}
	onRotate      func(code string, expires time.Time)
}

// New creates a Gate and starts the rotation loop if ttl > 0.
func New(ttl time.Duration) *Gate {
	g := &Gate{ttl: ttl, stop: make(chan struct{})}
	g.current = NewCode()
	if ttl > 0 {
		g.expiresAt = time.Now().Add(ttl)
		go g.rotateLoop()
	}
	return g
}

// NewFixed creates a Gate whose access code is fixed for the lifetime of the
// process (used by zero-config agent mode where the code is the machine key).
func NewFixed(code string) *Gate {
	return &Gate{current: code, stop: make(chan struct{})}
}

// NewCode returns a random access code in the form "a1b2-c3d4".
func NewCode() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	h := hex.EncodeToString(b)
	return h[:4] + "-" + h[4:]
}

// OnRotate registers a callback fired every time the access code rotates.
func (g *Gate) OnRotate(fn func(code string, expires time.Time)) {
	g.onRotate = fn
}

// Close stops the rotation loop.
func (g *Gate) Close() {
	select {
	case <-g.stop:
	default:
		close(g.stop)
	}
}

func (g *Gate) rotateLoop() {
	for {
		select {
		case <-g.stop:
			return
		case <-time.After(g.ttl):
			code, expires := g.Rotate()
			if g.onRotate != nil {
				g.onRotate(code, expires)
			}
		}
	}
}

// Rotate replaces the current access code. The previous code keeps working
// for PreviousGrace so clients holding a just-stale manual are not stranded.
func (g *Gate) Rotate() (code string, expires time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.previous = g.current
	g.previousUntil = time.Now().Add(PreviousGrace)
	g.current = NewCode()
	if g.ttl > 0 {
		g.expiresAt = time.Now().Add(g.ttl)
	}
	return g.current, g.expiresAt
}

// Current returns the active access code and its expiry (zero time when static).
func (g *Gate) Current() (code string, expires time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.current, g.expiresAt
}

// CoolingDown reports whether the gate is rejecting everything right now.
func (g *Gate) CoolingDown() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return time.Now().Before(g.cooldownUntil)
}

func (g *Gate) registerFail() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fails++
	if g.fails >= FailThreshold {
		g.cooldownUntil = time.Now().Add(CooldownPeriod)
		g.fails = 0
	}
}

func (g *Gate) check(code string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if now.Before(g.cooldownUntil) {
		return ErrCooldown
	}
	ok := subtle.ConstantTimeCompare([]byte(code), []byte(g.current)) == 1
	if !ok && g.previous != "" && now.Before(g.previousUntil) {
		ok = subtle.ConstantTimeCompare([]byte(code), []byte(g.previous)) == 1
	}
	if !ok {
		g.fails++
		if g.fails >= FailThreshold {
			g.cooldownUntil = now.Add(CooldownPeriod)
			g.fails = 0
		}
		return ErrBadCode
	}
	g.fails = 0
	return nil
}

// Handshake authenticates the peer on c. On success it returns a wrapped
// connection that first replays any bytes buffered past the auth line (a
// peer may pipeline its SSH banner with the auth line in one segment) and
// then streams c fully transparently. On failure the peer receives a short
// error line and the caller should close the connection.
func (g *Gate) Handshake(c conn.Conn) (conn.Conn, error) {
	if g.CoolingDown() {
		writeErr(c, "rate limited, retry in a minute")
		return nil, ErrCooldown
	}

	c.SetReadDeadline(time.Now().Add(AuthTimeout))
	line, rest, err := readFirstLine(c)
	if err != nil {
		g.registerFail()
		writeErr(c, "%v", ErrNoCode)
		return nil, err
	}

	code, ok := ParseAuthLine(line)
	if !ok {
		g.registerFail()
		writeErr(c, "bad request, expected: AUTH <code>")
		return nil, ErrBadCode
	}

	if err := g.check(code); err != nil {
		writeErr(c, "access denied")
		return nil, err
	}

	// authenticated: restore normal streaming behavior
	c.SetReadDeadline(time.Time{})
	return &authedConn{Conn: c, rest: rest}, nil
}

// readFirstLine reads up to the first '\n' and returns the line plus any
// bytes that arrived beyond it in the same read burst.
func readFirstLine(c conn.Conn) (line []byte, rest []byte, err error) {
	buf := make([]byte, 1)
	seenNL := false
	for len(line)+len(rest) <= MaxLineLen {
		n, rerr := c.Read(buf)
		for i := 0; i < n; i++ {
			b := buf[i]
			switch {
			case !seenNL && b == '\n':
				seenNL = true
			case !seenNL:
				line = append(line, b)
			default:
				rest = append(rest, b)
			}
		}
		if seenNL {
			return line, rest, nil
		}
		if rerr != nil {
			return nil, nil, rerr
		}
	}
	return nil, nil, errors.New("auth line too long")
}

// ParseAuthLine validates "AUTH <code>" (case-insensitive verb, tolerant of
// trailing CR/whitespace) and returns the code.
func ParseAuthLine(line []byte) (code string, ok bool) {
	s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(string(line)), "\r"))
	parts := strings.SplitN(s, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "AUTH") {
		return "", false
	}
	code = strings.TrimSpace(parts[1])
	if code == "" || len(code) > 64 {
		return "", false
	}
	return code, true
}

func writeErr(c conn.Conn, format string, args ...interface{}) {
	c.SetWriteDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(c, "ERR ngrok-gate: "+format+"\n", args...)
}

// authedConn replays bytes that were buffered during the handshake, then
// delegates everything else to the underlying connection.
type authedConn struct {
	conn.Conn
	rest []byte
}

func (a *authedConn) Read(p []byte) (int, error) {
	if len(a.rest) > 0 {
		n := copy(p, a.rest)
		a.rest = a.rest[n:]
		return n, nil
	}
	return a.Conn.Read(p)
}
