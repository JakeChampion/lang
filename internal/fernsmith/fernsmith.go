// Package fernsmith generates syntactically- and type-correct Lang
// programs from a seeded random source.
//
// The shape is deliberately small for this first slice — five
// scalar types (i32, i64, boolean, f32, string), top-level
// functions only, no statement-level control flow beyond a body's
// sequence of `var` declarations and a final `return`. Every
// expression production picks operands whose types match the
// surrounding context, so the emitted source is guaranteed to
// parse AND type-check. That property is the load-bearing invariant
// the fuzz oracle relies on: any parser or checker error means a
// real bug.
//
// Inspired by wasm-smith: a structured generator that walks the
// grammar top-down, in contrast to the byte-mutation fuzzers in
// internal/parser and internal/checker that exercise mostly junk
// inputs the parser rejects before any later stage sees them.
package fernsmith

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
)

// Config tunes the size of generated programs. Zero values are
// treated as their effective minimum (at least one function, one
// possible statement, etc.) so callers can't accidentally request
// an empty program.
type Config struct {
	MaxFuncs     int
	MaxParams    int
	MaxStmts     int
	MaxExprDepth int
	// MaxLoopDepth caps how deeply nested `while` loops can get
	// inside a body. The bounded-counter pattern emits one i32
	// counter per loop and increments by 1 each iteration, so
	// every loop terminates in a small constant number of steps;
	// the cap is there to keep emitted source short, not to
	// prevent divergence.
	MaxLoopDepth int
	// MaxLoopIters caps the random iteration count picked for
	// each emitted while-loop's counter bound. The generator
	// emits `var c = 0; while (c < N) { ...; c = c + 1; }` with
	// N drawn from [1, MaxLoopIters]. Differential testing
	// needs N small so backends without optimisations don't
	// blow up runtime.
	MaxLoopIters int
}

// DefaultConfig is what Gen uses.
func DefaultConfig() Config {
	return Config{
		MaxFuncs:     3,
		MaxParams:    4,
		MaxStmts:     6,
		MaxExprDepth: 4,
		MaxLoopDepth: 2,
		MaxLoopIters: 5,
	}
}

// Gen returns a Lang program for the given seed. Output is
// deterministic in seed and is guaranteed to parse and type-check
// against the current compiler (within the subset the generator
// covers). Useful for deterministic unit tests; for fuzzing,
// prefer GenBytes — its byte-stream-driven generation maps each
// generator decision onto a single byte of the input, which lets
// `testing.F`'s minimiser shrink failing corpora monotonically.
func Gen(seed uint64) string {
	return newRandGen(seed, DefaultConfig()).Program()
}

// GenBytes returns a Lang program driven directly by the bytes in
// data. Each generator decision (which production to pick, which
// in-scope variable to reference, how many statements to emit)
// consumes one byte; once the stream is exhausted, every
// subsequent decision returns 0, so the generator naturally winds
// down — recursion stops at the leaf production, statement
// counters pick zero. That property is the wasm-smith-style
// minimisation contract: chopping bytes off the end of a corpus
// produces a shorter program, not a different one.
//
// Wire this into `testing.F.Fuzz(func(t *testing.T, data []byte))`
// so the mutator's byte-level edits drive the generator's
// decisions directly.
func GenBytes(data []byte) string {
	return newByteGen(data, DefaultConfig()).Program()
}

// GenMainBytes is GenBytes's runnable counterpart — same byte-
// stream semantics, emits a single `function main(): i32 { ... }`
// instead of a free-form program. Pairs with the differential-
// execution oracle in internal/e2e.
func GenMainBytes(data []byte) string {
	return newByteGen(data, DefaultConfig()).MainProgram()
}

// GenPrintableMain emits a runnable `main` that PRINTS a sequence
// of computed values to stdout (then returns 0), rather than packing
// one byte into the return code. The printable profile re-admits
// float (f32) and exercises string expressions, observing each
// result through a portable channel (booleans, raw string bytes, or
// a truncating `as i32` cast) so the stdout differential oracle in
// internal/e2e can compare them across interp / x86-64 / arm64 / wasm
// without tripping over float formatting. Deterministic in seed.
func GenPrintableMain(seed uint64) string {
	return newRandGen(seed, DefaultConfig()).MainPrintableProgram()
}

// GenPrintableMainBytes is GenPrintableMain's byte-stream counterpart
// for `testing.F` — each generator decision consumes one byte, so the
// fuzzer's mutations drive generation and corpus minimisation works.
func GenPrintableMainBytes(data []byte) string {
	return newByteGen(data, DefaultConfig()).MainPrintableProgram()
}

// New constructs a generator with default limits, drawing from rng.
func New(rng *rand.Rand) *Generator { return NewWithConfig(rng, DefaultConfig()) }

// NewWithConfig is New with a caller-supplied tuning.
func NewWithConfig(rng *rand.Rand, cfg Config) *Generator {
	return &Generator{ch: &randChooser{rng: rng}, cfg: cfg}
}

// newRandGen builds a *Generator backed by a PCG seeded from a
// single uint64, hiding the splitmix-stream constant.
func newRandGen(seed uint64, cfg Config) *Generator {
	rng := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
	return &Generator{ch: &randChooser{rng: rng}, cfg: cfg}
}

// newByteGen builds a *Generator that pulls decisions directly
// from data.
func newByteGen(data []byte, cfg Config) *Generator {
	return &Generator{ch: &byteChooser{data: data}, cfg: cfg}
}

// chooser is the abstraction the Generator uses for every random
// decision. Two implementations: randChooser (PCG-backed,
// statistically-uniform draws — what Gen / GenMain / New use) and
// byteChooser (one byte per decision, deterministic, exhaustion-
// safe — what GenBytes / GenMainBytes use). The split exists so
// `testing.F`'s byte-level mutator can drive the generator
// without sitting behind a `math/rand` wrapper whose rejection-
// sampling loops would deadlock on an all-zero stream.
//
// Exhaustion convention. The byteChooser is required to bias
// every decision towards the *smallest* / *most-terminating*
// branch once its input is exhausted. intN returns 0 (the 0th
// option, which by convention is the smallest), and flip returns
// true — so generator call sites must spell each `flip(p)` such
// that `true` is the branch that shortens the output (pick the
// leaf, reuse an in-scope var, skip the recursion). That rule
// keeps the wasm-smith-style minimisation contract: chopping
// bytes off a failing corpus shrinks the program monotonically.
type chooser interface {
	// intN returns a value in [0, n). Panics if n <= 0.
	intN(n int) int
	// flip returns true with probability roughly p. byteChooser
	// quantises p to one byte's worth of resolution. After
	// exhaustion, byteChooser.flip returns true unconditionally
	// — see the "exhaustion convention" note on the chooser
	// interface.
	flip(p float64) bool
}

// randChooser is a PCG-driven chooser. Honours the math/rand/v2
// statistical guarantees.
type randChooser struct{ rng *rand.Rand }

func (c *randChooser) intN(n int) int      { return c.rng.IntN(n) }
func (c *randChooser) flip(p float64) bool { return c.rng.Float64() < p }

// byteChooser yields decisions from a fixed byte slice. Each
// `intN` and `flip` call consumes exactly one byte. Once the
// slice is exhausted every subsequent decision returns the "0th
// option" / `false` — the smallest, most-terminating choice —
// so chopping bytes off the end of a corpus produces a
// shorter program, not a different one.
//
// This is the load-bearing minimisation contract for the fuzz
// oracle: the testing.F mutator can shrink failing inputs by
// truncating, knowing the generator will collapse smoothly.
type byteChooser struct {
	data []byte
	pos  int
}

func (c *byteChooser) next() (byte, bool) {
	if c.pos >= len(c.data) {
		return 0, false
	}
	b := c.data[c.pos]
	c.pos++
	return b, true
}

func (c *byteChooser) intN(n int) int {
	if n <= 1 {
		return 0
	}
	b, ok := c.next()
	if !ok {
		return 0
	}
	return int(b) % n
}

func (c *byteChooser) flip(p float64) bool {
	b, ok := c.next()
	if !ok {
		// Exhaustion convention: bias to the "smaller / more-
		// terminating" branch. Call sites are written so that
		// `true` is that branch (pick the leaf, reuse a var,
		// skip the recursion).
		return true
	}
	// One byte → 256 quantisation buckets; plenty for the
	// 0.4..0.6 biases the generator uses today.
	return float64(b)/256.0 < p
}

// Profile names the fernsmith generator's operating mode. The
// two values trade coverage for cross-backend determinism:
//
//   - ProfileFree: free-form generation. Every type (including
//     f32 and Map) and every production is allowed. Used by
//     `Gen` / `GenBytes`. The output is for parse + check fuzz
//     coverage only — not run against any backend.
//
//   - ProfileRunnable: the differential-oracle path. Drops f32
//     because Lang's float semantics deliberately under-specify
//     IEEE 754 edge cases (NaN bit-pattern, sign-of-zero through
//     arithmetic, denormal handling — see docs/FLOAT-SEMANTICS.md)
//     and the oracle compares main()'s 1-byte return code bit-for-
//     bit across interp → arm64 → x86_64 → wasm. Generating
//     float math in the runnable path would produce legitimate
//     non-portable results that the oracle can't distinguish from
//     real codegen bugs; excluding f32 keeps the oracle a clean
//     signal. Used by `GenMain` / `GenMainBytes`.
//
// Replaces the prior `noFloats bool` field, which was three
// concepts in one boolean: "skip f32", "runnable mode", and
// "prefer deterministic-across-backends choices". Renaming to
// an enum makes each gate site explicit about which axis it's
// checking.
type Profile int

const (
	ProfileFree Profile = iota
	ProfileRunnable
	// ProfilePrintable is the stdout-oracle path. Like
	// ProfileRunnable it produces a runnable `main`, but it
	// observes results by PRINTING them (rather than packing one
	// byte into the return code), which lets the differential
	// oracle compare full stdout across backends. Crucially it
	// re-admits float (f32) expressions — the bug class the
	// return-byte oracle can't reach — but only ever observes a
	// float through a PORTABLE channel: a boolean comparison
	// ("T"/"F") or a truncating `as i32` cast. Raw float
	// formatting (whose digit count Lang under-specifies — see
	// docs/FLOAT-SEMANTICS.md) is never compared, so NaN / Inf /
	// rounding stay off the oracle's diff while the codegen for
	// float arithmetic and comparison is still exercised.
	ProfilePrintable
)

// floatsAllowed reports whether the current profile's
// production set includes f32 / float-typed values. ProfileFree
// and ProfilePrintable allow them; ProfileRunnable (the return-
// byte oracle) does not.
func (p Profile) floatsAllowed() bool { return p == ProfileFree || p == ProfilePrintable }

