package ssa

import "fmt"

// Eval is a reference interpreter for the integer/control-flow subset of SSA.
// It exists as a correctness oracle: an optimisation pass must not change a
// function's result (`Eval(f) == Eval(optimize(f))`), and the SSA→native
// register allocator + emitter (#4112) are validated differentially against it
// (the emitted code's result must match `Eval`). This mirrors how the
// self-hosted compiler validates its SSA builder with an SSA interpreter
// (CLAUDE.md), and how `irlower.fern` is checked end-to-end without a backend.
//
// Scope: integer values (i32/i64 modelled as int64), booleans (0/1), the
// integer arithmetic / bitwise / shift / comparison ops, OpNot, OpSelect,
// phis, and the Br/BrIf/Ret terminators. Memory, calls, floats, strings, and
// composites are out of scope here — those gain evaluation as the emitter
// learns them, phase by phase. An unsupported op is a clear error, never a
// silent wrong answer.

// Eval interprets f with the given integer arguments (bound to f's params in
// order) and returns the value its Ret terminator produces. Width is modelled
// by masking i32 results to 32 bits; i64 ops use the full width. It cannot
// resolve OpCall — use EvalIn with a function table for programs that call.
func Eval(f *Func, args ...int64) (int64, error) {
	return EvalIn(nil, f, args...)
}

// EvalIn is Eval with a function table so OpCall can recurse into callees
// (resolved by name via Op.Str). Direct integer calls only.
func EvalIn(funcs map[string]*Func, f *Func, args ...int64) (int64, error) {
	vals := map[int32]int64{}

	params := realParams(f)
	if len(args) != len(params) {
		return 0, fmt.Errorf("Eval: got %d args, function has %d params", len(args), len(params))
	}
	for i, p := range params {
		vals[p.ID] = args[i]
	}

	cur := f.Entry
	var from *Block // predecessor we arrived from, for phi resolution
	const maxSteps = 1 << 20
	for steps := 0; ; steps++ {
		if steps > maxSteps {
			return 0, fmt.Errorf("Eval: step limit exceeded (non-terminating?)")
		}

		// Phis first, resolved against the edge we arrived on. All phis in a
		// block execute in PARALLEL: read every incoming arg before assigning
		// any result, so a phi whose arg is another phi in the same block (the
		// swap / cycle case, e.g. `a,b = b,a`) sees the old value, not one a
		// sibling phi just overwrote. (A sequential read-then-assign here is the
		// classic out-of-SSA bug.)
		var phiResults []int32
		var phiValues []int64
		for _, op := range cur.Ops {
			if op.Kind != OpPhi {
				break
			}
			if from == nil {
				return 0, fmt.Errorf("Eval: phi in entry block %d", cur.ID)
			}
			pi := predIndex(cur, from)
			if pi < 0 || pi >= len(op.Args) {
				return 0, fmt.Errorf("Eval: phi v%d has no arg for predecessor block %d", op.Result.ID, from.ID)
			}
			v, err := readVal(vals, op.Args[pi])
			if err != nil {
				return 0, err
			}
			phiResults = append(phiResults, op.Result.ID)
			phiValues = append(phiValues, v)
		}
		for k, id := range phiResults {
			vals[id] = phiValues[k]
		}

		// Then the straight-line ops.
		for _, op := range cur.Ops {
			if op.Kind == OpPhi {
				continue
			}
			if err := evalOp(funcs, op, vals); err != nil {
				return 0, err
			}
		}

		// Terminator.
		switch cur.Term.Kind {
		case TermRet:
			if !cur.Term.Value.IsValid() {
				return 0, nil // void return
			}
			return readVal(vals, cur.Term.Value)
		case TermBr:
			from, cur = cur, cur.Term.Target
		case TermBrIf:
			c, err := readVal(vals, cur.Term.Cond)
			if err != nil {
				return 0, err
			}
			if c != 0 {
				from, cur = cur, cur.Term.True
			} else {
				from, cur = cur, cur.Term.False
			}
		default:
			return 0, fmt.Errorf("Eval: unsupported terminator %v in block %d", cur.Term.Kind, cur.ID)
		}
		if cur == nil {
			return 0, fmt.Errorf("Eval: branch to nil block")
		}
	}
}

// realParams returns f's params excluding the zero sentinel at index 0.
func realParams(f *Func) []Value {
	out := make([]Value, 0, len(f.Params))
	for _, p := range f.Params {
		if p.IsValid() {
			out = append(out, p)
		}
	}
	return out
}

func readVal(vals map[int32]int64, v Value) (int64, error) {
	if !v.IsValid() {
		return 0, fmt.Errorf("Eval: read of invalid value")
	}
	x, ok := vals[v.ID]
	if !ok {
		return 0, fmt.Errorf("Eval: value v%d used before defined", v.ID)
	}
	return x, nil
}

