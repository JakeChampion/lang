// Package closureconv hoists local function declarations to the
// top-level Funcs list and rewrites their bodies so the codegen
// backend sees a flat program of top-level functions only.
//
// Each hoisted function gets a generated unique name and an extra
// trailing parameter `__env: number` (an i32 pointer to a heap-
// allocated env block). References to captured outer-scope
// variables are rewritten from *ast.Ident to *ast.CaptureRef, which
// codegen lowers as loads from the env parameter at the recorded
// offset. At the original def site, the *ast.FuncDecl statement is
// replaced with `var name = makeClosure(funcIndex, [captures])`,
// where the synthetic *ast.MakeClosure node tells codegen to
// allocate the env, populate it with current capture values, and
// build the 8-byte closure pair.
package closureconv

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// Convert mutates prog in place: every IsLocal *ast.FuncDecl in any
// function body is hoisted to prog.Funcs (with a renamed identifier
// and a synthetic env parameter), captures inside its body are
// rewritten as *ast.CaptureRef nodes, and the original statement is
// replaced with `var <orig-name> = MakeClosure{...}`.
func Convert(prog *ast.Program, info *checker.Info) error {
	c := &converter{info: info, hoisted: map[string]int{}, funcIdx: map[string]int{}}
	// Original top-level functions occupy table indices 0..N-1; track
	// them so the synthetic env-call signature uses stable indices and
	// MakeClosure nodes can name them by table position.
	for i, fn := range prog.Funcs {
		c.funcIdx[fn.Name] = i
	}
	// Walk every top-level body and rewrite inner FuncDecl statements.
	for _, fn := range prog.Funcs {
		c.outerFn = fn
		if err := c.rewriteBlock(fn.Body, nil); err != nil {
			return err
		}
	}
	c.outerFn = nil
	// Append hoisted functions after the rewrite finishes so the
	// indices we stamped into MakeClosure nodes match prog.Funcs.
	prog.Funcs = append(prog.Funcs, c.appended...)
	return nil
}

type converter struct {
	info *checker.Info
	// hoisted gives each hoisted function its globally-unique name,
	// generated from a counter to avoid clashing with user names.
	hoisted map[string]int
	// funcIdx maps a top-level function name to its position in
	// prog.Funcs (i.e. its funcref-table index).
	funcIdx  map[string]int
	appended []*ast.FuncDecl
	// outerFn is the top-level FuncDecl whose body we're currently
	// rewriting. Var statements introduced for `MakeClosure` are
	// recorded under it in info.Locals so the codegen pass declares
	// the right locals.
	outerFn *ast.FuncDecl
}

func (c *converter) freshName(orig string) string {
	c.hoisted[orig]++
	return fmt.Sprintf("__closure_%s_%d", orig, len(c.hoisted))
}

// rewriteBlock walks a block and replaces inner FuncDecl statements
// with closure-creation Vars. The hoistedFor parameter is non-nil
// only when we're walking a hoisted function's own body; in that
// case captured-name idents inside expressions are replaced with
// CaptureRef nodes.
func (c *converter) rewriteBlock(b *ast.Block, hoistedFor *captureCtx) error {
	if b == nil {
		return nil
	}
	for i, s := range b.Stmts {
		ns, err := c.rewriteStmt(s, hoistedFor)
		if err != nil {
			return err
		}
		b.Stmts[i] = ns
	}
	return nil
}

// captureCtx tracks the captures in scope while rewriting a hoisted
// function's body. byName maps each capture's original name to the
// byte offset (4 * index) inside the env block.
type captureCtx struct {
	byName  map[string]capInfo
	envName string // always "$__env" but kept here for readability
}

type capInfo struct {
	offset int
	typ    ast.Type
}

