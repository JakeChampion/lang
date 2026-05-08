// Package monomorph turns generic function declarations + their
// per-call-site type-argument inferences (filled in by the
// checker) into concrete, name-mangled clones — so every later
// stage (IR lowering, codegen, interp) only ever sees monomorphic
// functions.
//
// Pipeline ordering: the pass runs after `checker.Check` and
// before any IR / codegen. It mutates the program in place:
//
//   - For every Call whose Callee is a generic FuncDecl, the
//     mangled clone name overwrites the Callee identifier.
//   - For every unique (name, type-args) instantiation, a cloned
//     FuncDecl is appended to prog.Funcs with TypeParams cleared
//     and ParamType references substituted with the concrete
//     types.
//   - The original generic decls are removed so the IR pipeline
//     never sees a function with TypeParams set.
//
// We also re-run `checker.Check` against the rewritten program
// at the end of the pass: the cloned functions need their
// FuncSigs entries, and any generic-call body that referenced
// the type parameters has to be re-typed in the concrete-arg
// world. The re-check uses the same checker.Info struct (so
// upstream callers keep their accumulated metadata) but with
// the monomorphic decls in place. Errors there indicate a
// type-substitution bug in the pass itself, not user error.
package monomorph

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// instKey identifies a unique (generic-name, mangled-name) pair —
// one entry per cloned monomorphic function. Declared at package
// scope so the deterministic sort helper can take it by type.
type instKey struct {
	name string
	mang string
}

// Run mutates prog in place, replacing every generic function +
// its call sites with monomorphic equivalents. After Run returns
// successfully, no FuncDecl in prog has non-empty TypeParams and
// no Call has non-empty TypeArgs (the field is consumed by the
// rewrite). info is re-populated to reflect the new shape.
func Run(prog *ast.Program, info *checker.Info) error {
	if len(info.GenericFuncs) == 0 {
		return nil
	}

	// 1. Walk every body, rewriting Call sites that target a
	//    generic function. For each such call, mangle the callee
	//    name and record the instantiation in `instantiations`.
	instantiations := map[instKey][]ast.Type{}

	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		walkBlock(fn.Body, func(c *ast.Call) {
			id, ok := c.Callee.(*ast.Ident)
			if !ok {
				return
			}
			gen, isGen := info.GenericFuncs[id.Name]
			if !isGen {
				return
			}
			if len(c.TypeArgs) != len(gen.TypeParams) {
				// Checker should have populated TypeArgs; if it
				// didn't, the call was already flagged as an
				// inference failure and we leave it alone (the
				// downstream stages will see a still-generic
				// callee and report a clearer error).
				return
			}
			mang := mangle(id.Name, c.TypeArgs)
			instantiations[instKey{name: id.Name, mang: mang}] = c.TypeArgs
			id.Name = mang
			c.TypeArgs = nil
		})
	}

	// 2. Generate the cloned, monomorphic FuncDecls. Walk the
	//    instantiations slice deterministically: name mangling is
	//    deterministic-by-construction, but Go's map iteration
	//    order isn't, so we sort the keys before cloning so wat
	//    emit + tests stay reproducible.
	keys := make([]instKey, 0, len(instantiations))
	for k := range instantiations {
		keys = append(keys, k)
	}
	sortKeys(keys)
	var cloned []*ast.FuncDecl
	for _, k := range keys {
		gen := info.GenericFuncs[k.name]
		args := instantiations[k]
		sub := make(map[string]ast.Type, len(gen.TypeParams))
		for i, tp := range gen.TypeParams {
			sub[tp] = args[i]
		}
		c := cloneFuncDecl(gen)
		c.Name = k.mang
		c.TypeParams = nil
		for i := range c.Params {
			c.Params[i].Type = substituteType(c.Params[i].Type, sub)
		}
		c.ReturnType = substituteType(c.ReturnType, sub)
		substituteBlock(c.Body, sub)
		cloned = append(cloned, c)
	}

	// 3. Drop the original generic decls + append the clones.
	keep := prog.Funcs[:0]
	for _, fn := range prog.Funcs {
		if _, isGen := info.GenericFuncs[fn.Name]; isGen {
			continue
		}
		keep = append(keep, fn)
	}
	prog.Funcs = append(keep, cloned...)

	// 4. Re-check. The cloned functions need FuncSigs entries +
	//    body type-checking with the substituted parameter types.
	// Reset GenericFuncs so a second monomorph run is a no-op.
	info.GenericFuncs = map[string]*ast.FuncDecl{}
	newInfo, err := checker.Check(prog)
	if err != nil {
		return fmt.Errorf("monomorph: re-check failed (compiler bug): %w", err)
	}
	*info = *newInfo
	return nil
}

