package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"
)

// --- `.with` on a string[] must not leak the receiver (#6407) ------
//
// On the native compiler, `arr.with(i, v)` did no rc bookkeeping at all for a
// `string[]`: strings sat outside the counted-array-element set, so the CoW
// copy shared the receiver's element buffers uncounted, the overwritten
// element was never released, and the escape analysis — which keys the
// receiver's reclaimability on that store being counted — tainted the whole
// receiver out of freeEligible. One `.with` stranded N+1 blocks per round.
//
// The self-host compiler lowers `.with` differently — a clone (arr_slice over
// the whole length) plus an in-place arr_set, never __fern_arr_cow_inplace —
// so it does not inherit the native bug, and measured here it costs exactly
// nothing: the `.with` delta is 0 B at both round counts.
//
// It is the delta that this asserts, not a byte count. The self-host DOES leak
// this loop (~384 B/round at the time of writing), but identically with and
// without the `.with`: the array comes back from `mks()`, which costs it the
// element-reclaim credit (`strarr_elem_store_ok` / "SARR:") regardless. That
// is the goal-2 port's own gap and belongs to it — subtracting the control
// keeps this test from quietly becoming a budget for it, while still failing
// the moment a `.with` starts costing the self-host a per-round block.
func strArrayWithChurnSrc(rounds int, with bool) string {
	set := ""
	if with {
		set = "a = a.with(3, a[5]);"
	}
	return fmt.Sprintf(`import "std/i32";
import "std/string";

function mks(): string[] {
    var out: string[] = [];
    var i: i32 = 0;
    while (i < 8) { out = out.append("kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; }
    return out;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < %d) {
        var a: string[] = mks();
        %s
        t = t + a.len() + a[3].len();
        r = r + 1;
    }
    return t %% 7;
}`, rounds, set)
}

func TestSelfHostStrArrayWithReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	liveOf := func(name string, rounds int, with bool) int64 {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, strArrayWithChurnSrc(rounds, with), []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, _ := hevRun(t, runner, progBin)
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary in %q", name, stderr)
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("parse %q: %v", summary, err)
		}
		if allocs == 0 {
			t.Fatalf("%s: allocated nothing — the probe is not exercising the path", name)
		}
		t.Logf("%s (rounds=%d): %s", name, rounds, summary)
		return live
	}

	// The `.with` DELTA against the identical loop without it, at two round
	// counts. Subtracting the control is what keeps this from becoming a byte
	// budget for the rest of the goal-2 port: whatever the self-host leaks
	// for unrelated reasons appears in both columns and cancels.
	base100 := liveOf("strbase100", 100, false)
	base200 := liveOf("strbase200", 200, false)
	with100 := liveOf("strwith100", 100, true)
	with200 := liveOf("strwith200", 200, true)

	d100, d200 := with100-base100, with200-base200
	t.Logf(".with delta: 100 rounds = %d B, 200 rounds = %d B", d100, d200)
	if d200 != d100 {
		t.Errorf(".with on a string[] leaks per round: delta over the same loop without it "+
			"is %d B at 100 rounds and %d B at 200 (control %d / %d). A build-`.with`-discard "+
			"round keeps nothing, so the delta must be flat; growth here is unbounded (#6407)",
			d100, d200, base100, base200)
	}
}
