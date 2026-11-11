package checker

import (
	"slices"
	"strconv"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

type litLocal struct {
	decl  *ast.Var
	scope *scope
	arg   int
	path  []int
	fixed bool
	width ast.NumberType
	onFix []func(ast.NumberType)
	// float marks a nested float literal; its width is fwidth, and an
	// unfixed one stays polymorphic.
	float  bool
	fwidth ast.FloatType
}

func (c *checker) beginLitLocal(decl *ast.Var, s *scope) {
	ll := &litLocal{decl: decl, scope: s, arg: -1}
	if c.litLocalOf == nil {
		c.litLocalOf = map[*ast.Var]*litLocal{}
		c.litIdents = map[*ast.Ident]*litLocal{}
	}
	c.litLocalOf[decl] = ll
	c.litLocals = append(c.litLocals, ll)
}

func (c *checker) beginLitCompositeLocal(decl *ast.Var, args []ast.Type, s *scope) {
	var lls []*litLocal
	for i, a := range args {
		if nt, ok := a.(ast.NumberType); ok && nt.Polymorphic {
			if lls == nil {
				lls = make([]*litLocal, len(args))
			}
			lls[i] = &litLocal{decl: decl, scope: s, arg: i}
			c.litLocals = append(c.litLocals, lls[i])
		}
	}
	if lls == nil {
		return
	}
	if c.litCompositeOf == nil {
		c.litCompositeOf = map[*ast.Var][]*litLocal{}
		c.litFields = map[*ast.FieldAccess]*litLocal{}
		c.litCompositeReads = map[*ast.Ident]*ast.Var{}
	}
	c.litCompositeOf[decl] = lls
	if et, ok := decl.Type.(ast.EnumType); ok {
		c.defaultWideEnumLiterals(decl, et, lls)
	}
}

// beginLitNestedLocal tracks each polymorphic integer or float inside a tuple
// or an array of an unannotated local's type, by its path through that type.
func (c *checker) beginLitNestedLocal(decl *ast.Var, s *scope) {
	var lls []*litLocal
	var walk func(t ast.Type, path []int, nested bool)
	walk = func(t ast.Type, path []int, nested bool) {
		switch x := t.(type) {
		case ast.NumberType:
			if x.Polymorphic && nested {
				lls = append(lls, &litLocal{decl: decl, scope: s, arg: -1, path: append([]int(nil), path...)})
			}
		case ast.FloatType:
			if x.Polymorphic && nested {
				lls = append(lls, &litLocal{decl: decl, scope: s, arg: -1, path: append([]int(nil), path...), float: true})
			}
		case ast.TupleType:
			for i, el := range x.Elems {
				walk(el, append(path, i), true)
			}
		case ast.ArrayType:
			walk(x.Elem, append(path, 0), true)
		case ast.EnumType:
			for i, a := range x.Args {
				walk(a, append(path, i), nested)
			}
		case ast.StructType:
			for i, a := range x.Args {
				walk(a, append(path, i), nested)
			}
		}
	}
	walk(decl.Type, nil, false)
	if lls == nil {
		return
	}
	if c.litNestedOf == nil {
		c.litNestedOf = map[*ast.Var][]*litLocal{}
	}
	if c.litCompositeReads == nil {
		c.litCompositeReads = map[*ast.Ident]*ast.Var{}
	}
	c.litNestedOf[decl] = lls
	c.litLocals = append(c.litLocals, lls...)
	c.linkLitNestedParts(decl, decl.Init, decl.P)
}

// typeAt is the part of t at path, or nil when t has no such part.
func typeAt(t ast.Type, path []int) ast.Type {
	for _, i := range path {
		switch x := t.(type) {
		case ast.TupleType:
			if i >= len(x.Elems) {
				return nil
			}
			t = x.Elems[i]
		case ast.ArrayType:
			t = x.Elem
		case ast.EnumType:
			if i >= len(x.Args) {
				return nil
			}
			t = x.Args[i]
		case ast.StructType:
			if i >= len(x.Args) {
				return nil
			}
			t = x.Args[i]
		default:
			return nil
		}
	}
	return t
}

// withTypeAt is t with its part at path replaced by w.
func withTypeAt(t ast.Type, path []int, w ast.Type) ast.Type {
	if len(path) == 0 {
		return w
	}
	i := path[0]
	switch x := t.(type) {
	case ast.TupleType:
		elems := append([]ast.Type(nil), x.Elems...)
		elems[i] = withTypeAt(elems[i], path[1:], w)
		return ast.TupleType{Elems: elems}
	case ast.ArrayType:
		x.Elem = withTypeAt(x.Elem, path[1:], w)
		return x
	case ast.EnumType:
		args := append([]ast.Type(nil), x.Args...)
		args[i] = withTypeAt(args[i], path[1:], w)
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.StructType:
		args := append([]ast.Type(nil), x.Args...)
		args[i] = withTypeAt(args[i], path[1:], w)
		return ast.StructType{Name: x.Name, Args: args}
	}
	return t
}

func (c *checker) defaultWideEnumLiterals(decl *ast.Var, et ast.EnumType, lls []*litLocal) {
	construction, resolved := c.info.EnumConstructions[decl.Init]
	call, called := decl.Init.(*ast.Call)
	ed := c.info.Enums[et.Name]
	if !resolved || !called || ed == nil {
		return
	}
	for p, typ := range ed.Variants[construction.VariantIndex].Payloads {
		param, ok := typ.(ast.ParamType)
		if !ok || p >= len(call.Args) {
			continue
		}
		for i, name := range ed.TypeParams {
			if name == param.Name && i < len(lls) && lls[i] != nil {
				if width := c.polymorphicIntDefault(call.Args[p]); width.Width == 64 {
					c.fixLitLocal(lls[i], width, decl.P)
				}
			}
		}
	}
}

func (c *checker) noteLitField(fa *ast.FieldAccess, t ast.Type, s *scope) {
	if c.litCompositeOf == nil {
		return
	}
	if nt, ok := t.(ast.NumberType); !ok || !nt.Polymorphic {
		return
	}
	id, ok := fa.Target.(*ast.Ident)
	if !ok {
		return
	}
	decl := c.lookupLitDecl(id.Name, s)
	lls := c.litCompositeOf[decl]
	if lls == nil {
		return
	}
	st, _ := decl.Type.(ast.StructType)
	sd := c.info.Structs[st.Name]
	if sd == nil {
		return
	}
	for _, f := range sd.Fields {
		pt, ok := f.Type.(ast.ParamType)
		if !ok || f.Name != fa.Field {
			continue
		}
		for i, tp := range sd.TypeParams {
			if tp == pt.Name && i < len(lls) && lls[i] != nil {
				c.litFields[fa] = lls[i]
			}
		}
	}
}

func (c *checker) settleLitCompositeLocal(id *ast.Ident, hint ast.Type) {
	decl := c.litCompositeReads[id]
	if decl != nil {
		c.settleOpenEnumLocal(decl, hint)
	}
	for _, ll := range c.litNestedOf[decl] {
		if ll.fixed {
			continue
		}
		switch nt := typeAt(hint, ll.path).(type) {
		case ast.NumberType:
			if !ll.float && !nt.Polymorphic {
				c.fixLitLocal(ll, nt, id.P)
			}
		case ast.FloatType:
			if ll.float && !nt.Polymorphic {
				c.fixLitFloatLocal(ll, nt)
			}
		}
	}
	name, args := literalNominalArgs(hint)
	if decl == nil || name == "" {
		return
	}
	declName, _ := literalNominalArgs(decl.Type)
	if declName != name {
		return
	}
	for i, ll := range c.litCompositeOf[decl] {
		if ll == nil || ll.fixed || i >= len(args) {
			continue
		}
		if nt, ok := args[i].(ast.NumberType); ok && !nt.Polymorphic {
			c.fixLitLocal(ll, nt, id.P)
		}
	}
}

// settleLitArrayByCall gives a literal array local the element type
// of a typed argument to append or with, as a literal takes the type of the other
// operand of a binary operator.
func (c *checker) settleLitArrayByCall(call *ast.Call, fa *ast.FieldAccess, s *scope) {
	id, ok := fa.Target.(*ast.Ident)
	if !ok {
		return
	}
	decl := s.lookupVarDecl(id.Name)
	if decl == nil || c.litNestedOf[decl] == nil {
		return
	}
	if _, array := decl.Type.(ast.ArrayType); !array {
		return
	}
	at := litArrayStepArg(call, fa)
	if at < 0 {
		return
	}
	c.litCompositeReads[id] = decl
	if c.litArraySteps == nil {
		c.litArraySteps = map[*ast.Call]litArrayStep{}
	}
	c.litArraySteps[call] = litArrayStep{from: decl}
	if t := c.checkExpr(call.Args[at], s); settledNumeric(t) {
		c.settleLitCompositeLocal(id, ast.ArrayType{Elem: t})
	}
}

// litArrayStepArg is the index of the argument that an append or a with call
// puts in its array, or -1 for another call.
func litArrayStepArg(call *ast.Call, fa *ast.FieldAccess) int {
	switch {
	case fa.Field == "append" && len(call.Args) == 1:
		return 0
	case fa.Field == "with" && len(call.Args) == 2:
		return 1
	}
	return -1
}

// litArrayStep is an append or a with call on the literal array local from.
type litArrayStep struct {
	from *ast.Var
}

// linkLitLocals makes two literal locals one group: the width that one of
// them gets is the width of the other.
func (c *checker) linkLitLocals(a, b *litLocal, pos ast.Position) {
	switch {
	case a == b || a.float || b.float:
	case a.fixed:
		c.fixLitLocal(b, a.width, pos)
	case b.fixed:
		c.fixLitLocal(a, b.width, pos)
	default:
		a.onFix = append(a.onFix, func(w ast.NumberType) { c.fixLitLocal(b, w, pos) })
		b.onFix = append(b.onFix, func(w ast.NumberType) { c.fixLitLocal(a, w, pos) })
	}
}

// linkLitNestedParts links each part of the literal array or tuple local r
// with the literal locals that value reads for that part. For an append or a
// with call on a literal array local, the parts are those of that local.
func (c *checker) linkLitNestedParts(r *ast.Var, value ast.Expr, pos ast.Position) {
	link := func(ll *litLocal, e ast.Expr) {
		locals, _ := c.litTreeLocals(e, nil)
		for _, other := range locals {
			c.linkLitLocals(ll, other, pos)
		}
	}
	for _, ll := range c.litNestedOf[r] {
		for _, e := range litInitParts(value, ll.path) {
			link(ll, e)
		}
	}
	call, ok := value.(*ast.Call)
	if !ok {
		return
	}
	step, ok := c.litArraySteps[call]
	if !ok {
		return
	}
	for _, b := range c.litNestedOf[step.from] {
		for _, a := range c.litNestedOf[r] {
			if slices.Equal(a.path, b.path) {
				c.linkLitLocals(a, b, pos)
			}
		}
	}
}

// settleLitArrayByIndex gives a literal array local the element type
// of a typed destination of one of its elements, such as `let m: u8 = z[0];`.
func (c *checker) settleLitArrayByIndex(ix *ast.Index, hint ast.Type) {
	id, ok := ix.Array.(*ast.Ident)
	if !ok || c.litCompositeReads[id] == nil {
		return
	}
	if settledNumeric(hint) {
		c.settleLitCompositeLocal(id, ast.ArrayType{Elem: hint})
	}
}

// settleLitTupleByField gives a literal tuple local the type of a typed
// destination of one of its elements, such as `let y: u8 = t.0;`.
func (c *checker) settleLitTupleByField(fa *ast.FieldAccess, hint ast.Type) {
	id, ok := fa.Target.(*ast.Ident)
	if !ok || !settledNumeric(hint) {
		return
	}
	decl := c.litCompositeReads[id]
	if decl == nil {
		return
	}
	tt, ok := decl.Type.(ast.TupleType)
	i, err := strconv.Atoi(fa.Field)
	if !ok || err != nil || i < 0 || i >= len(tt.Elems) {
		return
	}
	c.settleLitCompositeLocal(id, withTypeAt(decl.Type, []int{i}, hint))
}

// settledNumeric is true for an integer or float type that is not polymorphic.
func settledNumeric(t ast.Type) bool {
	switch x := t.(type) {
	case ast.NumberType:
		return !x.Polymorphic
	case ast.FloatType:
		return !x.Polymorphic
	}
	return false
}

func literalNominalArgs(t ast.Type) (string, []ast.Type) {
	switch t := t.(type) {
	case ast.StructType:
		return t.Name, t.Args
	case ast.EnumType:
		return t.Name, t.Args
	}
	return "", nil
}

func (c *checker) bindEnumLiteralPayloads(tag ast.Expr, ed *ast.EnumDecl, variant *ast.EnumVariant, bindings []string, types []ast.Type, armScope *scope, lls []*litLocal) {
	for p, payload := range variant.Payloads {
		param, ok := payload.(ast.ParamType)
		if !ok || p >= len(bindings) || p >= len(types) || bindings[p] == "" || bindings[p] == "_" {
			continue
		}
		for i, name := range ed.TypeParams {
			if name != param.Name || i >= len(lls) || lls[i] == nil {
				continue
			}
			ll := lls[i]
			decl := &ast.Var{Name: bindings[p], Type: types[p], P: tag.Pos()}
			armScope.bindVar(decl.Name, decl.Type, decl)
			if c.litLocalOf == nil {
				c.litLocalOf = map[*ast.Var]*litLocal{}
				c.litIdents = map[*ast.Ident]*litLocal{}
			}
			c.litLocalOf[decl] = ll
			set := func(w ast.NumberType) {
				decl.Type = w
				types[p] = w
				armScope.set(decl.Name, w)
			}
			if ll.fixed {
				set(ll.width)
			} else {
				ll.onFix = append(ll.onFix, set)
			}
		}
	}
}

func (c *checker) enumLiteralLocals(tag ast.Expr, et ast.EnumType, s *scope) []*litLocal {
	if id, ok := tag.(*ast.Ident); ok {
		return c.litCompositeOf[c.litCompositeReads[id]]
	}
	if _, resolved := c.info.EnumConstructions[tag]; !resolved {
		return nil
	}
	decl := &ast.Var{Type: et, Init: tag, P: tag.Pos()}
	c.beginLitCompositeLocal(decl, et.Args, s)
	return c.litCompositeOf[decl]
}

func (c *checker) litCompositeType(id *ast.Ident) ast.Type {
	if decl := c.litCompositeReads[id]; decl != nil {
		return decl.Type
	}
	return nil
}

func (c *checker) noteLitIdent(id *ast.Ident, t ast.Type, decl *ast.Var) {
	if decl == nil || c.litLocalOf == nil && c.litCompositeOf == nil && c.litNestedOf == nil && c.openEnumLocals == nil {
		return
	}
	if c.litCompositeOf[decl] != nil || c.litNestedOf[decl] != nil || c.openEnumLocals[decl] != nil {
		c.litCompositeReads[id] = decl
		return
	}
	if nt, ok := t.(ast.NumberType); !ok || !nt.Polymorphic {
		return
	}
	if ll := c.litLocalOf[decl]; ll != nil {
		c.litIdents[id] = ll
	}
}

func (c *checker) restampLitCaptures(caps []ast.Param, s *scope) {
	if c.litLocalOf == nil && c.litCompositeOf == nil {
		return
	}
	for i := range caps {
		if _, args := literalNominalArgs(caps[i].Type); args != nil {
			c.restampLitCompositeCapture(&caps[i], args, c.lookupLitDecl(caps[i].Name, s))
			continue
		}
		if nt, ok := caps[i].Type.(ast.NumberType); !ok || !nt.Polymorphic {
			continue
		}
		ll := c.litLocalOf[c.lookupLitDecl(caps[i].Name, s)]
		switch {
		case ll == nil:
		case ll.fixed:
			caps[i].Type = ll.width
		default:
			ll.onFix = append(ll.onFix, func(w ast.NumberType) { caps[i].Type = w })
		}
	}
}

func (c *checker) restampLitCompositeCapture(cp *ast.Param, originalArgs []ast.Type, decl *ast.Var) {
	for k, ll := range c.litCompositeOf[decl] {
		if ll == nil || k >= len(originalArgs) {
			continue
		}
		k := k
		set := func(w ast.NumberType) {
			name, current := literalNominalArgs(cp.Type)
			if name == "" || k >= len(current) {
				return
			}
			args := append([]ast.Type(nil), current...)
			args[k] = w
			if _, ok := cp.Type.(ast.EnumType); ok {
				cp.Type = ast.EnumType{Name: name, Args: args}
			} else {
				cp.Type = ast.StructType{Name: name, Args: args}
			}
		}
		if ll.fixed {
			set(ll.width)
		} else {
			ll.onFix = append(ll.onFix, set)
		}
	}
}

func (c *checker) lookupLitDecl(name string, s *scope) *ast.Var {
	decl := s.lookupVarDecl(name)
	for j := len(c.captureChain) - 1; decl == nil && j >= 0; j-- {
		if ent := c.captureChain[j]; ent.scope != nil {
			decl = ent.scope.lookupVarDecl(name)
		}
	}
	return decl
}

func (c *checker) fixLitLocal(ll *litLocal, w ast.NumberType, pos ast.Position) {
	w = ast.NumberType{Width: w.Width, Signed: w.Signed}
	if ll.fixed {
		if !ast.Equal(ll.width, w) {
			c.errfCode(pos, "E003", "cannot use %q as %s: its type was already inferred as %s", ll.decl.Name, w, ll.width)
		}
		return
	}
	ll.fixed = true
	ll.width = w
	if ll.path != nil {
		c.retypeLitPath(ll, w)
	} else if ll.arg >= 0 {
		if et, ok := ll.decl.Type.(ast.EnumType); ok {
			args := append([]ast.Type(nil), et.Args...)
			args[ll.arg] = w
			nt := ast.EnumType{Name: et.Name, Args: args}
			ll.decl.Type = nt
			c.info.VarTypes[ll.decl] = nt
			if ll.scope.vars[ll.decl.Name] == ll.decl {
				ll.scope.set(ll.decl.Name, nt)
			}
			c.settleNumeric(ll.decl.Init, nt)
		} else {
			c.fixLitStructArg(ll, w)
		}
	} else {
		ll.decl.Type = w
		c.info.VarTypes[ll.decl] = w
		if ll.scope.vars[ll.decl.Name] == ll.decl {
			ll.scope.set(ll.decl.Name, w)
		}
		c.settleInt(ll.decl.Init, w)
	}
	for _, f := range ll.onFix {
		f(w)
	}
	ll.onFix = nil
}

// fixLitFloatLocal gives a nested float literal of a local the width w.
func (c *checker) fixLitFloatLocal(ll *litLocal, w ast.FloatType) {
	ll.fixed = true
	ll.fwidth = ast.FloatType{Width: w.Width}
	c.retypeLitPath(ll, ll.fwidth)
}

// retypeLitPath sets the part at ll.path of the local's type to w.
func (c *checker) retypeLitPath(ll *litLocal, w ast.Type) {
	nt := withTypeAt(ll.decl.Type, ll.path, w)
	ll.decl.Type = nt
	c.info.VarTypes[ll.decl] = nt
	if ll.scope.vars[ll.decl.Name] == ll.decl {
		ll.scope.set(ll.decl.Name, nt)
	}
	c.settleNumeric(ll.decl.Init, nt)
}

func (c *checker) fixLitStructArg(ll *litLocal, w ast.NumberType) {
	st := ll.decl.Type.(ast.StructType)
	args := append([]ast.Type(nil), st.Args...)
	args[ll.arg] = w
	nt := ast.StructType{Name: st.Name, Args: args}
	ll.decl.Type = nt
	c.info.VarTypes[ll.decl] = nt
	if ll.scope.vars[ll.decl.Name] == ll.decl {
		ll.scope.set(ll.decl.Name, nt)
	}
	sl, ok := ll.decl.Init.(*ast.StructLit)
	sd := c.info.Structs[st.Name]
	if !ok || sd == nil || ll.arg >= len(sd.TypeParams) {
		return
	}
	sl.TypeArgs = append([]ast.Type(nil), args...)
	for _, e := range c.structParamExprs(sd, sd.TypeParams[ll.arg], []ast.Expr{sl}) {
		c.settleInt(e, w)
	}
}

func (c *checker) settleLitLocals(body *ast.Block) {
	if len(c.litLocals) == 0 {
		return
	}
	for {
		c.propagateLitLocals(body)
		var open *litLocal
		for _, ll := range c.litLocals {
			if !ll.fixed && !ll.float {
				open = ll
				break
			}
		}
		if open == nil {
			break
		}
		c.fixLitLocal(open, ast.NumberType{Width: 32, Signed: true}, open.decl.P)
	}
	c.propagateLitLocals(body)
	c.litLocals = nil
	c.litLocalOf = nil
	c.litIdents = nil
	c.litCompositeOf = nil
	c.litNestedOf = nil
	c.litFields = nil
	c.litCompositeReads = nil
	c.litRecvCalls = nil
	c.litArraySteps = nil
}

func (c *checker) propagateLitLocals(body *ast.Block) {
	for {
		before := c.fixedLitLocals()
		ast.Walk(body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Var:
				if ll := c.litLocalOf[x]; ll != nil {
					c.settleLitGroup(ll, x.Init)
				}
				if id, ok := x.Init.(*ast.Ident); ok {
					c.linkLitCompositeCopy(x, c.litCompositeReads[id], id.P)
				}
			case *ast.Assign:
				if id, ok := x.Target.(*ast.Ident); ok {
					if ll := c.litIdents[id]; ll != nil {
						c.settleLitGroup(ll, x.Value)
					}
				}
			case *ast.Binary:
				c.settleLitBinary(x)
			case *ast.CastExpr:
				if nt, ok := x.InnerType.(ast.NumberType); ok && nt.Polymorphic {
					if w, ok := c.litGroupWidth(nil, x.Inner); ok {
						c.settleInt(x.Inner, w)
						x.InnerType = w
					}
				}
			}
			return true
		})
		if c.fixedLitLocals() == before {
			return
		}
	}
}