// mangle generates a unique function name for a given
// instantiation. Format: `<base>__<arg1>__<arg2>…`. Type names
// come from ast.Type.String() which is already
// `i32` / `f32` / `Foo` / `Foo[i32]` style. Brackets and commas
// are stripped to keep the result a single identifier the rest
// of the pipeline accepts.
func mangle(base string, args []ast.Type) string {
	var b strings.Builder
	b.WriteString(base)
	for _, a := range args {
		b.WriteString("__")
		b.WriteString(sanitize(a.String()))
	}
	return b.String()
}

func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '[' || c == ']' || c == ',' || c == ' ' {
			out = append(out, '_')
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

func sortKeys(ks []instKey) {
	// Insertion sort — list lengths are tiny in practice (one
	// per unique generic instantiation in the program).
	for i := 1; i < len(ks); i++ {
		j := i
		for j > 0 && (ks[j-1].name > ks[j].name ||
			(ks[j-1].name == ks[j].name && ks[j-1].mang > ks[j].mang)) {
			ks[j-1], ks[j] = ks[j], ks[j-1]
			j--
		}
	}
}

// substituteType rewrites every ParamType in t to its concrete
// binding from sub. Mirrors the helper in the checker — duplicated
// here so the monomorph pass doesn't pull the checker's exported
// surface beyond what it needs.
func substituteType(t ast.Type, sub map[string]ast.Type) ast.Type {
	if t == nil {
		return nil
	}
	switch x := t.(type) {
	case ast.ParamType:
		if v, ok := sub[x.Name]; ok {
			return v
		}
		return x
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substituteType(x.Args[i], sub)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.ArrayType:
		return ast.ArrayType{Elem: substituteType(x.Elem, sub)}
	case ast.SliceType:
		return ast.SliceType{Elem: substituteType(x.Elem, sub)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = substituteType(x.Elems[i], sub)
		}
		return out
	case *ast.FuncType:
		out := &ast.FuncType{Result: substituteType(x.Result, sub)}
		for _, p := range x.Params {
			out.Params = append(out.Params, substituteType(p, sub))
		}
		return out
	}
	return t
}

// substituteBlock walks the body of a cloned generic function
// and rewrites any Var declarations whose type uses the type
// parameters. Other expression types either don't carry types
// directly (the checker re-derives them) or carry types that
// don't reference the parameters (e.g. integer literals are
// fine — they don't depend on T).
func substituteBlock(b *ast.Block, sub map[string]ast.Type) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		substituteStmt(s, sub)
	}
}

func substituteStmt(s ast.Stmt, sub map[string]ast.Type) {
	switch x := s.(type) {
	case *ast.Var:
		x.Type = substituteType(x.Type, sub)
	case *ast.Block:
		substituteBlock(x, sub)
	case *ast.If:
		substituteStmt(x.Then, sub)
		if x.Else != nil {
			substituteStmt(x.Else, sub)
		}
	case *ast.While:
		substituteStmt(x.Body, sub)
	case *ast.For:
		if x.Init != nil {
			substituteStmt(x.Init, sub)
		}
		substituteStmt(x.Body, sub)
	}
	// ExprStmt / Return / Assign / Switch / Match don't carry
	// declared types; the checker re-derives them when re-running.
}

// cloneFuncDecl produces a deep copy of fn suitable for
// post-substitution mutation. Body is structure-cloned so
// substituteBlock's in-place mutation doesn't leak into the
// generic source decl.
func cloneFuncDecl(fn *ast.FuncDecl) *ast.FuncDecl {
	c := *fn
	c.TypeParams = nil
	c.Params = append([]ast.Param(nil), fn.Params...)
	c.Body = cloneBlock(fn.Body)
	return &c
}

