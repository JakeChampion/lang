package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real stdlib MODULES on the self-host IR path. Several self-host tests
// historically INLINED stdlib aggregate types (HeaderMap → "Headers",
// BytesWriter → "BW", Stream → "Buf") because the importless single-program
// driver could not `import "std/…"`. With stdlib loading + the (default-on)
// treeshake prune, the self-hosted compiler now compiles programs that import
// those REAL modules and use their public API, routing IR and matching the
// native interpreter. Drives the self-hosted x86-64 loader (asm_load_run) with
// the repo's real stdlib as the root — proof the inlining workarounds are no
// longer required.
var stdlibModuleCases = []struct {
	name string
	src  string
}{
	// std/headers HeaderMap: case-insensitive multimap (two parallel string[]
	// fields), functional struct-spread `append`, `get` → Option[string], `len`.
	{"headers", `import "std/headers";
function main(): i32 {
    var h = headers.header_map_new();
    h = h.append("Content-Type", "text/html");
    h = h.append("X-Count", "7");
    match (h.get("Content-Type")) {
        Some(v) => { if (v == "text/html") { return h.len() * 21; } return 0; },
        None => { return 0; }
    }
}`},
	// std/io_buffered BytesWriter: u8[]-backed builder, functional write_string,
	// len. "hello" + "!" = 6 bytes * 7 = 42.
	{"bytes-writer", `import "std/io_buffered";
function main(): i32 {
    var w = io_buffered.bytes_writer_new();
    w = w.write_string("hello");
    w = w.write_string("!");
    return w.len() * 7;
}`},
	// std/stream Stream: u8[] + cursor reader. len("hello world") = 11 * 3 = 33.
	{"stream", `import "std/stream";
function main(): i32 {
    var s = stream.stream_from_string("hello world");
    return s.len() * 3;
}`},
	// core/cmp's Eq-driven generic verbs (`contains` / `index_of` / `distinct`,
	// bound `[T: Eq]`) on the REAL stdlib via the self-host loader: the bound
	// makes them monomorphise per element type, so the i32 instances keep the
	// scalar `==` while the string instances dispatch byte equality — proof the
	// shipped bodies (not an inlined copy) lower through the IR path for both
	// element types. #2689 / #5348 (the verbs' single home is core/cmp).
	{"cmp-eq-verbs", `import "core/cmp" as cmp;
function main(): i32 {
    var a: i32[] = [10, 20, 30, 20];
    var ss: string[] = ["a", "b", "a", "c", "b"];
    var r: i32 = 0;
    if (cmp.contains(a, 20)) { r = r + 1; }
    if (!cmp.contains(a, 99)) { r = r + 2; }
    if (cmp.contains(ss, "c")) { r = r + 4; }
    if (!cmp.contains(ss, "z")) { r = r + 8; }
    if (cmp.distinct(a).len() == 3) { r = r + 16; }
    if (cmp.distinct(ss).len() == 3) { r = r + 32; }
    match (cmp.index_of(ss, "c")) {
        Some(v) => { if (v == 3) { r = r + 64; } },
        None => {}
    }
    return r; // 127
}`},
	// The persistent collections (#6794) through the self-host loader: generic
	// enums / structs with receiver methods whose bodies call bounded free
	// generics on the struct's own type vars (`__om_insert(m.root, k, v)` under
	// `OrdMap[K, V]`). Those calls are instantiated per STRUCT clone, with the
	// receiver typed in the env — pre-fix the method body was rewritten once
	// with `k: K`, keying an erased clone (`__om_insert__K__V`) whose own
	// generic calls then dangled. Each row inserts, snapshots, removes and reads
	// both the live value and the snapshot.
	{"ordmap", `import "std/ordmap";
function main(): i32 {
    var m: ordmap.OrdMap[i32, string] = ordmap.ordmap_new();
    var i: i32 = 0;
    while (i < 50) { m = m.insert((i * 7) % 50, "v"); i = i + 1; }
    var snap: ordmap.OrdMap[i32, string] = m;
    m = m.remove(7);
    m = m.insert(100, "w");
    if (!m.is_valid() || !snap.is_valid()) { return 1; }
    if (m.len() != 50 || snap.len() != 50) { return 2; }
    if (m.contains(7) || !snap.contains(7) || !m.contains(100) || snap.contains(100)) { return 3; }
    if (m.get_or(100, "") != "w" || snap.get_or(3, "") != "v") { return 4; }
    return 42;
}`},
	{"pmap", `import "std/pmap";
function main(): i32 {
    var m: pmap.PMap[string, i32] = pmap.pmap_new();
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.insert("c", 3);
    var snap: pmap.PMap[string, i32] = m;
    m = m.remove("b");
    m = m.insert("d", 4);
    if (!m.is_valid() || !snap.is_valid()) { return 1; }
    if (m.len() != 3 || snap.len() != 3) { return 2; }
    if (m.contains("b") || !snap.contains("b") || !m.contains("d") || snap.contains("d")) { return 3; }
    if (m.get_or("d", 0) != 4 || snap.get_or("a", 0) != 1) { return 4; }
    return 42;
}`},
	// pvec's `get_or` descends the trie in a loop that returns the leaf's
	// array straight out of a match binding — the borrowed-payload return that
	// must retain, or the snapshot's leaf is freed under it.
	{"pvec", `import "std/pvec";
function main(): i32 {
    var v: pvec.PVec[i32] = pvec.pvec_new();
    var i: i32 = 0;
    while (i < 100) { v = v.append(i); i = i + 1; }
    var snap: pvec.PVec[i32] = v;
    v = v.with(5, 500);
    v = v.pop();
    if (!v.is_valid() || !snap.is_valid()) { return 1; }
    if (v.len() != 99 || snap.len() != 100) { return 2; }
    if (v.get_or(5, -1) != 500 || snap.get_or(5, -1) != 5 || snap.get_or(99, -1) != 99) { return 3; }
    return 42;
}`},
	{"ordset", `import "std/ordset";
function main(): i32 {
    var s: ordset.OrdSet[i32] = ordset.ordset_of([5, 3, 9, 3, 1]);
    var snap: ordset.OrdSet[i32] = s;
    s = s.remove(3);
    s = s.add(7);
    if (!s.is_valid() || !snap.is_valid()) { return 1; }
    if (s.len() != 4 || snap.len() != 4) { return 2; }
    if (s.contains(3) || !snap.contains(3) || !s.contains(7) || snap.contains(7)) { return 3; }
    var xs: i32[] = s.to_array();
    if (xs.len() != 4 || xs[0] != 1 || xs[3] != 9) { return 4; }
    return 42;
}`},
	{"pset", `import "std/pset";
function main(): i32 {
    var s: pset.PSet[string] = pset.pset_of(["a", "b", "a", "c"]);
    var snap: pset.PSet[string] = s;
    s = s.remove("a");
    s = s.add("d");
    if (!s.is_valid() || !snap.is_valid()) { return 1; }
    if (s.len() != 3 || snap.len() != 3) { return 2; }
    if (s.contains("a") || !snap.contains("a") || !s.contains("d") || snap.contains("d")) { return 3; }
    if (!s.union(snap).contains("a") || s.union(snap).len() != 4) { return 4; }
    return 42;
}`},
}

func TestSelfHostStdlibModulesIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range stdlibModuleCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "sm_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "sm_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
