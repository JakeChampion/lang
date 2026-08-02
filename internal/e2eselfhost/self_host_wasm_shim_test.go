package e2eselfhost

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

	watbin, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	source := string(watbin) + "\n" + shimCoreSelfTestMain

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

// TestSelfHostWasmComponentSuffixStdout gates the data-driven CLI-component
// suffix generator (wat_component.fern's component_suffix, which runs the
// native composer's lower() Phases B-H + cli/run finish() over an import
// list). A self-test asserts the generated suffixes for three shapes —
// stdout (479B), eprint (545B), exit (531B) — match the native compiler's
// bytes exactly. Check ids: 1/2 stdout, 3/4 eprint, 5/6 exit (len/bytes).
// (fs_read's byte-identity to native is gated by TestSelfHostWasmComponentFullIOFS,
// which byte-compares the whole fs component against the Go reference.)
func TestSelfHostWasmComponentSuffixStdout(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host component-suffix e2e")
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

	watbin, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	source := string(watbin) + "\n" + suffixStdoutSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the component-suffix self-test")
	}
	watPath := filepath.Join(dir, "suffix_stdout_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("component-suffix self-test failed at check %d", code)
	}
}

// suffixStdoutSelfTestMain compares the generated suffixes against the native
// compiler's exact bytes for stdout / eprint / exit. (fs_read's larger byte
// array is verified via the whole-component compare in
// TestSelfHostWasmComponentFullIOFS instead, to keep this self-host compile
// within memory.)
const suffixStdoutSelfTestMain = `
function suffix_eq(got: i32[], want: i32[]): boolean {
    if (got.len() != want.len()) { return false; }
    var i: i32 = 0;
    while (i < got.len()) {
        if (got[i] != want[i]) { return false; }
        i = i + 1;
    }
    return true;
}
function main(): i32 {
    var g0: i32[] = component_suffix_stdout();
    var w0: i32[] = [
` + suffixStdoutBytes + `
    ];
    if (g0.len() != w0.len()) { return 1; }
    if (!suffix_eq(g0, w0)) { return 2; }
    var g1: i32[] = component_suffix_eprint();
    var w1: i32[] = [
` + suffixEprintBytes + `
    ];
    if (g1.len() != w1.len()) { return 3; }
    if (!suffix_eq(g1, w1)) { return 4; }
    var g2: i32[] = component_suffix_exit();
    var w2: i32[] = [
` + suffixExitBytes + `
    ];
    if (g2.len() != w2.len()) { return 5; }
    if (!suffix_eq(g2, w2)) { return 6; }
    return 0;
}
`

// suffixStdoutBytes is the native compiler's 479-byte stdout-component suffix.
const suffixStdoutBytes = `		1, 66, 0, 97, 115, 109, 1, 0, 0, 0, 1, 8, 1, 96, 4, 127, 127, 127, 127, 0,
		3, 2, 1, 0, 4, 5, 1, 112, 1, 1, 1, 7, 16, 2, 1, 48, 0, 0, 8, 36,
		105, 109, 112, 111, 114, 116, 115, 1, 0, 10, 17, 1, 15, 0, 32, 0, 32, 1, 32, 2,
		32, 3, 65, 0, 17, 0, 0, 11, 1, 50, 0, 97, 115, 109, 1, 0, 0, 0, 1, 8,
		1, 96, 4, 127, 127, 127, 127, 0, 2, 21, 2, 0, 1, 48, 0, 0, 0, 8, 36, 105,
		109, 112, 111, 114, 116, 115, 1, 112, 1, 1, 1, 9, 7, 1, 0, 65, 0, 11, 1, 0,
		2, 4, 1, 0, 1, 0, 6, 7, 1, 0, 0, 1, 0, 1, 48, 6, 15, 1, 1, 0,
		2, 10, 103, 101, 116, 45, 115, 116, 100, 111, 117, 116, 8, 5, 1, 1, 0, 0, 0, 2,
		52, 1, 1, 1, 46, 91, 109, 101, 116, 104, 111, 100, 93, 111, 117, 116, 112, 117, 116, 45,
		115, 116, 114, 101, 97, 109, 46, 98, 108, 111, 99, 107, 105, 110, 103, 45, 119, 114, 105, 116,
		101, 45, 97, 110, 100, 45, 102, 108, 117, 115, 104, 0, 0, 2, 16, 1, 1, 1, 10, 103,
		101, 116, 45, 115, 116, 100, 111, 117, 116, 0, 1, 2, 52, 1, 0, 0, 2, 21, 119, 97,
		115, 105, 58, 105, 111, 47, 115, 116, 114, 101, 97, 109, 115, 64, 48, 46, 50, 46, 48, 18,
		1, 21, 119, 97, 115, 105, 58, 99, 108, 105, 47, 115, 116, 100, 111, 117, 116, 64, 48, 46,
		50, 46, 48, 18, 2, 6, 12, 1, 0, 2, 1, 3, 6, 109, 101, 109, 111, 114, 121, 6,
		14, 1, 0, 1, 1, 0, 8, 36, 105, 109, 112, 111, 114, 116, 115, 6, 51, 1, 1, 0,
		1, 46, 91, 109, 101, 116, 104, 111, 100, 93, 111, 117, 116, 112, 117, 116, 45, 115, 116, 114,
		101, 97, 109, 46, 98, 108, 111, 99, 107, 105, 110, 103, 45, 119, 114, 105, 116, 101, 45, 97,
		110, 100, 45, 102, 108, 117, 115, 104, 8, 7, 1, 1, 0, 1, 1, 3, 0, 2, 18, 1,
		1, 2, 8, 36, 105, 109, 112, 111, 114, 116, 115, 1, 0, 1, 48, 0, 2, 2, 7, 1,
		0, 2, 1, 0, 18, 4, 6, 15, 1, 0, 0, 1, 3, 9, 95, 108, 97, 110, 103, 95,
		114, 117, 110, 7, 8, 2, 106, 0, 0, 64, 0, 0, 5, 8, 6, 1, 0, 0, 3, 0,
		6, 5, 10, 1, 1, 1, 0, 3, 114, 117, 110, 1, 2, 11, 24, 1, 0, 18, 119, 97,
		115, 105, 58, 99, 108, 105, 47, 114, 117, 110, 64, 48, 46, 50, 46, 48, 5, 3, 0`

