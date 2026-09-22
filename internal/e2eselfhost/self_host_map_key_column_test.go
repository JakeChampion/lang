package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMapKeyWithNoColumnRefusesEverySurface pins that a map key too wide for
// the integer key column is REFUSED wherever it is reached, rather than
// lowered against a column it does not fit.
//
// An `i64` / `u64` / `usize` key has nowhere to live: the integer column is a
// 4-byte cell on wasm, and the string column reads a key's VALUE as an
// address — which is how these segfaulted before #9973. The lowering refuses
// them instead, and this is the test that the refusal covers every surface
// rather than the one that was written first: the gate began life on the map
// METHODS alone, where a program that only constructs or only iterates such a
// map still lowered silently (caught in review).
//
// Float keys are not among the cases. They have no column either, but E045
// refuses them in both checkers now (#10009), so the interpreter oracle below
// rejects such a program and there is no lowering decision left to pin.
//
// Each case isolates one surface. The three parameter cases pass a bare
// `map_new(2)` rather than a binding, so the construction gate cannot fire
// first and mask the one under test. The i32 control proves the cases are
// refused for their KEY and not for their shape — without it, a gate that
// refused every map would pass this test.
var noColumnMapKeyCases = []struct {
	name   string
	src    string
	wantIR bool
	oracle int
}{
	// Construction ALONE, with no method on the map and no loop over it — the
	// only shape that isolates the construction gate. `construct-and-insert`
	// below does not: its `.insert` trips the method gate, so that row stays
	// green with the construction gate deleted (caught in review, by deleting
	// it). This one routes `ir` without it.
	{"construct-only", `import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(2);
    return 7;
}
`, false, 7},
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
function total(m: Map[usize, i32]): i32 { return m.len() + 7; }
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

func TestSelfHostMapKeyWithNoColumnRefusesEverySurface(t *testing.T) {
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

	for _, tc := range noColumnMapKeyCases {
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

// narrowMapKeyCases are the keys that DO fit the integer column, each read
// back through the shapes a key column is consumed by.
//
// `f32` is NOT here: it fits the cell by width, but E045 refuses every float
// key in both checkers now (#10009) — and it had to be refused somewhere,
// because neither compiler gets an f32 key to the cell as an i32 on wasm
// (#10008), so admitting it would make acceptance depend on the target.
var narrowMapKeyCases = []struct {
	name   string
	src    string
	oracle int
}{
	{"u8", `import "core/map";
function main(): i32 {
    var m: Map[u8, i32] = map_new(2);
    var i: i32 = 0;
    while (i < 12) { m = m.insert(i as u8, i * 2); i = i + 1; }
    var s: i32 = 0;
    for (k, v) in m { s = s + (k as i32) + v; }
    return s + m.len();
}
`, 210},
	{"u32", `import "core/map";
function main(): i32 {
    var m: Map[u32, i32] = map_new(2);
    var i: i32 = 0;
    while (i < 12) { m = m.insert(i as u32, i * 2); i = i + 1; }
    var s: i32 = 0;
    for k in m.keys() { s = s + m.get_or(k, 0); }
    return s + m.len();
}
`, 144},
}

// TestSelfHostNarrowMapKeyAnswersX86_64 is the other half of the wide-key
// refusal: the keys that DO fit the column have to answer what the
// interpreter answers, not merely lower.
//
// Both of these segfaulted before #9973 — `map_key_kind_of` asked whether the
// type was spelled `Map[i32,`, so a `u8` or `u32` key took the STRING column
// and the insert path read the key's VALUE as an address. A
// refusal test alone cannot catch that coming back: a gate that refused these
// too would pass it. This runs them.
func TestSelfHostNarrowMapKeyAnswersX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("self-host CLI runs natively only (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	for _, tc := range narrowMapKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := filepath.Join(dir, "narrow_map_key_"+tc.name+".fern")
			if err := os.WriteFile(prog, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write program: %v", err)
			}
			if _, got := runFixtureInterp(t, prog, ""); got != tc.oracle {
				t.Fatalf("interpreter = %d, want %d (the case stopped exercising what it describes)", got, tc.oracle)
			}

			asmPath := filepath.Join(dir, "narrow_map_key_"+tc.name+".s")
			if out, err := exec.Command(cli, "-target", "x86-64-linux", "-emit", "asm",
				prog, stdlib, "-o", asmPath).CombinedOutput(); err != nil {
				t.Fatalf("self-host compile failed: %v\n%s", err, out)
			}
			asm, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatalf("read asm: %v", err)
			}
			bin := buildBin(t, gcc, dir, "narrow_map_key_"+tc.name, string(asm))
			run := exec.Command(bin)
			_ = run.Run()
			// A SIGSEGV is the shape this regresses to, and Go reports a
			// signalled process as exit code -1 rather than 128+n — so say
			// what happened instead of printing -1 as an answer.
			if code := run.ProcessState.ExitCode(); code != tc.oracle {
				if code < 0 {
					t.Fatalf("the self-host binary was killed by a signal (%s) — a %s key is being read "+
						"as a pointer again; the interpreter answers %d",
						run.ProcessState.String(), tc.name, tc.oracle)
				}
				t.Fatalf("self-host answered %d, interpreter says %d", code, tc.oracle)
			}
		})
	}
}
