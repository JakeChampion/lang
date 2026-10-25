package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func runLangInterp(t *testing.T, bin, src string) (int, string, string) {
	t.Helper()
	t.Parallel()
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode(), out.String(), errb.String()
}

func TestRunnerSimdExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/simd_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: simd", "ok 18 - sweep against std/f64", "ok 23 - lane Math against the scalar", "1..23", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerLazyExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/runner_lazy_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	wantSubstrings := []string{
		"ok 1 - dropped by the filter # SKIP filtered out",
		"ok 2 - checks the dropped body never ran",
		"ok 4 - checks the kept body ran once",
		"# pass 3",
		"# fail 0",
		"# skip 1",
	}
	for _, w := range wantSubstrings {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerRegexExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/regex_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/regex", "# pass 22", "# fail 0", "1..22"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerCmpHelpersExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/cmp_helpers_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: core/cmp helpers", "# pass 19", "# fail 0", "1..19"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerU32ExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/u32_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/u32 helpers", "# pass 4", "# fail 0", "1..4"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerUintRangeExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/uint_range_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/uint range", "# pass 7", "# fail 0", "1..7"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerStringFindAllExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/string_find_all_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/string find_all", "# pass 7", "# fail 0", "1..7"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerTimeRelativeExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/time_relative_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/time relative_to", "# pass 4", "# fail 0", "1..4"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerEqBoundDeriveExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/eq_bound_derive_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: Eq-bound verbs on a derived element", "# pass 10", "# fail 0", "1..10"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerCmpNanTotalOrderExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/cmp_nan_total_order_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: core/cmp NaN total order", "# pass 11", "# fail 0", "1..11"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerFailingSuiteExitsOne(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
import "std/test";

function test_passing(): test.TestOutcome {
    return test.assert_eq(1 + 1, 2);
}

function test_failing(): test.TestOutcome {
    return test.assert_eq(2 + 2, 5);
}

function main(): i32 {
    let r: test.TestRunner = test.test_new("failure-shape");
    r = r.it("passing", () => test_passing());
    r = r.it("failing", () => test_failing());
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
		"  message: assert_eq: expected \"5\", got \"4\"",
		"# pass 1",
		"# fail 1",
		"# failures:",
		"failing: assert_eq: expected \"5\", got \"4\"",
	}
	for _, w := range wantPieces {
		if !strings.Contains(gotOut, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, gotOut)
		}
	}
}

func runDroppedHandleCase(t *testing.T, src string, want []string) {
	t.Helper()
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Fatalf("exit = 0, want non-zero (a printed `not ok` must fail the run)\nstdout: %s\nstderr: %s",
			out.String(), errb.String())
	}
	got := out.String()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, got)
		}
	}
}

const droppedHandlePreamble = `
import "std/test";

function failing(): test.TestOutcome { return test.assert_eq(1, 2); }
function passing(): test.TestOutcome { return test.assert_eq(1, 1); }
`

func TestRunnerDroppedItResultStillFailsSuite(t *testing.T) {
	runDroppedHandleCase(t, droppedHandlePreamble+`
function main(): i32 {
    let r: test.TestRunner = test.test_new("dropped-it");
    r = r.it("kept", passing);
    r.it("dropped result", failing);
    return r.finish();
}
`, []string{
		"ok 1 - kept",
		"not ok 2 - dropped result",
		"  message: assert_eq: expected \"2\", got \"1\"",
		"1..2",
		"# tests 2",
		"# pass 1",
		"# fail 1",
		"# WARNING: 1 result(s) were discarded",
	})
}

func TestRunnerUnusedSubsuiteHandleStillFailsSuite(t *testing.T) {
	runDroppedHandleCase(t, droppedHandlePreamble+`
function main(): i32 {
    let r: test.TestRunner = test.test_new("unused-subsuite");
    r = r.it("kept", passing);
    let sub: test.TestRunner = r.subsuite("child");
    sub = sub.it("child fails", failing);
    return r.finish();
}
`, []string{
		"ok 1 - kept",
		"not ok 2 - child / child fails",
		"  message: assert_eq: expected \"2\", got \"1\"",
		"1..2",
		"# tests 2",
		"# pass 1",
		"# fail 1",
		"# WARNING: 1 result(s) were discarded",
	})
}