// Generator emits source text directly while tracking an in-scope
// set of typed identifiers. Each expression production picks
// operands whose types match the context so the result type-checks
// by construction.
//
// The chooser field is what every random decision goes through; see
// the `chooser` interface for the two implementations.
type Generator struct {
	ch  chooser
	cfg Config
	// profile names the generator's operating mode. Two values
	// today: ProfileFree (the free-form Gen / GenBytes path,
	// every production allowed) and ProfileRunnable (the
	// MainProgram path that has to produce identical output
	// across backends). Replaces what used to be a single
	// `noFloats bool` whose three implicit meanings — skip
	// f32, this is the runnable path, prefer deterministic
	// choices — kept drifting together.
	profile Profile
	// helpers is the running list of every top-level function
	// signature emitted so far. Subsequent function bodies
	// (including main) can call any prior helper with type-
	// correct arguments — this exercises the calling
	// convention, parameter passing, and cross-function return
	// flow that single-function programs miss. Order matters:
	// only forward calls are allowed (helper N can call helpers
	// 0..N-1), which sidesteps self-recursion and unbounded
	// stack growth without needing a static check.
	helpers []helperSig
	// loopDepth tracks the current `while` nesting level so the
	// generator can cap depth at cfg.MaxLoopDepth — every loop
	// is bounded-counter (terminates in <= MaxLoopIters
	// iterations), but unbounded nesting still bloats the
	// emitted source and slows the differential test.
	loopDepth int
	// loopCounter is the monotonically-increasing index used to
	// name every emitted loop counter (`__loop_i0`,
	// `__loop_i1`, …). The `__loop_` prefix guarantees these
	// names never collide with user-style vars (`v*`, `p*`,
	// `w*`) that the rest of the generator emits.
	loopCounter int
	// currentReturnType is the return-type slot of the function
	// whose body is currently being emitted. The try operator
	// (`expr?`) is only legal when the enclosing function
	// returns Option[_] / Result[_, _] — the checker enforces
	// this — so the generator gates `?` emission on this field.
	// Outside any function (between decls), value is unused; the
	// initial zero value is tI32, which is harmless because
	// `?` only fires when currentReturnType == tOptI32.
	currentReturnType gtype
	// optBindCounter names match-arm payload bindings the
	// generator introduces (`__opt_x0`, `__opt_x1`, …). The
	// `__opt_` prefix keeps these out of any path that the
	// user-style `v*` / `p*` / `w*` names take.
	optBindCounter int
	// localHelpers tracks nested function declarations emitted
	// inside the currently-being-generated body. Lifetime is
	// the enclosing function — funcDecl / MainProgram
	// save+restore around each body so local helpers stay
	// scoped to their declaring function. emitCall walks
	// localHelpers alongside the top-level helpers list so
	// callable candidates include both.
	localHelpers []helperSig
	// localFnCounter names emitted local function decls
	// (`__local_fn0`, ...). Generator-private prefix keeps
	// these out of the user-style namespace.
	localFnCounter int
	// Dynamic nominal types. Each entry in structShapes /
	// enumShapes lives at a gtype value beyond `numTypes`
	// (allocated by nextNominal). Multiple struct / enum
	// decls per program flow through the same productions
	// as the fixed Pair / Xyz / Color / Status — the only
	// difference is the shape lives in these maps instead of
	// being baked into the literal / field / match production
	// code.
	structShapes map[gtype]*structShape
	enumShapes   map[gtype]*enumShape
	nextNominal  gtype
}

// structShape is the runtime-allocated form of a struct decl.
// Mirrors `ast.StructDecl` but holds the field types as gtype
// values so productions can recurse into expr() directly.
type structShape struct {
	name   string
	fields []structField
}

type structField struct {
	name string
	t    gtype
}

// enumShape is the runtime-allocated form of a payload-less
// enum decl. Variants are bare names; payload-bearing variants
// are out of scope for the first cut — `Some(T)` and friends
// are reserved built-ins anyway.
type enumShape struct {
	name     string
	variants []string
}

// helperSig is the bare minimum the generator needs to emit a
// call site: the function's name, the types it takes, and the
// type it returns.
type helperSig struct {
	name    string
	params  []gtype
	retType gtype
}

// gtype is the generator's internal enum of Lang types. Each kind
// has its own literal form, applicable operators, and per-type
// bucket inside scope.
//
// Composite types here are deliberately limited to a single
// representative per kind: one array per scalar (i32 / i64 /
// bool element), one fixed `struct Pair { fst: i32, snd: i32 }`,
// and one fixed payload-less `enum Color { Red, Green, Blue }`.
// That keeps the generator's nominal-type tracking trivial (no
// map keyed on declarations) while still exercising the codegen
// paths for array literals + indexing, struct construction +
// field access, and enum literals + match dispatch.
type gtype int

const (
	tI32 gtype = iota
	tI64
	tBool
	tF32
	tString
	tArrI32
	tArrI64
	tArrBool
	tPair
	tColor
	tOptI32
	tMapI32I32
	// Additional nominal types. Each adds a distinct codegen
	// shape so the differential oracle reaches code paths the
	// first round (Pair / Color only) couldn't trigger:
	//
	// - Xyz: heterogeneous struct with a non-i32 field (boolean)
	//   — exercises the per-field stride / offset arithmetic
	//   that uniform-i32 Pair can't.
	// - Status: three-variant payload-less enum like Color but
	//   with different variant *names*, so anywhere a variant
	//   name needs to round-trip through the parser / checker /
	//   IR / codegen / formatter it has a second sample to
	//   compare against.
	tXyz
	tStatus
	// tResI32I32 = Result[i32, i32]. Pairs with the existing
	// Option[i32] machinery (literals, match-with-binding, try
	// operator) — Result's two-arm shape (Ok(T) / Err(E))
	// exercises the variant-with-payload code paths Color /
	// Status (payload-less) and Option (single payload variant)
	// can't reach.
	tResI32I32
	// tTupI32I64 = (i32, i64). A tuple is neither a struct nor an
	// enum: it has its own box layout and its own element
	// stride/offset arithmetic (`tupleElemLayout`), and a
	// heterogeneous one mixes a 4-byte and an 8-byte element so a
	// backend that assumed a uniform stride is caught. Nothing else
	// in the corpus produced a tuple at all, despite fn-typed tuple
	// elements having been a real bug cluster.
	tTupI32I64
	numTypes
)

var allTypes = [numTypes]gtype{
	tI32, tI64, tBool, tF32, tString,
	tArrI32, tArrI64, tArrBool, tPair, tColor, tOptI32, tMapI32I32,
	tXyz, tStatus, tResI32I32, tTupI32I64,
}

// gtypeNames is the source-level name for each builtin gtype, in
// the same iota order as the const block above. Single-source-of-
// truth — `gtype.String()` and `Generator.typeName()` both look
// through this table for the builtin range, so adding a builtin
// is one slot here (plus the const + allTypes entries) rather
// than three switch cases that can drift apart. Pinned in place by
// `TestGtypeNamesCoversEveryBuiltin`.
var gtypeNames = [numTypes]string{
	tI32:       "i32",
	tI64:       "i64",
	tBool:      "boolean",
	tF32:       "f32",
	tString:    "string",
	tArrI32:    "i32[]",
	tArrI64:    "i64[]",
	tArrBool:   "boolean[]",
	tPair:      "Pair",
	tColor:     "Color",
	tOptI32:    "Option[i32]",
	tMapI32I32: "Map[i32, i32]",
	tXyz:       "Xyz",
	tStatus:    "Status",
	tResI32I32: "Result[i32, i32]",
	tTupI32I64: "(i32, i64)",
}

// String reports the source-level name for a builtin gtype.
// Dynamic nominal types (struct / enum shapes registered at
// generation time on a Generator) DON'T resolve through this
// path because the value receiver can't see generator state.
// Use `Generator.typeName(t)` everywhere a dynamic-type name
// might be needed — this method is for builtin-only diagnostic
// paths (panic messages, internal asserts).
func (t gtype) String() string {
	if t >= 0 && int(t) < len(gtypeNames) {
		return gtypeNames[t]
	}
	return fmt.Sprintf("?dyn%d", int(t))
}

// typeName returns the source-level name token for type t. This
// is the canonical name-fetcher: builtin gtypes resolve through
// the static `gtypeNames` table, dynamic nominal types through
// the generator's per-shape maps. Use anywhere a type's name
// needs to land in the emitted source (var annotations,
// function-decl return types, struct-field types). Diagnostic-
// only callers that don't have a Generator can fall back to
// `gtype.String()`, which covers the builtin range.
func (g *Generator) typeName(t gtype) string {
	if t < numTypes {
		return gtypeNames[t]
	}
	if sh, ok := g.structShapes[t]; ok {
		return sh.name
	}
	if sh, ok := g.enumShapes[t]; ok {
		return sh.name
	}
	return fmt.Sprintf("?gtype%d", int(t))
}

// arrayTypeFor returns the array gtype whose element is t, plus a
// success flag. Used to decide whether an in-scope array can be
// indexed to produce a value of type t.
func arrayTypeFor(t gtype) (gtype, bool) {
	switch t {
	case tI32:
		return tArrI32, true
	case tI64:
		return tArrI64, true
	case tBool:
		return tArrBool, true
	}
	return 0, false
}

// arrayElemOf returns the element gtype of an array gtype, plus a
// success flag.
func arrayElemOf(t gtype) (gtype, bool) {
	switch t {
	case tArrI32:
		return tI32, true
	case tArrI64:
		return tI64, true
	case tArrBool:
		return tBool, true
	}
	return 0, false
}

// scope tracks identifiers visible at the current generation
// point, bucketed by gtype so picking "any variable of type T"
// is a constant-time lookup. Map-keyed (not fixed-size array)
// so dynamically-allocated nominal gtypes — user-declared
// structs / enums beyond the closed const set — can be tracked
// uniformly.
type scope struct {
	parent *scope
	vars   map[gtype][]string
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, vars: map[gtype][]string{}}
}

func (s *scope) declare(t gtype, name string) { s.vars[t] = append(s.vars[t], name) }

// inScope returns every identifier of type t reachable from s.
func (s *scope) inScope(t gtype) []string {
	var out []string
	for cur := s; cur != nil; cur = cur.parent {
		out = append(out, cur.vars[t]...)
	}
	return out
}

// ---------- top level ----------

