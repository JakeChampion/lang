// Package interp is a small tree-walking interpreter for the lang AST.
//
// It's used by the REPL (cmd/lang -repl) and by tests; production
// builds still go through the ARM32 / WASM code generators.
//
// Control flow inside a function uses a flow-tagged result value
// rather than panics: each statement returns a stmtResult whose Flow
// field tells the surrounding loop / block whether to keep going,
// break, continue, or unwind to the enclosing call site.
package interp

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// Value is the runtime tagged-union of every type the language
// evaluates to. Concrete kinds: Number, Bool, String, Void, Array,
// Func (a user-defined function reference), and Builtin.
type Value interface {
	String() string
}

type Number int64
type Bool bool
type String string
type Void struct{}
type Array []Value
type Func struct{ Decl *ast.FuncDecl }

// Builtin is a host-provided function callable from interpreted code.
// It receives evaluated arguments and may emit output via the
// interpreter's stdout.
type Builtin struct {
	Fn func(*Interp, []Value) (Value, error)
}

func (n Number) String() string  { return fmt.Sprintf("%d", int64(n)) }
func (b Bool) String() string {
	if b {
		return "true"
	}
	return "false"
}
func (s String) String() string  { return string(s) }
func (Void) String() string      { return "" }
func (a Array) String() string {
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range a {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(v.String())
	}
	b.WriteByte(']')
	return b.String()
}
func (f Func) String() string    { return "function " + f.Decl.Name }
func (Builtin) String() string   { return "<builtin>" }

// Interp owns global state: top-level user functions, host built-ins,
// the persistent REPL environment, and the writer used for `print` /
// `putchar` output.
type Interp struct {
	Funcs    map[string]*ast.FuncDecl
	Builtins map[string]*Builtin
	Stdout   io.Writer
	// Global is the env used by REPL-typed top-level statements;
	// `var x = 7` at the prompt declares x here so the next prompt
	// can read it.
	Global *env
}

func New() *Interp {
	i := &Interp{
		Funcs:    map[string]*ast.FuncDecl{},
		Builtins: map[string]*Builtin{},
		Stdout:   os.Stdout,
		Global:   newEnv(nil),
	}
	i.Builtins["print"] = &Builtin{Fn: builtinPrint}
	i.Builtins["putchar"] = &Builtin{Fn: builtinPutchar}
	return i
}

func builtinPrint(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("print: expected 1 arg, got %d", len(args))
	}
	s, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("print: expected string arg, got %T", args[0])
	}
	fmt.Fprintln(i.Stdout, string(s))
	return Void{}, nil
}

func builtinPutchar(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("putchar: expected 1 arg, got %d", len(args))
	}
	n, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("putchar: expected number arg, got %T", args[0])
	}
	fmt.Fprintf(i.Stdout, "%c", rune(int64(n)))
	return Void{}, nil
}

// Register adds a user-defined function to the interpreter. Subsequent
// declarations of the same name overwrite the previous one (handy for
// REPL redefinitions).
func (i *Interp) Register(fn *ast.FuncDecl) { i.Funcs[fn.Name] = fn }

// CallByName looks up a user function and invokes it with the given
// arguments.
func (i *Interp) CallByName(name string, args []Value) (Value, error) {
	if fn, ok := i.Funcs[name]; ok {
		return i.callFunc(fn, args)
	}
	if b, ok := i.Builtins[name]; ok {
		return b.Fn(i, args)
	}
	return nil, fmt.Errorf("undefined function %q", name)
}

// ---------- evaluation core ----------

type env struct {
	parent *env
	vars   map[string]Value
}

func newEnv(parent *env) *env { return &env{parent: parent, vars: map[string]Value{}} }

func (e *env) get(name string) (Value, bool) {
	for cur := e; cur != nil; cur = cur.parent {
		if v, ok := cur.vars[name]; ok {
			return v, true
		}
	}
	return nil, false
}

// set assigns to the nearest existing binding, or creates one in the
// innermost scope if none exists.
func (e *env) set(name string, v Value) {
	for cur := e; cur != nil; cur = cur.parent {
		if _, ok := cur.vars[name]; ok {
			cur.vars[name] = v
			return
		}
	}
	e.vars[name] = v
}

// declare always binds in the innermost scope (for `var` decls).
func (e *env) declare(name string, v Value) { e.vars[name] = v }

type flowKind int

const (
	flowNormal flowKind = iota
	flowReturn
	flowBreak
	flowContinue
)

type result struct {
	flow flowKind
	val  Value
}

func (i *Interp) callFunc(fn *ast.FuncDecl, args []Value) (Value, error) {
	if len(args) != len(fn.Params) {
		return nil, fmt.Errorf("%s: expected %d args, got %d", fn.Name, len(fn.Params), len(args))
	}
	e := newEnv(nil)
	for k, p := range fn.Params {
		e.declare(p.Name, args[k])
	}
	r, err := i.execBlock(fn.Body, e)
	if err != nil {
		return nil, err
	}
	if r.flow == flowReturn {
		return r.val, nil
	}
	return Void{}, nil
}

