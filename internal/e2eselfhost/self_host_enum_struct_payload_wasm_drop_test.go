package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostEnumStructPayloadDropWasm is the wasm mirror of the enum-payload
// struct deep-drop (the x86 sibling is TestSelfHostEnumStructPayloadDropIRX86_64).
// The reclaim is emitted in the shared irlower lowering (an IR
// call_direct("__struct_drop_<Inner>")), so wasm gets it for free; wasm_ir's
// struct_drop_types scan (plus the deep-drop transitive closure) emits the
// $__struct_drop_Inner body. Reclaim is proven by a memory-cap differential: a long
// consume-by-match churn over a fresh `Full(Inner{items:[..]})` stays bounded under a
// tight max-memory-size cap with trap-on-grow-failure (the payload buffer + boxes are
// reclaimed onto the freelist and reused); a regression to the leak blows past the cap
// and traps. The WAT assertion pins that the recursive $__struct_drop_Inner is emitted.
func TestSelfHostEnumStructPayloadDropWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm enum-struct-payload e2e")
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
	prog := `struct Inner { items: i32[] }
enum Box { Full(Inner), Empty }
function mk(): i32 {
    var b: Box = Full(Inner { items: [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16] });
    match (b) {
        Full(_) => {},
        Empty => {},
    }
    return 5;
}
function main(): i32 {
    var s: i32 = 0; var k: i32 = 0;
    while (k < 400000) { s = mk(); k = k + 1; }
    return s - 5;
}`
	wat := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes")
	}
	if !strings.Contains(string(wat), "$__struct_drop_Inner") {
		t.Fatalf("emitted WAT missing $__struct_drop_Inner — the enum struct payload did not deep-drop\n--- WAT ---\n%s", wat)
	}
	watPath := filepath.Join(dir, "enum_struct_payload.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run",
		"-W", "max-memory-size="+cap,
		"-W", "trap-on-grow-failure=y",
		"--dir", dir, watPath)
	_, _ = cmd.Output()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wasm exited %d, want 0 (a trap means the payload leaked past the %s-byte cap — the enum struct payload did not reclaim)\n--- WAT ---\n%s", code, cap, wat)
	}
}
