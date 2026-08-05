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
// fields — so the append path is admitted only for an element struct whose
// fields are all scalar or `string` (struct_all_scalar_or_string_fields). That
// restriction is not caution for its own sake: the first version of this change
// used the class's existing, fully loose field guard and broke self-compilation
// (#6129 — gen0/gen1 diverged on unit 2_s83), because that guard also lets map /
// option / tuple fields through and the compiler's own sources have ~112
// append-built struct arrays.
//
// `string` was folded back in afterwards, because the exclusion was costing a
// real leak for no safety: the LITERAL-built form of the same array has always
// reclaimed its element boxes under the fully loose rule, so refusing the append
// form made two ways of building one value disagree (28800 vs 9600 bytes over
// 100 rounds). It is sound because the element BOX is fresh by construction —
// structarr_elem_store_ok admits only a no-base struct literal — so the shallow
// free releases a box the array solely owns, and the string field leaks exactly
// as it already does on the literal path. map / option / tuple stay out.

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

// A string-fielded element struct: the element BOXES must be reclaimed, leaving
// only the string boxes live. Asserted against the LITERAL-built form of the
// same array rather than a byte count — the two ways of building one value have
// to agree, which is the property that was broken (28800 vs 9600 over 100
// rounds) and the one that survives any change in box sizes.
const structArrAppendStrFieldSrc = `struct N { s: string, n: i32 }

function round(i: i32): i32 {
    var xs: N[] = [];
    var k: i32 = 0;
    while (k < 4) { xs = xs.append(N { s: "ab", n: i }); k = k + 1; }
    return xs.len();
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    return t / 100;
}`

const structArrLiteralStrFieldSrc = `struct N { s: string, n: i32 }

function round(i: i32): i32 {
    var xs: N[] = [N { s: "ab", n: i }, N { s: "cd", n: i }, N { s: "ef", n: i }, N { s: "gh", n: i }];
    return xs.len();
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    return t / 100;
}`

// TestSelfHostStructArrAppendStrFieldReclaimX86_64 — an append-built struct
// array whose element carries a `string` field reclaims its element boxes, to
// the same live_bytes as the literal-built form of the identical array.
func TestSelfHostStructArrAppendStrFieldReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	liveOf := func(t *testing.T, name, src string) int64 {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != 4 {
			t.Fatalf("%s exited %d, want 4", name, exit)
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
		return live
	}

	appended := liveOf(t, "structarr_append_strfield", structArrAppendStrFieldSrc)
	literal := liveOf(t, "structarr_literal_strfield", structArrLiteralStrFieldSrc)
	if appended != literal {
		t.Errorf("append-built live_bytes=%d, literal-built live_bytes=%d — the two ways "+
			"of building the same array must reclaim alike; a larger append figure means "+
			"the element boxes are leaking again, and the gap scales with the iteration "+
			"count", appended, literal)
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
			// A string field is now ADMITTED (the element box is freed, the
			// string leaks). Reading a field back out of a reclaimed-class
			// array must still be correct — this is the shape that broke
			// self-compilation when the fully loose guard was used (#6129), so
			// it stays pinned behaviourally even though it now takes the credit.
			name: "string_field_admitted",
			src: `struct N { name: string, n: i32 }
function main(): i32 {
    var ns: N[] = [];
    var i: i32 = 0;
    while (i < 3) { ns = ns.append(N { name: "abc", n: i }); i = i + 1; }
    return ns[2].n + ns[0].name.len();
}`,
			want: 5,
		},
		{
			// A bound ELEMENT of a string-fielded array aliases a box the
			// reclaim would free — the element-escape gate must still refuse
			// the credit now that the field type no longer does.
			name: "string_field_element_alias",
			src: `struct N { name: string, n: i32 }
function main(): i32 {
    var ns: N[] = [];
    var i: i32 = 0;
    while (i < 3) { ns = ns.append(N { name: "wxyz", n: i }); i = i + 1; }
    var q: N = ns[1];
    return q.n + q.name.len() + ns.len();
}`,
			want: 8,
		},
		{
			// An IDENT element of a string-fielded array: the array does not
			// solely own the box, so freeing it would dangle `shared` — and
			// `shared.name` must survive the array's reclaim.
			name: "string_field_ident_element",
			src: `struct N { name: string, n: i32 }
function main(): i32 {
    var shared: N = N { name: "pq", n: 5 };
    var ns: N[] = [];
    var i: i32 = 0;
    while (i < 3) { ns = ns.append(shared); i = i + 1; }
    return ns[0].n + shared.name.len();
}`,
			want: 7,
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