func TestRunnerDroppedItAndUnusedSubsuiteTogether(t *testing.T) {
	runDroppedHandleCase(t, droppedHandlePreamble+`
function main(): i32 {
    let r: test.TestRunner = test.test_new("s");
    r.it("dropped result", failing);
    let sub: test.TestRunner = r.subsuite("child");
    sub = sub.it("child fails", failing);
    return r.finish();
}
`, []string{
		"not ok 1 - dropped result",
		"not ok 2 - child / child fails",
		"1..2",
		"# tests 2",
		"# pass 0",
		"# fail 2",
		"# WARNING: 2 result(s) were discarded",
	})
}

func TestRunnerUtf8ValidityProperty(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/utf8_validity_property_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - byte-count operations snap to boundaries",
		"ok 2 - s[a:b] Some payload is always valid",
		"ok 3 - width padding keeps validity",
		"ok 4 - case and style conversion keeps validity",
		"ok 5 - splitting and trimming keep validity",
		"# pass 5",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerFuzzFailureSurfacesInputReproducer(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
import "std/fuzz";
import "std/test";
import "std/string";

function detect_bad(input: u8[]): test.TestOutcome {
    if (string_from_bytes_unchecked(input).contains("BAD")) { return test.fail("forbidden pattern"); }
    return test.pass();
}

function main(): i32 {
    let r: test.TestRunner = test.test_new("fuzz-failure");
    r = r.fuzz("detect", ["good".bytes(), "BAD seed".bytes(), "another".bytes()], 5, detect_bad);
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

func TestRunnerDeferCleanupRunsAtFinish(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
import "std/test";

function main(): i32 {
    let r: test.TestRunner = test.test_new("cleanup");
    match (temp_dir("fern-cleanup-probe")) {
        Ok(dir) => {
            print("# tempdir: " + dir);
            r = r.defer_cleanup(dir);
            match (write_file(dir + "/x.txt", "x")) {
                Ok(_) => { },
                Err(_) => { r = r.it("write", () => test.fail("write failed")); return r.finish(); }
            }
            match (read_file(dir + "/x.txt")) {
                Ok(s) => { r = r.it("roundtrip", () => test.assert_eq(s, "x")); },
                Err(_) => { r = r.it("roundtrip", () => test.fail("read failed")); }
            }
        },
        Err(_) => { r = r.skip("setup", "temp_dir failed"); }
    }
    return r.finish();
}
`)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s",
			code, out.String(), errb.String())
	}
	gotOut := out.String()
	if !strings.Contains(gotOut, "ok 1 - roundtrip") {
		t.Errorf("expected roundtrip case in output:\n%s", gotOut)
	}
	const marker = "# tempdir: "
	mi := strings.Index(gotOut, marker)
	if mi < 0 {
		t.Fatalf("could not locate `%s` marker in output:\n%s", marker, gotOut)
	}
	rest := gotOut[mi+len(marker):]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	tempPath := strings.TrimSpace(rest)
	if tempPath == "" {
		t.Fatal("empty tempdir path parsed from output")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Errorf("defer_cleanup did not remove %q (stat err = %v)", tempPath, err)
	}
}

func TestRunnerFuzzShrinkSurfacesMinimisedInput(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
import "std/fuzz";
import "std/test";
import "std/string";

function detect_bad(input: u8[]): test.TestOutcome {
    if (string_from_bytes_unchecked(input).contains("BAD")) { return test.fail("forbidden"); }
    return test.pass();
}

function main(): i32 {
    let r: test.TestRunner = test.test_new("shrink-failure");
    r = r.fuzz_shrink("detect",
                      ["lots of padding here BAD lots more padding".bytes()],
                      5, detect_bad);
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
		"raw     (42 bytes)",
		`shrunk  (3 bytes): "BAD"`,
	} {
		if !strings.Contains(gotOut, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, gotOut)
		}
	}
}

func TestRunnerHttpResponseBodyShimExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/http_response_body_shim_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"# Suite: HTTP response body shim",
		"ok 1 - body_string round-trips",
		"ok 2 - body_len matches the string length",
		"ok 5 - bytes constructor agrees with text",
		"ok 7 - wire Content-Length matches the body",
		"ok 8 - wire ends with the body",
		"# pass 8",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerHttpStatusExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/http_status_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/http status", "1..4", "# pass 4", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerSetExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/set_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/set", "ok 4 - insert is pure", "1..14", "# pass 14", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func runPersistentSuite(t *testing.T, path, suite, persistCase string, n int) {
	t.Helper()
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, path)
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	plan := fmt.Sprintf("1..%d", n)
	pass := fmt.Sprintf("# pass %d", n)
	for _, w := range []string{"# Suite: " + suite, persistCase, plan, pass, "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerNdarrayExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/ndarray_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/ndarray", "1..18", "# pass 18", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerUnicodeExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/unicode_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/unicode", "1..18", "# pass 18", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerSemverExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/semver_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/semver", "1..8", "# pass 8", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerGlobExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/glob_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/glob", "1..7", "# pass 7", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerDotenvExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/dotenv_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/dotenv", "1..7", "# pass 7", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerRandExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/rand_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/rand", "1..6", "# pass 6", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerBase32ExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/base32_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/base32", "1..6", "# pass 6", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerStrdistExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/strdist_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/strdist", "1..4", "# pass 4", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerTableExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/table_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/table", "1..6", "# pass 6", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerTextwrapExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/textwrap_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/textwrap", "1..6", "# pass 6", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerFormatDurationParseExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/format_duration_parse_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/format parse_duration_ms", "1..4", "# pass 4", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerAnsiExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/ansi_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/ansi", "1..6", "# pass 6", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerEmptySuiteIsValidTAP(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
import "std/test";

function main(): i32 {
    let r: test.TestRunner = test.test_new("empty");
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

func TestRunnerPegExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/peg_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "# pass 19") || !strings.Contains(out, "# fail 0") {
		t.Errorf("expected 19 passes, 0 fails\noutput:\n%s", out)
	}
}

func TestRunnerDiscardedResultCannotReportSuccess(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
import "std/test";

function failing(): test.TestOutcome { return test.assert_eq(1, 2); }

function main(): i32 {
    let r: test.TestRunner = test.test_new("discarded");
    r.it("dropped result", failing);
    let sub: test.TestRunner = r.subsuite("child");
    sub = sub.it("child fails", failing);
    return r.finish();
}
`)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		t.Fatalf("exit = 0 for a suite that printed two `not ok` lines\nstdout: %s\nstderr: %s", out.String(), errb.String())
	}
	gotOut := out.String()
	wantPieces := []string{
		"not ok 1 - dropped result",
		"not ok 2 - child / child fails",
		"1..2",
		"# tests 2",
		"# fail 2",
		"result(s) were discarded",
	}
	for _, w := range wantPieces {
		if !strings.Contains(gotOut, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, gotOut)
		}
	}
	if strings.Contains(gotOut, "# fail 0") {
		t.Errorf("reported `# fail 0` with failures printed\nfull output:\n%s", gotOut)
	}
}

