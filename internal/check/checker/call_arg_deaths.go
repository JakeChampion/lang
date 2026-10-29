package checker

import (
	"github.com/jakechampion/lang/internal/syntax/ast"
)

func StmtReferencesName(st ast.Node, name string) bool {
	found := false
	ast.Walk(st, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

type IdentOrder struct {
	Idx  map[*ast.Ident]int
	last map[string]int
}

func IdentOrderOf(body ast.Node) IdentOrder {
	o := IdentOrder{Idx: map[*ast.Ident]int{}, last: map[string]int{}}
	n := 0
	ast.Walk(body, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok {
			n++
			o.Idx[id] = n
			if n > o.last[id.Name] {
				o.last[id.Name] = n
			}
		}
		return true
	})
	return o
}

func (o IdentOrder) IsLast(id *ast.Ident) bool {
	return o.Idx[id] == o.last[id.Name]
}

func DeferOrLambdaNames(body ast.Node) map[string]bool {
	return escapingNames(body, true)
}

// LambdaNames is the subset of DeferOrLambdaNames that a lambda captures.
func LambdaNames(body ast.Node) map[string]bool {
	return escapingNames(body, false)
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
			sub = n
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
	return esc
}

func RenameRoots(body ast.Node) map[string]string {
	occurrences := map[string]int{}
	declCount := map[string]int{}
	ast.Walk(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			occurrences[x.Name]++
		case *ast.Var:
			declCount[x.Name]++
		}
		return true
	})
	direct := map[string]string{}
	ast.Walk(body, func(n ast.Node) bool {
		v, isVar := n.(*ast.Var)
		if !isVar || declCount[v.Name] != 1 {
			return true
		}
		if src, isID := v.Init.(*ast.Ident); isID && occurrences[src.Name] == 1 {
			direct[v.Name] = src.Name
		}
		return true
	})
	out := make(map[string]string, len(direct))
	for name := range direct {
		root := direct[name]
		seen := map[string]bool{name: true, root: true}
		for {
			next, chained := direct[root]
			if !chained || seen[next] {
				break
			}
			root, seen[next] = next, true
		}
		out[name] = root
	}
	return out
}

type ArgDeaths struct {
	Dies      map[*ast.Call]map[string]bool
	Repeating map[*ast.Call]bool
	Escaping  map[string]bool
	// Deferred holds the call of each defer statement whose argument dies
	// there: the deferred call runs at the function's exit, so it is the last
	// use of the names it consumes.
	Deferred map[*ast.Call]bool
	// FieldDies names the field reads that are the last use of their field
	// path, with no later use of the whole: `q.r` passed to an own parameter
	// moves the field out of the local or own parameter `q`.
	FieldDies   map[*ast.Call]map[*ast.FieldAccess]bool
	Occurrences map[string]int
}

