package checker

import "github.com/jakechampion/lang/internal/ast"

// A literal local is an unannotated `var` whose initialiser is still an
// untyped integer — `var x = 5`, and the `var i = LOW` a range loop desugars
// to. It takes ONE integer type: the first use that fixes a width decides it,
// and it defaults to i32 when no use does (#10123). Every read of the local
// that was typed before its width was known is settled to that width once the
// function body is checked, so the slot, its initialiser and every expression
// over it agree.
type litLocal struct {
	decl  *ast.Var
	scope *scope
	fixed bool
	width ast.NumberType
	// onFix re-stamps a closure capture typed while the local was pending.
	onFix []func(ast.NumberType)
}

// beginLitLocal registers an unannotated local whose type is still
// polymorphic after its initialiser was checked.
func (c *checker) beginLitLocal(decl *ast.Var, s *scope) {
	ll := &litLocal{decl: decl, scope: s}
	if c.litLocalOf == nil {
		c.litLocalOf = map[*ast.Var]*litLocal{}
		c.litIdents = map[*ast.Ident]*litLocal{}
	}
	c.litLocalOf[decl] = ll
	c.litLocals = append(c.litLocals, ll)
}

// noteLitIdent records that `id` read a literal local before its width was
// fixed, so a later settle of the expression holding it fixes the local.
func (c *checker) noteLitIdent(id *ast.Ident, t ast.Type, decl *ast.Var) {
	if decl == nil || c.litLocalOf == nil {
		return
	}
	if nt, ok := t.(ast.NumberType); !ok || !nt.Polymorphic {
		return
	}
	if ll := c.litLocalOf[decl]; ll != nil {
		c.litIdents[id] = ll
	}
}

// restampLitCaptures gives each closure capture of a literal local the width
// that local settles to: the capture list is built from the type the name had
// when the body read it, which may have been before any use fixed it. `s` is
// the scope the closure was defined in.
func (c *checker) restampLitCaptures(caps []ast.Param, s *scope) {
	if c.litLocalOf == nil {
		return
	}
	for i := range caps {
		if nt, ok := caps[i].Type.(ast.NumberType); !ok || !nt.Polymorphic {
			continue
		}
		decl := s.lookupVarDecl(caps[i].Name)
		for j := len(c.captureChain) - 1; decl == nil && j >= 0; j-- {
			if ent := c.captureChain[j]; ent.scope != nil {
				decl = ent.scope.lookupVarDecl(caps[i].Name)
			}
		}
		ll := c.litLocalOf[decl]
		switch {
		case ll == nil:
		case ll.fixed:
			caps[i].Type = ll.width
		default:
			ll.onFix = append(ll.onFix, func(w ast.NumberType) { caps[i].Type = w })
		}
	}
}

// fixLitLocal gives a literal local its one integer type. A second, different
// width for the same local is E003: the local has a single slot, so it cannot
// be read as two types.
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
	ll.decl.Type = w
	c.info.VarTypes[ll.decl] = w
	if ll.scope.vars[ll.decl.Name] == ll.decl {
		ll.scope.names[ll.decl.Name] = w
	}
	c.settleInt(ll.decl.Init, w)
	for _, f := range ll.onFix {
		f(w)
	}
	ll.onFix = nil
}

// settleLitLocals runs after a function body is checked. It propagates each
// fixed width through the expressions that tie literal locals together,
// defaults whatever no use fixed to i32, and settles every read typed while
// its local was pending.
func (c *checker) settleLitLocals(body *ast.Block) {
	if len(c.litLocals) == 0 {
		return
	}
	for {
		c.propagateLitLocals(body)
		var open *litLocal
		for _, ll := range c.litLocals {
			if !ll.fixed {
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
}

// propagateLitLocals settles, to a member's fixed width, every group of
// expressions that must share one integer type, until no new local is fixed.
func (c *checker) propagateLitLocals(body *ast.Block) {
	for {
		before := c.fixedLitLocals()
		ast.Walk(body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Var:
				if ll := c.litLocalOf[x]; ll != nil {
					c.settleLitGroup(ll, x.Init)
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

func (c *checker) fixedLitLocals() int {
	n := 0
	for _, ll := range c.litLocals {
		if ll.fixed {
			n++
		}
	}
	return n
}

// settleLitBinary settles an integer operator whose operands were all still
// untyped when it was checked: an arithmetic tree as a whole, a comparison
// operand by operand, since its bool result reaches no outer context.
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

// settleLitGroup ties a literal local to the expression that must share its
// type — its initialiser, or the value assigned to it.
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

// litGroupWidth returns the width of the first fixed literal local among
// `own` and the untyped integer trees `es`, reporting false when every
// expression is already typed or no local in the group is fixed yet.
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

// litTreeLocals collects the literal locals read inside an untyped integer
// tree — the shape settleInt descends — and reports whether `e` is one.
func (c *checker) litTreeLocals(e ast.Expr, acc []*litLocal) ([]*litLocal, bool) {
	switch x := e.(type) {
	case *ast.NumberLit:
		return acc, x.Width == 0 && !x.IsFloat
	case *ast.Ident:
		if ll := c.litIdents[x]; ll != nil {
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
	}
	return acc, false
}

// holdsPendingLitLocal reports whether an untyped integer tree reads a
// literal local whose width is still open. A cast over one converts the
// local's value; it does not decide the local's type.
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

// settleLitReturns commits an arrow lambda whose inferred result is still an
// untyped integer because it returns a literal local: the signature is fixed
// here, so the local takes its width now — the width it already has, or i32.
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

// settleIndexOperand reads an untyped index or slice bound at i32, the one
// type a position has, so a literal local used as one is an i32.
func (c *checker) settleIndexOperand(e ast.Expr, t ast.Type) ast.Type {
	if nt, ok := t.(ast.NumberType); ok && nt.Polymorphic {
		c.settleInt(e, ast.NumberType{Width: 32, Signed: true})
		return c.postSettleType(e, t)
	}
	return t
}
