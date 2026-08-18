// This file is deliberately in package diag_test, not diag. WithFile's whole
// job is stamping error types declared in OTHER packages (parser, lexer,
// checker), and an in-package test cannot tell whether it works: the mutator
// interface used to name an unexported method, which every type declared
// inside diag satisfies for free and no type outside it ever can. An
// in-package test would have passed throughout the years the stamp reached
// nothing.
package diag_test

import (
	"errors"
	"testing"

	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
)

// foreignErr stands in for a loader-stamped error type declared outside diag.
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

// The real callers are what the interface exists for, so pin one concretely:
// a parse error handed to WithFile comes back knowing its file. parser.Error
// is the type modload stamps on every module it loads.
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
