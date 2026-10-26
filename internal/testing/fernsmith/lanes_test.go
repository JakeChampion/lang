package fernsmith

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestLaneExprExhaustionDoesNotGrow(t *testing.T) {
	for _, kind := range laneKinds {
		for op := byte(0); op < 9; op++ {
			t.Run(fmt.Sprintf("%s/op%d", kind.name, op), func(t *testing.T) {
				// The first byte selects an operation instead of a leaf.
				data := []byte{255, op}
				previous := 0
				for n := len(data); n >= 0; n-- {
					g := newByteGen(data[:n], DefaultConfig())
					var expression strings.Builder
					multiplied := false
					g.laneExpr(&expression, kind, []string{"v"}, 1, &multiplied)
					source := fmt.Sprintf("function f(v: %s): %s { return %s; }", kind.name, kind.name, expression.String())
					program, err := parser.Parse(source)
					if err != nil {
						t.Fatalf("prefix %d: %v\n%s", n, err, source)
					}
					nodes := 0
					ast.WalkProgram(program, func(ast.Node) bool { nodes++; return true })
					if n < len(data) && nodes > previous {
						t.Fatalf("prefix %d grew from %d to %d AST nodes: %s", n, previous, nodes, expression.String())
					}
					previous = nodes
				}
			})
		}
	}
}
