package fernsmith

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
)

type Config struct {
	MaxFuncs     int
	MaxParams    int
	MaxStmts     int
	MaxExprDepth int
	MaxLoopDepth int
	MaxLoopIters int
}

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

func Gen(seed uint64) string {
	return newRandGen(seed, DefaultConfig()).Program()
}

func GenBytes(data []byte) string {
	return newByteGen(data, DefaultConfig()).Program()
}

func GenMainBytes(data []byte) string {
	return newByteGen(data, DefaultConfig()).MainProgram()
}

func GenPrintableMain(seed uint64) string {
	return newRandGen(seed, DefaultConfig()).MainPrintableProgram()
}

func GenPrintableMainBytes(data []byte) string {
	return newByteGen(data, DefaultConfig()).MainPrintableProgram()
}

func newRandGen(seed uint64, cfg Config) *Generator {
	return &Generator{ch: &randChooser{rng: seedRNG(seed)}, cfg: cfg}
}

func seedRNG(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
}

// MainBytes is the corpus GenMainBytes turns into GenMain(seed)'s program, so
// a failing seed can be shrunk with ShrinkMainBytes.
func MainBytes(seed uint64) ([]byte, error) {
	c := &recordChooser{rng: seedRNG(seed)}
	(&Generator{ch: c, cfg: DefaultConfig()}).MainProgram()
	if c.err != nil {
		return nil, c.err
	}
	// The trailing byte keeps byteChooser from reporting itself exhausted
	// after the last choice, which randChooser never does.
	return append(c.out, 0), nil
}

func newByteGen(data []byte, cfg Config) *Generator {
	return &Generator{ch: &byteChooser{data: data}, cfg: cfg}
}

type chooser interface {
	intN(n int) int
	flip(p float64) bool
	exhausted() bool
}

type randChooser struct{ rng *rand.Rand }

func (c *randChooser) intN(n int) int      { return c.rng.IntN(n) }
func (c *randChooser) flip(p float64) bool { return c.rng.Float64() < p }
func (c *randChooser) exhausted() bool     { return false }

// recordChooser makes randChooser's choices and writes each as the bytes
// byteChooser reads back as the same choice.
type recordChooser struct {
	rng *rand.Rand
	out []byte
	err error
}

func (c *recordChooser) intN(n int) int {
	v := c.rng.IntN(n)
	switch {
	case n <= 1:
	case n <= 256:
		c.out = append(c.out, byte(v))
	case n <= 1<<16:
		c.out = append(c.out, byte(v>>8), byte(v))
	case c.err == nil:
		c.err = fmt.Errorf("intN(%d) has no byte encoding", n)
	}
	return v
}

func (c *recordChooser) flip(p float64) bool {
	v := c.rng.Float64() < p
	b := 0.0
	if !v {
		b = math.Ceil(p * 256)
	}
	if b > 255 && c.err == nil {
		c.err = fmt.Errorf("flip(%v) false has no byte encoding", p)
	}
	c.out = append(c.out, byte(b))
	return v
}

func (c *recordChooser) exhausted() bool { return false }

type byteChooser struct {
	data []byte
	pos  int
}

func (c *byteChooser) exhausted() bool { return c.pos >= len(c.data) }

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
	v := int(b)
	if n > 256 {
		if lo, ok := c.next(); ok {
			v = v<<8 | int(lo)
		}
	}
	return v % n
}

func (c *byteChooser) flip(p float64) bool {
	b, ok := c.next()
	if !ok {
		return true
	}
	return float64(b)/256.0 < p
}

type Profile int

const (
	ProfileFree Profile = iota
	ProfileRunnable
	ProfilePrintable
)

func (p Profile) floatsAllowed() bool { return p == ProfileFree || p == ProfilePrintable }

type Generator struct {
	ch                chooser
	cfg               Config
	spans             []span
	exprs             []exprSpan
	profile           Profile
	helpers           []helperSig
	loopDepth         int
	loopCounter       int
	currentReturnType gtype
	ctrlCounter       int
	optBindCounter    int
	localHelpers      []helperSig
	localFnCounter    int
	floatAggCounter   int
	cowCounter        int
	lambdaCounter     int
	structShapes      map[gtype]*structShape
	enumShapes        map[gtype]*enumShape
	arrShapes         map[gtype]gtype
	nextNominal       gtype
	ownCounter        int
	cloCounter        int
	self              *helperSig
	selfCalls         int
	inClosure         int
	feats             uint64
	types             uint64
	trait             *traitShape
	dynCounter        int
	generics          bool
	genCounter        int
	ownRecv           bool
	ownRecvCounter    int
}

type structShape struct {
	name   string
	fields []structField
}

type structField struct {
	name string
	t    gtype
}

type enumShape struct {
	name     string
	variants []enumVariant
}

type enumVariant struct {
	name    string
	payload []gtype
}

type helperSig struct {
	name    string
	params  []gtype
	retType gtype
	fueled  bool
}

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
	tXyz
	tStatus
	tResI32I32
	tTupI32I64
	tU32
	tU64
	tF64
	tU8
	tFnI32I32
	tArrFnI32I32
	tArrF32
	tArrF64
	tTupF32F64
	tVec2
	tFShape
	numTypes
)

var allTypes = [numTypes]gtype{
	tI32, tI64, tBool, tF32, tString,
	tArrI32, tArrI64, tArrBool, tPair, tColor, tOptI32, tMapI32I32,
	tXyz, tStatus, tResI32I32, tTupI32I64, tU32, tU64, tF64, tU8,
	tFnI32I32, tArrFnI32I32,
	tArrF32, tArrF64, tTupF32F64, tVec2, tFShape,
}

var gtypeNames = [numTypes]string{
	tI32:         "i32",
	tI64:         "i64",
	tBool:        "boolean",
	tF32:         "f32",
	tString:      "string",
	tArrI32:      "i32[]",
	tArrI64:      "i64[]",
	tArrBool:     "boolean[]",
	tPair:        "Pair",
	tColor:       "Color",
	tOptI32:      "Option[i32]",
	tMapI32I32:   "Map[i32, i32]",
	tXyz:         "Xyz",
	tStatus:      "Status",
	tResI32I32:   "Result[i32, i32]",
	tTupI32I64:   "(i32, i64)",
	tU32:         "u32",
	tU64:         "u64",
	tF64:         "f64",
	tU8:          "u8",
	tFnI32I32:    "(i32) => i32",
	tArrFnI32I32: "((i32) => i32)[]",
	tArrF32:      "f32[]",
	tArrF64:      "f64[]",
	tTupF32F64:   "(f32, f64)",
	tVec2:        "Vec2",
	tFShape:      "FShape",
}

func (t gtype) String() string {
	if t >= 0 && int(t) < len(gtypeNames) {
		return gtypeNames[t]
	}
	return fmt.Sprintf("?dyn%d", int(t))
}

func (g *Generator) maybeTypeArg(b *strings.Builder, t gtype) {
	name := g.typeName(t)
	if g.flip(0.75) || g.ch.exhausted() || strings.HasPrefix(name, "?") {
		return
	}
	b.WriteString("[" + name + "]")
}

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
	if elem, ok := g.arrShapes[t]; ok {
		return g.typeName(elem) + "[]"
	}
	return fmt.Sprintf("?gtype%d", int(t))
}

func (g *Generator) arrayTypeFor(t gtype) (gtype, bool) {
	for _, at := range sortedKeys(g.arrShapes) {
		if g.arrShapes[at] == t {
			return at, true
		}
	}
	switch t {
	case tI32:
		return tArrI32, true
	case tI64:
		return tArrI64, true
	case tBool:
		return tArrBool, true
	case tFnI32I32:
		return tArrFnI32I32, true
	case tF32:
		return tArrF32, true
	case tF64:
		return tArrF64, true
	}
	return 0, false
}

func (g *Generator) arrayElemOf(t gtype) (gtype, bool) {
	if elem, ok := g.arrShapes[t]; ok {
		return elem, true
	}
	switch t {
	case tArrI32:
		return tI32, true
	case tArrI64:
		return tI64, true
	case tArrBool:
		return tBool, true
	case tArrFnI32I32:
		return tFnI32I32, true
	case tArrF32:
		return tF32, true
	case tArrF64:
		return tF64, true
	}
	return 0, false
}

type scope struct {
	parent *scope
	vars   map[gtype][]string
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, vars: map[gtype][]string{}}
}

func (s *scope) declare(t gtype, name string) { s.vars[t] = append(s.vars[t], name) }

func (s *scope) inScope(t gtype) []string {
	var out []string
	for cur := s; cur != nil; cur = cur.parent {
		out = append(out, cur.vars[t]...)
	}
	return out
}

func GenMain(seed uint64) string {
	return newRandGen(seed, DefaultConfig()).MainProgram()
}

