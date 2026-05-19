package e2e

import (
	"bytes"
	"os"
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

// `examples/tests/process_assertions_test.lang` exercises the
// `subprocess(cmd, args, stdin) -> ProcessResult` builtin and
// the assert_exit / assert_stdout_eq / assert_process /
// assert_stderr_contains family layered on top of it. The
// fixture itself self-skips when `sh` isn't on $PATH (rare on
// the Linux + macOS targets Lang supports, but possible in
// stripped CI images) — so this gate accepts both the all-pass
// outcome (POSIX tools available) and the all-skip outcome.
//
// The temp_dir + subprocess interplay case writes a fixture
// to disk and runs `cat` against it, which is the smallest
// end-to-end shape the e2e suite migration needs: spawn the
// compiler binary, point it at a tempdir-built input, assert
// on its output.
func TestRunnerProcessAssertionsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/process_assertions_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if strings.Contains(out, "# SKIP sh / echo / cat not on $PATH") {
		// CI image without POSIX shell — accept the skip, no
		// further assertions to make.
		return
	}
	for _, w := range []string{
		"ok 1 - echo stdout eq",
		"ok 6 - spawn missing binary",
		"ok 7 - temp_dir writes + reads",
		"# pass 7",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/wide_numerics_test.lang` covers the i64 /
// u32 / u64 assertion family. The corresponding i32 helpers
// are pinned by `arithmetic_test.lang`; this exercises the
// wider widths so a regression in the interp's
// `__int_to_string_u64` override (the one Lang code in
// `core/int.lang` whose body the interp can't run) would
// surface here.
func TestRunnerWideNumericsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/wide_numerics_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - i64 addition",
		"ok 3 - u32 max",
		"ok 4 - u64 max",
		"# pass 5",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/filesystem_ops_test.lang` exercises the
// `read_dir` / `remove_file` / `remove_dir_all` builtins and
// pins the matching semantics for each — particularly the
// "missing target" cases where remove_file is an error
// (matches Go's `os.Remove`) but remove_dir_all is silently
// OK (matches `os.RemoveAll`).
func TestRunnerFilesystemOpsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/filesystem_ops_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - temp_dir returns absolute path",
		"ok 2 - read_dir lists what we wrote",
		"ok 4 - remove_file on missing target errors",
		"ok 5 - remove_dir_all on missing target is ok",
		"# pass 5",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `defer_cleanup(path)` is the runner-level hook for the
// `temp_dir(...)` lifecycle: register the path immediately,
// run the test against it, and `finish()` calls
// `remove_dir_all` on every registered path before the
// process exits. We verify this end-to-end by:
//   1. running an inline test program that creates a fresh
//      temp dir, writes a file into it, registers the dir
//      for cleanup, then runs an assertion against the file
//   2. confirming exit=0 + the expected TAP output
//   3. confirming the directory no longer exists on the host
//      filesystem afterward (cleanup actually fired)
//
// We don't pin the exact tempdir path — `os.MkdirTemp`
// picks a random suffix — but we DO grep the test output
// for the printed path so the post-cleanup check has a
// concrete target.
func TestRunnerDeferCleanupRunsAtFinish(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
function main(): i32 {
    var r: TestRunner = test_new("cleanup");
    match (temp_dir("lang-cleanup-probe")) {
        Ok(dir) => {
            print("# tempdir: " + dir);
            r = r.defer_cleanup(dir);
            match (write_file(dir + "/x.txt", "x")) {
                None => { },
                Some(_) => { r = r.it("write", fail("write failed")); return r.finish(); }
            }
            match (read_file(dir + "/x.txt")) {
                Ok(s) => { r = r.it("roundtrip", assert_eq_string(s, "x")); },
                Err(_) => { r = r.it("roundtrip", fail("read failed")); }
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
	// Pull the printed tempdir path back out of the stream
	// so we can probe the host filesystem after `finish()`
	// returns. The line shape is "# tempdir: <abs path>\n".
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
	// Cleanup hook should have removed the directory before
	// the lang process exited. Stat the path: a NotExist
	// error is the success signal, anything else is a leak.
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Errorf("defer_cleanup did not remove %q (stat err = %v)", tempPath, err)
	}
}

// `examples/tests/lang_binary_e2e_test.lang` is the canonical
// migration-pattern example: a Lang test file spawns the
// `lang` binary itself (path read from `$LANG_BIN`), drives
// it through `-interp` / `-check` against inline source +
// tempdir fixtures, and asserts on the (exit, stdout,
// stderr) triple. This is the shape the migrated Go-side
// e2e suite will adopt.
//
// We exercise both paths the example handles:
//   1. With `$LANG_BIN` pointing at a fresh build of the
//      compiler — every case runs and passes.
//   2. With `$LANG_BIN` unset — the suite skips cleanly
//      rather than failing, so dev laptops without an
//      explicit env setup don't see false negatives.
func TestRunnerLangBinaryE2EExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/lang_binary_e2e_test.lang")

	t.Run("with LANG_BIN set", func(t *testing.T) {
		cmd := exec.Command(bin, "-interp", src)
		cmd.Env = append(os.Environ(), "LANG_BIN="+bin)
		var out, errb bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errb
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s",
				code, out.String(), errb.String())
		}
		gotOut := out.String()
		for _, w := range []string{
			"ok 1 - interp returns exit code",
			"ok 3 - check passes clean program",
			"ok 4 - check rejects type error",
			"ok 5 - interp reads a file",
			"# pass 5",
			"# fail 0",
		} {
			if !strings.Contains(gotOut, w) {
				t.Errorf("stdout missing %q\nfull output:\n%s", w, gotOut)
			}
		}
	})

	t.Run("without LANG_BIN — skips cleanly", func(t *testing.T) {
		cmd := exec.Command(bin, "-interp", src)
		// Drop LANG_BIN from the environment explicitly —
		// the parent process may have it set.
		env := []string{}
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "LANG_BIN=") {
				env = append(env, kv)
			}
		}
		cmd.Env = env
		var out, errb bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errb
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Fatalf("exit = %d, want 0 (skip, not fail)\nstdout: %s\nstderr: %s",
				code, out.String(), errb.String())
		}
		gotOut := out.String()
		for _, w := range []string{
			"# SKIP LANG_BIN not set",
			"# skip 1",
		} {
			if !strings.Contains(gotOut, w) {
				t.Errorf("stdout missing %q\nfull output:\n%s", w, gotOut)
			}
		}
		if strings.Contains(gotOut, "# fail 1") {
			t.Errorf("missing LANG_BIN should skip, not fail\noutput:\n%s", gotOut)
		}
	})
}

