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
// successfully, no FuncDecl / StructDecl in prog has non-empty
// TypeParams and no Call / StructLit has non-empty TypeArgs (the
// field is consumed by the rewrite). info is re-populated to
// reflect the new shape.
func Run(prog *ast.Program, info *checker.Info) error {
	if len(info.GenericFuncs) == 0 && len(info.GenericStructs) == 0 {
		return nil
	}

	// 1. Walk every body, rewriting Call sites that target a
	//    generic function. For each such call, mangle the callee
	//    name and record the instantiation in `instantiations`.
	instantiations := map[instKey][]ast.Type{}
	structInsts := map[instKey][]ast.Type{}

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
		// Rewrite generic StructLits in the same body — TypeArgs
		// was stamped by the checker for every generic struct
		// literal, including those nested inside expressions.
		walkBlockStructLits(fn.Body, func(sl *ast.StructLit) {
			if len(sl.TypeArgs) == 0 {
				return
			}
			gen, isGen := info.GenericStructs[sl.TypeName]
			if !isGen || len(sl.TypeArgs) != len(gen.TypeParams) {
				return
			}
			mang := mangle(sl.TypeName, sl.TypeArgs)
			structInsts[instKey{name: sl.TypeName, mang: mang}] = sl.TypeArgs
			sl.TypeName = mang
			sl.TypeArgs = nil
		})
	}
	// (Type-slot rewriting for generic StructType references
	// happens AFTER function cloning below, since function
	// clones get their concrete StructType[Args] from
	// substitution and need to be mangled in the same pass.)

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

	// 3. Same shape for generic structs: clone per-instantiation
	//    with substituted field types.
	structKeys := make([]instKey, 0, len(structInsts))
	for k := range structInsts {
		structKeys = append(structKeys, k)
	}
	sortKeys(structKeys)
	var clonedStructs []*ast.StructDecl
	for _, k := range structKeys {
		gen := info.GenericStructs[k.name]
		args := structInsts[k]
		sub := make(map[string]ast.Type, len(gen.TypeParams))
		for i, tp := range gen.TypeParams {
			sub[tp] = args[i]
		}
		c := *gen
		c.Name = k.mang
		c.TypeParams = nil
		c.Fields = make([]ast.Param, len(gen.Fields))
		for i, f := range gen.Fields {
			c.Fields[i] = ast.Param{Name: f.Name, Type: substituteType(f.Type, sub)}
		}
		clonedStructs = append(clonedStructs, &c)
	}

	// 4. Drop the original generic decls + append the clones.
	keep := prog.Funcs[:0]
	for _, fn := range prog.Funcs {
		if _, isGen := info.GenericFuncs[fn.Name]; isGen {
			continue
		}
		keep = append(keep, fn)
	}
	prog.Funcs = append(keep, cloned...)
	keepStructs := prog.Structs[:0]
	for _, sd := range prog.Structs {
		if _, isGen := info.GenericStructs[sd.Name]; isGen {
			continue
		}
		keepStructs = append(keepStructs, sd)
	}
	prog.Structs = append(keepStructs, clonedStructs...)

	// 4b. Now that only monomorphic decls remain in prog.Funcs /
	//     prog.Structs, walk every type slot to mangle remaining
	//     generic StructType references (`Pair[i32, string]` →
	//     `Pair__i32__string`). Each unique instantiation seen
	//     here joins structInsts so the clone-generation loop
	//     below can produce the matching StructDecl.
	//
	//     Iterate to a fixed point: cloning a struct may
	//     introduce more StructType references (a generic struct
	//     whose field type is itself a generic struct), and the
	//     newly-cloned struct's field types may need their own
	//     mangling. Two passes typically suffice but we loop
	//     until no new instantiations are discovered.
	for round := 0; round < 8; round++ {
		before := len(structInsts)
		rewriteGenericStructTypes(prog, info, structInsts)
		// Append any structs the new pass found.
		for _, k := range collectKeys(structInsts) {
			already := false
			for _, sd := range prog.Structs {
				if sd.Name == k.mang {
					already = true
					break
				}
			}
			if already {
				continue
			}
			gen := info.GenericStructs[k.name]
			args := structInsts[k]
			sub := make(map[string]ast.Type, len(gen.TypeParams))
			for i, tp := range gen.TypeParams {
				sub[tp] = args[i]
			}
			c := *gen
			c.Name = k.mang
			c.TypeParams = nil
			c.Fields = make([]ast.Param, len(gen.Fields))
			for i, f := range gen.Fields {
				c.Fields[i] = ast.Param{Name: f.Name, Type: substituteType(f.Type, sub)}
			}
			prog.Structs = append(prog.Structs, &c)
		}
		if len(structInsts) == before {
			break
		}
	}

	// 5. Re-check. The cloned functions / structs need FuncSigs /
	//    Structs entries + body type-checking with the
	//    substituted types.
	info.GenericFuncs = map[string]*ast.FuncDecl{}
	info.GenericStructs = map[string]*ast.StructDecl{}
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
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substituteType(x.Args[i], sub)
		}
		return ast.StructType{Name: x.Name, Args: args}
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
		substituteExpr(x.Init, sub)
	case *ast.Destructure:
		substituteExpr(x.Init, sub)
	case *ast.Block:
		substituteBlock(x, sub)
	case *ast.If:
		substituteExpr(x.Cond, sub)
		substituteStmt(x.Then, sub)
		if x.Else != nil {
			substituteStmt(x.Else, sub)
		}
	case *ast.IfLet:
		substituteExpr(x.Source, sub)
		// BindingTypes are concrete after the checker stamped
		// them in the original generic body; substitute so
		// per-clone they specialise to the concrete instantiation
		// of the enum / struct payload.
		for i := range x.BindingTypes {
			x.BindingTypes[i] = substituteType(x.BindingTypes[i], sub)
		}
		substituteStmt(x.Then, sub)
		if x.Else != nil {
			substituteStmt(x.Else, sub)
		}
	case *ast.LetElse:
		substituteExpr(x.Source, sub)
		for i := range x.BindingTypes {
			x.BindingTypes[i] = substituteType(x.BindingTypes[i], sub)
		}
		substituteBlock(x.Else, sub)
	case *ast.While:
		substituteExpr(x.Cond, sub)
		substituteStmt(x.Body, sub)
	case *ast.For:
		if x.Init != nil {
			substituteStmt(x.Init, sub)
		}
		substituteExpr(x.Cond, sub)
		substituteStmt(x.Body, sub)
	case *ast.ExprStmt:
		substituteExpr(x.Expr, sub)
	case *ast.Return:
		substituteExpr(x.Value, sub)
	case *ast.Match:
		substituteExpr(x.Tag, sub)
		for _, arm := range x.Arms {
			substituteExpr(arm.Guard, sub)
			substituteBlock(arm.Body, sub)
		}
	}
}

