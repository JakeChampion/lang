package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `examples/vcl/` is a front end for a language that is not Fern: a
// lexer, parser, AST, and printer for the Varnish Configuration Language,
// written in Fern and depending on nothing outside `std`.
//
// It is a gate as well as an example. A parser is the densest user of the
// features a self-hosting language has to get right — recursive unions
// with struct payloads, exhaustive `match`, arrays of nodes grown by
// `append`, functional struct update in place of a mutable cursor — and
// it exercises them through the interpreter on every CI run, from a
// program nobody is tempted to shape around a compiler bug.
//
// The suites below are the two properties worth pinning: the TAP tests
// pass, and running the formatter over its own output changes nothing.

// TestVCLExampleTapSuitePasses runs the in-language TAP suite.
func TestVCLExampleTapSuitePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/vcl/vcl_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	wantSubstrings := []string{
		"TAP version 13",
		"# Suite: vcl",
		"# fail 0",
	}
	for _, w := range wantSubstrings {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
	if strings.Contains(out, "not ok") {
		t.Errorf("a case failed:\n%s", out)
	}
}

// runVclfmt runs the example driver over `arg`, from the example's own
// directory so its relative testdata path resolves.
func runVclfmt(t *testing.T, bin, arg string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin, "-interp", "vclfmt.fern", "--", arg)
	cmd.Dir = langSrcAbs(t, "examples/vcl")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode(), out.String(), errb.String()
}

// TestVCLExampleFormatterIsAFixedPoint pins the property the printer
// exists to have: formatting formatted output changes nothing. It runs
// over `testdata/sample.vcl`, which carries every construct the front end
// claims — all three `elseif` spellings, an inline probe, the four ACL
// entry forms, `backend … none`, inline C, and the long string forms.
//
// A round trip through a temp file rather than a second in-language
// assertion, because it also proves the driver's exit status: 0 means the
// sample parsed with no diagnostics at all.
func TestVCLExampleFormatterIsAFixedPoint(t *testing.T) {
	bin := buildLangBinForInterp(t)

	code, once, errOut := runVclfmt(t, bin, "testdata/sample.vcl")
	if code != 0 {
		t.Fatalf("formatting the sample: exit = %d, want 0\nstderr: %s", code, errOut)
	}
	if !strings.Contains(once, "sub vcl_recv {") {
		t.Fatalf("output does not look like VCL:\n%s", once)
	}

	// Feed the output back in. The driver takes a path, so the second
	// pass reads from a temp file next to the example's testdata.
	tmp := filepath.Join(t.TempDir(), "once.vcl")
	if err := os.WriteFile(tmp, []byte(once), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	code, twice, errOut := runVclfmt(t, bin, tmp)
	if code != 0 {
		t.Fatalf("reformatting: exit = %d, want 0\nstderr: %s", code, errOut)
	}
	if once != twice {
		t.Errorf("formatter is not a fixed point.\nfirst pass:\n%s\nsecond pass:\n%s", once, twice)
	}
}

// TestVCLExampleReportsBadSyntax pins the other half of the driver's
// contract: a file it cannot parse exits 1 and says where.
func TestVCLExampleReportsBadSyntax(t *testing.T) {
	bin := buildLangBinForInterp(t)
	bad := filepath.Join(t.TempDir(), "bad.vcl")
	badSrc := "vcl 4.1;\nsub vcl_recv {\n  set = 1;\n}\n"
	if err := os.WriteFile(bad, []byte(badSrc), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	code, out, errOut := runVclfmt(t, bin, bad)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(errOut, ":3:7:") {
		t.Errorf("diagnostic missing the error position:\n%s", errOut)
	}
}
