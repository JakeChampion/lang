package e2ecompiler

import (
	"testing"
)

type countedParamCase struct {
	name    string
	src     string
	want    int
	balance bool
}

func countedParamCases() []countedParamCase {
	return []countedParamCase{
		{
			name: "counted_arg_string",
			src: `struct P { f: string, n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string { let s: string = w("k"); return s; }

function round(src: string, i: i32): i32 {
    let p: P = P { f: src, n: i };
    return (p.f.len() + p.n) % 101;
}
function main(): i32 {
    let keep: string = mkv(7);
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(keep, r); t = t + 0; r = r + 1; }
    t = (t + 0) % 97;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 73, balance: true,
		},
		{
			name: "counted_arg_strarr",
			src: `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { let o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }

function round(src: string[], i: i32): i32 {
    let p: P = P { f: src, n: i };
    return (p.f.len() + p.f[0].len() + p.n) % 101;
}
function main(): i32 {
    let keep: string[] = mkv(7);
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(keep, r); t = t + 0; r = r + 1; }
    t = (t + 0) % 97;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 71, balance: true,
		},
		{
			name: "callee_returns_param",
			src: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string { let s: string = w("k"); return s; }
// hands the param straight back — the caller must NOT get a release credit
@noinline function esc(src: string, i: i32): string { return src; }
function main(): i32 {
    let keep: string = mkv(7);
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { let o: string = esc(keep, r); t = t + o.len(); r = r + 1; }
    // churn: overwrite anything freed under us
    let c: i32 = 0;
    while (c < 200) { let junk: string = w("zzzz"); t = t + junk.len(); c = c + 1; }
    // read keep back AFTER the churn — a dangling keep shows up here
    t = t + keep.len() * 1000;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 63, balance: false,
		},
		{
			name: "callee_returns_param_readback",
			src: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string { let s: string = w("k"); return s; }
@noinline function esc(src: string, i: i32): string { return src; }
function bytesum(s: string): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < s.len()) { acc = acc + (s[i] as i32); i = i + 1; }
    return acc;
}
function main(): i32 {
    let keep: string = mkv(7);
    let before: i32 = bytesum(keep);
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { let o: string = esc(keep, r); t = t + o.len(); r = r + 1; }
    let c: i32 = 0;
    while (c < 200) { let junk: string = w("zzzz"); t = t + junk.len(); c = c + 1; }
    let after: i32 = bytesum(keep);
    if (after != before) { return 88; }
    if (__rc_underflow_count() != 0) { return 99; }
    return (t + before) % 97;
}`,
			want: 45, balance: false,
		},
		{
			name: "param_in_returned_struct",
			src: `struct P { f: string, n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string { let s: string = w("k"); return s; }
// stores the param into a RETURNED struct: the counted-store escaping path
function mk(src: string, i: i32): P { return P { f: src, n: i }; }
function main(): i32 {
    let keep: string = mkv(7);
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { let h: P = mk(keep, r); t = t + h.f.len() + h.n; r = r + 1; }
    let c: i32 = 0;
    while (c < 200) { let junk: string = w("zzzz"); t = t + junk.len(); c = c + 1; }
    t = t + keep.len() * 1000;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 66, balance: false,
		},
		{
			name: "onward_pass_to_handback",
			src: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string { let s: string = w("k"); return s; }
@noinline function esc2(s: string): string { return s; }
// passes the param ONWARD to a callee that hands it back
function outer(src: string, i: i32): i32 { let o: string = esc2(src); return o.len(); }
function main(): i32 {
    let keep: string = mkv(7);
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + outer(keep, r); r = r + 1; }
    let c: i32 = 0;
    while (c < 200) { let junk: string = w("zzzz"); t = t + junk.len(); c = c + 1; }
    t = t + keep.len() * 1000;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 63, balance: false,
		},
		{
			name: "callee_extracts_element",
			src: `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { let o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }
// hands an ELEMENT out — the caller's deep walk must not free it under the result
function el(src: string[], i: i32): string { return src[0]; }
function main(): i32 {
    let keep: string[] = mkv(7);
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { let e: string = el(keep, r); t = t + e.len(); r = r + 1; }
    let c: i32 = 0;
    while (c < 200) { let junk: string = w("zzzz"); t = t + junk.len(); c = c + 1; }
    t = t + keep.len() * 1000 + keep[0].len() * 10;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 85, balance: false,
		},
		{
			name: "strarr_in_returned_struct",
			src: `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { let o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }
// string[] stored into a RETURNED struct, then read back after churn
function mk(src: string[], i: i32): P { return P { f: src, n: i }; }
function main(): i32 {
    let keep: string[] = mkv(7);
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { let h: P = mk(keep, r); t = t + h.f.len() + h.f[0].len() + h.n; r = r + 1; }
    let c: i32 = 0;
    while (c < 200) { let junk: string = w("zzzz"); t = t + junk.len(); c = c + 1; }
    t = t + keep.len() * 1000 + keep[0].len() * 10;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 94, balance: false,
		},
		{
			name: "counted_arg_enum",
			src: `struct P { f: E, n: i32 }
enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }

function round(src: E, i: i32): i32 {
    let p: P = P { f: src, n: i };
    return ((match (p.f) { E.A(xs) => xs.len(), E.B => 0 }) + p.n) % 101;
}
function main(): i32 {
    let keep: E = mkv(7);
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(keep, r); t = t + 0; r = r + 1; }
    t = (t + 0) % 97;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 5, balance: true,
		},
		{
			name: "callee_returns_enum",
			src: `enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }
function seed(): i32 { return 7; }
struct P { f: E, n: i32 }
@noinline function esc(src: E, i: i32): E { return src; }
function main(): i32 {
    let keep: E = mkv(seed());
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { let o: E = esc(keep, r); t = t + (match (o) { E.A(xs) => xs.len(), E.B => 0 }); r = r + 1; }
    t = t + (match (keep) { E.A(xs) => xs[0], E.B => 0 });
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 13, balance: false,
		},
		{
			name: "callee_hands_out_payload",
			src: `enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }
function seed(): i32 { return 7; }
function grab(src: E, i: i32): i32[] { match (src) { E.A(xs) => { return xs; }, E.B => { return []; } } return []; }
function main(): i32 {
    let keep: E = mkv(seed());
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { let p: i32[] = grab(keep, r); t = t + p.len() + p[0]; r = r + 1; }
    let c: i32 = 0;
    while (c < 200) { let junk: i32[] = [c, c + 1, c + 2]; t = t + junk[2]; c = c + 1; }
    t = t + (match (keep) { E.A(xs) => xs[0] * 1000, E.B => 0 });
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 70, balance: false,
		},
		{
			name: "enum_holder_escapes_readback",
			src: `enum E { A(i32[]), B }
function mkv(i: i32): E { return E.A([i, i + 1]); }
function seed(): i32 { return 7; }
struct P { f: E, n: i32 }
function keepit(src: E, i: i32): P { return P { f: src, n: i }; }
function churnjunk(i: i32): i32 { let a: i32[] = [i, i + 1, i + 2]; return a[2]; }
function round(i: i32): i32 {
    let k: E = mkv(seed() + i);
    let h: P = keepit(k, i);
    let j: i32 = 0; let t: i32 = 0;
    while (j < 20) { t = t + churnjunk(j); j = j + 1; }
    let v: i32 = (match (h.f) { E.A(xs) => xs[0] + xs[1], E.B => 0 });
    if (v != (seed() + i) + (seed() + i + 1)) { return 0 - 1; }
    return (t + v) % 101;
}
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 300) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 10, balance: true,
		},
	}
}

func TestSelfHostCountedParamReleaseX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range countedParamCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "countedparam_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (88 = the value read back wrong after "+
					"churn; 99 = rc underflow; 139 = it read freed memory)", tc.name, exit, tc.want)
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
			}
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
