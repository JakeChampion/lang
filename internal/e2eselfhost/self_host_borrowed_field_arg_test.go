package e2eselfhost

import (
	"fmt"
	"testing"
)

// --- A field read at a BORROWABLE call-arg position is not a move (#6691) ----
//
// `tag(o.v)` cost the struct local `o` its entire reclaim credit: the NODEEP
// field-move scan reads every non-scalar field read in a direct call argument as
// a move, so `emit_struct_field_drops` withheld the deep drop AND no
// `__field_reclaim_S` was emitted at all. Every superseded box and the `xs`
// buffer it owned then leaked per ITERATION — 1840 / 10240 / 98560 bytes at
// k = 1 / 8 / 32, against a flat 0 on native.
//
// `borrowable_params_of` already answers the question the scan needs: a param is
// admitted only when the callee provably never returns, stores, slices or
// captures it, so such a position leaves the field owned by `o` alone. It is the
// same registry and the same Level-2 rule `expr_unsafe_for` applies to a
// bare-ident argument, where it is trusted for caller-side FREES — a strictly
// stronger action than keeping a deep drop.
func borrowedFieldArgSrc(k int) string {
	return fmt.Sprintf(`enum V { A(i32[]), B }
struct S { xs: i32[], v: V, n: i32 }

function tagof(v: V): i32 {
    match (v) {
        V.A(d) => { return d.len(); },
        V.B => { return 0; }
    }
}

function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], v: V.A([9, 8, 7]), n: 0 };
    var i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), v: o.v, n: i };
        i = i + 1;
    }
    return o.xs.len() + tagof(o.v);
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 10) { t = t + work(%d); r = r + 1; }
    return t & 63;
}`, k)
}

// borrowedFieldArgExit: `work` returns (2 + k) + 3, ten calls summed, masked to
// six bits.
func borrowedFieldArgExit(k int) int { return (10 * (k + 5)) & 63 }

// The callee that RETAINS its argument must keep marking. `keep` returns its
// param, so `borrowable_params_of` refuses it and `kept` genuinely aliases the
// enum box `o` still holds — deep-dropping a superseded `o` would free the box
// `tagof(kept)` reads after the loop, which is a wrong answer, not a byte count.
const borrowedFieldArgRetainedSrc = `enum V { A(i32[]), B }
struct S { xs: i32[], v: V, n: i32 }

function tagof(v: V): i32 {
    match (v) {
        V.A(d) => { return d.len(); },
        V.B => { return 0; }
    }
}

function keep(v: V): V { return v; }

function work(k: i32): i32 {
    var o: S = S { xs: [1, 2], v: V.A([9, 8, 7]), n: 0 };
    var kept: V = keep(o.v);
    var i: i32 = 0;
    while (i < k) {
        o = S { xs: o.xs.append(i), v: o.v, n: i };
        i = i + 1;
    }
    return o.xs.len() + tagof(kept);
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 10) { t = t + work(8); r = r + 1; }
    return t & 63;
}`

// TestSelfHostBorrowedFieldArgReclaimX86_64 — the k curve is the discriminator:
// the defect was per-iteration, so a single absolute count could not tell it
// from the flat residual the enum payload's shallow-drop model leaves behind.
func TestSelfHostBorrowedFieldArgReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	var first int64 = -1
	for _, k := range []int{1, 2, 8, 32} {
		name := fmt.Sprintf("bfa_k%d", k)
		live, _, exit := leakSummary(t, gcc, runner, driverBin, dir, name, borrowedFieldArgSrc(k))
		if want := borrowedFieldArgExit(k); exit != want {
			t.Fatalf("k=%d exited %d, want %d — the payload `tagof` reads back was released early", k, exit, want)
		}
		if first < 0 {
			first = live
			continue
		}
		if live != first {
			t.Errorf("k=%d leaked %d bytes against %d at k=1 — `tagof(o.v)` is taking the local's "+
				"reclaim credit again, so every superseded box and its `xs` buffer strands (#6691)",
				k, live, first)
		}
	}

	t.Run("retaining-callee-still-marks", func(t *testing.T) {
		_, _, exit := leakSummary(t, gcc, runner, driverBin, dir, "bfa_retained", borrowedFieldArgRetainedSrc)
		if exit != 2 {
			t.Errorf("exited %d, want 2 — `keep` returns its param, so `kept` aliases the box `o` "+
				"holds and the exemption must not reach it", exit)
		}
	})
}
