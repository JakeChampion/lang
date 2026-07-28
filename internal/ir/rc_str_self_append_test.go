package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #5637 option 3: `out = out + piece` — the stdlib's universal string builder
// (`std/unicode`'s _map_case, `std/utf8`'s encode_all, the JSON / CSV
// encoders) — must lower to __fern_str_append, the in-place-when-unique
// append, instead of OpStrConcat's unconditional allocate-and-copy-both.
//
// These pin the LOWERING decision target-independently; the runtime payoff
// (allocation count, balance) is pinned end-to-end in
// internal/e2e/rc_str_self_append_test.go.

// strSelfAppendSrc is the canonical accumulator: an owned literal-init local
// grown by a borrowed piece and returned.
const strSelfAppendSrc = `function build(n: i32, piece: string): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < n) {
        out = out + piece;
        i = i + 1;
    }
    return out;
}
function main(): i32 { return build(3, "ab").len(); }`

// countFnCallDirect counts direct calls to `helper` inside one function
// (countCallDirect's whole-op-slice sibling).
func countFnCallDirect(p *Program, fnName, helper string) int {
	n := 0
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		n += countCallDirect(fn.Ops, helper)
	}
	return n
}

func countOpKind(p *Program, fnName string, kind OpKind) int {
	n := 0
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		for _, op := range fn.Ops {
			if op.Kind == kind {
				n++
			}
		}
	}
	return n
}

// TestLowerStrSelfAppendEmitsAppendHelper: the self-append lowers to
// __fern_str_append on both reclaiming ABIs — wasm's two-word (ptrW==4) and
// native single-word x86_64 (ptrW==8) — and the plain OpStrConcat is gone
// from that function.
func TestLowerStrSelfAppendEmitsAppendHelper(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, strSelfAppendSrc, ptrW)
		if got := countFnCallDirect(prog, "build", "__fern_str_append"); got != 1 {
			t.Errorf("ptrW=%d: __fern_str_append calls in build = %d, want 1 (the `out = out + piece` self-append did not take the in-place path)", ptrW, got)
		}
		if got := countOpKind(prog, "build", OpStrConcat); got != 0 {
			t.Errorf("ptrW=%d: OpStrConcat in build = %d, want 0 (the self-append should replace it, not sit alongside it)", ptrW, got)
		}
	}
}

// TestLowerStrSelfAppendSuppressesOverwriteDec: __fern_str_append CONSUMES the
// accumulator — in place it keeps the buffer at rc==1, otherwise it runs the
// reclaim itself — so assign() must not also emit its dec-on-overwrite. A
// second release here would free a buffer the slot still holds.
//
// Asserted differentially against a control that is the SAME function with a
// non-self-append RHS (`out = piece + "!"`), so it isolates the overwrite dec
// from the function's other string decs (the accumulator's own scope-exit
// release): the control emits one more.
func TestLowerStrSelfAppendSuppressesOverwriteDec(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	control := `function build(n: i32, piece: string): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < n) {
        out = piece + "!";
        i = i + 1;
    }
    return out;
}
function main(): i32 { return build(3, "ab").len(); }`

	for _, ptrW := range []int{4, 8} {
		self := countStringDecs(lowerSourceWith(t, strSelfAppendSrc, ptrW), "build")
		ctl := countStringDecs(lowerSourceWith(t, control, ptrW), "build")
		if ctl-self != 1 {
			t.Errorf("ptrW=%d: string decs in build — self-append %d, control %d; want the self-append to emit exactly ONE fewer (its overwrite dec suppressed, __fern_str_append having already consumed the old buffer)", ptrW, self, ctl)
		}
	}
}

// TestLowerStrSelfAppendSkipsBorrowedParam: a BORROWED string parameter is not
// the callee's to grow — its buffer is the caller's still-live value, and
// rc==1 does not prove uniqueness under the borrow model (no caller-side inc).
// Such an accumulator keeps the plain concat.
func TestLowerStrSelfAppendSkipsBorrowedParam(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	src := `function grow(acc: string, n: i32): string {
    var i: i32 = 0;
    while (i < n) {
        acc = acc + "x";
        i = i + 1;
    }
    return acc;
}
function main(): i32 { return grow("a", 3).len(); }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		if got := countFnCallDirect(prog, "grow", "__fern_str_append"); got != 0 {
			t.Errorf("ptrW=%d: __fern_str_append calls in grow = %d, want 0 (a borrowed param must not be grown in place)", ptrW, got)
		}
		if got := countOpKind(prog, "grow", OpStrConcat); got != 1 {
			t.Errorf("ptrW=%d: OpStrConcat in grow = %d, want 1", ptrW, got)
		}
	}
}

// TestLowerStrSelfAppendSkipsArm64: arm64 (ptrW==8 + TwoWordOverride) does not
// reclaim heap strings on overwrite — that is the deferred RC-perceus slice 5g
// — so there is no reclaim for the helper to take over and its codegen stays
// byte-identical.
func TestLowerStrSelfAppendSkipsArm64(t *testing.T) {
	prevFree, prevOverride := ast.RcFreeEnabled, ast.TwoWordOverride
	ast.RcFreeEnabled = true
	ast.TwoWordOverride = true
	defer func() { ast.RcFreeEnabled, ast.TwoWordOverride = prevFree, prevOverride }()

	prog := lowerSourceWith(t, strSelfAppendSrc, 8)
	if got := countFnCallDirect(prog, "build", "__fern_str_append"); got != 0 {
		t.Errorf("arm64 (two-word, ptrW=8): __fern_str_append calls = %d, want 0", got)
	}
	if got := countOpKind(prog, "build", OpStrConcat); got != 1 {
		t.Errorf("arm64 (two-word, ptrW=8): OpStrConcat = %d, want 1", got)
	}
}

// TestLowerStrSelfAppendOnlyOuterConcat: the appended PIECE is not itself an
// append target. In `out = out + (a + b)` the inner concat's own left operand
// is `a` — a borrowed ident, not an owned temp — so it has nothing to grow and
// stays on OpStrConcat.
func TestLowerStrSelfAppendOnlyOuterConcat(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	src := `function build(n: i32, a: string, b: string): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < n) {
        out = out + (a + b);
        i = i + 1;
    }
    return out;
}
function main(): i32 { return build(3, "a", "b").len(); }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		if got := countFnCallDirect(prog, "build", "__fern_str_append"); got != 1 {
			t.Errorf("ptrW=%d: __fern_str_append calls = %d, want 1 (the outer concat)", ptrW, got)
		}
		if got := countOpKind(prog, "build", OpStrConcat); got != 1 {
			t.Errorf("ptrW=%d: OpStrConcat = %d, want 1 (the inner `a + b`)", ptrW, got)
		}
	}
}

