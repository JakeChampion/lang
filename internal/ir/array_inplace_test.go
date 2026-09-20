package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// Ownership-aware materialization for a same-shape map (#9733).
//
// The positive is one line; the refusals are the substance. Reuse that fires
// where it should not does not fail to compile — it silently returns a
// different answer, or mutates something a caller can still see. So every
// condition that licenses the rewrite has a test that removes exactly that
// condition and asserts the rewrite stops.

func inPlaceCount(t *testing.T, src string) (*ir.Program, int) {
	t.Helper()
	p := lowerPipelineSrc(t, src)
	return p, ir.MapOwnedArrayInPlace(p, 8)
}

const inPlaceChain = `
function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => dbl(x)); }
function main(): i32 { return run([1 as i64]).len(); }`

const inPlacePrelude = `import "std/array";
function dbl(x: i64): i64 { return x * (2 as i64); }`

func TestOwnedSameShapeMapWritesThroughTheDonor(t *testing.T) {
	p, n := inPlaceCount(t, inPlacePrelude+inPlaceChain)
	if n != 1 {
		t.Fatalf("rewrote %d maps, want 1", n)
	}
	// The combinator call is gone, and the uniqueness guard is there. The
	// guard is what keeps a shared donor immutable, so a rewrite that emitted
	// the loop WITHOUT it would pass every correctness test in this file that
	// uses an unshared donor and be wrong in production.
	if got := callsIn(t, p, "run", "array__"); len(got) != 0 {
		t.Errorf("run still calls the combinator: %v", got)
	}
	guards := 0
	for _, fn := range p.Funcs {
		if fn.Name != "run" {
			continue
		}
		for _, op := range fn.Ops {
			if op.Kind == ir.OpRcIsUnique {
				guards++
			}
		}
	}
	if guards != 1 {
		t.Errorf("run has %d uniqueness guards, want exactly 1 — none means a shared donor "+
			"would be mutated in place; more than one means it is being asked per element, "+
			"which is the cost this rewrite exists to remove", guards)
	}
}

// A borrowed receiver has an owner elsewhere. Nothing licenses writing through
// it, and this is the control the benchmark's `with_borrowed` variant is too.
func TestBorrowedReceiverIsNotRewritten(t *testing.T) {
	_, n := inPlaceCount(t, `import "std/array";
function dbl(x: i64): i64 { return x * (2 as i64); }
function run(xs: i64[]): i64[] { return xs.map((x: i64): i64 => dbl(x)); }
function main(): i32 { return run([1 as i64]).len(); }`)
	if n != 0 {
		t.Fatalf("rewrote %d maps over a BORROWED receiver, want 0", n)
	}
}

// An element function that closes over the donor would read, mid-loop,
// elements the rewrite has already overwritten. `xs.map(x => x + xs[0])` gives
// every element the ORIGINAL xs[0] under value semantics.
func TestCapturingElementFunctionIsNotRewritten(t *testing.T) {
	_, n := inPlaceCount(t, `import "std/array";
function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => x + xs[0]); }
function main(): i32 { return run([1 as i64]).len(); }`)
	if n != 0 {
		t.Fatalf("rewrote %d maps whose element function captures, want 0", n)
	}
}

// A capture of something unrelated is refused too. Conservative: a capture
// this pass cannot prove is not the donor is treated as if it were.
func TestUnrelatedCaptureIsAlsoRefused(t *testing.T) {
	_, n := inPlaceCount(t, `import "std/array";
function run(own xs: i64[], k: i64): i64[] { return xs.map((x: i64): i64 => x + k); }
function main(): i32 { return run([1 as i64], 2 as i64).len(); }`)
	if n != 0 {
		t.Fatalf("rewrote %d maps whose element function captures an unrelated value, want 0", n)
	}
}

// A map that changes the element type needs a buffer of a different size, so
// the donor's is the wrong shape — REUSE-CONTRACT.md's incompatible-shape taint.
func TestTypeChangingMapIsNotRewritten(t *testing.T) {
	_, n := inPlaceCount(t, `import "std/array";
function narrow(x: i64): i32 { return x as i32; }
function run(own xs: i64[]): i32[] { return xs.map((x: i64): i32 => narrow(x)); }
function main(): i32 { return run([1 as i64]).len(); }`)
	if n != 0 {
		t.Fatalf("rewrote %d type-changing maps, want 0", n)
	}
}

// An element function that can reach the world would observe the donor's
// storage being rewritten under it.
func TestEffectfulElementFunctionIsNotRewritten(t *testing.T) {
	_, n := inPlaceCount(t, `import "std/array";
function noisy(x: i64): i64 { print("x"); return x; }
function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => noisy(x)); }
function main(): i32 { return run([1 as i64]).len(); }`)
	if n != 0 {
		t.Fatalf("rewrote %d maps over an effectful element function, want 0", n)
	}
}

func TestInPlaceOffSwitch(t *testing.T) {
	t.Setenv("FERN_NO_ARRAY_INPLACE", "1")
	_, n := inPlaceCount(t, inPlacePrelude+inPlaceChain)
	if n != 0 {
		t.Fatalf("rewrote %d maps with the off switch set, want 0", n)
	}
}

