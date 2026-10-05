package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The runtime's raw floor — `__syscall3` … `__syscall6`, `__store_u8` —
// through the `fern` CLI on each Linux target (#9853), the names and shapes
// the asmcore helpers are written on, so a socket primitive is one Fern body
// rather than assembly per backend. The probe maps a file at a nonzero offset through
// `__syscall6`, reads it back through `__load_u8`, unmaps and closes through
// `__syscall3`, pins the signed -errno of a bad descriptor, and round-trips
// bytes through `__store_u8`. A leg whose ISA this host cannot run is
// skipped, as its siblings are; the CI matrix runs each on its own host.
func TestNativeSyscallFloor(t *testing.T) {
	fern := buildFernCLI(t)
	x86, x86ok := x86Runner()
	arm, armok := arm64Runner()
	for _, c := range []struct {
		name, target string
		runnable     bool
		run          func(bin string) *exec.Cmd
	}{
		{"x86-64", "x86-64-linux", x86ok, func(bin string) *exec.Cmd { return runX86Bin(x86, bin) }},
		{"arm64", "arm64-linux", armok, func(bin string) *exec.Cmd { return runArm64Bin(arm, bin) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !c.runnable {
				t.Skipf("no way to run %s binaries on this host", c.target)
			}
			dir := t.TempDir()
			src := filepath.Join(dir, "probe.fern")
			if err := os.WriteFile(src, []byte(e2eharness.SyscallFloorProbe(t, dir, c.target)), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "probe")
			if out, err := exec.Command(fern, "-target", c.target, "-o", bin, src).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			if out, err := c.run(bin).CombinedOutput(); err != nil {
				t.Fatalf("probe step failed: %v\n%s", err, out)
			}
		})
	}
}

// The Darwin leg of the same probe, on the macOS lane: XNU's numbers, the
// number in x16, and the carry-flagged errno the emitter negates.
func TestArm64DarwinNativeSyscallFloor(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires the Apple Silicon execution lane")
	}
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.fern")
	if err := os.WriteFile(src, []byte(e2eharness.SyscallFloorProbe(t, dir, "arm64-darwin")), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "probe")
	if out, err := exec.Command(fern, "-target", "arm64-darwin", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("probe step failed: %v\n%s", err, out)
	}
}

// wasm has no kernel: a program that reaches the syscall floor is refused
// at compile time, naming the callee, rather than linked against nothing.
func TestSyscallFloorRefusedOnWasm(t *testing.T) {
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.fern")
	if err := os.WriteFile(src, []byte(e2eharness.SyscallFloorProbe(t, dir, "x86-64-linux")), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(fern, "-target", "wasm32-wasi", "-o", filepath.Join(dir, "probe.wasm"), src).CombinedOutput()
	if err == nil {
		t.Fatal("wasm compiled a program that issues raw syscalls")
	}
	if !strings.Contains(string(out), "__syscall") {
		t.Fatalf("refusal should name the syscall floor, got:\n%s", out)
	}
}
