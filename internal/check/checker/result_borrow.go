package checker

import (
	"slices"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func (c *checker) recordOwnedResult(call *ast.Call, own bool) {
	if own {
		if c.callOwnResults == nil {
			c.callOwnResults = map[*ast.Call]bool{}
		}
		c.callOwnResults[call] = true
	} else {
		delete(c.callOwnResults, call)
	}
}

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
	return c.funcResultBorrowsWith(fn, false)
}

func (c *checker) funcResultBorrowsWith(fn *ast.FuncDecl, ownedResult bool) bool {
	// A scalar result is a copy, so it cannot borrow from a parameter.
	if fn.ReturnType != nil && definitelyScalar(fn.ReturnType) {
		return false
	}
	if ownedResult && c.valueData(fn.ReturnType, map[string]bool{}) {
		return false
	}
	b := borrowScope{borrowed: map[string]bool{}, values: map[string]bool{}, handlesOnly: !c.holdsNoHandle(fn.ReturnType), ownedResult: ownedResult}
	for _, p := range fn.Params {
		if c.callbackCanLend(p.Type) || !p.Own && p.Type != nil && mayRetainBorrow(p.Type) {
			b.borrowed[p.Name] = true
			if elem := elemType(p.Type); elem != nil && !mayRetainBorrow(elem) {
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
			case *ast.ForEach:
				changed = markForEach(mark, s, b.values) || changed
			case *ast.Block:
				// The checker lowers a `for` into a block, and the block
				// keeps the loop as its Sugar. The answer must not depend
				// on whether the lowering already ran.
				if s.Sugar != nil {
					changed = markForEach(mark, s.Sugar, b.values) || changed
				}
			}
			return true
		})
	}
	return b.locals
}

// Scalar array elements are copies. Other elements retain the iterable borrow.
func markForEach(mark func(string, ast.Expr) bool, fe *ast.ForEach, values map[string]bool) bool {
	if fe.Pattern != nil || fe.RangeHigh != nil || valueElementIterable(fe.Iter, values) {
		return false
	}
	return mark(fe.Var, fe.Iter)
}

func valueElementIterable(e ast.Expr, values map[string]bool) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return values[x.Name]
	case *ast.SliceExpr:
		return valueElementIterable(x.Source, values)
	}
	return false
}

type borrowScope struct {
	borrowed, locals, values map[string]bool
	handlesOnly              bool
	ownedResult              bool
}

// binding is the scope inside a pattern that binds names. They shadow the
// outer names, and they borrow only when lends is true.
func (b borrowScope) binding(names []string, lends bool) borrowScope {
	if len(names) == 0 {
		return b
	}
	out := borrowScope{borrowed: map[string]bool{}, locals: map[string]bool{}, values: b.values, handlesOnly: b.handlesOnly, ownedResult: b.ownedResult}
	for k, v := range b.borrowed {
		out.borrowed[k] = v
	}
	for k, v := range b.locals {
		out.locals[k] = v
	}
	for _, n := range names {
		out.borrowed[n] = lends
		delete(out.locals, n)
	}
	return out
}

