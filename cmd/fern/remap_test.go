package main

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/literate"
)

// remapFor must map a generated position outside the tangle line map to
// the zero Position, so the diagnostic renderer falls back to the bare
// message instead of using a generated line number to index the document
// source (which would draw a caret over an unrelated prose line).
// Regression for L4 in docs/ADVERSARIAL-REVIEW-2026-06.md.
func TestRemapForOutOfRangeMapsToZero(t *testing.T) {
	lineMap := []literate.Line{{Lit: 7, ColShift: 4}} // one generated line
	remap := remapFor(lineMap)

	// In range: generated line 1 → document line 7, col shifted back.
	if got := remap(ast.Position{Line: 1, Col: 9}); got.Line != 7 || got.Col != 5 {
		t.Errorf("in-range remap = %+v, want {Line:7 Col:5}", got)
	}
	// Out of range (line 2 > len 1): zero Position triggers the bare
	// message fallback rather than indexing the document.
	if got := remap(ast.Position{Line: 2, Col: 3}); got.Line != 0 {
		t.Errorf("out-of-range remap = %+v, want zero Position (Line 0)", got)
	}
	// Below range (line 0) likewise maps to zero.
	if got := remap(ast.Position{Line: 0, Col: 1}); got.Line != 0 {
		t.Errorf("below-range remap = %+v, want zero Position", got)
	}
}
