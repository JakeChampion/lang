package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// Producer-consumer fusion of std/array pipelines (#9731).
//
// Two things are worth locking here and they pull in opposite directions. A
// positive says the chain became one loop: the combinator calls are gone and
// nothing allocates an intermediate. A negative says the pass declined a shape
// it does not understand, and those carry the same weight — an eagerly fusing
// pass that mishandles a type-changing `map` miscompiles silently, where one
// that declines merely leaves the old code in place.
//
// The runtime half (that the fused loop computes the same answer, on every
// backend, including the empty and single-element inputs a `reduce` seed makes
// special) lives in internal/e2e/array_fusion_test.go. Neither half is
// sufficient alone: this one cannot see a wrong answer, and that one cannot
// see a pipeline that quietly failed to fuse.

// fuseSrc lowers `src` and runs the fusion pass, returning the program and how
// many pipelines fused.
func fuseSrc(t *testing.T, src string) (*ir.Program, int) {
	t.Helper()
	p := lowerPipelineSrc(t, src)
	// The anti-vacuity check below reads the recognizer, and fusion removes
	// what it would have seen — so the stage counts are taken first.
	recognized = map[string]int{}
	for _, pl := range ir.RecognizeArrayPipelines(p) {
		if len(pl.Stages) > recognized[pl.Func] {
			recognized[pl.Func] = len(pl.Stages)
		}
	}
	return p, ir.FuseArrayPipelines(p, 8)
}

// recognized holds the longest chain the recognizer saw per function, as of
// the last fuseSrc call.
var recognized map[string]int

// mustRecognize fails unless the recogniser saw a chain of at least
// `stages` stages in `fn`. Every negative below asserts this first: without
// it a refusal test passes just as well when recognition never produced a
// pipeline at all, which proves nothing about the fusion pass.
func mustRecognize(t *testing.T, p *ir.Program, fn string, stages int) {
	t.Helper()
	best := recognized[fn]
	if best < stages {
		t.Fatalf("recogniser saw a %d-stage chain in %s, want at least %d — "+
			"this refusal test would pass vacuously:\n%s", best, fn, stages, ir.FormatArrayPipelines(p))
	}
}

// callsIn returns the direct callees of `fn` whose name contains `needle`.
func callsIn(t *testing.T, p *ir.Program, fn, needle string) []string {
	t.Helper()
	var out []string
	for _, f := range p.Funcs {
		if f.Name != fn {
			continue
		}
		for _, op := range f.Ops {
			if op.Kind == ir.OpCallDirect && strings.Contains(op.Str, needle) {
				out = append(out, op.Str)
			}
		}
	}
	return out
}

func TestFuseMapFoldRemovesTheCombinatorCalls(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => x + (1 as i64))
           .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return run([1 as i64]) as i32; }`)
	if n != 1 {
		t.Fatalf("fused %d pipelines, want 1", n)
	}
	if got := callsIn(t, p, "run", "array__"); len(got) != 0 {
		t.Errorf("run still calls std/array combinators: %v", got)
	}
}

func TestFuseMapMapReduceRemovesTheCombinatorCalls(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  var out: Option[i64] = xs
    .map((x: i64): i64 => x + (1 as i64))
    .map((x: i64): i64 => x * (2 as i64))
    .reduce((a: i64, b: i64): i64 => a + b);
  match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return run([1 as i64]) as i32; }`)
	if n != 1 {
		t.Fatalf("fused %d pipelines, want 1", n)
	}
	if got := callsIn(t, p, "run", "array__"); len(got) != 0 {
		t.Errorf("run still calls std/array combinators: %v", got)
	}
}

func TestFuseFilterMapFold(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  return xs.filter((x: i64): boolean => x > (0 as i64))
           .map((x: i64): i64 => x + (1 as i64))
           .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return run([1 as i64]) as i32; }`)
	if n != 1 {
		t.Fatalf("fused %d pipelines, want 1", n)
	}
	if got := callsIn(t, p, "run", "array__"); len(got) != 0 {
		t.Errorf("run still calls std/array combinators: %v", got)
	}
}

// A lone combinator is not a pipeline: `xs.map(f)` has to materialize its
// result because that result IS the value, so there is nothing to fuse away.
func TestRefuseSingleStage(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function run(xs: i64[]): i64[] {
  return xs.map((x: i64): i64 => x + (1 as i64));
}
function main(): i32 { return run([1 as i64]).len(); }`)
	mustRecognize(t, p, "run", 1)
	if n != 0 {
		t.Fatalf("fused %d pipelines, want 0", n)
	}
}