// A type parameter can hold a handle after substitution.
func mayRetainBorrow(t ast.Type) bool {
	_, parameter := t.(ast.ParamType)
	return parameter || ast.IsPointerType(t)
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

func (c *checker) resultPartBorrows(e ast.Expr, b borrowScope) bool {
	return !c.resultCopyParts[e] && c.exprBorrows(e, b)
}

func (c *checker) exprBorrows(e ast.Expr, b borrowScope) bool {
	if b.ownedResult {
		if c.scalarArgs[e] || c.valueArgs[e] {
			return false
		}
		switch x := e.(type) {
		case *ast.BlockExpr:
			return x.Tail == nil || c.exprBorrows(x.Tail, b)
		case *ast.StructLit:
			if x.Base != nil && c.resultPartBorrows(x.Base, b) {
				return true
			}
			for _, f := range x.Fields {
				if c.resultPartBorrows(f.Value, b) {
					return true
				}
			}
			return false
		case *ast.ArrayLit:
			return slices.ContainsFunc(x.Elems, func(e ast.Expr) bool { return c.resultPartBorrows(e, b) })
		case *ast.TupleLit:
			return slices.ContainsFunc(x.Elems, func(e ast.Expr) bool { return c.resultPartBorrows(e, b) })
		case *ast.MapLit:
			for _, entry := range x.Entries {
				if c.resultPartBorrows(entry.Key, b) || c.resultPartBorrows(entry.Value, b) {
					return true
				}
			}
			return false
		case *ast.Call:
			if c.callOwnResults[x] {
				return false
			}
			if c.exprBorrows(x.Callee, b) {
				return true
			}
			flags := c.callOwnFlags[x]
			if id, ok := x.Callee.(*ast.Ident); ok && flags == nil {
				flags = c.ownFuncs[id.Name]
			}
			for i, arg := range x.Args {
				if (i >= len(flags) || !flags[i]) && c.resultPartBorrows(arg, b) {
					return true
				}
			}
			return false
		}
	}
	switch x := e.(type) {
	case nil:
		return false
	case *ast.Lambda:
		return c.lambdaResultBorrows(x, b)
	case *ast.NumberLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.CharLit, *ast.UnitLit,
		*ast.StructLit, *ast.TupleLit, *ast.ArrayLit, *ast.MapLit, *ast.EnumLit,
		*ast.Binary, *ast.Unary, *ast.CastExpr:
		return false
	case *ast.Ident:
		return b.borrowed[x.Name] || b.locals[x.Name]
	case *ast.BlockExpr:
		if x.Unsafe && len(x.Stmts) == 0 && x.Tail != nil {
			return c.exprBorrows(x.Tail, b)
		}
		return true
	case *ast.FieldAccess:
		return c.exprBorrows(x.Target, b)
	case *ast.Index:
		// Lowered for-each loops index a synthetic iterable alias.
		if x.ElemType != nil && !mayRetainBorrow(x.ElemType) {
			return false
		}
		if id, ok := x.Array.(*ast.Ident); ok && b.values[id.Name] {
			return false
		}
		return c.exprBorrows(x.Array, b)
	case *ast.SliceExpr:
		return c.exprBorrows(x.Source, b)
	case *ast.IfExpr:
		return c.exprBorrows(x.Then, b) || c.exprBorrows(x.Else, b)
	case *ast.MatchExpr:
		// An arm's binders come from the scrutinee, so they borrow only when it does.
		lends := c.exprBorrows(x.Tag, b)
		for _, arm := range x.Arms {
			if c.exprBorrows(arm.Body, b.binding(arm.Binders(), lends)) {
				return true
			}
		}
		return false
	case *ast.Call:
		if !c.calleeResultBorrows(x) {
			return false
		}
		if c.exprBorrows(x.Callee, b) {
			return true
		}
		// Dynamic methods keep the receiver on the callee, outside Args.
		if fa, ok := x.Callee.(*ast.FieldAccess); ok && x.Method != nil && c.exprBorrows(fa.Target, b) {
			return true
		}
		var sig *ast.FuncType
		var flags []bool
		if id, ok := x.Callee.(*ast.Ident); ok {
			sig = c.info.FuncSigs[id.Name]
			flags = c.ownFuncs[id.Name]
		}
		for i, a := range x.Args {
			if (!b.handlesOnly || c.paramCanLend(sig, flags, i)) && c.exprBorrows(a, b) {
				return true
			}
		}
		return false
	}
	return true
}

func (c *checker) calleeResultBorrows(x *ast.Call) bool {
	if c.callOwnResults[x] {
		return false
	}
	if freshAssociatedResult(x) {
		return false
	}
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
	if id.Name == "__method_Cell_get" && x.Method != nil {
		if recv, ok := x.Method.Receiver.(ast.StructType); ok && len(recv.Args) == 1 {
			return !c.holdsNoHandle(recv.Args[0])
		}
	}
	return c.signatureResultBorrows(id.Name)
}

// callSiteBorrows reports a plain call whose result can borrow: the callee's
// result can hold a handle and borrows, and an argument that can lend a
// handle is not a fresh value.
func (c *checker) callSiteBorrows(x *ast.Call) bool {
	return c.callSiteBorrowsBeside(x, "")
}

// callSiteBorrowsBeside is callSiteBorrows without the arguments that are
// the plain name except: `x = f(x)` leaves x the one holder of what the
// result took from it.
func (c *checker) callSiteBorrowsBeside(x *ast.Call, except string) bool {
	id, named := x.Callee.(*ast.Ident)
	if named && c.handleFreeResults[id.Name] {
		return false
	}
	if !c.calleeResultBorrows(x) {
		return false
	}
	var sig *ast.FuncType
	var flags []bool
	if named {
		sig = c.info.FuncSigs[id.Name]
		flags = c.ownFuncs[id.Name]
	}
	for i, a := range x.Args {
		if aid, ok := a.(*ast.Ident); ok && except != "" && aid.Name == except {
			continue
		}
		if c.argMayBorrow(a) && c.paramCanLend(sig, flags, i) {
			return true
		}
	}
	return false
}

// paramCanLend reports a parameter of the signature that can lend a handle
// to the result: a borrowed type that can hold one. A parameter the
// signature does not know can.
func (c *checker) paramCanLend(sig *ast.FuncType, flags []bool, i int) bool {
	if sig == nil || i >= len(sig.Params) || sig.Params[i] == nil {
		return true
	}
	if i < len(flags) && flags[i] && !c.callbackCanLend(sig.Params[i]) {
		return false
	}
	pt := sig.Params[i]
	return c.callbackCanLend(pt) || mayRetainBorrow(pt) && !c.holdsNoHandle(pt)
}

// A callback can return a captured handle without consuming its capture.
func (c *checker) callbackCanLend(t ast.Type) bool {
	fn, ok := t.(*ast.FuncType)
	return ok && !fn.ResultOwn && (c.callbackCanLend(fn.Result) || mayRetainBorrow(fn.Result) && !c.holdsNoHandle(fn.Result))
}

func (c *checker) lambdaResultBorrows(lm *ast.Lambda, outer borrowScope) bool {
	if lm.ReturnType != nil && definitelyScalar(lm.ReturnType) {
		return false
	}
	b := borrowScope{borrowed: map[string]bool{}, values: outer.values, handlesOnly: lm.ReturnType != nil && !c.holdsNoHandle(lm.ReturnType), ownedResult: outer.ownedResult}
	for name := range outer.borrowed {
		b.borrowed[name] = true
	}
	for name := range outer.locals {
		b.borrowed[name] = true
	}
	for _, p := range lm.Params {
		delete(b.borrowed, p.Name)
	}
	b.locals = c.borrowedLocals(&ast.FuncDecl{Body: lm.Body}, b)
	if c.lambdaRetainsCapture(lm, b) {
		return true
	}
	found := false
	ast.Walk(lm.Body, func(n ast.Node) bool {
		if _, nested := n.(*ast.Lambda); nested || found {
			return false
		}
		if r, ok := n.(*ast.Return); ok && c.exprBorrows(r.Value, b) {
			found = true
		}
		return true
	})
	return found
}

// Fresh containers can retain captures too. Keep this proof conservative
// for aggregates, including aliases built before the callback returns.
func (c *checker) lambdaRetainsCapture(lm *ast.Lambda, b borrowScope) bool {
	found := false
	ast.Walk(lm.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch x := n.(type) {
		case *ast.Lambda:
			return false
		case *ast.StructLit:
			if !c.structLitOwned(x, func(e ast.Expr) bool { return !c.exprBorrows(e, b) }) {
				found = true
			}
			return !found
		case *ast.ArrayLit, *ast.TupleLit, *ast.MapLit:
			if b.ownedResult {
				found = c.exprBorrows(x.(ast.Expr), b)
				return false
			}
			ast.Walk(n, func(part ast.Node) bool {
				if id, ok := part.(*ast.Ident); ok && (b.borrowed[id.Name] || b.locals[id.Name]) {
					found = true
				}
				return !found
			})
			return false
		}
		return true
	})
	return found
}

