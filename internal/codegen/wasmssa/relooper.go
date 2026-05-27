// CFG relooper — converts an arbitrary reducible SSA CFG into
// structured wasm control flow. Acts as the lowering backend
// for emitFunc.
//
// Algorithm overview:
//
//  1. Compute the dominator tree (for RPO) and natural loops.
//  2. For each natural loop (sorted innermost-first), reorder
//     RPO so the loop's body blocks sit consecutively starting
//     at the header. The bottom-up application preserves the
//     contiguity of inner loops as outer loops are reordered.
//  3. Compute the innermost containing loop for each block.
//     Compute, for each block needing a `block` wrap, the scope
//     at which it should open:
//     - non-loop blocks open at function start (outermost).
//     - blocks whose innermost loop is L open inside L's
//     `loop` wrap (just after L's `loop` opens).
//  4. Walk RPO. For each block B:
//     - Close any BlockScope on top targeting B.
//     - If B is a loop header H of loop L, open L's exit
//     `block` then `loop`, then L's body BlockScopes
//     (reverse RPO).
//     - Emit B's ops, lower its terminator (using scope stack
//     to compute label depths).
//     - If B is the last block of any loop(s) ending at this
//     position, close those loops' scopes (innermost first).
//  5. Terminators (TermRet / TermBr / TermBrIf) lower to
//     `return` / `br $depth` / `br_if $depth`. Phi-args at
//     branch targets are pre-written before the cond push.
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
	"sort"

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
//
// For skExit, `target` is the block control lands on AFTER
// the loop closes — i.e. the block at loopEnd[L]+1 in RPO.
// Branches from inside the loop body to that block lower to
// `br $exit_L`. When no such block exists (loop is the last
// thing in the function), target is nil and the ExitScope
// is unreachable as a direct target.
type scopeFrame struct {
	kind   scopeKind
	target *ssa.Block
}

