package dashboard

import (
	"encoding/json"
	"net/http/httptest"
	"ngrok/msg"
	"strings"
	"testing"
)

func TestEmbeddedClientBootstrapIsStableAndPrivate(t *testing.T) {
	d, s := newTestDashboard(t)
	d.opts.HTTPSAddr = ":443"
	d.opts.TunnelAddr = "nat.example:4443"
	d.opts.ClientTrustHostRootCerts = true
	owner, err := s.CreateUser("embedded-owner", "password", "user")
	if err != nil {
		t.Fatal(err)
	}
	const dshKey = "dsk_installation_only_0123456789"
	body := `{"client_id":"installation-first-12345","name":"node A","local_port":45678,"api_key":"` + dshKey + `"}`
	first := sessRequest(t, d, owner, "POST", "/api/client/bootstrap", body)
	if first.Code != 200 {
		t.Fatalf("bootstrap: %d %s", first.Code, first.Body.String())
	}
	var a map[string]interface{}
	json.Unmarshal(first.Body.Bytes(), &a)
	if strings.Contains(first.Body.String(), "public_url") {
		t.Fatal("offline bootstrap must not claim an endpoint")
	}
	config := a["config"].(string)
	if !strings.Contains(config, "trust_host_root_certs: true") || !strings.Contains(config, "inspect_addr: disabled") {
		t.Fatal("wrong headless/TLS config")
	}
	if strings.Contains(config, dshKey) || !strings.Contains(first.Body.String(), `"read_api_key":"onk-`) {
		t.Fatal("bootstrap must return only a scoped read key, never the DSH key")
	}
	readKey := a["read_api_key"].(string)
	var found *ApiKey
	for _, k := range s.ApiKeys(owner.ID, false) {
		if k.Key == readKey {
			found = k
		}
	}
	if found == nil || !found.CanReadCred {
		t.Fatal("installation read key must be owner-scoped and credential-readable")
	}
	if _, _, _, got, _, _, err := s.RevealMappingCredential(a["mapping_id"].(string)); err != nil || got != dshKey {
		t.Fatalf("mapping credential was not sealed into the managed app: %v", err)
	}
	tun := s.clientTunnel(owner.ID, "installation-first-12345")
	if tun == nil || tun.OwnerID != owner.ID || !strings.Contains(config, tun.Key) {
		t.Fatal("config must carry this owner's installation-scoped key")
	}
	again := sessRequest(t, d, owner, "POST", "/api/client/bootstrap", strings.Replace(body, "45678", "45679", 1))
	var b map[string]interface{}
	json.Unmarshal(again.Body.Bytes(), &b)
	if a["tunnel_id"] != b["tunnel_id"] || a["mapping_id"] != b["mapping_id"] || a["subdomain"] != b["subdomain"] || a["config"] != b["config"] {
		t.Fatal("reprovision must preserve all stable IDs and key")
	}
	if len(tun.Mappings) != 1 || tun.Mappings[0].LocalPort != 45679 {
		t.Fatal("reprovision should update one mapping")
	}
	status := sessRequest(t, d, owner, "GET", "/api/client/status?client_id=installation-first-12345", "")
	if status.Code != 200 || strings.Contains(status.Body.String(), tun.Key) || strings.Contains(status.Body.String(), "config") || strings.Contains(status.Body.String(), "read_api_key") || strings.Contains(status.Body.String(), dshKey) {
		t.Fatalf("status leaks a secret: %s", status.Body.String())
	}
	secondBody := strings.Replace(body, "installation-first-12345", "installation-second-12345", 1)
	second := sessRequest(t, d, owner, "POST", "/api/client/bootstrap", secondBody)
	if second.Code != 200 {
		t.Fatal(second.Body.String())
	}
	other := s.clientTunnel(owner.ID, "installation-second-12345")
	if tun.Key == other.Key || tun.ID == other.ID {
		t.Fatal("each installation requires a separate tunnel key")
	}
	reopened, err := OpenStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.clientTunnel(owner.ID, tun.ClientID).ClientMappingID != tun.ClientMappingID {
		t.Fatal("installation identity must survive server restart")
	}
}

