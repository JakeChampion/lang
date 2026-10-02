package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The runtime's bytes floor — `__str_bytes`, `__arr_set_len` — on every
// native backend of the Go compiler (#9853, #4451): what lets tcp_recv,
// tcp_send and udp_send be Fern bodies. The probe fills a byte array through
// its data pointer, shortens it, and reads an inline and a heap string's
// bytes through the floor, with and without scratch to spill into.
func TestBytesFloor(t *testing.T) {
	fern := buildFernCLI(t)
	x86, x86ok := x86Runner()
	arm, armok := arm64Runner()
	for _, c := range []struct {
		name, target, backend string
		runnable              bool
		run                   func(bin string) *exec.Cmd
	}{
		{"x86-64", "x86-64-linux", "", x86ok, func(bin string) *exec.Cmd { return runX86Bin(x86, bin) }},
		{"x86-64-ssa", "x86-64-linux", "ssa", x86ok, func(bin string) *exec.Cmd { return runX86Bin(x86, bin) }},
		{"arm64", "arm64-linux", "", armok, func(bin string) *exec.Cmd { return runArm64Bin(arm, bin) }},
		{"arm64-ssa", "arm64-linux", "ssa", armok, func(bin string) *exec.Cmd { return runArm64Bin(arm, bin) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !c.runnable {
				t.Skipf("no way to run %s binaries on this host", c.target)
			}
			dir := t.TempDir()
			src := filepath.Join(dir, "probe.fern")
			if err := os.WriteFile(src, []byte(e2eharness.BytesFloorProbe(true)), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "probe")
			args := []string{"-target", c.target}
			if c.backend != "" {
				args = append(args, "-backend", c.backend)
			}
			args = append(args, "-o", bin, src)
			if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			out, err := c.run(bin).CombinedOutput()
			if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 42 {
				t.Fatalf("probe: %v, want exit 42; first failing step: %s", err, out)
			}
		})
	}
}

func TestArm64DarwinBytesFloor(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires the Apple Silicon execution lane")
	}
	fern := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "probe.fern")
	if err := os.WriteFile(src, []byte(e2eharness.BytesFloorProbe(true)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "probe")
	if out, err := exec.Command(fern, "-target", "arm64-darwin", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 42 {
		t.Fatalf("probe: %v, want exit 42; first failing step: %s", err, out)
	}
}
