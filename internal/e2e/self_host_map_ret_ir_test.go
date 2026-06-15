package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMapRetUnannotatedIRX86_64 verifies that an UNANNOTATED binding
// from a `Map[K, V]`-returning function — `var m = build()` (no `: Map[K,V]`) —
// is admitted to the IR path and that the map dispatches correctly. The
// annotated form (`var m: Map[K,V] = build()`) already lowered; the unannotated
// form used to leave m untyped, so `m.get_or(...)` didn't dispatch as a map and
// the whole module bailed to the ~35 KB AST runtime. A new `map_ret_fns`
// registry (the map sibling of opt_ret_fns) lets the StmtVar recover m's map
// type from the callee. See #3317 (gap 3).
//
// build returns Map{1:5, 2:6}; main reads it unannotated and returns
// m.get_or(1,0) + m.get_or(2,0) = 5 + 6 = 11. The small asm proves IR
// admission; the exit code proves the round-trip.
func TestSelfHostMapRetUnannotatedIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	prog := `import "core/map";
function build(): Map[i32, i32] { return Map { 1: 5, 2: 6 }; }
function main(): i32 { var m = build(); return m.get_or(1, 0) + m.get_or(2, 0); }`
	asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(asm) == 0 || len(asm) > 20000 {
		t.Fatalf("asm is %d bytes — expected IR output (with map helpers); the unannotated map-returning-call binding likely bailed to the AST runtime", len(asm))
	}
	progBin := buildBin(t, gcc, dir, "map_ret_unannotated", string(asm))
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 11 {
		t.Errorf("exit %d, want 11 (m.get_or(1,0) + m.get_or(2,0))", code)
	}
}
