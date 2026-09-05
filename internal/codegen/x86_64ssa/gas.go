package x86_64ssa

import (
	"fmt"
	"github.com/jakechampion/lang/internal/strerror"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	nativex86_64 "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/ssa"
)

// sysvArgRegs is the System V AMD64 integer argument-register sequence: the
// first six integer/pointer args arrive in these registers, in order.
var sysvArgRegs = []string{"rdi", "rsi", "rdx", "rcx", "r8", "r9"}

// stackArgCount is how many of n arguments travel on the stack rather than in
// sysvArgRegs. Arguments past the sixth are the caller's to push, so this is a
// property of a call site, not a limit on how many parameters a function has.
func stackArgCount(n int) int {
	if n <= len(sysvArgRegs) {
		return 0
	}
	return n - len(sysvArgRegs)
}

// inArgMem is where the callee finds the argument that landed in stack-argument
// position k. The frame is rbp-based (push rbp; mov rbp, rsp), so the saved rbp
// is at [rbp], the return address at [rbp + 8], and the first stack argument at
// [rbp + 16].
func inArgMem(k int) string { return fmt.Sprintf("qword ptr [rbp + %d]", 16+8*k) }

// pushStackArgs pushes the arguments past the register half, highest index
// first, so the lowest-numbered stack argument ends up at [rsp] when the call
// executes — which is where the callee's inArgMem(0) reads it from.
func pushStackArgs(argLocs []Loc) []string {
	var out []string
	for i := len(argLocs) - 1; i >= len(sysvArgRegs); i-- {
		if l := argLocs[i]; l.IsReg {
			out = append(out, fmt.Sprintf("push %s", reg(l.Reg)))
		} else {
			out = append(out, fmt.Sprintf("push qword ptr %s", slotMem(l.Slot)))
		}
	}
	return out
}

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
	// Resolve every op's result width across the module before instruction
	// selection: which results are addresses (and so must never be narrowed to
	// 32 bits) is a whole-module question, and no caller can be expected to
	// remember to ask it.
	ssa.ResolveWidths(funcs)
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
		line := fmt.Sprintf(format, args...)
		if isDeadSelfMove(line) {
			return
		}
		b.WriteString(line)
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
	sentLabels, sentOrder := collectSentinels(progs, names)
	// fn_idx for closures: a function's index in the module's (sorted) emission
	// order — the same value the model's function-index table carries. Indices
	// are 1-based: table slot 0 is the reserved null reference (see fnTableSym).
	fnIndex := make(map[string]int, len(names))
	for i, n := range names {
		fnIndex[n] = i + 1
	}

	w(".intel_syntax noprefix")
	w(".text")
	w(".globl _start")
	w("_start:")
	if heap {
		emitHeapReserve(w)
	}
	// Load the entry arguments before the call: the first six in the SysV
	// argument registers, the rest pushed. The kernel enters _start with rsp
	// 16-aligned, so an odd number of stack arguments needs a pad to keep the
	// callee's own frame aligned.
	entryStack := stackArgCount(len(ep.ParamLocs))
	entryArg := func(i int) int64 {
		if i < len(entryArgs) {
			return entryArgs[i]
		}
		return 0
	}
	if entryStack%2 != 0 {
		w("\tsub rsp, 8")
	}
	for i := len(ep.ParamLocs) - 1; i >= len(sysvArgRegs); i-- {
		w("\tmov rax, %d", entryArg(i))
		w("\tpush rax")
	}
	for i := 0; i < len(ep.ParamLocs) && i < len(sysvArgRegs); i++ {
		w("\tmov %s, %d", sysvArgRegs[i], entryArg(i))
	}
	// No cleanup after the call: _start exits through the syscall below and
	// never returns, so the pushed arguments die with the process.
	w("\tcall %s", fnLabel(entry))
	w("\tmov edi, eax")     // exit code = return value
	w("\tmov eax, %d", 231) // sysExitGroup
	w("\tsyscall")
	w("")
	for _, name := range names {
		if err := emitFuncBody(w, name, progs[name], numAlloc, strLabels, sentLabels, fnIndex); err != nil {
			return "", err
		}
	}
	emitRuntimeHelpers(w, helpers)
	if usesBcopy(helpers) {
		emitBcopy(w)
	}
	if heap {
		emitHeapGuard(w)
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
	if len(sentOrder) > 0 {
		// Shared static enum sentinels: one 4-byte cell per distinct tag, holding
		// the tag at offset 0 — the same [ptr+0] load a heap `[tag=N]` box answers,
		// so a match site does not care which it got. Each cell carries the string
		// literals' 8-byte immortal header (rc sentinel 0x80000000 at [ptr-8],
		// padding at [ptr-4]) so a scope-exit drop short-circuits instead of
		// writing to .rodata. Consecutive cells stay contiguous, so each label's
		// header is exactly the 8 bytes before it.
		w("")
		w(".section .rodata")
		for _, tag := range sentOrder {
			w("\t.4byte 0x80000000")
			w("\t.4byte 0")
			w("%s:", sentLabels[tag])
			w("\t.4byte %d", tag)
		}
	}
	if targets := staticClosureTargets(progs, names); len(targets) > 0 {
		// Capture-free closure cells: {fn_idx, env=0, drop_idx=0, 0}, the same
		// four words closureLines writes on the heap for a capturing one. Each
		// carries the immortal 8-byte header a string literal does, so inc / dec
		// / is_unique and closure_drop all short-circuit rather than write a
		// read-only cell, and the reuse pass never takes one as a token.
		w("")
		w(".section .rodata")
		for _, t := range targets {
			w(".align 8")
			w("\t.4byte 0x80000000")
			w("\t.4byte 0")
			w("%s:", staticClosureLabel(fnIndex[t]))
			w("\t.quad %d", fnIndex[t])
			w("\t.quad 0")
			w("\t.quad 0")
			w("\t.quad 0")
		}
	}
	if usesCallIndirect(progs) {
		// Function-address dispatch table: a reserved null slot, then one .quad
		// per function in module (sorted) order, so table[fn_idx] is the callee's
		// absolute address. A closure cell carries fn_idx; OpCallIndirect indexes
		// this table.
		w("")
		w(".section .rodata")
		w(".align 8")
		w("%s:", fnTableSym)
		w("\t.quad 0")
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
		w("%s:", heapEndSym)
		w("\t.quad 0")
	}
	w(".section .note.GNU-stack,\"\",@progbits")
	asm := b.String()
	if err := checkNoDanglingCalls(asm); err != nil {
		return "", err
	}
	return asm, nil
}

// emitFuncBody writes one function's label, prologue, parameter moves, block
// bodies, and epilogue. Block labels are namespaced by the function label so
// several functions coexist in one program.
func emitFuncBody(w func(string, ...any), name string, p *Program, numAlloc int, strLabels map[string]string, sentLabels map[int64]string, fnIndex map[string]int) error {
	label := fnLabel(name)
	// s3 — the last register in the file — is the free scratch the div/shift and
	// call sequences stage operands through. It is above the allocatable range,
	// so it never aliases the fixed registers those sequences pin (see gpRegs on
	// why sharing r8/r9 with the argument registers is safe) nor any register in
	// a call's save set, which holds allocatable homes only.
	scratch := p.NumRegFile - 1

	// Callee-saved registers this function actually clobbers. Per the System V
	// ABI the function must preserve them for its caller, so they are pushed
	// below the spill area and popped at every return. A leaf that touches none
	// of them pays nothing.
	//
	// The set is read back off the emitted text rather than predicted from the
	// Program, because predicting it means keeping a list of every field and
	// every helper that can name a register, and a list that falls behind drops
	// a save silently — the caller gets a clobbered register and the failure
	// surfaces as a wrong answer somewhere else entirely. So the body is emitted
	// once into a buffer with nothing saved, scanned for what it actually
	// mentions, and only then emitted for real. Adding the saves cannot widen
	// the set: they name only registers already in it.
	var probe strings.Builder
	probeW := func(format string, args ...any) {
		fmt.Fprintf(&probe, format+"\n", args...)
	}
	if err := emitFuncBlocks(probeW, label, p, numAlloc, scratch, strLabels, sentLabels, fnIndex, func() {}); err != nil {
		return err
	}
	saved := calleeSavedIn(probe.String(), p.NumRegFile)
	restore := func() {
		for i := len(saved) - 1; i >= 0; i-- {
			w("\tpop %s", reg(saved[i]))
		}
	}

	w("%s:", label)
	w("\tpush rbp")
	w("\tmov rbp, rsp")
	// The spill area is reserved first so a slot's [rbp - 8*(n+1)] never lands on
	// a pushed register, and the reservation absorbs whatever padding the pushes
	// need: the two together shift rsp by a multiple of 16, which is the
	// alignment the call sequences' own padding assumes for the body.
	frame := align16(8*(p.NumSlots+len(saved))) - 8*len(saved)
	if frame > 0 {
		w("\tsub rsp, %d", frame)
	}
	for _, r := range saved {
		w("\tpush %s", reg(r))
	}
	for _, line := range paramMoveLines(p.ParamLocs, scratch) {
		w("\t%s", line)
	}

	return emitFuncBlocks(w, label, p, numAlloc, scratch, strLabels, sentLabels, fnIndex, restore)
}

