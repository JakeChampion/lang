package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// An integer literal past i64 max settled into float context carries its
// magnitude in Value as a wrapped bit pattern. Converted as signed it lowered
// to a negative constant: `var f: f64 = 9223372036854775808` was -2^63 on
// every compiled backend.
func TestWideIntLiteralInFloatContextLowersPositive(t *testing.T) {
	src := `function main(): i32 {
		var f: f64 = 9223372036854775808;
		var g: f32 = 18446744073709551615;
		if (f > 0.0 && g > 0.0) { return 1; }
		return 0;
	}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	ip, err := LowerWith(prog, info, 8)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	var sawF64, sawF32 bool
	for _, fn := range ip.Funcs {
		if fn == nil || fn.Name != "main" {
			continue
		}
		for _, op := range fn.Ops {
			switch op.Kind {
			case OpConstF64:
				if op.F64 == 9223372036854775808.0 {
					sawF64 = true
				} else if op.F64 < 0 {
					t.Errorf("f64 const %v lowered negative", op.F64)
				}
			case OpConstF32:
				if op.F32 == 18446744073709551615.0 {
					sawF32 = true
				} else if op.F32 < 0 {
					t.Errorf("f32 const %v lowered negative", op.F32)
				}
			}
		}
	}
	if !sawF64 || !sawF32 {
		t.Errorf("did not see both literals as positive float consts (f64=%v f32=%v)", sawF64, sawF32)
	}
}
