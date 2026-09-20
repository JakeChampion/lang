package ir_test

import (
	"strconv"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// "Why did it allocate?" — #9732's second question, answered per stage.
//
// The verdicts come from the planner that performs R7's in-place rewrite
// (docs/REUSE-CONTRACT.md), so the report says `reused` exactly where the
// pass writes through the donor and names the declining rule everywhere
// else. A hardcoded sentence would go on being printed after the fact it
// describes stopped being true, which is what the report said about `map`
// for the day between #9797 landing R7 and this reading its verdicts.

func storageOf(t *testing.T, p *ir.Program, fn string) []ir.ArrayStorage {
	t.Helper()
	verdicts := ir.ArrayStorageVerdicts(p)
	var out []ir.ArrayStorage
	for _, pl := range ir.RecognizeArrayPipelines(p) {
		if pl.Func != fn {
			continue
		}
		for _, s := range pl.Stages {
			out = append(out, verdicts[pl.Func+"#"+strconv.Itoa(s.Op)])
		}
	}
	return out
}

func wantStorage(t *testing.T, got, want []ir.ArrayStorage) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d stage verdicts %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("stage %d: %q, want %q", i, got[i].Tag(), want[i].Tag())
		}
	}
}

// A chain the fusion pass declines whose receiver is a field read: its stages
// really do materialize, and the reason is that no `own` parameter licenses
// writing through the array.
func TestMaterializingStageBlamesTheUnownedReceiver(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
struct Box { xs: i64[] }
function run(b: Box): i64 {
  return b.xs.map((x: i64): i64 => x + (1 as i64))
             .fold(0 as i64, (p: i64, q: i64): i64 => p + q);
}
function main(): i32 { return run(Box { xs: [1 as i64] }) as i32; }`)
	wantStorage(t, storageOf(t, p, "run"), []ir.ArrayStorage{ir.StorageReceiverNotOwnedParam, ir.StorageScalar})
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
	wantStorage(t, storageOf(t, p, "run"), []ir.ArrayStorage{ir.StorageFused, ir.StorageScalar})
}

// The R7 shape reports the donation the pass performs.
func TestOwnedMapReportsTheDonatedBuffer(t *testing.T) {
	p := lowerPipelineSrc(t, inPlacePrelude+inPlaceChain)
	wantStorage(t, storageOf(t, p, "run"), []ir.ArrayStorage{ir.StorageReused})
}

// Each R7 refusal is its own row, so the histogram is the checklist for
// widening the shape rather than one undifferentiated "fresh".
func TestEachInPlaceRefusalHasItsOwnVerdict(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want ir.ArrayStorage
	}{
		{"borrowed receiver", `import "std/array";
function dbl(x: i64): i64 { return x * (2 as i64); }
function run(xs: i64[]): i64[] { return xs.map((x: i64): i64 => dbl(x)); }
function main(): i32 { return run([1 as i64]).len(); }`, ir.StorageReceiverNotOwnedParam},
		{"capturing element function", `import "std/array";
function run(own xs: i64[], k: i64): i64[] { return xs.map((x: i64): i64 => x + k); }
function main(): i32 { return run([1 as i64], 2 as i64).len(); }`, ir.StorageElementFunctionCaptures},
		{"effectful element function", `import "std/array";
function noisy(x: i64): i64 { print("x"); return x; }
function run(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => noisy(x)); }
function main(): i32 { return run([1 as i64]).len(); }`, ir.StorageElementFunctionEffectful},
		{"type-changing map", `import "std/array";
function narrow(x: i64): i32 { return x as i32; }
function run(own xs: i64[]): i32[] { return xs.map((x: i64): i32 => narrow(x)); }
function main(): i32 { return run([1 as i64]).len(); }`, ir.StorageShapeChange},
		{"narrow elements", `import "std/array";
function run(own xs: i32[]): i32[] { return xs.map((x: i32): i32 => x * 2); }
function main(): i32 { return run([1]).len(); }`, ir.StorageElementWidthUnsupported},
		{"an operator with no in-place shape", `import "std/array";
function run(own xs: i64[]): i64[] { return xs.filter((x: i64): boolean => x > (0 as i64)); }
function main(): i32 { return run([1 as i64]).len(); }`, ir.StorageNoInPlaceShape},
	} {
		p := lowerPipelineSrc(t, tc.src)
		got := storageOf(t, p, "run")
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: verdicts %v, want [%s]", tc.name, got, tc.want.Tag())
		}
	}
}

// The verdict set is closed with distinct tags, for the reason the fusion
// refusals are: it is a histogram row set, and a tally of colliding or empty
// tags is not a tally.
func TestArrayStorageTagsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range ir.AllArrayStorage {
		if s.Tag() == "unknown" {
			t.Errorf("verdict %d has no tag", int(s))
		}
		if seen[s.Tag()] {
			t.Errorf("tag %q is shared by two verdicts", s.Tag())
		}
		seen[s.Tag()] = true
		if s.Reason() == "" || s.Reason() == "unknown" {
			t.Errorf("verdict %q has a tag but no reason", s.Tag())
		}
	}
}
