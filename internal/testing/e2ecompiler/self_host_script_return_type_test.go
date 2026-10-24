package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostScriptReturnType(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cases := []struct{ name, src, want string }{
		{"boolean", "let x: i32 = 3;\nreturn x > 2;\n", "return value: expected i32, got boolean"},
		{"string", "return \"s\";\n", "return value: expected i32, got string"},
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
