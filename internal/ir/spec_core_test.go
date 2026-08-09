// Gates spec/core.md against the instruction set it describes.
//
// A table of opcodes is the easiest kind of normative document to get
// wrong, because every row is individually plausible and nothing about
// reading it reveals that an op is missing, that a row describes an op
// that was deleted, or that an effect is off by one slot. So the table
// is matched against the enum in both directions, and its effects are
// not compared against a second transcription but RUN: each row's
// declared operand stack is built and stepped through the verifier's
// own stack model, at both pointer widths.
package ir

import (
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

const coreSpecPath = "../../spec/core.md"

// coreRow is one instruction row of the table.
type coreRow struct {
	op     string
	pops   []string
	pushes []string
	imms   []string
}

var (
	coreOpCellRe  = regexp.MustCompile("^`([a-z0-9_.]+)`$")
	coreImmCellRe = regexp.MustCompile("`([A-Za-z0-9]+)`")
)

// readCoreSpec parses the instruction rows. A row is a four-cell table
// row whose second cell holds an effect arrow — which is what separates
// the instruction tables from the notation table above them, whose rows
// are three cells wide.
func readCoreSpec(t *testing.T) []coreRow {
	t.Helper()
	raw, err := os.ReadFile(coreSpecPath)
	if err != nil {
		t.Fatalf("read %s: %v", coreSpecPath, err)
	}
	var rows []coreRow
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 4 {
			continue
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if !strings.Contains(cells[1], "→") {
			continue
		}
		m := coreOpCellRe.FindStringSubmatch(cells[0])
		if m == nil {
			t.Errorf("row with an effect has a malformed op cell %q", cells[0])
			continue
		}
		pops, pushes, ok := parseEffect(cells[1])
		if !ok {
			t.Errorf("%s has an unparseable effect %q", m[1], cells[1])
			continue
		}
		var imms []string
		for _, im := range coreImmCellRe.FindAllStringSubmatch(cells[2], -1) {
			imms = append(imms, im[1])
		}
		rows = append(rows, coreRow{op: m[1], pops: pops, pushes: pushes, imms: imms})
	}
	if len(rows) == 0 {
		t.Fatalf("%s has no instruction rows — the parse is broken, not the document", coreSpecPath)
	}
	return rows
}

// parseEffect splits a `pops → pushes` cell into its symbol lists.
func parseEffect(cell string) (pops, pushes []string, ok bool) {
	cell = strings.Trim(cell, "`")
	sides := strings.Split(cell, "→")
	if len(sides) != 2 {
		return nil, nil, false
	}
	side := func(s string) []string {
		s = strings.TrimSpace(s)
		if s == "—" {
			return nil
		}
		return strings.Fields(s)
	}
	return side(sides[0]), side(sides[1]), true
}

// mnemonics maps every op kind to its mnemonic. A kind whose String()
// has no case answers "<invalid>", which is reported rather than keyed.
func mnemonics(t *testing.T) map[string]OpKind {
	t.Helper()
	out := map[string]OpKind{}
	for k := OpInvalid + 1; k < opKindCount; k++ {
		name := k.String()
		if name == "<invalid>" {
			t.Errorf("op kind %d has no String() case, so it has no name to document", int(k))
			continue
		}
		if prev, dup := out[name]; dup {
			t.Errorf("op kinds %d and %d share the mnemonic %q", int(prev), int(k), name)
		}
		out[name] = k
	}
	return out
}

func TestCoreOpsIndexIsAccurate(t *testing.T) {
	rows := readCoreSpec(t)
	byName := mnemonics(t)

	documented := map[string]bool{}
	for _, r := range rows {
		if documented[r.op] {
			t.Errorf("%s is listed twice", r.op)
		}
		documented[r.op] = true
		if _, ok := byName[r.op]; !ok {
			t.Errorf("%s has a row but is not an op kind — it was renamed or deleted", r.op)
		}
		for _, im := range r.imms {
			if !opHasField(im) {
				t.Errorf("%s names the immediate %q, which is not a field of Op or OpExt", r.op, im)
			}
		}
	}
	for name := range byName {
		if !documented[name] {
			t.Errorf("op %s has no row in %s — an instruction in the language that the "+
				"specification does not mention", name, coreSpecPath)
		}
	}
	t.Logf("%d instructions documented", len(rows))
}

// opHasField reports whether name is a field of Op or of its Ext block,
// which is what an Imm. cell is allowed to name.
func opHasField(name string) bool {
	for _, s := range []any{Op{}, OpExt{}} {
		if _, ok := reflect.TypeOf(s).FieldByName(name); ok {
			return true
		}
	}
	return false
}

// coreFixture supplies the context an op needs before its effect can be
// observed: the enclosing function, the immediates, the callees, and
// concrete instantiations for the symbolic shapes the row uses. It
// supplies INPUTS only — the expected effect comes from the document.
type coreFixture struct {
	label      string
	fn         *Func
	op         Op
	known      map[string]*Func
	tType      ast.Type // instantiates `T`
	rType      ast.Type // instantiates `R`
	argTypes   []ast.Type
	varN       int // instantiates `*…`
	openScopes []openScope
}

type openScope struct {
	kind OpKind
	bt   int32
}

func coreFixtures(op OpKind, mnemonic string) []coreFixture {
	// One type of each shape the operand stack distinguishes: an
	// integer word, a float, and the one type that is a PAIR under the
	// two-word ABI and a single word otherwise.
	shapes := []struct {
		name string
		t    ast.Type
	}{
		{"i32", ast.NumberType{Width: 32}},
		{"f64", ast.FloatType{Width: 64}},
		{"string", ast.StringType{}},
	}

	switch op {
	case OpLoadLocal, OpStoreLocal, OpTeeLocal:
		var out []coreFixture
		for _, sh := range shapes {
			out = append(out, coreFixture{
				label: "local of type " + sh.name,
				fn:    &Func{Name: "f", ReturnType: ast.VoidType{}, Params: []ast.Param{{Type: sh.t}}},
				op:    Op{Kind: op},
				tType: sh.t,
			})
		}
		return out

	case OpReturn:
		var out []coreFixture
		for _, sh := range shapes {
			out = append(out, coreFixture{
				label: "returning " + sh.name,
				fn:    &Func{Name: "f", ReturnType: sh.t},
				op:    Op{Kind: op},
				tType: sh.t,
			})
		}
		return out

	case OpElse:
		return []coreFixture{{op: Op{Kind: op}, openScopes: []openScope{{OpIf, BlockTypeVoid}}}}
	case OpEnd:
		return []coreFixture{{op: Op{Kind: op}, openScopes: []openScope{{OpBlock, BlockTypeVoid}}}}

	case OpMakeClosure, OpMakeEnv:
		return []coreFixture{{op: Op{Kind: op, I32: 3}, varN: 3}}

	case OpCallDirect, OpCallDirectPair, OpCallClosureDirect:
		var out []coreFixture
		for _, sh := range shapes {
			args := []ast.Type{sh.t}
			out = append(out, coreFixture{
				label:    sh.name + " argument and result",
				op:       Op{Kind: op, Str: "g", I32: 1, Ext: &OpExt{ArgTypes: args}},
				known:    map[string]*Func{"g": {Name: "g", Params: []ast.Param{{Type: sh.t}}, ReturnType: sh.t}},
				argTypes: args,
				rType:    sh.t,
			})
		}
		return out

	case OpCallIndirect:
		var out []coreFixture
		for _, sh := range shapes {
			args := []ast.Type{sh.t}
			out = append(out, coreFixture{
				label:    sh.name + " argument and result",
				op:       Op{Kind: op, I32: 1, Ext: &OpExt{Sig: &ast.FuncType{Params: args, Result: sh.t}}},
				argTypes: args,
				rType:    sh.t,
			})
		}
		return out

	case OpCallDyn:
		// The signature is receiver-first and I32 is the METHOD'S VTABLE
		// SLOT, not an argument count. A fixture where the two coincide
		// checks neither reading, so the slot here is deliberately
		// nothing like the argument count.
		var out []coreFixture
		recv := ast.NumberType{Width: 32}
		for _, sh := range shapes {
			args := []ast.Type{sh.t}
			out = append(out, coreFixture{
				label: sh.name + " argument and result",
				op: Op{Kind: op, I32: 5, Ext: &OpExt{
					Sig: &ast.FuncType{Params: append([]ast.Type{recv}, args...), Result: sh.t},
				}},
				argTypes: args,
				rType:    sh.t,
			})
		}
		return out
	}
	return []coreFixture{{op: Op{Kind: op}}}
}

func TestCoreOpEffectsMatchTheModel(t *testing.T) {
	rows := readCoreSpec(t)
	byName := mnemonics(t)

	checked := 0
	for _, r := range rows {
		kind, ok := byName[r.op]
		if !ok {
			continue // already reported by the index gate
		}
		for _, fx := range coreFixtures(kind, r.op) {
			for _, ptrW := range []int{4, 8} {
				checkCoreEffect(t, r, fx, ptrW)
				checked++
			}
		}
	}
	t.Logf("ran %d instruction effects through the stack model", checked)
}

func checkCoreEffect(t *testing.T, r coreRow, fx coreFixture, ptrW int) {
	t.Helper()
	fn := fx.fn
	if fn == nil {
		fn = &Func{Name: "f", ReturnType: ast.VoidType{}}
	}
	known := fx.known
	if known == nil {
		known = map[string]*Func{}
	}
	s := &stackChecker{
		f: fn, known: known, externs: map[string]*ExternFunc{},
		ptrW: ptrW, twoWordStr: ast.UseTwoWordStrings(ptrW),
	}
	ret := s.typeSlots(fn.ReturnType)
	s.frames = []ctrlFrame{{kind: OpInvalid, at: -1, height: 0, labelSlots: ret, endSlots: ret}}
	for _, sc := range fx.openScopes {
		end, ok := blockSlots(sc.bt)
		if !ok {
			t.Fatalf("%s: fixture opens a scope with an unknown block type", r.op)
		}
		label := end
		if sc.kind == OpLoop {
			label = nil
		}
		s.frames = append(s.frames, ctrlFrame{
			kind: sc.kind, at: 0, height: len(s.stack), labelSlots: label, endSlots: end,
		})
	}

	where := r.op
	if fx.label != "" {
		where += " (" + fx.label + ")"
	}
	where += " at pointer width " + strconv.Itoa(ptrW)

	want, ok := s.expandSymbols(r.pops, fx)
	if !ok {
		t.Errorf("%s: the effect's operand side uses a symbol the gate cannot instantiate: %v", where, r.pops)
		return
	}
	s.stack = append(s.stack, want...)

	s.step(0, fx.op)

	if s.bail != "" {
		t.Errorf("%s: the stack model could not follow the op (%s), so the row is unchecked", where, s.bail)
		return
	}
	if len(s.problems) > 0 {
		t.Errorf("%s: the row's operands were rejected by the stack model: %s", where, s.problems[0].Msg)
		return
	}
	got := s.stack
	wantPush, ok := s.expandSymbols(r.pushes, fx)
	if !ok {
		t.Errorf("%s: the effect's result side uses a symbol the gate cannot instantiate: %v", where, r.pushes)
		return
	}
	if !sameSlots(got, wantPush) {
		t.Errorf("%s: the document says the op leaves %s, but the model leaves %s",
			where, slotsString(wantPush), slotsString(got))
	}
}

// expandSymbols turns a row's effect symbols into concrete operand
// slots for this fixture and pointer width.
func (s *stackChecker) expandSymbols(syms []string, fx coreFixture) ([]valKind, bool) {
	var out []valKind
	for _, sym := range syms {
		switch sym {
		case "i", "*":
			out = append(out, kInt)
		case "f":
			out = append(out, kFloat)
		case "s":
			out = append(out, make([]valKind, s.strSlots())...)
		case "T":
			if fx.tType == nil {
				return nil, false
			}
			out = append(out, s.typeSlots(fx.tType)...)
		case "R":
			if fx.rType == nil {
				return nil, false
			}
			out = append(out, s.typeSlots(fx.rType)...)
		case "A…":
			if fx.argTypes == nil {
				return nil, false
			}
			for _, a := range fx.argTypes {
				out = append(out, s.typeSlots(a)...)
			}
		case "*…":
			if fx.varN == 0 {
				return nil, false
			}
			out = append(out, make([]valKind, fx.varN)...)
		default:
			return nil, false
		}
	}
	return out, true
}

func sameSlots(a, b []valKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func slotsString(ks []valKind) string {
	if len(ks) == 0 {
		return "nothing"
	}
	var parts []string
	for _, k := range ks {
		parts = append(parts, k.String())
	}
	return strings.Join(parts, " ")
}
