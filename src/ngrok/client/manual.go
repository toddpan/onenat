package client

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"
)

// defaultAgentDir is the per-machine agent state directory
// (machine key, manuals). Not a user-written config file.
func defaultAgentDir() string {
	home := os.Getenv("HOME")
	return path.Join(home, ".ngrok.d")
}

func manualDir(config *Configuration) string {
	if config.ManualPath != "" {
		return config.ManualPath
	}
	return defaultAgentDir()
}

// agentService is one tunneled service advertised in the manual/card.
type agentService struct {
	Kind      string `json:"kind"`       // "ssh" | "web"
	PublicUrl string `json:"public_url"` // tcp://host:port or http://host:port
	Gated     bool   `json:"gated"`      // true = first line must be AUTH <key>
	LocalAddr string `json:"local_addr"` // where it forwards to on the target side
}

// ConnectionCard is the terminal summary printed after agent login.
type ConnectionCard struct {
	Services   []agentService `json:"services"`
	Key        string         `json:"key"`
	KeyPath    string         `json:"key_path"`
	ManualPath string         `json:"manual_path"`
	LocalAddr  string         `json:"local_addr"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// Manual is the machine-readable remote access manual: a complete,
// self-contained instruction set that lets a remote AI agent reach the
// exposed services of the target machine.
type Manual struct {
	GeneratedAt time.Time `json:"generated_at"`
	Key         struct {
		Value    string `json:"value"`
		Path     string `json:"path"`
		Stable   bool   `json:"stable"`
		UsedFor  string `json:"used_for"`
		HowToUse string `json:"how_to_use"`
	} `json:"key"`
	Services []agentService `json:"services"`
	Gate     struct {
		Mode         string `json:"mode"`
		Handshake    string `json:"handshake"`
		FailBehavior string `json:"fail_behavior"`
	} `json:"gate"`
	Ssh struct {
		Host               string `json:"host"`
		Port               int    `json:"port"`
		User               string `json:"user"`
		Auth               string `json:"auth"`
		HostKeyAlgorithm   string `json:"host_key_algorithm,omitempty"`
		HostKeyFingerprint string `json:"host_key_fingerprint,omitempty"`
		SetupHint          string `json:"setup_hint,omitempty"`
	} `json:"ssh"`
	Web struct {
		Host string `json:"host"`
		Port int    `json:"port"`
		Note string `json:"note,omitempty"`
	} `json:"web"`
	Machine struct {
		Os   string `json:"os"`
		Desc string `json:"desc,omitempty"`
	} `json:"machine"`
	Commands struct {
		SshShell   string `json:"ssh_shell"`
		SshOneshot string `json:"ssh_oneshot"`
		ScpUp      string `json:"scp_up"`
		ScpDown    string `json:"scp_down"`
		SshSmoke   string `json:"ssh_smoke_test"`
		WebCurl    string `json:"web_curl"`
	} `json:"commands"`
	Rules        []string          `json:"rules,omitempty"`
	Troubleshoot map[string]string `json:"troubleshoot"`
}

// classifyAgentServices splits the currently established tunnels into the
// agent's service list (ssh = gated tcp, web = plain http).
func classifyAgentServices(tunnels map[string]struct {
	PublicUrl string
	ProtoName string
	LocalAddr string
}) []agentService {
	kinds := make([]agentService, 0, 2)
	keys := make([]string, 0, len(tunnels))
	for k := range tunnels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, url := range keys {
		t := tunnels[url]
		switch t.ProtoName {
		case "tcp":
			kinds = append(kinds, agentService{Kind: "ssh", PublicUrl: t.PublicUrl, Gated: true, LocalAddr: t.LocalAddr})
		case "http":
			kinds = append(kinds, agentService{Kind: "web", PublicUrl: t.PublicUrl, Gated: false, LocalAddr: t.LocalAddr})
		}
	}
	return kinds
}

// sshHostKeyFingerprint probes the sshd of host:port for its public host key
// fingerprint. Best effort: empty strings when sshd is unreachable.
func sshHostKeyFingerprint(host string, port int) (algo, fp string) {
	out, err := exec.Command("ssh-keyscan", "-p", fmt.Sprint(port), host).Output()
	if err != nil {
		return "", ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		keyIn := strings.Join(f[:3], " ")
		fpOut, ferr := exec.Command("sh", "-c",
			fmt.Sprintf("echo %q | ssh-keygen -lf -", keyIn)).Output()
		if ferr == nil {
			fields := strings.Fields(string(fpOut))
			if len(fields) >= 2 {
				return f[2], fields[1]
			}
		}
	}
	return "", ""
}

// sshSetupHint returns macOS-specific guidance when the (local) sshd is off.
func sshSetupHint(host string) string {
	if host != "127.0.0.1" && host != "localhost" {
		return ""
	}
	if _, err := os.Stat("/System/Library/LaunchDaemons/ssh.plist"); err == nil {
		out, err := exec.Command("launchctl", "list", "com.openssh.sshd").Output()
		if err != nil || len(out) == 0 {
			return "Remote Login appears DISABLED on this Mac. Run: " +
				"sudo systemsetup -setremotelogin on  (or System Settings > General > Sharing > Remote Login)"
		}
	}
	return ""
}

// GenerateManual renders remote-manual.md and remote-manual.json for the
// current set of agent services and returns the connection card.
func GenerateManual(config *Configuration, services []agentService) (*ConnectionCard, error) {
	if len(services) == 0 {
		return nil, fmt.Errorf("no agent services established yet")
	}
	key := config.GateToken
	if key == "" {
		key = config.AuthToken
	}

	dir := manualDir(config)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	// find ssh and web services
	var sshSvc, webSvc *agentService
	for i := range services {
		switch services[i].Kind {
		case "ssh":
			sshSvc = &services[i]
		case "web":
			webSvc = &services[i]
		}
	}

	gateHost, gatePort := "", 0
	if sshSvc != nil {
		u := strings.TrimPrefix(sshSvc.PublicUrl, "tcp://")
		idx := strings.LastIndex(u, ":")
		gateHost, gatePort = u[:idx], atoiOr(u[idx+1:], 0)
	}

	var m Manual
	m.GeneratedAt = time.Now()
	m.Key.Value = key
	m.Key.Path = machineKeyPath()
	m.Key.Stable = true
	m.Key.UsedFor = "1) client authtoken to ngrokd  2) access-gateway code on the SSH tunnel  3) the single secret AI agents need"
	m.Key.HowToUse = `Prefix every SSH TCP connection with one line: AUTH <key>. The key does NOT protect the web URL (web is intentionally public).`
	m.Services = services
	m.Gate.Mode = "plain"
	m.Gate.Handshake = `First line of every TCP connection to the SSH entrypoint must be exactly: AUTH <key> (CR/LF terminated). After acceptance the connection is transparent SSH. You may pipeline the SSH banner with the AUTH line in one segment.`
	m.Gate.FailBehavior = `Wrong/missing code: connection rejected with "ERR ngrok-gate: ..." and closed. 5 consecutive failures rate-limit the tunnel for 60s.`

	machineSSHHost := "127.0.0.1"
	sshDestName := "localhost"
	webHost := ""
	if config.MachineDesc != "" {
		// extract "Remote host X (...)" declared by agent mode
		if i := strings.Index(config.MachineDesc, "Remote host "); i >= 0 {
			rest := config.MachineDesc[i+len("Remote host "):]
			if j := strings.Index(rest, " "); j > 0 {
				machineSSHHost = rest[:j]
			}
		}
	}
	if machineSSHHost != "127.0.0.1" {
		sshDestName = machineSSHHost
		webHost = machineSSHHost
	}

	if sshSvc != nil {
		m.Ssh.Host = machineSSHHost
		m.Ssh.Port = config.SshPort
		m.Ssh.User = config.SshUser
		if m.Ssh.User == "" {
			m.Ssh.User = "USER"
		}
		m.Ssh.Auth = "publickey (recommended) or password configured on the target machine"
		m.Ssh.HostKeyAlgorithm, m.Ssh.HostKeyFingerprint = sshHostKeyFingerprint(machineSSHHost, config.SshPort)
		m.Ssh.SetupHint = sshSetupHint(machineSSHHost)
	}
	if webSvc != nil {
		m.Web.Host = webHost
		m.Web.Port = atoiOr(webSvc.LocalAddr[strings.LastIndex(webSvc.LocalAddr, ":")+1:], agentDefaultWeb)
		m.Web.Note = "The web entrypoint is intentionally public: no key needed, just open/curl the URL."
	}

	m.Machine.Os = hostOs()
	m.Machine.Desc = config.MachineDesc

	userAt := m.Ssh.User + "@" + sshDestName
	if sshSvc != nil && gatePort != 0 {
		proxyFmt := fmt.Sprintf(`'{ printf "AUTH %s\r\n"; cat; } | nc %s %d'`, key, gateHost, gatePort)
		baseOpts := fmt.Sprintf(`-o ProxyCommand=%s -o StrictHostKeyChecking=accept-new`, proxyFmt)
		m.Commands.SshShell = fmt.Sprintf(`ssh %s %s`, baseOpts, userAt)
		m.Commands.SshOneshot = fmt.Sprintf(`ssh %s %s 'uname -a && df -h'`, baseOpts, userAt)
		m.Commands.ScpUp = fmt.Sprintf(`scp %s LOCALFILE %s:/tmp/`, baseOpts, userAt)
		m.Commands.ScpDown = fmt.Sprintf(`scp %s %s:/path/remote/file LOCALDIR/`, baseOpts, userAt)
		m.Commands.SshSmoke = fmt.Sprintf(`(printf "AUTH %s\r\n"; sleep 1) | nc %s %d   # expect an SSH banner back`, key, gateHost, gatePort)
	}
	if webSvc != nil {
		m.Commands.WebCurl = fmt.Sprintf(`curl -s %s   # public, no key`, webSvc.PublicUrl)
	}

	m.Rules = config.Rules
	m.Troubleshoot = map[string]string{
		"connection closed immediately (ssh)": "machine key rotated (operator ran -new-key) - get the fresh manual",
		"ERR ngrok-gate: rate limited":        "too many wrong attempts - wait 60s and retry with the correct key",
		"ERR ngrok-gate: bad request":         "first line was not 'AUTH <key>' - check quoting of the ProxyCommand printf",
		"kex/banner timeout (ssh)":            "gate passed but sshd did not answer - is sshd running on the target machine?",
		"web URL 404/timeout":                 "the web service on the target is down or the port changed - ask the operator",
		"host key mismatch":                   "the machine was reinstalled; verify the new fingerprint out-of-band, then delete the stale line from ~/.ssh/known_hosts",
	}

	card := &ConnectionCard{
		Services:   services,
		Key:        key,
		KeyPath:    machineKeyPath(),
		ManualPath: dir,
		UpdatedAt:  time.Now(),
	}
	if len(services) == 1 {
		card.LocalAddr = services[0].LocalAddr
	}

	// write json
	j, err := json.MarshalIndent(&m, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = ioutil.WriteFile(path.Join(dir, "remote-manual.json"), j, 0600); err != nil {
		return nil, err
	}

	// write markdown
	if err = ioutil.WriteFile(path.Join(dir, "remote-manual.md"), []byte(renderManualMarkdown(&m)), 0600); err != nil {
		return nil, err
	}

	return card, nil
}

