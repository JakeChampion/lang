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

// `examples/tests/lines_log_test.lang` exercises the
// `assert_lines_eq(actual, expected_lines)` helper +
// `(r).log(msg)` chainable TAP-comment emitter. Four cases
// + interleaved log breadcrumbs verify both the matching
// and the line-count-mismatch / first-diff-line localisation
// paths.
func TestRunnerLinesLogExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/lines_log_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"# phase 1: positive cases",
		"# phase 2: failure cases (inverted)",
		"ok 1 - lines match",
		"ok 3 - line count mismatch reports",
		"ok 4 - first diff line localised",
		"# pass 4",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/fuzz_corpus_test.lang` exercises the
// `fuzz_corpus_from_dir` + `fuzz_corpus_from_dir_or` helpers
// that load seed corpora from disk. Six cases cover the
// loaded-seeds path, the fallback paths (missing directory
// + empty directory), and a smoke test for the new fuzz
// mutators (bit flip / byte duplicate / byte zero / byte max).
func TestRunnerFuzzCorpusExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/fuzz_corpus_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - loads three seeds (skips _-prefixed)",
		"ok 4 - missing corpus dir falls back",
		"ok 5 - empty corpus falls back",
		"ok 6 - new mutators don't crash",
		"# pass 6",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/bench_test.lang` exercises the bench
// harness: `r.bench(name, iter, fn)` reports timing as a TAP
// comment and always passes; `r.bench_max_us(name, iter, fn,
// budget)` fails when the median exceeds the budget. We
// verify both the comment shape (min / median / mean / max
// fields) and the budgeted case's pass path.
func TestRunnerBenchExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/bench_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"# bench tiny arithmetic loop",
		"min=", "median=", "mean=", "max=",
		"ok 1 - tiny arithmetic loop",
		"budget=1000000us",
		"ok 3 - tiny loop under 1s budget",
		"# pass 3",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/array_reductions_test.lang` exercises the
// new wider-int / float array reductions added as free
// functions to std/array: `sum_i64` / `max_i64` / `min_i64` /
// `avg_i64`, `sum_u32` / `max_u32` / `min_u32`,
// `sum_u64` / `max_u64` / `min_u64`, `sum_f64` / `max_f64` /
// `min_f64` / `avg_f64`. Eleven cases cover the happy path
// + empty input semantics + the near-u64-max unsigned-
// compare correctness check.
func TestRunnerArrayReductionsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/array_reductions_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - sum_i64",
		"ok 5 - max_i64 empty returns None",
		"ok 8 - max_u64 unsigned semantics",
		"ok 11 - avg_f64",
		"# pass 11",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/set_eq_test.lang` exercises the order-
// independent (multiset) array assertions:
// `assert_set_eq_i32` / `_string` and `assert_subset_i32` /
// `_string`. Ten cases cover passing, reversed order,
// duplicate-multiplicity requirements (multiset semantics),
// length mismatches, and the vacuous "empty subset of X"
// case.
func TestRunnerSetEqExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/set_eq_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 2 - set_eq i32 reversed",
		"ok 3 - set_eq i32 multiset matches",
		"ok 4 - set_eq i32 multiset mismatch fails",
		"ok 8 - subset i32 multiplicity required",
		"ok 10 - subset empty is vacuous",
		"# pass 10",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/env_unreachable_test.lang` exercises the
// `assert_env_set` / `_unset` / `_eq` env-var assertion
// family and `unreachable(label)`. Five cases — every
// helper exercised in both directions where applicable,
// plus the failure-message context checks.
func TestRunnerEnvUnreachableExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/env_unreachable_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - PATH is set",
		"ok 3 - env_set failure message names var",
		"ok 5 - unreachable() embeds label + prefix",
		"# pass 5",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/rel_tol_and_ms_bench_test.lang` exercises
