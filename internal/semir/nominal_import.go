package semir

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// Import used record and enum declarations in one transaction. Back edges may
// cross between both kinds; neither catalogue can publish an incomplete peer.
func (p *Program) importNominalTypes(root ast.Type, info *checker.Info) error {
	if !containsNominalType(root) {
		return nil
	}
	var records map[string]*recordContract
	var enums map[string]*enumContract
	checkedExpansion := false
	var copyType func(ast.Type, map[string]ast.Type) (ast.Type, error)
	copyType = func(typ ast.Type, sub map[string]ast.Type) (ast.Type, error) {
		switch t := typ.(type) {
		case ast.ParamType:
			if concrete := sub[t.Name]; concrete != nil {
				return copyType(concrete, nil)
			}
			return nil, fmt.Errorf("semir: unresolved nominal type parameter %s", t.Name)
		case ast.ArrayType:
			elem, err := copyType(t.Elem, sub)
			return ast.ArrayType{Elem: elem}, err
		case ast.TupleType:
			elems := make([]ast.Type, len(t.Elems))
			for i, elem := range t.Elems {
				var err error
				elems[i], err = copyType(elem, sub)
				if err != nil {
					return nil, err
				}
			}
			return ast.TupleType{Elems: elems}, nil
		case ast.StructType:
			if len(t.Args) != 0 {
				return nil, fmt.Errorf("semir record %s: monomorphization must precede typed construction", t.Name)
			}
			if p.records[t.Name] != nil || records[t.Name] != nil {
				return t, nil
			}
			decl := info.Structs[t.Name]
			if decl == nil || decl.Name != t.Name || len(decl.TypeParams) != 0 {
				return nil, fmt.Errorf("semir record %s: missing concrete checked field interface", t.Name)
			}
			if records == nil {
				records = make(map[string]*recordContract)
			}
			r := &recordContract{owner: p, name: t.Name, fields: make([]recordField, len(decl.Fields))}
			records[t.Name] = r
			for i, field := range decl.Fields {
				ft, err := copyType(field.Type, nil)
				if err != nil {
					return nil, err
				}
				r.fields[i] = recordField{field.Name, ft, field.NamePos}
			}
			return t, nil
		case ast.EnumType:
			if existing := p.enum(t); existing != nil {
				return existing.typ, nil
			}
			decl := info.Enums[t.Name]
			if decl == nil || decl.Name != t.Name || len(decl.TypeParams) != len(t.Args) {
				return nil, fmt.Errorf("semir enum %s: incomplete checked type arguments", t.Name)
			}
			if !checkedExpansion {
				if err := verifyFiniteEnumExpansion(root, info); err != nil {
					return nil, err
				}
				checkedExpansion = true
			}
			args := make([]ast.Type, len(t.Args))
			for i, arg := range t.Args {
				var err error
				args[i], err = copyType(arg, sub)
				if err != nil {
					return nil, err
				}
			}
			t = ast.EnumType{Name: t.Name, Args: args}
			key := t.String()
			if e := p.enums[key]; e != nil {
				return e.typ, nil
			}
			if e := enums[key]; e != nil {
				return e.typ, nil
			}
			if enums == nil {
				enums = make(map[string]*enumContract)
			}
			e := &enumContract{owner: p, typ: t, variants: make([]enumVariant, len(decl.Variants))}
			enums[key] = e
			params := make(map[string]ast.Type, len(args))
			for i, name := range decl.TypeParams {
				params[name] = args[i]
			}
			count := 0
			for _, variant := range decl.Variants {
				count += len(variant.Payloads)
			}
			e.fields = make([]enumField, 0, count)
			for i, variant := range decl.Variants {
				e.variants[i] = enumVariant{name: variant.Name, first: len(e.fields), count: len(variant.Payloads), pos: variant.P}
				for j, payload := range variant.Payloads {
					ft, err := copyType(payload, params)
					if err != nil {
						return nil, err
					}
					name := ""
					if j < len(variant.FieldNames) {
						name = variant.FieldNames[j]
					}
					e.fields = append(e.fields, enumField{variant: i, index: j, name: name, typ: ft, pos: variant.P})
				}
			}
			return t, nil
		default:
			return typ, nil
		}
	}
	if _, err := copyType(root, nil); err != nil {
		return err
	}
	if len(records) != 0 && p.records == nil {
		p.records = make(map[string]*recordContract, len(records))
	}
	for name, r := range records {
		p.records[name] = r
	}
	if len(enums) != 0 && p.enums == nil {
		p.enums = make(map[string]*enumContract, len(enums))
	}
	for key, e := range enums {
		p.enums[key] = e
	}
	return nil
}
