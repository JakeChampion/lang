package x86_64ssa

import (
	"fmt"
	"github.com/jakechampion/lang/internal/strerror"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	nativex86_64 "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/fernrt"
	"github.com/jakechampion/lang/internal/ir"
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

	// A helper written in Fern (internal/fernrt) is lifted and emitted as a
	// function of this module, under the label its callers already use. Its
	// own calls may reach further helpers, so the scan repeats until nothing
	// new is reached.
	for _, fern := referencedRuntimeHelpers(progs); len(fern) > 0; _, fern = referencedRuntimeHelpers(progs) {
		for _, name := range fern {
			p, err := liftFernHelper(name, numAlloc)
			if err != nil {
				return "", err
			}
			progs[name] = p
			names = append(names, name)
		}
		sort.Strings(names)
	}

	ep := progs[entry]
	if len(entryArgs) != 0 && len(entryArgs) != len(ep.ParamLocs) {
		return "", fmt.Errorf("x86_64ssa: got %d entry args, entry %q has %d params", len(entryArgs), entry, len(ep.ParamLocs))
	}

	var b strings.Builder
	w := lineWriter(&b)

	// Runtime helpers to append to .text (module-referenced + transitive deps).
	// Some of them allocate (e.g. __str_concat bumps the heap cursor), so the
	// heap section must exist whenever one is present, even if no direct heap op
	// (MemAlloc / MakeClosure / …) does.
	helpers, _ := referencedRuntimeHelpers(progs)
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

	withArgs := usesArgs(helpers)
	withEnv := usesEnv(helpers)
	w(".intel_syntax noprefix")
	w(".text")
	w(".globl _start")
	w("_start:")
	emitProcCapture(w, withArgs, withEnv)
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
	if ast.LeakCheckEnabled {
		w("\tpush rax") // park the exit status across the census
		w("\tcall %s", lcReportSym)
		w("\tpop rax")
	}
	w("\tmov edi, eax")     // exit code = return value
	w("\tmov eax, %d", 231) // sysExitGroup
	w("\tsyscall")
	w("")
	for _, name := range names {
		if err := emitFuncBody(w, name, progs[name], numAlloc, strLabels, sentLabels, fnIndex, countsAllocs(helpers)); err != nil {
			return "", err
		}
	}
	emitRuntimeHelpers(w, helpers)
	emitAbortTails(w, b.String())
	// The allocator and its trampoline go in when the text so far reaches
	// them — a compiled OpAlloc, a closure cell, or a helper's allocation — so
	// a module that never allocates carries neither.
	if strings.Contains(b.String(), "call "+allocPresSym) {
		emitAllocPres(w)
		if !slices.Contains(helpers, "__alloc") {
			helpers = append(helpers, "__alloc")
			w(".p2align 4")
			if countsAllocs(helpers) {
				emitAllocHelperCounting(w)
			} else {
				emitAllocHelper(w)
			}
		}
	}
	if usesTranscendentals(helpers) {
		emitTranscendentals(w)
	}
	if usesBcopy(helpers) {
		emitBcopy(w)
	}
	if usesBfill(helpers) {
		emitBfill(w)
	}
	if usesMismatch(helpers) {
		emitMismatch(w)
	}
	if heap {
		emitHeapGuard(w)
	}
	if ast.LeakCheckEnabled {
		emitLcReport(w, heap)
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
		w("%s:", heapBaseSym)
		w("\t.quad 0")
	}
	if slices.Contains(helpers, "__alloc") || slices.Contains(helpers, "__free") || strings.Contains(b.String(), freelistSym) {
		emitFreelistBss(w)
	}
	if ast.LeakCheckEnabled {
		emitLcBss(w)
	}
	emitProcBss(w, withArgs, withEnv)
	emitHelperBss(w, helpers, strings.Contains(b.String(), rcUnderflowSym))
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
func emitFuncBody(w func(string, ...any), name string, p *Program, numAlloc int, strLabels map[string]string, sentLabels map[int64]string, fnIndex map[string]int, counting bool) error {
	label := fnLabel(name)
	// s3 — the last register in the file — is the free scratch the div/shift and
	// call sequences stage operands through. It is above the allocatable range,
	// so it never aliases the fixed registers those sequences pin (see gpRegs on
	// why sharing r8/r9 with the argument registers is safe) nor any register in
	// a call's save set, which holds allocatable homes only.
	scratch := p.NumRegFile - 1

	// Callee-saved registers this function actually clobbers. Per the System V
	// ABI the function must preserve them for its caller, so they are pushed
	// below the spill area and popped in the epilogue. A leaf that touches none
	// of them pays nothing.
	//
	// The set is read back off the emitted text rather than predicted from the
	// Program, because predicting it means keeping a list of every field and
	// every helper that can name a register, and a list that falls behind drops
	// a save silently — the caller gets a clobbered register and the failure
	// surfaces as a wrong answer somewhere else entirely. So the body is
	// emitted once into a buffer, scanned for what it mentions, and then copied
	// out behind the prologue, with the epilogue rendered after it. The
	// epilogue's pops cannot widen the set: they name only registers already in
	// it.
	var body strings.Builder
	bodyW := func(format string, args ...any) {
		fmt.Fprintf(&body, format+"\n", args...)
	}
	if err := emitFuncBlocks(bodyW, label, p, numAlloc, scratch, strLabels, sentLabels, fnIndex, counting); err != nil {
		return err
	}
	saved := calleeSavedIn(body.String(), p.NumRegFile)

	w("%s:", label)
	// Call-frame information, so a profiler or debugger can walk out of this
	// frame. The assembler turns these into .eh_frame (internal/native/cfi);
	// without them the image carries no unwind data at all, which is what the
	// stack-machine emitter has always provided and this one did not.
	//
	// Everything is expressed against rbp: once it is the CFA register the
	// rule holds for the whole body, so neither the spill reservation nor the
	// callee-saved pushes below need to touch it.
	//
	// The rules are the ones the stack-machine emitter emits, and no more.
	// Describing where each callee-saved register went would let a debugger
	// recover the caller's copies, but that emitter does not do it either.
	w("\t.cfi_startproc")
	w("\tpush rbp")
	w("\t.cfi_def_cfa_offset 16")
	w("\t.cfi_offset rbp, -16")
	w("\tmov rbp, rsp")
	w("\t.cfi_def_cfa_register rbp")
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

	copyLines(w, body.String())
	// One epilogue per function, which every return reaches by falling into it
	// or branching to it. A teardown at each return site needs its own CFI
	// bracket, because blocks are emitted in layout order and more body can
	// follow a return; one at the end needs none, and it is the whole of the
	// difference between 17,149 teardowns and 4,807 across the self-host
	// driver.
	if FuncReturns(p) {
		w(".L_%s_epi:", label)
		for j := len(saved) - 1; j >= 0; j-- {
			w("\tpop %s", reg(saved[j]))
		}
		w("\tmov rsp, rbp")
		w("\tpop rbp")
		w("\t.cfi_def_cfa rsp, 8")
		w("\tret")
	}
	w("\t.cfi_endproc")
	return nil
}

// FuncReturns reports whether any block ends in a return, so a function that
// cannot fall out of itself gets no epilogue to jump to. Exported because the
// arm64 emitter renders the same Program and asks the same question.
func FuncReturns(p *Program) bool {
	for _, blk := range p.Blocks {
		if blk.Term.Kind == TRet || blk.Term.Kind == TRetPair {
			return true
		}
	}
	return false
}

// copyLines hands the emitted body to w one line at a time. A multi-line
// string emitted as a unit would otherwise reach w whole, and w's per-line
// filtering — the self-move drop — would never see the lines inside it.
func copyLines(w func(string, ...any), body string) {
	for body != "" {
		line, rest, _ := strings.Cut(body, "\n")
		w("%s", line)
		body = rest
	}
}

// emitFuncBlocks writes every block body and terminator.
func emitFuncBlocks(w func(string, ...any), label string, p *Program, numAlloc, scratch int, strLabels map[string]string, sentLabels map[int64]string, fnIndex map[string]int, counting bool) error {
	order := LayoutOrder(p)
	// nextInLayout[bi] is the block physically following bi in the emitted
	// order, or -1 for the last one: a branch to it needs no instruction.
	nextInLayout := make([]int, len(p.Blocks))
	for i := range nextInLayout {
		nextInLayout[i] = -1
	}
	for oi := 0; oi+1 < len(order); oi++ {
		nextInLayout[order[oi]] = order[oi+1]
	}
	for _, bi := range order {
		blk := p.Blocks[bi]
		next := nextInLayout[bi]
		w(".L_%s_b%d:", label, bi)
		insts, cmpLine, jcc := fuseBranchCmp(blk)
		for ii, in := range insts {
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
			if lines, ok := inlineAllocLines(in, numAlloc, fmt.Sprintf("%s_b%d_i%d", label, bi, ii), counting); ok {
				for _, l := range lines {
					if strings.HasSuffix(l, ":") {
						w("%s", l)
					} else {
						w("\t%s", l)
					}
				}
				continue
			}
			if lines, ok := inlineRcLines(in, numAlloc, fmt.Sprintf("%s_b%d_i%d", label, bi, ii)); ok {
				for _, l := range lines {
					if strings.HasSuffix(l, ":") {
						w("%s", l)
					} else {
						w("\t%s", l)
					}
				}
				continue
			}
			// An index is address arithmetic, not a call: inline it rather than
			// making the allocator spill around it.
			if lines, ok := inlineArrIdxLines(in, numAlloc, fmt.Sprintf("%s_b%d_i%d", label, bi, ii)); ok {
				for _, l := range lines {
					if strings.HasSuffix(l, ":") {
						w("%s", l)
					} else {
						w("\t%s", l)
					}
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
			if line != "" {
				w("\t%s", line)
			}
		}
		if cmpLine != "" {
			w("\t%s", cmpLine)
		}
		switch blk.Term.Kind {
		case TRet:
			w("\tmov rax, %s", reg(blk.Term.RetReg))
			// The epilogue follows the last block, so a return there falls
			// into it.
			if next >= 0 {
				w("\tjmp .L_%s_epi", label)
			}
		case TRetPair:
			// System V pair return: tag in rax, payload in rdx. The two moves
			// are a parallel copy, since a home may already be rax or rdx.
			for _, l := range pairRetMoves(blk.Term.RetReg, blk.Term.RetReg2) {
				w("\t%s", l)
			}
			if next >= 0 {
				w("\tjmp .L_%s_epi", label)
			}
		case TJmp:
			// A block that ends where its successor begins falls through.
			if blk.Term.Target != next {
				w("\tjmp .L_%s_b%d", label, blk.Term.Target)
			}
		case TBrIf:
			// The false arm is the fallthrough where the layout allows it; when
			// the TRUE arm is the next block instead, branch on the inverse
			// condition to the false arm and fall through to the true one.
			t, f := blk.Term.True, blk.Term.False
			if jcc != "" {
				if inv, ok := invertJcc(jcc); ok && t == next && f != next {
					w("\t%s .L_%s_b%d", inv, label, f)
					break
				}
				w("\t%s .L_%s_b%d", jcc, label, t)
			} else {
				w("\ttest %s, %s", reg(blk.Term.CondReg), reg(blk.Term.CondReg))
				if t == next && f != next {
					w("\tjz .L_%s_b%d", label, f)
					break
				}
				w("\tjnz .L_%s_b%d", label, t)
			}
			if f != next {
				w("\tjmp .L_%s_b%d", label, f)
			}
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
	if n := len(insts); n > 0 && (c.SrcImm || c.Src != c.Dst) {
		if m := insts[n-1]; m.Op == MovReg && m.Dst == c.Dst {
			left = m.Src
			insts = insts[:n-1]
		}
	}
	return insts, fmt.Sprintf("cmp %s, %s", reg(left), rightOperandText(c)), cc
}

// rightOperandText renders a BinOp's or SetCmp's right operand: the immediate
// it carries, else its source register.
func rightOperandText(in Inst) string {
	if in.SrcImm {
		return strconv.FormatInt(in.Imm, 10)
	}
	return reg(in.Src)
}

// invertJcc is the conditional jump that takes the other arm. The integer
// conditions come in exact pairs, so a branch on the inverse falls through to
// the arm the original would have jumped to.
func invertJcc(jcc string) (string, bool) {
	inv, ok := map[string]string{
		"je": "jne", "jne": "je",
		"jl": "jge", "jge": "jl", "jle": "jg", "jg": "jle",
		"jb": "jae", "jae": "jb", "jbe": "ja", "ja": "jbe",
	}[jcc]
	return inv, ok
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
	seen := make([]bool, len(gpRegs))
	for i := 0; i < len(asm); {
		if !isRegTokenStart(asm[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(asm) && isRegTokenByte(asm[j]) {
			j++
		}
		if j-i <= maxRegSpelling {
			if r, ok := calleeSavedByName[asm[i:j]]; ok {
				seen[r] = true
			}
		}
		i = j
	}
	var out []int
	for _, r := range calleeSavedRegs {
		if r < numRegFile && seen[r] {
			out = append(out, r)
		}
	}
	return out
}

// Tokens are identifier-shaped, with `_` and `.` as word characters so a label
// name cannot decompose into a register name.
func isRegTokenStart(c byte) bool {
	return c == '_' || c == '.' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isRegTokenByte(c byte) bool { return isRegTokenStart(c) || (c >= '0' && c <= '9') }

// maxRegSpelling is the longest register name (`r12d`); a longer token cannot
// be one.
const maxRegSpelling = 4

// calleeSavedByName maps every width spelling of each callee-saved register to
// its gpRegs index.
var calleeSavedByName = func() map[string]int {
	m := map[string]int{}
	for _, r := range calleeSavedRegs {
		for _, name := range regSpellings(r) {
			m[name] = r
		}
	}
	return m
}()

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
	// it, so pad to make their combined count even. The pad is one more push
	// rather than a stack adjust: a push is one or two bytes where the adjust
	// is four, and a call site pays it twice.
	pad := (len(saved)+nStack)%2 != 0
	if pad {
		out = append(out, fmt.Sprintf("push %s", padReg(saved)))
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
	// ir.CodegenAlias resolves a Map / MapIter call onto the stdlib `_impl`
	// that implements it; the driver keeps those alive under the same map.
	out = append(out, fmt.Sprintf("call %s", fnLabel(ir.CodegenAlias(in.Callee))))
	// With nothing saved the pad sits directly under the stack arguments and
	// leaves with them in the one adjust.
	if drop := nStack + padSlots(pad, saved); drop > 0 {
		out = append(out, fmt.Sprintf("add rsp, %d", 8*drop))
	}
	restore := func() {
		for i := len(saved) - 1; i >= 0; i-- {
			out = append(out, fmt.Sprintf("pop %s", reg(saved[i])))
		}
		if pad && len(saved) > 0 {
			out = append(out, fmt.Sprintf("pop %s", reg(saved[0])))
		}
	}
	maskDst := func() {
		if fix := maskFix(in.Dst, in.W, in.Narrow); fix != "" {
			out = append(out, strings.TrimPrefix(fix, "\n\t"))
		}
	}
	// The result registers can be written before the restores — skipping the
	// staging scratch entirely — exactly when the restores do not write them.
	// The allocator cannot put a result and a value live ACROSS the same call in
	// one register (their intervals overlap), so this holds for every call; the
	// check is what keeps that an optimisation rather than depending on an
	// assumption about a pass in another package.
	if !inSaveSet(saved, in.Dst) && (in.Op != CallPair || !inSaveSet(saved, in.Dst2)) {
		if in.Op != CallPair {
			// A 32-bit result is sign-extended as it is taken from eax, so no
			// fix follows; a 64-bit one already in place needs no move.
			if in.Dst != raxReg || in.W != 64 {
				out = append(out, captureRax(in.Dst, in.W))
			}
			restore()
			return out, nil
		}
		// System V returns in rax (tag) / rdx (payload), so delivering a pair into
		// its destinations is a parallel copy over abstract indices. Self-moves
		// are dropped rather than emitted as `mov rax, rax`, which is the whole
		// point of coalescing here.
		var moves [][2]int
		if in.Dst != raxReg {
			moves = append(moves, [2]int{in.Dst, raxReg})
		}
		if in.Dst2 != rdxReg {
			moves = append(moves, [2]int{in.Dst2, rdxReg})
		}
		out = append(out, resolveRegMoves(moves)...)
		restore()
		maskDst()
		return out, nil
	}
	// The capture sign-extends a 32-bit result, so the placing move below
	// carries a value that needs no fix.
	out = append(out, captureRax(scratch, in.W)) // capture result (tag)
	if in.Op == CallPair {
		// The second return (payload) is in rdx. Capture it into s0 — free during
		// the call inst and not in the caller-saved set — so the restores below
		// don't overwrite it.
		out = append(out, fmt.Sprintf("mov %s, rdx", reg(s0)))
	}
	restore()
	out = append(out, fmt.Sprintf("mov %s, %s", reg(in.Dst), reg(scratch))) // place result
	if in.Op == CallPair {
		out = append(out, fmt.Sprintf("mov %s, %s", reg(in.Dst2), reg(s0))) // place payload
	}
	return out, nil
}

// padReg names the register whose extra push realigns the stack at a call
// site: the first saved register, so the matching pop restores a value that
// is being restored anyway, or rax when nothing is saved, which the call
// overwrites and the pad then leaves the stack with the arguments.
func padReg(saved []int) string {
	if len(saved) > 0 {
		return reg(saved[0])
	}
	return "rax"
}

// padSlots is the number of pad slots the post-call stack adjust drops: one
// when the pad was pushed with nothing saved, since only then is there no pop
// to take it.
func padSlots(pad bool, saved []int) int {
	if pad && len(saved) == 0 {
		return 1
	}
	return 0
}

// captureRax takes a call's result out of rax into dst: a 32-bit result is
// sign-extended on the way (`movsxd dst, eax`), which is the same instruction
// count as a move and leaves nothing for a later fix to do.
func captureRax(dst int, w int8) string {
	if w != 64 {
		return fmt.Sprintf("movsxd %s, eax", reg(dst))
	}
	return fmt.Sprintf("mov %s, rax", reg(dst))
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
	// The env pointer is passed as the callee's final argument, so the argument
	// sequence is one longer than ArgLocs.
	nArgs := len(in.ArgLocs) + 1
	nStack := stackArgCount(nArgs)
	nReg := nArgs - nStack
	// Everything below the call shifts rsp by 8: the pad, the saved registers,
	// the stashed target, and the stack arguments. The register-half pushes are
	// popped again before the call, so they do not count.
	pad := (len(saved)+1+nStack)%2 != 0
	if pad {
		out = append(out, fmt.Sprintf("push %s", padReg(saved)))
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
		captureRax(scratch, in.W), // capture result, sign-extended when 32-bit
		// drop the stack args, the stash, and the pad when nothing is saved
		fmt.Sprintf("add rsp, %d", 8*(nStack+1+padSlots(pad, saved))),
	)
	for i := len(saved) - 1; i >= 0; i-- {
		out = append(out, fmt.Sprintf("pop %s", reg(saved[i])))
	}
	if pad && len(saved) > 0 {
		out = append(out, fmt.Sprintf("pop %s", reg(saved[0])))
	}
	out = append(out, fmt.Sprintf("mov %s, %s", reg(in.Dst), reg(scratch))) // place result
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
	if fix := maskFix(in.Dst, in.W, in.Narrow); fix != "" {
		out = append(out, strings.TrimPrefix(fix, "\n\t"))
	}
	return out
}

// lineWriter returns the emitter's line writer over b: one formatted line per
// call, dropping the dead self-moves register allocation leaves behind.
func lineWriter(b *strings.Builder) func(string, ...any) {
	return func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if isDeadSelfMove(line) {
			return
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

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
// essential in one way: the last numScratch entries are the emitter's staging
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
func maskFix(dst int, w int8, narrow bool) string {
	if w == 64 || narrow {
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
			line += maskFix(in.Dst, in.W, in.Narrow)
		}
		return line, nil
	case MovReg:
		return fmt.Sprintf("mov %s, %s", reg(in.Dst), reg(in.Src)), nil
	case UnNeg:
		return fmt.Sprintf("neg %s", reg(in.Dst)) + maskFix(in.Dst, in.W, in.Narrow), nil
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
		case ssa.OpShl, ssa.OpShr, ssa.OpShrU, ssa.OpRotr:
			return shiftSeq(in) + maskFix(in.Dst, in.W, in.Narrow), nil
		case ssa.OpDiv, ssa.OpDivU, ssa.OpRem, ssa.OpRemU:
			return divSeq(in, scratch) + maskFix(in.Dst, in.W, in.Narrow), nil
		}
		op, ok := binMnemonic(in.K)
		if !ok {
			return "", fmt.Errorf("x86_64ssa: binary op %v unsupported in the real-asm slice", in.K)
		}
		return fmt.Sprintf("%s %s, %s", op, reg(in.Dst), rightOperandText(in)) + maskFix(in.Dst, in.W, in.Narrow), nil
	case SetCmp:
		cc, ok := setccMnemonic(in.K)
		if !ok {
			return "", fmt.Errorf("x86_64ssa: comparison %v unsupported", in.K)
		}
		// dst = (dst CMP src): compare, set the low byte from flags, zero-extend.
		return fmt.Sprintf("cmp %s, %s\n\t%s %s\n\tmovzx %s, %s",
			reg(in.Dst), rightOperandText(in), cc, reg8n(in.Dst), reg(in.Dst), reg8n(in.Dst)), nil
	case MemAlloc:
		// A bare 16-aligned block of Src bytes through __alloc, exactly what
		// the IR expects: it writes rc = 1 at base+0 itself and uses base+8
		// as the data pointer. See docs/SSA-RC-RUNTIME.md.
		return strings.Join(allocPresLines(reg(in.Dst), reg(in.Src)), "\n\t"), nil
	case MemLoad:
		mem := memRef(reg(in.Src), in.Imm)
		if in.Bytes == 8 {
			return fmt.Sprintf("mov %s, %s", reg(in.Dst), mem) + maskFix(in.Dst, in.W, in.Narrow), nil
		}
		if in.Bytes == 4 {
			// 4-byte load: `mov r32, [mem]` zero-extends into the 64-bit reg.
			return fmt.Sprintf("mov %s, %s", reg32n(in.Dst), mem) + maskFix(in.Dst, in.W, in.Narrow), nil
		}
		size := "byte ptr"
		if in.Bytes == 2 {
			size = "word ptr"
		}
		if in.Signed {
			if in.W == 64 {
				return fmt.Sprintf("movsx %s, %s %s", reg(in.Dst), size, mem), nil
			}
			return fmt.Sprintf("movsx %s, %s %s", reg32n(in.Dst), size, mem) + maskFix(in.Dst, in.W, in.Narrow), nil
		}
		// A zero-extending sub-word load already leaves bits 63:8 (or 63:16)
		// clear, which is the i32 sign-extension of a value that small, so the
		// fixup would rewrite the register with itself.
		return fmt.Sprintf("movzx %s, %s %s", reg32n(in.Dst), size, mem), nil
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
		return fmt.Sprintf("movq xmm0, %s\n\tcvttsd2si %s, xmm0", d, d) + maskFix(in.Dst, in.W, in.Narrow), nil
	case ssa.OpReinterpretF64ToI64, ssa.OpReinterpretI64ToF64:
		// Identity: the register already holds the f64 bit pattern.
		return "", nil
	case ssa.OpReinterpretF32ToI32:
		// cvtsd2ss leaves bits 63:32 of xmm0 stale; the movsxd sign-extends
		// the f32 pattern out of the low half into the i32 storage convention.
		return fmt.Sprintf("movq xmm0, %s\n\tcvtsd2ss xmm0, xmm0\n\tmovq %s, xmm0", d, d) + maskFix(in.Dst, 32, in.Narrow), nil
	case ssa.OpReinterpretI32ToF32:
		// cvtss2sd reads only the low 32 bits, so the i32's sign-extension
		// above them is ignored.
		return fmt.Sprintf("movq xmm0, %s\n\tcvtss2sd xmm0, xmm0\n\tmovq %s, xmm0", d, d), nil
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
	heapBaseSym  = "__ssa_heap_base"
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

// strBlockBytes is the byte overhead of a string block over its length: the
// rc word (4), the length word (4), and the trailing NUL (1). Every site that
// allocates a string and every site that frees one must use this same number,
// because __alloc and __free derive the size CLASS from it and a block pushed
// onto one class is only ever handed back out from that class.
const strBlockBytes = 9

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
	w("\tmov [rip + %s], rax", heapBaseSym)
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
	if ast.LeakCheckEnabled {
		// Every bump reaches the guard, so this counts them all; the pushfq
		// above is what makes a flag-clobbering add safe here.
		emitLcAdd(w, lcAllocCountSym, "")
	}
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

// abortKinds are the fatal aborts this backend has check sites for. Each names
// an entry in the flat x86-64 backend's table (nativex86_64.AbortMsg), which
// is where the text and the exit status come from: a program's abort output
// must not depend on which x86-64 emitter built it (#5538). The sanitizer
// diagnostics are absent because their detectors are.
//
// A tail is emitted only when the module reaches it, like every other helper
// here — emitAbortTails looks for its label in what has already been written.
// A check site is then a single jump instead of the three instructions it used to
// open-code.
var abortKinds = []struct{ tail, msgSym string }{
	{".Lssa_abort_arr_oob", "__fern_msg_arr_oob"},
	{".Lssa_abort_slice_oob", "__fern_msg_slice_oob"},
	{".Lssa_abort_slice_range", "__fern_msg_slice_range"},
	{".Lssa_abort_str_slice", "__fern_msg_str_slice"},
	{".Lssa_abort_alloc_size", "__fern_msg_alloc_size"},
}

// Tail labels, named rather than spelled at each site so a typo is a build
// error instead of a silent jump to whichever kind sorts next to it.
const (
	abortArrOOB     = ".Lssa_abort_arr_oob"
	abortSliceOOB   = ".Lssa_abort_slice_oob"
	abortSliceRange = ".Lssa_abort_slice_range"
	abortStrSlice   = ".Lssa_abort_str_slice"
	abortAllocSize  = ".Lssa_abort_alloc_size"
)

// abortKindForIdx picks the tail an index check jumps to. One emitter serves
// arrays, single-word strings and slice views, and the flat backend gives each
// of the three its own message.
func abortKindForIdx(name string, slice bool) string {
	switch {
	case name == "__str_idx":
		return abortStrSlice
	case slice:
		return abortSliceOOB
	}
	return abortArrOOB
}

// emitAbortTails writes a tail for every abortKinds entry `emitted` — the
// module text written so far — branches to: the diagnostic to stderr, then
// exit with that kind's status, followed by the read-only messages they point
// at. Every site that can reach a tail is already written by the time this
// runs, so a label absent from `emitted` is one no path takes.
func emitAbortTails(w func(string, ...any), emitted string) {
	var used []int
	for i, k := range abortKinds {
		if strings.Contains(emitted, k.tail) {
			used = append(used, i)
		}
	}
	if len(used) == 0 {
		return
	}
	w("")
	for _, i := range used {
		k := abortKinds[i]
		text, code := nativex86_64.AbortMsg(k.msgSym)
		w("%s:", k.tail)
		w("\tmov edi, 2") // stderr
		w("\tlea rsi, [rip + %s]", k.msgSym)
		w("\tmov edx, %d", len(text))
		w("\tmov eax, 1") // write
		w("\tsyscall")
		w("\tmov edi, %d", code)
		w("\tmov eax, 231") // exit_group
		w("\tsyscall")
	}
	w(".section .rodata")
	for _, i := range used {
		k := abortKinds[i]
		text, _ := nativex86_64.AbortMsg(k.msgSym)
		bytes := make([]string, len(text))
		for i := 0; i < len(text); i++ {
			bytes[i] = strconv.Itoa(int(text[i]))
		}
		w("%s:", k.msgSym)
		w("\t.byte %s", strings.Join(bytes, ", "))
	}
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
		// An rc-headed block through __alloc: rc = 1 at base+0, the payload
		// size at base+4 (what __fern_closure_drop hands __fern_box_free),
		// data at base+8. A zero-byte payload still gets a slot of its own.
		if bytes == 0 {
			bytes = 8
		}
		out = append(out, allocPresLines(reg(dst), fmt.Sprintf("%d", bytes+8))...)
		out = append(out,
			fmt.Sprintf("mov dword ptr %s, 1", memRef(reg(dst), 0)),         // rc = 1
			fmt.Sprintf("mov dword ptr %s, %d", memRef(reg(dst), 4), bytes), // payload size
			fmt.Sprintf("add %s, 8", reg(dst)),                              // data = base + 8
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
	if fix := maskFix(in.Dst, in.W, in.Narrow); fix != "" {
		out = append(out, strings.TrimPrefix(fix, "\n\t"))
	}
	return out, true
}

// arrIdxInline maps an index helper onto its element-stride shift, whether it
// bounds-checks, and whether the operand is a view rather than a buffer. These
// are the calls the IR emits for `a[i]` (internal/ir), and each is a compare and
// an lea — so the call machinery around them costs more than the work.
//
// Both stack-machine backends have inlined all of these all along
// (emitInlineIdxHelper in internal/codegen/x86_64 and internal/codegen/arm64);
// this backend had no inline form at all, so indexing — which sits in the
// innermost loop of anything that walks an array — was a call per element, and
// the allocator spilled every caller-saved register live across it.
//
// slice marks the view spellings, one indirection further out: the length is a
// field at [base+8] rather than a header at [base-4], and the data pointer has
// to be loaded from [base+0] before the scaled address. They have no _nc form —
// the IR emits no bounds-check-elided slice index — and __slice_idx_1 is the one
// an indexed walk of `chunk.as_bytes()` reaches, which is how every byte scan in
// coreutils/ is written.
//
// Strings here are single-word data pointers with a byte length at -4, so
// __str_idx shares __arr_idx_1's form; the default backend's two-word string ABI
// does not apply.
var arrIdxInline = map[string]struct {
	shift   int
	checked bool
	slice   bool
}{
	"__str_idx":       {0, true, false}, // single-word string, byte stride
	"__str_idx_nc":    {0, false, false},
	"__arr_idx":       {2, true, false}, // stride 4 (i32)
	"__arr_idx_1":     {0, true, false}, // stride 1 (byte array)
	"__arr_idx_8":     {3, true, false}, // stride 8 (i64 / pointer)
	"__arr_idx_16":    {4, true, false}, // stride 16 (two-word string[])
	"__arr_idx_nc":    {2, false, false},
	"__arr_idx_1_nc":  {0, false, false},
	"__arr_idx_8_nc":  {3, false, false},
	"__arr_idx_16_nc": {4, false, false},
	"__slice_idx":     {2, true, true},
	"__slice_idx_1":   {0, true, true},
	"__slice_idx_8":   {3, true, true},
}

// inlineArrIdxLines renders an index call as the address compute it is, or
// reports false when the callee is something else. The seed keeps each site's
// bounds-check label unique.
//
// Each form reproduces its helper exactly, including two details that are not
// free choices. The bounds check is a SINGLE unsigned compare because a negative
// index arrives as a huge unsigned and fails the same test. And the slice form
// re-narrows the checked index through a 32-bit move before the 64-bit address
// calculation (`mov edx, esi` in emitSliceIdxHelper): the compare only proved
// the low half in range, so an operand reaching here with dirty upper bits would
// otherwise escape it.
func inlineArrIdxLines(in Inst, numAlloc int, seed string) ([]string, bool) {
	form, ok := arrIdxInline[in.Callee]
	if !ok || in.Op != Call || len(in.ArgLocs) != 2 {
		return nil, false
	}
	// s0/s1 home a slot-resident operand; s2 is the third register the length
	// read needs, which is neither operand nor the destination.
	s0, s1, s2 := numAlloc, numAlloc+1, numAlloc+2
	var out []string
	materialise := func(l Loc, tmp int) int {
		if l.IsReg {
			return l.Reg
		}
		out = append(out, fmt.Sprintf("mov %s, %s", reg(tmp), slotMem(l.Slot)))
		return tmp
	}
	base := materialise(in.ArgLocs[0], s0)
	idx := materialise(in.ArgLocs[1], s1)
	if form.checked {
		okLbl := fmt.Sprintf(".Lssa_idx_%s_ok", seed)
		lenRef := memRef(reg(base), -4)
		if form.slice {
			lenRef = memRef(reg(base), 8)
		}
		// The length is read where it lies: `cmp r32, m32` needs no register
		// for it, and the loop that indexes a buffer repeatedly pays one load
		// per access instead of a load and a move.
		out = append(out,
			fmt.Sprintf("cmp %s, dword ptr %s", reg32n(idx), lenRef),
			fmt.Sprintf("jb %s", okLbl),
			"jmp "+abortKindForIdx(in.Callee, form.slice),
			okLbl+":",
		)
	}
	if form.slice {
		// s2 held the length, which the compare has consumed. The destination
		// takes the data pointer: it is written last in the call this replaces,
		// and neither operand is read after this point, so a destination that
		// aliases one of them loses nothing.
		out = append(out,
			fmt.Sprintf("mov %s, %s", reg32n(s2), reg32n(idx)),
			fmt.Sprintf("mov %s, %s", reg(in.Dst), memRef(reg(base), 0)),
		)
		base, idx = in.Dst, s2
	}
	// lea scales by 1, 2, 4 or 8 only, so stride 16 needs the shift spelled out
	// — through s2 rather than in place, because unlike the helper's dead
	// argument register the index here may still be live.
	if form.shift <= 3 {
		out = append(out, fmt.Sprintf("lea %s, [%s + %s*%d]", reg(in.Dst), reg(base), reg(idx), 1<<form.shift))
	} else {
		out = append(out,
			fmt.Sprintf("mov %s, %s", reg(s2), reg(idx)),
			fmt.Sprintf("shl %s, %d", reg(s2), form.shift),
			fmt.Sprintf("lea %s, [%s + %s]", reg(in.Dst), reg(base), reg(s2)),
		)
	}
	if fix := maskFix(in.Dst, in.W, in.Narrow); fix != "" {
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
	"__fern_rc_is_unique":             emitRcIsUniqueHelper,
	"__fern_rc_inc":                   emitRcIncHelper,
	"__fern_rc_dec":                   emitRcDecHelper,
	"__fern_closure_drop":             emitClosureDropHelper,
	"__fern_box_free":                 emitBoxFreeHelper,
	"__str_len":                       emitStrLenHelper,
	"__fern_arr_dec":                  emitArrDecHelper,
	"__arr_idx":                       emitArrIdxHelperN("__arr_idx", 2),
	"__arr_idx_nc":                    emitArrIdxHelperNChecked("__arr_idx_nc", 2, false),
	"__arr_idx_1":                     emitArrIdxHelperN("__arr_idx_1", 0),
	"__arr_idx_1_nc":                  emitArrIdxHelperNChecked("__arr_idx_1_nc", 0, false),
	"__arr_idx_8":                     emitArrIdxHelperN("__arr_idx_8", 3),
	"__arr_idx_8_nc":                  emitArrIdxHelperNChecked("__arr_idx_8_nc", 3, false),
	"__arr_idx_16":                    emitArrIdxHelperN("__arr_idx_16", 4),
	"__arr_idx_16_nc":                 emitArrIdxHelperNChecked("__arr_idx_16_nc", 4, false),
	"__str_idx":                       emitArrIdxHelperN("__str_idx", 0),
	"__str_idx_nc":                    emitArrIdxHelperNChecked("__str_idx_nc", 0, false),
	"__slice_idx":                     emitSliceIdxHelper("__slice_idx", 2),
	"__slice_idx_1":                   emitSliceIdxHelper("__slice_idx_1", 0),
	"__slice_idx_8":                   emitSliceIdxHelper("__slice_idx_8", 3),
	"__slice_range":                   emitSliceRangeHelper,
	"__slice_make":                    emitSliceMakeHelper,
	"stdin":                           emitStdHandleHelper("stdin", 0),
	"stdout":                          emitStdHandleHelper("stdout", 1),
	"stderr":                          emitStdHandleHelper("stderr", 2),
	"exit":                            emitExitHelper,
	"__method_Reader_close":           emitHandleCloseHelper("__method_Reader_close", "rdc"),
	"__method_Writer_close":           emitHandleCloseHelper("__method_Writer_close", "wrc"),
	"__method_Writer_write":           emitWriterWriteHelper,
	"__method_Reader_read_chunk":      emitReaderReadChunkHelper,
	"open_reader":                     emitOpenHandleHelper("open_reader", "or", 0, 0),
	"open_writer":                     emitOpenHandleHelper("open_writer", "ow", 577, 438),
	"__method_string_as_bytes":        emitStringAsBytesHelper,
	"__fern_memchr":                   emitMemchrHelper,
	"__fern_mismatch":                 emitMismatchHelper,
	"__fern_rmemchr":                  emitRmemchrHelper,
	"__fern_ascii_run":                emitAsciiRunHelper,
	"__fern_count_byte":               emitCountByteHelper,
	"__fern_scan_set":                 emitScanSetHelper,
	"__fern_count_runs":               emitCountRunsHelper,
	"__fern_bsd_sum":                  emitBsdSumHelper,
	"__fern_sum_bytes":                emitSumBytesHelper,
	"__fern_scale_f64":                emitScaleF64Helper,
	"__fern_crc32_cksum":              emitCrc32CksumHelper,
	"__alloc_u8":                      emitAllocU8Helper,
	"string_from_bytes_unchecked":     emitStringFromBytesHelper,
	"__str_slice":                     emitStrSliceHelper,
	"__fern_arr_push_grow":            emitArrPushGrowHelper,
	"__fern_arr_push_grow_ptr":        emitArrPushGrowElemHelper("__fern_arr_push_grow_ptr", "apgp", false),
	"__fern_arr_push_grow_str":        emitArrPushGrowElemHelper("__fern_arr_push_grow_str", "apgs", false),
	"__fern_arr_push_grow_move_ptr":   emitArrPushGrowElemHelper("__fern_arr_push_grow_move_ptr", "apgmp", true),
	"__fern_arr_push_grow_move_str":   emitArrPushGrowElemHelper("__fern_arr_push_grow_move_str", "apgms", true),
	"__fern_arr_cow_inplace":          emitArrCowInplaceHelper,
	"__fern_arr_cow_inplace_ptr":      emitArrCowInplaceElemHelper("__fern_arr_cow_inplace_ptr", "__fern_rc_inc", "cowp"),
	"__fern_arr_cow_inplace_str":      emitArrCowInplaceElemHelper("__fern_arr_cow_inplace_str", "__fern_rc_inc", "cows"),
	"__str_eq":                        emitStrEqHelper,
	"__str_ord":                       emitStrOrdHelper,
	"__str_concat":                    emitStrConcatHelper,
	"strbuf_reset":                    emitStrbufResetHelper,
	"strbuf_append":                   emitStrbufAppendHelper,
	"strbuf_take":                     emitStrbufTakeHelper,
	"__fern_str_append":               emitStrAppendHelper,
	"__fern_str_dec":                  emitStrDecHelper,
	"__fern_drop_arr_str":             emitDropArrElemHelper("__fern_drop_arr_str", "__fern_str_dec", "dropstr"),
	"__fern_drop_arr_ptr":             emitDropArrElemHelper("__fern_drop_arr_ptr", "__fern_rc_dec", "dropptr"),
	"__fern_map_drop":                 emitMapDropHelper,
	"__fern_map_hash_seed":            emitMapHashSeedHelper,
	"__alloc":                         emitAllocHelper,
	"__free":                          emitFreeHelper,
	"__memset":                        emitMemsetHelper,
	"write":                           emitWriteHelper,
	"__fern_heap_bump_bytes":          emitHeapBumpBytesHelper,
	"__fern_heap_alloc_count":         emitHeapAllocCountHelper,
	"__method_Reader_read_line":       emitReadLineHelper("__method_Reader_read_line", "", -1),
	"read_line":                       emitReadLineHelper("read_line", "_0", 0),
	"read_dir":                        emitReadDirHelper,
	"tcp_listen":                      emitTcpListenHelper,
	"tcp_connect":                     emitTcpConnectHelper,
	"tcp_accept":                      emitTcpAcceptHelper,
	"tcp_local_port":                  emitTcpLocalPortHelper,
	"tcp_recv":                        emitTcpRecvHelper,
	"tcp_send":                        emitTcpSendHelper,
	"tcp_close":                       emitTcpCloseHelper,
	"tcp_pollable":                    emitIdentityHelper("tcp_pollable"),
	"wasm_timer_pollable":             emitConstHelper("wasm_timer_pollable", -1),
	"wasm_pollable_drop":              emitConstHelper("wasm_pollable_drop", 0),
	"poll":                            emitPollHelper,
	"isatty":                          emitIsattyHelper,
	"__method_Reader_isatty":          emitHandleIsattyHelper("__method_Reader_isatty"),
	"__method_Writer_isatty":          emitHandleIsattyHelper("__method_Writer_isatty"),
	"__method_Reader_stat":            emitFdStatHelper("__method_Reader_stat", "rst"),
	"__method_Writer_stat":            emitFdStatHelper("__method_Writer_stat", "wst"),
	"hostname":                        emitHostnameHelper,
	"putchar":                         emitPutcharHelper,
	"create_dir_all":                  emitCreateDirAllHelper,
	"__fern_rc_underflow_count":       emitRcUnderflowCountHelper,
	"buf_new":                         emitBufNewHelper,
	"__fern_buf_reserve":              emitBufReserveHelper,
	"buf_push":                        emitBufPushHelper,
	"buf_push_range":                  emitBufPushRangeHelper,
	"buf_push_mapped":                 emitBufPushMappedHelper,
	"buf_push_filtered":               emitBufPushFilteredHelper,
	"buf_push_expanded":               emitBufPushExpandedHelper,
	"buf_push_byte":                   emitBufPushByteHelper,
	"buf_push_u64":                    emitBufPushU64Helper,
	"buf_len":                         emitBufLenHelper,
	"buf_take":                        emitBufTakeHelper,
	"buf_free":                        emitBufFreeHelper,
	"__alloc_reuse":                   emitAllocReuseHelper,
	"print":                           emitPrintHelper("print", 1),
	"remove_dir_all":                  emitRemoveDirAllHelper,
	"__fern_io_error":                 emitIoErrorHelper,
	"eprint":                          emitPrintHelper("eprint", 2),
	"args":                            emitArgsHelper,
	"env":                             emitEnvHelper,
	"stat":                            emitStatHelper,
	"lstat":                           emitLstatHelper,
	"access":                          emitAccessHelper,
	"getcwd":                          emitGetcwdHelper,
	"read_link":                       emitReadLinkHelper,
	"chdir":                           emitChdirHelper,
	"chroot":                          emitChrootHelper,
	"setuid":                          emitCredSetHelper("setuid", "suid", 105),
	"setgid":                          emitCredSetHelper("setgid", "sgid", 106),
	"setgroups":                       emitSetgroupsHelper,
	"create_dir":                      emitCreateDirHelper,
	"remove_dir":                      emitRemoveDirHelper,
	"create_link":                     emitCreateLinkHelper,
	"create_symlink":                  emitCreateSymlinkHelper,
	"rename":                          emitRenameHelper,
	"chmod":                           emitChmodHelper,
	"chmod_at":                        emitChmodAtHelper,
	"truncate":                        emitTruncateHelper,
	"mknod":                           emitMknodHelper,
	"chown_at":                        emitChownAtHelper,
	"getuid":                          emitIdHelper("getuid", 102),
	"getgid":                          emitIdHelper("getgid", 104),
	"geteuid":                         emitIdHelper("geteuid", 107),
	"getegid":                         emitIdHelper("getegid", 108),
	"umask":                           emitUmaskHelper,
	"cpu_count":                       emitCPUCountHelper,
	"sleep_ns":                        emitSleepNsHelper,
	"now_ns":                          emitClockHelper("now_ns", 0, 1_000_000_000, 1),
	"__method_Reader_seek":            emitSeekHelper("__method_Reader_seek", "rsk"),
	"__method_Reader_splice_to":       emitReaderSpliceHelper,
	"__method_Writer_seek":            emitSeekHelper("__method_Writer_seek", "wsk"),
	"__method_Writer_flags":           emitFdFlagsHelper("__method_Writer_flags", "wfl"),
	"__method_Reader_flags":           emitFdFlagsHelper("__method_Reader_flags", "rfl"),
	"__method_Writer_write_some":      emitWriterWriteSomeHelper,
	"__method_Writer_truncate":        emitFdCallHelper("__method_Writer_truncate", "wtr", 77, nil),
	"__method_Reader_fsync":           emitFdCallHelper("__method_Reader_fsync", "rfsy", 74, nil),
	"__method_Writer_fsync":           emitFdCallHelper("__method_Writer_fsync", "wfsy", 74, nil),
	"__method_Reader_fdatasync":       emitFdCallHelper("__method_Reader_fdatasync", "rfds", 75, nil),
	"__method_Writer_fdatasync":       emitFdCallHelper("__method_Writer_fdatasync", "wfds", 75, nil),
	"__method_Reader_syncfs":          emitFdCallHelper("__method_Reader_syncfs", "rsfs", 306, nil),
	"__method_Writer_syncfs":          emitFdCallHelper("__method_Writer_syncfs", "wsfs", 306, nil),
	"__method_Writer_dup_onto":        emitFdCallHelper("__method_Writer_dup_onto", "wdpo", 292, prepDupOnto),
	"sync":                            emitSyncHelper,
	"open_appender":                   emitOpenHandleHelper("open_appender", "oa", 1089, 438),
	"open_exclusive":                  emitOpenHandleHelper("open_exclusive", "ox", 193, 384),
	"open_reader_with":                emitOpenWithHelper("open_reader_with", "orw", 0),
	"open_writer_with":                emitOpenWithHelper("open_writer_with", "oww", 1),
	"environ":                         emitEnvironHelper,
	"read_dir_all":                    emitReadDirAllHelper,
	"statfs":                          emitStatfsHelper,
	"uname_field":                     emitUnameFieldHelper,
	"getgroups":                       emitGetgroupsHelper,
	"signal_send":                     emitSignalSendHelper,
	"set_process_group":               emitSetProcessGroupHelper,
	"priority":                        emitPriorityHelper,
	"set_priority":                    emitSetPriorityHelper,
	"signal_ignore":                   emitSignalDispositionHelper("signal_ignore", 1),
	"signal_default":                  emitSignalDispositionHelper("signal_default", 0),
	"signal_disposition":              emitSignalDispositionReadHelper,
	"signal_mask":                     emitSignalMaskHelper,
	"proc_fork":                       emitProcForkHelper,
	"proc_waitpid":                    emitProcWaitpidHelper("proc_waitpid", "wait", false),
	"proc_waitpid_nohang":             emitProcWaitpidHelper("proc_waitpid_nohang", "wnh", true),
	"proc_exec":                       emitProcExecHelper,
	"proc_exec_as":                    emitProcExecAsHelper,
	"window_size":                     emitWindowSizeHelper,
	"set_window_size":                 emitSetWindowSizeHelper,
	"termios_get":                     emitTermiosGetHelper,
	"termios_set":                     emitTermiosSetHelper,
	"set_file_times":                  emitSetFileTimesHelper,
	"__method_Reader_window_size":     emitHandleTtyHelper("__method_Reader_window_size", "window_size"),
	"__method_Reader_set_window_size": emitHandleTtyHelper("__method_Reader_set_window_size", "set_window_size"),
	"__method_Reader_termios_get":     emitHandleTtyHelper("__method_Reader_termios_get", "termios_get"),
	"__method_Reader_termios_set":     emitHandleTtyHelper("__method_Reader_termios_set", "termios_set"),
	"__memcpy":                        emitMemcpyHelper,
	"read_file":                       emitReadFileHelper("read_file", "rf", false),
	"read_file_bytes":                 emitReadFileHelper("read_file_bytes", "rfb", true),
	"write_file":                      emitWriteFileHelper,
	"remove_file":                     emitRemoveFileHelper,
	"temp_dir":                        emitTempDirHelper,
	"monotonic_ns":                    emitClockHelper("monotonic_ns", 1, 1_000_000_000, 1),
	"now_unix_ms":                     emitClockHelper("now_unix_ms", 0, 1_000, 1_000_000),
	"sleep_ms":                        emitSleepMsHelper,
	"random_bytes":                    emitRandomBytesHelper,
	"random_i32":                      emitRandomI32Helper,
	"__abs_f64":                       emitAbsF64Helper,
	"__sqrt_f64":                      emitF64UnaryHelper("__sqrt_f64", "sqrtsd xmm0, xmm0"),
	"__floor_f64":                     emitF64UnaryHelper("__floor_f64", "roundsd xmm0, xmm0, 1"),
	"__ceil_f64":                      emitF64UnaryHelper("__ceil_f64", "roundsd xmm0, xmm0, 2"),
	"__trunc_f64":                     emitF64UnaryHelper("__trunc_f64", "roundsd xmm0, xmm0, 3"),
	"__round_f64":                     emitRoundF64Helper,
	"__sin_f64":                       emitTranscendentalHelper("__sin_f64"),
	"__cos_f64":                       emitTranscendentalHelper("__cos_f64"),
	"__exp_f64":                       emitTranscendentalHelper("__exp_f64"),
	"__log_f64":                       emitTranscendentalHelper("__log_f64"),
	"__pow_f64":                       emitTranscendentalHelper("__pow_f64"),
}

// heapUsingHelpers are runtime helpers that allocate on the SSA bump heap, so
// the .bss heap section + cursor must be emitted whenever one is referenced even
// if the program body has no direct heap op.
var heapUsingHelpers = map[string]bool{
	"strbuf_append":               true,
	"strbuf_take":                 true,
	"__slice_make":                true,
	"__str_concat":                true,
	"__alloc_u8":                  true,
	"__fern_scale_f64":            true,
	"string_from_bytes_unchecked": true, "__str_slice": true,
	"__fern_arr_push_grow":       true,
	"__fern_arr_cow_inplace":     true,
	"__alloc_reuse":              true,
	"remove_dir_all":             true,
	"__fern_io_error":            true,
	"buf_new":                    true,
	"__fern_buf_reserve":         true,
	"buf_take":                   true,
	"stdin":                      true,
	"stdout":                     true,
	"stderr":                     true,
	"__method_Reader_close":      true,
	"__method_Writer_close":      true,
	"__method_Writer_write":      true,
	"__method_Reader_read_chunk": true,
	"open_reader":                true,
	"open_writer":                true,
	"args":                       true,
	"env":                        true,
	"stat":                       true,
	"lstat":                      true,
	"access":                     true,
	"getcwd":                     true,
	"read_link":                  true,
	"chdir":                      true,
	"chroot":                     true,
	"setuid":                     true,
	"setgid":                     true,
	"setgroups":                  true,
	"create_dir":                 true,
	"remove_dir":                 true,
	"create_link":                true,
	"create_symlink":             true,
	"rename":                     true,
	"chmod":                      true,
	"chmod_at":                   true,
	"truncate":                   true,
	"mknod":                      true,
	"chown_at":                   true,
	"__method_Reader_seek":       true,
	"__method_Reader_splice_to":  true,
	"__method_Writer_seek":       true,
	"__method_Writer_flags":      true,
	"__method_Reader_flags":      true,
	"__method_Writer_write_some": true,
	"__method_Writer_truncate":   true,
	"__method_Reader_fsync":      true,
	"__method_Writer_fsync":      true,
	"__method_Reader_fdatasync":  true,
	"__method_Writer_fdatasync":  true,
	"__method_Reader_syncfs":     true,
	"__method_Writer_syncfs":     true,
	"__method_Writer_dup_onto":   true,
	"sync":                       true,
	"open_appender":              true,
	"open_exclusive":             true,
	"open_reader_with":           true,
	"open_writer_with":           true,
	"environ":                    true,
	"read_dir_all":               true,
	"statfs":                     true,
	"uname_field":                true,
	"getgroups":                  true,
	"signal_send":                true,
	"set_process_group":          true,
	"set_priority":               true,
	"proc_exec":                  true,
	"proc_exec_as":               true,
	"window_size":                true,
	"set_window_size":            true,
	"termios_get":                true,
	"termios_set":                true,
	"set_file_times":             true,
	"__method_Reader_stat":       true,
	"__method_Writer_stat":       true,
	"read_file":                  true,
	"read_file_bytes":            true,
	"write_file":                 true,
	"remove_file":                true,
	"temp_dir":                   true,
	"random_bytes":               true,
	"__alloc":                    true,
	"__method_Reader_read_line":  true,
	"read_line":                  true,
	"read_dir":                   true,
	"tcp_recv":                   true,
	"poll":                       true,
	"hostname":                   true,
	"create_dir_all":             true,
	"__fern_heap_bump_bytes":     true,
	"__fern_heap_alloc_count":    true,
}

// runtimeHelperDeps records the helper→helper call edges (a helper that tail-
// calls another must have that callee emitted too — the module never references
// it directly). Transitively closed by referencedRuntimeHelpers.
var runtimeHelperDeps = map[string][]string{
	"poll":                            {"__alloc", "__free"},
	"strbuf_append":                   {"__alloc", "__free"},
	"strbuf_take":                     {"__alloc"},
	"__method_string_as_bytes":        {"__slice_make"},
	"__fern_closure_drop":             {"__fern_box_free", "__fern_rc_dec"},
	"__fern_arr_push_grow_ptr":        {"__fern_arr_push_grow", "__fern_rc_inc"},
	"__fern_arr_push_grow_str":        {"__fern_arr_push_grow", "__fern_rc_inc"},
	"__fern_arr_push_grow_move_ptr":   {"__fern_arr_push_grow", "__fern_rc_inc"},
	"__fern_arr_push_grow_move_str":   {"__fern_arr_push_grow", "__fern_rc_inc"},
	"__fern_arr_cow_inplace_ptr":      {"__fern_arr_cow_inplace", "__fern_rc_inc"},
	"__fern_arr_cow_inplace_str":      {"__fern_arr_cow_inplace", "__fern_rc_inc"},
	"__fern_str_dec":                  {"__free"},
	"__fern_drop_arr_str":             {"__fern_str_dec", "__fern_arr_dec"},
	"__fern_drop_arr_ptr":             {"__fern_rc_dec", "__fern_arr_dec"},
	"__fern_map_drop":                 {"__free"},
	"__fern_box_free":                 {"__free"},
	"__fern_arr_dec":                  {"__free"},
	"__alloc_reuse":                   {"__free", "__alloc"},
	"__fern_str_append":               {"__str_concat", "__fern_str_dec"},
	"__alloc_u8":                      {"__alloc"},
	"__fern_scale_f64":                {"__alloc"},
	"proc_exec_as":                    {"__alloc"},
	"proc_exec":                       {"__alloc"},
	"__fern_map_hash_seed":            {"random_i32"},
	"remove_dir_all":                  {"__fern_io_error"},
	"__method_Reader_close":           {"__fern_io_error"},
	"__method_Writer_close":           {"__fern_io_error"},
	"__method_Writer_write":           {"__fern_io_error"},
	"__method_Reader_read_chunk":      {"__fern_io_error"},
	"open_reader":                     {"__fern_io_error", "__fern_rc_inc"},
	"stat":                            {"__fern_io_error", "__fern_rc_inc"},
	"lstat":                           {"__fern_io_error", "__fern_rc_inc"},
	"access":                          {"__fern_io_error", "__fern_rc_inc"},
	"read_link":                       {"__fern_io_error", "__fern_rc_inc"},
	"chdir":                           {"__fern_io_error", "__fern_rc_inc"},
	"chroot":                          {"__fern_io_error", "__fern_rc_inc"},
	"setuid":                          {"__fern_io_error"},
	"setgid":                          {"__fern_io_error"},
	"setgroups":                       {"__fern_io_error", "__free"},
	"create_dir":                      {"__fern_io_error", "__fern_rc_inc"},
	"remove_dir":                      {"__fern_io_error", "__fern_rc_inc"},
	"create_link":                     {"__fern_io_error", "__fern_rc_inc"},
	"create_symlink":                  {"__fern_io_error", "__fern_rc_inc"},
	"rename":                          {"__fern_io_error", "__fern_rc_inc"},
	"chmod":                           {"__fern_io_error", "__fern_rc_inc"},
	"chmod_at":                        {"__fern_io_error", "__fern_rc_inc"},
	"truncate":                        {"__fern_io_error", "__fern_rc_inc"},
	"mknod":                           {"__fern_io_error", "__fern_rc_inc"},
	"chown_at":                        {"__fern_io_error", "__fern_rc_inc"},
	"__method_Reader_seek":            {"__fern_io_error"},
	"__method_Reader_splice_to":       {"__fern_io_error"},
	"__method_Writer_seek":            {"__fern_io_error"},
	"__method_Writer_flags":           {"__fern_io_error"},
	"__method_Reader_flags":           {"__fern_io_error"},
	"__method_Writer_write_some":      {"__fern_io_error"},
	"__method_Writer_truncate":        {"__fern_io_error"},
	"__method_Reader_fsync":           {"__fern_io_error"},
	"__method_Writer_fsync":           {"__fern_io_error"},
	"__method_Reader_fdatasync":       {"__fern_io_error"},
	"__method_Writer_fdatasync":       {"__fern_io_error"},
	"__method_Reader_syncfs":          {"__fern_io_error"},
	"__method_Writer_syncfs":          {"__fern_io_error"},
	"__method_Writer_dup_onto":        {"__fern_io_error"},
	"open_appender":                   {"__fern_io_error", "__fern_rc_inc"},
	"open_exclusive":                  {"__fern_io_error", "__fern_rc_inc"},
	"open_reader_with":                {"__fern_io_error", "__fern_rc_inc"},
	"open_writer_with":                {"__fern_io_error", "__fern_rc_inc"},
	"read_dir_all":                    {"__fern_io_error", "__fern_rc_inc"},
	"statfs":                          {"__fern_io_error", "__fern_rc_inc"},
	"signal_send":                     {"__fern_io_error", "__fern_rc_inc"},
	"set_process_group":               {"__fern_io_error", "__fern_rc_inc"},
	"set_priority":                    {"__fern_io_error", "__fern_rc_inc"},
	"window_size":                     {"__fern_io_error", "__fern_rc_inc"},
	"set_window_size":                 {"__fern_io_error", "__fern_rc_inc"},
	"termios_get":                     {"__fern_io_error", "__fern_rc_inc"},
	"termios_set":                     {"__fern_io_error", "__fern_rc_inc"},
	"set_file_times":                  {"__fern_io_error", "__fern_rc_inc"},
	"__method_Reader_window_size":     {"window_size"},
	"__method_Reader_set_window_size": {"set_window_size"},
	"__method_Reader_termios_get":     {"termios_get"},
	"__method_Reader_termios_set":     {"termios_set"},
	"__method_Reader_stat":            {"__fern_io_error"},
	"__method_Writer_stat":            {"__fern_io_error"},
	"open_writer":                     {"__fern_io_error", "__fern_rc_inc"},
	"read_file":                       {"__fern_io_error", "__fern_utf8_valid", "__fern_rc_inc"},
	"read_file_bytes":                 {"__fern_io_error", "__alloc_u8", "__fern_rc_inc"},
	"write_file":                      {"__fern_io_error", "__fern_rc_inc"},
	"remove_file":                     {"__fern_io_error", "__fern_rc_inc"},
	"temp_dir":                        {"__fern_io_error", "__fern_rc_inc"},
	"random_bytes":                    {"__alloc_u8"},
	"read_dir":                        {"__fern_io_error", "__fern_rc_inc"},
	"tcp_recv":                        {"__alloc_u8"},
	"__method_Reader_isatty":          {"isatty"},
	"__method_Writer_isatty":          {"isatty"},
	"create_dir_all":                  {"__fern_io_error", "__fern_rc_inc"},
	"__fern_buf_reserve":              {"__fern_box_free"},
	"buf_push":                        {"__fern_buf_reserve"},
	"buf_push_range":                  {"__fern_buf_reserve"},
	"buf_push_mapped":                 {"__fern_buf_reserve"},
	"buf_push_filtered":               {"__fern_buf_reserve"},
	"buf_push_expanded":               {"__fern_buf_reserve"},
	"buf_push_byte":                   {"__fern_buf_reserve"},
	"buf_push_u64":                    {"__fern_buf_reserve"},
	"buf_free":                        {"__fern_box_free"},
}

// emitRuntimeHelpers writes the named helper bodies, each at a 16-byte
// boundary. The alignment is the point: these are the smallest and hottest
// routines in an rc-carrying program — `__fern_rc_inc` / `_dec` run once per
// rc op — so a helper's entry address decides whether its body shares one
// 32-byte instruction-fetch window. Letting that fall out of wherever the
// preceding helper happened to end is a hazard: removing two subsumed
// instructions from an EARLIER helper doubled examples/bench/string_rfind_byte,
// 61 ms to 122 ms, without changing one instruction that program runs (#8193).
func emitRuntimeHelpers(w func(string, ...any), helpers []string) {
	counting := countsAllocs(helpers)
	for _, h := range helpers {
		// Column 0, like every other directive the backends emit: the
		// instruction counters (scripts/perf-bench, the SSA CLI gate) count
		// indented lines, so an indented directive would read as an
		// instruction the program does not execute.
		w(".p2align 4")
		if counting && h == "__alloc" {
			emitAllocHelperCounting(w)
			continue
		}
		runtimeHelperEmitters[h](w)
	}
}

// countsAllocs reports whether this module's allocations are counted, so
// __alloc must tick the counter and the inline pop must not hand out a block
// the count never saw. Two things ask for it: the leak census (#9604), which
// counts every alloc and free, and a program reading __heap_alloc_count()
// (#9596). No other program pays for the tick.
func countsAllocs(helpers []string) bool {
	return ast.LeakCheckEnabled || referencesHelper(helpers, "__fern_heap_alloc_count")
}

// referencedRuntimeHelpers returns, sorted, the hand-written runtime-helper
// names to append to .text — every helper any emitted program calls, plus the
// transitive closure of their helper→helper dependencies (runtimeHelperDeps)
// — and separately the helpers written in Fern (internal/fernrt) reached the
// same way that progs does not yet hold.
func referencedRuntimeHelpers(progs map[string]*Program) (asm, fern []string) {
	seen := map[string]bool{}
	fernSeen := map[string]bool{}
	var add func(name string)
	add = func(name string) {
		if seen[name] || fernSeen[name] {
			return
		}
		if fernrt.Has(name) {
			if progs[name] == nil {
				fernSeen[name] = true
			}
			return
		}
		if runtimeHelperEmitters[name] == nil {
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
				if (in.Op == Call || in.Op == CallPair) && !inlinedCall(in) {
					add(in.Callee)
				}
			}
		}
	}
	for n := range seen {
		asm = append(asm, n)
	}
	sort.Strings(asm)
	for n := range fernSeen {
		fern = append(fern, n)
	}
	sort.Strings(fern)
	return asm, fern
}

// liftFernHelper lowers one internal/fernrt helper through the same lift,
// optimise and emit steps as a program function, so it lands in the module
// under fnLabel(name).
func liftFernHelper(name string, numAlloc int) (*Program, error) {
	_, irFn, err := fernrt.Func(name, 8)
	if err != nil {
		return nil, err
	}
	f, err := ssa.LiftFromIRWith(irFn, ir.NewCallShapes(&ir.Program{Funcs: []*ir.Func{irFn}}))
	if err != nil {
		return nil, fmt.Errorf("x86_64ssa: lift %q: %w", name, err)
	}
	ssa.Optimize(f)
	if err := ssa.Verify(f); err != nil {
		return nil, fmt.Errorf("x86_64ssa: verify %q: %w", name, err)
	}
	ssa.ResolveWidths(map[string]*ssa.Func{name: f})
	p, err := Emit(f, numAlloc)
	if err != nil {
		return nil, fmt.Errorf("x86_64ssa: emit %q: %w", name, err)
	}
	if p.NumRegFile > len(gpRegs) {
		return nil, fmt.Errorf("x86_64ssa: %q needs %d registers but only %d are available", name, p.NumRegFile, len(gpRegs))
	}
	return p, nil
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
	w("\tjz .Lssa_rcdec_underflow")
	w("\tsub eax, 1")
	w("\tmov %s, eax", memRef("rdi", -8))
	w(".Lssa_rcdec_ret:")
	rcPassThroughRet(w)
	// Releasing an already-zero count is an over-release. Count it and leave
	// the count alone rather than wrapping it to the static sentinel, which
	// would turn one bug into an immortal object.
	w(".Lssa_rcdec_underflow:")
	w("\tadd dword ptr [rip + %s], 1", rcUnderflowSym)
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

// emitArrDecHelper writes __fern_arr_dec(data, stride) -> data: the array
// drop the IR inserts at scope exit. The data pointer sits past a header of
// max(16, stride) bytes with cap@-12, rc@-8 and len@-4. Guarded (null,
// low address, static sentinel); on the last reference (rc == 1) the buffer
// goes back to the freelist (base = data - headerBytes, headerBytes +
// cap*stride bytes) — the elements are not walked, the __fern_drop_arr_*
// wrappers do that first; rc > 1 drops a shared reference. rbx = data across
// the free.
func emitArrDecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_arr_dec"))
	w("\tmov rax, rdi")
	w("\tcmp rdi, 0x10000")
	w("\tjb .Lssa_arrdec_ret")
	w("\tmov ecx, %s", memRef("rdi", -8)) // rc
	w("\ttest ecx, ecx")
	w("\tjle .Lssa_arrdec_ret") // static sentinel, or already dropped
	w("\tcmp ecx, 1")
	w("\tje .Lssa_arrdec_free")
	w("\tsub ecx, 1")
	w("\tmov %s, ecx", memRef("rdi", -8))
	w(".Lssa_arrdec_ret:")
	w("\tret")
	w(".Lssa_arrdec_free:")
	w("\tpush rbx") // one push past the return address: 16-aligned for the call
	w("\tmov rbx, rdi")
	w("\tmov esi, esi")
	w("\tmov ecx, 16")
	w("\tcmp rsi, 16")
	w("\tcmova rcx, rsi")                  // headerBytes = max(16, stride)
	w("\tmov edx, %s", memRef("rdi", -12)) // cap
	w("\timul rsi, rdx")
	w("\tadd rsi, rcx") // size = cap*stride + headerBytes
	w("\tsub rdi, rcx") // base = data - headerBytes
	w("\tcall %s", fnLabel("__free"))
	w("\tmov rax, rbx")
	w("\tpop rbx")
	w("\tret")
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
			w("\tjmp %s", abortKindForIdx(name, false))
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
// (length at [ptr-4]), then asks __ssa_mismatch where the bytes first differ.
// The IR lowers `a == b` on strings (OpStrEq) to a call here.
func emitStrEqHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_eq"))
	w("\tcmp rdi, rsi")
	w("\tje .Lssa_streq_eq")              // same pointer → equal
	w("\tmov ecx, %s", memRef("rdi", -4)) // len a
	w("\tmov edx, %s", memRef("rsi", -4)) // len b
	w("\tcmp ecx, edx")
	w("\tjne .Lssa_streq_neq") // different lengths
	w("\tcall %s", mismatchSym)
	w("\tcmp rax, rdx")
	w("\tjne .Lssa_streq_neq") // a byte differs before the end
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
// the FIRST difference, which __ssa_mismatch finds over the shorter length.
// Lengths live at [ptr-4].
func emitStrOrdHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_ord"))
	w("\tmov r10d, %s", memRef("rdi", -4)) // la
	w("\tmov r11d, %s", memRef("rsi", -4)) // lb
	w("\tmov edx, r10d")                   // n = min(la, lb)
	w("\tcmp r11d, edx")
	w("\tjae .Lssa_strord_n")
	w("\tmov edx, r11d")
	w(".Lssa_strord_n:")
	w("\tcall %s", mismatchSym)
	w("\tcmp rax, rdx")
	w("\tjae .Lssa_strord_len")
	w("\tmovzx ecx, byte ptr [rdi + rax]")
	w("\tmovzx r8d, byte ptr [rsi + rax]")
	w("\tmov eax, ecx")
	w("\tsub eax, r8d")
	w("\tmovsx rax, eax")
	w("\tret")
	w(".Lssa_strord_len:")
	w("\tmov eax, r10d")
	w("\tsub eax, r11d")
	w("\tmovsx rax, eax")
	w("\tret")
}

// mismatchSym names the shared first-difference scan __str_eq and __str_ord
// route through: __ssa_mismatch(rdi=a, rsi=b, rdx=n) -> rax = the index of
// the first byte at which the two n-byte ranges differ, or n when they are
// equal. Internal like __ssa_bcopy, so a bare symbol.
const mismatchSym = "__ssa_mismatch"

// emitMismatch writes __ssa_mismatch: 32 bytes an iteration with AVX2 while
// 32 remain, 16 with SSE2 while 16 remain, 8 with a general register while 8
// remain, then bytes, mirroring the flat backend's __fern_mismatch. A hit's
// index comes from the first clear bit of the compare mask (or the first set
// bit of the xor for the word step). It clobbers rax, rcx, r8 and r9 and
// the two vector registers, and leaves rdi, rsi and rdx as they came, so a
// caller keeps its operands across the call. Leaf.
func emitMismatch(w func(string, ...any)) {
	w("")
	w("%s:", mismatchSym)
	w("\txor ecx, ecx") // cursor
	w(".Lssa_mm_avx:")
	w("\tmov rax, rdx")
	w("\tsub rax, rcx")
	w("\tcmp rax, 32")
	w("\tjl .Lssa_mm_avx_done")
	w("\tvmovdqu ymm0, [rdi + rcx]")
	w("\tvmovdqu ymm1, [rsi + rcx]")
	w("\tvpcmpeqb ymm0, ymm0, ymm1")
	w("\tvpmovmskb eax, ymm0")
	w("\tcmp eax, -1")
	w("\tjne .Lssa_mm_hit32")
	w("\tadd rcx, 32")
	w("\tjmp .Lssa_mm_avx")
	w(".Lssa_mm_hit32:")
	w("\tvzeroupper")
	w("\tnot eax")
	w("\tbsf eax, eax") // the first lane that differs
	w("\tadd rax, rcx")
	w("\tret")
	w(".Lssa_mm_avx_done:")
	w("\tvzeroupper")
	w(".Lssa_mm_vec:")
	w("\tmov rax, rdx")
	w("\tsub rax, rcx")
	w("\tcmp rax, 16")
	w("\tjl .Lssa_mm_word")
	w("\tmovdqu xmm0, [rdi + rcx]")
	w("\tmovdqu xmm1, [rsi + rcx]")
	w("\tpcmpeqb xmm0, xmm1")
	w("\tpmovmskb eax, xmm0")
	w("\tcmp eax, 65535")
	w("\tjne .Lssa_mm_hit16")
	w("\tadd rcx, 16")
	w("\tjmp .Lssa_mm_vec")
	w(".Lssa_mm_hit16:")
	w("\tnot eax") // the top half sets too, above every lane the branch guarantees below
	w("\tbsf eax, eax")
	w("\tadd rax, rcx")
	w("\tret")
	w(".Lssa_mm_word:")
	w("\tmov rax, rdx")
	w("\tsub rax, rcx")
	w("\tcmp rax, 8")
	w("\tjl .Lssa_mm_tail")
	w("\tmov r8, [rdi + rcx]")
	w("\tmov r9, [rsi + rcx]")
	w("\tcmp r8, r9")
	w("\tjne .Lssa_mm_hit8")
	w("\tadd rcx, 8")
	w("\tjmp .Lssa_mm_word")
	w(".Lssa_mm_hit8:")
	w("\txor r8, r9")
	w("\tbsf r8, r8") // the first differing bit, little-endian: its byte is the first differing byte
	w("\tshr r8, 3")
	w("\tlea rax, [rcx + r8]")
	w("\tret")
	w(".Lssa_mm_tail:")
	w("\tcmp rcx, rdx")
	w("\tjae .Lssa_mm_eq")
	w("\tmovzx r8d, byte ptr [rdi + rcx]")
	w("\tmovzx r9d, byte ptr [rsi + rcx]")
	w("\tcmp r8d, r9d")
	w("\tjne .Lssa_mm_hitb")
	w("\tadd rcx, 1")
	w("\tjmp .Lssa_mm_tail")
	w(".Lssa_mm_hitb:")
	w("\tmov rax, rcx")
	w("\tret")
	w(".Lssa_mm_eq:")
	w("\tmov rax, rdx")
	w("\tret")
}

// usesMismatch reports whether the module needs __ssa_mismatch: the two
// string comparisons call it.
func usesMismatch(helpers []string) bool {
	for _, h := range helpers {
		if h == "__str_eq" || h == "__str_ord" {
			return true
		}
	}
	return false
}

// bcopySym names the shared forward byte copy every allocating helper routes
// through. It is internal — the IR never calls it — so it carries a bare symbol
// rather than an fnLabel, the way __ssa_heap_guard does.
const bcopySym = "__ssa_bcopy"

// emitBcopy writes __ssa_bcopy(rdi=dst, rsi=src, rdx=n): a forward copy of n
// bytes. Regions must not overlap.
//
// Below 64 bytes the copy is a loop of 8-byte moves with an overlapping
// 8-byte tail, or a byte loop under 8: `rep movsb` costs about 35 cycles to
// start on the declared Haswell-class baseline whatever the length, and
// nearly every copy this backend makes is a string of a few bytes (a
// `to_string`, a slice, an appended fragment). Paying the startup on each of
// them was 1.7x the flat build on `to_string` and 1.6x on `struct_drop`. From
// 64 bytes `rep movsb` is the fast path (ERMSB) and takes over, which is why
// there is no size-classed SSE2 copy like the native __fern_memcpy.
//
// It clobbers rdi, rsi and rcx, which leave as `rep movsb` leaves them (both
// pointers advanced by n, rcx zero), and the flags. All are caller-saved and
// dead at every call site, each of which passes its own arguments. `cld` is
// one byte of insurance on the rep path: System V guarantees DF is clear at
// every call boundary and nothing in this backend sets it, but a copy running
// backwards would corrupt the heap silently rather than fault.
func emitBcopy(w func(string, ...any)) {
	w("")
	w("%s:", bcopySym)
	w("\tcmp rdx, %d", bcopyRepFrom)
	w("\tjae .Lssa_bcopy_rep")
	w("\tpush rax")
	w("\txor ecx, ecx")
	w("\tcmp rdx, 8")
	w("\tjb .Lssa_bcopy_bytes")
	w(".Lssa_bcopy_words:")
	w("\tmov rax, [rsi + rcx]")
	w("\tmov [rdi + rcx], rax")
	w("\tadd rcx, 8")
	w("\tmov rax, rdx")
	w("\tsub rax, rcx")
	w("\tcmp rax, 8")
	w("\tjae .Lssa_bcopy_words")
	w("\tmov rax, [rsi + rdx - 8]") // the last 8 bytes, overlapping what the loop wrote
	w("\tmov [rdi + rdx - 8], rax")
	w("\tjmp .Lssa_bcopy_done")
	w(".Lssa_bcopy_bytes:")
	w("\tcmp rcx, rdx")
	w("\tjae .Lssa_bcopy_done")
	w("\tmov al, [rsi + rcx]")
	w("\tmov [rdi + rcx], al")
	w("\tadd rcx, 1")
	w("\tjmp .Lssa_bcopy_bytes")
	w(".Lssa_bcopy_done:")
	w("\tpop rax")
	w("\tadd rdi, rdx")
	w("\tadd rsi, rdx")
	w("\txor ecx, ecx")
	w("\tret")
	w(".Lssa_bcopy_rep:")
	w("\tcld")
	w("\tmov rcx, rdx")
	w("\trep movsb")
	w("\tret")
}

// bcopyRepFrom is the length from which __ssa_bcopy and __ssa_bfill use the
// rep string instructions; below it their startup cost exceeds the loop.
const bcopyRepFrom = 64

// bfillSym names the shared byte fill: __ssa_bfill(rdi=dst, eax=byte, rcx=n)
// writes n copies of the low byte of eax at dst, the same way __ssa_bcopy
// copies: 8-byte stores of the replicated byte below bcopyRepFrom, `rep
// stosb` from there. Internal like __ssa_bcopy. It clobbers rdi (advanced by
// n), rcx (zero), rax (the replicated byte) and the flags.
const bfillSym = "__ssa_bfill"

func emitBfill(w func(string, ...any)) {
	w("")
	w("%s:", bfillSym)
	w("\tcmp rcx, %d", bcopyRepFrom)
	w("\tjae .Lssa_bfill_rep")
	w("\tmovzx eax, al")
	w("\tpush rdx")
	w("\tmov rdx, 72340172838076673") // 0x0101010101010101: the byte in every lane
	w("\timul rax, rdx")
	w("\tmov rdx, rcx") // n
	w("\txor ecx, ecx")
	w("\tcmp rdx, 8")
	w("\tjb .Lssa_bfill_bytes")
	w(".Lssa_bfill_words:")
	w("\tmov [rdi + rcx], rax")
	w("\tadd rcx, 8")
	w("\tsub rdx, 8")
	w("\tcmp rdx, 8")
	w("\tjae .Lssa_bfill_words")
	w("\tadd rcx, rdx")
	w("\tmov [rdi + rcx - 8], rax") // the last 8 bytes, overlapping what the loop wrote
	w("\tjmp .Lssa_bfill_done")
	w(".Lssa_bfill_bytes:")
	w("\ttest rdx, rdx")
	w("\tjz .Lssa_bfill_done")
	w("\tmov [rdi + rcx], al")
	w("\tadd rcx, 1")
	w("\tsub rdx, 1")
	w("\tjmp .Lssa_bfill_bytes")
	w(".Lssa_bfill_done:")
	w("\tadd rdi, rcx")
	w("\txor ecx, ecx")
	w("\tpop rdx")
	w("\tret")
	w(".Lssa_bfill_rep:")
	w("\tcld")
	w("\trep stosb")
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
	"strbuf_append":               true,
	"strbuf_take":                 true,
	"string_from_bytes_unchecked": true,
	"__str_slice":                 true,
	"__str_concat":                true,
	"__fern_str_append":           true,
	"__fern_arr_push_grow":        true,
	"__fern_arr_cow_inplace":      true,
	"__fern_buf_reserve":          true,
	"buf_push":                    true,
	"buf_push_range":              true,
	"__memcpy":                    true,
	"read_file":                   true,
	"read_file_bytes":             true,
	"temp_dir":                    true,
	"__method_Reader_read_line":   true,
	"read_line":                   true,
	"read_dir":                    true,
	"read_dir_all":                true,
	"hostname":                    true,
	"uname_field":                 true,
	"getcwd":                      true,
	"read_link":                   true,
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

// usesBfill reports whether the module needs __ssa_bfill: the two helpers
// that fill a block byte by byte call it.
func usesBfill(helpers []string) bool {
	for _, h := range helpers {
		if h == "__alloc_u8" || h == "__memset" {
			return true
		}
	}
	return false
}

// emitMemcpyHelper writes __memcpy(dst, src, n) -> dst through __ssa_bcopy.
// std/string.fern's bytes() and core/map.fern's buffer moves call it; n is a
// non-negative i32.
func emitMemcpyHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__memcpy"))
	w("\tmov rax, rdi")
	w("\tmov edx, edx")
	emitBcopyCall(w, "rdi", "rsi", "rdx")
	w("\tret")
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
	// A negative n is a length computation that overflowed i32 (#8457): the
	// header add wraps it small, the bump hands back an undersized block, and
	// the zero-fill below then writes the unwrapped count past it.
	w("\ttest edi, edi")
	w("\tjs .Lssa_allocu8_len_overflow")
	w("\tpush rbx")            // one push past the return address: 16-aligned for the call
	w("\tmov ebx, edi")        // n
	w("\tlea edi, [rbx + 16]") // allocSize = n + header
	w("\tcall %s", fnLabel("__alloc"))
	w("\tadd rax, 16")                   // data
	w("\tmov dword ptr [rax - 12], ebx") // cap = n
	w("\tmov dword ptr [rax - 8], 1")    // rc = 1
	w("\tmov dword ptr [rax - 4], ebx")  // len = n
	// Zero the payload — a popped block carries its last contents. The fill
	// writes through rdi and consumes rcx and rax, so the return value is
	// parked in r10 for the duration.
	w("\tmov r10, rax")
	w("\tmov rdi, rax")
	w("\tmov ecx, ebx")
	w("\txor eax, eax")
	w("\tcall %s", bfillSym)
	w("\tmov rax, r10")
	w("\tpop rbx")
	w("\tret")
	w(".Lssa_allocu8_len_overflow:")
	w("\tjmp %s", abortAllocSize)
}

// emitStringFromBytesHelper writes string_from_bytes_unchecked(bs) -> data: copy
// a u8[] payload into a fresh string and return its data pointer — the
// round-trip companion to s.bytes().
//
// Strings here are single-word and rc-headered (rc=1@base+0, len@base+4,
// data@base+8, the layout ConstStr and __str_concat already use) with no
// small-string inline form, so this is an allocation and a copy. The native
// twin spends most of its body deciding whether the result fits in seven inline
// bytes and packing it if so; none of that survives the representation change.
// rdi=bs (the u8[] data pointer, its length at [bs-4]), returns rax=data.
func emitStringFromBytesHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("string_from_bytes_unchecked"))
	w("\tmov esi, %s", memRef("rdi", -4))     // len (a 32-bit write zero-extends)
	w("\tlea r11, [rsi + %d]", strBlockBytes) // header + len + NUL
	w("\tsub rsp, 8")                         // entered 8 past alignment; the trampoline is called at 16
	ssaBumpAlloc(w, "r8", "r11")
	w("\tadd rsp, 8")
	w("\tmov dword ptr [r8], 1") // rc = 1
	w("\tmov [r8 + 4], esi")     // len
	w("\tlea r10, [r8 + 8]")     // data
	emitBcopyCall(w, "r10", "rdi", "rsi")
	w("\tmov rax, r10")
	w("\tret")
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
	// A new_len+strBlockBytes block: rc=1@base, len@base+4, data@base+8.
	w("\tlea r11, [r9 + %d]", strBlockBytes)
	w("\tsub rsp, 8") // entered 8 past alignment; the trampoline is called at 16
	ssaBumpAlloc(w, "r10", "r11")
	w("\tadd rsp, 8")
	w("\tmov dword ptr [r10], 1") // rc = 1
	w("\tmov [r10 + 4], r9d")     // len
	w("\tlea r11, [r10 + 8]")     // data
	w("\tlea rax, [rdi + rsi]")   // src = base + low
	emitBcopyCall(w, "r11", "rax", "r9")
	w("\tmov rax, r11")
	w("\tret")
	w(".Lssa_strslice_trap:")
	w("\tjmp %s", abortStrSlice)
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
	w("\tmov edx, edx") // stride is an i32: clear the high half for the 64-bit products below
	w("\tmov r8d, esi")
	w("\tadd r8d, 1") // newLen
	// Sized in 64 bits: a 32-bit doubling goes negative past 2^30 elements
	// (the floor would then pick cap = 4 under a length near 1e9) and the
	// product wraps past 4 GiB. Array sizes are i32, so a total past 2^31 - 1
	// is refused.
	w("\tmov r9d, r8d")
	w("\tshl r9, 1")
	w("\tcmp r9, 4")
	w("\tjge .Lssa_apg_cap_ok")
	w("\tmov r9d, 4") // newCap = max(2*newLen, 4)
	w(".Lssa_apg_cap_ok:")
	w("\tmov r10d, 16")
	w("\tcmp edx, 16")
	w("\tjle .Lssa_apg_hdr_ok")
	w("\tmov r10d, edx") // headerBytes = max(stride, 16)
	w(".Lssa_apg_hdr_ok:")
	w("\tmov r11, r9")
	w("\timul r11, rdx")
	w("\tadd r11, r10") // allocSize = headerBytes + newCap*stride
	w("\tcmp r11, 2147483647")
	w("\tja .Lssa_apg_sizebad")
	w("\tsub rsp, 8")             // entered 8 past alignment; the trampoline is called at 16
	ssaBumpAlloc(w, "rax", "r11") // base; every other register survives
	w("\tadd rsp, 8")
	w("\tmov r11, rax")
	w("\tadd r11, r10")               // new_data = base + headerBytes
	w("\tmov [r11 - 12], r9d")        // cap = newCap
	w("\tmov dword ptr [r11 - 8], 1") // rc = 1
	w("\tmov [r11 - 4], r8d")         // len = newLen
	w("\tmov eax, esi")
	w("\timul rax, rdx") // nbytes = oldLen*stride
	emitBcopyCall(w, "r11", "rdi", "rax")
	w("\tmov rax, r11")
	w("\tret")
	w(".Lssa_apg_sizebad:")
	w("\tjmp %s", abortAllocSize)
}

// emitArrPushGrowElemHelper returns the emitter for the element-retaining
// siblings of __fern_arr_push_grow, for arrays of rc-tracked pointers
// (single-word strings included, which is why the _str spellings share it):
// the same grow, then on the copy path a __fern_rc_inc over the oldLen copied
// elements, so the fresh buffer owns a reference to every element it shares
// with the old one. Without it the copy leaves the elements at unchanged
// count, and the old buffer's release then drops what the new one still
// holds — invisible until the freelist hands the element's block out again.
//
// moveForm is the self-append `a = a.append(v)` contract: the old buffer is
// about to be released without an element walk, so at rc == 1 the copy
// inherits its references and retaining would leak one per element per
// grow; the retain happens only when rc != 1, when an alias keeps the old
// buffer alive. Mirrors the flat backend's helpers. rbx = arr then the index,
// r12 = oldLen, r13 = stride, r14 = the rc before the grow, r15 = new_data.
func emitArrPushGrowElemHelper(name, tag string, moveForm bool) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		lbl := func(suffix string) string { return ".Lssa_" + tag + "_" + suffix }
		w("")
		w("%s:", fnLabel(name))
		w("\tpush rbx")
		w("\tpush r12")
		w("\tpush r13")
		w("\tpush r14")
		w("\tpush r15") // five pushes past the return address: 16-aligned for the calls
		w("\tmov rbx, rdi")
		w("\tmov r12d, esi")
		w("\tmov r13d, edx")
		w("\tmov r14d, %s", memRef("rdi", -8)) // rc before the grow
		w("\tcall %s", fnLabel("__fern_arr_push_grow"))
		w("\tmov r15, rax")
		w("\tcmp r15, rbx")
		w("\tje %s", lbl("done")) // grown in place: no copy, nothing to retain
		if moveForm {
			w("\tcmp r14d, 1")
			w("\tje %s", lbl("done")) // the copy inherits a sole owner's references
		}
		w("\txor ebx, ebx")
		w("%s:", lbl("loop"))
		w("\tcmp ebx, r12d")
		w("\tjge %s", lbl("done"))
		w("\tmov rax, rbx")
		w("\timul rax, r13")
		w("\tmov rdi, [r15 + rax]") // element i
		w("\tcall %s", fnLabel("__fern_rc_inc"))
		w("\tadd ebx, 1")
		w("\tjmp %s", lbl("loop"))
		w("%s:", lbl("done"))
		w("\tmov rax, r15")
		w("\tpop r15")
		w("\tpop r14")
		w("\tpop r13")
		w("\tpop r12")
		w("\tpop rbx")
		w("\tret")
	}
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
	w("\tadd r11d, r10d")         // allocSize = headerBytes + cap*stride
	w("\tsub rsp, 8")             // entered 8 past alignment; the trampoline is called at 16
	ssaBumpAlloc(w, "rax", "r11") // base; every other register survives
	w("\tadd rsp, 8")
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
// All four are SSE2, 16 bytes an iteration, the same block algorithms the
// native backend and arm64ssa run (docs/ATLAS-PLATFORM-PLAN.md §3). What paid
// for the vectorising was a gate rather than a decision to go faster: the
// flat-vs-ssa ratio gate (#8069) named memchr as a 20x divergence the moment
// the flat side got quicker, and the length sweep in gas_scan_lengths_test.go
// is what makes a block kernel readable off the page — it walks every length
// across two blocks with the needle at every position.
//
// Two conventions are shared and worth stating once. A byte operand outside
// 0..255 can never occur in the haystack, and ONE unsigned compare covers both
// ends because a negative arrives as a huge unsigned — checked before the loop
// so no iteration pays for it. And `from` CLAMPS rather than trapping, matching
// the interpreter: a forward scan clamps it up to 0, a backward scan clamps it
// down to len-1.

// emitMismatchHelper writes __fern_mismatch(a, ao, b, bo, n) -> the offset of
// the first byte where a[ao..ao+n) and b[bo..bo+n) differ, or n when they are
// equal (#8791). Leaf.
//
// SSE2, mirroring the shipping backend's kernel
// (x86_64.emitMismatchRuntime) minus its AVX2 main loop — this backend's
// helpers stay in the SSE2 baseline the rest of the file uses. The mask means
// EQUAL here where __memchr's means FOUND, so the loop continues while it is
// all-ones and `not` precedes the bsf.
//
// Sub-16 lengths take memcmp's overlapping windows rather than the scalar
// remainder — the leading and trailing 8 (or 4) bytes, which overlap, so two
// loads per operand cover the whole band. A line-oriented utility compares
// SHORT ranges, and walking those a byte at a time is the cost this kernel
// exists to remove.
//
// Strings here are one word with the length at [ptr-4] and no inline form, so
// the five arguments land in rdi/esi/rdx/ecx/r8d with no unpacking.
func emitMismatchHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_mismatch"))
	w("\tmov r9d, %s", memRef("rdi", -4))  // len(a)
	w("\tmov r10d, %s", memRef("rdx", -4)) // len(b)
	// Clamp each offset into [0, len].
	w("\ttest esi, esi")
	w("\tjns .Lssa_fmm_ao_pos")
	w("\txor esi, esi")
	w(".Lssa_fmm_ao_pos:")
	w("\tcmp esi, r9d")
	w("\tjle .Lssa_fmm_ao_ok")
	w("\tmov esi, r9d")
	w(".Lssa_fmm_ao_ok:")
	w("\ttest ecx, ecx")
	w("\tjns .Lssa_fmm_bo_pos")
	w("\txor ecx, ecx")
	w(".Lssa_fmm_bo_pos:")
	w("\tcmp ecx, r10d")
	w("\tjle .Lssa_fmm_bo_ok")
	w("\tmov ecx, r10d")
	w(".Lssa_fmm_bo_ok:")
	// n = min(n, len(a) - ao, len(b) - bo), floored at 0.
	w("\tsub r9d, esi")
	w("\tsub r10d, ecx")
	w("\tcmp r8d, r9d")
	w("\tjle .Lssa_fmm_n_a")
	w("\tmov r8d, r9d")
	w(".Lssa_fmm_n_a:")
	w("\tcmp r8d, r10d")
	w("\tjle .Lssa_fmm_n_b")
	w("\tmov r8d, r10d")
	w(".Lssa_fmm_n_b:")
	w("\ttest r8d, r8d")
	w("\tjns .Lssa_fmm_n_ok")
	w("\txor r8d, r8d")
	w(".Lssa_fmm_n_ok:")
	// rsi / rcx become the range bases, r9 the clamped n (also the answer on
	// equality), r10 the cursor offset.
	w("\tmov esi, esi")
	w("\tmov ecx, ecx")
	w("\tadd rsi, rdi")
	w("\tadd rcx, rdx")
	w("\tmov r9d, r8d")
	w("\txor r10d, r10d")
	// Length dispatch: the sub-16 band never reaches the vector loop, so it
	// gets its own windows rather than a byte walk.
	w("\tcmp r9, 16")
	w("\tjge .Lssa_fmm_vec")
	w("\tcmp r9, 8")
	w("\tjge .Lssa_fmm_w8")
	w("\tcmp r9, 4")
	w("\tjge .Lssa_fmm_w4")
	w("\tjmp .Lssa_fmm_tail")
	// 8..15 bytes: the leading 8 and the trailing 8, which overlap. rdi and
	// rdx are dead by here — both were folded into the range bases above.
	w(".Lssa_fmm_w8:")
	w("\tmov rax, [rsi]")
	w("\txor rax, [rcx]")
	w("\tjnz .Lssa_fmm_w8_lead")
	w("\tmov rdi, r9")
	w("\tsub rdi, 8")
	w("\tmov rax, [rsi + rdi]")
	w("\txor rax, [rcx + rdi]")
	w("\tjz .Lssa_fmm_eq")
	// The leading window proved [0, 8) equal, so a difference the trailing
	// window reports cannot land below 8 — its offset is the first one.
	w("\tbsf rax, rax")
	w("\tshr rax, 3")
	w("\tadd rax, rdi")
	w("\tret")
	w(".Lssa_fmm_w8_lead:")
	// bsf finds the lowest set BIT of the xor; >> 3 turns it into the byte,
	// little-endian, so byte 0 is the low one.
	w("\tbsf rax, rax")
	w("\tshr rax, 3")
	w("\tret")
	// 4..7 bytes: the same pair of windows, four bytes wide.
	w(".Lssa_fmm_w4:")
	w("\tmov eax, [rsi]")
	w("\txor eax, [rcx]")
	w("\tjnz .Lssa_fmm_w4_lead")
	w("\tmov rdi, r9")
	w("\tsub rdi, 4")
	w("\tmov eax, [rsi + rdi]")
	w("\txor eax, [rcx + rdi]")
	w("\tjz .Lssa_fmm_eq")
	w("\tbsf eax, eax")
	w("\tshr eax, 3")
	w("\tadd rax, rdi")
	w("\tret")
	w(".Lssa_fmm_w4_lead:")
	w("\tbsf eax, eax")
	w("\tshr eax, 3")
	w("\tret")
	// SSE2 loop: 16 bytes of each operand per iteration.
	w(".Lssa_fmm_vec:")
	w("\tmov rax, r9")
	w("\tsub rax, r10")
	w("\tcmp rax, 16")
	w("\tjl .Lssa_fmm_tail")
	w("\tmovdqu xmm0, [rsi + r10]")
	w("\tmovdqu xmm1, [rcx + r10]")
	w("\tpcmpeqb xmm0, xmm1")
	w("\tpmovmskb eax, xmm0")
	w("\tcmp eax, 65535")
	w("\tjne .Lssa_fmm_hit16")
	w("\tadd r10, 16")
	w("\tjmp .Lssa_fmm_vec")
	w(".Lssa_fmm_hit16:")
	// pmovmskb writes only the low 16 bits, so `not` sets the top half; the
	// branch above guarantees a clear bit below 16, which is the one bsf
	// finds first.
	w("\tnot eax")
	w("\tbsf eax, eax")
	w("\tadd r10, rax")
	w("\tmov eax, r10d")
	w("\tret")
	// Scalar remainder: fewer than 16 bytes left after the vector loop, or
	// fewer than 4 from the dispatch above.
	w(".Lssa_fmm_tail:")
	w("\tcmp r10, r9")
	w("\tjge .Lssa_fmm_eq")
	w("\tmovzx eax, byte ptr [rsi + r10]")
	w("\tmovzx r11d, byte ptr [rcx + r10]")
	w("\tcmp eax, r11d")
	w("\tjne .Lssa_fmm_tail_hit")
	w("\tinc r10")
	w("\tjmp .Lssa_fmm_tail")
	w(".Lssa_fmm_tail_hit:")
	w("\tmov eax, r10d")
	w("\tret")
	w(".Lssa_fmm_eq:")
	w("\tmov eax, r9d")
	w("\tret")
}

// emitMemchrHelper writes __fern_memchr(s, byte, from) -> the index of the first
// `byte` at or after `from`, or -1. Leaf.
//
// AVX2 32 bytes an iteration then SSE2 16, mirroring the shipping backend's
// kernel (x86_64.emitMemchrRuntime). The scalar loop this replaces was five
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
	// Broadcast the needle across xmm1 with the SSE2 splat.
	w("\tmovd xmm1, esi")
	w("\tpunpcklbw xmm1, xmm1")
	w("\tpunpcklwd xmm1, xmm1")
	w("\tpshufd xmm1, xmm1, 0")
	w("\tvpbroadcastb ymm1, xmm1")
	// 32 bytes an iteration while at least 32 remain; a hit leaves for the
	// caller with the upper halves cleared, as does the fall into the 16-byte
	// legacy-SSE loop.
	w(".Lssa_memchr_avx:")
	w("\tmov eax, r8d")
	w("\tsub eax, edx")
	w("\tcmp eax, 32")
	w("\tjl .Lssa_memchr_avx_done")
	w("\tvmovdqu ymm0, [rdi + rdx]")
	w("\tvpcmpeqb ymm0, ymm0, ymm1")
	w("\tvpmovmskb eax, ymm0")
	w("\ttest eax, eax")
	w("\tjnz .Lssa_memchr_hit32")
	w("\tadd edx, 32")
	w("\tjmp .Lssa_memchr_avx")
	w(".Lssa_memchr_hit32:")
	w("\tvzeroupper")
	w("\tbsf eax, eax")
	w("\tadd eax, edx")
	w("\tret")
	w(".Lssa_memchr_avx_done:")
	w("\tvzeroupper")
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
	w("\tvpbroadcastb ymm1, xmm1")
	// Each iteration covers the 32 bytes ENDING at the cursor, [edx-31, edx],
	// while a whole block fits below it; the 16-byte loop takes over from
	// whatever cursor is left, negative included.
	w(".Lssa_rmemchr_avx:")
	w("\tcmp edx, 31")
	w("\tjl .Lssa_rmemchr_avx_done")
	w("\tlea r9d, [rdx - 31]")
	w("\tvmovdqu ymm0, [rdi + r9]")
	w("\tvpcmpeqb ymm0, ymm0, ymm1")
	w("\tvpmovmskb eax, ymm0")
	w("\ttest eax, eax")
	w("\tjnz .Lssa_rmemchr_hit32")
	w("\tsub edx, 32")
	w("\tjmp .Lssa_rmemchr_avx")
	w(".Lssa_rmemchr_hit32:")
	w("\tvzeroupper")
	w("\tbsr eax, eax") // the LAST match in the block
	w("\tadd eax, r9d")
	w("\tret")
	w(".Lssa_rmemchr_avx_done:")
	w("\tvzeroupper")
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
//
// SSE2, 16 bytes an iteration, and the cheapest of the four: `pmovmskb` gathers
// the top bit of each byte, which IS the "not ASCII" test, so there is no splat
// and no compare — the whole block is movdqu / pmovmskb / test where __memchr
// needs four instructions to broadcast the needle and a pcmpeqb per block.
func emitAsciiRunHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_ascii_run"))
	w("\tmov r8d, %s", memRef("rdi", -4)) // len
	w("\ttest esi, esi")
	w("\tjns .Lssa_ascii_from_ok")
	w("\txor esi, esi") // clamp `from` up to 0
	w(".Lssa_ascii_from_ok:")
	// The cursor is scaled into an address below, so its top half has to be
	// clean; `from` arrives as an i32 and the caller owes nothing about rsi's
	// upper bits. One instruction, once — every later write to esi zeroes the
	// top half itself.
	w("\tmov esi, esi")
	// 32 bytes an iteration while at least 32 remain: vpmovmskb of the raw
	// block IS the test, so the AVX loop needs no splat either.
	w(".Lssa_ascii_avx:")
	w("\tmov eax, r8d")
	w("\tsub eax, esi")
	w("\tcmp eax, 32")
	w("\tjl .Lssa_ascii_avx_done")
	w("\tvmovdqu ymm0, [rdi + rsi]")
	w("\tvpmovmskb eax, ymm0")
	w("\ttest eax, eax")
	w("\tjnz .Lssa_ascii_hit32")
	w("\tadd esi, 32")
	w("\tjmp .Lssa_ascii_avx")
	w(".Lssa_ascii_hit32:")
	w("\tvzeroupper")
	w("\tbsf eax, eax")
	w("\tadd eax, esi")
	w("\tret")
	w(".Lssa_ascii_avx_done:")
	w("\tvzeroupper")
	w(".Lssa_ascii_vec:")
	w("\tmov eax, r8d")
	w("\tsub eax, esi") // bytes left at or after the cursor
	w("\tcmp eax, 16")
	w("\tjl .Lssa_ascii_tail")
	// Unaligned load is deliberate, as in __memchr: at least 16 bytes remain,
	// so the read stays inside the string.
	w("\tmovdqu xmm0, [rdi + rsi]")
	w("\tpmovmskb eax, xmm0")
	w("\ttest eax, eax")
	w("\tjnz .Lssa_ascii_hit")
	w("\tadd esi, 16")
	w("\tjmp .Lssa_ascii_vec")
	w(".Lssa_ascii_hit:")
	// bsf, not tzcnt: tzcnt is BMI1, and below the baseline its F3 prefix is
	// ignored, so it degrades silently to bsf rather than faulting.
	w("\tbsf eax, eax")
	w("\tadd eax, esi")
	w("\tret")
	// Scalar tail: under 16 bytes left, and the whole algorithm for a string
	// shorter than one block.
	w(".Lssa_ascii_tail:")
	w("\tcmp esi, r8d")
	w("\tjae .Lssa_ascii_none")
	w("\tmovzx r9d, byte ptr [rdi + rsi]")
	w("\ttest r9d, 128")
	w("\tjnz .Lssa_ascii_tail_hit")
	w("\tadd esi, 1")
	w("\tjmp .Lssa_ascii_tail")
	w(".Lssa_ascii_tail_hit:")
	w("\tmov eax, esi")
	w("\tret")
	w(".Lssa_ascii_none:")
	w("\tmov eax, r8d") // no high byte: the answer is len
	w("\tret")
}

// emitSumBytesHelper writes __fern_sum_bytes(s) -> every byte of `s` added
// into a 32-bit accumulator that wraps (docs/ATLAS-PLATFORM-PLAN.md §3.3,
// sixth kernel).
//
// SCALAR (§3.4 step 1). This is the leg §3.4 records as the SILENT undercount
// — a backend that has the op but lowers it byte-at-a-time compiles, agrees
// with every differential, and is simply slow — so it gets the lowering at the
// same time as the other seven rather than after, and the throughput gate is
// what will report it when the vector bodies land.
//
// `movzx` is the whole of the sign question: a byte is unsigned, so 0xff
// contributes 255 rather than -1. The accumulate is a 32-bit `add`, so the
// wrap is the register width.
//
// Strings on this backend are ONE word (the data pointer) with the length at
// [ptr-4], so the single argument lands in rdi with no slot arithmetic. Leaf:
// no frame, and every register it touches is caller-saved.
//
// No cursor and no byte operand, so there is no clamp and no range guard: an
// empty string sums to 0 because it has no bytes.
func emitSumBytesHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_sum_bytes"))
	w("\tmov r8d, %s", memRef("rdi", -4)) // len
	w("\txor eax, eax")                   // running sum
	w("\txor edx, edx")                   // cursor, as an INDEX
	w(".Lssa_sum_bytes_loop:")
	w("\tcmp edx, r8d")
	w("\tjae .Lssa_sum_bytes_ret")
	w("\tmovzx r9d, byte ptr [rdi + rdx]")
	w("\tadd eax, r9d")
	w("\tadd edx, 1")
	w("\tjmp .Lssa_sum_bytes_loop")
	w(".Lssa_sum_bytes_ret:")
	w("\tret")
}

// emitScaleF64Helper writes __fern_scale_f64(xs, k) -> a fresh f64 array of
// xs's length, element i = xs[i] * k, header written here (cap@-12, rc=1@-8,
// len@-4) so the caller owns one fresh array.
//
// AVX2 (§3.4 step 3), the same body as the stack-machine emitter in
// internal/codegen/x86_64: vmulpd over four lanes with a scalar tail for the
// remainder. A multiply is elementwise, so the lanes reassociate nothing
// (docs/ARRAY-ALGEBRA.md §3, AA-02). This leg owes the assembler nothing —
// internal/native/x86_64 gained vmovupd / vmulpd / vbroadcastsd when the
// stack-machine emitter vectorised, and both emitters feed that one encoder.
//
// Both bodies multiply k BY the element: the vector body has no choice (the
// folded memory operand is the second source), and the tail matches it so the
// two cannot disagree about which NaN a NaN times a NaN yields.
//
// rdi = xs, rsi = k as f64 bits (the SSA GP convention), result in rax.
func emitScaleF64Helper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_scale_f64"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")                        // three pushes past the return address: 16-aligned for the call
	w("\tmov rbx, rdi")                    // xs
	w("\tmov r12, rsi")                    // k bits
	w("\tmov r13d, %s", memRef("rdi", -4)) // n
	w("\tmov rdi, r13")
	w("\tshl rdi, 3")
	w("\tadd rdi, 16") // allocSize = header + n * 8
	w("\tcall %s", fnLabel("__alloc"))
	w("\tadd rax, 16")                    // data
	w("\tmov dword ptr [rax - 12], r13d") // cap = n
	w("\tmov dword ptr [rax - 8], 1")     // rc = 1
	w("\tmov dword ptr [rax - 4], r13d")  // len = n
	w("\tmovq xmm1, r12")
	w("\tvbroadcastsd ymm1, xmm1") // k in all four lanes
	w("\txor ecx, ecx")
	w("\tmov edx, r13d")
	w("\tand edx, -4") // whole blocks of four only
	w(".Lssa_scale_f64_vec:")
	w("\tcmp ecx, edx")
	w("\tjae .Lssa_scale_f64_tail")
	w("\tvmulpd ymm0, ymm1, [rbx + rcx*8]")
	w("\tvmovupd [rax + rcx*8], ymm0")
	w("\tadd ecx, 4")
	w("\tjmp .Lssa_scale_f64_vec")
	w(".Lssa_scale_f64_tail:")
	// vzeroupper clears only the upper half, so xmm1 still holds k for the
	// legacy-SSE tail.
	w("\tvzeroupper")
	w(".Lssa_scale_f64_loop:")
	w("\tcmp ecx, r13d")
	w("\tjae .Lssa_scale_f64_ret")
	w("\tmovsd xmm0, xmm1")
	w("\tmulsd xmm0, qword ptr [rbx + rcx*8]")
	w("\tmovsd qword ptr [rax + rcx*8], xmm0")
	w("\tadd ecx, 1")
	w("\tjmp .Lssa_scale_f64_loop")
	w(".Lssa_scale_f64_ret:")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitCrc32CksumHelper writes __fern_crc32_cksum(crc, s) -> the bytes of `s`
// folded into the running CRC-32 that cksum(1) prints: poly 0x04C11DB7, MSB
// first, unreflected, and no final complement — the length fold and the
// complement belong to std/hash's finish, not to a chunk.
//
// SCALAR (docs/ATLAS-PLATFORM-PLAN.md §3.4 step 1). The flat x86-64 backend
// folds the same bytes with pclmulqdq at 4.3x, and this backend's baseline has
// the instruction too, so the fold is portable here whenever a measurement
// asks for it. It is not this commit: §3.4 puts the scalar lowering in every
// backend FIRST, because a builtin that is fast on two legs and absent on six
// cannot be adopted by std/hash at all.
//
// Branchless: the polynomial is selected by an arithmetic shift of the sign
// bit rather than a conditional jump, so the eight bit steps have no
// unpredictable branch between them.
//
// crc arrives in edi and the string in rsi — the SCALAR is first here, which
// is the reverse of the family's other kernels. Strings on this backend are
// ONE word (the data pointer) with the length at [ptr-4]. Leaf: no frame, and
// every register it touches is caller-saved.
//
// An empty string returns the incoming crc unchanged, which is what makes
// chunked hashing associative.
func emitCrc32CksumHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_crc32_cksum"))
	w("\tmov eax, edi")                   // running crc
	w("\tmov r8d, %s", memRef("rsi", -4)) // len
	w("\txor edx, edx")                   // cursor, as an INDEX
	w(".Lssa_crc32_scan:")
	w("\tcmp edx, r8d")
	w("\tjae .Lssa_crc32_ret")
	w("\tmovzx r9d, byte ptr [rsi + rdx]")
	w("\tshl r9d, 24")
	w("\txor eax, r9d")
	w("\tmov r11d, 8")
	w(".Lssa_crc32_bit:")
	w("\tmov r10d, eax")
	w("\tsar r10d, 31")
	w("\tand r10d, 0x04c11db7")
	w("\tadd eax, eax")
	w("\txor eax, r10d")
	w("\tsub r11d, 1")
	w("\tjnz .Lssa_crc32_bit")
	w("\tadd edx, 1")
	w("\tjmp .Lssa_crc32_scan")
	w(".Lssa_crc32_ret:")
	w("\tret")
}

// emitCountByteHelper writes __fern_count_byte(s, byte) -> how many bytes of `s`
// equal `byte`. No cursor, so no clamp; both degenerate answers are real
// counts rather than sentinels — an out-of-range byte counts 0 because nothing
// can equal it, an empty string counts 0 because it has no bytes. Leaf.
//
// The vector body mirrors the flat backend's kernel because the scalar one it
// replaced was 12.6x slower on examples/bench/string_count_byte.fern, which is
// what the differential's ratio gate reports (#8069). Byte-at-a-time is a fine
// helper right up until a program counts 32 KiB twice per round, 6000 rounds.
// emitScanSetHelper writes __fern_scan_set(s, from, set) -> the index of the
// first byte at or after `from` whose entry in `set` is nonzero, or len(s).
// Scalar: the table read per byte is the kernel. A set with an entry for
// every byte value takes the loop with no length check. Leaf.
func emitScanSetHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_scan_set"))
	w("\tmov r8d, %s", memRef("rdi", -4)) // len
	w("\ttest esi, esi")
	w("\tjns .Lssa_scan_set_from_ok")
	w("\txor esi, esi") // clamp `from` up to 0
	w(".Lssa_scan_set_from_ok:")
	w("\tmov esi, esi")
	w("\tmov r9d, %s", memRef("rdx", -4)) // set length
	w("\tcmp r9d, 256")
	w("\tjb .Lssa_scan_set_short")
	// Full set: four bytes a turn on a cursor, then one at a time. A hit at
	// byte k of the four steps the cursor k times before its offset is read.
	w("\tcmp esi, r8d")
	w("\tjge .Lssa_scan_set_end")
	w("\tlea r10, [rdi + rsi]") // cursor
	w("\tlea r11, [rdi + r8]")  // end
	w("\tlea rcx, [r11 - 4]")   // the last cursor with four bytes ahead
	w(".Lssa_scan_set_quad:")
	w("\tcmp r10, rcx")
	w("\tja .Lssa_scan_set_one")
	for k := 0; k < 4; k++ {
		w("\tmovzx eax, byte ptr [r10 + %d]", k)
		w("\tcmp byte ptr [rdx + rax], 0")
		w("\tjne .Lssa_scan_set_hit%d", k)
	}
	w("\tadd r10, 4")
	w("\tjmp .Lssa_scan_set_quad")
	w(".Lssa_scan_set_one:")
	w("\tcmp r10, r11")
	w("\tjae .Lssa_scan_set_end")
	w("\tmovzx eax, byte ptr [r10]")
	w("\tcmp byte ptr [rdx + rax], 0")
	w("\tjne .Lssa_scan_set_hit0")
	w("\tinc r10")
	w("\tjmp .Lssa_scan_set_one")
	w(".Lssa_scan_set_hit3:")
	w("\tinc r10")
	w(".Lssa_scan_set_hit2:")
	w("\tinc r10")
	w(".Lssa_scan_set_hit1:")
	w("\tinc r10")
	w(".Lssa_scan_set_hit0:")
	w("\tmov rax, r10")
	w("\tsub rax, rdi")
	w("\tret")
	w(".Lssa_scan_set_short:")
	w("\tcmp esi, r8d")
	w("\tjge .Lssa_scan_set_end")
	w("\tmovzx eax, byte ptr [rdi + rsi]")
	w("\tcmp eax, r9d")
	w("\tjae .Lssa_scan_set_short_next")
	w("\tcmp byte ptr [rdx + rax], 0")
	w("\tjne .Lssa_scan_set_hit")
	w(".Lssa_scan_set_short_next:")
	w("\tinc esi")
	w("\tjmp .Lssa_scan_set_short")
	w(".Lssa_scan_set_hit:")
	w("\tmov eax, esi")
	w("\tret")
	w(".Lssa_scan_set_end:")
	w("\tmov eax, r8d")
	w("\tret")
}

// emitCountRunsHelper writes __fern_count_runs(s, inside, set) -> how many
// runs of bytes whose entry in `set` is nonzero begin in s, `inside` nonzero
// meaning the byte before s was a member. It keeps "not a member" as a 0/1
// byte, and a run begins where that drops from 1 to 0: the borrow of the
// current flag minus the previous one. A byte past the set's end is not a
// member.
func emitCountRunsHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_count_runs"))
	w("\tmov ecx, %s", memRef("rdi", -4)) // len
	w("\txor r9d, r9d")
	w("\txor r11d, r11d")
	w("\ttest esi, esi")
	w("\tsete r9b") // the previous byte is not a member
	w("\txor esi, esi")
	w("\txor eax, eax")
	w("\tmov r8d, %s", memRef("rdx", -4)) // set length
	w(".Lssa_count_runs_loop:")
	w("\tcmp esi, ecx")
	w("\tjge .Lssa_count_runs_end")
	w("\tmovzx r10d, byte ptr [rdi + rsi]")
	w("\tmov r11b, 1")
	w("\tcmp r10d, r8d")
	w("\tjae .Lssa_count_runs_flag")
	w("\tcmp byte ptr [rdx + r10], 1")
	w("\tsetb r11b")
	w(".Lssa_count_runs_flag:")
	w("\tcmp r11b, r9b")
	w("\tadc eax, 0")
	w("\tmov r9b, r11b")
	w("\tinc esi")
	w("\tjmp .Lssa_count_runs_loop")
	w(".Lssa_count_runs_end:")
	w("\tret")
}

// emitBsdSumHelper writes __fern_bsd_sum(s, sum) -> the BSD checksum continued
// over s: per byte a 16-bit rotate right by one and an add.
func emitBsdSumHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_bsd_sum"))
	w("\tmov ecx, %s", memRef("rdi", -4)) // len
	w("\tmovzx eax, si")
	w("\txor edx, edx")
	w("\ttest ecx, ecx")
	w("\tjz .Lssa_bsd_sum_ret")
	w(".Lssa_bsd_sum_loop:")
	w("\tmovzx r8d, byte ptr [rdi + rdx]")
	w("\tror ax, 1")
	w("\tadd ax, r8w")
	w("\tadd edx, 1")
	w("\tcmp edx, ecx")
	w("\tjb .Lssa_bsd_sum_loop")
	w(".Lssa_bsd_sum_ret:")
	w("\tmovzx eax, ax")
	w("\tret")
}

func emitCountByteHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_count_byte"))
	w("\tmov r8d, %s", memRef("rdi", -4)) // len
	w("\txor eax, eax")                   // running count
	w("\tcmp rsi, 255")
	w("\tja .Lssa_count_ret")
	w("\txor edx, edx")
	// SSE2 splat of the needle across xmm1.
	w("\tmovd xmm1, esi")
	w("\tpunpcklbw xmm1, xmm1")
	w("\tpunpcklwd xmm1, xmm1")
	w("\tpshufd xmm1, xmm1, 0")
	w("\tvpbroadcastb ymm1, xmm1")
	// 32 bytes an iteration while at least 32 remain (AVX2, in the x86-64-v3
	// baseline), then 16 while at least 16 remain, then the scalar loop takes
	// the 0..15-byte tail — and the whole string when it is shorter than one
	// block. The loads are unaligned on purpose: the pointer comes from the
	// allocator, so a read starting inside the string cannot cross into an
	// unmapped page, and a scalar align-up prologue would cost more.
	w(".Lssa_count_avx:")
	w("\tmov r9d, r8d")
	w("\tsub r9d, edx")
	w("\tcmp r9d, 32")
	w("\tjl .Lssa_count_avx_done")
	w("\tvmovdqu ymm0, [rdi + rdx]")
	w("\tvpcmpeqb ymm0, ymm0, ymm1")
	w("\tvpmovmskb r9d, ymm0")
	w("\tpopcnt r9d, r9d")
	w("\tadd eax, r9d")
	w("\tadd edx, 32")
	w("\tjmp .Lssa_count_avx")
	w(".Lssa_count_avx_done:")
	// The 16-byte loop is legacy SSE: the upper halves are cleared once here,
	// the only way from the AVX loop to it.
	w("\tvzeroupper")
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

