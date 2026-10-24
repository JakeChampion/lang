package e2ecompiler

import (
	"testing"
)

type arrstructShareCase struct {
	name    string
	src     string
	want    int
	balance bool
}

const arrstructShareMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 100) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

const arrstructShareDecl = `struct Inner { xs: i32[], k: i32 }
struct P { f: Inner[], n: i32 }
function mkv(i: i32): Inner[] { let o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1], k: i }); return o; }
`

func arrstructShareCases() []arrstructShareCase {
	return []arrstructShareCase{
		{
			name: "conditional",
			src: arrstructShareDecl + `function round(i: i32): i32 {
    let src: Inner[] = mkv(i);
    let t: i32 = 0;
    if (i % 2 == 0) { let p: P = P { f: src, n: i }; t = p.f.len() + p.f[0].k + p.n; }
    return t % 101;
}` + arrstructShareMain,
			want: 18, balance: true,
		},
		{
			name: "always",
			src: arrstructShareDecl + `function round(i: i32): i32 {
    let src: Inner[] = mkv(i);
    let p: P = P { f: src, n: i };
    return (p.f.len() + p.f[0].k + p.n + src.len()) % 101;
}` + arrstructShareMain,
			want: 70, balance: true,
		},
		{
			name: "respread",
			src: arrstructShareDecl + `function round(i: i32): i32 {
    let src: Inner[] = mkv(i);
    let q: P = P { f: src, n: i };
    let p: P = P { ...q, n: i + 1 };
    return (p.f.len() + p.n + q.n) % 101;
}` + arrstructShareMain,
			want: 70, balance: true,
		},
		{
			name: "holder_escapes",
			src: arrstructShareDecl + `function keepit(p: P): i32 { return p.f.len() + p.n; }
function round(i: i32): i32 {
    let src: Inner[] = mkv(i);
    let t: i32 = 0;
    if (i % 2 == 0) { let p: P = P { f: src, n: i }; t = keepit(p); }
    return (t + src.len()) % 101;
}` + arrstructShareMain,
			want: 27, balance: true,
		},
		{
			name: "moved_ret",
			src: arrstructShareDecl + `function hold(i: i32): P {
    let src: Inner[] = mkv(i);
    return P { f: src, n: i };
}
function round(i: i32): i32 { let p: P = hold(i); return (p.f.len() + p.f[0].k + p.n) % 101; }` + arrstructShareMain,
			want: 53,
		},
		{
			name: "sibling_alias",
			src: arrstructShareDecl + `function round(i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let src: Inner[] = mkv(i); let p: P = P { f: src, n: i }; t = t + p.f.len() + p.n; }
    if (i % 2 == 1) { let src: Inner[] = [Inner { xs: [i], k: i }]; t = t + src.len() + src[0].k; }
    return t % 101;
}` + arrstructShareMain,
			want: 70, balance: true,
		},
	}
}

func TestSelfHostArrStructFieldShareX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrstructShareCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrstructshare_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: the source walked a "+
					"buffer an uncounted co-owner still holds)", tc.name, exit, tc.want)
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
