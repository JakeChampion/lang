package constfold

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
)

// Error is a folding diagnostic with a position, a file and a stable code,
// so the driver renders it with the same `file:line:col: error[Ennn]` header
// and caret as a checker error.
type Error struct {
	Pos     ast.Position
	Msg     string
	Path    string
	ErrCode string
}

func (e *Error) Error() string          { return fmt.Sprintf("fold error at %s: %s", e.Pos, e.Msg) }
func (e *Error) Position() ast.Position { return e.Pos }
func (e *Error) File() string           { return e.Path }
func (e *Error) SetFile(p string)       { e.Path = p }
func (e *Error) Code() string           { return e.ErrCode }
