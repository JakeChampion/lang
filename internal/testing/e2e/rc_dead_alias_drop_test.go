// A binding that dies inside a loop body used to hold its reference for the
// rest of the function. computePreciseDrops built its candidate set from
// `fn.Body.Stmts`, so only a TOP-LEVEL `let` could be released at its last
// use; anything declared inside an if / while / for / match survived to the
// next entry's re-init drop or the function-exit sweep.
//
// For an alias of an accumulator that is exactly the rc==1 append cliff
// (#6024). `let keep = acc` takes an alias inc, so `acc` sits at rc 2, and
// the append grows in place only at rc 1 — every subsequent append copies the
// whole buffer. Releasing `keep` at its last read restores rc 1 and lets the
// appends run in place.
//
// Both halves are pinned: the DEAD alias must reach 0 copies, and the LIVE
// alias (with an opaque observer after the append) must still copy on every
// append. The observer sees the original buffer, so reusing it would change
// its result. A direct length-only read can instead be evaluated early; a
// separate case pins that optimization. The counter tallies spare-capacity
// copies, and
// seven of the 200 appends find the buffer full, so the live alias reads 193.
package e2e

import (
	"testing"
)

// deadAliasAppendSrc: `keep` is read BEFORE the append and never again, so the
// mutation is unobservable through it and the buffer can be grown in place.
const deadAliasAppendSrc = `@noinline function observed_length(xs: i32[]): i32 { return xs.len(); }
function main(): i32 {
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < 200) {
        let keep: i32[] = acc;
        if (observed_length(keep) != i) { return 999; }
        acc = acc.append(i);
        i = i + 1;
    }
    if (acc.len() != 200) { return 254; }
    if (acc[7] != 7 || acc[199] != 199) { return 253; }
    return __arr_push_shared_count();
}`

// liveAliasAppendSrc: the control. Same program with the read moved AFTER the
// append, so `keep` is still live across it and every copy is required.
const liveAliasAppendSrc = `@noinline function observed_length(xs: i32[]): i32 { return xs.len(); }
function main(): i32 {
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < 200) {
        let keep: i32[] = acc;
        acc = acc.append(i);
        if (observed_length(keep) != i) { return 999; }
        i = i + 1;
    }
    if (acc.len() != 200) { return 254; }
    return __arr_push_shared_count();
}`

func TestX86_64DeadAliasAppendNoCopy(t *testing.T) {
	if _, got := compileAndRunX86_64FreeOn(t, lengthOnlyAliasAppendSrc); got != 0 {
		t.Errorf("x86-64 length-only alias: got %d, want 0 copies with the original length preserved", got)
	}

	if _, got := compileAndRunX86_64FreeOn(t, deadAliasAppendSrc); got != 0 {
		t.Errorf("x86-64 dead alias: __arr_push_shared_count() = %d, want 0 — "+
			"a loop-body binding nothing reads again is still forcing a full buffer copy per append", got)
	}
	if _, got := compileAndRunX86_64FreeOn(t, liveAliasAppendSrc); got != 193 {
		t.Errorf("x86-64 live alias: __arr_push_shared_count() = %d, want 193 — "+
			"the alias is read after the append, so every copy is mandatory", got)
	}
}

// A length fact needs no live array identity after the append.
const lengthOnlyAliasAppendSrc = `function main(): i32 {
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < 200) {
        let keep: i32[] = acc;
        acc = acc.append(i);
        if (keep.len() != i) { return 999; }
        i = i + 1;
    }
    if (acc.len() != 200) { return 254; }
    return __arr_push_shared_count();
}`
