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
// Float keys are not among these cases: E045 refuses every WRITTEN float-key
// spelling in both checkers now (#10009), so the interpreter oracle below
// rejects such a program and there is no lowering decision left to pin. The
// one road a float key still travels — a monomorphised clone, which the
// self-host runs no diagnostic pass over — is
// TestSelfHostMonomorphisedFloatKeyStillRefusesToLower, which needs no
// oracle because native refuses the program outright.
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
	// key is the key type the refusal must name, for the rows that refuse.
	// Before #10032 the bail carried no description at all, so the message
	// said only which statement it unwound to — and for a tuple key it never
	// got that far: map_key_eqfn read `(i32` as a struct name and the program
	// died in the x86 assembler on an unencodable `leaq`.
	key string
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
`, false, 7, "i64"},
	{"construct-and-insert", `import "core/map";
function main(): i32 {
    var m: Map[i64, i32] = map_new(2);
    m = m.insert(7, 3);
    return m.len() + 7;
}
`, false, 8, "i64"},
	{"pair-iteration-of-a-parameter", `import "core/map";
function total(m: Map[i64, i32]): i32 {
    var s: i32 = 0;
    for (k, v) in m { s = s + v; }
    return s + 7;
}
function main(): i32 { return total(map_new(2)); }
`, false, 7, "i64"},
	{"keys-of-a-parameter", `import "core/map";
function total(m: Map[u64, i32]): i32 {
    var s: i32 = 0;
    for k in m.keys() { s = s + 1; }
    return s + 7;
}
function main(): i32 { return total(map_new(2)); }
`, false, 7, "u64"},
	{"method-on-a-parameter", `import "core/map";
function total(m: Map[usize, i32]): i32 { return m.len() + 7; }
function main(): i32 { return total(map_new(2)); }
`, false, 7, "usize"},
	// The COMPOSITE keys, refused for the opposite reason to the scalars
	// above: not too wide for the column, but with no hash or equality to
	// dispatch at all. A tuple is a struct without a name, so there is nowhere
	// to hang the derived Eq + Hash a struct key uses — map_key_eqfn named one
	// regardless, and asked the linker for `__fn_i32[]__eq` (#10032).
	//
	// The tuple row is also what pins the type string being split at the
	// TOP-LEVEL comma: a key of `(i32, i32)` contains the comma the split used
	// to stop at, which read the key as `(i32` and the value as `i32), i32`.
	// The refusal names the whole key, so a regression there shows up here as
	// a truncated name rather than as a wrong column much later.
	{"tuple-key", `import "core/map";
function main(): i32 {
    var m: Map[(i32, i32), i32] = map_new(8);
    m = m.insert((1, 2), 5);
    return m.get_or((1, 2), 0) + 9;
}
`, false, 14, "(i32, i32)"},
	{"array-key", `import "core/map";
function main(): i32 {
    var m: Map[i32[], i32] = map_new(8);
    m = m.insert([1, 2], 5);
    return m.get_or([1, 2], 0) + 9;
}
`, false, 14, "i32[]"},
	// The control: the same iteration over a key that DOES fit the column.
	{"pair-iteration-of-an-i32-parameter", `import "core/map";
function total(m: Map[i32, i32]): i32 {
    var s: i32 = 0;
    for (k, v) in m { s = s + v; }
    return s + 7;
}
function main(): i32 { return total(map_new(2)); }
`, true, 7, ""},
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
			if got != "refused" {
				t.Errorf("-decide = %q, want \"refused\": this key does not fit the integer column, and "+
					"lowering it anyway is the silent wrong answer the refusal exists to prevent", got)
			}
			// The route alone is only half of it. A refusal a reader cannot
			// act on is the state #10032 reported: the whole-module message
			// says to set FERN_STRICT_IR=1, and what that then printed named
			// the statement the bail unwound to and nothing about the key.
			cmd := exec.Command(driver, entry, root)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			var stderr strings.Builder
			cmd.Stderr = &stderr
			_ = cmd.Run()
			want := "a Map keyed by `" + tc.key + "` has no key column"
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("the strict refusal does not say %q, so it does not tell its reader which "+
					"key to change:\n%s", want, stderr.String())
			}
		})
	}
}

// narrowMapKeyCases are the keys that DO fit the integer column, each read
// back through the shapes a key column is consumed by — including a lookup
// for a key that is ABSENT, which no other case here covers.
//
// A key in the wrong column does not fail quietly: the string runtime reads
// its VALUE as an address, so the process dies. Which way it dies is not
// fixed — the pre-fix compiler SIGSEGVs on both boolean rows here, and a
// reviewer running the same revert saw one of them spin instead — so the
// failure below reports whatever the OS said rather than naming a signal.
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
	// A boolean is a non-pointer scalar, so it belongs in the integer column
	// too. The LITERAL spelling is the one that matters: the key-kind walk
	// had no ExprBool arm, so a bool-keyed literal took the string
	// constructor and the raw 0/1 went in as a pointer.
	//
	// Two rows because they fail for different reasons. This one never looks
	// up an absent key — inserting `false`, the raw 0, is what kills it, so
	// it catches the regression at CONSTRUCTION.
	{"boolean-literal", `import "core/map";