// GenMain returns a single-function runnable Lang program — exactly
// one `function main(): i32 { ... }` whose return value is a
// deterministic byte in [0, 255]. Useful as input for a
// differential-execution oracle, which runs the program through
// every available backend (interp + arm64 + x86_64 + wasm) and
// asserts the observers agree on the result.
//
// Limited to integer + boolean operations so the same expression
// has a single well-defined value across every backend. Floats
// can diverge at Inf/NaN edges, strings need a print-channel
// observation, and division would risk a `/0` trap; all three
// stay out. The mask `& 255i32` makes the result fit in an 8-bit
// exit code AND keeps wasm's PrintMainResult stdout output a
// short ASCII byte string the harness can compare to the native
// exit codes.
//
// For native `testing.F`-driven fuzzing, prefer GenMainBytes —
// its byte-stream RNG plays nicely with the corpus minimiser.
func GenMain(seed uint64) string {
	return newRandGen(seed, DefaultConfig()).MainProgram()
}

// MainProgram emits a program of the shape
//
//	function gen_f0(...): ... { ... }
//	function gen_f1(...): ... { ... }
//	...
//	function main(): i32 { ... return (<i32-expr> & 255i32); }
//
// The 0..K helpers exist so main has things to call: every call
// site in main exercises one more chunk of the calling
// convention / parameter-passing path that single-function
// programs miss. Helpers can themselves call any *earlier*
// helper (forward refs only — sidesteps self- and mutual-
// recursion without a static check). Sets the generator's
// profile to ProfileRunnable for the duration so nested
// productions can't sneak f32 in through boolean comparisons.
func (g *Generator) MainProgram() string {
	prevProfile := g.profile
	prevHelpers := g.helpers
	prevStructShapes := g.structShapes
	prevEnumShapes := g.enumShapes
	prevNextNominal := g.nextNominal
	g.profile = ProfileRunnable
	g.helpers = nil
	g.structShapes = nil
	g.enumShapes = nil
	g.nextNominal = 0
	defer func() {
		g.profile = prevProfile
		g.helpers = prevHelpers
		g.structShapes = prevStructShapes
		g.enumShapes = prevEnumShapes
		g.nextNominal = prevNextNominal
	}()

	var b strings.Builder
	g.preludeDecls(&b)

	// Emit a small number of helpers before main. Helpers in
	// ProfileRunnable mode share the profile's float-free type
	// pool, so their signatures are drawn from {i32, i64, bool,
	// string, i32[], i64[], boolean[], Pair, Color, ...}.
	// Main only consumes i32-returning helpers in its return
	// expression, but non-i32 helpers still get
	// exercised: any helper can call any earlier helper of
	// matching arg/return types.
	nHelpers := g.ch.intN(maxInt(g.cfg.MaxFuncs, 1))
	for i := 0; i < nHelpers; i++ {
		g.funcDecl(&b, i)
	}

	sc := newScope(nil)
	b.WriteString("function main(): i32 { ")
	prevRet := g.currentReturnType
	g.currentReturnType = tI32
	defer func() { g.currentReturnType = prevRet }()
	prevLocals := g.localHelpers
	g.localHelpers = nil
	defer func() { g.localHelpers = prevLocals }()
	n := g.ch.intN(maxInt(g.cfg.MaxStmts, 0) + 1)
	for i := 0; i < n; i++ {
		if g.maybeEmitWhile(&b, sc) {
			continue
		}
		if g.maybeEmitForEach(&b, sc) {
			continue
		}
		if g.maybeEmitLocalFn(&b, sc) {
			continue
		}
		// Main's vars are drawn from the deterministic-across-
		// backends subset (no floats, no strings whose
		// observation needs a print channel). Composite types
		// (arrays / Pair / Color) are pure values that flow
		// through the i32 return path via index / field /
		// match-expr productions in expr(), so they're safe.
		vt := g.pickMainVarType()
		vname := fmt.Sprintf("v%d", i)
		fmt.Fprintf(&b, "var %s: %s = ", vname, g.typeName(vt))
		g.expr(&b, sc, vt, 0)
		b.WriteString("; ")
		sc.declare(vt, vname)
	}
	b.WriteString("return (")
	g.expr(&b, sc, tI32, 0)
	b.WriteString(" & 255i32); }\n")
	return b.String()
}

// MainPrintableProgram emits the printable-oracle counterpart of
// MainProgram: a `main` that prints a sequence of computed values and
// returns 0. See ProfilePrintable for the portability contract. Var
// declarations stay on the float-free runnable type pool (so floats
// only ever appear inside the print observations, never as a bare
// `var` whose formatting would be compared); the observations
// themselves draw in float (f32) arithmetic + comparison, including a
// guaranteed NaN/Inf-aware float comparison so the oracle reaches the
// unordered-comparison codegen the return-byte path can't.
func (g *Generator) MainPrintableProgram() string {
	prevProfile := g.profile
	prevHelpers := g.helpers
	prevStructShapes := g.structShapes
	prevEnumShapes := g.enumShapes
	prevNextNominal := g.nextNominal
	g.profile = ProfilePrintable
	g.helpers = nil
	g.structShapes = nil
	g.enumShapes = nil
	g.nextNominal = 0
	defer func() {
		g.profile = prevProfile
		g.helpers = prevHelpers
		g.structShapes = prevStructShapes
		g.enumShapes = prevEnumShapes
		g.nextNominal = prevNextNominal
	}()

	var b strings.Builder
	g.preludeDecls(&b)

	nHelpers := g.ch.intN(maxInt(g.cfg.MaxFuncs, 1))
	for i := 0; i < nHelpers; i++ {
		g.funcDecl(&b, i)
	}

	sc := newScope(nil)
	b.WriteString("function main(): i32 { ")
	prevRet := g.currentReturnType
	g.currentReturnType = tI32
	defer func() { g.currentReturnType = prevRet }()
	prevLocals := g.localHelpers
	g.localHelpers = nil
	defer func() { g.localHelpers = prevLocals }()

	// Runtime float 0.0 / 1.0 for the special-value (NaN / Inf)
	// comparison observation. Runtime vars (not literals) so
	// constfold neither rejects nor pre-evaluates `__fz / __fz`.
	b.WriteString("var __fz: f32 = 0.0f32; var __fo: f32 = 1.0f32; ")

	n := g.ch.intN(maxInt(g.cfg.MaxStmts, 0) + 1)
	for i := 0; i < n; i++ {
		if g.maybeEmitWhile(&b, sc) {
			continue
		}
		if g.maybeEmitForEach(&b, sc) {
			continue
		}
		vt := g.pickMainVarType()
		vname := fmt.Sprintf("v%d", i)
		fmt.Fprintf(&b, "var %s: %s = ", vname, g.typeName(vt))
		g.expr(&b, sc, vt, 0)
		b.WriteString("; ")
		sc.declare(vt, vname)
	}

	// One mandatory NaN/Inf-aware float comparison (guarantees the
	// unordered path is covered every program, and that __fz/__fo are
	// referenced), then a handful of random observations.
	g.emitFloatSpecialObservation(&b, sc)
	nObs := g.ch.intN(maxInt(g.cfg.MaxStmts, 1))
	for i := 0; i < nObs; i++ {
		g.emitPrintObservation(&b, sc)
	}

	b.WriteString("return 0; }\n")
	return b.String()
}

// emitPrintObservation writes one `print(...)` statement observing a
// generated expression through a backend-portable channel: integers
// via .to_string(), booleans (including float comparisons) as
// "T"/"F", strings raw, and floats truncated through `as i32`.
func (g *Generator) emitPrintObservation(b *strings.Builder, sc *scope) {
	switch g.ch.intN(6) {
	case 0:
		b.WriteString("print((")
		g.expr(b, sc, tI32, 0)
		b.WriteString(").to_string()); ")
	case 1:
		b.WriteString("print((")
		g.expr(b, sc, tI64, 0)
		b.WriteString(").to_string()); ")
	case 2:
		b.WriteString("print(if (")
		g.expr(b, sc, tBool, 0)
		b.WriteString(") { \"T\" } else { \"F\" }); ")
	case 3:
		b.WriteString("print(")
		g.expr(b, sc, tString, 0)
		b.WriteString("); ")
	case 4:
		// Float value observed by truncation — exercises float
		// arithmetic + the saturating float→int conversion, both
		// portable.
		b.WriteString("print(((")
		g.expr(b, sc, tF32, 0)
		b.WriteString(") as i32).to_string()); ")
	default:
		g.emitFloatSpecialObservation(b, sc)
	}
}

// emitFloatSpecialObservation prints the result of a float comparison
// ("T"/"F") whose operands may be NaN or ±Inf (built from the runtime
// __fz / __fo zeros and ones). Float division is well-defined (never
// traps) so these are safe to emit; the boolean result is portable,
// which is what makes the unordered-comparison codegen testable.
func (g *Generator) emitFloatSpecialObservation(b *strings.Builder, sc *scope) {
	op := []string{"<", "<=", ">", ">=", "==", "!="}[g.ch.intN(6)]
	b.WriteString("print(if ((")
	g.emitFloatSpecialOperand(b, sc)
	fmt.Fprintf(b, ") %s (", op)
	g.emitFloatSpecialOperand(b, sc)
	b.WriteString(")) { \"T\" } else { \"F\" }); ")
}

// emitFloatSpecialOperand writes one operand of a special-value float
// comparison: NaN (0/0), +Inf (1/0), -Inf ((0-1)/0), or an ordinary
// finite f32 expression.
func (g *Generator) emitFloatSpecialOperand(b *strings.Builder, sc *scope) {
	switch g.ch.intN(4) {
	case 0:
		b.WriteString("(__fz / __fz)") // NaN
	case 1:
		b.WriteString("(__fo / __fz)") // +Inf
	case 2:
		b.WriteString("((__fz - __fo) / __fz)") // -Inf
	default:
		g.expr(b, sc, tF32, 1) // ordinary finite f32 expression
	}
}

// mainVarTypes are the gtypes legal for `var v<N>` declarations
// inside `main` under ProfileRunnable. Strings are exercised
// through `len(s)` in the i32 path (see tryCompositeProduction),
// so they
// flow into the byte oracle even without a separate stdout
// channel. Composite types do the same via array index, Pair
// field access, and match-over-Color.
var mainVarTypes = []gtype{
	tI32, tI64, tBool, tString,
	tArrI32, tArrI64, tArrBool,
	tPair, tColor, tOptI32,
	tXyz, tStatus, tResI32I32, tTupI32I64,
	// tMapI32I32 is now included since the interp grew a Map
	// runtime — `map_new`, `__method_Map_*`, and `*ast.MapLit`
	// evaluation all live in internal/interp/interp.go now,
	// so Map values flowing into main's expression path
	// round-trip through interp + native backends identically.
	tMapI32I32,
}

