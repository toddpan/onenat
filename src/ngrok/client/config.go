package client

import (
	"fmt"
	"gopkg.in/yaml.v1"
	"io/ioutil"
	"net"
	"net/url"
	"ngrok/log"
	"os"
	"os/user"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Configuration struct {
	HttpProxy          string                          `yaml:"http_proxy,omitempty"`
	ServerAddr         string                          `yaml:"server_addr,omitempty"`
	InspectAddr        string                          `yaml:"inspect_addr,omitempty"`
	TrustHostRootCerts bool                            `yaml:"trust_host_root_certs,omitempty"`
	AuthToken          string                          `yaml:"auth_token,omitempty"`
	Tunnels            map[string]*TunnelConfiguration `yaml:"tunnels,omitempty"`
	LogTo              string                          `yaml:"-"`
	// Managed mode: the dashboard pushes the tunnel set over the control
	// channel; nothing below but server/token matters locally.
	Managed  bool   `yaml:"-"`
	TunnelID string `yaml:"tunnel_id,omitempty"`

	// AI remote-agent support
	// Gate enables the client-side access gateway on tcp tunnels:
	// "plain" requires "AUTH <code>" as the first line, "off" (default)
	// keeps classic transparent tunneling.
	Gate string `yaml:"gate,omitempty"`
	// GateToken pins a static access code instead of auto-generated
	// rotating ones. When set, CodeTTL is ignored.
	GateToken string `yaml:"gate_token,omitempty"`
	// CodeTTL is how often the access code rotates (e.g. "30m").
	// Empty or "0" means the code is valid for the whole session.
	CodeTTL string `yaml:"code_ttl,omitempty"`
	// ManualPath is the directory where remote-manual.md/.json and the
	// connection card are written (default: ~/.ngrok.d).
	ManualPath string `yaml:"manual_path,omitempty"`
	// SshUser is advertised in the generated manual for AI agents.
	SshUser string `yaml:"ssh_user,omitempty"`
	// SshPort is the local sshd port advertised in the manual (default 22).
	SshPort int `yaml:"ssh_port,omitempty"`
	// MachineDesc tells the AI agent what this machine is for.
	MachineDesc string `yaml:"machine_desc,omitempty"`
	// Rules are behavioral constraints for the AI agent, embedded verbatim
	// in the manual.
	Rules []string `yaml:"rules,omitempty"`

	// GateTTL is the parsed duration of CodeTTL (not user-settable directly).
	GateTTL time.Duration `yaml:"-"`
	// Path is the config file path (not user-settable).
	Path string `yaml:"-"`
}

type TunnelConfiguration struct {
	Subdomain  string            `yaml:"subdomain,omitempty"`
	Hostname   string            `yaml:"hostname,omitempty"`
	Protocols  map[string]string `yaml:"proto,omitempty"`
	HttpAuth   string            `yaml:"auth,omitempty"`
	RemotePort uint16            `yaml:"remote_port,omitempty"`
}

func LoadConfiguration(opts *Options) (config *Configuration, err error) {
	// zero-config agent mode: no configuration file is involved at all
	if opts.command == "agent" {
		return buildAgentConfig(opts)
	}

	configPath := opts.config
	if configPath == "" {
		configPath = defaultPath()
	}

	log.Info("Reading configuration file %s", configPath)
	configBuf, err := ioutil.ReadFile(configPath)
	if err != nil {
		// failure to read a configuration file is only a fatal error if
		// the user specified one explicitly
		if opts.config != "" {
			err = fmt.Errorf("Failed to read configuration file %s: %v", configPath, err)
			return
		}
	}

	// deserialize/parse the config
	config = new(Configuration)
	if err = yaml.Unmarshal(configBuf, &config); err != nil {
		err = fmt.Errorf("Error parsing configuration file %s: %v", configPath, err)
		return
	}

	// try to parse the old .ngrok format for backwards compatibility
	matched := false
	content := strings.TrimSpace(string(configBuf))
	if matched, err = regexp.MatchString("^[0-9a-zA-Z_\\-!]+$", content); err != nil {
		return
	} else if matched {
		config = &Configuration{AuthToken: content}
	}

	// set configuration defaults
	if config.ServerAddr == "" {
		config.ServerAddr = defaultServerAddr
	}

	// dashboard-managed clients run headless
	if opts.command == "managed" && config.InspectAddr == "" {
		config.InspectAddr = "disabled"
	}

	if config.InspectAddr == "" {
		config.InspectAddr = defaultInspectAddr
	}

	if config.HttpProxy == "" {
		config.HttpProxy = os.Getenv("http_proxy")
	}

	// validate and normalize configuration
	if config.InspectAddr != "disabled" {
		if config.InspectAddr, err = normalizeAddress(config.InspectAddr, "inspect_addr"); err != nil {
			return
		}
	}

	if config.ServerAddr, err = normalizeAddress(config.ServerAddr, "server_addr"); err != nil {
		return
	}

	if config.HttpProxy != "" {
		var proxyUrl *url.URL
		if proxyUrl, err = url.Parse(config.HttpProxy); err != nil {
			return
		} else {
			if proxyUrl.Scheme != "http" && proxyUrl.Scheme != "https" {
				err = fmt.Errorf("Proxy url scheme must be 'http' or 'https', got %v", proxyUrl.Scheme)
				return
			}
		}
	}

	for name, t := range config.Tunnels {
		if t == nil || t.Protocols == nil || len(t.Protocols) == 0 {
			err = fmt.Errorf("Tunnel %s does not specify any protocols to tunnel.", name)
			return
		}

		for k, addr := range t.Protocols {
			tunnelName := fmt.Sprintf("for tunnel %s[%s]", name, k)
			if t.Protocols[k], err = normalizeAddress(addr, tunnelName); err != nil {
				return
			}

			if err = validateProtocol(k, tunnelName); err != nil {
				return
			}
		}

		// use the name of the tunnel as the subdomain if none is specified
		if t.Hostname == "" && t.Subdomain == "" {
			// XXX: a crude heuristic, really we should be checking if the last part
			// is a TLD
			if len(strings.Split(name, ".")) > 1 {
				t.Hostname = name
			} else {
				t.Subdomain = name
			}
		}
	}

	// override configuration with command-line options
	config.LogTo = opts.logto
	config.Path = configPath
	if opts.config != "" || configHasContent(config) {
		// config-file mode only when the user gave a file or the default
		// file exists with content; agent mode skips this entirely
		if opts.authtoken != "" {
			config.AuthToken = opts.authtoken
		}
	}
	if opts.gate != "" {
		config.Gate = opts.gate
	}

	// parse gate settings
	switch config.Gate {
	case "":
		config.Gate = "off"
	case "plain", "off":
	default:
		err = fmt.Errorf("Invalid gate mode %q: must be plain or off", config.Gate)
		return
	}

	if config.GateToken != "" && config.CodeTTL != "" {
		err = fmt.Errorf("gate_token and code_ttl are mutually exclusive")
		return
	}

	if config.CodeTTL != "" && config.CodeTTL != "0" {
		if config.GateTTL, err = time.ParseDuration(config.CodeTTL); err != nil {
			err = fmt.Errorf("Invalid code_ttl %q: %v", config.CodeTTL, err)
			return
		}
		if config.GateTTL < 30*time.Second {
			err = fmt.Errorf("code_ttl must be at least 30s")
			return
		}
	}

	if config.SshPort == 0 {
		config.SshPort = 22
	}

	switch opts.command {
	// dashboard-managed client: the server pushes the tunnel set via
	// ConfigSync after authentication; the local file only carries
	// server_addr + auth_token (+ tunnel_id metadata)
	case "managed":
		if config.ServerAddr == "" || config.AuthToken == "" {
			err = fmt.Errorf("managed 模式配置必须包含 server_addr 与 auth_token " +
				"(由管理后台一键安装脚本自动生成)")
			return
		}
		config.Managed = true
		config.Tunnels = make(map[string]*TunnelConfiguration)

	// start a single tunnel, the default, simple ngrok behavior
	case "default":
		config.Tunnels = make(map[string]*TunnelConfiguration)
		config.Tunnels["default"] = &TunnelConfiguration{
			Subdomain: opts.subdomain,
			Hostname:  opts.hostname,
			HttpAuth:  opts.httpauth,
			Protocols: make(map[string]string),
		}

		for _, proto := range strings.Split(opts.protocol, "+") {
			if err = validateProtocol(proto, "default"); err != nil {
				return
			}

			if config.Tunnels["default"].Protocols[proto], err = normalizeAddress(opts.args[0], ""); err != nil {
				return
			}
		}

	// start a single tunnel for AI remote agents, with access gateway
	// and a generated machine-readable remote access manual.
	// (With no arguments this is now handled zero-config in buildAgentConfig
	// before any config file is read; reaching this branch means the user
	// passed -config explicitly.)
	case "agent":
		if len(opts.args) != 1 {
			err = fmt.Errorf("Usage: ngrok agent <target-host[:ssh-port]>")
			return
		}

		port, perr := strconv.Atoi(opts.args[0])
		if perr != nil || port <= 0 || port > 65535 {
			err = fmt.Errorf("Invalid local ssh port: %s", opts.args[0])
			return
		}

		if config.Gate == "off" {
			config.Gate = "plain"
		}

		config.Tunnels = make(map[string]*TunnelConfiguration)
		config.Tunnels["agent"] = &TunnelConfiguration{
			Subdomain:  opts.subdomain,
			Hostname:   opts.hostname,
			HttpAuth:   opts.httpauth,
			RemotePort: uint16(opts.remotePort),
			Protocols:  map[string]string{"tcp": fmt.Sprintf("127.0.0.1:%d", port)},
		}

	// list tunnels
	case "list":
		for name, _ := range config.Tunnels {
			fmt.Println(name)
		}
		os.Exit(0)

	// start tunnels
	case "start":
		if len(opts.args) == 0 {
			err = fmt.Errorf("You must specify at least one tunnel to start")
			return
		}

		requestedTunnels := make(map[string]bool)
		for _, arg := range opts.args {
			requestedTunnels[arg] = true

			if _, ok := config.Tunnels[arg]; !ok {
				err = fmt.Errorf("Requested to start tunnel %s which is not defined in the config file.", arg)
				return
			}
		}

		for name, _ := range config.Tunnels {
			if !requestedTunnels[name] {
				delete(config.Tunnels, name)
			}
		}

	case "start-all":
		return

	default:
		err = fmt.Errorf("Unknown command: %s", opts.command)
		return
	}

	return
}

func defaultPath() string {
	user, err := user.Current()

	// user.Current() does not work on linux when cross compiling because
	// it requires CGO; use os.Getenv("HOME") hack until we compile natively
	homeDir := os.Getenv("HOME")
	if err != nil {
		log.Warn("Failed to get user's home directory: %s. Using $HOME: %s", err.Error(), homeDir)
	} else {
		homeDir = user.HomeDir
	}

	return path.Join(homeDir, ".ngrok")
}

// configHasContent reports whether a parsed default config file actually
// carried any settings (used to decide whether to treat $HOME/.ngrok as a
// real config or ignore it for zero-config agent mode).
func configHasContent(c *Configuration) bool {
	return c.ServerAddr != "" || c.AuthToken != "" || len(c.Tunnels) > 0 ||
		c.HttpProxy != "" || c.InspectAddr != ""
}

func normalizeAddress(addr string, propName string) (string, error) {
	// normalize port to address
	if _, err := strconv.Atoi(addr); err == nil {
		addr = ":" + addr
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("Invalid address %s '%s': %s", propName, addr, err.Error())
	}

	if host == "" {
		host = "127.0.0.1"
	}

	return fmt.Sprintf("%s:%s", host, port), nil
}

func validateProtocol(proto, propName string) (err error) {
	switch proto {
	case "http", "https", "http+https", "tcp":
	default:
		err = fmt.Errorf("Invalid protocol for %s: %s", propName, proto)
	}

	return
}

func SaveAuthToken(configPath, authtoken string) (err error) {
	// empty configuration by default for the case that we can't read it
	c := new(Configuration)

	// read the configuration
	oldConfigBytes, err := ioutil.ReadFile(configPath)
	if err == nil {
		// unmarshal if we successfully read the configuration file
		if err = yaml.Unmarshal(oldConfigBytes, c); err != nil {
			return
		}
	}

	// no need to save, the authtoken is already the correct value
	if c.AuthToken == authtoken {
		return
	}

	// update auth token
	c.AuthToken = authtoken

	// rewrite configuration
	newConfigBytes, err := yaml.Marshal(c)
	if err != nil {
		return
	}

	err = ioutil.WriteFile(configPath, newConfigBytes, 0600)
	return
}
