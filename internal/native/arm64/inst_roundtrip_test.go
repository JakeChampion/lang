package arm64_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// TestInstRoundTripsThroughTheModel holds the typed operand model to the
// vocabulary the gas differential already covers: every instruction in
// gasCorpus is parsed into an Inst, rendered back, and the rebuilt program
// must assemble to the SAME BYTES as the original.
//
// Bytes rather than text, because the model normalises spellings — `#0x10`
// renders as `#16`, a `w`-register keeps its width, `[x0, #0]` loses its
// zero offset — and every one of those is a correct round trip. What must
// not change is the instruction.
//
// This is the gate that makes the model load-bearing before any dispatch
// arm consumes it. An operand kind the parser does not know refuses the
// line here rather than silently reaching an arm as raw text.
func TestInstRoundTripsThroughTheModel(t *testing.T) {
	for name, src := range gasCorpus {
		t.Run(name, func(t *testing.T) {
			want, err := arm64.Assemble(src)
			if err != nil {
				t.Fatalf("assembling the original failed: %v", err)
			}
			var b strings.Builder
			for _, line := range strings.Split(src, "\n") {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, ".") || strings.HasSuffix(trimmed, ":") {
					b.WriteString(line + "\n")
					continue
				}
				in, err := arm64.ParseInst(trimmed)
				if err != nil {
					t.Fatalf("ParseInst(%q): %v", trimmed, err)
				}
				b.WriteString("\t" + in.String() + "\n")
			}
			got, err := arm64.Assemble(b.String())
			if err != nil {
				t.Fatalf("assembling the round-tripped program failed: %v\n%s", err, b.String())
			}
			if !bytes.Equal(got, want) {
				t.Errorf("round trip changed the encoding\noriginal:\n%s\nrendered:\n%s\nwant % x\ngot  % x", src, b.String(), want, got)
			}
		})
	}
}

// TestParseInstRejectsNonsense pins that an unparseable operand is an
// error rather than a zero-valued Operand a dispatch arm would then read
// as register x0. The model has no "unknown" operand: OpUnknown is the
// zero value precisely so that a kind nobody set cannot be mistaken for a
// real one.
func TestParseInstRejectsNonsense(t *testing.T) {
	for _, line := range []string{
		"add x0, x1, %rax",
		"ldr x0, [x1",
		"mov x0, #",
		"add x0, x1, x2, qsl #3",
		"ldr x0, {x1}",
	} {
		if in, err := arm64.ParseInst(line); err == nil {
			t.Errorf("ParseInst(%q) accepted it as %v, want an error", line, in)
		}
	}
}

// TestInstConstructorsAssembleWithoutText builds a real instruction
// sequence from Operand constructors — no assembly text anywhere — and
// requires the same bytes the text form produces. That is the point of
// the model: a code generator hands over Inst values, skipping a render
// and a re-parse it would otherwise pay for every instruction.
func TestInstConstructorsAssembleWithoutText(t *testing.T) {
	x0, x1, x29, x30 := arm64.Reg(0, false), arm64.Reg(1, false), arm64.Reg(29, false), arm64.Reg(30, false)
	w2 := arm64.Reg(2, true)
	eq, ok := arm64.Cond("eq")
	if !ok {
		t.Fatal(`Cond("eq") refused a real condition`)
	}
	lsl3, ok := arm64.Shift("lsl", 3)
	if !ok {
		t.Fatal(`Shift("lsl", 3) refused a real shift`)
	}
	// uxtx #0 is the EXTENDED-register form, which is the only add/sub
	// encoding where register 31 means sp rather than xzr — so it is what a
	// frame adjust larger than 4095 bytes has to be built as.
	uxtx0, ok := arm64.Extend("uxtx", 0)
	if !ok {
		t.Fatal(`Extend("uxtx", 0) refused a real extend`)
	}

	built := []arm64.Inst{
		{Mnem: "stp", Ops: []arm64.Operand{x29, x30, arm64.Mem(31, -16, true)}},
		{Mnem: "mov", Ops: []arm64.Operand{x29, arm64.RegSP()}},
		{Mnem: "sub", Ops: []arm64.Operand{arm64.RegSP(), arm64.RegSP(), arm64.Reg(16, false), uxtx0}},
		{Mnem: "add", Ops: []arm64.Operand{x0, x1, arm64.Imm(24)}},
		{Mnem: "ldr", Ops: []arm64.Operand{x0, arm64.MemIndex(1, 2, 3, true)}},
		{Mnem: "lsl", Ops: []arm64.Operand{x0, x1, arm64.Imm(3)}},
		{Mnem: "orr", Ops: []arm64.Operand{x0, x1, x1, lsl3}},
		{Mnem: "csel", Ops: []arm64.Operand{w2, w2, arm64.Reg(3, true), eq}},
		{Mnem: "cbz", Ops: []arm64.Operand{x0, arm64.Sym("done")}},
		{Mnem: "b", Ops: []arm64.Operand{arm64.Sym("done")}},
		{Mnem: "add", Ops: []arm64.Operand{arm64.RegSP(), arm64.RegSP(), arm64.Reg(16, false), uxtx0}},
		{Mnem: "ldp", Ops: []arm64.Operand{x29, x30, arm64.Mem(31, 16, false)}},
		{Mnem: "ret", Ops: nil},
	}

	a := arm64.NewAssembler()
	for _, in := range built {
		if err := a.Inst(in); err != nil {
			t.Fatalf("Inst(%v): %v", in, err)
		}
	}
	// The branches above target it, so the label has to exist before Bytes
	// resolves them — a generator places labels the same way.
	a.TextLabel("done")
	got, err := a.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	var src strings.Builder
	for _, in := range built {
		src.WriteString("\t" + in.String() + "\n")
	}
	src.WriteString("done:\n")
	want, err := arm64.Assemble(src.String())
	if err != nil {
		t.Fatalf("assembling the text form failed: %v\n%s", err, src.String())
	}
	if !bytes.Equal(got, want) {
		t.Errorf("constructed program differs from the text form\n%s\nwant % x\ngot  % x", src.String(), want, got)
	}
}
