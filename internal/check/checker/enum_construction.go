package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

func resultPayloadWidens(enumName string, from, to ast.Type) bool {
	src, srcOK := from.(ast.NumberType)
	dst, dstOK := to.(ast.NumberType)
	return enumName == "Result" && srcOK && dstOK && src.IsSigned() == dst.IsSigned() && src.NormalWidth() > 0 && src.NormalWidth() < dst.NormalWidth()
}

type EnumConstruction struct {
	Type         ast.EnumType
	VariantIndex int
	Payloads     []ast.Type
}

func (c *checker) recordEnumConstruction(e ast.Expr, vr variantRef, result ast.EnumType, payloads []ast.Type, expected ast.Type) {
	construction := EnumConstruction{Type: result, VariantIndex: vr.index, Payloads: payloads}
	if hint, ok := expected.(ast.EnumType); ok {
		c.settleEnumConstruction(&construction, hint)
	}
	if c.info.EnumConstructions == nil {
		c.info.EnumConstructions = make(map[ast.Expr]EnumConstruction)
	}
	c.info.EnumConstructions[e] = construction
}

func (c *checker) refineEnumConstruction(e ast.Expr, hint ast.EnumType) (EnumConstruction, bool) {
	construction, ok := c.info.EnumConstructions[e]
	if !ok || !c.settleEnumConstruction(&construction, hint) {
		return EnumConstruction{}, false
	}
	c.info.EnumConstructions[e] = construction
	return construction, true
}

func (c *checker) settleEnumConstruction(construction *EnumConstruction, hint ast.EnumType) bool {
	if construction.Type.Name != hint.Name {
		return false
	}
	ed := c.info.Enums[hint.Name]
	if ed == nil || len(ed.TypeParams) != len(hint.Args) {
		return false
	}
	sub := make(map[string]ast.Type, len(hint.Args))
	for i, param := range ed.TypeParams {
		sub[param] = hint.Args[i]
	}
	declared := ed.Variants[construction.VariantIndex].Payloads
	for i, typ := range declared {
		construction.Payloads[i] = substituteType(typ, sub)
	}
	construction.Type = hint
	return true
}
