package ast

import "strings"

// The monomorphiser names each generic instantiation it clones with
// MangleInst, and the checker reads the same names back when it compares a
// generic spelling with the clone that replaced it.

// MangleInst generates a unique name for a given
// instantiation. Format: `<base>__<arg1>__<arg2>…`. Type names
// come from Type.String() which is already
// `i32` / `f32` / `Foo` / `Foo[i32]` style. Brackets and commas
// are stripped to keep the result a single identifier the rest
// of the pipeline accepts.
func MangleInst(base string, args []Type) string {
	var b strings.Builder
	b.WriteString(base)
	for _, a := range args {
		b.WriteString("__")
		b.WriteString(MangleArg(a))
	}
	return b.String()
}

// MangleArg renders one type argument into a symbol token.
//
// The subtlety it solves: a nested generic instantiation like
// `Box[Box[i32]]` reaches `MangleInst` from two directions. The
// type-slot rewriter (rewriteType) recurses inner-first, so the
// outer call sees its arg ALREADY rewritten to a flat
// `StructType{Name:"Box__i32"}` (no Args). The struct-literal path
// instead passes the arg RAW as `StructType{Name:"Box", Args:[i32]}`.
// If we mangled via `SanitizeName(a.String())` those two render
// differently ("Box__i32" vs "Box_i32_"), the clone names diverge,
// and the trailing re-check fails with "cannot assign Box__Box_i32_
// to Box__Box__i32".
//
// Recursing through generic nominal types (and the composite shapes
// that can contain them) makes both directions converge: a raw
// `Box[i32]` arg mangles via `MangleInst("Box", [i32])` → "Box__i32",
// matching the pre-flattened form's `sanitize("Box__i32")`.
func MangleArg(t Type) string {
	switch x := t.(type) {
	case StructType:
		if len(x.Args) > 0 {
			return MangleInst(x.Name, x.Args)
		}
	case EnumType:
		if len(x.Args) > 0 {
			return MangleInst(x.Name, x.Args)
		}
	case ArrayType:
		return MangleArg(x.Elem) + "_arr"
	case SliceType:
		return MangleArg(x.Elem) + "_slice"
	case TupleType:
		var b strings.Builder
		b.WriteString("tup")
		for _, e := range x.Elems {
			b.WriteString("_")
			b.WriteString(MangleArg(e))
		}
		return b.String()
	}
	return SanitizeName(t.String())
}

// SanitizeName maps a type's String() form into a token safe to embed
// in a backend symbol name. Any character outside [A-Za-z0-9_] is
// replaced with '_' — this covers the bracket/comma/space of
// generic + slice forms (`Foo[Bar]`, `i32[]`) AND the parentheses
// of a tuple type (`(i32, i32)`), whose literal `(`/`)` would
// otherwise survive into the assembler symbol and fail to
// assemble on the native backends (wasm tolerated them).
func SanitizeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		isAlnum := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '_'
		if !isAlnum {
			out = append(out, '_')
			continue
		}
		out = append(out, c)
	}
	return string(out)
}
