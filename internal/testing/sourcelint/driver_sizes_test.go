package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func runSizeCheck(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "ci-check-driver-sizes"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("size script missing: %v", err)
	}
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = ciEnv(env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); !ok {
			t.Fatalf("run size script: %v (output: %s)", err, out)
		}
		code = ee.ExitCode()
	}
	return code, string(out)
}

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

const sizeBaseline0806 = `# tracked-artifact: fern.fern
fern.fern 134243612
asm_ir_run.fern 127504604
wasm_ir_run.fern 58870908
`

const sizeReport0814 = "fern.fern\t155895596\nasm_ir_run.fern\t146081148\nwasm_ir_run.fern\t77378268\n"

func TestCheckDriverSizesQuietOnBaseline(t *testing.T) {
	base := writeTemp(t, "baseline.txt", sizeBaseline0806)
	report := writeTemp(t, "report.txt", "fern.fern\t134243612\nasm_ir_run.fern\t127504604\nwasm_ir_run.fern\t58870908\n")

	for _, env := range [][]string{{"GITHUB_ACTIONS=true"}, nil} {
		code, out := runSizeCheck(t, env, base, report)
		if code != 0 {
			t.Fatalf("env %v: want exit 0, got %d: %s", env, code, out)
		}
		if !strings.Contains(out, "Every driver measured here is within 5% of its baseline") {
			t.Errorf("env %v: want the all-clear line, got:\n%s", env, out)
		}
		if !strings.Contains(out, "Coverage: 3 of 3 baselined drivers measured.") {
			t.Errorf("env %v: the all-clear must state what it covered, got:\n%s", env, out)
		}
		for _, bad := range []string{"GREW", "SHRANK", "WARNING", "::warning", "Corrected baseline"} {
			if strings.Contains(out, bad) {
				t.Errorf("env %v: a matching run produced %q:\n%s", env, bad, out)
			}
		}
		if !strings.Contains(out, "fern.fern (tracked artifact)") {
			t.Errorf("env %v: the tracked artifact must be marked as such:\n%s", env, out)
		}
	}
}

