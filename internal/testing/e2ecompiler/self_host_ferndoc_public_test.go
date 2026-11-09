package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A function of a type with no receiver, such as one a derive makes, is
// written as `Type.name` with no receiver in its signature.
func TestSelfHostFernDocTypeFunctions(t *testing.T) {
	fernBin := buildFernDocCLI(t)
	want, err := os.ReadFile(filepath.Join("testdata", "ferndoc", "pair.md"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := exec.Command(fernBin, "-doc", filepath.Join("testdata", "ferndoc", "std", "pair.fern")).Output()
	if err != nil {
		t.Fatalf("fern -doc: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("page differs from testdata/ferndoc/pair.md\n%s", firstPageDiff(string(want), string(got)))
	}
	if strings.Contains(string(got), "(: ") {
		t.Errorf("a signature has an empty receiver:\n%s", got)
	}
}
