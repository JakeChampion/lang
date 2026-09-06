package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #8770: `acc = acc + slice_unchecked(s, lo, hi)` is the hottest shape in
// every line-oriented utility, and unfused it copies the bytes TWICE and
// allocates once — `__str_slice` builds a fresh string, `__fern_str_append`
// memcpys it into the accumulator's slack and frees it.
// `__fern_str_append_range` appends the range straight out of the source.
//
// These pin the LOWERING decision target-independently, on both shapes that
// reach the append (the marked self-append and a chain's consumed
// intermediate) and on every shape that must NOT reach it. The runtime payoff
// and the bounds trap are pinned end-to-end in
// internal/e2e/rc_str_append_range_test.go.

const strAppendRangeSrc = `function build(n: i32, s: string): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < n) {
        out = out + slice_unchecked(s, 0, 3);
        i = i + 1;
    }
    return out;
}
function main(): i32 { return build(3, "abcdef").len(); }`

// TestLowerStrAppendRangeFuses: the fused call replaces BOTH the slice
// materialisation and the append on the two reclaiming ABIs.
func TestLowerStrAppendRangeFuses(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, strAppendRangeSrc, ptrW)
		if got := countFnCallDirect(prog, "build", "__fern_str_append_range"); got != 1 {
			t.Errorf("ptrW=%d: __fern_str_append_range calls in build = %d, want 1", ptrW, got)
		}
		if got := countFnCallDirect(prog, "build", "__str_slice"); got != 0 {
			t.Errorf("ptrW=%d: __str_slice calls in build = %d, want 0 (the slice must not be materialised)", ptrW, got)
		}
		if got := countFnCallDirect(prog, "build", "__fern_str_append"); got != 0 {
			t.Errorf("ptrW=%d: __fern_str_append calls in build = %d, want 0 (the fused helper replaces it)", ptrW, got)
		}
		if got := countOpKind(prog, "build", OpStrConcat); got != 0 {
			t.Errorf("ptrW=%d: OpStrConcat in build = %d, want 0", ptrW, got)
		}
		// The accumulator's dec-on-overwrite stays suppressed: the fused
		// helper consumes the old buffer exactly as __fern_str_append does,
		// so a second release here would free a buffer the slot still holds.
		if got := countStringDecs(prog, "build"); got != 1 {
			t.Errorf("ptrW=%d: string decs in build = %d, want 1 (the accumulator's scope-exit release only)", ptrW, got)
		}
	}
}

// TestLowerStrAppendRangeFusesChainIntermediate: a chain's consumed
// intermediate takes the fused helper too — `out + "|" + slice(...)` grows
// the buffer the leftmost join allocated.
func TestLowerStrAppendRangeFusesChainIntermediate(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	src := `function build(n: i32, s: string): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < n) {
        out = out + "|" + slice_unchecked(s, 1, 4);
        i = i + 1;
    }
    return out;
}
function main(): i32 { return build(3, "abcdef").len(); }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		if got := countFnCallDirect(prog, "build", "__fern_str_append_range"); got != 1 {
			t.Errorf("ptrW=%d: __fern_str_append_range calls = %d, want 1 (the outer join)", ptrW, got)
		}
		if got := countFnCallDirect(prog, "build", "__str_slice"); got != 0 {
			t.Errorf("ptrW=%d: __str_slice calls = %d, want 0", ptrW, got)
		}
		if got := countOpKind(prog, "build", OpStrConcat); got != 1 {
			t.Errorf("ptrW=%d: OpStrConcat = %d, want 1 (the leftmost join, whose left operand is borrowed)", ptrW, got)
		}
	}
}

// TestLowerStrAppendRangeSkipsArm64: arm64 (ptrW==8 + TwoWordOverride) has no
// __fern_str_append and so has none of its range form either — widening the
// predicate without the helper is what turns the suppressed dec-on-overwrite
// into a use-after-free (see strAppendAvailable).
func TestLowerStrAppendRangeSkipsArm64(t *testing.T) {
	prevFree, prevOverride := ast.RcFreeEnabled, ast.TwoWordOverride
	ast.RcFreeEnabled = true
	ast.TwoWordOverride = true
	defer func() { ast.RcFreeEnabled, ast.TwoWordOverride = prevFree, prevOverride }()

	prog := lowerSourceWith(t, strAppendRangeSrc, 8)
	if got := countFnCallDirect(prog, "build", "__fern_str_append_range"); got != 0 {
		t.Errorf("arm64 (two-word, ptrW=8): __fern_str_append_range calls = %d, want 0", got)
	}
	if got := countFnCallDirect(prog, "build", "__str_slice"); got != 1 {
		t.Errorf("arm64: __str_slice calls = %d, want 1 (the slice is still materialised)", got)
	}
	if got := countOpKind(prog, "build", OpStrConcat); got != 1 {
		t.Errorf("arm64: OpStrConcat = %d, want 1", got)
	}
}

// TestLowerStrAppendRangeSkipsCellRead: a `Cell[string]` read is owned only in
// the sense of carrying a retain on top of the reference the SLOT still holds,
// so it must not be grown in place — #8067, silent loss on x86-64 and a freed
// live buffer on arm64 and wasm. The exclusion has to hold for the range form
// as firmly as for the plain append.
func TestLowerStrAppendRangeSkipsCellRead(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	src := `function build(s: string): i32 {
    var c: Cell[string] = cell_new("");
    c.set(c.get() + slice_unchecked(s, 0, 3));
    return c.get().len();
}
function main(): i32 { return build("abcdef"); }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		if got := countFnCallDirect(prog, "build", "__fern_str_append_range"); got != 0 {
			t.Errorf("ptrW=%d: __fern_str_append_range calls = %d, want 0 (a cell read must not be consumed):\n%s", ptrW, got, prog)
		}
		if got := countFnCallDirect(prog, "build", "__str_slice"); got != 1 {
			t.Errorf("ptrW=%d: __str_slice calls = %d, want 1", ptrW, got)
		}
	}
}

