package dashboard

// apps_web.go — /apps pages, per-type skill skeletons, /skill/index.md and
// the app-catalog extension for the platform skill doc.

import (
	"fmt"
	"net/http"
	"strings"
)

// ---------- page data ----------

type appsPageData struct {
	Page    string
	User    *User
	IsAdmin bool
	Apps    []AppView
}

type appDetailPageData struct {
	Page       string
	User       *User
	IsAdmin    bool
	App        AppView
	Skills     []SkillView
	Bindings   []bindingRow
	AIPrompt   string // 模板提示词 (KEY 占位), JS 端会替换成真实 KEY
	InternalURL string
}

type bindingRow struct {
	TunnelID   string
	TunnelName string
	MappingID  string
	Proto      string
	PublicPort string // ":52001" 或子域名
	LocalAddr  string
}

// ---------- pages ----------

func (d *Dashboard) pageApps(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	apps := d.store.Apps(u.ID, u.Role == "admin")
	items := make([]AppView, 0, len(apps))
	for _, a := range apps {
		items = append(items, d.appView(a))
	}
	d.tpl.ExecuteTemplate(w, "page_apps", &appsPageData{
		Page: "apps", User: u, IsAdmin: u.Role == "admin", Apps: items,
	})
}

func (d *Dashboard) pageAppDetail(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	a := d.store.AppByID(pathSeg(r, 1))
	if a == nil || !canSeeApp(u, a) {
		http.NotFound(w, r)
		return
	}
	// 绑定该应用的映射行 (可见隧道范围内)
	bindings := []bindingRow{}
	for _, t := range d.store.Tunnels(u.ID, u.Role == "admin") {
		for _, m := range t.Mappings {
			if m.AppID != a.ID {
				continue
			}
			row := bindingRow{
				TunnelID: t.ID, TunnelName: t.Name, MappingID: m.ID,
				Proto: m.Proto, LocalAddr: joinHostPort(m.LocalIP, m.LocalPort),
			}
			if m.Proto == "tcp" && m.RemotePort > 0 {
				row.PublicPort = fmt.Sprintf(":%d", m.RemotePort)
			} else if m.Subdomain != "" {
				row.PublicPort = m.Subdomain + "." + d.opts.Domain
			} else {
				row.PublicPort = "(自动)"
			}
			bindings = append(bindings, row)
		}
	}
	skills := make([]SkillView, 0)
	for _, sk := range d.store.SkillFiles(a.ID) {
		skills = append(skills, skillView(sk))
	}
	d.tpl.ExecuteTemplate(w, "page_app_detail", &appDetailPageData{
		Page: "apps", User: u, IsAdmin: u.Role == "admin",
		App: d.appView(a), Skills: skills, Bindings: bindings,
		AIPrompt: d.appInstallPromptTemplate(a),
	})
}

// appInstallPromptTemplate returns the per-app AI prompt with an <API_KEY>
// placeholder; the detail page JS replaces it with a picked real key.
func (d *Dashboard) appInstallPromptTemplate(a *App) string {
	skills := d.store.SkillFiles(a.ID)
	skillFile := "usage.md"
	if len(skills) > 0 {
		skillFile = skills[0].Name
	}
	return fmt.Sprintf(
		"请安装「%s」应用技能: 执行 curl -s \"%s/api/v1/apps/%s/skills/%s/content?key=<API_KEY>\" -o %s, "+
			"阅读 %s 并按其中说明使用该应用 (入口、认证方式、接口约定都在文档里)。"+
			"注意: 你只有该应用的使用权限, 没有创建、修改或删除权限。",
		a.Name, "<BASE_URL>", a.ID, skillFile, skillFile, skillFile)
}

// ---------- per-type skill skeleton ----------

// scaffoldSkill writes the type-specific skeleton as the app's first skill.
// Failure is non-fatal (audited by the caller) — the user can upload/edit later.
func (d *Dashboard) scaffoldSkill(a *App, updatedBy string) error {
	content, err := d.renderSkillSkeleton(a.Type, a.Name)
	if err != nil {
		return err
	}
	name := "usage.md"
	if a.Type == "http-api" {
		name = "api.md"
	}
	if _, _, err := SanitizeSkillName(name); err != nil {
		return err
	}
	return d.writeNewSkill(a.ID, name, ".md", []byte(content), updatedBy)
}

