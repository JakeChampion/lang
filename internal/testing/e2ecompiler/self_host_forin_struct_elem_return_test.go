package e2ecompiler

import (
	"testing"
)

// --- A struct-array element read through a for-in body that RETURNS ---------
//
// Native's for-in element borrow (#6888) admits a body that returns a
// projection of the element as of #8178 — `return sd.name`, `return
// sd.fields.len()` — instead of retaining and deep-dropping the element every
// iteration. This file is the self-host side of that rule, and it pins what
// measurement found: there is nothing to port. The self-host never retains a
// for-in binder — it is an uncounted borrow of the element the container
// owns — and its body walkers already read `sd.f` as a borrow wherever it
// stands (expr_unsafe_for's FieldAccess arm is position-independent), so a
// read-only body and a scalar-returning body over a local or a param struct
// array balance on the self-host exactly as they do on native.
//
// Two shapes once leaked on the AST lowering — a returned rc-typed projection
// of a borrowed PARAM's element (`return sd.name`, and its INDEX spellings),
// and `for sd in mks(i)` over a CALL RESULT. On the typed lowering every case
// holds allocs == frees at live_bytes 0 and exits identically on native x86-64
// and the self-host.

const forinStructElemDecl = `struct S { name: string, fields: string[] }
function mkstr(p: string): string { return p + "-long-enough-to-heap-allocate"; }
function mks(i: i32): S[] {
    return [S{ name: mkstr("a"), fields: [mkstr("f")] }, S{ name: mkstr("bb"), fields: [] }];
}
function churn(i: i32): i32 { let a: string[] = [mkstr("c"), mkstr("d")]; return a[0].len() + a[1].len(); }
`

const forinStructElemMain = `
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }
`

func forinStructElemReturnCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			// The control: a read-only body over a local struct array.
			name: "forin_local_read",
			src: forinStructElemDecl + `@noinline function scan(i: i32): i32 {
    let xs: S[] = mks(i);
    let t: i32 = 0;
    for sd in xs { t = t + sd.name.len() + sd.fields.len(); }
    return t;
}
function round(i: i32): i32 { return scan(i) % 101; }` + forinStructElemMain,
			want: 58,
		},
		{
			// A scalar read through the element returned mid-loop: the sweep
			// at that return releases the local container under the borrowed
			// element, after the read.
			name: "forin_local_return_scalar",
			src: forinStructElemDecl + `@noinline function count(i: i32, k: i32): i32 {
    let xs: S[] = mks(i);
    for sd in xs { if (sd.name.len() == k) { return sd.fields.len(); } }
    return 0 - 1;
}
function round(i: i32): i32 { return (count(i, 30) + count(i, 31) + 2) % 101; }` + forinStructElemMain,
			want: 51,
		},
		{
			// The same two bodies over a PARAM container.
			name: "forin_param_read",
			src: forinStructElemDecl + `@noinline function scan(xs: S[]): i32 {
    let t: i32 = 0;
    for sd in xs { t = t + sd.name.len() + sd.fields.len(); }
    return t;
}
function round(i: i32): i32 { let xs: S[] = mks(i); return (scan(xs) + scan(xs)) % 101; }` + forinStructElemMain,
			want: 59,
		},
		{
			name: "forin_param_return_scalar",
			src: forinStructElemDecl + `@noinline function count(xs: S[], k: i32): i32 {
    for sd in xs { if (sd.name.len() == k) { return sd.fields.len(); } }
    return 0 - 1;
}
function round(i: i32): i32 { let xs: S[] = mks(i); return (count(xs, 30) + count(xs, 31) + 2) % 101; }` + forinStructElemMain,
			want: 51,
		},
		{
			// A returned STRING FIELD of a borrowed param's element. The
			// value survives the churn (exit 36 on both compilers).
			name: "forin_param_return_string_field",
			src: forinStructElemDecl + `@noinline function pick(xs: S[], k: i32): string {
    for sd in xs { if (sd.fields.len() == k) { return sd.name; } }
    return "";
}
function round(i: i32): i32 {
    let xs: S[] = mks(i);
    let hit: string = pick(xs, 1);
    let junk: i32 = churn(i);
    if (hit.len() != 30) { return 0 - 1; }
    return (hit.len() + junk) % 101;
}` + forinStructElemMain,
			want: 36,
		},
		{
			// The INDEX spelling of the case above.
			name: "index_param_return_string_field",
			src: forinStructElemDecl + `@noinline function pick(xs: S[], k: i32): string {
    let j: i32 = 0;
    while (j < xs.len()) { if (xs[j].fields.len() == k) { return xs[j].name; } j = j + 1; }
    return "";
}
function round(i: i32): i32 {
    let xs: S[] = mks(i);
    let hit: string = pick(xs, 1);
    let junk: i32 = churn(i);
    if (hit.len() != 30) { return 0 - 1; }
    return (hit.len() + junk) % 101;
}` + forinStructElemMain,
			want: 36,
		},
		{
			// A CALL-RESULT iterand with a read-only body.
			name: "forin_call_iterand_read",
			src: forinStructElemDecl + `@noinline function scan(i: i32): i32 {
    let t: i32 = 0;
    for sd in mks(i) { t = t + sd.name.len() + sd.fields.len(); }
    return t;
}
function round(i: i32): i32 { return scan(i) % 101; }` + forinStructElemMain,
			want: 58,
		},
	}
}

// TestSelfHostForInStructElemReturnX86_64 — a for-in body that returns a
// projection of a struct-array element keeps the container's credit on the
// self-host as native's #8178 rule keeps the element borrow.
func TestSelfHostForInStructElemReturnX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range forinStructElemReturnCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "forinstruct_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 139 = it read freed memory)", tc.name, exit, tc.want)
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
