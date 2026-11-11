package constfold

import (
	"fmt"
	"strings"
	"testing"
)

// doubledConsts is a chain of consts that each use the const before two
// times, so the value doubles at each step.
func doubledConsts(first, typ string, grow func(i int, typ string) (string, string), steps int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "const C0: %s = %s;\n", typ, first)
	for i := 1; i <= steps; i++ {
		var init string
		typ, init = grow(i, typ)
		fmt.Fprintf(&b, "const C%d: %s = %s;\n", i, typ, init)
	}
	b.WriteString("function main(): i32 { return 0; }\n")
	return b.String()
}

func doubledString(steps int) string {
	return doubledConsts(`"aaaaaaaaaaaaaaaa"`, "string", func(i int, typ string) (string, string) {
		return typ, fmt.Sprintf("C%d + C%d", i-1, i-1)
	}, steps)
}

func doubledArray(steps int) string {
	return doubledConsts("[1, 2]", "i32[]", func(i int, typ string) (string, string) {
		return typ + "[]", fmt.Sprintf("[C%d, C%d]", i-1, i-1)
	}, steps)
}

// A const string of 1 MiB folds, and the const that doubles it is refused. No
// later const makes a larger string.
func TestConstStringSizeLimit(t *testing.T) {
	fold(t, doubledString(16))
	msg := foldErr(t, doubledString(40))
	if !strings.Contains(msg, "const C17: string of more than 1048576 bytes in constant expression") {
		t.Fatalf("got %q, want the size limit on C17", msg)
	}
}

// A const aggregate of 1048576 scalars folds, and the const that doubles it
// is refused.
func TestConstAggregateSizeLimit(t *testing.T) {
	fold(t, doubledArray(19))
	msg := foldErr(t, doubledArray(40))
	if !strings.Contains(msg, "const C20: aggregate of more than 1048576 elements in constant expression") {
		t.Fatalf("got %q, want the size limit on C20", msg)
	}
}
