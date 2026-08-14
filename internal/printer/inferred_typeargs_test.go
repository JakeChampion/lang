package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// The checker stamps its INFERRED instantiation onto the very fields the
// parser fills for a written one — `StructLit.TypeArgs` and
// `Call.TypeArgs`. Printing those unconditionally would write an
// instantiation into source that never named one, which is the same
// data-loss bug as dropping a written one, pointing the other way. Both
// printers gate on TypeArgsWritten; this pins that they do, by checking
// first (so the inferred args are present on the AST) and then printing.
func TestInferredTypeArgsAreNotPrinted(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		unwanted string
	}{
		{
			"struct literal",
			"struct Box[T] { val: T }\nfunction main(): i32 { var b = Box { val: 1 }; return b.val; }\n",
			"Box[",
		},
		{
			"generic call",
			"function id[T](x: T): T { return x; }\nfunction main(): i32 { return id(1); }\n",
			"id[",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := checker.Check(prog); err != nil {
				t.Fatalf("check: %v", err)
			}
			for name, got := range map[string]string{"Print": Print(prog), "Format": Format(prog)} {
				// The decl's own `[T]` is legitimate; only the
				// use sites must stay bare.
				body := got[strings.Index(got, "function main"):]
				if strings.Contains(body, tc.unwanted) {
					t.Errorf("%s printed an inferred instantiation (%q) into main:\n%s", name, tc.unwanted, got)
				}
			}
		})
	}
}
