package interp

import "github.com/jakechampion/lang/internal/syntax/ast"

// analyse finds the reads of fn that move their local or a field of it, once
// for each fn.
func (i *Interp) analyse(fn *ast.FuncDecl) {
	if i.analysed[fn] {
		return
	}
	if i.analysed == nil {
		i.analysed = map[*ast.FuncDecl]bool{}
		i.lastUse = map[*ast.Ident]bool{}
		i.fieldLastUse = map[*ast.FieldAccess]bool{}
	}
	i.analysed[fn] = true
	w := lastUses(fn)
	for id := range w.idents {
		i.lastUse[id] = true
	}
	for fa := range w.fields {
		i.fieldLastUse[fa] = true
	}
}

// moveOut ends the holds of the local name on the array v, which a read at
// the last use of the local gives away. The local is empty after the read.
func (i *Interp) moveOut(name string, v Value, e *env) {
	arr, ok := v.(Array)
	if !ok {
		return
	}
	if _, captured := e.slot(name).(*binding); captured {
		return
	}
	release(arr)
	arrDisown(arr)
	e.set(name, Void{})
}

// returnChainMove moves the root of an update chain that a return expression
// reads more than once, before the expression runs, as assignIdent does for an
// assignment. The caller pops it from i.moving and owns it again, because the
// scope of the function still releases it. The last-use analysis moves a root
// that the expression reads once.
func (i *Interp) returnChainMove(rhs ast.Expr, e *env) (bool, Value) {
	if n := len(i.deferStack); n > 0 && len(i.deferStack[n-1]) > 0 {
		return false, nil
	}
	name, ok := i.returnRoots[rhs]
	if !ok {
		if i.returnRoots == nil {
			i.returnRoots = map[ast.Expr]string{}
		}
		name = chainMoved(rhs)
		i.returnRoots[rhs] = name
	}
	if name == "" {
		return false, nil
	}
	v, isArray := e.slot(name).(Array)
	if !isArray {
		return false, nil
	}
	arrDisown(v)
	i.moving = append(i.moving, v)
	return true, v
}

// chainMoved gives the root of rhs when rhs is an update chain that reads
// its root more than once, or "".
func chainMoved(rhs ast.Expr) string {
	e := rhs
	for {
		if fa, ok := e.(*ast.FieldAccess); ok {
			e = fa.Target
		}
		c, ok := e.(*ast.Call)
		if !ok || !isUpdateCall(c) {
			return ""
		}
		if id, ok := c.Args[0].(*ast.Ident); ok {
			if reads(rhs, usePath{name: id.Name}) > 1 && updateChain(rhs, id.Name) {
				return id.Name
			}
			return ""
		}
		e = c.Args[0]
	}
}

// takeField takes the field that x reads out of the struct s, which a read at
// the last use of the field gives away. Only the local of x holds s, so no
// run sees the struct without the field.
func (i *Interp) takeField(s *Struct, x *ast.FieldAccess, e *env) {
	id := x.Target.(*ast.Ident)
	if !s.holdsElems || s.owners != 1 || e.slot(id.Name) != Value(s) {
		return
	}
	v := s.Fields[x.Field]
	s.Fields[x.Field] = Void{}
	arrDisown(v)
}

// lastUses finds each read of a local, or of a field of a local, in fn after
// which no run reads it before it writes the local again or leaves the scope
// of the local. Such a read moves the value out, as a compiled read at a last
// use does, so the local does not stop a write in place.
func lastUses(fn *ast.FuncDecl) *lastUseWalk {
	w := &lastUseWalk{
		idents:   map[*ast.Ident]bool{},
		fields:   map[*ast.FieldAccess]bool{},
		captured: capturedNames(fn.Body),
	}
	if fn.Body == nil {
		return w
	}
	params := map[string]bool{}
	for _, p := range fn.Params {
		params[p.Name] = true
	}
	w.block(fn.Body.Stmts, nil, params)
	return w
}

// A useFrame is a block of the walk: stmts[at] is the statement that the walk
// is in, owner is the statement that holds the block (nil for the body of the
// function), and decls are the names that the block declares before at.
type useFrame struct {
	stmts []ast.Stmt
	at    int
	owner ast.Stmt
	decls map[string]bool
}

type lastUseWalk struct {
	frames   []*useFrame
	captured map[string]bool
	idents   map[*ast.Ident]bool
	fields   map[*ast.FieldAccess]bool
}

func (w *lastUseWalk) block(stmts []ast.Stmt, owner ast.Stmt, decls map[string]bool) {
	f := &useFrame{stmts: stmts, owner: owner, decls: decls}
	w.frames = append(w.frames, f)
	for k, st := range stmts {
		f.at = k
		w.stmt(st)
		for _, name := range declaredNames(st) {
			f.decls[name] = true
		}
	}
	w.frames = w.frames[:len(w.frames)-1]
}

