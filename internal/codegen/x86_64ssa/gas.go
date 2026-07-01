package x86_64ssa

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ssa"
)

// EmitAsm lowers an SSA function to a complete, runnable x86-64 GAS program
// (Intel syntax) by first producing the abstract register program (Emit) and
// then rendering real instructions for the allocated registers + spill slots.
// The result is a self-contained static executable source: a `_start` that
// calls the function and exits with its return value, plus the function itself.
//
// This is phase-2 slice 3a — the first path that produces real machine code,
// validated by assembling + running (see gas_run_test.go). Scope: no-parameter
// integer functions over the slice-2 op set MINUS shifts/div (which need fixed
// registers — cl for shifts, rax/rdx for idiv — handled in a follow-up). The
// System V parameter ABI is also a follow-up; a function with parameters is
// rejected here so nothing is silently miscompiled.
func EmitAsm(f *ssa.Func, numAlloc int) (string, error) {
	p, err := Emit(f, numAlloc)
	if err != nil {
		return "", err
	}
	if len(p.ParamLocs) > 0 {
		return "", fmt.Errorf("x86_64ssa: real-asm path does not yet support parameters")
	}
	if p.NumRegFile > len(gpRegs) {
		return "", fmt.Errorf("x86_64ssa: need %d registers but only %d are available", p.NumRegFile, len(gpRegs))
	}

	const fn = "fern_fn"
	var b strings.Builder
	w := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}

	w(".intel_syntax noprefix")
	w(".text")
	w(".globl _start")
	w("_start:")
	w("\tcall %s", fn)
	w("\tmov edi, eax")     // exit code = return value
	w("\tmov eax, %d", 231) // sysExitGroup
	w("\tsyscall")
	w("")
	w("%s:", fn)
	w("\tpush rbp")
	w("\tmov rbp, rsp")
	frame := align16(8 * p.NumSlots)
	if frame > 0 {
		w("\tsub rsp, %d", frame)
	}

	for bi, blk := range p.Blocks {
		w(".Lb%d:", bi)
		for _, in := range blk.Insts {
			line, err := asmInst(in)
			if err != nil {
				return "", err
			}
			w("\t%s", line)
		}
		switch blk.Term.Kind {
		case TRet:
			w("\tmov rax, %s", reg(blk.Term.RetReg))
			w("\tmov rsp, rbp")
			w("\tpop rbp")
			w("\tret")
		case TJmp:
			w("\tjmp .Lb%d", blk.Term.Target)
		case TBrIf:
			w("\ttest %s, %s", reg(blk.Term.CondReg), reg(blk.Term.CondReg))
			w("\tjnz .Lb%d", blk.Term.True)
			w("\tjmp .Lb%d", blk.Term.False)
		default:
			return "", fmt.Errorf("x86_64ssa: unknown terminator %d", blk.Term.Kind)
		}
	}
	w(".section .note.GNU-stack,\"\",@progbits")
	return b.String(), nil
}

// gpRegs is the allocatable+scratch register pool (rsp/rbp reserved for the
// frame). reg8 is the parallel 8-bit subregister used by setcc.
var gpRegs = []string{"rax", "rbx", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15"}
var reg8 = []string{"al", "bl", "cl", "dl", "sil", "dil", "r8b", "r9b", "r10b", "r11b", "r12b", "r13b", "r14b", "r15b"}
var reg32 = []string{"eax", "ebx", "ecx", "edx", "esi", "edi", "r8d", "r9d", "r10d", "r11d", "r12d", "r13d", "r14d", "r15d"}
var reg16 = []string{"ax", "bx", "cx", "dx", "si", "di", "r8w", "r9w", "r10w", "r11w", "r12w", "r13w", "r14w", "r15w"}

func reg(i int) string    { return gpRegs[i] }
func reg8n(i int) string  { return reg8[i] }
func reg32n(i int) string { return reg32[i] }
func reg16n(i int) string { return reg16[i] }

// slotMem is the memory operand for spill slot n: [rbp - 8*(n+1)].
func slotMem(n int) string { return fmt.Sprintf("[rbp - %d]", 8*(n+1)) }

func align16(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + 15) &^ 15
}

