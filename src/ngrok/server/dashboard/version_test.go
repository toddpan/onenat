package dashboard

import (
	"reflect"
	"strings"
	"testing"
)

func TestVersionFormat(t *testing.T) {
	d := &Dashboard{}
	// 默认 (无覆盖): 单行, 形如 r<release> · proto 2.1.7
	v := d.Version()
	if strings.Contains(v, "\n") {
		t.Fatalf("version must be single-line, got %q", v)
	}
	if !strings.HasPrefix(v, "r") {
		t.Fatalf("version should start with r, got %q", v)
	}
	if !strings.Contains(v, "proto 2.1.7") {
		t.Fatalf("version should contain proto 2.1.7, got %q", v)
	}
}

func TestVersionReleaseOverride(t *testing.T) {
	d := &Dashboard{}
	if !SetRelease("9999.01.01") {
		t.Fatal("SetRelease should return true for non-empty value")
	}
	defer SetRelease("")
	v := d.Version()
	if !strings.Contains(v, "r9999.01.01") {
		t.Fatalf("version should reflect override, got %q", v)
	}
}

func TestSetReleaseEmptyIgnored(t *testing.T) {
	if SetRelease("") {
		t.Fatal("SetRelease(\"\") should return false")
	}
}

func TestAssetsFSHasNoGoFiles(t *testing.T) {
	// 防御: 确认 dashboard 包根目录的 .go (如 version.go) 没被误打进
	// 静态资源 embed (否则 assets/ 目录里会出现 .go)。
	_ = reflect.TypeOf(assetsFS) // assetsFS 是 embed.FS
	if _, err := assetsFS.ReadFile("version.go"); err == nil {
		t.Fatal("version.go should not be inside the assets embed")
	}
}
