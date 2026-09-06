package dashboard

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	htmpl "html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	ttmpl "text/template"
	"time"
)

// ---------- template helpers ----------

var funcMap = htmpl.FuncMap{
	"fmtTime":  func(t time.Time) string { return t.Format("2006-01-02 15:04") },
	"fmtBytes": humanBytes,
	// staticV: 版本戳 (进程启动时间), 让静态资源 URL 在每次重启后变化,
	// 避免浏览器缓存旧版 app.js/style.css
	"staticV": func() string { return staticVer },
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

// staticVer is the per-process static asset version stamp.
var staticVer = strconv.FormatInt(time.Now().UnixNano(), 36)

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
	Apps          []AppOption // 映射表单「关联应用」下拉
}

// AppOption is a slim app descriptor for select dropdowns.
type AppOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// appOptionsFor lists the apps selectable by the user in mapping forms:
// users pick their own apps; admins may pick any app owned by the tunnel
// owner (binding is validated server-side again on submit).
func (d *Dashboard) appOptionsFor(u *User, tunnel *Tunnel) []AppOption {
	var ownerID string
	if tunnel != nil {
		ownerID = tunnel.OwnerID
	} else {
		ownerID = u.ID
	}
	var out []AppOption
	for _, a := range d.store.Apps(ownerID, u.Role == "admin") {
		out = append(out, AppOption{ID: a.ID, Name: a.Name, Type: a.Type})
	}
	return out
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
	tskill, err := ttmpl.New("skill").Parse(skillTmpl)
	if err != nil {
		return err
	}
	d.skillTmpl = tskill
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
		d.sessions.Issue(w, u.Username, d.isSecure(r))
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
		mv := MappingView{Mapping: m, PublicURL: rt.Active[m.ID], Error: rt.Errors[m.ID]}
		if m.AppID != "" {
			if a := d.store.AppByID(m.AppID); a != nil {
				mv.AppName = a.Name
			}
		}
		detail.Mappings = append(detail.Mappings, mv)
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
		Apps:       d.appOptionsFor(u, t),
	}
	for _, day := range data.Weekly {
		if day.Bytes > data.MaxWeekly {
			data.MaxWeekly = day.Bytes
		}
	}
	d.tpl.ExecuteTemplate(w, "page_tunnel_detail", data)
}

func (d *Dashboard) pageKeys(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	base := baseURL(r)
	items := make([]keyPageItem, 0)
	for _, k := range d.store.ApiKeys(u.ID, u.Role == "admin") {
		items = append(items, keyPageItem{
			apiKeyView: d.apiKeyView(k),
			InstallPrompt: fmt.Sprintf(
				"请安装 oneNat 技能: 执行 curl -s \"%s/skill/onenat.md?key=%s\" -o onenat-skill.md, "+
					"阅读 onenat-skill.md 并按其中说明查询和使用我的隧道资源。"+
					"注意: 你只有资源的使用权限, 没有创建、修改或删除权限。",
				base, k.Key),
		})
	}
	d.tpl.ExecuteTemplate(w, "page_keys", &keysPageData{
		Page: "keys", User: u, IsAdmin: u.Role == "admin", Keys: items, BaseURL: base,
	})
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
	// no-cache forces revalidation on every load so an upgraded server
	// never leaves clients running a stale app.js (embedded assets change
	// only with a new binary, but heuristic browser caching would
	// otherwise keep serving the old one)
	w.Header().Set("Cache-Control", "no-cache")
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
	d.installTmpl.Execute(w, map[string]string{
		"BaseURL":       baseURL(r),
		"ChecksumTable": d.dlChecksumTableSh(),
	})
}

func (d *Dashboard) handleInstallPs1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	d.installPs1Tmpl.Execute(w, map[string]string{
		"BaseURL":       baseURL(r),
		"ChecksumTable": d.dlChecksumTablePs1(),
	})
}

// dlChecksumTableSh renders a POSIX-sh case-table baking the MD5 of every
// client binary in the dl dir into the installer, so the script can skip
// re-downloading an unchanged binary and verify the download afterwards.
func (d *Dashboard) dlChecksumTableSh() string {
	var b strings.Builder
	for _, n := range d.dlClientBinaries() {
		sum, err := fileMD5(filepath.Join(d.opts.DlDir, n))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "    %s) echo %q ;;\n", n, sum)
	}
	return b.String()
}

