package checker

import (
	"slices"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func StmtReferencesName(st ast.Node, name string) bool {
	found := false
	ast.Walk(st, func(n ast.Node) bool {
		if lm, ok := n.(*ast.Lambda); ok {
			found = found || lambdaFreeNames(lm)[name]
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

type IdentOrder struct {
	Idx    map[*ast.Ident]int
	last   map[string]int
	byName map[string][]*ast.Ident
	flow   *flowTree
}

// IdentOrderOf numbers the identifiers of body in source order. The else arm
// of a let-else is the last arm of its match, but the source writes it first.
func IdentOrderOf(body ast.Node) IdentOrder {
	o := IdentOrder{Idx: map[*ast.Ident]int{}, last: map[string]int{}, byName: map[string][]*ast.Ident{}}
	n := 0
	var visit func(node ast.Node) bool
	visit = func(node ast.Node) bool {
		switch x := node.(type) {
		case *ast.Ident:
			n++
			o.Idx[x] = n
			o.byName[x.Name] = append(o.byName[x.Name], x)
			if n > o.last[x.Name] {
				o.last[x.Name] = n
			}
		case *ast.Match:
			els := len(x.Arms) - 1
			if els < 1 || !letElseDivergentArm(x, els) {
				return true
			}
			ast.Walk(x.Tag, visit)
			for _, a := range append([]*ast.MatchArm{x.Arms[els]}, x.Arms[:els]...) {
				if a.Guard != nil {
					ast.Walk(a.Guard, visit)
				}
				if a.Body != nil {
					ast.Walk(a.Body, visit)
				}
			}
			return false
		}
		return true
	}
	ast.Walk(body, visit)
	if st, ok := body.(ast.Stmt); ok {
		o.flow = newFlowTree(st, o.Idx)
	}
	return o
}

// IsLast reports whether no run reads the name of id after id: each later
// read is in a branch that the run of id does not take, or after a return.
func (o IdentOrder) IsLast(id *ast.Ident) bool {
	if o.Idx[id] == o.last[id.Name] {
		return true
	}
	return o.isLastAmong(id, o.byName[id.Name])
}

// isLastAmong reports whether no run reaches an identifier of mentions after id.
func (o IdentOrder) isLastAmong(id *ast.Ident, mentions []*ast.Ident) bool {
	node := -2
	for _, later := range mentions {
		if o.Idx[later] <= o.Idx[id] {
			continue
		}
		if o.flow == nil {
			return false
		}
		if node == -2 {
			node = o.flow.nodeAt(o.Idx[id])
		}
		if o.flow.reaches(node, o.Idx[later]) {
			return false
		}
	}
	return true
}

// flowTree holds the statements of a function as a tree, for the question
// whether a run can read one identifier after another, as LuFlow of
// compiler/checker.fern does. Node n is a statement: first[n] and last[n]
// are the numbers of its first and its last identifier, or -1. ends[n] is
// true when no run goes on after it, looping[n] for a loop, kids[n] are its
// blocks, and block[n] and slot[n] say where it is. Block b holds the nodes
// blocks[b] in order, and owner[b] is the node that holds it, or -1.
type flowTree struct {
	first, last []int
	ends        []bool
	looping     []bool
	kids        [][]int
	block, slot []int
	blocks      [][]int
	owner       []int
}

func newFlowTree(body ast.Stmt, idx map[*ast.Ident]int) *flowTree {
	f := &flowTree{}
	f.addBlock(flowStmts(body), -1, idx)
	return f
}

// flowStmts is the statements of a block, or the one statement st.
func flowStmts(st ast.Stmt) []ast.Stmt {
	if st == nil {
		return nil
	}
	if b, ok := st.(*ast.Block); ok {
		if b == nil {
			return nil
		}
		return b.Stmts
	}
	return []ast.Stmt{st}
}

func (f *flowTree) addBlock(stmts []ast.Stmt, owner int, idx map[*ast.Ident]int) {
	b := len(f.blocks)
	f.blocks = append(f.blocks, nil)
	f.owner = append(f.owner, owner)
	var nodes []int
	for k, st := range stmts {
		n := len(f.first)
		lo, hi := -1, -1
		ast.Walk(st, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok {
				if at, numbered := idx[id]; numbered {
					if lo < 0 || at < lo {
						lo = at
					}
					if at > hi {
						hi = at
					}
				}
			}
			return true
		})
		f.first = append(f.first, lo)
		f.last = append(f.last, hi)
		f.ends = append(f.ends, flowEnds(st))
		f.looping = append(f.looping, flowLoop(st))
		f.kids = append(f.kids, nil)
		f.block = append(f.block, b)
		f.slot = append(f.slot, k)
		nodes = append(nodes, n)
		var kids []int
		for _, body := range flowBodies(st) {
			kids = append(kids, len(f.blocks))
			f.addBlock(body, n, idx)
		}
		f.kids[n] = kids
	}
	f.blocks[b] = nodes
}

// flowBodies is the blocks of st that the flow follows: the arms of an if or
// a match, and the body of a loop.
func flowBodies(st ast.Stmt) [][]ast.Stmt {
	switch x := st.(type) {
	case *ast.If:
		return [][]ast.Stmt{flowStmts(x.Then), flowStmts(x.Else)}
	case *ast.Match:
		var out [][]ast.Stmt
		for _, a := range x.Arms {
			out = append(out, flowStmts(a.Body))
		}
		return out
	case *ast.While:
		return [][]ast.Stmt{flowStmts(x.Body)}
	case *ast.Loop:
		return [][]ast.Stmt{flowStmts(x.Body)}
	case *ast.For:
		return [][]ast.Stmt{flowStmts(x.Body)}
	case *ast.ForEach:
		return [][]ast.Stmt{flowStmts(x.Body)}
	}
	return nil
}

func flowLoop(st ast.Stmt) bool {
	switch st.(type) {
	case *ast.While, *ast.Loop, *ast.For, *ast.ForEach:
		return true
	}
	return false
}

// flowEnds reports whether no run goes on after st. A break or a continue
// goes on elsewhere, so it does not end the run here.
func flowEnds(st ast.Stmt) bool {
	switch x := st.(type) {
	case *ast.Return:
		return true
	case *ast.Block:
		return x != nil && flowBlockEnds(x.Stmts)
	case *ast.If:
		return flowBlockEnds(flowStmts(x.Then)) && flowBlockEnds(flowStmts(x.Else))
	case *ast.Match:
		if len(x.Arms) == 0 {
			return false
		}
		for _, a := range x.Arms {
			if !flowBlockEnds(flowStmts(a.Body)) {
				return false
			}
		}
		return true
	}
	return false
}

func flowBlockEnds(stmts []ast.Stmt) bool {
	for _, st := range stmts {
		if flowEnds(st) {
			return true
		}
	}
	return false
}

func (f *flowTree) holds(n, at int) bool {
	return f.first[n] >= 0 && f.first[n] <= at && at <= f.last[n]
}

// nodeAt is the innermost statement that holds the identifier numbered at,
// or -1.
func (f *flowTree) nodeAt(at int) int {
	found := -1
	b := 0
	for len(f.blocks) > 0 {
		next := -1
		for _, n := range f.blocks[b] {
			if !f.holds(n, at) {
				continue
			}
			found = n
			for _, kb := range f.kids[n] {
				for _, m := range f.blocks[kb] {
					if next < 0 && f.holds(m, at) {
						next = kb
					}
				}
			}
			break
		}
		if next < 0 {
			return found
		}
		b = next
	}
	return found
}

// reaches reports whether a run can reach the identifier numbered at after
// statement n, which holds the earlier read. A run goes on through the
// statements after n until one ends it, and out of the block to the
// statements after its owner. The end of a loop body goes back to the loop,
// so it reaches every place after.
func (f *flowTree) reaches(n, at int) bool {
	if n < 0 || f.holds(n, at) {
		return true
	}
	if f.ends[n] {
		return false
	}
	cur := n
	for {
		b := f.block[cur]
		for _, m := range f.blocks[b][f.slot[cur]+1:] {
			if f.holds(m, at) {
				return true
			}
			if f.ends[m] {
				return false
			}
		}
		up := f.owner[b]
		if up < 0 {
			return false
		}
		if f.looping[up] {
			return true
		}
		cur = up
	}
}

func DeferOrLambdaNames(body ast.Node) map[string]bool {
	return escapingNames(body, true)
}

func lambdaFreeNames(lm *ast.Lambda) map[string]bool {
	names := map[string]bool{}
	w := &ast.ScopeWalker{}
	w.Visit = func(slot *ast.Expr, w *ast.ScopeWalker) bool {
		if id, ok := (*slot).(*ast.Ident); ok && !w.IsBound(id.Name) {
			names[id.Name] = true
		}
		return true
	}
	w.WalkFunc(&ast.FuncDecl{Params: lm.Params, Body: lm.Body})
	return names
}

func escapingNames(body ast.Node, withDefer bool) map[string]bool {
	esc := map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		var sub ast.Node
		switch d := n.(type) {
		case *ast.Defer:
			if !withDefer {
				return true
			}
			sub = d.Expr
		case *ast.Lambda:
			for name := range lambdaFreeNames(d) {
				esc[name] = true
			}
			return false
		default:
			return true
		}
		ast.Walk(sub, func(m ast.Node) bool {
			if id, isIdent := m.(*ast.Ident); isIdent {
				esc[id.Name] = true
			}
			return true
		})
		return true
	})
	if len(esc) == 0 {
		return esc
	}
	for name := range endedCallbackLoans(body) {
		delete(esc, name)
	}
	if block, ok := body.(*ast.Block); ok && len(esc) != 0 {
		for name := range directCallbackLoans(block, esc) {
			delete(esc, name)
		}
	}
	return esc
}

// loopRepeats reports a loop that runs c again on the same owner: one that
// holds c but not the node that binds the owner. A nil decl is a parameter,
// which no loop binds afresh.
func loopRepeats(body ast.Node, c ast.Node, decl ast.Node) bool {
	found := false
	ast.Walk(body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch n.(type) {
		case *ast.While, *ast.Loop, *ast.For, *ast.ForEach:
			if nodeHolds(n, c) && (decl == nil || !nodeHolds(n, decl)) {
				found = true
			}
		}
		return !found
	})
	return found
}

// freshInit reports an initialiser whose value no other name holds. A plain
// named call qualifies unless its result can borrow from an argument.
func freshInit(info *Info, init ast.Expr) bool {
	if info != nil {
		if value, ok := info.EnumConstructions[init]; ok && len(value.Payloads) == 0 {
			return true
		}
	}
	switch init := init.(type) {
	case *ast.TryOp:
		return freshInit(info, init.Inner)
	case *ast.Call:
		if freshAssociatedResult(init) {
			return true
		}
		// An `own` result borrows no argument, also through a function value.
		if info != nil && info.CallOwnedResult != nil && info.CallOwnedResult(init) {
			return true
		}
		if info != nil && info.CallValueLoan != nil && info.CallValueLoan(init) {
			return false
		}
		if info != nil {
			// A variant construction from another module has a qualified
			// callee. It is fresh on the same terms as an unqualified one.
			if _, isVariant := info.EnumConstructions[init]; isVariant {
				return info.CallBorrows == nil || !info.CallBorrows(init)
			}
		}
		if _, named := init.Callee.(*ast.Ident); !named {
			return false
		}
		if init.Method != nil && !cellGet(init) && (info == nil || info.DeclaredMethod == nil || !info.DeclaredMethod(init)) {
			return true
		}
		return info == nil || info.CallBorrows == nil || !info.CallBorrows(init)
	case *ast.ArrayLit, *ast.StringLit:
		return true
	case *ast.Lambda:
		// A `once` lambda moves its captures into a new environment.
		return init.Once
	case *ast.StructLit:
		return (init.Base == nil || freshInit(info, init.Base)) && litFieldsOwned(info, init, func(v ast.Expr) bool { return freshInit(info, v) })
	case *ast.MatchExpr:
		return freshMatchValue(info, init)
	case *ast.IfExpr:
		return freshScopedValue(info, nil, init.Then) && freshScopedValue(info, nil, init.Else)
	case *ast.BlockExpr:
		return init.Tail != nil && freshScopedValue(info, init, init.Tail)
	case *ast.TupleLit:
		return freshScopedValue(info, nil, init)
	case *ast.FieldAccess:
		return tupleProjection(init) && freshInit(info, init.Target)
	}
	return false
}

// cellGet reports a call of the builtin Cell.get. Its result is a snapshot of
// the payload, so it lends what the cell holds.
func cellGet(call *ast.Call) bool {
	id, ok := call.Callee.(*ast.Ident)
	return ok && call.Method != nil && id.Name == "__method_Cell_get"
}

// litFieldsOwned reports a struct literal whose field values are each owned,
// as owned decides, or hold no handle. The caller decides the base.
func litFieldsOwned(info *Info, sl *ast.StructLit, owned func(ast.Expr) bool) bool {
	if info == nil || info.StructLitOwned == nil {
		return true
	}
	return info.StructLitOwned(sl, func(v ast.Expr) bool { return v == sl.Base || owned(v) })
}

// tupleProjection reports an element read `t.0`. A struct field cannot have
// a numeric name.
func tupleProjection(fa *ast.FieldAccess) bool {
	if fa.Field == "" {
		return false
	}
	for _, r := range fa.Field {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// A compound block result can take unmodified local payloads from fresh values.
func freshScopedValue(info *Info, block *ast.BlockExpr, value ast.Expr) bool {
	switch x := value.(type) {
	case *ast.TupleLit:
		for _, element := range x.Elems {
			if !freshScopedValue(info, block, element) {
				return false
			}
		}
		return true
	case *ast.NumberLit, *ast.BoolLit:
		return true
	case *ast.Call:
		if info != nil {
			if constructor, ok := info.EnumConstructions[x]; ok && len(constructor.Payloads) == len(x.Args) {
				for i, argument := range x.Args {
					if (info.TypeHoldsNoHandle == nil || !info.TypeHoldsNoHandle(constructor.Payloads[i])) && !freshScopedValue(info, block, argument) {
						return false
					}
				}
				return true
			}
		}
		return freshInit(info, value) && (info == nil || info.CallScopedFresh == nil || info.CallScopedFresh(x))
	case *ast.Ident:
		if block == nil {
			return freshInit(info, value)
		}
		body := &ast.Block{Stmts: append(slices.Clone(block.Stmts), &ast.ExprStmt{Expr: block.Tail})}
		reads := 0
		ast.Walk(body, func(node ast.Node) bool {
			if id, ok := node.(*ast.Ident); ok && id.Name == x.Name {
				reads++
			}
			if assign, ok := node.(*ast.Assign); ok && StmtReferencesName(assign.Target, x.Name) {
				reads += 2
			}
			return true
		})
		if reads != 1 || DeferOrLambdaNames(body)[x.Name] {
			return false
		}
		for i := len(block.Stmts) - 1; i >= 0; i-- {
			if !bindsName(block.Stmts[i], x.Name) {
				continue
			}
			switch binding := block.Stmts[i].(type) {
			case *ast.Var:
				return freshScopedValue(info, nil, binding.Init)
			case *ast.Destructure:
				return binding.AtName == "" && freshScopedValue(info, nil, binding.Init)
			}
			return false
		}
		return freshInit(info, value)
	case *ast.MatchExpr:
		return ownedMatchValue(info, x, func(e ast.Expr) bool { return freshScopedValue(info, block, e) }, func(ast.Expr) bool { return false })
	}
	return freshInit(info, value)
}

// freshMatchValue reports a match on a fresh value whose each variant arm
// yields a payload it binds, a fresh value, or leaves.
func freshMatchValue(info *Info, m *ast.MatchExpr) bool {
	if _, named := m.Tag.(*ast.Ident); named || m.StructMatch != "" || m.IsFloat || len(m.Arms) == 0 || !freshInit(info, m.Tag) {
		return false
	}
	for _, arm := range m.Arms {
		if arm.VariantName == "" && !arm.IsWildcard || len(arm.TupleElems) > 0 || !armYieldsFresh(info, arm) {
			return false
		}
	}
	return true
}

func armYieldsFresh(info *Info, arm *ast.MatchExprArm) bool {
	value := arm.Body
	var block *ast.BlockExpr
	if b, ok := value.(*ast.BlockExpr); ok {
		if len(b.Stmts) > 0 && divergesStmt(info, b.Stmts[len(b.Stmts)-1]) {
			return true
		}
		if b.Tail == nil {
			return false
		}
		value = b.Tail
		block = b
	}
	if id, ok := value.(*ast.Ident); ok && (slices.Contains(arm.Binders(), id.Name) || block != nil && slices.ContainsFunc(block.Stmts, func(st ast.Stmt) bool { return bindsName(st, id.Name) })) {
		return freshArmPayload(info, block, id)
	}
	return freshScopedValue(info, block, value)
}

// freshArmPayload checks the binding that the arm returns: its payload, or a
// local of its block. A nested shadow does not change that binding.
func freshArmPayload(info *Info, block *ast.BlockExpr, id *ast.Ident) bool {
	if block == nil {
		return true
	}
	body := &ast.Block{Stmts: append(slices.Clone(block.Stmts), &ast.ExprStmt{Expr: id})}
	for i := len(block.Stmts) - 1; i >= 0; i-- {
		if bindsName(block.Stmts[i], id.Name) {
			if _, ok := block.Stmts[i].(*ast.Var); !ok {
				return false
			}
			break
		}
	}
	bf := newBodyFrames(body)
	bound := bindingVar(bf, id, id.Name)
	if bound != nil && !freshInit(info, bound.Init) {
		return false
	}
	fresh := true
	ast.Walk(body, func(n ast.Node) bool {
		if a, ok := n.(*ast.Assign); ok {
			if target, ok := a.Target.(*ast.Ident); ok && target.Name == id.Name && bindingVar(bf, a, id.Name) == bound && !freshInit(info, a.Value) && !freshLocalReplacement(info, bf, a, a.Value, IdentOrderOf(body)) {
				fresh = false
			}
		}
		return fresh
	})
	return fresh
}

// A replacement can take an unmodified fresh local at its last read.
func freshLocalReplacement(info *Info, bf *bodyFrames, holder ast.Node, value ast.Expr, order IdentOrder) bool {
	id, ok := value.(*ast.Ident)
	if !ok || bf.deferOrLambda()[id.Name] {
		return false
	}
	source := bindingVar(bf, holder, id.Name)
	if source == nil || !freshInit(info, source.Init) || loopRepeats(bf.body, holder, source) || !lastInBinding(bf, holder, id, order) {
		return false
	}
	for _, a := range bf.assignsTo(id.Name) {
		if bindingVar(bf, a, id.Name) == source {
			return false
		}
	}
	return true
}

// A match that consumes an owned local transfers an unmodified payload into
// its result. A sibling field of the source does not retain that payload.
func ownedMatchValue(info *Info, m *ast.MatchExpr, ownedAtLastUse, freshAtLastUse func(ast.Expr) bool) bool {
	if m.StructMatch != "" || m.IsFloat || len(m.Arms) == 0 || !ownedAtLastUse(m.Tag) && !freshAtLastUse(m.Tag) {
		return false
	}
	for _, arm := range m.Arms {
		if arm.VariantName == "" && !arm.IsWildcard || len(arm.TupleElems) > 0 || !armYieldsFresh(info, arm) {
			return false
		}
	}
	return true
}

// liveFrame is one block on the path from a function body to a statement:
// the block's statements, the index of the statement on the path, and the
// loop whose body the block is, if any.
type liveFrame struct {
	stmts []ast.Stmt
	idx   int
	loop  ast.Stmt
	label string
}

func nodeHolds(n ast.Node, target ast.Node) bool {
	found := false
	ast.Walk(n, func(m ast.Node) bool {
		if found {
			return false
		}
		if m == target {
			found = true
		}
		return !found
	})
	return found
}

// bodyFrames answers questions about one body, which the analysis does not
// change, and keeps each answer.
type bodyFrames struct {
	body    *ast.Block
	frames  map[ast.Node][]liveFrame
	escapes map[string]bool
	assigns map[string][]*ast.Assign
}

func newBodyFrames(body *ast.Block) *bodyFrames {
	return &bodyFrames{body: body}
}

// of is the chain of blocks from the body down to the innermost statement
// that holds target. A statement inside an expression-level block or a
// deferred action counts as its enclosing statement.
func (b *bodyFrames) of(target ast.Node) []liveFrame {
	if b.frames == nil {
		b.frames = map[ast.Node][]liveFrame{}
		if b.body != nil {
			b.index(b.body.Stmts, nil, "", nil)
		}
	}
	f := b.frames[target]
	return f[:len(f):len(f)]
}

// index gives each node under stmts its chain. A node that two places hold
// keeps the chain of the first statement, of a then branch before its else
// branch, of the first match arm, and of the innermost statement.
func (b *bodyFrames) index(stmts []ast.Stmt, loop ast.Stmt, label string, outer []liveFrame) {
	for i, st := range stmts {
		chain := append(outer[:len(outer):len(outer)], liveFrame{stmts: stmts, idx: i, loop: loop, label: label})
		switch x := st.(type) {
		case *ast.Block:
			b.index(x.Stmts, nil, "", chain)
		case *ast.If:
			b.indexBody(x.Then, nil, "", chain)
			b.indexBody(x.Else, nil, "", chain)
		case *ast.Match:
			for _, arm := range x.Arms {
				if arm.Body != nil {
					b.index(arm.Body.Stmts, nil, "", chain)
				}
			}
		case *ast.While:
			b.indexBody(x.Body, x, x.Label, chain)
		case *ast.Loop:
			b.indexBody(x.Body, x, x.Label, chain)
		case *ast.For:
			b.indexBody(x.Body, x, x.Label, chain)
		case *ast.ForEach:
			b.indexBody(x.Body, x, x.Label, chain)
		}
		ast.Walk(st, func(n ast.Node) bool {
			if _, ok := b.frames[n]; !ok {
				b.frames[n] = chain
			}
			return true
		})
	}
}

// indexBody indexes a branch or a loop body, which is a block or one
// statement.
func (b *bodyFrames) indexBody(body ast.Stmt, loop ast.Stmt, label string, outer []liveFrame) {
	if blk, ok := body.(*ast.Block); ok {
		b.index(blk.Stmts, loop, label, outer)
	} else if body != nil {
		b.index([]ast.Stmt{body}, loop, label, outer)
	}
}

// assignsTo is each assignment in the body whose target is the name.
func (b *bodyFrames) assignsTo(name string) []*ast.Assign {
	if b.assigns == nil {
		b.assigns = map[string][]*ast.Assign{}
		ast.Walk(b.body, func(n ast.Node) bool {
			if a, ok := n.(*ast.Assign); ok {
				if target, ok := a.Target.(*ast.Ident); ok {
					b.assigns[target.Name] = append(b.assigns[target.Name], a)
				}
			}
			return true
		})
	}
	return b.assigns[name]
}

// deferOrLambda is DeferOrLambdaNames of the body.
func (b *bodyFrames) deferOrLambda() map[string]bool {
	if b.escapes == nil {
		b.escapes = DeferOrLambdaNames(b.body)
	}
	return b.escapes
}

// callNeverReturns reports a call that never returns: one that resolved to
// the builtin exit, or a direct call of a name noReturn holds that no local
// takes.
func (info *Info) callNeverReturns(e ast.Expr, noReturn map[string]bool) bool {
	call, ok := e.(*ast.Call)
	if !ok || info == nil {
		return false
	}
	if info.IntrinsicCalls[call].Kind == IntrinsicExit {
		return true
	}
	id, ok := call.Callee.(*ast.Ident)
	return ok && !id.Builtin && !info.LocalCallees[call] && noReturn[id.Name]
}

// divergesStmt reports a statement that leaves on every path.
func divergesStmt(info *Info, st ast.Stmt) bool {
	switch x := st.(type) {
	case *ast.Return, *ast.Break, *ast.Continue:
		return true
	case *ast.ExprStmt:
		return info.callNeverReturns(x.Expr, nil)
	case *ast.Loop:
		return !loopCanBreak(x.Body, x.Label)
	case *ast.While:
		if lit, ok := x.Cond.(*ast.BoolLit); ok && lit.Value {
			return !loopCanBreak(x.Body, x.Label)
		}
		return false
	case *ast.Block:
		return len(x.Stmts) > 0 && divergesStmt(info, x.Stmts[len(x.Stmts)-1])
	case *ast.If:
		return x.Else != nil && divergesStmt(info, x.Then) && divergesStmt(info, x.Else)
	case *ast.Match:
		for i, arm := range x.Arms {
			if letElseDivergentArm(x, i) {
				continue
			}
			if arm.Body == nil || len(arm.Body.Stmts) == 0 || !divergesStmt(info, arm.Body.Stmts[len(arm.Body.Stmts)-1]) {
				return false
			}
		}
		return len(x.Arms) > 0
	}
	return false
}

// bindingLevel is the level of the innermost frame that binds name before the
// statement that the frame holds: the block that the read binding belongs to.
// A match arm binds its names for its body. It is -1 when no frame binds
// name, as for a parameter.
func bindingLevel(frames []liveFrame, name string) int {
	for l := len(frames) - 1; l >= 0; l-- {
		f := frames[l]
		switch loop := f.loop.(type) {
		case *ast.ForEach:
			if loop.Var == name {
				return l
			}
		case *ast.For:
			if loop.Init != nil && bindsName(loop.Init, name) {
				return l
			}
		}
		for j := 0; j < f.idx; j++ {
			if bindsName(f.stmts[j], name) {
				return l
			}
		}
		if l > 0 && armBinds(frames[l-1], f, name) {
			return l
		}
	}
	return -1
}

// armBinds reports whether the arm whose body is f binds name, when f is the
// body of an arm of the match that parent holds.
func armBinds(parent, f liveFrame, name string) bool {
	m, ok := parent.stmts[parent.idx].(*ast.Match)
	if !ok {
		return false
	}
	for _, arm := range m.Arms {
		if arm.Body != nil && nodeHolds(arm.Body, f.stmts[f.idx]) {
			return slices.Contains(arm.Binders(), name)
		}
	}
	return false
}

// lastInBinding reports whether no later read of name belongs to the binding
// that aid reads: no read after aid inside the block that declares it.
func lastInBinding(bf *bodyFrames, c ast.Node, aid *ast.Ident, order IdentOrder) bool {
	frames := bf.of(c)
	scope := bindingLevel(frames, aid.Name)
	if scope < 0 {
		return order.IsLast(aid)
	}
	f := frames[scope]
	at := order.Idx[aid]
	later := false
	for _, st := range f.stmts[f.idx:] {
		ast.Walk(st, func(m ast.Node) bool {
			if lm, ok := m.(*ast.Lambda); ok && !lambdaFreeNames(lm)[aid.Name] {
				return false
			}
			if id, ok := m.(*ast.Ident); ok && id.Name == aid.Name && order.Idx[id] > at {
				later = true
			}
			return !later
		})
	}
	return !later
}

// bindingVar is the let that a read of name at c belongs to, or nil when
// another kind of declaration or a parameter binds it.
func bindingVar(bf *bodyFrames, c ast.Node, name string) *ast.Var {
	frames := bf.of(c)
	l := bindingLevel(frames, name)
	if l < 0 {
		return nil
	}
	f := frames[l]
	for j := f.idx - 1; j >= 0; j-- {
		if bindsName(f.stmts[j], name) {
			v, _ := f.stmts[j].(*ast.Var)
			return v
		}
	}
	return nil
}

// bindsName reports whether the statement itself declares name, not a
// statement nested inside it.
func bindsName(st ast.Stmt, name string) bool {
	switch x := st.(type) {
	case *ast.Var:
		return x.Name == name
	case *ast.Destructure:
		return slices.Contains(x.Names, name)
	}
	return false
}
