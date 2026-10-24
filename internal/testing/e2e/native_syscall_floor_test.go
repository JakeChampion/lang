package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestNativeSyscallFloor(t *testing.T) {
	fern := e2eharness.RuntimeCLI(t)
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

func TestArm64DarwinNativeSyscallFloor(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires the Apple Silicon execution lane")
	}
	fern := e2eharness.RuntimeCLI(t)
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

func TestSyscallFloorRefusedOnWasm(t *testing.T) {
	fern := e2eharness.RuntimeCLI(t)
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
