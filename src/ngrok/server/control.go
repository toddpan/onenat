package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"ngrok/conn"
	"ngrok/msg"
	"ngrok/server/dashboard"
	"ngrok/util"
	"ngrok/version"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

const (
	pingTimeoutInterval = 30 * time.Second
	connReapInterval    = 10 * time.Second
	controlWriteTimeout = 10 * time.Second
	proxyStaleDuration  = 60 * time.Second
	proxyMaxPoolSize    = 10
)

type Control struct {
	// auth message
	auth *msg.Auth

	// actual connection
	conn conn.Conn

	// put a message in this channel to send it over
	// conn to the client
	out chan (msg.Message)

	// read from this channel to get the next message sent
	// to us over conn by the client
	in chan (msg.Message)

	// the last time we received a ping from the client - for heartbeats
	lastPing time.Time

	// all of the tunnels this control connection handles
	tunnels []*Tunnel

	// guards tunnels mutations: the manager goroutine registers tunnels
	// while the dashboard goroutine reconciles them via ApplyDesired
	tunnelsMu sync.Mutex

	// dashboard-managed tunnel id; empty for classic/agent clients.
	// Managed clients authenticate with the tunnel key and always use the
	// stable ClientId "tun-<tunnelId>".
	dashTunnelID string

	// proxy connections
	proxies chan conn.Conn

	// single-use proxy token tracking for managed tunnel auth
	pendingTokens   map[string]time.Time
	pendingTokensMu sync.Mutex

	// identifier
	id string

	// synchronizer for controlled shutdown of writer()
	writerShutdown *util.Shutdown

	// synchronizer for controlled shutdown of reader()
	readerShutdown *util.Shutdown

	// synchronizer for controlled shutdown of manager()
	managerShutdown *util.Shutdown

	// synchronizer for controller shutdown of entire Control
	shutdown *util.Shutdown
}

// tokenAllowed reports whether the presented token matches any of the
// comma-separated required tokens (constant-time per candidate).
func tokenAllowed(required, presented string) bool {
	for _, tok := range strings.Split(required, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(presented)) == 1 {
			return true
		}
	}
	return false
}

func NewControl(ctlConn conn.Conn, authMsg *msg.Auth) {
	var err error

		// create the object
		c := &Control{
			auth:            authMsg,
			conn:            ctlConn,
			out:             make(chan msg.Message),
			in:              make(chan msg.Message),
			proxies:         make(chan conn.Conn, 10),
			pendingTokens:   make(map[string]time.Time),
			lastPing:        time.Now(),
			writerShutdown:  util.NewShutdown(),
			readerShutdown:  util.NewShutdown(),
			managerShutdown: util.NewShutdown(),
			shutdown:        util.NewShutdown(),
		}

	failAuth := func(e error) {
		_ = msg.WriteMsg(ctlConn, &msg.AuthResp{Error: e.Error()})
		ctlConn.Close()
	}

	// register the clientid
	c.id = authMsg.ClientId
	if c.id == "" {
		// it's a new session, assign an ID
		if c.id, err = util.SecureRandId(16); err != nil {
			failAuth(err)
			return
		}
	}

	// enforce the server's required auth token(s), if configured.
	// -authToken accepts a comma-separated list (zero-config agent clients
	// each present their stable machine key). An empty presented token
	// fails every constant-time comparison on purpose.
	//
	// Additionally, dashboard-managed tunnels authenticate with their
	// per-tunnel key regardless of -authToken.
	var managedTun *dashboard.Tunnel
	if dash != nil {
		if t, ok := dash.Store().AuthenticateKey(authMsg.User); ok {
			managedTun = t
		}
	}
	if opts.authToken != "" {
		if !tokenAllowed(opts.authToken, authMsg.User) && managedTun == nil {
			c.conn.Error("Authentication failed: invalid auth token")
			failAuth(fmt.Errorf("Invalid authentication token"))
			return
		}
	}

	// bind the control to the managed tunnel: stable ClientId gives us
	// session replacement on reconnect + TCP port affinity from the cache
	if managedTun != nil {
		if managedTun.Locked {
			c.conn.Error("Authentication failed: tunnel %s is locked", managedTun.ID)
			failAuth(fmt.Errorf("Tunnel is locked"))
			return
		}
		c.dashTunnelID = managedTun.ID
		c.id = managedClientId(managedTun.ID)
		if dash != nil {
			dash.TouchClient(managedTun.ID, authMsg.MmVersion, authMsg.OS)
		}
	} else if strings.HasPrefix(c.id, "tun-") {
		// A client claiming a managed slot ("tun-<id>") must present that
		// tunnel's CURRENT key. Otherwise a stale-key client could resurrect
		// the identity of a reset/deleted tunnel after a reconnect.
		c.conn.Error("Authentication failed: invalid token for managed client %s", c.id)
		failAuth(fmt.Errorf("Invalid authentication token"))
		return
	}

	// set logging prefix
	ctlConn.SetType("ctl")
	ctlConn.AddLogPrefix(c.id)

	if authMsg.Version != version.Proto {
		failAuth(fmt.Errorf("Incompatible versions. Server %s, client %s. Download a new version at http://ngrok.com", version.MajorMinor(), authMsg.Version))
		return
	}

	// register the control
	if replaced := controlRegistry.Add(c.id, c); replaced != nil {
		replaced.shutdown.WaitComplete()
	}

	// start the writer first so that the following messages get sent
	go c.writer()

	// Respond to authentication
	c.out <- &msg.AuthResp{
		Version:   version.Proto,
		MmVersion: version.MajorMinor(),
		ClientId:  c.id,
	}

		// As a performance optimization, ask for a proxy connection up front
		c.out <- &msg.ReqProxy{Token: c.issueProxyToken()}

	// managed client: push the desired tunnel configuration right away so
	// the client requests its tunnels from the dashboard's source of truth
	if c.dashTunnelID != "" && dash != nil {
		dash.PushConfig(c.dashTunnelID)
	}

	// manage the connection
	go c.manager()
	go c.reader()
	go c.stopper()
}

