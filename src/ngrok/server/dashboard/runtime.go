package dashboard

import (
	"net"
	"strconv"
	"sync"
	"time"

	"ngrok/msg"
)

// ControlConn is the subset of *server.Control the dashboard needs.
// Implemented by the server package; wired via SetControlLookup to avoid
// an import cycle (server -> dashboard only).
type ControlConn interface {
	Online() bool
	ClientVersion() string
	ClientOS() string
	// ApplyDesired pushes a full desired-config sync to the client and
	// reconciles server-side tunnels towards it.
	ApplyDesired(version int64, desired []msg.DesiredTunnel)
	// DropAllTunnels closes every tunnel on this control (repair).
	DropAllTunnels()
	// Kick closes the control connection (used on delete / key reset).
	Kick()
}

// ConnRecord is one proxied connection through a tunnel.
type ConnRecord struct {
	Time       time.Time `json:"time"`
	ClientAddr string    `json:"client_addr"`
	MappingID  string    `json:"mapping_id"`
	PublicURL  string    `json:"public_url"`
	BytesIn    int64     `json:"bytes_in"`
	BytesOut   int64     `json:"bytes_out"`
	DurationMs int64     `json:"duration_ms"`
}

const maxConnRecords = 50

type runtimeState struct {
	mu            sync.RWMutex
	lastSeen      time.Time
	clientVersion string
	clientOS      string
	active        map[string]string // mappingID -> public url
	errors        map[string]string // mappingID -> last error
	version       int64             // last acked config version
	conns         []ConnRecord
	daily         map[string]int64 // "2006-01-02" -> total bytes
}

func (rs *runtimeState) snapshotActive() map[string]string {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	out := make(map[string]string, len(rs.active))
	for k, v := range rs.active {
		out[k] = v
	}
	return out
}

// ---------- wiring ----------

// SetControlLookup installs the callback that resolves a tunnel id to its
// live control connection. Called once from server.Main.
func (d *Dashboard) SetControlLookup(fn func(tunnelID string) (ControlConn, bool)) {
	d.controlLookup = fn
}

func (d *Dashboard) lookupControl(tunnelID string) ControlConn {
	if d.controlLookup == nil {
		return nil
	}
	c, _ := d.controlLookup(tunnelID)
	return c
}

// IsOnline reports whether a managed client is currently connected.
func (d *Dashboard) IsOnline(tunnelID string) bool {
	c := d.lookupControl(tunnelID)
	return c != nil && c.Online()
}

// nextVersion bumps and returns the config version for a tunnel.
func (d *Dashboard) nextVersion(tunnelID string) int64 {
	d.rtMu.Lock()
	defer d.rtMu.Unlock()
	rs := d.runtime[tunnelID]
	if rs == nil {
		rs = &runtimeState{active: map[string]string{}, errors: map[string]string{}, daily: map[string]int64{}}
		d.runtime[tunnelID] = rs
	}
	rs.mu.Lock()
	rs.version++
	v := rs.version
	rs.mu.Unlock()
	return v
}

// PushConfig sends the current stored configuration of the tunnel to its
// live client. No-op when the client is offline (changes apply on connect).
func (d *Dashboard) PushConfig(tunnelID string) {
	t := d.store.TunnelByID(tunnelID)
	if t == nil {
		return
	}
	c := d.lookupControl(tunnelID)
	if c == nil || !c.Online() {
		return
	}
	desired := d.DesiredFor(t)
	v := d.nextVersion(tunnelID)
	c.ApplyDesired(v, desired)
}

// PushAll marks the tunnel config as changed even when offline (bumps the
// version so the next connect pushes fresh state).
func (d *Dashboard) TouchConfig(tunnelID string) {
	d.nextVersion(tunnelID)
}

// RepairTunnel closes all tunnels on the live client and re-pushes the
// desired config so everything is rebuilt from scratch.
func (d *Dashboard) RepairTunnel(tunnelID string) {
	c := d.lookupControl(tunnelID)
	if c == nil || !c.Online() {
		return
	}
	c.DropAllTunnels()
	d.PushConfig(tunnelID)
}

// KickClient disconnects the live client (delete / key reset).
func (d *Dashboard) KickClient(tunnelID string) {
	if c := d.lookupControl(tunnelID); c != nil {
		c.Kick()
	}
}

// DesiredFor converts stored mappings into the wire-level desired config.
func (d *Dashboard) DesiredFor(t *Tunnel) []msg.DesiredTunnel {
	out := make([]msg.DesiredTunnel, 0, len(t.Mappings))
	for _, m := range t.Mappings {
		out = append(out, msg.DesiredTunnel{
			Name:       m.ID,
			Protocol:   m.Proto,
			LocalAddr:  joinHostPort(m.LocalIP, m.LocalPort),
			RemotePort: uint16(m.RemotePort),
			Subdomain:  m.Subdomain,
			HttpAuth:   "",
		})
	}
	return out
}

