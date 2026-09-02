package client

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	metrics "github.com/rcrowley/go-metrics"
	"io/ioutil"
	"math"
	"net"
	"ngrok/client/mvc"
	"ngrok/conn"
	"ngrok/gate"
	"ngrok/log"
	"ngrok/msg"
	"ngrok/proto"
	"ngrok/util"
	"ngrok/version"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultServerAddr   = "ngrokd.ngrok.com:443"
	defaultInspectAddr  = "127.0.0.1:4040"
	pingInterval        = 20 * time.Second
	maxPongLatency      = 15 * time.Second
	updateCheckInterval = 6 * time.Hour
	BadGateway          = `<html>
<body style="background-color: #97a8b9">
    <div style="margin:auto; width:400px;padding: 20px 60px; background-color: #D3D3D3; border: 5px solid maroon;">
        <h2>Tunnel %s unavailable</h2>
        <p>Unable to initiate connection to <strong>%s</strong>. A web server must be running on port <strong>%s</strong> to complete the tunnel.</p>
`
)

type ClientModel struct {
	log.Logger

	id            string
	tunnels       map[string]mvc.Tunnel
	serverVersion string
	metrics       *ClientMetrics
	updateStatus  mvc.UpdateStatus
	connStatus    mvc.ConnStatus
	protoMap      map[string]proto.Protocol
	protocols     []proto.Protocol
	ctl           mvc.Controller
	serverAddr    string
	proxyUrl      string
	authToken     string
	tlsConfig     *tls.Config
	tunnelConfig  map[string]*TunnelConfiguration
	configPath    string

	// AI remote-agent support
	config      *Configuration
	gate        *gate.Gate
	card        *ConnectionCard
	tunnelMetas map[string]tunnelMeta // publicUrl -> proto/localaddr for manual

	// dashboard-managed mode state (only touched by the control-loop
	// goroutine, so no locking needed)
	ctlConn        conn.Conn
	managedVersion int64
	managedDesired map[string]*msg.DesiredTunnel
	managedActive  map[string]string // mapping name -> public url
	managedErrors  map[string]string // mapping name -> last registration error
}

// tunnelMeta records what a public URL is for, so the agent manual can
// describe each entrypoint (ssh vs web) after tunnels are established.
type tunnelMeta struct {
	ProtoName string
	LocalAddr string
}

func newClientModel(config *Configuration, ctl mvc.Controller) *ClientModel {
	protoMap := make(map[string]proto.Protocol)
	protoMap["http"] = proto.NewHttp()
	protoMap["https"] = protoMap["http"]
	protoMap["tcp"] = proto.NewTcp()
	protocols := []proto.Protocol{protoMap["http"], protoMap["tcp"]}

	m := &ClientModel{
		Logger: log.NewPrefixLogger("client"),

		// server address
		serverAddr: config.ServerAddr,

		// proxy address
		proxyUrl: config.HttpProxy,

		// auth token
		authToken: config.AuthToken,

		// connection status
		connStatus: mvc.ConnConnecting,

		// update status
		updateStatus: mvc.UpdateNone,

		// metrics
		metrics: NewClientMetrics(),

		// protocols
		protoMap: protoMap,

		// protocol list
		protocols: protocols,

		// open tunnels
		tunnels: make(map[string]mvc.Tunnel),

		// controller
		ctl: ctl,

		// tunnel configuration
		tunnelConfig: config.Tunnels,

		// config path
		configPath: config.Path,

		// full configuration (gate settings, manual options)
		config: config,

		// entrypoint metadata for the agent manual
		tunnelMetas: make(map[string]tunnelMeta),
	}

	// dashboard-managed mode bookkeeping
	if config.Managed {
		m.managedDesired = make(map[string]*msg.DesiredTunnel)
		m.managedActive = make(map[string]string)
		m.managedErrors = make(map[string]string)
	}

	// configure TLS
	if config.TrustHostRootCerts {
		m.Info("Trusting host's root certificates")
		m.tlsConfig = &tls.Config{}
	} else {
		m.Info("Trusting root CAs: %v", rootCrtPaths)
		var err error
		if m.tlsConfig, err = LoadTLSConfig(rootCrtPaths); err != nil {
			panic(err)
		}
	}

	// configure TLS SNI
	m.tlsConfig.ServerName = serverName(m.serverAddr)
	m.tlsConfig.InsecureSkipVerify = useInsecureSkipVerify()

	return m
}

