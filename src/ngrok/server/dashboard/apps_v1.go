package dashboard

// apps_v1.go — read-only AI-agent endpoints (API KEY auth) for applications.
//
// Security:
//   - app visibility follows the key owner, narrowed by AppScopes when set
//   - resources / apps views NEVER contain credentials (not even masked)
//   - credentials endpoint requires CanReadCred on the key; rate-limited;
//     every attempt (ok/denied) is audited

import (
	"fmt"
	"net/http"
	"strings"
)

// appVisibleToKey reports whether an app is inside the key's scope.
func appVisibleToKey(k *ApiKey, a *App) bool {
	if len(k.AppScopes) == 0 {
		return true
	}
	for _, id := range k.AppScopes {
		if id == a.ID {
			return true
		}
	}
	return false
}

// ---------- resource views ----------

// ResourceApp is the app summary embedded into /api/v1/resources mappings.
// Deliberately excludes every credential field.
type ResourceApp struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	Description string          `json:"description"`
	AuthType    string          `json:"auth_type"`
	Username    string          `json:"username,omitempty"`
	InternalURL string          `json:"internal_url,omitempty"`
	Skills      []ResourceSkill `json:"skills"`
}

// ResourceSkill points the AI at the skill document download URL.
type ResourceSkill struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
}

// skillContentURL builds the direct-download URL for a skill file, baking the
// caller's API key into the query string so the AI can fetch it with a plain
// curl (no headers) as promised by the onenat skill document.
func skillContentURL(base, appID, skillName, key string) string {
	return fmt.Sprintf("%s/api/v1/apps/%s/skills/%s/content?key=%s", base, appID, skillName, key)
}

func (d *Dashboard) resourceAppFor(base, appID, key string) *ResourceApp {
	a := d.store.AppByID(appID)
	if a == nil {
		return nil
	}
	ra := &ResourceApp{
		ID: a.ID, Name: a.Name, Type: a.Type, Description: a.Description,
		AuthType: a.Auth.AuthType, Username: a.Auth.Username, InternalURL: a.InternalURL,
		Skills: []ResourceSkill{},
	}
	for _, sk := range d.store.SkillFiles(a.ID) {
		ra.Skills = append(ra.Skills, ResourceSkill{
			Name: sk.Name,
			Size: sk.Size,
			URL:  skillContentURL(base, a.ID, sk.Name, key),
		})
	}
	return ra
}

// ---------- handlers ----------

// apiV1Resources — extended: mappings embed the bound app (§5.2).
// (The original struct lives in api.go; the app embedding is appended here.)
func (d *Dashboard) apiV1Apps(w http.ResponseWriter, r *http.Request) {
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
	type appEntry struct {
		ID          string          `json:"id"`
		Name        string          `json:"name"`
		Type        string          `json:"type"`
		Description string          `json:"description"`
		AuthType    string          `json:"auth_type"`
		Username    string          `json:"username,omitempty"`
		InternalURL string          `json:"internal_url,omitempty"`
		Skills      []ResourceSkill `json:"skills"`
	}
	out := []appEntry{}
	for _, a := range d.store.Apps(u.ID, false) {
		if !appVisibleToKey(k, a) {
			continue
		}
		e := appEntry{
			ID: a.ID, Name: a.Name, Type: a.Type, Description: a.Description,
			AuthType: a.Auth.AuthType, Username: a.Auth.Username, InternalURL: a.InternalURL,
			Skills: []ResourceSkill{},
		}
		for _, sk := range d.store.SkillFiles(a.ID) {
			e.Skills = append(e.Skills, ResourceSkill{
				Name: sk.Name, Size: sk.Size,
				URL: skillContentURL(base, a.ID, sk.Name, k.Key),
			})
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"apps": out, "base_url": base})
}

// routeAppV1 dispatches /api/v1/apps/:id[/skills/:name/content | /credentials].
func (d *Dashboard) routeAppV1(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodMismatch(w, r, http.MethodGet)
		return
	}
	p := r.URL.Path
	switch {
	case strings.HasSuffix(p, "/credentials"):
		d.apiV1AppCredentials(w, r)
	case strings.Contains(p, "/skills/"):
		d.apiV1AppSkillContent(w, r)
	default:
		http.NotFound(w, r)
	}
}

// appForApiKey resolves the app for an AI key: must exist, belong to the key
// owner, and be inside the key's scope. Uniform 404 (no existence leak).
func (d *Dashboard) appForApiKey(w http.ResponseWriter, r *http.Request) (*ApiKey, *App) {
	k, u, ok := d.userFromApiKey(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "无效的 API KEY")
		return nil, nil
	}
	a := d.store.AppByID(pathSeg(r, 3))
	if a == nil || a.OwnerID != u.ID || !appVisibleToKey(k, a) {
		writeErr(w, http.StatusNotFound, "应用不存在")
		return nil, nil
	}
	return k, a
}

