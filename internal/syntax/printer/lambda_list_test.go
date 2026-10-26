package printer

import (
	"github.com/jakechampion/lang/internal/syntax/parser"
	"strings"
	"testing"
)

func TestFormatLambdaListGroups(t *testing.T) {
	for _, source := range []string{
		`function main(): i32 { let work = (): i32 => call((): i32 => { let n = 1; return n; }, 2); return work(); }`,
		`function main(): i32 { let work = (): i32 => { let value = call((): i32 => { let n = 1; return n; }, 2); return value; }; return work(); }`,
		`function main(): i32 { let work = (): void => { call((): i32 => { let n = 1; return n; }, 2); }; return 0; }`,

		`function main(): i32 { return call((): i32 => { let n = 1; return n; }, 2); }`,
		`function main(): i32 { return call(
(): i32 => { let n = 1; return n; }, 2,
); }`,
		`function main(): i32 { let callbacks = [(): i32 => { let n = 1; return n; }, (): i32 => 2]; return 0; }`,
		`function main(): i32 { let value = Callbacks { first: (): i32 => { let n = 1; return n; }, second: (): i32 => 2 }; return 0; }`,
		`function main(): i32 { return call((): i32 => { let n = 1; return n; },
// second argument
2); }`,
		`function main(): i32 { return call((): i32 => { let n = 1; return n; }, (): i32 => { let n = 2; return n; }, 3); }`,
		`function main(): i32 { return call((point @ P { x }: P): i32 => { let n = point.x; return n + x; }, 2); }`,
	} {
		t.Run(source, func(t *testing.T) {
			written, err := parser.Parse(source)
			if err != nil {
				t.Fatal(err)
			}
			first := Format(written)
			back, err := parser.Parse(first)
			if err != nil {
				t.Fatal(err)
			}
			if ASTShape(written) != ASTShape(back) {
				t.Fatal(FirstShapeDiff(ASTShape(written), ASTShape(back)))
			}
			if strings.Contains(source, "// second argument") && !strings.Contains(first, "// second argument") {
				t.Fatal("lost argument comment")
			}
			if second := formatSrc(t, first); second != first {
				t.Fatalf("format is not stable\n--- first ---\n%s\n--- second ---\n%s", first, second)
			}
		})
	}
}
