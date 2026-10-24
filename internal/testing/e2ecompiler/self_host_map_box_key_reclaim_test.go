package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var mapBoxKeyReclaimPrograms = []struct {
	name string
	src  string
}{
	{"struct-key-flat", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Coord { x: i32, y: i32 }

function build(n: i32): i32 {
    let m: Map[Coord, i32] = map_new(2);
    let i: i32 = 0;
    while (i < 8) {
        m = m.insert(Coord { x: i, y: n }, i * 10);
        i = i + 1;
    }
    return m.len();
}

function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { acc = acc + build(w); w = w + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc != 17600) { return 88; }
    return 0;
}`},
	{"enum-key-flat", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
enum Tag { TagLo(i32), TagHi(i32), TagNil }

function build(n: i32): i32 {
    let m: Map[Tag, i32] = map_new(2);
    let i: i32 = 0;
    while (i < 8) {
        m = m.insert(Tag.TagLo(i + n), i);
        i = i + 1;
    }
    return m.len();
}

function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { acc = acc + build(w); w = w + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc != 17600) { return 88; }
    return 0;
}`},
	{"struct-key-string-value-flat", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Coord { x: i32, y: i32 }

function build(n: i32): i32 {
    let m: Map[Coord, string] = map_new(2);
    let i: i32 = 0;
    while (i < 8) {
        m = m.insert(Coord { x: i, y: n }, "v" + "al");
        i = i + 1;
    }
    return m.len();
}

function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { acc = acc + build(w); w = w + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc != 17600) { return 88; }
    return 0;
}`},
	{"struct-key-reads-back", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Coord { x: i32, y: i32 }

function main(): i32 {
    let bad: i32 = 0;
    let r: i32 = 0;
    while (r < 500) {
        let m: Map[Coord, i32] = map_new(2);
        let i: i32 = 0;
        while (i < 8) {
            m = m.insert(Coord { x: i, y: i * 2 }, i * 10);
            i = i + 1;
        }
        if (m.len() != 8) { bad = 1; }
        if (m.get_or(Coord { x: 3, y: 6 }, 0) != 30) { bad = 1; }
        if (m.get_or(Coord { x: 7, y: 14 }, 0) != 70) { bad = 1; }
        if (m.get_or(Coord { x: 9, y: 9 }, 0) != 0) { bad = 1; }
        r = r + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`},
	{"shadowed-variant-name-excluded", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
enum Tag { TagLo(i32), TagNil }

function main(): i32 {
    let bad: i32 = 0;
    let r: i32 = 0;
    while (r < 500) {
        let TagNil: Tag = Tag.TagLo(r);
        let m: Map[Tag, i32] = map_new(2);
        m = m.insert(TagNil, 7);
        if (m.get_or(TagNil, 0) != 7) { bad = 1; }
        r = r + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`},
	{"aliased-key-excluded", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Coord { x: i32, y: i32 }

function main(): i32 {
    let bad: i32 = 0;
    let r: i32 = 0;
    while (r < 500) {
        let k: Coord = Coord { x: 1, y: 2 };
        let m: Map[Coord, i32] = map_new(2);
        m = m.insert(k, 7);
        if (k.x != 1) { bad = 1; }
        if (k.y != 2) { bad = 1; }
        if (m.get_or(Coord { x: 1, y: 2 }, 0) != 7) { bad = 1; }
        r = r + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`},
}

func TestSelfHostMapBoxKeyReclaimX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	runMapBoxKeyReclaim(t, runner, "x86-64-linux")
}

func TestSelfHostMapBoxKeyReclaimArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	runMapBoxKeyReclaim(t, []string{qemu}, "arm64-linux")
}

func runMapBoxKeyReclaim(t *testing.T, runner []string, target string) {
	hostGcc, _ := x86_64Tooling(t)
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, hostGcc, dir, "fern.fern", "fern")

	for _, p := range mapBoxKeyReclaimPrograms {
		t.Run(p.name, func(t *testing.T) {
			work := t.TempDir()
			src := filepath.Join(work, "main.fern")
			if err := os.WriteFile(src, []byte(p.src), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(work, "prog")
			cmd := exec.Command(fernBin, "-target", target, "-o", bin, src, stdlibRoot)
			var cerr strings.Builder
			cmd.Stderr = &cerr
			if err := cmd.Run(); err != nil {
				t.Fatalf("compile for %s: %v\n%s", target, err, cerr.String())
			}
			if err := os.Chmod(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			stderr, code := hevRun(t, runner, bin)
			if code != 0 {
				t.Fatalf("%s/%s exited %d, want 0 "+
					"(1 = the key column still leaks; 88 = a key read back wrong or a count is off; "+
					"99 = over-release, a per-key dec on a key the map did not own)\n%s",
					target, p.name, code, strings.TrimSpace(stderr))
			}
		})
	}
}