// emitFuncBlocks writes every block body and terminator. Split out of
// emitFuncBody so the same emission can be run twice: once into a scratch buffer
// to discover which callee-saved registers the output names, then once for real
// with the matching save/restore. `restore` emits the callee-saved reloads that
// precede each return.
func emitFuncBlocks(w func(string, ...any), label string, p *Program, numAlloc, scratch int, strLabels map[string]string, sentLabels map[int64]string, fnIndex map[string]int, restore func()) error {
	for bi, blk := range p.Blocks {
		w(".L_%s_b%d:", label, bi)
		insts, cmpLine, jcc := fuseBranchCmp(blk)
		for _, in := range insts {
			if in.Op == Select {
				for _, l := range selectLines(in) {
					w("\t%s", l)
				}
				continue
			}
			if lines, ok := inlinePokeLines(in, numAlloc); ok {
				for _, l := range lines {
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
			if in.Op == EnumSentinel {
				lbl, ok := sentLabels[in.Imm]
				if !ok {
					return fmt.Errorf("x86_64ssa: EnumSentinel tag %d has no .rodata cell", in.Imm)
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
		if cmpLine != "" {
			w("\t%s", cmpLine)
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
			if jcc != "" {
				w("\t%s .L_%s_b%d", jcc, label, blk.Term.True)
			} else {
				w("\ttest %s, %s", reg(blk.Term.CondReg), reg(blk.Term.CondReg))
				w("\tjnz .L_%s_b%d", label, blk.Term.True)
			}
			w("\tjmp .L_%s_b%d", label, blk.Term.False)
		default:
			return fmt.Errorf("x86_64ssa: unsupported terminator %d in real asm", blk.Term.Kind)
		}
	}
	return nil
}

// fuseBranchCmp decides how a block's conditional branch reads the comparison
// that produced its condition, and returns the instructions still to render,
// the bare `cmp` that replaces a dropped SetCmp, and the conditional jump to
// branch with. jcc is "" when neither applies and the caller falls back to
// testing the 0/1 in a register.
//
// Two independent savings, in increasing strength:
//
//   - `test`/`jnz` is always redundant when the block's last instruction is the
//     SetCmp defining CondReg. Neither setcc nor movzx writes flags, so at the
//     branch the flags still describe that cmp and a direct jcc reads them.
//     This holds however many other sites read the 0/1.
//   - When Term.CondFuse also says the terminator is the comparison's only
//     reader, the 0/1 need not exist at all: the SetCmp is rendered as its
//     leading `cmp` alone and the setcc/movzx pair goes away with it.
//
// Together this is the five-instruction sequence in #6979 item 3 (cmp, setcc,
// movzx, test, jcc) reduced to the two the stack machine emits.
//
// SetCmp only: an FCmp's flags come from a ucomisd whose condition codes do not
// match the predicate (see fCmpSeq), and the FEq/FNe sequences end in a
// flag-writing `and`/`or` on the byte, so neither reduction is sound there.
func fuseBranchCmp(blk MBlock) (insts []Inst, cmpLine, jcc string) {
	insts = blk.Insts
	if blk.Term.Kind != TBrIf || len(insts) == 0 {
		return insts, "", ""
	}
	c := insts[len(insts)-1]
	if c.Op != SetCmp || c.Dst != blk.Term.CondReg {
		return insts, "", ""
	}
	cc, ok := jccMnemonic(c.K)
	if !ok {
		return insts, "", ""
	}
	if !blk.Term.CondFuse {
		return insts, "", cc
	}
	insts = insts[:len(insts)-1]
	// With the SetCmp gone, the copy that only existed to put its left operand
	// in its destination register has no reader left either: `cmp` discards its
	// result into the flags, so it can name the copy's source directly.
	//
	// c.Src != c.Dst is what makes that rewrite equivalent, not a nicety: a
	// comparison reading its own destination on the right reads the value the
	// copy put there, so dropping the copy would compare a stale register.
	left := c.Dst
	if n := len(insts); n > 0 && c.Src != c.Dst {
		if m := insts[n-1]; m.Op == MovReg && m.Dst == c.Dst {
			left = m.Src
			insts = insts[:n-1]
		}
	}
	return insts, fmt.Sprintf("cmp %s, %s", reg(left), reg(c.Src)), cc
}

// jccMnemonic is the conditional jump that branches on what setccMnemonic's
// setcc would have stored. Every x86 condition spells its jcc and its setcc
// with the same suffix, so that one table defines both.
func jccMnemonic(k ssa.OpKind) (string, bool) {
	cc, ok := setccMnemonic(k)
	if !ok {
		return "", false
	}
	return "j" + strings.TrimPrefix(cc, "set"), true
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

// calleeSavedNames are the System V general-purpose registers a function must
// preserve for its caller. Every other register in gpRegs is clobberable across
// a call. Naming them rather than their gpRegs indices is what lets gpRegs be
// reordered without three tables drifting apart.
var calleeSavedNames = map[string]bool{"rbx": true, "r12": true, "r13": true, "r14": true, "r15": true}

// isCallerSaved reports whether gpRegs index r is a System V caller-saved
// register (clobberable across a call).
func isCallerSaved(r int) bool { return !calleeSavedNames[gpRegs[r]] }

// calleeSavedRegs are the gpRegs indices of calleeSavedNames, ascending.
var calleeSavedRegs = func() []int {
	var out []int
	for r := range gpRegs {
		if !isCallerSaved(r) {
			out = append(out, r)
		}
	}
	return out
}()

// calleeSavedIn returns, in ascending order, the callee-saved gpRegs indices the
// emitted text `asm` mentions — the registers the function must preserve for its
// caller.
//
// Reading it back off the text is what makes it complete. The alternative is a
// list of every Program field and every line helper that can name a register,
// and the two failure directions are not symmetric: an over-wide set costs one
// push and one pop, while a missed register is returned to the caller clobbered,
// with nothing failing until some unrelated code reads it.
//
// Tokens keep `_` and `.` so a label like `.Lssa_strcat_bl` stays one word and
// does not read as `bl`. Matching a register anywhere on a line (rather than
// only in a write position) over-approximates deliberately: a function that
// merely reads one still gets it saved, which is safe and keeps the scan free of
// per-opcode operand knowledge.
func calleeSavedIn(asm string, numRegFile int) []int {
	seen := map[string]bool{}
	for _, tok := range regTokenRe.FindAllString(asm, -1) {
		seen[tok] = true
	}
	var out []int
	for _, r := range calleeSavedRegs {
		if r >= numRegFile {
			continue
		}
		for _, name := range regSpellings(r) {
			if seen[name] {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// regTokenRe splits assembly into identifier-ish tokens, treating `_` and `.` as
// word characters so label names cannot decompose into register names.
var regTokenRe = regexp.MustCompile(`[A-Za-z_.][A-Za-z0-9_.]*`)

// regSpellings returns every width spelling of gpRegs index r, so a 32-bit or
// byte-width use counts as a use.
func regSpellings(r int) [4]string { return [4]string{gpRegs[r], reg32[r], reg16[r], reg8[r]} }

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
// The first six arguments move from their homes into rdi/rsi/… as a parallel
// copy (argMoveLines); the rest are pushed, and pushed FIRST, because that copy
// is what scrambles the homes they read. The result (rax) is captured into the
// scratch register, which is never in the saved set, so the restores don't
// overwrite it.
func callLines(in Inst, numAlloc, scratch, s0 int) ([]string, error) {
	saved := callSavedSet(in, numAlloc)
	nStack := stackArgCount(len(in.ArgLocs))
	var out []string
	// 16-byte stack alignment at the call: rsp is 16-aligned in the body, and
	// the pad, the saved registers and the stack arguments are all that shift
	// it, so pad to make their combined count even.
	pad := ((len(saved) + nStack) % 2) * 8
	if pad != 0 {
		out = append(out, "sub rsp, 8")
	}
	for _, r := range saved {
		out = append(out, fmt.Sprintf("push %s", reg(r)))
	}
	// The stack arguments are pushed before the register half is shuffled: they
	// read the arguments' homes, and argMoveLines is what scrambles those.
	out = append(out, pushStackArgs(in.ArgLocs)...)
	regArgs := in.ArgLocs
	if nStack > 0 {
		regArgs = in.ArgLocs[:len(sysvArgRegs)]
	}
	out = append(out, argMoveLines(regArgs)...)
	out = append(out, fmt.Sprintf("call %s", fnLabel(in.Callee)))
	if nStack > 0 {
		out = append(out, fmt.Sprintf("add rsp, %d", 8*nStack))
	}
	restore := func() {
		for i := len(saved) - 1; i >= 0; i-- {
			out = append(out, fmt.Sprintf("pop %s", reg(saved[i])))
		}
		if pad != 0 {
			out = append(out, "add rsp, 8")
		}
	}
	maskDst := func() {
		if fix := maskFix(in.Dst, in.W); fix != "" {
			out = append(out, strings.TrimPrefix(fix, "\n\t"))
		}
	}
	// The result registers can be written before the restores — skipping the
	// staging scratch entirely — exactly when the restores do not write them.
	// The allocator cannot put a result and a value live ACROSS the same call in
	// one register (their intervals overlap), so this holds for every call; the
	// check is what keeps that an optimisation rather than a load-bearing
	// assumption about a pass in another package.
	if !inSaveSet(saved, in.Dst) && (in.Op != CallPair || !inSaveSet(saved, in.Dst2)) {
		// System V returns in rax (tag) / rdx (payload), so delivering a pair into
		// its destinations is a parallel copy over abstract indices. Self-moves
		// are dropped rather than emitted as `mov rax, rax`, which is the whole
		// point of coalescing here.
		var moves [][2]int
		if in.Dst != raxReg {
			moves = append(moves, [2]int{in.Dst, raxReg})
		}
		if in.Op == CallPair && in.Dst2 != rdxReg {
			moves = append(moves, [2]int{in.Dst2, rdxReg})
		}
		out = append(out, resolveRegMoves(moves)...)
		restore()
		maskDst()
		return out, nil
	}
	out = append(out, fmt.Sprintf("mov %s, rax", reg(scratch))) // capture result (tag)
	if in.Op == CallPair {
		// The second return (payload) is in rdx. Capture it into s0 — free during
		// the call inst and not in the caller-saved set — so the restores below
		// don't overwrite it.
		out = append(out, fmt.Sprintf("mov %s, rdx", reg(s0)))
	}
	restore()
	out = append(out, fmt.Sprintf("mov %s, %s", reg(in.Dst), reg(scratch))) // place result
	maskDst()
	if in.Op == CallPair {
		out = append(out, fmt.Sprintf("mov %s, %s", reg(in.Dst2), reg(s0))) // place payload
	}
	return out, nil
}

// inSaveSet reports whether the call-save set contains register r — i.e. whether
// a pop writes it after the call returns.
func inSaveSet(saved []int, r int) bool {
	for _, s := range saved {
		if s == r {
			return true
		}
	}
	return false
}

// callIndirectLines renders a closure dispatch on the real-asm path. in.IdxLoc
// is a pointer to a closure cell (or its drop sub-pair): fn_idx (at +0) indexes
// the module function-address table (fnTableSym); env_ptr (at +8) is appended as the
// callee's LAST argument (docs/SSA-CLOSURE-DISPATCH.md). Caller-saved registers
// are conservatively preserved as in callLines. The resolved target address is
// stashed on the stack across the argument shuffle (so no scratch register needs
// to dodge the arg registers) and read back into rax — never an argument
// register, and its own live value is already in the caller-saved set — to be
// called register-indirect. It is read rather than popped because any stack
// arguments sit between it and rsp, waiting for the callee.
func callIndirectLines(in Inst, numAlloc, scratch int) ([]string, error) {
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
	// The env pointer rides as the callee's final argument, so the argument
	// sequence is one longer than ArgLocs.
	nArgs := len(in.ArgLocs) + 1
	nStack := stackArgCount(nArgs)
	nReg := nArgs - nStack
	// Everything below the call shifts rsp by 8: the pad, the saved registers,
	// the stashed target, and the stack arguments. The register-half pushes are
	// popped again before the call, so they do not count.
	pad := ((len(saved) + 1 + nStack) % 2) * 8
	if pad != 0 {
		out = append(out, "sub rsp, 8")
	}
	for _, r := range saved {
		out = append(out, fmt.Sprintf("push %s", reg(r)))
	}
	// pushArg pushes the i'th argument in the callee's numbering; the last one
	// is the env pointer, already staged in s0.
	pushArg := func(i int) {
		if i == nArgs-1 {
			out = append(out, fmt.Sprintf("push %s", reg(s0)))
			return
		}
		stage(scratch, in.ArgLocs[i])
		out = append(out, fmt.Sprintf("push %s", reg(scratch)))
	}
	// Stash the target address deepest, then the stack arguments highest-index
	// first so the lowest lands at [rsp] once the register half is popped off,
	// then the register half (arg0 first) to be popped into its registers.
	out = append(out, fmt.Sprintf("push %s", reg(s1)))
	for i := nArgs - 1; i >= nReg; i-- {
		pushArg(i)
	}
	for i := 0; i < nReg; i++ {
		pushArg(i)
	}
	// Pop into arg registers in reverse, then recover the target address and
	// call it. The target sits above the stack arguments, which stay in place
	// for the callee to read.
	for i := nReg - 1; i >= 0; i-- {
		out = append(out, fmt.Sprintf("pop %s", sysvArgRegs[i]))
	}
	out = append(out,
		fmt.Sprintf("mov rax, qword ptr [rsp + %d]", 8*nStack), // recover the stashed target
		"call rax",
		fmt.Sprintf("mov %s, rax", reg(scratch)), // capture result
		fmt.Sprintf("add rsp, %d", 8*(nStack+1)), // drop the stack args + the stash
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

// selectLines renders `Dst = (Src != 0) ? Src2 : Src3` branch-free: the
// else value moves in, then cmovne overwrites it with the then value. Only
// Dst is written — materialize hands back the operands' own home registers,
// not fresh copies, so no operand may be clobbered — which is also what
// makes the arm64 backend's csel the same shape.
func selectLines(in Inst) []string {
	out := []string{
		fmt.Sprintf("cmp %s, 0", reg(in.Src)),
		fmt.Sprintf("mov %s, %s", reg(in.Dst), reg(in.Src3)),
		fmt.Sprintf("cmovne %s, %s", reg(in.Dst), reg(in.Src2)),
	}
	if fix := maskFix(in.Dst, in.W); fix != "" {
		out = append(out, strings.TrimPrefix(fix, "\n\t"))
	}
	return out
}

// gpRegs is the allocatable+scratch register pool (rsp/rbp reserved for the
// frame). reg8 is the parallel 8-bit subregister used by setcc.
// isDeadSelfMove reports whether `line` is a 64-bit register-to-itself move,
// which the CPU does nothing for. Register allocation leaves a few behind
// (a result already in its home register still gets a placement mov), and they
// reach the emitted text, where they cost a byte count and make the assembly
// harder to read past while reviewing anything else (#6979).
//
// WIDTH IS THE WHOLE CONDITION, NOT A DETAIL. A 32-bit self-move is NOT a
// no-op: `mov eax, eax` zero-extends into the upper 32 bits, and truncOrExt
// emits exactly that, deliberately, as the u32 conversion. Dropping a
// self-move by operand equality alone would delete it and silently miscompile
// every u32 narrowing. So only the 64-bit names qualify; every other width is
// doing work.
func isDeadSelfMove(line string) bool {
	t := strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(t, "mov ")
	if !ok {
		return false
	}
	dst, src, ok := strings.Cut(rest, ", ")
	if !ok || dst != src {
		return false
	}
	for _, r := range gpRegs {
		if dst == r {
			return true
		}
	}
	return false
}

// gpRegs maps an abstract register index to a physical register. The order is
// load-bearing in one way: the last numScratch entries are the emitter's staging
// registers, so they must be CALLER-saved, leaving every callee-saved register
// (rbx, r12–r15) inside the allocatable file. Staging is dead across a call and
// costs nothing to lose, while a value the allocator homes in a callee-saved
// register crosses a call for free — with the split the other way round the
// allocator had rbx and nothing else, and paid a push/pop per call site instead.
//
// r8 and r9 are System V argument registers as well as scratch. That is safe
// because no staged value is live when a call's argument shuffle writes them;
// TestAsmRunStackArgsDirectCall covers the five- and six-argument shapes that
// would break if one ever were.
var gpRegs = []string{"rax", "rbx", "rcx", "rdx", "rsi", "rdi", "r12", "r13", "r14", "r15", "r8", "r9", "r10", "r11"}

// DefaultNumAlloc is the largest allocatable file EmitAsmModule accepts, and so
// the size a caller with no reason to pick otherwise should ask for.
//
// It is len(gpRegs) MINUS numScratch, not len(gpRegs): Program.NumRegFile is
// `numAlloc + numScratch` (the scratch registers sit above the allocatable file
// and are mapped out of the same gpRegs), and EmitAsmModule refuses a function
// whose NumRegFile exceeds the mapping. Asking for all fourteen therefore
// refuses every function, with a message that reads like a program too complex
// to allocate rather than a caller asking for an impossible file. Tests sweep
// smaller files deliberately, to exercise spilling.
var DefaultNumAlloc = len(gpRegs) - numScratch
var reg8 = []string{"al", "bl", "cl", "dl", "sil", "dil", "r12b", "r13b", "r14b", "r15b", "r8b", "r9b", "r10b", "r11b"}
var reg32 = []string{"eax", "ebx", "ecx", "edx", "esi", "edi", "r12d", "r13d", "r14d", "r15d", "r8d", "r9d", "r10d", "r11d"}
var reg16 = []string{"ax", "bx", "cx", "dx", "si", "di", "r12w", "r13w", "r14w", "r15w", "r8w", "r9w", "r10w", "r11w"}

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
//   - Stack params last. Their homes may be argument registers that steps A and
//     B still need as sources, and by here those are all consumed. A slot-homed
//     one goes through `scratch`, since x86 has no memory-to-memory mov.
func paramMoveLines(paramLocs []Loc, scratch int) []string {
	var out []string
	regParams := paramLocs
	if len(regParams) > len(sysvArgRegs) {
		regParams = regParams[:len(sysvArgRegs)]
	}
	// Step A: slot-homed params (read arg regs, write memory).
	for i, loc := range regParams {
		if !loc.IsReg && loc.Slot >= 0 {
			out = append(out, fmt.Sprintf("mov %s, %s", slotMem(loc.Slot), sysvArgRegs[i]))
		}
	}
	// Step B: register-homed params — parallel register copy.
	var moves [][2]int // {dst, src} gpRegs indices
	for i, loc := range regParams {
		if loc.IsReg {
			if src := gpIndex(sysvArgRegs[i]); src != loc.Reg {
				moves = append(moves, [2]int{loc.Reg, src})
			}
		}
	}
	out = append(out, resolveRegMoves(moves)...)
	// Step C: stack params.
	for i := len(sysvArgRegs); i < len(paramLocs); i++ {
		loc := paramLocs[i]
		mem := inArgMem(i - len(sysvArgRegs))
		switch {
		case loc.IsReg:
			out = append(out, fmt.Sprintf("mov %s, %s", reg(loc.Reg), mem))
		case loc.Slot >= 0:
			out = append(out,
				fmt.Sprintf("mov %s, %s", reg(scratch), mem),
				fmt.Sprintf("mov %s, %s", slotMem(loc.Slot), reg(scratch)))
		}
	}
	return out
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
		case ssa.OpClz, ssa.OpCtz, ssa.OpPopcount:
			// in.W is the OPERAND width, so the 32-bit form is the 32-bit
			// register — lzcnt on the full 64-bit register would count the
			// zero-extended high half too. LZCNT/TZCNT rather than bsr/bsf
			// because the IR defines the zero case as the operand width, which
			// is what they give; the Haswell baseline (docs/BACKEND-PARITY.md)
			// makes them assumable. A 32-bit destination zero-extends, so the
			// count is already a clean i32 in the full register.
			r := reg32n(d)
			if in.W == 64 {
				r = reg(d)
			}
			return fmt.Sprintf("%s %s, %s", bitCountMnemonic(in.K), r, r), nil
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
			heapGuardCall,
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
// from "equal" (ZF=1) or "below" (CF=1) if you read ZF/CF alone. So the
// obvious mapping — sete/setne/setb/setbe — is wrong on NaN for four of the
// six predicates. Only seta (!CF && !ZF) and setae (!CF) are unordered-safe
// as written.
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

// Heap symbols + size backing the x86-64 SSA bump allocator: a lazy mmap
// reservation seeded in _start, mirroring the stack-machine backend's
// __fern_alloc arena (internal/codegen/x86_64) — same 16 GiB MAP_NORESERVE
// window, same diagnostic and exit status when a bump runs past it. Pages
// commit only as they are touched, so the window costs nothing until a program
// grows into it.
const (
	heapPtrSym   = "__ssa_heap_ptr"
	heapEndSym   = "__ssa_heap_end"
	heapGuardSym = "__ssa_heap_guard"
	heapOOMLabel = ".Lssa_heap_oom"
	heapOOMMsg   = "__ssa_msg_oom"

	// The reservation: heapBytes at heapHint. The hint sits at 16 GiB rather
	// than low in the address space so that every address handed out has bits
	// above 31 set — arithmetic that narrows a pointer to 32 bits is then wrong
	// for the first allocation of the smallest program, instead of being
	// invisible until a program grows past 2 GiB (#7329).
	heapHint  = 0x400000000
	heapBytes = 0x400000000 // 16 GiB

	// The reservation's last page is not handed out: a bump site writes the
	// object's rc header at the new block's base BEFORE it publishes the cursor
	// and reaches the guard, so the bytes just past the limit must still be
	// mapped for the guard to report exhaustion instead of faulting on that
	// header.
	heapSlackBytes = 4096
)

// emitHeapReserve seeds the arena in _start: one lazy anonymous mmap with the
// same MAP_NORESERVE flags the stack-machine backend's __fern_alloc uses, then
// the cursor/limit pair the guard compares.
//
// The limit is the reservation's end minus the slack page (see heapSlackBytes).
func emitHeapReserve(w func(string, ...any)) {
	w("\tmovabs rdi, %d", heapHint)
	w("\tmovabs rsi, %d", heapBytes)
	w("\tmov edx, 3")       // PROT_READ|PROT_WRITE
	w("\tmov r10d, 0x4022") // MAP_PRIVATE|MAP_ANONYMOUS|MAP_NORESERVE
	w("\tmov r8d, -1")      // fd
	w("\txor r9d, r9d")     // offset
	w("\tmov eax, 9")       // mmap
	w("\tsyscall")
	w("\tcmp rax, 0")
	w("\tjl %s", heapOOMLabel)
	w("\tmov [rip + %s], rax", heapPtrSym)
	w("\tmov rcx, rax")
	w("\tmovabs rdx, %d", heapBytes-heapSlackBytes)
	w("\tadd rcx, rdx")
	w("\tmov [rip + %s], rcx", heapEndSym)
}

// emitHeapGuard writes __ssa_heap_guard: compare the freshly published cursor
// against the limit and abort with the arena diagnostic if it has run past.
//
// A call rather than an inline compare because a bump site has no uniformly free
// register and some keep flags live across the allocation, so the guard saves
// both. The diagnostic and status come from the stack-machine backend so a
// program's abort output does not depend on which x86-64 emitter built it.
func emitHeapGuard(w func(string, ...any)) {
	w("")
	w("%s:", heapGuardSym)
	w("\tpush rax")
	w("\tpush rcx")
	w("\tpushfq")
	w("\tmov rax, [rip + %s]", heapPtrSym)
	w("\tmov rcx, [rip + %s]", heapEndSym)
	w("\tcmp rax, rcx")
	w("\tja %s", heapOOMLabel)
	w("\tpopfq")
	w("\tpop rcx")
	w("\tpop rax")
	w("\tret")
	// Exhausted: write the diagnostic to stderr and exit with the status the
	// native backends use for it (pinned across emitters by
	// internal/e2e/arena_exit_code_test.go).
	w("%s:", heapOOMLabel)
	w("\tmov edi, 2") // stderr
	w("\tlea rsi, [rip + %s]", heapOOMMsg)
	w("\tmov edx, %d", len(nativex86_64.MsgArenaExhausted))
	w("\tmov eax, 1") // write
	w("\tsyscall")
	w("\tmov edi, %d", nativex86_64.ExitArenaExhausted)
	w("\tmov eax, 231") // exit_group
	w("\tsyscall")
	w(".section .rodata")
	w("%s:", heapOOMMsg)
	bytes := make([]string, len(nativex86_64.MsgArenaExhausted))
	for i := 0; i < len(nativex86_64.MsgArenaExhausted); i++ {
		bytes[i] = strconv.Itoa(int(nativex86_64.MsgArenaExhausted[i]))
	}
	w("\t.byte %s", strings.Join(bytes, ", "))
	w(".text")
}

// heapGuardCall is the instruction a bump site emits immediately after
// publishing its new cursor. Every such site must carry it: one that does not
// allocates past the end silently.
const heapGuardCall = "call " + heapGuardSym

// fnTableSym labels the module's function-address dispatch table: a reserved
// null slot, then one `.quad` per function in the module's (sorted) emission
// order — i.e. indexed by the same fn_idx a closure cell carries. A real-asm
// OpCallIndirect resolves its callee by indexing this table with the cell's
// fn_idx.
//
// Function-value indices are therefore 1-based, so 0 is the null function
// reference. The closure cell's drop slot needs one: a target with no
// __closure_drop_ thunk stores 0 there and __drop_arr_closure's `drop != 0`
// guard skips the dispatch (see closureLines).
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
// pointer. MakeClosure additionally allocates a 32-byte
// {fn_idx, env_ptr, drop_idx, env_ptr} cell (fn_idx = the target's module index)
// and returns the cell pointer. The env pointer is held in s0 (free during this
// instruction) across the second allocation; s3 stages the bump cursor and
// capture values. Captures pack at per-type offsets and store widths (i32 at
// 4-byte slots, pointers at 8) so the env matches the CaptureRef load side (see
// captureEnvLayout / the IR's irCaptureSlotSize).
//
// The 4-slot cell is the shape a generic holder expects — the IR's
// __drop_arr_closure walks an array of closures and, for each element, dispatches
// the sub-pair at {+2*ptrW, +3*ptrW} to free the captures. A 2-slot cell made
// that walk read past the cell into the next heap block and call the LAMBDA as
// though it were the element's drop routine (#6144). drop_idx is
// __closure_drop_<target>'s function index, or 0 (the reserved null) when the
// module has no such thunk; the duplicated env_ptr at +24 is what makes
// {drop_idx, env_ptr} itself a dispatchable cell.
func closureLines(in Inst, numAlloc int, fnIndex map[string]int) ([]string, error) {
	scratch := numAlloc + 3 // s3
	envReg := numAlloc      // s0 — unused by the MakeEnv/MakeClosure inst itself
	var out []string
	alloc := func(dst int, bytes int64) {
		// Same rc-headed bump as MemAlloc (see asmInst): rc=1 at base+0, data at
		// base+8, cursor past header+bytes. Keeps env blocks and closure cells
		// droppable through __fern_rc_is_unique / the drop helpers.
		//
		// A zero-byte payload would return data == the bumped cursor, so the next
		// block's rc header lands on this block's first byte. Give every block at
		// least one 8-byte slot of its own.
		if bytes == 0 {
			bytes = 8
		}
		out = append(out,
			fmt.Sprintf("mov %s, [rip + %s]", reg(dst), heapPtrSym),
			fmt.Sprintf("add %s, 7", reg(dst)),
			fmt.Sprintf("and %s, -8", reg(dst)),
			fmt.Sprintf("mov dword ptr %s, 1", memRef(reg(dst), 0)), // rc = 1
			fmt.Sprintf("mov %s, %s", reg(scratch), reg(dst)),
			fmt.Sprintf("add %s, %d", reg(scratch), bytes+8),
			fmt.Sprintf("mov [rip + %s], %s", heapPtrSym, reg(scratch)),
			heapGuardCall,
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
	if len(in.ArgLocs) == 0 {
		// No captures: env_ptr = 0 and drop_idx = 0 — there is no env block to
		// free, so nothing may dispatch the drop sub-pair. Every word is a
		// compile-time constant, so the cell is the module's immortal .rodata one
		// (staticClosureTargets) and materialising the value is its address.
		return append(out, fmt.Sprintf("lea %s, [rip + %s]", reg(in.Dst), staticClosureLabel(idx))), nil
	}
	// drop_idx = __closure_drop_<target>'s index, or 0 when the module has no
	// such thunk (RcFree off, or a target dead-function elimination culled).
	// Read structurally from the emitted function set, never from a flag, so it
	// can never name a symbol this module does not define.
	dropIdx := fnIndex["__closure_drop_"+in.Callee]
	alloc(envReg, envBytes) // env block -> s0
	storeCaps(envReg)
	alloc(in.Dst, 32) // the 4-slot cell -> Dst
	out = append(out,
		fmt.Sprintf("mov %s, %d", reg(scratch), idx),
		fmt.Sprintf("mov %s, %s", memRef(reg(in.Dst), 0), reg(scratch)),
		fmt.Sprintf("mov %s, %s", memRef(reg(in.Dst), 8), reg(envReg)),
		fmt.Sprintf("mov %s, %d", reg(scratch), dropIdx),
		fmt.Sprintf("mov %s, %s", memRef(reg(in.Dst), 16), reg(scratch)),
		fmt.Sprintf("mov %s, %s", memRef(reg(in.Dst), 24), reg(envReg)),
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

// checkNoDanglingCalls reports a `call` or `jmp` to a label the module never
// defines.
//
// referencedRuntimeHelpers walks the call graph and emits the runtime helpers it
// finds, but a callee with no entry in runtimeHelperEmitters is simply skipped —
// a user function is a legitimate skip, and a helper the table has never heard of
// was one too. The call went out with nothing behind it and the failure surfaced
// in the assembler as `undefined label "fn___fern_drop_arr_str"`, which names
// neither the backend nor the fact that this is a coverage gap. 247 of the 317
// example programs fail that way today.
//
// The point is to fail HERE instead, naming the helpers, so a gap in the table
// reads as one. `call rax` and the other register/indirect forms are not label
// references and are skipped; conditional jumps only ever target local labels,
// which are defined, so collecting `jmp` (the tail-call form) is enough.
func checkNoDanglingCalls(asm string) error {
	defined := map[string]bool{}
	var called []string
	seen := map[string]bool{}
	for _, line := range strings.Split(asm, "\n") {
		switch {
		case strings.HasPrefix(line, "\tcall "), strings.HasPrefix(line, "\tjmp "):
			t := strings.TrimSpace(line[strings.IndexByte(line, ' ')+1:])
			if !isLabelRef(t) || seen[t] {
				continue
			}
			seen[t] = true
			called = append(called, t)
		case strings.HasSuffix(line, ":") && len(line) > 0 && line[0] != '\t':
			defined[strings.TrimSuffix(line, ":")] = true
		}
	}
	var missing []string
	for _, t := range called {
		if !defined[t] {
			missing = append(missing, t)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("x86_64ssa: %d call target(s) the module never defines — a runtime helper with no emitter, or a function missing from the module: %s",
		len(missing), strings.Join(missing, ", "))
}

// isLabelRef reports whether a branch operand names a label rather than a
// register or a memory operand (`call rax`, `call [rip + tbl]`).
func isLabelRef(t string) bool {
	if t == "" || gpIndex(t) >= 0 {
		return false
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return true
}

// collectSentinels assigns a .rodata label to each distinct enum-sentinel tag
// (EnumSentinel.Imm) used in the module, in first-seen order — so every
// OpEnumSentinel for a given tag references the same shared static cell, which
// is what makes two `None`s compare equal by address.
func collectSentinels(progs map[string]*Program, names []string) (map[int64]string, []int64) {
	labels := map[int64]string{}
	var order []int64
	for _, name := range names {
		for _, blk := range progs[name].Blocks {
			for _, in := range blk.Insts {
				if in.Op == EnumSentinel {
					if _, ok := labels[in.Imm]; !ok {
						labels[in.Imm] = fmt.Sprintf("sent_%d", len(order))
						order = append(order, in.Imm)
					}
				}
			}
		}
	}
	return labels, order
}

// pokeInline maps each raw-memory intrinsic core/map.fern is written against
// onto the one instruction it is. mnem is the mnemonic (empty for __ptr_width's
// constant), wide selects a 64-bit operand over a 32-bit one, store writes
// ArgLocs[1] through ArgLocs[0] and yields nothing, and off is the displacement
// — negative for a string's length field.
//
// core/map.fern reaches its kv buffer through these rather than through typed
// field access, because the buffer is one untyped allocation whose layout
// depends on the target's pointer width — so a map lookup runs several of them
// per probe. The stack-machine backends and arm64ssa inline them at the call
// site too; this backend used to have no emitter for them at all, so a program
// that used one was refused.
var pokeInline = map[string]struct {
	mnem  string
	wide  bool
	store bool
	off   int64
}{
	"__load_i32":  {mnem: "mov"},
	"__load_u8":   {mnem: "movzx"},
	"__load_i64":  {mnem: "mov", wide: true},
	"__load_ptr":  {mnem: "mov", wide: true},
	"__str_len":   {mnem: "mov", off: -4},
	"__store_i32": {mnem: "mov", store: true},
	"__store_i64": {mnem: "mov", wide: true, store: true},
	"__store_ptr": {mnem: "mov", wide: true, store: true},
	"__ptr_width": {},
}

// inlinePokeLines renders a raw-memory intrinsic as that instruction, or reports
// false when the callee is something else. The cost was never the `call` but the
// caller-saves the allocator plants around it.
//
// Each case reproduces the width the helper would have returned: a 4-byte load
// through a 32-bit register leaves the value zero-extended and the trailing
// maskFix sign-extends it, exactly as the call sequence did with the result.
func inlinePokeLines(in Inst, numAlloc int) ([]string, bool) {
	form, ok := pokeInline[in.Callee]
	if !ok || in.Op != Call {
		return nil, false
	}
	s0, s1 := numAlloc, numAlloc+1
	var out []string
	materialise := func(l Loc, tmp int) int {
		if l.IsReg {
			return l.Reg
		}
		out = append(out, fmt.Sprintf("mov %s, %s", reg(tmp), slotMem(l.Slot)))
		return tmp
	}
	operand := func(r int) string {
		if form.wide {
			return reg(r)
		}
		return reg32n(r)
	}
	switch {
	case form.mnem == "": // __ptr_width(): a constant, no operands
		if len(in.ArgLocs) != 0 {
			return nil, false
		}
		out = append(out, fmt.Sprintf("mov %s, 8", reg32n(in.Dst)))
	case form.store:
		if len(in.ArgLocs) != 2 {
			return nil, false
		}
		addr := materialise(in.ArgLocs[0], s0)
		val := materialise(in.ArgLocs[1], s1)
		// Void, so there is no result to place and no width to fix.
		return append(out, fmt.Sprintf("mov %s, %s", memRef(reg(addr), form.off), operand(val))), true
	case form.mnem == "movzx":
		if len(in.ArgLocs) != 1 {
			return nil, false
		}
		addr := materialise(in.ArgLocs[0], s0)
		out = append(out, fmt.Sprintf("movzx %s, byte ptr %s", reg32n(in.Dst), memRef(reg(addr), form.off)))
	default:
		if len(in.ArgLocs) != 1 {
			return nil, false
		}
		addr := materialise(in.ArgLocs[0], s0)
		out = append(out, fmt.Sprintf("mov %s, %s", operand(in.Dst), memRef(reg(addr), form.off)))
	}
	if fix := maskFix(in.Dst, in.W); fix != "" {
		out = append(out, strings.TrimPrefix(fix, "\n\t"))
	}
	return out, true
}

// staticClosureTargets returns, in module order, every function a CAPTURE-FREE
// MakeClosure names. Such a cell holds {fn_idx, env=0, drop_idx=0, 0} — all four
// words known at compile time and never written again — so one immortal .rodata
// cell per target stands in for every evaluation of the value, where allocating
// it cost a bump sequence and a heap-guard call each time.
func staticClosureTargets(progs map[string]*Program, names []string) []string {
	seen := map[string]bool{}
	var order []string
	for _, name := range names {
		for _, blk := range progs[name].Blocks {
			for _, in := range blk.Insts {
				if in.Op != MakeClosure || len(in.ArgLocs) > 0 || seen[in.Callee] {
					continue
				}
				seen[in.Callee] = true
				order = append(order, in.Callee)
			}
		}
	}
	return order
}

// staticClosureLabel names the .rodata cell for the target at dispatch-table
// index idx. Keyed on the index rather than the name so it needs no table of
// its own: closureLines already resolves the callee to its index.
func staticClosureLabel(idx int) string { return fmt.Sprintf("clo_%d", idx) }

// usesHeap reports whether any emitted program contains a heap op (so the heap
// section + cursor init are only emitted when needed).
func usesHeap(progs map[string]*Program) bool {
	for _, p := range progs {
		for _, blk := range p.Blocks {
			for _, in := range blk.Insts {
				switch in.Op {
				case MemAlloc, MemLoad, MemStore, MakeEnv:
					return true
				case MakeClosure:
					// A capture-free cell is static .rodata
					// (staticClosureTargets), so it needs no arena.
					if len(in.ArgLocs) > 0 {
						return true
					}
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
	"__fern_rc_is_unique":           emitRcIsUniqueHelper,
	"__fern_rc_inc":                 emitRcIncHelper,
	"__fern_rc_dec":                 emitRcDecHelper,
	"__fern_closure_drop":           emitClosureDropHelper,
	"__fern_box_free":               emitBoxFreeHelper,
	"__str_len":                     emitStrLenHelper,
	"__fern_arr_dec":                emitArrDecHelper,
	"__arr_idx":                     emitArrIdxHelperN("__arr_idx", 2),
	"__arr_idx_nc":                  emitArrIdxHelperNChecked("__arr_idx_nc", 2, false),
	"__arr_idx_1":                   emitArrIdxHelperN("__arr_idx_1", 0),
	"__arr_idx_1_nc":                emitArrIdxHelperNChecked("__arr_idx_1_nc", 0, false),
	"__arr_idx_8":                   emitArrIdxHelperN("__arr_idx_8", 3),
	"__arr_idx_8_nc":                emitArrIdxHelperNChecked("__arr_idx_8_nc", 3, false),
	"__arr_idx_16":                  emitArrIdxHelperN("__arr_idx_16", 4),
	"__arr_idx_16_nc":               emitArrIdxHelperNChecked("__arr_idx_16_nc", 4, false),
	"__str_idx":                     emitArrIdxHelperN("__str_idx", 0),
	"__fern_memchr":                 emitMemchrHelper,
	"__fern_rmemchr":                emitRmemchrHelper,
	"__fern_ascii_run":              emitAsciiRunHelper,
	"__fern_count_byte":             emitCountByteHelper,
	"__alloc_u8":                    emitAllocU8Helper,
	"string_from_bytes_unchecked":   emitStringFromBytesHelper,
	"__str_slice":                   emitStrSliceHelper,
	"__fern_arr_push_grow":          emitArrPushGrowHelper,
	"__fern_arr_push_grow_ptr":      emitAliasHelper("__fern_arr_push_grow_ptr", "__fern_arr_push_grow"),
	"__fern_arr_push_grow_str":      emitAliasHelper("__fern_arr_push_grow_str", "__fern_arr_push_grow"),
	"__fern_arr_push_grow_move_ptr": emitAliasHelper("__fern_arr_push_grow_move_ptr", "__fern_arr_push_grow"),
	"__fern_arr_push_grow_move_str": emitAliasHelper("__fern_arr_push_grow_move_str", "__fern_arr_push_grow"),
	"__fern_arr_cow_inplace":        emitArrCowInplaceHelper,
	"__fern_arr_cow_inplace_ptr":    emitArrCowInplaceElemHelper("__fern_arr_cow_inplace_ptr", "__fern_rc_inc", "cowp"),
	"__fern_arr_cow_inplace_str":    emitAliasHelper("__fern_arr_cow_inplace_str", "__fern_arr_cow_inplace"),
	"__str_eq":                      emitStrEqHelper,
	"__str_ord":                     emitStrOrdHelper,
	"__str_concat":                  emitStrConcatHelper,
	"__fern_str_dec":                emitStrDecHelper,
	"__fern_drop_arr_str":           emitDropArrStrHelper,
	"__alloc_reuse":                 emitAllocReuseHelper,
	"print":                         emitPrintHelper("print", 1),
	"remove_dir_all":                emitRemoveDirAllHelper,
	"__fern_io_error":               emitIoErrorHelper,
	"eprint":                        emitPrintHelper("eprint", 2),
}

// heapUsingHelpers are runtime helpers that allocate on the SSA bump heap, so
// the .bss heap section + cursor must be emitted whenever one is referenced even
// if the program body has no direct heap op.
var heapUsingHelpers = map[string]bool{
	"__str_concat":                true,
	"__alloc_u8":                  true,
	"string_from_bytes_unchecked": true, "__str_slice": true,
	"__fern_arr_push_grow":   true,
	"__fern_arr_cow_inplace": true,
	"__alloc_reuse":          true,
	"remove_dir_all":         true,
	"__fern_io_error":        true,
}

// runtimeHelperDeps records the helper→helper call edges (a helper that tail-
// calls another must have that callee emitted too — the module never references
// it directly). Transitively closed by referencedRuntimeHelpers.
var runtimeHelperDeps = map[string][]string{
	"__fern_closure_drop":           {"__fern_box_free", "__fern_rc_dec"},
	"__fern_arr_push_grow_ptr":      {"__fern_arr_push_grow"},
	"__fern_arr_push_grow_str":      {"__fern_arr_push_grow"},
	"__fern_arr_push_grow_move_ptr": {"__fern_arr_push_grow"},
	"__fern_arr_push_grow_move_str": {"__fern_arr_push_grow"},
	"__fern_arr_cow_inplace_ptr":    {"__fern_arr_cow_inplace", "__fern_rc_inc"},
	"__fern_arr_cow_inplace_str":    {"__fern_arr_cow_inplace"},
	"__fern_drop_arr_str":           {"__fern_str_dec", "__fern_arr_dec"},
	"remove_dir_all":                {"__fern_io_error"},
}

// emitRuntimeHelpers writes the named helper bodies, each at a 16-byte
// boundary. The alignment is the point: these are the smallest and hottest
// routines in an rc-carrying program — `__fern_rc_inc` / `_dec` run once per
// rc op — so a helper's entry address decides whether its body shares one
// 32-byte instruction-fetch window. Letting that fall out of wherever the
// preceding helper happened to end is a landmine: removing two subsumed
// instructions from an EARLIER helper doubled examples/bench/string_rfind_byte,
// 61 ms to 122 ms, without changing one instruction that program runs (#8193).
func emitRuntimeHelpers(w func(string, ...any), helpers []string) {
	for _, h := range helpers {
		// Column 0, like every other directive the backends emit: the
		// instruction counters (scripts/perf-bench, the SSA CLI gate) count
		// indented lines, so an indented directive would read as an
		// instruction the program does not execute.
		w(".p2align 4")
		runtimeHelperEmitters[h](w)
	}
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

// rcPassThroughRet writes the shared exit of a pass-through runtime helper. The
// rc/drop family hands back the pointer it was given — ir.OpRcInc and ir.OpRcDec
// are documented `(ptr) -> ptr`, and the drop calls carry ir.ResAddr — so a
// caller reads the result out of rax and uses it. These bodies use eax as the
// scratch for the rc word, which leaves the header value there, so rax has to be
// restored from rdi before returning. The arm64 sibling needs no equivalent: the
// argument and the result share x0, so leaving it untouched already satisfies
// the contract.
func rcPassThroughRet(w func(string, ...any)) {
	w("\tmov rax, rdi")
	w("\tret")
}

// emitRcIncHelper writes __fern_rc_inc(data): bump the reference count at
// [data-8] by one, guarded like __fern_rc_is_unique (null / low-address /
// static-sentinel) so it is safe on a slot that might hold a non-pointer or a
// static cell. Returns the pointer it was given, leaf. On the SSA path every
// rc-managed value is a heap
// pointer (function values are heap cells, not code addresses), so the
// 0x10000 low-address guard is sufficient — a code/rodata address never flows
// in. Mirrors the native __fern_rc_inc (minus the SSO string tag, which the SSA
// path has no equivalent of).
func emitRcIncHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rc_inc"))
	w("\tcmp rdi, 0x10000")
	w("\tjb .Lssa_rcinc_ret")
	w("\tmov eax, %s", memRef("rdi", -8))
	w("\ttest eax, eax")
	w("\tjs .Lssa_rcinc_ret") // static sentinel
	w("\tadd eax, 1")
	w("\tmov %s, eax", memRef("rdi", -8))
	w(".Lssa_rcinc_ret:")
	rcPassThroughRet(w)
}

// emitRcDecHelper writes __fern_rc_dec(data): drop the reference count at
// [data-8] by one, same guard chain as __fern_rc_inc. It does NOT free at rc==0:
// the free-and-reclaim decision belongs to __fern_closure_drop / the per-type
// drop thunks. Returns the pointer it was given, leaf.
func emitRcDecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rc_dec"))
	w("\tcmp rdi, 0x10000")
	w("\tjb .Lssa_rcdec_ret")
	w("\tmov eax, %s", memRef("rdi", -8))
	w("\ttest eax, eax")
	w("\tjs .Lssa_rcdec_ret") // static sentinel
	w("\tsub eax, 1")
	w("\tmov %s, eax", memRef("rdi", -8))
	w(".Lssa_rcdec_ret:")
	rcPassThroughRet(w)
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
	rcPassThroughRet(w)
}

// emitBoxFreeHelper writes __fern_box_free(data, size) -> data: release an
// rc-headed heap block. This emitter's heap has no freelist yet (the arm64 SSA
// emitter's does, docs/SSA-RC-RUNTIME.md), so this is a no-op that returns the
// data pointer. A real freelist return is the follow-up that makes the size arg
// live.
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
// (rc == 1) the buffer would be freed, a no-op while this emitter's heap has
// no freelist (the arm64 SSA emitter's does), so we just return; otherwise it
// drops a shared reference. The stride arg is unused until real reclamation
// lands. Leaf.
func emitArrDecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_arr_dec"))
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
	rcPassThroughRet(w)
}

// emitArrIdxHelperN writes an indexing helper `<name>(base, idx) -> elem
// address` for a length-prefixed array of stride 1<<shift.
//
// Checked forms compare idx against the length at [base-4] with a SINGLE
// unsigned compare — a negative idx arrives as a huge unsigned and fails the
// same test — and exit 134 out of range, matching the native array-index trap
// and wasm's `unreachable`. The `_nc` forms are the address compute alone, for
// sites the checker has already proved in range. Returns base + idx*stride; the
// caller's OpLoad reads the element. Leaf.
//
// The local label is keyed by NAME rather than by shift: two helpers can share a
// stride (`__str_idx` and `__arr_idx_1` are both byte-stride), and keying on the
// shift would emit the same label twice in one module.
func emitArrIdxHelperN(name string, shift int) func(w func(string, ...any)) {
	return emitArrIdxHelperNChecked(name, shift, true)
}

func emitArrIdxHelperNChecked(name string, shift int, checked bool) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		if checked {
			ok := fmt.Sprintf(".Lssa_idx_%s_ok", strings.TrimLeft(name, "_"))
			w("\tmov edx, %s", memRef("rdi", -4)) // len
			w("\tcmp esi, edx")
			w("\tjb %s", ok)
			w("\tmov edi, 134")
			w("\tmov eax, 231") // exit_group
			w("\tsyscall")
			w("%s:", ok)
		}
		// lea scales by 1, 2, 4 or 8 only, so stride 16 needs the shift spelled
		// out. rsi is dead after it either way.
		if shift <= 3 {
			w("\tlea rax, [rdi + rsi*%d]", 1<<shift)
		} else {
			w("\tshl rsi, %d", shift)
			w("\tlea rax, [rdi + rsi]")
		}
		w("\tret")
	}
}

// emitArrIdxNCHelper is emitArrIdxHelper minus the bounds check — the
// elided (`_nc`) variant used when the caller proved the index in range
// (ForEach desugar, #4380 lever 3). Just base + idx*4.

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

// emitStrOrdHelper writes __str_ord(a, b) -> i32: the three-way byte compare
// behind `<` / `<=` / `>` / `>=` on strings — the first differing byte's
// difference, or the length difference when one is a prefix of the other.
// Unlike __str_eq it cannot bail on a length mismatch: ordering is decided by
// the FIRST difference. Lengths live at [ptr-4]. Leaf.
func emitStrOrdHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_ord"))
	w("\tmov ecx, %s", memRef("rdi", -4)) // la
	w("\tmov edx, %s", memRef("rsi", -4)) // lb
	w("\tmov r8d, ecx")                   // n = min(la, lb)
	w("\tcmp edx, r8d")
	w("\tjae .Lssa_strord_n")
	w("\tmov r8d, edx")
	w(".Lssa_strord_n:")
	w("\txor r9d, r9d") // i = 0
	w(".Lssa_strord_loop:")
	w("\tcmp r9d, r8d")
	w("\tjae .Lssa_strord_len")
	w("\tmovzx r10d, byte ptr [rdi + r9]")
	w("\tmovzx r11d, byte ptr [rsi + r9]")
	w("\tcmp r10d, r11d")
	w("\tjne .Lssa_strord_diff")
	w("\tadd r9, 1")
	w("\tjmp .Lssa_strord_loop")
	w(".Lssa_strord_diff:")
	w("\tmov eax, r10d")
	w("\tsub eax, r11d")
	w("\tmovsx rax, eax")
	w("\tret")
	w(".Lssa_strord_len:")
	w("\tmov eax, ecx")
	w("\tsub eax, edx")
	w("\tmovsx rax, eax")
	w("\tret")
}

// bcopySym names the shared forward byte copy every allocating helper routes
// through. It is internal — the IR never calls it — so it carries a bare symbol
// rather than an fnLabel, the way __ssa_heap_guard does.
const bcopySym = "__ssa_bcopy"

// emitBcopy writes __ssa_bcopy(rdi=dst, rsi=src, rdx=n): a forward copy of n
// bytes. Regions must not overlap.
//
// One instruction does the work. `rep movsb` is the fast path on the declared
// Haswell-2013 baseline (ERMSB), which is why this backend needs no size-classed
// SSE2 copy like the native __fern_memcpy — and why arm64ssa's sibling, which
// has no such instruction, spends fifteen lines on a 16/8/1 ladder.
//
// It clobbers only rdi, rsi and rcx, all caller-saved and all already dead at
// every call site (each passes its own arguments in them). `cld` is one byte of
// insurance: System V guarantees DF is clear at every call boundary and nothing
// in this backend sets it, but a copy running backwards would corrupt the heap
// silently rather than fault.
func emitBcopy(w func(string, ...any)) {
	w("")
	w("%s:", bcopySym)
	w("\tcld")
	w("\tmov rcx, rdx")
	w("\trep movsb")
	w("\tret")
}

// emitBcopyCall writes a copy of n bytes from src to dst through __ssa_bcopy.
// The three names are the registers holding the arguments; they are moved into
// rdi/rsi/rdx in an order that survives any overlap between them — rdx first,
// then rsi, then rdi, so a value already sitting in a destination register is
// read before it is overwritten.
func emitBcopyCall(w func(string, ...any), dst, src, n string) {
	for _, mv := range [][2]string{{"rdx", n}, {"rsi", src}, {"rdi", dst}} {
		if mv[0] != mv[1] {
			w("\tmov %s, %s", mv[0], mv[1])
		}
	}
	w("\tcall %s", bcopySym)
}

// bcopyUsingHelpers are the helpers that call __ssa_bcopy, so the shared routine
// is emitted whenever one of them is. It is not in runtimeHelperEmitters (the IR
// cannot name it), so this gate is what puts it in the module.
var bcopyUsingHelpers = map[string]bool{
	"string_from_bytes_unchecked": true,
	"__str_slice":                 true,
	"__fern_arr_push_grow":        true,
	"__fern_arr_cow_inplace":      true,
}

// usesBcopy reports whether any referenced helper calls __ssa_bcopy.
func usesBcopy(helpers []string) bool {
	for _, h := range helpers {
		if bcopyUsingHelpers[h] {
			return true
		}
	}
	return false
}

// emitAllocU8Helper writes __alloc_u8(n) -> data: a fresh length-prefixed u8[]
// of n bytes, returning the data pointer past the 16-byte header (cap@-12,
// rc=1@-8, len@-4).
//
// The n data bytes are ZERO-FILLED. The interpreter hands back a zeroed u8[],
// so a read-before-write caller — SHA padding is the one that found this, #2768
// — depends on it, and the bump cursor walks memory that a previous allocation
// may have written. n==0 runs the fill zero times and yields a valid header-only
// buffer whose len reads 0.
//
// Unlike the native helper, which calls __fern_alloc, this inlines the raw bump
// so it needs no frame of its own. rdi=n, returns rax=data.
func emitAllocU8Helper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__alloc_u8"))
	w("\tmov esi, edi") // n, preserved across the bump
	w("\tmov edx, esi")
	w("\tadd edx, 16") // allocSize = n + header (a 32-bit write zero-extends)
	w("\tmov r8, [rip + %s]", heapPtrSym)
	w("\tadd r8, 7")
	w("\tand r8, -8") // base, 8-aligned
	w("\tmov r9, r8")
	w("\tadd r9, rdx")
	w("\tmov [rip + %s], r9", heapPtrSym)
	w("\t%s", heapGuardCall)
	w("\tlea rax, [r8 + 16]")            // data
	w("\tmov dword ptr [rax - 12], esi") // cap = n
	w("\tmov dword ptr [rax - 8], 1")    // rc = 1
	w("\tmov dword ptr [rax - 4], esi")  // len = n
	// Zero the payload. rep stosb writes through rdi and consumes rcx, so the
	// return value is parked in r10 for the duration.
	w("\tmov r10, rax")
	w("\tmov rdi, rax")
	w("\tmov ecx, esi")
	w("\txor eax, eax")
	w("\tcld")
	w("\trep stosb")
	w("\tmov rax, r10")
	w("\tret")
}

// emitStringFromBytesHelper writes string_from_bytes_unchecked(bs) -> data: copy
// a u8[] payload into a fresh string and return its data pointer — the
// round-trip companion to s.bytes().
//
// Strings here are single-word and rc-headered (rc=1@base+0, len@base+4,
// data@base+8, the layout ConstStr and __str_concat already use) with no
// small-string inline form, so this is a bump allocation and a copy. The native
// twin spends most of its body deciding whether the result fits in seven inline
// bytes and packing it if so; none of that survives the representation change.
// rdi=bs (the u8[] data pointer, its length at [bs-4]), returns rax=data.
func emitStringFromBytesHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("string_from_bytes_unchecked"))
	w("\tmov esi, %s", memRef("rdi", -4)) // len (a 32-bit write zero-extends)
	w("\tmov r8, [rip + %s]", heapPtrSym)
	w("\tadd r8, 7")
	w("\tand r8, -8")            // base, 8-aligned
	w("\tmov dword ptr [r8], 1") // rc = 1
	w("\tmov [r8 + 4], esi")     // len
	w("\tlea r9, [r8 + 8]")
	w("\tadd r9, rsi")
	w("\tmov [rip + %s], r9", heapPtrSym)
	w("\t%s", heapGuardCall)
	w("\tlea r10, [r8 + 8]") // data
	emitBcopyCall(w, "r10", "rdi", "rsi")
	w("\tmov rax, r10")
	w("\tret")
}

// emitAliasHelper writes `<name>:` as a jump to another helper. The bump heap
// never reclaims, so the _ptr / _str / _move_ptr / _move_str variants, which
// natively differ only by an element-retain or element-release walk, have
// nothing to do that the plain helper does not. That holds only while nothing
// is freed: a raw copy leaves the grown buffer sharing its element references
// with the old one under a single count, so arm64ssa, whose heap reclaims,
// gives each spelling its own body.
func emitAliasHelper(name, target string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tjmp %s", fnLabel(target))
	}
}

// emitStrSliceHelper writes __str_slice(base, low, high) -> data: a fresh string
// holding base[low:high]. Traps (exit 134) on low < 0, high > src_len, or
// low > high, matching the native helper.
//
// low and high arrive as i32 and are SIGN-EXTENDED before the bounds compares.
// Skipping that is #5294: a negative low arrives zero-extended in the low half,
// so a 64-bit compare reads it as a large positive and the trap never fires.
// rdi=base, esi=low, edx=high; returns rax=data.
func emitStrSliceHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_slice"))
	w("\tmovsxd rsi, esi")
	w("\tmovsxd rdx, edx")
	w("\tmov r8d, %s", memRef("rdi", -4)) // src_len (non-negative; zero-extends)
	w("\ttest rsi, rsi")
	w("\tjs .Lssa_strslice_trap")
	w("\tcmp rdx, r8")
	w("\tjg .Lssa_strslice_trap")
	w("\tcmp rsi, rdx")
	w("\tjg .Lssa_strslice_trap")
	w("\tmov r9d, edx")
	w("\tsub r9d, esi") // new_len = high - low
	// Bump-allocate new_len+8: rc=1@base, len@base+4, data@base+8.
	w("\tmov r10, [rip + %s]", heapPtrSym)
	w("\tadd r10, 7")
	w("\tand r10, -8")
	w("\tmov dword ptr [r10], 1") // rc = 1
	w("\tmov [r10 + 4], r9d")     // len
	w("\tlea r11, [r10 + 8]")     // data
	w("\tmov rax, r11")
	w("\tadd rax, r9")
	w("\tmov [rip + %s], rax", heapPtrSym)
	w("\t%s", heapGuardCall)
	w("\tlea rax, [rdi + rsi]") // src = base + low
	emitBcopyCall(w, "r11", "rax", "r9")
	w("\tmov rax, r11")
	w("\tret")
	w(".Lssa_strslice_trap:")
	w("\tmov edi, 134")
	w("\tmov eax, 231") // exit_group
	w("\tsyscall")
}

// emitArrPushGrowHelper writes __fern_arr_push_grow(arr, oldLen, stride) ->
// new_data, the array-append growth helper.
//
// Fast path, and the one that matters: an array uniquely held (rc == 1) with
// spare capacity bumps its rc to 2 and its length in place and returns the same
// pointer. Otherwise a fresh buffer of newCap = max(2*newLen, 4) elements, past
// a headerBytes = max(16, stride) prefix, with the old elements copied over.
//
// The old buffer LEAKS: this emitter's heap has no freelist.
// rdi=arr, esi=oldLen, edx=stride; returns rax=new_data.
func emitArrPushGrowHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_arr_push_grow"))
	w("\tmov eax, %s", memRef("rdi", -8)) // rc
	w("\tcmp eax, 1")
	w("\tjne .Lssa_apg_copy")
	w("\tmov ecx, %s", memRef("rdi", -12)) // cap
	w("\tcmp esi, ecx")
	w("\tjge .Lssa_apg_copy")
	w("\tmov dword ptr [rdi - 8], 2") // rc = 2
	w("\tlea eax, [rsi + 1]")
	w("\tmov [rdi - 4], eax") // len = oldLen + 1
	w("\tmov rax, rdi")
	w("\tret")
	w(".Lssa_apg_copy:")
	w("\tmov r8d, esi")
	w("\tadd r8d, 1") // newLen
	w("\tmov r9d, r8d")
	w("\tshl r9d, 1")
	w("\tcmp r9d, 4")
	w("\tjge .Lssa_apg_cap_ok")
	w("\tmov r9d, 4") // newCap = max(2*newLen, 4)
	w(".Lssa_apg_cap_ok:")
	w("\tmov r10d, 16")
	w("\tcmp edx, 16")
	w("\tjle .Lssa_apg_hdr_ok")
	w("\tmov r10d, edx") // headerBytes = max(stride, 16)
	w(".Lssa_apg_hdr_ok:")
	w("\tmov r11d, r9d")
	w("\timul r11d, edx")
	w("\tadd r11d, r10d") // allocSize = headerBytes + newCap*stride
	w("\tmov rax, [rip + %s]", heapPtrSym)
	w("\tadd rax, 7")
	w("\tand rax, -8") // base (8-aligned)
	w("\tmov rcx, rax")
	w("\tadd rcx, r11")
	w("\tmov [rip + %s], rcx", heapPtrSym)
	w("\t%s", heapGuardCall) // preserves rax and rcx, so base survives
	w("\tmov r11, rax")
	w("\tadd r11, r10")               // new_data = base + headerBytes
	w("\tmov [r11 - 12], r9d")        // cap = newCap
	w("\tmov dword ptr [r11 - 8], 1") // rc = 1
	w("\tmov [r11 - 4], r8d")         // len = newLen
	w("\tmov eax, esi")
	w("\timul eax, edx") // nbytes = oldLen*stride (32-bit, zero-extends)
	emitBcopyCall(w, "r11", "rdi", "rax")
	w("\tmov rax, r11")
	w("\tret")
}

// emitArrCowInplaceHelper writes __fern_arr_cow_inplace(arr, stride) -> buf, the
// copy-on-write helper behind `arr[i] = v`.
//
// rc == 1 means uniquely held: return the array unchanged and let the caller
// store into it. Shared means copy — and the copy TAKES the caller's reference,
// so arr's rc drops by one on the way out, skipping a static sentinel whose rc
// word has the high bit set (writing one would fault on .rodata).
//
// rdi=arr, esi=stride; returns rax=buf. Note the fast path needs an explicit
// `mov rax, rdi`: arm64's sibling gets it free because x0 is both the argument
// and the result, and five helpers returned the wrong register for want of that
// on this backend (#8044).
func emitArrCowInplaceHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_arr_cow_inplace"))
	w("\tmov eax, %s", memRef("rdi", -8)) // rc
	w("\tcmp eax, 1")
	w("\tjne .Lssa_cow_slow")
	w("\tmov rax, rdi")
	w("\tret")
	w(".Lssa_cow_slow:")
	w("\tmov r8d, %s", memRef("rdi", -4))  // len
	w("\tmov r9d, %s", memRef("rdi", -12)) // cap
	w("\tmov eax, %s", memRef("rdi", -8))
	w("\ttest eax, eax")
	w("\tjs .Lssa_cow_skipdec") // high bit = static sentinel
	w("\tsub eax, 1")
	w("\tmov [rdi - 8], eax")
	w(".Lssa_cow_skipdec:")
	w("\tmov r10d, 16")
	w("\tcmp esi, 16")
	w("\tjle .Lssa_cow_hdr_ok")
	w("\tmov r10d, esi") // headerBytes = max(stride, 16)
	w(".Lssa_cow_hdr_ok:")
	w("\tmov r11d, r9d")
	w("\timul r11d, esi")
	w("\tadd r11d, r10d") // allocSize = headerBytes + cap*stride
	w("\tmov rax, [rip + %s]", heapPtrSym)
	w("\tadd rax, 7")
	w("\tand rax, -8")
	w("\tmov rcx, rax")
	w("\tadd rcx, r11")
	w("\tmov [rip + %s], rcx", heapPtrSym)
	w("\t%s", heapGuardCall)
	w("\tmov r11, rax")
	w("\tadd r11, r10")               // new_data = base + headerBytes
	w("\tmov [r11 - 12], r9d")        // cap
	w("\tmov dword ptr [r11 - 8], 1") // rc = 1
	w("\tmov [r11 - 4], r8d")         // len
	w("\tmov eax, r8d")
	w("\timul eax, esi") // nbytes = len*stride
	emitBcopyCall(w, "r11", "rdi", "rax")
	w("\tmov rax, r11")
	w("\tret")
}

// emitArrCowInplaceElemHelper writes the element-retaining
// __fern_arr_cow_inplace_ptr(arr, stride) -> buf: the scalar helper's fast
// path and copy, then `elemInc` on every element the fresh buffer now shares
// with the receiver, so each array owns its own reference. A raw copy leaves
// the elements at unchanged count, and a consuming match one level down then
// reads a child both arrays reach as unique and rewrites it in place — the
// snapshot of a persistent vector changing under a `.with`. rdi=arr,
// esi=stride; returns rax=buf.
func emitArrCowInplaceElemHelper(name, elemInc, tag string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		lbl := func(suffix string) string { return ".Lssa_" + tag + "_" + suffix }
		w("")
		w("%s:", fnLabel(name))
		w("\tmov eax, %s", memRef("rdi", -8)) // rc
		w("\tcmp eax, 1")
		w("\tjne %s", lbl("slow"))
		w("\tmov rax, rdi")
		w("\tret")
		w("%s:", lbl("slow"))
		// Four callee-saved pushes plus the return address leave rsp 8 mod
		// 16; the extra 8 realigns it for the calls below.
		w("\tpush rbx")
		w("\tpush r12")
		w("\tpush r13")
		w("\tpush r14")
		w("\tsub rsp, 8")
		w("\tmov r12d, esi") // stride
		w("\tcall %s", fnLabel("__fern_arr_cow_inplace"))
		w("\tmov rbx, rax")                    // buf
		w("\tmov r13d, %s", memRef("rbx", -4)) // len
		w("\txor r14d, r14d")                  // i
		w("%s:", lbl("loop"))
		w("\tcmp r14d, r13d")
		w("\tjge %s", lbl("done"))
		w("\tmov eax, r14d")
		w("\timul eax, r12d")       // i*stride
		w("\tmov rdi, [rbx + rax]") // element
		w("\tcall %s", fnLabel(elemInc))
		w("\tadd r14d, 1")
		w("\tjmp %s", lbl("loop"))
		w("%s:", lbl("done"))
		w("\tmov rax, rbx")
		w("\tadd rsp, 8")
		w("\tpop r14")
		w("\tpop r13")
		w("\tpop r12")
		w("\tpop rbx")
		w("\tret")
	}
}

// The four single-byte scan kernels the std/string routines lower to. All take a
// string as ONE word — the data pointer, with the byte length at [ptr-4] — so
// there is no unboxing step; the native x86-64 twins spend a frame pulling a
// two-word SSO string apart before they can start.
//
// Three of the four are SSE2, 16 bytes an iteration, the same block algorithms
// the native backend and arm64ssa run (docs/ATLAS-PLATFORM-PLAN.md §3). Only
// __fern_ascii_run is still the scalar byte-an-iteration version these all
// started as. What paid for the vectorising was a net rather than a decision to
// go faster: the flat-vs-ssa ratio gate (#8069) named memchr as a 20x
// divergence the moment the flat side got quicker, and the length sweep in
// gas_scan_lengths_test.go is what makes a block kernel readable off the page —
// it walks every length across two blocks with the needle at every position.
// Vectorising ascii_run wants the same sweep first.
//
// Two conventions are shared and worth stating once. A byte operand outside
// 0..255 can never occur in the haystack, and ONE unsigned compare covers both
// ends because a negative arrives as a huge unsigned — checked before the loop
// so no iteration pays for it. And `from` CLAMPS rather than trapping, matching
// the interpreter: a forward scan clamps it up to 0, a backward scan clamps it
// down to len-1.

// emitMemchrHelper writes __fern_memchr(s, byte, from) -> the index of the first
// `byte` at or after `from`, or -1. Leaf.
//
// SSE2, 16 bytes an iteration, mirroring the shipping backend's kernel
// (x86_64.emitMemchrRuntime). The scalar loop this replaces was five
// instructions per byte, which read as a 20x flat-vs-ssa divergence on
// examples/bench/string_find_byte and is what the #8069 ratio gate exists to
// name. Indices rather than pointers throughout, so the whole thing fits in
// the registers the scalar version already used plus xmm0/xmm1 — the SSA
// backend shuttles floats through those per instruction and never holds one
// live across a call, so clobbering them is free.
func emitMemchrHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_memchr"))
	w("\tmov r8d, %s", memRef("rdi", -4)) // len
	w("\tcmp rsi, 255")
	w("\tja .Lssa_memchr_miss")
	w("\ttest edx, edx")
	w("\tjns .Lssa_memchr_from_ok")
	w("\txor edx, edx") // clamp `from` up to 0
	w(".Lssa_memchr_from_ok:")
	w("\tcmp edx, r8d")
	w("\tjae .Lssa_memchr_miss")
	// The index is scaled into an address below, so its top half has to be
	// clean; `from` arrives as an i32 and the caller owes nothing about rdx's
	// upper bits. One instruction, once.
	w("\tmov edx, edx")
	// Broadcast the needle across xmm1. movd + punpcklbw + punpcklwd + pshufd
	// is the SSE2 splat; pshufb would be one instruction but is SSSE3, outside
	// the declared baseline.
	w("\tmovd xmm1, esi")
	w("\tpunpcklbw xmm1, xmm1")
	w("\tpunpcklwd xmm1, xmm1")
	w("\tpshufd xmm1, xmm1, 0")
	w(".Lssa_memchr_vec:")
	w("\tmov eax, r8d")
	w("\tsub eax, edx") // bytes left at or after the cursor
	w("\tcmp eax, 16")
	w("\tjl .Lssa_memchr_tail")
	// Unaligned load is deliberate: at least 16 bytes remain, so the read
	// stays inside the string, and an aligning prologue costs more than movdqu
	// does on anything in the baseline.
	w("\tmovdqu xmm0, [rdi + rdx]")
	w("\tpcmpeqb xmm0, xmm1")
	w("\tpmovmskb eax, xmm0")
	w("\ttest eax, eax")
	w("\tjnz .Lssa_memchr_hit")
	w("\tadd edx, 16")
	w("\tjmp .Lssa_memchr_vec")
	w(".Lssa_memchr_hit:")
	// bsf gives the lowest set mask bit — the first match in the block. NOT
	// tzcnt: that is BMI1, and below the baseline its F3 prefix is ignored, so
	// it degrades silently to bsf rather than faulting.
	w("\tbsf eax, eax")
	w("\tadd eax, edx")
	w("\tret")
	// Scalar tail: under 16 bytes left, and the whole algorithm for the short
	// strings that dominate a search family.
	w(".Lssa_memchr_tail:")
	w("\tcmp edx, r8d")
	w("\tjae .Lssa_memchr_miss")
	w("\tmovzx r9d, byte ptr [rdi + rdx]")
	w("\tcmp r9d, esi")
	w("\tje .Lssa_memchr_tail_hit")
	w("\tadd edx, 1")
	w("\tjmp .Lssa_memchr_tail")
	w(".Lssa_memchr_tail_hit:")
	w("\tmov eax, edx")
	w("\tret")
	w(".Lssa_memchr_miss:")
	w("\tmov eax, -1")
	w("\tret")
}

// emitRmemchrHelper writes __fern_rmemchr(s, byte, from) -> the index of the LAST
// `byte` at or before `from`, or -1. emitMemchrHelper walked backwards, with the
// clamp mirrored and bsr for the highest lane instead of bsf for the lowest.
// Leaf.
func emitRmemchrHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rmemchr"))
	w("\tmov r8d, %s", memRef("rdi", -4)) // len
	w("\tcmp rsi, 255")
	w("\tja .Lssa_rmemchr_miss")
	w("\ttest edx, edx")
	w("\tjs .Lssa_rmemchr_miss") // from < 0: nothing at or before it
	w("\tcmp edx, r8d")
	w("\tjb .Lssa_rmemchr_start_ok")
	w("\tmov edx, r8d")
	w("\tsub edx, 1") // clamp `from` down to len-1
	w(".Lssa_rmemchr_start_ok:")
	w("\ttest edx, edx")
	w("\tjs .Lssa_rmemchr_miss") // the empty string clamped to -1
	w("\tmov edx, edx")          // clean top half, as in the forward kernel
	w("\tmovd xmm1, esi")
	w("\tpunpcklbw xmm1, xmm1")
	w("\tpunpcklwd xmm1, xmm1")
	w("\tpshufd xmm1, xmm1, 0")
	// Each iteration covers the 16 bytes ENDING at the cursor, [edx-15, edx].
	w(".Lssa_rmemchr_vec:")
	w("\tcmp edx, 15")
	w("\tjl .Lssa_rmemchr_tail")
	w("\tlea r9d, [rdx - 15]")
	w("\tmovdqu xmm0, [rdi + r9]")
	w("\tpcmpeqb xmm0, xmm1")
	w("\tpmovmskb eax, xmm0")
	w("\ttest eax, eax")
	w("\tjnz .Lssa_rmemchr_hit")
	// The next cursor is one below the block's first byte, so nothing between
	// the blocks is skipped; a negative one falls through the tail to the miss.
	w("\tsub edx, 16")
	w("\tjmp .Lssa_rmemchr_vec")
	w(".Lssa_rmemchr_hit:")
	w("\tbsr eax, eax") // highest set lane — the LAST match in the block
	w("\tadd eax, r9d")
	w("\tret")
	w(".Lssa_rmemchr_tail:")
	w("\ttest edx, edx")
	w("\tjs .Lssa_rmemchr_miss")
	w("\tmovzx r9d, byte ptr [rdi + rdx]")
	w("\tcmp r9d, esi")
	w("\tje .Lssa_rmemchr_tail_hit")
	w("\tsub edx, 1")
	w("\tjmp .Lssa_rmemchr_tail")
	w(".Lssa_rmemchr_tail_hit:")
	w("\tmov eax, edx")
	w("\tret")
	w(".Lssa_rmemchr_miss:")
	w("\tmov eax, -1")
	w("\tret")
}

// emitAsciiRunHelper writes __fern_ascii_run(s, from) -> the index of the first
// byte at or after `from` with its high bit set, or len(s) if the rest is ASCII.
// The length rather than -1 on a miss, matching the intrinsic's branch-free-skip
// contract on the other backends. Leaf.
func emitAsciiRunHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_ascii_run"))
	w("\tmov r8d, %s", memRef("rdi", -4)) // len
	w("\ttest esi, esi")
	w("\tjns .Lssa_ascii_from_ok")
	w("\txor esi, esi") // clamp `from` up to 0
	w(".Lssa_ascii_from_ok:")
	w(".Lssa_ascii_loop:")
	w("\tcmp esi, r8d")
	w("\tjae .Lssa_ascii_none")
	w("\tmovzx r9d, byte ptr [rdi + rsi]")
	w("\ttest r9d, 128")
	w("\tjnz .Lssa_ascii_hit")
	w("\tadd esi, 1")
	w("\tjmp .Lssa_ascii_loop")
	w(".Lssa_ascii_hit:")
	w("\tmov eax, esi")
	w("\tret")
	w(".Lssa_ascii_none:")
	w("\tmov eax, r8d") // no high byte: the answer is len
	w("\tret")
}

// emitCountByteHelper writes __fern_count_byte(s, byte) -> how many bytes of `s`
// equal `byte`. No cursor, so no clamp; both degenerate answers are honest
// counts rather than sentinels — an out-of-range byte counts 0 because nothing
// can equal it, an empty string counts 0 because it has no bytes. Leaf.
//
// The vector body mirrors the flat backend's kernel because the scalar one it
// replaced was 12.6x slower on examples/bench/string_count_byte.fern, which is
// what the differential's ratio gate reports (#8069). Byte-at-a-time is a fine
// helper right up until a program counts 32 KiB twice per round, 6000 rounds.
func emitCountByteHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_count_byte"))
	w("\tmov r8d, %s", memRef("rdi", -4)) // len
	w("\txor eax, eax")                   // running count
	w("\tcmp rsi, 255")
	w("\tja .Lssa_count_ret")
	w("\txor edx, edx")
	// SSE2 splat of the needle across xmm1: pshufb would be one instruction
	// but is SSSE3, outside the declared Haswell baseline.
	w("\tmovd xmm1, esi")
	w("\tpunpcklbw xmm1, xmm1")
	w("\tpunpcklwd xmm1, xmm1")
	w("\tpshufd xmm1, xmm1, 0")
	// 16 bytes an iteration while at least 16 remain, then the scalar loop
	// takes the 0..15-byte tail — and the whole string when it is shorter than
	// one block. The load is unaligned on purpose: the pointer comes from the
	// allocator, so a 16-byte read starting inside the string cannot cross into
	// an unmapped page, and a scalar align-up prologue would cost more.
	w(".Lssa_count_vec:")
	w("\tmov r9d, r8d")
	w("\tsub r9d, edx")
	w("\tcmp r9d, 16")
	w("\tjl .Lssa_count_loop")
	w("\tmovdqu xmm0, [rdi + rdx]")
	w("\tpcmpeqb xmm0, xmm1")
	w("\tpmovmskb r9d, xmm0")
	w("\tpopcnt r9d, r9d")
	w("\tadd eax, r9d")
	w("\tadd edx, 16")
	w("\tjmp .Lssa_count_vec")
	w(".Lssa_count_loop:")
	w("\tcmp edx, r8d")
	w("\tjae .Lssa_count_ret")
	w("\tmovzx r9d, byte ptr [rdi + rdx]")
	w("\tcmp r9d, esi")
	w("\tjne .Lssa_count_next")
	w("\tadd eax, 1")
	w(".Lssa_count_next:")
	w("\tadd edx, 1")
	w("\tjmp .Lssa_count_loop")
	w(".Lssa_count_ret:")
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
	w("\t%s", heapGuardCall)
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
	rcPassThroughRet(w)
}

// emitPrintHelper writes print(s) / eprint(s): the string's bytes to the fd,
// then a newline, as the two write(2) calls the stack-machine backend's
// __fern_puts / __fern_eprint make — same syscall count, same order, so a
// program interleaving stdout and stderr writes the same bytes under either
// backend. The string is one word with its length at [ptr-4]; the value is
// returned unchanged, as every rc-neutral helper here does.
//
// The newline is a byte on this frame rather than a .rodata entry: one word of
// stack costs nothing and keeps the helper self-contained, where a shared
// literal would have to be emitted whether or not any helper referenced it.
//
// Not a short-write loop, deliberately — the stack-machine backend does not
// have one either, and this leg exists to compare the two.
func emitPrintHelper(name string, fd int) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tpush rbp")
		w("\tmov rbp, rsp")
		w("\tpush rbx")
		w("\tsub rsp, 8") // 16-byte aligned; [rsp] holds the newline byte
		w("\tmov rbx, rdi")
		w("\tmov edx, %s", memRef("rdi", -4)) // len
		w("\tmov rsi, rdi")                   // buf
		w("\tmov edi, %d", fd)
		w("\tmov eax, 1") // sysWrite
		w("\tsyscall")
		w("\tmov byte ptr [rsp], 10")
		w("\tmov rsi, rsp")
		w("\tmov edx, 1")
		w("\tmov edi, %d", fd)
		w("\tmov eax, 1")
		w("\tsyscall")
		w("\tmov rax, rbx")
		w("\tadd rsp, 8")
		w("\tpop rbx")
		w("\tpop rbp")
		w("\tret")
	}
}

// emitAllocReuseHelper writes __alloc_reuse(token, tokenSize, size) -> data —
// the drop-reuse (FBIP) primitive. A live token whose 16-byte size class matches
// the request's is handed straight back, so the constructor writes its fields
// into the block the drop just released; anything else allocates fresh.
//
// The class arithmetic is the stack-machine backend's, ((sz+15)&-16), because a
// match has to mean the same thing on both sides of the differential: the
// reused block must be wide enough for the new value.
//
// Where this backend differs is the mismatch path. The native helper FREES the
// dropped block before allocating, which it can do because it has a freelist;
// this heap is a bump cursor with no reclamation (see emitArrPushGrowHelper),
// so the block is simply left behind. A leak, not a miscompile — the same
// trade every allocating helper here already makes.
//
// The fresh path is MemAlloc's sequence: rc=1 at base+0, data at base+8, cursor
// past header+size. __ssa_heap_guard preserves rax, rcx and the flags, so the
// data pointer survives the guard call in rax.
func emitAllocReuseHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__alloc_reuse"))
	w("\ttest rdi, rdi")
	w("\tjz .Lssa_reuse_fresh") // null token: nothing to reuse
	w("\tmov rax, rsi")
	w("\tadd rax, 15")
	w("\tand rax, -16") // class(tokenSize)
	w("\tmov rcx, rdx")
	w("\tadd rcx, 15")
	w("\tand rcx, -16") // class(size)
	w("\tcmp rax, rcx")
	w("\tjne .Lssa_reuse_fresh")
	w("\tmov rax, rdi") // in place: the token IS the block
	w("\tret")
	w(".Lssa_reuse_fresh:")
	w("\tmov rax, [rip + %s]", heapPtrSym)
	w("\tadd rax, 7")
	w("\tand rax, -8")            // base, 8-aligned
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tmov rcx, rax")
	w("\tadd rcx, rdx")
	w("\tadd rcx, 8") // header
	w("\tmov [rip + %s], rcx", heapPtrSym)
	w("\t%s", heapGuardCall)
	w("\tadd rax, 8") // data = base + 8
	w("\tret")
}

// emitDropArrStrHelper writes __fern_drop_arr_str(ptr, stride) -> ptr — the
// scope-exit drop of a string ARRAY, which owns its elements: at rc == 1 the
// array is about to die, so each element's own reference goes first, then the
// array's. A SHARED array (rc != 1) walks nothing — the other owner still reads
// those elements — which is the same test the stack-machine backend makes.
//
// The element release is __fern_str_dec and the array's is __fern_arr_dec, so
// the guards (null, low address, static sentinel, rc underflow) are stated once
// each and this helper is the walk alone.
func emitDropArrStrHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_drop_arr_str"))
	w("\tpush rbp")
	w("\tmov rbp, rsp")
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14") // four pushes past rbp: rsp stays 16-aligned
	w("\tmov rbx, rdi")
	w("\tmov r14, rsi") // stride
	w("\tcmp rbx, 0x10000")
	w("\tjb .Lssa_dropstr_ret")
	w("\tmov eax, %s", memRef("rbx", -8)) // rc
	w("\ttest eax, eax")
	w("\tjs .Lssa_dropstr_ret") // static sentinel
	w("\tcmp eax, 1")
	w("\tjne .Lssa_dropstr_arr")           // shared: the elements are not ours to drop
	w("\tmov r12d, %s", memRef("rbx", -4)) // len
	w("\txor r13, r13")
	w(".Lssa_dropstr_loop:")
	w("\tcmp r13, r12")
	w("\tjge .Lssa_dropstr_arr")
	w("\tmov rax, r13")
	w("\timul rax, r14")
	w("\tmov rdi, [rbx + rax]") // element i, a string pointer
	w("\tcall %s", fnLabel("__fern_str_dec"))
	w("\tinc r13")
	w("\tjmp .Lssa_dropstr_loop")
	w(".Lssa_dropstr_arr:")
	w("\tmov rdi, rbx")
	w("\tmov rsi, r14")
	w("\tcall %s", fnLabel("__fern_arr_dec"))
	w(".Lssa_dropstr_ret:")
	w("\tmov rax, rbx")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tpop rbp")
	w("\tret")
}

// ssaBumpAlloc emits the bump-heap allocation every helper here opens with:
// `dst` = the 16-aligned cursor, the cursor moves past `size` bytes, and the
// guard checks the reservation. `size` is an immediate or a register name.
// __ssa_heap_guard preserves rax, rcx and the flags, so a caller may hold the
// block pointer in either across the call — anything else it must place after.
func ssaBumpAlloc(w func(string, ...any), dst, size string) {
	w("\tmov %s, [rip + %s]", dst, heapPtrSym)
	w("\tadd %s, 15", dst)
	w("\tand %s, -16", dst)
	w("\tmov r11, %s", dst)
	w("\tadd r11, %s", size)
	w("\tmov [rip + %s], r11", heapPtrSym)
	w("\t%s", heapGuardCall)
}

// emitIoErrorHelper writes __fern_io_error(errno, path) -> IoError box: the
// errno's IoError variant, boxed the way a Match reads it. The x86-64 sibling
// of arm64ssa's, and the layouts are the natives': the four path variants —
// NotFound(0) / PermissionDenied(1) / AlreadyExists(2) / InvalidUtf8(3) — are
// {tag@0, path@8}, Interrupted(4) is tag-only, and Other(6) carries
// {tag@0, path@8, msg@16} where msg is glibc's strerror text for that errno.
//
// The text comes from internal/strerror, the one table #8265 pinned across
// every backend and the self-host, as a compare ladder over .rodata literals —
// each with the immortal rc header a user literal carries, so a drop of the
// message short-circuits instead of writing to .rodata. An errno outside the
// table builds "Unknown error N" on the stack and copies it into a fresh rc
// string, digits first from the end of a 32-byte scratch.
//
// rdi = errno (positive), rsi = path string. Leaf.
func emitIoErrorHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_io_error"))
	w("\tcmp edi, 2") // ENOENT
	w("\tje .Lssa_ioe_nf")
	w("\tcmp edi, 13") // EACCES
	w("\tje .Lssa_ioe_pm")
	w("\tcmp edi, 17") // EEXIST
	w("\tje .Lssa_ioe_ex")
	w("\tcmp edi, 4") // EINTR
	w("\tje .Lssa_ioe_intr")
	// EILSEQ is synthetic — read_file's UTF-8 validation dispatches it; no
	// file syscall produces it (#5714).
	w("\tcmp edi, 84") // EILSEQ
	w("\tje .Lssa_ioe_il")
	texts := strerror.Dense(strerror.Linux)
	for n, text := range texts {
		if text == "" {
			continue
		}
		w("\tcmp edi, %d", n)
		w("\tjne .Lssa_ioe_not_%d", n)
		w("\tlea r9, [rip + .Lssa_ioe_str_%d]", n)
		w("\tjmp .Lssa_ioe_other")
		w(".Lssa_ioe_not_%d:", n)
	}
	// "Unknown error N": the digits into a 32-byte stack scratch from its end,
	// the prefix in front of them, then a bump-allocated rc string of exactly
	// the bytes written.
	w("\tsub rsp, 32")
	w("\tlea rcx, [rsp + 32]") // write cursor, moving down
	w("\tmov eax, edi")        // the errno to render
	w(".Lssa_ioe_itoa:")
	w("\txor edx, edx")
	w("\tmov r8d, 10")
	w("\tdiv r8d") // eax = n/10, edx = n%%10
	w("\tadd edx, 48")
	w("\tsub rcx, 1")
	w("\tmov [rcx], dl")
	w("\ttest eax, eax")
	w("\tjnz .Lssa_ioe_itoa")
	w("\tlea rsi, [rip + .Lssa_ioe_unknown_prefix]")
	w("\tadd rsi, %d", len(strerror.UnknownPrefix))
	w("\tmov r8d, %d", len(strerror.UnknownPrefix))
	w(".Lssa_ioe_prefix:")
	w("\tsub rsi, 1")
	w("\tsub rcx, 1")
	w("\tmov al, [rsi]")
	w("\tmov [rcx], al")
	w("\tsub r8d, 1")
	w("\tjnz .Lssa_ioe_prefix")
	w("\tlea rdx, [rsp + 32]")
	w("\tsub rdx, rcx") // rdx = byte length
	// The message string: rc header + bytes + NUL. rdx is live across the
	// bump, so the size goes through r10 and the length is re-read after.
	w("\tmov r10, rdx")
	w("\tadd r10, 9")
	ssaBumpAlloc(w, "rax", "r10")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tmov [rax + 4], edx")     // len
	w("\tlea r9, [rax + 8]")      // r9 = msg data ptr
	w("\tmov r8, r9")
	w(".Lssa_ioe_copy:")
	w("\tmov al, [rcx]")
	w("\tmov [r8], al")
	w("\tadd rcx, 1")
	w("\tadd r8, 1")
	w("\tsub rdx, 1")
	w("\tjnz .Lssa_ioe_copy")
	w("\tmov byte ptr [r8], 0") // NUL
	w("\tadd rsp, 32")
	w(".Lssa_ioe_other:")
	ssaBumpAlloc(w, "rax", "32")  // 8 header + 24 box (tag, path, msg)
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tadd rax, 8")             // box data
	w("\tmov dword ptr [rax], 6") // tag = 6 (Other)
	w("\tmov [rax + 8], rsi")
	w("\tmov [rax + 16], r9")
	w("\tret")
	w(".Lssa_ioe_intr:")
	ssaBumpAlloc(w, "rax", "16") // 8 header + 8 (tag only)
	w("\tmov dword ptr [rax], 1")
	w("\tadd rax, 8")
	w("\tmov dword ptr [rax], 4") // tag = 4 (Interrupted)
	w("\tret")
	w(".Lssa_ioe_nf:")
	w("\tmov r9d, 0")
	w("\tjmp .Lssa_ioe_path")
	w(".Lssa_ioe_pm:")
	w("\tmov r9d, 1")
	w("\tjmp .Lssa_ioe_path")
	w(".Lssa_ioe_ex:")
	w("\tmov r9d, 2")
	w("\tjmp .Lssa_ioe_path")
	w(".Lssa_ioe_il:")
	w("\tmov r9d, 3")
	w(".Lssa_ioe_path:")
	ssaBumpAlloc(w, "rax", "24") // 8 header + 16 (tag, path)
	w("\tmov dword ptr [rax], 1")
	w("\tadd rax, 8")
	w("\tmov [rax], r9d") // tag
	w("\tmov [rax + 8], rsi")
	w("\tret")
	// The strerror literals, each with the immortal rc header a .rodata string
	// literal carries (see the .rodata block in emitProgram).
	w(".section .rodata")
	for n, text := range texts {
		if text == "" {
			continue
		}
		w("\t.4byte 0x80000000")
		w("\t.4byte %d", len(text))
		w(".Lssa_ioe_str_%d:", n)
		w("\t.byte %s", asmByteList(text))
	}
	w(".Lssa_ioe_unknown_prefix:")
	w("\t.byte %s", asmByteList(strerror.UnknownPrefix))
	w(".text")
}

// asmByteList renders a string as a `.byte` operand list.
func asmByteList(s string) string {
	parts := make([]string, len(s))
	for i := 0; i < len(s); i++ {
		parts[i] = strconv.Itoa(int(s[i]))
	}
	return strings.Join(parts, ", ")
}

// emitRemoveDirAllHelper writes remove_dir_all(path) -> Result[void, IoError]:
// a recursive rm -rf. It opens the path O_DIRECTORY; a directory is drained
// (getdents64) and each non-"."/".." child is recursed into (a child that is a
// plain file hits ENOTDIR and is unlinked), then the now-empty directory is
// removed via unlinkat(AT_REMOVEDIR); a plain-file path is unlinked; a missing
// path is a silent success, matching os.RemoveAll. Child errors are
// best-effort, as they are on every other backend.
//
// The path arrives as a single-word string and is copied into a
// NUL-terminated heap buffer once at entry, since every syscall here needs a C
// string. Child paths "pathz/name" are fresh single-word rc strings.
//
// NOTE: each recursion level bump-allocates a 1 KiB getdents buffer this heap
// never reclaims, so a directory whose entries do not fit in 1 KiB per level is
// drained only as far as the buffer — the same bound arm64ssa's helper carries,
// where the stack-machine backends use 64 KiB.
//
// Non-leaf and self-recursive. Callee-saved: rbx=pathz, r12=dir fd, r13=dirent
// buffer, r14=total, r15=offset; the child's name pointer and the two lengths
// live in the frame, since the recursion clobbers every caller-saved register.
func emitRemoveDirAllHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("remove_dir_all"))
	w("\tpush rbp")
	w("\tmov rbp, rsp")
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14")
	w("\tpush r15")
	// Five pushes past rbp leave rsp 8 mod 16; 24 bytes of scratch realign it
	// and give the three slots the recursion has to survive:
	//   [rbp-48] child name ptr   [rbp-56] plen   [rbp-64] nlen
	w("\tsub rsp, 24")
	w("\tmov r8d, %s", memRef("rdi", -4)) // path len
	w("\tmov r9, rdi")                    // path data
	w("\tmov r10, r8")
	w("\tadd r10, 1") // + NUL
	ssaBumpAlloc(w, "rbx", "r10")
	w("\txor ecx, ecx")
	w(".Lssa_rda_cp:")
	w("\tcmp rcx, r8")
	w("\tjae .Lssa_rda_cpd")
	w("\tmov al, [r9 + rcx]")
	w("\tmov [rbx + rcx], al")
	w("\tadd rcx, 1")
	w("\tjmp .Lssa_rda_cp")
	w(".Lssa_rda_cpd:")
	w("\tmov byte ptr [rbx + r8], 0")
	// openat(AT_FDCWD, pathz, O_RDONLY|O_DIRECTORY, 0). O_DIRECTORY is
	// 0x10000 on x86-64, not the generic 0x4000 the arm64 helper uses.
	w("\tmov edi, -100")
	w("\tmov rsi, rbx")
	w("\tmov edx, 0x10000")
	w("\txor r10d, r10d")
	w("\tmov eax, 257") // openat
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjns .Lssa_rda_dir")
	w("\tcmp rax, -2") // -ENOENT: already gone
	w("\tje .Lssa_rda_ok")
	w("\tcmp rax, -20") // -ENOTDIR: a plain file
	w("\tjne .Lssa_rda_err")
	w("\tmov edi, -100")
	w("\tmov rsi, rbx")
	w("\txor edx, edx")
	w("\tmov eax, 263") // unlinkat
	w("\tsyscall")
	w("\tjmp .Lssa_rda_ok")
	w(".Lssa_rda_dir:")
	w("\tmov r12, rax") // dir fd
	ssaBumpAlloc(w, "r13", "1024")
	w("\txor r14, r14") // total
	w(".Lssa_rda_g:")
	w("\tmov edx, 1024")
	w("\tsub rdx, r14")
	w("\tjz .Lssa_rda_gd") // buffer full: stop draining
	w("\tmov edi, r12d")
	w("\tlea rsi, [r13 + r14]")
	w("\tmov eax, 217") // getdents64
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjle .Lssa_rda_gd") // 0 (end) or < 0 (error)
	w("\tadd r14, rax")
	w("\tjmp .Lssa_rda_g")
	w(".Lssa_rda_gd:")
	w("\txor r15, r15") // offset
	w(".Lssa_rda_it:")
	w("\tcmp r15, r14")
	w("\tjae .Lssa_rda_itd")
	w("\tlea rax, [r13 + r15]")
	w("\tlea rsi, [rax + 19]") // d_name
	w("\tmovzx ecx, byte ptr [rsi]")
	w("\tcmp cl, 46") // '.'
	w("\tjne .Lssa_rda_ch")
	w("\tmovzx ecx, byte ptr [rsi + 1]")
	w("\ttest cl, cl")
	w("\tjz .Lssa_rda_adv") // "."
	w("\tcmp cl, 46")
	w("\tjne .Lssa_rda_ch")
	w("\tmovzx ecx, byte ptr [rsi + 2]")
	w("\ttest cl, cl")
	w("\tjz .Lssa_rda_adv") // ".."
	w(".Lssa_rda_ch:")
	w("\tmov [rbp - 48], rsi")
	// plen = strlen(pathz)
	w("\txor rcx, rcx")
	w(".Lssa_rda_pl:")
	w("\tcmp byte ptr [rbx + rcx], 0")
	w("\tje .Lssa_rda_pld")
	w("\tadd rcx, 1")
	w("\tjmp .Lssa_rda_pl")
	w(".Lssa_rda_pld:")
	w("\tmov [rbp - 56], rcx")
	// nlen = strlen(name)
	w("\txor rdx, rdx")
	w(".Lssa_rda_nl:")
	w("\tcmp byte ptr [rsi + rdx], 0")
	w("\tje .Lssa_rda_nld")
	w("\tadd rdx, 1")
	w("\tjmp .Lssa_rda_nl")
	w(".Lssa_rda_nld:")
	w("\tmov [rbp - 64], rdx")
	// The child string "pathz/name": rc header + childlen bytes + NUL.
	w("\tlea r10, [rcx + rdx + 10]") // 8 header + childlen(plen+1+nlen) + NUL
	ssaBumpAlloc(w, "rax", "r10")
	w("\tmov rcx, [rbp - 56]")
	w("\tmov rdx, [rbp - 64]")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tlea r8d, [rcx + rdx + 1]")
	w("\tmov [rax + 4], r8d") // len = childlen
	w("\tlea r8, [rax + 8]")  // child data
	w("\txor r9, r9")
	w(".Lssa_rda_c1:")
	w("\tcmp r9, rcx")
	w("\tjae .Lssa_rda_c1d")
	w("\tmov al, [rbx + r9]")
	w("\tmov [r8 + r9], al")
	w("\tadd r9, 1")
	w("\tjmp .Lssa_rda_c1")
	w(".Lssa_rda_c1d:")
	w("\tmov byte ptr [r8 + rcx], 47") // '/'
	w("\tmov rsi, [rbp - 48]")
	w("\txor r9, r9")
	w(".Lssa_rda_c2:")
	w("\tcmp r9, rdx")
	w("\tjae .Lssa_rda_c2d")
	w("\tmov al, [rsi + r9]")
	w("\tlea r10, [rcx + r9 + 1]")
	w("\tmov [r8 + r10], al")
	w("\tadd r9, 1")
	w("\tjmp .Lssa_rda_c2")
	w(".Lssa_rda_c2d:")
	w("\tlea r10, [rcx + rdx + 1]")
	w("\tmov byte ptr [r8 + r10], 0")
	w("\tmov rdi, r8")
	w("\tcall %s", fnLabel("remove_dir_all"))
	w(".Lssa_rda_adv:")
	w("\tmovzx eax, word ptr [r13 + r15 + 16]") // d_reclen
	w("\tadd r15, rax")
	w("\tjmp .Lssa_rda_it")
	w(".Lssa_rda_itd:")
	w("\tmov edi, r12d")
	w("\tmov eax, 3") // close
	w("\tsyscall")
	w("\tmov edi, -100")
	w("\tmov rsi, rbx")
	w("\tmov edx, 512") // AT_REMOVEDIR
	w("\tmov eax, 263") // unlinkat
	w("\tsyscall")
	w(".Lssa_rda_ok:")
	// Result.Ok(()): the unit rides a payload slot like any other value, so
	// this is the same 24-byte block the Err arm builds, tag 0.
	ssaBumpAlloc(w, "rax", "24")
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tadd rax, 8")
	w("\tmov dword ptr [rax], 0")     // tag = 0 (Ok)
	w("\tmov qword ptr [rax + 8], 0") // unit payload
	w("\tjmp .Lssa_rda_ret")
	w(".Lssa_rda_err:")
	w("\tneg rax")
	w("\tmov r12, rax") // errno — r12 is free, no fd was opened on this path
	// __fern_io_error(errno, "") — a top-level open failure reports the path
	// it was given, and this helper hands it an empty one, as arm64ssa does.
	ssaBumpAlloc(w, "rax", "9")
	w("\tmov dword ptr [rax], 1")     // rc = 1
	w("\tmov dword ptr [rax + 4], 0") // len = 0
	w("\tlea rsi, [rax + 8]")
	w("\tmov byte ptr [rsi], 0")
	w("\tmov edi, r12d")
	w("\tcall %s", fnLabel("__fern_io_error"))
	w("\tmov r12, rax") // IoError box
	ssaBumpAlloc(w, "rax", "24")
	w("\tmov dword ptr [rax], 1")
	w("\tadd rax, 8")
	w("\tmov dword ptr [rax], 1") // tag = 1 (Err)
	w("\tmov [rax + 8], r12")
	w(".Lssa_rda_ret:")
	w("\tadd rsp, 24")
	w("\tpop r15")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tpop rbp")
	w("\tret")
}

