package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A multi-file literate document: `file=PATH` blocks tangle to separate
// modules that import each other normally, with shared `<<chunk>>`
// definitions. The entry module (`entry`) computes 6² via a helper
// module assembled from a chunk.
const multiFileDoc = "# Two modules\n" +
	"\n" +
	"```fern file=main.fern entry\n" +
	"import \"core/no_prelude\";\n" +
	"import \"./mathx\";\n" +
	"function main(): i32 {\n" +
	"    return mathx.square(6);\n" +
	"}\n" +
	"```\n" +
	"\n" +
	"The helper module is assembled from a chunk:\n" +
	"\n" +
	"```fern file=mathx.fern\n" +
	"<<square>>\n" +
	"```\n" +
	"\n" +
	"```fern\n" +
	"<<square>>=\n" +
	"pub function square(n: i32): i32 { return n * n; }\n" +
	"```\n"

// `fern -interp` tangles a multi-file document in memory — each
// `file=` block becomes a virtual module fed to modload, so the entry's
// `import "./mathx"` resolves — and runs it: 6² = 36.
func TestLiterateMultiFileInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "app.fern.md")
	if err := os.WriteFile(src, []byte(multiFileDoc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 36 {
		t.Errorf("exit = %d, want 36 (6²)\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

// `fern -tangle` on a multi-file document prints every module under a
// `// ==> path <==` banner.
func TestLiterateMultiFileTangle(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "app.fern.md")
	if err := os.WriteFile(src, []byte(multiFileDoc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-tangle", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("tangle failed: %v\nstderr: %s", err, errb.String())
	}
	got := out.String()
	for _, want := range []string{"// ==> main.fern <==", "// ==> mathx.fern <==", "pub function square", "import \"./mathx\";"} {
		if !strings.Contains(got, want) {
			t.Errorf("tangle output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<<square>>") {
		t.Errorf("chunk reference left untangled:\n%s", got)
	}
}

// A type error in a *helper* module remaps back to its line in the
// `.fern.md` document — the multi-file diagnostic contract: each
// generated module carries its own provenance map.
func TestLiterateMultiFileDiagnosticRemap(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	doc := "# Buggy\n" + // 1
		"\n" + // 2
		"```fern file=main.fern entry\n" + // 3
		"import \"core/no_prelude\";\n" + // 4
		"import \"./mathx\";\n" + // 5
		"function main(): i32 { return mathx.square(6); }\n" + // 6
		"```\n" + // 7
		"\n" + // 8
		"```fern file=mathx.fern\n" + // 9
		"pub function square(n: i32): i32 { return \"oops\"; }\n" + // 10  <- error here
		"```\n" // 11
	src := filepath.Join(dir, "buggy.fern.md")
	if err := os.WriteFile(src, []byte(doc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-check", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected -check to fail\nstdout: %s", out.String())
	}
	msg := errb.String()
	// The error lives in mathx.fern's source, but must be reported
	// against the document at line 10 (not mathx.fern:2).
	if !strings.Contains(msg, "buggy.fern.md:10:") {
		t.Errorf("diagnostic should point at buggy.fern.md line 10, got:\n%s", msg)
	}
	if !strings.Contains(msg, `return "oops"`) {
		t.Errorf("diagnostic should render the document source line, got:\n%s", msg)
	}
}
