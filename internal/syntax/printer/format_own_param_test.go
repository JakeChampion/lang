package printer

import (
	"strings"
	"testing"
)

func TestFormatKeepsOwnAtEveryParamPosition(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{
			name: "arrow_lambda_param",
			src: "function fold(own a: i32[], visit: (i32, own i32[]) => i32[]): i32[] { return visit(1, a); }\n" +
				"function main(): i32 { return fold([1], (n: i32, own x: i32[]): i32[] => x).len(); }",
			want: "(n: i32, own x: i32[]): i32[] => x",
		},
		{
			name: "nested_declaration_param",
			src: "function fold(own a: i32[], visit: (i32, own i32[]) => i32[]): i32[] { return visit(1, a); }\n" +
				"function main(): i32 {\n    function keep(n: i32, own x: i32[]): i32[] { return x; }\n    return fold([1], keep).len();\n}",
			want: "function keep(n: i32, own x: i32[]): i32[] {",
		},
		{
			name: "function_type_slot",
			src: "function fold(own a: i32[], visit: (i32, own i32[]) => i32[]): i32[] { return visit(1, a); }\n" +
				"function main(): i32 { return fold([1], (n: i32, own x: i32[]): i32[] => x).len(); }",
			want: "visit: (i32, own i32[]) => i32[]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustCheck(t, tc.src, "source")

			got := formatSrc(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Errorf("got:\n%s\nwant it to contain %q", got, tc.want)
			}
			mustCheck(t, got, "formatted output")

			if again := formatSrc(t, got); again != got {
				t.Errorf("format is not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
			}
		})
	}
}
