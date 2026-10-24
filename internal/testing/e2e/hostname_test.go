package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func hostnameProbeSource(want string) string {
	return fmt.Sprintf(`function main(): i32 {
    let h: string = hostname();
    print(h);
    if (h == %q) { return 0; }
    return 1;
}
`, want)
}

func hostHostname(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname: %v", err)
	}
	if h == "" {
		t.Fatal("os.Hostname is empty; the probe would prove nothing")
	}
	return h
}

func TestX86_64Hostname(t *testing.T) {
	want := hostHostname(t)
	out, code := compileAndRunX86_64(t, hostnameProbeSource(want))
	if code != 0 || strings.TrimSpace(out) != want {
		t.Errorf("hostname() = %q (exit %d), want %q", strings.TrimSpace(out), code, want)
	}
}

func TestArm64Hostname(t *testing.T) {
	want := hostHostname(t)
	out, code := compileAndRunArm64(t, hostnameProbeSource(want))
	if code != 0 || strings.TrimSpace(out) != want {
		t.Errorf("hostname() = %q (exit %d), want %q", strings.TrimSpace(out), code, want)
	}
}

func TestInterpHostname(t *testing.T) {
	want := hostHostname(t)
	if code := runInterpExit(t, hostnameProbeSource(want)); code != 0 {
		t.Errorf("hostname() did not match %q (exit %d)", want, code)
	}
}

func TestWasmHostnameIsEmpty(t *testing.T) {
	const src = `function main(): i32 {
    let h: string = hostname();
    if (h.len() != 0) { return 1; }
    if (h != "") { return 2; }
    let s: string = "[" + h + "]";
    if (s != "[]") { return 3; }
    return 0;
}
`
	if code := runWasm(t, src); code != 0 {
		t.Errorf("wasm hostname probe exited %d, want 0 (the code names the case)", code)
	}
}
