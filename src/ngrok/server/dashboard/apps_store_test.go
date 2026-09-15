package dashboard

// apps_store_test.go — app/skill store layer + binding validation tests.

import (
	"path/filepath"
	"strings"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	return randBytes(32)
}

func seedAppUser(t *testing.T, s *Store) *User {
	t.Helper()
	s.BootstrapAdmin("adminpass1")
	u, err := s.CreateUser("alice", "alicepass", "user")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestAppCRUDAndCredentialSealing(t *testing.T) {
	s := tempStore(t)
	s.SetCredentialKey(testKey(t))
	u := seedAppUser(t, s)

	a, err := s.CreateApp(AppInput{
		Name: "KB API", Type: "http-api", Description: "仓储接口", OwnerID: u.ID,
		InternalURL: "http://192.168.30.164/kb",
		Auth: AppAuthInput{AuthType: "basic", Username: "kbadmin", Password: "kbpass-88", ApiKey: "onk-upstream"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.ID, "app-") {
		t.Fatalf("app id prefix: %q", a.ID)
	}
	// 密文落盘, 明文绝不进存储
	if strings.Contains(a.Auth.PasswordEnc, "kbpass-88") {
		t.Fatal("password must be sealed")
	}
	if a.Auth.PasswordEnc == "" || a.Auth.ApiKeyEnc == "" {
		t.Fatal("credentials must be sealed non-empty")
	}
	// 打开验证
	user, pass, key, _, err := s.RevealAppCredential(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if user != "kbadmin" || pass != "kbpass-88" || key != "onk-upstream" {
		t.Fatalf("reveal mismatch: %q/%q/%q", user, pass, key)
	}
	// 更新凭证
	if err := s.UpdateAppCredential(a.ID, AppAuthInput{AuthType: "bearer", ApiKey: "new-key"}); err != nil {
		t.Fatal(err)
	}
	_, pass2, key2, _, err := s.RevealAppCredential(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pass2 != "" || key2 != "new-key" {
		t.Fatalf("credential update mismatch: %q/%q", pass2, key2)
	}
	// 更新元数据
	if err := s.UpdateAppMeta(a.ID, AppInput{Name: "KB API v2", Type: "http-api", OwnerID: u.ID}); err != nil {
		t.Fatal(err)
	}
	if s.AppByID(a.ID).Name != "KB API v2" {
		t.Fatal("meta update failed")
	}
	// 删除
	if _, err := s.DeleteApp(a.ID, false); err != nil {
		t.Fatal(err)
	}
	if s.AppByID(a.ID) != nil {
		t.Fatal("app must be gone")
	}
}

func TestAppValidation(t *testing.T) {
	s := tempStore(t)
	s.SetCredentialKey(testKey(t))
	u := seedAppUser(t, s)

	cases := []AppInput{
		{Name: "", Type: "ssh", OwnerID: u.ID, Auth: AppAuthInput{AuthType: "none"}},
		{Name: "x", Type: "bad-type", OwnerID: u.ID, Auth: AppAuthInput{AuthType: "none"}},
		{Name: "x", Type: "ssh", OwnerID: u.ID, Auth: AppAuthInput{AuthType: "bad-auth"}},
		{Name: "x", Type: "ssh", OwnerID: u.ID, Auth: AppAuthInput{AuthType: "basic"}}, // basic 无用户名
	}
	for i, in := range cases {
		if _, err := s.CreateApp(in); err == nil {
			t.Fatalf("case %d must fail validation", i)
		}
	}
}

func TestAppDeleteForceUnbindsMappings(t *testing.T) {
	s := tempStore(t)
	s.SetCredentialKey(testKey(t))
	u := seedAppUser(t, s)
	a, _ := s.CreateApp(AppInput{Name: "SSH-164", Type: "ssh", OwnerID: u.ID, Auth: AppAuthInput{AuthType: "none"}})
	tun := s.CreateTunnel(NewTunnelInput{Name: "t1", OwnerID: u.ID})
	m, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalPort: 22, RemotePort: 52001, AppID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	// 未 force 时拒绝删除
	if n, err := s.DeleteApp(a.ID, false); err == nil || n != 1 {
		t.Fatalf("delete without force must refuse (n=%d err=%v)", n, err)
	}
	// force 后解绑且映射保留
	if _, err := s.DeleteApp(a.ID, true); err != nil {
		t.Fatal(err)
	}
	t2 := s.TunnelByID(tun.ID)
	if t2.Mappings[0].ID != m.ID || t2.Mappings[0].AppID != "" {
		t.Fatal("mapping must survive with empty app_id")
	}
}

func TestMappingAppBindingValidation(t *testing.T) {
	s := tempStore(t)
	s.SetCredentialKey(testKey(t))
	admin := s.UserByName("admin")
	alice := seedAppUser(t, s)
	_ = admin
	// alice 的应用
	a, _ := s.CreateApp(AppInput{Name: "my-app", Type: "ssh", OwnerID: alice.ID, Auth: AppAuthInput{AuthType: "none"}})
	// admin 的应用
	ta, _ := s.CreateApp(AppInput{Name: "admin-app", Type: "ssh", OwnerID: s.UserByName("admin").ID, Auth: AppAuthInput{AuthType: "none"}})
	tun := s.CreateTunnel(NewTunnelInput{Name: "t", OwnerID: alice.ID})

	// 空 AppID 在 store 层放行 (兼容存量); API 层强制必选 (见 api_test)
	m0, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalPort: 22, RemotePort: 52000})
	if err != nil {
		t.Fatalf("legacy empty app_id must pass at store level: %v", err)
	}
	_ = m0
	// 应用不存在 → 拒绝
	if _, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalPort: 22, RemotePort: 52001, AppID: "app-nonexist"}); err == nil {
		t.Fatal("mapping with unknown app must be rejected")
	}
	// 跨用户应用 → 拒绝
	if _, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalPort: 22, RemotePort: 52001, AppID: ta.ID}); err == nil {
		t.Fatal("cross-owner app binding must be rejected")
	}
	// 正常绑定
	m, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalPort: 22, RemotePort: 52001, AppID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if m.AppID != a.ID {
		t.Fatal("binding not stored")
	}
	// 计数
	if got := s.AppMappingCount(a.ID); got != 1 {
		t.Fatalf("mapping count = %d, want 1", got)
	}
}

