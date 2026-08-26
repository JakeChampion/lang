package e2eselfhost

import (
	"testing"
)

// --- A struct LITERAL whose field is read in place ---------------------------
//
// `(S { … }).a` — the last of the three positions an unbound struct literal can
// appear in, and the last one that leaked. 100 allocs / 0 frees against native's
// 100/100 for a scalar-only struct; 200/0 with an rc-array field. Like the
// argument position (#7576) it leaks per EVALUATION, and like it, the shape is
// invisible to the construction-retain matrix, whose 35 cells all bind the
// literal to `var p` first.
//
// With this the three positions agree: discarded statement, call argument, and
// intermediate field read all reclaim; binding to a var still does.
//
// THE MECHANISM WAS HERE, for a different receiver. `lower_expr`'s
// ExprFieldAccess arm already reclaims the box behind a SCALAR field read off a
// strict-fresh producer CALL (`mk().k`, #6491): stash the box, read the field,
// deep-drop the rc fields while the box still owns them, then dec it. A struct
// LITERAL receiver is the same temporary and takes the same release; it simply
// was not admitted.
//
// THE GATE IS A DIFFERENT QUESTION FROM #7576's, and that is the point worth
// keeping. In argument position the question was "can the callee keep this?",
// answered by the borrowability verdict the string and array arms already use.
// Here there is no callee to ask. The hazard is that the read RESULT may alias a
// field the release frees, so the gate is on the FIELD BEING READ: a scalar
// result cannot alias anything, which makes the deep drop unconditionally safe.
//
// An rc-field read (`(A { … }).xs`) stays refused, and that refusal is
// load-bearing. Dropping the scalar gate puts `rc_field_read_uaf` at
// **802 frees against 800 allocs** — more frees than allocations, a double free
// — with the leakcheck summary itself corrupted. It also makes the plain
// `rc_field_read` case read a clean 200/200 where the correct compiler reads
// 200/0: the fifth slice running where a census-only comparison scores the
// broken build higher than the correct one.
//
// String / enum / map / tuple / option fields keep struct_fields_reusable false,
// so a struct carrying one still leaks whole — the safe-leak floor the
// discarded-statement arm documents. `string_field_struct` pins that it is
// unchanged here.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.

const structLitFieldReadDecl = `struct S { a: i32, b: i32 }
struct A { xs: i32[], k: i32 }
`

func structLitFieldReadMain(loopBody string) string {
	return `
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { ` + loopBody + ` }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
}

func structLitFieldReadCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			// The repro, simplest struct: 100/0 before.
			name: "scalar_struct_field_read",
			src: structLitFieldReadDecl +
				structLitFieldReadMain(`t = t + (S { a: r, b: r }).a; r = r + 1;`),
			want: 3, balance: true,
		},
		{
			// A scalar read off a struct that also owns an rc-ARRAY field: the
			// deep drop frees `xs` while the box still owns it, then the box
			// decs. The result is a scalar, so it cannot alias what was freed.
			// 200/0 before.
			name: "array_field_struct_scalar_read",
			src: structLitFieldReadDecl +
				structLitFieldReadMain(`t = t + (A { xs: [r, r + 1], k: r }).k; r = r + 1;`),
			want: 3, balance: true,
		},
		{
			// REFUSED: the read hands out the rc field itself, which the deep
			// drop would free. Stays the leak it was, deliberately.
			name: "rc_field_read",
			src: structLitFieldReadDecl +
				structLitFieldReadMain(`t = t + (A { xs: [r, r + 1], k: r }).xs.len(); r = r + 1;`),
			want: 6,
		},
		{
			// REFUSED, and the case that proves the scalar gate load-bearing:
			// hold the handed-out array across allocation churn and read it.
			// Without the gate this reports 802 frees for 800 allocs.
			name: "rc_field_read_uaf",
			src: `struct A { xs: i32[], k: i32 }
function churn(i: i32): i32 {
    var a: i32[] = [i, i + 1, i + 2, i + 3];
    var b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    var held: i32[] = (A { xs: [i, i + 1], k: i }).xs;
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
			want: 25,
		},
		{
			// The wrong-ANSWER probe for the ADMITTED shape: the scalar must
			// still read correctly after the box it came from was released and
			// churn had the chance to reuse that memory.
			name: "scalar_read_uaf",
			src: `struct A { xs: i32[], k: i32 }
function churn(i: i32): i32 {
    var a: i32[] = [i, i + 1, i + 2, i + 3];
    var b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    var k: i32 = (A { xs: [i, i + 1], k: i * 3 + 1 }).k;
    var junk: i32 = churn(i * 7 + 3);
    if (k != i * 3 + 1) { return 0 - 1; }
    return k % 101;
}
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0; var bad: i32 = 0;
    while (i < 200) { var r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 28, balance: true,
		},
		{
			// The documented safe-leak floor, pinned unchanged: a string field
			// keeps struct_fields_reusable false, so the whole box still leaks
			// even for a scalar read.
			name: "string_field_struct",
			src: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
struct P { f: string, n: i32 }` +
				structLitFieldReadMain(`t = t + (P { f: w("z"), n: r }).n; r = r + 1;`),
			want: 3,
		},
	}
}

// TestSelfHostStructLitFieldReadX86_64 — a scalar field read off a struct
// literal releases the temporary box, and an rc-field read keeps refusing.
func TestSelfHostStructLitFieldReadX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

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
			// frees > allocs is a DOUBLE FREE, and is what dropping the scalar
			// gate produces. Check it on every case, admitted or refused.
			if frees > allocs {
				t.Errorf("%s: %s — more frees than allocs is a double free", tc.name, summary)
			}
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
