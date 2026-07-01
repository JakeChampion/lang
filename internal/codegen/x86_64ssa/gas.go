package x86_64ssa

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/ssa"
)

// sysvArgRegs is the System V AMD64 integer argument-register sequence: the
// first six integer/pointer args arrive in these registers, in order.
var sysvArgRegs = []string{"rdi", "rsi", "rdx", "rcx", "r8", "r9"}

// EmitAsm lowers an SSA function to a complete, runnable x86-64 GAS program
// (Intel syntax) with a `_start` that calls the function with no arguments and
// exits with its return value. See EmitAsmArgs for the parameterised form.
func EmitAsm(f *ssa.Func, numAlloc int) (string, error) {
	return EmitAsmArgs(f, numAlloc, nil)
}

// EmitAsmArgs lowers an SSA function to a complete, runnable x86-64 GAS program
// (Intel syntax) by first producing the abstract register program (Emit) and
// then rendering real instructions for the allocated registers + spill slots.
// The result is a self-contained static executable source: a `_start` that
// loads `entryArgs` into the System V argument registers, calls the function,
// and exits with its return value, plus the function itself.
//
// This is phase-2 slice 3b — the System V parameter ABI on the real-asm path.
// The function prologue moves each incoming argument register into that param's
// allocated home (register or spill slot), so a parameterised function runs
// natively. Scope: up to six integer parameters (stack args are a follow-up)
// over the full integer op set — arithmetic, bitwise, shifts (cl), div/rem
// (rdx:rax), comparisons, control flow — with i32-width results sign-extended
// to match the model. Validated by assembling + running (see gas_run_test.go).
func EmitAsmArgs(f *ssa.Func, numAlloc int, entryArgs []int64) (string, error) {
	return EmitAsmModule(map[string]*ssa.Func{f.Name: f}, f.Name, numAlloc, entryArgs)
}

// EmitAsmModule lowers a set of SSA functions to one runnable x86-64 GAS program
// (Intel syntax). Each function is emitted under a unique label; a `_start`
// loads `entryArgs` into the System V argument registers, calls the entry, and
// exits with its return value. Direct calls (`OpCall`) between functions are
// lowered to the System V call ABI — args in rdi/rsi/…, result in rax — with
// caller-saved registers conservatively preserved across each call (the
// allocator has no call-clobber awareness yet, so every caller-saved allocatable
// register that could hold a live-across-call value is saved; callee-saved
// registers and spill slots survive a call untouched).
func EmitAsmModule(funcs map[string]*ssa.Func, entry string, numAlloc int, entryArgs []int64) (string, error) {
	if _, ok := funcs[entry]; !ok {
		return "", fmt.Errorf("EmitAsmModule: unknown entry %q", entry)
	}
	names := make([]string, 0, len(funcs))
	for name := range funcs {
		names = append(names, name)
	}
	sort.Strings(names)

	progs := make(map[string]*Program, len(funcs))
	for _, name := range names {
		p, err := Emit(funcs[name], numAlloc)
		if err != nil {
			return "", fmt.Errorf("emit %q: %w", name, err)
		}
		if len(p.ParamLocs) > len(sysvArgRegs) {
			return "", fmt.Errorf("x86_64ssa: %q has %d params, real-asm supports up to %d", name, len(p.ParamLocs), len(sysvArgRegs))
		}
		if p.NumRegFile > len(gpRegs) {
			return "", fmt.Errorf("x86_64ssa: %q needs %d registers but only %d are available", name, p.NumRegFile, len(gpRegs))
		}
		progs[name] = p
	}

	ep := progs[entry]
	if len(entryArgs) != 0 && len(entryArgs) != len(ep.ParamLocs) {
		return "", fmt.Errorf("x86_64ssa: got %d entry args, entry %q has %d params", len(entryArgs), entry, len(ep.ParamLocs))
	}

	var b strings.Builder
	w := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}

	heap := usesHeap(progs)
	strLabels, strOrder := collectStrings(progs, names)
	// fn_idx for closures: a function's index in the module's (sorted) emission
	// order — the same value the model's function-index table carries.
	fnIndex := make(map[string]int, len(names))
	for i, n := range names {
		fnIndex[n] = i
	}

	w(".intel_syntax noprefix")
	w(".text")
	w(".globl _start")
	w("_start:")
	// Initialise the bump-allocator cursor to the base of the .bss heap.
	if heap {
		w("\tlea rax, [rip + %s]", heapSym)
		w("\tmov [rip + %s], rax", heapPtrSym)
	}
	// Load the entry arguments into the SysV argument registers before the call.
	for i := range ep.ParamLocs {
		var v int64
		if i < len(entryArgs) {
			v = entryArgs[i]
		}
		w("\tmov %s, %d", sysvArgRegs[i], v)
	}
	w("\tcall %s", fnLabel(entry))
	w("\tmov edi, eax")     // exit code = return value
	w("\tmov eax, %d", 231) // sysExitGroup
	w("\tsyscall")
	w("")
	for _, name := range names {
		if err := emitFuncBody(w, name, progs[name], numAlloc, strLabels, fnIndex); err != nil {
			return "", err
		}
	}
	if len(strOrder) > 0 {
		w("")
		w(".section .rodata")
		for _, s := range strOrder {
			w("%s:", strLabels[s])
			if len(s) > 0 {
				parts := make([]string, len(s))
				for i := 0; i < len(s); i++ {
					parts[i] = strconv.Itoa(int(s[i]))
				}
				w("\t.byte %s", strings.Join(parts, ", "))
			}
		}
	}
	if heap {
		w("")
		w(".section .bss")
		w(".align 8")
		w("%s:", heapPtrSym)
		w("\t.quad 0")
		w("%s:", heapSym)
		w("\t.space %d", heapBytes)
	}
	w(".section .note.GNU-stack,\"\",@progbits")
	return b.String(), nil
}

