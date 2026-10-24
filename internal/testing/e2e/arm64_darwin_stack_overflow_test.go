package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestArm64DarwinStackOverflow(t *testing.T) {
	bin := buildFernCLI(t)
	report := regexp.MustCompile(`^fern: stack overflow\nbacktrace:\n(  0x[0-9a-f]{16}\n)+$`)
	cases := []struct {
		name, src string
		check     func(t *testing.T, stdout, stderr string, ps *os.ProcessState)
	}{
		{"runaway-recursion", "function f(n: i32): i32 {\n  return 1 + f(n - 1);\n}\nfunction main(): i32 {\n  return f(1);\n}\n",
			func(t *testing.T, stdout, stderr string, ps *os.ProcessState) {
				if ps.ExitCode() != 134 || !report.MatchString(stderr) {
					t.Errorf("exit %d, stderr %q; want 134 and a stack-overflow report with a backtrace", ps.ExitCode(), stderr)
				}
			}},
		{"million-deep", "import \"std/i32\";\nfunction f(n: i32): i32 {\n  if (n == 0) {\n    return 0;\n  }\n  return 1 + f(n - 1);\n}\nfunction main(): i32 {\n  print(f(1000000).to_string());\n  return 0;\n}\n",
			func(t *testing.T, stdout, stderr string, ps *os.ProcessState) {
				if ps.ExitCode() != 0 || stdout != "1000000\n" {
					t.Errorf("exit %d, stdout %q, stderr %q; want 1000000", ps.ExitCode(), stdout, stderr)
				}
			}},
		{"other-fault", "function main(): i32 {\n  return __load_i32(8 as usize);\n}\n",
			func(t *testing.T, stdout, stderr string, ps *os.ProcessState) {
				ws, _ := ps.Sys().(syscall.WaitStatus)
				if !ws.Signaled() || (ws.Signal() != syscall.SIGSEGV && ws.Signal() != syscall.SIGBUS) || strings.Contains(stderr, "fern:") {
					t.Errorf("state %v, stderr %q; want a bare SIGSEGV or SIGBUS", ps, stderr)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "prog")
			if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
				t.Fatalf("arm64-darwin build failed: %v\n%s", err, o)
			}
			if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
				t.Skip("execution check only runs on Apple Silicon")
			}
			cmd := exec.Command(out)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			_ = cmd.Run()
			tc.check(t, stdout.String(), stderr.String(), cmd.ProcessState)
		})
	}
}