func (w *lastUseWalk) stmt(st ast.Stmt) {
	switch x := st.(type) {
	case *ast.ExprStmt, *ast.Var, *ast.Destructure, *ast.Return:
		w.simple(st)
	case *ast.Block:
		if x != nil {
			w.block(x.Stmts, x, map[string]bool{})
		}
	case *ast.If:
		w.body(x.Then, x)
		w.body(x.Else, x)
	case *ast.While:
		w.body(x.Body, x)
	case *ast.Loop:
		w.body(x.Body, x)
	case *ast.For:
		w.body(x.Body, x)
	case *ast.Match:
		for _, arm := range x.Arms {
			if arm.Body == nil {
				continue
			}
			decls := map[string]bool{}
			for _, b := range arm.Binders() {
				decls[b] = true
			}
			w.block(arm.Body.Stmts, x, decls)
		}
	}
}

func (w *lastUseWalk) body(st ast.Stmt, owner ast.Stmt) {
	switch b := st.(type) {
	case nil:
	case *ast.Block:
		if b != nil {
			w.block(b.Stmts, owner, map[string]bool{})
		}
	default:
		w.block([]ast.Stmt{st}, owner, map[string]bool{})
	}
}

// A usePath is a local, or a field of a local when field is not empty.
type usePath struct{ name, field string }

// simple marks the reads of st that are a last use. A path qualifies when one
// run of st reads it once and no later run reads it before a write. A return
// ends every scope. An assignment writes its target, so one read of the old
// value is its last, except the root of an update chain, which assignIdent
// moves before the chain runs.
func (w *lastUseWalk) simple(st ast.Stmt) {
	var target *ast.Ident
	var value ast.Expr
	compound := false
	if es, ok := st.(*ast.ExprStmt); ok {
		if as, ok := es.Expr.(*ast.Assign); ok {
			if t, ok := as.Target.(*ast.Ident); ok {
				target, value, compound = t, as.Value, as.Op != ""
			}
		}
	}
	_, returns := st.(*ast.Return)
	idents := map[usePath][]*ast.Ident{}
	fields := map[usePath][]*ast.FieldAccess{}
	ast.Walk(st, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Lambda, *ast.FuncDecl:
			return false
		case *ast.Ident:
			if x != target {
				p := usePath{name: x.Name}
				idents[p] = append(idents[p], x)
			}
		case *ast.FieldAccess:
			if id, ok := x.Target.(*ast.Ident); ok {
				p := usePath{id.Name, x.Field}
				fields[p] = append(fields[p], x)
			}
		}
		return true
	})
	declares := map[string]bool{}
	for _, name := range declaredNames(st) {
		declares[name] = true
	}
	qualifies := func(p usePath) bool {
		if declares[p.name] || w.captured[p.name] || w.declLevel(p.name) < 0 {
			return false
		}
		if target != nil && p.name == target.Name {
			return !compound && p.field == "" && reads(value, p) == 1 && !updateChain(value, p.name)
		}
		return reads(st, p) == 1 && (returns || w.deadAfter(p))
	}
	for p, list := range idents {
		if qualifies(p) {
			for _, id := range list {
				w.idents[id] = true
			}
		}
	}
	for p, list := range fields {
		if qualifies(p) {
			for _, fa := range list {
				w.fields[fa] = true
			}
		}
	}
}

// declLevel is the index of the innermost frame that declares name, or -1.
func (w *lastUseWalk) declLevel(name string) int {
	for lvl := len(w.frames) - 1; lvl >= 0; lvl-- {
		if w.frames[lvl].decls[name] {
			return lvl
		}
	}
	return -1
}

// loopLevel is the index of the innermost frame up to lvl that is the body of
// a loop, or -1.
func (w *lastUseWalk) loopLevel(lvl int) int {
	for ; lvl >= 0; lvl-- {
		switch w.frames[lvl].owner.(type) {
		case *ast.While, *ast.For, *ast.Loop:
			return lvl
		}
	}
	return -1
}

// deadAfter reports whether no run reads p after the statement that the walk
// is in, before it writes the local of p or leaves the scope of the local.
func (w *lastUseWalk) deadAfter(p usePath) bool {
	decl := w.declLevel(p.name)
	for lvl := len(w.frames) - 1; lvl >= 0; lvl-- {
		f := w.frames[lvl]
		// A break or a continue leaves the scope of the local when the
		// body of the loop that it leaves declares the local.
		jumpEnds := decl >= w.loopLevel(lvl)
		for _, t := range f.stmts[f.at+1:] {
			if mentions(t, p.name) {
				if writes(t, p.name) {
					return true
				}
				if uses(t, p) {
					return false
				}
			}
			switch t.(type) {
			case *ast.Return:
				return true
			case *ast.Break, *ast.Continue:
				return jumpEnds
			}
			if jumps(t) && !jumpEnds {
				return false
			}
		}
		if f.decls[p.name] {
			return true
		}
		switch f.owner.(type) {
		case *ast.While, *ast.For, *ast.Loop:
			if uses(f.owner, p) {
				return false
			}
		}
	}
	return true
}

