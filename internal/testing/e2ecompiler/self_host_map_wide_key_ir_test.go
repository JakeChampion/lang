package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// wideMapKeyCases pin an i64 / u64 key column on the typed lowering (#10005).
// The register runtimes hold every key at eight bytes, so a wide key takes the
// integer column; wasm's key cell is four bytes, so there each key is boxed
// into a cell the map owns and probes through (the `_k64` helpers). Every key
// is above 2^32 or below -2^32, so a column that kept 32 bits of it would
// collide or miss. The interpreter and native answer every program 42, and
// each test runs every program a second time under FERN_LEAKCHECK, whose
// census has to balance: the key cells wasm boxes are freed by the map alone.
var wideMapKeyCases = []struct {
	name string
	src  string
}{
	// Insert, has, get, get_or, keys, pair iteration, without, an i64 value
	// column beside an i64 key, and a u64 key at the top of its range.
	{"wide-keys", `import "core/map";
function main(): i32 {
    let m: Map[i64, i32] = map_new(4);
    let big: i64 = 4294967296 + 7;
    m = m.insert(big, 10);
    m = m.insert(7 as i64, 20);
    m = m.insert(0 - big, 30);
    let r: i32 = 0;
    if (m.len() != 3) { return 1; }
    if (m.get_or(big, 0) != 10) { return 2; }
    if (m.get_or(7 as i64, 0) != 20) { return 3; }
    if (m.get_or(0 - big, 0) != 30) { return 4; }
    if (m.has(4294967296 as i64)) { return 5; }
    let ks: i64[] = m.keys();
    if (ks.len() != 3) { return 6; }
    if (ks[0] != big) { return 7; }
    let s: i64 = 0;
    for k in ks { s = s + k; }
    if (s != 7 as i64) { return 8; }
    let t: i32 = 0;
    for (k, v) in m { if (k == big) { t = t + v; } }
    if (t != 10) { return 9; }
    let w: Map[i64, i64] = map_new(2);
    w = w.insert(big, big * 2);
    if (w.get_or(big, 0 as i64) != big * 2) { return 11; }
    let u: Map[u64, i32] = map_new(2);
    u = u.insert(18446744073709551615 as u64, 3);
    if (u.get_or(18446744073709551615 as u64, 0) != 3) { return 12; }
    match (m.get(big)) { Some(v) => { if (v != 10) { return 13; } }, None => { return 14; } }
    let m2: Map[i64, i32] = m.without(big).0;
    if (m2.has(big)) { return 15; }
    if (m2.len() != 2) { return 16; }
    return 42;
}
`},
	// Growth past the initial capacity with cells to rehash, deletes that leave
	// tombstones and reinserts over them, overwrites that discard the incoming
	// key, an aliased map copied before its insert, and a u64-keyed map with a
	// wide value column read back through iteration, keys and get.
	// Five shared maps rebuilt in one function, their key and value columns
	// of every width: the rebuild's scratch slots are one per width, so a
	// narrow column and a wide one never share a slot's declared type
	// (#10779).
	{"mixed-width-rebuilds", `import "core/map";
function main(): i32 {
    let a: Map[string, u8] = map_new(2);
    let a2: Map[string, u8] = a;
    a = a.insert("x", 10);
    let b: Map[string, i64] = map_new(2);
    let b2: Map[string, i64] = b;
    b = b.insert("y", 4294967296 + 5);
    let c: Map[i64, i32] = map_new(2);
    let c2: Map[i64, i32] = c;
    c = c.insert(4294967296 + 9, 9);
    let d: Map[i64, i64] = map_new(2);
    let d2: Map[i64, i64] = d;
    d = d.insert(4294967296 + 11, 4294967296 + 12);
    let e: Map[string, f64] = map_new(2);
    let e2: Map[string, f64] = e;
    e = e.insert("z", 2.5);
    if ((a.get_or("x", 0) as i32) != 10) { return 1; }
    if (b.get_or("y", 0 as i64) != 4294967296 + 5) { return 2; }
    if (c.get_or(4294967296 + 9, 0) != 9) { return 3; }
    if (d.get_or(4294967296 + 11, 0 as i64) != 4294967296 + 12) { return 4; }
    if (e.get_or("z", 0.0) != 2.5) { return 5; }
    if (a2.len() + b2.len() + c2.len() + d2.len() + e2.len() != 0) { return 6; }
    return 42;
}
`},
	{"wide-keys-churn", `import "core/map";
function fill(n: i32): Map[i64, i32] {
    let m: Map[i64, i32] = map_new(2);
    let i: i32 = 0;
    while (i < n) { m = m.insert((i as i64) * 4294967296 + (i as i64), i); i = i + 1; }
    return m;
}
function main(): i32 {
    let m: Map[i64, i32] = fill(40);
    if (m.len() != 40) { return 1; }
    let i: i32 = 0;
    while (i < 40) { if (m.get_or((i as i64) * 4294967296 + (i as i64), 0 - 1) != i) { return 2; } i = i + 1; }
    if (m.has(1 as i64)) { return 3; }
    i = 0;
    while (i < 40) { m = m.without((i as i64) * 4294967296 + (i as i64)).0; i = i + 2; }
    if (m.len() != 20) { return 4; }
    i = 0;
    while (i < 40) { m = m.insert((i as i64) * 4294967296 + (i as i64), i * 10); i = i + 2; }
    if (m.len() != 40) { return 5; }
    let s: i64 = 0;
    let t: i32 = 0;
    for (k, v) in m { s = s + k; t = t + v; }
    if (t != 4200) { return 6; }
    if (s != (0 as i64)) { s = s - s; }
    let a: Map[i64, i32] = m;
    let b: Map[i64, i32] = a.insert(99 as i64, 99);
    if (b.len() != 41) { return 7; }
    if (a.len() != 40) { return 8; }
    if (m.len() != 40) { return 9; }
    b = b.insert(99 as i64, 100);
    b = b.insert(99 as i64, 101);
    if (b.get_or(99 as i64, 0) != 101) { return 10; }
    if (b.len() != 41) { return 11; }
    let w: Map[u64, i64] = map_new(1);
    i = 0;
    while (i < 12) { w = w.insert((18446744073709551615 as u64) - (i as u64), (i as i64) * 4294967296); i = i + 1; }
    let ws: i64 = 0;
    for (k, v) in w { ws = ws + v; }
    if (ws != (66 as i64) * 4294967296) { return 12; }
    let ks: u64[] = w.keys();
    if (ks.len() != 12) { return 13; }
    if (ks[11] != (18446744073709551615 as u64) - (11 as u64)) { return 14; }
    match (w.get((18446744073709551615 as u64) - (5 as u64))) { Some(v) => { if (v != (5 as i64) * 4294967296) { return 15; } }, None => { return 16; } }
    return 42;
}
`},
	// A wide key beside a value column the map reclaims: strings, string
	// arrays and struct boxes, each overwritten, aliased before an insert and
	// deleted, and an f64 column overwritten. On wasm the key is a cell the
	// map owns and the value column carries its own release flags, so this is
	// where the two meet.
	{"wide-keys-counted-values", `import "core/map";
struct Item { name: string, n: i32 }
function w(i: i32): string {
    let t: string = "x";
    if (i % 2 == 0) { t = "yy"; }
    return "v-a-wide-payload-past-any-inline-threshold-" + t;
}
function main(): i32 {
    let big: i64 = 4294967296;
    let s: Map[i64, string] = map_new(2);
    let i: i32 = 0;
    while (i < 20) { s = s.insert(big * (i as i64) + 1, w(i)); i = i + 1; }
    s = s.insert(big + 1, w(3));
    let s2: Map[i64, string] = s;
    s = s.insert(big * 99, w(4));
    if (s2.len() != 20) { return 1; }
    if (s.len() != 21) { return 2; }
    s = s.without(big + 1).0;
    if (s.len() != 20) { return 3; }
    if (s.get_or(big * 99, "").len() != w(4).len()) { return 4; }
    let total: i32 = 0;
    for (k, v) in s { total = total + v.len(); }
    // Keys 0 and 2..19 keep w(i) and big*99 holds w(4): ten payloads of
    // 45 bytes, nine of 44 and one more of 45.
    if (total != 891) { return 5; }
    let a: Map[i64, string[]] = map_new(2);
    a = a.insert(big * 3, [w(1), w(2)]);
    a = a.insert(big * 3, [w(5)]);
    let a2: Map[i64, string[]] = a;
    a = a.insert(big * 5, [w(6), w(7), w(8)]);
    if (a.get_or(big * 3, []).len() != 1) { return 6; }
    if (a2.len() != 1) { return 7; }
    a = a.without(big * 3).0;
    if (a.len() != 1) { return 8; }
    let t: Map[i64, Item] = map_new(2);
    t = t.insert(big * 7, Item { name: w(1), n: 1 });
    t = t.insert(big * 7, Item { name: w(2), n: 2 });
    let t2: Map[i64, Item] = t;
    t = t.insert(big * 8, Item { name: w(3), n: 3 });
    match (t.get(big * 7)) { Some(it) => { if (it.n != 2) { return 9; } }, None => { return 10; } }
    t = t.without(big * 8).0;
    if (t.len() != 1) { return 11; }
    if (t2.len() != 1) { return 12; }
    let f: Map[i64, f64] = map_new(2);
    f = f.insert(big * 9, 2.5);
    f = f.insert(big * 9, 3.5);
    if (f.get_or(big * 9, 0.0) != 3.5) { return 13; }
    return 42;
}
`},
}

func TestSelfHostMapWideKeyIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range wideMapKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != 42 {
				t.Errorf("%s: exit %d, want 42 (the code names the failing step); out %q", tc.name, code, out)
			}
			bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", tc.src, "FERN_LEAKCHECK=1"))
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != 42 {
				t.Fatalf("census run: exit %d, want 42\n%s", exit, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostMapWideKeyIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range wideMapKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != 42 {
				t.Errorf("%s arm64: exit %d, want 42; out %q", tc.name, code, out)
			}
			cmd := runArm64Bin(qemu, buildBinArm64(t, gcc, t.TempDir(), "census", cli.emit(t, "arm64-linux", tc.src, "FERN_LEAKCHECK=1")))
			var eb strings.Builder
			cmd.Stderr = &eb
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Fatalf("census run: exit %d, want 42\n%s", code, eb.String())
			}
			assertBalancedCensus(t, eb.String())
		})
	}
}

func TestSelfHostMapWideKeyWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range wideMapKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			wat := cli.emit(t, "wasm32-wasi", tc.src)
			if code, out := runWasm(t, wat); code != 42 {
				t.Errorf("%s wasm: exit %d, want 42; out %q\nstderr:\n%s", tc.name, code, out, wasmStderr(t, wat))
			}
			census := filepath.Join(t.TempDir(), "census.wat")
			if err := os.WriteFile(census, []byte(cli.emit(t, "wasm32-wasi", tc.src, "FERN_LEAKCHECK=1")), 0o644); err != nil {
				t.Fatal(err)
			}
			stderr, exit := runWasmCensus(t, census)
			if exit != 42 {
				t.Fatalf("census run: exit %d, want 42\n%s", exit, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

// wasmStderr runs wat under wasmtime and answers what it wrote to stderr: a
// validation error or a trap names the failing site, which the exit code
// alone does not.
func wasmStderr(t *testing.T, wat string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prog.wat")
	if err := os.WriteFile(path, []byte(wat), 0o644); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("wasmtime", path)
	var stderr bytes.Buffer
	run.Stderr = &stderr
	_ = run.Run()
	return stderr.String()
}