// emitFuncBody writes one function's label, prologue, parameter moves, block
// bodies, and epilogue. Block labels are namespaced by the function label so
// several functions coexist in one program.
func emitFuncBody(w func(string, ...any), name string, p *Program, numAlloc int, strLabels map[string]string, fnIndex map[string]int) error {
	label := fnLabel(name)
	// s3 — the last register in the file — is the free scratch the div/shift and
	// call sequences stage operands through. It is above the allocatable range
	// (and above rax/rcx/rdx), so it never aliases the fixed registers those
	// sequences pin, nor any caller-saved register saved across a call.
	scratch := p.NumRegFile - 1

	// Callee-saved registers this function may clobber (allocatable homes and the
	// scratch registers can land on rbx / r12–r15). Per the System V ABI the
	// function must preserve them for its caller, so they are saved into fresh
	// slots above the allocator's spill slots and restored at every return.
	saved := calleeSavedUsed(p.NumRegFile)
	savedSlot := func(i int) string { return slotMem(p.NumSlots + i) }
	restore := func() {
		for i := len(saved) - 1; i >= 0; i-- {
			w("\tmov %s, %s", reg(saved[i]), savedSlot(i))
		}
	}

	w("%s:", label)
	w("\tpush rbp")
	w("\tmov rbp, rsp")
	frame := align16(8 * (p.NumSlots + len(saved)))
	if frame > 0 {
		w("\tsub rsp, %d", frame)
	}
	for i, r := range saved {
		w("\tmov %s, %s", savedSlot(i), reg(r))
	}
	for _, line := range paramMoveLines(p.ParamLocs) {
		w("\t%s", line)
	}

	for bi, blk := range p.Blocks {
		w(".L_%s_b%d:", label, bi)
		for _, in := range blk.Insts {
			if in.Op == Call || in.Op == CallPair {
				lines, err := callLines(in, numAlloc, scratch, numAlloc)
				if err != nil {
					return err
				}
				for _, l := range lines {
					w("\t%s", l)
				}
				continue
			}
			if in.Op == ConstStr {
				lbl, ok := strLabels[in.Str]
				if !ok {
					return fmt.Errorf("x86_64ssa: ConstStr %q has no .rodata label", in.Str)
				}
				w("\tlea %s, [rip + %s]", reg(in.Dst), lbl)
				continue
			}
			if in.Op == MakeEnv || in.Op == MakeClosure {
				lines, err := closureLines(in, numAlloc, fnIndex)
				if err != nil {
					return err
				}
				for _, l := range lines {
					w("\t%s", l)
				}
				continue
			}
			line, err := asmInst(in, scratch)
			if err != nil {
				return err
			}
			w("\t%s", line)
		}
		switch blk.Term.Kind {
		case TRet:
			w("\tmov rax, %s", reg(blk.Term.RetReg))
			restore()
			w("\tmov rsp, rbp")
			w("\tpop rbp")
			w("\tret")
		case TRetPair:
			// System V pair return: tag in rax, payload in rdx. The two moves are
			// a parallel copy (a home may already be rax/rdx), so resolve them
			// before the callee-saved restore (which never touches rax/rdx).
			for _, l := range pairRetMoves(blk.Term.RetReg, blk.Term.RetReg2) {
				w("\t%s", l)
			}
			restore()
			w("\tmov rsp, rbp")
			w("\tpop rbp")
			w("\tret")
		case TJmp:
			w("\tjmp .L_%s_b%d", label, blk.Term.Target)
		case TBrIf:
			w("\ttest %s, %s", reg(blk.Term.CondReg), reg(blk.Term.CondReg))
			w("\tjnz .L_%s_b%d", label, blk.Term.True)
			w("\tjmp .L_%s_b%d", label, blk.Term.False)
		default:
			return fmt.Errorf("x86_64ssa: unsupported terminator %d in real asm", blk.Term.Kind)
		}
	}
	return nil
}

