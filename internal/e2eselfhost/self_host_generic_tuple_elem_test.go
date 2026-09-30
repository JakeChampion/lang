package e2eselfhost

import (
	"strings"
	"testing"
)

// A call in a generic's body whose argument is a tuple element of a generic
// parameter (#10683): `inner(p.1)` with `p: (S, Result[i32, E])`. The mono
// pass inferred nothing for a tuple index, so the inner call stayed the
// template and the instance failed to lower with `inner` undefined; an
// erased, unbounded `S` beside the element must not stop the element's own
// type from binding either. std/http's `respond_with` is this shape.
func TestSelfHostGenericTupleElementArgIRX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	src := `trait Answer { function answer(self: Self): i32; }
struct Boom { n: i32 }
impl Answer for Boom { function answer(self: Self): i32 { return self.n; } }
function inner[E: Answer](r: Result[i32, E]): i32 {
    match (r) { Ok(v) => { return v; }, Err(e) => { return e.answer(); } }
    return 0;
}
function one[E: Answer](p: (i32, Result[i32, E])): i32 {
    return inner(p.1);
}
function two[S, E: Answer](p: (S, Result[i32, E])): (S, i32) {
    return (p.0, inner(p.1));
}
function main(): i32 {
    var p: (i32, Result[i32, Boom]) = (1, Err(Boom { n: 7 }));
    if (one(p) != 7) { return 1; }
    var q: (string, Result[i32, Boom]) = ("s", Err(Boom { n: 9 }));
    var o: (string, i32) = two(q);
    if (o.0 != "s" || o.1 != 9) { return 2; }
    var r: (string, Result[i32, Boom]) = ("t", Ok(3));
    if (two(r).1 != 3) { return 3; }
    return 0;
}
`
	asm, progDir := compileSourceModload(t, runner, driverBin, src)
	if !strings.Contains(asm, ".Lssa_") {
		t.Fatal("the program did not route through the IR path")
	}
	bin := buildBin(t, gcc, progDir, "tuple_elem", asm)
	if err := binCmd(runner, bin).Run(); err != nil {
		t.Fatalf("the program failed: %v (a non-zero exit names the shape whose call did not bind)", err)
	}
}

// The same issue's second shape: a bare `Err(...)` (or `Ok`, `Some`) as the
// argument binding a generic's type parameter. The constructors belong to
// no declared enum, so the variant-argument binding knew nothing of them,
// `E` never bound and the call stayed the template (`outer` undefined).
func TestSelfHostGenericBuiltinVariantArgIRX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)
	src := `trait Answer { function answer(self: Self): i32; }
struct Boom { n: i32 }
impl Answer for Boom { function answer(self: Self): i32 { return self.n; } }
function inner[E: Answer](r: Result[i32, E]): i32 {
    match (r) { Ok(v) => { return v; }, Err(e) => { return e.answer(); } }
    return 0;
}
function okay[T: Answer](r: Result[T, string]): i32 {
    match (r) { Ok(v) => { return v.answer(); }, Err(e) => { return 0 - 1; } }
    return 0;
}
function outer[S, E: Answer](s: S, r: Result[i32, E]): (S, i32) { return (s, inner(r)); }
function first[T: Answer](o: Option[T]): i32 {
    match (o) { Some(v) => { return v.answer(); }, None => { return 0; } }
    return 0;
}
function main(): i32 {
    var o: (string, i32) = outer("s", Err(Boom { n: 7 }));
    if (o.0 != "s" || o.1 != 7) { return 1; }
    if (inner(Err(Boom { n: 5 })) != 5) { return 2; }
    if (first(Some(Boom { n: 4 })) != 4) { return 3; }
    if (okay(Ok(Boom { n: 8 })) != 8) { return 4; }
    return 0;
}
`
	asm, progDir := compileSourceModload(t, runner, driverBin, src)
	if !strings.Contains(asm, ".Lssa_") {
		t.Fatal("the program did not route through the IR path")
	}
	bin := buildBin(t, gcc, progDir, "builtin_variant", asm)
	if err := binCmd(runner, bin).Run(); err != nil {
		t.Fatalf("the program failed: %v (a non-zero exit names the shape whose call did not bind)", err)
	}
}
