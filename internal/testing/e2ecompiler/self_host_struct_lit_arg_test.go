package e2ecompiler

import (
	"testing"
)

const structLitArgDecl = `struct S { a: i32, b: i32 }
struct A { xs: i32[], k: i32 }
struct Box { a: A, n: i32 }
`

func structLitArgMain(loopBody string) string {
	return `
function main(): i32 {
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { ` + loopBody + ` }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
}

func structLitArgCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			name: "scalar_struct_arg",
			src: structLitArgDecl + `function takeS(p: S): i32 { return p.a + p.b; }` +
				structLitArgMain(`t = t + takeS(S { a: r, b: r }); r = r + 1;`),
			want: 6, balance: true,
		},
		{
			name: "array_field_arg",
			src: structLitArgDecl + `function takeA(p: A): i32 { return p.xs.len() + p.k; }` +
				structLitArgMain(`t = t + takeA(A { xs: [r, r + 1], k: r }); r = r + 1;`),
			want: 9, balance: true,
		},
		{
			name: "discarded_statement",
			src: structLitArgDecl +
				structLitArgMain(`A { xs: [r, r + 1], k: r }; t = t + r; r = r + 1;`),
			want: 3, balance: true,
		},
		{
			name: "callee_returns_param",
			src: structLitArgDecl + `@noinline function keep(p: A): A { return p; }` +
				structLitArgMain(`t = t + keep(A { xs: [r, r + 1], k: r }).k; r = r + 1;`),
			want: 3,
		},
		{
			name: "callee_wraps_param",
			src: structLitArgDecl + `function wrap(p: A, i: i32): Box { return Box { a: p, n: i }; }` +
				structLitArgMain(`t = t + wrap(A { xs: [r, r + 1], k: r }, r).n; r = r + 1;`),
			want: 3,
		},
		{
			name: "callee_returns_field",
			src: structLitArgDecl + `function grab(p: A): i32[] { return p.xs; }` +
				structLitArgMain(`t = t + grab(A { xs: [r, r + 1], k: r }).len(); r = r + 1;`),
			want: 6, balance: true,
		},
		{
			name: "field_handed_out_uaf",
			src: `struct A { xs: i32[], k: i32 }
function grab(p: A): i32[] { return p.xs; }
function churn(i: i32): i32 {
    let a: i32[] = [i, i + 1, i + 2, i + 3];
    let b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    let held: i32[] = grab(A { xs: [i, i + 1], k: i });
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
			want: 25, balance: true,
		},
	}
}

func TestSelfHostStructLitArgX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range structLitArgCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "structlitarg_"+tc.name, asm)
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
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
