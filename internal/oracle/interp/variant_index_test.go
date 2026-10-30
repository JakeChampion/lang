package interp

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestFindVariantOnIndex(t *testing.T) {
	i := New()
	i.RegisterEnum(&ast.EnumDecl{Name: "E", Variants: []ast.EnumVariant{{Name: "A"}, {Name: "B"}}})
	i.RegisterEnum(&ast.EnumDecl{Name: "F", Variants: []ast.EnumVariant{{Name: "B"}, {Name: "C"}}})
	for _, c := range []struct {
		name, enum, want string
		idx              int
	}{
		{"A", "", "E", 0},
		{"C", "", "F", 1},
		{"B", "F", "F", 0},
		{"B", "E", "E", 1},
		{"B", "", "", 0},
		{"D", "", "", 0},
	} {
		ed, idx, ok := i.findVariantOn(c.name, c.enum)
		got := ""
		if ok {
			got = ed.Name
		}
		if got != c.want || ok && idx != c.idx {
			t.Errorf("findVariantOn(%q, %q) = %q %d, want %q %d", c.name, c.enum, got, idx, c.want, c.idx)
		}
	}
	i.RegisterEnum(&ast.EnumDecl{Name: "G", Variants: []ast.EnumVariant{{Name: "A"}}})
	if _, _, ok := i.findVariantOn("A", ""); ok {
		t.Errorf("findVariantOn(\"A\", \"\") found a variant after a second enum declared A")
	}
}
