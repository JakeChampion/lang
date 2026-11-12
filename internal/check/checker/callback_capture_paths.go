package checker

import (
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// Result selects a callback component. Path selects its source environment.
// Direct loans retain a captured binding instead of its environment loans.
type callbackLoan struct {
	param        ast.Param
	result, path string
	direct, wide bool
}

type callbackPart struct {
	selector string
	value    ast.Expr
	group    string
}

// Repeated reads of one immutable binding share the same environment shape.
func groupCallbackParts(parts []callbackPart) []callbackPart {
	var out []callbackPart
	groups := map[string]int{}
	for _, part := range parts {
		if index, known := groups[part.group]; part.group != "" && known {
			out[index].selector += "|" + part.selector
			continue
		}
		if part.group != "" {
			groups[part.group] = len(out)
		}
		out = append(out, part)
	}
	return out
}

// Nominal callback containment is a least fixed point over declaration edges.
// Shared and recursive type graphs do not require repeated subtree walks.
func (c *checker) callbackContainerTypes() map[handleTypeKey]bool {
	types := map[handleTypeKey]bool{}
	for name := range c.info.Structs {
		types[handleTypeKey{name: name}] = false
	}
	for name := range c.info.Enums {
		types[handleTypeKey{name: name, enum: true}] = false
	}
	for changed := true; changed; {
		changed = false
		for name, decl := range c.info.Structs {
			key := handleTypeKey{name: name}
			if types[key] || decl == nil {
				continue
			}
			for _, field := range decl.Fields {
				if c.callbackContainer(field.Type, types) {
					types[key] = true
					changed = true
					break
				}
			}
		}
		for name, decl := range c.info.Enums {
			key := handleTypeKey{name: name, enum: true}
			if types[key] || decl == nil {
				continue
			}
			for _, variant := range decl.Variants {
				for _, part := range variant.Payloads {
					if c.callbackContainer(part, types) {
						types[key] = true
						changed = true
						break
					}
				}
				if types[key] {
					break
				}
			}
		}
	}
	return types
}

func (c *checker) callbackContainer(t ast.Type, types map[handleTypeKey]bool) bool {
	switch t := t.(type) {
	case *ast.FuncType, ast.ParamType, ast.ProjType, ast.DynTraitType, ast.UnboundType, ast.SelfType, ast.StreamType:
		return true
	case ast.ArrayType:
		return c.callbackContainer(t.Elem, types)
	case ast.SliceType:
		return c.callbackContainer(t.Elem, types)
	case ast.TupleType:
		for _, elem := range t.Elems {
			if c.callbackContainer(elem, types) {
				return true
			}
		}
	case ast.StructType:
		for _, arg := range t.Args {
			if c.callbackContainer(arg, types) {
				return true
			}
		}
		if found, known := types[handleTypeKey{name: t.Name}]; known {
			return found
		}
		return t.Name != "Map" && t.Name != "Cell"
	case ast.EnumType:
		for _, arg := range t.Args {
			if c.callbackContainer(arg, types) {
				return true
			}
		}
		if found, known := types[handleTypeKey{name: t.Name, enum: true}]; known {
			return found
		}
		return t.Name != "Option" && t.Name != "Result"
	}
	return false
}

func mergeCallbackLoans(into, from []callbackLoan) []callbackLoan {
	for _, loan := range from {
		found := false
		for _, old := range into {
			if old.param.Name == loan.param.Name && old.result == loan.result && old.path == loan.path && old.direct == loan.direct && old.wide == loan.wide {
				found = true
				break
			}
		}
		if !found {
			into = append(into, loan)
		}
	}
	return into
}

func prefixCallbackLoans(part string, loans []callbackLoan) []callbackLoan {
	out := make([]callbackLoan, 0, len(loans))
	for _, loan := range loans {
		loan.result = callbackPath(part, loan.result)
		out = append(out, loan)
	}
	return out
}

func projectCallbackLoans(loans []callbackLoan, part string) []callbackLoan {
	var out []callbackLoan
	for _, loan := range loans {
		if loan.result == "" {
			if !loan.wide {
				loan.path = callbackPath(loan.path, part)
			}
		} else {
			head, tail, _ := strings.Cut(loan.result, "/")
			if !callbackSelectorsMatch(head, part) {
				continue
			}
			loan.result = tail
		}
		out = mergeCallbackLoans(out, []callbackLoan{loan})
	}
	return out
}

func selectCallbackLoans(loans []callbackLoan, path string) []callbackLoan {
	if path != "" {
		for _, part := range strings.Split(path, "/") {
			loans = projectCallbackLoans(loans, part)
		}
	}
	return loans
}

func flattenCallbackLoans(loans []callbackLoan) []callbackLoan {
	var out []callbackLoan
	for _, loan := range loans {
		loan.result = ""
		out = mergeCallbackLoans(out, []callbackLoan{loan})
	}
	return out
}

func widenCallbackLoans(loans []callbackLoan) []callbackLoan {
	var out []callbackLoan
	for _, loan := range loans {
		loan.result = ""
		loan.wide = true
		out = mergeCallbackLoans(out, []callbackLoan{loan})
	}
	return out
}

func callbackLoanParams(loans []callbackLoan) []ast.Param {
	var out []ast.Param
	seen := map[string]bool{}
	for _, loan := range loans {
		if !seen[loan.param.Name] {
			seen[loan.param.Name] = true
			out = append(out, loan.param)
		}
	}
	return out
}

func callbackCapturePlace(value ast.Expr, name string, aliases map[string]ast.Expr, active map[string]bool) (string, bool) {
	switch v := value.(type) {
	case *ast.Ident:
		if v.Name == name {
			return "", true
		}
		if alias := aliases[v.Name]; alias != nil && !active[v.Name] {
			active[v.Name] = true
			defer delete(active, v.Name)
			return callbackCapturePlace(alias, name, aliases, active)
		}
	case *ast.FieldAccess:
		path, ok := callbackCapturePlace(v.Target, name, aliases, active)
		return callbackPath(path, callbackFieldSelector(v.Field)), ok
	case *ast.Index:
		path, ok := callbackCapturePlace(v.Array, name, aliases, active)
		return callbackPath(path, "a:*"), ok
	}
	return "", false
}

func callbackPurePlace(value ast.Expr) bool {
	switch v := value.(type) {
	case *ast.Ident:
		return true
	case *ast.FieldAccess:
		return callbackPurePlace(v.Target)
	}
	return false
}

func (c *checker) callbackCaptureAliases(lambda *ast.Lambda) map[string]ast.Expr {
	definitions := map[string][]ast.Expr{}
	addParams := func(params []ast.Param) {
		for _, param := range params {
			definitions[param.Name] = append(definitions[param.Name], nil)
		}
	}
	addParams(lambda.Params)
	ast.Walk(lambda.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.Var:
			var alias ast.Expr
			switch c.info.VarTypes[n].(type) {
			case ast.StructType, ast.TupleType, ast.ArrayType, ast.SliceType:
				if callbackPurePlace(n.Init) {
					alias = n.Init
				}
			}
			definitions[n.Name] = append(definitions[n.Name], alias)
		case *ast.Assign:
			if id, named := n.Target.(*ast.Ident); named {
				definitions[id.Name] = append(definitions[id.Name], nil)
			}
		case *ast.Lambda:
			addParams(n.Params)
		}
		return true
	})
	aliases := map[string]ast.Expr{}
	for name, definitions := range definitions {
		if len(definitions) == 1 && definitions[0] != nil {
			aliases[name] = definitions[0]
		}
	}
	return aliases
}

