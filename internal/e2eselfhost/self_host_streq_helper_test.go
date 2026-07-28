package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStreqHelperGap is a regression test for the self-host wasm
// backend's string-helper emission gap: a program that uses string equality
// (which lowers to a `$__fern_streq` call) but declares no string *literals*
// must still emit the string runtime. Before the fix, `has_strings` was gated
// solely on the string-literal table, so this program referenced an undefined
// `$__fern_streq` and wasmtime rejected the module. The program gets its
// strings from string_from_bytes_unchecked + string-typed declarations (no literals),
// then compares them.
func TestSelfHostStreqHelperGap(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host streq-helper e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	leb, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	// No string literals anywhere; strings come from string_from_bytes_unchecked and
	// string-typed values, and are compared with ==.
	const prog = `
function mk(c: i32): string { return string_from_bytes_unchecked([c as u8]); }
function streq(a: string, b: string): boolean { return a == b; }
function main(): i32 {
    var a: string = mk(120);
    var b: string = mk(120);
    var d: string = mk(121);
    if (!streq(a, b)) { return 1; }
    if (streq(a, d)) { return 2; }
    if (a == d) { return 3; }
    return 0;
}
`
	wat := runCapture(t, gcc, runner, driverBin, []byte(string(leb)+"\n"+prog))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes")
	}
	watPath := filepath.Join(dir, "streq.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("streq program failed (exit %d) — string runtime not emitted?\n%s", code, out)
	}
}