func (g *Generator) MainProgram() string {
	g.profile = ProfileRunnable
	g.drawSwarm()

	var b strings.Builder
	g.preludeDecls(&b)

	nHelpers, helpersAt := 0, 0
	if g.on(featHelpers) {
		nHelpers, helpersAt = g.listCount(maxInt(g.cfg.MaxFuncs, 1))
	}
	for i := 0; i < nHelpers; i++ {
		start := g.pos()
		g.funcDecl(&b, i)
		g.markSpan(helpersAt, start)
	}

	sc := newScope(nil)
	b.WriteString("function main(): i32 { ")
	g.currentReturnType = tI32
	n, stmtsAt := g.listCount(maxInt(g.cfg.MaxStmts, 0) + 1)
	for i := 0; i < n; i++ {
		start := g.pos()
		g.mainStmt(&b, sc, i)
		g.markSpan(stmtsAt, start)
	}
	b.WriteString("return (")
	g.expr(&b, sc, tI32, 0)
	if !g.ch.exhausted() {
		b.WriteString(" + ")
		g.emitStateFold(&b, sc)
	}
	b.WriteString(" & 255i32); }\n")
	return b.String()
}

func (g *Generator) mainStmt(b *strings.Builder, sc *scope, i int) {
	if g.maybeEmitWhile(b, sc) || g.maybeEmitForEach(b, sc) || g.maybeEmitLocalFn(b, sc) ||
		g.maybeEmitArrayAliasCow(b, sc) || g.maybeEmitLoopReadBeforeWith(b, sc) || g.maybeEmitOwnershipCluster(b, sc) || g.maybeEmitClosureCluster(b, sc) ||
		g.maybeEmitTraitCluster(b, sc) || g.maybeEmitGenericCluster(b, sc) || g.maybeEmitOwnRecvCluster(b, sc) ||
		g.maybeEmitControlStmt(b, sc, tI32) {
		return
	}
	vt := g.pickMainVarType()
	vname := fmt.Sprintf("v%d", i)
	fmt.Fprintf(b, "let %s: %s = ", vname, g.typeName(vt))
	g.expr(b, sc, vt, 0)
	b.WriteString("; ")
	sc.declare(vt, vname)
}

func (g *Generator) MainPrintableProgram() string {
	g.profile = ProfilePrintable
	g.drawSwarm()

	var b strings.Builder
	g.preludeDecls(&b)

	nHelpers := 0
	if g.on(featHelpers) {
		nHelpers = g.ch.intN(maxInt(g.cfg.MaxFuncs, 1))
	}
	for i := 0; i < nHelpers; i++ {
		g.funcDecl(&b, i)
	}

	sc := newScope(nil)
	b.WriteString("function main(): i32 { ")
	g.currentReturnType = tI32

	b.WriteString("let __fz: f32 = 0.0f32; let __fo: f32 = 1.0f32; ")

	n := g.ch.intN(maxInt(g.cfg.MaxStmts, 0) + 1)
	for i := 0; i < n; i++ {
		if g.maybeEmitWhile(&b, sc) {
			continue
		}
		if g.maybeEmitForEach(&b, sc) {
			continue
		}
		if g.maybeEmitArrayAliasCow(&b, sc) {
			continue
		}
		if g.maybeEmitLoopReadBeforeWith(&b, sc) {
			continue
		}
		if g.maybeEmitOwnershipCluster(&b, sc) {
			continue
		}
		if g.maybeEmitClosureCluster(&b, sc) {
			continue
		}
		if g.maybeEmitControlStmt(&b, sc, tI32) {
			continue
		}
		vt := g.pickMainVarType()
		vname := fmt.Sprintf("v%d", i)
		fmt.Fprintf(&b, "let %s: %s = ", vname, g.typeName(vt))
		g.expr(&b, sc, vt, 0)
		b.WriteString("; ")
		sc.declare(vt, vname)
	}

	g.emitFloatAggregateObservation(&b, sc)
	g.emitFloatSpecialObservation(&b, sc)
	nObs := g.ch.intN(maxInt(g.cfg.MaxStmts, 1))
	for i := 0; i < nObs; i++ {
		g.emitPrintObservation(&b, sc)
	}

	if !g.ch.exhausted() {
		b.WriteString("print((")
		g.emitStateFold(&b, sc)
		b.WriteString(").to_string()); ")
	}
	b.WriteString("return 0; }\n")
	return b.String()
}

func (g *Generator) emitPrintObservation(b *strings.Builder, sc *scope) {
	switch g.ch.intN(9) {
	case 0:
		b.WriteString("print(")
		g.expr(b, sc, tString, 0)
		b.WriteString("); ")
	case 1:
		b.WriteString("print((")
		g.expr(b, sc, tI64, 0)
		b.WriteString(").to_string()); ")
	case 2:
		b.WriteString("print(if (")
		g.expr(b, sc, tBool, 0)
		b.WriteString(") { \"T\" } else { \"F\" }); ")
	case 3:
		b.WriteString("print((")
		g.expr(b, sc, tI32, 0)
		b.WriteString(").to_string()); ")
	case 4:
		b.WriteString("print(((")
		g.expr(b, sc, tF32, 0)
		b.WriteString(") as i32).to_string()); ")
	case 7:
		b.WriteString("print((")
		g.expr(b, sc, tU64, 0)
		b.WriteString(").to_string()); ")
	case 8:
		b.WriteString("print(((")
		g.expr(b, sc, tF64, 0)
		b.WriteString(") as i32).to_string()); ")
	case 6:
		b.WriteString("print((")
		g.expr(b, sc, tU32, 0)
		b.WriteString(").to_string()); ")
	default:
		g.emitFloatSpecialObservation(b, sc)
	}
}

const floatAggArrayLen = 3

func (g *Generator) emitFloatAggregateObservation(b *strings.Builder, sc *scope) {
	idx := g.floatAggCounter
	g.floatAggCounter++
	name := fmt.Sprintf("__fag%d", idx)
	d := maxInt(g.cfg.MaxExprDepth-1, 0)
	switch g.ch.intN(5) {
	case 1:
		g.emitFloatArrayObservation(b, sc, name, tArrF32, d)
	case 2:
		g.emitFloatArrayObservation(b, sc, name, tArrF64, d)
	case 3:
		fmt.Fprintf(b, "let %s: (f32, f64) = (", name)
		g.expr(b, sc, tF32, d)
		b.WriteString(", ")
		g.expr(b, sc, tF64, d)
		b.WriteString("); ")
		sc.declare(tTupF32F64, name)
		g.printFloatAsI32(b, name+".0")
		g.printFloatAsI32(b, name+".1")
	case 4:
		if g.flip(0.5) {
			fmt.Fprintf(b, "let %s: FShape = (FTwo(", name)
			g.expr(b, sc, tF32, d)
			b.WriteString(", ")
			g.expr(b, sc, tF32, d)
		} else {
			fmt.Fprintf(b, "let %s: FShape = (FWide(", name)
			g.expr(b, sc, tF64, d)
			b.WriteString(", ")
			g.expr(b, sc, tF64, d)
		}
		b.WriteString(")); ")
		sc.declare(tFShape, name)
		for slot := 0; slot < 2; slot++ {
			narrowA := fmt.Sprintf("__fs_a%d_%d", idx, slot)
			narrowB := fmt.Sprintf("__fs_b%d_%d", idx, slot)
			wideA := fmt.Sprintf("__fs_c%d_%d", idx, slot)
			wideB := fmt.Sprintf("__fs_d%d_%d", idx, slot)
			narrowRead, wideRead := narrowA, wideA
			if slot == 1 {
				narrowRead, wideRead = narrowB, wideB
			}
			fmt.Fprintf(b,
				"print((match (%s) { FNone => 0i32, FTwo(%s, %s) => ((%s) as i32), FWide(%s, %s) => ((%s) as i32) }).to_string()); ",
				name, narrowA, narrowB, narrowRead, wideA, wideB, wideRead)
		}
	default:
		fmt.Fprintf(b, "let %s: Vec2 = (Vec2 { x: ", name)
		g.expr(b, sc, tF32, d)
		b.WriteString(", y: ")
		g.expr(b, sc, tF32, d)
		b.WriteString(" }); ")
		sc.declare(tVec2, name)
		g.printFloatAsI32(b, name+".x")
		g.printFloatAsI32(b, name+".y")
	}
}

func (g *Generator) emitFloatArrayObservation(b *strings.Builder, sc *scope, name string, arrT gtype, depth int) {
	elem, _ := g.arrayElemOf(arrT)
	fmt.Fprintf(b, "let %s: %s = [", name, g.typeName(arrT))
	for i := 0; i < floatAggArrayLen; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		g.expr(b, sc, elem, depth)
	}
	b.WriteString("]; ")
	sc.declare(arrT, name)
	for i := 0; i < floatAggArrayLen; i++ {
		g.printFloatAsI32(b, fmt.Sprintf("%s[%di32]", name, i))
	}
}

