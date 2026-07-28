package ssa

// SCCP — Sparse Conditional Constant Propagation (Wegman &
// Zadeck, 1991). A combined dataflow analysis that propagates
// constant values + CFG reachability together, strictly more
// powerful than running Fold + FoldBranches independently.
//
// Each Value tracks a lattice point:
//
//	Top      — unknown / not yet seen
//	Const(c) — provably the constant c
//	Bottom   — not a single constant (depends on runtime)
//
// Each Block tracks reachability — initially only f.Entry is
// reachable, and CFG edges are added as the analysis proves
// each one is taken at runtime.
//
// Worklist algorithm:
//   - cfgWorklist: blocks newly marked reachable.
//   - valueWorklist: Values whose lattice point changed.
//
// On each step:
//   - When a block becomes reachable, evaluate its phi ops
//     (using meet over reachable-pred args), evaluate its
//     ordinary ops, and evaluate its terminator (which decides
//     which successors become reachable).
//   - When a Value moves down the lattice, re-evaluate every
//     op + terminator that uses it.
//
// At fixed point:
//   - Every Const Value's defining Op is rewritten in place to
//     OpConstInt/Bool/Float/String carrying the proven value.
//   - Every BrIf with a Const cond is rewritten to Br targeting
//     the taken successor; the untaken successor loses its
//     pred-entry + phi-arg slot.
//   - Unreachable blocks aren't dropped here; PruneUnreachable
//     handles that on a downstream pass.
//
// Strictly stronger than Fold + FoldBranches running
// separately because SCCP can prove that a phi merging values
// from multiple paths is constant when only ONE of those paths
// is actually reachable. Fold can't see that without
// FoldBranches first dropping the unreachable edges, and
// FoldBranches can't drop them without knowing the cond is
// const — which often depends on Fold first. SCCP solves both
// simultaneously.
//
// Returns the number of Values rewritten to constants — a
// quick proxy for "how much did this pass do?".
func SCCP(f *Func) int {
	if f == nil || f.Entry == nil {
		return 0
	}

	// Lattice state per Value.
	val := map[int32]latticeVal{}
	// Reachability per Block.
	reach := map[*Block]bool{}
	// CFG edges that have been processed (block, target) → ok.
	// Tracks which incoming edges are reachable, which feeds
	// the phi meet calculation.
	edgeReach := map[edge]bool{}
	// Pre-index defs and uses for fast lookups.
	defs := map[int32]*Op{}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Result.IsValid() {
				defs[op.Result.ID] = op
			}
		}
	}
	uses := BuildUses(f)

	// Params are unknown at compile time — Bottom.
	for _, p := range f.Params {
		if p.IsValid() {
			val[p.ID] = latticeBottom()
		}
	}

	// Worklists.
	var cfgWL []edge
	var valWL []Value

	markEdge := func(from, to *Block) {
		e := edge{from: from, to: to}
		if edgeReach[e] {
			return
		}
		edgeReach[e] = true
		cfgWL = append(cfgWL, e)
	}

	// Seed: entry is reachable via a synthetic edge.
	markEdge(nil, f.Entry)

	for len(cfgWL) > 0 || len(valWL) > 0 {
		if len(cfgWL) > 0 {
			e := cfgWL[len(cfgWL)-1]
			cfgWL = cfgWL[:len(cfgWL)-1]
			target := e.to
			firstTime := !reach[target]
			reach[target] = true
			if firstTime {
				// Visit every op in the newly-reachable block.
				for _, op := range target.Ops {
					reevalOp(op, target, val, defs, reach, edgeReach, &valWL)
				}
				reevalTerm(target, val, defs, &cfgWL, edgeReach, markEdge)
			} else {
				// Block already reachable; just re-eval phi ops
				// because a new incoming edge may have arrived.
				for _, op := range target.Ops {
					if op.Kind != OpPhi {
						break
					}
					reevalOp(op, target, val, defs, reach, edgeReach, &valWL)
				}
			}
			continue
		}
		// Process valueWL.
		v := valWL[len(valWL)-1]
		valWL = valWL[:len(valWL)-1]
		for _, u := range uses.Of(v) {
			if u.Op == nil {
				// Terminator use — re-eval its containing block's term.
				reevalTerm(u.Block, val, defs, &cfgWL, edgeReach, markEdge)
				continue
			}
			if !reach[u.Block] {
				continue
			}
			reevalOp(u.Op, u.Block, val, defs, reach, edgeReach, &valWL)
		}
	}

	// Rewrite phase: replace Const Values' defining Ops with
	// the matching const op kind, and rewrite BrIf-on-const
	// terminators.
	rewritten := 0
	for _, b := range f.Blocks {
		if !reach[b] {
			continue
		}
		for _, op := range b.Ops {
			if !op.Result.IsValid() {
				continue
			}
			lv, ok := val[op.Result.ID]
			if !ok || lv.tag != latticeTagConst {
				continue
			}
			// Skip if already a const of the matching kind.
			if op.Kind == lv.kind {
				continue
			}
			// Skip phis: rewriting a phi in place to a const
			// breaks the block-leading-phi invariant in
			// downstream consumers if not careful. The phi's
			// result is already aliased through the lattice;
			// uses will be replaced via the const def. Actually
			// the safer move: rewrite the phi in place to a
			// const, since SSA Result preservation lets uses
			// keep their Value reference.
			rewriteConst(op, lv)
			rewritten++
		}
		// Terminator rewrites.
		if b.Term.Kind == TermBrIf && b.Term.Cond.IsValid() {
			lv, ok := val[b.Term.Cond.ID]
			if ok && lv.tag == latticeTagConst && lv.kind == OpConstBool {
				tBlock, fBlock := b.Term.True, b.Term.False
				if tBlock == nil || fBlock == nil {
					continue
				}
				var taken, untaken *Block
				if lv.imm != 0 {
					taken, untaken = tBlock, fBlock
				} else {
					taken, untaken = fBlock, tBlock
				}
				b.Term = Terminator{Kind: TermBr, Target: taken}
				if taken != untaken {
					removePred(untaken, b)
				}
			}
		}
	}
	return rewritten
}

