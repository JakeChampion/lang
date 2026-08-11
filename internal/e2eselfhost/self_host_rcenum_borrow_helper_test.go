package e2eselfhost

import (
	"strings"
	"testing"
)

// A fresh rc-payload enum LOOP-LOCAL with no consuming match in its own block —
// passed to a helper that only matches on it, or never used at all — is reclaimed
// (#6606).
//
// Two independent gaps kept this shape uncredited, and closing either alone leaves
// it leaking:
//
//  1. `collect_fresh_rcenum_names` required a consuming `match` in the same block.
//     A local with no match at all therefore earned nothing, so `var b = Val(…)`
//     declared in a loop and never read grew unboundedly.
//
//  2. `borrowable_params_interproc` — the fixpoint the EMIT path uses, not the
//     single-pass `borrowable_params_of` the inspection passes use — read a
//     bare-ident `match (param)` scrutinee as an ESCAPE. So `head(b)` refused the
//     caller-side release, and the caller could not tell a borrow from a retain.
//     #6127 had already made the opposite argument (a match reads the tag and the
//     payload and retains neither) and wired it into the local-reclaim analyses;
//     it had never been applied to the borrowability verdict.
//
// Both compilers agreed on every exit code and on `__rc_underflow_count()`
// throughout, which is why nothing caught this: a compiler that reclaims NOTHING
// satisfies a value-and-underflow assertion perfectly. Only the byte counts move.
//
// EACH ROW IS COMPILED AT TWO ROUND COUNTS, so a per-iteration leak shows up as a
// residue that doubles with the rounds rather than as a single number needing a
// magic constant.
//
// These rows landed asserting a surviving 80-byte tail — the FINAL value, which no
// sweep reclaimed for this class. That tail is gone: the "RCENUMS:" sweep credit
// releases it, and the rows are now an exact balance. The tail was never visible to
// THIS suite's scaling assertion (a constant cannot fail it); what caught it was
// #6608's `enum-payload-still-grows`, which compares the heap mark across two
// identical calls and so sees a per-call tail directly. That is why the rows below
// pin `allocs == frees` outright now — the weaker scaling property would ratify a
// regression back to the tail.

