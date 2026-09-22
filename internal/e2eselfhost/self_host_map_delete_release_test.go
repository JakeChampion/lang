package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A deleted map entry's key and value are released (#9970). `m.without(k)`
// used to remove an entry and release nothing it held — the register
// backends overwrite the slot with the last entry and shrink the columns, wasm
// tombstones the slot and its release walk visits live slots only — so every
// counted box in the deleted slot was stranded for the life of the program.
// The typed path refused a delete over any counted column for exactly that
// reason, which kept every program deleting from a string-keyed, string-valued
// or keyed map on the AST lowering.
//
// The register backends route a delete that owes a release to
// `__fern_map_delete_rel`, which releases the removed key and value through the
// releases the op names; wasm's `$__fern_map_delete` releases under the box's
// own column kinds before tombstoning. The assert is the churn gate's: over
// 1000 build-and-drop rounds the heap bump is flat, `__rc_underflow_count()`
// reads zero, and the module is produced whole by the typed path.
var mapDeleteReleasePrograms = []mapChurnProgram{
	// A string key column: the removed key takes __fern_str_free, and the
	// re-insert of the same key afterwards proves the slot is reusable.
	{"string-key", `import "core/map";
import "core/cmp";
function build(n: i32): i32 {
    var m: Map[string, i32] = map_new(2);
    var i: i32 = 0;
    while (i < 6) { m = m.insert("k" + i.to_string(), i); i = i + 1; }
    var (m2, gone) = m.without("k" + "3");
    if (!gone) { return 0 - 1; }
    var (m3, absent) = m2.without("zz");
    if (absent) { return 0 - 2; }
    m3 = m3.insert("k3", 30);
    return m3.len() + m3.get_or("k3", 0) + n - n;
}
` + mapChurnMain(39600)},
	// A string value column (vkind 1).
	{"string-value", `import "core/map";
import "core/cmp";
function build(n: i32): i32 {
    var m: Map[i32, string] = map_new(2);
    var i: i32 = 0;
    while (i < 6) { m = m.insert(i, "v" + i.to_string()); i = i + 1; }
    var (m2, gone) = m.without(2);
    if (!gone) { return 0 - 1; }
    return m2.len() + m2.get_or(4, "").len() + n - n;
}
` + mapChurnMain(7700)},
	// A string[] value column (vkind 2): the removed value's strings go with
	// its buffer, the deep dec rather than the shallow one.
	{"string-array-value", `import "core/map";
import "core/cmp";
function build(n: i32): i32 {
    var m: Map[i32, string[]] = map_new(2);
    var i: i32 = 0;
    while (i < 4) { m = m.insert(i, ["a" + i.to_string(), "b"]); i = i + 1; }
    var (m2, gone) = m.without(1);
    if (!gone) { return 0 - 1; }
    return m2.len() + m2.get_or(2, []).len() + n - n;
}
` + mapChurnMain(5500)},
	// A keyed column with a string field: the removed key takes the key
	// type's own release, which frees its string before its box.
	{"keyed", `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Name { first: string, rank: i32 }
function build(n: i32): i32 {
    var m: Map[Name, i32] = map_new(2);
    var i: i32 = 0;
    while (i < 6) { m = m.insert(Name { first: "k" + i.to_string(), rank: i }, i); i = i + 1; }
    var (m2, gone) = m.without(Name { first: "k" + "3", rank: 3 });
    if (!gone) { return 0 - 1; }
    if (m2.has(Name { first: "k3", rank: 3 })) { return 0 - 2; }
    return m2.len() + m2.get_or(Name { first: "k5", rank: 5 }, 0) + n - n;
}
` + mapChurnMain(11000)},
	// A keyed column over a STRING value column (`_kfvs`) and over a column
	// of string ARRAYS (`_kfvsa`): the key walk beside the value column's own
	// free, at the map's death and at the delete.
	{"keyed-string-value", `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Name { first: string, rank: i32 }
function build(n: i32): i32 {
    var m: Map[Name, string] = map_new(2);
    var i: i32 = 0;
    while (i < 6) { m = m.insert(Name { first: "k" + i.to_string(), rank: i }, "v" + i.to_string()); i = i + 1; }
    m = m.insert(Name { first: "k" + "2", rank: 2 }, "w" + "w");
    var (m2, gone) = m.without(Name { first: "k" + "4", rank: 4 });
    if (!gone) { return 0 - 1; }
    return m2.len() + m2.get_or(Name { first: "k2", rank: 2 }, "").len() + n - n;
}
` + mapChurnMain(7700)},
	{"keyed-string-array-value", `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Name { first: string, rank: i32 }
function build(n: i32): i32 {
    var m: Map[Name, string[]] = map_new(2);
    var i: i32 = 0;
    while (i < 4) { m = m.insert(Name { first: "k" + i.to_string(), rank: i }, ["a" + i.to_string(), "b"]); i = i + 1; }
    var (m2, gone) = m.without(Name { first: "k" + "1", rank: 1 });
    if (!gone) { return 0 - 1; }
    return m2.len() + m2.get_or(Name { first: "k3", rank: 3 }, []).len() + n - n;
}
` + mapChurnMain(5500)},
	// A keyed column over a column of boxes (vkind 3): both releases, two
	// deletes in a row.
	{"keyed-boxed-value", `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Coord { a: i32, b: i32 }
struct Box { n: i32, tag: string }
function build(n: i32): i32 {
    var m: Map[Coord, Box] = map_new(2);
    var i: i32 = 0;
    while (i < 8) { m = m.insert(Coord { a: i, b: i * 2 }, Box { n: i * 10, tag: "t" + i.to_string() }); i = i + 1; }
    var (m2, gone) = m.without(Coord { a: 3, b: 6 });
    if (!gone) { return 0 - 1; }
    var (m3, gone2) = m2.without(Coord { a: 5, b: 10 });
    if (!gone2) { return 0 - 2; }
    if (m3.len() != 6) { return 0 - 3; }
    return m3.get_or(Coord { a: 4, b: 8 }, Box { n: 0, tag: "" }).n + m3.len() + n - n;
}
` + mapChurnMain(50600)},
}