func CallArgDeaths(fn *ast.FuncDecl, info *Info) ArgDeaths {
	body := fn.Body
	out := map[*ast.Call]map[string]bool{}
	occurrences := map[string]int{}
	ast.Walk(body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			occurrences[id.Name]++
		}
		return true
	})
	isParam := map[string]bool{}
	ownParam := map[string]bool{}
	frameOwns := map[string]bool{}
	for _, p := range fn.Params {
		isParam[p.Name] = true
		if p.Own {
			ownParam[p.Name] = true
			frameOwns[p.Name] = true
		}
	}
	ast.Walk(body, func(n ast.Node) bool {
		if v, isVar := n.(*ast.Var); isVar && !isParam[v.Name] {
			frameOwns[v.Name] = true
		}
		return true
	})
	freshInitLocal := map[string]bool{}
	declCount := map[string]int{}
	ast.Walk(body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Var:
			declCount[v.Name]++
			if freshInit(v.Init) {
				freshInitLocal[v.Name] = true
			}
		case *ast.Destructure:
			fresh := v.AtName == "" && freshInit(v.Init)
			for i, name := range v.Names {
				declCount[name]++
				if fresh && (i >= len(v.Nested) || v.Nested[i] == nil) {
					freshInitLocal[name] = true
				}
			}
		}
		return true
	})
	for name, n := range declCount {
		if n > 1 {
			delete(freshInitLocal, name)
		}
	}
	var fieldSelections map[string]int
	var fieldReads map[[2]string]int
	unpackInitLocal := map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		v, isVar := n.(*ast.Var)
		if !isVar || declCount[v.Name] != 1 {
			return true
		}
		fa, isField := v.Init.(*ast.FieldAccess)
		if !isField {
			return true
		}
		hid, isID := fa.Target.(*ast.Ident)
		// An own parameter holds its fields as a fresh local does.
		if !isID || !freshInitLocal[hid.Name] && !(ownParam[hid.Name] && declCount[hid.Name] == 0) {
			return true
		}
		if fieldSelections == nil {
			fieldSelections = map[string]int{}
			fieldReads = map[[2]string]int{}
			ast.Walk(body, func(m ast.Node) bool {
				if field, ok := m.(*ast.FieldAccess); ok {
					if root, ok := field.Target.(*ast.Ident); ok {
						fieldSelections[root.Name]++
						fieldReads[[2]string{root.Name, field.Field}]++
					}
				}
				return true
			})
		}
		if fieldReads[[2]string{hid.Name, fa.Field}] == 1 && fieldSelections[hid.Name] == occurrences[hid.Name] {
			unpackInitLocal[v.Name] = true
		}
		return true
	})
	aliasInitLocal := map[string]bool{}
	for name, root := range RenameRoots(body) {
		if isParam[root] || freshInitLocal[root] || unpackInitLocal[root] {
			aliasInitLocal[name] = true
		}
	}
	admitted := func(name string) bool {
		return isParam[name] || freshInitLocal[name] || unpackInitLocal[name] || aliasInitLocal[name]
	}
	markOnce := func(scope ast.Expr, name string) {
		total := 0
		ast.Walk(scope, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok && id.Name == name {
				total++
			}
			return true
		})
		if total != 1 {
			return
		}
		var site *ast.Call
		ast.Walk(scope, func(m ast.Node) bool {
			c, isCall := m.(*ast.Call)
			if !isCall {
				return true
			}
			for _, a := range c.Args {
				if aid, ok := a.(*ast.Ident); ok && aid.Name == name {
					site = c
				}
			}
			return true
		})
		if site == nil {
			return
		}
		if out[site] == nil {
			out[site] = map[string]bool{}
		}
		out[site][name] = true
	}
	ast.Walk(body, func(n ast.Node) bool {
		switch st := n.(type) {
		case *ast.Assign:
			t, ok := st.Target.(*ast.Ident)
			if !ok {
				return true
			}
			if sl, isLit := st.Value.(*ast.StructLit); isLit {
				markSupersededFields(out, sl, t.Name)
				return true
			}
			c, ok := st.Value.(*ast.Call)
			if !ok {
				return true
			}
			markOnce(c, t.Name)
		case *ast.Return:
			if sl, isLit := st.Value.(*ast.StructLit); isLit {
				if sl.Base != nil {
					if bid, ok := sl.Base.(*ast.Ident); ok && frameOwns[bid.Name] {
						markSupersededFields(out, sl, bid.Name)
					}
				}
			}
			if st.Value == nil {
				return true
			}
			ast.Walk(st.Value, func(m ast.Node) bool {
				switch m.(type) {
				case *ast.BlockExpr, *ast.IfExpr, *ast.MatchExpr, *ast.Lambda:
					return false
				}
				if c, isCall := m.(*ast.Call); isCall {
					for _, a := range c.Args {
						if aid, ok := a.(*ast.Ident); ok {
							markOnce(st.Value, aid.Name)
						}
					}
				}
				return true
			})
		}
		return true
	})
	ast.Walk(body, func(n ast.Node) bool {
		blk, isBlk := n.(*ast.Block)
		if !isBlk {
			return true
		}
		for i := 0; i+1 < len(blk.Stmts); i++ {
			var init ast.Expr
			switch st := blk.Stmts[i].(type) {
			case *ast.Var:
				init = st.Init
			case *ast.Destructure:
				init = st.Init
			default:
				continue
			}
			c, isCall := init.(*ast.Call)
			if !isCall {
				continue
			}
			for j := i + 1; j < len(blk.Stmts); j++ {
				t, asn := storeTarget(blk.Stmts[j])
				if t == "" || !StmtReferencesName(c, t) || !reboundAfter(blk.Stmts[i+1:j], t) {
					continue
				}
				if !StmtReferencesName(asn.Value, t) {
					markOnce(c, t)
				}
			}
		}
		return true
	})
	repeating := map[*ast.Call]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.While, *ast.Loop, *ast.For, *ast.ForEach, *ast.Lambda:
		default:
			return true
		}
		ast.Walk(n, func(m ast.Node) bool {
			if c, ok := m.(*ast.Call); ok {
				repeating[c] = true
			}
			return true
		})
		return true
	})
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || repeating[c] {
			return true
		}
		for _, a := range c.Args {
			aid, ok := a.(*ast.Ident)
			if !ok || !isParam[aid.Name] || occurrences[aid.Name] != 1 {
				continue
			}
			if out[c] == nil {
				out[c] = map[string]bool{}
			}
			out[c][aid.Name] = true
		}
		return true
	})
	inLambda := map[*ast.Call]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.Lambda); !ok {
			return true
		}
		ast.Walk(n, func(m ast.Node) bool {
			if c, ok := m.(*ast.Call); ok {
				inLambda[c] = true
			}
			return true
		})
		return true
	})
	escaping := DeferOrLambdaNames(body)
	order := IdentOrderOf(body)
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || inLambda[c] {
			return true
		}
		for _, a := range c.Args {
			aid, ok := a.(*ast.Ident)
			if !ok || escaping[aid.Name] || !admitted(aid.Name) {
				continue
			}
			if heldByEnclosingCall(body, c, aid.Name) {
				continue
			}
			if !order.IsLast(aid) && arrayArgPosition(info, c, aid) {
				continue
			}
			if liveAfter(info, body, c, aid, order) {
				continue
			}
			markOnce(c, aid.Name)
		}
		return true
	})
	deferred := map[*ast.Call]bool{}
	lambdaCaptured := LambdaNames(body)
	ast.Walk(body, func(n ast.Node) bool {
		d, ok := n.(*ast.Defer)
		if !ok {
			return true
		}
		c, isCall := d.Expr.(*ast.Call)
		if !isCall || repeating[c] {
			return true
		}
		for _, a := range c.Args {
			aid, ok := a.(*ast.Ident)
			if !ok || lambdaCaptured[aid.Name] || !admitted(aid.Name) || declCount[aid.Name] > 1 {
				continue
			}
			if !isParam[aid.Name] && !freshInitLocal[aid.Name] && !unpackInitLocal[aid.Name] {
				continue
			}
			markOnce(c, aid.Name)
			if out[c][aid.Name] {
				deferred[c] = true
			}
		}
		return true
	})
	fieldDies := map[*ast.Call]map[*ast.FieldAccess]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		d, ok := n.(*ast.Defer)
		if !ok {
			return true
		}
		c, isCall := d.Expr.(*ast.Call)
		if !isCall || repeating[c] {
			return true
		}
		for _, a := range c.Args {
			fa, ok := a.(*ast.FieldAccess)
			if !ok {
				continue
			}
			q, ok := fa.Target.(*ast.Ident)
			if !ok || lambdaCaptured[q.Name] {
				continue
			}
			if !(freshInitLocal[q.Name] && declCount[q.Name] == 1) && !(ownParam[q.Name] && declCount[q.Name] == 0) {
				continue
			}
			mentions := 0
			ast.Walk(c, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok && id.Name == q.Name {
					mentions++
				}
				return true
			})
			if mentions != 1 {
				continue
			}
			if fieldDies[c] == nil {
				fieldDies[c] = map[*ast.FieldAccess]bool{}
			}
			fieldDies[c][fa] = true
			deferred[c] = true
		}
		return true
	})
	fieldOf := map[*ast.Ident]string{}
	ast.Walk(body, func(n ast.Node) bool {
		if fa, ok := n.(*ast.FieldAccess); ok {
			if id, ok := fa.Target.(*ast.Ident); ok {
				fieldOf[id] = fa.Field
			}
		}
		return true
	})
	// A deferred action runs at the exit, so a path it mentions is live until
	// then: a bare mention of q holds every field, and `q.g` holds g alone.
	deferBare := map[string]bool{}
	deferField := map[[2]string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		d, ok := n.(*ast.Defer)
		if !ok {
			return true
		}
		ast.Walk(d.Expr, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok {
				if f, isField := fieldOf[id]; isField {
					deferField[[2]string{id.Name, f}] = true
				} else {
					deferBare[id.Name] = true
				}
			}
			return true
		})
		return true
	})
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok || repeating[c] || deferred[c] {
			return true
		}
		for _, a := range c.Args {
			fa, ok := a.(*ast.FieldAccess)
			if !ok {
				continue
			}
			q, ok := fa.Target.(*ast.Ident)
			if !ok || lambdaCaptured[q.Name] || deferBare[q.Name] || deferField[[2]string{q.Name, fa.Field}] {
				continue
			}
			if !(freshInitLocal[q.Name] && declCount[q.Name] == 1) && !(ownParam[q.Name] && declCount[q.Name] == 0) {
				continue
			}
			mentions := 0
			ast.Walk(c, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok && id.Name == q.Name {
					mentions++
				}
				return true
			})
			if mentions != 1 || !fieldPathIsLast(order, fieldOf, q, fa.Field) || withinLiteralOf(body, c, q.Name) {
				continue
			}
			if fieldDies[c] == nil {
				fieldDies[c] = map[*ast.FieldAccess]bool{}
			}
			fieldDies[c][fa] = true
		}
		return true
	})
	return ArgDeaths{Dies: out, Repeating: repeating, Escaping: escaping, Occurrences: occurrences, Deferred: deferred, FieldDies: fieldDies}
}

