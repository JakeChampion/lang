package monomorph_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

func TestEnumConstructionContractsAfterMonomorph(t *testing.T) {
	prog, err := parser.Parse(`struct Box[T] { value: T }
enum Wrapped[T] { Full(Box[T]), Empty }
function wrap[T](x: T): Option[T] { return Some(x); }
function empty[T](x: T): Wrapped[T] { return Empty; }
function main(): i32 {
    var a = wrap(1i64);
    var b = wrap("hello");
    var c = empty(2i64);
    var d: Wrapped[string] = Full(Box { value: "hi" });
    return 0;
}`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatal(err)
	}
	reachable := make(map[ast.Expr]bool)
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if e, ok := n.(ast.Expr); ok {
			reachable[e] = true
		}
		return true
	})
	var options []string
	wrapped := 0
	for expr, construction := range info.EnumConstructions {
		if !reachable[expr] {
			t.Fatalf("contract retained a removed generic body: %T", expr)
		}
		decl := info.Enums[construction.Type.Name]
		if decl == nil || len(decl.TypeParams) != len(construction.Type.Args) {
			t.Fatalf("incomplete constructor type: %+v", construction)
		}
		switch {
		case construction.Type.Name == "Option":
			options = append(options, construction.Type.String())
		case strings.HasPrefix(construction.Type.Name, "Wrapped__"):
			wrapped++
		default:
			t.Fatalf("unexpected construction: %+v", construction)
		}
	}
	slices.Sort(options)
	if !slices.Equal(options, []string{"Option[i64]", "Option[string]"}) || wrapped != 2 {
		t.Fatalf("options=%v, cloned enum constructions=%d", options, wrapped)
	}
}
