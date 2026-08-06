package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Rebound scalar-tuple reclaim (#6127) ------------------------------------
//
// The TUP: class already credits a fresh, non-escaping scalar-tuple local: the
// exit sweep frees its FINAL box, and a `var` RE-DECLARATION frees the prior one
// (bind_var_slot's emit_arr_store carries a `|| slot_is_reclaimable_tuple`
// release term). lower_stmt_assign never carried that term, so a REBIND leaked
// every superseded box. A gate matrix isolated it — same tuple, differing only
// in how the local is written:
//
//	single bind                    0   balanced
//	declared inside the loop    4000   3 of 4 freed (a separate residue)
//	REBOUND in a loop          16000   only the exit sweep's free
//
// This is the declare-vs-reassign split #6218 / #6232 / #6225 closed for enums
// and Options, a third time.
//
// The "TUPRB:" credit layered on top of TUP: is what the assign path needs and
// the `var` path does not: a TUP: name is admitted on its DECLARATION alone, and
// nothing there constrains what a later `t = …` stores. Granting the release
// only when EVERY assignment builds a fresh tuple box is what keeps an aliasing
// rebind (`t = u`) from freeing a box its real owner still holds.
//
// These assert allocs == frees alongside live_bytes == 0: frees > allocs is a
// double free, frees < allocs an unclaimed box, and they mean different bugs.

// TestSelfHostTupleRebindReclaimX86_64 — a rebound scalar tuple reclaims every
// superseded box, and the forms that were already balanced stay balanced.
func TestSelfHostTupleRebindReclaimX86_64(t *testing.T) {
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

	t.Run("rebound_in_loop", func(t *testing.T) {
		src := `function round(r: i32): i32 {
    var t: (i32, i32) = (r, 0);
    var i: i32 = 0;
    while (i < 4) {
        t = (i, i + 1);
        i = i + 1;
    }
    return t.0;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 7;
}`
		allocs, frees, live := counts(t, "tup_rebound", src, 6)
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0 — every superseded tuple box must be freed "+
				"at the rebind; the leak scales with the iteration count, so any "+
				"nonzero here is unbounded", live)
		}
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
		}
	})

	t.Run("rebind_reads_the_old_value", func(t *testing.T) {
		// `t = (t.0 + 1, t.1 + i)` — the RHS is lowered BEFORE the release, so the
		// reads of the superseded box have all completed by the time it is freed.
		// Admitted by the binding annotation rather than by element shapes, which
		// is why the rebind check needs the declaration's type.
		src := `function round(r: i32): i32 {
    var t: (i32, i32) = (r, 1);
    var i: i32 = 0;
    while (i < 4) {
        t = (t.0 + 1, t.1 + i);
        i = i + 1;
    }
    return t.0 + t.1;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 7;
}`
		allocs, frees, live := counts(t, "tup_rebind_selfread", src, 2)
		if allocs != frees || live != 0 {
			t.Errorf("allocs=%d frees=%d live=%d — must balance; a wrong exit code here "+
				"would instead mean the release ran before the RHS finished reading",
				allocs, frees, live)
		}
	})

	t.Run("rebound_inside_a_branch", func(t *testing.T) {
		// The rebind sits two blocks deep (if inside while), so the whole-name
		// rebind scan has to recurse to find it.
		src := `function round(r: i32): i32 {
    var t: (i32, i32) = (r, 1);
    var i: i32 = 0;
    while (i < 4) {
        if (i % 2 == 0) { t = (i, i + 1); }
        i = i + 1;
    }
    return t.0 + t.1;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 7;
}`
		allocs, frees, live := counts(t, "tup_rebind_branch", src, 3)
		if allocs != frees || live != 0 {
			t.Errorf("allocs=%d frees=%d live=%d — must balance", allocs, frees, live)
		}
	})

	t.Run("single_bind_unchanged", func(t *testing.T) {
		// Never reassigned, so it earns no TUPRB: credit and stays the exit
		// sweep's. If both claimed it, its box would be dec'd twice.
		src := `function round(r: i32): i32 {
    var t: (i32, i32) = (r, r + 1);
    return t.0 + t.1;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 7;
}`
		allocs, frees, live := counts(t, "tup_single", src, 4)
		if allocs != frees || live != 0 {
			t.Errorf("allocs=%d frees=%d live=%d — a single-bind tuple is the exit "+
				"sweep's, and must stay balanced", allocs, frees, live)
		}
	})
}

