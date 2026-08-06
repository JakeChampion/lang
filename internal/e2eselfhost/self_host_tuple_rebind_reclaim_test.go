package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Rebound scalar-tuple reclaim (#6127) -----------------------------------
//
// A fresh non-escaping scalar tuple local already earned the "TUP:" credit whether
// or not it was rebound — that credit's escape gate (body_unsafe_for) never looked
// at reassignment. But "TUP:" is only consumed by the scope-exit sweep, so a loop
// rebind freed the FINAL box and leaked every box the rebinds superseded:
// allocs=500 frees=100 live_bytes=16000 over 100 rounds against 0 on native, while
// the same tuple bound ONCE is flat at 0.
//
// So this was never an admission gap — the credit was already there and the
// release was missing. #6127's own notes read the measurement the other way, as an
// all-scalar tuple leaking on a SINGLE bind, and concluded "do not start from the
// rebind template". The 500/100/16000 row it cited is reproducibly the REBOUND
// probe; single-bind all-scalar measures 100/100/0.
//
// The new "TUPREBIND:" credit says the superseded box is releasable, which is a
// different claim from "TUP:" (the box is non-escaping and freeable at exit), so it
// is a separate credit rather than a widened one. It is granted only when EVERY
// assignment builds a fresh scalar tuple: one `p = q` rebind would leave the slot
// holding q's box, and the next assignment's release would free a box q still owns.
//
// The release itself is emit_arr_store's existing do_dec branch — cow-guarded, and
// a SHALLOW __fern_rc_dec, which is all a scalar tuple needs.

const tupleRebindSrc = `function round(): i32 {
    var p: (i32, i32) = (0, 1);
    var i: i32 = 0;
    while (i < 4) { p = (i, i + 1); i = i + 1; }
    return p.0;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 7;
}`

// TestSelfHostTupleRebindReclaimX86_64 — a rebound scalar-tuple local frees every
// box it supersedes as well as its final value. allocs == frees is the
// load-bearing assertion: frees short of allocs is the leak this closes, frees
// ABOVE allocs would mean the rebind release and the scope-exit sweep both claimed
// one box (a double free).
func TestSelfHostTupleRebindReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, tupleRebindSrc, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "tuple_rebind", asm)
	stderr, exit := hevRun(t, runner, progBin)
	// 6: confirmed against both oracles (bin/fern -interp and native -target
	// x86-64), not read off the self-host run this test exists to check.
	if exit != 6 {
		t.Fatalf("exited %d, want 6", exit)
	}
	summary := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			summary = line
		}
	}
	if summary == "" {
		t.Fatalf("no leakcheck summary")
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if allocs == 0 {
		t.Fatalf("allocated nothing — the probe is not exercising the path")
	}
	if allocs != frees {
		t.Errorf("allocs=%d frees=%d — a rebound scalar tuple must free the box each "+
			"assignment supersedes; frees > allocs means the rebind release and the "+
			"scope-exit sweep both claimed one box (double free)", allocs, frees)
	}
	if live != 0 {
		t.Errorf("live_bytes=%d, want 0 — one leaked tuple box per iteration, so this "+
			"scales with the loop count", live)
	}
}

// TestSelfHostTupleRebindHazardsX86_64 — the rebind shapes the release must still
// REFUSE, plus the granted shapes whose correctness is easy to break. Each asserts
// BEHAVIOUR: a wrongly-granted release frees a box something else still reads, so
// the failure is a wrong answer or a crash, not a leak. Every `want` was confirmed
// against both the interpreter and the native x86-64 backend.
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
			// Aliased into a second local that still reads the old box after the
			// rebind. body_unsafe_for flags the bare-ident init, so no credit at
			// all — releasing here would free the box `keep` holds.
			name: "aliased_to_local",
			src: `function round(): i32 {
    var p: (i32, i32) = (1, 2);
    var keep: (i32, i32) = p;
    p = (3, 4);
    return keep.0 + keep.1 + p.0 + p.1;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(); r = r + 1; } return t % 97; }`,
			want: 30,
		},
		{
			// One non-fresh rebind (`p = q`) disqualifies the NAME, not just that
			// assignment: after it the slot holds q's box, so the LATER `p = (7, 8)`
			// would otherwise free a box `q` still owns. This is why the gate is
			// all-assignments rather than per-assignment.
			name: "nonfresh_rebind_disqualifies",
			src: `function round(): i32 {
    var q: (i32, i32) = (5, 6);
    var p: (i32, i32) = (1, 2);
    p = q;
    p = (7, 8);
    return q.0 + q.1 + p.0 + p.1;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(); r = r + 1; } return t % 97; }`,
			want: 78,
		},
		{
			// Stored into a container before being overwritten — the old box is
			// still reachable through the array.
			name: "escapes_to_container",
			src: `function round(): i32 {
    var keep: (i32, i32)[] = [];
    var p: (i32, i32) = (0, 0);
    var i: i32 = 0;
    while (i < 3) { p = (i, i + 1); keep = keep.append(p); i = i + 1; }
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < keep.len()) { t = t + keep[k].0 + keep[k].1; k = k + 1; }
    return t;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(); r = r + 1; } return t % 97; }`,
			want: 27,
		},
		{
			// Returned to the caller: the value is MOVED OUT, so the callee must not
			// release the box it hands back.
			name: "returned_to_caller",
			src: `function mk(n: i32): (i32, i32) {
    var p: (i32, i32) = (0, 0);
    var i: i32 = 0;
    while (i < 3) { p = (i, n); i = i + 1; }
    return p;
}
function round(n: i32): i32 { var t: (i32, i32) = mk(n); return t.0 + t.1; }
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r % 3); r = r + 1; } return t % 97; }`,
			want: 8,
		},
		{
			// A borrowing callee does NOT disqualify — this shape is GRANTED, and
			// pins that the release still produces the right answer when the old box
			// was read by a call earlier in the same iteration.
			name: "borrowed_by_call_still_correct",
			src: `function sink(x: (i32, i32)): i32 { return x.0 + x.1; }
function round(): i32 {
    var p: (i32, i32) = (0, 0);
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) { p = (i, i + 1); t = t + sink(p); i = i + 1; }
    return t;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(); r = r + 1; } return t % 97; }`,
			want: 27,
		},
		{
			// The new value is built by reading the old one. The cow guard is what
			// keeps this correct — the RHS is lowered before the release, and a
			// self-store must not free the live box.
			name: "rebind_reads_old_value",
			src: `function round(): i32 {
    var p: (i32, i32) = (1, 2);
    var i: i32 = 0;
    while (i < 3) { p = (p.0 + 1, p.1 + 1); i = i + 1; }
    return p.0 + p.1;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(); r = r + 1; } return t % 97; }`,
			want: 27,
		},
		{
			// A rebind guarded by a branch, so on one path the box the next
			// assignment supersedes came from the declaration and on the other from
			// the branch. Pins that the release copes with both.
			name: "rebind_under_branch",
			src: `function round(c: boolean): i32 {
    var p: (i32, i32) = (0, 0);
    if (c) { p = (1, 2); }
    p = (3, 4);
    return p.0 + p.1;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r % 2 == 0); r = r + 1; } return t % 97; }`,
			want: 21,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "tuple_rebind_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"superseded-box release was granted to a shape that still holds a live "+
					"reference (use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}
