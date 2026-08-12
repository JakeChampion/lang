package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMapIterIRWasm pins map iteration (m.iter() / it.has_next() / .key() /
// .value() / .advance()) on the wasm IR path. The register backends iterate their
// parallel-array maps lazily; a wasm map is a hash map with gaps, so map_iter +
// mapiter_* were wasm_eligible exclusions (and the wasm AST path has no iterator
// runtime either — map iteration simply did not work on wasm). They now lower to
// op_map_iter / op_mapiter_* -> a fresh runtime ($__fern_map_iter materializes the
// live entries via the existing $__fern_map_keys / $__fern_map_values snapshots
// into an iterator box [keys@0, vals@4, cursor@8]; the accessors index those
// compacted arrays). It reuses map_helpers, so it pulls in no new structural
// runtime.
//
// Value-tested (not differential — the wasm AST path has no map iteration to diff
// against): the program builds a 3-entry i32->i32 map and sums key+value over a
// full iter() loop. The sum is order-independent, so the wasm hash-slot iteration
// order is irrelevant. Exits with that sum (66) only if every op lowered
// correctly; the test also pins that the IR path was taken (`call $__fern_map_iter`
// in the WAT).
func TestSelfHostMapIterIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-iter wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	// 3 entries; sum of key+value = (1+10)+(2+20)+(3+30) = 66, order-independent.
	const src = `function main(): i32 {
    var m: Map[i32, i32] = map_new(4);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    m = m.insert(3, 30);
    var it: MapIter[i32, i32] = m.iter();
    var sum: i32 = 0;
    while (it.has_next()) {
        sum = sum + it.key() + it.value();
        it.advance();
    }
    return sum;
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("call $__fern_map_iter")) {
		t.Fatal("map iteration did not reach the wasm IR runtime path (no call $__fern_map_iter in WAT)")
	}
	watFile := filepath.Join(dir, "mapiter_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 66 {
		t.Errorf("map-iter wasm IR program exited %d, want 66 (sum of key+value over 3 entries)\n--- WAT ---\n%s", code, wat)
	}
}
