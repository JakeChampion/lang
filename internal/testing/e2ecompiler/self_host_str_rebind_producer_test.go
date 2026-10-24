package e2ecompiler

import (
	"testing"
)

const strRebindDecl = `function mkstr(a: string): string { return a + "-long-enough-to-heap-allocate"; }
function churn(i: i32): i32 { let a: string = mkstr("c"); let b: string = mkstr("d"); return a.len() + b.len(); }
`

const strRebindChurnMain = `
function main(): i32 {
    let acc: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 200) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } acc = acc + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`

func strRebindCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			name: "producer_rebind_read",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: i32 = 0;
    let x: string = mkstr("x");
    x = mkstr("yz");
    t = (t + x.len()) % 101;
    t = t + 1;
    return t;
}
function main(): i32 {
    let acc: i32 = 0; let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 68,
		},
		{
			name: "producer_rebind_heap",
			src: strRebindDecl + `function round(i: i32): i32 {
    let t: i32 = 0;
    let x: string = mkstr("x");
    x = mkstr("yz");
    t = (t + x.len()) % 101;
    return t + 1;
}
function main(): i32 {
    let acc: i32 = 0; let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 46,
		},
		{
			name: "conditional_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    let x: string = mkstr("x");
    if (i % 2 == 0) { x = mkstr("yz"); }
    let junk: i32 = churn(i);
    if (x.len() < 10) { return 0 - 1; }
    return (x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 6,
		},
		{
			name: "loop_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    let x: string = mkstr("x");
    let j: i32 = 0;
    while (j < 3) { x = mkstr("y"); j = j + 1; }
    let junk: i32 = churn(i);
    if (x.len() < 10) { return 0 - 1; }
    return (x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 72,
		},
		{
			name: "self_consuming_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    let x: string = mkstr("x");
    x = mkstr(x);
    let junk: i32 = churn(i);
    if (x.len() < 10) { return 0 - 1; }
    return (x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 31,
		},
		{
			name: "moved_out_return",
			src: strRebindDecl + `function grab(i: i32): string { let x: string = mkstr("x"); x = mkstr("yz"); return x; }
function round(i: i32): i32 {
    let want: i32 = mkstr("yz").len();
    let s: string = grab(i);
    let junk: i32 = churn(i);
    if (s.len() != want) { return 0 - 1; }
    return (s.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 23,
		},
		{
			name: "single_bind_unchanged",
			src: `function mkstr(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    let t: i32 = 0;
    let x: string = mkstr("x");
    t = (t + x.len()) % 101;
    return t + 1;
}
function main(): i32 {
    let acc: i32 = 0; let i: i32 = 0;
    while (i < 100) { acc = acc + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return acc % 83;
}`,
			want: 51,
		},
		{
			name: "refused_alias_before_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    let want: i32 = mkstr("x").len();
    let x: string = mkstr("x");
    let y: string = x;
    x = mkstr("yz");
    let junk: i32 = churn(i);
    if (y.len() != want) { return 0 - 1; }
    return (y.len() + x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 16,
		},
		{
			name: "refused_nonfresh_rebind",
			src: strRebindDecl + `function round(i: i32): i32 {
    let other: string = mkstr("o");
    let x: string = mkstr("x");
    x = other;
    let junk: i32 = churn(i);
    if (x.len() != other.len()) { return 0 - 1; }
    return (x.len() + junk) % 101;
}` + strRebindChurnMain,
			want: 72,
		},
		{
			name: "refused_container_store",
			src: strRebindDecl + `function round(i: i32): i32 {
    let x: string = mkstr("x");
    x = mkstr("yz");
    let box: string[] = [x];
    let junk: i32 = churn(i);
    if (box[0].len() != x.len()) { return 0 - 1; }
    return (box[0].len() + junk) % 101;
}` + strRebindChurnMain,
			want: 23,
		},
	}
}

func TestSelfHostStrRebindProducerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strRebindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strrebind_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = a value read back "+
					"wrong, i.e. an over-release; 139 = it read freed memory)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
