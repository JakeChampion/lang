package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Block-scoped reclaim (#6127) ----------------------------------------------
//
// A local declared inside a loop or a nested block must be released on every
// iteration, including the last, and sibling blocks that declare the same name
// must each be freed exactly once. Each probe holds a heap value, since a
// scalar tuple is not heap-allocated.
//
// These assert allocs == frees alongside live_bytes == 0: frees > allocs is a
// double free, frees < allocs an unclaimed box, and they mean different bugs.

func TestSelfHostBlockScopedReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	balanced := func(t *testing.T, name, src string, wantExit int) {
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
		if live != 0 {
			t.Errorf("%s: live_bytes=%d, want 0 — one unfreed box per round, so this "+
				"scales with the loop count", name, live)
		}
		if allocs != frees {
			t.Errorf("%s: allocs=%d frees=%d — must balance exactly", name, allocs, frees)
		}
	}

	t.Run("tuple_declared_in_loop", func(t *testing.T) {
		balanced(t, "bs_tuple", `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let t: (i32, i32[]) = (i, [i + 1]);
        acc = acc + t.0 + t.1[0];
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 7;
}`, 4)
	})

	t.Run("opttup_declared_in_loop", func(t *testing.T) {
		balanced(t, "bs_opttup", `function round(i: i32): i32 {
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 4) {
        let o: Option[(i32, i32[])] = Some((k, [k, k + 1]));
        match (o) { Some(t) => { acc = acc + t.0 + t.1.len(); }, None => {} }
        k = k + 1;
    }
    return acc + i;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`, 42)
	})

	t.Run("optstruct_declared_in_loop", func(t *testing.T) {
		balanced(t, "bs_optstruct", `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 4) {
        let o: Option[P] = Some(P { xs: [k, k + 1], n: k });
        match (o) { Some(p) => { acc = acc + p.n + p.xs.len(); }, None => {} }
        k = k + 1;
    }
    return acc + i;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`, 42)
	})

	t.Run("optarrarr_declared_in_loop", func(t *testing.T) {
		balanced(t, "bs_optarrarr", `function round(i: i32): i32 {
    let acc: i32 = 0;
    let k: i32 = 0;
    while (k < 4) {
        let o: Option[i32[][]] = Some([[k, k + 1], [k + 2]]);
        match (o) { Some(g) => { acc = acc + g.len(); }, None => {} }
        k = k + 1;
    }
    return acc + i;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`, 23)
	})

	t.Run("sibling_blocks_declare_the_same_name", func(t *testing.T) {
		// Two slots, one name-keyed credit. Each must be freed exactly once — this
		// is the case a shared credit could double-free.
		balanced(t, "bs_siblings", `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        if (i % 2 == 0) {
            let t: (i32, i32[]) = (i, [1]);
            acc = acc + t.0 + t.1[0];
        } else {
            let t: (i32, i32[]) = (i, [2]);
            acc = acc + t.0 + t.1[0];
        }
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 7;
}`, 3)
	})

	t.Run("declared_two_blocks_deep", func(t *testing.T) {
		// An `if` inside a `while` retires the slot, then the while body retires it
		// again. Stacking sentinels left reclaim_slot_name returning "!retired!t"
		// rather than "t" — measured, exactly half of this corpus stayed unreclaimed
		// until retire_locals was made prefix-once.
		balanced(t, "bs_nested2", `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        if (i > 0) {
            let t: (i32, i32[]) = (i, [i + 1]);
            acc = acc + t.0 + t.1[0];
        }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 7;
}`, 3)
	})
}

// TestSelfHostBlockScopedReclaimHazardsX86_64 — the block-scoped shapes the
// credit must still REFUSE. Freeing a retired slot at function exit is only
// sound while that slot is the sole reference to its value, so each of these
// keeps a live reference past the block and asserts BEHAVIOUR: the failure mode
// is a wrong answer or a crash, not a leak. Every `want` is from `fern -interp`.
func TestSelfHostBlockScopedReclaimHazardsX86_64(t *testing.T) {
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
			// The block-scoped value is copied to a local that OUTLIVES the block and
			// is read after the loop. Freeing the retired slot would dangle it.
			name: "aliased_to_an_outer_local",
			src: `function round(r: i32): i32 {
    let outer: (i32, i32) = (0, 0);
    let i: i32 = 0;
    while (i < 4) {
        let t: (i32, i32) = (i, i + 1);
        outer = t;
        i = i + 1;
    }
    return outer.0 + outer.1;
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 71;
}`,
			want: 61,
		},
		{
			// Stored into a container declared outside the loop, read after it.
			name: "escapes_into_an_outer_container",
			src: `function round(r: i32): i32 {
    let xs: (i32, i32)[] = [];
    let i: i32 = 0;
    while (i < 4) {
        let t: (i32, i32) = (i, i + 1);
        xs = xs.append(t);
        i = i + 1;
    }
    let acc: i32 = 0;
    let j: i32 = 0;
    while (j < xs.len()) { acc = acc + xs[j].0 + xs[j].1; j = j + 1; }
    return acc;
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 7;
}`,
			want: 4,
		},
		{
			// Returned from inside the loop — moved out, so the callee must not free
			// it at its own exit sweep.
			name: "returned_from_inside_the_loop",
			src: `function build(r: i32): (i32, i32) {
    let i: i32 = 0;
    while (i < 4) {
        let t: (i32, i32) = (i, i + r);
        if (i == 3) { return t; }
        i = i + 1;
    }
    return (0, 0);
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) {
        let p: (i32, i32) = build(r);
        acc = acc + p.0 + p.1;
        r = r + 1;
    }
    return acc % 7;
}`,
			want: 6,
		},
		{
			// The declaring branch is never taken, so the slot is never written. It
			// must route a guarded null rather than stack garbage — the prologue
			// zeroes the whole body slot range, and this pins that it still does.
			name: "declaring_branch_never_taken",
			src: `function round(r: i32): i32 {
    let acc: i32 = 0;
    if (r > 1000) {
        let t: (i32, i32) = (r, 1);
        acc = t.0 + t.1;
    }
    return acc + r;
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 7;
}`,
			want: 1,
		},
		{
			// A block-scoped Option whose Some-arm payload leaves as a bare ident, so
			// the callee may retain the buffer.
			name: "block_scoped_option_payload_escapes",
			src: `function take(a: i32[]): i32 { return a.len(); }
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
        match (o) { Some(t) => { acc = acc + take(t.1); }, None => {} }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 71;
}`,
			want: 70,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "bs_hazard_"+tc.name, asm)
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
