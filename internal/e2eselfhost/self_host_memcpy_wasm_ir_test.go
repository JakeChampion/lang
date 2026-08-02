package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMemcpyWasmIR pins the wasm IR backend's op_memcpy emission: a
// `__memcpy(dst, src, n)` lowers to the bulk-memory `memory.copy` instruction
// (the wasm sibling of x86-64 `rep movsb` / the arm64 byte loop). Before
// op_memcpy, `__memcpy` was BAIL call on every backend, keeping core/int's
// int_to_string (and everything calling it) off the IR path.
//
// Cloning a same-size u8[] box makes the copy observable: the wasm runtime lays
// a u8[] out as an [len@0, cap@4] header (8 bytes) + 3 4-byte slots = 20 bytes,
// so copying 20 bytes byte-clones src into dst and dst[0..2] read back src's
// 5/7/9 -> 21. (The x86-64 / arm64 differential cases in TestSelfHostAsmIRPath
// cover those backends' layouts — 8-byte slots, count 32; the interp can't
// oracle-check here because it rejects the `as usize` cast __memcpy needs, so
// the expected value is the manually verified wasm-layout clone result.) A
// broken / missing memory.copy emission would produce invalid WAT (wasmtime
// won't run it) or the wrong sum.
func TestSelfHostMemcpyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host memcpy wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	src := `function main(): i32 {
    var src: u8[] = __alloc_u8(3);
    src = src.with(0, 5 as u8); src = src.with(1, 7 as u8); src = src.with(2, 9 as u8);
    var dst: u8[] = __alloc_u8(3);
    __memcpy(dst as usize, src as usize, 20);
    return (dst[0] as i32) + (dst[1] as i32) + (dst[2] as i32);
}`
	const want = 21 // 5 + 7 + 9, after the 20-byte (header + 3*4-slot) clone

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
	watFile := filepath.Join(dir, "memcpy_ir.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	rcmd := exec.Command("wasmtime", "run", watFile)
	_ = rcmd.Run()
	if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally (likely invalid WAT — broken memory.copy):\n%s", wat)
	}
	if got := rcmd.ProcessState.ExitCode(); got != want {
		t.Errorf("memcpy wasm IR exited %d, want %d", got, want)
	}
}