// server name in release builds is the host part of the server address
func serverName(addr string) string {
	host, _, err := net.SplitHostPort(addr)

	// should never panic because the config parser calls SplitHostPort first
	if err != nil {
		panic(err)
	}

	return host
}

// mvc.State interface
func (c ClientModel) GetProtocols() []proto.Protocol { return c.protocols }
func (c ClientModel) GetClientVersion() string       { return version.MajorMinor() }
func (c ClientModel) GetServerVersion() string       { return c.serverVersion }
func (c ClientModel) GetTunnels() []mvc.Tunnel {
	tunnels := make([]mvc.Tunnel, 0)
	for _, t := range c.tunnels {
		tunnels = append(tunnels, t)
	}
	return tunnels
}
func (c ClientModel) GetConnStatus() mvc.ConnStatus     { return c.connStatus }
func (c ClientModel) GetUpdateStatus() mvc.UpdateStatus { return c.updateStatus }

// GetCard returns the latest agent connection card (nil outside agent mode).
func (c ClientModel) GetCard() *ConnectionCard { return c.card }

func (c ClientModel) GetConnectionMetrics() (metrics.Meter, metrics.Timer) {
	return c.metrics.connMeter, c.metrics.connTimer
}

func (c ClientModel) GetBytesInMetrics() (metrics.Counter, metrics.Histogram) {
	return c.metrics.bytesInCount, c.metrics.bytesIn
}

func (c ClientModel) GetBytesOutMetrics() (metrics.Counter, metrics.Histogram) {
	return c.metrics.bytesOutCount, c.metrics.bytesOut
}
func (c ClientModel) SetUpdateStatus(updateStatus mvc.UpdateStatus) {
	c.updateStatus = updateStatus
	c.update()
}

// mvc.Model interface
func (c *ClientModel) PlayRequest(tunnel mvc.Tunnel, payload []byte) {
	var localConn conn.Conn
	localConn, err := conn.Dial(tunnel.LocalAddr, "prv", nil)
	if err != nil {
		c.Warn("Failed to open private leg to %s: %v", tunnel.LocalAddr, err)
		return
	}

	defer localConn.Close()
	localConn = tunnel.Protocol.WrapConn(localConn, mvc.ConnectionContext{Tunnel: tunnel, ClientAddr: "127.0.0.1"})
	localConn.Write(payload)
	ioutil.ReadAll(localConn)
}

func (c *ClientModel) Shutdown() {
	if c.gate != nil {
		c.gate.Close()
	}
}

func (c *ClientModel) update() {
	c.ctl.Update(c)
}

func (c *ClientModel) Run() {
	// how long we should wait before we reconnect
	maxWait := 30 * time.Second
	wait := 1 * time.Second

	for {
		// run the control channel
		c.control()

		// control only returns when a failure has occurred, so we're going to try to reconnect
		if c.connStatus == mvc.ConnOnline {
			wait = 1 * time.Second
		}

		log.Info("Waiting %d seconds before reconnecting", int(wait.Seconds()))
		time.Sleep(wait)
		// exponentially increase wait time
		wait = 2 * wait
		wait = time.Duration(math.Min(float64(wait), float64(maxWait)))
		c.connStatus = mvc.ConnReconnecting
		c.update()
	}
}

