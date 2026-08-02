package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostLEB128 exercises the self-hosted binary-wasm LEB128 byte
// encoders, which live in examples/self_host/watbin.fern (the WAT-text
// emitter is wasm_ir.fern).
//
// watbin.fern is a single import-free module, so this test reads it from
// disk and concatenates it with a self-test main() that encodes the
// textbook LEB128 vectors and asserts the resulting bytes, then runs the
// combined program through the existing self-host wasm pipeline
// (wasm_run -> WAT -> wasmtime). The program returns 0 when every vector
// matches, or the (1-based) number of the failing check — which the error
// message surfaces — so a regression points straight at the bad case.
func TestSelfHostLEB128(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host leb128 e2e")
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

	leb, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	source := string(leb) + "\n" + leb128SelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the leb128 self-test")
	}
	watPath := filepath.Join(dir, "leb128_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("leb128 self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// leb128SelfTestMain checks the canonical LEB128 vectors. Each `return N`
// is a distinct failing-check id (0 = all pass). Byte values are decimal
// (0xE5=229, 0x8E=142, 0x26=38, 0xC0=192, 0xBF=191, 0x7F=127).
const leb128SelfTestMain = `
function main(): i32 {
    var b: i32[] = [];
    b = leb_u32([], 0);        if (b.len() != 1 || b[0] != 0) { return 1; }
    b = leb_u32([], 127);      if (b.len() != 1 || b[0] != 127) { return 2; }
    b = leb_u32([], 128);      if (b.len() != 2 || b[0] != 128 || b[1] != 1) { return 3; }
    b = leb_u32([], 624485);   if (b.len() != 3 || b[0] != 229 || b[1] != 142 || b[2] != 38) { return 4; }
    b = leb_i32([], 0);        if (b.len() != 1 || b[0] != 0) { return 5; }
    b = leb_i32([], 0 - 1);    if (b.len() != 1 || b[0] != 127) { return 6; }
    b = leb_i32([], 63);       if (b.len() != 1 || b[0] != 63) { return 7; }
    b = leb_i32([], 64);       if (b.len() != 2 || b[0] != 192 || b[1] != 0) { return 8; }
    b = leb_i32([], 0 - 64);   if (b.len() != 1 || b[0] != 64) { return 9; }
    b = leb_i32([], 0 - 65);   if (b.len() != 2 || b[0] != 191 || b[1] != 127) { return 10; }
    var z: i64 = 0;            b = leb_i64([], z);   if (b.len() != 1 || b[0] != 0) { return 11; }
    var n1: i64 = 0 - 1;       b = leb_i64([], n1);  if (b.len() != 1 || b[0] != 127) { return 12; }
    var p64: i64 = 64;         b = leb_i64([], p64); if (b.len() != 2 || b[0] != 192 || b[1] != 0) { return 13; }
    var big: i64 = 5000000000; b = leb_i64([], big); if (b.len() != 5) { return 14; }
    return 0;
}
`
