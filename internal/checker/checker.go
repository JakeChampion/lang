// Package checker performs name-resolution and type-checking on a Program.
//
// Each function is checked against an environment chain that starts at the
// top-level (functions) and is extended for parameters and `var`
// declarations. Errors are accumulated rather than fatally aborting on the
// first one, so a single run reports as much as possible.
package checker

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
)

type Error struct {
	Pos ast.Position
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("type error at %s: %s", e.Pos, e.Msg) }

// Info captures everything codegen needs that the checker discovered:
// the inferred type of every var without an annotation, and a per-function
// list of locals (so codegen can lay out a frame).
type Info struct {
	VarTypes map[*ast.Var]ast.Type
	Locals   map[*ast.FuncDecl][]*ast.Var
	FuncSigs map[string]*ast.FuncType
}

// Check type-checks the program. It returns an aggregated error if any
// problems were found.
func Check(prog *ast.Program) (*Info, error) {
	c := &checker{
		info: &Info{
			VarTypes: map[*ast.Var]ast.Type{},
			Locals:   map[*ast.FuncDecl][]*ast.Var{},
			FuncSigs: map[string]*ast.FuncType{},
		},
	}

	// Pre-declare built-ins so user code can call them.
	c.info.FuncSigs["putchar"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.VoidType{},
	}

	// First pass: gather all top-level signatures so functions can call
	// each other in any order.
	for _, fn := range prog.Funcs {
		if _, dup := c.info.FuncSigs[fn.Name]; dup {
			c.errf(fn.P, "function %q redeclared", fn.Name)
			continue
		}
		params := make([]ast.Type, len(fn.Params))
		for i, p := range fn.Params {
			params[i] = p.Type
		}
		c.info.FuncSigs[fn.Name] = &ast.FuncType{Params: params, Result: fn.ReturnType}
	}

	// Second pass: check bodies.
	for _, fn := range prog.Funcs {
		c.checkFunction(fn)
	}

	if len(c.errors) > 0 {
		return c.info, c.errors[0]
	}
	return c.info, nil
}

type checker struct {
	info    *Info
	errors  []error
	current *ast.FuncDecl
}

