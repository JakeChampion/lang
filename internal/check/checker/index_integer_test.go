package checker

import (
	"strings"
	"testing"
)

func TestIndexTakesAnyIntegerType(t *testing.T) {
	for _, ty := range []string{"i32", "i64", "u8", "u32", "u64", "usize"} {
		src := `function f(xs: i32[], s: string, i: ` + ty + `): i32 { let b: u8 = s[i]; return xs[i] + b as i32; }
function main(): i32 { return 0; }`
		if err := checkSource(t, src); err != nil {
			t.Errorf("%s index: %v", ty, err)
		}
	}
	for _, ty := range []string{"f64", "boolean", "string", "char"} {
		src := `function f(xs: i32[], i: ` + ty + `): i32 { return xs[i]; }
function main(): i32 { return 0; }`
		err := checkSource(t, src)
		if err == nil || !strings.Contains(err.Error(), "index must be an integer, got "+ty) {
			t.Errorf("%s index: want E034, got %v", ty, err)
		}
	}
}