// emitRelooper lowers `f` to wasm bytes using the relooper.
// Returns an error for features not yet supported (RetPair).
func emitRelooper(f *ssa.Func, ctx *emitCtx) ([]byte, error) {
	dom := ssa.BuildDomTree(f)
	rpo := dom.RPO()
	if len(rpo) == 0 {
		return nil, fmt.Errorf("relooper: empty RPO")
	}
	for _, b := range f.Blocks {
		found := false
		for _, r := range rpo {
			if r == b {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("relooper: unreachable block in f.Blocks")
		}
	}

	loops := ssa.Loops(f)
	// Sort innermost-first: a loop L_a is "inside" L_b iff
	// L_a.Header ∈ L_b.Body and L_a != L_b. Depth(L) = number
	// of loops that contain L. Higher depth → more nested →
	// innermost. Sort descending depth.
	sortedLoops := sortLoopsByDepth(loops)

	// Reorder RPO to make each loop's body contiguous. Apply
	// innermost-first so outer-loop reordering doesn't disrupt
	// inner-loop contiguity (inner blocks move as a unit).
	for _, L := range sortedLoops {
		inBody := map[*ssa.Block]bool{}
		for b := range L.Body {
			inBody[b] = true
		}
		rpo = reorderRPOForLoop(rpo, L.Header, inBody)
	}

	rpoIdx := map[*ssa.Block]int{}
	for i, b := range rpo {
		rpoIdx[b] = i
	}

	// Verify each loop's body is contiguous after reorder + record
	// each loop's first/last RPO index.
	loopStart := map[*ssa.Loop]int{}
	loopEnd := map[*ssa.Loop]int{}
	for _, L := range loops {
		s := rpoIdx[L.Header]
		e := s
		for b := range L.Body {
			if rpoIdx[b] > e {
				e = rpoIdx[b]
			}
		}
		for i := s; i <= e; i++ {
			if !L.Body[rpo[i]] {
				return nil, fmt.Errorf("relooper: loop body non-contiguous after reorder at index %d", i)
			}
		}
		loopStart[L] = s
		loopEnd[L] = e
	}

	// Innermost containing loop for each block (nil if none).
	innermost := map[*ssa.Block]*ssa.Loop{}
	for _, L := range sortedLoops { // innermost first → first claim wins
		for b := range L.Body {
			if _, ok := innermost[b]; !ok {
				innermost[b] = L
			}
		}
	}

	// Index loops by header for quick lookup.
	loopByHeader := map[*ssa.Block]*ssa.Loop{}
	for _, L := range loops {
		loopByHeader[L.Header] = L
	}

	// Compute scope-open lists.
	// - preOpens: non-loop blocks (innermost == nil) and not entry.
	// - loopBodyOpens[L]: blocks whose innermost loop is L,
	//   excluding L's header.
	var preOpens []*ssa.Block
	loopBodyOpens := map[*ssa.Loop][]*ssa.Block{}
	for i := 1; i < len(rpo); i++ {
		b := rpo[i]
		L := innermost[b]
		if L == nil {
			preOpens = append(preOpens, b)
			continue
		}
		if b == L.Header {
			continue // loop header gets LoopScope, not BlockScope
		}
		loopBodyOpens[L] = append(loopBodyOpens[L], b)
	}

	// Loops ending at each RPO position. When multiple loops
	// share an end position (nested), close innermost-first.
	loopsEndingAt := map[int][]*ssa.Loop{}
	for _, L := range loops {
		loopsEndingAt[loopEnd[L]] = append(loopsEndingAt[loopEnd[L]], L)
	}
	// depthOf: lower index in sortedLoops = innermost.
	depthOf := map[*ssa.Loop]int{}
	for i, L := range sortedLoops {
		depthOf[L] = i
	}
	for _, list := range loopsEndingAt {
		sort.Slice(list, func(i, j int) bool {
			return depthOf[list[i]] < depthOf[list[j]]
		})
	}

	var body []byte
	var stack []scopeFrame

	// Push non-loop BlockScopes at function start (reverse RPO so
	// later RPO is outermost).
	for i := len(preOpens) - 1; i >= 0; i-- {
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
		stack = append(stack, scopeFrame{kind: skBlock, target: preOpens[i]})
	}

	for i, b := range rpo {
		// Close any BlockScope on top targeting b.
		for len(stack) > 0 && stack[len(stack)-1].kind == skBlock && stack[len(stack)-1].target == b {
			body = inst.InstEnd(body)
			stack = stack[:len(stack)-1]
		}

		// Open loop scopes when entering a loop header.
		if L, ok := loopByHeader[b]; ok {
			// The ExitScope's "target" is the block control lands
			// on once we br out of the loop — the RPO position
			// right after loopEnd[L].
			var exitTarget *ssa.Block
			if loopEnd[L]+1 < len(rpo) {
				exitTarget = rpo[loopEnd[L]+1]
			}
			body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
			stack = append(stack, scopeFrame{kind: skExit, target: exitTarget})
			body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
			stack = append(stack, scopeFrame{kind: skLoop, target: L.Header})
			opens := loopBodyOpens[L]
			for j := len(opens) - 1; j >= 0; j-- {
				body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
				stack = append(stack, scopeFrame{kind: skBlock, target: opens[j]})
			}
		}

		// Emit ops + terminator.
		var err error
		body, err = emitStraightBlock(body, b, ctx)
		if err != nil {
			return nil, err
		}
		var nextBlock *ssa.Block
		if i+1 < len(rpo) {
			nextBlock = rpo[i+1]
		}
		body, err = lowerReloopTerm(body, b, stack, nextBlock, ctx)
		if err != nil {
			return nil, err
		}

		// Close LoopScope + ExitScope for any loops ending at i,
		// innermost first.
		for _, L := range loopsEndingAt[i] {
			if len(stack) < 2 ||
				stack[len(stack)-1].kind != skLoop ||
				stack[len(stack)-1].target != L.Header ||
				stack[len(stack)-2].kind != skExit {
				return nil, fmt.Errorf("relooper: scope-stack invariant violated at end of loop with header B%d", L.Header.ID)
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

// sortLoopsByDepth returns `loops` sorted innermost-first.
// "Inner" means L_a.Header ∈ L_b.Body (and L_a != L_b).
// Depth = number of loops containing this one; deeper first.
func sortLoopsByDepth(loops []*ssa.Loop) []*ssa.Loop {
	if len(loops) <= 1 {
		out := make([]*ssa.Loop, len(loops))
		copy(out, loops)
		return out
	}
	depth := make(map[*ssa.Loop]int, len(loops))
	for _, L := range loops {
		d := 0
		for _, other := range loops {
			if L == other {
				continue
			}
			if other.Body[L.Header] {
				d++
			}
		}
		depth[L] = d
	}
	out := make([]*ssa.Loop, len(loops))
	copy(out, loops)
	sort.SliceStable(out, func(i, j int) bool {
		return depth[out[i]] > depth[out[j]]
	})
	return out
}

// reorderRPOForLoop produces an RPO where the loop's body
// blocks (including the header) sit consecutively starting at
// the header. Standard RPO can interleave a non-loop block
// between loop-body blocks when both are reachable from the
// header in different traversal orders.
//
// The reorder: keep entries before the header unchanged; at
// the header, emit the header followed by all other body
// blocks in their RPO order; then emit the remaining (non-body)
// blocks in their RPO order. When applied innermost-first to a
// CFG with nested loops, each inner loop's already-contiguous
// body moves as a unit during the outer's reorder.
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
	for _, b := range rpo[headerPos+1:] {
		if inLoop[b] && b != header {
			out = append(out, b)
		}
	}
	for _, b := range rpo[headerPos+1:] {
		if !inLoop[b] {
			out = append(out, b)
		}
	}
	return out
}

// findScope returns the wasm label depth for branching to
// `target`, along with the kind of scope matched.
//
// Match precedence (innermost first):
//   - BlockScope whose target == target → forward branch to a
//     wrapped block.
//   - LoopScope whose header == target → back-edge / continue.
//   - ExitScope whose post-loop target == target → exit a loop
//     and land at the block that follows it (useful when the
//     post-loop block is itself a loop header or a function-
//     level wrap that lives below this exit on the stack).
func findScope(stack []scopeFrame, target *ssa.Block) (depth int, kind scopeKind, ok bool) {
	for i := len(stack) - 1; i >= 0; i-- {
		f := stack[i]
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
			return append(body, 0x1a), nil
		case tFall:
			body = append(body, 0x45) // i32.eqz
			return inst.InstBrIf(body, uint32(fDepth)), nil
		case fFall:
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
