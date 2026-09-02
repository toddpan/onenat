package server

import (
	"crypto/tls"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"ngrok/conn"
	log "ngrok/log"
	"ngrok/msg"
	"ngrok/server/dashboard"
	"ngrok/util"
)

const (
	registryCacheSize uint64        = 1024 * 1024 // 1 MB
	connReadTimeout   time.Duration = 10 * time.Second
)

// GLOBALS
var (
	tunnelRegistry  *TunnelRegistry
	controlRegistry *ControlRegistry

	// XXX: kill these global variables - they're only used in tunnel.go for constructing forwarding URLs
	opts      *Options
	listeners map[string]*conn.Listener

	// dash is the web management console; nil when -webAddr is empty.
	dash *dashboard.Dashboard
)

func NewProxy(pxyConn conn.Conn, regPxy *msg.RegProxy) {
	// fail gracefully if the proxy connection fails to register
	defer func() {
		if r := recover(); r != nil {
			pxyConn.Warn("Failed with error: %v", r)
			pxyConn.Close()
		}
	}()

	// set logging prefix
	pxyConn.SetType("pxy")

	// look up the control connection for this proxy
	pxyConn.Info("Registering new proxy for %s", regPxy.ClientId)
	ctl := controlRegistry.Get(regPxy.ClientId)

	if ctl == nil {
		panic("No client found for identifier: " + regPxy.ClientId)
	}

	// For dashboard-managed tunnels, enforce single-use token and HMAC signature
	// to prevent proxy connection spoofing/hijacking
	if ctl.dashTunnelID != "" && dash != nil {
		tun := dash.Store().TunnelByID(ctl.dashTunnelID)
		if tun == nil || !ctl.VerifyAndConsumeProxyToken(regPxy.Token, regPxy.Sig, tun.Key) {
			pxyConn.Error("Rejected unauthorized proxy connection for %s: invalid or expired proxy token/sig", regPxy.ClientId)
			pxyConn.Close()
			return
		}
	}

	ctl.RegisterProxy(pxyConn)
}

// Listen for incoming control and proxy connections
// We listen for incoming control and proxy connections on the same port
// for ease of deployment. The hope is that by running on port 443, using
// TLS and running all connections over the same port, we can bust through
// restrictive firewalls.
func tunnelListener(addr string, tlsConfig *tls.Config) {
	// listen for incoming connections
	listener, err := conn.Listen(addr, "tun", tlsConfig)
	if err != nil {
		panic(err)
	}

	log.Info("Listening for control and proxy connections on %s", listener.Addr.String())
	for c := range listener.Conns {
		go func(tunnelConn conn.Conn) {
			// don't crash on panics
			defer func() {
				if r := recover(); r != nil {
					tunnelConn.Info("tunnelListener failed with error %v: %s", r, debug.Stack())
				}
			}()

			tunnelConn.SetReadDeadline(time.Now().Add(connReadTimeout))
			var rawMsg msg.Message
			if rawMsg, err = msg.ReadMsg(tunnelConn); err != nil {
				tunnelConn.Warn("Failed to read message: %v", err)
				tunnelConn.Close()
				return
			}

			// don't timeout after the initial read, tunnel heartbeating will kill
			// dead connections
			tunnelConn.SetReadDeadline(time.Time{})

			switch m := rawMsg.(type) {
			case *msg.Auth:
				NewControl(tunnelConn, m)

			case *msg.RegProxy:
				NewProxy(tunnelConn, m)

			default:
				tunnelConn.Close()
			}
		}(c)
	}
}