type edge struct {
	from, to *Block
}

const (
	latticeTagTop = iota
	latticeTagConst
	latticeTagBottom
)

// latticeVal is the per-Value SCCP state. tag selects the
// variant; for Const, kind + imm/f64/str carry the constant.
type latticeVal struct {
	tag  int
	kind OpKind // OpConstInt / OpConstBool / OpConstFloat / OpConstString
	imm  int64
	f64  float64
	str  string
}

func latticeTop() latticeVal    { return latticeVal{tag: latticeTagTop} }
func latticeBottom() latticeVal { return latticeVal{tag: latticeTagBottom} }
func latticeConstInt(v int64) latticeVal {
	return latticeVal{tag: latticeTagConst, kind: OpConstInt, imm: v}
}
func latticeConstBool(v bool) latticeVal {
	lv := latticeVal{tag: latticeTagConst, kind: OpConstBool}
	if v {
		lv.imm = 1
	}
	return lv
}
func latticeConstFloat(v float64) latticeVal {
	return latticeVal{tag: latticeTagConst, kind: OpConstFloat, f64: v}
}
func latticeConstString(v string) latticeVal {
	return latticeVal{tag: latticeTagConst, kind: OpConstString, str: v}
}

// equal reports whether two lattice values are the same.
func (a latticeVal) equal(b latticeVal) bool {
	if a.tag != b.tag {
		return false
	}
	if a.tag != latticeTagConst {
		return true
	}
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case OpConstInt, OpConstBool:
		return a.imm == b.imm
	case OpConstFloat:
		return a.f64 == b.f64
	case OpConstString:
		return a.str == b.str
	}
	return false
}

// meet combines two lattice values (greatest lower bound).
// Top ⊓ x = x; Const(c) ⊓ Const(c) = Const(c); anything else
// → Bottom.
func meet(a, b latticeVal) latticeVal {
	if a.tag == latticeTagTop {
		return b
	}
	if b.tag == latticeTagTop {
		return a
	}
	if a.tag == latticeTagBottom || b.tag == latticeTagBottom {
		return latticeBottom()
	}
	// Both Const.
	if a.equal(b) {
		return a
	}
	return latticeBottom()
}

// latticeOf returns the lattice value of a Value, treating
// const-op defs as constants (the analysis seeds Params as
// Bottom; everything else starts Top).
func latticeOf(v Value, val map[int32]latticeVal, defs map[int32]*Op) latticeVal {
	if !v.IsValid() {
		return latticeTop()
	}
	if lv, ok := val[v.ID]; ok {
		return lv
	}
	// Const op def — seed lazily.
	if def, ok := defs[v.ID]; ok {
		switch def.Kind {
		case OpConstInt:
			return latticeConstInt(def.Imm)
		case OpConstBool:
			return latticeConstBool(def.Imm != 0)
		case OpConstFloat:
			return latticeConstFloat(def.F64)
		case OpConstString:
			return latticeConstString(def.Str)
		}
	}
	return latticeTop()
}