// the two batch-18 additions to std/test:
//   - `assert_eq_f64_rel(actual, expected, rel_tol)` /
//     `assert_eq_f32_rel` — relative-tolerance float
//     compare, for test data whose magnitudes span many
//     orders of magnitude.
//   - `(r).bench_max_ms(name, iter, fn, budget_ms)` —
//     millisecond-budget wrapper around `bench_max_us`.
//
// Nine assertion cases plus one ms-budget bench. We pin the
// pass/fail counts and a couple of representative TAP lines
// so a regression in either helper surfaces immediately.
func TestRunnerRelTolAndMsBenchExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/rel_tol_and_ms_bench_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - rel_tol passes within ratio",
		"ok 3 - rel_tol scale invariance",
		"ok 4 - rel_tol zero expected falls back to abs",
		"ok 6 - rel_tol NaN actual fails",
		"ok 9 - rel_tol f32 mirror",
		"# bench tiny loop under 1000ms budget",
		"budget=1000000us",
		"ok 10 - tiny loop under 1000ms budget",
		"# pass 10",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/sorted_unique_range_test.lang` exercises
// the batch-19 additions to std/test:
//   - `assert_in_range_f64(v, lo, hi)` — inclusive float
//     range; NaN always fails
//   - `assert_sorted_asc_i32` / `_string` — monotonically
//     non-decreasing; empty / single-element arrays are
//     vacuously sorted; failure embeds the inversion index
//   - `assert_unique_i32` / `_string` — sort-then-walk
//     uniqueness check; failure embeds the offending value
//
// 19 cases total covering inclusive bounds, NaN guards,
// inversion-index reporting, multi-magnitude inputs, and
// the vacuous empty / single-element cases.
func TestRunnerSortedUniqueRangeExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/sorted_unique_range_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 4 - in_range_f64 below names bound",
		"ok 6 - in_range_f64 NaN fails",
		"ok 10 - sorted_asc_i32 equal runs ok",
		"ok 11 - sorted_asc_i32 inversion + index",
		"ok 15 - unique_i32 unsorted input",
		"ok 18 - unique_string duplicate fails",
		"# pass 19",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/float_array_strict_sort_test.lang`
// exercises the batch-20 additions:
//   - `assert_eq_f64_array_near` / `_f32_array_near` —
//     element-wise float array compare with tolerance.
//     NaN anywhere fails; length-mismatch is its own
//     diagnostic; mismatched element reports the index.
//   - `assert_sorted_desc_i32` / `_string` — descending
//     order verification.
//   - `assert_strictly_sorted_asc_i32` / `_string` —
//     strict monotonic (sorted AND unique), rejecting
//     equal adjacent pairs that the non-strict variant
//     accepts.
//
// 15 cases.
func TestRunnerFloatArrayStrictSortExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/float_array_strict_sort_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 3 - f64 array outside tolerance + idx",
		"ok 4 - f64 array length mismatch",
		"ok 5 - f64 array NaN fails",
		"ok 6 - f32 array near pass",
		"ok 9 - sorted_desc_i32 inversion + index",
		"ok 12 - strict_asc_i32 rejects equal pair",
		"ok 15 - strict_asc_string rejects equal",
		"# pass 15",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/map_eq_and_predicates_test.lang` exercises
// batch-21 additions:
//   - `assert_eq_map_i32_i32` / `_string_string` — full
//     map deep equality (length + key-with-matching-value
//     in one direction; pigeonhole gives the reverse). Map
//     iteration order isn't observable so walks
//     `actual.keys()` rather than `iter`.
//   - `assert_all_i32` / `_string` — ∀ predicate; vacuous
//     pass on empty array. Failure names index + value.
//   - `assert_any_i32` / `_string` — ∃ predicate; vacuous
//     FAIL on empty array (mathematical convention). The
//     test takes a `(T) => boolean` lambda or named fn.
//
// 16 cases total.
func TestRunnerMapEqAndPredicatesExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/map_eq_and_predicates_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - map i32 eq pass (order-indep)",
		"ok 3 - map i32 eq value mismatch + key",
		"ok 4 - map i32 eq disjoint same-len fails",
		"ok 9 - all_i32 inline lambda",
		"ok 10 - all_i32 fail names index + value",
		"ok 11 - all_i32 empty vacuous",
		"ok 14 - any_i32 empty fails",
		"# pass 16",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/wider_array_contains_count_test.lang`
