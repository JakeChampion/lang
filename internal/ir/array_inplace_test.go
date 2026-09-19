package ir_test

import (
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
