package monomorph

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

type instKey struct {
	name string
	mang string
}

const maxStructInstRounds = 64

func Run(prog *ast.Program, info *checker.Info) error {
	if len(info.Generics) == 0 {
		return nil
	}

	instantiations := map[instKey][]ast.Type{}
	structInsts := map[instKey][]ast.Type{}

	var boundErrs []error
	instantiate := func(id *ast.Ident, gen *ast.FuncDecl, typeArgs []ast.Type, pos ast.Position) bool {
		if hasParamType(typeArgs) {
			return false
		}
		boundErrs = append(boundErrs, checker.BoundErrors(info, gen, typeArgs, pos)...)
		mang := ast.MangleInst(id.Name, typeArgs)
		instantiations[instKey{name: id.Name, mang: mang}] = typeArgs
		id.Name = mang
		return true
	}
	collectCalls := func(body *ast.Block) {
		if body == nil {
			return
		}
		ast.Walk(body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Call:
				if name, margs, ok := enumAssocCallee(prog, info, x); ok {
					x.Callee = &ast.Ident{P: x.P, Name: name}
					x.TypeArgs = margs
					x.Method = nil
				}
				id, ok := x.Callee.(*ast.Ident)
				if !ok {
					return true
				}
				gen, isGen := info.GenericFuncs[id.Name]
				if !isGen || len(x.TypeArgs) != len(gen.TypeParams) {
					return true
				}
				if !instantiate(id, gen, x.TypeArgs, x.P) {
					return true
				}
				sub := make(map[string]ast.Type, len(gen.TypeParams))
				for i, name := range gen.TypeParams {
					sub[name] = x.TypeArgs[i]
				}
				for _, arg := range x.Args {
					substituteNode(arg, sub)
				}
				x.TypeArgs = nil
			case *ast.Ident:
				gen, isGen := info.GenericFuncs[x.Name]
				if !isGen || len(x.TypeArgs) != len(gen.TypeParams) {
					return true
				}
				if instantiate(x, gen, x.TypeArgs, x.P) {
					x.TypeArgs = nil
				}
			}
			return true
		})
	}

	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		collectCalls(fn.Body)
		walkBlockStructLits(fn.Body, func(sl *ast.StructLit) {
			if len(sl.TypeArgs) == 0 {
				return
			}
			if hasParamType(sl.TypeArgs) {
				return
			}
			gen, isGen := info.GenericStructs[sl.TypeName]
			if !isGen || len(sl.TypeArgs) != len(gen.TypeParams) {
				return
			}
			mang := ast.MangleInst(sl.TypeName, sl.TypeArgs)
			structInsts[instKey{name: sl.TypeName, mang: mang}] = sl.TypeArgs
			sl.TypeName = mang
			sl.TypeArgs = nil
		})
	}

	keys := make([]instKey, 0, len(instantiations))
	for k := range instantiations {
		keys = append(keys, k)
	}
	sortKeys(keys)
	done := make(map[instKey]bool, len(keys))
	var cloned []*ast.FuncDecl
	for i := 0; i < len(keys); i++ {
		k := keys[i]
		if done[k] {
			continue
		}
		done[k] = true
		gen := info.GenericFuncs[k.name]
		args := instantiations[k]
		sub := make(map[string]ast.Type, len(gen.TypeParams))
		for i, tp := range gen.TypeParams {
			sub[tp] = args[i]
		}
		c := cloneFuncDecl(gen)
		c.Name = k.mang
		c.TypeParams = nil
		for i := range c.Params {
			c.Params[i].Type = substituteType(c.Params[i].Type, sub)
		}
		c.ReturnType = substituteType(c.ReturnType, sub)
		substituteBlock(c.Body, sub)
		substituteConstParams(c, sub)
		before := len(instantiations)
		for mk, margs := range boundEnumImplMethods(prog, info, gen, args) {
			if _, seen := instantiations[mk]; !seen {
				instantiations[mk] = margs
			}
		}
		collectCalls(c.Body)
		if len(instantiations) != before {
			fresh := make([]instKey, 0, len(instantiations)-before)
			for nk := range instantiations {
				if !done[nk] {
					fresh = append(fresh, nk)
				}
			}
			sortKeys(fresh)
			keys = append(keys, fresh...)
		}
		walkBlockStructLits(c.Body, func(sl *ast.StructLit) {
			if len(sl.TypeArgs) == 0 {
				return
			}
			if hasParamType(sl.TypeArgs) {
				return
			}
			gen, isGen := info.GenericStructs[sl.TypeName]
			if !isGen || len(sl.TypeArgs) != len(gen.TypeParams) {
				return
			}
			mang := ast.MangleInst(sl.TypeName, sl.TypeArgs)
			structInsts[instKey{name: sl.TypeName, mang: mang}] = sl.TypeArgs
			sl.TypeName = mang
			sl.TypeArgs = nil
		})
		cloned = append(cloned, c)
	}

	if len(boundErrs) > 0 {
		return diag.Errors(dedupeErrors(boundErrs))
	}

	structKeys := make([]instKey, 0, len(structInsts))
	for k := range structInsts {
		structKeys = append(structKeys, k)
	}
	sortKeys(structKeys)
	var clonedStructs []*ast.StructDecl
	for _, k := range structKeys {
		gen := info.GenericStructs[k.name]
		args := structInsts[k]
		sub := make(map[string]ast.Type, len(gen.TypeParams))
		for i, tp := range gen.TypeParams {
			sub[tp] = args[i]
		}
		c := *gen
		c.Name = k.mang
		c.TypeParams = nil
		c.Fields = make([]ast.Param, len(gen.Fields))
		for i, f := range gen.Fields {
			c.Fields[i] = ast.Param{Name: f.Name, Type: substituteType(f.Type, sub)}
		}
		clonedStructs = append(clonedStructs, &c)
	}

	keptEnumMethods := map[string]bool{}
	for _, impl := range prog.Impls {
		if len(impl.TypeParams) > 0 && forKeptGenericEnum(impl, info) {
			for _, mn := range impl.MethodNames {
				name, _, _ := info.ResolveMethod(impl.Type.(ast.EnumType).Name, mn, []string{impl.Trait})
				keptEnumMethods[name] = true
			}
		}
	}
	keptGenerics := genericsReachedFrom(prog, info, keptEnumMethods)
	keep := prog.Funcs[:0]
	for _, fn := range prog.Funcs {
		if _, isGen := info.Generics[fn.Name]; isGen && !keptGenerics[fn.Name] {
			continue
		}
		keep = append(keep, fn)
	}
	prog.Funcs = append(keep, cloned...)
	keepStructs := prog.Structs[:0]
	for _, sd := range prog.Structs {
		if _, isGen := info.Generics[sd.Name]; isGen {
			continue
		}
		keepStructs = append(keepStructs, sd)
	}
	prog.Structs = append(keepStructs, clonedStructs...)

	keepEnums := prog.Enums[:0]
	for _, ed := range prog.Enums {
		if len(ed.TypeParams) > 0 && enumNeedsClone(ed, info) {
			continue
		}
		keepEnums = append(keepEnums, ed)
	}
	prog.Enums = keepEnums

	converged := false
	for round := 0; round < maxStructInstRounds; round++ {
		before := len(structInsts)
		rewriteGenericStructTypes(prog, info, structInsts)
		for _, k := range collectKeys(structInsts) {
			if ged := genericEnum(info, k.name); ged != nil {
				already := false
				for _, ed := range prog.Enums {
					if ed.Name == k.mang {
						already = true
						break
					}
				}
				if already {
					continue
				}
				args := structInsts[k]
				sub := make(map[string]ast.Type, len(ged.TypeParams))
				for i, tp := range ged.TypeParams {
					sub[tp] = args[i]
				}
				c := *ged
				c.Name = k.mang
				c.TypeParams = nil
				c.Monomorphized = true
				c.Variants = make([]ast.EnumVariant, len(ged.Variants))
				for i, v := range ged.Variants {
					nv := v
					nv.Payloads = make([]ast.Type, len(v.Payloads))
					for j, p := range v.Payloads {
						nv.Payloads[j] = substituteType(p, sub)
					}
					c.Variants[i] = nv
				}
				prog.Enums = append(prog.Enums, &c)
				continue
			}
			already := false
			for _, sd := range prog.Structs {
				if sd.Name == k.mang {
					already = true
					break
				}
			}
			if already {
				continue
			}
			gen := info.GenericStructs[k.name]
			args := structInsts[k]
			sub := make(map[string]ast.Type, len(gen.TypeParams))
			for i, tp := range gen.TypeParams {
				sub[tp] = args[i]
			}
			c := *gen
			c.Name = k.mang
			c.TypeParams = nil
			c.Fields = make([]ast.Param, len(gen.Fields))
			for i, f := range gen.Fields {
				c.Fields[i] = ast.Param{Name: f.Name, Type: substituteType(f.Type, sub)}
			}
			prog.Structs = append(prog.Structs, &c)
		}
		if len(structInsts) == before {
			converged = true
			break
		}
	}
	if !converged {
		return fmt.Errorf("monomorph: generic struct instantiation did not terminate after %d rounds — a generic type appears to be infinitely (polymorphically) recursive, e.g. a field typed `T[T]`-style that nests the struct inside its own type argument; such types can't be monomorphised", maxStructInstRounds)
	}

	existingFunc := func(name string) bool {
		for _, fn := range prog.Funcs {
			if fn.Name == name {
				return true
			}
		}
		return false
	}
	existingStruct := func(prog *ast.Program, name string) bool {
		for _, sd := range prog.Structs {
			if sd.Name == name {
				return true
			}
		}
		return false
	}
	mangleBodyStructLits := func(b *ast.Block) {
		walkBlockStructLits(b, func(sl *ast.StructLit) {
			if len(sl.TypeArgs) == 0 || hasParamType(sl.TypeArgs) {
				return
			}
			gen, isGen := info.GenericStructs[sl.TypeName]
			if !isGen || len(sl.TypeArgs) != len(gen.TypeParams) {
				return
			}
			mang := ast.MangleInst(sl.TypeName, sl.TypeArgs)
			structInsts[instKey{name: sl.TypeName, mang: mang}] = sl.TypeArgs
			sl.TypeName = mang
			sl.TypeArgs = nil
		})
	}
	methodDone := map[instKey]bool{}
	implDone := map[string]bool{}
	for mround := 0; mround < maxStructInstRounds; mround++ {
		progressed := false
		var methodKeys []instKey
		methodArgs := map[instKey][]ast.Type{}
		methodRecv := map[instKey]string{}
		methodSimple := map[instKey]string{}
		var newImpls []*ast.ImplDecl
		called := methodCallsOn(prog)
		for _, impl := range prog.Impls {
			if len(impl.TypeParams) == 0 {
				continue
			}
			baseName, ok := baseTypeName(impl.Type)
			if !ok {
				continue
			}
			pset := map[string]bool{}
			for _, tp := range impl.TypeParams {
				pset[tp] = true
			}
			for _, sk := range collectKeys(structInsts) {
				if sk.name != baseName {
					continue
				}
				psub := map[string]ast.Type{}
				if !unifyImplType(impl.Type, ast.StructType{Name: baseName, Args: structInsts[sk]}, pset, psub) {
					continue
				}
				implK := impl.Trait + "/" + sk.mang
				if !implDone[implK] {
					implDone[implK] = true
					ci := *impl
					ci.TypeParams = nil
					ci.Bounds = nil
					ci.BoundArgs = nil
					if _, isEnum := impl.Type.(ast.EnumType); isEnum {
						ci.Type = ast.EnumType{Name: sk.mang}
					} else {
						ci.Type = ast.StructType{Name: sk.mang}
					}
					if len(impl.TraitArgs) > 0 {
						ta := make([]ast.Type, len(impl.TraitArgs))
						for k := range impl.TraitArgs {
							ta[k] = substTypeByName(impl.TraitArgs[k], psub)
						}
						ci.TraitArgs = ta
					}
					if len(impl.AssocTypeBindings) > 0 {
						ab := make(map[string]ast.Type, len(impl.AssocTypeBindings))
						for name, t := range impl.AssocTypeBindings {
							ab[name] = substTypeByName(t, psub)
						}
						ci.AssocTypeBindings = ab
					}
					newImpls = append(newImpls, &ci)
					progressed = true
				}
				for _, mn := range impl.MethodNames {
					genName, _, _ := info.ResolveMethod(baseName, mn, []string{impl.Trait})
					gen, isGen := info.GenericFuncs[genName]
					for _, pre := range []string{"__method_", "__assoc_"} {
						if isGen {
							break
						}
						genName = pre + baseName + "_" + mn
						gen, isGen = info.GenericFuncs[genName]
					}
					if !isGen {
						continue
					}
					prefix := "__method_"
					if strings.HasPrefix(genName, "__assoc_") {
						prefix = "__assoc_"
					}
					margs := make([]ast.Type, len(gen.TypeParams))
					complete := true
					for i, tp := range gen.TypeParams {
						if v, found := psub[tp]; found {
							margs[i] = v
						} else {
							complete = false
						}
					}
					if !complete || hasParamType(margs) {
						continue
					}
					if wrapsReceiver(gen.ReturnType, impl.Type) && !called[sk.mang+"."+mn] {
						continue
					}
					mk := instKey{name: genName, mang: prefix + sk.mang + "_" + mn}
					if methodDone[mk] {
						continue
					}
					methodDone[mk] = true
					methodKeys = append(methodKeys, mk)
					methodArgs[mk] = margs
					methodRecv[mk] = sk.mang
					methodSimple[mk] = mn
				}
			}
		}
		prog.Impls = append(prog.Impls, newImpls...)
		sortKeys(methodKeys)
		for _, mk := range methodKeys {
			gen := info.GenericFuncs[mk.name]
			margs := methodArgs[mk]
			sub := make(map[string]ast.Type, len(gen.TypeParams))
			for i, tp := range gen.TypeParams {
				sub[tp] = margs[i]
			}
			mc := cloneFuncDecl(gen)
			mc.Name = mk.mang
			mc.TypeParams = nil
			mc.MethodRecv = methodRecv[mk]
			mc.MethodSimpleName = methodSimple[mk]
			for i := range mc.Params {
				mc.Params[i].Type = substituteType(mc.Params[i].Type, sub)
			}
			mc.ReturnType = substituteType(mc.ReturnType, sub)
			substituteBlock(mc.Body, sub)
			collectCalls(mc.Body)
			mangleBodyStructLits(mc.Body)
			prog.Funcs = append(prog.Funcs, mc)
			progressed = true
		}
		for _, fk := range collectKeys(instantiations) {
			if existingFunc(fk.mang) {
				continue
			}
			gen, isGen := info.GenericFuncs[fk.name]
			if !isGen {
				continue
			}
			args := instantiations[fk]
			if len(args) != len(gen.TypeParams) || hasParamType(args) {
				continue
			}
			sub := make(map[string]ast.Type, len(gen.TypeParams))
			for i, tp := range gen.TypeParams {
				sub[tp] = args[i]
			}
			fc := cloneFuncDecl(gen)
			fc.Name = fk.mang
			fc.TypeParams = nil
			for i := range fc.Params {
				fc.Params[i].Type = substituteType(fc.Params[i].Type, sub)
			}
			fc.ReturnType = substituteType(fc.ReturnType, sub)
			substituteBlock(fc.Body, sub)
			collectCalls(fc.Body)
			mangleBodyStructLits(fc.Body)
			prog.Funcs = append(prog.Funcs, fc)
			progressed = true
		}
		beforeStructs := len(structInsts)
		rewriteGenericStructTypes(prog, info, structInsts)
		for _, k := range collectKeys(structInsts) {
			if ged := genericEnum(info, k.name); ged != nil {
				already := false
				for _, ed := range prog.Enums {
					if ed.Name == k.mang {
						already = true
						break
					}
				}
				if already {
					continue
				}
				args := structInsts[k]
				sub := make(map[string]ast.Type, len(ged.TypeParams))
				for i, tp := range ged.TypeParams {
					sub[tp] = args[i]
				}
				c := *ged
				c.Name = k.mang
				c.TypeParams = nil
				c.Monomorphized = true
				c.Variants = make([]ast.EnumVariant, len(ged.Variants))
				for i, v := range ged.Variants {
					nv := v
					nv.Payloads = make([]ast.Type, len(v.Payloads))
					for j, p := range v.Payloads {
						nv.Payloads[j] = substituteType(p, sub)
					}
					c.Variants[i] = nv
				}
				prog.Enums = append(prog.Enums, &c)
				progressed = true
				continue
			}
			if existingStruct(prog, k.mang) {
				continue
			}
			gen := info.GenericStructs[k.name]
			if gen == nil {
				continue
			}
			args := structInsts[k]
			sub := make(map[string]ast.Type, len(gen.TypeParams))
			for i, tp := range gen.TypeParams {
				sub[tp] = args[i]
			}
			c := *gen
			c.Name = k.mang
			c.TypeParams = nil
			c.Fields = make([]ast.Param, len(gen.Fields))
			for i, f := range gen.Fields {
				c.Fields[i] = ast.Param{Name: f.Name, Type: substituteType(f.Type, sub)}
			}
			prog.Structs = append(prog.Structs, &c)
			progressed = true
		}
		if !progressed && len(structInsts) == beforeStructs {
			break
		}
	}

	keepImpls := prog.Impls[:0]
	for _, impl := range prog.Impls {
		if len(impl.TypeParams) > 0 && !forKeptGenericEnum(impl, info) {
			continue
		}
		keepImpls = append(keepImpls, impl)
	}
	prog.Impls = keepImpls

	clonedEnumNames := map[string]bool{}
	for name, ed := range info.Enums {
		if len(ed.TypeParams) > 0 && enumNeedsClone(ed, info) {
			clonedEnumNames[name] = true
		}
	}
	if len(clonedEnumNames) > 0 {
		ast.WalkProgram(prog, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if clonedEnumNames[x.EnumName] {
					x.EnumName = ""
				}
			case *ast.EnumLit:
				if clonedEnumNames[x.EnumName] {
					x.EnumName = ""
				}
			}
			return true
		})
	}

	info.GenericFuncs = map[string]*ast.FuncDecl{}
	info.GenericStructs = map[string]*ast.StructDecl{}
	newInfo, err := checker.CheckTarget(prog, info.Target)
	if err != nil {
		if hasDiagnosticCode(err) {
			return err
		}
		return fmt.Errorf("monomorph: re-check failed (compiler bug): %w", err)
	}
	stateful := info.StatefulHandler
	*info = *newInfo
	info.StatefulHandler = info.StatefulHandler || stateful
	return nil
}