// ReportAck records the client's acknowledgement of a config version.
func (d *Dashboard) ReportAck(tunnelID string, ack *msg.AckConfig) {
	d.rtMu.Lock()
	rs := d.runtime[tunnelID]
	if rs == nil {
		rs = &runtimeState{active: map[string]string{}, errors: map[string]string{}, daily: map[string]int64{}}
		d.runtime[tunnelID] = rs
	}
	d.rtMu.Unlock()

	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.version = ack.Version
	rs.lastSeen = time.Now()
	for _, a := range ack.Tunnels {
		delete(rs.active, a.Name)
		delete(rs.errors, a.Name)
		if a.Error != "" {
			rs.errors[a.Name] = a.Error
		} else if a.URL != "" {
			rs.active[a.Name] = a.URL
		}
	}
}

// TouchClient records liveness info from the auth step.
func (d *Dashboard) TouchClient(tunnelID, clientVersion, clientOS string) {
	d.rtMu.Lock()
	defer d.rtMu.Unlock()
	rs := d.runtime[tunnelID]
	if rs == nil {
		rs = &runtimeState{active: map[string]string{}, errors: map[string]string{}, daily: map[string]int64{}}
		d.runtime[tunnelID] = rs
	}
	rs.mu.Lock()
	rs.lastSeen = time.Now()
	rs.clientVersion = clientVersion
	rs.clientOS = clientOS
	rs.mu.Unlock()
}

// RecordConn is called by the server for every proxied connection so the
// dashboard can show recent activity and daily traffic.
func (d *Dashboard) RecordConn(tunnelID string, rec ConnRecord) {
	d.rtMu.Lock()
	rs := d.runtime[tunnelID]
	if rs == nil {
		rs = &runtimeState{active: map[string]string{}, errors: map[string]string{}, daily: map[string]int64{}}
		d.runtime[tunnelID] = rs
	}
	d.rtMu.Unlock()

	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.lastSeen = rec.Time
	rs.conns = append(rs.conns, rec)
	if len(rs.conns) > maxConnRecords {
		rs.conns = rs.conns[len(rs.conns)-maxConnRecords:]
	}
	day := rec.Time.Format("2006-01-02")
	rs.daily[day] += rec.BytesIn + rec.BytesOut
	// keep only the last 8 days
	for k := range rs.daily {
		if k < rec.Time.AddDate(0, 0, -7).Format("2006-01-02") {
			delete(rs.daily, k)
		}
	}
}

// RuntimeView is the JSON-friendly runtime status shown in the UI.
type RuntimeView struct {
	Online        bool              `json:"online"`
	LastSeen      *time.Time        `json:"last_seen"`
	ClientVersion string            `json:"client_version"`
	ClientOS      string            `json:"client_os"`
	Active        map[string]string `json:"active"`  // mappingID -> public url
	Errors        map[string]string `json:"errors"`  // mappingID -> error
	Conns         []ConnRecord      `json:"conns"`   // most recent last
	WeeklyTraffic []DayTraffic      `json:"traffic"` // 7 entries, oldest first
}

type DayTraffic struct {
	Day   string `json:"day"`
	Bytes int64  `json:"bytes"`
}

func (d *Dashboard) RuntimeView(tunnelID string) *RuntimeView {
	v := &RuntimeView{
		Online: d.IsOnline(tunnelID),
		Active: map[string]string{},
		Errors: map[string]string{},
		Conns:  []ConnRecord{},
	}
	if c := d.lookupControl(tunnelID); c != nil {
		v.ClientVersion = c.ClientVersion()
		v.ClientOS = c.ClientOS()
	}

	d.rtMu.RLock()
	rs := d.runtime[tunnelID]
	d.rtMu.RUnlock()
	if rs == nil {
		return v
	}

	rs.mu.RLock()
	defer rs.mu.RUnlock()
	if !rs.lastSeen.IsZero() {
		t := rs.lastSeen
		v.LastSeen = &t
	}
	v.ClientVersion = firstNonEmpty(v.ClientVersion, rs.clientVersion)
	v.ClientOS = firstNonEmpty(v.ClientOS, rs.clientOS)
	for k, val := range rs.active {
		v.Active[k] = val
	}
	for k, val := range rs.errors {
		v.Errors[k] = val
	}
	for i := len(rs.conns) - 1; i >= 0; i-- {
		v.Conns = append(v.Conns, rs.conns[i])
	}
	// last 7 days, oldest first
	now := time.Now()
	for i := 6; i >= 0; i-- {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		v.WeeklyTraffic = append(v.WeeklyTraffic, DayTraffic{Day: day, Bytes: rs.daily[day]})
	}
	return v
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func joinHostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
