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
}

// DefaultConfig is what Gen uses.
func DefaultConfig() Config {
	return Config{
		MaxFuncs:     3,
		MaxParams:    4,
		MaxStmts:     6,
		MaxExprDepth: 4,
	}
}

// Gen returns a Lang program for the given seed. Output is
// deterministic in seed and is guaranteed to parse and type-check
// against the current compiler (within the subset the generator
// covers).
func Gen(seed uint64) string {
	return NewWithConfig(rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15)), DefaultConfig()).Program()
}

// New constructs a generator with default limits.
func New(rng *rand.Rand) *Generator { return NewWithConfig(rng, DefaultConfig()) }

// NewWithConfig is New with a caller-supplied tuning.
func NewWithConfig(rng *rand.Rand, cfg Config) *Generator {
	return &Generator{rng: rng, cfg: cfg}
}

// Generator emits source text directly while tracking an in-scope
// set of typed identifiers. Each expression production picks
// operands whose types match the context so the result type-checks
// by construction.
type Generator struct {
	rng *rand.Rand
	cfg Config
	// noFloats removes f32 from every production — type picker,
	// numeric expressions, and the operand-type chosen inside
	// boolean comparisons. MainProgram sets this so the
	// differential-execution oracle doesn't have to reason about
	// IEEE-754 edges (NaN propagation, denormal flush, Inf
	// comparison) that may legitimately differ across backends.
	noFloats bool
}

// gtype is the generator's internal enum of Lang types. Each kind
// has its own literal form, applicable operators, and per-type
// bucket inside scope.
type gtype int

const (
	tI32 gtype = iota
	tI64
	tBool
	tF32
	tString
	numTypes
)

var allTypes = [numTypes]gtype{tI32, tI64, tBool, tF32, tString}

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
	}
	panic(fmt.Sprintf("unknown gtype %d", int(t)))
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
func GenMain(seed uint64) string {
	return New(rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))).MainProgram()
}

