package parser

import (
	"strings"
	"testing"
)

func TestLoopBodiesRequireBlocks(t *testing.T) {
	for _, head := range []string{
		"while (false)", "loop", "for (let i = 0; i < 1; i = i + 1)",
		"for x in [1]", "for x in 0..1", "for x in 0..=1", "for (x, y) in [(1, 2)]",
		"outer: while (false)", "outer: loop", "outer: for x in [1]",
	} {
		t.Run(head, func(t *testing.T) {
			if _, err := Parse("function main(): i32 { " + head + " return 1; return 0; }"); err == nil || !strings.Contains(err.Error(), "{") {
				t.Fatalf("braceless loop: got %v", err)
			}
			if _, err := Parse("function main(): i32 { " + head + " { break; } return 0; }"); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := Parse("function main(): i32 { if (true) return 1; else return 0; }"); err != nil {
		t.Fatal(err)
	}
}
