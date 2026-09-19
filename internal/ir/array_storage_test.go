package ir_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// "Why did it allocate?" — #9732's second question, answered per stage.
//
// The verdicts are read off the IR rather than asserted: whether a combinator
// consumes its array is `Func.ParamConsumed`, and whether the element shape
// survives is the two stages' parameter types. That matters because the answer
// today is the same for every std/array combinator, and a hardcoded sentence
// would go on being printed after the fact it describes stopped being true.

func storageOf(t *testing.T, p *ir.Program, fn string) []ir.ArrayStorage {
	t.Helper()
	verdicts := ir.ArrayFusionVerdicts(p)
	var out []ir.ArrayStorage
	for _, pl := range ir.RecognizeArrayPipelines(p) {
		if pl.Func != fn {
			continue
		}
		fused := verdicts[pl.Func+"#"+strconv.Itoa(pl.Stages[0].Op)].Why == ir.FusionFused
		for i := range pl.Stages {
			out = append(out, ir.ArrayStageStorage(p, pl, i, fused))
		}
	}
	return out
}

// A chain the pass declines: its stages really do materialize, and the reason
// is the combinator's signature rather than anything about the caller.
func TestMaterializingStageBlamesTheBorrowedArray(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
struct Box { xs: i64[] }
function run(b: Box): i64 {
  return b.xs.map((x: i64): i64 => x + (1 as i64))
             .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
}
function main(): i32 { return run(Box { xs: [1 as i64] }) as i32; }`)
	got := storageOf(t, p, "run")
	want := []ir.ArrayStorage{ir.StorageBorrowedDonor, ir.StorageScalar}
	if len(got) != len(want) {
		t.Fatalf("got %d stage verdicts, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("stage %d: %q, want %q", i, got[i].Tag(), want[i].Tag())
		}
	}
}

// A fused chain materializes nothing — except that its reduction never did,
// which is why the reduction is answered before the fusion.
func TestFusedStagesReportNoBuffer(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => x + (1 as i64))
           .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
}
function main(): i32 { return run([1 as i64]) as i32; }`)
	got := storageOf(t, p, "run")
	want := []ir.ArrayStorage{ir.StorageFused, ir.StorageScalar}
	if len(got) != len(want) {
		t.Fatalf("got %d stage verdicts, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("stage %d: %q, want %q", i, got[i].Tag(), want[i].Tag())
		}
	}
}

// The canary. `shape-change` and `reused` cannot fire today because every
// std/array combinator BORROWS its array, so the ownership check answers
// first and nothing reaches them. That is a property of the stdlib, not of
// this pass — and if it changes, the report starts having more to say and
// this test is where someone finds out.
func TestNoStdArrayCombinatorConsumesItsArray(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => x + (1 as i64))
           .filter((x: i64): boolean => x > (0 as i64))
           .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return run([1 as i64]) as i32; }`)
	for _, fn := range p.Funcs {
		if !strings.HasPrefix(fn.Name, "array__") || len(fn.ParamConsumed) == 0 {
			continue
		}
		if len(fn.Params) == 0 {
			continue
		}
		if _, isArr := fn.Params[0].Type.(ast.ArrayType); !isArr {
			continue
		}
		if fn.ParamConsumed[0] {
			t.Errorf("%s now CONSUMES its array parameter. A stage feeding it can donate "+
				"its buffer, so ArrayStageStorage reaches shape-change/reused for the "+
				"first time — check the report says something useful there rather than "+
				"falling through", fn.Name)
		}
	}
}

// The verdict set is closed with distinct tags, for the reason the fusion
// refusals are: it is a histogram row set, and a tally of colliding or empty
// tags is not a tally.
func TestArrayStorageTagsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range ir.AllArrayStorage {
		if s.Tag() == "unknown" && s != ir.StorageUnknown {
			t.Errorf("verdict %d has no tag", int(s))
		}
		if seen[s.Tag()] {
			t.Errorf("tag %q is shared by two verdicts", s.Tag())
		}
		seen[s.Tag()] = true
		if s.String() == "" {
			t.Errorf("verdict %q has a tag but no prose", s.Tag())
		}
	}
}