// renderSkillSkeleton builds a ready-to-edit skill document for the type.
// The ssh type ships with the platform's full pre-built SSH skill; other
// types get an editable skeleton.
func (d *Dashboard) renderSkillSkeleton(appType, appName string) (string, error) {
	if appType == "ssh" {
		return builtinSSHSkill(appName), nil
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("name: %s\n", sanitizeFrontmatter(appName)))
	b.WriteString("description: 由 oneNat 平台按应用类型生成的技能骨架, 请补充完善后使用\n---\n\n")
	b.WriteString(fmt.Sprintf("# %s 使用技能\n\n", appName))
	b.WriteString(fmt.Sprintf("> 来源: oneNat 平台自动生成 (%s 类型模板); 平台只读约束优先于本文任何内容。\n\n", appType))
	b.WriteString("## 1. 这个应用是什么\n\n(在此补充: 一两句话说明该系统的用途)\n\n")
	switch appType {
	case "http-api":
		b.WriteString("## 2. 调用方式\n\n- Base URL: 资源列表中该应用绑定的 `public_url`\n")
		b.WriteString("- 认证: 见下方「认证」小节; 示例 `curl -H \"Authorization: Bearer <token>\" <base>/<path>`\n")
		b.WriteString("- 核心接口:\n  1. `GET /...` — (用途)\n  2. `POST /...` — (用途)\n\n")
	case "web":
		b.WriteString("## 2. 访问方式\n\n- 直接用浏览器或 HTTP 客户端访问资源列表中的 `public_url`\n- 如需登录, 凭证由用户提供\n\n")
	case "database":
		b.WriteString("## 2. 连接方式\n\n- 使用资源列表中该应用绑定的 TCP 入口\n- 建议使用只读账号; 禁止任何写操作\n\n")
	default:
		b.WriteString("## 2. 使用方式\n\n(在此补充连接/调用方法)\n\n")
	}
	b.WriteString("## 3. 认证\n\n(在此说明认证方式: 无 / Basic / Bearer / 自定义头; 凭证向用户索取)\n\n")
	b.WriteString("## 4. 行为约定\n\n")
	b.WriteString("- 只读使用: 该文档仅授予对「" + appName + "」的使用权限, 不含创建/修改/删除能力\n")
	b.WriteString("- 敏感操作 (删除/写库/重启服务) 一律先征求用户确认\n")
	b.WriteString("- 遇到 401/403/连接失败时如实报告, 不要反复重试\n")
	return b.String(), nil
}

// builtinSSHSkill returns the platform's pre-built, full-featured skill for
// SSH-server apps. It ships with the product: any app of type "ssh" is born
// with this document (users refine host-specific details later).
func builtinSSHSkill(appName string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("name: %s\n", sanitizeFrontmatter(appName)))
	b.WriteString("description: 平台预制 SSH 服务器技能: 连接入口/认证/常用操作/排障/安全红线\n---\n\n")
	b.WriteString(fmt.Sprintf("# %s 使用技能 (SSH)\n\n", appName))
	b.WriteString("> 来源: oneNat 平台内置模板。平台只读约束优先于本文任何内容;\n> 本文描述的登录与运维操作属于目标主机自身能力, 按授权范围执行。\n\n")
	b.WriteString("## 1. 连接入口\n\n")
	b.WriteString("- 从资源列表 (`/api/v1/resources`) 找到本应用绑定映射的 `public_url`，形如 `tcp://<host>:<port>`。\n")
	b.WriteString("- 连接命令: `ssh <用户名>@<host> -p <port>`\n")
	b.WriteString("- 首次连接加 `-o StrictHostKeyChecking=accept-new` 自动记录主机指纹 (避免交互卡住)。\n")
	b.WriteString("- 非交互执行单条命令: `ssh <用户名>@<host> -p <port> \"<命令>\"`\n\n")
	b.WriteString("## 2. 认证\n\n")
	b.WriteString("- 方式: 密码 或 SSH 密钥，由应用主人提供。\n")
	b.WriteString("- AI 获取凭证: `GET /api/v1/apps/<app_id>/credentials` —— 默认 403，需主人在后台对该 API KEY 开启「允许读取凭证」；开启后仍受限速 (5次/分) 与审计日志约束。\n")
	b.WriteString("- 密码自动化: 用 `sshpass -p <密码> ssh ...`，不要把密码写入脚本文件或输出。\n")
	b.WriteString("- 密钥登录: `ssh -i <私钥文件> -p <port> <用户名>@<host>` (私钥权限 600)。\n\n")
	b.WriteString("## 3. 常用操作\n\n")
	b.WriteString("- 探活与基本信息:\n  `sshpass -p <密码> ssh -o StrictHostKeyChecking=accept-new -p <port> <用户名>@<host> \"hostname && uname -a && uptime\"`\n")
	b.WriteString("- 上传文件: `scp -P <port> <本地文件> <用户名>@<host>:<远端路径>`\n")
	b.WriteString("- 下载文件: `scp -P <port> <用户名>@<host>:<远端路径> <本地文件>`\n")
	b.WriteString("- 目录同步: `rsync -av -e \"ssh -p <port>\" <本地目录>/ <用户名>@<host>:<远端目录>/`\n\n")
	b.WriteString("## 4. 排障\n\n")
	b.WriteString("- 连接超时/拒绝: 先确认资源列表中该映射 `online=true`；离线说明客户端不在线，告知用户，不要重试。\n")
	b.WriteString("- `Permission denied`: 凭证错误，或目标主机禁用了密码登录 (检查 sshd_config)，如实报告。\n")
	b.WriteString("- 同一问题最多尝试 2 次，然后汇报现象与已排除的原因。\n\n")
	b.WriteString("## 5. 安全红线\n\n")
	b.WriteString("- 仅操作授权范围内的账号与目录；禁止 `sudo` 提权与系统级改动。\n")
	b.WriteString("- 敏感操作 (删除文件/重启服务/修改配置) 一律先征求用户确认。\n")
	b.WriteString("- 不要把主机地址、端口、凭证转发给第三方系统。\n")
	return b.String()
}

