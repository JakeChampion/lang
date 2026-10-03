// The rc==1 append cliff has two readings, and only one of them ranks work.
//
// `__arr_push_shared_count()` counts CROSSINGS: appends that copied a buffer
// which still had spare capacity, so the copy was bought by an extra reference
// rather than by a full buffer. It answers "did anything cross". It cannot
// answer "does it matter", and the gap between those questions is three orders
// of magnitude: a whole-module compile of checker.fern by the self-host
// compiler crosses the cliff 188 times and copies 812 bytes doing it (4-byte
// loop-depth stacks in irlower's enter_loop), while one threaded accumulator
// over 20k appends crosses ~20k times and copies 2.3 GB. Two rounds of
// accumulator work were scoped against the unweighted count and aimed at sites
// that could not have paid.
//
// `__arr_push_shared_bytes()` is the weight: oldLen * stride summed at each
// crossing, as an i64 (the quantity is arena-scale — an i32 would wrap on
// exactly the runs it exists to measure). These cases pin both readings
// together on every backend, because the pair is only useful if it agrees:
// a crossing with no bytes, or bytes with no crossing, means one of the two
// emission sites has drifted from the other.
package e2e

import (
	"fmt"
	"testing"
)

// cliffBytesCase is one driver, run twice: once returning the crossing COUNT
// and once the byte WEIGHT. Both are kept under 256 so they survive as exit
// codes on every backend (WASI clamps to [0..126) for anything it forwards,
// which these clear).
type cliffBytesCase struct {
	name string
	// body is the accumulator called 7 times as `acc = g(acc, i)`.
	g string
	// appends per call, for the length self-check.
	appends   int
	wantCount int
	wantBytes int
}

var cliffBytesCases = []cliffBytesCase{
	// Clean: the self-append never crosses, so both readings are 0. This is
	// the case that catches a bytes accumulator wired to fire on the ORDINARY
	// grow path — it would report bytes here while the count stays 0.
	{"clean_self_append", `function g(a: i32[], v: i32): i32[] { a = a.append(v); return a; }`, 1, 0, 0},

	// Crossing: an alias of the incoming buffer is read after the append, so
	// the alias genuinely observes the old contents and every append that
	// had spare capacity must copy instead: four of the seven calls on the
	// self-host's size classes, 240 bytes between them.
	{"live_alias_append", `function g(b: i32[], v: i32): i32[] { let k: i32[] = b; let r: i32[] = b.append(v); if (k.len() + 1 != r.len()) { return k; } return r.append(v + 1); }`, 2, 4, 240},
}

// src builds the driver. `probe` is the expression whose value becomes the
// exit code. The length check runs first, so a case that miscompiles reports
// 254 rather than a plausible reading.
func (c cliffBytesCase) src(probe string) string {
	wantLen := 7 * c.appends
	return fmt.Sprintf(`function f(b: i32[], v: i32): i32[] { return b.append(v); }
%s
function main(): i32 {
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < 7) { acc = g(acc, i); i = i + 1; }
    if (acc.len() != %d) { return 254; }
    return %s;
}`, c.g, wantLen, probe)
}

func (c cliffBytesCase) countSrc() string { return c.src("__arr_push_shared_count()") }
func (c cliffBytesCase) bytesSrc() string { return c.src("__arr_push_shared_bytes() as i32") }

func (c cliffBytesCase) check(t *testing.T, backend string, gotCount, gotBytes int) {
	t.Helper()
	if gotCount == 254 || gotBytes == 254 {
		t.Fatalf("%s %s: driver returned 254 — the accumulator computed the WRONG "+
			"contents; the readings below it are meaningless until that is fixed", backend, c.name)
	}
	if gotCount != c.wantCount {
		t.Errorf("%s %s: __arr_push_shared_count() = %d, want %d",
			backend, c.name, gotCount, c.wantCount)
	}
	if gotBytes != c.wantBytes {
		t.Errorf("%s %s: __arr_push_shared_bytes() = %d, want %d — the weight and the "+
			"count are emitted at the same site and must move together",
			backend, c.name, gotBytes, c.wantBytes)
	}
}

func TestX86_64ArrPushCliffBytes(t *testing.T) {
	for _, c := range cliffBytesCases {
		t.Run(c.name, func(t *testing.T) {
			_, count := compileAndRunX86_64FreeOn(t, c.countSrc())
			_, bytes := compileAndRunX86_64FreeOn(t, c.bytesSrc())
			c.check(t, "x86-64-linux", count, bytes)
		})
	}
}

// The interpreter has no refcounts and copies nothing, so it crosses no cliff
// and copies no bytes — both builtins report 0 there. That is what lets a
// program that asserts "count == 0" pass under -interp for the right reason;
// this pins the same contract for the weight.
func TestInterpArrPushCliffBytesZero(t *testing.T) {
	c := cliffBytesCases[1] // the case that DOES cross on the compiled backends
	if got := runInterpExit(t, c.countSrc()); got != 0 {
		t.Errorf("-interp __arr_push_shared_count() = %d, want 0", got)
	}
	if got := runInterpExit(t, c.bytesSrc()); got != 0 {
		t.Errorf("-interp __arr_push_shared_bytes() = %d, want 0", got)
	}
}
