package interp

import (
	"fmt"
	"testing"
)

func TestNumericJoinSettlesLiteralArms(t *testing.T) {
	shapes := []struct{ name, decl, expr, first, second string }{
		{"literal", "", "match (%s) { 0 => %s, _ => %s }", "0", "1"},
		{"tuple", "", "match (%s) { (0, _) => %s, _ => %s }", "(0, 0)", "(1, 0)"},
		{"struct", "struct Tag { n: i32 }", "match (%s) { Tag { n: 0 } => %s, _ => %s }", "Tag { n: 0 }", "Tag { n: 1 }"},
		{"enum", "enum Tag { A, B }", "match (%s) { A => %s, B => %s }", "Tag.A", "Tag.B"},
		{"if", "", "if (%s) { %s } else { %s }", "true", "false"},
	}
	values := []struct{ name, literal, concrete, projection string }{
		{"scalar", "16777216.0", "narrow", "v"},
		{"tuple", "(16777216.0, 3)", "(narrow, 3)", "v.0"},
		{"nested tuple", "(3, (16777216.0, 4))", "(3, (narrow, 4))", "v.1.0"},
	}
	for _, shape := range shapes {
		for _, value := range values {
			for _, reverse := range []bool{false, true} {
				for _, selector := range []string{shape.first, shape.second} {
					t.Run(fmt.Sprintf("%s/%s/reverse=%t/select=%s", shape.name, value.name, reverse, selector), func(t *testing.T) {
						a, b := value.literal, value.concrete
						if reverse {
							a, b = b, a
						}
						expr := fmt.Sprintf(shape.expr, selector, a, b)
						// Both branches have the same numeric value. Only their source
						// types differ: one is polymorphic, the other concrete f32.
						src := fmt.Sprintf("%s function main(): i32 { var narrow: f32 = 16777216.0; var v = %s; if (%s + 1.0 == %s) { return 7; } return 99; }", shape.decl, expr, value.projection, value.projection)
						got := evalProgramValue(t, src)
						if n, ok := got.(Number); !ok || n != 7 {
							t.Fatalf("resolved f32 join must round arithmetic to f32: got %v, want 7\n%s", got, src)
						}
					})
				}
			}
		}
	}
}
