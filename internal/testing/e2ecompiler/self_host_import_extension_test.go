package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// An import may write its module's `.fern` or leave it off, in both compilers:
// `import "./two.fern";` loads two.fern and binds `two` (#12980).
func TestImportWithFernExtension(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"two.fern":       "pub function f(): i32 { return 2; }\nfunction hidden(): i32 { return 9; }\n",
		"sub/three.fern": "import \"../two.fern\";\npub function g(): i32 { return two.f() + 1; }\n",
		"main.fern":      "import \"./two.fern\";\nimport \"./sub/three.fern\";\nfunction main(): i32 { return two.f() + three.g(); }\n",
		"private.fern":   "import \"./two.fern\";\nfunction main(): i32 { return two.hidden(); }\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, c := range []struct {
		main string
		want int
	}{
		{"main.fern", 5},
		// The module keeps its privacy behind the suffixed spelling.
		{"private.fern", 1},
	} {
		path := filepath.Join(dir, c.main)
		for leg, cmd := range map[string]*exec.Cmd{
			"self-host": runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib),
			"oracle":    exec.Command(oracle, "-interp", path),
		} {
			out, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil {
				t.Fatalf("%s did not run: %v", leg, err)
			}
			if code := cmd.ProcessState.ExitCode(); code != c.want {
				t.Errorf("%s %s: exit %d, want %d\n%s", leg, c.main, code, c.want, out)
			}
		}
	}
}
