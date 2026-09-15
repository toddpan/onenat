package dashboard

// 配置导入/导出: 应用 / 隧道 / 端口映射 (含实例级凭证覆盖与技能文件)。
// 目标是在多环境间迁移配置时避免重复手工录入。
//
// 安全语义:
//   - 导出仅管理员可用; 凭证以明文写入导出文件 (带显式警告), 因为跨部署
//     导入方没有源环境的凭证主密钥, 密文形态无法二次封存。
//   - 导入由 store 层用本地主密钥重新封存凭证; 明文只经过请求体, 不落日志。
//   - 导入按「名称」匹配既有实体, mode=skip (默认, 跳过同名) 或 update
//     (更新同名实体的元数据与凭证); 永不删除既有数据。隧道 KEY 一律重新生成。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ---------- 导出格式 ----------

const configExportFormat = "onenat-config"
const configExportVersion = 1

// ConfigExportAuth 明文凭证 (仅在导出文件中存在)。
type ConfigExportAuth struct {
	AuthType     string            `json:"auth_type"`
	Username     string            `json:"username,omitempty"`
	Password     string            `json:"password,omitempty"`
	ApiKey       string            `json:"api_key,omitempty"`
	ExtraHeaders map[string]string `json:"extra_headers,omitempty"`
}

type ConfigExportSkill struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type ConfigExportApp struct {
	Name        string              `json:"name"`
	Type        string              `json:"type"`
	Description string              `json:"description,omitempty"`
	InternalURL string              `json:"internal_url,omitempty"`
	Tags        []string            `json:"tags,omitempty"`
	Auth        *ConfigExportAuth   `json:"auth,omitempty"`
	Skills      []ConfigExportSkill `json:"skills,omitempty"`
}

type ConfigExportMapping struct {
	Proto      string            `json:"proto"`
	LocalIP    string            `json:"local_ip,omitempty"`
	LocalPort  int               `json:"local_port"`
	RemotePort int               `json:"remote_port,omitempty"`
	Subdomain  string            `json:"subdomain,omitempty"`
	Note       string            `json:"note,omitempty"`
	AppName    string            `json:"app_name"` // 按应用名解析绑定 (导入端重建 id)
	Auth       *ConfigExportAuth `json:"auth,omitempty"` // 仅映射级覆盖; 继承应用默认时省略
}

type ConfigExportTunnel struct {
	Name               string                 `json:"name"`
	Note               string                 `json:"note,omitempty"`
	Locked             bool                   `json:"locked,omitempty"`
	AllowRemoteTargets bool                   `json:"allow_remote_targets,omitempty"`
	Mappings           []ConfigExportMapping  `json:"mappings"`
}

type ConfigExport struct {
	Format             string               `json:"format"` // "onenat-config"
	Version            int                  `json:"version"` // 1
	ExportedAt         time.Time            `json:"exported_at"`
	IncludeCredentials bool                 `json:"include_credentials"`
	Apps               []ConfigExportApp    `json:"apps"`
	Tunnels            []ConfigExportTunnel `json:"tunnels"`
}

// ---------- 导出 ----------

// exportAuth converts a revealed plaintext credential; nil when unset.
func exportAuth(authType, username, password, apiKey string, extra map[string]string) *ConfigExportAuth {
	if authType == "" || authType == "none" {
		return nil
	}
	a := &ConfigExportAuth{AuthType: authType, Username: username, Password: password, ApiKey: apiKey, ExtraHeaders: extra}
	return a
}

