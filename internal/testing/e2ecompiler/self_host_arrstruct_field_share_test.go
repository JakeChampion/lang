package e2ecompiler

import (
	"testing"
)

// --- The struct-literal FIELD share of an array-of-structs local -------------
//
// `let p: P = P { f: src, … }` where `src` is an `Inner[]` local. The
// construction retains `src`, so the field holds a counted share and the source
// keeps its own release. Both releases are rc-gated, so whichever drops last
// finds rc 1 and walks every element box and element array field. A source
// left with no walk leaks on the path where the share does NOT run, which is
// what `conditional` measures.
//
// `respread` is why the underflow guard is asserted on every case:
// `P { ...q, … }` is a third holder of the buffer, and an uncounted copy there
// is a double free at a census that reads allocs == frees at live_bytes 0.
//
// `moved_ret` returns the construction, so it MOVES `src` rather than retaining
// it (#6726); it asserts the answer only, not the balance.
//
// Every want was confirmed against bin/fern -interp, never read off the
// self-host run.

type arrstructShareCase struct {
	name    string
	src     string
	want    int
	balance bool // assert allocs == frees at live_bytes 0
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
			// The share is in a branch taken half the time, so the rounds that
			// skip it exercise the source's own release.
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
			// The share always runs and the source outlives it. Clean before this
			// change too — the holder's rc-gated field walk was doing the work —
			// and it must stay clean now that the source walks as well, which is
			// exactly what the buffer gate decides between them.
			name: "always",
			src: arrstructShareDecl + `function round(i: i32): i32 {
    let src: Inner[] = mkv(i);
    let p: P = P { f: src, n: i };
    return (p.f.len() + p.f[0].k + p.n + src.len()) % 101;
}` + arrstructShareMain,
			want: 70, balance: true,
		},
		{
			// THE OVER-RELEASE PROBE. `P { ...q, … }` is a third counted holder
			// of q's buffer, released by the same rc-gated walk as the other two.
			// 99 here is the uncounted copy of old: three owners at rc 2, a
			// census flat at 600/600, live_bytes 0, and a double free.
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
			// The holder is handed to a callee that may keep it. The share is
			// still counted, and the source's walk is still gated, so the source
			// finding rc > 1 simply declines — no leak, no double free. 450/350
			// before, 450/450 now.
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
			// THE MOVE GUARD. The holder is RETURNED, so the share is src's last
			// use and the construction MOVES rather than retains — no inc, and
			// the sweep dec elided with it. A credit granted here deep-frees a
			// buffer the returned struct owns: on x86-64 that reads as a plain
			// leak, and on the arm64 stage-2 fixpoint it segfaulted gen2.
			// Refused, so this stays the leak it was before the widening.
			name: "moved_ret",
			src: arrstructShareDecl + `function hold(i: i32): P {
    let src: Inner[] = mkv(i);
    return P { f: src, n: i };
}
function round(i: i32): i32 { let p: P = hold(i); return (p.f.len() + p.f[0].k + p.n) % 101; }` + arrstructShareMain,
			want: 53,
		},
		{
			// Two same-named `src` in sibling blocks, one sharing into a holder
			// and one not. The credit is site-keyed (#7253), so the widened
			// exception cannot leak from the block that earned it into the one
			// that did not — 400/300 before, 400/400 now, and never 99.
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

// TestSelfHostArrStructFieldShareX86_64 — a counted struct-literal field share
// keeps the source its rc-gated element walk, and the shapes where the share
// count is incomplete keep refusing it.
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
			// Every case balances except the one the move gate refuses, where
			// the source correctly keeps no walk at all.
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
