package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWideMapKeyRefusesEverySurface pins that a map key too wide for
// the integer key column is REFUSED wherever it is reached, rather than
// lowered against a column it does not fit.
//
// An `i64` / `u64` / `f64` / `usize` key has nowhere to live: the integer
// column is a 4-byte cell on wasm, and the string column reads a key's VALUE
// as an address — which is how `Map[f64, V]` segfaulted before #9973. The
// lowering refuses them instead, and this is the test that the refusal covers
// every surface rather than the one that was written first: the gate began
// life on the map METHODS alone, where a program that only constructs or only
// iterates such a map still lowered silently (caught in review).
//
// Each case isolates one surface. The three parameter cases pass a bare
// `map_new(2)` rather than a binding, so the construction gate cannot fire
// first and mask the one under test. The i32 control proves the cases are
// refused for their KEY and not for their shape — without it, a gate that
// refused every map would pass this test.
var wideMapKeyCases = []struct {
	name   string
	src    string
	wantIR bool
	oracle int
}{
	{"construct-and-insert", `import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(2);
    m = m.insert(7, 3);
    return m.len() + 7;
}
`, false, 8},
	{"pair-iteration-of-a-parameter", `import "core/map";
function total(m: Map[i64, i32]): i32 {
    var s: i32 = 0;
    for (k, v) in m { s = s + v; }
    return s + 7;
}
function main(): i32 { return total(map_new(2)); }
`, false, 7},
	{"keys-of-a-parameter", `import "core/map";
function total(m: Map[u64, i32]): i32 {
    var s: i32 = 0;
    for k in m.keys() { s = s + 1; }
    return s + 7;
}
function main(): i32 { return total(map_new(2)); }
`, false, 7},
	{"method-on-a-parameter", `import "core/map";
function total(m: Map[f64, i32]): i32 { return m.len() + 7; }
function main(): i32 { return total(map_new(2)); }
`, false, 7},
	// The control: the same iteration over a key that DOES fit the column.
	{"pair-iteration-of-an-i32-parameter", `import "core/map";
function total(m: Map[i32, i32]): i32 {
    var s: i32 = 0;
    for (k, v) in m { s = s + v; }
    return s + 7;
}
function main(): i32 { return total(map_new(2)); }
`, true, 7},
}

func TestSelfHostWideMapKeyRefusesEverySurface(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host driver runs natively only")
	}
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range wideMapKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "wide_map_key_"+strings.ReplaceAll(tc.name, "-", "_")+".fern")
			if err := os.WriteFile(entry, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			// The interpreter is the oracle for what the program MEANS, and
			// says these are well-formed: the refusal below is about the
			// column, not about a program nobody can run.
			if _, got := runFixtureInterp(t, entry, ""); got != tc.oracle {
				t.Fatalf("interpreter = %d, want %d (the case stopped exercising what it describes)", got, tc.oracle)
			}

			route, _ := exec.Command(driver, entry, root, "-decide").Output()
			got := strings.TrimSpace(string(route))
			if tc.wantIR {
				if got != "ir" {
					t.Errorf("-decide = %q, want \"ir\": an i32 key fits the column, so refusing it "+
						"means the gate is refusing maps rather than wide keys", got)
				}
				return
			}
			if got != "ast" {
				t.Errorf("-decide = %q, want \"ast\": this key does not fit the integer column, and "+
					"lowering it anyway is the silent wrong answer the refusal exists to prevent", got)
			}
		})
	}
}
