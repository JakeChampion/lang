package monomorph_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestCollectionReceiverInstancesSurviveRecheck(t *testing.T) {
	for _, tc := range []struct{ name, impl, values string }{
		{"array", `impl[T] Value for T[] {
  function get(self: T[]): i32 { return self.len(); }
}`, `let a = Wrapper { value: [1] }; let b = Wrapper { value: ["x"] };`},
		{"tuple", `impl[A, B] Value for (A, B) {
  function get(self: (A, B)): i32 { return 1; }
}`, `let a = Wrapper { value: (1, true) }; let b = Wrapper { value: ("x", 2) };`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `trait Value { function get(self: Self): i32; }
struct Wrapper[T] { value: T }
impl[T: Value] Value for Wrapper[T] {
  function get(self: Wrapper[T]): i32 { return self.value.get(); }
}
` + tc.impl + `
function main(): i32 { ` + tc.values + ` return a.get() + b.get(); }`
			prog, err := parser.Parse(source)
			if err != nil {
				t.Fatal(err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatal(err)
			}
			if err := monomorph.Run(prog, info); err != nil {
				t.Fatalf("collection method selected another receiver instance: %v", err)
			}
		})
	}
}
