package server

import (
	"testing"
)

func TestParsePortRange(t *testing.T) {
	cases := []struct {
		in      string
		wantMin int
		wantMax int
		wantErr bool
	}{
		{in: "", wantMin: 0, wantMax: 0},   // 未指定 = 不限制
		{in: "  ", wantMin: 0, wantMax: 0}, // 空白同样视为不限制
		{in: "30000-40000", wantMin: 30000, wantMax: 40000},
		{in: "30000 - 40000", wantMin: 30000, wantMax: 40000},
		{in: "1024-1024", wantMin: 1024, wantMax: 1024},
		{in: "30000", wantErr: true},       // 缺少 max
		{in: "40000-30000", wantErr: true}, // min > max
		{in: "0-40000", wantErr: true},     // min 越界
		{in: "30000-65536", wantErr: true}, // max 越界
		{in: "abc-40000", wantErr: true},   // 非数字
		{in: "30000-4o000", wantErr: true}, // 非数字
	}
	for _, c := range cases {
		min, max, err := parsePortRange(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parsePortRange(%q) = %d-%d, want error", c.in, min, max)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePortRange(%q) unexpected error: %v", c.in, err)
			continue
		}
		if min != c.wantMin || max != c.wantMax {
			t.Errorf("parsePortRange(%q) = %d-%d, want %d-%d", c.in, min, max, c.wantMin, c.wantMax)
		}
	}
}

// withPortRange installs a temporary range on the package-global opts and
// restores the previous value after the test finishes.
func withPortRange(t *testing.T, min, max int) {
	t.Helper()
	old := opts
	opts = &Options{portRangeMin: min, portRangeMax: max}
	t.Cleanup(func() { opts = old })
}

func TestPortRangeAllowedUnconfigured(t *testing.T) {
	// opts 未设置 (nil) 时必须视为不限制, 且不得 panic
	old := opts
	opts = nil
	t.Cleanup(func() { opts = old })

	if !portRangeAllowed(1) || !portRangeAllowed(65535) {
		t.Fatal("nil opts must mean unrestricted")
	}
	if portRangeConfigured() {
		t.Fatal("nil opts must not report configured")
	}
	if portRangeDesc() != "[unrestricted]" {
		t.Fatalf("portRangeDesc with nil opts = %q", portRangeDesc())
	}
}

func TestPortRangeAllowed(t *testing.T) {
	withPortRange(t, 30000, 40000)

	for _, p := range []int{30000, 31337, 40000} {
		if !portRangeAllowed(p) {
			t.Errorf("port %d inside [30000,40000] must be allowed", p)
		}
	}
	for _, p := range []int{0, 1024, 29999, 40001, 65535} {
		if portRangeAllowed(p) {
			t.Errorf("port %d outside [30000,40000] must be rejected", p)
		}
	}

	for i := 0; i < 100; i++ {
		p := randomPortInRange()
		if p < 30000 || p > 40000 {
			t.Fatalf("randomPortInRange produced out-of-range port %d", p)
		}
	}
}
