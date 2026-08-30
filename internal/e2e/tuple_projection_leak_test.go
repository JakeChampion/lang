package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/e2eharness"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// --- Projecting a pointer field out of a match binding leaks its box ---------
//
// This is the leak the `core/iter` adapters actually hit, and it is NOT
// the match-binding over-retain pinned in
// match_binding_overretain_test.go. The two have different signatures:
//
//	Some(v) => cur = v      cur's payload ends at refcount 2
//	Some(t) => cur = t.1    cur's payload ends at 1 (correct), and the
//	                        PROJECTED ARRAY still leaks, one per call
//
// `filter`, `map` and `enumerate` are all written on the second shape —
// `cur = t.1` inside a match arm, where `t` is the (value, next-iterator)
// pair — so an iterator pipeline drops one tuple box per element.
// Measured: `iter.filter(iter.of(xs), λ)` loses 11 allocations of 14,
// `iter.map` 7 of 10, `iter.of` alone none.
//
// What triggers it is narrow, and the variants below pin the boundary:
// only projecting an RC-TRACKED field out of the binding leaks. Leaving
// the binding unused is clean, projecting just the scalar field is
// clean, and a tuple with no pointer fields at all is clean.
//
// Attribution comes from the heap tracer's alloc sites: three of the
// four leaked boxes come from one site inside `step`, one per call.
//
// WHICH object that is was established by SIZE, not by reading the
// addresses in order — `step` allocates two 32-byte blocks per call and
// the ordering argument gets it backwards. Growing the array literal
// from 3 elements to 20 moves one site to 48 bytes and leaves the other
// at 32; a two-field tuple's box does not depend on the array's length,
// so the one that grew is the ARRAY, and that is the one that leaks. The
// tuple box is freed every time.
//
// ROOT CAUSE: the wrong dec helper. With `FERN_RC_TRACE`'s inc/dec
// events paired against its alloc/free ones (the a/f pointers name the
// block, i/d the object 16 bytes above), the leaked arrays read
// `alloc inc dec dec` — the counts BALANCE and reach zero, and the
// block is still not freed.
//
// `emitRcDecRuntime`'s own doc says why: "on rc == 1 the helper still
// decrements to 0 instead of calling a type-specific drop handler +
// freelist push. The bump allocator leaks." `__fern_rc_dec` does not
// reclaim; `__fern_arr_dec` does. And the leaking shape emits
// `rc.dec __fern_rc_dec` twice with no `__fern_arr_dec`, while both
// clean shapes emit neither.
//
// So this is a ROUTING defect, not an accounting one: a projected
// array-typed value is released through the generic helper. Nothing
// needs to move an inc or add a dec, which is what the attempt that
// segfaulted unidiff did.
//
// The gate that routes it is `freeEligible`. Instrumenting `b.assign`'s
// array branch: `cur = t.1` leaves cur NOT free-eligible and emits the
// generic dec, while `cur = v` leaves it eligible and emits
// `__fern_arr_dec`. The conservatism is deliberate —
// `computeFreeEligible` excludes borrowed-derived locals because only
// the owner frees, and the fallback dec cannot double-free. Safe, and
// leaky.
//
// So the repair is not at the dec site either: it is to establish that a
// projection of an owned value is itself owned, so cur stays eligible.
// That is an ownership-inference question (#7786).
//
// THIS TEST PINS A BUG. A fix makes it fail, which is the point — the
// same rule the conformance leak census follows.
//
// docs/rc-log/2026-08-30-match-binding-rebind-overretain.md.

const tupleProjLeakSrc = `
function step(n: i32): Option[(i32, u8[])] {
    if (n < 3) { return Some((n + 1, [1, 2, 3])); }
    return None;
}
function main(): i32 {
    var cur: u8[] = [0];
    var i: i32 = 0;
    var go: boolean = true;
    while (go) {
        match (step(i)) {
            Some(t) => { i = t.0; cur = t.1; },
            None => { go = false; },
        }
    }
    return cur.len();
}
`

// The three clean neighbours. Each differs from the case above by one
// thing, so together they say what the trigger is rather than just that
// there is one.
const (
	tupleProjBindingUnusedSrc = `
function step(n: i32): Option[(i32, u8[])] {
    if (n < 3) { return Some((n + 1, [1, 2, 3])); }
    return None;
}
function main(): i32 {
    var i: i32 = 0; var go: boolean = true;
    while (go) { match (step(i)) { Some(t) => { i = i + 1; }, None => { go = false; }, } }
    return i;
}
`
	tupleProjScalarOnlySrc = `
function step(n: i32): Option[(i32, u8[])] {
    if (n < 3) { return Some((n + 1, [1, 2, 3])); }
    return None;
}
function main(): i32 {
    var i: i32 = 0; var go: boolean = true;
    while (go) { match (step(i)) { Some(t) => { i = t.0; }, None => { go = false; }, } }
    return i;
}
`
	tupleProjNoPointersSrc = `
function step(n: i32): Option[(i32, i32)] {
    if (n < 3) { return Some((n + 1, 7)); }
    return None;
}
function main(): i32 {
    var i: i32 = 0; var go: boolean = true;
    while (go) { match (step(i)) { Some(t) => { i = t.0; }, None => { go = false; }, } }
    return i;
}
`
)

func TestTupleProjectionFromMatchBindingLeaksX86_64(t *testing.T) {
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
		// The bug: the projected array is stranded, one per call, plus
		// main's initial value.
		{"pointer field projected", tupleProjLeakSrc, 4},
		// The boundary.
		{"binding unused", tupleProjBindingUnusedSrc, 0},
		{"scalar field only", tupleProjScalarOnlySrc, 0},
		{"tuple has no pointers", tupleProjNoPointersSrc, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := unpairedAllocs(t, gcc, runner, tc.src)
			if got != tc.want {
				t.Errorf("%d unpaired allocation(s), want %d. If this dropped to 0 for "+
					"\"pointer field projected\", the tuple-box leak is FIXED — update "+
					"this pin and regenerate the conformance leak census, which should "+
					"improve with it",
					got, tc.want)
			}
		})
	}
}

// unpairedAllocs compiles src with the heap tracer on, runs it, and
// counts allocations that never got a matching free.
func unpairedAllocs(t *testing.T, gcc string, runner []string, src string) int {
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
	if exit == -1 {
		t.Fatal("the program was killed by a signal")
	}
	n, _, err := pairRcTrace(stderr)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
