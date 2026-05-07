// Tee fusion: recognise the IR's `OpStoreLocal X; OpLoadLocal X`
// adjacency and collapse it to a single OpTeeLocal X. The pattern
// surfaces from a few places — the inliner's arg-bind round-trip,
// hand-written `var x = ...` followed by an immediate use, the
// assignment-as-expression lowering — and emitting it as a real
// tee gives the WASM backend a single `local.tee` (saves a byte and
// a load over `local.set $X; local.get $X`). The arm32 backend
// generates equivalent code either way (pop / str / push), so the
// pass is effectively WASM-only in payoff.
//
// The pass only fires when the store and load are immediately
// adjacent and address the same slot. Anything between them — even
// another op that doesn't touch the slot — preserves the original
// store / load pair (a future copy-propagation pass tackles those
// cases). Idempotent and runs in a single linear walk.

package ir

// FuseTee rewrites every `OpStoreLocal X; OpLoadLocal X` adjacency
// in prog to a single `OpTeeLocal X`. Functions without the pattern
// are unchanged.
func FuseTee(prog *Program) {
	for _, fn := range prog.Funcs {
		fn.Ops = fuseTeeOps(fn.Ops)
	}
}

func fuseTeeOps(ops []Op) []Op {
	out := make([]Op, 0, len(ops))
	for i := 0; i < len(ops); i++ {
		if i+1 < len(ops) &&
			ops[i].Kind == OpStoreLocal &&
			ops[i+1].Kind == OpLoadLocal &&
			ops[i].I32 == ops[i+1].I32 {
			out = append(out, Op{
				Kind: OpTeeLocal,
				I32:  ops[i].I32,
				Pos:  ops[i].Pos,
			})
			i++ // also consume the OpLoadLocal
			continue
		}
		out = append(out, ops[i])
	}
	return out
}