func cloneBlock(b *ast.Block) *ast.Block {
	if b == nil {
		return nil
	}
	out := &ast.Block{P: b.P, Stmts: make([]ast.Stmt, len(b.Stmts))}
	for i, s := range b.Stmts {
		out.Stmts[i] = cloneStmt(s)
	}
	return out
}

func cloneStmt(s ast.Stmt) ast.Stmt {
	switch x := s.(type) {
	case *ast.Var:
		c := *x
		return &c
	case *ast.Block:
		return cloneBlock(x)
	case *ast.If:
		c := *x
		c.Then = cloneStmt(x.Then).(*ast.Block)
		if x.Else != nil {
			c.Else = cloneStmt(x.Else)
		}
		return &c
	case *ast.While:
		c := *x
		if b, ok := x.Body.(*ast.Block); ok {
			c.Body = cloneBlock(b)
		} else {
			c.Body = cloneStmt(x.Body)
		}
		return &c
	case *ast.For:
		c := *x
		if x.Init != nil {
			c.Init = cloneStmt(x.Init)
		}
		if b, ok := x.Body.(*ast.Block); ok {
			c.Body = cloneBlock(b)
		} else {
			c.Body = cloneStmt(x.Body)
		}
		return &c
	}
	return s
}

// walkBlock invokes fn on every Call expression reachable from
// the block. Generic function call sites — the only thing the
// monomorph pass cares about — are necessarily Call nodes, so we
// don't need to recurse into other expression shapes that don't
// hold Call children.
func walkBlock(b *ast.Block, fn func(*ast.Call)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		walkStmt(s, fn)
	}
}

func walkStmt(s ast.Stmt, fn func(*ast.Call)) {
	switch x := s.(type) {
	case *ast.Var:
		walkExpr(x.Init, fn)
	case *ast.ExprStmt:
		walkExpr(x.Expr, fn)
	case *ast.Return:
		walkExpr(x.Value, fn)
	case *ast.If:
		walkExpr(x.Cond, fn)
		walkStmt(x.Then, fn)
		if x.Else != nil {
			walkStmt(x.Else, fn)
		}
	case *ast.While:
		walkExpr(x.Cond, fn)
		walkStmt(x.Body, fn)
	case *ast.For:
		if x.Init != nil {
			walkStmt(x.Init, fn)
		}
		if x.Cond != nil {
			walkExpr(x.Cond, fn)
		}
		if x.Step != nil {
			walkStmt(x.Step, fn)
		}
		walkStmt(x.Body, fn)
	case *ast.Block:
		walkBlock(x, fn)
	case *ast.Match:
		walkExpr(x.Tag, fn)
		for _, arm := range x.Arms {
			walkStmt(arm.Body, fn)
		}
	}
}

func walkExpr(e ast.Expr, fn func(*ast.Call)) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.Call:
		fn(x)
		walkExpr(x.Callee, fn)
		for _, a := range x.Args {
			walkExpr(a, fn)
		}
	case *ast.Binary:
		walkExpr(x.Left, fn)
		walkExpr(x.Right, fn)
	case *ast.Unary:
		walkExpr(x.Operand, fn)
	case *ast.Index:
		walkExpr(x.Array, fn)
		walkExpr(x.Idx, fn)
	case *ast.SliceExpr:
		walkExpr(x.Source, fn)
		walkExpr(x.Low, fn)
		walkExpr(x.High, fn)
	case *ast.FieldAccess:
		walkExpr(x.Target, fn)
	case *ast.Ternary:
		walkExpr(x.Cond, fn)
		walkExpr(x.Then, fn)
		walkExpr(x.Else, fn)
	case *ast.ArrayLit:
		for _, e := range x.Elems {
			walkExpr(e, fn)
		}
	case *ast.TupleLit:
		for _, e := range x.Elems {
			walkExpr(e, fn)
		}
	case *ast.StructLit:
		for _, f := range x.Fields {
			walkExpr(f.Value, fn)
		}
	case *ast.CastExpr:
		walkExpr(x.Inner, fn)
	}
}
