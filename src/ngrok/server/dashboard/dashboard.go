// Package dashboard implements the ngrokd web management console: users,
// tunnels, port mappings, one-click client deployment and live config push.
// The server package imports this package (never the reverse); live control
// connections are reached through the ControlConn interface wired at startup.
package dashboard

import (
	htmpl "html/template"
	"net/http"
	"path"
	"strings"
	"sync"
	ttmpl "text/template"
	"time"
)

type Options struct {
	Domain     string // domain used when signing tunnels / generating client config
	TunnelAddr string // client control address "host:port" baked into client config
	DataPath   string // JSON persistence path
	DlDir      string // directory serving prebuilt client binaries
	AdminPass  string // optional initial admin password (random when empty)
}

type Dashboard struct {
	store          *Store
	sessions       *Sessions
	opts           Options
	tpl            *htmpl.Template
	installTmpl    *ttmpl.Template
	installPs1Tmpl *ttmpl.Template

	controlLookup func(tunnelID string) (ControlConn, bool)

	rtMu    sync.RWMutex
	runtime map[string]*runtimeState

	deployMu   sync.Mutex
	deployHits map[string]*deployCounter
}

// New opens (or bootstraps) the store and returns a ready dashboard.
func New(opts Options) (*Dashboard, error) {
	store, err := OpenStore(opts.DataPath)
	if err != nil {
		return nil, err
	}
	d := &Dashboard{
		store:      store,
		sessions:   NewSessions(store.SessionSecret()),
		opts:       opts,
		runtime:    map[string]*runtimeState{},
		deployHits: map[string]*deployCounter{},
	}
	if err := d.parseTemplates(); err != nil {
		return nil, err
	}
	return d, nil
}

// Bootstrap prints and returns the initial admin credentials when the store
// was empty (fresh install). Returns created=false otherwise.
func (d *Dashboard) Bootstrap() (username, password string, created bool) {
	return d.store.BootstrapAdmin(d.opts.AdminPass)
}

// Store exposes the underlying store (used by the server package at startup).
func (d *Dashboard) Store() *Store { return d.store }

// ---------- routing ----------
//
// Routing is done manually instead of via ServeMux method patterns
// ("GET /x/{id}"): GOPATH builds (no go.mod) run net/http in pre-1.22
// compatibility mode where those patterns silently never match. A plain
// dispatcher behaves identically on every toolchain.

// Handler returns the full http.Handler for the dashboard.
func (d *Dashboard) Handler() http.Handler {
	return http.HandlerFunc(d.route)
}

// pathSeg returns the n-th (0-based) slash-separated segment of the path.
func pathSeg(r *http.Request, n int) string {
	segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if n < len(segs) {
		return segs[n]
	}
	return ""
}

// methodMismatch answers with 405 when the method differs from want.
func methodMismatch(w http.ResponseWriter, r *http.Request, want ...string) bool {
	for _, m := range want {
		if r.Method == m {
			return false
		}
	}
	w.Header().Set("Allow", strings.Join(want, ", "))
	writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	return true
}