// substituteExpr walks an expression tree applying sub to every
// type-bearing node (StructLit.TypeArgs, CastExpr.Target,
// Call.TypeArgs). Doesn't touch type-free shapes — the checker
// re-derives those during the post-monomorph re-check.
func substituteExpr(e ast.Expr, sub map[string]ast.Type) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.StructLit:
		if len(x.TypeArgs) > 0 {
			for i := range x.TypeArgs {
				x.TypeArgs[i] = substituteType(x.TypeArgs[i], sub)
			}
		}
		for _, f := range x.Fields {
			substituteExpr(f.Value, sub)
		}
	case *ast.Call:
		if len(x.TypeArgs) > 0 {
			for i := range x.TypeArgs {
				x.TypeArgs[i] = substituteType(x.TypeArgs[i], sub)
			}
		}
		substituteExpr(x.Callee, sub)
		for _, a := range x.Args {
			substituteExpr(a, sub)
		}
	case *ast.CastExpr:
		x.Target = substituteType(x.Target, sub)
		substituteExpr(x.Inner, sub)
	case *ast.Binary:
		substituteExpr(x.Left, sub)
		substituteExpr(x.Right, sub)
	case *ast.Unary:
		substituteExpr(x.Operand, sub)
	case *ast.Index:
		substituteExpr(x.Array, sub)
		substituteExpr(x.Idx, sub)
	case *ast.SliceExpr:
		substituteExpr(x.Source, sub)
		substituteExpr(x.Low, sub)
		substituteExpr(x.High, sub)
	case *ast.FieldAccess:
		substituteExpr(x.Target, sub)
	case *ast.Ternary:
		substituteExpr(x.Cond, sub)
		substituteExpr(x.Then, sub)
		substituteExpr(x.Else, sub)
	case *ast.ArrayLit:
		for _, e := range x.Elems {
			substituteExpr(e, sub)
		}
	case *ast.TupleLit:
		for _, e := range x.Elems {
			substituteExpr(e, sub)
		}
	}
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
		c.Init = cloneExpr(x.Init)
		return &c
	case *ast.Destructure:
		c := *x
		c.Names = append([]string(nil), x.Names...)
		c.Init = cloneExpr(x.Init)
		return &c
	case *ast.ExprStmt:
		c := *x
		c.Expr = cloneExpr(x.Expr)
		return &c
	case *ast.Return:
		c := *x
		c.Value = cloneExpr(x.Value)
		return &c
	case *ast.Block:
		return cloneBlock(x)
	case *ast.If:
		c := *x
		c.Cond = cloneExpr(x.Cond)
		c.Then = cloneStmt(x.Then).(*ast.Block)
		if x.Else != nil {
			c.Else = cloneStmt(x.Else)
		}
		return &c
	case *ast.IfLet:
		c := *x
		c.Source = cloneExpr(x.Source)
		c.Bindings = append([]string(nil), x.Bindings...)
		c.BindingTypes = append([]ast.Type(nil), x.BindingTypes...)
		c.Then = cloneStmt(x.Then)
		if x.Else != nil {
			c.Else = cloneStmt(x.Else)
		}
		return &c
	case *ast.LetElse:
		c := *x
		c.Source = cloneExpr(x.Source)
		c.Bindings = append([]string(nil), x.Bindings...)
		c.BindingTypes = append([]ast.Type(nil), x.BindingTypes...)
		c.Else = cloneBlock(x.Else)
		return &c
	case *ast.While:
		c := *x
		c.Cond = cloneExpr(x.Cond)
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
		c.Cond = cloneExpr(x.Cond)
		if x.Step != nil {
			c.Step = cloneStmt(x.Step)
		}
		if b, ok := x.Body.(*ast.Block); ok {
			c.Body = cloneBlock(b)
		} else {
			c.Body = cloneStmt(x.Body)
		}
		return &c
	case *ast.Match:
		c := *x
		c.Tag = cloneExpr(x.Tag)
		c.Arms = make([]*ast.MatchArm, len(x.Arms))
		for i, arm := range x.Arms {
			ac := *arm
			ac.Guard = cloneExpr(arm.Guard)
			ac.Body = cloneBlock(arm.Body)
			c.Arms[i] = &ac
		}
		return &c
	}
	return s
}

