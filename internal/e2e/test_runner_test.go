package e2e

import (
	"bytes"
	"os"
	"os/exec"
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
// through `fern -interp` rather than reimplementing the
// assertions on the Go side — that way the same examples that
// users see in the repo are also the regression gate, and any
// breakage in the runner shows up here.
//
// Each test computes the absolute path to the example with
// `filepath.Abs` so the test still works under
// `go test ./internal/e2e/...` (where Go sets cwd to the
// package dir, three deep from the project root).

// runLangInterp runs `fern -interp <src>` and returns the
// exit code + stdout + stderr. Shared by every runner-
// example gate in this file.
//
// Calls `t.Parallel()` up front so the ~40 TestRunner*
// gates fan out across cores. Safe because:
//   - `buildLangBinForInterp` caches the compiled binary
//     in a package-lifetime tempdir (not `t.TempDir()`),
//     so parallel callers all read the same path.
//   - Each gate uses its own `*_test.fern` source file
//     under `examples/tests/`; no shared writable state.
//   - The lang binary is read-only at this point; the
//     `fern -interp src` subprocess gets its own stdio
//     buffers per `exec.Command`.
//
// On an 8-core host this drops the runner-suite wall
// clock from ~25s to ~5s.
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

// `examples/tests/arithmetic_test.fern` is the canonical
// "all cases pass" run. Exercises assert_eq_i32 +
// assert_{lt,le,gt,ge}_i32 + fail()/pass() and a hand-rolled
// table walk. Exit code is 0; the TAP plan line is `1..10`.
func TestRunnerArithmeticExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/arithmetic_test.fern")
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

// `examples/tests/strings_test.fern` covers the string-method
// assertion helpers (contains, starts_with, ends_with, etc.).
// Passing suite → exit 0.
func TestRunnerStringsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/strings_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "# pass 16") || !strings.Contains(out, "# fail 0") {
		t.Errorf("expected 16 passes, 0 fails\noutput:\n%s", out)
	}
}