func (i *Interp) execBlock(b *ast.Block, parent *env) (result, error) {
	e := newEnv(parent)
	for _, s := range b.Stmts {
		r, err := i.execStmt(s, e)
		if err != nil {
			return result{}, err
		}
		if r.flow != flowNormal {
			return r, nil
		}
	}
	return result{flow: flowNormal}, nil
}

func (i *Interp) execStmt(s ast.Stmt, e *env) (result, error) {
	switch x := s.(type) {
	case *ast.Block:
		return i.execBlock(x, e)
	case *ast.If:
		c, err := i.evalExpr(x.Cond, e)
		if err != nil {
			return result{}, err
		}
		if asBool(c) {
			return i.execStmt(x.Then, e)
		}
		if x.Else != nil {
			return i.execStmt(x.Else, e)
		}
		return result{flow: flowNormal}, nil
	case *ast.While:
		for {
			c, err := i.evalExpr(x.Cond, e)
			if err != nil {
				return result{}, err
			}
			if !asBool(c) {
				break
			}
			r, err := i.execStmt(x.Body, e)
			if err != nil {
				return result{}, err
			}
			if r.flow == flowReturn {
				return r, nil
			}
			if r.flow == flowBreak {
				break
			}
			// flowContinue or flowNormal: re-test the condition.
		}
		return result{flow: flowNormal}, nil
	case *ast.For:
		inner := newEnv(e)
		if x.Init != nil {
			if _, err := i.execStmt(x.Init, inner); err != nil {
				return result{}, err
			}
		}
		for {
			c, err := i.evalExpr(x.Cond, inner)
			if err != nil {
				return result{}, err
			}
			if !asBool(c) {
				break
			}
			r, err := i.execStmt(x.Body, inner)
			if err != nil {
				return result{}, err
			}
			if r.flow == flowReturn {
				return r, nil
			}
			if r.flow == flowBreak {
				break
			}
			// flowContinue or flowNormal: run step and re-test.
			if x.Step != nil {
				if _, err := i.execStmt(x.Step, inner); err != nil {
					return result{}, err
				}
			}
		}
		return result{flow: flowNormal}, nil
	case *ast.Break:
		return result{flow: flowBreak}, nil
	case *ast.Continue:
		return result{flow: flowContinue}, nil
	case *ast.Return:
		if x.Value == nil {
			return result{flow: flowReturn, val: Void{}}, nil
		}
		v, err := i.evalExpr(x.Value, e)
		if err != nil {
			return result{}, err
		}
		return result{flow: flowReturn, val: v}, nil
	case *ast.Var:
		v, err := i.evalExpr(x.Init, e)
		if err != nil {
			return result{}, err
		}
		e.declare(x.Name, v)
		return result{flow: flowNormal}, nil
	case *ast.ExprStmt:
		if _, err := i.evalExpr(x.Expr, e); err != nil {
			return result{}, err
		}
		return result{flow: flowNormal}, nil
	}
	return result{}, fmt.Errorf("interp: unsupported statement %T", s)
}

