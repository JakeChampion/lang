package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func coverRun(t *testing.T, src string, args, env []string) string {
	t.Helper()
	report, srcPath := coverRunRaw(t, src, args, env)
	return strings.ReplaceAll(report, srcPath, "ENTRY")
}

func coverRunRaw(t *testing.T, src string, args, env []string) (report, srcPath string) {
	t.Helper()
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	srcPath = filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "prog")
	cmd := exec.Command(h.cli, append(append([]string{"-target", h.targets[0].target}, args...), "-o", exe, srcPath, h.stdlib)...)
	cmd.Env = e2eharness.ChildEnv(env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	var stderr bytes.Buffer
	run := exec.Command(exe)
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("run: %v\n%s", err, stderr.String())
	}
	return stderr.String(), srcPath
}

func TestSelfHostCoverFeedsChangeRisk(t *testing.T) {
	const src = `function pick(n: i32): i32 {
    if (n > 0) { return 1; }
    if (n < 0) { return 2; }
    return 0;
}
function main(): i32 {
    return pick(1) - 1;
}
`
	report, srcPath := coverRunRaw(t, src, []string{"-cover"}, nil)
	cov := filepath.Join(t.TempDir(), "cov.txt")
	if err := os.WriteFile(cov, []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}
	h := selfHostCLIForHost(t)
	cmd := exec.Command(h.cli, "-lint", "-color", "never", "-lint-cover", cov, "-lint-opt", "change-risk.max=1", srcPath)
	out, _ := cmd.CombinedOutput()
	want := srcPath + ":1:1: warning[change-risk]: function `pick` has a change risk of 6: complexity 3 with 33.3% of its 3 lines covered, over the limit of 1\n"
	if cmd.ProcessState.ExitCode() != 0 || !strings.HasPrefix(string(out), want) || strings.Contains(string(out), "`main`") {
		t.Errorf("exit %d\n--- got ---\n%s--- want a report starting ---\n%s--- run ---\n%s", cmd.ProcessState.ExitCode(), out, want, report)
	}
}

func TestSelfHostCoverEdgesAndLambdas(t *testing.T) {
	const src = `function pick(a: i32, b: i32): i32 {
    if (a > 0 || b > 0) { return 1; }
    return 0;
}
function main(): void {
    let inc: (i32) => i32 = (x: i32): i32 => {
        return x + 1;
    };
    let n: i32 = pick(1, 0) + pick(0, 0) + inc(1) + inc(2);
    if (n < 0) { print("negative"); }
}
`
	report := coverRun(t, src, []string{"-cover"}, nil)
	for _, want := range []string{
		"fern-branch: ENTRY:2:15 E 2\n",
		"fern-branch: ENTRY:2:15 T 1\n",
		"fern-cover: ENTRY:7 2\n",
		"fern-cover: ENTRY:10 1\n",
		"fern-branch: ENTRY:10:9 T 0\n",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
}

func TestSelfHostCoverFromEnvironment(t *testing.T) {
	const src = "function main(): i32 {\n    return 0;\n}\n"
	if report := coverRun(t, src, nil, []string{"FERN_COVER=1"}); !strings.Contains(report, "fern-cover: ENTRY:2 1\n") {
		t.Errorf("FERN_COVER=1 build reports:\n%s", report)
	}
	if report := coverRun(t, src, nil, nil); report != "" {
		t.Errorf("an uninstrumented build wrote to stderr:\n%s", report)
	}
}

func TestSelfHostCoverRefusals(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte("function main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"wasm", []string{"-target", "wasm32-wasi", "-cover", "-o", filepath.Join(dir, "p.wasm")}, "-target wasm32-wasi has no coverage instrumentation"},
		{"interp", []string{"-interp", "-cover"}, "-interp does not lower"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(h.cli, append(tc.args, srcPath, h.stdlib)...)
			cmd.Env = e2eharness.ChildEnv()
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("-cover was accepted:\n%s", out)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("refusal does not say %q:\n%s", tc.want, out)
			}
		})
	}
}