// buildConfigExport assembles the export snapshot (caller holds no lock;
// reveal helpers take the store lock themselves).
func (d *Dashboard) buildConfigExport(u *User, includeCred bool) *ConfigExport {
	out := &ConfigExport{
		Format:             configExportFormat,
		Version:            configExportVersion,
		ExportedAt:         time.Now(),
		IncludeCredentials: includeCred,
		Apps:               []ConfigExportApp{},
		Tunnels:            []ConfigExportTunnel{},
	}

	appName := map[string]string{} // app id -> name
	for _, a := range d.store.Apps(u.ID, u.Role == "admin") {
		appName[a.ID] = a.Name
		ea := ConfigExportApp{
			Name: a.Name, Type: a.Type, Description: a.Description,
			InternalURL: a.InternalURL, Tags: a.Tags,
		}
		if includeCred {
			if username, password, apiKey, extra, err := d.store.RevealAppCredential(a.ID); err == nil {
				ea.Auth = exportAuth(a.Auth.AuthType, username, password, apiKey, extra)
			}
		}
		// 技能文件内容 (大小受限, 文本型)
		for _, sk := range d.store.SkillFiles(a.ID) {
			content, err := ReadSkillFile(d.skillsDirResolved(), a.ID, sk.ID, sk.Ext)
			if err != nil {
				continue
			}
			ea.Skills = append(ea.Skills, ConfigExportSkill{Name: sk.Name, Content: string(content)})
		}
		out.Apps = append(out.Apps, ea)
	}

	for _, t := range d.store.Tunnels(u.ID, u.Role == "admin") {
		et := ConfigExportTunnel{
			Name: t.Name, Note: t.Note, Locked: t.Locked,
			AllowRemoteTargets: t.AllowRemoteTargets,
			Mappings:           []ConfigExportMapping{},
		}
		for _, m := range t.Mappings {
			em := ConfigExportMapping{
				Proto: m.Proto, LocalIP: m.LocalIP, LocalPort: m.LocalPort,
				RemotePort: m.RemotePort, Subdomain: m.Subdomain, Note: m.Note,
			}
			if m.AppID != "" {
				em.AppName = appName[m.AppID]
			}
			if includeCred && m.Auth != nil {
				if _, _, _, _, source, extra, err := d.store.RevealMappingCredential(m.ID); err == nil && source == "mapping" {
					em.Auth = exportAuth(m.Auth.AuthType, "", "", "", extra)
					// RevealMappingCredential 只回明文字段; 用户名按覆盖自身补齐
					em.Auth.Username = m.Auth.Username
				}
			}
			et.Mappings = append(et.Mappings, em)
		}
		out.Tunnels = append(out.Tunnels, et)
	}
	return out
}

func (d *Dashboard) apiExportConfig(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	includeCred := r.URL.Query().Get("credentials") != "0"
	out := d.buildConfigExport(u, includeCred)
	d.AuditUser(r, u, "config.export", "", "ok", map[string]string{
		"credentials": fmt.Sprintf("%v", includeCred),
	})
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=\"onenat-config-%s.json\"", time.Now().Format("20060102-150405")))
	w.Write(body)
}

// ---------- 导入 ----------

type ImportSummary struct {
	AppsCreated     int      `json:"apps_created"`
	AppsUpdated     int      `json:"apps_updated"`
	AppsSkipped     int      `json:"apps_skipped"`
	SkillsAdded     int      `json:"skills_added"`
	TunnelsCreated  int      `json:"tunnels_created"`
	TunnelsUpdated  int      `json:"tunnels_updated"`
	TunnelsSkipped  int      `json:"tunnels_skipped"`
	MappingsCreated int      `json:"mappings_created"`
	MappingsFailed  []string `json:"mappings_failed,omitempty"`
}

