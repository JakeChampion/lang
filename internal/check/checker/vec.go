package checker

import (
	"slices"
	"sort"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// `Vec[N, T]` is the second spelling of a lane type. `Vec[2, f64]` is f64x2
// and `Vec[4, f32]` is f32x4. A `Vec` whose arguments name a type parameter
// is a lane type that each instance picks.

var vecTable = []ast.LanesType{{Width: 64, Spelling: "f64x2"}, {Width: 32, Spelling: "f32x4"}}

// vecLanes is the lane type that `Vec[n, elem]` names on the portable tier.
func vecLanes(args []ast.Type) (ast.LanesType, bool) {
	if len(args) != 2 {
		return ast.LanesType{}, false
	}
	n, nok := args[0].(ast.ConstArg)
	f, fok := args[1].(ast.FloatType)
	if !nok || !fok {
		return ast.LanesType{}, false
	}
	for _, l := range vecTable {
		if n.Value == int64(l.Lanes()) && f.NormalWidth() == l.Width {
			return l, true
		}
	}
	return ast.LanesType{}, false
}

// vecLaneTypes are the lane types that a `Vec[N, T]` can stand for.
func vecLaneTypes(args []ast.Type) []ast.LanesType {
	var out []ast.LanesType
	for _, l := range vecTable {
		if vecArgFits(args[0], ast.ConstArg{Value: int64(l.Lanes())}) && vecArgFits(args[1], ast.FloatType{Width: l.Width}) {
			out = append(out, l)
		}
	}
	return out
}

func vecArgFits(a, want ast.Type) bool {
	switch x := a.(type) {
	case ast.ParamType:
		return true
	case ast.ConstArg:
		w, ok := want.(ast.ConstArg)
		return ok && w.Value == x.Value
	case ast.FloatType:
		w, ok := want.(ast.FloatType)
		return ok && w.NormalWidth() == x.NormalWidth()
	}
	return false
}

// isBuiltinVec is a `Vec` with two arguments where no declared type is named Vec.
func (c *checker) isBuiltinVec(name string, args []ast.Type) bool {
	if name != "Vec" || len(args) != 2 {
		return false
	}
	_, s := c.info.Structs[name]
	_, e := c.info.Enums[name]
	_, t := c.info.Traits[name]
	return !s && !e && !t
}

// genericVec is the lane types a `Vec[N, T]` that names a type parameter can
// stand for.
func (c *checker) genericVec(t ast.Type) ([]ast.LanesType, bool) {
	st, ok := t.(ast.StructType)
	if !ok || !c.isBuiltinVec(st.Name, st.Args) || !containsParamType(st) {
		return nil, false
	}
	return vecLaneTypes(st.Args), true
}

// vecTraits are the traits that every lane type in lanes implements.
func (c *checker) vecTraits(lanes []ast.LanesType) []string {
	var out []string
	for trait, types := range c.info.Impls {
		all := len(lanes) > 0
		for _, l := range lanes {
			if !types[l.Spelling] {
				all = false
			}
		}
		if all {
			out = append(out, trait)
		}
	}
	sort.Strings(out)
	return out
}

// resolveTraitMethodForVec finds a method of a generic `Vec` among the traits
// that every lane type it can stand for implements.
func (c *checker) resolveTraitMethodForVec(lanes []ast.LanesType, self ast.Type, field string) (ast.TraitMethod, string, bool) {
	names := c.vecTraits(lanes)
	return c.traitMethodIn(c.expandTraitRequirements(names, make([][]ast.Type, len(names)), self), field)
}

// expandVecImpls replaces an impl for a generic `Vec[N, T]` with one impl per
// lane type the spelling can stand for, with N and T bound in its methods. A
// program that declares its own `Vec` keeps the impl as written.
func expandVecImpls(prog *ast.Program) {
	if declaresVec(prog) {
		return
	}
	var impls []*ast.ImplDecl
	parts := map[*ast.FuncDecl][]*ast.FuncDecl{}
	for _, impl := range prog.Impls {
		lanes, fits := vecImplParts(impl)
		if !fits {
			for _, m := range impl.Methods {
				parts[m] = nil
			}
		}
		if len(lanes) == 0 {
			impls = append(impls, impl)
			continue
		}
		impls = append(impls, lanes...)
		for _, part := range lanes {
			for i, m := range impl.Methods {
				parts[m] = append(parts[m], part.Methods[i])
			}
		}
	}
	if len(parts) == 0 {
		return
	}
	prog.Impls = impls
	var funcs []*ast.FuncDecl
	for _, fn := range prog.Funcs {
		if ms, ok := parts[fn]; ok {
			funcs = append(funcs, ms...)
			continue
		}
		funcs = append(funcs, fn)
	}
	prog.Funcs = funcs
}

func declaresVec(prog *ast.Program) bool {
	for _, sd := range prog.Structs {
		if sd.Name == "Vec" {
			return true
		}
	}
	for _, ed := range prog.Enums {
		if ed.Name == "Vec" {
			return true
		}
	}
	for _, ud := range prog.Unions {
		if ud.Name == "Vec" {
			return true
		}
	}
	for _, td := range prog.Traits {
		if td.Name == "Vec" {
			return true
		}
	}
	return false
}

// vecImplParts is one impl per lane type that a generic `Vec` target can
// stand for. It reports whether any lane type fits the target, so a concrete
// pair, which the checker resolves to its lane type, is not refused here.
func vecImplParts(impl *ast.ImplDecl) ([]*ast.ImplDecl, bool) {
	name, args := namedApplication(impl.Type)
	if name != "Vec" || len(args) != 2 {
		return nil, true
	}
	var out []*ast.ImplDecl
	fits := false
	for _, l := range vecTable {
		sub, ok := vecImplBinding(args, impl.TypeParams, l)
		fits = fits || ok
		if len(sub) > 0 {
			out = append(out, boundVecImpl(impl, l, sub))
		}
	}
	return out, fits
}

// vecImplBinding binds the impl parameters that args names to the count and
// element of l, and reports whether l fits args.
func vecImplBinding(args []ast.Type, tparams []string, l ast.LanesType) (map[string]ast.Type, bool) {
	want := []ast.Type{ast.ConstArg{Value: int64(l.Lanes())}, ast.FloatType{Width: l.Width}}
	sub := map[string]ast.Type{}
	for k, a := range args {
		name, named := implParamName(a, tparams)
		if !named {
			if !vecArgFits(a, want[k]) {
				return nil, false
			}
			continue
		}
		if prev, ok := sub[name]; ok && !ast.Equal(prev, want[k]) {
			return nil, false
		}
		sub[name] = want[k]
	}
	return sub, true
}

// namedApplication is the name and arguments of a parsed `Name[Args]`, which
// the parser spells as a struct or an enum type until the checker resolves it.
func namedApplication(t ast.Type) (string, []ast.Type) {
	switch x := t.(type) {
	case ast.StructType:
		return x.Name, x.Args
	case ast.EnumType:
		return x.Name, x.Args
	}
	return "", nil
}

func implParamName(t ast.Type, tparams []string) (string, bool) {
	switch x := t.(type) {
	case ast.ParamType:
		return x.Name, slices.Contains(tparams, x.Name)
	case ast.StructType:
		return x.Name, len(x.Args) == 0 && slices.Contains(tparams, x.Name)
	}
	return "", false
}

func boundVecImpl(impl *ast.ImplDecl, l ast.LanesType, sub map[string]ast.Type) *ast.ImplDecl {
	c := *impl
	c.Type = l
	c.TypeParams = withoutBound(impl.TypeParams, sub)
	c.ConstParams = withoutBound(impl.ConstParams, sub)
	c.Bounds = boundsWithout(impl.Bounds, sub)
	c.BoundArgs = boundArgsWithout(impl.BoundArgs, sub)
	if impl.AssocTypeBindings != nil {
		c.AssocTypeBindings = map[string]ast.Type{}
		for k, v := range impl.AssocTypeBindings {
			c.AssocTypeBindings[k] = substByName(v, sub)
		}
	}
	c.Methods = make([]*ast.FuncDecl, len(impl.Methods))
	for i, m := range impl.Methods {
		c.Methods[i] = boundVecMethod(m, l, sub)
	}
	return &c
}

func boundVecMethod(m *ast.FuncDecl, l ast.LanesType, sub map[string]ast.Type) *ast.FuncDecl {
	c := *m
	c.Params = append([]ast.Param(nil), m.Params...)
	if m.Receiver != nil {
		r := *m.Receiver
		r.Type = l
		c.Receiver = &r
	}
	if c.AssocType != "" {
		c.AssocType = l.Spelling
	}
	c.TypeParams = withoutBound(m.TypeParams, sub)
	c.ConstParams = withoutBound(m.ConstParams, sub)
	c.Bounds = boundsWithout(m.Bounds, sub)
	c.BoundArgs = boundArgsWithout(m.BoundArgs, sub)
	c.Body = ast.CloneBlock(m.Body)
	names := make([]string, 0, len(sub))
	types := make([]ast.Type, 0, len(sub))
	for n, t := range sub {
		names = append(names, n)
		types = append(types, t)
	}
	substTraitArgs(&c, names, types)
	substConstReads(&c, sub)
	return &c
}

// substConstReads puts the integer that sub binds in each read of a const
// parameter in the body of method.
func substConstReads(method *ast.FuncDecl, sub map[string]ast.Type) {
	to := map[string]int64{}
	for n, t := range sub {
		if v, ok := t.(ast.ConstArg); ok {
			to[n] = v.Value
		}
	}
	for _, p := range method.Params {
		delete(to, p.Name)
	}
	if method.Receiver != nil {
		delete(to, method.Receiver.Name)
	}
	if len(to) == 0 || method.Body == nil {
		return
	}
	ast.RewriteBlockExprs(method.Body, func(e ast.Expr) ast.Expr {
		if id, ok := e.(*ast.Ident); ok {
			if v, ok := to[id.Name]; ok {
				return &ast.NumberLit{P: id.P, Value: v}
			}
		}
		return e
	})
}

func withoutBound(names []string, sub map[string]ast.Type) []string {
	var out []string
	for _, n := range names {
		if _, ok := sub[n]; !ok {
			out = append(out, n)
		}
	}
	return out
}

func boundsWithout(b map[string][]string, sub map[string]ast.Type) map[string][]string {
	if b == nil {
		return nil
	}
	out := map[string][]string{}
	for k, v := range b {
		if _, ok := sub[k]; !ok {
			out[k] = v
		}
	}
	return out
}

func boundArgsWithout(b map[string][][]ast.Type, sub map[string]ast.Type) map[string][][]ast.Type {
	if b == nil {
		return nil
	}
	out := map[string][][]ast.Type{}
	for k, v := range b {
		if _, ok := sub[k]; !ok {
			out[k] = v
		}
	}
	return out
}
