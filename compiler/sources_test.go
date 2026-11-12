package compiler

import (
	"io/fs"
	"strings"
	"testing"
)

func TestSourcesHoldNoTestDrivers(t *testing.T) {
	names, err := fs.Glob(Sources, "*")
	if err != nil {
		t.Fatal(err)
	}
	var sawEntry bool
	for _, n := range names {
		if n == "fern.fern" {
			sawEntry = true
		}
		if strings.HasSuffix(n, "_run.fern") && n != "playground_run.fern" {
			t.Errorf("%s is embedded; test drivers belong in compiler/drivers/", n)
		}
	}
	if !sawEntry {
		t.Errorf("fern.fern is not embedded among %d files", len(names))
	}
}

// `ast.binary` and `ast.unary` calculate the literal class of a node, so a
// node from a struct literal has a class that nothing calculated.
func TestOperatorNodesComeFromTheirConstructors(t *testing.T) {
	names, err := fs.Glob(Sources, "*.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		src, err := fs.ReadFile(Sources, n)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if !strings.Contains(line, "ExprBinary {") && !strings.Contains(line, "ExprUnary {") {
				continue
			}
			if n == "ast.fern" && constructorLine(line) {
				continue
			}
			t.Errorf("%s:%d builds an operator node from a struct literal; use ast.binary or ast.unary", n, i+1)
		}
	}
}

// constructorLine is true for the declaration of an operator node and for a
// line of one of its two constructors.
func constructorLine(line string) bool {
	for _, start := range []string{"pub struct Expr", "pub function binary(", "pub function unary(", "  return Expr"} {
		if strings.HasPrefix(line, start) {
			return true
		}
	}
	return false
}
