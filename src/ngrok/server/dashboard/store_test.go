package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "dash.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBootstrapAdminOnlyOnce(t *testing.T) {
	s := tempStore(t)
	u, p, created := s.BootstrapAdmin("")
	if !created || u != "admin" || p == "" {
		t.Fatalf("expected fresh admin bootstrap, got %q/%q/%v", u, p, created)
	}
	if _, _, created := s.BootstrapAdmin("x"); created {
		t.Fatal("second bootstrap must not recreate admin")
	}
}

func TestPasswordHashRoundtrip(t *testing.T) {
	h := HashPassword("s3cret-密码")
	if h == "" || h == "s3cret-密码" {
		t.Fatal("hash must differ from password")
	}
	if !VerifyPassword("s3cret-密码", h) {
		t.Fatal("correct password must verify")
	}
	if VerifyPassword("wrong", h) {
		t.Fatal("wrong password must not verify")
	}
	// tampering with the hash must fail
	if VerifyPassword("s3cret-密码", h+"x") {
		t.Fatal("tampered hash must not verify")
	}
}

func TestUserCRUD(t *testing.T) {
	s := tempStore(t)
	s.BootstrapAdmin("adminpass1")

	u, err := s.CreateUser("alice", "alicepass", "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("alice", "x", "user"); err == nil {
		t.Fatal("duplicate username must fail")
	}
	if err := s.UpdateUserRole(u.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateUserPassword(u.ID, "newpass99"); err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("newpass99", s.UserByID(u.ID).PassHash) {
		t.Fatal("password not updated")
	}
	// alice is now the second admin, so deleting her is legal
	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if s.UserByID(u.ID) != nil {
		t.Fatal("user must be gone after delete")
	}
	// deleting the last remaining admin must fail
	last := s.UserByName("admin")
	if err := s.DeleteUser(last.ID); err == nil {
		t.Fatal("must refuse to delete the last admin")
	}
}

func TestTunnelAndMappingCRUD(t *testing.T) {
	s := tempStore(t)
	tun := s.CreateTunnel(NewTunnelInput{Name: "kb", Note: "test", OwnerID: "u1"})
	if tun == nil || tun.ID == "" || tun.Key == "" {
		t.Fatal("tunnel must be created with id and key")
	}
	if len(tun.ID) != 10 {
		t.Fatalf("tunnel id length = %d, want 10", len(tun.ID))
	}
	origKey := tun.Key // store hands out shared pointers; capture before any mutation

	m, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalIP: "127.0.0.1", LocalPort: 22, RemotePort: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMapping(tun.ID, MappingInput{Proto: "smtp", LocalPort: 25}); err == nil {
		t.Fatal("invalid proto must fail")
	}
	if _, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalPort: 70000}); err == nil {
		t.Fatal("invalid port must fail")
	}

	if err := s.UpdateMapping(tun.ID, m.ID, MappingInput{Proto: "tcp", LocalIP: "127.0.0.1", LocalPort: 2222, RemotePort: 47339}); err != nil {
		t.Fatal(err)
	}
	t2 := s.TunnelByID(tun.ID)
	if t2.Mappings[0].LocalPort != 2222 || t2.Mappings[0].RemotePort != 47339 {
		t.Fatalf("mapping not updated: %+v", t2.Mappings[0])
	}

	// key auth
	if _, ok := s.AuthenticateKey(t2.Key); !ok {
		t.Fatal("valid key must authenticate")
	}
	if _, ok := s.AuthenticateKey("ngk-0000"); ok {
		t.Fatal("wrong key must not authenticate")
	}
	newKey, err := s.ResetKey(tun.ID)
	if err != nil || newKey == origKey {
		t.Fatalf("key reset must produce a new key (err=%v)", err)
	}
	if _, ok := s.AuthenticateKey(origKey); ok {
		t.Fatal("old key must stop authenticating after reset")
	}

	if err := s.DeleteMapping(tun.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTunnel(tun.ID); err != nil {
		t.Fatal(err)
	}
	if s.TunnelByID(tun.ID) != nil {
		t.Fatal("tunnel must be gone")
	}
}

func TestSecurityPoliciesAndBoundaries(t *testing.T) {
	s := tempStore(t)
	s.BootstrapAdmin("adminpass")
	u1, _ := s.CreateUser("user1", "pass1234", "user")
	u2, _ := s.CreateUser("user2", "pass1234", "user")

	t1 := s.CreateTunnel(NewTunnelInput{Name: "t1", OwnerID: u1.ID})
	t2 := s.CreateTunnel(NewTunnelInput{Name: "t2", OwnerID: u2.ID})

	// 1. 默认仅允许 127.0.0.1/localhost, 禁止随意拨号局域网 IP
	if _, err := s.AddMapping(t1.ID, MappingInput{Proto: "tcp", LocalIP: "192.168.1.100", LocalPort: 80}); err == nil {
		t.Fatal("default should reject remote LAN IP")
	}
	if _, err := s.AddMapping(t1.ID, MappingInput{Proto: "tcp", LocalIP: "169.254.169.254", LocalPort: 80}); err == nil {
		t.Fatal("default should reject cloud metadata IP")
	}
	// 开启 allow_remote_targets 后允许
	s.UpdateTunnelMeta(t1.ID, t1.Name, t1.Note, t1.Locked, true, t1.OwnerID)
	if _, err := s.AddMapping(t1.ID, MappingInput{Proto: "tcp", LocalIP: "192.168.1.100", LocalPort: 80}); err != nil {
		t.Fatalf("should allow remote target when enabled: %v", err)
	}

	// 2. 特权端口保护 (< 1024)
	if _, err := s.AddMapping(t1.ID, MappingInput{Proto: "tcp", LocalPort: 80, RemotePort: 80}); err == nil {
		t.Fatal("should reject privileged port < 1024")
	}

	// 3. 子域名多租户防劫持 (u1 占用的 subdomain, u2 无法抢占)
	if _, err := s.AddMapping(t1.ID, MappingInput{Proto: "http", LocalPort: 8080, Subdomain: "corp-app"}); err != nil {
		t.Fatalf("u1 should add subdomain: %v", err)
	}
	if _, err := s.AddMapping(t2.ID, MappingInput{Proto: "http", LocalPort: 8080, Subdomain: "corp-app"}); err == nil {
		t.Fatal("u2 should not steal u1's subdomain")
	}
}

func TestStorePersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dash.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	tun := s.CreateTunnel(NewTunnelInput{Name: "persist", OwnerID: "u1"})
	s.AddMapping(tun.ID, MappingInput{Proto: "http", LocalPort: 8080, Subdomain: "web"})

	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.TunnelByID(tun.ID)
	if got == nil || len(got.Mappings) != 1 || got.Mappings[0].Subdomain != "web" {
		t.Fatalf("state lost across reload: %+v", got)
	}
	if len(s2.SessionSecret()) != 64 { // hex of 32 bytes
		t.Fatalf("session secret not persisted")
	}
}

func TestVisibilityScoping(t *testing.T) {
	s := tempStore(t)
	mine := s.CreateTunnel(NewTunnelInput{Name: "mine", OwnerID: "user-a"})
	s.CreateTunnel(NewTunnelInput{Name: "theirs", OwnerID: "user-b"})

	if got := s.Tunnels("user-a", false); len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("normal user must only see own tunnels, got %d", len(got))
	}
	if got := s.Tunnels("", true); len(got) != 2 {
		t.Fatalf("admin must see all tunnels, got %d", len(got))
	}
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