// cloneExpr deep-copies an expression so the original generic
// function's body stays untouched when substituteExpr mutates
// the clone's TypeArgs / target Type. Most node kinds are
// shallow-cloneable structs whose only pointer fields are
// sub-expressions or sub-statement slices.
func cloneExpr(e ast.Expr) ast.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ast.Ident, *ast.NumberLit, *ast.FloatLit, *ast.BoolLit, *ast.StringLit:
		// Leaf types — no sub-expressions to clone, but we do a
		// pointer-level shallow copy so callers can swap fields
		// without aliasing.
		switch v := x.(type) {
		case *ast.Ident:
			c := *v
			return &c
		case *ast.NumberLit:
			c := *v
			return &c
		case *ast.FloatLit:
			c := *v
			return &c
		case *ast.BoolLit:
			c := *v
			return &c
		case *ast.StringLit:
			c := *v
			return &c
		}
	case *ast.Binary:
		c := *x
		c.Left = cloneExpr(x.Left)
		c.Right = cloneExpr(x.Right)
		return &c
	case *ast.Unary:
		c := *x
		c.Operand = cloneExpr(x.Operand)
		return &c
	case *ast.Call:
		c := *x
		c.Callee = cloneExpr(x.Callee)
		c.Args = make([]ast.Expr, len(x.Args))
		for i, a := range x.Args {
			c.Args[i] = cloneExpr(a)
		}
		c.TypeArgs = append([]ast.Type(nil), x.TypeArgs...)
		return &c
	case *ast.Index:
		c := *x
		c.Array = cloneExpr(x.Array)
		c.Idx = cloneExpr(x.Idx)
		return &c
	case *ast.SliceExpr:
		c := *x
		c.Source = cloneExpr(x.Source)
		c.Low = cloneExpr(x.Low)
		c.High = cloneExpr(x.High)
		return &c
	case *ast.FieldAccess:
		c := *x
		c.Target = cloneExpr(x.Target)
		return &c
	case *ast.Ternary:
		c := *x
		c.Cond = cloneExpr(x.Cond)
		c.Then = cloneExpr(x.Then)
		c.Else = cloneExpr(x.Else)
		return &c
	case *ast.ArrayLit:
		c := *x
		c.Elems = make([]ast.Expr, len(x.Elems))
		for i, e := range x.Elems {
			c.Elems[i] = cloneExpr(e)
		}
		return &c
	case *ast.TupleLit:
		c := *x
		c.Elems = make([]ast.Expr, len(x.Elems))
		for i, e := range x.Elems {
			c.Elems[i] = cloneExpr(e)
		}
		return &c
	case *ast.StructLit:
		c := *x
		c.Fields = make([]ast.FieldInit, len(x.Fields))
		for i, f := range x.Fields {
			c.Fields[i] = ast.FieldInit{Name: f.Name, Value: cloneExpr(f.Value)}
		}
		c.TypeArgs = append([]ast.Type(nil), x.TypeArgs...)
		return &c
	case *ast.CastExpr:
		c := *x
		c.Inner = cloneExpr(x.Inner)
		return &c
	case *ast.Assign:
		c := *x
		c.Target = cloneExpr(x.Target)
		c.Value = cloneExpr(x.Value)
		return &c
	}
	return e
}