// preludeDecls emits the fixed `struct Pair` + `enum Color`
// declarations every generated program shares, plus a small set
// of fixed methods on Pair. Methods exercise the method-dispatch
// path (`expr.method(args)` syntactic sugar that the checker
// rewrites to `__method_Pair_<name>(expr, args)`) without
// needing per-struct nominal-type tracking — Pair is fixed, so
// the method names are fixed too.
func (g *Generator) preludeDecls(b *strings.Builder) {
	// The auto-prelude is gone (docs/PRELUDE-TO-MODULES.md phase 5),
	// so generated programs must declare the stdlib they lean on:
	// `.to_string()` (std/i32 / core/int), string + Map `.len()` and
	// Map literals (core/map), array helpers (std/array). Imports
	// must precede all decls. Resolving them needs the modload path —
	// the fernsmith tests load generated source through modload, not
	// bare parser.Parse.
	b.WriteString("import \"std/i32\";\n")
	b.WriteString("import \"std/i64\";\n")
	b.WriteString("import \"std/string\";\n")
	b.WriteString("import \"std/array\";\n")
	b.WriteString("import \"core/int\";\n")
	b.WriteString("import \"core/map\";\n")
	b.WriteString("struct Pair { fst: i32, snd: i32 }\n")
	// `Xyz` — heterogeneous struct with an i32 field and a
	// boolean field. Exercises the per-field stride / offset
	// arithmetic that Pair (uniform i32) can't.
	//
	// The field name `n` (not `id`) avoids a name collision
	// with the generic helper `function id[T](x: T): T { ... }`
	// declared further down: an `Xyz { id: 1, valid: true }`
	// literal sits next to `id(42)` call sites in the same
	// program, and the monomorph re-check rejects the program
	// because `id` resolves to the field-name token in the
	// scope where it should be the generic function.
	b.WriteString("struct Xyz { n: i32, valid: boolean }\n")
	b.WriteString("enum Color { Red, Green, Blue }\n")
	// `Status` — second payload-less enum with different
	// variant names. Anywhere a variant name has to round-trip
	// through the parser / checker / IR / codegen / formatter,
	// this gives the path a second sample distinct from
	// Color's Red / Green / Blue.
	b.WriteString("enum Status { Active, Inactive, Pending }\n")
	// `pair.sum()`: Pair → i32. The simplest method shape — no
	// args, scalar return. Flows back into the i32 byte-oracle
	// path via tryCompositeProduction.
	b.WriteString("function (p: Pair) sum(): i32 { return (p.fst + p.snd); }\n")
	// `pair.swap()`: Pair → Pair. Tests methods that return the
	// receiver type — a common stdlib shape codegen could
	// plausibly mishandle.
	b.WriteString("function (p: Pair) swap(): Pair { return (Pair { fst: p.snd, snd: p.fst }); }\n")
	// Generic helpers. Each accepts a type parameter T and the
	// checker infers it from the call's arg types. Monomorph
	// clones each one per instantiation before codegen sees
	// them; calling `id` with both i32 and bool args in the
	// same program forces the monomorphiser to walk its
	// clone-and-rename path, which the differential oracle
	// then exercises end-to-end.
	b.WriteString("function id[T](x: T): T { return x; }\n")
	b.WriteString("function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }\n")
	// Dynamic nominal types — per-program random struct + enum
	// shapes. See declareDynamicNominals for the field /
	// variant generation rules.
	g.declareDynamicNominals(b)
}

// declareDynamicNominals emits 0..3 additional struct decls and
// 0..2 additional enum decls per program, with random field /
// variant shapes drawn from the chooser. Each declaration gets a
// fresh gtype value past `numTypes` so the scope's map keys
// stay collision-free with the fixed Pair / Xyz / Color / Status.
//
// Field types are drawn from the runnable-safe scalar set
// (i32 / i64 / boolean / string) — composites stay out of
// fields for the first cut to keep the recursive expression
// generation finite and the codegen layout simple.
//
// Variant names use a generator-private `__E<i>_V<j>` prefix to
// guarantee global uniqueness without colliding with any
// stdlib variant. Struct names use `S<i>` and field names
// `f<j>` — those don't collide with anything in the prelude or
// any user code the generator emits.
func (g *Generator) declareDynamicNominals(b *strings.Builder) {
	if g.structShapes == nil {
		g.structShapes = map[gtype]*structShape{}
	}
	if g.enumShapes == nil {
		g.enumShapes = map[gtype]*enumShape{}
	}
	if g.nextNominal < numTypes {
		g.nextNominal = numTypes
	}
	// Up to 3 extra structs. Each has 2..4 fields drawn from
	// scalar types.
	nStructs := g.ch.intN(4) // 0..3
	for i := 0; i < nStructs; i++ {
		name := fmt.Sprintf("S%d", i)
		nFields := 2 + g.ch.intN(3) // 2..4 fields
		fields := make([]structField, nFields)
		fmt.Fprintf(b, "struct %s { ", name)
		for j := 0; j < nFields; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			ft := []gtype{tI32, tI64, tBool, tString}[g.ch.intN(4)]
			fname := fmt.Sprintf("f%d", j)
			fields[j] = structField{name: fname, t: ft}
			fmt.Fprintf(b, "%s: %s", fname, ft)
		}
		b.WriteString(" }\n")
		gt := g.nextNominal
		g.nextNominal++
		g.structShapes[gt] = &structShape{name: name, fields: fields}
	}
	// Up to 2 extra enums. Each has 2..4 payload-less variants.
	nEnums := g.ch.intN(3) // 0..2
	for i := 0; i < nEnums; i++ {
		name := fmt.Sprintf("E%d", i)
		nVariants := 2 + g.ch.intN(3) // 2..4
		variants := make([]string, nVariants)
		fmt.Fprintf(b, "enum %s { ", name)
		for j := 0; j < nVariants; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			vname := fmt.Sprintf("__E%d_V%d", i, j)
			variants[j] = vname
			b.WriteString(vname)
		}
		b.WriteString(" }\n")
		gt := g.nextNominal
		g.nextNominal++
		g.enumShapes[gt] = &enumShape{name: name, variants: variants}
	}
}

// Program emits a complete program: prelude type decls followed
// by N top-level function declarations. Helpers can call any
// earlier helper (forward refs only — see funcDecl).
func (g *Generator) Program() string {
	prevStructShapes := g.structShapes
	prevEnumShapes := g.enumShapes
	prevNextNominal := g.nextNominal
	g.structShapes = nil
	g.enumShapes = nil
	g.nextNominal = 0
	defer func() {
		g.structShapes = prevStructShapes
		g.enumShapes = prevEnumShapes
		g.nextNominal = prevNextNominal
	}()
	var b strings.Builder
	g.preludeDecls(&b)
	n := 1 + g.ch.intN(maxInt(g.cfg.MaxFuncs, 1))
	for i := 0; i < n; i++ {
		g.funcDecl(&b, i)
	}
	return b.String()
}

func (g *Generator) funcDecl(b *strings.Builder, idx int) {
	sc := newScope(nil)
	nParams := g.ch.intN(maxInt(g.cfg.MaxParams, 0) + 1)
	name := fmt.Sprintf("gen_f%d", idx)
	params := make([]gtype, nParams)
	fmt.Fprintf(b, "function %s(", name)
	for i := 0; i < nParams; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		pt := g.pickType()
		params[i] = pt
		pn := fmt.Sprintf("p%d", i)
		sc.declare(pt, pn)
		fmt.Fprintf(b, "%s: %s", pn, g.typeName(pt))
	}
	b.WriteByte(')')
	ret := g.pickType()
	fmt.Fprintf(b, ": %s { ", g.typeName(ret))
	prevRet := g.currentReturnType
	g.currentReturnType = ret
	g.body(b, sc, ret)
	g.currentReturnType = prevRet
	b.WriteString("}\n")
	// Register AFTER the body emit so this decl can't accidentally
	// recurse into itself. Only forward calls are visible.
	g.helpers = append(g.helpers, helperSig{name: name, params: params, retType: ret})
}

// body emits a sequence of `var` declarations / while-loops
// followed by a typed `return`. Each `var` adds a fresh name to
// the scope so later statements can reference it; while-loops use
// the bounded-counter pattern (see emitWhileLoop) and don't add
// anything to the outer scope.
func (g *Generator) body(b *strings.Builder, sc *scope, retT gtype) {
	prevLocals := g.localHelpers
	g.localHelpers = nil
	defer func() { g.localHelpers = prevLocals }()
	n := g.ch.intN(maxInt(g.cfg.MaxStmts, 0) + 1)
	for i := 0; i < n; i++ {
		if g.maybeEmitWhile(b, sc) {
			continue
		}
		if g.maybeEmitForEach(b, sc) {
			continue
		}
		if g.maybeEmitLocalFn(b, sc) {
			continue
		}
		vt := g.pickType()
		vname := fmt.Sprintf("v%d", i)
		fmt.Fprintf(b, "var %s: %s = ", vname, g.typeName(vt))
		g.expr(b, sc, vt, 0)
		b.WriteString("; ")
		sc.declare(vt, vname)
	}
	b.WriteString("return ")
	g.expr(b, sc, retT, 0)
	b.WriteString("; ")
}

// maybeEmitWhile probabilistically emits a bounded-counter while
// loop. Returns true when it wrote one (caller should skip the
// var-decl it would have emitted otherwise), false when the
// loop-depth cap or the random gate skipped emission.
//
// Bounded-counter pattern:
//
//	var __loop_i<N>: i32 = 0i32;
//	while (__loop_i<N> < <K>i32) {
//	    <body var-decls>
//	    __loop_i<N> = __loop_i<N> + 1i32;
//	}
//
// The counter name is generator-private (`__loop_` prefix), so
// body var-decls (`v*`, `p*`, `w*`) can't shadow or alias it.
// The body never assigns to the counter — only the trailing
// increment does — so termination after at most K iterations is
// statically obvious.
func (g *Generator) maybeEmitWhile(b *strings.Builder, sc *scope) bool {
	if g.loopDepth >= maxInt(g.cfg.MaxLoopDepth, 0) {
		return false
	}
	// Exhaustion convention: `false` here = no loop (smaller
	// output). Bias to ~15% loop emission per slot when bytes
	// are flowing.
	if g.flip(0.85) {
		return false
	}
	g.emitWhileLoop(b, sc)
	return true
}

