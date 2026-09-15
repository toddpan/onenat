package server

import (
	"fmt"
	"math/rand"
)

// Public TCP port-mapping range helpers.
//
// The operator may restrict which public ports tunnels may occupy with
// -portRange "30000-40000" (env ONENAT_PORT_RANGE). When no range is
// configured (min/max == 0) every port is allowed — only the separate
// privileged-port (< 1024) rule applies, as before.

// portRangeConfigured reports whether a public-port range is enforced.
func portRangeConfigured() bool {
	return opts != nil && opts.portRangeMin > 0 && opts.portRangeMax > 0
}

// portRangeAllowed reports whether p may be used as a public mapping port.
// An unconfigured range permits every port.
func portRangeAllowed(p int) bool {
	if !portRangeConfigured() {
		return true
	}
	return p >= opts.portRangeMin && p <= opts.portRangeMax
}

// portRangeDesc renders the configured range for logs and error messages,
// e.g. "[30000-40000]"; " unrestricted" when no range is configured.
func portRangeDesc() string {
	if !portRangeConfigured() {
		return "[unrestricted]"
	}
	return fmt.Sprintf("[%d-%d]", opts.portRangeMin, opts.portRangeMax)
}

// randomPortInRange draws a uniformly random port from the configured
// range. Only call when portRangeConfigured() is true.
func randomPortInRange() int {
	return opts.portRangeMin + rand.Intn(opts.portRangeMax-opts.portRangeMin+1)
}

// portRangeAutoAttempts is how many random in-range candidates
// bindTcpAuto tries before falling back to an OS-assigned port.
const portRangeAutoAttempts = 32
