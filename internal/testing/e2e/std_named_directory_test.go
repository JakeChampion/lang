package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A user module in a directory named std is not the standard library, so the
// entry's import of std/result does not reach it (#13760). The conformance
// corpus cannot hold this case, because a case directory has no
// subdirectories.
func TestStdNamedDirectoryIsNotTheStdlib(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "std"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{
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
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const want = "no method \"ok\" on Result[i32, string] — add `import \"std/result\"`"
	entry := filepath.Join(dir, "main.fern")

	prog, _, err := modload.Load(entry)
	if err == nil {
		_, err = checker.Check(prog)
	}
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Go checker: want %q, got %v", want, err)
	}

	out, err := exec.Command(e2eharness.ShippedFern(t), "-check", entry).CombinedOutput()
	if err == nil || !strings.Contains(string(out), want) {
		t.Errorf("self-host checker: want %q, got %v\n%s", want, err, out)
	}
}
