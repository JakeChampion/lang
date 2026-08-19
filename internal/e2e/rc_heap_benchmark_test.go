package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// Phase 6 measurement: quantify what RC reclamation buys for the
// peak-heap footprint of a churny program — the only memory metric
// that matters for short-lived processes (total is reclaimed by the
// OS at exit regardless; PEAK is what a freelist can bound).
//
// The bump allocator only advances its cursor; the freelist
// (RcFreeEnabled) recycles freed blocks without advancing it. So
// the bump cursor's high-water mark — reflected in the
// process's peak RSS (touched pages) — is bounded under churn iff
// reclamation is on. We compile + run the SAME program with the
// freelist on and off and compare ru_maxrss.
//
// Only meaningful when the binary runs natively (its own rusage),
// so the test skips under a qemu runner.
func TestX86_64HeapReclamationPeakRSS(t *testing.T) {
	// Env-gated: this is a measurement tool, not a CI gate. ru_maxrss
	// is environment-sensitive (it flaked once under the loaded full
	// suite while passing in isolation), so a hard threshold here
	// would be a flaky gate. The FUNCTIONAL guarantee that
	// reclamation bounds peak under churn is already covered by the
	// map_growth_buffer_free / map_overwrite_churn_free corpus cases
	// (they'd OOM/diverge if the freelist stopped recycling). Run
	// with FERN_BENCH=1 to print the on/off peak-RSS comparison.
	// CI-DARK: FERN_BENCH — a measurement tool, not a gate. ru_maxrss is
	// environment-sensitive (it varies 12x with transparent hugepages), so a
	// threshold here would be a flake, and the functional guarantee is already
	// covered by the corpus cases named above.
	if os.Getenv("FERN_BENCH") == "" {
		t.Skip("set FERN_BENCH=1 to run the peak-RSS reclamation benchmark")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("peak-RSS measurement needs native execution (own rusage); skipping under cross-run")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("non-native runner; ru_maxrss would measure the emulator, not the program")
	}

	// Overwrite the same key 800k times with a fresh 16-element
	// array. With reclamation the overwrite-dec frees the prior
	// array and the next alloc reuses its block, so peak RSS stays
	// FLAT as the loop count grows (a fixed working-set floor);
	// without it every array leaks and the bump cursor climbs
	// linearly with the loop count. Measured here: ON ~ 27 MB
	// regardless of N, OFF ~ 62 MB at 800k (and rising with N).
	// Result is 0 either way (last array's [0] == 799999).
	const churn = `import "core/map";

function main(): i32 {
    var m: Map[i32, i32[]] = map_new(4);
    var i: i32 = 0;
    while (i < 800000) {
        m = m.insert(0, [i, i, i, i, i, i, i, i, i, i, i, i, i, i, i, i]);
        i = i + 1;
    }
    return m.get_or(0, [])[0] - 799999;
}`

	measure := func(freeOn bool) (exit int, maxRSSKB int64) {
		prev := ast.RcFreeEnabled
		ast.RcFreeEnabled = freeOn
		defer func() { ast.RcFreeEnabled = prev }()

		dir := t.TempDir()
		srcPath := filepath.Join(dir, "main.fern")
		if err := os.WriteFile(srcPath, []byte(churn), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		prog, _, err := modload.Load(srcPath)
		if err != nil {
			t.Fatalf("modload: %v", err)
		}
		if err := constfold.Fold(prog, nil); err != nil {
			t.Fatalf("constfold: %v", err)
		}
		info, err := checker.Check(prog)
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		asm, err := x86_64.Emit(prog, info)
		if err != nil {
			t.Fatalf("emit: %v", err)
		}
		asmPath := filepath.Join(dir, "prog.s")
		binPath := filepath.Join(dir, "prog")
		if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
			t.Fatalf("write asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			t.Fatalf("gcc: %v\n%s", err, out)
		}
		cmd := exec.Command(binPath)
		_ = cmd.Run()
		ru, _ := cmd.ProcessState.SysUsage().(*syscall.Rusage)
		return cmd.ProcessState.ExitCode(), ru.Maxrss // KB on Linux
	}

	exitOn, rssOn := measure(true)
	exitOff, rssOff := measure(false)

	if exitOn != 0 || exitOff != 0 {
		t.Fatalf("churn miscomputed: exit on=%d off=%d (want 0/0)", exitOn, exitOff)
	}
	t.Logf("peak RSS: freelist ON = %d KB, OFF = %d KB, saved = %d KB (%.1f%%)",
		rssOn, rssOff, rssOff-rssOn, 100*float64(rssOff-rssOn)/float64(rssOff))

	// The 800k unreclaimed 16-elem arrays bump ~35 MB past the
	// reclaimed working-set floor. Guard against the freelist
	// silently regressing (peak no longer bounded under churn) with
	// a generous 8 MB floor — ~4x under the observed ~35 MB delta
	// and well above per-run RSS noise (~MB).
	if rssOff-rssOn < 8000 {
		t.Errorf("expected reclamation to bound peak RSS by >8 MB under churn; got ON=%d KB OFF=%d KB (delta %d KB)",
			rssOn, rssOff, rssOff-rssOn)
	}
}
