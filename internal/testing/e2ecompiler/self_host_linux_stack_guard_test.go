//go:build linux

package e2ecompiler

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostStackGuardDenial(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	dir := t.TempDir()
	launcher := filepath.Join(dir, "guard-errno")
	if out, err := exec.Command("gcc", "-Wall", "-Wextra", "-Werror", "-O2", "-o", launcher, "testdata/stack_guard_errno.c").CombinedOutput(); err != nil {
		t.Fatalf("build the seccomp test launcher: %v\n%s", err, out)
	}
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { print(\"main reached\"); return 42; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	targets := []string{"arm64-linux"}
	if runtime.GOARCH == "amd64" {
		targets = append([]string{"x86-64-linux"}, targets...)
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			var runner []string
			if target == "arm64-linux" {
				if qemu := e2eharness.Arm64Runner(t); qemu != "" {
					runner = []string{qemu}
				}
			}
			bin := filepath.Join(dir, target)
			if out, err := exec.Command(cli, "-target", target, "-o", bin, src, e2eharness.SelfHostStdlibRoot(t)).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			argv := append(append([]string{}, runner...), bin)
			run := func(denied bool) (string, string, int) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				args := argv
				if denied {
					args = append([]string{launcher}, argv...)
				}
				cmd := exec.CommandContext(ctx, args[0], args[1:]...)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err := cmd.Run()
				if ctx.Err() != nil || cmd.ProcessState == nil {
					t.Fatalf("run denied=%t: %v\n%s", denied, err, stderr.String())
				}
				return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
			}
			if out, errout, code := run(false); code != 42 || out != "main reached\n" || errout != "" {
				t.Fatalf("positive control: exit=%d stdout=%q stderr=%q", code, out, errout)
			}
			if out, errout, code := run(true); code != 134 || out != "" || errout != "fern: could not protect the stack guard\n" {
				t.Errorf("denied guard: exit=%d stdout=%q stderr=%q", code, out, errout)
			}
		})
	}
}