func (c *checker) errf(pos ast.Position, format string, args ...any) {
	c.errors = append(c.errors, &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// scope is an environment of named bindings plus a pointer to its parent.
type scope struct {
	parent *scope
	names  map[string]ast.Type
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, names: map[string]ast.Type{}}
}

func (s *scope) lookup(name string) (ast.Type, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if t, ok := cur.names[name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (c *checker) checkFunction(fn *ast.FuncDecl) {
	c.current = fn
	defer func() { c.current = nil }()

	root := newScope(nil)
	for _, p := range fn.Params {
		if _, dup := root.names[p.Name]; dup {
			c.errf(fn.P, "duplicate parameter %q", p.Name)
		}
		root.names[p.Name] = p.Type
	}
	c.checkBlock(fn.Body, root)
}

func (c *checker) checkBlock(b *ast.Block, parent *scope) {
	s := newScope(parent)
	for _, st := range b.Stmts {
		c.checkStmt(st, s)
	}
}

func (c *checker) checkStmt(st ast.Stmt, s *scope) {
	switch n := st.(type) {
	case *ast.Block:
		c.checkBlock(n, s)
	case *ast.If:
		t := c.checkExpr(n.Cond, s)
		if t != nil && !ast.Equal(t, ast.BoolType{}) {
			c.errf(n.Cond.Pos(), "if condition must be boolean, got %s", t)
		}
		c.checkStmt(n.Then, s)
		if n.Else != nil {
			c.checkStmt(n.Else, s)
		}
	case *ast.While:
		t := c.checkExpr(n.Cond, s)
		if t != nil && !ast.Equal(t, ast.BoolType{}) {
			c.errf(n.Cond.Pos(), "while condition must be boolean, got %s", t)
		}
		c.checkStmt(n.Body, s)
	case *ast.Return:
		want := c.current.ReturnType
		if n.Value == nil {
			if !ast.Equal(want, ast.VoidType{}) {
				c.errf(n.P, "return without value in function returning %s", want)
			}
			return
		}
		got := c.checkExpr(n.Value, s)
		if got != nil && !ast.Equal(got, want) {
			c.errf(n.P, "return type mismatch: function returns %s but expression is %s", want, got)
		}
	case *ast.Var:
		if _, dup := s.names[n.Name]; dup {
			c.errf(n.P, "variable %q already declared in this scope", n.Name)
		}
		got := c.checkExpr(n.Init, s)
		if n.Type == nil {
			if got == nil {
				return
			}
			n.Type = got
		} else if got != nil && !ast.Equal(got, n.Type) {
			c.errf(n.P, "cannot assign %s to variable of type %s", got, n.Type)
		}
		s.names[n.Name] = n.Type
		c.info.VarTypes[n] = n.Type
		c.info.Locals[c.current] = append(c.info.Locals[c.current], n)
	case *ast.ExprStmt:
		c.checkExpr(n.Expr, s)
	}
}

func (c *checker) checkExpr(e ast.Expr, s *scope) ast.Type {
	switch n := e.(type) {
	case *ast.NumberLit:
		return ast.NumberType{}
	case *ast.BoolLit:
		return ast.BoolType{}
	case *ast.Ident:
		if t, ok := s.lookup(n.Name); ok {
			return t
		}
		if sig, ok := c.info.FuncSigs[n.Name]; ok {
			return sig
		}
		c.errf(n.P, "undefined identifier %q", n.Name)
		return nil
	case *ast.ArrayLit:
		if len(n.Elems) == 0 {
			c.errf(n.P, "empty array literal needs a type annotation")
			return nil
		}
		elemT := c.checkExpr(n.Elems[0], s)
		for _, el := range n.Elems[1:] {
			t := c.checkExpr(el, s)
			if t != nil && elemT != nil && !ast.Equal(t, elemT) {
				c.errf(el.Pos(), "array element type %s, expected %s", t, elemT)
			}
		}
		return ast.ArrayType{Elem: elemT}
	case *ast.Index:
		at := c.checkExpr(n.Array, s)
		it := c.checkExpr(n.Idx, s)
		if it != nil && !ast.Equal(it, ast.NumberType{}) {
			c.errf(n.Idx.Pos(), "array index must be number, got %s", it)
		}
		if arr, ok := at.(ast.ArrayType); ok {
			return arr.Elem
		}
		if at != nil {
			c.errf(n.P, "indexing non-array value of type %s", at)
		}
		return nil
	case *ast.Call:
		callee := c.checkExpr(n.Callee, s)
		ft, ok := callee.(*ast.FuncType)
		if !ok {
			if callee != nil {
				c.errf(n.P, "calling non-function value of type %s", callee)
			}
			return nil
		}
		if len(n.Args) != len(ft.Params) {
			c.errf(n.P, "function expects %d arguments, got %d", len(ft.Params), len(n.Args))
		}
		for i, a := range n.Args {
			at := c.checkExpr(a, s)
			if i < len(ft.Params) && at != nil && !ast.Equal(at, ft.Params[i]) {
				c.errf(a.Pos(), "argument %d: expected %s, got %s", i+1, ft.Params[i], at)
			}
		}
		return ft.Result
	case *ast.Binary:
		lt := c.checkExpr(n.Left, s)
		rt := c.checkExpr(n.Right, s)
		switch n.Op {
		case "+", "-", "*", "/":
			c.requireNumber(n.P, lt, n.Op)
			c.requireNumber(n.P, rt, n.Op)
			return ast.NumberType{}
		case "<", ">", "<=", ">=":
			c.requireNumber(n.P, lt, n.Op)
			c.requireNumber(n.P, rt, n.Op)
			return ast.BoolType{}
		case "==", "!=":
			if lt != nil && rt != nil && !ast.Equal(lt, rt) {
				c.errf(n.P, "cannot compare %s and %s", lt, rt)
			}
			return ast.BoolType{}
		case "&&", "||":
			c.requireBool(n.P, lt, n.Op)
			c.requireBool(n.P, rt, n.Op)
			return ast.BoolType{}
		}
		c.errf(n.P, "unknown binary operator %q", n.Op)
		return nil
	case *ast.Unary:
		t := c.checkExpr(n.Operand, s)
		switch n.Op {
		case "-":
			c.requireNumber(n.P, t, n.Op)
			return ast.NumberType{}
		case "!":
			c.requireBool(n.P, t, n.Op)
			return ast.BoolType{}
		}
		return nil
	case *ast.Assign:
		lt := c.checkExpr(n.Target, s)
		rt := c.checkExpr(n.Value, s)
		if lt != nil && rt != nil && !ast.Equal(lt, rt) {
			c.errf(n.P, "cannot assign %s to %s", rt, lt)
		}
		return lt
	}
	return nil
}

func (c *checker) requireNumber(p ast.Position, t ast.Type, op string) {
	if t != nil && !ast.Equal(t, ast.NumberType{}) {
		c.errf(p, "operator %q requires number, got %s", op, t)
	}
}
func (c *checker) requireBool(p ast.Position, t ast.Type, op string) {
	if t != nil && !ast.Equal(t, ast.BoolType{}) {
		c.errf(p, "operator %q requires boolean, got %s", op, t)
	}
}