// fnLabel is the assembly label for an SSA function name (sanitised so
// non-identifier characters in generated names don't break the assembler).
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

// isCallerSaved reports whether gpRegs index r is a System V caller-saved
// register (clobberable across a call). rbx and r12–r15 are callee-saved.
func isCallerSaved(r int) bool {
	switch r {
	case 1, 10, 11, 12, 13: // rbx, r12, r13, r14, r15
		return false
	default:
		return true
	}
}

// calleeSavedUsed returns the callee-saved gpRegs indices within a register file
// of size numRegFile, in ascending order — the registers a function must
// preserve for its caller if it touches them (which it may, via allocatable
// homes or scratch registers landing on rbx / r12–r15).
func calleeSavedUsed(numRegFile int) []int {
	var out []int
	for _, r := range []int{1, 10, 11, 12, 13} { // rbx, r12–r15
		if r < numRegFile {
			out = append(out, r)
		}
	}
	return out
}

// callLines renders a direct call under the System V ABI. Because the allocator
// doesn't yet track which values are live across a call, every caller-saved
// allocatable register is conservatively saved (values in callee-saved registers
// or spill slots survive a call on their own). Arguments are passed via the
// stack — pushed from their homes then popped into the arg registers — so the
// home→arg-register shuffle can't clobber a not-yet-consumed source. The result
// (rax) is captured into the scratch register, which is never in the saved set,
// so the restores don't overwrite it.
func callLines(in Inst, numAlloc, scratch, s0 int) ([]string, error) {
	if len(in.ArgLocs) > len(sysvArgRegs) {
		return nil, fmt.Errorf("x86_64ssa: real-asm call supports up to %d args, got %d", len(sysvArgRegs), len(in.ArgLocs))
	}
	var saved []int
	for r := 0; r < numAlloc; r++ {
		if isCallerSaved(r) {
			saved = append(saved, r)
		}
	}
	var out []string
	// 16-byte stack alignment at the call: args are pushed then popped (net 0),
	// so only the pad + saved pushes remain. Pad to make their count even.
	pad := (len(saved) % 2) * 8
	if pad != 0 {
		out = append(out, "sub rsp, 8")
	}
	for _, r := range saved {
		out = append(out, fmt.Sprintf("push %s", reg(r)))
	}
	// Push argument values (arg0 first). Register homes still hold their values
	// after the saves (push doesn't clear the source); slot homes are
	// rbp-relative, unaffected by the pushes.
	for _, l := range in.ArgLocs {
		if l.IsReg {
			out = append(out, fmt.Sprintf("mov %s, %s", reg(scratch), reg(l.Reg)))
		} else {
			out = append(out, fmt.Sprintf("mov %s, %s", reg(scratch), slotMem(l.Slot)))
		}
		out = append(out, fmt.Sprintf("push %s", reg(scratch)))
	}
	// Pop into arg registers in reverse (last pushed = last arg).
	for i := len(in.ArgLocs) - 1; i >= 0; i-- {
		out = append(out, fmt.Sprintf("pop %s", sysvArgRegs[i]))
	}
	out = append(out, fmt.Sprintf("call %s", fnLabel(in.Callee)))
	out = append(out, fmt.Sprintf("mov %s, rax", reg(scratch))) // capture result (tag)
	if in.Op == CallPair {
		// The second return (payload) is in rdx. Capture it into s0 — free during
		// the call inst and not in the caller-saved set — so the restores below
		// don't overwrite it.
		out = append(out, fmt.Sprintf("mov %s, rdx", reg(s0)))
	}
	for i := len(saved) - 1; i >= 0; i-- {
		out = append(out, fmt.Sprintf("pop %s", reg(saved[i])))
	}
	if pad != 0 {
		out = append(out, "add rsp, 8")
	}
	out = append(out, fmt.Sprintf("mov %s, %s", reg(in.Dst), reg(scratch))) // place result
	if fix := maskFix(in.Dst, in.W); fix != "" {
		out = append(out, strings.TrimPrefix(fix, "\n\t"))
	}
	if in.Op == CallPair {
		out = append(out, fmt.Sprintf("mov %s, %s", reg(in.Dst2), reg(s0))) // place payload
	}
	return out, nil
}

