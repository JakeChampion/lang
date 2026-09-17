package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #5637 option 3 — end-to-end benefit and safety of the in-place string
// self-append (`s = s + piece` → __fern_str_append). The LOWERING decision is
// pinned target-independently in internal/ir/rc_str_self_append_test.go; these
// pin what the emitted runtime actually does.

// strSelfAppendLoopSrc grows a string 2 bytes at a time to 4000 bytes, the
// shape `std/unicode`'s _map_case and `std/utf8`'s encode_all use per code
// point. Returns 0 on the expected length so a miscompile shows as a non-zero
// exit rather than a silently wrong allocation count.
const strSelfAppendLoopSrc = `function main(): i32 {
    var s: string = "";
    var i: i32 = 0;
    while (i < 2000) {
        s = s + "ab";
        i = i + 1;
    }
    if (s.len() != 4000) { return 1; }
    return 0;
}`

// TestX86_64StrSelfAppendAllocsBounded pins the allocation collapse through the
// leak detector, which counts every __fern_alloc / __fern_free (see
// leakcheck_test.go). Measured on this exact program:
//
//	before -> allocs=1997 frees=1    live_bytes=4031936
//	after  -> allocs= 250 frees=250  live_bytes=0
//
// Two things changed. The appends now mostly land in the buffer's existing
// 16-byte size-class slack, so one allocation covers ~8 of them instead of one
// each; and the fallback path releases the old buffer through __fern_str_dec
// (which frees at rc==1) where the suppressed dec-on-overwrite used the native
// __fern_rc_dec, which decrements but never frees — so the accumulator's
// intermediates were leaked outright, 4 MB of them here.
//
// The assertions are the invariants, not the exact numbers: one allocation per
// class step rather than per append (allocs well under the iteration count),
// and a balanced heap at exit (allocs == frees, live_bytes == 0) — the latter
// is what catches an over-release just as firmly as a leak.
func TestX86_64StrSelfAppendAllocsBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, strSelfAppendLoopSrc)
	if code != 0 {
		t.Fatalf("string self-append loop exited %d (want 0 — the accumulated length was wrong); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	// 2000 appends over ~4000 bytes cross ~250 16-byte classes. Anything near
	// the iteration count means the in-place path never fired.
	if allocs > 400 {
		t.Errorf("allocs = %d for 2000 appends, want <= 400 (~one per 16-byte class step); the in-place append is not firing", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced after the append loop: allocs=%d frees=%d live_bytes=%d, want allocs==frees and live_bytes==0", allocs, frees, live)
	}
}

// strSelfAppendCorrectnessSrc exercises the shapes the in-place path must get
// right, all against values the interpreter agrees on:
//
//   - plain growth across the SSO -> heap boundary and several class steps,
//   - an ALIASED accumulator: `alias` holds the buffer while `s` grows, so the
//     alias inc puts it at rc>1 and the append MUST copy instead of mutating
//     the value `alias` still reads,
//   - self-concat (`e = e + e`), where source and destination are one buffer,
//   - appending the empty string (a zero-byte copy that must not disturb the
//     length or the class test).
const strSelfAppendCorrectnessSrc = `function build(n: i32, piece: string): string {
    var s: string = "";
    var i: i32 = 0;
    while (i < n) {
        s = s + piece;
        i = i + 1;
    }
    return s;
}

function main(): i32 {
    print(build(5, "ab"));
    print(build(40, "xyz"));
    print("[" + build(3, "") + "]");
    var d: string = "";
    var i: i32 = 0;
    while (i < 6) {
        var alias: string = d;
        d = d + "q";
        print(alias + "|" + d);
        i = i + 1;
    }
    var e: string = "ab";
    var k: i32 = 0;
    while (k < 5) {
        e = e + e;
        k = k + 1;
    }
    print(e);
    return 0;
}`

// The trailing newline of the final print is omitted: runWasmCapturingStdout
// trims it, and the native comparison adds it back.
const strSelfAppendWant = `ababababab
xyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyzxyz
[]
|q
q|qq
qq|qqq
qqq|qqqq
qqqq|qqqqq
qqqqq|qqqqqq
abababababababababababababababababababababababababababababababab`

// TestWASMStrSelfAppendCorrect runs the shapes above on the two-word (wasm)
// ABI, where the in-place path returns (a_data, la+lb) with the buffer's rc
// left at 1.
func TestWASMStrSelfAppendCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	if got := runWasmCapturingStdout(t, strSelfAppendCorrectnessSrc); got != strSelfAppendWant {
		t.Errorf("wasm string self-append output =\n%q\nwant\n%q", got, strSelfAppendWant)
	}
}

// TestX86_64StrSelfAppendCorrect is the native single-word sibling, where the
// in-place path also restamps the length prefix at [data-4] and the trailing
// NUL. Runs under the leak detector so the same program doubles as an
// over-release probe: a buffer freed while still aliased would show up as
// frees > allocs (or as corrupted output).
func TestX86_64StrSelfAppendCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, strSelfAppendCorrectnessSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != strSelfAppendWant+"\n" {
		t.Errorf("x86-64 string self-append output =\n%q\nwant\n%q", stdout, strSelfAppendWant+"\n")
	}
	if allocs, frees, _ := parseLeakCheckLine(t, stderr); frees > allocs {
		t.Errorf("frees=%d > allocs=%d — the append over-released a buffer", frees, allocs)
	}
}

