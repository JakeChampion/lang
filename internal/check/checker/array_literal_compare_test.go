package checker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// An array literal on one side of == takes the element type of the array on
// the other side, as the self-host checker gives it. The program does not
// import core/cmp, so the check stops later at the Eq requirement.
func TestArrayLiteralComparesAtTheOtherSideElemType(t *testing.T) {
	for _, side := range []string{"xs == [1.5, -2.25]", "[1.5, -2.25] != xs"} {
		t.Run(side, func(t *testing.T) {
			prog, err := parser.Parse("function main(): i32 {\n    let xs: f32[] = [1.5, -2.25];\n    let same: boolean = " + side + ";\n    return 0;\n}\n")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := Check(prog); err != nil && strings.Contains(err.Error(), "must have the same type") {
				t.Fatalf("check: %v", err)
			}
			var lit *ast.ArrayLit
			ast.WalkProgram(prog, func(n ast.Node) bool {
				if b, ok := n.(*ast.Binary); ok {
					if al, ok := b.Right.(*ast.ArrayLit); ok {
						lit = al
					}
					if al, ok := b.Left.(*ast.ArrayLit); ok {
						lit = al
					}
				}
				return true
			})
			if lit == nil {
				t.Fatal("no array literal in the comparison")
			}
			if got := fmt.Sprint(lit.ElemType); got != "f32" {
				t.Errorf("literal elements are %s, want f32", got)
			}
		})
	}
	msg := checkSrc(t, "function main(): i32 {\n    let xs: f32[] = [1.5];\n    let s: boolean = xs == [\"a\"];\n    return 0;\n}\n")
	if !strings.Contains(msg, "cannot compare f32[] with string[]") {
		t.Errorf("a string literal against f32[]: got %q, want the mismatch", msg)
	}
}
