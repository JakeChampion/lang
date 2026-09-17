package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// x86_64ssa had no FERN_LEAKCHECK census at all: the flag is read by the
// COMPILER at build time, arm64ssa and the flat backend both emit the line,
// and this backend emitted nothing. That left the two SSA backends
// unmeasurable with one instrument, which is what the retention work (#9542)
// needs to tell a shared-lowering bug from an emitter one.
//
// The line is `leakcheck: allocs=N frees=M live_bytes=K`, identical to the
// other two by design.
var leakcheckCounts = regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=(-?\d+)`)

// A program that allocates a heap string per iteration and drops it. Every
// block is freed, so a census whose counters net out correctly reports
// live_bytes 0 and frees equal to allocs. It returns n%7, so a non-zero exit
// is the normal path.
//
// The concatenation joins a VARIABLE to itself rather than two literals:
// `"abc" + "defgh"` folds to a single .rodata literal at compile time and
// allocates nothing, which made the first version of this fixture report
// allocs=0 on both backends — the test was right and the program was wrong.
const x86SSALeakcheckSrc = `function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    var base: string = "abcdefghij";
    loop {
        if (i >= 500) { break; }
        var s: string = base + base;
        n = (n + s.len()) % 101;
        i = i + 1;
    }
    return n % 7;
}
`

func censusFor(t *testing.T, bin, qemu, dir, backend string) (allocs, frees, live int) {
	t.Helper()
	src := filepath.Join(dir, "census_"+backend+".fern")
	if err := os.WriteFile(src, []byte(x86SSALeakcheckSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	binPath := filepath.Join(dir, "census_"+backend+".bin")
	compile := exec.Command(bin, "-target", "x86-64-linux", "-backend", backend, "-o", binPath, src)
	// The compiler reads the flag, not the produced binary.
	compile.Env = e2eharness.ChildEnv("FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("x86-64 -backend %s build failed: %v\n%s", backend, err, out)
	}
	run := runX86Bin(qemu, binPath)
	run.Env = e2eharness.ChildEnv()
	var errBuf strings.Builder
	run.Stderr = &errBuf
	_ = run.Run() // exits with n%7
	m := leakcheckCounts.FindStringSubmatch(errBuf.String())
	if m == nil {
		t.Fatalf("no census line on stderr for -backend %s.\nFERN_LEAKCHECK=1 is read by the compiler at "+
			"build time, and this backend has to emit __ssa_lc_report and call it from both paths a "+
			"program leaves by (_start's epilogue and the exit() builtin).\nstderr: %q",
			backend, errBuf.String())
	}
	toInt := func(s string) int {
		v, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return v
	}
	return toInt(m[1]), toInt(m[2]), toInt(m[3])
}

func TestX86_64SSAEmitsTheLeakCensus(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	ssaAllocs, ssaFrees, ssaLive := censusFor(t, bin, qemu, dir, "ssa")
	flatAllocs, flatFrees, flatLive := censusFor(t, bin, qemu, dir, "flat")

	for _, c := range []struct {
		backend             string
		allocs, frees, live int
	}{
		{"ssa", ssaAllocs, ssaFrees, ssaLive},
		{"flat", flatAllocs, flatFrees, flatLive},
	} {
		t.Run(c.backend, func(t *testing.T) {
			// The loop allocates 500 strings, so a census reporting nothing
			// allocated is counting the wrong thing rather than being clean.
			if c.allocs < 100 {
				t.Errorf("%s: allocs=%d, want at least 100 — the loop allocates a string per "+
					"iteration, so a count this low means the counter misses the sites that "+
					"matter (under the census the inline fast paths must stay calls, because "+
					"__alloc and __free are where the counting happens)", c.backend, c.allocs)
			}
			if c.frees < c.allocs/2 {
				t.Errorf("%s: allocs=%d frees=%d — every string in the loop is dropped, so the "+
					"frees should track the allocs", c.backend, c.allocs, c.frees)
			}
			// Everything the loop allocates is freed, so what stays live is
			// the handful of blocks the runtime holds, not the 500 strings.
			if c.live < 0 || c.live > 8192 {
				t.Errorf("%s: live_bytes=%d, want a small non-negative number — every block the "+
					"loop allocates is freed, so live_bytes is bumped+popped-freed netting out "+
					"to the runtime's own few blocks. A negative value means the frees are "+
					"counted against sizes the allocs were not", c.backend, c.live)
			}
		})
	}

	// The instrument has to agree between the backends or it cannot be used to
	// compare them, which is the whole reason it was ported.
	t.Run("the_two_backends_report_comparably", func(t *testing.T) {
		if diff := ssaLive - flatLive; diff > 4096 || diff < -4096 {
			t.Errorf("live_bytes differs by %d between the backends (ssa %d, flat %d) on a program "+
				"that frees everything it allocates. The census is meant to be one instrument "+
				"across both, so a gap this size is a real difference in what is retained, not "+
				"noise.\n%s", diff, ssaLive, flatLive,
				fmt.Sprintf("ssa: allocs=%d frees=%d / flat: allocs=%d frees=%d",
					ssaAllocs, ssaFrees, flatAllocs, flatFrees))
		}
	})
}
