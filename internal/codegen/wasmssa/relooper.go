// CFG relooper — converts an arbitrary acyclic reducible SSA
// CFG into structured wasm control flow. Acts as the fallback
// when none of the shape-specific classifiers (linear chain,
// if-else, etc.) recognise the function.
//
// Algorithm — "trivial relooper":
//
//  1. Compute the dominator tree (for RPO) and reject any
//     function that has natural loops.
//  2. Open a `block` wrap for every non-entry block, in reverse
//     RPO order, before emitting anything. The wraps nest:
//     rpo[1] is innermost (depth 0 from entry's body), rpo[N-1]
//     is outermost.
//  3. Walk blocks in RPO. For each block: emit its ops, lower
//     its terminator (using RPO indices to compute label
//     depths), then close one `end` (the wrap whose label
//     pointed at the next block).
//  4. Terminators:
//      - TermRet → push value + return.
//      - TermBr → if target is the next RPO block, fall through;
//        otherwise emit `br $depth` where depth = rpoIdx[target]
//        - rpoIdx[current] - 1.
//      - TermBrIf → push cond. If True is next-RPO, flip cond
//        via i32.eqz + br_if to False's depth. If False is
//        next-RPO, br_if to True's depth. Otherwise br_if T,
//        br F.
//
// Loops + phis aren't yet supported. The relooper rejects
// functions containing either. Most CFG shapes the existing
// classifiers don't recognise (nested early-returns, switch
// trees, composed diamonds without phis) lift+optimize without
// phis at merge points, so this restricted relooper still
// unlocks a meaningful slice of real programs.
package wasmssa

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ssa"
	"github.com/jakechampion/lang/internal/wasm/inst"
)

// emitRelooper lowers `f` to wasm bytes using the trivial
// relooper. Returns an error if the function uses features
// not yet supported (loops, phis at merges, RetPair).
func emitRelooper(f *ssa.Func, ctx *emitCtx) ([]byte, error) {
	dom := ssa.BuildDomTree(f)
	rpo := dom.RPO()
	if len(rpo) == 0 {
		return nil, fmt.Errorf("relooper: empty RPO")
	}
	if loops := ssa.Loops(f); len(loops) > 0 {
		return nil, fmt.Errorf("relooper: loops not yet supported (%d natural loops)", len(loops))
	}
	rpoIdx := map[*ssa.Block]int{}
	for i, b := range rpo {
		rpoIdx[b] = i
	}
	for _, b := range f.Blocks {
		if _, ok := rpoIdx[b]; !ok {
			return nil, fmt.Errorf("relooper: unreachable block in f.Blocks")
		}
		for _, op := range b.Ops {
			if op.Kind == ssa.OpPhi {
				return nil, fmt.Errorf("relooper: phis at merges not yet supported")
			}
		}
	}

	var body []byte
	// Open one block-wrap per non-entry block in reverse RPO
	// order. Innermost (rpo[1]) opened last → depth 0 from
	// entry's terminator. Outermost (rpo[N-1]) opened first.
	for i := len(rpo) - 1; i >= 1; i-- {
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	}

	for i, b := range rpo {
		// Emit ops (phis already rejected above).
		var err error
		body, err = emitStraightBlock(body, b, ctx)
		if err != nil {
			return nil, err
		}
		// Lower terminator using RPO indices for depth math.
		body, err = lowerReloopTerm(body, b, i, rpoIdx, ctx)
		if err != nil {
			return nil, err
		}
		// Close one wrap unless this is the last block — its
		// natural fallthrough is the function-body end.
		if i < len(rpo)-1 {
			body = inst.InstEnd(body)
		}
	}
	return body, nil
}

// lowerReloopTerm lowers b.Term assuming b is at RPO index
// `curRPO` and the open scope stack is exactly the blocks
// rpo[curRPO+1 .. len(rpo)-1] in reverse order (innermost
// first). Depth from the current emission point to block
// rpo[j] (j > curRPO) is (j - curRPO - 1).
func lowerReloopTerm(body []byte, b *ssa.Block, curRPO int, rpoIdx map[*ssa.Block]int, ctx *emitCtx) ([]byte, error) {
	switch b.Term.Kind {
	case ssa.TermRet:
		return emitRet(body, b.Term, ctx), nil
	case ssa.TermBr:
		target := b.Term.Target
		if target == nil {
			return nil, fmt.Errorf("relooper: TermBr with nil target")
		}
		tRPO, ok := rpoIdx[target]
		if !ok || tRPO <= curRPO {
			return nil, fmt.Errorf("relooper: TermBr target isn't forward in RPO")
		}
		if tRPO == curRPO+1 {
			return body, nil // fall through
		}
		return inst.InstBr(body, uint32(tRPO-curRPO-1)), nil
	case ssa.TermBrIf:
		if !b.Term.Cond.IsValid() {
			return nil, fmt.Errorf("relooper: TermBrIf with invalid cond")
		}
		t, fb := b.Term.True, b.Term.False
		if t == nil || fb == nil {
			return nil, fmt.Errorf("relooper: TermBrIf with nil arm")
		}
		tRPO, tok := rpoIdx[t]
		fRPO, fok := rpoIdx[fb]
		if !tok || !fok || tRPO <= curRPO || fRPO <= curRPO {
			return nil, fmt.Errorf("relooper: TermBrIf arm isn't forward in RPO")
		}
		nextRPO := curRPO + 1
		body = pushValue(body, b.Term.Cond, ctx)
		switch {
		case tRPO == nextRPO && fRPO == nextRPO:
			// Both arms target the next RPO block — equivalent
			// to TermBr. Drop the cond and fall through.
			return append(body, 0x1a), nil
		case tRPO == nextRPO:
			// True is fallthrough; jump to F when !cond.
			body = append(body, 0x45) // i32.eqz
			return inst.InstBrIf(body, uint32(fRPO-curRPO-1)), nil
		case fRPO == nextRPO:
			// False is fallthrough; jump to T when cond.
			return inst.InstBrIf(body, uint32(tRPO-curRPO-1)), nil
		default:
			// Neither is fallthrough: br_if T, then br F.
			body = inst.InstBrIf(body, uint32(tRPO-curRPO-1))
			return inst.InstBr(body, uint32(fRPO-curRPO-1)), nil
		}
	case ssa.TermRetPair:
		return nil, fmt.Errorf("relooper: TermRetPair not yet supported")
	}
	return nil, fmt.Errorf("relooper: unknown terminator kind %v", b.Term.Kind)
}
