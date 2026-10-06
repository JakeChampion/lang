package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An array element read takes its slot when the array is the frame's own
// (ssaunits.payload_root), and that take is the read's unit. The read must not
// also be held as an element outliving its array (held_elements): the hold's
// retain and release pair up, and the take's unit was never released. Here the
// `.with` copies `xs` because `xs` is still read, so `xs` is unique at its
// read and the take empties slot 0; the element then outlives the array.
//
// The exit code: 1 for a wrong sum, 99 for a count that went under.
const arrayElementTakeProg = `struct In { v: i32 }
function round(i: i32): i32 {
    let xs: In[] = [In { v: i }, In { v: i + 1 }];
    let ys: In[] = xs.with(0, In { v: i + 7 });
    return xs[0].v + ys[0].v + ys.len();
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { t = t + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (t != 10800) { return 1; }
    return 0;
}
`

func TestSelfHostArrayElementTakeIsOneUnit(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "take.fern")
	if err := os.WriteFile(src, []byte(arrayElementTakeProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		build := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib)
		build.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
		if combined, err := build.CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		var stderr strings.Builder
		run.Stderr = &stderr
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 0 {
			t.Errorf("%s: exit %d, want 0 (1: a wrong sum, 99: a count went under)", tg.target, got)
		}
		allocs, frees, live := parseLeakcheck(t, tg.target, stderr.String())
		if allocs == 0 || allocs != frees || live != 0 {
			t.Errorf("%s: allocs=%d frees=%d live_bytes=%d, want every block freed once", tg.target, allocs, frees, live)
		}
	}
}