func boundEnumImplMethods(prog *ast.Program, info *checker.Info, gen *ast.FuncDecl, args []ast.Type) map[instKey][]ast.Type {
	out := map[instKey][]ast.Type{}
	for i, tp := range gen.TypeParams {
		if i >= len(args) {
			break
		}
		et, isEnum := args[i].(ast.EnumType)
		if !isEnum || len(et.Args) == 0 || hasParamType(et.Args) {
			continue
		}
		ed, ok := info.Enums[et.Name]
		if !ok || len(ed.TypeParams) == 0 || enumNeedsClone(ed, info) {
			continue
		}
		for _, trait := range gen.Bounds[tp] {
			for _, impl := range prog.Impls {
				if impl.Trait != trait {
					continue
				}
				for _, mn := range impl.MethodNames {
					if k, margs, ok := enumImplMethod(info, impl, et, mn); ok {
						out[k] = margs
					}
				}
			}
		}
	}
	return out
}

func genericsReachedFrom(prog *ast.Program, info *checker.Info, kept map[string]bool) map[string]bool {
	byName := make(map[string]*ast.FuncDecl, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		byName[fn.Name] = fn
	}
	out := make(map[string]bool, len(kept))
	var work []string
	for name := range kept {
		out[name] = true
		work = append(work, name)
	}
	for len(work) > 0 {
		fn := byName[work[len(work)-1]]
		work = work[:len(work)-1]
		if fn == nil || fn.Body == nil {
			continue
		}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if _, isGen := info.GenericFuncs[id.Name]; isGen && !out[id.Name] {
				out[id.Name] = true
				work = append(work, id.Name)
			}
			return true
		})
	}
	return out
}

