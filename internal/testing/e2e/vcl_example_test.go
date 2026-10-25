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

func TestVCLExampleFormatterIsAFixedPoint(t *testing.T) {
	bin := buildLangBinForInterp(t)

	code, once, errOut := runVclfmt(t, bin, "testdata/sample.vcl")
	if code != 0 {
		t.Fatalf("formatting the sample: exit = %d, want 0\nstderr: %s", code, errOut)
	}
	if !strings.Contains(once, "sub vcl_recv {") {
		t.Fatalf("output does not look like VCL:\n%s", once)
	}

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

func TestVCLCompiledMatchesInterpreted(t *testing.T) {
	testVCLPolicyMatchesInterpreted(t, "testdata/policy.vcl", false)
}

func TestVCLRegexErrorsMatchInterpreted(t *testing.T) {
	for _, expression := range []string{
		`req.url ~ "("`,
		`req.url !~ "["`,
		`regsub(req.url, "(", "x") == "x"`,
		`regsuball(req.url, "[", "x") == "x"`,
	} {
		t.Run(expression, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-regex.vcl")
			policy := `vcl 4.1; backend origin { .host = "127.0.0.1"; }
sub vcl_recv { if (` + expression + `) { return (pass); } return (hash); }`
			if err := os.WriteFile(path, []byte(policy), 0o644); err != nil {
				t.Fatal(err)
			}
			testVCLPolicyMatchesInterpreted(t, path, true)
		})
	}
}

func testVCLPolicyMatchesInterpreted(t *testing.T, policyPath string, regexError bool) {
	t.Helper()
	bin := buildLangBinForInterp(t)
	vclDir := langSrcAbs(t, "examples/vcl")
	outDir := t.TempDir()

	prefix, err := filepath.Rel(outDir, vclDir)
	if err != nil {
		t.Fatalf("relative path from %s to %s: %v", outDir, vclDir, err)
	}
	prefix = filepath.ToSlash(prefix) + "/"

	// Compile testdata/policy.vcl with imports pointing back at the runtime.
	compileCmd := exec.Command(bin, "-interp", "vclc.fern", "--", "-p", prefix, policyPath)
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
	if regexError {
		cases = [][]string{{"GET", "/"}}
	}
	for _, args := range cases {
		args := args
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			interpArgs := append([]string{"-interp", "vclrun.fern", "--", policyPath}, args...)
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

			if regexError && (iCode == 0 || !strings.Contains(iErr.String(), "regex byte") || !strings.Contains(cErr.String(), "regex byte")) {
				t.Fatalf("regex error missing: interpreted exit %d stderr %s; compiled exit %d stderr %s", iCode, iErr.String(), cCode, cErr.String())
			}
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
