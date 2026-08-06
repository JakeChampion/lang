package e2eselfhost

import (
	"strings"
	"testing"
)

// --- Reaching a field THROUGH a field is a borrow (#6127) --------------------
//
// `optstruct_bind_esc_expr` (#6308) gates the deep drop of an Option's struct
// payload on no rc FIELD being moved out of the arm binding. It understood one
// level: `p.xs`, `p.xs[j]`, `p.xs.len()`. A chain — `p.inner.ys.len()`, where the
// payload's rc content sits one struct further down — fell through to the generic
// call tail, which reaches the bare-ident arm through `p.inner` and reports the
// non-scalar field `inner` extracted. So the deep drop was refused and the whole
// nested level leaked.
//
// That is the same distinction structfld_safe_operand draws for the whole-program
// scan, and the reason that scan could not reuse the string walker either (#6274):
// reaching `ys` through `inner` retains only ys's box, so `inner` is borrowed.
//
// Which block leaked, by growing one dimension at a time over 100 rounds:
//
//	base                          400 / 200   8000
//	ys 2 -> 10 elements           400 / 200  14400   <- +64/round, the array
//	Inner 1 -> 5 fields           400 / 200  11200   <- +32/round, the Inner box
//	P 2 -> 6 fields               400 / 200   8000   <- unchanged
//
// so both the Inner box and its array leaked while the P box and the option box
// were freed. The other half of the diagnosis is that the SAME struct bound as a
// bare local reclaims fully (300/300) and emits both `__struct_drop_P` and
// `__struct_drop_Inner` — the drop helpers were there all along, and the option
// payload path was the only caller that never asked for them.

func TestSelfHostOptStructBorrowChainX86_64(t *testing.T) {
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
			// The minimal chain: `.len()` two levels down.
			name: "nested_field_len",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1] }, n: i });
    match (o) { Some(p) => { acc = p.n + p.inner.ys.len(); }, None => {} }
    return acc;
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
			// An indexed read two levels down — the other borrow shape.
			name: "nested_field_indexed",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1] }, n: i });
    match (o) { Some(p) => { acc = p.inner.ys[1] + p.n; }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 40,
		},
		{
			// A 10-element nested array — the dimension that identified the array as
			// one of the two leaked blocks.
			name: "nested_field_bigger_array",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { inner: Inner { ys: [i, i, i, i, i, i, i, i, i, i] }, n: i });
    match (o) { Some(p) => { acc = p.n + p.inner.ys.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 57,
		},
		{
			// A wider Inner — the dimension that identified the Inner BOX as the
			// other leaked block, distinct from its array.
			name: "nested_field_wider_inner",
			src: `struct Inner { ys: i32[], a: i32, b: i32, c: i32, d: i32 }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1], a: 1, b: 2, c: 3, d: 4 }, n: i });
    match (o) { Some(p) => { acc = p.n + p.inner.ys.len(); }, None => {} }
    return acc;
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
			// A string field beside the nested array, read through the same chain —
			// __struct_drop_Inner's k_str arm, reached two levels down.
			name: "nested_string_field",
			src: `struct Inner { ys: i32[], tag: string }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1], tag: "hello" }, n: i });
    match (o) { Some(p) => { acc = p.n + p.inner.ys.len() + p.inner.tag.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 6,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "osbc_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d", exit, tc.want)
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
			if live != 0 {
				t.Errorf("live_bytes=%d, want 0 — the nested struct box AND its array leak "+
					"once per round, so this scales with the loop count", live)
			}
			if allocs != frees {
				t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
			}
		})
	}
}

// TestSelfHostOptStructBorrowChainHazardsX86_64 — a chain READ is a borrow; a
// chain whose leaf is EXTRACTED is not, at any depth. Free counts are exact and
// pinned at the values measured before the change: these shapes leak by design,
// so a correct fix leaves the count alone, and an over-release shows up as a
// count that grew even where the program still exits correctly.
//
// Every `want` is from `fern -interp`.
func TestSelfHostOptStructBorrowChainHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name      string
		src       string
		want      int
		wantFrees int64
	}{
		{
			// The INTERMEDIATE struct extracted whole — a borrow only while it is
			// read through, and this reads it out.
			name: "intermediate_struct_extracted",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    var held: Inner = Inner { ys: [0] };
    var acc: i32 = 0;
    var o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1] }, n: i });
    match (o) { Some(p) => { held = p.inner; acc = p.n; }, None => {} }
    return acc + held.ys[1];
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:      40,
			wantFrees: 200,
		},
		{
			// The LEAF array extracted through the chain.
			name: "leaf_array_extracted",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    var held: i32[] = [];
    var acc: i32 = 0;
    var o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1] }, n: i });
    match (o) { Some(p) => { held = p.inner.ys; acc = p.n; }, None => {} }
    return acc + held[1];
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:      40,
			wantFrees: 300,
		},
		{
			// The leaf moved into a container that outlives the match.
			name: "leaf_array_into_a_container",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    var keep: i32[][] = [];
    var acc: i32 = 0;
    var o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1] }, n: i });
    match (o) { Some(p) => { keep = keep.append(p.inner.ys); acc = p.n; }, None => {} }
    return acc + keep[0][1];
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:      40,
			wantFrees: 400,
		},
		{
			// The leaf passed to a callee that keeps it.
			name: "leaf_array_to_a_callee_that_keeps_it",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
function keepit(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    var held: i32[] = [];
    var acc: i32 = 0;
    var o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1] }, n: i });
    match (o) { Some(p) => { held = keepit(p.inner.ys); acc = p.n; }, None => {} }
    return acc + held[1];
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:      40,
			wantFrees: 300,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "osbch_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d — a wrong answer or a crash here means the "+
					"chain borrow laundered an extraction and the deep drop freed a value "+
					"something else still holds", exit, tc.want)
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
			if frees != tc.wantFrees {
				t.Errorf("frees=%d, want exactly %d (allocs=%d live=%d) — a HIGHER count is "+
					"an extracted value released under a live reference; a lower one means "+
					"this probe stopped exercising the path it was written for",
					frees, tc.wantFrees, allocs, live)
			}
		})
	}
}

// TestSelfHostOptStructBorrowChainNoUnderflowX86_64 — the deep drop now recurses
// a level further, so it releases a nested box and its array and string. A
// string LITERAL is the sharp case: its data lives in .rodata and the heap guard
// skips it, so a double release registers only in this counter.
func TestSelfHostOptStructBorrowChainNoUnderflowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	src := `struct Inner { ys: i32[], tag: string }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    var acc: i32 = 0;
    var o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1], tag: "hello" }, n: i });
    match (o) { Some(p) => { acc = p.n + p.inner.ys.len() + p.inner.ys[0] + p.inner.tag.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}`
	asm := hevCompile(t, runner, driverBin, src, nil)
	progBin := buildBin(t, gcc, dir, "osbcu", asm)
	_, exit := hevRun(t, runner, progBin)
	if exit != 0 {
		t.Errorf("__rc_underflow_count() == %d, want 0 — a box released twice", exit)
	}
}
