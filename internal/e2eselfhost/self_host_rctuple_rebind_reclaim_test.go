package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Rebound rc-tuple reclaim (#6127 tuple_churn, rc half) -------------------
//
// #6251 gave a fresh rc-tuple local a scope-exit sweep ("TUPRCS:") and left the
// REBOUND form deliberately unfixed, pinned as still leaking 32000, because the
// assign path emitted no release for the class and so the final value's shape
// could not be judged from the declaration alone. A rebound rc-tuple therefore
// freed NOTHING: allocs=1000 frees=0 live_bytes=32000 over 100 rounds on
// `(i32, string)`, against 0 on native.
//
// Two halves, and #6232 is the worked example of why both are needed — with only
// the assign-site release the LAST chain still leaks, and with only the credit
// every superseded one does:
//
//   - lower_stmt_assign now routes a reclaimable rc-tuple slot through the
//     existing emit_tuple_deep_reinit_store, the same release the StmtVar
//     re-declaration path has always used;
//   - the "TUPRCS:" sweep credit admits a reassigned name when every assignment
//     rebuilds a fresh rc-tuple of the SAME annotated type
//     (all_assigns_fresh_rc_tuple), which is what makes the declaration's type a
//     sound description of the final value the sweep frees by.
//
// This is a DEEP drop — the rc children as well as the box — so a wrongly-granted
// release frees a payload something else still reads, not merely a box.

const rcTupleRebindSrc = `function round(): i32 {
    var p: (i32, string) = (0, "hello");
    var i: i32 = 0;
    while (i < 4) { p = (i, "hello"); i = i + 1; }
    return p.0;
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 7;
}`

// TestSelfHostRcTupleRebindReclaimX86_64 — a rebound rc-tuple frees every chain it
// supersedes AND its final value. allocs == frees is load-bearing: frees short of
// allocs is the leak this closes, frees ABOVE allocs would mean the assign-site
// deep drop and the scope-exit sweep both claimed one chain (a double free).
func TestSelfHostRcTupleRebindReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, rcTupleRebindSrc, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "rctuple_rebind", asm)
	stderr, exit := hevRun(t, runner, progBin)
	// 300 % 7 = 6, confirmed against both oracles (bin/fern -interp and native
	// -target x86-64), not read off the self-host run this test exists to check.
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
		t.Errorf("allocs=%d frees=%d — a rebound rc-tuple must deep-drop each chain it "+
			"supersedes and the sweep must release the last one; frees > allocs means "+
			"both claimed one chain (double free)", allocs, frees)
	}
	if live != 0 {
		t.Errorf("live_bytes=%d, want 0 — a rebound rc-tuple leaks its box AND its rc "+
			"child per assignment, so this scales with the loop count", live)
	}
}

// TestSelfHostRcTupleRebindHazardsX86_64 — the shapes the deep drop must still
// REFUSE. Because this releases the rc CHILD as well as the box, a wrongly-granted
// credit frees a payload something else still points at, so the failure is a wrong
// answer or a crash rather than a leak. Every `want` came from the interpreter and
// native x86-64 agreeing; all five are currently refused, verified by checking each
// still leaks rather than assuming a passing exit means the gate fired.
func TestSelfHostRcTupleRebindHazardsX86_64(t *testing.T) {
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
			// The rc element is extracted whole into a local that outlives the
			// rebind. Deep-dropping the old chain would free the buffer `keep`
			// still points at — rctuple_payload_escapes is what refuses this.
			name: "rc_element_extracted_to_local",
			src: `function round(i: i32): i32 {
    var p: (i32, i32[]) = (i, [i, i + 1]);
    var keep: i32[] = p.1;
    p = (i + 1, [i + 2, i + 3]);
    return keep[0] + keep[1] + p.1[0];
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 18,
		},
		{
			// One non-fresh rebind, whose rc position is a LIVE local's buffer.
			// This is the shape the all-assignments gate exists for: judging only
			// the assignment at hand would deep-drop `xs` underneath its owner.
			name: "nonfresh_rebind_disqualifies",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var p: (i32, i32[]) = (i, [i, i + 2]);
    p = (i, xs);
    return p.1[0] + xs[1];
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 9,
		},
		{
			// The tuple is stored into a container before each rebind, so the old
			// chain is still reachable through the array.
			name: "escapes_to_container",
			src: `function round(i: i32): i32 {
    var keep: (i32, i32[])[] = [];
    var p: (i32, i32[]) = (0, [0, 0]);
    var k: i32 = 0;
    while (k < 3) { p = (k, [k, k + 1]); keep = keep.append(p); k = k + 1; }
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < keep.len()) { t = t + keep[j].0 + keep[j].1[1]; j = j + 1; }
    return t;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 27,
		},
		{
			// Rebound inside a callee and then returned: the value is moved out, so
			// the callee must not drop the chain it hands back.
			name: "returned_to_caller",
			src: `function mk(n: i32): (i32, i32[]) {
    var p: (i32, i32[]) = (0, [0, 0]);
    var k: i32 = 0;
    while (k < 3) { p = (k, [k, n]); k = k + 1; }
    return p;
}
function round(n: i32): i32 { var t: (i32, i32[]) = mk(n); return t.0 + t.1[1]; }
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r % 3); r = r + 1; } return t % 97; }`,
			want: 8,
		},
		{
			// The string element is compared and measured after the last rebind.
			// Whatever the gate decides, the reads must still see the original
			// bytes — this is the assertion that a granted drop never reaches a
			// live payload.
			name: "string_element_read_after_rebind",
			src: `function round(): i32 {
    var p: (i32, string) = (0, "hello_there_world");
    var i: i32 = 0;
    while (i < 4) { p = (i, "hello_there_world"); i = i + 1; }
    if (p.1 != "hello_there_world") { return 1; }
    return p.0 + p.1.len();
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(); r = r + 1; } return t % 97; }`,
			want: 60,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "rctuple_rebind_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash means the DEEP drop "+
					"was granted to a shape that still holds a live reference to the payload "+
					"(use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}
