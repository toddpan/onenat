package gate

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"ngrok/conn"
)

// pipeConn is a minimal in-memory conn.Conn pair for gate tests.
type pipeConn struct {
	net.Conn
}

func (p pipeConn) SetType(string)             {}
func (p pipeConn) Id() string                 { return "test" }
func (p pipeConn) CloseRead() error           { return nil }
func (p pipeConn) AddLogPrefix(string)        {}
func (p pipeConn) ClearLogPrefixes()          {}
func (p pipeConn) Debug(string, ...interface{})   {}
func (p pipeConn) Info(string, ...interface{})    {}
func (p pipeConn) Warn(string, ...interface{}) error  { return nil }
func (p pipeConn) Error(string, ...interface{}) error { return nil }

func newPair(t *testing.T) (client, server conn.Conn) {
	c1, c2 := net.Pipe()
	return pipeConn{c1}, pipeConn{c2}
}

func TestParseAuthLine(t *testing.T) {
	cases := []struct {
		in   string
		code string
		ok   bool
	}{
		{"AUTH abcd-1234", "abcd-1234", true},
		{"auth abcd-1234", "abcd-1234", true},
		{"AUTH abcd-1234\r", "abcd-1234", true},
		{"AUTH abcd-1234 \n", "abcd-1234", true},
		{"AUTH", "", false},
		{"AUTH ", "", false},
		{"HELLO abcd", "", false},
		{"SSH-2.0-OpenSSH_9.6", "", false},
		{"AUTH " + string(make([]byte, 100)), "", false},
	}
	for _, c := range cases {
		code, ok := ParseAuthLine([]byte(c.in))
		if ok != c.ok || (ok && code != c.code) {
			t.Errorf("ParseAuthLine(%q) = %q,%v want %q,%v", c.in, code, ok, c.code, c.ok)
		}
	}
}

func TestHandshakeOK(t *testing.T) {
	g := New(0)
	defer g.Close()
	code, _ := g.Current()

	cl, sv := newPair(t)
	go func() {
		fmt.Fprintf(cl, "AUTH %s\r\n", code)
		fmt.Fprint(cl, "SSH-2.0-test\r\n")
	}()

	authed, err := g.Handshake(sv)
	if err != nil {
		t.Fatalf("Handshake: %v", err)
	}

	// first read should yield the pipelined banner (rest buffer)
	buf := make([]byte, 32)
	n, err := authed.Read(buf)
	if err != nil || string(buf[:n]) != "SSH-2.0-test\r\n" {
		t.Fatalf("rest replay = %q err=%v", buf[:n], err)
	}

	// then transparent streaming
	go fmt.Fprint(cl, "more-bytes")
	n, err = authed.Read(buf)
	if err != nil || string(buf[:n]) != "more-bytes" {
		t.Fatalf("stream read = %q err=%v", buf[:n], err)
	}
}

// badAttempt drives one handshake with the given first line on one end of a
// pipe while concurrently draining the gate's error reply, then asserts the
// handshake error and the reply line. (net.Pipe is synchronous: writes only
// complete when the other side reads, so the peer's write and read run in
// separate goroutines and carry deadlines.)
func badAttempt(t *testing.T, g *Gate, line string, wantErr error, wantReply string) {
	t.Helper()
	cl, sv := newPair(t)
	replyCh := make(chan string, 1)
	go func() {
		cl.SetWriteDeadline(time.Now().Add(2 * time.Second))
		fmt.Fprint(cl, line)
	}()
	go func() {
		cl.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 128)
		n, _ := cl.Read(buf)
		replyCh <- string(buf[:n])
	}()
	_, err := g.Handshake(sv)
	if err != wantErr {
		t.Fatalf("want %v got %v", wantErr, err)
	}
	if got := <-replyCh; got != wantReply {
		t.Fatalf("peer reply = %q want %q", got, wantReply)
	}
}

func TestHandshakeWrongCode(t *testing.T) {
	g := New(0)
	defer g.Close()
	badAttempt(t, g, "AUTH 0000-0000\n", ErrBadCode, "ERR ngrok-gate: access denied\n")
}

func TestHandshakeGarbage(t *testing.T) {
	g := New(0)
	defer g.Close()
	badAttempt(t, g, "SSH-2.0-OpenSSH_9.6\r\n", ErrBadCode, "ERR ngrok-gate: bad request, expected: AUTH <code>\n")
}

func TestHandshakeNoCode(t *testing.T) {
	g := New(0)
	defer g.Close()

	cl, sv := newPair(t)
	// silent peer: EOF instead of waiting out the 5s auth timeout
	cl.Close()

	if _, err := g.Handshake(sv); err == nil {
		t.Fatal("want error on silent/EOF peer")
	}
}

func TestRotationAndGrace(t *testing.T) {
	g := New(0)
	defer g.Close()
	old, _ := g.Current()
	g.Rotate()
	now, _ := g.Current()
	if now == old {
		t.Fatal("code did not rotate")
	}

	// previous code still accepted during grace window
	cl, sv := newPair(t)
	go fmt.Fprintf(cl, "AUTH %s\n", old)
	if _, err := g.Handshake(sv); err != nil {
		t.Fatalf("previous code should be accepted in grace window: %v", err)
	}
}

func TestCooldown(t *testing.T) {
	g := New(0)
	defer g.Close()

	for i := 0; i < FailThreshold; i++ {
		badAttempt(t, g, "AUTH 0000-0000\n", ErrBadCode, "ERR ngrok-gate: access denied\n")
	}

	if !g.CoolingDown() {
		t.Fatal("gate should be cooling down after threshold failures")
	}

	badAttempt(t, g, "AUTH 0000-0000\n", ErrCooldown, "ERR ngrok-gate: rate limited, retry in a minute\n")
}

func TestTTLExpiryRotation(t *testing.T) {
	g := New(25 * time.Millisecond)
	defer g.Close()
	first, _ := g.Current()
	seen := first
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, _ := g.Current()
		if c != seen {
			return // rotated at least once
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("code never rotated under ttl")
}

// compile-time interface checks
var (
	_ conn.Conn = pipeConn{}
	_ conn.Conn = &authedConn{}
	_ = bytes.MinRead
)
