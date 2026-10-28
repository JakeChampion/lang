//go:build linux

package e2e

import (
	"bytes"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostEntropyFailure(t *testing.T) {
	var nr uintptr
	switch runtime.GOARCH {
	case "amd64":
		nr = 318
	case "arm64":
		nr = 278
	default:
		t.Skip("entropy fault tests require an x86-64 or ARM64 Linux host")
	}
	targets := []string{e2eharness.HostSelfHostTarget()}
	if runtime.GOARCH == "amd64" {
		targets = append(targets, "arm64-linux")
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			var runner []string
			if target == "arm64-linux" {
				if qemu := e2eharness.Arm64Runner(t); qemu != "" {
					runner = []string{qemu}
				}
			}
			for _, tc := range []struct{ name, body string }{
				{"bytes", "let bs: u8[] = rand.bytes(257); print((bs[256] as i32).to_string());"},
				{"integer", "print(rand.next_i32().to_string());"},
				{"empty", "let bs: u8[] = rand.bytes(0); print(bs.len().to_string());"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					bin := e2eharness.CompileSelfHostSource(t, target, "import \"std/i32\"; import \"std/rand\"; function main(): i32 { "+tc.body+" print(\"random returned\"); return 0; }", nil)
					argv := append(append([]string{}, runner...), bin)
					run := func(cmd *exec.Cmd) (string, string, int) {
						t.Helper()
						var stdout, stderr bytes.Buffer
						cmd.Stdout, cmd.Stderr = &stdout, &stderr
						if err := cmd.Run(); cmd.ProcessState == nil {
							t.Fatalf("run: %v\n%s", err, stderr.String())
						}
						return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
					}
					if out, errout, code := run(exec.Command(argv[0], argv[1:]...)); code != 0 || !strings.HasSuffix(out, "random returned\n") || errout != "" {
						t.Fatalf("positive control: exit=%d stdout=%q stderr=%q", code, out, errout)
					}
					for _, errno := range []syscall.Errno{syscall.EIO, syscall.ENOSYS} {
						t.Run(errno.Error(), func(t *testing.T) {
							cmd := underSeccompErrno(t, t.TempDir(), errno, []uintptr{nr}, argv...)
							out, errout, code := run(cmd)
							if tc.name == "empty" {
								if code != 0 || out != "0\nrandom returned\n" || errout != "" {
									t.Errorf("empty draw: exit=%d stdout=%q stderr=%q", code, out, errout)
								}
							} else if code != 134 || out != "" || errout != "fern: entropy unavailable\n" {
								t.Errorf("failed draw: exit=%d stdout=%q stderr=%q", code, out, errout)
							}
						})
					}
				})
			}
		})
	}
}

func TestSelfHostSandboxEntropyFailure(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("the seccomp sandbox requires native x86-64 Linux")
	}
	// No user output: the runtime must supply the diagnostic syscall dependency.
	bin := buildSandboxed(t, "import \"std/rand\";\nfunction main(): i32 { return rand.next_i32() & 255; }", true)
	control := exec.Command(bin)
	if out, err := control.CombinedOutput(); control.ProcessState == nil || !control.ProcessState.Exited() || len(out) != 0 {
		t.Fatalf("positive control: %v\n%s", err, out)
	}
	for _, errno := range []syscall.Errno{syscall.EIO, syscall.ENOSYS} {
		t.Run(errno.Error(), func(t *testing.T) {
			cmd := underSeccompErrno(t, t.TempDir(), errno, []uintptr{318}, bin)
			out, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 134 || string(out) != "fern: entropy unavailable\n" {
				t.Errorf("denied entropy: %v\n%s", err, out)
			}
		})
	}
}
