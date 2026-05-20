package ssa

import (
	"fmt"
	"strconv"
	"strings"
)

// String renders `f` in a stable, human-readable text form
// suitable for golden-file tests and for `lang dump-ssa`-style
// debug output. The format mirrors what LLVM IR + Go's SSA
// dumper converged on:
//
//	func f(v1, v2):
//	  block 1:
//	    v3 = add v1, v2
//	    ret v3
//	  block 2:
//	    v4 = const_int 7
//	    br block 3
//
// Block ordering follows `f.Blocks` (insertion order). Within
// a block, Ops are printed in order, terminator last. Operand
// formatting:
//
//   - Ops with a Result: `<result> = <kind> <operands>`
//   - Side-effect-only Ops: `<kind> <operands>` (no LHS)
//   - OpConstInt: prints the Imm value (`const_int 42`)
//   - OpConstString: prints the Str field in Go-quoted form
//   - OpCall: prints the callee name from Str (`call foo, v1, v2`)
//
// Terminator forms:
//
//   - br block N
//   - brif <cond>, block T, block F
//   - ret             (void return)
//   - ret <value>     (typed return)
//   - <invalid>       (no terminator set — Verify rejects)
func (f *Func) String() string {
	if f == nil {
		return "<nil func>"
	}
	var b strings.Builder
	b.WriteString("func ")
	b.WriteString(f.Name)
	b.WriteByte('(')
	for i, p := range f.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.String())
	}
	b.WriteString("):\n")
	for _, blk := range f.Blocks {
		writeBlock(&b, blk)
	}
	return b.String()
}

func writeBlock(b *strings.Builder, blk *Block) {
	fmt.Fprintf(b, "  block %d:\n", blk.ID)
	for _, op := range blk.Ops {
		b.WriteString("    ")
		writeOp(b, op, blk)
		b.WriteByte('\n')
	}
	b.WriteString("    ")
	writeTerm(b, &blk.Term)
	b.WriteByte('\n')
}

func writeOp(b *strings.Builder, op *Op, blk *Block) {
	if op == nil {
		b.WriteString("<nil op>")
		return
	}
	if op.Result.IsValid() && op.Result2.IsValid() {
		fmt.Fprintf(b, "%s, %s = ", op.Result, op.Result2)
	} else if op.Result.IsValid() {
		b.WriteString(op.Result.String())
		b.WriteString(" = ")
	}
	b.WriteString(op.Kind.String())
	switch op.Kind {
	case OpPhi:
		// `phi v1 [block 2], v2 [block 3]` — pair each arg
		// with its pred-block per the parallel slice contract.
		for i, a := range op.Args {
			if i == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteString(", ")
			}
			b.WriteString(a.String())
			if blk != nil && i < len(blk.Preds) {
				fmt.Fprintf(b, " [block %d]", blk.Preds[i].ID)
			} else {
				b.WriteString(" [block ?]")
			}
		}
	case OpConstInt, OpConstBool:
		b.WriteByte(' ')
		b.WriteString(strconv.FormatInt(op.Imm, 10))
	case OpConstFloat:
		b.WriteByte(' ')
		b.WriteString(strconv.FormatFloat(op.F64, 'g', -1, 64))
	case OpConstString:
		b.WriteByte(' ')
		b.WriteString(strconv.Quote(op.Str))
	case OpCall:
		b.WriteByte(' ')
		b.WriteString(strconv.Quote(op.Str))
		for _, a := range op.Args {
			b.WriteString(", ")
			b.WriteString(a.String())
		}
	default:
		for i, a := range op.Args {
			if i == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteString(", ")
			}
			b.WriteString(a.String())
		}
	}
}

func writeTerm(b *strings.Builder, t *Terminator) {
	switch t.Kind {
	case TermBr:
		fmt.Fprintf(b, "br block %d", blockID(t.Target))
	case TermBrIf:
		fmt.Fprintf(b, "brif %s, block %d, block %d",
			t.Cond, blockID(t.True), blockID(t.False))
	case TermRet:
		if t.Value.IsValid() {
			fmt.Fprintf(b, "ret %s", t.Value)
		} else {
			b.WriteString("ret")
		}
	case TermRetPair:
		fmt.Fprintf(b, "ret_pair %s, %s", t.Value, t.Value2)
	default:
		b.WriteString("<invalid>")
	}
}

func blockID(b *Block) int32 {
	if b == nil {
		return -1
	}
	return b.ID
}
