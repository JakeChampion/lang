package e2e

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// The three array pipelines of #9728, measured by
// docs/ARRAY-PIPELINE-BASELINE-2026-09.md. That document decides whether the
// fusion work of #9727 is worth doing at all, so what it must not do is go
// stale: every number in it rests on the claims below, and a change that made
// one of them false would leave the report describing a compiler that no
// longer exists.
//
// scripts/array-pipeline-baseline is the full measurement — both compilers,
// every backend, callgrind instruction counts. This is the part of it that
// belongs in a gate: the claims, not the numbers. Absolute allocation counts
// are per-backend and must never be asserted (docs/ALLOCATION-OBSERVABLE.md),
// so everything here is a shape or a comparison.
//
//  1. Every variant of a program computes the SAME answer. Without this the
//     other claims are between programs that do different work, and a variant
//     that skipped a stage would report an allocation win for it.
//  2. The eager combinators allocate in steady state and the hand-written
//     loops do not — both halves, because "the loop allocates nothing" is also
//     true of a loop that was optimised away.
//  3. The allocation tracks the INPUT, not the startup: doubling n does not
//     reduce it. A fixed cost would mean the intermediates were already being
//     recovered and there is nothing here to fuse.
//  4. The intermediates are visible in BYTES on the cold pass and invisible in
//     the steady state, where the freelist recycles them. This is the half the
//     bump counter alone cannot report, and it is why the report quotes both.
//  5. `own` plus a same-shape `map` still allocates, while the hand-written
//     `own` loop does not. That gap is the whole of pipeline 3: ownership does
//     not by itself recover the donor buffer through a combinator.
type arrayPipelineReport struct {
	Program         string `json:"program"`
	Variant         string `json:"variant"`
	N               int64  `json:"n"`
	Rounds          int64  `json:"rounds"`
	Checksum        int64  `json:"checksum"`
	ColdAllocs      int64  `json:"cold_allocs"`
	ColdFreshBytes  int64  `json:"cold_fresh_bytes"`
	SteadyAllocs    int64  `json:"steady_allocs"`
	SteadyFreshByte int64  `json:"steady_fresh_bytes"`
	P50             int64  `json:"round_p50_ns"`
}

// arrayPipelineN is small enough that the whole matrix runs in seconds under
// qemu and large enough that an intermediate array is many allocator blocks
// rather than one. arrayPipelineRounds only has to reach a steady state.
const (
	arrayPipelineN      = 2048
	arrayPipelineRounds = 20
)

func buildArrayPipeline(t *testing.T, fern, dir, program string) string {
	t.Helper()
	src := langSrcAbs(t, filepath.Join("examples", "array_pipeline", program+".fern"))
	bin := filepath.Join(dir, program)
	if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile %s: %v\n%s", program, err, out)
	}
	return bin
}

func runArrayPipeline(t *testing.T, bin, program, variant string, n int64, runner []string) arrayPipelineReport {
	t.Helper()
	args := []string{variant, strconv.FormatInt(n, 10), strconv.Itoa(arrayPipelineRounds)}
	out, err := benchX86Cmd(runner, bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("run %s %s n=%d: %v\n%s", program, variant, n, err, out)
	}
	var got arrayPipelineReport
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%s/%s printed no report: %v\n%s", program, variant, err, out)
	}
	if got.Program != program || got.Variant != variant || got.N != n {
		t.Fatalf("report says %s/%s n=%d, ran %s/%s n=%d",
			got.Program, got.Variant, got.N, program, variant, n)
	}
	return got
}

