package parser

import (
	"strings"
	"testing"
)

func TestDelimitedHeaderStructLiterals(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"array", `for p in [Point { x: 1 }] {}`},
		{"qualified", `for p in [fixture.Point { x: 1 }] {}`},
		{"generic", `for p in [Box[i32] { value: 1 }] {}`},
		{"parenthesized", `for p in (Group { values: [1, 2] }).values {}`},
		{"tuple", `for p in (Point { x: 1 }, [1, 2]).1 {}`},
		{"call", `for p in one(Point { x: 1 }) {}`},
		{"named call", `for p in one(value = Point { x: 1 }) {}`},
		{"generic call", `for p in one[Point](Point { x: 1 }) {}`},
		{"index", `for p in rows[Point { x: 0 }.x] {}`},
		{"slice", `for p in rows[Point { x: 0 }.x:Point { x: 1 }.x] {}`},
		{"slice high", `for p in rows[:Point { x: 1 }.x] {}`},
		{"tuple binder", `for (p, n) in [(Point { x: 1 }, 2)] {}`},
		{"map", `for (k, v) in (Map { "x": Point { x: 1 } }) {}`},
		{"while call", `while (positive(Point { x: 1 })) { break; }`},
		{"if let call", `if let Some(p) = Some(Point { x: 1 }) {}`},
		{"range bound", `for n in 0..number(Point { x: 2 }) {}`},
		{"bare iterable control", `for p in values { let q = Point { x: 1 }; }`},
		{"bare field control", `for p in group.values { let q = Point { x: 1 }; }`},
		{"restored field control", `for p in (group).values { let q = Point { x: 1 }; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse("function main(): i32 { " + tc.body + " return 0; }"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDelimitedHeaderStillRequiresArrayClose(t *testing.T) {
	_, err := Parse(`function main(): i32 { for p in [Point { x: 1 } { } return 0; }`)
	if err == nil || !strings.Contains(err.Error(), `expected "]" to close this array literal`) {
		t.Fatalf("missing array delimiter: %v", err)
	}
}
