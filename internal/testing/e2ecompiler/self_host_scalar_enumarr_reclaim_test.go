package e2ecompiler

import (
	"testing"
)

// --- All-scalar-payload enum arrays free their element boxes (#7678) --------
//
// An enum whose every variant carries only scalars still boxes each element, so
// releasing one of its arrays must free every element box, not just the
// buffer. A buffer-only release strands one box per element — a constant leak
// per array, invisible to the exit code. The release is box-only per element,
// since there is no payload under it.
//
// Wants confirmed against bin/fern -interp; counts are the self-host's own.
// Exit 99 is reserved for __rc_underflow_count() — the row that catches an
// over-release here, since the census alone cannot.

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
function mkv(i: i32): Tag[] { let o: Tag[] = []; o = o.append(Tag.Box(i)); return o; }
function round(src: Tag[], i: i32): i32 {
    let t: i32 = 0;
    let e: Tag = src[0];
    match (e) {
        Box(v) => { t = (t + v) % 101; },
        Nil => { t = 9; }
    }
    return (t + i - i) % 101;
}
function main(): i32 {
    let keep: Tag[] = mkv(5);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = acc + keep.len();
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 3, allocs: 2, frees: 2,
		},
		{
			// The literal flavor — it never consulted the walk gate at all;
			// only the element-admission fallback flips it. `idt` keeps the
			// array a heap box around its static elements.
			name: "scalar_literal",
			src: `enum Tag { Box(i32), Nil }
@noinline function idt(t: Tag): Tag { return t; }
function round(src: Tag[], i: i32): i32 {
    let t: i32 = 0;
    let e: Tag = src[0];
    match (e) {
        Box(v) => { t = (t + v) % 101; },
        Nil => { t = 9; }
    }
    return (t + i - i) % 101;
}
function main(): i32 {
    let keep: Tag[] = [Tag.Box(5), idt(Tag.Nil)];
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + round(keep, i); i = i + 1; }
    acc = acc + keep.len();
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 4, allocs: 1, frees: 1,
		},
		{
			// Control: the rc-payload sibling, admitted by the pre-existing
			// path — must not move.
			name: "rcpayload_control",
			src: `enum E { A(i32[]), B }
function mkv(i: i32): E[] { let o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
function rd(src: E[], i: i32): i32 {
    let e: E = src[0];
    return (match (e) { E.A(xs) => xs.len(), E.B => 0 }) + i - i;
}
function main(): i32 {
    let keep: E[] = mkv(7);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { acc = acc + rd(keep, i); i = i + 1; }
    acc = acc + keep.len();
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 35, allocs: 3, frees: 3,
		},
	}
}

// TestSelfHostScalarEnumArrReclaimX86_64 pins the leak accounting.
func TestSelfHostScalarEnumArrReclaimX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
				t.Errorf("%s: %s — want frees=%d",
					tc.name, summary, tc.frees)
			}
		})
	}
}
