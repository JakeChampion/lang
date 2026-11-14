package checker

import (
	"strings"
	"testing"
)

// An array element whose type parameter nothing infers yet takes the type of
// a known element, as the self-hosted checker does.
func TestArrayOpenElementTakesTheKnownType(t *testing.T) {
	decls := `enum Msg { Go, Stop } struct El[M] { tap: Option[M] }
function text[M](s: string): El[M] { return El { tap: None }; }
function button(m: Msg): El[Msg] { return El { tap: Some(m) }; }
function row[M](kids: El[M][]): El[M] { return kids[0]; }
`
	for _, body := range []string{
		`let t = row([button(Go), row([text("a"), text("b")])]);`,
		`let t = row([row([text("a"), text("b")]), button(Go)]);`,
		`let t = row([button(Go), text("a"), row([text("b")])]); let k: El[Msg][] = [t];`,
	} {
		if err := checkSource(t, decls+`function main(): i32 { `+body+` return 0; }`); err != nil {
			t.Errorf("%s: %v", body, err)
		}
	}
	// A type parameter of the function that holds the array is a type of its
	// own, not an open one.
	err := checkSource(t, decls+`function both[M](e: El[M]): i32 { let t = [e, button(Go)]; return 0; } function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "array element: expected El[M], got El[Msg]") {
		t.Errorf("a type parameter in scope must not take the known type; got: %v", err)
	}
}
