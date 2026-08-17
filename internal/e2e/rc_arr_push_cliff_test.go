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

// arrPushCliffFieldBorrowSrc threads a struct-held accumulator and consults the
// container through a SCALAR-returning call each step — the shape the x86
// assembler is built from (`x86_label_off(a, name)` before the queue append).
// The call gives back an i32, so nothing that outlives it can name the
// container, and the append must still grow in place. Treating that read as a
// capture forced a full clone per append and made the self-host compiler's own
// assembly quadratic (#6911).
const arrPushCliffFieldBorrowSrc = `struct Asm { code: i32[], labels: i32[] }
function label_off(a: Asm, want: i32): i32 {
    var i: i32 = 0;
    while (i < a.labels.len()) { if (a.labels[i] == want) { return i; } i = i + 1; }
    return 0 - 1;
}
function emit(own a: Asm, v: i32): Asm {
    var t: i32 = label_off(a, v);
    a = Asm { ...a, code: a.code.append(v + t) };
    return a;
}
function main(): i32 {
    var a: Asm = Asm { code: [], labels: [1, 2, 3] };
    var i: i32 = 0;
    while (i < 200) { a = emit(a, i); i = i + 1; }
    if (a.code.len() != 200) { return 254; }
    if (a.code[7] != 6 || a.code[199] != 198 || a.code[2] != 3) { return 253; }
    return __arr_push_shared_count();
}`

// arrPushCliffFieldAliasSrc is the same loop with the container bound to a name
// that OUTLIVES the append, so the in-place grow would be observable through
// `keep` and the copy is mandatory. The paired case: the relaxation above must
// not reach a binding that can hold the container.
const arrPushCliffFieldAliasSrc = `struct Asm { code: i32[], labels: i32[] }
function grown(): i32[] {
    var c: i32[] = [];
    var i: i32 = 0;
    while (i < 5) { c = c.append(i); i = i + 1; }
    return c;
}
function emit(a: Asm, v: i32): i32 {
    var keep: Asm = a;
    a = Asm { ...a, code: a.code.append(v) };
    return a.code.len() - keep.code.len();
}
function main(): i32 {
    var a: Asm = Asm { code: grown(), labels: [] };
    if (emit(a, 9) != 1) { return 252; }
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
	if _, got := compileAndRunX86_64FreeOn(t, arrPushCliffFieldBorrowSrc); got != 0 {
		t.Errorf("x86-64 field accumulator past a scalar-returning read: "+
			"__arr_push_shared_count() = %d, want 0", got)
	}
	if _, got := compileAndRunX86_64FreeOn(t, arrPushCliffFieldAliasSrc); got != 1 {
		t.Errorf("x86-64 field accumulator with a live container alias: "+
			"__arr_push_shared_count() = %d, want 1 — the in-place grow is observable "+
			"through the alias and must have been refused", got)
	}
}

func TestArm64ArrPushCliffCounter(t *testing.T) {
	if _, got := compileAndRunArm64(t, arrPushCliffHealthySrc); got != 0 {
		t.Errorf("arm64 healthy accumulator: __arr_push_shared_count() = %d, want 0", got)
	}
	if _, got := compileAndRunArm64(t, arrPushCliffSharedSrc); got != 1 {
		t.Errorf("arm64 shared buffer: __arr_push_shared_count() = %d, want 1", got)
	}
	if _, got := compileAndRunArm64(t, arrPushCliffFieldBorrowSrc); got != 0 {
		t.Errorf("arm64 field accumulator past a scalar-returning read: "+
			"__arr_push_shared_count() = %d, want 0", got)
	}
	if _, got := compileAndRunArm64(t, arrPushCliffFieldAliasSrc); got != 1 {
		t.Errorf("arm64 field accumulator with a live container alias: "+
			"__arr_push_shared_count() = %d, want 1", got)
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
	if got := runWasm(t, arrPushCliffFieldBorrowSrc); got != 0 {
		t.Errorf("wasm field accumulator past a scalar-returning read: "+
			"__arr_push_shared_count() = %d, want 0", got)
	}
	if got := runWasm(t, arrPushCliffFieldAliasSrc); got != 1 {
		t.Errorf("wasm field accumulator with a live container alias: "+
			"__arr_push_shared_count() = %d, want 1", got)
	}
}
