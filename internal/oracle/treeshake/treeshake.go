package treeshake

import (
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/ast"
)

func dynVtableRoots(info *checker.Info, traits []string, concrete string, enqueue func(string)) {
	if info == nil || concrete == "" {
		return
	}
	_, isStruct := info.Structs[concrete]
	_, isEnum := info.Enums[concrete]
	prim := !isStruct && !isEnum
	for _, tr := range traits {
		td, ok := info.Traits[tr]
		if !ok {
			continue
		}
		for _, m := range td.Methods {
			if m.Assoc {
				continue
			}
			enqueue(implMethodName(info, concrete, m.Name, tr))
			if prim {
				enqueue("__dynbox_" + concrete + "_" + m.Name)
			}
		}
	}
}

func coercionTraits(dc checker.DynCoercion) []string {
	if len(dc.Traits) > 0 {
		return dc.Traits
	}
	return []string{dc.Trait}
}

func downcastRoots(dc *ast.DowncastExpr, info *checker.Info, enqueue func(string)) {
	if dc == nil || dc.Trait == "" {
		return
	}
	concrete := ""
	switch t := dc.Target.(type) {
	case ast.StructType:
		concrete = t.Name
	case ast.EnumType:
		concrete = t.Name
	default:
		return
	}
	traits := dc.Traits
	if len(traits) == 0 {
		traits = []string{dc.Trait}
	}
	dynVtableRoots(info, traits, concrete, enqueue)
}

var watHelperDeps = map[string][]string{
	"map_new":                     {"map_new_impl", "__map_drop_values", "__map_drop_impl"},
	"__method_Map_len":            {"__map_len_impl"},
	"__method_Map_contains":       {"__map_has_impl", "__map_lookup", "__map_hash"},
	"__method_Map_get":            {"__map_get_impl", "__map_lookup", "__map_hash"},
	"__method_Map_get_or":         {"__map_get_or_impl", "__map_lookup", "__map_hash"},
	"__method_Map_set":            {"__map_set_impl", "__map_grow", "__map_hash", "__map_lookup_val", "__map_clone"},
	"__method_Map_remove":         {"__map_delete_impl", "__map_hash", "__map_clone"},
	"__method_Map_contains_keyed": {"__map_has_keyed_impl", "__map_lookup_keyed"},
	"__method_Map_get_keyed":      {"__map_get_keyed_impl", "__map_lookup_keyed"},
	"__method_Map_get_or_keyed":   {"__map_get_or_keyed_impl", "__map_lookup_keyed"},
	"__method_Map_set_keyed":      {"__map_set_keyed_impl", "__map_grow_keyed", "__map_lookup_val_keyed", "__map_clone"},
	"__method_Map_remove_keyed":   {"__map_delete_keyed_impl", "__map_clone"},
	"__method_Map_clear":          {"__map_clear_impl", "__map_clone"},
	"__method_Map_keys":           {"__map_keys_impl", "__map_i32_column", "__map_bool_column", "__map_u8_column"},
	"__method_Map_values":         {"__map_values_impl", "__map_i32_column", "__map_bool_column", "__map_u8_column"},
	"__method_Map_iter":           {"__map_iter_impl"},
	"__method_MapIter_has_next":   {"__mapiter_has_next_impl"},
	"__method_MapIter_key":        {"__mapiter_key_impl", "__mapiter_entry_addr"},
	"__method_MapIter_value":      {"__mapiter_value_impl", "__mapiter_entry_addr"},
	"__method_MapIter_advance":    {"__mapiter_advance_impl"},
}

func keyedMapMethod(name string) bool {
	switch name {
	case "__method_Map_set", "__method_Map_get", "__method_Map_get_or",
		"__method_Map_contains", "__method_Map_remove":
		return true
	}
	return false
}

func enqueueKeyedMapDeps(method string, kType ast.Type, info *checker.Info, enqueue func(string)) {
	name := nominalKeyName(kType)
	if name == "" {
		return
	}
	enqueue(method + "_keyed")
	enqueue(implMethodName(info, name, "hash", "Hash"))
	enqueue(implMethodName(info, name, "eq", "Eq"))
}

func implMethodName(info *checker.Info, typeName, method, trait string) string {
	if fn, _, ok := info.ResolveMethod(typeName, method, []string{trait}); ok && fn != "" {
		return fn
	}
	return "__method_" + typeName + "_" + method
}

