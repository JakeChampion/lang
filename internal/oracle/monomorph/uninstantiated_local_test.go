package monomorph_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// `let v = None;` leaves Option's T uninstantiated. The checker accepts it,
// and the re-check monomorphisation runs over a function with a generic call
// must accept it too: it used to read the checker's stamped `Option` back as
// an annotation and fail with "enum Option takes 1 type argument but was
// given 0", at no source position. Found by TestDifferential_CheckerMutants.
func TestUninstantiatedEnumLocalSurvivesRecheck(t *testing.T) {
	for _, src := range []string{
		"function id[T](x: T): T { return x; }\nfunction main(): i32 { let v = None; return id(0); }\n",
		"enum GO[T] { GNil, GHas(T) }\nfunction id[T](x: T): T { return x; }\nfunction main(): i32 { let v = GNil; return id(0); }\n",
	} {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		info, err := checker.Check(prog)
		if err != nil {
			t.Fatalf("check: %v\n%s", err, src)
		}
		if err := monomorph.Run(prog, info); err != nil {
			t.Errorf("monomorph: %v\n%s", err, src)
		}
	}
}

// A generic enum annotated without type arguments is the bare form in every
// position, and the re-check reads the bare form the first check stamped back
// as the same annotation. It used to fail its arity there, so the program
// checked alone and was refused once anything generic was called (#12804).
func TestBareEnumAnnotationSurvivesRecheck(t *testing.T) {
	const decls = "enum Shape[T] { Circle(i32), Blank }\n" +
		"enum Nest[T] { Wrap(Shape), Hole(T) }\n" +
		"struct Holder { s: Shape }\n" +
		"function id[T](x: T): T { return x; }\n" +
		"function area(s: Shape): i32 { match (s) { Circle(r) => { return r * r; }, Blank => { return 0; } } return 0; }\n" +
		"function make(n: i32): Shape { if (n > 0) { return Circle(n); } return Blank; }\n"
	for _, src := range []string{
		decls + "function main(): i32 { return id(area(Circle(3))); }\n",
		decls + "function main(): i32 { let xs: Shape[] = [Circle(1), make(2)]; let h: Holder = Holder { s: xs[0] }; let n: Nest = Wrap(h.s); return id(area(h.s)); }\n",
		decls + "function main(): i32 { let s: Shape = id(make(2)); return area(s); }\n",
	} {
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		info, err := checker.Check(prog)
		if err != nil {
			t.Fatalf("check: %v\n%s", err, src)
		}
		if err := monomorph.Run(prog, info); err != nil {
			t.Errorf("monomorph: %v\n%s", err, src)
		}
	}
}

// A bare recursive enum keeps its uninhabited parameters beside concrete instances.
func TestBareRecursiveEnumSurvivesOtherInstantiations(t *testing.T) {
	const decls = "enum Tree[T] { Leaf, Node(Tree[T], T, Tree[T]) }\n" +
		"function id[T](x: T): T { return x; }\n"
	for _, tc := range []struct{ name, decl, body string }{
		{"inferred", "", "let c = Leaf;"},
		{"annotated", "", "let c: Tree = Leaf;"},
		{"array", "", "let c: Tree[] = [Leaf];"},
		{"tuple", "", "let c: (Tree, i32) = (Leaf, 0);"},
		{"field", "struct Holder { value: Tree }\n", "let c = Holder { value: Leaf };"},
		{"return", "function empty(): Tree { return Leaf; }\n", "let c = empty();"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + tc.decl + "function main(): i32 { let a: Tree[string] = Leaf; let b: Tree[i32] = Leaf; " + tc.body + " return 0; }\n"
			prog, err := parser.Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatalf("monomorph: %v", err)
			}
		})
	}
}
