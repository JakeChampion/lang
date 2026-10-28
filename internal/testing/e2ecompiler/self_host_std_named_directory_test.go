package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A user module under a directory named `std` is not the standard library:
// it sees only the methods of its own imports, so the entry's import of
// std/result does not lend it Result.ok.
func TestSelfHostStdNamedDirectoryIsNotStdlib(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	dir := t.TempDir()
	files := map[string]string{
		"main.fern": `import "std/result";
import "./std/first";

function main(): i32 {
  match (first.first(Ok(3))) {
    Some(v) => { return v; },
    None => { return 0; }
  }
}
`,
		"std/first.fern": `pub function first(r: Result[i32, string]): Option[i32] {
  return r.ok();
}
`,
	}
	for name, text := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.fern")
	const want = "no method \"ok\" on Result[i32, string] — add `import \"std/result\"`"
	for _, cmd := range []*exec.Cmd{
		exec.Command(oracle, "-check", entry),
		exec.Command(cli, "-check", entry, stdlib),
	} {
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("%s accepted a std-named user module calling an unimported method\n%s", cmd.Path, out)
		}
		if !strings.Contains(string(out), want) || !strings.Contains(string(out), "error[E043]") {
			t.Errorf("%s: want E043 %q\n%s", cmd.Path, want, out)
		}
	}
}
