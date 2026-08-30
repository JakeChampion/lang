package ssa

import (
	"strings"
	"testing"
)

// callFn builds a one-block function with `n` pointer parameters whose
// body is the ops `build` appends.
func callFn(name string, n int, build func(f *Func, b *Block, ps []Value)) *Func {
	f := NewFunc(name)
	ps := make([]Value, n)
	f.ParamAddrs = make([]bool, n)
	for i := range ps {
		ps[i] = f.AddParam()
		f.ParamAddrs[i] = true
	}
	b := f.NewBlock()
	f.Entry = b
	build(f, b, ps)
	b.Term = Terminator{Kind: TermRet}
	return f
}

func call(f *Func, b *Block, callee string, args ...Value) Value {
	v := f.AddOp(b, OpCall, args...)
	b.Ops[len(b.Ops)-1].Str = callee
	return v
}

func callVoid(f *Func, b *Block, callee string, args ...Value) {
	o := f.AddOpNoResult(b, OpCall, args...)
	o.Str = callee
}

func modeOf(t *testing.T, sol Solution, fn string, i int) ParamOwnership {
	t.Helper()
	sig, ok := sol.Sigs[fn]
	if !ok {
		t.Fatalf("%s: no signature", fn)
	}
	if i >= len(sig.Params) {
		t.Fatalf("%s: no parameter %d", fn, i)
	}
	return sig.Params[i]
}

// The leaf: a body that releases its parameter with no matching retain
// demands a unit from its caller.
func TestSolveOwnershipReadsTheLocalDemand(t *testing.T) {
	sink := callFn("sink", 1, func(f *Func, b *Block, ps []Value) {
		callVoid(f, b, "__fern_rc_dec", ps[0])
	})
	read := callFn("read", 1, func(f *Func, b *Block, ps []Value) {
		f.AddOp(b, OpLoad, ps[0])
	})
	sol := SolveOwnership(map[string]*Func{"sink": sink, "read": read})
	if got := modeOf(t, sol, "sink", 0); got != Consumed {
		t.Errorf("a released parameter is consumed, got %v", got)
	}
	if got := modeOf(t, sol, "read", 0); got != Borrowed {
		t.Errorf("a parameter the body only reads is borrowed, got %v", got)
	}
}

// The lesson of the 921: a retain and a release together demand
// nothing. The body borrowed the value, held it for a local use, and
// gave the hold back.
func TestSolveOwnershipTreatsABalancedPairAsABorrow(t *testing.T) {
	f := callFn("hold", 1, func(f *Func, b *Block, ps []Value) {
		inc := call(f, b, "__fern_rc_inc", ps[0])
		callVoid(f, b, "__fern_rc_dec", inc)
	})
	sol := SolveOwnership(map[string]*Func{"hold": f})
	if got := modeOf(t, sol, "hold", 0); got != Borrowed {
		t.Errorf("a balanced retain/release demands no unit, got %v", got)
	}
}

// The interprocedural edge, and the reason this is a fixpoint rather
// than a walk: `outer` does nothing to its parameter itself. It hands
// it to `middle`, which hands it to `sink`, which releases it. The
// demand has to travel two hops back.
func TestSolveOwnershipPropagatesThroughTheCallGraph(t *testing.T) {
	sink := callFn("sink", 1, func(f *Func, b *Block, ps []Value) {
		callVoid(f, b, "__fern_rc_dec", ps[0])
	})
	middle := callFn("middle", 1, func(f *Func, b *Block, ps []Value) {
		callVoid(f, b, "sink", ps[0])
	})
	outer := callFn("outer", 1, func(f *Func, b *Block, ps []Value) {
		callVoid(f, b, "middle", ps[0])
	})
	sol := SolveOwnership(map[string]*Func{"sink": sink, "middle": middle, "outer": outer})
	for _, fn := range []string{"sink", "middle", "outer"} {
		if got := modeOf(t, sol, fn, 0); got != Consumed {
			t.Errorf("%s: the demand must reach every caller, got %v", fn, got)
		}
	}
	if sol.Rounds < 2 {
		t.Errorf("a two-hop demand cannot settle in one round, got %d", sol.Rounds)
	}
}

// Only the argument position that is consumed is consumed. A callee
// that releases its first parameter says nothing about its second.
func TestSolveOwnershipPropagatesPerArgumentPosition(t *testing.T) {
	sink := callFn("sink", 2, func(f *Func, b *Block, ps []Value) {
		callVoid(f, b, "__fern_rc_dec", ps[0])
		f.AddOp(b, OpLoad, ps[1])
	})
	// caller passes its own p0 as sink's SECOND argument and p1 as the first.
	caller := callFn("caller", 2, func(f *Func, b *Block, ps []Value) {
		callVoid(f, b, "sink", ps[1], ps[0])
	})
	sol := SolveOwnership(map[string]*Func{"sink": sink, "caller": caller})
	if got := modeOf(t, sol, "caller", 1); got != Consumed {
		t.Errorf("caller's p1 lands in sink's consumed position, got %v", got)
	}
	if got := modeOf(t, sol, "caller", 0); got != Borrowed {
		t.Errorf("caller's p0 lands in sink's borrowed position, got %v", got)
	}
}