// Establishes and manages a tunnel control connection with the server
func (c *ClientModel) control() {
	defer func() {
		if r := recover(); r != nil {
			log.Error("control recovering from failure %v", r)
		}
	}()

	// establish control channel
	var (
		ctlConn conn.Conn
		err     error
	)
	if c.proxyUrl == "" {
		// simple non-proxied case, just connect to the server
		ctlConn, err = conn.Dial(c.serverAddr, "ctl", c.tlsConfig)
	} else {
		ctlConn, err = conn.DialHttpProxy(c.proxyUrl, c.serverAddr, "ctl", c.tlsConfig)
	}
	if err != nil {
		panic(err)
	}
	defer ctlConn.Close()
	c.ctlConn = ctlConn

	// authenticate with the server
	auth := &msg.Auth{
		ClientId:  c.id,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Version:   version.Proto,
		MmVersion: version.MajorMinor(),
		User:      c.authToken,
	}

	if err = msg.WriteMsg(ctlConn, auth); err != nil {
		panic(err)
	}

	// wait for the server to authenticate us
	var authResp msg.AuthResp
	if err = msg.ReadMsgInto(ctlConn, &authResp); err != nil {
		panic(err)
	}

	if authResp.Error != "" {
		emsg := fmt.Sprintf("Failed to authenticate to server: %s", authResp.Error)
		c.ctl.Shutdown(emsg)
		return
	}

	c.id = authResp.ClientId
	c.serverVersion = authResp.MmVersion
	c.Info("Authenticated with server, client id: %v", c.id)
	c.update()
	// zero-config agent mode has no config file to persist the token into;
	// the machine key file already holds it
	if err = SaveAuthToken(c.configPath, c.authToken); err != nil && c.configPath != "" {
		c.Error("Failed to save auth token: %v", err)
	}

	// request tunnels
	reqIdToTunnelConfig := make(map[string]*TunnelConfiguration)
	for _, config := range c.tunnelConfig {
		// create the protocol list to ask for
		var protocols []string
		for proto, _ := range config.Protocols {
			protocols = append(protocols, proto)
		}

		reqTunnel := &msg.ReqTunnel{
			ReqId:      util.RandId(8),
			Protocol:   strings.Join(protocols, "+"),
			Hostname:   config.Hostname,
			Subdomain:  config.Subdomain,
			HttpAuth:   config.HttpAuth,
			RemotePort: config.RemotePort,
		}

		// send the tunnel request
		if err = msg.WriteMsg(ctlConn, reqTunnel); err != nil {
			panic(err)
		}

		// save request id association so we know which local address
		// to proxy to later
		reqIdToTunnelConfig[reqTunnel.ReqId] = config
	}

	// start the heartbeat
	lastPong := time.Now().UnixNano()
	c.ctl.Go(func() { c.heartbeat(&lastPong, ctlConn) })

	// main control loop
	for {
		var rawMsg msg.Message
		if rawMsg, err = msg.ReadMsg(ctlConn); err != nil {
			panic(err)
		}

		switch m := rawMsg.(type) {
		case *msg.ReqProxy:
			token := m.Token
			c.ctl.Go(func() { c.proxy(token) })

		case *msg.Pong:
			atomic.StoreInt64(&lastPong, time.Now().UnixNano())

		case *msg.ConfigSync:
			// dashboard-managed mode: rebuild towards the server's desired set
			if c.config.Managed {
				c.applyConfigSync(m, ctlConn)
			}

		case *msg.NewTunnel:
			if m.Error != "" {
				if c.config.Managed && m.Name != "" {
					// keep the client alive; surface the error to the
					// dashboard via the next ack
					c.Error("Server failed to allocate tunnel %s: %s", m.Name, m.Error)
					c.managedErrors[m.Name] = m.Error
					delete(c.managedActive, m.Name)
					c.sendManagedAck()
					c.update()
					continue
				}
				emsg := fmt.Sprintf("Server failed to allocate tunnel: %s", m.Error)
				c.Error(emsg)
				c.ctl.Shutdown(emsg)
				continue
			}

			var localAddr string
			if cfg := reqIdToTunnelConfig[m.ReqId]; cfg != nil {
				localAddr = cfg.Protocols[m.Protocol]
			} else if spec := c.managedDesired[m.Name]; spec != nil {
				localAddr = spec.LocalAddr
			}

			tunnel := mvc.Tunnel{
				PublicUrl: m.Url,
				LocalAddr: localAddr,
				Protocol:  c.protoMap[m.Protocol],
			}

			c.tunnels[tunnel.PublicUrl] = tunnel
			if c.config.Managed && m.Name != "" {
				c.managedActive[m.Name] = tunnel.PublicUrl
				delete(c.managedErrors, m.Name)
				c.Info("Managed mapping %s established at %v -> %s", m.Name, tunnel.PublicUrl, tunnel.LocalAddr)
				c.sendManagedAck()
			}
			c.tunnelMetas[tunnel.PublicUrl] = tunnelMeta{ProtoName: m.Protocol, LocalAddr: tunnel.LocalAddr}
			// advertise entrypoints under the host the client actually
			// dialed (supports bare-IP servers); remember the mapping
			displayUrl := tunnel.PublicUrl
			if serverHost, _, serr := net.SplitHostPort(c.serverAddr); serr == nil && serverHost != "" {
				if ip := net.ParseIP(serverHost); ip != nil {
					displayUrl = RewriteURLHost(tunnel.PublicUrl, serverHost)
				}
			}
			c.tunnelMetas[displayUrl] = c.tunnelMetas[tunnel.PublicUrl]
			if displayUrl != tunnel.PublicUrl {
				delete(c.tunnelMetas, tunnel.PublicUrl)
			}
			c.connStatus = mvc.ConnOnline
			c.Info("Tunnel established at %v", tunnel.PublicUrl)

			// agent mode: create the access gateway and generate the
			// machine-readable remote access manual once ALL agent tunnels
			// are up (ssh+web arrive as two NewTunnel messages)
			if c.config.Gate == "plain" && strings.HasPrefix(m.Protocol, "tcp") {
				if c.gate == nil {
					if c.config.GateToken != "" {
						c.gate = gate.NewFixed(c.config.GateToken)
					} else {
						c.gate = gate.New(c.config.GateTTL)
						c.gate.OnRotate(c.onCodeRotate)
					}
				}
			}
			if c.isAgentMode() && c.tunnelMetasComplete() {
				c.publishManual()
			}

			c.update()

		default:
			ctlConn.Warn("Ignoring unknown control message %v ", m)
		}
	}
}

