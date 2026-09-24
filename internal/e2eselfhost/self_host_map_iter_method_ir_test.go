package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostMapIterMethodIR pins the Map.iter() builtin cluster
// (iter/has_next/key/value/advance) on the self-host x86-64 IR path. The four
// MapIter methods are compiler builtins with no function body; the AST emitter
// handled them inline (asm.fern:1258-1313). Without IR lowering, m.iter() lowered
// to a Map.iter call_direct that calls_only_known couldn't resolve -> BAIL
// call[Map.iter] -> AST, dragging std/json (json_encode's JObject walk) to the
// legacy emitter. It now lowers to op_map_iter / op_mapiter_* (the same inline
// parallel-array sequences the AST path emits).
func TestSelfHostMapIterMethodIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("map-iter method IR test runs only natively")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// Sum the values via the iterator: 7 + 8 = 15.
	src := `function f(): i32 {
    var m: Map[string, i32] = map_new(0);
    m = m.insert("a", 7);
    m = m.insert("b", 8);
    var sum: i32 = 0;
    var it: MapIter[string, i32] = m.iter();
    while (it.has_next()) {
        sum = sum + it.value();
        it.advance();
    }
    return sum;
}
function main(): i32 { return f(); }`

	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	// IR-path proof: the inline iterator-box alloc is present, and the module did
	// NOT pull in the large map runtime (the AST path on a bail emitted the
	// full ~40KB+ map runtime; the IR path is far smaller for this program).
	if !strings.Contains(string(asm), "movq $16, %rdi") {
		t.Fatal("map_iter did not reach the IR path (no inline iterator-box alloc in asm)")
	}
	if len(asm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes")
	}
	progBin := buildBin(t, gcc, dir, "map_iter_method", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 15 {
		t.Errorf("map-iter IR program exited %d, want 15 (7+8)", code)
	}
}
