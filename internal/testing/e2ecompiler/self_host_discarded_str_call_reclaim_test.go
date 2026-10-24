package e2ecompiler

import (
	"strconv"
	"strings"
	"testing"
)

func discardedStrCallSrc(rounds int) string {
	return `function mk(s: string): string { return s + "!"; }

function churn(a: string, n: i32): i32 {
    let i: i32 = 0;
    while (i < n) { mk(a); i = i + 1; }
    return i;
}

function main(): i32 {
    let a: string = "longer_string_one_here";
    return churn(a, ` + strconv.Itoa(rounds) + `) % 7;
}`
}

func TestSelfHostDiscardedStrCallReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	type reading struct{ allocs, frees, live int64 }
	measure := func(t *testing.T, rounds int) reading {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, discardedStrCallSrc(rounds), []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, "discarded_str_call_"+strconv.Itoa(rounds), asm)
		stderr, exit := hevRun(t, runner, progBin)
		if want := rounds % 7; exit != want {
			t.Fatalf("rounds=%d: exited %d, want %d", rounds, exit, want)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("rounds=%d: no leakcheck summary", rounds)
		}
		var r reading
		if _, err := fmtSscan(summary, &r.allocs, &r.frees, &r.live); err != nil {
			t.Fatalf("rounds=%d: parse %q: %v", rounds, summary, err)
		}
		if r.allocs == 0 {
			t.Fatalf("rounds=%d: allocated nothing — the probe is not exercising the path", rounds)
		}
		if r.frees > r.allocs {
			t.Fatalf("rounds=%d: allocs=%d frees=%d — frees above allocs means something "+
				"else already claimed the value (double free)", rounds, r.allocs, r.frees)
		}
		return r
	}

	lo := measure(t, 200)
	hi := measure(t, 400)
	if hi.allocs <= lo.allocs {
		t.Fatalf("allocs did not grow with the round count (%d at 200, %d at 400) — "+
			"the churn is being optimised away and the measurement is meaningless",
			lo.allocs, hi.allocs)
	}
	if hi.live != lo.live {
		t.Errorf("live_bytes %d at 200 rounds, %d at 400 — a discarded fresh-string "+
			"call must free its result, so the residue is a fixed floor and doubling "+
			"the rounds must not move it", lo.live, hi.live)
	}
	if d, e := hi.allocs-hi.frees, lo.allocs-lo.frees; d != e {
		t.Errorf("unfreed allocations %d at 200 rounds, %d at 400 — must not scale "+
			"with the round count", e, d)
	}
}

func TestSelfHostDiscardedStrCallHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{"borrowed_return_untouched", `function mk(s: string): string { return s + "!"; }
function pick(a: string, b: string): string { if (a.len() > 3) { return a; } return b; }
function main(): i32 {
    let a: string = mk("abcdefg");
    let b: string = "xy";
    let i: i32 = 0;
    while (i < 200) { pick(a, b); i = i + 1; }
    if (a != "abcdefg!") { return 1; }
    if (b != "xy") { return 2; }
    return 5;
}`, 5},
		{"mixed_return_disqualifies", `function half(a: string, c: boolean): string {
    if (c) { return a; }
    return a + "!";
}
function main(): i32 {
    let a: string = "abcdefgh";
    let i: i32 = 0;
    while (i < 200) { half(a, true); i = i + 1; }
    if (a != "abcdefgh") { return 1; }
    return 6;
}`, 6},
		{"bound_and_discarded_coexist", `function mk(s: string): string { return s + "!"; }
function main(): i32 {
    let a: string = "abcdefgh";
    let keep: string = mk(a);
    let i: i32 = 0;
    while (i < 200) { mk(a); i = i + 1; }
    if (keep != "abcdefgh!") { return 1; }
    return 7;
}`, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "discard_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrongly-granted free releases a box "+
					"something else still reads", exit, tc.want)
			}
		})
	}
}
