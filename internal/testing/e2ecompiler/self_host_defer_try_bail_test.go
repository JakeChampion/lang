package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostDeferTryOpBailsRatherThanRecursing(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	shDir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, shDir, "drivers/asm_modload_run.fern", "defer_try_bail_driver")

	src := `function g(v: i32): Option[i32] {
    if (v < 100) { return Some(v + 1); }
    return None;
}
function build(): Option[i32] {
    let n: i32 = 0;
    defer n = g(n)?;
    return Some(n);
}
function main(): i32 { build(); return 0; }
`
	entry := filepath.Join(t.TempDir(), "defer_try.fern")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	for _, args := range [][]string{{"-per-module-emit", "0"}, nil} {
		_, stderr, code := runDriver(t, runner, driverBin, nil, true, append([]string{entry}, args...)...)
		if code != 3 {
			t.Fatalf("%v: exited %d, want 3 (a refusal) — a signal here is the recursion back\n%s", args, code, stderr)
		}
		if !strings.Contains(stderr, "build: `?` inside a defer action (E079)") {
			t.Errorf("%v: refusal does not name build and the reason\n%s", args, stderr)
		}
	}
}
