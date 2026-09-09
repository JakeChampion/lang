package semir

import (
	"strconv"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

func (b *builder) recordValue(n *ast.StructLit) (ssa.Value, error) {
	typ := ast.StructType{Name: n.TypeName, Args: n.TypeArgs}
	if err := b.fn.program.importRecordTypes(typ, b.info); err != nil {
		return ssa.Value{}, b.errorAt(n.P, err.Error())
	}
	if err := b.fn.resolvedType(typ, false); err != nil {
		return ssa.Value{}, b.errorAt(n.P, err.Error())
	}
	r := b.fn.program.record(typ)
	var base ssa.Value
	if n.Base != nil {
		value, err := b.expr(n.Base)
		if err != nil || value.ended {
			return ssa.Value{}, err
		}
		base = value.value
		if !ast.Equal(typ, b.fn.values[base.ID].typ.source) {
			return ssa.Value{}, b.errorAt(n.P, "record update base has a different nominal type")
		}
	}
	args := make([]ssa.Value, len(r.fields))
	for _, field := range n.Fields {
		index := -1
		for i, declaration := range r.fields {
			if declaration.name == field.Name {
				index = i
				break
			}
		}
		if index < 0 || args[index].IsValid() {
			return ssa.Value{}, b.errorAt(field.NamePos, "unknown or duplicate checked record field: "+field.Name)
		}
		value, err := b.expr(field.Value)
		if err != nil || value.ended {
			return ssa.Value{}, err
		}
		if !ast.Equal(r.fields[index].typ, b.fn.values[value.value.ID].typ.source) {
			return ssa.Value{}, b.errorAt(field.Value.Pos(), "record field differs from its nominal interface")
		}
		args[index] = value.value
	}
	for i, field := range r.fields {
		if args[i].IsValid() {
			continue
		}
		if !base.IsValid() {
			return ssa.Value{}, b.errorAt(n.P, "missing checked record field: "+field.name)
		}
		args[i] = b.fn.addOp(b.current, ssa.OpRecordGet, field.typ, n.P, base)
		b.current.Ops[len(b.current.Ops)-1].Imm = int64(i)
	}
	return b.fn.addOp(b.current, ssa.OpRecordMake, typ, n.P, args...), nil
}

func (b *builder) fieldValue(n *ast.FieldAccess) (ssa.Value, error) {
	value, err := b.expr(n.Target)
	if err != nil || value.ended {
		return ssa.Value{}, err
	}
	typ := b.fn.values[value.value.ID].typ.source
	kind, index := ssa.OpRecordGet, -1
	var fieldType ast.Type
	if tuple, ok := typ.(ast.TupleType); ok {
		kind = ssa.OpTupleGet
		if i, err := strconv.Atoi(n.Field); err == nil && i >= 0 && i < len(tuple.Elems) {
			index, fieldType = i, tuple.Elems[i]
		}
	} else if r := b.fn.program.record(typ); r != nil {
		for i, field := range r.fields {
			if field.name == n.Field {
				index, fieldType = i, field.typ
				break
			}
		}
	}
	if index < 0 {
		return ssa.Value{}, b.errorAt(n.P, "unresolved checked field projection: "+n.Field)
	}
	result := b.fn.addOp(b.current, kind, fieldType, n.P, value.value)
	b.current.Ops[len(b.current.Ops)-1].Imm = int64(index)
	return result, nil
}
