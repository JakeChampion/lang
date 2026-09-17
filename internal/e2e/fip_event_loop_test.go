package e2e

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// The bounded event loop of #9583, in its three memory disciplines: an
// idiomatic allocating baseline, an `fbip` struct-of-arrays, and a strict
// `fip` data plane over one packed array. `docs/FIP-EVENT-LOOP.md` reports
// what they measure; this pins the two claims that would make that report
// false.
//
//  1. The two disciplined variants allocate NOTHING in steady state, over
//     1.28M events. Not "few" — zero, which is the claim `fip` exists to make
//     checkable and which `__heap_bump_bytes()` alone cannot see, since every
//     allocation the baseline makes here is recycled and buys no fresh bytes.
//  2. All three agree, event for event. Three implementations of one state
//     machine are a differential test of the disciplines: a variant that
//     drifted would otherwise report a throughput win for doing less work.
//
// The baseline is asserted to allocate, which is not a formality — if it ever
// stops, the comparison is between two things that are the same and the
// experiment has quietly lost its control.

type fipEventLoopReport struct {
	Variant      string `json:"variant"`
	Events       int64  `json:"events"`
	LiveEntries  int64  `json:"live_entries"`
	Ok           int64  `json:"ok"`
	Missing      int64  `json:"missing"`
	TableFull    int64  `json:"table_full"`
	Dropped      int64  `json:"dropped"`
	SteadyAllocs int64  `json:"steady_allocs"`
	P50          int64  `json:"round_p50_ns"`
	P999         int64  `json:"round_p999_ns"`
}

func runFipEventLoop(t *testing.T, fern, dir, variant string, runner []string) fipEventLoopReport {
	t.Helper()
	src := langSrcAbs(t, filepath.Join("examples", "fip", "event_loop_"+variant+".fern"))
	bin := filepath.Join(dir, variant)
	if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile %s: %v\n%s", variant, err, out)
	}
	// The binary is x86-64 whatever the host is, so it goes through the runner
	// x86NativeRunner supplies — directly on amd64, under qemu-x86_64
	// elsewhere. Exec'ing it unconditionally failed the whole aarch64 lane
	// with `exec format error`, which reads as the harness being broken rather
	// than unrunnable; #9616 fixed exactly that in the std/bench gates.
	out, err := benchX86Cmd(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("run %s: %v\n%s", variant, err, out)
	}
	var got fipEventLoopReport
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%s printed no report: %v\n%s", variant, err, out)
	}
	if got.Variant != variant {
		t.Fatalf("report names variant %q, ran %q", got.Variant, variant)
	}
	if got.Events == 0 {
		t.Fatalf("%s reports no events", variant)
	}
	return got
}

func TestFipEventLoopDisciplinesAgreeAndDoNotAllocate(t *testing.T) {
	runner := x86NativeRunner(t) // SKIPs if neither native amd64 nor qemu-x86_64
	fern := buildFernCLI(t)
	dir := t.TempDir()

	baseline := runFipEventLoop(t, fern, dir, "baseline", runner)
	fbip := runFipEventLoop(t, fern, dir, "fbip", runner)
	fip := runFipEventLoop(t, fern, dir, "fip", runner)

	// 1. The disciplined variants allocate nothing after initialization.
	for _, r := range []fipEventLoopReport{fbip, fip} {
		if r.SteadyAllocs != 0 {
			t.Errorf("%s: %d allocations over %d events, want 0 — the steady state is no longer allocation-free",
				r.Variant, r.SteadyAllocs, r.Events)
		}
	}

	// 2. The control must actually allocate, or there is nothing to compare.
	if baseline.SteadyAllocs == 0 {
		t.Errorf("baseline allocated nothing: the experiment has lost its control, and the other two variants' zero means nothing")
	}

	// 3. The three agree event for event.
	for _, r := range []fipEventLoopReport{fbip, fip} {
		if r.Events != baseline.Events || r.Ok != baseline.Ok || r.Missing != baseline.Missing ||
			r.LiveEntries != baseline.LiveEntries || r.TableFull != baseline.TableFull || r.Dropped != baseline.Dropped {
			t.Errorf("%s disagrees with the baseline:\n  %s: %+v\n  baseline: %+v", r.Variant, r.Variant, r, baseline)
		}
	}

	// 4. Every variant reports a tail, so the latency half of the report cannot
	//    silently become zeros.
	for _, r := range []fipEventLoopReport{baseline, fbip, fip} {
		if r.P50 <= 0 || r.P999 < r.P50 {
			t.Errorf("%s: p50=%d p99.9=%d is not a distribution", r.Variant, r.P50, r.P999)
		}
	}
}