func enumImplMethod(info *checker.Info, impl *ast.ImplDecl, et ast.EnumType, mn string) (instKey, []ast.Type, bool) {
	if len(impl.TypeParams) == 0 {
		return instKey{}, nil, false
	}
	pset := map[string]bool{}
	for _, itp := range impl.TypeParams {
		pset[itp] = true
	}
	psub := map[string]ast.Type{}
	if !unifyImplType(impl.Type, et, pset, psub) {
		return instKey{}, nil, false
	}
	genName, _, _ := info.ResolveMethod(et.Name, mn, []string{impl.Trait})
	mgen, isGen := info.GenericFuncs[genName]
	for _, pre := range []string{"__method_", "__assoc_"} {
		if isGen {
			break
		}
		genName = pre + et.Name + "_" + mn
		mgen, isGen = info.GenericFuncs[genName]
	}
	if !isGen {
		return instKey{}, nil, false
	}
	margs := make([]ast.Type, len(mgen.TypeParams))
	for k, mtp := range mgen.TypeParams {
		v, found := psub[mtp]
		if !found {
			return instKey{}, nil, false
		}
		margs[k] = v
	}
	if hasParamType(margs) {
		return instKey{}, nil, false
	}
	return instKey{name: genName, mang: ast.MangleInst(genName, margs)}, margs, true
}

