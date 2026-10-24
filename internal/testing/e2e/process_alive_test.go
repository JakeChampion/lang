package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

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

func TestInterpProcessAlive(t *testing.T) {
	if code := runInterpExit(t, processAliveSource); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see processAliveSource)", code)
	}
}

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