// TestArm64StrSelfAppendCorrect is the two-word NATIVE sibling: the pair is
// carried in registers, the in-place path returns (a_data, la+lb), and
// [data-4] — the payload size __fern_str_dec frees at — is left alone.
func TestArm64StrSelfAppendCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckArm64(t, strSelfAppendCorrectnessSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != strSelfAppendWant+"\n" {
		t.Errorf("arm64 string self-append output =\n%q\nwant\n%q", stdout, strSelfAppendWant+"\n")
	}
	if allocs, frees, _ := parseLeakCheckLine(t, stderr); frees > allocs {
		t.Errorf("frees=%d > allocs=%d — the append over-released a buffer", frees, allocs)
	}
}

// strConcatChainSrc builds a string through a CHAIN of joins per iteration —
// `hdr_block = hdr_block + name + ": " + value + "\r\n"` is the shape, straight
// from std/http's response assembly. Every join grows the accumulator: the
// leftmost appends into its own slack, and each join above grows the buffer the
// one below returned.
const strConcatChainSrc = `function main(): i32 {
    var out: string = "";
    var i: i32 = 0;
    while (i < 500) {
        out = out + "a" + "bb" + "ccc";
        i = i + 1;
    }
    if (out.len() != 3000) { return 1; }
    return 0;
}`

