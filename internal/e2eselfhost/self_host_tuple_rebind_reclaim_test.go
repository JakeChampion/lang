package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Rebound scalar-tuple reclaim (#6127) -----------------------------------
//
// A fresh non-escaping all-scalar tuple local is credited "TUP:", and its box is
// shallow-freed at scope exit. What leaked was every box a REBIND superseded: the
// StmtVar path hands slot_is_reclaimable_tuple to emit_arr_store's do_dec, and
// lower_stmt_assign's tail did not, so the release fired at a re-declaration and
// never at a reassignment. Isolating the sub-shapes is what located it — the same
// split that separated the two enum classes from this one:
//
//	single bind, no rebind      allocs=100 frees=100        0
//	REBOUND in a loop           allocs=500 frees=100    16000
//
// Only the final value was ever freed, so the leak scales with the iteration
// count. The new "TUPRB:" credit adds the missing disjunct, gated on every
// assignment constructing a fresh all-scalar tuple literal.
//
// These assert allocs == frees alongside live_bytes == 0: frees > allocs would be
// a double free (the rebind release and the exit sweep both claiming one box),
// frees < allocs an unclaimed one.

// TestSelfHostTupleRebindReclaimX86_64 — a rebound scalar tuple frees every
// superseded box, and the single-bind form that was already correct stays so.
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
		src := `function round(i: i32): i32 {
    var t: (i32, i32) = (i, i + 1);
    var k: i32 = 0;
    while (k < 4) { t = (k, k + 1); k = k + 1; }
    return t.0 + t.1;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`
		allocs, frees, live := counts(t, "tuprb_rebound", src, 36)
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0 — every superseded tuple box must be freed "+
				"at the rebind; the leak scales with the iteration count, so any "+
				"nonzero here is unbounded", live)
		}
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
		}
	})

	t.Run("conditional_rebind", func(t *testing.T) {
		// Only some iterations supersede a box. emit_arr_store's cow guard is what
		// makes the untaken path a no-op rather than a second release.
		src := `function round(i: i32): i32 {
    var t: (i32, i32) = (i, i + 1);
    var k: i32 = 0;
    while (k < 4) { if (k % 2 == 0) { t = (k, k + 1); } k = k + 1; }
    return t.0 + t.1;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`
		allocs, frees, live := counts(t, "tuprb_cond", src, 2)
		if live != 0 || allocs != frees {
			t.Errorf("allocs=%d frees=%d live=%d — a conditionally rebound tuple must "+
				"balance too", allocs, frees, live)
		}
	})

	t.Run("single_bind_unchanged", func(t *testing.T) {
		// Owned by the exit sweep alone. If the rebind credit also claimed it, its
		// box would be dec'd twice.
		src := `function round(i: i32): i32 {
    var t: (i32, i32) = (i, i + 1);
    return t.0 + t.1;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`
		allocs, frees, live := counts(t, "tuprb_single", src, 40)
		if allocs != frees || live != 0 {
			t.Errorf("allocs=%d frees=%d live=%d — a single-bind scalar tuple is the "+
				"exit sweep's and must stay balanced", allocs, frees, live)
		}
	})

	t.Run("borrowing_call_reclaims", func(t *testing.T) {
		// Passing the tuple to a function that only READS it is a borrow, not an
		// escape, so the credit survives — the same judgement native makes (it frees
		// all 240 here). Pinned so a future tightening of the escape gate has to be
		// a deliberate choice rather than a silent regression.
		src := `function take(p: (i32, i32)): i32 { return p.0 + p.1; }
function round(i: i32): i32 {
    var t: (i32, i32) = (i, i + 1);
    var acc: i32 = take(t);
    var k: i32 = 0;
    while (k < 3) { t = (k, k + 1); acc = acc + take(t); k = k + 1; }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`
		allocs, frees, live := counts(t, "tuprb_borrow_call", src, 22)
		if allocs != frees || live != 0 {
			t.Errorf("allocs=%d frees=%d live=%d", allocs, frees, live)
		}
	})
}

// TestSelfHostTupleRebindHazardsX86_64 — the shapes the credit must still REFUSE.
// Each still leaks, deliberately: a wrongly-granted credit frees a box something
// else still points at, so these assert BEHAVIOUR (exit code), because the failure
// mode is a wrong answer or a crash rather than a leak. Every `want` comes from
// the interpreter and the native backend agreeing, never from the self-host run
// being checked.
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
			// Aliased to a second local that is read after the rebind. Releasing the
			// superseded box would dangle `keep`.
			name: "aliased_to_second_local",
			src: `function round(i: i32): i32 {
    var t: (i32, i32) = (i, i + 1);
    var keep: (i32, i32) = t;
    var k: i32 = 0;
    while (k < 3) { t = (k, k + 1); k = k + 1; }
    return keep.0 + keep.1 + t.0;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 28,
		},
		{
			// Stored into a container before being superseded — the array holds the
			// box the rebind would free.
			name: "stored_into_container",
			src: `function round(i: i32): i32 {
    var xs: (i32, i32)[] = [];
    var t: (i32, i32) = (i, i + 1);
    var k: i32 = 0;
    while (k < 3) { xs = xs.append(t); t = (k, k + 1); k = k + 1; }
    return xs.len() + xs[0].0 + t.1;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 0,
		},
		{
			// Rebound from an ALIAS rather than a fresh literal: after `t = u` the
			// slot is a second reference to u's box, so releasing it at the next
			// rebind — or letting the exit sweep dec both — is a double free. This is
			// what the ExprTuple requirement in all_assigns_fresh_scalar_tuple buys.
			name: "rebound_from_alias",
			src: `function round(i: i32): i32 {
    var u: (i32, i32) = (i, i + 1);
    var t: (i32, i32) = (0, 0);
    var k: i32 = 0;
    while (k < 3) { t = u; k = k + 1; }
    return t.0 + t.1 + u.0;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 45,
		},
		{
			// The tuple escapes by return, so the callee must not free it.
			name: "escaping_return",
			src: `function mk(i: i32): (i32, i32) {
    var t: (i32, i32) = (i, i + 1);
    var k: i32 = 0;
    while (k < 3) { t = (k, k + 1); k = k + 1; }
    return t;
}
function round(i: i32): i32 { var p: (i32, i32) = mk(i); return p.0 + p.1; }
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 16,
		},
		{
			// A self-referencing rebind. Refused (expr_is_scalarish rejects an element
			// read), so it keeps leaking — conservatism, not a soundness requirement,
			// since emit_arr_store releases only after the RHS is lowered. Pinned so
			// that if expr_is_scalarish is ever widened, the answer is checked.
			name: "self_referencing_rebind",
			src: `function round(i: i32): i32 {
    var t: (i32, i32) = (i, i + 1);
    var k: i32 = 0;
    while (k < 3) { t = (t.1, t.0 + 1); k = k + 1; }
    return t.0 + t.1;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 17,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "tuprb_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"reclaim credit was granted to a shape that still holds a live "+
					"reference (use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}
