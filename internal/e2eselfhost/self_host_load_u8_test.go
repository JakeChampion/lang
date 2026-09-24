package e2eselfhost

import (
	"os/exec"
	"testing"
)

// __load_u8 lowers to raw_load8, which the wasm backend had no instruction
// selection for, so any program reading a byte through it refused to compile
// on the self-host's wasm target while native wasm ran it. The word stored is
// 0xC8030201: the high byte checks the load zero-extends.
const loadU8Src = `function main(): i32 {
    var p: usize = __alloc(16);
    __store_i32(p, 0 - 939326975);
    if (__load_u8(p) != 1) { return 1; }
    if (__load_u8(p + 1) != 2) { return 2; }
    if (__load_u8(p + 2) != 3) { return 3; }
    if (__load_u8(p + 3) != 200) { return 4; }
    return 42;
}
`

func TestSelfHostLoadU8X86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	asm := hevCompile(t, runner, driverBin, loadU8Src, nil)
	if stderr, exit := hevRun(t, runner, buildBin(t, gcc, dir, "load_u8", asm)); exit != 42 {
		t.Fatalf("exit = %d, want 42 (1-4 = the byte at that offset + 1 was wrong)\n%s", exit, stderr)
	}
}

func TestSelfHostLoadU8Wasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm __load_u8")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasmdriver")
	wat := wasmLcCompile(t, runner, driverBin, loadU8Src, nil)
	if stderr, exit := wasmLcRun(t, dir, "load_u8", wat); exit != 42 {
		t.Fatalf("exit = %d, want 42 (1-4 = the byte at that offset + 1 was wrong)\n%s", exit, stderr)
	}
}
