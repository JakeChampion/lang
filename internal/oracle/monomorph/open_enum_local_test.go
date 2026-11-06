package monomorph_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// An unannotated local whose generic enum has no type arguments takes them
// from its first use, so the recheck after monomorphisation sees a whole type.
func TestOpenEnumLocalTakesTheTypeOfItsUse(t *testing.T) {
	const decls = "enum Node[T] { Empty, Leaf(T), Branch(Node[T][]) }\n" +
		"struct Holder[T] { size: i32, root: Node[T] }\n"
	for _, tc := range []struct{ name, src string }{
		{"return", "function find[T](n: Node[T]): Node[T] { let e = Empty; return e; }\n" +
			"function main(): i32 { match (find(Leaf(1))) { Empty => { return 0; }, _ => { return 9; } } }\n"},
		{"array method argument", "function swap[T](kids: Node[T][]): Node[T][] { let e = Empty; return kids.with(0, e); }\n" +
			"function main(): i32 { let n: Node[i32] = Leaf(1); return swap([n]).len(); }\n"},
		{"struct spread field", "function cleared[T](h: Holder[T]): Holder[T] { let e = Empty; return Holder { ...h, root: e }; }\n" +
			"function main(): i32 { let e: Node[i32] = Leaf(2); return cleared(Holder { size: 3, root: e }).size; }\n"},
		{"cell of an option", "function fill(c: Cell[Option[i32]]): void { c.set(Some(4)); }\n" +
			"function main(): i32 { let errors = cell_new(None); fill(errors); if let Some(n) = errors.get() { return n; } return 0; }\n"},
		{"array literal of a generic enum argument", "function count[T](kids: Node[T][]): i32 { return kids.len(); }\n" +
			"function main(): i32 { let n: Node[i32] = Leaf(1); return count([n, n]); }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + tc.src
			prog, err := parser.Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v\n%s", err, src)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatalf("monomorph: %v\n%s", err, src)
			}
		})
	}
}
