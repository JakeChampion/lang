package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ssa"
)

// BuildFunc builds the experimental typed phase from a checked declaration,
// before ir.LowerWith erases surface types and inserts concrete RC operations.
// The current slice handles immutable local bindings, array/tuple construction
// and projections, and conditional control flow. Mutation, loops and
// cleanup effects remain explicit unsupported errors, not silent fallbacks.
// Production compilation continues to use its existing route until ownership
// analysis and verified lowering make this phase an end-to-end replacement.
func BuildFunc(decl *ast.FuncDecl, info *checker.Info) (*Func, error) {
	program, err := BuildProgram(&ast.Program{Funcs: []*ast.FuncDecl{decl}}, info)
	if err != nil {
		return nil, err
	}
	return program.funcs[0], nil
}

func buildBody(f *Func, decl *ast.FuncDecl, info *checker.Info) error {
	b := builder{fn: f, info: info, current: f.graph.NewBlock(), values: make(map[BindingID]ssa.Value)}
	b.pushScope()
	for i, p := range decl.Params {
		v := f.addParam(p.Type, f.contract.modes[i], p.NamePos)
		if err := b.bind(p.Name, p.Type, p.NamePos, v); err != nil {
			return err
		}
	}
	if err := b.stmt(decl.Body); err != nil {
		return err
	}
	if b.current != nil {
		if _, void := f.result.(ast.VoidType); !void {
			return b.errorAt(decl.P, "value-returning body falls through")
		}
		f.graph.SetRet(b.current, ssa.Value{})
	}
	return nil
}

type builder struct {
	fn      *Func
	info    *checker.Info
	current *ssa.Block
	scopes  []map[string]BindingID
	values  map[BindingID]ssa.Value
}

func (b *builder) errorAt(pos ast.Position, message string) error {
	return fmt.Errorf("semir %s at %d:%d: %s", b.fn.graph.Name, pos.Line, pos.Col, message)
}

func (b *builder) pushScope() { b.scopes = append(b.scopes, make(map[string]BindingID)) }

func (b *builder) popScope() { b.scopes = b.scopes[:len(b.scopes)-1] }

func (b *builder) bind(name string, typ ast.Type, pos ast.Position, value ssa.Value) error {
	if !ast.Equal(typ, b.fn.values[value.ID].typ) {
		return b.errorAt(pos, "binding type does not match its semantic value")
	}
	scope := b.scopes[len(b.scopes)-1]
	if _, exists := scope[name]; exists {
		return b.errorAt(pos, "duplicate declaration in the same scope")
	}
	id := b.fn.addBinding(name, typ, pos)
	scope[name] = id
	b.values[id] = value
	return nil
}

func (b *builder) lookup(name string) (ssa.Value, bool) {
	for i := len(b.scopes) - 1; i >= 0; i-- {
		if id, ok := b.scopes[i][name]; ok {
			return b.values[id], true
		}
	}
	return ssa.Value{}, false
}

func (b *builder) stmt(stmt ast.Stmt) error {
	if b.current == nil {
		return nil // The preceding statement terminates every path.
	}
	switch n := stmt.(type) {
	case *ast.Block:
		b.pushScope()
		defer b.popScope()
		for _, child := range n.Stmts {
			if err := b.stmt(child); err != nil {
				return err
			}
		}
		return nil
	case *ast.Var:
		typ, ok := b.info.VarTypes[n]
		if !ok || !ast.Equal(typ, n.Type) {
			return b.errorAt(n.P, "missing or inconsistent checked local type")
		}
		value, err := b.expr(n.Init)
		if err != nil {
			return err
		}
		return b.bind(n.Name, typ, n.P, value)
	case *ast.Destructure:
		return b.destructure(n)
	case *ast.Return:
		var value ssa.Value
		if n.Value != nil {
			var err error
			value, err = b.expr(n.Value)
			if err != nil {
				return err
			}
		}
		b.fn.graph.SetRet(b.current, value)
		b.current = nil
		return nil
	case *ast.If:
		cond, err := b.expr(n.Cond)
		if err != nil {
			return err
		}
		yes, no := b.fn.graph.NewBlock(), b.fn.graph.NewBlock()
		b.fn.graph.SetBrIf(b.current, cond, yes, no)
		ends := make([]*ssa.Block, 0, 2)
		for i, arm := range []ast.Stmt{n.Then, n.Else} {
			b.current = []*ssa.Block{yes, no}[i]
			b.pushScope()
			if arm != nil {
				err = b.stmt(arm)
			}
			b.popScope()
			if err != nil {
				return err
			}
			if b.current != nil {
				ends = append(ends, b.current)
			}
		}
		b.current = nil
		if len(ends) != 0 {
			b.current = b.fn.graph.NewBlock()
			for _, end := range ends {
				b.fn.graph.SetBr(end, b.current)
			}
		}
		return nil
	default:
		return b.errorAt(stmt.Pos(), fmt.Sprintf("unsupported statement %T", stmt))
	}
}