// walkBlockStructLits is the StructLit analogue of walkBlock —
// invokes fn on every StructLit reachable from the body so the
// monomorpher can rewrite generic instantiations regardless of
// where they nest (struct literal inside a tuple inside a
// variable initialiser, etc).
func walkBlockStructLits(b *ast.Block, fn func(*ast.StructLit)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		walkStmtStructLits(s, fn)
	}
}

func walkStmtStructLits(s ast.Stmt, fn func(*ast.StructLit)) {
	switch x := s.(type) {
	case *ast.Var:
		walkExprStructLits(x.Init, fn)
	case *ast.Destructure:
		walkExprStructLits(x.Init, fn)
	case *ast.ExprStmt:
		walkExprStructLits(x.Expr, fn)
	case *ast.Return:
		walkExprStructLits(x.Value, fn)
	case *ast.If:
		walkExprStructLits(x.Cond, fn)
		walkStmtStructLits(x.Then, fn)
		if x.Else != nil {
			walkStmtStructLits(x.Else, fn)
		}
	case *ast.IfLet:
		walkExprStructLits(x.Source, fn)
		walkStmtStructLits(x.Then, fn)
		if x.Else != nil {
			walkStmtStructLits(x.Else, fn)
		}
	case *ast.LetElse:
		walkExprStructLits(x.Source, fn)
		walkBlockStructLits(x.Else, fn)
	case *ast.While:
		walkExprStructLits(x.Cond, fn)
		walkStmtStructLits(x.Body, fn)
	case *ast.For:
		if x.Init != nil {
			walkStmtStructLits(x.Init, fn)
		}
		if x.Cond != nil {
			walkExprStructLits(x.Cond, fn)
		}
		if x.Step != nil {
			walkStmtStructLits(x.Step, fn)
		}
		walkStmtStructLits(x.Body, fn)
	case *ast.Block:
		walkBlockStructLits(x, fn)
	case *ast.Match:
		walkExprStructLits(x.Tag, fn)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				walkExprStructLits(arm.Guard, fn)
			}
			walkBlockStructLits(arm.Body, fn)
		}
	}
}

func walkExprStructLits(e ast.Expr, fn func(*ast.StructLit)) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.StructLit:
		fn(x)
		for _, f := range x.Fields {
			walkExprStructLits(f.Value, fn)
		}
	case *ast.Call:
		walkExprStructLits(x.Callee, fn)
		for _, a := range x.Args {
			walkExprStructLits(a, fn)
		}
	case *ast.Binary:
		walkExprStructLits(x.Left, fn)
		walkExprStructLits(x.Right, fn)
	case *ast.Unary:
		walkExprStructLits(x.Operand, fn)
	case *ast.Index:
		walkExprStructLits(x.Array, fn)
		walkExprStructLits(x.Idx, fn)
	case *ast.SliceExpr:
		walkExprStructLits(x.Source, fn)
		walkExprStructLits(x.Low, fn)
		walkExprStructLits(x.High, fn)
	case *ast.FieldAccess:
		walkExprStructLits(x.Target, fn)
	case *ast.Ternary:
		walkExprStructLits(x.Cond, fn)
		walkExprStructLits(x.Then, fn)
		walkExprStructLits(x.Else, fn)
	case *ast.ArrayLit:
		for _, e := range x.Elems {
			walkExprStructLits(e, fn)
		}
	case *ast.TupleLit:
		for _, e := range x.Elems {
			walkExprStructLits(e, fn)
		}
	case *ast.CastExpr:
		walkExprStructLits(x.Inner, fn)
	}
}

