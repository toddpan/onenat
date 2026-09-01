package server

import (
	"testing"

	"ngrok/conn"
	"ngrok/log"
	"ngrok/msg"
)

// Tunnel.Shutdown touches the package-global registries; provide real ones
// for the tests in this package.
func init() {
	tunnelRegistry = NewTunnelRegistry(1024, "")
	controlRegistry = NewControlRegistry()
}

var _ = conn.Conn(nil) // keep conn import for Control.proxies field type

func TestTunnelDesiredBy(t *testing.T) {
	mk := func(name string, remote uint16) *msg.ReqTunnel {
		return &msg.ReqTunnel{ReqId: name, Name: name, Protocol: "tcp", RemotePort: remote}
	}

	t.Run("classic tunnels are never reconciled away", func(t *testing.T) {
		tun := &Tunnel{req: &msg.ReqTunnel{Protocol: "tcp", RemotePort: 1234}}
		if !tun.desiredBy(map[string]msg.DesiredTunnel{}) {
			t.Fatal("classic tunnel must always be desired")
		}
	})

	t.Run("managed tunnel survives when unchanged", func(t *testing.T) {
		tun := &Tunnel{req: mk("m1", 2222), dashMappingID: "m1"}
		desired := map[string]msg.DesiredTunnel{
			"m1": {Name: "m1", Protocol: "tcp", RemotePort: 2222, LocalAddr: "127.0.0.1:22"},
		}
		if !tun.desiredBy(desired) {
			t.Fatal("unchanged mapping must be kept (local addr change alone must not rebind)")
		}
	})

	t.Run("managed tunnel is dropped on removal or server-visible change", func(t *testing.T) {
		tun := &Tunnel{req: mk("m1", 2222), dashMappingID: "m1"}
		if tun.desiredBy(map[string]msg.DesiredTunnel{}) {
			t.Fatal("removed mapping must not be desired")
		}
		if tun.desiredBy(map[string]msg.DesiredTunnel{
			"m1": {Name: "m1", Protocol: "tcp", RemotePort: 3333},
		}) {
			t.Fatal("remote port change must not be desired")
		}
		if tun.desiredBy(map[string]msg.DesiredTunnel{
			"m1": {Name: "m1", Protocol: "http", RemotePort: 2222},
		}) {
			t.Fatal("protocol change must not be desired")
		}
	})
}

func TestControlApplyDesiredReconcilesTunnelSet(t *testing.T) {
	c := &Control{
		out:     make(chan msg.Message, 16),
		id:      "tun-test",
		auth:    &msg.Auth{MmVersion: "1.x", OS: "test"},
		proxies: make(chan conn.Conn, 1),
	}
	mkTun := func(name, url string, remote uint16) *Tunnel {
		return &Tunnel{
			req:           &msg.ReqTunnel{ReqId: name, Name: name, Protocol: "tcp", RemotePort: remote},
			dashMappingID: name,
			url:           url,
			Logger:        log.NewPrefixLogger("test"),
		}
	}
	c.tunnels = []*Tunnel{
		mkTun("keep", "tcp://x:1", 1),
		mkTun("drop", "tcp://x:2", 2),
		mkTun("change", "tcp://x:3", 3),
	}

	desired := []msg.DesiredTunnel{
		{Name: "keep", Protocol: "tcp", RemotePort: 1},
		{Name: "change", Protocol: "tcp", RemotePort: 33}, // remote changed -> rebind
		{Name: "new", Protocol: "tcp", RemotePort: 0},
	}

	c.ApplyDesired(7, desired)

	// server-side reconcile: keep survives, drop & change are shut down
	if len(c.tunnels) != 1 || c.tunnels[0].dashMappingID != "keep" {
		t.Fatalf("expected only 'keep' to survive, got %d tunnels", len(c.tunnels))
	}

	// client-side push: ConfigSync with full desired set + active view
	synced := <-c.out
	cfg, ok := synced.(*msg.ConfigSync)
	if !ok {
		t.Fatalf("expected ConfigSync, got %T", synced)
	}
	if cfg.Version != 7 || len(cfg.Desired) != 3 {
		t.Fatalf("unexpected ConfigSync: v=%d desired=%d", cfg.Version, len(cfg.Desired))
	}
	if len(cfg.Active) != 1 || cfg.Active["keep"] != "tcp://x:1" {
		t.Fatalf("unexpected active view: %v", cfg.Active)
	}
}

func TestTokenAllowedStillWorks(t *testing.T) {
	if !tokenAllowed("k1,k2", "k2") {
		t.Fatal("static token list must still authenticate")
	}
	if tokenAllowed("k1", "") {
		t.Fatal("empty token must never authenticate")
	}
}
