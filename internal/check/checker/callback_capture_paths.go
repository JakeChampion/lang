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

// Only containers that can hold callbacks need environment summaries.
func (c *checker) callbackContainer(t ast.Type, active map[string]bool) bool {
	switch t := t.(type) {
	case *ast.FuncType, ast.ParamType, ast.ProjType, ast.DynTraitType, ast.UnboundType, ast.SelfType:
		return true
	case ast.ArrayType:
		return c.callbackContainer(t.Elem, active)
	case ast.SliceType:
		return c.callbackContainer(t.Elem, active)
	case ast.StreamType:
		return c.callbackContainer(t.Elem, active)
	case ast.TupleType:
		for _, elem := range t.Elems {
			if c.callbackContainer(elem, active) {
				return true
			}
		}
	case ast.StructType:
		for _, arg := range t.Args {
			if c.callbackContainer(arg, active) {
				return true
			}
		}
		if active[t.Name] {
			return false
		}
		active[t.Name] = true
		defer delete(active, t.Name)
		if decl := c.info.Structs[t.Name]; decl != nil {
			for _, field := range decl.Fields {
				if c.callbackContainer(substituteType(field.Type, declArgs(decl.TypeParams, t.Args)), active) {
					return true
				}
			}
		} else {
			return true
		}
	case ast.EnumType:
		for _, arg := range t.Args {
			if c.callbackContainer(arg, active) {
				return true
			}
		}
		if active[t.Name] {
			return false
		}
		active[t.Name] = true
		defer delete(active, t.Name)
		if decl := c.info.Enums[t.Name]; decl != nil {
			for _, variant := range decl.Variants {
				for _, part := range variant.Payloads {
					if c.callbackContainer(substituteType(part, declArgs(decl.TypeParams, t.Args)), active) {
						return true
					}
				}
			}
		} else {
			return true
		}
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

func callbackCapturePlace(value ast.Expr, name string) (string, bool) {
	switch v := value.(type) {
	case *ast.Ident:
		return "", v.Name == name
	case *ast.FieldAccess:
		path, ok := callbackCapturePlace(v.Target, name)
		return callbackPath(path, callbackFieldSelector(v.Field)), ok
	case *ast.Index:
		path, ok := callbackCapturePlace(v.Array, name)
		return callbackPath(path, "a:*"), ok
	}
	return "", false
}

// Keep maximal places. The object of r.left is not a separate read of r.
func callbackCapturePlaces(lambda *ast.Lambda, name string) []string {
	var out []string
	var visit func(ast.Node) bool
	visit = func(node ast.Node) bool {
		if value, ok := node.(ast.Expr); ok {
			if path, found := callbackCapturePlace(value, name); found {
				out = append(out, path)
				if index, indexed := value.(*ast.Index); indexed {
					ast.Walk(index.Idx, visit)
				}
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
	return a == b || strings.HasPrefix(a, "a:") && strings.HasPrefix(b, "a:") && (a == "a:*" || b == "a:*")
}
func (c *checker) callbackProjectionType(t ast.Type, part string) ast.Type {
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