// emitStrConcatHelper writes __str_concat(a, b) -> new data pointer: a fresh
// length-prefixed string holding a's bytes followed by b's (rc=1 at base+0,
// total length at base+4, data at base+8 — the header ConstStr and every heap
// string use). The block comes from __alloc at total+strBlockBytes bytes, so
// its extent is that request's size class, which is what lets
// __fern_str_append grow it in place later. The IR lowers `a + b` on strings (OpStrConcat) to a call here.
// Lengths live at [ptr-4]. rdi=a, rsi=b; returns rax=data.
func emitStrConcatHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_concat"))
	w("\tmov ecx, %s", memRef("rdi", -4)) // la
	w("\tmov edx, %s", memRef("rsi", -4)) // lb
	// total = la + lb summed in 64 bits (both 32-bit loads zero-extend). A
	// total past the i32 ceiling cannot be stamped into the 4-byte length
	// slot, so it aborts rather than storing a negative length (#8457).
	w("\tlea r8, [rcx + rdx]")
	w("\tcmp r8, 2147483647")
	w("\tja .Lssa_strcat_len_overflow")
	w("\tlea r11, [r8 + %d]", strBlockBytes) // header + total + NUL
	w("\tsub rsp, 8")                        // entered 8 past alignment; the trampoline is called at 16
	ssaBumpAlloc(w, "r9", "r11")
	w("\tadd rsp, 8")
	w("\tmov dword ptr [r9], 1")          // rc = 1
	w("\tmov [r9 + 4], r8d")              // len = total
	w("\tlea rax, [r9 + 8]")              // data (return value)
	w("\tmov r9, rsi")                    // b, before the copy clobbers the argument registers
	w("\tmov r10, rdx")                   // lb
	emitBcopyCall(w, "rax", "rdi", "rcx") // a's la bytes at data
	w("\tlea rdi, [rax + r8]")
	w("\tsub rdi, r10")                  // data + la
	emitBcopyCall(w, "rdi", "r9", "r10") // b's lb bytes after them
	w("\tret")
	w(".Lssa_strcat_len_overflow:")
	w("\tjmp %s", abortAllocSize)
}