func asmInst(in Inst) (string, error) {
	switch in.Op {
	case MovImm:
		return fmt.Sprintf("mov %s, %d", reg(in.Dst), in.Imm), nil
	case MovReg:
		return fmt.Sprintf("mov %s, %s", reg(in.Dst), reg(in.Src)), nil
	case UnNeg:
		return fmt.Sprintf("neg %s", reg(in.Dst)), nil
	case UnOp:
		d := in.Dst
		switch in.K {
		case ssa.OpNot:
			return fmt.Sprintf("cmp %s, 0\n\tsete %s\n\tmovzx %s, %s", reg(d), reg8n(d), reg(d), reg8n(d)), nil
		case ssa.OpTrunc, ssa.OpExtendS:
			return fmt.Sprintf("movsxd %s, %s", reg(d), reg32n(d)), nil // sign-extend low 32
		case ssa.OpExtendU:
			return fmt.Sprintf("mov %s, %s", reg32n(d), reg32n(d)), nil // 32-bit mov zero-extends
		case ssa.OpExtend8S:
			return fmt.Sprintf("movsx %s, %s", reg(d), reg8n(d)), nil
		case ssa.OpExtend16S:
			return fmt.Sprintf("movsx %s, %s", reg(d), reg16n(d)), nil
		default:
			return "", fmt.Errorf("x86_64ssa: unsupported unary op %v", in.K)
		}
	case LoadSlot:
		return fmt.Sprintf("mov %s, %s", reg(in.Dst), slotMem(int(in.Imm))), nil
	case StoreSlot:
		return fmt.Sprintf("mov %s, %s", slotMem(int(in.Imm)), reg(in.Src)), nil
	case BinOp:
		op, ok := binMnemonic(in.K)
		if !ok {
			return "", fmt.Errorf("x86_64ssa: op %v not supported in the real-asm slice (shifts/div need fixed registers)", in.K)
		}
		return fmt.Sprintf("%s %s, %s", op, reg(in.Dst), reg(in.Src)), nil
	case SetCmp:
		cc, ok := setccMnemonic(in.K)
		if !ok {
			return "", fmt.Errorf("x86_64ssa: comparison %v unsupported", in.K)
		}
		// dst = (dst CMP src): compare, set the low byte from flags, zero-extend.
		return fmt.Sprintf("cmp %s, %s\n\t%s %s\n\tmovzx %s, %s",
			reg(in.Dst), reg(in.Src), cc, reg8n(in.Dst), reg(in.Dst), reg8n(in.Dst)), nil
	default:
		return "", fmt.Errorf("x86_64ssa: unknown opcode %d", in.Op)
	}
}

func binMnemonic(k ssa.OpKind) (string, bool) {
	switch k {
	case ssa.OpAdd:
		return "add", true
	case ssa.OpSub:
		return "sub", true
	case ssa.OpMul:
		return "imul", true
	case ssa.OpAnd:
		return "and", true
	case ssa.OpOr:
		return "or", true
	case ssa.OpXor:
		return "xor", true
	default:
		return "", false // shifts (cl) and div (rax/rdx) are a follow-up
	}
}

func setccMnemonic(k ssa.OpKind) (string, bool) {
	switch k {
	case ssa.OpEq:
		return "sete", true
	case ssa.OpNe:
		return "setne", true
	case ssa.OpLt:
		return "setl", true
	case ssa.OpLe:
		return "setle", true
	case ssa.OpGt:
		return "setg", true
	case ssa.OpGe:
		return "setge", true
	case ssa.OpLtU:
		return "setb", true
	case ssa.OpLeU:
		return "setbe", true
	case ssa.OpGtU:
		return "seta", true
	case ssa.OpGeU:
		return "setae", true
	default:
		return "", false
	}
}
