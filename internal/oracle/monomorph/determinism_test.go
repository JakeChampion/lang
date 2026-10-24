package monomorph_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/syntax/printer"
)

var determinismMatrix = map[string]string{
	"single_generic_fn": `
function id[T](x: T): T { return x; }
function main(): i32 {
	let a: i32 = id[i32](7);
	let b: string = id[string]("hi");
	return a + b.len();
}`,

	"many_generic_fns": `
function id[T](x: T): T { return x; }
function pair[A, B](a: A, b: B): A { return a; }
function third[X](x: X): X { return x; }
function main(): i32 {
	let i1: i32 = id[i32](1);
	let s1: string = id[string]("a");
	let i2: i32 = pair[i32, string](2, "b");
	let s2: string = pair[string, i32]("c", 3);
	let i3: i32 = third[i32](4);
	let s3: string = third[string]("d");
	return i1 + s1.len() + i2 + s2.len() + i3 + s3.len();
}`,

	"generic_struct": `
struct Box[T] { val: T }
function main(): i32 {
	let bi: Box[i32] = Box { val: 7 };
	let bs: Box[string] = Box { val: "hi" };
	return bi.val + bs.val.len();
}`,

	"mixed_decls": `
struct Pair[A, B] { a: A, b: B }
function fst[X, Y](p: Pair[X, Y]): X { return p.a; }
struct Plain { v: i32 }
function main(): i32 {
	let p1: Pair[i32, string] = Pair { a: 10, b: "x" };
	let p2: Pair[string, i32] = Pair { a: "y", b: 20 };
	let pl: Plain = Plain { v: 30 };
	return fst[i32, string](p1) + fst[string, i32](p2).len() + pl.v;
}`,

	"transitive_worklist": `
function ga[T](x: T): T { return x; }
function gb[T](x: T): T { return x; }
function gc[T](x: T): T { return x; }
function gd[T](x: T): T { return x; }
function ge[T](x: T): T { return x; }
function outer[T](x: T): T { return ga(gb(gc(gd(ge(x))))); }
function main(): i32 {
	let a: i32 = outer[i32](1);
	let b: string = outer[string]("s");
	let c: boolean = outer[boolean](true);
	let n: i32 = 0;
	if (c) { n = 1; }
	return a + b.len() + n;
}`,
}

func TestMonomorphDeterministic(t *testing.T) {
	for name, src := range determinismMatrix {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			first := mustMonomorphPrint(t, src)
			for i := 0; i < 4; i++ {
				again := mustMonomorphPrint(t, src)
				if again != first {
					t.Fatalf("monomorph not deterministic on run %d: output differs (%d bytes vs %d bytes)",
						i+2, len(first), len(again))
				}
			}
		})
	}
}

func mustMonomorphPrint(t *testing.T, src string) string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	return printer.Print(prog)
}
