package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostArm64WholeProgramMatchesGas(t *testing.T) {
	gas := gnuArm64Oracle(t)
	gcc, runner := x86_64Tooling(t)

	const program = `
function fib(n: i32): i32 {
    if (n < 2) { return n; }
    return fib(n - 1) + fib(n - 2);
}
function double(n: i32): i32 { return n * 2; }
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    let xs: i32[] = [];
    let i: i32 = 0;
    while (i < 8) { xs = xs.append(fib(i)); i = i + 1; }
    let rows: i32[][] = [];
    rows = rows.append(xs);
    let s: string = "";
    let j: i32 = 0;
    while (j < xs.len()) { s = s + "ab"; j = j + 1; }
    let mid: string = slice_unchecked(s, 2, 6) + "";
    if (mid.len() != 4 || mid == "zzzz") { return 2; }
    let f: f64 = 3.0;
    let g: f64 = f * f + 4.0;
    let h: f64 = g / 2.0 - 1.5;
    let big: i64 = 1234567890123;
    let q: i64 = big / 1000;
    let r: i64 = big - q * 1000;
    let doubled: i32 = apply(double, xs[5]);
    if (h < 0.0 || r != 123 || doubled < 0) { return 3; }
    if (g > 12.5 && s.len() == 16 && rows[0].len() == 8) { return xs[7]; }
    return 1;
}
`
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	emitBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "asm_ir_run")
	asm := string(runCaptureEnv(t, runner, emitBin, []byte(program), nil, "-target", "arm64-linux"))
	if len(asm) == 0 {
		t.Fatal("the arm64 emitter produced no asm")
	}
	insns := strings.Count(asm, "\n")
	t.Logf("whole-program asm: %d lines", insns)

	got := assembleSelfHost(t, buildAsmBenchDriver(t, gcc), runner, asm)

	want := arm64Words(gas.linkedText(t, asm))

	if len(got) != len(want) {
		t.Fatalf("word count differs: self-host %d, GNU as %d — one of them dropped or added instructions", len(got), len(want))
	}

	bad := 0
	for i := range want {
		if got[i] == want[i] {
			continue
		}
		if isAddressBearing(want[i]) && isAddressBearing(got[i]) {
			continue
		}
		if bad < 20 {
			t.Errorf("word %d: self-host %08x, GNU as %08x", i, got[i], want[i])
		}
		bad++
	}
	if bad > 0 {
		t.Errorf("%d non-address-bearing words differ out of %d", bad, len(want))
	}
}

func isAddressBearing(w uint32) bool {
	return w&0x9f000000 == 0x90000000 || w&0x7f800000 == 0x11000000
}
