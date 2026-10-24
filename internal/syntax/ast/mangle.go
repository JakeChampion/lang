package ast

import "strings"

func MangleInst(base string, args []Type) string {
	var b strings.Builder
	b.WriteString(base)
	for _, a := range args {
		b.WriteString("__")
		b.WriteString(MangleArg(a))
	}
	return b.String()
}

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
