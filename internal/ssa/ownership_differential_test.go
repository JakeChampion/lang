package ssa_test

// The two ownership models, compared.
//
// `internal/ir` decides which parameters a function reclaims while it
// lowers, out of `p.Own`, `paramOwnedByDefault` and
// `rc.consumedParams`, and records the answer as `ir.Func.ParamConsumed`.
// `ssa.SolveOwnership` reaches the same kind of answer from the lifted
// form, by a fixpoint over call sites, having never seen any of those
// tables.
//
// Two independent models of one fact are only worth having if something
// compares them, which is what this does. It is deliberately NOT a
// pass/fail on equality: they answer subtly different questions and the
// disagreement is structured, measured and explained below. What it
// gates is the agreement RATE and the shape of the disagreement, so a
// change that quietly moves either shows up here rather than in a
// miscompile six months later.

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/ssa"
)

// lowerSelfHost lowers the self-host compiler, which is the biggest real
// program the repository has and the one goal 2 is about. The corpus
// would re-lower the stdlib once per fixture and count it every time.
func lowerSelfHost(t *testing.T) *ir.Program {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "self_host", "fern.fern")
	prog, _, err := modload.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("fold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	ip, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return ip
}

func TestOwnershipSolverAgreesWithTheLoweringsOwnVerdict(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the whole self-host compiler; not a -short test")
	}
	ip := lowerSelfHost(t)

	funcs := map[string]*ssa.Func{}
	irByName := map[string]*ir.Func{}
	for _, fn := range ip.Funcs {
		sf, err := ssa.LiftFromIR(fn)
		if err != nil {
			continue
		}
		funcs[fn.Name] = sf
		irByName[fn.Name] = fn
	}
	if len(funcs) < 1000 {
		t.Fatalf("only %d functions lifted; the differential is not covering the compiler", len(funcs))
	}
	sol := ssa.SolveOwnership(funcs)

	var agreeConsumed, agreeBorrowed, solverOnly, incumbentOnly, incumbentOnlyViaPhi int
	solverOnlyTypes := map[string]int{}
	incumbentOnlyTypes := map[string]int{}
	for name, sig := range sol.Sigs {
		irf, sf := irByName[name], funcs[name]
		if irf == nil || sf == nil || len(irf.ParamConsumed) == 0 {
			// Generated drop bodies and anything else built after
			// lowerFunc carry no verdict. They are not compared.
			continue
		}
		for i, o := range sig.Params {
			if !sig.Pointer[i] {
				continue
			}
			// Through ParamIRIndex, never i directly: a two-word
			// parameter becomes TWO SSA parameters, so the two
			// numberings stop agreeing the moment this is lifted at an
			// ABI where a string is a (data, len) pair.
			iri := i
			if len(sf.ParamIRIndex) == len(sig.Params) {
				iri = sf.ParamIRIndex[i]
			}
			if iri >= len(irf.ParamConsumed) || iri >= len(irf.Params) {
				continue
			}
			solver, incumbent := o == ssa.Consumed, irf.ParamConsumed[iri]
			typ := irf.Params[iri].Type.String()
			switch {
			case solver && incumbent:
				agreeConsumed++
			case !solver && !incumbent:
				agreeBorrowed++
			case solver:
				solverOnly++
				solverOnlyTypes[typ]++
			default:
				incumbentOnly++
				incumbentOnlyTypes[typ]++
				if reachesPhi(sf, sf.Params[i]) {
					incumbentOnlyViaPhi++
				}
			}
		}
	}
	total := agreeConsumed + agreeBorrowed + solverOnly + incumbentOnly
	if total == 0 {
		t.Fatal("no parameter was compared")
	}
	rate := float64(agreeConsumed+agreeBorrowed) / float64(total)
	t.Logf("%d pointer parameters compared: agree consumed=%d agree borrowed=%d "+
		"solver-only=%d incumbent-only=%d (%.2f%% agreement)",
		total, agreeConsumed, agreeBorrowed, solverOnly, incumbentOnly, 100*rate)
	t.Logf("  of the incumbent-only parameters, %d of %d reach a phi", incumbentOnlyViaPhi, incumbentOnly)
	logTop(t, "solver-only", solverOnlyTypes)
	logTop(t, "incumbent-only", incumbentOnlyTypes)

	// The floor, not the figure. Measured at 95.4% and it should move
	// with the compiler; what would be a defect is a COLLAPSE, which is
	// what a solver that stopped propagating, or a lowering that stopped
	// recording, would look like.
	const floor = 0.90
	if rate < floor {
		t.Errorf("the two ownership models agree on only %.2f%% of pointer parameters, "+
			"under the %.0f%% floor — one of them has changed its mind at scale",
			100*rate, 100*floor)
	}
	// Both directions have to stay populated. An empty bucket means one
	// model has been made to mirror the other, at which point comparing
	// them proves nothing.
	if agreeConsumed == 0 {
		t.Error("the two models agree on no consumed parameter at all")
	}
	if solverOnly == 0 || incumbentOnly == 0 {
		t.Errorf("a disagreement bucket is empty (solver-only=%d incumbent-only=%d) — "+
			"the models are no longer independent, or one has stopped answering",
			solverOnly, incumbentOnly)
	}
}

