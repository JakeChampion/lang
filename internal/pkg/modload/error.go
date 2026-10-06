package modload

import (
	"fmt"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// importError is a reference into an imported module that the import
// rewriter refuses: a name the module does not export, or does not declare.
// It carries its position and file, so it renders and publishes like a
// checker error.
type importError struct {
	path string // the module the reference is written in
	pos  ast.Position
	msg  string
}

func (e *importError) Error() string          { return fmt.Sprintf("import error at %s: %s", e.pos, e.msg) }
func (e *importError) Position() ast.Position { return e.pos }
func (e *importError) File() string           { return e.path }