// gpRegs is the allocatable+scratch register pool (rsp/rbp reserved for the
// frame). reg8 is the parallel 8-bit subregister used by setcc.
var gpRegs = []string{"rax", "rbx", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15"}
var reg8 = []string{"al", "bl", "cl", "dl", "sil", "dil", "r8b", "r9b", "r10b", "r11b", "r12b", "r13b", "r14b", "r15b"}
var reg32 = []string{"eax", "ebx", "ecx", "edx", "esi", "edi", "r8d", "r9d", "r10d", "r11d", "r12d", "r13d", "r14d", "r15d"}
var reg16 = []string{"ax", "bx", "cx", "dx", "si", "di", "r8w", "r9w", "r10w", "r11w", "r12w", "r13w", "r14w", "r15w"}

// gpIndex returns the gpRegs index of a physical register name.
func gpIndex(name string) int {
	for i, r := range gpRegs {
		if r == name {
			return i
		}
	}
	return -1
}

// paramMoveLines emits the System V parameter-ABI prologue: move each incoming
// argument register (rdi, rsi, …) into that param's allocated home. It is a
// parallel copy — an arg register may be another param's home register — so it
// resolves in two steps:
//
//   - Slot-homed params first (`mov [slot], argreg`). These only READ arg
//     registers and write memory, so doing them before any register is
//     overwritten is always safe.
//   - Register-homed params as a parallel register copy: emit any move whose
//     destination is not still needed as a source; when only cycles remain,
//     break one with `xchg` and redirect the moves that read the swapped
//     register. (Sources and destinations are each distinct, so this is the
//     standard parallel-move resolution and always terminates.)
func paramMoveLines(paramLocs []Loc) []string {
	var out []string
	// Step A: slot-homed params (read arg regs, write memory).
	for i, loc := range paramLocs {
		if !loc.IsReg && loc.Slot >= 0 {
			out = append(out, fmt.Sprintf("mov %s, %s", slotMem(loc.Slot), sysvArgRegs[i]))
		}
	}
	// Step B: register-homed params — parallel register copy.
	var moves [][2]int // {dst, src} gpRegs indices
	for i, loc := range paramLocs {
		if loc.IsReg {
			if src := gpIndex(sysvArgRegs[i]); src != loc.Reg {
				moves = append(moves, [2]int{loc.Reg, src})
			}
		}
	}
	return append(out, resolveRegMoves(moves)...)
}

// resolveRegMoves renders a parallel register copy (each {dst, src} entry; dsts
// distinct, srcs distinct). It emits any move whose destination is not still
// needed as a source; when only cycles remain it breaks one with `xchg` and
// redirects the moves that read the swapped register. Always terminates.
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
			out = append(out, fmt.Sprintf("mov %s, %s", reg(m[0]), reg(m[1])))
			moves = append(moves[:idx], moves[idx+1:]...)
			continue
		}
		m := moves[0]
		out = append(out, fmt.Sprintf("xchg %s, %s", reg(m[0]), reg(m[1])))
		moves = moves[1:]
		for k := range moves {
			if moves[k][1] == m[0] {
				moves[k][1] = m[1]
			}
		}
	}
	return out
}

