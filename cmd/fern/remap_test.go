package main

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/tools/literate"
)

func TestRemapForOutOfRangeMapsToZero(t *testing.T) {
	lineMap := []literate.Line{{Lit: 7, ColShift: 4}}
	remap := remapFor(lineMap)

	if got := remap(ast.Position{Line: 1, Col: 9}); got.Line != 7 || got.Col != 5 {
		t.Errorf("in-range remap = %+v, want {Line:7 Col:5}", got)
	}
	if got := remap(ast.Position{Line: 2, Col: 3}); got.Line != 0 {
		t.Errorf("out-of-range remap = %+v, want zero Position (Line 0)", got)
	}
	if got := remap(ast.Position{Line: 0, Col: 1}); got.Line != 0 {
		t.Errorf("below-range remap = %+v, want zero Position", got)
	}
}
