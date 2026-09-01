package server

import (
	"flag"
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
	flag.Parse()

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
	}
}
