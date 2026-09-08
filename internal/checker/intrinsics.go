package checker

import "github.com/jakechampion/lang/internal/ast"

// IntrinsicKind identifies a resolved semantic operation, independently of the
// mangled spelling retained for legacy lowering. It describes behavior, not an
// ownership proof or a choice of runtime helper.
type IntrinsicKind uint8

const (
	IntrinsicNone IntrinsicKind = iota
	IntrinsicArrayAppend
)

// IntrinsicCall carries the instantiated signature of a checked intrinsic.
// Keeping this in Info avoids enlarging every AST call and preserves full
// semantic types without making the typed IR decode helper names.
type IntrinsicCall struct {
	Kind      IntrinsicKind
	Signature *ast.FuncType
}
