package dashboard

import (
	"encoding/json"
	htmpl "html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	ttmpl "text/template"
	"time"
)

// ---------- template helpers ----------

var funcMap = htmpl.FuncMap{
	"fmtTime":  func(t time.Time) string { return t.Format("2006-01-02 15:04") },
	"fmtBytes": humanBytes,
	"div64": func(a, b, scale int64) int64 {
		if b <= 0 {
			return 0
		}
		p := a * scale / b
		if p > 100 {
			p = 100
		}
		return p
	},
	"toJSON": func(v interface{}) htmpl.JS {
		b, _ := json.Marshal(v)
		return htmpl.JS(b)
	},
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return itoa64(n) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	return fmt1f(v) + " " + "KMGTPE"[exp:exp+1] + "B"
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [21]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func fmt1f(v float64) string {
	whole := int64(v)
	frac := int64((v - float64(whole)) * 10)
	return itoa64(whole) + "." + itoa64(frac)
}

// ---------- page data ----------

type loginPageData struct {
	Error string
}

type tunnelsPageData struct {
	Page    string
	User    *User
	IsAdmin bool
	Q       string
	Status  string
	Tunnels []TunnelListItem
	Users   []*User // admin: owner select in create modal
}

type tunnelDetailPageData struct {
	Page          string
	User          *User
	IsAdmin       bool
	T             *TunnelDetail
	InstallCmd    string
	InstallCmdWin string
	BaseURL       string
	OwnerName     string
	ConnRows      []ConnRecord
	Weekly        []DayTraffic
	MaxWeekly     int64
}

type usersPageData struct {
	Page         string
	User         *User
	IsAdmin      bool
	Users        []*User
	TunnelCounts map[string]int
}

// ---------- templates ----------

func (d *Dashboard) parseTemplates() error {
	tpl, err := htmpl.New("dash").Funcs(funcMap).ParseFS(assetsFS, "assets/templates/*.html")
	if err != nil {
		return err
	}
	d.tpl = tpl
	tinstall, err := ttmpl.New("install").Parse(installScriptTmpl)
	if err != nil {
		return err
	}
	d.installTmpl = tinstall
	tps1, err := ttmpl.New("installps1").Parse(installPs1Tmpl)
	if err != nil {
		return err
	}
	d.installPs1Tmpl = tps1
	return nil
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// ---------- pages ----------

func (d *Dashboard) pageLogin(w http.ResponseWriter, r *http.Request) {
	if d.UserFromRequest(r) != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	d.tpl.ExecuteTemplate(w, "page_login", &loginPageData{})
}

func (d *Dashboard) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	username := r.FormValue("username")
	password := r.FormValue("password")
	u := d.store.UserByName(username)
	if u == nil || !VerifyPassword(password, u.PassHash) {
		d.tpl.ExecuteTemplate(w, "page_login", &loginPageData{Error: "用户名或密码错误"})
		return
	}
	d.sessions.Issue(w, u.Username)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (d *Dashboard) pageTunnels(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	admin := u.Role == "admin"
	q := r.URL.Query().Get("q")
	status := r.URL.Query().Get("status")

	all := d.store.Tunnels(u.ID, admin)
	items := make([]TunnelListItem, 0, len(all))
	for _, t := range all {
		item := d.tunnelListItem(t)
		if q != "" && !strings.Contains(strings.ToLower(t.Name+t.ID), strings.ToLower(q)) {
			continue
		}
		switch status {
		case "online":
			if !item.Online {
				continue
			}
		case "offline":
			if item.Online {
				continue
			}
		case "locked":
			if !item.Locked {
				continue
			}
		}
		items = append(items, item)
	}

	data := &tunnelsPageData{
		Page: "tunnels", User: u, IsAdmin: admin, Q: q, Status: status, Tunnels: items,
	}
	if admin {
		data.Users = d.store.Users()
	}
	d.tpl.ExecuteTemplate(w, "page_tunnels", data)
}

func (d *Dashboard) tunnelDetail(t *Tunnel) *TunnelDetail {
	item := d.tunnelListItem(t)
	detail := &TunnelDetail{
		TunnelListItem: item,
		Key:            t.Key,
		Node:           d.opts.Domain,
		Runtime:        d.RuntimeView(t.ID),
	}
	rt := detail.Runtime
	for _, m := range t.Mappings {
		detail.Mappings = append(detail.Mappings, MappingView{
			Mapping:   m,
			PublicURL: rt.Active[m.ID],
			Error:     rt.Errors[m.ID],
		})
	}
	return detail
}

func (d *Dashboard) pageTunnelDetail(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	t := d.store.TunnelByID(pathSeg(r, 1))
	if t == nil || !canSeeTunnel(u, t) {
		http.NotFound(w, r)
		return
	}
	td := d.tunnelDetail(t)
	base := baseURL(r)
	data := &tunnelDetailPageData{
		Page:       "tunnels",
		User:       u,
		IsAdmin:    u.Role == "admin",
		T:          td,
		BaseURL:    base,
		InstallCmd: "curl -sSL " + base + "/install.sh | bash -s -- " + t.ID + " " + t.Key,
		InstallCmdWin: `powershell -NoProfile -ExecutionPolicy Bypass -Command "& ([scriptblock]::Create((irm '` + base + `/install.ps1'))) -TunnelId '` + t.ID + `' -Key '` + t.Key + `'"`,
		OwnerName:  td.OwnerName,
		ConnRows:   td.Runtime.Conns,
		Weekly:     td.Runtime.WeeklyTraffic,
	}
	for _, day := range data.Weekly {
		if day.Bytes > data.MaxWeekly {
			data.MaxWeekly = day.Bytes
		}
	}
	d.tpl.ExecuteTemplate(w, "page_tunnel_detail", data)
}

func (d *Dashboard) pageUsers(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	counts := map[string]int{}
	for _, t := range d.store.Tunnels("", true) {
		if owner := d.store.UserByID(t.OwnerID); owner != nil {
			counts[owner.Username]++
		}
	}
	d.tpl.ExecuteTemplate(w, "page_users", &usersPageData{
		Page: "users", User: u, IsAdmin: u.Role == "admin",
		Users: d.store.Users(), TunnelCounts: counts,
	})
}

// ---------- static / downloads / install script ----------

func (d *Dashboard) handleStatic(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(assetsFS, "assets/static")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// the sub-FS is rooted at assets/static, so strip the /static/ prefix
	// before handing the request to FileServer (otherwise every asset 404s)
	http.StripPrefix("/static/", http.FileServer(http.FS(sub))).ServeHTTP(w, r)
}

func (d *Dashboard) handleDownload(w http.ResponseWriter, r *http.Request) {
	name, ok := sanitizeDLName(strings.TrimPrefix(r.URL.Path, "/dl/"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(d.opts.DlDir, name)
	if _, err := os.Stat(path); err != nil {
		writeErr(w, http.StatusNotFound, "客户端二进制不存在: "+name+" — 请将交叉编译产物放入 -dlDir 目录")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	http.ServeFile(w, r, path)
}

func (d *Dashboard) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	d.installTmpl.Execute(w, map[string]string{"BaseURL": baseURL(r)})
}

func (d *Dashboard) handleInstallPs1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	d.installPs1Tmpl.Execute(w, map[string]string{"BaseURL": baseURL(r)})
}

// installScriptTmpl is the one-click installer. {{.BaseURL}} is baked at
// request time so the same server serves any access host.
const installScriptTmpl = `#!/bin/sh
# ngrok managed-client one-click installer (generated by ngrokd dashboard)
set -e
SERVER="{{.BaseURL}}"
TUNNEL_ID="${1:-}"; KEY="${2:-}"
# also accept env-var form: TUNNEL_ID=xx KEY=yy curl ... | bash
TUNNEL_ID="${TUNNEL_ID:-${TUNNEL_ID_ENV:-}}"; KEY="${KEY:-${KEY_ENV:-}}"
if [ -z "$TUNNEL_ID" ] || [ -z "$KEY" ]; then
  echo "用法: curl -sSL $SERVER/install.sh | bash -s -- <隧道ID> <KEY>"
  echo "或:   curl -sSL $SERVER/install.sh | TUNNEL_ID_ENV=<ID> KEY_ENV=<KEY> bash"
  exit 1
fi

OS=$(uname -s); ARCH=$(uname -m)
case "$OS" in Linux) os=linux ;; Darwin) os=darwin ;; *) echo "暂不支持的系统: $OS"; exit 1 ;; esac
case "$ARCH" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; armv7l) arch=arm ;; *) echo "暂不支持的架构: $ARCH"; exit 1 ;; esac
BIN_NAME="ngrok_${os}_${arch}"

IS_ROOT=0; [ "$(id -u)" = "0" ] && IS_ROOT=1
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

echo ">> [1/4] 下载客户端 $BIN_NAME ..."
curl -fsSL "$SERVER/dl/$BIN_NAME" -o "$TMP/ngrok" 2>/dev/null || wget -q "$SERVER/dl/$BIN_NAME" -O "$TMP/ngrok"
chmod +x "$TMP/ngrok"

if [ "$IS_ROOT" = "1" ] || [ -w /usr/local/bin ] 2>/dev/null; then
  DEST=/usr/local/bin/ngrok
else
  DEST="$HOME/.local/bin/ngrok"; mkdir -p "$(dirname "$DEST")"
fi
mv "$TMP/ngrok" "$DEST"
echo "   客户端: $DEST"

echo ">> [2/4] 拉取部署配置 ..."
if [ "$IS_ROOT" = "1" ] || [ -w /etc ] 2>/dev/null; then CFG_DIR=/etc/ngrok; else CFG_DIR="$HOME/.ngrok.d"; fi
mkdir -p "$CFG_DIR"
curl -fsSL "$SERVER/api/deploy?id=$TUNNEL_ID&key=$KEY" -o "$CFG_DIR/ngrok-managed.yml" \
  || wget -q "$SERVER/api/deploy?id=$TUNNEL_ID&key=$KEY" -O "$CFG_DIR/ngrok-managed.yml"
chmod 600 "$CFG_DIR/ngrok-managed.yml"
grep -q "server_addr" "$CFG_DIR/ngrok-managed.yml" || { echo "配置拉取失败:"; cat "$CFG_DIR/ngrok-managed.yml"; exit 1; }
echo "   配置: $CFG_DIR/ngrok-managed.yml"

echo ">> [3/4] 注册常驻服务 ..."
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  cat > /etc/systemd/system/ngrok-client.service <<UNIT
[Unit]
Description=ngrok managed client
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$DEST -config=$CFG_DIR/ngrok-managed.yml managed
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
  systemctl enable --now ngrok-client
  echo "   服务: ngrok-client (systemd)。查看: systemctl status ngrok-client"
elif [ "$os" = "darwin" ]; then
  PLIST="$HOME/Library/LaunchAgents/com.ngrok.client.plist"
  mkdir -p "$(dirname "$PLIST")"
  cat > "$PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.ngrok.client</string>
  <key>ProgramArguments</key><array>
    <string>$DEST</string><string>-config</string><string>$CFG_DIR/ngrok-managed.yml</string><string>managed</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/ngrok-client.log</string>
  <key>StandardErrorPath</key><string>/tmp/ngrok-client.log</string>
</dict></plist>
PLIST
  launchctl unload "$PLIST" 2>/dev/null || true
  launchctl load "$PLIST"
  echo "   服务: com.ngrok.client (launchd)。日志: /tmp/ngrok-client.log"
else
  pkill -f "ngrok.*managed" 2>/dev/null || true
  nohup "$DEST" -config="$CFG_DIR/ngrok-managed.yml" managed >/tmp/ngrok-client.log 2>&1 &
  echo "   已后台启动 (nohup)。日志: /tmp/ngrok-client.log"
fi

echo ">> [4/4] 完成! 稍候可在管理后台看到本隧道变为 [在线]。"
`

// installPs1Tmpl is the Windows one-click installer (PowerShell 5.1+ compatible).
// Per-user install to %LOCALAPPDATA%\ngrok + HKCU Run-key autostart, so it
// needs no administrator rights. {{.BaseURL}} is baked at request time.
const installPs1Tmpl = `# ngrok managed-client one-click installer for Windows (generated by ngrokd dashboard)
param(
  [Parameter(Mandatory=$true)][string]$TunnelId,
  [Parameter(Mandatory=$true)][string]$Key,
  [string]$InstallDir,
  [switch]$NoStart
)
$ErrorActionPreference = "Stop"
$BaseURL = "{{.BaseURL}}"
try {
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch {}

switch ($env:PROCESSOR_ARCHITECTURE) {
  "ARM64" { $arch = "arm64" }
  default { $arch = "amd64" }
}
$exeName = "ngrok_windows_${arch}.exe"

if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA "ngrok" }
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$exePath = Join-Path $InstallDir "ngrok.exe"
$cfgPath = Join-Path $InstallDir "ngrok-managed.yml"

Write-Host ">> [1/4] 下载客户端 $exeName ..."
Invoke-WebRequest -UseBasicParsing -Uri "$BaseURL/dl/$exeName" -OutFile $exePath

Write-Host ">> [2/4] 拉取部署配置 ..."
Invoke-WebRequest -UseBasicParsing -Uri "$BaseURL/api/deploy?id=$TunnelId&key=$Key" -OutFile $cfgPath
if (-not (Select-String -Path $cfgPath -Pattern "server_addr" -Quiet)) {
  Write-Host "配置拉取失败:"
  Get-Content $cfgPath
  exit 1
}

Write-Host ">> [3/4] 注册当前用户自启动 (无需管理员) ..."
# 停掉本安装目录下的旧进程, 避免文件占用
Get-Process ngrok -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $exePath } | Stop-Process -Force -ErrorAction SilentlyContinue

if ($PSVersionTable.Platform -eq "Unix") {
  Write-Host "   (当前非 Windows 环境, 跳过自启动注册与启动)"
} else {
  $runKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
  if (-not (Test-Path $runKey)) { New-Item -Path $runKey -Force | Out-Null }
  Set-ItemProperty -Path $runKey -Name "ngrok-client" -Value ('"' + $exePath + '" -config="' + $cfgPath + '" managed')
}

Write-Host ">> [4/4] 启动 ..."
if ($NoStart) {
  Write-Host "   已按 -NoStart 跳过启动; 手动运行: $exePath -config=$cfgPath managed"
} elseif ($PSVersionTable.Platform -eq "Unix") {
  # non-Windows pwsh 调试路径
} else {
  Start-Process -FilePath $exePath -ArgumentList @("-config", ('"' + $cfgPath + '"'), "managed") -WindowStyle Hidden
}

Write-Host ">> 完成! 稍候可在管理后台看到本隧道变为 [在线]。"
Write-Host "   安装目录: $InstallDir"
Write-Host "   卸载: 删除 $InstallDir 并移除注册表键 HKCU\...\Run 下的 ngrok-client"
`
