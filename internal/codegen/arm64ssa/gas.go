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
	"sort"
	"strconv"
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
	helpers := referencedRuntimeHelpers(progs)
	heap := usesHeap(progs)
	for _, h := range helpers {
		if heapUsingHelpers[h] {
			heap = true // e.g. __str_concat bump-allocates even with no direct heap op
			break
		}
	}
	strLabels, strOrder := collectStrings(progs, names)
	sentLabels, sentOrder := collectSentinels(progs, names)
	// fn_idx for closures: a function's index in the module's (sorted) emission
	// order — the value a closure cell carries, and the index into the function-
	// address table (fnTableSym) that OpCallIndirect dereferences.
	fnIndex := make(map[string]int, len(names))
	for i, n := range names {
		fnIndex[n] = i
	}

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
		if err := emitFunc(w, name, progs[name], numAlloc, strLabels, sentLabels, fnIndex); err != nil {
			return "", err
		}
		w("")
	}
	// Runtime-helper bodies (hand-written leaf asm, AArch64 PCS: arg/result in
	// x0), emitted only when the module calls them.
	for _, h := range helpers {
		runtimeHelperEmitters[h](w)
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
	if usesCallIndirect(progs) {
		// Function-address dispatch table: one .quad per function in module
		// (sorted) order, so table[fn_idx] is the callee's absolute address. A
		// closure cell carries fn_idx; OpCallIndirect indexes this table. The
		// assembler resolves each `.quad fn_<name>` to the label's address.
		w(".section .rodata")
		w(".align 8")
		w("%s:", fnTableSym)
		for _, name := range names {
			w("\t.quad %s", fnLabel(name))
		}
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
				case x86.MemAlloc, x86.MemLoad, x86.MemStore, x86.MakeEnv, x86.MakeClosure:
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
	"__fern_rc_is_unique":    emitRcIsUniqueHelper,
	"__fern_rc_inc":          emitRcIncHelper,
	"__fern_rc_dec":          emitRcDecHelper,
	"__fern_box_free":        emitBoxFreeHelper,
	"__fern_closure_drop":    emitClosureDropHelper,
	"__str_len":              emitStrLenHelper,
	"__str_eq":               emitStrEqHelper,
	"__str_concat":           emitStrConcatHelper,
	"__fern_str_dec":         emitStrDecHelper,
	"__fern_arr_dec":         emitArrDecHelper,
	"__str_idx":              emitStrIdxHelper,
	"__arr_idx":              emitArrIdxHelperN("__arr_idx", 2),    // stride 4 (i32)
	"__arr_idx_1":            emitArrIdxHelperN("__arr_idx_1", 0),  // stride 1 (byte array)
	"__arr_idx_2":            emitArrIdxHelperN("__arr_idx_2", 1),  // stride 2 (halfword)
	"__arr_idx_8":            emitArrIdxHelperN("__arr_idx_8", 3),  // stride 8 (i64 / pointer)
	"__arr_idx_16":           emitArrIdxHelperN("__arr_idx_16", 4), // stride 16 (two-word string[])
	"__fern_arr_push_grow":   emitArrPushGrowHelper,
	"__alloc_u8":             emitAllocU8Helper,
	"__fern_arr_cow_inplace": emitArrCowInplaceHelper,
	"string_from_bytes":      emitStringFromBytesHelper,
	"__str_slice":            emitStrSliceHelper,
	"print":                  emitPrintHelper,
	"__abs_f64":              emitFloatUnaryHelper("__abs_f64", "fabs"),
	"__sqrt_f64":             emitFloatUnaryHelper("__sqrt_f64", "fsqrt"),
	"__floor_f64":            emitFloatUnaryHelper("__floor_f64", "frintm"),
	"__ceil_f64":             emitFloatUnaryHelper("__ceil_f64", "frintp"),
	"__trunc_f64":            emitFloatUnaryHelper("__trunc_f64", "frintz"),
	"__round_f64":            emitFloatUnaryHelper("__round_f64", "frinta"),
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

// runtimeHelperDeps records helper→helper call edges: a helper that tail-calls
// another must have that callee emitted too, since the module never references
// it directly. Transitively closed by referencedRuntimeHelpers.
var runtimeHelperDeps = map[string][]string{
	"__fern_closure_drop": {"__fern_box_free", "__fern_rc_dec"},
}

// helperReturns64 lists runtime helpers whose result is a full 8-byte value — a
// heap pointer or an f64 bit pattern — so the direct-call sequence must NOT apply
// the i32 sign-extend mask to their result (it would truncate an f64's exponent
// or, for a high heap address, the pointer). i32/void-returning helpers are
// absent (the mask is correct or harmless for them).
var helperReturns64 = map[string]bool{
	"__str_concat":           true,
	"__fern_box_free":        true,
	"__fern_arr_push_grow":   true,
	"__alloc_u8":             true,
	"__fern_arr_cow_inplace": true,
	"string_from_bytes":      true,
	"__str_slice":            true,
	"__str_idx":              true,
	"__arr_idx":              true,
	"__arr_idx_1":            true,
	"__arr_idx_2":            true,
	"__arr_idx_8":            true,
	"__arr_idx_16":           true,
	"__abs_f64":              true,
	"__sqrt_f64":             true,
	"__floor_f64":            true,
	"__ceil_f64":             true,
	"__trunc_f64":            true,
	"__round_f64":            true,
}

// heapUsingHelpers are runtime helpers that bump-allocate on the SSA heap, so the
// .bss heap section + cursor must exist whenever one is referenced even if no
// program body has a direct heap op.
var heapUsingHelpers = map[string]bool{
	"__str_concat":           true,
	"__fern_arr_push_grow":   true,
	"__alloc_u8":             true,
	"__fern_arr_cow_inplace": true,
	"string_from_bytes":      true,
	"__str_slice":            true,
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

// referencedRuntimeHelpers returns, sorted, every runtime helper any emitted
// program calls (that arm64 has an emitter for), plus the transitive closure of
// their helper→helper dependencies (runtimeHelperDeps).
func referencedRuntimeHelpers(progs map[string]*x86.Program) []string {
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

// emitBoxFreeHelper writes __fern_box_free(data, size) -> data: release an
// rc-headed heap block. The SSA bump heap has no reclamation yet
// (docs/SSA-RC-RUNTIME.md: leak until a later reuse slice), so this is a no-op
// returning the data pointer — already in x0, so just ret. A real freelist
// return is the follow-up that makes the size arg (x1) live.
func emitBoxFreeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_box_free"))
	w("\tret") // return data (x0) unchanged; free is a no-op for now
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

// Single-word strings carry their byte length as a 4-byte field immediately
// before the data (length at [ptr-4]); heap strings also have the rc word at
// [ptr-8], literals an immortal sentinel there. The helpers below match that
// layout, the arm64 siblings of the x86-64 string helpers.

// emitStrLenHelper writes __str_len(ptr) -> i32: the byte length at [ptr-4].
func emitStrLenHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_len"))
	w("\tldur w0, [x0, #-4]")
	w("\tret")
}

// emitStrEqHelper writes __str_eq(a, b) -> i32: 1 if the two single-word strings
// are byte-equal, else 0. Fast paths on pointer identity and length mismatch,
// then a byte loop. Leaf.
func emitStrEqHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__str_eq"))
	w("\tcmp x0, x1")
	w("\tb.eq .Lssa_streq_eq") // same pointer → equal
	w("\tldur w2, [x0, #-4]")  // len a
	w("\tldur w3, [x1, #-4]")  // len b
	w("\tcmp w2, w3")
	w("\tb.ne .Lssa_streq_neq") // different lengths
	w("\tmov w4, #0")           // i = 0
	w(".Lssa_streq_loop:")
	w("\tcmp w4, w2")
	w("\tb.hs .Lssa_streq_eq") // i >= len → all bytes matched (unsigned)
	w("\tldrb w5, [x0, x4]")
	w("\tldrb w6, [x1, x4]")
	w("\tcmp w5, w6")
	w("\tb.ne .Lssa_streq_neq")
	w("\tadd w4, w4, #1")
	w("\tb .Lssa_streq_loop")
	w(".Lssa_streq_eq:")
	w("\tmov x0, #1")
	w("\tret")
	w(".Lssa_streq_neq:")
	w("\tmov x0, #0")
	w("\tret")
}

// emitStrConcatHelper writes __str_concat(a, b) -> new data pointer: bump-allocate
// a fresh length-prefixed string holding a's bytes then b's, and return its data
// pointer. Inline-allocates the rc-headed block (rc=1 at base+0, total length at
// base+4, data at base+8 — the same header ConstStr / heap strings use) and
// byte-copies each operand, so it needs no calls. Leaf.
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
	w("\tadd x6, x6, #7")
	w("\tand x6, x6, #-8") // x6 = base
	w("\tmov w7, #1")
	w("\tstr w7, [x6]")     // rc = 1
	w("\tstr w4, [x6, #4]") // len = total
	w("\tadd x7, x6, #8")
	w("\tadd x7, x7, x4") // new cursor = base + 8 + total
	w("\tstr x7, [x5]")
	w("\tadd x9, x6, #8") // x9 = data
	// Copy a's la bytes: [data + i] = [a + i].
	w("\tmov x10, #0")
	w(".Lssa_strcat_a:")
	w("\tcmp w10, w2")
	w("\tb.hs .Lssa_strcat_b")
	w("\tldrb w11, [x0, x10]")
	w("\tstrb w11, [x9, x10]")
	w("\tadd x10, x10, #1")
	w("\tb .Lssa_strcat_a")
	// Copy b's lb bytes after a: dest base = data + la.
	w(".Lssa_strcat_b:")
	w("\tadd x12, x9, x2") // x2 = la (zero-extended)
	w("\tmov x10, #0")
	w(".Lssa_strcat_bl:")
	w("\tcmp w10, w3")
	w("\tb.hs .Lssa_strcat_done")
	w("\tldrb w11, [x1, x10]")
	w("\tstrb w11, [x12, x10]")
	w("\tadd x10, x10, #1")
	w("\tb .Lssa_strcat_bl")
	w(".Lssa_strcat_done:")
	w("\tmov x0, x9") // return data
	w("\tret")
}

// emitStrDecHelper writes __fern_str_dec(ptr): the scope-exit drop for a
// string-valued local. Guarded (null / low-address / immortal-sentinel top bit —
// so it skips .rodata literals); reads the rc at [ptr-8]; rc<=1 leaks (no
// reclamation on the bump heap), else drops a shared reference. Leaf.
func emitStrDecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_str_dec"))
	w("\tcbz x0, .Lssa_strdec_ret")
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
// inserts at scope exit. The array element pointer carries a 16-byte header with
// its rc at [data-8] (cap@-12, rc@-8, len@-4). Guarded (null / low-address /
// static sentinel); rc<=1 leaks (unique — the bump heap doesn't reclaim, per
// docs/SSA-RC-RUNTIME.md) else drops a shared reference. The stride arg (x1) is
// unused until real reclamation. Leaf.
func emitArrDecHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__fern_arr_dec"))
	w("\tcbz x0, .Lssa_arrdec_ret")
	w("\tcmp x0, #0x10000")
	w("\tb.lo .Lssa_arrdec_ret")
	w("\tldur w2, [x0, #-8]")             // rc (x1 holds stride)
	w("\ttbnz w2, #31, .Lssa_arrdec_ret") // static sentinel
	w("\tcmp w2, #1")
	w("\tb.le .Lssa_arrdec_ret") // rc<=1: unique (leak) or already dropped
	w("\tsub w2, w2, #1")
	w("\tstur w2, [x0, #-8]")
	w(".Lssa_arrdec_ret:")
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

// emitArrIdxHelperN returns the emitter for a bounds-checked array-index helper
// of a given element stride (2^shift bytes): __arr_idx (stride 4), __arr_idx_1
// (byte), __arr_idx_2 (halfword), __arr_idx_8 (i64/pointer), __arr_idx_16
// (two-word string[]). Each compares idx against the length at [base-4] with a
// single unsigned compare (a negative idx is huge unsigned, so it fails too) and,
// on out-of-range, exits 134 — matching the native array-index trap / wasm's
// `unreachable`. Returns base + idx*stride; the caller's OpLoad reads the element.
// Leaf. The ok-label is namespaced by shift so all variants coexist in a module.
func emitArrIdxHelperN(name string, shift int) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		ok := fmt.Sprintf(".Lssa_arridx%d_ok", shift)
		w("")
		w("%s:", fnLabel(name))
		w("\tldur w2, [x0, #-4]") // len
		w("\tcmp w1, w2")
		w("\tb.lo %s", ok) // idx < len (unsigned)
		w("\tmov x0, #134")
		w("\tmov x8, #94") // exit_group
		w("\tsvc #0")
		w("%s:", ok)
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
// byte-copies the old elements, and returns the new data pointer. Unlike the
// native helper (which calls __fern_alloc / __fern_memcpy) this inlines a raw
// bump allocation and a byte-copy loop, so it is a leaf — mirroring __str_concat.
// x0=arr, w1=oldLen, w2=stride. The bump heap doesn't reclaim, so the old buffer
// leaks (docs/SSA-RC-RUNTIME.md).
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
	w("\tmul w7, w5, w2")
	w("\tadd w7, w7, w6") // w7 = allocSize = headerBytes + newCap*stride
	// Inline raw bump allocation of w7 bytes (no rc header — the array lays its
	// own cap/rc/len header past the headerBytes prefix).
	w("\tadrp x8, %s", heapPtrSym)
	w("\tadd x8, x8, #:lo12:%s", heapPtrSym) // x8 = &cursor
	w("\tldr x9, [x8]")
	w("\tadd x9, x9, #7")
	w("\tand x9, x9, #-8")       // x9 = base (8-aligned)
	w("\tadd x10, x9, w7, uxtw") // new cursor = base + allocSize
	w("\tstr x10, [x8]")
	w("\tadd x11, x9, w6, uxtw") // x11 = new_data = base + headerBytes
	w("\tsub x12, x11, #12")
	w("\tstr w5, [x12]") // cap = newCap
	w("\tmov w13, #1")
	w("\tstur w13, [x11, #-8]") // rc = 1
	w("\tstur w4, [x11, #-4]")  // len = newLen
	// Byte-copy oldLen*stride bytes from arr (x0) to new_data (x11).
	w("\tmul w14, w1, w2") // nbytes
	w("\tmov w15, #0")     // i
	w(".Lssa_apg_cp:")
	w("\tcmp w15, w14")
	w("\tb.hs .Lssa_apg_done")
	w("\tldrb w16, [x0, x15]")
	w("\tstrb w16, [x11, x15]")
	w("\tadd w15, w15, #1")
	w("\tb .Lssa_apg_cp")
	w(".Lssa_apg_done:")
	w("\tmov x0, x11") // return new_data
	w("\tret")
}