func (g *Generator) emitWhileLoop(b *strings.Builder, sc *scope) {
	idx := g.loopCounter
	g.loopCounter++
	g.loopDepth++
	defer func() { g.loopDepth-- }()

	counter := fmt.Sprintf("__loop_i%d", idx)
	iters := 1 + g.ch.intN(maxInt(g.cfg.MaxLoopIters, 1))

	fmt.Fprintf(b, "var %s: i32 = 0i32; ", counter)
	fmt.Fprintf(b, "while (%s < %di32) { ", counter, iters)

	// Body: a few var-decls. Use an inner scope so loop-body
	// names don't leak out; the inner scope's parent chain
	// still gives expressions visibility into outer vars.
	inner := newScope(sc)
	nstmts := g.ch.intN(3)
	for i := 0; i < nstmts; i++ {
		if g.maybeEmitWhile(b, inner) {
			continue
		}
		if g.maybeEmitForEach(b, inner) {
			continue
		}
		vt := g.pickType()
		vname := fmt.Sprintf("w%d_%d", idx, i)
		fmt.Fprintf(b, "var %s: %s = ", vname, g.typeName(vt))
		g.expr(b, inner, vt, 0)
		b.WriteString("; ")
		inner.declare(vt, vname)
	}

	fmt.Fprintf(b, "%s = %s + 1i32; ", counter, counter)
	b.WriteString("} ")
}

// maybeEmitLocalFn probabilistically emits a nested function
// declaration as a statement. Local fns are full closures —
// they read outer-scope vars by name (the checker's capture
// analysis stamps the FuncDecl.Captures list, and codegen /
// the interpreter use that snapshot at the def site).
//
// Returns true when it wrote one (caller should skip the
// var-decl it would have emitted otherwise). Adds the
// signature to g.localHelpers so subsequent expressions in
// the same body can emitCall it. The localHelpers list is
// scoped to the enclosing function — save+restore around
// each body keeps cross-function state clean.
func (g *Generator) maybeEmitLocalFn(b *strings.Builder, sc *scope) bool {
	if g.flip(0.88) {
		return false
	}
	idx := g.localFnCounter
	g.localFnCounter++
	name := fmt.Sprintf("__local_fn%d", idx)
	nParams := g.ch.intN(maxInt(g.cfg.MaxParams, 0) + 1)
	params := make([]gtype, nParams)
	inner := newScope(sc)
	fmt.Fprintf(b, "function %s(", name)
	for i := 0; i < nParams; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		pt := g.pickType()
		params[i] = pt
		pn := fmt.Sprintf("lp%d", i)
		inner.declare(pt, pn)
		fmt.Fprintf(b, "%s: %s", pn, g.typeName(pt))
	}
	b.WriteByte(')')
	ret := g.pickType()
	fmt.Fprintf(b, ": %s { return ", g.typeName(ret))
	// Body is a single return expression. Don't recurse into
	// emitWhile / emitForEach / nested local fns from the body
	// shape — that'd bloat the source and put nested closures
	// on hot paths the diff oracle hasn't proved out yet.
	prevReturnType := g.currentReturnType
	g.currentReturnType = ret
	g.expr(b, inner, ret, 0)
	g.currentReturnType = prevReturnType
	b.WriteString("; } ")
	g.localHelpers = append(g.localHelpers, helperSig{name: name, params: params, retType: ret})
	return true
}

// maybeEmitForEach probabilistically emits a `for x in arr { ... }`
// or `for (k, v) in m { ... }` over an in-scope array / Map. Returns
// true when it wrote one (caller should skip the var-decl it
// would have emitted otherwise), false when no eligible scope
// var exists or the random gate skipped emission.
//
// Termination is automatic: each iteration walks one position of
// the underlying data structure, which has a finite size set at
// construction time. No counter to mutate, no risk of infinite
// loops.
//
// The body lives in an inner scope so the loop variable's name
// can't leak out. Same loop-depth budget as the while loop.
func (g *Generator) maybeEmitForEach(b *strings.Builder, sc *scope) bool {
	if g.loopDepth >= maxInt(g.cfg.MaxLoopDepth, 0) {
		return false
	}
	// Pick an in-scope iterable: array or Map. If neither is
	// available, skip.
	var arrayVar string
	var elemType gtype
	for _, at := range []gtype{tArrI32, tArrI64, tArrBool} {
		vars := sc.inScope(at)
		if len(vars) > 0 {
			arrayVar = vars[g.ch.intN(len(vars))]
			elemType, _ = arrayElemOf(at)
			break
		}
	}
	mapVars := sc.inScope(tMapI32I32)
	if arrayVar == "" && len(mapVars) == 0 {
		return false
	}
	// Exhaustion convention: `false` here = no loop (smaller
	// output). Bias to ~12% emission per slot when bytes flow.
	if g.flip(0.88) {
		return false
	}
	g.loopDepth++
	defer func() { g.loopDepth-- }()
	idx := g.loopCounter
	g.loopCounter++

	inner := newScope(sc)
	switch {
	case arrayVar != "" && (len(mapVars) == 0 || g.flip(0.5)):
		// for x in <arr> { ... }
		bind := fmt.Sprintf("__fe_x%d", idx)
		fmt.Fprintf(b, "for %s in %s { ", bind, arrayVar)
		inner.declare(elemType, bind)
	default:
		// for (k, v) in <map> { ... } — k/v are i32 since we
		// only generate Map[i32, i32].
		mapVar := mapVars[g.ch.intN(len(mapVars))]
		kBind := fmt.Sprintf("__fe_k%d", idx)
		vBind := fmt.Sprintf("__fe_v%d", idx)
		fmt.Fprintf(b, "for (%s, %s) in %s { ", kBind, vBind, mapVar)
		inner.declare(tI32, kBind)
		inner.declare(tI32, vBind)
	}

	nstmts := g.ch.intN(3)
	for i := 0; i < nstmts; i++ {
		if g.maybeEmitWhile(b, inner) {
			continue
		}
		if g.maybeEmitForEach(b, inner) {
			continue
		}
		vt := g.pickType()
		vname := fmt.Sprintf("fe%d_%d", idx, i)
		fmt.Fprintf(b, "var %s: %s = ", vname, g.typeName(vt))
		g.expr(b, inner, vt, 0)
		b.WriteString("; ")
		inner.declare(vt, vname)
	}
	b.WriteString("} ")
	return true
}

// ---------- expressions ----------

// expr emits a well-typed expression of type t. Depth is the
// current recursion level; once it exceeds cfg.MaxExprDepth, only
// leaf productions (literals + variable references) are emitted so
// the tree always terminates.
//
// Two non-leaf productions are tried before falling through to the
// per-type composite path: a call to a registered helper whose
// return type is t, and an if-expression with both arms typed t.
// Either can short-circuit the rest of the dispatch — when neither
// applies, the dispatch falls into numericExpr / boolExpr / a
// leaf depending on t.
func (g *Generator) expr(b *strings.Builder, sc *scope, t gtype, depth int) {
	if depth >= g.cfg.MaxExprDepth || g.flip(0.4) {
		g.leaf(b, sc, t, depth)
		return
	}
	// Try operator: `(<Option[i32]-expr>?)` yields i32. Only
	// legal when the enclosing function returns Option[T] (the
	// checker enforces this), so gate on currentReturnType. The
	// `?` short-circuits the function on `None`, returning None
	// to the caller — that's the early-return path differential
	// testing wouldn't otherwise exercise.
	//
	// The operand can't be bare `None` because the checker
	// can't infer its T inside the `?` slot — `emitTypedOptI32`
	// picks an in-scope Option[i32] var (whose type is known)
	// or a Some-wrapped value (where the i32 payload pins the
	// type).
	if t == tI32 && g.currentReturnType == tOptI32 && !g.flip(0.85) {
		b.WriteByte('(')
		g.emitTypedOptI32(b, sc, depth)
		b.WriteString("?)")
		return
	}
	// Same try-operator pattern for Result. `(<Result-expr>?)`
	// yields the Ok-payload T (i32 here); on Err the function
	// short-circuits with the Err value passed through. Only
	// legal when the enclosing function returns Result[_, E]
	// with the SAME E — the checker requires error-type
	// equality so we can't mix a Result[_, i32] try with a
	// Result[_, string]-returning function.
	//
	// Requires an in-scope Result[i32, i32] var because bare
	// `Ok(x)` / `Err(e)` only pins one of the two type
	// parameters and the `?` slot doesn't propagate
	// surrounding context to fill in the other. With an
	// in-scope var the type is known from its annotation.
	if t == tI32 && g.currentReturnType == tResI32I32 && !g.flip(0.85) {
		ress := sc.inScope(tResI32I32)
		if len(ress) > 0 {
			fmt.Fprintf(b, "(%s?)", ress[g.ch.intN(len(ress))])
			return
		}
		// Fall through to other productions if no in-scope
		// Result var is available.
	}
	// Helper call. Skipped when no helper returns t, or when the
	// generator rolls "small branch" — exhaustion convention
	// keeps generation terminating.
	if !g.flip(0.7) {
		if g.emitCall(b, sc, t, depth) {
			return
		}
	}
	// Composite-derived production: route an in-scope array /
	// Pair / Color through an index / field / match-expr to
	// produce a value of type t. Bails out (returns false)
	// when no in-scope composite supplies t; caller falls
	// through to the per-type composite path below.
	if !g.flip(0.75) {
		if g.tryCompositeProduction(b, sc, t, depth) {
			return
		}
	}
	// If-expression. The Lang `if (cond) { then } else { else }`
	// in expression position requires both arms to share a type;
	// recursing with the same t on both arms preserves that.
	if !g.flip(0.8) {
		g.emitIfExpr(b, sc, t, depth)
		return
	}
	// Generic helper calls: `id(<expr>)` and `pick(<bool>,
	// <expr>, <expr>)`. Both are declared in preludeDecls with
	// type-parameter T; the checker infers T from the arg
	// types and the monomorphiser clones the function per
	// instantiation. Wiring them here for any t exercises that
	// inference + monomorph path across the type universe.
	//
	if !g.flip(0.85) {
		// `id` is the simpler call — single arg of type t,
		// returns t. Exhaustion convention: `true` => skip the
		// generic call (smaller output, no extra clone for
		// monomorph to handle).
		fmt.Fprintf(b, "id(")
		g.genericArg(b, sc, t, depth)
		b.WriteString(")")
		return
	}
	if !g.flip(0.9) {
		// `pick` is the three-arg variant. Both `a` and `b`
		// recurse at type t so the checker's pairwise
		// unification produces a single T. Use genericArg so
		// type-ambiguous values like bare `None` get nudged
		// into a concrete form before the inference runs.
		b.WriteString("pick(")
		g.expr(b, sc, tBool, depth+1)
		b.WriteString(", ")
		g.genericArg(b, sc, t, depth)
		b.WriteString(", ")
		g.genericArg(b, sc, t, depth)
		b.WriteString(")")
		return
	}
	switch t {
	case tI32, tI64, tF32:
		g.numericExpr(b, sc, t, depth)
	case tBool:
		g.boolExpr(b, sc, depth)
	case tString:
		g.stringExpr(b, sc, depth)
	default:
		// Composite types — no binary / arithmetic productions,
		// fall through to leaf which handles var-refs and
		// literals (including the recursive array / Pair literal
		// forms).
		g.leaf(b, sc, t, depth)
	}
}

