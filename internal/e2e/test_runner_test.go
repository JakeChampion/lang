package e2e

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `std/test` ships as a pure-Lang unit-test runner the project
// plans to migrate to once the compiler is self-hosted (see
// docs/ROADMAP-AND-SELF-HOSTING.md and the project notes in
// CLAUDE.md). These tests pin the runner's contract:
//   - exit code is 0 when every case in a suite passes
//   - exit code is 1 when any case fails
//   - the output is TAP-13 with the standard `ok` /
//     `not ok` per-case lines and a `1..N` plan line
//
// They drive the example test files under `examples/tests/`
// through `lang -interp` rather than reimplementing the
// assertions on the Go side — that way the same examples that
// users see in the repo are also the regression gate, and any
// breakage in the runner shows up here.
//
// Each test computes the absolute path to the example with
// `filepath.Abs` so the test still works under
// `go test ./internal/e2e/...` (where Go sets cwd to the
// package dir, three deep from the project root).

// langSrcAbs joins the project root with the given relative
// path. The project root is two directories up from this
// test file (internal/e2e/ → internal/ → repo root).
func langSrcAbs(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", rel, err)
	}
	return abs
}

// runLangInterp runs `lang -interp <src>` and returns the
// exit code + stdout + stderr. Shared by the four runner
// tests below.
func runLangInterp(t *testing.T, bin, src string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode(), out.String(), errb.String()
}

// `examples/tests/arithmetic_test.lang` is the canonical
// "all cases pass" run. Exercises assert_eq_i32 +
// assert_{lt,le,gt,ge}_i32 + fail()/pass() and a hand-rolled
// table walk. Exit code is 0; the TAP plan line is `1..10`.
func TestRunnerArithmeticExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/arithmetic_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	wantSubstrings := []string{
		"TAP version 13",
		"# Suite: arithmetic",
		"ok 1 - addition",
		"ok 10 - greater or equal",
		"1..10",
		"# tests 10",
		"# pass 10",
		"# fail 0",
	}
	for _, w := range wantSubstrings {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/strings_test.lang` covers the string-method
// assertion helpers (contains, starts_with, ends_with, etc.).
// Passing suite → exit 0.
func TestRunnerStringsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/strings_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "# pass 11") || !strings.Contains(out, "# fail 0") {
		t.Errorf("expected 11 passes, 0 fails\noutput:\n%s", out)
	}
}

// `examples/tests/runner_self_test.lang` is the runner's own
// meta-test — confirms that every assertion helper returns the
// expected Option[string] shape on both pass and fail paths.
// If THIS regresses, the rest of the suite reports false
// positives.
func TestRunnerSelfTestPasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/runner_self_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("self-test exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	// 20 meta-tests; if this number changes intentionally,
	// update both the file and this expected count together.
	if !strings.Contains(out, "# pass 20") || !strings.Contains(out, "# fail 0") {
		t.Errorf("expected 20 passes, 0 fails\noutput:\n%s", out)
	}
}

// A failing suite exits 1 and emits `not ok` + a summary that
// names the failure. Inline-source so the failure cases stay
// adjacent to the assertions about them — a regression in the
// failure path would be invisible if it lived in a separate
// file the assertion didn't read.
func TestRunnerFailingSuiteExitsOne(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
function test_passing(): Option[string] {
    return assert_eq_i32(1 + 1, 2);
}

function test_failing(): Option[string] {
    return assert_eq_i32(2 + 2, 5);
}

function main(): i32 {
    var r: TestRunner = test_new("failure-shape");
    r = r.it("passing", test_passing());
    r = r.it("failing", test_failing());
    return r.finish();
}
`)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	gotOut := out.String()
	wantPieces := []string{
		"ok 1 - passing",
		"not ok 2 - failing",
		"  message: assert_eq_i32: expected 5, got 4",
		"# pass 1",
		"# fail 1",
		"# failures:",
		"failing: assert_eq_i32: expected 5, got 4",
	}
	for _, w := range wantPieces {
		if !strings.Contains(gotOut, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, gotOut)
		}
	}
}

// `examples/tests/skip_and_subsuites_test.lang` covers the
// skip / skip_if / subsuite / merge surface. Skips don't count
// as failures (exit 0) and the TAP stream stays monotonic
// across subsuite boundaries — the harness threads a base_idx
// through the child runner so the first subsuite case prints
// `ok 5` (not `ok 1` again) when the parent ran 4 cases first.
func TestRunnerSkipAndSubsuitesExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/skip_and_subsuites_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	want := []string{
		"ok 1 - top-level pass",
		"ok 2 - wasmtime-only # SKIP wasmtime not on $PATH",
		"ok 5 - arithmetic / addition",
		"ok 7 - arithmetic / multiplication # SKIP out of scope for this slice",
		"ok 10 - trailing top-level",
		"# tests 10",
		"# pass 7",
		"# fail 0",
		"# skip 3",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/fuzz_example_test.lang` exercises the
// `std/fuzz` harness on three benign properties (always-OK,
// non-negative length, idempotent to_upper) and one transform
// invariant (trim strips edge spaces). The seeds are arranged
// so the mutation path actually exercises the property — eg
// the to_upper-idempotent target includes mixed-case seeds so
// byte flips into / out of the upper range get tested.
func TestRunnerFuzzExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/fuzz_example_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - trivial",
		"ok 2 - len is non-negative",
		"ok 3 - to_upper idempotent",
		"ok 4 - trim strips edge spaces",
		"# pass 4",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// A fuzz target that detects a forbidden pattern in its
// seeds (`BAD seed`) must surface the failure with the
// offending input quoted so the failure log doubles as a
// reproducer. Inline source keeps the assertion adjacent to
// the target.
func TestRunnerFuzzFailureSurfacesInputReproducer(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
function detect_bad(input: string): Option[string] {
    if (input.contains("BAD")) { return Some("forbidden pattern"); }
    return None;
}

function main(): i32 {
    var r: TestRunner = test_new("fuzz-failure");
    r = r.fuzz("detect", ["good", "BAD seed", "another"], 5, detect_bad);
    return r.finish();
}
`)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s",
			code, out.String(), errb.String())
	}
	gotOut := out.String()
	for _, w := range []string{
		"not ok 1 - detect",
		`seed[1] = "BAD seed"`,
		"forbidden pattern",
		"# fail 1",
	} {
		if !strings.Contains(gotOut, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, gotOut)
		}
	}
}

// An empty-suite run still produces well-formed TAP — the
// `1..0` plan line and a `# tests 0` summary. Useful for
// scaffolding a new test file before the first case lands.
func TestRunnerEmptySuiteIsValidTAP(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
function main(): i32 {
    var r: TestRunner = test_new("empty");
    return r.finish();
}
`)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	gotOut := out.String()
	for _, w := range []string{"TAP version 13", "1..0", "# tests 0", "# pass 0", "# fail 0"} {
		if !strings.Contains(gotOut, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, gotOut)
		}
	}
}
