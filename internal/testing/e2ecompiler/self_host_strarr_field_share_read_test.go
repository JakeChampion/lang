package e2ecompiler

import (
	"testing"
)

const strarrShareReadDecl = `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { let o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }
`

func strarrShareReadCases() []arrenumShareCase {
	loop := `
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
	return []arrenumShareCase{
		{
			name: "inline_field_share",
			src: strarrShareReadDecl + `function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let p: P = P { f: q.f, n: i };
    return (p.f.len() + p.f[0].len() + p.n + q.n) % 101;
}` + loop,
			want: 72,
		},
		{
			name: "local_store_unchanged",
			src: strarrShareReadDecl + `function round(i: i32): i32 {
    let src: string[] = mkv(i);
    let p: P = P { f: src, n: i };
    return (p.f.len() + p.f[0].len() + p.n) % 101;
}` + loop,
			want: 71,
		},
		{
			name: "hoisted_bind_now_clean",
			src: strarrShareReadDecl + `function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    return (p.f.len() + p.f[0].len() + p.n + q.n) % 101;
}` + loop,
			want: 72,
		},
		{
			name: "escaping_holder",
			src: strarrShareReadDecl + strarrEscapingChurn + `function make(i: i32): P { let q: P = P { f: mkv(i), n: i }; let p: P = P { f: q.f, n: i }; return p; }
` + strarrEscapingRound + strarrEscapingMain,
			want: 8,
		},
		{
			name: "escaping_holder_read_twice",
			src: strarrShareReadDecl + strarrEscapingChurn + `function make(i: i32): P {
    let q: P = P { f: mkv(i), n: i };
    let p2: P = P { f: q.f, n: i + 1 };
    let p: P = P { f: q.f, n: i + p2.f.len() - 2 };
    return p;
}
` + strarrEscapingRound + strarrEscapingMain,
			want: 8,
		},
		{
			name: "escaping_holder_source_returned",
			src: strarrShareReadDecl + strarrEscapingChurn + `function make(i: i32): P {
    let q: P = P { f: mkv(i), n: i };
    let p: P = P { f: q.f, n: i };
    if (i % 2 == 0) { return q; }
    return p;
}
` + strarrEscapingRound + strarrEscapingMain,
			want: 8,
		},
		{
			name: "escaping_holder_param_source",
			src: strarrShareReadDecl + strarrEscapingChurn + `function make(q: P, i: i32): P { return P { f: q.f, n: i }; }
function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let q: P = P { f: mkv(i), n: i };
    let p: P = make(q, i);
    let junk: i32 = churn(i * 3 + 1);
    if (p.f.len() != 2) { return 0 - 1; }
    if (p.f[0].len() != want) { return 0 - 2; }
    if (q.f[1].len() != want) { return 0 - 3; }
    return (p.f[1].len() + q.f.len() + junk) % 101;
}` + strarrEscapingMain,
			want: 76,
		},
		{
			name: "escaping_holder_shadowed_holder",
			src: strarrShareReadDecl + strarrEscapingChurn + `function make(q: P, i: i32): P {
    let q: P = P { f: mkv(i), n: i };
    let p: P = P { f: q.f, n: i };
    return p;
}
function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let q0: P = P { f: mkv(i + 7), n: i };
    let p: P = make(q0, i);
    let junk: i32 = churn(i * 3 + 1);
    if (p.f.len() != 2) { return 0 - 1; }
    if (p.f[0].len() != want) { return 0 - 2; }
    return (p.f[1].len() + q0.f.len() + junk) % 101;
}` + strarrEscapingMain,
			want: 76,
		},
		{
			name: "escaping_holder_element_bound",
			src: strarrShareReadDecl + strarrEscapingChurn + `function make(i: i32): P {
    let q: P = P { f: mkv(i), n: i };
    let e: string = q.f[0];
    let p: P = P { f: q.f, n: e.len() };
    return p;
}
` + strarrEscapingRound + strarrEscapingMain,
			want: 8,
		},
	}
}

const strarrEscapingChurn = `function churn(i: i32): i32 { let a: string[] = mkv(i); let b: string[] = mkv(i + 1); return a[0].len() + b[1].len(); }
`

const strarrEscapingRound = `function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let p: P = make(i);
    let junk: i32 = churn(i * 3 + 1);
    if (p.f.len() != 2) { return 0 - 1; }
    if (p.f[0].len() != want) { return 0 - 2; }
    return (p.f[1].len() + junk) % 101;
}`

const strarrEscapingMain = `
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 200) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`

func TestSelfHostStrArrFieldShareReadX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strarrShareReadCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strarrshareread_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = an element read back "+
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