// applyConfigSync rebuilds the client towards the server's desired tunnel
// set: routing entries are rebuilt from Active (tunnels the server already
// has open), missing ones are requested via ReqTunnel, and removals simply
// drop out (the server closed its side before pushing).
func (c *ClientModel) applyConfigSync(m *msg.ConfigSync, ctlConn conn.Conn) {
	c.Info("Config sync v%d: %d desired, %d active", m.Version, len(m.Desired), len(m.Active))
	c.managedVersion = m.Version

	desired := make(map[string]*msg.DesiredTunnel, len(m.Desired))
	for i := range m.Desired {
		desired[m.Desired[i].Name] = &m.Desired[i]
	}
	c.managedDesired = desired

	// rebuild the routing table (publicUrl -> local tunnel)
	tunnels := make(map[string]mvc.Tunnel)
	nextActive := make(map[string]string, len(m.Active))
	for name, url := range m.Active {
		spec := desired[name]
		if spec == nil {
			continue
		}
		p := c.protoMap[spec.Protocol]
		if p == nil {
			c.Error("Config sync: unsupported protocol %q for mapping %s", spec.Protocol, name)
			c.managedErrors[name] = "unsupported protocol " + spec.Protocol
			continue
		}
		tunnels[url] = mvc.Tunnel{
			PublicUrl: url,
			LocalAddr: spec.LocalAddr,
			Protocol:  p,
		}
		nextActive[name] = url
		delete(c.managedErrors, name)
	}
	c.tunnels = tunnels
	c.managedActive = nextActive

	// request every desired mapping the server does not have open yet
	for name, spec := range desired {
		if _, isActive := m.Active[name]; isActive {
			continue
		}
		req := &msg.ReqTunnel{
			ReqId:      name,
			Name:       name,
			Protocol:   spec.Protocol,
			Hostname:   spec.Hostname,
			Subdomain:  spec.Subdomain,
			HttpAuth:   spec.HttpAuth,
			RemotePort: spec.RemotePort,
		}
		if err := msg.WriteMsg(ctlConn, req); err != nil {
			c.Error("Failed to request managed tunnel %s: %v", name, err)
			c.managedErrors[name] = err.Error()
		}
	}

	c.sendManagedAck()
	c.update()
}

