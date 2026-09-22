package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A struct-keyed map's `keys()` snapshot must COPY the key column with a
// per-element retain, not hand back a raw alias of the map's own buffer.
// `irlower.map_kv_elem_flag` returned flag 1 (raw alias) for every key column
// that was not a string or an integer, so the frame released keys the map
// still held.
//
// The abort is invisible without the sanitizer — the freed block keeps its
// bytes at this size, so the program answers correctly either way — and the
// differential production rows compare the two lowerings against EACH OTHER,
// which agree here because the typed path refuses the shape and falls to the
// same AST lowering. So the pin has to be an absolute one under
// FERN_SANITIZE=1, against the answer the interpreter gives.
const mapStructKeyColumnSource = `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Coord { x: i32, y: i32 }

function main(): i32 {
    var m: Map[Coord, i32] = map_new(2);
    var i: i32 = 0;
    while (i < 12) {
        m = m.insert(Coord { x: i, y: i * 2 }, i * 10);
        i = i + 1;
    }
    var ks: Coord[] = m.keys();
    return m.len() + ks.len();
}`

func TestSelfHostMapStructKeyColumnX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	interp := buildLangBinForInterp(t)
	want := interpExit(t, interp, mapStructKeyColumnSource)
	if want != 24 {
		t.Fatalf("interpreter = %d, want 24", want)
	}

	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(mapStructKeyColumnSource), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "prog")
	cmd := exec.Command(fernBin, "-target", "x86-64-linux", src, stdlibRoot, "-o", out)
	cmd.Env = append(os.Environ(), "FERN_SANITIZE=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("compile: %v\n%s", err, stderr.String())
	}
	if err := os.Chmod(out, 0o755); err != nil {
		t.Fatal(err)
	}
	got, runErr := hevRun(t, runner, out)
	if runErr != want {
		t.Fatalf("exit = %d, want %d — a raw-aliased key column aborts at 124\n%s", runErr, want, got)
	}
	if strings.Contains(got, "use-after-free") {
		t.Fatalf("the sanitizer reported a use-after-free:\n%s", got)
	}
}