func (d *Dashboard) route(w http.ResponseWriter, r *http.Request) {
	// normalize the path so "/api/../secret" style tricks never reach a handler
	cleaned := path.Clean("/" + r.URL.Path)
	if cleaned != r.URL.Path {
		http.Redirect(w, r, cleaned, http.StatusMovedPermanently)
		return
	}
	p := cleaned
	m := r.Method

	switch {
	// ---------- auth pages ----------
	case p == "/login" && m == http.MethodGet:
		d.pageLogin(w, r)
	case p == "/login" && m == http.MethodPost:
		d.handleLoginPost(w, r)
	case p == "/logout" && m == http.MethodGet:
		d.handleLogout(w, r)

	// ---------- public: installer, binaries, deploy config ----------
	case strings.HasPrefix(p, "/static/"):
		d.handleStatic(w, r)
	case p == "/install.sh":
		if methodMismatch(w, r, http.MethodGet) {
			return
		}
		d.handleInstallScript(w, r)
	case p == "/install.ps1":
		if methodMismatch(w, r, http.MethodGet) {
			return
		}
		d.handleInstallPs1(w, r)
	case strings.HasPrefix(p, "/dl/"):
		if methodMismatch(w, r, http.MethodGet) {
			return
		}
		d.handleDownload(w, r)
	case p == "/api/deploy":
		if methodMismatch(w, r, http.MethodGet) {
			return
		}
		d.handleDeploy(w, r)

	// ---------- auth api ----------
	case p == "/api/login":
		if methodMismatch(w, r, http.MethodPost) {
			return
		}
		d.apiLogin(w, r)
	case p == "/api/logout":
		if methodMismatch(w, r, http.MethodPost) {
			return
		}
		d.apiLogout(w, r)

	// ---------- pages ----------
	case p == "/" && m == http.MethodGet:
		d.requireUser(http.HandlerFunc(d.pageTunnels)).ServeHTTP(w, r)
	case p == "/t" || strings.HasPrefix(p, "/t/"):
		if methodMismatch(w, r, http.MethodGet) {
			return
		}
		d.requireUser(http.HandlerFunc(d.pageTunnelDetail)).ServeHTTP(w, r)
	case p == "/users" && m == http.MethodGet:
		d.requireAdmin(http.HandlerFunc(d.pageUsers)).ServeHTTP(w, r)

	// ---------- tunnels api ----------
	case p == "/api/me" && m == http.MethodGet:
		d.requireUser(http.HandlerFunc(d.apiMe)).ServeHTTP(w, r)
	case p == "/api/tunnels" && m == http.MethodGet:
		d.requireUser(http.HandlerFunc(d.apiListTunnels)).ServeHTTP(w, r)
	case p == "/api/tunnels" && m == http.MethodPost:
		d.requireUser(http.HandlerFunc(d.apiCreateTunnel)).ServeHTTP(w, r)
	case strings.HasPrefix(p, "/api/tunnels/"):
		action := pathSeg(r, 3) // "" | config | reset-key | repair | mappings
		switch action {
		case "":
			if methodMismatch(w, r, http.MethodGet, http.MethodPatch, http.MethodDelete) {
				return
			}
			switch m {
			case http.MethodGet:
				d.requireUser(d.apiHandler(d.apiGetTunnel)).ServeHTTP(w, r)
			case http.MethodPatch:
				d.requireAdmin(d.apiHandler(d.apiPatchTunnel)).ServeHTTP(w, r)
			case http.MethodDelete:
				d.requireAdmin(d.apiHandler(d.apiDeleteTunnel)).ServeHTTP(w, r)
			}
		case "config":
			if methodMismatch(w, r, http.MethodGet) {
				return
			}
			d.requireUser(http.HandlerFunc(d.apiTunnelConfig)).ServeHTTP(w, r)
		case "reset-key", "repair":
			if methodMismatch(w, r, http.MethodPost) {
				return
			}
			if action == "reset-key" {
				d.requireAdmin(d.apiHandler(d.apiResetKey)).ServeHTTP(w, r)
			} else {
				d.requireAdmin(d.apiHandler(d.apiRepair)).ServeHTTP(w, r)
			}
		case "mappings":
			if methodMismatch(w, r, http.MethodPost) {
				return
			}
			d.requireAdmin(d.apiHandler(d.apiAddMapping)).ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}

	// ---------- mappings api ----------
	case strings.HasPrefix(p, "/api/mappings/"):
		if methodMismatch(w, r, http.MethodPatch, http.MethodDelete) {
			return
		}
		if m == http.MethodPatch {
			d.requireAdmin(d.apiHandler(d.apiPatchMapping)).ServeHTTP(w, r)
		} else {
			d.requireAdmin(d.apiHandler(d.apiDeleteMapping)).ServeHTTP(w, r)
		}

	// ---------- users api ----------
	case p == "/api/users" && m == http.MethodGet:
		d.requireAdmin(http.HandlerFunc(d.apiListUsers)).ServeHTTP(w, r)
	case p == "/api/users" && m == http.MethodPost:
		d.requireAdmin(http.HandlerFunc(d.apiCreateUser)).ServeHTTP(w, r)
	case strings.HasPrefix(p, "/api/users/"):
		if methodMismatch(w, r, http.MethodPatch, http.MethodDelete) {
			return
		}
		if m == http.MethodPatch {
			d.requireAdmin(d.apiHandler(d.apiPatchUser)).ServeHTTP(w, r)
		} else {
			d.requireAdmin(d.apiHandler(d.apiDeleteUser)).ServeHTTP(w, r)
		}

	default:
		http.NotFound(w, r)
	}
}

// ---------- deploy endpoint rate limiting (simple per-IP counter) ----------

type deployCounter struct {
	windowStart int64
	count       int
}

const deployMaxPerMinute = 30

func nowUnix() int64 { return time.Now().Unix() }

func (d *Dashboard) deployAllowed(ip string) bool {
	d.deployMu.Lock()
	defer d.deployMu.Unlock()
	now := nowUnix()
	c, ok := d.deployHits[ip]
	if !ok || now-c.windowStart >= 60 {
		d.deployHits[ip] = &deployCounter{windowStart: now, count: 1}
		return true
	}
	c.count++
	if c.count > deployMaxPerMinute {
		return false
	}
	return true
}

// sanitizeDLName only allows plain file names under the dl directory.
func sanitizeDLName(name string) (string, bool) {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return "", false
	}
	return name, true
}