function main(): i32 {
    var m = Map { true: 5, false: 9 };
    var s: i32 = m.get_or(true, 0) + m.get_or(false, 0);
    if (m.has(true)) { s = s + 1; }
    if (!m.has(false)) { s = s + 100; }
    return s + m.len();
}
`, 17},
	// And this one inserts only `true`, so `get_or(false, 0)` and `has(false)`
	// are genuine MISSES — the read side of the same bug, which a row whose
	// every lookup is a hit cannot distinguish from pointer-identity luck.
	{"boolean-literal-absent-key", `import "core/map";
function main(): i32 {
    var m = Map { true: 5 };
    var s: i32 = m.get_or(true, 0) + m.get_or(false, 0);
    if (!m.has(false)) { s = s + 100; }
    return s + m.len();
}
`, 106},
	// The annotated spellings, which took the integer column already — here
	// so a fix to the literal path cannot regress them unnoticed.
	{"boolean-annotated", `import "core/map";
function main(): i32 {
    var m: Map[boolean, i32] = map_new(2);
    m = m.insert(true, 5);
    m = m.insert(false, 9);
    var s: i32 = m.get_or(true, 0) + m.get_or(false, 0);
    if (m.has(true)) { s = s + 1; }
    return s + m.len();
}
`, 17},
	{"boolean-annotated-literal", `import "core/map";
function main(): i32 {
    var m: Map[boolean, i32] = Map { true: 5, false: 9 };
    return m.get_or(false, 0) + m.get_or(true, 0) + m.len();
}
`, 16},
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

// monomorphisedFloatKeySrc has no float key written anywhere: `build(2.5, 7)`
// instantiates a `Map[T, i32]`-returning generic at T = f64.
const monomorphisedFloatKeySrc = `import "core/map";
function build[T](k: T, v: i32): Map[T, i32] {
    var m: Map[T, i32] = map_new(2);
    return m.insert(k, v);
}
function main(): i32 { return build(2.5, 7).get_or(2.5, 0); }
`

// TestSelfHostMonomorphisedFloatKeyStillRefusesToLower pins the float half of
// map_key_has_no_column, which nothing else does now that E045 refuses every
// written float-key spelling (#10009).
//
// This program writes none. Both checkers re-check its instantiations and
// report E045 on the monomorphised copy (#10018,
// TestSelfHostInstantiationRecheckDifferentialX86_64), but `-decide` lowers
// without the checker, as does any driver that skips it. map_key_kind_of would
// then answer 0 for the f64 key — the STRING column, which reads a key's VALUE
// as an address: the #9973 segfault by another road. The lowering refusal is
// what stands in the way there, and this test pins it.
//
// It takes no interpreter oracle. The other cases have one to prove they are
// well-formed programs refused for their key; this one native refuses
// outright, and that refusal is half of what is asserted.
func TestSelfHostMonomorphisedFloatKeyStillRefusesToLower(t *testing.T) {
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
	entry := filepath.Join(dir, "monomorphised_float_key.fern")
	if err := os.WriteFile(entry, []byte(monomorphisedFloatKeySrc), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}

	// Half one: native catches it. The whole pipeline is what is run, not
	// checker.Check — the diagnostic arrives with MONOMORPHISATION.
	if _, code := runFixtureInterp(t, entry, ""); code == 0 {
		t.Errorf("native ran a program with a monomorphised f64 map key; it reported E045 when this " +
			"test was written, so either the instantiation re-check regressed or the rule moved")
	}

	// Half two: the self-host lowering refuses it with no checker in front.
	route, _ := exec.Command(driver, entry, root, "-decide").Output()
	if got := strings.TrimSpace(string(route)); got != "refused" {
		t.Errorf("-decide = %q, want \"refused\": lowering this f64 key without the checker "+
			"puts the key's VALUE through the string column as an address", got)
	}
}
