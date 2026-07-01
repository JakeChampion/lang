// Package arm64ssa renders the target-neutral abstract register program produced
// by the SSA register allocator (internal/codegen/x86_64ssa's Emit — the emit
// and model layers are target-independent; only the asm rendering is per-target)
// into AArch64 assembly. This is the arm64 sibling of the x86-64 real-asm path
// (x86_64ssa/gas.go), and the first step of Phase 4 (arm64 SSA emit) of the
// binary-size epic (#4109/#4112). arm64 is the default target, so the shipped
// binary's register-allocation win needs this path.
//
// Scope of this first slice: the straight-line integer arithmetic core — integer
// constants, register moves, and add/sub/mul/and/or/xor — for a single-block
// function, validated end-to-end by assembling with internal/native/arm64 and
// running under qemu-aarch64 against the abstract model. Control flow, calls,
// parameters, comparisons, div/rem/shift, memory, floats, and the runtime layer
// are follow-up slices, mirroring how the x86-64 path was built up.
package arm64ssa

import (
	"fmt"
	"strings"

	x86 "github.com/jakechampion/lang/internal/codegen/x86_64ssa"
	"github.com/jakechampion/lang/internal/ssa"
)

// armX / armW map an abstract register index to its 64-bit / 32-bit AArch64
// register name. The abstract file (allocatable + 4 scratch) is mapped onto
// x0..x15 — all caller-saved / temporary registers under the AArch64 PCS, so the
// leaf integer core needs no callee-saved save/restore bookkeeping.
var armX = []string{"x0", "x1", "x2", "x3", "x4", "x5", "x6", "x7", "x8", "x9", "x10", "x11", "x12", "x13", "x14", "x15"}
var armW = []string{"w0", "w1", "w2", "w3", "w4", "w5", "w6", "w7", "w8", "w9", "w10", "w11", "w12", "w13", "w14", "w15"}

func xreg(i int) string { return armX[i] }
func wreg(i int) string { return armW[i] }

// argRegCount is the number of integer argument registers under the AArch64 PCS
// (x0..x7); the parameter ABI supports up to this many params.
const argRegCount = 8

// EmitAsm lowers an integer SSA function to a complete, runnable AArch64 GAS
// program: a `_start` that loads entryArgs into the argument registers, calls
// the function, and exits with its return value in the low byte, plus the
// function itself. Allocation is over numAlloc registers.
func EmitAsm(f *ssa.Func, numAlloc int, entryArgs ...int64) (string, error) {
	p, err := x86.Emit(f, numAlloc)
	if err != nil {
		return "", fmt.Errorf("arm64ssa: %w", err)
	}
	if p.NumRegFile > len(armX) {
		return "", fmt.Errorf("arm64ssa: %q needs %d registers but only %d are wired", f.Name, p.NumRegFile, len(armX))
	}
	if len(p.ParamLocs) > argRegCount {
		return "", fmt.Errorf("arm64ssa: %q has %d params, arm64 SSA supports up to %d", f.Name, len(p.ParamLocs), argRegCount)
	}

	var b strings.Builder
	w := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	w(".text")
	w(".globl _start")
	w("_start:")
	// Load the entry arguments into the argument registers before the call.
	for i := range p.ParamLocs {
		var v int64
		if i < len(entryArgs) {
			v = entryArgs[i]
		}
		w("\tmov %s, #%d", xreg(i), v)
	}
	w("\tbl %s", fnLabel(f.Name))
	// exit_group(status): status is the function's return value in x0; the kernel
	// keeps the low byte. x8 = 94 (exit_group on AArch64 Linux).
	w("\tmov x8, #94")
	w("\tsvc #0")
	w("")
	if err := emitFunc(w, f.Name, p); err != nil {
		return "", err
	}
	w(".section .note.GNU-stack,\"\",@progbits")
	return b.String(), nil
}