// exercises batch-22 additions:
//   - `assert_eq_i64_array` / `_u32_array` / `_u64_array`
//     — wider-int element-wise array equality (i32 variant
//     was already there).
//   - `assert_array_contains_i32` / `_string` plus the
//     negative `_not_contains_*` mirrors — typed-array
//     membership, complementing the existing
//     `assert_contains(haystack: string, needle: string)`.
//   - `assert_count_i32(arr, pred, n)` / `_string` —
//     exact-cardinality predicate match; sits between
//     `assert_all` (every) and `assert_any` (at least one).
//
// 20 cases.
func TestRunnerWiderArrayContainsCountExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/wider_array_contains_count_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 3 - eq_i64_array reports index",
		"ok 5 - eq_u64_array pass",
		"ok 9 - array_contains_i32 missing",
		"ok 10 - array_contains_i32 empty fails",
		"ok 13 - array_not_contains_i32 fail+idx",
		"ok 17 - count_i32 zero matches",
		"ok 19 - count_i32 mismatch reports",
		"# pass 20",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/one_of_none_of_test.lang` exercises
// batch-23 additions:
//   - `assert_one_of_i32(actual, allowed)` / `_string` —
//     positive enumerated-set membership. Failure embeds
//     both the actual value AND the full allowed set
//     (rendered with appropriate quoting for the type).
//   - `assert_none_of_i32(actual, forbidden)` / `_string`
//     — negative enumerated-set check. Vacuously passes
//     on an empty forbidden list.
//
// 13 cases.
func TestRunnerOneOfNoneOfExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/one_of_none_of_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 5 - one_of_i32 missing renders set",
		"ok 6 - one_of_i32 empty set always fails",
		"ok 8 - one_of_string missing quotes both",
		"ok 10 - none_of_i32 match reports value",
		"ok 11 - none_of_i32 empty list vacuous",
		"ok 13 - none_of_string match quoted",
		"# pass 13",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/all_substring_array_test.lang` exercises
// batch-24 additions:
//   - `assert_all_starts_with(arr, prefix)` /
//     `assert_all_ends_with` / `assert_all_contain` —
//     substring property held across every element of a
//     string array (∀ over substring relation). Failure
//     embeds the first violation's index + value.
//   - `assert_starts_with_any(s, prefixes)` /
//     `assert_ends_with_any(s, suffixes)` — single-string
//     matches at least one of the supplied options.
//
// 13 cases.
func TestRunnerAllSubstringArrayExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/all_substring_array_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 2 - all_starts_with empty vacuous",
		"ok 3 - all_starts_with first violation",
		"ok 5 - all_ends_with violation",
		"ok 7 - all_contain violation",
		"ok 10 - starts_with_any no match + count",
		"ok 11 - starts_with_any empty list fails",
		"ok 13 - ends_with_any no match",
		"# pass 13",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/file_lines_and_timestamp_test.lang`
// exercises batch-25 additions:
//   - `assert_file_lines(path, expected_lines)` — read +
//     line-by-line compare; delegates to `assert_lines_eq`.
//   - `assert_file_line_count(path, n)` — line cardinality
//     (no trailing-newline overcount).
//   - `assert_close_to_now_ms(actual_ms, max_skew_ms)` —
//     wall-clock timestamp recency, bidirectional skew.
//
// 9 cases.
func TestRunnerFileLinesAndTimestampExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/file_lines_and_timestamp_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - file_lines exact match",
		"ok 3 - file_lines no-trail match",
		"ok 4 - file_lines mismatch named",
		"ok 5 - count mismatch reports both",
		"ok 6 - file_lines missing file",
		"ok 8 - close_to_now_ms 1h-old fails",
		"ok 9 - close_to_now_ms future-skew fails",
		"# pass 9",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/option_and_set_ops_test.lang` exercises
