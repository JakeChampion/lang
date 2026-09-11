// `rlimit_nofile` (#8819) end to end on every backend that provides it.
//
// The probe cannot assert a fixed number — the limit is whatever the runner
// was started with — so it asserts the limit the test process ITSELF reads
// through Go, which is the same process ancestry and therefore the same
// ceiling. That is the check with teeth: the ways the helper can be wrong all
// produce a plausible-looking number. Reading `rlim_max` instead of `rlim_cur`
// gives a larger one, reading the second word of a struct laid out backwards
// gives another, and letting Linux's all-ones RLIM_INFINITY through unclamped
// gives -1.
//
// wasm has no leg: neither preview has resource limits, so the `rlimit`
// capability withholds the builtin and the program is refused at check time.
package e2e

import (
	"fmt"
	"os"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
)

// softNofile is the soft RLIMIT_NOFILE of the test process, normalised the
// way the builtin normalises it.
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

// rlimitSource returns 0 when the builtin agrees with the limit the harness
// read, and a code naming the disagreement otherwise. `want` is the answer,
// compared exactly — a soft limit does not drift under a running process.
func rlimitSource(want int64) string {
	return fmt.Sprintf(`function main(): i32 {
    var n: i64 = rlimit_nofile();
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

func TestArm64SSARlimitNofile(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, rlimitSource(softNofile(t)), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (3 = disagreed with the harness's own getrlimit)\n%s", code, stderr)
	}
}

func TestInterpRlimitNofile(t *testing.T) {
	if code := runInterpExit(t, rlimitSource(softNofile(t))); code != 0 {
		t.Fatalf("exit = %d, want 0 (3 = disagreed with the harness's own getrlimit)", code)
	}
}

// Both wasm worlds refuse it, and the refusal has to come from the capability
// scan rather than from the emitter: "this target has no resource limits" is
// a different statement from "the backend forgot one".
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