func nominalKeyName(t ast.Type) string {
	switch x := t.(type) {
	case ast.StructType:
		return x.Name
	case ast.EnumType:
		return x.Name
	}
	return ""
}

func Run(prog *ast.Program, info *checker.Info, extras ...string) {
	if len(prog.Funcs) == 0 {
		return
	}
	byName := map[string]*ast.FuncDecl{}
	for _, fn := range prog.Funcs {
		byName[fn.Name] = fn
	}
	reachable := map[string]bool{}
	seen := map[string]bool{}
	var queue []string
	enqueue := func(name string) {
		if name == "" {
			return
		}
		if !seen[name] {
			seen[name] = true
			for _, dep := range watHelperDeps[name] {
				if !seen[dep] {
					seen[dep] = true
					if _, ok := byName[dep]; ok && !reachable[dep] {
						reachable[dep] = true
						queue = append(queue, dep)
					}
				}
			}
		}
		if reachable[name] {
			return
		}
		if _, ok := byName[name]; !ok {
			return
		}
		reachable[name] = true
		queue = append(queue, name)
	}
	enqueue("main")
	enqueue(checker.WasiHandleName)
	enqueue("__state_init")
	for _, name := range extras {
		enqueue(name)
	}
	hasEntry := reachable["main"] || reachable[checker.WasiHandleName]
	if !hasEntry {
		for _, fn := range prog.Funcs {
			enqueue(fn.Name)
		}
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		fn := byName[name]
		if fn == nil || fn.Body == nil {
			continue
		}
		walkStmt(fn.Body, byName, info, enqueue)
	}
	out := prog.Funcs[:0]
	for _, fn := range prog.Funcs {
		if reachable[fn.Name] {
			out = append(out, fn)
		}
	}
	prog.Funcs = out
}

func walkStmt(s ast.Stmt, byName map[string]*ast.FuncDecl, info *checker.Info, enqueue func(string)) {
	switch x := s.(type) {
	case *ast.Block:
		for _, st := range x.Stmts {
			walkStmt(st, byName, info, enqueue)
		}
	case *ast.If:
		walkExpr(x.Cond, byName, info, enqueue)
		walkStmt(x.Then, byName, info, enqueue)
		if x.Else != nil {
			walkStmt(x.Else, byName, info, enqueue)
		}
	case *ast.While:
		walkExpr(x.Cond, byName, info, enqueue)
		walkStmt(x.Body, byName, info, enqueue)
	case *ast.Loop:
		walkStmt(x.Body, byName, info, enqueue)
	case *ast.For:
		if x.Init != nil {
			walkStmt(x.Init, byName, info, enqueue)
		}
		walkExpr(x.Cond, byName, info, enqueue)
		if x.Step != nil {
			walkStmt(x.Step, byName, info, enqueue)
		}
		walkStmt(x.Body, byName, info, enqueue)
	case *ast.Return:
		if x.Value != nil {
			walkExpr(x.Value, byName, info, enqueue)
		}
	case *ast.Var:
		walkExpr(x.Init, byName, info, enqueue)
	case *ast.Destructure:
		walkExpr(x.Init, byName, info, enqueue)
	case *ast.ExprStmt:
		walkExpr(x.Expr, byName, info, enqueue)
	case *ast.Match:
		walkExpr(x.Tag, byName, info, enqueue)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				walkExpr(arm.Guard, byName, info, enqueue)
			}
			walkStmt(arm.Body, byName, info, enqueue)
		}
	case *ast.Defer:
		walkExpr(x.Expr, byName, info, enqueue)
	case *ast.FuncDecl:
		if x.Body != nil {
			walkStmt(x.Body, byName, info, enqueue)
		}
	}
}

