package ir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// eraseStrTypes rewrites every ast.StrType (`str`, the borrowed-string view —
// #4813 / #4297 Option A) to a plain ast.StringType across the AST and the
// checker Info that the IR reads.
//
// A `str` lowers to exactly the same runtime shape as `string` (a box
// pointer; the #4294 immortal rc=-1 view box is the runtime view), so once
// the checker has enforced the view discipline (string borrows into str;
// str never silently promotes to an owned string), the backends — and the
// self-host emitter — only ever need to see StringType. Erasing here, at the
// single LowerWith choke point next to eraseHandleTypes, keeps StrType out
// of every width / store-op / value classification without threading a new
// type through the backends. Idempotent: a second run finds no StrType and
// is a no-op, so re-lowering for multiple backends is safe.
func eraseStrTypes(prog *ast.Program, info *checker.Info) {
	ast.WalkProgram(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			for i := range x.Params {
				x.Params[i].Type = eraseStr(x.Params[i].Type)
			}
			x.ReturnType = eraseStr(x.ReturnType)
			if x.Receiver != nil {
				x.Receiver.Type = eraseStr(x.Receiver.Type)
			}
		case *ast.Var:
			x.Type = eraseStr(x.Type)
		}
		return true
	})
	// Struct fields and enum variant payloads aren't visited as type slots
	// by WalkProgram (same shape as eraseHandleTypes).
	for _, sd := range prog.Structs {
		for i := range sd.Fields {
			sd.Fields[i].Type = eraseStr(sd.Fields[i].Type)
		}
	}
	for _, ed := range prog.Enums {
		for i := range ed.Variants {
			for j := range ed.Variants[i].Payloads {
				ed.Variants[i].Payloads[j] = eraseStr(ed.Variants[i].Payloads[j])
			}
		}
	}
	if info == nil {
		return
	}
	for _, sig := range info.FuncSigs {
		if sig == nil {
			continue
		}
		for i := range sig.Params {
			sig.Params[i] = eraseStr(sig.Params[i])
		}
		sig.Result = eraseStr(sig.Result)
	}
	for v, t := range info.VarTypes {
		info.VarTypes[v] = eraseStr(t)
	}
	for _, ts := range info.VariantCallPayloads {
		for i := range ts {
			ts[i] = eraseStr(ts[i])
		}
	}
}

// eraseStr returns t with every StrType (including nested inside arrays,
// slices, tuples, function types, or generic type arguments) replaced by
// StringType.
func eraseStr(t ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.StrType:
		return ast.StringType{}
	case ast.ArrayType:
		return ast.ArrayType{Elem: eraseStr(x.Elem)}
	case ast.SliceType:
		return ast.SliceType{Elem: eraseStr(x.Elem)}
	case ast.TupleType:
		elems := make([]ast.Type, len(x.Elems))
		for i := range x.Elems {
			elems[i] = eraseStr(x.Elems[i])
		}
		return ast.TupleType{Elems: elems}
	case *ast.FuncType:
		params := make([]ast.Type, len(x.Params))
		for i := range x.Params {
			params[i] = eraseStr(x.Params[i])
		}
		return &ast.FuncType{Params: params, Result: eraseStr(x.Result)}
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = eraseStr(x.Args[i])
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = eraseStr(x.Args[i])
		}
		return ast.EnumType{Name: x.Name, Args: args}
	}
	return t
}