// emitStrAppendHelper writes __fern_str_append(a, b) -> data, the string
// self-append the IR emits for `s = s + piece` (#5637): it CONSUMES a, and the
// assignment that follows skips its release. When a is a uniquely held heap
// string and the grown length still fits the block, b's bytes are copied into
// the slack past a's data, the length is restamped and the same pointer comes
// back — no allocation, no re-copy of the accumulated prefix. Anything else
// (a literal or other static sentinel, a shared buffer, a block the growth
// would overflow) is a plain __str_concat followed by the release of a
// through __fern_str_dec, which is what consuming it means.
//
// The fit test is the block's size class: every heap string is an __alloc of
// at least la+strBlockBytes bytes (the header plus its length and NUL; the
// string builder's buffers request more), and __alloc hands out the class's
// whole rounded extent, so that class is capacity the string owns whatever
// produced it. Reader.read_chunk, the one producer that bumps the cursor itself,
// rounds its block to the same class. The grown string is freed at its new
// length, which classes the same as the block. rdi=a, rsi=b; returns
// rax=data.
func emitStrAppendHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_str_append"))
	w("\tcmp rdi, 0x10000")
	w("\tjb .Lssa_strapp_copy")
	w("\tmov eax, %s", memRef("rdi", -8)) // rc; a static sentinel has its top bit set
	w("\tcmp eax, 1")
	w("\tjne .Lssa_strapp_copy")
	w("\tmov r9d, %s", memRef("rdi", -4))  // la
	w("\tmov r10d, %s", memRef("rsi", -4)) // lb
	w("\tlea rdx, [r9 + r10]")             // total, in 64 bits
	w("\tcmp rdx, 2147483647")
	w("\tja .Lssa_strapp_copy")                                       // __str_concat aborts on it
	w("\tlea r11, [r9 + %d]", strBlockBytes)                          // the request the block was at least allocated at
	emitFreelistClass(w, "strapp", "r11", "rax", ".Lssa_strapp_copy") // r11 = the class's extent
	w("\tlea rax, [rdx + %d]", strBlockBytes+15)
	w("\tand rax, -16") // the grown request, 16-rounded
	w("\tcmp rax, r11")
	w("\tja .Lssa_strapp_copy")
	w("\tmov %s, edx", memRef("rdi", -4)) // len = total
	w("\tmov rax, rdi")
	w("\tlea rdi, [rdi + r9]") // dst = a + la
	w("\tmov rdx, r10")        // lb; rsi = b already
	w("\tcall %s", bcopySym)
	w("\tret")
	w(".Lssa_strapp_copy:")
	w("\tpush rbx") // one push past the return address: 16-aligned for the calls
	w("\tmov rbx, rdi")
	w("\tcall %s", fnLabel("__str_concat"))
	w("\tmov rdi, rbx")
	w("\tmov rbx, rax")
	w("\tcall %s", fnLabel("__fern_str_dec"))
	w("\tmov rax, rbx")
	w("\tpop rbx")
	w("\tret")
}

