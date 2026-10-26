package e2ecompiler

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A declaration under a built-in type's name is E010 and nothing else, as in
// the Go checker: the imported stdlib must not report the cascade (#13366).
func TestSelfHostCheckEntryShadowOfBuiltin(t *testing.T) {
	cli := newStrictCLI(t)
	cases := []struct{ name, src, msg string }{
		{"enum-takes-builtin-struct-name", "import \"std/string\";\nenum Span { Empty, Wide(f64, string) }\nfunction f(s: Span): i32 {\n  match (s) { Wide(d, t) => { return (d * 4.0) as i32 + t.len(); }, Empty => { return 0; } }\n}\nfunction main(): i32 { return f(Wide(1.0, \"ab\")); }\n", `enum "Span" is a reserved built-in name`},
		{"struct-takes-builtin-enum-name", "import \"std/string\";\nstruct Option { n: i32 }\nfunction main(): i32 { let o: Option = Option { n: 1 }; return o.n; }\n", `struct "Option" is a reserved built-in name`},
		{"struct-takes-builtin-struct-name", "import \"std/string\";\nstruct Span { n: i32 }\nfunction main(): i32 { let s: Span = Span { n: 1 }; return s.n; }\n", `struct "Span" is a reserved built-in name`},
		{"enum-takes-builtin-enum-name", "import \"std/string\";\nenum Option { A, B }\nfunction main(): i32 { let o: Option = A; return 0; }\n", `enum "Option" is a reserved built-in name`},
		{"qualified-enum-with-method", "import \"std/string\";\nenum Span { Empty, Wide(f64) }\nfunction (s: Span) wide(): Span { return Span.Wide(1.0); }\nfunction f(s: Span): i32 { match (s) { Span.Empty => { return 0; }, _ => { return 1; } } }\nfunction main(): i32 { return f(Span.Empty); }\n", `enum "Span" is a reserved built-in name`},
		{"later-mismatch", "import \"std/string\";\nstruct Span { n: i32 }\nfunction main(): i32 { let s: Span = Span { n: 1 }; let t: string = s; return 0; }\n", `struct "Span" is a reserved built-in name`},
		{"generic-use-of-shadowed-enum", "struct Option { n: i32 }\nfunction g(): Option[i32] { return None; }\nfunction main(): i32 { return 0; }\n", `struct "Option" is a reserved built-in name`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			want := goCheckerCodes(t, dir, tc.src)
			if !slices.Equal(want, []string{"E010"}) {
				t.Fatalf("Go checker codes %v, want [E010]", want)
			}
			path := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib).CombinedOutput()
			if got := codeRE.FindAllString(string(out), -1); err == nil || !slices.Equal(got, want) || !strings.Contains(string(out), tc.msg) {
				t.Fatalf("check = %v, codes %v, want %v with %q\n%s", err, got, want, tc.msg, out)
			}
		})
	}
}