func enumAssocCallee(prog *ast.Program, info *checker.Info, c *ast.Call) (string, []ast.Type, bool) {
	if c.Method == nil || !c.Method.Assoc {
		return "", nil, false
	}
	et, ok := c.Method.Receiver.(ast.EnumType)
	if !ok || len(et.Args) == 0 || hasParamType(et.Args) {
		return "", nil, false
	}
	fa, ok := c.Callee.(*ast.FieldAccess)
	if !ok {
		return "", nil, false
	}
	ed, ok := info.Enums[et.Name]
	if !ok || len(ed.TypeParams) == 0 || enumNeedsClone(ed, info) {
		return "", nil, false
	}
	for _, impl := range prog.Impls {
		if c.Method.OwnerTrait != "" && impl.Trait != c.Method.OwnerTrait {
			continue
		}
		if !slices.Contains(impl.MethodNames, fa.Field) {
			continue
		}
		if k, margs, ok := enumImplMethod(info, impl, et, fa.Field); ok {
			return k.name, margs, true
		}
	}
	return "", nil, false
}

func hasDiagnosticCode(err error) bool {
	switch e := err.(type) {
	case nil:
		return false
	case *checker.Error:
		return e.ErrCode != ""
	case diag.Errors:
		for _, one := range e {
			if hasDiagnosticCode(one) {
				return true
			}
		}
		return false
	}
	var one *checker.Error
	if errors.As(err, &one) {
		return one.ErrCode != ""
	}
	return hasDiagnosticCode(errors.Unwrap(err))
}

