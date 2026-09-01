package client

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Zero-config agent mode:
//
//	ngrok agent [target] [-server host:port] [-new-key] [-remote-port N] [-web-port P]
//
// target     "192.168.1.20"        -> forward SSH(22) of that host + HTTP(80) of that host
//            "192.168.1.20:2222"   -> forward SSH(2222) of that host + HTTP(80) of that host
//            ""                    -> this machine (127.0.0.1:22 / :80)
// No configuration file is read. The stable per-machine key in
// ~/.ngrok.d/machine.key is created on first run and serves as
// server authtoken + gateway code + the secret in the manual.
const (
	agentDefaultSSH = 22
	agentDefaultWeb = 80
)

// agentTarget is the parsed "agent [target]" argument.
type agentTarget struct {
	host string // IP/host to forward to ("127.0.0.1" for local)
	ssh  int    // remote sshd port on that host
	web  int    // remote http port on that host
}

func parseAgentTarget(arg string) (*agentTarget, error) {
	t := &agentTarget{host: "127.0.0.1", ssh: agentDefaultSSH, web: agentDefaultWeb}
	if arg == "" {
		return t, nil
	}
	host, port := arg, 0
	if idx := strings.LastIndex(arg, ":"); idx >= 0 {
		host = arg[:idx]
		p, err := strconv.Atoi(arg[idx+1:])
		if err != nil || p <= 0 || p > 65535 {
			return nil, fmt.Errorf("Invalid port in target %q", arg)
		}
		port = p
	}
	if host == "" {
		return nil, fmt.Errorf("Invalid target host in %q", arg)
	}
	t.host = host
	if port != 0 {
		t.ssh = port
	}
	return t, nil
}

// buildAgentConfig assembles the full zero-config Configuration for agent
// mode. No file is read; everything comes from defaults, flags and the
// machine key.
func buildAgentConfig(opts *Options) (*Configuration, error) {
	target, err := parseAgentTarget(strings.Join(opts.args, " "))
	if err != nil {
		return nil, err
	}

	serverAddr := opts.server
	if serverAddr == "" {
		serverAddr = os.Getenv("NGROK_SERVER")
	}
	if serverAddr == "" {
		return nil, fmt.Errorf(
			"ngrok agent needs a server: pass -server=host:port or set NGROK_SERVER\n" +
				"  example: ngrok agent -server=tunnel.example.com:4443 192.168.1.20")
	}
	// require an explicit host:port and remember the host: every entrypoint
	// advertised in the manual is rewritten to this host so bare-IP servers
	// work even when ngrokd's -domain is something else.
	serverHost, _, err := net.SplitHostPort(serverAddr)
	if err != nil || serverHost == "" {
		return nil, fmt.Errorf("Invalid -server %q: must be host:port", serverAddr)
	}

	key, created, err := LoadOrCreateMachineKey()
	if err != nil {
		return nil, fmt.Errorf("Failed to load machine key: %v", err)
	}

	if opts.newKey {
		newKey, oldKey, rerr := RotateMachineKey()
		if rerr != nil {
			return nil, fmt.Errorf("Failed to rotate machine key: %v", rerr)
		}
		fmt.Printf("Machine key rotated.\n  old: %s (revoked)\n  new: %s\n", oldKey, newKey)
		key = newKey
		created = true
	}

	webPort := target.web
	if opts.webPort != 0 {
		webPort = opts.webPort
	}

	cfg := &Configuration{
		ServerAddr:         serverAddr,
		TrustHostRootCerts: true,
		AuthToken:          key,
		InspectAddr:        "disabled",
		Gate:               "plain",
		GateToken:          key, // fixed gateway code == machine key
		ManualPath:         defaultAgentDir(),
		SshUser:            "", // unknown cross-machine; manual says "your login user"
		SshPort:            target.ssh,
		MachineDesc:        fmt.Sprintf("Remote host %s (SSH :%d, Web :%d). Operator provides OS details on request.", target.host, target.ssh, webPort),
		Rules: []string{
			"Use SSH only for inspection and approved changes; confirm before any service restart",
			"Never reboot/poweroff the machine",
		},
		LogTo:   opts.logto,
		GateTTL: 0, // fixed key, no rotation
		// zero-config: nothing that SaveAuthToken could rewrite
		Path: "",
	}
	cfg.Tunnels = map[string]*TunnelConfiguration{
		"agent-ssh": {
			Hostname:   opts.hostname,
			RemotePort: uint16(opts.remotePort),
			Protocols:  map[string]string{"tcp": fmt.Sprintf("%s:%d", target.host, target.ssh)},
		},
		"agent-web": {
			Subdomain: opts.subdomain,
			Hostname:  opts.hostname,
			HttpAuth:  opts.httpauth,
			Protocols: map[string]string{"http": fmt.Sprintf("%s:%d", target.host, webPort)},
		},
	}

	// When the server is reached by bare IP, request the web tunnel's
	// virtual hostname to be that IP so Host-header routing matches
	// direct IP access (works when ngrokd serves http on the standard :80).
	if ip := net.ParseIP(serverHost); ip != nil {
		cfg.Tunnels["agent-web"].Hostname = serverHost
	}

	if created {
		fmt.Printf("First run: machine key generated at %s (keep it stable; rotate with -new-key)\n", machineKeyPath())
	}
	return cfg, nil
}

// RewriteURLHost replaces the host (keeping scheme and port) of a tunnel
// URL, e.g. tcp://ngrok.com:49769 -> tcp://203.0.113.7:49769.
func RewriteURLHost(publicUrl, newHost string) string {
	scheme := ""
	rest := publicUrl
	if i := strings.Index(publicUrl, "://"); i >= 0 {
		scheme = publicUrl[:i+3]
		rest = publicUrl[i+3:]
	}
	hostPort := rest
	if k := strings.Index(hostPort, "/"); k >= 0 {
		hostPort = hostPort[:k]
	}
	port := ""
	if j := strings.LastIndex(hostPort, ":"); j >= 0 {
		p := hostPort[j+1:]
		numeric := p != ""
		for _, c := range p {
			if c < '0' || c > '9' {
				numeric = false
				break
			}
		}
		if numeric {
			port = ":" + p
		}
	}
	return scheme + newHost + port
}