// reads is the most reads of p that one run of n makes, or 2 for a read in a
// loop or a function, which can run more than once. A read of the whole local
// is 2 reads of a field.
func reads(n ast.Node, p usePath) int {
	switch x := n.(type) {
	case nil:
		return 0
	case *ast.Ident:
		switch {
		case x.Name != p.name:
			return 0
		case p.field == "":
			return 1
		}
		return 2
	case *ast.FieldAccess:
		if id, ok := x.Target.(*ast.Ident); ok && id.Name == p.name && p.field != "" {
			if x.Field == p.field {
				return 1
			}
			return 0
		}
	case *ast.IfExpr:
		return reads(x.Cond, p) + max(readsOf(x.Then, p), readsOf(x.Else, p))
	case *ast.If:
		return reads(x.Cond, p) + max(readsOf(x.Then, p), readsOf(x.Else, p))
	case *ast.MatchExpr:
		most := 0
		for _, arm := range x.Arms {
			most = max(most, readsOf(arm.Guard, p)+readsOf(arm.Body, p))
		}
		return reads(x.Tag, p) + most
	case *ast.Match:
		most := 0
		for _, arm := range x.Arms {
			most = max(most, readsOf(arm.Guard, p)+readsOf(arm.Body, p))
		}
		return reads(x.Tag, p) + most
	case *ast.While, *ast.For, *ast.Loop, *ast.Lambda, *ast.FuncDecl:
		if mentions(x, p.name) {
			return 2
		}
		return 0
	}
	total := 0
	ast.Walk(n, func(m ast.Node) bool {
		if m == n {
			return true
		}
		total += reads(m, p)
		return false
	})
	return total
}

// readsOf is reads for a child that can be a nil pointer.
func readsOf(n ast.Node, p usePath) int {
	if b, ok := n.(*ast.Block); ok && b == nil {
		return 0
	}
	if !mentions(n, p.name) {
		return 0
	}
	return reads(n, p)
}

// writes reports whether t writes name before it reads it: t is an assignment
// of name whose value does not read name.
func writes(t ast.Stmt, name string) bool {
	es, ok := t.(*ast.ExprStmt)
	if !ok {
		return false
	}
	as, ok := es.Expr.(*ast.Assign)
	if !ok || as.Op != "" {
		return false
	}
	target, ok := as.Target.(*ast.Ident)
	return ok && target.Name == name && !mentions(as.Value, name)
}

// jumps reports whether t holds a break or a continue that leaves the loop
// around t.
func jumps(t ast.Stmt) bool {
	found := false
	var visit func(n ast.Node, inLoop bool) bool
	visit = func(n ast.Node, inLoop bool) bool {
		switch x := n.(type) {
		case *ast.Lambda, *ast.FuncDecl:
			return false
		case *ast.Break:
			found = found || !inLoop || x.Label != ""
		case *ast.Continue:
			found = found || !inLoop || x.Label != ""
		case *ast.While, *ast.For, *ast.Loop:
			if !inLoop {
				ast.Walk(n, func(m ast.Node) bool {
					if m == n {
						return true
					}
					return visit(m, true)
				})
				return false
			}
		}
		return !found
	}
	ast.Walk(t, func(n ast.Node) bool { return visit(n, false) })
	return found
}

func mentions(n ast.Node, name string) bool {
	return uses(n, usePath{name: name})
}

// uses reports whether n reads p: the local, or for a field, the local whole
// or that field of it.
func uses(n ast.Node, p usePath) bool {
	if n == nil {
		return false
	}
	found := false
	ast.Walk(n, func(m ast.Node) bool {
		switch x := m.(type) {
		case *ast.Ident:
			found = found || x.Name == p.name
		case *ast.FieldAccess:
			if id, ok := x.Target.(*ast.Ident); ok && id.Name == p.name && p.field != "" {
				found = found || x.Field == p.field
				return false
			}
		}
		return !found
	})
	return found
}

// capturedNames gives each name that a lambda, a nested function or a defer
// of body reads. A run of those can read the name after its last use in the
// body.
func capturedNames(body *ast.Block) map[string]bool {
	names := map[string]bool{}
	if body == nil {
		return names
	}
	ast.Walk(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.Lambda, *ast.FuncDecl, *ast.Defer:
			ast.Walk(n, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					names[id.Name] = true
				}
				return true
			})
			return false
		}
		return true
	})
	return names
}

// declaredNames gives the names that the statement st declares.
func declaredNames(st ast.Stmt) []string {
	switch x := st.(type) {
	case *ast.Var:
		return []string{x.Name}
	case *ast.Destructure:
		return destructureNames(x)
	case *ast.FuncDecl:
		return []string{x.Name}
	}
	return nil
}

func destructureNames(d *ast.Destructure) []string {
	names := append([]string{}, d.Names...)
	if d.AtName != "" {
		names = append(names, d.AtName)
	}
	for _, nested := range d.Nested {
		if nested != nil {
			names = append(names, destructureNames(nested)...)
		}
	}
	return names
}