// emitFunc writes one function: its label, a stack frame for spill slots, each
// block's straight-line body under a namespaced label, and the terminators.
func emitFunc(w func(string, ...any), name string, p *x86.Program) error {
	label := fnLabel(name)
	w("%s:", label)
	frame := align16(8 * p.NumSlots) // leaf: no saved regs / fp yet
	if frame > 0 {
		w("\tsub sp, sp, #%d", frame)
	}
	// Parameter ABI: move each incoming argument register (x0, x1, …) into that
	// param's allocated home. Must follow the frame setup (slot-homed params store
	// to [sp]).
	for _, l := range paramMoveLines(p.ParamLocs) {
		w("\t%s", l)
	}
	// Execution falls into block 0 (the entry) right after the prologue.
	for bi, blk := range p.Blocks {
		w(".L%s_b%d:", label, bi)
		for _, in := range blk.Insts {
			lines, err := asmInst(in)
			if err != nil {
				return err
			}
			for _, l := range lines {
				w("\t%s", l)
			}
		}
		switch blk.Term.Kind {
		case x86.TRet:
			if blk.Term.RetReg != 0 {
				w("\tmov x0, %s", xreg(blk.Term.RetReg))
			}
			if frame > 0 {
				w("\tadd sp, sp, #%d", frame)
			}
			w("\tret")
		case x86.TJmp:
			w("\tb .L%s_b%d", label, blk.Term.Target)
		case x86.TBrIf:
			w("\tcbnz %s, .L%s_b%d", xreg(blk.Term.CondReg), label, blk.Term.True)
			w("\tb .L%s_b%d", label, blk.Term.False)
		default:
			return fmt.Errorf("arm64ssa: unsupported terminator %d", blk.Term.Kind)
		}
	}
	return nil
}

// paramMoveLines emits the AArch64 parameter-ABI prologue: move each incoming
// argument register (x0, x1, …) into that param's allocated home. It is a
// parallel copy — an arg register may be another param's home register — so it
// resolves in two steps, mirroring the x86-64 path:
//
//   - Slot-homed params first (`str x{i}, [sp, #…]`). These only READ arg
//     registers and write memory, so doing them before any register is
//     overwritten is always safe.
//   - Register-homed params as a parallel register copy. The abstract register
//     file maps index i onto x{i}, so the incoming physical arg register for
//     param i is exactly abstract register i — the parallel move is over
//     abstract indices i → home.
func paramMoveLines(paramLocs []x86.Loc) []string {
	var out []string
	// Step A: slot-homed params (read arg regs, write memory).
	for i, loc := range paramLocs {
		if !loc.IsReg && loc.Slot >= 0 {
			out = append(out, fmt.Sprintf("str %s, [sp, #%d]", xreg(i), 8*loc.Slot))
		}
	}
	// Step B: register-homed params — parallel register copy.
	var moves [][2]int // {dst, src} abstract register indices
	for i, loc := range paramLocs {
		if loc.IsReg && loc.Reg != i {
			moves = append(moves, [2]int{loc.Reg, i})
		}
	}
	return append(out, resolveRegMoves(moves)...)
}

// resolveRegMoves renders a parallel register copy (each {dst, src} entry; dsts
// distinct, srcs distinct). It emits any move whose destination is not still
// needed as a source; when only cycles remain it breaks one with an eor-based
// swap (AArch64 has no single-instruction xchg) and redirects the moves that
// read the swapped register. Always terminates.
func resolveRegMoves(moves [][2]int) []string {
	var out []string
	isSrc := func(r int) bool {
		for _, m := range moves {
			if m[1] == r {
				return true
			}
		}
		return false
	}
	for len(moves) > 0 {
		idx := -1
		for j, m := range moves {
			if !isSrc(m[0]) {
				idx = j
				break
			}
		}
		if idx >= 0 {
			m := moves[idx]
			out = append(out, fmt.Sprintf("mov %s, %s", xreg(m[0]), xreg(m[1])))
			moves = append(moves[:idx], moves[idx+1:]...)
			continue
		}
		// Only cycles remain: swap two registers with the three-eor trick.
		m := moves[0]
		a, b := xreg(m[0]), xreg(m[1])
		out = append(out,
			fmt.Sprintf("eor %s, %s, %s", a, a, b),
			fmt.Sprintf("eor %s, %s, %s", b, a, b),
			fmt.Sprintf("eor %s, %s, %s", a, a, b),
		)
		moves = moves[1:]
		for k := range moves {
			if moves[k][1] == m[0] {
				moves[k][1] = m[1]
			}
		}
	}
	return out
}

// align16 rounds n up to a multiple of 16 (the AArch64 stack-alignment rule).
func align16(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + 15) &^ 15
}

