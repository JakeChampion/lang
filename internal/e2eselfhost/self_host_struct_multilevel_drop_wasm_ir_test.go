package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStructMultiLevelDropWasm is the wasm mirror of the MULTI-LEVEL
// deep-drop (the x86 sibling is TestSelfHostStructMultiLevelDropIRX86_64). The
// reclaim decision is the shared irlower `nested_field_deep_drop_ok` (now an
// acyclic-closure gate), so wasm gets multi-level for free; wasm_ir's
// struct_drop_types scan plus its index-driven transitive closure emit
// `$__struct_drop_A/_B/_C` — the closure re-reads its worklist length as it appends,
// so it chains beyond depth-1.
//
// Reclaim is proven by a memory-cap differential: a long alloc->drop churn over a
// fresh 3-level `A -> B -> C{ items }` stays bounded under a tight max-memory-size cap
// with trap-on-grow-failure (the whole chain's boxes + the items buffer recycle onto
// the freelist); a regression to the leaf-only drop leaks B.c + C.items past the cap
// and traps. The WAT assertion pins that the recursive $__struct_drop_B (the non-leaf
// inner) is emitted.
func TestSelfHostStructMultiLevelDropWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm multi-level deep-drop e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	const cap = "16777216" // 16 MiB
	prog := `struct C { items: i32[] }
struct B { c: C, bt: i32 }
struct A { b: B, at: i32 }
function mk(): i32 {
    var a: A = A { b: B { c: C { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] }, bt: 2 }, at: 7 };
    return a.b.c.items[0] + a.b.c.items[15] + a.b.bt + a.at;
}
function main(): i32 {
    var s: i32 = 0; var k: i32 = 0;
    while (k < 400000) { s = mk(); k = k + 1; }
    return s - 26;
}`
	wat := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes")
	}
	if !strings.Contains(string(wat), "$__struct_drop_B") {
		t.Fatalf("emitted WAT missing $__struct_drop_B — the multi-level nested field did not deep-drop\n--- WAT ---\n%s", wat)
	}
	watPath := filepath.Join(dir, "struct_multilevel_drop.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run",
		"-W", "max-memory-size="+cap,
		"-W", "trap-on-grow-failure=y",
		"--dir", dir, watPath)
	_, _ = cmd.Output()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wasm exited %d, want 0 (a trap means the chain leaked past the %s-byte cap — the multi-level nested field did not reclaim)\n--- WAT ---\n%s", code, cap, wat)
	}
}
