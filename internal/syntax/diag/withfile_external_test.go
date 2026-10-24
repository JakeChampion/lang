package diag_test

import (
	"errors"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

type foreignErr struct{ path string }

func (e *foreignErr) Error() string    { return "boom" }
func (e *foreignErr) File() string     { return e.path }
func (e *foreignErr) SetFile(p string) { e.path = p }

var _ diag.FileSetter = (*foreignErr)(nil)

func TestWithFileStampsErrorsDeclaredOutsideDiag(t *testing.T) {
	one := &foreignErr{}
	if got := diag.WithFile(one, "a.fern"); got != error(one) {
		t.Fatalf("WithFile should return the same error it stamped, got %v", got)
	}
	if one.File() != "a.fern" {
		t.Fatalf("single error not stamped: File() = %q", one.File())
	}

	first, second := &foreignErr{}, &foreignErr{}
	diag.WithFile(diag.Errors{first, errors.New("unstampable"), second}, "b.fern")
	for i, e := range []*foreignErr{first, second} {
		if e.File() != "b.fern" {
			t.Fatalf("Errors entry %d not stamped: File() = %q", i, e.File())
		}
	}
}

func TestWithFileStampsAParseError(t *testing.T) {
	_, err := parser.Parse("function f(): i32 { return 1 +; }\n")
	if err == nil {
		t.Fatal("expected a parse error")
	}
	for i, one := range diag.WithFile(err, "mod.fern").(diag.Errors) {
		f, ok := one.(diag.Filed)
		if !ok {
			t.Fatalf("parse error %d (%T) is not diag.Filed", i, one)
		}
		if f.File() != "mod.fern" {
			t.Fatalf("parse error %d not stamped: File() = %q", i, f.File())
		}
	}
}
