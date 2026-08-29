package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// TestVCLCompileTapSuitePasses runs the compiler's TAP suite, which pins
// the SHAPE of the generated source.
func TestVCLCompileTapSuitePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := langSrcAbs(t, "examples/vcl/vclcompile_test.fern")
	code, out, errOut := runLangInterp(t, bin, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, w := range []string{"TAP version 13", "# Suite: vcl-compile", "# fail 0"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout missing %q\nfull output:\n%s", w, out)
		}
	}
	if strings.Contains(out, "not ok") {
		t.Errorf("a case failed:\n%s", out)
	}
}

// TestVCLCompiledMatchesInterpreted is the gate the compiler actually
// stands on: compile the sample policy to Fern, run the result, and diff
// its output against the interpreter's over a set of requests chosen to
// take different paths through the state machine.
//
// A string assertion on generated source can only say the compiler emitted
// what was intended. This says the emitted code MEANS the same thing —
// which is the only claim worth making about a compiler, and the one that
// catches a codegen table drifting from the runtime one it mirrors.
//
// The generated module goes in a temp dir with an import prefix pointing
// back at examples/vcl, so nothing is written into the source tree.
func TestVCLCompiledMatchesInterpreted(t *testing.T) {
	bin := buildLangBinForInterp(t)
	vclDir := langSrcAbs(t, "examples/vcl")
	outDir := t.TempDir()

	prefix, err := filepath.Rel(outDir, vclDir)
	if err != nil {
		t.Fatalf("relative path from %s to %s: %v", outDir, vclDir, err)
	}
	prefix = filepath.ToSlash(prefix) + "/"

	// Compile testdata/policy.vcl with imports pointing back at the runtime.
	compileCmd := exec.Command(bin, "-interp", "vclc.fern", "--", "-p", prefix, "testdata/policy.vcl")
	compileCmd.Dir = vclDir
	var gen, genErr bytes.Buffer
	compileCmd.Stdout = &gen
	compileCmd.Stderr = &genErr
	if err := compileCmd.Run(); err != nil {
		t.Fatalf("vclc failed: %v\nstderr: %s", err, genErr.String())
	}
	genPath := filepath.Join(outDir, "policy.fern")
	if err := os.WriteFile(genPath, gen.Bytes(), 0o644); err != nil {
		t.Fatalf("write generated policy: %v", err)
	}

	// Each case takes a different route: cache hit, pass, uncacheable 5xx,
	// the built-in cookie pass, pipe on an unknown method, and every arm of
	// the purge ACL including the exclusion inside the /24.
	cases := [][]string{
		{"GET", "/static/app.css", "-n", "3"},
		{"GET", "/admin"},
		{"GET", "/status/503", "-n", "2"},
		{"GET", "/", "Cookie:a=1"},
		{"GET", "/", "Authorization:Bearer_x"},
		{"FROBNICATE", "/"},
		{"PURGE", "/static/app.css", "192.0.2.10"},
		{"PURGE", "/static/app.css", "192.0.2.23"},
		{"PURGE", "/static/app.css", "10.0.0.5"},
		{"PURGE", "/static/app.css", "127.0.0.1"},
		{"HEAD", "/static/x.css", "-n", "2"},
	}
	for _, args := range cases {
		args := args
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			interpArgs := append([]string{"-interp", "vclrun.fern", "--", "testdata/policy.vcl"}, args...)
			ic := exec.Command(bin, interpArgs...)
			ic.Dir = vclDir
			var iOut, iErr bytes.Buffer
			ic.Stdout = &iOut
			ic.Stderr = &iErr
			_ = ic.Run()
			iCode := ic.ProcessState.ExitCode()

			compArgs := append([]string{"-interp", genPath, "--"}, args...)
			cc := exec.Command(bin, compArgs...)
			cc.Dir = outDir
			var cOut, cErr bytes.Buffer
			cc.Stdout = &cOut
			cc.Stderr = &cErr
			_ = cc.Run()
			cCode := cc.ProcessState.ExitCode()

			if iCode != cCode {
				t.Errorf("exit differs: interpreted %d, compiled %d\ninterp stderr: %s\ncompiled stderr: %s",
					iCode, cCode, iErr.String(), cErr.String())
			}
			if iOut.String() != cOut.String() {
				t.Errorf("output differs.\ninterpreted:\n%s\ncompiled:\n%s", iOut.String(), cOut.String())
			}
		})
	}
}

// hostFernTarget names the fern target that produces a binary this machine
// can execute.
func hostFernTarget(t *testing.T) string {
	t.Helper()
	switch {
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		return "arm64-darwin"
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
		return "x86-64-linux"
	case runtime.GOOS == "linux" && runtime.GOARCH == "arm64":
		return "arm64-linux"
	default:
		t.Skipf("no fern target for %s/%s", runtime.GOOS, runtime.GOARCH)
		return ""
	}
}

// TestVCLCompiledPolicyBuildsNatively pins that a compiled policy is a
// real program: it builds to a native binary and that binary behaves like
// the interpreter. It builds for the HOST architecture so the binary can
// actually be run, whichever runner this lands on.
func TestVCLCompiledPolicyBuildsNatively(t *testing.T) {
	if testing.Short() {
		t.Skip("native build is slow; skipped under -short")
	}
	bin := buildLangBinForInterp(t)
	vclDir := langSrcAbs(t, "examples/vcl")
	outDir := t.TempDir()

	prefix, err := filepath.Rel(outDir, vclDir)
	if err != nil {
		t.Fatalf("relative path: %v", err)
	}
	prefix = filepath.ToSlash(prefix) + "/"

	compileCmd := exec.Command(bin, "-interp", "vclc.fern", "--", "-p", prefix, "testdata/policy.vcl")
	compileCmd.Dir = vclDir
	var gen, genErr bytes.Buffer
	compileCmd.Stdout = &gen
	compileCmd.Stderr = &genErr
	if err := compileCmd.Run(); err != nil {
		t.Fatalf("vclc failed: %v\nstderr: %s", err, genErr.String())
	}
	genPath := filepath.Join(outDir, "policy.fern")
	if err := os.WriteFile(genPath, gen.Bytes(), 0o644); err != nil {
		t.Fatalf("write generated policy: %v", err)
	}

	// The binary is RUN, so it has to be built for the host: this gate runs
	// on both x86-64 and aarch64 runners, and a cross-built binary fails
	// with "exec format error" rather than telling us anything.
	target := hostFernTarget(t)
	exePath := filepath.Join(outDir, "policy")
	build := exec.Command(bin, "-target", target, "-o", exePath, genPath)
	build.Dir = outDir
	if o, err := build.CombinedOutput(); err != nil {
		t.Fatalf("native build of the compiled policy failed: %v\n%s", err, o)
	}

	native := exec.Command(exePath, "GET", "/static/app.css", "-n", "2")
	native.Dir = outDir
	nOut, err := native.Output()
	if err != nil {
		t.Fatalf("running the compiled policy: %v", err)
	}

	interp := exec.Command(bin, "-interp", "vclrun.fern", "--", "testdata/policy.vcl",
		"GET", "/static/app.css", "-n", "2")
	interp.Dir = vclDir
	iOut, err := interp.Output()
	if err != nil {
		t.Fatalf("running the interpreter: %v", err)
	}

	if string(nOut) != string(iOut) {
		t.Errorf("native binary differs from the interpreter.\nnative:\n%s\ninterpreted:\n%s", nOut, iOut)
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
