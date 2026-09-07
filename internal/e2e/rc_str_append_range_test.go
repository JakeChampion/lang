package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #8770 — end-to-end payoff and safety of `__fern_str_append_range`, the
// fusion of `acc = acc + slice_unchecked(s, lo, hi)`. The LOWERING decision is
// pinned target-independently in internal/ir/rc_str_append_range_test.go;
// these pin what the emitted runtime does.

// strAppendRangeCorrectnessSrc covers every shape the fused path must get
// right, all against values the interpreter and the unfused arm64 lowering
// agree on:
//
//   - an EMPTY range (the shared-sentinel case __str_slice short-circuits),
//   - a range of 7 bytes or fewer (the inline/SSO case __str_slice packs
//     without allocating), and one above it (its heap case),
//   - an ALIASED accumulator, which puts the buffer at rc>1 so the append
//     must copy instead of mutating the value `alias` still reads,
//   - a source that IS the accumulator (`e = e + slice_unchecked(e, 0, 2)`),
//     where the read range and the write slack are one buffer apart,
//   - a chain, whose left operand is the previous join's intermediate,
//   - growth from nothing to 4800 bytes, so the in-place grow crosses out of
//     the small tier at 2048 and through several large-tier classes; the
//     slices either side of 2048 and at the very end catch a grow that
//     silently stopped copying.
const strAppendRangeCorrectnessSrc = `function build(s: string, lo: i32, hi: i32, n: i32): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < n) {
        out = out + slice_unchecked(s, lo, hi);
        i = i + 1;
    }
    return out;
}

function sum_bytes(s: string): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < s.len()) {
        t = t + (s[i] as i32);
        i = i + 1;
    }
    return t;
}

function main(): i32 {
    var s: string = "abcdefghijklmnop";
    print(build(s, 0, 3, 5));
    print("[" + build(s, 2, 2, 4) + "]");
    print(build(s, 0, 7, 3));
    print(build(s, 0, 12, 2));
    var d: string = "";
    var k: i32 = 0;
    while (k < 5) {
        var alias: string = d;
        d = d + slice_unchecked(s, 0, 2);
        print(alias + "|" + d);
        k = k + 1;
    }
    var e: string = "xy";
    var j: i32 = 0;
    while (j < 4) {
        e = e + slice_unchecked(e, 0, 2);
        j = j + 1;
    }
    print(e);
    var c: string = "";
    var m: i32 = 0;
    while (m < 3) {
        c = c + "<" + slice_unchecked(s, 1, 4) + ">";
        m = m + 1;
    }
    print(c);
    var big: string = build(s, 0, 4, 1200);
    if (big.len() != 4800) { return 1; }
    if (sum_bytes(big) != 472800) { return 2; }
    if (slice_unchecked(big, 4796, 4800) != "abcd") { return 3; }
    if (slice_unchecked(big, 2044, 2052) != "abcdabcd") { return 4; }
    return 0;
}`

// The trailing newline of the final print is omitted: runWasmCapturingStdout
// trims it, and the native comparison adds it back.
const strAppendRangeWant = `abcabcabcabcabc
[]
abcdefgabcdefgabcdefg
abcdefghijklabcdefghijkl
|ab
ab|abab
abab|ababab
ababab|abababab
abababab|ababababab
xyxyxyxyxy
<bcd><bcd><bcd>`

// TestX86_64StrAppendRangeCorrect runs the shapes above on the native
// single-word ABI under the leak detector, so the same program doubles as an
// over-release probe: a buffer freed while still aliased shows up as
// frees > allocs, or as corrupted output.
func TestX86_64StrAppendRangeCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, strAppendRangeCorrectnessSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0 (a non-zero code names the failed length / checksum / boundary-slice check); stderr=%q", code, stderr)
	}
	if stdout != strAppendRangeWant+"\n" {
		t.Errorf("x86-64 fused range append output =\n%q\nwant\n%q", stdout, strAppendRangeWant+"\n")
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d, want allocs==frees and live_bytes==0", allocs, frees, live)
	}
}

// TestWASMStrAppendRangeCorrect is the two-word (wasm) sibling, where the
// in-place path returns (a_data, la+lb) with the buffer's rc left at 1.
func TestWASMStrAppendRangeCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	if got := runWasmCapturingStdout(t, strAppendRangeCorrectnessSrc); got != strAppendRangeWant {
		t.Errorf("wasm fused range append output =\n%q\nwant\n%q", got, strAppendRangeWant)
	}
}

// TestWASMStrAppendRangeBalanced runs the same program through the wasm leak
// census: the fused helper's fallback releases BOTH the consumed accumulator
// and the range it materialised, and only that census sees the second one.
func TestWASMStrAppendRangeBalanced(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	_, stderr, code := runLeakCheckWasm(t, strAppendRangeCorrectnessSrc, false)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d, want allocs==frees and live_bytes==0", allocs, frees, live)
	}
}

// TestArm64StrAppendRangeCorrect: arm64 has no in-place append and so no
// fused range form — it keeps __str_slice + OpStrConcat. Same answers, which
// is the property the exclusion has to preserve.
func TestArm64StrAppendRangeCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckArm64(t, strAppendRangeCorrectnessSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != strAppendRangeWant+"\n" {
		t.Errorf("arm64 range append output =\n%q\nwant\n%q", stdout, strAppendRangeWant+"\n")
	}
}

