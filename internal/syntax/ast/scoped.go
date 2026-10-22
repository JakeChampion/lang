package ast

// ScopeWalker walks a function body with binder tracking, so a pass that
// runs before the checker can tell a module-level name from a local that
// shadows it. Every binder the parser produces — parameters, `let` and
// destructure declarations, `for` and `match` bindings, lambda parameters —
// opens or extends a scope; IsBound answers for the point the walk is at.
//
// Visit is called on every expression slot before that expression's
// children, and may replace the slot. It returns whether to descend into
// the (possibly replaced) expression. AfterStmt, when set, runs after a
// statement's children have been walked.
type ScopeWalker struct {
	Visit     func(slot *Expr, w *ScopeWalker) bool
	AfterStmt func(st Stmt)
	bound     []map[string]bool
}

func (w *ScopeWalker) pushScope() { w.bound = append(w.bound, map[string]bool{}) }

func (w *ScopeWalker) popScope() { w.bound = w.bound[:len(w.bound)-1] }

// Bind records name as bound in the innermost scope.
func (w *ScopeWalker) Bind(name string) {
	if name == "" || len(w.bound) == 0 {
		return
	}
	w.bound[len(w.bound)-1][name] = true
}

// IsBound reports whether name is shadowed by a binder at this point.
func (w *ScopeWalker) IsBound(name string) bool {
	for i := len(w.bound) - 1; i >= 0; i-- {
		if w.bound[i][name] {
			return true
		}
	}
	return false
}

// WalkFunc walks fn's parameter defaults at top-level scope, then its body
// with the parameters bound. A default is pasted into the call site, so it
// sees none of the parameters.
func (w *ScopeWalker) WalkFunc(fn *FuncDecl) {
	for i := range fn.Params {
		if fn.Params[i].Default != nil {
			w.WalkExpr(&fn.Params[i].Default)
		}
	}
	w.pushScope()
	for _, p := range fn.Params {
		w.Bind(p.Name)
	}
	w.WalkBlock(fn.Body)
	w.popScope()
}

// WalkBlock walks b's statements in a scope of their own.
func (w *ScopeWalker) WalkBlock(b *Block) {
	if b == nil {
		return
	}
	w.pushScope()
	for _, st := range b.Stmts {
		w.WalkStmt(st)
	}
	w.popScope()
}

// WalkStmt walks one statement, binding what it declares.
func (w *ScopeWalker) WalkStmt(st Stmt) {
	switch x := st.(type) {
	case *Block:
		w.WalkBlock(x)
	case *If:
		w.WalkExpr(&x.Cond)
		w.WalkStmt(x.Then)
		if x.Else != nil {
			w.WalkStmt(x.Else)
		}
	case *While:
		w.WalkExpr(&x.Cond)
		w.WalkStmt(x.Body)
	case *Loop:
		w.WalkStmt(x.Body)
	case *For:
		// The init's binding is in scope for the cond, the step and the
		// body, so the frame wraps all four rather than the body alone.
		w.pushScope()
		if x.Init != nil {
			w.WalkStmt(x.Init)
		}
		w.WalkExpr(&x.Cond)
		if x.Step != nil {
			w.WalkStmt(x.Step)
		}
		w.WalkStmt(x.Body)
		w.popScope()
	case *ForEach:
		w.WalkExpr(&x.Iter)
		w.WalkExpr(&x.RangeHigh)
		w.pushScope()
		// A destructuring header carries its real binders on Pattern —
		// `for (a, b) in xs` — with Var only the synthetic element holder.
		w.Bind(x.Var)
		if x.Pattern != nil {
			w.WalkStmt(x.Pattern)
		}
		w.WalkStmt(x.Body)
		w.popScope()
	case *Return:
		if x.Value != nil {
			w.WalkExpr(&x.Value)
		}
	case *Var:
		// The init is walked BEFORE the name binds: `let N = N;` reads the
		// outer N on its right-hand side and shadows it only afterwards.
		w.WalkExpr(&x.Init)
		w.Bind(x.Name)
	case *Destructure:
		w.WalkExpr(&x.Init)
		w.Bind(x.AtName)
		w.Bind(x.TempName)
		for _, n := range x.Names {
			w.Bind(n)
		}
		// A nested level's binders live on Nested[i].Names, and its Init
		// reads Names[i], so each level walks after this one binds.
		for _, nd := range x.Nested {
			if nd != nil {
				w.WalkStmt(nd)
			}
		}
	case *ExprStmt:
		w.WalkExpr(&x.Expr)
	case *FuncDecl:
		w.pushScope()
		for _, p := range x.Params {
			w.Bind(p.Name)
		}
		w.WalkBlock(x.Body)
		w.popScope()
	case *Match:
		w.WalkExpr(&x.Tag)
		for _, arm := range x.Arms {
			// A literal or range pattern is a value position; the pattern's
			// binders shadow only the guard and the body.
			if arm.Literal != nil {
				w.WalkExpr(&arm.Literal)
			}
			if arm.RangeHi != nil {
				w.WalkExpr(&arm.RangeHi)
			}
			w.pushScope()
			for _, b := range arm.Binders() {
				w.Bind(b)
			}
			if arm.Guard != nil {
				w.WalkExpr(&arm.Guard)
			}
			w.WalkBlock(arm.Body)
			w.popScope()
		}
	case *Defer:
		w.WalkExpr(&x.Expr)
	}
	if w.AfterStmt != nil {
		w.AfterStmt(st)
	}
}

