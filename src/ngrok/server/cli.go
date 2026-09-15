package server

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Options struct {
	httpAddr   string
	httpsAddr  string
	tunnelAddr string
	domain     string
	tlsCrt     string
	tlsKey     string
	authToken  string
	logto      string
	loglevel   string

	// web management console
	webAddr      string
	webData      string
	webAdminPass string
	dlDir        string
	webTlsCrt    string
	webTlsKey    string
	skillsDir    string

	// public TCP port-mapping range: when both bounds are > 0, clients may
	// only request public ports inside [portRangeMin, portRangeMax] and
	// auto-assigned ports are drawn from the same range; 0/0 = unrestricted
	portRangeMin int
	portRangeMax int
}

func parseArgs() *Options {
	httpAddr := flag.String("httpAddr", ":80", "Public address for HTTP connections, empty string to disable")
	httpsAddr := flag.String("httpsAddr", ":443", "Public address listening for HTTPS connections, emptry string to disable")
	tunnelAddr := flag.String("tunnelAddr", ":4443", "Public address listening for ngrok client")
	domain := flag.String("domain", "ngrok.com", "Domain where the tunnels are hosted")
	tlsCrt := flag.String("tlsCrt", "", "Path to a TLS certificate file")
	tlsKey := flag.String("tlsKey", "", "Path to a TLS key file")
	authToken := flag.String("authToken", "", "Require clients to present this token in their Auth message; comma-separated list allowed (one per zero-config agent machine); empty disables authentication")
	logto := flag.String("log", "stdout", "Write log messages to this file. 'stdout' and 'none' have special meanings")
	loglevel := flag.String("log-level", "DEBUG", "The level of messages to log. One of: DEBUG, INFO, WARNING, ERROR")
	webAddr := flag.String("webAddr", ":18080", "Web management console listen address, empty string to disable")
	webData := flag.String("webData", "./ngrokd-dashboard.json", "Dashboard users/tunnels data file (JSON)")
	webAdminPass := flag.String("webAdminPass", "", "Initial admin password for the dashboard (random one printed to log when empty and the data file is fresh)")
	dlDir := flag.String("dlDir", "./dl", "Directory served at /dl/ for prebuilt client binaries")
	webTlsCrt := flag.String("webTlsCrt", "", "Path to a TLS certificate file for the web dashboard (enables HTTPS)")
	webTlsKey := flag.String("webTlsKey", "", "Path to a TLS key file for the web dashboard (enables HTTPS)")
	skillsDir := flag.String("skillsDir", envOr("ONENAT_SKILLS_DIR", ""), "Directory holding application skill files (default: <webData dir>/skills; env ONENAT_SKILLS_DIR)")
	portRange := flag.String("portRange", envOr("ONENAT_PORT_RANGE", ""), "Allowed public port range for TCP mappings, \"min-max\" (e.g. 30000-40000); empty = unrestricted except privileged ports < 1024 (env ONENAT_PORT_RANGE)")
	flag.Parse()

	portRangeMin, portRangeMax, perr := parsePortRange(*portRange)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "invalid -portRange %q: %v\n", *portRange, perr)
		os.Exit(1)
	}

	return &Options{
		httpAddr:   *httpAddr,
		httpsAddr:  *httpsAddr,
		tunnelAddr: *tunnelAddr,
		domain:     *domain,
		tlsCrt:     *tlsCrt,
		tlsKey:     *tlsKey,
		authToken:  *authToken,
		logto:      *logto,
		loglevel:   *loglevel,

		webAddr:      *webAddr,
		webData:      *webData,
		webAdminPass: *webAdminPass,
		dlDir:        *dlDir,
		webTlsCrt:    *webTlsCrt,
		webTlsKey:    *webTlsKey,
		skillsDir:    *skillsDir,

		portRangeMin: portRangeMin,
		portRangeMax: portRangeMax,
	}
}

// parsePortRange parses a "min-max" public-port range specification.
// An empty string means unrestricted and yields (0, 0). Bounds must be
// valid ports with min <= max.
func parsePortRange(s string) (min, max int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, nil
	}
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf(`expected "min-max" (e.g. 30000-40000)`)
	}
	if min, err = strconv.Atoi(strings.TrimSpace(parts[0])); err != nil {
		return 0, 0, fmt.Errorf("invalid min port %q", parts[0])
	}
	if max, err = strconv.Atoi(strings.TrimSpace(parts[1])); err != nil {
		return 0, 0, fmt.Errorf("invalid max port %q", parts[1])
	}
	if min < 1 || max > 65535 {
		return 0, 0, fmt.Errorf("ports must be within 1-65535, got %d-%d", min, max)
	}
	if min > max {
		return 0, 0, fmt.Errorf("min port %d must not exceed max port %d", min, max)
	}
	return min, max, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