func TestSkillMetadataLifecycle(t *testing.T) {
	s := tempStore(t)
	s.SetCredentialKey(testKey(t))
	u := seedAppUser(t, s)
	a, _ := s.CreateApp(AppInput{Name: "app", Type: "http-api", OwnerID: u.ID, Auth: AppAuthInput{AuthType: "none"}})

	content := []byte("# usage doc")
	sk, err := s.AddSkillMeta(a.ID, "usage.md", ".md", content, u.Username)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Version != 1 || sk.SHA256 != sha256Hex(content) {
		t.Fatal("skill meta mismatch")
	}
	// 同名拒绝
	if _, err := s.AddSkillMeta(a.ID, "usage.md", ".md", content, u.Username); err == nil {
		t.Fatal("duplicate name must fail")
	}
	// 更新 → 版本 +1
	sk2, err := s.ReplaceSkillMeta(sk.ID, []byte("# v2"), u.Username)
	if err != nil {
		t.Fatal(err)
	}
	if sk2.Version != 2 {
		t.Fatalf("version = %d, want 2", sk2.Version)
	}
	// 按名字解析 (AI 接口路径)
	if s.SkillByAppName(a.ID, "usage.md") == nil {
		t.Fatal("SkillByAppName must resolve")
	}
	// 删除
	appID, disk, err := s.DeleteSkillMeta(sk.ID)
	if err != nil || appID != a.ID || disk != sk.ID+".md" {
		t.Fatalf("delete meta: %v/%q/%q", err, appID, disk)
	}
}

