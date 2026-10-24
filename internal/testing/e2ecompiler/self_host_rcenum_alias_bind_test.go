package e2ecompiler

import (
	"strings"
	"testing"
)

func rcenumAliasBindCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			name: "dead_alias_bind",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 { let src: E = E.Full([i, i + 1]); let x: E = src; let t: i32 = 7; return t; }
function main(): i32 {
    let s: i32 = 0; let r: i32 = 0;
    while (r < 100) { s = s + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return s % 97;
}`,
			want: 21,
		},
		{
			name: "alias_matched_borrow_only",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 { let src: E = E.Full([i, i + 1]); let x: E = src; match (x) { Full(xs) => { return xs.len(); }, None => { return 0; } } }
function main(): i32 {
    let s: i32 = 0; let r: i32 = 0;
    while (r < 100) { s = s + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return s % 97;
}`,
			want: 6,
		},
		{
			name: "source_rebound_alias_live",
			src: `enum E { Full(i32[]), None }
function round(i: i32): i32 { let src: E = E.Full([i, i + 1]); let x: E = src; src = E.Full([i + 2]); match (x) { Full(xs) => { return xs.len(); }, None => { return 0; } } }
function main(): i32 {
    let s: i32 = 0; let r: i32 = 0;
    while (r < 100) { s = s + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return s % 97;
}`,
			want: 6,
		},
		{
			name: "alias_passed_to_callee",
			src: `enum E { Full(i32[]), None }
function peek(e: E): i32 { match (e) { Full(xs) => { return xs.len(); }, None => { return 0; } } }
function round(i: i32): i32 { let src: E = E.Full([i, i + 1]); let x: E = src; return peek(x); }
function main(): i32 {
    let s: i32 = 0; let r: i32 = 0;
    while (r < 100) { s = s + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return s % 97;
}`,
			want: 6,
		},
		{
			name: "payload_out_via_alias_counted",
			src: `enum E { Full(i32[]), None }
function g(i: i32): i32[] { let src: E = E.Full([i, i + 1]); let x: E = src; match (x) { Full(xs) => { return xs; }, None => { return [0]; } } }
function round(i: i32): i32 { let v: i32[] = g(i); return v.len(); }
function main(): i32 {
    let s: i32 = 0; let r: i32 = 0;
    while (r < 100) { s = s + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return s % 97;
}`,
			want: 6,
		},
		{
			name: "alias_returned_refused",
			src: `enum E { Full(i32[]), None }
function mk(i: i32): E { let src: E = E.Full([i, i + 1]); let x: E = src; return x; }
function round(i: i32): i32 { let v: E = mk(i); match (v) { Full(xs) => { return xs.len(); }, None => { return 0; } } }
function main(): i32 {
    let s: i32 = 0; let r: i32 = 0;
    while (r < 100) { s = s + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return s % 97;
}`,
			want: 6,
		},
	}
}

func TestSelfHostRcEnumAliasBindX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range rcenumAliasBindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "rcenumalias_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d — 99 means __rc_underflow_count() fired: the alias forgiveness "+
					"handed the source a credit that frees a payload someone else still holds. The census "+
					"below balances either way, so the exit code is the whole signal.\n%s",
					tc.name, exit, tc.want, leakSummaryLine(stderr))
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
			sanBin := buildBin(t, gcc, dir, "rcenumalias_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "use-after-free") || strings.Contains(sanErr, "rc over-release") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}
