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

// TestASCIIGutterFallback asserts --ascii swaps the box-drawing gutter for
// a plain `|`, and that the default (UTF-8) keeps `│` (#4413 Rec §7).
func TestASCIIGutterFallback(t *testing.T) {
	defer SetColor(SetColor(true))
	err := &fakeCodedErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 1, Col: 23}, msg: "undefined identifier \"x\""},
		code:    "E001",
	}
	defer SetASCII(SetASCII(true))
	if !ASCIIEnabled() {
		t.Fatal("ASCIIEnabled should report the fallback is on after SetASCII(true)")
	}
	out := Format("", "function f() { return x; }\n", err)
	if !strings.Contains(out, " 1 "+paint(ansiBlue, "|")) {
		t.Errorf("ascii mode should use a plain | gutter, got:\n%q", out)
	}
	if strings.Contains(out, "│") {
		t.Errorf("ascii mode should not emit the box-drawing │, got:\n%q", out)
	}
	SetASCII(false)
	if ASCIIEnabled() {
		t.Fatal("ASCIIEnabled should report the fallback is off after SetASCII(false)")
	}
	out = Format("", "function f() { return x; }\n", err)
	if !strings.Contains(out, "│") {
		t.Errorf("default (UTF-8) should use the box-drawing │, got:\n%q", out)
	}
}

// TestColorGuttersSecondaryLabel asserts a multi-label diagnostic's
// secondary line also gets a line-number gutter in rich mode, so the whole
// diagnostic reads as one consistently-guttered block (#4413 Rec §7).
func TestColorGuttersSecondaryLabel(t *testing.T) {
	defer SetColor(SetColor(true))
	src := "function f(): i32 {\n    var x: i32 = 1;\n    x = \"oops\";\n    return x;\n}\n"
	err := &fakeLabeledErr{
		fakeErr: fakeErr{pos: ast.Position{Line: 3, Col: 9}, msg: "cannot assign string to i32"},
		labels: []Label{
			{Pos: ast.Position{Line: 3, Col: 9}, Length: 6, Message: "expected i32 here", Kind: LabelPrimary},
			{Pos: ast.Position{Line: 2, Col: 12}, Length: 3, Message: "declared with this type", Kind: LabelSecondary},
		},
	}
	out := Format("", src, err)
	// The secondary (line 2) source line carries its own gutter.
	if !strings.Contains(out, " 2 "+paint(ansiBlue, "│")) {
		t.Errorf("secondary label should be guttered in rich mode, got:\n%q", out)
	}
	// Its underline stays `-` (distinct from the primary caret).
	if !strings.Contains(out, paint(ansiBlue, "---")) {
		t.Errorf("secondary underline should be blue `---`, got:\n%q", out)
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