// `examples/tests/helpers_test.lang` covers the convenience
// helpers layered on top of the base assertion family:
// multi-substring (`contains_all` / `contains_any` /
// `contains_in_order`), string-diff (`assert_eq_string_diff`
// reports the first differing line + line number), numeric
// range (`assert_in_range_i32` / `_i64`), file-state
// (`assert_file_exists` / `_not_exists` / `_contents` /
// `_contains`), and the tempdir-tuple combinator
// `must_temp_dir(r, prefix)`.
//
// Every helper has a "passes when used right" case and an
// "inverted" case that verifies the failure message embeds
// the right context — e.g. `assert_contains_all` failure
// names the FIRST missing needle, `assert_eq_string_diff`
// names the line number where the values diverge.
func TestRunnerHelpersExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/helpers_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - contains_all pass",
		"ok 2 - contains_all names missing",
		"ok 5 - contains_in_order rejects reorder",
		"ok 8 - in_range_i32 below fails",
		"ok 11 - string diff localises line",
		"ok 15 - file contents eq",
		"# pass 15",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/float_test.lang` pins the f32 / f64
// assertion family + the underlying interp Float support.
// Before this work the interp errored out on `*ast.FloatLit`,
// which made float-touching code impossible to unit-test
// without compiling to a backend. Now `lang -interp` handles
// float arithmetic, comparison, casts, and the f32_bits /
// f32_from_bits reinterpret pair.
//
// Eleven cases cover: tolerance-equal vs exact-equal,
// f32 precision-loss tolerance (the 0.1+0.1+0.1 != 0.3
// textbook example), NaN detection + the NaN-unequal-to-
// itself property, ±0.0, ±Inf, and f32_bits round-trips.
func TestRunnerFloatExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/float_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - f64 addition",
		"ok 4 - f32 precision loss tolerance",
		"ok 6 - NaN detection",
		"ok 8 - f32_bits gives expected pattern",
		"ok 11 - Inf vs Inf",
		"# pass 11",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/fuzz_shrink_test.lang` exercises the
// `r.fuzz_shrink` receiver method on three benign properties
// (no failures expected) — the harness's mutation loop runs
// each one through `fuzz_default_iterations()` mutated
// variants. The shrinker only kicks in on a failure, so
// this gate confirms the no-failure path stays clean +
// returns exit 0.
func TestRunnerFuzzShrinkExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/fuzz_shrink_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - len non-negative",
		"ok 2 - reverse_bytes idempotent",
		"ok 3 - to_lower has no uppercase",
		"# pass 3",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// The contract that justifies the shrinking layer: when a
