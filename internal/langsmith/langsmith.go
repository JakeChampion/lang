// Package langsmith generates syntactically- and type-correct Lang
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
package langsmith

import (
	"fmt"
	"math/rand/v2"
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

func (c *randChooser) intN(n int) int       { return c.rng.IntN(n) }
func (c *randChooser) flip(p float64) bool  { return c.rng.Float64() < p }

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
	// noFloats removes f32 from every production — type picker,
	// numeric expressions, and the operand-type chosen inside
	// boolean comparisons. MainProgram sets this so the
	// differential-execution oracle doesn't have to reason about
	// IEEE-754 edges (NaN propagation, denormal flush, Inf
	// comparison) that may legitimately differ across backends.
	noFloats bool
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
	numTypes
)

var allTypes = [numTypes]gtype{
	tI32, tI64, tBool, tF32, tString,
	tArrI32, tArrI64, tArrBool, tPair, tColor,
}

func (t gtype) String() string {
	switch t {
	case tI32:
		return "i32"
	case tI64:
		return "i64"
	case tBool:
		return "boolean"
	case tF32:
		return "f32"
	case tString:
		return "string"
	case tArrI32:
		return "i32[]"
	case tArrI64:
		return "i64[]"
	case tArrBool:
		return "boolean[]"
	case tPair:
		return "Pair"
	case tColor:
		return "Color"
	}
	panic(fmt.Sprintf("unknown gtype %d", int(t)))
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

// scope tracks identifiers visible at the current generation point,
// bucketed by gtype so picking "any variable of type T" is a
// constant-time lookup. Inner blocks would push a new scope; this
// slice doesn't need that yet (no nested blocks in v1) but the
// shape is in place for later.
type scope struct {
	parent *scope
	vars   [numTypes][]string
}

func newScope(parent *scope) *scope { return &scope{parent: parent} }

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
// recursion without a static check). Sets `noFloats` for the
// duration so nested productions can't sneak f32 in through
// boolean comparisons.
func (g *Generator) MainProgram() string {
	prevNoFloats := g.noFloats
	prevHelpers := g.helpers
	g.noFloats = true
	g.helpers = nil
	defer func() {
		g.noFloats = prevNoFloats
		g.helpers = prevHelpers
	}()

	var b strings.Builder
	g.preludeDecls(&b)

	// Emit a small number of helpers before main. Helpers in
	// main-program mode inherit noFloats, so their signatures are
	// drawn from {i32, i64, bool, string, i32[], i64[], boolean[],
	// Pair, Color}. Main only consumes i32-returning helpers in
	// its return expression, but non-i32 helpers still get
	// exercised: any helper can call any earlier helper of
	// matching arg/return types.
	nHelpers := g.ch.intN(maxInt(g.cfg.MaxFuncs, 1))
	for i := 0; i < nHelpers; i++ {
		g.funcDecl(&b, i)
	}

	sc := newScope(nil)
	b.WriteString("function main(): i32 { ")
	n := g.ch.intN(maxInt(g.cfg.MaxStmts, 0) + 1)
	for i := 0; i < n; i++ {
		if g.maybeEmitWhile(&b, sc) {
			continue
		}
		// Main's vars are drawn from the deterministic-across-
		// backends subset (no floats, no strings whose
		// observation needs a print channel). Composite types
		// (arrays / Pair / Color) are pure values that flow
		// through the i32 return path via index / field /
		// match-expr productions in expr(), so they're safe.
		vt := mainVarTypes[g.ch.intN(len(mainVarTypes))]
		vname := fmt.Sprintf("v%d", i)
		fmt.Fprintf(&b, "var %s: %s = ", vname, vt)
		g.expr(&b, sc, vt, 0)
		b.WriteString("; ")
		sc.declare(vt, vname)
	}
	b.WriteString("return (")
	g.expr(&b, sc, tI32, 0)
	b.WriteString(" & 255i32); }\n")
	return b.String()
}

// mainVarTypes are the gtypes legal for `var v<N>` declarations
// inside `main` under noFloats. Strings stay out — they'd
// need a print channel to observe — but composite types are
// fine because the i32 return path can extract bytes from them
// (array index, Pair.fst/.snd, match over Color).
var mainVarTypes = []gtype{
	tI32, tI64, tBool,
	tArrI32, tArrI64, tArrBool,
	tPair, tColor,
}

// preludeDecls emits the fixed `struct Pair` and `enum Color`
// declarations every generated program shares. Called at the top
// of Program / MainProgram before any function decl.
func (g *Generator) preludeDecls(b *strings.Builder) {
	b.WriteString("struct Pair { fst: i32, snd: i32 }\n")
	b.WriteString("enum Color { Red, Green, Blue }\n")
}

// Program emits a complete program: prelude type decls followed
// by N top-level function declarations. Helpers can call any
// earlier helper (forward refs only — see funcDecl).
func (g *Generator) Program() string {
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
		fmt.Fprintf(b, "%s: %s", pn, pt)
	}
	b.WriteByte(')')
	ret := g.pickType()
	fmt.Fprintf(b, ": %s { ", ret)
	g.body(b, sc, ret)
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
	n := g.ch.intN(maxInt(g.cfg.MaxStmts, 0) + 1)
	for i := 0; i < n; i++ {
		if g.maybeEmitWhile(b, sc) {
			continue
		}
		vt := g.pickType()
		vname := fmt.Sprintf("v%d", i)
		fmt.Fprintf(b, "var %s: %s = ", vname, vt)
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
		vt := g.pickType()
		vname := fmt.Sprintf("w%d_%d", idx, i)
		fmt.Fprintf(b, "var %s: %s = ", vname, vt)
		g.expr(b, inner, vt, 0)
		b.WriteString("; ")
		inner.declare(vt, vname)
	}

	fmt.Fprintf(b, "%s = %s + 1i32; ", counter, counter)
	b.WriteString("} ")
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
	switch t {
	case tI32, tI64, tF32:
		g.numericExpr(b, sc, t, depth)
	case tBool:
		g.boolExpr(b, sc, depth)
	default:
		// String + composite types — no binary / arithmetic
		// productions, fall through to leaf which handles var-
		// refs and literals (including the recursive array /
		// Pair literal forms).
		g.leaf(b, sc, t, depth)
	}
}

