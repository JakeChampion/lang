package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The SSA backend's heap reclaims (#8069): a program that churns through struct
// boxes and arrays runs in bounded memory however long it churns, as it does on
// the flat backend. pmap_insert is the corpus program written for exactly that
// shape (a self-reassigning persistent map), scaled here by its entry count.
//
// Peak RSS is the process's own rusage, so this runs only where the binary
// runs natively: under qemu it would measure the emulator. It is read through
// testdata/maxrss rather than from this process: a child's rusage high-water
// mark starts at the RSS of the process that forked it, and this test binary's
// own footprint is larger than either build's peak.
func TestArm64SSAHeapReclaimBoundsPeakRSS(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
		if os.Getenv("FERN_REQUIRE_ARM64_SSA_DIFF") != "" {
			t.Fatal("FERN_REQUIRE_ARM64_SSA_DIFF is set but peak RSS needs native arm64 Linux execution")
		}
		t.Skip("peak RSS needs native arm64 Linux execution (the program's own rusage, not an emulator's)")
	}
	fern := buildFernCLI(t)
	maxrss := filepath.Join(t.TempDir(), "maxrss")
	if out, err := exec.Command("go", "build", "-o", maxrss, langSrcAbs(t, "internal/e2e/testdata/maxrss")).CombinedOutput(); err != nil {
		t.Fatalf("go build maxrss: %v\n%s", err, out)
	}
	src, err := os.ReadFile(langSrcAbs(t, "examples/bench/pmap_insert.fern"))
	if err != nil {
		t.Fatal(err)
	}
	const bound = "< 4000"
	if !strings.Contains(string(src), bound) {
		t.Fatalf("pmap_insert.fern no longer loops to %q; the entry count this test scales is gone", bound)
	}
	run := func(bin string) (exit int, maxRSSKB int64) {
		cmd := exec.Command(maxrss, bin)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		_ = cmd.Run()
		exit = cmd.ProcessState.ExitCode()
		if exit < 0 || exit == 255 || exit == 2 {
			t.Fatalf("%s: %v\n%s", bin, cmd.ProcessState, stderr.String())
		}
		kb, err := strconv.ParseInt(strings.TrimSpace(stdout.String()), 10, 64)
		if err != nil {
			t.Fatalf("%s: maxrss printed %q: %v", bin, stdout.String(), err)
		}
		return exit, kb
	}
	// The flat build's exit code is the oracle for the SSA build's: a binary
	// that dies early has a flat RSS profile too.
	measure := func(n int) (ssaKB int64) {
		dir := t.TempDir()
		prog := filepath.Join(dir, "pm.fern")
		scaled := strings.ReplaceAll(string(src), bound, fmt.Sprintf("< %d", n))
		if err := os.WriteFile(prog, []byte(scaled), 0o644); err != nil {
			t.Fatal(err)
		}
		build := func(name string, backend ...string) string {
			bin := filepath.Join(dir, name)
			args := append([]string{"-target", "arm64-linux"}, backend...)
			args = append(args, "-o", bin, prog)
			if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
				t.Fatalf("build %s n=%d: %v\n%s", name, n, err, out)
			}
			return bin
		}
		flatExit, flatKB := run(build("flat"))
		ssaExit, ssaKB := run(build("ssa", "-backend", "ssa"))
		if ssaExit != flatExit {
			t.Fatalf("n=%d: ssa build exited %d, flat build exited %d", n, ssaExit, flatExit)
		}
		t.Logf("n=%d: exit %d, peak RSS flat %d KB, ssa %d KB", n, ssaExit, flatKB, ssaKB)
		return ssaKB
	}
	small := measure(1000)
	large := measure(8000)
	// Eight times the entries is eight times the churn, not eight times the live
	// set. Both readings sit on the maxrss helper's own footprint, which moves by
	// a couple of MB between runs; the slack covers that and page rounding while
	// a linear leak (28 MB over this range before #8069) still trips it.
	if large > small+8192 {
		t.Errorf("peak RSS grew from %d KB to %d KB between 1000 and 8000 entries: the SSA heap is not reclaiming", small, large)
	}
}
