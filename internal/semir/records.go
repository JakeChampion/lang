package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
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

// Import only used declarations. Unused builtin and generic declarations must
// not widen or obstruct the pilot's explicitly supported type surface.
func (p *Program) importRecordTypes(typ ast.Type, info *checker.Info) error {
	if !containsRecordType(typ) {
		return nil
	}
	var copyType func(ast.Type) (ast.Type, error)
	var pending map[string]*recordContract
	copyType = func(typ ast.Type) (ast.Type, error) {
		switch t := typ.(type) {
		case ast.ArrayType:
			elem, err := copyType(t.Elem)
			return ast.ArrayType{Elem: elem}, err
		case ast.TupleType:
			elems := make([]ast.Type, len(t.Elems))
			for i, elem := range t.Elems {
				var err error
				elems[i], err = copyType(elem)
				if err != nil {
					return nil, err
				}
			}
			return ast.TupleType{Elems: elems}, nil
		case ast.StructType:
			if len(t.Args) != 0 {
				return nil, fmt.Errorf("semir record %s: monomorphization must precede typed construction", t.Name)
			}
			if p.records[t.Name] != nil || pending[t.Name] != nil {
				return t, nil
			}
			decl := info.Structs[t.Name]
			if decl == nil || decl.Name != t.Name || len(decl.TypeParams) != 0 {
				return nil, fmt.Errorf("semir record %s: missing concrete checked field interface", t.Name)
			}
			if pending == nil {
				pending = make(map[string]*recordContract)
			}
			r := &recordContract{owner: p, name: t.Name, fields: make([]recordField, len(decl.Fields))}
			pending[t.Name] = r
			for i, field := range decl.Fields {
				ft, err := copyType(field.Type)
				if err != nil {
					return nil, err
				}
				r.fields[i] = recordField{field.Name, ft, field.NamePos}
			}
			return t, nil
		default:
			return typ, nil
		}
	}
	_, err := copyType(typ)
	if err != nil {
		return err
	}
	// Publish only complete interfaces. Back edges name the pending nominal
	// contract, while any failed import leaves the existing catalogue intact.
	if len(pending) != 0 && p.records == nil {
		p.records = make(map[string]*recordContract, len(pending))
	}
	for name, r := range pending {
		p.records[name] = r
	}
	return nil
}

// A verifier-local traversal memo is safe only while the input is unchanged.
// It is never retained as a successful-verification certificate on the IR.
type typeVerifier struct {
	program *Program
	records map[string]uint8
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

func containsRecordType(typ ast.Type) bool {
	switch t := typ.(type) {
	case ast.StructType:
		return true
	case ast.ArrayType:
		return containsRecordType(t.Elem)
	case ast.TupleType:
		for _, elem := range t.Elems {
			if containsRecordType(elem) {
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
}

func (s aggregateShape) len() int {
	return len(s.tuple) + len(s.record)
}

func (s aggregateShape) at(i int) ast.Type {
	if s.record != nil {
		return s.record[i].typ
	}
	return s.tuple[i]
}

func (p *Program) aggregateFields(typ ast.Type) aggregateShape {
	if tuple, ok := typ.(ast.TupleType); ok {
		return aggregateShape{tuple: tuple.Elems}
	}
	r := p.record(typ)
	if r == nil {
		return aggregateShape{}
	}
	return aggregateShape{record: r.fields}
}
