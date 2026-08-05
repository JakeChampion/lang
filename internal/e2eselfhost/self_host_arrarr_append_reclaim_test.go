package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Append-built arr-of-arr row reclaim (#6092) -------------------
//
// irlower's "ARRARR:" credit routes a fresh, non-escaping arr-of-arr local to
// the deep release (__fern_arrarr_free / __fern_strarrarr_free), which frees
// the inner row buffers and then the outer one. That credit used to be refused
// outright for any REASSIGNED name — and `g = g.append(row)` is a
// reassignment — so an append-BUILT `T[][]` fell out of the credit entirely and
// leaked one row buffer per append. Sound, but unbounded in a loop.
//
// The string[] class (SARR:) has validated the self-append rebind individually
// since #4355 rather than excluding it wholesale; arrarr_unsafe_for is that
// same treatment for the two-level class.
//
// Found by the self-host FERN_LEAKCHECK port (#6091) as a differential against
// the native compiler, which frees everything on the same program. That is what
// these tests assert: not an absolute byte count, but AGREEMENT with native,
// which is the only reading that stays meaningful as allocation shapes change.

// arrarrAppendChurnSrc builds and drops an arr-of-arr by append, 200 times. If
// the rows are not reclaimed the leak scales with the iteration count, so a
// regression shows up as a large live_bytes rather than a marginal one.
const arrarrAppendChurnSrc = `function round(): i32 {
    var keep: i32[][] = [];
    var i: i32 = 0;
    while (i < 3) { keep = keep.append([1, 2, 3, 4]); i = i + 1; }
    return keep.len();
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { t = t + round(); r = r + 1; }
    return t / 200;
}`

// arrarrStrAppendChurnSrc is the string-inner sibling. Its rows hold string
// LITERALS, so the strict "ARRARRS:" credit applies and the release walks each
// element box via __fern_str_arr_free before freeing the row.
const arrarrStrAppendChurnSrc = `function round(): i32 {
    var keep: string[][] = [];
    var i: i32 = 0;
    while (i < 3) { keep = keep.append(["ab", "cd"]); i = i + 1; }
    return keep.len();
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 200) { t = t + round(); r = r + 1; }
    return t / 200;
}`

// TestSelfHostArrArrAppendReclaimX86_64 — an append-built arr-of-arr reclaims
// its rows, and leaves live_bytes where native leaves it.
func TestSelfHostArrArrAppendReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"scalar_rows", arrarrAppendChurnSrc},
		{"string_rows", arrarrStrAppendChurnSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrarr_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != 3 {
				t.Fatalf("program exited %d, want 3", exit)
			}

			summary := ""
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "leakcheck: ") {
					summary = line
				}
			}
			if summary == "" {
				t.Fatal("no leakcheck summary")
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("parse %q: %v", summary, err)
			}
			if allocs == 0 {
				t.Fatal("program allocated nothing — the probe is not exercising the path")
			}
			if live != 0 {
				t.Errorf("%s: live_bytes=%d (allocs=%d frees=%d), want 0 — an append-built "+
					"arr-of-arr must reclaim its row buffers; the leak scales with the "+
					"iteration count, so any nonzero here is unbounded in a loop",
					summary, live, allocs, frees)
			}
		})
	}
}

// TestSelfHostArrArrAppendHazardsX86_64 — the shapes the credit must still
// REFUSE. Each one keeps a live reference the deep free would dangle, so the
// only safe outcome is the shallow release (rows leak, soundly). Asserted
// through behaviour rather than through the credit directly: a wrongly-granted
// credit is a use-after-free, which shows up as a wrong answer or a crash.
func TestSelfHostArrArrAppendHazardsX86_64(t *testing.T) {
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
			// A bound ROW aliases an inner buffer the deep free would release.
			name: "row_alias",
			src: `function main(): i32 {
    var g: i32[][] = [];
    var i: i32 = 0;
    while (i < 3) { g = g.append([1, 2, 3, 4]); i = i + 1; }
    var row: i32[] = g[0];
    return row[1] + g.len();
}`,
			want: 5,
		},
		{
			// The appended row is an IDENT, so the array does not solely own it;
			// freeing it would dangle `r`.
			name: "ident_row",
			src: `function main(): i32 {
    var g: i32[][] = [];
    var r: i32[] = [7, 8];
    var i: i32 = 0;
    while (i < 3) { g = g.append(r); i = i + 1; }
    return g[0][1] + r[0];
}`,
			want: 15,
		},
		{
			// The array ESCAPES by return, so the callee must not free it.
			name: "escaping_return",
			src: `function build(): i32[][] {
    var g: i32[][] = [];
    var i: i32 = 0;
    while (i < 3) { g = g.append([1, 2]); i = i + 1; }
    return g;
}
function main(): i32 { var q: i32[][] = build(); return q[2][1] + q.len(); }`,
			want: 5,
		},
		{
			// A non-append reassignment must still sink the credit outright.
			name: "rebound_to_other",
			src: `function main(): i32 {
    var g: i32[][] = [];
    var i: i32 = 0;
    while (i < 2) { g = g.append([4, 5]); i = i + 1; }
    var h: i32[][] = [[9, 9]];
    g = h;
    return g[0][0] + h.len();
}`,
			want: 10,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "arrarr_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"reclaim credit was granted to a shape that still holds a live "+
					"reference (use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}
