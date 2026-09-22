// Conditions as branch chains: a boolean that an `if` or a `br_if` is
// about to test, built by `&&` / `||` / `!` from smaller booleans, becomes
// one branch per operand instead of a value each backend has to
// materialise and test again.
//
// The lowering emits `a && b` as `a; if i32 { b } else { 0 }`, and `a || b`
// as `a; if i32 { 1 } else { b }`, so after inlining an `if (is_digit(c))`
// carries the callee's `c >= 48 && c <= 57` in exactly that form. This pass
// finds the value an `if` or `br_if` consumes, reads it as a tree of those
// two shapes and `not`, and re-emits it as a chain: `a && b` branching on
// false is a branch on each operand's falsity in turn, and each backend
// already fuses a comparison with the branch behind it. Branching on the
// operator's own value — `a && b` on true, which a rotated loop's bottom
// test does — takes a block so the operand that decides early can skip the
// other's branch.
//
// A consuming `if` becomes blocks, so the chain has an end to branch to:
//
//	block               <- else target (only with an else)
//	  block             <- past-body target
//	    a; not; br_if   <- one per operand
//	    b; not; br_if
//	    body
//	    br              <- over the else arm (only with one)
//	  end
//	  else arm
//	end
//
// The rewrite needs the value's extent, so the operand stack is simulated
// through each function: an `if i32` whose scope holds nothing but the
// value it tests, at a point where the stack effect of every op since the
// enclosing scope's entry is known, is a candidate. Anything else is left
// as it is.

package ir

// ChainConditions rewrites every `&&` / `||` / `!` value that an `if` or
// `br_if` consumes into a chain of branches.
func ChainConditions(prog *Program) {
	sigs := buildFuncSigs(prog)
	shapes := NewCallShapes(prog)
	for _, fn := range prog.Funcs {
		for {
			ops, ok := chainOneCondition(fn.Ops, sigs, shapes)
			if !ok {
				break
			}
			fn.Ops = ops
		}
	}
}

// chainFrame is one open scope during the stack simulation: the operand
// stack height at its entry, whether that height is known, and the index
// after which the stack last held nothing above it.
type chainFrame struct {
	entry     int
	known     bool
	lastEmpty int
	results   int // values the scope leaves on the stack at its end
}

// chainOneCondition rewrites the first candidate in ops and reports
// whether it found one.
func chainOneCondition(ops []Op, sigs map[string]funcSig, shapes *CallShapes) ([]Op, bool) {
	frames := []chainFrame{{known: true}}
	cur, known := 0, true
	for i, op := range ops {
		top := &frames[len(frames)-1]
		if op.Kind == OpIf && op.I32 == BlockTypeI32 && known && cur == top.entry+1 && top.known {
			if out, ok := rewriteChain(ops, top.lastEmpty, i); ok {
				return out, true
			}
		}
		switch op.Kind {
		case OpBlock, OpLoop, OpIf:
			if op.Kind == OpIf {
				cur--
			}
			results, _ := blockSlots(op.I32)
			frames = append(frames, chainFrame{entry: cur, known: known, lastEmpty: i + 1, results: len(results)})
			continue
		case OpElse:
			cur, known = top.entry, top.known
			top.lastEmpty = i + 1
			continue
		case OpEnd:
			if len(frames) == 1 {
				return nil, false
			}
			frames = frames[:len(frames)-1]
			cur, known = top.entry+top.results, top.known
		case OpBrIf:
			cur--
		case OpBr, OpReturn, OpReturnVoid:
			known = false
		default:
			pops, pushes, ok := chainStackEffect(op, sigs, shapes)
			if !ok {
				known = false
			} else {
				cur += pushes - pops
			}
		}
		top = &frames[len(frames)-1]
		if known && cur == top.entry {
			top.lastEmpty = i + 1
		}
	}
	return nil, false
}