// rewriteGenericStructTypes walks every Type slot in the program
// (function params + return types, var declarations, struct
// field types) and rewrites StructType references with non-empty
// Args into the mangled flat name — recording each unique
// instantiation in `into` so the clone-generation step below
// emits exactly one StructDecl per (name, args) pair.
func rewriteGenericStructTypes(prog *ast.Program, info *checker.Info, into map[instKey][]ast.Type) {
	rewrite := func(slot *ast.Type) {
		if slot == nil {
			return
		}
		*slot = rewriteType(*slot, info, into)
	}
	// Functions: receivers, params, return types, var decls.
	for _, fn := range prog.Funcs {
		if fn.Receiver != nil {
			rewrite(&fn.Receiver.Type)
		}
		for i := range fn.Params {
			rewrite(&fn.Params[i].Type)
		}
		rewrite(&fn.ReturnType)
		rewriteBlockTypes(fn.Body, info, into)
	}
	// Structs: field types of NON-GENERIC structs (generic ones
	// are about to be dropped). Field types of cloned structs
	// were already substituted during cloning above.
	for _, sd := range prog.Structs {
		if _, isGen := info.GenericStructs[sd.Name]; isGen {
			continue
		}
		for i := range sd.Fields {
			rewrite(&sd.Fields[i].Type)
		}
	}
}

// collectKeys returns a deterministic-order key list for the
// fixed-point loop below — same insertion-sort the function
// instantiation step uses, kept inline so the wat output stays
// reproducible across runs.
func collectKeys(m map[instKey][]ast.Type) []instKey {
	out := make([]instKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortKeys(out)
	return out
}

// rewriteType rewrites a single Type tree in place: any
// StructType whose Name appears in info.GenericStructs and whose
// Args is populated gets flattened to a mangled StructType. The
// mangled name lookup populates `into` so the caller can build
// clones for every unique instantiation.
func rewriteType(t ast.Type, info *checker.Info, into map[instKey][]ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		// Recursively rewrite nested args first — a generic
		// struct's args may themselves reference other generic
		// structs.
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = rewriteType(x.Args[i], info, into)
		}
		if _, isGen := info.GenericStructs[x.Name]; !isGen {
			return ast.StructType{Name: x.Name, Args: args}
		}
		mang := mangle(x.Name, args)
		into[instKey{name: x.Name, mang: mang}] = args
		return ast.StructType{Name: mang}
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = rewriteType(x.Args[i], info, into)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.ArrayType:
		return ast.ArrayType{Elem: rewriteType(x.Elem, info, into)}
	case ast.SliceType:
		return ast.SliceType{Elem: rewriteType(x.Elem, info, into)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = rewriteType(x.Elems[i], info, into)
		}
		return out
	case *ast.FuncType:
		out := &ast.FuncType{Result: rewriteType(x.Result, info, into)}
		for _, p := range x.Params {
			out.Params = append(out.Params, rewriteType(p, info, into))
		}
		return out
	}
	return t
}

func rewriteBlockTypes(b *ast.Block, info *checker.Info, into map[instKey][]ast.Type) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		rewriteStmtTypes(s, info, into)
	}
}

func rewriteStmtTypes(s ast.Stmt, info *checker.Info, into map[instKey][]ast.Type) {
	switch x := s.(type) {
	case *ast.Var:
		if x.Type != nil {
			x.Type = rewriteType(x.Type, info, into)
		}
	case *ast.Destructure:
		// No types stored on the node itself — element types
		// flow from the synthesised temp `*ast.Var` in
		// info.Locals, which the existing Var case handles.
	case *ast.Block:
		rewriteBlockTypes(x, info, into)
	case *ast.If:
		rewriteStmtTypes(x.Then, info, into)
		if x.Else != nil {
			rewriteStmtTypes(x.Else, info, into)
		}
	case *ast.IfLet:
		for i := range x.BindingTypes {
			x.BindingTypes[i] = rewriteType(x.BindingTypes[i], info, into)
		}
		rewriteStmtTypes(x.Then, info, into)
		if x.Else != nil {
			rewriteStmtTypes(x.Else, info, into)
		}
	case *ast.LetElse:
		for i := range x.BindingTypes {
			x.BindingTypes[i] = rewriteType(x.BindingTypes[i], info, into)
		}
		rewriteBlockTypes(x.Else, info, into)
	case *ast.While:
		rewriteStmtTypes(x.Body, info, into)
	case *ast.For:
		if x.Init != nil {
			rewriteStmtTypes(x.Init, info, into)
		}
		rewriteStmtTypes(x.Body, info, into)
	case *ast.Match:
		for _, arm := range x.Arms {
			rewriteBlockTypes(arm.Body, info, into)
		}
	}
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
	case *ast.Destructure:
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
	case *ast.IfLet:
		walkExpr(x.Source, fn)
		walkStmt(x.Then, fn)
		if x.Else != nil {
			walkStmt(x.Else, fn)
		}
	case *ast.LetElse:
		walkExpr(x.Source, fn)
		walkBlock(x.Else, fn)
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
