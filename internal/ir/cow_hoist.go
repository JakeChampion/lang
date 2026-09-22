package ir

import "github.com/jakechampion/lang/internal/ast"

// HoistUniquenessGuards takes the mutate-or-copy decision of an in-loop
// `a = a.with(i, v)` out of the loop. emitCowInplace guards every such
// write with an rc.is_unique test whose copy arm calls the CoW helper;
// inside a loop that asks the same question on every iteration, and after
// the first write the answer cannot change: the copy arm leaves the local
// holding a fresh buffer, and a body that only reads the array's elements,
// takes its length and writes back through the same sites never gives that
// buffer a second owner.
//
// Where every iteration reaches the write, the whole guard moves to just
// before the `loop`, on the local itself, and each site keeps its
// bounds-checked store and nothing else. The guard is reached under the
// same condition the loop is, and a copy the body would have made on its
// first iteration is made before it instead, which value semantics cannot
// observe. Where a write sits under a condition, or past an exit of the
// loop, the guard stays at the site behind a flag: the first write the
// loop reaches runs it and sets the flag, and every later one skips it —
// so a loop that never writes never tests or copies, as before.
func HoistUniquenessGuards(prog *Program) {
	sigs := buildFuncSigs(prog)
	shapes := NewCallShapes(prog)
	for _, fn := range prog.Funcs {
		for {
			ops, ok := hoistOneGuard(fn, sigs, shapes)
			if !ok {
				break
			}
			fn.Ops = ops
		}
	}
}

// cowGuardLen is the length of the sequence emitCowInplace emits after the
// receiver is loaded: tee S, is_unique, if, else, load S, stride, call,
// store S, end.
const cowGuardLen = 9

// cowSite is one guarded `.with` inside a loop body: `at` indexes the
// receiver's `local.load recv` and the guard follows it.
type cowSite struct {
	at      int
	recv    int32
	scratch int32
	stride  int32
	helper  string
}

// matchCowSite recognises the guard at ops[at+1:] over the receiver loaded
// at ops[at].
func matchCowSite(ops []Op, at int) (cowSite, bool) {
	if at+cowGuardLen >= len(ops) || ops[at].Kind != OpLoadLocal {
		return cowSite{}, false
	}
	g := ops[at+1 : at+1+cowGuardLen]
	if g[0].Kind != OpTeeLocal || g[1].Kind != OpRcIsUnique || g[2].Kind != OpIf || g[3].Kind != OpElse ||
		g[4].Kind != OpLoadLocal || g[4].I32 != g[0].I32 || g[5].Kind != OpConstI32 ||
		g[6].Kind != OpCallDirect || !isCowHelper(g[6].Str) ||
		g[7].Kind != OpStoreLocal || g[7].I32 != g[0].I32 || g[8].Kind != OpEnd {
		return cowSite{}, false
	}
	return cowSite{at: at, recv: ops[at].I32, scratch: g[0].I32, stride: g[5].I32, helper: g[6].Str}, true
}

func isCowHelper(name string) bool {
	return name == "__fern_arr_cow_inplace" || name == "__fern_arr_cow_inplace_ptr"
}

// hoistOneGuard rewrites the first loop, innermost first, that holds a
// guarded site whose receiver the body treats as its own.
func hoistOneGuard(fn *Func, sigs map[string]funcSig, shapes *CallShapes) ([]Op, bool) {
	ops := fn.Ops
	for l := len(ops) - 1; l >= 0; l-- {
		if ops[l].Kind != OpLoop {
			continue
		}
		end := matchingScopeEnd(ops, l)
		if end < 0 {
			continue
		}
		byRecv := map[int32][]cowSite{}
		var order []int32
		for j := l + 1; j < end; j++ {
			if s, ok := matchCowSite(ops, j); ok {
				if _, seen := byRecv[s.recv]; !seen {
					order = append(order, s.recv)
				}
				byRecv[s.recv] = append(byRecv[s.recv], s)
			}
		}
		for _, recv := range order {
			sites := byRecv[recv]
			if !receiverStaysOwned(ops, l+1, end, recv, sites, sigs, shapes) {
				continue
			}
			always := true
			for _, s := range sites {
				if !siteAlwaysReached(ops, l, s.at) {
					always = false
				}
			}
			if always {
				return hoistGuard(ops, l, recv, sites), true
			}
			flag := int32(len(fn.Params)) + int32(len(fn.Locals)) + int32(len(fn.ScratchTypes))
			fn.ScratchTypes = append(fn.ScratchTypes, ast.NumberType{})
			return hoistTest(ops, l, recv, flag, sites), true
		}
	}
	return nil, false
}