// TestArrayPipelineCombinatorsAllocateAndLoopsDoNot covers pipelines 1 and 2:
// the same-cardinality `map`/`map`/`reduce` chain and the variable-cardinality
// `filter`/`map`/`reduce` one. They are asserted together because the claim is
// the same for both and the difference between them — how much the
// intermediate costs when a stage narrows the data — is the report's business,
// not the gate's.
func TestArrayPipelineCombinatorsAllocateAndLoopsDoNot(t *testing.T) {
	runner := x86NativeRunner(t) // SKIPs if neither native amd64 nor qemu-x86_64
	fern := buildFernCLI(t)
	dir := t.TempDir()

	for _, program := range []string{"map_map_reduce", "filter_map_reduce"} {
		t.Run(program, func(t *testing.T) {
			bin := buildArrayPipeline(t, fern, dir, program)

			pipeline := runArrayPipeline(t, bin, program, "pipeline", arrayPipelineN, runner)
			closureLoop := runArrayPipeline(t, bin, program, "closure_loop", arrayPipelineN, runner)
			loop := runArrayPipeline(t, bin, program, "loop", arrayPipelineN, runner)
			pipeline2n := runArrayPipeline(t, bin, program, "pipeline", 2*arrayPipelineN, runner)
			loop2n := runArrayPipeline(t, bin, program, "loop", 2*arrayPipelineN, runner)

			// 1. All three compute the same answer.
			for _, r := range []arrayPipelineReport{closureLoop, loop} {
				if r.Checksum != pipeline.Checksum {
					t.Errorf("%s computed %d, the combinator chain computed %d — the variants are no longer the same function, so nothing else here compares anything",
						r.Variant, r.Checksum, pipeline.Checksum)
				}
			}

			// 2. The combinators allocate; both hand-written loops do not.
			if pipeline.SteadyAllocs == 0 {
				t.Errorf("the combinator chain allocated nothing over %d rounds: either the intermediates are already being recovered — in which case docs/ARRAY-PIPELINE-BASELINE-2026-09.md's verdict is stale and #9727 has lost its premise — or the pipeline was optimised away",
					pipeline.Rounds)
			}
			for _, r := range []arrayPipelineReport{closureLoop, loop} {
				if r.SteadyAllocs != 0 {
					t.Errorf("%s allocated %d times over %d rounds, want 0 — the hand-written baseline is the parity bar (docs/ITERATOR-FUSION-CONTRACT.md) and it is no longer allocation-free",
						r.Variant, r.SteadyAllocs, r.Rounds)
				}
			}

			// 3. The allocation tracks the input, not a fixed startup cost.
			if pipeline2n.SteadyAllocs < pipeline.SteadyAllocs {
				t.Errorf("the combinator chain allocated %d times at n=%d but only %d at n=%d: allocation that FALLS as the input grows is not the per-stage intermediate this experiment measured",
					pipeline.SteadyAllocs, arrayPipelineN, pipeline2n.SteadyAllocs, 2*arrayPipelineN)
			}
			if loop2n.SteadyAllocs != 0 {
				t.Errorf("the hand-written loop allocated %d times at n=%d: it is allocation-free at n and must stay so at 2n",
					loop2n.SteadyAllocs, 2*arrayPipelineN)
			}

			// 4. The intermediates are bytes on the cold pass and nothing in
			//    the steady state. Both readings are load-bearing in the
			//    report, so both are pinned.
			if pipeline.ColdFreshBytes <= 0 {
				t.Errorf("the combinator chain's first pass took %d fresh bytes: the intermediate arrays are what this experiment is about and the cold reading can no longer see them",
					pipeline.ColdFreshBytes)
			}
			if pipeline2n.ColdFreshBytes <= pipeline.ColdFreshBytes {
				t.Errorf("the first pass took %d fresh bytes at n=%d and %d at n=%d — the intermediate is meant to be linear in the input, and a flat one would mean there is nothing to fuse",
					pipeline.ColdFreshBytes, arrayPipelineN, pipeline2n.ColdFreshBytes, 2*arrayPipelineN)
			}
			if loop.ColdFreshBytes != 0 {
				t.Errorf("the hand-written loop's first pass took %d fresh bytes, want 0", loop.ColdFreshBytes)
			}

			// 5. A distribution, so the latency half cannot quietly become
			//    zeros and keep reporting.
			for _, r := range []arrayPipelineReport{pipeline, closureLoop, loop} {
				if r.P50 <= 0 {
					t.Errorf("%s reported p50=%d", r.Variant, r.P50)
				}
			}
		})
	}
}

// TestArrayPipelineOwnMapDoesNotReuseTheDonor covers pipeline 3, which asks a
// different question from the other two: not whether an intermediate can
// disappear, but whether the result can BE the input's buffer.
//
// The answer today is no through the combinator and yes through the loop, and
// both halves matter. `with_borrowed` is the control: without it, `with_own`
// allocating nothing would be satisfiable by a `.with` that never copies for
// anybody, and ownership would not have been shown to be what did it.
func TestArrayPipelineOwnMapDoesNotReuseTheDonor(t *testing.T) {
	runner := x86NativeRunner(t)
	fern := buildFernCLI(t)
	dir := t.TempDir()
	bin := buildArrayPipeline(t, fern, dir, "own_map_inplace")

	mapOwn := runArrayPipeline(t, bin, "own_map_inplace", "map_own", arrayPipelineN, runner)
	withOwn := runArrayPipeline(t, bin, "own_map_inplace", "with_own", arrayPipelineN, runner)
	withBorrowed := runArrayPipeline(t, bin, "own_map_inplace", "with_borrowed", arrayPipelineN, runner)

	for _, r := range []arrayPipelineReport{withOwn, withBorrowed} {
		if r.Checksum != mapOwn.Checksum {
			t.Errorf("%s computed %d, the combinator computed %d — the three are meant to apply the same transform the same number of times",
				r.Variant, r.Checksum, mapOwn.Checksum)
		}
	}

	// The hand-written owned loop is allocation-free. It is annotated `fip`,
	// so E068 has already made this claim at compile time; this is the
	// runtime half of it, and a `fip` function that allocates would mean the
	// verifier and the allocator disagree.
	if withOwn.SteadyAllocs != 0 {
		t.Errorf("the `fip` in-place loop allocated %d times over %d rounds: E068 accepted the function, so the verifier and the allocator now disagree",
			withOwn.SteadyAllocs, withOwn.Rounds)
	}

	// `own` handed to `.map` does not recover the buffer. This is the finding
	// pipeline 3 exists to record; if it ever stops being true, the report is
	// wrong and ownership-aware materialization (#9733) has already happened.
	if mapOwn.SteadyAllocs <= withOwn.SteadyAllocs {
		t.Errorf("`own xs` through `xs.map(f)` allocated %d times against the in-place loop's %d — the combinator has started reusing the donor, which is #9733's job and would make docs/ARRAY-PIPELINE-BASELINE-2026-09.md's verdict stale",
			mapOwn.SteadyAllocs, withOwn.SteadyAllocs)
	}

	// The control: the borrowed receiver pays the copy-on-write copy, so it
	// allocates — less than the combinator, more than nothing.
	if withBorrowed.SteadyAllocs == 0 {
		t.Errorf("the borrowed-receiver loop allocated nothing: it is the control that shows ownership is what makes `with_own` free, and without it that zero means nothing")
	}
	if withBorrowed.SteadyAllocs >= mapOwn.SteadyAllocs {
		t.Errorf("the borrowed loop allocated %d times and the combinator %d: one copy-on-write copy per round is meant to be cheaper than rebuilding the array by append",
			withBorrowed.SteadyAllocs, mapOwn.SteadyAllocs)
	}
}
