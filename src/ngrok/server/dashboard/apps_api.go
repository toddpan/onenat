package dashboard

// apps_api.go — session-authenticated HTTP handlers for applications and
// their skill files. Ownership isolation mirrors tunnels: users manage their
// own apps, admins manage everything. Every credential-touching call is
// audited; reveal is rate-limited.

import (
	"io"
	"net/http"
	"strings"
	"time"
)

// ---------- views ----------

// AppView is the safe (masked) app representation for the UI.
type AppView struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Type         string    `json:"type"`
	Description  string    `json:"description"`
	OwnerID      string    `json:"owner_id"`
	OwnerName    string    `json:"owner_name"`
	InternalURL  string    `json:"internal_url"`
	AuthType     string    `json:"auth_type"`
	Username     string    `json:"username"`
	PasswordMask string    `json:"password_mask"` // "••••1234"
	ApiKeyMask   string    `json:"api_key_mask"`
	HasPassword  bool      `json:"has_password"`
	HasApiKey    bool      `json:"has_api_key"`
	ExtraKeys    []string  `json:"extra_keys,omitempty"` // 自定义头名 (值永不外发)
	Tags         []string  `json:"tags,omitempty"`
	SkillCount   int       `json:"skill_count"`
	MappingCount int       `json:"mapping_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (d *Dashboard) appView(a *App) AppView {
	v := AppView{
		ID: a.ID, Name: a.Name, Type: a.Type, Description: a.Description,
		OwnerID: a.OwnerID, InternalURL: a.InternalURL,
		AuthType:    a.Auth.AuthType,
		Username:    a.Auth.Username,
		HasPassword: a.Auth.PasswordEnc != "",
		HasApiKey:   a.Auth.ApiKeyEnc != "",
		Tags:        a.Tags,
		CreatedAt:   a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
	if u := d.store.UserByID(a.OwnerID); u != nil {
		v.OwnerName = u.Username
	}
	// 只回自定义头的名字, 值永不离开服务端
	for k := range a.Auth.ExtraHeaders {
		v.ExtraKeys = append(v.ExtraKeys, k)
	}
	if v.HasPassword {
		// 打码基于密文尾部, 稳定且不泄露明文尾字符
		v.PasswordMask = MaskSecret(a.Auth.PasswordEnc)
	}
	if v.HasApiKey {
		v.ApiKeyMask = MaskSecret(a.Auth.ApiKeyEnc)
	}
	for _, sk := range d.store.SkillFiles(a.ID) {
		if sk != nil {
			v.SkillCount++
		}
	}
	v.MappingCount = d.store.AppMappingCount(a.ID)
	return v
}

// SkillView is the metadata view of a skill file.
type SkillView struct {
	ID        string    `json:"id"`
	AppID     string    `json:"app_id"`
	Name      string    `json:"name"`
	Ext       string    `json:"ext"`
	Size      int64     `json:"size"`
	Version   int       `json:"version"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func skillView(sk *SkillFile) SkillView {
	return SkillView{
		ID: sk.ID, AppID: sk.AppID, Name: sk.Name, Ext: sk.Ext,
		Size: sk.Size, Version: sk.Version, UpdatedBy: sk.UpdatedBy,
		CreatedAt: sk.CreatedAt, UpdatedAt: sk.UpdatedAt,
	}
}

// ---------- ownership helpers ----------

func canSeeApp(u *User, a *App) bool {
	return u != nil && a != nil && (u.Role == "admin" || a.OwnerID == u.ID)
}

