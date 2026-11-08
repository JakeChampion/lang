package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Execute the Android runtime's raw syscalls on the native Linux kernel. This
// checks fork/wait behavior and Fern's filter; Android app policy has its own
// corpus gate and device execution remains a separate test lane.
func TestAndroidWorkerClone(t *testing.T) {
	const source = `function main(): i32 {
  let pid = proc_fork();
  if (pid < 0) { return 20; }
  if (pid == 0) { exit(7); }
  return proc_waitpid(pid) - 7;
 }`
	gcc, host := sandboxHost(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, leg := range []struct {
		target, host string
		clone        int
	}{
		{"x86-64-android", "x86-64-linux", 56},
		{"arm64-android", "arm64-linux", 220},
	} {
		t.Run(leg.target, func(t *testing.T) {
			asmPath := filepath.Join(t.TempDir(), "prog.s")
			command := exec.Command(e2eharness.SelfHostCLI(t), "-target", leg.target, "-emit", "asm", "-o", asmPath, path, e2eharness.SelfHostStdlibRoot(t))
			command.Env = e2eharness.ChildEnv("FERN_SANDBOX=1")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, output)
			}
			raw, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatal(err)
			}
			asm := string(raw)
			calls, ok := filterSyscalls(asm)
			if !ok || !slices.Contains(calls, leg.clone) || leg.target == "x86-64-android" && slices.Contains(calls, 57) {
				t.Fatalf("fork/clone syscall selection: %v", calls)
			}
			if host != leg.host {
				return
			}
			for _, probe := range []struct {
				name  string
				flags uint64
			}{{"valid", 17}, {"namespace", 0x10000000 | 0x200 | 17}, {"high-word", 1<<32 | 17}} {
				t.Run(probe.name, func(t *testing.T) {
					patched := asm
					if probe.flags != 17 {
						patched = injectSyscall(leg.clone, probe.flags, 0, 0, 0, 0)(asm)
						if patched == asm {
							t.Fatal("probe did not reach main")
						}
					}
					asmPath := filepath.Join(t.TempDir(), "probe.s")
					bin := asmPath + ".bin"
					if err := os.WriteFile(asmPath, []byte(patched), 0o644); err != nil {
						t.Fatal(err)
					}
					if output, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", bin).CombinedOutput(); err != nil {
						t.Fatalf("assemble: %v\n%s", err, output)
					}
					command := exec.Command(bin)
					output, err := command.CombinedOutput()
					if probe.flags == 17 {
						if err != nil {
							t.Fatalf("fork/wait: %v\n%s", err, output)
						}
						return
					}
					status, ok := command.ProcessState.Sys().(syscall.WaitStatus)
					if !ok || !status.Signaled() || status.Signal() != syscall.SIGSYS {
						t.Fatalf("%s %s clone flags %#x: %v, want SIGSYS", runtime.GOARCH, leg.target, probe.flags, command.ProcessState)
					}
				})
			}
		})
	}
}
