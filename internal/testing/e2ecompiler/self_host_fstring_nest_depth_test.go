package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// TestSelfHostFStringPlaceholderNestDepth: an f-string placeholder is parsed
// from depth zero, as the Go parser parses it, so nesting outside an f-string
// and nesting inside its placeholder are each held to the limit on their own.
// Each side here is about half the limit; together they are well past it.
func TestSelfHostFStringPlaceholderNestDepth(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	root := e2eharness.SelfHostStdlibRoot(t)
	oracle := e2eharness.BuildLangBinForInterp(t)
	const n = 2400
	placeholder := strings.Repeat("(", n) + "x" + strings.Repeat(")", n)
	src := "import \"std/i32\";\nfunction main(): i32 {\n  let x = 5;\n  let s = " +
		strings.Repeat("(", n) + "f\"{" + placeholder + "}\"" + strings.Repeat(")", n) +
		";\n  return 0;\n}\n"
	p := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(oracle, "-check", p).CombinedOutput(); err != nil {
		t.Fatalf("the Go parser refused the program, so it cannot serve as the oracle: %v\n%s", err, out)
	}
	if out, err := exec.Command(cli, "-check", p, root).CombinedOutput(); err != nil {
		t.Errorf("self-host -check refused what the Go parser accepts: %v\n%s", err, out)
	}
}
