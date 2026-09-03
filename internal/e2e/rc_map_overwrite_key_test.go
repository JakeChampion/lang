package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #7911 — `m.insert(k, v)` overwriting an existing entry keeps the key the
// column already holds and discards the incoming one. The boxed ABIs (wasm,
// arm64 two-word) released that key cell in freeDiscardedSetKeyCell; the
// single-word x86-64 path, where the column holds the data pointer itself,
// dropped the incoming reference on the floor — one heap key per overwrite.
//
// Every key below is pushed past the SSO inline threshold so it has a buffer
// to leak, and the suffix cycles through four values so most inserts are
// overwrites.

const mapOverwriteFreshKeySrc = `import "core/map";
import "std/i32";
@noinline
function suffix(i: i32): string { return "-a-wide-payload-past-any-inline-threshold-" + (i % 4).to_string(); }
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    var i: i32 = 0;
    while (i < 500) {
        m = m.insert("k" + suffix(i), i);
        i = i + 1;
    }
    return m.len() - 4;
}`

func mapOverwriteFreshKeyBumpSrc(n string) string {
	return `import "core/map";
import "std/i32";
@noinline
function suffix(i: i32): string { return "-a-wide-payload-past-any-inline-threshold-" + (i % 4).to_string(); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var m: Map[string, i32] = map_new(4);
    var i: i32 = 0;
    while (i < ` + n + `) {
        m = m.insert("k" + suffix(i), i);
        i = i + 1;
    }
    if (m.len() != 4) { return 99; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// The release must undo exactly the reference the set took. An aliased key
// is retained at the set, so it stays alive for the reads after the overwrite;
// a literal's sentinel is a no-op; an index-read key is retained like an
// ident. 0 iff every read saw the right string AND nothing dec'd past zero.
const mapOverwriteAliasedKeyUnderflowSrc = `import "core/map";
import "std/i32";
@noinline
function suffix(i: i32): string { return "-a-wide-payload-past-any-inline-threshold-" + (i % 4).to_string(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var m: Map[string, i32] = map_new(4);
        var k: string = "k" + suffix(i);
        m = m.insert(k, 1);
        m = m.insert(k, 2);
        m = m.insert("lit-key-long-literal", 1);
        m = m.insert("lit-key-long-literal", 2);
        var ks: string[] = ["e" + suffix(i)];
        m = m.insert(ks[0], 3);
        m = m.insert(ks[0], 4);
        acc = acc + (k.len() - ks[0].len()) + (m.get_or(k, 0) - 2) + (m.get_or(ks[0], 0) - 4) + (m.len() - 3);
        i = i + 1;
    }
    if (acc != 0) { return 99; }
    return __rc_underflow_count();
}`

func TestX86_64MapOverwriteKeyReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckX86_64(t, mapOverwriteFreshKeySrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (keys are heap strings), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("overwritten map keys leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	if _, code := compileAndRunX86_64FreeOn(t, mapOverwriteAliasedKeyUnderflowSrc); code != 0 {
		t.Errorf("aliased-key overwrite: code=%d (99=wrong value, >0=over-release)", code)
	}
	small := mustRunX86_64FreeOn(t, mapOverwriteFreshKeyBumpSrc("50"))
	large := mustRunX86_64FreeOn(t, mapOverwriteFreshKeyBumpSrc("5000"))
	if small != large {
		t.Errorf("overwrite bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64MapOverwriteKeyReclaim(t *testing.T) {
	_, stderr, code := runLeakCheckArm64(t, mapOverwriteFreshKeySrc)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 {
		t.Fatalf("expected allocations (keys are heap strings), got 0")
	}
	if allocs != frees || live != 0 {
		t.Errorf("overwritten map keys leak: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
	if _, code := compileAndRunArm64FreeOn(t, mapOverwriteAliasedKeyUnderflowSrc); code != 0 {
		t.Errorf("aliased-key overwrite: code=%d (99=wrong value, >0=over-release)", code)
	}
	small := mustRunArm64FreeOn(t, mapOverwriteFreshKeyBumpSrc("50"))
	large := mustRunArm64FreeOn(t, mapOverwriteFreshKeyBumpSrc("5000"))
	if small != large {
		t.Errorf("overwrite bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestWASMMapOverwriteKeyReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, mapOverwriteFreshKeyBumpSrc("50"))
	large := runWasm(t, mapOverwriteFreshKeyBumpSrc("5000"))
	if small != large {
		t.Errorf("overwrite bump should be bounded: N=50 -> %d, N=5000 -> %d", small, large)
	}
	if got := runWasm(t, mapOverwriteAliasedKeyUnderflowSrc); got != 0 {
		t.Errorf("aliased-key overwrite: code=%d (99=wrong value, >0=over-release)", got)
	}
}
