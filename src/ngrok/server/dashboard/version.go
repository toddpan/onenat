package dashboard

import (
	"fmt"
	"sync"

	"ngrok/version"
)

// 版本维护 (单一来源): 每次发版只需改 DefaultRelease 常量, 其余
// (发行包名 / 文档 / systemd) 均以 r<DefaultRelease> 派生。
// 编译期可用 -ldflags 覆盖, 用于在不改源码的情况下打测试/灰度版本:
//   -X ngrok/server/dashboard.ReleaseOverride=2026.09.15-beta1
var (
	// DefaultRelease 当前发行标识 (r 后缀在 Version() 中补)。发版改这一处。
	DefaultRelease = "2026.09.15"

	// ReleaseOverride 编译期覆盖值 (留空=用 DefaultRelease)。
	ReleaseOverride = ""
)

// effectiveRelease 计算生效的发行标识, 优先 ReleaseOverride。
func effectiveRelease() string {
	if ReleaseOverride != "" {
		return ReleaseOverride
	}
	return DefaultRelease
}

var (
	overrideMu  sync.RWMutex
	overrideVer string
)

// SetRelease 运行时覆盖显示版本号 (主要给测试用; 返回 false 表示空值忽略)。
func SetRelease(v string) bool {
	if v == "" {
		return false
	}
	overrideMu.Lock()
	overrideVer = v
	overrideMu.Unlock()
	return true
}

// Version 返回在 Web UI 展示的版本号, 单行: 发行标识 r<...> + 协议版本。
// 例: "r2026.09.15 · proto 2.1.7"。
// 构建时间经 ldflags 注入 buildTime 后可在此拼接 (留空则不显示), 保持单行。
func (d *Dashboard) Version() string {
	overrideMu.RLock()
	v := overrideVer
	overrideMu.RUnlock()
	base := effectiveRelease()
	if v != "" {
		base = v
	}
	return fmt.Sprintf("r%s · proto %s", base, protoFull())
}

// protoFull 协议版本, 形如 "2.1.7" (来自 ngrok/version)。
func protoFull() string {
	return version.Proto + "." + version.Major + "." + version.Minor
}
