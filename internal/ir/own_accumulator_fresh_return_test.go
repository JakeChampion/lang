package ir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// An `own` array accumulator threaded through RECURSION — `acc = into(l,
// acc); acc = into(r, acc); return acc;` — leaked one buffer per call when the
// recursion's other argument was borrow-tainted (a borrowed array param, or a
// binding of a borrowed non-uniform enum). rhsTainted's any-tainted-arg rule
// tainted the call result, `acc` lost freeEligible, and every `return acc`
// then retained as a borrow does with no sweep dec to balance it: the frame's
// own reference was stranded, and the rc-2 buffer forced the next append onto
// the copy path.
//
// The callee hands back the box the caller MOVED in, so findReturnsFreshBox
// credits an `own` param that is only ever rebound from owned values — the
// cow array mutators and calls that are themselves fresh-returning. The
// flat-loop shape (no recursion) never needed the credit; the same-shaped
// ordmap walker was clean only because its enum is uniform and so owned by
// default, which left nothing tainted to propagate.
const ownAccumulatorSrc = `enum N { L(i32), B(N, N) }
function into(n: N, own acc: i32[]): i32[] {
    match (n) {
        L(x) => { acc = acc.append(x); return acc; },
        B(l, r) => { acc = into(l, acc); acc = into(r, acc); return acc; },
    }
}
function idx(xs: i32[], i: i32, own acc: i32[]): i32[] {
    if (i >= xs.len()) { return acc; }
    acc = acc.append(xs[i]);
    acc = idx(xs, i + 1, acc);
    return acc;
}
function withp(xs: i32[], own acc: i32[]): i32[] {
    acc = acc.with(0, xs[0]);
    return acc;
}
function rebound(q: i32[], own acc: i32[]): i32[] {
    acc = q;
    return acc;
}
function borrowed_recv(q: i32[], own acc: i32[]): i32[] {
    acc = q.append(1);
    return acc;
}
function shadowed(n: N, own acc: i32[]): i32[] {
    match (n) {
        L(acc) => { return []; },
        B(l, r) => { return acc; },
    }
}
function borrowed_ret(q: i32[], own acc: i32[]): i32[] { return q; }
function main(): i32 { return 0; }`

func TestOwnAccumulatorReturnsFreshBox(t *testing.T) {
	prog, err := parser.Parse(ownAccumulatorSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	got := findReturnsFreshBox(prog, info, map[string]bool{}, map[string]bool{})
	want := map[string]bool{
		// The box returned is the one moved in, rebound only through owned
		// values: a recursive call to itself (fixpoint) and the cow mutators.
		"into":  true,
		"idx":   true,
		"withp": true,
		// Rebound from a borrowed param — the box handed back is the caller's.
		"rebound": false,
		// The cow result of a BORROWED receiver is that receiver's buffer.
		"borrowed_recv": false,
		// A binding shadows the param name; the return may read either.
		"shadowed": false,
		// A borrowed param returned is never fresh.
		"borrowed_ret": false,
	}
	for fn, w := range want {
		if got[fn] != w {
			t.Errorf("returnsFreshBox[%s] = %v, want %v", fn, got[fn], w)
		}
	}
}

// The plan-level consequence: with the recursion's result credited, `acc`
// stays freeEligible, and every `return acc` retain is paired with the exit
// sweep's __fern_arr_dec — the dec whose absence was the leak. The
// self-append's overwrite dec makes the arr_dec count strictly larger.
func TestOwnAccumulatorRecursionPlanAndSweep(t *testing.T) {
	dumps := map[string]string{}
	RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
	defer func() { RcPlanHook = nil }()
	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, ownAccumulatorSrc, ptrW)
		for _, fn := range []string{"into", "idx"} {
			if !hasPlanName(dumps[fn], "freeEligible", "acc") {
				t.Errorf("ptrW=%d: %s: acc is not freeEligible — the recursion's result taints the own accumulator; plan:\n%s", ptrW, fn, dumps[fn])
			}
			incs, decs := countRcIncs(prog, fn), countCalls(prog, fn, "__fern_arr_dec")
			if incs > decs {
				t.Errorf("ptrW=%d: %s: %d rc-incs but only %d __fern_arr_dec calls — a `return acc` retain with no sweep dec strands the frame's reference", ptrW, fn, incs, decs)
			}
		}
	}
}

// hasPlanName reports whether the rcPlan dump's `key:` line names `name`.
func hasPlanName(dump, key, name string) bool {
	for _, line := range strings.Split(dump, "\n") {
		if !strings.HasPrefix(line, key+": ") {
			continue
		}
		for _, n := range strings.Split(strings.TrimPrefix(line, key+": "), ",") {
			if n == name {
				return true
			}
		}
	}
	return false
}

// countCalls counts the OpCall ops naming `callee` in a function.
func countCalls(p *Program, fnName, callee string) int {
	n := 0
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		for _, op := range fn.Ops {
			if op.Kind == OpCallDirect && op.Str == callee {
				n++
			}
		}
	}
	return n
}