// pairRetMoves moves the pair-return (tag, payload) values into the System V
// pair-return registers rax and rdx, resolving the parallel copy (either home
// may already be rax/rdx).
func pairRetMoves(tagReg, payReg int) []string {
	var moves [][2]int
	if tagReg != raxReg {
		moves = append(moves, [2]int{raxReg, tagReg})
	}
	if payReg != rdxReg {
		moves = append(moves, [2]int{rdxReg, payReg})
	}
	return resolveRegMoves(moves)
}

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

// maskFix mirrors the model's maskW: for an i32-width result (W != 64) it
// sign-extends the low 32 bits back into the full register, so a value whose
// high 32 bits are later observed (unsigned shift/div, unsigned compare) matches
// ssa.Eval. Returns the empty string for 64-bit results (no fix needed).
func maskFix(dst int, w int8) string {
	if w == 64 {
		return ""
	}
	return fmt.Sprintf("\n\tmovsxd %s, %s", reg(dst), reg32n(dst))
}

func asmInst(in Inst, scratch int) (string, error) {
	switch in.Op {
	case MovImm:
		return fmt.Sprintf("mov %s, %d", reg(in.Dst), in.Imm) + maskFix(in.Dst, in.W), nil
	case MovReg:
		return fmt.Sprintf("mov %s, %s", reg(in.Dst), reg(in.Src)), nil
	case UnNeg:
		return fmt.Sprintf("neg %s", reg(in.Dst)) + maskFix(in.Dst, in.W), nil
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
		switch in.K {
		case ssa.OpShl, ssa.OpShr, ssa.OpShrU:
			return shiftSeq(in) + maskFix(in.Dst, in.W), nil
		case ssa.OpDiv, ssa.OpDivU, ssa.OpRem, ssa.OpRemU:
			return divSeq(in, scratch) + maskFix(in.Dst, in.W), nil
		}
		op, ok := binMnemonic(in.K)
		if !ok {
			return "", fmt.Errorf("x86_64ssa: binary op %v unsupported in the real-asm slice", in.K)
		}
		return fmt.Sprintf("%s %s, %s", op, reg(in.Dst), reg(in.Src)) + maskFix(in.Dst, in.W), nil
	case SetCmp:
		cc, ok := setccMnemonic(in.K)
		if !ok {
			return "", fmt.Errorf("x86_64ssa: comparison %v unsupported", in.K)
		}
		// dst = (dst CMP src): compare, set the low byte from flags, zero-extend.
		return fmt.Sprintf("cmp %s, %s\n\t%s %s\n\tmovzx %s, %s",
			reg(in.Dst), reg(in.Src), cc, reg8n(in.Dst), reg(in.Dst), reg8n(in.Dst)), nil
	case MemAlloc:
		// Bump allocator: result = align8(cursor); cursor = result + size. The
		// heap cursor lives in .bss (see the heap section emitted per module).
		return strings.Join([]string{
			fmt.Sprintf("mov %s, [rip + %s]", reg(in.Dst), heapPtrSym),
			fmt.Sprintf("add %s, 7", reg(in.Dst)),
			fmt.Sprintf("and %s, -8", reg(in.Dst)),
			fmt.Sprintf("mov %s, %s", reg(scratch), reg(in.Dst)),
			fmt.Sprintf("add %s, %s", reg(scratch), reg(in.Src)),
			fmt.Sprintf("mov [rip + %s], %s", heapPtrSym, reg(scratch)),
		}, "\n\t"), nil
	case MemLoad:
		mem := memRef(reg(in.Src), in.Imm)
		if in.Bytes == 8 {
			return fmt.Sprintf("mov %s, %s", reg(in.Dst), mem) + maskFix(in.Dst, in.W), nil
		}
		size := "byte ptr"
		if in.Bytes == 2 {
			size = "word ptr"
		}
		if in.Signed {
			if in.W == 64 {
				return fmt.Sprintf("movsx %s, %s %s", reg(in.Dst), size, mem), nil
			}
			return fmt.Sprintf("movsx %s, %s %s", reg32n(in.Dst), size, mem) + maskFix(in.Dst, in.W), nil
		}
		return fmt.Sprintf("movzx %s, %s %s", reg32n(in.Dst), size, mem) + maskFix(in.Dst, in.W), nil
	case MemStore:
		mem := memRef(reg(in.Src), in.Imm)
		switch in.Bytes {
		case 1:
			return fmt.Sprintf("mov byte ptr %s, %s", mem, reg8n(in.Src2)), nil
		case 2:
			return fmt.Sprintf("mov word ptr %s, %s", mem, reg16n(in.Src2)), nil
		default:
			return fmt.Sprintf("mov %s, %s", mem, reg(in.Src2)), nil
		}
	case FConst:
		// Floats live in GP registers as their f64 bit pattern (like ssa.Eval).
		// Materialise the compile-time bits directly (rounded to f32 if W==32).
		bits := math.Float64bits(in.F64)
		if in.W == 32 {
			bits = math.Float64bits(float64(float32(in.F64)))
		}
		return fmt.Sprintf("movabs %s, %d", reg(in.Dst), int64(bits)), nil
	case FBin:
		return fBinSeq(in), nil
	case FCmp:
		cc, ok := fcmpSetcc(in.K)
		if !ok {
			return "", fmt.Errorf("x86_64ssa: float compare %v unsupported", in.K)
		}
		// Shuttle both operands into xmm, ordered-compare, materialise 0/1.
		return strings.Join([]string{
			fmt.Sprintf("movq xmm0, %s", reg(in.Dst)),
			fmt.Sprintf("movq xmm1, %s", reg(in.Src)),
			"ucomisd xmm0, xmm1",
			fmt.Sprintf("%s %s", cc, reg8n(in.Dst)),
			fmt.Sprintf("movzx %s, %s", reg(in.Dst), reg8n(in.Dst)),
		}, "\n\t"), nil
	case FConv:
		return fConvSeq(in, scratch)
	default:
		return "", fmt.Errorf("x86_64ssa: unknown opcode %d", in.Op)
	}
}