// litInitParts is the expressions inside an array or tuple literal that give
// the part of its type at path.
func litInitParts(e ast.Expr, path []int) []ast.Expr {
	if len(path) == 0 {
		return []ast.Expr{e}
	}
	switch x := e.(type) {
	case *ast.ArrayLit:
		var out []ast.Expr
		for _, el := range x.Elems {
			out = append(out, litInitParts(el, path[1:])...)
		}
		return out
	case *ast.TupleLit:
		if path[0] < len(x.Elems) {
			return litInitParts(x.Elems[path[0]], path[1:])
		}
	}
	return nil
}

func (c *checker) linkLitCompositeCopy(r *ast.Var, q *ast.Var, pos ast.Position) {
	rs, qs := c.litCompositeOf[r], c.litCompositeOf[q]
	for i := 0; i < len(rs) && i < len(qs); i++ {
		a, b := rs[i], qs[i]
		switch {
		case a == nil || b == nil || !a.fixed && !b.fixed:
		case a.fixed:
			c.fixLitLocal(b, a.width, pos)
		default:
			c.fixLitLocal(a, b.width, pos)
		}
	}
}

func (c *checker) fixedLitLocals() int {
	n := 0
	for _, ll := range c.litLocals {
		if ll.fixed {
			n++
		}
	}
	return n
}

