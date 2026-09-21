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

// --- Assigning a match binding out of its arm ------------------------------
//
// `cur = v`, where `v` is a match-destructured binding, leaves the value at
// rc 1 with `cur` the only owner. The assignment retains what it reads, and
// the arm gives back the reference the callee handed over.
//
// It matters out of proportion to its size: every eager adapter in
// `core/iter` — filter, map, enumerate — is written on this exact loop
// (`cur = t.1` inside a match arm), so getting it wrong costs one iterator
// state per element.
//
// It WAS wrong, and this test pinned the wrongness at rc 2 / 3 unpaired
// until #8003. The asymmetry that made it hard to read: an ordinary local is
// swept at scope exit, so the alias inc it takes has something to cancel,
// while a match binding is not — and the arm-end release that should have
// been that counterpart was refused whenever the binding was assigned out.
// Two half-mechanisms disagreeing, one treating the assignment as a move and
// the other as an alias. The repair sketched here at the time was to suppress
// the inc; the other half turned out to be the right one, because the inc is
// what makes `cur` an owner.
//
// Three readings were ruled out by measurement before that one, and each is
// worth not re-deriving:
//
//   - NOT "an assignment inside a match arm". Assigning a fresh literal in
//     the same arm was always clean; only assigning the BINDING leaked.
//   - NOT a skipped dec-on-overwrite. Instrumenting the array branch of
//     `b.assign` printed identical gates for both shapes
//     (freeEligible=true, moved=false), so the release was emitted.
//   - NOT the array grow / copy-on-write helpers, which the census's site
//     attribution pointed at first: a loop of `append` past capacity and a
//     `.with` on a shared array both measure clean.
//
// The accounting is exact because there is no enum box to blame: grouping
// the trace by allocation size shows one 32-byte block per iteration, so
// `Some([1,2,3])` does not box the Option separately and the payload array
// is the only allocation. 1 from the callee, inc'd to 2 by `cur = v`,
// arm-end release back to 1, held by cur.
//
// docs/rc-log/2026-08-30-match-binding-rebind-overretain.md has the original
// investigation; docs/rc-log/2026-09-21-a-counted-escape-is-not-an-escape.md
// has the repair.

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

// The same loop with a fresh literal rather than the binding — the control,
// and the shape the case above now matches.
const freshRebindSrc = `
function main(): i32 {
    var cur: u8[] = [0];
    var i: i32 = 0;
    while (i < 3) { cur = [1, 2, 3]; i = i + 1; }
    return __rc_get(cur);
}
`

func TestMatchBindingRebindOwnsOnceX86_64(t *testing.T) {
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
		// Solely owned, and held once.
		{"match binding", matchBindingRebindSrc, 1, 0},
		// The control it now matches.
		{"fresh literal", freshRebindSrc, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc, unpaired := runRcGetProbe(t, gcc, runner, tc.src)
			if rc != tc.wantRc {
				t.Errorf("__rc_get(cur) = %d, want %d. A 2 here is the arm-end release "+
					"going missing again while the assignment's alias inc stays, which "+
					"strands one value per iteration through every core/iter adapter",
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