// emitAllocU8Helper writes __alloc_u8(n) -> data: allocate a fresh
// length-prefixed u8[] of n bytes and return the data pointer (past a 16-byte
// header; cap@-12, rc=1@-8, len@-4). The n data bytes are zero-filled to match
// the interpreter's zero-initialised u8[] (issue #2768: read-before-write
// callers like SHA padding rely on it). Unlike the native helper (which calls
// __fern_alloc) this inlines a raw bump allocation, so it is a leaf — mirroring
// __fern_arr_push_grow. n==0 falls through with a zero-iteration zero loop,
// yielding a valid header-only buffer whose len reads 0. x0=n, returns x0=data.
func emitAllocU8Helper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("__alloc_u8"))
	w("\tmov w1, w0")      // w1 = n (preserve across the bump)
	w("\tadd w2, w1, #16") // allocSize = n + 16-byte header
	// Inline raw bump allocation of w2 bytes (8-aligned base).
	w("\tadrp x8, %s", heapPtrSym)
	w("\tadd x8, x8, #:lo12:%s", heapPtrSym) // x8 = &cursor
	w("\tldr x9, [x8]")
	w("\tadd x9, x9, #7")
	w("\tand x9, x9, #-8")       // x9 = base (8-aligned)
	w("\tadd x10, x9, w2, uxtw") // new cursor = base + allocSize
	w("\tstr x10, [x8]")
	w("\tadd x0, x9, #16")     // x0 = data ptr (past 16-byte header)
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

