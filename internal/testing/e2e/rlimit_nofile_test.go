package e2e

import (
	"fmt"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func softNofile(t *testing.T) int64 {
	t.Helper()
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatalf("getrlimit: %v", err)
	}
	if lim.Cur > uint64(1<<63-1) {
		return 1<<63 - 1
	}
	return int64(lim.Cur)
}

func rlimitSource(want int64) string {
	return fmt.Sprintf(`function main(): i32 {
    let n: i64 = rlimit_nofile();
    // All-ones RLIM_INFINITY reaching a caller unclamped reads as -1.
    if (n < (0 as i64)) { return 1; }
    // Every kernel enforces a ceiling above the three standard descriptors.
    if (n < (4 as i64)) { return 2; }
    if (n != (%d as i64)) { return 3; }
    return 0;
}
`, want)
}

func TestX86_64RlimitNofile(t *testing.T) {
	if code, out := compileRunX86_64WithSetup(t, rlimitSource(softNofile(t)), nil); code != 0 {
		t.Fatalf("exit = %d, want 0 (3 = disagreed with the harness's own getrlimit)\n%s", code, out)
	}
}

func TestArm64RlimitNofile(t *testing.T) {
	out, code := compileAndRunArm64(t, rlimitSource(softNofile(t)))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (3 = disagreed with the harness's own getrlimit)\n%s", code, out)
	}
}

func TestInterpRlimitNofile(t *testing.T) {
	if code := runInterpExit(t, rlimitSource(softNofile(t))); code != 0 {
		t.Fatalf("exit = %d, want 0 (3 = disagreed with the harness's own getrlimit)", code)
	}
}

func TestWASMRlimitNofileRefused(t *testing.T) {
	prog, err := parser.Parse(rlimitSource(1024))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s accepted rlimit_nofile; it enforces no resource limits", target)
			continue
		}
		if vs[0].Builtin != "rlimit_nofile" || vs[0].Capability != "rlimit" {
			t.Errorf("%s refused %q on %q, want rlimit_nofile on rlimit", target, vs[0].Builtin, vs[0].Capability)
		}
	}
}
