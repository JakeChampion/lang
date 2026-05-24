package componenttype_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/componenttype"
)

// TestEmbedSimple — minimal cover: Embed appends a custom
// section to a trivial module and the resulting bytes start
// with the input + section id 0.
func TestEmbedSimple(t *testing.T) {
	core := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00} // bare module header
	out, err := componenttype.Embed(core, "lang")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if !bytes.HasPrefix(out, core) {
		t.Fatalf("output doesn't start with core bytes")
	}
	if out[len(core)] != 0x00 {
		t.Fatalf("byte after core = %#x, want 0x00 (custom section id)", out[len(core)])
	}
	if len(out) <= len(core)+10 {
		t.Fatalf("output too short, got %d bytes", len(out))
	}
}

// TestEmbedUnknownWorld asserts the error path for unrecognised
// world names — the only validation Embed does.
func TestEmbedUnknownWorld(t *testing.T) {
	_, err := componenttype.Embed([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}, "nope")
	if err == nil {
		t.Fatal("Embed returned nil for unknown world")
	}
}

// TestEmbedMatchesWasmTools is the integration guarantee. It
// builds a small core module via wasm-tools (so the test is
// independent of our Lang backend), runs `wasm-tools component
// embed` for each world, and asserts that componenttype.Embed
// produces byte-for-byte identical output. If wasm-tools is
// unavailable the test skips — the wasmtime e2e tests already
// gate on the same binary.
func TestEmbedMatchesWasmTools(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	witDir, err := findWITDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	watPath := filepath.Join(dir, "in.wat")
	corePath := filepath.Join(dir, "in.wasm")
	if err := os.WriteFile(watPath, []byte(`(module
  (func (export "main") (result i32) i32.const 42))`), 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse: %v\n%s", err, out)
	}
	coreBytes, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatalf("read core: %v", err)
	}

	for _, world := range []string{"lang", "http"} {
		t.Run(world, func(t *testing.T) {
			expectedPath := filepath.Join(dir, world+".embedded.wasm")
			if out, err := exec.Command("wasm-tools", "component", "embed",
				witDir, "-w", world, corePath, "-o", expectedPath).CombinedOutput(); err != nil {
				t.Fatalf("wasm-tools component embed: %v\n%s", err, out)
			}
			expected, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("read expected: %v", err)
			}
			got, err := componenttype.Embed(coreBytes, world)
			if err != nil {
				t.Fatalf("Embed: %v", err)
			}
			if !bytes.Equal(got, expected) {
				t.Fatalf("Embed(%q) differs from wasm-tools (len got=%d, want=%d, first diff at byte %d)",
					world, len(got), len(expected), firstDiff(got, expected))
			}
		})
	}
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func findWITDir() (string, error) {
	// Walk up from the package directory until we find cmd/fern/wit.
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := cwd; ; {
		candidate := filepath.Join(dir, "cmd", "lang", "wit")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
