package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeSkillName(t *testing.T) {
	ok := map[string]string{
		"kb-api.md":   ".md",
		"usage_v2.txt": ".txt",
		"A1.b.c.yaml":  ".yaml",
	}
	for name, ext := range ok {
		_, got, err := SanitizeSkillName(name)
		if err != nil || got != ext {
			t.Fatalf("%q: got %q err=%v", name, got, err)
		}
	}
	bad := []string{
		"", ".hidden.md", "..md", "a/../b.md", "a/b.md", `a\b.md`,
		"中文名.md", "a b.md", "shell.sh", "run.exe", "no-ext",
		strings.Repeat("x", 90) + ".md",
	}
	for _, name := range bad {
		if _, _, err := SanitizeSkillName(name); err == nil {
			t.Fatalf("%q must be rejected", name)
		}
	}
}

func TestSkillDiskRoundtrip(t *testing.T) {
	dir := t.TempDir()
	content := []byte("# hello 应用")
	p, err := WriteSkillFile(dir, "app-abc", "sk-1234", ".md", content)
	if err != nil {
		t.Fatal(err)
	}
	// 路径必须是 dir/app-abc/sk-1234.md — 用户输入永不进入磁盘路径
	if p != filepath.Join(dir, "app-abc", "sk-1234.md") {
		t.Fatalf("unexpected path %q", p)
	}
	got, err := ReadSkillFile(dir, "app-abc", "sk-1234", ".md")
	if err != nil || string(got) != string(content) {
		t.Fatalf("roundtrip: %v %q", err, got)
	}
	// 0600 权限
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("skill file must be 0600, got %v", fi.Mode().Perm())
	}
	// 穿越攻击被拒绝 (双保险: 即使 id 被污染)
	if _, err := SkillDiskPath(dir, "../evil", "sk-1", ".md"); err == nil {
		t.Fatal("traversal appID must be rejected")
	}
	if _, err := SkillDiskPath(dir, "app-abc", "../../evil", ".md"); err == nil {
		t.Fatal("traversal skillID must be rejected")
	}
	if _, err := SkillDiskPath(dir, "app-abc", "sk-1", ".sh"); err == nil {
		t.Fatal("non-whitelisted ext must be rejected")
	}
	// 删除 + 空目录清理
	if err := DeleteSkillFile(dir, "app-abc", "sk-1234", ".md"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "app-abc")); !os.IsNotExist(err) {
		t.Fatal("empty app dir must be cleaned up")
	}
}

func TestValidateSkillContent(t *testing.T) {
	if err := ValidateSkillContent([]byte("")); err == nil {
		t.Fatal("empty content must fail")
	}
	if err := ValidateSkillContent(make([]byte, maxSkillSize+1)); err == nil {
		t.Fatal("oversize must fail")
	}
	if err := ValidateSkillContent([]byte("ok\x00binary")); err == nil {
		t.Fatal("NUL byte must fail")
	}
	if err := ValidateSkillContent([]byte("# 正常内容 🚀")); err != nil {
		t.Fatalf("valid content rejected: %v", err)
	}
}

func TestSkillSkeletonRender(t *testing.T) {
	// 无法直接构造 Dashboard (依赖 store); 通过纯函数验证骨架内容
	// renderSkillSkeleton 是方法但只依赖参数 — 用零值 Dashboard 测试
	d := &Dashboard{}
	for _, typ := range []string{"ssh", "http-api", "web", "database", "custom"} {
		out, err := d.renderSkillSkeleton(typ, "KB API")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "KB API") || !strings.Contains(out, "---") {
			t.Fatalf("skeleton for %s missing basics", typ)
		}
		if strings.Contains(out, "\n\"") {
			t.Fatalf("frontmatter must be sanitized for %s", typ)
		}
	}
	// 平台预制 SSH 技能: 关键章节必须齐全
	ssh, _ := d.renderSkillSkeleton("ssh", "SSH Server")
	for _, want := range []string{"连接入口", "认证", "常用操作", "排障", "安全红线", "sshpass", "scp -P"} {
		if !strings.Contains(ssh, want) {
			t.Fatalf("builtin SSH skill missing %q", want)
		}
	}
}
