package checker

import (
	"github.com/jakechampion/lang/internal/syntax/ast"
)

// paramNode is one type parameter of a generic struct or enum.
type paramNode struct {
	decl  string
	index int
}

// paramEdge is one edge of the graph of type parameters. A member of the
// generic type from.decl names a generic type, and the argument that it gives
// to the parameter `to` holds the parameter `from`. The edge grows when the
// argument is larger than the parameter alone.
type paramEdge struct {
	from, to paramNode
	grows    bool
	member   string
	variant  bool
}

// reportExpandingTypes reports E107 for each generic struct or enum that has
// no last instance: a member gives one of its type parameters a larger type,
// and that type comes back to the same parameter.
func (c *checker) reportExpandingTypes(prog *ast.Program) {
	params := map[string][]string{}
	pos := map[string]ast.Position{}
	for _, sd := range prog.Structs {
		if len(sd.TypeParams) > 0 {
			params[sd.Name] = sd.TypeParams
			pos[sd.Name] = sd.P
		}
	}
	for _, ed := range prog.Enums {
		if len(ed.TypeParams) > 0 {
			params[ed.Name] = ed.TypeParams
			pos[ed.Name] = ed.P
		}
	}
	var edges []paramEdge
	var walk func(owner, member string, variant bool, t ast.Type)
	walk = func(owner, member string, variant bool, t ast.Type) {
		name, args := namedTypeRef(t)
		for j, arg := range args {
			if j >= len(params[name]) {
				break
			}
			for i, p := range params[owner] {
				if holdsTypeParam(arg, p) {
					bare, _ := namedTypeRef(arg)
					edges = append(edges, paramEdge{
						from: paramNode{owner, i}, to: paramNode{name, j},
						grows: bare != p || len(typeChildren(arg)) > 0, member: member, variant: variant,
					})
				}
			}
		}
		for _, child := range typeChildren(t) {
			walk(owner, member, variant, child)
		}
	}
	for _, sd := range prog.Structs {
		if len(sd.TypeParams) > 0 {
			for _, f := range sd.Fields {
				walk(sd.Name, f.Name, false, f.Type)
			}
		}
	}
	for _, ed := range prog.Enums {
		if len(ed.TypeParams) > 0 {
			for _, v := range ed.Variants {
				for _, payload := range v.Payloads {
					walk(ed.Name, v.Name, true, payload)
				}
			}
		}
	}
	reaches := func(from, to paramNode) bool {
		seen := map[paramNode]bool{from: true}
		for grew := true; grew; {
			grew = false
			for _, e := range edges {
				if seen[e.from] && !seen[e.to] {
					seen[e.to] = true
					grew = true
				}
			}
		}
		return seen[to]
	}
	reported := map[string]bool{}
	for _, e := range edges {
		owner := e.from.decl
		if !e.grows || reported[owner] || !reaches(e.to, e.from) {
			continue
		}
		reported[owner] = true
		param := params[owner][e.from.index]
		if e.variant {
			c.errfCode(pos[owner], "E107", "generic enum %q has no last instance: variant %q gives the type parameter %q a larger type each time", demangle(owner), e.member, param)
			continue
		}
		c.errfCode(pos[owner], "E107", "generic struct %q has no last instance: field %q gives the type parameter %q a larger type each time", demangle(owner), e.member, param)
	}
}

// namedTypeRef is the name and the type arguments of a struct, enum or type
// parameter reference. The name is empty for each other type.
func namedTypeRef(t ast.Type) (string, []ast.Type) {
	switch x := t.(type) {
	case ast.StructType:
		return x.Name, x.Args
	case ast.EnumType:
		return x.Name, x.Args
	case ast.ParamType:
		return x.Name, nil
	}
	return "", nil
}

func holdsTypeParam(t ast.Type, param string) bool {
	if name, args := namedTypeRef(t); name == param && len(args) == 0 {
		return true
	}
	for _, child := range typeChildren(t) {
		if holdsTypeParam(child, param) {
			return true
		}
	}
	return false
}
