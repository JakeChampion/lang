package checker

import (
	"strings"
	"testing"
)

func TestCharOrdering(t *testing.T) {
	for _, op := range []string{"<", "<=", ">", ">="} {
		t.Run(op, func(t *testing.T) {
			if err := checkSource(t, "function f(a: char, b: char): boolean { return a "+op+" b; }"); err != nil {
				t.Fatal(err)
			}
			for _, types := range [][2]string{{"char", "i32"}, {"i32", "char"}, {"char", "u8"}, {"char", "f64"}} {
				err := checkSource(t, "function f(a: "+types[0]+", b: "+types[1]+"): boolean { return a "+op+" b; }")
				if err == nil || !strings.Contains(err.Error(), "operator") {
					t.Errorf("%s %s %s: want an operator error, got %v", types[0], op, types[1], err)
				}
			}
		})
	}
}
