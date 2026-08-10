package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"
)

// --- A closure in a struct field must not leak per round (#6443) ---
//
// Native released a closure FIELD with the bare `__fern_rc_dec` fall-through
// in `appendChildDrop`, which zeroes the pair's count and stops: pair block,
// env block and every rc-tracked capture stranded, three blocks per instance.
// The fix routes container-held closures through the same pointer-dispatched
// release the array-of-closure path already used.
//
// This is the self-host half. It asserts the DELTA between a provider table
// whose record carries a closure field and the identical table with a plain
// `i32` in that slot, at two round counts — the self-host's Perceus port is
// still in progress (docs/RC-PERCEUS-SELF-HOST-PORT.md), so an absolute byte
// figure here would be a budget for the rest of the port rather than a gate on
// this shape. What must hold is that the closure field costs the same per
// round at 100 rounds as at 200, i.e. nothing accumulates.
func closureFieldChurnSrc(rounds int, closureField bool) string {
	field, value, call := "k: i32", "k: n", "ps[j].k"
	if closureField {
		field, value, call = "f: (i32) => i32", "f: (x: i32) => x + n", "(ps[j].f)(1)"
	}
	return fmt.Sprintf(`import "std/i32";
import "std/string";

struct P { name: string, %s }

function mkP(n: i32): P {
    return P { name: "provider" + n.to_string(), %s };
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < %d) {
        var ps: P[] = [];
        var i: i32 = 0;
        while (i < 8) { ps = ps.append(mkP(i)); i = i + 1; }
        var j: i32 = 0;
        while (j < ps.len()) { t = t + %s + ps[j].name.len(); j = j + 1; }
        r = r + 1;
    }
    return t %% 7;
}`, field, value, rounds, call)
}

func TestSelfHostClosureFieldReclaimX86_64(t *testing.T) {
	// The self-host had its OWN version of this leak, and it was a different
	// one (#6461): the clofld admission was granted and the `k_clo` arm was
	// ready, but a single `fn` field took the whole `P[]` local out of the
	// append-built struct-array reclaim class, so nothing under it — element
	// boxes, strings, closures — was ever freed. Both halves are closed now,
	// and the probe is not merely flat in the delta: `allocs == frees`,
	// `live_bytes = 0`.
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	liveOf := func(name string, rounds int, closureField bool) int64 {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, closureFieldChurnSrc(rounds, closureField), []string{"FERN_LEAKCHECK=1"})
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
		t.Logf("%s (rounds=%d, closureField=%v): %s", name, rounds, closureField, summary)
		return live
	}

	base100 := liveOf("clofld_base100", 100, false)
	base200 := liveOf("clofld_base200", 200, false)
	with100 := liveOf("clofld_with100", 100, true)
	with200 := liveOf("clofld_with200", 200, true)

	d100, d200 := with100-base100, with200-base200
	t.Logf("closure-field delta: 100 rounds = %d B, 200 rounds = %d B", d100, d200)
	if d200 != d100 {
		t.Errorf("a closure in a struct field leaks per round on the self-host: delta over the "+
			"same table with a plain i32 field is %d B at 100 rounds and %d B at 200 (control "+
			"%d / %d). Each round discards its whole table, so the delta must be flat (#6443)",
			d100, d200, base100, base200)
	}
}