// withinLiteralOf reports whether c sits inside a struct literal whose base
// is name: `S { ...q, f: g(q.f) }` is the superseded-field rule's shape, and
// that rule decides it.
func withinLiteralOf(body ast.Node, c *ast.Call, name string) bool {
	found := false
	ast.Walk(body, func(n ast.Node) bool {
		if found {
			return false
		}
		sl, ok := n.(*ast.StructLit)
		if !ok || sl.Base == nil {
			return true
		}
		if bid, ok := sl.Base.(*ast.Ident); !ok || bid.Name != name {
			return true
		}
		ast.Walk(sl, func(m ast.Node) bool {
			if mc, isCall := m.(*ast.Call); isCall && mc == c {
				found = true
			}
			return !found
		})
		return !found
	})
	return found
}

// fieldPathIsLast reports whether every later occurrence of q reads a field
// other than field: the read `q.field` at q is then the last use of that
// path, and q as a whole is never used again.
func fieldPathIsLast(order IdentOrder, fieldOf map[*ast.Ident]string, q *ast.Ident, field string) bool {
	at := order.Idx[q]
	for id, idx := range order.Idx {
		if idx <= at || id.Name != q.Name {
			continue
		}
		f, isField := fieldOf[id]
		if !isField || f == field {
			return false
		}
	}
	return true
}