func (g *Generator) printFloatAsI32(b *strings.Builder, expr string) {
	fmt.Fprintf(b, "print(((%s) as i32).to_string()); ", expr)
}

func (g *Generator) emitFloatSpecialObservation(b *strings.Builder, sc *scope) {
	op := []string{"<", "<=", ">", ">=", "==", "!="}[g.ch.intN(6)]
	b.WriteString("print(if ((")
	g.emitSpecialFloatValue(b)
	fmt.Fprintf(b, ") %s (", op)
	g.emitFloatComparand(b, sc)
	b.WriteString(")) { \"T\" } else { \"F\" }); ")
}

func (g *Generator) emitSpecialFloatValue(b *strings.Builder) {
	switch g.ch.intN(3) {
	case 1:
		b.WriteString("(__fo / __fz)")
	case 2:
		b.WriteString("((__fz - __fo) / __fz)")
	default:
		b.WriteString("(__fz / __fz)")
	}
}

func (g *Generator) emitFloatComparand(b *strings.Builder, sc *scope) {
	if g.ch.intN(4) == 0 {
		g.expr(b, sc, tF32, 1)
		return
	}
	g.emitSpecialFloatValue(b)
}

var mainVarTypes = []gtype{
	tI32, tI64, tU32, tU64, tU8, tBool, tString,
	tArrI32, tArrI64, tArrBool,
	tPair, tColor, tOptI32,
	tXyz, tStatus, tResI32I32, tTupI32I64,
	tFnI32I32, tArrFnI32I32,
	tMapI32I32,
}

var floatVarTypes = []gtype{
	tF32, tF64,
	tArrF32, tArrF64,
	tTupF32F64, tVec2, tFShape,
}

func (g *Generator) preludeDecls(b *strings.Builder) {
	b.WriteString("import \"std/i32\";\n")
	b.WriteString("import \"std/i64\";\n")
	b.WriteString("import \"std/u32\";\n")
	b.WriteString("import \"std/u64\";\n")
	b.WriteString("import \"std/string\";\n")
	b.WriteString("import \"std/array\";\n")
	b.WriteString("import \"core/int\";\n")
	b.WriteString("import \"core/map\";\n")
	b.WriteString("struct Pair { fst: i32, snd: i32 }\n")
	b.WriteString("struct Xyz { n: i32, valid: boolean }\n")
	b.WriteString("enum Color { Red, Green, Blue }\n")
	b.WriteString("enum Status { Active, Inactive, Pending }\n")
	b.WriteString("function (p: Pair) sum(): i32 { return (p.fst + p.snd); }\n")
	b.WriteString("function (p: Pair) swap(): Pair { return (Pair { fst: p.snd, snd: p.fst }); }\n")
	b.WriteString("function id[T](x: T): T { return x; }\n")
	b.WriteString("function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }\n")
	if g.profile.floatsAllowed() {
		b.WriteString("struct Vec2 { x: f32, y: f32 }\n")
		b.WriteString("enum FShape { FNone, FTwo(f32, f32), FWide(f64, f64) }\n")
	}
	g.declareDynamicNominals(b)
	g.declareTrait(b)
	g.declareGenerics(b)
	g.declareOwnRecv(b)
}

func (g *Generator) declareDynamicNominals(b *strings.Builder) {
	if g.structShapes == nil {
		g.structShapes = map[gtype]*structShape{}
	}
	if g.enumShapes == nil {
		g.enumShapes = map[gtype]*enumShape{}
	}
	if g.arrShapes == nil {
		g.arrShapes = map[gtype]gtype{}
	}
	if !g.on(featNominals) {
		return
	}
	if g.nextNominal < numTypes {
		g.nextNominal = numTypes
	}
	nStructs := g.ch.intN(4)
	for i := 0; i < nStructs; i++ {
		name := fmt.Sprintf("S%d", i)
		nFields := 2 + g.ch.intN(3)
		fields := make([]structField, nFields)
		fmt.Fprintf(b, "struct %s { ", name)
		for j := 0; j < nFields; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			ft := g.pickFieldType()
			fname := fmt.Sprintf("f%d", j)
			fields[j] = structField{name: fname, t: ft}
			fmt.Fprintf(b, "%s: %s", fname, g.typeName(ft))
		}
		b.WriteString(" }\n")
		gt := g.nextNominal
		g.nextNominal++
		g.structShapes[gt] = &structShape{name: name, fields: fields}
	}
	nEnums := g.ch.intN(3)
	for i := 0; i < nEnums; i++ {
		name := fmt.Sprintf("E%d", i)
		nVariants := 2 + g.ch.intN(3)
		variants := make([]enumVariant, nVariants)
		fmt.Fprintf(b, "enum %s { ", name)
		for j := 0; j < nVariants; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			vname := fmt.Sprintf("__E%d_V%d", i, j)
			b.WriteString(vname)
			var payload []gtype
			if !g.flip(0.55) {
				nSlots := 1 + g.ch.intN(2)
				payload = make([]gtype, nSlots)
				b.WriteByte('(')
				for k := 0; k < nSlots; k++ {
					if k > 0 {
						b.WriteString(", ")
					}
					payload[k] = g.pickFieldType()
					b.WriteString(g.typeName(payload[k]))
				}
				b.WriteByte(')')
			}
			variants[j] = enumVariant{name: vname, payload: payload}
		}
		b.WriteString(" }\n")
		gt := g.nextNominal
		g.nextNominal++
		g.enumShapes[gt] = &enumShape{name: name, variants: variants}
	}
	for _, elem := range append(sortedKeys(g.structShapes), sortedKeys(g.enumShapes)...) {
		if !g.on(featNested) || g.flip(0.6) {
			continue
		}
		g.arrShapes[g.nextNominal] = elem
		g.nextNominal++
	}
}

