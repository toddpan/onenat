package client

import (
	"flag"
	"fmt"
	"ngrok/version"
	"os"
	"strings"
)

const usage1 string = `Usage: %s [OPTIONS] <local port or address>
Options:
`

const usage2 string = `
Examples:
	ngrok 80
	ngrok -subdomain=example 8080
	ngrok -proto=tcp 22
	ngrok -hostname="example.com" -httpauth="user:password" 10.0.0.1

AI remote agent (zero-config, TeamViewer-style):
	ngrok agent -server=host:4443                     this machine: SSH+HTTP(80)
	ngrok agent -server=host:4443 192.168.1.20        remote host's SSH+HTTP(80)
	ngrok agent -server=host:4443 192.168.1.20:2222   remote host, sshd on 2222
	ngrok agent -server=host:4443 -new-key            rotate the machine key first
	Prints an entrypoint+key card and writes a self-contained
	remote-manual.{md,json} for the AI agent. No config file needed.

Advanced usage: ngrok [OPTIONS] <command> [command args] [...]
Commands:
	ngrok agent [host[:port]]     SSH+Web tunnels behind the machine key
	ngrok managed                 Dashboard-managed client: connects with the
	                              tunnel key from its config file and applies
	                              the tunnel set pushed by the server
	ngrok start [tunnel] [...]    Start tunnels by name from config file
	ngork start-all               Start all tunnels defined in config file
	ngrok list                    List tunnel names from config file
	ngrok help                    Print help
	ngrok version                 Print ngrok version

Examples:
	ngrok start www api blog pubsub
	ngrok -log=stdout -config=ngrok.yml start ssh
	ngrok start-all
	ngrok version

`

type Options struct {
	config     string
	logto      string
	loglevel   string
	authtoken  string
	httpauth   string
	hostname   string
	protocol   string
	subdomain  string
	remotePort int
	gate       string
	server     string
	newKey     bool
	webPort    int
	command    string
	args       []string
}

func ParseArgs() (opts *Options, err error) {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, usage1, os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, usage2)
	}

	// Go's flag package stops at the first positional argument, so
	// "ngrok agent -server=..." would treat the flags as positionals.
	// For the agent/managed commands we allow interleaved flags: pull the
	// subcommand out, let flag.Parse run first, and re-parse below.
	argv := os.Args[1:]
	commandHint := ""
	for i, a := range argv {
		if a == "agent" || a == "managed" {
			commandHint = a
			argv = append(argv[:i:i], argv[i+1:]...)
			break
		}
		if a == "start" || a == "start-all" || a == "list" || a == "help" || a == "version" {
			break // other subcommands keep classic parsing
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		break // classic "ngrok 80" style
	}

	// make flag.Parse() see the argv with 'agent' removed, so that flags
	// written after the subcommand ("ngrok agent -server=...") parse cleanly
	if commandHint != "" {
		os.Args = append([]string{os.Args[0]}, argv...)
	}

	config := flag.String(
		"config",
		"",
		"Path to ngrok configuration file. (default: $HOME/.ngrok)")

	logto := flag.String(
		"log",
		"none",
		"Write log messages to this file. 'stdout' and 'none' have special meanings")

	loglevel := flag.String(
		"log-level",
		"DEBUG",
		"The level of messages to log. One of: DEBUG, INFO, WARNING, ERROR")

	authtoken := flag.String(
		"authtoken",
		"",
		"Authentication token for identifying an ngrok.com account")

	httpauth := flag.String(
		"httpauth",
		"",
		"username:password HTTP basic auth creds protecting the public tunnel endpoint")

	subdomain := flag.String(
		"subdomain",
		"",
		"Request a custom subdomain from the ngrok server. (HTTP only)")

	hostname := flag.String(
		"hostname",
		"",
		"Request a custom hostname from the ngrok server. (HTTP only) (requires CNAME of your DNS)")

	protocol := flag.String(
		"proto",
		"http+https",
		"The protocol of the traffic over the tunnel {'http', 'https', 'tcp'} (default: 'http+https')")

	remotePort := flag.Int(
		"remote-port",
		0,
		"Request a specific remote TCP port from the server (agent/tcp mode)")

	gate := flag.String(
		"gate",
		"",
		"Access gateway mode on tcp tunnels: plain or off (default from config; 'agent' implies plain)")

	server := flag.String(
		"server",
		"",
		"[agent] ngrokd control address host:port (or set NGROK_SERVER); makes agent mode fully zero-config")

	newKey := flag.Bool(
		"new-key",
		false,
		"[agent] rotate the machine key before starting (old key is revoked for new connections)")

	webPort := flag.Int(
		"web-port",
		0,
		"[agent] HTTP service port on the target host (default 80)")

	flag.Parse()

	opts = &Options{
		config:     *config,
		logto:      *logto,
		loglevel:   *loglevel,
		httpauth:   *httpauth,
		subdomain:  *subdomain,
		protocol:   *protocol,
		authtoken:  *authtoken,
		hostname:   *hostname,
		remotePort: *remotePort,
		gate:       *gate,
		server:     *server,
		newKey:     *newKey,
		webPort:    *webPort,
		command:    commandHint,
	}

	// re-parse the positionals left after pulling out the subcommand
	if commandHint == "agent" || commandHint == "managed" {
		rest := make([]string, 0)
		for _, a := range flag.Args() {
			if a != "agent" && a != "managed" {
				rest = append(rest, a)
			}
		}
		opts.args = rest
	} else {
		opts.command = flag.Arg(0)
	}

	switch opts.command {
	case "list":
		opts.args = flag.Args()[1:]
	case "start":
		opts.args = flag.Args()[1:]
	case "start-all":
		opts.args = flag.Args()[1:]
	case "managed":
		// no positional args; tunnels are pushed by the server
	case "agent":
		// args already extracted above (flags interleaved parsing)
	case "version":
		fmt.Println(version.MajorMinor())
		os.Exit(0)
	case "help":
		flag.Usage()
		os.Exit(0)
	case "":
		err = fmt.Errorf("Error: Specify a local port to tunnel to, or " +
			"an ngrok command.\n\nExample: To expose port 80, run " +
			"'ngrok 80'")
		return

	default:
		if len(flag.Args()) > 1 {
			err = fmt.Errorf("You may only specify one port to tunnel to on the command line, got %d: %v",
				len(flag.Args()),
				flag.Args())
			return
		}

		opts.command = "default"
		opts.args = flag.Args()
	}

	return
}
