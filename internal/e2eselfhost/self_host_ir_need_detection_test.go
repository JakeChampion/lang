package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostIRNeedDetectionX86_64 guards the #3425 fix: emit_module_ir's
// runtime-need detection (does the module use maps? does it allocate?) is now
// folded into the SINGLE per-function lowering the emit loop already performs,
// instead of re-lowering the whole module twice in the tail (the old
// module_uses_maps / module_uses_heap passes — each a full extra lower_func
// pass over every function, which on the ~1000-function self-host compiler
// retained enough per-function ops to blow the bump heap and OOM the IR
// self-compile).
//
// The behavioural contract the fold must preserve: a program that uses a Map
// (needs the map runtime) AND allocates on the heap (needs the allocator + RC
// runtime) still pulls BOTH runtimes in when emitted via the IR path. If the
// per-loop need-marking missed either, the emitted asm would reference an
// undefined __fern_map_* / __fern_alloc and fail to link — so a successful
// link + correct exit code proves the needs were marked off the ops the emit
// loop lowered, with no redundant re-lowering pass.
func TestSelfHostIRNeedDetectionX86_64(t *testing.T) {
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

	for _, tc := range []struct {
		name string
		prog string
		want int
	}{
		// Map + heap (array) in one module: both runtimes must be emitted.
		{"map-and-array", `function f(): i32 {
	var m: Map[string, i32] = map_new(0);
	m = m.insert("a", 7);
	m = m.insert("b", 8);
	var xs: i32[] = [1, 2, 3];
	var s: i32 = m.get_or("a", 0) + m.get_or("b", 0);
	var i: i32 = 0;
	while (i < xs.len()) { s = s + xs[i]; i = i + 1; }
	return s;
}
function main(): i32 { return f(); }`, 21},
		// Heap-only (array allocation, no map): the allocator/RC runtime is still
		// pulled in by the op_allocates marking, with no "maps" need.
		{"array-only", `function main(): i32 {
	var xs: i32[] = [10, 20, 30];
	var s: i32 = 0;
	var i: i32 = 0;
	while (i < xs.len()) { s = s + xs[i]; i = i + 1; }
	return s;
}`, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.prog))
			if len(asm) == 0 {
				t.Fatalf("driver produced no asm")
			}
			// IR path produces a far smaller binary than the ~40 KB AST map/heap
			// runtime; a generous bound just confirms the IR path was taken (a
			// bail would refuse the module outright).
			if len(asm) > 33000 {
				t.Fatalf("asm is %d bytes — expected the compact IR runtime; the module likely took a wider runtime", len(asm))
			}
			progBin := buildBin(t, gcc, dir, "need_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("exit %d, want %d", code, tc.want)
			}
		})
	}
}