// stringExpr picks a non-leaf string production: either `s1 + s2`
// concatenation or an `f"..."` interpolated string. Both are
// observable through `len()` in the byte oracle, so they
// participate in the differential test even without a separate
// stdout channel.
func (g *Generator) stringExpr(b *strings.Builder, sc *scope, depth int) {
	if g.flip(0.5) {
		// Concat: `(s1 + s2)`. The checker stamps IsStringConcat
		// for backends that need the runtime helper.
		b.WriteByte('(')
		g.expr(b, sc, tString, depth+1)
		b.WriteString(" + ")
		g.expr(b, sc, tString, depth+1)
		b.WriteByte(')')
		return
	}
	// F-string: `f"<lit>{<i32-expr>}<lit>{<i32-expr>}..."`. Two
	// literal segments + one or two interpolants keeps the
	// emitted source short; the checker's desugar wires each
	// `{e}` through `.to_string()` for the eventual concat.
	n := 1 + g.ch.intN(2) // 1..2 interpolants
	b.WriteString("f\"")
	for i := 0; i < n; i++ {
		g.fstringLitSegment(b)
		b.WriteByte('{')
		// Restrict to i32 interpolants — every backend has a
		// to_string for i32, and the result is platform-
		// neutral (no float NaN, no platform pointer fmt).
		g.expr(b, sc, tI32, depth+1)
		b.WriteByte('}')
	}
	g.fstringLitSegment(b)
	b.WriteByte('"')
}

// fstringLitSegment writes a short ASCII-only segment for the
// literal parts of an f-string. No escape sequences — the lexer
// doesn't need to special-case them, the checker doesn't see them
// in special-case interpolant positions.
func (g *Generator) fstringLitSegment(b *strings.Builder) {
	n := g.ch.intN(6)
	for i := 0; i < n; i++ {
		b.WriteByte(byte('a' + g.ch.intN(26)))
	}
}

// tryCompositeProduction emits one of: array index access,
// struct field access, enum match-expression, or `len(s)` over
// a string — whichever kind of in-scope composite / string var
// can produce a value of type t. Tries them in fixed order
// (array, struct, enum, len) so a richer program seed naturally
// cascades through more shapes. Returns false (without writing)
// when no in-scope composite supplies t; caller falls back to
// another production.
func (g *Generator) tryCompositeProduction(b *strings.Builder, sc *scope, t gtype, depth int) bool {
	// Array index. `<arr-var>[0i32]` — fixed index avoids
	// needing to track per-array lengths. The generator emits
	// every array literal with length >= 1, so [0] is in-
	// bounds by construction.
	if arrT, ok := arrayTypeFor(t); ok {
		arrs := sc.inScope(arrT)
		if len(arrs) > 0 {
			name := arrs[g.ch.intN(len(arrs))]
			fmt.Fprintf(b, "%s[0i32]", name)
			return true
		}
	}
	// Struct field. `Pair.fst` / `.snd` are both i32.
	// Sometimes a method call instead of a field access — both
	// produce i32, both desugar through different checker paths
	// (FieldAccess vs Call-with-MethodCallSite-rewritten).
	if t == tI32 {
		pairs := sc.inScope(tPair)
		if len(pairs) > 0 {
			name := pairs[g.ch.intN(len(pairs))]
			if g.flip(0.6) {
				field := []string{"fst", "snd"}[g.ch.intN(2)]
				fmt.Fprintf(b, "%s.%s", name, field)
			} else {
				// `pair.sum()` — method dispatch path.
				fmt.Fprintf(b, "%s.sum()", name)
			}
			return true
		}
	}
	// `pair.swap()`: Pair → Pair. Routes any in-scope Pair into
	// a fresh Pair via the method-dispatch path.
	if t == tPair {
		pairs := sc.inScope(tPair)
		if len(pairs) > 0 && !g.flip(0.7) {
			name := pairs[g.ch.intN(len(pairs))]
			fmt.Fprintf(b, "%s.swap()", name)
			return true
		}
	}
	// Tuple element access. `t.0` is i32, `t.1` is i64 — the read
	// side of the only heterogeneous-stride box in the corpus.
	// Without a read production the generator would construct
	// tuples and never observe one, which is exactly how `i64[]`
	// values sat in the corpus unread throughout #5729.
	if t == tI32 || t == tI64 {
		tups := sc.inScope(tTupI32I64)
		if len(tups) > 0 {
			name := tups[g.ch.intN(len(tups))]
			if t == tI32 {
				fmt.Fprintf(b, "%s.0", name)
			} else {
				fmt.Fprintf(b, "%s.1", name)
			}
			return true
		}
	}
	// Xyz field access. `Xyz.n` is i32, `Xyz.valid` is bool.
	if t == tI32 {
		xyzs := sc.inScope(tXyz)
		if len(xyzs) > 0 {
			name := xyzs[g.ch.intN(len(xyzs))]
			fmt.Fprintf(b, "%s.n", name)
			return true
		}
	}
	if t == tBool {
		xyzs := sc.inScope(tXyz)
		if len(xyzs) > 0 {
			name := xyzs[g.ch.intN(len(xyzs))]
			fmt.Fprintf(b, "%s.valid", name)
			return true
		}
	}
	// `s.len()` — string-to-i32 byte-count. Same shape as the
	// array-index path: every string is observable byte-wise,
	// so this is the channel through which string ops flow
	// into the byte oracle's i32 return path.
	if t == tI32 {
		strs := sc.inScope(tString)
		if len(strs) > 0 {
			name := strs[g.ch.intN(len(strs))]
			fmt.Fprintf(b, "%s.len()", name)
			return true
		}
	}
	// Enum match-expr. `(match (c) { Red => e1, Green => e2,
	// Blue => e3 })` routes a Color into any non-Color t.
	// Skipped for t == Color to avoid infinite recursion (the
	// arms would themselves need Color values).
	if t != tColor {
		colors := sc.inScope(tColor)
		if len(colors) > 0 {
			name := colors[g.ch.intN(len(colors))]
			fmt.Fprintf(b, "(match (%s) { Red => ", name)
			g.expr(b, sc, t, depth+1)
			b.WriteString(", Green => ")
			g.expr(b, sc, t, depth+1)
			b.WriteString(", Blue => ")
			g.expr(b, sc, t, depth+1)
			b.WriteString(" })")
			return true
		}
	}
	// Status match-expr (parallel to Color). Routes a Status
	// into any non-Status t through the three variants.
	if t != tStatus {
		stats := sc.inScope(tStatus)
		if len(stats) > 0 {
			name := stats[g.ch.intN(len(stats))]
			fmt.Fprintf(b, "(match (%s) { Active => ", name)
			g.expr(b, sc, t, depth+1)
			b.WriteString(", Inactive => ")
			g.expr(b, sc, t, depth+1)
			b.WriteString(", Pending => ")
			g.expr(b, sc, t, depth+1)
			b.WriteString(" })")
			return true
		}
	}
	// Option[i32] match-with-binding. `(match (o) { Some(x) =>
	// <expr>, None => <expr> })` routes an Option into any
	// non-Option t. The Some arm binds the payload as an i32 in
	// a sub-scope so the body's recursive expr might pick it up
	// — that's the path exercising payload-binding resolution
	// the wildcard-pattern Color match can't reach.
	if t != tOptI32 {
		opts := sc.inScope(tOptI32)
		if len(opts) > 0 {
			name := opts[g.ch.intN(len(opts))]
			bind := fmt.Sprintf("__opt_x%d", g.optBindCounter)
			g.optBindCounter++
			fmt.Fprintf(b, "(match (%s) { Some(%s) => ", name, bind)
			innerSome := newScope(sc)
			innerSome.declare(tI32, bind)
			g.expr(b, innerSome, t, depth+1)
			b.WriteString(", None => ")
			g.expr(b, sc, t, depth+1)
			b.WriteString(" })")
			return true
		}
	}
	// Result[i32, i32] match-with-binding. Both arms have an
	// i32 payload binding — exercises the two-armed-with-
	// payload match path that Option's single-payload variant
	// doesn't reach.
	if t != tResI32I32 {
		ress := sc.inScope(tResI32I32)
		if len(ress) > 0 {
			name := ress[g.ch.intN(len(ress))]
			okBind := fmt.Sprintf("__res_ok%d", g.optBindCounter)
			errBind := fmt.Sprintf("__res_err%d", g.optBindCounter)
			g.optBindCounter++
			fmt.Fprintf(b, "(match (%s) { Ok(%s) => ", name, okBind)
			innerOk := newScope(sc)
			innerOk.declare(tI32, okBind)
			g.expr(b, innerOk, t, depth+1)
			fmt.Fprintf(b, ", Err(%s) => ", errBind)
			innerErr := newScope(sc)
			innerErr.declare(tI32, errBind)
			g.expr(b, innerErr, t, depth+1)
			b.WriteString(" })")
			return true
		}
	}
	// Map methods. `m.get(k)` returns Option[i32], `m.has(k)` =>
	// bool, `m.len()` => i32. Each routes an in-scope Map into
	// the requested type.
	if t == tOptI32 {
		maps := sc.inScope(tMapI32I32)
		if len(maps) > 0 {
			name := maps[g.ch.intN(len(maps))]
			fmt.Fprintf(b, "%s.get(", name)
			g.expr(b, sc, tI32, depth+1)
			b.WriteByte(')')
			return true
		}
	}
	if t == tBool {
		maps := sc.inScope(tMapI32I32)
		if len(maps) > 0 {
			name := maps[g.ch.intN(len(maps))]
			fmt.Fprintf(b, "%s.has(", name)
			g.expr(b, sc, tI32, depth+1)
			b.WriteByte(')')
			return true
		}
	}
	if t == tI32 {
		maps := sc.inScope(tMapI32I32)
		if len(maps) > 0 {
			name := maps[g.ch.intN(len(maps))]
			// Map has `.len()` as a method, not via the builtin
			// `len()`. The latter is for string / array / slice
			// only — using it on a Map fails the checker.
			fmt.Fprintf(b, "%s.len()", name)
			return true
		}
	}
	// Dynamic-struct field access. For each declared struct
	// shape, see if any field's type matches t and there's a
	// var in scope of that struct's type. Iterate in
	// deterministic gtype-ascending order so the same seed
	// produces the same output (Go's `range map` is
	// randomised — see sortedDynamicTypes for the same fix
	// applied to pickType / pickMainVarType).
	structKeys := make([]gtype, 0, len(g.structShapes))
	for k := range g.structShapes {
		structKeys = append(structKeys, k)
	}
	sort.Slice(structKeys, func(i, j int) bool { return structKeys[i] < structKeys[j] })
	for _, st := range structKeys {
		sh := g.structShapes[st]
		vars := sc.inScope(st)
		if len(vars) == 0 {
			continue
		}
		// Collect matching field names. Random pick among them.
		var matching []string
		for _, f := range sh.fields {
			if f.t == t {
				matching = append(matching, f.name)
			}
		}
		if len(matching) == 0 {
			continue
		}
		vname := vars[g.ch.intN(len(vars))]
		fname := matching[g.ch.intN(len(matching))]
		fmt.Fprintf(b, "%s.%s", vname, fname)
		return true
	}
	// Dynamic-enum match. Route an in-scope dynamic enum into
	// any non-same-enum t via an exhaustive match. Each variant
	// arm recurses on t. Same determinism requirement as the
	// struct iteration above.
	enumKeys := make([]gtype, 0, len(g.enumShapes))
	for k := range g.enumShapes {
		enumKeys = append(enumKeys, k)
	}
	sort.Slice(enumKeys, func(i, j int) bool { return enumKeys[i] < enumKeys[j] })
	for _, et := range enumKeys {
		if et == t {
			continue
		}
		sh := g.enumShapes[et]
		vars := sc.inScope(et)
		if len(vars) == 0 {
			continue
		}
		vname := vars[g.ch.intN(len(vars))]
		fmt.Fprintf(b, "(match (%s) {", vname)
		for i, v := range sh.variants {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(b, " %s => ", v)
			g.expr(b, sc, t, depth+1)
		}
		b.WriteString(" })")
		return true
	}
	return false
}

