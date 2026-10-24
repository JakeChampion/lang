package e2ecompiler

import (
	"strings"
	"testing"
)

const rcenumRebindSrc = `enum T { Text(string), Nothing }
@noinline function ids(s: string): string { return s; }

function round(): i32 {
    let e: T = Nothing;
    let i: i32 = 0;
    while (i < 4) { e = Text(ids("hello")); i = i + 1; }
    let t: i32 = 0;
    match (e) { Text(s) => { t = s.len(); }, Nothing => { t = 0; } }
    return t;
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 7;
}`

func TestSelfHostRcEnumRebindReclaimX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, rcenumRebindSrc, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "rcenum_rebind", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 3 {
		t.Fatalf("exited %d, want 3", exit)
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
		t.Errorf("allocs=%d frees=%d — every rebind must deep-drop the chain it "+
			"orphans and the consuming match must release the last one; frees > allocs "+
			"means both claimed one chain (double free)", allocs, frees)
	}
	if live != 0 {
		t.Errorf("live_bytes=%d, want 0 — a rebound rc-payload enum leaks its box AND "+
			"its string payload per assignment, so this scales with the loop count", live)
	}
}

func TestSelfHostRcEnumRebindHazardsX86_64(t *testing.T) {
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
			name: "escapes_to_container",
			src: `enum T { Text(string), Nothing }
@noinline function ids(s: string): string { return s; }
function round(): i32 {
    let keep: T[] = [];
    let e: T = Nothing;
    let i: i32 = 0;
    while (i < 4) { e = Text(ids("hello")); keep = keep.append(e); i = i + 1; }
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < keep.len()) { match (keep[k]) { Text(s) => { t = t + s.len(); }, Nothing => {} } k = k + 1; }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 97;
}`,
			want: 60,
		},
		{
			name: "passed_to_call",
			src: `enum T { Text(string), Nothing }
@noinline function ids(s: string): string { return s; }
function sink(x: T): i32 { match (x) { Text(s) => { return s.len(); }, Nothing => { return 0; } } return 0; }
function round(): i32 {
    let e: T = Nothing;
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 4) { e = Text(ids("hello")); t = t + sink(e); i = i + 1; }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 97;
}`,
			want: 60,
		},
		{
			name: "aliased_to_local",
			src: `enum T { Text(string), Nothing }
@noinline function ids(s: string): string { return s; }
function round(): i32 {
    let e: T = Text(ids("aa"));
    let keep: T = e;
    e = Text(ids("bbbb"));
    let t: i32 = 0;
    match (keep) { Text(s) => { t = t + s.len(); }, Nothing => {} }
    match (e) { Text(s) => { t = t + s.len(); }, Nothing => {} }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 97;
}`,
			want: 18,
		},
		{
			name: "arm_moves_payload_out",
			src: `enum T { Text(string), Nothing }
@noinline function ids(s: string): string { return s; }
function round(): i32 {
    let out: string[] = [];
    let e: T = Nothing;
    let i: i32 = 0;
    while (i < 3) { e = Text(ids("hello")); match (e) { Text(s) => { out = out.append(s); }, Nothing => {} } i = i + 1; }
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < out.len()) { t = t + out[k].len(); k = k + 1; }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t % 97;
}`,
			want: 45,
		},
		{
			name: "payload_is_param_string",
			src: `enum T { Text(string), Nothing }
function build(p: string, n: i32): i32 {
    let e: T = Nothing;
    let i: i32 = 0;
    while (i < n) { e = Text(p); i = i + 1; }
    let t: i32 = 0;
    match (e) { Text(s) => { t = s.len(); }, Nothing => { t = 0; } }
    return t;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { let owned: string = "hello" + "!"; t = t + build(owned, 3) + owned.len(); r = r + 1; }
    return t % 97;
}`,
			want: 36,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "rcenum_rebind_hazard_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d — a wrong answer or a crash here means the "+
					"rebind's DEEP drop was granted to a shape that still holds a live "+
					"reference to the payload (use-after-free), not merely that it leaked",
					exit, tc.want)
			}
			allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
			if live != 0 || allocs != frees {
				t.Errorf("allocs=%d frees=%d live_bytes=%d, want a balanced census", allocs, frees, live)
			}
		})
	}
}
