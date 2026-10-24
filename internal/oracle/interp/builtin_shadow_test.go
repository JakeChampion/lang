package interp

import "testing"

func TestLocalFunctionShadowsBuiltin(t *testing.T) {
	cases := []struct{ name, source string }{
		{"closure-write", `function main(): i32 { let write: () => i32 = (): i32 => 7; return write(); }`},
		{"closure-print", `function main(): i32 { let print: () => i32 = (): i32 => 7; return print(); }`},
		{"closure-args", `function main(): i32 { let args: () => i32 = (): i32 => 7; return args(); }`},
		{"function-value", `function seven(): i32 { return 7; } function main(): i32 { let write: () => i32 = seven; return write(); }`},
		{"parameter", `function run(write: () => i32): i32 { return write(); } function main(): i32 { return run((): i32 => 7); }`},
		{"nested-function", `function main(): i32 { function write(): i32 { return 7; } return write(); }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalProgramValue(t, tc.source)
			n, ok := got.(Number)
			if !ok || n != 7 {
				t.Fatalf("got %v (%T), want 7", got, got)
			}
		})
	}
}