// MainProgram emits a single `function main(): i32 { ... }`. Body
// declares 0..N integer/boolean vars (which give the differential
// harness something to disagree on across backends) and returns
// `(<i32-expr> & 255i32)`. Sets `noFloats` for the duration of
// the call so nested productions can't sneak f32 in through
// boolean comparisons.
func (g *Generator) MainProgram() string {
	prevNoFloats := g.noFloats
	g.noFloats = true
	defer func() { g.noFloats = prevNoFloats }()

	var b strings.Builder
	sc := newScope(nil)
	b.WriteString("function main(): i32 { ")
	n := g.rng.IntN(maxInt(g.cfg.MaxStmts, 0) + 1)
	for i := 0; i < n; i++ {
		// Restrict to deterministic-across-backends types. Strings
		// would also need a print channel to observe; we just want
		// the return-value byte.
		vt := []gtype{tI32, tI64, tBool}[g.rng.IntN(3)]
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

// Program emits a complete program with N top-level function
// declarations. Each function is independent: no inter-function
// calls in v1.
func (g *Generator) Program() string {
	var b strings.Builder
	n := 1 + g.rng.IntN(maxInt(g.cfg.MaxFuncs, 1))
	for i := 0; i < n; i++ {
		g.funcDecl(&b, i)
	}
	return b.String()
}

func (g *Generator) funcDecl(b *strings.Builder, idx int) {
	sc := newScope(nil)
	nParams := g.rng.IntN(maxInt(g.cfg.MaxParams, 0) + 1)
	fmt.Fprintf(b, "function gen_f%d(", idx)
	for i := 0; i < nParams; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		pt := g.pickType()
		pn := fmt.Sprintf("p%d", i)
		sc.declare(pt, pn)
		fmt.Fprintf(b, "%s: %s", pn, pt)
	}
	b.WriteByte(')')
	ret := g.pickType()
	fmt.Fprintf(b, ": %s { ", ret)
	g.body(b, sc, ret)
	b.WriteString("}\n")
}

// body emits a sequence of `var` declarations followed by a typed
// `return`. Each `var` adds a fresh name to the scope so later
// statements can reference it.
func (g *Generator) body(b *strings.Builder, sc *scope, retT gtype) {
	n := g.rng.IntN(maxInt(g.cfg.MaxStmts, 0) + 1)
	for i := 0; i < n; i++ {
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

// ---------- expressions ----------

// expr emits a well-typed expression of type t. Depth is the
// current recursion level; once it exceeds cfg.MaxExprDepth, only
// leaf productions (literals + variable references) are emitted so
// the tree always terminates.
func (g *Generator) expr(b *strings.Builder, sc *scope, t gtype, depth int) {
	if depth >= g.cfg.MaxExprDepth || g.flip(0.4) {
		g.leaf(b, sc, t)
		return
	}
	switch t {
	case tI32, tI64, tF32:
		g.numericExpr(b, sc, t, depth)
	case tBool:
		g.boolExpr(b, sc, depth)
	case tString:
		// No string-producing binary op in v1 — concat lowers
		// through a checker-stamped helper and complicates the
		// invariant. Stick to leaves.
		g.leaf(b, sc, t)
	}
}

func (g *Generator) leaf(b *strings.Builder, sc *scope, t gtype) {
	vars := sc.inScope(t)
	if len(vars) > 0 && g.flip(0.6) {
		b.WriteString(vars[g.rng.IntN(len(vars))])
		return
	}
	g.literal(b, t)
}

// numericExpr picks `+`, `-`, or `*` and recurses with operands of
// the same numeric type so the checker doesn't see a width mismatch.
// Division and modulo are skipped: division by zero traps and we
// want every emitted program to be runnable, not just type-correct.
func (g *Generator) numericExpr(b *strings.Builder, sc *scope, t gtype, depth int) {
	op := []string{"+", "-", "*"}[g.rng.IntN(3)]
	b.WriteByte('(')
	g.expr(b, sc, t, depth+1)
	fmt.Fprintf(b, " %s ", op)
	g.expr(b, sc, t, depth+1)
	b.WriteByte(')')
}

// boolExpr picks one of: unary `!`, binary `&&`/`||` over booleans,
// or a numeric comparison whose operand type is drawn fresh.
func (g *Generator) boolExpr(b *strings.Builder, sc *scope, depth int) {
	switch g.rng.IntN(4) {
	case 0:
		b.WriteString("(!")
		g.expr(b, sc, tBool, depth+1)
		b.WriteByte(')')
	case 1:
		op := []string{"&&", "||"}[g.rng.IntN(2)]
		b.WriteByte('(')
		g.expr(b, sc, tBool, depth+1)
		fmt.Fprintf(b, " %s ", op)
		g.expr(b, sc, tBool, depth+1)
		b.WriteByte(')')
	default:
		nt := g.pickNumeric()
		op := []string{"<", "<=", ">", ">=", "==", "!="}[g.rng.IntN(6)]
		b.WriteByte('(')
		g.expr(b, sc, nt, depth+1)
		fmt.Fprintf(b, " %s ", op)
		g.expr(b, sc, nt, depth+1)
		b.WriteByte(')')
	}
}

func (g *Generator) literal(b *strings.Builder, t gtype) {
	switch t {
	case tI32:
		fmt.Fprintf(b, "%di32", g.rng.IntN(1000))
	case tI64:
		fmt.Fprintf(b, "%di64", g.rng.IntN(1000))
	case tBool:
		if g.flip(0.5) {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case tF32:
		// Always include a decimal point so the lexer locks onto
		// the float production regardless of the suffix.
		fmt.Fprintf(b, "%.2ff32", g.rng.Float64()*100)
	case tString:
		b.WriteString(g.stringLiteral())
	}
}

// stringLiteral emits a short ASCII-only string with no escape
// sequences so the lexer can't trip on quoting edge cases. Length is
// bounded so corpora stay compact.
func (g *Generator) stringLiteral() string {
	n := g.rng.IntN(8)
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < n; i++ {
		sb.WriteByte(byte('a' + g.rng.IntN(26)))
	}
	sb.WriteByte('"')
	return sb.String()
}

// ---------- random helpers ----------

// pickType draws from the full type universe, honouring noFloats
// when the generator is in main-program mode.
func (g *Generator) pickType() gtype {
	if g.noFloats {
		// Skip tF32 — the type universe has it at index tF32.
		// Round-robin over the other four entries instead.
		nonFloats := []gtype{tI32, tI64, tBool, tString}
		return nonFloats[g.rng.IntN(len(nonFloats))]
	}
	return allTypes[g.rng.IntN(len(allTypes))]
}

// pickNumeric draws from the numeric types only, honouring
// noFloats. Used by `boolExpr` to choose the operand type of a
// comparison.
func (g *Generator) pickNumeric() gtype {
	if g.noFloats {
		ints := []gtype{tI32, tI64}
		return ints[g.rng.IntN(len(ints))]
	}
	return []gtype{tI32, tI64, tF32}[g.rng.IntN(3)]
}

func (g *Generator) flip(p float64) bool { return g.rng.Float64() < p }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