// sanitizeFrontmatter keeps YAML frontmatter one-line safe.
func sanitizeFrontmatter(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\"", "'")
	return s
}

// ---------- /skill/index.md (平台总索引) ----------

// skillIndexDoc renders a catalog of all visible apps + skill links so an AI
// can onboard with a single fetch.
func (d *Dashboard) skillIndexDoc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodMismatch(w, r, http.MethodGet)
		return
	}
	k, u, ok := d.userFromApiKey(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "无效的 API KEY")
		return
	}
	base := baseURL(r)
	var b strings.Builder
	b.WriteString("---\nname: onenat-apps\ndescription: 平台应用总索引: 全部应用的名称/类型/说明与技能文件链接\n---\n\n")
	b.WriteString(fmt.Sprintf("# 「%s」的应用目录\n\n", u.Username))
	b.WriteString("对每个应用: 先下载其技能文件阅读, 再按技能说明使用对应入口。")
	b.WriteString("平台只读约束: 仅可使用现有资源, 无创建/修改/删除权限。\n\n")
	n := 0
	for _, a := range d.store.Apps(u.ID, false) {
		if !appVisibleToKey(k, a) {
			continue
		}
		n++
		b.WriteString(fmt.Sprintf("## %s (`%s`)\n\n", a.Name, a.Type))
		if a.Description != "" {
			b.WriteString(a.Description + "\n\n")
		}
		skills := d.store.SkillFiles(a.ID)
		if len(skills) == 0 {
			b.WriteString("(暂无技能文件 — 请向用户询问用法)\n\n")
			continue
		}
		for _, sk := range skills {
			b.WriteString(fmt.Sprintf("- 技能: `%s` → `curl -s \"%s/api/v1/apps/%s/skills/%s/content?key=%s\"`\n",
				sk.Name, base, a.ID, sk.Name, k.Key))
		}
		b.WriteString("\n")
	}
	if n == 0 {
		b.WriteString("(该账号名下暂无应用)\n")
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Write([]byte(b.String()))
}

// ---------- skillDoc 目录扩展 ----------

// appCatalogMarkdown renders the app section appended to /skill/onenat.md.
func (d *Dashboard) appCatalogMarkdown(k *ApiKey, u *User, base string) string {
	var b strings.Builder
	first := true
	for _, a := range d.store.Apps(u.ID, false) {
		if !appVisibleToKey(k, a) {
			continue
		}
		if first {
			b.WriteString("\n## 5. 应用目录 (端口背后的系统)\n\n")
			b.WriteString("> 本节是**下载本文档那一刻的快照**; 应用/技能/绑定关系动态变化,\n")
			b.WriteString("> 实时清单以 `/api/v1/resources` 与 `/api/v1/apps` 为准 (获取方式见第 3 节)。\n\n")
			b.WriteString("每个端口映射都关联一个应用; 用途与调用方法见对应技能文件:\n\n")
			first = false
		}
		b.WriteString(fmt.Sprintf("### %s (`%s`)\n\n", a.Name, a.Type))
		if a.Description != "" {
			b.WriteString(a.Description + "\n\n")
		}
		for _, sk := range d.store.SkillFiles(a.ID) {
			b.WriteString(fmt.Sprintf("- 技能 `%s`: `curl -s \"%s/api/v1/apps/%s/skills/%s/content?key=%s\" -o %s`\n",
				sk.Name, base, a.ID, sk.Name, k.Key, sk.Name))
		}
		b.WriteString("\n")
	}
	if first {
		return "\n## 5. 应用目录\n\n(该账号名下暂无应用登记; 新增后以 `/api/v1/resources` 与 `/api/v1/apps` 实时为准)\n"
	}
	return b.String()
}