// chainStackEffect is opStackEffect with the calls resolved through
// CallShapes, which knows the backend-provided callees (`__str_idx` and
// its kind) that the signature table does not.
func chainStackEffect(op Op, sigs map[string]funcSig, shapes *CallShapes) (pops, pushes int, ok bool) {
	switch op.Kind {
	case OpCallDirect, OpCallDirectPair, OpCallClosureDirect, OpCallIndirect, OpCallDyn:
		var args int
		if op.Kind == OpCallDyn {
			// A dyn call's I32 is the method's vtable slot, not an
			// argument count: the arguments are the receiver-first
			// signature's, less the receiver, which sits below them as
			// its own word with the vtable word above (stackChecker.call).
			sig := op.Sig()
			if sig == nil || len(sig.Params) == 0 || anyErased(sig.Params) {
				return 0, 0, false
			}
			args = shapes.slotCount(sig.Params[1:]) + 2
		} else {
			var bail string
			args, bail = shapes.ArgSlots(op)
			if bail != "" {
				return 0, 0, false
			}
			if op.Kind == OpCallIndirect {
				args++ // the closure pair on top of the arguments
			}
		}
		results, bail := shapes.ResultSlots(op)
		if bail != "" {
			return 0, 0, false
		}
		return args, results, true
	}
	return opStackEffect(op, sigs, stringSlots(shapes.twoWordStr))
}

// condTree is a boolean value read as `&&`, `||`, `!` and atoms.
type condTree struct {
	kind        byte // 'a' atom, '&', '|', '!'
	ops         []Op // atom
	left, right *condTree
}

// parseCond reads ops, which leave one i32 on the stack, as a condition
// tree. It refuses a span whose atoms hold a branch out of the span, since
// the rewrite nests them differently.
func parseCond(ops []Op) (*condTree, bool) {
	n := len(ops)
	if n == 0 {
		return nil, false
	}
	if ops[n-1].Kind == OpNot {
		inner, ok := parseCond(ops[:n-1])
		if !ok {
			return nil, false
		}
		return &condTree{kind: '!', left: inner}, true
	}
	if ops[n-1].Kind == OpEnd {
		if i, j, ok := matchIfElse(ops, n-1); ok && ops[i].I32 == BlockTypeI32 {
			thenArm, elseArm := ops[i+1:j], ops[j+1:n-1]
			if len(elseArm) == 1 && elseArm[0].Kind == OpConstI32 && elseArm[0].I32 == 0 {
				return parseBinary('&', ops[:i], thenArm)
			}
			if len(thenArm) == 1 && thenArm[0].Kind == OpConstI32 && thenArm[0].I32 == 1 {
				return parseBinary('|', ops[:i], elseArm)
			}
		}
	}
	if escapesSpan(ops) {
		return nil, false
	}
	return &condTree{kind: 'a', ops: ops}, true
}

func parseBinary(kind byte, left, right []Op) (*condTree, bool) {
	l, ok := parseCond(left)
	if !ok {
		return nil, false
	}
	r, ok := parseCond(right)
	if !ok {
		return nil, false
	}
	return &condTree{kind: kind, left: l, right: r}, true
}

// matchIfElse finds the OpIf and OpElse whose OpEnd sits at index end.
// It refuses an if without an else.
func matchIfElse(ops []Op, end int) (ifAt, elseAt int, ok bool) {
	depth := 0
	elseAt = -1
	for k := end - 1; k >= 0; k-- {
		switch ops[k].Kind {
		case OpEnd:
			depth++
		case OpElse:
			if depth == 0 {
				elseAt = k
			}
		case OpBlock, OpLoop, OpIf:
			if depth == 0 {
				if ops[k].Kind != OpIf || elseAt < 0 {
					return 0, 0, false
				}
				return k, elseAt, true
			}
			depth--
		}
	}
	return 0, 0, false
}

// matchEnd finds the OpElse (or -1) and OpEnd of the scope opened at
// index open.
func matchEnd(ops []Op, open int) (elseAt, end int, ok bool) {
	depth := 0
	elseAt = -1
	for k := open + 1; k < len(ops); k++ {
		switch ops[k].Kind {
		case OpBlock, OpLoop, OpIf:
			depth++
		case OpElse:
			if depth == 0 {
				elseAt = k
			}
		case OpEnd:
			if depth == 0 {
				return elseAt, k, true
			}
			depth--
		}
	}
	return 0, 0, false
}

// escapesSpan reports whether a branch in ops targets a scope outside it.
func escapesSpan(ops []Op) bool {
	depth := int32(0)
	for _, op := range ops {
		switch op.Kind {
		case OpBlock, OpLoop, OpIf:
			depth++
		case OpEnd:
			depth--
		case OpBr, OpBrIf:
			if op.I32 >= depth {
				return true
			}
		}
	}
	return false
}

