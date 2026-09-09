package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
)

// A record interface belongs to this typed program, not to a retained AST
// declaration. Field ordinals refer to this ordered, nominal interface.
type recordContract struct {
	owner  *Program
	name   string
	fields []recordField
}

type recordField struct {
	name string
	typ  ast.Type
	pos  ast.Position
}

func (p *Program) record(typ ast.Type) *recordContract {
	t, ok := typ.(ast.StructType)
	if !ok || len(t.Args) != 0 || p == nil {
		return nil
	}
	return p.records[t.Name]
}

// A verifier-local traversal memo is safe only while the input is unchanged.
// It is never retained as a successful-verification certificate on the IR.
type typeVerifier struct {
	program *Program
	records map[string]uint8
	enums   map[string]uint8
}

func (v *typeVerifier) checkRecord(t ast.StructType) error {
	r := v.program.record(t)
	if r == nil || r.owner != v.program || r.name != t.Name || r.name == "" {
		return fmt.Errorf("invalid or unresolved nominal record interface %s", t)
	}
	switch v.records[t.Name] {
	case 1:
		// A nominal back edge is valid. The active caller still verifies all
		// remaining fields before marking this interface complete.
		return nil
	case 2:
		return nil
	}
	if v.records == nil {
		v.records = make(map[string]uint8)
	}
	v.records[t.Name] = 1
	names := make(map[string]bool, len(r.fields))
	for _, field := range r.fields {
		if field.name == "" || names[field.name] {
			return fmt.Errorf("record %s: empty or duplicate field identity", t)
		}
		names[field.name] = true
		if err := v.check(field.typ, false); err != nil {
			return fmt.Errorf("record %s field %s at %d:%d: %w", t, field.name, field.pos.Line, field.pos.Col, err)
		}
	}
	v.records[t.Name] = 2
	return nil
}

func (f *Func) resolvedType(typ ast.Type, allowVoid bool) error {
	v := typeVerifier{program: f.program}
	return v.check(typ, allowVoid)
}

func containsNominalType(typ ast.Type) bool {
	switch t := typ.(type) {
	case ast.StructType, ast.EnumType:
		return true
	case ast.ArrayType:
		return containsNominalType(t.Elem)
	case ast.TupleType:
		for _, elem := range t.Elems {
			if containsNominalType(elem) {
				return true
			}
		}
	}
	return false
}

// Borrow the existing typed field interface. Do not allocate a temporary type
// slice for every projection, flow node and physical layout query.
type aggregateShape struct {
	tuple  []ast.Type
	record []recordField
	enum   []enumField
}

func (s aggregateShape) len() int {
	return len(s.tuple) + len(s.record) + len(s.enum)
}

func (s aggregateShape) at(i int) ast.Type {
	if s.record != nil {
		return s.record[i].typ
	}
	if s.enum != nil {
		return s.enum[i].typ
	}
	return s.tuple[i]
}

func (p *Program) aggregateFields(typ ast.Type) aggregateShape {
	if tuple, ok := typ.(ast.TupleType); ok {
		return aggregateShape{tuple: tuple.Elems}
	}
	if e := p.enum(typ); e != nil {
		return aggregateShape{enum: e.fields}
	}
	r := p.record(typ)
	if r == nil {
		return aggregateShape{}
	}
	return aggregateShape{record: r.fields}
}