// rcxReg/raxReg/rdxReg are the fixed registers the shift/div and call sequences
// pin. Derived from gpRegs so a reordering cannot leave them naming something
// else.
var (
	rcxReg = gpIndex("rcx")
	raxReg = gpIndex("rax")
	rdxReg = gpIndex("rdx")
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
	// EVERY shift at 32-bit width must operate on the 32-bit register, for two
	// independent reasons.
	//
	// Operand: a logical right shift on a u32 with bit 31 set would drag in the
	// sign-extended high bits (1s in bits 32-63), which is the u32 `>>` bug that
	// miscompiled SHA-256.
	//
	// COUNT: x86 masks a shift count to the width of its destination — `shl r32,
	// cl` uses cl & 31, `shl r64, cl` uses cl & 63. So `460 << 124` at i32 width
	// is `460 << 28` = -1073741824, but on the full register it becomes
	// `460 << 60`, whose low 32 bits are 0. "shl's excess bits are masked off
	// by maskFix" and "sar wants the sign-extended operand" are both true of
	// the VALUE, and neither licenses taking the full register for the COUNT.
	//
	// The 32-bit form reads only the low 32 bits; the caller's trailing maskFix
	// re-sign-extends to the storage convention.
	dst := reg(in.Dst)
	if in.W != 64 {
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

// bitCountMnemonic names the x86-64 instruction for a bit-count op. LZCNT and
// TZCNT are BMI1/LZCNT; both are in the Haswell-class baseline this backend
// targets, and both fail SILENTLY on an older CPU (same opcodes as bsr/bsf plus
// an F3 prefix it ignores) rather than faulting — see docs/BACKEND-PARITY.md.
func bitCountMnemonic(k ssa.OpKind) string {
	switch k {
	case ssa.OpClz:
		return "lzcnt"
	case ssa.OpCtz:
		return "tzcnt"
	default:
		return "popcnt"
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