// emitStrDecHelper writes __fern_str_dec(ptr) -> ptr: the scope-exit drop
// for a string-valued local. Guarded (null / low-address / immortal-sentinel
// top bit, so it skips .rodata literals); on the last reference (rc == 1 at
// [ptr-8]) the block goes back to the freelist at base = ptr-8 and
// len+strBlockBytes bytes, which is the number every string producer
// allocates: each is an __alloc of at least that with its data at base+8, and
// a string grown in place stayed inside its class. A shared string is
// decremented in place.
func emitStrDecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_str_dec"))
	w("\tmov rax, rdi")
	w("\tcmp rdi, 0x10000")
	w("\tjb .Lssa_strdec_ret")
	w("\tmov ecx, %s", memRef("rdi", -8)) // rc
	w("\ttest ecx, ecx")
	w("\tjle .Lssa_strdec_ret") // static sentinel, or already dropped
	w("\tcmp ecx, 1")
	w("\tje .Lssa_strdec_free")
	w("\tsub ecx, 1")
	w("\tmov %s, ecx", memRef("rdi", -8))
	w(".Lssa_strdec_ret:")
	w("\tret")
	w(".Lssa_strdec_free:")
	w("\tpush rbx") // one push past the return address: 16-aligned for the call
	w("\tmov rbx, rdi")
	w("\tmov esi, %s", memRef("rdi", -4)) // len
	w("\tadd rsi, %d", strBlockBytes)     // the size every producer allocated
	w("\tsub rdi, 8")                     // base
	w("\tcall %s", fnLabel("__free"))
	w("\tmov rax, rbx")
	w("\tpop rbx")
	w("\tret")
}