func (c *converter) rewriteStmt(s ast.Stmt, ctx *captureCtx) (ast.Stmt, error) {
	switch n := s.(type) {
	case *ast.FuncDecl:
		if !n.IsLocal {
			return n, nil
		}
		return c.hoist(n, ctx)
	case *ast.Block:
		return n, c.rewriteBlock(n, ctx)
	case *ast.If:
		nc, err := c.rewriteExpr(n.Cond, ctx)
		if err != nil {
			return nil, err
		}
		n.Cond = nc
		ns, err := c.rewriteStmt(n.Then, ctx)
		if err != nil {
			return nil, err
		}
		n.Then = ns
		if n.Else != nil {
			ns, err := c.rewriteStmt(n.Else, ctx)
			if err != nil {
				return nil, err
			}
			n.Else = ns
		}
		return n, nil
	case *ast.While:
		nc, err := c.rewriteExpr(n.Cond, ctx)
		if err != nil {
			return nil, err
		}
		n.Cond = nc
		ns, err := c.rewriteStmt(n.Body, ctx)
		if err != nil {
			return nil, err
		}
		n.Body = ns
		return n, nil
	case *ast.For:
		if n.Init != nil {
			ns, err := c.rewriteStmt(n.Init, ctx)
			if err != nil {
				return nil, err
			}
			n.Init = ns
		}
		nc, err := c.rewriteExpr(n.Cond, ctx)
		if err != nil {
			return nil, err
		}
		n.Cond = nc
		if n.Step != nil {
			ns, err := c.rewriteStmt(n.Step, ctx)
			if err != nil {
				return nil, err
			}
			n.Step = ns
		}
		nb, err := c.rewriteStmt(n.Body, ctx)
		if err != nil {
			return nil, err
		}
		n.Body = nb
		return n, nil
	case *ast.Return:
		if n.Value != nil {
			nv, err := c.rewriteExpr(n.Value, ctx)
			if err != nil {
				return nil, err
			}
			n.Value = nv
		}
		return n, nil
	case *ast.Var:
		nv, err := c.rewriteExpr(n.Init, ctx)
		if err != nil {
			return nil, err
		}
		n.Init = nv
		return n, nil
	case *ast.ExprStmt:
		ne, err := c.rewriteExpr(n.Expr, ctx)
		if err != nil {
			return nil, err
		}
		n.Expr = ne
		return n, nil
	case *ast.Switch:
		nt, err := c.rewriteExpr(n.Tag, ctx)
		if err != nil {
			return nil, err
		}
		n.Tag = nt
		for _, k := range n.Cases {
			for i, v := range k.Values {
				nv, err := c.rewriteExpr(v, ctx)
				if err != nil {
					return nil, err
				}
				k.Values[i] = nv
			}
			if err := c.rewriteBlock(k.Body, ctx); err != nil {
				return nil, err
			}
		}
		if n.Default != nil {
			if err := c.rewriteBlock(n.Default, ctx); err != nil {
				return nil, err
			}
		}
		return n, nil
	}
	return s, nil
}

func (c *converter) rewriteExpr(e ast.Expr, ctx *captureCtx) (ast.Expr, error) {
	switch n := e.(type) {
	case *ast.Ident:
		if ctx != nil {
			if ci, ok := ctx.byName[n.Name]; ok {
				return &ast.CaptureRef{P: n.P, Name: n.Name, Offset: ci.offset, Type: ci.typ}, nil
			}
		}
		return n, nil
	case *ast.Binary:
		nl, err := c.rewriteExpr(n.Left, ctx)
		if err != nil {
			return nil, err
		}
		n.Left = nl
		nr, err := c.rewriteExpr(n.Right, ctx)
		if err != nil {
			return nil, err
		}
		n.Right = nr
		return n, nil
	case *ast.Unary:
		nv, err := c.rewriteExpr(n.Operand, ctx)
		if err != nil {
			return nil, err
		}
		n.Operand = nv
		return n, nil
	case *ast.Call:
		nc, err := c.rewriteExpr(n.Callee, ctx)
		if err != nil {
			return nil, err
		}
		n.Callee = nc
		for i, a := range n.Args {
			na, err := c.rewriteExpr(a, ctx)
			if err != nil {
				return nil, err
			}
			n.Args[i] = na
		}
		return n, nil
	case *ast.Index:
		na, err := c.rewriteExpr(n.Array, ctx)
		if err != nil {
			return nil, err
		}
		n.Array = na
		ni, err := c.rewriteExpr(n.Idx, ctx)
		if err != nil {
			return nil, err
		}
		n.Idx = ni
		return n, nil
	case *ast.ArrayLit:
		for i, el := range n.Elems {
			ne, err := c.rewriteExpr(el, ctx)
			if err != nil {
				return nil, err
			}
			n.Elems[i] = ne
		}
		return n, nil
	case *ast.Assign:
		nt, err := c.rewriteExpr(n.Target, ctx)
		if err != nil {
			return nil, err
		}
		n.Target = nt
		nv, err := c.rewriteExpr(n.Value, ctx)
		if err != nil {
			return nil, err
		}
		n.Value = nv
		return n, nil
	case *ast.Ternary:
		nc, err := c.rewriteExpr(n.Cond, ctx)
		if err != nil {
			return nil, err
		}
		n.Cond = nc
		nt, err := c.rewriteExpr(n.Then, ctx)
		if err != nil {
			return nil, err
		}
		n.Then = nt
		ne, err := c.rewriteExpr(n.Else, ctx)
		if err != nil {
			return nil, err
		}
		n.Else = ne
		return n, nil
	case *ast.StructLit:
		for i, f := range n.Fields {
			nv, err := c.rewriteExpr(f.Value, ctx)
			if err != nil {
				return nil, err
			}
			n.Fields[i].Value = nv
		}
		return n, nil
	case *ast.FieldAccess:
		nt, err := c.rewriteExpr(n.Target, ctx)
		if err != nil {
			return nil, err
		}
		n.Target = nt
		return n, nil
	}
	return e, nil
}

