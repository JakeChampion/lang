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
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/fdlibm"
	x86 "github.com/jakechampion/lang/internal/codegen/x86_64ssa"
	"github.com/jakechampion/lang/internal/fernrt"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/ssa"
)

// armX / armW map an abstract register index to its 64-bit / 32-bit AArch64
// register name. Abstract indices [0, numAlloc) are the allocatable file and
// [numAlloc, numAlloc+4) the scratch pool, so the ORDER of this table decides
// which physical registers each role gets:
//
//	 0..11  x0..x11    caller-saved (temporaries under the AArch64 PCS)
//	12..21  x19..x28   callee-saved (preserved by the callee, including every
//	                   runtime helper in this file — pinned by
//	                   TestRuntimeHelpersPreserveCalleeSaved)
//	22..25  x12..x15   caller-saved, the scratch pool at the default numAlloc
//
// Two properties are load-bearing. Indices 0..7 must stay x0..x7 because
// argMoveLines / paramMoveLines treat the incoming argument register for arg i
// as abstract register i. And the callee-saved run must be contiguous and below
// the scratch pool at the default numAlloc, so a call-crossing value the
// allocator steered into x19..x28 costs one prologue save instead of a
// store/reload at every call it spans.
//
// A numAlloc below maxAlloc simply shortens the allocatable file and slides the
// scratch pool down into the callee-saved run; that stays correct (the prologue
// saves whatever the body touches) and only costs the extra saves.
var armX = []string{
	"x0", "x1", "x2", "x3", "x4", "x5", "x6", "x7", "x8", "x9", "x10", "x11",
	"x19", "x20", "x21", "x22", "x23", "x24", "x25", "x26", "x27", "x28",
	"x12", "x13", "x14", "x15",
}
var armW = []string{
	"w0", "w1", "w2", "w3", "w4", "w5", "w6", "w7", "w8", "w9", "w10", "w11",
	"w19", "w20", "w21", "w22", "w23", "w24", "w25", "w26", "w27", "w28",
	"w12", "w13", "w14", "w15",
}

// maxAlloc is the largest allocatable file the mapping supports: the 22 entries
// before the scratch pool.
const maxAlloc = 22

// DefaultNumAlloc is the allocatable-file size the compiler driver uses. At this
// size the scratch pool lands on x12..x15, the caller-saved temporaries — any
// smaller and the pool slides into the callee-saved run and every function pays
// to save its own scratch registers.
const DefaultNumAlloc = maxAlloc

// firstCalleeSaved is the abstract index where the callee-saved run begins.
const firstCalleeSaved = 12

// armCalleeSaved reports whether abstract register index r maps onto a
// callee-saved AArch64 register (x19..x28).
func armCalleeSaved(r int) bool { return r >= firstCalleeSaved && r < maxAlloc }

// armCalleeSavedMask is the partition EmitWithCalleeSaved wants: one entry per
// allocatable register, true where it survives a call.
func armCalleeSavedMask(numAlloc int) []bool {
	mask := make([]bool, numAlloc)
	for r := range mask {
		mask[r] = armCalleeSaved(r)
	}
	return mask
}

func xreg(i int) string { return armX[i] }
func wreg(i int) string { return armW[i] }

// argRegCount is the number of integer argument registers under the AArch64 PCS
// (x0..x7). Arguments past it travel in the caller's outgoing-argument area —
// see frameLayout — rather than being a limit on how many a function may take.
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
// functions are lowered to the AArch64 PCS — args in x0..x7, result in x0.
// Part of the allocatable file maps onto the PCS callee-saved registers
// (armX), so a value live across a call can be kept in one over the call and
// saved once in the prologue instead of at every call it spans.
func EmitAsmModule(funcs map[string]*ssa.Func, entry string, numAlloc int, entryArgs []int64, vtables ...ir.VtableDecl) (string, error) {
	if _, ok := funcs[entry]; !ok {
		return "", fmt.Errorf("arm64ssa: unknown entry %q", entry)
	}
	if numAlloc > maxAlloc {
		return "", fmt.Errorf("arm64ssa: numAlloc %d exceeds the %d mapped allocatable registers", numAlloc, maxAlloc)
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

	progs := make(map[string]*x86.Program, len(funcs))
	for _, name := range names {
		p, err := x86.EmitWithCalleeSaved(funcs[name], numAlloc, armCalleeSavedMask(numAlloc))
		if err != nil {
			return "", fmt.Errorf("arm64ssa: emit %q: %w", name, err)
		}
		if p.NumRegFile > len(armX) {
			return "", fmt.Errorf("arm64ssa: %q needs %d registers but only %d are wired", name, p.NumRegFile, len(armX))
		}
		progs[name] = p
	}

	var b strings.Builder
	w := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	helpers, fern := referencedRuntimeHelpers(progs)
	// A helper written in Fern (internal/fernrt) is lifted and emitted as a
	// function of this module, under the label its callers already use. Its
	// own calls may reach further helpers, so the reference scan repeats
	// until nothing new is reached.
	for len(fern) > 0 {
		for _, name := range fern {
			_, irFn, err := fernrt.Func(name, 8)
			if err != nil {
				return "", err
			}
			f, err := ssa.LiftFromIRWith(irFn, ir.NewCallShapes(&ir.Program{Funcs: []*ir.Func{irFn}}))
			if err != nil {
				return "", fmt.Errorf("arm64ssa: lift %q: %w", name, err)
			}
			ssa.Optimize(f)
			if err := ssa.Verify(f); err != nil {
				return "", fmt.Errorf("arm64ssa: verify %q: %w", name, err)
			}
			ssa.ResolveWidths(map[string]*ssa.Func{name: f})
			p, err := x86.EmitWithCalleeSaved(f, numAlloc, armCalleeSavedMask(numAlloc))
			if err != nil {
				return "", fmt.Errorf("arm64ssa: emit %q: %w", name, err)
			}
			if p.NumRegFile > len(armX) {
				return "", fmt.Errorf("arm64ssa: %q needs %d registers but only %d are wired", name, p.NumRegFile, len(armX))
			}
			progs[name] = p
			names = append(names, name)
		}
		sort.Strings(names)
		helpers, fern = referencedRuntimeHelpers(progs)
	}
	heap := usesHeap(progs)
	for _, h := range helpers {
		if heapUsingHelpers[h] {
			heap = true // e.g. __str_concat bump-allocates even with no direct heap op
			break
		}
	}
	if heap && !slices.Contains(helpers, "__alloc") {
		// Every inline allocation site reaches __alloc through the trampoline.
		helpers = append(helpers, "__alloc")
		sort.Strings(helpers)
	}
	withArgs := usesArgs(helpers)
	withStrbuf := usesStrbuf(helpers)
	withMapSeed := usesMapSeed(helpers)
	withReadLine := usesReadLine(helpers)
	withEnv := usesEnv(helpers)
	withRcUnderflow := usesRcUnderflow(helpers)
	strLabels, strOrder := collectStrings(progs, names)
	sentLabels, sentOrder := collectSentinels(progs, names)
	// fn_idx for closures: a function's index in the module's (sorted) emission
	// order — the value a closure cell carries, and the index into the function-
	// address table (fnTableSym) that OpCallIndirect dereferences. Indices are
	// 1-based: table slot 0 is the reserved null reference (see fnTableSym).
	fnIndex := make(map[string]int, len(names))
	for i, n := range names {
		fnIndex[n] = i + 1
	}

	w(".text")
	w(".globl _start")
	w("_start:")
	if withArgs {
		// Capture argc/argv from the process stack before any frame setup: the
		// kernel leaves argc at [sp] and the argv[] vector at sp+8. Stash both into
		// .bss globals the args() helper reads on demand. x9/x10 are scratch (entry
		// args aren't loaded yet).
		w("\tldr x9, [sp]") // argc
		w("\tadrp x10, %s", argcSym)
		w("\tadd x10, x10, #:lo12:%s", argcSym)
		w("\tstr x9, [x10]")
		w("\tadd x9, sp, #8") // &argv[0]
		w("\tadrp x10, %s", argvSym)
		w("\tadd x10, x10, #:lo12:%s", argvSym)
		w("\tstr x9, [x10]")
	}
	if withEnv {
		// envp sits just past argv's NULL terminator: [sp]=argc, argv[] at sp+8
		// (argc entries + a NULL), so envp = sp + 16 + argc*8. Stash it for env().
		w("\tldr x9, [sp]") // argc
		w("\tadd x10, sp, #16")
		w("\tadd x10, x10, x9, lsl #3") // envp = sp + 16 + argc*8
		w("\tadrp x11, %s", envpSym)
		w("\tadd x11, x11, #:lo12:%s", envpSym)
		w("\tstr x10, [x11]")
	}
	if heap {
		emitHeapReserve(w)
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
		if err := emitFunc(w, name, progs[name], numAlloc, strLabels, sentLabels, fnIndex); err != nil {
			return "", err
		}
		w("")
	}
	// Runtime-helper bodies (hand-written leaf asm, AArch64 PCS: arg/result in
	// x0), emitted only when the module calls them.
	emitRuntimeHelpers(w, helpers)
	if heap {
		emitHeapGuard(w)
		emitAllocPres(w)
	}
	if usesBcopy(helpers) {
		emitBcopy(w)
	}
	if usesTranscendentals(helpers) {
		// Shared .rodata coefficient table for the exp/log/pow polynomials, emitted
		// once (the helper bodies above reference its labels via adrp/:lo12:).
		emitTranscendentalRodata(w)
	}
	if len(strOrder) > 0 {
		// Length-prefixed single-word string literals (mirrors the native backends
		// + the x86-64 SSA path): an 8-byte header — an immortal rc sentinel
		// (0x80000000, top bit set) at [data-8] and the 4-byte byte-length at
		// [data-4] — sits immediately before the data, so the string pointer is the
		// data pointer. The sentinel makes __fern_str_dec / rc helpers short-circuit
		// on literals (they never free .rodata). Consecutive literals stay
		// contiguous, so each label's header is exactly the 8 bytes before it.
		w(".section .rodata")
		for _, s := range strOrder {
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
		// the tag at offset 0 (every match / try site reads it with the same
		// [ptr+0] load a heap `[tag=N]` box uses). Each cell carries the same
		// 8-byte immortal header as a string literal — rc sentinel (0x80000000) at
		// [ptr-8], padding at [ptr-4] — so a scope-exit drop short-circuits on it
		// instead of writing a read-only/​shared cell. Consecutive cells stay
		// contiguous, so each label's header is exactly the 8 bytes before it.
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
		// carries the immortal 8-byte header a string literal does — rc sentinel
		// (0x80000000) at [ptr-8], padding at [ptr-4] — so inc / dec / is_unique
		// and closure_drop all short-circuit rather than write a read-only cell,
		// and the reuse pass never takes one as a token (is_unique reads false).
		w(".section .rodata")
		for _, t := range targets {
			w(".align 3")
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
		// this table. The assembler resolves each `.quad fn_<name>` to the label's
		// address.
		w(".section .rodata")
		w(".align 8")
		w("%s:", fnTableSym)
		w("\t.quad 0")
		for _, name := range names {
			w("\t.quad %s", fnLabel(name))
		}
	}
	if keys := referencedVtables(progs); len(keys) > 0 {
		// Static `dyn Trait` vtables: one `.rodata` cell per (trait-set,
		// concrete) pair an OpConstVtable materialised, holding the concrete
		// type's `__method_*` implementations as absolute function pointers in
		// trait declaration order (docs/DYN-TRAITS.md §4.2.2). OpCallDyn loads
		// slot k (`vtable + k*8`) and `blr`s through it. Each method points at a
		// module function, so the pointer is that function's `fn_<name>` label.
		// The trailing per-type drop slot the native backend emits (§4.4) is
		// omitted here: this path does not wire `dyn` RC (no DynRcSupported), so
		// nothing reads it and its drop fn is never emitted.
		byPair := map[string]ir.VtableDecl{}
		for _, vt := range vtables {
			byPair[vt.Trait+"/"+vt.Concrete] = vt
		}
		w(".section .rodata")
		for _, key := range keys {
			w(".align 8")
			tr, co := splitDynPair(key)
			w("%s:", dynVtableLabel(tr, co))
			vt, ok := byPair[key]
			if !ok {
				// OpConstVtable only names pairs collectVtables produced; a missing
				// entry would be a lowering bug. Emit an empty labelled cell so the
				// link fails on the dispatch, not an undefined symbol.
				continue
			}
			for _, m := range vt.Methods {
				w("\t.quad %s", fnLabel(m.Func))
			}
		}
	}
	if heap {
		// The bump allocator's cursor and its limit. Both are seeded by the
		// mmap reservation _start makes (emitHeapReserve); zero here means the
		// reservation has not run, which cannot happen on a path that allocates.
		w(".section .bss")
		w(".align 8")
		w("%s:", heapPtrSym)
		w("\t.quad 0")
		w("%s:", heapEndSym)
		w("\t.quad 0")
		// The reservation's base, kept so __fern_heap_bump_bytes can report
		// the high-water mark as (cursor - base). The cursor alone cannot:
		// mmap picks the base, so it is not a compile-time constant.
		w("%s:", heapBaseSym)
		w("\t.quad 0")
		// Segregated freelist heads, one intrusive list per size class: 0..127
		// the 16-byte exact-fit classes (16..2048 B), 128+ the large tier's
		// 3-significant-bit classes. Same layout as the flat backend's.
		w("%s:", freelistSym)
		w("\t.space 2048")
	}
	if withArgs {
		// argc / argv snapshot + the memoised args() container pointer.
		w(".section .bss")
		w(".align 8")
		w("%s:", argcSym)
		w("\t.quad 0")
		w("%s:", argvSym)
		w("\t.quad 0")
		w("%s:", argsCacheSym)
		w("\t.quad 0")
	}
	if withEnv {
		// envp snapshot for the env() helper.
		w(".section .bss")
		w(".align 8")
		w("%s:", envpSym)
		w("\t.quad 0")
	}
	if withRcUnderflow {
		w(".section .bss")
		w(".align 8")
		w("%s:", rcUnderflowSym)
		w("\t.quad 0")
	}
	if withStrbuf {
		// The string-builder's length counter + byte buffer. BSS, so the large
		// buffer costs no file space; it lands in the R+W data segment under the
		// W^X layout, so appends can write to it.
		w(".section .bss")
		w(".align 8")
		w("%s:", strbufLenSym)
		w("\t.quad 0")
		w(".align 8")
		w("%s:", strbufDataSym)
		w("\t.space %d", strbufBytes)
	}
	if withMapSeed {
		// core/map's per-process string-hash seed. One word, zero-initialised —
		// and zero doubles as "not yet drawn", which is why the drawn value is
		// forced nonzero (see emitMapHashSeedHelper).
		w(".section .bss")
		w(".align 8")
		w("%s:", mapSeedSym)
		w("\t.quad 0")
	}
	if withReadLine {
		// The Reader.read_line scratch buffer (reused across calls; NOBITS).
		w(".section .bss")
		w(".align 8")
		w("%s:", readlineBufSym)
		w("\t.space %d", readlineBytes)
	}
	w(".section .note.GNU-stack,\"\",@progbits")
	asm := b.String()
	if err := checkNoDanglingCalls(asm); err != nil {
		return "", err
	}
	return asm, nil
}

// Heap symbols + size backing the arm64 SSA bump allocator: a lazy mmap
// reservation seeded in _start, mirroring the stack-machine backend's
// __fern_alloc arena (internal/codegen/arm64) — same 16 GiB MAP_NORESERVE
// window, same diagnostic and exit status when a bump runs past it. Pages
// commit only as they are touched, so the window costs nothing until a program
// grows into it.
//
// The window sits at 16 GiB rather than the stack-machine backend's 0x10000000
// so that EVERY address this backend hands out has bits above 31 set. Address
// arithmetic that narrows to a w-register is then wrong for the first
// allocation of the smallest program, instead of being invisible until a
// program grows past 2 GiB — the failure mode that hid #7329.
const (
	heapPtrSym   = "__ssa_heap_ptr"
	heapEndSym   = "__ssa_heap_end"
	heapBaseSym  = "__ssa_heap_base"
	heapGuardSym = "__ssa_heap_guard"
	freelistSym  = "__ssa_freelist_heads"
	allocPresSym = "__ssa_alloc_pres"
	bcopySym     = "__ssa_bcopy"
	heapOOMLabel = ".Lssa_heap_oom"
	heapOOMMsg   = "__ssa_msg_oom"

	// The reservation as movz + lsl operands: heapUnits << heapShift bytes at
	// 1 << heapBaseShift, so _start needs no literal pool to materialise either.
	heapBaseShift = 34 // 0x4_0000_0000 (16 GiB)
	heapShift     = 28
	heapUnits     = 64 // 64 << 28 = 16 GiB
	heapBytes     = heapUnits << heapShift

	// The reservation's last page is not handed out: a bump site writes the
	// object's rc header at the new block's base BEFORE it publishes the cursor
	// and reaches the guard, so the bytes just past heapEndSym must still be
	// mapped for the guard to report exhaustion instead of faulting on that
	// header. Only headers (at most a few words above the base) are written
	// ahead of the guard; the payload fill always follows it.
	heapSlackBytes = 4096

	// argc / argv captured from the process stack by _start, and the memoised
	// string[] the args() helper builds from them.
	argcSym      = "__ssa_argc"
	argvSym      = "__ssa_argv"
	argsCacheSym = "__ssa_args_cache"

	// envp captured from the process stack by _start, walked by env().
	envpSym = "__ssa_envp"

	// The Phase 3 over-release counter: __fern_rc_dec bumps it when it is asked
	// to release a refcount that is already zero, and __fern_rc_underflow_count
	// reads it back. A test that asserts the count is zero is only meaningful if
	// the detector actually runs, so the two ship together.
	rcUnderflowSym = "__fern_rc_underflow"

	// The global string-builder: an 8-byte length counter and a fixed 64 MiB
	// .bss byte buffer that strbuf_append writes into and strbuf_take copies out
	// of, with no bounds check (the register backends grow theirs on demand;
	// this experimental one never builds the compiler). It costs no file space:
	// the W^X ELF writer stores the data segment only up to its last non-zero
	// byte (p_filesz) and lets the loader zero-fill the rest via p_memsz, so the
	// whole zero-init buffer is NOBITS (see elf.imageWX / trailingTrimZeros).
	strbufLenSym  = "__ssa_strbuf_len"
	mapSeedSym    = "__ssa_map_seed"
	strbufDataSym = "__ssa_strbuf_data"
	strbufBytes   = 64 << 20 // 64 MiB (NOBITS — no file cost)

	// The Reader.read_line scratch buffer: read_line reads one byte at a time into
	// this fixed .bss buffer (reused across calls, so a read-loop doesn't leak the
	// bump heap), then copies the line into a right-sized string. 4 KiB; longer
	// lines are truncated (matching the native backend).
	readlineBufSym = "__ssa_readline_buf"
	readlineBytes  = 4096
)

// Where _start resumes once the reservation succeeded. The failure path
// branches to the abort unconditionally (±128 MiB) rather than testing straight
// to it: the abort sits past every emitted function, far outside a conditional
// branch's ±1 MiB reach in any non-trivial program.
const heapOKLabel = ".Lssa_heap_ok"

// emitHeapReserve emits _start's heap reservation: one lazy anonymous mmap
// backing the bump allocator, seeding both the cursor and the limit the guard
// checks. Mirrors the stack-machine backend's __fern_alloc arena
// (internal/codegen/arm64) — same size, MAP_NORESERVE so the window is not
// charged against the host's commit limit, and the same out-of-memory report
// when the kernel refuses it. x0..x11 are scratch here: the entry arguments are
// loaded after this.
func emitHeapReserve(w func(string, ...any)) {
	// mmap(1<<heapBaseShift, heapBytes, PROT_READ|PROT_WRITE,
	//      MAP_PRIVATE|MAP_ANONYMOUS|MAP_NORESERVE, -1, 0).
	w("\tmov x0, #1")
	w("\tlsl x0, x0, #%d", heapBaseShift)
	w("\tmov x1, #%d", heapUnits)
	w("\tlsl x1, x1, #%d", heapShift)
	w("\tmov x2, #3")      // PROT_READ|PROT_WRITE
	w("\tmov x3, #0x4022") // MAP_PRIVATE|MAP_ANONYMOUS|MAP_NORESERVE
	w("\tmov x4, #-1")     // fd
	w("\tmov x5, #0")      // offset
	w("\tmov x8, #222")    // mmap
	w("\tsvc #0")
	w("\ttbz x0, #63, %s", heapOKLabel)
	w("\tb %s", heapOOMLabel)
	w("%s:", heapOKLabel)
	w("\tmov x1, #%d", heapUnits)
	w("\tlsl x1, x1, #%d", heapShift)
	w("\tadd x10, x0, x1")
	w("\tsub x10, x10, #%d", heapSlackBytes) // limit = base + heapBytes - slack
	w("\tadrp x9, %s", heapPtrSym)
	w("\tadd x9, x9, #:lo12:%s", heapPtrSym)
	w("\tstr x0, [x9]") // cursor = region base
	w("\tadrp x12, %s", heapBaseSym)
	w("\tadd x12, x12, #:lo12:%s", heapBaseSym)
	w("\tstr x0, [x12]")
	w("\tadrp x11, %s", heapEndSym)
	w("\tadd x11, x11, #:lo12:%s", heapEndSym)
	w("\tstr x10, [x11]")
}

// emitHeapGuard emits __ssa_heap_guard and the out-of-memory abort it branches
// to. Every bump site calls the guard right after it publishes its new cursor:
// the arena is a fixed reservation, so a bump past its end has to abort with a
// diagnostic instead of handing back a pointer into unmapped memory.
//
// The guard preserves every register — x0/x1 on its own stack frame, x30 by the
// caller — and the condition flags with it: it compares by subtracting and
// testing the sign bit rather than with `cmp`, because the sites it is spliced
// into are hand-written helpers that may keep flags live across an allocation.
func emitHeapGuard(w func(string, ...any)) {
	w("")
	w("%s:", heapGuardSym)
	w("\tstp x0, x1, [sp, #-16]!")
	w("\tadrp x0, %s", heapPtrSym)
	w("\tadd x0, x0, #:lo12:%s", heapPtrSym)
	w("\tldr x1, [x0]")
	w("\tadrp x0, %s", heapEndSym)
	w("\tadd x0, x0, #:lo12:%s", heapEndSym)
	w("\tldr x0, [x0]")
	w("\tsub x0, x0, x1") // limit - cursor: negative once the arena is spent
	w("\ttbnz x0, #63, %s", heapOOMLabel)
	w("\tldp x0, x1, [sp], #16")
	w("\tret")
	// Exhausted: write the diagnostic to stderr and exit with the status the
	// native backends use for it (pinned across emitters by
	// internal/e2e/arena_exit_code_test.go).
	w("%s:", heapOOMLabel)
	w("\tmov x0, #2") // stderr
	w("\tadrp x1, %s", heapOOMMsg)
	w("\tadd x1, x1, #:lo12:%s", heapOOMMsg)
	w("\tmov x2, #%d", len(arm64.MsgArenaExhausted))
	w("\tmov x8, #64") // write
	w("\tsvc #0")
	w("\tmov x0, #%d", arm64.ExitArenaExhausted)
	w("\tmov x8, #94") // exit_group
	w("\tsvc #0")
	w(".section .rodata")
	w("%s:", heapOOMMsg)
	bytes := make([]string, len(arm64.MsgArenaExhausted))
	for i := 0; i < len(arm64.MsgArenaExhausted); i++ {
		bytes[i] = strconv.Itoa(int(arm64.MsgArenaExhausted[i]))
	}
	w("\t.byte %s", strings.Join(bytes, ", "))
	w(".text")
}

// emitBcopy emits __ssa_bcopy(dst, src, n): a forward byte copy of n bytes,
// 16 bytes per iteration through the bulk, one 8-byte step, then a byte tail of
// at most 7. Regions must not overlap.
//
// It clobbers only x0, x1, x2 (its arguments) and x16 / x17 (IP0 / IP1, which a
// veneer already clobbers across any call), so the helpers that call it keep
// their live values in x3..x15 and need no frame beyond stacking x30 — the same
// arrangement __ssa_heap_guard uses. Every string and array copy in this backend
// routes here; each one previously open-coded a byte-at-a-time loop costing five
// instructions per byte, which dominated concatenation, slicing and array
// growth.
func emitBcopy(w func(string, ...any)) {
	w("")
	w("%s:", bcopySym)
	w("\tcmp x2, #16")
	w("\tb.lo .Lssa_bcp_8")
	w(".Lssa_bcp_16:")
	w("\tldp x16, x17, [x1], #16")
	w("\tstp x16, x17, [x0], #16")
	w("\tsub x2, x2, #16")
	w("\tcmp x2, #16")
	w("\tb.hs .Lssa_bcp_16")
	w(".Lssa_bcp_8:")
	w("\tcmp x2, #8")
	w("\tb.lo .Lssa_bcp_1")
	w("\tldr x16, [x1], #8")
	w("\tstr x16, [x0], #8")
	w("\tsub x2, x2, #8")
	w(".Lssa_bcp_1:")
	w("\tcbz x2, .Lssa_bcp_done")
	w(".Lssa_bcp_byte:")
	w("\tldrb w16, [x1], #1")
	w("\tstrb w16, [x0], #1")
	w("\tsubs x2, x2, #1")
	w("\tb.ne .Lssa_bcp_byte")
	w(".Lssa_bcp_done:")
	w("\tret")
}

// bcopyCallLines copies n bytes from src to dst through __ssa_bcopy. dst, src
// and n name the x-registers holding the three arguments; they are moved into
// x0..x2 in an order that survives any overlap between the argument registers
// themselves. x30 is stacked because the callers are otherwise leaf helpers.
func bcopyCallLines(dst, src, n string) []string {
	var out []string
	// x2 first, then x1, then x0: each move's destination is not read by a
	// later one, so an argument already sitting in x0 or x1 is not lost.
	for _, mv := range [][2]string{{"x2", n}, {"x1", src}, {"x0", dst}} {
		if mv[0] != mv[1] {
			out = append(out, fmt.Sprintf("mov %s, %s", mv[0], mv[1]))
		}
	}
	return append(out,
		"stp x29, x30, [sp, #-16]!",
		"bl "+bcopySym,
		"ldp x29, x30, [sp], #16",
	)
}

// emitBcopyCall writes bcopyCallLines through a line writer.
func emitBcopyCall(w func(string, ...any), dst, src, n string) {
	for _, l := range bcopyCallLines(dst, src, n) {
		w("\t%s", l)
	}
}

// bcopyUsingHelpers are the runtime helpers that call __ssa_bcopy, so the shared
// routine is emitted whenever one of them is.
var bcopyUsingHelpers = map[string]bool{
	"__memcpy":                    true,
	"__str_concat":                true,
	"__str_slice":                 true,
	"__fern_arr_push_grow":        true,
	"__fern_arr_cow_inplace":      true,
	"string_from_bytes_unchecked": true,
	"strbuf_append":               true,
	"strbuf_take":                 true,
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

// emitAllocPres writes __ssa_alloc_pres, the register-preserving way into
// __alloc for an allocation spliced inline into compiled code or a hand-written
// helper: size in x16, block base back in x16, every other register and the
// condition flags exactly as they were. x16/x17 are the only registers the
// allocator never homes a value in, which is what makes the splice safe
// without knowing what is live around it.
func emitAllocPres(w func(string, ...any)) {
	w("")
	w("%s:", allocPresSym)
	w("\tstp x29, x30, [sp, #-112]!")
	w("\tmov x29, sp")
	w("\tstp x0, x1, [sp, #16]")
	w("\tstp x2, x3, [sp, #32]")
	w("\tstp x4, x5, [sp, #48]")
	w("\tstp x6, x7, [sp, #64]")
	w("\tstp x8, x9, [sp, #80]")
	w("\tstp x10, x11, [sp, #96]")
	w("\tmrs x17, nzcv")
	w("\tmov x0, x16")
	w("\tbl %s", fnLabel("__alloc"))
	w("\tmov x16, x0")
	w("\tmsr nzcv, x17")
	w("\tldp x0, x1, [sp, #16]")
	w("\tldp x2, x3, [sp, #32]")
	w("\tldp x4, x5, [sp, #48]")
	w("\tldp x6, x7, [sp, #64]")
	w("\tldp x8, x9, [sp, #80]")
	w("\tldp x10, x11, [sp, #96]")
	w("\tldp x29, x30, [sp], #112")
	w("\tret")
}

// allocPresLines is the inline splice: total block size in x16 on entry, the
// 16-aligned block base in x16 on exit; x17 and the flags survive too (the
// trampoline restores them), and x30 is stacked because the site may be a leaf
// helper that keeps its return address there.
func allocPresLines() []string {
	return []string{
		"stp x29, x30, [sp, #-16]!",
		"bl " + allocPresSym,
		"ldp x29, x30, [sp], #16",
	}
}

// emitAllocPresCall writes allocPresLines through a line writer.
func emitAllocPresCall(w func(string, ...any)) {
	for _, l := range allocPresLines() {
		w("\t%s", l)
	}
}

// emitFreelistClass writes the size-class computation __alloc and __free share,
// mirroring the flat backend's two-tier scheme so the two agree on every block:
// sizeReg holds the request and comes out as the bytes the class actually
// spans (the 16-rounded size, at least 16; in the large tier the 3-significant-
// bit capacity); idxReg comes out as the class index into freelistSym. Requests
// above 1 GiB branch to noneLabel with sizeReg rounded (they are bump-only, so
// the index can never run off the 256-slot table). t2..t7 are scratch; the
// caller must not need them preserved. Labels are namespaced by tag.
func emitFreelistClass(w func(string, ...any), tag, sizeReg, idxReg string, t2, t3, t4, t5, t6, t7, noneLabel string) {
	lbl := func(suffix string) string { return ".Lssa_" + tag + "_" + suffix }
	w("\tadd %s, %s, #15", sizeReg, sizeReg)
	w("\tand %s, %s, #-16", sizeReg, sizeReg)
	w("\tmov %s, #16", t2)
	w("\tcmp %s, %s", sizeReg, t2)
	w("\tcsel %s, %s, %s, lo", sizeReg, t2, sizeReg) // a zero-byte request still owns a block
	w("\tcmp %s, #2048", sizeReg)
	w("\tb.hi %s", lbl("large"))
	w("\tlsr %s, %s, #4", idxReg, sizeReg)
	w("\tsub %s, %s, #1", idxReg, idxReg) // small class index 0..127
	w("\tb %s", lbl("classed"))
	w("%s:", lbl("large"))
	w("\tmov %s, #1", t5)
	w("\tlsl %s, %s, #30", t5, t5) // 1 GiB
	w("\tcmp %s, %s", sizeReg, t5)
	w("\tb.hi %s", noneLabel)
	// Round up to 3 significant bits: gran = 1 << (bsr(size) - 2).
	w("\tclz %s, %s", t2, sizeReg)
	w("\tmov %s, #63", t3)
	w("\tsub %s, %s, %s", t3, t3, t2) // e = bsr(size)
	w("\tsub %s, %s, #2", t4, t3)
	w("\tmov %s, #1", t6)
	w("\tlsl %s, %s, %s", t6, t6, t4) // gran
	w("\tadd %s, %s, %s", idxReg, sizeReg, t6)
	w("\tsub %s, %s, #1", idxReg, idxReg)
	w("\tneg %s, %s", t7, t6)
	w("\tand %s, %s, %s", sizeReg, idxReg, t7) // cap = roundup(size, gran)
	// class = (e2-11)*4 + mant + 124, e2 = bsr(cap), mant = cap >> (e2-2).
	w("\tclz %s, %s", t2, sizeReg)
	w("\tmov %s, #63", t3)
	w("\tsub %s, %s, %s", t3, t3, t2)
	w("\tsub %s, %s, #2", t4, t3)
	w("\tlsr %s, %s, %s", idxReg, sizeReg, t4)
	w("\tsub %s, %s, #11", t3, t3)
	w("\tlsl %s, %s, #2", t3, t3)
	w("\tadd %s, %s, %s", idxReg, idxReg, t3)
	w("\tadd %s, %s, #124", idxReg, idxReg)
	w("%s:", lbl("classed"))
}

// heapGuardCallLines are the instructions a bump site emits immediately after
// publishing its new cursor: a call to __ssa_heap_guard, which aborts when the
// bump ran past the arena. x30 is stacked around the call because most sites
// sit in leaf helpers that keep their return address in it.
func heapGuardCallLines() []string {
	return []string{
		"stp x29, x30, [sp, #-16]!",
		"bl " + heapGuardSym,
		"ldp x29, x30, [sp], #16",
	}
}

// emitHeapGuardCall writes heapGuardCallLines through a line writer.
func emitHeapGuardCall(w func(string, ...any)) {
	for _, l := range heapGuardCallLines() {
		w("\t%s", l)
	}
}

// usesReadLine reports whether the module references Reader.read_line, so the
// .bss line buffer is emitted only when needed.
func usesReadLine(helpers []string) bool {
	for _, h := range helpers {
		if h == "__method_Reader_read_line" {
			return true
		}
	}
	return false
}

// usesStrbuf reports whether the module references any strbuf builtin, so the
// .bss counter + buffer are emitted only when needed.
// usesMapSeed reports whether the module references core/map's per-process
// string-hash seed, so its cached .bss word is emitted.
func usesMapSeed(helpers []string) bool {
	for _, h := range helpers {
		if h == "__fern_map_hash_seed" {
			return true
		}
	}
	return false
}

func usesStrbuf(helpers []string) bool {
	for _, h := range helpers {
		if h == "strbuf_reset" || h == "strbuf_append" || h == "strbuf_take" {
			return true
		}
	}
	return false
}

// usesEnv reports whether the module references the env() builtin, so _start
// captures envp and the .bss slot is emitted.
// usesEnv reports whether the envp snapshot _start takes is needed: env() walks
// it, and __fern_proc_exec hands it to execve so the child inherits the
// environment.
func usesEnv(helpers []string) bool {
	for _, h := range helpers {
		if h == "env" || h == "proc_exec" {
			return true
		}
	}
	return false
}

// usesArgs reports whether the module references the args() builtin, so _start
// captures argc/argv and the .bss slots + cache are emitted.
// usesRcUnderflow reports whether the over-release counter is in play: either
// the detector that bumps it or the probe that reads it was referenced.
func usesRcUnderflow(helpers []string) bool {
	for _, h := range helpers {
		if h == "__fern_rc_dec" || h == "__fern_rc_underflow_count" {
			return true
		}
	}
	return false
}

func usesArgs(helpers []string) bool {
	for _, h := range helpers {
		if h == "args" {
			return true
		}
	}
	return false
}

// usesHeap reports whether any program contains a heap op (so the arena's
// reservation, cursor and guard are emitted only when needed).
func usesHeap(progs map[string]*x86.Program) bool {
	for _, p := range progs {
		for _, blk := range p.Blocks {
			for _, in := range blk.Insts {
				switch in.Op {
				case x86.MemAlloc, x86.MemLoad, x86.MemStore, x86.MakeEnv, x86.BoxDyn:
					return true
				case x86.MakeClosure:
					// A capture-free cell is static .rodata (staticClosureTargets),
					// so it needs no arena.
					if len(in.ArgLocs) > 0 {
						return true
					}
				}
			}
		}
	}
	return false
}

// referencedVtables returns the sorted set of "<trait-set>/<concrete>" keys
// named by an OpConstVtable (x86.ConstVtable) anywhere in the module, so only
// the vtables a `dyn` value can actually reach get `.rodata` cells.
func referencedVtables(progs map[string]*x86.Program) []string {
	seen := map[string]bool{}
	for _, p := range progs {
		for _, blk := range p.Blocks {
			for _, in := range blk.Insts {
				if in.Op == x86.ConstVtable {
					seen[in.Str] = true
				}
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// dynVtableLabel is the GAS symbol for the (trait-set, concrete) `dyn Trait`
// vtable cell. A merged multi-trait set key joins traits with '+' (not a valid
// label char), sanitized to "_x_" — matching the native backends so the two
// emit identically-named cells.
func dynVtableLabel(trait, concrete string) string {
	return "__vtable_" + strings.ReplaceAll(trait, "+", "_x_") + "_" + concrete
}

// splitDynPair undoes the "<trait-set>/<concrete>" key ConstVtable carries.
func splitDynPair(key string) (string, string) {
	if i := strings.IndexByte(key, '/'); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
}

// runtimeHelperEmitters maps a __fern_* runtime-helper name to the code that
// writes its AArch64 body into .text — the arm64 siblings of the x86-64 helper
// emitters, mirroring the native backends' hand-written runtime asm
// (docs/SSA-RC-RUNTIME.md). A helper is emitted only when the module calls it
// (referencedRuntimeHelpers); its `bl fn_<name>` site links the label
// fnLabel(name) writes. Leaf functions under the AArch64 PCS (arg/result x0).
var runtimeHelperEmitters = map[string]func(w func(string, ...any)){
	"__fern_rc_is_unique":       emitRcIsUniqueHelper,
	"__fern_rc_inc":             emitRcIncHelper,
	"__fern_rc_dec":             emitRcDecHelper,
	"__fern_rc_underflow_count": emitRcUnderflowCountHelper,
	"proc_fork":                 emitProcForkHelper,
	"proc_waitpid":              emitProcWaitpidHelper,
	"proc_exec":                 emitProcExecHelper,
	"__fern_box_free":           emitBoxFreeHelper,
	"__fern_closure_drop":       emitClosureDropHelper,
	"__memset":                  emitMemsetHelper,
	"__alloc":                   emitAllocHelper,
	"__free":                    emitFreeHelper,
	"__str_eq":                  emitStrEqHelper,
	"__str_ord":                 emitStrOrdHelper,
	"__str_concat":              emitStrConcatHelper,
	"__fern_str_dec":            emitStrDecHelper,
	"__fern_arr_dec":            emitArrDecHelper,
	"__fern_drop_arr_str":       emitDropArrElemHelper("__fern_drop_arr_str", "__fern_str_dec", "das"),
	"__fern_drop_arr_ptr":       emitDropArrElemHelper("__fern_drop_arr_ptr", "__fern_rc_dec", "dap"),
	"__memcpy":                  emitMemcpyHelper,
	"__fern_map_drop":           emitMapDropHelper,
	"__fern_map_hash_seed":      emitMapHashSeedHelper,
	"__alloc_reuse":             emitAllocReuseHelper,
	"__str_idx":                 emitStrIdxHelper,
	"__fern_memchr":             emitMemchrHelper,
	"__fern_rmemchr":            emitRmemchrHelper,
	"__fern_count_byte":         emitCountByteHelper,
	"__fern_ascii_run":          emitAsciiRunHelper,
	"__arr_idx":                 emitArrIdxHelperN("__arr_idx", 2),    // stride 4 (i32)
	"__arr_idx_1":               emitArrIdxHelperN("__arr_idx_1", 0),  // stride 1 (byte array)
	"__arr_idx_8":               emitArrIdxHelperN("__arr_idx_8", 3),  // stride 8 (i64 / pointer)
	"__arr_idx_16":              emitArrIdxHelperN("__arr_idx_16", 4), // stride 16 (two-word string[])
	// Bounds-check-elided variants (#4380 lever 3): same address compute, no trap.
	"__arr_idx_nc":                  emitArrIdxHelperNChecked("__arr_idx_nc", 2, false),
	"__arr_idx_1_nc":                emitArrIdxHelperNChecked("__arr_idx_1_nc", 0, false),
	"__arr_idx_8_nc":                emitArrIdxHelperNChecked("__arr_idx_8_nc", 3, false),
	"__arr_idx_16_nc":               emitArrIdxHelperNChecked("__arr_idx_16_nc", 4, false),
	"__fern_arr_push_grow":          emitArrPushGrowHelper,
	"__fern_arr_push_grow_ptr":      emitArrPushGrowElemHelper("__fern_arr_push_grow_ptr", "pgp", false),
	"__fern_arr_push_grow_str":      emitArrPushGrowElemHelper("__fern_arr_push_grow_str", "pgs", false),
	"__fern_arr_push_grow_move_ptr": emitArrPushGrowElemHelper("__fern_arr_push_grow_move_ptr", "pgmp", true),
	"__fern_arr_push_grow_move_str": emitArrPushGrowElemHelper("__fern_arr_push_grow_move_str", "pgms", true),
	"__alloc_u8":                    emitAllocU8Helper,
	"__fern_arr_cow_inplace":        emitArrCowInplaceHelper,
	"__fern_arr_cow_inplace_ptr":    emitArrCowInplaceElemHelper("__fern_arr_cow_inplace_ptr", "__fern_rc_inc", "cowp"),
	"__fern_arr_cow_inplace_str":    emitArrCowInplaceElemHelper("__fern_arr_cow_inplace_str", "__fern_rc_inc", "cows"),
	"__fern_heap_bump_bytes":        emitHeapBumpBytesHelper,
	"__method_string_as_bytes":      emitStringAsBytesHelper,
	"__slice_idx":                   emitSliceIdxHelper("__slice_idx", 2),
	"__slice_idx_1":                 emitSliceIdxHelper("__slice_idx_1", 0),
	"__slice_idx_8":                 emitSliceIdxHelper("__slice_idx_8", 3),
	"__slice_make":                  emitSliceMakeHelper,
	"__slice_range":                 emitSliceRangeHelper,
	"stat":                          emitStatHelper,
	"lstat":                         emitLstatHelper,
	"monotonic_ns":                  emitClockHelper("monotonic_ns", clockMonotonic, 1_000_000_000, 1),
	"now_unix_ms":                   emitClockHelper("now_unix_ms", clockRealtime, 1_000, 1_000_000),
	"sleep_ms":                      emitSleepMsHelper,
	"string_from_bytes_unchecked":   emitStringFromBytesHelper,
	"__str_slice":                   emitStrSliceHelper,
	"args":                          emitArgsHelper,
	"env":                           emitEnvHelper,
	"write_file":                    emitWriteFileHelper,
	"read_file":                     emitReadFileHelper,
	"read_file_bytes":               emitReadFileBytesHelper,
	"remove_file":                   emitRemoveFileHelper,
	"create_dir_all":                emitCreateDirAllHelper,
	"remove_dir_all":                emitRemoveDirAllHelper,
	"temp_dir":                      emitTempDirHelper,
	"read_dir":                      emitReadDirHelper,
	"__fern_io_error":               emitIoErrorHelper,
	"tcp_listen":                    emitTcpListenHelper,
	"tcp_accept":                    emitTcpAcceptHelper,
	"tcp_recv":                      emitTcpRecvHelper,
	"tcp_send":                      emitTcpSendHelper,
	"tcp_close":                     emitTcpCloseHelper,
	"tcp_pollable":                  emitTcpPollableHelper,
	"poll":                          emitPollHelper,
	"isatty":                        emitIsattyHelper,
	"wasm_timer_pollable":           emitWasmTimerPollableHelper,
	"wasm_poll":                     emitWasmPollHelper,
	"wasm_pollable_drop":            emitWasmPollableDropHelper,
	"wasm_block":                    emitWasmBlockHelper,
	"open_writer":                   emitOpenWriterHelper,
	"__method_Writer_write":         emitWriterWriteHelper,
	"__method_Writer_close":         emitWriterCloseHelper,
	"open_reader":                   emitOpenReaderHelper,
	"__method_Reader_read_chunk":    emitReaderReadChunkHelper,
	"__method_Reader_read_line":     emitReaderReadLineHelper,
	"__method_Reader_close":         emitReaderCloseHelper,
	"open_appender":                 emitOpenAppenderHelper,
	"stdin":                         emitStdHandleHelper("stdin", 0),
	"stdout":                        emitStdHandleHelper("stdout", 1),
	"stderr":                        emitStdHandleHelper("stderr", 2),
	"print":                         emitPrintHelper,
	"write":                         emitWriteHelper,
	"eprint":                        emitEprintHelper,
	"putchar":                       emitPutcharHelper,
	"exit":                          emitExitHelper,
	"strbuf_reset":                  emitStrbufResetHelper,
	"strbuf_append":                 emitStrbufAppendHelper,
	"strbuf_take":                   emitStrbufTakeHelper,
	"__abs_f64":                     emitFloatUnaryHelper("__abs_f64", "fabs"),
	"__sqrt_f64":                    emitFloatUnaryHelper("__sqrt_f64", "fsqrt"),
	"__floor_f64":                   emitFloatUnaryHelper("__floor_f64", "frintm"),
	"__ceil_f64":                    emitFloatUnaryHelper("__ceil_f64", "frintp"),
	"__trunc_f64":                   emitFloatUnaryHelper("__trunc_f64", "frintz"),
	"__round_f64":                   emitFloatUnaryHelper("__round_f64", "frinta"),
	"__exp_f64":                     emitExpF64Helper,
	"__log_f64":                     emitLogF64Helper,
	"__pow_f64":                     emitPowF64Helper,
	"__sin_f64":                     emitSinF64Helper,
	"__cos_f64":                     emitCosF64Helper,
	"__rem_pio2_large":              emitRemPio2LargeHelper,
	"random_i32":                    emitRandomI32Helper,
	"random_bytes":                  emitRandomBytesHelper,
}

// emitFloatUnaryHelper returns the emitter for a single-instruction f64 unary
// math helper (abs/sqrt/floor/ceil/trunc/round). Floats arrive as f64 bits in x0
// (the SSA GP-register convention); the helper shuttles them into d0, applies the
// AArch64 FP op, and returns the result bits in x0. Leaf.
func emitFloatUnaryHelper(name, mnem string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tfmov d0, x0")
		w("\t%s d0, d0", mnem)
		w("\tfmov x0, d0")
		w("\tret")
	}
}

// transcendentalHelpers are the f64 math helpers that share the .rodata
// coefficient table (emitTranscendentalRodata), emitted once whenever any of
// them is referenced.
var transcendentalHelpers = map[string]bool{
	"__exp_f64": true,
	"__log_f64": true,
	"__pow_f64": true,
	"__sin_f64": true,
	"__cos_f64": true,
}

// usesTranscendentals reports whether any referenced helper needs the shared
// transcendental .rodata coefficient table.
func usesTranscendentals(helpers []string) bool {
	for _, h := range helpers {
		if transcendentalHelpers[h] {
			return true
		}
	}
	return false
}

// emitTranscendentalRodata writes the coefficient table for the f64
// transcendental helpers, from internal/codegen/fdlibm — the same numbers
// internal/codegen/arm64 emits, since the two are the same kernels.
func emitTranscendentalRodata(w func(string, ...any)) {
	w("")
	w(".section .rodata")
	w(".align 3")
	for _, c := range fdlibm.Coeffs {
		w(".Lfc_%s: .double %s", c.Name, c.Text)
	}
	w(".Lfc_2opi_bits:")
	for _, v := range fdlibm.TwoOverPiBits {
		w("\t.quad 0x%016x", v)
	}
	w(".text")
}

// emitExpF64Helper writes __exp_f64(x) -> e^x. x arrives as f64 bits in x0 (the
// SSA GP convention) and the result leaves the same way. Domain-guarded: NaN
// passes through, +Inf trips the overflow arm and -Inf the underflow one, so
// only NaN needs a separate test. Leaf; reads the shared .rodata table.
func emitExpF64Helper(w func(string, ...any)) {
	ldc := func(reg, lbl string) {
		w("\tadrp x12, %s", lbl)
		w("\tadd x12, x12, #:lo12:%s", lbl)
		w("\tldr %s, [x12]", reg)
	}
	w("")
	w("%s:", fnLabel("__exp_f64"))
	w("\tfmov d0, x0")
	w("\tfcmp d0, d0")
	w("\tb.vs .Lssa_exp_ret")
	ldc("d1", ".Lfc_expovf")
	w("\tfcmp d0, d1")
	w("\tb.gt .Lssa_exp_inf")
	ldc("d1", ".Lfc_expunf")
	w("\tfcmp d0, d1")
	w("\tb.lt .Lssa_exp_zero")
	ldc("d1", ".Lfc_invln2")
	w("\tfmul d1, d1, d0")
	w("\tfrintn d1, d1")
	w("\tfcvtzs x10, d1")
	ldc("d2", ".Lfc_ln2hi")
	w("\tfmul d2, d1, d2")
	w("\tfsub d3, d0, d2")
	ldc("d2", ".Lfc_ln2lo")
	w("\tfmul d1, d1, d2")
	w("\tfsub d0, d3, d1")
	w("\tfmul d4, d0, d0")
	ldc("d5", ".Lfc_p5")
	ldc("d20", ".Lfc_p4")
	w("\tfmul d5, d5, d4")
	w("\tfadd d5, d5, d20")
	ldc("d20", ".Lfc_p3")
	w("\tfmul d5, d5, d4")
	w("\tfadd d5, d5, d20")
	ldc("d20", ".Lfc_p2")
	w("\tfmul d5, d5, d4")
	w("\tfadd d5, d5, d20")
	ldc("d20", ".Lfc_p1")
	w("\tfmul d5, d5, d4")
	w("\tfadd d5, d5, d20")
	w("\tfmul d5, d5, d4")
	w("\tfsub d6, d0, d5")
	w("\tfmul d7, d0, d6")
	ldc("d2", ".Lfc_two")
	w("\tfsub d2, d2, d6")
	w("\tfdiv d7, d7, d2")
	w("\tfsub d2, d1, d7")
	w("\tfsub d2, d2, d3")
	ldc("d0", ".Lfc_one")
	w("\tfsub d0, d0, d2")
	// 2^k as two half-scales; see the x86-64 emitter for why one field
	// cannot hold the subnormal band.
	w("\tasr x11, x10, #1")
	w("\tsub x10, x10, x11")
	w("\tadd x11, x11, #1023")
	w("\tlsl x11, x11, #52")
	w("\tfmov d1, x11")
	w("\tadd x10, x10, #1023")
	w("\tlsl x10, x10, #52")
	w("\tfmov d2, x10")
	w("\tfmul d0, d0, d1")
	w("\tfmul d0, d0, d2")
	w(".Lssa_exp_ret:")
	w("\tfmov x0, d0")
	w("\tret")
	w(".Lssa_exp_inf:")
	w("\tmovz x14, #32752, lsl #48")
	w("\tfmov d0, x14")
	w("\tfmov x0, d0")
	w("\tret")
	w(".Lssa_exp_zero:")
	w("\tfmov d0, xzr")
	w("\tfmov x0, d0")
	w("\tret")
}

// emitLogF64Helper writes __log_f64(x) -> ln x. x = m*2^e with m normalised to
// [sqrt2/2, sqrt2); f = m-1; s = f/(2+f). R is two INDEPENDENT chains in w = z^2
// so they issue in parallel rather than as one 7-deep Horner. Domain-guarded:
// without it log(0) returned -709.09 and log(-1) returned 0, because the bit
// twiddling below happily extracts an exponent from 0 or +Inf.
func emitLogF64Helper(w func(string, ...any)) {
	ldc := func(reg, lbl string) {
		w("\tadrp x12, %s", lbl)
		w("\tadd x12, x12, #:lo12:%s", lbl)
		w("\tldr %s, [x12]", reg)
	}
	w("")
	w("%s:", fnLabel("__log_f64"))
	w("\tfmov d0, x0")
	w("\tfcmp d0, d0")
	w("\tb.vs .Lssa_log_ret")
	w("\tfmov d1, xzr")
	w("\tfcmp d0, d1")
	w("\tb.lt .Lssa_log_nan")
	w("\tb.eq .Lssa_log_ninf")
	w("\tmovz x14, #32752, lsl #48")
	w("\tfmov d1, x14")
	w("\tfcmp d0, d1")
	w("\tb.eq .Lssa_log_ret")
	w("\tfmov x10, d0")
	w("\tlsr x11, x10, #52")
	w("\tand x11, x11, #0x7ff")
	w("\tsub x11, x11, #1023")
	w("\tmov x13, #1")
	w("\tlsl x13, x13, #52")
	w("\tsub x13, x13, #1")
	w("\tand x10, x10, x13")
	w("\tmov x14, #1023")
	w("\tlsl x14, x14, #52")
	w("\torr x10, x10, x14")
	w("\tfmov d1, x10")
	ldc("d2", ".Lfc_sqrt2")
	w("\tfcmp d1, d2")
	w("\tb.lt .Lssa_log_noadj")
	ldc("d3", ".Lfc_half")
	w("\tfmul d1, d1, d3")
	w("\tadd x11, x11, #1")
	w(".Lssa_log_noadj:")
	ldc("d4", ".Lfc_one")
	w("\tfsub d1, d1, d4")
	ldc("d2", ".Lfc_two")
	w("\tfadd d2, d2, d1")
	w("\tfdiv d3, d1, d2")
	w("\tfmul d4, d3, d3")
	w("\tfmul d5, d4, d4")
	ldc("d6", ".Lfc_lg6")
	ldc("d20", ".Lfc_lg4")
	w("\tfmul d6, d6, d5")
	w("\tfadd d6, d6, d20")
	ldc("d20", ".Lfc_lg2")
	w("\tfmul d6, d6, d5")
	w("\tfadd d6, d6, d20")
	w("\tfmul d6, d6, d5")
	ldc("d7", ".Lfc_lg7")
	ldc("d20", ".Lfc_lg5")
	w("\tfmul d7, d7, d5")
	w("\tfadd d7, d7, d20")
	ldc("d20", ".Lfc_lg3")
	w("\tfmul d7, d7, d5")
	w("\tfadd d7, d7, d20")
	ldc("d20", ".Lfc_lg1")
	w("\tfmul d7, d7, d5")
	w("\tfadd d7, d7, d20")
	w("\tfmul d7, d7, d4")
	w("\tfadd d6, d6, d7")
	w("\tfmul d2, d1, d1")
	ldc("d16", ".Lfc_half")
	w("\tfmul d2, d2, d16")
	w("\tscvtf d0, x11")
	ldc("d16", ".Lfc_ln2lo")
	w("\tfmul d5, d0, d16")
	w("\tfadd d6, d6, d2")
	w("\tfmul d6, d6, d3")
	w("\tfadd d6, d6, d5")
	w("\tfsub d2, d2, d6")
	w("\tfsub d2, d2, d1")
	ldc("d16", ".Lfc_ln2hi")
	w("\tfmul d0, d0, d16")
	w("\tfsub d0, d0, d2")
	w(".Lssa_log_ret:")
	w("\tfmov x0, d0")
	w("\tret")
	w(".Lssa_log_nan:")
	w("\tmovz x14, #32760, lsl #48")
	w("\tfmov d0, x14")
	w("\tfmov x0, d0")
	w("\tret")
	w(".Lssa_log_ninf:")
	w("\tmovz x14, #65520, lsl #48")
	w("\tfmov d0, x14")
	w("\tfmov x0, d0")
	w("\tret")
}

// emitPowF64Helper writes __pow_f64(x, y) -> x^y. x arrives as f64 bits in x0
// and y in x1. The integer-exponent fast path is not an optimisation:
// exp(y*ln x) CANNOT return exactly 9 for pow(3,2), because a 1-ulp error in
// ln 3 amplified by the exponential lands just under and `as i32` truncated it
// to 8. Repeated squaring is exact wherever the result is representable, and it
// is a LEAF, so it returns before the frame the general path sets up.
// Integrality is an i64 round-trip, so a NaN or out-of-range y falls out as a
// huge |n| the range check rejects, sending it to the general path below.
func emitPowF64Helper(w func(string, ...any)) {
	ldc := func(reg, lbl string) {
		w("\tadrp x12, %s", lbl)
		w("\tadd x12, x12, #:lo12:%s", lbl)
		w("\tldr %s, [x12]", reg)
	}
	w("")
	w("%s:", fnLabel("__pow_f64"))
	w("\tfmov d0, x0")
	w("\tfmov d1, x1")
	w("\tfcvtzs x10, d1")
	w("\tscvtf d2, x10")
	w("\tfcmp d2, d1")
	w("\tb.ne .Lssa_pow_gen")
	// Re-entered once with a reciprocated base and a negated n; see the
	// x86-64 emitter for the overflow retry.
	w(".Lssa_pow_retry:")
	w("\tmov x11, x10")
	w("\tcmp x11, #0")
	w("\tb.ge .Lssa_pow_abs")
	w("\tneg x11, x11")
	w(".Lssa_pow_abs:")
	w("\tcmp x11, #%d", fdlibm.PowIntMax)
	w("\tb.gt .Lssa_pow_gen")
	ldc("d3", ".Lfc_one")
	w("\tfmov d4, d0")
	w(".Lssa_pow_loop:")
	w("\ttbz x11, #0, .Lssa_pow_skip")
	w("\tfmul d3, d3, d4")
	w(".Lssa_pow_skip:")
	w("\tfmul d4, d4, d4")
	w("\tlsr x11, x11, #1")
	w("\tcmp x11, #0")
	w("\tb.ne .Lssa_pow_loop")
	w("\tcmp x10, #0")
	w("\tb.ge .Lssa_pow_done")
	ldc("d5", ".Lfc_one")
	w("\tfdiv d5, d5, d3")
	// 1/acc is zero only when acc reached an infinity.
	w("\tfcmp d5, #0.0")
	w("\tb.ne .Lssa_pow_recip")
	ldc("d5", ".Lfc_one")
	w("\tfdiv d0, d5, d0")
	w("\tneg x10, x10")
	w("\tb .Lssa_pow_retry")
	w(".Lssa_pow_recip:")
	w("\tfmov d3, d5")
	w(".Lssa_pow_done:")
	w("\tfmov x0, d3")
	w("\tret")
	// General case: x^y = sign * exp(y*ln|x|). ln is defined only for x > 0,
	// so a negative base is split into a sign and |x| first: for an integral y
	// the sign is (-1)^y, and a non-integral y makes the result NaN. Every
	// |y| >= 2^53 is an even integer, which is what leaves pow(x<0, +-Inf) a
	// magnitude rather than a NaN. The f64 bit patterns are movz-able because
	// their low 48 bits are zero: 16368 = +1.0, 49136 = -1.0, 17216 = 2^53,
	// 32752 = +Inf, 32760 = quiet NaN.
	w(".Lssa_pow_gen:")
	w("\tstp x29, x30, [sp, #-32]!")
	w("\tmov x29, sp")
	w("\tstr x19, [sp, #16]")
	w("\tmov x19, x1")
	w("\tmovz x15, #16368, lsl #48")
	w("\ttbz x0, #63, .Lssa_pow_mag")
	w("\tlsl x0, x0, #1")
	w("\tlsr x0, x0, #1")
	w("\tlsl x13, x1, #1")
	w("\tlsr x13, x13, #1")
	w("\tmovz x16, #17216, lsl #48")
	w("\tcmp x13, x16")
	w("\tb.lo .Lssa_pow_parity")
	w("\tmovz x16, #32752, lsl #48")
	w("\tcmp x13, x16")
	w("\tb.ls .Lssa_pow_mag")
	w("\tb .Lssa_pow_nan")
	w(".Lssa_pow_parity:")
	w("\tfmov d1, x1")
	w("\tfcvtzs x13, d1")
	w("\tscvtf d2, x13")
	w("\tfcmp d2, d1")
	w("\tb.ne .Lssa_pow_nan")
	w("\ttbz x13, #0, .Lssa_pow_mag")
	w("\tmovz x15, #49136, lsl #48")
	// |x| == 1 is 1 for every y, including the NaN and +-Inf that
	// exp(y*ln|x|) would turn into NaN through y*0.
	w(".Lssa_pow_mag:")
	w("\tmovz x16, #16368, lsl #48")
	w("\tcmp x0, x16")
	w("\tb.eq .Lssa_pow_sign")
	w("\tstr x15, [sp, #24]")
	w("\tbl %s", fnLabel("__log_f64"))
	w("\tfmov d0, x0")
	w("\tfmov d1, x19")
	w("\tfmul d0, d1, d0")
	w("\tfmov x0, d0")
	w("\tbl %s", fnLabel("__exp_f64"))
	w("\tldr x15, [sp, #24]")
	w(".Lssa_pow_sign:")
	w("\tfmov d0, x0")
	w("\tfmov d1, x15")
	w("\tfmul d0, d0, d1")
	w("\tfmov x0, d0")
	w("\tldr x19, [sp, #16]")
	w("\tldp x29, x30, [sp], #32")
	w("\tret")
	w(".Lssa_pow_nan:")
	w("\tmovz x0, #32760, lsl #48")
	w("\tldr x19, [sp, #16]")
	w("\tldp x29, x30, [sp], #32")
	w("\tret")
}

// emitSinCosReduction emits the shared reduction and the sin(r)/cos(r) kernels
// for __sin_f64 / __cos_f64. Argument in d0; on exit x10 = quadrant (k&3),
// d6 = sin(r), d16 = cos(r), r in [-pi/4, pi/4]. Both kernels are evaluated and
// the caller selects, which keeps this backend's on-demand helper emission
// simple — there is no shared kernel subroutine to pull in.
//
// prefix keeps the local labels unique across the two inlining sites.
//
// frintn, not frinta: ties-to-EVEN, matching x86's `roundsd …, 0` and wasm's
// `f64.nearest`, so every backend picks the same k.
func emitSinCosReduction(w func(string, ...any), prefix string) {
	ldc := func(reg, lbl string) {
		w("\tadrp x12, %s", lbl)
		w("\tadd x12, x12, #:lo12:%s", lbl)
		w("\tldr %s, [x12]", reg)
	}
	// |x| >= 2^20 (biased exponent >= 1043) puts k past pio2h's 22 exact
	// mantissa bits, so the Cody-Waite chain below reduces against noise there
	// and needs Payne-Hanek. e == 0x7ff (Inf/NaN) stays on the small path,
	// whose Inf - Inf / NaN propagation reduces it to NaN — the same result
	// the native backends' trigGuard returns.
	w("\tfmov x13, d0")
	w("\tubfx x13, x13, #52, #11")
	w("\tcmp x13, #1043")
	w("\tb.lt .Lssa_%s_cw", prefix)
	w("\tcmp x13, #2047")
	w("\tb.eq .Lssa_%s_cw", prefix)
	// The frame lives inside the branch so the common small path stays leaf.
	w("\tstp x29, x30, [sp, #-16]!")
	w("\tmov x29, sp")
	w("\tbl %s", fnLabel("__rem_pio2_large"))
	w("\tldp x29, x30, [sp], #16")
	w("\tb .Lssa_%s_red", prefix)
	w(".Lssa_%s_cw:", prefix)
	ldc("d1", ".Lfc_2opi")
	w("\tfmul d1, d1, d0")
	w("\tfrintn d1, d1")
	w("\tfcvtzs x10, d1")
	ldc("d2", ".Lfc_pio2h")
	w("\tfmul d2, d1, d2")
	w("\tfsub d0, d0, d2")
	ldc("d2", ".Lfc_pio2m")
	w("\tfmul d2, d1, d2")
	w("\tfsub d0, d0, d2")
	ldc("d2", ".Lfc_pio2l")
	w("\tfmul d1, d1, d2")
	w("\tfsub d0, d0, d1")
	w("\tand x10, x10, #3")
	w(".Lssa_%s_red:", prefix)
	w("\tfmul d5, d0, d0")
	w("\tfmul d17, d5, d0")
	ldc("d6", ".Lfc_s6")
	ldc("d20", ".Lfc_s5")
	w("\tfmul d6, d6, d5")
	w("\tfadd d6, d6, d20")
	ldc("d20", ".Lfc_s4")
	w("\tfmul d6, d6, d5")
	w("\tfadd d6, d6, d20")
	ldc("d20", ".Lfc_s3")
	w("\tfmul d6, d6, d5")
	w("\tfadd d6, d6, d20")
	ldc("d20", ".Lfc_s2")
	w("\tfmul d6, d6, d5")
	w("\tfadd d6, d6, d20")
	ldc("d20", ".Lfc_s1")
	w("\tfmul d6, d6, d5")
	w("\tfadd d6, d6, d20")
	w("\tfmul d6, d6, d17")
	w("\tfadd d6, d0, d6")
	ldc("d16", ".Lfc_c6")
	ldc("d20", ".Lfc_c5")
	w("\tfmul d16, d16, d5")
	w("\tfadd d16, d16, d20")
	ldc("d20", ".Lfc_c4")
	w("\tfmul d16, d16, d5")
	w("\tfadd d16, d16, d20")
	ldc("d20", ".Lfc_c3")
	w("\tfmul d16, d16, d5")
	w("\tfadd d16, d16, d20")
	ldc("d20", ".Lfc_c2")
	w("\tfmul d16, d16, d5")
	w("\tfadd d16, d16, d20")
	ldc("d20", ".Lfc_c1")
	w("\tfmul d16, d16, d5")
	w("\tfadd d16, d16, d20")
	w("\tfmul d16, d16, d5")
	w("\tfmul d16, d16, d5")
	ldc("d18", ".Lfc_half")
	w("\tfmul d18, d5, d18")
	ldc("d19", ".Lfc_one")
	w("\tfsub d21, d19, d18")
	w("\tfsub d22, d19, d21")
	w("\tfsub d22, d22, d18")
	w("\tfadd d22, d22, d16")
	w("\tfadd d16, d21, d22")
}

// emitSinF64Helper writes __sin_f64(x) → sin x via the shared reduction: k =
// round(x/(π/2)), r = x − k·(π/2) ∈ [−π/4, π/4]; the quadrant q = k&3 selects
// ±sin(r)/±cos(r). x0 in/out (f64 bits). Leaf; reads the shared .rodata table.
func emitSinF64Helper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__sin_f64"))
	w("\tfmov d0, x0")
	emitSinCosReduction(w, "sin")
	w("\tcmp x10, #0")
	w("\tb.eq .Lssa_sin_sr")
	w("\tcmp x10, #1")
	w("\tb.eq .Lssa_sin_cr")
	w("\tcmp x10, #2")
	w("\tb.eq .Lssa_sin_nsr")
	w("\tfneg d0, d16") // q3: -cos(r)
	w("\tb .Lssa_sin_ret")
	w(".Lssa_sin_sr:")
	w("\tfmov d0, d6")
	w("\tb .Lssa_sin_ret")
	w(".Lssa_sin_cr:")
	w("\tfmov d0, d16")
	w("\tb .Lssa_sin_ret")
	w(".Lssa_sin_nsr:")
	w("\tfneg d0, d6")
	w(".Lssa_sin_ret:")
	w("\tfmov x0, d0")
	w("\tret")
}

// emitCosF64Helper writes __cos_f64(x) → cos x. Same reduction as __sin_f64; the
// quadrant selects cos(r)/−sin(r)/−cos(r)/sin(r). x0 in/out (f64 bits). Leaf.
func emitCosF64Helper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__cos_f64"))
	w("\tfmov d0, x0")
	emitSinCosReduction(w, "cos")
	w("\tcmp x10, #0")
	w("\tb.eq .Lssa_cos_cr")
	w("\tcmp x10, #1")
	w("\tb.eq .Lssa_cos_nsr")
	w("\tcmp x10, #2")
	w("\tb.eq .Lssa_cos_ncr")
	w("\tfmov d0, d6") // q3: sin(r)
	w("\tb .Lssa_cos_ret")
	w(".Lssa_cos_cr:")
	w("\tfmov d0, d16")
	w("\tb .Lssa_cos_ret")
	w(".Lssa_cos_nsr:")
	w("\tfneg d0, d6")
	w("\tb .Lssa_cos_ret")
	w(".Lssa_cos_ncr:")
	w("\tfneg d0, d16")
	w(".Lssa_cos_ret:")
	w("\tfmov x0, d0")
	w("\tret")
}

// emitRemPio2LargeHelper writes __rem_pio2_large(d0 = x, |x| >= 2^20 and
// finite) → x10 = k&3, d0 = r. Payne-Hanek: multiplies the significand by the
// window of 2/pi its exponent selects, keeping 128 bits about the binary
// point — the top two bits are the quadrant and the rest the fraction of
// x/(pi/2), neither of which loses accuracy with magnitude, where Cody-Waite
// needs k to fit in pio2h's 22 zeroed mantissa bits and returns noise past
// that. Kept in lockstep with the native arm64 backend's
// __fern_rem_pio2_large, register for register.
func emitRemPio2LargeHelper(w func(string, ...any)) {
	ldc := func(reg, lbl string) {
		w("\tadrp x12, %s", lbl)
		w("\tadd x12, x12, #:lo12:%s", lbl)
		w("\tldr %s, [x12]", reg)
	}
	w("")
	w("%s:", fnLabel("__rem_pio2_large"))
	w("\tfmov x1, d0")
	w("\tlsr x2, x1, #63")       // sign of x
	w("\tubfx x3, x1, #52, #11") // biased exponent
	w("\tand x4, x1, #0x000fffffffffffff")
	w("\torr x4, x4, #0x0010000000000000") // m, the 53-bit significand
	// x = m*2^(e-1075), so the fraction of x*(2/pi) starts at bit
	// (e-1075)+62 of the table once the product is read as a Q126.
	w("\tsub x3, x3, #1013")
	w("\tand x5, x3, #63") // bit offset within the limb
	w("\tlsr x3, x3, #6")  // limb index
	w("\tadrp x17, .Lfc_2opi_bits")
	w("\tadd x17, x17, #:lo12:.Lfc_2opi_bits")
	w("\tadd x17, x17, x3, lsl #3")
	w("\tldr x6, [x17]")
	w("\tldr x7, [x17, #8]")
	w("\tldr x1, [x17, #16]")
	w("\tldr x3, [x17, #24]")
	// Each 64-bit window is (T[i] << off) | (T[i+1] >> (64-off)). aarch64
	// has no variable EXTR, and lsrv masks its count mod 64, so the right
	// half is spelled as two shifts — a plain >> (64-off) is wrong at off 0.
	w("\tmov x16, #63")
	w("\tsub x16, x16, x5")
	shiftIn := func(dst, hi, lo string) {
		w("\tlsl %s, %s, x5", dst, hi)
		w("\tlsr x0, %s, #1", lo)
		w("\tlsr x0, x0, x16")
		w("\torr %s, %s, x0", dst, dst)
	}
	shiftIn("x13", "x6", "x7") // w0
	shiftIn("x14", "x7", "x1") // w1
	shiftIn("x15", "x1", "x3") // w2
	// acc(128) = lo(m*w0)<<64 + m*w1 + hi(m*w2), i.e. x*(2/pi) as a Q126.
	w("\tmul x7, x4, x13")
	w("\tmul x1, x4, x14")
	w("\tumulh x3, x4, x14")
	w("\tumulh x6, x4, x15")
	w("\tadds x1, x1, x6")
	w("\tadc x7, x7, x3")
	w("\tlsr x10, x7, #62") // quadrant
	w("\tand x7, x7, #0x3fffffffffffffff")
	// A fraction at or above a half belongs to the next quadrant, as the
	// negative remainder below it — which is what keeps |r| <= pi/4.
	w("\tmov x0, xzr") // frac is negative iff this becomes 1
	w("\tmovz x16, #8192, lsl #48")
	w("\tcmp x7, x16")
	w("\tb.lo .Lssa_ph_pos")
	w("\tadd x10, x10, #1")
	w("\tsubs x1, xzr, x1") // negs: sets the borrow sbc needs
	w("\tmovz x6, #16384, lsl #48")
	w("\tsbc x7, x6, x7")
	w("\tmov x0, #1")
	w(".Lssa_ph_pos:")
	// >> 11 keeps the low word inside 53 bits so the conversion is exact,
	// and matches the other backends bit for bit.
	w("\tlsr x1, x1, #11")
	w("\tscvtf d1, x7")
	ldc("d3", ".Lfc_2m62")
	w("\tfmul d1, d1, d3")
	w("\tscvtf d2, x1")
	ldc("d3", ".Lfc_2m115")
	w("\tfmul d2, d2, d3")
	w("\tfadd d1, d1, d2")
	w("\tcmp x0, #0")
	w("\tb.eq .Lssa_ph_nn")
	w("\tfneg d1, d1")
	w(".Lssa_ph_nn:")
	w("\tcmp x2, #0")
	w("\tb.eq .Lssa_ph_sgn")
	w("\tfneg d1, d1")
	w("\tneg x10, x10")
	w(".Lssa_ph_sgn:")
	w("\tand x10, x10, #3")
	ldc("d3", ".Lfc_pio2hi")
	w("\tfmul d0, d1, d3")
	ldc("d3", ".Lfc_pio2lo")
	w("\tfmul d1, d1, d3")
	w("\tfadd d0, d0, d1")
	w("\tret")
}

// emitRandomI32Helper writes random_i32() → a single kernel-CSPRNG i32 via a
// getrandom(2) read of 4 bytes into a stack slot. Leaf; the svc preserves all
// registers but x0, so no frame save is needed. Returns the 4 random bytes as
// i32 in x0 (the caller's i32 sxtw mask applies the signed interpretation).
func emitRandomI32Helper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("random_i32"))
	w("\tsub sp, sp, #16")
	w("\tmov x0, sp")
	w("\tmov x1, #4")
	w("\tmov x2, #0")
	w("\tmov x8, #278") // getrandom
	w("\tsvc #0")
	w("\tldr w0, [sp]") // 4 random bytes → i32
	w("\tadd sp, sp, #16")
	w("\tret")
}

// emitIsattyHelper writes isatty(fd) -> 0/1: one TCGETS ioctl, which only a
// terminal answers. `struct termios` is 60 bytes; 80 keeps sp 16-aligned.
// Leaf — the svc preserves every register but x0, so no frame save is needed.
func emitIsattyHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("isatty"))
	w("\tsub sp, sp, #80")
	w("\tmov x1, #21505") // TCGETS
	w("\tmov x2, sp")
	w("\tmov x8, #29") // ioctl
	w("\tsvc #0")
	w("\tcmp x0, #0")
	w("\tcset w0, eq")
	w("\tadd sp, sp, #80")
	w("\tret")
}

// emitRandomBytesHelper writes random_bytes(n) → a fresh u8[] of n
// kernel-CSPRNG bytes (getrandom(2), flags=0), in the __alloc_u8 box shape
// (16-byte header; cap@-12, rc=1@-8, len@-4). Returns the data pointer in x0.
// Leaf: it bump-allocates inline and the getrandom svc preserves all
// registers but x0, so n (x9) and the data pointer (x10) survive the syscall
// without callee-saved spills. No zero-fill: getrandom overwrites all n data
// bytes. x0=n.
func emitRandomBytesHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("random_bytes"))
	w("\tmov x9, x0") // n
	// Allocate a u8[] box: 16-byte header + n data bytes.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x9, #16")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tadd x10, x4, #16")     // data ptr (past 16-byte header)
	w("\tstur w9, [x10, #-12]") // cap = n
	w("\tmov w7, #1")
	w("\tstur w7, [x10, #-8]") // rc = 1
	w("\tstur w9, [x10, #-4]") // len = n
	// getrandom(data, n, 0).
	w("\tmov x0, x10")
	w("\tmov x1, x9")
	w("\tmov x2, #0")
	w("\tmov x8, #278") // getrandom
	w("\tsvc #0")
	w("\tmov x0, x10") // return data ptr
	w("\tret")
}

// emitTcpListenHelper writes tcp_listen(port) → i32: create an AF_INET TCP
// listener bound to 0.0.0.0:port and return its fd, or -errno on any syscall
// failure. socket(2)/bind(2)/listen(2); the 16-byte sockaddr_in is built on the
// stack (htons the port via rev16). x19=port / x20=fd across the syscalls.
func emitTcpListenHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("tcp_listen"))
	w("\tstp x29, x30, [sp, #-32]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tmov x19, x0") // port
	// socket(AF_INET=2, SOCK_STREAM=1, 0)
	w("\tmov x0, #2")
	w("\tmov x1, #1")
	w("\tmov x2, #0")
	w("\tmov x8, #198") // socket
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_tcpl_err")
	w("\tmov x20, x0") // listener fd
	// Build sockaddr_in { family=AF_INET, port=htons(port), addr=0 } on the stack.
	w("\tsub sp, sp, #16")
	w("\tmov w0, #2")
	w("\tstrh w0, [sp]") // sin_family
	w("\trev16 w0, w19") // htons(port)
	w("\tstrh w0, [sp, #2]")
	w("\tstr wzr, [sp, #4]") // sin_addr = 0.0.0.0
	w("\tstr xzr, [sp, #8]") // sin_zero
	// bind(fd, sa, 16)
	w("\tmov x0, x20")
	w("\tmov x1, sp")
	w("\tmov x2, #16")
	w("\tmov x8, #200") // bind
	w("\tsvc #0")
	w("\tadd sp, sp, #16") // pop sockaddr_in before any branch
	w("\ttbnz x0, #63, .Lssa_tcpl_err")
	// listen(fd, 128)
	w("\tmov x0, x20")
	w("\tmov x1, #128")
	w("\tmov x8, #201") // listen
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_tcpl_err")
	w("\tmov x0, x20") // return fd
	w("\tb .Lssa_tcpl_ret")
	w(".Lssa_tcpl_err:")
	// x0 holds -errno from the failed syscall.
	w(".Lssa_tcpl_ret:")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #32")
	w("\tret")
}

// emitTcpAcceptHelper writes tcp_accept(fd) → i32: accept(2) a connection on the
// listener fd (NULL addr/addrlen — callers don't need the peer address) and
// return the new connection fd, or -errno. Leaf. x0=listener fd.
func emitTcpAcceptHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("tcp_accept"))
	w("\tmov x1, #0")   // addr = NULL
	w("\tmov x2, #0")   // addrlen = NULL
	w("\tmov x8, #202") // accept
	w("\tsvc #0")
	w("\tret")
}

// emitTcpRecvHelper writes tcp_recv(fd, max) → u8[]: read up to max bytes from
// the socket fd into a fresh u8[] in the __alloc_u8 box shape (16-byte header;
// cap@-12, rc=1@-8, len@-4). len = the actual byte count; 0 on EOF/error.
// max <= 0 skips the read and returns the box empty. Leaf: bump-allocates
// inline (like random_bytes) and read's svc preserves every register but x0,
// so fd/max/data survive without spills. x0=fd, x1=max.
func emitTcpRecvHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("tcp_recv"))
	w("\tmov x9, x0") // fd
	w("\tcmp w1, #0")
	w("\tcsel w10, w1, wzr, gt") // n = max, clamped to >= 0
	// Allocate a u8[] box: 16-byte header + n data bytes.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x10, #16")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tadd x11, x4, #16")      // data ptr (past 16-byte header)
	w("\tstur w10, [x11, #-12]") // cap = n
	w("\tmov w7, #1")
	w("\tstur w7, [x11, #-8]")  // rc = 1
	w("\tstur wzr, [x11, #-4]") // len = 0 until the read lands
	w("\tcbz w10, .Lssa_tcpr_ret")
	// read(fd, data, n)
	w("\tmov x0, x9")
	w("\tmov x1, x11")
	w("\tmov x2, x10")
	w("\tmov x8, #63") // read
	w("\tsvc #0")
	// len = max(result, 0); errors and EOF leave the box empty.
	w("\tcmp x0, #0")
	w("\tcsel x0, x0, xzr, ge")
	w("\tstur w0, [x11, #-4]")
	w(".Lssa_tcpr_ret:")
	w("\tmov x0, x11") // return data ptr
	w("\tret")
}

// emitTcpSendHelper writes tcp_send(fd, data) → i32: write(2) the whole
// single-word string to the fd; returns the byte count written or -errno. Leaf.
// x0=fd, x1=data (single-word string; length at [data-4]).
func emitTcpSendHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("tcp_send"))
	w("\tldur w2, [x1, #-4]") // byte length
	w("\tmov x8, #64")        // write (x0=fd, x1=data already in place)
	w("\tsvc #0")
	w("\tret")
}

// emitTcpCloseHelper writes tcp_close(fd) → i32: close(2) the fd; returns 0 or
// -errno. Leaf. x0=fd.
func emitTcpCloseHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("tcp_close"))
	w("\tmov x8, #57") // close
	w("\tsvc #0")
	w("\tret")
}

// emitTcpPollableHelper writes tcp_pollable(fd) → i32: on native the readiness
// token for a socket IS its fd (poll(2) takes fds directly), so this is the
// identity — the fd is already in x0. Leaf.
func emitTcpPollableHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("tcp_pollable"))
	w("\tret")
}

// emitWasmTimerPollableHelper writes wasm_timer_pollable(ns) → i32: on native
// there's no pollable to make for a deadline (the timeout is poll(2)'s argument),
// so it returns -1 — an fd poll(2) ignores. Lets std/async's with_deadline append
// a portable "timer" slot to its poll set (on wasm this yields a real pollable).
// Leaf.
func emitWasmTimerPollableHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("wasm_timer_pollable"))
	w("\tmov x0, #-1") // no native pollable; -1 is ignored by poll(2)
	w("\tret")
}

// emitWasmPollHelper writes wasm_poll(pollables) → i32: returns -1 on native (no
// real pollables; native readiness rides poll(2)), ignoring its array arg. On
// wasm this is the real wasi:io/poll.poll(list<pollable>) multiplexer. Leaf.
func emitWasmPollHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("wasm_poll"))
	w("\tmov x0, #-1") // no native pollables; nothing ready
	w("\tret")
}

// emitWasmPollableDropHelper writes wasm_pollable_drop(p) → i32: a no-op on
// native (a pollable is just an fd; the socket fd is closed via tcp_close).
// Returns 0. Lets std/async drop the wasm pollable portably. Leaf.
func emitWasmPollableDropHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("wasm_pollable_drop"))
	w("\tmov x0, #0") // no-op
	w("\tret")
}

// emitWasmBlockHelper writes wasm_block(p) → i32: a no-op on native (there's no
// pollable to wait on; a deadline comes from poll(2)'s own timeout arg). Returns
// 0. Lets std/async's with_deadline block on a timer pollable portably; on wasm
// this symbol is the real wasi:io/poll.[method]pollable.block instead. Leaf.
func emitWasmBlockHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("wasm_block"))
	w("\tmov x0, #0") // no-op
	w("\tret")
}

// emitWriterHandleAlloc bump-allocates a Reader/Writer handle and leaves its
// value-pointer in `dst`. The handle mirrors arm64-ssa's generic struct box: an
// rc word at [ptr-8], an (unused) struct-type-id slot at [ptr+0], and the fd at
// [ptr+8] (the first — and only — field, which a `.fd` read loads at ptr+8). The
// rc word is the immortal sentinel 0x80000000, so the handle is runtime-owned:
// __fern_rc_dec / is_unique short-circuit on it (tbnz #31), so it is never freed
// and its type-id slot is never dispatched on. `fdReg` holds the fd (w-reg).
// Clobbers x3-x7. Emitted inline (no call) so callers stay leaf-or-simple.
func emitWriterHandleAlloc(w func(string, ...any), dst, fdReg string) {
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tlsl w6, w6, #31")          // 0x80000000 immortal-rc sentinel
	w("\tstr w6, [x4]")             // rc @ base (= ptr-8)
	w("\tstr xzr, [x4, #8]")        // struct-type-id slot @ ptr+0 (unused)
	w("\tstr %s, [x4, #16]", fdReg) // fd @ ptr+8
	w("\tadd %s, x4, #8", dst)      // value pointer = base + 8
}

// emitOptionBox writes an Option[IoError] heap box and leaves its value pointer
// in x0. Layout {rc@base, tag@base+8, payload@base+16}, value pointer = base+8.
// `tag` is 1 for None (rc + tag only), 0 for Some (rc + tag + payload), where
// `payloadReg` (a 64-bit reg) holds the IoError pointer stored at box+8. Clobbers
// x3-x6.
// emitResultUnitBox builds a Result[void, IoError] box: Ok(()) puts the
// unit in the payload slot, Err puts the IoError there. BOTH arms are 24
// bytes (rc + tag + payload) — unlike Option, whose None arm is 16 with
// no payload at all.
//
// Deliberately separate from emitOptionBox: most of that function's
// callers (close, writer_write, the reader helpers) still return
// Option[IoError], and shifting their layout here would corrupt them.
func emitResultUnitBox(w func(string, ...any), ok bool, payloadReg string) {
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	if ok {
		w("\tstr wzr, [x0]")     // tag = 0 (Ok)
		w("\tstr xzr, [x0, #8]") // unit payload
	} else {
		w("\tmov w6, #1")
		w("\tstr w6, [x0]")                 // tag = 1 (Err)
		w("\tstr %s, [x0, #8]", payloadReg) // IoError payload
	}
}

func emitOptionBox(w func(string, ...any), tag int, payloadReg string) {
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	if tag == 1 {
		w("\tadd x5, x4, #16") // None: rc + tag only
	} else {
		w("\tadd x5, x4, #24") // Some: rc + tag + payload
	}
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	if tag == 1 {
		w("\tmov w6, #1")
		w("\tstr w6, [x0]") // tag = 1 (None)
	} else {
		w("\tstr wzr, [x0]")                // tag = 0 (Some)
		w("\tstr %s, [x0, #8]", payloadReg) // IoError payload
	}
}

// emitEmptyString bump-allocates a zero-length single-word rc string into `dst`
// (rc=1, len=0, one NUL byte). Used to supply a valid (empty) path to
// __fern_io_error for write/close errors, which carry no path. Clobbers x3-x7.
func emitEmptyString(w func(string, ...any), dst string) {
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x6, x4, #9")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w7, #1")
	w("\tstr w7, [x4]")      // rc = 1
	w("\tstr wzr, [x4, #4]") // len = 0
	w("\tadd %s, x4, #8", dst)
	w("\tstrb wzr, [%s]", dst) // NUL
}

// emitOpenHandleHelper writes an open_reader / open_writer / open_appender
// builtin -> Result[Reader|Writer, IoError]: NUL-terminate the path, openat it
// with the given flags/mode, wrap the fd in a handle (fd at [ptr+8], immortal
// rc), and return Ok(handle), or map -errno through __fern_io_error and return
// Err(IoError). `name` is the builtin symbol and `lbl` a unique label infix.
// Non-leaf (calls __fern_io_error). x19=path / x20=pathz. x0=path.
func emitOpenHandleHelper(w func(string, ...any), name, lbl string, flags, mode int) {
	w("")
	w("%s:", fnLabel(name))
	w("\tstp x29, x30, [sp, #-32]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tmov x19, x0") // path
	// NUL-terminate the path into a fresh heap buffer (x20).
	w("\tldur w2, [x19, #-4]")
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x2, #1")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w7, #0")
	w(".Lssa_%s_cp:", lbl)
	w("\tcmp w7, w2")
	w("\tb.hs .Lssa_%s_cpd", lbl)
	w("\tldrb w8, [x19, x7]")
	w("\tstrb w8, [x4, x7]")
	w("\tadd w7, w7, #1")
	w("\tb .Lssa_%s_cp", lbl)
	w(".Lssa_%s_cpd:", lbl)
	w("\tstrb wzr, [x4, x2]")
	w("\tmov x20, x4") // pathz
	// openat(AT_FDCWD, pathz, flags, mode).
	w("\tmov x0, #100")
	w("\tneg x0, x0")
	w("\tmov x1, x20")
	w("\tmov x2, #%d", flags)
	w("\tmov x3, #%d", mode)
	w("\tmov x8, #56") // openat
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_%s_err", lbl)
	// Success: wrap the fd in a handle (x19), then Ok(handle).
	w("\tmov w9, w0") // fd (survives the inline alloc — no call/svc)
	emitWriterHandleAlloc(w, "x19", "w9")
	// Result.Ok(handle): box {rc=1, tag=0, handle@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	w("\tstr wzr, [x0]")  // tag = 0 (Ok)
	w("\tstr x19, [x0, #8]")
	w("\tb .Lssa_%s_ret", lbl)
	w(".Lssa_%s_err:", lbl)
	w("\tneg x0, x0")  // errno
	w("\tmov x1, x19") // path
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x19, x0") // IoError box
	// Result.Err(IoError): box {rc=1, tag=1, ioerr@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	w("\tmov w6, #1")
	w("\tstr w6, [x0]") // tag = 1 (Err)
	w("\tstr x19, [x0, #8]")
	w(".Lssa_%s_ret:", lbl)
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #32")
	w("\tret")
}

// emitOpenWriterHelper: open_writer(path) — O_WRONLY|O_CREAT|O_TRUNC (577), 0644.
func emitOpenWriterHelper(w func(string, ...any)) {
	emitOpenHandleHelper(w, "open_writer", "ow", 577, 420)
}

// emitOpenAppenderHelper: open_appender(path) — O_WRONLY|O_CREAT|O_APPEND (1089),
// 0644. Opens (creating if needed) for appending rather than truncating.
func emitOpenAppenderHelper(w func(string, ...any)) {
	emitOpenHandleHelper(w, "open_appender", "oa", 1089, 420)
}

// emitWriterWriteHelper writes __method_Writer_write(writer, data) ->
// Option[IoError]: loop write(2) the whole single-word string to the handle's fd
// (loaded from [writer+8]); return None on success, or map -errno through
// __fern_io_error (with an empty path, since a write carries none) and return
// Some(IoError). Non-leaf. x19=fd / x20=data / x21=written / x22=len.
// x0=writer handle, x1=data.
func emitWriterWriteHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__method_Writer_write"))
	w("\tstp x29, x30, [sp, #-48]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tstp x21, x22, [sp, #32]")
	w("\tldr w19, [x0, #8]")    // fd @ ptr+8
	w("\tmov x20, x1")          // data ptr
	w("\tldur w22, [x20, #-4]") // byte length
	w("\tmov x21, #0")          // bytes written
	w(".Lssa_wrw_loop:")
	w("\tcmp x21, x22")
	w("\tb.ge .Lssa_wrw_done")
	w("\tmov w0, w19")
	w("\tadd x1, x20, x21")
	w("\tsub x2, x22, x21")
	w("\tmov x8, #64") // write
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_wrw_err")
	w("\tadd x21, x21, x0")
	w("\tb .Lssa_wrw_loop")
	w(".Lssa_wrw_done:")
	emitOptionBox(w, 1, "")
	w("\tb .Lssa_wrw_ret")
	w(".Lssa_wrw_err:")
	w("\tneg x19, x0") // errno (reuse x19; fd no longer needed)
	emitEmptyString(w, "x1")
	w("\tmov x0, x19") // errno
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x19, x0") // IoError box
	emitOptionBox(w, 0, "x19")
	w(".Lssa_wrw_ret:")
	w("\tldp x21, x22, [sp, #32]")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #48")
	w("\tret")
}

// emitWriterCloseHelper writes __method_Writer_close(writer) -> Option[IoError]:
// close(2) the handle's fd (from [writer+8]); return None on success or
// Some(IoError) on failure. Non-leaf. x0=writer handle.
func emitWriterCloseHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__method_Writer_close"))
	w("\tstp x29, x30, [sp, #-16]!")
	w("\tmov x29, sp")
	w("\tldr w0, [x0, #8]") // fd @ ptr+8
	w("\tmov x8, #57")      // close
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_wrc_err")
	emitOptionBox(w, 1, "")
	w("\tb .Lssa_wrc_ret")
	w(".Lssa_wrc_err:")
	w("\tneg x9, x0") // errno (x9 survives the inline empty-string alloc)
	emitEmptyString(w, "x1")
	w("\tmov x0, x9") // errno
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x9, x0") // IoError box (x9 survives the box alloc below)
	emitOptionBox(w, 0, "x9")
	w(".Lssa_wrc_ret:")
	w("\tldp x29, x30, [sp], #16")
	w("\tret")
}

// emitOpenReaderHelper: open_reader(path) — O_RDONLY. The read-only sibling of
// open_writer (identical handle shape: fd at [ptr+8], immortal rc).
func emitOpenReaderHelper(w func(string, ...any)) {
	emitOpenHandleHelper(w, "open_reader", "or", 0, 0)
}

// emitStdHandleHelper writes stdin / stdout / stderr → a Reader/Writer handle
// wrapping the fixed fd (0 / 1 / 2). The result is a bare handle (no Result
// wrapper); the value pointer is returned in x0. Leaf.
func emitStdHandleHelper(name string, fd int) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tmov w9, #%d", fd)
		emitWriterHandleAlloc(w, "x0", "w9")
		w("\tret")
	}
}

// emitReaderReadChunkHelper writes __method_Reader_read_chunk(reader, n) ->
// Option[string]: a single read(2) of up to n bytes from the handle's fd (loaded
// from [reader+8]) into a fresh single-word rc string. Returns None on EOF/error
// (read <= 0) or Some(string) with the actual byte count as its length. Leaf: the
// read svc preserves every register but x0, so fd/n/data survive without spills.
// x0=reader handle, x1=n.
func emitReaderReadChunkHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__method_Reader_read_chunk"))
	w("\tldr w9, [x0, #8]") // fd @ ptr+8
	w("\tmov x10, x1")      // n
	// Allocate a single-word rc string: 8-byte header + n + 1 NUL.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x10, #9")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w7, #1")
	w("\tstr w7, [x4]")    // rc = 1
	w("\tadd x11, x4, #8") // data ptr
	// read(fd, data, n)
	w("\tmov x0, x9")
	w("\tmov x1, x11")
	w("\tmov x2, x10")
	w("\tmov x8, #63") // read
	w("\tsvc #0")
	w("\tcmp x0, #0")
	w("\tble .Lssa_rrc_none")  // EOF or error → None
	w("\tstur w0, [x11, #-4]") // len = bytes read
	w("\tadd x1, x11, x0")
	w("\tstrb wzr, [x1]") // trailing NUL
	// Some(string): box {rc=1, tag=0, string@8}.
	emitOptionBox(w, 0, "x11")
	w("\tret")
	w(".Lssa_rrc_none:")
	// None: box {rc=1, tag=1}.
	emitOptionBox(w, 1, "")
	w("\tret")
}

// emitReaderReadLineHelper writes __method_Reader_read_line(reader) ->
// Option[string]: read one byte at a time from the handle's fd (loaded from
// [reader+8]) into the shared 4 KiB .bss line buffer until '\n' (kept), 4 KiB, or
// EOF/error, then copy the line into a fresh right-sized single-word rc string.
// Returns None when the first read returns 0 (EOF before any byte), else
// Some(line). Leaf: the read svc preserves every register but x0. x19=buffer /
// x20=bytes read / x21=scratch-then-data / x22=fd. x0=reader handle.
func emitReaderReadLineHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__method_Reader_read_line"))
	w("\tstp x29, x30, [sp, #-48]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tstp x21, x22, [sp, #32]")
	w("\tldr w22, [x0, #8]") // fd @ ptr+8
	w("\tadrp x19, %s", readlineBufSym)
	w("\tadd x19, x19, #:lo12:%s", readlineBufSym)
	w("\tmov x20, #0") // bytes read
	w(".Lssa_rrl_loop:")
	w("\tcmp x20, #%d", readlineBytes)
	w("\tb.ge .Lssa_rrl_done")
	// read(fd, buf + bytes, 1)
	w("\tmov w0, w22")
	w("\tadd x1, x19, x20")
	w("\tmov x2, #1")
	w("\tmov x8, #63") // read
	w("\tsvc #0")
	w("\tcmp x0, #1")
	w("\tb.lt .Lssa_rrl_done") // EOF (0) or error (<0) → finish
	w("\tadd x21, x19, x20")
	w("\tldrb w21, [x21]") // the byte just read
	w("\tadd x20, x20, #1")
	w("\tcmp w21, #10") // '\n' — kept in the line
	w("\tb.eq .Lssa_rrl_done")
	w("\tb .Lssa_rrl_loop")
	w(".Lssa_rrl_done:")
	w("\tcbz x20, .Lssa_rrl_none") // no bytes → None (EOF)
	// Allocate a single-word rc string of x20 bytes (+ NUL) and copy the line.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x20, #9")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w7, #1")
	w("\tstr w7, [x4]")      // rc = 1
	w("\tstr w20, [x4, #4]") // len = bytes read
	w("\tadd x21, x4, #8")   // data ptr
	w("\tmov x9, #0")
	w(".Lssa_rrl_cp:")
	w("\tcmp x9, x20")
	w("\tb.hs .Lssa_rrl_cpd")
	w("\tldrb w10, [x19, x9]")
	w("\tstrb w10, [x21, x9]")
	w("\tadd x9, x9, #1")
	w("\tb .Lssa_rrl_cp")
	w(".Lssa_rrl_cpd:")
	w("\tstrb wzr, [x21, x20]") // trailing NUL
	// Some(string): box {rc=1, tag=0, string@8}.
	emitOptionBox(w, 0, "x21")
	w("\tb .Lssa_rrl_ret")
	w(".Lssa_rrl_none:")
	emitOptionBox(w, 1, "")
	w(".Lssa_rrl_ret:")
	w("\tldp x21, x22, [sp, #32]")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #48")
	w("\tret")
}

// emitReaderCloseHelper writes __method_Reader_close(reader) -> Option[IoError]:
// close(2) the handle's fd (from [reader+8]); the read-side twin of Writer.close.
// Non-leaf. x0=reader handle.
func emitReaderCloseHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__method_Reader_close"))
	w("\tstp x29, x30, [sp, #-16]!")
	w("\tmov x29, sp")
	w("\tldr w0, [x0, #8]") // fd @ ptr+8
	w("\tmov x8, #57")      // close
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_rdc_err")
	emitOptionBox(w, 1, "")
	w("\tb .Lssa_rdc_ret")
	w(".Lssa_rdc_err:")
	w("\tneg x9, x0") // errno
	emitEmptyString(w, "x1")
	w("\tmov x0, x9")
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x9, x0")
	emitOptionBox(w, 0, "x9")
	w(".Lssa_rdc_ret:")
	w("\tldp x29, x30, [sp], #16")
	w("\tret")
}

// emitPollHelper writes poll(fds, timeout_ms) → i32: the std/task reactor's
// readiness multiplexer. `fds` is a single-word i32[] (len at [ptr-4], stride 4);
// the helper bump-allocates a transient struct pollfd[] (8 bytes each: i32 fd,
// i16 events, i16 revents), requests POLLIN on every fd, calls ppoll(2), and
// returns the INDEX of the first readable fd, or -1 on timeout / none. A
// timeout_ms < 0 blocks indefinitely (NULL timespec); >= 0 builds a timespec.
// x19=nfds / x20=fds ptr / x21=pollfd buf / x22=loop i / x23=timeout_ms; the
// 16-byte timespec scratch lives at [x29,#64].
func emitPollHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("poll"))
	w("\tstp x29, x30, [sp, #-80]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tstp x21, x22, [sp, #32]")
	w("\tstr x23, [sp, #48]")
	w("\tmov x20, x0")          // fds ptr
	w("\tmov x23, x1")          // timeout_ms
	w("\tldur w19, [x20, #-4]") // nfds
	w("\tcmp w19, #0")
	w("\tb.le .Lssa_poll_none")
	// Bump-allocate the transient pollfd[]: nfds * 8 bytes.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tlsl x5, x19, #3")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov x21, x4") // pollfd buf
	// Marshal: pollfd[i] = { fd = fds[i], events = POLLIN, revents = 0 }.
	w("\tmov x22, #0")
	w(".Lssa_poll_fill:")
	w("\tcmp x22, x19")
	w("\tb.ge .Lssa_poll_filled")
	w("\tldr w0, [x20, x22, lsl #2]") // fd = fds[i]
	w("\tadd x9, x21, x22, lsl #3")   // &pollfd[i]
	w("\tstr w0, [x9]")               // .fd
	w("\tmov w1, #1")
	w("\tstrh w1, [x9, #4]")  // .events = POLLIN
	w("\tstrh wzr, [x9, #6]") // .revents = 0
	w("\tadd x22, x22, #1")
	w("\tb .Lssa_poll_fill")
	w(".Lssa_poll_filled:")
	// timespec: timeout_ms < 0 → NULL (block); else { sec, nsec }.
	w("\tcmp x23, #0")
	w("\tb.lt .Lssa_poll_infinite")
	w("\tmov x9, #1000")
	w("\tudiv x10, x23, x9")      // sec = ms / 1000
	w("\tmsub x11, x10, x9, x23") // rem ms
	w("\tmov x12, #1000000")
	w("\tmul x11, x11, x12") // nsec = rem * 1e6
	w("\tadd x2, x29, #64")  // &timespec
	w("\tstp x10, x11, [x2]")
	w("\tb .Lssa_poll_call")
	w(".Lssa_poll_infinite:")
	w("\tmov x2, #0") // NULL tmo_p → block
	w(".Lssa_poll_call:")
	w("\tmov x0, x21") // fds buf
	w("\tmov w1, w19") // nfds
	w("\tmov x3, #0")  // sigmask = NULL
	w("\tmov x4, #0")  // sigsetsize
	w("\tmov x8, #73") // ppoll
	w("\tsvc #0")
	// Scan revents for the first POLLIN-ready fd.
	w("\tmov x22, #0")
	w(".Lssa_poll_scan:")
	w("\tcmp x22, x19")
	w("\tb.ge .Lssa_poll_none")
	w("\tadd x9, x21, x22, lsl #3")
	w("\tldrh w0, [x9, #6]") // revents
	w("\tand w0, w0, #1")    // POLLIN
	w("\tcbnz w0, .Lssa_poll_found")
	w("\tadd x22, x22, #1")
	w("\tb .Lssa_poll_scan")
	w(".Lssa_poll_found:")
	w("\tmov x0, x22")
	w("\tb .Lssa_poll_ret")
	w(".Lssa_poll_none:")
	w("\tmov x0, #-1")
	w(".Lssa_poll_ret:")
	w("\tldr x23, [sp, #48]")
	w("\tldp x21, x22, [sp, #32]")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #80")
	w("\tret")
}

// runtimeHelperDeps records helper→helper call edges: a helper that tail-calls
// another must have that callee emitted too, since the module never references
// it directly. Transitively closed by referencedRuntimeHelpers.
var runtimeHelperDeps = map[string][]string{
	"__fern_closure_drop":           {"__fern_box_free", "__fern_rc_dec"},
	"__fern_box_free":               {"__free"},
	"__fern_arr_dec":                {"__free"},
	"__fern_drop_arr_str":           {"__fern_str_dec", "__fern_arr_dec"},
	"__fern_drop_arr_ptr":           {"__fern_rc_dec", "__fern_arr_dec"},
	"__fern_map_drop":               {"__free"},
	"__fern_map_hash_seed":          {"random_i32"},
	"proc_exec":                     {"__alloc"},
	"__alloc_reuse":                 {"__free", "__alloc"},
	"__fern_arr_push_grow_ptr":      {"__fern_arr_push_grow", "__fern_rc_inc"},
	"__fern_arr_push_grow_str":      {"__fern_arr_push_grow", "__fern_rc_inc"},
	"__fern_arr_push_grow_move_ptr": {"__fern_arr_push_grow", "__fern_rc_inc"},
	"__fern_arr_push_grow_move_str": {"__fern_arr_push_grow", "__fern_rc_inc"},
	"__fern_arr_cow_inplace_ptr":    {"__fern_arr_cow_inplace", "__fern_rc_inc"},
	"__fern_arr_cow_inplace_str":    {"__fern_arr_cow_inplace", "__fern_rc_inc"},
	"write_file":                    {"__fern_io_error"},
	"read_file":                     {"__fern_io_error", "__fern_utf8_valid"},
	"stat":                          {"__fern_io_error"},
	"lstat":                         {"__fern_io_error"},
	"__method_string_as_bytes":      {"__slice_make"},
	"read_file_bytes":               {"__fern_io_error", "__alloc_u8"},
	"remove_file":                   {"__fern_io_error"},
	"create_dir_all":                {"__fern_io_error"},
	"remove_dir_all":                {"__fern_io_error"},
	"temp_dir":                      {"__fern_io_error"},
	"read_dir":                      {"__fern_io_error"},
	"open_writer":                   {"__fern_io_error"},
	"__method_Writer_write":         {"__fern_io_error"},
	"__method_Writer_close":         {"__fern_io_error"},
	"open_reader":                   {"__fern_io_error"},
	"__method_Reader_close":         {"__fern_io_error"},
	"open_appender":                 {"__fern_io_error"},
	"__pow_f64":                     {"__log_f64", "__exp_f64"},
	"__sin_f64":                     {"__rem_pio2_large"},
	"__cos_f64":                     {"__rem_pio2_large"},
}

// heapUsingHelpers are runtime helpers that bump-allocate on the SSA heap, or
// read its cursor, so the arena must be reserved whenever one is referenced even
// if no program body has a direct heap op.
var heapUsingHelpers = map[string]bool{
	"__free":                        true,
	"__fern_box_free":               true,
	"__fern_arr_dec":                true,
	"__str_concat":                  true,
	"__fern_arr_push_grow":          true,
	"__fern_arr_push_grow_ptr":      true,
	"__fern_arr_push_grow_str":      true,
	"__fern_arr_push_grow_move_ptr": true,
	"__fern_arr_push_grow_move_str": true,
	"__alloc_u8":                    true,
	"__alloc":                       true,
	"__alloc_reuse":                 true,
	"__fern_arr_cow_inplace":        true,
	"__fern_arr_cow_inplace_ptr":    true,
	"__fern_arr_cow_inplace_str":    true,
	"__fern_heap_bump_bytes":        true,
	"__slice_make":                  true,
	"stat":                          true,
	"lstat":                         true,
	"string_from_bytes_unchecked":   true,
	"__str_slice":                   true,
	"args":                          true,
	"strbuf_take":                   true,
	"env":                           true,
	"write_file":                    true,
	"read_file":                     true,
	"read_file_bytes":               true,
	"remove_file":                   true,
	"create_dir_all":                true,
	"remove_dir_all":                true,
	"temp_dir":                      true,
	"read_dir":                      true,
	"random_bytes":                  true,
	"tcp_recv":                      true,
	"poll":                          true,
	"open_writer":                   true,
	"__method_Writer_write":         true,
	"__method_Writer_close":         true,
	"open_reader":                   true,
	"__method_Reader_read_chunk":    true,
	"__method_Reader_read_line":     true,
	"__method_Reader_close":         true,
	"open_appender":                 true,
	"stdin":                         true,
	"stdout":                        true,
	"stderr":                        true,
	"__fern_io_error":               true,
}

// collectStrings assigns a .rodata label to each unique OpConstString literal, in
// first-seen order over the (sorted) module functions — the arm64 sibling of the
// x86-64 path's collector.
func collectStrings(progs map[string]*x86.Program, names []string) (map[string]string, []string) {
	labels := map[string]string{}
	var order []string
	for _, name := range names {
		for _, blk := range progs[name].Blocks {
			for _, in := range blk.Insts {
				if in.Op == x86.ConstStr {
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

// collectSentinels assigns a .rodata label to each distinct enum-sentinel tag
// (EnumSentinel.Imm) used in the module, in first-seen order — so every
// OpEnumSentinel for a given tag references the same shared static cell.
func collectSentinels(progs map[string]*x86.Program, names []string) (map[int64]string, []int64) {
	labels := map[int64]string{}
	var order []int64
	for _, name := range names {
		for _, blk := range progs[name].Blocks {
			for _, in := range blk.Insts {
				if in.Op == x86.EnumSentinel {
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

// staticClosureTargets returns, in module order, every function a CAPTURE-FREE
// MakeClosure names. Such a cell holds {fn_idx, env=0, drop_idx=0, 0} — all four
// words known at compile time and never written again — so one immortal .rodata
// cell per target stands in for every evaluation of the value. Allocating it
// instead cost a bump sequence and a heap-guard call each time the value was
// materialised, which for a bare function name passed to a helper (core/map
// hands __map_lookup_keyed its hash and eq functions on every lookup) is per
// call.
func staticClosureTargets(progs map[string]*x86.Program, names []string) []string {
	seen := map[string]bool{}
	var order []string
	for _, name := range names {
		for _, blk := range progs[name].Blocks {
			for _, in := range blk.Insts {
				if in.Op != x86.MakeClosure || len(in.ArgLocs) > 0 || seen[in.Callee] {
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

// referencedRuntimeHelpers returns, sorted, every runtime helper any emitted
// program calls (that arm64 has an emitter for), plus the transitive closure of
// their helper→helper dependencies (runtimeHelperDeps).
// checkNoDanglingCalls reports a `bl` to a label the module never defines.
//
// referencedRuntimeHelpers walks the call graph and emits the runtime helpers
// it finds, but a callee with no entry in runtimeHelperEmitters is simply
// skipped — a user function is a legitimate skip, and until this a helper the
// table had never heard of was one too. The call went out with nothing behind
// it and the program died in the assembler, half an hour of compilation later,
// on a mangled label name that says nothing about which backend owed it.
//
// This is the same condition the assembler checks; the point is to fail here,
// naming the helper, so a coverage gap reads as one.
func checkNoDanglingCalls(asm string) error {
	defined := map[string]bool{}
	var called []string
	seen := map[string]bool{}
	for _, line := range strings.Split(asm, "\n") {
		switch {
		case strings.HasPrefix(line, "\tbl "):
			t := strings.TrimSpace(line[len("\tbl "):])
			if !seen[t] {
				seen[t] = true
				called = append(called, t)
			}
		case len(line) > 0 && line[0] != '\t' && line[0] != '.' && strings.HasSuffix(line, ":"):
			defined[strings.TrimSuffix(line, ":")] = true
		case strings.HasPrefix(line, ".L") && strings.HasSuffix(line, ":"):
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
	return fmt.Errorf("arm64ssa: %d call target(s) the module never defines — a runtime helper this backend does not emit: %s", len(missing), strings.Join(missing, ", "))
}

// referencedRuntimeHelpers returns the hand-written helpers the module calls,
// closed over runtimeHelperDeps, and separately the helpers written in Fern
// (internal/fernrt) it reaches the same way and has not yet lifted into
// progs.
func referencedRuntimeHelpers(progs map[string]*x86.Program) (asm, fern []string) {
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
				if in.Op == x86.Call {
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
	for n := range fernSeen {
		fern = append(fern, n)
	}
	sort.Strings(fern)
	return out, fern
}

// The RC helpers read a 4-byte reference count at [data-8] (the header the IR
// lays down on a fresh OpAlloc block, or closureLines / boxDynLines on theirs;
// data = base+8). They share a guard chain that makes them
// safe on a slot that might hold a non-pointer scalar or a static cell: null,
// below the 0x10000 low-address guard, or the static-sentinel top bit (0x80000000)
// set — all short-circuit. The negative header offset needs the unscaled
// ldur/stur form. Mirrors the native / x86-64 SSA versions.

// emitRcIsUniqueHelper writes __fern_rc_is_unique(data) -> i32: 1 iff data is a
// real, uniquely-owned heap value (rc == 1), else 0.
func emitRcIsUniqueHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rc_is_unique"))
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
// guarded. Returns the pointer it was given (x0 untouched), leaf.
func emitRcIncHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rc_inc"))
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
// same guard chain. Like the flat backend's, it does not free at rc==0: the IR
// gates every release on rc==1 and frees through a type-specific drop
// (__fern_box_free / __fern_arr_dec / ...), so a dec that reaches zero is a
// shared-reference bookkeeping step, never the last owner. Returns the pointer
// it was given (x0 untouched), leaf.
func emitRcDecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rc_dec"))
	w("\tcmp x0, #0x10000")
	w("\tb.lo .Lssa_rcdec_ret")
	w("\tldur w1, [x0, #-8]")
	w("\ttbnz w1, #31, .Lssa_rcdec_ret") // static sentinel
	w("\tcbz w1, .Lssa_rcdec_underflow")
	w("\tsub w1, w1, #1")
	w("\tstur w1, [x0, #-8]")
	w(".Lssa_rcdec_ret:")
	w("\tret")
	// Releasing an already-zero count is an over-release. Count it and leave
	// the refcount alone rather than wrapping it to 0xffffffff, which would
	// turn one bug into an immortal object.
	w(".Lssa_rcdec_underflow:")
	w("\tadrp x2, %s", rcUnderflowSym)
	w("\tadd x2, x2, #:lo12:%s", rcUnderflowSym)
	w("\tldr w3, [x2]")
	w("\tadd w3, w3, #1")
	w("\tstr w3, [x2]")
	w("\tret")
}

// emitRcUnderflowCountHelper emits `__fern_rc_underflow_count() -> i32`, the
// Phase 3 probe that reads back what emitRcDecHelper counted.
func emitRcUnderflowCountHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rc_underflow_count"))
	w("\tadrp x0, %s", rcUnderflowSym)
	w("\tadd x0, x0, #:lo12:%s", rcUnderflowSym)
	w("\tldr w0, [x0]")
	w("\tret")
}

// emitBoxFreeHelper writes __fern_box_free(data, size) -> data: return an
// rc-headed block (base = data-8, size+8 bytes) to the freelist. The IR
// pre-gates the call on rc==1 and has already dropped the box's counted
// fields, so this is only the push. Null / low-address guarded; __free leaves
// x0 alone, so data comes back as base+8.
func emitBoxFreeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_box_free"))
	w("\tcmp x0, #0x10000")
	w("\tb.lo .Lssa_boxfree_ret")
	w("\tstp x29, x30, [sp, #-16]!")
	w("\tmov x29, sp")
	w("\tsub x0, x0, #8") // base
	w("\tmov w1, w1")     // size is an i32
	w("\tadd x1, x1, #8") // plus the rc header
	w("\tbl %s", fnLabel("__free"))
	w("\tadd x0, x0, #8") // data
	w("\tldp x29, x30, [sp], #16")
	w(".Lssa_boxfree_ret:")
	w("\tret")
}

// emitClosureDropHelper writes __fern_closure_drop(data): the scope-exit drop the
// IR inserts for a closure-valued local. Guarded (the 0x10000 low-address check
// also rejects null); reads the rc word at [data-8]; if uniquely held (rc == 1)
// it tail-calls __fern_box_free(data, payload_size) to release the cell (size
// from [data-4] into x1), otherwise tail-calls __fern_rc_dec(data) to drop a
// shared reference. Tail calls use `b` so the callee's ret returns to our caller.
func emitClosureDropHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_closure_drop"))
	w("\tcmp x0, #0x10000")
	w("\tb.lo .Lssa_cd_ret")
	w("\tldur w1, [x0, #-8]") // rc
	w("\tcmp w1, #1")
	w("\tb.ne .Lssa_cd_dec")  // rc != 1 (shared or static sentinel) → dec
	w("\tldur w1, [x0, #-4]") // payload size → arg2 (x1)
	w("\tb %s", fnLabel("__fern_box_free"))
	w(".Lssa_cd_dec:")
	w("\tb %s", fnLabel("__fern_rc_dec"))
	w(".Lssa_cd_ret:")
	w("\tret")
}

// emitAllocHelper writes __alloc(n) -> ptr, the one allocator behind every
// block this backend hands out: a raw 16-aligned block of at least n bytes with
// NO header of its own (callers lay down rc / cap / len words themselves).
// It pops the block's size class off the freelist when one is waiting and
// otherwise bumps the cursor by the class's rounded size, so a block's
// physical extent always covers the class __free will later push it on.
// Popped memory is NOT zeroed, the same contract as the flat __fern_alloc.
// Clobbers x0..x10; the inline sites go through __ssa_alloc_pres instead.
func emitAllocHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__alloc"))
	w("\tmov w0, w0") // size is a non-negative i32
	emitFreelistClass(w, "alloc", "x0", "x1", "x2", "x3", "x4", "x5", "x6", "x7", ".Lssa_alloc_bump")
	w("\tadrp x2, %s", freelistSym)
	w("\tadd x2, x2, #:lo12:%s", freelistSym)
	w("\tldr x3, [x2, x1, lsl #3]") // head
	w("\tcbz x3, .Lssa_alloc_bump")
	w("\tldr x4, [x3]")             // head.next
	w("\tstr x4, [x2, x1, lsl #3]") // heads[idx] = next
	w("\tmov x0, x3")
	w("\tret")
	w(".Lssa_alloc_bump:")
	w("\tadrp x8, %s", heapPtrSym)
	w("\tadd x8, x8, #:lo12:%s", heapPtrSym)
	w("\tldr x9, [x8]")
	w("\tadd x9, x9, #15")
	w("\tand x9, x9, #-16") // base, 16-aligned
	w("\tadd x10, x9, x0")  // cursor = base + rounded size
	w("\tstr x10, [x8]")
	emitHeapGuardCall(w)
	w("\tmov x0, x9")
	w("\tret")
}

// The subprocess trio, the arm64 Linux syscalls behind Fern's `proc_*`
// builtins. Ported from the flat backend, which had them and this one did not
// — asm_modload_run spawns per-module workers, so it could not be compiled here
// at all.
//
// Strings and arrays are the same shape as everywhere else in this backend: a
// data pointer with the byte/element count at [p, #-4] and the refcount at
// [p, #-8]. execve wants NUL-terminated C strings and a NULL-terminated argv,
// so proc_exec builds both.

// emitProcForkHelper writes `proc_fork() -> i32`: 0 in the child, the
// child's pid in the parent, -errno on failure.
//
// arm64 Linux has no bare fork(2). fork is clone(SIGCHLD, 0, 0, 0, 0) — the
// kernel's return shape is already the contract, so nothing to normalise.
func emitProcForkHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("proc_fork"))
	w("\tmov x0, #17") // flags = SIGCHLD
	w("\tmov x1, #0")  // newsp = 0: share the parent's, copy-on-write
	w("\tmov x2, #0")  // parent_tid
	w("\tmov x3, #0")  // tls
	w("\tmov x4, #0")  // child_tid
	w("\tmov x8, #220")
	w("\tsvc #0")
	w("\tret")
}

// emitProcWaitpidHelper writes `proc_waitpid(pid) -> i32`: a blocking
// wait4, then the status-word decode the shell uses —
//
//	WIFEXITED  ((status & 0x7f) == 0) -> (status >> 8) & 0xff
//	else (signal death)               -> 128 + (status & 0x7f)
//
// so a bounds-trapped worker surfaces as its raw exit code, e.g. 134. A failing
// syscall returns -errno as-is.
func emitProcWaitpidHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("proc_waitpid"))
	w("\tstp x29, x30, [sp, #-32]!")
	w("\tmov x29, sp")
	w("\tsxtw x0, w0")      // pid
	w("\tadd x1, x29, #16") // &status
	w("\tmov x2, #0")       // options
	w("\tmov x3, #0")       // rusage = NULL
	w("\tmov x8, #260")
	w("\tsvc #0")
	w("\tcmp x0, #0")
	w("\tb.lt .Lssa_wait_done") // -errno: return as-is
	w("\tldr w9, [x29, #16]")
	w("\tand w10, w9, #0x7f") // termination signal (0 = exited)
	w("\tcbnz w10, .Lssa_wait_sig")
	w("\tlsr w0, w9, #8")
	w("\tand w0, w0, #0xff")
	w("\tb .Lssa_wait_done")
	w(".Lssa_wait_sig:")
	w("\tadd w0, w10, #128")
	w(".Lssa_wait_done:")
	w("\tldp x29, x30, [sp], #32")
	w("\tret")
}

// emitProcExecHelper writes `proc_exec(path, args) -> i32`.
//
// It only ever returns on failure (-errno): a successful execve replaces the
// image. argv is [path, args...] because execve does not prepend argv[0], and
// the NUL-terminated copies are deliberately never freed — nothing outlives the
// call to free them for, and on the failure path the process is about to die.
func emitProcExecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("proc_exec"))
	w("\tstp x29, x30, [sp, #-80]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tstp x21, x22, [sp, #32]")
	w("\tstp x23, x24, [sp, #48]")
	w("\tstp x25, x26, [sp, #64]")
	w("\tmov x19, x1") // args array
	w("\tmov x25, x0") // path bytes

	// argv[0]: a NUL-terminated copy of path.
	w("\tldur w20, [x25, #-4]") // path length
	w("\tadd w0, w20, #1")
	w("\tbl %s", fnLabel("__alloc"))
	w("\tmov x26, x0")
	w("\tmov x24, #0")
	w(".Lssa_pexec_pcopy:")
	w("\tcmp x24, x20")
	w("\tb.ge .Lssa_pexec_pcopy_done")
	w("\tldrb w1, [x25, x24]")
	w("\tstrb w1, [x26, x24]")
	w("\tadd x24, x24, #1")
	w("\tb .Lssa_pexec_pcopy")
	w(".Lssa_pexec_pcopy_done:")
	w("\tstrb wzr, [x26, x20]")

	// argv itself: argc + 2 slots — argv[0] plus the NULL terminator.
	w("\tldur w20, [x19, #-4]") // argc
	w("\tadd w0, w20, #2")
	w("\tlsl w0, w0, #3")
	w("\tbl %s", fnLabel("__alloc"))
	w("\tmov x21, x0")
	w("\tstr x26, [x21]")
	w("\tmov x22, #0")

	w(".Lssa_pexec_arg:")
	w("\tcmp x22, x20")
	w("\tb.ge .Lssa_pexec_arg_done")
	w("\tldr x25, [x19, x22, lsl #3]") // args[i]
	w("\tldur w24, [x25, #-4]")
	w("\tadd w0, w24, #1")
	w("\tbl %s", fnLabel("__alloc"))
	w("\tmov x23, x0")
	w("\tmov x1, #0")
	w(".Lssa_pexec_acopy:")
	w("\tcmp x1, x24")
	w("\tb.ge .Lssa_pexec_acopy_done")
	w("\tldrb w2, [x25, x1]")
	w("\tstrb w2, [x23, x1]")
	w("\tadd x1, x1, #1")
	w("\tb .Lssa_pexec_acopy")
	w(".Lssa_pexec_acopy_done:")
	w("\tstrb wzr, [x23, x24]")
	w("\tadd x1, x22, #1")
	w("\tstr x23, [x21, x1, lsl #3]")
	w("\tadd x22, x22, #1")
	w("\tb .Lssa_pexec_arg")
	w(".Lssa_pexec_arg_done:")
	w("\tadd x1, x20, #1")
	w("\tstr xzr, [x21, x1, lsl #3]")

	w("\tldr x0, [x21]") // path
	w("\tmov x1, x21")   // argv
	w("\tadrp x2, %s", envpSym)
	w("\tadd x2, x2, #:lo12:%s", envpSym)
	w("\tldr x2, [x2]")
	w("\tmov x8, #221")
	w("\tsvc #0")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x21, x22, [sp, #32]")
	w("\tldp x23, x24, [sp, #48]")
	w("\tldp x25, x26, [sp, #64]")
	w("\tldp x29, x30, [sp], #80")
	w("\tret")
}

// emitFreeHelper writes __free(base, n): push the n-byte block at base onto its
// size class's intrusive freelist (successor pointer in the block's first 8
// bytes), for __alloc to hand out again. Blocks above 1 GiB were never
// freelisted and are dropped. x0 is left untouched (__fern_box_free relies on
// that); clobbers x1..x8. Leaf.
func emitFreeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__free"))
	w("\tmov w1, w1") // size is an i32
	emitFreelistClass(w, "free", "x1", "x2", "x3", "x4", "x5", "x6", "x7", "x8", ".Lssa_free_ret")
	w("\tadrp x3, %s", freelistSym)
	w("\tadd x3, x3, #:lo12:%s", freelistSym)
	w("\tldr x4, [x3, x2, lsl #3]") // old head
	w("\tstr x4, [x0]")             // base.next = old head
	w("\tstr x0, [x3, x2, lsl #3]") // heads[idx] = base
	w(".Lssa_free_ret:")
	w("\tret")
}

// emitMemsetHelper writes __memset(dst, byte, n): splat the low byte of `byte`
// across a 64-bit pattern, store it 8 bytes at a time, then finish the tail one
// byte at a time. The Map runtime uses it to fill a fresh bucket array with the
// 0xff empty-slot marker. Void, leaf.
func emitMemsetHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__memset"))
	w("\tand w1, w1, #0xff")
	w("\torr w3, w1, w1, lsl #8")
	w("\torr w3, w3, w3, lsl #16")
	w("\torr x3, x3, x3, lsl #32")
	w(".Lssa_mset_word:")
	w("\tcmp x2, #8")
	w("\tb.lt .Lssa_mset_tail")
	w("\tstr x3, [x0], #8")
	w("\tsub x2, x2, #8")
	w("\tb .Lssa_mset_word")
	w(".Lssa_mset_tail:")
	w("\tcmp x2, #0")
	w("\tb.eq .Lssa_mset_done")
	w("\tstrb w1, [x0], #1")
	w("\tsub x2, x2, #1")
	w("\tb .Lssa_mset_tail")
	w(".Lssa_mset_done:")
	w("\tret")
}

// Single-word strings carry their byte length as a 4-byte field immediately
// before the data (length at [ptr-4]); heap strings also have the rc word at
// [ptr-8], literals an immortal sentinel there. The string helpers below match
// that layout, the arm64 siblings of the x86-64 string helpers.

// emitStrEqHelper writes __str_eq(a, b) -> i32: 1 if the two single-word strings
// are byte-equal, else 0. Fast paths on pointer identity and length mismatch,
// then 8 bytes per iteration, one 4-byte step and a tail of at most 3. Equality
// needs no first-difference position, so a whole word compares in one cmp — the
// byte loop this replaces cost six instructions per byte, which is the whole
// cost of a linear symbol-table lookup. Leaf; both pointers are advanced.
func emitStrEqHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_eq"))
	w("\tcmp x0, x1")
	w("\tb.eq .Lssa_streq_eq") // same pointer → equal
	w("\tldur w2, [x0, #-4]")  // len a — also the remaining count below
	w("\tldur w3, [x1, #-4]")  // len b
	w("\tcmp w2, w3")
	w("\tb.ne .Lssa_streq_neq") // different lengths
	w(".Lssa_streq_8:")
	w("\tcmp w2, #8")
	w("\tb.lo .Lssa_streq_4")
	w("\tldr x4, [x0], #8")
	w("\tldr x5, [x1], #8")
	w("\tcmp x4, x5")
	w("\tb.ne .Lssa_streq_neq")
	w("\tsub w2, w2, #8")
	w("\tb .Lssa_streq_8")
	w(".Lssa_streq_4:")
	w("\tcmp w2, #4")
	w("\tb.lo .Lssa_streq_1")
	w("\tldr w4, [x0], #4")
	w("\tldr w5, [x1], #4")
	w("\tcmp w4, w5")
	w("\tb.ne .Lssa_streq_neq")
	w("\tsub w2, w2, #4")
	w(".Lssa_streq_1:")
	w("\tcbz w2, .Lssa_streq_eq")
	w(".Lssa_streq_byte:")
	w("\tldrb w4, [x0], #1")
	w("\tldrb w5, [x1], #1")
	w("\tcmp w4, w5")
	w("\tb.ne .Lssa_streq_neq")
	w("\tsubs w2, w2, #1")
	w("\tb.ne .Lssa_streq_byte")
	w(".Lssa_streq_eq:")
	w("\tmov x0, #1")
	w("\tret")
	w(".Lssa_streq_neq:")
	w("\tmov x0, #0")
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
	w("\tldur w2, [x0, #-4]") // la
	w("\tldur w3, [x1, #-4]") // lb
	w("\tcmp w2, w3")
	w("\tcsel w4, w2, w3, lo") // n = min(la, lb), unsigned
	w("\tmov w5, #0")          // i = 0
	w(".Lssa_strord_loop:")
	w("\tcmp w5, w4")
	w("\tb.hs .Lssa_strord_len")
	w("\tldrb w6, [x0, x5]")
	w("\tldrb w7, [x1, x5]")
	w("\tcmp w6, w7")
	w("\tb.ne .Lssa_strord_diff")
	w("\tadd w5, w5, #1")
	w("\tb .Lssa_strord_loop")
	w(".Lssa_strord_diff:")
	w("\tsub w0, w6, w7")
	w("\tsxtw x0, w0")
	w("\tret")
	w(".Lssa_strord_len:")
	w("\tsub w0, w2, w3")
	w("\tsxtw x0, w0")
	w("\tret")
}

// emitStrConcatHelper writes __str_concat(a, b) -> new data pointer: bump-allocate
// a fresh length-prefixed string holding a's bytes then b's, and return its data
// pointer. Inline-allocates the rc-headed block (rc=1 at base+0, total length at
// base+4, data at base+8 — the same header ConstStr / heap strings use) and
// copies each operand through __ssa_bcopy.
func emitStrConcatHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_concat"))
	w("\tldur w2, [x0, #-4]") // la
	w("\tldur w3, [x1, #-4]") // lb
	w("\tadd w4, w2, w3")     // total = la + lb (zero-extends into x4)
	// Bump-allocate total+8 bytes: base = align8(cursor); rc=1 at base+0, len at
	// base+4; cursor advances past header+total; data = base+8.
	w("\tadrp x5, %s", heapPtrSym)
	w("\tadd x5, x5, #:lo12:%s", heapPtrSym) // x5 = &cursor
	w("\tldr x6, [x5]")
	w("\tadd x6, x6, #15")
	w("\tand x6, x6, #-16") // x6 = base
	w("\tmov w7, #1")
	w("\tstr w7, [x6]")     // rc = 1
	w("\tstr w4, [x6, #4]") // len = total
	w("\tadd x7, x6, #8")
	w("\tadd x7, x7, x4") // new cursor = base + 8 + total
	w("\tstr x7, [x5]")
	emitHeapGuardCall(w)
	w("\tadd x9, x6, #8")  // x9 = data
	w("\tadd x10, x9, x2") // x10 = data + la, where b's bytes go
	// b and lb move clear of x0..x2 before the first copy consumes those.
	w("\tmov x11, x1") // b
	w("\tmov x12, x3") // lb
	emitBcopyCall(w, "x9", "x0", "x2")
	emitBcopyCall(w, "x10", "x11", "x12")
	w("\tmov x0, x9") // return data
	w("\tret")
}

// emitStrDecHelper writes __fern_str_dec(ptr): the scope-exit drop for a
// string-valued local. Guarded (null / low-address / immortal-sentinel top bit —
// so it skips .rodata literals); reads the rc at [ptr-8]; rc<=1 leaks, else
// drops a shared reference. Strings are the one class still not reclaimed:
// their producers do not all put the block base at ptr-8 (strbuf_take hands
// out a 16-byte-headed buffer), so a push from here could strand a block's
// first slot outside its class and hand __alloc an undersized block. Leaf.
func emitStrDecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_str_dec"))
	w("\tcmp x0, #0x10000")
	w("\tb.lo .Lssa_strdec_ret")
	w("\tldur w1, [x0, #-8]")             // rc
	w("\ttbnz w1, #31, .Lssa_strdec_ret") // immortal literal sentinel
	w("\tcmp w1, #1")
	w("\tb.le .Lssa_strdec_ret") // rc<=1: unique (leak) or already dropped
	w("\tsub w1, w1, #1")
	w("\tstur w1, [x0, #-8]")
	w(".Lssa_strdec_ret:")
	w("\tret")
}

// emitArrDecHelper writes __fern_arr_dec(data, stride): the array-drop the IR
// inserts at scope exit. The array element pointer carries a header of
// max(16, stride) bytes with its rc at [data-8] (cap@-12, rc@-8, len@-4).
// Guarded (null / low-address / static sentinel); on the last reference
// (rc==1) the buffer goes back to the freelist (base = data - headerBytes,
// headerBytes + cap*stride bytes) - it does NOT walk elements, the
// __fern_drop_arr_* wrappers do that first; rc>1 drops a shared reference.
// Mirrors the flat backend's helper.
func emitArrDecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_arr_dec"))
	w("\tcmp x0, #0x10000")
	w("\tb.lo .Lssa_arrdec_ret")
	w("\tldur w2, [x0, #-8]")             // rc (x1 holds stride)
	w("\ttbnz w2, #31, .Lssa_arrdec_ret") // static sentinel
	w("\tcmp w2, #1")
	w("\tb.lt .Lssa_arrdec_ret") // already dropped
	w("\tb.eq .Lssa_arrdec_free")
	w("\tsub w2, w2, #1")
	w("\tstur w2, [x0, #-8]")
	w(".Lssa_arrdec_ret:")
	w("\tret")
	w(".Lssa_arrdec_free:")
	w("\tstp x29, x30, [sp, #-16]!")
	w("\tmov x29, sp")
	w("\tmov w1, w1")
	w("\tmov x3, #16")
	w("\tcmp x1, #16")
	w("\tcsel x3, x1, x3, hi") // headerBytes = max(16, stride)
	w("\tldur w4, [x0, #-12]") // cap
	w("\tmadd x1, x4, x1, x3") // size = cap*stride + headerBytes
	w("\tsub x0, x0, x3")      // base = data - headerBytes
	w("\tbl %s", fnLabel("__free"))
	w("\tldp x29, x30, [sp], #16")
	w("\tret")
}

// emitStrIdxHelper writes __str_idx(base, idx) -> byte address: the char-index
// helper behind `s[i]`. arm64ssa strings are always heap data pointers with no
// small-string inline optimisation (byte length at [base-4]), so this is the
// byte-stride sibling of __arr_idx: a single unsigned bounds compare against the
// length (a negative idx is huge unsigned and fails too), exit 134 on
// out-of-range, else base + idx. The caller's byte-load reads the char. Leaf.
func emitStrIdxHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_idx"))
	w("\tldur w2, [x0, #-4]") // len
	w("\tcmp w1, w2")
	w("\tb.lo .Lssa_stridx_ok") // idx < len (unsigned)
	w("\tmov x0, #134")
	w("\tmov x8, #94") // exit_group
	w("\tsvc #0")
	w(".Lssa_stridx_ok:")
	w("\tadd x0, x0, x1") // base + idx (byte stride)
	w("\tret")
}

// emitMemchrHelper writes __fern_memchr(s, byte, from) -> the index of the
// first `byte` at or after `from`, or -1 (docs/ATLAS-PLATFORM-PLAN.md §3).
//
// NEON, 16 bytes an iteration, the same kernel the two native arm64 emitters
// serve this op with. This backend needed the op at all only because
// std/string's single-byte search routes through the intrinsic: before that
// adoption no module reachable from this target called it, so the missing entry
// cost nothing and was invisible. It surfaced as a LINK error — `branch to
// undefined label "fn___fern_memchr"` — not a wrong answer, which is the good
// failure mode for a missing helper.
//
// The mask extraction is the interesting half, and it is where NEON differs
// from SSE2: there is no pmovmskb. The idiom is cmeq to 0x00/0xFF per byte,
// then `shrn v0.8b, v0.8h, #4` to narrow each 16-bit lane to one byte carrying
// four mask bits, then fmov the 64 bits out — so each input byte becomes a
// NIBBLE and the lane index is the lowest set bit over four. Conversely the
// splat is cheaper: `dup v1.16b, w1` where SSE2 needs four instructions.
//
// Strings on this backend are ONE word (the data pointer) with the length at
// [ptr-4], not the two-word box the native arm64 backend uses, so the three
// arguments land in x0/x1/x2 with no slot arithmetic. Leaf: no frame, and the
// registers it touches (x0..x3, x8..x12, v0/v1) are all caller-saved — floats
// here live as their f64 bit pattern in a GPR, so no v register is live across
// a call for this to tread on.
//
// No feature detection: Advanced SIMD is mandatory in the declared ARMv8-A
// baseline, so these are hard requirements rather than a fast path.
func emitMemchrHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_memchr"))
	w("\tldur w3, [x0, #-4]") // len
	// A byte outside 0..255 can never occur in the haystack. ONE unsigned
	// compare covers both ends: a negative arrives as a huge unsigned.
	// Checked once, so neither loop below needs a per-iteration guard.
	w("\tcmp x1, #255")
	w("\tb.hi .Lssa_memchr_none")
	// `from` clamps at 0 rather than trapping, matching the interpreter.
	w("\tcmp w2, #0")
	w("\tb.ge .Lssa_memchr_from_ok")
	w("\tmov w2, #0")
	w(".Lssa_memchr_from_ok:")
	w("\tcmp w2, w3")
	w("\tb.ge .Lssa_memchr_none")
	w("\tadd x8, x0, w2, uxtw") // cursor = data + from
	w("\tadd x9, x0, w3, uxtw") // end    = data + len
	w("\tdup v1.16b, w1")
	w(".Lssa_memchr_vec:")
	w("\tsub x10, x9, x8")
	w("\tcmp x10, #16")
	w("\tb.lt .Lssa_memchr_tail")
	// Unaligned load, and never a byte past the string: the branch above
	// entered this only with a full block left. NEON has no alignment
	// requirement, so there is no scalar prologue to pay either.
	w("\tld1 {v0.16b}, [x8]")
	w("\tcmeq v0.16b, v0.16b, v1.16b")
	w("\tshrn v0.8b, v0.8h, #4")
	w("\tfmov x11, d0")
	w("\tcbz x11, .Lssa_memchr_next")
	// Lowest set bit -> lane. Four mask bits per input byte, hence the >>2.
	w("\trbit x12, x11")
	w("\tclz x12, x12")
	w("\tlsr x12, x12, #2")
	w("\tadd x8, x8, x12")
	w("\tsub x0, x8, x0") // back to an index; x0 is the base's last use
	w("\tret")
	w(".Lssa_memchr_next:")
	w("\tadd x8, x8, #16")
	w("\tb .Lssa_memchr_vec")
	// Scalar tail: fewer than 16 bytes left, and the whole algorithm for a
	// string shorter than one block — the common case in a search family.
	w(".Lssa_memchr_tail:")
	w("\tcmp x8, x9")
	w("\tb.ge .Lssa_memchr_none")
	w("\tldrb w10, [x8]")
	w("\tcmp w10, w1")
	w("\tb.eq .Lssa_memchr_tail_hit")
	w("\tadd x8, x8, #1")
	w("\tb .Lssa_memchr_tail")
	w(".Lssa_memchr_tail_hit:")
	w("\tsub x0, x8, x0")
	w("\tret")
	w(".Lssa_memchr_none:")
	w("\tmov x0, #-1")
	w("\tret")
}

// emitRmemchrHelper writes __fern_rmemchr(s, byte, from) -> the index of the
// LAST `byte` at or before `from`, or -1 (docs/ATLAS-PLATFORM-PLAN.md §3).
//
// NEON, 16 bytes an iteration, walking DOWN: the block ENDING at the cursor is
// loaded at `cursor - 15`.
//
// The mask extraction is __memchr's read backwards. shrn gives four mask bits
// per input byte, so __memchr divides the LOWEST set bit by four; here the
// answer is the HIGHEST, and arm64 has no find-highest instruction — `clz`
// counts leading zeros, so the bit index is 63 - clz and the lane is that over
// four. x86-64 gets the same step in one instruction (bsr), which is the only
// place these two vector bodies differ in shape rather than in constants.
//
// Strings on this backend are ONE word (the data pointer) with the length at
// [ptr-4], so the three arguments land in x0/x1/x2 with no slot arithmetic —
// where the native arm64 twin spends a frame unboxing a two-word SSO string
// before it can start. Leaf: no frame, and every register it touches
// (x0..x4, x8..x12, v0/v1) is caller-saved.
//
// The clamp is __memchr's mirrored: a forward scan clamps `from` UP to 0, a
// backward scan clamps it DOWN to len-1, so a negative `from` finds nothing.
func emitRmemchrHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_rmemchr"))
	w("\tldur w3, [x0, #-4]") // len
	// A byte outside 0..255 can never occur; ONE unsigned compare covers
	// both ends, checked once so neither loop needs a per-iteration guard.
	w("\tcmp x1, #255")
	w("\tb.hi .Lssa_rmemchr_none")
	// Clamp `from` down to the last index; an empty string has none.
	w("\tsub w3, w3, #1")
	w("\ttbnz w3, #31, .Lssa_rmemchr_none")
	w("\tcmp w2, w3")
	w("\tb.le .Lssa_rmemchr_from_ok")
	w("\tmov w2, w3")
	w(".Lssa_rmemchr_from_ok:")
	w("\tdup v1.16b, w1")
	// Vector loop while a whole block still fits BELOW the cursor. That bound
	// keeps the load in bounds at the LOW end, where __memchr's "16 or more
	// left" keeps it in bounds at the high end. A negative `from` fails this
	// compare and lands on the tail's guard.
	w("\tcmp w2, #15")
	w("\tb.lt .Lssa_rmemchr_tail")
	// w10 = block base index, x11 = its address. Both are carried across
	// iterations and stepped by 16 rather than recomputed from the cursor.
	w("\tsub w10, w2, #15")
	w("\tadd x11, x0, w10, uxtw")
	w(".Lssa_rmemchr_vec:")
	w("\tld1 {v0.16b}, [x11]")
	w("\tcmeq v0.16b, v0.16b, v1.16b")
	w("\tshrn v0.8b, v0.8h, #4")
	w("\tfmov x12, d0")
	w("\tcbz x12, .Lssa_rmemchr_next")
	// Highest set bit -> lane. Four mask bits per input byte, hence the >>2;
	// 63 - clz because clz counts from the TOP and the rightmost match is the
	// highest set bit.
	w("\tclz x8, x12")
	w("\tmov x9, #63")
	w("\tsub x8, x9, x8")
	w("\tlsr x8, x8, #2")
	w("\tadd w0, w10, w8") // x0's base is dead from here
	w("\tret")
	w(".Lssa_rmemchr_next:")
	w("\tsub w10, w10, #16")
	w("\ttbnz w10, #31, .Lssa_rmemchr_vecdone")
	w("\tsub x11, x11, #16")
	w("\tb .Lssa_rmemchr_vec")
	// The next block would start below 0, so the untested bytes are
	// [0, base-1] where base is the block just cleared. Restore it and hand
	// that cursor to the scalar tail.
	w(".Lssa_rmemchr_vecdone:")
	w("\tadd w10, w10, #15")
	w("\tmov w2, w10")
	// Scalar tail: the final 0..14 bytes, and the whole algorithm for a cursor
	// that never had a full block beneath it.
	w(".Lssa_rmemchr_tail:")
	// Checked HERE rather than before the vector loop, because the tail has
	// TWO entry paths: a cursor with no full block beneath it, and the vector
	// loop running out. The second hands over `base - 1`, which is -1 when the
	// last block started at 0 — a guard only at the top leaves that path
	// reading [data - 1].
	w("\ttbnz w2, #31, .Lssa_rmemchr_none")
	w(".Lssa_rmemchr_scan:")
	w("\tldrb w4, [x0, w2, uxtw]")
	w("\tcmp w4, w1")
	w("\tb.eq .Lssa_rmemchr_found")
	w("\tsub w2, w2, #1")
	w("\ttbz w2, #31, .Lssa_rmemchr_scan")
	w(".Lssa_rmemchr_none:")
	w("\tmov x0, #-1")
	w("\tret")
	w(".Lssa_rmemchr_found:")
	w("\tmov x0, x2")
	w("\tret")
}

// emitCountByteHelper writes __fern_count_byte(s, byte) -> how many bytes of
// `s` equal `byte` (docs/ATLAS-PLATFORM-PLAN.md §3.3, fourth kernel).
//
// NEON, 16 bytes an iteration, the same kernel the native arm64 emitter runs
// (§3.4 step 3). The counting shape is the one place NEON's missing bitmask
// costs nothing: __memchr needs a lane INDEX and pays the shrn nibble trick to
// recover it, where a count needs only a POPULATION. cmeq leaves 0xFF in a
// matching lane, cnt turns each of those into 8, and addv sums the sixteen
// bytes — at most 16 x 8 = 128, so the total fits the byte addv writes and
// cannot overflow. The block's count is that over eight.
//
// Strings on this backend are ONE word (the data pointer) with the length at
// [ptr-4], so the two arguments land in x0/x1 with no slot arithmetic — where
// the native arm64 twin spends a frame unboxing a two-word SSO string first.
// Leaf: no frame, and every register it touches (x0..x6, x8..x11, v0/v1) is
// caller-saved. Floats here live as their f64 bit pattern in a GPR, so no v
// register is live across a call for this to tread on.
//
// No cursor, so no clamp. Both degenerate answers are honest counts rather
// than sentinels: an out-of-range byte counts 0 because nothing can equal it,
// an empty string counts 0 because it has no bytes.
func emitCountByteHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_count_byte"))
	w("\tldur w2, [x0, #-4]") // len
	w("\tmov w4, #0")         // running count
	// A byte outside 0..255 can never occur; ONE unsigned compare covers
	// both ends, checked once so neither loop needs a per-iteration guard.
	// The answer is 0 rather than -1 because a count has no miss.
	w("\tcmp x1, #255")
	w("\tb.hi .Lssa_count_byte_ret")
	w("\tmov x8, x0")           // cursor, as a POINTER: ld1 has no indexed form
	w("\tadd x9, x0, w2, uxtw") // end = data + len
	w("\tdup v1.16b, w1")
	w(".Lssa_count_byte_vec:")
	w("\tsub x10, x9, x8")
	w("\tcmp x10, #16")
	w("\tb.lt .Lssa_count_byte_tail")
	// Unaligned load, and never past the string: the branch above entered
	// this only with a full block left. NEON has no alignment requirement,
	// so there is no scalar prologue to pay either.
	w("\tld1 {v0.16b}, [x8]")
	w("\tcmeq v0.16b, v0.16b, v1.16b")
	w("\tcnt v0.16b, v0.16b")
	w("\taddv b0, v0.16b")
	w("\tumov w11, v0.b[0]")
	w("\tlsr w11, w11, #3")
	w("\tadd w4, w4, w11")
	w("\tadd x8, x8, #16")
	w("\tb .Lssa_count_byte_vec")
	// Scalar tail: the final 0..15 bytes, and the whole algorithm for a
	// string shorter than one block.
	w(".Lssa_count_byte_tail:")
	w("\tcmp x8, x9")
	w("\tb.ge .Lssa_count_byte_ret")
	w("\tldrb w6, [x8]")
	w("\tcmp w6, w1")
	w("\tb.ne .Lssa_count_byte_next")
	w("\tadd w4, w4, #1")
	w(".Lssa_count_byte_next:")
	w("\tadd x8, x8, #1")
	w("\tb .Lssa_count_byte_tail")
	w(".Lssa_count_byte_ret:")
	w("\tmov w0, w4")
	w("\tret")
}

// emitAsciiRunHelper writes __fern_ascii_run(s, from) -> the index of the first
// byte at or after `from` with its high bit set, or len(s) if the rest is ASCII
// (docs/ATLAS-PLATFORM-PLAN.md §3, second kernel).
//
// NEON as __memchr's is, and cheaper than it — but for the opposite reason to
// the one x86-64 gets. There, pmovmskb already gathers the top bit of each
// byte, so the compare disappears. NEON has no bitmask, so a compare is still
// needed to widen "high bit set" into an all-ones lane before shrn can narrow
// it — but `cmlt v0.16b, v0.16b, #0` compares against zero, so it is the SPLAT
// that disappears: there is no operand to broadcast.
//
// The entry existed here first for TOTALITY rather than speed: §3.4 requires an
// op to lower on every backend before any caller may adopt it, precisely so an
// adoption cannot turn into a link error on the target nobody remembered.
// __memchr learned that the expensive way — added to the other six backends,
// adopted, and only then did CI report `branch to undefined label
// "fn___fern_memchr"` from this seventh one.
//
// One-word strings with the length at [ptr-4], so `from` is x1 with no slot
// arithmetic — one register earlier than __memchr's x2, there being no byte
// operand. Leaf: no frame, same caller-saved register set as __memchr's.
//
// Returns the length rather than -1 on a miss, matching the intrinsic's
// branch-free-skip contract everywhere else.
func emitAsciiRunHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_ascii_run"))
	w("\tldur w2, [x0, #-4]") // len
	// `from` clamps at 0 rather than trapping, matching the interpreter.
	w("\tcmp w1, #0")
	w("\tb.ge .Lssa_ascii_from_ok")
	w("\tmov w1, #0")
	w(".Lssa_ascii_from_ok:")
	w("\tcmp w1, w2")
	w("\tb.ge .Lssa_ascii_none")
	w("\tadd x8, x0, w1, uxtw") // cursor = data + from
	w("\tadd x9, x0, w2, uxtw") // end    = data + len
	w(".Lssa_ascii_vec:")
	w("\tsub x10, x9, x8")
	w("\tcmp x10, #16")
	w("\tb.lt .Lssa_ascii_tail")
	// Unaligned load, never past the string: see __memchr's counterpart.
	w("\tld1 {v0.16b}, [x8]")
	w("\tcmlt v0.16b, v0.16b, #0")
	w("\tshrn v0.8b, v0.8h, #4")
	w("\tfmov x11, d0")
	w("\tcbz x11, .Lssa_ascii_next")
	// Lowest set bit -> lane, four mask bits per input byte.
	w("\trbit x12, x11")
	w("\tclz x12, x12")
	w("\tlsr x12, x12, #2")
	w("\tadd x8, x8, x12")
	w("\tsub x0, x8, x0")
	w("\tret")
	w(".Lssa_ascii_next:")
	w("\tadd x8, x8, #16")
	w("\tb .Lssa_ascii_vec")
	// Scalar tail: the final 0..15 bytes, and the whole algorithm for a
	// string shorter than one block.
	w(".Lssa_ascii_tail:")
	w("\tcmp x8, x9")
	w("\tb.ge .Lssa_ascii_none")
	w("\tldrb w3, [x8]") // zero-extending, so bit 7 is the high bit
	w("\ttbnz w3, #7, .Lssa_ascii_tail_hit")
	w("\tadd x8, x8, #1")
	w("\tb .Lssa_ascii_tail")
	w(".Lssa_ascii_tail_hit:")
	w("\tsub x0, x8, x0")
	w("\tret")
	w(".Lssa_ascii_none:")
	w("\tmov x0, x2") // no high byte: the answer is len
	w("\tret")
}

// emitArgsHelper writes args() -> string[]: build a length-prefixed string[] of
// the process arguments from the argc/argv snapshot _start captured. The result
// is memoised in __ssa_args_cache so repeat calls are O(1). Each entry is a fresh
// single-word rc-headered string (rc=1@base, len@base+4, data@base+8) holding the
// argv[i] bytes plus a trailing NUL (for C-shaped consumers). The container is a
// standard rc-headered array (cap/rc/len at data -12/-8/-4) of pointer-stride
// entries. Everything is bump-allocated inline, so this is a leaf. Registers held
// across the outer loop: x1=&cache, x2=argc, x3=argv, x9=container, x10=i.
func emitArgsHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("args"))
	// Fast path: return the memoised container if present.
	w("\tadrp x1, %s", argsCacheSym)
	w("\tadd x1, x1, #:lo12:%s", argsCacheSym)
	w("\tldr x0, [x1]")
	w("\tcbnz x0, .Lssa_args_ret")
	// argc (x2), argv (x3).
	w("\tadrp x2, %s", argcSym)
	w("\tadd x2, x2, #:lo12:%s", argcSym)
	w("\tldr x2, [x2]")
	w("\tadrp x3, %s", argvSym)
	w("\tadd x3, x3, #:lo12:%s", argvSym)
	w("\tldr x3, [x3]")
	// Allocate the container: 16-byte header + argc*8 entry pointers.
	w("\tadrp x4, %s", heapPtrSym)
	w("\tadd x4, x4, #:lo12:%s", heapPtrSym) // x4 = &cursor (held across the loop)
	w("\tldr x5, [x4]")
	w("\tadd x5, x5, #15")
	w("\tand x5, x5, #-16") // base (16-aligned)
	w("\tlsl x6, x2, #3")   // argc*8
	w("\tadd x7, x6, #16")  // allocSize = argc*8 + 16-byte header
	w("\tadd x8, x5, x7")
	w("\tstr x8, [x4]") // bump
	emitHeapGuardCall(w)
	w("\tadd x9, x5, #16")     // x9 = container data (entries past the header)
	w("\tstur w2, [x9, #-12]") // cap = argc
	w("\tmov w6, #1")
	w("\tstur w6, [x9, #-8]") // rc = 1
	w("\tstur w2, [x9, #-4]") // len = argc
	w("\tmov x10, #0")        // i
	w(".Lssa_args_loop:")
	w("\tcmp x10, x2")
	w("\tb.hs .Lssa_args_done")
	w("\tldr x11, [x3, x10, lsl #3]") // x11 = argv[i] (NUL-terminated C string)
	// strlen(x11) -> x12.
	w("\tmov x12, #0")
	w(".Lssa_args_slen:")
	w("\tldrb w13, [x11, x12]")
	w("\tcbz w13, .Lssa_args_slen_done")
	w("\tadd x12, x12, #1")
	w("\tb .Lssa_args_slen")
	w(".Lssa_args_slen_done:")
	// Allocate a single-word string: 8-byte header + len bytes + 1 NUL.
	w("\tldr x5, [x4]") // reload cursor
	w("\tadd x5, x5, #15")
	w("\tand x5, x5, #-16")
	w("\tadd x6, x12, #9") // header(8) + len + NUL(1)
	w("\tadd x7, x5, x6")
	w("\tstr x7, [x4]") // bump
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x5]")      // rc = 1
	w("\tstr w12, [x5, #4]") // len
	w("\tadd x14, x5, #8")   // x14 = string data
	// Copy the len bytes, then the NUL.
	w("\tmov x15, #0")
	w(".Lssa_args_cp:")
	w("\tcmp x15, x12")
	w("\tb.hs .Lssa_args_cp_done")
	w("\tldrb w16, [x11, x15]")
	w("\tstrb w16, [x14, x15]")
	w("\tadd x15, x15, #1")
	w("\tb .Lssa_args_cp")
	w(".Lssa_args_cp_done:")
	w("\tstrb wzr, [x14, x12]")       // trailing NUL
	w("\tstr x14, [x9, x10, lsl #3]") // container[i] = string ptr
	w("\tadd x10, x10, #1")
	w("\tb .Lssa_args_loop")
	w(".Lssa_args_done:")
	w("\tstr x9, [x1]") // memoise
	w("\tmov x0, x9")
	w(".Lssa_args_ret:")
	w("\tret")
}

// emitEnvHelper writes env(name) -> Option[string]: walk the captured envp for a
// "NAME=VALUE" entry and return a heap Option box {tag@0 (i32), value-ptr@8}. On
// a match the value (after '=') is copied into a fresh single-word rc string and
// the box tag is 0 (Some); otherwise tag is 1 (None) and the payload slot is 0.
// The box carries the standard rc header (rc=1 at [box-8]) so its scope-exit drop
// finds a valid count. Single-return (box pointer in x0); env is a builtin, not a
// user pair-form function, so its result is the box the match reads at [box+0] /
// [box+8], matching the native backend. Everything is bump-allocated inline, so
// it is a leaf. x0=name (single-word string; length at [name-4]).
func emitEnvHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("env"))
	w("\tldur w2, [x0, #-4]") // name_len (zero-extends into x2)
	w("\tmov x3, x0")         // name ptr
	w("\tadrp x4, %s", envpSym)
	w("\tadd x4, x4, #:lo12:%s", envpSym)
	w("\tldr x4, [x4]") // x4 = envp
	w(".Lssa_env_loop:")
	w("\tldr x5, [x4]") // x5 = envp[i]
	w("\tcbz x5, .Lssa_env_none")
	// Compare the first name_len bytes of envp[i] with name.
	w("\tmov w6, #0")
	w(".Lssa_env_cmp:")
	w("\tcmp w6, w2")
	w("\tb.hs .Lssa_env_eq") // matched all name_len bytes
	w("\tldrb w7, [x5, x6]")
	w("\tldrb w8, [x3, x6]")
	w("\tcmp w7, w8")
	w("\tb.ne .Lssa_env_next")
	w("\tadd w6, w6, #1")
	w("\tb .Lssa_env_cmp")
	w(".Lssa_env_eq:")
	w("\tldrb w7, [x5, x2]") // the byte after the name must be '='
	w("\tcmp w7, #61")
	w("\tb.ne .Lssa_env_next")
	// Found: value is the NUL-terminated string at envp[i] + name_len + 1.
	w("\tadd x9, x5, x2")
	w("\tadd x9, x9, #1") // x9 = value src
	w("\tmov x10, #0")    // value len
	w(".Lssa_env_slen:")
	w("\tldrb w11, [x9, x10]")
	w("\tcbz w11, .Lssa_env_slen_done")
	w("\tadd x10, x10, #1")
	w("\tb .Lssa_env_slen")
	w(".Lssa_env_slen_done:")
	// Allocate a single-word rc string of the value: rc=1@base, len@base+4, data@base+8, +1 NUL.
	w("\tadrp x12, %s", heapPtrSym)
	w("\tadd x12, x12, #:lo12:%s", heapPtrSym) // x12 = &cursor (held across both allocs)
	w("\tldr x13, [x12]")
	w("\tadd x13, x13, #15")
	w("\tand x13, x13, #-16") // base
	w("\tadd x14, x10, #9")   // header(8) + len + NUL(1)
	w("\tadd x15, x13, x14")
	w("\tstr x15, [x12]") // bump
	emitHeapGuardCall(w)
	w("\tmov w14, #1")
	w("\tstr w14, [x13]")     // rc = 1
	w("\tstr w10, [x13, #4]") // len
	w("\tadd x16, x13, #8")   // x16 = value data ptr
	w("\tmov w6, #0")         // copy i (reuse w6)
	w(".Lssa_env_cp:")
	w("\tcmp w6, w10")
	w("\tb.hs .Lssa_env_cpdone")
	w("\tldrb w7, [x9, x6]")
	w("\tstrb w7, [x16, x6]")
	w("\tadd w6, w6, #1")
	w("\tb .Lssa_env_cp")
	w(".Lssa_env_cpdone:")
	w("\tstrb wzr, [x16, x10]") // NUL
	// Allocate the Option box (rc=1@base, tag@base+8, value-ptr@base+16); return base+8.
	w("\tldr x13, [x12]")
	w("\tadd x13, x13, #15")
	w("\tand x13, x13, #-16")
	w("\tadd x15, x13, #24") // 8 header + 16 box (tag + pad + ptr)
	w("\tstr x15, [x12]")    // bump
	emitHeapGuardCall(w)
	w("\tmov w14, #1")
	w("\tstr w14, [x13]")    // rc = 1
	w("\tadd x0, x13, #8")   // x0 = box data ptr (return)
	w("\tstr wzr, [x0]")     // tag = 0 (Some)
	w("\tstr x16, [x0, #8]") // value string ptr
	w("\tret")
	w(".Lssa_env_next:")
	w("\tadd x4, x4, #8")
	w("\tb .Lssa_env_loop")
	w(".Lssa_env_none:")
	// None: box {rc=1, tag=1, ptr=0}.
	w("\tadrp x12, %s", heapPtrSym)
	w("\tadd x12, x12, #:lo12:%s", heapPtrSym)
	w("\tldr x13, [x12]")
	w("\tadd x13, x13, #15")
	w("\tand x13, x13, #-16")
	w("\tadd x15, x13, #24")
	w("\tstr x15, [x12]")
	emitHeapGuardCall(w)
	w("\tmov w14, #1")
	w("\tstr w14, [x13]")  // rc = 1
	w("\tadd x0, x13, #8") // box data ptr
	w("\tmov w14, #1")
	w("\tstr w14, [x0]")     // tag = 1 (None)
	w("\tstr xzr, [x0, #8]") // payload = 0
	w("\tret")
}

// emitIoErrorHelper writes __fern_io_error(errno, path) -> IoError box: map a
// (positive) errno to the matching IoError variant and build its heap box,
// matching the native layout so a Match on the value reads the right tag/payload.
// Payloaded path variants — NotFound(0)/PermissionDenied(1)/AlreadyExists(2)/
// InvalidUtf8(3) — are {tag@0, path@8}; Interrupted(4) is tag-only; the Other(6) fallback carries
// {tag@0, path@8, msg@16} with an empty msg string built on the heap. Every box
// carries the standard rc header (rc=1 at [box-8]). Leaf; x0=errno, x1=path.
func emitIoErrorHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_io_error"))
	w("\tcmp w0, #2") // ENOENT
	w("\tb.eq .Lssa_ioe_nf")
	w("\tcmp w0, #13") // EACCES
	w("\tb.eq .Lssa_ioe_pm")
	w("\tcmp w0, #17") // EEXIST
	w("\tb.eq .Lssa_ioe_ex")
	w("\tcmp w0, #4") // EINTR
	w("\tb.eq .Lssa_ioe_intr")
	// EILSEQ is synthetic — read_file's UTF-8 validation dispatches
	// it; no file syscall produces it (#5714).
	w("\tcmp w0, #84") // EILSEQ
	w("\tb.eq .Lssa_ioe_il")
	// Other(path, ""): build the empty msg string, then a 3-slot box.
	w("\tadrp x2, %s", heapPtrSym)
	w("\tadd x2, x2, #:lo12:%s", heapPtrSym) // x2 = &cursor
	w("\tldr x3, [x2]")
	w("\tadd x3, x3, #15")
	w("\tand x3, x3, #-16") // base_msg
	w("\tadd x4, x3, #9")   // 8 header + 0 len + 1 NUL
	w("\tstr x4, [x2]")
	emitHeapGuardCall(w)
	w("\tmov w5, #1")
	w("\tstr w5, [x3]")      // rc = 1
	w("\tstr wzr, [x3, #4]") // len = 0
	w("\tadd x6, x3, #8")    // x6 = empty msg ptr
	w("\tstrb wzr, [x6]")    // NUL
	w("\tldr x3, [x2]")      // box alloc
	w("\tadd x3, x3, #15")
	w("\tand x3, x3, #-16")
	w("\tadd x4, x3, #32") // 8 header + 24 box (tag + path + msg)
	w("\tstr x4, [x2]")
	emitHeapGuardCall(w)
	w("\tmov w5, #1")
	w("\tstr w5, [x3]")   // rc = 1
	w("\tadd x0, x3, #8") // box data ptr (return)
	w("\tmov w5, #6")
	w("\tstr w5, [x0]")      // tag = 6 (Other)
	w("\tstr x1, [x0, #8]")  // path
	w("\tstr x6, [x0, #16]") // msg = ""
	w("\tret")
	w(".Lssa_ioe_intr:")
	w("\tadrp x2, %s", heapPtrSym)
	w("\tadd x2, x2, #:lo12:%s", heapPtrSym)
	w("\tldr x3, [x2]")
	w("\tadd x3, x3, #15")
	w("\tand x3, x3, #-16")
	w("\tadd x4, x3, #16") // 8 header + 8 (tag only)
	w("\tstr x4, [x2]")
	emitHeapGuardCall(w)
	w("\tmov w5, #1")
	w("\tstr w5, [x3]")
	w("\tadd x0, x3, #8")
	w("\tmov w5, #4")
	w("\tstr w5, [x0]") // tag = 4 (Interrupted)
	w("\tret")
	w(".Lssa_ioe_nf:")
	w("\tmov w7, #0")
	w("\tb .Lssa_ioe_path")
	w(".Lssa_ioe_pm:")
	w("\tmov w7, #1")
	w("\tb .Lssa_ioe_path")
	w(".Lssa_ioe_ex:")
	w("\tmov w7, #2")
	w("\tb .Lssa_ioe_path")
	w(".Lssa_ioe_il:")
	w("\tmov w7, #3")
	w(".Lssa_ioe_path:")
	w("\tadrp x2, %s", heapPtrSym)
	w("\tadd x2, x2, #:lo12:%s", heapPtrSym)
	w("\tldr x3, [x2]")
	w("\tadd x3, x3, #15")
	w("\tand x3, x3, #-16")
	w("\tadd x4, x3, #24") // 8 header + 16 (tag + path)
	w("\tstr x4, [x2]")
	emitHeapGuardCall(w)
	w("\tmov w5, #1")
	w("\tstr w5, [x3]")
	w("\tadd x0, x3, #8")
	w("\tstr w7, [x0]")     // tag
	w("\tstr x1, [x0, #8]") // path
	w("\tret")
}

// emitWriteFileHelper writes write_file(path, content) -> Option[IoError]:
// truncate-create the file and write the content. Returns None (tag 1) on
// success and Some(IoError) (tag 0, box@8) on failure. The path is NUL-terminated
// into a fresh heap buffer (Fern strings aren't NUL-terminated) before openat;
// the content bytes come straight from the single-word string. On a negative
// openat result the errno (-fd) is mapped by __fern_io_error. Non-leaf (calls
// __fern_io_error), so it keeps a frame with callee-saved x19=path / x20=content
// / x21=path_nul / x22=fd across the syscalls and the call. x0=path, x1=content.
func emitWriteFileHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("write_file"))
	w("\tstp x29, x30, [sp, #-48]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tstp x21, x22, [sp, #32]")
	w("\tmov x19, x0") // path
	w("\tmov x20, x1") // content
	// NUL-terminate the path into a fresh heap buffer (x21).
	w("\tldur w2, [x19, #-4]") // path len
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16") // base
	w("\tadd x5, x2, #1")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]") // bump
	emitHeapGuardCall(w)
	w("\tmov w7, #0")
	w(".Lssa_wf_cp:")
	w("\tcmp w7, w2")
	w("\tb.hs .Lssa_wf_cpd")
	w("\tldrb w8, [x19, x7]")
	w("\tstrb w8, [x4, x7]")
	w("\tadd w7, w7, #1")
	w("\tb .Lssa_wf_cp")
	w(".Lssa_wf_cpd:")
	w("\tstrb wzr, [x4, x2]") // NUL
	w("\tmov x21, x4")        // path_nul
	// openat(AT_FDCWD, path_nul, O_WRONLY|O_CREAT|O_TRUNC, 0644).
	w("\tmov x0, #100")
	w("\tneg x0, x0") // AT_FDCWD = -100
	w("\tmov x1, x21")
	w("\tmov x2, #577") // 0x241
	w("\tmov x3, #420") // 0644
	w("\tmov x8, #56")  // openat
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_wf_err") // fd < 0 → error
	w("\tmov x22, x0")                // fd
	// write(fd, content_data, content_len).
	w("\tmov x0, x22")
	w("\tmov x1, x20")
	w("\tldur w2, [x20, #-4]")
	w("\tmov x8, #64") // write
	w("\tsvc #0")
	// close(fd).
	w("\tmov x0, x22")
	w("\tmov x8, #57") // close
	w("\tsvc #0")
	// return None box {rc=1, tag=1}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")      // rc = 1
	w("\tadd x0, x4, #8")    // box data
	w("\tstr wzr, [x0]")     // tag = 0 (Ok)
	w("\tstr xzr, [x0, #8]") // unit payload
	w("\tb .Lssa_wf_ret")
	w(".Lssa_wf_err:")
	w("\tneg x0, x0") // errno = -fd
	w("\tmov x1, x19")
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x22, x0") // IoError box
	// return Some(IoError) box {rc=1, tag=0, ioerr@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	w("\tstr w6, [x0]")   // tag = 1 (Err) — w6 still holds 1 from rc
	w("\tstr x22, [x0, #8]")
	w(".Lssa_wf_ret:")
	w("\tldp x21, x22, [sp, #32]")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #48")
	w("\tret")
}

// emitReadFileHelper writes read_file(path) -> Result[string, IoError]: open the
// file read-only, fstat it for the size, read the whole thing into a fresh
// single-word rc string, UTF-8-validate it (D9, #5714 — invalid content maps to
// Err(InvalidUtf8(path)) via a synthetic EILSEQ), and return Ok(string) (tag 0,
// string@8). Any syscall failure maps -errno through __fern_io_error and returns
// Err(IoError) (tag 1, box@8). The path is NUL-terminated into a heap buffer
// first. Non-leaf (calls __fern_io_error / __fern_utf8_valid); frame carries a
// 192-byte statbuf scratch plus callee-saved
// x19=path / x20=fd / x21=data-or-errno / x22=size / x23=bytes_read / x24=path_nul.
// x0=path.
func emitReadFileHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("read_file"))
	w("\tstp x29, x30, [sp, #-256]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tstp x21, x22, [sp, #32]")
	w("\tstp x23, x24, [sp, #48]")
	w("\tmov x19, x0") // path
	// NUL-terminate the path into a heap buffer (x24).
	w("\tldur w2, [x19, #-4]")
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x2, #1")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w7, #0")
	w(".Lssa_rf_cp:")
	w("\tcmp w7, w2")
	w("\tb.hs .Lssa_rf_cpd")
	w("\tldrb w8, [x19, x7]")
	w("\tstrb w8, [x4, x7]")
	w("\tadd w7, w7, #1")
	w("\tb .Lssa_rf_cp")
	w(".Lssa_rf_cpd:")
	w("\tstrb wzr, [x4, x2]")
	w("\tmov x24, x4") // path_nul
	// openat(AT_FDCWD, path_nul, O_RDONLY, 0).
	w("\tmov x0, #100")
	w("\tneg x0, x0")
	w("\tmov x1, x24")
	w("\tmov x2, #0") // O_RDONLY
	w("\tmov x3, #0")
	w("\tmov x8, #56") // openat
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_rf_err_open")
	w("\tmov x20, x0") // fd
	// fstat(fd, statbuf@sp+64); st_size at statbuf+48 → [sp+112].
	w("\tmov x0, x20")
	w("\tadd x1, sp, #64")
	w("\tmov x8, #80") // fstat
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_rf_err_close")
	w("\tldr x22, [sp, #112]") // st_size
	// Allocate a single-word rc string of size bytes (+ NUL).
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x22, #9") // 8 header + size + 1 NUL
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w7, #1")
	w("\tstr w7, [x4]")      // rc = 1
	w("\tstr w22, [x4, #4]") // len = size
	w("\tadd x21, x4, #8")   // x21 = string data ptr
	// Read loop: x23 = cumulative bytes read.
	w("\tmov x23, #0")
	w(".Lssa_rf_loop:")
	w("\tcmp x23, x22")
	w("\tb.ge .Lssa_rf_done")
	w("\tmov x0, x20")
	w("\tadd x1, x21, x23")
	w("\tsub x2, x22, x23")
	w("\tmov x8, #63") // read
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_rf_err_close")
	w("\tcbz x0, .Lssa_rf_done") // EOF
	w("\tadd x23, x23, x0")
	w("\tb .Lssa_rf_loop")
	w(".Lssa_rf_done:")
	w("\tstrb wzr, [x21, x22]") // trailing NUL
	w("\tmov x0, x20")
	w("\tmov x8, #57") // close
	w("\tsvc #0")
	// Zero the shrink tail [x21+x23, x21+x22): a file that shrank
	// between fstat and read would otherwise leave heap slack there,
	// making the validation below nondeterministic. NUL bytes are
	// valid UTF-8.
	w("\tsubs x2, x22, x23")
	w("\tb.le .Lssa_rf_val")
	w("\tadd x1, x21, x23")
	w(".Lssa_rf_zfill:")
	w("\tstrb wzr, [x1], #1")
	w("\tsubs x2, x2, #1")
	w("\tb.gt .Lssa_rf_zfill")
	w(".Lssa_rf_val:")
	// D9 (#5714): the text read validates at the boundary; invalid
	// content dispatches as Err(InvalidUtf8(path)) via the synthetic
	// EILSEQ errno. Raw reads go through read_file_bytes.
	w("\tmov x0, x21")
	w("\tmov x1, x22")
	w("\tbl %s", fnLabel("__fern_utf8_valid"))
	w("\tcbnz w0, .Lssa_rf_okb")
	w("\tmov x21, #84") // EILSEQ
	w("\tb .Lssa_rf_err_dispatch")
	w(".Lssa_rf_okb:")
	// Result.Ok(string): box {rc=1, tag=0, string@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	w("\tstr wzr, [x0]")  // tag = 0 (Ok)
	w("\tstr x21, [x0, #8]")
	w("\tb .Lssa_rf_ret")
	w(".Lssa_rf_err_close:")
	w("\tneg x21, x0") // errno (x21 no longer needed as data)
	w("\tmov x0, x20")
	w("\tmov x8, #57") // close
	w("\tsvc #0")
	w("\tb .Lssa_rf_err_dispatch")
	w(".Lssa_rf_err_open:")
	w("\tneg x21, x0") // errno
	w(".Lssa_rf_err_dispatch:")
	w("\tmov x0, x21") // errno
	w("\tmov x1, x19") // path
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x19, x0") // IoError box (path no longer needed)
	// Result.Err(IoError): box {rc=1, tag=1, ioerr@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	w("\tmov w6, #1")
	w("\tstr w6, [x0]") // tag = 1 (Err)
	w("\tstr x19, [x0, #8]")
	w(".Lssa_rf_ret:")
	w("\tldp x23, x24, [sp, #48]")
	w("\tldp x21, x22, [sp, #32]")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #256")
	w("\tret")
}

// emitReadFileBytesHelper writes read_file_bytes(path) -> Result[u8[], IoError]:
// read_file's raw sibling — the same openat/fstat/read-loop/close pipeline, but
// the contents land in a fresh u8[] from __alloc_u8 (16-byte header; cap@-12,
// rc@-8, len@-4) and Ok carries the array data pointer. A file that shrinks
// between fstat and read leaves the trailing bytes zero (__alloc_u8 zero-fills)
// with len still st_size. Any syscall failure maps -errno through
// __fern_io_error and returns Err(IoError) (tag 1, box@8). The path is
// NUL-terminated into a heap buffer first. Non-leaf (calls __alloc_u8 /
// __fern_io_error); frame carries a 192-byte statbuf scratch plus callee-saved
// x19=path / x20=fd / x21=data-or-errno / x22=size / x23=bytes_read /
// x24=path_nul. x0=path.
func emitReadFileBytesHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("read_file_bytes"))
	w("\tstp x29, x30, [sp, #-256]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tstp x21, x22, [sp, #32]")
	w("\tstp x23, x24, [sp, #48]")
	w("\tmov x19, x0") // path
	// NUL-terminate the path into a heap buffer (x24).
	w("\tldur w2, [x19, #-4]")
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x2, #1")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w7, #0")
	w(".Lssa_rfb_cp:")
	w("\tcmp w7, w2")
	w("\tb.hs .Lssa_rfb_cpd")
	w("\tldrb w8, [x19, x7]")
	w("\tstrb w8, [x4, x7]")
	w("\tadd w7, w7, #1")
	w("\tb .Lssa_rfb_cp")
	w(".Lssa_rfb_cpd:")
	w("\tstrb wzr, [x4, x2]")
	w("\tmov x24, x4") // path_nul
	// openat(AT_FDCWD, path_nul, O_RDONLY, 0).
	w("\tmov x0, #100")
	w("\tneg x0, x0")
	w("\tmov x1, x24")
	w("\tmov x2, #0") // O_RDONLY
	w("\tmov x3, #0")
	w("\tmov x8, #56") // openat
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_rfb_err_open")
	w("\tmov x20, x0") // fd
	// fstat(fd, statbuf@sp+64); st_size at statbuf+48 → [sp+112].
	w("\tmov x0, x20")
	w("\tadd x1, sp, #64")
	w("\tmov x8, #80") // fstat
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_rfb_err_close")
	w("\tldr x22, [sp, #112]") // st_size
	// Fresh u8[] of size bytes; __alloc_u8 owns the header layout
	// and the zero-fill.
	w("\tmov x0, x22")
	w("\tbl %s", fnLabel("__alloc_u8"))
	w("\tmov x21, x0") // x21 = array data ptr
	// Read loop: x23 = cumulative bytes read.
	w("\tmov x23, #0")
	w(".Lssa_rfb_loop:")
	w("\tcmp x23, x22")
	w("\tb.ge .Lssa_rfb_done")
	w("\tmov x0, x20")
	w("\tadd x1, x21, x23")
	w("\tsub x2, x22, x23")
	w("\tmov x8, #63") // read
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_rfb_err_close")
	w("\tcbz x0, .Lssa_rfb_done") // EOF
	w("\tadd x23, x23, x0")
	w("\tb .Lssa_rfb_loop")
	w(".Lssa_rfb_done:")
	w("\tmov x0, x20")
	w("\tmov x8, #57") // close
	w("\tsvc #0")
	// Result.Ok(u8[]): box {rc=1, tag=0, data@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	w("\tstr wzr, [x0]")  // tag = 0 (Ok)
	w("\tstr x21, [x0, #8]")
	w("\tb .Lssa_rfb_ret")
	w(".Lssa_rfb_err_close:")
	w("\tneg x21, x0") // errno (x21 no longer needed as data)
	w("\tmov x0, x20")
	w("\tmov x8, #57") // close
	w("\tsvc #0")
	w("\tb .Lssa_rfb_err_dispatch")
	w(".Lssa_rfb_err_open:")
	w("\tneg x21, x0") // errno
	w(".Lssa_rfb_err_dispatch:")
	w("\tmov x0, x21") // errno
	w("\tmov x1, x19") // path
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x19, x0") // IoError box (path no longer needed)
	// Result.Err(IoError): box {rc=1, tag=1, ioerr@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	w("\tmov w6, #1")
	w("\tstr w6, [x0]") // tag = 1 (Err)
	w("\tstr x19, [x0, #8]")
	w(".Lssa_rfb_ret:")
	w("\tldp x23, x24, [sp, #48]")
	w("\tldp x21, x22, [sp, #32]")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #256")
	w("\tret")
}

// emitRemoveFileHelper writes remove_file(path) -> Option[IoError]: unlink the
// file at `path`. Returns None (tag 1) on success and Some(IoError) (tag 0,
// box@8) on failure (mirroring os.Remove, a missing target is an ENOENT error,
// not a silent success). The path is NUL-terminated into a fresh heap buffer
// first, then unlinkat(AT_FDCWD, path_nul, 0). A negative result maps -errno
// through __fern_io_error. Non-leaf (calls __fern_io_error), so it keeps a frame
// with callee-saved x19=path / x20=path_nul across the syscall and the call.
// x0=path.
func emitRemoveFileHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("remove_file"))
	w("\tstp x29, x30, [sp, #-32]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tmov x19, x0") // path
	// NUL-terminate the path into a fresh heap buffer (x20).
	w("\tldur w2, [x19, #-4]") // path len
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16") // base
	w("\tadd x5, x2, #1")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]") // bump
	emitHeapGuardCall(w)
	w("\tmov w7, #0")
	w(".Lssa_rmf_cp:")
	w("\tcmp w7, w2")
	w("\tb.hs .Lssa_rmf_cpd")
	w("\tldrb w8, [x19, x7]")
	w("\tstrb w8, [x4, x7]")
	w("\tadd w7, w7, #1")
	w("\tb .Lssa_rmf_cp")
	w(".Lssa_rmf_cpd:")
	w("\tstrb wzr, [x4, x2]") // NUL
	w("\tmov x20, x4")        // path_nul
	// unlinkat(AT_FDCWD, path_nul, 0).
	w("\tmov x0, #100")
	w("\tneg x0, x0") // AT_FDCWD = -100
	w("\tmov x1, x20")
	w("\tmov x2, #0")  // flags
	w("\tmov x8, #35") // unlinkat
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_rmf_err") // < 0 → error
	// return None box {rc=1, tag=1}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")      // rc = 1
	w("\tadd x0, x4, #8")    // box data
	w("\tstr wzr, [x0]")     // tag = 0 (Ok)
	w("\tstr xzr, [x0, #8]") // unit payload
	w("\tb .Lssa_rmf_ret")
	w(".Lssa_rmf_err:")
	w("\tneg x0, x0") // errno = -ret
	w("\tmov x1, x19")
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x20, x0") // IoError box
	// return Some(IoError) box {rc=1, tag=0, ioerr@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	w("\tstr w6, [x0]")   // tag = 1 (Err) — w6 still holds 1 from rc
	w("\tstr x20, [x0, #8]")
	w(".Lssa_rmf_ret:")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #32")
	w("\tret")
}

// emitCreateDirAllHelper writes create_dir_all(path) -> Result[void, IoError]:
// mkdirat(AT_FDCWD, prefix, 0777) for every missing component of `path`, POSIX
// `mkdir -p`. The path is NUL-terminated into a fresh heap buffer, then walked:
// at each '/' the separator becomes a NUL, mkdirat runs for the prefix, and the
// '/' goes back. Intermediate results are discarded — a parent that could not be
// created makes the final mkdirat fail with the same errno — and EEXIST on the
// leaf is success, so an existing path is Ok. Non-leaf (calls __fern_io_error),
// keeping callee-saved x19=path / x20=path_nul / x21=len / x22=cursor across the
// syscalls and the call. x0=path.
func emitCreateDirAllHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("create_dir_all"))
	w("\tstp x29, x30, [sp, #-48]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tstp x21, x22, [sp, #32]")
	w("\tmov x19, x0") // path
	// NUL-terminate the path into a fresh heap buffer (x20).
	w("\tldur w21, [x19, #-4]") // path len
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16") // base
	w("\tadd x5, x21, #1")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]") // bump
	emitHeapGuardCall(w)
	w("\tmov w7, #0")
	w(".Lssa_cda_cp:")
	w("\tcmp w7, w21")
	w("\tb.hs .Lssa_cda_cpd")
	w("\tldrb w8, [x19, x7]")
	w("\tstrb w8, [x4, x7]")
	w("\tadd w7, w7, #1")
	w("\tb .Lssa_cda_cp")
	w(".Lssa_cda_cpd:")
	w("\tstrb wzr, [x4, x21]") // NUL
	w("\tmov x20, x4")         // path_nul
	// Parents: every '/' at index 1..len-1 not itself preceded by one. Index 0
	// is skipped so a leading '/' does not ask for the empty path.
	w("\tmov x22, #1")
	w(".Lssa_cda_lp:")
	w("\tcmp x22, x21")
	w("\tb.hs .Lssa_cda_lpd")
	w("\tldrb w9, [x20, x22]")
	w("\tcmp w9, #47") // '/'
	w("\tb.ne .Lssa_cda_nx")
	w("\tsub x10, x22, #1")
	w("\tldrb w9, [x20, x10]")
	w("\tcmp w9, #47")
	w("\tb.eq .Lssa_cda_nx")
	w("\tstrb wzr, [x20, x22]")
	w("\tmov x0, #100")
	w("\tneg x0, x0") // AT_FDCWD = -100
	w("\tmov x1, x20")
	w("\tmov x2, #511") // 0777
	w("\tmov x8, #34")  // mkdirat
	w("\tsvc #0")
	w("\tmov w9, #47")
	w("\tstrb w9, [x20, x22]")
	w(".Lssa_cda_nx:")
	w("\tadd x22, x22, #1")
	w("\tb .Lssa_cda_lp")
	w(".Lssa_cda_lpd:")
	// The leaf decides the result.
	w("\tmov x0, #100")
	w("\tneg x0, x0")
	w("\tmov x1, x20")
	w("\tmov x2, #511")
	w("\tmov x8, #34")
	w("\tsvc #0")
	w("\tcmp x0, #0")
	w("\tb.eq .Lssa_cda_ok")
	w("\tcmn x0, #17") // -EEXIST is success
	w("\tb.ne .Lssa_cda_err")
	w(".Lssa_cda_ok:")
	// return Ok(()) box {rc=1, tag=0, unit@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")      // rc = 1
	w("\tadd x0, x4, #8")    // box data
	w("\tstr wzr, [x0]")     // tag = 0 (Ok)
	w("\tstr xzr, [x0, #8]") // unit payload
	w("\tb .Lssa_cda_ret")
	w(".Lssa_cda_err:")
	w("\tneg x0, x0") // errno = -ret
	w("\tmov x1, x19")
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x20, x0") // IoError box
	// return Err(IoError) box {rc=1, tag=1, ioerr@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	w("\tstr w6, [x0]")   // tag = 1 (Err) — w6 still holds 1 from rc
	w("\tstr x20, [x0, #8]")
	w(".Lssa_cda_ret:")
	w("\tldp x21, x22, [sp, #32]")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #48")
	w("\tret")
}

// emitRemoveDirAllHelper writes remove_dir_all(path) -> Option[IoError]: a
// recursive rm -rf, ported from the self-hosted asm_arm64.fern. It opens the path
// O_DIRECTORY; a directory is drained (getdents64) and each non-"."/".." child is
// recursed into (a child that is a plain file hits ENOTDIR and is unlinked), then
// the now-empty directory is rmdir'd via unlinkat(AT_REMOVEDIR); a plain-file path
// (ENOTDIR at the top) is unlinked; a missing path (ENOENT) is a silent success
// (matching os.RemoveAll). Returns None on success, or Some(IoError) for a
// top-level open error other than ENOENT/ENOTDIR. Child errors are best-effort
// (not propagated), mirroring the self-host. Non-leaf + self-recursive; callee-
// saved x19=pathz / x20=fd / x21=buf / x22=total / x23=offset / x24=name-or-errno.
//
// NOTE: each recursion level bump-allocates a 1 KiB getdents buffer the heap
// never reclaims, so remove_dir_all is bounded to directories whose entries fit
// in 1 KiB per level — sufficient for the CLI use case. The native backend uses
// a 64 KiB buffer.
func emitRemoveDirAllHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("remove_dir_all"))
	w("\tstp x29, x30, [sp, #-16]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #-16]!")
	w("\tstp x21, x22, [sp, #-16]!")
	w("\tstp x23, x24, [sp, #-16]!")
	w("\tmov x20, x0")         // path data (single-word string)
	w("\tldur w21, [x0, #-4]") // path len
	// NUL-terminate the path into a heap buffer (x19 = pathz).
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x21, #1")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov x19, x4")
	w("\tmov x9, #0")
	w(".Lssa_rda_cp:")
	w("\tcmp x9, x21")
	w("\tb.hs .Lssa_rda_cpd")
	w("\tldrb w10, [x20, x9]")
	w("\tstrb w10, [x19, x9]")
	w("\tadd x9, x9, #1")
	w("\tb .Lssa_rda_cp")
	w(".Lssa_rda_cpd:")
	w("\tstrb wzr, [x19, x21]")
	// openat(AT_FDCWD, pathz, O_RDONLY|O_DIRECTORY=16384, 0).
	w("\tmov x0, #100")
	w("\tneg x0, x0")
	w("\tmov x1, x19")
	w("\tmov x2, #16384")
	w("\tmov x3, #0")
	w("\tmov x8, #56") // openat
	w("\tsvc #0")
	w("\tcmp x0, #0")
	w("\tb.ge .Lssa_rda_dir")
	w("\tcmn x0, #2") // -ENOENT → already gone (None)
	w("\tb.eq .Lssa_rda_none")
	w("\tcmn x0, #20") // -ENOTDIR → it's a file
	w("\tb.ne .Lssa_rda_some")
	// unlinkat(AT_FDCWD, pathz, 0) — remove the file.
	w("\tmov x0, #100")
	w("\tneg x0, x0")
	w("\tmov x1, x19")
	w("\tmov x2, #0")
	w("\tmov x8, #35") // unlinkat
	w("\tsvc #0")
	w("\tb .Lssa_rda_none")
	w(".Lssa_rda_dir:")
	w("\tmov x20, x0") // dir fd
	// Allocate a 1 KiB dirent buffer (x21) and drain the directory into it.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x6, x4, #1024")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov x21, x4")
	w("\tmov x22, #0") // total
	w(".Lssa_rda_g:")
	w("\tmov x2, #1024")
	w("\tsub x2, x2, x22")
	w("\tcbz x2, .Lssa_rda_gd") // buffer full → stop (small-tree cap)
	w("\tmov x0, x20")
	w("\tadd x1, x21, x22")
	w("\tmov x8, #61") // getdents64
	w("\tsvc #0")
	w("\tcmp x0, #0")
	w("\tble .Lssa_rda_gd")
	w("\tadd x22, x22, x0")
	w("\tb .Lssa_rda_g")
	w(".Lssa_rda_gd:")
	w("\tmov x23, #0") // offset
	w(".Lssa_rda_it:")
	w("\tcmp x23, x22")
	w("\tb.ge .Lssa_rda_itd")
	w("\tadd x10, x21, x23")
	w("\tadd x10, x10, #19") // d_name ptr
	w("\tldrb w11, [x10]")
	w("\tcmp w11, #46")
	w("\tb.ne .Lssa_rda_ch")
	w("\tldrb w11, [x10, #1]")
	w("\tcbz w11, .Lssa_rda_adv") // "."
	w("\tcmp w11, #46")
	w("\tb.ne .Lssa_rda_ch")
	w("\tldrb w11, [x10, #2]")
	w("\tcbz w11, .Lssa_rda_adv") // ".."
	w(".Lssa_rda_ch:")
	w("\tmov x24, x10") // name ptr (callee-saved — survives the recursion)
	// plen = strlen(pathz).
	w("\tmov x9, #0")
	w(".Lssa_rda_pl:")
	w("\tldrb w11, [x19, x9]")
	w("\tcbz w11, .Lssa_rda_pld")
	w("\tadd x9, x9, #1")
	w("\tb .Lssa_rda_pl")
	w(".Lssa_rda_pld:")
	// nlen = strlen(name).
	w("\tmov x13, #0")
	w(".Lssa_rda_nl:")
	w("\tldrb w11, [x24, x13]")
	w("\tcbz w11, .Lssa_rda_nld")
	w("\tadd x13, x13, #1")
	w("\tb .Lssa_rda_nl")
	w(".Lssa_rda_nld:")
	// Build the child single-word rc string "pathz/name" (len = plen+1+nlen).
	w("\tadd x14, x9, x13")
	w("\tadd x14, x14, #1") // childlen
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x14, #9")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w7, #1")
	w("\tstr w7, [x4]")      // rc = 1
	w("\tstr w14, [x4, #4]") // len = childlen
	w("\tadd x12, x4, #8")   // child data ptr
	// copy pathz[0..plen]
	w("\tmov x15, #0")
	w(".Lssa_rda_c1:")
	w("\tcmp x15, x9")
	w("\tb.hs .Lssa_rda_c1d")
	w("\tldrb w11, [x19, x15]")
	w("\tstrb w11, [x12, x15]")
	w("\tadd x15, x15, #1")
	w("\tb .Lssa_rda_c1")
	w(".Lssa_rda_c1d:")
	w("\tmov w11, #47") // '/'
	w("\tstrb w11, [x12, x9]")
	// copy name at plen+1
	w("\tmov x15, #0")
	w(".Lssa_rda_c2:")
	w("\tcmp x15, x13")
	w("\tb.hs .Lssa_rda_c2d")
	w("\tldrb w11, [x24, x15]")
	w("\tadd x16, x9, #1")
	w("\tadd x16, x16, x15")
	w("\tstrb w11, [x12, x16]")
	w("\tadd x15, x15, #1")
	w("\tb .Lssa_rda_c2")
	w(".Lssa_rda_c2d:")
	w("\tstrb wzr, [x12, x14]") // NUL at childlen
	// recurse: remove_dir_all(child).
	w("\tmov x0, x12")
	w("\tbl %s", fnLabel("remove_dir_all"))
	w(".Lssa_rda_adv:")
	w("\tadd x12, x21, x23")
	w("\tldrh w11, [x12, #16]") // d_reclen
	w("\tadd x23, x23, x11")
	w("\tb .Lssa_rda_it")
	w(".Lssa_rda_itd:")
	// close(fd), then rmdir the now-empty directory.
	w("\tmov x0, x20")
	w("\tmov x8, #57") // close
	w("\tsvc #0")
	w("\tmov x0, #100")
	w("\tneg x0, x0")
	w("\tmov x1, x19")
	w("\tmov x2, #512") // AT_REMOVEDIR
	w("\tmov x8, #35")  // unlinkat
	w("\tsvc #0")
	w(".Lssa_rda_none:")
	emitResultUnitBox(w, true, "")
	w("\tb .Lssa_rda_ret")
	w(".Lssa_rda_some:")
	w("\tneg x24, x0") // errno
	emitEmptyString(w, "x1")
	w("\tmov x0, x24")
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x24, x0") // IoError box
	emitResultUnitBox(w, false, "x24")
	w(".Lssa_rda_ret:")
	w("\tldp x23, x24, [sp], #16")
	w("\tldp x21, x22, [sp], #16")
	w("\tldp x19, x20, [sp], #16")
	w("\tldp x29, x30, [sp], #16")
	w("\tret")
}

// emitTempDirHelper writes temp_dir(prefix) -> Result[string, IoError]: create a
// fresh uniquely-named directory "/tmp/<prefix>-XXXXXXXX" (8 lowercase-hex random
// digits) and return Ok(path) with the created directory's path. Any mkdirat
// failure maps -errno through __fern_io_error (passing the prefix as the path, as
// the interpreter does) and returns Err(IoError). The base is always /tmp — the
// arm64-ssa backend doesn't honour $TMPDIR (a documented simplification vs the
// interpreter's os.TempDir()), which is sufficient for the edge/CLI use case.
// The path is built into a scratch heap buffer; on EEXIST (a suffix collision) a
// fresh random suffix is drawn and mkdirat retried. On success a single-word rc
// string of the path is allocated and wrapped in the Result Ok box (tag 0,
// string@8). Non-leaf (calls __fern_io_error); 128-byte frame with callee-saved
// x19=prefix / x20=pathbuf / x21=path_len / x22=hex_start / x24=string. x0=prefix.
func emitTempDirHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("temp_dir"))
	w("\tstp x29, x30, [sp, #-128]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tstp x21, x22, [sp, #32]")
	w("\tstp x23, x24, [sp, #48]")
	w("\tmov x19, x0") // prefix
	// Allocate a scratch path buffer of prefix_len + 15 bytes:
	// "/tmp/"(5) + prefix + "-"(1) + 8 hex + NUL(1).
	w("\tldur w2, [x19, #-4]") // prefix_len
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x2, #15")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov x20, x4") // pathbuf
	// path_len = prefix_len + 14; hex_start = pathbuf + 6 + prefix_len.
	w("\tadd x21, x2, #14")
	w("\tadd x22, x20, #6")
	w("\tadd x22, x22, x2")
	// Write the "/tmp/" prefix.
	w("\tmov w9, #47") // '/'
	w("\tstrb w9, [x20]")
	w("\tmov w9, #116") // 't'
	w("\tstrb w9, [x20, #1]")
	w("\tmov w9, #109") // 'm'
	w("\tstrb w9, [x20, #2]")
	w("\tmov w9, #112") // 'p'
	w("\tstrb w9, [x20, #3]")
	w("\tmov w9, #47") // '/'
	w("\tstrb w9, [x20, #4]")
	// Copy the prefix bytes to pathbuf+5.
	w("\tadd x13, x20, #5")
	w("\tmov w10, #0")
	w(".Lssa_td_cp:")
	w("\tcmp w10, w2")
	w("\tb.hs .Lssa_td_cpd")
	w("\tldrb w11, [x19, x10]")
	w("\tstrb w11, [x13, x10]")
	w("\tadd w10, w10, #1")
	w("\tb .Lssa_td_cp")
	w(".Lssa_td_cpd:")
	// Write the '-' separator at pathbuf+5+prefix_len.
	w("\tadd x13, x13, x2") // pathbuf + 5 + prefix_len
	w("\tmov w11, #45")     // '-'
	w("\tstrb w11, [x13]")
	// NUL-terminate at pathbuf+path_len (fixed regardless of the random suffix).
	w("\tstrb wzr, [x20, x21]")
	w(".Lssa_td_retry:")
	// getrandom(sp+64, 4, 0) — 4 random bytes into the scratch slot.
	w("\tadd x0, sp, #64")
	w("\tmov x1, #4")
	w("\tmov x2, #0")
	w("\tmov x8, #278") // getrandom
	w("\tsvc #0")
	w("\tldr w15, [sp, #64]") // 32-bit random
	// Format 8 lowercase-hex digits into [hex_start .. hex_start+8], low nibble
	// first (a deterministic-but-reversed rendering — order is irrelevant, the
	// suffix only needs to be unique).
	w("\tmov w9, #0")
	w(".Lssa_td_hex:")
	w("\tcmp w9, #8")
	w("\tb.hs .Lssa_td_hexd")
	w("\tand w12, w15, #0xf")
	w("\tcmp w12, #10")
	w("\tb.lo .Lssa_td_dig")
	w("\tadd w12, w12, #87") // 'a' - 10
	w("\tb .Lssa_td_put")
	w(".Lssa_td_dig:")
	w("\tadd w12, w12, #48") // '0'
	w(".Lssa_td_put:")
	w("\tstrb w12, [x22, x9]")
	w("\tlsr x15, x15, #4")
	w("\tadd w9, w9, #1")
	w("\tb .Lssa_td_hex")
	w(".Lssa_td_hexd:")
	// mkdirat(AT_FDCWD, pathbuf, 0700).
	w("\tmov x0, #100")
	w("\tneg x0, x0")
	w("\tmov x1, x20")
	w("\tmov x2, #448") // 0o700
	w("\tmov x8, #34")  // mkdirat
	w("\tsvc #0")
	w("\tcbz x0, .Lssa_td_ok")
	w("\tcmn x0, #17") // -EEXIST → retry with a fresh suffix
	w("\tb.eq .Lssa_td_retry")
	// Other error: map -errno through __fern_io_error(errno, prefix).
	w("\tneg x0, x0")
	w("\tmov x1, x19")
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x19, x0") // IoError box
	// Result.Err(IoError): box {rc=1, tag=1, ioerr@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	w("\tmov w6, #1")
	w("\tstr w6, [x0]") // tag = 1 (Err)
	w("\tstr x19, [x0, #8]")
	w("\tb .Lssa_td_ret")
	w(".Lssa_td_ok:")
	// Allocate a single-word rc string of path_len bytes (+ NUL).
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x21, #9")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w7, #1")
	w("\tstr w7, [x4]")      // rc = 1
	w("\tstr w21, [x4, #4]") // len = path_len
	w("\tadd x24, x4, #8")   // string data ptr
	// Copy path_len bytes from pathbuf to the string.
	w("\tmov w9, #0")
	w(".Lssa_td_scp:")
	w("\tcmp w9, w21")
	w("\tb.hs .Lssa_td_scpd")
	w("\tldrb w10, [x20, x9]")
	w("\tstrb w10, [x24, x9]")
	w("\tadd w9, w9, #1")
	w("\tb .Lssa_td_scp")
	w(".Lssa_td_scpd:")
	w("\tstrb wzr, [x24, x21]") // trailing NUL
	// Result.Ok(string): box {rc=1, tag=0, string@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	w("\tstr wzr, [x0]")  // tag = 0 (Ok)
	w("\tstr x24, [x0, #8]")
	w(".Lssa_td_ret:")
	w("\tldp x23, x24, [sp, #48]")
	w("\tldp x21, x22, [sp, #32]")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #128")
	w("\tret")
}

// emitReadDirHelper writes read_dir(path) -> Result[string[], IoError]: list the
// immediate children of a directory (base names only, no recursion, "." and ".."
// excluded), matching os.ReadDir. It NUL-terminates the path, opens it with
// O_DIRECTORY (16384 — the arm64 Linux arch-specific value, NOT the asm-generic
// 65536), then makes two getdents64 passes over a small 4 KiB scratch buffer
// (the buffer is never reclaimed, so it stays far smaller than the native
// backend's 1 MiB one): pass 1 counts the kept entries to size the array, an lseek rewinds
// the directory, and pass 2 allocates a single-word rc string per base name and
// stores it into the string[] container (16-byte header: cap@[data-12],
// rc@[data-8], len@[data-4]; element pointers at [data + i*8]). Each pass loops
// getdents until it returns 0, so directories of any size are listed (bounded
// only by the arena the strings must fit in). The container is
// wrapped in the Result Ok box (tag 0, arr@8). Any openat/getdents failure maps
// -errno through __fern_io_error and returns Err(IoError). Non-leaf; 96-byte
// frame with callee-saved x19=offset / x20=fd / x21=buffer / x22=chunk-len /
// x23=count / x24=fill-index / x25=path / x26=pathz / x27=container. x0=path.
func emitReadDirHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("read_dir"))
	w("\tstp x29, x30, [sp, #-96]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tstp x21, x22, [sp, #32]")
	w("\tstp x23, x24, [sp, #48]")
	w("\tstp x25, x26, [sp, #64]")
	w("\tstp x27, x28, [sp, #80]")
	w("\tmov x25, x0") // path
	// NUL-terminate the path into a fresh heap buffer (x26).
	w("\tldur w2, [x25, #-4]") // path len
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x2, #1")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w7, #0")
	w(".Lssa_rd_cp:")
	w("\tcmp w7, w2")
	w("\tb.hs .Lssa_rd_cpd")
	w("\tldrb w8, [x25, x7]")
	w("\tstrb w8, [x4, x7]")
	w("\tadd w7, w7, #1")
	w("\tb .Lssa_rd_cp")
	w(".Lssa_rd_cpd:")
	w("\tstrb wzr, [x4, x2]")
	w("\tmov x26, x4") // pathz
	// openat(AT_FDCWD, pathz, O_RDONLY|O_DIRECTORY, 0).
	w("\tmov x0, #100")
	w("\tneg x0, x0")
	w("\tmov x1, x26")
	w("\tmov x2, #16384") // O_DIRECTORY (arm64 Linux)
	w("\tmov x3, #0")
	w("\tmov x8, #56") // openat
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa_rd_err_open")
	w("\tmov x20, x0") // fd
	// Allocate a 4 KiB dirent scratch buffer (x21), reused across both passes.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x6, x4, #4096")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov x21, x4") // buffer
	// Pass 1: getdents loop counting kept entries (excluding "." / "..") into x23.
	w("\tmov x23, #0")
	w(".Lssa_rd_g1:")
	w("\tmov x0, x20")
	w("\tmov x1, x21")
	w("\tmov x2, #4096")
	w("\tmov x8, #61") // getdents64
	w("\tsvc #0")
	w("\tcbz x0, .Lssa_rd_g1d")             // end of directory
	w("\ttbnz x0, #63, .Lssa_rd_err_close") // error
	w("\tmov x22, x0")                      // chunk len
	w("\tmov x19, #0")                      // offset
	w(".Lssa_rd_c1:")
	w("\tcmp x19, x22")
	w("\tb.hs .Lssa_rd_g1") // chunk consumed → read the next
	w("\tadd x10, x21, x19")
	w("\tadd x10, x10, #19") // d_name ptr
	w("\tldrb w11, [x10]")
	w("\tcmp w11, #46") // '.'
	w("\tb.ne .Lssa_rd_c1n")
	w("\tldrb w11, [x10, #1]")
	w("\tcbz w11, .Lssa_rd_c1s") // "." → skip
	w("\tcmp w11, #46")
	w("\tb.ne .Lssa_rd_c1n")
	w("\tldrb w11, [x10, #2]")
	w("\tcbz w11, .Lssa_rd_c1s") // ".." → skip
	w(".Lssa_rd_c1n:")
	w("\tadd x23, x23, #1")
	w(".Lssa_rd_c1s:")
	w("\tadd x12, x21, x19")
	w("\tldrh w11, [x12, #16]") // d_reclen
	w("\tadd x19, x19, x11")
	w("\tb .Lssa_rd_c1")
	w(".Lssa_rd_g1d:")
	// Rewind the directory for pass 2: lseek(fd, 0, SEEK_SET).
	w("\tmov x0, x20")
	w("\tmov x1, #0")
	w("\tmov x2, #0")
	w("\tmov x8, #62") // lseek
	w("\tsvc #0")
	// Allocate the string[] container: 16-byte header + count*8 pointers (x27).
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tlsl x5, x23, #3")
	w("\tadd x6, x5, #16")
	w("\tadd x7, x4, x6")
	w("\tstr x7, [x3]")
	emitHeapGuardCall(w)
	w("\tadd x27, x4, #16")      // container data
	w("\tstur w23, [x27, #-12]") // cap = count
	w("\tmov w9, #1")
	w("\tstur w9, [x27, #-8]")  // rc = 1
	w("\tstur w23, [x27, #-4]") // len = count
	// Pass 2: getdents loop again, filling the container with a fresh string per
	// kept entry.
	w("\tmov x24, #0") // fill index
	w(".Lssa_rd_g2:")
	w("\tmov x0, x20")
	w("\tmov x1, x21")
	w("\tmov x2, #4096")
	w("\tmov x8, #61") // getdents64
	w("\tsvc #0")
	w("\tcbz x0, .Lssa_rd_g2d")
	w("\ttbnz x0, #63, .Lssa_rd_err_close")
	w("\tmov x22, x0") // chunk len
	w("\tmov x19, #0") // offset
	w(".Lssa_rd_p2:")
	w("\tcmp x19, x22")
	w("\tb.hs .Lssa_rd_g2") // chunk consumed → read the next
	w("\tadd x10, x21, x19")
	w("\tadd x10, x10, #19") // d_name ptr
	w("\tldrb w11, [x10]")
	w("\tcmp w11, #46")
	w("\tb.ne .Lssa_rd_p2t")
	w("\tldrb w11, [x10, #1]")
	w("\tcbz w11, .Lssa_rd_p2a")
	w("\tcmp w11, #46")
	w("\tb.ne .Lssa_rd_p2t")
	w("\tldrb w11, [x10, #2]")
	w("\tcbz w11, .Lssa_rd_p2a")
	w(".Lssa_rd_p2t:")
	// strlen(d_name) → x12.
	w("\tmov x12, #0")
	w(".Lssa_rd_p2sl:")
	w("\tldrb w13, [x10, x12]")
	w("\tcbz w13, .Lssa_rd_p2sd")
	w("\tadd x12, x12, #1")
	w("\tb .Lssa_rd_p2sl")
	w(".Lssa_rd_p2sd:")
	// Allocate a single-word rc string: 8-byte header + len + NUL.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x12, #9")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w7, #1")
	w("\tstr w7, [x4]")      // rc = 1
	w("\tstr w12, [x4, #4]") // len
	w("\tadd x14, x4, #8")   // string data
	w("\tmov x15, #0")
	w(".Lssa_rd_p2cp:")
	w("\tcmp x15, x12")
	w("\tb.hs .Lssa_rd_p2cpd")
	w("\tldrb w16, [x10, x15]")
	w("\tstrb w16, [x14, x15]")
	w("\tadd x15, x15, #1")
	w("\tb .Lssa_rd_p2cp")
	w(".Lssa_rd_p2cpd:")
	w("\tstrb wzr, [x14, x12]")        // trailing NUL
	w("\tstr x14, [x27, x24, lsl #3]") // container[idx] = string
	w("\tadd x24, x24, #1")
	w(".Lssa_rd_p2a:")
	w("\tadd x12, x21, x19")
	w("\tldrh w11, [x12, #16]") // d_reclen
	w("\tadd x19, x19, x11")
	w("\tb .Lssa_rd_p2")
	w(".Lssa_rd_g2d:")
	// close(fd).
	w("\tmov x0, x20")
	w("\tmov x8, #57") // close
	w("\tsvc #0")
	// Result.Ok(container): box {rc=1, tag=0, arr@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	w("\tstr wzr, [x0]")  // tag = 0 (Ok)
	w("\tstr x27, [x0, #8]")
	w("\tb .Lssa_rd_ret")
	w(".Lssa_rd_err_close:")
	w("\tneg x9, x0") // errno (x9 survives the close syscall)
	w("\tmov x0, x20")
	w("\tmov x8, #57") // close
	w("\tsvc #0")
	w("\tmov x0, x9")
	w("\tb .Lssa_rd_err_dispatch")
	w(".Lssa_rd_err_open:")
	w("\tneg x0, x0") // errno
	w(".Lssa_rd_err_dispatch:")
	w("\tmov x1, x25") // path
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x25, x0") // IoError box
	// Result.Err(IoError): box {rc=1, tag=1, ioerr@8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")   // rc = 1
	w("\tadd x0, x4, #8") // box data
	w("\tmov w6, #1")
	w("\tstr w6, [x0]") // tag = 1 (Err)
	w("\tstr x25, [x0, #8]")
	w(".Lssa_rd_ret:")
	w("\tldp x27, x28, [sp, #80]")
	w("\tldp x25, x26, [sp, #64]")
	w("\tldp x23, x24, [sp, #48]")
	w("\tldp x21, x22, [sp, #32]")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #96")
	w("\tret")
}

// emitDropArrElemHelper returns the emitter for a per-element array drop:
// __fern_drop_arr_<kind>(ptr, stride) -> ptr, the scope-exit drop for an array
// whose elements each need releasing. On the LAST reference (rc == 1) it walks
// the `len` elements and calls `elemDrop` on each, then dec's the array box via
// __fern_arr_dec; a shared array (rc != 1) dec's the box only, since its
// elements stay alive for the other holder. Same null / low-address /
// static-sentinel guards as __fern_arr_dec. Returns the input ptr (the dec
// contract).
//
// Non-leaf — it calls into `elemDrop` in a loop — so it keeps a frame with
// callee-saved x19=ptr / x20=stride / x21=len / x22=i live across the calls.
//
// Two instantiations, differing only in the per-element call:
//
//   - string[]  -> __fern_str_dec. arm64ssa strings are single-word (one data
//     pointer per element), so each element load is a single ldr — unlike the
//     native backend's two-word walk.
//   - pointer[] -> __fern_rc_dec, for elements that are themselves rc-tracked
//     heap values (arrays, maps, structs). This is the one core/map.fern needs
//     to reclaim a Map's array-typed value column.
//
// `tag` distinguishes the two instantiations' local labels; emitting both with
// one tag would define each label twice.
func emitDropArrElemHelper(name, elemDrop, tag string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		lbl := func(suffix string) string { return ".Lssa_" + tag + "_" + suffix }
		w("")
		w("%s:", fnLabel(name))
		w("\tstp x29, x30, [sp, #-48]!")
		w("\tmov x29, sp")
		w("\tstp x19, x20, [sp, #16]")
		w("\tstp x21, x22, [sp, #32]")
		w("\tmov x19, x0") // ptr
		w("\tmov x20, x1") // stride
		w("\tcbz x19, %s", lbl("ret"))
		w("\tcmp x19, #0x10000")
		w("\tb.lo %s", lbl("ret"))
		w("\tldur w0, [x19, #-8]")          // rc
		w("\ttbnz w0, #31, %s", lbl("ret")) // static sentinel
		w("\tcmp w0, #1")
		w("\tb.ne %s", lbl("decarr")) // shared: dec the box only
		// rc == 1: walk the elements, releasing each.
		w("\tldur w21, [x19, #-4]") // len
		w("\tmov x22, #0")          // i
		w("%s:", lbl("loop"))
		w("\tcmp w22, w21")
		w("\tb.ge %s", lbl("decarr"))
		w("\tmadd x0, x22, x20, x19") // &elem[i]
		w("\tldr x0, [x0]")           // elem
		w("\tbl %s", fnLabel(elemDrop))
		w("\tadd x22, x22, #1")
		w("\tb %s", lbl("loop"))
		w("%s:", lbl("decarr"))
		w("\tmov x0, x19")
		w("\tmov x1, x20")
		w("\tbl %s", fnLabel("__fern_arr_dec"))
		w("\tmov x0, x19") // return ptr
		w("\tb %s", lbl("done"))
		w("%s:", lbl("ret"))
		w("\tmov x0, x19")
		w("%s:", lbl("done"))
		w("\tldp x21, x22, [sp, #32]")
		w("\tldp x19, x20, [sp, #16]")
		w("\tldp x29, x30, [sp], #48")
		w("\tret")
	}
}

// emitMapDropHelper writes __fern_map_drop(m) -> m: the scope-exit drop for a
// Map local. A Map handle keeps its rc at [m-8] and its kv-buffer pointer at
// [m+0]. On the LAST reference (rc == 1) both allocations are released — the buf
// (ast.MapHeaderBytes+8 + cap*(4 + entryStride + 1), cap at [buf+0], entryStride
// = 2*ptrW = 16 here, the +1/+8 being core/map's ctrl bytes and their mirror —
// __map_buf_bytes) and then the 16-byte handle cell at m-8; on a shared handle
// (rc > 1) the count is decremented in place. Entry keys and values are NOT
// walked: the IR emits a __map_drop_values call ahead of this one for the value
// column. Mirrors the stack-machine backend's emitMapDropRuntime, including its
// null / low-address / static-sentinel / non-positive-rc guards. m is held in
// callee-saved x19 across the __free calls.
func emitMapDropHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_map_drop"))
	w("\tstp x29, x30, [sp, #-32]!")
	w("\tmov x29, sp")
	w("\tstr x19, [sp, #16]")
	w("\tmov x19, x0") // m
	w("\tcbz x19, .Lssa_mapdrop_ret")
	w("\tcmp x19, #0x10000")
	w("\tb.lo .Lssa_mapdrop_ret")
	w("\tldur w1, [x19, #-8]") // rc
	w("\ttbnz w1, #31, .Lssa_mapdrop_ret")
	w("\tcmp w1, #0")
	w("\tb.le .Lssa_mapdrop_ret") // non-positive rc: nothing to do
	w("\tcmp w1, #1")
	w("\tb.ne .Lssa_mapdrop_dec") // shared: just decrement
	w("\tldr x4, [x19]")          // buf
	w("\tcbz x4, .Lssa_mapdrop_freehandle")
	w("\tcmp x4, #0x10000")
	w("\tb.lo .Lssa_mapdrop_freehandle")
	w("\tldr w5, [x4]")   // cap
	w("\tmov x6, #21")    // 4 + entryStride(16) + 1 ctrl byte
	w("\tmul x5, x5, x6") // cap * 21
	w("\tadd x1, x5, #%d", ast.MapHeaderBytes+8)
	w("\tmov x0, x4") // base = buf
	w("\tbl %s", fnLabel("__free"))
	w(".Lssa_mapdrop_freehandle:")
	w("\tsub x0, x19, #8") // handle base
	w("\tmov x1, #16")     // handle size
	w("\tbl %s", fnLabel("__free"))
	w("\tb .Lssa_mapdrop_ret")
	w(".Lssa_mapdrop_dec:")
	w("\tldur w1, [x19, #-8]")
	w("\tsub w1, w1, #1")
	w("\tstur w1, [x19, #-8]")
	w(".Lssa_mapdrop_ret:")
	w("\tmov x0, x19")
	w("\tldr x19, [sp, #16]")
	w("\tldp x29, x30, [sp], #32")
	w("\tret")
}

// emitMapHashSeedHelper writes __fern_map_hash_seed() -> i32: core/map's
// per-process string-hash seed, mixed into its FNV basis so attacker-supplied
// key strings cannot be precomputed into a colliding set offline (#6194).
//
// Per PROCESS, not per map — the draw is a getrandom syscall, which a program
// creating maps freely must not pay repeatedly, so the word caches it and every
// map after the first is a load. Value and cache flag share that one word (zero
// means "not yet drawn"), which is why the drawn value is forced nonzero with
// `orr w0, w0, #1`: zero is also core/map's "unseeded" sentinel, and a 0 seed
// reaching a map header would silently leave it unseeded. Mirrors the
// stack-machine backend's emitMapHashSeedRuntime.
func emitMapHashSeedHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_map_hash_seed"))
	w("\tadrp x1, %s", mapSeedSym)
	w("\tadd x1, x1, #:lo12:%s", mapSeedSym)
	w("\tldr w0, [x1]")
	w("\tcbnz w0, .Lssa_mapseed_ret")
	w("\tstp x29, x30, [sp, #-16]!")
	w("\tbl %s", fnLabel("random_i32"))
	w("\torr w0, w0, #1") // never 0 — see the doc above
	w("\tadrp x1, %s", mapSeedSym)
	w("\tadd x1, x1, #:lo12:%s", mapSeedSym)
	w("\tstr w0, [x1]")
	w("\tldp x29, x30, [sp], #16")
	w(".Lssa_mapseed_ret:")
	w("\tret")
}

// emitAllocReuseHelper writes __alloc_reuse(token, tokenSize, size) -> ptr: the
// drop-reuse (FBIP) primitive. When the token is live and its 16-byte size class
// matches `size`'s, it hands the token straight back — no free, no allocation,
// which is the whole point of reuse. On a null token or a class mismatch it
// releases the dropped block and allocates fresh, so a mispaired reuse is
// slow-not-wrong. Class arithmetic ((sz+15)&-16) mirrors the native helper's.
//
// AAPCS64: x0 = token, x1 = tokenSize, x2 = size. Tails into __alloc.
func emitAllocReuseHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__alloc_reuse"))
	w("\tcbz x0, .Lssa_reuse_fresh") // null token -> plain alloc(size)
	w("\tadd x3, x1, #15")
	w("\tand x3, x3, #-16")
	w("\tadd x4, x2, #15")
	w("\tand x4, x4, #-16")
	w("\tcmp x3, x4")
	w("\tb.ne .Lssa_reuse_mismatch")
	w("\tret") // same class: reuse the token in place
	w(".Lssa_reuse_mismatch:")
	w("\tstp x29, x30, [sp, #-16]!")
	w("\tmov x29, sp")
	w("\tstr x2, [sp, #-16]!") // save size (keeps sp 16-aligned)
	w("\tbl %s", fnLabel("__free"))
	w("\tldr x2, [sp], #16") // restore size
	w("\tldp x29, x30, [sp], #16")
	w(".Lssa_reuse_fresh:")
	w("\tmov x0, x2")               // size
	w("\tb %s", fnLabel("__alloc")) // tail call
}

// emitMemcpyHelper writes __memcpy(dst, src, n) -> dst, forwarding to
// __ssa_bcopy. core/map.fern uses it to move a kv buffer's entries into a freshly
// grown one. Mirrors the stack-machine backend's __fern_memcpy. Returns dst (an
// 8-byte pointer), so ssa.ResolveWidths classifies it wide.
func emitMemcpyHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__memcpy"))
	w("\tmov x3, x0") // save dst for the return (outside __ssa_bcopy's clobbers)
	emitBcopyCall(w, "x0", "x1", "x2")
	w("\tmov x0, x3")
	w("\tret")
}

// emitArrIdxHelperN returns the emitter for a bounds-checked array-index helper
// of a given element stride (2^shift bytes): __arr_idx (stride 4), __arr_idx_1
// (byte), __arr_idx_8 (i64/pointer), __arr_idx_16 (two-word string[]). Each
// compares idx against the length at [base-4] with a
// single unsigned compare (a negative idx is huge unsigned, so it fails too) and,
// on out-of-range, exits 134 — matching the native array-index trap / wasm's
// `unreachable`. Returns base + idx*stride; the caller's OpLoad reads the element.
// Leaf. The ok-label is namespaced by shift so all variants coexist in a module.
func emitArrIdxHelperN(name string, shift int) func(w func(string, ...any)) {
	return emitArrIdxHelperNChecked(name, shift, true)
}

// emitArrIdxHelperNChecked is emitArrIdxHelperN with an explicit bounds-check
// toggle. The `_nc` (no-check) variants (#4380 lever 3) drop the len-load +
// compare + trap when the caller proved the index in range (ForEach desugar).
func emitArrIdxHelperNChecked(name string, shift int, checked bool) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		ok := fmt.Sprintf(".Lssa_arridx%d_ok", shift)
		w("")
		w("%s:", fnLabel(name))
		if checked {
			w("\tldur w2, [x0, #-4]") // len
			w("\tcmp w1, w2")
			w("\tb.lo %s", ok) // idx < len (unsigned)
			w("\tmov x0, #134")
			w("\tmov x8, #94") // exit_group
			w("\tsvc #0")
			w("%s:", ok)
		}
		if shift == 0 {
			w("\tadd x0, x0, x1") // base + idx*1
		} else {
			w("\tadd x0, x0, x1, lsl #%d", shift) // base + idx*stride
		}
		w("\tret")
	}
}

// emitArrPushGrowHelper writes __fern_arr_push_grow(arr, oldLen, stride) ->
// new_data: the array-append growth helper. Fast path — if the array is uniquely
// held (rc==1) and has spare capacity (oldLen < cap) — bumps rc to 2 and the
// length in place, returning arr. Otherwise it allocates a fresh, larger buffer
// (newCap = max(2*newLen, 4)), lays the array header (cap@-12, rc=1@-8, len@-4
// relative to the new data pointer, past a headerBytes = max(16, stride) prefix),
// copies the old elements, and returns the new data pointer. Unlike the native
// helper (which calls __fern_alloc) this allocates through __ssa_alloc_pres,
// and the copy goes through __ssa_bcopy — mirroring __str_concat.
// x0=arr, w1=oldLen, w2=stride. The old buffer is the caller's to release
// (the IR decs it after the move), so nothing is freed here.
func emitArrPushGrowHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_arr_push_grow"))
	// Fast path: rc == 1 and oldLen < cap → grow in place.
	w("\tldur w3, [x0, #-8]") // rc
	w("\tcmp w3, #1")
	w("\tb.ne .Lssa_apg_copy")
	w("\tldur w4, [x0, #-12]") // cap
	w("\tcmp w1, w4")
	w("\tb.ge .Lssa_apg_copy")
	w("\tmov w3, #2")
	w("\tstur w3, [x0, #-8]") // rc = 2
	w("\tadd w4, w1, #1")
	w("\tstur w4, [x0, #-4]") // len = oldLen + 1
	w("\tret")
	w(".Lssa_apg_copy:")
	w("\tadd w4, w1, #1") // w4 = newLen
	w("\tlsl w5, w4, #1")
	w("\tmov w6, #4")
	w("\tcmp w5, w6")
	w("\tcsel w5, w5, w6, ge") // w5 = newCap = max(2*newLen, 4)
	w("\tmov w6, #16")
	w("\tcmp w2, w6")
	w("\tcsel w6, w2, w6, ge") // w6 = headerBytes = max(stride, 16)
	w("\tmadd w7, w5, w2, w6") // w7 = allocSize = headerBytes + newCap*stride
	// The array lays its own cap/rc/len header past the headerBytes prefix.
	w("\tmov w16, w7")
	emitAllocPresCall(w)
	w("\tmov x9, x16")           // x9 = base
	w("\tadd x11, x9, w6, uxtw") // x11 = new_data = base + headerBytes
	w("\tsub x12, x11, #12")
	w("\tstr w5, [x12]") // cap = newCap
	w("\tmov w13, #1")
	w("\tstur w13, [x11, #-8]") // rc = 1
	w("\tstur w4, [x11, #-4]")  // len = newLen
	// Copy oldLen*stride bytes from arr (x0) to new_data (x11).
	w("\tmul w14, w1, w2") // nbytes (a w-destination zero-extends into x14)
	w("\tmov x15, x0")     // src, clear of __ssa_bcopy's argument registers
	emitBcopyCall(w, "x11", "x15", "x14")
	w("\tmov x0, x11") // return new_data
	w("\tret")
}

// emitArrPushGrowElemHelper writes the element-retaining siblings of
// __fern_arr_push_grow for arrays of rc-tracked pointers (single-word strings
// included, which is why the _str spellings share it): the same grow, then on
// the copy path a __fern_rc_inc over the oldLen copied elements so the fresh
// buffer owns its own references. A raw copy would leave both buffers holding
// the same pointers under one count, and the old buffer's walk-drop would
// release elements the copy still uses.
//
// moveForm is the self-append `a = a.append(v)` contract: the old buffer is
// about to be released without an element walk, so at rc == 1 the copy
// inherits its references and retaining would leak one per element per grow;
// the retain happens only when rc != 1, when an alias keeps the old buffer
// alive. Mirrors the flat backend's helpers. x0=arr, w1=oldLen, w2=stride;
// returns x0=new_data.
func emitArrPushGrowElemHelper(name, tag string, moveForm bool) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		lbl := func(suffix string) string { return ".Lssa_" + tag + "_" + suffix }
		w("")
		w("%s:", fnLabel(name))
		w("\tstp x29, x30, [sp, #-64]!")
		w("\tmov x29, sp")
		w("\tstp x19, x20, [sp, #16]")
		w("\tstp x21, x22, [sp, #32]")
		w("\tstp x23, x24, [sp, #48]")
		w("\tmov x19, x0")         // arr
		w("\tmov w20, w1")         // oldLen
		w("\tmov w21, w2")         // stride
		w("\tldur w22, [x0, #-8]") // rc before the grow
		w("\tbl %s", fnLabel("__fern_arr_push_grow"))
		w("\tmov x23, x0") // new_data
		w("\tcmp x23, x19")
		w("\tb.eq %s", lbl("done")) // grown in place: no copy, nothing to retain
		if moveForm {
			w("\tcmp w22, #1")
			w("\tb.eq %s", lbl("done")) // the copy inherits a sole owner's references
		}
		w("\tmov x24, #0")
		w("%s:", lbl("loop"))
		w("\tcmp w24, w20")
		w("\tb.ge %s", lbl("done"))
		w("\tmadd x0, x24, x21, x23") // &new_data[i]
		w("\tldr x0, [x0]")
		w("\tbl %s", fnLabel("__fern_rc_inc"))
		w("\tadd x24, x24, #1")
		w("\tb %s", lbl("loop"))
		w("%s:", lbl("done"))
		w("\tmov x0, x23")
		w("\tldp x23, x24, [sp, #48]")
		w("\tldp x21, x22, [sp, #32]")
		w("\tldp x19, x20, [sp, #16]")
		w("\tldp x29, x30, [sp], #64")
		w("\tret")
	}
}

// emitStatHelper writes stat(path) -> Result[FileStat, IoError]: fstatat the
// path and report its kind and size. x0 = path (single-word string).
//
// Linux-only, like the rest of this emitter, so there are none of the flat
// backend's darwin branches: fstatat is syscall 79, AT_FDCWD is -100, and the
// two fields read out of the 128-byte struct stat are st_mode (u32 @ +16) and
// st_size (i64 @ +48).
//
// Shape follows read_file exactly — the NUL-terminated path copy, the frame
// statbuf, and the two boxed results — because they are the same contract:
// a Result box is {rc=1, tag, payload@+8} with the payload one word, and Err
// dispatches errno + path through __fern_io_error. The Ok payload here is a
// FileStat box laid out {is_file@+0, is_dir@+4, size@+8}, matching the flat
// backend's.
func emitStatHelper(w func(string, ...any)) {
	emitStatLikeHelper(w, "stat", 0, "stat")
}

// emitLstatHelper writes lstat(path): emitStatHelper with AT_SYMLINK_NOFOLLOW
// in fstatat's flags word, so a symlink reports its own st_mode and comes back
// neither is_file nor is_dir (#7982).
func emitLstatHelper(w func(string, ...any)) {
	emitStatLikeHelper(w, "lstat", 256, "lstat")
}

// emitStatLikeHelper is the shared body. `atFlags` is fstatat's flags word and
// `lp` prefixes the local labels so both helpers can live in one object.
func emitStatLikeHelper(w func(string, ...any), name string, atFlags int, lp string) {
	w("")
	w("%s:", fnLabel(name))
	w("\tstp x29, x30, [sp, #-256]!")
	w("\tmov x29, sp")
	w("\tstp x19, x20, [sp, #16]")
	w("\tstp x21, x22, [sp, #32]")
	w("\tstp x23, x24, [sp, #48]")
	w("\tmov x19, x0") // path
	// NUL-terminate the path into a heap buffer (x24).
	w("\tldur w2, [x19, #-4]")
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x2, #1")
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w7, #0")
	w(".Lssa%s_cp:", lp)
	w("\tcmp w7, w2")
	w("\tb.hs .Lssa%s_cpd", lp)
	w("\tldrb w8, [x19, x7]")
	w("\tstrb w8, [x4, x7]")
	w("\tadd w7, w7, #1")
	w("\tb .Lssa%s_cp", lp)
	w(".Lssa%s_cpd:", lp)
	w("\tstrb wzr, [x4, x2]")
	w("\tmov x24, x4") // path_nul
	// fstatat(AT_FDCWD, path_nul, statbuf@sp+64, 0).
	w("\tmov x0, #100")
	w("\tneg x0, x0") // AT_FDCWD
	w("\tmov x1, x24")
	w("\tadd x2, sp, #64")
	w("\tmov x3, #%d", atFlags)
	w("\tmov x8, #79") // fstatat
	w("\tsvc #0")
	w("\ttbnz x0, #63, .Lssa%s_err", lp)
	w("\tldr w9, [sp, #80]")   // st_mode (u32 @ statbuf+16)
	w("\tldr x22, [sp, #112]") // st_size (i64 @ statbuf+48)
	w("\tmov w11, #61440")     // S_IFMT
	w("\tand w9, w9, w11")
	w("\tmov x20, #0")     // is_file
	w("\tmov w10, #32768") // S_IFREG
	w("\tcmp w9, w10")
	w("\tb.ne .Lssa%s_nf", lp)
	w("\tmov x20, #1")
	w(".Lssa%s_nf:", lp)
	w("\tmov x21, #0")     // is_dir
	w("\tmov w10, #16384") // S_IFDIR
	w("\tcmp w9, w10")
	w("\tb.ne .Lssa%s_nd", lp)
	w("\tmov x21, #1")
	w(".Lssa%s_nd:", lp)
	// FileStat box: {rc=1, is_file@+0, is_dir@+4, size@+8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]")    // rc = 1
	w("\tadd x23, x4, #8") // FileStat data
	w("\tstr w20, [x23]")
	w("\tstr w21, [x23, #4]")
	w("\tstr x22, [x23, #8]")
	// Result.Ok(FileStat): box {rc=1, tag=0, filestat@+8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]") // rc = 1
	w("\tadd x0, x4, #8")
	w("\tstr wzr, [x0]") // tag = 0 (Ok)
	w("\tstr x23, [x0, #8]")
	w("\tb .Lssa%s_ret", lp)
	w(".Lssa%s_err:", lp)
	w("\tneg x0, x0")  // errno
	w("\tmov x1, x19") // path
	w("\tbl %s", fnLabel("__fern_io_error"))
	w("\tmov x19, x0") // IoError box
	// Result.Err(IoError): box {rc=1, tag=1, ioerr@+8}.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym)
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16")
	w("\tadd x5, x4, #24")
	w("\tstr x5, [x3]")
	emitHeapGuardCall(w)
	w("\tmov w6, #1")
	w("\tstr w6, [x4]") // rc = 1
	w("\tadd x0, x4, #8")
	w("\tmov w6, #1")
	w("\tstr w6, [x0]") // tag = 1 (Err)
	w("\tstr x19, [x0, #8]")
	w(".Lssa%s_ret:", lp)
	w("\tldp x23, x24, [sp, #48]")
	w("\tldp x21, x22, [sp, #32]")
	w("\tldp x19, x20, [sp, #16]")
	w("\tldp x29, x30, [sp], #256")
	w("\tret")
}

// emitSliceIdxHelper writes __slice_idx / _1 / _8 / _16 (slice, idx) -> address:
// the bounds-checked element address for a slice view, one per element stride
// (shift is the stride's log2). The array siblings above take the buffer
// pointer directly and read the length from its header at [base-4]; a slice is
// one indirection further out — a {data_ptr, len} header — so the length comes
// from [slice+8] and the data pointer has to be loaded from [slice+0] before
// the stride-shifted add.
//
// Out of range exits 134, matching emitArrIdxHelperNChecked rather than the
// flat backend's message-printing abort: the exit code is the observable, and
// this emitter's array path already reports it this way.
//
// No `_nc` variants: unlike the array helpers, the IR emits no
// bounds-check-elided slice form. And no `_16`: that stride is a two-word
// string element, which only exists at ptrW=4 — at this emitter's ptrW=8 a
// `[string]` slice indexes through `__slice_idx_8`.
func emitSliceIdxHelper(name string, shift int) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		// Keyed on the NAME, not the stride: two helpers can share a shift
		// (__slice_idx and a stride-4 spelling of it both want 2), and the
		// assembler silently keeps the last definition of a duplicate label.
		ok := fmt.Sprintf(".Lssa_%s_ok", strings.TrimPrefix(fnLabel(name), "fn_"))
		w("")
		w("%s:", fnLabel(name))
		w("\tldr w2, [x0, #8]") // len, past the 8-byte data pointer
		w("\tcmp w1, w2")
		w("\tb.lo %s", ok) // idx < len (unsigned)
		w("\tmov x0, #134")
		w("\tmov x8, #94") // exit_group
		w("\tsvc #0")
		w("%s:", ok)
		w("\tldr x0, [x0]") // data_ptr
		if shift == 0 {
			w("\tadd x0, x0, x1")
		} else {
			w("\tadd x0, x0, x1, lsl #%d", shift)
		}
		w("\tret")
	}
}

// emitSliceRangeHelper writes __slice_range(lo, hi, len) -> i32: the
// slice-construction bounds check, returning the new length hi - lo. Leaf, no
// heap. w0 = lo, w1 = hi, w2 = len.
//
// The comparisons are UNSIGNED, which folds four conditions into two branches:
// a negative bound reads as a huge unsigned value, so `hi > len` also catches
// hi < 0 and `lo > hi` also catches lo < 0.
//
// The sxtw normalises a bound that reaches the helper with dirty bits above 31,
// mirroring the flat backend's #5294 fix, so the unsigned compares see the
// value the caller meant. It is belt-and-braces at this emitter's width
// discipline rather than load-bearing: maskFix sign-extends every narrow
// result, so bounds already arrive sign-extended, and on a value that fits i32
// zero-extension would decide identically (both turn a negative into a large
// unsigned). Keep it — the cost is three instructions and it stops the helper
// depending on an invariant maintained somewhere else.
func emitSliceRangeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__slice_range"))
	w("\tsxtw x0, w0")
	w("\tsxtw x1, w1")
	w("\tsxtw x2, w2")
	w("\tcmp x1, x2")
	w("\tb.hi .Lssa_slicerange_trap") // hi > len
	w("\tcmp x0, x1")
	w("\tb.hi .Lssa_slicerange_trap") // lo > hi
	w("\tsub w0, w1, w0")
	w("\tret")
	w(".Lssa_slicerange_trap:")
	w("\tmov x0, #134")
	w("\tmov x8, #94") // exit_group
	w("\tsvc #0")
}

// emitSliceMakeHelper writes __slice_make(data, len) -> slice: the 16-byte
// {data_ptr@+0, i32 len@+8} view header, 4 bytes of trailing pad. x0 = data
// (full 64 bits — a `str w` here would truncate a heap pointer, since this
// arena is based above 4 GiB), w1 = len.
//
// The layout is forced, not chosen: the IR lowers `.len()` to a load at
// [slice + ptrW] and `slice as usize` to a pointer-width load at [slice + 0],
// and __slice_idx_* dereference the same two fields. No rc header — a slice is
// a view over someone else's bytes and nothing drops it.
func emitSliceMakeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__slice_make"))
	w("\tadrp x8, %s", heapPtrSym)
	w("\tadd x8, x8, #:lo12:%s", heapPtrSym)
	w("\tldr x9, [x8]")
	w("\tadd x9, x9, #15")
	w("\tand x9, x9, #-16") // header base, 16-aligned
	w("\tadd x10, x9, #16")
	w("\tstr x10, [x8]")
	emitHeapGuardCall(w)
	w("\tstr x0, [x9]")     // [+0] data pointer
	w("\tstr w1, [x9, #8]") // [+8] len (i32)
	w("\tmov x0, x9")
	w("\tret")
}

// emitStringAsBytesHelper writes __method_string_as_bytes(s) -> slice: the
// non-copying `.as_bytes()` view over the receiver's own bytes.
//
// It reads the length itself rather than taking an incoming (data, len) pair,
// which is what the flat backend's version does: that one runs the TWO-word
// string ABI, and it panics outright on the single-word ABI. This path is
// single-word — buildArm64SSA never sets ast.TwoWordOverride, and __str_len
// here is `ldur w0, [x0, #-4]` — so the receiver is one pointer with its
// length at [data-4]. Past that it IS __slice_make, so it tail-branches there
// rather than laying the header down a second time.
func emitStringAsBytesHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__method_string_as_bytes"))
	w("\tldur w1, [x0, #-4]") // len
	w("\tb %s", fnLabel("__slice_make"))
}

// emitHeapBumpBytesHelper writes __fern_heap_bump_bytes() -> i64: the bump
// high-water mark, (cursor - base) in bytes. Zero before _start seeds the
// reservation, which only a program that never allocates can observe. Mirrors
// the native helper of the same name; leaf.
func emitHeapBumpBytesHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_heap_bump_bytes"))
	w("\tadrp x1, %s", heapPtrSym)
	w("\tadd x1, x1, #:lo12:%s", heapPtrSym)
	w("\tldr x0, [x1]")
	w("\tcbz x0, .Lssa_bumpb_zero") // never reserved -> 0
	w("\tadrp x2, %s", heapBaseSym)
	w("\tadd x2, x2, #:lo12:%s", heapBaseSym)
	w("\tldr x2, [x2]")
	w("\tsub x0, x0, x2")
	w("\tret")
	w(".Lssa_bumpb_zero:")
	w("\tmov x0, #0")
	w("\tret")
}

// movImm64 writes `reg = v` for a non-negative v that exceeds `mov`'s 16-bit
// immediate, as movz + up to three movk. The native backends reach for the
// `ldr reg, =v` literal-pool form here; this emitter has no pool to flush, so
// materialise the constant in registers instead.
func movImm64(w func(string, ...any), reg string, v uint64) {
	for _, line := range movImmLines(reg, v) {
		w("%s", line)
	}
}

// movImmLines materialises `reg = v` as the shortest correct sequence: a single
// `mov` where the assembler can encode one (a lone shifted halfword, or the
// inverse of one — MOVZ and MOVN), otherwise movz + a movk per remaining
// non-zero halfword. The single-instruction test has to be made here rather
// than left to the assembler, because only one of the two assemblers this
// backend feeds rejects what it cannot encode.
func movImmLines(reg string, v uint64) []string {
	if oneHalfword(v) || oneHalfword(^v) {
		return []string{fmt.Sprintf("\tmov %s, #%d", reg, int64(v))}
	}
	out := []string{fmt.Sprintf("\tmov %s, #%d", reg, v&0xffff)}
	for shift := 16; shift < 64; shift += 16 {
		if half := (v >> uint(shift)) & 0xffff; half != 0 {
			out = append(out, fmt.Sprintf("\tmovk %s, #%d, lsl #%d", reg, half, shift))
		}
	}
	return out
}

// oneHalfword reports whether v has at most one non-zero 16-bit halfword, which
// is exactly what MOVZ encodes.
func oneHalfword(v uint64) bool {
	n := 0
	for shift := 0; shift < 64; shift += 16 {
		if (v>>uint(shift))&0xffff != 0 {
			n++
		}
	}
	return n <= 1
}

// The asm-generic syscall numbers the time helpers issue, and the two clock ids
// clock_gettime reads.
const (
	sysClockGettime = 113
	sysNanosleep    = 101
	clockRealtime   = 0
	clockMonotonic  = 1
)

// emitClockHelper writes a clock_gettime(clockID, &ts) reader returning an i64
// in x0: `tv_sec*mul + tv_nsec/div`. monotonic_ns is (CLOCK_MONOTONIC, 1e9, 1)
// — whole nanoseconds — and now_unix_ms is (CLOCK_REALTIME, 1e3, 1e6). The
// timespec lands in a 16-byte frame slot; errno is ignored, as on the native
// backends, because the clock id is fixed and the buffer is ours.
func emitClockHelper(name string, clockID int, mul, div uint64) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tsub sp, sp, #16") // struct timespec { i64 tv_sec; i64 tv_nsec }
		w("\tmov x0, #%d", clockID)
		w("\tmov x1, sp")
		w("\tmov x8, #%d", sysClockGettime)
		w("\tsvc #0")
		w("\tldr x9, [sp]")      // tv_sec
		w("\tldr x11, [sp, #8]") // tv_nsec
		movImm64(w, "x10", mul)
		w("\tmul x9, x9, x10")
		if div > 1 {
			movImm64(w, "x10", div)
			w("\tudiv x11, x11, x10")
		}
		w("\tadd x0, x9, x11")
		w("\tadd sp, sp, #16")
		w("\tret")
	}
}

// emitSleepMsHelper writes sleep_ms(ms): nanosleep for ms milliseconds, split
// into the timespec's whole seconds and the nanosecond remainder. A
// non-positive argument returns without a syscall. Mirrors the native
// __fern_sleep_ms; leaf, with the request struct in a 16-byte frame slot. A
// signal-interrupted sleep is not resumed (rem = NULL), as on the natives.
func emitSleepMsHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("sleep_ms"))
	w("\tcmp x0, #0")
	w("\tb.le .Lssa_sleep_done")
	w("\tsub sp, sp, #16") // struct timespec { i64 tv_sec; i64 tv_nsec }
	w("\tmov x9, #1000")
	w("\tudiv x10, x0, x9")      // tv_sec = ms / 1000
	w("\tmsub x11, x10, x9, x0") // remainder ms
	w("\tstr x10, [sp]")
	movImm64(w, "x12", 1_000_000)
	w("\tmul x11, x11, x12") // tv_nsec = rem * 1e6
	w("\tstr x11, [sp, #8]")
	w("\tmov x0, sp") // &req
	w("\tmov x1, #0") // rem = NULL
	w("\tmov x8, #%d", sysNanosleep)
	w("\tsvc #0")
	w("\tadd sp, sp, #16")
	w(".Lssa_sleep_done:")
	w("\tret")
}

// emitAllocU8Helper writes __alloc_u8(n) -> data: allocate a fresh
// length-prefixed u8[] of n bytes and return the data pointer (past a 16-byte
// header; cap@-12, rc=1@-8, len@-4). The n data bytes are zero-filled to match
// the interpreter's zero-initialised u8[] (issue #2768: read-before-write
// callers like SHA padding rely on it, and a block popped off the freelist is
// not fresh). Allocates through __ssa_alloc_pres like __fern_arr_push_grow.
// n==0 falls through with a zero-iteration zero loop, yielding a valid
// header-only buffer whose len reads 0. x0=n, returns x0=data.
func emitAllocU8Helper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__alloc_u8"))
	w("\tmov w1, w0")       // w1 = n (preserve across the allocation)
	w("\tadd w16, w1, #16") // allocSize = n + 16-byte header
	emitAllocPresCall(w)
	w("\tadd x0, x16, #16")    // x0 = data ptr (past 16-byte header)
	w("\tstur w1, [x0, #-12]") // cap = n
	w("\tmov w11, #1")
	w("\tstur w11, [x0, #-8]") // rc = 1
	w("\tstur w1, [x0, #-4]")  // len = n
	// Zero the n data bytes (0 iterations when n==0).
	w("\tmov x12, x0") // cursor (x0 stays the return value)
	w("\tmov w13, w1") // count = n
	w(".Lssa_allocu8_zero:")
	w("\tcbz w13, .Lssa_allocu8_ret")
	w("\tstrb wzr, [x12], #1")
	w("\tsub w13, w13, #1")
	w("\tb .Lssa_allocu8_zero")
	w(".Lssa_allocu8_ret:")
	w("\tret")
}

// emitPrintHelper writes print(s): write the string's bytes to stdout (fd 1)
// followed by a single trailing newline — two write(2) syscalls. The arm64ssa
// string ABI passes the data pointer directly (byte length at [ptr-4]), so the
// helper reads the length in place and writes from the pointer with no header
// arithmetic. The newline is materialised on the stack rather than via a
// .rodata symbol so the helper is self-contained. Clobbers x0-x2/x8/x9 (all
// caller-saved; the emitter has already spilled any live-across values). print's
// return value is unused by Fern code, so it returns 0.
func emitPrintHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("print"))
	w("\tldur w2, [x0, #-4]") // len
	w("\tmov x1, x0")         // buf = data
	w("\tmov x0, #1")         // fd = stdout
	w("\tmov x8, #64")        // write(2)
	w("\tsvc #0")
	// Trailing newline: '\n' on the stack, one-byte write.
	w("\tsub sp, sp, #16")
	w("\tmov w9, #10")
	w("\tstrb w9, [sp]")
	w("\tmov x1, sp")
	w("\tmov x2, #1")
	w("\tmov x0, #1")
	w("\tmov x8, #64")
	w("\tsvc #0")
	w("\tadd sp, sp, #16")
	w("\tmov x0, xzr") // unused return
	w("\tret")
}

// emitWriteHelper writes write(s): print's no-newline sibling — a single write(2)
// of the string's bytes to stdout (fd 1). Same single-word string ABI (data
// pointer in x0, byte length at [ptr-4]). Leaf; unused return is 0.
func emitWriteHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("write"))
	w("\tldur w2, [x0, #-4]") // len
	w("\tmov x1, x0")         // buf = data
	w("\tmov x0, #1")         // fd = stdout
	w("\tmov x8, #64")        // write(2)
	w("\tsvc #0")
	w("\tmov x0, xzr") // unused return
	w("\tret")
}

// emitEprintHelper writes eprint(s): print's stderr sibling — the string's bytes
// then a trailing newline, both to fd 2. Two write(2) syscalls; the newline is
// materialised on the stack. Leaf; unused return is 0.
func emitEprintHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("eprint"))
	w("\tldur w2, [x0, #-4]") // len
	w("\tmov x1, x0")         // buf = data
	w("\tmov x0, #2")         // fd = stderr
	w("\tmov x8, #64")        // write(2)
	w("\tsvc #0")
	w("\tsub sp, sp, #16")
	w("\tmov w9, #10")
	w("\tstrb w9, [sp]")
	w("\tmov x1, sp")
	w("\tmov x2, #1")
	w("\tmov x0, #2")
	w("\tmov x8, #64")
	w("\tsvc #0")
	w("\tadd sp, sp, #16")
	w("\tmov x0, xzr") // unused return
	w("\tret")
}

// emitPutcharHelper writes putchar(c): write the low byte of x0 to stdout (fd 1).
// The byte is materialised on the stack so the kernel has a real address to read.
// Leaf; unused return is 0.
func emitPutcharHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("putchar"))
	w("\tsub sp, sp, #16")
	w("\tstrb w0, [sp]") // byte on the stack
	w("\tmov x1, sp")    // buf
	w("\tmov x2, #1")    // len = 1
	w("\tmov x0, #1")    // fd = stdout
	w("\tmov x8, #64")   // write(2)
	w("\tsvc #0")
	w("\tadd sp, sp, #16")
	w("\tmov x0, xzr") // unused return
	w("\tret")
}

// emitExitHelper writes exit(code): terminate the process immediately with the
// given status via the exit_group syscall (x8 = 94; the code is already in x0).
// It never returns, so there is no ret — but the IR still emits the post-call
// stack discipline at the call site, which is harmless because control never
// comes back. Leaf.
func emitExitHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("exit"))
	w("\tmov x8, #94") // exit_group; status already in x0
	w("\tsvc #0")
}

// emitStrbufResetHelper writes strbuf_reset(): zero the global string-builder
// length counter. Leaf; unused return is 0.
func emitStrbufResetHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("strbuf_reset"))
	w("\tadrp x0, %s", strbufLenSym)
	w("\tadd x0, x0, #:lo12:%s", strbufLenSym)
	w("\tstr xzr, [x0]")
	w("\tmov x0, xzr")
	w("\tret")
}

// emitStrbufAppendHelper writes strbuf_append(s): copy the single-word string's
// bytes (length at [s-4]) into the builder buffer past the current tail and bump
// the length counter. Unused return is 0.
func emitStrbufAppendHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("strbuf_append"))
	w("\tldur w2, [x0, #-4]") // w2 = append length (zero-extends into x2)
	w("\tadrp x3, %s", strbufLenSym)
	w("\tadd x3, x3, #:lo12:%s", strbufLenSym) // x3 = &len
	w("\tldr x4, [x3]")                        // x4 = current len
	w("\tadrp x5, %s", strbufDataSym)
	w("\tadd x5, x5, #:lo12:%s", strbufDataSym)
	w("\tadd x5, x5, x4") // x5 = dst = data + len
	w("\tmov x6, x0")     // src, clear of __ssa_bcopy's argument registers
	w("\tmov x7, x2")     // append length
	w("\tadd x4, x4, x2") // len += append length
	w("\tstr x4, [x3]")
	emitBcopyCall(w, "x5", "x6", "x7")
	w("\tmov x0, xzr")
	w("\tret")
}

// emitStrbufTakeHelper writes strbuf_take() -> string: bump-allocate a fresh
// single-word rc-headered string of the current builder length, copy the builder
// bytes into it, reset the counter, and return the new string. Leaf.
func emitStrbufTakeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("strbuf_take"))
	w("\tadrp x1, %s", strbufLenSym)
	w("\tadd x1, x1, #:lo12:%s", strbufLenSym) // x1 = &len
	w("\tldr x2, [x1]")                        // x2 = len
	// Bump-allocate len+8: rc=1@base, len@base+4, data@base+8.
	w("\tadrp x3, %s", heapPtrSym)
	w("\tadd x3, x3, #:lo12:%s", heapPtrSym) // x3 = &cursor
	w("\tldr x4, [x3]")
	w("\tadd x4, x4, #15")
	w("\tand x4, x4, #-16") // base
	w("\tadd x5, x2, #8")   // allocSize = len + 8
	w("\tadd x6, x4, x5")
	w("\tstr x6, [x3]") // bump
	emitHeapGuardCall(w)
	w("\tmov w7, #1")
	w("\tstr w7, [x4]")     // rc = 1
	w("\tstr w2, [x4, #4]") // len
	w("\tadd x8, x4, #8")   // x8 = data
	w("\tadrp x9, %s", strbufDataSym)
	w("\tadd x9, x9, #:lo12:%s", strbufDataSym) // x9 = builder buffer
	w("\tmov x10, x2")                          // len, clear of __ssa_bcopy's argument registers
	emitBcopyCall(w, "x8", "x9", "x10")
	w("\tstr xzr, [x1]") // reset len = 0
	w("\tmov x0, x8")    // return data pointer
	w("\tret")
}

// emitStrSliceHelper writes __str_slice(base, low, high) -> data: allocate a
// fresh length-prefixed string holding base[low:high]. Bounds-traps (exit 134)
// on low < 0, high > src_len, or low > high, matching the native helper. Like
// the other string helpers it inlines the bump allocation (no __fern_alloc call)
// into a fresh single-word rc-headered string (rc=1@base, len@base+4,
// data@base+8), and copies through __ssa_bcopy. low/high arrive as i32;
// they are sign-extended for the signed bound checks. x0=base, w1=low, w2=high;
// returns x0=data.
func emitStrSliceHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_slice"))
	w("\tsxtw x1, w1")        // low (signed)
	w("\tsxtw x2, w2")        // high (signed)
	w("\tldur w3, [x0, #-4]") // src_len (non-negative, zero-extends into x3)
	// Bounds checks: low < 0, high > src_len, low > high → trap.
	w("\tcmp x1, #0")
	w("\tb.lt .Lssa_strslice_trap")
	w("\tcmp x2, x3")
	w("\tb.gt .Lssa_strslice_trap")
	w("\tcmp x1, x2")
	w("\tb.gt .Lssa_strslice_trap")
	w("\tsub w4, w2, w1") // new_len = high - low
	// Bump-allocate new_len+8: rc=1@base, len@base+4, data@base+8.
	w("\tadrp x5, %s", heapPtrSym)
	w("\tadd x5, x5, #:lo12:%s", heapPtrSym) // x5 = &cursor
	w("\tldr x6, [x5]")
	w("\tadd x6, x6, #15")
	w("\tand x6, x6, #-16") // x6 = base (16-aligned)
	w("\tmov w7, #1")
	w("\tstr w7, [x6]")         // rc = 1
	w("\tstr w4, [x6, #4]")     // len = new_len
	w("\tadd x8, x6, #8")       // x8 = data
	w("\tadd x9, x8, w4, uxtw") // new cursor = data + new_len
	w("\tstr x9, [x5]")
	emitHeapGuardCall(w)
	// Copy new_len bytes from base+low (x0+x1) to data (x8).
	w("\tadd x10, x0, x1") // src = base + low
	w("\tmov w11, w4")     // new_len, zero-extended into x11
	emitBcopyCall(w, "x8", "x10", "x11")
	w("\tmov x0, x8") // return data
	w("\tret")
	w(".Lssa_strslice_trap:")
	w("\tmov x0, #134")
	w("\tmov x8, #94") // exit_group
	w("\tsvc #0")
}

// emitStringFromBytesHelper writes string_from_bytes_unchecked(bs) -> data: copy a u8[]
// payload into a fresh length-prefixed string and return its data pointer — the
// round-trip companion to s.bytes(). arm64ssa strings are single-word and
// rc-headered (rc=1@base+0, len@base+4, data@base+8 — the same layout ConstStr
// and __str_concat use), with no small-string inline optimisation, so this is a
// straight inline bump-allocate plus an __ssa_bcopy. bs is the input u8[] data pointer;
// its byte length is at [bs-4]. x0=bs; returns x0=data.
func emitStringFromBytesHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("string_from_bytes_unchecked"))
	w("\tldur w1, [x0, #-4]") // w1 = byte length of bs (zero-extends into x1)
	// Bump-allocate len+8: rc=1@base, len@base+4, data@base+8.
	w("\tadrp x2, %s", heapPtrSym)
	w("\tadd x2, x2, #:lo12:%s", heapPtrSym) // x2 = &cursor
	w("\tldr x3, [x2]")
	w("\tadd x3, x3, #15")
	w("\tand x3, x3, #-16") // x3 = base (16-aligned)
	w("\tmov w4, #1")
	w("\tstr w4, [x3]")     // rc = 1
	w("\tstr w1, [x3, #4]") // len
	w("\tadd x5, x3, #8")
	w("\tadd x5, x5, x1") // new cursor = base + 8 + len
	w("\tstr x5, [x2]")
	emitHeapGuardCall(w)
	w("\tadd x6, x3, #8") // x6 = data
	// Copy len bytes from bs (x0) to data (x6).
	w("\tmov x7, x0") // src
	w("\tmov x8, x1") // len
	emitBcopyCall(w, "x6", "x7", "x8")
	w("\tmov x0, x6") // return data
	w("\tret")
}

// emitArrCowInplaceHelper writes __fern_arr_cow_inplace(arr, stride) -> buf —
// the copy-on-write helper behind `arr[i] = v`. Fast path: rc == 1 → the array
// is uniquely held, return it unchanged for an in-place store. Slow path (rc >
// 1, shared): decrement arr's rc (taking the caller's reference as we copy;
// skip a static sentinel whose rc word has the high bit set), bump-allocate a
// fresh buffer with the SAME cap+len, copy the payload through __ssa_bcopy, write
// rc=1 on the new header, and return the new data pointer. Like
// __fern_arr_push_grow it inlines the bump allocation (the native helper calls
// __fern_alloc). x0=arr, w1=stride; returns x0=buf.
func emitArrCowInplaceHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_arr_cow_inplace"))
	// Fast path: rc == 1 → return arr unchanged.
	w("\tldur w2, [x0, #-8]") // rc
	w("\tcmp w2, #1")
	w("\tb.ne .Lssa_cow_slow")
	w("\tret")
	w(".Lssa_cow_slow:")
	w("\tldur w3, [x0, #-4]")  // w3 = len
	w("\tldur w4, [x0, #-12]") // w4 = cap
	// Decrement arr's rc; skip a static sentinel (rc high bit set).
	w("\tldur w5, [x0, #-8]")
	w("\ttbnz w5, #31, .Lssa_cow_skipdec")
	w("\tsub w5, w5, #1")
	w("\tstur w5, [x0, #-8]")
	w(".Lssa_cow_skipdec:")
	// headerBytes = max(16, stride); allocSize = headerBytes + cap*stride.
	w("\tmov w6, #16")
	w("\tcmp w1, w6")
	w("\tcsel w6, w1, w6, ge") // w6 = headerBytes
	w("\tmadd w7, w4, w1, w6") // w7 = allocSize
	w("\tmov w16, w7")
	emitAllocPresCall(w)
	w("\tmov x9, x16")           // x9 = base
	w("\tadd x11, x9, w6, uxtw") // x11 = new_data = base + headerBytes
	w("\tsub x12, x11, #12")
	w("\tstr w4, [x12]") // cap
	w("\tmov w13, #1")
	w("\tstur w13, [x11, #-8]") // rc = 1
	w("\tstur w3, [x11, #-4]")  // len
	// Copy len*stride bytes from arr (x0) to new_data (x11).
	w("\tmul w14, w3, w1") // nbytes (a w-destination zero-extends into x14)
	w("\tmov x15, x0")     // src, clear of __ssa_bcopy's argument registers
	emitBcopyCall(w, "x11", "x15", "x14")
	w("\tmov x0, x11") // return new_data
	w("\tret")
}

// emitArrCowInplaceElemHelper writes the element-retaining
// __fern_arr_cow_inplace_ptr(arr, stride) -> buf: the scalar helper's fast
// path and copy, then `elemInc` on every element the fresh buffer now shares
// with the receiver, so each array owns its own reference. A raw copy leaves
// the elements at unchanged count, and a consuming match one level down then
// reads a child both arrays reach as unique and rewrites it in place — the
// snapshot of a persistent vector changing under a `.with`. x0=arr, w1=stride;
// returns x0=buf.
func emitArrCowInplaceElemHelper(name, elemInc, tag string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		lbl := func(suffix string) string { return ".Lssa_" + tag + "_" + suffix }
		w("")
		w("%s:", fnLabel(name))
		w("\tldur w2, [x0, #-8]") // rc
		w("\tcmp w2, #1")
		w("\tb.ne %s", lbl("slow"))
		w("\tret")
		w("%s:", lbl("slow"))
		w("\tstp x29, x30, [sp, #-48]!")
		w("\tmov x29, sp")
		w("\tstp x19, x20, [sp, #16]")
		w("\tstp x21, x22, [sp, #32]")
		w("\tmov x20, x1") // stride
		w("\tbl %s", fnLabel("__fern_arr_cow_inplace"))
		w("\tmov x19, x0")          // buf
		w("\tldur w21, [x19, #-4]") // len
		w("\tmov x22, #0")          // i
		w("%s:", lbl("loop"))
		w("\tcmp w22, w21")
		w("\tb.ge %s", lbl("done"))
		w("\tmadd x0, x22, x20, x19") // &buf[i]
		w("\tldr x0, [x0]")           // element
		w("\tbl %s", fnLabel(elemInc))
		w("\tadd x22, x22, #1")
		w("\tb %s", lbl("loop"))
		w("%s:", lbl("done"))
		w("\tmov x0, x19")
		w("\tldp x21, x22, [sp, #32]")
		w("\tldp x19, x20, [sp, #16]")
		w("\tldp x29, x30, [sp], #48")
		w("\tret")
	}
}

// emitFunc writes one function. It emits the body twice: the first pass finds
// which callee-saved registers the body actually touches, the second emits the
// prologue saves and epilogue restores for exactly those.
//
// Reading the set back off the emitted text is what makes it complete, the same
// argument x86_64ssa's calleeSavedIn makes: the alternative is a list of every
// Program field and line helper that can name a register, and the two failure
// directions are not symmetric — an over-wide set costs a store and a load,
// while a missed register is handed back to the caller clobbered with nothing
// failing until unrelated code reads it. The second pass adds only saves and
// restores of registers already in the set, so the set is stable.
func emitFunc(w func(string, ...any), name string, p *x86.Program, numAlloc int, strLabels map[string]string, sentLabels map[int64]string, fnIndex map[string]int) error {
	var probe strings.Builder
	pw := func(format string, args ...any) {
		fmt.Fprintf(&probe, format, args...)
		probe.WriteByte('\n')
	}
	if err := emitFuncBody(pw, name, p, numAlloc, nil, strLabels, sentLabels, fnIndex); err != nil {
		return err
	}
	return emitFuncBody(w, name, p, numAlloc, calleeSavedIn(probe.String(), p.NumRegFile), strLabels, sentLabels, fnIndex)
}

// calleeSavedIn returns, in ascending abstract-index order, the callee-saved
// registers the emitted text mentions. Tokens keep `_` and `.` so a label like
// `.Lssa_x19_ok` stays one word rather than decomposing into a register name.
func calleeSavedIn(asm string, numRegFile int) []int {
	seen := map[string]bool{}
	for _, tok := range armRegTokenRe.FindAllString(asm, -1) {
		seen[tok] = true
	}
	var out []int
	for r := 0; r < numRegFile && r < len(armX); r++ {
		if armCalleeSaved(r) && (seen[armX[r]] || seen[armW[r]]) {
			out = append(out, r)
		}
	}
	return out
}

// armRegTokenRe splits assembly into identifier-ish tokens, treating `_` and `.`
// as word characters so a label name cannot decompose into a register name.
var armRegTokenRe = regexp.MustCompile(`[A-Za-z_.][A-Za-z0-9_.]*`)

// maxFrameBytes is the largest frame the two-instruction adjustment below can
// reach: both halves of an ADD/SUB immediate are 12 bits, one of them shifted
// left by 12.
const maxFrameBytes = 4095<<12 | 4095

// spAdjustLines moves sp by n bytes — "sub" to open a frame, "add" to close it.
//
// The immediate is 12 bits with an optional 12-bit left shift, so anything past
// 4095 bytes needs two instructions: the 4096-multiple part and the remainder.
// A single self-host function reaches 20 KB of spill slots, which is where this
// stopped being hypothetical.
func spAdjustLines(op string, n int) ([]string, error) {
	if n > maxFrameBytes {
		return nil, fmt.Errorf("arm64ssa: frame of %d bytes exceeds the %d an sp adjustment can reach", n, maxFrameBytes)
	}
	if hi := n &^ 0xfff; hi != 0 {
		// n is 16-aligned and hi is a multiple of 4096, so the remainder is too.
		out := []string{fmt.Sprintf("%s sp, sp, #%d", op, hi)}
		if lo := n & 0xfff; lo != 0 {
			out = append(out, fmt.Sprintf("%s sp, sp, #%d", op, lo))
		}
		return out, nil
	}
	return []string{fmt.Sprintf("%s sp, sp, #%d", op, n)}, nil
}

// frameLayout is where everything a function body addresses lives, in 8-byte
// slots measured from a stack pointer that does not move for the body's
// lifetime:
//
//	[0, outArgs)          outgoing stack arguments
//	[slotBase, +NumSlots) spill slots and phi temps
//	[callSaveBase, ...)   the call-save area
//	[csBase, ...)         one slot per callee-saved register the body uses
//	lrSlot                the saved link register, when the function calls
//
// The outgoing arguments have to be at the very bottom. A callee reads its
// stack arguments from the sp it is entered with, which is the caller's own sp
// — so what the caller writes at [sp, #0] upward is exactly what the callee
// finds, and everything else the caller owns has to sit above it.
type frameLayout struct {
	outArgs      int
	slotBase     int
	callSaveBase int
	csBase       int
	lrSlot       int
	bytes        int // the whole frame, 16-aligned — what the prologue subtracts
}

// slot is the byte offset of spill/phi slot n.
func (f frameLayout) slot(n int) int { return 8 * (f.slotBase + n) }

// outArg is where the caller writes the argument that lands in the callee's
// stack-argument position k (that is, its (argRegCount+k)'th parameter).
func (f frameLayout) outArg(k int) int { return 8 * k }

// inArg is where the callee finds that same argument. The caller wrote it at
// the caller's sp, which this frame sits entirely below.
func (f frameLayout) inArg(k int) int { return f.bytes + 8*k }

// emitFuncBody writes one function: its label, a stack frame (spill slots, plus
// a call-save area, one slot per callee-saved register in csSaved, and a
// saved-x30 slot when the function makes calls), each block's straight-line body
// under a namespaced label, and the terminators. The stack pointer stays fixed
// for the whole body — call-crossing registers are preserved in the reserved
// call-save area rather than by moving sp — so every slot access is a stable
// sp-relative offset.
func emitFuncBody(w func(string, ...any), name string, p *x86.Program, numAlloc int, csSaved []int, strLabels map[string]string, sentLabels map[int64]string, fnIndex map[string]int) error {
	label := fnLabel(name)
	call := funcHasCall(p)

	// The call-save area is as wide as the largest single call's save set, not
	// the whole allocatable file, since a call only ever writes save-set slot 0
	// upward. See frameLayout for the rest.
	fr := frameLayout{outArgs: maxOutArgSlots(p)}
	fr.slotBase = fr.outArgs
	fr.callSaveBase = fr.slotBase + p.NumSlots
	fr.csBase = fr.callSaveBase + maxCallSaveSlots(p, numAlloc)
	fr.lrSlot = fr.csBase + len(csSaved)
	nslots := fr.lrSlot
	if call {
		nslots = fr.lrSlot + 1
	}
	fr.bytes = align16(8 * nslots)
	scratch := p.NumRegFile - 1 // result-capture scratch; above the allocatable file

	w("%s:", label)
	if fr.bytes > 0 {
		lines, err := spAdjustLines("sub", fr.bytes)
		if err != nil {
			return err
		}
		for _, l := range lines {
			w("\t%s", l)
		}
	}
	if call {
		w("\tstr x30, [sp, #%d]", 8*fr.lrSlot)
	}
	for _, l := range slotSaveLines(csSaved, fr.csBase) {
		w("\t%s", l)
	}
	// Parameter ABI: move each incoming argument register into its param's home.
	// Must follow the frame setup (slot-homed params store to [sp]) and the
	// callee-saved saves (a param's home may be one of them).
	for _, l := range paramMoveLines(p.ParamLocs, fr, scratch) {
		w("\t%s", l)
	}

	// teardown restores the link register and the callee-saved registers, then
	// drops the frame. Emitted after the return value is in place, so restoring
	// a callee-saved register that held it cannot clobber it.
	teardown := func() {
		if call {
			w("\tldr x30, [sp, #%d]", 8*fr.lrSlot)
		}
		for _, l := range slotRestoreLines(csSaved, fr.csBase) {
			w("\t%s", l)
		}
		if fr.bytes > 0 {
			// The prologue already proved this frame fits.
			lines, _ := spAdjustLines("add", fr.bytes)
			for _, l := range lines {
				w("\t%s", l)
			}
		}
	}

	ret := func(reg int) {
		if reg != 0 {
			w("\tmov x0, %s", xreg(reg))
		}
		teardown()
		w("\tret")
	}

	order := layoutOrder(p)
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
		w(".L%s_b%d:", label, bi)
		insts, fuseCC, fuseLeft, fuseRight := fuseBranchCmp(blk)
		movSkip, accLeft := deadAccMoves(insts)

		for ii, in := range insts {
			if movSkip[ii] {
				continue
			}
			left := accLeft[ii]
			if left < 0 {
				left = in.Dst
			}
			if in.Op == x86.Call || in.Op == x86.CallPair {
				// An array index is address arithmetic, not a call: inline it
				// rather than making the allocator spill around it. The seed
				// keeps the bounds-check label unique per site.
				if lines, ok := inlinePokeLines(in, fr, numAlloc); ok {
					for _, l := range lines {
						w("\t%s", l)
					}
					continue
				}
				if lines, ok := inlineArrIdxLines(in, fr, numAlloc, fmt.Sprintf("%s_b%d_i%d", label, bi, ii)); ok {
					for _, l := range lines {
						if strings.HasSuffix(l, ":") {
							w("%s", l)
						} else {
							w("\t%s", l)
						}
					}
					continue
				}
				lines, err := callLines(in, numAlloc, scratch, fr)
				if err != nil {
					return err
				}
				for _, l := range lines {
					w("\t%s", l)
				}
				continue
			}
			if in.Op == x86.CallIndirect {
				lines, err := callIndirectLines(in, numAlloc, fr)
				if err != nil {
					return err
				}
				for _, l := range lines {
					w("\t%s", l)
				}
				continue
			}
			if in.Op == x86.MakeEnv || in.Op == x86.MakeClosure {
				lines, err := closureLines(in, numAlloc, fnIndex, fr)
				if err != nil {
					return err
				}
				for _, l := range lines {
					w("\t%s", l)
				}
				continue
			}
			if in.Op == x86.ConstVtable {
				tr, co := splitDynPair(in.Str)
				// Materialise the static vtable cell's address (PC-relative page +
				// offset), the same shape ConstStr / the fn-table use.
				w("\tadrp %s, %s", xreg(in.Dst), dynVtableLabel(tr, co))
				w("\tadd %s, %s, #:lo12:%s", xreg(in.Dst), xreg(in.Dst), dynVtableLabel(tr, co))
				continue
			}
			if in.Op == x86.BoxDyn {
				for _, l := range boxDynLines(in, numAlloc, fr) {
					w("\t%s", l)
				}
				continue
			}
			if in.Op == x86.CallDyn {
				lines, err := callDynLines(in, numAlloc, fr)
				if err != nil {
					return err
				}
				for _, l := range lines {
					w("\t%s", l)
				}
				continue
			}
			if in.Op == x86.ConstStr {
				lbl, ok := strLabels[in.Str]
				if !ok {
					return fmt.Errorf("arm64ssa: ConstStr %q has no .rodata label", in.Str)
				}
				// Materialise the string's data pointer (the label; its 8-byte header
				// sits just before it) via PC-relative page + offset.
				w("\tadrp %s, %s", xreg(in.Dst), lbl)
				w("\tadd %s, %s, #:lo12:%s", xreg(in.Dst), xreg(in.Dst), lbl)
				continue
			}
			if in.Op == x86.EnumSentinel {
				lbl, ok := sentLabels[in.Imm]
				if !ok {
					return fmt.Errorf("arm64ssa: EnumSentinel tag %d has no .rodata cell", in.Imm)
				}
				// Materialise the shared sentinel cell's address (tag at [ptr+0]).
				w("\tadrp %s, %s", xreg(in.Dst), lbl)
				w("\tadd %s, %s, #:lo12:%s", xreg(in.Dst), xreg(in.Dst), lbl)
				continue
			}
			lines, err := asmInst(in, left, scratch, fr)
			if err != nil {
				return err
			}
			for _, l := range lines {
				w("\t%s", l)
			}
		}
		if fuseLeft >= 0 {
			w("\tcmp %s, %s", xreg(fuseLeft), xreg(fuseRight))
		}
		switch blk.Term.Kind {
		case x86.TRet:
			ret(blk.Term.RetReg)
		case x86.TRetPair:
			// AArch64 pair-return convention: tag in x0, payload in x1. A home may
			// already be x0/x1, so resolve the two moves as a parallel copy before
			// the frame teardown.
			for _, l := range resolveRegMoves(pairRetMoves(blk.Term.RetReg, blk.Term.RetReg2)) {
				w("\t%s", l)
			}
			teardown()
			w("\tret")
		case x86.TJmp:
			if blk.Term.Target != nextInLayout[bi] {
				w("\tb .L%s_b%d", label, blk.Term.Target)
			}
		case x86.TBrIf:
			taken, notTaken := "cbnz", "cbz"
			if fuseCC != "" {
				inv, ok := invCond(fuseCC)
				if !ok {
					return fmt.Errorf("arm64ssa: no inverse for condition %q", fuseCC)
				}
				taken, notTaken = "b."+fuseCC, "b."+inv
			}
			cond := ""
			if fuseCC == "" {
				cond = xreg(blk.Term.CondReg) + ", "
			}
			switch nxt := nextInLayout[bi]; {
			case blk.Term.True == nxt:
				// The taken arm follows, so test the inverse and fall into it.
				w("\t%s %s.L%s_b%d", notTaken, cond, label, blk.Term.False)
			case blk.Term.False == nxt:
				w("\t%s %s.L%s_b%d", taken, cond, label, blk.Term.True)
			default:
				w("\t%s %s.L%s_b%d", taken, cond, label, blk.Term.True)
				w("\tb .L%s_b%d", label, blk.Term.False)
			}
		default:
			return fmt.Errorf("arm64ssa: unsupported terminator %d", blk.Term.Kind)
		}
	}
	return nil
}

// maxCallSaveSlots is the width of the call-save area: the largest number of
// caller-saved registers any one call in p has to preserve.
// maxOutArgSlots is the width of the outgoing-argument area: the most stack
// arguments any one call in p passes. A call's stack arguments are the ones
// past the register half, and every call writes that area from slot 0 upward,
// so the widest single call sizes it.
func maxOutArgSlots(p *x86.Program) int {
	most := 0
	count := func(n int) {
		if n-argRegCount > most {
			most = n - argRegCount
		}
	}
	for _, blk := range p.Blocks {
		for _, in := range blk.Insts {
			switch in.Op {
			case x86.Call, x86.CallPair:
				count(len(in.ArgLocs))
			case x86.CallIndirect:
				count(len(in.ArgLocs) + 1) // the env rides as the final argument
			case x86.CallDyn:
				count(len(in.ArgLocs) - 1) // the last operand is the vtable
			}
		}
	}
	return most
}

func maxCallSaveSlots(p *x86.Program, numAlloc int) int {
	most := 0
	for _, blk := range p.Blocks {
		for _, in := range blk.Insts {
			switch in.Op {
			case x86.Call, x86.CallPair:
				// The same narrowing callLines applies, so a frame reserves the
				// slots its calls will use rather than the ones they would have.
				saved := liveAcrossToSave(callSavedSet(in, numAlloc), ir.CodegenAlias(in.Callee), len(in.ArgLocs))
				if n := len(saved); n > most {
					most = n
				}
			case x86.CallIndirect, x86.CallDyn:
				if n := len(callSavedSet(in, numAlloc)); n > most {
					most = n
				}
			}
		}
	}
	return most
}

// funcHasCall reports whether any block of p contains a direct call.
func funcHasCall(p *x86.Program) bool {
	for _, blk := range p.Blocks {
		for _, in := range blk.Insts {
			if in.Op == x86.Call || in.Op == x86.CallPair || in.Op == x86.CallIndirect || in.Op == x86.CallDyn {
				return true
			}
		}
	}
	return false
}

// usesCallIndirect reports whether any program dispatches a closure (so the
// function-address table is emitted only when needed).
func usesCallIndirect(progs map[string]*x86.Program) bool {
	for _, p := range progs {
		for _, blk := range p.Blocks {
			for _, in := range blk.Insts {
				if in.Op == x86.CallIndirect {
					return true
				}
			}
		}
	}
	return false
}

// arrIdxInline maps an array-index helper onto its element-stride shift and
// whether it bounds-checks. These are the calls the IR emits for `a[i]`
// (internal/ir), and every one of them is four instructions or fewer of address
// arithmetic — so the call machinery around them costs more than the work. The
// stack-machine backend has always inlined them (emitInlineIdxHelper in
// internal/codegen/arm64); this is the SSA side of the same decision.
//
// __str_idx is absent on purpose: two-word strings make it a different shape,
// not an address compute.
var arrIdxInline = map[string]struct {
	shift   int
	checked bool
}{
	"__arr_idx":       {2, true}, // stride 4 (i32)
	"__arr_idx_1":     {0, true}, // stride 1 (byte array)
	"__arr_idx_8":     {3, true}, // stride 8 (i64 / pointer)
	"__arr_idx_16":    {4, true}, // stride 16 (two-word string[])
	"__arr_idx_nc":    {2, false},
	"__arr_idx_1_nc":  {0, false},
	"__arr_idx_8_nc":  {3, false},
	"__arr_idx_16_nc": {4, false},
}

// pokeInline maps each raw-memory intrinsic core/map.fern is written against
// onto the one instruction it is. mnem is the load/store mnemonic (empty for
// __ptr_width's constant), wide selects an x-register operand over a w one,
// store writes ArgLocs[1] through ArgLocs[0] and yields nothing, and off is the
// displacement — negative for a string's length field, which is why that one
// takes the unscaled ldur form.
//
// core/map.fern reaches its kv buffer through these rather than through typed
// field access, because the buffer is one untyped allocation whose layout
// depends on the target's pointer width — so a map lookup runs several of them
// per probe. The stack-machine backends inline them at the call site too.
var pokeInline = map[string]struct {
	mnem  string
	wide  bool
	store bool
	off   int
}{
	"__load_i32":  {mnem: "ldr"},
	"__load_u8":   {mnem: "ldrb"},
	"__load_i64":  {mnem: "ldr", wide: true},
	"__load_ptr":  {mnem: "ldr", wide: true},
	"__str_len":   {mnem: "ldur", off: -4},
	"__store_i32": {mnem: "str", store: true},
	"__store_i64": {mnem: "str", wide: true, store: true},
	"__store_ptr": {mnem: "str", wide: true, store: true},
	"__ptr_width": {},
}

// inlinePokeLines renders a raw-memory intrinsic as that instruction, or reports
// false when the callee is something else. Same trade as inlineArrIdxLines: the
// cost was never the `bl` but the caller-saves the allocator plants around it.
//
// Each case reproduces the helper body exactly, the width included — a load
// through a w-register leaves the value zero-extended and the trailing maskFix
// sign-extends it, which is what the call sequence did with the helper's result.
func inlinePokeLines(in x86.Inst, fr frameLayout, numAlloc int) ([]string, bool) {
	form, ok := pokeInline[ir.CodegenAlias(in.Callee)]
	if !ok || in.Op != x86.Call {
		return nil, false
	}
	s0, s1 := numAlloc, numAlloc+1
	var out []string
	materialise := func(l x86.Loc, tmp int) int {
		if l.IsReg {
			return l.Reg
		}
		out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(tmp), fr.slot(l.Slot)))
		return tmp
	}
	operand := func(r int) string {
		if form.wide {
			return xreg(r)
		}
		return wreg(r)
	}
	switch {
	case form.mnem == "": // __ptr_width(): a constant, no operands
		if len(in.ArgLocs) != 0 {
			return nil, false
		}
		out = append(out, fmt.Sprintf("mov %s, #8", wreg(in.Dst)))
	case form.store:
		if len(in.ArgLocs) != 2 {
			return nil, false
		}
		addr := materialise(in.ArgLocs[0], s0)
		val := materialise(in.ArgLocs[1], s1)
		// Void, so there is no result to place and no width to fix.
		return append(out, fmt.Sprintf("%s %s, [%s]", form.mnem, operand(val), xreg(addr))), true
	default:
		if len(in.ArgLocs) != 1 {
			return nil, false
		}
		addr := materialise(in.ArgLocs[0], s0)
		if form.off != 0 {
			out = append(out, fmt.Sprintf("%s %s, [%s, #%d]", form.mnem, operand(in.Dst), xreg(addr), form.off))
		} else {
			out = append(out, fmt.Sprintf("%s %s, [%s]", form.mnem, operand(in.Dst), xreg(addr)))
		}
	}
	return append(out, maskFix(in.Dst, in.W)...), true
}

// inlineArrIdxLines renders an array-index call as the address compute it is,
// or reports false when the callee is something else.
//
// The saving is not the `bl`. A call makes the allocator spill every
// caller-saved register holding a value live across it, and indexing sits in
// the innermost loop of anything that walks an array — 22 of the 75 calls in
// cmp.sort's monomorphised body were this helper, against flat's zero.
//
// It reproduces the helper exactly, including the detail that the bounds check
// is what makes the wide add safe: `cmp w` rejects a negative index as a huge
// unsigned, so the full x-register add below only ever sees an index whose top
// half is clear. The `_nc` variants keep the helper's own (unchecked) semantics.
func inlineArrIdxLines(in x86.Inst, fr frameLayout, numAlloc int, seed string) ([]string, bool) {
	form, ok := arrIdxInline[ir.CodegenAlias(in.Callee)]
	if !ok || in.Op != x86.Call || len(in.ArgLocs) != 2 {
		return nil, false
	}
	// s0..s2 are the scratch pool, above the allocatable file: a slot-homed
	// argument lands in one, and the length read needs a third register that is
	// neither operand nor the destination.
	s0, s1, s2 := numAlloc, numAlloc+1, numAlloc+2
	var out []string
	materialise := func(l x86.Loc, tmp int) int {
		if l.IsReg {
			return l.Reg
		}
		out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(tmp), fr.slot(l.Slot)))
		return tmp
	}
	base := materialise(in.ArgLocs[0], s0)
	idx := materialise(in.ArgLocs[1], s1)
	if form.checked {
		ok := fmt.Sprintf(".Lssa_idx_%s_ok", seed)
		out = append(out,
			fmt.Sprintf("ldur %s, [%s, #-4]", wreg(s2), xreg(base)),
			fmt.Sprintf("cmp %s, %s", wreg(idx), wreg(s2)),
			fmt.Sprintf("b.lo %s", ok),
			"mov x0, #134",
			"mov x8, #94", // exit_group
			"svc #0",
			ok+":",
		)
	}
	// The add reads both operands and writes the destination, so a destination
	// that already IS one of them is fine: nothing is clobbered before its use.
	if form.shift == 0 {
		out = append(out, fmt.Sprintf("add %s, %s, %s", xreg(in.Dst), xreg(base), xreg(idx)))
	} else {
		out = append(out, fmt.Sprintf("add %s, %s, %s, lsl #%d", xreg(in.Dst), xreg(base), xreg(idx), form.shift))
	}
	return append(out, maskFix(in.Dst, in.W)...), true
}

// helperClobbers is, per runtime helper this file emits, the caller-saved
// allocatable registers a call to it can leave changed.
//
// It is derived from the emitted body rather than declared beside it, and
// over-approximated in the safe direction twice over: a register the body so
// much as MENTIONS counts as clobbered even when it saves and restores it, and
// a branch to anything that is not another helper — a compiled module function,
// which uses the whole file — counts as all of them. The callee-saved half never
// appears: TestRuntimeHelpersPreserveCalleeSaved makes a helper that touches one
// save and restore it.
//
// What it buys is at the call sites. The rc primitives touch x0 and x1 and
// nothing else, so a value the allocator homed in x2..x11 and kept live across
// an inc, a dec or an is_unique no longer costs a store and a reload at each
// one — and there are 70 to 86 sites of each in a program built on a persistent
// collection. `__fern_box_free`, a bare `ret` while this heap does not reclaim,
// stops costing anything at all.
var helperClobbers = sync.OnceValue(computeHelperClobbers)

// renderHelper returns a helper body's assembly text.
func renderHelper(emit func(w func(string, ...any))) string {
	var b strings.Builder
	emit(func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	})
	return b.String()
}

// helperBranchTargets returns the labels a body branches to, `bl` and the `b`
// of a tail call alike — a tail call hands its callee's clobbers straight back
// to us. Local `.L` labels are branches within the body, not calls.
func helperBranchTargets(asm string) []string {
	re := regexp.MustCompile(`(?m)\b(?:bl|b)\s+([A-Za-z_.][\w.]*)`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(asm, -1) {
		if !strings.HasPrefix(m[1], ".") {
			out = append(out, m[1])
		}
	}
	return out
}

// branchesIndirectly reports whether a body reaches a target held in a register
// (`blr` / `br`), which helperBranchTargets cannot name.
func branchesIndirectly(asm string) bool {
	return regexp.MustCompile(`(?m)^\s*(blr|br)\b`).MatchString(asm)
}

func computeHelperClobbers() map[string][]bool {
	// The two routines a body reaches by their bare labels rather than through
	// fnLabel, so a branch to either resolves like any other helper.
	bodies := map[string]string{
		heapGuardSym: renderHelper(emitHeapGuard),
		bcopySym:     renderHelper(emitBcopy),
		// Preserves every register it touches (that is its whole job), so a
		// body reaching it inherits nothing; reading its text would say x0..x11.
		allocPresSym: "",
	}
	for name, emit := range runtimeHelperEmitters {
		bodies[fnLabel(name)] = renderHelper(emit)
	}
	all := make([]bool, firstCalleeSaved)
	for r := range all {
		all[r] = true
	}
	sets := map[string][]bool{}
	for label, asm := range bodies {
		set := make([]bool, firstCalleeSaved)
		if branchesIndirectly(asm) {
			// The target is a register, so there is no body to read: assume the
			// worst rather than let a future helper narrow a set it should not.
			copy(set, all)
			sets[label] = set
			continue
		}
		for r := range set {
			set[r] = mentionsReg(asm, armX[r]) || mentionsReg(asm, armW[r])
		}
		sets[label] = set
	}
	// Fixed point over the call graph: a body inherits every callee's set, and
	// a callee that is not a helper of this file is a compiled function.
	for changed := true; changed; {
		changed = false
		for label, asm := range bodies {
			for _, target := range helperBranchTargets(asm) {
				callee, known := sets[target]
				if !known {
					callee = all
				}
				for r, c := range callee {
					if c && !sets[label][r] {
						sets[label][r] = true
						changed = true
					}
				}
			}
		}
	}
	return sets
}

// mentionsReg reports whether asm names a register, as an operand or anywhere
// else. Word-bounded, so the `x1` inside an immediate like #0x10000 is not one.
func mentionsReg(asm, reg string) bool {
	return regexp.MustCompile(`\b` + reg + `\b`).MatchString(asm)
}

// liveAcrossToSave narrows the allocator's live-across set to the registers this
// call can actually disturb: what the callee clobbers, plus the argument
// registers the caller's own parallel move writes on the way in (the saves
// precede that move, so they protect against it too). A callee this file does
// not emit — a compiled module function — is assumed to use every one.
func liveAcrossToSave(saved []int, callee string, args int) []int {
	clob, known := helperClobbers()[fnLabel(callee)]
	if !known {
		return saved
	}
	if args > argRegCount {
		args = argRegCount
	}
	out := make([]int, 0, len(saved))
	for _, r := range saved {
		// A register past the caller-saved run is one the set says nothing
		// about, so it keeps its save.
		if r < args || r >= len(clob) || clob[r] {
			out = append(out, r)
		}
	}
	return out
}

// callLines renders a direct call under the AArch64 PCS. The CALLER-SAVED
// registers holding values live across the call (in.SaveRegs, computed by the
// allocator) are spilled to the reserved call-save area — sp stays fixed, so
// those saves and the arg-move slot loads share the same stable offsets. A
// value the allocator steered into the callee-saved half needs nothing here:
// the callee restores it, and this function's prologue covers its own use of
// the register. Arguments go into x0..x7 as a parallel register copy (reg-homed)
// plus slot loads; the result (x0) is captured into the scratch register —
// above the allocatable file, never in the save set — before the saved
// registers are restored, then placed into the destination.
func callLines(in x86.Inst, numAlloc, scratch int, fr frameLayout) ([]string, error) {
	saved := liveAcrossToSave(callSavedSet(in, numAlloc), ir.CodegenAlias(in.Callee), len(in.ArgLocs))
	// s0 — the first scratch register — stages the pair-return payload. It is
	// above the allocatable file (never in the save set) and distinct from the
	// result-capture scratch (s3), so neither the restores nor the tag placement
	// clobber it. AArch64 returns a pair in x0 (tag) / x1 (payload).
	s0 := numAlloc
	// The result registers can be written before the restores — skipping the
	// staging scratch entirely — exactly when the restores do not write them.
	// The allocator cannot put a result and a value live ACROSS the same call in
	// one register (their intervals overlap), so this holds for every call; the
	// check is what keeps that an optimisation rather than a load-bearing
	// assumption about a pass in another package.
	direct := !inSaveSet(saved, in.Dst) && (in.Op != x86.CallPair || !inSaveSet(saved, in.Dst2))
	var out []string
	out = append(out, slotSaveLines(saved, fr.callSaveBase)...)
	out = append(out, argMoveLines(in.ArgLocs, fr, scratch)...)
	// ir.CodegenAlias resolves a Map/MapIter call target onto the stdlib `_impl`
	// that implements it. Applied HERE, where the callee becomes a label, so
	// there is no path that emits the unaliased name (#6609). Its twin is the
	// alias map handed to LiveFunctionsWithAliases in buildArm64SSA — miss
	// either and the assembler reports the same dangling label.
	out = append(out, fmt.Sprintf("bl %s", fnLabel(ir.CodegenAlias(in.Callee))))
	restore := func() {
		out = append(out, slotRestoreLines(saved, fr.callSaveBase)...)
	}
	if direct {
		// AArch64 returns in x0 (tag) / x1 (payload), which are abstract registers
		// 0 and 1 — so delivering a pair into its destinations is a parallel copy
		// over abstract indices, and resolveRegMoves drops the self-moves a
		// destination that already IS x0/x1 produces.
		moves := [][2]int{{in.Dst, 0}}
		if in.Op == x86.CallPair {
			moves = append(moves, [2]int{in.Dst2, 1})
		}
		out = append(out, resolveRegMoves(moves)...)
		restore()
		out = append(out, maskFix(in.Dst, in.W)...)
		return out, nil
	}
	out = append(out, fmt.Sprintf("mov %s, x0", xreg(scratch))) // capture tag / result
	if in.Op == x86.CallPair {
		out = append(out, fmt.Sprintf("mov %s, x1", xreg(s0))) // capture payload
	}
	restore()
	out = append(out, fmt.Sprintf("mov %s, %s", xreg(in.Dst), xreg(scratch))) // place tag / result
	out = append(out, maskFix(in.Dst, in.W)...)
	if in.Op == x86.CallPair {
		// Placed after the tag (whose home may be the payload's capture reg s3) so
		// the tag is out of s3 before Dst2 (typically s3) is written.
		out = append(out, fmt.Sprintf("mov %s, %s", xreg(in.Dst2), xreg(s0))) // place payload
	}
	return out, nil
}

// inSaveSet reports whether the call-save set contains register r — i.e. whether
// a restore writes it after the call returns.
func inSaveSet(saved []int, r int) bool {
	for _, s := range saved {
		if s == r {
			return true
		}
	}
	return false
}

// pairRetMoves builds the parallel-copy move list that places the pair-return
// tag in x0 and payload in x1 (either home may already sit in x0/x1). Fed to
// resolveRegMoves, which orders the copies and breaks a swap cycle with eor.
func pairRetMoves(tagReg, payReg int) [][2]int {
	var moves [][2]int
	if tagReg != 0 {
		moves = append(moves, [2]int{0, tagReg})
	}
	if payReg != 1 {
		moves = append(moves, [2]int{1, payReg})
	}
	return moves
}

// maxPairOffset is the deepest slot an stp/ldp can reach: the pair forms scale a
// signed 7-bit immediate by 8, so [sp, #504] is the last one.
const maxPairOffset = 504

// slotSaveLines and slotRestoreLines emit the stores and loads that move regs
// through the contiguous 8-byte slots starting at slotBase.
//
// Consecutive slots are what stp/ldp address, so two registers cost one
// instruction instead of two. The call-save area is the backend's largest
// single block of emitted traffic — 92% of its stack memory ops on
// compiler-shaped input — and pairing it takes 10% off the whole program
// (docs/SSA-REGALLOC-PLAN.md).
//
// A slot past maxPairOffset falls back to a single str/ldr; the assembler
// rejects a pair beyond that rather than truncating the offset.
func slotSaveLines(regs []int, slotBase int) []string { return slotLines("st", regs, slotBase) }

func slotRestoreLines(regs []int, slotBase int) []string { return slotLines("ld", regs, slotBase) }

func slotLines(op string, regs []int, slotBase int) []string {
	out := make([]string, 0, len(regs))
	for k := 0; k < len(regs); {
		off := 8 * (slotBase + k)
		if k+1 < len(regs) && off <= maxPairOffset {
			out = append(out, fmt.Sprintf("%sp %s, %s, [sp, #%d]", op, xreg(regs[k]), xreg(regs[k+1]), off))
			k += 2
			continue
		}
		out = append(out, fmt.Sprintf("%sr %s, [sp, #%d]", op, xreg(regs[k]), off))
		k++
	}
	return out
}

// callSavedSet returns the caller-saved allocatable registers to preserve across
// a call. The allocator computes the live-across set (SaveRegsSet) from liveness
// and has already filtered it to the caller-saved partition; absent that set the
// fallback saves every caller-saved allocatable register. Callee-saved registers
// are never in either set — the callee restores them, and the prologue covers
// this function's own use of them.
func callSavedSet(in x86.Inst, numAlloc int) []int {
	if in.SaveRegsSet {
		return in.SaveRegs
	}
	saved := make([]int, 0, numAlloc)
	for r := 0; r < numAlloc; r++ {
		if !armCalleeSaved(r) {
			saved = append(saved, r)
		}
	}
	return saved
}

// fnTableSym labels the module's function-address dispatch table: a reserved
// null slot followed by one .quad per function in the module's (sorted) emission
// order — indexed by the same fn_idx a closure cell carries. OpCallIndirect
// resolves its callee by loading table[fn_idx].
//
// Function-value indices are therefore 1-based, so 0 is the null function
// reference. The closure cell's drop slot needs one: a target with no
// __closure_drop_ thunk stores 0 there and __drop_arr_closure's `drop != 0`
// guard skips the dispatch (see closureLines).
const fnTableSym = "__ssa_fn_table"

// captureEnvLayout returns each capture's byte offset and slot size in the env
// block, plus the total env size, from in.CaptureSlots (the packed layout the
// CaptureRef loads expect). A nil/short CaptureSlots falls back to one 8-byte
// slot per capture.
func captureEnvLayout(in x86.Inst) (offs, sizes []int64, total int64) {
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

// closureLines renders OpMakeEnv / OpMakeClosure on the .bss bump heap. MakeEnv
// allocates a packed env block and stores each capture; MakeClosure additionally
// wraps it in a 32-byte {fn_idx, env_ptr, drop_idx, env_ptr} cell. Both blocks
// carry the rc header (rc=1 at base+0, data at base+8) so they drop through the
// RC helpers. Bump allocation forms the cursor address in a register (unlike
// x86's RIP-relative mem-immediate), so each alloc draws two temporaries from the
// scratch pool avoiding the destination and (for the cell) the env register that
// must survive.
//
// The 4-slot cell is the shape a generic holder expects — the IR's
// __drop_arr_closure walks an array of closures and, for each element, dispatches
// the sub-pair at {+2*ptrW, +3*ptrW} to free the captures. A 2-slot cell made
// that walk read past the cell into the next heap block and call the LAMBDA as
// though it were the element's drop routine (#6144). drop_idx is
// __closure_drop_<target>'s function index, or 0 (the reserved null) when the
// module has no such thunk; the duplicated env_ptr at +24 is what makes
// {drop_idx, env_ptr} itself a dispatchable cell.
func closureLines(in x86.Inst, numAlloc int, fnIndex map[string]int, fr frameLayout) ([]string, error) {
	stage := numAlloc + 3 // s3 — capture value staging
	envReg := numAlloc    // s0 — env block held across the cell alloc
	var out []string
	// An rc-headed block of `bytes` payload into dst, through __ssa_alloc_pres
	// so nothing else live here is disturbed. The payload size at base+4 is
	// what __fern_closure_drop frees the cell with.
	alloc := func(dst int, bytes int64) {
		for _, l := range movImmLines("x16", uint64(bytes+8)) {
			out = append(out, strings.TrimPrefix(l, "\t"))
		}
		out = append(out, allocPresLines()...)
		out = append(out, "mov w17, #1", "str w17, [x16]")
		for _, l := range movImmLines("w17", uint64(bytes)) {
			out = append(out, strings.TrimPrefix(l, "\t"))
		}
		out = append(out,
			"str w17, [x16, #4]",
			fmt.Sprintf("add %s, x16, #8", xreg(dst)), // data = base + 8
		)
	}
	offs, sizes, envBytes := captureEnvLayout(in)
	storeCaps := func(base int) {
		for i, l := range in.ArgLocs {
			if l.IsReg {
				out = append(out, fmt.Sprintf("mov %s, %s", xreg(stage), xreg(l.Reg)))
			} else {
				out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(stage), fr.slot(l.Slot)))
			}
			if sizes[i] == 4 {
				out = append(out, fmt.Sprintf("str %s, [%s, #%d]", wreg(stage), xreg(base), offs[i]))
			} else {
				out = append(out, fmt.Sprintf("str %s, [%s, #%d]", xreg(stage), xreg(base), offs[i]))
			}
		}
	}
	if in.Op == x86.MakeEnv {
		alloc(in.Dst, envBytes)
		storeCaps(in.Dst)
		return out, nil
	}
	idx, ok := fnIndex[in.Callee]
	if !ok {
		return nil, fmt.Errorf("arm64ssa: MakeClosure target %q not in module", in.Callee)
	}
	if len(in.ArgLocs) == 0 {
		// No captures: env_ptr = 0 and drop_idx = 0 — there is no env block to
		// free, so nothing may dispatch the drop sub-pair. Every word is a
		// compile-time constant, so the cell is the module's immortal .rodata one
		// (staticClosureTargets) and materialising the value is its address.
		lbl := staticClosureLabel(idx)
		return append(out,
			fmt.Sprintf("adrp %s, %s", xreg(in.Dst), lbl),
			fmt.Sprintf("add %s, %s, #:lo12:%s", xreg(in.Dst), xreg(in.Dst), lbl),
		), nil
	}
	// drop_idx = __closure_drop_<target>'s index, or 0 when the module has no
	// such thunk (RcFree off, or a target dead-function elimination culled).
	// Read structurally from the emitted function set, never from a flag, so it
	// can never name a symbol this module does not define.
	dropIdx := fnIndex["__closure_drop_"+in.Callee]
	alloc(envReg, envBytes) // env block -> s0
	storeCaps(envReg)
	alloc(in.Dst, 32) // the 4-slot cell -> Dst; env stays in s0
	out = append(out,
		fmt.Sprintf("mov %s, #%d", xreg(stage), idx),
		fmt.Sprintf("str %s, [%s, #0]", xreg(stage), xreg(in.Dst)),
		fmt.Sprintf("str %s, [%s, #8]", xreg(envReg), xreg(in.Dst)),
		fmt.Sprintf("mov %s, #%d", xreg(stage), dropIdx),
		fmt.Sprintf("str %s, [%s, #16]", xreg(stage), xreg(in.Dst)),
		fmt.Sprintf("str %s, [%s, #24]", xreg(envReg), xreg(in.Dst)),
	)
	return out, nil
}

// callIndirectLines renders a closure dispatch. in.IdxLoc points at a closure
// cell (or its drop sub-pair): fn_idx (at +0) indexes the function-address table
// (fnTableSym), env_ptr (at +8) is appended as the callee's LAST argument
// (docs/SSA-CLOSURE-DISPATCH.md). The scratch registers (s0..s3 = x12..x15) sit
// above both the argument registers (x0..x7) and the allocatable homes
// (x0..x11), so the resolved target (s1) and the env (s0) survive the argument
// parallel-move untouched. Caller-saved live-across registers are preserved in
// the call-save area exactly as callLines does.
func callIndirectLines(in x86.Inst, numAlloc int, fr frameLayout) ([]string, error) {
	s0, s1, s2, s3 := numAlloc, numAlloc+1, numAlloc+2, numAlloc+3
	var out []string
	// Stage the cell pointer, then read env (+8) and fn_idx (+0) before any
	// argument register is disturbed.
	if in.IdxLoc.IsReg {
		out = append(out, fmt.Sprintf("mov %s, %s", xreg(s2), xreg(in.IdxLoc.Reg)))
	} else {
		out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(s2), fr.slot(in.IdxLoc.Slot)))
	}
	out = append(out,
		fmt.Sprintf("ldr %s, [%s, #8]", xreg(s0), xreg(s2)), // env  = cell[8]
		fmt.Sprintf("ldr %s, [%s]", xreg(s3), xreg(s2)),     // fn_idx = cell[0]
		// Resolve table[fn_idx] -> absolute code address into s1.
		fmt.Sprintf("adrp %s, %s", xreg(s2), fnTableSym),
		fmt.Sprintf("add %s, %s, #:lo12:%s", xreg(s2), xreg(s2), fnTableSym),
		fmt.Sprintf("lsl %s, %s, #3", xreg(s3), xreg(s3)),
		fmt.Sprintf("add %s, %s, %s", xreg(s2), xreg(s2), xreg(s3)),
		fmt.Sprintf("ldr %s, [%s]", xreg(s1), xreg(s2)), // target -> s1
	)
	// Preserve the caller-saved registers live across the call.
	saved := callSavedSet(in, numAlloc)
	out = append(out, slotSaveLines(saved, fr.callSaveBase)...)
	// Move the explicit args plus the env (as the final argument) into x0..x{n}.
	argsWithEnv := append(append([]x86.Loc{}, in.ArgLocs...), x86.Loc{IsReg: true, Reg: s0})
	out = append(out, argMoveLines(argsWithEnv, fr, s2)...)
	out = append(out, fmt.Sprintf("blr %s", xreg(s1)))
	out = append(out, fmt.Sprintf("mov %s, x0", xreg(s3))) // capture result
	out = append(out, slotRestoreLines(saved, fr.callSaveBase)...)
	out = append(out, fmt.Sprintf("mov %s, %s", xreg(in.Dst), xreg(s3))) // place result
	out = append(out, maskFix(in.Dst, in.W)...)
	return out, nil
}

// boxDynLines renders OpBoxDyn: pack a boxed one-word `dyn Trait` value
// (docs/DYN-TRAITS.md §4.2.2) as a 16-byte {data @0, vtable @8} cell on the
// .bss bump heap. ArgLocs = [data, vtable]. Unlike the native backend — which
// routes through __fern_alloc and must park the operands in callee-saved
// registers across the clobbering call — this path bump-allocates inline (no
// call, only the scratch pool is touched), so the operand homes in the
// allocatable file survive untouched and no save/restore is needed. The cell
// carries the standard rc header (rc=1 at base, payload at base+8) so it is a
// well-formed heap object; it leaks (this path does not wire `dyn` RC), matching
// the arm64 native `dyn` slice.
func boxDynLines(in x86.Inst, numAlloc int, fr frameLayout) []string {
	s0, s2 := numAlloc, numAlloc+2
	// Allocate the rc-headed 16-byte payload cell into s2 through
	// __ssa_alloc_pres, then place it into Dst at the end.
	out := []string{"mov x16, #24"}
	out = append(out, allocPresLines()...)
	out = append(out,
		"mov w17, #1",
		"str w17, [x16]", // rc = 1
		"mov w17, #16",
		"str w17, [x16, #4]",                     // payload size
		fmt.Sprintf("add %s, x16, #8", xreg(s2)), // payload = base + 8
	)
	// Store data @+0 and vtable @+8. The allocatable-file / slot homes the
	// operands live in are untouched by the alloc above (only x16/x17 and s2),
	// so staging them now through s0 is safe.
	stage := func(l x86.Loc) {
		if l.IsReg {
			out = append(out, fmt.Sprintf("mov %s, %s", xreg(s0), xreg(l.Reg)))
		} else {
			out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(s0), fr.slot(l.Slot)))
		}
	}
	stage(in.ArgLocs[0])
	out = append(out, fmt.Sprintf("str %s, [%s]", xreg(s0), xreg(s2))) // data @+0
	stage(in.ArgLocs[1])
	out = append(out, fmt.Sprintf("str %s, [%s, #8]", xreg(s0), xreg(s2))) // vtable @+8
	out = append(out, fmt.Sprintf("mov %s, %s", xreg(in.Dst), xreg(s2)))   // place cell ptr
	return out
}

// callDynLines renders OpCallDyn: dispatch a `dyn Trait` method call
// (docs/DYN-TRAITS.md §4.2.2). ArgLocs = [data, method-args..., vtable] — the
// vtable is the LAST operand, not a call argument. Load the vtable, read slot
// `in.Imm`'s absolute function pointer (`vtable + slot*8`) into a scratch, then
// call it with [data, method-args...] as the AArch64 PCS args (receiver-first,
// plain — no closure env). Mirrors callIndirectLines' save/restore + argument
// parallel-move; the scratch registers sit above the allocatable file and so
// are disjoint from both the arg registers (x0..x7) and every allocatable home,
// so the resolved target (s1) survives the argument move.
func callDynLines(in x86.Inst, numAlloc int, fr frameLayout) ([]string, error) {
	n := len(in.ArgLocs)
	if n < 1 {
		return nil, fmt.Errorf("arm64ssa: OpCallDyn needs the vtable operand")
	}
	callArgs := in.ArgLocs[:n-1]
	vtLoc := in.ArgLocs[n-1]
	s1, s2, s3 := numAlloc+1, numAlloc+2, numAlloc+3
	var out []string
	// Stage the vtable into s2, then load slot -> target s1, before any argument
	// register is disturbed.
	if vtLoc.IsReg {
		out = append(out, fmt.Sprintf("mov %s, %s", xreg(s2), xreg(vtLoc.Reg)))
	} else {
		out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(s2), fr.slot(vtLoc.Slot)))
	}
	if in.Imm != 0 {
		out = append(out, fmt.Sprintf("ldr %s, [%s, #%d]", xreg(s1), xreg(s2), in.Imm*8))
	} else {
		out = append(out, fmt.Sprintf("ldr %s, [%s]", xreg(s1), xreg(s2)))
	}
	// Preserve the caller-saved registers live across the call.
	saved := callSavedSet(in, numAlloc)
	out = append(out, slotSaveLines(saved, fr.callSaveBase)...)
	// Move [data, method-args...] into x0..x{n-1} and dispatch.
	out = append(out, argMoveLines(callArgs, fr, s2)...)
	out = append(out, fmt.Sprintf("blr %s", xreg(s1)))
	out = append(out, fmt.Sprintf("mov %s, x0", xreg(s3))) // capture result
	out = append(out, slotRestoreLines(saved, fr.callSaveBase)...)
	out = append(out, fmt.Sprintf("mov %s, %s", xreg(in.Dst), xreg(s3))) // place result
	out = append(out, maskFix(in.Dst, in.W)...)
	return out, nil
}

// argMoveLines moves call arguments from their allocated homes into the AArch64
// argument registers (arg i → x{i}). The abstract file maps index i onto x{i},
// so reg-homed args are a parallel register copy over abstract indices (resolved
// by resolveRegMoves); slot-homed args load from [sp] afterward, by which point
// every reg-homed source has been consumed.
func argMoveLines(argLocs []x86.Loc, fr frameLayout, scratch int) []string {
	// The arguments past the register half go out first, while their homes are
	// still intact: the register half below writes x0..x{argRegCount-1}, which
	// a stack argument may well be sitting in.
	var out []string
	for i := argRegCount; i < len(argLocs); i++ {
		l := argLocs[i]
		src := scratch
		if l.IsReg {
			src = l.Reg
		} else {
			out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(scratch), fr.slot(l.Slot)))
		}
		out = append(out, fmt.Sprintf("str %s, [sp, #%d]", xreg(src), fr.outArg(i-argRegCount)))
	}

	var moves [][2]int // {dstArgReg=i, srcHomeReg}
	for i, l := range argLocs {
		if i >= argRegCount {
			break
		}
		if l.IsReg && l.Reg != i {
			moves = append(moves, [2]int{i, l.Reg})
		}
	}
	out = append(out, resolveRegMoves(moves)...)
	for i, l := range argLocs {
		if i >= argRegCount {
			break
		}
		if !l.IsReg {
			out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(i), fr.slot(l.Slot)))
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
func paramMoveLines(paramLocs []x86.Loc, fr frameLayout, scratch int) []string {
	var out []string
	// Step A: slot-homed params (read arg regs, write memory).
	for i, loc := range paramLocs {
		if i >= argRegCount {
			break
		}
		if !loc.IsReg && loc.Slot >= 0 {
			out = append(out, fmt.Sprintf("str %s, [sp, #%d]", xreg(i), fr.slot(loc.Slot)))
		}
	}
	// Step B: register-homed params — parallel register copy.
	var moves [][2]int // {dst, src} abstract register indices
	for i, loc := range paramLocs {
		if i >= argRegCount {
			break
		}
		if loc.IsReg && loc.Reg != i {
			moves = append(moves, [2]int{loc.Reg, i})
		}
	}
	out = append(out, resolveRegMoves(moves)...)
	// Step C: the params the caller left on the stack. Last, because a home
	// here can be one of the argument registers steps A and B still read.
	for i := argRegCount; i < len(paramLocs); i++ {
		loc := paramLocs[i]
		dst := scratch
		if loc.IsReg {
			dst = loc.Reg
		}
		out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(dst), fr.inArg(i-argRegCount)))
		if !loc.IsReg && loc.Slot >= 0 {
			out = append(out, fmt.Sprintf("str %s, [sp, #%d]", xreg(scratch), fr.slot(loc.Slot)))
		}
	}
	return out
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
// left is the register holding the instruction's left (accumulator) operand.
// It is in.Dst for the abstract two-address form; deadAccMoves names another
// register when the copy that would have put the value there is dropped.
func asmInst(in x86.Inst, left, scratch int, fr frameLayout) ([]string, error) {
	switch in.Op {
	case x86.MovImm:
		// The immediate is the value in the model's slot, already sign-extended
		// for i32. A plain `mov` materialises it only when the value is one
		// shifted halfword (MOVZ) or the inverse of one (MOVN); anything else
		// needs the movz + movk sequence spelled out. gas rejects the rest
		// outright ("immediate cannot be moved by a single instruction"), and
		// the in-process assembler took the low half and produced a wrong
		// answer, so `mov x0, #100000` in int_to_string mis-numbered every
		// value at or above 100000.
		return movImmLines(xreg(in.Dst), uint64(in.Imm)), nil
	case x86.MovReg:
		if in.Dst == in.Src {
			return nil, nil
		}
		return []string{fmt.Sprintf("mov %s, %s", xreg(in.Dst), xreg(in.Src))}, nil
	case x86.LoadSlot:
		return []string{fmt.Sprintf("ldr %s, [sp, #%d]", xreg(in.Dst), fr.slot(int(in.Imm)))}, nil
	case x86.StoreSlot:
		return []string{fmt.Sprintf("str %s, [sp, #%d]", xreg(in.Src), fr.slot(int(in.Imm)))}, nil
	case x86.BinOp:
		switch in.K {
		case ssa.OpShl, ssa.OpShr, ssa.OpShrU, ssa.OpDiv, ssa.OpDivU, ssa.OpRem, ssa.OpRemU:
			return divShiftSeq(in, left, scratch), nil
		}
		mnem, ok := binMnemonic(in.K)
		if !ok {
			return nil, fmt.Errorf("arm64ssa: binary op %v not supported yet", in.K)
		}
		// 3-operand form: the abstract semantics reg[Dst] = reg[Dst] (K) reg[Src]
		// name the accumulator twice, but AArch64 can read it from anywhere.
		out := []string{fmt.Sprintf("%s %s, %s, %s", mnem, xreg(in.Dst), xreg(left), xreg(in.Src))}
		out = append(out, maskFix(in.Dst, in.W)...)
		return out, nil
	case x86.SetCmp:
		return setCmpSeq(in, left)
	case x86.MemAlloc:
		return memAllocSeq(in), nil
	case x86.MemLoad:
		return memLoadSeq(in), nil
	case x86.MemStore:
		return memStoreSeq(in), nil
	case x86.FConst:
		return fConstSeq(in), nil
	case x86.FBin:
		return fBinSeq(in)
	case x86.FCmp:
		return fCmpSeq(in, left)
	case x86.FConv:
		return fConvSeq(in)
	case x86.UnNeg:
		return append([]string{fmt.Sprintf("neg %s, %s", xreg(in.Dst), xreg(left))}, maskFix(in.Dst, in.W)...), nil
	case x86.UnOp:
		return unOpSeq(in, left)
	case x86.Select:
		return selectSeq(in), nil
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
func divShiftSeq(in x86.Inst, left, scratch int) []string {
	d, s := xreg(in.Dst), xreg(in.Src)
	a, aw := xreg(left), wreg(left)
	// A 32-bit op renders in the 32-bit w-form, for TWO independent reasons.
	//
	// Operand: the unsigned ops (lsr / udiv / unsigned rem) must not see the
	// sign-extended high 32 bits of a 32-bit operand. A u32 with bit 31 set is
	// stored sign-extended (all 1s in bits 32-63, per maskFix), so a 64-bit
	// logical shift or unsigned divide would drag those bits in and miscompute
	// (the u32 `>>` bug that broke SHA-256).
	//
	// COUNT: every shift, signed included, masks its count to the width of the
	// register it operates on — 5 bits for w, 6 for x. So `460 << 124` at i32
	// width is `460 << (124 & 31)` = -1073741824, but rendered as `lsl x` it
	// becomes `460 << (124 & 63)`, whose low 32 bits are 0. The signed shifts
	// used to take the x-form on the grounds that "sign-extended operands are
	// already correct", which is true of the VALUE and says nothing about the
	// count.
	//
	// The w-form reads only the low 32 bits and zero-extends the result; the
	// trailing maskFix re-sign-extends to the storage convention. 64-bit ops
	// keep the x-form. Signed DIVIDE genuinely does not care: sdiv takes no
	// count, and a sign-extended operand divides correctly at either width.
	width32 := in.W != 64
	dw, sw := wreg(in.Dst), wreg(in.Src)
	var out []string
	switch in.K {
	case ssa.OpShl:
		if width32 {
			out = []string{fmt.Sprintf("lsl %s, %s, %s", dw, aw, sw)}
		} else {
			out = []string{fmt.Sprintf("lsl %s, %s, %s", d, a, s)}
		}
	case ssa.OpShr:
		if width32 {
			out = []string{fmt.Sprintf("asr %s, %s, %s", dw, aw, sw)} // arithmetic, 32-bit
		} else {
			out = []string{fmt.Sprintf("asr %s, %s, %s", d, a, s)} // arithmetic, 64-bit
		}
	case ssa.OpShrU:
		if width32 {
			out = []string{fmt.Sprintf("lsr %s, %s, %s", dw, aw, sw)} // logical, 32-bit
		} else {
			out = []string{fmt.Sprintf("lsr %s, %s, %s", d, a, s)} // logical, 64-bit
		}
	case ssa.OpDiv:
		out = []string{fmt.Sprintf("sdiv %s, %s, %s", d, a, s)}
	case ssa.OpDivU:
		if width32 {
			out = []string{fmt.Sprintf("udiv %s, %s, %s", dw, aw, sw)} // 32-bit
		} else {
			out = []string{fmt.Sprintf("udiv %s, %s, %s", d, a, s)} // 64-bit
		}
	case ssa.OpRem, ssa.OpRemU:
		if in.K == ssa.OpRemU && width32 {
			q := wreg(scratch)
			out = []string{
				fmt.Sprintf("udiv %s, %s, %s", q, aw, sw),         // q = a / s (32-bit)
				fmt.Sprintf("msub %s, %s, %s, %s", dw, q, sw, aw), // d = a - q*s
			}
		} else {
			q := xreg(scratch)
			div := "sdiv"
			if in.K == ssa.OpRemU {
				div = "udiv"
			}
			out = []string{
				fmt.Sprintf("%s %s, %s, %s", div, q, a, s),     // q = a / s
				fmt.Sprintf("msub %s, %s, %s, %s", d, q, s, a), // d = a - q*s
			}
		}
	}
	return append(out, maskFix(in.Dst, in.W)...)
}

// memAllocSeq renders OpAlloc: a raw block of Src bytes through
// __ssa_alloc_pres, its 16-aligned base into Dst. No header is added here: the
// IR lays the rc word down itself and frees the block from the same base
// (__fern_box_free(data, size) -> __free(data-8, size+8)), and the SSA
// evaluator's heap hands out the same bare base, so the three agree on where a
// block starts. Only x16/x17 are touched besides Dst, so nothing live around
// the op needs saving.
func memAllocSeq(in x86.Inst) []string {
	seq := []string{fmt.Sprintf("mov x16, %s", xreg(in.Src))}
	seq = append(seq, allocPresLines()...)
	return append(seq, fmt.Sprintf("mov %s, x16", xreg(in.Dst)))
}

// memLoadSeq renders a load of in.Bytes bytes from [Src + Imm] into Dst. It
// mirrors the x86-64 widths: 8-byte loads move a full word; 4-byte loads move
// the low word (zero-extended); sub-word loads sign- or zero-extend per
// in.Signed. The trailing maskFix reproduces the model's i32 width mask.
func memLoadSeq(in x86.Inst) []string {
	d, dw := xreg(in.Dst), wreg(in.Dst)
	mem := fmt.Sprintf("[%s, #%d]", xreg(in.Src), in.Imm)
	// AArch64's loads already sign- or zero-extend into the full register, so
	// an i32 result's sign-extension is either part of the load's own form or a
	// no-op — only the 8-byte load actually narrows.
	narrow := in.W != 64
	switch in.Bytes {
	case 8:
		return append([]string{fmt.Sprintf("ldr %s, %s", d, mem)}, maskFix(in.Dst, in.W)...)
	case 4:
		if narrow {
			return []string{fmt.Sprintf("ldrsw %s, %s", d, mem)}
		}
		return []string{fmt.Sprintf("ldr %s, %s", dw, mem)} // zero-extends to 64
	case 2:
		if in.Signed {
			return []string{fmt.Sprintf("ldrsh %s, %s", d, mem)}
		}
		return []string{fmt.Sprintf("ldrh %s, %s", dw, mem)}
	default: // 1 byte
		if in.Signed {
			return []string{fmt.Sprintf("ldrsb %s, %s", d, mem)}
		}
		return []string{fmt.Sprintf("ldrb %s, %s", dw, mem)}
	}
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

// Floats live in GP registers as their f64 bit pattern (like ssa.Eval and the
// x86-64 SSA path) — even an f32 value is stored as the f64 bits of its
// f32-rounded value. The sequences below shuttle those bits into the FP file
// (d0/d1 — always free, the abstract register file is GP-only) for the actual
// arithmetic/compare/convert, then move the result back. When the result width
// is 32, an fcvt round-trip (d->s->d) reproduces the model's f32 precision mask.

// fConstSeq materialises a float literal's f64 bit pattern into Dst via a literal
// load. For an f32 result the bits are those of the f32-rounded value.
func fConstSeq(in x86.Inst) []string {
	bits := math.Float64bits(in.F64)
	if in.W == 32 {
		bits = math.Float64bits(float64(float32(in.F64)))
	}
	return []string{fmt.Sprintf("ldr %s, =0x%x", xreg(in.Dst), bits)}
}

// fBinSeq renders a scalar float arithmetic op: shuttle both operands into d0/d1,
// compute in f64, round to f32 if W==32, shuttle the result back.
func fBinSeq(in x86.Inst) ([]string, error) {
	mnem, ok := fbinMnemonic(in.K)
	if !ok {
		return nil, fmt.Errorf("arm64ssa: float op %v not supported yet", in.K)
	}
	d, s := xreg(in.Dst), xreg(in.Src)
	out := []string{
		fmt.Sprintf("fmov d0, %s", d),
		fmt.Sprintf("fmov d1, %s", s),
		fmt.Sprintf("%s d0, d0, d1", mnem),
	}
	out = fround32(out, in.W)
	return append(out, fmt.Sprintf("fmov %s, d0", d)), nil
}

// fCmpSeq renders a scalar float comparison. AArch64 fcmp sets C/Z compatibly
// with x86 ucomisd, so the unsigned condition codes (lo/ls/hi/hs) order finite
// operands correctly; cset materialises the 0/1 result. NaN is out of scope (the
// model uses ordered Go comparisons on finite values).
func fCmpSeq(in x86.Inst, left int) ([]string, error) {
	cc, ok := fcondCode(in.K)
	if !ok {
		return nil, fmt.Errorf("arm64ssa: float compare %v not supported yet", in.K)
	}
	d, s := xreg(in.Dst), xreg(in.Src)
	return []string{
		fmt.Sprintf("fmov d0, %s", xreg(left)),
		fmt.Sprintf("fmov d1, %s", s),
		"fcmp d0, d1",
		fmt.Sprintf("cset %s, %s", d, cc),
	}, nil
}

// fConvSeq renders a float conversion / unary op. Integer results are width-
// masked (maskFix); float results carry their f64 bit pattern.
func fConvSeq(in x86.Inst) ([]string, error) {
	d := xreg(in.Dst)
	switch in.K {
	case ssa.OpFNeg:
		// Flip the sign; negating an f32-precision value keeps f32 precision.
		return []string{fmt.Sprintf("fmov d0, %s", d), "fneg d0, d0", fmt.Sprintf("fmov %s, d0", d)}, nil
	case ssa.OpFPromote:
		// f32 -> f64: the value already lives as f64 bits; identity.
		return nil, nil
	case ssa.OpFDemote:
		return []string{fmt.Sprintf("fmov d0, %s", d), "fcvt s0, d0", "fcvt d0, s0", fmt.Sprintf("fmov %s, d0", d)}, nil
	case ssa.OpIToFS:
		return append(fround32([]string{fmt.Sprintf("scvtf d0, %s", d)}, in.W), fmt.Sprintf("fmov %s, d0", d)), nil
	case ssa.OpIToFU:
		return append(fround32([]string{fmt.Sprintf("ucvtf d0, %s", d)}, in.W), fmt.Sprintf("fmov %s, d0", d)), nil
	case ssa.OpFToIS, ssa.OpFToIU:
		// float -> int, truncating toward zero and SATURATING: NaN gives 0, out
		// of range clamps to the destination's min/max (docs/FLOAT-SEMANTICS.md).
		//
		// AArch64's fcvtz{s,u} already saturate — but to the width of the
		// DESTINATION REGISTER, so the register form has to match the
		// destination type. Converting into `x` and then narrowing with maskFix
		// saturates to the 64-bit range and sign-extends bit 31 of that, which
		// is wraparound, not saturation: `(91.23f32 * 1e9) as i32` came out as
		// 1035689984 where every other backend gives INT32_MAX. The `w` form
		// clamps to the 32-bit range directly, so maskFix is then a no-op on an
		// already-in-range value (and is what zero-extends the u32 case into the
		// sign-extended storage convention).
		mnem := "fcvtzs"
		if in.K == ssa.OpFToIU {
			mnem = "fcvtzu"
		}
		dst := wreg(in.Dst)
		if in.W == 64 {
			dst = d
		}
		return append([]string{
			fmt.Sprintf("fmov d0, %s", d),
			fmt.Sprintf("%s %s, d0", mnem, dst),
		}, maskFix(in.Dst, in.W)...), nil
	case ssa.OpReinterpretF64ToI64, ssa.OpReinterpretI64ToF64:
		// Identity, for the same reason OpFPromote is: a float is HELD in a
		// general register as its raw f64 bit pattern (that is what every
		// `fmov d0, x` above moves), so asking for those bits as an i64 — or
		// handing an i64 pattern back as an f64 — is already what the
		// register contains. Mirrors x86_64ssa's model (model.go's
		// "identity: floats live as their f64 bit pattern already").
		return nil, nil
	case ssa.OpReinterpretF32ToI32:
		// f32 bits as an i32. The register holds an f64 pattern, so narrow to
		// f32 first, then move the 32-bit half out. `fmov w, s` transfers the
		// raw bits (no conversion); maskFix sign-extends to the i32 storage
		// convention.
		return append([]string{
			fmt.Sprintf("fmov d0, %s", d),
			"fcvt s0, d0",
			fmt.Sprintf("fmov %s, s0", wreg(in.Dst)),
		}, maskFix(in.Dst, 32)...), nil
	case ssa.OpReinterpretI32ToF32:
		// The inverse: read the low 32 bits as an f32 pattern, then widen to
		// the f64 pattern the register convention stores.
		return []string{
			fmt.Sprintf("fmov s0, %s", wreg(in.Dst)),
			"fcvt d0, s0",
			fmt.Sprintf("fmov %s, d0", d),
		}, nil
	}
	return nil, fmt.Errorf("arm64ssa: float conversion %v not supported yet", in.K)
}

// fround32 appends the d0 -> s0 -> d0 fcvt round-trip that reproduces f32
// precision when the result width is 32; a no-op at width 64.
func fround32(out []string, w int8) []string {
	if w == 32 {
		return append(out, "fcvt s0, d0", "fcvt d0, s0")
	}
	return out
}

// fbinMnemonic maps a float arithmetic op to its AArch64 mnemonic.
func fbinMnemonic(k ssa.OpKind) (string, bool) {
	switch k {
	case ssa.OpFAdd:
		return "fadd", true
	case ssa.OpFSub:
		return "fsub", true
	case ssa.OpFMul:
		return "fmul", true
	case ssa.OpFDiv:
		return "fdiv", true
	}
	return "", false
}

// fcondCode maps a float comparison to its AArch64 condition mnemonic.
//
// These are the canonical IEEE codes, NOT the unsigned integer ones. On
// ordered operands the two sets agree exactly (lo≡mi, hi≡gt, hs≡ge), which
// is why unsigned codes here pass every finite-valued test. They diverge on
// NaN: `fcmp` marks unordered with N=0 Z=0 C=1 V=1, so
// `hi` (C && !Z) and `hs` (C) both read TRUE where every ordered comparison
// against a NaN must be false. `0.0/0.0 <= x` therefore printed "T" here and
// "F" under the interpreter and both native backends.
//
// The codes below are false-on-unordered by construction — `mi` tests N,
// `ls` tests !C||Z, `gt` tests !Z && N==V, `ge` tests N==V — leaving `ne` as
// the one comparison that is correctly true for NaN.
//
// The source-level symptom showed up on `<` / `<=` rather than the `>` / `>=`
// that map to `hi` / `hs` directly, because ssa/canon.go's
// flipDirectionalCmp rewrites `a < b` to `b > a` when that lets an operand
// commute. The flip itself is IEEE-sound (both forms are ordered, both false
// on NaN); only the condition code it landed on was not.
func fcondCode(k ssa.OpKind) (string, bool) {
	switch k {
	case ssa.OpFEq:
		return "eq", true
	case ssa.OpFNe:
		return "ne", true
	case ssa.OpFLt:
		return "mi", true
	case ssa.OpFLe:
		return "ls", true
	case ssa.OpFGt:
		return "gt", true
	case ssa.OpFGe:
		return "ge", true
	}
	return "", false
}

// unOpSeq renders the UnOp unary transforms. OpNot is a zero-test materialised
// with cset (clean 0/1, no mask); the extends map onto the AArch64 sign/zero
// extraction instructions, which already produce the model's width-masked value.
func unOpSeq(in x86.Inst, left int) ([]string, error) {
	d, a := in.Dst, left
	switch in.K {
	case ssa.OpNot:
		return []string{fmt.Sprintf("cmp %s, #0", xreg(a)), fmt.Sprintf("cset %s, eq", xreg(d))}, nil
	case ssa.OpTrunc, ssa.OpExtendS:
		return []string{fmt.Sprintf("sxtw %s, %s", xreg(d), wreg(a))}, nil // sign-extend low 32
	case ssa.OpExtendU:
		return []string{fmt.Sprintf("mov %s, %s", wreg(d), wreg(a))}, nil // 32-bit mov zero-extends
	case ssa.OpExtend8S:
		return []string{fmt.Sprintf("sxtb %s, %s", xreg(d), wreg(a))}, nil
	case ssa.OpExtend16S:
		return []string{fmt.Sprintf("sxth %s, %s", xreg(d), wreg(a))}, nil
	case ssa.OpClz, ssa.OpCtz, ssa.OpPopcount:
		return bitCountSeq(in, left), nil
	}
	return nil, fmt.Errorf("arm64ssa: unary op %v not supported yet", in.K)
}

// bitCountSeq renders clz / ctz / popcount. in.W is the OPERAND width, so the
// 32-bit forms use the w-register — counting over the full x-register would add
// the zero-extended high half to clz. A w-register write zero-extends, so the
// count comes back as a clean i32 either way.
//
// AArch64 has clz but no ctz: reversing the bits first turns trailing zeros into
// leading ones. Nor does it have a scalar popcount — cnt is Advanced SIMD and
// counts per BYTE, so the value shuttles into v0, cnt gives eight per-byte
// counts, and addv sums them into one. At 32 bits `fmov s0, w` leaves the top
// four bytes of the 8B group zero, so the same 8-byte sequence serves both
// widths, and the ≤64 total cannot overflow addv's byte-wide destination.
func bitCountSeq(in x86.Inst, left int) []string {
	d, a, wide := in.Dst, left, in.W == 64
	r := wreg
	if wide {
		r = xreg
	}
	switch in.K {
	case ssa.OpClz:
		return []string{fmt.Sprintf("clz %s, %s", r(d), r(a))}
	case ssa.OpCtz:
		return []string{
			fmt.Sprintf("rbit %s, %s", r(d), r(a)),
			fmt.Sprintf("clz %s, %s", r(d), r(d)),
		}
	}
	mov := fmt.Sprintf("fmov s0, %s", wreg(a))
	if wide {
		mov = fmt.Sprintf("fmov d0, %s", xreg(a))
	}
	return []string{mov, "cnt v0.8b, v0.8b", "addv b0, v0.8b", fmt.Sprintf("fmov %s, s0", wreg(d))}
}

// selectSeq renders OpSelect (reg[Dst] = reg[Src] != 0 ? reg[Src2] : reg[Src3])
// with the branch-free conditional select: cmp the condition, then csel reads
// both operands and writes Dst in one instruction — no intermediate move can
// clobber a still-live operand (the hazard the x86 path avoids with a branch).
// A trailing maskFix reproduces the model's i32 width mask.
func selectSeq(in x86.Inst) []string {
	out := []string{
		fmt.Sprintf("cmp %s, #0", xreg(in.Src)),
		fmt.Sprintf("csel %s, %s, %s, ne", xreg(in.Dst), xreg(in.Src2), xreg(in.Src3)),
	}
	return append(out, maskFix(in.Dst, in.W)...)
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

// setCmpSeq renders an integer comparison whose left operand lives in `left`.
// A 64-bit cmp on sign-extended i32 operands orders correctly for both signed
// and unsigned conditions; cset materialises the 0/1 result (no i32 mask
// needed).
func setCmpSeq(in x86.Inst, left int) ([]string, error) {
	cc, ok := condCode(in.K)
	if !ok {
		return nil, fmt.Errorf("arm64ssa: comparison %v not supported yet", in.K)
	}
	return []string{
		fmt.Sprintf("cmp %s, %s", xreg(left), xreg(in.Src)),
		fmt.Sprintf("cset %s, %s", xreg(in.Dst), cc),
	}, nil
}

// invCond returns the AArch64 condition that holds exactly when cc does not,
// so a branch can be reversed to let its taken arm fall through.
func invCond(cc string) (string, bool) {
	switch cc {
	case "eq":
		return "ne", true
	case "ne":
		return "eq", true
	case "lt":
		return "ge", true
	case "ge":
		return "lt", true
	case "le":
		return "gt", true
	case "gt":
		return "le", true
	case "lo":
		return "hs", true
	case "hs":
		return "lo", true
	case "ls":
		return "hi", true
	case "hi":
		return "ls", true
	}
	return "", false
}

// fuseBranchCmp splits a block into the instructions to render and, when its
// conditional branch can test the flags of a comparison directly, that
// comparison's condition code and operand registers. The comparison — and the
// copy that fed its left operand — are then not rendered at all: AArch64's
// b.<cond> reads the flags cmp already set, so materialising the 0/1 with cset
// and testing it with cbnz is two wasted instructions.
//
// The fusion needs Term.CondFuse, which is the emitter's word that nothing but
// the terminator reads the comparison's value; the structural checks here
// confirm the comparison really is the block's last instruction.
// cc is "" when nothing fuses.
func fuseBranchCmp(blk x86.MBlock) (insts []x86.Inst, cc string, left, right int) {
	insts, left, right = blk.Insts, -1, -1
	if blk.Term.Kind != x86.TBrIf || !blk.Term.CondFuse || len(insts) == 0 {
		return insts, "", -1, -1
	}
	c := insts[len(insts)-1]
	code, ok := condCode(c.K)
	if !ok || c.Op != x86.SetCmp || c.Dst != blk.Term.CondReg {
		return insts, "", -1, -1
	}
	cc, left, right = code, c.Dst, c.Src
	insts = insts[:len(insts)-1]
	// The same dead copy deadAccMoves would have removed, one instruction
	// earlier because the comparison itself is gone.
	if n := len(insts); n > 0 {
		if m := insts[n-1]; m.Op == x86.MovReg && m.Dst == c.Dst && c.Src != c.Dst {
			left = m.Src
			insts = insts[:n-1]
		}
	}
	return insts, cc, left, right
}

// accReads reports whether opcode `op` reads its destination register as an
// input — the abstract model's two-address form, where `dst = dst K src`. Only
// those instructions can take their left operand from somewhere else.
//
// srcIsOperand additionally reports whether the instruction reads Src, which
// decides whether a copy into Dst can be dropped: when Src IS Dst the copy
// holds the value being read on the right, so it is not dead.
func accReads(op x86.Opcode) (acc, srcIsOperand bool) {
	switch op {
	case x86.BinOp, x86.SetCmp, x86.FCmp:
		return true, true
	case x86.UnNeg, x86.UnOp:
		return true, false
	}
	return false, false
}

// deadAccMoves locates the register copies that only exist to satisfy the
// abstract two-address form. `dst = dst K src` needs its left operand copied
// into the destination first, but every AArch64 instruction the renderer emits
// for one is three-address (and cmp / fcmp have no destination at all), so the
// copy is never read and the instruction can name its source directly.
//
// skip[i] marks such a copy; left[i] is the register the instruction at i reads
// as its left operand, or -1 when it reads its destination as usual.
func deadAccMoves(insts []x86.Inst) (skip []bool, left []int) {
	skip = make([]bool, len(insts))
	left = make([]int, len(insts))
	for i := range left {
		left[i] = -1
	}
	for i := 0; i+1 < len(insts); i++ {
		m, c := insts[i], insts[i+1]
		if m.Op != x86.MovReg {
			continue
		}
		acc, srcIsOperand := accReads(c.Op)
		if !acc || c.Dst != m.Dst {
			continue
		}
		if srcIsOperand && c.Src == m.Dst {
			continue
		}
		skip[i] = true
		left[i+1] = m.Src
	}
	return skip, left
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
// emitRuntimeHelpers writes the named helper bodies, each at a 16-byte
// boundary, for the reason the x86-64 sibling does: a helper entry that falls
// wherever the preceding code happens to end makes an unrelated change to that
// code read as a large regression here. The measurement behind it is
// x86-64's (#8193); this side is the same hazard, applied symmetrically
// rather than measured.
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