// `examples/tests/iter_test.fern` covers the core/iter stdlib (the
// generic Iterator[T] protocol + Range / ArrayIter and the eager
// drivers — sum / count / of / product / nth / last / min / max /
// contains / count_value / fold / any / all / map / filter) through the
// pure-Fern runner. Passing suite → exit 0.
func TestRunnerIterExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/iter_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: core/iter", "# pass 15", "# fail 0", "1..15"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/base64_test.fern` covers std/base64 (encode / decode
// across all three padding tails, empty input, a decode∘encode round-trip,
// and the base64_decode_strict Some/None cases for #4384's malformed-input
// signalling) — a deterministic codec that had only Go-side coverage.
// (std/hex has its own sibling suite, hex_test.fern.) Passing → exit 0.
func TestRunnerBase64ExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/base64_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/base64", "# pass 15", "# fail 0", "1..15"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/format_test.fern` covers std/format — positional `{}`
// substitution (incl. `{{`/`}}` escapes and `{:>w}`/`{:<w}` width
// alignment, the `+` sign and sign-aware `0` zero-pad flags), the
// binary-IEC `format_bytes`, and `format_duration_ms` (h/m/s/ms
// components) — a deterministic formatter that had only Go-side
// coverage. Passing suite → exit 0.
func TestRunnerFormatExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/format_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/format", "# pass 26", "# fail 0", "1..26"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/cmp_helpers_test.fern` covers core/cmp's generic free
// helpers over the primitive Ord/Eq impls (min / max / clamp / lt / gte /
// sort / is_sorted / contains / index_of / distinct / eq_arrays, incl.
// string) — distinct from derive_test.fern's `@derive` coverage. Passing
// suite → exit 0.
func TestRunnerCmpHelpersExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/cmp_helpers_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: core/cmp helpers", "# pass 18", "# fail 0", "1..18"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/math_test.fern` covers the deterministic std/math
// surface — range / range_step (half-open i32 ranges), the numeric-width
// constants (i32_max/min, i64_max/min) and the pack_rgb bit-packer
// (random_int is omitted as non-deterministic). Passing suite → exit 0.
func TestRunnerMathExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/math_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/math", "# pass 12", "# fail 0", "1..12"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/path_test.fern` covers the std/path POSIX helpers
// (string-level, no FS) — path_join / path_parent / path_file_name /
// path_extension / path_clean, incl. separator-collapsing,
// root-preservation, trailing-slash, hidden-file, and `.`/`..`
// resolution edges. Passing suite → exit 0.
func TestRunnerPathExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/path_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/path", "# pass 25", "# fail 0", "1..25"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/hex_test.fern` covers std/hex's lowercase encode /
// decode — round-trip fidelity, empty input, case-insensitive decode,
// the lenient decode termination (first non-hex char or odd-length
// tail), and the hex_decode_strict Some/None cases for #4384's
// malformed-input signalling. Passing suite → exit 0.
func TestRunnerHexExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/hex_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/hex", "# pass 12", "# fail 0", "1..12"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/url_test.fern` covers std/url's RFC 3986
// percent-encoding (url_encode / url_decode — unreserved pass-through,
// reserved escaping, lower-case + truncated decode, round-trip) and the
// best-effort url_parse split (scheme/host/port/path/query/fragment plus
// the empty-input None). Passing suite → exit 0.
func TestRunnerUrlExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/url_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/url", "# pass 17", "# fail 0", "1..17"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/csv_test.fern` covers std/csv's RFC 4180 single-line
// surface — csv_escape (quote-wrap on comma / quote / newline, interior
// quotes doubled), csv_join (escape then comma-join) and csv_parse_line
// (split, quoted-field commas, "" → " decode). Passing suite → exit 0.
func TestRunnerCsvExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/csv_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/csv", "# pass 19", "# fail 0", "1..19"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/int_test.fern` covers core/int's integer-formatting
// primitives — int_to_string (signed i32 → decimal incl. the INT_MIN
// unsigned-safe path), parse_int_radix (bases 2..36, sign handling,
// out-of-range / bad-digit → None) and int_to_string_radix (the inverse,
// lowercase). Passing suite → exit 0.
func TestRunnerIntExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/int_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: core/int", "# pass 19", "# fail 0", "1..19"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/i32_test.fern` covers std/i32's deterministic
// receiver-method helpers — abs / signum, byte classification (is_digit /
// is_alpha / hex_value / to_lower / to_upper), number-shape helpers
// (reverse_digits / is_palindrome / sum_of_digits / factorial / is_prime /
// is_perfect_square / is_multiple_of), the range checks (is_in_range
// half-open vs is_between inclusive), and the i32::MIN digit-family edge
// cases (#4390: digits / sum_of_digits / has_digit / reverse_digits no longer
// silently zero when abs() can't represent the magnitude). Passing suite → exit 0.
func TestRunnerI32ExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/i32_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/i32", "# pass 26", "# fail 0", "1..26"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/i64_test.fern` covers std/i64's signed-64-bit receiver
// methods (the wider counterpart to std/i32) — abs / min / max / clamp /
// pow (incl. a value past the i32 range) / gcd / lcm / to_string (incl.
// negative) / is_even / is_odd. Passing suite → exit 0.
func TestRunnerI64ExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/i64_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/i64", "# pass 14", "# fail 0", "1..14"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerU64ExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/u64_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/u64", "# pass 11", "# fail 0", "1..11"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// TestRunnerU32ArithExamplePasses gates the u32 wrapping-arithmetic contract
// suite under the interpreter. Interp-gated only: the self-host backends do not
// yet truncate u32 `+` / `<<` results back to 32 bits (the suite header documents
// the minimal repro + the std/crypto connection), so it is deliberately absent
// from selfHostStdTestCases until that codegen gap closes.
func TestRunnerU32ArithExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/u32_arith_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/u32 wrapping arithmetic", "# pass 10", "# fail 0", "1..10"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// TestRunnerIoBufferedExamplePasses gates the std/io_buffered BytesWriter suite
// under the interpreter. Interp-gated only: BytesWriter holds a `u8[]` field and
// is rebuilt immutably (`{ ...w, data }`) per write, so a writer retained to
// scope/program exit hits the RC drop-at-exit gap on the self-host backends
// (the same class as array_hof's flat_map/reduce/sort_by — crashes -1 during the
// first test's teardown). Deliberately absent from selfHostStdTestCases until the
// goal-2 RC port drops struct-holding-array locals correctly.
func TestRunnerIoBufferedExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/io_buffered_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/io_buffered BytesWriter", "# pass 9", "# fail 0", "1..9"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerSortByAndCiExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/sort_by_and_ci_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/sort comparator + ci", "# pass 10", "# fail 0", "1..10"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerStringClassifyTransformExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/string_classify_transform_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/string classify + transform", "# pass 20", "# fail 0", "1..20"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerStringSliceExtractExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/string_slice_extract_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/string slice + extract", "# pass 21", "# fail 0", "1..21"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerStringEscapeCountExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/string_escape_count_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/string escape + count", "# pass 19", "# fail 0", "1..19"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerStringReplaceSplitExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/string_replace_split_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/string replace + split", "# pass 14", "# fail 0", "1..14"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerTimeCalendarExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/time_calendar_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/time calendar", "# pass 15", "# fail 0", "1..15"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerTimeIsoSpanExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/time_iso_span_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/time iso + span", "# pass 12", "# fail 0", "1..12"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerTimeHttpDateExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/time_http_date_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/time http-date", "# pass 11", "# fail 0", "1..11"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/time_timezone_test.fern` covers std/time's zone lookup
// (timezone_iana standard-time offsets, incl. the half-hour Kolkata
// offset and the unknown-zone None) and the DST-awareness predicate
// timezone_observes_dst (#4388 item 5). Passing → exit 0.
func TestRunnerTimeTimezoneExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/time_timezone_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/time timezone", "# pass 6", "# fail 0", "1..6"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/time_relative_test.fern` covers std/time's
// Instant.relative_to humaniser — the "just now" window, past/future
// direction, singular vs plural units, and the unit ladder. Passing →
// exit 0; plan line `1..4`.
func TestRunnerTimeRelativeExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/time_relative_test.fern")
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

func TestRunnerJsonRoundtripExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/json_roundtrip_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/json roundtrip", "# pass 23", "# fail 0", "1..23"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/json_pointer_test.fern` covers std/json's RFC 6901
// JSON Pointer resolver (json_pointer) — object descent, array indexing,
// the ~1/~0 key escapes, empty-pointer (whole doc) / empty-key ("/")
// cases, and the miss paths (missing key, out-of-range / malformed
// index, descent into a scalar, no leading slash). Passing → exit 0.
func TestRunnerJsonPointerExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/json_pointer_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/json pointer", "# pass 10", "# fail 0", "1..10"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/utf8_test.fern` covers std/utf8 — UTF-8 codepoint
// decode (1..4-byte, plus the stray-continuation / truncated / overlong
// / surrogate rejections), encode (widths + U+FFFD substitution),
// codepoint_count / codepoints, is_valid_utf8, the encode_all round
// trip, and the codepoint-indexing layer (codepoint_at / char_at /
// substring). Passing → exit 0.
func TestRunnerUtf8ExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/utf8_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/utf8", "# pass 20", "# fail 0", "1..20"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/uuid_test.fern` covers std/uuid's generators by
// shape — v4/v7 length, hyphen positions, version + variant nibbles,
// is_uuid, and distinctness. The output is random but the assertions are
// structural, so the TAP output is deterministic. Passing → exit 0.
func TestRunnerUuidExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/uuid_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/uuid", "# pass 9", "# fail 0", "1..9"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/crypto_test.fern` covers std/crypto's SHA-256 +
// HMAC-SHA256 against the standard NIST (FIPS 180-4) / RFC 4231
// known-answer vectors (empty / "abc" / pangram, raw-digest length, an
// HMAC vector), plus the constant-time consteq / hmac_verify / hmac_verify_hex
// MAC-comparison helpers (#4384). This is the interp oracle; std/crypto also
// rides the self-host IR differential now (selfHostStdTestCases). Passing
// suite → exit 0.
func TestRunnerCryptoExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/crypto_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/crypto", "# pass 18", "# fail 0", "1..18"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/cli_test.fern` covers std/cli's spec-driven argument
// parser (#4385 item 1): valued options in --long V / --long=V / -short V
// forms, boolean flags, positional operands, the `--` terminator, the
// value_or default, the error paths (unknown option / missing value /
// value on a bool), and auto-usage. This is the interp oracle; std/cli
// also rides the self-host IR differential (selfHostStdTestCases).
func TestRunnerCliExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/cli_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/cli", "# pass 18", "# fail 0", "1..18"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/option_combinators_test.fern` covers the std/option
// COMBINATOR surface (distinct from option_and_set_ops_test, which covers the
// std/test Option assertion helpers) — is_some / is_none / unwrap_or /
// unwrap_or_else / map / and_then / or_else / filter / ok_or / map_or /
// is_some_and / or / and / ok_or_else / map_or_else / zip / xor / flatten,
// including the closure-taking generic methods. Passing suite → exit 0.
func TestRunnerOptionCombinatorsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/option_combinators_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/option combinators", "# pass 19", "# fail 0", "1..19"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/result_combinators_test.fern` covers the std/result
// COMBINATOR surface (distinct from result_assertions_test, which covers the
// std/test Result assertion helpers) — is_ok / is_err / unwrap_or /
// unwrap_or_else / map / and_then / map_err / ok / err / map_or / is_ok_and /
// is_err_and / or / and / or_else / map_or_else / flatten, including the
// closure-taking generic methods. Passing → exit 0.
func TestRunnerResultCombinatorsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/result_combinators_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/result combinators", "# pass 16", "# fail 0", "1..16"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/array_hof_test.fern` covers the std/array higher-order
// combinators NOT already in array_combinators_test (map/filter/fold/any/all/
// find): flat_map, reduce (→ Option[T]), sort_by (a comparator closure), and
// intersperse (structural, no callback).
// Interp-gated only: these three crash the self-hosted binary at program exit
// (a drop/RC-at-exit bug — the tests all PASS, output is byte-correct, then
// teardown traps; see the audit log), so the suite is intentionally NOT in
// selfHostStdTestCases. Passing suite → exit 0.
func TestRunnerArrayHofExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/array_hof_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array higher-order", "# pass 18", "# fail 0", "1..18"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/array_batch_test.fern` covers std/array's batch verbs
// slice / chunks / windows (#4416) — half-open clamped range, even /
// uneven / empty chunking, and overlapping / too-wide / full-width
// windows, including the i32[][] nested-array return shape. Passing →
// exit 0.
func TestRunnerArrayBatchExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/array_batch_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array batch", "# pass 10", "# fail 0", "1..10"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/iter_combinators_test.fern` covers the core/iter
// combinators NOT in iter_test (sum/count/of/product/nth/last/min/max/
// contains/count_value/fold/any/all/map/filter): to_array / take / skip /
// find / position / position_by / count_by. Interp-gated only: `take` / `skip`
// over an `ArrayIter[T]` argument make the self-host monomorphiser emit an
// invalid symbol — `bl __fn_iter__take__iter__ArrayIter[T]` with the
// unsubstituted `[T]` (bracket chars the assembler rejects; see the audit log)
// — so the suite is intentionally NOT in selfHostStdTestCases. Passing → exit 0.
func TestRunnerIterCombinatorsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/iter_combinators_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: core/iter combinators", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerNumReducersExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/num_reducers_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/num reducers", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/runner_self_test.fern` is the runner's own
// meta-test — confirms that every assertion helper returns the
// expected TestOutcome shape on both pass and fail paths.
// If THIS regresses, the rest of the suite reports false
// positives.
func TestRunnerSelfTestPasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/runner_self_test.fern")
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

// `examples/tests/async_combinators_test.fern` exercises the blessed
// structured-concurrency surface (docs/ASYNC-REDESIGN.md): the
// `gather` / `race` / `with_deadline` combinators over `Future[T]`,
// on the portable `Ready`-future path (resolves on every backend).
// Generic over T (gather over Future[string]). Passing suite → exit 0.
func TestRunnerAsyncCombinatorsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/async_combinators_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "# pass 5") || !strings.Contains(out, "# fail 0") {
		t.Errorf("expected 5 passes, 0 fails\noutput:\n%s", out)
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
import "std/test";

function test_passing(): test.TestOutcome {
    return test.assert_eq(1 + 1, 2);
}

function test_failing(): test.TestOutcome {
    return test.assert_eq(2 + 2, 5);
}

function main(): i32 {
    var r: test.TestRunner = test.test_new("failure-shape");
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
		"  message: assert_eq: expected 5, got 4",
		"# pass 1",
		"# fail 1",
		"# failures:",
		"failing: assert_eq: expected 5, got 4",
	}
	for _, w := range wantPieces {
		if !strings.Contains(gotOut, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, gotOut)
		}
	}
}

// `examples/tests/skip_and_subsuites_test.fern` covers the
// skip / skip_if / subsuite / merge surface. Skips don't count
// as failures (exit 0) and the TAP stream stays monotonic
// across subsuite boundaries — the harness threads a base_idx
// through the child runner so the first subsuite case prints
// `ok 5` (not `ok 1` again) when the parent ran 4 cases first.
func TestRunnerSkipAndSubsuitesExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/skip_and_subsuites_test.fern")
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

// `examples/tests/fuzz_example_test.fern` exercises the
// `std/fuzz` harness on three benign properties (always-OK,
// non-negative length, idempotent to_upper) and one transform
// invariant (trim strips edge spaces). The seeds are arranged
// so the mutation path actually exercises the property — eg
// the to_upper-idempotent target includes mixed-case seeds so
// byte flips into / out of the upper range get tested.
func TestRunnerFuzzExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/fuzz_example_test.fern")
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
import "std/fuzz";
import "std/test";

function detect_bad(input: string): test.TestOutcome {
    if (input.contains("BAD")) { return test.fail("forbidden pattern"); }
    return test.pass();
}

function main(): i32 {
    var r: test.TestRunner = test.test_new("fuzz-failure");
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

// `examples/tests/process_assertions_test.fern` exercises the
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
	src := langSrcAbs(t, "examples/tests/process_assertions_test.fern")
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

// `examples/tests/wide_numerics_test.fern` covers the i64 /
// u32 / u64 assertion family. The corresponding i32 helpers
// are pinned by `arithmetic_test.fern`; this exercises the
// wider widths so a regression in the interp's
// `__int_to_string_u64` override (the one Lang code in
// `core/int.fern` whose body the interp can't run) would
// surface here.
func TestRunnerWideNumericsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/wide_numerics_test.fern")
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

// `examples/tests/filesystem_ops_test.fern` exercises the
// `read_dir` / `remove_file` / `remove_dir_all` builtins and
// pins the matching semantics for each — particularly the
// "missing target" cases where remove_file is an error
// (matches Go's `os.Remove`) but remove_dir_all is silently
// OK (matches `os.RemoveAll`).
func TestRunnerFilesystemOpsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/filesystem_ops_test.fern")
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
//  1. running an inline test program that creates a fresh
//     temp dir, writes a file into it, registers the dir
//     for cleanup, then runs an assertion against the file
//  2. confirming exit=0 + the expected TAP output
//  3. confirming the directory no longer exists on the host
//     filesystem afterward (cleanup actually fired)
//
// We don't pin the exact tempdir path — `os.MkdirTemp`
// picks a random suffix — but we DO grep the test output
// for the printed path so the post-cleanup check has a
// concrete target.
func TestRunnerDeferCleanupRunsAtFinish(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
import "std/test";

function main(): i32 {
    var r: test.TestRunner = test.test_new("cleanup");
    match (temp_dir("fern-cleanup-probe")) {
        Ok(dir) => {
            print("# tempdir: " + dir);
            r = r.defer_cleanup(dir);
            match (write_file(dir + "/x.txt", "x")) {
                None => { },
                Some(_) => { r = r.it("write", test.fail("write failed")); return r.finish(); }
            }
            match (read_file(dir + "/x.txt")) {
                Ok(s) => { r = r.it("roundtrip", test.assert_eq(s, "x")); },
                Err(_) => { r = r.it("roundtrip", test.fail("read failed")); }
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

// `examples/tests/lang_binary_e2e_test.fern` is the canonical
// migration-pattern example: a Lang test file spawns the
// `lang` binary itself (path read from `$LANG_BIN`), drives
// it through `-interp` / `-check` against inline source +
// tempdir fixtures, and asserts on the (exit, stdout,
// stderr) triple. This is the shape the migrated Go-side
// e2e suite will adopt.
//
// We exercise both paths the example handles:
//  1. With `$LANG_BIN` pointing at a fresh build of the
//     compiler — every case runs and passes.
//  2. With `$LANG_BIN` unset — the suite skips cleanly
//     rather than failing, so dev laptops without an
//     explicit env setup don't see false negatives.
func TestRunnerLangBinaryE2EExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/lang_binary_e2e_test.fern")

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

// `examples/tests/helpers_test.fern` covers the convenience
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
	src := langSrcAbs(t, "examples/tests/helpers_test.fern")
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

// `examples/tests/float_test.fern` pins the f32 / f64
// assertion family + the underlying interp Float support.
// Before this work the interp errored out on `*ast.FloatLit`,
// which made float-touching code impossible to unit-test
// without compiling to a backend. Now `fern -interp` handles
// float arithmetic, comparison, casts, and the f32_bits /
// f32_from_bits reinterpret pair.
//
// Twelve cases cover: tolerance-equal vs exact-equal,
// f32 precision-loss tolerance (the 0.1+0.1+0.1 != 0.3
// textbook example), NaN detection + the NaN-unequal-to-
// itself property, ±0.0, ±Inf, f32_bits round-trips, and
// the to_string digit-formatting paths incl. the >= 2^63
// __float_int_part branch (#4379).
func TestRunnerFloatExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/float_test.fern")
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
		"ok 12 - to_string digit paths",
		"# pass 12",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/fuzz_shrink_test.fern` exercises the