// tryCompositeProduction emits one of: array index access,
// struct field access, or enum match-expression — whichever
// kind of in-scope composite var can produce a value of type t.
// Tries them in fixed order (array, struct, enum) so a richer
// program seed naturally cascades through more shapes. Returns
// false (without writing) when no in-scope composite supplies
// t; caller falls back to another production.
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
	if t == tI32 {
		pairs := sc.inScope(tPair)
		if len(pairs) > 0 {
			name := pairs[g.ch.intN(len(pairs))]
			field := []string{"fst", "snd"}[g.ch.intN(2)]
			fmt.Fprintf(b, "%s.%s", name, field)
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
	return false
}

// emitCall picks a previously-registered helper whose return type
// is t and emits a typed call to it. Returns false (without
// writing) if no such helper exists; the caller should fall back
// to another production.
func (g *Generator) emitCall(b *strings.Builder, sc *scope, t gtype, depth int) bool {
	var cands []helperSig
	for _, h := range g.helpers {
		if h.retType == t {
			cands = append(cands, h)
		}
	}
	if len(cands) == 0 {
		return false
	}
	h := cands[g.ch.intN(len(cands))]
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
func (g *Generator) numericExpr(b *strings.Builder, sc *scope, t gtype, depth int) {
	op := []string{"+", "-", "*"}[g.ch.intN(3)]
	b.WriteByte('(')
	g.expr(b, sc, t, depth+1)
	fmt.Fprintf(b, " %s ", op)
	g.expr(b, sc, t, depth+1)
	b.WriteByte(')')
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
	}
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

// pickType draws from the full type universe, honouring noFloats
// when the generator is in main-program mode. The composite types
// (arrays, Pair, Color) are float-free, so they're allowed in
// both modes.
func (g *Generator) pickType() gtype {
	if g.noFloats {
		// Skip tF32 — everything else (scalar + composite) is
		// safe across backends for the differential oracle.
		nonFloats := []gtype{
			tI32, tI64, tBool, tString,
			tArrI32, tArrI64, tArrBool, tPair, tColor,
		}
		return nonFloats[g.ch.intN(len(nonFloats))]
	}
	return allTypes[g.ch.intN(len(allTypes))]
}

// pickNumeric draws from the numeric types only, honouring
// noFloats. Used by `boolExpr` to choose the operand type of a
// comparison.
func (g *Generator) pickNumeric() gtype {
	if g.noFloats {
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
