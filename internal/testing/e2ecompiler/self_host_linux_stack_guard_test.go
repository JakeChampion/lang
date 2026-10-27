//go:build linux

package e2ecompiler

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// startupDenial makes one startup system call fail through the seccomp
// launcher in testdata/stack_guard_errno.c and checks that the program stops
// before main with the expected message and exit status 134. The same
// program runs without the launcher first, as the positive control.
// QEMU user mode emulates sigaltstack and rt_sigaction without a host
// system call, so under QEMU those two modes verify the emitted check and
// leave the run to a native arm64 host. check is the instruction to find.
func startupDenial(t *testing.T, mode, want, check string) {
	t.Helper()
	cli := e2eharness.SelfHostCLI(t)
	dir := t.TempDir()
	launcher := filepath.Join(dir, "startup-errno")
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
					args = append([]string{launcher, mode}, argv...)
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
			if len(runner) > 0 && check != "" {
				asmPath := filepath.Join(dir, target+".s")
				if out, err := exec.Command(cli, "-emit", "asm", "-target", target, "-o", asmPath, src, e2eharness.SelfHostStdlibRoot(t)).CombinedOutput(); err != nil {
					t.Fatalf("emit asm: %v\n%s", err, out)
				}
				asm, err := os.ReadFile(asmPath)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(asm), check) {
					t.Fatalf("the %s startup has no %q check", target, check)
				}
				t.Skipf("QEMU user mode emulates this system call itself; the %s denial runs on a native arm64 host", mode)
			}
			if out, errout, code := run(true); code != 134 || out != "" || errout != want {
				t.Errorf("denied %s: exit=%d stdout=%q stderr=%q; want 134 and %q", mode, code, out, errout, want)
			}
		})
	}
}

func TestSelfHostStackGuardDenial(t *testing.T) {
	startupDenial(t, "guard", "fern: could not protect the stack guard\n", "")
}

func TestSelfHostSignalStackMapDenial(t *testing.T) {
	startupDenial(t, "signal-stack-map", "fern: could not map the signal stack\n", "")
}

func TestSelfHostSignalStackDenial(t *testing.T) {
	startupDenial(t, "signal-stack", "fern: could not install the signal stack\n", "cbnz x0, .Lfern_signal_stack_failed")
}

func TestSelfHostFaultHandlerDenial(t *testing.T) {
	startupDenial(t, "fault-handler", "fern: could not install the fault handler\n", "cbnz x0, .Lfern_fault_handler_failed")
}