// emitCall picks a previously-registered helper whose return type
// is t and emits a typed call to it. Half the time, when the
// helper has at least one parameter, emits the pipe form
// `(<arg0> |> helper(<arg1>, ...))` instead of the prefix form
// `helper(<arg0>, ...)`. Both lower to the same Call AST node at
// parse time (the parser desugars pipe to a Call with IsPipe set),
// so the two shapes exercise the same backend codepath — but the
// pipe form lets the generator's source resemble real-world
// data-first stdlib chains and exercises the pipe parser /
// formatter paths.
//
// Returns false (without writing) if no helper returns t; caller
// falls back to another production.
func (g *Generator) emitCall(b *strings.Builder, sc *scope, t gtype, depth int) bool {
	var cands []helperSig
	for _, h := range g.helpers {
		if h.retType == t {
			cands = append(cands, h)
		}
	}
	for _, h := range g.localHelpers {
		if h.retType == t {
			cands = append(cands, h)
		}
	}
	if len(cands) == 0 {
		return false
	}
	h := cands[g.ch.intN(len(cands))]
	// Pipe form: smaller-output choice is "no pipe" (exhaustion
	// returns true here = no pipe = prefix form).
	if len(h.params) >= 1 && !g.flip(0.6) {
		// Outer parens so the `|>` precedence (which sits
		// between assignment and ternary) doesn't clash with
		// whatever expression slot the caller drops this into.
		b.WriteByte('(')
		g.expr(b, sc, h.params[0], depth+1)
		fmt.Fprintf(b, " |> %s(", h.name)
		for i, pt := range h.params[1:] {
			if i > 0 {
				b.WriteString(", ")
			}
			g.expr(b, sc, pt, depth+1)
		}
		b.WriteString("))")
		return true
	}
	b.WriteString(h.name)
	b.WriteByte('(')
	for i, pt := range h.params {
		if i > 0 {
			b.WriteString(", ")
		}
		g.expr(b, sc, pt, depth+1)
	}
	b.WriteByte(')')
	return true
}

// genericArg emits an expression of type t for a generic-call
// argument slot. Same shape as `expr` for most types, but routes
// Option[i32] through `emitTypedOptI32` to avoid bare `None` —
// generic-arg unification can't disambiguate `None` (Option with
// no Args) against a sibling arg's `Option[i32]`, and the
// checker rejects with "argument N: expected T, got Option[i32]".
func (g *Generator) genericArg(b *strings.Builder, sc *scope, t gtype, depth int) {
	if t == tOptI32 {
		g.emitTypedOptI32(b, sc, depth)
		return
	}
	if t == tResI32I32 {
		g.emitTypedResI32I32(b, sc, depth)
		return
	}
	g.expr(b, sc, t, depth+1)
}

// emitTypedOptI32 emits an Option[i32] expression that the
// checker can resolve without surrounding context. Bare `None`
// is deliberately excluded — `None` is type-polymorphic, so in
// slots that don't carry a type annotation (the operand of `?`,
// some deeply nested arm positions) the checker can't infer
// the T and errors out with "malformed Option type Option".
// Picks an in-scope Option[i32] var (carries its declared type)
// or wraps a fresh `Some(<i32>)` (the i32 payload pins T).
func (g *Generator) emitTypedOptI32(b *strings.Builder, sc *scope, depth int) {
	opts := sc.inScope(tOptI32)
	if len(opts) > 0 && g.flip(0.6) {
		b.WriteString(opts[g.ch.intN(len(opts))])
		return
	}
	b.WriteString("(Some(")
	g.expr(b, sc, tI32, depth+1)
	b.WriteString("))")
}

// emitTypedResI32I32 emits a Result[i32, i32] expression that
// the checker can fully type without surrounding context.
// ALWAYS uses an in-scope Result var when one is available —
// bare `(Ok(x))` / `(Err(e))` only pins one of Result's two
// type parameters (T from Ok, E from Err) and the checker's
// generic-call inference doesn't flow surrounding-context
// type info back to fill in the missing one. The call's
// TypeArgs ends up as `[Result{no args}]` and monomorph
// produces a clone with bare Result-typed params. Skip the
// generic-call production entirely (via `skipGeneric` in
// `expr`) when no Result var is in scope.
//
// The bare-literal fallback at the bottom is reached only
// when called from contexts that DO carry full type info
// (var-init annotations, fn return slots) — the surrounding
// context makes the type unambiguous despite the variant
// constructor only fixing one of the two params.
func (g *Generator) emitTypedResI32I32(b *strings.Builder, sc *scope, depth int) {
	ress := sc.inScope(tResI32I32)
	if len(ress) > 0 {
		b.WriteString(ress[g.ch.intN(len(ress))])
		return
	}
	if g.flip(0.5) {
		b.WriteString("(Ok(")
	} else {
		b.WriteString("(Err(")
	}
	g.expr(b, sc, tI32, depth+1)
	b.WriteString("))")
}

// emitIfExpr writes `(if (<bool-expr>) { <expr-of-t> } else {
// <expr-of-t> })`. Outer parens make the result safe to drop into
// any expression slot regardless of precedence.
func (g *Generator) emitIfExpr(b *strings.Builder, sc *scope, t gtype, depth int) {
	b.WriteString("(if (")
	g.expr(b, sc, tBool, depth+1)
	b.WriteString(") { ")
	g.expr(b, sc, t, depth+1)
	b.WriteString(" } else { ")
	g.expr(b, sc, t, depth+1)
	b.WriteString(" })")
}

func (g *Generator) leaf(b *strings.Builder, sc *scope, t gtype, depth int) {
	vars := sc.inScope(t)
	if len(vars) > 0 && g.flip(0.6) {
		b.WriteString(vars[g.ch.intN(len(vars))])
		return
	}
	g.literal(b, sc, t, depth)
}

// numericExpr picks `+`, `-`, or `*` and recurses with operands of
// the same numeric type so the checker doesn't see a width mismatch.
// Division and modulo are skipped: division by zero traps and we
// want every emitted program to be runnable, not just type-correct.
//
// For i32 it sometimes produces the HIGH half of an i64 instead —
// see emitI64HighHalf.
func (g *Generator) numericExpr(b *strings.Builder, sc *scope, t gtype, depth int) {
	if t == tI32 && g.flip(0.15) {
		g.emitI64HighHalf(b, sc, depth)
		return
	}
	op := []string{"+", "-", "*"}[g.ch.intN(3)]
	b.WriteByte('(')
	g.expr(b, sc, t, depth+1)
	fmt.Fprintf(b, " %s ", op)
	g.expr(b, sc, t, depth+1)
	b.WriteByte(')')
}

// emitI64HighHalf writes `((<i64 expr> >> 32i64) as i32)`, routing the top
// 32 bits of an i64 down into the i32 return path.
//
// Without it the exit-code oracle cannot observe the high half of an i64 at
// all, which makes it blind to a whole class of miscompile. `main` returns a
// byte, and the two ways an i64 reaches that byte both discard the top half:
// a truncating `as i32` keeps the low word by definition, and a wrong
// sign-extension — the actual shape of the #5729 i64[] corruption — only ever
// rewrites bits 32..63, so the low 8 bits are identical either way. The one
// existing channel that does observe the high half is a comparison in
// boolExpr, and it needs a wide value to land on the specific operand that
// differs, which is rare enough to miss the bug entirely: reintroducing #5729
// and sweeping the exit-byte corpus through arm64-ssa changed ZERO of 150
// runnable seeds' exit codes, where the printable corpus (whole stdout, via
// `.to_string()`) diverged on 3 of 201.
//
// A right shift is the cheapest fix: it is total in Fern, needs no new type,
// and any wide value anywhere in the expression — including an `i64[]`
// element load — can now reach the returned byte.
//
// The operand prefers a value that has been through memory — an `i64[]`
// element, else an in-scope i64 var — over a fresh sub-expression, because a
// literal-only operand is constant-folded and so cannot expose a codegen bug
// no matter how wide it is. Observing a load is the whole point.
func (g *Generator) emitI64HighHalf(b *strings.Builder, sc *scope, depth int) {
	b.WriteString("(((")
	switch {
	case len(sc.inScope(tArrI64)) > 0:
		arrs := sc.inScope(tArrI64)
		fmt.Fprintf(b, "%s[0i32]", arrs[g.ch.intN(len(arrs))])
	case len(sc.inScope(tI64)) > 0:
		vars := sc.inScope(tI64)
		b.WriteString(vars[g.ch.intN(len(vars))])
	default:
		g.expr(b, sc, tI64, depth+1)
	}
	b.WriteString(") >> 32i64) as i32)")
}

// boolExpr picks one of: unary `!`, binary `&&`/`||` over booleans,
// or a numeric comparison whose operand type is drawn fresh.
func (g *Generator) boolExpr(b *strings.Builder, sc *scope, depth int) {
	switch g.ch.intN(4) {
	case 0:
		b.WriteString("(!")
		g.expr(b, sc, tBool, depth+1)
		b.WriteByte(')')
	case 1:
		op := []string{"&&", "||"}[g.ch.intN(2)]
		b.WriteByte('(')
		g.expr(b, sc, tBool, depth+1)
		fmt.Fprintf(b, " %s ", op)
		g.expr(b, sc, tBool, depth+1)
		b.WriteByte(')')
	default:
		nt := g.pickNumeric()
		op := []string{"<", "<=", ">", ">=", "==", "!="}[g.ch.intN(6)]
		b.WriteByte('(')
		g.expr(b, sc, nt, depth+1)
		fmt.Fprintf(b, " %s ", op)
		g.expr(b, sc, nt, depth+1)
		b.WriteByte(')')
	}
}