// WalkExpr visits the expression in slot, then its children unless Visit
// declined.
func (w *ScopeWalker) WalkExpr(slot *Expr) {
	if slot == nil || *slot == nil {
		return
	}
	if w.Visit != nil && !w.Visit(slot, w) {
		return
	}
	switch x := (*slot).(type) {
	case *Call:
		w.WalkExpr(&x.Callee)
		for i := range x.Args {
			w.WalkExpr(&x.Args[i])
		}
	case *Binary:
		w.WalkExpr(&x.Left)
		w.WalkExpr(&x.Right)
	case *Unary:
		w.WalkExpr(&x.Operand)
	case *Index:
		w.WalkExpr(&x.Array)
		w.WalkExpr(&x.Idx)
	case *ArrayLit:
		for i := range x.Elems {
			w.WalkExpr(&x.Elems[i])
		}
	case *Assign:
		w.WalkExpr(&x.Target)
		w.WalkExpr(&x.Value)
	case *IfExpr:
		w.WalkExpr(&x.Cond)
		w.WalkExpr(&x.Then)
		w.WalkExpr(&x.Else)
	case *TryOp:
		w.WalkExpr(&x.Inner)
	case *MatchExpr:
		w.WalkExpr(&x.Tag)
		for _, arm := range x.Arms {
			w.pushScope()
			for _, b := range arm.Binders() {
				w.Bind(b)
			}
			if arm.Guard != nil {
				w.WalkExpr(&arm.Guard)
			}
			w.WalkExpr(&arm.Body)
			w.popScope()
		}
	case *BlockExpr:
		w.pushScope()
		for _, st := range x.Stmts {
			w.WalkStmt(st)
		}
		if x.Tail != nil {
			w.WalkExpr(&x.Tail)
		}
		w.popScope()
	case *StructLit:
		if x.Base != nil {
			w.WalkExpr(&x.Base)
		}
		for i := range x.Fields {
			w.WalkExpr(&x.Fields[i].Value)
		}
	case *FieldAccess:
		w.WalkExpr(&x.Target)
	case *CastExpr:
		w.WalkExpr(&x.Inner)
	case *DowncastExpr:
		w.WalkExpr(&x.Inner)
	case *SliceExpr:
		w.WalkExpr(&x.Source)
		if x.Low != nil {
			w.WalkExpr(&x.Low)
		}
		if x.High != nil {
			w.WalkExpr(&x.High)
		}
	case *TupleLit:
		for i := range x.Elems {
			w.WalkExpr(&x.Elems[i])
		}
	case *MapLit:
		for i := range x.Entries {
			w.WalkExpr(&x.Entries[i].Key)
			w.WalkExpr(&x.Entries[i].Value)
		}
	case *EnumLit:
		for i := range x.Args {
			w.WalkExpr(&x.Args[i])
		}
	case *FString:
		for i := range x.Parts {
			if x.Parts[i].Expr != nil {
				w.WalkExpr(&x.Parts[i].Expr)
			}
		}
		if x.Desugared != nil {
			w.WalkExpr(&x.Desugared)
		}
	case *Lambda:
		w.pushScope()
		for _, p := range x.Params {
			w.Bind(p.Name)
		}
		w.WalkBlock(x.Body)
		w.popScope()
	}
}