// shiftEscapes adds delta to every branch in ops that targets a scope
// outside it.
func shiftEscapes(ops []Op, delta int32) []Op {
	out := make([]Op, len(ops))
	copy(out, ops)
	depth := int32(0)
	for k := range out {
		switch out[k].Kind {
		case OpBlock, OpLoop, OpIf:
			depth++
		case OpEnd:
			depth--
		case OpBr, OpBrIf:
			if out[k].I32 >= depth {
				out[k].I32 += delta
			}
		}
	}
	return out
}

// chainEmitter appends a condition's branch chain. `extra` counts the
// blocks it has opened around the current point, so a target given as a
// depth relative to the chain's start stays right as blocks open.
type chainEmitter struct {
	out   []Op
	extra int32
}

// branch emits the branches for c: to the scope at depth target, relative
// to the point the chain started (negative for a block the emitter opened
// itself), when c is `when`, falling through otherwise.
func (e *chainEmitter) branch(c *condTree, target int32, when bool) {
	switch c.kind {
	case '!':
		e.branch(c.left, target, !when)
	case '&', '|':
		if (c.kind == '&') != when {
			e.branch(c.left, target, when)
			e.branch(c.right, target, when)
			return
		}
		e.out = append(e.out, Op{Kind: OpBlock, I32: BlockTypeVoid})
		e.extra++
		e.branch(c.left, -e.extra, !when) // the block just opened
		e.branch(c.right, target, when)
		e.out = append(e.out, Op{Kind: OpEnd})
		e.extra--
	default:
		e.out = append(e.out, c.ops...)
		if !when {
			e.out = append(e.out, Op{Kind: OpNot})
		}
		e.out = append(e.out, Op{Kind: OpBrIf, I32: target + e.extra})
	}
}

// rewriteChain rewrites the value in ops[start:ifAt] ⋯ End and its consumer.
// ifAt is the `if i32` whose scope holds the value's right operand.
func rewriteChain(ops []Op, start, ifAt int) ([]Op, bool) {
	_, end, ok := matchEnd(ops, ifAt)
	if !ok {
		return nil, false
	}
	// The consumer: a run of nots, then an if or a br_if.
	c := end + 1
	nots := 0
	for c < len(ops) && ops[c].Kind == OpNot {
		nots++
		c++
	}
	if c >= len(ops) {
		return nil, false
	}
	tree, ok := parseCond(ops[start : end+1])
	if !ok || tree.kind == 'a' {
		return nil, false
	}
	flip := nots%2 == 1
	var e chainEmitter
	e.out = append(e.out, ops[:start]...)
	switch ops[c].Kind {
	case OpBrIf:
		// Branch to the consumer's target when the value is true.
		e.branch(tree, ops[c].I32, !flip)
		e.out = append(e.out, ops[c+1:]...)
		return e.out, true
	case OpIf:
		if ops[c].I32 != BlockTypeVoid {
			return nil, false
		}
		elseAt, bodyEnd, ok := matchEnd(ops, c)
		if !ok {
			return nil, false
		}
		hasElse := elseAt >= 0
		body := ops[c+1 : bodyEnd]
		var elseArm []Op
		if hasElse {
			body = ops[c+1 : elseAt]
			elseArm = ops[elseAt+1 : bodyEnd]
		}
		if hasElse {
			e.out = append(e.out, Op{Kind: OpBlock, I32: BlockTypeVoid})
		}
		e.out = append(e.out, Op{Kind: OpBlock, I32: BlockTypeVoid})
		// Past the body when the value is false. The past-body block is
		// the innermost scope, at depth 0 from the chain's start.
		e.branch(tree, 0, flip)
		if hasElse {
			// The body now sits one scope deeper than it did in the if.
			e.out = append(e.out, shiftEscapes(body, 1)...)
			e.out = append(e.out, Op{Kind: OpBr, I32: 1})
		} else {
			e.out = append(e.out, body...)
		}
		e.out = append(e.out, Op{Kind: OpEnd})
		if hasElse {
			e.out = append(e.out, elseArm...)
			e.out = append(e.out, Op{Kind: OpEnd})
		}
		e.out = append(e.out, ops[bodyEnd+1:]...)
		return e.out, true
	}
	return nil, false
}