// siteAlwaysReached reports whether every iteration of the loop opened at
// l runs the site at `at`: no `if` or inner loop is open around it, and no
// branch before it can leave a scope that is still open at it, or the
// function.
func siteAlwaysReached(ops []Op, l, at int) bool {
	type scope struct {
		kind    OpKind
		skipped bool // a branch before the site targets this scope
	}
	var open []scope
	for k := l + 1; k < at; k++ {
		switch ops[k].Kind {
		case OpBlock, OpLoop, OpIf:
			open = append(open, scope{kind: ops[k].Kind})
		case OpElse:
			// The if stays open; an else after the site is not reached here.
		case OpEnd:
			if len(open) == 0 {
				return false
			}
			open = open[:len(open)-1]
		case OpBr, OpBrIf:
			d := int(ops[k].I32)
			if d >= len(open) {
				return false // leaves the loop
			}
			open[len(open)-1-d].skipped = true
		case OpReturn, OpReturnVoid, OpReturnPair:
			return false
		}
	}
	for _, sc := range open {
		if sc.kind != OpBlock || sc.skipped {
			return false
		}
	}
	return true
}

// hoistTest keeps each site's guard behind a flag that says the receiver
// is known to be its own. The flag is cleared before the loop at l, the
// first site the loop reaches runs the guard as before and sets it, and
// every later site skips the guard on the flag alone — so a loop that
// never writes pays one store, and one that writes on every iteration
// pays the guard once.
func hoistTest(ops []Op, l int, recv, flag int32, sites []cowSite) []Op {
	out := make([]Op, 0, len(ops)+2+len(sites)*6)
	out = append(out, ops[:l]...)
	out = append(out, Op{Kind: OpConstI32, I32: 0}, Op{Kind: OpStoreLocal, I32: flag})
	siteAt := map[int]cowSite{}
	for _, s := range sites {
		siteAt[s.at] = s
	}
	for j := l; j < len(ops); j++ {
		s, ok := siteAt[j]
		if !ok {
			out = append(out, ops[j])
			continue
		}
		// load recv; store S; if !flag { <the guard on S>; flag = 1 }
		out = append(out,
			Op{Kind: OpLoadLocal, I32: recv},
			Op{Kind: OpStoreLocal, I32: s.scratch},
			Op{Kind: OpLoadLocal, I32: flag},
			Op{Kind: OpNot},
			Op{Kind: OpIf, I32: BlockTypeVoid},
			Op{Kind: OpLoadLocal, I32: s.scratch},
		)
		out = append(out, ops[j+2:j+1+cowGuardLen]...) // is_unique ... end
		out = append(out,
			Op{Kind: OpConstI32, I32: 1},
			Op{Kind: OpStoreLocal, I32: flag},
			Op{Kind: OpEnd},
		)
		j += cowGuardLen
	}
	return out
}

