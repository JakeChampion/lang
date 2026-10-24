package e2ecompiler

import (
	"testing"
)

const strarrBindShareDecl = `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { let o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }
function churn(i: i32): i32 { let a: string[] = mkv(i); let b: string[] = mkv(i + 1); return a[0].len() + b[1].len(); }
`

const strarrBindChurnMain = `
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 200) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`

const strarrBindPlainMain = `
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func strarrBindShareCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			name: "bind_then_store",
			src: strarrBindShareDecl + `function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    return (p.f.len() + p.f[0].len() + p.n + q.n) % 101;
}` + strarrBindPlainMain,
			want: 72,
		},
		{
			name: "escaping_holder_now_clean",
			src: strarrBindShareDecl + `function make(i: i32): P {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    return p;
}
function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let p: P = make(i);
    let junk: i32 = churn(i * 3 + 1);
    if (p.f.len() != 2) { return 0 - 1; }
    if (p.f[0].len() != want) { return 0 - 2; }
    return (p.f[1].len() + junk) % 101;
}` + strarrBindChurnMain,
			want: 8,
		},
		{
			name: "fresh_local_bind_unchanged",
			src: strarrBindShareDecl + `function round(i: i32): i32 {
    let src: string[] = mkv(i);
    let tt: string[] = src;
    let p: P = P { f: tt, n: i };
    return (p.f.len() + p.f[0].len() + p.n) % 101;
}` + strarrBindPlainMain,
			want: 71,
		},
		{
			name: "refused_element_escapes",
			src: strarrBindShareDecl + `function grab(i: i32): string {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    return tt[0];
}
function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let s: string = grab(i);
    let junk: i32 = churn(i * 3 + 1);
    if (s.len() != want) { return 0 - 1; }
    return (s.len() + junk) % 101;
}` + strarrBindChurnMain,
			want: 8,
		},
		{
			name: "refused_array_escapes",
			src: strarrBindShareDecl + `function grab(i: i32): string[] {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    return tt;
}
function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let xs: string[] = grab(i);
    let junk: i32 = churn(i * 3 + 1);
    if (xs.len() != 2) { return 0 - 1; }
    if (xs[0].len() != want) { return 0 - 2; }
    return (xs[1].len() + junk) % 101;
}` + strarrBindChurnMain,
			want: 8,
		},
		{
			name: "refused_element_bound",
			src: strarrBindShareDecl + `function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let e: string = tt[0];
    let p: P = P { f: tt, n: i };
    let junk: i32 = churn(i * 3 + 1);
    if (e.len() != want) { return 0 - 1; }
    return (e.len() + p.n + junk) % 101;
}` + strarrBindChurnMain,
			want: 40,
		},
		{
			name: "appended_now_clean",
			src: strarrBindShareDecl + `function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    tt = tt.append(w("c"));
    let p: P = P { f: tt, n: i };
    return (p.f.len() + p.f[0].len() + q.f.len() + q.n) % 101;
}` + strarrBindPlainMain,
			want: 68,
		},
		{
			name: "iterated_now_clean",
			src: strarrBindShareDecl + `function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    let acc: i32 = 0;
    for s in tt { acc = acc + s.len(); }
    return (acc + p.n + q.n) % 101;
}` + strarrBindPlainMain,
			want: 43,
		},
		{
			name: "refused_call_argument",
			src: strarrBindShareDecl + `function keep(xs: string[]): i32 { let k: string[] = xs; return k[0].len() + k.len(); }
function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    let k: i32 = keep(tt);
    return (k + p.n + q.n) % 101;
}` + strarrBindPlainMain,
			want: 72,
		},
	}
}

func TestSelfHostStrArrFieldBindShareX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strarrBindShareCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strarrbindshare_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = a value read back "+
					"wrong, i.e. an over-release; 139 = it read freed memory)", tc.name, exit, tc.want)
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
