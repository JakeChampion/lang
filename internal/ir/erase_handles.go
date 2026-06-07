package ir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// eraseHandleTypes rewrites every ast.HandleType (`own R` / `borrow R` — P5
// WIT resource handles, docs/WIT-BRING-YOUR-OWN.md) to a plain i32
// (ast.NumberType{}) across the AST and the checker Info that the IR reads.
//
// A resource handle is an opaque i32 at the canonical ABI, so once the checker
// has enforced handle type-safety (own-vs-borrow, "no plain i32 where a handle
// is required"), the compiled backends — and the interpreter and the self-host
// emitter — only ever need to see the scalar. Erasing here, at the single
// LowerWith choke point (mirroring rejectDynTrait), keeps HandleType out of
// every width / store-op / value classification in the backends without
// threading a new type through them. Idempotent: a second run finds no
// handles and is a no-op, so re-lowering for multiple backends is safe.
func eraseHandleTypes(prog *ast.Program, info *checker.Info) {
	ast.WalkProgram(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			for i := range x.Params {
				x.Params[i].Type = eraseHandle(x.Params[i].Type)
			}
			x.ReturnType = eraseHandle(x.ReturnType)
			if x.Receiver != nil {
				x.Receiver.Type = eraseHandle(x.Receiver.Type)
			}
		case *ast.Var:
			x.Type = eraseHandle(x.Type)
		}
		return true
	})
	// Struct fields and enum variant payloads aren't visited as type slots by
	// WalkProgram, so erase them explicitly (a handle stored in a composite
	// is still an i32 to the backend).
	for _, sd := range prog.Structs {
		for i := range sd.Fields {
			sd.Fields[i].Type = eraseHandle(sd.Fields[i].Type)
		}
	}
	for _, ed := range prog.Enums {
		for i := range ed.Variants {
			for j := range ed.Variants[i].Payloads {
				ed.Variants[i].Payloads[j] = eraseHandle(ed.Variants[i].Payloads[j])
			}
		}
	}
	if info == nil {
		return
	}
	// FuncSigs holds copies of the (resolved) signature types, read directly
	// by lowering, so it must be erased alongside the AST. VarTypes and
	// VariantCallPayloads are erased defensively — cheap, and keeps any future
	// Info-driven lowering path handle-free.
	for _, sig := range info.FuncSigs {
		if sig == nil {
			continue
		}
		for i := range sig.Params {
			sig.Params[i] = eraseHandle(sig.Params[i])
		}
		sig.Result = eraseHandle(sig.Result)
	}
	for v, t := range info.VarTypes {
		info.VarTypes[v] = eraseHandle(t)
	}
	for _, ts := range info.VariantCallPayloads {
		for i := range ts {
			ts[i] = eraseHandle(ts[i])
		}
	}
}

// eraseHandle returns t with every HandleType (including nested inside arrays,
// slices, tuples, function types, or generic type arguments) replaced by i32.
func eraseHandle(t ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.HandleType:
		return ast.NumberType{}
	case ast.ArrayType:
		return ast.ArrayType{Elem: eraseHandle(x.Elem)}
	case ast.SliceType:
		return ast.SliceType{Elem: eraseHandle(x.Elem)}
	case ast.TupleType:
		elems := make([]ast.Type, len(x.Elems))
		for i := range x.Elems {
			elems[i] = eraseHandle(x.Elems[i])
		}
		return ast.TupleType{Elems: elems}
	case *ast.FuncType:
		params := make([]ast.Type, len(x.Params))
		for i := range x.Params {
			params[i] = eraseHandle(x.Params[i])
		}
		return &ast.FuncType{Params: params, Result: eraseHandle(x.Result)}
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = eraseHandle(x.Args[i])
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = eraseHandle(x.Args[i])
		}
		return ast.EnumType{Name: x.Name, Args: args}
	}
	return t
}
