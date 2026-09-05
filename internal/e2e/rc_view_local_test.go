package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #8535 — a `[T]` view bound to a LOCAL never released its header.
//
// #8502 gave the header an owner where it is materialised as a call argument:
// dead once the call returns. A header bound to a local has the binding's
// lifetime instead, and nothing released it — 16 bytes per binding on the
// natives, 8 on wasm.
//
// The header is an rc1 block since #8406, so the binding's own release is the
// exit sweep's and `var t = s` retains: both names are counted and the block
// goes at the second release, not the first. The aliased control below pins
// that — it was written when a bespoke last-use free had to REFUSE an aliased
// header, and it holds for the stronger reason now.

const viewLocalSrc = `function mk(n: i32): i32[] {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = a.append(i); i = i + 1; }
    return a;
}
function round(i: i32): i32 {
    var a: i32[] = mk(8);
    var s: [i32] = a[1:4];
    return s.len() + s[0];
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + round(i); i = i + 1; }
    if (acc != 200 * 4) { return 1; }
    return 0;
}`

// The header must NOT be freed while a second name still holds it. `t` aliases
// `s`'s block — no new `__slice_make` — and `t` is read AFTER `s`'s last use,
// which is where a release keyed to the materialising binding alone would fire.
// The `mk(4)` between them recycles the freed block through the freelist, so
// the read lands on reused memory rather than on stale-but-intact bytes: the
// difference between a test that fails and one that happens to pass.
const viewLocalAliasedSrc = `function mk(n: i32): i32[] {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = a.append(i); i = i + 1; }
    return a;
}
function round(i: i32): i32 {
    var a: i32[] = mk(8);
    var s: [i32] = a[1:4];
    var t: [i32] = s;
    var u: i32 = s.len();
    var b: i32[] = mk(4);
    var v: i32 = t[0] + t.len();
    if (b.len() != 4) { return 1; }
    return u + v;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + round(i); i = i + 1; }
    if (acc != 200 * 7) { return 2; }
    return __rc_underflow_count();
}`

// A view over a PARAMETER may leave the frame — E063 only forbids a view of
// function-LOCAL storage — so the header goes with it and must survive.
const viewLocalEscapeSrc = `function mk(n: i32): i32[] {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = a.append(i); i = i + 1; }
    return a;
}
function window(b: i32[]): [i32] {
    var s: [i32] = b[1:4];
    return s;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var arr: i32[] = mk(8);
        var v: [i32] = window(arr);
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
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = a.append(i); i = i + 1; }
    return a;
}
function round(i: i32): i32 {
    var a: i32[] = mk(8);
    var s: [i32] = a[1:4];
    return s.len() + s[0];
}
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var acc: i32 = 0;
    var i: i32 = 0;
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
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
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