// The capacity-carrying string builder (#8773), laid out word for word as the
// flat backend's emitStrBuilderRuntime lays it out. A handle addresses a
// 32-byte control block, itself rc-headed: data at +0, length at +8, capacity
// at +16, and the reserve a take re-arms from at +24. The buffer is a string
// block — rc=1@base, its payload size at base+4 until buf_take stamps the
// length there, data at base+8 — sized cap+1 for the trailing NUL, which is
// what makes buf_take zero-copy: it stamps the length and the NUL and hands the
// same pointer back.
//
// Growth doubles from the current capacity, or from the reserve once a take has
// unarmed the builder, from a floor of 64. Blocks go back through
// __fern_box_free at the size they were requested at, the handoff the flat
// backend's freelist reads, so nothing here changes once this heap reclaims.

// emitBufStrBlock writes the allocation of a buffer holding n payload bytes —
// n in nReg, with n32 its 32-bit spelling — and leaves the data pointer in
// dataReg. Clobbers r10 and r11 besides dataReg, so nReg must be neither.
func emitBufStrBlock(w func(string, ...any), nReg, n32, dataReg string) {
	w("\tlea r10, [%s + 8]", nReg)
	ssaBumpAlloc(w, dataReg, "r10")
	w("\tmov dword ptr [%s], 1", dataReg) // rc = 1
	w("\tmov [%s + 4], %s", dataReg, n32) // payload size, until buf_take stamps the length
	w("\tadd %s, 8", dataReg)
}

