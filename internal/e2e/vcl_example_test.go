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

// TestVCLExampleTapSuitePasses runs the front end's in-language TAP suite.
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

// TestVCLBackendTapSuitePasses runs the evaluator's TAP suite: VCL's
// coercion rules, ACL matching, the per-subroutine variable scoping, and
// the request state machine.
func TestVCLBackendTapSuitePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/vcl/vclbackend_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"TAP version 13", "# Suite: vcl-backend", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
	if strings.Contains(out, "not ok") {
		t.Errorf("a case failed:\n%s", out)
	}
}

// TestVCLCheckTapSuitePasses runs the static checker's TAP suite.
func TestVCLCheckTapSuitePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/vcl/vclcheck_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"TAP version 13", "# Suite: vcl-check", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
	if strings.Contains(out, "not ok") {
		t.Errorf("a case failed:\n%s", out)
	}
}

// runVclcheck runs the checker driver from the example's own directory.
func runVclcheck(t *testing.T, bin string, args ...string) (int, string, string) {
	t.Helper()
	argv := append([]string{"-interp", "vclcheck.fern", "--"}, args...)
	cmd := exec.Command(bin, argv...)
	cmd.Dir = langSrcAbs(t, "examples/vcl")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode(), out.String(), errb.String()
}

// TestVCLCheckAcceptsTheSamplePolicy pins that the shipped policy is
// clean: a checker that rejected it would make every other gate here
// meaningless.
func TestVCLCheckAcceptsTheSamplePolicy(t *testing.T) {
	bin := buildLangBinForInterp(t)
	code, out, errOut := runVclcheck(t, bin, "testdata/policy.vcl")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "ok: testdata/policy.vcl") {
		t.Errorf("expected an ok line:\n%s", out)
	}
}

// TestVCLCheckReportsEveryProblem pins the checker's two distinctive
// properties on one file: it reports ALL problems rather than the first,
// and it finds a helper's illegal access under the entry point that makes
// it illegal — `tag_it` reads `req.url`, which is fine in principle and
// wrong because `vcl_backend_response` is what calls it.
func TestVCLCheckReportsEveryProblem(t *testing.T) {
	bin := buildLangBinForInterp(t)
	bad := filepath.Join(t.TempDir(), "broken.vcl")
	src := `vcl 4.1;
backend origin { .host = "127.0.0.1"; }
sub tag_it { set beresp.http.X-Tag = req.url; }
sub vcl_recv {
    set resp.http.X = "1";
    return (deliver);
}
sub vcl_backend_response { call tag_it; return (deliver); }
`
	if err := os.WriteFile(bad, []byte(src), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	code, out, errOut := runVclcheck(t, bin, bad)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{
		"[vcl_recv] 'resp.http.X' is not writable in vcl_recv",
		"[vcl_recv] 'deliver' is not a valid return from vcl_recv",
		"[vcl_backend_response] 'req.url' is not readable in vcl_backend_response",
	} {
		if !strings.Contains(errOut, w) {
			t.Errorf("missing %q\nfull stderr:\n%s", w, errOut)
		}
	}
}

// runVclrun runs the evaluator driver from the example's own directory so
// its relative testdata path resolves.
func runVclrun(t *testing.T, bin string, args ...string) (int, string, string) {
	t.Helper()
	argv := append([]string{"-interp", "vclrun.fern", "--"}, args...)
	cmd := exec.Command(bin, argv...)
	cmd.Dir = langSrcAbs(t, "examples/vcl")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode(), out.String(), errb.String()
}

// TestVCLBackendCachesAndPurges drives the sample policy end to end
// through the driver: the first request misses and stores, the second
// hits what it stored. This is the whole point of the state machine, so
// it is worth pinning outside the in-language suite too.
func TestVCLBackendCachesAndPurges(t *testing.T) {
	bin := buildLangBinForInterp(t)
	code, out, errOut := runVclrun(t, bin, "testdata/policy.vcl", "GET", "/static/app.css", "-n", "2")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "deliver (miss)") {
		t.Errorf("first request should miss:\n%s", out)
	}
	if !strings.Contains(out, "deliver (HIT)") {
		t.Errorf("second request should hit what the first stored:\n%s", out)
	}
	if !strings.Contains(out, "vcl_recv -> vcl_hash -> vcl_hit -> vcl_deliver") {
		t.Errorf("hit should take the vcl_hit path:\n%s", out)
	}
}

// TestVCLBackendEnforcesACL pins that the sample policy's purge ACL both
// admits and rejects, including the exclusion that a first-match-wins
// walk would wrongly admit — `192.0.2.23` is inside the `192.0.2.0/24`
// the ACL also lists.
func TestVCLBackendEnforcesACL(t *testing.T) {
	bin := buildLangBinForInterp(t)
	for _, tc := range []struct {
		ip   string
		want string
	}{
		{"192.0.2.10", "200 Purged"},
		{"127.0.0.1", "200 Purged"},
		{"192.0.2.23", "405 Not allowed"},
		{"10.0.0.5", "405 Not allowed"},
	} {
		code, out, errOut := runVclrun(t, bin, "testdata/policy.vcl", "PURGE", "/static/app.css", tc.ip)
		if code != 0 {
			t.Fatalf("%s: exit = %d, want 0\nstderr: %s", tc.ip, code, errOut)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: want status %q, got:\n%s", tc.ip, tc.want, out)
		}
	}
}

// TestVCLBackendRejectsOutOfScopeVariable pins the scoping rule VCL
// authors trip over most: `req` is not readable while fetching from a
// backend. The driver must exit 1 and name both the variable and the
// subroutine.
func TestVCLBackendRejectsOutOfScopeVariable(t *testing.T) {
	bin := buildLangBinForInterp(t)
	bad := filepath.Join(t.TempDir(), "scope.vcl")
	src := "vcl 4.1;\nbackend o { .host = \"127.0.0.1\"; }\n" +
		"sub vcl_backend_response { set beresp.http.X = req.url; return (deliver); }\n"
	if err := os.WriteFile(bad, []byte(src), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	code, out, errOut := runVclrun(t, bin, bad, "GET", "/")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "'req.url' is not readable in vcl_backend_response") {
		t.Errorf("diagnostic should name the variable and the subroutine:\n%s", errOut)
	}
}
