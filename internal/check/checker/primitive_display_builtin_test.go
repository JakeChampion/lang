package checker

import (
	"strings"
	"testing"
)

func TestPrimitiveDisplayBuiltin(t *testing.T) {
	for _, ty := range []string{"i32", "i64", "u8", "u32", "u64", "usize", "f32", "f64", "boolean", "char", "string"} {
		t.Run(ty, func(t *testing.T) {
			src := `function show(x: ` + ty + `): string { print(x); write(x); eprint(x); write(f"{x}"); return x.to_string(); } function main(): i32 { return 0; }`
			if err := checkSource(t, src); err != nil {
				t.Fatal(err)
			}
			src = `function (x: ` + ty + `) to_string(): i32 { return 1; } function show(x: ` + ty + `): i32 { return x.to_string(); } function main(): i32 { return 0; }`
			if err := checkSource(t, src); err != nil {
				t.Fatal(err)
			}
			src = `function show(x: ` + ty + `): string { return x.to_string(1); } function main(): i32 { return 0; }`
			if err := checkSource(t, src); err == nil {
				t.Fatal("wrong arity was accepted")
			}
		})
	}
	src := `function __primitive_i32_to_string(n: i32): string { return "a"; } function __primitive_i32_to_string(n: i32): string { return "b"; } function main(): i32 { return 0; }`
	if err := checkSource(t, src); err == nil || !strings.Contains(err.Error(), "redeclared") {
		t.Fatalf("duplicate formatter: %v", err)
	}
}
