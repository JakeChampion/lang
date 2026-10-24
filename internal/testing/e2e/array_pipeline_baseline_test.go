package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

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

const (
	arrayPipelineN      = 2048
	arrayPipelineRounds = 20
)

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

func TestArrayPipelineCombinatorsAllocateAndLoopsDoNot(t *testing.T) {
	runner := x86NativeRunner(t)
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

			for _, r := range []arrayPipelineReport{closureLoop, loop, callLoop, raw} {
				if r.Checksum != pipeline.Checksum {
					t.Errorf("%s computed %d, the fused combinator chain computed %d — the variants are no longer the same function, so nothing else here compares anything",
						r.Variant, r.Checksum, pipeline.Checksum)
				}
			}

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

			for _, r := range []arrayPipelineReport{pipeline, closureLoop, loop, callLoop, raw} {
				if r.P50 <= 0 {
					t.Errorf("%s reported p50=%d", r.Variant, r.P50)
				}
			}
		})
	}
}

func TestArrayPipelineOwnMapReusesTheDonor(t *testing.T) {
	runner := x86NativeRunner(t)
	fern := buildFernCLI(t)
	dir := t.TempDir()
	bin := buildArrayPipelineFused(t, fern, dir, "own_map_inplace", true)

	mapOwn := runArrayPipeline(t, bin, "own_map_inplace", "map_own", arrayPipelineN, runner)
	withOwn := runArrayPipeline(t, bin, "own_map_inplace", "with_own", arrayPipelineN, runner)
	withBorrowed := runArrayPipeline(t, bin, "own_map_inplace", "with_borrowed", arrayPipelineN, runner)

	for _, r := range []arrayPipelineReport{withOwn, withBorrowed} {
		if r.Checksum != mapOwn.Checksum {
			t.Errorf("%s computed %d, the combinator computed %d — the variants are meant to apply the same transform the same number of times",
				r.Variant, r.Checksum, mapOwn.Checksum)
		}
	}

	if withOwn.SteadyAllocs != 0 {
		t.Errorf("the `fip` in-place loop allocated %d times over %d rounds: E068 accepted the function, so the verifier and the allocator now disagree",
			withOwn.SteadyAllocs, withOwn.Rounds)
	}

	if mapOwn.SteadyAllocs != withOwn.SteadyAllocs {
		t.Errorf("`own xs` through `xs.map(f)` allocated %d times against the in-place loop's %d: the point of #9733 is that they match",
			mapOwn.SteadyAllocs, withOwn.SteadyAllocs)
	}

	if withBorrowed.SteadyAllocs == 0 {
		t.Errorf("the borrowed-receiver loop allocated nothing: it is the control that shows ownership is what makes the owned forms free, and without it those zeros mean nothing")
	}
}
