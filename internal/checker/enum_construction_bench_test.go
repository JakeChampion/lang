package checker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// Include parsing: Check mutates the input, so every iteration needs a fresh
// program. The same fixture runs unchanged against the parent compiler.
func BenchmarkEnumConstructionFrontend(b *testing.B) {
	for _, enums := range []bool{false, true} {
		for _, count := range []int{1, 16, 128} {
			b.Run(fmt.Sprintf("enums-%t/values-%d", enums, count), func(b *testing.B) {
				var src strings.Builder
				src.WriteString("function main(): i32 {\n")
				for i := range count {
					if enums {
						fmt.Fprintf(&src, "var a%d: Option[i64] = Some(%d); var b%d: Result[string, boolean] = Err(true);\n", i, i, i)
					} else {
						fmt.Fprintf(&src, "var a%d: i64 = %d; var b%d: boolean = true;\n", i, i, i)
					}
				}
				src.WriteString("return 0; }")
				source := src.String()
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					prog, err := parser.Parse(source)
					if err != nil {
						b.Fatal(err)
					}
					if _, err := Check(prog); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
