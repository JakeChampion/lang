package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Append-built struct-array element reclaim (#6127) -------------
//
// irlower's "STRUCTARR:" credit routes a fresh, non-escaping scalar-field
// struct array to __fern_arrarr_free, which frees each element STRUCT BOX and
// then the outer buffer. Like the arr-of-arr class before #6092, that credit
// was refused for any REASSIGNED name — and `ps = ps.append(P { .. })` is a
// reassignment — so an append-built `P[]` fell out of the credit and leaked one
// element box per append.
//
// Found by the FERN_LEAKCHECK differential (#6091) against native, which is
// also why these tests assert AGREEMENT with native rather than a byte count.
//
// Scope note: STRUCTARR is a SHALLOW class — it frees element boxes, not their
// fields — so the append path is admitted ONLY for an element struct whose
// fields are all scalar (struct_all_scalar_fields), where the shallow free is
// exact. That restriction is not caution for its own sake: the first version of
// this change used the class's existing, looser field guard and broke
// self-compilation (#6129 — gen0/gen1 diverged on unit 2_s83), because that
// guard lets string / map / option / tuple fields through and the compiler's own
// sources have ~112 append-built struct arrays. So the probe below uses a
// scalar-field struct, which is exactly the set the fix covers.

const structArrAppendChurnSrc = `struct P { x: i32, y: i32 }

function round(): i32 {
    var ps: P[] = [];
    var i: i32 = 0;
    while (i < 8) { ps = ps.append(P { x: i, y: i }); i = i + 1; }
    return ps.len();
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t / 100;
}`

// TestSelfHostStructArrAppendReclaimX86_64 — an append-built struct array
// reclaims its element boxes.
func TestSelfHostStructArrAppendReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, structArrAppendChurnSrc, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "structarr_append", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 8 {
		t.Fatalf("program exited %d, want 8", exit)
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
		t.Errorf("%s: live_bytes=%d, want 0 — an append-built struct array must reclaim "+
			"its element boxes; the leak scales with the iteration count, so any "+
			"nonzero here is unbounded in a loop", summary, live)
	}
}

// TestSelfHostStructArrAppendHazardsX86_64 — the shapes the credit must still
// REFUSE. A wrongly-granted credit frees an element box something else still
// points at, so these are asserted through behaviour: the failure mode is a
// wrong answer or a crash, not a leak.
func TestSelfHostStructArrAppendHazardsX86_64(t *testing.T) {
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
			// A bound ELEMENT aliases a box the reclaim would free.
			name: "element_alias",
			src: `struct P { x: i32, y: i32 }
function main(): i32 {
    var ps: P[] = [];
    var i: i32 = 0;
    while (i < 3) { ps = ps.append(P { x: i, y: i }); i = i + 1; }
    var q: P = ps[1];
    return q.x + ps.len();
}`,
			want: 4,
		},
		{
			// The appended element is an IDENT, so the array does not solely
			// own the box; freeing it would dangle `shared`.
			name: "ident_element",
			src: `struct P { x: i32, y: i32 }
function main(): i32 {
    var shared: P = P { x: 5, y: 6 };
    var ps: P[] = [];
    var i: i32 = 0;
    while (i < 3) { ps = ps.append(shared); i = i + 1; }
    return ps[0].x + shared.y;
}`,
			want: 11,
		},
		{
			// A `...base` copy shares the base's field pointers, so the element
			// is not a fresh sole owner.
			name: "base_copy_element",
			src: `struct P { x: i32, y: i32 }
function main(): i32 {
    var base: P = P { x: 2, y: 3 };
    var ps: P[] = [];
    var i: i32 = 0;
    while (i < 3) { ps = ps.append(P { ...base, x: i }); i = i + 1; }
    return ps[2].x + base.y;
}`,
			want: 5,
		},
		{
			// A non-append reassignment must still sink the credit outright.
			name: "rebound_to_other",
			src: `struct P { x: i32, y: i32 }
function main(): i32 {
    var ps: P[] = [];
    var i: i32 = 0;
    while (i < 2) { ps = ps.append(P { x: 4, y: 4 }); i = i + 1; }
    var qs: P[] = [P { x: 9, y: 9 }];
    ps = qs;
    return ps[0].x + qs.len();
}`,
			want: 10,
		},
		{
			// The array ESCAPES by return, so the callee must not free it.
			name: "escaping_return",
			src: `struct P { x: i32, y: i32 }
function build(): P[] {
    var ps: P[] = [];
    var i: i32 = 0;
    while (i < 3) { ps = ps.append(P { x: i, y: i }); i = i + 1; }
    return ps;
}
function main(): i32 { var r: P[] = build(); return r[2].x + r.len(); }`,
			want: 5,
		},
		{
			// A NON-scalar field (string) puts the element struct outside the
			// class's exactness guarantee, so the append path must not admit
			// it. Behaviourally this must still be correct — it simply leaks
			// the field rather than being shallow-freed, which is what broke
			// self-compilation when the looser guard was used (#6129).
			name: "string_field_excluded",
			src: `struct N { name: string, n: i32 }
function main(): i32 {
    var ns: N[] = [];
    var i: i32 = 0;
    while (i < 3) { ns = ns.append(N { name: "abc", n: i }); i = i + 1; }
    return ns[2].n + ns[0].name.len();
}`,
			want: 5,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "structarr_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"reclaim credit was granted to a shape that still holds a live "+
					"reference (use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}