// A `map` may change the element type, and the fused loop carries the element
// in ONE slot. Fusing `(i64) => i32` into that slot would truncate on a
// register backend and fail validation on wasm, so the chain is declined.
func TestRefuseTypeChangingMap(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function run(xs: i64[]): i32 {
  return xs.map((x: i64): i32 => x as i32)
           .fold(0 as i32, (a: i32, b: i32): i32 => a + b);
}
function main(): i32 { return run([1 as i64]); }`)
	mustRecognize(t, p, "run", 2)
	if n != 0 {
		t.Fatalf("fused %d pipelines, want 0", n)
	}
}

// Only 8-byte elements are emitted: the index helper is chosen by width and
// each width wants its own coverage, so the rest are declined rather than
// guessed at.
func TestRefuseNarrowElements(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function run(xs: i32[]): i32 {
  return xs.map((x: i32): i32 => x + 1)
           .fold(0, (a: i32, b: i32): i32 => a + b);
}
function main(): i32 { return run([1]); }`)
	mustRecognize(t, p, "run", 2)
	if n != 0 {
		t.Fatalf("fused %d pipelines, want 0", n)
	}
}

// `fold`'s seed runs once, before the loop. A seed that is an arbitrary
// expression would have to be hoisted out of the chain with its own
// evaluation-order argument, so only a constant is taken.
func TestRefuseNonConstantFoldSeed(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function run(xs: i64[], s: i64): i64 {
  return xs.map((x: i64): i64 => x + (1 as i64))
           .fold(s, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return run([1 as i64], 3 as i64) as i32; }`)
	mustRecognize(t, p, "run", 2)
	if n != 0 {
		t.Fatalf("fused %d pipelines, want 0", n)
	}
}

// A sink the recogniser knows but the fusion vocabulary does not. `scan` is a
// prefix stage: its output is an array, one element per input, so it is a
// reduction in shape only and its `finish` is not an accumulator.
func TestRefuseUnknownSink(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => x + (1 as i64))
           .scan(0 as i64, (a: i64, b: i64): i64 => a + b)
           .len() as i64;
}
function main(): i32 { return run([1 as i64]) as i32; }`)
	mustRecognize(t, p, "run", 2)
	if n != 0 {
		t.Fatalf("fused %d pipelines, want 0", n)
	}
}

// The same verb in the middle of a chain. A stage is only fused when its
// `step` is in the fragment vocabulary, and `scan`'s carries an accumulator
// across elements — concatenating it as though it were elementwise would drop
// that state.
func TestRefuseUnknownMiddleStage(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  return xs.scan(0 as i64, (a: i64, b: i64): i64 => a + b)
           .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return run([1 as i64]) as i32; }`)
	mustRecognize(t, p, "run", 2)
	if n != 0 {
		t.Fatalf("fused %d pipelines, want 0", n)
	}
}

// std/array's own bodies are left alone: rewriting `map` into a loop over
// itself is not what this is for.
func TestStdlibBodiesAreNotFused(t *testing.T) {
	p, _ := fuseSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => x + (1 as i64))
           .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return run([1 as i64]) as i32; }`)
	// Recognition skips them, so the planner never sees one — and the
	// caller's own chain has already been rewritten, so nothing is left.
	if got := ir.FusibleArrayPipelines(p); len(got) != 0 {
		t.Errorf("after fusing, %d pipelines still report as fusible: %v", len(got), got)
	}
}

// The off switch exists so a miscompilation suspected here can be ruled out in
// one run. A pass that ignored it would make that impossible.
func TestOffSwitchDisablesFusion(t *testing.T) {
	t.Setenv("FERN_NO_ARRAY_FUSION", "1")
	_, n := fuseSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => x + (1 as i64))
           .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return run([1 as i64]) as i32; }`)
	if n != 0 {
		t.Fatalf("fused %d pipelines with the off switch set, want 0", n)
	}
}

// docs/ARRAY-ALGEBRA.md §1: an element function that touches the world is not
// fusible. Unfused, `map(f).map(g)` runs every f and then every g; fused, it
// interleaves them. The multiset of applications is unchanged either way, but
// the order of their side effects is not, and that is observable.
func TestRefuseEffectfulElementFunction(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function noisy(x: i64): i64 {
  print("x");
  return x + (1 as i64);
}
function run(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => noisy(x))
           .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return run([1 as i64]) as i32; }`)
	mustRecognize(t, p, "run", 2)
	if n != 0 {
		t.Fatalf("fused %d pipelines over an effectful element function, want 0", n)
	}
}

// The boundary is transitive: an element function that reaches the world
// through one more call is no purer than one that does it itself.
func TestRefuseTransitivelyEffectfulElementFunction(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function shout(x: i64): i64 { print("x"); return x; }
function quiet(x: i64): i64 { return shout(x) + (1 as i64); }
function run(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => quiet(x))
           .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return run([1 as i64]) as i32; }`)
	mustRecognize(t, p, "run", 2)
	if n != 0 {
		t.Fatalf("fused %d pipelines over a transitively effectful element function, want 0", n)
	}
}

// And the boundary does not swallow everything: an element function that calls
// an ordinary pure helper still fuses, or the rule would have no cases left.
func TestPureHelperStillFuses(t *testing.T) {
	_, n := fuseSrc(t, `import "std/array";
function double(x: i64): i64 { return x * (2 as i64); }
function run(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => double(x))
           .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return run([1 as i64]) as i32; }`)
	if n != 1 {
		t.Fatalf("fused %d pipelines over a pure helper, want 1", n)
	}
}
