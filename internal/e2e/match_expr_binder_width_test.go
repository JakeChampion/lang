package e2e

import "testing"

// A match EXPRESSION sizes its result slot from the arm bodies, before any
// arm is lowered — and the IR resolves an identifier's type by NAME against
// the function's locals and params, which a pattern binder is not. An arm
// body that is JUST a binder therefore answered "no type", the slot fell
// back to i32, and a value wider than one word went through it: on wasm the
// module fails validation outright, which is the only checker in the project
// that reports this class at all (see CompileAndRunWasmbinMain's note on
// #8456). The natives absorb the same mis-sized slot silently.
//
// conformance/cases/match_expr_binder_body pins the two spellings every
// implementation can compile. These are the rest: a TUPLE arm and a payload
// SUB-PATTERN, which the self-host types i32 whatever the binder is (#8657),
// so they cannot live in the corpus until that closes.
//
// Every arm body here is a bare binder on purpose. One arm with a derivable
// body type — a literal, a local — sizes the slot correctly and hides the
// defect, which is why it survived this long.

func TestMatchExprTupleBinderBodySizesResultSlot(t *testing.T) {
	src := `function main(): i32 {
	var t: (string, i32) = ("elem", 4);
	var got: string = match (t) {
		(q, n) => q
	};
	if (got != "elem") { return 13; }
	return 7;
}`
	if got := compileAndRunWasmbinMain(t, src); got != 7 {
		t.Errorf("exit = %d, want 7", got)
	}
}

func TestMatchExprNestedTupleBinderBodySizesResultSlot(t *testing.T) {
	src := `function main(): i32 {
	var t: (i32, (string, i32)) = (1, ("elem", 4));
	var got: string = match (t) {
		(a, (q, n)) => q
	};
	if (got != "elem") { return 13; }
	return 7;
}`
	if got := compileAndRunWasmbinMain(t, src); got != 7 {
		t.Errorf("exit = %d, want 7", got)
	}
}

// The same defect at 64-bit width rather than two words: an i64 arm body
// stored into an i32 slot is "type mismatch: expected i32, found i64".
func TestMatchExprTupleBinderBodyKeepsI64Width(t *testing.T) {
	src := `function main(): i32 {
	var t: (i64, i32) = (5000000000i64, 4);
	var got: i64 = match (t) {
		(q, n) => q
	};
	if (got != 5000000000i64) { return 13; }
	return 7;
}`
	if got := compileAndRunWasmbinMain(t, src); got != 7 {
		t.Errorf("exit = %d, want 7", got)
	}
}

func TestMatchExprPayloadSubPatternBinderBodySizesResultSlot(t *testing.T) {
	src := `enum Res { Ok1(string), Err1(string) }
enum Wrap { Box(Res) }
function main(): i32 {
	var w: Wrap = Box(Ok1("elem"));
	var got: string = match (w) {
		Box(Ok1(q)) => q,
		Box(Err1(r)) => r
	};
	if (got != "elem") { return 13; }
	return 7;
}`
	if got := compileAndRunWasmbinMain(t, src); got != 7 {
		t.Errorf("exit = %d, want 7", got)
	}
}

// The `@` whole-value binder names the SCRUTINEE, so its type is the one
// position the arm's own binding lists cannot supply. It is a CONTROL: it
// holds on both sides of the fix, because a pointer-shaped enum handle fits
// the one-word default slot the mis-typing produced. It is here so a future
// change that starts sizing this position from the wrong list is caught.
func TestMatchExprAtBinderBodySizesResultSlot(t *testing.T) {
	src := `enum One { Only(string) }
function take(o: One): string { match (o) { Only(v) => { return v; } } }
function main(): i32 {
	var o: One = Only("elem");
	var got: One = match (o) {
		w @ Only(v) => w
	};
	if (take(got) != "elem") { return 13; }
	return 7;
}`
	if got := compileAndRunWasmbinMain(t, src); got != 7 {
		t.Errorf("exit = %d, want 7", got)
	}
}
