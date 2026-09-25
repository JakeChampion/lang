package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A struct- or enum-keyed map on the typed path (#9962). The key column is a
// column of boxes the map owns one unit of per entry, hashed and compared
// through the key type's derived `hash` and `eq`, and released through the
// key type's own `__sem_release_<K>` — the `_kf` members of the map free
// family walk the column calling it, and `op_map_set` names it so an
// overwrite can release the consumed key it discards.
//
// The assert is ABSOLUTE and on every backend: the heap bump across 1000
// build-and-drop rounds is flat, `__rc_underflow_count()` reads zero, and the
// module is produced WHOLE by the typed path — a body the typed path refused
// would fall to the AST lowering and the flat heap would say nothing about
// this path. Each program is also checked to answer what the interpreter
// answers.
var mapKeyedTypedPrograms = []mapChurnProgram{
	// A struct key carrying a STRING field, overwritten through a FRESH
	// value-equal key: the map keeps its existing key and releases the one it
	// discards through the key release op_map_set names — a shallow dec there
	// would strand the key's string. `keys()` is the retaining snapshot, read
	// after the map is gone.
	{"struct-key-string-field", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Name { first: string, rank: i32 }

function build(n: i32): i32 {
    var ks: Name[] = [];
    var t: i32 = 0;
    {
        var m: Map[Name, i32] = map_new(2);
        var i: i32 = 0;
        while (i < 6) {
            m = m.insert(Name { first: "k" + (i % 3).to_string(), rank: i }, i);
            i = i + 1;
        }
        if (m.len() != 6) { return 0 - 1; }
        m = m.insert(Name { first: "k" + "1", rank: 1 }, 100);
        if (m.len() != 6) { return 0 - 2; }
        if (m.get_or(Name { first: "k1", rank: 1 }, 0) != 100) { return 0 - 3; }
        if (!m.has(Name { first: "k2", rank: 5 })) { return 0 - 4; }
        ks = m.keys();
        t = m.get_or(Name { first: "k0", rank: 3 }, 0);
    }
    for k in ks { t = t + k.first.len() + k.rank; }
    return t + n - n;
}

function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 100) { acc = acc + build(w); w = w + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc != 1100 * 30) { return 88; }
    return 0;
}
`},
	// An enum key with a string payload beside a boxed VALUE column: the
	// `_kfvf` member takes both releases, and an overwrite releases the
	// superseded value through one and the discarded key through the other.
	// A grow from capacity 2 to twenty entries rehashes on wasm and reallocates
	// both columns on the register backends.
	{"enum-key-boxed-value", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
enum Tag { A(i32), B, C(string) }

struct Box { n: i32, tag: string }

function build(n: i32): i32 {
    var m: Map[Tag, Box] = map_new(2);
    var i: i32 = 0;
    while (i < 20) {
        m = m.insert(A(i), Box { n: i, tag: "a" + i.to_string() });
        i = i + 1;
    }
    m = m.insert(B, Box { n: 1000, tag: "b" });
    m = m.insert(C("x" + "y"), Box { n: 2000, tag: "c" });
    m = m.insert(C("xy"), Box { n: 3000, tag: "cc" });
    if (m.len() != 22) { return 0 - 1; }
    var t: i32 = 0;
    match (m.get(C("xy"))) { Some(b) => { t = t + b.n + b.tag.len(); }, None => { return 0 - 2; } }
    match (m.get(A(7))) { Some(b) => { t = t + b.n; }, None => { return 0 - 3; } }
    match (m.get(A(99))) { Some(b) => { return 0 - 4; }, None => {} }
    for (k, v) in m { t = t + v.n % 2; }
    return t + m.get_or(B, Box { n: 0, tag: "" }).n + n - n;
}

function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 100) { acc = acc + build(w); w = w + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc != 1100 * 4019) { return 88; }
    return 0;
}
`},
	// A SHARED map written through: the insert finds the box aliased and
	// rebuilds it, retaining every key the copy names, so the two maps hold
	// one unit each of the same key boxes and both release cleanly. The key
	// is also a LOCAL the frame keeps, so the insert's consumed unit is a
	// retain rather than the local's own.
	{"struct-key-shared-rebuild", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Coord { a: i32, b: i32 }

function build(n: i32): i32 {
    var k: Coord = Coord { a: n, b: 1 };
    var m: Map[Coord, i32] = map_new(4);
    m = m.insert(k, 5);
    m = m.insert(Coord { a: 1, b: 2 }, 6);
    var shared: Map[Coord, i32] = m;
    m = m.insert(Coord { a: 2, b: 3 }, 7);
    if (shared.len() != 2 || m.len() != 3) { return 0 - 1; }
    if (shared.get_or(k, 0) != 5 || m.get_or(k, 0) != 5) { return 0 - 2; }
    if (k.a != n) { return 0 - 3; }
    var t: i32 = 0;
    for x in m.keys() { t = t + x.b; }
    return t + shared.get_or(Coord { a: 1, b: 2 }, 0);
}

function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 100) { acc = acc + build(w); w = w + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc != 1100 * 12) { return 88; }
    return 0;
}
`},
}

func TestSelfHostMapKeyedTypedX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	runMapChurnTyped(t, runner, "x86-64-linux", mapKeyedTypedPrograms)
}

func TestSelfHostMapKeyedTypedArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	runMapChurnTyped(t, []string{qemu}, "arm64-linux", mapKeyedTypedPrograms)
}

func TestSelfHostMapKeyedTypedWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	runMapChurnTyped(t, nil, "wasm32-wasi", mapKeyedTypedPrograms)
}

// semProducedWhole reads the production tally and reports whether every
// declaration and instance of the module was produced.
func semProducedWhole(report string) bool {
	m := regexp.MustCompile(`module: produced (\d+) of (\d+) declarations and (\d+) of (\d+) instances`).FindStringSubmatch(report)
	return m != nil && m[1] == m[2] && m[3] == m[4]
}

type mapChurnProgram struct {
	name string
	src  string
}

// runMapChurnTyped builds the self-host compiler once, then compiles each
// program for `target` through the typed path with the report on, checks the
// tally says the module was produced whole, and runs it: natively, under
// qemu, or under wasmtime.
func runMapChurnTyped(t *testing.T, runner []string, target string, programs []mapChurnProgram) {
	hostGcc, hostRunner := x86_64Tooling(t)
	if len(hostRunner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, hostGcc, dir, "fern.fern", "fern")

	for _, p := range programs {
		t.Run(p.name, func(t *testing.T) {
			work := t.TempDir()
			src := filepath.Join(work, "main.fern")
			if err := os.WriteFile(src, []byte(p.src), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(work, "prog")
			args := []string{"-target", target, src, stdlibRoot, "-o", bin}
			if target == "wasm32-wasi" {
				bin = filepath.Join(work, "prog.wat")
				args = []string{"-target", target, "-emit", "asm", src, stdlibRoot, "-o", bin}
			}
			cmd := exec.Command(fernBin, args...)
			cmd.Env = append(os.Environ(), "FERN_SEM_IR=1", "FERN_SEM_IR_REPORT=1")
			var cerr strings.Builder
			cmd.Stderr = &cerr
			if err := cmd.Run(); err != nil {
				t.Fatalf("compile for %s: %v\n%s", target, err, cerr.String())
			}
			if !semProducedWhole(cerr.String()) {
				t.Fatalf("the typed path did not produce the module whole — a refused body "+
					"falls to the AST lowering and the run below would not measure this path:\n%s",
					cerr.String())
			}
			var stderr string
			var code int
			if target == "wasm32-wasi" {
				run := exec.Command("wasmtime", "run", bin)
				var errBuf strings.Builder
				run.Stderr = &errBuf
				_ = run.Run()
				stderr, code = errBuf.String(), run.ProcessState.ExitCode()
			} else {
				if err := os.Chmod(bin, 0o755); err != nil {
					t.Fatal(err)
				}
				stderr, code = hevRun(t, runner, bin)
			}
			if code != 0 {
				t.Fatalf("%s/%s exited %d, want 0 "+
					"(1 = the map leaks; 88 = an answer is off; "+
					"99 = over-release, a dec on a box the map did not own)\n%s",
					target, p.name, code, strings.TrimSpace(stderr))
			}
		})
	}
}