// The disagreement is structured rather than scattered, and the
// structure is the interesting part.
//
// INCUMBENT-ONLY is dominated by the big threaded state structs —
// asmcore__EmitState, parser__Par, ssa__BState, irlower__LowerState.
// Those are `computeConsumedParams` promotions: a parameter the body
// REASSIGNS is promoted callee-internally, and lowerFunc emits one entry
// retain to pay for the reassignment's overwrite release. Its own doc
// says "the call ABI is unchanged (the caller still passes the arg
// borrowed)" — so the two models are both right, about different
// questions. The solver answers the ABI question and sees a balanced
// body; the incumbent answers "does this function's exit sweep reclaim
// it".
//
// SOLVER-ONLY is dominated by `i32[]` — the assembler buffer threaded
// through the arm64 and x86 encoders. `arm64_cvtf(buf, …)` does not
// reassign `buf`; it passes it to `arm64_le32` and returns the result,
// so `computeConsumedParams` does not promote it and the incumbent
// reports borrowed. The solver marks it consumed because the demand
// propagates back from the callee that does release it.
//
// That shape reproduces clean, which is the answer the reading could
// not give. A program with the same structure — a struct field holding
// the buffer, a forwarder that passes it on without reassigning, a
// callee that appends — measures 11 allocs / 11 frees / 0 live bytes
// with `__rc_underflow_count()` at 0 and the sanitizer silent; the same
// program with a SECOND live owner across the call, read back
// afterwards so a released buffer would show, measures 103 / 103 / 0
// and still 0.
//
// The reason is the one that makes borrowing safe in the first place:
// the release the solver sees is a refcount DECREMENT whose reclamation
// is gated on uniqueness. At rc > 1 the append copies and frees
// nothing, so the other owner survives; at rc == 1 there is no other
// owner to lose. The solver cannot see that gate, so its extra
// "consumed" verdicts here are CONSERVATIVE rather than wrong.
//
// Conservative is still a real cost if anything ever lowers from this
// pass — a spurious consumed verdict makes callers retain, which leaks
// rather than corrupts. And the measurement is of a reproduction, not
// of all 122, so it is evidence and not a proof about each one.
func logTop(t *testing.T, label string, m map[string]int) {
	t.Helper()
	type kv struct {
		k string
		v int
	}
	var xs []kv
	for k, v := range m {
		xs = append(xs, kv{k, v})
	}
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].v != xs[j].v {
			return xs[i].v > xs[j].v
		}
		return xs[i].k < xs[j].k
	})
	for i, x := range xs {
		if i >= 6 {
			break
		}
		t.Logf("   %-15s %-40s %d", label, x.k, x.v)
	}
}

// reachesPhi reports whether p flows into a phi.
//
// `aliasesOf` stops at one, so a parameter reassigned in a loop can
// satisfy its ABI in a way the solver's predicate cannot see.
//
// How much of the incumbent-only bucket that explains depends on the
// population, and the two differ enough to be worth stating separately:
// 158 of 324 here, but 507 of 517 over the conformance corpus. The
// self-host's bucket is dominated instead by the threaded state structs
// described above, whose promotion is callee-internal and leaves the
// ABI alone — a different cause, already understood.
//
// Following the phi edge does NOT fix it, which is why this reports
// rather than patches. `demandsUnit` is `released && !retained`, and
// `!retained` is ANTI-monotone in the alias set: widening it finds new
// retains as readily as new releases. Adding the edge moved the corpus
// count the wrong way, 517 to 684, and overall agreement from 99.21% to
// 99.00%. Reconciling the two definitions needs per-path accounting,
// not a wider alias relation — the cost #7786 records for the
// certifier, and why Roc's arc_certify.zig carries a join lattice.
func reachesPhi(f *ssa.Func, p ssa.Value) bool {
	for _, b := range f.Blocks {
		for _, o := range b.Ops {
			if o.Kind != ssa.OpPhi {
				continue
			}
			for _, a := range o.Args {
				if a == p {
					return true
				}
			}
		}
	}
	return false
}