// An actual callback that returns its own input or a fresh value cannot lend
// a capture. Keep other borrowed arguments conservative at direct own calls.
func (c *checker) callbacksReturnOwned(call *ast.Call) bool {
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return false
	}
	sig := c.info.FuncSigs[id.Name]
	if sig == nil {
		return false
	}
	flags := c.ownFuncs[id.Name]
	found := false
	for i, arg := range call.Args {
		if !c.paramCanLend(sig, flags, i) {
			continue
		}
		if i >= len(sig.Params) {
			return false
		}
		if _, callback := sig.Params[i].(*ast.FuncType); !callback {
			return false
		}
		lm, literal := arg.(*ast.Lambda)
		if !literal || c.argMayBorrow(lm) {
			return false
		}
		found = true
	}
	return found
}

func (c *checker) argMayBorrow(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Lambda:
		return c.lambdaResultBorrows(x, borrowScope{borrowed: lambdaFreeNames(x)})
	case *ast.BlockExpr:
		if x.Unsafe && len(x.Stmts) == 0 && x.Tail != nil {
			return c.argMayBorrow(x.Tail)
		}
		return true
	case *ast.NumberLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.CharLit, *ast.UnitLit,
		*ast.StructLit, *ast.TupleLit, *ast.ArrayLit, *ast.MapLit, *ast.EnumLit,
		*ast.Binary, *ast.Unary, *ast.CastExpr:
		return false
	case *ast.Call:
		if freshAssociatedResult(x) {
			return false
		}
		if _, named := x.Callee.(*ast.Ident); !named || x.Method != nil {
			return true
		}
		return c.callSiteBorrows(x)
	}
	return true
}