// `fip` reaches the combinator (#9733's third acceptance line). E053 admits
// `xs.map(f)` on an `own` receiver the way it admits constructors — the
// checker cannot tell which `map` R7 writes through its donor — and E068
// counts the ones the planner declines, naming the stage and the rule. The
// positive is what makes the space contract reachable from array code at all;
// the negatives are what keep it a contract rather than a hope.

func TestFipOwnedMapPassesE068(t *testing.T) {
	if _, err := lowerPipelineErr(t, `import "std/array";
fip function dbl(x: i64): i64 { return x * (2 as i64); }
fip function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => dbl(x)); }
function main(): i32 { return run([1 as i64]).len(); }`); err != nil {
		t.Fatalf("a `fip` map R7 writes in place was refused: %v", err)
	}
}

func TestFbipOwnedMapPassesE068(t *testing.T) {
	if _, err := lowerPipelineErr(t, `import "std/array";
fip function dbl(x: i64): i64 { return x * (2 as i64); }
fbip function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => dbl(x)); }
function main(): i32 { return run([1 as i64]).len(); }`); err != nil {
		t.Fatalf("an `fbip` map R7 writes in place was refused: %v", err)
	}
}

// A declined `map` is an E068 that names the call, the function, and the R7
// rule — not a bare count.
func TestFipDeclinedMapIsE068NamingTheRule(t *testing.T) {
	for _, tc := range []struct{ name, src, rule string }{
		{"capture", `import "std/array";
fip function run(own xs: i64[], k: i64): i64[] { return xs.map((x: i64): i64 => x + k); }
function main(): i32 { return run([1 as i64], 2 as i64).len(); }`, "captures"},
		{"type change", `import "std/array";
fip function narrow(x: i64): i32 { return x as i32; }
fip function run(own xs: i64[]): i32[] { return xs.map((x: i64): i32 => narrow(x)); }
function main(): i32 { return run([1 as i64]).len(); }`, "changes the element type"},
		{"field receiver of an own struct", `import "std/array";
struct S { xs: i64[] }
fip function run(own s: S): i64[] { return s.xs.map((x: i64): i64 => x); }
function main(): i32 { return run(S { xs: [1 as i64] }).len(); }`, "not an `own` parameter"},
	} {
		_, err := lowerPipelineErr(t, tc.src)
		if err == nil {
			t.Errorf("%s: a `map` R7 declines was accepted under `fip`", tc.name)
			continue
		}
		msg := err.Error()
		for _, want := range []string{"E068", "`fip` function \"run\"", "`map` at", tc.rule} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: E068 does not say %q:\n%s", tc.name, want, msg)
			}
		}
	}
}

// A graded claim buys the declined `map` exactly as it buys a fresh
// constructor: the site is one un-reused allocation site, whatever it
// allocates at run time. (A capturing element function would be a second
// site — the closure — so the declined shape here changes the element type.)
func TestGradedFipCountsADeclinedMap(t *testing.T) {
	if _, err := lowerPipelineErr(t, `import "std/array";
fip function narrow(x: i64): i32 { return x as i32; }
fip(1) function run(own xs: i64[]): i32[] { return xs.map((x: i64): i32 => narrow(x)); }
function main(): i32 { return run([1 as i64]).len(); }`); err != nil {
		t.Fatalf("fip(1) did not cover one declined map: %v", err)
	}
}

// The off switch disables the verification along with the machinery it
// verifies, the stance verifyFipAllocs already takes for the reuse flags.
func TestFipMapVerificationSkippedWithInPlaceOff(t *testing.T) {
	t.Setenv("FERN_NO_ARRAY_INPLACE", "1")
	if _, err := lowerPipelineErr(t, `import "std/array";
fip function narrow(x: i64): i32 { return x as i32; }
fip function run(own xs: i64[]): i32[] { return xs.map((x: i64): i32 => narrow(x)); }
function main(): i32 { return run([1 as i64]).len(); }`); err != nil {
		t.Fatalf("with the pass off the claim was verified against it anyway: %v", err)
	}
}

// E053 admits `xs.map(f)` on an `own` root by the METHOD NAME, and the IR only
// recognizes std/array's map (TestUserDeclaredArrayMapIsNotTheAlgebra). Between
// the two, a program that never imports std/array and declares its own `map`
// would reach E068 with a call nothing counts, whatever that map allocates. So
// a `map` that is not the algebra's is a site of its own.
func TestFipUserDeclaredMapIsE068(t *testing.T) {
	_, err := lowerPipelineErr(t, `function (xs: i64[]) map(f: (i64) => i64): i64[] { return [f(xs[0])]; }
fip function dbl(x: i64): i64 { return x * (2 as i64); }
fip function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => dbl(x)); }
function main(): i32 { return run([1 as i64]).len(); }`)
	if err == nil {
		t.Fatalf("a user-declared map was accepted under `fip` with nothing verifying it")
	}
	for _, want := range []string{"E068", "`fip` function \"run\"", "`map` that is not std/array's"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("E068 does not say %q:\n%s", want, err)
		}
	}
}
