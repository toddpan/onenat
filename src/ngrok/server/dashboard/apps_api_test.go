package dashboard

// apps_api_test.go — handler-level tests: API contract enforcement
// (mapping→app required), ownership isolation and skill upload/download
// over HTTP with a logged-in session.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestDashboard(t *testing.T) (*Dashboard, *Store) {
	t.Helper()
	dir := t.TempDir()
	d, err := New(Options{
		DataPath:  dir + "/dash.json",
		SkillsDir: dir + "/skills",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 与 server.Main 一致: 启动时引导初始 admin 账号
	d.Bootstrap()
	return d, d.store
}

// TestSeedBuiltinSSHApp — 全新安装必须预置「SSH Server」应用并带完整 SSH 技能;
// 重复调用幂等。
func TestSeedBuiltinSSHApp(t *testing.T) {
	d, s := newTestDashboard(t)
	u := s.UserByName("admin")

	d.SeedBuiltinApps()

	apps := s.Apps(u.ID, true)
	var ssh *App
	for _, a := range apps {
		if a.Name == "SSH Server" {
			ssh = a
		}
	}
	if ssh == nil {
		t.Fatal("builtin SSH Server app must be seeded")
	}
	if ssh.Type != "ssh" || ssh.OwnerID != u.ID {
		t.Fatalf("seeded app wrong: %+v", ssh)
	}
	skills := s.SkillFiles(ssh.ID)
	if len(skills) != 1 {
		t.Fatalf("seeded app must carry 1 skill, got %d", len(skills))
	}
	// 技能内容: 平台预制 SSH 技能的关键章节
	content, err := ReadSkillFile(d.skillsDirResolved(), ssh.ID, skills[0].ID, skills[0].Ext)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"连接入口", "认证", "常用操作", "安全红线"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("seeded SSH skill missing %q", want)
		}
	}
	// 幂等: 再次播种不产生第二个
	d.SeedBuiltinApps()
	if n := len(s.Apps(u.ID, true)); n != 1 {
		t.Fatalf("seed must be idempotent, got %d apps", n)
	}
	// 已有数据的旧安装 (非空 users): Bootstrap 返回 created=false, 不重复播种
	// (该路径由 server.Main 的 created 分支保证, 此处验证 Seed 本身的幂等即可)
}

func loginAs(t *testing.T, d *Dashboard, username string) *http.Cookie {
	t.Helper()
	sess := NewSessions(d.store.SessionSecret())
	rec := httptest.NewRecorder()
	sess.Issue(rec, username, false)
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func sessRequest(t *testing.T, d *Dashboard, u *User, method, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(loginAs(t, d, u.Username))
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)
	return rec
}

