package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostNestedMatchBorrowX86_64(t *testing.T) {
	boxedProbes(t)
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
			name: "match_at_top_level",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    match (o) { Some(a) => { acc = a.len() + a[0]; }, None => {} }
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
			name: "match_inside_an_if",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { acc = a.len() + a[0]; }, None => {} }
    }
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
			name: "match_inside_a_while",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    let k: i32 = 0;
    while (k < 1) {
        match (o) { Some(a) => { acc = a.len() + a[0]; }, None => {} }
        k = k + 1;
    }
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
			name: "string_payload_inside_an_if",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[string] = Some("hello");
    if (i >= 0) {
        match (o) { Some(s) => { acc = s.len(); }, None => {} }
    }
    return acc + i;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 55,
		},
		{
			name: "struct_payload_inside_an_if",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    if (i >= 0) {
        match (o) { Some(p) => { acc = p.n + p.xs.len(); }, None => {} }
    }
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "nmb_"+tc.name, asm)
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
				t.Errorf("live_bytes=%d, want 0 — the whole option box and its payload leak "+
					"once per round, so this scales with the loop count", live)
			}
			if allocs != frees {
				t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
			}
		})
	}
}

func TestSelfHostNestedMatchBorrowHazardsX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name       string
		src        string
		want       int
		wantAllocs int64
	}{
		{
			name: "aliased_beside_the_match",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    let keep: Option[i32[]] = None;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        keep = o;
        match (o) { Some(a) => { acc = a[0]; }, None => {} }
    }
    match (keep) { Some(b) => { acc = acc + b[1]; }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:       40,
			wantAllocs: 200,
		},
		{
			name: "aliased_after_the_block",
			src: `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { acc = a[0]; }, None => {} }
    }
    let keep: Option[i32[]] = o;
    match (keep) { Some(b) => { acc = acc + b[1]; }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:       40,
			wantAllocs: 200,
		},
		{
			name: "returned_to_the_caller",
			src: `function build(i: i32): Option[i32[]] {
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { if (a[0] < 0) { return None; } }, None => {} }
    }
    return o;
}
function round(i: i32): i32 {
    let g: Option[i32[]] = build(i);
    match (g) { Some(a) => { return a[0] + a[1]; }, None => {} }
    return 0;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:       40,
			wantAllocs: 200,
		},
		{
			name: "passed_to_a_callee_that_keeps_it",
			src: `@noinline function keepit(o: Option[i32[]]): Option[i32[]] { return o; }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let held: Option[i32[]] = None;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        held = keepit(o);
        match (o) { Some(a) => { acc = a[0]; }, None => {} }
    }
    match (held) { Some(b) => { acc = acc + b[1]; }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:       40,
			wantAllocs: 200,
		},
		{
			name: "arm_binding_escapes_to_an_outer_local",
			src: `function round(i: i32): i32 {
    let held: i32[] = [];
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { held = a; acc = a[0]; }, None => {} }
    }
    return acc + held[1];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:       40,
			wantAllocs: 200,
		},
		{
			name: "arm_binding_escapes_into_a_container",
			src: `function round(i: i32): i32 {
    let keep: i32[][] = [];
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { keep = keep.append(a); acc = a[0]; }, None => {} }
    }
    return acc + keep[0][1];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:       40,
			wantAllocs: 300,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "nmbh_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d — a wrong answer or a crash here means the "+
					"scrutinee borrow laundered a genuine escape and the drop freed a box "+
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
			if allocs != tc.wantAllocs || frees != tc.wantAllocs || live != 0 {
				t.Errorf("allocation census: allocs=%d frees=%d live=%d; want allocs=frees=%d and live=0",
					allocs, frees, live, tc.wantAllocs)
			}
		})
	}
}

func TestSelfHostNestedMatchBorrowNoUnderflowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	src := `function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[i32[]] = Some([i, i + 1]);
    if (i >= 0) {
        match (o) { Some(a) => { acc = a.len() + a[0]; }, None => {} }
    }
    let s: Option[string] = Some("hello");
    let k: i32 = 0;
    while (k < 1) {
        match (s) { Some(t) => { acc = acc + t.len(); }, None => {} }
        k = k + 1;
    }
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
	progBin := buildBin(t, gcc, dir, "nmbu", asm)
	_, exit := hevRun(t, runner, progBin)
	if exit != 0 {
		t.Errorf("__rc_underflow_count() == %d, want 0 — a box released twice", exit)
	}
}
