package ir

// Keep the argument's zero on the stack as the result. Calls without a
// definition carrying the contract, including same-named runtime helpers,
// cannot use a generated function's null behavior.
func foldNullIdentityCalls(ops []Op, identities map[string]bool) []Op {
	if len(identities) == 0 {
		return ops
	}
	var out []Op
	for i, op := range ops {
		if i > 0 && ops[i-1].Kind == OpConstI32 && ops[i-1].I32 == 0 &&
			op.Kind == OpCallDirect && !op.Runtime && op.I32 == 1 && identities[op.Str] {
			if out == nil {
				out = append(make([]Op, 0, len(ops)-1), ops[:i]...)
			}
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