// TestAppCredentialPropagation — 应用凭证更新的传导规则:
// 与旧值/新值相同的映射级覆盖 (历史快照) 自动回归继承; 实例差异凭证保留。
func TestAppCredentialPropagation(t *testing.T) {
	s := tempStore(t)
	s.SetCredentialKey(testKey(t))
	u := seedAppUser(t, s)

	a, err := s.CreateApp(AppInput{
		Name: "DSH", Type: "http-api", OwnerID: u.ID,
		InternalURL: "http://127.0.0.1:3080",
		Auth:        AppAuthInput{AuthType: "bearer", ApiKey: "key-A"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tun := s.CreateTunnel(NewTunnelInput{Name: "brain", OwnerID: u.ID})
	mk := func(key string) *AppAuthInput {
		if key == "" {
			return nil
		}
		return &AppAuthInput{AuthType: "bearer", ApiKey: key}
	}
	mSnap, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalIP: "127.0.0.1", LocalPort: 3081, RemotePort: 0, AppID: a.ID, Auth: mk("key-A")}) // 快照拷贝
	if err != nil {
		t.Fatal(err)
	}
	mReal, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalIP: "127.0.0.1", LocalPort: 3082, RemotePort: 0, AppID: a.ID, Auth: mk("key-B")}) // 实例差异
	if err != nil {
		t.Fatal(err)
	}
	mInherit, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalIP: "127.0.0.1", LocalPort: 3083, RemotePort: 0, AppID: a.ID}) // 无覆盖
	if err != nil {
		t.Fatal(err)
	}
	reveal := func(id string) (string, string) {
		_, _, _, key, source, _, err := s.RevealMappingCredential(id)
		if err != nil {
			t.Fatal(err)
		}
		return key, source
	}
	if k, src := reveal(mSnap.ID); k != "key-A" || src != "mapping" {
		t.Fatalf("pre-state snapshot: %q/%q", k, src)
	}

	// 应用凭证轮换 A → C: 快照覆盖应回归继承, 实例差异凭证保留
	if err := s.UpdateAppCredential(a.ID, AppAuthInput{AuthType: "bearer", ApiKey: "key-C"}); err != nil {
		t.Fatal(err)
	}
	propagated, kept, err := s.PropagateAppCredential(a.ID,
		AppAuthInput{AuthType: "bearer", ApiKey: "key-A"},
		AppAuthInput{AuthType: "bearer", ApiKey: "key-C"})
	if err != nil {
		t.Fatal(err)
	}
	if propagated != 1 || kept != 1 {
		t.Fatalf("propagate counts: got %d/%d, want 1/1", propagated, kept)
	}
	if k, src := reveal(mSnap.ID); k != "key-C" || src != "app" {
		t.Fatalf("snapshot override should inherit app: %q/%q", k, src)
	}
	if k, src := reveal(mReal.ID); k != "key-B" || src != "mapping" {
		t.Fatalf("genuine override must survive: %q/%q", k, src)
	}
	if k, src := reveal(mInherit.ID); k != "key-C" || src != "app" {
		t.Fatalf("inherit mapping: %q/%q", k, src)
	}

	// 与新值相同的覆盖同样是冗余快照: mReal 改成 key-C 后再传导应清为继承
	if err := s.UpdateMapping(tun.ID, mReal.ID, MappingInput{Proto: "tcp", LocalIP: "127.0.0.1", LocalPort: 3082, RemotePort: 0, AppID: a.ID, Auth: mk("key-C")}); err != nil {
		t.Fatal(err)
	}
	propagated, kept, err = s.PropagateAppCredential(a.ID,
		AppAuthInput{AuthType: "bearer", ApiKey: "key-C"},
		AppAuthInput{AuthType: "bearer", ApiKey: "key-C"})
	if err != nil {
		t.Fatal(err)
	}
	if propagated != 1 || kept != 0 {
		t.Fatalf("second propagate counts: got %d/%d, want 1/0", propagated, kept)
	}
	if _, src := reveal(mReal.ID); src != "app" {
		t.Fatalf("override equal to app value should clear: %q", src)
	}

	// 传导结果持久化: 重开存储后快照映射仍为继承态
	s2, err := OpenStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	s2.SetCredentialKey(s.credKey)
	_, _, _, key, source, _, err := s2.RevealMappingCredential(mSnap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if key != "key-C" || source != "app" {
		t.Fatalf("persisted inherit state: %q/%q", key, source)
	}
}

// TestMappingAuthPersistence — 回归测试 (2026-09-11 事故): 映射级独立凭证
// 曾因 Mapping.MarshalJSON 同时用于 API 视图与落盘而被静默丢弃, 重启即丢。
// 修复后: 落盘走 mappingPersist 投影, 密文 auth 必须跨"重启"存活。
func TestMappingAuthPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dash.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	key := testKey(t)
	s.SetCredentialKey(key)
	u := seedAppUser(t, s)

	a, err := s.CreateApp(AppInput{
		Name: "DSH", Type: "http-api", OwnerID: u.ID,
		InternalURL: "http://127.0.0.1:3080",
		Auth:        AppAuthInput{AuthType: "bearer", ApiKey: "app-key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tun := s.CreateTunnel(NewTunnelInput{Name: "brain", OwnerID: u.ID})
	ov := &AppAuthInput{AuthType: "bearer", ApiKey: "instance-key-9bbb"}
	m, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalIP: "127.0.0.1", LocalPort: 3080, RemotePort: 0, AppID: a.ID, Auth: ov})
	if err != nil {
		t.Fatal(err)
	}

		// 重开存储 = 模拟进程重启
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s2.SetCredentialKey(key)
	authType, username, password, apiKey, source, _, err := s2.RevealMappingCredential(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if source != "mapping" || authType != "bearer" || username != "" || password != "" || apiKey != "instance-key-9bbb" {
		t.Fatalf("override lost across restart: source=%q authType=%q user=%q pass=%q key=%q", source, authType, username, password, apiKey)
	}
}
