// `process_alive` (#8767) end to end on every backend that provides it.
//
// The probe is written against processes the runner is guaranteed to have an
// answer about rather than ones it spawns: pid 1 always exists, and a pid
// above the kernel's own pid_max never has. Both arms of the "yes" — success
// and EPERM — are reachable that way without the test needing to know whether
// it runs as root, which is exactly the distinction the primitive must NOT
// make.
//
// wasm has no leg: neither WASI preview has a process table or pids, so the
// `proc` capability withholds the builtin and E066 refuses the program at
// check time. TestWASMProcessAliveRefused is that half.
package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
)

// Each failing step returns its own code.
//
// 4194304 is 2^22, one above the largest pid_max Linux accepts, so no process
// can ever hold it — where a pid merely "not in use right now" could be
// reassigned between the compile and the run.
const processAliveSource = `function main(): i32 {
    // pid 1 exists on any system this runs on. When the caller is not root
    // the kernel answers EPERM, which is still a live process — a helper
    // that only accepted a zero return would fail here and nowhere else.
    if (!process_alive(1)) { return 1; }

    // Above the kernel's own pid ceiling: ESRCH, and never a live process.
    if (process_alive(4194304)) { return 2; }

    // kill(2) reads these as process GROUPS, not processes. Passing one
    // through would answer a different question and report the caller's own
    // group as alive.
    if (process_alive(0)) { return 3; }
    if (process_alive(0 - 1)) { return 4; }
    return 0;
}
`

func TestX86_64ProcessAlive(t *testing.T) {
	if code, out := compileRunX86_64WithSetup(t, processAliveSource, nil); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see processAliveSource)\n%s", code, out)
	}
}

func TestArm64ProcessAlive(t *testing.T) {
	out, code := compileAndRunArm64(t, processAliveSource)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see processAliveSource)\n%s", code, out)
	}
}

func TestArm64SSAProcessAlive(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, processAliveSource, os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see processAliveSource)\n%s", code, stderr)
	}
}

func TestInterpProcessAlive(t *testing.T) {
	if code := runInterpExit(t, processAliveSource); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see processAliveSource)", code)
	}
}

// The wasm answer is a refusal, and it has to arrive from the capability
// scan rather than as an `unknown callee` out of the emitter — the difference
// between "this target has no processes" and "the backend forgot one". Both
// previews, since `proc` is withheld by each.
func TestWASMProcessAliveRefused(t *testing.T) {
	prog, err := parser.Parse(processAliveSource)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s accepted process_alive; it has no process table to ask", target)
			continue
		}
		if vs[0].Builtin != "process_alive" || vs[0].Capability != "proc" {
			t.Errorf("%s refused %q on %q, want process_alive on proc", target, vs[0].Builtin, vs[0].Capability)
		}
	}
}