// asmInst renders one abstract instruction to AArch64 GAS lines.
func asmInst(in x86.Inst) ([]string, error) {
	switch in.Op {
	case x86.MovImm:
		// The immediate is the value in the model's slot (already sign-extended for
		// i32), so a plain move materialises it; the assembler expands wide
		// immediates to movz/movk.
		return []string{fmt.Sprintf("mov %s, #%d", xreg(in.Dst), in.Imm)}, nil
	case x86.MovReg:
		if in.Dst == in.Src {
			return nil, nil
		}
		return []string{fmt.Sprintf("mov %s, %s", xreg(in.Dst), xreg(in.Src))}, nil
	case x86.LoadSlot:
		return []string{fmt.Sprintf("ldr %s, [sp, #%d]", xreg(in.Dst), 8*in.Imm)}, nil
	case x86.StoreSlot:
		return []string{fmt.Sprintf("str %s, [sp, #%d]", xreg(in.Src), 8*in.Imm)}, nil
	case x86.BinOp:
		mnem, ok := binMnemonic(in.K)
		if !ok {
			return nil, fmt.Errorf("arm64ssa: binary op %v not supported yet", in.K)
		}
		// 3-operand form with dst as both the accumulator and destination, matching
		// the abstract semantics reg[Dst] = reg[Dst] (K) reg[Src].
		out := []string{fmt.Sprintf("%s %s, %s, %s", mnem, xreg(in.Dst), xreg(in.Dst), xreg(in.Src))}
		out = append(out, maskFix(in.Dst, in.W)...)
		return out, nil
	case x86.SetCmp:
		cc, ok := condCode(in.K)
		if !ok {
			return nil, fmt.Errorf("arm64ssa: comparison %v not supported yet", in.K)
		}
		// 64-bit cmp on sign-extended i32 operands orders correctly for both
		// signed and unsigned conditions; cset materialises the 0/1 result (no i32
		// mask needed).
		return []string{
			fmt.Sprintf("cmp %s, %s", xreg(in.Dst), xreg(in.Src)),
			fmt.Sprintf("cset %s, %s", xreg(in.Dst), cc),
		}, nil
	default:
		return nil, fmt.Errorf("arm64ssa: opcode %d not supported yet", in.Op)
	}
}

// binMnemonic maps an SSA integer arithmetic/bitwise op to its AArch64 mnemonic.
func binMnemonic(k ssa.OpKind) (string, bool) {
	switch k {
	case ssa.OpAdd:
		return "add", true
	case ssa.OpSub:
		return "sub", true
	case ssa.OpMul:
		return "mul", true
	case ssa.OpAnd:
		return "and", true
	case ssa.OpOr:
		return "orr", true
	case ssa.OpXor:
		return "eor", true
	}
	return "", false
}

// condCode maps an SSA comparison op to its AArch64 condition mnemonic (signed:
// lt/le/gt/ge; unsigned: lo/ls/hi/hs) for cset / conditional branches.
func condCode(k ssa.OpKind) (string, bool) {
	switch k {
	case ssa.OpEq:
		return "eq", true
	case ssa.OpNe:
		return "ne", true
	case ssa.OpLt:
		return "lt", true
	case ssa.OpLe:
		return "le", true
	case ssa.OpGt:
		return "gt", true
	case ssa.OpGe:
		return "ge", true
	case ssa.OpLtU:
		return "lo", true
	case ssa.OpLeU:
		return "ls", true
	case ssa.OpGtU:
		return "hi", true
	case ssa.OpGeU:
		return "hs", true
	}
	return "", false
}

// maskFix mirrors the model's i32 sign-extension: for an i32-width result the low
// 32 bits are sign-extended back into the full register (sxtw), so a value whose
// high bits are later observed matches the model. 64-bit results need no fix.
func maskFix(dst int, wdt int8) []string {
	if wdt == 64 {
		return nil
	}
	return []string{fmt.Sprintf("sxtw %s, %s", xreg(dst), wreg(dst))}
}

// fnLabel sanitises an SSA function name into an assembly label.
func fnLabel(name string) string {
	var s strings.Builder
	s.WriteString("fn_")
	for _, r := range name {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			s.WriteRune(r)
		} else {
			s.WriteByte('_')
		}
	}
	return s.String()
}