func (g *Generator) Program() string {
	g.drawSwarm()
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
	fueled := g.on(featRecursion) && !g.flip(0.5)
	nParams := g.ch.intN(maxInt(g.cfg.MaxParams, 0) + 1)
	name := fmt.Sprintf("gen_f%d", idx)
	params := make([]gtype, nParams)
	fmt.Fprintf(b, "function %s(", name)
	if fueled {
		b.WriteString("__fuel: i32")
		sc.declare(tI32, "__fuel")
	}
	for i := 0; i < nParams; i++ {
		if i > 0 || fueled {
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
	sig := helperSig{name: name, params: params, retType: ret, fueled: fueled}
	if fueled {
		b.WriteString("if (__fuel <= 0i32) { return ")
		g.expr(b, sc, ret, 1)
		b.WriteString("; } ")
		g.self, g.selfCalls = &sig, 2
		if !g.flip(0.5) {
			fmt.Fprintf(b, "let __rec: %s = ", g.typeName(ret))
			g.emitCallTo(b, sc, sig, 1)
			b.WriteString("; ")
			sc.declare(ret, "__rec")
		}
	}
	g.body(b, sc, ret)
	g.self = nil
	g.currentReturnType = prevRet
	b.WriteString("}\n")
	g.helpers = append(g.helpers, sig)
}

func (g *Generator) body(b *strings.Builder, sc *scope, retT gtype) {
	prevLocals := g.localHelpers
	g.localHelpers = nil
	defer func() { g.localHelpers = prevLocals }()
	n, stmtsAt := g.listCount(maxInt(g.cfg.MaxStmts, 0) + 1)
	for i := 0; i < n; i++ {
		start := g.pos()
		g.bodyStmt(b, sc, retT, i)
		g.markSpan(stmtsAt, start)
	}
	b.WriteString("return ")
	g.expr(b, sc, retT, 0)
	b.WriteString("; ")
}

func (g *Generator) bodyStmt(b *strings.Builder, sc *scope, retT gtype, i int) {
	if g.maybeEmitWhile(b, sc) || g.maybeEmitForEach(b, sc) || g.maybeEmitLocalFn(b, sc) ||
		g.maybeEmitArrayAliasCow(b, sc) || g.maybeEmitLoopReadBeforeWith(b, sc) || g.maybeEmitOwnershipCluster(b, sc) || g.maybeEmitClosureCluster(b, sc) ||
		g.maybeEmitTraitCluster(b, sc) || g.maybeEmitGenericCluster(b, sc) || g.maybeEmitOwnRecvCluster(b, sc) ||
		g.maybeEmitControlStmt(b, sc, retT) {
		return
	}
	vt := g.pickType()
	vname := fmt.Sprintf("v%d", i)
	fmt.Fprintf(b, "let %s: %s = ", vname, g.typeName(vt))
	g.expr(b, sc, vt, 0)
	b.WriteString("; ")
	sc.declare(vt, vname)
}

func (g *Generator) maybeEmitWhile(b *strings.Builder, sc *scope) bool {
	if !g.on(featWhile) {
		return false
	}
	if g.loopDepth >= maxInt(g.cfg.MaxLoopDepth, 0) {
		return false
	}
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

	fmt.Fprintf(b, "let %s: i32 = 0i32; ", counter)
	fmt.Fprintf(b, "while (%s < %di32) { ", counter, iters)

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
		fmt.Fprintf(b, "let %s: %s = ", vname, g.typeName(vt))
		g.expr(b, inner, vt, 0)
		b.WriteString("; ")
		inner.declare(vt, vname)
	}

	fmt.Fprintf(b, "%s = %s + 1i32; ", counter, counter)
	b.WriteString("} ")
}

func (g *Generator) maybeEmitLocalFn(b *strings.Builder, sc *scope) bool {
	if !g.on(featLocalFn) {
		return false
	}
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
	prevReturnType := g.currentReturnType
	g.currentReturnType = ret
	g.inClosure++
	g.expr(b, inner, ret, 0)
	g.inClosure--
	g.currentReturnType = prevReturnType
	b.WriteString("; } ")
	g.localHelpers = append(g.localHelpers, helperSig{name: name, params: params, retType: ret})
	return true
}

func (g *Generator) maybeEmitForEach(b *strings.Builder, sc *scope) bool {
	if !g.on(featForEach) {
		return false
	}
	if g.loopDepth >= maxInt(g.cfg.MaxLoopDepth, 0) {
		return false
	}
	var arrayVar string
	var elemType gtype
	for _, at := range append([]gtype{tArrI32, tArrI64, tArrBool, tArrFnI32I32, tArrF32, tArrF64}, sortedKeys(g.arrShapes)...) {
		vars := sc.inScope(at)
		if len(vars) > 0 {
			arrayVar = vars[g.ch.intN(len(vars))]
			elemType, _ = g.arrayElemOf(at)
			break
		}
	}
	mapVars := sc.inScope(tMapI32I32)
	if arrayVar == "" && len(mapVars) == 0 {
		return false
	}
	if g.flip(0.88) {
		return false
	}
	g.loopDepth++
	defer func() { g.loopDepth-- }()
	idx := g.loopCounter
	g.loopCounter++

	inner := newScope(sc)
	switch {
	case arrayVar != "" && (len(mapVars) == 0 || !g.flip(0.5)):
		bind := fmt.Sprintf("__fe_x%d", idx)
		fmt.Fprintf(b, "for %s in %s { ", bind, arrayVar)
		inner.declare(elemType, bind)
	default:
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
		fmt.Fprintf(b, "let %s: %s = ", vname, g.typeName(vt))
		g.expr(b, inner, vt, 0)
		b.WriteString("; ")
		inner.declare(vt, vname)
	}
	b.WriteString("} ")
	return true
}

var cowArrayTypes = []gtype{tArrI32, tArrI64, tArrBool, tArrFnI32I32}

var cowFloatArrayTypes = []gtype{tArrF32, tArrF64}

func (g *Generator) maybeEmitArrayAliasCow(b *strings.Builder, sc *scope) bool {
	if !g.on(featCow) {
		return false
	}
	if g.flip(0.85) {
		return false
	}
	if g.ch.exhausted() {
		return false
	}
	pool := cowArrayTypes
	if g.profile.floatsAllowed() {
		pool = append(append([]gtype{}, pool...), cowFloatArrayTypes...)
	}
	arrT := pool[g.ch.intN(len(pool))]
	elem, _ := g.arrayElemOf(arrT)
	arrName := g.typeName(arrT)

	idx := g.cowCounter
	g.cowCounter++
	src := fmt.Sprintf("__cow_s%d", idx)
	alias := fmt.Sprintf("__cow_a%d", idx)
	result := fmt.Sprintf("cw%d_w", idx)
	readBack := fmt.Sprintf("cw%d_a", idx)

	fmt.Fprintf(b, "let %s: %s = ", src, arrName)
	n := g.arrayLiteral(b, sc, elem, 0)
	b.WriteString("; ")
	fmt.Fprintf(b, "let %s: %s = %s; ", alias, arrName, src)
	at := g.ch.intN(n)
	fmt.Fprintf(b, "let %s: %s = %s.with(%di32, ", result, arrName, src, at)
	g.expr(b, sc, elem, 1)
	b.WriteString("); ")
	fmt.Fprintf(b, "let %s: %s = %s[%di32]; ", readBack, g.typeName(elem), alias, at)

	sc.declare(arrT, result)
	sc.declare(elem, readBack)
	return true
}

func (g *Generator) maybeEmitLoopReadBeforeWith(b *strings.Builder, sc *scope) bool {
	if !g.on(featCow) {
		return false
	}
	if g.loopDepth >= maxInt(g.cfg.MaxLoopDepth, 0) {
		return false
	}
	if g.flip(0.85) {
		return false
	}
	idx := g.cowCounter
	g.cowCounter++
	src := fmt.Sprintf("__cowl_s%d", idx)
	counter := fmt.Sprintf("__cowl_i%d", idx)
	acc := fmt.Sprintf("cwl%d_acc", idx)
	result := fmt.Sprintf("cwl%d_w", idx)

	fmt.Fprintf(b, "let %s: i32[] = ", src)
	n := g.arrayLiteral(b, sc, tI32, 0)
	b.WriteString("; ")
	at := g.ch.intN(n)
	iters := 2 + g.ch.intN(2)
	fmt.Fprintf(b, "let %s: i32 = 0i32; let %s: i32 = 0i32; ", acc, counter)
	fmt.Fprintf(b, "while (%s < %di32) { ", counter, iters)
	g.loopDepth++
	fmt.Fprintf(b, "%s = %s + %s[%di32]; ", acc, acc, src, at)
	fmt.Fprintf(b, "let %s: i32[] = %s.with(%di32, ", result, src, at)
	g.expr(b, sc, tI32, 1)
	b.WriteString("); ")
	fmt.Fprintf(b, "%s = %s + 1i32; ", counter, counter)
	g.loopDepth--
	b.WriteString("} ")

	sc.declare(tI32, acc)
	return true
}

func (g *Generator) expr(b *strings.Builder, sc *scope, t gtype, depth int) {
	start := g.pos()
	g.production(b, sc, t, depth)
	g.markExpr(start, t, depth < g.cfg.MaxExprDepth)
}

const leafChance = 0.4

func (g *Generator) production(b *strings.Builder, sc *scope, t gtype, depth int) {
	if depth >= g.cfg.MaxExprDepth || g.flip(leafChance) {
		g.leaf(b, sc, t, depth)
		return
	}
	if t == tI32 && g.currentReturnType == tOptI32 && !g.flip(0.85) {
		b.WriteByte('(')
		g.emitTypedOptI32(b, sc, depth)
		b.WriteString("?)")
		return
	}
	if t == tI32 && g.currentReturnType == tResI32I32 && !g.flip(0.85) {
		ress := sc.inScope(tResI32I32)
		if len(ress) > 0 {
			fmt.Fprintf(b, "(%s?)", ress[g.ch.intN(len(ress))])
			return
		}
	}
	if g.on(featCall) && !g.flip(0.7) {
		if g.emitCall(b, sc, t, depth) {
			return
		}
	}
	if g.on(featProjection) && !g.flip(0.75) {
		if g.tryCompositeProduction(b, sc, t, depth) {
			return
		}
	}
	if g.on(featIfExpr) && !g.flip(0.8) {
		g.emitIfExpr(b, sc, t, depth)
		return
	}
	if g.on(featGeneric) && !g.flip(0.85) {
		b.WriteString("id")
		g.maybeTypeArg(b, t)
		b.WriteString("(")
		g.genericArg(b, sc, t, depth)
		b.WriteString(")")
		return
	}
	if g.on(featGeneric) && !g.flip(0.9) {
		b.WriteString("pick")
		g.maybeTypeArg(b, t)
		b.WriteString("(")
		g.expr(b, sc, tBool, depth+1)
		b.WriteString(", ")
		g.genericArg(b, sc, t, depth)
		b.WriteString(", ")
		g.genericArg(b, sc, t, depth)
		b.WriteString(")")
		return
	}
	switch t {
	case tI32, tI64, tU32, tU64, tU8, tF32, tF64:
		g.numericExpr(b, sc, t, depth)
	case tBool:
		g.boolExpr(b, sc, depth)
	case tString:
		g.stringExpr(b, sc, depth)
	default:
		g.leaf(b, sc, t, depth)
	}
}

func (g *Generator) stringExpr(b *strings.Builder, sc *scope, depth int) {
	if !g.flip(0.5) {
		b.WriteByte('(')
		g.expr(b, sc, tString, depth+1)
		b.WriteString(" + ")
		g.expr(b, sc, tString, depth+1)
		b.WriteByte(')')
		return
	}
	n := 1 + g.ch.intN(2)
	b.WriteString("f\"")
	for i := 0; i < n; i++ {
		g.fstringLitSegment(b)
		b.WriteByte('{')
		g.expr(b, sc, tI32, depth+1)
		b.WriteByte('}')
	}
	g.fstringLitSegment(b)
	b.WriteByte('"')
}

func (g *Generator) fstringLitSegment(b *strings.Builder) {
	n := g.ch.intN(6)
	for i := 0; i < n; i++ {
		g.stringChar(b)
	}
}

var simpleEscapes = []string{`\n`, `\t`, `\r`, `\0`, `\\`, `\"`, `\'`}

// stringChar writes one character of a string literal: a lowercase letter,
// or an escape the lexer decodes, a control byte or a multi-byte scalar.
func (g *Generator) stringChar(b *strings.Builder) {
	if g.flip(0.85) {
		b.WriteByte(byte('a' + g.ch.intN(26)))
		return
	}
	switch g.ch.intN(5) {
	case 0:
		b.WriteString(simpleEscapes[g.ch.intN(len(simpleEscapes))])
	case 1:
		fmt.Fprintf(b, `\x%02x`, g.ch.intN(0x80))
	case 2:
		fmt.Fprintf(b, `\u{%x}`, 0x80+g.ch.intN(0x800-0x80))
	case 3:
		fmt.Fprintf(b, `\u{%x}`, 0x800+g.ch.intN(0xD800-0x800))
	default:
		fmt.Fprintf(b, `\u{%x}`, 0x10000+g.ch.intN(0x10000))
	}
}

func (g *Generator) tryCompositeProduction(b *strings.Builder, sc *scope, t gtype, depth int) bool {
	if arrT, ok := g.arrayTypeFor(t); ok {
		arrs := sc.inScope(arrT)
		if len(arrs) > 0 {
			name := arrs[g.ch.intN(len(arrs))]
			fmt.Fprintf(b, "%s[0i32]", name)
			return true
		}
	}
	if t == tI32 {
		pairs := sc.inScope(tPair)
		if len(pairs) > 0 {
			name := pairs[g.ch.intN(len(pairs))]
			if g.flip(0.6) {
				field := []string{"fst", "snd"}[g.ch.intN(2)]
				fmt.Fprintf(b, "%s.%s", name, field)
			} else {
				fmt.Fprintf(b, "%s.sum()", name)
			}
			return true
		}
	}
	if t == tI32 {
		fns := sc.inScope(tFnI32I32)
		if len(fns) > 0 {
			name := fns[g.ch.intN(len(fns))]
			fmt.Fprintf(b, "%s(", name)
			g.expr(b, sc, tI32, depth+1)
			b.WriteByte(')')
			return true
		}
	}
	if elem, ok := g.arrayElemOf(t); ok {
		arrs := sc.inScope(t)
		if len(arrs) > 0 && !g.flip(0.7) {
			name := arrs[g.ch.intN(len(arrs))]
			fmt.Fprintf(b, "%s.with(0i32, ", name)
			g.expr(b, sc, elem, depth+1)
			b.WriteByte(')')
			return true
		}
	}
	if t == tPair {
		pairs := sc.inScope(tPair)
		if len(pairs) > 0 && !g.flip(0.4) {
			name := pairs[g.ch.intN(len(pairs))]
			fmt.Fprintf(b, "%s.swap()", name)
			return true
		}
	}
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
	if t == tF32 || t == tF64 {
		tups := sc.inScope(tTupF32F64)
		if len(tups) > 0 {
			name := tups[g.ch.intN(len(tups))]
			if t == tF32 {
				fmt.Fprintf(b, "%s.0", name)
			} else {
				fmt.Fprintf(b, "%s.1", name)
			}
			return true
		}
	}
	if t == tF32 {
		vecs := sc.inScope(tVec2)
		if len(vecs) > 0 {
			name := vecs[g.ch.intN(len(vecs))]
			fmt.Fprintf(b, "%s.%s", name, []string{"x", "y"}[g.ch.intN(2)])
			return true
		}
	}
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
	if t == tI32 {
		strs := sc.inScope(tString)
		if len(strs) > 0 {
			name := strs[g.ch.intN(len(strs))]
			fmt.Fprintf(b, "%s.len()", name)
			return true
		}
	}
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
	if t != tFShape {
		shapes := sc.inScope(tFShape)
		if len(shapes) > 0 {
			name := shapes[g.ch.intN(len(shapes))]
			idx := g.optBindCounter
			g.optBindCounter++
			fmt.Fprintf(b, "(match (%s) { FNone => ", name)
			g.expr(b, sc, t, depth+1)
			innerTwo := newScope(sc)
			innerTwo.declare(tF32, fmt.Sprintf("__fs_a%d", idx))
			innerTwo.declare(tF32, fmt.Sprintf("__fs_b%d", idx))
			fmt.Fprintf(b, ", FTwo(__fs_a%d, __fs_b%d) => ", idx, idx)
			g.expr(b, innerTwo, t, depth+1)
			innerWide := newScope(sc)
			innerWide.declare(tF64, fmt.Sprintf("__fs_c%d", idx))
			innerWide.declare(tF64, fmt.Sprintf("__fs_d%d", idx))
			fmt.Fprintf(b, ", FWide(__fs_c%d, __fs_d%d) => ", idx, idx)
			g.expr(b, innerWide, t, depth+1)
			b.WriteString(" })")
			return true
		}
	}
	if t != tOptI32 {
		opts := sc.inScope(tOptI32)
		if len(opts) > 0 {
			name := opts[g.ch.intN(len(opts))]
			idx := g.optBindCounter
			g.optBindCounter++
			bind := fmt.Sprintf("__opt_x%d", idx)
			fmt.Fprintf(b, "(match (%s) { ", name)
			g.guardedArm(b, sc, "Some", fmt.Sprintf("__opt_g%d", idx), t, depth)
			innerSome := newScope(sc)
			g.atBinding(b, innerSome, tOptI32, fmt.Sprintf("__opt_w%d", idx))
			fmt.Fprintf(b, "Some(%s) => ", bind)
			innerSome.declare(tI32, bind)
			g.expr(b, innerSome, t, depth+1)
			b.WriteString(", None => ")
			g.expr(b, sc, t, depth+1)
			b.WriteString(" })")
			return true
		}
	}
	if t != tResI32I32 {
		ress := sc.inScope(tResI32I32)
		if len(ress) > 0 {
			name := ress[g.ch.intN(len(ress))]
			idx := g.optBindCounter
			g.optBindCounter++
			okBind := fmt.Sprintf("__res_ok%d", idx)
			errBind := fmt.Sprintf("__res_err%d", idx)
			fmt.Fprintf(b, "(match (%s) { ", name)
			g.guardedArm(b, sc, "Ok", fmt.Sprintf("__res_g%d", idx), t, depth)
			innerOk := newScope(sc)
			g.atBinding(b, innerOk, tResI32I32, fmt.Sprintf("__res_w%d", idx))
			fmt.Fprintf(b, "Ok(%s) => ", okBind)
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
			fmt.Fprintf(b, "%s.contains(", name)
			g.expr(b, sc, tI32, depth+1)
			b.WriteByte(')')
			return true
		}
	}
	if t == tI32 {
		maps := sc.inScope(tMapI32I32)
		if len(maps) > 0 {
			name := maps[g.ch.intN(len(maps))]
			fmt.Fprintf(b, "%s.len()", name)
			return true
		}
	}
	if paths := g.accessPaths(sc, t); len(paths) > 0 {
		b.WriteString(paths[g.ch.intN(len(paths))])
		return true
	}
	for _, et := range sortedKeys(g.enumShapes) {
		if et == t {
			continue
		}
		sh := g.enumShapes[et]
		vars := sc.inScope(et)
		if len(vars) == 0 {
			continue
		}
		vname := vars[g.ch.intN(len(vars))]
		bindIdx := g.optBindCounter
		g.optBindCounter++
		fmt.Fprintf(b, "(match (%s) {", vname)
		for i, v := range sh.variants {
			if i > 0 {
				b.WriteString(",")
			}
			arm := sc
			if len(v.payload) == 0 {
				fmt.Fprintf(b, " %s => ", v.name)
			} else {
				arm = newScope(sc)
				fmt.Fprintf(b, " %s(", v.name)
				for k, pt := range v.payload {
					if k > 0 {
						b.WriteString(", ")
					}
					bind := fmt.Sprintf("__dv%d_%d_%d", bindIdx, i, k)
					arm.declare(pt, bind)
					b.WriteString(bind)
				}
				b.WriteString(") => ")
			}
			g.expr(b, arm, t, depth+1)
		}
		b.WriteString(" })")
		return true
	}
	return false
}

const maxPathSteps = 3

func (g *Generator) accessPaths(sc *scope, t gtype) []string {
	type node struct {
		path string
		t    gtype
	}
	var frontier []node
	for _, vt := range g.projectable() {
		for _, name := range sc.inScope(vt) {
			frontier = append(frontier, node{name, vt})
		}
	}
	var out []string
	for step := 0; step < maxPathSteps && len(frontier) > 0; step++ {
		var next []node
		for _, n := range frontier {
			for _, m := range g.members(n.t) {
				child := node{n.path + m.suffix, m.t}
				if child.t == t {
					out = append(out, child.path)
				}
				next = append(next, child)
			}
		}
		frontier = next
	}
	return out
}

type member struct {
	suffix string
	t      gtype
}

func (g *Generator) projectable() []gtype {
	out := []gtype{tPair, tXyz, tTupI32I64, tArrI32, tArrI64, tArrBool, tArrFnI32I32}
	if g.profile.floatsAllowed() {
		out = append(out, tVec2, tTupF32F64, tArrF32, tArrF64)
	}
	return append(out, append(sortedKeys(g.structShapes), sortedKeys(g.arrShapes)...)...)
}

func (g *Generator) members(t gtype) []member {
	if sh, ok := g.structShapes[t]; ok {
		out := make([]member, len(sh.fields))
		for i, f := range sh.fields {
			out[i] = member{"." + f.name, f.t}
		}
		return out
	}
	if elem, ok := g.arrayElemOf(t); ok {
		return []member{{"[0i32]", elem}}
	}
	switch t {
	case tPair:
		return []member{{".fst", tI32}, {".snd", tI32}}
	case tXyz:
		return []member{{".n", tI32}, {".valid", tBool}}
	case tTupI32I64:
		return []member{{".0", tI32}, {".1", tI64}}
	case tVec2:
		return []member{{".x", tF32}, {".y", tF32}}
	case tTupF32F64:
		return []member{{".0", tF32}, {".1", tF64}}
	}
	return nil
}

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
	if g.self != nil && g.self.retType == t && g.selfCalls > 0 && g.loopDepth == 0 && g.inClosure == 0 {
		cands = append(cands, *g.self)
	}
	if len(cands) == 0 {
		return false
	}
	var pick helperSig
	if g.ch.exhausted() {
		pick = cands[0]
		for _, h := range cands[1:] {
			if g.callCost(sc, h) < g.callCost(sc, pick) {
				pick = h
			}
		}
	} else {
		pick = cands[g.ch.intN(len(cands))]
	}
	g.emitCallTo(b, sc, pick, depth)
	return true
}

