package tv

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The op kind ids the model gives a meaning to (ir.fern's K_* constants,
// pinned by internal/testing/e2ecompiler/testdata/ir-kind-ids.txt).
const (
	kConstI32     = 1
	kConstI32Text = 2
	kConstI64     = 3
	kIntExtend    = 7
	kIntWrap      = 8
	kIntCast      = 9
	kU32Wrap      = 10
	kLoadLocal    = 18
	kStoreLocal   = 19
	kTeeLocal     = 20
	kAdd          = 21
	kSub          = 22
	kMul          = 23
	kDivS         = 24
	kDivU         = 25
	kRemS         = 26
	kRemU         = 27
	kAnd          = 28
	kOr           = 29
	kXor          = 30
	kShl          = 31
	kShrS         = 32
	kShrU         = 33
	kNot          = 34
	kEq           = 35
	kNe           = 36
	kLtS          = 37
	kLeS          = 38
	kGtS          = 39
	kGeS          = 40
	kLtU          = 41
	kLeU          = 42
	kGtU          = 43
	kGeU          = 44
	kDrop         = 159
)

// Modelled reports whether the encoder gives op a meaning: the integer
// fragment of docs/INTEGER-SEMANTICS.md at 32 and 64 bits, plus the local and
// stack shuffles. Every other op is a barrier Split requires both sides to keep.
func Modelled(op Op) bool {
	switch op.Kind {
	case kConstI32:
		return op.Str == ""
	case kConstI32Text:
		v, err := strconv.ParseInt(op.Str, 10, 32)
		return err == nil && strconv.FormatInt(v, 10) == op.Str
	case kConstI64:
		if op.Str == "" {
			return true
		}
		v, err := strconv.ParseInt(op.Str, 10, 64)
		return err == nil && strconv.FormatInt(v, 10) == op.Str
	case kIntExtend, kIntWrap, kU32Wrap, kLoadLocal, kStoreLocal, kTeeLocal, kDrop:
		return true
	case kIntCast:
		return op.Str == "i32" || op.Str == "u32" || op.Str == "u8"
	case kNot:
		return op.Width == 0
	}
	if op.Kind >= kAdd && op.Kind <= kGeU {
		return op.Width == 0 || op.Width == 64
	}
	return false
}

// Pair is one straight-line run of modelled ops as a pass read it and as it
// wrote it.
type Pair struct {
	Before, After []Op
}

// Split cuts both sides of rw at their unmodelled ops and returns the runs
// between them that differ. The unmodelled ops must match one for one: the
// model cannot say what a changed one means, so a pass that changes one is
// outside what this package validates, and Split says so.
//
// Proving each pair equivalent proves the whole rewrite: every pair is checked
// over any stack beneath it and any value in every local, and the barriers
// between pairs are the same ops run on the same state.
func Split(rw Rewrite) ([]Pair, error) {
	bRuns, bBars := runs(rw.Before)
	aRuns, aBars := runs(rw.After)
	if len(bBars) != len(aBars) {
		return nil, fmt.Errorf("the pass changed the unmodelled ops: %d before, %d after", len(bBars), len(aBars))
	}
	for i := range bBars {
		if bBars[i].Line != aBars[i].Line {
			return nil, fmt.Errorf("the pass changed unmodelled op %d: %s became %s", i, bBars[i], aBars[i])
		}
	}
	var out []Pair
	for i := range bRuns {
		if !sameOps(bRuns[i], aRuns[i]) {
			out = append(out, Pair{Before: bRuns[i], After: aRuns[i]})
		}
	}
	return out, nil
}

// runs splits ops at its unmodelled ops: len(runs) == len(barriers)+1.
func runs(ops []Op) (runs [][]Op, barriers []Op) {
	cur := []Op{}
	for _, op := range ops {
		if Modelled(op) {
			cur = append(cur, op)
			continue
		}
		runs = append(runs, cur)
		barriers = append(barriers, op)
		cur = []Op{}
	}
	return append(runs, cur), barriers
}

func sameOps(a, b []Op) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Line != b[i].Line {
			return false
		}
	}
	return true
}

// Mismatch is a pair whose sides cannot be equivalent whatever the solver
// says: they leave stacks of different depths, or a value at different widths.
type Mismatch struct{ Reason string }

func (m *Mismatch) Error() string { return m.Reason }

// value is a symbolic stack or local value: an SMT term and its width in bits.
// Width 0 marks an input no op has used yet; its width is in encoder.inputs
// once one does.
type value struct {
	term  string
	width int
}

// encoder holds what both sides of a pair share: the inputs (the entry stack
// s0, s1, ... from the top down, and each local l<n> as it was on entry) and
// the definitions the two sides' terms name.
type encoder struct {
	inputs map[string]int
	order  []string
	defs   []string
	err    error
}

func (e *encoder) input(name string) value {
	if _, ok := e.inputs[name]; !ok {
		e.inputs[name] = 0
		e.order = append(e.order, name)
	}
	return value{term: name}
}

