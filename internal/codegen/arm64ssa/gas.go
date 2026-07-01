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
	"sort"
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

// EmitAsm lowers a single integer SSA function to a complete, runnable AArch64
// GAS program. It is EmitAsmModule with a one-function module whose entry is f.
func EmitAsm(f *ssa.Func, numAlloc int, entryArgs ...int64) (string, error) {
	return EmitAsmModule(map[string]*ssa.Func{f.Name: f}, f.Name, numAlloc, entryArgs)
}

// EmitAsmModule lowers a set of SSA functions to one runnable AArch64 GAS
// program: a `_start` that loads entryArgs into the argument registers, calls
// the entry, and exits with its return value in the low byte, followed by each
// function emitted under a unique label. Direct calls (`OpCall`) between the
// functions are lowered to the AArch64 PCS — args in x0..x7, result in x0. The
// whole allocatable file (x0..x15) is caller-saved under the PCS, so every
// call-crossing value is preserved by the caller (the allocator marks them via
// EmitWithCalleeSaved's all-caller-saved partition).
func EmitAsmModule(funcs map[string]*ssa.Func, entry string, numAlloc int, entryArgs []int64) (string, error) {
	if _, ok := funcs[entry]; !ok {
		return "", fmt.Errorf("arm64ssa: unknown entry %q", entry)
	}
	names := make([]string, 0, len(funcs))
	for name := range funcs {
		names = append(names, name)
	}
	sort.Strings(names)

	progs := make(map[string]*x86.Program, len(funcs))
	for _, name := range names {
		// nil callee-saved partition: on arm64 the whole abstract file maps onto
		// x0..x15, all caller-saved, so no register survives a call on its own.
		p, err := x86.EmitWithCalleeSaved(funcs[name], numAlloc, nil)
		if err != nil {
			return "", fmt.Errorf("arm64ssa: emit %q: %w", name, err)
		}
		if p.NumRegFile > len(armX) {
			return "", fmt.Errorf("arm64ssa: %q needs %d registers but only %d are wired", name, p.NumRegFile, len(armX))
		}
		if len(p.ParamLocs) > argRegCount {
			return "", fmt.Errorf("arm64ssa: %q has %d params, arm64 SSA supports up to %d", name, len(p.ParamLocs), argRegCount)
		}
		progs[name] = p
	}

	var b strings.Builder
	w := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	heap := usesHeap(progs)

	w(".text")
	w(".globl _start")
	w("_start:")
	// Initialise the bump-allocator cursor to the base of the .bss heap. x9/x10
	// are scratch here (before the entry args are loaded into x0..x7).
	if heap {
		w("\tadrp x9, %s", heapSym)
		w("\tadd x9, x9, #:lo12:%s", heapSym)
		w("\tadrp x10, %s", heapPtrSym)
		w("\tadd x10, x10, #:lo12:%s", heapPtrSym)
		w("\tstr x9, [x10]")
	}
	// Load the entry arguments into the argument registers before the call.
	for i := range progs[entry].ParamLocs {
		var v int64
		if i < len(entryArgs) {
			v = entryArgs[i]
		}
		w("\tmov %s, #%d", xreg(i), v)
	}
	w("\tbl %s", fnLabel(entry))
	// exit_group(status): status is the function's return value in x0; the kernel
	// keeps the low byte. x8 = 94 (exit_group on AArch64 Linux).
	w("\tmov x8, #94")
	w("\tsvc #0")
	w("")
	for _, name := range names {
		if err := emitFunc(w, name, progs[name], numAlloc); err != nil {
			return "", err
		}
		w("")
	}
	// Runtime-helper bodies (hand-written leaf asm, AArch64 PCS: arg/result in
	// x0), emitted only when the module calls them.
	for _, h := range referencedRuntimeHelpers(progs) {
		runtimeHelperEmitters[h](w)
	}
	if heap {
		// A fixed .bss buffer backs the bump allocator (mirrors the x86-64 SSA
		// path). Under the W^X ELF layout this lands in the R+W data segment, so
		// stores to it succeed. The cursor sits just before the buffer.
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

// Heap symbols + size backing the arm64 SSA bump allocator, mirroring the x86-64
// path's fixed .bss buffer (a lazy mmap/brk allocator is future work).
const (
	heapPtrSym = "__ssa_heap_ptr"
	heapSym    = "__ssa_heap"
	heapBytes  = 1 << 16 // 64 KiB
)

// usesHeap reports whether any program contains a heap op (so the .bss heap
// section + cursor are emitted and initialised only when needed).
func usesHeap(progs map[string]*x86.Program) bool {
	for _, p := range progs {
		for _, blk := range p.Blocks {
			for _, in := range blk.Insts {
				switch in.Op {
				case x86.MemAlloc, x86.MemLoad, x86.MemStore:
					return true
				}
			}
		}
	}
	return false
}

// runtimeHelperEmitters maps a __fern_* runtime-helper name to the code that
// writes its AArch64 body into .text — the arm64 siblings of the x86-64 helper
// emitters, mirroring the native backends' hand-written runtime asm
// (docs/SSA-RC-RUNTIME.md). A helper is emitted only when the module calls it
// (referencedRuntimeHelpers); its `bl fn_<name>` site links the label
// fnLabel(name) writes. Leaf functions under the AArch64 PCS (arg/result x0).
var runtimeHelperEmitters = map[string]func(w func(string, ...any)){
	"__fern_rc_is_unique": emitRcIsUniqueHelper,
	"__fern_rc_inc":       emitRcIncHelper,
	"__fern_rc_dec":       emitRcDecHelper,
}

// referencedRuntimeHelpers returns, sorted, every runtime helper any emitted
// program calls (that arm64 has an emitter for). Helper→helper dependencies are
// added when those helpers land; the RC leaves have none.
func referencedRuntimeHelpers(progs map[string]*x86.Program) []string {
	seen := map[string]bool{}
	for _, p := range progs {
		for _, blk := range p.Blocks {
			for _, in := range blk.Insts {
				if in.Op == x86.Call && runtimeHelperEmitters[in.Callee] != nil {
					seen[in.Callee] = true
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

// The RC helpers read a 4-byte reference count at [data-8] (the header the bump
// allocator lays down; data = base+8). They share a guard chain that makes them
// safe on a slot that might hold a non-pointer scalar or a static cell: null,
// below the 0x10000 low-address guard, or the static-sentinel top bit (0x80000000)
// set — all short-circuit. The negative header offset needs the unscaled
// ldur/stur form. Mirrors the native / x86-64 SSA versions.

// emitRcIsUniqueHelper writes __fern_rc_is_unique(data) -> i32: 1 iff data is a
// real, uniquely-owned heap value (rc == 1), else 0.
func emitRcIsUniqueHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rc_is_unique"))
	w("\tcbz x0, .Lssa_rcuniq_no")
	w("\tcmp x0, #0x10000")
	w("\tb.lo .Lssa_rcuniq_no")
	w("\tldur w1, [x0, #-8]") // rc word at data-8
	w("\ttbnz w1, #31, .Lssa_rcuniq_no")
	w("\tcmp w1, #1")
	w("\tb.ne .Lssa_rcuniq_no")
	w("\tmov x0, #1")
	w("\tret")
	w(".Lssa_rcuniq_no:")
	w("\tmov x0, #0")
	w("\tret")
}

// emitRcIncHelper writes __fern_rc_inc(data): bump the count at [data-8] by one,
// guarded. Void, leaf.
func emitRcIncHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rc_inc"))
	w("\tcbz x0, .Lssa_rcinc_ret")
	w("\tcmp x0, #0x10000")
	w("\tb.lo .Lssa_rcinc_ret")
	w("\tldur w1, [x0, #-8]")
	w("\ttbnz w1, #31, .Lssa_rcinc_ret") // static sentinel
	w("\tadd w1, w1, #1")
	w("\tstur w1, [x0, #-8]")
	w(".Lssa_rcinc_ret:")
	w("\tret")
}

// emitRcDecHelper writes __fern_rc_dec(data): drop the count at [data-8] by one,
// same guard chain. Does NOT free at rc==0 — the SSA bump heap never reclaims
// (docs/SSA-RC-RUNTIME.md). Void, leaf.
func emitRcDecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rc_dec"))
	w("\tcbz x0, .Lssa_rcdec_ret")
	w("\tcmp x0, #0x10000")
	w("\tb.lo .Lssa_rcdec_ret")
	w("\tldur w1, [x0, #-8]")
	w("\ttbnz w1, #31, .Lssa_rcdec_ret") // static sentinel
	w("\tsub w1, w1, #1")
	w("\tstur w1, [x0, #-8]")
	w(".Lssa_rcdec_ret:")
	w("\tret")
}

// emitFunc writes one function: its label, a stack frame (spill slots, plus a
// call-save area and a saved-x30 slot when the function makes calls), each
// block's straight-line body under a namespaced label, and the terminators.
// The stack pointer stays fixed for the whole body — call-crossing registers are
// preserved in the reserved call-save area rather than by moving sp — so every
// slot access is a stable sp-relative offset.
func emitFunc(w func(string, ...any), name string, p *x86.Program, numAlloc int) error {
	label := fnLabel(name)
	call := funcHasCall(p)

	// Frame layout (8-byte slots): [0, NumSlots) spill slots; when the function
	// calls, [NumSlots, NumSlots+numAlloc) is the call-save area and NumSlots+
	// numAlloc holds the saved link register (x30, clobbered by `bl`).
	callSaveBase := p.NumSlots
	lrSlot := p.NumSlots + numAlloc
	nslots := p.NumSlots
	if call {
		nslots = lrSlot + 1
	}
	frame := align16(8 * nslots)
	scratch := p.NumRegFile - 1 // result-capture scratch; above the allocatable file

	w("%s:", label)
	if frame > 0 {
		w("\tsub sp, sp, #%d", frame)
	}
	if call {
		w("\tstr x30, [sp, #%d]", 8*lrSlot)
	}
	// Parameter ABI: move each incoming argument register into its param's home.
	// Must follow the frame setup (slot-homed params store to [sp]).
	for _, l := range paramMoveLines(p.ParamLocs) {
		w("\t%s", l)
	}

	ret := func(reg int) {
		if reg != 0 {
			w("\tmov x0, %s", xreg(reg))
		}
		if call {
			w("\tldr x30, [sp, #%d]", 8*lrSlot)
		}
		if frame > 0 {
			w("\tadd sp, sp, #%d", frame)
		}
		w("\tret")
	}

	for bi, blk := range p.Blocks {
		w(".L%s_b%d:", label, bi)
		for _, in := range blk.Insts {
			if in.Op == x86.Call {
				lines, err := callLines(in, numAlloc, scratch, callSaveBase)
				if err != nil {
					return err
				}
				for _, l := range lines {
					w("\t%s", l)
				}
				continue
			}
			lines, err := asmInst(in, scratch)
			if err != nil {
				return err
			}
			for _, l := range lines {
				w("\t%s", l)
			}
		}
		switch blk.Term.Kind {
		case x86.TRet:
			ret(blk.Term.RetReg)
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

// funcHasCall reports whether any block of p contains a direct call.
func funcHasCall(p *x86.Program) bool {
	for _, blk := range p.Blocks {
		for _, in := range blk.Insts {
			if in.Op == x86.Call {
				return true
			}
		}
	}
	return false
}

// callLines renders a direct call under the AArch64 PCS. The whole allocatable
// file is caller-saved, so the registers holding values live across the call
// (in.SaveRegs, computed by the allocator) are spilled to the reserved call-save
// area — sp stays fixed, so those saves and the arg-move slot loads share the
// same stable offsets. Arguments go into x0..x7 as a parallel register copy
// (reg-homed) plus slot loads; the result (x0) is captured into the scratch
// register — above the allocatable file, never in the save set — before the
// saved registers are restored, then placed into the destination.
func callLines(in x86.Inst, numAlloc, scratch, callSaveBase int) ([]string, error) {
	if len(in.ArgLocs) > argRegCount {
		return nil, fmt.Errorf("arm64ssa: call supports up to %d args, got %d", argRegCount, len(in.ArgLocs))
	}
	saved := callSavedSet(in, numAlloc)
	var out []string
	for k, r := range saved {
		out = append(out, fmt.Sprintf("str %s, [sp, #%d]", xreg(r), 8*(callSaveBase+k)))
	}
	out = append(out, argMoveLines(in.ArgLocs)...)
	out = append(out, fmt.Sprintf("bl %s", fnLabel(in.Callee)))
	out = append(out, fmt.Sprintf("mov %s, x0", xreg(scratch))) // capture result
	for k := len(saved) - 1; k >= 0; k-- {
		out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(saved[k]), 8*(callSaveBase+k)))
	}
	out = append(out, fmt.Sprintf("mov %s, %s", xreg(in.Dst), xreg(scratch))) // place result
	out = append(out, maskFix(in.Dst, in.W)...)
	return out, nil
}

// callSavedSet returns the caller-saved allocatable registers to preserve across
// a call. The allocator computes the live-across set (SaveRegsSet) from liveness;
// on arm64 every allocatable register is caller-saved, so absent that set the
// fallback saves the whole allocatable file.
func callSavedSet(in x86.Inst, numAlloc int) []int {
	if in.SaveRegsSet {
		return in.SaveRegs
	}
	saved := make([]int, 0, numAlloc)
	for r := 0; r < numAlloc; r++ {
		saved = append(saved, r)
	}
	return saved
}

// argMoveLines moves call arguments from their allocated homes into the AArch64
// argument registers (arg i → x{i}). The abstract file maps index i onto x{i},
// so reg-homed args are a parallel register copy over abstract indices (resolved
// by resolveRegMoves); slot-homed args load from [sp] afterward, by which point
// every reg-homed source has been consumed.
func argMoveLines(argLocs []x86.Loc) []string {
	var moves [][2]int // {dstArgReg=i, srcHomeReg}
	for i, l := range argLocs {
		if l.IsReg && l.Reg != i {
			moves = append(moves, [2]int{i, l.Reg})
		}
	}
	out := resolveRegMoves(moves)
	for i, l := range argLocs {
		if !l.IsReg {
			out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(i), 8*l.Slot))
		}
	}
	return out
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
		// Drop no-op self-moves. Redirecting a swap can turn the other half of a
		// 2-cycle into {r, r}, which is already satisfied — and unlike x86's
		// `xchg r, r`, a three-eor self-swap would ZERO the register, so these
		// must be discarded rather than emitted.
		for j := 0; j < len(moves); {
			if moves[j][0] == moves[j][1] {
				moves = append(moves[:j], moves[j+1:]...)
			} else {
				j++
			}
		}
		if len(moves) == 0 {
			break
		}
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

// asmInst renders one abstract instruction to AArch64 GAS lines. scratch is a
// free above-the-file register the remainder sequence stages its quotient
// through.
func asmInst(in x86.Inst, scratch int) ([]string, error) {
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
		switch in.K {
		case ssa.OpShl, ssa.OpShr, ssa.OpShrU, ssa.OpDiv, ssa.OpDivU, ssa.OpRem, ssa.OpRemU:
			return divShiftSeq(in, scratch), nil
		}
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
	case x86.MemAlloc:
		return memAllocSeq(in, scratch), nil
	case x86.MemLoad:
		return memLoadSeq(in), nil
	case x86.MemStore:
		return memStoreSeq(in), nil
	default:
		return nil, fmt.Errorf("arm64ssa: opcode %d not supported yet", in.Op)
	}
}

// divShiftSeq renders the register-shift and divide/remainder BinOps, which
// AArch64 spells with dedicated mnemonics rather than a plain 3-operand ALU op.
// All operate on the full 64-bit (sign-extended) register values, exactly as the
// abstract model does before its width mask, so a trailing maskFix reproduces the
// i32 result. Remainder has no direct instruction: divide into the scratch
// register, then msub (Xd = Xa - Xn*Xm) yields dividend - quotient*divisor.
func divShiftSeq(in x86.Inst, scratch int) []string {
	d, s := xreg(in.Dst), xreg(in.Src)
	var out []string
	switch in.K {
	case ssa.OpShl:
		out = []string{fmt.Sprintf("lsl %s, %s, %s", d, d, s)}
	case ssa.OpShr:
		out = []string{fmt.Sprintf("asr %s, %s, %s", d, d, s)} // arithmetic (signed)
	case ssa.OpShrU:
		out = []string{fmt.Sprintf("lsr %s, %s, %s", d, d, s)} // logical (unsigned)
	case ssa.OpDiv:
		out = []string{fmt.Sprintf("sdiv %s, %s, %s", d, d, s)}
	case ssa.OpDivU:
		out = []string{fmt.Sprintf("udiv %s, %s, %s", d, d, s)}
	case ssa.OpRem, ssa.OpRemU:
		q := xreg(scratch)
		div := "sdiv"
		if in.K == ssa.OpRemU {
			div = "udiv"
		}
		out = []string{
			fmt.Sprintf("%s %s, %s, %s", div, q, d, s), // q = d / s
			fmt.Sprintf("msub %s, %s, %s, %s", d, q, s, d), // d = d - q*s
		}
	}
	return append(out, maskFix(in.Dst, in.W)...)
}

// memAllocSeq renders the bump allocator. It mirrors the x86-64 SSA layout: an
// 8-byte rc header (rc=1 at base+0) precedes the data, and the returned pointer
// is base+8, so the RC drop helpers (a later slice) find a valid count at
// [data-8]. base = align8(cursor); the cursor advances past header+size.
//
// Unlike x86 (which stores the rc immediate straight to memory and addresses the
// cursor RIP-relative, needing one staging register), AArch64 must form the
// cursor address in a register and hold the rc value in a register. So it needs
// two temporaries beyond the destination; they are drawn from the scratch pool
// (the top four registers) avoiding Dst and Src — the emitter may itself have
// homed this op's result in a low scratch register (s0..s2), so a fixed offset
// from `scratch` could otherwise alias the base pointer mid-sequence.
func memAllocSeq(in x86.Inst, scratch int) []string {
	var tmps []int
	for i := 0; i < 4 && len(tmps) < 2; i++ {
		r := scratch - i
		if r == in.Dst || r == in.Src {
			continue
		}
		tmps = append(tmps, r)
	}
	d := xreg(in.Dst)
	addr := xreg(tmps[0])
	tmp := xreg(tmps[1])
	wtmp := wreg(tmps[1])
	return []string{
		fmt.Sprintf("adrp %s, %s", addr, heapPtrSym),
		fmt.Sprintf("add %s, %s, #:lo12:%s", addr, addr, heapPtrSym),
		fmt.Sprintf("ldr %s, [%s]", d, addr), // d = cursor
		fmt.Sprintf("add %s, %s, #7", d, d),
		fmt.Sprintf("and %s, %s, #-8", d, d), // d = base (8-aligned)
		fmt.Sprintf("mov %s, #1", wtmp),
		fmt.Sprintf("str %s, [%s]", wtmp, d), // rc = 1 (4 bytes at base)
		fmt.Sprintf("add %s, %s, %s", tmp, d, xreg(in.Src)),
		fmt.Sprintf("add %s, %s, #8", tmp, tmp), // header bytes
		fmt.Sprintf("str %s, [%s]", tmp, addr),  // cursor = base + size + 8
		fmt.Sprintf("add %s, %s, #8", d, d),     // return data = base + 8
	}
}

// memLoadSeq renders a load of in.Bytes bytes from [Src + Imm] into Dst. It
// mirrors the x86-64 widths: 8-byte loads move a full word; 4-byte loads move
// the low word (zero-extended); sub-word loads sign- or zero-extend per
// in.Signed. The trailing maskFix reproduces the model's i32 width mask.
func memLoadSeq(in x86.Inst) []string {
	d, dw := xreg(in.Dst), wreg(in.Dst)
	mem := fmt.Sprintf("[%s, #%d]", xreg(in.Src), in.Imm)
	var out []string
	switch in.Bytes {
	case 8:
		out = []string{fmt.Sprintf("ldr %s, %s", d, mem)}
	case 4:
		out = []string{fmt.Sprintf("ldr %s, %s", dw, mem)} // zero-extends to 64
	case 2:
		if in.Signed {
			out = []string{fmt.Sprintf("ldrsh %s, %s", d, mem)}
		} else {
			out = []string{fmt.Sprintf("ldrh %s, %s", dw, mem)}
		}
	default: // 1 byte
		if in.Signed {
			out = []string{fmt.Sprintf("ldrsb %s, %s", d, mem)}
		} else {
			out = []string{fmt.Sprintf("ldrb %s, %s", dw, mem)}
		}
	}
	return append(out, maskFix(in.Dst, in.W)...)
}

// memStoreSeq renders a store of the low in.Bytes bytes of Src2 to [Src + Imm].
func memStoreSeq(in x86.Inst) []string {
	mem := fmt.Sprintf("[%s, #%d]", xreg(in.Src), in.Imm)
	switch in.Bytes {
	case 1:
		return []string{fmt.Sprintf("strb %s, %s", wreg(in.Src2), mem)}
	case 2:
		return []string{fmt.Sprintf("strh %s, %s", wreg(in.Src2), mem)}
	case 4:
		return []string{fmt.Sprintf("str %s, %s", wreg(in.Src2), mem)}
	default: // 8 bytes
		return []string{fmt.Sprintf("str %s, %s", xreg(in.Src2), mem)}
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
