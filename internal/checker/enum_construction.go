package checker

import "github.com/jakechampion/lang/internal/ast"

// EnumConstruction is the semantic contract of one resolved constructor.
// Type retains every result type argument, including parameters absent from
// this variant's payloads. An under-inferred constructor keeps an argless Type;
// generic bodies may retain ParamTypes until monomorph rechecks their clones.
// Consumers requiring concrete types must validate that requirement explicitly.
// Like the other Info types, this contract belongs to the checked frontend and
// must be copied before legacy lowering erases its surface types.
type EnumConstruction struct {
	Type         ast.EnumType
	VariantIndex int
	Payloads     []ast.Type
}

// recordEnumConstruction is called only after lexical/qualified resolution has
// established a constructor. Context can complete phantom parameters, but never
// change the nominal enum or turn an ordinary expression into a constructor.
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

// refineEnumConstruction follows the checker's existing contextual settlement.
// A prior resolution witness is essential: settleNumeric also runs before
// checkExpr, when a constructor-like spelling may still denote a local/function.
func (c *checker) refineEnumConstruction(e ast.Expr, hint ast.EnumType) (EnumConstruction, bool) {
	construction, ok := c.info.EnumConstructions[e]
	if !ok || !c.settleEnumConstruction(&construction, hint) {
		return EnumConstruction{}, false
	}
	c.info.EnumConstructions[e] = construction
	return construction, true
}

// Settle before the first map insertion as well as during later contextual
// refinement, without inserting and immediately looking up a partial contract.
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