func freshInit(init ast.Expr) bool {
	switch init := init.(type) {
	case *ast.TryOp:
		return freshInit(init.Inner)
	case *ast.Call:
		_, named := init.Callee.(*ast.Ident)
		return named
	case *ast.ArrayLit, *ast.StringLit:
		return true
	case *ast.StructLit:
		return init.Base == nil
	}
	return false
}

func heldByEnclosingCall(body ast.Node, c *ast.Call, name string) bool {
	found := false
	ast.Walk(body, func(n ast.Node) bool {
		if found {
			return false
		}
		p, isCall := n.(*ast.Call)
		if !isCall || p == c || !callContains(p, c) {
			return true
		}
		ast.Walk(p, func(m ast.Node) bool {
			if found {
				return false
			}
			if mc, isC := m.(*ast.Call); isC && mc == c {
				return false
			}
			if id, isID := m.(*ast.Ident); isID && id.Name == name {
				found = true
			}
			return true
		})
		return !found
	})
	return found
}

func callContains(p *ast.Call, target *ast.Call) bool {
	if p == target {
		return false
	}
	found := false
	ast.Walk(p, func(n ast.Node) bool {
		if found {
			return false
		}
		if c, ok := n.(*ast.Call); ok && c == target {
			found = true
			return false
		}
		return true
	})
	return found
}

func arrayArgPosition(info *Info, c *ast.Call, aid *ast.Ident) bool {
	if info == nil {
		return true
	}
	callee, isID := c.Callee.(*ast.Ident)
	if !isID {
		return true
	}
	sig := info.FuncSigs[callee.Name]
	if sig == nil {
		return true
	}
	for i, a := range c.Args {
		if id, ok := a.(*ast.Ident); !ok || id != aid {
			continue
		}
		if i >= len(sig.Params) {
			return true
		}
		_, isArr := sig.Params[i].(ast.ArrayType)
		return isArr
	}
	return true
}

