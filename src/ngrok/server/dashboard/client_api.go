package dashboard

// Session-authenticated, owner-scoped bootstrap for embedded managed clients.
// It does not grant write access to the public read-only onk API.
import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"ngrok/msg"
	"regexp"
	"strings"
	"time"
)

var embeddedClientID = regexp.MustCompile("^[a-zA-Z0-9_-]{16,80}$")

type clientBootstrapInput struct {
	ClientID   string `json:"client_id"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	LocalPort  int    `json:"local_port"`
	RemotePort int    `json:"remote_port"`
	Subdomain  string `json:"subdomain"`
	// ApiKey is the node-local DSH data credential. It is sealed into the
	// mapping's app auth and never echoed by bootstrap/status.
	ApiKey string `json:"api_key"`
}

func (s *Store) clientTunnel(owner, clientID string) *Tunnel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.data.Tunnels {
		if t.OwnerID == owner && t.ClientID == clientID {
			return t
		}
	}
	return nil
}

// ensureClientTunnel validates first and commits one store transaction. Display
// names are never used as identifiers, including when a plugin is restarted.
func (s *Store) ensureClientTunnel(owner string, in clientBootstrapInput) (*Tunnel, *Mapping, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !embeddedClientID.MatchString(in.ClientID) {
		return nil, nil, fmt.Errorf("client_id 必须为 16-80 位字母、数字、下划线或中划线")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 64 {
		return nil, nil, fmt.Errorf("名称必填且不超过 64 字符")
	}
	if in.Protocol == "" {
		in.Protocol = "https"
	}
	if in.Protocol != "https" && in.Protocol != "tcp" {
		return nil, nil, fmt.Errorf("客户端映射协议必须为 https 或 tcp")
	}
	if len(in.ApiKey) > 8192 || strings.ContainsAny(in.ApiKey, "\r\n\x00") {
		return nil, nil, fmt.Errorf("节点访问密钥无效")
	}
	var t *Tunnel
	for _, candidate := range s.data.Tunnels {
		if candidate.OwnerID == owner && candidate.ClientID == in.ClientID {
			t = candidate
			break
		}
	}
	if t != nil && t.Locked {
		return nil, nil, fmt.Errorf("隧道已锁定")
	}
	var m *Mapping
	if t != nil {
		for _, candidate := range t.Mappings {
			if candidate.ID == t.ClientMappingID {
				m = candidate
				break
			}
		}
	}
	if in.Protocol == "https" && in.Subdomain == "" {
		if m != nil && m.Subdomain != "" {
			in.Subdomain = m.Subdomain
		} else {
			sum := sha256.Sum256([]byte(owner + ":" + in.ClientID))
			in.Subdomain = "dsh-" + hex.EncodeToString(sum[:10])
		}
	}
	if in.Protocol == "https" && in.RemotePort != 0 {
		return nil, nil, fmt.Errorf("HTTPS 使用服务端固定共享端口，不能指定独立 remote_port")
	}
	if in.Protocol == "tcp" {
		in.Subdomain = ""
	}
	input := MappingInput{Proto: in.Protocol, LocalIP: "127.0.0.1", LocalPort: in.LocalPort, RemotePort: in.RemotePort, Subdomain: in.Subdomain}
	if err := s.validateMappingInput(&input, false); err != nil {
		return nil, nil, err
	}
	for _, other := range s.data.Tunnels {
		for _, existing := range other.Mappings {
			if m != nil && t.ID == other.ID && existing.ID == m.ID {
				continue
			}
			if input.Subdomain != "" && strings.EqualFold(existing.Subdomain, input.Subdomain) {
				return nil, nil, fmt.Errorf("子域名已被其他映射占用")
			}
			if input.Proto == "tcp" && input.RemotePort > 0 && existing.Proto == "tcp" && existing.RemotePort == input.RemotePort {
				return nil, nil, fmt.Errorf("固定公网端口已被其他映射配置占用")
			}
		}
	}
	now := time.Now()
	if t == nil {
		t = &Tunnel{ID: NewTunnelID(), Key: NewKey(), Name: in.Name, OwnerID: owner, CreatedAt: now, ClientID: in.ClientID, Mappings: []*Mapping{}}
		s.data.Tunnels = append(s.data.Tunnels, t)
	}
	t.Name = in.Name
	if m == nil {
		app := &App{ID: NewAppID(), Name: in.Name, Type: "http-api", OwnerID: owner, Auth: AppAuth{AuthType: "none"}, Tags: []string{"dsh-web-service", "embedded-client"}, CreatedAt: now, UpdatedAt: now}
		s.data.Apps = append(s.data.Apps, app)
		m = &Mapping{ID: NewMappingID(), AppID: app.ID}
		t.Mappings = append(t.Mappings, m)
		t.ClientMappingID = m.ID
	}
	m.Proto = input.Proto
	m.LocalIP = input.LocalIP
	m.LocalPort = input.LocalPort
	m.RemotePort = input.RemotePort
	m.Subdomain = input.Subdomain
	if in.ApiKey != "" {
		app := s.appByIDLocked(m.AppID)
		if app == nil {
			return nil, nil, fmt.Errorf("映射应用不存在")
		}
		auth, err := s.sealAuthLocked(AppAuthInput{AuthType: "bearer", ApiKey: in.ApiKey})
		if err != nil {
			return nil, nil, err
		}
		app.Auth = auth
		app.UpdatedAt = now
	}
	if err := s.saveLocked(); err != nil {
		return nil, nil, err
	}
	return t, m, nil
}

func (d *Dashboard) clientState(t *Tunnel) map[string]interface{} {
	out := map[string]interface{}{"tunnel_id": t.ID, "client_id": t.ClientID, "mapping_id": t.ClientMappingID, "name": t.Name, "online": false}
	rt := d.RuntimeView(t.ID)
	out["online"] = rt.Online
	for _, m := range t.Mappings {
		if m.ID == t.ClientMappingID {
			out["protocol"] = m.Proto
			out["subdomain"] = m.Subdomain
			out["remote_port"] = m.RemotePort
			out["local_port"] = m.LocalPort
			// Public URL is authoritative runtime state, never a synthesized success.
			if rt.Online {
				if publicURL := d.PublicEndpoint(m.ID, rt); publicURL != "" {
					out["public_url"] = publicURL
				}
			}
			if message := rt.Errors[m.ID]; message != "" {
				out["error"] = message
			}
		}
	}
	out["https_available"] = d.opts.HTTPSAddr != ""
	out["https_addr"] = d.opts.HTTPSAddr
	return out
}

func (d *Dashboard) apiClientBootstrap(w http.ResponseWriter, r *http.Request) {
	// JSON plus a same-origin check prevents ambient-cookie cross-site writes.
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeErr(w, 415, "需要 application/json")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "https://"+r.Host && origin != "http://"+r.Host {
		writeErr(w, 403, "跨站请求被拒绝")
		return
	}
	var in clientBootstrapInput
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if (in.Protocol == "" || in.Protocol == "https") && d.opts.HTTPSAddr == "" {
		writeErr(w, 409, "ONENAT 服务端未启用公网 HTTPS 隧道监听，请配置 httpsAddr 和有效域名证书")
		return
	}
	if _, _, err := net.SplitHostPort(d.opts.TunnelAddr); err != nil {
		writeErr(w, 409, "ONENAT 服务端控制地址无效")
		return
	}
	u := d.UserFromRequest(r)
	t, _, err := d.store.ensureClientTunnel(u.ID, in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	d.PushConfig(t.ID)
	out := d.clientState(t)
	readKey, err := d.store.EnsureClientReadKey(u.ID, in.ClientID)
	if err != nil {
		writeErr(w, 500, "节点只读凭据创建失败")
		return
	}
	out["read_api_key"] = readKey.Key
	// Config contains the tunnel key: bootstrap only, never the status response.
	// read_api_key is likewise bootstrap-only and scoped to this installation.
	server, _ := json.Marshal(d.opts.TunnelAddr)
	key, _ := json.Marshal(t.Key)
	out["config"] = fmt.Sprintf("server_addr: %s\ntrust_host_root_certs: %t\nauthtoken: %s\ngate: off\ninspect_addr: disabled\ntunnels: {}\n", server, d.opts.ClientTrustHostRootCerts, key)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}

// AuthorizeManagedRequest makes a tunnel key a capability for exactly its
// configured mappings. A modified client cannot use it to register arbitrary
// names, ports, virtual hosts, or a different installation's mapping.
func (s *Store) AuthorizeManagedRequest(tunnelID, key string, request *msg.ReqTunnel) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.data.Tunnels {
		if t.ID == tunnelID {
			if t.Locked || subtle.ConstantTimeCompare([]byte(t.Key), []byte(key)) != 1 {
				return fmt.Errorf("managed tunnel credentials revoked or locked")
			}
			for _, m := range t.Mappings {
				if m.ID == request.Name {
					if request.Protocol != m.Proto || request.RemotePort != uint16(m.RemotePort) || request.Subdomain != m.Subdomain || request.Hostname != "" || request.HttpAuth != "" {
						return fmt.Errorf("managed mapping parameters must match server configuration")
					}
					return nil
				}
			}
			return fmt.Errorf("mapping does not belong to authenticated tunnel")
		}
	}
	return fmt.Errorf("authenticated managed tunnel no longer exists")
}

func (d *Dashboard) apiClientStatus(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	t := d.store.clientTunnel(u.ID, r.URL.Query().Get("client_id"))
	if t == nil {
		writeErr(w, 404, "客户端映射不存在")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, d.clientState(t))
}