func (c *checker) settleLitBinary(b *ast.Binary) {
	if b.IntWidth != 0 || b.IsFloat || b.IsStringConcat || b.IsStringCmp || b.IsStringOrd ||
		b.EqCall != nil || b.CmpCall != nil || b.ArithCall != nil {
		return
	}
	switch b.Op {
	case "<", ">", "<=", ">=", "==", "!=":
		w, ok := c.litGroupWidth(nil, b.Left, b.Right)
		if !ok {
			return
		}
		c.settleInt(b.Left, w)
		c.settleInt(b.Right, w)
		b.IntWidth = w.NormalWidth()
		b.IsUnsigned = !w.IsSigned()
	default:
		if w, ok := c.litGroupWidth(nil, b); ok {
			c.settleInt(b, w)
		}
	}
}

func (c *checker) settleLitGroup(ll *litLocal, e ast.Expr) {
	w, ok := c.litGroupWidth(ll, e)
	if !ok {
		return
	}
	if !ll.fixed {
		c.fixLitLocal(ll, w, ll.decl.P)
	}
	c.settleInt(e, w)
}

func (c *checker) litGroupWidth(own *litLocal, es ...ast.Expr) (ast.NumberType, bool) {
	var locals []*litLocal
	if own != nil {
		locals = append(locals, own)
	}
	unsettled := false
	for _, e := range es {
		var ok bool
		locals, ok = c.litTreeLocals(e, locals)
		unsettled = unsettled || ok
	}
	if !unsettled {
		return ast.NumberType{}, false
	}
	for _, ll := range locals {
		if ll.fixed {
			return ll.width, true
		}
	}
	return ast.NumberType{}, false
}

