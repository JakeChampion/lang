package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #8502 — the `[T]` view header a call argument materialises had no owner.
//
// `checker.lendArrayAsView` rewrites an owned `T[]` argument at a `[T]`
// parameter into the full-range slice `xs[:]`, and `as_bytes` produces the
// same shape explicitly. Both lower to `__slice_make`, a two-word
// `__fern_alloc` block with no rc header — `rcResultRaw` by classification —
// so nothing in the rc machinery could release it and every call stranded 16
// bytes on x86-64 and arm64, 8 on wasm.
//
// The caller frees it once the call returns. Three things make that sound, and
// the first two are the language's: the coercion fires only at ARGUMENT
// positions (E043 rejects it in a struct literal), and fields are immutable
// after construction (E048), so the callee cannot write the view into storage
// the caller still reads. That leaves the callee's RETURN, which
// typeCannotCarrySlice gates.

const viewLendSrc = `function total(src: [u8], n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + (src[i] as i32); i = i + 1; }
    return t;
}
function mk(n: i32): u8[] {
    var a: u8[] = __alloc_u8(n);
    var i: i32 = 0;
    while (i < n) { a = a.with(i, ((i * 3) % 251) as u8); i = i + 1; }
    return a;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var b: u8[] = mk(8);
        acc = acc + total(b, 8);
        i = i + 1;
    }
    if (acc != 200 * 84) { return 1; }
    return 0;
}`

// The `as_bytes` producer of the same header, at the same argument position.
// This is the half `std/crypto` reaches through `__sha256_absorb`, and the
// reason audit_std_crypto's census row moved.
const viewLendAsBytesSrc = `function total(src: [u8], n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + (src[i] as i32); i = i + 1; }
    return t;
}
function w(i: i32): string {
    var t: string = "x";
    if (i % 2 == 0) { t = "yy"; }
    return "a-wide-payload-past-any-inline-threshold-" + t;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var s: string = w(i);
        acc = acc + total(s.as_bytes(), 8);
        i = i + 1;
    }
    if (acc < 0) { return 1; }
    return 0;
}`

// The direction leakcheck cannot see. A callee may RETURN its view parameter —
// `E063` forbids a view of function-local storage leaving the frame, but a
// view of a PARAMETER may, and the header the lend allocated goes with it. The
// caller-side free must not fire there, and the value must survive the call.
//
// 17 = element 3 of mk(8) (which is 9) plus the length 8. A freed header reads
// back as whatever the freelist put there, so a wrong answer here is the
// use-after-free this gate exists for.
const viewLendEscapeSrc = `function id(s: [u8]): [u8] { return s; }
function mk(n: i32): u8[] {
    var a: u8[] = __alloc_u8(n);
    var i: i32 = 0;
    while (i < n) { a = a.with(i, ((i * 3) % 251) as u8); i = i + 1; }
    return a;
}
function through(b: u8[]): [u8] { return id(b); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var arr: u8[] = mk(8);
        var v: [u8] = through(arr);
        var w: [u8] = id(arr);
        if ((v[3] as i32) + v.len() != 17) { return 1; }
        if ((w[3] as i32) + w.len() != 17) { return 2; }
        acc = acc + 1;
        i = i + 1;
    }
    if (acc != 200) { return 3; }
    return __rc_underflow_count();
}`

func viewLendBumpSrc(n string) string {
	return `function total(src: [u8], n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) { t = t + (src[i] as i32); i = i + 1; }
    return t;
}
function mk(n: i32): u8[] {
    var a: u8[] = __alloc_u8(n);
    var i: i32 = 0;
    while (i < n) { a = a.with(i, ((i * 3) % 251) as u8); i = i + 1; }
    return a;
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < ` + n + `) {
        var b: u8[] = mk(8);
        acc = acc + total(b, 8);
        i = i + 1;
    }
    if (acc < 0) { return acc; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func checkViewLendBalanced(t *testing.T, backend, stderr string, code int) {
	t.Helper()
	if code != 0 {
		t.Fatalf("%s: exit=%d, want 0", backend, code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("%s: expected allocations (each round makes an array and a view header), got 0", backend)
	}
	if allocs != frees || live != 0 {
		t.Errorf("%s: view header leaks: allocs=%d frees=%d live_bytes=%d, want balanced / 0",
			backend, allocs, frees, live)
	}
}

func TestX86_64ViewLendHeaderReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, viewLendSrc)
	checkViewLendBalanced(t, "x86-64 lend", stderr, code)
	_, stderr, code = runLeakCheckX86_64(t, viewLendAsBytesSrc)
	checkViewLendBalanced(t, "x86-64 as_bytes", stderr, code)
	if out, code := compileAndRunX86_64FreeOn(t, viewLendEscapeSrc); code != 0 {
		t.Errorf("returned view param: code=%d (1/2=wrong value after the call, 3=wrong count, >3=over-release)\n%s", code, out)
	}
}

func TestArm64ViewLendHeaderReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, viewLendSrc)
	checkViewLendBalanced(t, "arm64 lend", stderr, code)
	_, stderr, code = runLeakCheckArm64(t, viewLendAsBytesSrc)
	checkViewLendBalanced(t, "arm64 as_bytes", stderr, code)
	if out, code := compileAndRunArm64FreeOn(t, viewLendEscapeSrc); code != 0 {
		t.Errorf("returned view param: code=%d (1/2=wrong value after the call, 3=wrong count, >3=over-release)\n%s", code, out)
	}
}

// wasm has no leak counter, so it rides the __heap_bump_bytes() high-water
// probe: flat under reclaim, linear under a leak.
func TestWASMViewLendHeaderReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, viewLendBumpSrc("50"))
	large := runWasm(t, viewLendBumpSrc("5000"))
	if small != large {
		t.Errorf("view-header bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if got := runWasm(t, viewLendEscapeSrc); got != 0 {
		t.Errorf("returned view param: code=%d (1/2=wrong value after the call, >3=over-release)", got)
	}
}
