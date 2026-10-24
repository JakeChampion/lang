package e2ecompiler

import (
	"strings"
	"testing"
)

func tupleDestructureRetainCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			name: "i32arr_bind_read",
			src: `function round(i: i32): i32 { let p: (i32, i32[]) = (i, [i, i + 1]); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			name: "f64arr_bind_read",
			src: `function round(i: i32): i32 { let p: (i32, f64[]) = (i, [1.5, 2.5]); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			name: "i64arr_bind_read",
			src: `function round(i: i32): i32 { let p: (i32, i64[]) = (i, [1i64, 2i64]); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			name: "bare_ident_elem_source",
			src: `function round(i: i32): i32 { let xs: i32[] = [i, i + 1]; let p: (i32, i32[]) = (i, xs); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			name: "moved_out_by_return",
			src: `function get(i: i32): i32[] { let p: (i32, i32[]) = (i, [i, i + 1]); let (a, b) = p; return b; }
function round(i: i32): i32 { let r: i32[] = get(i); return r.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 6,
		},
		{
			name: "passed_to_callee",
			src: `function consume(xs: i32[]): i32 { return xs.len(); }
function round(i: i32): i32 { let p: (i32, i32[]) = (i, [i, i + 1]); let (a, b) = p; return consume(b) + a % 3; }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 8,
		},
		{
			name: "strarr_elem_bind_read",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { let p: (i32, string[]) = (i, [w("x"), w("y")]); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			name: "strarr_literal_elems",
			src: `function round(i: i32): i32 { let p: (i32, string[]) = (i, ["x", "y"]); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			name: "strarr_bare_ident_elem_source",
			src: `function round(i: i32): i32 { let xs: string[] = ["x", "y"]; let p: (i32, string[]) = (i, xs); let (a, b) = p; return a + b.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 9,
		},
		{
			name: "strarr_passed_to_callee",
			src: `function w(a: string): string { return a + "!"; }
function consume(xs: string[]): i32 { return xs.len(); }
function round(i: i32): i32 { let p: (i32, string[]) = (i, [w("x"), w("y")]); let (a, b) = p; return consume(b) + a % 3; }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 8,
		},
		{
			name: "strarr_moved_out_by_return",
			src: `function w(a: string): string { return a + "!"; }
function get(i: i32): string[] { let p: (i32, string[]) = (i, [w("x"), w("y")]); let (a, b) = p; return b; }
function round(i: i32): i32 { let r: string[] = get(i); return r.len(); }
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 6,
		},
	}
}

func TestSelfHostTupleDestructureRetainX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range tupleDestructureRetainCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupdestr_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d — 99 means __rc_underflow_count() fired, which is this issue's defect: "+
					"the destructured element was released once more than it was retained. The census below is "+
					"balanced either way, so the exit code is the whole signal.\n%s",
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
			sanBin := buildBin(t, gcc, dir, "tupdestr_san_"+tc.name, sanAsm)
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
