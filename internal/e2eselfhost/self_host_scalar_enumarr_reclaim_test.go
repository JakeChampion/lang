package e2eselfhost

import (
	"testing"
)

// --- All-scalar-payload enum arrays free their element boxes (#7678) --------
//
// An enum whose every variant carries only scalars got NO element release for
// its arrays: the credit path's element admissions ran only through
// fresh_rcpayload_enum_init (whose rc-droppable set is deliberately disjoint
// from the all-scalar set the match-consume machinery owns), and the
// append/producer flavors additionally gated on enum_arr_elems_walk_ok, whose
// any_arr clause protects a payload dec an all-scalar enum does not have. So
// the exit sweep's bare buffer dec freed the buffer and stranded every
// element box — a constant leak per array, invisible to exits (native and
// self-host agreed throughout).
//
// The fix admits a fresh all-scalar ctor beside the rc-payload one at both
// element admissions, and routes the two credit-side walk gates through
// enum_arr_release_walk_ok — a wrapper, so enum_arr_elems_walk_ok keeps its
// meaning and the field-walk emitters emit no empty-dispatch helper. The
// release then degenerates per element to the box-only free
// emit_enum_variant_drops already performs for a payload-less variant.
//
// Wants confirmed against bin/fern -interp and the native x86-64 backend;
// counts are the self-host's own (native const-folds differently). Exit 99 is
// reserved for __rc_underflow_count() — the row that catches an over-widening
// here, since the census alone cannot.

type scalarEnumArrCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func scalarEnumArrCases() []scalarEnumArrCase {
	return []scalarEnumArrCase{
		{
			// The producer flavor: keep built by an append-built producer call.
			// Was 3/2 with the element box stranded.
			name: "scalar_producer",
			src: `enum Tag { Box(i32), Nil }
function mkv(i: i32): Tag[] { var o: Tag[] = []; o = o.append(Tag.Box(i)); return o; }
function round(src: Tag[], i: i32): i32 {
    var t: i32 = 0;
    var e: Tag = src[0];
    match (e) {
        Box(v) => { t = (t + v) % 101; },
        Nil => { t = 9; }
    }
    return (t + i - i) % 101;
}
function main(): i32 {
    var keep: Tag[] = mkv(5);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = acc + keep.len();
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 3, allocs: 3, frees: 3,
		},
		{
			// The literal flavor — it never consulted the walk gate at all;
			// only the element-admission fallback flips it. Was 3/2.
			name: "scalar_literal",
			src: `enum Tag { Box(i32), Nil }
function round(src: Tag[], i: i32): i32 {
    var t: i32 = 0;
    var e: Tag = src[0];
    match (e) {
        Box(v) => { t = (t + v) % 101; },
        Nil => { t = 9; }
    }
    return (t + i - i) % 101;
}
function main(): i32 {
    var keep: Tag[] = [Tag.Box(5), Tag.Nil];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = acc + keep.len();
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 4, allocs: 3, frees: 3,
		},
		{
			// Control: the rc-payload sibling, admitted by the pre-existing
			// path — must not move.
			name: "rcpayload_control",
			src: `enum E { A(i32[]), B }
function mkv(i: i32): E[] { var o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
function rd(src: E[], i: i32): i32 {
    var e: E = src[0];
    return (match (e) { E.A(xs) => xs.len(), E.B => 0 }) + i - i;
}
function main(): i32 {
    var keep: E[] = mkv(7);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) { acc = acc + rd(keep, i); i = i + 1; }
    acc = acc + keep.len();
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 35, allocs: 4, frees: 4,
		},
	}
}

// TestSelfHostScalarEnumArrReclaimX86_64 pins the leak accounting.
func TestSelfHostScalarEnumArrReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range scalarEnumArrCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "sea_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the box-only "+
					"element free ran against a box something still owned)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. FEWER means the scalar-enum "+
					"element release stopped being credited and the boxes strand again",
					tc.name, summary, tc.frees)
			}
		})
	}
}