func (b *builder) destructure(n *ast.Destructure) error {
	if n.StructName != "" || len(n.Fields) != 0 {
		return b.errorAt(n.P, "struct projection is not implemented in the typed pilot")
	}
	value, err := b.expr(n.Init)
	if err != nil {
		return err
	}
	typ, ok := b.fn.values[value.ID].typ.(ast.TupleType)
	if !ok || len(typ.Elems) != len(n.Names) || (len(n.Nested) != 0 && len(n.Nested) != len(n.Names)) {
		return b.errorAt(n.P, "inconsistent checked tuple destructure")
	}
	if n.AtName != "" {
		if err := b.bind(n.AtName, typ, n.P, value); err != nil {
			return err
		}
	}
	for i, name := range n.Names {
		field := b.fn.addOp(b.current, ssa.OpTupleGet, typ.Elems[i], n.P, value)
		b.current.Ops[len(b.current.Ops)-1].Imm = int64(i)
		if err := b.bind(name, typ.Elems[i], n.P, field); err != nil {
			return err
		}
		if len(n.Nested) != 0 && n.Nested[i] != nil {
			if err := b.destructure(n.Nested[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *builder) expr(expr ast.Expr) (ssa.Value, error) {
	if expr == nil {
		return ssa.Value{}, fmt.Errorf("semir: missing checked expression")
	}
	emit := func(kind ssa.OpKind, typ ast.Type, args ...ssa.Value) ssa.Value {
		return b.fn.addOp(b.current, kind, typ, expr.Pos(), args...)
	}
	switch n := expr.(type) {
	case *ast.Ident:
		if value, ok := b.lookup(n.Name); ok && n.EnumName == "" {
			return value, nil
		}
		return ssa.Value{}, b.errorAt(n.P, "unresolved local binding: "+n.Name)
	case *ast.StringLit:
		value := emit(ssa.OpConstString, ast.StringType{})
		b.current.Ops[len(b.current.Ops)-1].Str = n.Value
		return value, nil
	case *ast.NumberLit:
		if n.IsFloat {
			return ssa.Value{}, b.errorAt(n.P, "float literal is not implemented in the typed pilot")
		}
		value := emit(ssa.OpConstInt, ast.NumberType{Width: n.Width, Signed: !n.IsUnsigned})
		b.current.Ops[len(b.current.Ops)-1].Imm = n.Value
		return value, nil
	case *ast.BoolLit:
		value := emit(ssa.OpConstBool, ast.BoolType{})
		if n.Value {
			b.current.Ops[len(b.current.Ops)-1].Imm = 1
		}
		return value, nil
	case *ast.ArrayLit:
		args, _, err := b.exprs(n.Elems)
		if err != nil {
			return ssa.Value{}, err
		}
		return emit(ssa.OpArrayMake, ast.ArrayType{Elem: n.ElemType}, args...), nil
	case *ast.TupleLit:
		args, types, err := b.exprs(n.Elems)
		if err != nil {
			return ssa.Value{}, err
		}
		return emit(ssa.OpTupleMake, ast.TupleType{Elems: types}, args...), nil
	case *ast.Index:
		if n.IsString || n.IsSlice || n.Unchecked {
			return ssa.Value{}, b.errorAt(n.P, "unsupported projection contract")
		}
		args, _, err := b.exprs([]ast.Expr{n.Array, n.Idx})
		if err != nil {
			return ssa.Value{}, err
		}
		return emit(ssa.OpArrayGet, n.ElemType, args...), nil
	case *ast.Call:
		if intrinsic, ok := b.info.IntrinsicCalls[n]; ok {
			if intrinsic.Kind != checker.IntrinsicArrayAppend || intrinsic.Signature == nil {
				return ssa.Value{}, b.errorAt(n.P, "unsupported or unresolved intrinsic contract")
			}
			args, types, err := b.exprs(n.Args)
			if err != nil {
				return ssa.Value{}, err
			}
			sig := intrinsic.Signature
			if len(args) != len(sig.Params) {
				return ssa.Value{}, b.errorAt(n.P, "intrinsic argument count differs from checked contract")
			}
			for i, typ := range types {
				if !ast.Equal(typ, sig.Params[i]) {
					return ssa.Value{}, b.errorAt(n.P, "intrinsic argument type differs from checked contract")
				}
			}
			return emit(ssa.OpArrayAppend, sig.Result, args...), nil
		}
		ident, direct := n.Callee.(*ast.Ident)
		if !direct || n.IsVariantCall || n.DynTrait != "" || len(n.TypeArgs) != 0 || len(n.ArgNames) != 0 {
			return ssa.Value{}, b.errorAt(n.P, "unsupported semantic call form")
		}
		if _, local := b.lookup(ident.Name); local {
			return ssa.Value{}, b.errorAt(n.P, "indirect calls are not implemented in the typed pilot")
		}
		id, ok := b.fn.program.byName[ident.Name]
		if !ok {
			return ssa.Value{}, b.errorAt(n.P, "callee is outside the typed program: "+ident.Name)
		}
		callee := b.fn.program.funcs[id-1]
		if _, void := callee.result.(ast.VoidType); void {
			return ssa.Value{}, b.errorAt(n.P, "void call effects are not implemented in the typed pilot")
		}
		args, _, err := b.exprs(n.Args)
		if err != nil {
			return ssa.Value{}, err
		}
		value := emit(ssa.OpSemanticCall, callee.result, args...)
		b.current.Ops[len(b.current.Ops)-1].Imm = id
		return value, nil
	default:
		return ssa.Value{}, b.errorAt(expr.Pos(), fmt.Sprintf("unsupported expression %T", expr))
	}
}

func (b *builder) exprs(exprs []ast.Expr) ([]ssa.Value, []ast.Type, error) {
	values := make([]ssa.Value, 0, len(exprs))
	types := make([]ast.Type, 0, len(exprs))
	for _, expr := range exprs {
		value, err := b.expr(expr)
		if err != nil {
			return nil, nil, err
		}
		values = append(values, value)
		types = append(types, b.fn.values[value.ID].typ)
	}
	return values, types, nil
}
