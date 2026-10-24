package e2ecompiler

import (
	"strings"
	"testing"
)

func enumAliasMatchConsumedCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			name: "alias_bind_with_source_match",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 {
    let src: E = E.Full([i, i + 1]);
    let x: E = src;
    let t: i32 = 0;
    match (src) { E.Full(ys) => { t = (t + ys.len()) % 101; }, E.None => {} }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 34,
		},
		{
			name: "both_matched",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 {
    let src: E = E.Full([i, i + 1]);
    let x: E = src;
    let t: i32 = 0;
    match (x) { E.Full(xs) => { t = t + xs.len(); }, E.None => {} }
    match (src) { E.Full(ys) => { t = (t + ys.len()) % 101; }, E.None => {} }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68,
		},
		{
			name: "two_matches_no_alias_control",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 {
    let src: E = E.Full([i, i + 1]);
    let t: i32 = 0;
    match (src) { E.Full(xs) => { t = t + xs.len(); }, E.None => {} }
    match (src) { E.Full(ys) => { t = (t + ys.len()) % 101; }, E.None => {} }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68,
		},
		{
			name: "payload_out_via_alias_refused",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 {
    let src: E = E.Full([i, i + 1]);
    let x: E = src;
    let out: i32[] = [0];
    match (x) { E.Full(xs) => { out = xs; }, E.None => {} }
    match (src) { E.Full(ys) => { return (out.len() + ys.len()) % 101; }, E.None => {} }
    return out.len();
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 68,
		},
		{
			name: "returned_alias_refused",
			src: `enum E { Full(i32[]), None }
function mk(i: i32): E {
    let src: E = E.Full([i, i + 1]);
    let x: E = src;
    match (src) { E.Full(ys) => { if (ys.len() == 99) { return E.None; } }, E.None => {} }
    return x;
}
function round(i: i32): i32 {
    let v: E = mk(i);
    let t: i32 = 0;
    match (v) { E.Full(zs) => { t = zs.len(); }, E.None => {} }
    return t;
}
function main(): i32 { let acc: i32 = 0; let i: i32 = 0; while (i < 100) { acc = acc + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return acc % 83; }`,
			want: 34,
		},
	}
}

func TestSelfHostEnumAliasMatchConsumedX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range enumAliasMatchConsumedCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "enumaliasmc_"+tc.name, asm)
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
			sanBin := buildBin(t, gcc, dir, "enumaliasmc_san_"+tc.name, sanAsm)
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
