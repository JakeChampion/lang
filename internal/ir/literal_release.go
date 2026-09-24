package ir

// dropLiteralStrReleases deletes `load s ; call __fern_str_dec ; drop` when
// every write to local s stores a string literal. A literal is static, so
// releasing it does nothing, and neither does releasing the zero the slot
// holds before its first write. Inlining a function that returns a literal
// leaves exactly this: the call's result slot, released after its use.
// Deleting all three ops is stack-neutral whatever width the load has.
func dropLiteralStrReleases(fn *Func) []Op {
	ops := fn.Ops
	literal := map[int32]bool{}
	other := map[int32]bool{}
	for i, op := range ops {
		if op.Kind != OpStoreLocal && op.Kind != OpTeeLocal {
			continue
		}
		if i > 0 && ops[i-1].Kind == OpConstStr && int(op.I32) >= len(fn.Params) {
			literal[op.I32] = true
		} else {
			other[op.I32] = true
		}
	}
	var out []Op
	for i := 0; i < len(ops); i++ {
		op := ops[i]
		if op.Kind == OpLoadLocal && literal[op.I32] && !other[op.I32] && i+2 < len(ops) &&
			ops[i+1].Kind == OpCallDirect && ops[i+1].Runtime && ops[i+1].Str == "__fern_str_dec" &&
			ops[i+2].Kind == OpDrop {
			if out == nil {
				out = append(make([]Op, 0, len(ops)), ops[:i]...)
			}
			i += 2
			continue
		}
		if out != nil {
			out = append(out, op)
		}
	}
	if out == nil {
		return ops
	}
	return out
}
