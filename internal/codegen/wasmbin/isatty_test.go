package wasmbin

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

const isattySrc = `
function main(): i32 {
    if (isatty(1)) { return 1; }
    return 0;
}
`

func buildIsatty(t *testing.T, opts BuildOptions) []byte {
	t.Helper()
	prog, err := parser.Parse(isattySrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	bin, err := BuildWithOptions(prog, info, opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return bin
}

// WASI has no isatty. Preview 1 does have an fd table, and
// `fd_fdstat_get`'s `fs_filetype` is the nearest question it can answer —
// a redirect reports `regular_file`, a pipe `unknown`, a terminal
// `character_device` — so that is what the preview-1 helper asks.
func TestIsattyPreview1AsksTheFdTable(t *testing.T) {
	bin := buildIsatty(t, BuildOptions{})
	if !importExists(t, bin, "wasi_snapshot_preview1", "fd_fdstat_get") {
		t.Error("preview-1 isatty does not import fd_fdstat_get, so it cannot " +
			"distinguish a terminal from a redirect and every colouriser " +
			"guesses instead")
	}
}

// A component has no fd table, so the preview-1 question cannot be asked at
// all. The preview-2 helper is a constant `false` rather than a guess:
// plain text is right for every embedder that captures the component's
// output, and FORCE_COLOR still asks for escapes. Pinned by the ABSENCE of
// the import, which is also what keeps the component composer able to
// classify the module — an unrecognised preview-1 import there is a hard
// build failure.
func TestIsattyPreview2ImportsNothing(t *testing.T) {
	bin := buildIsatty(t, BuildOptions{Preview2WASI: true})
	if importExists(t, bin, "wasi_snapshot_preview1", "fd_fdstat_get") {
		t.Error("preview-2 isatty still imports preview-1 fd_fdstat_get; the " +
			"component composer cannot place that import and the build will fail")
	}
}
