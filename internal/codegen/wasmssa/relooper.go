// CFG relooper — converts an arbitrary reducible SSA CFG into
// structured wasm control flow. Acts as the fallback when none
// of the shape-specific classifiers (linear chain, if-else,
// etc.) recognise the function.
//
// Algorithm overview:
//
//  1. Compute the dominator tree (for RPO), natural loops, and
//     reject CFGs we don't yet handle (>1 natural loop, >1
//     back-edge per loop, non-contiguous loop body in RPO).
//  2. Open scopes:
//      - For every non-entry block NOT in any loop: a wasm
//        `block` wrap at function start (the "trivial" wrap).
//        Reverse RPO order so later-RPO targets are outer.
//      - For the loop header (if any): a wasm `block` (the
//        loop's exit label) followed by a `loop` (back-edge
//        target). The loop body blocks (other than the header)
//        get their own `block` wraps inside the `loop`,
//        opened in reverse RPO order.
//  3. Walk RPO. For each block B:
//      - If a BlockScope on top of the stack targets B, close
//        it (`end`) before emitting B.
//      - If B is the loop header, open the loop's scopes.
//      - Emit B's ops, then its terminator (using the scope
//        stack to compute label depths).
//      - If B is the last block of the loop, close `loop` then
//        the exit `block`.
//  4. Terminators (TermRet / TermBr / TermBrIf) lower to
//     `return` / `br $depth` / `br_if $depth` using scope
//     lookup. Phi-args at branch targets are pre-written
//     before the cond push.
//
// Scope kinds:
//   - BlockScope(target): wasm `block`. `br` to it lands at
//     `target`'s emission point.
//   - LoopScope(header): wasm `loop`. `br` to it restarts the
//     loop at `header` (back-edges become `br $loop`).
//   - ExitScope: wasm `block` around a `loop`. Branched to
//     implicitly via the post-loop BlockScope on the outer
//     stack; not a direct target.
//
// Phi-arg pre-writing for BOTH brif arms is safe because each
// phi has a unique result Value (and hence unique wasm local).
// Writing T's phi locals on the F-taken path is dead work but
// can't corrupt F's state.
package wasmssa

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ssa"
	"github.com/jakechampion/lang/internal/wasm/inst"
)

type scopeKind int

const (
	skBlock scopeKind = iota // wasm `block`
	skLoop                   // wasm `loop`
	skExit                   // wasm `block` wrapping a `loop`
)

// scopeFrame is one wasm structured-control entry on the
// emission scope stack.
type scopeFrame struct {
	kind   scopeKind
	target *ssa.Block // skBlock: target block; skLoop: header; skExit: nil
}

