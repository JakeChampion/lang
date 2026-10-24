package e2ecompiler

import (
	"os/exec"
	"strconv"
	"testing"
)

var mapDeleteReleasePrograms = []mapChurnProgram{
	{"string-key", `import "core/map";
import "core/cmp";
function build(n: i32): i32 {
    let m: Map[string, i32] = map_new(2);
    let i: i32 = 0;
    while (i < 6) { m = m.insert("k" + i.to_string(), i); i = i + 1; }
    let gone: boolean = m.contains("k" + "3");
    let m2 = m.remove("k" + "3");
    if (!gone) { return 0 - 1; }
    let absent: boolean = m2.contains("zz");
    let m3 = m2.remove("zz");
    if (absent) { return 0 - 2; }
    m3 = m3.insert("k3", 30);
    return m3.len() + m3.get_or("k3", 0) + n - n;
}
` + mapChurnMain(39600)},
	{"string-value", `import "core/map";
import "core/cmp";
function build(n: i32): i32 {
    let m: Map[i32, string] = map_new(2);
    let i: i32 = 0;
    while (i < 6) { m = m.insert(i, "v" + i.to_string()); i = i + 1; }
    let gone: boolean = m.contains(2);
    let m2 = m.remove(2);
    if (!gone) { return 0 - 1; }
    return m2.len() + m2.get_or(4, "").len() + n - n;
}
` + mapChurnMain(7700)},
	{"string-array-value", `import "core/map";
import "core/cmp";
function build(n: i32): i32 {
    let m: Map[i32, string[]] = map_new(2);
    let i: i32 = 0;
    while (i < 4) { m = m.insert(i, ["a" + i.to_string(), "b"]); i = i + 1; }
    let gone: boolean = m.contains(1);
    let m2 = m.remove(1);
    if (!gone) { return 0 - 1; }
    return m2.len() + m2.get_or(2, []).len() + n - n;
}
` + mapChurnMain(5500)},
	{"keyed", `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Name { first: string, rank: i32 }
function build(n: i32): i32 {
    let m: Map[Name, i32] = map_new(2);
    let i: i32 = 0;
    while (i < 6) { m = m.insert(Name { first: "k" + i.to_string(), rank: i }, i); i = i + 1; }
    let gone: boolean = m.contains(Name { first: "k" + "3", rank: 3 });
    let m2 = m.remove(Name { first: "k" + "3", rank: 3 });
    if (!gone) { return 0 - 1; }
    if (m2.contains(Name { first: "k3", rank: 3 })) { return 0 - 2; }
    return m2.len() + m2.get_or(Name { first: "k5", rank: 5 }, 0) + n - n;
}
` + mapChurnMain(11000)},
	{"keyed-string-value", `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Name { first: string, rank: i32 }
function build(n: i32): i32 {
    let m: Map[Name, string] = map_new(2);
    let i: i32 = 0;
    while (i < 6) { m = m.insert(Name { first: "k" + i.to_string(), rank: i }, "v" + i.to_string()); i = i + 1; }
    m = m.insert(Name { first: "k" + "2", rank: 2 }, "w" + "w");
    let gone: boolean = m.contains(Name { first: "k" + "4", rank: 4 });
    let m2 = m.remove(Name { first: "k" + "4", rank: 4 });
    if (!gone) { return 0 - 1; }
    return m2.len() + m2.get_or(Name { first: "k2", rank: 2 }, "").len() + n - n;
}
` + mapChurnMain(7700)},
	{"keyed-string-array-value", `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Name { first: string, rank: i32 }
function build(n: i32): i32 {
    let m: Map[Name, string[]] = map_new(2);
    let i: i32 = 0;
    while (i < 4) { m = m.insert(Name { first: "k" + i.to_string(), rank: i }, ["a" + i.to_string(), "b"]); i = i + 1; }
    let gone: boolean = m.contains(Name { first: "k" + "1", rank: 1 });
    let m2 = m.remove(Name { first: "k" + "1", rank: 1 });
    if (!gone) { return 0 - 1; }
    return m2.len() + m2.get_or(Name { first: "k3", rank: 3 }, []).len() + n - n;
}
` + mapChurnMain(5500)},
	{"keyed-boxed-value", `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Coord { a: i32, b: i32 }
struct Box { n: i32, tag: string }
function build(n: i32): i32 {
    let m: Map[Coord, Box] = map_new(2);
    let i: i32 = 0;
    while (i < 8) { m = m.insert(Coord { a: i, b: i * 2 }, Box { n: i * 10, tag: "t" + i.to_string() }); i = i + 1; }
    let gone: boolean = m.contains(Coord { a: 3, b: 6 });
    let m2 = m.remove(Coord { a: 3, b: 6 });
    if (!gone) { return 0 - 1; }
    let gone2: boolean = m2.contains(Coord { a: 5, b: 10 });
    let m3 = m2.remove(Coord { a: 5, b: 10 });
    if (!gone2) { return 0 - 2; }
    if (m3.len() != 6) { return 0 - 3; }
    return m3.get_or(Coord { a: 4, b: 8 }, Box { n: 0, tag: "" }).n + m3.len() + n - n;
}
` + mapChurnMain(50600)},
}

func mapChurnMain(acc int) string {
	return `function main(): i32 {
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 100) { acc = acc + build(w); w = w + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 1000) { acc = acc + build(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
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