func TestCheckDriverSizesWritesJobSummary(t *testing.T) {
	base := writeTemp(t, "baseline.txt", sizeBaseline0806)
	report := writeTemp(t, "report.txt", sizeReport0814)

	summary := filepath.Join(t.TempDir(), "summary.md")
	code, out := runSizeCheck(t, []string{"GITHUB_STEP_SUMMARY=" + summary, "GITHUB_ACTIONS=true"}, base, report)
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
	body, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	for _, want := range []string{"| driver | linked | baseline | delta |", "GREW: fern.fern", "Corrected baseline"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("summary missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "::warning") {
		t.Errorf("annotations must not be pasted into the summary:\n%s", body)
	}
	if !strings.Contains(out, "::warning title=fern.fern grew") {
		t.Errorf("the annotation still belongs on stdout:\n%s", out)
	}
}

func TestCheckDriverSizesFlagsTheHistoricalGrowth(t *testing.T) {
	base := writeTemp(t, "baseline.txt", sizeBaseline0806)
	report := writeTemp(t, "report.txt", sizeReport0814)

	code, out := runSizeCheck(t, []string{"GITHUB_ACTIONS=1"}, base, report)
	if code != 0 {
		t.Fatalf("advisory check must exit 0, got %d: %s", code, out)
	}
	for _, want := range []string{
		"GREW: fern.fern 134.2 MB -> 155.9 MB (+16.1%",
		"GREW: asm_ir_run.fern 127.5 MB -> 146.1 MB (+14.6%",
		"GREW: wasm_ir_run.fern 58.9 MB -> 77.4 MB (+31.4%",
		"::warning title=fern.fern grew +16.1%::",
		"::fern.fern linked at 155.9 MB against a baseline of 134.2 MB",
		"Corrected baseline",
		"fern.fern 155895596",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestCheckDriverSizesFlagsShrink(t *testing.T) {
	base := writeTemp(t, "baseline.txt", sizeBaseline0806)
	report := writeTemp(t, "report.txt", "fern.fern\t100000000\n")

	code, out := runSizeCheck(t, nil, base, report)
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "SHRANK: fern.fern") {
		t.Errorf("a shrink must be reported:\n%s", out)
	}
	if !strings.Contains(out, "fern.fern 100000000") {
		t.Errorf("a shrink must come with the corrected baseline line:\n%s", out)
	}
}

func TestCheckDriverSizesToleratesSmallDrift(t *testing.T) {
	base := writeTemp(t, "baseline.txt", "# tracked-artifact: fern.fern\nfern.fern 100000000\n")
	report := writeTemp(t, "report.txt", "fern.fern\t103000000\n")

	code, out := runSizeCheck(t, nil, base, report)
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "+3.0%") {
		t.Errorf("the delta must still be recorded:\n%s", out)
	}
	if strings.Contains(out, "GREW") {
		t.Errorf("3%% is under the 5%% tolerance:\n%s", out)
	}
}

func TestCheckDriverSizesReportsUnbaselinedOnce(t *testing.T) {
	base := writeTemp(t, "baseline.txt", "# tracked-artifact: fern.fern\nfern.fern 155895596\n")
	report := writeTemp(t, "report.txt", "fern.fern\t155895596\nssa_run.fern\t40000000\nchecker_modload_run.fern\t41000000\n")

	code, out := runSizeCheck(t, []string{"GITHUB_ACTIONS=1"}, base, report)
	if code != 0 {
		t.Fatalf("want exit 0, got %d: %s", code, out)
	}
	if !strings.Contains(out, "UNBASELINED: 2 driver(s)") {
		t.Errorf("want the aggregate unbaselined line:\n%s", out)
	}
	if n := strings.Count(out, "::warning"); n != 1 {
		t.Errorf("want exactly 1 annotation for 2 unbaselined drivers, got %d:\n%s", n, out)
	}
}

func TestCheckDriverSizesReportsWhenItRanBlind(t *testing.T) {
	base := writeTemp(t, "baseline.txt", sizeBaseline0806)

	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"on a runner", []string{"GITHUB_ACTIONS=true"}, "::warning title=driver size check ran blind::"},
		{"off a runner", nil, "WARNING: driver size check ran blind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runSizeCheck(t, tc.env, base, filepath.Join(t.TempDir(), "never-written.txt"))
			if code != 0 {
				t.Fatalf("want exit 0, got %d: %s", code, out)
			}
			if !strings.Contains(out, "NO SIZE REPORT") {
				t.Errorf("a missing report must say so:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, out)
			}
		})
	}
}

func TestCheckDriverSizesStrictModeIsFatal(t *testing.T) {
	base := writeTemp(t, "baseline.txt", sizeBaseline0806)
	report := writeTemp(t, "report.txt", sizeReport0814)

	code, out := runSizeCheck(t, []string{"FERN_CI_SIZE_GATE_STRICT=1"}, base, report)
	if code != 1 {
		t.Fatalf("want exit 1 under strict mode, got %d: %s", code, out)
	}
	if !strings.Contains(out, "3 finding(s) and FERN_CI_SIZE_GATE_STRICT=1") {
		t.Errorf("missing the strict-mode reason:\n%s", out)
	}
}

func TestCheckDriverSizesPartialReportSaysItIsPartial(t *testing.T) {
	base := writeTemp(t, "baseline.txt", sizeBaseline0806)
	report := writeTemp(t, "report.txt", "fern.fern\t134243612\n")

	code, out := runSizeCheck(t, []string{"GITHUB_ACTIONS=1"}, base, report)
	if code != 0 {
		t.Fatalf("a slice is not a finding without FERN_SIZE_REPORT_COMPLETE: got %d: %s", code, out)
	}
	if !strings.Contains(out, "Coverage: 1 of 3 baselined drivers measured HERE") {
		t.Errorf("a partial report must name its own denominator:\n%s", out)
	}
	if strings.Contains(out, "MISSING") {
		t.Errorf("a partial report must not report the drivers it never claimed:\n%s", out)
	}
	if strings.Contains(out, "::warning") {
		t.Errorf("a within-tolerance slice must not annotate:\n%s", out)
	}
}

