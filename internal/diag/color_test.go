package diag

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// TestColorOffIsPlainDefault asserts colour is off by default and the
// rendered output carries no ANSI escape — the invariant every
// non-interactive caller (LSP, golden/differential harnesses, piped
// `fern -check`) relies on.
func TestColorOffIsPlainDefault(t *testing.T) {
	if ColorEnabled() {
		t.Fatal("colour should be OFF by default")
	}
	err := &fakeCodedErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 23}, msg: "undefined identifier \"x\""},
		code:    "E001",
	}
	out := Format("", "function f() { return x; }\n", err)
	if strings.Contains(out, "\x1b[") {
		t.Errorf("default (colour off) output must contain no ANSI escape, got:\n%q", out)
	}
}

// TestColorWrapsPrefix asserts the error label is wrapped in the
// bold-red SGR + reset when colour is on, and that the plain text is still
// present between the escapes.
func TestColorWrapsPrefix(t *testing.T) {
	defer SetColor(SetColor(true))
	err := &fakeCodedErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 23}, msg: "undefined identifier \"x\""},
		code:    "E001",
	}
	out := Format("", "function f() { return x; }\n", err)
	if !strings.Contains(out, ansiRedBld+"error[E001]"+ansiReset) {
		t.Errorf("expected bold-red error label, got:\n%q", out)
	}
}

// TestColorWrapsCaret asserts the caret/squiggle is red under colour.
func TestColorWrapsCaret(t *testing.T) {
	defer SetColor(SetColor(true))
	err := &fakeSpanErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 23}, msg: "undefined identifier \"x\""},
		span:    3,
	}
	out := Format("", "function f() { return xyz; }\n", err)
	if !strings.Contains(out, ansiRed+"^~~"+ansiReset) {
		t.Errorf("expected red caret+squiggle, got:\n%q", out)
	}
}

// TestColorWrapsNote asserts a hint's `note:` tag is blue under colour.
func TestColorWrapsNote(t *testing.T) {
	defer SetColor(SetColor(true))
	err := &fakeHintErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 23}, msg: "undefined identifier \"x\""},
		hint:    "did you mean \"y\"?",
	}
	out := Format("", "function f() { return x; }\n", err)
	if !strings.Contains(out, ansiBlue+"note:"+ansiReset) {
		t.Errorf("expected blue note tag, got:\n%q", out)
	}
}

// TestColorRendersGutter asserts rich (colour) mode renders a
// right-aligned line-number gutter with a box-drawing separator, replacing
// the classic 4-space indent for the source + caret lines (#4413 Rec §7).
func TestColorRendersGutter(t *testing.T) {
	defer SetColor(SetColor(true))
	err := &fakeCodedErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 23}, msg: "undefined identifier \"x\""},
		code:    "E001",
	}
	out := Format("", "function f() { return x; }\n", err)
	if !strings.Contains(out, " 1 "+paint(ansiBlue, "│")) {
		t.Errorf("rich mode should render a right-aligned line-number gutter, got:\n%q", out)
	}
}

// TestSetColorReturnsPrevious pins the restore contract used by the
// defer SetColor(SetColor(true)) idiom above.
func TestSetColorReturnsPrevious(t *testing.T) {
	defer SetColor(SetColor(false))
	if prev := SetColor(true); prev {
		t.Errorf("SetColor should report the prior (off) state, got on")
	}
	if prev := SetColor(false); !prev {
		t.Errorf("SetColor should report the prior (on) state, got off")
	}
}