// emitRelooper lowers `f` to wasm bytes using the relooper.
// Returns an error if the function uses features not yet
// supported (>1 loop, multiple back-edges, non-contiguous loop
// body in RPO, RetPair).
func emitRelooper(f *ssa.Func, ctx *emitCtx) ([]byte, error) {
	dom := ssa.BuildDomTree(f)
	rpo := dom.RPO()
	if len(rpo) == 0 {
		return nil, fmt.Errorf("relooper: empty RPO")
	}
	rpoIdx := map[*ssa.Block]int{}
	for i, b := range rpo {
		rpoIdx[b] = i
	}
	for _, b := range f.Blocks {
		if _, ok := rpoIdx[b]; !ok {
			return nil, fmt.Errorf("relooper: unreachable block in f.Blocks")
		}
	}

	// Loop detection + validation.
	loops := ssa.Loops(f)
	var loop *ssa.Loop
	inLoop := map[*ssa.Block]bool{}
	loopStart, loopEnd := -1, -1
	if len(loops) > 1 {
		return nil, fmt.Errorf("relooper: %d natural loops; only 0-1 supported", len(loops))
	}
	if len(loops) == 1 {
		loop = loops[0]
		if len(loop.BackEdges) > 1 {
			return nil, fmt.Errorf("relooper: loop has %d back-edges; only 1 supported", len(loop.BackEdges))
		}
		for b := range loop.Body {
			inLoop[b] = true
		}
		// Standard RPO doesn't guarantee loop blocks sit
		// contiguously — a non-loop block may appear between
		// loop blocks (e.g. when the header dominates a
		// post-loop block whose RPO position lands before
		// some loop-body block). Reorder so the loop's body
		// blocks follow the header consecutively, preserving
		// the relative order of both the body blocks and the
		// non-loop blocks among themselves.
		rpo = reorderRPOForLoop(rpo, loop.Header, inLoop)
		rpoIdx = map[*ssa.Block]int{}
		for i, b := range rpo {
			rpoIdx[b] = i
		}
		loopStart = rpoIdx[loop.Header]
		loopEnd = loopStart
		for b := range loop.Body {
			if rpoIdx[b] > loopEnd {
				loopEnd = rpoIdx[b]
			}
		}
		for i := loopStart; i <= loopEnd; i++ {
			if !inLoop[rpo[i]] {
				return nil, fmt.Errorf("relooper: loop body still non-contiguous after reorder at index %d", i)
			}
		}
	}

	// Compute scope-open lists.
	// - preOpens: BlockScopes opened at function start (non-entry, non-loop blocks).
	// - loopBodyOpens: BlockScopes opened just after the loop's
	//   LoopScope (loop body blocks except the header), reverse-RPO
	//   for inner-most-first push order.
	var preOpens, loopBodyOpens []*ssa.Block
	for i := 1; i < len(rpo); i++ {
		b := rpo[i]
		if loop != nil && b == loop.Header {
			continue
		}
		if inLoop[b] {
			loopBodyOpens = append(loopBodyOpens, b)
		} else {
			preOpens = append(preOpens, b)
		}
	}

	var body []byte
	var stack []scopeFrame

	// Push pre-loop / non-loop BlockScopes (reverse RPO so later
	// RPO is outermost).
	for i := len(preOpens) - 1; i >= 0; i-- {
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
		stack = append(stack, scopeFrame{kind: skBlock, target: preOpens[i]})
	}

	for i, b := range rpo {
		// Close any BlockScope on top targeting b. (At most one;
		// each block is the target of at most one BlockScope.)
		for len(stack) > 0 && stack[len(stack)-1].kind == skBlock && stack[len(stack)-1].target == b {
			body = inst.InstEnd(body)
			stack = stack[:len(stack)-1]
		}

		// Open loop scopes when entering the loop header.
		if loop != nil && b == loop.Header {
			body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
			stack = append(stack, scopeFrame{kind: skExit})
			body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
			stack = append(stack, scopeFrame{kind: skLoop, target: loop.Header})
			// Inner BlockScopes for loop body blocks (reverse RPO).
			for j := len(loopBodyOpens) - 1; j >= 0; j-- {
				body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
				stack = append(stack, scopeFrame{kind: skBlock, target: loopBodyOpens[j]})
			}
		}

		// Emit ops.
		var err error
		body, err = emitStraightBlock(body, b, ctx)
		if err != nil {
			return nil, err
		}

		// Lower terminator using scope stack.
		var nextBlock *ssa.Block
		if i+1 < len(rpo) {
			nextBlock = rpo[i+1]
		}
		body, err = lowerReloopTerm(body, b, stack, nextBlock, ctx)
		if err != nil {
			return nil, err
		}

		// After the last loop block, close LoopScope + ExitScope.
		if loop != nil && i == loopEnd {
			if len(stack) < 2 ||
				stack[len(stack)-1].kind != skLoop ||
				stack[len(stack)-2].kind != skExit {
				return nil, fmt.Errorf("relooper: scope-stack invariant violated at loop end")
			}
			body = inst.InstEnd(body) // close loop
			stack = stack[:len(stack)-1]
			body = inst.InstEnd(body) // close exit block
			stack = stack[:len(stack)-1]
		}
	}

	if len(stack) != 0 {
		return nil, fmt.Errorf("relooper: %d scope(s) unclosed at function end", len(stack))
	}
	return body, nil
}

// reorderRPOForLoop produces an RPO where the loop's body
// blocks (including the header) sit consecutively starting at
// the header. Standard RPO can interleave a post-loop block
// between loop-body blocks when both are reachable from the
// header in different traversal orders.
//
// The reorder is: keep entries before the header unchanged;
// at the header's position, emit the header followed by all
// other body blocks in their RPO order; then emit the
// remaining (non-loop) blocks in their RPO order.
func reorderRPOForLoop(rpo []*ssa.Block, header *ssa.Block, inLoop map[*ssa.Block]bool) []*ssa.Block {
	out := make([]*ssa.Block, 0, len(rpo))
	headerPos := -1
	for i, b := range rpo {
		if b == header {
			headerPos = i
			break
		}
	}
	if headerPos < 0 {
		return rpo // shouldn't happen
	}
	for i := 0; i < headerPos; i++ {
		out = append(out, rpo[i])
	}
	out = append(out, header)
	// Loop body blocks (except header) in their RPO order.
	for _, b := range rpo[headerPos+1:] {
		if inLoop[b] && b != header {
			out = append(out, b)
		}
	}
	// Remaining non-loop blocks in their RPO order.
	for _, b := range rpo[headerPos+1:] {
		if !inLoop[b] {
			out = append(out, b)
		}
	}
	return out
}