func TestEmbeddedClientBootstrapAuthAndAvailability(t *testing.T) {
	d, s := newTestDashboard(t)
	admin := s.UserByName("admin")
	body := `{"client_id":"installation-third-12345","name":"node","local_port":45678}`
	request := httptest.NewRequest("POST", "/api/client/bootstrap", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	d.Handler().ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatalf("unauthenticated bootstrap: %d", response.Code)
	}
	if code := sessRequest(t, d, admin, "POST", "/api/client/bootstrap", body).Code; code != 409 {
		t.Fatalf("HTTPS disabled: %d", code)
	}
	if len(s.Tunnels(admin.ID, false)) != 0 {
		t.Fatal("unavailable HTTPS must not create partial resources")
	}
	d.opts.HTTPSAddr = ":443"
	d.opts.TunnelAddr = "nat.example:4443"
	if code := sessRequest(t, d, admin, "POST", "/api/client/bootstrap", body).Code; code != 200 {
		t.Fatalf("bootstrap: %d", code)
	}
	other, err := s.CreateUser("embedded-other", "password", "user")
	if err != nil {
		t.Fatal(err)
	}
	if code := sessRequest(t, d, other, "GET", "/api/client/status?client_id=installation-third-12345", "").Code; code != 404 {
		t.Fatalf("cross owner access: %d", code)
	}
	key := s.CreateApiKey(admin.ID, "read-only")
	request = httptest.NewRequest("POST", "/api/client/bootstrap", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key.Key)
	response = httptest.NewRecorder()
	d.Handler().ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatalf("read-only resource key must not bootstrap: %d", response.Code)
	}
	request = httptest.NewRequest("POST", "/api/client/bootstrap", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://attacker.example")
	request.AddCookie(loginAs(t, d, admin.Username))
	response = httptest.NewRecorder()
	d.Handler().ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatalf("cross-site bootstrap: %d", response.Code)
	}
}

func TestEmbeddedManagedCredentialCannotExpandScope(t *testing.T) {
	_, s := newTestDashboard(t)
	owner := s.UserByName("admin")
	tun, m, err := s.ensureClientTunnel(owner.ID, clientBootstrapInput{ClientID: "scope-installation-12345", Name: "node", Protocol: "tcp", LocalPort: 45678, RemotePort: 34567})
	if err != nil {
		t.Fatal(err)
	}
	valid := msg.ReqTunnel{Name: m.ID, Protocol: m.Proto, RemotePort: uint16(m.RemotePort)}
	if err := s.AuthorizeManagedRequest(tun.ID, tun.Key, &valid); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*msg.ReqTunnel){func(r *msg.ReqTunnel) { r.Name = "foreign-mapping" }, func(r *msg.ReqTunnel) { r.RemotePort++ }, func(r *msg.ReqTunnel) { r.Hostname = "attacker.example" }, func(r *msg.ReqTunnel) { r.Subdomain = "attacker" }, func(r *msg.ReqTunnel) { r.Protocol = "https" }, func(r *msg.ReqTunnel) { r.HttpAuth = "injected" }}
	for _, mutate := range mutations {
		request := valid
		mutate(&request)
		if s.AuthorizeManagedRequest(tun.ID, tun.Key, &request) == nil {
			t.Fatalf("accepted forged managed request: %#v", request)
		}
	}
	if s.AuthorizeManagedRequest(tun.ID, "wrong-key", &valid) == nil {
		t.Fatal("wrong key accepted")
	}
	tun.Locked = true
	if s.AuthorizeManagedRequest(tun.ID, tun.Key, &valid) == nil {
		t.Fatal("locked key accepted")
	}
}