func Mentions(n ast.Node, name string) int {
	k := 0
	ast.Walk(n, func(m ast.Node) bool {
		if id, ok := m.(*ast.Ident); ok && id.Name == name {
			k++
		}
		return true
	})
	return k
}

func JumpEscapes(st ast.Stmt) bool {
	inLoop := map[ast.Node]bool{}
	ast.Walk(st, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.While, *ast.Loop, *ast.For, *ast.ForEach:
			ast.Walk(n, func(m ast.Node) bool { inLoop[m] = true; return true })
		}
		return true
	})
	out := false
	ast.Walk(st, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Break:
			if !inLoop[n] || x.Label != "" {
				out = true
			}
		case *ast.Continue:
			if !inLoop[n] || x.Label != "" {
				out = true
			}
		}
		return true
	})
	return out
}

func markSupersededFields(out map[*ast.Call]map[string]bool, sl *ast.StructLit, target string) {
	if sl.Base == nil {
		return
	}
	if bid, ok := sl.Base.(*ast.Ident); !ok || bid.Name != target {
		return
	}
	for _, f := range sl.Fields {
		c, ok := f.Value.(*ast.Call)
		if !ok {
			continue
		}
		if _, named := c.Callee.(*ast.Ident); !named {
			continue
		}
		direct := 0
		for _, a := range c.Args {
			if fa, ok := a.(*ast.FieldAccess); ok && fa.Field == f.Name {
				if id, ok := fa.Target.(*ast.Ident); ok && id.Name == target {
					direct++
				}
			}
		}
		if direct != 1 {
			continue
		}
		total := 0
		ast.Walk(sl, func(m ast.Node) bool {
			if fa, ok := m.(*ast.FieldAccess); ok && fa.Field == f.Name {
				if id, ok := fa.Target.(*ast.Ident); ok && id.Name == target {
					total++
				}
			}
			return true
		})
		if total != 1 {
			continue
		}
		if out[c] == nil {
			out[c] = map[string]bool{}
		}
		out[c][target+"."+f.Name] = true
	}
}

func lastUseArgs(fn *ast.FuncDecl, info *Info) map[ast.Expr]bool {
	out := map[ast.Expr]bool{}
	if fn.Body == nil {
		return out
	}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		if inner, ok := n.(*ast.FuncDecl); ok && inner != fn {
			for e := range lastUseArgs(inner, info) {
				out[e] = true
			}
			return false
		}
		return true
	})
	d := CallArgDeaths(fn, info)
	isParam := map[string]bool{}
	ownParam := map[string]bool{}
	for _, p := range fn.Params {
		isParam[p.Name] = true
		if p.Own {
			ownParam[p.Name] = true
		}
	}
	local := map[string]bool{}
	nested := map[*ast.Call]bool{}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			if !isParam[x.Name] {
				local[x.Name] = true
			}
		case *ast.Destructure:
			for _, name := range x.Names {
				if !isParam[name] {
					local[name] = true
				}
			}
		case *ast.FuncDecl, *ast.Lambda:
			ast.Walk(x, func(m ast.Node) bool {
				if c, ok := m.(*ast.Call); ok {
					nested[c] = true
				}
				return true
			})
		}
		return true
	})
	for call, dies := range d.Dies {
		if nested[call] {
			continue
		}
		for _, a := range call.Args {
			if id, ok := a.(*ast.Ident); ok && dies[id.Name] && local[id.Name] && (!d.Escaping[id.Name] || d.Deferred[call]) {
				out[id] = true
			}
		}
	}
	for call, fields := range d.FieldDies {
		if nested[call] {
			continue
		}
		for fa := range fields {
			if id, ok := fa.Target.(*ast.Ident); ok && (local[id.Name] || ownParam[id.Name]) {
				out[fa] = true
			}
		}
	}
	return out
}

func storeTarget(st ast.Stmt) (string, *ast.Assign) {
	es, isExpr := st.(*ast.ExprStmt)
	if !isExpr {
		return "", nil
	}
	asn, isAsn := es.Expr.(*ast.Assign)
	if !isAsn || asn.Value == nil {
		return "", nil
	}
	t, isID := asn.Target.(*ast.Ident)
	if !isID {
		return "", nil
	}
	return t.Name, asn
}

