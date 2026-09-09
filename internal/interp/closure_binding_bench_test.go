package interp

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

func BenchmarkClosureBindings(b *testing.B) {
	cases := []struct{ name, src string }{
		{"locals", `function main(): i32 { var a = 1; var c = 2; var d = 3; return a + c + d; }`},
		{"capture-free", `function main(): i32 { var n = 7; var f = (): i32 => 7; return f(); }`},
		{"capture", `function main(): i32 { var n = 7; var unused = 99; var f = (): i32 => n; return f(); }`},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := checker.Check(prog); err != nil {
				b.Fatal(err)
			}
			i := New()
			for _, fn := range prog.Funcs {
				i.Register(fn)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := i.CallByName("main", nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