// dlChecksumTablePs1 is the PowerShell hashtable variant of the same table.
func (d *Dashboard) dlChecksumTablePs1() string {
	var b strings.Builder
	for _, n := range d.dlClientBinaries() {
		sum, err := fileMD5(filepath.Join(d.opts.DlDir, n))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "  %q = %q\n", n, sum)
	}
	return b.String()
}

// dlClientBinaries lists ngrok_* client binaries in the dl dir (sorted).
func (d *Dashboard) dlClientBinaries() []string {
	entries, err := os.ReadDir(d.opts.DlDir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "ngrok_") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func fileMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// installScriptTmpl is the one-click installer. {{.BaseURL}} is baked at
// request time so the same server serves any access host.
const installScriptTmpl = `#!/bin/sh
# ngrok managed-client one-click installer (generated by oneNat dashboard)
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

# ---- 客户端 MD5 清单 (服务端渲染时从 dl 目录计算烘入) ----
md5_of_remote() {
  case "$1" in
{{.ChecksumTable}}    *) echo "" ;;
  esac
}
# 跨平台 MD5: Linux=md5sum, macOS=md5 -q, 缺工具返回空
md5_of_file() {
  if command -v md5sum >/dev/null 2>&1; then md5sum "$1" 2>/dev/null | awk '{print $1}'
  elif command -v md5 >/dev/null 2>&1; then md5 -q "$1" 2>/dev/null
  else echo ""; fi
}

IS_ROOT=0; [ "$(id -u)" = "0" ] && IS_ROOT=1
if [ "$IS_ROOT" = "1" ] || [ -w /usr/local/bin ] 2>/dev/null; then
  DEST=/usr/local/bin/ngrok
else
  DEST="$HOME/.local/bin/ngrok"; mkdir -p "$(dirname "$DEST")"
fi
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

download_client() {
  if command -v curl >/dev/null 2>&1; then
    curl -# -fSL "$SERVER/dl/$BIN_NAME" -o "$TMP/ngrok"
  elif command -v wget >/dev/null 2>&1; then
    wget --progress=bar:force -q --show-progress "$SERVER/dl/$BIN_NAME" -O "$TMP/ngrok" 2>&1 || wget -q "$SERVER/dl/$BIN_NAME" -O "$TMP/ngrok"
  else
    echo "未找到 curl 或 wget"
    exit 1
  fi
  chmod +x "$TMP/ngrok"
  if [ -n "$REMOTE_MD5" ]; then
    GOT=$(md5_of_file "$TMP/ngrok")
    if [ -z "$GOT" ]; then
      echo "   (本地无 md5 工具, 跳过下载后校验)"
    elif [ "$GOT" != "$REMOTE_MD5" ]; then
      echo "下载校验失败: 期望 MD5=$REMOTE_MD5, 实际=$GOT"
      exit 1
    else
      echo "   MD5 校验通过: $GOT"
    fi
  fi
  mv "$TMP/ngrok" "$DEST"
  echo "   客户端: $DEST"
}

echo ">> [1/4] 客户端 $BIN_NAME ..."
REMOTE_MD5=$(md5_of_remote "$BIN_NAME")
if [ -x "$DEST" ] && [ -n "$REMOTE_MD5" ]; then
  LOCAL_MD5=$(md5_of_file "$DEST")
  if [ -n "$LOCAL_MD5" ] && [ "$LOCAL_MD5" = "$REMOTE_MD5" ]; then
    echo "   ✓ 客户端已是最新版本 (MD5 一致), 跳过下载: $DEST"
  else
    echo "   本地客户端与服务器版本不一致, 重新下载 ..."
    download_client
  fi
else
  if [ ! -x "$DEST" ]; then
    echo "   未检测到已安装客户端, 开始下载 ..."
  else
    echo "   无版本校验值, 重新下载 ..."
  fi
  download_client
fi

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
const installPs1Tmpl = `# ngrok managed-client one-click installer for Windows (generated by oneNat dashboard)
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

# ---- 客户端 MD5 清单 (服务端渲染时从 dl 目录计算烘入) ----
$MD5Table = @{
{{.ChecksumTable}}}

if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA "ngrok" }
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$exePath = Join-Path $InstallDir "ngrok.exe"
$cfgPath = Join-Path $InstallDir "ngrok-managed.yml"

