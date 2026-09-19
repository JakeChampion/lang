package e2e

import (
	"encoding/json"
	"os"
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
	return buildArrayPipelineFused(t, fern, dir, program, true)
}

// buildArrayPipelineInPlace compiles with ownership-aware materialization
// (#9733) on or off. The off build is what pipeline 3 measured before the pass
// existed, and keeping it reachable is what lets the premise and the
// improvement be asserted in the same test rather than one replacing the other.
func buildArrayPipelineInPlace(t *testing.T, fern, dir, program string, inPlace bool) string {
	t.Helper()
	src := langSrcAbs(t, filepath.Join("examples", "array_pipeline", program+".fern"))
	name := program
	cmd := exec.Command(fern, "-target", "x86-64-linux", "-o", "", src)
	if !inPlace {
		name = program + "_noinplace"
		cmd.Env = append(os.Environ(), "FERN_NO_ARRAY_INPLACE=1")
	}
	bin := filepath.Join(dir, name)
	cmd.Args[len(cmd.Args)-2] = bin
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile %s (in-place=%v): %v\n%s", program, inPlace, err, out)
	}
	return bin
}

// buildArrayPipelineFused compiles one of the baseline programs with array
// fusion (#9731) on or off. The off build is what this experiment measured
// before the pass existed, and keeping it reachable is what lets the premise
// and the improvement be asserted in the same test rather than one replacing
// the other.
func buildArrayPipelineFused(t *testing.T, fern, dir, program string, fused bool) string {
	t.Helper()
	src := langSrcAbs(t, filepath.Join("examples", "array_pipeline", program+".fern"))
	name := program
	cmd := exec.Command(fern, "-target", "x86-64-linux", "-o", "", src)
	if !fused {
		name = program + "_unfused"
		cmd.Env = append(os.Environ(), "FERN_NO_ARRAY_FUSION=1")
	}
	bin := filepath.Join(dir, name)
	cmd.Args[len(cmd.Args)-2] = bin
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile %s (fused=%v): %v\n%s", program, fused, err, out)
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
//
// The name is now half the story. #9731 fuses both chains, so the combinators
// no longer allocate, and the premise this test was written to pin —
// `docs/ARRAY-PIPELINE-BASELINE-2026-09.md`'s finding that each stage
// materializes a buffer linear in the input — is only visible with fusion
// turned off. Both builds are therefore measured: the unfused one still
// asserts the premise, exactly as before, and the fused one asserts what the
// pass bought. Dropping the unfused half would leave nothing establishing that
// there was anything to fuse.
func TestArrayPipelineCombinatorsAllocateAndLoopsDoNot(t *testing.T) {
	runner := x86NativeRunner(t) // SKIPs if neither native amd64 nor qemu-x86_64
	fern := buildFernCLI(t)
	dir := t.TempDir()

	for _, program := range []string{"map_map_reduce", "filter_map_reduce"} {
		t.Run(program, func(t *testing.T) {
			unfused := buildArrayPipelineFused(t, fern, dir, program, false)
			bin := buildArrayPipelineFused(t, fern, dir, program, true)

			raw := runArrayPipeline(t, unfused, program, "pipeline", arrayPipelineN, runner)
			raw2n := runArrayPipeline(t, unfused, program, "pipeline", 2*arrayPipelineN, runner)

			pipeline := runArrayPipeline(t, bin, program, "pipeline", arrayPipelineN, runner)
			closureLoop := runArrayPipeline(t, bin, program, "closure_loop", arrayPipelineN, runner)
			loop := runArrayPipeline(t, bin, program, "loop", arrayPipelineN, runner)
			callLoop := runArrayPipeline(t, bin, program, "call_loop", arrayPipelineN, runner)
			pipeline2n := runArrayPipeline(t, bin, program, "pipeline", 2*arrayPipelineN, runner)
			loop2n := runArrayPipeline(t, bin, program, "loop", 2*arrayPipelineN, runner)

			// 1. Every variant computes the same answer, fused or not. A
			//    fusion that changed the result is the failure this catches
			//    before any allocation number is worth reading.
			for _, r := range []arrayPipelineReport{closureLoop, loop, callLoop, raw} {
				if r.Checksum != pipeline.Checksum {
					t.Errorf("%s computed %d, the fused combinator chain computed %d — the variants are no longer the same function, so nothing else here compares anything",
						r.Variant, r.Checksum, pipeline.Checksum)
				}
			}

			// 2. THE PREMISE, unfused: the combinators allocate, and they
			//    allocate more as the input grows. This is what #9727 exists
			//    to remove and what the baseline document measured.
			if raw.SteadyAllocs == 0 {
				t.Errorf("with fusion off the combinator chain allocated nothing over %d rounds: docs/ARRAY-PIPELINE-BASELINE-2026-09.md's verdict would be stale and #9727 would have lost its premise",
					raw.Rounds)
			}
			if raw2n.SteadyAllocs < raw.SteadyAllocs {
				t.Errorf("with fusion off the chain allocated %d times at n=%d but only %d at n=%d: allocation that FALLS as the input grows is not the per-stage intermediate this experiment measured",
					raw.SteadyAllocs, arrayPipelineN, raw2n.SteadyAllocs, 2*arrayPipelineN)
			}
			if raw.ColdFreshBytes <= 0 {
				t.Errorf("with fusion off the first pass took %d fresh bytes: the intermediate arrays are what this experiment is about and the cold reading can no longer see them",
					raw.ColdFreshBytes)
			}
			if raw2n.ColdFreshBytes <= raw.ColdFreshBytes {
				t.Errorf("with fusion off the first pass took %d fresh bytes at n=%d and %d at n=%d — the intermediate is meant to be linear in the input, and a flat one would mean there was nothing to fuse",
					raw.ColdFreshBytes, arrayPipelineN, raw2n.ColdFreshBytes, 2*arrayPipelineN)
			}

			// 3. WHAT FUSION BOUGHT: the intermediates are gone. What is left
			//    is `reduce`'s own `Some(acc)` box, one per round, which its
			//    `Option[T]` return requires — so the bar is "does not grow
			//    with the input", not zero. Both chains here end in `reduce`;
			//    a `fold` sink allocates nothing at all.
			if pipeline.SteadyAllocs >= raw.SteadyAllocs {
				t.Errorf("fused the chain allocated %d times over %d rounds and unfused %d: fusion is meant to remove the intermediates",
					pipeline.SteadyAllocs, pipeline.Rounds, raw.SteadyAllocs)
			}
			if pipeline2n.SteadyAllocs != pipeline.SteadyAllocs {
				t.Errorf("the fused chain allocated %d times at n=%d and %d at n=%d: what remains is one O(1) box per round and must not track the input",
					pipeline.SteadyAllocs, arrayPipelineN, pipeline2n.SteadyAllocs, 2*arrayPipelineN)
			}
			if pipeline2n.ColdFreshBytes != pipeline.ColdFreshBytes {
				t.Errorf("the fused chain's first pass took %d fresh bytes at n=%d and %d at n=%d: with the intermediates gone the cold reading must not track the input either",
					pipeline.ColdFreshBytes, arrayPipelineN, pipeline2n.ColdFreshBytes, 2*arrayPipelineN)
			}

			// 4. The hand-written loops are the parity bar
			//    (docs/ITERATOR-FUSION-CONTRACT.md) and stay allocation-free.
			//    `call_loop` is the one that controls for the calls: it invokes
			//    the same element functions the pipeline's lambdas do, where
			//    `loop` writes their arithmetic out by hand. Without it a
			//    comparison against `loop` measures inlining as much as fusion.
			for _, r := range []arrayPipelineReport{closureLoop, loop, callLoop} {
				if r.SteadyAllocs != 0 {
					t.Errorf("%s allocated %d times over %d rounds, want 0 — the hand-written baseline is the parity bar and it is no longer allocation-free",
						r.Variant, r.SteadyAllocs, r.Rounds)
				}
			}
			if loop2n.SteadyAllocs != 0 {
				t.Errorf("the hand-written loop allocated %d times at n=%d: it is allocation-free at n and must stay so at 2n",
					loop2n.SteadyAllocs, 2*arrayPipelineN)
			}
			if loop.ColdFreshBytes != 0 {
				t.Errorf("the hand-written loop's first pass took %d fresh bytes, want 0", loop.ColdFreshBytes)
			}

			// 5. A distribution, so the latency half cannot quietly become
			//    zeros and keep reporting.
			for _, r := range []arrayPipelineReport{pipeline, closureLoop, loop, callLoop, raw} {
				if r.P50 <= 0 {
					t.Errorf("%s reported p50=%d", r.Variant, r.P50)
				}
			}
		})
	}
}

// Pipeline 3 asks a different question from the other two: not whether an
// intermediate can disappear, but whether the result can BE the input's
// buffer.
//
// The answer used to be no through the combinator and yes through the loop.
// #9733 changed it: `own xs` handed to `xs.map(f)` now writes through the
// donor's buffer, so the combinator reaches the hand-written loop's zero. Both
// builds are measured — the off build still asserts what pipeline 3 was built
// to record, so what the pass bought stays visible, and `with_borrowed`
// remains the control proving OWNERSHIP is what does it rather than the
// transform firing on anything that looks like a map.
func TestArrayPipelineOwnMapReusesTheDonor(t *testing.T) {
	runner := x86NativeRunner(t)
	fern := buildFernCLI(t)
	dir := t.TempDir()
	bin := buildArrayPipelineInPlace(t, fern, dir, "own_map_inplace", true)
	off := buildArrayPipelineInPlace(t, fern, dir, "own_map_inplace", false)

	mapOwn := runArrayPipeline(t, bin, "own_map_inplace", "map_own", arrayPipelineN, runner)
	withOwn := runArrayPipeline(t, bin, "own_map_inplace", "with_own", arrayPipelineN, runner)
	withBorrowed := runArrayPipeline(t, bin, "own_map_inplace", "with_borrowed", arrayPipelineN, runner)
	rawMapOwn := runArrayPipeline(t, off, "own_map_inplace", "map_own", arrayPipelineN, runner)

	// Every variant computes the same transform the same number of times,
	// with the pass and without it. A reuse that changed the answer is the
	// failure to catch before any allocation number is worth reading.
	for _, r := range []arrayPipelineReport{withOwn, withBorrowed, rawMapOwn} {
		if r.Checksum != mapOwn.Checksum {
			t.Errorf("%s computed %d, the combinator computed %d — the variants are meant to apply the same transform the same number of times",
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

	// THE PREMISE, with the pass off: rebuilding by append costs an
	// allocation per round and more.
	if rawMapOwn.SteadyAllocs <= withOwn.SteadyAllocs {
		t.Errorf("with ownership-aware materialization OFF, `own xs` through `xs.map(f)` allocated %d times against the in-place loop's %d — pipeline 3 exists to record that gap",
			rawMapOwn.SteadyAllocs, withOwn.SteadyAllocs)
	}

	// WHAT IT BOUGHT: the combinator reaches the hand-written loop.
	if mapOwn.SteadyAllocs != withOwn.SteadyAllocs {
		t.Errorf("`own xs` through `xs.map(f)` allocated %d times against the in-place loop's %d: the point of #9733 is that they match",
			mapOwn.SteadyAllocs, withOwn.SteadyAllocs)
	}

	// The control, and the one that matters most. A BORROWED receiver still
	// pays the copy: ownership is what licenses the reuse, so a transform
	// that fired on anything shaped like a map would show up as this zero.
	if withBorrowed.SteadyAllocs == 0 {
		t.Errorf("the borrowed-receiver loop allocated nothing: it is the control that shows ownership is what makes the owned forms free, and without it those zeros mean nothing")
	}
}