// `r.fuzz_shrink` receiver method on three benign properties
// (no failures expected) — the harness's mutation loop runs
// each one through `fuzz_default_iterations()` mutated
// variants. The shrinker only kicks in on a failure, so
// this gate confirms the no-failure path stays clean +
// returns exit 0.
func TestRunnerFuzzShrinkExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/fuzz_shrink_test.fern")
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
import "std/fuzz";
import "std/test";

function detect_bad(input: string): test.TestOutcome {
    if (input.contains("BAD")) { return test.fail("forbidden"); }
    return test.pass();
}

function main(): i32 {
    var r: test.TestRunner = test.test_new("shrink-failure");
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

// `examples/tests/batch7_test.fern` is the omnibus example
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
	src := langSrcAbs(t, "examples/tests/batch7_test.fern")
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

// `examples/tests/batch8_test.fern` exercises the additions
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
	src := langSrcAbs(t, "examples/tests/batch8_test.fern")

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

// `examples/tests/float_math_test.fern` exercises the f64
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
	src := langSrcAbs(t, "examples/tests/float_math_test.fern")
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

// `examples/tests/timing_test.fern` exercises the time
// builtins (`now_unix_ms`, `monotonic_ns`, `sleep_ms`) and
// the elapsed-time assertion helpers (`assert_elapsed_lt_ms`
// / `_us`). Six cases — the failure-message case verifies
// the contract that the diagnostic embeds both the observed
// elapsed time AND the deadline so a flaky-bench failure
// preserves enough state to decide whether to bump the
// bound.
func TestRunnerTimingExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/timing_test.fern")
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

// `examples/tests/lines_log_test.fern` exercises the
// `assert_lines_eq(actual, expected_lines)` helper +
// `(r).log(msg)` chainable TAP-comment emitter. Four cases
// + interleaved log breadcrumbs verify both the matching
// and the line-count-mismatch / first-diff-line localisation
// paths.
func TestRunnerLinesLogExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/lines_log_test.fern")
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

// `examples/tests/fuzz_corpus_test.fern` exercises the
// `fuzz_corpus_from_dir` + `fuzz_corpus_from_dir_or` helpers
// that load seed corpora from disk. Six cases cover the
// loaded-seeds path, the fallback paths (missing directory
// + empty directory), and a smoke test for the new fuzz
// mutators (bit flip / byte duplicate / byte zero / byte max).
func TestRunnerFuzzCorpusExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/fuzz_corpus_test.fern")
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

// `examples/tests/bench_test.fern` exercises the bench
// harness: `r.bench(name, iter, fn)` reports timing as a TAP
// comment and always passes; `r.bench_max_us(name, iter, fn,
// budget)` fails when the median exceeds the budget. We
// verify both the comment shape (min / median / mean / max
// fields) and the budgeted case's pass path.
func TestRunnerBenchExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/bench_test.fern")
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

// `examples/tests/array_reductions_test.fern` exercises the
// new wider-int / float array reductions added as free
// functions to std/array: `sum_i64` / `max_i64` / `min_i64` /
// `avg_i64`, `sum_u32` / `max_u32` / `min_u32`,
// `sum_u64` / `max_u64` / `min_u64`, `sum_f64` / `max_f64` /
// `min_f64` / `avg_f64`. Eleven cases cover the happy path
// + empty input semantics + the near-u64-max unsigned-
// compare correctness check.
func TestRunnerArrayReductionsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/array_reductions_test.fern")
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

// `examples/tests/array_combinators_test.fern` exercises the
// generic array combinators added as free functions over a
// parametric T[] to std/array: `map` / `filter` / `fold` /
// `any` / `all` / `find` / `enumerate` (STDLIB-ROADMAP item
// #1). Twenty-two cases cover the happy path, empty-array
// semantics, element-type-changing map (i32 -> string),
// accumulator-type-differing fold, a captured-variable closure
// through filter, both Option arms of find, a map-then-fold
// pipeline, and the byte-exact join / join_with_last edges (#4379).
func TestRunnerArrayCombinatorsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/array_combinators_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - map",
		"ok 3 - map change type",
		"ok 6 - filter capture",
		"ok 9 - fold acc type",
		"ok 16 - find some",
		"ok 17 - find none",
		"ok 18 - enumerate",
		"ok 20 - map then fold",
		"ok 21 - join edges",
		"ok 22 - join_with_last edges",
		"# pass 22",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/array_structural_verbs_test.fern` exercises the
// generic structural array verbs added to std/array (#2689):
// `reverse` / `take` / `drop` / `concat` over an arbitrary `T[]`.
// Thirteen cases cover the happy path, empty / single-element inputs,
// the clamping behaviour of take/drop (n <= 0 and n >= len), the
// `take(n) ++ drop(n) == xs` complement law, and the `.concat()`
// receiver-method form. These verbs take no callback, so (unlike the
// combinators) they are also gated through the self-host compiler —
// see TestSelfHostStdTestE2E.
func TestRunnerArrayStructuralVerbsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/array_structural_verbs_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - reverse",
		"ok 6 - take over length clamps",
		"ok 8 - drop over length clamps",
		"ok 10 - take ++ drop complement",
		"ok 13 - concat method form",
		"# pass 13",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/log_test.fern` exercises the leveled `Logger`
// added to std/log (#2683): min-level threshold filtering, the five
// levels TRACE..ERROR, structured key/value fields, and the JSON-line
// output mode. Assertions target the pure `render(msg)` output. The
// logger is structs + strings only, so it also runs through the
// self-host stdtest gate (TestSelfHostStdTestE2E case "log").
func TestRunnerLogExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/log_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - text basic",
		"ok 3 - threshold filters below",
		"ok 6 - json fields",
		"ok 7 - json escaping",
		"ok 9 - at explicit level",
		"ok 10 - json escaping whitespace",
		"ok 11 - json escaping control",
		"# pass 11",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/map_verbs_test.fern` exercises the higher-level
// Map verbs added to core/map (#2685): `entries`, `merge` / `extend`,
// `from`, `get_or_insert`, `update`, and `contains_value`, over both i32
// and string keys (including the word-count use case via both
// get_or_insert and the one-pass update). Sixteen cases. These verbs use
// Option + tuples + closures + generic map ops which the self-host
// compiler can't lower yet, so — like the closure combinators — they are
// gated through the interpreter rather than the self-host stdtest gate.
func TestRunnerMapVerbsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/map_verbs_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - entries sum k+v",
		"ok 4 - from pairs",
		"ok 7 - merge other wins",
		"ok 12 - get_or_insert word count",
		"ok 13 - update word count",
		"ok 15 - contains_value",
		"# pass 16",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/set_eq_test.fern` exercises the order-
// independent (multiset) array assertions:
// `assert_set_eq_i32` / `_string` and `assert_subset_i32` /
// `_string`. Ten cases cover passing, reversed order,
// duplicate-multiplicity requirements (multiset semantics),
// length mismatches, and the vacuous "empty subset of X"
// case.
func TestRunnerSetEqExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/set_eq_test.fern")
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

// `examples/tests/env_unreachable_test.fern` exercises the
// `assert_env_set` / `_unset` / `_eq` env-var assertion
// family and `unreachable(label)`. Five cases — every
// helper exercised in both directions where applicable,
// plus the failure-message context checks.
func TestRunnerEnvUnreachableExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/env_unreachable_test.fern")
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

// `examples/tests/rel_tol_and_ms_bench_test.fern` exercises
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
	src := langSrcAbs(t, "examples/tests/rel_tol_and_ms_bench_test.fern")
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

// `examples/tests/sorted_unique_range_test.fern` exercises
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
	src := langSrcAbs(t, "examples/tests/sorted_unique_range_test.fern")
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

// `examples/tests/float_array_strict_sort_test.fern`
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
	src := langSrcAbs(t, "examples/tests/float_array_strict_sort_test.fern")
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

// `examples/tests/map_eq_and_predicates_test.fern` exercises
// batch-21 additions:
//   - `assert_eq_map` — full map deep equality (length +
//     key-with-matching-value in one direction; pigeonhole
//     gives the reverse), generic over K/V. Map iteration
//     order isn't observable so walks `actual.keys()`
//     rather than `iter`.
//   - `assert_all_i32` / `_string` — ∀ predicate; vacuous
//     pass on empty array. Failure names index + value.
//   - `assert_any_i32` / `_string` — ∃ predicate; vacuous
//     FAIL on empty array (mathematical convention). The
//     test takes a `(T) => boolean` lambda or named fn.
//
// 16 cases total.
func TestRunnerMapEqAndPredicatesExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/map_eq_and_predicates_test.fern")
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

// `examples/tests/wider_array_contains_count_test.fern`
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
	src := langSrcAbs(t, "examples/tests/wider_array_contains_count_test.fern")
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

// `examples/tests/one_of_none_of_test.fern` exercises
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
	src := langSrcAbs(t, "examples/tests/one_of_none_of_test.fern")
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

// `examples/tests/all_substring_array_test.fern` exercises
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
	src := langSrcAbs(t, "examples/tests/all_substring_array_test.fern")
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

// `examples/tests/file_lines_and_timestamp_test.fern`
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
	src := langSrcAbs(t, "examples/tests/file_lines_and_timestamp_test.fern")
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

// `examples/tests/option_and_set_ops_test.fern` exercises
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
	src := langSrcAbs(t, "examples/tests/option_and_set_ops_test.fern")
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

// `examples/tests/array_prefix_suffix_subseq_test.fern`
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
	src := langSrcAbs(t, "examples/tests/array_prefix_suffix_subseq_test.fern")
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

// `examples/tests/array_at_and_f32_range_test.fern`
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
	src := langSrcAbs(t, "examples/tests/array_at_and_f32_range_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 4 - at_i32 wrong value diag",
		"ok 5 - at_i32 out-of-bounds high",
		"ok 6 - at_i32 negative index",
		"ok 7 - at_i32 empty array OOB",
		"ok 9 - at_string embeds both",
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

// `examples/tests/ci_string_and_log_kv_test.fern`
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
	src := langSrcAbs(t, "examples/tests/ci_string_and_log_kv_test.fern")
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

// `examples/tests/result_assertions_test.fern` exercises
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
	src := langSrcAbs(t, "examples/tests/result_assertions_test.fern")
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

// `examples/tests/process_output_shortcuts_test.fern`
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
	src := langSrcAbs(t, "examples/tests/process_output_shortcuts_test.fern")
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

// `examples/tests/assert_at_wider_test.fern` exercises
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
	src := langSrcAbs(t, "examples/tests/assert_at_wider_test.fern")
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

// `examples/tests/json_detail_test.fern` exercises batch-33
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
	src := langSrcAbs(t, "examples/tests/json_detail_test.fern")
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

// `examples/tests/json_field_eq_test.fern` exercises
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
	src := langSrcAbs(t, "examples/tests/json_field_eq_test.fern")
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

// `examples/tests/string_count_and_dir_listing_test.fern`
// exercises batch-35 additions:
//   - `assert_string_count(haystack, needle, n)` —
//     non-overlapping occurrence count of `needle`.
//   - `assert_eq_dir_listing(dir, expected_names)` —
//     multiset compare of dir contents (readdir order
//     not observable; sorts both sides then delegates to
//     assert_eq_string_array).
//
// 10 cases.
func TestRunnerStringCountAndDirListingExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/string_count_and_dir_listing_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 4 - string_count non-overlapping",
		"ok 5 - string_count wrong reports both",
		"ok 6 - string_count empty haystack",
		"ok 7 - listing matches multiset (sorted)",
		"ok 9 - extra file flagged via length diff",
		"ok 10 - dir_listing missing dir",
		"# pass 10",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/string_prelude_migrated_test.fern` is a
// proof-of-concept Lang port of `TestInterpScriptStringPrelude`
// in `interp_script_test.go`. Same 8-property surface, but
// instead of piping an inline Lang program through
// `fern -interp` and grepping the exit code + stdout, the
// new test is the program — every property becomes its own
// TAP case via `TestRunner.it`. Demonstrates the migration
// pattern for tests that exercise user-language behaviour
// without poking at compiler internals.
//
// Both versions stay live during the wider runner-adoption
// effort: the Go test guards the subprocess + stdout
// shape, the Lang test guards the in-process semantics
// once `fern -interp` itself stops being a Go target.
func TestRunnerStringPreludeMigratedExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/string_prelude_migrated_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"# Suite: string prelude (migrated)",
		"ok 6 - s.to_upper() returns HELLO",
		"ok 7 - s.to_lower() returns hello",
		"ok 8 - string_from_bytes(s.bytes()) round-trips",
		"# pass 8",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/unions_migrated_test.fern` — Lang port
// of `TestInterpScriptUnions` from `interp_script_test.go`.
// Second migration in the runner-adoption campaign
// (after the string-prelude port). Original Go test pinned
// one Add(10, 32) → 42 data point via exit-code; the
// migrated form expands to a small table of match-arm
// cases since once the migration cost is paid, each
// additional `r.it(...)` is one line of marginal cost.
// Both versions stay live until the broader cutover.
func TestRunnerUnionsMigratedExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/unions_migrated_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"# Suite: unions + match (migrated)",
		"ok 1 - Add(10, 32) = 42",
		"ok 4 - Add(5, -5) = 0",
		"ok 7 - Lit(-100) = -100",
		"# pass 7",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/header_map_migrated_test.fern` — Lang
// port of `TestInterpScriptHeaderMap`. Third migration in
// the runner-adoption campaign. The Go original was 5
// table-driven subprocess cases; the migrated form folds
// them into 6 in-process `r.it(...)` cases (splitting the
// "set replaces in place" test into a size-check + value-
// check pair since each is one extra `r.it(...)` line).
//
// First migration to need an explicit `import "std/foo"`
// (std/headers isn't in the auto-prelude) — the runner +
// modload survive the mangled-load path here, which
// expands the corpus of tests confirmed safe under that
// codepath.
func TestRunnerHeaderMapMigratedExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/header_map_migrated_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"# Suite: HeaderMap (migrated)",
		"ok 1 - case-insensitive get",
		"ok 2 - get_all preserves insertion order",
		"ok 3 - set replaces drops dupes (size=2)",
		"ok 6 - get_all on missing name is empty",
		"# pass 6",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/http_request_headers_migrated_test.fern`
// — Lang port of `TestInterpScriptHttpRequestHeaders`.
// Fourth migration in the runner-adoption campaign.
// Original was 3 table-driven subprocess cases; migrated
// to 5 in-process `r.it(...)` cases (split the
// "parsed headers reachable" case into Content-Type-value
// + header-count, and added a missing-header `get_all`
// returns-empty-array case that the original didn't
// pin).
//
// First migration that uses the larger `std/http`
// surface — confirms `http.http_parse_request` works
// cleanly through the runner's flat-namespace load
// path.
func TestRunnerHttpRequestHeadersMigratedExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/http_request_headers_migrated_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"# Suite: HTTP request headers (migrated)",
		"ok 1 - Content-Type reachable case-insensitively",
		"ok 3 - duplicate Set-Cookie preserves insertion order",
		"ok 4 - missing header returns None",
		"ok 6 - X-*-Content-Length is not the body length",
		"ok 7 - duplicate Content-Length rejected",
		"ok 8 - Transfer-Encoding rejected",
		"ok 9 - http_header_value via HeaderMap",
		"ok 10 - http_header_value missing returns None",
		"# pass 10",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/http_response_headers_migrated_test.fern`
// — Lang port of `TestInterpScriptHttpResponseHeaders`.
// Fifth migration in the runner-adoption campaign.
// Original was 4 table-driven subprocess cases; migrated
// to 6 in-process `r.it(...)` cases — added the stronger
// "bogus 9999 doesn't leak into wire" negative form and
// the explicit "duplicates preserve order" invariant
// alongside the whole-wire compare. Compares the raw
// wire returned by `http_serialize_response` directly via
// `assert_eq_string` instead of routing through
// print-then-grep-stdout (cleaner since the original's
// trailing newline was print()'s, not the wire's).
func TestRunnerHttpResponseHeadersMigratedExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/http_response_headers_migrated_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"# Suite: HTTP response headers (migrated)",
		"ok 1 - user-set headers before auto block",
		"ok 3 - user Content-Length overridden by auto",
		"ok 4 - bogus Content-Length absent from wire",
		"ok 6 - duplicate Set-Cookie preserves order",
		"ok 7 - status reason for extended codes",
		"ok 8 - unknown status falls back to Status",
		"# pass 8",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/fail_fast_test.fern` exercises the new
// fail-fast mode added to std/test:
//   - `test_new_fail_fast(suite)` constructor.
//   - `(r).with_fail_fast()` post-init opt-in.
//   - `parse_fail_fast_from_args(argv)` CLI lift.
//
// The contract: once any case fails, subsequent `it()`
// calls auto-skip with reason "fail-fast: prior case
// failed". The test exercises this by spinning up an
// isolated child runner that deliberately fails, then
// inspects its counters from the outer suite — keeps
// the outer exit code clean while still pinning the
// behaviour. The interleaved TAP output (child TAP +
// outer TAP) is part of what we pin: the SKIP-line
// wording shows up in the combined stream.
func TestRunnerFailFastExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/fail_fast_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - c1 pass before fail",
		"not ok 2 - c2 fail triggers fast",
		"ok 3 - c3 should auto-skip after fail # SKIP fail-fast: prior case failed",
		"ok 1 - fail-fast short-circuits post-fail",
		"ok 2 - default mode runs every case",
		"ok 3 - parse_fail_fast detects --fail-fast",
		"ok 4 - parse_fail_fast no false-positive",
		"ok 5 - with_fail_fast preserves filter",
		"# pass 5",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/quiet_mode_test.fern` exercises the new
// --quiet mode added to std/test:
//   - `test_new_quiet(suite)` / `(r).with_quiet()` /
//     `parse_quiet_from_args(argv)` — the constructor +
//     fluent setter + CLI parser triple, matching the
//     `--filter` / `--fail-fast` family shape.
//   - The contract: `ok N - name` lines suppressed for
//     passes + skips; `not ok` lines still print; the
//     `1..N` plan + summary footer still print; counters
//     are unaffected.
//
// 7 cases (isolated-child-runner pattern from
// fail_fast_test.fern). The interleaved TAP output is
// part of what we pin: child runners' `# Suite:` headers
// still appear (those come from `test_new`, not quiet
// mode), but their `ok N - p1` / `ok N - p2` per-case
// lines are absent — proving quiet suppresses the
// per-case prints.
func TestRunnerQuietModeExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/quiet_mode_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"# Suite: quiet-counters",
		"ok 1 - quiet preserves counters across passes",
		"ok 7 - test_new_quiet enables flag",
		"# pass 7",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
	// Negative check: the quiet child-runner cases (p1 /
	// p2 / p3 inside test_quiet_preserves_counters) MUST
	// NOT print their per-case lines. If they ever do, the
	// suppression broke.
	for _, suppressed := range []string{
		"- p1",
		"- p2",
		"- p3",
	} {
		if strings.Contains(out, suppressed) {
			t.Errorf("quiet mode failed to suppress %q in:\n%s", suppressed, out)
		}
	}
}

// `examples/tests/set_test.fern` covers std/set — the generic,
// value-semantic Set[T] (membership, dedup, union/intersect/
// difference, subset/equals) over both i32 and string elements. The
// load-bearing case is `add is pure` (test 4): the value-semantics
// contract that an operation never mutates its receiver. Passing
// suite → exit 0; the TAP plan line is `1..12`.
func TestRunnerSetExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/set_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/set", "ok 4 - add is pure", "1..12", "# pass 12", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/unicode_test.fern` covers std/unicode — simple
// (1:1) case mapping across ASCII / Latin-1 / Greek / Cyrillic, the
// code-point helpers, the simple-mapping caveat (ß unchanged),
// eq_ignore_case, and the character-class predicates (is_letter /
// is_digit / is_alnum / is_whitespace / is_upper / is_lower). Passing
// suite → exit 0; the TAP plan line is `1..13`.
func TestRunnerUnicodeExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/unicode_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/unicode", "1..13", "# pass 13", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/semver_test.fern` covers std/semver — SemVer 2.0.0
// parse, canonical to_string, the §11 precedence chain (incl. the
// numeric `beta.2 < beta.11` trap), build-metadata-ignored, and
// malformed-input rejection. Passing suite → exit 0; plan line `1..7`.
func TestRunnerSemverExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/semver_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/semver", "1..7", "# pass 7", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/glob_test.fern` covers std/glob — the shell-style
// matcher: `*` (non-separator), `?`, `**` (globstar with zero-directory
// elision), and `[...]` classes with ranges + negation. Passing suite →
// exit 0; plan line `1..7`.
func TestRunnerGlobExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/glob_test.fern")
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

// `examples/tests/dotenv_test.fern` covers std/dotenv — KEY=VALUE
// parsing, trimming, comments/blanks, the `export` prefix, double- and
// single-quoted values, last-wins, and malformed-line skipping. Passing
// suite → exit 0; plan line `1..7`.
func TestRunnerDotenvExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/dotenv_test.fern")
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

// `examples/tests/rand_test.fern` covers std/rand — shuffle (a
// permutation that leaves its input untouched), choice (always
// in-bounds; None only on empty), and sample (k distinct elements). The
// draws are non-deterministic, so it asserts the contracts. Passing
// suite → exit 0; plan line `1..6`.
func TestRunnerRandExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/rand_test.fern")
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

// `examples/tests/base32_test.fern` covers std/base32 against the
// RFC 4648 §10 known-answer vectors (every padding length) plus decode
// round-trips. Passing suite → exit 0; plan line `1..4`.
func TestRunnerBase32ExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/base32_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/base32", "1..4", "# pass 4", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

// `examples/tests/strdist_test.fern` covers std/strdist — Levenshtein
// edit distance (reference cases + code-point awareness) and the
// normalised similarity ratio. Passing suite → exit 0; plan line
// `1..4`.
func TestRunnerStrdistExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/strdist_test.fern")
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

// `examples/tests/table_test.fern` covers std/table — column-aligned
// rendering (last column unpadded, short rows padded, code-point-width
// alignment) and the header variant with its `-` rule. Passing suite →
// exit 0; plan line `1..6`.
func TestRunnerTableExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/table_test.fern")
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

// `examples/tests/textwrap_test.fern` covers std/textwrap.word_wrap —
// greedy wrapping at word boundaries, the exact-fit boundary, long
// words, hard-newline preservation, space collapsing, and edge widths.
// Passing suite → exit 0; plan line `1..6`.
func TestRunnerTextwrapExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/textwrap_test.fern")
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

// `examples/tests/format_duration_parse_test.fern` covers
// std/format.parse_duration_ms — single units, multi-part durations
// with/without spaces, the i64 range beyond i32, and the None
// rejections. Passing suite → exit 0; plan line `1..4`.
func TestRunnerFormatDurationParseExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/tests/format_duration_parse_test.fern")
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

// An empty-suite run still produces well-formed TAP — the
// `1..0` plan line and a `# tests 0` summary. Useful for
// scaffolding a new test file before the first case lands.
func TestRunnerEmptySuiteIsValidTAP(t *testing.T) {
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", "-")
	cmd.Stdin = strings.NewReader(`
import "std/test";

function main(): i32 {
    var r: test.TestRunner = test.test_new("empty");
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
