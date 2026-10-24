package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostImportCycleX86_64(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		entry   string
		wantMsg string
	}{
		{name: "two-module cycle", files: map[string]string{
			"a.fern":    "import \"./b\";\npub function fa(): i32 { return 1; }\npub function viab(): i32 { return b.fb(); }\n",
			"b.fern":    "import \"./a\";\npub function fb(): i32 { return a.fa() + 1; }\n",
			"main.fern": "import \"./a\";\nfunction main(): i32 { return a.viab(); }\n",
		}, wantMsg: "%DIR%/b.fern:1:1: error[E086]: import \"./a\" closes an import cycle"},
		{name: "three-module cycle", files: map[string]string{
			"a.fern":    "import \"./b\";\npub function fa(): i32 { return b.fb(); }\n",
			"b.fern":    "import \"./c\";\npub function fb(): i32 { return c.fc(); }\n",
			"c.fern":    "import \"./a\";\npub function fc(): i32 { return 3; }\npub function back(): i32 { return a.fa(); }\n",
			"main.fern": "import \"./a\";\nfunction main(): i32 { return a.fa(); }\n",
		}, wantMsg: "%DIR%/c.fern:1:1: error[E086]: import \"./a\" closes an import cycle"},
		{name: "entry in the cycle", files: map[string]string{
			"a.fern":    "import \"./main\";\npub function fa(): i32 { return 1; }\n",
			"main.fern": "import \"./a\";\nfunction main(): i32 { return a.fa(); }\n",
		}, wantMsg: "%DIR%/a.fern:1:1: error[E086]: import \"./main\" closes an import cycle"},
		{name: "entry named with ./", files: map[string]string{
			"a.fern":    "import \"./main\";\npub function fa(): i32 { return 1; }\n",
			"main.fern": "import \"./a\";\nfunction main(): i32 { return a.fa(); }\n",
		}, entry: "./main.fern", wantMsg: "a.fern:1:1: error[E086]: import \"./main\" closes an import cycle"},
		{name: "diamond", files: map[string]string{
			"left.fern":  "import \"./base\";\npub function l(): i32 { return base.b() + 1; }\n",
			"right.fern": "import \"./base\";\npub function r(): i32 { return base.b() + 2; }\n",
			"base.fern":  "pub function b(): i32 { return 10; }\n",
			"main.fern":  "import \"./left\";\nimport \"./right\";\nimport \"std/string\";\nfunction main(): i32 { return left.l() + right.r(); }\n",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			entry := filepath.Join(dir, "main.fern")
			if tc.entry != "" {
				entry = tc.entry
			}
			cmd := exec.Command(e2eharness.SelfHostCLI(t), "-check", entry, e2eharness.SelfHostStdlibRoot(t))
			cmd.Dir = dir
			out, _ := cmd.CombinedOutput()
			code := cmd.ProcessState.ExitCode()
			if tc.wantMsg == "" {
				if code != 0 {
					t.Fatalf("-check exit = %d, want 0\n%s", code, out)
				}
				return
			}
			want := strings.ReplaceAll(tc.wantMsg, "%DIR%", dir)
			if code == 0 || !strings.Contains(string(out), want) {
				t.Fatalf("-check exit = %d, want a refusal containing %q\n%s", code, want, out)
			}
		})
	}
}
