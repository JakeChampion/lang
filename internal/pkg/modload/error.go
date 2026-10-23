package modload

import (
	"fmt"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// importError is an import the loader cannot resolve (E086), or a reference
// into an imported module that the import rewriter refuses (E087): a name the
// module does not export, or does not declare. It carries its position and
// file, so it renders and publishes like a checker error.
type importError struct {
	path string // the module the import or reference is written in
	pos  ast.Position
	code string
	msg  string
}

func (e *importError) Error() string          { return fmt.Sprintf("import error at %s: %s", e.pos, e.msg) }
func (e *importError) Position() ast.Position { return e.pos }
func (e *importError) File() string           { return e.path }
func (e *importError) Code() string           { return e.code }
