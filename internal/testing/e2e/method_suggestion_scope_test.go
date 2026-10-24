package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

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
	const intRecv = `import "std/i32";
function main(): i32 {
    let a: i32[] = [3, 1, 2];
    return a.no_such_method_at_all();
}`
	const strRecv = `import "std/i32";
function main(): i32 {
    let a: string[] = ["b", "a"];
    let n: i32 = a.no_such_method_at_all();
    return n;
}`

	cases := []struct {
		method   string
		onInt    bool
		onString bool
	}{
		{"len", true, true},
		{"append", true, true},
		{"max", true, true},
		{"count", true, true},
		{"is_sorted", true, true},
		{"take", true, true},
		{"drop", true, true},
		{"reverse", true, true},
		{"distinct", true, true},
		{"sum", true, false},
		{"product", true, false},
		{"avg", true, false},
		{"median", true, false},
		{"cumsum", true, false},
		{"gcd_all", true, false},
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

func TestUnknownMethodDoesNotSuggestAnInapplicableMethod(t *testing.T) {
	msg := e043Message(t, `import "std/i32";
function main(): i32 {
    let a: i32[] = [3, 1, 2];
    let s: string = a.joim(",");
    return s.len();
}`)
	if strings.Contains(msg, `did you mean "join"`) {
		t.Errorf("suggested string[]-only `join` for an i32[] receiver: %s", msg)
	}
}

func TestUnknownMethodStillSuggestsAnApplicableMethod(t *testing.T) {
	msg := e043Message(t, `import "std/i32";
function main(): i32 {
    let a: i32[] = [3, 1, 2];
    return a.lenn();
}`)
	if !strings.Contains(msg, `did you mean "len"`) {
		t.Errorf("lost the suggestion for a plain typo of `len`: %s", msg)
	}
}
