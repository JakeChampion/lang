package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
)

// E043's "it has:" list and its did-you-mean must be scoped to the methods
// THIS receiver can call. A collection namespace is shared across element
// types — `Array` holds `avg` (i32[] only), `join` (string[] only) and `len`
// (anything) side by side — so an unfiltered list advertises to an `i32[]`
// receiver an API it does not have.
//
// That is not merely noisy: following one of those names turns the E043 into
// an E038, which is worse than the original typo, because the reader now
// believes the method exists.
//
// Since #2663 the list also has to get the OTHER direction right. The verbs
// that are element-polymorphic (`max` / `count` / `sorted_asc` /
// `take` / `drop` / `reverse` / `distinct`) really do apply to both
// receivers, so a filter that still keyed them by element type would now be
// hiding a method that exists. Both kinds of row are checked below.

func e043Message(t *testing.T, src string) string {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, cerr := checker.Check(prog)
	if cerr == nil {
		t.Fatal("expected an E043, got no error")
	}
	return cerr.Error()
}

func TestUnknownMethodListsOnlyApplicableMethods(t *testing.T) {
	// `std/i32` is imported so the full stdlib Array namespace is registered;
	// without it the list is just the three builtins and proves nothing.
	const intRecv = `import "std/i32";
function main(): i32 {
    var a: i32[] = [3, 1, 2];
    return a.no_such_method_at_all();
}`
	const strRecv = `import "std/i32";
function main(): i32 {
    var a: string[] = ["b", "a"];
    var n: i32 = a.no_such_method_at_all();
    return n;
}`

	// Each name is checked in the list it belongs to AND in the one it does
	// not, so a filter that dropped everything would fail as loudly as one
	// that dropped nothing.
	cases := []struct {
		method   string
		onInt    bool // applicable to i32[]
		onString bool // applicable to string[]
	}{
		{"len", true, true},
		{"append", true, true},
		// Element-polymorphic since #2663: applicable to BOTH receivers.
		{"max", true, true},
		{"count", true, true},
		{"sorted_asc", true, true},
		{"take", true, true},
		{"drop", true, true},
		{"reverse", true, true},
		{"distinct", true, true},
		// Genuinely element-specific: integer division / an i32 identity.
		{"sum", true, false},
		{"product", true, false},
		{"avg", true, false},
		{"median", true, false},
		{"cumsum", true, false},
		{"gcd_all", true, false},
		// Genuinely element-specific: about strings.
		{"join", false, true},
		{"sum_lens", false, true},
		{"all_starts_with", false, true},
	}

	intMsg := e043Message(t, intRecv)
	strMsg := e043Message(t, strRecv)
	for _, c := range cases {
		if listed(intMsg, c.method) != c.onInt {
			t.Errorf("i32[]: %q listed=%v, want %v", c.method, !c.onInt, c.onInt)
		}
		if listed(strMsg, c.method) != c.onString {
			t.Errorf("string[]: %q listed=%v, want %v", c.method, !c.onString, c.onString)
		}
	}
}

// listed reports whether the diagnostic's comma-separated "(it has: …)" list
// contains exactly this name. Substring matching would not do: `sum` occurs
// inside `sum_by`, `cumsum` and `sum_lens`.
func listed(msg, method string) bool {
	open := strings.Index(msg, "(it has: ")
	if open < 0 {
		return false
	}
	rest := msg[open+len("(it has: "):]
	close := strings.Index(rest, ")")
	if close < 0 {
		return false
	}
	for _, name := range strings.Split(rest[:close], ", ") {
		if name == method {
			return true
		}
	}
	return false
}

// The did-you-mean is filtered too. Suggesting a method the receiver cannot
// call sends the reader to an E038 one compile later, having taught them a
// method exists when it does not.
func TestUnknownMethodDoesNotSuggestAnInapplicableMethod(t *testing.T) {
	msg := e043Message(t, `import "std/i32";
function main(): i32 {
    var a: i32[] = [3, 1, 2];
    var s: string = a.joim(",");
    return s.len();
}`)
	if strings.Contains(msg, `did you mean "join"`) {
		t.Errorf("suggested string[]-only `join` for an i32[] receiver: %s", msg)
	}
}

// …and a typo of a method the receiver DOES have is still corrected. The
// filter must only remove names it can prove inapplicable.
func TestUnknownMethodStillSuggestsAnApplicableMethod(t *testing.T) {
	msg := e043Message(t, `import "std/i32";
function main(): i32 {
    var a: i32[] = [3, 1, 2];
    return a.lenn();
}`)
	if !strings.Contains(msg, `did you mean "len"`) {
		t.Errorf("lost the suggestion for a plain typo of `len`: %s", msg)
	}
}