// TestSelfHostTupleRebindHazardsX86_64 — the shapes the credit must still
// REFUSE. A wrongly-granted one frees a box something else still reads, so these
// assert behaviour: the failure mode is a wrong answer or a crash, not a leak.
func TestSelfHostTupleRebindHazardsX86_64(t *testing.T) {
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
			// `var u = t` makes the slot a second reference. Releasing at the next
			// rebind would dangle `u`, which is read after the loop. Refused by the
			// shared body_unsafe_for escape gate.
			name: "aliased_to_a_second_local",
			src: `function round(r: i32): i32 {
    var t: (i32, i32) = (r, 1);
    var u: (i32, i32) = t;
    var i: i32 = 0;
    while (i < 4) {
        t = (i, i + 1);
        i = i + 1;
    }
    return t.0 + u.0 + u.1;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 7;
}`,
			want: 2,
		},
		{
			// The box is stored into a container that outlives the rebind. Freeing
			// the superseded one would leave the array pointing at released memory.
			name: "escapes_into_a_container",
			src: `function round(r: i32): i32 {
    var t: (i32, i32) = (r, 1);
    var xs: (i32, i32)[] = [];
    var i: i32 = 0;
    while (i < 4) {
        xs = xs.append(t);
        t = (i, i + 1);
        i = i + 1;
    }
    var acc: i32 = 0;
    var j: i32 = 0;
    while (j < xs.len()) { acc = acc + xs[j].0 + xs[j].1; j = j + 1; }
    return acc + t.0;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 7;
}`,
			want: 6,
		},
		{
			// The callee STORES the tuple into an array it returns, so the box
			// outlives every rebind. A borrowing callee (`take(t)` that only reads)
			// is admissible and does reclaim — this is the case that separates the
			// two, and it is the one an escape gate keyed on syntax alone would miss.
			name: "call_arg_the_callee_keeps",
			src: `function keep(p: (i32, i32)): (i32, i32)[] { return [p]; }
function round(r: i32): i32 {
    var t: (i32, i32) = (r, 1);
    var held: (i32, i32)[] = keep(t);
    var i: i32 = 0;
    while (i < 4) {
        t = (i, i + 1);
        i = i + 1;
    }
    return held[0].0 + held[0].1 + t.0;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 71;
}`,
			want: 25,
		},
		{
			// The tuple leaves by return, so its box belongs to the caller.
			name: "escaping_return",
			src: `function build(r: i32): (i32, i32) {
    var t: (i32, i32) = (r, 1);
    var i: i32 = 0;
    while (i < 4) {
        t = (i, i + r);
        i = i + 1;
    }
    return t;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) {
        var p: (i32, i32) = build(r);
        acc = acc + p.0 + p.1;
        r = r + 1;
    }
    return acc % 7;
}`,
			want: 6,
		},
		{
			// One rebind is a fresh literal and one is a call result. The whole name
			// is disqualified: releasing at the fresh rebind would free the box the
			// call handed over, which the slot is the only reference to.
			name: "one_rebind_from_a_call",
			src: `function mk(k: i32): (i32, i32) { return (k, k + 1); }
function round(r: i32): i32 {
    var t: (i32, i32) = (r, 1);
    var i: i32 = 0;
    while (i < 4) {
        if (i > 1) { t = (i, i + 1); } else { t = mk(i); }
        i = i + 1;
    }
    return t.0 + t.1;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 71;
}`,
			want: 61,
		},
		{
			// Every rebind stores another live local's box. Freeing the superseded
			// one is freeing `u`'s, which is read after the loop.
			name: "rebind_aliases_another_local",
			src: `function round(r: i32): i32 {
    var t: (i32, i32) = (r, 1);
    var u: (i32, i32) = (r + 1, 2);
    var i: i32 = 0;
    while (i < 4) {
        t = u;
        i = i + 1;
    }
    return t.0 + t.1 + u.0;
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { acc = acc + round(r); r = r + 1; }
    return acc % 7;
}`,
			want: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "tup_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"reclaim credit was granted to a shape that still holds a live "+
					"reference (use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}
