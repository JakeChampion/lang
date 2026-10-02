package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `m.without(k)` on a map an alias still reads deletes from a copy, so the
// alias keeps every entry, as on native and the interpreter (#9835). The alias
// may be bound before the delete, through either name, or later in an
// enclosing loop.
var mapWithoutAliasCases = []struct {
	name string
	src  string
	want int
}{
	{"issue", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    let snapshot: Map[i32, i32] = m;
    let (rest, had) = m.without(2);
    return rest.len() * 10 + (if (had) { 1 } else { 0 }) + snapshot.len() * 40 + m.len();
}
`, 93},
	{"snapshot_read", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    m = m.insert(3, 30);
    let snapshot: Map[i32, i32] = m;
    let (rest, had) = m.without(2);
    if (!had || rest.has(2) || rest.get_or(3, -1) != 30) { return 99; }
    let sum: i32 = 0;
    for k in snapshot.keys() { sum = sum + snapshot.get_or(k, 0); }
    return sum + snapshot.get_or(2, -1) + rest.len();
}
`, 82},
	{"through_alias", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    let snapshot: Map[i32, i32] = m;
    let (rest, had) = snapshot.without(2);
    let grown: Map[i32, i32] = m;
    grown = grown.insert(3, 30);
    return rest.len() * 10 + m.len() + grown.len() * 30;
}
`, 102},
	{"rebind", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    m = m.insert(3, 30);
    let snapshot: Map[i32, i32] = m;
    m = m.without(1).0;
    m = m.without(3).0;
    return m.len() * 10 + snapshot.len() + snapshot.get_or(1, 0);
}
`, 23},
	{"loop", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(8);
    let i: i32 = 0;
    while (i < 5) { m = m.insert(i, i * 2); i = i + 1; }
    let keep: Map[i32, i32] = map_new(8);
    let acc: i32 = 0;
    let r: i32 = 0;
    while (r < 3) {
        let (rest, had) = m.without(r);
        if (had) { acc = acc + rest.len(); }
        keep = m;
        r = r + 1;
    }
    return acc * 8 + keep.len() + m.len();
}
`, 106},
	{"string_value", `import "core/map";
function main(): i32 {
    let m: Map[string, string] = map_new(8);
    m = m.insert("a" + "k", "x" + "y");
    m = m.insert("b" + "k", "p" + "qr");
    let snapshot: Map[string, string] = m;
    let (rest, had) = m.without("bk");
    if (!had || rest.has("bk")) { return 99; }
    return rest.len() * 10 + snapshot.len() + snapshot.get_or("bk", "").len() * 20 + m.get_or("ak", "").len();
}
`, 74},
}

// mapWithoutUnaliased deletes from a map nothing else names, which stays in
// place: the only allocation the delete adds is the tuple it returns.
const mapWithoutUnaliased = `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    let (rest, had) = m.without(2);
    return rest.len() * 10 + (if (had) { 1 } else { 0 });
}
`

const mapWithoutUnaliasedWant = 11

func TestSelfHostMapWithoutAliasX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range mapWithoutAliasCases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), tc.name+".fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
				stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, mode), nil)
				if exit != tc.want {
					t.Fatalf("%s: exit = %d, want %d\n%s", mode, exit, tc.want, stderr)
				}
				assertNoSanitizerFault(t, stderr)
			}
		})
	}
	t.Run("unaliased_in_place", func(t *testing.T) {
		allocs := func(source string, want int) int64 {
			stderr, exit := cli.exitOf(t, source, "x86-64-linux", "FERN_LEAKCHECK=1")
			if exit != want {
				t.Fatalf("exit = %d, want %d\n%s", exit, want, stderr)
			}
			return censusAllocs(t, stderr)
		}
		assertWithoutAddsTuple(t, allocs)
	})
}

func TestSelfHostMapWithoutAliasWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range mapWithoutAliasCases {
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := cli.exitOf(t, tc.src, "wasm32-wasi", "FERN_LEAKCHECK=1")
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
		})
	}
	t.Run("unaliased_in_place", func(t *testing.T) {
		allocs := func(source string, want int) int64 {
			stderr, exit := cli.exitOf(t, source, "wasm32-wasi", "FERN_LEAKCHECK=1")
			if exit != want {
				t.Fatalf("exit = %d, want %d\n%s", exit, want, stderr)
			}
			return censusAllocs(t, stderr)
		}
		assertWithoutAddsTuple(t, allocs)
	})
}

// assertWithoutAddsTuple compares mapWithoutUnaliased against the same program
// with the delete taken out: a copy of the map would add its box and columns.
func assertWithoutAddsTuple(t *testing.T, allocs func(string, int) int64) {
	t.Helper()
	noDelete := strings.Replace(mapWithoutUnaliased, "let (rest, had) = m.without(2);", "let rest: Map[i32, i32] = m;\n    let had: boolean = true;", 1)
	with := allocs(mapWithoutUnaliased, mapWithoutUnaliasedWant)
	without := allocs(noDelete, 21)
	if with != without+1 {
		t.Fatalf("allocs with the delete = %d, without = %d: want exactly the tuple more", with, without)
	}
}

// assertNoSanitizerFault fails on any sanitizer report but a leak.
func assertNoSanitizerFault(t *testing.T, stderr string) {
	t.Helper()
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "fern-sanitizer:") && !strings.HasPrefix(line, "fern-sanitizer: leak ") {
			t.Fatalf("sanitizer fault: %s\n%s", line, stderr)
		}
	}
}

// censusAllocs reads the allocation count off a run's leakcheck summary.
func censusAllocs(t *testing.T, stderr string) int64 {
	t.Helper()
	var allocs, frees, live int64
	if _, err := fmtSscan(leakSummaryLine(stderr), &allocs, &frees, &live); err != nil || allocs == 0 {
		t.Fatalf("no leakcheck census (%v): %q", err, stderr)
	}
	return allocs
}
