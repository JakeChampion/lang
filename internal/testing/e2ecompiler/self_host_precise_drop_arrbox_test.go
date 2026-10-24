package e2ecompiler

import (
	"testing"
)

const preciseDropArrBoxDecl = `enum E { A(i32[]), B }
struct P { f: E[], n: i32 }
function mkv(i: i32): E[] { let o: E[] = []; o = o.append(E.A([i, i + 1])); return o; }
function seed(): i32 { return 7; }
function rd(src: E[], i: i32): i32 { let p: P = P { f: src, n: i }; return (p.f.len() + p.n) % 101; }
`

func preciseDropArrBoxMain(init, tail string) string {
	return `
function main(): i32 {
    ` + init + `
    let t: i32 = 0; let r: i32 = 0;
    while (r < 100) { t = t + rd(keep, r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    ` + tail + `
}`
}

func preciseDropArrBoxCases() []arrenumShareCase {
	mk := func(init, tail string) string {
		return preciseDropArrBoxDecl + preciseDropArrBoxMain(init, tail)
	}
	ret := "return t % 97;"
	return []arrenumShareCase{
		{
			name: "literal_arg_precise_drop",
			src:  mk("let keep: E[] = mkv(7);", ret),
			want: 6, balance: true,
		},
		{
			name: "call_arg_unchanged",
			src:  mk("let keep: E[] = mkv(seed());", ret),
			want: 6, balance: true,
		},
		{
			name: "const_expr_arg_unchanged",
			src:  mk("let keep: E[] = mkv(1 + 6);", ret),
			want: 6, balance: true,
		},
		{
			name: "var_arg_unchanged",
			src:  mk("let q: i32 = 7; let keep: E[] = mkv(q);", ret),
			want: 6, balance: true,
		},
		{
			name: "literal_arg_kept_live",
			src:  mk("let keep: E[] = mkv(7);", "return (t + keep.len()) % 97;"),
			want: 7, balance: true,
		},
		{
			name: "struct_array_twin",
			src: `struct Inner { xs: i32[], k: i32 }
struct P { f: Inner[], n: i32 }
function mkv(i: i32): Inner[] { let o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1], k: i }); return o; }
function rd(src: Inner[], i: i32): i32 { let p: P = P { f: src, n: i }; return (p.f.len() + p.n) % 101; }
` + preciseDropArrBoxMain("let keep: Inner[] = mkv(7);", ret),
			want: 6, balance: true,
		},
	}
}

func TestSelfHostPreciseDropArrBoxX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range preciseDropArrBoxCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "precisedroparrbox_"+tc.name, asm)
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
			if !tc.balance && live == 0 && allocs == frees {
				t.Errorf("%s: %s — pinned as LEAKING; if this now balances the pin "+
					"is stale and the row belongs to whatever closed it", tc.name, summary)
			}
		})
	}
}