func TestCheckDriverSizesCompleteModeFlagsUnmeasured(t *testing.T) {
	base := writeTemp(t, "baseline.txt", sizeBaseline0806)
	report := writeTemp(t, "report.txt", "fern.fern\t134243612\n")

	code, out := runSizeCheck(t, []string{"FERN_SIZE_REPORT_COMPLETE=1", "GITHUB_ACTIONS=1"}, base, report)
	if code != 0 {
		t.Fatalf("advisory unless strict: got %d: %s", code, out)
	}
	if !strings.Contains(out, "MISSING: 2 of 3 baselined driver(s)") {
		t.Errorf("want the unmeasured drivers reported:\n%s", out)
	}
	for _, want := range []string{"asm_ir_run.fern", "wasm_ir_run.fern"} {
		if !strings.Contains(out, want) {
			t.Errorf("the unmeasured drivers must be named, missing %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "::warning"); n != 1 {
		t.Errorf("want exactly 1 annotation for 2 unmeasured drivers, got %d:\n%s", n, out)
	}
	if strings.Contains(out, "Every driver measured here is within") {
		t.Errorf("an incomplete comparison must not end in an all-clear:\n%s", out)
	}
}

func TestCheckDriverSizesCompleteModeStrictFailsOnUnmeasured(t *testing.T) {
	base := writeTemp(t, "baseline.txt", sizeBaseline0806)
	report := writeTemp(t, "report.txt", "fern.fern\t134243612\n")

	code, out := runSizeCheck(t, []string{"FERN_SIZE_REPORT_COMPLETE=1", "FERN_CI_SIZE_GATE_STRICT=1"}, base, report)
	if code != 1 {
		t.Fatalf("want exit 1 for a short union under strict, got %d: %s", code, out)
	}
	if !strings.Contains(out, "2 finding(s) and FERN_CI_SIZE_GATE_STRICT=1") {
		t.Errorf("missing the strict-mode reason:\n%s", out)
	}
}

func TestCheckDriverSizesCompleteModeQuietWhenWhole(t *testing.T) {
	base := writeTemp(t, "baseline.txt", sizeBaseline0806)
	report := writeTemp(t, "report.txt", "fern.fern\t134243612\nasm_ir_run.fern\t127504604\nwasm_ir_run.fern\t58870908\n")

	code, out := runSizeCheck(t, []string{"FERN_SIZE_REPORT_COMPLETE=1", "FERN_CI_SIZE_GATE_STRICT=1", "GITHUB_ACTIONS=1"}, base, report)
	if code != 0 {
		t.Fatalf("want exit 0 for a whole, in-tolerance union, got %d: %s", code, out)
	}
	if !strings.Contains(out, "Coverage: 3 of 3 baselined drivers measured — this report is the union") {
		t.Errorf("the complete run must state its coverage:\n%s", out)
	}
	for _, bad := range []string{"MISSING", "GREW", "SHRANK", "::warning"} {
		if strings.Contains(out, bad) {
			t.Errorf("a whole in-tolerance union produced %q:\n%s", bad, out)
		}
	}
}

func TestCheckDriverSizesBlindIsFatalUnderStrict(t *testing.T) {
	base := writeTemp(t, "baseline.txt", sizeBaseline0806)

	code, out := runSizeCheck(t, []string{"FERN_CI_SIZE_GATE_STRICT=1"}, base, filepath.Join(t.TempDir(), "never-written.txt"))
	if code != 1 {
		t.Fatalf("want exit 1 when strict and nothing was measured, got %d: %s", code, out)
	}
	if !strings.Contains(out, "nothing was measured and FERN_CI_SIZE_GATE_STRICT=1") {
		t.Errorf("missing the strict-mode reason:\n%s", out)
	}
}

func TestCheckDriverSizesBadUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"only-one-arg"}, {"/nonexistent-baseline", "/nonexistent-report"}} {
		code, out := runSizeCheck(t, nil, args...)
		if code == 0 {
			t.Errorf("args %q exited 0: %s", args, out)
		}
	}
}