// Register a new tunnel on this control connection
func (c *Control) registerTunnel(rawTunnelReq *msg.ReqTunnel) {
	for _, proto := range strings.Split(rawTunnelReq.Protocol, "+") {
		tunnelReq := *rawTunnelReq
		tunnelReq.Protocol = proto

		c.conn.Debug("Registering new tunnel")
		t, err := NewTunnel(&tunnelReq, c)
		if err != nil {
			c.out <- &msg.NewTunnel{Error: err.Error(), ReqId: rawTunnelReq.ReqId, Name: rawTunnelReq.Name}
			// classic clients die when their only tunnel fails; managed
			// clients stay connected so the dashboard can show the error
			// and keep serving the other mappings
			if c.dashTunnelID == "" && len(c.tunnels) == 0 {
				c.shutdown.Begin()
			}

			// we're done
			return
		}

		// add it to the list of tunnels
		c.tunnelsMu.Lock()
		c.tunnels = append(c.tunnels, t)
		c.tunnelsMu.Unlock()

		// acknowledge success
		c.out <- &msg.NewTunnel{
			Url:      t.url,
			Protocol: proto,
			ReqId:    rawTunnelReq.ReqId,
			Name:     rawTunnelReq.Name,
		}

		rawTunnelReq.Hostname = strings.Replace(t.url, proto+"://", "", 1)
	}
}

func (c *Control) manager() {
	// don't crash on panics
	defer func() {
		if err := recover(); err != nil {
			c.conn.Info("Control::manager failed with error %v: %s", err, debug.Stack())
		}
	}()

	// kill everything if the control manager stops
	defer c.shutdown.Begin()

	// notify that manager() has shutdown
	defer c.managerShutdown.Complete()

	// reaping timer for detecting heartbeat failure
	reap := time.NewTicker(connReapInterval)
	defer reap.Stop()

	for {
		select {
		case <-reap.C:
			if time.Since(c.lastPing) > pingTimeoutInterval {
				c.conn.Info("Lost heartbeat")
				c.shutdown.Begin()
			}

		case mRaw, ok := <-c.in:
			// c.in closes to indicate shutdown
			if !ok {
				return
			}

			switch m := mRaw.(type) {
			case *msg.ReqTunnel:
				c.registerTunnel(m)

			case *msg.AckConfig:
				// managed client reporting per-mapping status
				if c.dashTunnelID != "" && dash != nil {
					dash.ReportAck(c.dashTunnelID, m)
				}

			case *msg.Ping:
				c.lastPing = time.Now()
				c.out <- &msg.Pong{}
			}
		}
	}
}

func (c *Control) writer() {
	defer func() {
		if err := recover(); err != nil {
			c.conn.Info("Control::writer failed with error %v: %s", err, debug.Stack())
		}
	}()

	// kill everything if the writer() stops
	defer c.shutdown.Begin()

	// notify that we've flushed all messages
	defer c.writerShutdown.Complete()

	// write messages to the control channel
	for m := range c.out {
		c.conn.SetWriteDeadline(time.Now().Add(controlWriteTimeout))
		if err := msg.WriteMsg(c.conn, m); err != nil {
			panic(err)
		}
	}
}