// batch-26 additions:
//   - Option result family: `assert_is_some_i32` /
//     `_string`, `assert_is_none_i32` / `_string`, and the
//     equality-paired `assert_is_some_eq_i32` /
//     `assert_is_some_eq_string` (i32 and string cover the
//     bulk of practical Option payload types; lang has no
//     generics over the payload so each gets a typed
//     helper).
//   - Array set relations: `assert_array_intersects_i32`
//     / `_string` (at least one shared element; empty
//     either side fails) and `assert_array_disjoint_i32`
//     / `_string` (no shared element; empty either side
//     vacuously passes). Complement to the existing
//     `assert_set_eq_*` / `assert_subset_*` family.
//
// 19 cases.
func TestRunnerOptionAndSetOpsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/option_and_set_ops_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 2 - is_some_i32 None fails",
		"ok 4 - is_none_i32 Some fails with value",
		"ok 6 - is_some_eq_i32 wrong value",
		"ok 7 - is_some_eq_i32 None distinct msg",
		"ok 11 - is_none_string quotes payload",
		"ok 13 - intersects_i32 no overlap + lens",
		"ok 17 - disjoint_i32 shared names value",
		"ok 18 - disjoint_i32 empty vacuous",
		"# pass 19",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/array_prefix_suffix_subseq_test.lang`
// exercises batch-27 additions:
//   - `assert_array_starts_with_i32(arr, prefix)` /
//     `_string` — `arr[0..len(prefix)] == prefix`.
//   - `assert_array_ends_with_i32(arr, suffix)` /
//     `_string` — same idea anchored at the tail; mismatch
//     diagnostic uses array-coords indices so failures
//     pinpoint the actual slot.
//   - `assert_array_contains_subseq_i32(arr, needle)` /
//     `_string` — contiguous-sub-array membership;
//     order-sensitive (unlike `assert_subset_i32`).
//
// 20 cases (covering empty-vacuous, length-mismatch, full-
// array-is-its-own-prefix, mismatch-at-index, the "retry
// after partial match" scan corner of subseq).
func TestRunnerArrayPrefixSuffixSubseqExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/array_prefix_suffix_subseq_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 2 - starts_with_i32 empty vacuous",
		"ok 4 - starts_with_i32 prefix too long",
		"ok 5 - starts_with_i32 mismatch at index",
		"ok 10 - ends_with_i32 arr-coords index",
		"ok 13 - subseq_i32 at start",
		"ok 17 - subseq_i32 order matters",
		"ok 18 - subseq_i32 retries after partial",
		"# pass 20",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/array_at_and_f32_range_test.lang`
// exercises batch-28 additions:
//   - `assert_at_i32(arr, idx, expected)` / `_string` /
//     `_i64` — single-position spot check with a distinct
//     diagnostic for out-of-bounds vs value-mismatch.
//   - `assert_in_range_f32(v, lo, hi)` — f32 mirror
//     of the existing range family; NaN inputs fail
//     unconditionally.
//
// 15 cases.
func TestRunnerArrayAtAndF32RangeExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/array_at_and_f32_range_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 4 - at_i32 wrong value diag",
		"ok 5 - at_i32 out-of-bounds high",
		"ok 6 - at_i32 negative index",
		"ok 7 - at_i32 empty array OOB",
		"ok 9 - at_string quoted diff",
		"ok 11 - at_i64 wrong value",
		"ok 13 - in_range_f32 inclusive bounds",
		"ok 15 - in_range_f32 NaN fails",
		"# pass 15",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/ci_string_and_log_kv_test.lang`
// exercises batch-29 additions:
//   - Case-insensitive string assertions (`_eq_string_ci`,
//     `_neq_string_ci`, `_contains_ci`, `_starts_with_ci`,
//     `_ends_with_ci`) wrapping the existing CI methods
//     on `std/string`.
//   - Structured-breadcrumb log methods on TestRunner:
//     `log_kv_string` quotes the value, `log_kv_i32` /
//     `log_kv_i64` emit unquoted numerics so awk/grep
//     numeric filters work without stripping quotes.
//
// 13 cases + 3 log_kv breadcrumbs whose exact rendering
// we pin here so a regression in the key=value shape
// surfaces immediately.
func TestRunnerCIStringAndLogKVExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/ci_string_and_log_kv_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 2 - eq_string_ci different case",
		"ok 3 - eq_string_ci mismatch raw quoted",
		"ok 5 - neq_string_ci matches CI fails",
		"ok 7 - contains_ci subseq",
		"ok 11 - ends_with_ci pass",
		// The exact breadcrumb shape — quoted string,
		// unquoted i32, unquoted i64.
		"# session_id=\"abc-123\"",
		"# retry_count=3",
		"# bytes_seen=1234567890123",
		"# pass 13",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/result_assertions_test.lang` exercises
