package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func runBounded(cmd *exec.Cmd, timeout time.Duration) (timedOut bool, err error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return false, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return false, err
	case <-time.After(timeout):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		return true, nil
	}
}

func TestRunBoundedClassifiesTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX sleep/true/false")
	}
	if timedOut, err := runBounded(exec.Command("true"), 60*time.Second); timedOut || err != nil {
		t.Errorf("clean exit: timedOut=%v err=%v, want false and nil", timedOut, err)
	}
	if timedOut, err := runBounded(exec.Command("false"), 60*time.Second); timedOut || err == nil {
		t.Errorf("non-zero exit: timedOut=%v err=%v, want false and an error", timedOut, err)
	}
	if timedOut, err := runBounded(exec.Command("sleep", "30"), 100*time.Millisecond); !timedOut || err != nil {
		t.Errorf("outlived the wall: timedOut=%v err=%v, want true and nil", timedOut, err)
	}
	held := exec.Command("sh", "-c", "sleep 30 & sleep 30")
	held.Stdout = &bytes.Buffer{}
	result := make(chan bool, 1)
	go func() {
		timedOut, _ := runBounded(held, 100*time.Millisecond)
		result <- timedOut
	}()
	select {
	case timedOut := <-result:
		if !timedOut {
			t.Errorf("forked holder of stdout: timedOut=false, want true")
		}
	case <-time.After(10 * time.Second):
		t.Errorf("forked holder of stdout: runBounded did not return 10 s after a 100 ms wall; the group kill left the pipe open")
	}
}

func x86_64RunnerOrEmpty() (runner []string, ok bool) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return nil, true
	}
	if p, err := exec.LookPath("qemu-x86_64"); err == nil {
		return []string{p}, true
	}
	return nil, false
}

func TestRunSelfHostSeedBoundsAHungCompiler(t *testing.T) {
	saved := selfHostCompileTimeout
	selfHostCompileTimeout = 200 * time.Millisecond
	defer func() { selfHostCompileTimeout = saved }()
	fake := filepath.Join(t.TempDir(), "fern")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	const src = "function main(): i32 { return 0; }\n"
	legs := map[string]func(failFunc) (*selfHostRun, string){
		"x86-64": func(f failFunc) (*selfHostRun, string) {
			return runSelfHostSeed(t, fake, t.TempDir(), src, nil, nil, f)
		},
		"arm64": func(f failFunc) (*selfHostRun, string) {
			return runSelfHostArm64Seed(t, fake, t.TempDir(), "qemu-aarch64", src, nil, nil, f)
		},
		"wasm": func(f failFunc) (*selfHostRun, string) {
			return runSelfHostWasmSeed(t, fake, t.TempDir(), src, nil, nil, f)
		},
	}
	for name, run := range legs {
		failed := ""
		start := time.Now()
		r, gap := run(func(format string, args ...any) { failed = fmt.Sprintf(format, args...) })
		if r != nil || gap != "" || !strings.Contains(failed, "did not finish") {
			t.Errorf("%s: got run %v, gap %q, failure %q; want a reported hang", name, r, gap, failed)
		}
		if elapsed := time.Since(start); elapsed > 30*time.Second {
			t.Errorf("%s: returned after %v, want about the timeout", name, elapsed)
		}
	}
}

func TestBisectSelfHostSeedNamesTheDecision(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("links and runs x86-64 Linux asm")
	}
	fake := filepath.Join(t.TempDir(), "fern")
	script := `#!/bin/sh
while [ $# -gt 0 ]; do
  if [ "$1" = -o ]; then out=$2; fi
  shift
done
if [ -n "$FERN_OPT_TRACE" ]; then
  echo "opt-decision 10 inline a" >&2
  echo "opt-decision 20 reuse b" >&2
  echo "opt-decision 30 fuse c" >&2
fi
code=7
if [ "${FERN_OPT_BISECT:-0}" -gt 20 ]; then code=0; fi
printf '.globl _start\n_start:\n mov $60, %%eax\n mov $%s, %%edi\n syscall\n' $code > "$out"
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	const src = "function main(): i32 { return 0; }\n"
	got, pass := bisectSelfHostSeed(t, fake, t.TempDir(), "x86-64-linux", src, nil, nil, func(env []string) bool {
		r, gap := runSelfHostSeed(t, fake, t.TempDir(), src, nil, env, func(string, ...any) {})
		return seedRunAgrees(r, gap, 0, src)
	})
	if want := "follows `reuse b` (FERN_OPT_BISECT=21 agrees, FERN_OPT_BISECT=20 does not)"; !strings.Contains(got, want) || pass != "reuse" {
		t.Fatalf("got %q (pass %q), want it to contain %q (pass reuse)", got, pass, want)
	}
}
