package semir

import (
	"fmt"
	"maps"
	"slices"

	"github.com/jakechampion/lang/internal/ssa"
)

// planProgramUnits checks the entire experimental closed-module counted-result
// ABI. No caller is certified merely because its callee has a typed signature.
func planProgramUnits(p *Program) (map[*Func]*functionUnits, error) {
	if err := VerifyProgram(p); err != nil {
		return nil, err
	}
	plans := make(map[*Func]*functionUnits, len(p.funcs))
	for _, f := range p.funcs {
		plan, err := planFunctionUnits(f)
		if err != nil {
			return nil, err
		}
		plans[f] = plan
	}
	if err := verifyProgramUnits(p, plans); err != nil {
		return nil, err
	}
	return plans, nil
}

// verifyProgramUnits must be repeated after graph or plan transformations and
// before eventual RC lowering. Verifying just a caller cannot establish its
// callees' counted-result obligations, including in a recursive component.
func verifyProgramUnits(p *Program, plans map[*Func]*functionUnits) error {
	if err := VerifyProgram(p); err != nil {
		return err
	}
	if len(plans) != len(p.funcs) {
		return fmt.Errorf("semir units: incomplete closed-module plans")
	}
	for _, f := range p.funcs {
		if plans[f] == nil || plans[f].function != f {
			return fmt.Errorf("semir units: missing or foreign function plan")
		}
		if err := verifyFunctionUnits(plans[f], plans); err != nil {
			return err
		}
	}
	return nil
}