const rcenumBorrowedSrc = `import "core/int";
enum Box { Val(i32[]), Empty }
function head(b: Box): i32 {
    match (b) { Val(xs) => { return xs[0]; }, Empty => { return 0; } }
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < ROUNDS) {
        var k: i32 = 0;
        while (k < 4) {
            var b: Box = Val([k, k + 7]);
            acc = acc + head(b);
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// The reduction: no match, no helper, no use at all. It leaked identically, which
// is what showed the missing credit was the whole story and the helper incidental.
const rcenumNeverUsedSrc = `import "core/int";
enum Box { Val(i32[]), Empty }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < ROUNDS) {
        var k: i32 = 0;
        while (k < 4) {
            var b: Box = Val([k, k + 7]);
            acc = acc + k;
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// The control that always worked — the consuming match written inline. It must
// stay at an exact balance, and must not gain a second credit now that the
// no-match branch exists beside it.
const rcenumInlineMatchSrc = `import "core/int";
enum Box { Val(i32[]), Empty }
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < ROUNDS) {
        var k: i32 = 0;
        while (k < 4) {
            var b: Box = Val([k, k + 7]);
            match (b) { Val(xs) => { acc = acc + xs[0]; }, Empty => { acc = acc + 1; } }
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

// THE HAZARD THE BORROW VERDICT EXISTS TO REFUSE. The callee's arm binds the
// payload and RETURNS it, so the caller still holds a live buffer where the drop
// would land. `param_match_binding_escapes` is what sees that — the scrutinee-is-a-
// borrow reading alone cannot, because `b` is never mentioned outside the match, so
// no walk over `b` can observe the payload leaving.
//
// THE BYTE COUNT IS THE DETECTOR HERE, not the exit code, and that was measured
// rather than assumed. Deleting `param_match_binding_escapes` takes this row from
// 401 frees to 800 and from 16000 live bytes to 40 — the enum gets released under
// its other holder — while the exit stays 5. Adding a same-shaped churn loop
// between the release and the read did NOT recycle the buffer into the answer
// either (30 both ways), so unlike #6467's string case there is no spelling of this
// shape where the exit moves. The exact stranded count below is what kills the
// mutation: 400 at 100 rounds against the mutation's 1.
const rcenumPayloadEscapesSrc = `import "core/int";
enum Box { Val(i32[]), Empty }
function take(b: Box): i32[] {
    match (b) { Val(xs) => { return xs; }, Empty => { return [0]; } }
}
function main(): i32 {
    var held: i32[] = [0, 0];
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < ROUNDS) {
        var k: i32 = 0;
        while (k < 4) {
            var b: Box = Val([k, k + 7]);
            held = take(b);
            acc = acc + held[0];
            k = k + 1;
        }
        r = r + 1;
    }
    return acc % 7;
}
`

func TestSelfHostRcEnumBorrowHelperX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)

	counts := func(t *testing.T, name, src string, rounds string) (int64, int64, int64) {
		t.Helper()
		src = strings.ReplaceAll(src, "ROUNDS", rounds)
		// These return a value, so `fern -interp` IS the oracle: releasing a
		// payload the callee handed out shows up as a wrong answer or a crash,
		// and that matters more than any byte count below.
		want := interpExit(t, interpBin, src)
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name+rounds, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != want {
			t.Fatalf("%s@%s: self-host exited %d, fern -interp exited %d — the enum drop "+
				"reached a live payload", name, rounds, exit, want)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s@%s: no leakcheck summary — FERN_LEAKCHECK did not take effect", name, rounds)
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("%s@%s: parse %q: %v", name, rounds, summary, err)
		}
		if allocs == 0 {
			t.Fatalf("%s@%s allocated nothing — the probe is not exercising the path", name, rounds)
		}
		return allocs, frees, live
	}

	// Doubling the rounds must double the allocations and the frees, and every
	// allocation must be freed at both counts. Before the borrow fix this shape was
	// 2.00x per doubling with frees=0; before the sweep credit it balanced except
	// for the final value of each block.
	for _, tc := range []struct {
		name      string
		src       string
		wantStuck int64 // allocs - frees, at BOTH round counts
	}{
		{"helper_borrows_the_enum", rcenumBorrowedSrc, 0},
		{"never_used", rcenumNeverUsedSrc, 0},
		{"inline_match_control", rcenumInlineMatchSrc, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a1, f1, l1 := counts(t, tc.name, tc.src, "100")
			a2, f2, l2 := counts(t, tc.name, tc.src, "200")
			if a2 != a1*2 {
				t.Fatalf("%s: allocs %d at 100 rounds and %d at 200 — the probe does not "+
					"scale with the loop, so it cannot separate a leak from a reclaim",
					tc.name, a1, a2)
			}
			if a1-f1 != tc.wantStuck || a2-f2 != tc.wantStuck {
				t.Errorf("%s: unfreed allocations %d at 100 rounds and %d at 200, want %d at "+
					"both. This shape leaked EVERY iteration (frees=0, 24000 bytes at 300 "+
					"rounds against 48000 at 600) because the enum earned no credit without "+
					"a consuming match, and `head(b)` read as an escape. A residue of 2 that "+
					"does NOT grow with the rounds is the separate tail case: the block's "+
					"final value, released by the \"RCENUMS:\" sweep credit",
					tc.name, a1-f1, a2-f2, tc.wantStuck)
			}
			if l1 != 0 || l2 != 0 {
				t.Errorf("%s: live_bytes %d at 100 rounds and %d at 200, want 0 at both — "+
					"every box and payload is accounted for once the sweep releases the "+
					"final value", tc.name, l1, l2)
			}
		})
	}

	// Refused, so the correct outcome is one stranded enum chain per iteration —
	// 4 inner rounds x N outer. An EXACT count, not a bound: deleting the
	// binding-escape gate leaves exactly 1, which any "some leak" assertion would
	// have accepted.
	t.Run("callee_returns_the_payload", func(t *testing.T) {
		a1, f1, _ := counts(t, "callee_returns_the_payload", rcenumPayloadEscapesSrc, "100")
		a2, f2, _ := counts(t, "callee_returns_the_payload", rcenumPayloadEscapesSrc, "200")
		if a1-f1 != 400 || a2-f2 != 800 {
			t.Errorf("callee_returns_the_payload: unfreed %d at 100 rounds and %d at 200, "+
				"want 400 and 800. The callee's arm returns the payload, so the param is not "+
				"borrowable and the enum must be stranded rather than released; without the "+
				"binding-escape gate this reads 1 and 1 — the buffer freed under its other "+
				"holder (allocs=%d, frees=%d)", a1-f1, a2-f2, a1, f1)
		}
	})
}
