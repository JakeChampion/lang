package langsmith_test

import (
	"encoding/binary"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/langsmith"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// randBytes fills out with n bytes drawn from r. math/rand/v2 dropped
// the v1 *rand.Rand.Read method, so the tests roll their own.
func randBytes(r *rand.Rand, n int) []byte {
	out := make([]byte, n)
	for i := 0; i+8 <= n; i += 8 {
		binary.LittleEndian.PutUint64(out[i:], r.Uint64())
	}
	if tail := n % 8; tail != 0 {
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], r.Uint64())
		copy(out[n-tail:], buf[:tail])
	}
	return out
}

// TestGenProducesValidPrograms is the deterministic counterpart to
// FuzzGenerate_ParseRoundTrips — it walks a fixed range of seeds so
// regressions show up in `go test ./...` without anyone having to
// remember to run the fuzzer. Failure prints the failing source so
// the seed-to-input mapping is reproducible.
func TestGenProducesValidPrograms(t *testing.T) {
	for seed := uint64(0); seed < 256; seed++ {
		src := langsmith.Gen(seed)
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("seed=%d failed to parse:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("seed=%d failed to type-check:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
	}
}

// TestGenIsDeterministic — same seed → same source, every time.
// Without this the corpus for the fuzzer can't be minimised
// reproducibly and a reported crash can't be replayed.
func TestGenIsDeterministic(t *testing.T) {
	for _, seed := range []uint64{0, 1, 42, 1234567} {
		a := langsmith.Gen(seed)
		b := langsmith.Gen(seed)
		if a != b {
			t.Errorf("seed=%d: output diverges across calls\nfirst:\n%s\nsecond:\n%s", seed, a, b)
		}
	}
}

// TestGenEmitsAtLeastOneFunction — the load-bearing claim of v1 is
// "non-empty, well-typed program". Tighten the guarantee here.
func TestGenEmitsAtLeastOneFunction(t *testing.T) {
	for seed := uint64(0); seed < 16; seed++ {
		src := langsmith.Gen(seed)
		if !strings.Contains(src, "function gen_f0(") {
			t.Errorf("seed=%d: missing gen_f0 in output\nsrc:\n%s", seed, src)
		}
	}
}

// TestGenMainProducesRunnablePrograms — every seed yields a
// well-typed program whose `main(): i32` returns a byte. Also
// runs the full driver pipeline (modload → constfold → checker
// → monomorph) for each seed, because the parse + check path
// alone misses bugs that only show up under generic-function
// monomorphisation (an earlier seed produced a program where
// the field name `id` shadowed the `id[T]` generic, and the
// monomorph re-check rejected the cloned program after the
// initial checker said OK).
func TestGenMainProducesRunnablePrograms(t *testing.T) {
	for seed := uint64(0); seed < 256; seed++ {
		src := langsmith.GenMain(seed)
		if !strings.Contains(src, "function main(): i32") {
			t.Errorf("seed=%d: missing main\nsrc:\n%s", seed, src)
			continue
		}
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "main.lang")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("seed=%d write: %v", seed, err)
		}
		prog, _, err := modload.Load(srcPath)
		if err != nil {
			t.Fatalf("seed=%d modload:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
		if err := constfold.Fold(prog); err != nil {
			t.Fatalf("seed=%d constfold:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
		info, err := checker.Check(prog)
		if err != nil {
			t.Fatalf("seed=%d check:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
		if err := monomorph.Run(prog, info); err != nil {
			t.Fatalf("seed=%d monomorph:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
	}
}

// TestGenBytesProducesValidPrograms — byte-stream API mirror of
// TestGenProducesValidPrograms. Uses pseudo-random byte slabs of
// varying lengths so the test exercises both byte-rich (mutator
// drives every decision) and byte-poor (early exhaustion → short
// program) regimes.
func TestGenBytesProducesValidPrograms(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 99))
	for i := 0; i < 64; i++ {
		n := r.IntN(512) // 0..511 bytes; covers exhaustion path too
		data := randBytes(r, n)
		src := langsmith.GenBytes(data)
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("i=%d len=%d failed to parse:\nsrc:\n%s\nerr: %v", i, n, src, err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("i=%d len=%d failed to type-check:\nsrc:\n%s\nerr: %v", i, n, src, err)
		}
	}
}

// TestGenMainBytesProducesRunnablePrograms — byte-stream API
// mirror for GenMain. Same exhaustion-coverage rationale.
func TestGenMainBytesProducesRunnablePrograms(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 13))
	for i := 0; i < 64; i++ {
		n := r.IntN(512)
		data := randBytes(r, n)
		src := langsmith.GenMainBytes(data)
		if !strings.Contains(src, "function main(): i32") {
			t.Errorf("i=%d: missing main\nsrc:\n%s", i, src)
			continue
		}
		prog, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("i=%d failed to parse:\nsrc:\n%s\nerr: %v", i, src, err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("i=%d failed to type-check:\nsrc:\n%s\nerr: %v", i, src, err)
		}
	}
}

// TestGenBytesIsDeterministic — same bytes → same source. The
// minimisation contract relies on this: a smaller corpus must
// produce a smaller-or-same program, not a different one.
func TestGenBytesIsDeterministic(t *testing.T) {
	for _, n := range []int{0, 1, 8, 64, 256} {
		r := rand.New(rand.NewPCG(uint64(n), 31415))
		data := randBytes(r, n)
		a := langsmith.GenBytes(data)
		b := langsmith.GenBytes(data)
		if a != b {
			t.Errorf("len=%d: output diverges across calls\nfirst:\n%s\nsecond:\n%s", n, a, b)
		}
	}
}

// TestGenFeatureCoverage — the load-bearing observation that
// language features added to the generator actually show up in
// practice. Without this, regressions like "the call production
// stopped firing because the flip bias drifted" would only
// surface when someone notices a backend stopped crashing.
// Walks 1024 seeds and asserts each landmark feature appears in
// at least one program.
func TestGenFeatureCoverage(t *testing.T) {
	want := map[string]bool{
		"function call":              false,
		"if-expression":              false,
		"binary arithmetic":          false,
		"variable reference":         false,
		"helper-call inside main":    false,
		"nested call (call as arg)":  false,
		"main with var declarations": false,
		"while loop":                 false,
		"struct decl":                false,
		"enum decl":                  false,
		"array literal":              false,
		"array index":                false,
		"pair literal":               false,
		"field access":               false,
		"enum variant in expr":       false,
		"match expression":           false,
		"string concat":              false,
		"f-string":                   false,
		"len(string)":                false,
		"pipe operator":              false,
		"Some literal":               false,
		"None literal":               false,
		"Option match":               false,
		"try operator (?)":           false,
		"method call (.sum)":         false,
		"method call (.swap)":        false,
		"Xyz struct decl":            false,
		"Xyz literal":                false,
		"Xyz.n field access":         false,
		"Xyz.valid field access":     false,
		"Status enum decl":           false,
		"Status variant":             false,
		"Status match expression":    false,
		"Map literal":                false,
		"map .get() / .has() / .len()": false,
		"dynamic struct decl":        false,
		"dynamic enum decl":          false,
		"id[T] generic call":         false,
		"pick[T] generic call":       false,
		"for-each over array":        false,
		"for-each over map":          false,
		"nested function (closure)":  false,
		"Ok literal":                 false,
		"Err literal":                false,
		"Result match-with-binding":  false,
		"Result try (?)":             false,
	}
	for seed := uint64(0); seed < 1024; seed++ {
		src := langsmith.GenMain(seed)
		// "gen_f0(" appears either in the helper decl OR a call.
		// Both forms are useful evidence; the nested-call check
		// distinguishes a call-from-main shape.
		if strings.Contains(src, "gen_f0(") {
			want["function call"] = true
		}
		if strings.Contains(src, "(if (") {
			want["if-expression"] = true
		}
		if strings.Contains(src, " + ") || strings.Contains(src, " * ") {
			want["binary arithmetic"] = true
		}
		// Reference to a generated var inside an expression — `v0`
		// only ever appears as a token after a `var v0` decl.
		if strings.Contains(src, "v0") && strings.Count(src, "v0") > 1 {
			want["variable reference"] = true
		}
		// Helper call inside main: the helper name appears after
		// `function main`.
		if i := strings.Index(src, "function main"); i >= 0 {
			tail := src[i:]
			if strings.Contains(tail, "gen_f0(") || strings.Contains(tail, "gen_f1(") {
				want["helper-call inside main"] = true
			}
		}
		// Nested call: `gen_f0(... gen_f0(...) ...)` — second `gen_f0(`
		// occurs before the matching close of the first.
		if strings.Count(src, "gen_f0(") >= 2 || strings.Count(src, "gen_f1(") >= 2 {
			want["nested call (call as arg)"] = true
		}
		if i := strings.Index(src, "function main"); i >= 0 && strings.Contains(src[i:], "var v0") {
			want["main with var declarations"] = true
		}
		if strings.Contains(src, "while (__loop_i") {
			want["while loop"] = true
		}
		// Prelude decls — should always be present, but assert
		// they're not silently dropped.
		if strings.Contains(src, "struct Pair { fst: i32, snd: i32 }") {
			want["struct decl"] = true
		}
		if strings.Contains(src, "enum Color { Red, Green, Blue }") {
			want["enum decl"] = true
		}
		// Array-typed var: `: i32[] = [` is a strong signal for
		// "literal array constructed at runtime".
		if strings.Contains(src, "i32[] = [") || strings.Contains(src, "i64[] = [") || strings.Contains(src, "boolean[] = [") {
			want["array literal"] = true
		}
		// Array indexed read: `v0[0i32]` shape — written in
		// tryCompositeProduction.
		if strings.Contains(src, "[0i32]") {
			want["array index"] = true
		}
		// Pair literal written by pairLiteral as `(Pair {`.
		if strings.Contains(src, "(Pair { fst: ") {
			want["pair literal"] = true
		}
		// Field access from tryCompositeProduction: `vN.fst` or
		// `vN.snd`.
		if strings.Contains(src, ".fst") || strings.Contains(src, ".snd") {
			want["field access"] = true
		}
		// An enum variant appears as a bare identifier in an
		// expression position. The prelude line contains them
		// but with `{` / `,` / `}` punctuation. A use site has
		// the variant followed by either `,` (inside match arm)
		// or a token like `;` / `)` / `}` / space. Just check
		// for one occurrence outside the prelude line — count
		// total occurrences > 1 in the source.
		if strings.Count(src, "Red") > 1 || strings.Count(src, "Green") > 1 || strings.Count(src, "Blue") > 1 {
			want["enum variant in expr"] = true
		}
		if strings.Contains(src, "match (") {
			want["match expression"] = true
		}
		// String concat produces a `(<string-expr> + <string-expr>)`
		// shape. Tokens like `" + ` (string-quote then plus) only
		// appear in the concat / f-string productions.
		if strings.Contains(src, "\" + \"") || strings.Contains(src, "\" + ") || strings.Contains(src, " + \"") {
			want["string concat"] = true
		}
		if strings.Contains(src, `f"`) {
			want["f-string"] = true
		}
		if strings.Contains(src, "len(") {
			want["len(string)"] = true
		}
		if strings.Contains(src, "|>") {
			want["pipe operator"] = true
		}
		// Some appears in literals + match arms. Match arms are
		// `Some(__opt_x...)`, so check for the literal-call form
		// `Some(` separately. Subtract 0 occurrences in Color decl.
		if strings.Contains(src, "(Some(") {
			want["Some literal"] = true
		}
		// None as a bare token. Appears in literal positions plus
		// match arms. Don't conflate with `Done` / `Stone` / etc.
		// — full word match is enough since the generator never
		// emits names ending in "None".
		for _, tok := range []string{" None;", " None,", " None ", "{None}", "= None;"} {
			if strings.Contains(src, tok) {
				want["None literal"] = true
				break
			}
		}
		if strings.Contains(src, "match (") && strings.Contains(src, "Some(__opt_x") {
			want["Option match"] = true
		}
		// `?)` is the generator's try-operator shape (always
		// emitted with the outer paren).
		if strings.Contains(src, "?)") {
			want["try operator (?)"] = true
		}
		if strings.Contains(src, ".sum()") {
			want["method call (.sum)"] = true
		}
		if strings.Contains(src, ".swap()") {
			want["method call (.swap)"] = true
		}
		if strings.Contains(src, "struct Xyz { n: i32, valid: boolean }") {
			want["Xyz struct decl"] = true
		}
		if strings.Contains(src, "(Xyz { n: ") {
			want["Xyz literal"] = true
		}
		// `.n ` (with trailing space / token boundary) catches
		// `.n` as a field access but not `.snd` / `.swap()` etc.
		if strings.Contains(src, ".n ") || strings.Contains(src, ".n)") || strings.Contains(src, ".n,") || strings.Contains(src, ".n;") {
			want["Xyz.n field access"] = true
		}
		if strings.Contains(src, ".valid") {
			want["Xyz.valid field access"] = true
		}
		if strings.Contains(src, "enum Status { Active, Inactive, Pending }") {
			want["Status enum decl"] = true
		}
		// Status variant appears as an Ident; check for any of the
		// three names outside the decl line. Total > 1 catches use sites.
		if strings.Count(src, "Active") > 1 || strings.Count(src, "Inactive") > 1 || strings.Count(src, "Pending") > 1 {
			want["Status variant"] = true
		}
		if strings.Contains(src, "Active =>") {
			want["Status match expression"] = true
		}
		if strings.Contains(src, "(Map {") || strings.Contains(src, "Map[i32, i32]") {
			want["Map literal"] = true
		}
		if strings.Contains(src, ".get(") || strings.Contains(src, ".has(") || strings.Contains(src, ".len()") {
			want["map .get() / .has() / .len()"] = true
		}
		// Dynamic structs use `struct S<N>` and dynamic enums
		// `enum E<N>` with `__E<N>_V<M>` variants.
		if strings.Contains(src, "struct S0 ") || strings.Contains(src, "struct S1 ") {
			want["dynamic struct decl"] = true
		}
		if strings.Contains(src, "enum E0 ") || strings.Contains(src, "enum E1 ") {
			want["dynamic enum decl"] = true
		}
		// id(...) appears after the prelude decl `function id[T]
		// (x: T): T { return x; }`. Total occurrences > 1 means
		// at least one CALL site (not just the decl).
		if strings.Count(src, "id(") > 1 {
			want["id[T] generic call"] = true
		}
		if strings.Count(src, "pick(") > 1 {
			want["pick[T] generic call"] = true
		}
		// for-each over arrays: `for __fe_x<N> in <arr-var>`.
		// for-each over maps: `for (__fe_k<N>, __fe_v<N>) in <map>`.
		if strings.Contains(src, "for __fe_x") {
			want["for-each over array"] = true
		}
		if strings.Contains(src, "for (__fe_k") {
			want["for-each over map"] = true
		}
		// Nested function: `function __local_fn<N>(...)`.
		if strings.Contains(src, "function __local_fn") {
			want["nested function (closure)"] = true
		}
		// Result literals.
		if strings.Contains(src, "(Ok(") {
			want["Ok literal"] = true
		}
		if strings.Contains(src, "(Err(") {
			want["Err literal"] = true
		}
		// Result match-with-binding emits `Ok(__res_ok<N>)`.
		if strings.Contains(src, "Ok(__res_ok") {
			want["Result match-with-binding"] = true
		}
		// Result try operator: generator emits `(<var>?)` where
		// the var is a Result-typed scope var. We have the
		// Option try gate emitting `?)` too, so distinguish by
		// also checking for `Result[i32, i32]` somewhere in the
		// program (a stronger signal that Result paths fired).
		if strings.Contains(src, "?)") && strings.Contains(src, "Result[i32, i32]") {
			want["Result try (?)"] = true
		}
	}
	for feature, ok := range want {
		if !ok {
			t.Errorf("feature never seen in 1024 GenMain seeds: %s", feature)
		}
	}
}

// TestGenBytesExhaustionShrinksProgram — chopping bytes off the
// end of a corpus shouldn't make the emitted program *longer*.
// This is the load-bearing minimisation property: the fuzzer's
// shrinker truncates and shuffles bytes, and a working
// generator turns shorter input into shorter (or equal) source.
func TestGenBytesExhaustionShrinksProgram(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	full := randBytes(r, 256)
	long := langsmith.GenBytes(full)
	short := langsmith.GenBytes(full[:8]) // one decision's worth
	empty := langsmith.GenBytes(nil)
	if len(short) > len(long) {
		t.Errorf("short corpus produced longer program: %d > %d", len(short), len(long))
	}
	if len(empty) > len(short) {
		t.Errorf("empty corpus produced longer program: %d > %d", len(empty), len(short))
	}
}
