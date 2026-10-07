package e2ecompiler

import (
	"strings"
	"testing"
)

// --- String fields of struct-array ELEMENTS (#6127) --------------------------
//
// A struct-array local whose element struct has a string field must free each
// element's string when the array dies, not only each element box and the
// outer buffer. A shallow element walk leaks every element's string:
//
//	bare local            allocs=200  frees=200  live_bytes=0     <- balanced
//	array, literal-built  allocs=500  frees=300  live_bytes=4800  = 2 elems x 24
//	array, append-built   allocs=1000 frees=600  live_bytes=9600  = 4 elems x 24
//	scalar-field elements allocs=600  frees=600  live_bytes=0     <- balanced
//
// The other half is soundness: an element string still referenced elsewhere
// when the array dies (moved into a container, or a parameter the caller
// still owns) must not be freed with the element. An over-freed string
// corrupts the freelist rather than crashing cleanly.

const structArrStrFieldSrc = `struct N { name: string, v: i32 }

function round(): i32 {
    let xs: N[] = [];
    let i: i32 = 0;
    while (i < 4) { xs = xs.append(N { name: "hello", v: i }); i = i + 1; }
    return xs.len();
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 7;
}`

// TestSelfHostStructArrStrFieldReclaimX86_64 — the element string fields are
// freed. allocs == frees is essential: frees short of allocs is the leak this
// closes; frees ABOVE allocs would mean one string was released twice, which
// for a string is a freelist corruption rather than a clean crash.
func TestSelfHostStructArrStrFieldReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

// TestSelfHostStructArrStrFieldHazardsX86_64 — shapes whose element strings are
// still referenced elsewhere when the array dies. A wrongly-granted drop frees a
// string something else still reads, so the first failure is a wrong answer or a
// crash; the census must balance as well. Every `want` was confirmed against both
// the interpreter and the native x86-64 backend.
func TestSelfHostStructArrStrFieldHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
    let keep: string[] = [];
    let xs: N[] = [N { name: "hello_world_long", v: i }, N { name: "second_string_val", v: i }];
    keep = keep.append(xs[0].name);
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < keep.len()) { t = t + keep[k].len(); k = k + 1; }
    return t;
}
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 48,
		},
		{
			// The field value is a PARAM string the caller still owns and reads
			// afterwards — the element box does not sole-own it.
			name: "field_is_param_string",
			src: `struct N { name: string, v: i32 }
function build(p: string, n: i32): i32 {
    let xs: N[] = [N { name: p, v: n }, N { name: p, v: n }];
    return xs.len();
}
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { let owned: string = "hello" + "_suffix"; t = t + build(owned, r) + owned.len(); r = r + 1; }
    return t % 97;
}`,
			want: 42,
		},
		{
			// A bound element (`let q = xs[0]`) holds a box the array's release
			// must not free while `q` still reads it.
			name: "element_bound_to_local",
			src: `struct N { name: string, v: i32 }
function round(i: i32): i32 {
    let xs: N[] = [N { name: "hello_world_long", v: i }, N { name: "second_string_val", v: i }];
    let q: N = xs[0];
    return q.v + q.name.len();
}
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 51,
		},
		{
			// The array is built and returned by a callee: the value is moved out,
			// so the callee must not walk it.
			name: "array_returned_from_callee",
			src: `struct N { name: string, v: i32 }
function mk(i: i32): N[] { return [N { name: "hello_world_long", v: i }]; }
function round(i: i32): i32 { let xs: N[] = mk(i); return xs[0].v + xs[0].name.len(); }
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 51,
		},
		{
			// Every element's string is read (compared and measured) before the
			// array dies. Pins that whatever the scan decides, the reads still see
			// the original bytes.
			name: "fields_read_before_death",
			src: `struct N { name: string, v: i32 }
function round(i: i32): i32 {
    let xs: N[] = [];
    let k: i32 = 0;
    while (k < 4) { xs = xs.append(N { name: "hello_world_long", v: i }); k = k + 1; }
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < xs.len()) { if (xs[j].name != "hello_world_long") { return 1; } t = t + xs[j].name.len(); j = j + 1; }
    return t;
}
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 95,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "structarr_strfield_hazard_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d — a wrong answer or a crash means the element "+
					"walk's string drop was granted to a shape whose string is still read "+
					"elsewhere (use-after-free), not merely that it leaked", exit, tc.want)
			}
			allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
			if live != 0 || allocs != frees {
				t.Errorf("allocs=%d frees=%d live_bytes=%d, want a balanced census", allocs, frees, live)
			}
		})
	}
}