func (c *checker) litTreeLocals(e ast.Expr, acc []*litLocal) ([]*litLocal, bool) {
	switch x := e.(type) {
	case *ast.NumberLit:
		return acc, x.Width == 0 && !x.IsFloat
	case *ast.Ident:
		if ll := c.litIdents[x]; ll != nil {
			return append(acc, ll), true
		}
	case *ast.FieldAccess:
		if ll := c.litFields[x]; ll != nil {
			return append(acc, ll), true
		}
	case *ast.Unary:
		if x.Op == "-" || x.Op == "+" {
			return c.litTreeLocals(x.Operand, acc)
		}
	case *ast.Binary:
		if x.IntWidth != 0 || x.IsFloat || x.FloatWidth != 0 || x.IsStringConcat || x.ArithCall != nil {
			return acc, false
		}
		switch x.Op {
		case "+", "-", "*", "/", "%", "&", "|", "^", "<<", ">>", "+|", "-|", "*|", "<<|":
			acc, l := c.litTreeLocals(x.Left, acc)
			acc, r := c.litTreeLocals(x.Right, acc)
			return acc, l && r
		}
	case *ast.IfExpr:
		if x.Then == nil || x.Else == nil {
			return acc, false
		}
		acc, t := c.litTreeLocals(x.Then, acc)
		acc, f := c.litTreeLocals(x.Else, acc)
		return acc, t && f
	case *ast.BlockExpr:
		if x.Tail != nil {
			return c.litTreeLocals(x.Tail, acc)
		}
	case *ast.Call:
		if _, open := c.litRecvCalls[x]; open {
			return c.litTreeLocals(x.Args[0], acc)
		}
	}
	return acc, false
}

