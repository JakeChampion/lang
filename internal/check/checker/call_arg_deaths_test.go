package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestCallArgDeathsFieldReads(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"single field", "let a = b.x; consume(a);", true},
		{"different field", "let a = b.x; inspect(b.y); consume(a);", true},
		{"repeated field", "let a = b.x; inspect(b.x); consume(a);", false},
		{"whole owner", "let a = b.x; inspect(b); consume(a);", false},
		{"nested field", "let a = b.x; inspect(b.x.y); consume(a);", false},
		{"captured field", "let a = b.x; let f = (): i32 => b.x.len(); consume(a);", false},
		{"shadowed owner", "let a = b.x; { let b = Box { x: [3], y: [4] }; inspect(b.y); } consume(a);", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse("struct Box { x: i32[], y: i32[] } function f(): void { let b = Box { x: [1], y: [2] }; " + tc.body + " }")
			if err != nil {
				t.Fatal(err)
			}
			deaths := CallArgDeaths(prog.Funcs[0], nil)
			got := false
			for _, names := range deaths.Dies {
				got = got || names["a"]
			}
			if got != tc.want {
				t.Fatalf("field alias is transferable = %v, want %v", got, tc.want)
			}
		})
	}
}
