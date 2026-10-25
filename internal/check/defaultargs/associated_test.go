package defaultargs

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestAssociatedFunctionDoesNotReplaceFreeArguments(t *testing.T) {
	const associated = `trait Construct { function one(): Self; }
struct Other { x: i32 }
impl Construct for Other { function one(): Other { return Other { x: 7 }; } }
`
	const free = `function one(value: i32 = 42): i32 { return value; }
`
	for _, tc := range []struct{ name, declarations, call string }{
		{"named associated last", free + associated, "one(value = 42)"},
		{"named free last", associated + free, "one(value = 42)"},
		{"default associated last", free + associated, "one()"},
		{"default free last", associated + free, "one()"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program, errs := fill(t, tc.declarations+"function main(): i32 { return "+tc.call+"; }")
			if len(errs) != 0 {
				t.Fatalf("free function argument expansion: %v", errs)
			}
			args := callArgs(program, "one")
			if len(args) != 1 {
				t.Fatalf("arguments = %d, want 1", len(args))
			}
			value, ok := args[0].(*ast.NumberLit)
			if !ok || value.Value != 42 {
				t.Fatalf("expanded argument = %#v, want 42", args[0])
			}
		})
	}
}