// fuzz target fails on a wild input, the harness MUST
// minimise the input to a small reproducer before reporting.
// We give it a target that fails on "BAD" anywhere in the
// input, seed it with a long string containing "BAD" buried
// in padding, and check that the failure message reports
// the minimised form ("BAD") alongside the raw failing
// input (the padded original).
//
// The minimised form must be 3 bytes (`BAD`) — the
// shrinker drops every other byte. If it doesn't reach
// that minimum, the regression is in the
// single-byte-drop / halving pass.
func TestRunnerFuzzShrinkSurfacesMinimisedInput(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
function detect_bad(input: string): Option[string] {
    if (input.contains("BAD")) { return Some("forbidden"); }
    return None;
}

function main(): i32 {
    var r: TestRunner = test_new("shrink-failure");
    r = r.fuzz_shrink("detect",
                      ["lots of padding here BAD lots more padding"],
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

// `examples/tests/batch7_test.lang` is the omnibus example
// for the seventh test-runner-migration tranche: wider-int
// relational asserts (lt / le / gt / ge on i64 / u32 / u64),
// `f64_bits` / `f64_from_bits`, the `stat(...)` builtin +
// file-state helpers (`assert_is_file` / `_is_dir` /
// `_file_size`), and `assert_json_eq` for order-independent
// JSON comparison.
//
// Nineteen cases — every helper exercised in both directions
// where applicable. The JSON cases verify the key-order-
// independence contract that's the main reason to reach for
// `assert_json_eq` over a plain `assert_eq_string` on the
// serialized output.
func TestRunnerBatch7Example(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/batch7_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - i64 lt",
		"ok 8 - f64_bits gives expected pattern",
		"ok 10 - is_file true",
		"ok 12 - file_size matches",
		"ok 16 - json key order independent",
		"ok 19 - json invalid actual fails",
		"# pass 19",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/batch8_test.lang` exercises the additions
// from the eighth tranche: argv passthrough,
// `--filter PATTERN` selection via `parse_filter_from_args` +
// `test_new_filtered`, golden-file assertions, and Map
// receiver assertions.
//
// Two t.Run subtests pin both the unfiltered path (all nine
// cases run + pass) and the filtered path (only the map-
// related cases run, the others convert to skips with reason
// "filtered out").
func TestRunnerBatch8Example(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/batch8_test.lang")

	t.Run("unfiltered", func(t *testing.T) {
		code, out, errOut := runLangInterp(t, bin, src)
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s",
				code, out, errOut)
		}
		for _, w := range []string{
			"ok 1 - argv populated",
			"ok 5 - map has i32-i32",
			"# golden file bootstrapped at",
			"ok 7 - bootstrap golden",
			"ok 9 - strict missing fails",
			"# pass 9",
			"# fail 0",
		} {
			if !strings.Contains(out, w) {
				t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
			}
		}
	})

	t.Run("filtered to map cases", func(t *testing.T) {
		cmd := exec.Command(bin, "-interp", src, "--", "--filter", "map")
		var out, errb bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errb
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s",
				code, out.String(), errb.String())
		}
		gotOut := out.String()
		for _, w := range []string{
			"argv populated # SKIP filtered out (--filter=map)",
			"ok 5 - map has i32-i32",
			"strict missing fails # SKIP filtered out (--filter=map)",
			"# pass 5",
			"# skip 4",
		} {
			if !strings.Contains(gotOut, w) {
				t.Errorf("stdout missing %q\nfull output:\n%s", w, gotOut)
			}
		}
	})
}

// `examples/tests/float_math_test.lang` exercises the f64
// math primitives (sqrt / pow / abs / floor / ceil / round /
// trunc / log / exp / sin / cos) added to std/float, plus
// the IEEE-754 classification helpers (is_nan / is_finite /
// is_inf) and min / max / clamp. The f32 receiver path also
// gets a single round-trip case to confirm the
// promote-to-f64 / apply / demote wrappers work.
//
// Eighteen cases — every method tested with at least one
// concrete value + a "round-trip" / "identity" property
// where applicable (exp(log(x))==x, sin^2+cos^2==1).
func TestRunnerFloatMathExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/float_math_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - abs(negative)",
		"ok 7 - sqrt(2)",
		"ok 9 - exp(log(x)) round-trips",
		"ok 12 - sin^2 + cos^2 = 1",
		"ok 13 - is_nan",
		"ok 15 - is_inf (both signs)",
		"ok 18 - f32 sqrt round-trip",
		"# pass 18",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/timing_test.lang` exercises the time
// builtins (`now_unix_ms`, `monotonic_ns`, `sleep_ms`) and
// the elapsed-time assertion helpers (`assert_elapsed_lt_ms`
// / `_us`). Six cases — the failure-message case verifies
// the contract that the diagnostic embeds both the observed
// elapsed time AND the deadline so a flaky-bench failure
// preserves enough state to decide whether to bump the
// bound.
func TestRunnerTimingExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/timing_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - now_unix_ms advances",
		"ok 3 - sleep_ms actually sleeps",
		"ok 5 - elapsed failure embeds context",
		"# pass 6",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
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