// f32round rounds an f64-in-xmm0 to f32 precision (round-trip through f32),
// mirroring the model's fbits when W==32.
const f32round = "cvtsd2ss xmm0, xmm0\n\tcvtss2sd xmm0, xmm0"

// fBinSeq renders a scalar float arithmetic op: shuttle both operands into xmm,
// compute in f64, round to f32 if W==32, shuttle the result back.
func fBinSeq(in Inst) string {
	var mnem string
	switch in.K {
	case ssa.OpFAdd:
		mnem = "addsd"
	case ssa.OpFSub:
		mnem = "subsd"
	case ssa.OpFMul:
		mnem = "mulsd"
	case ssa.OpFDiv:
		mnem = "divsd"
	}
	lines := []string{
		fmt.Sprintf("movq xmm0, %s", reg(in.Dst)),
		fmt.Sprintf("movq xmm1, %s", reg(in.Src)),
		fmt.Sprintf("%s xmm0, xmm1", mnem),
	}
	if in.W == 32 {
		lines = append(lines, f32round)
	}
	lines = append(lines, fmt.Sprintf("movq %s, xmm0", reg(in.Dst)))
	return strings.Join(lines, "\n\t")
}

// fcmpSetcc maps a float comparison to its ordered setcc (ucomisd flags). NaN
// operands are out of scope: for finite values these match ssa.Eval's Go
// comparisons.
func fcmpSetcc(k ssa.OpKind) (string, bool) {
	switch k {
	case ssa.OpFEq:
		return "sete", true
	case ssa.OpFNe:
		return "setne", true
	case ssa.OpFLt:
		return "setb", true
	case ssa.OpFLe:
		return "setbe", true
	case ssa.OpFGt:
		return "seta", true
	case ssa.OpFGe:
		return "setae", true
	default:
		return "", false
	}
}

