package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

func (c *checker) computeResultBorrows(prog *ast.Program) {
	c.resultBorrows = make(map[string]bool, len(prog.Funcs))
	c.resultAnalysed = make(map[string]bool, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		if fn.Body != nil {
			c.resultAnalysed[fn.Name] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range prog.Funcs {
			if fn.Body == nil || c.resultBorrows[fn.Name] {
				continue
			}
			if c.funcResultBorrows(fn) {
				c.resultBorrows[fn.Name] = true
				changed = true
			}
		}
	}
}

func (c *checker) funcResultBorrows(fn *ast.FuncDecl) bool {
	b := borrowScope{borrowed: map[string]bool{}, values: map[string]bool{}}
	for _, p := range fn.Params {
		if !p.Own && p.Type != nil && ast.IsPointerType(p.Type) {
			b.borrowed[p.Name] = true
			if elem := elemType(p.Type); elem != nil && !ast.IsPointerType(elem) {
				b.values[p.Name] = true
			}
		}
	}
	if len(b.borrowed) == 0 {
		return false
	}
	b.locals = c.borrowedLocals(fn, b)
	found := false
	ast.Walk(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch s := n.(type) {
		case *ast.Lambda:
			return false
		case *ast.Return:
			if s.Value != nil && c.exprBorrows(s.Value, b) {
				found = true
			}
		}
		return true
	})
	return found
}

func (c *checker) borrowedLocals(fn *ast.FuncDecl, b borrowScope) map[string]bool {
	b.locals = map[string]bool{}
	mark := func(name string, init ast.Expr) bool {
		if name == "" || b.locals[name] || init == nil {
			return false
		}
		if !c.exprBorrows(init, b) {
			return false
		}
		b.locals[name] = true
		return true
	}
	for changed := true; changed; {
		changed = false
		ast.Walk(fn.Body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.Lambda:
				return false
			case *ast.Var:
				changed = mark(s.Name, s.Init) || changed
			case *ast.Assign:
				if id, ok := s.Target.(*ast.Ident); ok {
					changed = mark(id.Name, s.Value) || changed
				}
			}
			return true
		})
	}
	return b.locals
}

type borrowScope struct {
	borrowed, locals, values map[string]bool
}

func elemType(t ast.Type) ast.Type {
	switch at := t.(type) {
	case ast.ArrayType:
		return at.Elem
	case ast.SliceType:
		return at.Elem
	}
	return nil
}

func (c *checker) exprBorrows(e ast.Expr, b borrowScope) bool {
	switch x := e.(type) {
	case nil:
		return false
	case *ast.NumberLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.CharLit, *ast.UnitLit,
		*ast.StructLit, *ast.TupleLit, *ast.ArrayLit, *ast.MapLit, *ast.EnumLit,
		*ast.Binary, *ast.Unary, *ast.CastExpr:
		return false
	case *ast.Ident:
		return b.borrowed[x.Name] || b.locals[x.Name]
	case *ast.FieldAccess:
		return c.exprBorrows(x.Target, b)
	case *ast.Index:
		if id, ok := x.Array.(*ast.Ident); ok && b.values[id.Name] {
			return false
		}
		return c.exprBorrows(x.Array, b)
	case *ast.SliceExpr:
		return c.exprBorrows(x.Source, b)
	case *ast.IfExpr:
		return c.exprBorrows(x.Then, b) || c.exprBorrows(x.Else, b)
	case *ast.Call:
		if !c.calleeResultBorrows(x) {
			return false
		}
		for _, a := range x.Args {
			if c.exprBorrows(a, b) {
				return true
			}
		}
		return false
	}
	return true
}

func (c *checker) calleeResultBorrows(x *ast.Call) bool {
	id, ok := x.Callee.(*ast.Ident)
	if !ok {
		return true
	}
	if _, vrOk, _ := c.resolveVariant(id.Name, id.EnumName); vrOk {
		return false
	}
	if c.resultAnalysed[id.Name] {
		return c.resultBorrows[id.Name]
	}
	return c.signatureResultBorrows(id.Name)
}

var freshResultBuiltins = map[string]bool{
	"__bytes_owned": true, "__bytes_range": true, "__bytes_into": true, "__aes_expand_key": true, "__aes_ctr32": true,
	"__ghash": true, "__aes_gcm_seal": true, "__aes_gcm_open": true, "__sha256_hw_blocks": true, "__sha1_hw_blocks": true,
}

func (c *checker) signatureResultBorrows(name string) bool {
	if freshResultBuiltins[name] {
		return false
	}
	sig, ok := c.info.FuncSigs[name]
	if !ok {
		return true
	}
	flags := c.ownFuncs[name]
	for i, pt := range sig.Params {
		if pt == nil || !ast.IsPointerType(pt) {
			continue
		}
		if i >= len(flags) || !flags[i] {
			return true
		}
	}
	return false
}

func (c *checker) isOwnedScrutinee(tag ast.Expr, inferred func(ast.Expr) bool) bool {
	if call, ok := tag.(*ast.Call); ok {
		id, isIdent := call.Callee.(*ast.Ident)
		if !isIdent {
			return false
		}
		if _, vrOk, _ := c.resolveVariant(id.Name, id.EnumName); vrOk {
			return true
		}
		sig, known := c.info.FuncSigs[id.Name]
		if !known || sig.Result == nil || !ast.IsPointerType(sig.Result) {
			return false
		}
		return !c.signatureResultBorrows(id.Name)
	}
	return inferred(tag)
}
