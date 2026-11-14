package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The program prints one line and then spins. Under -cpu-limit 1 the kernel
// kills it after one second of processor time. A control program with the
// same flag ends before the limit with its own status.
const cpuLimitSpinSrc = `function main(): i32 {
  let n: i64 = 0;
  print("start");
  while (true) {
    n += 1;
  }
  return 0;
}
`

const cpuLimitQuickSrc = `function main(): i32 {
  print("done");
  return 7;
}
`

func TestSelfHostCPULimit(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	dir := t.TempDir()
	write := func(name, text string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	spin := write("spin.fern", cpuLimitSpinSrc)
	quick := write("quick.fern", cpuLimitQuickSrc)
	compile := func(t *testing.T, src string, flags ...string) (string, string, int) {
		t.Helper()
		bin := filepath.Join(t.TempDir(), "main")
		args := append(append([]string{}, flags...), "-o", bin, src, cli.stdlib)
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Env = childEnv()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		_ = cmd.Run()
		return bin, stderr.String(), cmd.ProcessState.ExitCode()
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			run := func(bin string) (string, *os.ProcessState, time.Duration) {
				t.Helper()
				var c *exec.Cmd
				if target == "arm64-linux" {
					c = runArm64Bin(qemu, bin)
				} else {
					c = runX86_64Bin(cli.runner, bin)
				}
				var stdout bytes.Buffer
				c.Stdout = &stdout
				started := time.Now()
				_ = c.Run()
				return stdout.String(), c.ProcessState, time.Since(started)
			}
			bin, errout, code := compile(t, spin, "-target", target, "-cpu-limit", "1")
			if code != 0 {
				t.Fatalf("compile with -cpu-limit 1: exit %d\n%s", code, errout)
			}
			out, state, took := run(bin)
			status, _ := state.Sys().(syscall.WaitStatus)
			if out != "start\n" || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatalf("the spin under -cpu-limit 1: stdout %q, state %v; want one line and a kill by the kernel", out, state)
			}
			if took > 30*time.Second {
				t.Fatalf("the spin ran for %v before the kill", took)
			}
			bin, errout, code = compile(t, quick, "-target", target, "-cpu-limit", "1")
			if code != 0 {
				t.Fatalf("compile of the control: exit %d\n%s", code, errout)
			}
			if out, state, _ := run(bin); out != "done\n" || state.ExitCode() != 7 {
				t.Fatalf("the control: stdout %q, state %v; want its own status 7", out, state)
			}
			manifest, err := runX86_64Bin(cli.runner, cli.bin, "-manifest", bin).Output()
			if err != nil || !strings.Contains(string(manifest), "cpu_limit: 1\n") {
				t.Fatalf("fern -manifest: %v\n%s\nwant the line %q", err, manifest, "cpu_limit: 1")
			}
		})
	}
	t.Run("refusals", func(t *testing.T) {
		for _, tc := range []struct {
			flags []string
			want  string
		}{
			{[]string{"-target", "x86-64-linux", "-cpu-limit", "0"}, "fern: -cpu-limit takes a number of seconds from 1 to 31536000"},
			{[]string{"-target", "x86-64-linux", "-cpu-limit", "2s"}, "fern: -cpu-limit takes a number of seconds from 1 to 31536000"},
			{[]string{"-target", "arm64-linux", "-cpu-limit", "31536001"}, "fern: -cpu-limit takes a number of seconds from 1 to 31536000"},
			{[]string{"-target", "x86-64-linux", "-cpu-limit", "99999999999999999999"}, "fern: -cpu-limit takes a number of seconds from 1 to 31536000"},
			{[]string{"-target", "wasm32-wasi", "-cpu-limit", "2"}, "fern: -cpu-limit is only supported for the Linux and Android targets (got \"wasm32-wasi\")"},
			{[]string{"-target", "arm64-darwin", "-cpu-limit", "2"}, "fern: -cpu-limit is only supported for the Linux and Android targets (got \"arm64-darwin\")"},
		} {
			_, errout, code := compile(t, quick, tc.flags...)
			if code != 2 || !strings.HasPrefix(errout, tc.want) {
				t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.flags, code, errout, tc.want)
			}
		}
		for _, seconds := range []string{"1", "31536000"} {
			if _, errout, code := compile(t, quick, "-target", "x86-64-linux", "-emit", "asm", "-cpu-limit", seconds); code != 0 {
				t.Errorf("-cpu-limit %s refused: exit %d\n%s", seconds, code, errout)
			}
		}
	})
}
