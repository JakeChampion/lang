package ir

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// eraseSurfaceTypes rewrites the SURFACE-ONLY types — the ones the checker
// enforces a discipline on but which lower to an existing runtime shape — to
// that shape, across the AST and the checker Info that the IR reads:
//
//   - ast.StrType (`str`, the borrowed-string view — #4813 / #4297 Option A)
//     to ast.StringType. A `str` lowers to exactly the same runtime shape as
//     `string` (a box pointer; the #4294 immortal rc=-1 view box IS the
//     runtime view).
//   - ast.CharType (`char`, the Unicode scalar value — #5629) to a 32-bit
//     signed ast.NumberType. A `char` rides an i32 slot.
//
// In both cases the checker has already enforced the discipline (string
// borrows into str and str never silently promotes; char never implicitly
// converts to or from an integer), so the backends — and the self-host
// emitter — only ever need to see the underlying shape. Erasing here, at the
// single LowerWith choke point next to eraseHandleTypes, keeps these types
// out of every width / store-op / value classification without threading new
// types through the backends. Idempotent: a second run finds neither and is a
// no-op, so re-lowering for multiple backends is safe.
func eraseSurfaceTypes(prog *ast.Program, info *checker.Info) {
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
		case *ast.Lambda:
			for i := range x.Params {
				x.Params[i].Type = eraseStr(x.Params[i].Type)
			}
			x.ReturnType = eraseStr(x.ReturnType)
		case *ast.CastExpr:
			// Type slots inside an EXPRESSION, not just a declaration.
			// `str` never needed this (nothing casts to a view), but a
			// cast is the only way to produce a `char`, so leaving these
			// unerased hands the backends a `cast from char to i32`
			// they have no lowering for.
			x.Target = eraseStr(x.Target)
			x.InnerType = eraseStr(x.InnerType)
		case *ast.ArrayLit:
			// The checker stamps the settled element type here and the
			// array lowering reads it. Leaving `str` unerased made the
			// elements of a `str[]` literal miss the refcount increment
			// an owned `string` element gets — a view is borrowed, so the
			// lowering skips it — and the array then held pointers whose
			// storage was freed underneath it (#5695). Silently wrong on
			// x86-64, a segfault on arm64.
			//
			// Only `str` is rewritten, NOT the whole surface set: `char`
			// classifies at pointer width here, every other stride site
			// for a `char[]` agrees with that, and erasing it to i32
			// narrows the stride to 4 at this site alone — which breaks
			// `char[]` exactly the way this fixes `str[]`.
			x.ElemType = eraseStrViewOnly(x.ElemType)
		case *ast.Index:
			// Same slot on the read side: the element load has to agree
			// with how the literal above stored them.
			x.ElemType = eraseStrViewOnly(x.ElemType)
		case *ast.SliceExpr:
			x.ElemType = eraseStrViewOnly(x.ElemType)
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

// eraseStr returns t with every surface-only type (including nested inside
// arrays, slices, tuples, function types, or generic type arguments) replaced
// by its runtime shape: StrType by StringType, CharType by i32.
func eraseStr(t ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.StrType:
		return ast.StringType{}
	case ast.CharType:
		return ast.NumberType{Width: 32}
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

// eraseStrViewOnly rewrites `str` to `string` and leaves every other
// surface type alone, recursing the same way eraseStr does.
//
// It exists for the checker-stamped ElemType slots on expression nodes,
// where the two surface types must be treated differently: `str` and
// `string` share a runtime shape so rewriting is a no-op for width and a
// fix for ownership, while `char` is classified at pointer width by the
// stride sites that read these slots and rewriting it to i32 would make
// this one site disagree with the rest (#5695).
func eraseStrViewOnly(t ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.StrType:
		return ast.StringType{}
	case ast.ArrayType:
		return ast.ArrayType{Elem: eraseStrViewOnly(x.Elem)}
	case ast.SliceType:
		return ast.SliceType{Elem: eraseStrViewOnly(x.Elem)}
	case ast.TupleType:
		elems := make([]ast.Type, len(x.Elems))
		for i := range x.Elems {
			elems[i] = eraseStrViewOnly(x.Elems[i])
		}
		return ast.TupleType{Elems: elems}
	}
	return t
}