func TestRunnerCorrectSuiteUnaffectedByDiscardTracking(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
import "std/test";

function passing(): test.TestOutcome { return test.assert_eq(1, 1); }

function main(): i32 {
    let r: test.TestRunner = test.test_new("clean");
    r = r.it("first", passing);
    let sub: test.TestRunner = r.subsuite("child");
    sub = sub.it("nested", passing);
    r = r.merge(sub);
    r = r.it("last", passing);
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
	for _, w := range []string{
		"ok 1 - first",
		"ok 2 - child / nested",
		"ok 3 - last",
		"1..3",
		"# pass 3",
		"# fail 0",
	} {
		if !strings.Contains(gotOut, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, gotOut)
		}
	}
	if strings.Contains(gotOut, "discarded") {
		t.Errorf("a correctly written suite reported discarded results\nfull output:\n%s", gotOut)
	}
}

func TestRunnerCoreutilsResolvExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/coreutils_resolv_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: coreutils/lib/resolv", "1..9", "# pass 9", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerCoreutilsPwdbExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/coreutils_pwdb_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: coreutils/lib/pwdb + lib/utmp", "1..15", "# pass 15", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerCoreutilsSelinuxExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/coreutils_selinux_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: coreutils/lib/selinux", "1..7", "# pass 7", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerCoreutilsTimefmtExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/coreutils_timefmt_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: coreutils/lib/timefmt", "1..4", "# pass 4", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerCoreutilsLongDoubleExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/coreutils_ld_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: coreutils/lib/ld", "1..23", "# pass 23", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}
