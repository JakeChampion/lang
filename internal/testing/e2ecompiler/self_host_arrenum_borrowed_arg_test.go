package e2ecompiler

import (
	"testing"
)

const arrenumBorrowDecl = `enum E { A(i32[]), B }
function mkv(i: i32): E[] { let o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
function seed(): i32 { return 7; }
`

func arrenumBorrowMain(src, use string) string {
	return `
function main(): i32 {
    let keep: E[] = ` + src + `;
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + ` + use + `; r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
}

func arrenumBorrowCases() []arrenumShareCase {
	producer := "mkv(seed())"
	literal := "[E.A(id([seed(), 8]))]"
	mk := func(decls, src, use string) string {
		return arrenumBorrowDecl + decls + arrenumBorrowMain(src, use)
	}
	return []arrenumShareCase{
		{
			name: "borrowed_arg",
			src: mk(`function rd(src: E[], i: i32): i32 { return (src.len() + i) % 101; }`,
				producer, "rd(keep, r)"),
			want: 6, balance: true,
		},
		{
			name: "borrowed_arg_literal",
			src: mk(`function rd(src: E[], i: i32): i32 { return (src.len() + i) % 101; }
@noinline function id(xs: i32[]): i32[] { return xs; }`,
				literal, "rd(keep, r)"),
			want: 6, balance: true,
		},
		{
			name: "not_passed",
			src:  mk(``, producer, "keep.len() + r"),
			want: 6, balance: true,
		},
		{
			name: "callee_stores_field",
			src: mk(`struct P { f: E[], n: i32 }
function rd(src: E[], i: i32): i32 { let p: P = P { f: src, n: i }; return (p.f.len() + p.n) % 101; }`,
				producer, "rd(keep, r)"),
			want: 6, balance: true,
		},
		{
			name: "callee_extracts_element_expr",
			src: mk(`function rd(src: E[], i: i32): i32 { return (match (src[0]) { E.A(xs) => xs.len(), E.B => 0 }) + i - i; }`,
				producer, "rd(keep, r)"),
			want: 6, balance: true,
		},
		{
			name: "iife_element_handed_out",
			src: `enum E { A(i32[]), B }
struct H { e: E, n: i32 }
function mkv(i: i32): E[] { let o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
function grab(src: E[], i: i32): H { return (if (i >= 0) { H { e: src[0], n: i } } else { H { e: E.B, n: 0 } }); }
function f(i: i32): H { let keep: E[] = mkv(i); return grab(keep, i); }
function round(i: i32): i32 {
    let h: H = f(i);
    let v: i32 = 0;
    match (h.e) { E.A(xs) => { v = xs[0] + xs[1]; }, E.B => { v = 0 - 1; } }
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
			name: "iife_arm_binding_escapes",
			src: mk(`function take(xs: i32[]): i32 { return xs.len(); }
function rd(src: E[], i: i32): i32 { return (match (src[0]) { E.A(xs) => take(xs), E.B => 0 }) + i - i; }`,
				producer, "rd(keep, r)"),
			want: 6,
		},
		{
			name: "callee_returns_param",
			src: mk(`@noinline function rd(src: E[], i: i32): E[] { return src; }`,
				producer, "rd(keep, r).len()"),
			want: 3,
		},
		{
			name: "callee_extracts_element",
			src: mk(`function rd(src: E[], i: i32): i32 { let e: E = src[0]; return (match (e) { E.A(xs) => xs.len(), E.B => 0 }) + i; }`,
				producer, "rd(keep, r)"),
			want: 9, balance: true,
		},
		{
			name: "callee_appends_element",
			src: mk(`function rd(src: E[], i: i32): i32 { let o: E[] = []; o = o.append(src[0]); return o.len() + i; }`,
				producer, "rd(keep, r)"),
			want: 6,
		},
		{
			name: "element_handed_out",
			src: `enum E { A(i32[]), B }
struct H { e: E, n: i32 }
function mkv(i: i32): E[] { let o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
function grab(src: E[], i: i32): H { return H { e: src[0], n: i }; }
function f(i: i32): H { let keep: E[] = mkv(i); return grab(keep, i); }
function churn(i: i32): i32 {
    let a: i32[] = [i, i + 1, i + 2, i + 3];
    let b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    let h: H = f(i);
    let junk: i32 = churn(i * 7 + 3);
    let v: i32 = 0;
    match (h.e) { E.A(xs) => { v = xs[0] + xs[1]; }, E.B => { v = 0 - 1; } }
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
	}
}

func TestSelfHostArrEnumBorrowedArgX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrenumBorrowCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrenumborrow_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = the payload "+
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
