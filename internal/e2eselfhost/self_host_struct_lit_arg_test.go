package e2eselfhost

import (
	"testing"
)

// --- A struct LITERAL passed as a call argument ------------------------------
//
// `take(P { … })` — a temporary nothing else can reach. The ladder that
// reclaims a DISCARDED struct literal lives in lower_stmt_inner's StmtExpr arm
// and has no counterpart in expression position, so an argument temp leaked its
// box and every rc field it owned. Even a SCALAR-ONLY struct, whose release in
// statement position is a single `__fern_rc_dec`: 100 allocs / 0 frees against
// native's 100/100, 4800 bytes over 100 rounds.
//
// It leaks per EVALUATION, not once, which is what separates it from the
// construction-retain matrix's cells — and it is invisible to that matrix,
// because all 35 of its cells bind the literal to `var p` first. That is the one
// position which already worked.
//
// The mechanism was already here and only lacked a dispatch arm.
// `lower_call_named` stashes a fresh literal argument in a scratch local and
// frees it after the call, with arms for string literals, scalar-array
// literals, "ARR:"/"STRARR:" producer calls and the consumed-append temp. Two
// pieces were missing and BOTH are needed — either alone is a no-op:
//
//   - the stash arm itself, releasing with the discarded-statement arm's own two
//     shapes (scalar-only -> box dec; reusable rc fields -> __struct_drop_<T>
//     then the box dec), and
//   - a "BORROW:" row to consult. Those rows are NARROW-SEEDED, deliberately:
//     lower_func seeds only callees that lit_arg_callees_expr saw carrying a
//     literal argument, so the list stays tiny. A struct literal was not in that
//     census, so call_arg_borrowable answered false and the arm could never
//     fire. Adding the arm without the census entry measures as no change at
//     all, which is exactly what it did the first time.
//
// SAFETY is the borrowability gate the string and array arms already use. A
// callee that KEEPS the argument must not have it freed underneath: `keep(p) ->
// p` and `wrap(p, i) -> Box { a: p, n: i }` both stay refused and keep leaking,
// deliberately. Removing that gate puts `callee_wraps_param` at self-host
// exit 99 — an rc underflow — while native exits 3, at a flat 300 allocs / 300
// frees. The same edit makes `callee_returns_param` read a clean 200/200 where
// the correct compiler reads 200/0, so a census-only comparison again scores the
// broken build higher; `field_handed_out_uaf` is the case that reads a value
// back after churn and would catch it.
//
// String / enum / map / tuple / option fields keep struct_fields_reusable false
// and so keep the documented safe-leak floor the statement arm states; nothing
// here widens that.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.

const structLitArgDecl = `struct S { a: i32, b: i32 }
struct A { xs: i32[], k: i32 }
struct Box { a: A, n: i32 }
`

func structLitArgMain(loopBody string) string {
	return `
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { ` + loopBody + ` }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
}

func structLitArgCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			// The repro, simplest possible struct: 100/0 before.
			name: "scalar_struct_arg",
			src: structLitArgDecl + `function takeS(p: S): i32 { return p.a + p.b; }` +
				structLitArgMain(`t = t + takeS(S { a: r, b: r }); r = r + 1;`),
			want: 6, balance: true,
		},
		{
			// An rc-ARRAY field: the deep drop runs before the box dec, in the
			// discarded-statement arm's order. 200/0 before.
			name: "array_field_arg",
			src: structLitArgDecl + `function takeA(p: A): i32 { return p.xs.len() + p.k; }` +
				structLitArgMain(`t = t + takeA(A { xs: [r, r + 1], k: r }); r = r + 1;`),
			want: 9, balance: true,
		},
		{
			// The statement position, which already worked — a control so a
			// regression there is caught here too.
			name: "discarded_statement",
			src: structLitArgDecl +
				structLitArgMain(`A { xs: [r, r + 1], k: r }; t = t + r; r = r + 1;`),
			want: 3, balance: true,
		},
		{
			// REFUSED: the callee returns the argument, so freeing it after the
			// call would hand the caller freed memory. Stays the leak it was.
			name: "callee_returns_param",
			src: structLitArgDecl + `function keep(p: A): A { return p; }` +
				structLitArgMain(`t = t + keep(A { xs: [r, r + 1], k: r }).k; r = r + 1;`),
			want: 3,
		},
		{
			// REFUSED, and the case that proves the gate load-bearing: without it
			// this is exit 99 at a flat 300/300.
			name: "callee_wraps_param",
			src: structLitArgDecl + `function wrap(p: A, i: i32): Box { return Box { a: p, n: i }; }` +
				structLitArgMain(`t = t + wrap(A { xs: [r, r + 1], k: r }, r).n; r = r + 1;`),
			want: 3,
		},
		{
			// ADMITTED and correct: the callee hands the array FIELD back, but
			// that read retains it, so the temp's deep drop decs rather than
			// frees. Balances at 200/200.
			name: "callee_returns_field",
			src: structLitArgDecl + `function grab(p: A): i32[] { return p.xs; }` +
				structLitArgMain(`t = t + grab(A { xs: [r, r + 1], k: r }).len(); r = r + 1;`),
			want: 6, balance: true,
		},
		{
			// The wrong-ANSWER probe for that admission: hold the handed-back
			// array across allocation churn and read it. A census cannot tell a
			// correct release from one that freed this array early.
			name: "field_handed_out_uaf",
			src: `struct A { xs: i32[], k: i32 }
function grab(p: A): i32[] { return p.xs; }
function churn(i: i32): i32 {
    var a: i32[] = [i, i + 1, i + 2, i + 3];
    var b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    var held: i32[] = grab(A { xs: [i, i + 1], k: i });
    var junk: i32 = churn(i * 7 + 3);
    if (held.len() != 2) { return 0 - 1; }
    var v: i32 = held[0] + held[1];
    if (v != i + i + 1) { return 0 - 1; }
    return v % 101;
}
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0; var bad: i32 = 0;
    while (i < 200) { var r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 25, balance: true,
		},
	}
}

// TestSelfHostStructLitArgX86_64 — a struct literal handed to a borrowing callee
// is freed after the call, and every callee that could keep it stays refused.
func TestSelfHostStructLitArgX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

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
