package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// runSizeCheck invokes scripts/ci-check-driver-sizes with `env` overrides and
// returns exit code plus combined output.
func runSizeCheck(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "ci-check-driver-sizes"))
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

// The 2026-08-06 figures from #6826, as a baseline.
const sizeBaseline0806 = `# tracked-artifact: fern.fern
fern.fern 134243612
asm_ir_run.fern 127504604
wasm_ir_run.fern 58870908
`

// The 2026-08-14 figures from #6826 — the +16% regression nothing measured.
const sizeReport0814 = "fern.fern\t155895596\nasm_ir_run.fern\t146081148\nwasm_ir_run.fern\t77378268\n"

// A run whose sizes match the baseline says so and flags nothing.
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
		// The all-clear has to carry its own denominator. Unqualified, it read
		// as whole-compiler cover over a fifth of the drivers (#7519).
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

// The size table is the "record it" half of #6826, so it has to reach the job
// summary — and stdout regardless, so the script is usable by hand.
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

// The #6826 incident itself: replaying the 08-14 sizes against the 08-06
// baseline must report the growth that went unnoticed for eight days.
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
		// The message must name the driver as well as the title, or a raw log
		// line reads "linked at 155.9 MB" with no subject.
		"::fern.fern linked at 155.9 MB against a baseline of 134.2 MB",
		"Corrected baseline",
		"fern.fern 155895596",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// A shrink is a finding too: an un-updated baseline after a win silently raises
// the ceiling the next regression is measured against.
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

// Drift under the tolerance is recorded in the table but is not a finding.
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

// A driver nobody baselined is measured but not compared, and one aggregate
// annotation asks for the entry — one per driver would drown the channel.
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

// Measuring nothing must not read as green: that is the failure mode the whole
// check exists to remove. Asserted on both channels, each under the environment
// that selects it.
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

// The escalation to a hard failure is one variable.
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

// #7519 itself: a report holding a fraction of the baselined drivers, every one
// of them within tolerance, used to end in an unqualified all-clear — so a
// five-row table read as the whole compiler while the drivers grew 2.3x. The
// slice is legitimate (each job links its own set); claiming to be everything
// is not.
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
	// The two it never weighed are not findings here — this caller was never
	// claiming to hold them — but the reader must not be told otherwise.
	if strings.Contains(out, "MISSING") {
		t.Errorf("a partial report must not report the drivers it never claimed:\n%s", out)
	}
	if strings.Contains(out, "::warning") {
		t.Errorf("a within-tolerance slice must not annotate:\n%s", out)
	}
}

// The union caller says so, and then an absent driver is a finding: nothing else
// can tell "this job linked five of fifteen" apart from "ten went unmeasured".
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
	// One annotation for the set, as with UNBASELINED — one per driver would
	// drown the channel this gate already lost once.
	if n := strings.Count(out, "::warning"); n != 1 {
		t.Errorf("want exactly 1 annotation for 2 unmeasured drivers, got %d:\n%s", n, out)
	}
	if strings.Contains(out, "Every driver measured here is within") {
		t.Errorf("an incomplete comparison must not end in an all-clear:\n%s", out)
	}
}

// The whole point of the escalation: the union job fails rather than warning.
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

// A complete report that really is complete stays quiet, or the enforcing job
// is red on every run and teaches the same lesson the warnings did.
func TestCheckDriverSizesCompleteModeQuietWhenWhole(t *testing.T) {
	base := writeTemp(t, "baseline.txt", sizeBaseline0806)
	report := writeTemp(t, "report.txt", "fern.fern\t134243612\nasm_ir_run.fern\t127504604\nwasm_ir_run.fern\t58870908\n")

	// GITHUB_ACTIONS is set so the annotation channel is the `::warning` form —
	// ciEnv strips it, and without it the "no ::warning" check below passes
	// against output that could never contain one.
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

// Measuring nothing is the loudest form of what this gate exists to catch, so
// the enforcing caller must fail on it rather than pass an empty comparison.
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

// The checked-in baseline has to be readable by the checker and has to name a
// tracked artifact — the whole point of #6826's third proposal is that "the
// self-host binary" means one specific file.
func TestSelfHostDriverSizeBaselineIsWellFormed(t *testing.T) {
	p := filepath.Join("..", "..", ".github", "selfhost-driver-sizes.txt")
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

// The workflow must actually export the report path, or the check runs blind on
// every run and says so forever.
func TestSelfHostWorkflowExportsDriverSizeReport(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "test-e2e-selfhost.yml"))
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	src := string(body)
	exports := strings.Count(src, "FERN_DRIVER_SIZE_REPORT:")
	checks := strings.Count(src, "scripts/ci-check-driver-sizes")
	if exports == 0 {
		t.Error("no job exports FERN_DRIVER_SIZE_REPORT, so nothing records a driver size")
	}
	// One check per measuring job — a job that measures without checking
	// records nothing — plus exactly one more over the union of their reports.
	// Every measuring job links a disjoint set, so its own check compares a
	// slice; only the aggregate weighs the fifteen baselined drivers as a set.
	if checks != exports+1 {
		t.Errorf("%d job(s) export FERN_DRIVER_SIZE_REPORT and %d invoke ci-check-driver-sizes — "+
			"want one check per measuring job plus one aggregate check over the union (#7519)", exports, checks)
	}
	// The aggregate is the only enforcing copy, and it is worth nothing without
	// both variables: COMPLETE turns an unmeasured driver into a finding,
	// STRICT turns findings into a failed job. Asserted on the `env:` spelling
	// so prose about them elsewhere in the file does not satisfy this, and
	// WITHIN the driver-sizes block so they cannot drift onto one of the
	// advisory per-job steps, where they would be counted and discarded.
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
	// The third leg of the escalation, and the one that silently undoes the
	// other two: a step whose exit status is discarded enforces nothing. The
	// job's download step carries continue-on-error legitimately, so this is
	// scoped to the check step rather than to the whole block.
	step := enforcingStep(t, job)
	if strings.Contains(step, "continue-on-error") {
		t.Error("the aggregate size check carries continue-on-error, so a finding cannot fail the job — " +
			"the gate is advisory again and the drivers can grow unwatched (#7519)")
	}
}

// sliceBlock returns the text from the first match of `open` up to the next
// match of `close` after it, or to the end. RE2 has no lookahead, so the two
// bounds are found separately rather than in one pattern.
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

// A job key is indented two spaces; everything inside one is indented deeper,
// so the next two-space key ends the block.
var (
	driverSizesKey = regexp.MustCompile(`(?m)^  driver-sizes:$`)
	anyJobKey      = regexp.MustCompile(`(?m)^  [a-zA-Z][\w-]*:$`)
	enforcingName  = regexp.MustCompile(`(?m)^      - name: check every baselined driver.*$`)
	anyStep        = regexp.MustCompile(`(?m)^      - `)
)

// driverSizesJob returns the body of the `driver-sizes:` job.
func driverSizesJob(t *testing.T, src string) string {
	t.Helper()
	job := sliceBlock(src, driverSizesKey, anyJobKey)
	if job == "" {
		t.Fatal("no `driver-sizes:` job — nothing compares the union of the per-job size reports (#7519)")
	}
	return job
}

// enforcingStep returns the body of the step that runs the complete check.
func enforcingStep(t *testing.T, job string) string {
	t.Helper()
	step := sliceBlock(job, enforcingName, anyStep)
	if step == "" {
		t.Fatal("the driver-sizes job has no `check every baselined driver` step — the enforcing check is gone (#7519)")
	}
	return step
}
