package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A map column whose elements are COUNTED must be snapshotted by `keys()` /
// `values()` as a copy with a per-element retain, not handed back as a raw
// alias of the map's own buffer. `irlower.map_kv_elem_flag` returned flag 1
// (raw alias) for every key column that was not a string or an integer, and
// for every value column that was not a string or an i32, so the frame
// released elements the map still held — on both sides of the same function.
//
// The abort is invisible without the sanitizer — the freed block keeps its
// bytes at this size, so the program answers correctly either way — and the
// differential production rows compare the two lowerings against EACH OTHER,
// which agree here because the typed path refuses these shapes and falls to
// the same AST lowering. So the pins have to be absolute ones under
// FERN_SANITIZE=1, against the answer the interpreter gives.
var mapColumnSnapshotPrograms = []struct {
	name string
	src  string
	want int
}{
	// The KEY column: a struct key, compared through its derived equality.
	{"struct-keys", `import "core/map";
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
}`, 24},
	// The VALUE column, the same bug's other half: a column of record boxes.
	{"struct-values", `import "core/map";

struct Coord { x: i32, y: i32 }

function main(): i32 {
    var m: Map[i32, Coord] = map_new(2);
    var i: i32 = 0;
    while (i < 12) {
        m = m.insert(i, Coord { x: i, y: i * 2 });
        i = i + 1;
    }
    var vs: Coord[] = m.values();
    return m.len() + vs.len();
}`, 24},
	// A raw CELL column must NOT take the retaining snapshot: rc-incing an i64
	// would read its bytes as an address. Pins that the widened arm stopped at
	// pointers.
	{"i64-values-stay-raw", `import "core/map";

function main(): i32 {
    var m: Map[i32, i64] = map_new(2);
    var i: i32 = 0;
    while (i < 12) {
        m = m.insert(i, (i as i64) * 1000000000);
        i = i + 1;
    }
    var vs: i64[] = m.values();
    return m.len() + vs.len();
}`, 24},
}

func TestSelfHostMapColumnSnapshotX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	runMapColumnSnapshot(t, runner, "x86-64-linux")
}

// The arm64 twin. The register backends dispatch flag 2 independently
// (`asm_arm64_ir.fern` sends `i32_imm 2` to `__fern_map_snapshot_col_str`), and
// no conformance struct-key case calls keys() or values(), so without this the
// arm64 path is never exercised end to end.
func TestSelfHostMapColumnSnapshotArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	runMapColumnSnapshot(t, []string{qemu}, "arm64-linux")
}

func runMapColumnSnapshot(t *testing.T, runner []string, target string) {
	hostGcc, _ := x86_64Tooling(t)
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, hostGcc, dir, "fern.fern", "fern")
	interp := buildLangBinForInterp(t)

	for _, p := range mapColumnSnapshotPrograms {
		t.Run(p.name, func(t *testing.T) {
			if got := interpExit(t, interp, p.src); got != p.want {
				t.Fatalf("interpreter = %d, want %d", got, p.want)
			}
			work := t.TempDir()
			src := filepath.Join(work, "main.fern")
			if err := os.WriteFile(src, []byte(p.src), 0o644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(work, "prog")
			cmd := exec.Command(fernBin, "-target", target, src, stdlibRoot, "-o", out)
			cmd.Env = append(os.Environ(), "FERN_SANITIZE=1")
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("compile for %s: %v\n%s", target, err, stderr.String())
			}
			if err := os.Chmod(out, 0o755); err != nil {
				t.Fatal(err)
			}
			got, code := hevRun(t, runner, out)
			if code != p.want {
				t.Fatalf("%s/%s exited %d, want %d — a raw-aliased column aborts at 124\n%s",
					target, p.name, code, p.want, got)
			}
			if strings.Contains(got, "use-after-free") {
				t.Fatalf("%s/%s: the sanitizer reported a use-after-free:\n%s", target, p.name, got)
			}
		})
	}
}
