package e2e

import (
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

// Allocates per iteration and drops it all, in two shapes on purpose:
//
//   - `base + base` is a VARIABLE-size concat, which never reaches
//     inlineAllocLines. Two string literals would fold at compile time and
//     allocate nothing at all, which is what made the first version of this
//     fixture report allocs=0 on both backends.
//   - `[1, 2, 3]` is a CONSTANT-size allocation, which is exactly what the
//     inline fast path claims. Without it the allocs threshold below cannot
//     pin the census's inline gate: every allocation would bypass that path
//     anyway, so removing the gate would not move a single number.
const x86SSALeakcheckSrc = `function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    var base: string = "abcdefghij";
    loop {
        if (i >= 500) { break; }
        var s: string = base + base;
        var xs: i32[] = [1, 2, 3];
        n = (n + s.len() + xs[0]) % 101;
        i = i + 1;
    }
    return n % 7;
}
`

// The same work, left through the exit() builtin instead of returning. exit()
// bypasses _start's epilogue, so it carries its own call to the report: a
// program leaving this way would otherwise print no census at all.
const x86SSALeakcheckExitSrc = `function main(): i32 {
    var n: i32 = 0;
    var i: i32 = 0;
    var base: string = "abcdefghij";
    loop {
        if (i >= 500) { break; }
        var s: string = base + base;
        var xs: i32[] = [1, 2, 3];
        n = (n + s.len() + xs[0]) % 101;
        i = i + 1;
    }
    exit(n % 7);
    return 0;
}
`

type census struct {
	allocs, frees, live, exit int
	line                      string
}

// runCensus builds src for x86-64 under `backend`, with the census on or off,
// and returns what it reported and the status it exited with.
func runCensus(t *testing.T, bin, qemu, dir, backend, name, src string, leakcheck bool) census {
	t.Helper()
	tag := name + "_" + backend
	if leakcheck {
		tag += "_lc"
	}
	srcPath := filepath.Join(dir, tag+".fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	binPath := filepath.Join(dir, tag+".bin")
	compile := exec.Command(bin, "-target", "x86-64-linux", "-backend", backend, "-o", binPath, srcPath)
	// The compiler reads the flag, not the produced binary.
	env := []string{}
	if leakcheck {
		env = append(env, "FERN_LEAKCHECK=1")
	}
	compile.Env = e2eharness.ChildEnv(env...)
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("x86-64 -backend %s build failed: %v\n%s", backend, err, out)
	}
	run := runX86Bin(qemu, binPath)
	run.Env = e2eharness.ChildEnv()
	var errBuf strings.Builder
	run.Stderr = &errBuf
	err := run.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		if ee.ExitCode() < 0 {
			t.Fatalf("%s died on a signal: %v\n%s", tag, ee, errBuf.String())
		}
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s: %v\n%s", tag, err, errBuf.String())
	}
	c := census{exit: code, line: strings.TrimSpace(errBuf.String())}
	if !leakcheck {
		if strings.Contains(errBuf.String(), "leakcheck:") {
			t.Errorf("%s printed a census without FERN_LEAKCHECK=1 at build time: %q", tag, errBuf.String())
		}
		return c
	}
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
	c.allocs, c.frees, c.live = toInt(m[1]), toInt(m[2]), toInt(m[3])
	return c
}

func TestX86_64SSAEmitsTheLeakCensus(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	var ssa, flat census
	for _, backend := range []string{"ssa", "flat"} {
		c := runCensus(t, bin, qemu, dir, backend, "ret", x86SSALeakcheckSrc, true)
		if backend == "ssa" {
			ssa = c
		} else {
			flat = c
		}

		t.Run(backend, func(t *testing.T) {
			// 500 iterations allocate a string and an array each, and the
			// array is the constant-size shape the inline fast path claims.
			// A count near 500 means one of the two is not being counted.
			if c.allocs < 900 {
				t.Errorf("%s: allocs=%d, want at least 900 — the loop allocates a variable-size "+
					"string AND a constant-size array per iteration. A count near 500 means the "+
					"constant-size one is being inlined past the counter: under the census the "+
					"allocation fast paths must stay calls, because __alloc and __free are where "+
					"the counting happens", backend, c.allocs)
			}
			if c.frees < c.allocs/2 {
				t.Errorf("%s: allocs=%d frees=%d — everything the loop allocates is dropped, so "+
					"the frees should track the allocs", backend, c.allocs, c.frees)
			}
			if c.live < 0 || c.live > 8192 {
				t.Errorf("%s: live_bytes=%d, want a small non-negative number — every block the "+
					"loop allocates is freed, so live_bytes is bumped+popped-freed netting out "+
					"to the runtime's own few blocks. A negative value means the frees are "+
					"counted against sizes the allocs were not", backend, c.live)
			}

			// The report runs between main returning and exit_group, so it
			// has to park the status across itself. Comparing against the
			// same program built WITHOUT the census pins that without
			// hardcoding what n%7 happens to be.
			plain := runCensus(t, bin, qemu, dir, backend, "ret", x86SSALeakcheckSrc, false)
			if c.exit != plain.exit {
				t.Errorf("%s: exited %d with the census and %d without it — the report must not "+
					"clobber main's exit code, which is what parking it across the call is for",
					backend, c.exit, plain.exit)
			}

			// exit() bypasses _start's epilogue, so it carries its own call
			// to the report and its own parking of the status.
			ex := runCensus(t, bin, qemu, dir, backend, "exit", x86SSALeakcheckExitSrc, true)
			exPlain := runCensus(t, bin, qemu, dir, backend, "exit", x86SSALeakcheckExitSrc, false)
			if ex.exit != exPlain.exit {
				t.Errorf("%s: the exit() leg exited %d with the census and %d without it — the "+
					"exit() builtin has to park the status across its report too",
					backend, ex.exit, exPlain.exit)
			}
			if ex.allocs < 900 {
				t.Errorf("%s: the exit() leg reported allocs=%d, want at least 900 — leaving "+
					"through exit() must still report the same census as returning does",
					backend, ex.allocs)
			}
		})
	}

	// The instrument has to agree between the backends or it cannot be used to
	// compare them, which is the whole reason it was ported.
	t.Run("the_two_backends_report_comparably", func(t *testing.T) {
		if diff := ssa.live - flat.live; diff > 4096 || diff < -4096 {
			t.Errorf("live_bytes differs by %d between the backends (ssa %d, flat %d) on a program "+
				"that frees everything it allocates. The census is meant to be one instrument "+
				"across both, so a gap this size is a real difference in what is retained, not "+
				"noise.\nssa: %q\nflat: %q", diff, ssa.live, flat.live, ssa.line, flat.line)
		}
	})
}