func (c *checker) holdsPendingLitLocal(e ast.Expr) bool {
	if c.litIdents == nil {
		return false
	}
	locals, ok := c.litTreeLocals(e, nil)
	if !ok {
		return false
	}
	for _, ll := range locals {
		if !ll.fixed {
			return true
		}
	}
	return false
}

func (c *checker) settleLitReturns(fn *ast.FuncDecl) {
	if nt, ok := fn.ReturnType.(ast.NumberType); !ok || !nt.Polymorphic || c.litIdents == nil {
		return
	}
	var vals []ast.Expr
	var locals []*litLocal
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Lambda, *ast.FuncDecl:
			return false
		case *ast.Return:
			if x.Value != nil {
				var ok bool
				if locals, ok = c.litTreeLocals(x.Value, locals); ok {
					vals = append(vals, x.Value)
				}
			}
		}
		return true
	})
	if len(locals) == 0 {
		return
	}
	w := ast.NumberType{Width: 32, Signed: true}
	for _, ll := range locals {
		if ll.fixed {
			w = ll.width
			break
		}
	}
	for _, v := range vals {
		c.settleInt(v, w)
	}
	fn.ReturnType = w
	if sig := c.info.FuncSigs[fn.Name]; sig != nil {
		sig.Result = w
	}
}