// emitStrSliceHelper writes __str_slice(base, low, high) -> data: allocate a
// fresh length-prefixed string holding base[low:high]. Bounds-traps (exit 134)
// on low < 0, high > src_len, or low > high, matching the native helper. Like
// the other string helpers it inlines the bump allocation + byte-copy (no
// __fern_alloc / __fern_memcpy call) into a fresh single-word rc-headered string
// (rc=1@base, len@base+4, data@base+8), so it is a leaf. low/high arrive as i32;
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
	w("\tadd x6, x6, #7")
	w("\tand x6, x6, #-8") // x6 = base (8-aligned)
	w("\tmov w7, #1")
	w("\tstr w7, [x6]")         // rc = 1
	w("\tstr w4, [x6, #4]")     // len = new_len
	w("\tadd x8, x6, #8")       // x8 = data
	w("\tadd x9, x8, w4, uxtw") // new cursor = data + new_len
	w("\tstr x9, [x5]")
	// Copy new_len bytes from base+low (x0+x1) to data (x8).
	w("\tadd x10, x0, x1") // src = base + low
	w("\tmov w11, #0")     // i
	w(".Lssa_strslice_cp:")
	w("\tcmp w11, w4")
	w("\tb.hs .Lssa_strslice_done")
	w("\tldrb w12, [x10, x11]")
	w("\tstrb w12, [x8, x11]")
	w("\tadd w11, w11, #1")
	w("\tb .Lssa_strslice_cp")
	w(".Lssa_strslice_done:")
	w("\tmov x0, x8") // return data
	w("\tret")
	w(".Lssa_strslice_trap:")
	w("\tmov x0, #134")
	w("\tmov x8, #94") // exit_group
	w("\tsvc #0")
}

