package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Statement-temporary reclamation, stage (a), on the self-host IR path: a
// discarded bare-ExprStmt whose value is a FRESH scalar-element array literal
// (`[i, i + 1, i + 2];`) is DEC'd at the statement boundary (the rc-guarded
// __fern_rc_dec, discardable_scalar_arr_lit) instead of leaking its buffer
// every iteration. This is the self-host sibling of native's
// emitOwnedTempStackDrop (internal/e2e/rc_heap_bump_stmt_temp_test.go);
// #4365 flagged it as a native-tested behavior with no self-host equivalent.
//
// Two assertions, both through the self-host x86-64 IR driver (asm_run):
//   - FIXPOINT: the discarded-temp loop's bump-growth is now BOUNDED — equal at
//     N=50 and N=5000 (before the reclaim it scaled with N: 96 -> 128 -> …).
//   - OVER-RELEASE: the discarded temp must reclaim its OWN box without touching
//     the live `xs` built from the same loop-variable operands — a wrong "owned"
//     verdict that freed a shared buffer would corrupt the sum (999) or trip the
//     __rc_underflow detector (> 0).

func stmtTempArrBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { [i, i + 1, i + 2]; i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// A discarded owned array temp reclaims its box while the live `xs` (built from
// the same operands) is untouched: sum over i=0..199 of (i)+(i+1)+(i+2) =
// 3*(199*200/2) + 3*200 = 60300. __rc_underflow() (the self-host detector) then
// reports 0 only if nothing was over-released.
const stmtTempReclaimDetectorSrc = `function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        [i, i + 1, i + 2];
        var xs: i32[] = [i, i + 1, i + 2];
        acc = acc + xs[0] + xs[1] + xs[2];
        i = i + 1;
    }
    if (acc != 60300) { return 999; }
    return __rc_underflow();
}`

// The other stage-(a) discarded-temp shapes, each a fresh rc=1 sole-owner box
// released at the statement boundary: a scalar tuple / scalar struct literal
// (shallow __fern_rc_dec) and a string concat (rc-aware __fern_str_free).

func stmtTempTupleBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { (i, i + 1); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func stmtTempStructBumpSrc(n string) string {
	return `struct P { x: i32, y: i32 }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { P { x: i, y: i + 1 }; i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func stmtTempStrConcatBumpSrc(n string) string {
	return `function main(): i32 {
    var a: string = "hello";
    var b: string = "world";
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { a + b; i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// A discarded fresh rc-FIELD struct literal (`H { id, xs: [..] };`) leaks BOTH
// its box AND its field buffers if merely dropped. The reclaim deep-drops it the
// way the exit sweep drops a struct LOCAL: __struct_drop_<T> releases the rc-array
// field buffers, then __fern_rc_dec frees the box. Because __struct_drop_<T>'s
// inner arr_dec clobbers its box return register, the box is stashed in a scratch
// local and re-loaded for the free (the exit sweep's slot-reload pattern) — a
// naive box→drop→box chain double-freed the field buffer instead (the __rc_underflow
// detector below has teeth for exactly that regression).
func stmtTempRcFieldStructBumpSrc(n string) string {
	return `struct H { id: i32, xs: i32[] }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { H { id: i, xs: [i, i + 1, i + 2] }; i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// A discarded call to a STRICT fresh-struct-returning function hands back a rc=1
// box that leaks if merely dropped; the ExprCall reclaim releases it (native
// ownedCallResultType). `mk` returns a fresh no-base `P { … }`, so it is in
// return_fresh_struct_ret_fns.
func stmtTempFreshCallBumpSrc(n string) string {
	return `struct P { x: i32, y: i32 }
function mk(a: i32): P { return P { x: a, y: a + 1 }; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// A discarded ARRAY-fresh-ret call (`mk(i) -> i32[]`, every return a direct
// scalar-element array literal — the "ARR:" entries of the strict-fresh
// registry, #4365): the returned rc=1 buffer is released with the shallow
// rc-guarded __fern_rc_dec at the statement boundary (element-blind, scalar
// elements ride the freed buffer). Native bounds this shape
// (rc_heap_bump_discarded_call); the self-host leaked it until the ARR: arm.
func stmtTempFreshCallArrBumpSrc(n string) string {
	return `function mk(a: i32): i32[] { return [a, a + 1, a + 2]; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// A FRESH string-concat RECEIVER (`(s1 + s2).len()`) — the value-consuming-op
// sibling of the discarded-statement concat (#4365; native's
// rc_heap_bump_len_receiver arc). The `.len()` intercept stashes the fresh
// temp box in an unmarked scratch, reads the length, then releases the box
// with the rc-aware __fern_str_free. No warmup: the fixpoint harness requires
// a NON-ZERO bounded high-water (a zero reads as "nothing measured"), and the
// cold first iteration allocates exactly one concat box before the freelist
// recycles it — the same small constant at every N.
func stmtTempLenReceiverBumpSrc(n string) string {
	return `function main(): i32 {
    var s1: string = "ab";
    var s2: string = "cd";
    var acc: i32 = 0;
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { acc = (acc + (s1 + s2).len()) % 251; i = i + 1; }
    if (acc < 0) { return 0 - 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// A discarded STRICT fresh-struct-returning call whose return type carries an
// rc-ARRAY field (`mk(i) -> H { id, xs: [..] };`) sole-owns every field buffer
// (the strict-fresh registry guarantees no aliased inner buffers), so the ExprCall
// reclaim DEEP-drops it — __struct_drop_<H> releases xs, then __fern_rc_dec frees
// the box — instead of freeing only the box and leaking xs.
func stmtTempFreshCallRcFieldBumpSrc(n string) string {
	return `struct H { id: i32, xs: i32[] }
function mk(a: i32): H { return H { id: a, xs: [a, a + 1, a + 2] }; }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// A discarded fresh string[] literal (`[p + "a", p + "b"];`) leaks BOTH its
// buffer AND every element box if merely dropped (unbounded in a hot loop). The
// reclaim deep-frees it with __fern_str_arr_free — the element-walking release
// the exit sweep gives a reclaimable string[] LOCAL (#4355): each element is a
// proven-fresh concat, so the buffer sole-owns it at rc=1, the rc==1-gated walk
// frees every element box, then returns the buffer to its freelist. Elements are
// DISTINCT concats (`p+"a"`, `p+"b"`) — not the same expression twice, which the
// native emitter shares into one box (an aliased element the deep free would
// leak, not a fresh sole-owned one).
func stmtTempFreshStrArrBumpSrc(n string) string {
	return `function main(): i32 {
    var p: string = "x";
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { [p + "a", p + "b"]; i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// Detectors: the discarded temp reclaims its OWN box while a live value built
// from the same operands stays intact — a wrong "owned" verdict that freed a
// shared box would corrupt the sum (999) or trip __rc_underflow (> 0). Sums:
// tuple/struct t=(i,i+2): 2i+2 over 0..199 = 40200; string s=a+b: 200 * 10.
const stmtTempTupleDetectorSrc = `function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        (i, i + 1);
        var t: (i32, i32) = (i, i + 2);
        acc = acc + t.0 + t.1;
        i = i + 1;
    }
    if (acc != 40200) { return 999; }
    return __rc_underflow();
}`

const stmtTempStructDetectorSrc = `struct P { x: i32, y: i32 }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        P { x: i, y: i + 1 };
        var p: P = P { x: i, y: i + 2 };
        acc = acc + p.x + p.y;
        i = i + 1;
    }
    if (acc != 40200) { return 999; }
    return __rc_underflow();
}`

const stmtTempStrConcatDetectorSrc = `function main(): i32 {
    var a: string = "hello"; var b: string = "world";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        a + b;
        var s: string = a + b;
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc != 2000) { return 999; }
    return __rc_underflow();
}`

// The discarded rc-field struct `H { id, xs: [..] };` reclaims its box AND xs
// buffer while the live `h = H { id: i, xs: [..] }` (same operands, its own box)
// stays intact: acc += h.id + h.xs[0] + h.xs[1] + h.xs[2] = i + i + (i+1) + (i+2)
// = 4i+3 over 0..199 = 4*19900 + 600 = 80200. The double-free bug this guards
// (the box-return-register clobber) trips __rc_underflow (> 0), not the sum.
const stmtTempRcFieldStructDetectorSrc = `struct H { id: i32, xs: i32[] }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        H { id: i, xs: [i, i + 1, i + 2] };
        var h: H = H { id: i, xs: [i, i + 1, i + 2] };
        acc = acc + h.id + h.xs[0] + h.xs[1] + h.xs[2];
        i = i + 1;
    }
    if (acc != 80200) { return 999; }
    return __rc_underflow();
}`

// The discarded call `mk(i);` reclaims its fresh box while the live `p = mk(i+1)`
// (an independent fresh box) stays intact: p = P{i+1, i+2}, acc += (i+1)+(i+2) =
// 2i+3 over 0..199 = 40400. A wrong reclaim of a shared box would corrupt it.
const stmtTempFreshCallDetectorSrc = `struct P { x: i32, y: i32 }
function mk(a: i32): P { return P { x: a, y: a + 1 }; }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        mk(i);
        var p: P = mk(i + 1);
        acc = acc + p.x + p.y;
        i = i + 1;
    }
    if (acc != 40400) { return 999; }
    return __rc_underflow();
}`

// The discarded rc-field-returning call `mk(i);` deep-drops its box AND xs while
// the live `p = mk(i+1)` (its own fresh box + buffers) stays intact: p from
// mk(i+1) has id=i+1, xs=[i+1,i+2,i+3]; acc += (i+1)+(i+1)+(i+2)+(i+3) = 4i+7
// over 0..199 = 4*19900 + 1400 = 81000. The box-return-register clobber this
// guards would double-free xs and trip __rc_underflow (> 0), not the sum.
// The len-receiver release frees only the fresh CONCAT temp: the operands
// s1/s2 stay readable after (their own boxes untouched), the length value is
// exact, and the detector stays zero.
const stmtTempLenReceiverDetectorSrc = `function main(): i32 {
    var s1: string = "ab" + "c";
    var s2: string = "de";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        acc = acc + (s1 + s2).len();
        i = i + 1;
    }
    if (acc != 1000) { return 999; }
    if (s1.len() != 3 || s2.len() != 2) { return 998; }
    return __rc_underflow();
}`

// The discarded array-returning call `mk(i);` frees only its OWN buffer while
// the live `v = mk(i + 1)` stays intact: v = [i+1, i+2, i+3], acc += 3i + 6
// over 0..199 = 3*19900 + 1200 = 60900. A wrong dec of the live buffer would
// corrupt the sum (999) or trip __rc_underflow (> 0).
const stmtTempFreshCallArrDetectorSrc = `function mk(a: i32): i32[] { return [a, a + 1, a + 2]; }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        mk(i);
        var v = mk(i + 1);
        acc = acc + v[0] + v[1] + v[2];
        i = i + 1;
    }
    if (acc != 60900) { return 999; }
    return __rc_underflow();
}`

const stmtTempFreshCallRcFieldDetectorSrc = `struct H { id: i32, xs: i32[] }
function mk(a: i32): H { return H { id: a, xs: [a, a + 1, a + 2] }; }
function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        mk(i);
        var p: H = mk(i + 1);
        acc = acc + p.id + p.xs[0] + p.xs[1] + p.xs[2];
        i = i + 1;
    }
    if (acc != 81000) { return 999; }
    return __rc_underflow();
}`

// The discarded fresh string[] `[p+"a", p+"b"];` deep-frees its buffer AND element
// boxes while the live `xs = [p+"c", p+"d"]` (independent fresh boxes) stays
// intact: xs[0]="xc", xs[1]="xd", each len 2, so acc += 4 over 0..199 = 800. A
// wrong reclaim that decdouble-freed a live element box would trip __rc_underflow
// (> 0); an over-eager release of the live xs would corrupt the sum (999).
const stmtTempFreshStrArrDetectorSrc = `function main(): i32 {
    var p: string = "x";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        [p + "a", p + "b"];
        var xs: string[] = [p + "c", p + "d"];
        acc = acc + xs[0].len() + xs[1].len();
        i = i + 1;
    }
    if (acc != 800) { return 999; }
    return __rc_underflow();
}`

// A discarded string[] literal whose elements are BORROWED (`[s, s];` — a bare
// owned local, not a fresh producer) must NOT be admitted to the deep free: the
// element boxes alias `s`, which the exit sweep also frees, so a deep free here
// would double-free. discardable_fresh_strarr_lit excludes borrowed elements
// (expr_is_fresh_str is false for a bare ident), so this keeps leaking on the
// plain drop — sound. The detector proves the exclusion holds: `s.len()`==5 over
// 200 = 1000, and __rc_underflow stays 0 (no over-release). A regression that
// admitted borrowed elements would trip __rc_underflow (> 0).
const stmtTempBorrowedStrArrDetectorSrc = `function main(): i32 {
    var s: string = "hello";
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) {
        [s, s];
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc != 1000) { return 999; }
    return __rc_underflow();
}`

// TestSelfHostStmtTempReclaimIRX86_64 builds the self-host x86-64 IR driver once
// and drives the two programs through it.
func TestSelfHostStmtTempReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, tag, prog string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", tag)
		}
		progBin := buildBin(t, gcc, dir, tag, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	fixpointShapes := []struct {
		name string
		src  func(n string) string
	}{
		{"scalar-array", stmtTempArrBumpSrc},
		{"scalar-tuple", stmtTempTupleBumpSrc},
		{"scalar-struct", stmtTempStructBumpSrc},
		{"rc-field-struct", stmtTempRcFieldStructBumpSrc},
		{"string-concat", stmtTempStrConcatBumpSrc},
		{"fresh-call-struct", stmtTempFreshCallBumpSrc},
		{"fresh-call-rc-field-struct", stmtTempFreshCallRcFieldBumpSrc},
		{"fresh-call-array", stmtTempFreshCallArrBumpSrc},
		{"len-receiver-concat", stmtTempLenReceiverBumpSrc},
		{"fresh-strarr", stmtTempFreshStrArrBumpSrc},
	}
	for _, sh := range fixpointShapes {
		t.Run("fixpoint-bounded/"+sh.name, func(t *testing.T) {
			small := run(t, sh.name+"-50", sh.src("50"))
			large := run(t, sh.name+"-5000", sh.src("5000"))
			if small != large {
				t.Errorf("discarded-%s-temp bump must be bounded: N=50 -> %d, N=5000 -> %d", sh.name, small, large)
			}
			if small == 0 {
				t.Errorf("%s: expected a non-zero bounded high-water, got 0 (nothing allocated / measured)", sh.name)
			}
		})
	}

	detectorShapes := []struct {
		name string
		src  string
	}{
		{"scalar-array", stmtTempReclaimDetectorSrc},
		{"scalar-tuple", stmtTempTupleDetectorSrc},
		{"scalar-struct", stmtTempStructDetectorSrc},
		{"rc-field-struct", stmtTempRcFieldStructDetectorSrc},
		{"string-concat", stmtTempStrConcatDetectorSrc},
		{"fresh-call-struct", stmtTempFreshCallDetectorSrc},
		{"fresh-call-rc-field-struct", stmtTempFreshCallRcFieldDetectorSrc},
		{"fresh-call-array", stmtTempFreshCallArrDetectorSrc},
		{"len-receiver-concat", stmtTempLenReceiverDetectorSrc},
		{"fresh-strarr", stmtTempFreshStrArrDetectorSrc},
		{"borrowed-strarr", stmtTempBorrowedStrArrDetectorSrc},
	}
	for _, sh := range detectorShapes {
		t.Run("no-over-release/"+sh.name, func(t *testing.T) {
			if code := run(t, sh.name+"-detector", sh.src); code != 0 {
				t.Errorf("discarded-%s-temp reclaim: exit=%d (999=value mismatch, >0=over-release)", sh.name, code)
			}
		})
	}
}
