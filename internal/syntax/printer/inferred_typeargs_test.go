package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestInferredTypeArgsAreNotPrinted(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		unwanted string
	}{
		{
			"struct literal",
			"struct Box[T] { val: T }\nfunction main(): i32 { let b = Box { val: 1 }; return b.val; }\n",
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
				body := got[strings.Index(got, "function main"):]
				if strings.Contains(body, tc.unwanted) {
					t.Errorf("%s printed an inferred instantiation (%q) into main:\n%s", name, tc.unwanted, got)
				}
			}
		})
	}
}