func (g *Generator) emitCallTo(b *strings.Builder, sc *scope, h helperSig, depth int) {
	fuel := ""
	if h.fueled {
		if g.self != nil && h.name == g.self.name {
			g.selfCalls--
			fuel = "(__fuel - 1i32)"
		} else {
			fuel = fmt.Sprintf("%di32", g.ch.intN(4))
		}
	}
	if (len(h.params) >= 1 || fuel != "") && !g.flip(0.6) {
		b.WriteByte('(')
		if fuel != "" {
			b.WriteString(fuel)
		} else {
			g.expr(b, sc, h.params[0], depth+1)
		}
		fmt.Fprintf(b, " |> %s(", h.name)
		rest := h.params
		if fuel == "" {
			rest = h.params[1:]
		}
		for i, pt := range rest {
			if i > 0 {
				b.WriteString(", ")
			}
			g.expr(b, sc, pt, depth+1)
		}
		b.WriteString("))")
		return
	}
	b.WriteString(h.name)
	b.WriteByte('(')
	if fuel != "" {
		b.WriteString(fuel)
	}
	for i, pt := range h.params {
		if i > 0 || fuel != "" {
			b.WriteString(", ")
		}
		g.expr(b, sc, pt, depth+1)
	}
	b.WriteByte(')')
}

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
	if g.ch.exhausted() {
		g.leafMin(b, sc, t, depth)
		return
	}
	g.literal(b, sc, t, depth)
}

