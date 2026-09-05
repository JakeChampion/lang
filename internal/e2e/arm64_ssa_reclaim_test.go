package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// The SSA backend's heap reclaims (#8069): a program that churns through struct
// boxes and arrays runs in bounded memory however long it churns, as it does on
// the flat backend. pmap_insert is the corpus program written for exactly that
// shape (a self-reassigning persistent map), and before reclamation its peak
// RSS under `-backend ssa` doubled with its entry count while flat's stayed put.
//
// Peak RSS is the process's own rusage, so this runs only where the binary
// runs natively: under qemu it would measure the emulator.
func TestArm64SSAHeapReclaimBoundsPeakRSS(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
		t.Skip("peak RSS needs native arm64 Linux execution (the program's own rusage, not an emulator's)")
	}
	fern := buildFernCLI(t)
	src, err := os.ReadFile(langSrcAbs(t, "examples/bench/pmap_insert.fern"))
	if err != nil {
		t.Fatal(err)
	}
	const bound = "< 4000"
	if !strings.Contains(string(src), bound) {
		t.Fatalf("pmap_insert.fern no longer loops to %q; the entry count this test scales is gone", bound)
	}
	measure := func(n int) (exit int, maxRSSKB int64) {
		dir := t.TempDir()
		prog := filepath.Join(dir, "pm.fern")
		scaled := strings.ReplaceAll(string(src), bound, fmt.Sprintf("< %d", n))
		if err := os.WriteFile(prog, []byte(scaled), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dir, "pm")
		if out, err := exec.Command(fern, "-target", "arm64-linux", "-backend", "ssa", "-o", bin, prog).CombinedOutput(); err != nil {
			t.Fatalf("build n=%d: %v\n%s", n, err, out)
		}
		cmd := exec.Command(bin)
		_ = cmd.Run()
		ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
		if !ok {
			t.Fatal("no rusage for the child process")
		}
		return cmd.ProcessState.ExitCode(), ru.Maxrss
	}
	_, small := measure(1000)
	_, large := measure(8000)
	t.Logf("peak RSS: 1000 entries = %d KB, 8000 entries = %d KB", small, large)
	// Eight times the entries is eight times the churn, not eight times the live
	// set. Before reclamation the large run sat at 8x the small one; a few pages
	// of slack covers allocator and page-rounding noise without letting a linear
	// leak back in.
	if large > small+4096 {
		t.Errorf("peak RSS grew from %d KB to %d KB between 1000 and 8000 entries: the SSA heap is not reclaiming", small, large)
	}
}