# 停掉本安装目录下的旧进程, 避免文件占用
Get-Process ngrok -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $exePath } | Stop-Process -Force -ErrorAction SilentlyContinue

Write-Host ">> [1/4] 客户端 $exeName ..."
$wantMd5 = $MD5Table[$exeName]
$skip = $false
if ($wantMd5 -and (Test-Path $exePath)) {
  $haveMd5 = (Get-FileHash -Algorithm MD5 -Path $exePath).Hash.ToLower()
  if ($haveMd5 -eq $wantMd5) {
    Write-Host "   客户端已是最新版本 (MD5 一致), 跳过下载: $exePath"
    $skip = $true
  }
}
if (-not $skip) {
  Invoke-WebRequest -UseBasicParsing -Uri "$BaseURL/dl/$exeName" -OutFile "$exePath.tmp"
  if ($wantMd5) {
    $gotMd5 = (Get-FileHash -Algorithm MD5 -Path "$exePath.tmp").Hash.ToLower()
    if ($gotMd5 -ne $wantMd5) {
      Write-Host "下载校验失败: 期望 MD5=$wantMd5, 实际=$gotMd5"
      Remove-Item "$exePath.tmp" -Force -ErrorAction SilentlyContinue
      exit 1
    }
    Write-Host "   MD5 校验通过: $gotMd5"
  }
  Move-Item -Force "$exePath.tmp" $exePath
  Write-Host "   客户端: $exePath"
}

Write-Host ">> [2/4] 拉取部署配置 ..."
Invoke-WebRequest -UseBasicParsing -Uri "$BaseURL/api/deploy?id=$TunnelId&key=$Key" -OutFile $cfgPath
if (-not (Select-String -Path $cfgPath -Pattern "server_addr" -Quiet)) {
  Write-Host "配置拉取失败:"
  Get-Content $cfgPath
  exit 1
}

Write-Host ">> [3/4] 注册当前用户自启动 (无需管理员) ..."

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