func atoiOr(s string, def int) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if len(s) == 0 {
		return def
	}
	return n
}

func hostOs() string {
	if out, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
		return "macOS " + strings.TrimSpace(string(out))
	}
	if b, err := ioutil.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
			}
		}
	}
	return "unknown"
}

// FormatCard renders the TeamViewer-style connection card for the terminal.
func FormatCard(c *ConnectionCard) string {
	var b strings.Builder
	b.WriteString("\n┌─────────────────────────────────────────────────────────────┐\n")
	fmt.Fprintf(&b, "│  ngrok agent ONLINE   %s\n", c.UpdatedAt.Format("15:04:05"))
	for _, s := range c.Services {
		switch s.Kind {
		case "ssh":
			fmt.Fprintf(&b, "│  ssh  ➜  %s   (需要密钥)\n", s.PublicUrl)
		case "web":
			fmt.Fprintf(&b, "│  web  ➜  %s   (公开直连)\n", s.PublicUrl)
		}
	}
	fmt.Fprintf(&b, "│  密钥 KEY    %s\n", c.Key)
	fmt.Fprintf(&b, "│  密钥文件    %s  (机器唯一,不变; -new-key 刷新)\n", c.KeyPath)
	fmt.Fprintf(&b, "│  说明书      %s/remote-manual.{md,json}\n", c.ManualPath)
	b.WriteString("│  把 remote-manual.json 发给 AI，它照里面命令自行连接。\n")
	b.WriteString("│  Ctrl-C 整体下线。\n")
	b.WriteString("└─────────────────────────────────────────────────────────────┘\n")
	return b.String()
}

