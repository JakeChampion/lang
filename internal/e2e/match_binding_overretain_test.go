package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/e2eharness"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// --- Assigning a match binding over-retains it -------------------------------
//
// `cur = v`, where `v` is a match-destructured binding, leaves the value
// with a refcount of 2 where it should be 1. The box then survives the
// dec-on-overwrite and leaks, one per iteration.
//
// THIS TEST PINS A BUG. The refcount it asserts is the wrong one; a fix
// makes it fail, which is the point — the same rule the conformance leak
// census pin follows, where a fixture leaking LESS also fails so the
// improvement gets recorded rather than absorbed.
//
// It matters out of proportion to its size: every eager adapter in
// `core/iter` — filter, map, enumerate — is written on this exact loop
// (`cur = t.1` inside a match arm), so an iterator pipeline leaks one
// iterator state per element. Measured: `iter.filter(iter.of(xs), λ)`
// loses 11 allocations of 14, `iter.map` 7 of 10, `iter.of` alone none.
//
// Three readings were ruled out by measurement before this one, and each
// is worth not re-deriving:
//
//   - NOT "an assignment inside a match arm". Assigning a fresh literal
//     in the same arm is clean (0 unpaired of 11); only assigning the
//     BINDING leaks.
//   - NOT a skipped dec-on-overwrite. Instrumenting the array branch of
//     `b.assign` prints identical gates for the leaking and clean shapes
//     (freeEligible=true, moved=false in both), so the release is
//     emitted.
//   - NOT the array grow / copy-on-write helpers, which the census's
//     site attribution pointed at first: a loop of `append` past
//     capacity and a `.with` on a shared array both measure clean.
//
// The lowered IR differs by exactly ONE op — an `rc.inc` on the
// `cur = v` assignment — and that inc is NOT the bug. An ordinary local
// aliased in the same position gets the same inc and is balanced:
// `while { var other = […]; cur = other }` and the same inside an `if`
// both measure rc 1 and 0 unpaired. `computeMovedLocals` explains it:
// move-on-alias fires only for a top-level `y = x` at x's last
// occurrence, so "aliases inside control flow keep their inc" — working
// as designed.
//
// The asymmetry is the finding: an ordinary local is swept at scope
// exit, so the inc it takes has something to cancel. A MATCH BINDING is
// not.
//
// And only assigning the binding OUT breaks it. Matching and merely
// using it — `total = total + v.len()` — or ignoring it entirely both
// measure 0 unpaired of 3.
//
// There is no enum box to blame: grouping the trace by allocation size
// shows one 32-byte block per iteration and nothing else, so
// `Some([1,2,3])` does not box the Option separately and the payload
// array is the only allocation. That makes the accounting exact. Used
// only: 1, never inc'd, arm-end release takes it to 0. Assigned out: 1,
// inc'd to 2 by `cur = v`, next iteration's dec-on-overwrite back to 1,
// never 0.
//
// So the arm-end release is ABSENT when the binding is assigned out,
// while the alias-inc is still emitted — two half-mechanisms
// disagreeing, one treating the assignment as a move and the other as an
// alias. Not the documented safe leak for ineligible enums either:
// `enumRcPayloadsEligible` excludes only enums transitively containing a
// Map, and `ast.EnumRcPayloads` is on.
//
// ONE ITERATION IS ENOUGH. Varying how many times the loop runs gives
// __rc_get(cur) = 2 at one, two and three iterations (allocations 2, 3,
// 4). The count is already wrong after a SINGLE arm execution, so the
// cross-iteration dec-on-overwrite is a red herring — it only ever takes
// the count from 2 back to 1. The alias-inc has no counterpart inside
// the arm.
//
// Suppressing that inc is the repair to try: `bindingSlotScoped` returns
// a restore closure that puts the name back after the arm, so `v` is
// unreadable afterwards and there is no later path to strand — which is
// what `computeMovedLocals`' top-level guard exists to prevent. But the
// same function REUSES an existing slot of matching shape rather than
// allocating per arm, so the slot outlives the name and is shared across
// iterations, and whether a move-marking is sound against a slot the
// exit sweep may also touch is exactly what those guards exist to
// answer. Not answered here.
//
// docs/rc-log/2026-08-30-match-binding-rebind-overretain.md.

const matchBindingRebindSrc = `
function pick(n: i32): Option[u8[]] {
    if (n < 3) { return Some([1, 2, 3]); }
    return None;
}
function main(): i32 {
    var cur: u8[] = [0];
    var i: i32 = 0;
    var go: boolean = true;
    while (go) {
        match (pick(i)) {
            Some(v) => { cur = v; i = i + 1; },
            None => { go = false; },
        }
    }
    return __rc_get(cur);
}
`

// The same loop with a fresh literal rather than the binding — the
// control, and the shape the fix should make the case above match.
const freshRebindSrc = `
function main(): i32 {
    var cur: u8[] = [0];
    var i: i32 = 0;
    while (i < 3) { cur = [1, 2, 3]; i = i + 1; }
    return __rc_get(cur);
}
`

func TestMatchBindingRebindOverRetainsX86_64(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs; not a -short test")
	}
	gcc, runner, ok := e2eharness.LookupX86_64Tooling()
	if !ok {
		t.Skip("no x86-64 toolchain")
	}
	for _, tc := range []struct {
		name     string
		src      string
		wantRc   int
		unpaired int
	}{
		// The bug: solely owned, but held twice.
		{"match binding", matchBindingRebindSrc, 2, 3},
		// The control: correct.
		{"fresh literal", freshRebindSrc, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc, unpaired := runRcGetProbe(t, gcc, runner, tc.src)
			if rc != tc.wantRc {
				t.Errorf("__rc_get(cur) = %d, want %d. If this is now 1 for the match "+
					"binding, the over-retain is FIXED — update this pin and the "+
					"conformance leak census, which should improve with it",
					rc, tc.wantRc)
			}
			if unpaired != tc.unpaired {
				t.Errorf("%d unpaired allocation(s), want %d", unpaired, tc.unpaired)
			}
		})
	}
}

// runRcGetProbe compiles src with the heap tracer on, runs it, and
// returns main's result (an `__rc_get`) and the unpaired alloc count.
func runRcGetProbe(t *testing.T, gcc string, runner []string, src string) (int, int) {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("fold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := emitWithTracer(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	asmPath, binPath := filepath.Join(dir, "p.s"), filepath.Join(dir, "p")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
	}
	_, stderr, exit := runSplit(t, cmd)
	live := map[string]int{}
	for _, line := range strings.Split(stderr, "\n") {
		m := rcTraceLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if m[1] == "a" {
			live[m[2]]++
		} else {
			live[m[2]]--
		}
	}
	n := 0
	for _, c := range live {
		if c > 0 {
			n += c
		}
	}
	return exit, n
}