// apiV1AppSkillContent serves GET /api/v1/apps/:id/skills/:name/content
// (?download=1 switches to an attachment disposition).
func (d *Dashboard) apiV1AppSkillContent(w http.ResponseWriter, r *http.Request) {
	k, a := d.appForApiKey(w, r)
	if a == nil {
		return
	}
	// path: /api/v1/apps/:id/skills/:name/content → segs(0-based): 5 = :name
	name := pathSeg(r, 5)
	if name == "" {
		writeErr(w, http.StatusNotFound, "技能文件不存在")
		return
	}
	sk := d.store.SkillByAppName(a.ID, name)
	if sk == nil {
		writeErr(w, http.StatusNotFound, "技能文件不存在")
		return
	}
	content, err := ReadSkillFile(d.skillsDirResolved(), a.ID, sk.ID, sk.Ext)
	if err != nil {
		d.AuditKey(r, k, "skill.read", sk.ID, err.Error(), nil)
		writeErr(w, http.StatusNotFound, "技能文件不存在")
		return
	}
	d.AuditKey(r, k, "skill.read", sk.ID, "ok", map[string]string{"app": a.ID, "name": sk.Name})
	disposition := "inline"
	if r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", disposition+"; filename=\""+sk.Name+"\"")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(content)
}

// apiV1AppCredentials serves GET /api/v1/apps/:id/credentials — the ONLY
// AI-facing way to read upstream credentials. Default keys get 403.
func (d *Dashboard) apiV1AppCredentials(w http.ResponseWriter, r *http.Request) {
	k, a := d.appForApiKey(w, r)
	if a == nil {
		return
	}
	if !k.CanReadCred {
		d.AuditKey(r, k, "cred.read", a.ID, "denied", nil)
		writeErr(w, http.StatusForbidden, "该 API KEY 无凭证读取权限; 请用户在后台为 KEY 开启「允许读取凭证」")
		return
	}
	if !d.credLimiter.allow("cred:"+k.ID, revealMaxPerMinute) {
		d.AuditKey(r, k, "cred.read", a.ID, "rate-limited", nil)
		writeErr(w, http.StatusTooManyRequests, "请求过于频繁, 请稍后再试")
		return
	}
	username, password, apiKey, extra, err := d.store.RevealAppCredential(a.ID)
	if err != nil {
		d.AuditKey(r, k, "cred.read", a.ID, err.Error(), nil)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.AuditKey(r, k, "cred.read", a.ID, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"app_id":        a.ID,
		"auth_type":     a.Auth.AuthType,
		"username":      username,
		"password":      password,
		"api_key":       apiKey,
		"extra_headers": extra,
	})
}

// apiV1MappingCredentials serves GET /api/v1/mappings/:id/credentials —
// resolves the EFFECTIVE credential of a mapping's upstream instance:
// mapping-level override wins, otherwise falls back to the bound app's
// default (resolved_from reports which). Same gate as the app-level
// endpoint: CanReadCred + shared rate limit + audit on every attempt.
// 一个应用可被多条映射指向不同实例 (凭证各异), AI 按映射取凭证。
func (d *Dashboard) apiV1MappingCredentials(w http.ResponseWriter, r *http.Request) {
	k, u, ok := d.userFromApiKey(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "无效的 API KEY")
		return
	}
	t, m := d.store.MappingByID(pathSeg(r, 3)) // /api/v1/mappings/:id → segs[3]
	if t == nil || t.OwnerID != u.ID {
		// 不泄露存在性: 非本账号隧道的映射一律 404
		http.NotFound(w, r)
		return
	}
	if !k.CanReadCred {
		d.AuditKey(r, k, "cred.read", m.ID, "denied", map[string]string{"scope": "mapping"})
		writeErr(w, http.StatusForbidden, "该 API KEY 无凭证读取权限; 请用户在后台为 KEY 开启「允许读取凭证」")
		return
	}
	if !d.credLimiter.allow("cred:"+k.ID, revealMaxPerMinute) {
		d.AuditKey(r, k, "cred.read", m.ID, "rate-limited", map[string]string{"scope": "mapping"})
		writeErr(w, http.StatusTooManyRequests, "请求过于频繁, 请稍后再试")
		return
	}
	authType, username, password, apiKey, source, extra, err := d.store.RevealMappingCredential(m.ID)
	if err != nil {
		d.AuditKey(r, k, "cred.read", m.ID, err.Error(), map[string]string{"scope": "mapping"})
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.AuditKey(r, k, "cred.read", m.ID, "ok", map[string]string{"scope": "mapping", "resolved_from": source})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"mapping_id":    m.ID,
		"app_id":        m.AppID,
		"resolved_from": source,
		"auth_type":     authType,
		"username":      username,
		"password":      password,
		"api_key":       apiKey,
		"extra_headers": extra,
	})
}