func renderManualMarkdown(m *Manual) string {
	var b strings.Builder
	b.WriteString("# Remote Access Manual (ngrok agent)\n\n")
	b.WriteString("This file is written for a remote AI agent that needs to reach the exposed\n")
	b.WriteString("services of the target machine. Everything below is self-contained: copy-paste\n")
	b.WriteString("the commands as-is.\n\n")

	b.WriteString("## 1. Access key (the only secret)\n\n")
	fmt.Fprintf(&b, "- Key: **`%s`**\n", m.Key.Value)
	fmt.Fprintf(&b, "- Stored at: `%s` (stable per machine; changes only when the operator runs `-new-key`)\n", m.Key.Path)
	fmt.Fprintf(&b, "- Used for: %s\n\n", m.Key.UsedFor)

	b.WriteString("## 2. Entrypoints\n\n")
	for _, s := range m.Services {
		switch s.Kind {
		case "ssh":
			fmt.Fprintf(&b, "- SSH: **`%s`** — key required (AUTH handshake), then plain SSH\n", s.PublicUrl)
		case "web":
			fmt.Fprintf(&b, "- Web: **`%s`** — public, no key, direct HTTP\n", s.PublicUrl)
		}
	}
	b.WriteString("\n")

	b.WriteString("## 3. Gateway handshake (SSH entrypoint only, every connection)\n\n")
	fmt.Fprintf(&b, "%s\n\n%s\n\n", m.Gate.Handshake, m.Gate.FailBehavior)
	fmt.Fprintf(&b, "%s\n\n", m.Key.HowToUse)

	b.WriteString("## 4. SSH connection\n\n")
	fmt.Fprintf(&b, "- Target: sshd on %s:%d\n", m.Ssh.Host, m.Ssh.Port)
	if m.Ssh.User != "USER" {
		fmt.Fprintf(&b, "- User: %s\n", m.Ssh.User)
	}
	fmt.Fprintf(&b, "- Auth: %s\n", m.Ssh.Auth)
	if m.Ssh.HostKeyFingerprint != "" {
		fmt.Fprintf(&b, "- Host key (%s): `%s`\n", m.Ssh.HostKeyAlgorithm, m.Ssh.HostKeyFingerprint)
	}
	if m.Ssh.SetupHint != "" {
		fmt.Fprintf(&b, "- NOTE: %s\n", m.Ssh.SetupHint)
	}

	b.WriteString("\n## 5. Ready-to-run commands\n\n")
	if m.Commands.SshShell != "" {
		fmt.Fprintf(&b, "Interactive shell:\n\n```bash\n%s\n```\n\n", m.Commands.SshShell)
		fmt.Fprintf(&b, "One-shot command:\n\n```bash\n%s\n```\n\n", m.Commands.SshOneshot)
		fmt.Fprintf(&b, "Upload a file:\n\n```bash\n%s\n```\n\n", m.Commands.ScpUp)
		fmt.Fprintf(&b, "Download a file:\n\n```bash\n%s\n```\n\n", m.Commands.ScpDown)
		fmt.Fprintf(&b, "SSH gate smoke test:\n\n```bash\n%s\n```\n\n", m.Commands.SshSmoke)
	}
	if m.Commands.WebCurl != "" {
		fmt.Fprintf(&b, "Web access:\n\n```bash\n%s\n```\n\n", m.Commands.WebCurl)
	}

	if len(m.Rules) > 0 {
		b.WriteString("## 6. Operating rules for the AI agent\n\n")
		for _, r := range m.Rules {
			fmt.Fprintf(&b, "- %s\n", r)
		}
		b.WriteString("\n")
	}

	b.WriteString("## 7. Troubleshooting\n\n")
	for k, v := range m.Troubleshoot {
		fmt.Fprintf(&b, "- **%s**: %s\n", k, v)
	}
	b.WriteString("\n")

	if m.Machine.Desc != "" {
		fmt.Fprintf(&b, "## 8. About the target machine\n\n%s\n", m.Machine.Desc)
	}

	return b.String()
}