func walkExpr(e ast.Expr, byName map[string]*ast.FuncDecl, info *checker.Info, enqueue func(string)) {
	if e == nil {
		return
	}
	if info != nil {
		if dc, ok := info.DynCoercions[e]; ok {
			dynVtableRoots(info, coercionTraits(dc), dc.Concrete, enqueue)
		}
	}
	switch x := e.(type) {
	case *ast.Ident:
		enqueue(x.Name)
	case *ast.Call:
		walkExpr(x.Callee, byName, info, enqueue)
		if id, ok := x.Callee.(*ast.Ident); ok && keyedMapMethod(id.Name) && len(x.TypeArgs) >= 1 {
			enqueueKeyedMapDeps(id.Name, x.TypeArgs[0], info, enqueue)
		}
		for _, a := range x.Args {
			walkExpr(a, byName, info, enqueue)
		}
	case *ast.Binary:
		walkExpr(x.Left, byName, info, enqueue)
		walkExpr(x.Right, byName, info, enqueue)
	case *ast.Unary:
		walkExpr(x.Operand, byName, info, enqueue)
	case *ast.IfExpr:
		walkExpr(x.Cond, byName, info, enqueue)
		walkExpr(x.Then, byName, info, enqueue)
		walkExpr(x.Else, byName, info, enqueue)
	case *ast.TryOp:
		walkExpr(x.Inner, byName, info, enqueue)
	case *ast.MatchExpr:
		walkExpr(x.Tag, byName, info, enqueue)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				walkExpr(arm.Guard, byName, info, enqueue)
			}
			walkExpr(arm.Body, byName, info, enqueue)
		}
	case *ast.BlockExpr:
		for _, st := range x.Stmts {
			walkStmt(st, byName, info, enqueue)
		}
		if x.Tail != nil {
			walkExpr(x.Tail, byName, info, enqueue)
		}
	case *ast.Assign:
		walkExpr(x.Target, byName, info, enqueue)
		walkExpr(x.Value, byName, info, enqueue)
	case *ast.Index:
		walkExpr(x.Array, byName, info, enqueue)
		walkExpr(x.Idx, byName, info, enqueue)
	case *ast.SliceExpr:
		walkExpr(x.Source, byName, info, enqueue)
		walkExpr(x.Low, byName, info, enqueue)
		walkExpr(x.High, byName, info, enqueue)
	case *ast.FieldAccess:
		walkExpr(x.Target, byName, info, enqueue)
	case *ast.ArrayLit:
		for _, el := range x.Elems {
			walkExpr(el, byName, info, enqueue)
		}
	case *ast.StructLit:
		for _, f := range x.Fields {
			walkExpr(f.Value, byName, info, enqueue)
		}
		if x.Base != nil {
			walkExpr(x.Base, byName, info, enqueue)
		}
	case *ast.MapLit:
		enqueue("map_new")
		enqueue("__method_Map_set")
		enqueueKeyedMapDeps("__method_Map_set", x.KeyType, info, enqueue)
		for _, en := range x.Entries {
			walkExpr(en.Key, byName, info, enqueue)
			walkExpr(en.Value, byName, info, enqueue)
		}
	case *ast.TupleLit:
		for _, el := range x.Elems {
			walkExpr(el, byName, info, enqueue)
		}
	case *ast.EnumLit:
		for _, p := range x.Args {
			walkExpr(p, byName, info, enqueue)
		}
	case *ast.CastExpr:
		walkExpr(x.Inner, byName, info, enqueue)
	case *ast.DowncastExpr:
		downcastRoots(x, info, enqueue)
		walkExpr(x.Inner, byName, info, enqueue)
	case *ast.MakeClosure:
		enqueue(x.FuncName)
		for _, c := range x.Captures {
			walkExpr(c, byName, info, enqueue)
		}
	case *ast.Lambda:
		walkStmt(x.Body, byName, info, enqueue)
	case *ast.CaptureRef:
	case *ast.FString:
		for _, p := range x.Parts {
			if p.Expr != nil {
				walkExpr(p.Expr, byName, info, enqueue)
			}
		}
		walkExpr(x.Desugared, byName, info, enqueue)
	}
}

func DropImplMethods(info *checker.Info) []string {
	if info == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for trait, types := range info.Impls {
		simple := trait
		if i := strings.LastIndex(simple, "__"); i >= 0 {
			simple = simple[i+2:]
		}
		if simple != "Drop" {
			continue
		}
		for typeName, impld := range types {
			if !impld {
				continue
			}
			fn := implMethodName(info, typeName, "drop", trait)
			if !seen[fn] {
				seen[fn] = true
				out = append(out, fn)
			}
		}
	}
	sort.Strings(out)
	return out
}
