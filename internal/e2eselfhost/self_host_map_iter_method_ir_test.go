package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
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
	// NOT fall back to the large AST map runtime (the AST path on a bail emits the
	// full ~40KB+ map runtime; the IR path is far smaller for this program).
	if !strings.Contains(string(asm), "movq $16, %rdi") {
		t.Fatal("map_iter did not reach the IR path (no inline iterator-box alloc in asm)")
	}
	// 25 KB keeps a wide margin below the ~40 KB+ AST-bail signature while
	// tolerating IR-runtime growth: the 20 KB threshold tripped at 20,279
	// bytes when #4355's always-emitted __fn___fern_str_arr_free helper
	// landed, with the program still fully on the IR path.
	if len(asm) > 28000 {
		t.Fatalf("asm is %d bytes — expected compact IR output; the module likely bailed to the AST runtime", len(asm))
	}
	progBin := buildBin(t, gcc, dir, "map_iter_method", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 15 {
		t.Errorf("map-iter IR program exited %d, want 15 (7+8)", code)
	}
}
