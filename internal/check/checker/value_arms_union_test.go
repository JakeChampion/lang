package checker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestValueArmsWidenToExpectedUnion(t *testing.T) {
	const decls = "struct A { n: i32 } struct B { s: string } type U = A | B; enum Tag { X, Y } struct Box { n: i32 } function take(u: U): i32 { return 0; } "
	values := []struct{ name, param, expr string }{
		{"literal-match", "k: i32", "match (k) { 0 => A { n: 1 }, _ => B { s: \"b\" } }"},
		{"tuple-match", "k: (i32, i32)", "match (k) { (0, _) => A { n: 1 }, _ => B { s: \"b\" } }"},
		{"struct-match", "k: Box", "match (k) { Box { n: 0 } => A { n: 1 }, _ => B { s: \"b\" } }"},
		{"enum-match", "k: Tag", "match (k) { X => A { n: 1 }, Y => B { s: \"b\" } }"},
		{"union-match", "k: U", "match (k) { A(a) => B { s: \"b\" }, B(b) => A { n: 1 } }"},
		{"block-arms", "k: i32", "match (k) { 0 => { let n: i32 = 1; A { n: n } }, _ => { B { s: \"b\" } } }"},
		{"if", "k: boolean", "if (k) { A { n: 1 } } else { B { s: \"b\" } }"},
		{"else-if", "k: i32", "if (k == 0) { A { n: 1 } } else if (k == 1) { B { s: \"b\" } } else { A { n: 2 } }"},
	}
	positions := []struct{ name, body string }{
		{"return", "return %s;"},
		{"let", "let u: U = %s; return 0;"},
		{"argument", "take(%s); return 0;"},
		{"assign", "let u: U = A { n: 0 }; u = %s; return 0;"},
	}
	for _, v := range values {
		for _, p := range positions {
			t.Run(v.name+"/"+p.name, func(t *testing.T) {
				ret := "i32"
				if p.name == "return" {
					ret = "U"
				}
				src := fmt.Sprintf("%sfunction f(%s): %s { %s }", decls, v.param, ret, fmt.Sprintf(p.body, v.expr))
				if err := checkSource(t, src); err != nil {
					t.Fatal(diag.Format("arms.fern", src, err))
				}
			})
		}
		t.Run(v.name+"/no-expected-type", func(t *testing.T) {
			src := fmt.Sprintf("%sfunction f(%s): i32 { let u = %s; return 0; }", decls, v.param, v.expr)
			err := checkSource(t, src)
			if err == nil || !strings.Contains(diag.Format("arms.fern", src, err), "E031") {
				t.Fatalf("arms with no expected type must report E031, got %v", err)
			}
		})
	}
}
