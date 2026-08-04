// __fern_arr_push_grow's in-place fast path requires rc == 1. That threshold
// is a performance CORRECTNESS boundary, and until now it had no diagnostic:
// one stray retain anywhere upstream — a return-transfer inc on an `own`
// param, a consumed-param entry retain, a caller-side alias inc — makes every
// append in a threaded accumulator copy the whole buffer, and the program
// stays CORRECT while going quadratic. Three regressions of exactly that shape
// landed in a single PR's history and were each discovered somewhere else
// entirely, as an arena exhaustion or a CI OOM, days after the commit that
// caused them.
//
// __arr_push_shared_count() reports it directly: the number of appends that
// copied a buffer which still had SPARE CAPACITY, so the copy was bought by an
// extra reference rather than by a full buffer. Zero on a healthy run — which
// is what makes it usable as an assertion rather than a profiling curiosity.
//
// Both halves are pinned here. A counter that never fires would pass the
// healthy case forever.
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// arrPushCliffHealthySrc threads an accumulator through a borrowed param and
// hands it back — the shape every byte-emitter in the self-host compiler is
// built from. Nothing else holds the buffer, so every append after the first
// grow must mutate in place: the cliff is never crossed.
const arrPushCliffHealthySrc = `function step(acc: i32[], v: i32): i32[] { return acc.append(v); }
function main(): i32 {
    var acc: i32[] = [];
    var i: i32 = 0;
    while (i < 200) { acc = step(acc, i); i = i + 1; }
    if (acc.len() != 200) { return 254; }
    if (acc[7] != 7 || acc[199] != 199) { return 253; }
    return __arr_push_shared_count();
}`

// arrPushCliffSharedSrc crosses the cliff exactly once, deliberately. The
// first loop grows the buffer to cap 8 / len 5, so it has room to spare; `b`
// then takes a second reference; the append that follows therefore CANNOT
// mutate in place despite the spare capacity, and must copy. Reading b and c
// afterwards keeps both live and proves the copy actually happened — if the
// append had mutated in place, b would see length 6.
const arrPushCliffSharedSrc = `function main(): i32 {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < 5) { a = a.append(i); i = i + 1; }
    var b: i32[] = a;
    var c: i32[] = a.append(99);
    if (b.len() != 5 || c.len() != 6) { return 250; }
    if (c[5] != 99 || b[4] != 4) { return 251; }
    return __arr_push_shared_count();
}`

func TestX86_64ArrPushCliffCounter(t *testing.T) {
	if _, got := compileAndRunX86_64FreeOn(t, arrPushCliffHealthySrc); got != 0 {
		t.Errorf("x86-64 healthy accumulator: __arr_push_shared_count() = %d, want 0 — "+
			"a threaded accumulator is copying its whole buffer per append (the rc==1 cliff)", got)
	}
	if _, got := compileAndRunX86_64FreeOn(t, arrPushCliffSharedSrc); got != 1 {
		t.Errorf("x86-64 shared buffer: __arr_push_shared_count() = %d, want 1 — "+
			"the counter is not reporting a copy that spare capacity could have avoided", got)
	}
}

func TestArm64ArrPushCliffCounter(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrPushCliffHealthySrc); got != 0 {
		t.Errorf("arm64 healthy accumulator: __arr_push_shared_count() = %d, want 0", got)
	}
	if _, got := compileAndRunArm64(t, arrPushCliffSharedSrc); got != 1 {
		t.Errorf("arm64 shared buffer: __arr_push_shared_count() = %d, want 1", got)
	}
}

func TestWASMArrPushCliffCounter(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, arrPushCliffHealthySrc); got != 0 {
		t.Errorf("wasm healthy accumulator: __arr_push_shared_count() = %d, want 0", got)
	}
	if got := runWasm(t, arrPushCliffSharedSrc); got != 1 {
		t.Errorf("wasm shared buffer: __arr_push_shared_count() = %d, want 1", got)
	}
}