// suffixEprintBytes / suffixExitBytes: native bytes for the eprint
// (stdout + get-stderr) and exit (stdout + cli/exit) shapes.
const suffixEprintBytes = `		1, 66, 0, 97, 115, 109, 1, 0, 0, 0, 1, 8, 1, 96, 4, 127, 127, 127, 127, 0,
		3, 2, 1, 0, 4, 5, 1, 112, 1, 1, 1, 7, 16, 2, 1, 48, 0, 0, 8, 36,
		105, 109, 112, 111, 114, 116, 115, 1, 0, 10, 17, 1, 15, 0, 32, 0, 32, 1, 32, 2,
		32, 3, 65, 0, 17, 0, 0, 11, 1, 50, 0, 97, 115, 109, 1, 0, 0, 0, 1, 8,
		1, 96, 4, 127, 127, 127, 127, 0, 2, 21, 2, 0, 1, 48, 0, 0, 0, 8, 36, 105,
		109, 112, 111, 114, 116, 115, 1, 112, 1, 1, 1, 9, 7, 1, 0, 65, 0, 11, 1, 0,
		2, 4, 1, 0, 1, 0, 6, 7, 1, 0, 0, 1, 0, 1, 48, 6, 15, 1, 1, 0,
		2, 10, 103, 101, 116, 45, 115, 116, 100, 111, 117, 116, 8, 5, 1, 1, 0, 0, 0, 6,
		15, 1, 1, 0, 3, 10, 103, 101, 116, 45, 115, 116, 100, 101, 114, 114, 8, 5, 1, 1,
		0, 1, 0, 2, 52, 1, 1, 1, 46, 91, 109, 101, 116, 104, 111, 100, 93, 111, 117, 116,
		112, 117, 116, 45, 115, 116, 114, 101, 97, 109, 46, 98, 108, 111, 99, 107, 105, 110, 103, 45,
		119, 114, 105, 116, 101, 45, 97, 110, 100, 45, 102, 108, 117, 115, 104, 0, 0, 2, 16, 1,
		1, 1, 10, 103, 101, 116, 45, 115, 116, 100, 111, 117, 116, 0, 1, 2, 16, 1, 1, 1,
		10, 103, 101, 116, 45, 115, 116, 100, 101, 114, 114, 0, 2, 2, 76, 1, 0, 0, 3, 21,
		119, 97, 115, 105, 58, 105, 111, 47, 115, 116, 114, 101, 97, 109, 115, 64, 48, 46, 50, 46,
		48, 18, 1, 21, 119, 97, 115, 105, 58, 99, 108, 105, 47, 115, 116, 100, 111, 117, 116, 64,
		48, 46, 50, 46, 48, 18, 2, 21, 119, 97, 115, 105, 58, 99, 108, 105, 47, 115, 116, 100,
		101, 114, 114, 64, 48, 46, 50, 46, 48, 18, 3, 6, 12, 1, 0, 2, 1, 4, 6, 109,
		101, 109, 111, 114, 121, 6, 14, 1, 0, 1, 1, 0, 8, 36, 105, 109, 112, 111, 114, 116,
		115, 6, 51, 1, 1, 0, 1, 46, 91, 109, 101, 116, 104, 111, 100, 93, 111, 117, 116, 112,
		117, 116, 45, 115, 116, 114, 101, 97, 109, 46, 98, 108, 111, 99, 107, 105, 110, 103, 45, 119,
		114, 105, 116, 101, 45, 97, 110, 100, 45, 102, 108, 117, 115, 104, 8, 7, 1, 1, 0, 2,
		1, 3, 0, 2, 18, 1, 1, 2, 8, 36, 105, 109, 112, 111, 114, 116, 115, 1, 0, 1,
		48, 0, 3, 2, 7, 1, 0, 2, 1, 0, 18, 5, 6, 15, 1, 0, 0, 1, 4, 9,
		95, 108, 97, 110, 103, 95, 114, 117, 110, 7, 8, 2, 106, 0, 0, 64, 0, 0, 6, 8,
		6, 1, 0, 0, 4, 0, 7, 5, 10, 1, 1, 1, 0, 3, 114, 117, 110, 1, 3, 11,
		24, 1, 0, 18, 119, 97, 115, 105, 58, 99, 108, 105, 47, 114, 117, 110, 64, 48, 46, 50,
		46, 48, 5, 4, 0`

