package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An Option bound from a builtin `m.get(k)` and consumed by one match gives
// its box back after that match, as the direct `match (m.get(k))` form does
// (#10195, and the hoisted half of #10083 on wasm), whatever the map's value
// type (#10306).
var mapGetBoundCases = []struct {
	name string
	src  string
	want int
}{
	{"annotated", `import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    var g: Option[i32] = m.get("b");
    var r: i32 = 9;
    match (g) { Some(v) => { r = v + 40; }, None => { r = 9; } }
    return r;
}
`, 42},
	{"unannotated_in_loop", `import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 20) {
        var g = m.get("b");
        match (g) { Some(v) => { n = n + v; }, None => { n = n + 100; } }
        i = i + 1;
    }
    return n;
}
`, 40},
	{"early_return", `import "core/map";
@noinline
function find(m: Map[string, i32], k: string): i32 {
    var g: Option[i32] = m.get(k);
    if (k == "q") { return 3; }
    match (g) { Some(v) => { return v + 40; }, None => { return 9; } }
}
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("b", 2);
    return find(m, "b") + find(m, "z") + find(m, "q");
}
`, 54},
	{"string_value", `import "core/map";
function main(): i32 {
    var m: Map[string, string] = map_new(8);
    m = m.insert("a", "x" + "y");
    m = m.insert("b", "p" + "qr");
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 20) {
        var g: Option[string] = m.get("b");
        match (g) { Some(v) => { n = n + v.len(); }, None => { n = n + 100; } }
        i = i + 1;
    }
    return n;
}
`, 60},
	// An array-valued map's `get` hands back an Option box of its own; the
	// binding used to be retained as if it were the array, so the box-only
	// release left it at rc 1 (#10306).
	{"array_value", `import "core/map";
function main(): i32 {
    var m: Map[string, i32[]] = map_new(8);
    m = m.insert("a", [1, 2]);
    m = m.insert("b", [3, 4, 5]);
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 20) {
        var g = m.get("b");
        match (g) { Some(v) => { n = n + v.len(); }, None => { n = n + 100; } }
        i = i + 1;
    }
    return n;
}
`, 60},
	{"array_value_payload_kept", `import "core/map";
function main(): i32 {
    var m: Map[string, i32[]] = map_new(8);
    m = m.insert("a", [1, 2]);
    m = m.insert("b", [3, 4, 5]);
    var n: i32 = 0;
    var keep: i32[] = [];
    var i: i32 = 0;
    while (i < 20) {
        var g = m.get("b");
        match (g) { Some(v) => { keep = v; n = n + v.len(); }, None => { n = n + 100; } }
        i = i + 1;
    }
    return n + keep.len();
}
`, 63},
	{"array_value_payload_unbound", `import "core/map";
function main(): i32 {
    var m: Map[string, i32[]] = map_new(8);
    m = m.insert("b", [3, 4, 5]);
    var n: i32 = 0;
    var i: i32 = 0;
    while (i < 20) {
        var g = m.get("b");
        match (g) { Some(_) => { n = n + 1; }, None => { n = n + 100; } }
        i = i + 1;
    }
    return n;
}
`, 20},
	{"unused", `import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(8);
    m = m.insert("b", 2);
    var g = m.get("b");
    return 7;
}
`, 7},
}

func TestSelfHostMapGetBoundReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range mapGetBoundCases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), tc.name+".fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
				stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, mode), nil)
				if exit != tc.want || strings.Contains(stderr, "fern-sanitizer:") {
					t.Fatalf("%s: exit = %d, want %d, and the sanitizer silent\n%s", mode, exit, tc.want, stderr)
				}
				assertBalancedCensus(t, stderr)
			}
		})
	}
}

func TestSelfHostMapGetBoundReleaseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	cases := append(mapGetBoundCases[:len(mapGetBoundCases):len(mapGetBoundCases)], struct {
		name string
		src  string
		want int
	}{"arm_return", mapGetScrutineeSrc, (20 * (42 + 9)) % 101})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), tc.name+".fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
