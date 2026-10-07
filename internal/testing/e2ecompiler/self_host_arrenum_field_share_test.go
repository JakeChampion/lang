package e2ecompiler

import (
	"testing"
)

// --- The struct-literal FIELD share of an array-of-enums local ---------------
//
// `let p: P = P { f: xs, … }` where `xs` is an `E[]` local: the construction
// RETAINS it, so the field holds a COUNTED share and `xs` keeps its deep
// release. Both owners' element walks are rc-gated on the buffer, so only the
// last one to die walks it. That matters more here than on the struct side
// because this walk FREES each element box rather than deccing it, so two
// owners both walking is a double free.
//
// Two shapes carry the hazard:
//
//   - `respread`: `P { ...q, … }` copies the buffer pointer into a third box,
//     so the base copy must retain the array it carries and release it through
//     the same rc-gated walk.
//   - `moved_ret`: the retain is MOVE-gated (#6726), so at a move site the box
//     takes over the local's reference and both the inc and the source's dec are
//     dropped. `return P { f: xs, … }` is that shape — the return is xs's last use.
//
// The second is the dangerous one, because NOTHING counts it. Without the gate,
// `moved_ret` measures 500 allocs / 400 frees — MORE frees than the correct
// 500/100, since a double free counts as a free — and `__rc_underflow_count()`
// stays silent, because this class frees element boxes rather than deccing them.
// The stage-2 fixpoint does not see it either: the compiler's own source has no
// enum-array moved share. `moved_uaf` below is what catches it — it reads the
// payload back after the callee returned and checks the value, and without the
// gate the self-host binary SEGFAULTS (139) where the interp exits 25.
//
// So: a wrong-ANSWER case, not a census case. Every want was confirmed against
// bin/fern -interp, never read off the self-host run under test.

type arrenumShareCase struct {
	name    string
	src     string
	want    int
	balance bool // assert allocs == frees at live_bytes 0
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
			// The repro: the share is in a branch taken half the time, so the
			// rounds that skip it are the ones that leaked. 450/350 before.
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
			// The share always runs and the source outlives it. The buffer gate is
			// what lets the two owners decide between them.
			name: "always",
			src: arrenumShareDecl + `function round(i: i32): i32 {
    let src: E[] = mkv(i);
    let p: P = P { f: src, n: i };
    return (p.f.len() + p.n + src.len()) % 101;
}` + arrenumShareMain,
			want: 69, balance: true,
		},
		{
			// The holder goes to a callee that may keep it. The source finding
			// rc > 1 simply declines.
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
			// The third holder is the spread copy, counted like the other two;
			// uncounted, this was exit 99 at 600/600, live_bytes 0.
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
			// PRECONDITION 2, the census-invisible one. Refused, so it stays the
			// leak it was — 500/100. Note that REMOVING the gate takes it to
			// 500/400, which reads as an improvement and is a double free.
			name: "moved_ret",
			src: arrenumShareDecl + `function hold(i: i32): P {
    let src: E[] = mkv(i);
    return P { f: src, n: i };
}
function round(i: i32): i32 { let p: P = hold(i); return (p.f.len() + p.n) % 101; }` + arrenumShareMain,
			want: 70,
		},
		{
			// The case that actually catches precondition 2: read the payload back
			// after the callee returned, with allocation churn in between so freed
			// memory is reused, and check the value. Without the move gate the
			// self-host binary segfaults (139) here while native and interp exit 25
			// — and both leak counters stay silent throughout.
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

// TestSelfHostArrEnumFieldShareX86_64 — a counted struct-literal field share keeps
// an array-of-enums source its rc-gated element walk, and the two shapes whose
// share count is incomplete keep refusing it.
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
			// The two refused cases stay leaks, deliberately: the source keeps no
			// walk where the share count is incomplete.
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
