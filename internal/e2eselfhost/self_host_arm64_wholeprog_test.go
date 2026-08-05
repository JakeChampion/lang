package e2eselfhost

import (
	"encoding/binary"
	"strings"
	"testing"

	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
)

// TestSelfHostArm64WholeProgramMatchesNative is
// TestSelfHostArm64AsmEncodingMatchesNative scaled from a hand-written snippet
// to a WHOLE PROGRAM: it takes the arm64 emitter's own GAS text — runtime and
// all — and requires the self-host in-process assembler and
// internal/native/arm64 to agree on every word of it.
//
// # Why this is worth having on top of the snippet
//
// The snippet covers ~110 forms somebody thought to list. This covers every
// instruction the emitter actually produces, in the proportions it produces
// them, including the runtime helpers nobody writes rows for. #6062 wanted it
// specifically: it recorded four FP instructions appearing one time fewer in a
// linked binary than in the emitted asm, and a count cannot distinguish "an
// instruction was dropped" from "the counting method is off by one" — an
// alignment can, by naming the first index where the two streams differ.
//
// The blocker was that the oracle rejected the emitter's numeric local labels
// (`1:`), so it could not read whole-program output at all. #6075 fixed that,
// and this test is the payoff. On its first run it found cset assembling as its
// 64-bit sibling 73 times — behaviourally invisible, so no execution test could
// ever have caught it, and not a form anyone had added a snippet row for.
//
// # Why adrp / add-immediate are excluded
//
// Those two carry ADDRESSES, and the two assemblers lay out their images
// differently (different data-segment vaddr, different rodata placement), so
// they legitimately differ. Everything else — every ALU op, every load/store,
// every branch displacement, every literal-pool word — must match exactly.
// Measured on a stdlib-using program through the full CLI (117,465 asm lines):
// 809 divergences, all 809 adrp or add-immediate, zero in any other class
// across 118,420 words.
func TestSelfHostArm64WholeProgramMatchesNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	// Import-free on purpose: asm_ir_run has no module loader. That bounds what
	// this reaches — the emitter DCEs the runtime down to what the program
	// touches, so this is ~4k asm lines rather than the ~117k a stdlib-using
	// program produces through the full CLI. So it is deliberately written to
	// touch a spread of runtime: refcounted arrays, string concat and slicing,
	// f64 arithmetic and comparison, i64, division/remainder, a closure, and
	// nested indexing. Widen the program before reaching for a heavier driver.
	const program = `
function fib(n: i32): i32 {
    if (n < 2) { return n; }
    return fib(n - 1) + fib(n - 2);
}
function double(n: i32): i32 { return n * 2; }
function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
function main(): i32 {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < 8) { xs = xs.append(fib(i)); i = i + 1; }
    var rows: i32[][] = [];
    rows = rows.append(xs);
    var s: string = "";
    var j: i32 = 0;
    while (j < xs.len()) { s = s + "ab"; j = j + 1; }
    var mid: string = s[2:6] + "";
    if (mid.len() != 4 || mid == "zzzz") { return 2; }
    var f: f64 = 3.0;
    var g: f64 = f * f + 4.0;
    var h: f64 = g / 2.0 - 1.5;
    var big: i64 = 1234567890123;
    var q: i64 = big / 1000;
    var r: i64 = big - q * 1000;
    var doubled: i32 = apply(double, xs[5]);
    if (h < 0.0 || r != 123 || doubled < 0) { return 3; }
    if (g > 12.5 && s.len() == 16 && rows[0].len() == 8) { return xs[7]; }
    return 1;
}
`
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	emitBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "asm_ir_run")
	asm := string(runCaptureEnv(t, runner, emitBin, []byte(program), nil, "-target", "arm64"))
	if len(asm) == 0 {
		t.Fatal("the arm64 emitter produced no asm")
	}
	insns := strings.Count(asm, "\n")
	t.Logf("whole-program asm: %d lines", insns)

	got := assembleSelfHost(t, buildAsmBenchDriver(t, gcc), runner, asm)

	text, _, err := nativearm64.AssembleProgram(asm, nativeelf.TextVAddr)
	if err != nil {
		// A refusal here is a finding in its own right: the oracle has to be
		// able to read whatever the emitter writes, or this gate silently
		// covers nothing. #6075 closed four such gaps (numeric local labels,
		// movn, FP stur/ldur, and '$' in a symbol name — every lifted-lambda
		// wrapper is named `…$wrap0`).
		t.Fatalf("the native assembler rejected the emitter's own output: %v", err)
	}
	var want []uint32
	for i := 0; i+4 <= len(text); i += 4 {
		want = append(want, binary.LittleEndian.Uint32(text[i:]))
	}

	if len(got) != len(want) {
		t.Fatalf("word count differs: self-host %d, native %d — one of them dropped or added instructions", len(got), len(want))
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
			t.Errorf("word %d: self-host %08x, native %08x", i, got[i], want[i])
		}
		bad++
	}
	if bad > 0 {
		t.Errorf("%d non-address-bearing words differ out of %d", bad, len(want))
	}
}

// isAddressBearing reports whether a word is an adrp or an add-immediate, the
// two forms whose operands depend on where the assembler placed its sections.
func isAddressBearing(w uint32) bool {
	return w&0x9f000000 == 0x90000000 || w&0x7f800000 == 0x11000000
}
