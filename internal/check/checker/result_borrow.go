package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

func (c *checker) computeResultBorrows(prog *ast.Program) {
	c.resultBorrows = make(map[string]bool, len(prog.Funcs))
	c.resultAnalysed = make(map[string]bool, len(prog.Funcs))
	c.declaredMethods = map[string]bool{}
	c.handleFreeResults = map[string]bool{}
	for _, fn := range prog.Funcs {
		if fn.MethodSimpleName != "" {
			c.declaredMethods[fn.MethodSimpleName] = true
		}
		if fn.ReturnType != nil && c.holdsNoHandle(fn.ReturnType) {
			c.handleFreeResults[fn.Name] = true
		}
	}
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
	if !ok || x.Method != nil && c.freshMethodResult(x) {
		return !c.freshMethodResult(x)
	}
	if _, vrOk, _ := c.resolveVariant(id.Name, id.EnumName); vrOk {
		// A variant carries its payload, so the result borrows what the
		// payload borrows.
		return true
	}
	if c.resultAnalysed[id.Name] {
		return c.resultBorrows[id.Name]
	}
	return c.signatureResultBorrows(id.Name)
}

// callSiteBorrows reports a plain call whose result can borrow: the callee's
// result can hold a handle and borrows, and an argument is not a fresh value.
func (c *checker) callSiteBorrows(x *ast.Call) bool {
	if id, ok := x.Callee.(*ast.Ident); ok && c.handleFreeResults[id.Name] {
		return false
	}
	if !c.calleeResultBorrows(x) {
		return false
	}
	for _, a := range x.Args {
		if c.argMayBorrow(a) {
			return true
		}
	}
	return false
}

func (c *checker) argMayBorrow(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.NumberLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.CharLit, *ast.UnitLit,
		*ast.StructLit, *ast.TupleLit, *ast.ArrayLit, *ast.MapLit, *ast.EnumLit,
		*ast.Binary, *ast.Unary, *ast.CastExpr:
		return false
	case *ast.Call:
		if _, named := x.Callee.(*ast.Ident); !named || x.Method != nil {
			return true
		}
		return c.callSiteBorrows(x)
	}
	return true
}

// freshMethodResult reports a call of a built-in method whose result is a
// kernel descriptor, a directory opened under another directory, when no
// method the program declares has the same name. The name decides, so the
// answer is the same before and after the checker resolves the receiver.
func (c *checker) freshMethodResult(x *ast.Call) bool {
	field := ""
	if x.Method != nil {
		field = x.Method.Field
	} else if fa, ok := x.Callee.(*ast.FieldAccess); ok {
		field = fa.Field
	}
	return freshBuiltinMethods[field] && !c.declaredMethods[field]
}

var freshBuiltinMethods = map[string]bool{"open_dir": true}

// A handle an open builtin returns is a kernel descriptor: it borrows nothing
// from the path it was opened with.
var freshResultBuiltins = map[string]bool{
	"__bytes_owned": true, "__bytes_range": true, "__bytes_into": true, "__aes_expand_key": true, "__aes_ctr32": true,
	"__ghash": true, "__aes_gcm_seal_into": true, "__aes_gcm_open_into": true, "__sha256_hw_blocks": true, "__sha1_hw_blocks": true,
	"open_reader": true, "open_writer": true, "open_appender": true, "open_exclusive": true,
	"open_reader_with": true, "open_writer_with": true, "open_dir": true,
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
		// A parameter that cannot hold a handle cannot lend one to the result.
		if (i >= len(flags) || !flags[i]) && !c.holdsNoHandle(pt) {
			return true
		}
	}
	return false
}

// A call scrutinee is owned when its result is fresh by signature: a
// variant, a fresh method, or a function with no borrowed pointer parameter
// that can hold a handle.
func (c *checker) isOwnedScrutinee(tag ast.Expr, inferred func(ast.Expr) bool) bool {
	if call, ok := tag.(*ast.Call); ok {
		id, isIdent := call.Callee.(*ast.Ident)
		if !isIdent {
			return c.freshMethodResult(call)
		}
		if _, vrOk, _ := c.resolveVariant(id.Name, id.EnumName); vrOk {
			return true
		}
		sig, known := c.info.FuncSigs[id.Name]
		if !known || sig.Result == nil || !ast.IsPointerType(sig.Result) {
			return false
		}
		if call.Method != nil {
			return c.freshMethodResult(call)
		}
		return !c.signatureResultBorrows(id.Name)
	}
	return inferred(tag)
}

// holdsNoHandle reports a type that cannot hold a handle: a number, a
// boolean, a char, a string, a function, or an array, slice, tuple, option,
// result, struct or enum whose parts are all such types. Reader, Writer and
// Dir are handles, and so is a type with a method that consumes its
// receiver. A function cannot consume what it captures, so it cannot close
// a handle.
func (c *checker) holdsNoHandle(t ast.Type) bool {
	return c.noHandleIn(t, nil, map[string]bool{})
}

// noHandleIn decides t inside a declaration whose type parameters in free
// stand for arguments that hold no handle. seen holds the declarations on
// the path, so a recursive type is decided by its other parts.
func (c *checker) noHandleIn(t ast.Type, free map[string]bool, seen map[string]bool) bool {
	switch x := t.(type) {
	case ast.NumberType, ast.FloatType, ast.BoolType, ast.VoidType, ast.StringType, ast.StrType, ast.CharType, ast.NeverType, ast.LanesType:
		return true
	case ast.ParamType:
		return free[x.Name]
	case *ast.FuncType:
		return true
	case ast.ArrayType:
		return c.noHandleIn(x.Elem, free, seen)
	case ast.SliceType:
		return c.noHandleIn(x.Elem, free, seen)
	case ast.TupleType:
		return c.allNoHandleIn(x.Elems, free, seen)
	case ast.EnumType:
		if !c.allNoHandleIn(x.Args, free, seen) {
			return false
		}
		if x.Name == "Option" || x.Name == "Result" {
			return true
		}
		ed, ok := c.info.Enums[x.Name]
		if !ok || c.consumingTypes[x.Name] {
			return false
		}
		if seen[x.Name] {
			return true
		}
		seen[x.Name] = true
		inner := paramSet(ed.TypeParams)
		for _, v := range ed.Variants {
			if !c.allNoHandleIn(v.Payloads, inner, seen) {
				return false
			}
		}
		return true
	case ast.StructType:
		sd, ok := c.info.Structs[x.Name]
		if !ok || builtinHandle(x.Name) || c.consumingTypes[x.Name] || !c.allNoHandleIn(x.Args, free, seen) {
			return false
		}
		if seen[x.Name] {
			return true
		}
		seen[x.Name] = true
		inner := paramSet(sd.TypeParams)
		for _, f := range sd.Fields {
			if !c.noHandleIn(f.Type, inner, seen) {
				return false
			}
		}
		return true
	}
	return false
}

func (c *checker) allNoHandleIn(ts []ast.Type, free map[string]bool, seen map[string]bool) bool {
	for _, t := range ts {
		if t == nil || !c.noHandleIn(t, free, seen) {
			return false
		}
	}
	return true
}

func builtinHandle(name string) bool {
	return name == "Reader" || name == "Writer" || name == "Dir"
}

func paramSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}
