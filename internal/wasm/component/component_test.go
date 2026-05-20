package component_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

// TestPutComponentTypeSection_StructuralRoundTrip emits a component
// with just a header + a synthetic component-type custom section
// payload and confirms wasm-tools accepts it + surfaces the
// section under that name.
func TestPutComponentTypeSection_StructuralRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}

	payload := []byte{'t', 'e', 's', 't'}
	buf := component.PutComponentHeader(nil)
	buf = component.PutComponentTypeSection(buf, payload)

	dir := t.TempDir()
	compPath := filepath.Join(dir, "out.wasm")
	if err := os.WriteFile(compPath, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("component-type")) {
		t.Errorf("expected `component-type` in printed output, got:\n%s", out)
	}
}
