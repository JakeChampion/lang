package e2ecompiler

import (
	"testing"
)

type arrenumShareCase struct {
	name    string
	src     string
	want    int
	balance bool
}

const arrenumShareMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 100) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

const arrenumShareDecl = `enum E { A(i32[]), B }
struct P { f: E[], n: i32 }
function mkv(i: i32): E[] { let o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
`

func arrenumShareCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			name: "conditional",
			src: arrenumShareDecl + `function round(i: i32): i32 {
    let src: E[] = mkv(i);
    let t: i32 = 0;
    if (i % 2 == 0) { let p: P = P { f: src, n: i }; t = p.f.len() + p.n; }
    return t % 101;
}` + arrenumShareMain,
			want: 10, balance: true,
		},
		{
			name: "always",
			src: arrenumShareDecl + `function round(i: i32): i32 {
    let src: E[] = mkv(i);
    let p: P = P { f: src, n: i };
    return (p.f.len() + p.n + src.len()) % 101;
}` + arrenumShareMain,
			want: 69, balance: true,
		},
		{
			name: "holder_escapes",
			src: arrenumShareDecl + `function keepit(p: P): i32 { return p.f.len() + p.n; }
function round(i: i32): i32 {
    let src: E[] = mkv(i);
    let t: i32 = 0;
    if (i % 2 == 0) { let p: P = P { f: src, n: i }; t = keepit(p); }
    return (t + src.len()) % 101;
}` + arrenumShareMain,
			want: 27, balance: true,
		},
		{
			name: "respread",
			src: arrenumShareDecl + `function round(i: i32): i32 {
    let src: E[] = mkv(i);
    let q: P = P { f: src, n: i };
    let p: P = P { ...q, n: i + 1 };
    return (p.f.len() + p.n + q.n) % 101;
}` + arrenumShareMain,
			want: 70, balance: true,
		},
		{
			name: "moved_ret",
			src: arrenumShareDecl + `function hold(i: i32): P {
    let src: E[] = mkv(i);
    return P { f: src, n: i };
}
function round(i: i32): i32 { let p: P = hold(i); return (p.f.len() + p.n) % 101; }` + arrenumShareMain,
			want: 70,
		},
		{
			name: "moved_uaf",
			src: arrenumShareDecl + `function hold(i: i32): P {
    let src: E[] = mkv(i);
    return P { f: src, n: i };
}
function churn(i: i32): i32 {
    let a: i32[] = [i, i + 1, i + 2, i + 3];
    let b: i32[] = [i + 4, i + 5, i + 6, i + 7];
    return a[0] + b[3];
}
function round(i: i32): i32 {
    let p: P = hold(i);
    let junk: i32 = churn(i * 7 + 3);
    let t: i32 = 0;
    match (p.f[0]) {
        E.A(xs) => { t = xs[0] + xs[1]; },
        E.B => { t = 0 - 1; }
    }
    if (t != i + i + 1) { return 0 - 1; }
    return t % 101;
}
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    let bad: i32 = 0;
    while (i < 200) {
        let r: i32 = round(i);
        if (r < 0) { bad = bad + 1; }
        t = t + r;
        i = i + 1;
    }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 25,
		},
	}
}

func TestSelfHostArrEnumFieldShareX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrenumShareCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrenumshare_"+tc.name, asm)
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
