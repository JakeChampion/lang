package e2ecompiler

import (
	"os"
	"os/exec"
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

// A function of an imported package under a raw builtin's name is E010 at its
// own file in both checkers, as it is in the entry module (#15203).
func TestSelfHostImportedRawBuiltinNameIsReserved(t *testing.T) {
	cli := newStrictCLI(t)
	fern := buildFernCLIBin(t)
	dir := t.TempDir()
	files := map[string]string{
		"lib.fern":  "pub function __load_u8(x: i32): i32 { return x; }\npub function g(): i32 { return __load_u8(1); }\n",
		"main.fern": "import \"./lib\";\nfunction main(): i32 { return lib.g(); }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.fern")
	const want = `lib.fern:1:5: error[E010]: function "__load_u8" is a reserved built-in name`
	goOut, goErr := exec.Command(fern, "-check", entry).CombinedOutput()
	if goErr == nil || !strings.Contains(string(goOut), want) || len(codeRE.FindAllString(string(goOut), -1)) != 1 {
		t.Fatalf("Go checker = %v, want one diagnostic %q\n%s", goErr, want, goOut)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-check", entry, cli.stdlib).CombinedOutput()
	if err == nil || !strings.Contains(string(out), want) || len(codeRE.FindAllString(string(out), -1)) != 1 {
		t.Fatalf("self-host checker = %v, want one diagnostic %q\n%s", err, want, out)
	}
}

// A variant may take a built-in type's name. In a type, the name still means
// the built-in, in the declaring module and in each importer, in both checkers.
func TestSelfHostImportedVariantKeepsBuiltinType(t *testing.T) {
	cli := newStrictCLI(t)
	fern := buildFernCLIBin(t)
	dir := t.TempDir()
	files := map[string]string{
		"lib.fern": "pub enum Fault { Quiet, Socket(i32) }\n" +
			"pub function fault(n: i32): Fault { return Socket(n); }\n" +
			"pub function code(f: Fault): i32 {\n  match (f) { Quiet => { return 0; }, Socket(n) => { return n; } }\n  return 0;\n}\n" +
			"pub function fd_of(s: Socket): i32 { return s.fd; }\n",
		"main.fern": "import \"./lib\";\n" +
			"function width(s: Socket): i32 { return lib.fd_of(s); }\n" +
			"function main(): i32 {\n  let s: Socket = udp_bind([127u8, 0u8, 0u8, 1u8], 0);\n  let n: i32 = width(s) + lib.code(lib.fault(2));\n  tcp_close(s);\n  return n - n;\n}\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.fern")
	if out, err := exec.Command(fern, "-check", entry).CombinedOutput(); err != nil {
		t.Fatalf("Go checker: %v\n%s", err, out)
	}
	if out, err := runX86_64Bin(cli.runner, cli.bin, "-check", entry, cli.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("self-host checker: %v\n%s", err, out)
	}
}
