package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostEnumStructPayloadDropWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm enum-struct-payload e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	const cap = "16777216"
	prog := `struct Inner { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
enum Box { Full(Inner), Empty }
@noinline function make(): Box {
    return Full(Inner { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) });
}
@noinline function eat(b: Box): i32 {
    match (b) {
        Full(_) => { return 5; },
        Empty => { return 0; },
    }
}
function main(): i32 {
    let s: i32 = 0; let k: i32 = 0;
    while (k < 400000) { s = eat(make()); k = k + 1; }
    return s - 5;
}`
	wat := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes")
	}
	if !strings.Contains(string(wat), "$__sem_drop_Inner") {
		t.Fatalf("emitted WAT missing $__sem_drop_Inner — the enum struct payload did not deep-drop\n--- WAT ---\n%s", wat)
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