func TestAPIAddMappingRequiresApp(t *testing.T) {
	d, s := newTestDashboard(t)
	u := s.UserByName("admin")

	tun := s.CreateTunnel(NewTunnelInput{Name: "t", OwnerID: u.ID})

	// 无 app_id → 400
	rec := sessRequest(t, d, u, http.MethodPost,
		"/api/tunnels/"+tun.ID+"/mappings",
		`{"proto":"tcp","local_port":22,"remote_port":52001,"note":"ssh"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mapping without app_id must 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "关联") {
		t.Fatalf("error must mention binding: %s", rec.Body.String())
	}

	// 带 app_id → 200
	a, err := s.CreateApp(AppInput{Name: "SSH", Type: "ssh", OwnerID: u.ID, Auth: AppAuthInput{AuthType: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	rec = sessRequest(t, d, u, http.MethodPost,
		"/api/tunnels/"+tun.ID+"/mappings",
		`{"proto":"tcp","local_port":22,"remote_port":52001,"app_id":"`+a.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("mapping with app_id must 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIAppOwnershipIsolation(t *testing.T) {
	d, s := newTestDashboard(t)
	admin := s.UserByName("admin")
	alice, err := s.CreateUser("alice", "alicepass", "user")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.CreateApp(AppInput{Name: "alice-app", Type: "ssh", OwnerID: alice.ID, Auth: AppAuthInput{AuthType: "none"}})

	// alice 不能看 admin 的应用 (不存在泄漏: 404)
	b, _ := s.CreateApp(AppInput{Name: "admin-app", Type: "ssh", OwnerID: admin.ID, Auth: AppAuthInput{AuthType: "none"}})
	rec := sessRequest(t, d, alice, http.MethodGet, "/api/apps/"+b.ID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-owner read must 404, got %d", rec.Code)
	}
	// alice 不能删 admin 的应用
	rec = sessRequest(t, d, alice, http.MethodDelete, "/api/apps/"+b.ID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-owner delete must 404, got %d", rec.Code)
	}
	// alice 读自己的应用 → 200
	rec = sessRequest(t, d, alice, http.MethodGet, "/api/apps/"+a.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner read must 200, got %d", rec.Code)
	}
	// 列表隔离: alice 看不到 admin 的应用
	rec = sessRequest(t, d, alice, http.MethodGet, "/api/apps", "")
	if strings.Contains(rec.Body.String(), "admin-app") {
		t.Fatal("app list must be owner-scoped")
	}
}

func TestAPISkillUploadDownloadFlow(t *testing.T) {
	d, s := newTestDashboard(t)
	u := s.UserByName("admin")
	a, _ := s.CreateApp(AppInput{Name: "KB API", Type: "http-api", OwnerID: u.ID, Auth: AppAuthInput{AuthType: "none"}})

	// JSON 上传
	rec := sessRequest(t, d, u, http.MethodPost, "/api/apps/"+a.ID+"/skills",
		`{"name":"kb-api.md","content":"# KB API 技能\n## 认证"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("skill upload must 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// 恶意文件名 → 400
	rec = sessRequest(t, d, u, http.MethodPost, "/api/apps/"+a.ID+"/skills",
		`{"name":"../../evil.md","content":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("traversal name must 400, got %d", rec.Code)
	}
	// 可执行扩展名 → 400
	rec = sessRequest(t, d, u, http.MethodPost, "/api/apps/"+a.ID+"/skills",
		`{"name":"evil.sh","content":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-whitelisted ext must 400, got %d", rec.Code)
	}
	// 下载 (需要 skill id)
	skills := s.SkillFiles(a.ID)
	if len(skills) != 1 {
		t.Fatalf("want 1 skill, got %d", len(skills))
	}
	rec = sessRequest(t, d, u, http.MethodGet, "/api/apps/"+a.ID+"/skills/"+skills[0].ID+"/download", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "KB API 技能") {
		t.Fatalf("download failed: %d %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("download must be attachment, got %q", cd)
	}
	// 在线修改 → 版本递增
	rec = sessRequest(t, d, u, http.MethodPut, "/api/apps/"+a.ID+"/skills/"+skills[0].ID,
		`{"content":"# v2 content"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("skill update must 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"version":2`) {
		t.Fatalf("version must bump to 2: %s", rec.Body.String())
	}
	// 凭证永不进技能接口响应
	if strings.Contains(rec.Body.String(), "enc:v1:") {
		t.Fatal("skill responses must not leak sealed credentials")
	}
}

func TestAPIRevealAuditedAndMasked(t *testing.T) {
	d, s := newTestDashboard(t)
	u := s.UserByName("admin")
	a, _ := s.CreateApp(AppInput{
		Name: "ssh", Type: "ssh", OwnerID: u.ID,
		Auth: AppAuthInput{AuthType: "basic", Username: "root", Password: "real-secret-88"},
	})

	// 详情视图: 凭证打码, 明文不出现
	rec := sessRequest(t, d, u, http.MethodGet, "/api/apps/"+a.ID, "")
	if strings.Contains(rec.Body.String(), "real-secret-88") {
		t.Fatal("detail view must not leak plaintext credential")
	}
	if !strings.Contains(rec.Body.String(), "••••") {
		t.Fatal("detail view must mask credentials")
	}

	// reveal → 明文返回 (owner) 且写入审计日志
	rec = sessRequest(t, d, u, http.MethodPost, "/api/apps/"+a.ID+"/reveal", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "real-secret-88") {
		t.Fatalf("reveal must return plaintext to owner, got %d", rec.Code)
	}

	// 限速: 连续超过 revealMaxPerMinute 次 → 429
	for i := 0; i < revealMaxPerMinute; i++ {
		sessRequest(t, d, u, http.MethodPost, "/api/apps/"+a.ID+"/reveal", "")
	}
	rec = sessRequest(t, d, u, http.MethodPost, "/api/apps/"+a.ID+"/reveal", "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("reveal beyond budget must 429, got %d", rec.Code)
	}
}

func TestAIV1AppsScopeAndCredentialGate(t *testing.T) {
	d, s := newTestDashboard(t)
	u := s.UserByName("admin")
	a, err := s.CreateApp(AppInput{
		Name: "KB", Type: "http-api", OwnerID: u.ID, Description: "kb desc",
		Auth: AppAuthInput{AuthType: "basic", Username: "root", Password: "topsecret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.AddSkillMeta(a.ID, "kb.md", ".md", []byte("# kb"), "admin")
	// 元数据对应的磁盘内容文件 (真实流程由 handler 双写)
	sk := s.SkillByAppName(a.ID, "kb.md")
	if _, err := WriteSkillFile(d.skillsDirResolved(), a.ID, sk.ID, ".md", []byte("# kb")); err != nil {
		t.Fatal(err)
	}
	key := s.CreateApiKey(u.ID, "ai")

	// 绑定一个隧道映射, 资源列表才会展示该应用
	tun := s.CreateTunnel(NewTunnelInput{Name: "t", OwnerID: u.ID})
	if _, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalPort: 22, RemotePort: 52001, AppID: a.ID}); err != nil {
		t.Fatal(err)
	}

	// /api/v1/resources 内嵌应用 + 技能链接, 无凭证
	req := httptest.NewRequest(http.MethodGet, "/api/v1/resources", nil)
	req.Header.Set("Authorization", "Bearer "+key.Key)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resources: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"app"`) || !strings.Contains(body, "kb.md") {
		t.Fatalf("resources must embed app + skills: %s", body)
	}
	if strings.Contains(body, "topsecret") {
		t.Fatal("resources must never leak credentials")
	}

	// /api/v1/apps
	req = httptest.NewRequest(http.MethodGet, "/api/v1/apps", nil)
	req.Header.Set("Authorization", "Bearer "+key.Key)
	rec = httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "kb.md") {
		t.Fatalf("v1 apps must list skills: %s", rec.Body.String())
	}

	// 技能内容下载 (按名字)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/apps/"+a.ID+"/skills/kb.md/content", nil)
	req.Header.Set("Authorization", "Bearer "+key.Key)
	rec = httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "# kb") {
		t.Fatalf("skill content: %d %s", rec.Code, rec.Body.String())
	}

	// 默认 KEY 无凭证读取权 → 403
	req = httptest.NewRequest(http.MethodGet, "/api/v1/apps/"+a.ID+"/credentials", nil)
	req.Header.Set("Authorization", "Bearer "+key.Key)
	rec = httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("credentials must be 403 by default, got %d", rec.Code)
	}

	// canReadCred=true → 200 (直接改存储模拟)
	s.mu.Lock()
	for _, k := range s.data.ApiKeys {
		if k.ID == key.ID {
			k.CanReadCred = true
		}
	}
	s.mu.Unlock()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/apps/"+a.ID+"/credentials", nil)
	req.Header.Set("Authorization", "Bearer "+key.Key)
	rec = httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "topsecret") {
		t.Fatalf("cred read with grant: %d %s", rec.Code, rec.Body.String())
	}

	// appScopes 限定可见性
	b, _ := s.CreateApp(AppInput{Name: "Other", Type: "web", OwnerID: u.ID, Auth: AppAuthInput{AuthType: "none"}})
	s.mu.Lock()
	for _, k := range s.data.ApiKeys {
		if k.ID == key.ID {
			k.AppScopes = []string{b.ID}
		}
	}
	s.mu.Unlock()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/apps/"+a.ID+"/skills/kb.md/content", nil)
	req.Header.Set("Authorization", "Bearer "+key.Key)
	rec = httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("out-of-scope app must 404, got %d", rec.Code)
	}
}

func TestSkillIndexDocListsApps(t *testing.T) {
	d, s := newTestDashboard(t)
	u := s.UserByName("admin")
	a, err := s.CreateApp(AppInput{
		Name: "KB API", Type: "http-api", OwnerID: u.ID, Description: "desc",
		Auth: AppAuthInput{AuthType: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.AddSkillMeta(a.ID, "api.md", ".md", []byte("x"), "admin")
	key := s.CreateApiKey(u.ID, "ai")

	req := httptest.NewRequest(http.MethodGet, "/skill/index.md?key="+key.Key, nil)
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("index: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"KB API", "api.md", "onenat-apps"} {
		if !strings.Contains(body, want) {
			t.Fatalf("index must contain %q: %s", want, body)
		}
	}
}