// TestX86_64StrConcatChainAllocsBounded pins #5637's follow-up on this exact
// program: allocs=129 frees=129 live_bytes=0.
//
// Allocations are bounded because no join copies. Each join above the leftmost
// grows the previous join's intermediate instead of allocating a fresh buffer
// and freeing it; the leftmost grows the accumulator itself, so the only
// allocations left are the size-class steps 3000 bytes of growth crosses. An
// unfused leftmost join allocated and copied the whole accumulator every
// iteration and cost 627 here.
//
// live_bytes is zero because the accumulator's reclaim routes through
// __fern_str_dec (which frees at rc==1) rather than __fern_rc_dec (which
// decrements to zero and stops).
func TestX86_64StrConcatChainAllocsBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, strConcatChainSrc)
	if code != 0 {
		t.Fatalf("chained concat loop exited %d (want 0 — the accumulated length was wrong); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	// 3000 bytes of growth crosses ~129 size classes. Anything near the
	// iteration count means a join is copying rather than growing.
	if allocs > 250 {
		t.Errorf("allocs = %d for 500 three-join iterations, want <= 250 (~one per size-class step); a join is allocating and copying instead of growing", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced after the chain loop: allocs=%d frees=%d live_bytes=%d, want allocs==frees and live_bytes==0 (the accumulator's overwrite must FREE, not just decrement)", allocs, frees, live)
	}
}

// strConcatChainCorrectnessSrc covers the shapes the chain path must get right:
// a plain multi-join expression, a chained self-append in a loop, an aliased
// accumulator across a chained append (must copy, not mutate), and a chain
// whose operands are string SLICES — the other isOwnedStringTemp shape, so the
// consumed intermediate is a slice buffer rather than a concat buffer.
const strConcatChainCorrectnessSrc = `function join3(a: string, b: string, c: string): string { return a + b + c; }

function main(): i32 {
    print(join3("aa", "bb", "cc"));
    print("<" + "x" + "|" + "yy" + ">");
    var out: string = "";
    var i: i32 = 0;
    while (i < 8) {
        out = out + "[" + "*" + "]";
        i = i + 1;
    }
    print(out);
    var d: string = "";
    var k: i32 = 0;
    while (k < 5) {
        var alias: string = d;
        d = d + "q" + "r";
        print(alias + "/" + d);
        k = k + 1;
    }
    var s: string = "abcdefghij";
    print(slice_unchecked(s, 0, 3) + slice_unchecked(s, 3, 6) + slice_unchecked(s, 6, 9) + "!");
    return 0;
}`

const strConcatChainWant = `aabbcc
<x|yy>
[*][*][*][*][*][*][*][*]
/qr
qr/qrqr
qrqr/qrqrqr
qrqrqr/qrqrqrqr
qrqrqrqr/qrqrqrqrqr
abcdefghi!`

func TestWASMStrConcatChainCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	if got := runWasmCapturingStdout(t, strConcatChainCorrectnessSrc); got != strConcatChainWant {
		t.Errorf("wasm chained concat output =\n%q\nwant\n%q", got, strConcatChainWant)
	}
}

func TestX86_64StrConcatChainCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, strConcatChainCorrectnessSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != strConcatChainWant+"\n" {
		t.Errorf("x86-64 chained concat output =\n%q\nwant\n%q", stdout, strConcatChainWant+"\n")
	}
	if allocs, frees, _ := parseLeakCheckLine(t, stderr); frees > allocs {
		t.Errorf("frees=%d > allocs=%d — the chain over-released an intermediate", frees, allocs)
	}
}

// strAppendClassBoundarySrc grows an accumulator two bytes at a time from
// nothing to 4200 bytes, so the in-place grow crosses out of the small tier
// at 2048 and through several large-tier classes, and then verifies every
// byte by POSITION — an alternating a/b fill, so a copy that landed at the
// wrong offset or a length prefix restamped wrong is visible where a uniform
// fill would not be.
const strAppendClassBoundarySrc = `function main(): i32 {
    var s: string = "";
    var i: i32 = 0;
    while (i < 2100) {
        s = s + "ab";
        i = i + 1;
    }
    if (s.len() != 4200) { return 1; }
    var j: i32 = 0;
    while (j < 4200) {
        var want: i32 = 97;
        if (j % 2 == 1) { want = 98; }
        if ((s[j] as i32) != want) { return 2; }
        j = j + 1;
    }
    if (slice_unchecked(s, 2046, 2050) != "abab") { return 3; }
    if (slice_unchecked(s, 4196, 4200) != "abab") { return 4; }
    return 0;
}`

// The in-place grow's guard asks whether the grown request still lands in the
// block the old one reserved. That is ONE capacity computation — `req_new <=
// cap(req_old)` — where it used to be two compared for equality; the algebra
// is proved in internal/codegen/x86_64/sizeclass_cap_test.go, and this is the
// emitted code agreeing with it.
//
// The allocation COUNT is the assertion, because the count is the guard's
// decision sequence: 2100 appends against 132 allocations means the guard said
// "grow in place" 1968 times and "allocate" 132 times, at exactly the lengths
// the size classes fall on. A predicate that differed anywhere across the
// 0..4200 span — including at the 2048 tier change — moves this number. A
// change that legitimately moves it (a different rounding, a different header)
// should re-bank it rather than loosen it.
func TestX86_64StrAppendClassBoundary(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, strAppendClassBoundarySrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0 (1 = wrong length, 2 = a byte at the wrong position, 3/4 = a boundary slice); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 132 {
		t.Errorf("allocs = %d for 2100 appends across the 2048 tier change, want 132 — the in-place guard fires at different lengths than the size classes fall on", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

// TestArm64StrAppendClassBoundary is the two-word NATIVE sibling. Two more
// allocations than x86-64: a two-word string is __fern_alloc_rc1(len) where
// the single-word one asks for len+1 (its trailing NUL), so the 16-byte class
// steps fall two lengths apart across the same 0..4200 span.
func TestArm64StrAppendClassBoundary(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckArm64(t, strAppendClassBoundarySrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0 (1 = wrong length, 2 = a byte at the wrong position, 3/4 = a boundary slice); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 134 {
		t.Errorf("allocs = %d for 2100 appends across the 2048 tier change, want 134 — the in-place guard fires at different lengths than the size classes fall on", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

// TestWASMStrAppendClassBoundary is the two-word sibling, whose guard is
// emitFreelistBin rather than emitSizeClassCap and had the same doubled
// computation. One more allocation than the natives: wasm has no inline
// small-string form, so the first append heap-allocates where x86-64 packs.
func TestWASMStrAppendClassBoundary(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	_, stderr, code := runLeakCheckWasm(t, strAppendClassBoundarySrc, false)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != 133 {
		t.Errorf("allocs = %d for 2100 appends across the 2048 tier change, want 133", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

// strChainAliasSrc reads the accumulator AGAIN from the right of a chained
// append. Fusing the leftmost join would consume `out`'s buffer before that
// third operand is evaluated, so the read would see a buffer already grown in
// place (a doubled answer) or, on the copy path, one already freed.
//
// Doubling per iteration makes a fused compile visible rather than subtle:
// each step must be `prev + "-" + prev`.
const strChainAliasSrc = `function main(): i32 {
    var out: string = "ab";
    var i: i32 = 0;
    while (i < 5) {
        out = out + "-" + out;
        i = i + 1;
    }
    print(out);
    if (out.len() != 95) { return 1; }
    return 0;
}`

const strChainAliasWant = "ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab\n"

// TestX86_64StrSelfAppendChainAliasIsNotFused is the runtime half of
// internal/ir's TestLowerStrSelfAppendChainStopsAtAReadOfTheAccumulator: the
// answer stays right, and the heap stays balanced, when the spine declines to
// fuse. Under the leak detector so an over-release shows as frees > allocs.
func TestX86_64StrSelfAppendChainAliasIsNotFused(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, strChainAliasSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0 (the accumulated length was wrong — the chain fused over a read of its own accumulator); stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != strChainAliasWant {
		t.Errorf("x86-64 aliased chain output =\n%q\nwant\n%q", stdout, strChainAliasWant)
	}
	if allocs, frees, live := parseLeakCheckLine(t, stderr); frees > allocs || live != 0 {
		t.Errorf("heap after the aliased chain: allocs=%d frees=%d live_bytes=%d, want frees <= allocs and live_bytes==0", allocs, frees, live)
	}
}

// TestArm64StrSelfAppendChainAliasIsNotFused is the two-word NATIVE sibling.
func TestArm64StrSelfAppendChainAliasIsNotFused(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckArm64(t, strChainAliasSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != strChainAliasWant {
		t.Errorf("arm64 aliased chain output =\n%q\nwant\n%q", stdout, strChainAliasWant)
	}
	if allocs, frees, live := parseLeakCheckLine(t, stderr); frees > allocs || live != 0 {
		t.Errorf("heap after the aliased chain: allocs=%d frees=%d live_bytes=%d, want frees <= allocs and live_bytes==0", allocs, frees, live)
	}
}

// TestWASMStrSelfAppendChainAliasIsNotFused is the wasm sibling.
func TestWASMStrSelfAppendChainAliasIsNotFused(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	if got := runWasmCapturingStdout(t, strChainAliasSrc); got != strings.TrimSuffix(strChainAliasWant, "\n") {
		t.Errorf("wasm aliased chain output =\n%q\nwant\n%q", got, strChainAliasWant)
	}
}