// An associated call has no receiver or captured environment that can lend.
func freshAssociatedResult(call *ast.Call) bool {
	return call.Method != nil && call.Method.Assoc && len(call.Args) == 0
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

var freshBuiltinMethods = map[string]bool{"open_dir": true, "open_reader": true, "open_writer": true, "open_reader_with": true, "open_writer_with": true}

// These builtins promise an accepted owned result. share_unchecked makes
// this promise explicit for a shared value inside an unsafe block.
var freshResultBuiltins = map[string]bool{
	"share_unchecked": true,
	"__bytes_owned":   true, "__bytes_range": true, "__bytes_into": true, "__aes_expand_key": true, "__aes_ctr32": true,
	"__ghash": true, "__aes_gcm_seal_into": true, "__aes_gcm_open_into": true, "__sha256_hw_blocks": true, "__sha1_hw_blocks": true,
	"open_reader": true, "open_writer": true, "open_appender": true, "open_exclusive": true,
	"open_reader_with": true, "open_writer_with": true, "open_dir": true,
	"__socket_from_fd": true, "tcp_accept": true, "tcp_accept_with": true, "tcp_connect_with": true, "tcp_listen": true, "tcp_listen_with": true,
	"udp_bind": true, "udp_bind_with": true, "unix_connect": true, "unix_listen": true,
}

func (c *checker) signatureResultBorrows(name string) bool {
	if freshResultBuiltins[name] {
		return false
	}
	sig, ok := c.info.FuncSigs[name]
	if !ok {
		return true
	}
	if sig.ResultOwn {
		return false
	}
	flags := c.ownFuncs[name]
	for i := range sig.Params {
		if c.paramCanLend(sig, flags, i) {
			return true
		}
	}
	return false
}

// A call scrutinee is owned when its result is fresh: a variant, a fresh
// method, a function with no borrowed parameter that can hold a
// handle, or a function whose body returns a handle-bearing value with no borrow.
func (c *checker) isOwnedScrutinee(tag ast.Expr, inferred func(ast.Expr) bool) bool {
	if call, ok := tag.(*ast.Call); ok {
		if freshAssociatedResult(call) {
			return true
		}
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
			return c.freshMethodResult(call) || !c.signatureResultBorrows(id.Name)
		}
		return !c.signatureResultBorrows(id.Name) || c.resultAnalysed[id.Name] && !c.resultBorrows[id.Name] && !c.holdsNoHandle(sig.Result)
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

// structLitOwned says a struct literal owns what it holds: each
// field value is owned or holds no handle, and each field that the literal
// keeps from a base that is not owned holds no handle.
func (c *checker) structLitOwned(sl *ast.StructLit, owned func(ast.Expr) bool) bool {
	decl := c.info.Structs[sl.TypeName]
	if decl == nil {
		return false
	}
	st, _ := c.litTypes[sl].(ast.StructType)
	sub := declArgs(decl.TypeParams, st.Args)
	fieldType := map[string]ast.Type{}
	for _, f := range decl.Fields {
		fieldType[f.Name] = substituteType(f.Type, sub)
	}
	noHandle := func(t ast.Type) bool {
		return t != nil && c.holdsNoHandle(t)
	}
	written := map[string]bool{}
	for _, f := range sl.Fields {
		written[f.Name] = true
		if !owned(f.Value) && !noHandle(fieldType[f.Name]) {
			return false
		}
	}
	if sl.Base != nil && !owned(sl.Base) {
		for _, f := range decl.Fields {
			if !written[f.Name] && !noHandle(fieldType[f.Name]) {
				return false
			}
		}
	}
	return true
}

// arrayUpdateOwned says call is `with`, `append` or `truncate` on an array,
// and the result owns its elements: they hold no handle, or the receiver
// and the new element are owned.
func (c *checker) arrayUpdateOwned(call *ast.Call, recvOwned, owned func(ast.Expr) bool) bool {
	id, ok := call.Callee.(*ast.Ident)
	if !ok || call.Method == nil || len(call.Args) == 0 {
		return false
	}
	elem := -1
	switch id.Name {
	case "__method_Array_set":
		elem = 2
	case "__method_Array_push":
		elem = 1
	case "__method_Array_truncate":
	default:
		return false
	}
	array, ok := call.Method.Receiver.(ast.ArrayType)
	if !ok {
		return false
	}
	if c.holdsNoHandle(array.Elem) {
		return true
	}
	return recvOwned(call.Args[0]) && (elem < 0 || elem < len(call.Args) && owned(call.Args[elem]))
}

// replacedReads maps a read `b.f` to the literal `S { ...b, f: v }` that
// holds it in v. It does this only when v names the root of b in that read
// alone. The literal drops b.f, so the read takes what b held there: it is
// owned when b is. Inside v, the reads that an inner literal with base b.f
// replaces belong to the inner literal.
func replacedReads(body ast.Node) map[*ast.FieldAccess]*ast.StructLit {
	var lits []*ast.StructLit
	ast.Walk(body, func(n ast.Node) bool {
		if sl, ok := n.(*ast.StructLit); ok && sl.Base != nil {
			lits = append(lits, sl)
		}
		return true
	})
	out := map[*ast.FieldAccess]*ast.StructLit{}
	for i := len(lits) - 1; i >= 0; i-- {
		sl := lits[i]
		root := accessRoot(sl.Base)
		if root == "" {
			continue
		}
		for _, f := range sl.Fields {
			var read *ast.FieldAccess
			mentions := 0
			ast.Walk(f.Value, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Lambda:
					// A lambda can run later, so it takes no field.
					ast.Walk(x, func(m ast.Node) bool {
						if id, ok := m.(*ast.Ident); ok && id.Name == root {
							mentions += 2
						}
						return true
					})
					return false
				case *ast.FieldAccess:
					if inner := out[x]; inner != nil && sameAccess(x.Target, inner.Base) {
						return false
					}
					if x.Field == f.Name && sameAccess(x.Target, sl.Base) {
						read = x
						mentions++
						return false
					}
				case *ast.Ident:
					if x.Name == root {
						mentions++
					}
				}
				return true
			})
			if read != nil && mentions == 1 {
				out[read] = sl
			}
		}
	}
	return out
}

// accessRoot is the local that a name or a field path of it starts from.
func accessRoot(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.FieldAccess:
		return accessRoot(x.Target)
	}
	return ""
}

// sameAccess says a and b name the same local or the same field path of it.
func sameAccess(a, b ast.Expr) bool {
	switch x := a.(type) {
	case *ast.Ident:
		y, ok := b.(*ast.Ident)
		return ok && x.Name == y.Name
	case *ast.FieldAccess:
		y, ok := b.(*ast.FieldAccess)
		return ok && x.Field == y.Field && sameAccess(x.Target, y.Target)
	}
	return false
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

// fdHandleNames are the built-in types that own a descriptor. Only the
// call that opens one makes it, and its close consumes it.
var fdHandleNames = []string{"Reader", "Writer", "Dir", "Socket"}

func builtinHandle(name string) bool {
	return slices.Contains(fdHandleNames, name)
}

func paramSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}
