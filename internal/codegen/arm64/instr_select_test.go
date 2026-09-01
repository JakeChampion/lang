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