func reboundAfter(gap []ast.Stmt, name string) bool {
	for _, st := range gap {
		switch st.(type) {
		case *ast.Var, *ast.Destructure, *ast.ExprStmt:
		default:
			return false
		}
		if StmtReferencesName(st, name) || holdsTry(st) {
			return false
		}
	}
	return true
}

func holdsTry(n ast.Node) bool {
	found := false
	ast.Walk(n, func(m ast.Node) bool {
		switch m.(type) {
		case *ast.TryOp:
			found = true
		case *ast.Lambda, *ast.FuncDecl:
			return false
		}
		return !found
	})
	return found
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

// enclosingFrames is the chain of blocks from body down to the innermost
// statement that holds target. A statement inside an expression-level block
// or a deferred action counts as its enclosing statement.
func enclosingFrames(body *ast.Block, target ast.Node) []liveFrame {
	if body == nil {
		return nil
	}
	return framesIn(body.Stmts, nil, "", target)
}

func framesIn(stmts []ast.Stmt, loop ast.Stmt, label string, target ast.Node) []liveFrame {
	for i, st := range stmts {
		if !nodeHolds(st, target) {
			continue
		}
		out := []liveFrame{{stmts: stmts, idx: i, loop: loop, label: label}}
		var sub []liveFrame
		switch x := st.(type) {
		case *ast.Block:
			sub = framesIn(x.Stmts, nil, "", target)
		case *ast.If:
			sub = framesInStmt(x.Then, target)
			if sub == nil && x.Else != nil {
				sub = framesInStmt(x.Else, target)
			}
		case *ast.Match:
			for _, arm := range x.Arms {
				if arm.Body != nil && nodeHolds(arm.Body, target) {
					sub = framesIn(arm.Body.Stmts, nil, "", target)
					break
				}
			}
		case *ast.While:
			sub = framesInLoopBody(x.Body, x, x.Label, target)
		case *ast.Loop:
			sub = framesInLoopBody(x.Body, x, x.Label, target)
		case *ast.For:
			sub = framesInLoopBody(x.Body, x, x.Label, target)
		case *ast.ForEach:
			sub = framesInLoopBody(x.Body, x, x.Label, target)
		}
		return append(out, sub...)
	}
	return nil
}

func framesInStmt(st ast.Stmt, target ast.Node) []liveFrame {
	if blk, ok := st.(*ast.Block); ok {
		return framesIn(blk.Stmts, nil, "", target)
	}
	if st == nil {
		return nil
	}
	return framesIn([]ast.Stmt{st}, nil, "", target)
}

func framesInLoopBody(body ast.Stmt, loop ast.Stmt, label string, target ast.Node) []liveFrame {
	if blk, ok := body.(*ast.Block); ok {
		return framesIn(blk.Stmts, loop, label, target)
	}
	if body == nil {
		return nil
	}
	return framesIn([]ast.Stmt{body}, loop, label, target)
}

// stmtJumps is the control that leaves a statement without completing it:
// a return or an exit call, and the breaks and continues that escape it
// with the label each one names ("" for the innermost loop).
type stmtJumps struct {
	returns   bool
	breaks    []string
	continues []string
}

func jumpsOf(info *Info, st ast.Stmt) stmtJumps {
	var out stmtJumps
	inner := map[string]bool{}
	ast.Walk(st, func(m ast.Node) bool {
		switch x := m.(type) {
		case *ast.Lambda, *ast.FuncDecl, *ast.Defer:
			return false
		case *ast.Loop:
			inner[x.Label] = true
		case *ast.While:
			inner[x.Label] = true
		case *ast.For:
			inner[x.Label] = true
		case *ast.ForEach:
			inner[x.Label] = true
		}
		return true
	})
	escapes := func(label string, nested bool) bool {
		if label == "" {
			return !nested
		}
		return !inner[label]
	}
	var walk func(n ast.Node, nested bool)
	walk = func(n ast.Node, nested bool) {
		ast.Walk(n, func(m ast.Node) bool {
			switch x := m.(type) {
			case *ast.Lambda, *ast.FuncDecl, *ast.Defer:
				return false
			case *ast.Return:
				out.returns = true
			case *ast.Call:
				if info != nil && info.IntrinsicCalls[x].Kind == IntrinsicExit {
					out.returns = true
				}
			case *ast.Break:
				if escapes(x.Label, nested) {
					out.breaks = append(out.breaks, x.Label)
				}
				return false
			case *ast.Continue:
				if escapes(x.Label, nested) {
					out.continues = append(out.continues, x.Label)
				}
				return false
			case *ast.Loop:
				walk(x.Body, true)
				return false
			case *ast.While:
				walk(x.Cond, nested)
				walk(x.Body, true)
				return false
			case *ast.For:
				if x.Init != nil {
					walk(x.Init, nested)
				}
				if x.Cond != nil {
					walk(x.Cond, nested)
				}
				if x.Step != nil {
					walk(x.Step, nested)
				}
				walk(x.Body, true)
				return false
			case *ast.ForEach:
				walk(x.Iter, nested)
				walk(x.Body, true)
				return false
			}
			return true
		})
	}
	walk(st, false)
	return out
}

// divergesStmt reports a statement that leaves on every path.
func divergesStmt(info *Info, st ast.Stmt) bool {
	switch x := st.(type) {
	case *ast.Return, *ast.Break, *ast.Continue:
		return true
	case *ast.ExprStmt:
		call, ok := x.Expr.(*ast.Call)
		return ok && info != nil && info.IntrinsicCalls[call].Kind == IntrinsicExit
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

func declaresName(n ast.Node, name string) bool {
	found := false
	ast.Walk(n, func(m ast.Node) bool {
		if found {
			return false
		}
		switch x := m.(type) {
		case *ast.Lambda, *ast.FuncDecl:
			return false
		case *ast.Var:
			found = x.Name == name
		case *ast.Destructure:
			for _, nm := range x.Names {
				if nm == name {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// loopFrameLevel is the level of the body frame of the loop a jump names:
// the innermost loop for "", else the loop carrying the label. -1 when none.
func loopFrameLevel(frames []liveFrame, level int, label string) int {
	for l := level; l >= 0; l-- {
		if frames[l].loop == nil {
			continue
		}
		if label == "" || frames[l].label == label {
			return l
		}
	}
	return -1
}

// loopHeadLive reports whether the loop whose body is frames[l] reads name
// on its next iteration: name is mentioned in the loop and is not a binding
// the body declares afresh each time round.
func loopHeadLive(frames []liveFrame, l int, name string) bool {
	loop := frames[l].loop
	if declaresName(loop, name) {
		return false
	}
	return StmtReferencesName(loop, name)
}

// liveAfter reports whether name can be read again on some path after the
// statement that holds c. A path that reaches the end of a loop body, or
// continues, comes back to every use in that loop.
func liveAfter(info *Info, body *ast.Block, c *ast.Call, aid *ast.Ident, order IdentOrder) bool {
	frames := enclosingFrames(body, c)
	if len(frames) == 0 {
		return true
	}
	inner := frames[len(frames)-1]
	st := inner.stmts[inner.idx]
	// A loop inside the statement runs the call again each round: the call
	// sits in a loop's head, or in a loop inside an expression-level block.
	repeats := false
	ast.Walk(st, func(m ast.Node) bool {
		switch m.(type) {
		case *ast.While, *ast.Loop, *ast.For, *ast.ForEach:
			if nodeHolds(m, c) {
				repeats = true
			}
		}
		return !repeats
	})
	if repeats {
		return true
	}
	later := false
	ast.Walk(st, func(m ast.Node) bool {
		if id, ok := m.(*ast.Ident); ok && id.Name == aid.Name && order.Idx[id] > order.Idx[aid] {
			later = true
		}
		return !later
	})
	if later {
		return true
	}
	return liveFrom(info, frames, len(frames)-1, aid.Name)
}

func liveFrom(info *Info, frames []liveFrame, level int, name string) bool {
	f := frames[level]
	for j := f.idx + 1; j < len(f.stmts); j++ {
		st := f.stmts[j]
		if StmtReferencesName(st, name) {
			return true
		}
		jumps := jumpsOf(info, st)
		for _, label := range jumps.continues {
			l := loopFrameLevel(frames, level, label)
			if l < 0 || loopHeadLive(frames, l, name) || liveFrom(info, frames, l-1, name) {
				return true
			}
		}
		for _, label := range jumps.breaks {
			l := loopFrameLevel(frames, level, label)
			if l < 0 || liveFrom(info, frames, l-1, name) {
				return true
			}
		}
		if divergesStmt(info, st) {
			return false
		}
	}
	if f.loop != nil && loopHeadLive(frames, level, name) {
		return true
	}
	if level == 0 {
		return false
	}
	return liveFrom(info, frames, level-1, name)
}
