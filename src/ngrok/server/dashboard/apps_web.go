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
	Version string
	Apps    []AppView
}

type appDetailPageData struct {
	Page        string
	User        *User
	IsAdmin     bool
	Version     string
	App         AppView
	Skills      []SkillView
	Bindings    []bindingRow
	AIPrompt    string // 模板提示词 (KEY 占位), JS 端会替换成真实 KEY
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
		Page: "apps", User: u, IsAdmin: u.Role == "admin", Version: d.Version(), Apps: items,
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
		Page: "apps", User: u, IsAdmin: u.Role == "admin", Version: d.Version(),
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
	b.WriteString(fmt.Sprintf("> 来源: oneNat 平台自动生成 (%s 类型骨架); 平台只读约束优先于本文任何内容。\n\n", appType))
	b.WriteString("## 1. 这个应用是什么\n\n(补一句: 该系统的用途, 给 AI 判断何时该用它)\n\n")
	switch appType {
	case "http-api":
		b.WriteString("## 2. 调用方式\n\n- Base URL: 资源列表中该应用绑定的 `public_url`\n")
		b.WriteString("- 认证: 见第 3 节; 示例 `curl -H \"Authorization: Bearer <token>\" <base>/<path>`\n")
		b.WriteString("- 核心接口:\n  1. `GET /...` — (用途)\n  2. `POST /...` — (用途)\n\n")
	case "web":
		b.WriteString("## 2. 访问方式\n\n- 浏览器或 HTTP 客户端直接访问资源列表中的 `public_url`\n- 如需登录, 凭证由用户提供\n\n")
	case "database":
		b.WriteString("## 2. 连接方式\n\n- 用资源列表中该应用绑定的 TCP 入口\n- 只用只读账号; **禁止任何写操作**\n\n")
	default:
		b.WriteString("## 2. 使用方式\n\n(补一段: 连接/调用方法与最小示例)\n\n")
	}
	b.WriteString("## 3. 认证\n\n(补一行: 无 / Basic / Bearer / 自定义头; 凭证向用户索取, 不要写入输出)\n\n")
	b.WriteString("## 4. 行为约定\n\n")
	b.WriteString("- 只读使用: 该文档仅授予对「" + appName + "」的使用权限, 不含创建/修改/删除能力\n")
	b.WriteString("- 接口与行为以实测为准, 不要凭记忆猜测端点或字段\n")
	b.WriteString("- 敏感操作 (删除/写库/重启服务) 一律先征求用户确认\n")
	b.WriteString("- 401/403/连接失败时如实报告, 不要反复重试\n")
	return b.String(), nil
}

// builtinSSHSkillRev 内置 SSH 技能模板版本号。文档里会写入
// `<!-- builtin-rev: ... -->` 标记: 平台升级后可用 builtinSSHSkillStale()
// 找出仍是旧版内置模板的存量应用(用户自己改写过的文档不会被误判)。
const builtinSSHSkillRev = "2026-09-21-simplify"

// builtinSSHSkillStale 判断一份已存技能是否需要按当前模板刷新:
// 只认"仍是平台内置(带来源标记)且 rev 落后"的文档; 不含内置标记的视为
// 主人自己写/大改过的文档, 一律返回 false(不覆盖)。
func builtinSSHSkillStale(content string) bool {
	if !strings.Contains(content, "来源: oneNat 平台内置模板") {
		return false
	}
	return !strings.Contains(content, "builtin-rev: "+builtinSSHSkillRev)
}

// RenderBuiltinSSHSkill 导出内置 SSH 模板, 供离线工具刷新仓库里的模板拷贝
// (onenat-app-ssh-usage.md 等), 保证拷贝与服务端产出逐字节一致。
func RenderBuiltinSSHSkill(appName string) (string, error) {
	return builtinSSHSkill(appName), nil
}

