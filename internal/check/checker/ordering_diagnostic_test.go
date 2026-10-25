package checker

import (
	"strings"
	"testing"
)

func TestOrderingMismatchKeepsBooleanResult(t *testing.T) {
	for _, op := range []string{"<", "<=", ">", ">="} {
		for _, body := range []string{
			"let x: i32 = 1 " + op + " true; return 0;",
			"return 1 " + op + " true;",
		} {
			t.Run(body, func(t *testing.T) {
				err := checkSource(t, "function main(): i32 { "+body+" }")
				if err == nil || !strings.Contains(err.Error(), "requires matching numeric or string operands, got i32 and boolean") || !strings.Contains(err.Error(), "expected i32, got boolean") {
					t.Fatalf("want operand and result errors, got %v", err)
				}
			})
		}
	}
}
