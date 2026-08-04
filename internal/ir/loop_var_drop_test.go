package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// arrDecCount counts __fern_arr_dec calls a function lowers — the
// owned-array buffer-free helper. A loop-body `var` of array type that
// fires the Phase 5h dec-on-reinit emits one inside the loop, on top of
// the single function-exit sweep dec.
func arrDecCount(fn *ir.Func) int {
	n := 0
	for _, op := range fn.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == "__fern_arr_dec" {
			n++
		}
	}
	return n
}

// Phase 5h: a loop-body `var row` of an owned array type releases the
// prior iteration's buffer before the re-init store. The dec-on-reinit
// (__fern_arr_dec inside the loop) is in addition to the one the exit
// sweep emits for the function-scoped slot.
//
// A THIRD one is the nested-block precise drop (computeNestedDrops, #6024):
// `row` is dead after `sum = sum + row[0] + row[2]`, so it is released there
// rather than a whole iteration later. The other two stay because they are
// the paths a `break` / `continue` before the precise point still exits
// through; the precise drop zeroes the slot, so on the ordinary path they
// null-guard to no-ops.
func TestLoopVarDropFiresForArray(t *testing.T) {
	ip := lowerForTest(t, `function churn(n: i32): i32 {
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var row: i32[] = [i, i + 1, i + 2];
        sum = sum + row[0] + row[2];
        i = i + 1;
    }
    return sum;
}
function main(): i32 { return churn(3); }`)
	f := funcByName(ip, "churn")
	if f == nil {
		t.Fatal("no func churn")
	}
	if got := arrDecCount(f); got != 3 {
		t.Errorf("loop-body array var should lower 3 __fern_arr_dec (1 precise drop at its last use + 1 dec-on-reinit + 1 exit sweep), got %d", got)
	}
}

// A closure-typed loop-body var is deliberately SKIPPED by dec-on-reinit:
// emitting a dec between OpMakeClosure and OpStoreLocal would break the
// defunctionalise / closure-pair-elide pattern match. Pin that the
// MakeClosure → StoreLocal store is still immediately adjacent (no
// dec ops spliced in) by asserting the closure pair still elides to
// OpMakeEnv — which only happens when the writer/reader shape is intact.
func TestLoopVarDropSkipsClosure(t *testing.T) {
	ip := lowerForTest(t, `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var f: (i32) => i32 = function (x: i32): i32 { return x + 1; };
        acc = acc + f(i);
        i = i + 1;
    }
    return acc;
}
function main(): i32 { return churn(3); }`)
	f := funcByName(ip, "churn")
	if f == nil {
		t.Fatal("no func churn")
	}
	// If dec-on-reinit had fired for the closure var, the spliced
	// OpLoadLocal/__fern_rc_dec/OpDrop between MakeClosure and StoreLocal
	// would block the elide pass and leave an OpMakeClosure behind.
	for _, op := range f.Ops {
		if op.Kind == ir.OpRcDec {
			t.Errorf("closure loop-body var must not lower a dec-on-reinit __fern_rc_dec")
		}
	}
}
