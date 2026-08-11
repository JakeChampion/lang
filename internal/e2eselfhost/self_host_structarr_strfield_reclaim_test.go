package e2eselfhost

import (
	"strings"
	"testing"
)

// --- String fields of struct-array ELEMENTS (#6127 struct_string) ------------
//
// A struct-array local whose element struct has a string field landed on the
// SHALLOW structarr path: __fern_arrarr_free decs each element box and the outer
// buffer, and never looks inside the box, so every element's string leaked. A
// BARE local of the same struct already reclaimed its string correctly, through
// __field_reclaim_<T>'s STRFLDOK arm — it was only the ELEMENT walk that never
// reached that machinery.
//
//	bare local            allocs=200  frees=200  live_bytes=0     <- already fine
//	array, literal-built  allocs=500  frees=300  live_bytes=4800  = 2 elems x 24
//	array, append-built   allocs=1000 frees=600  live_bytes=9600  = 4 elems x 24
//	scalar-field elements allocs=600  frees=600  live_bytes=0     <- already fine
//
// The routing, not the machinery, was missing. slot_is_reclaimable_structarr
// already excludes rc-array-field structs, so a structarr slot whose element type
// ROUTES field reclaim can only be a STRFLDOK-admitted string / string[] / fn
// -fielded one — and those take the same counted element walk ARRSTRUCT uses
// (per element __struct_drop_<T>, then the box, then the outer buffer).
//
// Crucially the admission gate is the EXISTING whole-program read scan
// (strfld_reclaim_ok_types_of, via struct_routes_field_reclaim), not a new looser
// check. That scan carries hard-won history: the per-module compiler self-run
// segfaulted on exactly this class, because the self-host has no read-side
// alias-inc for strings, so a field read that escapes is an uncounted alias the
// free would dangle. Reusing it is what makes this sound; hand-rolling a
// predicate to make a probe pass is what #6148 did and had to revert.

const structArrStrFieldSrc = `struct N { name: string, v: i32 }

function round(): i32 {
    var xs: N[] = [];
    var i: i32 = 0;
    while (i < 4) { xs = xs.append(N { name: "hello", v: i }); i = i + 1; }
    return xs.len();
}

function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 7;
}`

// TestSelfHostStructArrStrFieldReclaimX86_64 — the element string fields are
// freed. allocs == frees is load-bearing: frees short of allocs is the leak this
// closes; frees ABOVE allocs would mean the element walk's __struct_drop_<T> and
// something else both claimed one string (a double free), which for a string is a
// freelist corruption rather than a clean crash.
func TestSelfHostStructArrStrFieldReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, structArrStrFieldSrc, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "structarr_strfield", asm)
	stderr, exit := hevRun(t, runner, progBin)
	// 400 % 7 = 1, confirmed against both oracles (bin/fern -interp and native
	// -target x86-64-linux), not read off the self-host run this test exists to check.
	if exit != 1 {
		t.Fatalf("exited %d, want 1", exit)
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
		t.Errorf("allocs=%d frees=%d — each element's string field must be freed by the "+
			"element walk; frees > allocs means it was freed twice", allocs, frees)
	}
	if live != 0 {
		t.Errorf("live_bytes=%d, want 0 — one 24-byte string box per element per round, "+
			"so this scales with both the loop count and the element count", live)
	}
}

// TestSelfHostStructArrStrFieldHazardsX86_64 — the shapes the deep element walk
// must still REFUSE. A wrongly-granted drop frees a string something else still
// reads, so the failure is a wrong answer or a crash, not a leak. Every `want` was
// confirmed against both the interpreter and the native x86-64 backend.
//
// All of these currently measure as still leaking, i.e. genuinely refused rather
// than incidentally passing — checked, not assumed.
func TestSelfHostStructArrStrFieldHazardsX86_64(t *testing.T) {
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
			// The element's string field is moved into a container outliving the
			// array. Freeing it with the element would dangle the container's copy.
			// This is the read the whole-program scan exists to catch.
			name: "field_extracted_to_container",
			src: `struct N { name: string, v: i32 }
function round(i: i32): i32 {
    var keep: string[] = [];
    var xs: N[] = [N { name: "hello_world_long", v: i }, N { name: "second_string_val", v: i }];
    keep = keep.append(xs[0].name);
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < keep.len()) { t = t + keep[k].len(); k = k + 1; }
    return t;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 48,
		},
		{
			// The field value is a PARAM string the caller still owns and reads
			// afterwards — the element box does not sole-own it.
			name: "field_is_param_string",
			src: `struct N { name: string, v: i32 }
function build(p: string, n: i32): i32 {
    var xs: N[] = [N { name: p, v: n }, N { name: p, v: n }];
    return xs.len();
}
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { var owned: string = "hello" + "_suffix"; t = t + build(owned, r) + owned.len(); r = r + 1; }
    return t % 97;
}`,
			want: 42,
		},
		{
			// A bound element (`var q = xs[0]`) holds a box the free would dangle —
			// structarr_elem_escapes refuses the whole array.
			name: "element_bound_to_local",
			src: `struct N { name: string, v: i32 }
function round(i: i32): i32 {
    var xs: N[] = [N { name: "hello_world_long", v: i }, N { name: "second_string_val", v: i }];
    var q: N = xs[0];
    return q.v + q.name.len();
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 51,
		},
		{
			// The array is built and returned by a callee: the value is moved out,
			// so the callee must not walk it.
			name: "array_returned_from_callee",
			src: `struct N { name: string, v: i32 }
function mk(i: i32): N[] { return [N { name: "hello_world_long", v: i }]; }
function round(i: i32): i32 { var xs: N[] = mk(i); return xs[0].v + xs[0].name.len(); }
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 51,
		},
		{
			// Every element's string is read (compared and measured) before the
			// array dies. Pins that whatever the scan decides, the reads still see
			// the original bytes.
			name: "fields_read_before_death",
			src: `struct N { name: string, v: i32 }
function round(i: i32): i32 {
    var xs: N[] = [];
    var k: i32 = 0;
    while (k < 4) { xs = xs.append(N { name: "hello_world_long", v: i }); k = k + 1; }
    var t: i32 = 0;
    var j: i32 = 0;
    while (j < xs.len()) { if (xs[j].name != "hello_world_long") { return 1; } t = t + xs[j].name.len(); j = j + 1; }
    return t;
}
function main(): i32 { var t: i32 = 0; var r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 95,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "structarr_strfield_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash means the element "+
					"walk's string drop was granted to a shape whose string is still read "+
					"elsewhere (use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}
