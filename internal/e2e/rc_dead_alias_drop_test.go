// A binding that dies inside a loop body used to hold its reference for the
// rest of the function. computePreciseDrops built its candidate set from
// `fn.Body.Stmts`, so only a TOP-LEVEL `var` could be released at its last
// use; anything declared inside an if / while / for / match survived to the
// next entry's re-init drop or the function-exit sweep.
//
// For an alias of an accumulator that is exactly the rc==1 append cliff
// (#6024). `var keep = acc` takes an alias inc, so `acc` sits at rc 2, and
// __fern_arr_push_grow mutates in place only at rc 1 — every subsequent
// append copies the whole buffer. 200 appends behind a binding that nothing
// reads again cost 199 full-buffer copies, identically on x86-64, arm64 and
// wasm. computeNestedDrops releases `keep` at its last read, which restores
// rc 1 and lets the appends run in place.
//
// Both halves are pinned: the DEAD alias must reach 0 copies, and the LIVE
// alias (the same program with the read moved after the append) must still
// report 199 — there the alias genuinely observes the buffer and the copy is
// mandatory, so a "fix" that took it to 0 would be a use-after-free, not a
// win.
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// deadAliasAppendSrc: `keep` is read BEFORE the append and never again, so the
// mutation is unobservable through it and the buffer can be grown in place.
const deadAliasAppendSrc = `function main(): i32 {
    var acc: i32[] = [];
    var i: i32 = 0;
    while (i < 200) {
        var keep: i32[] = acc;
        if (keep.len() != i) { return 999; }
        acc = acc.append(i);
        i = i + 1;
    }
    if (acc.len() != 200) { return 254; }
    if (acc[7] != 7 || acc[199] != 199) { return 253; }
    return __arr_push_shared_count();
}`

// liveAliasAppendSrc: the control. Same program with the read moved AFTER the
// append, so `keep` is still live across it and every copy is required.
const liveAliasAppendSrc = `function main(): i32 {
    var acc: i32[] = [];
    var i: i32 = 0;
    while (i < 200) {
        var keep: i32[] = acc;
        acc = acc.append(i);
        if (keep.len() != i) { return 999; }
        i = i + 1;
    }
    if (acc.len() != 200) { return 254; }
    return __arr_push_shared_count();
}`

func TestX86_64DeadAliasAppendNoCopy(t *testing.T) {
	if _, got := compileAndRunX86_64FreeOn(t, deadAliasAppendSrc); got != 0 {
		t.Errorf("x86-64 dead alias: __arr_push_shared_count() = %d, want 0 — "+
			"a loop-body binding nothing reads again is still forcing a full buffer copy per append", got)
	}
	if _, got := compileAndRunX86_64FreeOn(t, liveAliasAppendSrc); got != 199 {
		t.Errorf("x86-64 live alias: __arr_push_shared_count() = %d, want 199 — "+
			"the alias is read after the append, so every copy is mandatory", got)
	}
}

func TestArm64DeadAliasAppendNoCopy(t *testing.T) {
	if _, got := compileAndRunArm64(t, deadAliasAppendSrc); got != 0 {
		t.Errorf("arm64 dead alias: __arr_push_shared_count() = %d, want 0", got)
	}
	if _, got := compileAndRunArm64(t, liveAliasAppendSrc); got != 199 {
		t.Errorf("arm64 live alias: __arr_push_shared_count() = %d, want 199", got)
	}
}

func TestWASMDeadAliasAppendNoCopy(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, deadAliasAppendSrc); got != 0 {
		t.Errorf("wasm dead alias: __arr_push_shared_count() = %d, want 0", got)
	}
	if got := runWasm(t, liveAliasAppendSrc); got != 199 {
		t.Errorf("wasm live alias: __arr_push_shared_count() = %d, want 199", got)
	}
}
