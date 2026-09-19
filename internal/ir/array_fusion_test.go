package ir_test

import (
	"strconv"
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
	for key, v := range ir.ArrayFusionVerdicts(p) {
		if v.Why == ir.FusionFused {
			t.Errorf("after fusing, %s still reports as fusible", key)
		}
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

// Two chains in one function. Emitting the first rewrites the op slice, so
// every index the planner recorded for the second is stale — the pass replans
// from scratch after each emission for exactly this reason, and a version that
// planned both up front would corrupt the second.
func TestFuseTwoChainsInOneFunction(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function run(xs: i64[], ys: i64[]): i64 {
  var a: i64 = xs.map((x: i64): i64 => x + (1 as i64))
                 .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
  var b: i64 = ys.filter((y: i64): boolean => y > (0 as i64))
                 .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
  return a + b;
}
function main(): i32 { return run([1 as i64], [2 as i64]) as i32; }`)
	if n != 2 {
		t.Fatalf("fused %d pipelines, want 2", n)
	}
	if got := callsIn(t, p, "run", "array__"); len(got) != 0 {
		t.Errorf("run still calls std/array combinators: %v", got)
	}
}

// A chain inside a loop. The fused body is emitted into the middle of an
// enclosing structured-control-flow region, so its own OpBlock/OpLoop nesting
// has to balance or the br targets of the enclosing loop shift underneath it.
func TestFuseChainInsideALoop(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function run(xs: i64[], rounds: i32): i64 {
  var total: i64 = 0 as i64;
  var i: i32 = 0;
  while (i < rounds) {
    total = total + xs.map((x: i64): i64 => x + (1 as i64))
                      .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
    i = i + 1;
  }
  return total;
}
function main(): i32 { return run([1 as i64], 2) as i32; }`)
	if n != 1 {
		t.Fatalf("fused %d pipelines, want 1", n)
	}
	if got := callsIn(t, p, "run", "array__"); len(got) != 0 {
		t.Errorf("run still calls std/array combinators: %v", got)
	}
}

// Clause 4 of docs/ITERATOR-FUSION-CONTRACT.md: a chain outside the algebra
// says so, and says WHY, rather than quietly allocating per stage. The set of
// reasons is closed so the report can tally them — a checklist of free-text
// strings is not a checklist.
func TestRefusalsNameTheirReason(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
struct Box { xs: i64[] }
function noisy(x: i64): i64 { print("x"); return x + (1 as i64); }
function effectful(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => noisy(x)).fold(0 as i64, (p: i64, q: i64): i64 => p + q);
}
function fieldRecv(b: Box): i64 {
  return b.xs.map((x: i64): i64 => x + (1 as i64)).fold(0 as i64, (p: i64, q: i64): i64 => p + q);
}
function narrow(xs: i32[]): i32 {
  return xs.map((x: i32): i32 => x + 1).fold(0, (p: i32, q: i32): i32 => p + q);
}
function prefix(xs: i64[]): i64 {
  return xs.scan(0 as i64, (p: i64, q: i64): i64 => p + q).fold(0 as i64, (p: i64, q: i64): i64 => p + q);
}
function main(): i32 { return 0; }`)

	want := map[string]ir.FusionRefusal{
		"effectful": ir.FusionElementFunctionEffectful,
		"fieldRecv": ir.FusionReceiverNotASlot,
		"narrow":    ir.FusionElementWidthUnsupported,
		"prefix":    ir.FusionStageNotElementwise,
	}
	got := map[string]ir.FusionRefusal{}
	for _, pl := range ir.RecognizeArrayPipelines(p) {
		if _, interesting := want[pl.Func]; interesting {
			got[pl.Func] = ir.ArrayFusionVerdicts(p)[pl.Func+"#"+strconv.Itoa(pl.Stages[0].Op)].Why
		}
	}
	for fn, wantWhy := range want {
		if got[fn] != wantWhy {
			t.Errorf("%s refused with %q, want %q", fn, got[fn].Tag(), wantWhy.Tag())
		}
	}

	// Every reason is distinct and none renders as the fallback, or the
	// histogram would tally different failures into one row.
	tags := map[string]bool{}
	for _, r := range ir.AllFusionRefusals {
		if r.Tag() == "unknown" || r.String() == "not fused" {
			t.Errorf("refusal %d has no tag or no prose", int(r))
		}
		if tags[r.Tag()] {
			t.Errorf("tag %q is shared by two refusals", r.Tag())
		}
		tags[r.Tag()] = true
	}
	if _, listed := tags[ir.FusionFused.Tag()]; listed {
		t.Error("FusionFused is listed as a refusal; it is the absence of one")
	}
}

// #9732's acceptance: a refusal names a STAGE as well as a reason. "neither
// map nor filter" sends a reader back to count stages themselves on a chain of
// four, which is the work the report exists to save.
func TestRefusalsNameTheOffendingStage(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
function late(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => x + (1 as i64))
           .map((x: i64): i64 => x * (2 as i64))
           .scan(0 as i64, (a: i64, b: i64): i64 => a + b)
           .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return late([1 as i64]) as i32; }`)

	var got ir.FusionVerdict
	for key, v := range ir.ArrayFusionVerdicts(p) {
		if strings.HasPrefix(key, "late#") {
			got = v
		}
	}
	if got.Why != ir.FusionStageNotElementwise {
		t.Fatalf("refused with %q, want %q", got.Why.Tag(), ir.FusionStageNotElementwise.Tag())
	}
	// `scan` is the third stage, and naming the first two would be worse than
	// naming none: it would send the reader to a stage that is fine.
	if got.Stage != 2 || got.Verb != "scan" {
		t.Errorf("blamed stage %d (%q), want stage 2 (\"scan\")", got.Stage, got.Verb)
	}
	if s := got.String(); !strings.Contains(s, "stage 3") || !strings.Contains(s, "scan") {
		t.Errorf("rendered %q, want it to name stage 3 and scan", s)
	}
}

