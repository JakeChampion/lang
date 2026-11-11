package checker

import (
	"strings"
	"testing"
)

const genericArrayArgDecls = `struct El[M] { kids: El[M][], tap: Option[M] }
function text[M](s: string): El[M] { return El { kids: [], tap: None }; }
function button[M](s: string, m: M): El[M] { return El { kids: [], tap: Some(m) }; }
function column[M](kids: El[M][]): El[M] { return El { kids: kids, tap: None }; }
enum Msg { Inc, Dec }
`

// The expected type of a generic call gives the element type to an array
// literal argument (#15292).
func TestGenericCallGivesItsExpectedTypeToAnArrayArgument(t *testing.T) {
	ok := map[string]string{
		"return":      `function view(): El[Msg] { return column([text("a"), text("b")]); }`,
		"binding":     `function view(): i32 { let e: El[Msg] = column([text("a")]); return e.kids.len(); }`,
		"mixed":       `function view(): El[Msg] { return column([text("a"), button("+", Inc)]); }`,
		"bound first": `function view(): El[Msg] { return column([button("+", Inc), text("a")]); }`,
	}
	for name, src := range ok {
		t.Run(name, func(t *testing.T) {
			if err := checkSource(t, genericArrayArgDecls+src+"\nfunction main(): i32 { return 0; }"); err != nil {
				t.Errorf("should type-check: %v", err)
			}
		})
	}
	bad := map[string]struct{ src, want string }{
		"argument binds": {`function view(): El[Msg] { return column([button("+", 1)]); }`, "expected El[Msg], got El[i32]"},
	}
	for name, tc := range bad {
		t.Run(name, func(t *testing.T) {
			err := checkSource(t, genericArrayArgDecls+tc.src+"\nfunction main(): i32 { return 0; }")
			if err == nil {
				t.Fatalf("want an error containing %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want an error containing %q, got:\n%v", tc.want, err)
			}
		})
	}
}