// reevalOp recomputes the lattice value for op's Result based
// on current arg lattice values. If the new value differs
// from the cached one, records the new value and adds Result
// to the value-worklist.
func reevalOp(op *Op, b *Block, val map[int32]latticeVal, defs map[int32]*Op, reach map[*Block]bool, edgeReach map[edge]bool, valWL *[]Value) {
	if !op.Result.IsValid() {
		return
	}
	var newVal latticeVal
	switch {
	case op.Kind == OpPhi:
		newVal = evalPhi(op, b, val, defs, edgeReach)
	case IsConst(op.Kind):
		switch op.Kind {
		case OpConstInt:
			newVal = latticeConstInt(op.Imm)
		case OpConstBool:
			newVal = latticeConstBool(op.Imm != 0)
		case OpConstFloat:
			newVal = latticeConstFloat(op.F64)
		case OpConstString:
			newVal = latticeConstString(op.Str)
		}
	case !IsPure(op.Kind):
		// Impure ops (Call, Load, Alloc, etc.) — Bottom.
		newVal = latticeBottom()
	default:
		newVal = evalPureOp(op, val, defs)
	}
	old, seen := val[op.Result.ID]
	if seen && old.equal(newVal) {
		return
	}
	val[op.Result.ID] = newVal
	*valWL = append(*valWL, op.Result)
	if op.Result2.IsValid() {
		// Pair-returning ops (OpCallPair) — both results
		// become Bottom (we don't track them separately).
		val[op.Result2.ID] = latticeBottom()
		*valWL = append(*valWL, op.Result2)
	}
}

// evalPhi computes the meet of phi args from reachable preds.
func evalPhi(op *Op, b *Block, val map[int32]latticeVal, defs map[int32]*Op, edgeReach map[edge]bool) latticeVal {
	if len(op.Args) != len(b.Preds) {
		return latticeBottom()
	}
	result := latticeTop()
	for i, arg := range op.Args {
		pred := b.Preds[i]
		if !edgeReach[edge{from: pred, to: b}] {
			continue // unreachable incoming edge contributes nothing
		}
		result = meet(result, latticeOf(arg, val, defs))
		if result.tag == latticeTagBottom {
			break
		}
	}
	return result
}

// evalPureOp folds a pure binary/unary op when all args are
// Const; bails to Bottom when any arg is Bottom; otherwise
// returns Top (we'll re-eval when args change).
func evalPureOp(op *Op, val map[int32]latticeVal, defs map[int32]*Op) latticeVal {
	// Any Bottom arg → Bottom result.
	for _, a := range op.Args {
		if latticeOf(a, val, defs).tag == latticeTagBottom {
			return latticeBottom()
		}
	}
	// All args must be Const for us to fold.
	args := make([]latticeVal, len(op.Args))
	for i, a := range op.Args {
		args[i] = latticeOf(a, val, defs)
		if args[i].tag != latticeTagConst {
			return latticeTop()
		}
	}
	return foldOp(op.Kind, op.Width, args)
}

// foldOp evaluates a pure op with all-Const args. Mirrors the
// per-Kind logic in constfold.go but operates on lattice values
// rather than mutating an Op in place.
//
// `width` is the op's raw width, not a widened bool, because the
// integer and float paths read it differently: integers only care
// whether it is 64 (see foldint.go), while floats must distinguish
// f32 from f64 to round the folded result to the precision the
// backend would produce at runtime.
func foldOp(k OpKind, width int8, args []latticeVal) latticeVal {
	w64 := width == 64
	switch k {
	case OpNeg:
		if len(args) == 1 && args[0].kind == OpConstInt {
			return latticeConstInt(negAtWidth(w64, args[0].imm))
		}
	case OpNot:
		if len(args) == 1 && args[0].kind == OpConstBool {
			return latticeConstBool(args[0].imm == 0)
		}
	case OpFNeg:
		if len(args) == 1 && args[0].kind == OpConstFloat {
			return latticeConstFloat(-args[0].f64)
		}
	case OpSelect:
		if len(args) == 3 && args[0].kind == OpConstBool {
			if args[0].imm != 0 {
				return args[1]
			}
			return args[2]
		}
	}
	if len(args) != 2 {
		return latticeBottom()
	}
	if args[0].kind == OpConstInt && args[1].kind == OpConstInt {
		res, isBool, boolRes, ok := foldIntBinaryAtWidth(k, w64, args[0].imm, args[1].imm)
		if !ok {
			return latticeBottom()
		}
		if isBool {
			return latticeConstBool(boolRes)
		}
		return latticeConstInt(res)
	}
	if args[0].kind == OpConstBool && args[1].kind == OpConstBool {
		return foldBoolBinary(k, args[0].imm != 0, args[1].imm != 0)
	}
	if args[0].kind == OpConstFloat && args[1].kind == OpConstFloat {
		return foldFloatBinary(k, width, args[0].f64, args[1].f64)
	}
	return latticeBottom()
}

