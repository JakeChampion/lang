package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A package or module named with a `-` binds the qualifier with `_` in its
// place: `import "my-h";` is called through `my_h`, in both compilers. The
// module keeps its own name, so a diagnostic about it still says `two-x`.
func TestHyphenatedImportQualifier(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("fern.toml", "[package]\nname = \"app\"\n\n[dependencies]\nmy-h = { path = \"./my-h\" }\n")
	write("my-h/fern.toml", "[package]\nname = \"my-h\"\n")
	write("my-h/lib.fern", "pub function three(): i32 { return 3; }\n")
	write("two-x.fern", "pub function two(): i32 { return 2; }\n")
	write("main.fern", "import \"my-h\";\nimport \"./two-x\";\nfunction main(): i32 { return my_h.three() + two_x.two(); }\n")
	write("lib/fern.toml", "[package]\nname = \"lib\"\n")
	write("lib/two-x.fern", "pub(package) function hidden(): i32 { return 1; }\n")
	write("app/fern.toml", "[package]\nname = \"app2\"\n")
	write("app/main.fern", "import \"../lib/two-x\";\nfunction main(): i32 { return two_x.hidden(); }\n")
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, c := range []struct {
		main string
		code int
		want string
	}{
		{filepath.Join(dir, "main.fern"), 5, ""},
		{filepath.Join(dir, "app", "main.fern"), 1, "two-x.hidden is `pub(package)` — only modules in the same package as two-x may use it"},
	} {
		for name, cmd := range map[string]*exec.Cmd{
			"self-host": runX86_64Bin(cli.runner, cli.bin, "-interp", c.main, cli.stdlib),
			"oracle":    exec.Command(oracle, "-interp", c.main),
		} {
			cmd.Dir = filepath.Dir(c.main)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != c.code || !strings.Contains(string(out), c.want) {
				t.Errorf("%s %s: exit %d, want %d and %q\n%s", name, c.main, code, c.code, c.want, out)
			}
		}
	}
}