func dedupeErrors(errs []error) []error {
	seen := map[string]bool{}
	out := errs[:0:0]
	for _, e := range errs {
		if k := e.Error(); !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	return out
}

func forKeptGenericEnum(impl *ast.ImplDecl, info *checker.Info) bool {
	et, ok := impl.Type.(ast.EnumType)
	if !ok {
		return false
	}
	ed := genericEnum(info, et.Name)
	return ed != nil && !enumNeedsClone(ed, info)
}

func genericEnum(info *checker.Info, name string) *ast.EnumDecl {
	ed, ok := info.Enums[name]
	if !ok || len(ed.TypeParams) == 0 {
		return nil
	}
	return ed
}

func typeRefsGenericNominal(t ast.Type, info *checker.Info) bool {
	switch x := t.(type) {
	case ast.StructType:
		if len(x.Args) > 0 {
			if _, isGen := info.GenericStructs[x.Name]; isGen {
				return true
			}
		}
		for _, a := range x.Args {
			if typeRefsGenericNominal(a, info) {
				return true
			}
		}
	case ast.EnumType:
		if len(x.Args) > 0 && genericEnum(info, x.Name) != nil {
			return true
		}
		for _, a := range x.Args {
			if typeRefsGenericNominal(a, info) {
				return true
			}
		}
	case ast.ArrayType:
		return typeRefsGenericNominal(x.Elem, info)
	case ast.SliceType:
		return typeRefsGenericNominal(x.Elem, info)
	case ast.TupleType:
		for _, e := range x.Elems {
			if typeRefsGenericNominal(e, info) {
				return true
			}
		}
	case *ast.FuncType:
		return false
	}
	return false
}

func enumNeedsClone(ed *ast.EnumDecl, info *checker.Info) bool {
	for _, v := range ed.Variants {
		for _, p := range v.Payloads {
			if typeRefsGenericNominal(p, info) {
				return true
			}
		}
	}
	return false
}

func sortKeys(ks []instKey) {
	for i := 1; i < len(ks); i++ {
		j := i
		for j > 0 && (ks[j-1].name > ks[j].name ||
			(ks[j-1].name == ks[j].name && ks[j-1].mang > ks[j].mang)) {
			ks[j-1], ks[j] = ks[j], ks[j-1]
			j--
		}
	}
}

func substTypeByName(t ast.Type, sub map[string]ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.ParamType:
		if v, ok := sub[x.Name]; ok {
			return v
		}
		return x
	case ast.StructType:
		if len(x.Args) == 0 {
			if v, ok := sub[x.Name]; ok {
				return v
			}
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substTypeByName(x.Args[i], sub)
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.EnumType:
		if len(x.Args) == 0 {
			if v, ok := sub[x.Name]; ok {
				return v
			}
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substTypeByName(x.Args[i], sub)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.ArrayType:
		return ast.ArrayType{Elem: substTypeByName(x.Elem, sub)}
	case ast.SliceType:
		return ast.SliceType{Elem: substTypeByName(x.Elem, sub)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = substTypeByName(x.Elems[i], sub)
		}
		return out
	case *ast.FuncType:
		out := &ast.FuncType{Result: substTypeByName(x.Result, sub), ParamOwn: x.ParamOwn}
		for _, p := range x.Params {
			out.Params = append(out.Params, substTypeByName(p, sub))
		}
		return out
	case ast.ProjType:
		return ast.ProjType{Base: substTypeByName(x.Base, sub), Name: x.Name}
	}
	return t
}

func substituteType(t ast.Type, sub map[string]ast.Type) ast.Type {
	if t == nil {
		return nil
	}
	switch x := t.(type) {
	case ast.ParamType:
		if v, ok := sub[x.Name]; ok {
			return v
		}
		return x
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substituteType(x.Args[i], sub)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substituteType(x.Args[i], sub)
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.ArrayType:
		return ast.ArrayType{Elem: substituteType(x.Elem, sub)}
	case ast.SliceType:
		return ast.SliceType{Elem: substituteType(x.Elem, sub)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = substituteType(x.Elems[i], sub)
		}
		return out
	case *ast.FuncType:
		out := &ast.FuncType{Result: substituteType(x.Result, sub), ParamOwn: x.ParamOwn}
		for _, p := range x.Params {
			out.Params = append(out.Params, substituteType(p, sub))
		}
		return out
	case ast.ProjType:
		return ast.ProjType{Base: substituteType(x.Base, sub), Name: x.Name}
	}
	return t
}

func substituteBlock(b *ast.Block, sub map[string]ast.Type) {
	if b == nil {
		return
	}
	substituteNode(b, sub)
}

func substituteNode(n ast.Node, sub map[string]ast.Type) {
	if n == nil {
		return
	}
	ast.Walk(n, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			x.Type = substituteType(x.Type, sub)
		case *ast.StructLit:
			for i := range x.TypeArgs {
				x.TypeArgs[i] = substituteType(x.TypeArgs[i], sub)
			}
		case *ast.Call:
			for i := range x.TypeArgs {
				x.TypeArgs[i] = substituteType(x.TypeArgs[i], sub)
			}
			substituteAssocReceiver(x, sub)
			if x.Method != nil && !x.Method.Assoc && containsParamType(x.Method.Receiver) {
				site := *x.Method
				site.Receiver = substituteType(site.Receiver, sub)
				x.Method = &site
			}
		case *ast.Ident:
			for i := range x.TypeArgs {
				x.TypeArgs[i] = substituteType(x.TypeArgs[i], sub)
			}
		case *ast.MapLit:
			x.KeyType = substituteType(x.KeyType, sub)
			x.ValueType = substituteType(x.ValueType, sub)
		case *ast.ArrayLit:
			x.ElemType = substituteType(x.ElemType, sub)
		case *ast.CastExpr:
			x.Target = substituteType(x.Target, sub)
		case *ast.DowncastExpr:
			x.Target = substituteType(x.Target, sub)
		case *ast.TryOp:
			x.Type = substituteType(x.Type, sub)
		case *ast.Lambda:
			for i := range x.Params {
				x.Params[i].Type = substituteType(x.Params[i].Type, sub)
			}
			x.ReturnType = substituteType(x.ReturnType, sub)
			for i := range x.Captures {
				x.Captures[i].Type = substituteType(x.Captures[i].Type, sub)
			}
		case *ast.FuncDecl:
			for i := range x.Params {
				x.Params[i].Type = substituteType(x.Params[i].Type, sub)
			}
			x.ReturnType = substituteType(x.ReturnType, sub)
		}
		return true
	})
}