// hoist takes a local FuncDecl, gives it a unique top-level name,
// adds a synthetic `__env: number` parameter, rewrites its body's
// capture references, appends it to the converter's hoisted list,
// and returns the replacement Var statement that creates a closure
// at the original def site.
func (c *converter) hoist(fn *ast.FuncDecl, parentCtx *captureCtx) (ast.Stmt, error) {
	origName := fn.Name
	hoistedName := c.freshName(origName)

	// Build the capture context for the hoisted body. Captures that
	// are themselves captures of the enclosing function need their
	// values forwarded to the new env, but the body still reads them
	// through its own env — the offset is just whatever index we
	// assign here.
	ctx := &captureCtx{byName: map[string]capInfo{}, envName: "$__env"}
	for i, cap := range fn.Captures {
		ctx.byName[cap.Name] = capInfo{offset: i * 4, typ: cap.Type}
	}

	// Add the synthetic env parameter as the function's last param.
	fn.Params = append(fn.Params, ast.Param{Name: "__env", Type: ast.NumberType{}})
	// Rename the function so the top-level table can host it without
	// colliding with user names.
	fn.Name = hoistedName
	// Update FuncSigs: the original local-name signature stays so the
	// outer body's calls keep type-checking; add a hoisted-name entry
	// (with the env param) so codegen can look up the indirect-call
	// signature.
	hoistedSig := &ast.FuncType{Result: fn.ReturnType}
	for _, p := range fn.Params {
		hoistedSig.Params = append(hoistedSig.Params, p.Type)
	}
	c.info.FuncSigs[hoistedName] = hoistedSig

	// Rewrite the body's captured-name references and any nested
	// closures.
	if err := c.rewriteBlock(fn.Body, ctx); err != nil {
		return nil, err
	}

	// Reserve the hoisted function's table index now (after existing
	// top-level funcs and any earlier hoisted ones).
	idx := len(c.funcIdx) + len(c.appended)
	c.funcIdx[hoistedName] = idx
	c.appended = append(c.appended, fn)

	// Build the MakeClosure node. Each capture is the current value
	// of the named outer variable, rewritten through parentCtx so
	// that nested closures forward outer captures correctly.
	caps := make([]ast.Expr, len(fn.Captures))
	for i, cap := range fn.Captures {
		var src ast.Expr = &ast.Ident{P: fn.P, Name: cap.Name}
		if rewritten, err := c.rewriteExpr(src, parentCtx); err == nil {
			src = rewritten
		}
		caps[i] = src
	}
	mc := &ast.MakeClosure{
		P:         fn.P,
		FuncName:  hoistedName,
		FuncIndex: idx,
		Captures:  caps,
	}

	// The original local-name FuncSig describes the user-visible
	// signature without the env param; that's what calls bind
	// against. Indirect-call codegen knows to add the env arg.
	userSig := &ast.FuncType{Result: fn.ReturnType}
	for _, p := range fn.Params[:len(fn.Params)-1] { // drop trailing __env
		userSig.Params = append(userSig.Params, p.Type)
	}

	v := &ast.Var{
		P:    fn.P,
		Name: origName,
		Type: userSig,
		Init: mc,
	}
	// Register the new Var with the checker's per-function locals so
	// codegen declares a slot for it. The current outerFn is the
	// nearest top-level function; nested closures within nested
	// closures still attach to their containing top-level entry.
	if c.outerFn != nil {
		c.info.VarTypes[v] = userSig
		c.info.Locals[c.outerFn] = append(c.info.Locals[c.outerFn], v)
	}
	return v, nil
}
