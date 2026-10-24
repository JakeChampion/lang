package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostOptStructPayloadDropX86_64(t *testing.T) {
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
			name: "single_bind_scalar_field_read",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { acc = p.n; }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 53,
		},
		{
			name: "single_bind_field_len",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { acc = p.xs.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 34,
		},
		{
			name: "single_bind_element_read",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { acc = p.xs[1]; }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 70,
		},
		{
			name: "single_bind_bigger_array_field",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i, i, i, i, i, i, i, i, i], n: i });
    match (o) { Some(p) => { acc = p.n; }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 53,
		},
		{
			name: "result_ok_struct_payload",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Result[P, string] = Ok(P { xs: [i, i + 1], n: i });
    match (o) { Ok(p) => { acc = p.n + p.xs.len(); }, Err(e) => { acc = e.len(); } }
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
			name: "string_field_beside_the_array_field",
			src: `struct P { xs: i32[], s: string }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], s: "hello" });
    match (o) { Some(p) => { acc = p.xs.len() + p.s.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 36,
		},
		{
			name: "guarded_arm",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) {
        Some(p) when p.n > 50 => { acc = p.n; },
        Some(p) => { acc = p.xs.len(); },
        None => {}
    }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 42,
		},
		{
			name: "scalar_only_struct_payload_stays_balanced",
			src: `struct P { n: i32, m: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { n: i, m: i });
    match (o) { Some(p) => { acc = p.n; }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 53,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "osp_"+tc.name, asm)
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
				t.Errorf("live_bytes=%d, want 0 — the payload struct's array field leaks "+
					"once per round, so this scales with the loop count", live)
			}
			if allocs != frees {
				t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
			}
		})
	}
}

func TestSelfHostOptStructPayloadDropHazardsX86_64(t *testing.T) {
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
			name: "field_extracted_to_an_outer_local",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let held: i32[] = [];
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { held = p.xs; acc = p.n; }, None => {} }
    return acc + held[0] + held.len();
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:      57,
			wantFrees: 300,
		},
		{
			name: "field_appended_into_a_container",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let keep: i32[][] = [];
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { keep = keep.append(p.xs); acc = p.n; }, None => {} }
    return acc + keep[0][1];
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
			name: "field_passed_to_a_callee_that_keeps_it",
			src: `struct P { xs: i32[], n: i32 }
@noinline function keepit(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    let held: i32[] = [];
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { held = keepit(p.xs); acc = p.n; }, None => {} }
    return acc + held[1];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:      40,
			wantFrees: 300,
		},
		{
			name: "whole_payload_struct_escapes",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let hp: P = P { xs: [0], n: i };
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { hp = p; acc = p.n; }, None => {} }
    return acc + hp.xs[1];
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
			name: "nested_block_match_field_extracted",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let held: i32[] = [];
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    if (i >= 0) {
        match (o) { Some(p) => { held = p.xs; acc = p.n; }, None => {} }
    }
    return acc + held[1];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want:      40,
			wantFrees: 300,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "osph_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d — a wrong answer or a crash here means the "+
					"deep drop freed a field the arm moved out (use-after-free)", exit, tc.want)
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
					"the moved field being released under a live reference; a lower one means "+
					"this probe stopped exercising the path it was written for",
					frees, tc.wantFrees, allocs, live)
			}
		})
	}
}

func TestSelfHostOptStructPayloadDropNoUnderflowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
	}{
		{
			name: "array_field",
			src: `struct P { xs: i32[], n: i32 }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], n: i });
    match (o) { Some(p) => { acc = p.n + p.xs.len() + p.xs[0]; }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}`,
		},
		{
			name: "string_literal_field",
			src: `struct P { xs: i32[], s: string }
function round(i: i32): i32 {
    let acc: i32 = 0;
    let o: Option[P] = Some(P { xs: [i, i + 1], s: "hello" });
    match (o) { Some(p) => { acc = p.xs.len() + p.s.len(); }, None => {} }
    return acc;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "ospu_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != 0 {
				t.Errorf("__rc_underflow_count() == %d, want 0 — the deep drop released a "+
					"payload field the construction did not retain", exit)
			}
		})
	}
}
