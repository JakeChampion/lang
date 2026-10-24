package e2ecompiler

import (
	"testing"
)

const structLitFieldReadDecl = `struct S { a: i32, b: i32 }
struct A { xs: i32[], k: i32 }
`

func structLitFieldReadMain(loopBody string) string {
	return `
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { ` + loopBody + ` }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
}

func structLitFieldReadCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			name: "scalar_struct_field_read",
			src: structLitFieldReadDecl +
				structLitFieldReadMain(`t = t + (S { a: r, b: r }).a; r = r + 1;`),
			want: 3,
		},
		{
			name: "array_field_struct_scalar_read",
			src: structLitFieldReadDecl +
				structLitFieldReadMain(`t = t + (A { xs: [r, r + 1], k: r }).k; r = r + 1;`),
			want: 3,
		},
		{
			name: "rc_field_read",
			src: structLitFieldReadDecl +
				structLitFieldReadMain(`t = t + (A { xs: [r, r + 1], k: r }).xs.len(); r = r + 1;`),
			want: 6,
		},
		{
			name: "rc_field_read_uaf",
			src: `struct A { xs: i32[], k: i32 }
function churn(i: i32): i32 {
    let a: i32[] = [i, i + 1, i + 2, i + 3];
    let b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    let held: i32[] = (A { xs: [i, i + 1], k: i }).xs;
    let junk: i32 = churn(i * 7 + 3);
    if (held.len() != 2) { return 0 - 1; }
    let v: i32 = held[0] + held[1];
    if (v != i + i + 1) { return 0 - 1; }
    return v % 101;
}
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 200) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 25,
		},
		{
			name: "scalar_read_uaf",
			src: `struct A { xs: i32[], k: i32 }
function churn(i: i32): i32 {
    let a: i32[] = [i, i + 1, i + 2, i + 3];
    let b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    let k: i32 = (A { xs: [i, i + 1], k: i * 3 + 1 }).k;
    let junk: i32 = churn(i * 7 + 3);
    if (k != i * 3 + 1) { return 0 - 1; }
    return k % 101;
}
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 200) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 28,
		},
		{
			name: "string_field_struct",
			src: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
struct P { f: string, n: i32 }` +
				structLitFieldReadMain(`t = t + (P { f: w("z"), n: r }).n; r = r + 1;`),
			want: 3,
		},
	}
}

func TestSelfHostStructLitFieldReadX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range structLitFieldReadCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "structlitfld_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = the value "+
					"read back wrong; 139 = it read freed memory)", tc.name, exit, tc.want)
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
			if frees > allocs {
				t.Errorf("%s: %s — more frees than allocs is a double free", tc.name, summary)
			} else if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
