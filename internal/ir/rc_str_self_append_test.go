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

// TestLowerStrSelfAppendOnlyOuterConcat: only the assignment's OWN top-level
// concat is the self-append. A nested concat inside the appended piece
// (`out = out + (a + b)`) is a fresh temp with no accumulator to grow, so it
// stays on OpStrConcat — node identity, not shape matching, is what selects it.
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
