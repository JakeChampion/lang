package fernsmith_test

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/fernsmith"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// sweepN returns the number of seeds / iterations a generator
// sweep should run. Drops to 1/8th the full size under
// `testing.Short()` so dev-loop `go test ./internal/fernsmith`
// returns in ~3s instead of ~25s. Coverage-landmark sweeps
// (TestGenFeatureCoverage) should stay at their full size —
// dropping seeds risks missing a rarely-fired landmark and
// turning the test flaky.
func sweepN(t *testing.T, full uint64) uint64 {
	t.Helper()
	if testing.Short() {
		short := full / 8
		if short < 4 {
			short = 4
		}
		return short
	}
	return full
}

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

// checkGenerated loads generated source through modload (so the
// `import "std/…";` preamble fernsmith now emits resolves) and
// type-checks it. Post-prelude-removal a bare parser.Parse no longer
// pulls stdlib into scope, so generated programs must go through the
// real module loader the same way the CLI does.
func checkGenerated(t *testing.T, src string) error {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(p)
	if err != nil {
		return err
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return err
	}
	_, err = checker.Check(prog)
	return err
}

// TestGenProducesValidPrograms is the deterministic counterpart to
// FuzzGenerate_ParseRoundTrips — it walks a fixed range of seeds so
// regressions show up in `go test ./...` without anyone having to
// remember to run the fuzzer. Failure prints the failing source so
// the seed-to-input mapping is reproducible.
func TestGenProducesValidPrograms(t *testing.T) {
	n := sweepN(t, 256)
	for seed := uint64(0); seed < n; seed++ {
		src := fernsmith.Gen(seed)
		if err := checkGenerated(t, src); err != nil {
			t.Fatalf("seed=%d failed to type-check:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
	}
}

// TestGenIsDeterministic — same seed → same source, every time.
// Without this the corpus for the fuzzer can't be minimised
// reproducibly and a reported crash can't be replayed.
func TestGenIsDeterministic(t *testing.T) {
	for _, seed := range []uint64{0, 1, 42, 1234567} {
		a := fernsmith.Gen(seed)
		b := fernsmith.Gen(seed)
		if a != b {
			t.Errorf("seed=%d: output diverges across calls\nfirst:\n%s\nsecond:\n%s", seed, a, b)
		}
	}
}

// TestGenEmitsAtLeastOneFunction — the load-bearing claim of v1 is
// "non-empty, well-typed program". Tighten the guarantee here.
func TestGenEmitsAtLeastOneFunction(t *testing.T) {
	for seed := uint64(0); seed < 16; seed++ {
		src := fernsmith.Gen(seed)
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
//
// 256 seeds, not the differential oracle's 2048, and the gap is
// deliberate: this walk costs ~29s here and ~3m50s at 2048, which does
// not earn its place in the unit lane when the differential lane
// already type-checks all 2048 as a precondition to running them. A
// generator bug rare enough to need more than 256 seeds surfaces
// there, not here — the lambda-body `?` bug did exactly that, on seeds
// 680 / 1117 / 1759 with this test green. Widen it only if the
// differential lane stops being the broader net.
func TestGenMainProducesRunnablePrograms(t *testing.T) {
	n := sweepN(t, 256)
	for seed := uint64(0); seed < n; seed++ {
		src := fernsmith.GenMain(seed)
		if !strings.Contains(src, "function main(): i32") {
			t.Errorf("seed=%d: missing main\nsrc:\n%s", seed, src)
			continue
		}
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "main.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("seed=%d write: %v", seed, err)
		}
		prog, _, err := modload.Load(srcPath)
		if err != nil {
			t.Fatalf("seed=%d modload:\nsrc:\n%s\nerr: %v", seed, src, err)
		}
		if err := constfold.Fold(prog, nil); err != nil {
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
	n := sweepN(t, 64)
	for i := 0; uint64(i) < n; i++ {
		n := r.IntN(512) // 0..511 bytes; covers exhaustion path too
		data := randBytes(r, n)
		src := fernsmith.GenBytes(data)
		if err := checkGenerated(t, src); err != nil {
			t.Fatalf("i=%d len=%d failed to type-check:\nsrc:\n%s\nerr: %v", i, n, src, err)
		}
	}
}

// TestGenMainBytesProducesRunnablePrograms — byte-stream API
// mirror for GenMain. Same exhaustion-coverage rationale.
func TestGenMainBytesProducesRunnablePrograms(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 13))
	iters := sweepN(t, 64)
	for i := 0; uint64(i) < iters; i++ {
		n := r.IntN(512)
		data := randBytes(r, n)
		src := fernsmith.GenMainBytes(data)
		if !strings.Contains(src, "function main(): i32") {
			t.Errorf("i=%d: missing main\nsrc:\n%s", i, src)
			continue
		}
		if err := checkGenerated(t, src); err != nil {
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
		a := fernsmith.GenBytes(data)
		b := fernsmith.GenBytes(data)
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
// u32LiteralRE / u64LiteralRE match a suffixed unsigned literal.
var (
	u32LiteralRE = regexp.MustCompile(`\b(\d+)u32\b`)
	u64LiteralRE = regexp.MustCompile(`\b(\d+)u64\b`)
	i64LiteralRE = regexp.MustCompile(`\b(\d+)i64\b`)
	u8LiteralRE  = regexp.MustCompile(`\b(\d+)u8\b`)
)

// unsignedOperandNear reports whether op appears with a literal of the given
// unsigned suffix directly on one side of it. Substring-matching the operator
// alone would credit the signed corpus, since `/` and `<` are generated at i32
// and i64 too; anchoring on a `<digits>u32` / `<digits>u64` token is a cheap
// way to be sure the UNSIGNED opcode is the one being reached, at the width
// being claimed.
func unsignedOperandNear(src, suffix, op string, lit *regexp.Regexp) bool {
	for i := 0; i+len(op) <= len(src); i++ {
		if src[i:i+len(op)] != op {
			continue
		}
		// Skip the parentheses the generator wraps sub-expressions in, so
		// `(460i64) +? (...)` counts the same as a bare `460i64 +? ...`.
		// Without this the checked-fold production, whose operands are
		// always parenthesised, reads as having no wide operand at all.
		left := strings.TrimRight(src[:i], "( )")
		if strings.HasSuffix(left, suffix) {
			return true
		}
		right := strings.TrimLeft(src[i+len(op):], "( ")
		if m := lit.FindStringIndex(right); m != nil && m[0] == 0 {
			return true
		}
	}
	return false
}

func u32OperandNear(src, op string) bool {
	return unsignedOperandNear(src, "u32", op, u32LiteralRE)
}

func u64OperandNear(src, op string) bool {
	return unsignedOperandNear(src, "u64", op, u64LiteralRE)
}

// wideOperandNear reports whether op appears with a 64-bit literal — i64 or
// u64 — on either side.
func wideOperandNear(src, op string) bool {
	return unsignedOperandNear(src, "i64", op, i64LiteralRE) ||
		unsignedOperandNear(src, "u64", op, u64LiteralRE)
}

// u32AsI32RE matches emitU32AsI32's cast with a u32 token inside it, so a
// plain `(<i64 expr> >> 32i64) as i32` from emitI64HighHalf doesn't count.
var u32AsI32RE = regexp.MustCompile(`u32[^()]*\) as i32\)`)

func TestGenFeatureCoverage(t *testing.T) {
	want := map[string]bool{
		"function call":                false,
		"if-expression":                false,
		"binary arithmetic":            false,
		"variable reference":           false,
		"helper-call inside main":      false,
		"nested call (call as arg)":    false,
		"main with var declarations":   false,
		"while loop":                   false,
		"struct decl":                  false,
		"enum decl":                    false,
		"array literal":                false,
		"array index":                  false,
		"pair literal":                 false,
		"field access":                 false,
		"enum variant in expr":         false,
		"match expression":             false,
		"string concat":                false,
		"f-string":                     false,
		"len(string)":                  false,
		"pipe operator":                false,
		"Some literal":                 false,
		"None literal":                 false,
		"Option match":                 false,
		"try operator (?)":             false,
		"method call (.sum)":           false,
		"method call (.swap)":          false,
		"Xyz struct decl":              false,
		"Xyz literal":                  false,
		"Xyz.n field access":           false,
		"Xyz.valid field access":       false,
		"Status enum decl":             false,
		"Status variant":               false,
		"Status match expression":      false,
		"Map literal":                  false,
		"map .get() / .has() / .len()": false,
		"dynamic struct decl":          false,
		"dynamic enum decl":            false,
		"id[T] generic call":           false,
		"pick[T] generic call":         false,
		"for-each over array":          false,
		"for-each over map":            false,
		"nested function (closure)":    false,
		"Ok literal":                   false,
		"Err literal":                  false,
		"Result match-with-binding":    false,
		"Result try (?)":               false,
		"u32 var declaration":          false,
		"u32 literal above i32::MAX":   false,
		"unsigned comparison":          false,
		"unsigned divide or remainder": false,
		"unsigned shift":               false,
		"u32 reinterpreted as i32":     false,
		"u64 var declaration":          false,
		"u64 literal above i64::MAX":   false,
		"unsigned 64-bit comparison":   false,
		"unsigned 64-bit divide/rem":   false,
		"unsigned 64-bit shift":        false,
		"u64 high half as i32":         false,
		"saturating add or subtract":   false,
		"saturating multiply":          false,
		"saturating shift":             false,
		"saturating at i64 or u64":     false,
		"checked add or subtract":      false,
		"checked multiply":             false,
		"checked divide or remainder":  false,
		"checked shift":                false,
		"checked at i64 or u64":        false,
		"checked None arm taken":       false,
		"u8 var declaration":           false,
		"u8 literal at or above 200":   false,
		"u8 arithmetic":                false,
		"u8 saturating or checked":     false,
		"lambda literal":               false,
		"fn-typed var":                 false,
		"closure array literal":        false,
		"closure escapes via return":   false,
		"fn-typed parameter":           false,
		"indirect call via fn value":   false,
	}
	for seed := uint64(0); seed < 1024; seed++ {
		src := fernsmith.GenMain(seed)
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
		// Function VALUES. Each of these is a distinct lowering, and
		// the escaping / array / indirect-call trio is the #5001 /
		// #5007 / #5009 / #5026 bug cluster that until now only
		// hand-written probes could reach.
		if strings.Contains(src, "((__lam_x") {
			want["lambda literal"] = true
		}
		if strings.Contains(src, ": (i32) => i32 =") {
			want["fn-typed var"] = true
		}
		if strings.Contains(src, "((i32) => i32)[] = [") {
			want["closure array literal"] = true
		}
		// An escaping closure: a generated helper whose RETURN type is
		// a function, so the value outlives the frame that built it.
		if strings.Contains(src, "): (i32) => i32 {") {
			want["closure escapes via return"] = true
		}
		// A closure crossing a call boundary as an argument.
		if strings.Contains(src, "p0: (i32) => i32") || strings.Contains(src, ", p1: (i32) => i32") {
			want["fn-typed parameter"] = true
		}
		// Indirect call through a function value. `__lam_x` params and
		// `__fe_x` bindings are the two names a fn value can carry
		// besides `v<N>` / `p<N>` / `w<N>`; a call through any of them
		// is a `blr` / `call r11`, never a direct symbol.
		if fnCallRE.MatchString(src) {
			want["indirect call via fn value"] = true
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
		// u32 — the corpus's only unsigned type. Reaching the unsigned
		// opcodes is not enough on its own: div_u / rem_u / shr_u and
		// the unsigned condition codes only diverge from their signed
		// siblings on an operand with bit 31 set, so the above-i32::MAX
		// literal is tracked as its own feature.
		if strings.Contains(src, ": u32 ") {
			want["u32 var declaration"] = true
		}
		for _, m := range u32LiteralRE.FindAllStringSubmatch(src, -1) {
			if n, err := strconv.ParseUint(m[1], 10, 64); err == nil && n > math.MaxInt32 {
				want["u32 literal above i32::MAX"] = true
				break
			}
		}
		for _, op := range []string{" < ", " > ", " <= ", " >= "} {
			if u32OperandNear(src, op) {
				want["unsigned comparison"] = true
			}
		}
		for _, op := range []string{" / ", " % "} {
			if u32OperandNear(src, op) {
				want["unsigned divide or remainder"] = true
			}
		}
		if u32OperandNear(src, " >> ") || u32OperandNear(src, " << ") {
			want["unsigned shift"] = true
		}
		// emitU32AsI32's `((<u32 expr>) as i32)` — the channel that
		// carries a full 32-bit unsigned result into the exit byte.
		if u32AsI32RE.MatchString(src) {
			want["u32 reinterpreted as i32"] = true
		}
		// u64 — the same pairs one width up. Tracked separately from
		// u32 because the wide unsigned opcodes are different
		// instructions with a different (6-bit) count mask, so u32
		// coverage says nothing about them.
		if strings.Contains(src, ": u64 ") {
			want["u64 var declaration"] = true
		}
		for _, m := range u64LiteralRE.FindAllStringSubmatch(src, -1) {
			if n, err := strconv.ParseUint(m[1], 10, 64); err == nil && n > math.MaxInt64 {
				want["u64 literal above i64::MAX"] = true
				break
			}
		}
		for _, op := range []string{" < ", " > ", " <= ", " >= "} {
			if u64OperandNear(src, op) {
				want["unsigned 64-bit comparison"] = true
			}
		}
		for _, op := range []string{" / ", " % "} {
			if u64OperandNear(src, op) {
				want["unsigned 64-bit divide/rem"] = true
			}
		}
		if u64OperandNear(src, " >> ") || u64OperandNear(src, " << ") {
			want["unsigned 64-bit shift"] = true
		}
		if strings.Contains(src, ") >> 32u64) as i32)") {
			want["u64 high half as i32"] = true
		}
		// Saturating arithmetic. Tracked per operator because the four
		// expand to materially different clamps: the +|/-| pre-checks
		// against MAX-b / MIN-b, *| and <<| post-check the wrapped
		// result with a division instead.
		if strings.Contains(src, " +| ") || strings.Contains(src, " -| ") {
			want["saturating add or subtract"] = true
		}
		if strings.Contains(src, " *| ") {
			want["saturating multiply"] = true
		}
		if strings.Contains(src, " <<| ") {
			want["saturating shift"] = true
		}
		// The clamp bounds are per-width, so a saturating op reached
		// only at i32 leaves the 64-bit bounds untested.
		for _, op := range []string{" +| ", " -| ", " *| ", " <<| "} {
			if wideOperandNear(src, op) {
				want["saturating at i64 or u64"] = true
			}
		}
		// Checked arithmetic, folded back to a bare integer through a
		// match. Tracked per operator group because the desugars differ:
		// a carry / sign-pattern test for +? and -?, a division
		// round-trip for *?, divisor and MIN/-1 guards for /? and %?,
		// a count-range test for the shifts.
		if strings.Contains(src, " +? ") || strings.Contains(src, " -? ") {
			want["checked add or subtract"] = true
		}
		if strings.Contains(src, " *? ") {
			want["checked multiply"] = true
		}
		if strings.Contains(src, " /? ") || strings.Contains(src, " %? ") {
			want["checked divide or remainder"] = true
		}
		if strings.Contains(src, " <<? ") || strings.Contains(src, " >>? ") {
			want["checked shift"] = true
		}
		for _, op := range []string{" +? ", " -? ", " *? ", " /? ", " %? ", " <<? ", " >>? "} {
			if wideOperandNear(src, op) {
				want["checked at i64 or u64"] = true
			}
		}
		// The None arm is the half that only runs on an actual
		// overflow; its presence in the source is what makes the
		// operator observable rather than a Some passthrough.
		if strings.Contains(src, "None => ") && strings.Contains(src, "__chk_v") {
			want["checked None arm taken"] = true
		}
		// u8 — the only sub-word integer. Its wrap mask is 8 bits and its
		// saturating clamp is 0/255, so reaching it at all is a different
		// surface from the 32- and 64-bit types; the near-255 literal is
		// tracked separately because only those cross either boundary.
		if strings.Contains(src, ": u8 ") {
			want["u8 var declaration"] = true
		}
		for _, m := range u8LiteralRE.FindAllStringSubmatch(src, -1) {
			if n, err := strconv.ParseUint(m[1], 10, 64); err == nil && n >= 200 {
				want["u8 literal at or above 200"] = true
				break
			}
		}
		for _, op := range []string{" + ", " - ", " * ", " / ", " % ", " << ", " >> "} {
			if unsignedOperandNear(src, "u8", op, u8LiteralRE) {
				want["u8 arithmetic"] = true
			}
		}
		for _, op := range []string{" +| ", " -| ", " *| ", " <<| ", " +? ", " -? ", " *? ", " /? ", " %? ", " <<? ", " >>? "} {
			if unsignedOperandNear(src, "u8", op, u8LiteralRE) {
				want["u8 saturating or checked"] = true
			}
		}
	}
	for feature, ok := range want {
		if !ok {
			t.Errorf("feature never seen in 1024 GenMain seeds: %s", feature)
		}
	}
}

// TestGenPrintableFloatCoverage is TestGenFeatureCoverage's counterpart for
// the float-allowing profile. Floats are dropped from ProfileRunnable — the
// exit-byte oracle observes one byte and cannot see a float — so GenMain seeds
// never contain an f32 or f64 at all, and the features below are invisible to
// that sweep no matter how many seeds it runs.
//
// f64 is tracked apart from f32 because width is the whole point: f64 is the
// width at which a float needs no rounding step, so an f32 path that rounds
// correctly says nothing about it.
func TestGenPrintableFloatCoverage(t *testing.T) {
	want := map[string]bool{
		"f32 literal":                   false,
		"f64 literal":                   false,
		"f64 var or param":              false,
		"f64 arithmetic":                false,
		"f64 truncated to i32":          false,
		"f64 literal past f32 mantissa": false,
	}
	for seed := uint64(0); seed < 1024; seed++ {
		src := fernsmith.GenPrintableMain(seed)
		if strings.Contains(src, "f32") {
			want["f32 literal"] = true
		}
		if f64LiteralRE.MatchString(src) {
			want["f64 literal"] = true
		}
		if strings.Contains(src, ": f64") {
			want["f64 var or param"] = true
		}
		for _, op := range []string{" + ", " - ", " * ", " / "} {
			if unsignedOperandNear(src, "f64", op, f64LiteralRE) {
				want["f64 arithmetic"] = true
			}
		}
		if strings.Contains(src, "f64) as i32)") || f64CastRE.MatchString(src) {
			want["f64 truncated to i32"] = true
		}
		// An f32 carries ~7 significant digits; a literal needing more
		// is one only f64 can hold, which is what catches a backend
		// that narrowed an f64 on the way through.
		for _, m := range f64LiteralRE.FindAllStringSubmatch(src, -1) {
			if len(strings.ReplaceAll(m[1], ".", "")) > 8 {
				want["f64 literal past f32 mantissa"] = true
				break
			}
		}
	}
	for feature, ok := range want {
		if !ok {
			t.Errorf("feature never seen in 1024 GenPrintableMain seeds: %s", feature)
		}
	}
}

var (
	f64LiteralRE = regexp.MustCompile(`\b(\d+\.\d+)f64\b`)
	f64CastRE    = regexp.MustCompile(`f64[^()]*\) as i32\)`)
	// A call whose callee is a VARIABLE rather than a declared
	// function name. Every generated declaration is `gen_f<N>` /
	// `__local_fn<N>` / `id` / `pick`, so a `(` directly after any
	// value-carrying name can only be an indirect call through a
	// function value. Indexing is `v0[...]` and field access is
	// `v0.fst`, neither of which this matches.
	fnCallRE = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(?:v\d+|p\d+|lp\d+|w\d+_\d+|__fe_x\d+|__lam_x\d+)\(`)
)

// ---- float-in-aggregate coverage (#7348) -----------------------------

// reFloatAggDecl matches the aggregate the printable profile's mandatory
// float-aggregate observation declares, capturing its name and its type.
var reFloatAggDecl = regexp.MustCompile(`var (__fag\d+): (.+?) = `)

// floatAggObservation reports which kind of float-bearing aggregate the
// mandatory observation built in this program, and whether EVERY slot of
// it is printed. The second half is the part that matters: a float
// element's width is only observable next to a neighbour, so an
// aggregate whose later slots are never read is a shape in the corpus
// that the oracle still cannot see.
func floatAggObservation(mainBody string) (kind string, allSlotsPrinted bool) {
	m := reFloatAggDecl.FindStringSubmatch(mainBody)
	if m == nil {
		return "", false
	}
	name, kind := m[1], strings.TrimSpace(m[2])
	var want []string
	switch kind {
	case "f32[]", "f64[]":
		for i := 0; i < 3; i++ {
			want = append(want, fmt.Sprintf("print(((%s[%di32]) as i32)", name, i))
		}
	case "(f32, f64)":
		want = []string{
			fmt.Sprintf("print(((%s.0) as i32)", name),
			fmt.Sprintf("print(((%s.1) as i32)", name),
		}
	case "Vec2":
		want = []string{
			fmt.Sprintf("print(((%s.x) as i32)", name),
			fmt.Sprintf("print(((%s.y) as i32)", name),
		}
	case "FShape":
		// One match per payload slot, each reading a DIFFERENT binding:
		// slot 0 through the first, slot 1 through the second. Other
		// observations may match on the same var too, so the check is
		// for these two exact reads rather than a count.
		idx := strings.TrimPrefix(name, "__fag")
		for slot, narrow := range []string{"a", "b"} {
			wide := []string{"c", "d"}[slot]
			want = append(want, fmt.Sprintf(
				"print((match (%s) { FNone => 0i32, FTwo(__fs_a%s_%d, __fs_b%s_%d) => ((__fs_%s%s_%d) as i32), FWide(__fs_c%s_%d, __fs_d%s_%d) => ((__fs_%s%s_%d) as i32) }).to_string());",
				name, idx, slot, idx, slot, narrow, idx, slot, idx, slot, idx, slot, wide, idx, slot))
		}
	default:
		return kind, false
	}
	for _, w := range want {
		if !strings.Contains(mainBody, w) {
			return kind, false
		}
	}
	return kind, true
}

func mainBodyOf(src string) string {
	if i := strings.Index(src, "function main"); i >= 0 {
		return src[i:]
	}
	return ""
}

var (
	// A dynamic struct decl with at least one float field.
	reDynStructFloatField = regexp.MustCompile(`struct S\d+ \{[^}]*: f(?:32|64)`)
	// A dynamic enum variant declared with a float payload slot.
	reDynEnumFloatPayload = regexp.MustCompile(`__E\d+_V\d+\([^)]*f(?:32|64)`)
	// An ordinary main-level local, as drawn by pickMainVarType.
	reMainLocalDecl = regexp.MustCompile(`var v\d+: (.+?) = `)
)

// TestGenPrintableFloatsReachAggregates is the non-vacuity gate on the
// float-in-aggregate widening, and it is stated as FREQUENCY floors
// rather than "seen at least once" on purpose.
//
// The class this covers — a float sharing a box with a neighbour, where
// an access too wide for the element reaches the slot after it — was
// unreachable for the whole life of the generator, so #7333 had to be
// found by hand. A widening that merely CAN produce the shape is worse
// than none: it reads as coverage while a distribution that emits one
// in five hundred seeds leaves the class as unreached as it was. So each
// landmark carries the share of seeds it must reach, generously below
// what the generator actually produces (measured at the floors' comment)
// but far enough above zero that a regression to "technically possible"
// fails here.
func TestGenPrintableFloatsReachAggregates(t *testing.T) {
	const seeds = 512
	// Measured over 1024 seeds when written: each mandatory-observation
	// kind ~19-21%, each ordinary main-local kind ~7-9%, float struct
	// field 64%, float enum payload 41%.
	landmarks := []struct {
		name  string
		floor float64
		hit   func(src, body string) bool
	}{
		{"f32[] built and every element printed", 0.10, func(_, body string) bool {
			k, ok := floatAggObservation(body)
			return k == "f32[]" && ok
		}},
		{"f64[] built and every element printed", 0.10, func(_, body string) bool {
			k, ok := floatAggObservation(body)
			return k == "f64[]" && ok
		}},
		{"(f32, f64) built and both elements printed", 0.10, func(_, body string) bool {
			k, ok := floatAggObservation(body)
			return k == "(f32, f64)" && ok
		}},
		{"Vec2 built and both f32 fields printed", 0.10, func(_, body string) bool {
			k, ok := floatAggObservation(body)
			return k == "Vec2" && ok
		}},
		{"FShape multi-slot payload built and both slots printed", 0.10, func(_, body string) bool {
			k, ok := floatAggObservation(body)
			return k == "FShape" && ok
		}},
		{"f32[] as an ordinary main-level local", 0.03, mainLocalIs("f32[]")},
		{"f64[] as an ordinary main-level local", 0.03, mainLocalIs("f64[]")},
		{"(f32, f64) as an ordinary main-level local", 0.03, mainLocalIs("(f32, f64)")},
		{"Vec2 as an ordinary main-level local", 0.03, mainLocalIs("Vec2")},
		{"FShape as an ordinary main-level local", 0.03, mainLocalIs("FShape")},
		{"f32 as an ordinary main-level local", 0.03, mainLocalIs("f32")},
		{"f64 as an ordinary main-level local", 0.03, mainLocalIs("f64")},
		{"dynamic struct with a float field", 0.25, func(src, _ string) bool {
			return reDynStructFloatField.MatchString(src)
		}},
		{"dynamic enum variant with a float payload slot", 0.15, func(src, _ string) bool {
			return reDynEnumFloatPayload.MatchString(src)
		}},
		// Every seed: the observation is mandatory, so a seed that does
		// not observe an aggregate-carried float is a wiring bug, not a
		// sampling shortfall.
		{"every slot of one float aggregate observed", 1.0, func(_, body string) bool {
			_, ok := floatAggObservation(body)
			return ok
		}},
	}
	hits := make([]int, len(landmarks))
	for seed := uint64(0); seed < seeds; seed++ {
		src := fernsmith.GenPrintableMain(seed)
		body := mainBodyOf(src)
		for i, l := range landmarks {
			if l.hit(src, body) {
				hits[i]++
			}
		}
	}
	for i, l := range landmarks {
		got := float64(hits[i]) / float64(seeds)
		if got < l.floor {
			t.Errorf("%s: %d/%d seeds (%.1f%%), want at least %.1f%% — the class is reachable in theory and unreached in practice",
				l.name, hits[i], seeds, 100*got, 100*l.floor)
		}
	}
}

func mainLocalIs(typeName string) func(src, body string) bool {
	return func(_, body string) bool {
		for _, m := range reMainLocalDecl.FindAllStringSubmatch(body, -1) {
			if strings.TrimSpace(m[1]) == typeName {
				return true
			}
		}
		return false
	}
}

// TestGenMainStaysFloatFree pins the other half of the widening: the
// return-byte oracle compares main()'s exit code bit-for-bit across
// backends, and Fern deliberately under-specifies IEEE edge cases
// (docs/FLOAT-SEMANTICS.md), so ProfileRunnable admits no float at all.
// Widening the aggregate element types is exactly the kind of change
// that leaks one in through a type list that is not profile-gated —
// a dynamic struct field, an array element, an enum payload slot — and
// the leak would show up as a non-portable exit byte reported as a
// codegen bug.
func TestGenMainStaysFloatFree(t *testing.T) {
	banned := regexp.MustCompile(`\b(?:f32|f64|Vec2|FShape)\b`)
	for seed := uint64(0); seed < sweepN(t, 512); seed++ {
		src := fernsmith.GenMain(seed)
		if m := banned.FindString(src); m != "" {
			t.Fatalf("seed=%d: ProfileRunnable program mentions %q — floats must not reach the return-byte oracle\nsrc:\n%s", seed, m, src)
		}
	}
}

// TestGenBytesShrinkIsMonotonicAndValid is the minimisation contract
// stated as a property rather than sampled at three points, and it is the
// gate on growing this generator (#6073).
//
// The contract (fernsmith.go's doc, and what makes the fuzzer's shrinker
// work) is that chopping bytes off a corpus collapses the program
// smoothly. This replaces TestGenBytesExhaustionShrinksProgram, which
// sampled 256 / 8 / 0 bytes on one seed and compared source LENGTH — a
// quantity the generator does not promise, see below. Two things that
// sampling could not see:
//
//   - a non-monotonic step anywhere between those points, which would let
//     the shrinker walk uphill instead of converging
//   - a truncation that yields an INVALID program, which would make the
//     shrinker hand back a repro that does not compile, and would break
//     the always-type-checks invariant that lets the fuzz oracle treat
//     any checker error as a real bug
//
// Both are exactly what closures, `match` and nested control flow
// threaten: truncating mid-construct is where "different but still valid"
// and "smaller corpus, bigger program" become easy to hit. So this needs
// to be in place BEFORE the generator grows — a contract that silently
// lapses leaves every future finding arriving as an unreduced program
// with no shrinker to attack it.
//
// # Why AST nodes and not source length
//
// Source length is the wrong measure, and measuring it produces a false
// failure. On seed 0, truncating 116 -> 115 bytes swaps a parameter's
// type from `u8` to `i32` — the exhaustion fallback correctly takes the
// 0th option, which simply has a wider NAME. The program is structurally
// simpler and one character longer:
//
//	116: function gen_f2(p0: i64[], p1: u8,  p2: i32): i32 { return p2; }
//	115: function gen_f2(p0: i64[], p1: i32, p2: i32): i32 { return p1; }
//
// The generator never promised byte-monotone text, and contorting it to
// order type names by width would be satisfying the metric rather than
// the contract. What the shrinker needs is that the PROGRAM collapses —
// fewer declarations, shallower nesting, less recursion — so the property
// counts AST nodes. That is insensitive to leaf-name width and is what
// "smoothly collapses" actually means.
//
// # Why all three entry points
//
// Each byte-driven entry point has productions the others do not — the
// runnable and printable mains draw their locals from their own pools
// and the printable one emits observation statements no free-form
// program contains. Checking only GenBytes left the two corpora the
// differential oracles actually shrink unproven.
func TestGenBytesShrinkIsMonotonicAndValid(t *testing.T) {
	gens := []struct {
		name string
		gen  func([]byte) string
	}{
		{"GenBytes", fernsmith.GenBytes},
		{"GenMainBytes", fernsmith.GenMainBytes},
		{"GenPrintableMainBytes", fernsmith.GenPrintableMainBytes},
	}
	// One type-check per truncation point per seed per entry point: the
	// full sweep is tens of thousands of them, which does not belong in
	// a default `go test ./internal/fernsmith`. Two seeds each always
	// run, because a contract nothing checks is a contract that lapses —
	// not a token sample, since seed 0 alone found every flip-site
	// defect this property was written for. The dedicated test-fernsmith
	// workflow sets RUN_SHRINK_PROPERTY=1 for the rest.
	seeds := uint64(2)
	if os.Getenv("RUN_SHRINK_PROPERTY") == "1" {
		seeds = sweepN(t, 24)
	}
	for _, g := range gens {
		t.Run(g.name, func(t *testing.T) {
			t.Parallel()
			for seed := uint64(0); seed < seeds; seed++ {
				r := rand.New(rand.NewPCG(seed, 0x5eed))
				corpus := randBytes(r, 192)

				// prev is the program from the NEXT-larger corpus, so each
				// step asserts against its immediate neighbour rather than a
				// distant sample — a single uphill step is enough to break
				// convergence.
				prevSrc := g.gen(corpus)
				prevNodes, err := astNodeCount(t, prevSrc)
				if err != nil {
					t.Fatalf("seed=%d: full corpus does not type-check:\n%s\nerr: %v", seed, prevSrc, err)
				}
				for n := len(corpus) - 1; n >= 0; n-- {
					src := g.gen(corpus[:n])
					nodes, err := astNodeCount(t, src)
					if err != nil {
						t.Fatalf("seed=%d: truncating to %d bytes produced a program that does not type-check — the shrinker would hand back a non-compiling repro\nsrc:\n%s\nerr: %v",
							seed, n, src, err)
					}
					if nodes > prevNodes {
						t.Fatalf("seed=%d: truncating to %d bytes GREW the program (%d AST nodes > %d) — the shrinker can walk uphill from here\nsmaller corpus produced:\n%s\nlarger corpus produced:\n%s",
							seed, n, nodes, prevNodes, src, prevSrc)
					}
					prevSrc, prevNodes = src, nodes
				}
			}
		})
	}
}

// astNodeCount type-checks src and returns the size of its AST. It is the
// structural size measure the shrink property is stated in; an error means
// the generated program is not valid, which is itself a contract breach.
func astNodeCount(t *testing.T, src string) (int, error) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(p)
	if err != nil {
		return 0, err
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return 0, err
	}
	if _, err := checker.Check(prog); err != nil {
		return 0, err
	}
	n := 0
	ast.WalkProgram(prog, func(ast.Node) bool { n++; return true })
	return n, nil
}