// builtinSSHSkill returns the platform's pre-built, full-featured skill for
// SSH-server apps. It ships with the product: any app of type "ssh" is born
// with this document (users refine host-specific details later).
//
// 编写约定 (简精易懂): 决策用表格、流程给最小可执行命令、禁止长句嵌套;
// 改动后同步更新根目录模板拷贝 (onenat-app-ssh-usage.md 等) 并升 rev。
func builtinSSHSkill(appName string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("name: %s\n", sanitizeFrontmatter(appName)))
	b.WriteString("description: 平台预制 SSH 服务器技能: 连接入口/认证/常用操作/排障/安全红线\n---\n\n")
	b.WriteString(fmt.Sprintf("# %s 使用技能 (SSH)\n\n", appName))
	b.WriteString("> 来源: oneNat 平台内置模板。平台只读约束优先于本文任何内容;\n> 登录与运维操作属于目标主机自身能力, 按授权范围执行。\n")
	b.WriteString("<!-- builtin-rev: " + builtinSSHSkillRev + " -->\n\n")
	b.WriteString("## 1. 连接入口\n\n")
	b.WriteString("- 入口: 资源列表 (`/api/v1/resources`) 中本应用绑定映射的 `public_url`, 形如 `tcp://<host>:<port>`。\n")
	b.WriteString("- 连接: `ssh <用户名>@<host> -p <port> -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10` (首次自动记录指纹, 始终带超时; 非交互探测再加 `-o BatchMode=yes`, 失败立即返回不卡密码提示)。\n")
	b.WriteString("- 非交互执行单条命令: `ssh <用户名>@<host> -p <port> \"<命令>\"`。\n")
	b.WriteString("- 映射 `online=false` 时主机不可达: 告知用户, **不要重试**。\n\n")
	b.WriteString("## 2. 认证: 先判凭证来源, 再连接 (必读)\n\n")
	b.WriteString("同一个应用常被多条映射指向**不同机器**, 应用默认凭证往往只对其中一台有效:\n\n")
	b.WriteString("| 取法 | 接口 | 说明 |\n|---|---|---|\n")
	b.WriteString("| 映射实例凭证 (首选) | `GET /api/v1/mappings/<mapping_id>/credentials` | 返回该映射的**有效凭证**: 实例凭证优先, 未配则回退应用默认; `resolved_from` 字段标明来源 (`mapping` / `app`) |\n")
	b.WriteString("| 应用默认凭证 | `GET /api/v1/apps/<app_id>/credentials` | 兜底; 多映射共用时通常只对一台有效 |\n\n")
	b.WriteString("取凭证一律用**你要连的那条映射**的 id。返回 403 时: 请主人在后台为该 API KEY 开启「允许读取凭证」(开启后仍限速 5 次/分, 且记录审计日志)。\n\n")
	b.WriteString("连接前自检 (只看来源与用户名, 不打印密码):\n\n")
	b.WriteString("```bash\ncurl -s -H \"Authorization: Bearer $ONENAT_KEY\" \\\n  \"$BASE/api/v1/mappings/<mapping_id>/credentials\" \\\n  | python3 -c 'import json,sys;d=json.load(sys.stdin);print(\"resolved_from=%s username=%s pwd_len=%d\"%(d.get(\"resolved_from\"),d.get(\"username\"),len(d.get(\"password\") or \"\")))'\n```\n\n")
	b.WriteString("- `resolved_from=mapping` → 凭证正确, 直接连接。\n")
	b.WriteString("- `resolved_from=app` → 这是应用级共享凭证 (resources 里该映射 `auth_override=false`; `auth_inherited_shared=true` 表示确有别的映射共用)。用户名多半**不是目标机真实账号**, 连接必然 `Permission denied`: **停止尝试, 不要换用户名或密码反复猜**, 如实报告「该映射未配实例凭证, 请主人在 Web 后台为该映射填写实例凭证」, 并附自检输出。\n")
	b.WriteString("- 凭证会轮换或被改: 提示词/缓存里的凭证只是快照, 认证失败先重新取一次再试。\n")
	b.WriteString("- 密码自动化: `sshpass -p <密码> ssh ...`, 不要把密码写入脚本文件或输出; 密钥登录: `ssh -i <私钥文件> -p <port> <用户名>@<host>` (私钥权限 600)。\n\n")
	b.WriteString("## 3. 常用操作\n\n")
	b.WriteString("- 探活: `sshpass -p <密码> ssh -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 -p <port> <用户名>@<host> \"hostname && uname -a && uptime\"`\n")
	b.WriteString("- 上传: `scp -P <port> <本地文件> <用户名>@<host>:<远端路径>`; 下载把参数对调。\n")
	b.WriteString("- 目录同步: `rsync -av -e \"ssh -p <port>\" <本地目录>/ <用户名>@<host>:<远端目录>/`\n")
	b.WriteString("- 同一主机要跑多条命令时先建复用主连接, 后续秒连:\n  `ssh -o ControlMaster=auto -o ControlPath=/tmp/ssh-onenat-%r@%h:%p -o ControlPersist=10m -fN <用户名>@<host> -p <port>`\n\n")
	b.WriteString("## 4. 排障\n\n")
	b.WriteString("| 现象 | 动作 |\n|---|---|\n")
	b.WriteString("| 连接超时/拒绝 | 确认该映射 `online=true`; 离线说明客户端不在线, 告知用户, 不要重试 |\n")
	b.WriteString("| `Permission denied` | 先做第 2 节自检: `resolved_from=app` → 停止并报告缺实例凭证; 否则是凭证错误或目标机禁用密码登录 (查 sshd_config), 如实报告 |\n")
	b.WriteString("| `Connection reset by peer` | 映射刚变更或隧道重启: 重拉 `/api/v1/resources` 核对端口, 再试一次 |\n")
	b.WriteString("| 卡在密码提示 | 加 `-o BatchMode=yes` 探测; 密码登录用 `sshpass` |\n\n")
	b.WriteString("- 同一问题最多尝试 2 次, 然后汇报现象与已排除的原因。\n\n")
	b.WriteString("## 5. 安全红线\n\n")
	b.WriteString("- 仅操作授权范围内的账号与目录; 禁止 `sudo` 提权与系统级改动。\n")
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
			b.WriteString("\n## 7. 应用目录 (端口背后的系统, 实时生成)\n\n")
			b.WriteString("> 本节在每次获取时按当前绑定关系实时生成;\n")
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
		return "\n## 7. 应用目录\n\n(该账号名下暂无应用登记; 新增后以 `/api/v1/resources` 与 `/api/v1/apps` 实时为准)\n"
	}
	return b.String()
}
