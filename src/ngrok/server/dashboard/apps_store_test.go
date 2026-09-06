package dashboard

// apps_store_test.go — app/skill store layer + binding validation tests.

import (
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
