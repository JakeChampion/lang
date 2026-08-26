package e2eselfhost

import (
	"testing"
)

// --- The struct-literal FIELD share of an array-of-structs local -------------
//
// `var p: P = P { f: src, … }` where `src` is a credited `Inner[]` local. The
// construction RETAINS `src` unconditionally (the ExprStructLit ident arm in
// lower_expr, `fav_alias_inc`), so the field holds a COUNTED share — but every
// escape gate on the ARRSTRUCT credit read the bare ident as an escape and sank
// `src`'s reclaim outright. It then took the generic buffer dec, freeing the
// outer array while every element box and element array field stranded.
//
// The measurement that located it inverts the obvious reading. On the path where
// the share RUNS the census was already flat, because the holder's own field
// walk did the work; it was the path where the share did NOT run that leaked:
//
//	share taken at runtime       5 allocs / 5 frees      already clean
//	share NOT taken at runtime   4 allocs / 2 frees      88 B/round
//
// So "the share is uncounted" was the wrong diagnosis. The share was counted and
// the SOURCE had been left with no walk at all.
//
// Both releases are rc-gated, which is what makes granting this safe: the
// holder's is __struct_arr_elems_drop_<E>, which is_unique-gates the buffer, and
// the source's is emit_arrstruct_deep_free, which now gates the same way. That
// gate is a no-op for every shape that existed before — a sole owner is rc 1 —
// and the prerequisite for this one. Half the pairing would be an over-release:
// with one owner gated and the other walking statically, both sweeps free the
// buffers, at a census that reads allocs == frees at live_bytes 0.
//
// `respread` is the case that proves the credit needs its own precondition
// rather than a guard inside the walk, and it is the reason the underflow guard
// is asserted on every case here: `P { ...q, … }` copies the buffer pointer into
// a third box with NO inc, so three owners sit at rc 2. Granting the share
// without refusing that took it to exit 99 at 600 allocs, 600 frees,
// live_bytes 0 — nothing in the census dissented.
//
// `moved_ret` is the second such precondition, and it cost a red CI to find. The
// retain is MOVE-gated (#6726): where the analysis says the construction moves
// the local, the box takes over its reference and both the inc and the sweep dec
// that cancels it are dropped. This credit does not route that plain sweep dec —
// it routes emit_arrstruct_deep_free — so granting it at a moved site frees a
// buffer whose ownership left the frame. `return P { f: src, … }` is exactly
// that, because the return is src's last use.
//
// Nothing on x86-64 saw it. Both matrices, TestSelfHostRcPlanDiff, the whole
// probe corpus and all three x86-64 fixpoints were green; it surfaced as gen2
// SEGFAULTING under qemu on TestSelfHostStage2FixpointArm64/lexer — the
// whole-compiler leg, and the blind spot docs/TEST-GATES.md names. The credit
// now asks exactly the question the retain asks, so this case stays the leak it
// was, which is why it opts out of the balance assertion.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.

type arrstructShareCase struct {
	name    string
	src     string
	want    int
	balance bool // assert allocs == frees at live_bytes 0
}

const arrstructShareMain = "\nfunction main(): i32 { var t: i32 = 0; var i: i32 = 0; " +
	"while (i < 100) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

const arrstructShareDecl = `struct Inner { xs: i32[], k: i32 }
struct P { f: Inner[], n: i32 }
function mkv(i: i32): Inner[] { var o: Inner[] = []; o = o.append(Inner { xs: [i, i + 1], k: i }); return o; }
`

func arrstructShareCases() []arrstructShareCase {
	return []arrstructShareCase{
		{
			// The repro. The share is in a branch taken half the time, so the
			// rounds that skip it are the ones that leaked: 450 allocs / 350
			// frees, 4400 bytes over 100 rounds, against native's 450/450.
			name: "conditional",
			src: arrstructShareDecl + `function round(i: i32): i32 {
    var src: Inner[] = mkv(i);
    var t: i32 = 0;
    if (i % 2 == 0) { var p: P = P { f: src, n: i }; t = p.f.len() + p.f[0].k + p.n; }
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
    var src: Inner[] = mkv(i);
    var p: P = P { f: src, n: i };
    return (p.f.len() + p.f[0].k + p.n + src.len()) % 101;
}` + arrstructShareMain,
			want: 70, balance: true,
		},
		{
			// THE OVER-RELEASE GUARD. `P { ...q, … }` copies q's buffer pointer
			// with no inc, so the counted share is not the whole story and the
			// credit must be refused. 99 here is three owners at rc 2 — and the
			// census stays at 600/600, live_bytes 0, while it double-frees.
			name: "respread",
			src: arrstructShareDecl + `function round(i: i32): i32 {
    var src: Inner[] = mkv(i);
    var q: P = P { f: src, n: i };
    var p: P = P { ...q, n: i + 1 };
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
    var src: Inner[] = mkv(i);
    var t: i32 = 0;
    if (i % 2 == 0) { var p: P = P { f: src, n: i }; t = keepit(p); }
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
    var src: Inner[] = mkv(i);
    return P { f: src, n: i };
}
function round(i: i32): i32 { var p: P = hold(i); return (p.f.len() + p.f[0].k + p.n) % 101; }` + arrstructShareMain,
			want: 53,
		},
		{
			// Two same-named `src` in sibling blocks, one sharing into a holder
			// and one not. The credit is site-keyed (#7253), so the widened
			// exception cannot leak from the block that earned it into the one
			// that did not — 400/300 before, 400/400 now, and never 99.
			name: "sibling_alias",
			src: arrstructShareDecl + `function round(i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var src: Inner[] = mkv(i); var p: P = P { f: src, n: i }; t = t + p.f.len() + p.n; }
    if (i % 2 == 1) { var src: Inner[] = [Inner { xs: [i], k: i }]; t = t + src.len() + src[0].k; }
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
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

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