// ImportConfig merges an export snapshot into the store. Matching is by
// name (apps and tunnels independently); mode "skip" leaves existing
// entities untouched, "update" refreshes their metadata/credentials.
// New tunnel keys are always regenerated. Nothing is ever deleted.
func (d *Dashboard) ImportConfig(in *ConfigExport, mode, ownerID, updatedBy string) *ImportSummary {
	sum := &ImportSummary{MappingsFailed: []string{}}

	// ---- 应用 ----
	// name -> id 映射: 既覆盖导入中新建的, 也供隧道映射解析绑定
	appIDs := map[string]string{}
	for _, a := range d.store.Apps(ownerID, true) {
		appIDs[a.Name] = a.ID
	}
	ensureApp := func(ea ConfigExportApp) (id string, created bool, err error) {
		if id, ok := appIDs[ea.Name]; ok {
			if mode == "update" {
				in := AppInput{
					Name: ea.Name, Type: ea.Type, Description: ea.Description,
					OwnerID: ownerID, InternalURL: ea.InternalURL, Tags: ea.Tags,
				}
				if err := d.store.UpdateAppMeta(id, in); err != nil {
					return id, false, err
				}
				if ea.Auth != nil {
					ai := AppAuthInput{
						AuthType: ea.Auth.AuthType, Username: ea.Auth.Username,
						Password: ea.Auth.Password, ApiKey: ea.Auth.ApiKey,
						ExtraHeaders: ea.Auth.ExtraHeaders,
					}
					if err := d.store.UpdateAppCredential(id, ai); err != nil {
						return id, false, err
					}
				}
				sum.AppsUpdated++
			} else {
				sum.AppsSkipped++
			}
			return id, false, nil
		}
		in := AppInput{
			Name: ea.Name, Type: ea.Type, Description: ea.Description,
			OwnerID: ownerID, InternalURL: ea.InternalURL, Tags: ea.Tags,
		}
		if ea.Auth != nil {
			in.Auth = AppAuthInput{
				AuthType: ea.Auth.AuthType, Username: ea.Auth.Username,
				Password: ea.Auth.Password, ApiKey: ea.Auth.ApiKey,
				ExtraHeaders: ea.Auth.ExtraHeaders,
			}
		} else {
			in.Auth = AppAuthInput{AuthType: "none"}
		}
		a, err := d.store.CreateApp(in)
		if err != nil {
			return "", false, err
		}
		appIDs[a.Name] = a.ID
		sum.AppsCreated++
		return a.ID, true, nil
	}

	for _, ea := range in.Apps {
		id, _, err := ensureApp(ea)
		if err != nil {
			sum.MappingsFailed = append(sum.MappingsFailed, fmt.Sprintf("应用 %q: %v", ea.Name, err))
			continue
		}
		// 技能文件: 只补缺失的同名技能, 不覆盖已有版本
		existing := map[string]bool{}
		for _, sk := range d.store.SkillFiles(id) {
			existing[sk.Name] = true
		}
		for _, es := range ea.Skills {
			if existing[es.Name] {
				continue
			}
			name, ext, err := SanitizeSkillName(es.Name)
			if err != nil {
				sum.MappingsFailed = append(sum.MappingsFailed, fmt.Sprintf("应用 %q 技能 %q: %v", ea.Name, es.Name, err))
				continue
			}
			if _, err := d.store.AddSkillMeta(id, name, ext, []byte(es.Content), updatedBy); err != nil {
				sum.MappingsFailed = append(sum.MappingsFailed, fmt.Sprintf("应用 %q 技能 %q: %v", ea.Name, es.Name, err))
				continue
			}
			sum.SkillsAdded++
		}
	}

	// ---- 隧道 + 映射 ----
	for _, et := range in.Tunnels {
		var t *Tunnel
		existing := false
		for _, cand := range d.store.Tunnels(ownerID, true) {
			if cand.Name == et.Name {
				t, existing = cand, true
				break
			}
		}
		if existing {
			if mode == "update" {
				if err := d.store.UpdateTunnelMeta(t.ID, et.Name, et.Note, et.Locked, et.AllowRemoteTargets, t.OwnerID); err != nil {
					sum.MappingsFailed = append(sum.MappingsFailed, fmt.Sprintf("隧道 %q: %v", et.Name, err))
					continue
				}
				sum.TunnelsUpdated++
			} else {
				sum.TunnelsSkipped++
			}
		} else {
			t = d.store.CreateTunnel(NewTunnelInput{Name: et.Name, Note: et.Note, OwnerID: ownerID})
			if t == nil {
				sum.MappingsFailed = append(sum.MappingsFailed, fmt.Sprintf("隧道 %q: 创建失败", et.Name))
				continue
			}
			if et.Locked || et.AllowRemoteTargets {
				_ = d.store.UpdateTunnelMeta(t.ID, et.Name, et.Note, et.Locked, et.AllowRemoteTargets, ownerID)
			}
			sum.TunnelsCreated++
		}
		// 已有映射去重键: proto+local_port+app 名
		have := map[string]bool{}
		for _, m := range t.Mappings {
			key := fmt.Sprintf("%s:%d:%s", m.Proto, m.LocalPort, appNameOf(d, m.AppID))
			have[key] = true
		}
		for _, em := range et.Mappings {
			appID := appIDs[em.AppName]
			if appID == "" {
				sum.MappingsFailed = append(sum.MappingsFailed,
					fmt.Sprintf("隧道 %q 映射 %s/%d: 应用 %q 不存在且导入未创建", et.Name, em.Proto, em.LocalPort, em.AppName))
				continue
			}
			dedup := fmt.Sprintf("%s:%d:%s", em.Proto, em.LocalPort, em.AppName)
			if existing && have[dedup] {
				continue // 幂等: 已有等价映射不重复添加
			}
			in := MappingInput{
				Proto: em.Proto, LocalIP: em.LocalIP, LocalPort: em.LocalPort,
				RemotePort: em.RemotePort, Subdomain: em.Subdomain, Note: em.Note,
				AppID: appID,
			}
			if em.Auth != nil {
				ai := AppAuthInput{
					AuthType: em.Auth.AuthType, Username: em.Auth.Username,
					Password: em.Auth.Password, ApiKey: em.Auth.ApiKey,
					ExtraHeaders: em.Auth.ExtraHeaders,
				}
				in.Auth = &ai
			}
			if _, err := d.store.AddMapping(t.ID, in); err != nil {
				sum.MappingsFailed = append(sum.MappingsFailed,
					fmt.Sprintf("隧道 %q 映射 %s/%d: %v", et.Name, em.Proto, em.LocalPort, err))
				continue
			}
			sum.MappingsCreated++
		}
		d.TouchConfig(t.ID)
	}
	return sum
}

