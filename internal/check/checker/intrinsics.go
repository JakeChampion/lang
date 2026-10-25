package checker

import "github.com/jakechampion/lang/internal/syntax/ast"

type IntrinsicKind uint8

const (
	IntrinsicNone IntrinsicKind = iota
	IntrinsicArrayAppend
	IntrinsicExit
)

type IntrinsicCall struct {
	Kind      IntrinsicKind
	Signature *ast.FuncType
}
