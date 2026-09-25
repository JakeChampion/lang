package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A reclaimable map with a struct- or enum-KEY column must release the key
// boxes, not just the buffer that holds them. The key column had exactly one
// deep release before #9966 — "MAPKS:" for a fresh-string column — so a
// struct-keyed map fell to the shallow `arr_dec` in the kfree position and
// stranded every key box. `Map[Coord, i32]` measured 2304 bytes over four
// rounds of twelve inserts against 0 for the same program keyed by i32.
//
// The assert is ABSOLUTE rather than differential: a credited map reclaims
// everything, so the heap bump across 2000 build-and-drop rounds is flat. The
// programs import core/map and core/cmp — a derived Eq/Hash is what makes a
// struct usable as a key at all — so they are compiled through a project
// directory with the stdlib root rather than fed to a driver on stdin, which
// cannot resolve an import.
//
// Every case also reads __rc_underflow_count(): a per-key dec that ran on a key the
// map did not solely own would read non-zero here, which is the direction a
// wrong credit fails in.
var mapBoxKeyReclaimPrograms = []struct {
	name string
	src  string
}{
	// An all-scalar struct key: one dec per key is its whole release, which is
	// __fern_arrarr_free's element walk (__fern_map_free_ka).
	{"struct-key-flat", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Coord { x: i32, y: i32 }

function build(n: i32): i32 {
    var m: Map[Coord, i32] = map_new(2);
    var i: i32 = 0;
    while (i < 8) {
        m = m.insert(Coord { x: i, y: n }, i * 10);
        i = i + 1;
    }
    return m.len();
}

function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { acc = acc + build(w); w = w + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc != 17600) { return 88; }
    return 0;
}`},
	// An all-scalar ENUM key: the variants carry scalar payloads, so one dec
	// releases a key box exactly as it does a struct's.
	{"enum-key-flat", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
enum Tag { TagLo(i32), TagHi(i32), TagNil }

function build(n: i32): i32 {
    var m: Map[Tag, i32] = map_new(2);
    var i: i32 = 0;
    while (i < 8) {
        m = m.insert(Tag.TagLo(i + n), i);
        i = i + 1;
    }
    return m.len();
}

function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { acc = acc + build(w); w = w + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc != 17600) { return 88; }
    return 0;
}`},
	// A struct KEY column over a string VALUE column (__fern_map_free_kavs):
	// both columns are counted, and the two credits are independent.
	{"struct-key-string-value-flat", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Coord { x: i32, y: i32 }

function build(n: i32): i32 {
    var m: Map[Coord, string] = map_new(2);
    var i: i32 = 0;
    while (i < 8) {
        m = m.insert(Coord { x: i, y: n }, "v" + "al");
        i = i + 1;
    }
    return m.len();
}

function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 200) { acc = acc + build(w); w = w + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc != 17600) { return 88; }
    return 0;
}`},
	// Correctness through the churn: the keys the map still holds read back,
	// which is the direction a per-key dec that ran too early fails in.
	{"struct-key-reads-back", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Coord { x: i32, y: i32 }

function main(): i32 {
    var bad: i32 = 0;
    var r: i32 = 0;
    while (r < 500) {
        var m: Map[Coord, i32] = map_new(2);
        var i: i32 = 0;
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
	// A key whose NAME shadows a unit variant is a read of the BINDING, not a
	// construction — a shadow the checker allows (checker.fern's ctor_shadowed)
	// — so the frame still owns that box and the credit must be declined. The
	// freshness predicate answers variant names out of the struct table with no
	// shadow test of its own; what makes that safe is lexical.resolve_func,
	// which rewrites every binding use to `$binding$N$TagNil` before lowering
	// sees it, so the name never reaches the table. This pins that: were the
	// credit issued, the map would deep-release a key the frame releases again
	// and __rc_underflow_count() would read non-zero (exit 99).
	{"shadowed-variant-name-excluded", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
enum Tag { TagLo(i32), TagNil }

function main(): i32 {
    var bad: i32 = 0;
    var r: i32 = 0;
    while (r < 500) {
        var TagNil: Tag = Tag.TagLo(r);
        var m: Map[Tag, i32] = map_new(2);
        m = m.insert(TagNil, 7);
        if (m.get_or(TagNil, 0) != 7) { bad = 1; }
        r = r + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`},
	// An ALIASED key excludes the credit: the key comes from a local the frame
	// still owns, so map_column_args_fresh reads false, no MAPKA: is issued and
	// the key column keeps the shallow free. The local must survive the map.
	{"aliased-key-excluded", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Coord { x: i32, y: i32 }

function main(): i32 {
    var bad: i32 = 0;
    var r: i32 = 0;
    while (r < 500) {
        var k: Coord = Coord { x: 1, y: 2 };
        var m: Map[Coord, i32] = map_new(2);
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

// runMapBoxKeyReclaim builds the self-host compiler once, then compiles and
// runs each program for `target`. The compiler is always the x86-64 build — it
// is what this host executes; `target` selects what it emits, and it links its
// own output, so no separate assembler step is involved.
func runMapBoxKeyReclaim(t *testing.T, runner []string, target string) {
	hostGcc, _ := x86_64Tooling(t)
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, hostGcc, dir, "fern.fern", "fern")

	// Both lowerings, named: the typed path admits a keyed map since #9962 and
	// is the default, so a run with the environment alone would measure only
	// it and the AST-path credit this test was written for would go untested.
	for _, leg := range []struct{ name, env string }{{"ast", "FERN_SEM_IR="}, {"typed", "FERN_SEM_IR=1"}} {
		for _, p := range mapBoxKeyReclaimPrograms {
			t.Run(leg.name+"/"+p.name, func(t *testing.T) {
				work := t.TempDir()
				src := filepath.Join(work, "main.fern")
				if err := os.WriteFile(src, []byte(p.src), 0o644); err != nil {
					t.Fatal(err)
				}
				bin := filepath.Join(work, "prog")
				cmd := exec.Command(fernBin, "-target", target, src, stdlibRoot, "-o", bin)
				cmd.Env = append(os.Environ(), leg.env)
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
					t.Fatalf("%s/%s/%s exited %d, want 0 "+
						"(1 = the key column still leaks; 88 = a key read back wrong or a count is off; "+
						"99 = over-release, a per-key dec on a key the map did not own)\n%s",
						target, leg.name, p.name, code, strings.TrimSpace(stderr))
				}
			})
		}
	}
}