// Mutual recursion is a cycle in the call graph. The lattice only ever
// moves up, so the solve settles rather than spinning.
func TestSolveOwnershipTerminatesOnACycle(t *testing.T) {
	ping := callFn("ping", 1, func(f *Func, b *Block, ps []Value) {
		callVoid(f, b, "pong", ps[0])
	})
	pong := callFn("pong", 1, func(f *Func, b *Block, ps []Value) {
		callVoid(f, b, "ping", ps[0])
		callVoid(f, b, "__fern_rc_dec", ps[0])
	})
	sol := SolveOwnership(map[string]*Func{"ping": ping, "pong": pong})
	for _, fn := range []string{"ping", "pong"} {
		if got := modeOf(t, sol, fn, 0); got != Consumed {
			t.Errorf("%s: got %v", fn, got)
		}
	}
}

// A callee with no signature borrows every argument, and says so. The
// count is the coverage figure, and hiding it would make an assumption
// look like a derivation.
func TestSolveOwnershipCountsAnUnknownCallee(t *testing.T) {
	f := callFn("caller", 1, func(f *Func, b *Block, ps []Value) {
		callVoid(f, b, "somewhere_else", ps[0])
	})
	sol := SolveOwnership(map[string]*Func{"caller": f})
	if got := modeOf(t, sol, "caller", 0); got != Borrowed {
		t.Errorf("an unknown callee borrows, got %v", got)
	}
	if sol.Opaque != 1 || len(sol.OpaqueCallees) != 1 || sol.OpaqueCallees[0] != "somewhere_else" {
		t.Errorf("the unknown callee must be named, got %d across %v", sol.Opaque, sol.OpaqueCallees)
	}
}

// A runtime helper rcsigs.go records as moving counts in a shape it
// cannot express is NOT the same as one that moves none. Reading the
// first as inert is the one case where the assumption is known wrong,
// so it is counted — with the reason, so the gap can be read.
func TestSolveOwnershipSeparatesUnmodelledHelpersFromInertOnes(t *testing.T) {
	f := callFn("thread", 2, func(f *Func, b *Block, ps []Value) {
		call(f, b, "__fern_arr_push_grow", ps[0], ps[1])
		call(f, b, "__str_concat", ps[1])
	})
	sol := SolveOwnership(map[string]*Func{"thread": f})
	if sol.Calls != 2 {
		t.Fatalf("want two call sites, got %d", sol.Calls)
	}
	if sol.Opaque != 1 || len(sol.OpaqueCallees) != 1 {
		t.Fatalf("only the unmodelled helper is opaque, got %d across %v", sol.Opaque, sol.OpaqueCallees)
	}
	if !strings.HasPrefix(sol.OpaqueCallees[0], "__fern_arr_push_grow (") {
		t.Errorf("the opaque entry must name the helper and its reason, got %q", sol.OpaqueCallees[0])
	}
}

// Scalars are reported for completeness and mean nothing. A parameter
// reference counting cannot apply to is never consumed.
func TestSolveOwnershipLeavesNonPointerParametersAlone(t *testing.T) {
	f := callFn("scalar", 1, func(f *Func, b *Block, ps []Value) {
		callVoid(f, b, "__fern_rc_dec", ps[0])
	})
	f.ParamAddrs = []bool{false}
	sol := SolveOwnership(map[string]*Func{"scalar": f})
	if got := modeOf(t, sol, "scalar", 0); got != Borrowed {
		t.Errorf("a scalar parameter carries no unit, got %v", got)
	}
}

// The solve ranges a map, so its visit order is randomised per run. The
// RESULT is order-independent because the lattice is monotone — but
// Rounds is only order-independent because the names are sorted first,
// and Rounds is the number a regression test would hold.
func TestSolveOwnershipIsDeterministic(t *testing.T) {
	build := func() map[string]*Func {
		sink := callFn("sink", 1, func(f *Func, b *Block, ps []Value) {
			callVoid(f, b, "__fern_rc_dec", ps[0])
		})
		mid := callFn("mid", 1, func(f *Func, b *Block, ps []Value) {
			callVoid(f, b, "sink", ps[0])
		})
		top := callFn("top", 1, func(f *Func, b *Block, ps []Value) {
			callVoid(f, b, "mid", ps[0])
		})
		return map[string]*Func{"sink": sink, "mid": mid, "top": top}
	}
	want := SolveOwnership(build())
	for i := 0; i < 20; i++ {
		got := SolveOwnership(build())
		if got.Rounds != want.Rounds {
			t.Fatalf("round %d: settled in %d rounds, first run took %d", i, got.Rounds, want.Rounds)
		}
		for n, sig := range want.Sigs {
			for j, m := range sig.Params {
				if got.Sigs[n].Params[j] != m {
					t.Fatalf("round %d: %s param %d disagrees with the first run", i, n, j)
				}
			}
		}
	}
}
