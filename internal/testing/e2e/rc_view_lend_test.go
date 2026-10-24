package e2e

import (
	"testing"
)

const viewLendSrc = `function total(src: [u8], n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + (src[i] as i32); i = i + 1; }
    return t;
}
function mk(n: i32): u8[] {
    let a: u8[] = __alloc_u8(n);
    let i: i32 = 0;
    while (i < n) { a = a.with(i, ((i * 3) % 251) as u8); i = i + 1; }
    return a;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let b: u8[] = mk(8);
        acc = acc + total(b, 8);
        i = i + 1;
    }
    if (acc != 200 * 84) { return 1; }
    return 0;
}`

const viewLendAsBytesSrc = `function total(src: [u8], n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + (src[i] as i32); i = i + 1; }
    return t;
}
function w(i: i32): string {
    let t: string = "x";
    if (i % 2 == 0) { t = "yy"; }
    return "a-wide-payload-past-any-inline-threshold-" + t;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let s: string = w(i);
        acc = acc + total(s.as_bytes(), 8);
        i = i + 1;
    }
    if (acc < 0) { return 1; }
    return 0;
}`

const viewLendEscapeSrc = `@noinline function id(s: [u8]): [u8] { return s; }
function mk(n: i32): u8[] {
    let a: u8[] = __alloc_u8(n);
    let i: i32 = 0;
    while (i < n) { a = a.with(i, ((i * 3) % 251) as u8); i = i + 1; }
    return a;
}
function through(b: u8[]): [u8] { return id(b); }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let arr: u8[] = mk(8);
        let v: [u8] = through(arr);
        let w: [u8] = id(arr);
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
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) { t = t + (src[i] as i32); i = i + 1; }
    return t;
}
function mk(n: i32): u8[] {
    let a: u8[] = __alloc_u8(n);
    let i: i32 = 0;
    while (i < n) { a = a.with(i, ((i * 3) % 251) as u8); i = i + 1; }
    return a;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < ` + n + `) {
        let b: u8[] = mk(8);
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

func TestWASMViewLendHeaderReclaim(t *testing.T) {
	small := runWasm(t, viewLendBumpSrc("50"))
	large := runWasm(t, viewLendBumpSrc("5000"))
	if small != large {
		t.Errorf("view-header bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if got := runWasm(t, viewLendEscapeSrc); got != 0 {
		t.Errorf("returned view param: code=%d (1/2=wrong value after the call, >3=over-release)", got)
	}
}
