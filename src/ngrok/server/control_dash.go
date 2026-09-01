package server

import (
	"ngrok/msg"
	"ngrok/util"
)

// Implementation of dashboard.ControlConn for *Control. Managed clients are
// reached through the control registry by the stable id "tun-<tunnelId>",
// so the dashboard can push configuration changes to live clients.

// ClientVersion implements dashboard.ControlConn.
func (c *Control) ClientVersion() string {
	if c.auth != nil {
		return c.auth.MmVersion
	}
	return ""
}

// ClientOS implements dashboard.ControlConn.
func (c *Control) ClientOS() string {
	if c.auth != nil {
		return c.auth.OS
	}
	return ""
}

// Online implements dashboard.ControlConn: a control counts as online while
// it is still the registered one for its id.
func (c *Control) Online() bool {
	return c.id != "" && controlRegistry.Get(c.id) == c
}

// ApplyDesired implements dashboard.ControlConn: reconcile server-side
// tunnels towards the desired set, then push a ConfigSync to the client so
// it rebuilds its routing table and requests anything missing.
func (c *Control) ApplyDesired(version int64, desired []msg.DesiredTunnel) {
	desiredByName := make(map[string]msg.DesiredTunnel, len(desired))
	for _, d := range desired {
		desiredByName[d.Name] = d
	}

	c.tunnelsMu.Lock()
	kept := c.tunnels[:0]
	for _, t := range c.tunnels {
		if !t.desiredBy(desiredByName) {
			// server-visible params changed or mapping removed: close the
			// public side now; the client drops its route via ConfigSync
			t.Shutdown()
			continue
		}
		kept = append(kept, t)
	}
	c.tunnels = kept

	active := make(map[string]string, len(c.tunnels))
	for _, t := range c.tunnels {
		if t.dashMappingID != "" {
			active[t.dashMappingID] = t.url
		}
	}
	c.tunnelsMu.Unlock()

	util.PanicToError(func() {
		c.out <- &msg.ConfigSync{Version: version, Desired: desired, Active: active}
	})
}

// DropAllTunnels implements dashboard.ControlConn (repair).
func (c *Control) DropAllTunnels() {
	c.tunnelsMu.Lock()
	defer c.tunnelsMu.Unlock()
	for _, t := range c.tunnels {
		t.Shutdown()
	}
	c.tunnels = c.tunnels[:0]
}

// Kick implements dashboard.ControlConn: disconnect the client entirely
// (tunnel deleted or key reset).
func (c *Control) Kick() {
	c.shutdown.Begin()
}