// verifyFunctionUnits replays unit balance independently of the planner's last
// use/move decisions. Each block starts with its explicit ownership invariant;
// every incoming edge must establish exactly that invariant. This inductive
// check covers arbitrary loop iterations without bounded path enumeration.
// It certifies this abstract unit protocol, not physical RC code or uniqueness.
func verifyFunctionUnits(p *functionUnits, closed map[*Func]*functionUnits) error {
	if p == nil || p.function == nil {
		return fmt.Errorf("semir units: missing function plan")
	}
	f := p.function
	effects, err := ownershipEffects(f)
	if err != nil {
		return err
	}
	// Do not trust cached lifetime facts from a potentially modified plan.
	l := analyzeLifetimes(f, effects)
	fail := func(format string, args ...any) error {
		return fmt.Errorf("semir units %s: %s", f.graph.Name, fmt.Sprintf(format, args...))
	}
	invariant := func(b *ssa.Block) map[int32]bool {
		state := make(map[int32]bool)
		for id := range l.live.LiveIn[b] {
			if l.owned[id] {
				state[id] = true
			}
		}
		for _, op := range b.Ops {
			if op.Kind == ssa.OpPhi && l.owned[op.Result.ID] {
				state[op.Result.ID] = true
			}
		}
		if b == f.graph.Entry {
			for _, v := range f.graph.Params {
				if l.owned[v.ID] {
					state[v.ID] = true
				}
			}
		}
		return state
	}
	read := func(state map[int32]bool, v ssa.Value) error {
		if !v.IsValid() {
			return nil
		}
		if v.Func != f.graph || f.values[v.ID].typ == nil {
			return fail("invalid value identity %s", v)
		}
		if l.owned[v.ID] && !state[v.ID] {
			return fail("read of %s without its counted unit", v)
		}
		for _, anchor := range l.dependencies[v.ID] {
			if l.owned[anchor.ID] && !state[anchor.ID] {
				return fail("borrow %s outlives container anchor %s", v, anchor)
			}
		}
		return nil
	}
	drop := func(state map[int32]bool, values []ssa.Value) error {
		for _, v := range values {
			if v.Func != f.graph || !state[v.ID] {
				return fail("drop of %s without its counted unit", v)
			}
			delete(state, v.ID)
		}
		return nil
	}
	supply := func(state map[int32]bool, step unitStep, want []unitSupply, copied []ssa.Value) error {
		if len(step.supplies) != len(want) || !slices.Equal(step.copyElements, copied) {
			return fail("unit supplies disagree with semantic effect contract")
		}
		for i, got := range step.supplies {
			if got.value != want[i].value || got.slot != want[i].slot {
				return fail("unit supply targets the wrong value or slot")
			}
			if err := read(state, got.value); err != nil {
				return err
			}
			switch got.mode {
			case unitRetain:
			case unitMove:
				if !state[got.value.ID] {
					return fail("move of %s without its counted unit", got.value)
				}
			case unitImmortal:
				if !l.immortal[got.value.ID] {
					return fail("%s is not certified immortal", got.value)
				}
			default:
				return fail("invalid unit supply mode")
			}
		}
		for _, array := range copied {
			if err := read(state, array); err != nil {
				return err
			}
		}
		// All acquisitions precede every transfer. Detect a duplicate move
		// even when multiple slots refer to the very same SSA value.
		for _, got := range step.supplies {
			if got.mode == unitMove {
				if !state[got.value.ID] {
					return fail("counted unit for %s transferred twice", got.value)
				}
				delete(state, got.value.ID)
			}
		}
		return nil
	}
	nBlocks, nOps, nEdges, nReturns := 0, 0, 0, 0
	seenEdges := make(map[flowEdge]bool)
	for _, b := range f.graph.Blocks {
		if !l.reachable[b] {
			continue
		}
		nBlocks++
		state := invariant(b)
		if _, ok := p.entry[b]; !ok {
			return fail("missing block entry plan")
		}
		if err := drop(state, p.entry[b]); err != nil {
			return err
		}
		for _, op := range b.Ops {
			if op.Kind == ssa.OpPhi {
				continue
			}
			nOps++
			step, ok := p.ops[op]
			if !ok {
				return fail("missing operation plan")
			}
			for _, arg := range op.Args {
				if err := read(state, arg); err != nil {
					return err
				}
			}
			effect := effects.ops[op]
			if effect.callee != nil {
				callee := closed[effect.callee]
				if callee == nil || callee.function != effect.callee {
					return fail("callee has no plan in the closed module")
				}
			}
			var want []unitSupply
			var copied []ssa.Value
			for i, input := range effect.inputs {
				if input.consume || (input.store == storeValue && input.counted) {
					want = append(want, unitSupply{value: input.value, slot: i})
				}
				if input.store == storeArrayElements && input.counted {
					copied = append(copied, input.value)
				}
			}
			if err := supply(state, step, want, copied); err != nil {
				return err
			}
			if op.Kind == ssa.OpSemanticCall {
				for _, input := range effect.inputs {
					if !input.consume {
						if err := read(state, input.value); err != nil {
							return err
						}
					}
				}
			}
			if l.owned[op.Result.ID] {
				if state[op.Result.ID] {
					return fail("result overwrites an unreleased counted unit")
				}
				state[op.Result.ID] = true
			}
			if err := drop(state, step.drops); err != nil {
				return err
			}
		}
		if err := read(state, b.Term.Cond); err != nil {
			return err
		}
		if b.Term.Kind == ssa.TermRet {
			nReturns++
			step, ok := p.returns[b]
			if !ok {
				return fail("missing return plan")
			}
			if err := read(state, b.Term.Value); err != nil {
				return err
			}
			var want []unitSupply
			if referenceBearing(f.result) {
				want = []unitSupply{{value: b.Term.Value}}
			}
			if err := supply(state, step, want, nil); err != nil {
				return err
			}
			if err := drop(state, step.drops); err != nil {
				return err
			}
			if len(state) != 0 {
				return fail("return leaves counted units unreleased")
			}
		}
		for _, succ := range b.Succs() {
			edge := flowEdge{b, succ}
			if seenEdges[edge] {
				continue
			}
			seenEdges[edge] = true
			nEdges++
			step, ok := p.edges[edge]
			if !ok {
				return fail("missing edge plan")
			}
			next := maps.Clone(state)
			pi := slices.Index(succ.Preds, b)
			var want []unitSupply
			for i, op := range succ.Ops {
				if op.Kind != ssa.OpPhi {
					continue
				}
				if err := read(next, op.Args[pi]); err != nil {
					return err
				}
				if l.owned[op.Result.ID] {
					want = append(want, unitSupply{value: op.Args[pi], slot: i})
				}
			}
			if err := supply(next, step, want, nil); err != nil {
				return err
			}
			if err := drop(next, step.drops); err != nil {
				return err
			}
			// Parallel phi assignment follows all acquisitions/transfers and
			// drops. Back-edge results may reuse the previous iteration's ID.
			for _, required := range want {
				result := succ.Ops[required.slot].Result
				if next[result.ID] {
					return fail("phi overwrites an unreleased counted unit")
				}
				next[result.ID] = true
			}
			if !maps.Equal(next, invariant(succ)) {
				return fail("edge does not establish successor's counted-unit invariant")
			}
		}
	}
	if len(p.entry) != nBlocks || len(p.ops) != nOps || len(p.edges) != nEdges || len(p.returns) != nReturns {
		return fail("plan contains foreign or unreachable entries")
	}
	return nil
}