// findScope returns the wasm label depth for branching to
// `target`, along with the kind of scope matched. For loop
// headers branched to (back-edges), returns the LoopScope.
// For normal forward branches, returns the BlockScope.
// ExitScopes aren't matched directly — they're only reached
// by br to a post-loop BlockScope sitting on the outer stack.
func findScope(stack []scopeFrame, target *ssa.Block) (depth int, kind scopeKind, ok bool) {
	for i := len(stack) - 1; i >= 0; i-- {
		f := stack[i]
		if f.kind == skExit {
			continue
		}
		if f.target == target {
			return len(stack) - 1 - i, f.kind, true
		}
	}
	return 0, 0, false
}

// lowerReloopTerm lowers b.Term given the current scope stack
// and the next RPO block (used for fallthrough into a target
// that doesn't yet have a scope — typically a loop header
// about to open its LoopScope).
//
// Depths come from findScope. BlockScope at depth 0 acts as
// fallthrough (the wrap's end is the next thing executed);
// LoopScope at depth 0 means "restart loop" — never a
// fallthrough. When findScope misses, the target must be the
// `nextBlock` (natural fallthrough into a loop header).
func lowerReloopTerm(body []byte, b *ssa.Block, stack []scopeFrame, nextBlock *ssa.Block, ctx *emitCtx) ([]byte, error) {
	switch b.Term.Kind {
	case ssa.TermRet:
		return emitRet(body, b.Term, ctx), nil
	case ssa.TermBr:
		target := b.Term.Target
		if target == nil {
			return nil, fmt.Errorf("relooper: TermBr with nil target")
		}
		body = writePhiArgs(body, target, b, ctx)
		depth, kind, ok := findScope(stack, target)
		if !ok {
			if target == nextBlock {
				return body, nil // fallthrough into next-RPO block (e.g. loop header)
			}
			return nil, fmt.Errorf("relooper: TermBr target not in scope stack and not next-RPO")
		}
		if kind == skBlock && depth == 0 {
			return body, nil // fallthrough
		}
		return inst.InstBr(body, uint32(depth)), nil
	case ssa.TermBrIf:
		if !b.Term.Cond.IsValid() {
			return nil, fmt.Errorf("relooper: TermBrIf with invalid cond")
		}
		t, fb := b.Term.True, b.Term.False
		if t == nil || fb == nil {
			return nil, fmt.Errorf("relooper: TermBrIf with nil arm")
		}
		// Pre-write phi-args for both arms (deduplicated).
		body = writePhiArgs(body, t, b, ctx)
		if fb != t {
			body = writePhiArgs(body, fb, b, ctx)
		}
		tDepth, tKind, tOK := findScope(stack, t)
		fDepth, fKind, fOK := findScope(stack, fb)
		tFall := (tOK && tKind == skBlock && tDepth == 0) || (!tOK && t == nextBlock)
		fFall := (fOK && fKind == skBlock && fDepth == 0) || (!fOK && fb == nextBlock)
		if !tOK && !tFall {
			return nil, fmt.Errorf("relooper: TermBrIf True target not in scope stack and not next-RPO")
		}
		if !fOK && !fFall {
			return nil, fmt.Errorf("relooper: TermBrIf False target not in scope stack and not next-RPO")
		}
		body = pushValue(body, b.Term.Cond, ctx)
		switch {
		case tFall && fFall:
			// Both arms fall through — drop cond.
			return append(body, 0x1a), nil
		case tFall:
			// True is fallthrough; jump to F when !cond.
			body = append(body, 0x45) // i32.eqz
			return inst.InstBrIf(body, uint32(fDepth)), nil
		case fFall:
			// False is fallthrough; jump to T when cond.
			return inst.InstBrIf(body, uint32(tDepth)), nil
		default:
			body = inst.InstBrIf(body, uint32(tDepth))
			return inst.InstBr(body, uint32(fDepth)), nil
		}
	case ssa.TermRetPair:
		return nil, fmt.Errorf("relooper: TermRetPair not yet supported")
	}
	return nil, fmt.Errorf("relooper: unknown terminator kind %v", b.Term.Kind)
}
