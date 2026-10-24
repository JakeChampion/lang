package printer

import (
	"strings"
	"testing"
)

func TestDestructureKeepsTrailingComment(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"tuple", "function f(): i32 {\n  let (a, b) = (1, 2); // pair\n  return a + b;\n}\n", "let (a, b) = (1, 2);  // pair"},
		{"split tuple", "function f(): i32 {\n  let (a, b) =\n    (1, 2); // pair\n  return a + b;\n}\n", "let (a, b) = (1, 2);  // pair"},
		{"record", "struct Point { x: i32, y: i32 }\nfunction f(): i32 {\n  let Point { x, y } = Point { x: 1, y: 2 }; // coordinates\n  return x + y;\n}\n", "let Point { x, y } = Point { x: 1, y: 2 };  // coordinates"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSrc(t, tc.source)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("comment attachment changed:\n%s", got)
			}
			if again := formatSrc(t, got); again != got {
				t.Fatalf("format is not stable:\n%s", again)
			}
		})
	}
}
