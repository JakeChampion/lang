package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

// Verify validates semantic types and projection shape as well as SSA's
// structural invariants. It does not certify RC balance or uniqueness: those
// require the ownership/effect analysis that follows this representation.
func Verify(f *Func) error {
	if f == nil || f.graph == nil {
		return fmt.Errorf("semir: nil function")
	}
	g := f.graph
	fail := func(format string, args ...any) error {
		return fmt.Errorf("semir %s: %s", g.Name, fmt.Sprintf(format, args...))
	}
	if err := resolvedType(f.result, true); err != nil {
		return fail("result: %v", err)
	}
	if g.Entry == nil || len(g.Blocks) == 0 {
		return fail("missing function body")
	}
	if len(f.modes) != len(g.Params) {
		return fail("parameter modes do not match parameters")
	}
	blocks := make(map[*ssa.Block]bool, len(g.Blocks))
	blockIDs := make(map[int32]bool, len(g.Blocks))
	for _, b := range g.Blocks {
		if b == nil || b.ID <= 0 || blocks[b] || blockIDs[b.ID] {
			return fail("nil or duplicate block identity")
		}
		blocks[b], blockIDs[b.ID] = true, true
	}
	defs := make(map[int32]bool, len(f.values))
	checkValue := func(v ssa.Value, define bool) error {
		if v.ID <= 0 || v.Func != g {
			return fail("invalid or foreign value %v", v)
		}
		info, ok := f.values[v.ID]
		if !ok {
			return fail("%v has no semantic type", v)
		}
		if define {
			if defs[v.ID] {
				return fail("%v defined twice", v)
			}
			defs[v.ID] = true
			if err := resolvedType(info.typ, false); err != nil {
				return fail("%v: %v", v, err)
			}
		}
		return nil
	}
	for i, p := range g.Params {
		if err := checkValue(p, true); err != nil {
			return err
		}
		ref := referenceBearing(f.values[p.ID].typ)
		mode := f.modes[i]
		if (ref && mode != ParamBorrow && mode != ParamCounted) || (!ref && mode != ParamValue) {
			return fail("parameter %v has invalid ownership mode %d", p, mode)
		}
	}
	for _, b := range g.Blocks {
		preds := make(map[*ssa.Block]bool, len(b.Preds))
		for _, p := range b.Preds {
			if !blocks[p] || preds[p] {
				return fail("block %d has foreign or duplicate predecessor", b.ID)
			}
			preds[p] = true
			found := false
			for _, s := range p.Succs() {
				found = found || s == b
			}
			if !found {
				return fail("block %d has stale predecessor", b.ID)
			}
		}
		for _, s := range b.Succs() {
			if !blocks[s] {
				return fail("block %d has foreign successor", b.ID)
			}
		}
		for _, op := range b.Ops {
			if op == nil || op.Result2 != (ssa.Value{}) {
				return fail("nil operation or ABI-split semantic value")
			}
			if err := checkValue(op.Result, true); err != nil {
				return err
			}
			for _, a := range op.Args {
				if err := checkValue(a, false); err != nil {
					return err
				}
			}
			if err := verifyOp(f, op); err != nil {
				return fail("%v = %s: %v", op.Result, op.Kind, err)
			}
		}
		var used ssa.Value
		switch b.Term.Kind {
		case ssa.TermBr:
		case ssa.TermBrIf:
			used = b.Term.Cond
			if !ast.Equal(f.values[used.ID].typ, ast.BoolType{}) {
				return fail("branch condition is not boolean")
			}
		case ssa.TermRet:
			used = b.Term.Value
			if _, void := f.result.(ast.VoidType); void {
				if used != (ssa.Value{}) {
					return fail("void function returns a value")
				}
			} else if !ast.Equal(f.values[used.ID].typ, f.result) {
				return fail("return type does not match signature")
			}
		default:
			return fail("unsupported semantic terminator %d", b.Term.Kind)
		}
		if used != (ssa.Value{}) {
			if err := checkValue(used, false); err != nil {
				return err
			}
		}
	}
	if len(defs) != len(f.values) {
		return fail("stale semantic value metadata")
	}
	for _, b := range f.bindings {
		if err := resolvedType(b.typ, false); err != nil {
			return fail("binding %q: %v", b.name, err)
		}
	}
	return ssa.Verify(g)
}