// fConvSeq renders a float conversion / unary op. Integer results are
// width-masked (maskFix); float results carry their f64 bit pattern.
func fConvSeq(in Inst, scratch int) (string, error) {
	d := reg(in.Dst)
	round := ""
	if in.W == 32 {
		round = "\n\t" + f32round
	}
	switch in.K {
	case ssa.OpFNeg:
		// Flip the f64 sign bit (bit 63). Negating an f32-precision value keeps
		// f32 precision, so no rounding is needed.
		return fmt.Sprintf("movabs %s, %d\n\txor %s, %s", reg(scratch), int64(-0x8000000000000000), d, reg(scratch)), nil
	case ssa.OpFPromote:
		// f32 -> f64: the value already lives as f64 bits; identity.
		return fmt.Sprintf("movq xmm0, %s\n\tmovq %s, xmm0", d, d), nil
	case ssa.OpFDemote:
		return fmt.Sprintf("movq xmm0, %s\n\t%s\n\tmovq %s, xmm0", d, f32round, d), nil
	case ssa.OpIToFS, ssa.OpIToFU:
		// int -> float. cvtsi2sd is signed; unsigned values >= 2^63 are out of
		// scope (rare; a follow-up if needed).
		return fmt.Sprintf("cvtsi2sd xmm0, %s%s\n\tmovq %s, xmm0", d, round, d), nil
	case ssa.OpFToIS, ssa.OpFToIU:
		// float -> int, truncating toward zero (Go semantics).
		return fmt.Sprintf("movq xmm0, %s\n\tcvttsd2si %s, xmm0", d, d) + maskFix(in.Dst, in.W), nil
	default:
		return "", fmt.Errorf("x86_64ssa: float conversion %v unsupported", in.K)
	}
}

// heapPtrSym / heapSym / heapBytes back the SSA real-asm bump allocator. The
// heap is a fixed .bss buffer; a lazy mmap/brk allocator (like the stack-machine
// backend's) is a follow-up if real programs outgrow it.
const (
	heapPtrSym = "__ssa_heap_ptr"
	heapSym    = "__ssa_heap"
	heapBytes  = 1 << 16 // 64 KiB
)

// memRef renders a [base + disp] memory operand.
func memRef(regName string, disp int64) string {
	if disp == 0 {
		return fmt.Sprintf("[%s]", regName)
	}
	if disp > 0 {
		return fmt.Sprintf("[%s + %d]", regName, disp)
	}
	return fmt.Sprintf("[%s - %d]", regName, -disp)
}

// closureLines renders OpMakeEnv / OpMakeClosure on the .bss bump heap.
// MakeEnv allocates an env block of the N captures (8-byte slots) and returns
// the env pointer. MakeClosure additionally allocates a {fn_idx, env_ptr} cell
// (fn_idx = the target's module index) and returns the cell pointer. The env
// pointer is held in s0 (free during this instruction) across the second
// allocation; s3 stages the bump cursor and capture values.
func closureLines(in Inst, numAlloc int, fnIndex map[string]int) ([]string, error) {
	scratch := numAlloc + 3 // s3
	envReg := numAlloc      // s0 — unused by the MakeEnv/MakeClosure inst itself
	var out []string
	alloc := func(dst int, bytes int64) {
		out = append(out,
			fmt.Sprintf("mov %s, [rip + %s]", reg(dst), heapPtrSym),
			fmt.Sprintf("add %s, 7", reg(dst)),
			fmt.Sprintf("and %s, -8", reg(dst)),
			fmt.Sprintf("mov %s, %s", reg(scratch), reg(dst)),
			fmt.Sprintf("add %s, %d", reg(scratch), bytes),
			fmt.Sprintf("mov [rip + %s], %s", heapPtrSym, reg(scratch)),
		)
	}
	storeCaps := func(base int) {
		for i, l := range in.ArgLocs {
			if l.IsReg {
				out = append(out, fmt.Sprintf("mov %s, %s", reg(scratch), reg(l.Reg)))
			} else {
				out = append(out, fmt.Sprintf("mov %s, %s", reg(scratch), slotMem(l.Slot)))
			}
			out = append(out, fmt.Sprintf("mov %s, %s", memRef(reg(base), int64(i*8)), reg(scratch)))
		}
	}
	n := int64(len(in.ArgLocs))
	if in.Op == MakeEnv {
		alloc(in.Dst, n*8)
		storeCaps(in.Dst)
		return out, nil
	}
	idx, ok := fnIndex[in.Callee]
	if !ok {
		return nil, fmt.Errorf("x86_64ssa: MakeClosure target %q not in module", in.Callee)
	}
	alloc(envReg, n*8) // env block -> s0
	storeCaps(envReg)
	alloc(in.Dst, 16) // {fn_idx, env_ptr} cell -> Dst
	out = append(out,
		fmt.Sprintf("mov %s, %d", reg(scratch), idx),
		fmt.Sprintf("mov %s, %s", memRef(reg(in.Dst), 0), reg(scratch)),
		fmt.Sprintf("mov %s, %s", memRef(reg(in.Dst), 8), reg(envReg)),
	)
	return out, nil
}

