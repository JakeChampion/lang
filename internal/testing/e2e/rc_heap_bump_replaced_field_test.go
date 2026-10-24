package e2e

import (
	"testing"
)

func replacedFieldBumpSrc(n string) string {
	return `struct Box { items: i32[], n: i32 }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let p: Box = Box{ items: [0], n: 0 };
    let i: i32 = 0;
    while (i < ` + n + `) {
        p = Box{ items: [i, i + 1, i + 2, i + 3], n: i };
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const replacedFieldAliasCallSrc = `struct Box { items: i32[], n: i32 }
@noinline function ident(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let p: Box = Box{ items: [1, 2, 3], n: 0 };
    let i: i32 = 0;
    let sum: i32 = 0;
    while (i < 100) {
        p = Box{ items: ident(p.items), n: p.n + 1 };
        // Force interleaved allocation that would corrupt a wrongly-freed
        // old buffer (the freelist would hand it back to junk).
        let junk: i32[] = [7, 7, 7];
        sum = sum + junk[0] + p.items[0] + p.items[1] + p.items[2];
        i = i + 1;
    }
    // p.items stays [1,2,3] every iter: sum = 100*(7+1+2+3) = 1300.
    if (sum != 1300) { return 700; }
    if (p.n != 100) { return 701; }
    return __rc_underflow_count();
}`

const replacedFieldAliasBranchSrc = `struct Box { items: i32[], n: i32 }
function pick(xs: i32[], k: i32): i32[] { if (k > 0) { return xs; } return xs; }
function main(): i32 {
    let p: Box = Box{ items: [5, 6, 7], n: 0 };
    let i: i32 = 0;
    while (i < 100) {
        p = Box{ items: pick(p.items, i), n: p.n + 1 };
        i = i + 1;
    }
    if (p.items[0] != 5) { return 800; }
    if (p.items[2] != 7) { return 801; }
    return __rc_underflow_count();
}`

const replacedFieldAliasLocalSrc = `struct Box { items: i32[], n: i32 }
@noinline function ident(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let p: Box = Box{ items: [9, 8, 7], n: 0 };
    let i: i32 = 0;
    let acc: i32 = 0;
    while (i < 100) {
        let keep: i32[] = p.items;
        p = Box{ items: ident(keep), n: p.n + 1 };
        acc = acc + keep[0];
        i = i + 1;
    }
    if (p.items[0] != 9) { return 600; }
    if (acc != 900) { return 601; }
    return __rc_underflow_count();
}`

func TestX86_64ReplacedFieldReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, replacedFieldBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, replacedFieldBumpSrc("5000"))
	if small != large {
		t.Errorf("replaced-field bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	for name, src := range map[string]string{
		"alias-call": replacedFieldAliasCallSrc, "alias-branch": replacedFieldAliasBranchSrc, "alias-local": replacedFieldAliasLocalSrc,
	} {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("%s: code=%d (>=600=value/UAF, >0=over-release)", name, code)
		}
	}
}

func TestArm64ReplacedFieldReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, replacedFieldBumpSrc("50"))
	large := mustRunArm64FreeOn(t, replacedFieldBumpSrc("5000"))
	if small != large {
		t.Errorf("replaced-field bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	for name, src := range map[string]string{
		"alias-call": replacedFieldAliasCallSrc, "alias-branch": replacedFieldAliasBranchSrc, "alias-local": replacedFieldAliasLocalSrc,
	} {
		if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
			t.Errorf("%s: code=%d", name, code)
		}
	}
}

func TestWASMReplacedFieldReclaim(t *testing.T) {
	small := runWasm(t, replacedFieldBumpSrc("50"))
	large := runWasm(t, replacedFieldBumpSrc("5000"))
	if small != large {
		t.Errorf("replaced-field bump should be bounded (old buffer reclaim): N=50 -> %d, N=5000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm heap-allocates; expected a non-zero bounded high-water, got 0")
	}
	for name, src := range map[string]string{
		"alias-call": replacedFieldAliasCallSrc, "alias-branch": replacedFieldAliasBranchSrc, "alias-local": replacedFieldAliasLocalSrc,
	} {
		if got := runWasm(t, src); got != 0 {
			t.Errorf("%s: got %d (>=600=value/UAF, >0=over-release)", name, got)
		}
	}
}
