package e2ecompiler

import (
	"testing"
)

const arrstructCountedDecl = `struct Inner { xs: i32[], k: i32 }
struct P { f: Inner[], n: i32 }
struct Q { e: Inner, n: i32 }
function mkv(i: i32): Inner[] { let o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1], k: i }); return o; }
function seed(): i32 { return 7; }
`

func arrstructCountedMain(use string) string {
	return `
function main(): i32 {
    let keep: Inner[] = mkv(seed());
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + ` + use + `; r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
}

func arrstructCountedCases() []arrenumShareCase {
	mk := func(decls, use string) string {
		return arrstructCountedDecl + decls + arrstructCountedMain(use)
	}
	return []arrenumShareCase{
		{
			name: "counted_store",
			src: mk(`function rd(src: Inner[], i: i32): i32 { let p: P = P { f: src, n: i }; return (p.f.len() + p.n) % 101; }`,
				"rd(keep, r)"),
			want: 6, balance: true,
		},
		{
			name: "callee_returns_param",
			src: mk(`@noinline function rd(src: Inner[], i: i32): Inner[] { return src; }`,
				"rd(keep, r).len()"),
			want: 3,
		},
		{
			name: "callee_extracts_element",
			src: mk(`function rd(src: Inner[], i: i32): i32 { let e: Inner = src[0]; return e.xs.len() + i; }`,
				"rd(keep, r)"),
			want: 9,
		},
		{
			name: "callee_stores_element",
			src: mk(`function rd(src: Inner[], i: i32): i32 { let q: Q = Q { e: src[0], n: i }; return q.e.xs.len() + q.n; }`,
				"rd(keep, r)"),
			want: 9,
		},
	}
}

func TestSelfHostArrStructCountedParamX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrstructCountedCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrstructcounted_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 139 = it read freed memory)",
					tc.name, exit, tc.want)
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