func substituteConstParams(fn *ast.FuncDecl, sub map[string]ast.Type) {
	if len(fn.ConstParams) == 0 {
		return
	}
	fn.ConstParams = nil
	ast.RewriteBlockExprs(fn.Body, func(e ast.Expr) ast.Expr {
		id, ok := e.(*ast.Ident)
		if !ok || !id.ConstParam {
			return e
		}
		if v, ok := sub[id.Name].(ast.ConstArg); ok {
			return &ast.NumberLit{P: id.P, Value: v.Value}
		}
		return e
	})
}

func substituteAssocReceiver(c *ast.Call, sub map[string]ast.Type) {
	if c.Method == nil || !c.Method.Assoc {
		return
	}
	pt, ok := c.Method.Receiver.(ast.ParamType)
	if !ok {
		return
	}
	fa, ok := c.Callee.(*ast.FieldAccess)
	if !ok {
		return
	}
	tid, ok := fa.Target.(*ast.Ident)
	if !ok || tid.Name != pt.Name {
		return
	}
	ct, ok := sub[pt.Name]
	if !ok {
		return
	}
	if st, ok := ct.(ast.StructType); ok && len(st.Args) > 0 {
		fa.Target = &ast.Ident{P: tid.P, Name: ast.MangleInst(st.Name, st.Args)}
		return
	}
	if name, ok := concreteTypeNameOf(ct); ok {
		fa.Target = &ast.Ident{P: tid.P, Name: name}
	}
	if et, ok := ct.(ast.EnumType); ok && len(et.Args) > 0 {
		site := *c.Method
		site.Receiver = et
		c.Method = &site
	}
}