// mapChurnMain is the main every churn program shares: warm up, read the
// heap bump either side of 1000 rounds, read the underflow counter, and pin
// the accumulated answer so the work is not dead.
func mapChurnMain(acc int) string {
	return `function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 100) { acc = acc + build(w); w = w + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc != ` + strconv.Itoa(acc) + `) { return 88; }
    return 0;
}
`
}

func TestSelfHostMapDeleteReleaseX86_64(t *testing.T) {
	_, runner := x86_64Tooling(t)
	runMapChurnTyped(t, runner, "x86-64-linux", mapDeleteReleasePrograms)
}

func TestSelfHostMapDeleteReleaseArm64(t *testing.T) {
	_, qemu := arm64Tooling(t)
	runMapChurnTyped(t, []string{qemu}, "arm64-linux", mapDeleteReleasePrograms)
}

func TestSelfHostMapDeleteReleaseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	runMapChurnTyped(t, nil, "wasm32-wasi", mapDeleteReleasePrograms)
}

// The AST lowering's wasm leg takes the same runtime delete, and its maps own
// their columns per insert on wasm as the typed path's do, so a delete there
// now releases too. That leg leaks the delete's own tuple and the map through
// it, so the pin is RELATIVE, between two deletes: one of a key that is
// present and one of a key that is absent. Both lose the map the same way;
// only the present key's entry is released, so deleting it must grow the heap
// STRICTLY less than missing it. Measured at 400,000 against 432,000 bytes
// over the 1000 rounds — the entry's two boxes a round — where before the fix
// both read 432,000. The register backends' AST maps own their columns only
// under a credit the delete does not read, so they keep the release-nothing
// delete and are not measured here.
func TestSelfHostMapDeleteReleaseWasmAST(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
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

	const head = `import "core/map";
import "core/cmp";
function build(n: i32): i32 {
    var m: Map[string, string] = map_new(2);
    var i: i32 = 0;
    while (i < 6) { m = m.insert("k" + i.to_string(), "v" + i.to_string()); i = i + 1; }
`
	const tail = `}
function main(): i32 {
    var acc: i32 = 0;
    var w: i32 = 0;
    while (w < 100) { acc = acc + build(w); w = w + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 1000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    print((s2 - s1).to_string());
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
`
	bump := func(name, body string) int {
		t.Helper()
		work := t.TempDir()
		src := filepath.Join(work, name+".fern")
		if err := os.WriteFile(src, []byte(head+body+tail), 0o644); err != nil {
			t.Fatal(err)
		}
		wat := filepath.Join(work, name+".wat")
		cmd := exec.Command(fernBin, "-target", "wasm32-wasi", "-emit", "asm", src, stdlibRoot, "-o", wat)
		cmd.Env = append(os.Environ(), "FERN_SEM_IR=")
		var cerr strings.Builder
		cmd.Stderr = &cerr
		if err := cmd.Run(); err != nil {
			t.Fatalf("compile %s: %v\n%s", name, err, cerr.String())
		}
		run := exec.Command("wasmtime", "run", wat)
		var errBuf strings.Builder
		run.Stderr = &errBuf
		out, _ := run.Output()
		if code := run.ProcessState.ExitCode(); code != 0 {
			t.Fatalf("%s exited %d, want 0 (99 = over-release)\n%s", name, code, strings.TrimSpace(errBuf.String()))
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil {
			t.Fatalf("%s printed %q, want the heap bump", name, out)
		}
		return n
	}
	absent := bump("absent", "    var (m2, gone) = m.without(\"z\" + \"z\");\n    if (gone) { return 0 - 1; }\n    return m2.len() + n - n;\n")
	present := bump("present", "    var (m2, gone) = m.without(\"k\" + \"3\");\n    if (!gone) { return 0 - 1; }\n    return m2.len() + 1 + n - n;\n")
	if present >= absent {
		t.Fatalf("deleting a present key grows the heap by %d bytes over 1000 rounds against %d for an absent one: "+
			"the deleted entry's key and value are stranded rather than released", present, absent)
	}
}
