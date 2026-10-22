package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A package or module named with a `-` binds the qualifier with `_` in its
// place: `import "my-h";` is called through `my_h`, in both compilers.
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
	main := filepath.Join(dir, "main.fern")
	for name, bin := range map[string]string{"self-host": buildFernCLIBin(t), "oracle": buildLangBinForInterp(t)} {
		cmd := exec.Command(bin, "-interp", main)
		cmd.Dir = dir
		out, _ := cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 5 {
			t.Errorf("%s: exit %d, want 5\n%s", name, code, out)
		}
	}
}
