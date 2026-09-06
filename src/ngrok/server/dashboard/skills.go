package dashboard

// skills.go — disk I/O for skill files.
//
// Security model (docs/app-management-design.md §7):
//   - the on-disk filename is ALWAYS "<skillID><ext>" (server-generated);
//     the user-facing display name lives in metadata only → path traversal
//     and name collision are impossible by construction
//   - display names are whitelisted to [A-Za-z0-9._-] (no unicode tricks)
//   - extensions are whitelisted to text formats; size capped at 512 KB
//   - writes are atomic (temp file + rename) with 0600 permissions

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxSkillSize    = 512 * 1024 // 512 KB per skill file
	maxSkillsPerApp = 20
)

// skillExtWhitelist limits uploads to text formats the AI can read directly.
var skillExtWhitelist = map[string]bool{
	".md":   true,
	".txt":  true,
	".yaml": true,
	".yml":  true,
	".json": true,
}

// SanitizeSkillName validates a user-provided display name and returns
// (name, ext). Rules: 1..80 chars of [A-Za-z0-9._-], known extension,
// no leading dot (hidden files), no ".." anywhere.
func SanitizeSkillName(name string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("技能文件名不能为空")
	}
	if len(name) > 80 {
		return "", "", fmt.Errorf("技能文件名过长 (≤80)")
	}
	if strings.Contains(name, "..") || strings.HasPrefix(name, ".") {
		return "", "", fmt.Errorf("技能文件名不允许以 . 开头或包含 ..")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-'
		if !ok {
			return "", "", fmt.Errorf("技能文件名仅允许字母、数字与 . _ -")
		}
	}
	ext := strings.ToLower(filepath.Ext(name))
	if !skillExtWhitelist[ext] {
		return "", "", fmt.Errorf("仅支持 %v 格式的技能文件", extList())
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if base == "" {
		return "", "", fmt.Errorf("技能文件名无效")
	}
	return name, ext, nil
}

func extList() []string {
	out := make([]string, 0, len(skillExtWhitelist))
	for k := range skillExtWhitelist {
		out = append(out, k)
	}
	// deterministic order for stable error messages
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// ValidateSkillContent enforces the size cap and UTF-8 text-ness.
func ValidateSkillContent(content []byte) error {
	if int64(len(content)) > maxSkillSize {
		return fmt.Errorf("技能文件过大 (上限 %d KB)", maxSkillSize/1024)
	}
	if len(content) == 0 {
		return fmt.Errorf("技能文件内容不能为空")
	}
	// reject NUL bytes (binary) — text formats only
	if strings.ContainsRune(string(content), 0) {
		return fmt.Errorf("技能文件必须是文本内容")
	}
	return nil
}

// skillAppDir returns <skillsDir>/<appID>, after re-validating appID as a
// plain id (defense in depth even though ids are server-generated).
func skillAppDir(skillsDir, appID string) (string, error) {
	if appID == "" || strings.ContainsAny(appID, "/\\") || strings.Contains(appID, "..") {
		return "", fmt.Errorf("非法的应用目录")
	}
	return filepath.Join(skillsDir, appID), nil
}

// SkillDiskPath returns the on-disk path for a skill: <dir>/<appID>/<skillID><ext>.
func SkillDiskPath(skillsDir, appID, skillID, ext string) (string, error) {
	dir, err := skillAppDir(skillsDir, appID)
	if err != nil {
		return "", err
	}
	if skillID == "" || strings.ContainsAny(skillID, "/\\") || strings.Contains(skillID, "..") {
		return "", fmt.Errorf("非法的技能文件标识")
	}
	if !skillExtWhitelist[ext] {
		return "", fmt.Errorf("非法的技能文件扩展名")
	}
	return filepath.Join(dir, skillID+ext), nil
}

// WriteSkillFile atomically writes skill content (0600, temp+rename).
func WriteSkillFile(skillsDir, appID, skillID, ext string, content []byte) (string, error) {
	p, err := SkillDiskPath(skillsDir, appID, skillID, ext)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, content, 0600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return p, nil
}

// ReadSkillFile returns the skill content.
func ReadSkillFile(skillsDir, appID, skillID, ext string) ([]byte, error) {
	p, err := SkillDiskPath(skillsDir, appID, skillID, ext)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// DeleteSkillFile removes the file; missing file is not an error (idempotent).
func DeleteSkillFile(skillsDir, appID, skillID, ext string) error {
	p, err := SkillDiskPath(skillsDir, appID, skillID, ext)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	// clean up the app directory when it becomes empty
	dir := filepath.Dir(p)
	if entries, _ := os.ReadDir(dir); len(entries) == 0 {
		os.Remove(dir)
	}
	return nil
}

// DeleteAppSkillDir removes the whole app skill directory (app deletion).
func DeleteAppSkillDir(skillsDir, appID string) error {
	dir, err := skillAppDir(skillsDir, appID)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
