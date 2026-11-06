package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestLiteralArrayInferenceDoesNotChangeTupleReceiver(t *testing.T) {
	const methods = `function (t: (i32, i32)) append(v: u8): i32 { let (a, b) = t; return a+b+(v as i32); }
function (t: (i32, i32)) with(index: i32, v: u8): i32 { let (a, b) = t; return a+b+index+(v as i32); }
`
	for _, tc := range []struct{ name, call string }{
		{"append", "t.append(v)"},
		{"with", "t.with(0, v)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := parser.Parse(methods + "function main(): i32 { let t = (1, 2); let v: u8 = 3; return " + tc.call + "; }")
			if err != nil {
				t.Fatal(err)
			}
			info, err := Check(p)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, fn := range p.Funcs {
				if fn.Name != "main" {
					continue
				}
				for _, stmt := range fn.Body.Stmts {
					if v, ok := stmt.(*ast.Var); ok && v.Name == "t" {
						found = true
						if got := info.VarTypes[v].String(); got != "(i32, i32)" {
							t.Fatalf("tuple type = %s, want (i32, i32)", got)
						}
					}
				}
			}
			if !found {
				t.Fatal("tuple declaration was not checked")
			}
		})
	}
}