// emitStringFromBytesHelper writes string_from_bytes(bs) -> data: copy a u8[]
// payload into a fresh length-prefixed string and return its data pointer — the
// round-trip companion to s.bytes(). arm64ssa strings are single-word and
// rc-headered (rc=1@base+0, len@base+4, data@base+8 — the same layout ConstStr
// and __str_concat use), with no small-string inline optimisation, so this is a
// straight bump-allocate + byte-copy leaf. bs is the input u8[] data pointer;
// its byte length is at [bs-4]. x0=bs; returns x0=data.
func emitStringFromBytesHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("string_from_bytes"))
	w("\tldur w1, [x0, #-4]") // w1 = byte length of bs (zero-extends into x1)
	// Bump-allocate len+8: rc=1@base, len@base+4, data@base+8.
	w("\tadrp x2, %s", heapPtrSym)
	w("\tadd x2, x2, #:lo12:%s", heapPtrSym) // x2 = &cursor
	w("\tldr x3, [x2]")
	w("\tadd x3, x3, #7")
	w("\tand x3, x3, #-8") // x3 = base (8-aligned)
	w("\tmov w4, #1")
	w("\tstr w4, [x3]")     // rc = 1
	w("\tstr w1, [x3, #4]") // len
	w("\tadd x5, x3, #8")
	w("\tadd x5, x5, x1") // new cursor = base + 8 + len
	w("\tstr x5, [x2]")
	w("\tadd x6, x3, #8") // x6 = data
	// Copy len bytes from bs (x0) to data (x6).
	w("\tmov x7, #0")
	w(".Lssa_sfb_cp:")
	w("\tcmp w7, w1")
	w("\tb.hs .Lssa_sfb_done")
	w("\tldrb w8, [x0, x7]")
	w("\tstrb w8, [x6, x7]")
	w("\tadd x7, x7, #1")
	w("\tb .Lssa_sfb_cp")
	w(".Lssa_sfb_done:")
	w("\tmov x0, x6") // return data
	w("\tret")
}