func methodCallsOn(prog *ast.Program) map[string]bool {
	out := map[string]bool{}
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			if c, ok := n.(*ast.Call); ok && c.Method != nil {
				switch r := c.Method.Receiver.(type) {
				case ast.StructType:
					out[ast.MangleInst(r.Name, r.Args)+"."+c.Method.Field] = true
				}
			}
			return true
		})
	}
	return out
}

func wrapsReceiver(t ast.Type, recv ast.Type) bool {
	switch x := t.(type) {
	case ast.StructType:
		for _, a := range x.Args {
			if mentionsType(a, recv) || wrapsReceiver(a, recv) {
				return true
			}
		}
	case ast.EnumType:
		for _, a := range x.Args {
			if wrapsReceiver(a, recv) {
				return true
			}
		}
	case ast.TupleType:
		for _, e := range x.Elems {
			if wrapsReceiver(e, recv) {
				return true
			}
		}
	case ast.ArrayType:
		return wrapsReceiver(x.Elem, recv)
	}
	return false
}

func mentionsType(t, want ast.Type) bool {
	if ast.Equal(t, want) {
		return true
	}
	switch x := t.(type) {
	case ast.StructType:
		for _, a := range x.Args {
			if mentionsType(a, want) {
				return true
			}
		}
	case ast.EnumType:
		for _, a := range x.Args {
			if mentionsType(a, want) {
				return true
			}
		}
	case ast.TupleType:
		for _, e := range x.Elems {
			if mentionsType(e, want) {
				return true
			}
		}
	case ast.ArrayType:
		return mentionsType(x.Elem, want)
	}
	return false
}

func hasParamType(types []ast.Type) bool {
	for _, t := range types {
		if containsParamType(t) {
			return true
		}
	}
	return false
}

func baseTypeName(t ast.Type) (string, bool) {
	switch x := t.(type) {
	case ast.StructType:
		return x.Name, true
	case ast.EnumType:
		return x.Name, true
	}
	return "", false
}

func unifyImplType(pattern, concrete ast.Type, params map[string]bool, sub map[string]ast.Type) bool {
	switch p := pattern.(type) {
	case ast.ParamType:
		if params[p.Name] {
			if ex, ok := sub[p.Name]; ok {
				return ast.Equal(ex, concrete)
			}
			sub[p.Name] = concrete
			return true
		}
		return ast.Equal(pattern, concrete)
	case ast.StructType:
		if len(p.Args) == 0 && params[p.Name] {
			if ex, ok := sub[p.Name]; ok {
				return ast.Equal(ex, concrete)
			}
			sub[p.Name] = concrete
			return true
		}
		c, ok := concrete.(ast.StructType)
		if !ok || c.Name != p.Name || len(c.Args) != len(p.Args) {
			return false
		}
		for i := range p.Args {
			if !unifyImplType(p.Args[i], c.Args[i], params, sub) {
				return false
			}
		}
		return true
	case ast.EnumType:
		c, ok := concrete.(ast.EnumType)
		if !ok || c.Name != p.Name || len(c.Args) != len(p.Args) {
			return false
		}
		for i := range p.Args {
			if !unifyImplType(p.Args[i], c.Args[i], params, sub) {
				return false
			}
		}
		return true
	case ast.ArrayType:
		c, ok := concrete.(ast.ArrayType)
		return ok && unifyImplType(p.Elem, c.Elem, params, sub)
	case ast.SliceType:
		c, ok := concrete.(ast.SliceType)
		return ok && unifyImplType(p.Elem, c.Elem, params, sub)
	case ast.TupleType:
		c, ok := concrete.(ast.TupleType)
		if !ok || len(c.Elems) != len(p.Elems) {
			return false
		}
		for i := range p.Elems {
			if !unifyImplType(p.Elems[i], c.Elems[i], params, sub) {
				return false
			}
		}
		return true
	}
	return ast.Equal(pattern, concrete)
}

func containsParamType(t ast.Type) bool {
	switch x := t.(type) {
	case ast.ParamType:
		return true
	case ast.StructType:
		return hasParamType(x.Args)
	case ast.EnumType:
		return hasParamType(x.Args)
	case ast.ArrayType:
		return containsParamType(x.Elem)
	case ast.SliceType:
		return containsParamType(x.Elem)
	case ast.TupleType:
		return hasParamType(x.Elems)
	case *ast.FuncType:
		if containsParamType(x.Result) {
			return true
		}
		return hasParamType(x.Params)
	}
	return false
}

func concreteTypeNameOf(t ast.Type) (string, bool) { return ast.ReceiverTypeName(t) }