// A refusal that no single stage owns says so rather than blaming stage 1.
func TestChainLevelRefusalBlamesNoStage(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
struct Box { xs: i64[] }
function fieldRecv(b: Box): i64 {
  return b.xs.map((x: i64): i64 => x + (1 as i64))
             .fold(0 as i64, (a: i64, b0: i64): i64 => a + b0);
}
function main(): i32 { return fieldRecv(Box { xs: [1 as i64] }) as i32; }`)

	for key, v := range ir.ArrayFusionVerdicts(p) {
		if !strings.HasPrefix(key, "fieldRecv#") {
			continue
		}
		if v.Why != ir.FusionReceiverNotASlot {
			t.Fatalf("refused with %q, want %q", v.Why.Tag(), ir.FusionReceiverNotASlot.Tag())
		}
		if v.Verb != "" {
			t.Errorf("blamed stage %d (%q) for a refusal about the whole chain", v.Stage, v.Verb)
		}
		if s := v.String(); strings.Contains(s, "stage") {
			t.Errorf("rendered %q, want no stage named", s)
		}
	}
}

// countOps returns how many ops of one kind `fn` holds.
func countOps(t *testing.T, p *ir.Program, fn string, kind ir.OpKind) int {
	t.Helper()
	n := 0
	for _, f := range p.Funcs {
		if f.Name != fn {
			continue
		}
		for _, op := range f.Ops {
			if op.Kind == kind {
				n++
			}
		}
	}
	return n
}

// `reduce` seeds from the first element to ARRIVE. With no stage that can
// skip, that is element 0, so the first iteration is peeled and the loop runs
// from 1 with no flag — which is what the hand-written loop does, and what the
// per-element flag cost about two instructions an element to avoid. With a
// filter in the chain the first arrival is not known until the loop runs, so
// the flag stays; peeling there would seed the accumulator from an element the
// filter rejected.
//
// Both shapes live in one program and the assertion is the DIFFERENCE between
// them, because the absolute count includes the equality tests the surrounding
// `match` and the prologue emit. Peeled asks "is the array empty" once;
// flagged asks "has anything arrived" per element and again in the finish, so
// the filtered one carries exactly one more. A regression that quietly stopped
// peeling would otherwise be invisible — the answers stay right either way.
func TestReducePeelsOnlyWhenNoStageCanSkip(t *testing.T) {
	p, n := fuseSrc(t, `import "std/array";
function peeled(xs: i64[]): i64 {
  var out: Option[i64] = xs
    .map((x: i64): i64 => x + (1 as i64))
    .reduce((a: i64, b: i64): i64 => a + b);
  match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function flagged(xs: i64[]): i64 {
  var out: Option[i64] = xs
    .filter((x: i64): boolean => x > (0 as i64))
    .reduce((a: i64, b: i64): i64 => a + b);
  match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return peeled([1 as i64]) as i32 + flagged([1 as i64]) as i32; }`)
	if n != 2 {
		t.Fatalf("fused %d pipelines, want 2", n)
	}
	got, want := countOps(t, p, "flagged", ir.OpEq)-countOps(t, p, "peeled", ir.OpEq), 1
	if got != want {
		t.Errorf("the filtered chain carries %d more equality tests than the unfiltered one, want %d: "+
			"0 means the filtered chain peeled, which would seed the accumulator from a rejected "+
			"element; 2 or more means the unfiltered chain kept its per-element arrival flag",
			got, want)
	}
}

// What the fused loop actually calls per element, pinned by kind and by
// whether the element function is inlinable.
//
// This exists because the claim "the element functions are inlined outright"
// went into two documents on the strength of a count that looked at
// OpCallIndirect and OpCallDirect and never at OpCallClosureDirect — which is
// the kind they are dispatched through when they are NOT inlined.
//
// What is always true is the dispatch: fusion runs before `Defunctionalise`,
// which resolves every element call to a statically named target, so no
// element function is reached through a function value at run time. That is
// clause 1's "no unspecialised call per element".
//
// What varies is whether the call then survives, and that is `Inline`'s
// ordinary decision rather than anything fusion does. A leaf element function
// is absorbed; one that calls something else is not, and stays a direct call.
// The benchmark programs in examples/array_pipeline are the second kind —
// their element functions call `modulus()` — which is why the fused loop there
// still makes three calls an element, and most of why it does not reach a
// hand-written loop that spells the arithmetic inline.
func countCallKinds(t *testing.T, src string) (indirect, closureDirect int) {
	t.Helper()
	p := lowerPipelineSrc(t, src)
	ir.OptimizeProgram(p, 8)
	for _, fn := range p.Funcs {
		if fn.Name != "run" {
			continue
		}
		for _, op := range fn.Ops {
			switch op.Kind {
			case ir.OpCallIndirect:
				indirect++
			case ir.OpCallClosureDirect:
				closureDirect++
			}
		}
	}
	return indirect, closureDirect
}

const fusedCallShapeChain = `
function run(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => f(x))
           .map((x: i64): i64 => g(x))
           .fold(0 as i64, (a: i64, b: i64): i64 => h(a, b));
}
function main(): i32 { return run([1 as i64]) as i32; }`

func TestFusedLoopResolvesDispatchAndInlinesLeafElementFunctions(t *testing.T) {
	indirect, closureDirect := countCallKinds(t, `import "std/array";
function f(x: i64): i64 { return x + (1 as i64); }
function g(x: i64): i64 { return x * (2 as i64); }
function h(a: i64, b: i64): i64 { return a + b; }`+fusedCallShapeChain)
	if indirect != 0 {
		t.Errorf("fused loop makes %d indirect calls, want 0", indirect)
	}
	if closureDirect != 0 {
		t.Errorf("fused loop makes %d direct closure calls over LEAF element functions, "+
			"want 0 — these are small enough for Inline to absorb", closureDirect)
	}
}

func TestFusedLoopStillCallsNonInlinableElementFunctions(t *testing.T) {
	indirect, closureDirect := countCallKinds(t, `import "std/array";
function k(): i64 { return 7 as i64; }
function f(x: i64): i64 { return x + k(); }
function g(x: i64): i64 { return x * k(); }
function h(a: i64, b: i64): i64 { return a + b + k(); }`+fusedCallShapeChain)
	if indirect != 0 {
		t.Errorf("fused loop makes %d indirect calls, want 0 — the dispatch is meant to be "+
			"resolved whether or not the body is then inlined", indirect)
	}
	if closureDirect != 3 {
		t.Errorf("fused loop makes %d direct closure calls, want 3 (two maps and the sink). "+
			"0 would mean Inline grew to absorb non-leaf element functions, which is better "+
			"than docs/ARRAY-FUSION-OPERATORS.md claims — update it deliberately", closureDirect)
	}
}