// emitBufNewHelper writes buf_new(cap) -> handle: allocate the control block
// and a buffer of at least 16 bytes, and publish both capacities.
func emitBufNewHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("buf_new"))
	w("\tcmp rdi, 16") // a floor, so the doubling has something to double
	w("\tjae .Lssa_bufnew_cap")
	w("\tmov edi, 16")
	w(".Lssa_bufnew_cap:")
	w("\tsub rsp, 8")                  // entered 8 past alignment; the trampoline is called at 16
	ssaBumpAlloc(w, "rax", "40")       // rc header + the four words
	w("\tmov dword ptr [rax], 1")      // rc = 1
	w("\tmov dword ptr [rax + 4], 32") // payload size
	w("\tadd rax, 8")                  // H
	w("\tlea rsi, [rdi + 1]")          // cap + NUL
	emitBufStrBlock(w, "rsi", "esi", "rcx")
	w("\tmov [rax], rcx")
	w("\tmov qword ptr [rax + 8], 0")
	w("\tmov [rax + 16], rdi")
	w("\tadd rsp, 8")
	w("\tret")
}

// emitBufReserveHelper writes __fern_buf_reserve(H, need): replace the buffer
// with one of at least `need` bytes, doubling from the current capacity, carry
// the live bytes across and give the old block back. Internal; the three pushes
// reach it.
//
// The capacity is always set: buf_new floors it at 16 and nothing clears it,
// since buf_take now copies out and leaves the builder holding its buffer
// (#9542). The fallbacks this used to need — the reserve word, then a bare 64 —
// were for the unarmed builder a take used to leave behind, and went with it.
func emitBufReserveHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_buf_reserve"))
	// Three callee-saved pushes plus the return address leave rsp 16-aligned
	// for the calls below.
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tmov rbx, rdi") // H
	w("\tmov r12, [rbx + 16]")
	w(".Lssa_bufres_dbl:")
	w("\tcmp r12, rsi")
	w("\tjae .Lssa_bufres_alloc")
	w("\tadd r12, r12")
	w("\tjmp .Lssa_bufres_dbl")
	w(".Lssa_bufres_alloc:")
	w("\tlea r8, [r12 + 1]") // cap + NUL
	emitBufStrBlock(w, "r8", "r8d", "r13")
	w("\tmov rdx, [rbx + 8]") // len
	w("\ttest rdx, rdx")
	w("\tjz .Lssa_bufres_nocopy")
	w("\tmov rsi, [rbx]")
	emitBcopyCall(w, "r13", "rsi", "rdx")
	w(".Lssa_bufres_nocopy:")
	w("\tmov rdi, [rbx]")
	w("\ttest rdi, rdi")
	w("\tjz .Lssa_bufres_store")
	w("\tmov rsi, [rbx + 16]")
	w("\tadd rsi, 1")
	w("\tcall %s", fnLabel("__fern_box_free"))
	w(".Lssa_bufres_store:")
	w("\tmov [rbx], r13")
	w("\tmov [rbx + 16], r12")
	w("\txor eax, eax")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitBufPushHelper writes buf_push(H, s): copy the single-word string's bytes
// (length at [s-4]) onto the builder's tail, growing it first when they do not
// fit. The fitting path needs no frame: __ssa_bcopy clobbers only its argument
// registers, so H is held in r8 across it. Unused return is 0.
func emitBufPushHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("buf_push"))
	w("\tmov edx, %s", memRef("rsi", -4)) // n (a 32-bit load zero-extends)
	w("\ttest edx, edx")
	w("\tjz .Lssa_bufpush_none")
	w("\tmov rax, [rdi + 8]")
	w("\tadd rax, rdx") // need
	w("\tcmp rax, [rdi + 16]")
	w("\tja .Lssa_bufpush_grow")
	w(".Lssa_bufpush_fits:")
	w("\tmov r8, rdi")
	w("\tmov rdi, [r8]")
	w("\tadd rdi, [r8 + 8]") // dst = data + len
	w("\tadd [r8 + 8], rdx") // len += n
	emitBcopyCall(w, "rdi", "rsi", "rdx")
	w(".Lssa_bufpush_none:")
	w("\txor eax, eax")
	w("\tret")
	w(".Lssa_bufpush_grow:")
	// Two callee-saved pushes plus the return address leave rsp 8 mod 16; the
	// extra 8 realigns it for the call.
	w("\tpush rbx")
	w("\tpush r12")
	w("\tsub rsp, 8")
	w("\tmov rbx, rdi")
	w("\tmov r12, rsi")
	w("\tmov rsi, rax")
	w("\tcall %s", fnLabel("__fern_buf_reserve"))
	w("\tmov rdi, rbx")
	w("\tmov rsi, r12")
	w("\tmov edx, %s", memRef("rsi", -4))
	w("\tadd rsp, 8")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tjmp .Lssa_bufpush_fits")
}

// emitBufPushRangeHelper writes buf_push_range(H, s, lo, hi): the same, for a
// byte range of s with no intermediate string. An empty or inverted range is a
// no-op; the bounds are the caller's, as with slice_unchecked. lo/hi arrive as
// i32 and are sign-extended.
func emitBufPushRangeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("buf_push_range"))
	w("\tmovsxd rdx, edx") // lo
	w("\tmovsxd rcx, ecx") // hi
	w("\tsub rcx, rdx")    // n = hi - lo
	w("\tjle .Lssa_bufrange_none")
	w("\tadd rsi, rdx") // src = s + lo
	w("\tmov rdx, rcx")
	w("\tmov rax, [rdi + 8]")
	w("\tadd rax, rdx") // need
	w("\tcmp rax, [rdi + 16]")
	w("\tja .Lssa_bufrange_grow")
	w(".Lssa_bufrange_fits:")
	w("\tmov r8, rdi")
	w("\tmov rdi, [r8]")
	w("\tadd rdi, [r8 + 8]") // dst = data + len
	w("\tadd [r8 + 8], rdx") // len += n
	emitBcopyCall(w, "rdi", "rsi", "rdx")
	w(".Lssa_bufrange_none:")
	w("\txor eax, eax")
	w("\tret")
	w(".Lssa_bufrange_grow:")
	// Three callee-saved pushes plus the return address leave rsp 16-aligned
	// for the call.
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tmov rbx, rdi")
	w("\tmov r12, rsi")
	w("\tmov r13, rdx")
	w("\tmov rsi, rax")
	w("\tcall %s", fnLabel("__fern_buf_reserve"))
	w("\tmov rdi, rbx")
	w("\tmov rsi, r12")
	w("\tmov rdx, r13")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tjmp .Lssa_bufrange_fits")
}

// emitBufPushMappedHelper writes buf_push_mapped(H, s, table): append
// table[c] for each byte c of s, or c itself when it is past the table's end.
// A table covering every byte value takes a loop with no length check, four
// bytes a turn.
func emitBufPushMappedHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("buf_push_mapped"))
	w("\tmov ecx, %s", memRef("rsi", -4)) // n
	w("\ttest ecx, ecx")
	w("\tjz .Lssa_bufmap_none")
	w("\tmov rax, [rdi + 8]")
	w("\tadd rax, rcx") // need
	w("\tcmp rax, [rdi + 16]")
	w("\tja .Lssa_bufmap_grow")
	w(".Lssa_bufmap_fits:")
	w("\tmov r8, [rdi]")
	w("\tadd r8, [rdi + 8]")              // dst = data + len
	w("\tadd [rdi + 8], rcx")             // len += n
	w("\tmov r9d, %s", memRef("rdx", -4)) // table length
	w("\txor r10d, r10d")
	w("\tcmp r9d, 256")
	w("\tjb .Lssa_bufmap_short")
	w("\tcmp rcx, 4")
	w("\tjb .Lssa_bufmap_tail")
	w("\tsub rcx, 4")
	w(".Lssa_bufmap_four:")
	w("\tmovzx eax, byte ptr [rsi + r10]")
	w("\tmovzx edi, byte ptr [rsi + r10 + 1]")
	w("\tmovzx r9d, byte ptr [rsi + r10 + 2]")
	w("\tmovzx r11d, byte ptr [rsi + r10 + 3]")
	w("\tmovzx eax, byte ptr [rdx + rax]")
	w("\tmovzx edi, byte ptr [rdx + rdi]")
	w("\tmovzx r9d, byte ptr [rdx + r9]")
	w("\tmovzx r11d, byte ptr [rdx + r11]")
	w("\tmov byte ptr [r8 + r10], al")
	w("\tmov byte ptr [r8 + r10 + 1], dil")
	w("\tmov byte ptr [r8 + r10 + 2], r9b")
	w("\tmov byte ptr [r8 + r10 + 3], r11b")
	w("\tadd r10, 4")
	w("\tcmp r10, rcx")
	w("\tjbe .Lssa_bufmap_four")
	w("\tadd rcx, 4")
	w("\tcmp r10, rcx")
	w("\tjae .Lssa_bufmap_none")
	w(".Lssa_bufmap_tail:")
	w("\tmovzx eax, byte ptr [rsi + r10]")
	w("\tmovzx eax, byte ptr [rdx + rax]")
	w("\tmov byte ptr [r8 + r10], al")
	w("\tinc r10")
	w("\tcmp r10, rcx")
	w("\tjb .Lssa_bufmap_tail")
	w("\tjmp .Lssa_bufmap_none")
	w(".Lssa_bufmap_short:")
	w("\tmovzx eax, byte ptr [rsi + r10]")
	w("\tcmp eax, r9d")
	w("\tjae .Lssa_bufmap_keep")
	w("\tmovzx eax, byte ptr [rdx + rax]")
	w(".Lssa_bufmap_keep:")
	w("\tmov byte ptr [r8 + r10], al")
	w("\tinc r10")
	w("\tcmp r10, rcx")
	w("\tjb .Lssa_bufmap_short")
	w(".Lssa_bufmap_none:")
	w("\txor eax, eax")
	w("\tret")
	w(".Lssa_bufmap_grow:")
	// Three callee-saved pushes plus the return address leave rsp 16-aligned
	// for the call.
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tmov rbx, rdi")
	w("\tmov r12, rsi")
	w("\tmov r13, rdx")
	w("\tmov rsi, rax")
	w("\tcall %s", fnLabel("__fern_buf_reserve"))
	w("\tmov rdi, rbx")
	w("\tmov rsi, r12")
	w("\tmov rdx, r13")
	w("\tmov ecx, %s", memRef("rsi", -4))
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tjmp .Lssa_bufmap_fits")
}

