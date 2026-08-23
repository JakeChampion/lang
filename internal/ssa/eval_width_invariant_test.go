package ssa

import (
	"strings"
	"testing"
)

// Eval is the oracle the SSA backends are compared against, and it silently
// gave wrong answers for any address-carrying op that reached it unresolved
// (#7406).
//
// The failure could not surface on its own. ResolveWidths stamps Width 64 on
// every Addr-marked op; mask() narrows anything else to its low 32 bits. So an
// unresolved address was truncated — and the model heap starts at address 8
// and grows by bumping, so every address it hands out fits in 31 bits and its
// bounds check never fired. Meanwhile the real arenas are based at
// 0x4_0000_0000, so every address a COMPILED program handles is exactly the
// range the mask destroys.
//
// It stayed harmless only because ResolveWidths runs inside EmitAsmModule, and
// that was the sole caller. The invariant was never written down or checked,
// which is what made it a trap for the next caller — a hand-built function in
// a test, or a tool that wants the model without the emitter.
func TestEvalRejectsUnresolvedAddressWidth(t *testing.T) {
	// A function whose result is marked as an address but never widened, which
	// is exactly the state ResolveWidths would have fixed.
	unresolved := func() *Func {
		f := NewFunc("addr_fn")
		b := f.NewBlock()
		v := f.AddOp(b, OpConstInt)
		op := b.Ops[len(b.Ops)-1]
		op.Imm = 0x4_0000_0000 // an arena-based address: wide, and what a real one looks like
		op.Addr = true
		op.Width = 32
		f.SetRet(b, v)
		return f
	}

	t.Run("unresolved address is refused", func(t *testing.T) {
		_, err := Eval(unresolved())
		if err == nil {
			t.Fatal("Eval accepted an address-carrying op at width 32 — it would have " +
				"truncated the address to 32 bits and returned a wrong answer silently")
		}
		// The message has to name what to do about it; a bare "invalid" would
		// send the reader into the evaluator rather than to ResolveWidths.
		for _, want := range []string{"ResolveWidths", "addr_fn", "truncate"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error does not mention %q: %v", want, err)
			}
		}
	})

	t.Run("the same function is accepted once resolved", func(t *testing.T) {
		f := unresolved()
		ResolveWidths(map[string]*Func{f.Name: f})
		got, err := Eval(f)
		if err != nil {
			t.Fatalf("Eval refused a resolved function: %v", err)
		}
		// The address survives intact — this is the value the old mask would
		// have destroyed, and it is the whole point of the invariant.
		if want := int64(0x4_0000_0000); got != want {
			t.Errorf("Eval = %#x, want %#x — the address did not survive", got, want)
		}
	})

	// The control that keeps this from being a rule against 32-bit ops. A
	// narrow SCALAR is ordinary and must stay evaluable; only an op claiming to
	// hold an address is required to be 64 bits wide.
	t.Run("a narrow scalar is untouched", func(t *testing.T) {
		f := NewFunc("scalar_fn")
		b := f.NewBlock()
		v := f.AddOp(b, OpConstInt)
		op := b.Ops[len(b.Ops)-1]
		op.Imm = 7
		op.Width = 32
		f.SetRet(b, v)

		got, err := Eval(f)
		if err != nil {
			t.Fatalf("Eval refused a plain 32-bit scalar: %v", err)
		}
		if got != 7 {
			t.Errorf("Eval = %d, want 7", got)
		}
	})
}
