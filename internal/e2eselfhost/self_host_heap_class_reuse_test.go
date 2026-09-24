package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// The self-host heap's small tier classed every block by its exact size in
// 8-byte words, so a buffer that grows a little per step asked for a new class
// every step and never found the block it had just freed: 40,000 one-byte
// appends to a string bumped ~800 MB of arena for an 80 KB result, with every
// free accounted for (#8793). Above 2 KiB the classes are now native's
// 3-significant-bit capacities and a block is bumped at its class's capacity,
// so each freed buffer serves the next, slightly larger request.
//
// Each program measures its own arena with __heap_bump_bytes() and exits 1 when
// the growth passes 16 MiB, so the observable is the reuse itself. The second
// shape threads the accumulator through a call, #8785's row, which the
// self-host OOM-killed at 100k appends.
var heapClassReuseCases = []struct {
	name, src string
	want      int // the result's length % 7 + 10
}{
	{"bare_local", `function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var out: string = "";
    var i: i32 = 0;
    while (i < 40000) { out = out + "x"; i = i + 1; }
    if (__heap_bump_bytes() - b0 > 16777216) { return 1; }
    return out.len() % 7 + 10;
}`, 40000%7 + 10},
	{"through_a_call", `function put(a: string, s: string): string { return a + s; }
function main(): i32 {
    var b0: i64 = __heap_bump_bytes();
    var acc: string = "";
    var i: i32 = 0;
    while (i < 20000) { acc = put(acc, "12345678"); i = i + 1; }
    if (__heap_bump_bytes() - b0 > 16777216) { return 1; }
    return acc.len() % 7 + 10;
}`, 160000%7 + 10},
}

func TestSelfHostHeapClassReuseX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range heapClassReuseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			stderr, exit := hevRun(t, runner, buildBin(t, gcc, dir, "hcr_"+tc.name, asm))
			if want := tc.want; exit != want {
				t.Fatalf("exit = %d, want %d (1 = the arena grew past 16 MiB)\n%s", exit, want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostHeapClassReuseArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")
	for _, tc := range heapClassReuseCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCaptureEnv(t, x86runner, driverBin, []byte(tc.src),
				[]string{"PATH=/usr/bin:/bin", "FERN_LEAKCHECK=1"}, "-target", "arm64-linux"))
			cmd := runArm64Bin(qemu, buildBinArm64(t, arm64gcc, dir, "hcr_"+tc.name, asm))
			var eb strings.Builder
			cmd.Stderr = &eb
			_ = cmd.Run()
			if want, code := tc.want, cmd.ProcessState.ExitCode(); code != want {
				t.Fatalf("exit = %d, want %d (1 = the arena grew past 16 MiB)\n%s", code, want, eb.String())
			}
			assertBalancedCensus(t, eb.String())
		})
	}
}

func TestSelfHostHeapClassReuseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm heap class reuse")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasmdriver")
	for _, tc := range heapClassReuseCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := wasmLcCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			stderr, exit := wasmLcRun(t, dir, "hcr_"+tc.name, wat)
			if want := tc.want; exit != want {
				t.Fatalf("exit = %d, want %d (1 = the arena grew past 16 MiB)\n%s", exit, want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
