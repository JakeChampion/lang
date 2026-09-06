package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/e2eharness"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// --- The core/iter adapters strand the pair-form payload ---------------------
//
// Distinct from the projection leak next door in
// tuple_projection_leak_test.go, which is fixed. That one was a routing
// defect: the overwrite dec reached the non-reclaiming __fern_rc_dec.
// This one is a release that is never emitted at all.
//
// `ArrayIter.next` returns `Option[(T, Self)]`, which is PAIR-FORM — the
// enum has no box, so the tuple the arm binds to `t` is the only heap
// value in play besides the iterator itself. Nothing releases it: traced,
// the tuple boxes carry no inc, no dec, no uniqueness test and no free.
// Everything else follows from that one omission. `cur = t.1` retains the
// fresh iterator, so it sits at rc 2 with the second reference held by the
// tuple nobody drops; at the next overwrite its own drop finds rc 2, is
// not unique, and flat-decs to 1. Stranded one reference short.
//
// The whole combinator library is written on this shape — sum, count,
// fold, map, filter and take all do `Some(t) => { …; cur = t.1; }` — so
// this is the per-element cost of iterating anything, not two adapters.
//
// `reclaimablePairFormPayload` is the machinery for exactly this and
// refuses. Three of its four conditions pass, the binding included; only
// freshness fails, because `returnsNoParamEscape` asks whether anything
// REACHABLE FROM the result aliases a parameter, where releasing the
// buffer needs only whether the result POINTER ITSELF is fresh.
// `next` answers no to the first and yes to the second.
//
// FIXED. freshPairFormEnumResultType now asks whether the returned
// payload BOX is the callee's own, instead of whether anything reachable
// from the result aliases a parameter — and it identifies a user function
// by map membership rather than a "__" name prefix, which had been
// refusing every concrete method along with the builtins it was aimed at.
// The release itself did not change and is still the deep
// emitOwnedSlotDrop: the tuple owns the fresh iterator at rc 1, `cur =
// t.1` retains it to 2, and the deep drop takes it to 1 held by cur. A
// SHALLOW free would leave it at 2 and still leak, which is why the
// obvious analogy to emitOwnedConsumingArmDrop is the wrong one here.
//
// These rows were 15 / 15 / 15. They now pin the REPAIR: a regression in
// the credited case fails here, and so does one that starts crediting a
// shape it should not — the boundary row is in the table rather than
// beside it.
//
// docs/rc-log/2026-08-30-iter-adapter-pair-form-payload.md.

const (
	iterFilterSrc = `
import "core/iter" as iter;
function main(): i32 {
    var xs: i32[] = [5, 2, 8, 1, 4, 9, 6];
    return iter.filter(iter.of(xs), (x: i32): boolean => { return x % 2 == 0; }).len();
}
`
	iterMapSrc = `
import "core/iter" as iter;
function main(): i32 {
    var xs: i32[] = [5, 2, 8, 1, 4, 9, 6];
    return iter.map(iter.of(xs), (x: i32): i32 => { return x + 1; }).len();
}
`
	iterSumSrc = `
import "core/iter" as iter;
function main(): i32 {
    var xs: i32[] = [5, 2, 8, 1, 4, 9, 6];
    return iter.sum(iter.of(xs));
}
`
	// The SOUNDNESS boundary, and the one that matters most here. The
	// payload is a fresh tuple, so the reclaim credits it — but that
	// tuple CAPTURES a parameter's array. Releasing it deep-drops the
	// tuple and decrements the array, which is only correct because the
	// construction that stored it retained it. If that assumption is
	// ever false this frees the caller's array three iterations early,
	// so the test asserts the RESULT as well as the leak count: a
	// premature free need not crash to be wrong.
	tupleCapturesParamSrc = `
function wrap(xs: u8[]): Option[(i32, u8[])] {
    return Some((1, xs));
}
function main(): i32 {
    var xs: u8[] = [1, 2, 3];
    var n = 0;
    var i = 0;
    while (i < 3) {
        match (wrap(xs)) {
            Some(t) => { n = n + t.1.len(); },
            None => { n = n + 100; },
        }
        i = i + 1;
    }
    return n + xs.len();
}
`
	// The other boundary: no match arm binds the pair, so there is no
	// pair-form payload to strand.
	iterOfOnlySrc = `
import "core/iter" as iter;
function main(): i32 {
    var xs: i32[] = [5, 2, 8, 1, 4, 9, 6];
    var it = iter.of(xs);
    return it.idx + xs.len();
}
`
)