// A maximal place can contain index expressions below a field projection.
func callbackPlaceIndices(value ast.Expr, visit func(ast.Node) bool) {
	switch v := value.(type) {
	case *ast.FieldAccess:
		callbackPlaceIndices(v.Target, visit)
	case *ast.Index:
		ast.Walk(v.Idx, visit)
		callbackPlaceIndices(v.Array, visit)
	}
}

// Keep maximal places. The object of r.left is not a separate read of r.
func (c *checker) callbackCapturePlaces(lambda *ast.Lambda, name string) []string {
	var out []string
	aliases := c.callbackCaptureAliases(lambda)
	var visit func(ast.Node) bool
	visit = func(node ast.Node) bool {
		if binding, ok := node.(*ast.Var); ok && aliases[binding.Name] != nil {
			if _, selected := callbackCapturePlace(binding.Init, name, aliases, map[string]bool{}); selected {
				return false
			}
		}
		if value, ok := node.(ast.Expr); ok {
			if path, found := callbackCapturePlace(value, name, aliases, map[string]bool{}); found {
				out = append(out, path)
				callbackPlaceIndices(value, visit)
				return false
			}
		}
		return true
	}
	ast.Walk(lambda.Body, visit)
	return out
}

func callbackPath(base, part string) string {
	if base == "" {
		return part
	}
	if part == "" {
		return base
	}
	return base + "/" + part
}
func callbackFieldSelector(field string) string {
	if _, err := strconv.Atoi(field); err == nil {
		return "t:" + field
	}
	return "f:" + field
}
func callbackSelectorsMatch(a, b string) bool {
	if a == b || strings.HasPrefix(a, "a:") && strings.HasPrefix(b, "a:") && (a == "a:*" || b == "a:*") {
		return true
	}
	if !strings.Contains(a, "|") && !strings.Contains(b, "|") {
		return false
	}
	for _, left := range strings.Split(a, "|") {
		for _, right := range strings.Split(b, "|") {
			if left == right {
				return true
			}
		}
	}
	return false
}
func (c *checker) callbackProjectionType(t ast.Type, part string) ast.Type {
	part, _, _ = strings.Cut(part, "|")
	if t == nil {
		return nil
	}
	switch t := t.(type) {
	case *ast.FuncType:
		if part == "r:*" {
			return t.Result
		}
	case ast.StructType:
		if t.Name == "Map" && len(t.Args) == 2 {
			if part == "m:key" {
				return t.Args[0]
			}
			if part == "m:value" {
				return t.Args[1]
			}
		}
		if decl := c.info.Structs[t.Name]; decl != nil {
			for _, field := range decl.Fields {
				if "f:"+field.Name == part {
					return substituteType(field.Type, declArgs(decl.TypeParams, t.Args))
				}
			}
		}
	case ast.TupleType:
		if strings.HasPrefix(part, "t:") {
			if index, err := strconv.Atoi(strings.TrimPrefix(part, "t:")); err == nil && index >= 0 && index < len(t.Elems) {
				return t.Elems[index]
			}
		}
	case ast.ArrayType:
		if part == "a:*" {
			return t.Elem
		}
	case ast.SliceType:
		if part == "a:*" {
			return t.Elem
		}
	case ast.EnumType:
		parts := strings.Split(part, ":")
		if len(parts) == 3 && parts[0] == "v" {
			variant, ve := strconv.Atoi(parts[1])
			slot, se := strconv.Atoi(parts[2])
			if decl := c.info.Enums[t.Name]; decl != nil && ve == nil && se == nil && variant >= 0 && variant < len(decl.Variants) && slot >= 0 && slot < len(decl.Variants[variant].Payloads) {
				return substituteType(decl.Variants[variant].Payloads[slot], declArgs(decl.TypeParams, t.Args))
			}
		}
	}
	return nil
}

// A repeated nominal type folds the remainder into one descendant set. This
// gives recursive containers a finite domain without a numeric iteration cap.
func (c *checker) boundCallbackPath(t ast.Type, path string) (string, bool) {
	if path == "" {
		return "", false
	}
	seen := map[string]bool{}
	prefix := ""
	for _, part := range strings.Split(path, "/") {
		if t == nil {
			return prefix, true
		}
		key := ""
		switch nominal := t.(type) {
		case ast.StructType:
			key = nominal.Name
		case ast.EnumType:
			key = nominal.Name
		}
		if key != "" {
			if seen[key] {
				return prefix, true
			}
			seen[key] = true
		}
		t = c.callbackProjectionType(t, part)
		prefix = callbackPath(prefix, part)
	}
	return prefix, false
}
