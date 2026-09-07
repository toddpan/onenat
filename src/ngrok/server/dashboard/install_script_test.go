package dashboard

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"text/template"
)

// TestInstallScriptRestartsExistingService guards the reinstall path: the
// systemd branch must restart the service (not merely `enable --now`), or a
// re-run of install.sh on a machine that already runs the client writes a new
// config that the old process never loads (stale auth token -> "Invalid
// authentication token").
func TestInstallScriptRestartsExistingService(t *testing.T) {
	tmpl, err := template.New("install").Parse(installScriptTmpl)
	if err != nil {
		t.Fatalf("parse install template: %v", err)
	}
	var b strings.Builder
	err = tmpl.Execute(&b, map[string]string{
		"BaseURL":       "http://127.0.0.1:18080",
		"ChecksumTable": "    ngrok_linux_amd64) echo \"deadbeef\" ;;\n",
	})
	if err != nil {
		t.Fatalf("render install template: %v", err)
	}
	script := b.String()

	if !strings.Contains(script, "systemctl restart ngrok-client") {
		t.Fatal("systemd branch must 'systemctl restart ngrok-client' so an already-running service picks up the new config/token")
	}
	if strings.Contains(script, "enable --now ngrok-client") {
		t.Fatal("stale 'systemctl enable --now ngrok-client' found: --now does not restart an active service")
	}
	// nohup 分支同样必须先杀旧进程再启动 (重装生效)
	if !strings.Contains(script, `pkill -f "ngrok.*managed"`) {
		t.Fatal("nohup branch must pkill the old process before starting a new one")
	}
	// launchd 分支必须 unload 后再 load (重装生效)
	if !strings.Contains(script, "launchctl unload") || !strings.Contains(script, "launchctl load") {
		t.Fatal("launchd branch must unload+load to reload the new config")
	}

	// 渲染产物必须是合法 POSIX sh
	if _, err := exec.LookPath("sh"); err == nil {
		f, err := os.CreateTemp("", "install-*.sh")
		if err != nil {
			t.Fatalf("temp file: %v", err)
		}
		defer os.Remove(f.Name())
		if _, err := f.WriteString(script); err != nil {
			t.Fatalf("write script: %v", err)
		}
		f.Close()
		if out, err := exec.Command("sh", "-n", f.Name()).CombinedOutput(); err != nil {
			t.Fatalf("rendered install.sh fails `sh -n`: %v\n%s", err, out)
		}
	}
}
