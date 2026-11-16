package server

import (
	"net"
	"ngrok/conn"
	"ngrok/msg"
	"strings"
	"testing"
)

func TestExplicitTCPPortDoesNotFallBackAndRangeCannotLeak(t *testing.T) {
	occupied, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port
	check, bindErr := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("0.0.0.0"), Port: port})
	if bindErr == nil {
		check.Close()
		t.Fatalf("test precondition: duplicate binding unexpectedly allowed for %d", port)
	}
	withPortRange(t, port, port)
	// Use a real logged TCP connection so the bind error path is exercised too.
	local, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	connection, err := net.Dial("tcp4", local.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	accepted, err := local.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	control := &Control{conn: conn.Wrap(accepted, "test")}
	tunnel, err := NewTunnel(&msg.ReqTunnel{Protocol: "tcp", RemotePort: uint16(port)}, control)
	if err == nil {
		if tunnel != nil {
			tunnel.Shutdown()
		}
		t.Fatalf("explicit occupied port %d must fail, got tunnel %#v", port, tunnel)
	}
	if !strings.Contains(err.Error(), "fixed public port") {
		t.Fatalf("wrong fixed error: %v", err)
	}
	tunnel, err = NewTunnel(&msg.ReqTunnel{Protocol: "tcp"}, control)
	if err == nil {
		if tunnel != nil {
			tunnel.Shutdown()
		}
		t.Fatal("full allowed range must fail instead of allocating an out-of-range port")
	}
	if !strings.Contains(err.Error(), "allowed range") {
		t.Fatalf("wrong exhaustion error: %v", err)
	}
}
