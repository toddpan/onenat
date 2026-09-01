package msg

import (
	"encoding/json"
	"reflect"
)

var TypeMap map[string]reflect.Type

func init() {
	TypeMap = make(map[string]reflect.Type)

	t := func(obj interface{}) reflect.Type { return reflect.TypeOf(obj).Elem() }
	TypeMap["Auth"] = t((*Auth)(nil))
	TypeMap["AuthResp"] = t((*AuthResp)(nil))
	TypeMap["ReqTunnel"] = t((*ReqTunnel)(nil))
	TypeMap["NewTunnel"] = t((*NewTunnel)(nil))
	TypeMap["RegProxy"] = t((*RegProxy)(nil))
	TypeMap["ReqProxy"] = t((*ReqProxy)(nil))
	TypeMap["StartProxy"] = t((*StartProxy)(nil))
	TypeMap["Ping"] = t((*Ping)(nil))
	TypeMap["Pong"] = t((*Pong)(nil))
	TypeMap["ConfigSync"] = t((*ConfigSync)(nil))
	TypeMap["AckConfig"] = t((*AckConfig)(nil))
}

type Message interface{}

type Envelope struct {
	Type    string
	Payload json.RawMessage
}

// When a client opens a new control channel to the server
// it must start by sending an Auth message.
type Auth struct {
	Version   string // protocol version
	MmVersion string // major/minor software version (informational only)
	User      string
	Password  string
	OS        string
	Arch      string
	ClientId  string // empty for new sessions
}

// A server responds to an Auth message with an
// AuthResp message over the control channel.
//
// If Error is not the empty string
// the server has indicated it will not accept
// the new session and will close the connection.
//
// The server response includes a unique ClientId
// that is used to associate and authenticate future
// proxy connections via the same field in RegProxy messages.
type AuthResp struct {
	Version   string
	MmVersion string
	ClientId  string
	Error     string
}

// A client sends this message to the server over the control channel
// to request a new tunnel be opened on the client's behalf.
// ReqId is a random number set by the client that it can pull
// from future NewTunnel's to correlate then to the requesting ReqTunnel.
type ReqTunnel struct {
	ReqId    string
	Protocol string

	// managed mode: stable name of the desired tunnel (dashboard mapping id);
	// echoed back in NewTunnel.ReqId so both sides can correlate
	Name string

	// http only
	Hostname  string
	Subdomain string
	HttpAuth  string

	// tcp only
	RemotePort uint16
}

// When the server opens a new tunnel on behalf of
// a client, it sends a NewTunnel message to notify the client.
// ReqId is the ReqId from the corresponding ReqTunnel message.
//
// A client may receive *multiple* NewTunnel messages from a single
// ReqTunnel. (ex. A client opens an https tunnel and the server
// chooses to open an http tunnel of the same name as well)
type NewTunnel struct {
	ReqId    string
	Name     string // managed mode: mapping name from the requesting ReqTunnel
	Url      string
	Protocol string
	Error    string
}

// When the server wants to initiate a new tunneled connection, it sends
// this message over the control channel to the client. When a client receives
// this message, it must initiate a new proxy connection to the server.
type ReqProxy struct {
}

// After a client receives a ReqProxy message, it opens a new
// connection to the server and sends a RegProxy message.
type RegProxy struct {
	ClientId string
}

// This message is sent by the server to the client over a *proxy* connection before it
// begins to send the bytes of the proxied request.
type StartProxy struct {
	Url        string // URL of the tunnel this connection connection is being proxied for
	ClientAddr string // Network address of the client initiating the connection to the tunnel
}

// A client or server may send this message periodically over
// the control channel to request that the remote side acknowledge
// its connection is still alive. The remote side must respond with a Pong.
type Ping struct {
}

// Sent by a client or server over the control channel to indicate
// it received a Ping.
type Pong struct {
}

// DesiredTunnel is one tunnel the server wants a managed client to keep
// open. Name is the dashboard mapping id and is used as ReqId on the
// corresponding ReqTunnel so messages correlate.
type DesiredTunnel struct {
	Name       string
	Protocol   string // tcp | http | https
	LocalAddr  string // "127.0.0.1:22"
	RemotePort uint16 // tcp only; 0 = auto
	Subdomain  string // http/https only
	Hostname   string // http/https only
	HttpAuth   string // http/https only
}

// ConfigSync is sent by the server to a managed client right after
// authentication and after every dashboard-side configuration change.
// Desired is the full set of tunnels that should exist; Active maps the
// names of those currently open on the server to their public URLs. The
// client rebuilds its routing table from Active and requests the missing
// ones via ReqTunnel. The server performs deletions on its own side
// before pushing, so any tunnel absent from Active is already closed.
type ConfigSync struct {
	Version int64
	Desired []DesiredTunnel
	Active  map[string]string // name -> public url
}

// AckTunnel reports the client-side state of one DesiredTunnel.
type AckTunnel struct {
	Name  string
	URL   string // public url when established
	Error string // last registration error, if any
}

// AckConfig is sent by the managed client back to the server after each
// ConfigSync (and after subsequent NewTunnel messages) so the dashboard
// can display per-mapping status.
type AckConfig struct {
	Version int64
	Tunnels []AckTunnel
}