func TestIterAdapterPairFormPayloadLeaksX86_64(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs; not a -short test")
	}
	gcc, runner, ok := e2eharness.LookupX86_64Tooling()
	if !ok {
		t.Skip("no x86-64 toolchain")
	}
	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		// Was 15 each: 7 elements x (one ArrayIter + one tuple) + the
		// source array the stranded iterators kept alive. Identical
		// across all three because it was the iteration that leaked —
		// `sum` builds no output array at all and still paid 15.
		{"filter", iterFilterSrc, 0},
		{"map", iterMapSrc, 0},
		{"sum", iterSumSrc, 0},
		// Fresh payload capturing a parameter — released, and correctly.
		{"tuple captures a param", tupleCapturesParamSrc, 0},
		// The boundary: binds no pair, so there is nothing to release.
		{"iter.of alone", iterOfOnlySrc, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := unpairedAllocs(t, gcc, runner, tc.src)
			if got != tc.want {
				t.Errorf("%d unpaired allocation(s), want %d — an iterator's next "+
					"returns a freshly allocated payload box, and the match arm that "+
					"binds it is meant to release it. A non-zero count here means "+
					"freshPairFormEnumResultType stopped crediting that box, and every "+
					"combinator went back to stranding two allocations per element",
					got, tc.want)
			}
		})
	}
}

// TestPairFormPayloadCapturingParamStaysCorrectX86_64 checks the value, not
// just the allocation count. The reclaim admitted by
// findReturnsFreshPairPayload deep-drops a fresh payload box that may hold a
// counted reference to a PARAMETER's heap; that is sound only because the
// construction retained it. A regression there frees the caller's array while
// it is still in use, which shows up as a wrong answer well before it shows up
// as a crash.
func TestPairFormPayloadCapturingParamStaysCorrectX86_64(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs; not a -short test")
	}
	gcc, runner, ok := e2eharness.LookupX86_64Tooling()
	if !ok {
		t.Skip("no x86-64 toolchain")
	}
	// 3 iterations x len 3, plus the array's own len, all after the
	// payload holding that array has been released three times.
	const want = 12
	if got := runForExit(t, gcc, runner, tupleCapturesParamSrc); got != want {
		t.Errorf("exit %d, want %d — the array a released payload captured was "+
			"freed or corrupted while main still held it", got, want)
	}
}

// runForExit compiles src for x86-64 and returns the program's exit status.
// Unlike unpairedAllocs it builds WITHOUT the heap tracer: the question here
// is whether the program computes the right answer, not what it allocated.
func runForExit(t *testing.T, gcc string, runner []string, src string) int {
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
	asm, err := emitNoTracer(prog, info)
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
	_, _, exit := runSplit(t, cmd)
	if exit == -1 {
		t.Fatal("the program was killed by a signal")
	}
	return exit
}

// emitNoTracer is emitWithTracer with the heap tracer OFF — reclaim enabled,
// no rctrace lines. It shares that helper's mutex because both flags are
// process-global.
func emitNoTracer(prog *ast.Program, info *checker.Info) (string, error) {
	emitTracerMu.Lock()
	defer emitTracerMu.Unlock()
	prevFree, prevTrace := ast.RcFreeEnabled, ast.RcTrace
	defer func() { ast.RcFreeEnabled, ast.RcTrace = prevFree, prevTrace }()
	ast.RcFreeEnabled, ast.RcTrace = true, false
	return x86_64.Emit(prog, info)
}