func cloneFuncDecl(fn *ast.FuncDecl) *ast.FuncDecl {
	c := *fn
	c.TypeParams = nil
	c.Params = append([]ast.Param(nil), fn.Params...)
	c.Body = ast.CloneBlock(fn.Body)
	return &c
}

func walkBlockStructLits(b *ast.Block, fn func(*ast.StructLit)) {
	if b == nil {
		return
	}
	ast.Walk(b, func(n ast.Node) bool {
		if sl, ok := n.(*ast.StructLit); ok {
			fn(sl)
		}
		return true
	})
}

func rewriteGenericStructTypes(prog *ast.Program, info *checker.Info, into map[instKey][]ast.Type) {
	rewrite := func(slot *ast.Type) {
		if slot == nil {
			return
		}
		*slot = rewriteType(*slot, info, into)
	}
	for _, fn := range prog.Funcs {
		if fn.Receiver != nil {
			rewrite(&fn.Receiver.Type)
		}
		for i := range fn.Params {
			rewrite(&fn.Params[i].Type)
		}
		rewrite(&fn.ReturnType)
		rewriteBlockTypes(fn.Body, info, into)
	}
	for _, sd := range prog.Structs {
		if _, isGen := info.GenericStructs[sd.Name]; isGen {
			continue
		}
		for i := range sd.Fields {
			rewrite(&sd.Fields[i].Type)
		}
	}
	for _, ed := range prog.Enums {
		if len(ed.TypeParams) > 0 {
			continue
		}
		for vi := range ed.Variants {
			for pi := range ed.Variants[vi].Payloads {
				rewrite(&ed.Variants[vi].Payloads[pi])
			}
		}
	}
}

func collectKeys(m map[instKey][]ast.Type) []instKey {
	out := make([]instKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortKeys(out)
	return out
}

func rewriteType(t ast.Type, info *checker.Info, into map[instKey][]ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = rewriteType(x.Args[i], info, into)
		}
		if _, isGen := info.GenericStructs[x.Name]; !isGen {
			return ast.StructType{Name: x.Name, Args: args}
		}
		mang := ast.MangleInst(x.Name, args)
		into[instKey{name: x.Name, mang: mang}] = args
		return ast.StructType{Name: mang}
	case ast.EnumType:
		if len(x.Args) == 0 {
			ed := genericEnum(info, x.Name)
			if ed == nil || !enumNeedsClone(ed, info) {
				return x
			}
			for range ed.TypeParams {
				x.Args = append(x.Args, ast.NeverType{})
			}
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = rewriteType(x.Args[i], info, into)
		}
		if ed := genericEnum(info, x.Name); ed != nil && enumNeedsClone(ed, info) {
			mang := ast.MangleInst(x.Name, args)
			into[instKey{name: x.Name, mang: mang}] = args
			return ast.EnumType{Name: mang}
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.ProjType:
		return ast.ProjType{Base: rewriteType(x.Base, info, into), Name: x.Name}
	case ast.ArrayType:
		return ast.ArrayType{Elem: rewriteType(x.Elem, info, into)}
	case ast.SliceType:
		return ast.SliceType{Elem: rewriteType(x.Elem, info, into)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = rewriteType(x.Elems[i], info, into)
		}
		return out
	case *ast.FuncType:
		out := &ast.FuncType{Result: rewriteType(x.Result, info, into), ParamOwn: x.ParamOwn}
		for _, p := range x.Params {
			out.Params = append(out.Params, rewriteType(p, info, into))
		}
		return out
	}
	return t
}

func rewriteBlockTypes(b *ast.Block, info *checker.Info, into map[instKey][]ast.Type) {
	if b == nil {
		return
	}
	rewrite := func(t ast.Type) ast.Type {
		if t == nil {
			return nil
		}
		return rewriteType(t, info, into)
	}
	ast.Walk(b, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Var:
			x.Type = rewrite(x.Type)
		case *ast.StructLit:
			for i := range x.TypeArgs {
				x.TypeArgs[i] = rewrite(x.TypeArgs[i])
			}
		case *ast.Call:
			for i := range x.TypeArgs {
				x.TypeArgs[i] = rewrite(x.TypeArgs[i])
			}
		case *ast.Ident:
			for i := range x.TypeArgs {
				x.TypeArgs[i] = rewrite(x.TypeArgs[i])
			}
		case *ast.MapLit:
			x.KeyType = rewrite(x.KeyType)
			x.ValueType = rewrite(x.ValueType)
		case *ast.ArrayLit:
			x.ElemType = rewrite(x.ElemType)
		case *ast.CastExpr:
			x.Target = rewrite(x.Target)
		case *ast.DowncastExpr:
			x.Target = rewrite(x.Target)
		case *ast.TryOp:
			x.Type = rewrite(x.Type)
		case *ast.Lambda:
			for i := range x.Params {
				x.Params[i].Type = rewrite(x.Params[i].Type)
			}
			x.ReturnType = rewrite(x.ReturnType)
			for i := range x.Captures {
				x.Captures[i].Type = rewrite(x.Captures[i].Type)
			}
		case *ast.FuncDecl:
			for i := range x.Params {
				x.Params[i].Type = rewrite(x.Params[i].Type)
			}
			x.ReturnType = rewrite(x.ReturnType)
		}
		return true
	})
}