// sendManagedAck reports the current state of the desired set back to the
// server so the dashboard can render per-mapping status.
func (c *ClientModel) sendManagedAck() {
	if !c.config.Managed || c.ctlConn == nil {
		return
	}
	ack := &msg.AckConfig{Version: c.managedVersion}
	for name := range c.managedDesired {
		a := msg.AckTunnel{Name: name}
		if url, ok := c.managedActive[name]; ok {
			a.URL = url
		} else if e, ok := c.managedErrors[name]; ok {
			a.Error = e
		}
		ack.Tunnels = append(ack.Tunnels, a)
	}
	if err := msg.WriteMsg(c.ctlConn, ack); err != nil {
		c.Error("Failed to send config ack: %v", err)
	}
}

// isAgentMode reports whether this session was assembled by agent mode
// (zero-config, machine key). Agent mode always carries a fixed GateToken.
func (c *ClientModel) isAgentMode() bool {
	return c.config.Gate == "plain" && c.config.GateToken != ""
}

// tunnelMetasComplete reports whether every configured agent tunnel has
// received its NewTunnel from the server.
func (c *ClientModel) tunnelMetasComplete() bool {
	return len(c.tunnelMetas) >= len(c.config.Tunnels)
}

// publishManual generates the connection card and remote-manual.{md,json}
// from the currently established tunnels.
func (c *ClientModel) publishManual() {
	metas := make(map[string]struct {
		PublicUrl string
		ProtoName string
		LocalAddr string
	}, len(c.tunnelMetas))
	for url, m := range c.tunnelMetas {
		metas[url] = struct {
			PublicUrl string
			ProtoName string
			LocalAddr string
		}{url, m.ProtoName, m.LocalAddr}
	}
	services := classifyAgentServices(metas)
	card, err := GenerateManual(c.config, services)
	if err != nil {
		c.Error("Failed to generate remote manual: %v", err)
		return
	}
	c.card = card
	c.Info("Remote manual written to %s (remote-manual.md, remote-manual.json)", card.ManualPath)
}

// onCodeRotate refreshes the manual whenever the access code rotates so the
// files on disk always advertise the currently valid code. (Fixed machine
// keys never rotate, so this only fires in rotating-code mode.)
func (c *ClientModel) onCodeRotate(code string, expires time.Time) {
	if c.isAgentMode() {
		return
	}
	c.publishManual()
}

func isTargetAllowed(addr string, allowRemote bool) bool {
	if allowRemote {
		return true
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" || h == "127.0.0.1" || h == "localhost" || h == "::1" || strings.HasPrefix(h, "127.") {
		return true
	}
	return false
}