func (g *Generator) numericExpr(b *strings.Builder, sc *scope, t gtype, depth int) {
	if t == tI32 && !g.flip(0.85) {
		g.emitI64HighHalf(b, sc, depth)
		return
	}
	if t == tI32 && !g.flip(0.85) {
		g.emitUnsignedAsI32(b, sc, depth)
		return
	}
	if t != tF32 && t != tF64 && depth < 2 && !g.flip(0.85) {
		g.emitCheckedFold(b, sc, t, depth)
		return
	}
	if g.ch.exhausted() {
		g.leaf(b, sc, t, depth)
		return
	}
	ops := []string{"+", "-", "*", "/"}
	if t != tF32 && t != tF64 {
		ops = append(ops, "%", "<<", ">>", "&", "|", "^")
	}
	op := ops[g.ch.intN(len(ops))]
	op = g.maybeSaturating(op, t)
	b.WriteByte('(')
	g.expr(b, sc, t, depth+1)
	fmt.Fprintf(b, " %s ", op)
	g.expr(b, sc, t, depth+1)
	b.WriteByte(')')
}

func (g *Generator) emitCheckedFold(b *strings.Builder, sc *scope, t gtype, depth int) {
	op := []string{">>?", "+?", "-?", "<<?", "/?", "%?", "*?"}[g.ch.intN(7)]
	bind := fmt.Sprintf("__chk_v%d", g.optBindCounter)
	g.optBindCounter++
	b.WriteString("(match ((")
	g.expr(b, sc, t, depth+1)
	fmt.Fprintf(b, ") %s (", op)
	g.expr(b, sc, t, depth+1)
	fmt.Fprintf(b, ")) { Some(%s) => %s, None => ", bind, bind)
	g.expr(b, sc, t, depth+1)
	b.WriteString(" })")
}

func (g *Generator) maybeSaturating(op string, t gtype) string {
	if t == tF32 || t == tF64 {
		return op
	}
	switch op {
	case "+", "-", "*", "<<":
		if g.flip(0.25) {
			return op + "|"
		}
	}
	return op
}

func (g *Generator) emitUnsignedAsI32(b *strings.Builder, sc *scope, depth int) {
	if !g.flip(0.5) {
		b.WriteString("(((")
		g.expr(b, sc, tU64, depth+1)
		b.WriteString(") >> 32u64) as i32)")
		return
	}
	b.WriteString("((")
	g.expr(b, sc, tU32, depth+1)
	b.WriteString(") as i32)")
}

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

func (g *Generator) guardedArm(b *strings.Builder, sc *scope, variant, bind string, t gtype, depth int) {
	if g.flip(0.7) {
		return
	}
	inner := newScope(sc)
	inner.declare(tI32, bind)
	fmt.Fprintf(b, "%s(%s) when ", variant, bind)
	g.boolExpr(b, inner, depth+1)
	b.WriteString(" => ")
	g.expr(b, inner, t, depth+1)
	b.WriteString(", ")
}

func (g *Generator) atBinding(b *strings.Builder, inner *scope, st gtype, whole string) {
	if g.flip(0.7) {
		return
	}
	fmt.Fprintf(b, "%s @ ", whole)
	inner.declare(st, whole)
}

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

