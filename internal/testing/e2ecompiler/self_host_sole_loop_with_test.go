package e2ecompiler

import (
	"strings"
	"testing"
)

// soleLoopWithCase is one program, the function `fn` whose `with` writes it
// pins, and whether those writes may drop their uniqueness test: `proved`
// when the array the loop writes is one only this frame ever holds, so the
// write is in place without asking, and false where another holder may see
// the box and the test must stay.
type soleLoopWithCase struct {
	name   string
	fn     string
	proved bool
	src    string
}

var soleLoopWithCases = []soleLoopWithCase{
	// The fill loop the compiler's own tables are built with: a fresh
	// zero-filled array, written once per iteration through the loop's phi.
	{"fill-fresh", "fill", true, `@noinline
function fill(n: i32): i32[] {
    let out: i32[] = __alloc_i32(n);
    let i: i32 = 0;
    while (i < n) {
        out = out.with(i, 0 - 1 - i);
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    let a: i32[] = fill(40);
    return (a[0] + a[39] + 100) % 101;
}`},
	// Reading an element or the length of the box shares nothing.
	{"fill-reads-back", "prefix", true, `@noinline
function prefix(n: i32): i32[] {
    let out: i32[] = __alloc_i32(n);
    let i: i32 = 1;
    while (i < out.len()) {
        out = out.with(i, out[i - 1] + i);
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    let a: i32[] = prefix(10);
    return a[9];
}`},
	// Two loops in a row over one box, the second's phi fed by the first's.
	{"two-loops", "twice", true, `@noinline
function twice(n: i32): boolean[] {
    let seen: boolean[] = __alloc_bool(n);
    let i: i32 = 0;
    while (i < n) {
        if (i % 3 == 0) {
            seen = seen.with(i, true);
        }
        i = i + 1;
    }
    i = 0;
    while (i < n) {
        seen = seen.with(i, !seen[i]);
        i = i + 2;
    }
    return seen;
}
function main(): i32 {
    let s: boolean[] = twice(12);
    let c: i32 = 0;
    for b in s { if (b) { c = c + 1; } }
    return c;
}`},
	// A second name held across the write: the write must copy, and the kept
	// name still reads the value before it.
	{"aliased-in-loop", "alias", false, `@noinline
function alias(n: i32): i32 {
    let out: i32[] = __alloc_i32(n);
    let keep: i32[] = out;
    let i: i32 = 0;
    while (i < n) {
        out = out.with(i, i + 1);
        keep = out;
        out = out.with(i, 0);
        i = i + 1;
    }
    return keep[n - 1] * 10 + out[n - 1];
}
function main(): i32 {
    return alias(5);
}`},
	// Lent to a call between writes: the callee could keep it, so the test stays.
	{"lent-to-call", "lent", false, `@noinline
function total(xs: i32[]): i32 {
    let t: i32 = 0;
    for x in xs { t = t + x; }
    return t;
}
@noinline
function lent(n: i32): i32 {
    let out: i32[] = __alloc_i32(n);
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        out = out.with(i, i);
        t = t + total(out);
        i = i + 1;
    }
    return t % 101;
}
function main(): i32 {
    return lent(12);
}`},
	// Stored into a record the loop then writes past: the record keeps the
	// value it was built with.
	{"stored-in-record", "store", false, `struct Snap { xs: i32[] }
@noinline
function store(n: i32): i32 {
    let out: i32[] = __alloc_i32(n);
    let snap: Snap = Snap { xs: out };
    let i: i32 = 0;
    while (i < n) {
        snap = Snap { xs: out };
        out = out.with(i, 7);
        i = i + 1;
    }
    return snap.xs[n - 1] * 10 + out[n - 1];
}
function main(): i32 {
    return store(4);
}`},
	// A parameter's box may have other holders in the caller.
	{"parameter", "fill_in", false, `@noinline
function fill_in(own xs: i32[]): i32[] {
    let i: i32 = 0;
    while (i < xs.len()) {
        xs = xs.with(i, 3);
        i = i + 1;
    }
    return xs;
}
function main(): i32 {
    let a: i32[] = __alloc_i32(4);
    let b: i32[] = a;
    let c: i32[] = fill_in(a);
    return b[0] * 10 + c[3];
}`},
}

// Each pinned function is @noinline: called once, it would otherwise be
// spliced into main and have no body of its own to count in.
//
// TestSelfHostSoleLoopWithX86_64 pins the uniqueness proof behind a `with`
// write in a loop (ssaunits.sole_boxes): an array a zero-filled allocation or
// a `with` makes, carried around a loop, read, written and returned but never
// lent, stored or retained, is written in place with no test, and every other
// shape keeps the test. Each program's exit is the interpreter's, run under
// the sanitizer, so a proof that let a shared box be written in place shows
// up as the wrong answer as well as in the count.
func TestSelfHostSoleLoopWithX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range soleLoopWithCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureEnv(t, runner, driverBin, []byte(tc.src),
				[]string{"PATH=/usr/bin:/bin", "FERN_STRICT_IR=1", "FERN_SANITIZE=1"})
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			tests := rcIsUniqueSites(emittedFn(t, string(asm), tc.fn))
			if tc.proved && tests != 0 {
				t.Errorf("%s carries %d uniqueness test(s), want none: its array is one only this frame holds", tc.fn, tests)
			}
			if !tc.proved && tests == 0 {
				t.Errorf("%s carries no uniqueness test, want one: another holder may see its array", tc.fn)
			}
			bin := buildBin(t, gcc, dir, "slw_"+tc.name, string(asm))
			stderr, code := runCaptureStderrExit(t, runner, bin)
			if code != want {
				t.Fatalf("exited %d under the sanitizer, want %d (interp oracle; 124 = fatal sanitizer check, 99 = rc underflow)", code, want)
			}
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "fern-sanitizer:") && !strings.HasPrefix(line, "fern-sanitizer: leak") {
					t.Error(line)
				}
			}
		})
	}
}
