package parser

// Determinism guard for parsing.
//
// parser.Parse is the front door of every pipeline (codegen, interp,
// LSP, formatter). The determinism guards already landed downstream —
// IR lowering, the three backends, the interpreter, the checker
// diagnostics — all assume the AST they receive is itself stable for a
// given source. This pins that assumption at the source: parsing the
// same text twice must yield the same tree.
//
// The witness is the formatted AST (printer.Format) rather than
// reflect.DeepEqual on the raw *ast.Program: Format renders decl
// order, every statement/expression, and types into text, so two
// trees that format identically are structurally identical for every
// purpose downstream consumers care about, and the comparison is
// position-insensitive without having to walk and zero Position
// fields by hand.
//
// The parser is hand-written recursive descent with no map iteration
// in its hot path, so this is expected to hold trivially today — the
// value is locking it so a future change that (say) collects decls
// through a map, or parallelises a sub-parse, fails here instead of as
// a flaky downstream determinism test or a non-reproducible build.

import (
	"testing"

	"github.com/jakechampion/lang/internal/printer"
)

// parseMatrix is a spread of self-contained programs covering the
// breadth of the grammar: decls (struct/enum/union/const/generic),
// statements (control flow, match, defer, destructure), and
// expressions (closures, f-strings, pipes, method chains, tuples).
var parseMatrix = map[string]string{
	"arithmetic_and_calls": `
function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 {
	var x: i32 = add(6, 7) * 2 - (3 + 1) / 2;
	return x;
}`,

	"control_flow": `
function classify(n: i32): i32 {
	if (n > 0) { return 1; } else if (n < 0) { return -1; }
	var i: i32 = 0;
	while (i < n) { i = i + 1; }
	for (var j: i32 = 0; j < 10; j = j + 1) { if (j == 5) { break; } }
	return 0;
}`,

	"structs_and_methods": `
struct Point { x: i32, y: i32 }
function (p: Point) mag(): i32 { return p.x * p.x + p.y * p.y; }
function main(): i32 {
	var p: Point = Point { x: 3, y: 4 };
	return p.mag();
}`,

	"enums_and_match": `
enum Shape { Circle(i32), Rect(i32, i32) }
function area(s: Shape): i32 {
	match (s) {
		Circle(r) => { return r * r; },
		Rect(w, h) => { return w * h; }
	}
	return 0;
}`,

	"generics": `
function id[T](x: T): T { return x; }
struct Box[T] { val: T }
function unbox[T](b: Box[T]): T { return b.val; }
function main(): i32 { return id(42); }`,

	"closures_and_fstrings": `
function adder(n: i32): (i32) => i32 {
	return function (x: i32): i32 { return x + n; };
}
function main(): void {
	var f: (i32) => i32 = adder(10);
	var r: i32 = f(5);
	print(f"result is {r}");
}`,

	"tuples_and_destructure": `
function divmod(a: i32, b: i32): (i32, i32) { return (a / b, a % b); }
function main(): i32 {
	var (q, r) = divmod(17, 5);
	return q + r;
}`,

	"union_and_const": `
const LIMIT: i32 = 100;
struct Leaf { v: i32 }
struct Node { left: Tree, right: Tree }
type Tree = Leaf | Node;
function depth(t: Tree): i32 {
	match (t) {
		Leaf(l) => { return 1; },
		Node(n) => { return 1 + depth(n.left); }
	}
	return 0;
}`,
}

// TestParseDeterministic parses each program several times and asserts
// the formatted AST is byte-identical to the first parse. A failure
// means parsing has become nondeterministic — which would undermine
// every downstream determinism guard and reproducible builds, since
// they all start from the parser's output.
func TestParseDeterministic(t *testing.T) {
	for name, src := range parseMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			first, err := Parse(src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			want := printer.Format(first)
			for i := 0; i < 8; i++ {
				again, err := Parse(src)
				if err != nil {
					t.Fatalf("parse on run %d: %v", i+2, err)
				}
				if got := printer.Format(again); got != want {
					t.Fatalf("parse not deterministic on run %d:\n--- first ---\n%s\n--- run %d ---\n%s",
						i+2, want, i+2, got)
				}
			}
		})
	}
}
