package monomorph_test

// Determinism guard for generic monomorphisation.
//
// monomorph.Run is the stage that drives generics: for each
// `function id[T](x: T): T` (and `struct Box[T] { … }`) it clones
// per-instantiation bodies with the type parameters substituted,
// appending the new decls to p.Funcs / p.Structs. The instantiation
// set is built from observed call sites and accumulated into a Go
// map (`instantiations[instKey]`), so an "iterate the map → append
// clones to p.Funcs" loop would normally pick up Go's randomised
// map order.
//
// The implementation sortKeys the per-call instKeys before cloning
// (the `// 1.` and `// 3.` blocks in monomorph.go), and — since
// #6077 — also sorts the instantiations discovered DURING cloning
// before appending them to the worklist. This guard locks that in:
// a refactor that removes either sort, or introduces another
// map-driven decision (struct-then-fn ordering, alias dedup,
// nested-generic resolution), fails loudly here instead of as a
// flaky downstream determinism test.
//
// The second sort was missing until #6077 and this file did not
// catch it: every case here instantiated only from call sites in
// `main`, so the initial sort covered them all. `transitive_worklist`
// is the case that reaches the other range — see its note below.
//
// printer.Print serialises the post-monomorph Program (including
// every cloned __<name>_<typeargs> function), so comparing across
// repeated runs witnesses the full instantiation order + body
// content.

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/printer"
)

// determinismMatrix favours the paths where a sort regression would
// bite first: multiple generic functions, multiple type-arg
// instantiations per function, mixed scalar / array / string args,
// and a generic struct sharing the same type parameter set.
var determinismMatrix = map[string]string{
	// single_generic_fn: one generic fn, two instantiations.
	"single_generic_fn": `
function id[T](x: T): T { return x; }
function main(): i32 {
	var a: i32 = id[i32](7);
	var b: string = id[string]("hi");
	return a + b.len();
}`,

	// many_generic_fns: three generic fns + multiple inst pairs.
	// The instantiations map has 6 entries; their clone order
	// must be deterministic regardless of Go map iteration.
	"many_generic_fns": `
function id[T](x: T): T { return x; }
function pair[A, B](a: A, b: B): A { return a; }
function third[X](x: X): X { return x; }
function main(): i32 {
	var i1: i32 = id[i32](1);
	var s1: string = id[string]("a");
	var i2: i32 = pair[i32, string](2, "b");
	var s2: string = pair[string, i32]("c", 3);
	var i3: i32 = third[i32](4);
	var s3: string = third[string]("d");
	return i1 + s1.len() + i2 + s2.len() + i3 + s3.len();
}`,

	// generic_struct: a parametric struct + literal construction
	// at two type args. structInsts is the map-keyed clone source.
	"generic_struct": `
struct Box[T] { val: T }
function main(): i32 {
	var bi: Box[i32] = Box { val: 7 };
	var bs: Box[string] = Box { val: "hi" };
	return bi.val + bs.val.len();
}`,

	// mixed_decls: generic fn + generic struct + concrete decls
	// interleaved; locks that the merge of cloned + original
	// decls stays in the same order across runs.
	"mixed_decls": `
struct Pair[A, B] { a: A, b: B }
function fst[X, Y](p: Pair[X, Y]): X { return p.a; }
struct Plain { v: i32 }
function main(): i32 {
	var p1: Pair[i32, string] = Pair { a: 10, b: "x" };
	var p2: Pair[string, i32] = Pair { a: "y", b: 20 };
	var pl: Plain = Plain { v: 30 };
	return fst[i32, string](p1) + fst[string, i32](p2).len() + pl.v;
}`,

	// transitive_worklist: instantiations discovered while CLONING, not at the
	// original call sites. `outer[T]`'s body calls five other generics, so
	// `ga[i32]`..`ge[i32]` only become known once `outer[i32]` has been cloned.
	// They land on the worklist through a SECOND map range (monomorph.go's
	// "append any newly-seen keys"), which the `// 1.` sort ahead of the loop
	// does not cover — it sorts the initial keys only.
	//
	// FIVE callees, not one, and that is the point: a clone step that discovers a
	// single fresh key ranges a one-element map, which has no order to get wrong.
	// A three-deep chain (outer -> mid -> inner) therefore passes even with the
	// sort removed. The bug needs several keys entering the worklist together.
	//
	// Every case above instantiates solely from call sites in `main`, so all of
	// them were already sorted before cloning began and none witnessed that
	// second range. #6077: an unsorted append there reordered emitted functions
	// run to run, which reached the binary (two hashes over 12 compiles of
	// examples/tests/set_test.fern) and silently defeats every byte-identity gate.
	// Several type args per level, so a stable order has something to be stable
	// about.
	"transitive_worklist": `
function ga[T](x: T): T { return x; }
function gb[T](x: T): T { return x; }
function gc[T](x: T): T { return x; }
function gd[T](x: T): T { return x; }
function ge[T](x: T): T { return x; }
function outer[T](x: T): T { return ga(gb(gc(gd(ge(x))))); }
function main(): i32 {
	var a: i32 = outer[i32](1);
	var b: string = outer[string]("s");
	var c: boolean = outer[boolean](true);
	var n: i32 = 0;
	if (c) { n = 1; }
	return a + b.len() + n;
}`,
}

// TestMonomorphDeterministic runs monomorph.Run on each program
// several times and asserts every run produces a printer-byte-
// identical Program. A failure means generic-clone ordering depends
// on Go map iteration — which would propagate through IR / codegen
// and break the byte-identical self-host fixed-point gates and
// reproducible builds.
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

// mustMonomorphPrint runs parser → checker → monomorph and returns
// the post-monomorph Program rendered via printer.Print. Fails the
// test on any error.
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