func (e *encoder) fail(format string, args ...any) {
	if e.err == nil {
		e.err = &Mismatch{Reason: fmt.Sprintf(format, args...)}
	}
}

// widthOf is v's width, 0 for an input nothing has decided yet.
func (e *encoder) widthOf(v value) int {
	if v.width != 0 {
		return v.width
	}
	return e.inputs[v.term]
}

// use reads v as a w-bit operand, deciding an input's width on its first use.
func (e *encoder) use(v value, w int) string {
	got := e.widthOf(v)
	if got == 0 {
		e.inputs[v.term] = w
	} else if got != w {
		e.fail("%s is read at %d bits and at %d", v.term, got, w)
	}
	return v.term
}

// side is one side's machine state.
type side struct {
	e      *encoder
	tag    string
	stack  []value
	pulled int
	locals map[int]value
	temps  int
}

func (s *side) push(v value) { s.stack = append(s.stack, v) }

// pop takes the top of the stack, reading the next entry-stack input once
// the side has consumed everything it pushed.
func (s *side) pop() value {
	if n := len(s.stack); n > 0 {
		v := s.stack[n-1]
		s.stack = s.stack[:n-1]
		return v
	}
	v := s.e.input("s" + strconv.Itoa(s.pulled))
	s.pulled++
	return v
}

func (s *side) local(n int) value {
	if v, ok := s.locals[n]; ok {
		return v
	}
	return s.e.input("l" + strconv.Itoa(n))
}

// define names expr as a fresh w-bit term, so a value used many times is
// spelled once.
func (s *side) define(w int, expr string) value {
	name := s.tag + strconv.Itoa(s.temps)
	s.temps++
	s.e.defs = append(s.e.defs, fmt.Sprintf("(define-fun %s () (_ BitVec %d) %s)", name, w, expr))
	return value{term: name, width: w}
}

func lit(v int64, w int) string {
	if w == 32 {
		return fmt.Sprintf("(_ bv%d 32)", uint32(v))
	}
	return fmt.Sprintf("(_ bv%d 64)", uint64(v))
}

func opWidth(op Op) int {
	if op.Width == 64 {
		return 64
	}
	return 32
}

func (s *side) step(op Op) {
	switch op.Kind {
	case kConstI32:
		s.push(value{term: lit(int64(op.I32), 32), width: 32})
	case kConstI32Text:
		v, _ := strconv.ParseInt(op.Str, 10, 32)
		s.push(value{term: lit(v, 32), width: 32})
	case kConstI64:
		v := op.I64
		if op.Str != "" {
			v, _ = strconv.ParseInt(op.Str, 10, 64)
		}
		s.push(value{term: lit(v, 64), width: 64})
	case kLoadLocal:
		s.push(s.local(int(op.I32)))
	case kStoreLocal:
		s.locals[int(op.I32)] = s.pop()
	case kTeeLocal:
		v := s.pop()
		s.locals[int(op.I32)] = v
		s.push(v)
	case kDrop:
		s.pop()
	default:
		s.convert(op)
	}
}

// convert covers the one-operand ops; binary everything else.
func (s *side) convert(op Op) {
	switch op.Kind {
	case kNot:
		a := s.e.use(s.pop(), 32)
		s.push(s.define(32, fmt.Sprintf("(ite (= %s %s) %s %s)", a, lit(0, 32), lit(1, 32), lit(0, 32))))
	case kIntExtend:
		ext := "sign_extend"
		if op.Unsigned {
			ext = "zero_extend"
		}
		s.push(s.define(64, fmt.Sprintf("((_ %s 32) %s)", ext, s.e.use(s.pop(), 32))))
	case kIntWrap:
		s.push(s.define(32, fmt.Sprintf("((_ extract 31 0) %s)", s.e.use(s.pop(), 64))))
	case kIntCast, kU32Wrap:
		v := s.pop()
		a := s.e.use(v, 32)
		if op.Str == "u8" {
			s.push(s.define(32, fmt.Sprintf("(bvand %s %s)", a, lit(255, 32))))
		} else {
			s.push(v)
		}
	default:
		w := opWidth(op)
		b := s.e.use(s.pop(), w)
		a := s.e.use(s.pop(), w)
		expr, rw := binary(op.Kind, a, b, w)
		s.push(s.define(rw, expr))
	}
}

