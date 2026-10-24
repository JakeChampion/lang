package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
)

func TestSelfHostStackOverflow(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const runaway = "function f(n: i32): i32 {\n  return 1 + f(n - 1);\n}\nfunction main(): i32 {\n  return f(1);\n}\n"
	const deep = "import \"std/i32\";\nfunction f(n: i32): i32 {\n  if (n == 0) {\n    return 0;\n  }\n  return 1 + f(n - 1);\n}\nfunction main(): i32 {\n  print(f(1000000).to_string());\n  return 0;\n}\n"
	const wild = "function main(): i32 {\n  return __load_i32(8 as usize);\n}\n"
	report := regexp.MustCompile(`^fern: stack overflow\nbacktrace:\n(  0x[0-9a-f]{16}\n)+$`)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			t.Run("runaway-recursion", func(t *testing.T) {
				r := runStackProgram(t, cli, target, runaway)
				if r.code != 134 || !report.MatchString(r.stderr) {
					t.Errorf("exit %d, stderr %q; want 134 and a stack-overflow report with a backtrace", r.code, r.stderr)
				}
			})
			t.Run("million-deep", func(t *testing.T) {
				r := runStackProgram(t, cli, target, deep)
				if r.code != 0 || r.stdout != "1000000\n" {
					t.Errorf("exit %d, stdout %q, stderr %q; want 1000000", r.code, r.stdout, r.stderr)
				}
			})
			t.Run("other-fault", func(t *testing.T) {
				r := runStackProgram(t, cli, target, wild)
				if !r.segv || strings.Contains(r.stderr, "fern:") {
					t.Errorf("exit %d, signalled %t, stderr %q; want a bare SIGSEGV", r.code, r.segv, r.stderr)
				}
			})
		})
	}
}

type stackRun struct {
	stdout, stderr string
	code           int
	segv           bool
}

func runStackProgram(t *testing.T, cli *selfHostCLI, target, source string) stackRun {
	t.Helper()
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	var cmd *exec.Cmd
	if target == "arm64-linux" {
		_, qemu := arm64Tooling(t)
		cmd = runArm64Bin(qemu, cli.arm64Binary(t, src))
	} else {
		bin := filepath.Join(t.TempDir(), "prog")
		if msg, err := cli.compileCmd(target, bin, src).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, msg)
		}
		if err := os.Chmod(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd = runX86_64Bin(cli.runner, bin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	r := stackRun{stdout: out.String(), stderr: errb.String(), code: cmd.ProcessState.ExitCode()}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() && ws.Signal() == syscall.SIGSEGV {
		r.segv = true
	}
	if r.code == 139 {
		r.segv = true
	}
	return r
}