// literal emits a value-of-type-t in the simplest available
// form. Composite literals (array / Pair / Color) get scope +
// depth because they contain sub-expressions whose depth must
// extend the current cap (resetting to 0 would let a recursive
// composite — `i32[]` element that's another `i32[]` — blow the
// stack). Scalar literals ignore sc + depth.
func (g *Generator) literal(b *strings.Builder, sc *scope, t gtype, depth int) {
	switch t {
	case tI32:
		fmt.Fprintf(b, "%di32", g.ch.intN(1000))
	case tI64:
		// A quarter of i64 literals land ABOVE the int32 range, so the wide
		// half of the type is actually exercised. While every i64 literal was
		// drawn from 0..999, no generated program could tell an i64 from an
		// i32, and a backend that narrowed i64 values to 32 bits agreed with
		// the interpreter on every seed — which is exactly how the arm64-ssa
		// i64[] corruption fixed in #5729 slipped through the differential
		// oracle on all three backends.
		//
		// The three magnitudes are the ones that discriminate: just past
		// 2^31 (bit 31 set, so a sign-extending narrow reads negative),
		// 0xFFFFFFFF (a narrow reads -1), and past 2^32 (a truncating narrow
		// loses the top half outright).
		if g.flip(0.25) {
			switch g.ch.intN(3) {
			case 0:
				fmt.Fprintf(b, "%di64", 2147483648+int64(g.ch.intN(1000)))
			case 1:
				fmt.Fprintf(b, "%di64", 4294967295-int64(g.ch.intN(1000)))
			default:
				fmt.Fprintf(b, "%di64", 1099511627776+int64(g.ch.intN(1000)))
			}
			return
		}
		fmt.Fprintf(b, "%di64", g.ch.intN(1000))
	case tBool:
		if g.flip(0.5) {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case tF32:
		// Always include a decimal point so the lexer locks onto
		// the float production regardless of the suffix. Two-
		// decimal value drawn from chooser-supplied integer
		// bits — keeps the byte-driven RNG cleanly one byte per
		// choice instead of needing a Float64 escape hatch.
		fmt.Fprintf(b, "%.2ff32", float64(g.ch.intN(10000))/100.0)
	case tString:
		b.WriteString(g.stringLiteral())
	case tArrI32, tArrI64, tArrBool:
		elem, _ := arrayElemOf(t)
		g.arrayLiteral(b, sc, elem, depth)
	case tPair:
		g.pairLiteral(b, sc, depth)
	case tColor:
		b.WriteString([]string{"Red", "Green", "Blue"}[g.ch.intN(3)])
	case tStatus:
		b.WriteString([]string{"Active", "Inactive", "Pending"}[g.ch.intN(3)])
	case tXyz:
		// `(Xyz { n: <i32>, valid: <bool> })`. Outer parens
		// match the disambiguation pattern from pairLiteral.
		b.WriteString("(Xyz { n: ")
		g.expr(b, sc, tI32, depth+1)
		b.WriteString(", valid: ")
		g.expr(b, sc, tBool, depth+1)
		b.WriteString(" })")
	case tOptI32:
		// Exhaustion convention: smaller / more-terminating
		// branch is `None` (no sub-expression). `Some(...)` is
		// the recursive shape.
		if g.flip(0.5) {
			b.WriteString("None")
		} else {
			b.WriteString("(Some(")
			g.expr(b, sc, tI32, depth+1)
			b.WriteString("))")
		}
	case tMapI32I32:
		g.mapLiteral(b, sc, depth)
	case tResI32I32:
		// `(Ok(<i32>))` or `(Err(<i32>))`. Both arms carry an i32
		// payload so the result type is always Result[i32, i32]
		// (no inference ambiguity unlike bare `None`).
		if g.flip(0.5) {
			b.WriteString("(Ok(")
			g.expr(b, sc, tI32, depth+1)
			b.WriteString("))")
		} else {
			b.WriteString("(Err(")
			g.expr(b, sc, tI32, depth+1)
			b.WriteString("))")
		}
	case tTupI32I64:
		// `(<i32>, <i64>)` — heterogeneous on purpose, so the 4-byte
		// and 8-byte elements sit at different offsets and a backend
		// that assumed a uniform stride is caught.
		b.WriteString("(")
		g.expr(b, sc, tI32, depth+1)
		b.WriteString(", ")
		g.expr(b, sc, tI64, depth+1)
		b.WriteString(")")
	default:
		// Dynamic nominal types — struct lit or enum-variant
		// pick based on which sidecar map t belongs to.
		if sh, ok := g.structShapes[t]; ok {
			g.dynStructLiteral(b, sc, sh, depth)
			return
		}
		if sh, ok := g.enumShapes[t]; ok {
			b.WriteString(sh.variants[g.ch.intN(len(sh.variants))])
			return
		}
		panic(fmt.Sprintf("literal: unknown gtype %d", int(t)))
	}
}

// dynStructLiteral emits `(<Name> { f0: <expr>, f1: <expr>, ... })`
// for a dynamic struct shape declared in declareDynamicNominals.
// Outer parens match the pairLiteral / Xyz pattern for arm-block
// disambiguation.
func (g *Generator) dynStructLiteral(b *strings.Builder, sc *scope, sh *structShape, depth int) {
	fmt.Fprintf(b, "(%s { ", sh.name)
	for i, f := range sh.fields {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%s: ", f.name)
		g.expr(b, sc, f.t, depth+1)
	}
	b.WriteString(" })")
}

// mapLiteral emits `Map { k: v, k2: v2, ... }` with 0..3 entries.
// All keys + values are i32 expressions; the IR lowering for
// `Map[i32, i32]` covers every backend. Outer parens
// disambiguate when the literal lands in an arm-block slot.
func (g *Generator) mapLiteral(b *strings.Builder, sc *scope, depth int) {
	n := g.ch.intN(4) // 0..3 entries (empty Map is valid)
	b.WriteString("(Map { ")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		g.expr(b, sc, tI32, depth+1)
		b.WriteString(": ")
		g.expr(b, sc, tI32, depth+1)
	}
	b.WriteString(" })")
}

// arrayLiteral emits `[e1, e2, ...]` with at least one element so
// the fixed `[0i32]` index used by tryCompositeProduction is
// always in-bounds. Sub-expressions recurse at depth+1 so the
// outer MaxExprDepth budget keeps composite-of-composite
// recursion finite.
func (g *Generator) arrayLiteral(b *strings.Builder, sc *scope, elem gtype, depth int) {
	n := 1 + g.ch.intN(4) // 1..4 elements
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		g.expr(b, sc, elem, depth+1)
	}
	b.WriteByte(']')
}

// pairLiteral emits `(Pair { fst: <i32-expr>, snd: <i32-expr> })`.
// Outer parens disambiguate the brace-balanced struct literal in
// contexts where bare `Pair { ... }` would clash with the
// surrounding statement / arm-block braces (e.g. inside match-arm
// bodies or if-then arm expressions). Both field exprs recurse
// at depth+1 to extend the outer budget.
func (g *Generator) pairLiteral(b *strings.Builder, sc *scope, depth int) {
	b.WriteString("(Pair { fst: ")
	g.expr(b, sc, tI32, depth+1)
	b.WriteString(", snd: ")
	g.expr(b, sc, tI32, depth+1)
	b.WriteString(" })")
}

// stringLiteral emits a short ASCII-only string with no escape
// sequences so the lexer can't trip on quoting edge cases. Length is
// bounded so corpora stay compact.
func (g *Generator) stringLiteral() string {
	n := g.ch.intN(8)
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < n; i++ {
		sb.WriteByte(byte('a' + g.ch.intN(26)))
	}
	sb.WriteByte('"')
	return sb.String()
}

// ---------- random helpers ----------

// pickType draws from the full type universe, honouring the
// generator's profile (ProfileRunnable drops f32). The
// composite types (arrays, Pair, Color) are float-free, so
// they're allowed in both profiles.
func (g *Generator) pickType() gtype {
	pool := g.typePool()
	return pool[g.ch.intN(len(pool))]
}

// pickMainVarType is pickType's ProfileRunnable counterpart.
// Same shape as typePool's runnable branch (drops f32) and
// includes any dynamic nominal types declared in
// declareDynamicNominals so main's vars can hold values of
// user-declared struct / enum types.
func (g *Generator) pickMainVarType() gtype {
	pool := append([]gtype{}, mainVarTypes...)
	pool = append(pool, g.sortedDynamicTypes()...)
	return pool[g.ch.intN(len(pool))]
}

// typePool returns the gtype universe pickType draws from,
// branching on the generator's profile and including any
// dynamic nominal types declared by the current program.
// Allocates fresh each call so callers can mutate without
// aliasing the underlying slices.
func (g *Generator) typePool() []gtype {
	var pool []gtype
	if !g.profile.floatsAllowed() {
		pool = []gtype{
			tI32, tI64, tBool, tString,
			tArrI32, tArrI64, tArrBool, tPair, tColor, tOptI32,
			tXyz, tStatus, tMapI32I32, tResI32I32, tTupI32I64,
		}
	} else {
		pool = append(pool, allTypes[:]...)
	}
	pool = append(pool, g.sortedDynamicTypes()...)
	return pool
}

// sortedDynamicTypes returns the dynamic struct + enum gtypes
// in deterministic order (gtype-value ascending). Go's `range
// map` iteration order is randomised per program run, so
// iterating `g.structShapes` / `g.enumShapes` directly into
// the type pool makes pickType non-deterministic and breaks
// TestGenIsDeterministic for the same seed.
func (g *Generator) sortedDynamicTypes() []gtype {
	out := make([]gtype, 0, len(g.structShapes)+len(g.enumShapes))
	for t := range g.structShapes {
		out = append(out, t)
	}
	for t := range g.enumShapes {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// pickNumeric draws from the numeric types only, honouring
// the generator's profile. Used by `boolExpr` to choose the
// operand type of a comparison.
func (g *Generator) pickNumeric() gtype {
	if !g.profile.floatsAllowed() {
		ints := []gtype{tI32, tI64}
		return ints[g.ch.intN(len(ints))]
	}
	return []gtype{tI32, tI64, tF32}[g.ch.intN(3)]
}

func (g *Generator) flip(p float64) bool { return g.ch.flip(p) }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