func (c *checker) settleIndexOperand(e ast.Expr, t ast.Type) ast.Type {
	if nt, ok := t.(ast.NumberType); ok && nt.Polymorphic {
		c.settleInt(e, ast.NumberType{Width: 32, Signed: true})
		return c.postSettleType(e, t)
	}
	return t
}

// deferLitReceiver keeps an open literal receiver open: the method takes
// the width the receiver settles to, from any use in the function.
func (c *checker) deferLitReceiver(call *ast.Call, recv ast.Expr, t ast.Type, prefer []string) *litLocal {
	id, ok := recv.(*ast.Ident)
	if !ok {
		return nil
	}
	ll := c.litIdents[id]
	if nt, isNum := t.(ast.NumberType); ll == nil || ll.fixed || !isNum || !nt.Polymorphic {
		return nil
	}
	if c.litRecvCalls == nil {
		c.litRecvCalls = map[*ast.Call][]int{}
	}
	c.litRecvCalls[call] = nil
	field := call.Method.Field
	ll.onFix = append(ll.onFix, func(w ast.NumberType) {
		name, _ := methodTypeName(w)
		callee := call.Callee.(*ast.Ident)
		if mangled, owner, _, found := c.resolveMethodFrom(name, field, prefer); found {
			callee.Name = c.instForReceiver(name, field, owner, mangled, w)
		} else if builtin, found := builtinScalarMethod(name, field); found {
			callee.Name = builtin
		} else {
			c.errfCode(call.Method.FieldPos, "E043", "%s", c.unknownMethodMessage(name, field, w))
			return
		}
		call.Method.Receiver = w
		ft := c.info.FuncSigs[callee.Name]
		for _, i := range c.litRecvCalls[call] {
			if ft != nil && i < len(ft.Params) {
				c.settleNumeric(call.Args[i], ft.Params[i])
			}
		}
	})
	return ll
}