// TestLowerStrAppendRangeSkipsBorrowedParam: a borrowed string parameter's
// buffer is the caller's still-live value, so the accumulator is not the
// callee's to grow — and with no in-place append there is nothing to fuse
// into.
func TestLowerStrAppendRangeSkipsBorrowedParam(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	src := `function grow(acc: string, s: string, n: i32): string {
    var i: i32 = 0;
    while (i < n) {
        acc = acc + slice_unchecked(s, 0, 2);
        i = i + 1;
    }
    return acc;
}
function main(): i32 { return grow("a", "bcd", 3).len(); }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		if got := countFnCallDirect(prog, "grow", "__fern_str_append_range"); got != 0 {
			t.Errorf("ptrW=%d: __fern_str_append_range calls in grow = %d, want 0", ptrW, got)
		}
		if got := countFnCallDirect(prog, "grow", "__str_slice"); got != 1 {
			t.Errorf("ptrW=%d: __str_slice calls in grow = %d, want 1", ptrW, got)
		}
	}
}

// TestLowerStrAppendRangeSkipsPlainConcat: a join whose left operand is
// BORROWED allocates its result anyway, so there is no second copy to remove
// and the slice keeps its own buffer.
func TestLowerStrAppendRangeSkipsPlainConcat(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	src := `function j(a: string, s: string): string { return a + slice_unchecked(s, 0, 2); }
function main(): i32 { return j("x", "abc").len(); }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		if got := countFnCallDirect(prog, "j", "__fern_str_append_range"); got != 0 {
			t.Errorf("ptrW=%d: __fern_str_append_range calls in j = %d, want 0", ptrW, got)
		}
		if got := countOpKind(prog, "j", OpStrConcat); got != 1 {
			t.Errorf("ptrW=%d: OpStrConcat in j = %d, want 1", ptrW, got)
		}
	}
}

// TestLowerStrAppendRangeReclaimsOwnedSource: the fused helper BORROWS its
// source, so an owned-temp source (`slice_unchecked(f(x), lo, hi)`) is
// reclaimed by nobody unless the lowering stashes and releases it — the same
// discipline every other borrowing string op keeps. Asserted differentially
// against a borrowed-ident source, which owes no such release.
func TestLowerStrAppendRangeReclaimsOwnedSource(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	src := `function mk(s: string): string { return s + "!"; }
function build(n: i32, s: string): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < n) {
        out = out + slice_unchecked(mk(s), 0, 3);
        i = i + 1;
    }
    return out;
}
function main(): i32 { return build(3, "abcdef").len(); }`

	for _, ptrW := range []int{4, 8} {
		owned := countStringDecs(lowerSourceWith(t, src, ptrW), "build")
		borrowed := countStringDecs(lowerSourceWith(t, strAppendRangeSrc, ptrW), "build")
		if owned-borrowed != 1 {
			t.Errorf("ptrW=%d: string decs in build — owned source %d, borrowed source %d; want the owned one to emit exactly ONE more (the source temp nothing else reclaims)",
				ptrW, owned, borrowed)
		}
	}
}
