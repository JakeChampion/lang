package e2e

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

type fipKVReport struct {
	Variant      string `json:"variant"`
	Mix          string `json:"mix"`
	LoadPct      int64  `json:"load_pct"`
	Requests     int64  `json:"requests"`
	LiveEntries  int64  `json:"live_entries"`
	Ok           int64  `json:"ok"`
	Missing      int64  `json:"missing"`
	TableFull    int64  `json:"table_full"`
	Values       int64  `json:"values"`
	Digest       int64  `json:"digest"`
	SteadyAllocs int64  `json:"steady_allocs"`
	P50          int64  `json:"round_p50_ns"`
	P999         int64  `json:"round_p999_ns"`
}

func runFipKV(t *testing.T, fern, dir, source, variant string, runner []string, args ...string) fipKVReport {
	t.Helper()
	src := langSrcAbs(t, filepath.Join("examples", "fip", "kv_"+source+".fern"))
	bin := filepath.Join(dir, "kv_"+source)
	if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile %s: %v\n%s", source, err, out)
	}
	out, err := benchX86Cmd(runner, bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("run %s %v: %v\n%s", variant, args, err, out)
	}
	var got fipKVReport
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%s printed no report: %v\n%s", variant, err, out)
	}
	if got.Variant != variant {
		t.Fatalf("report names variant %q, ran %q", got.Variant, variant)
	}
	if got.Requests == 0 {
		t.Fatalf("%s reports no requests", variant)
	}
	return got
}

func TestFipKVDisciplinesAgreeAndDoNotAllocate(t *testing.T) {
	runner := x86NativeRunner(t)
	fern := buildFernCLI(t)
	dir := t.TempDir()

	baseline := runFipKV(t, fern, dir, "baseline", "baseline", runner)
	pmap := runFipKV(t, fern, dir, "pmap", "pmap", runner)
	shared := runFipKV(t, fern, dir, "pmap", "pmap-shared", runner, "mixed", "50", "10000", "shared")
	fip := runFipKV(t, fern, dir, "fip", "fip", runner)

	if fip.SteadyAllocs != 0 {
		t.Errorf("fip: %d allocations over %d requests, want 0 — the steady state is no longer allocation-free",
			fip.SteadyAllocs, fip.Requests)
	}

	for _, r := range []fipKVReport{baseline, pmap, shared} {
		if r.SteadyAllocs == 0 {
			t.Errorf("%s allocated nothing: the experiment has lost its control, and the fip variant's zero means nothing", r.Variant)
		}
	}

	for _, r := range []fipKVReport{pmap, shared, fip} {
		if r.Requests != baseline.Requests || r.Ok != baseline.Ok || r.Missing != baseline.Missing ||
			r.LiveEntries != baseline.LiveEntries || r.TableFull != baseline.TableFull || r.Values != baseline.Values {
			t.Errorf("%s disagrees with the baseline:\n  %s: %+v\n  baseline: %+v", r.Variant, r.Variant, r, baseline)
		}
		if r.Digest != baseline.Digest {
			t.Errorf("%s answered different response bytes: digest %d, baseline %d", r.Variant, r.Digest, baseline.Digest)
		}
	}

	for _, r := range []fipKVReport{baseline, pmap, shared, fip} {
		if r.P50 <= 0 || r.P999 < r.P50 {
			t.Errorf("%s: p50=%d p99.9=%d is not a distribution", r.Variant, r.P50, r.P999)
		}
	}

	full := []fipKVReport{
		runFipKV(t, fern, dir, "baseline", "baseline", runner, "mixed", "150", "4000"),
		runFipKV(t, fern, dir, "pmap", "pmap", runner, "mixed", "150", "4000"),
		runFipKV(t, fern, dir, "fip", "fip", runner, "mixed", "150", "4000"),
	}
	if full[0].TableFull == 0 {
		t.Errorf("baseline at 150%% load reports no full-table responses; the capacity path is not exercised")
	}
	for _, r := range full[1:] {
		if r.TableFull != full[0].TableFull || r.LiveEntries != full[0].LiveEntries || r.Digest != full[0].Digest ||
			r.Ok != full[0].Ok || r.Missing != full[0].Missing || r.Values != full[0].Values {
			t.Errorf("%s at 150%% load disagrees with the baseline:\n  %s: %+v\n  baseline: %+v", r.Variant, r.Variant, r, full[0])
		}
	}
	if full[2].SteadyAllocs != 0 {
		t.Errorf("fip at 150%% load: %d allocations over %d requests, want 0", full[2].SteadyAllocs, full[2].Requests)
	}
}
