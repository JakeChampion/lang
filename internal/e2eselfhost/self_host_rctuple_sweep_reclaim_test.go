package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Sweepable rc-tuple reclaim (#6127) -------------------------------------
//
// A fresh non-escaping tuple local carrying an rc element — an array literal, a
// string, a nested tuple, a reclaim-struct — is credited "TUPRC:". That credit was
// consumed in only one place that runs: emit_tuple_deep_reinit_store on the
// StmtVar path. Nothing freed the local's FINAL
// value, so a SINGLE BIND with no reassignment anywhere leaked both the rc child
// and the tuple box, every round:
//
//	(i32, i32[]) single bind    allocs=200 frees=0    8000
//	(i32, string) single bind   allocs=200 frees=0    6400
//
// against 0 on native for both. #6127's comment 6 read this as a runtime-guard
// problem because two releases are visibly emitted in round() — but those are the
// declaration-site cow guard (a no-op on a first bind) and a sweep loop that only
// ever covered the ALL-SCALAR class.
//
// The new "TUPRCS:" credit adds the missing sweep. It is deliberately stricter
// than "TUPRC:" rather than a reuse of it, because the sweep has no init literal
// at exit and must free by TYPE — see the hazard test below for why that
// distinction is load-bearing.

// TestSelfHostRcTupleSweepReclaimX86_64 — the final value of a sweepable rc-tuple
// local is deep-freed: rc children first, then the box.
//
// allocs == frees is the load-bearing assertion. frees short of allocs is the leak
// this closes; frees ABOVE allocs would mean the sweep and the rebind path both
// claimed a box, which is a double free rather than a leak.
func TestSelfHostRcTupleSweepReclaimX86_64(t *testing.T) {
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

	// Every wantExit below was confirmed against BOTH oracles (bin/fern -interp and
	// the native x86-64 backend), never read off the self-host run under test.
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			// The array-element shape — the one tuple_lit_rc_reclaimable's own header
			// documents, and it leaked everything on a single bind.
			name: "array_elem_single_bind",
			src: `function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    return t.0 + t.1.len();
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 4,
		},
		{
			// A string element: construction str_box's it into a fresh sole-owned copy,
			// so the sweep's rc-aware __fern_str_free is balanced.
			name: "string_elem_single_bind",
			src: `function round(i: i32): i32 {
    var t: (i32, string) = (i, "abc");
    return t.0 + t.1.len();
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 21,
		},
		{
			// The rc element is never read at all. Pins that the sweep fires on the
			// binding itself rather than on some use.
			name: "rc_elem_never_read",
			src: `function round(i: i32): i32 {
    var t: (i32, string) = (i, "abc");
    return t.0;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 53,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, "rctupsweep_"+tc.name, tc.src, tc.want)
			if live != 0 {
				t.Errorf("live_bytes=%d, want 0 — the final rc-tuple value's children and "+
					"box must both be freed at scope exit; this leaks on every call, so "+
					"it is unbounded", live)
			}
			if allocs != frees {
				t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
			}
		})
	}
}

// TestSelfHostRcTupleSweepHazardsX86_64 — the shapes "TUPRCS:" must still REFUSE.
//
// The first case is the reason this is a separate, stricter credit rather than a
// reuse of "TUPRC:", and it is the specific use-after-free #6148 was reverted for.
// The scope-exit sweep has no init literal to consult, so it frees by TYPE
// (emit_tuple_type_child_drops), which dec's every rc-typed position BLIND.
// tuple_lit_rc_reclaimable — the "TUPRC:" gate — admits a bare-ident element, so
// `(xs, [i, i+2])` is "TUPRC:" while position 0 holds a live local's buffer.
// Sweeping that by type frees `xs` underneath its owner. tuple_arg_payload_fresh
// is what excludes it, the same gate OPTTUP already applies to the same helper.
//
// These assert BEHAVIOUR, not leak counts: a wrongly-granted credit produces a
// wrong answer or a crash. Each `want` came from the interpreter and the native
// backend agreeing.
func TestSelfHostRcTupleSweepHazardsX86_64(t *testing.T) {
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
			// THE trap. An rc-typed position holding a LIVE LOCAL's buffer, read after
			// the tuple is built. A type-driven sweep would free xs's buffer while xs
			// still owns it.
			name: "ident_elem_aliases_live_local",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32[], i32[]) = (xs, [i, i + 2]);
    return t.0.len() + xs[0] + xs[1];
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
			// The rc element is extracted out of the tuple into a local that outlives
			// the read. rctuple_payload_escapes refuses the whole name.
			name: "rc_elem_extracted_to_local",
			src: `function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    var keep: i32[] = t.1;
    return keep.len() + keep[0];
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 44,
		},
		{
			// The tuple escapes by return — moved to the caller, so the callee must
			// not sweep it.
			name: "escaping_return",
			src: `function mk(i: i32): (i32, i32[]) { var t: (i32, i32[]) = (i, [i, i + 1]); return t; }
function round(i: i32): i32 { var p: (i32, i32[]) = mk(i); return p.0 + p.1.len(); }
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 44,
		},
		{
			// The rc element leaves as a bare call argument, so the callee may retain
			// the buffer.
			name: "rc_elem_escapes_into_call",
			src: `function take(a: i32[]): i32 { return a.len(); }
function round(i: i32): i32 {
    var t: (i32, i32[]) = (i, [i, i + 1]);
    return t.0 + take(t.1);
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 44,
		},
		{
			// REASSIGNED, so refused: the assign path emits no release for this class,
			// and the final value's shape cannot be judged from the declaration alone.
			// Still leaking by design — pinned so that widening the assign path has to
			// come with a deliberate change here.
			name: "reassigned_refused",
			src: `function round(i: i32): i32 {
    var t: (i32, string) = (i, "abc");
    var k: i32 = 0;
    while (k < 4) { t = (k, "xy"); k = k + 1; }
    return t.0 + t.1.len();
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "rctupsweep_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"sweep freed memory something else still owns (use-after-free), not "+
					"merely that the shape leaked", exit, tc.want)
			}
		})
	}
}
