package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/printer"
)

func TestStatementMatchValueBodies(t *testing.T) {
	if _, err := Parse(`function main(): i32 { match (1) { _ => print("ok") }; return 0; }`); err != nil {
		t.Fatal(err)
	}
	for _, arms := range []string{
		`1 => sink(1), _ => sink(0)`,
		`1 => { let n = 1; sink(n) }, _ => {}`,
		`1 => { if (true) sink(1); }, _ => { return 0; }`,
		`1 => { if (true) { sink(1); } }, _ => {}`,
		`1 => { match (2) { 2 => { sink(2); }, _ => {} } }, _ => {}`,
	} {
		src := `function main(): i32 { match (1) { ` + arms + ` } return 0; }`
		p, err := Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", arms, err)
		}
		formatted := printer.Format(p)
		again, err := Parse(formatted)
		if err != nil {
			t.Fatalf("formatted %s: %v", formatted, err)
		}
		if got := printer.Format(again); got != formatted {
			t.Fatalf("unstable format:\n%s\n%s", formatted, got)
		}
	}
	for _, arms := range []string{`1 => sink(1) _ => sink(0)`, `1 => {} _ => {}`} {
		if _, err := Parse(`function main(): i32 { match (1) { ` + arms + ` } return 0; }`); err == nil {
			t.Fatalf("missing comma accepted: %s", arms)
		}
	}
}

func TestIfLetValueExpressions(t *testing.T) {
	for _, head := range []string{`Some(n)`, `Some(Ok(n))`, `(n, _)`, `Point { x: n, y: _ }`, `pkg.E.V { x: n }`, `all @ Some(n)`, `Some(n) | Other(n)`, `1..=3`} {
		src := `function main(): i32 { let result = if let ` + head + ` = input { let k = 1; k } else { 0 }; return result; }`
		p, err := Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", head, err)
		}
		value := p.Funcs[0].Body.Stmts[0].(*ast.Var).Init
		if _, ok := value.(*ast.MatchExpr); !ok {
			t.Fatalf("value is %T", value)
		}
		formatted := printer.Format(p)
		if !strings.Contains(formatted, "if let ") {
			t.Fatalf("lost written form: %s", formatted)
		}
		again, err := Parse(formatted)
		if err != nil {
			t.Fatalf("formatted %s: %v", formatted, err)
		}
		if got := printer.Format(again); got != formatted {
			t.Fatalf("unstable format:\n%s\n%s", formatted, got)
		}
	}
	if _, err := Parse(`function main(): i32 { let n = if let Some(n) = input { n }; return n; }`); err == nil {
		t.Fatal("value if-let without else accepted")
	}
	if _, err := Parse(`function main(): i32 { return if let Some(n) = input { n } else if let Other(n) = input { n } else if (true) { 1 } else { 0 }; }`); err != nil {
		t.Fatal(err)
	}
}