// routeAppAPI dispatches /api/apps/:id[/...] (session auth already applied).
func (d *Dashboard) routeAppAPI(w http.ResponseWriter, r *http.Request) {
	id := pathSeg(r, 2)
	sub := pathSeg(r, 3) // "" | credential | reveal | skills
	switch sub {
	case "":
		switch r.Method {
		case http.MethodGet:
			d.requireUser(d.apiHandler(d.apiGetApp)).ServeHTTP(w, r)
		case http.MethodPatch:
			d.requireUser(d.apiHandler(d.apiPatchApp)).ServeHTTP(w, r)
		case http.MethodDelete:
			d.requireUser(d.apiHandler(d.apiDeleteApp)).ServeHTTP(w, r)
		default:
			methodMismatch(w, r, http.MethodGet, http.MethodPatch, http.MethodDelete)
		}
	case "credential":
		if methodMismatch(w, r, http.MethodPut) {
			return
		}
		d.requireUser(d.apiHandler(d.apiPutAppCredential)).ServeHTTP(w, r)
	case "reveal":
		if methodMismatch(w, r, http.MethodPost) {
			return
		}
		d.requireUser(d.apiHandler(d.apiRevealAppCredential)).ServeHTTP(w, r)
	case "skills":
		sid := pathSeg(r, 4)
		if sid == "" {
			switch r.Method {
			case http.MethodGet:
				d.requireUser(http.HandlerFunc(d.apiListAppSkills)).ServeHTTP(w, r)
			case http.MethodPost:
				d.requireUser(d.apiHandler(d.apiUploadAppSkill)).ServeHTTP(w, r)
			default:
				methodMismatch(w, r, http.MethodGet, http.MethodPost)
			}
			return
		}
		if pathSeg(r, 5) == "download" {
			if methodMismatch(w, r, http.MethodGet) {
				return
			}
			d.requireUser(http.HandlerFunc(d.apiDownloadAppSkill)).ServeHTTP(w, r)
			return
		}
		switch r.Method {
		case http.MethodPut:
			d.requireUser(d.apiHandler(d.apiUpdateAppSkill)).ServeHTTP(w, r)
		case http.MethodDelete:
			d.requireUser(d.apiHandler(d.apiDeleteAppSkill)).ServeHTTP(w, r)
		default:
			methodMismatch(w, r, http.MethodPut, http.MethodDelete)
		}
	default:
		_ = id // 未知的子路径
		http.NotFound(w, r)
	}
}

// appForUser resolves :id with the 404-preserving pattern (no existence leak).
func (d *Dashboard) appForUser(w http.ResponseWriter, r *http.Request) *App {
	u := d.UserFromRequest(r)
	a := d.store.AppByID(pathSeg(r, 2))
	if a == nil || !canSeeApp(u, a) {
		writeErr(w, http.StatusNotFound, "应用不存在")
		return nil
	}
	return a
}

// ---------- app CRUD handlers ----------

func (d *Dashboard) apiListApps(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	apps := d.store.Apps(u.ID, u.Role == "admin")
	out := make([]AppView, 0, len(apps))
	for _, a := range apps {
		out = append(out, d.appView(a))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"apps": out})
}

type appBody struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Description string            `json:"description"`
	InternalURL string            `json:"internal_url"`
	Tags        []string          `json:"tags"`
	AuthType    string            `json:"auth_type"`
	Username    string            `json:"username"`
	Password    string            `json:"password"`
	ApiKey      string            `json:"api_key"`
	ExtraHeaders map[string]string `json:"extra_headers"`
}

func (b appBody) toInput() AppInput {
	return AppInput{
		Name: b.Name, Type: b.Type, Description: b.Description,
		InternalURL: b.InternalURL, Tags: b.Tags,
		Auth: AppAuthInput{
			AuthType: b.AuthType, Username: b.Username,
			Password: b.Password, ApiKey: b.ApiKey, ExtraHeaders: b.ExtraHeaders,
		},
	}
}

func (d *Dashboard) apiCreateApp(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	var in appBody
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	input := in.toInput()
	input.OwnerID = u.ID // 应用归属创建者; admin 亦然 (所有权模型与隧道不同: 应用永远跟人)
	a, err := d.store.CreateApp(input)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// 按类型生成技能骨架, 让新应用立刻可用
	if err := d.scaffoldSkill(a, u.Username); err != nil {
		d.AuditUser(r, u, "app.scaffold_fail", a.ID, err.Error(), nil)
	}
	d.AuditUser(r, u, "app.create", a.ID, "ok", map[string]string{"name": a.Name, "type": a.Type})
	writeJSON(w, http.StatusOK, map[string]interface{}{"app": d.appView(a)})
}

