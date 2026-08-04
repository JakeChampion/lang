package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Replaced struct-field reclamation (RC-Perceus 5f). A self-overwrite
// `p = T{ ... }` of an owned, uniquely-held struct reuses p's box in place
// (the Phase 5c FBIP constructor-reuse). When a pointer-typed field is
// REPLACED, its old buffer is freed before the new value overwrites it
// (`tryStructReuseOverwrite` step 4 → `emitFieldDropOnStack`). This is the
// freeing-dec the plan's 5f note long described as "deferred / needs alias
// analysis" — it is in fact already shipped, and sound, because StructLit
// construction inc's its fields globally, so the freeing dec is rc-protected:
// any live alias of the old buffer (including one read in the self-overwrite
// RHS) has bumped the rc, so the dec only frees the genuine last reference.
//
// These tests lock that invariant in. The boundedness probe proves the old
// buffer is actually reclaimed (flat high-water across 10x N). The adversarial
// soundness cases exercise exactly the aliasing shapes the 5f note feared — a
// `*ast.Call` returning the old field (`ident(p.items)`), a branch-returning
// helper, and an aliased local kept live across the overwrite — each pinning
// `__rc_underflow_count() == 0` (no over-release / UAF) AND value-correctness.
// The wasm e2e is the over-release arbiter (its bump cursor measures the leak
// directly; natives' segregated freelist arena is insensitive to small-block
// reclaim).

// replacedFieldBumpSrc: each iteration self-overwrites with a FRESH array
// field, so the old field buffer must be freed (reclaimed) every iteration.
func replacedFieldBumpSrc(n string) string {
	return `struct Box { items: i32[], n: i32 }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var p: Box = Box{ items: [0], n: 0 };
    var i: i32 = 0;
    while (i < ` + n + `) {
        p = Box{ items: [i, i + 1, i + 2, i + 3], n: i };
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// replacedFieldAliasCallSrc: the replaced field's new value is the OLD field
// returned through a borrowing call (`ident(p.items)`) — the *ast.Call shape
// the 5f note flagged as the UAF risk. Sound iff the old buffer's rc reflects
// the read in the RHS, so the freeing dec doesn't reclaim a still-referenced
// buffer. Returns 0 iff value-correct AND 0 over-releases.
const replacedFieldAliasCallSrc = `struct Box { items: i32[], n: i32 }
function ident(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    var p: Box = Box{ items: [1, 2, 3], n: 0 };
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < 100) {
        p = Box{ items: ident(p.items), n: p.n + 1 };
        // Force interleaved allocation that would corrupt a wrongly-freed
        // old buffer (the freelist would hand it back to junk).
        var junk: i32[] = [7, 7, 7];
        sum = sum + junk[0] + p.items[0] + p.items[1] + p.items[2];
        i = i + 1;
    }
    // p.items stays [1,2,3] every iter: sum = 100*(7+1+2+3) = 1300.
    if (sum != 1300) { return 700; }
    if (p.n != 100) { return 701; }
    return __rc_underflow_count();
}`

// replacedFieldAliasBranchSrc: the new value flows through a helper that
// returns the old field from a conditional (an IfExpr/return-of-borrow shape).
const replacedFieldAliasBranchSrc = `struct Box { items: i32[], n: i32 }
function pick(xs: i32[], k: i32): i32[] { if (k > 0) { return xs; } return xs; }
function main(): i32 {
    var p: Box = Box{ items: [5, 6, 7], n: 0 };
    var i: i32 = 0;
    while (i < 100) {
        p = Box{ items: pick(p.items, i), n: p.n + 1 };
        i = i + 1;
    }
    if (p.items[0] != 5) { return 800; }
    if (p.items[2] != 7) { return 801; }
    return __rc_underflow_count();
}`

// replacedFieldAliasLocalSrc: the old field is also held by a live local
// `keep` across the overwrite, and `keep` is read AFTER — the alias must keep
// the buffer alive past the reuse free.
const replacedFieldAliasLocalSrc = `struct Box { items: i32[], n: i32 }
function ident(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    var p: Box = Box{ items: [9, 8, 7], n: 0 };
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 100) {
        var keep: i32[] = p.items;
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
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
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
