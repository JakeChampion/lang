package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

// Each name that a pattern binds is a declaration: E013 reports one that the
// scope already declares, in both front ends.
func TestSelfHostPatternNameRedeclarationDiagnostics(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	diagnostic := regexp.MustCompile(`:(\d+:\d+): error\[(E\d+)\]: ([^\r\n]+)`)
	for _, tc := range []struct {
		name, decls string
		want        []string
	}{
		{"tuple then name", "let (a, b) = (1, 2);\n  let a = 3;", []string{`4:3 E013 variable "a" already declared in this scope`}},
		{"name then tuple", "let a = 3;\n  let (a, b) = (1, 2);", []string{`4:3 E013 variable "a" already declared in this scope`}},
		{"tuple then tuple", "let (a, b) = (1, 2);\n  let (b, a) = (3, 4);", []string{
			`4:3 E013 variable "b" already declared in this scope`,
			`4:3 E013 variable "a" already declared in this scope`,
		}},
		{"nested tuple", "let (a, (b, c)) = (1, (2, 3));\n  let c = 4;", []string{`4:3 E013 variable "c" already declared in this scope`}},
		{"record then name", "let P { a, b: _ } = P { a: 1, b: 2 };\n  let a = 3;", []string{`4:3 E013 variable "a" already declared in this scope`}},
		{"at binding", "let x @ (a, b) = (1, 2);\n  let (x, c) = (3, 4);", []string{`4:3 E013 variable "x" already declared in this scope`}},
		{"discards", "let _ = 1;\n  let (_, a) = (1, 2);\n  let (_, b) = (3, 4);", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "struct P { a: i32, b: i32 }\nfunction main(): i32 {\n  " + tc.decls + "\n  return 0;\n}\n"
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, frontend := range []struct{ name, bin string }{{"Go", oracle}, {"self", cli.bin}} {
				out, _ := exec.Command(frontend.bin, "-check", path, cli.stdlib).CombinedOutput()
				var got []string
				for _, m := range diagnostic.FindAllStringSubmatch(string(out), -1) {
					if m[2] == "E013" {
						got = append(got, m[1]+" "+m[2]+" "+m[3])
					}
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("%s: want %q, got %q:\n%s", frontend.name, tc.want, got, out)
				}
			}
		})
	}
}

// A parameter pattern declares its names in the body of the function.
func TestSelfHostParameterPatternNameRedeclarationDiagnostics(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	source := "function f((a, b): (i32, i32)): i32 {\n  let a = 5;\n  return a + b;\n}\nfunction main(): i32 {\n  return f((1, 2));\n}\n"
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	want := regexp.MustCompile(`:2:3: error\[E013\]: variable "a" already declared in this scope`)
	for _, frontend := range []struct{ name, bin string }{{"Go", oracle}, {"self", cli.bin}} {
		out, err := exec.Command(frontend.bin, "-check", path, cli.stdlib).CombinedOutput()
		if err == nil || !want.Match(out) {
			t.Fatalf("%s: want E013 for a at 2:3, got:\n%s", frontend.name, out)
		}
	}
}