const suffixExitBytes = `		1, 66, 0, 97, 115, 109, 1, 0, 0, 0, 1, 8, 1, 96, 4, 127, 127, 127, 127, 0,
		3, 2, 1, 0, 4, 5, 1, 112, 1, 1, 1, 7, 16, 2, 1, 48, 0, 0, 8, 36,
		105, 109, 112, 111, 114, 116, 115, 1, 0, 10, 17, 1, 15, 0, 32, 0, 32, 1, 32, 2,
		32, 3, 65, 0, 17, 0, 0, 11, 1, 50, 0, 97, 115, 109, 1, 0, 0, 0, 1, 8,
		1, 96, 4, 127, 127, 127, 127, 0, 2, 21, 2, 0, 1, 48, 0, 0, 0, 8, 36, 105,
		109, 112, 111, 114, 116, 115, 1, 112, 1, 1, 1, 9, 7, 1, 0, 65, 0, 11, 1, 0,
		2, 4, 1, 0, 1, 0, 6, 7, 1, 0, 0, 1, 0, 1, 48, 6, 15, 1, 1, 0,
		2, 10, 103, 101, 116, 45, 115, 116, 100, 111, 117, 116, 8, 5, 1, 1, 0, 0, 0, 6,
		9, 1, 1, 0, 3, 4, 101, 120, 105, 116, 8, 5, 1, 1, 0, 1, 0, 2, 52, 1,
		1, 1, 46, 91, 109, 101, 116, 104, 111, 100, 93, 111, 117, 116, 112, 117, 116, 45, 115, 116,
		114, 101, 97, 109, 46, 98, 108, 111, 99, 107, 105, 110, 103, 45, 119, 114, 105, 116, 101, 45,
		97, 110, 100, 45, 102, 108, 117, 115, 104, 0, 0, 2, 16, 1, 1, 1, 10, 103, 101, 116,
		45, 115, 116, 100, 111, 117, 116, 0, 1, 2, 10, 1, 1, 1, 4, 101, 120, 105, 116, 0,
		2, 2, 74, 1, 0, 0, 3, 21, 119, 97, 115, 105, 58, 105, 111, 47, 115, 116, 114, 101,
		97, 109, 115, 64, 48, 46, 50, 46, 48, 18, 1, 21, 119, 97, 115, 105, 58, 99, 108, 105,
		47, 115, 116, 100, 111, 117, 116, 64, 48, 46, 50, 46, 48, 18, 2, 19, 119, 97, 115, 105,
		58, 99, 108, 105, 47, 101, 120, 105, 116, 64, 48, 46, 50, 46, 48, 18, 3, 6, 12, 1,
		0, 2, 1, 4, 6, 109, 101, 109, 111, 114, 121, 6, 14, 1, 0, 1, 1, 0, 8, 36,
		105, 109, 112, 111, 114, 116, 115, 6, 51, 1, 1, 0, 1, 46, 91, 109, 101, 116, 104, 111,
		100, 93, 111, 117, 116, 112, 117, 116, 45, 115, 116, 114, 101, 97, 109, 46, 98, 108, 111, 99,
		107, 105, 110, 103, 45, 119, 114, 105, 116, 101, 45, 97, 110, 100, 45, 102, 108, 117, 115, 104,
		8, 7, 1, 1, 0, 2, 1, 3, 0, 2, 18, 1, 1, 2, 8, 36, 105, 109, 112, 111,
		114, 116, 115, 1, 0, 1, 48, 0, 3, 2, 7, 1, 0, 2, 1, 0, 18, 5, 6, 15,
		1, 0, 0, 1, 4, 9, 95, 108, 97, 110, 103, 95, 114, 117, 110, 7, 8, 2, 106, 0,
		0, 64, 0, 0, 6, 8, 6, 1, 0, 0, 4, 0, 7, 5, 10, 1, 1, 1, 0, 3,
		114, 117, 110, 1, 3, 11, 24, 1, 0, 18, 119, 97, 115, 105, 58, 99, 108, 105, 47, 114,
		117, 110, 64, 48, 46, 50, 46, 48, 5, 4, 0`