// strAppendRangeAllocSrc appends 12-byte ranges — above the 7-byte inline
// threshold, so the unfused pair allocates a slice buffer per append on top
// of the accumulator's own class steps.
const strAppendRangeAllocSrc = `function main(): i32 {
    var s: string = "abcdefghijklmnop";
    var out: string = "";
    var i: i32 = 0;
    while (i < 500) {
        out = out + slice_unchecked(s, 0, 12);
        i = i + 1;
    }
    if (out.len() != 6000) { return 1; }
    return 0;
}`

// TestX86_64StrAppendRangeAllocsCollapse pins the allocation half of the
// payoff on this exact program:
//
//	before -> allocs=633 frees=633 live_bytes=0
//	after  -> allocs=266 frees=266 live_bytes=0
//
// The 500 slice buffers are gone; what remains is the accumulator's own
// size-class steps, which the fusion does not change. The assertion is the
// invariant rather than the exact number: no allocation per append.
func TestX86_64StrAppendRangeAllocsCollapse(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, strAppendRangeAllocSrc)
	if code != 0 {
		t.Fatalf("range-append loop exited %d (want 0 — the accumulated length was wrong); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs > 400 {
		t.Errorf("allocs = %d for 500 range appends, want <= 400; the slice is still being materialised", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d, want allocs==frees and live_bytes==0", allocs, frees, live)
	}
}

func TestWASMStrAppendRangeAllocsCollapse(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	_, stderr, code := runLeakCheckWasm(t, strAppendRangeAllocSrc, false)
	if code != 0 {
		t.Fatalf("range-append loop exited %d, want 0; stderr=%q", code, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs > 400 {
		t.Errorf("allocs = %d for 500 range appends, want <= 400; the slice is still being materialised", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d, want allocs==frees and live_bytes==0", allocs, frees, live)
	}
}

// The trap contract: the fused helper must reject exactly the ranges
// __str_slice rejects — a < 0 || b > s.len() || a > b — and it has to do so on
// the FAST path too, where no __str_slice call is left to check them. Each
// case runs several valid appends first, so the accumulator is a uniquely-held
// heap buffer by the time the bad range arrives; without the up-front check
// the in-place grow reads out of bounds and the program returns a length.
// Bounds are carried in vars so constfold cannot pre-judge them.
var strAppendRangeTrapCases = []struct{ name, src string }{
	{"high_past_end", `function main(): i32 {
    var s: string = "hello";
    var out: string = "";
    var hi: i32 = 3;
    var i: i32 = 0;
    while (i < 10) {
        out = out + slice_unchecked(s, 0, hi);
        i = i + 1;
        if (i == 5) { hi = 6; }
    }
    return out.len();
}`},
	{"negative_low", `function main(): i32 {
    var s: string = "hello";
    var out: string = "";
    var lo: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        out = out + slice_unchecked(s, lo, 3);
        i = i + 1;
        if (i == 5) { lo = 0 - 1; }
    }
    return out.len();
}`},
	{"inverted", `function main(): i32 {
    var s: string = "hello";
    var out: string = "";
    var lo: i32 = 0;
    var i: i32 = 0;
    while (i < 10) {
        out = out + slice_unchecked(s, lo, 3);
        i = i + 1;
        if (i == 5) { lo = 4; }
    }
    return out.len();
}`},
}

func TestStrAppendRangeTrap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping trap e2e in -short mode")
	}
	for _, c := range strAppendRangeTrapCases {
		t.Run(c.name, func(t *testing.T) {
			// The fused helper is what has to trap, so pin that it is the
			// helper under test: a program that fell back to __str_slice
			// would trap for the wrong reason and pass.
			if asm := compileToX86Asm(t, c.src); !strings.Contains(asm, "call __fern_str_append_range") {
				t.Fatalf("the trap case does not lower to the fused append — it would be testing __str_slice's own check")
			}
			t.Run("interp", func(t *testing.T) {
				if got := runInterpExit(t, c.src); got == 0 {
					t.Errorf("interp did not trap (exit 0)\nsrc:\n%s", c.src)
				}
			})
			t.Run("x86_64", func(t *testing.T) {
				out, got := compileAndRunX86_64(t, c.src)
				if got != 134 {
					t.Errorf("x86-64 exit = %d, want 134 (the slice abort); output:\n%s", got, out)
				}
			})
			t.Run("arm64", func(t *testing.T) {
				out, got := compileAndRunArm64(t, c.src)
				if got != 134 {
					t.Errorf("arm64 exit = %d, want 134; output:\n%s", got, out)
				}
			})
			t.Run("wasm", func(t *testing.T) {
				// wasm's `unreachable` surfaces as wasmtime's own non-zero
				// exit, not 134 — assert the trap, not its spelling.
				_, _, code := runComponent(t, buildNumComponent(t, c.src), runOpts{})
				if code == 0 {
					t.Errorf("wasm did not trap (exit 0)")
				}
			})
		})
	}
}
