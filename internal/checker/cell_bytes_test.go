package checker

import (
	"strings"
	"testing"
)

func TestByteCellElementBoundary(t *testing.T) {
	for _, src := range []string{
		"let c: Cell[u8[]] = cell_new([0 as u8, 255 as u8]); c.set(c.get());",
		"let bytes: u8[] = []; let c = cell_new(bytes); c.set([255 as u8]);",
	} {
		if err := checkSource(t, "function main(): i32 { "+src+" return 0; }"); err != nil {
			t.Fatal(err)
		}
	}
	for _, ty := range []string{"i32[]", "u8[][]", "[u8]", "Cell[u8[]]", "(u8, u8)"} {
		t.Run(ty, func(t *testing.T) {
			err := checkSource(t, "function f(c: Cell["+ty+"]): i32 { return 0; } function main(): i32 { return 0; }")
			if err == nil || !strings.Contains(err.Error(), "must be a scalar") {
				t.Fatalf("expected cell element refusal, got %v", err)
			}
		})
	}
}
