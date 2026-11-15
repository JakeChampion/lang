package checker

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const pairDecl = "function pair[T](x: T): (T, T) {\n\treturn (x, x);\n}\n"

// nestedPairs is a call of pair that nests to depth levels.
func nestedPairs(depth int) string {
	return pairDecl + "function main(): i32 {\n\tlet x = " +
		strings.Repeat("pair(", depth) + "1" + strings.Repeat(")", depth) +
		";\n\treturn 0;\n}\n"
}

// tupleLets is a chain of lets that each hold the let before two times.
func tupleLets(steps int) string {
	var b strings.Builder
	b.WriteString("function main(): i32 {\n\tlet a0 = (1, 2);\n")
	for i := 1; i <= steps; i++ {
		fmt.Fprintf(&b, "\tlet a%d = (a%d, a%d);\n", i, i-1, i-1)
	}
	b.WriteString("\treturn 0;\n}\n")
	return b.String()
}

// A type of 4095 parts checks. The call that doubles it is the one E106, and
// no call around it reports a diagnostic.
func TestTypeSizeLimitOnNestedGenericCalls(t *testing.T) {
	if msg := checkSrc(t, nestedPairs(11)); msg != "" {
		t.Fatalf("11 levels: %s", msg)
	}
	start := time.Now()
	msg := checkSrc(t, nestedPairs(40))
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("40 levels took %v", took)
	}
	if n := strings.Count(msg, "type has more than 4096 parts"); n != 1 {
		t.Errorf("want one E106, got %d:\n%s", n, msg)
	}
	if strings.Contains(msg, "could not infer") {
		t.Errorf("an outer call reported E040:\n%s", msg)
	}
}

func TestTypeSizeLimitOnTupleLets(t *testing.T) {
	if msg := checkSrc(t, tupleLets(10)); msg != "" {
		t.Fatalf("10 steps: %s", msg)
	}
	msg := checkSrc(t, tupleLets(40))
	if !strings.Contains(msg, "type has more than 4096 parts") {
		t.Errorf("want E106, got:\n%s", msg)
	}
}

// A generic call does not report its type parameter when the argument that
// binds the parameter has an error of its own.
func TestFailedArgumentDoesNotReportTypeParameter(t *testing.T) {
	msg := checkSrc(t, pairDecl+"function main(): i32 {\n\tlet x = pair(pair(nope));\n\treturn 0;\n}\n")
	if !strings.Contains(msg, `cannot find "nope"`) {
		t.Errorf("want E001, got:\n%s", msg)
	}
	if strings.Contains(msg, "could not infer") {
		t.Errorf("want no E040, got:\n%s", msg)
	}
}

// nested is a main function whose let has depth levels of open and close
// around core, with the annotation written before the value.
func nested(annotation, open, core, close string, depth int) string {
	return "function main(): i32 {\n\tlet x" + annotation + " = " +
		strings.Repeat(open, depth) + core + strings.Repeat(close, depth) + ";\n\treturn 0;\n}\n"
}

// A type of 64 levels checks. One level more is the one E106, on the first
// value that is over the limit, and the check of a much deeper value ends.
func TestTypeDepthLimit(t *testing.T) {
	deepType := func(depth int) string {
		return ": " + strings.Repeat("Option[", depth) + "i32" + strings.Repeat("]", depth)
	}
	cases := []struct {
		name   string
		accept string
		refuse string
		deep   string
	}{
		{"constructor", nested("", "Some(", "1", ")", 63), nested("", "Some(", "1", ")", 64), nested("", "Some(", "1", ")", 2000)},
		{"array literal", nested("", "[", "1", "]", 63), nested("", "[", "1", "]", 64), nested("", "[", "1", "]", 2000)},
		{"tuple", nested("", "(1, ", "2", ")", 63), nested("", "(1, ", "2", ")", 64), nested("", "(1, ", "2", ")", 2000)},
		{"written type", nested(deepType(63), "", "None", "", 0), nested(deepType(64), "", "None", "", 0), nested(deepType(2000), "", "None", "", 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if msg := checkSrc(t, tc.accept); msg != "" {
				t.Fatalf("64 levels: %s", msg)
			}
			msg := checkSrc(t, tc.refuse)
			if n := strings.Count(msg, "type nests deeper than 64 levels"); n != 1 {
				t.Errorf("65 levels: want one E106 for the depth, got %d:\n%s", n, msg)
			}
			start := time.Now()
			msg = checkSrc(t, tc.deep)
			if took := time.Since(start); took > 5*time.Second {
				t.Errorf("2000 levels took %v", took)
			}
			if n := strings.Count(msg, "type nests deeper than 64 levels"); n != 1 {
				t.Errorf("2000 levels: want one E106, got %d", n)
			}
		})
	}
}