// Establishes and manages a tunnel proxy connection with the server
func (c *ClientModel) proxy(token string) {
	var (
		remoteConn conn.Conn
		err        error
	)

	if c.proxyUrl == "" {
		remoteConn, err = conn.Dial(c.serverAddr, "pxy", c.tlsConfig)
	} else {
		remoteConn, err = conn.DialHttpProxy(c.proxyUrl, c.serverAddr, "pxy", c.tlsConfig)
	}

	if err != nil {
		log.Error("Failed to establish proxy connection: %v", err)
		return
	}
	defer remoteConn.Close()

	var sig string
	if token != "" && c.authToken != "" {
		mac := hmac.New(sha256.New, []byte(c.authToken))
		mac.Write([]byte(token))
		sig = hex.EncodeToString(mac.Sum(nil))
	}

	err = msg.WriteMsg(remoteConn, &msg.RegProxy{ClientId: c.id, Token: token, Sig: sig})
	if err != nil {
		remoteConn.Error("Failed to write RegProxy: %v", err)
		return
	}

	// wait for the server to ack our register
	var startPxy msg.StartProxy
	if err = msg.ReadMsgInto(remoteConn, &startPxy); err != nil {
		remoteConn.Error("Server failed to write StartProxy: %v", err)
		return
	}

	tunnel, ok := c.tunnels[startPxy.Url]
	if !ok {
		remoteConn.Error("Couldn't find tunnel for proxy: %s", startPxy.Url)
		return
	}

	// defense-in-depth: ensure client config permits dialing this target
	if !isTargetAllowed(tunnel.LocalAddr, c.config.AllowRemoteTargets) {
		remoteConn.Error("Blocked dial to non-loopback target %s: remote targets disabled by client config", tunnel.LocalAddr)
		return
	}

	// start up the private connection
	start := time.Now()
	localConn, err := conn.Dial(tunnel.LocalAddr, "prv", nil)
	if err != nil {
		remoteConn.Warn("Failed to open private leg %s: %v", tunnel.LocalAddr, err)

		if tunnel.Protocol.GetName() == "http" {
			// try to be helpful when you're in HTTP mode and a human might see the output
			badGatewayBody := fmt.Sprintf(BadGateway, tunnel.PublicUrl, tunnel.LocalAddr, tunnel.LocalAddr)
			remoteConn.Write([]byte(fmt.Sprintf(`HTTP/1.0 502 Bad Gateway
Content-Type: text/html
Content-Length: %d

%s`, len(badGatewayBody), badGatewayBody)))
		}
		return
	}
	defer localConn.Close()

	m := c.metrics
	m.proxySetupTimer.Update(time.Since(start))
	m.connMeter.Mark(1)
	c.update()
	m.connTimer.Time(func() {
		remote := conn.Conn(remoteConn)
		// agent mode: the remote peer must pass the access-gateway
		// handshake before any byte reaches the local service
		if c.config.Gate == "plain" && c.gate != nil && tunnel.Protocol.GetName() == "tcp" {
			authed, gerr := c.gate.Handshake(remoteConn)
			if gerr != nil {
				remoteConn.Info("Gateway rejected connection: %v", gerr)
				return
			}
			remote = authed
		}
		local := conn.Conn(tunnel.Protocol.WrapConn(localConn, mvc.ConnectionContext{Tunnel: tunnel, ClientAddr: startPxy.ClientAddr}))
		bytesIn, bytesOut := conn.Join(local, remote)
		m.bytesIn.Update(bytesIn)
		m.bytesOut.Update(bytesOut)
		m.bytesInCount.Inc(bytesIn)
		m.bytesOutCount.Inc(bytesOut)
	})
	c.update()
}

// Hearbeating to ensure our connection ngrokd is still live
func (c *ClientModel) heartbeat(lastPongAddr *int64, conn conn.Conn) {
	lastPing := time.Unix(atomic.LoadInt64(lastPongAddr)-1, 0)
	ping := time.NewTicker(pingInterval)
	pongCheck := time.NewTicker(time.Second)

	defer func() {
		conn.Close()
		ping.Stop()
		pongCheck.Stop()
	}()

	for {
		select {
		case <-pongCheck.C:
			lastPong := time.Unix(0, atomic.LoadInt64(lastPongAddr))
			needPong := lastPong.Sub(lastPing) < 0
			pongLatency := time.Since(lastPing)

			if needPong && pongLatency > maxPongLatency {
				c.Info("Last ping: %v, Last pong: %v", lastPing, lastPong)
				c.Info("Connection stale, haven't gotten PongMsg in %d seconds", int(pongLatency.Seconds()))
				return
			}

		case <-ping.C:
			err := msg.WriteMsg(conn, &msg.Ping{})
			if err != nil {
				conn.Debug("Got error %v when writing PingMsg", err)
				return
			}
			lastPing = time.Now()
		}
	}
}