// emitArrCowInplaceHelper writes __fern_arr_cow_inplace(arr, stride) -> buf —
// the copy-on-write helper behind `arr[i] = v`. Fast path: rc == 1 → the array
// is uniquely held, return it unchanged for an in-place store. Slow path (rc >
// 1, shared): decrement arr's rc (taking the caller's reference as we copy;
// skip a static sentinel whose rc word has the high bit set), bump-allocate a
// fresh buffer with the SAME cap+len, byte-copy the payload, write rc=1 on the
// new header, and return the new data pointer. Like __fern_arr_push_grow this
// inlines the bump allocation + byte-copy so it is a leaf (the native helper
// calls __fern_alloc / __fern_memcpy). x0=arr, w1=stride; returns x0=buf.
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
	w("\tmul w7, w4, w1")
	w("\tadd w7, w7, w6") // w7 = allocSize
	// Inline raw bump allocation of w7 bytes (8-aligned base).
	w("\tadrp x8, %s", heapPtrSym)
	w("\tadd x8, x8, #:lo12:%s", heapPtrSym) // x8 = &cursor
	w("\tldr x9, [x8]")
	w("\tadd x9, x9, #7")
	w("\tand x9, x9, #-8")       // x9 = base (8-aligned)
	w("\tadd x10, x9, w7, uxtw") // new cursor = base + allocSize
	w("\tstr x10, [x8]")
	w("\tadd x11, x9, w6, uxtw") // x11 = new_data = base + headerBytes
	w("\tsub x12, x11, #12")
	w("\tstr w4, [x12]") // cap
	w("\tmov w13, #1")
	w("\tstur w13, [x11, #-8]") // rc = 1
	w("\tstur w3, [x11, #-4]")  // len
	// Byte-copy len*stride bytes from arr (x0) to new_data (x11).
	w("\tmul w14, w3, w1") // nbytes
	w("\tmov w15, #0")     // i
	w(".Lssa_cow_cp:")
	w("\tcmp w15, w14")
	w("\tb.hs .Lssa_cow_done")
	w("\tldrb w16, [x0, x15]")
	w("\tstrb w16, [x11, x15]")
	w("\tadd w15, w15, #1")
	w("\tb .Lssa_cow_cp")
	w(".Lssa_cow_done:")
	w("\tmov x0, x11") // return new_data
	w("\tret")
}

