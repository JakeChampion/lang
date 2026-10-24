package e2ecompiler

import (
	"strings"
	"testing"
)

const rcTupleRebindSrc = `function round(): i32 {
    let p: (i32, string) = (0, "hello");
    let i: i32 = 0;
    while (i < 4) { p = (i, "hello"); i = i + 1; }
    return p.0;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 7;
}`

func TestSelfHostRcTupleRebindReclaimX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, rcTupleRebindSrc, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "rctuple_rebind", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 6 {
		t.Fatalf("exited %d, want 6", exit)
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
		t.Errorf("allocs=%d frees=%d — a rebound rc-tuple must deep-drop each chain it "+
			"supersedes and the sweep must release the last one; frees > allocs means "+
			"both claimed one chain (double free)", allocs, frees)
	}
	if live != 0 {
		t.Errorf("live_bytes=%d, want 0 — a rebound rc-tuple leaks its box AND its rc "+
			"child per assignment, so this scales with the loop count", live)
	}
}

func TestSelfHostRcTupleRebindHazardsX86_64(t *testing.T) {
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
			name: "rc_element_extracted_to_local",
			src: `function round(i: i32): i32 {
    let p: (i32, i32[]) = (i, [i, i + 1]);
    let keep: i32[] = p.1;
    p = (i + 1, [i + 2, i + 3]);
    return keep[0] + keep[1] + p.1[0];
}
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 18,
		},
		{
			name: "nonfresh_rebind_disqualifies",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let p: (i32, i32[]) = (i, [i, i + 2]);
    p = (i, xs);
    return p.1[0] + xs[1];
}
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 9,
		},
		{
			name: "escapes_to_container",
			src: `function round(i: i32): i32 {
    let keep: (i32, i32[])[] = [];
    let p: (i32, i32[]) = (0, [0, 0]);
    let k: i32 = 0;
    while (k < 3) { p = (k, [k, k + 1]); keep = keep.append(p); k = k + 1; }
    let t: i32 = 0;
    let j: i32 = 0;
    while (j < keep.len()) { t = t + keep[j].0 + keep[j].1[1]; j = j + 1; }
    return t;
}
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(r); r = r + 1; } return t % 97; }`,
			want: 27,
		},
		{
			name: "returned_to_caller",
			src: `function mk(n: i32): (i32, i32[]) {
    let p: (i32, i32[]) = (0, [0, 0]);
    let k: i32 = 0;
    while (k < 3) { p = (k, [k, n]); k = k + 1; }
    return p;
}
function round(n: i32): i32 { let t: (i32, i32[]) = mk(n); return t.0 + t.1[1]; }
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(r % 3); r = r + 1; } return t % 97; }`,
			want: 8,
		},
		{
			name: "string_element_read_after_rebind",
			src: `function round(): i32 {
    let p: (i32, string) = (0, "hello_there_world");
    let i: i32 = 0;
    while (i < 4) { p = (i, "hello_there_world"); i = i + 1; }
    if (p.1 != "hello_there_world") { return 1; }
    return p.0 + p.1.len();
}
function main(): i32 { let t: i32 = 0; let r: i32 = 0; while (r < 100) { t = t + round(); r = r + 1; } return t % 97; }`,
			want: 60,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "rctuple_rebind_hazard_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d — a wrong answer or a crash means the DEEP drop "+
					"was granted to a shape that still holds a live reference to the payload "+
					"(use-after-free), not merely that it leaked", exit, tc.want)
			}
			allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
			if live != 0 || allocs != frees {
				t.Errorf("allocs=%d frees=%d live_bytes=%d, want a balanced census", allocs, frees, live)
			}
		})
	}
}
