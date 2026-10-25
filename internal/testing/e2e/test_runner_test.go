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

func TestRunnerArithmeticExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/arithmetic_test.fern")
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

func TestRunnerTzExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/tz_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: tz", "1..19", "# pass 19", "# fail 0"} {
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

func TestRunnerStringsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/strings_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "# pass 16") || !strings.Contains(out, "# fail 0") {
		t.Errorf("expected 16 passes, 0 fails\noutput:\n%s", out)
	}
}

func TestRunnerRegexExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/regex_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/regex", "# pass 24", "# fail 0", "1..24"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerIterExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/iter_test.fern")
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

func TestRunnerBase64ExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/base64_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/base64", "# pass 17", "# fail 0", "1..17"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerFormatExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/format_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/format", "# pass 49", "# fail 0", "1..49"} {
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

func TestRunnerMathExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/math_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/math", "# pass 16", "# fail 0", "1..16"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerPathExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/path_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/path", "# pass 28", "# fail 0", "1..28"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerHexExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/hex_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/hex", "# pass 13", "# fail 0", "1..13"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerUrlExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/url_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/url", "# pass 33", "# fail 0", "1..33"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerDeflateExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/deflate_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/deflate", "# pass 20", "# fail 0", "1..20"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerNetExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/net_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/net", "# pass 11", "# fail 0", "1..11"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerFetchProxyExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/fetch_proxy_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/fetch proxies", "# pass 19", "# fail 0", "1..19"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerCsvExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/csv_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/csv", "# pass 23", "# fail 0", "1..23"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerIntExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/int_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: core/int", "# pass 20", "# fail 0", "1..20"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerI32ExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/i32_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/i32", "# pass 33", "# fail 0", "1..33"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerI32BitLengthExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/i32_bit_length_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/i32 bit_length", "# pass 7", "# fail 0", "1..7"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerI32ToStringRadixExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/i32_to_string_radix_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/i32 to_string_radix", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerI32BitArithExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/i32_bit_arith_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/i32 bit + arith", "# pass 10", "# fail 0", "1..10"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerI64ExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/i64_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/i64", "# pass 18", "# fail 0", "1..18"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerI64ToStringRadixExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/i64_to_string_radix_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/i64 to_string_radix", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerI64BitOpsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/i64_bit_ops_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/i64 bit ops", "# pass 7", "# fail 0", "1..7"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerI64RangeExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/i64_range_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/i64 range", "# pass 10", "# fail 0", "1..10"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerU64ExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/u64_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/u64", "# pass 15", "# fail 0", "1..15"} {
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

func TestRunnerU32ArithExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/u32_arith_test.fern")
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

func TestRunnerIoBufferedExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/io_buffered_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/io_buffered BytesWriter", "# pass 20", "# fail 0", "1..20"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerSortByAndCiExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/sort_by_and_ci_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: cmp comparator + ci sorts", "# pass 13", "# fail 0", "1..13"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerSortFamilyExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/sort_family_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: cmp sort family", "# pass 12", "# fail 0", "1..12"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerStringClassifyTransformExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/string_classify_transform_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/string classify + transform", "# pass 22", "# fail 0", "1..22"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerStringSliceExtractExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/string_slice_extract_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/string slice + extract", "# pass 23", "# fail 0", "1..23"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerStringEscapeCountExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/string_escape_count_test.fern")
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
	src := langSrcAbs(t, "tests/stdlib/string_replace_split_test.fern")
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

func TestRunnerStringSwapCaseExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/string_swap_case_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/string swap_case", "# pass 7", "# fail 0", "1..7"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerStringZfillExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/string_zfill_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/string zfill", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerStringParseRadixExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/string_parse_radix_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/string parse_int_radix", "# pass 10", "# fail 0", "1..10"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerStringPartitionExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/string_partition_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/string partition", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerStringRsplitOnceExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/string_rsplit_once_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/string rsplit_once", "# pass 9", "# fail 0", "1..9"} {
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

func TestRunnerTimeCalendarExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/time_calendar_test.fern")
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
	src := langSrcAbs(t, "tests/stdlib/time_iso_span_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/time iso + span", "# pass 14", "# fail 0", "1..14"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerTimeHttpDateExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/time_http_date_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/time http-date", "# pass 18", "# fail 0", "1..18"} {
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

func TestRunnerJsonRoundtripExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/json_roundtrip_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/json roundtrip", "# pass 30", "# fail 0", "1..30"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerJsonPointerExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/json_pointer_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/json pointer", "# pass 11", "# fail 0", "1..11"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerJsonObjectExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/json_object_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/json object", "# pass 9", "# fail 0", "1..9"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerJsonDocumentExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/json_document_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/json document", "# pass 9", "# fail 0", "1..9"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerUtf8ExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/utf8_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/utf8", "# pass 30", "# fail 0", "1..30"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerUuidExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/uuid_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/uuid", "# pass 12", "# fail 0", "1..12"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerCryptoExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/crypto_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/crypto", "# pass 26", "# fail 0", "1..26"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func runnerSuitePasses(t *testing.T, file, suite string, n int) {
	t.Helper()
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/"+file+"_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	plan := fmt.Sprintf("1..%d", n)
	for _, w := range []string{"# Suite: " + suite, fmt.Sprintf("# pass %d", n), "# fail 0", plan} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerDigestMd5ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "digest_md5", "std/crypto MD5", 7)
}

func TestRunnerDigestSha1ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "digest_sha1", "std/crypto SHA-1", 7)
}

func TestRunnerDigestSha224ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "digest_sha224", "std/crypto SHA-224", 7)
}

func TestRunnerDigestSha256ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "digest_sha256", "std/crypto SHA-256", 7)
}

func TestRunnerDigestSha384ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "digest_sha384", "std/crypto SHA-384", 7)
}

func TestRunnerDigestSha512ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "digest_sha512", "std/crypto SHA-512", 7)
}

func TestRunnerDigestBlake2bExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "digest_blake2b", "std/crypto BLAKE2b", 8)
}

func TestRunnerDigestSm3ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "digest_sm3", "std/crypto SM3", 8)
}

func TestRunnerChaCha20Poly1305ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "chacha20poly1305", "std/crypto/chacha20poly1305", 12)
}

func TestRunnerAESGCMExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "aes_gcm", "std/crypto/aes_gcm", 19)
}

func TestRunnerX25519ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "x25519", "std/crypto/x25519", 9)
}

func TestRunnerMLKEM768ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "mlkem768", "std/crypto/mlkem768", 8)
}

func TestRunnerMontgomeryExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "montgomery", "std/crypto/montgomery", 4)
}

func TestRunnerField25519ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "field25519", "std/crypto/field25519", 5)
}

func TestRunnerP256ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "p256", "std/crypto/p256", 4)
}

func TestRunnerEd25519ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "ed25519", "std/crypto/ed25519", 11)
}

func TestRunnerRSAExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "rsa", "std/crypto/rsa", 12)
}

func TestRunnerTLSKeyScheduleExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "tls_keyschedule", "std/tls/keyschedule", 18)
}

func TestRunnerTLSDerExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "tls_der", "std/tls/der", 7)
}

func TestRunnerTLSX509ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "tls_x509", "std/tls/x509", 7)
}

func TestRunnerTLSVerifyExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "tls_verify", "std/tls/verify", 11)
}

func TestRunnerTLSRootsExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "tls_roots", "std/tls/verify roots", 3)
}

func TestRunnerTLSMozillaExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "tls_mozilla", "std/tls/mozilla", 3)
}

func TestRunnerHTTPRouterExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_router", "std/http/router", 11)
}

func TestRunnerHTTPJsonOfExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_json_of", "std/http json_of", 3)
}

func TestRunnerHTTPCorsExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_cors", "std/http/cors", 9)
}

func TestRunnerHTTPAuthExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_auth", "std/http/auth", 6)
}

func TestRunnerHTTPRequestIdExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_requestid", "std/http/requestid", 4)
}

func TestRunnerHTTPHpackExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_hpack", "std/http/hpack", 13)
}

func TestRunnerHTTPQPACKExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_qpack", "std/http/qpack", 11)
}

func TestRunnerHTTPH3ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_h3", "std/http/h3", 9)
}

func TestRunnerHTTPH3QExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_h3q", "std/http/h3q", 8)
}

func TestRunnerHTTPH2FrameExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_h2_frame", "std/http/h2/frame", 7)
}

func TestRunnerHTTPH2ExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_h2", "std/http/h2", 20)
}

func TestRunnerQUICFramesExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "quic_frames", "std/quic/frames", 6)
}

func TestRunnerQUICPacketExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "quic_packet", "std/quic/packet", 10)
}

func TestRunnerQUICParamsExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "quic_params", "std/quic/params", 4)
}

func TestRunnerQUICRecoveryExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "quic_recovery", "std/quic/recovery", 5)
}

func TestRunnerQUICConnExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "quic_conn", "std/quic/conn", 28)
}

func TestRunnerQUICStreamsExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "quic_streams", "std/quic/streams", 13)
}

func TestRunnerHTTPGRPCExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_grpc", "std/http/grpc", 5)
}

func TestRunnerProtobufExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "protobuf", "std/protobuf", 5)
}

func TestRunnerHTTPCookieExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_cookie", "std/http/cookie", 13)
}

func TestRunnerHTTPFormExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_form", "std/http/form", 4)
}

func TestRunnerHTTPCacheExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_cache", "std/http/cache", 16)
}

func TestRunnerHTTPForwardedExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_forwarded", "std/http/forwarded", 14)
}

func TestRunnerHTTPTraceExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_trace", "std/http/trace", 6)
}

func TestRunnerHTTPAccessLogExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_accesslog", "std/http/accesslog", 5)
}

func TestRunnerBrotliExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "brotli", "std/brotli", 7)
}

func TestRunnerZstdExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "zstd", "std/zstd", 9)
}

func TestRunnerHTTPCompressExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_compress", "std/http/compress", 12)
}

func TestRunnerHTTPStaticExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_static", "std/http/static", 18)
}

func TestRunnerHTTPSSEExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_sse", "std/http/sse", 12)
}

func TestRunnerHTTPWebSocketExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_websocket", "std/http/websocket", 20)
}

func TestRunnerPublicSuffixExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "publicsuffix", "std/publicsuffix", 8)
}

func TestRunnerHTTPMultipartExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "http_multipart", "std/http/multipart", 9)
}

func TestRunnerTLSClientExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "tls_client", "std/tls/client", 9)
}

func TestRunnerTLSServerExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "tls_server", "std/tls/server", 16)
}

func TestRunnerTLSHandshakeExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "tls_handshake", "std/tls/handshake", 20)
}

func TestRunnerTLSKeyshareExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "tls_keyshare", "std/tls/keyshare", 12)
}

func TestRunnerTLSMessageExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "tls_message", "std/tls/message", 11)
}

func TestRunnerTLSRecordExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "tls_record", "std/tls/record", 23)
}

func TestRunnerHashChecksumsExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "hash_checksums", "std/hash checksums", 9)
}

func TestRunnerConstmapExamplePasses(t *testing.T) {
	runnerSuitePasses(t, "constmap", "std/constmap", 6)
}

func TestRunnerCliExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/cli_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/cli", "# pass 37", "# fail 0", "1..37"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerEnvflagExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/envflag_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/envflag", "# pass 4", "# fail 0", "1..4"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerOptionCombinatorsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/option_combinators_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/option combinators", "# pass 20", "# fail 0", "1..20"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerResultCombinatorsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/result_combinators_test.fern")
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

func TestRunnerArrayHofExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_hof_test.fern")
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

func TestRunnerArrayExtremumByExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_extremum_by_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array extremum-by", "# pass 9", "# fail 0", "1..9"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayCountSumByExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_count_sum_by_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array count/sum by", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayNoneExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_none_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array none", "# pass 5", "# fail 0", "1..5"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayAllEqualExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_all_equal_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array all_equal", "# pass 7", "# fail 0", "1..7"} {
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

func TestRunnerArrayMinMaxIndexExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_min_max_index_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array min/max index", "# pass 6", "# fail 0", "1..6"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayBinarySearchExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_binary_search_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array binary_search", "# pass 10", "# fail 0", "1..10"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayDedupExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_dedup_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array dedup", "# pass 6", "# fail 0", "1..6"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayAccessorsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_accessors_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array accessors", "# pass 9", "# fail 0", "1..9"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayRotateExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_rotate_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array rotate", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayBatchExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_batch_test.fern")
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

func TestRunnerIterCombinatorsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/iter_combinators_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: core/iter combinators", "# pass 12", "# fail 0", "1..12"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerNumReducersExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/num_reducers_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/num reducers", "# pass 10", "# fail 0", "1..10"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerSelfTestPasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/runner_self_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("self-test exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "# pass 61") || !strings.Contains(out, "# fail 0") {
		t.Errorf("expected 61 passes, 0 fails\noutput:\n%s", out)
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

func TestRunnerAsyncCombinatorsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/async_combinators_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "# pass 6") || !strings.Contains(out, "# fail 0") {
		t.Errorf("expected 6 passes, 0 fails\noutput:\n%s", out)
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

func TestRunnerSkipAndSubsuitesExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/skip_and_subsuites_test.fern")
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

func TestRunnerFuzzExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/fuzz_example_test.fern")
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

func TestRunnerProcessAssertionsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/process_assertions_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if strings.Contains(out, "# SKIP sh / echo / cat not on $PATH") {
		t.Skip("fixture self-skipped: sh / echo / cat not on $PATH")
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

func TestRunnerWideNumericsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/wide_numerics_test.fern")
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

func TestRunnerFilesystemOpsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/filesystem_ops_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - temp_dir returns absolute path",
		"ok 2 - read_dir lists what we wrote",
		"ok 4 - remove_file on missing target errors",
		"ok 5 - remove_dir_all on missing target is ok",
		"ok 6 - create_dir_all builds a missing chain",
		"ok 7 - create_dir_all on an existing path is ok",
		"ok 8 - create_dir_all under a file errors",
		"# pass 8",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
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

func TestRunnerFernBinaryE2EExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/fern_binary_e2e_test.fern")

	t.Run("with FERN_BIN set", func(t *testing.T) {
		cmd := exec.Command(bin, "-interp", src)
		cmd.Env = append(os.Environ(), "FERN_BIN="+bin)
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

	t.Run("without FERN_BIN — skips cleanly", func(t *testing.T) {
		cmd := exec.Command(bin, "-interp", src)
		env := []string{}
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "FERN_BIN=") {
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
			"# SKIP FERN_BIN not set",
			"# skip 1",
		} {
			if !strings.Contains(gotOut, w) {
				t.Errorf("stdout missing %q\nfull output:\n%s", w, gotOut)
			}
		}
		if strings.Contains(gotOut, "# fail 1") {
			t.Errorf("missing FERN_BIN should skip, not fail\noutput:\n%s", gotOut)
		}
	})
}

func TestRunnerHelpersExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/helpers_test.fern")
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

func TestRunnerFloatExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/float_test.fern")
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

func TestRunnerFuzzShrinkExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/fuzz_shrink_test.fern")
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

func TestRunnerBatch7Example(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/batch7_test.fern")
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

func TestRunnerBatch8Example(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/batch8_test.fern")

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

func TestRunnerFloatMathExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/float_math_test.fern")
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

func TestRunnerFloatConvertExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/float_convert_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/f64 convert", "# pass 11", "# fail 0", "1..11"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerFloatRoundToExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/float_round_to_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/f64 round_to", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerFloatLog2Log10Example(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/float_log2_log10_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/f64 log2/log10", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerFloatExp2Exp10Example(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/float_exp2_exp10_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/f64 exp2/exp10", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerFloatRecipCopysignMidpointExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/float_recip_copysign_midpoint_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/f64 recip/copysign/midpoint", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerI64RootsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/i64_roots_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/i64 roots", "# pass 8", "# fail 0", "1..8"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerI64IntdivExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/i64_intdiv_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/i64 intdiv", "# pass 5", "# fail 0", "1..5"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerU64RootsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/u64_roots_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/u64 roots", "# pass 6", "# fail 0", "1..6"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerFloatClamp01AbsDiffMulAddExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/float_clamp01_absdiff_muladd_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/f64 clamp01/abs_diff/mul_add", "# pass 4", "# fail 0", "1..4"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerU32RootsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/u32_roots_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/u32 roots", "# pass 6", "# fail 0", "1..6"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerI32RootsPrimeExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/i32_roots_prime_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/i32 roots + prime", "# pass 5", "# fail 0", "1..5"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerFloatCbrtHypot3Example(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/float_cbrt_hypot3_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/f64 cbrt/hypot3", "# pass 6", "# fail 0", "1..6"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerFloatHyperbolicExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/float_hyperbolic_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/f64 sinh/cosh/tanh", "# pass 6", "# fail 0", "1..6"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayStatsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_stats_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array f64 stats", "# pass 5", "# fail 0", "1..5"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerSortF64Example(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/sort_f64_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: cmp.sort f64", "# pass 5", "# fail 0", "1..5"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayMedianRangeExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_median_range_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array f64 median/range", "# pass 5", "# fail 0", "1..5"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayVectorExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_vector_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array f64 vector", "# pass 6", "# fail 0", "1..6"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayDistanceNormalizeExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_distance_normalize_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array f64 distance/normalize", "# pass 5", "# fail 0", "1..5"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayProductCumsumExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_product_cumsum_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array f64 product/cumsum", "# pass 5", "# fail 0", "1..5"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayScaleAddExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_scale_add_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array f64 scale/add", "# pass 6", "# fail 0", "1..6"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayCumprodDiffExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_cumprod_diff_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/array f64 cumprod/diff", "# pass 5", "# fail 0", "1..5"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerIntMidpointExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/int_midpoint_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/int midpoint", "# pass 5", "# fail 0", "1..5"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerUintMidpointExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/uint_midpoint_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/uint midpoint", "# pass 4", "# fail 0", "1..4"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerFloatHypotExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/float_hypot_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/f64 hypot", "# pass 12", "# fail 0", "1..12"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerTimingExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/timing_test.fern")
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

func TestRunnerLinesLogExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/lines_log_test.fern")
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

func TestRunnerFuzzCorpusExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/fuzz_corpus_test.fern")
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

func TestRunnerBenchExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/runner_bench_test.fern")
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

func TestRunnerArrayReductionsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_reductions_test.fern")
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

func TestRunnerArrayCombinatorsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_combinators_test.fern")
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
		"ok 23 - find_map some",
		"ok 24 - find_map none",
		"# pass 24",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerArrayStructuralVerbsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_structural_verbs_test.fern")
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

func TestRunnerLogExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/log_test.fern")
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
		"ok 12 - sink receives each line",
		"ok 13 - sink keeps level and mode",
		"# pass 13",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerMetricsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/metrics_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 4 - histogram buckets are cumulative",
		"ok 7 - server metrics by method and status",
		"ok 8 - merge sums samples",
		"ok 11 - scrape answers",
		"# pass 11",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerMapVerbsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/map_verbs_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"ok 1 - entries sum k+v",
		"ok 4 - from pairs",
		"ok 7 - merge other wins",
		"ok 11 - get_or_insert word count",
		"ok 12 - update word count",
		"ok 14 - contains_value",
		"# pass 15",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerSetEqExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/set_eq_test.fern")
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

func TestRunnerEnvUnreachableExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/env_unreachable_test.fern")
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

func TestRunnerRelTolAndMsBenchExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/rel_tol_and_ms_bench_test.fern")
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

func TestRunnerSortedUniqueRangeExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/sorted_unique_range_test.fern")
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

func TestRunnerFloatArrayStrictSortExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/float_array_strict_sort_test.fern")
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

func TestRunnerMapEqAndPredicatesExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/map_eq_and_predicates_test.fern")
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

func TestRunnerWiderArrayContainsCountExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/wider_array_contains_count_test.fern")
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

func TestRunnerOneOfNoneOfExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/one_of_none_of_test.fern")
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

func TestRunnerAllSubstringArrayExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/all_substring_array_test.fern")
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

func TestRunnerFileLinesAndTimestampExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/file_lines_and_timestamp_test.fern")
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

func TestRunnerOptionAndSetOpsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/option_and_set_ops_test.fern")
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

func TestRunnerArrayPrefixSuffixSubseqExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_prefix_suffix_subseq_test.fern")
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

func TestRunnerArrayAtAndF32RangeExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/array_at_and_f32_range_test.fern")
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

func TestRunnerCIStringAndLogKVExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/ci_string_and_log_kv_test.fern")
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

func TestRunnerResultAssertionsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/result_assertions_test.fern")
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

func TestRunnerProcessOutputShortcutsExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/process_output_shortcuts_test.fern")
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

func TestRunnerAssertAtWiderExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/assert_at_wider_test.fern")
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

func TestRunnerJSONDetailExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/json_detail_test.fern")
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

func TestRunnerJSONFieldEqExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/json_field_eq_test.fern")
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

func TestRunnerStringCountAndDirListingExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/string_count_and_dir_listing_test.fern")
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

func TestRunnerStringPreludeMigratedExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/string_prelude_migrated_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"# Suite: string prelude (migrated)",
		"ok 6 - s.to_upper() returns HELLO",
		"ok 7 - s.to_lower() returns hello",
		"ok 8 - string_from_bytes_unchecked(s.bytes()) round-trips",
		"# pass 8",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerUnionsMigratedExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/unions_migrated_test.fern")
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

func TestRunnerHeaderMapMigratedExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/header_map_migrated_test.fern")
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
		"ok 8 - set takes the caller's spelling in place",
		"ok 10 - names fold ASCII only",
		"ok 13 - unquote undoes a quoted string",
		"ok 15 - weight reads a qvalue",
		"ok 16 - remove drops every line of a name",
		"# pass 16",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerHttpRequestBytesExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/http_request_bytes_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: HTTP request bytes", "# pass 25", "# fail 0", "1..25"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerHttpChunkDecoderExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/http_chunk_decoder_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: http.ChunkDecoder against the parser", "# pass 15", "# fail 0", "1..15"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerHttpBodyJsonExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/http_body_json_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: Typed JSON bodies", "# pass 6", "# fail 0", "1..6"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerHttpBodyExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/http_body_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: HttpResponse bodies", "# pass 7", "# fail 0", "1..7"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerMockPlatformCannedExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/mock_platform_canned_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: mock_platform.MockPlatform canned answers", "# pass 11", "# fail 0", "1..11"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerHttpRespondExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/http_respond_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: HTTP respond", "# pass 17", "# fail 0", "1..17"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerHttpRequestBuilderExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/http_request_builder_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: HTTP request builder", "# pass 5", "# fail 0", "1..5"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerHttpRequestHeadersMigratedExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/http_request_headers_migrated_test.fern")
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
		"ok 8 - Transfer-Encoding beside Content-Length rejected",
		"ok 9 - http_header_value via HeaderMap",
		"ok 10 - http_header_value missing returns None",
		"ok 11 - http_header_value refuses a malformed block",
		"ok 12 - a request with no headers parses",
		"# pass 13",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
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

func TestRunnerHttpRequestBodyStreamExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/http_request_body_stream_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"# Suite: HttpRequest.body as a Stream",
		"ok 1 - body_stream carries the body",
		"ok 4 - read_line walks the body",
		"ok 6 - read_n leaves the remainder",
		"ok 7 - body_string ignores a partial walk",
		"ok 10 - a bodyless request reads empty",
		"ok 13 - read_line spans a chunk boundary",
		"ok 15 - read_n pulls only what it owes",
		"ok 19 - a fault is EndedEarly with its status",
		"# pass 22",
		"# fail 0",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
}

func TestRunnerHttpResponseHeadersMigratedExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/http_response_headers_migrated_test.fern")
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
		"ok 9 - HEAD response has no body",
		"ok 10 - 204 has no Content-Length or body",
		"ok 11 - Connection field where it carries meaning",
		"# pass 12",
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

func TestRunnerFailFastExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/fail_fast_test.fern")
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

func TestRunnerQuietModeExample(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/quiet_mode_test.fern")
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

func TestRunnerOrdmapExamplePasses(t *testing.T) {
	runPersistentSuite(t, "tests/stdlib/ordmap_test.fern", "std/ordmap", "ok 4 - insert is persistent", 13)
}

func TestRunnerPmapExamplePasses(t *testing.T) {
	runPersistentSuite(t, "tests/stdlib/pmap_test.fern", "std/pmap", "ok 4 - insert is persistent", 11)
}

func TestRunnerPvecExamplePasses(t *testing.T) {
	runPersistentSuite(t, "tests/stdlib/pvec_test.fern", "std/pvec", "ok 3 - with is persistent", 10)
}

func TestRunnerOrdsetExamplePasses(t *testing.T) {
	runPersistentSuite(t, "tests/stdlib/ordset_test.fern", "std/ordset", "ok 2 - insert is persistent", 6)
}

func TestRunnerPsetExamplePasses(t *testing.T) {
	runPersistentSuite(t, "tests/stdlib/pset_test.fern", "std/pset", "ok 2 - insert is persistent", 5)
}

func TestRunnerCollectionVocabularyPasses(t *testing.T) {
	runPersistentSuite(t, "tests/stdlib/collection_vocabulary_test.fern", "collection vocabulary", "ok 4 - JsonObject", 5)
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

func TestRunnerDnsExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "tests/stdlib/dns_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"# Suite: std/dns", "1..36", "# pass 36", "# fail 0"} {
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
