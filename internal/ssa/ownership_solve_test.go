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

// twoArmFn builds `entry -> (armA | armB)`, each arm returning, with one
// pointer parameter. The arms are filled in by the caller.
func twoArmFn(name string) (f *Func, p Value, armA, armB *Block) {
	f = NewFunc(name)
	p = f.AddParam()
	f.ParamAddrs = []bool{true}
	entry := f.NewBlock()
	f.Entry = entry
	armA, armB = f.NewBlock(), f.NewBlock()
	cond := f.AddOp(entry, OpLoad, p)
	f.SetBrIf(entry, cond, armA, armB)
	armA.Term = Terminator{Kind: TermRet}
	armB.Term = Terminator{Kind: TermRet}
	return f, p, armA, armB
}

// The accounting is per path. One arm retains the parameter for a local
// use and gives the hold back; the other releases it outright. Counted
// over the whole body that is "retained and released" and reads as
// balanced, but the second arm ends holding less than it was handed,
// so the parameter is consumed.
//
// Under the owned-by-default model this is the ordinary shape, not a
// corner: `return a` on an owned parameter is a transfer inc beside the
// exit sweep's drop, and the arm that rebuilds instead just drops.
func TestSolveOwnershipCountsRetainsAndReleasesPerPath(t *testing.T) {
	f, p, armA, armB := twoArmFn("rebuild")
	held := call(f, armA, "__fern_rc_inc", p)
	callVoid(f, armA, "__fern_rc_dec", p)
	armA.Term = Terminator{Kind: TermRet, Value: held}
	callVoid(f, armB, "__fern_rc_dec", p)

	sol := SolveOwnership(map[string]*Func{"rebuild": f})
	if got := modeOf(t, sol, "rebuild", 0); got != Consumed {
		t.Errorf("a release on one arm is a demand however the other arm balances, got %v", got)
	}
}

// A store hands the unit to the container. Without a retain first the
// body has given away what it was handed; with one it has not.
func TestSolveOwnershipTreatsAStoreAsADischarge(t *testing.T) {
	moved := callFn("moved", 2, func(f *Func, b *Block, ps []Value) {
		f.AddOpNoResult(b, OpStore, ps[1], ps[0])
	})
	shared := callFn("shared", 2, func(f *Func, b *Block, ps []Value) {
		held := call(f, b, "__fern_rc_inc", ps[0])
		f.AddOpNoResult(b, OpStore, ps[1], held)
	})
	sol := SolveOwnership(map[string]*Func{"moved": moved, "shared": shared})
	if got := modeOf(t, sol, "moved", 0); got != Consumed {
		t.Errorf("a parameter stored without a retain is consumed, got %v", got)
	}
	if got := modeOf(t, sol, "shared", 0); got != Borrowed {
		t.Errorf("a parameter retained and then stored is borrowed, got %v", got)
	}
}

// A bare return of a parameter is not evidence either way: the typed
// lowering pairs it with an inc, and the untyped `usize` helpers hand
// back the raw word (`__map_get_or_impl` returns its fallback on the
// miss path). Reading it as a discharge called that fallback consumed,
// and the certifier then held it on the hit path.
func TestSolveOwnershipIgnoresABareReturnOfAParameter(t *testing.T) {
	f, p, armA, _ := twoArmFn("get_or")
	armA.Term = Terminator{Kind: TermRet, Value: p}

	sol := SolveOwnership(map[string]*Func{"get_or": f})
	if got := modeOf(t, sol, "get_or", 0); got != Borrowed {
		t.Errorf("a returned parameter with no rc traffic is borrowed, got %v", got)
	}
}

// A threaded accumulator: the parameter is retained at entry, one arm
// hands it to a consuming callee and continues with the FRESH result,
// the other keeps it, and the exit releases whichever the phi holds.
// On the rebuilt path the phi's release spends the fresh value's unit,
// not the parameter's, so the parameter is borrowed — the whole-body
// reading agreed, and a per-path count that ignored what the phi was
// fed on that edge called every such accumulator consumed.
func TestSolveOwnershipCreditsAFreshValueEnteringACarrierPhi(t *testing.T) {
	sink := callFn("push", 1, func(f *Func, b *Block, ps []Value) {
		callVoid(f, b, "__fern_rc_dec", ps[0])
		size := f.AddOp(b, OpConstInt)
		f.AddOp(b, OpAlloc, size)
	})
	f := NewFunc("thread")
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	entry, grow, keep, exit := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.Entry = entry
	held := call(f, entry, "__fern_rc_inc", p)
	cond := f.AddOp(entry, OpLoad, p)
	f.SetBrIf(entry, cond, grow, keep)
	size := f.AddOp(grow, OpConstInt)
	fresh := f.AddOp(grow, OpAlloc, size)
	callVoid(f, grow, "push", held)
	f.SetBr(grow, exit)
	f.SetBr(keep, exit)
	cur := f.AddPhi(exit, fresh, held)
	callVoid(f, exit, "__fern_rc_dec", cur)
	exit.Term = Terminator{Kind: TermRet}

	sol := SolveOwnership(map[string]*Func{"push": sink, "thread": f})
	if got := modeOf(t, sol, "thread", 0); got != Borrowed {
		t.Errorf("the phi's release on the rebuilt path spends the fresh unit, got %v", got)
	}
}

// The same phi handed BACK instead of released: `d = d.with(i, v);
// return d`, the shape every `.with` chain lowers to since the
// uniqueness branch moved into the IR (#8530). One arm keeps the
// parameter, the other hands it to a consuming callee and continues with
// the fresh result, and the exit retains the phi for the return beside
// the sweep's drop. The credit on the rebuilt edge has no release to pay
// for — the unit leaves through the return — so that arm consumed the
// parameter and nothing balanced it; the caller holds the result and
// must not touch the argument.
func TestSolveOwnershipReadsAReturnedRebuiltPhiAsConsumed(t *testing.T) {
	sink := callFn("cow", 1, func(f *Func, b *Block, ps []Value) {
		callVoid(f, b, "__fern_rc_dec", ps[0])
		size := f.AddOp(b, OpConstInt)
		f.AddOp(b, OpAlloc, size)
	})
	f := &Func{Name: "with", ReturnAddr: true}
	p := f.AddParam()
	f.ParamAddrs = []bool{true}
	entry, grow, keep, exit := f.NewBlock(), f.NewBlock(), f.NewBlock(), f.NewBlock()
	f.Entry = entry
	cond := f.AddOp(entry, OpLoad, p)
	f.SetBrIf(entry, cond, grow, keep)
	size := f.AddOp(grow, OpConstInt)
	fresh := f.AddOp(grow, OpAlloc, size)
	callVoid(f, grow, "cow", p)
	f.SetBr(grow, exit)
	f.SetBr(keep, exit)
	cur := f.AddPhi(exit, fresh, p)
	out := call(f, exit, "__fern_rc_inc", cur)
	callVoid(f, exit, "__fern_rc_dec", cur)
	exit.Term = Terminator{Kind: TermRet, Value: out}

	sol := SolveOwnership(map[string]*Func{"cow": sink, "with": f})
	if got := modeOf(t, sol, "with", 0); got != Consumed {
		t.Errorf("the rebuilt arm hands the parameter to a consuming callee and returns "+
			"the replacement, got %v", got)
	}
}