func foldBoolBinary(k OpKind, a, b bool) latticeVal {
	switch k {
	case OpEq:
		return latticeConstBool(a == b)
	case OpNe:
		return latticeConstBool(a != b)
	case OpAnd:
		return latticeConstBool(a && b)
	case OpOr:
		return latticeConstBool(a || b)
	case OpXor:
		return latticeConstBool(a != b)
	}
	return latticeBottom()
}

// foldFloatBinary folds a binary float op. An f32 op (width 32)
// rounds its result back to f32: folding at f64 precision and
// keeping the extra bits does not match what the expression
// computes at runtime, where every f32 op rounds (the backends
// emit an fcvt round trip for exactly this reason). Without it
// SCCP turned `((a - b) * c) * (d * (e * (g - h)))` over f32
// literals into -360517687, a value f32 cannot represent — its
// ulp at that magnitude is 32 — where the interpreter and both
// native backends produce -360517664.
//
// The comparisons need no rounding: they consume the operands as
// given and yield a bool.
func foldFloatBinary(k OpKind, width int8, a, b float64) latticeVal {
	round := func(v float64) float64 {
		if width == 32 {
			return float64(float32(v))
		}
		return v
	}
	switch k {
	case OpFAdd:
		return latticeConstFloat(round(a + b))
	case OpFSub:
		return latticeConstFloat(round(a - b))
	case OpFMul:
		return latticeConstFloat(round(a * b))
	case OpFDiv:
		return latticeConstFloat(round(a / b))
	case OpFEq:
		return latticeConstBool(a == b)
	case OpFNe:
		return latticeConstBool(a != b)
	case OpFLt:
		return latticeConstBool(a < b)
	case OpFLe:
		return latticeConstBool(a <= b)
	case OpFGt:
		return latticeConstBool(a > b)
	case OpFGe:
		return latticeConstBool(a >= b)
	}
	return latticeBottom()
}

// reevalTerm walks `b`'s terminator and marks successor edges
// as reachable based on the current cond's lattice point.
// Br: target is unconditionally reachable. BrIf with Const
// cond: only the matching target. BrIf with Bottom cond:
// both targets. BrIf with Top cond: neither (yet).
func reevalTerm(b *Block, val map[int32]latticeVal, defs map[int32]*Op, _ *[]edge, _ map[edge]bool, markEdge func(from, to *Block)) {
	switch b.Term.Kind {
	case TermBr:
		if b.Term.Target != nil {
			markEdge(b, b.Term.Target)
		}
	case TermBrIf:
		lv := latticeOf(b.Term.Cond, val, defs)
		switch lv.tag {
		case latticeTagConst:
			if lv.kind == OpConstBool {
				if lv.imm != 0 {
					if b.Term.True != nil {
						markEdge(b, b.Term.True)
					}
				} else {
					if b.Term.False != nil {
						markEdge(b, b.Term.False)
					}
				}
			} else {
				// Non-bool const cond — shouldn't happen on a
				// well-typed brif. Be safe and mark both.
				if b.Term.True != nil {
					markEdge(b, b.Term.True)
				}
				if b.Term.False != nil {
					markEdge(b, b.Term.False)
				}
			}
		case latticeTagBottom:
			if b.Term.True != nil {
				markEdge(b, b.Term.True)
			}
			if b.Term.False != nil {
				markEdge(b, b.Term.False)
			}
		}
		// Top: don't mark anything yet; revisit when cond changes.
	}
}

// rewriteConst mutates `op` in place to be a const op of the
// kind/value `lv` carries.
func rewriteConst(op *Op, lv latticeVal) {
	op.Kind = lv.kind
	op.Args = nil
	switch lv.kind {
	case OpConstInt, OpConstBool:
		op.Imm = lv.imm
		op.F64 = 0
		op.Str = ""
	case OpConstFloat:
		op.F64 = lv.f64
		op.Imm = 0
		op.Str = ""
	case OpConstString:
		op.Str = lv.str
		op.Imm = 0
		op.F64 = 0
	}
}
