package ir

// Determinism guard for IR lowering.
//
// LowerWith feeds every backend: the native emitters (arm64, x86-64)
// and the wasm builder all consume the *Program it produces, and the
// self-host fixed-point gates assert byte-identical output across
// compiler stages. Any nondeterminism introduced here propagates to
// *all* backends at once — strictly worse than a per-backend wobble,
// and harder to localize because it surfaces as simultaneous
// multi-backend "mismatches".
//
// Lowering walks several Go maps along the way (drop-function
// generation keyed by mangled type name, string/func interning, the
// checker Info tables it reads), and Go map iteration order is
// randomized. So both the *contents* of the emitted ops and the
// *order* of generated functions (e.g. the synthesised __drop_* /
// __closure_drop_* helpers appended to p.Funcs) are places ordering
// nondeterminism could leak in. Program.String() renders function
// order + every op + types, so comparing it across repeated lowerings
// of the same source is a faithful witness of both.
//
// This sits one layer earlier than the backend determinism tests
// (internal/codegen/{arm64,x86_64,wasmbin}) and the interpreter
// guard (internal/interp): if lowering is deterministic here, those
// downstream guards are testing only their own emit step, not noise
// inherited from the IR.

import "testing"

// determinismMatrix favours the lowering paths that thread through Go
// maps: generated drop functions for heap-allocated composites
// (structs, enums, tuples, arrays-of-pointers), closures (env layout
// + __closure_drop synthesis), maps (key/val kind tags), and multiple
// user functions (function ordering in p.Funcs). Each program is
// self-contained (defines main, no imports).
var determinismMatrix = map[string]string{
	"arithmetic": `
function main(): i32 {
	var x: i32 = 6;
	return x * 7 + (7 - x) / 2;
}`,

	"struct_drop": `
struct Pair { a: string, b: string }
function main(): i32 {
	var p: Pair = Pair { a: "hello", b: "world" };
	return p.a.len() + p.b.len();
}`,

	"enum_payload": `
enum Shape { Circle(i32), Rect(i32, i32) }
function area(s: Shape): i32 {
	match (s) {
		Circle(r) => { return r * r; },
		Rect(w, h) => { return w * h; }
	}
	return 0;
}
function main(): i32 { return area(Circle(3)) + area(Rect(2, 5)); }`,

	"closures": `
function adder(n: i32): (i32) => i32 {
	return function (x: i32): i32 { return x + n; };
}
function main(): i32 {
	var f: (i32) => i32 = adder(10);
	var g: (i32) => i32 = adder(20);
	return f(1) + g(2);
}`,

	// closure_drop_thunks: FIVE distinct closures that each capture an
	// rc-tracked value, so five __closure_drop_<name> thunks are synthesised
	// and appended to out.Funcs from a range over the closureCaps map.
	//
	// The `closures` case above does not reach this: both of its closure
	// values come from the SAME lambda, so closureCaps has one entry and a
	// one-element range has no order to get wrong. #6077 needed several
	// eligible thunks at once — the emitted function order then flipped run
	// to run and reached the binary (examples/tests/result_assertions_test
	// produced six distinct hashes over fifteen compiles).
	//
	// Not caught by TestLowerDeterministicOverFixtureCorpus either: that
	// globs conformance/cases, and the programs that exhibited
	// this were examples/tests/* — std/test users, which build many
	// capturing closures. A corpus guard is only as wide as its corpus.
	"closure_drop_thunks": `
function mk_a(s: string): () => i32 { return function (): i32 { return s.len(); }; }
function mk_b(s: string): () => i32 { return function (): i32 { return s.len() + 1; }; }
function mk_c(s: string): () => i32 { return function (): i32 { return s.len() + 2; }; }
function mk_d(s: string): () => i32 { return function (): i32 { return s.len() + 3; }; }
function mk_e(s: string): () => i32 { return function (): i32 { return s.len() + 4; }; }
function main(): i32 {
	var a: () => i32 = mk_a("v");
	var b: () => i32 = mk_b("w");
	var c: () => i32 = mk_c("x");
	var d: () => i32 = mk_d("y");
	var e: () => i32 = mk_e("z");
	return a() + b() + c() + d() + e();
}`,

	"map_ops": `
function main(): i32 {
	var m: Map[string, i32] = Map { "a": 1, "b": 2, "c": 3 };
	m = m.insert("d", 4);
	return m.get_or("b", 0) + m.get_or("d", 0);
}`,

	"many_funcs": `
function a(x: i32): i32 { return x + 1; }
function b(x: i32): i32 { return a(x) * 2; }
function c(x: i32): i32 { return b(x) - 3; }
function d(x: i32): i32 { return c(x) + a(x); }
function main(): i32 { return d(10); }`,

	"strings_interned": `
function pick(n: i32): string {
	if (n == 0) { return "alpha"; }
	if (n == 1) { return "beta"; }
	return "alpha";
}
function main(): i32 { return pick(0).len() + pick(1).len(); }`,
}

// TestLowerDeterministic lowers each program several times and asserts
// the rendered IR (function order, ops, types via Program.String()) is
// identical to the first lowering, on both pointer widths (4 = wasm32,
// 8 = native). A failure means lowering nondeterminism (most likely Go
// map iteration order in drop-function generation or interning) has
// leaked into the IR — which would propagate to every backend at once.
func TestLowerDeterministic(t *testing.T) {
	for _, ptrW := range []int{4, 8} {
		ptrW := ptrW
		t.Run(ptrWName(ptrW), func(t *testing.T) {
			for name, src := range determinismMatrix {
				name, src := name, src
				t.Run(name, func(t *testing.T) {
					want := lowerSourceWith(t, src, ptrW).String()
					for i := 0; i < 8; i++ {
						got := lowerSourceWith(t, src, ptrW).String()
						if got != want {
							t.Fatalf("IR not deterministic on lowering %d (ptrW=%d):\nfirst:\n%s\ngot:\n%s",
								i+2, ptrW, want, got)
						}
					}
				})
			}
		})
	}
}

func ptrWName(ptrW int) string {
	if ptrW == 8 {
		return "ptrW8_native"
	}
	return "ptrW4_wasm32"
}
