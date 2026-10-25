package printer

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestFormatDelimitedControlHeaders(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"struct receiver", `for p in (Group { values: base }).values {}`, `in (Group { values: base }.values)`},
		{"map", `for (k, v) in (Map { "x": 1 }) {}`, `in (Map { "x": 1 })`},
		{"range low", `for n in (Point { x: 0 }).x..3 {}`, `in (Point { x: 0 }.x)..3`},
		{"range high", `for n in 0..(Point { x: 3 }).x {}`, `in 0..(Point { x: 3 }.x)`},
		{"if let", `if let Some(v) = (Group { value: Some(1) }).value {}`, `= (Group { value: Some(1) }.value)`},
		{"let else stays bare", `let Some(v) = (Group { value: Some(1) }).value else { return 1; };`, `= Group { value: Some(1) }.value else`},
		{"array stays delimited", `for p in [Point { x: 1 }] {}`, `in [Point { x: 1 }]`},
		{"call stays delimited", `for p in one(Point { x: 1 }) {}`, `in one(Point { x: 1 })`},
		{"field stays bare", `for p in (group).values {}`, `in group.values`},
		{"block", `for p in ({ let value = 1; [value, 2] }) {}`, `in ({`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := formatSrc(t, "function main(): i32 { "+tc.body+" return 0; }")
			if !strings.Contains(first, tc.want) {
				t.Fatalf("missing %q in:\n%s", tc.want, first)
			}
			program, err := parser.Parse(first)
			if err != nil {
				t.Fatalf("formatted source: %v\n%s", err, first)
			}
			if second := Format(program); second != first {
				t.Fatalf("format is not stable:\nfirst:\n%s\nsecond:\n%s", first, second)
			}
		})
	}
}