func verifyOp(f *Func, op *ssa.Op) error {
	result := f.values[op.Result.ID].typ
	arg := func(i int) ast.Type { return f.values[op.Args[i].ID].typ }
	bad := func() error { return fmt.Errorf("invalid operand/result types or arity") }
	if scalarOp(op.Kind) {
		if len(op.Args) != 2 || !ast.Equal(arg(0), arg(1)) {
			return bad()
		}
		i32 := ast.Equal(arg(0), ast.NumberType{})
		boolean := ast.Equal(arg(0), ast.BoolType{}) && (op.Kind == ssa.OpEq || op.Kind == ssa.OpNe)
		if !i32 && !boolean {
			return bad()
		}
		var want ast.Type = ast.BoolType{}
		if scalarArithmetic(op.Kind) {
			want = arg(0)
		}
		if !ast.Equal(result, want) {
			return bad()
		}
		return nil
	}
	switch op.Kind {
	case ssa.OpNot:
		if len(op.Args) != 1 || !ast.Equal(arg(0), ast.BoolType{}) || !ast.Equal(result, ast.BoolType{}) {
			return bad()
		}
	case ssa.OpConstInt:
		if _, ok := result.(ast.NumberType); !ok || len(op.Args) != 0 {
			return bad()
		}
	case ssa.OpConstBool:
		if !ast.Equal(result, ast.BoolType{}) || len(op.Args) != 0 || (op.Imm != 0 && op.Imm != 1) {
			return bad()
		}
	case ssa.OpConstString:
		if !ast.Equal(result, ast.StringType{}) || len(op.Args) != 0 {
			return bad()
		}
	case ssa.OpArrayMake:
		a, ok := result.(ast.ArrayType)
		if !ok {
			return bad()
		}
		for i := range op.Args {
			if !ast.Equal(a.Elem, arg(i)) {
				return bad()
			}
		}
	case ssa.OpArrayGet:
		if len(op.Args) != 2 {
			return bad()
		}
		a, ok := arg(0).(ast.ArrayType)
		_, integer := arg(1).(ast.NumberType)
		if !ok || !integer || !ast.Equal(a.Elem, result) {
			return bad()
		}
	case ssa.OpArrayAppend:
		if len(op.Args) != 2 {
			return bad()
		}
		a, ok := arg(0).(ast.ArrayType)
		if !ok || !ast.Equal(arg(0), result) || !ast.Equal(a.Elem, arg(1)) {
			return bad()
		}
	case ssa.OpTupleMake:
		a, ok := result.(ast.TupleType)
		if !ok || len(a.Elems) != len(op.Args) {
			return bad()
		}
		for i, t := range a.Elems {
			if !ast.Equal(t, arg(i)) {
				return bad()
			}
		}
	case ssa.OpTupleGet:
		if len(op.Args) != 1 {
			return bad()
		}
		a, ok := arg(0).(ast.TupleType)
		if !ok || op.Imm < 0 || op.Imm >= int64(len(a.Elems)) || !ast.Equal(a.Elems[op.Imm], result) {
			return bad()
		}
	case ssa.OpPhi:
		if len(op.Args) == 0 {
			return bad()
		}
		for i := range op.Args {
			if !ast.Equal(result, arg(i)) {
				return bad()
			}
		}
	case ssa.OpSemanticCall:
		callee, err := f.callee(op)
		if err != nil {
			return err
		}
		c := callee.contract
		if len(op.Args) != len(c.params) || !ast.Equal(result, c.result) {
			return bad()
		}
		for i, typ := range c.params {
			if !ast.Equal(typ, arg(i)) {
				return bad()
			}
		}
	default:
		return fmt.Errorf("operation is not supported in the typed pre-RC phase")
	}
	return nil
}

// Keep the pilot's supported surface explicit. These are the checker's real
// types, not an alternate vocabulary; unresolved and not-yet-supported types
// are rejected rather than erased to pointer-width integers.
func resolvedType(typ ast.Type, allowVoid bool) error {
	switch t := typ.(type) {
	case ast.NumberType:
		if !t.Polymorphic && (t.Width == 0 || t.Width == 8 || t.Width == 16 || t.Width == 32 || t.Width == 64 || t.Width == ast.WidthPtr) {
			return nil
		}
	case ast.StringType, ast.BoolType:
		return nil
	case ast.VoidType:
		if allowVoid {
			return nil
		}
	case ast.ArrayType:
		return resolvedType(t.Elem, false)
	case ast.TupleType:
		for _, elem := range t.Elems {
			if err := resolvedType(elem, false); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unresolved or unsupported semantic type %T", typ)
}

func referenceBearing(typ ast.Type) bool {
	switch typ.(type) {
	case ast.StringType, ast.ArrayType, ast.TupleType:
		return true
	default:
		return false
	}
}