// binary is a two-operand op's term and result width, per
// docs/INTEGER-SEMANTICS.md: arithmetic wraps, a shift count is masked to the
// width, `x / 0` is 0, `x % 0` is x, and a compare yields an i32 0 or 1. The
// SMT-LIB signed quotient and remainder already wrap MIN / -1 to MIN and give
// MIN % -1 = 0.
func binary(kind int, a, b string, w int) (string, int) {
	zero := lit(0, w)
	switch kind {
	case kShl, kShrS, kShrU:
		fn := map[int]string{kShl: "bvshl", kShrS: "bvashr", kShrU: "bvlshr"}[kind]
		return fmt.Sprintf("(%s %s (bvand %s %s))", fn, a, b, lit(int64(w-1), w)), w
	case kDivS, kDivU:
		fn := map[int]string{kDivS: "bvsdiv", kDivU: "bvudiv"}[kind]
		return fmt.Sprintf("(ite (= %s %s) %s (%s %s %s))", b, zero, zero, fn, a, b), w
	case kRemS, kRemU:
		fn := map[int]string{kRemS: "bvsrem", kRemU: "bvurem"}[kind]
		return fmt.Sprintf("(ite (= %s %s) %s (%s %s %s))", b, zero, a, fn, a, b), w
	}
	if fn, ok := map[int]string{kAdd: "bvadd", kSub: "bvsub", kMul: "bvmul", kAnd: "bvand", kOr: "bvor", kXor: "bvxor"}[kind]; ok {
		return fmt.Sprintf("(%s %s %s)", fn, a, b), w
	}
	pred := map[int]string{
		kEq: "(= %s %s)", kNe: "(not (= %s %s))",
		kLtS: "(bvslt %s %s)", kLeS: "(bvsle %s %s)", kGtS: "(bvsgt %s %s)", kGeS: "(bvsge %s %s)",
		kLtU: "(bvult %s %s)", kLeU: "(bvule %s %s)", kGtU: "(bvugt %s %s)", kGeU: "(bvuge %s %s)",
	}[kind]
	return fmt.Sprintf("(ite "+pred+" %s %s)", a, b, lit(1, 32), lit(0, 32)), 32
}

// Encode states p's equivalence as an SMT-LIB query that is unsat exactly when
// the two sides leave the same stack and the same locals for every entry stack
// and every entry value of every local. Signedness lives in the op kind
// (div_u, lt_u, ...), so an op's unsigned flag carries no meaning here.
func Encode(p Pair) (string, error) {
	for _, op := range append(append([]Op{}, p.Before...), p.After...) {
		if !Modelled(op) {
			return "", fmt.Errorf("tv: %s is outside the model; Split keeps it out of every pair", op)
		}
	}
	e := &encoder{inputs: map[string]int{}}
	before := &side{e: e, tag: "b", locals: map[int]value{}}
	after := &side{e: e, tag: "a", locals: map[int]value{}}
	for _, op := range p.Before {
		before.step(op)
	}
	for _, op := range p.After {
		after.step(op)
	}
	eqs := e.compare(before, after)
	if e.err != nil {
		return "", e.err
	}
	var q strings.Builder
	q.WriteString("; before: " + Render(p.Before) + "\n")
	q.WriteString("; after:  " + Render(p.After) + "\n")
	for _, name := range e.order {
		w := e.inputs[name]
		if w == 0 {
			w = 32
		}
		fmt.Fprintf(&q, "(declare-const %s (_ BitVec %d))\n", name, w)
	}
	for _, d := range e.defs {
		q.WriteString(d + "\n")
	}
	fmt.Fprintf(&q, "(assert (not (and true %s)))\n(check-sat)\n", strings.Join(eqs, " "))
	return q.String(), nil
}

// compare lines the two sides' final states up: the whole stack down to the
// deepest input either side read, and every local either side wrote.
func (e *encoder) compare(b, a *side) []string {
	depth := max(b.pulled, a.pulled)
	full := func(s *side) []value {
		var st []value
		for i := depth - 1; i >= s.pulled; i-- {
			st = append(st, e.input("s"+strconv.Itoa(i)))
		}
		return append(st, s.stack...)
	}
	bs, as := full(b), full(a)
	if len(bs) != len(as) {
		e.fail("the stack grows by %d before and by %d after", len(b.stack)-b.pulled, len(a.stack)-a.pulled)
		return nil
	}
	var eqs []string
	for i := range bs {
		eqs = e.equal(eqs, bs[i], as[i], fmt.Sprintf("stack slot %d", i))
	}
	slots := map[int]bool{}
	for n := range b.locals {
		slots[n] = true
	}
	for n := range a.locals {
		slots[n] = true
	}
	ns := make([]int, 0, len(slots))
	for n := range slots {
		ns = append(ns, n)
	}
	sort.Ints(ns)
	for _, n := range ns {
		eqs = e.equal(eqs, b.local(n), a.local(n), fmt.Sprintf("local %d", n))
	}
	return eqs
}

// equal adds the claim x == y, unifying the widths of inputs neither side
// used.
func (e *encoder) equal(eqs []string, x, y value, where string) []string {
	if x.term == y.term {
		return eqs
	}
	wx, wy := e.widthOf(x), e.widthOf(y)
	switch {
	case wx == 0 && wy == 0:
		e.use(x, 32)
		e.use(y, 32)
	case wx == 0:
		e.use(x, wy)
	case wy == 0:
		e.use(y, wx)
	case wx != wy:
		e.fail("%s is %d bits before and %d after", where, wx, wy)
	}
	return append(eqs, fmt.Sprintf("(= %s %s)", x.term, y.term))
}
