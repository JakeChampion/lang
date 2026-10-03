package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// `fern -cover` on the self-host (examples/self_host/cover.fern). The report
// contract on both Linux targets is internal/e2e/cover_test.go; these pin what
// it does not reach: the refusals, FERN_COVER=1, the `||` edge, a lambda body,
// and a `main` with no result.

// coverRun compiles src for the host with the self-host CLI, adding `args`
// and `env`, runs the binary, and returns its stderr with the entry path
// replaced by ENTRY.
func coverRun(t *testing.T, src string, args, env []string) string {
	t.Helper()
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
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
	return strings.ReplaceAll(stderr.String(), srcPath, "ENTRY")
}

// TestSelfHostCoverEdgesAndLambdas: `||`'s T counts the short-circuit, so a
// call whose left operand is true takes it and one whose left operand is
// false does not; a lambda body's line counts its calls; and a `main` with no
// result still reports when it returns.
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

// TestSelfHostCoverFromEnvironment: FERN_COVER=1 instruments a build as the
// flag does, which is how a harness reaches a compile it does not spell.
func TestSelfHostCoverFromEnvironment(t *testing.T) {
	const src = "function main(): i32 {\n    return 0;\n}\n"
	if report := coverRun(t, src, nil, []string{"FERN_COVER=1"}); !strings.Contains(report, "fern-cover: ENTRY:2 1\n") {
		t.Errorf("FERN_COVER=1 build reports:\n%s", report)
	}
	if report := coverRun(t, src, nil, nil); report != "" {
		t.Errorf("an uninstrumented build wrote to stderr:\n%s", report)
	}
}

// TestSelfHostCoverRefusals: a target with no instrumentation, and the
// interpreter, which lowers nothing, refuse -cover rather than run silently.
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
