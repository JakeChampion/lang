package checker

import (
	"strings"
	"testing"
)

// The body of a range `for` cannot assign its index (#15122).
func TestRangeIndexAssignmentRejected(t *testing.T) {
	for name, src := range map[string]string{
		"compound":  `function f(): i32 { let n = 0; for i in 0..10 { n += 1; i += 1; } return n; }`,
		"plain":     `function f(): i32 { for i in 0..10 { i = 9; } return 0; }`,
		"inclusive": `function f(): i32 { for i in 0..=3 { i = 9; } return 0; }`,
		"nested":    `function f(): i32 { for i in 0..3 { for j in 0..3 { i = j; } } return 0; }`,
		"if body":   `function f(): i32 { for i in 0..3 { if (i == 1) { i = 2; } } return 0; }`,
		"captured":  `function f(): i32 { let t = 0; for i in 0..3 { let g = (): i32 => { i = 4; return i; }; t += g(); } return t; }`,
	} {
		err := checkSource(t, src)
		if err == nil || !hasCode(err, "E091") || !strings.Contains(err.Error(), "cannot assign to range index ") {
			t.Errorf("%s: want E091 for the range index, got %v", name, err)
		}
	}
}

func TestRangeIndexNeighboursAccepted(t *testing.T) {
	for name, src := range map[string]string{
		"shadowed":     `function f(): i32 { let t = 0; for i in 0..3 { let i = 5; i = 6; t += i; } return t; }`,
		"element":      `function f(): i32 { let t = 0; for x in [1, 2, 3] { x = 4; t += x; } return t; }`,
		"after loop":   `function f(): i32 { for i in 0..3 { } let i = 0; i = 3; return i; }`,
		"outer local":  `function f(): i32 { let i = 0; for k in 0..3 { i = k; } return i; }`,
		"while loop":   `function f(): i32 { let i = 0; while (i < 10) { i += 2; } return i; }`,
		"inner reader": `function f(): i32 { let t = 0; for i in 0..3 { t += i; } return t; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
