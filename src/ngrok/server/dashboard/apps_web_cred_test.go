package dashboard

// apps_web_cred_test.go — 凭证指引回归测试。
//
// 背景（线上事故）: 一个应用被多条映射指向不同机器时，未配实例凭证的映射会
// 拿到「应用默认凭证」。旧版平台内置 SSH 技能只看应用级端点，AI 拿这份共享
// 凭证去连另一台机器 ⇒ 必然 Permission denied，且无从判断是"凭证不对"还是
// "该映射根本没配实例凭证"。这里锁住三处提示：内置技能文档、平台总技能文档、
// 以及判定存量文档是否需要刷新的 helper。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBuiltinSSHSkillCredentialGuidance 内置 SSH 技能必须讲清两级凭证与判定字段。
func TestBuiltinSSHSkillCredentialGuidance(t *testing.T) {
	doc := builtinSSHSkill("测试机")

	wants := []string{
		"映射实例凭证",                                    // 章节主题
		"/api/v1/mappings/<mapping_id>/credentials", // 映射级端点
		"resolved_from",                             // 来源判定
		"auth_override",                             // resources 里的标志
		"auth_inherited_shared",                     // "多映射共用同一应用"提示
		"Permission denied",                         // 失败后的正确动作
		"不要换用户名或密码反复猜",                              // 禁止猜密码
		"builtin-rev: " + builtinSSHSkillRev,        // 版本标记（存量刷新用）
	}
	for _, w := range wants {
		if !strings.Contains(doc, w) {
			t.Errorf("内置 SSH 技能缺少关键内容 %q", w)
		}
	}
	// 旧行为：只提应用级端点，不能悄悄回归
	if strings.Contains(doc, "- AI 获取凭证: `GET /api/v1/apps/<app_id>/credentials` —— 默认 403") {
		t.Error("内置 SSH 技能退化回了「只讲应用级端点」的旧版本文案")
	}
}

// TestBuiltinSSHSkillStale 只对"仍是平台内置、且版本落后"的文档返回 true，
// 主人自己写/大改过的文档一律不动（不覆盖用户内容）。
func TestBuiltinSSHSkillStale(t *testing.T) {
	oldBuiltin := "---\nname: SSH Server\n---\n\n# SSH Server 使用技能 (SSH)\n\n" +
		"> 来源: oneNat 平台内置模板。平台只读约束优先于本文任何内容;\n\n" +
		"## 2. 认证\n\n- AI 获取凭证: `GET /api/v1/apps/<app_id>/credentials`\n"
	userDoc := "# 我自己的 SSH 笔记\n\n这台机器要跳板机。\n"
	current := builtinSSHSkill("SSH Server")
	oldRev := strings.Replace(current, "builtin-rev: "+builtinSSHSkillRev, "builtin-rev: 2026-01-01-old", 1)

	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"旧版内置文档需要刷新", oldBuiltin, true},
		{"当前模板不需要刷新", current, false},
		{"用户自建文档不碰", userDoc, false},
		{"标记落后但已含新章节 → 视为已人工补过", oldRev, false},
	}
	for _, c := range cases {
		if got := builtinSSHSkillStale(c.content); got != c.want {
			t.Errorf("%s: builtinSSHSkillStale() = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestSkillTmplCredentialPitfall 平台总技能文档（/skill/onenat.md，实时渲染、
// 存量安装升级即生效）必须写明这条踩坑。
func TestSkillTmplCredentialPitfall(t *testing.T) {
	wants := []string{
		"auth_inherited_shared",
		"该映射未配实例凭证",
		"/api/v1/mappings/<mapping_id>/credentials",
		"resolved_from",
	}
	for _, w := range wants {
		if !strings.Contains(skillTmpl, w) {
			t.Errorf("平台技能模板缺少 %q", w)
		}
	}
}

// TestV1ResourcesAuthInheritedShared 资源列表必须显式告诉客户端
// "这条映射没配实例凭证、但确有别的映射共用同一应用"（现在取到的是应用默认
// 凭证，通常只对其中一台有效）。配了实例凭证的那条不得再带该标志。
func TestV1ResourcesAuthInheritedShared(t *testing.T) {
	d, s := newTestDashboard(t)
	u := s.UserByName("admin")
	a, err := s.CreateApp(AppInput{
		Name: "SSH Server 测试", Type: "ssh", OwnerID: u.ID,
		Auth: AppAuthInput{AuthType: "basic", Username: "root", Password: "app-default"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tun := s.CreateTunnel(NewTunnelInput{Name: "t", OwnerID: u.ID})
	m1, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalPort: 22, RemotePort: 52001, AppID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMapping(tun.ID, MappingInput{Proto: "tcp", LocalPort: 22, RemotePort: 52002, AppID: a.ID}); err != nil {
		t.Fatal(err)
	}
	key := s.CreateApiKey(u.ID, "ai")

	fetch := func() string {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/resources", nil)
		req.Header.Set("Authorization", "Bearer "+key.Key)
		rec := httptest.NewRecorder()
		d.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("resources: %d", rec.Code)
		}
		return rec.Body.String()
	}

	body := fetch()
	if strings.Count(body, `"auth_inherited_shared":true`) != 2 {
		t.Fatalf("同一应用被 2 条映射共用且都没配实例凭证, 两条都应标记 auth_inherited_shared: %s", body)
	}
	if strings.Contains(body, "app-default") {
		t.Fatal("resources must never leak credentials")
	}

	// 给 m1 配实例凭证 ⇒ 只有 m2 仍算"继承共享默认凭证"
	if err := s.UpdateMapping(tun.ID, m1.ID, MappingInput{
		Proto: "tcp", LocalPort: 22, RemotePort: 52001, AppID: a.ID,
		Auth: &AppAuthInput{AuthType: "basic", Username: "panzj", Password: "instance-pw"},
	}); err != nil {
		t.Fatal(err)
	}
	body = fetch()
	if strings.Count(body, `"auth_inherited_shared":true`) != 1 {
		t.Fatalf("配了实例凭证的映射不应再标记继承: %s", body)
	}
	if !strings.Contains(body, `"auth_override":true`) {
		t.Fatal("配了实例凭证的映射应标记 auth_override=true")
	}
	if strings.Contains(body, "instance-pw") {
		t.Fatal("resources must never leak credentials")
	}
}
