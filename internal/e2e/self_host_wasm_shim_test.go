package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWasmShimCore gates the generative suffix builder's shim-core
// generators (wat_component.fern's shim_trampoline_core / shim_tablefill_core).
// A self-test compiled and run through the self-host asserts that, for the
// stdout blocking-write-and-flush signature (i32 i32 i32 i32) -> (), the two
// generated shim core modules match the native compiler's bytes (captured as
// io_suffix's first two sections). Returns 0 on pass, a check id on failure.
func TestSelfHostWasmShimCore(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host shim-core e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	leb, err := os.ReadFile("../../examples/self_host/leb128.fern")
	if err != nil {
		t.Fatalf("read leb128.fern: %v", err)
	}
	enc, err := os.ReadFile("../../examples/self_host/wat_encode.fern")
	if err != nil {
		t.Fatalf("read wat_encode.fern: %v", err)
	}
	comp, err := os.ReadFile("../../examples/self_host/wat_component.fern")
	if err != nil {
		t.Fatalf("read wat_component.fern: %v", err)
	}
	source := string(leb) + "\n" + string(enc) + "\n" + string(comp) + "\n" + shimCoreSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the shim-core self-test")
	}
	watPath := filepath.Join(dir, "shim_core_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("shim-core self-test failed at check %d", code)
	}
}

// shimCoreSelfTestMain compares the two generated shim cores for the stdout
// bwf signature against the native bytes. Check ids: 1 = trampoline length,
// 2 = trampoline bytes, 3 = tablefill length, 4 = tablefill bytes.
const shimCoreSelfTestMain = `
function shim_eq(got: i32[], want: i32[]): boolean {
    if (got.len() != want.len()) { return false; }
    var i: i32 = 0;
    while (i < got.len()) {
        if (got[i] != want[i]) { return false; }
        i = i + 1;
    }
    return true;
}
function main(): i32 {
    var sig: i32[] = [127, 127, 127, 127];
    var none: i32[] = [];
    var want0: i32[] = [0, 97, 115, 109, 1, 0, 0, 0, 1, 8, 1, 96, 4, 127, 127, 127, 127, 0, 3, 2, 1, 0, 4, 5, 1, 112, 1, 1, 1, 7, 16, 2, 1, 48, 0, 0, 8, 36, 105, 109, 112, 111, 114, 116, 115, 1, 0, 10, 17, 1, 15, 0, 32, 0, 32, 1, 32, 2, 32, 3, 65, 0, 17, 0, 0, 11];
    var t: i32[] = shim_trampoline_core(sig, none);
    if (t.len() != want0.len()) { return 1; }
    if (!shim_eq(t, want0)) { return 2; }
    var want1: i32[] = [0, 97, 115, 109, 1, 0, 0, 0, 1, 8, 1, 96, 4, 127, 127, 127, 127, 0, 2, 21, 2, 0, 1, 48, 0, 0, 0, 8, 36, 105, 109, 112, 111, 114, 116, 115, 1, 112, 1, 1, 1, 9, 7, 1, 0, 65, 0, 11, 1, 0];
    var f: i32[] = shim_tablefill_core(sig, none);
    if (f.len() != want1.len()) { return 3; }
    if (!shim_eq(f, want1)) { return 4; }
    return 0;
}
`
