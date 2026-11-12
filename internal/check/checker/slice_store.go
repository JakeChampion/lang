package checker

import (
	"github.com/jakechampion/lang/internal/syntax/ast"
)

const sliceStoreMsg = "storing a `[T]` slice in %q that views the storage of an inner block: the backing array is reclaimed when that block ends, leaving a dangling view — store an owned array (`T[]`), or declare the storage in the block of %q"

// containerCallParts gives part the receiver and each argument of a builtin
// method of an array or a map, such as `append` or `insert`. The result of
// the method can hold each view that the receiver or an argument holds.
func (c *checker) containerCallParts(e *ast.Call, t ast.Type, env *escapeEnv, part func(ast.Expr, ast.Type)) bool {
	// The checker names a builtin method by an identifier and puts the
	// receiver with the arguments.
	args := e.Args
	switch callee := e.Callee.(type) {
	case *ast.FieldAccess:
		args = append([]ast.Expr{callee.Target}, args...)
	case *ast.Ident:
		if c.declaredFuncs[callee.Name] {
			return false
		}
	default:
		return false
	}
	var elem ast.Type
	switch v := t.(type) {
	case ast.ArrayType:
		elem = v.Elem
	case ast.StructType:
		if v.Name == "Map" && len(v.Args) == 2 {
			elem = v.Args[1]
		}
	}
	if elem == nil {
		return false
	}
	if len(args) == 0 {
		return true
	}
	part(args[0], t)
	for _, arg := range args[1:] {
		if at := c.projType(arg, env); at != nil {
			part(arg, at)
			continue
		}
		part(arg, elem)
		part(arg, t)
	}
	return true
}

// checkSliceStores refuses a view that is stored in a variable of an outer
// block when it views the storage of an inner block: that storage ends with
// its block. compiler/checker.fern's slc_store_diags holds the same rule.
func (c *checker) checkSliceStores(fn *ast.FuncDecl, env *escapeEnv, sums map[string]viewSummary) {
	if fn.Body == nil {
		return
	}
	var blocks []*ast.Block
	var assigns []*ast.Assign
	slices := false
	ast.Walk(fn.Body, func(n ast.Node) bool {
		switch m := n.(type) {
		case *ast.Block:
			blocks = append(blocks, m)
		case *ast.Assign:
			assigns = append(assigns, m)
		case *ast.SliceExpr:
			slices = true
		}
		return true
	})
	if !slices || len(assigns) == 0 {
		return
	}
	// Blocks come outermost first, so the innermost block of a node is the
	// last one that reaches it.
	parent := map[*ast.Block]*ast.Block{}
	home := map[ast.Node]*ast.Block{}
	for _, b := range blocks {
		ast.Walk(b, func(n ast.Node) bool {
			switch m := n.(type) {
			case *ast.Block:
				if m != b {
					parent[m] = b
				}
			case *ast.Assign, *ast.Var:
				home[m] = b
			}
			return true
		})
	}
	within := func(b, outer *ast.Block) bool {
		for ; b != nil; b = parent[b] {
			if b == outer {
				return true
			}
		}
		return false
	}
	// visible is the declaration of name that a statement in block at sees.
	visible := func(name string, at *ast.Block) *ast.Var {
		var best *ast.Var
		for _, v := range c.info.Locals[fn] {
			vb := home[v]
			if v.Name != name || vb == nil || !within(at, vb) {
				continue
			}
			if best == nil || within(vb, home[best]) {
				best = v
			}
		}
		return best
	}
	for _, a := range assigns {
		id, ok := a.Target.(*ast.Ident)
		at := home[a]
		if !ok || at == nil {
			continue
		}
		var to *ast.Block
		var tt ast.Type
		if v := visible(id.Name, at); v != nil {
			to, tt = home[v], v.Type
		} else if i, isParam := env.params[id.Name]; isParam {
			to, tt = fn.Body, env.paramTypes[i]
		} else {
			continue
		}
		if to == at || tt == nil {
			continue
		}
		if _, isSlice := tt.(ast.SliceType); !isSlice && !c.holdsViewAggregate(tt) {
			continue
		}
		inner := *env
		inner.locals = map[string]*ast.Var{}
		inner.visiting = map[string]bool{}
		for _, v := range c.info.Locals[fn] {
			vb := home[v]
			if vb == nil || vb == to || !within(vb, to) || !within(at, vb) {
				continue
			}
			if seen := inner.locals[v.Name]; seen == nil || within(vb, home[seen]) {
				inner.locals[v.Name] = v
			}
		}
		if c.aggregateViewRoots(a.Value, tt, &inner, sums, c.sliceLeaf(&inner, sums)).local {
			c.errfCode(a.P, "E063", sliceStoreMsg, id.Name, id.Name)
		}
	}
}
