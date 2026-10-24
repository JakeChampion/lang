package e2ecompiler

import (
	"strings"
	"testing"
)

func optionAliasMatchConsumedCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			name: "alias_and_source_matched",
			src: `function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    let x: Option[i32[]] = src;
    let t: i32 = 0;
    match (x) { Some(xs) => { t = t + xs.len(); }, None => {} }
    match (src) { Some(ys) => { t = (t + ys.len()) % 101; }, None => {} }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68,
		},
		{
			name: "dead_alias_source_matched",
			src: `function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    let x: Option[i32[]] = src;
    let t: i32 = 0;
    match (src) { Some(ys) => { t = (t + ys.len()) % 101; }, None => {} }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 34,
		},
		{
			name: "alias_to_borrowing_callee",
			src: `function peek(o: Option[i32[]]): i32 { match (o) { Some(v) => { return v.len(); }, None => {} } return 0; }
function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    let x: Option[i32[]] = src;
    let t: i32 = peek(x);
    match (src) { Some(ys) => { t = (t + ys.len()) % 101; }, None => {} }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68,
		},
		{
			name: "payload_out_via_alias_refused",
			src: `function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    let x: Option[i32[]] = src;
    let out: i32[] = [0];
    match (x) { Some(xs) => { out = xs; }, None => {} }
    match (src) { Some(ys) => { return (out.len() + ys.len()) % 101; }, None => {} }
    return out.len();
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68,
		},
		{
			name: "returned_alias_refused",
			src: `function mk(i: i32): Option[i32[]] {
    let src: Option[i32[]] = Some([i, i + 1]);
    let x: Option[i32[]] = src;
    match (src) { Some(ys) => { if (ys.len() == 99) { return None; } }, None => {} }
    return x;
}
function round(i: i32): i32 {
    let v: Option[i32[]] = mk(i);
    let t: i32 = 0;
    match (v) { Some(zs) => { t = zs.len(); }, None => {} }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 34,
		},
		{
			name: "reassigned_alias_refused",
			src: `function round(i: i32): i32 {
    let src: Option[i32[]] = Some([i, i + 1]);
    let x: Option[i32[]] = src;
    let t: i32 = 0;
    x = Some([i, i + 2, i + 3]);
    match (src) { Some(ys) => { t = (t + ys.len()) % 101; }, None => {} }
    match (x) { Some(xs) => { t = (t + xs.len()) % 101; }, None => {} }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 2,
		},
	}
}

func TestSelfHostOptionAliasMatchConsumedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range optionAliasMatchConsumedCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "optaliasmc_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 139 = read freed memory)", tc.name, exit, tc.want)
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

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "optaliasmc_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}
