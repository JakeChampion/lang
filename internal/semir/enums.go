package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
)

type enumContract struct {
	owner    *Program
	typ      ast.EnumType
	variants []enumVariant
	fields   []enumField
}

type enumVariant struct {
	name         string
	first, count int
	pos          ast.Position
}

type enumField struct {
	variant, index int
	name           string
	typ            ast.Type
	pos            ast.Position
}

func (p *Program) enum(typ ast.Type) *enumContract {
	t, ok := typ.(ast.EnumType)
	if !ok || p == nil {
		return nil
	}
	return p.enums[t.String()]
}

func (v *typeVerifier) checkEnum(t ast.EnumType) error {
	e := v.program.enum(t)
	if e == nil || e.owner != v.program || e.typ.Name == "" || !ast.Equal(e.typ, t) || len(e.variants) == 0 {
		return fmt.Errorf("invalid or unresolved nominal enum interface %s", t)
	}
	key := t.String()
	if v.enums[key] != 0 {
		return nil
	}
	if v.enums == nil {
		v.enums = make(map[string]uint8)
	}
	v.enums[key] = 1
	for _, arg := range e.typ.Args {
		if err := v.check(arg, false); err != nil {
			return fmt.Errorf("enum %s argument: %w", t, err)
		}
	}
	names, next := make(map[string]bool), 0
	for i, variant := range e.variants {
		if variant.name == "" || names[variant.name] || variant.first != next || variant.count < 0 || variant.count > len(e.fields)-next {
			return fmt.Errorf("enum %s: invalid variant identity or field range", t)
		}
		names[variant.name] = true
		fieldNames := make(map[string]bool)
		for j := range variant.count {
			field := e.fields[next+j]
			if field.variant != i || field.index != j || field.name != "" && fieldNames[field.name] {
				return fmt.Errorf("enum %s: invalid payload identity", t)
			}
			fieldNames[field.name] = true
			if err := v.check(field.typ, false); err != nil {
				return fmt.Errorf("enum %s variant %s at %d:%d: %w", t, variant.name, field.pos.Line, field.pos.Col, err)
			}
		}
		next += variant.count
	}
	if next != len(e.fields) {
		return fmt.Errorf("enum %s: payload outside variant interface", t)
	}
	v.enums[key] = 2
	return nil
}

// Enum type arguments are mutable frontend slices. Semantic values borrow the
// program's copied nominal type, never the checker's original argument tree.
func (p *Program) canonicalType(typ ast.Type) ast.Type {
	switch t := typ.(type) {
	case ast.EnumType:
		if e := p.enum(t); e != nil {
			return e.typ
		}
	case ast.ArrayType:
		if containsEnumType(t.Elem) {
			return ast.ArrayType{Elem: p.canonicalType(t.Elem)}
		}
	case ast.TupleType:
		if containsEnumType(t) {
			elems := make([]ast.Type, len(t.Elems))
			for i, elem := range t.Elems {
				elems[i] = p.canonicalType(elem)
			}
			return ast.TupleType{Elems: elems}
		}
	}
	return typ
}

func containsEnumType(typ ast.Type) bool {
	switch t := typ.(type) {
	case ast.EnumType:
		return true
	case ast.ArrayType:
		return containsEnumType(t.Elem)
	case ast.TupleType:
		for _, elem := range t.Elems {
			if containsEnumType(elem) {
				return true
			}
		}
	}
	return false
}