// skillTmpl is the AI-agent skill document. It teaches the agent how to
// discover and USE the api-key owner's tunnel resources over HTTP. The
// credential is injected from runtime data at render time; the key only
// ever grants read-only access (resource listing + this document).
const skillTmpl = `---
name: onenat
description: 通过 oneNat 平台发现并使用用户的内网隧道资源 (SSH / Web 等). 提供资源查询 HTTP 接口与连接方式. 只读: 仅可查询与连接现有资源, 无创建/修改/删除权限.
---

# oneNat 隧道资源使用技能

本技能授予你对用户「{{.User}}」名下隧道资源的**只读使用权限**。
你可以查询资源列表并连接这些资源；**没有**创建、修改、删除隧道或端口的
权限，也不要尝试此类操作（接口会拒绝）。

## 认证

- API Base: {{.BaseURL}}
- API KEY: {{.Key}}
- 认证方式: 请求头 ` + "`Authorization: Bearer {{.Key}}`" + ` (也支持 ` + "`?key={{.Key}}`" + ` 查询参数)

## 1. 获取资源列表 (隧道 ↔ 应用绑定关系的实时来源)

` + "`" + `bash
curl -s -H "Authorization: Bearer {{.Key}}" {{.BaseURL}}/api/v1/resources
` + "`" + `

返回 JSON 结构 — **每个映射 (mapping) 内嵌它绑定的应用 ` + "`app`" + `**,
应用里带技能清单 ` + "`skills[]`" + ` (含下载 url):

` + "`" + `json
{
  "base_url": "{{.BaseURL}}",
  "tunnels": [
    {
      "id": "...", "name": "...", "note": "用途说明", "online": true,
      "mappings": [
        {
          "proto": "tcp", "public_url": "tcp://host:port",
          "local": "127.0.0.1:22", "note": "ssh",
          "app": {
            "id": "app-xxxx", "name": "SSH Server", "type": "ssh",
            "description": "...", "internal_url": "http://127.0.0.1:22",
            "skills": [
              {"name": "usage.md", "size": 2048,
               "url": "{{.BaseURL}}/api/v1/apps/app-xxxx/skills/usage.md/content"}
            ]
          }
        }
      ]
    }
  ]
}
` + "`" + `

> 映射没有绑定应用时 ` + "`app`" + ` 字段缺省 —— 此时端口用途请向用户确认。

## 2. 如何连接

- ` + "`proto=tcp`" + ` (SSH/TCP 类): 取 public_url 里的 host 与 port。
  SSH 示例: ` + "`ssh user@host -p PORT`" + ` (登录凭据由用户另行提供，技能只提供入口)。
  其他 TCP 服务用 ` + "`nc host PORT`" + ` 探测或连接。
- ` + "`proto=http`" + ` (Web 类): 直接 ` + "`curl http://public_url`" + ` 访问。
- 应用有自己的服务地址时以 ` + "`app.internal_url`" + ` 为准 (公网入口转发到它)。
- ` + "`online=false`" + ` 或映射缺少 public_url 时，说明客户端当前离线，该资源暂不可达，
  请告知用户，不要反复重试。

## 3. 获取应用技能 (先读技能, 再用资源)

**平台的应用与技能是动态维护的**：主人随时会新增应用、更新技能、调整隧道与应用
的绑定。因此——

- **绑定关系与技能清单的实时来源是 ` + "`" + `/api/v1/resources` + "`" + `** (每个映射的 ` + "`" + `app` + "`" + ` 字段)；
- 本文档末尾的「应用目录」以及 ` + "`" + `/skill/index.md` + "`" + ` 都是**下载那一刻的快照**；
- 距离上次获取较久或刚做过重要变更时，请重新拉取 resources 或 index.md，
  以拿到最新的应用与技能。

每个端口映射背后登记了一个「应用」(数据库 / API / SSH 等真实系统)。
**使用任何应用之前，必须先下载并阅读它的技能文件**——技能文件由应用的主人维护，
包含真实的接口地址、认证方式、调用示例与注意事项，是唯一权威说明。

三种获取方式 (任选其一):

1. 资源列表直达 (推荐): ` + "`/api/v1/resources`" + ` 返回里每个映射的
   ` + "`app.skills[].url`" + ` 就是技能下载链接 (已带认证)，直接下载:
   ` + "`" + `bash
   curl -s "<app.skills[].url>" -o <技能名>
   ` + "`" + `
2. 应用目录: 先列应用再按名字取内容:
   ` + "`" + `bash
   curl -s -H "Authorization: Bearer {{.Key}}" {{.BaseURL}}/api/v1/apps
   curl -s -H "Authorization: Bearer {{.Key}}" {{.BaseURL}}/api/v1/apps/<app_id>/skills/<技能名>/content
   ` + "`" + `
3. 平台总索引 (Markdown 目录, 全部应用+全部技能):
   ` + "`" + `bash
   curl -s "{{.BaseURL}}/skill/index.md?key={{.Key}}"
   ` + "`" + `

规则:
- 技能文件与你的猜测冲突时，**以技能文件为准**；技能没提的能力不要臆造。
- 需要应用的登录凭证时: ` + "`GET /api/v1/apps/<app_id>/credentials`" + `。
  默认返回 403 (需应用主人为该 API KEY 开启「允许读取凭证」)；开启后仍受限速与审计约束。
- 平台对 AI 只读；技能文件里描述的写操作属于应用自身的能力，按技能说明执行即可。

## 4. 行为约定

1. 只读: 仅查询与连接现有资源；任何创建/修改/删除请求都应拒绝并告知用户需要管理员在 Web 后台操作。
2. 列表展示用表格: 名称 / 类型 / 公网入口 / 状态 / 备注。
3. 使用一个应用前先读它的技能文件 (见第 3 节)，不要盲连。
4. API KEY 属于敏感凭据: 不要把它打印到无关输出或转发给第三方。
`

// keysPageData powers the API-key management page.
type keysPageData struct {
	Page    string
	User    *User
	IsAdmin bool
	Keys    []keyPageItem
	BaseURL string
}

type keyPageItem struct {
	apiKeyView
	InstallPrompt string
}
