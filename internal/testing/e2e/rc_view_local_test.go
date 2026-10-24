package e2e

import (
	"testing"
)

const viewLocalSrc = `function mk(n: i32): i32[] {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = a.append(i); i = i + 1; }
    return a;
}
function round(i: i32): i32 {
    let a: i32[] = mk(8);
    let s: [i32] = a[1:4];
    return s.len() + s[0];
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + round(i); i = i + 1; }
    if (acc != 200 * 4) { return 1; }
    return 0;
}`

const viewLocalAliasedSrc = `function mk(n: i32): i32[] {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = a.append(i); i = i + 1; }
    return a;
}
function round(i: i32): i32 {
    let a: i32[] = mk(8);
    let s: [i32] = a[1:4];
    let t: [i32] = s;
    let u: i32 = s.len();
    let b: i32[] = mk(4);
    let v: i32 = t[0] + t.len();
    if (b.len() != 4) { return 1; }
    return u + v;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + round(i); i = i + 1; }
    if (acc != 200 * 7) { return 2; }
    return __rc_underflow_count();
}`

const viewLocalEscapeSrc = `function mk(n: i32): i32[] {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = a.append(i); i = i + 1; }
    return a;
}
function window(b: i32[]): [i32] {
    let s: [i32] = b[1:4];
    return s;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let arr: i32[] = mk(8);
        let v: [i32] = window(arr);
        if (v.len() != 3) { return 1; }
        if (v[0] != 1) { return 2; }
        acc = acc + 1;
        i = i + 1;
    }
    if (acc != 200) { return 3; }
    return __rc_underflow_count();
}`

func viewLocalBumpSrc(n string) string {
	return `function mk(n: i32): i32[] {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < n) { a = a.append(i); i = i + 1; }
    return a;
}
function round(i: i32): i32 {
    let a: i32[] = mk(8);
    let s: [i32] = a[1:4];
    return s.len() + s[0];
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < ` + n + `) { acc = acc + round(i); i = i + 1; }
    if (acc < 0) { return acc; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func checkViewLocalBalanced(t *testing.T, backend, stderr string, code int) {
	t.Helper()
	if code != 0 {
		t.Fatalf("%s: exit=%d, want 0", backend, code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("%s: expected allocations (each round makes an array and a view header), got 0", backend)
	}
	if allocs != frees || live != 0 {
		t.Errorf("%s: view local leaks its header: allocs=%d frees=%d live_bytes=%d, want balanced / 0",
			backend, allocs, frees, live)
	}
}

func TestX86_64ViewLocalHeaderReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, viewLocalSrc)
	checkViewLocalBalanced(t, "x86-64", stderr, code)
	if out, code := compileAndRunX86_64FreeOn(t, viewLocalAliasedSrc); code != 0 {
		t.Errorf("aliased view: code=%d (1/2=wrong value after the alias was freed, >2=over-release)\n%s", code, out)
	}
	if out, code := compileAndRunX86_64FreeOn(t, viewLocalEscapeSrc); code != 0 {
		t.Errorf("view returned out of its frame: code=%d (1/2=wrong value, 3=wrong count, >3=over-release)\n%s", code, out)
	}
}

func TestArm64ViewLocalHeaderReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, viewLocalSrc)
	checkViewLocalBalanced(t, "arm64", stderr, code)
	if out, code := compileAndRunArm64FreeOn(t, viewLocalAliasedSrc); code != 0 {
		t.Errorf("aliased view: code=%d (1/2=wrong value after the alias was freed, >2=over-release)\n%s", code, out)
	}
	if out, code := compileAndRunArm64FreeOn(t, viewLocalEscapeSrc); code != 0 {
		t.Errorf("view returned out of its frame: code=%d (1/2=wrong value, 3=wrong count, >3=over-release)\n%s", code, out)
	}
}

func TestWASMViewLocalHeaderReclaim(t *testing.T) {
	small := runWasm(t, viewLocalBumpSrc("50"))
	large := runWasm(t, viewLocalBumpSrc("5000"))
	if small != large {
		t.Errorf("view-local bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if got := runWasm(t, viewLocalAliasedSrc); got != 0 {
		t.Errorf("aliased view: code=%d (1/2=wrong value after the alias was freed, >2=over-release)", got)
	}
	if got := runWasm(t, viewLocalEscapeSrc); got != 0 {
		t.Errorf("view returned out of its frame: code=%d", got)
	}
}