// batch-30 additions:
//   - `assert_is_ok_string(res)` / `_string_array` —
//     Result must be Ok variant.
//   - `assert_is_err_string(res)` / `_string_array` — Err
//     variant; Ok-on-Err diagnostic embeds the payload
//     (string value or array length) so a regression has
//     its bad value in the log.
//   - `assert_is_ok_eq_string(res, expected)` — Ok AND
//     value matches; distinct diagnostic for the
//     Err-when-Ok-expected case.
//
// Stdlib's Result error type is uniformly IoError, so the
// helpers specialise on the Ok-type only.
//
// 10 cases.
func TestRunnerResultAssertionsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/result_assertions_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - is_ok_string after successful read",
		"ok 2 - is_ok_string on Err fails",
		"ok 4 - is_err on Ok embeds payload",
		"ok 6 - is_ok_eq wrong value embeds both",
		"ok 7 - is_ok_eq Err distinct diag",
		"ok 10 - is_err_array on Ok embeds length",
		"# pass 10",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/process_output_shortcuts_test.lang`
// exercises batch-31 process-result shortcuts:
//   - `assert_exit_zero(proc)` /
//     `assert_exit_nonzero(proc)` — sugar for the most
//     common exit-code patterns; failure message names
//     the actual code.
//   - `assert_stdout_lines(proc, lines[])` /
//     `assert_stderr_lines` — multi-line compare via
//     `assert_lines_eq` (same "line N" failure wording).
//   - `assert_stdout_line_count(proc, n)` /
//     `assert_stderr_line_count` — line cardinality
//     without pinning content.
//
// 10 cases.
func TestRunnerProcessOutputShortcutsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/process_output_shortcuts_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 2 - exit_zero on non-zero fails",
		"ok 4 - exit_nonzero on 0 names code",
		"ok 6 - stdout_lines mismatch line N",
		"ok 9 - stdout_line_count wrong reports",
		"ok 10 - stderr_line_count pass",
		"# pass 10",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/assert_at_wider_test.lang` exercises
// batch-32 additions filling out the `assert_at_*` spot-
// check family for wider integer + float widths:
//   - `assert_at_u32` / `_u64` — unsigned-int variants.
//   - `assert_at_f64(arr, idx, expected, epsilon)` /
//     `_f32` — float variants with mandatory tolerance;
//     NaN inputs always fail.
//
// 13 cases.
func TestRunnerAssertAtWiderExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/assert_at_wider_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 2 - at_u32 wrong value diag",
		"ok 3 - at_u32 out of bounds",
		"ok 7 - at_f64 within tolerance",
		"ok 8 - at_f64 outside tol + eps",
		"ok 9 - at_f64 NaN actual",
		"ok 11 - at_f64 out of bounds",
		"ok 13 - at_f32 outside tolerance",
		"# pass 13",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/json_detail_test.lang` exercises batch-33
// additions — narrower JSON assertions:
//   - `assert_json_has_key` / `assert_json_lacks_key` —
//     top-level JObject key presence checks.
//   - `assert_json_array_len` / `assert_json_object_size`
//     — cardinality checks.
//
// All four parse via `std/json` and report distinct
// diagnostics for invalid JSON, wrong top-level type
// (`null` / bool / number / string / array / object — the
// helper names what was found vs what was expected), and
// missing/extra entries.
//
// 14 cases.
func TestRunnerJSONDetailExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/json_detail_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 2 - has_key missing names size",
		"ok 3 - has_key invalid JSON diag",
		"ok 4 - has_key array top-level rejected",
		"ok 5 - has_key null top-level rejected",
		"ok 7 - lacks_key present fails",
		"ok 10 - array_len wrong shows both",
		"ok 11 - array_len object rejected",
		"ok 14 - object_size wrong shows both",
		"# pass 14",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/json_field_eq_test.lang` exercises
// batch-34 additions — JSON field extraction:
//   - `assert_json_eq_field_string(json_text, key, exp)`
//     — key is JString equal to exp.
//   - `assert_json_eq_field_i32(json_text, key, exp)` —
//     key is JNumber parseable to i32 and equal to exp.
//     Decimals are rejected (not silently truncated).
//   - `assert_json_eq_field_bool(json_text, key, exp)` —
//     key is JBool equal to exp.
//
// Each helper has 5+ distinct failure modes — invalid
// JSON, top-level not object, missing key, wrong value
// type at that key, value mismatch — and the test file
// pins the diagnostic for each.
//
// 14 cases.
func TestRunnerJSONFieldEqExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/json_field_eq_test.lang")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 2 - field_string wrong value diag",
		"ok 3 - field_string wrong type rejected",
		"ok 4 - field_string missing key",
		"ok 7 - field_i32 wrong value shows both",
		"ok 9 - field_i32 decimal rejected",
		"ok 13 - field_bool wrong value diag",
		"ok 14 - field_bool wrong type rejected",
		"# pass 14",
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