// emitFunc writes one function: its label, a stack frame (spill slots, plus a
// call-save area and a saved-x30 slot when the function makes calls), each
// block's straight-line body under a namespaced label, and the terminators.
// The stack pointer stays fixed for the whole body — call-crossing registers are
// preserved in the reserved call-save area rather than by moving sp — so every
// slot access is a stable sp-relative offset.
func emitFunc(w func(string, ...any), name string, p *x86.Program, numAlloc int, strLabels map[string]string, sentLabels map[int64]string, fnIndex map[string]int) error {
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
			if in.Op == x86.Call || in.Op == x86.CallPair {
				lines, err := callLines(in, numAlloc, scratch, callSaveBase)
				if err != nil {
					return err
				}
				for _, l := range lines {
					w("\t%s", l)
				}
				continue
			}
			if in.Op == x86.CallIndirect {
				lines, err := callIndirectLines(in, numAlloc, callSaveBase)
				if err != nil {
					return err
				}
				for _, l := range lines {
					w("\t%s", l)
				}
				continue
			}
			if in.Op == x86.MakeEnv || in.Op == x86.MakeClosure {
				lines, err := closureLines(in, numAlloc, fnIndex)
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
		case x86.TRetPair:
			// AArch64 pair-return convention: tag in x0, payload in x1. A home may
			// already be x0/x1, so resolve the two moves as a parallel copy before
			// the frame teardown.
			for _, l := range resolveRegMoves(pairRetMoves(blk.Term.RetReg, blk.Term.RetReg2)) {
				w("\t%s", l)
			}
			if call {
				w("\tldr x30, [sp, #%d]", 8*lrSlot)
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

// funcHasCall reports whether any block of p contains a direct call.
func funcHasCall(p *x86.Program) bool {
	for _, blk := range p.Blocks {
		for _, in := range blk.Insts {
			if in.Op == x86.Call || in.Op == x86.CallPair || in.Op == x86.CallIndirect {
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
	// s0 — the first scratch register — stages the pair-return payload. It is
	// above the allocatable file (never in the save set) and distinct from the
	// result-capture scratch (s3), so neither the restores nor the tag placement
	// clobber it. AArch64 returns a pair in x0 (tag) / x1 (payload).
	s0 := numAlloc
	var out []string
	for k, r := range saved {
		out = append(out, fmt.Sprintf("str %s, [sp, #%d]", xreg(r), 8*(callSaveBase+k)))
	}
	out = append(out, argMoveLines(in.ArgLocs)...)
	out = append(out, fmt.Sprintf("bl %s", fnLabel(in.Callee)))
	out = append(out, fmt.Sprintf("mov %s, x0", xreg(scratch))) // capture tag / result
	if in.Op == x86.CallPair {
		out = append(out, fmt.Sprintf("mov %s, x1", xreg(s0))) // capture payload
	}
	for k := len(saved) - 1; k >= 0; k-- {
		out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(saved[k]), 8*(callSaveBase+k)))
	}
	out = append(out, fmt.Sprintf("mov %s, %s", xreg(in.Dst), xreg(scratch))) // place tag / result
	// An i32 return needs the sign-extend mask (the ABI defines only the low 32
	// bits), but a runtime helper returning a full 8-byte value (an f64 whose high
	// bits are its exponent, or a heap pointer) must skip it — the SSA lift can't
	// tag helper return widths (no ssa.Func), so the backend knows them by name.
	if !helperReturns64[in.Callee] {
		out = append(out, maskFix(in.Dst, in.W)...)
	}
	if in.Op == x86.CallPair {
		// Placed after the tag (whose home may be the payload's capture reg s3) so
		// the tag is out of s3 before Dst2 (typically s3) is written.
		out = append(out, fmt.Sprintf("mov %s, %s", xreg(in.Dst2), xreg(s0))) // place payload
	}
	return out, nil
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

// fnTableSym labels the module's function-address dispatch table: one .quad per
// function in the module's (sorted) emission order — indexed by the same fn_idx a
// closure cell carries. OpCallIndirect resolves its callee by loading
// table[fn_idx].
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
// wraps it in a 16-byte {fn_idx, env_ptr} cell. Both blocks carry the rc header
// (rc=1 at base+0, data at base+8) so they drop through the RC helpers. Bump
// allocation forms the cursor address in a register (unlike x86's RIP-relative
// mem-immediate), so each alloc draws two temporaries from the scratch pool
// avoiding the destination and (for the cell) the env register that must survive.
func closureLines(in x86.Inst, numAlloc int, fnIndex map[string]int) ([]string, error) {
	stage := numAlloc + 3 // s3 — capture value staging
	envReg := numAlloc    // s0 — env block held across the cell alloc
	var out []string
	alloc := func(dst int, bytes int64, avoid int) {
		var t []int
		for _, r := range []int{numAlloc + 3, numAlloc + 2, numAlloc + 1, numAlloc} {
			if r == dst || r == avoid {
				continue
			}
			t = append(t, r)
			if len(t) == 2 {
				break
			}
		}
		d, addr, tmp, wtmp := xreg(dst), xreg(t[0]), xreg(t[1]), wreg(t[1])
		out = append(out,
			fmt.Sprintf("adrp %s, %s", addr, heapPtrSym),
			fmt.Sprintf("add %s, %s, #:lo12:%s", addr, addr, heapPtrSym),
			fmt.Sprintf("ldr %s, [%s]", d, addr), // cursor
			fmt.Sprintf("add %s, %s, #7", d, d),
			fmt.Sprintf("and %s, %s, #-8", d, d), // base (8-aligned)
			fmt.Sprintf("mov %s, #1", wtmp),
			fmt.Sprintf("str %s, [%s]", wtmp, d), // rc = 1
			fmt.Sprintf("add %s, %s, #%d", tmp, d, bytes+8),
			fmt.Sprintf("str %s, [%s]", tmp, addr), // cursor = base + bytes + 8
			fmt.Sprintf("add %s, %s, #8", d, d),    // data = base + 8
		)
	}
	offs, sizes, envBytes := captureEnvLayout(in)
	storeCaps := func(base int) {
		for i, l := range in.ArgLocs {
			if l.IsReg {
				out = append(out, fmt.Sprintf("mov %s, %s", xreg(stage), xreg(l.Reg)))
			} else {
				out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(stage), 8*l.Slot))
			}
			if sizes[i] == 4 {
				out = append(out, fmt.Sprintf("str %s, [%s, #%d]", wreg(stage), xreg(base), offs[i]))
			} else {
				out = append(out, fmt.Sprintf("str %s, [%s, #%d]", xreg(stage), xreg(base), offs[i]))
			}
		}
	}
	if in.Op == x86.MakeEnv {
		alloc(in.Dst, envBytes, -1)
		storeCaps(in.Dst)
		return out, nil
	}
	idx, ok := fnIndex[in.Callee]
	if !ok {
		return nil, fmt.Errorf("arm64ssa: MakeClosure target %q not in module", in.Callee)
	}
	alloc(envReg, envBytes, -1) // env block -> s0
	storeCaps(envReg)
	alloc(in.Dst, 16, envReg) // {fn_idx, env_ptr} cell -> Dst, preserving env in s0
	out = append(out,
		fmt.Sprintf("mov %s, #%d", xreg(stage), idx),
		fmt.Sprintf("str %s, [%s, #0]", xreg(stage), xreg(in.Dst)),
		fmt.Sprintf("str %s, [%s, #8]", xreg(envReg), xreg(in.Dst)),
	)
	return out, nil
}

// callIndirectLines renders a closure dispatch. in.IdxLoc points at a
// {fn_idx, env_ptr} cell: fn_idx (at +0) indexes the function-address table
// (fnTableSym), env_ptr (at +8) is appended as the callee's LAST argument
// (docs/SSA-CLOSURE-DISPATCH.md). The scratch registers (s0..s3 = x12..x15) sit
// above both the argument registers (x0..x7) and the allocatable homes
// (x0..x11), so the resolved target (s1) and the env (s0) survive the argument
// parallel-move untouched. Caller-saved live-across registers are preserved in
// the call-save area exactly as callLines does.
func callIndirectLines(in x86.Inst, numAlloc, callSaveBase int) ([]string, error) {
	if len(in.ArgLocs)+1 > argRegCount {
		return nil, fmt.Errorf("arm64ssa: indirect call supports up to %d args incl. env, got %d", argRegCount, len(in.ArgLocs)+1)
	}
	s0, s1, s2, s3 := numAlloc, numAlloc+1, numAlloc+2, numAlloc+3
	var out []string
	// Stage the cell pointer, then read env (+8) and fn_idx (+0) before any
	// argument register is disturbed.
	if in.IdxLoc.IsReg {
		out = append(out, fmt.Sprintf("mov %s, %s", xreg(s2), xreg(in.IdxLoc.Reg)))
	} else {
		out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(s2), 8*in.IdxLoc.Slot))
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
	for k, r := range saved {
		out = append(out, fmt.Sprintf("str %s, [sp, #%d]", xreg(r), 8*(callSaveBase+k)))
	}
	// Move the explicit args plus the env (as the final argument) into x0..x{n}.
	argsWithEnv := append(append([]x86.Loc{}, in.ArgLocs...), x86.Loc{IsReg: true, Reg: s0})
	out = append(out, argMoveLines(argsWithEnv)...)
	out = append(out, fmt.Sprintf("blr %s", xreg(s1)))
	out = append(out, fmt.Sprintf("mov %s, x0", xreg(s3))) // capture result
	for k := len(saved) - 1; k >= 0; k-- {
		out = append(out, fmt.Sprintf("ldr %s, [sp, #%d]", xreg(saved[k]), 8*(callSaveBase+k)))
	}
	out = append(out, fmt.Sprintf("mov %s, %s", xreg(in.Dst), xreg(s3))) // place result
	out = append(out, maskFix(in.Dst, in.W)...)
	return out, nil
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
	case x86.FConst:
		return fConstSeq(in), nil
	case x86.FBin:
		return fBinSeq(in)
	case x86.FCmp:
		return fCmpSeq(in)
	case x86.FConv:
		return fConvSeq(in)
	case x86.UnNeg:
		return append([]string{fmt.Sprintf("neg %s, %s", xreg(in.Dst), xreg(in.Dst))}, maskFix(in.Dst, in.W)...), nil
	case x86.UnOp:
		return unOpSeq(in)
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
func divShiftSeq(in x86.Inst, scratch int) []string {
	d, s := xreg(in.Dst), xreg(in.Src)
	// The UNSIGNED ops (lsr / udiv / unsigned rem) must not see the sign-extended
	// high 32 bits of a 32-bit operand: a u32 with bit 31 set is stored sign-
	// extended (all 1s in bits 32-63, per maskFix), so a 64-bit logical shift or
	// unsigned divide would drag those bits in and miscompute (the u32 `>>` bug
	// that broke SHA-256). At 32-bit width (in.W != 64) render them in the 32-bit
	// w-form, which reads only the low 32 bits and zero-extends the result; the
	// trailing maskFix then re-sign-extends to the storage convention. Signed and
	// 64-bit ops keep the full-width x-form (their sign-extended operands are
	// already correct).
	unsigned32 := in.W != 64
	dw, sw := wreg(in.Dst), wreg(in.Src)
	var out []string
	switch in.K {
	case ssa.OpShl:
		out = []string{fmt.Sprintf("lsl %s, %s, %s", d, d, s)}
	case ssa.OpShr:
		out = []string{fmt.Sprintf("asr %s, %s, %s", d, d, s)} // arithmetic (signed)
	case ssa.OpShrU:
		if unsigned32 {
			out = []string{fmt.Sprintf("lsr %s, %s, %s", dw, dw, sw)} // logical, 32-bit
		} else {
			out = []string{fmt.Sprintf("lsr %s, %s, %s", d, d, s)} // logical, 64-bit
		}
	case ssa.OpDiv:
		out = []string{fmt.Sprintf("sdiv %s, %s, %s", d, d, s)}
	case ssa.OpDivU:
		if unsigned32 {
			out = []string{fmt.Sprintf("udiv %s, %s, %s", dw, dw, sw)} // 32-bit
		} else {
			out = []string{fmt.Sprintf("udiv %s, %s, %s", d, d, s)} // 64-bit
		}
	case ssa.OpRem, ssa.OpRemU:
		if in.K == ssa.OpRemU && unsigned32 {
			q := wreg(scratch)
			out = []string{
				fmt.Sprintf("udiv %s, %s, %s", q, dw, sw),         // q = d / s (32-bit)
				fmt.Sprintf("msub %s, %s, %s, %s", dw, q, sw, dw), // d = d - q*s
			}
		} else {
			q := xreg(scratch)
			div := "sdiv"
			if in.K == ssa.OpRemU {
				div = "udiv"
			}
			out = []string{
				fmt.Sprintf("%s %s, %s, %s", div, q, d, s),     // q = d / s
				fmt.Sprintf("msub %s, %s, %s, %s", d, q, s, d), // d = d - q*s
			}
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
func fCmpSeq(in x86.Inst) ([]string, error) {
	cc, ok := fcondCode(in.K)
	if !ok {
		return nil, fmt.Errorf("arm64ssa: float compare %v not supported yet", in.K)
	}
	d, s := xreg(in.Dst), xreg(in.Src)
	return []string{
		fmt.Sprintf("fmov d0, %s", d),
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
	case ssa.OpFToIS:
		// float -> int, truncating toward zero (Go semantics).
		return append([]string{fmt.Sprintf("fmov d0, %s", d), fmt.Sprintf("fcvtzs %s, d0", d)}, maskFix(in.Dst, in.W)...), nil
	case ssa.OpFToIU:
		return append([]string{fmt.Sprintf("fmov d0, %s", d), fmt.Sprintf("fcvtzu %s, d0", d)}, maskFix(in.Dst, in.W)...), nil
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

// fcondCode maps a float comparison to its AArch64 condition mnemonic. fcmp's
// C/Z flags follow the unsigned interpretation for ordered finite operands, so
// these mirror the integer unsigned codes (and x86's ucomisd setcc choices).
func fcondCode(k ssa.OpKind) (string, bool) {
	switch k {
	case ssa.OpFEq:
		return "eq", true
	case ssa.OpFNe:
		return "ne", true
	case ssa.OpFLt:
		return "lo", true
	case ssa.OpFLe:
		return "ls", true
	case ssa.OpFGt:
		return "hi", true
	case ssa.OpFGe:
		return "hs", true
	}
	return "", false
}

// unOpSeq renders the UnOp unary transforms. OpNot is a zero-test materialised
// with cset (clean 0/1, no mask); the extends map onto the AArch64 sign/zero
// extraction instructions, which already produce the model's width-masked value.
func unOpSeq(in x86.Inst) ([]string, error) {
	d := in.Dst
	switch in.K {
	case ssa.OpNot:
		return []string{fmt.Sprintf("cmp %s, #0", xreg(d)), fmt.Sprintf("cset %s, eq", xreg(d))}, nil
	case ssa.OpTrunc, ssa.OpExtendS:
		return []string{fmt.Sprintf("sxtw %s, %s", xreg(d), wreg(d))}, nil // sign-extend low 32
	case ssa.OpExtendU:
		return []string{fmt.Sprintf("mov %s, %s", wreg(d), wreg(d))}, nil // 32-bit mov zero-extends
	case ssa.OpExtend8S:
		return []string{fmt.Sprintf("sxtb %s, %s", xreg(d), wreg(d))}, nil
	case ssa.OpExtend16S:
		return []string{fmt.Sprintf("sxth %s, %s", xreg(d), wreg(d))}, nil
	}
	return nil, fmt.Errorf("arm64ssa: unary op %v not supported yet", in.K)
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