func Main() {
	// parse options
	opts = parseArgs()

	// init logging
	log.LogTo(opts.logto, opts.loglevel)

	// seed random number generator
	seed, err := util.RandomSeed()
	if err != nil {
		panic(err)
	}
	rand.Seed(seed)

	// init tunnel/control registry
	registryCacheFile := os.Getenv("REGISTRY_CACHE_FILE")
	if registryCacheFile != "" {
		// confine the affinity-cache file to the working directory: only
		// the final path element of the operator-provided value is kept,
		// so the registry can never write outside of it
		registryCacheFile = filepath.Join(".", filepath.Base(registryCacheFile))
	}
	tunnelRegistry = NewTunnelRegistry(registryCacheSize, registryCacheFile)
	controlRegistry = NewControlRegistry()

	// web management console (users, tunnels, live config push)
	if opts.webAddr != "" {
		if err := startDashboard(); err != nil {
			panic(err)
		}
	}

	// start listeners
	listeners = make(map[string]*conn.Listener)

	// load tls configuration
	tlsConfig, err := LoadTLSConfig(opts.tlsCrt, opts.tlsKey)
	if err != nil {
		panic(err)
	}

	// listen for http
	if opts.httpAddr != "" {
		listeners["http"] = startHttpListener(opts.httpAddr, nil)
	}

	// listen for https
	if opts.httpsAddr != "" {
		listeners["https"] = startHttpListener(opts.httpsAddr, tlsConfig)
	}

	// ngrok clients
	tunnelListener(opts.tunnelAddr, tlsConfig)
}

// startDashboard boots the management console and serves it on opts.webAddr.
func startDashboard() error {
	tunnelClientAddr := opts.tunnelAddr
	if _, port, err := net.SplitHostPort(opts.tunnelAddr); err == nil && strings.HasPrefix(opts.tunnelAddr, ":") {
		tunnelClientAddr = fmt.Sprintf("%s:%s", opts.domain, port)
	}

	isHttps := opts.webTlsCrt != "" && opts.webTlsKey != ""
	d, err := dashboard.New(dashboard.Options{
		Domain:       opts.domain,
		TunnelAddr:   tunnelClientAddr,
		DataPath:     opts.webData,
		DlDir:        opts.dlDir,
		AdminPass:    opts.webAdminPass,
		SecureCookie: isHttps,
	})
	if err != nil {
		return err
	}

	// reach live control connections through the registry (managed clients
	// authenticate with ClientId "tun-<tunnelId>")
	d.SetControlLookup(func(tunnelID string) (dashboard.ControlConn, bool) {
		ctl := controlRegistry.Get(managedClientId(tunnelID))
		if ctl == nil {
			// return a nil interface (not a typed-nil *Control) so callers
			// can rely on the nil check
			return nil, false
		}
		return ctl, true
	})

	dash = d

	if username, password, created := d.Bootstrap(); created {
		msg := fmt.Sprintf("oneNat dashboard: created initial admin account %q with password %q (change it after first login)", username, password)
		log.Warn(msg)
		fmt.Println("=============================================")
		fmt.Println("  oneNat 管理后台初始管理员: " + username)
		fmt.Println("  初始密码: " + password)
		fmt.Println("  (仅首次创建时打印, 请登录后尽快修改)")
		fmt.Println("=============================================")
	}

	log.Info("Starting web management console on %s (data: %s, https: %v)", opts.webAddr, opts.webData, isHttps)
	ln, err := net.Listen("tcp", opts.webAddr)
	if err != nil {
		return fmt.Errorf("webAddr %s: %v", opts.webAddr, err)
	}
	if isHttps {
		webTlsCfg, terr := LoadTLSConfig(opts.webTlsCrt, opts.webTlsKey)
		if terr != nil {
			return fmt.Errorf("web TLS config error: %v", terr)
		}
		ln = tls.NewListener(ln, webTlsCfg)
	}
	go func() {
		srv := &http.Server{
			Addr:              opts.webAddr,
			Handler:           d.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       60 * time.Second,
			IdleTimeout:       120 * time.Second,
			// no WriteTimeout: /dl/ serves client binaries to slow links
		}
		if err := srv.Serve(ln); err != nil {
			panic(err)
		}
	}()
	return nil
}

// managedClientId is the stable control-registry id for a managed tunnel.
func managedClientId(tunnelID string) string {
	return "tun-" + tunnelID
}
