package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// A `+` with one text operand reports one E009 that names both types, in
// both front ends.
func TestSelfHostPlusWithOneTextOperandDiagnostics(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	diagnostic := regexp.MustCompile(`error\[(E\d+)\]: ([^\r\n]+)`)
	for _, tc := range []struct{ expr, want string }{
		{`v + "x"`, `operator "+" requires matching numeric or string operands, got V and string`},
		{`"x" + v`, `operator "+" requires matching numeric or string operands, got string and V`},
		{`n + "x"`, `operator "+" requires matching numeric or string operands, got i32 and string`},
		{`"x" + 2.5`, `operator "+" requires matching numeric or string operands, got string and f64`},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			source := "struct V { a: i32 }\nfunction main(): i32 {\n  let v = V { a: 1 };\n  let n: i32 = 2;\n  let s = " + tc.expr + ";\n  print(v.a + n);\n  return 0;\n}\n"
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, frontend := range []struct{ name, bin string }{{"Go", oracle}, {"self", cli.bin}} {
				out, err := exec.Command(frontend.bin, "-check", path, cli.stdlib).CombinedOutput()
				if err == nil {
					t.Fatalf("%s accepted %s", frontend.name, tc.expr)
				}
				got := diagnostic.FindAllStringSubmatch(string(out), -1)
				if len(got) != 1 || got[0][1] != "E009" || got[0][2] != tc.want {
					t.Fatalf("%s: want one E009 %q, got:\n%s", frontend.name, tc.want, out)
				}
			}
		})
	}
}