// TestLowerConcatChainGrowsIntermediate: a CHAIN (`a + b + c + d`) allocates
// once and grows that buffer, instead of allocating and freeing a fresh one
// per join. The leftmost join has a borrowed left operand so it must still
// allocate (OpStrConcat); every join above it inherits an owned temp and
// appends into it.
//
// This is independent of any assignment — the consumed value is the previous
// join's unnameable intermediate, which is exactly the operand the old code
// stashed and __fern_str_dec'd. Nothing else can name it, and the append runs
// after BOTH operands are evaluated, so there is no window in which the
// consumption is observable. Hence zero stashed decs survive.
func TestLowerConcatChainGrowsIntermediate(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	src := `function j(a: string, b: string, c: string): string { return a + b + c + "!"; }
function main(): i32 { return j("a", "b", "c").len(); }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		if got := countOpKind(prog, "j", OpStrConcat); got != 1 {
			t.Errorf("ptrW=%d: OpStrConcat in j = %d, want 1 (only the leftmost join allocates)", ptrW, got)
		}
		if got := countFnCallDirect(prog, "j", "__fern_str_append"); got != 2 {
			t.Errorf("ptrW=%d: __fern_str_append calls in j = %d, want 2 (the two joins above the leftmost)", ptrW, got)
		}
		if got := countStringDecs(prog, "j"); got != 0 {
			t.Errorf("ptrW=%d: string decs in j = %d, want 0 (each intermediate is consumed by the append above it, not copied-then-freed)", ptrW, got)
		}
	}
}

// TestLowerConcatChainSkipsArm64: arm64 has no __fern_str_append helper, so a
// chain keeps the copy-then-__fern_str_dec shape and its codegen is unchanged.
func TestLowerConcatChainSkipsArm64(t *testing.T) {
	prevFree, prevOverride := ast.RcFreeEnabled, ast.TwoWordOverride
	ast.RcFreeEnabled = true
	ast.TwoWordOverride = true
	defer func() { ast.RcFreeEnabled, ast.TwoWordOverride = prevFree, prevOverride }()

	src := `function j(a: string, b: string, c: string): string { return a + b + c + "!"; }
function main(): i32 { return j("a", "b", "c").len(); }`

	prog := lowerSourceWith(t, src, 8)
	if got := countFnCallDirect(prog, "j", "__fern_str_append"); got != 0 {
		t.Errorf("arm64: __fern_str_append calls = %d, want 0", got)
	}
	if got := countOpKind(prog, "j", OpStrConcat); got != 3 {
		t.Errorf("arm64: OpStrConcat = %d, want 3 (one per join)", got)
	}
}

// TestLowerStringOverwriteFrees: the native single-word overwrite releases the
// old buffer through __fern_str_dec, which FREES at rc==1. It used
// __fern_rc_dec, which only decrements — so every intermediate of a string
// accumulator was orphaned (#5637). Both reclaiming ABIs now name the same
// helper; the arm64 exclusion is covered above.
func TestLowerStringOverwriteFrees(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	src := `function build(n: i32, piece: string): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < n) {
        out = piece + "!";
        i = i + 1;
    }
    return out;
}
function main(): i32 { return build(3, "ab").len(); }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		if got := countFnCallDirect(prog, "build", "__fern_str_dec"); got == 0 {
			t.Errorf("ptrW=%d: no __fern_str_dec in build — the string overwrite is not releasing its old buffer through the freeing helper", ptrW)
		}
		if got := countFnCallDirect(prog, "build", "__fern_rc_dec"); got != 0 {
			t.Errorf("ptrW=%d: __fern_rc_dec calls in build = %d, want 0 (rc_dec decrements without freeing — that is the leak)", ptrW, got)
		}
	}
}