// collectStrings assigns a .rodata label to each unique OpConstString literal
// across the module (in a deterministic order: functions by sorted name, then
// instruction order). Returns the literal→label map and the labels' emission
// order.
func collectStrings(progs map[string]*Program, names []string) (map[string]string, []string) {
	labels := map[string]string{}
	var order []string
	for _, name := range names {
		for _, blk := range progs[name].Blocks {
			for _, in := range blk.Insts {
				if in.Op == ConstStr {
					if _, ok := labels[in.Str]; !ok {
						labels[in.Str] = fmt.Sprintf("str_%d", len(order))
						order = append(order, in.Str)
					}
				}
			}
		}
	}
	return labels, order
}

// usesHeap reports whether any emitted program contains a heap op (so the heap
// section + cursor init are only emitted when needed).
func usesHeap(progs map[string]*Program) bool {
	for _, p := range progs {
		for _, blk := range p.Blocks {
			for _, in := range blk.Insts {
				switch in.Op {
				case MemAlloc, MemLoad, MemStore, MakeEnv, MakeClosure:
					return true
				}
			}
		}
	}
	return false
}

// rcxReg/raxReg/rdxReg are the fixed registers the shift/div sequences pin.
const (
	rcxReg = 2 // gpRegs index of rcx
	raxReg = 0 // gpRegs index of rax
	rdxReg = 3 // gpRegs index of rdx
)

// shiftSeq renders a variable shift (count in cl). dst holds the value, src the
// count. rcx is preserved with push/pop so a live value there survives; the
// count is copied into rcx and the shift reads cl. dst is a scratch reg (never
// rcx), so `<op> dst, cl` is safe.
func shiftSeq(in Inst) string {
	var mnem string
	switch in.K {
	case ssa.OpShl:
		mnem = "shl"
	case ssa.OpShr:
		mnem = "sar" // arithmetic (signed) right shift
	case ssa.OpShrU:
		mnem = "shr" // logical (unsigned) right shift
	}
	return strings.Join([]string{
		"push rcx",
		fmt.Sprintf("mov %s, %s", reg(rcxReg), reg(in.Src)),
		fmt.Sprintf("%s %s, cl", mnem, reg(in.Dst)),
		"pop rcx",
	}, "\n\t")
}

// divSeq renders a division. dst holds the dividend, src the divisor; the result
// (quotient for div, remainder for rem) lands back in dst. idiv/div pin rdx:rax,
// so rax and rdx are preserved with push/pop, the divisor is staged into the
// free scratch register (never rax/rdx), the dividend goes into rax, and the
// dividend is extended into rdx (cqo signed / xor rdx zero unsigned).
//
// dst may itself be rdx (when numAlloc==1, s2==rdx), so the result is captured
// into the scratch register BEFORE the pops restore rax/rdx, then written into
// dst afterwards — otherwise `pop rdx` would clobber a result placed in rdx.
func divSeq(in Inst, scratch int) string {
	signed := in.K == ssa.OpDiv || in.K == ssa.OpRem
	rem := in.K == ssa.OpRem || in.K == ssa.OpRemU
	lines := []string{
		"push rax",
		"push rdx",
		fmt.Sprintf("mov %s, %s", reg(scratch), reg(in.Src)), // stash divisor
		fmt.Sprintf("mov %s, %s", reg(raxReg), reg(in.Dst)),  // dividend -> rax
	}
	if signed {
		lines = append(lines, "cqo", fmt.Sprintf("idiv %s", reg(scratch)))
	} else {
		lines = append(lines, "xor rdx, rdx", fmt.Sprintf("div %s", reg(scratch)))
	}
	resultReg := raxReg // quotient
	if rem {
		resultReg = rdxReg // remainder
	}
	lines = append(lines,
		fmt.Sprintf("mov %s, %s", reg(scratch), reg(resultReg)), // capture result before pops
		"pop rdx",
		"pop rax",
		fmt.Sprintf("mov %s, %s", reg(in.Dst), reg(scratch)), // place result into dst
	)
	return strings.Join(lines, "\n\t")
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