func (g *Generator) literal(b *strings.Builder, sc *scope, t gtype, depth int) {
	switch t {
	case tI32:
		fmt.Fprintf(b, "%di32", g.ch.intN(1000))
	case tI64:
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
	case tU32:
		if g.flip(0.5) {
			fmt.Fprintf(b, "%du32", 2147483648+uint32(g.ch.intN(1000)))
			return
		}
		fmt.Fprintf(b, "%du32", g.ch.intN(1000))
	case tU8:
		if g.flip(0.5) {
			fmt.Fprintf(b, "%du8", 200+g.ch.intN(56))
			return
		}
		fmt.Fprintf(b, "%du8", g.ch.intN(200))
	case tU64:
		if g.flip(0.5) {
			fmt.Fprintf(b, "%du64", uint64(1)<<63+uint64(g.ch.intN(1000)))
			return
		}
		fmt.Fprintf(b, "%du64", g.ch.intN(1000))
	case tF64:
		fmt.Fprintf(b, "%d.%06df64", g.ch.intN(10000), g.ch.intN(1000000))
	case tF32:
		fmt.Fprintf(b, "%.2ff32", float64(g.ch.intN(10000))/100.0)
	case tString:
		b.WriteString(g.stringLiteral())
	case tArrI32, tArrI64, tArrBool, tArrFnI32I32, tArrF32, tArrF64:
		elem, _ := g.arrayElemOf(t)
		g.arrayLiteral(b, sc, elem, depth)
	case tFnI32I32:
		g.lambdaLiteral(b, sc, depth)
	case tPair:
		g.pairLiteral(b, sc, depth)
	case tColor:
		b.WriteString([]string{"Red", "Green", "Blue"}[g.ch.intN(3)])
	case tStatus:
		b.WriteString([]string{"Active", "Inactive", "Pending"}[g.ch.intN(3)])
	case tXyz:
		b.WriteString("(Xyz { n: ")
		g.expr(b, sc, tI32, depth+1)
		b.WriteString(", valid: ")
		g.expr(b, sc, tBool, depth+1)
		b.WriteString(" })")
	case tOptI32:
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
		if g.flip(0.5) {
			b.WriteString("(Ok(")
			g.expr(b, sc, tI32, depth+1)
			b.WriteString("))")
		} else {
			b.WriteString("(Err(")
			g.expr(b, sc, tI32, depth+1)
			b.WriteString("))")
		}
	case tVec2:
		b.WriteString("(Vec2 { x: ")
		g.expr(b, sc, tF32, depth+1)
		b.WriteString(", y: ")
		g.expr(b, sc, tF32, depth+1)
		b.WriteString(" })")
	case tTupF32F64:
		b.WriteString("(")
		g.expr(b, sc, tF32, depth+1)
		b.WriteString(", ")
		g.expr(b, sc, tF64, depth+1)
		b.WriteString(")")
	case tFShape:
		switch {
		case g.flip(0.4):
			b.WriteString("FNone")
		case g.flip(0.5):
			b.WriteString("(FTwo(")
			g.expr(b, sc, tF32, depth+1)
			b.WriteString(", ")
			g.expr(b, sc, tF32, depth+1)
			b.WriteString("))")
		default:
			b.WriteString("(FWide(")
			g.expr(b, sc, tF64, depth+1)
			b.WriteString(", ")
			g.expr(b, sc, tF64, depth+1)
			b.WriteString("))")
		}
	case tTupI32I64:
		b.WriteString("(")
		g.expr(b, sc, tI32, depth+1)
		b.WriteString(", ")
		g.expr(b, sc, tI64, depth+1)
		b.WriteString(")")
	default:
		if sh, ok := g.structShapes[t]; ok {
			g.dynStructLiteral(b, sc, t, sh, depth)
			return
		}
		if sh, ok := g.enumShapes[t]; ok {
			g.dynEnumLiteral(b, sc, sh, depth)
			return
		}
		if elem, ok := g.arrShapes[t]; ok {
			g.arrayLiteral(b, sc, elem, depth)
			return
		}
		panic(fmt.Sprintf("literal: unknown gtype %d", int(t)))
	}
}

