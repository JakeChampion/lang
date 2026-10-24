package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostOptStructBorrowChainX86_64(t *testing.T) {
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
			name: "nested_field_len",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1] }, n: i });
    match (o) { Some(p) => { acc = p.n + p.inner.ys.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 4,
		},
		{
			name: "nested_field_indexed",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1] }, n: i });
    match (o) { Some(p) => { acc = p.inner.ys[1] + p.n; }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 40,
		},
		{
			name: "nested_field_bigger_array",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { inner: Inner { ys: [i, i, i, i, i, i, i, i, i, i] }, n: i });
    match (o) { Some(p) => { acc = p.n + p.inner.ys.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 57,
		},
		{
			name: "nested_field_wider_inner",
			src: `struct Inner { ys: i32[], a: i32, b: i32, c: i32, d: i32 }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1], a: 1, b: 2, c: 3, d: 4 }, n: i });
    match (o) { Some(p) => { acc = p.n + p.inner.ys.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 4,
		},
		{
			name: "nested_string_field",
			src: `struct Inner { ys: i32[], tag: string }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1], tag: "hello" }, n: i });
    match (o) { Some(p) => { acc = p.n + p.inner.ys.len() + p.inner.tag.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
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

func TestSelfHostOptStructBorrowChainHazardsX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name      string
		src       string
		want      int
		wantFrees int64
	}{
		{
			name: "intermediate_struct_extracted",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
@noinline function id(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    let held: Inner = Inner { ys: id([0]) };
    let acc: i32 = 0;
    let o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1] }, n: i });
    match (o) { Some(p) => { held = p.inner; acc = p.n; }, None => {} }
    return acc + held.ys[1];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:      40,
			wantFrees: 500,
		},
		{
			name: "leaf_array_extracted",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    let held: i32[] = [];
    let acc: i32 = 0;
    let o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1] }, n: i });
    match (o) { Some(p) => { held = p.inner.ys; acc = p.n; }, None => {} }
    return acc + held[1];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:      40,
			wantFrees: 400,
		},
		{
			name: "leaf_array_into_a_container",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    let keep: i32[][] = [];
    let acc: i32 = 0;
    let o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1] }, n: i });
    match (o) { Some(p) => { keep = keep.append(p.inner.ys); acc = p.n; }, None => {} }
    return acc + keep[0][1];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:      40,
			wantFrees: 500,
		},
		{
			name: "leaf_array_to_a_callee_that_keeps_it",
			src: `struct Inner { ys: i32[] }
struct P { inner: Inner, n: i32 }
@noinline function keepit(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    let held: i32[] = [];
    let acc: i32 = 0;
    let o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1] }, n: i });
    match (o) { Some(p) => { held = keepit(p.inner.ys); acc = p.n; }, None => {} }
    return acc + held[1];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:      40,
			wantFrees: 400,
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

func TestSelfHostOptStructBorrowChainNoUnderflowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	src := `struct Inner { ys: i32[], tag: string }
struct P { inner: Inner, n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { inner: Inner { ys: [i, i + 1], tag: "hello" }, n: i });
    match (o) { Some(p) => { acc = p.n + p.inner.ys.len() + p.inner.ys[0] + p.inner.tag.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
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