func TestSelfHostDriverSizeBaselineIsWellFormed(t *testing.T) {
	p := filepath.Join("..", "..", "..", ".github", "selfhost-driver-sizes.txt")
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	var tracked string
	entries := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# tracked-artifact:") {
			tracked = strings.TrimSpace(strings.TrimPrefix(trimmed, "# tracked-artifact:"))
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		f := strings.Fields(trimmed)
		if len(f) != 2 {
			t.Errorf("baseline line is not `driver bytes`: %q", trimmed)
			continue
		}
		if mustAtoi(t, f[1]) <= 0 {
			t.Errorf("baseline entry %q has a non-positive size", trimmed)
		}
		entries[f[0]] = true
	}
	if tracked == "" {
		t.Fatal("baseline has no `# tracked-artifact:` directive — nothing names THE self-host binary")
	}
	if !entries[tracked] {
		t.Errorf("tracked artifact %q has no size entry of its own", tracked)
	}
	if len(entries) == 0 {
		t.Error("baseline has no entries, so the size check compares nothing")
	}
}

func TestSelfHostWorkflowExportsDriverSizeReport(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "test-e2e-selfhost.yml"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	src := string(body)
	exports := strings.Count(src, "FERN_DRIVER_SIZE_REPORT:")
	checks := strings.Count(src, "scripts/ci-check-driver-sizes")
	if exports == 0 {
		t.Error("no job exports FERN_DRIVER_SIZE_REPORT, so nothing records a driver size")
	}
	if exports != 1 || checks != 1 {
		t.Errorf("%d job(s) export FERN_DRIVER_SIZE_REPORT and %d invoke ci-check-driver-sizes — "+
			"want exactly one of each, both in the driver-sizes job (#7519)", exports, checks)
	}
	job := driverSizesJob(t, src)
	for _, want := range []struct{ needle, why string }{
		{`FERN_SIZE_REPORT_COMPLETE: "1"`, "a baselined driver that reached no report would go unnoticed"},
		{`FERN_CI_SIZE_GATE_STRICT: "1"`, "findings would warn into the same channel nobody reads"},
	} {
		if n := strings.Count(job, want.needle); n != 1 {
			t.Errorf("want the driver-sizes job to set %s exactly once, got %d — without it %s (#7519)", want.needle, n, want.why)
		}
		if n := strings.Count(src, want.needle); n != 1 {
			t.Errorf("%s is set in %d places; a second job setting it makes the enforcing one ambiguous", want.needle, n)
		}
	}
	step := enforcingStep(t, job)
	if strings.Contains(step, "continue-on-error") {
		t.Error("the aggregate size check carries continue-on-error, so a finding cannot fail the job — " +
			"the gate is advisory again and the drivers can grow unwatched (#7519)")
	}
}

func sliceBlock(src string, open, close *regexp.Regexp) string {
	start := open.FindStringIndex(src)
	if start == nil {
		return ""
	}
	rest := src[start[0]:]
	if end := close.FindStringIndex(rest[start[1]-start[0]:]); end != nil {
		return rest[:start[1]-start[0]+end[0]]
	}
	return rest
}

var (
	driverSizesKey = regexp.MustCompile(`(?m)^  driver-sizes:$`)
	anyJobKey      = regexp.MustCompile(`(?m)^  [a-zA-Z][\w-]*:$`)
	enforcingName  = regexp.MustCompile(`(?m)^      - name: check every baselined driver.*$`)
	anyStep        = regexp.MustCompile(`(?m)^      - `)
)

func driverSizesJob(t *testing.T, src string) string {
	t.Helper()
	job := sliceBlock(src, driverSizesKey, anyJobKey)
	if job == "" {
		t.Fatal("no `driver-sizes:` job — nothing compares the union of the per-job size reports (#7519)")
	}
	return job
}

func enforcingStep(t *testing.T, job string) string {
	t.Helper()
	step := sliceBlock(job, enforcingName, anyStep)
	if step == "" {
		t.Fatal("the driver-sizes job has no `check every baselined driver` step — the enforcing check is gone (#7519)")
	}
	return step
}