func (c *Control) reader() {
	defer func() {
		if err := recover(); err != nil {
			c.conn.Warn("Control::reader failed with error %v: %s", err, debug.Stack())
		}
	}()

	// kill everything if the reader stops
	defer c.shutdown.Begin()

	// notify that we're done
	defer c.readerShutdown.Complete()

	// read messages from the control channel
	for {
		if msg, err := msg.ReadMsg(c.conn); err != nil {
			if err == io.EOF {
				c.conn.Info("EOF")
				return
			} else {
				panic(err)
			}
		} else {
			// this can also panic during shutdown
			c.in <- msg
		}
	}
}

func (c *Control) stopper() {
	defer func() {
		if r := recover(); r != nil {
			c.conn.Error("Failed to shut down control: %v", r)
		}
	}()

	// wait until we're instructed to shutdown
	c.shutdown.WaitBegin()

	// remove ourself from the control registry
	controlRegistry.Del(c.id)

	// shutdown manager() so that we have no more work to do
	close(c.in)
	c.managerShutdown.WaitComplete()

	// shutdown writer()
	close(c.out)
	c.writerShutdown.WaitComplete()

	// close connection fully
	c.conn.Close()

	// shutdown all of the tunnels
	for _, t := range c.tunnels {
		t.Shutdown()
	}

	// shutdown all of the proxy connections
	close(c.proxies)
	for p := range c.proxies {
		p.Close()
	}

	c.shutdown.Complete()
	c.conn.Info("Shutdown complete")
}

func (c *Control) RegisterProxy(conn conn.Conn) {
	conn.AddLogPrefix(c.id)

	conn.SetDeadline(time.Now().Add(proxyStaleDuration))
	select {
	case c.proxies <- conn:
		conn.Info("Registered")
	default:
		conn.Info("Proxies buffer is full, discarding.")
		conn.Close()
	}
}

// Remove a proxy connection from the pool and return it
// If not proxy connections are in the pool, request one
// and wait until it is available
// Returns an error if we couldn't get a proxy because it took too long
// or the tunnel is closing
func (c *Control) GetProxy() (proxyConn conn.Conn, err error) {
	var ok bool

	// get a proxy connection from the pool
	select {
	case proxyConn, ok = <-c.proxies:
		if !ok {
			err = fmt.Errorf("No proxy connections available, control is closing")
			return
		}
		default:
			// no proxy available in the pool, ask for one over the control channel
			c.conn.Debug("No proxy in pool, requesting proxy from control . . .")
			if err = util.PanicToError(func() { c.out <- &msg.ReqProxy{Token: c.issueProxyToken()} }); err != nil {
				return
			}

		select {
		case proxyConn, ok = <-c.proxies:
			if !ok {
				err = fmt.Errorf("No proxy connections available, control is closing")
				return
			}

		case <-time.After(pingTimeoutInterval):
			err = fmt.Errorf("Timeout trying to get proxy connection")
			return
		}
	}
	return
}

// Called when this control is replaced by another control
// this can happen if the network drops out and the client reconnects
// before the old tunnel has lost its heartbeat
func (c *Control) Replaced(replacement *Control) {
	c.conn.Info("Replaced by control: %s", replacement.conn.Id())

	// set the control id to empty string so that when stopper()
	// calls registry.Del it won't delete the replacement
	c.id = ""

	// tell the old one to shutdown
	c.shutdown.Begin()
}

// issueProxyToken generates a fresh single-use nonce for proxy connections
func (c *Control) issueProxyToken() string {
	c.pendingTokensMu.Lock()
	defer c.pendingTokensMu.Unlock()
	now := time.Now()
	// reap expired tokens
	for tok, exp := range c.pendingTokens {
		if now.After(exp) {
			delete(c.pendingTokens, tok)
		}
	}
	tok, err := util.SecureRandId(16)
	if err != nil {
		tok = fmt.Sprintf("%d-%d", now.UnixNano(), time.Now().Nanosecond())
	}
	if c.pendingTokens == nil {
		c.pendingTokens = make(map[string]time.Time)
	}
	c.pendingTokens[tok] = now.Add(90 * time.Second)
	return tok
}

// VerifyAndConsumeProxyToken verifies and consumes the single-use token and its HMAC signature.
func (c *Control) VerifyAndConsumeProxyToken(token, sig, tunnelKey string) bool {
	// Preserve backwards compatibility for unmanaged/classic tunnels
	if c.dashTunnelID == "" {
		return true
	}
	if token == "" || sig == "" || tunnelKey == "" {
		return false
	}
	c.pendingTokensMu.Lock()
	defer c.pendingTokensMu.Unlock()
	exp, ok := c.pendingTokens[token]
	if !ok || time.Now().After(exp) {
		delete(c.pendingTokens, token)
		return false
	}
	delete(c.pendingTokens, token)

	// verify HMAC-SHA256(tunnelKey, token)
	mac := hmac.New(sha256.New, []byte(tunnelKey))
	mac.Write([]byte(token))
	want := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(sig), []byte(want)) == 1
}

