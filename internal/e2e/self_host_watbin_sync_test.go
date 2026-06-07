package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWatbinInSyncWithSlices guards the one deliberate duplication in the
// self-host tree: examples/self_host/watbin.fern is the *merged* single-module
// form of the five sliced WAT->binary encoder modules (leb128 / wat_lex /
// wat_parse / wat_encode / wat_emit_bin), so the unified `fern` CLI can
// `import` it (cross-module refs need qualification, which would break the
// sliced modules' concatenation-based tests; a single module keeps every
// internal reference bare).
//
// The slices remain the canonical, per-format source + tests. watbin.fern is
// built by concatenating them verbatim, so each slice's body must appear in
// watbin.fern unchanged. This test fails the moment a slice is edited without
// regenerating watbin.fern — preventing the two copies from drifting. To
// regenerate after editing a slice:
//
//	cat leb128.fern wat_lex.fern wat_parse.fern wat_encode.fern wat_emit_bin.fern
//
// (between watbin.fern's header comment and its trailing wat_to_binary wrapper).
func TestWatbinInSyncWithSlices(t *testing.T) {
	dir := "../../examples/self_host"
	watbin, err := os.ReadFile(filepath.Join(dir, "watbin.fern"))
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	merged := string(watbin)
	for _, slice := range []string{"leb128.fern", "wat_lex.fern", "wat_parse.fern", "wat_encode.fern", "wat_emit_bin.fern"} {
		body, err := os.ReadFile(filepath.Join(dir, slice))
		if err != nil {
			t.Fatalf("read %s: %v", slice, err)
		}
		if !strings.Contains(merged, string(body)) {
			t.Errorf("watbin.fern is out of sync with %s — its body is not present verbatim.\n"+
				"Regenerate watbin.fern from the five slices (see this test's doc comment).", slice)
		}
	}
	// And the public pipeline entry the CLI calls must exist.
	if !strings.Contains(merged, "pub function wat_to_binary(") {
		t.Error("watbin.fern is missing its `pub function wat_to_binary` entry point")
	}
}