func (d *Dashboard) apiGetApp(w http.ResponseWriter, r *http.Request) {
	a := d.appForUser(w, r)
	if a == nil {
		return
	}
	v := d.appView(a)
	// 详情附带技能列表
	skills := make([]SkillView, 0)
	for _, sk := range d.store.SkillFiles(a.ID) {
		skills = append(skills, skillView(sk))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"app": v, "skills": skills})
}

func (d *Dashboard) apiPatchApp(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	a := d.appForUser(w, r)
	if a == nil {
		return
	}
	var in appBody
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	input := in.toInput()
	// PATCH 语义: 凭证字段走专门的 /credential 入口, 这里不碰
	input.Auth = AppAuthInput{AuthType: a.Auth.AuthType, Username: a.Auth.Username}
	if err := d.store.UpdateAppMeta(a.ID, input); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d.AuditUser(r, u, "app.update", a.ID, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

func (d *Dashboard) apiDeleteApp(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	a := d.appForUser(w, r)
	if a == nil {
		return
	}
	force := r.URL.Query().Get("force") == "1"
	n, err := d.store.DeleteApp(a.ID, force)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	// 元数据已删, 清理磁盘内容
	_ = DeleteAppSkillDir(d.skillsDirResolved(), a.ID)
	d.AuditUser(r, u, "app.delete", a.ID, "ok", map[string]string{"name": a.Name, "unbound": itoa64(int64(n))})
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1", "unbound": itoa64(int64(n))})
}

// ---------- credential handlers ----------

func (d *Dashboard) apiPutAppCredential(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	a := d.appForUser(w, r)
	if a == nil {
		return
	}
	var in struct {
		AuthType     string            `json:"auth_type"`
		Username     string            `json:"username"`
		Password     string            `json:"password"`
		ApiKey       string            `json:"api_key"`
		ExtraHeaders map[string]string `json:"extra_headers"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	err := d.store.UpdateAppCredential(a.ID, AppAuthInput{
		AuthType: in.AuthType, Username: in.Username,
		Password: in.Password, ApiKey: in.ApiKey, ExtraHeaders: in.ExtraHeaders,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d.AuditUser(r, u, "cred.update", a.ID, "ok", map[string]string{"auth_type": in.AuthType})
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

// apiRevealAppCredential returns plaintext credentials after a confirm;
// audited + rate-limited (5/min/app).
func (d *Dashboard) apiRevealAppCredential(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	a := d.appForUser(w, r)
	if a == nil {
		return
	}
	if !d.revealLimiter.allow("reveal:"+a.ID, revealMaxPerMinute) {
		d.AuditUser(r, u, "cred.reveal", a.ID, "rate-limited", nil)
		writeErr(w, http.StatusTooManyRequests, "操作过于频繁, 请稍后再试")
		return
	}
	username, password, apiKey, extra, err := d.store.RevealAppCredential(a.ID)
	if err != nil {
		d.AuditUser(r, u, "cred.reveal", a.ID, err.Error(), nil)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.AuditUser(r, u, "cred.reveal", a.ID, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"username": username, "password": password, "api_key": apiKey, "extra_headers": extra,
	})
}

// ---------- skill handlers ----------

// writeNewSkill validates + persists content and inserts metadata.
func (d *Dashboard) writeNewSkill(appID, name, ext string, content []byte, updatedBy string) error {
	if err := ValidateSkillContent(content); err != nil {
		return err
	}
	// 先占元数据 (同名/数量校验), 再写磁盘; 元数据失败时磁盘无残留
	sk, err := d.store.AddSkillMeta(appID, name, ext, content, updatedBy)
	if err != nil {
		return err
	}
	if _, err := WriteSkillFile(d.skillsDirResolved(), appID, sk.ID, ext, content); err != nil {
		_, _, _ = d.store.DeleteSkillMeta(sk.ID) // 回滚元数据
		return err
	}
	return nil
}

func (d *Dashboard) apiListAppSkills(w http.ResponseWriter, r *http.Request) {
	a := d.appForUser(w, r)
	if a == nil {
		return
	}
	skills := make([]SkillView, 0)
	for _, sk := range d.store.SkillFiles(a.ID) {
		skills = append(skills, skillView(sk))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"skills": skills})
}

// apiUploadAppSkill accepts multipart (field "file", optional "name") or a
// JSON body {"name": "...", "content": "..."}.
func (d *Dashboard) apiUploadAppSkill(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	a := d.appForUser(w, r)
	if a == nil {
		return
	}
	var name string
	var content []byte
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, maxSkillSize+(1<<20)) // 512KB + multipart 开销
		f, hdr, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "缺少上传文件字段 file")
			return
		}
		defer f.Close()
		content, err = io.ReadAll(f)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "读取上传内容失败")
			return
		}
		name = strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			name = hdr.Filename
		}
	} else {
		var in struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式错误")
			return
		}
		name = strings.TrimSpace(in.Name)
		content = []byte(in.Content)
	}
	cname, ext, err := SanitizeSkillName(name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := d.writeNewSkill(a.ID, cname, ext, content, u.Username); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d.AuditUser(r, u, "skill.upload", a.ID, "ok", map[string]string{"name": cname, "size": itoa64(int64(len(content)))})
	// 返回更新后的技能列表
	skills := make([]SkillView, 0)
	for _, sk := range d.store.SkillFiles(a.ID) {
		skills = append(skills, skillView(sk))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"skills": skills})
}

// skillForUser resolves (app, skill id) with ownership on both.
func (d *Dashboard) skillForUser(w http.ResponseWriter, r *http.Request) (*App, *SkillFile) {
	a := d.appForUser(w, r)
	if a == nil {
		return nil, nil
	}
	sk := d.store.SkillByID(pathSeg(r, 4))
	if sk == nil || sk.AppID != a.ID {
		writeErr(w, http.StatusNotFound, "技能文件不存在")
		return nil, nil
	}
	return a, sk
}

func (d *Dashboard) apiDownloadAppSkill(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	a, sk := d.skillForUser(w, r)
	if a == nil {
		return
	}
	content, err := ReadSkillFile(d.skillsDirResolved(), a.ID, sk.ID, sk.Ext)
	if err != nil {
		d.AuditUser(r, u, "skill.download", sk.ID, err.Error(), nil)
		writeErr(w, http.StatusNotFound, "技能文件内容缺失")
		return
	}
	d.AuditUser(r, u, "skill.download", sk.ID, "ok", map[string]string{"name": sk.Name})
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+sk.Name+"\"")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(content)
}

func (d *Dashboard) apiUpdateAppSkill(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	a, sk := d.skillForUser(w, r)
	if a == nil {
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	content := []byte(in.Content)
	if err := ValidateSkillContent(content); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := WriteSkillFile(d.skillsDirResolved(), a.ID, sk.ID, sk.Ext, content); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	nsk, err := d.store.ReplaceSkillMeta(sk.ID, content, u.Username)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.AuditUser(r, u, "skill.update", sk.ID, "ok", map[string]string{"name": sk.Name, "version": itoa64(int64(nsk.Version))})
	writeJSON(w, http.StatusOK, map[string]interface{}{"skill": skillView(nsk)})
}

func (d *Dashboard) apiDeleteAppSkill(w http.ResponseWriter, r *http.Request) {
	u := d.UserFromRequest(r)
	_, sk := d.skillForUser(w, r)
	if sk == nil {
		return
	}
	appID, _, err := d.store.DeleteSkillMeta(sk.ID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if err := DeleteSkillFile(d.skillsDirResolved(), appID, sk.ID, sk.Ext); err != nil {
		d.AuditUser(r, u, "skill.delete", sk.ID, "disk:"+err.Error(), nil)
	} else {
		d.AuditUser(r, u, "skill.delete", sk.ID, "ok", map[string]string{"name": sk.Name})
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}
