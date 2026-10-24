package checker

import (
	"maps"

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
	esc := map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		var sub ast.Node
		switch d := n.(type) {
		case *ast.Defer:
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
	Dies        map[*ast.Call]map[string]bool
	Repeating   map[*ast.Call]bool
	Escaping    map[string]bool
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
	frameOwns := map[string]bool{}
	for _, p := range fn.Params {
		isParam[p.Name] = true
		if p.Own {
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
		if !isID || !freshInitLocal[hid.Name] {
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
				return true
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
	fresh := loopFreshNames(body, declCount, isParam)
	repeats := func(c *ast.Call, name string) bool {
		return repeating[c] && !fresh[c][name]
	}
	escaping := DeferOrLambdaNames(body)
	order := IdentOrderOf(body)
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok {
			return true
		}
		for _, a := range c.Args {
			aid, ok := a.(*ast.Ident)
			if !ok || repeats(c, aid.Name) || escaping[aid.Name] || !order.IsLast(aid) {
				continue
			}
			if !admitted(aid.Name) {
				continue
			}
			if heldByEnclosingCall(body, c, aid.Name) {
				continue
			}
			markOnce(c, aid.Name)
		}
		return true
	})
	stmtIdx := callBlockPositions(body)
	ast.Walk(body, func(n ast.Node) bool {
		c, ok := n.(*ast.Call)
		if !ok {
			return true
		}
		for _, a := range c.Args {
			aid, ok := a.(*ast.Ident)
			if !ok || repeats(c, aid.Name) || escaping[aid.Name] || order.IsLast(aid) {
				continue
			}
			if !admitted(aid.Name) {
				continue
			}
			if arrayArgPosition(info, c, aid) {
				continue
			}
			if !returnsBeforeReading(stmtIdx, c, aid.Name) {
				continue
			}
			markOnce(c, aid.Name)
		}
		return true
	})
	return ArgDeaths{Dies: out, Repeating: repeating, Escaping: escaping, Occurrences: occurrences}
}

func freshInit(init ast.Expr) bool {
	switch init := init.(type) {
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

type blockPos struct {
	blk *ast.Block
	idx int
}

func callBlockPositions(body ast.Node) map[*ast.Call]blockPos {
	var blocks []*ast.Block
	ast.Walk(body, func(n ast.Node) bool {
		if b, ok := n.(*ast.Block); ok {
			blocks = append(blocks, b)
		}
		return true
	})
	out := map[*ast.Call]blockPos{}
	for _, b := range blocks {
		for i, st := range b.Stmts {
			ast.Walk(st, func(n ast.Node) bool {
				if c, ok := n.(*ast.Call); ok {
					out[c] = blockPos{blk: b, idx: i}
				}
				return true
			})
		}
	}
	return out
}

func returnsBeforeReading(pos map[*ast.Call]blockPos, c *ast.Call, name string) bool {
	bp, ok := pos[c]
	if !ok {
		return false
	}
	if Mentions(bp.blk.Stmts[bp.idx], name) != 1 {
		return false
	}
	for i := bp.idx + 1; i < len(bp.blk.Stmts); i++ {
		if Mentions(bp.blk.Stmts[i], name) != 0 || JumpEscapes(bp.blk.Stmts[i]) {
			return false
		}
		if _, isRet := bp.blk.Stmts[i].(*ast.Return); isRet {
			return true
		}
	}
	return false
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
	for _, p := range fn.Params {
		isParam[p.Name] = true
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
			if id, ok := a.(*ast.Ident); ok && dies[id.Name] && local[id.Name] && !d.Escaping[id.Name] {
				out[id] = true
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

func loopFreshNames(body ast.Node, declCount map[string]int, isParam map[string]bool) map[*ast.Call]map[string]bool {
	out := map[*ast.Call]map[string]bool{}
	ast.Walk(body, func(n ast.Node) bool {
		if !repeatsBody(n) {
			return true
		}
		declared := map[string]bool{}
		declare := func(name string) {
			if declCount[name] == 1 && !isParam[name] {
				declared[name] = true
			}
		}
		ast.Walk(n, func(m ast.Node) bool {
			if m != n && repeatsBody(m) {
				return false
			}
			switch x := m.(type) {
			case *ast.Var:
				declare(x.Name)
			case *ast.Destructure:
				for _, name := range x.Names {
					declare(name)
				}
			case *ast.Call:
				if len(declared) > 0 {
					out[x] = maps.Clone(declared)
				}
			}
			return true
		})
		return true
	})
	return out
}

func repeatsBody(n ast.Node) bool {
	switch n.(type) {
	case *ast.While, *ast.Loop, *ast.For, *ast.ForEach, *ast.Lambda:
		return true
	}
	return false
}
