package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// hostnameProbeSource prints the node name and exits by whether it matched
// `want`, so a wrong answer names itself on stdout while the exit code
// stays the assertion.
func hostnameProbeSource(want string) string {
	return fmt.Sprintf(`function main(): i32 {
    var h: string = hostname();
    print(h);
    if (h == %q) { return 0; }
    return 1;
}
`, want)
}

// The expected value comes from the same kernel field the builtin reads:
// os.Hostname is uname(2)'s nodename on Linux and sysctl kern.hostname on
// Darwin. A non-empty check would pass a helper that read the wrong
// utsname field — sysname is "Linux" on every box — so the assertion is
// the real name.
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

// The SSA backend has its own helper table, so it is its own leg.
func TestArm64SSAHostname(t *testing.T) {
	want := hostHostname(t)
	out := compileAndRunArm64SSACapture(t, hostnameProbeSource(want))
	if strings.TrimSpace(out) != want {
		t.Errorf("hostname() = %q, want %q", strings.TrimSpace(out), want)
	}
}

func TestInterpHostname(t *testing.T) {
	want := hostHostname(t)
	if code := runInterpExit(t, hostnameProbeSource(want)); code != 0 {
		t.Errorf("hostname() did not match %q (exit %d)", want, code)
	}
}

// WASI has no host identity, so the answer there is the empty string —
// on both previews, and as a value the string runtime can release.
func TestWasmHostnameIsEmpty(t *testing.T) {
	const src = `function main(): i32 {
    var h: string = hostname();
    if (h.len() != 0) { return 1; }
    if (h != "") { return 2; }
    var s: string = "[" + h + "]";
    if (s != "[]") { return 3; }
    return 0;
}
`
	if code := runWasm(t, src); code != 0 {
		t.Errorf("wasm hostname probe exited %d, want 0 (the code names the case)", code)
	}
}