// receiverStaysOwned reports whether every use of `recv` and of the sites'
// scratch slots inside ops[from:to] is one that keeps the buffer's single
// owner: the receiver load of a site, an element address, a length read, or
// a site's write-back of its scratch into the receiver.
func receiverStaysOwned(ops []Op, from, to int, recv int32, sites []cowSite, sigs map[string]funcSig, shapes *CallShapes) bool {
	siteAt := map[int]cowSite{}
	scratch := map[int32]bool{}
	for _, s := range sites {
		siteAt[s.at] = s
		scratch[s.scratch] = true
		if s.helper != sites[0].helper || s.stride != sites[0].stride {
			return false
		}
	}
	for j := from; j < to; j++ {
		op := ops[j]
		if op.Kind != OpLoadLocal && op.Kind != OpStoreLocal && op.Kind != OpTeeLocal {
			continue
		}
		if op.I32 != recv && !scratch[op.I32] {
			continue
		}
		if s, ok := siteAt[j]; ok {
			j += cowGuardLen // the guard's own tee, load and store of the scratch
			_ = s
			continue
		}
		switch op.Kind {
		case OpLoadLocal:
			// A scratch loaded to be written back: `load S; tee recv; drop`.
			if scratch[op.I32] && j+2 < to && ops[j+1].Kind == OpTeeLocal && ops[j+1].I32 == recv && ops[j+2].Kind == OpDrop {
				j += 2
				continue
			}
			if !readsElementOrLength(ops, j, to, sigs, shapes) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// readsElementOrLength reports whether the value ops[j] loads is consumed
// by an element-address helper or by the `- 4; load` of a length read.
func readsElementOrLength(ops []Op, j, to int, sigs map[string]funcSig, shapes *CallShapes) bool {
	k := consumerOf(ops, j, to, sigs, shapes)
	if k < 0 {
		return false
	}
	c := ops[k]
	if c.Kind == OpCallDirect && isElementAddressHelper(c.Str) {
		return true
	}
	return c.Kind == OpSub && k == j+2 && ops[j+1].Kind == OpConstI32 && ops[j+1].I32 == 4 && k+1 < to && ops[k+1].Kind == OpLoad
}

func isElementAddressHelper(name string) bool {
	switch name {
	case "__arr_idx", "__arr_idx_nc", "__arr_idx_1", "__arr_idx_1_nc", "__arr_idx_8", "__arr_idx_8_nc", "__arr_idx_16", "__arr_idx_16_nc":
		return true
	}
	return false
}

// consumerOf is the index of the op that pops the value ops[j] pushes, or
// -1 when a scope boundary or an unknown call intervenes.
func consumerOf(ops []Op, j, to int, sigs map[string]funcSig, shapes *CallShapes) int {
	depth := 1
	for k := j + 1; k < to; k++ {
		if endsLoopHeader(ops[k].Kind) {
			return -1
		}
		pops, pushes, ok := chainStackEffect(ops[k], sigs, shapes)
		if !ok {
			return -1
		}
		if pops >= depth {
			return k
		}
		depth += pushes - pops
	}
	return -1
}

// hoistGuard deletes the sites' guards and puts one guard on the receiver
// before the loop at l.
func hoistGuard(ops []Op, l int, recv int32, sites []cowSite) []Op {
	out := make([]Op, 0, len(ops)+cowGuardLen)
	out = append(out, ops[:l]...)
	s0 := sites[0]
	out = append(out,
		Op{Kind: OpLoadLocal, I32: recv},
		Op{Kind: OpRcIsUnique, Str: "__fern_rc_is_unique", I32: 1},
		Op{Kind: OpIf, I32: BlockTypeVoid},
		Op{Kind: OpElse},
		Op{Kind: OpLoadLocal, I32: recv},
		Op{Kind: OpConstI32, I32: s0.stride},
		Op{Kind: OpCallDirect, Str: s0.helper, Width: ResAddr, I32: 2},
		Op{Kind: OpStoreLocal, I32: recv},
		Op{Kind: OpEnd},
	)
	skip := map[int]bool{}
	store := map[int]bool{}
	for _, s := range sites {
		// Keep `load recv` and make the tee a store, since the test that
		// consumed the tee'd copy is gone; drop the test, the arms and the end.
		store[s.at+1] = true
		for k := s.at + 2; k <= s.at+cowGuardLen; k++ {
			skip[k] = true
		}
	}
	for j := l; j < len(ops); j++ {
		if skip[j] {
			continue
		}
		op := ops[j]
		if store[j] {
			op.Kind = OpStoreLocal
		}
		out = append(out, op)
	}
	return out
}