// emitBufPushFilteredHelper writes buf_push_filtered(H, s, drop): append each
// byte c of s whose entry drop[c] is zero, or that is past the table's end.
// Room for all of s is reserved; the full-table loop stores every byte and
// advances the kept count only past a kept one.
func emitBufPushFilteredHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("buf_push_filtered"))
	w("\tmov ecx, %s", memRef("rsi", -4)) // n
	w("\ttest ecx, ecx")
	w("\tjz .Lssa_buffilt_none")
	w("\tmov rax, [rdi + 8]")
	w("\tadd rax, rcx") // need
	w("\tcmp rax, [rdi + 16]")
	w("\tja .Lssa_buffilt_grow")
	w(".Lssa_buffilt_fits:")
	w("\tmov r8, [rdi]")
	w("\tadd r8, [rdi + 8]")              // dst = data + len
	w("\tmov r9d, %s", memRef("rdx", -4)) // table length
	w("\txor r10d, r10d")                 // i
	w("\txor r11d, r11d")                 // kept
	w("\tcmp r9d, 256")
	w("\tjb .Lssa_buffilt_short")
	w(".Lssa_buffilt_full:")
	w("\tmovzx eax, byte ptr [rsi + r10]")
	w("\tmovzx r9d, byte ptr [rdx + rax]")
	w("\tmov byte ptr [r8 + r11], al")
	w("\tcmp r9d, 1")
	w("\tadc r11, 0")
	w("\tinc r10")
	w("\tcmp r10, rcx")
	w("\tjb .Lssa_buffilt_full")
	w("\tjmp .Lssa_buffilt_len")
	w(".Lssa_buffilt_short:")
	w("\tmovzx eax, byte ptr [rsi + r10]")
	w("\tcmp eax, r9d")
	w("\tjae .Lssa_buffilt_keep")
	w("\tcmp byte ptr [rdx + rax], 0")
	w("\tjne .Lssa_buffilt_next")
	w(".Lssa_buffilt_keep:")
	w("\tmov byte ptr [r8 + r11], al")
	w("\tinc r11")
	w(".Lssa_buffilt_next:")
	w("\tinc r10")
	w("\tcmp r10, rcx")
	w("\tjb .Lssa_buffilt_short")
	w(".Lssa_buffilt_len:")
	w("\tadd [rdi + 8], r11")
	w(".Lssa_buffilt_none:")
	w("\txor eax, eax")
	w("\tret")
	w(".Lssa_buffilt_grow:")
	// Three callee-saved pushes plus the return address leave rsp 16-aligned
	// for the call.
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tmov rbx, rdi")
	w("\tmov r12, rsi")
	w("\tmov r13, rdx")
	w("\tmov rsi, rax")
	w("\tcall %s", fnLabel("__fern_buf_reserve"))
	w("\tmov rdi, rbx")
	w("\tmov rsi, r12")
	w("\tmov rdx, r13")
	w("\tmov ecx, %s", memRef("rsi", -4))
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tjmp .Lssa_buffilt_fits")
}

// emitBufPushExpandedHelper writes buf_push_expanded(H, s, table): append each
// byte c of s as the record at table[c*8], a length byte (above 7 counts as
// 7) and then the bytes, or c itself when its record is not wholly inside
// the table. Eight bytes per input byte are reserved, so a record is copied
// as one eight-byte store and the tail advances by its length.
func emitBufPushExpandedHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("buf_push_expanded"))
	w("\tmov ecx, %s", memRef("rsi", -4)) // n
	w("\ttest ecx, ecx")
	w("\tjz .Lssa_bufexp_none")
	w("\tlea rax, [rcx*8 + 8]")
	w("\tadd rax, [rdi + 8]") // need
	w("\tcmp rax, [rdi + 16]")
	w("\tja .Lssa_bufexp_grow")
	w(".Lssa_bufexp_fits:")
	w("\tmov r8, [rdi]")
	w("\tadd r8, [rdi + 8]") // dst = data + len
	w("\tmov r9, r8")        // where this push began
	w("\tadd rcx, rsi")      // the string's end
	w("\tmov r10d, %s", memRef("rdx", -4))
	w("\tshr r10d, 3") // whole records
	w("\tcmp r10d, 256")
	w("\tjb .Lssa_bufexp_short")
	w("\tmov r10d, 7")
	w(".Lssa_bufexp_full:")
	w("\tmovzx eax, byte ptr [rsi]")
	w("\tmov r11, [rdx + rax*8]")
	w("\tmovzx eax, r11b")
	w("\tcmp eax, r10d")
	w("\tcmova eax, r10d")
	w("\tshr r11, 8")
	w("\tmov [r8], r11")
	w("\tadd r8, rax")
	w("\tinc rsi")
	w("\tcmp rsi, rcx")
	w("\tjb .Lssa_bufexp_full")
	w("\tjmp .Lssa_bufexp_len")
	w(".Lssa_bufexp_short:")
	w("\tmovzx eax, byte ptr [rsi]")
	w("\tcmp eax, r10d")
	w("\tjae .Lssa_bufexp_keep")
	w("\tmov r11, [rdx + rax*8]")
	w("\tmovzx eax, r11b")
	w("\tcmp eax, 7")
	w("\tjbe .Lssa_bufexp_room")
	w("\tmov eax, 7")
	w(".Lssa_bufexp_room:")
	w("\tshr r11, 8")
	w("\tmov [r8], r11")
	w("\tadd r8, rax")
	w("\tjmp .Lssa_bufexp_next")
	w(".Lssa_bufexp_keep:")
	w("\tmov byte ptr [r8], al")
	w("\tinc r8")
	w(".Lssa_bufexp_next:")
	w("\tinc rsi")
	w("\tcmp rsi, rcx")
	w("\tjb .Lssa_bufexp_short")
	w(".Lssa_bufexp_len:")
	w("\tsub r8, r9")
	w("\tadd [rdi + 8], r8")
	w(".Lssa_bufexp_none:")
	w("\txor eax, eax")
	w("\tret")
	w(".Lssa_bufexp_grow:")
	// Three callee-saved pushes plus the return address leave rsp 16-aligned
	// for the call.
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tmov rbx, rdi")
	w("\tmov r12, rsi")
	w("\tmov r13, rdx")
	w("\tmov rsi, rax")
	w("\tcall %s", fnLabel("__fern_buf_reserve"))
	w("\tmov rdi, rbx")
	w("\tmov rsi, r12")
	w("\tmov rdx, r13")
	w("\tmov ecx, %s", memRef("rsi", -4))
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tjmp .Lssa_bufexp_fits")
}

// emitBufPushByteHelper writes buf_push_byte(H, x): append the low byte of x.
func emitBufPushByteHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("buf_push_byte"))
	w("\tmov rax, [rdi + 8]")
	w("\tadd rax, 1") // need
	w("\tcmp rax, [rdi + 16]")
	w("\tja .Lssa_bufbyte_grow")
	w(".Lssa_bufbyte_fits:")
	w("\tmov rcx, [rdi]")
	w("\tadd rcx, [rdi + 8]")
	w("\tmov byte ptr [rcx], sil")
	w("\tadd qword ptr [rdi + 8], 1")
	w("\txor eax, eax")
	w("\tret")
	w(".Lssa_bufbyte_grow:")
	// Two callee-saved pushes plus the return address leave rsp 8 mod 16; the
	// extra 8 realigns it for the call.
	w("\tpush rbx")
	w("\tpush r12")
	w("\tsub rsp, 8")
	w("\tmov rbx, rdi")
	w("\tmov r12d, esi")
	w("\tmov rsi, rax")
	w("\tcall %s", fnLabel("__fern_buf_reserve"))
	w("\tmov rdi, rbx")
	w("\tmov esi, r12d")
	w("\tadd rsp, 8")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tjmp .Lssa_bufbyte_fits")
}

// emitBufPushU64Helper writes buf_push_u64(H, v): append the eight bytes of
// v, least significant first. One store, where the byte form would be eight
// calls (#9221).
func emitBufPushU64Helper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("buf_push_u64"))
	w("\tmov rax, [rdi + 8]")
	w("\tadd rax, 8") // need
	w("\tcmp rax, [rdi + 16]")
	w("\tja .Lssa_bufu64_grow")
	w(".Lssa_bufu64_fits:")
	w("\tmov rcx, [rdi]")
	w("\tadd rcx, [rdi + 8]")
	w("\tmov qword ptr [rcx], rsi")
	w("\tadd qword ptr [rdi + 8], 8")
	w("\txor eax, eax")
	w("\tret")
	w(".Lssa_bufu64_grow:")
	// Two callee-saved pushes plus the return address leave rsp 8 mod 16; the
	// extra 8 realigns it for the call.
	w("\tpush rbx")
	w("\tpush r12")
	w("\tsub rsp, 8")
	w("\tmov rbx, rdi")
	w("\tmov r12, rsi")
	w("\tmov rsi, rax")
	w("\tcall %s", fnLabel("__fern_buf_reserve"))
	w("\tmov rdi, rbx")
	w("\tmov rsi, r12")
	w("\tadd rsp, 8")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tjmp .Lssa_bufu64_fits")
}

// emitBufLenHelper writes buf_len(H) -> count. Leaf.
func emitBufLenHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("buf_len"))
	w("\tmov rax, [rdi + 8]")
	w("\tret")
}

// emitBufTakeHelper writes buf_take(H) -> string: copy the accumulated bytes
// into a fresh string of their own length, and leave the builder holding its
// buffer at full width with nothing in it. An empty build allocates a
// zero-length string, so the reserve survives that path too.
//
// The copy is what keeps the block's size derivable from the string's length.
// Every string this backend frees is sized len + strBlockBytes by
// __fern_str_dec, which is sound only while each producer asked for exactly
// that. Handing the builder's own block over instead — it is cap +
// strBlockBytes — broke that: the block came back to the class for its LENGTH
// and the rest of the capacity was stranded below its real class, so a program
// that took from a builder in a loop bumped fresh arena every round and never
// reused any of it (#9542: 125 MiB over 400 takes of a 256 KiB builder, where
// the stack machine held flat). The flat x86-64 backend, which shares this
// string layout, has always copied here for the same reason.
//
// Copying costs len bytes per take, not cap, and it is not new work overall:
// giving the block away forced the next push to allocate a fresh full-width
// buffer, where keeping it reuses one buffer for the builder's whole life.
func emitBufTakeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("buf_take"))
	w("\tmov rax, [rdi + 8]") // len
	w("\ttest rax, rax")
	w("\tjz .Lssa_buftake_empty")
	w("\tmov r9, rax")
	w("\tlea r11, [r9 + %d]", strBlockBytes)
	w("\tsub rsp, 8") // entered 8 past alignment; the trampoline is called at 16
	ssaBumpAlloc(w, "r10", "r11")
	w("\tadd rsp, 8")
	w("\tmov dword ptr [r10], 1") // rc = 1
	w("\tmov [r10 + 4], r9d")     // len
	w("\tlea r11, [r10 + 8]")     // data
	// The NUL sits one past the copied range, so it is written before the copy
	// rather than after: emitBcopyCall takes rdi, and the builder's fields have
	// to be read and cleared while rdi still holds the handle.
	w("\tmov byte ptr [r11 + r9], 0")
	w("\tmov rax, [rdi]")             // src: the builder's buffer
	w("\tmov qword ptr [rdi + 8], 0") // empty, and still holding its buffer
	emitBcopyCall(w, "r11", "rax", "r9")
	w("\tmov rax, r11")
	w("\tret")
	w(".Lssa_buftake_empty:")
	w("\tsub rsp, 8") // entered 8 past alignment; the trampoline is called at 16
	w("\tmov esi, 1") // the NUL
	emitBufStrBlock(w, "rsi", "esi", "rax")
	w("\tmov dword ptr [rax - 4], 0") // len
	w("\tmov byte ptr [rax], 0")
	w("\tadd rsp, 8")
	w("\tret")
}

// emitBufFreeHelper writes buf_free(H): give the buffer back when the builder
// still owns one, then the control block.
func emitBufFreeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("buf_free"))
	w("\ttest rdi, rdi")
	w("\tjz .Lssa_buffree_ret")
	// One callee-saved push plus the return address leave rsp 16-aligned for
	// the calls below.
	w("\tpush rbx")
	w("\tmov rbx, rdi")
	w("\tmov rdi, [rbx]")
	w("\ttest rdi, rdi")
	w("\tjz .Lssa_buffree_ctl")
	w("\tmov rsi, [rbx + 16]")
	w("\tadd rsi, 1")
	w("\tcall %s", fnLabel("__fern_box_free"))
	w(".Lssa_buffree_ctl:")
	w("\tmov rdi, rbx")
	w("\tmov esi, 32")
	w("\tcall %s", fnLabel("__fern_box_free"))
	w("\tpop rbx")
	w(".Lssa_buffree_ret:")
	w("\txor eax, eax")
	w("\tret")
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

// emitAllocReuseHelper writes __alloc_reuse(token, tokenSize, size) -> base —
// the drop-reuse (FBIP) primitive. The token is the dropped value's block
// BASE (the IR subtracts the rc header before the call), both sizes count
// that header, and the result is a base the IR lays its own rc header on,
// exactly as with OpAlloc. A live token whose exact 16-byte class matches
// the request's is handed straight back, so the constructor writes its
// fields over the dropped value's with no allocation at all — the point of
// reuse. A null token allocates fresh; a mismatch releases the token and
// then allocates, so a mispaired reuse is slow, not wrong. Only the small
// tier is reused in place: a large class is 3-significant-bit, which the
// 16-byte compare does not decide, and the free-then-allocate path is right
// for it. rbx = the request across the calls.
func emitAllocReuseHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__alloc_reuse"))
	w("\tpush rbx")     // one push past the return address: 16-aligned for the calls
	w("\tmov ebx, edx") // size
	w("\ttest rdi, rdi")
	w("\tjz .Lssa_reuse_fresh") // null token: nothing to reuse
	w("\tmov eax, esi")
	w("\tadd rax, 15")
	w("\tand rax, -16") // class(tokenSize)
	w("\tlea rcx, [rbx + 15]")
	w("\tand rcx, -16") // class(size)
	w("\tcmp rax, rcx")
	w("\tjne .Lssa_reuse_mismatch")
	w("\tcmp rax, 2048")
	w("\tja .Lssa_reuse_mismatch")
	w("\tmov rax, rdi") // in place: the token IS the block
	w("\tjmp .Lssa_reuse_ret")
	w(".Lssa_reuse_mismatch:")
	w("\tmov esi, esi")
	w("\tcall %s", fnLabel("__free"))
	w(".Lssa_reuse_fresh:")
	w("\tmov edi, ebx")
	w("\tcall %s", fnLabel("__alloc"))
	w(".Lssa_reuse_ret:")
	w("\tpop rbx")
	w("\tret")
}

// emitDropArrElemHelper returns the emitter for name(ptr, stride) -> ptr — the
// scope-exit drop of an ARRAY that owns its elements: at rc == 1 the array is
// about to die, so each element's own reference goes first through elemDrop,
// then the array's through __fern_arr_dec. A SHARED array (rc != 1) walks
// nothing — the other owner still reads those elements — which is the same
// test the stack-machine backend makes. The guards (null, low address, static
// sentinel, rc underflow) are stated once each in the callees, so this is the
// walk alone.
//
// Two instantiations: __fern_drop_arr_str releases string elements through
// __fern_str_dec, and __fern_drop_arr_ptr releases rc-tracked elements
// (arrays, maps, structs) through __fern_rc_dec, which is what core/map needs
// for a Map's array-typed value column. tag keeps their labels apart.
func emitDropArrElemHelper(name, elemDrop, tag string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		emitDropArrElemBody(w, name, elemDrop, ".Lssa_"+tag+"_")
	}
}

func emitDropArrElemBody(w func(string, ...any), name, elemDrop, lbl string) {
	w("")
	w("%s:", fnLabel(name))
	w("\tpush rbp")
	w("\tmov rbp, rsp")
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tpush r14") // four pushes past rbp: rsp stays 16-aligned
	w("\tmov rbx, rdi")
	w("\tmov r14, rsi") // stride
	w("\tcmp rbx, 0x10000")
	w("\tjb %sret", lbl)
	w("\tmov eax, %s", memRef("rbx", -8)) // rc
	w("\ttest eax, eax")
	w("\tjs %sret", lbl) // static sentinel
	w("\tcmp eax, 1")
	w("\tjne %sarr", lbl)                  // shared: the elements are not ours to drop
	w("\tmov r12d, %s", memRef("rbx", -4)) // len
	w("\txor r13, r13")
	w("%sloop:", lbl)
	w("\tcmp r13, r12")
	w("\tjge %sarr", lbl)
	w("\tmov rax, r13")
	w("\timul rax, r14")
	w("\tmov rdi, [rbx + rax]") // element i, a string pointer
	w("\tcall %s", fnLabel(elemDrop))
	w("\tinc r13")
	w("\tjmp %sloop", lbl)
	w("%sarr:", lbl)
	w("\tmov rdi, rbx")
	w("\tmov rsi, r14")
	w("\tcall %s", fnLabel("__fern_arr_dec"))
	w("%sret:", lbl)
	w("\tmov rax, rbx")
	w("\tpop r14")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tpop rbp")
	w("\tret")
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
	w("\tsub rsp, 8") // entered 8 past alignment; the trampoline is called at 16
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
	w("\tadd r10, %d", strBlockBytes)
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
	w("\tadd rsp, 8")
	w("\tret")
	w(".Lssa_ioe_intr:")
	ssaBumpAlloc(w, "rax", "16") // 8 header + 8 (tag only)
	w("\tmov dword ptr [rax], 1")
	w("\tadd rax, 8")
	w("\tmov dword ptr [rax], 4") // tag = 4 (Interrupted)
	w("\tadd rsp, 8")
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
	w("\tadd rsp, 8")
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
	w("\tlea r10, [rcx + rdx + %d]", strBlockBytes+1) // childlen(plen+1+nlen) + the header and NUL
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
	// Result.Ok(()): the unit occupies a payload slot like any other value, so
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

// shiftSeq renders a shift or rotate. dst holds the value; the count is the
// immediate when the instruction carries one, else src, copied into rcx (which
// is preserved with push/pop so a live value there survives) and read as cl.
// dst is a scratch reg (never rcx), so `<op> dst, cl` is safe.
func shiftSeq(in Inst) string {
	var mnem string
	switch in.K {
	case ssa.OpShl:
		mnem = "shl"
	case ssa.OpShr:
		mnem = "sar" // arithmetic (signed) right shift
	case ssa.OpShrU:
		mnem = "shr" // logical (unsigned) right shift
	case ssa.OpRotr:
		mnem = "ror"
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
	bits := int64(64)
	if in.W != 64 {
		dst = reg32[in.Dst]
		bits = 32
	}
	if in.SrcImm {
		// A constant count is the instruction's own imm8, masked as the
		// register form's would be.
		return fmt.Sprintf("%s %s, %d", mnem, dst, in.Imm&(bits-1))
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
