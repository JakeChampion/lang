package e2ecompiler

import "testing"

// TestSelfHostVariantTestReadsShapeAsImmediateX86_64 pins the variant test's
// operand: the shape address a match arm compares the scrutinee's tag against
// is a sign-extended imm32 of the compare, never loaded with a rip-relative
// lea first. The executable loads at a fixed address under 2^31, which is
// what makes the address an immediate; the test runs the program as well, so
// the assembler's absolute relocation is exercised, not only the text.
//
// A shape whose address is read for something other than a compare keeps the
// lea: construction stores it into the new box. The payload comes from the
// parameter so the construction is one, not a constant aggregate.
func TestSelfHostVariantTestReadsShapeAsImmediateX86_64(t *testing.T) {
	runX86ShapeCases(t, []shapeCase{
		{
			name: "match-arms",
			src: `enum Shape { Circle(i32), Square(i32), Dot }
@noinline function area(s: Shape): i32 {
    match (s) {
        Shape.Circle(r) => { return 3 * r * r; },
        Shape.Square(w) => { return w * w; },
        Shape.Dot => { return 1; }
    }
    return 0;
}
@noinline function make(k: i32): Shape {
    if (k == 0) { return Shape.Circle(k + 2); }
    if (k == 1) { return Shape.Square(k + 2); }
    return Shape.Dot;
}
function main(): i32 { return area(make(0)) + area(make(1)) + area(make(2)); }`,
			want: 12 + 9 + 1,
			has: map[string][]string{
				"area": {`\n    cmpq \$\.S[0-9]+, %r[a-z0-9]+\n`},
				"make": {`\n    leaq \.S[0-9]+\(%rip\), %r[a-z0-9]+\n`},
			},
			lacks: map[string][]string{
				"area": {`leaq \.S[0-9]+\(%rip\)`},
				"make": {`cmpq \$\.S`},
			},
		},
	})
}
