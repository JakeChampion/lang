package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `fern -tangle -chunk NAME` prints just that chunk's expansion.
func TestLiterateTangleChunk(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	doc := "```fern\n<<*>>=\n<<helper>>\nfn main() {}\n```\n```fern\n<<helper>>=\nfn helper(): i32 { return 1; }\n```\n"
	src := filepath.Join(dir, "p.fern.md")
	if err := os.WriteFile(src, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := exec.Command(bin, "-tangle", "-chunk", "helper", src)
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("tangle -chunk: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "fn helper(): i32 { return 1; }") {
		t.Errorf("expected the helper chunk, got:\n%s", got)
	}
	if strings.Contains(got, "fn main()") {
		t.Errorf("should print only the helper chunk, not the root:\n%s", got)
	}
	// An undefined chunk exits non-zero.
	if err := exec.Command(bin, "-tangle", "-chunk", "nope", src).Run(); err == nil {
		t.Error("expected non-zero exit for an undefined chunk")
	}
}