func appNameOf(d *Dashboard, appID string) string {
	if a := d.store.AppByID(appID); a != nil {
		return a.Name
	}
	return ""
}

func (d *Dashboard) apiImportConfig(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	defer r.Body.Close()
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "读取请求体失败")
		return
	}
	// 兼容两种请求形态: {"config":{...},"mode":"..."} 包装, 或导出文件本体
	var body struct {
		Config *json.RawMessage `json:"config"`
		Mode   string           `json:"mode"`
	}
	_ = json.Unmarshal(rawBody, &body)
	raw := rawBody
	if body.Config != nil {
		raw = *body.Config
	}
	var in ConfigExport
	if err := json.Unmarshal(raw, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "配置文件格式无效: "+err.Error())
		return
	}
	if in.Format != configExportFormat {
		writeErr(w, http.StatusBadRequest, "不是 oneNat 配置导出文件 (format != "+configExportFormat+")")
		return
	}
	mode := strings.TrimSpace(body.Mode)
	if mode == "" {
		mode = "skip"
	}
	if mode != "skip" && mode != "update" {
		writeErr(w, http.StatusBadRequest, "mode 仅支持 skip | update")
		return
	}
	sum := d.ImportConfig(&in, mode, u.ID, u.Username)
	d.AuditUser(r, u, "config.import", "", "ok", map[string]string{
		"mode":    mode,
		"apps":    fmt.Sprintf("created=%d updated=%d skipped=%d", sum.AppsCreated, sum.AppsUpdated, sum.AppsSkipped),
		"tunnels": fmt.Sprintf("created=%d updated=%d skipped=%d mappings=%d", sum.TunnelsCreated, sum.TunnelsUpdated, sum.TunnelsSkipped, sum.MappingsCreated),
	})
	writeJSON(w, http.StatusOK, sum)
}
