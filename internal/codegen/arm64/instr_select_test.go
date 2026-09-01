package arm64

// Instruction-selection tests for the constant-operand folds in the arm64
// emitter: a constant that feeds a load's address, an ALU op, or a compare is
// selected as that instruction's immediate operand instead of being
// materialised into a register first.
//
// Each fold has two halves under test. The encodability predicate is unit
// tested directly — a wrong answer there is either a lost fold (silent) or an
// assembler error / silently truncated field (loud but late). The selection
// itself is asserted on emitted text: the folded form appears AND the
// materialise-then-combine form it replaces does not.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

func TestFoldableConst(t *testing.T) {
	cases := []struct {
		name string
		op   ir.Op
		want int64
		ok   bool
	}{
		{"i32 zero", ir.Op{Kind: ir.OpConstI32, I32: 0}, 0, true},
		{"i32 small", ir.Op{Kind: ir.OpConstI32, I32: 24}, 24, true},
		{"i32 large", ir.Op{Kind: ir.OpConstI32, I32: 1 << 20}, 1 << 20, true},
		// A negative i32 is materialised zero-extended (0xffffffff for -1), so
		// folding the signed value would change the computed result.
		{"i32 negative", ir.Op{Kind: ir.OpConstI32, I32: -1}, 0, false},
		{"i64 small", ir.Op{Kind: ir.OpConstI64, I64: 8}, 8, true},
		{"i64 negative", ir.Op{Kind: ir.OpConstI64, I64: -8}, 0, false},
		{"not a const", ir.Op{Kind: ir.OpAdd}, 0, false},
		{"f64 const", ir.Op{Kind: ir.OpConstF64, F64: 1}, 0, false},
	}
	for _, c := range cases {
		got, ok := foldableConst(c.op)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: foldableConst = (%d, %v), want (%d, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestScaledOffsetOK(t *testing.T) {
	cases := []struct {
		off, size int64
		want      bool
	}{
		{0, 8, true},
		{8, 8, true},
		{4, 8, false},     // not a multiple of the access size
		{32760, 8, true},  // 4095 * 8, the largest encodable
		{32768, 8, false}, // 4096 * 8, one past the 12-bit field
		{16380, 4, true},  // 4095 * 4
		{16384, 4, false}, // 4096 * 4
		{4095, 1, true},   // byte access needs no alignment
		{4096, 1, false},  //
		{-8, 8, false},    // negative never reaches the scaled form
		{2, 4, false},     //
		{1000000, 8, false},
	}
	for _, c := range cases {
		if got := scaledOffsetOK(c.off, c.size); got != c.want {
			t.Errorf("scaledOffsetOK(%d, %d) = %v, want %v", c.off, c.size, got, c.want)
		}
	}
}

func TestLoadFoldForm(t *testing.T) {
	cases := []struct {
		name string
		op   ir.Op
		want addrFoldLoad
		ok   bool
	}{
		{"i64 load", ir.Op{Kind: ir.OpLoad, Width: 64}, addrFoldLoad{"ldr", "x0", 8}, true},
		{"ptr load", ir.Op{Kind: ir.OpLoad, Width: ir.WidthPtr}, addrFoldLoad{"ldr", "x0", 8}, true},
		{"i32 load", ir.Op{Kind: ir.OpLoad, Width: 32}, addrFoldLoad{"ldr", "w0", 4}, true},
		{"default-width load", ir.Op{Kind: ir.OpLoad}, addrFoldLoad{"ldr", "w0", 4}, true},
		// WidthString fans one address into two reads; there is no single
		// addressing mode to fold into.
		{"string load", ir.Op{Kind: ir.OpLoad, Width: ir.WidthString}, addrFoldLoad{}, false},
		{"match tag", ir.Op{Kind: ir.OpMatchTag}, addrFoldLoad{"ldr", "w0", 4}, true},
		{"byte load", ir.Op{Kind: ir.OpLoadByte}, addrFoldLoad{"ldrb", "w0", 1}, true},
		{"store", ir.Op{Kind: ir.OpStore, Width: 64}, addrFoldLoad{}, false},
		{"add", ir.Op{Kind: ir.OpAdd}, addrFoldLoad{}, false},
	}
	for _, c := range cases {
		got, ok := loadFoldForm(c.op)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: loadFoldForm = (%+v, %v), want (%+v, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// addThenDeref matches the unfolded address shape the load fold replaces: a
// computed address in x0 dereferenced at offset zero on the next line.
var addThenDeref = regexp.MustCompile(`\tadd x0, x1, x0\n\t(ldr|ldrb) [wx]0, \[x0\]\n`)

const fieldReadSrc = `
struct P { a: i32, b: i32, c: i32 }
function get(p: P): i32 { return p.c; }
function main(): i32 { var p: P = P{ a: 1, b: 2, c: 3 }; return get(p); }`

func TestConstantFieldOffsetFoldsIntoLoad(t *testing.T) {
	asm := compile(t, fieldReadSrc, Options{})
	if !strings.Contains(asm, "ldr w0, [x0, #8]") {
		t.Errorf("field read at offset 8 must fold into the load's addressing mode (ldr w0, [x0, #8]); asm:\n%s", asm)
	}
	if addThenDeref.MatchString(asm) {
		t.Errorf("no separate address `add` may survive in front of a zero-offset dereference; asm:\n%s", asm)
	}
}

// The fold must not change what the program computes. Reading each field of a
// three-field struct through the folded form still has to observe the values
// the struct was built with — a mis-scaled offset (folding a byte offset as an
// element index, say) would read a neighbouring field and still assemble.
func TestFoldedFieldOffsetsStayDistinct(t *testing.T) {
	asm := compile(t, `
struct P { a: i32, b: i32, c: i32 }
function main(): i32 {
    var p: P = P{ a: 1, b: 2, c: 3 };
    return p.a * 100 + p.b * 10 + p.c;
}`, Options{})
	for _, want := range []string{"ldr w0, [x0]", "ldr w0, [x0, #4]", "ldr w0, [x0, #8]"} {
		if !strings.Contains(asm, want) {
			t.Errorf("expected a folded load %q for the three distinct field offsets; asm:\n%s", want, asm)
		}
	}
}

func TestImm12OK(t *testing.T) {
	cases := []struct {
		v    int64
		want bool
	}{
		{0, true}, {1, true}, {4095, true},
		{4096, true},     // 1 << 12, the shifted form
		{4097, false},    // neither unshifted nor a clean lsl #12
		{8192, true},     // 2 << 12
		{16773120, true}, // 4095 << 12, the largest encodable
		{16777216, false},
		{-1, false}, {-4096, false},
	}
	for _, c := range cases {
		if got := imm12OK(c.v); got != c.want {
			t.Errorf("imm12OK(%d) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestBitmaskImmOK(t *testing.T) {
	// The logical-immediate class encodes a repeating run of set bits, so
	// masks encode and the two degenerate all-same values do not.
	for _, v := range []int64{1, 3, 7, 255, 0xffff, 0xff00, 0x5555555555555555} {
		if !bitmaskImmOK(v) {
			t.Errorf("bitmaskImmOK(%#x) = false, want true", v)
		}
	}
	for _, v := range []int64{0, -1, 5, 11, 0x123} {
		if bitmaskImmOK(v) {
			t.Errorf("bitmaskImmOK(%#x) = true, want false", v)
		}
	}
}

func TestAluImmForm(t *testing.T) {
	cases := []struct {
		kind ir.OpKind
		k    int64
		mnem string
		ok   bool
	}{
		{ir.OpAdd, 24, "add", true},
		{ir.OpAdd, 4097, "add", false},
		{ir.OpSub, 1, "sub", true},
		{ir.OpAnd, 255, "and", true},
		// 5 is not a repeating run of set bits, so it has no bitmask form —
		// the imm12 range test would have wrongly accepted it.
		{ir.OpAnd, 5, "and", false},
		{ir.OpOr, 0xff00, "orr", true},
		{ir.OpXor, 0, "eor", false},
		{ir.OpMul, 2, "", false},
		{ir.OpDivS, 2, "", false},
	}
	for _, c := range cases {
		mnem, ok := aluImmForm(c.kind, c.k)
		if ok != c.ok || (ok && mnem != c.mnem) {
			t.Errorf("aluImmForm(%v, %d) = (%q, %v), want (%q, %v)", c.kind, c.k, mnem, ok, c.mnem, c.ok)
		}
	}
}

func TestShiftImmForm(t *testing.T) {
	cases := []struct {
		name string
		op   ir.Op
		k    int64
		mnem string
		ok   bool
	}{
		{"shl i32", ir.Op{Kind: ir.OpShl, Width: 32}, 2, "lsl", true},
		{"shl i32 at width", ir.Op{Kind: ir.OpShl, Width: 32}, 32, "", false},
		{"shl i64 at 32", ir.Op{Kind: ir.OpShl, Width: 64}, 32, "lsl", true},
		{"shl i64 at width", ir.Op{Kind: ir.OpShl, Width: 64}, 64, "", false},
		{"shr signed", ir.Op{Kind: ir.OpShrS, Width: 32}, 3, "asr", true},
		{"shr unsigned", ir.Op{Kind: ir.OpShrS, Width: 32, Unsigned: true}, 3, "lsr", true},
		{"add is not a shift", ir.Op{Kind: ir.OpAdd, Width: 32}, 3, "", false},
	}
	for _, c := range cases {
		mnem, ok := shiftImmForm(c.op, c.k)
		if ok != c.ok || (ok && mnem != c.mnem) {
			t.Errorf("%s: shiftImmForm = (%q, %v), want (%q, %v)", c.name, mnem, ok, c.mnem, c.ok)
		}
	}
}

func TestPlanMoveWide(t *testing.T) {
	cases := []struct {
		name   string
		v      uint64
		chunks int
		movn   bool
		cost   int
	}{
		{"zero", 0, 4, false, 1},
		{"small", 42, 4, false, 1},
		{"high lane only", 0x1_0000, 4, false, 1},
		{"two lanes", 0x1_0001, 4, false, 2},
		// -1 is one movn, not four movz/movk.
		{"minus one", ^uint64(0), 4, true, 1},
		{"minus two", ^uint64(1), 4, true, 1},
		{"minus 0x10000", ^uint64(0xffff), 4, true, 1},
		{"mostly ones", 0xffff_ffff_0000_ffff, 4, true, 1},
		// A tie keeps the movz root — the same sequence length either way.
		{"tie prefers movz", 0xffff_0000_0000_ffff, 4, false, 2},
		{"mixed, movz wins", 0x0000_0001_0000_0001, 4, false, 2},
		{"32-bit all ones", 0xffff_ffff, 2, true, 1},
		{"32-bit small", 7, 2, false, 1},
	}
	for _, c := range cases {
		p := planMoveWide(c.v, c.chunks)
		if p.movn != c.movn || p.cost() != c.cost {
			t.Errorf("%s: planMoveWide(%#x) movn=%v cost=%d, want movn=%v cost=%d",
				c.name, c.v, p.movn, p.cost(), c.movn, c.cost)
		}
	}
}

// planMoveWide is only allowed to be cheaper, never wrong: replaying the plan
// must reproduce the constant exactly. Checked over every 16-bit lane pattern
// built from the interesting lane values, which is where the movz/movn choice
// and the "lane already matches the default" skip interact.
func TestPlanMoveWideReconstructs(t *testing.T) {
	lanes := []uint64{0, 1, 0x7fff, 0x8000, 0xfffe, 0xffff}
	for _, a := range lanes {
		for _, b := range lanes {
			for _, c := range lanes {
				for _, d := range lanes {
					v := a | b<<16 | c<<32 | d<<48
					if got := replayMoveWide(planMoveWide(v, 4), v, 64); got != v {
						t.Fatalf("64-bit plan for %#x reconstructs %#x", v, got)
					}
					w := a | b<<16
					if got := replayMoveWide(planMoveWide(w, 2), w, 32); got != w {
						t.Fatalf("32-bit plan for %#x reconstructs %#x", w, got)
					}
				}
			}
		}
	}
}

// replayMoveWide executes a plan the way the hardware would: the root move
// writes its lane and sets every other lane to the root form's default, then
// each movk overwrites one lane.
func replayMoveWide(p moveWidePlan, v uint64, width uint) uint64 {
	var acc uint64
	if p.movn {
		acc = ^uint64(0)
	}
	if p.root >= 0 {
		lane := p.rootImm
		if p.movn {
			lane = ^lane & 0xffff
		}
		acc = acc&^(0xffff<<(16*uint(p.root))) | lane<<(16*uint(p.root))
		if p.movn {
			// movn clears nothing else: every other lane is all-ones.
			acc = ^uint64(0)&^(0xffff<<(16*uint(p.root))) | lane<<(16*uint(p.root))
		}
	}
	for _, i := range p.fill {
		lane := (v >> (16 * i)) & 0xffff
		acc = acc&^(0xffff<<(16*uint(i))) | lane<<(16*uint(i))
	}
	if width == 32 {
		acc &= 0xffff_ffff
	}
	return acc
}

func TestConstantAluOperandFoldsToImmediate(t *testing.T) {
	asm := compile(t, `
@noinline function f(a: i32): i32 { return (a + 7) - 3; }
function main(): i32 { return f(1); }`, Options{})
	body := fnBody(t, asm, "f")
	for _, want := range []string{"add x0, x0, #7", "sub x0, x0, #3"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected the folded immediate form %q; body:\n%s", want, body)
		}
	}
	if strings.Contains(body, "add x0, x1, x0") || strings.Contains(body, "sub x0, x1, x0") {
		t.Errorf("the materialise-then-combine register form must be gone; body:\n%s", body)
	}
	if regexp.MustCompile(`\tmov x0, #(7|3)\n`).MatchString(body) {
		t.Errorf("the constant must not be materialised into a register; body:\n%s", body)
	}
}

func TestConstantCompareFoldsToImmediate(t *testing.T) {
	asm := compile(t, `
@noinline function f(a: i32): i32 { if (a > 10) { return 1; } return 2; }
function main(): i32 { return f(1); }`, Options{})
	body := fnBody(t, asm, "f")
	if !strings.Contains(body, "cmp w0, #10") {
		t.Errorf("expected the folded `cmp w0, #10`; body:\n%s", body)
	}
	if strings.Contains(body, "cmp w1, w0") {
		t.Errorf("the two-register compare must be gone; body:\n%s", body)
	}
}

// A comparison against zero needs no compare instruction at all.
func TestZeroCompareSelectsCbz(t *testing.T) {
	asm := compile(t, `
@noinline function f(a: i32): i32 { if (a == 0) { return 1; } return 2; }
function main(): i32 { return f(1); }`, Options{})
	body := fnBody(t, asm, "f")
	if !regexp.MustCompile(`\tcbnz w0, \.LifElse`).MatchString(body) {
		t.Errorf("`if (a == 0)` should branch to the else arm with a bare cbnz; body:\n%s", body)
	}
	if strings.Contains(body, "cmp w0, #0") {
		t.Errorf("a compare against zero must not emit a `cmp`; body:\n%s", body)
	}
}

func TestConstantShiftFoldsToImmediate(t *testing.T) {
	asm := compile(t, `
@noinline function f(a: i32): i32 { return (a << 3) >> 1; }
function main(): i32 { return f(4); }`, Options{})
	body := fnBody(t, asm, "f")
	for _, want := range []string{"lsl w0, w0, #3", "asr w0, w0, #1"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected the folded shift %q; body:\n%s", want, body)
		}
	}
	if regexp.MustCompile(`\t(lsl|asr) w0, w1, w0\n`).MatchString(body) {
		t.Errorf("the register-count shift form must be gone; body:\n%s", body)
	}
}

// The rc guard's below-heap range test: `x0 < 0x1000_0000` is the top 36 bits
// being clear, which is a shift and a compare-with-zero rather than a
// materialised bound and a flag-setting compare.
func TestRcBelowHeapGuardIsTwoInstructions(t *testing.T) {
	asm := compile(t, `@noinline function g(a: i32[]): i32[] { var b: i32[] = a; return b; }
function main(): i32 { var x: i32[] = [1, 2, 3]; var y: i32[] = g(x); return y[0]; }`, Options{})
	if !regexp.MustCompile(`\tlsr x1, x0, #28\n\tcbz x1, `).MatchString(asm) {
		t.Errorf("the below-heap guard should be `lsr x1, x0, #28` + `cbz`; asm:\n%s", asm)
	}
	// The materialised-bound form it replaces.
	if strings.Contains(asm, "lsl x1, x1, #28") {
		t.Errorf("the materialised 0x1000_0000 bound must be gone; asm:\n%s", asm)
	}
	if regexp.MustCompile(`\tcmp x0, x1\n\tb\.lo `).MatchString(asm) {
		t.Errorf("the compare-against-a-register bound must be gone; asm:\n%s", asm)
	}
}

func TestInvertCondBranch(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"b.eq", "b.ne", true},
		{"b.ne", "b.eq", true},
		{"b.lo", "b.hs", true},
		{"b.hs", "b.lo", true},
		{"b.gt", "b.le", true},
		{"b.cc", "b.cs", true},
		{"cbz", "cbnz", true},
		{"cbnz", "cbz", true},
		{"tbz", "tbnz", true},
		{"tbnz", "tbz", true},
		// Not reach-limited (or not a branch at all).
		{"b", "", false},
		{"bl", "", false},
		{"blr", "", false},
		{"ret", "", false},
		{"cset", "", false},
		{"b.al", "", false},
	}
	for _, c := range cases {
		got, ok := invertCondBranch(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("invertCondBranch(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestSplitCondBranch(t *testing.T) {
	cases := []struct {
		line               string
		mnem, head, target string
		ok                 bool
	}{
		{"\tb.eq .LifEnd_3", "b.eq", "", ".LifEnd_3", true},
		{"\tcbz w0, .LifElse_2", "cbz", "w0, ", ".LifElse_2", true},
		{"\ttbnz x0, #0, .LrcopDone_9", "tbnz", "x0, #0, ", ".LrcopDone_9", true},
		{"\tb .Lret_f_1", "", "", "", false},
		{"\tadd x0, x0, #8", "", "", "", false},
		{".LifEnd_3:", "", "", "", false},
		{"\t.size __fn_f, .-__fn_f", "", "", "", false},
		{"", "", "", "", false},
	}
	for _, c := range cases {
		mnem, head, target, ok := splitCondBranch([]byte(c.line))
		if ok != c.ok || (ok && (mnem != c.mnem || head != c.head || target != c.target)) {
			t.Errorf("splitCondBranch(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
				c.line, mnem, head, target, ok, c.mnem, c.head, c.target, c.ok)
		}
	}
}

func TestCountInstrs(t *testing.T) {
	text := "__fn_f:\n\tadd x0, x0, #1\n\t.loc 1 2 3\n.LifEnd_1:\n\tret\n\n"
	if got := countInstrs([]byte(text)); got != 2 {
		t.Errorf("countInstrs = %d, want 2 (directives, labels and blanks are not instructions)", got)
	}
}

// expandFarCondBranches is the cold half of the direct-branch selection: it
// only runs on a function too large for b.cond's reach, which in a whole
// self-host build happens to exactly one branch. Exercise it directly rather
// than relying on that one site.
func TestExpandFarCondBranchesOnlyTouchesOutOfReach(t *testing.T) {
	var body strings.Builder
	body.WriteString("__fn_big:\n")
	body.WriteString("\tb.eq .Lfar\n")     // out of reach: expands
	body.WriteString("\tcbz w0, .Lnear\n") // in reach: stays
	body.WriteString(".Lnear:\n")
	for i := 0; i < condBranchReachInstrs+10; i++ {
		body.WriteString("\tnop\n")
	}
	body.WriteString(".Lfar:\n\tret")

	g := &generator{}
	out, changed := g.expandFarCondBranches([]byte(body.String()))
	if !changed {
		t.Fatal("a branch past the reach limit must be expanded")
	}
	got := string(out)
	m := regexp.MustCompile(`\tb\.ne (\.LbrFar_\d+)\n\tb \.Lfar\n(\.LbrFar_\d+):\n`).FindStringSubmatch(got)
	if m == nil || m[1] != m[2] {
		t.Errorf("out-of-reach `b.eq .Lfar` should become the inverted test over `b .Lfar` with a matching skip label; got:\n%s", got[:200])
	}
	if !strings.Contains(got, "\tcbz w0, .Lnear\n") {
		t.Errorf("an in-reach branch must be left alone; got:\n%s", got[:200])
	}
	// A second pass has nothing left to do — the expansion reached a fixpoint.
	if _, again := g.expandFarCondBranches(out); again {
		t.Error("expansion did not reach a fixpoint in one round on this body")
	}
}

// A branch whose target is not a label of this function has no measurable
// distance, so it must expand rather than be assumed close.
func TestExpandFarCondBranchesExpandsUnknownTarget(t *testing.T) {
	body := "__fn_f:\n\tb.eq .Lsomewhere_else\n\tret"
	g := &generator{}
	out, changed := g.expandFarCondBranches([]byte(body))
	if !changed || !strings.Contains(string(out), "b .Lsomewhere_else") {
		t.Errorf("an unmeasurable target must expand to the unconditional form; got:\n%s", out)
	}
}
