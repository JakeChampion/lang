package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A script's top-level statements become a synthesized `function main(): i32`
// (asmcore.synth_script_main), so a top-level `return` is checked against i32
// as a written main's is (#10797). Native has no script form, so this is the
// self-host CLI alone.
func TestSelfHostScriptReturnType(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cases := []struct{ name, src, want string }{
		{"boolean", "let x: i32 = 3;\nreturn x > 2;\n", "function returns i32 but expression is boolean"},
		{"string", "return \"s\";\n", "function returns i32 but expression is string"},
		{"i32", "let x: i32 = 3;\nreturn x;\n", ""},
		{"no-return", "let x: i32 = 3;\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			argv := append(append([]string{}, cli.runner...), cli.bin, "-check", src, cli.stdlib)
			out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("check failed: %v\n%s", err, out)
				}
				return
			}
			if err == nil || !strings.Contains(string(out), "E002") || !strings.Contains(string(out), tc.want) {
				t.Fatalf("want E002 %q, got %v\n%s", tc.want, err, out)
			}
		})
	}
}
