package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

// TestSettleIntSkipsStringConcatArg pins that the numeric-settle walk leaves a
// string-concat `+` alone. settleInt's Call case settles every argument bound to
// a bare type parameter, so an `Eq`-bounded generic returning i32 whose result
// is compared against a literal — `find(xs, "a" + "b") != 1` — reached the
// concat node with an i32 hint and stamped IntWidth onto it. The next check pass
// (monomorph's re-check) then typed the concat as an integer add and reported
// `argument 2: expected string, got i32` as an internal "compiler bug".
//
// Reachable from plain code since `.index_of()` / `.contains()` became
// element-generic (#6801): `xs.index_of("a" + "b") != 1` is exactly this shape.
func TestSettleIntSkipsStringConcatArg(t *testing.T) {
	const src = `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for string { function eq(self: Self, other: Self): boolean { return self == other; } }
function find[T: Eq](xs: T[], target: T): i32 {
    var i: i32 = 0;
    while (i < xs.len()) {
        if (xs[i].eq(target)) { return i; }
        i = i + 1;
    }
    return 0 - 1;
}
function main(): i32 {
    var xs: string[] = ["a", "ab"];
    if (find(xs, "a" + "b") != 1) { return 1; }
    return 0;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	var concat *ast.Binary
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if b, ok := n.(*ast.Binary); ok && b.Op == "+" && b.IsStringConcat {
			concat = b
		}
		return true
	})
	if concat == nil {
		t.Fatal(`no string-concat "+" node found: the argument was not typed as a concat at all`)
	}
	if concat.IntWidth != 0 {
		t.Errorf("IntWidth = %d, want 0 — the numeric settle stamped an integer width onto a string concat", concat.IntWidth)
	}
	if concat.IsFloat || concat.FloatWidth != 0 {
		t.Errorf("IsFloat = %v, FloatWidth = %d, want false / 0", concat.IsFloat, concat.FloatWidth)
	}
}

// A float destination reaches the same argument walk through settleFloat, so it
// needs the same guard: `var r: f64 = pick(cond, "a" + "b", "c").len() as f64`
// is contrived, but a `T: Eq` generic returning a float is not.
func TestSettleFloatSkipsStringConcatArg(t *testing.T) {
	const src = `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for string { function eq(self: Self, other: Self): boolean { return self == other; } }
function score[T: Eq](xs: T[], target: T): f64 {
    if (xs[0].eq(target)) { return 1.0; }
    return 0.0;
}
function main(): i32 {
    var xs: string[] = ["ab"];
    var r: f64 = score(xs, "a" + "b");
    if (r > 0.5) { return 0; }
    return 1;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	var concat *ast.Binary
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if b, ok := n.(*ast.Binary); ok && b.Op == "+" && b.IsStringConcat {
			concat = b
		}
		return true
	})
	if concat == nil {
		t.Fatal(`no string-concat "+" node found`)
	}
	if concat.FloatWidth != 0 {
		t.Errorf("FloatWidth = %d, want 0 — the float settle stamped a width onto a string concat", concat.FloatWidth)
	}
}