func (g *Generator) dynStructLiteral(b *strings.Builder, sc *scope, t gtype, sh *structShape, depth int) {
	if srcs := sc.inScope(t); len(srcs) > 0 && (g.ch.exhausted() || !g.flip(0.7)) {
		src := srcs[g.ch.intN(len(srcs))]
		f := sh.fields[g.ch.intN(len(sh.fields))]
		fmt.Fprintf(b, "(%s { ...%s, %s: ", sh.name, src, f.name)
		g.expr(b, sc, f.t, depth+1)
		b.WriteString(" })")
		return
	}
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

func (g *Generator) dynEnumLiteral(b *strings.Builder, sc *scope, sh *enumShape, depth int) {
	v := sh.variants[g.ch.intN(len(sh.variants))]
	if g.ch.exhausted() {
		for _, cand := range sh.variants {
			if len(cand.payload) == 0 {
				v = cand
				break
			}
		}
	}
	if len(v.payload) == 0 {
		b.WriteString(v.name)
		return
	}
	fmt.Fprintf(b, "(%s(", v.name)
	for i, pt := range v.payload {
		if i > 0 {
			b.WriteString(", ")
		}
		g.expr(b, sc, pt, depth+1)
	}
	b.WriteString("))")
}

func (g *Generator) mapLiteral(b *strings.Builder, sc *scope, depth int) {
	n := g.ch.intN(4)
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

func (g *Generator) arrayLiteral(b *strings.Builder, sc *scope, elem gtype, depth int) int {
	n := 1 + g.ch.intN(4)
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		g.expr(b, sc, elem, depth+1)
	}
	b.WriteByte(']')
	return n
}

func (g *Generator) lambdaLiteral(b *strings.Builder, sc *scope, depth int) {
	name := fmt.Sprintf("__lam_x%d", g.lambdaCounter)
	g.lambdaCounter++
	inner := newScope(sc)
	inner.declare(tI32, name)
	fmt.Fprintf(b, "((%s: i32) => ", name)
	prevReturnType := g.currentReturnType
	g.currentReturnType = tI32
	g.inClosure++
	g.expr(b, inner, tI32, depth+1)
	g.inClosure--
	g.currentReturnType = prevReturnType
	b.WriteByte(')')
}

func (g *Generator) pairLiteral(b *strings.Builder, sc *scope, depth int) {
	b.WriteString("(Pair { fst: ")
	g.expr(b, sc, tI32, depth+1)
	b.WriteString(", snd: ")
	g.expr(b, sc, tI32, depth+1)
	b.WriteString(" })")
}

func (g *Generator) stringLiteral() string {
	n := g.ch.intN(8)
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < n; i++ {
		g.stringChar(&sb)
	}
	sb.WriteByte('"')
	return sb.String()
}

func (g *Generator) pickType() gtype {
	pool := g.typePool()
	return pool[g.ch.intN(len(pool))]
}

func (g *Generator) pickMainVarType() gtype {
	pool := append([]gtype{}, mainVarTypes...)
	if g.profile.floatsAllowed() {
		pool = append(pool, floatVarTypes...)
	}
	pool = append(g.swarmed(pool), g.sortedDynamicTypes()...)
	return pool[g.ch.intN(len(pool))]
}

func (g *Generator) typePool() []gtype {
	var pool []gtype
	if !g.profile.floatsAllowed() {
		pool = []gtype{
			tI32, tI64, tU32, tU64, tU8, tBool, tString,
			tArrI32, tArrI64, tArrBool, tPair, tColor, tOptI32,
			tXyz, tStatus, tMapI32I32, tResI32I32, tTupI32I64,
			tFnI32I32, tArrFnI32I32,
		}
	} else {
		pool = append(pool, allTypes[:]...)
	}
	return append(g.swarmed(pool), g.sortedDynamicTypes()...)
}

func (g *Generator) sortedDynamicTypes() []gtype {
	out := append(sortedKeys(g.structShapes), sortedKeys(g.enumShapes)...)
	out = append(out, sortedKeys(g.arrShapes)...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sortedKeys[V any](m map[gtype]V) []gtype {
	out := make([]gtype, 0, len(m))
	for t := range m {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (g *Generator) pickFieldType() gtype {
	nested := g.swarmed(nestedFieldTypes)
	nested = append(nested, sortedKeys(g.structShapes)...)
	nested = append(nested, sortedKeys(g.enumShapes)...)
	if len(nested) == 0 || !g.on(featNested) || g.flip(0.6) {
		scalars := []gtype{tI32, tI64, tBool, tString}
		if g.profile.floatsAllowed() {
			scalars = append(scalars, tF32, tF64)
		}
		scalars = g.swarmed(scalars)
		return scalars[g.ch.intN(len(scalars))]
	}
	return nested[g.ch.intN(len(nested))]
}

var nestedFieldTypes = []gtype{
	tArrI32, tOptI32, tResI32I32, tTupI32I64, tPair, tFnI32I32,
	tMapI32I32, tArrBool, tArrI64, tArrFnI32I32, tXyz, tColor,
}

func (g *Generator) pickNumeric() gtype {
	if !g.profile.floatsAllowed() {
		ints := []gtype{tI32, tI64, tU32, tU64, tU8}
		return ints[g.ch.intN(len(ints))]
	}
	return []gtype{tI32, tI64, tU32, tU64, tU8, tF32, tF64}[g.ch.intN(7)]
}

func (g *Generator) flip(p float64) bool { return g.ch.flip(p) }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (g *Generator) maybeEmitControlStmt(b *strings.Builder, sc *scope, retT gtype) bool {
	if !g.on(featControl) {
		return false
	}
	if g.maybeEmitLoopBreak(b, sc) {
		return true
	}
	if g.maybeEmitIfElseStmt(b, sc) {
		return true
	}
	if g.maybeEmitIfLet(b, sc) {
		return true
	}
	if g.maybeEmitMatchStmt(b, sc) {
		return true
	}
	if g.maybeEmitDefer(b, sc) {
		return true
	}
	if g.maybeEmitLetElse(b, sc, retT) {
		return true
	}
	return g.maybeEmitTupleDestructure(b, sc)
}

func (g *Generator) innerStmts(b *strings.Builder, inner *scope, prefix string, max int) {
	n := g.ch.intN(max)
	for i := 0; i < n; i++ {
		if g.maybeEmitWhile(b, inner) {
			continue
		}
		if g.maybeEmitForEach(b, inner) {
			continue
		}
		vt := g.pickType()
		vname := fmt.Sprintf("%s_%d", prefix, i)
		fmt.Fprintf(b, "let %s: %s = ", vname, g.typeName(vt))
		g.expr(b, inner, vt, 0)
		b.WriteString("; ")
		inner.declare(vt, vname)
	}
}

func (g *Generator) maybeEmitIfElseStmt(b *strings.Builder, sc *scope) bool {
	if g.flip(0.9) {
		return false
	}
	idx := g.ctrlCounter
	g.ctrlCounter++
	b.WriteString("if (")
	g.boolExpr(b, sc, 0)
	b.WriteString(") { ")
	g.innerStmts(b, newScope(sc), fmt.Sprintf("__if%d_t", idx), 3)
	b.WriteString("} else { ")
	g.innerStmts(b, newScope(sc), fmt.Sprintf("__if%d_e", idx), 3)
	b.WriteString("} ")
	return true
}

func (g *Generator) maybeEmitLoopBreak(b *strings.Builder, sc *scope) bool {
	if g.loopDepth >= maxInt(g.cfg.MaxLoopDepth, 0) {
		return false
	}
	if g.flip(0.9) {
		return false
	}
	idx := g.ctrlCounter
	g.ctrlCounter++
	g.loopDepth++
	defer func() { g.loopDepth-- }()
	counter := fmt.Sprintf("__lb%d", idx)
	iters := 1 + g.ch.intN(maxInt(g.cfg.MaxLoopIters, 1))
	fmt.Fprintf(b, "let %s: i32 = 0i32; ", counter)
	label := ""
	if !g.flip(0.6) {
		label = fmt.Sprintf("__lp%d", idx)
		fmt.Fprintf(b, "%s: ", label)
	}
	fmt.Fprintf(b, "loop { if (%s >= %di32) { break; } ", counter, iters)
	if !g.flip(0.6) {
		b.WriteString("if (")
		g.boolExpr(b, sc, 0)
		fmt.Fprintf(b, ") { %s = %s + 1i32; continue; } ", counter, counter)
	}
	if label != "" && g.loopDepth < maxInt(g.cfg.MaxLoopDepth, 0) && !g.flip(0.5) {
		g.loopDepth++
		inner := fmt.Sprintf("__li%d", idx)
		innerIters := 1 + g.ch.intN(maxInt(g.cfg.MaxLoopIters, 1))
		fmt.Fprintf(b, "let %s: i32 = 0i32; loop { if (%s >= %di32) { break; } if (", inner, inner, innerIters)
		g.boolExpr(b, sc, 0)
		fmt.Fprintf(b, ") { break %s; } if (", label)
		g.boolExpr(b, sc, 0)
		fmt.Fprintf(b, ") { %s = %s + 1i32; continue %s; } ", counter, counter, label)
		g.innerStmts(b, newScope(sc), fmt.Sprintf("__li%d", idx), 2)
		fmt.Fprintf(b, "%s = %s + 1i32; } ", inner, inner)
		g.loopDepth--
	}
	g.innerStmts(b, newScope(sc), fmt.Sprintf("__lb%d", idx), 3)
	fmt.Fprintf(b, "%s = %s + 1i32; } ", counter, counter)
	return true
}

func (g *Generator) maybeEmitDefer(b *strings.Builder, sc *scope) bool {
	if g.profile == ProfilePrintable {
		return false
	}
	if g.flip(0.92) {
		return false
	}
	idx := g.ctrlCounter
	g.ctrlCounter++
	prevRet := g.currentReturnType
	g.currentReturnType = tI32
	b.WriteString("defer { ")
	g.innerStmts(b, newScope(sc), fmt.Sprintf("__df%d", idx), 3)
	b.WriteString("} ")
	g.currentReturnType = prevRet
	return true
}

func (g *Generator) maybeEmitTupleDestructure(b *strings.Builder, sc *scope) bool {
	if g.flip(0.9) {
		return false
	}
	idx := g.ctrlCounter
	g.ctrlCounter++
	a := fmt.Sprintf("__td%d_a", idx)
	c := fmt.Sprintf("__td%d_b", idx)
	fmt.Fprintf(b, "let (%s, %s) = ", a, c)
	g.expr(b, sc, tTupI32I64, 0)
	b.WriteString("; ")
	sc.declare(tI32, a)
	sc.declare(tI64, c)
	return true
}

func (g *Generator) maybeEmitIfLet(b *strings.Builder, sc *scope) bool {
	opts := sc.inScope(tOptI32)
	ress := sc.inScope(tResI32I32)
	if len(opts) == 0 && len(ress) == 0 {
		return false
	}
	if g.flip(0.9) {
		return false
	}
	idx := g.ctrlCounter
	g.ctrlCounter++
	bind := fmt.Sprintf("__il%d", idx)
	then := newScope(sc)
	then.declare(tI32, bind)
	if len(opts) > 0 && (len(ress) == 0 || g.flip(0.5)) {
		fmt.Fprintf(b, "if let Some(%s) = %s", bind, opts[g.ch.intN(len(opts))])
	} else {
		fmt.Fprintf(b, "if let Ok(%s) = %s", bind, ress[g.ch.intN(len(ress))])
	}
	b.WriteString(" { ")
	g.innerStmts(b, then, fmt.Sprintf("__il%d_t", idx), 3)
	b.WriteString("} else { ")
	g.innerStmts(b, newScope(sc), fmt.Sprintf("__il%d_e", idx), 3)
	b.WriteString("} ")
	return true
}

func (g *Generator) maybeEmitMatchStmt(b *strings.Builder, sc *scope) bool {
	opts := sc.inScope(tOptI32)
	ress := sc.inScope(tResI32I32)
	if len(opts) == 0 && len(ress) == 0 {
		return false
	}
	if g.flip(0.9) {
		return false
	}
	idx := g.ctrlCounter
	g.ctrlCounter++
	bind := fmt.Sprintf("__ms%d", idx)
	variant, miss, st := "Some", "None", tOptI32
	if len(opts) > 0 && (len(ress) == 0 || g.flip(0.5)) {
		fmt.Fprintf(b, "match (%s) { ", opts[g.ch.intN(len(opts))])
	} else {
		variant, miss, st = "Ok", fmt.Sprintf("Err(%s_e)", bind), tResI32I32
		fmt.Fprintf(b, "match (%s) { ", ress[g.ch.intN(len(ress))])
	}
	if !g.flip(0.7) {
		guard := newScope(sc)
		guard.declare(tI32, bind+"_g")
		fmt.Fprintf(b, "%s(%s_g) when ", variant, bind)
		g.boolExpr(b, guard, 0)
		b.WriteString(" => { ")
		g.innerStmts(b, guard, bind+"_gb", 3)
		b.WriteString("}, ")
	}
	hit := newScope(sc)
	g.atBinding(b, hit, st, bind+"_w")
	fmt.Fprintf(b, "%s(%s) => { ", variant, bind)
	hit.declare(tI32, bind)
	g.innerStmts(b, hit, bind+"_h", 3)
	fmt.Fprintf(b, "}, %s => { ", miss)
	missScope := newScope(sc)
	if st == tResI32I32 {
		missScope.declare(tI32, bind+"_e")
	}
	g.innerStmts(b, missScope, bind+"_m", 3)
	b.WriteString("} } ")
	return true
}

func (g *Generator) maybeEmitLetElse(b *strings.Builder, sc *scope, retT gtype) bool {
	if g.profile == ProfilePrintable {
		return false
	}
	opts := sc.inScope(tOptI32)
	if len(opts) == 0 {
		return false
	}
	if g.flip(0.92) {
		return false
	}
	idx := g.ctrlCounter
	g.ctrlCounter++
	bind := fmt.Sprintf("__le%d", idx)
	fmt.Fprintf(b, "let Some(%s) = %s", bind, opts[g.ch.intN(len(opts))])
	if retT == tI32 {
		b.WriteString(" else { return (")
		g.expr(b, sc, tI32, 0)
		b.WriteString(" & 255i32); }; ")
	} else {
		b.WriteString(" else { return ")
		g.expr(b, sc, retT, 0)
		b.WriteString("; }; ")
	}
	sc.declare(tI32, bind)
	return true
}