func (i *Interp) evalExpr(e ast.Expr, env *env) (Value, error) {
	switch x := e.(type) {
	case *ast.NumberLit:
		return Number(x.Value), nil
	case *ast.BoolLit:
		return Bool(x.Value), nil
	case *ast.StringLit:
		return String(x.Value), nil
	case *ast.Ident:
		if v, ok := env.get(x.Name); ok {
			return v, nil
		}
		if fn, ok := i.Funcs[x.Name]; ok {
			return Func{Decl: fn}, nil
		}
		if _, ok := i.Builtins[x.Name]; ok {
			// Builtins aren't first-class for now; only callable.
			return nil, fmt.Errorf("interp: builtin %q can only be called, not used as a value", x.Name)
		}
		return nil, fmt.Errorf("undefined identifier %q", x.Name)
	case *ast.ArrayLit:
		out := make(Array, len(x.Elems))
		for k, el := range x.Elems {
			v, err := i.evalExpr(el, env)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	case *ast.Index:
		arrV, err := i.evalExpr(x.Array, env)
		if err != nil {
			return nil, err
		}
		idxV, err := i.evalExpr(x.Idx, env)
		if err != nil {
			return nil, err
		}
		arr, ok := arrV.(Array)
		if !ok {
			return nil, fmt.Errorf("indexing non-array %T", arrV)
		}
		idx, ok := idxV.(Number)
		if !ok {
			return nil, fmt.Errorf("array index must be number, got %T", idxV)
		}
		if idx < 0 || int(idx) >= len(arr) {
			return nil, fmt.Errorf("array index %d out of range [0, %d)", idx, len(arr))
		}
		return arr[idx], nil
	case *ast.Call:
		return i.evalCall(x, env)
	case *ast.Binary:
		return i.evalBinary(x, env)
	case *ast.Unary:
		v, err := i.evalExpr(x.Operand, env)
		if err != nil {
			return nil, err
		}
		switch x.Op {
		case "-":
			n, _ := v.(Number)
			return -n, nil
		case "!":
			b, _ := v.(Bool)
			return !b, nil
		}
		return nil, fmt.Errorf("interp: unsupported unary %q", x.Op)
	case *ast.Assign:
		return i.evalAssign(x, env)
	}
	return nil, fmt.Errorf("interp: unsupported expression %T", e)
}

func (i *Interp) evalCall(c *ast.Call, env *env) (Value, error) {
	args := make([]Value, len(c.Args))
	for k, a := range c.Args {
		v, err := i.evalExpr(a, env)
		if err != nil {
			return nil, err
		}
		args[k] = v
	}
	if id, ok := c.Callee.(*ast.Ident); ok {
		if b, ok := i.Builtins[id.Name]; ok {
			return b.Fn(i, args)
		}
		if v, ok := env.get(id.Name); ok {
			if fv, ok := v.(Func); ok {
				return i.callFunc(fv.Decl, args)
			}
			return nil, fmt.Errorf("calling non-function %q (%T)", id.Name, v)
		}
		if fn, ok := i.Funcs[id.Name]; ok {
			return i.callFunc(fn, args)
		}
		return nil, fmt.Errorf("undefined function %q", id.Name)
	}
	cv, err := i.evalExpr(c.Callee, env)
	if err != nil {
		return nil, err
	}
	if fv, ok := cv.(Func); ok {
		return i.callFunc(fv.Decl, args)
	}
	return nil, fmt.Errorf("interp: not a function: %T", cv)
}

func (i *Interp) evalBinary(b *ast.Binary, env *env) (Value, error) {
	// Short-circuit logical operators.
	switch b.Op {
	case "&&":
		l, err := i.evalExpr(b.Left, env)
		if err != nil {
			return nil, err
		}
		if !asBool(l) {
			return Bool(false), nil
		}
		return i.evalExpr(b.Right, env)
	case "||":
		l, err := i.evalExpr(b.Left, env)
		if err != nil {
			return nil, err
		}
		if asBool(l) {
			return Bool(true), nil
		}
		return i.evalExpr(b.Right, env)
	}
	l, err := i.evalExpr(b.Left, env)
	if err != nil {
		return nil, err
	}
	r, err := i.evalExpr(b.Right, env)
	if err != nil {
		return nil, err
	}
	if b.IsStringConcat {
		ls, _ := l.(String)
		rs, _ := r.(String)
		return ls + rs, nil
	}
	ln, lOk := l.(Number)
	rn, rOk := r.(Number)
	if lOk && rOk {
		switch b.Op {
		case "+":
			return ln + rn, nil
		case "-":
			return ln - rn, nil
		case "*":
			return ln * rn, nil
		case "/":
			if rn == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return ln / rn, nil
		case "%":
			if rn == 0 {
				return nil, fmt.Errorf("modulo by zero")
			}
			return ln % rn, nil
		case "&":
			return ln & rn, nil
		case "|":
			return ln | rn, nil
		case "^":
			return ln ^ rn, nil
		case "<<":
			return ln << rn, nil
		case ">>":
			return ln >> rn, nil
		case "==":
			return Bool(ln == rn), nil
		case "!=":
			return Bool(ln != rn), nil
		case "<":
			return Bool(ln < rn), nil
		case "<=":
			return Bool(ln <= rn), nil
		case ">":
			return Bool(ln > rn), nil
		case ">=":
			return Bool(ln >= rn), nil
		}
	}
	if lb, ok := l.(Bool); ok {
		if rb, ok := r.(Bool); ok {
			switch b.Op {
			case "==":
				return Bool(lb == rb), nil
			case "!=":
				return Bool(lb != rb), nil
			}
		}
	}
	return nil, fmt.Errorf("interp: %q on %T and %T not supported", b.Op, l, r)
}

func (i *Interp) evalAssign(a *ast.Assign, env *env) (Value, error) {
	v, err := i.evalExpr(a.Value, env)
	if err != nil {
		return nil, err
	}
	switch t := a.Target.(type) {
	case *ast.Ident:
		env.set(t.Name, v)
		return v, nil
	case *ast.Index:
		arrV, err := i.evalExpr(t.Array, env)
		if err != nil {
			return nil, err
		}
		idxV, err := i.evalExpr(t.Idx, env)
		if err != nil {
			return nil, err
		}
		arr, ok := arrV.(Array)
		if !ok {
			return nil, fmt.Errorf("array assignment to non-array %T", arrV)
		}
		idx, ok := idxV.(Number)
		if !ok {
			return nil, fmt.Errorf("array index must be number, got %T", idxV)
		}
		if idx < 0 || int(idx) >= len(arr) {
			return nil, fmt.Errorf("array index %d out of range [0, %d)", idx, len(arr))
		}
		arr[idx] = v
		return v, nil
	}
	return nil, fmt.Errorf("interp: invalid assignment target %T", a.Target)
}

func asBool(v Value) bool {
	switch x := v.(type) {
	case Bool:
		return bool(x)
	case Number:
		return x != 0
	}
	return false
}
