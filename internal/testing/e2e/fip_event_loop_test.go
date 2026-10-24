package e2e

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

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
	runner := x86NativeRunner(t)
	fern := buildFernCLI(t)
	dir := t.TempDir()

	baseline := runFipEventLoop(t, fern, dir, "baseline", runner)
	fbip := runFipEventLoop(t, fern, dir, "fbip", runner)
	fip := runFipEventLoop(t, fern, dir, "fip", runner)

	for _, r := range []fipEventLoopReport{fbip, fip} {
		if r.SteadyAllocs != 0 {
			t.Errorf("%s: %d allocations over %d events, want 0 — the steady state is no longer allocation-free",
				r.Variant, r.SteadyAllocs, r.Events)
		}
	}

	if baseline.SteadyAllocs == 0 {
		t.Errorf("baseline allocated nothing: the experiment has lost its control, and the other two variants' zero means nothing")
	}

	for _, r := range []fipEventLoopReport{fbip, fip} {
		if r.Events != baseline.Events || r.Ok != baseline.Ok || r.Missing != baseline.Missing ||
			r.LiveEntries != baseline.LiveEntries || r.TableFull != baseline.TableFull || r.Dropped != baseline.Dropped {
			t.Errorf("%s disagrees with the baseline:\n  %s: %+v\n  baseline: %+v", r.Variant, r.Variant, r, baseline)
		}
	}

	for _, r := range []fipEventLoopReport{baseline, fbip, fip} {
		if r.P50 <= 0 || r.P999 < r.P50 {
			t.Errorf("%s: p50=%d p99.9=%d is not a distribution", r.Variant, r.P50, r.P999)
		}
	}
}
