package constfold

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestFoldIntegerLiteralIntoFloat(t *testing.T) {
	for _, ty := range []string{"f32", "f64"} {
		t.Run(ty, func(t *testing.T) {
			p := fold(t, "const X: "+ty+" = -5; function main(): "+ty+" { return X; }")
			v, ok := returnLit(t, p).(*ast.FloatLit)
			if !ok || v.Value != -5 || v.Width != map[string]int{"f32": 32, "f64": 64}[ty] {
				t.Fatalf("want a typed float literal, got %#v", returnLit(t, p))
			}
		})
	}
}

func TestFoldStringAndCharOrdering(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want bool
	}{
		{`"a" < "b"`, true}, {`"a" <= "a"`, true},
		{`"é" > "z"`, true}, {`"β" >= "β"`, true},
		{`"b" < "a"`, false}, {`"ab" >= "abc"`, false},
		{`'a' < 'b'`, true}, {`'a' <= 'a'`, true},
		{`'🦊' > 'z'`, true}, {`'β' >= 'β'`, true},
		{`'b' < 'a'`, false}, {`'β' != 'β'`, false},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			p := fold(t, "const X: boolean = "+tc.expr+"; function main(): boolean { return X; }")
			v, ok := returnLit(t, p).(*ast.BoolLit)
			if !ok || v.Value != tc.want {
				t.Fatalf("want %v, got %#v", tc.want, returnLit(t, p))
			}
		})
	}
}

func TestFoldFloatDestinationSettlesArithmeticOperands(t *testing.T) {
	p := fold(t, "const X: f32 = 5 / 2; function main(): f32 { return X; }")
	v, ok := returnLit(t, p).(*ast.FloatLit)
	if !ok || v.Value != 2.5 || v.Width != 32 {
		t.Fatalf("want f32 2.5, got %#v", returnLit(t, p))
	}
}
