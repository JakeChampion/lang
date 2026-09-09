package semir

import "github.com/jakechampion/lang/internal/ast"

// valueType separates checked source values from compiler-internal availability
// sums. The payload type stays fully resolved; absence is not a value of it.
type valueType struct {
	source ast.Type
	form   valueTypeForm
}

type valueTypeForm uint8

const (
	sourceForm valueTypeForm = iota
	availabilityForm
)

func sourceValueType(typ ast.Type) valueType { return valueType{source: typ} }

func sameValueType(a, b valueType) bool {
	return a.form == b.form && ast.Equal(a.source, b.source)
}
