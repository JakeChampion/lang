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
	return verifyWithFacts(f, nil)
}

// Transformation-local facts reuse the verifier's dominance and initialization
// walks. They are never stored on IR or reused across graph mutation.
type verificationFacts struct {
	conditional *cleanupRegion
	deadBlocks  map[*ssa.Block]bool
}

func verifyWithFacts(f *Func, facts *verificationFacts) error {
	if f == nil || f.graph == nil {
		return fmt.Errorf("semir: nil function")
	}
	g := f.graph
	if f.unexpandedCleanups && !f.unpromotedBindings {
		return fmt.Errorf("semir %s: pending cleanup actions require binding places", g.Name)
	}
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
	hasAvailability := false
	hasGuardedWrites := false
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
			if err := resolvedType(info.typ.source, false); err != nil {
				return fail("%v: %v", v, err)
			}
			if info.typ.form != sourceForm && info.typ.form != availabilityForm {
				return fail("%v: invalid semantic type form", v)
			}
			hasAvailability = hasAvailability || info.typ.form == availabilityForm
		}
		return nil
	}
	for i, p := range g.Params {
		if err := checkValue(p, true); err != nil {
			return err
		}
		if f.values[p.ID].typ.form != sourceForm {
			return fail("internal availability state cannot be a source parameter")
		}
		ref := referenceBearing(f.values[p.ID].typ.source)
		mode := f.modes[i]
		if (ref && mode != ParamBorrow && mode != ParamCounted) || (!ref && mode != ParamValue) {
			return fail("parameter %v has invalid ownership mode %d", p, mode)
		}
	}
	effectCount := 0
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
			if op.Result == (ssa.Value{}) {
				if op.Kind != ssa.OpSemanticCall && op.Kind != ssa.OpBindingInit && op.Kind != ssa.OpBindingReplace && op.Kind != ssa.OpBindingReplaceGuarded {
					return fail("only void semantic calls and binding writes may omit their result")
				}
				if _, ok := f.effectPositions[op]; !ok {
					return fail("effect-only operation has no source metadata")
				}
				effectCount++
			} else if err := checkValue(op.Result, true); err != nil {
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
			hasGuardedWrites = hasGuardedWrites || op.Kind == ssa.OpBindingReplaceGuarded
		}
		var used ssa.Value
		switch b.Term.Kind {
		case ssa.TermBr:
		case ssa.TermBrIf:
			used = b.Term.Cond
			if f.values[used.ID].typ.form != sourceForm || !ast.Equal(f.values[used.ID].typ.source, ast.BoolType{}) {
				return fail("branch condition is not boolean")
			}
		case ssa.TermRet:
			used = b.Term.Value
			if _, void := f.result.(ast.VoidType); void {
				if used != (ssa.Value{}) {
					return fail("void function returns a value")
				}
			} else if f.values[used.ID].typ.form != sourceForm || !ast.Equal(f.values[used.ID].typ.source, f.result) {
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
	if effectCount != len(f.effectPositions) {
		return fail("stale effect-only source metadata")
	}
	knownBoundaries := make(map[*cleanupBoundary]bool, len(f.boundaries))
	for _, boundary := range f.boundaries {
		knownBoundaries[boundary] = true
	}
	for _, b := range f.bindings {
		if err := resolvedType(b.typ, false); err != nil {
			return fail("binding %q: %v", b.name, err)
		}
		// Boundary membership is checked here; verifyCleanups independently
		// validates every listed boundary's owner before any lifetime transfer.
		if (b.boundary == nil && len(f.boundaries) != 0) ||
			(b.boundary != nil && !knownBoundaries[b.boundary]) {
			return fail("binding %q at %d:%d has a missing or foreign lifetime boundary", b.name, b.pos.Line, b.pos.Col)
		}
	}
	if err := ssa.Verify(g); err != nil {
		return err
	}
	if hasAvailability {
		if err := verifyStateGuards(f); err != nil {
			return err
		}
	}
	conditional, err := verifyCleanups(f)
	if err != nil {
		return err
	}
	if f.unpromotedBindings {
		if hasGuardedWrites {
			if err := verifyGuardedBindingWrites(f); err != nil {
				return err
			}
		}
		var deadBlocks *map[*ssa.Block]bool
		if facts != nil {
			deadBlocks = &facts.deadBlocks
		}
		if err := verifyBindingInitialization(f, conditional == nil, deadBlocks); err != nil {
			return err
		}
	}
	if facts != nil {
		facts.conditional = conditional
	}
	return nil
}

func verifyOp(f *Func, op *ssa.Op) error {
	if op.Kind == ssa.OpBindingReplaceGuarded {
		if !f.unpromotedBindings || op.Imm <= 0 || op.Imm > int64(len(f.bindings)) {
			return fmt.Errorf("guarded binding replacement outside its phase or invalid identity")
		}
		typ := f.bindings[op.Imm-1].typ
		if op.Result.IsValid() || len(op.Args) != 2 || op.Str != "" || op.F64 != 0 ||
			!sameValueType(f.values[op.Args[0].ID].typ, valueType{source: typ, form: availabilityForm}) ||
			!sameValueType(f.values[op.Args[1].ID].typ, sourceValueType(typ)) {
			return fmt.Errorf("invalid guarded binding replacement operands or types")
		}
		return nil
	}
	if stateOp(op.Kind) {
		return verifyStateOp(f, op)
	}
	if op.Kind == ssa.OpBindingSnapshot {
		if !f.unpromotedBindings || op.Imm <= 0 || op.Imm > int64(len(f.bindings)) {
			return fmt.Errorf("binding snapshot outside its phase or invalid identity")
		}
		want := valueType{source: f.bindings[op.Imm-1].typ, form: availabilityForm}
		if !op.Result.IsValid() || len(op.Args) != 0 || op.Str != "" || op.F64 != 0 || !sameValueType(f.values[op.Result.ID].typ, want) {
			return fmt.Errorf("invalid binding snapshot type or operands")
		}
		return nil
	}
	typ := f.values[op.Result.ID].typ
	if op.Kind == ssa.OpPhi {
		if len(op.Args) == 0 {
			return fmt.Errorf("invalid operand/result types or arity: empty phi")
		}
		for _, arg := range op.Args {
			if !sameValueType(typ, f.values[arg.ID].typ) {
				return fmt.Errorf("phi operands differ in semantic type or availability form")
			}
		}
		return nil
	}
	if typ.form != sourceForm {
		return fmt.Errorf("ordinary operation cannot produce an availability state")
	}
	for _, arg := range op.Args {
		if f.values[arg.ID].typ.form != sourceForm {
			return fmt.Errorf("ordinary operation cannot consume an availability state")
		}
	}
	result := f.values[op.Result.ID].typ.source
	arg := func(i int) ast.Type { return f.values[op.Args[i].ID].typ.source }
	bad := func() error { return fmt.Errorf("invalid operand/result types or arity") }
	if bindingOp(op.Kind) {
		if !f.unpromotedBindings || op.Imm <= 0 || op.Imm > int64(len(f.bindings)) {
			return fmt.Errorf("binding operation outside its phase or invalid identity")
		}
		typ := f.bindings[op.Imm-1].typ
		if op.Kind == ssa.OpBindingRead {
			if len(op.Args) != 0 || !ast.Equal(result, typ) {
				return bad()
			}
		} else if op.Result.IsValid() || len(op.Args) != 1 || !ast.Equal(arg(0), typ) {
			return bad()
		}
		return nil
	}
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
	case ssa.OpNeg:
		if len(op.Args) != 1 || !ast.Equal(arg(0), ast.NumberType{}) || !ast.Equal(result, ast.NumberType{}) {
			return bad()
		}
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
	case ssa.OpSemanticCall:
		callee, err := f.callee(op)
		if err != nil {
			return err
		}
		c := callee.contract
		_, void := c.result.(ast.VoidType)
		if len(op.Args) != len(c.params) || (void && op.Result != (ssa.Value{})) ||
			(!void && (!op.Result.IsValid() || !ast.Equal(result, c.result))) {
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