// mask applies the op's width to a result: 32-bit ops keep the low 32 bits
// (sign-extended back to int64 so comparisons behave), 64-bit ops pass through.
func mask(width int8, v int64) int64 {
	if width == 64 {
		return v
	}
	return int64(int32(v))
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func evalOp(funcs map[string]*Func, op *Op, vals map[int32]int64) error {
	// Binary integer ops read Args[0], Args[1]; unary read Args[0].
	arg := func(i int) (int64, error) {
		if i >= len(op.Args) {
			return 0, fmt.Errorf("Eval: %v missing arg %d", op.Kind, i)
		}
		return readVal(vals, op.Args[i])
	}
	set := func(v int64) error {
		if !op.Result.IsValid() {
			return fmt.Errorf("Eval: %v has no result", op.Kind)
		}
		vals[op.Result.ID] = mask(op.Width, v)
		return nil
	}

	switch op.Kind {
	case OpConstInt:
		return set(op.Imm)
	case OpConstBool:
		return set(op.Imm)

	case OpAdd, OpSub, OpMul, OpDiv, OpDivU, OpRem, OpRemU,
		OpAnd, OpOr, OpXor, OpShl, OpShr, OpShrU:
		a, err := arg(0)
		if err != nil {
			return err
		}
		c, err := arg(1)
		if err != nil {
			return err
		}
		r, err := evalBinaryInt(op.Kind, a, c)
		if err != nil {
			return err
		}
		return set(r)

	case OpNeg:
		a, err := arg(0)
		if err != nil {
			return err
		}
		return set(-a)
	case OpNot:
		a, err := arg(0)
		if err != nil {
			return err
		}
		return set(b2i(a == 0))

	case OpEq, OpNe, OpLt, OpLtU, OpLe, OpLeU, OpGt, OpGtU, OpGe, OpGeU:
		a, err := arg(0)
		if err != nil {
			return err
		}
		c, err := arg(1)
		if err != nil {
			return err
		}
		return set(evalCompare(op.Kind, a, c))

	case OpSelect:
		cond, err := arg(0)
		if err != nil {
			return err
		}
		t, err := arg(1)
		if err != nil {
			return err
		}
		e, err := arg(2)
		if err != nil {
			return err
		}
		if cond != 0 {
			return set(t)
		}
		return set(e)

	case OpCall:
		if funcs == nil {
			return fmt.Errorf("Eval: OpCall %q requires a function table (use EvalIn)", op.Str)
		}
		callee, ok := funcs[op.Str]
		if !ok {
			return fmt.Errorf("Eval: unknown callee %q", op.Str)
		}
		argvals := make([]int64, 0, len(op.Args))
		for i := range op.Args {
			v, err := arg(i)
			if err != nil {
				return err
			}
			argvals = append(argvals, v)
		}
		r, err := EvalIn(funcs, callee, argvals...)
		if err != nil {
			return err
		}
		return set(r)

	default:
		return fmt.Errorf("Eval: unsupported op %v", op.Kind)
	}
}

func evalBinaryInt(k OpKind, a, b int64) (int64, error) {
	switch k {
	case OpAdd:
		return a + b, nil
	case OpSub:
		return a - b, nil
	case OpMul:
		return a * b, nil
	case OpDiv:
		if b == 0 {
			return 0, fmt.Errorf("Eval: division by zero")
		}
		return a / b, nil
	case OpDivU:
		if b == 0 {
			return 0, fmt.Errorf("Eval: division by zero")
		}
		return int64(uint64(a) / uint64(b)), nil
	case OpRem:
		if b == 0 {
			return 0, fmt.Errorf("Eval: remainder by zero")
		}
		return a % b, nil
	case OpRemU:
		if b == 0 {
			return 0, fmt.Errorf("Eval: remainder by zero")
		}
		return int64(uint64(a) % uint64(b)), nil
	case OpAnd:
		return a & b, nil
	case OpOr:
		return a | b, nil
	case OpXor:
		return a ^ b, nil
	case OpShl:
		return a << uint64(b), nil
	case OpShr:
		return a >> uint64(b), nil
	case OpShrU:
		return int64(uint64(a) >> uint64(b)), nil
	default:
		return 0, fmt.Errorf("Eval: not a binary int op: %v", k)
	}
}

func evalCompare(k OpKind, a, b int64) int64 {
	switch k {
	case OpEq:
		return b2i(a == b)
	case OpNe:
		return b2i(a != b)
	case OpLt:
		return b2i(a < b)
	case OpLtU:
		return b2i(uint64(a) < uint64(b))
	case OpLe:
		return b2i(a <= b)
	case OpLeU:
		return b2i(uint64(a) <= uint64(b))
	case OpGt:
		return b2i(a > b)
	case OpGtU:
		return b2i(uint64(a) > uint64(b))
	case OpGe:
		return b2i(a >= b)
	case OpGeU:
		return b2i(uint64(a) >= uint64(b))
	default:
		return 0
	}
}
