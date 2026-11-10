package checker

import (
	"fmt"
	"github.com/jakechampion/lang/internal/syntax/ast"
)

func (c *checker) valueType(name string) bool {
	d := c.info.Structs[name]
	return d != nil && d.ValueType
}

func (c *checker) checkValueTypes(prog *ast.Program) {
	for _, d := range prog.Structs {
		if d.ValueType {
			delete(c.consumingTypes, d.Name)
		}
	}
	c.callCopyTypes = nil
	for _, d := range prog.Structs {
		if !d.ValueType {
			continue
		}
		for _, field := range d.Fields {
			if !c.valueData(field.Type, map[string]bool{}) {
				c.errfCode(paramPos(field, d.P), "E051", "@value field %q must contain only immutable copyable data", field.Name)
			}
		}
	}
}

// Call checks revisit the same closed types across a module. Keep complete
// structural keys so a type parameter cannot inherit a nominal type's proof.
func (c *checker) copyableCallResult(t ast.Type) bool {
	if t == nil {
		return false
	}
	key := fmt.Sprintf("%#v", t)
	if copyable, found := c.callCopyTypes[key]; found {
		return copyable
	}
	copyable := c.valueData(t, map[string]bool{})
	if c.callCopyTypes == nil {
		c.callCopyTypes = map[string]bool{}
	}
	c.callCopyTypes[key] = copyable
	return copyable
}

func (c *checker) valueData(t ast.Type, seen map[string]bool) bool {
	if t == nil || !c.holdsNoHandle(t) {
		return false
	}
	switch x := t.(type) {
	case *ast.FuncType:
		return false
	case ast.ArrayType:
		return c.valueData(x.Elem, seen)
	case ast.SliceType, ast.StrType:
		return false
	case ast.TupleType:
		for _, part := range x.Elems {
			if !c.valueData(part, seen) {
				return false
			}
		}
	case ast.EnumType:
		for _, part := range x.Args {
			if !c.valueData(part, seen) {
				return false
			}
		}
		if x.Name == "Option" || x.Name == "Result" || seen[x.Name] {
			return true
		}
		d := c.info.Enums[x.Name]
		if d == nil || d.MustConsume {
			return false
		}
		seen[x.Name] = true
		for _, variant := range d.Variants {
			for _, part := range variant.Payloads {
				if !c.valueData(substituteType(part, declArgs(d.TypeParams, x.Args)), seen) {
					return false
				}
			}
		}
	case ast.StructType:
		if x.Name == "Cell" {
			return false
		}
		for _, part := range x.Args {
			if !c.valueData(part, seen) {
				return false
			}
		}
		d := c.info.Structs[x.Name]
		if d == nil || d.MustConsume {
			return false
		}
		if seen[x.Name] {
			return true
		}
		seen[x.Name] = true
		for _, field := range d.Fields {
			if !c.valueData(substituteType(field.Type, declArgs(d.TypeParams, x.Args)), seen) {
				return false
			}
		}
	}
	return true
}
