package checker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/diag"
)

func TestFloatOrderingRequiresCompatibleWidths(t *testing.T) {
	for _, op := range []string{"<", "<=", ">", ">="} {
		for _, operands := range [][2]string{{"a", "b"}, {"b", "a"}} {
			t.Run(op+operands[0], func(t *testing.T) {
				src := fmt.Sprintf("function f(a: f32, b: f64): boolean { return %s %s %s; }", operands[0], op, operands[1])
				err := checkSource(t, src)
				if err == nil || strings.Count(diag.Format("order.fern", src, err), "error[E009]") != 1 {
					t.Fatalf("want one width diagnostic, got %v", err)
				}
			})
		}
		for _, expr := range []string{"a " + op + " 0.5", "0.5 " + op + " a", "a " + op + " (b as f32)", "(a as f64) " + op + " b"} {
			t.Run(expr, func(t *testing.T) {
				if err := checkSource(t, "function f(a: f32, b: f64): boolean { return "+expr+"; }"); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
