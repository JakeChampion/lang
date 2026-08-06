package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Block-scoped reclaim, the rest of the family (#6127) --------------------
//
// #6263 gave the reclaim credits a retired-name-aware lookup (reclaim_slot_name)
// but switched it on for only four classes — "TUP:", "OPTTUP:", "OPTSTRUCT:",
// "OPTARRARR:" — the four with a measured leak at the time, deliberately leaving
// the rest keying on slot_name because switching a class on turns on an
// exit-sweep release that has never run for these slots.
//
// Probing the rest of the family found the same n-1-of-n signature on six more,
// each closed here and each verified to reach allocs == frees:
//
//	ARRARR / ARRARRS   7200      OPTAARR   12000      TUPRC / TUPRCS   8000
//	SARR               4800      STRUCTARR(A)   9600
//
// TUPRC needed BOTH its credits switched — "TUPRC:" drives the rebind and
// "TUPRCS:" (#6251) the exit sweep — and flipping only the first left the shape
// measuring exactly its unfixed 8000.
//
// Two classes are absent by MEASUREMENT rather than omission, and the tests
// below pin both directions:
//
//   - "RCENUM:" and "OPTARR:" are already flat at 0, because their
//     consuming-match analyses are per-block (#4357) and free the value before
//     the block ends. Switching them on would be a second claim on a released
//     box, so they must stay balanced without it.
//   - "ARRSTRUCT:" and "ARRTUP:" still leak (35200 / 32000) and switching them
//     changed NOTHING — measured identical before and after — so their cause is
//     elsewhere and they are left alone rather than half-fixed.
//   - The BARE-name struct credit leaks 8800 and stays leaking: switching it on
//     SEGFAULTS the gen1 self-compile. It is the credit the compiler's own
//     threaded builders lean on, and it feeds the deep field drop, the precise
//     drop and the reuse-donor paths as well as the exit sweep, so the releases
//     it turns on for block-scoped slots are not all sole-owner. That is why
//     these go in one class at a time behind the per-module fixpoint.

func TestSelfHostBlockScopedClassesX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	counts := func(t *testing.T, name, src string, wantExit int) (int64, int64, int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != wantExit {
			t.Fatalf("%s exited %d, want %d", name, exit, wantExit)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary", name)
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("%s: parse %q: %v", name, summary, err)
		}
		if allocs == 0 {
			t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
		}
		return allocs, frees, live
	}

	balanced := func(t *testing.T, name, src string, wantExit int) {
		t.Helper()
		allocs, frees, live := counts(t, name, src, wantExit)
		if live != 0 {
			t.Errorf("%s: live_bytes=%d, want 0 — one unfreed value per round, so this "+
				"scales with the loop count", name, live)
		}
		if allocs != frees {
			t.Errorf("%s: allocs=%d frees=%d — must balance exactly", name, allocs, frees)
		}
	}

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			name: "arrarr_declared_in_loop",
			src: `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var g: i32[][] = [[i, i + 1], [i + 2]];
        acc = acc + g.len() + g[0][0];
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 42,
		},
		{
			name: "strarr_declared_in_loop",
			src: `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var xs: string[] = ["alpha", "beta"];
        acc = acc + xs.len();
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 23,
		},
		{
			name: "scalar_field_struct_array_declared_in_loop",
			src: `struct S { a: i32, b: i32 }
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var xs: S[] = [S { a: i, b: 1 }, S { a: i, b: 2 }];
        acc = acc + xs.len();
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 23,
		},
		{
			name: "optaarr_declared_in_loop",
			src: `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var xs: Option[i32[]][] = [Some([i, i + 1]), None];
        acc = acc + xs.len();
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 23,
		},
		{
			// Needs BOTH "TUPRC:" and "TUPRCS:" — the rebind credit and the exit-sweep
			// credit #6251 added. With only the first switched this measured exactly
			// its unfixed 8000, which is why it has its own case.
			name: "rc_tuple_declared_in_loop",
			src: `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var t: (i32, i32[]) = (i, [i, i + 1]);
        acc = acc + t.0 + t.1.len();
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 42,
		},
		{
			// Already balanced WITHOUT this mechanism: consumed_rcpayload_enum_frees is
			// per-block (#4357), so the value is freed before the block ends. If
			// "RCENUM:" were switched on too, this would be freed twice.
			name: "rc_payload_enum_in_loop_stays_balanced",
			src: `enum E { Full(i32[]), Nil }
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var b: E = E.Full([i, i + 1]);
        match (b) { E.Full(xs) => { acc = acc + xs.len(); }, E.Nil => {} }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 23,
		},
		{
			// The Option sibling of the case above, same reason, same double-free risk.
			name: "flat_option_in_loop_stays_balanced",
			src: `function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var o: Option[i32[]] = Some([i, i + 1]);
        match (o) { Some(a) => { acc = acc + a.len() + a[0]; }, None => {} }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 42,
		},
	} {
		t.Run(tc.name, func(t *testing.T) { balanced(t, tc.name, tc.src, tc.want) })
	}
}

// TestSelfHostBlockScopedClassesHazardsX86_64 — the block-scoped shapes these
// classes must still REFUSE. Freeing a retired slot at function exit is sound
// only while that slot is the sole reference to its value, so each of these
// keeps a live reference past the block. They assert BEHAVIOUR: the failure mode
// is a wrong answer or a crash, not a leak. Every `want` is from `fern -interp`.
func TestSelfHostBlockScopedClassesHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			// Each block-scoped buffer is moved into a container that outlives the
			// loop and is read after it.
			name: "arr_escapes_into_an_outer_container",
			src: `function round(r: i32): i32 {
    var keep: i32[][] = [];
    var i: i32 = 0;
    while (i < 4) {
        var g: i32[] = [i, i + 1];
        keep = keep.append(g);
        i = i + 1;
    }
    var acc: i32 = 0;
    var j: i32 = 0;
    while (j < keep.len()) { acc = acc + keep[j][0]; j = j + 1; }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 72,
		},
		{
			// A block-scoped string[] both aliased outward and passed to a call.
			name: "strarr_aliased_and_passed_to_a_call",
			src: `function keepit(xs: string[]): i32 { return xs.len(); }
function round(r: i32): i32 {
    var acc: i32 = 0;
    var held: string[] = [];
    var i: i32 = 0;
    while (i < 4) {
        var xs: string[] = ["alpha", "beta"];
        held = xs;
        acc = acc + keepit(xs);
        i = i + 1;
    }
    return acc + held.len() + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 57,
		},
		{
			// A block-scoped Option[<arr>][] aliased outward — freeing it would dangle
			// both the option boxes and their payload buffers.
			name: "optaarr_aliased_to_an_outer_local",
			src: `function round(r: i32): i32 {
    var acc: i32 = 0;
    var held: Option[i32[]][] = [];
    var i: i32 = 0;
    while (i < 4) {
        var xs: Option[i32[]][] = [Some([i, i + 1]), None];
        held = xs;
        acc = acc + xs.len();
        i = i + 1;
    }
    return acc + held.len() + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 57,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "blc_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means a "+
					"retired slot was freed at function exit while something else still "+
					"held its value (use-after-free), not merely that it leaked",
					exit, tc.want)
			}
		})
	}
}
