package modload

import (
	"fmt"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

type importError struct {
	path string
	pos  ast.Position
	code string
	msg  string
}

func (e *importError) Error() string          { return fmt.Sprintf("import error at %s: %s", e.pos, e.msg) }
func (e *importError) Position() ast.Position { return e.pos }
func (e *importError) File() string           { return e.path }
func (e *importError) Code() string           { return e.code }
