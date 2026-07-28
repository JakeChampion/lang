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

	// Runtime helpers to append to .text (module-referenced + transitive deps).
	// Some of them allocate (e.g. __str_concat bumps the heap cursor), so the
	// heap section must exist whenever one is present, even if no direct heap op
	// (MemAlloc / MakeClosure / …) does.
	helpers := referencedRuntimeHelpers(progs)
	heap := usesHeap(progs)
	for _, h := range helpers {
		if heapUsingHelpers[h] {
			heap = true
			break
		}
	}
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
	for _, h := range helpers {
		runtimeHelperEmitters[h](w)
	}
	if len(strOrder) > 0 {
		w("")
		w(".section .rodata")
		for _, s := range strOrder {
			// Length-prefixed single-word string layout (mirrors the native
			// backends): an 8-byte header — an immortal rc sentinel (0x80000000,
			// top bit set) at [data-8] and the 4-byte byte-length at [data-4] —
			// sits immediately before the data, so the string pointer is the data
			// pointer. __str_len reads [ptr-4]; the sentinel makes __fern_str_dec
			// / rc helpers short-circuit on literals (they never free .rodata).
			// Consecutive literals stay contiguous, so each label's own header is
			// exactly the 8 bytes before it.
			w("\t.4byte 0x80000000")
			w("\t.4byte %d", len(s))
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
	if usesCallIndirect(progs) {
		// Function-address dispatch table: one .quad per function in module
		// (sorted) order, so table[fn_idx] is the callee's absolute address. A
		// closure cell carries fn_idx; OpCallIndirect indexes this table.
		w("")
		w(".section .rodata")
		w(".align 8")
		w("%s:", fnTableSym)
		for _, name := range names {
			w("\t.quad %s", fnLabel(name))
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
		for ii, in := range blk.Insts {
			if in.Op == Select {
				for _, l := range selectLines(in, fmt.Sprintf("%s_b%d_i%d", label, bi, ii)) {
					w("\t%s", l)
				}
				continue
			}
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
			if in.Op == CallIndirect {
				lines, err := callIndirectLines(in, numAlloc, scratch)
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

// callSavedSet returns the caller-saved allocatable registers to preserve across
// a call. When the emitter computed a live-across set (SaveRegsSet), only those
// registers are saved — values in callee-saved registers or spill slots, and
// caller-saved registers not live across the call, survive on their own. Absent
// that (SaveRegsSet false), it falls back to conservatively saving every
// caller-saved allocatable register.
func callSavedSet(in Inst, numAlloc int) []int {
	if in.SaveRegsSet {
		return in.SaveRegs // already filtered to caller-saved + sorted by the emitter
	}
	var saved []int
	for r := 0; r < numAlloc; r++ {
		if isCallerSaved(r) {
			saved = append(saved, r)
		}
	}
	return saved
}

// callLines renders a direct call under the System V ABI. Only the caller-saved
// registers holding values live across the call are preserved (callSavedSet).
// Arguments are passed via the stack — pushed from their homes then popped into
// the arg registers — so the home→arg-register shuffle can't clobber a
// not-yet-consumed source. The result (rax) is captured into the scratch
// register, which is never in the saved set, so the restores don't overwrite it.
func callLines(in Inst, numAlloc, scratch, s0 int) ([]string, error) {
	if len(in.ArgLocs) > len(sysvArgRegs) {
		return nil, fmt.Errorf("x86_64ssa: real-asm call supports up to %d args, got %d", len(sysvArgRegs), len(in.ArgLocs))
	}
	saved := callSavedSet(in, numAlloc)
	var out []string
	// 16-byte stack alignment at the call: the args no longer touch the stack,
	// so only the pad + saved pushes shift rsp. Pad to make their count even.
	pad := (len(saved) % 2) * 8
	if pad != 0 {
		out = append(out, "sub rsp, 8")
	}
	for _, r := range saved {
		out = append(out, fmt.Sprintf("push %s", reg(r)))
	}
	out = append(out, argMoveLines(in.ArgLocs)...)
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

// callIndirectLines renders a closure dispatch on the real-asm path. in.IdxLoc
// is a pointer to a {fn_idx, env_ptr} cell: fn_idx (at +0) indexes the module
// function-address table (fnTableSym); env_ptr (at +8) is appended as the
// callee's LAST argument (docs/SSA-CLOSURE-DISPATCH.md). Caller-saved registers
// are conservatively preserved as in callLines. The resolved target address is
// stashed on the stack across the argument-register shuffle (so no scratch
// register needs to dodge the arg registers), then popped into rax — never an
// argument register, and its own live value is already in the caller-saved set —
// and called register-indirect.
func callIndirectLines(in Inst, numAlloc, scratch int) ([]string, error) {
	if len(in.ArgLocs)+1 > len(sysvArgRegs) {
		return nil, fmt.Errorf("x86_64ssa: real-asm indirect call supports up to %d args incl. env, got %d",
			len(sysvArgRegs), len(in.ArgLocs)+1)
	}
	s0 := numAlloc     // env staging (free during this inst)
	s1 := numAlloc + 1 // fn_idx, then the resolved target address
	var out []string
	stage := func(dst int, l Loc) {
		if l.IsReg {
			out = append(out, fmt.Sprintf("mov %s, %s", reg(dst), reg(l.Reg)))
		} else {
			out = append(out, fmt.Sprintf("mov %s, %s", reg(dst), slotMem(l.Slot)))
		}
	}
	// Read env (+8) and fn_idx (+0) out of the cell before any register moves.
	stage(scratch, in.IdxLoc)
	out = append(out,
		fmt.Sprintf("mov %s, %s", reg(s0), memRef(reg(scratch), 8)), // env  = cell[8]
		fmt.Sprintf("mov %s, %s", reg(s1), memRef(reg(scratch), 0)), // fn_idx = cell[0]
	)
	// Resolve table[fn_idx] → absolute code address into s1.
	out = append(out,
		fmt.Sprintf("lea %s, [rip + %s]", reg(scratch), fnTableSym),
		fmt.Sprintf("shl %s, 3", reg(s1)),
		fmt.Sprintf("add %s, %s", reg(scratch), reg(s1)),
		fmt.Sprintf("mov %s, %s", reg(s1), memRef(reg(scratch), 0)),
	)
	// Preserve the caller-saved registers live across the call (see callLines).
	saved := callSavedSet(in, numAlloc)
	pad := (len(saved) % 2) * 8
	if pad != 0 {
		out = append(out, "sub rsp, 8")
	}
	for _, r := range saved {
		out = append(out, fmt.Sprintf("push %s", reg(r)))
	}
	// Stash the target address (deepest), then push the args (arg0 first) and the
	// env pointer last, so env lands in the callee's final parameter register.
	out = append(out, fmt.Sprintf("push %s", reg(s1)))
	for _, l := range in.ArgLocs {
		stage(scratch, l)
		out = append(out, fmt.Sprintf("push %s", reg(scratch)))
	}
	out = append(out, fmt.Sprintf("push %s", reg(s0))) // env — last argument
	// Pop into arg registers in reverse (env is the highest-indexed arg), then
	// recover the target address into rax and call it.
	n := len(in.ArgLocs)
	for i := n; i >= 0; i-- {
		out = append(out, fmt.Sprintf("pop %s", sysvArgRegs[i]))
	}
	out = append(out,
		"pop rax", // recover the stashed target address
		"call rax",
		fmt.Sprintf("mov %s, rax", reg(scratch)), // capture result
	)
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
	return out, nil
}

// selectLines renders `Dst = (Src != 0) ? Src2 : Src3` with a conditional branch
// over a unique label. The assembler has no cmov, and a branch-free mask sequence
// would need a second scratch — but materialize hands back the operands' own home
// registers (not fresh copies), so no operand may be clobbered. Writing only Dst
// (reading Src/Src2/Src3) sidesteps that. label must be unique per instruction.
func selectLines(in Inst, label string) []string {
	end := ".Lsel_end_" + label
	out := []string{
		fmt.Sprintf("cmp %s, 0", reg(in.Src)),
		fmt.Sprintf("mov %s, %s", reg(in.Dst), reg(in.Src3)), // default: else
		fmt.Sprintf("je %s", end),
		fmt.Sprintf("mov %s, %s", reg(in.Dst), reg(in.Src2)), // cond != 0: then
		end + ":",
	}
	if fix := maskFix(in.Dst, in.W); fix != "" {
		out = append(out, strings.TrimPrefix(fix, "\n\t"))
	}
	return out
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

// argMoveLines moves call arguments from their allocated homes into the System V
// argument registers (arg i → sysvArgRegs[i]), replacing the push-all/pop-all
// stack round-trip. Reg-homed args are a parallel register copy — an argument
// register may be another argument's home register, resolved by resolveRegMoves
// (which breaks cycles with xchg; callee-saved home registers are never argument
// registers, so they only ever appear as sources and are never clobbered).
// Slot-homed args load from memory afterward: by then every reg-homed source has
// been consumed, so a load into an argument register can't clobber one. Caller-
// saved home registers that are also live across the call were already saved by
// the surrounding sequence, so scrambling them here is undone by the restore.
func argMoveLines(argLocs []Loc) []string {
	var moves [][2]int // {dstArgReg, srcHomeReg} gpRegs indices
	for i, l := range argLocs {
		if l.IsReg {
			if d := gpIndex(sysvArgRegs[i]); d != l.Reg {
				moves = append(moves, [2]int{d, l.Reg})
			}
		}
	}
	out := resolveRegMoves(moves)
	for i, l := range argLocs {
		if !l.IsReg {
			out = append(out, fmt.Sprintf("mov %s, %s", sysvArgRegs[i], slotMem(l.Slot)))
		}
	}
	return out
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
		// `mov r64, imm32` (REX.W C7 /0) sign-extends an i32-range immediate into
		// the full register, which already matches the model's i32 sign-extension —
		// so the movsxd fixup is redundant for the common in-range constant. Only a
		// wider immediate (materialised without sign-extending its low 32 bits)
		// still needs it.
		line := fmt.Sprintf("mov %s, %d", reg(in.Dst), in.Imm)
		if in.Imm < -(1<<31) || in.Imm >= (1<<31) {
			line += maskFix(in.Dst, in.W)
		}
		return line, nil
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
		// Bump allocator with an 8-byte rc header (rc=1 at base+0), mirroring the
		// native __fern_alloc_rc1 layout so __fern_rc_is_unique / the drop helpers
		// find a valid reference count at [data-8]. base = align8(cursor); the
		// returned data pointer is base+8; the cursor advances past header+size.
		// See docs/SSA-RC-RUNTIME.md.
		return strings.Join([]string{
			fmt.Sprintf("mov %s, [rip + %s]", reg(in.Dst), heapPtrSym),
			fmt.Sprintf("add %s, 7", reg(in.Dst)),
			fmt.Sprintf("and %s, -8", reg(in.Dst)),                     // Dst = base (8-aligned)
			fmt.Sprintf("mov dword ptr %s, 1", memRef(reg(in.Dst), 0)), // rc = 1
			fmt.Sprintf("mov %s, %s", reg(scratch), reg(in.Dst)),
			fmt.Sprintf("add %s, %s", reg(scratch), reg(in.Src)),
			fmt.Sprintf("add %s, 8", reg(scratch)), // header bytes
			fmt.Sprintf("mov [rip + %s], %s", heapPtrSym, reg(scratch)),
			fmt.Sprintf("add %s, 8", reg(in.Dst)), // return data = base + 8
		}, "\n\t"), nil
	case MemLoad:
		mem := memRef(reg(in.Src), in.Imm)
		if in.Bytes == 8 {
			return fmt.Sprintf("mov %s, %s", reg(in.Dst), mem) + maskFix(in.Dst, in.W), nil
		}
		if in.Bytes == 4 {
			// 4-byte load: `mov r32, [mem]` zero-extends into the 64-bit reg.
			return fmt.Sprintf("mov %s, %s", reg32n(in.Dst), mem) + maskFix(in.Dst, in.W), nil
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
		case 4:
			return fmt.Sprintf("mov %s, %s", mem, reg32n(in.Src2)), nil
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
		return fCmpSeq(in, scratch)
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

// fCmpSeq renders a scalar float comparison as a 0/1 in Dst, with IEEE
// unordered semantics: every ordered predicate is false when either operand is
// NaN, and only `!=` is true.
//
// `ucomisd` reports unordered as ZF=1 PF=1 CF=1, which is indistinguishable
// from "equal" (ZF=1) or "below" (CF=1) if you read ZF/CF alone. The mapping
// this used to emit did exactly that — sete/setne/setb/setbe — so four of the
// six predicates were wrong on NaN. Only seta (!CF && !ZF) and setae (!CF) are
// unordered-safe as written.
//
// So: the two `>`-family predicates keep their setcc, the two `<`-family ones
// reach the same answer by comparing the operands in the opposite order
// (a < b ⟺ b > a, which holds under IEEE — both are false on NaN), and
// equality consults PF, the only flag that distinguishes unordered from equal.
func fCmpSeq(in Inst, scratch int) (string, error) {
	d, s := reg(in.Dst), reg(in.Src)
	d8, sc8 := reg8n(in.Dst), reg8n(scratch)
	load := []string{
		fmt.Sprintf("movq xmm0, %s", d),
		fmt.Sprintf("movq xmm1, %s", s),
	}
	var body []string
	switch in.K {
	case ssa.OpFGt:
		body = []string{"ucomisd xmm0, xmm1", fmt.Sprintf("seta %s", d8)}
	case ssa.OpFGe:
		body = []string{"ucomisd xmm0, xmm1", fmt.Sprintf("setae %s", d8)}
	case ssa.OpFLt:
		// Operands reversed: `a < b` becomes `b > a`, so the unordered-safe
		// seta does the work.
		body = []string{"ucomisd xmm1, xmm0", fmt.Sprintf("seta %s", d8)}
	case ssa.OpFLe:
		body = []string{"ucomisd xmm1, xmm0", fmt.Sprintf("setae %s", d8)}
	case ssa.OpFEq:
		// Equal AND ordered. PF is set only when unordered, so ZF && !PF.
		body = []string{
			"ucomisd xmm0, xmm1",
			fmt.Sprintf("sete %s", d8),
			fmt.Sprintf("setnp %s", sc8),
			fmt.Sprintf("and %s, %s", d8, sc8),
		}
	case ssa.OpFNe:
		// The negation: not-equal OR unordered.
		body = []string{
			"ucomisd xmm0, xmm1",
			fmt.Sprintf("setne %s", d8),
			fmt.Sprintf("setp %s", sc8),
			fmt.Sprintf("or %s, %s", d8, sc8),
		}
	default:
		return "", fmt.Errorf("x86_64ssa: float compare %v unsupported", in.K)
	}
	lines := append(load, body...)
	lines = append(lines, fmt.Sprintf("movzx %s, %s", d, d8))
	return strings.Join(lines, "\n\t"), nil
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
		// float -> int, truncating toward zero.
		//
		// KNOWN GAP: this does NOT saturate, and the language contract says it
		// must (docs/FLOAT-SEMANTICS.md — NaN → 0, out of range → the
		// destination's min/max, identically on every backend). `cvttsd2si`
		// returns the "integer indefinite" INT_MIN for every invalid input —
		// NaN, ±Inf, out of range — so NaN and +overflow come out wrong, and at
		// 32-bit width maskFix then sign-extends bit 31 of a 64-bit result,
		// which wraps rather than clamps.
		//
		// ssa.Eval — the oracle this package's tests diff against — DOES
		// saturate, so a test with an overflowing operand will fail here. The
		// sibling arm64ssa backend is fixed (its fcvtz{s,u} saturate natively
		// once the destination register width matches). Closing it here means
		// porting the native backend's `emitFloatToIntSat`
		// (internal/codegen/x86_64/x86_64.go): a float-domain compare plus
		// cmov/branch fixup per signedness and width, with the 2^63 bias trick
		// for u64. Left undone because this backend has no CLI target — it is
		// consumed only by arm64ssa for its Inst type — so no program can reach
		// the wrong sequence today.
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

// fnTableSym labels the module's function-address dispatch table: one `.quad`
// per function, in the module's (sorted) emission order — i.e. indexed by the
// same fn_idx a closure cell carries. A real-asm OpCallIndirect resolves its
// callee by indexing this table with the cell's fn_idx.
const fnTableSym = "__ssa_fn_table"

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

// captureEnvLayout returns each capture's byte offset and slot size in the env
// block, plus the total env size, from in.CaptureSlots (the packed layout the
// CaptureRef loads read). A nil/short CaptureSlots falls back to one 8-byte slot
// per capture — the uniform layout hand-built SSA closures assume.
func captureEnvLayout(in Inst) (offs, sizes []int64, total int64) {
	n := len(in.ArgLocs)
	offs = make([]int64, n)
	sizes = make([]int64, n)
	for i := 0; i < n; i++ {
		sz := int64(8)
		if len(in.CaptureSlots) == n {
			sz = int64(in.CaptureSlots[i])
		}
		offs[i] = total
		sizes[i] = sz
		total += sz
	}
	return offs, sizes, total
}

// closureLines renders OpMakeEnv / OpMakeClosure on the .bss bump heap.
// MakeEnv allocates a packed env block over the captures and returns the env
// pointer. MakeClosure additionally allocates a {fn_idx, env_ptr} cell (fn_idx =
// the target's module index) and returns the cell pointer. The env pointer is
// held in s0 (free during this instruction) across the second allocation; s3
// stages the bump cursor and capture values. Captures pack at per-type offsets
// and store widths (i32 at 4-byte slots, pointers at 8) so the env matches the
// CaptureRef load side (see captureEnvLayout / the IR's irCaptureSlotSize).
func closureLines(in Inst, numAlloc int, fnIndex map[string]int) ([]string, error) {
	scratch := numAlloc + 3 // s3
	envReg := numAlloc      // s0 — unused by the MakeEnv/MakeClosure inst itself
	var out []string
	alloc := func(dst int, bytes int64) {
		// Same rc-headed bump as MemAlloc (see asmInst): rc=1 at base+0, data at
		// base+8, cursor past header+bytes. Keeps env blocks and closure cells
		// droppable through __fern_rc_is_unique / the drop helpers.
		out = append(out,
			fmt.Sprintf("mov %s, [rip + %s]", reg(dst), heapPtrSym),
			fmt.Sprintf("add %s, 7", reg(dst)),
			fmt.Sprintf("and %s, -8", reg(dst)),
			fmt.Sprintf("mov dword ptr %s, 1", memRef(reg(dst), 0)), // rc = 1
			fmt.Sprintf("mov %s, %s", reg(scratch), reg(dst)),
			fmt.Sprintf("add %s, %d", reg(scratch), bytes+8),
			fmt.Sprintf("mov [rip + %s], %s", heapPtrSym, reg(scratch)),
			fmt.Sprintf("add %s, 8", reg(dst)), // return data = base + 8
		)
	}
	offs, sizes, envBytes := captureEnvLayout(in)
	storeCaps := func(base int) {
		for i, l := range in.ArgLocs {
			if l.IsReg {
				out = append(out, fmt.Sprintf("mov %s, %s", reg(scratch), reg(l.Reg)))
			} else {
				out = append(out, fmt.Sprintf("mov %s, %s", reg(scratch), slotMem(l.Slot)))
			}
			if sizes[i] == 4 {
				out = append(out, fmt.Sprintf("mov dword ptr %s, %s", memRef(reg(base), offs[i]), reg32n(scratch)))
			} else {
				out = append(out, fmt.Sprintf("mov %s, %s", memRef(reg(base), offs[i]), reg(scratch)))
			}
		}
	}
	if in.Op == MakeEnv {
		alloc(in.Dst, envBytes)
		storeCaps(in.Dst)
		return out, nil
	}
	idx, ok := fnIndex[in.Callee]
	if !ok {
		return nil, fmt.Errorf("x86_64ssa: MakeClosure target %q not in module", in.Callee)
	}
	alloc(envReg, envBytes) // env block -> s0
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

// usesCallIndirect reports whether any emitted program dispatches a closure
// (so the function-address dispatch table is only emitted when needed).
func usesCallIndirect(progs map[string]*Program) bool {
	for _, p := range progs {
		for _, blk := range p.Blocks {
			for _, in := range blk.Insts {
				if in.Op == CallIndirect {
					return true
				}
			}
		}
	}
	return false
}

// runtimeHelperEmitters maps a __fern_* runtime-helper name to the code that
// writes its body into the SSA .text. A helper is emitted iff the module calls
// it (referencedRuntimeHelpers); the bodies mirror the native backends'
// hand-written runtime asm (docs/SSA-RC-RUNTIME.md). Keyed by the exact callee
// name the IR emits, so the `call fn_<name>` site links against the label
// fnLabel(name) writes.
var runtimeHelperEmitters = map[string]func(w func(string, ...any)){
	"__fern_rc_is_unique": emitRcIsUniqueHelper,
	"__fern_rc_inc":       emitRcIncHelper,
	"__fern_rc_dec":       emitRcDecHelper,
	"__fern_closure_drop": emitClosureDropHelper,
	"__fern_box_free":     emitBoxFreeHelper,
	"__str_len":           emitStrLenHelper,
	"__fern_arr_dec":      emitArrDecHelper,
	"__arr_idx":           emitArrIdxHelper,
	"__arr_idx_nc":        emitArrIdxNCHelper,
	"__str_eq":            emitStrEqHelper,
	"__str_concat":        emitStrConcatHelper,
	"__fern_str_dec":      emitStrDecHelper,
}

// heapUsingHelpers are runtime helpers that allocate on the SSA bump heap, so
// the .bss heap section + cursor must be emitted whenever one is referenced even
// if the program body has no direct heap op.
var heapUsingHelpers = map[string]bool{
	"__str_concat": true,
}

// runtimeHelperDeps records the helper→helper call edges (a helper that tail-
// calls another must have that callee emitted too — the module never references
// it directly). Transitively closed by referencedRuntimeHelpers.
var runtimeHelperDeps = map[string][]string{
	"__fern_closure_drop": {"__fern_box_free", "__fern_rc_dec"},
}

// referencedRuntimeHelpers returns, sorted, the runtime-helper names to append to
// .text: every helper any emitted program calls, plus the transitive closure of
// their helper→helper dependencies (runtimeHelperDeps).
func referencedRuntimeHelpers(progs map[string]*Program) []string {
	seen := map[string]bool{}
	var add func(name string)
	add = func(name string) {
		if seen[name] || runtimeHelperEmitters[name] == nil {
			return
		}
		seen[name] = true
		for _, dep := range runtimeHelperDeps[name] {
			add(dep)
		}
	}
	for _, p := range progs {
		for _, blk := range p.Blocks {
			for _, in := range blk.Insts {
				if in.Op == Call || in.Op == CallPair {
					add(in.Callee)
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// emitRcIsUniqueHelper writes __fern_rc_is_unique(data) -> i32: 1 iff data is a
// real, uniquely-owned heap value — non-null, above the low-address guard, not a
// static sentinel (top bit of the rc word set), rc == 1; else 0. The guard chain
// makes it safe on a slot that might hold a non-pointer scalar. Leaf (no calls,
// no frame). Mirrors the arm64/x86-64 stack-machine backends' version.
func emitRcIsUniqueHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rc_is_unique"))
	w("\ttest rdi, rdi")
	w("\tjz .Lssa_rcuniq_no")
	w("\tcmp rdi, 0x10000")
	w("\tjb .Lssa_rcuniq_no")
	w("\tmov eax, %s", memRef("rdi", -8)) // rc word (4-byte) at data-8
	w("\ttest eax, eax")
	w("\tjs .Lssa_rcuniq_no") // sign bit set = static sentinel (0x80000000)
	w("\tcmp eax, 1")
	w("\tjne .Lssa_rcuniq_no")
	w("\tmov eax, 1")
	w("\tret")
	w(".Lssa_rcuniq_no:")
	w("\txor eax, eax")
	w("\tret")
}

// emitRcIncHelper writes __fern_rc_inc(data): bump the reference count at
// [data-8] by one, guarded like __fern_rc_is_unique (null / low-address /
// static-sentinel) so it is safe on a slot that might hold a non-pointer or a
// static cell. Void, leaf. On the SSA path every rc-managed value is a heap
// pointer (function values are heap cells, not code addresses), so the
// 0x10000 low-address guard is sufficient — a code/rodata address never flows
// in. Mirrors the native __fern_rc_inc (minus the SSO string tag, which the SSA
// path has no equivalent of).
func emitRcIncHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rc_inc"))
	w("\ttest rdi, rdi")
	w("\tjz .Lssa_rcinc_ret")
	w("\tcmp rdi, 0x10000")
	w("\tjb .Lssa_rcinc_ret")
	w("\tmov eax, %s", memRef("rdi", -8))
	w("\ttest eax, eax")
	w("\tjs .Lssa_rcinc_ret") // static sentinel
	w("\tadd eax, 1")
	w("\tmov %s, eax", memRef("rdi", -8))
	w(".Lssa_rcinc_ret:")
	w("\tret")
}

// emitRcDecHelper writes __fern_rc_dec(data): drop the reference count at
// [data-8] by one, same guard chain as __fern_rc_inc. It does NOT free at rc==0
// — the SSA bump heap never reclaims (docs/SSA-RC-RUNTIME.md: leak-until-a-later
// reuse slice); the free-and-reclaim decision belongs to __fern_closure_drop /
// the per-type drop thunks. Void, leaf.
func emitRcDecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rc_dec"))
	w("\ttest rdi, rdi")
	w("\tjz .Lssa_rcdec_ret")
	w("\tcmp rdi, 0x10000")
	w("\tjb .Lssa_rcdec_ret")
	w("\tmov eax, %s", memRef("rdi", -8))
	w("\ttest eax, eax")
	w("\tjs .Lssa_rcdec_ret") // static sentinel
	w("\tsub eax, 1")
	w("\tmov %s, eax", memRef("rdi", -8))
	w(".Lssa_rcdec_ret:")
	w("\tret")
}

// emitClosureDropHelper writes __fern_closure_drop(data): the scope-exit drop the
// IR inserts for a closure-valued local. Guarded (null / low-address); reads the
// rc word at [data-8]; if the closure is uniquely held (rc == 1) it tail-calls
// __fern_box_free(data, payload_size) to release the cell, otherwise it
// tail-calls __fern_rc_dec(data) to drop a shared reference. Mirrors the native
// __fern_closure_drop. (Recursive drop of pointer-typed captures via a per-
// closure __closure_drop_<name> thunk is a later slice — scalar captures need
// only the cell release here.)
func emitClosureDropHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_closure_drop"))
	w("\tcmp rdi, 0x10000")
	w("\tjb .Lssa_cd_ret")
	w("\tmov eax, %s", memRef("rdi", -8)) // rc
	w("\tcmp eax, 1")
	w("\tjne .Lssa_cd_dec")               // rc != 1 (shared or static sentinel) → dec
	w("\tmov esi, %s", memRef("rdi", -4)) // payload size → arg2
	w("\tjmp %s", fnLabel("__fern_box_free"))
	w(".Lssa_cd_dec:")
	w("\tjmp %s", fnLabel("__fern_rc_dec"))
	w(".Lssa_cd_ret:")
	w("\tret")
}

// emitBoxFreeHelper writes __fern_box_free(data, size) -> data: release an
// rc-headed heap block. On the SSA bump heap there is no reclamation yet
// (docs/SSA-RC-RUNTIME.md: leak until a later reuse slice), so this is a no-op
// that returns the data pointer — memory-safe and correct for short-lived
// programs. A real freelist return is the follow-up that makes the size arg live.
func emitBoxFreeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_box_free"))
	w("\tmov rax, rdi") // return data unchanged; free is a no-op for now
	w("\tret")
}

// emitStrLenHelper writes __str_len(ptr) -> i32: the byte length of a
// single-word string, stored as a 4-byte field immediately before the data
// (the layout ConstStr emits and the native backends use — length at [ptr-4]).
// Leaf. The IR lowers `s.len()` (OpStrLen) to a call here.
func emitStrLenHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_len"))
	w("\tmov eax, %s", memRef("rdi", -4))
	w("\tret")
}

// emitArrDecHelper writes __fern_arr_dec(data, stride): the array-drop the IR
// inserts at scope exit. The array element pointer carries a 16-byte header with
// its reference count at [data-8] (ArrayLit builds it: cap@-12, rc@-8, len@-4).
// Guarded (null / low-address / static sentinel); if the array is uniquely held
// (rc == 1) the buffer would be freed — a no-op on the SSA bump heap, which
// doesn't reclaim (docs/SSA-RC-RUNTIME.md: leak until a later reuse slice, which
// also skips the recursive per-element drops), so we just return; otherwise it
// drops a shared reference. The stride arg is unused until real reclamation
// lands. Leaf.
func emitArrDecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_arr_dec"))
	w("\ttest rdi, rdi")
	w("\tjz .Lssa_arrdec_ret")
	w("\tcmp rdi, 0x10000")
	w("\tjb .Lssa_arrdec_ret")
	w("\tmov eax, %s", memRef("rdi", -8)) // rc
	w("\ttest eax, eax")
	w("\tjs .Lssa_arrdec_ret") // static sentinel
	w("\tcmp eax, 1")
	w("\tjle .Lssa_arrdec_ret") // rc<=1: unique (leak, no free) or already dropped
	w("\tsub eax, 1")
	w("\tmov %s, eax", memRef("rdi", -8))
	w(".Lssa_arrdec_ret:")
	w("\tret")
}

// emitArrIdxHelper writes __arr_idx(base, idx) -> elem address: a bounds-checked
// index into a length-prefixed i32 (stride-4) array. Compares idx against the
// array's length at [base-4] with a single unsigned compare (a negative idx is
// huge unsigned, so it fails too) and, on out-of-range, exits 134 — matching the
// native array-index trap and wasm's `unreachable`. Returns base + idx*4; the
// caller's OpLoad reads the element. The IR lowers `a[i]` to a call here (the
// native backends inline the same address compute). Leaf.
func emitArrIdxHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__arr_idx"))
	w("\tmov edx, %s", memRef("rdi", -4)) // len
	w("\tcmp esi, edx")
	w("\tjb .Lssa_arridx_ok")
	w("\tmov edi, 134")
	w("\tmov eax, 231") // exit_group
	w("\tsyscall")
	w(".Lssa_arridx_ok:")
	w("\tlea rax, [rdi + rsi*4]")
	w("\tret")
}

// emitArrIdxNCHelper is emitArrIdxHelper minus the bounds check — the
// elided (`_nc`) variant used when the caller proved the index in range
// (ForEach desugar, #4380 lever 3). Just base + idx*4.
func emitArrIdxNCHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__arr_idx_nc"))
	w("\tlea rax, [rdi + rsi*4]")
	w("\tret")
}

// emitStrEqHelper writes __str_eq(a, b) -> i32: 1 if the two single-word strings
// are byte-equal, else 0. Fast paths on pointer identity and length mismatch
// (length at [ptr-4]), then compares bytes. The IR lowers `a == b` on strings
// (OpStrEq) to a call here. Leaf.
func emitStrEqHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_eq"))
	w("\tcmp rdi, rsi")
	w("\tje .Lssa_streq_eq")              // same pointer → equal
	w("\tmov ecx, %s", memRef("rdi", -4)) // len a
	w("\tmov edx, %s", memRef("rsi", -4)) // len b
	w("\tcmp ecx, edx")
	w("\tjne .Lssa_streq_neq") // different lengths
	w("\txor r8d, r8d")        // i = 0
	w(".Lssa_streq_loop:")
	w("\tcmp r8d, ecx")
	w("\tjae .Lssa_streq_eq") // all bytes matched
	w("\tmovzx r9d, byte ptr [rdi + r8]")
	w("\tmovzx r10d, byte ptr [rsi + r8]")
	w("\tcmp r9d, r10d")
	w("\tjne .Lssa_streq_neq")
	w("\tadd r8, 1")
	w("\tjmp .Lssa_streq_loop")
	w(".Lssa_streq_eq:")
	w("\tmov eax, 1")
	w("\tret")
	w(".Lssa_streq_neq:")
	w("\txor eax, eax")
	w("\tret")
}

// emitStrConcatHelper writes __str_concat(a, b) -> new data pointer: allocate a
// fresh length-prefixed string holding a's bytes followed by b's and return its
// data pointer. Inline-bump-allocates the rc-headed block (rc=1 at base+0, total
// length at base+4, data at base+8 — the same header ConstStr / heap strings
// use) and byte-copies each operand, so it needs no calls (no __fern_memcpy /
// callee-saves). The IR lowers `a + b` on strings (OpStrConcat) to a call here.
// Lengths live at [ptr-4]. Leaf.
func emitStrConcatHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_concat"))
	w("\tmov ecx, %s", memRef("rdi", -4)) // la
	w("\tmov edx, %s", memRef("rsi", -4)) // lb
	w("\tmov r8d, ecx")
	w("\tadd r8d, edx") // total = la + lb (zero-extends into r8)
	// Bump-allocate total+8 bytes: base = align8(cursor); rc=1 at base+0, len at
	// base+4; cursor advances past header+total; data = base+8.
	w("\tmov r9, [rip + %s]", heapPtrSym)
	w("\tadd r9, 7")
	w("\tand r9, -8")
	w("\tmov dword ptr [r9], 1") // rc = 1
	w("\tmov [r9 + 4], r8d")     // len = total
	w("\tmov r10, r9")
	w("\tadd r10, 8")
	w("\tadd r10, r8")
	w("\tmov [rip + %s], r10", heapPtrSym)
	w("\tlea rax, [r9 + 8]") // data (return value)
	// Copy a's la bytes: [rax + i] = [rdi + i].
	w("\txor r10, r10")
	w(".Lssa_strcat_a:")
	w("\tcmp r10d, ecx")
	w("\tjae .Lssa_strcat_b")
	w("\tmovzx r11d, byte ptr [rdi + r10]")
	w("\tmov byte ptr [rax + r10], r11b")
	w("\tadd r10, 1")
	w("\tjmp .Lssa_strcat_a")
	// Copy b's lb bytes after a: dest base = data + la.
	w(".Lssa_strcat_b:")
	w("\tlea r9, [rax + rcx]")
	w("\txor r10, r10")
	w(".Lssa_strcat_bl:")
	w("\tcmp r10d, edx")
	w("\tjae .Lssa_strcat_done")
	w("\tmovzx r11d, byte ptr [rsi + r10]")
	w("\tmov byte ptr [r9 + r10], r11b")
	w("\tadd r10, 1")
	w("\tjmp .Lssa_strcat_bl")
	w(".Lssa_strcat_done:")
	w("\tret")
}

// emitStrDecHelper writes __fern_str_dec(ptr): the scope-exit drop for a
// string-valued local. Guarded (null / low-address / immortal-sentinel top bit —
// so it skips .rodata literals); reads the rc at [ptr-8]; if uniquely held
// (rc==1) the heap buffer would be freed — a no-op on the SSA bump heap, which
// doesn't reclaim (leak-until-a-later-reuse slice) — else drops a shared
// reference. Leaf.
func emitStrDecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_str_dec"))
	w("\ttest rdi, rdi")
	w("\tjz .Lssa_strdec_ret")
	w("\tcmp rdi, 0x10000")
	w("\tjb .Lssa_strdec_ret")
	w("\tmov eax, %s", memRef("rdi", -8)) // rc
	w("\ttest eax, eax")
	w("\tjs .Lssa_strdec_ret") // top bit = immortal literal sentinel
	w("\tcmp eax, 1")
	w("\tjle .Lssa_strdec_ret") // rc<=1: unique (leak) or already dropped
	w("\tsub eax, 1")
	w("\tmov %s, eax", memRef("rdi", -8))
	w(".Lssa_strdec_ret:")
	w("\tret")
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
	// A logical right shift at 32-bit width must operate on the 32-bit register:
	// a u32 with bit 31 set is stored sign-extended (1s in bits 32-63), so a
	// 64-bit `shr` would drag those bits into the result (the u32 `>>` bug that
	// miscompiled SHA-256). The 32-bit form reads only the low 32 bits and zero-
	// extends; the caller's trailing maskFix re-sign-extends to the storage
	// convention. `shl`/`sar` are correct on the full register (shl's excess bits
	// are masked off by maskFix; sar wants the sign-extended operand).
	dst := reg(in.Dst)
	if in.K == ssa.OpShrU && in.W != 64 {
		dst = reg32[in.Dst]
	}
	return strings.Join([]string{
		"push rcx",
		fmt.Sprintf("mov %s, %s", reg(rcxReg), reg(in.Src)),
		fmt.Sprintf("%s %s, cl", mnem, dst),
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
	// Unsigned divide/remainder at 32-bit width uses the 32-bit registers so the
	// sign-extended high bits of a u32 operand (1s in bits 32-63 when bit 31 is
	// set) don't corrupt the unsigned 64-bit division. The result lands zero-
	// extended and the caller's maskFix re-sign-extends. Signed and 64-bit ops use
	// the full registers — their sign-extended operands are already correct.
	u32 := !signed && in.W != 64
	r := reg
	if u32 {
		r = func(i int) string { return reg32[i] }
	}
	lines := []string{
		"push rax",
		"push rdx",
		fmt.Sprintf("mov %s, %s", r(scratch), r(in.Src)), // stash divisor
		fmt.Sprintf("mov %s, %s", r(raxReg), r(in.Dst)),  // dividend -> (r|e)ax
	}
	switch {
	case signed:
		lines = append(lines, "cqo", fmt.Sprintf("idiv %s", r(scratch)))
	case u32:
		lines = append(lines, "xor edx, edx", fmt.Sprintf("div %s", r(scratch)))
	default:
		lines = append(lines, "xor rdx, rdx", fmt.Sprintf("div %s", r(scratch)))
	}
	resultReg := raxReg // quotient
	if rem {
		resultReg = rdxReg // remainder
	}
	lines = append(lines,
		fmt.Sprintf("mov %s, %s", r(scratch), r(resultReg)), // capture result before pops
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
