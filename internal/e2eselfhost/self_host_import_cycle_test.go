package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// An import cycle between a program's own modules is refused with native's
// diagnostic, naming the module the walk reached again (#11152). A diamond
// reaches one module twice without a cycle and must still build; it imports
// std/string as well, whose stdlib imports may cycle among themselves.
func TestSelfHostImportCycleX86_64(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		wantMsg string // "" means the program is accepted
	}{
		{"two-module cycle", map[string]string{
			"a.fern":    "import \"./b\";\npub function fa(): i32 { return 1; }\npub function viab(): i32 { return b.fb(); }\n",
			"b.fern":    "import \"./a\";\npub function fb(): i32 { return a.fa() + 1; }\n",
			"main.fern": "import \"./a\";\nfunction main(): i32 { return a.viab(); }\n",
		}, "import cycle detected including %DIR%/a.fern"},
		{"three-module cycle", map[string]string{
			"a.fern":    "import \"./b\";\npub function fa(): i32 { return b.fb(); }\n",
			"b.fern":    "import \"./c\";\npub function fb(): i32 { return c.fc(); }\n",
			"c.fern":    "import \"./a\";\npub function fc(): i32 { return 3; }\npub function back(): i32 { return a.fa(); }\n",
			"main.fern": "import \"./a\";\nfunction main(): i32 { return a.fa(); }\n",
		}, "import cycle detected including %DIR%/a.fern"},
		{"diamond", map[string]string{
			"left.fern":  "import \"./base\";\npub function l(): i32 { return base.b() + 1; }\n",
			"right.fern": "import \"./base\";\npub function r(): i32 { return base.b() + 2; }\n",
			"base.fern":  "pub function b(): i32 { return 10; }\n",
			"main.fern":  "import \"./left\";\nimport \"./right\";\nimport \"std/string\";\nfunction main(): i32 { return left.l() + right.r(); }\n",
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(e2eharness.SelfHostCLI(t), "-check", filepath.Join(dir, "main.fern"), e2eharness.SelfHostStdlibRoot(t))
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
