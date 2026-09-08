package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func verifyCleanups(f *Func) error {
	if len(f.cleanups) == 0 && len(f.boundaries) == 0 && len(f.cleanupExits) == 0 {
		return nil
	}
	fail := func(message string) error { return fmt.Errorf("semir %s cleanup: %s", f.graph.Name, message) }
	dom := ssa.BuildDomTree(f.graph)
	boundaryFlow, err := verifyCleanupBoundaries(f, dom)
	if err != nil {
		return err
	}
	seen := make(map[*cleanupRegion]bool)
	for _, r := range f.cleanups {
		if r == nil || seen[r] || r.owner != f || r.body == nil || r.body == f ||
			r.body.program != f.program || len(r.body.cleanups) != 0 || boundaryFlow.points[r.register] == nil || !boundaryFlow.known[r.boundary] {
			return fail("invalid action, owner or registration identity")
		}
		seen[r] = true
		if err := Verify(r.body); err != nil {
			return err
		}
		if len(r.captures) != len(r.body.graph.Params) || r.exit == nil || r.yield == nil ||
			r.yield.Kind != ssa.OpTupleMake || len(r.yield.Args) != len(r.captures) {
			return fail("invalid action input/output interface")
		}
		exits := 0
		for _, block := range r.body.graph.Blocks {
			if block.Term.Kind != ssa.TermRet {
				continue
			}
			exits++
			if block != r.exit || block.Term.Value != r.yield.Result ||
				len(block.Ops) == 0 || block.Ops[len(block.Ops)-1] != r.yield {
				return fail("action must return only its final binding yield")
			}
		}
		if exits != 1 || r.body.values[r.yield.Result.ID].pos != r.pos {
			return fail("invalid action exit or source origin")
		}
		captures := make(map[BindingID]bool)
		for i, id := range r.captures {
			if id == 0 || int(id) > len(f.bindings) || captures[id] {
				return fail("invalid or duplicate captured binding identity")
			}
			captures[id] = true
			typ := f.bindings[id-1].typ
			param := r.body.graph.Params[i]
			if !ast.Equal(typ, r.body.values[param.ID].typ) || !ast.Equal(typ, r.body.values[r.yield.Args[i].ID].typ) ||
				(referenceBearing(typ) && r.body.modes[i] != ParamBorrow) {
				return fail("capture input/output differs from its binding contract")
			}
		}
		replays := make(map[*ssa.Block]bool)
		for _, replay := range r.replays {
			if boundaryFlow.points[replay] == nil || replays[replay] || !dom.Dominates(r.register, replay) {
				return fail("replay lacks a dominating registration or has invalid identity")
			}
			replays[replay] = true
		}
	}
	return verifyCleanupFlow(f, boundaryFlow)
}
