package checker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
)

// A literal-first join must not let destination context overwrite the concrete
// width of its other arm. Both source orders have the same committed type.
func TestNumericJoinDestinationCannotWidenConcreteArm(t *testing.T) {
	forms := []struct{ name, decl, tag, expr string }{
		{"literal", "", "i32", "match (tag) { 0 => %s, _ => %s }"},
		{"tuple", "", "(i32, i32)", "match (tag) { (0, _) => %s, _ => %s }"},
		{"struct", "struct Tag { n: i32 }", "Tag", "match (tag) { Tag { n: 0 } => %s, _ => %s }"},
		{"enum", "enum Tag { A, B }", "Tag", "match (tag) { A => %s, B => %s }"},
		{"if", "", "boolean", "if (tag) { %s } else { %s }"},
	}
	for _, form := range forms {
		for _, reverse := range []bool{false, true} {
			for _, destination := range []string{"i32", "i64"} {
				t.Run(fmt.Sprintf("%s/reverse=%t/%s", form.name, reverse, destination), func(t *testing.T) {
					a, b := "5", "concrete"
					if reverse {
						a, b = b, a
					}
					src := fmt.Sprintf("%s function f(tag: %s, concrete: i32): %s { var result: %s = %s; return result; }", form.decl, form.tag, destination, destination, fmt.Sprintf(form.expr, a, b))
					err := checkSource(t, src)
					if destination == "i32" {
						if err != nil {
							t.Fatalf("matching destination rejected: %v", err)
						}
					} else if err == nil || !strings.Contains(diag.Format("join.fern", src, err), "E003") {
						t.Fatalf("widening a concrete arm must report E003, got %v", err)
					}
				})
			}
		}
	}
}

// These are the literal annotations the compiled backends consume, independent
// of whether the native interpreter happens to coerce a value at another site.
func TestNumericJoinStampsResolvedArmWidths(t *testing.T) {
	forms := []struct{ name, decl, tag, expr string }{
		{"literal", "", "i32", "match (tag) { 0 => %s, _ => %s }"},
		{"tuple", "", "(i32, i32)", "match (tag) { (0, _) => %s, _ => %s }"},
		{"struct", "struct Tag { n: i32 }", "Tag", "match (tag) { Tag { n: 0 } => %s, _ => %s }"},
		{"enum", "enum Tag { A, B }", "Tag", "match (tag) { A => %s, B => %s }"},
		{"if", "", "boolean", "if (tag) { %s } else { %s }"},
	}
	numerics := []struct {
		ty, literal string
		width       int
		unsigned    bool
	}{
		{"f32", "13.25", 32, false},
		{"f64", "13.25", 64, false},
		{"i64", "12345", 64, false},
		{"u64", "12345", 64, true},
	}
	for _, form := range forms {
		for _, numeric := range numerics {
			for _, wrap := range []string{"%s", "(%s, 3)", "(3, (%s, 4))", "({ var marker = 1; (3, (%s, 4)) })"} {
				for _, reverse := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/reverse=%t", form.name, numeric.ty, wrap, reverse), func(t *testing.T) {
						a, b := fmt.Sprintf(wrap, numeric.literal), fmt.Sprintf(wrap, "concrete")
						if reverse {
							a, b = b, a
						}
						src := fmt.Sprintf("%s function f(tag: %s, concrete: %s): i32 { var result = %s; return 0; }", form.decl, form.tag, numeric.ty, fmt.Sprintf(form.expr, a, b))
						prog, err := parser.Parse(src)
						if err != nil {
							t.Fatal(err)
						}
						if _, err := Check(prog); err != nil {
							t.Fatal(err)
						}
						found := 0
						ast.WalkProgram(prog, func(node ast.Node) bool {
							switch lit := node.(type) {
							case *ast.FloatLit:
								if lit.Value == 13.25 {
									found++
									if lit.Width != numeric.width {
										t.Errorf("float literal width = %d, want %d", lit.Width, numeric.width)
									}
								}
							case *ast.NumberLit:
								if lit.Value == 12345 {
									found++
									if lit.Width != numeric.width || lit.IsUnsigned != numeric.unsigned {
										t.Errorf("integer width/signedness = %d/%t, want %d/%t", lit.Width, lit.IsUnsigned, numeric.width, numeric.unsigned)
									}
								}
							}
							return true
						})
						if found != 1 {
							t.Fatalf("found %d result literals, want 1", found)
						}
					})
				}
			}
		}
	}
}

func TestNumericJoinKeepsUnconstrainedLiteralsPolymorphic(t *testing.T) {
	for _, expr := range []string{
		"if (tag) { 13.25 } else { 14.5 }",
		"match (tag) { true => 13.25, _ => 14.5 }",
		"if (tag) { (13.25, 3) } else { (14.5, 4) }",
		"match (tag) { true => (13.25, 3), _ => (14.5, 4) }",
	} {
		t.Run(expr, func(t *testing.T) {
			prog, err := parser.Parse("function f(tag: boolean): i32 { var value = " + expr + "; return 0; }")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Check(prog); err != nil {
				t.Fatal(err)
			}
			found := 0
			ast.WalkProgram(prog, func(node ast.Node) bool {
				if lit, ok := node.(*ast.FloatLit); ok {
					found++
					if lit.Width != 0 {
						t.Errorf("unconstrained float width = %d, want unsettled", lit.Width)
					}
				}
				return true
			})
			if found != 2 {
				t.Fatalf("found %d floats, want 2", found)
			}
		})
	}
}
