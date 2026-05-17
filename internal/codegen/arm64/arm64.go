// Package arm64 emits ARM64 (aarch64) assembly from a checked
// + monomorphised lang program. Two flavours, selected by
// Options.Darwin: Linux ELF for arm64 hosts (Raspberry Pi 4+,
// AWS Graviton, Android) and Mach-O for native Apple Silicon
// Macs. Shares the IR layer with the WASM backend; emits its
// own ISA + syscall wiring.
//
// ABI: AAPCS64. Args in x0..x7; return value in x0; frame
// pointer x29; link register x30; stack pointer must stay
// 16-byte aligned at memory accesses (Linux sets SCTLR.SA so
// any sp-relative access with mis-aligned sp faults).
//
// Operand stack: simulated on the physical sp via paired
// push/pop. Each push uses `str x0, [sp, #-16]!` — a 16-byte
// stride (8 bytes wasted per slot) keeps sp 16-aligned for
// the next memory access. Future PR can switch to packed
// pairs (stp / ldp) once the codegen consistently knows when
// two consecutive pushes happen.
//
// Syscalls: x8 holds the syscall number, x0..x5 carry args,
// `svc #0` traps to the kernel. Numbers come from the
// asm-generic table (Linux arm64 has no legacy renumbering
// — the same numbers apply to every distribution).
package arm64

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/treeshake"
)

// Linux arm64 syscall numbers from the asm-generic table.
// Only what the runtime needs at this stage.
const (
	sysRead      = 63
	sysWrite     = 64
	sysClose     = 57
	sysOpenat    = 56
	sysFstat     = 80
	sysExit      = 93
	sysExitGroup = 94
	sysMmap      = 222
	sysGetrandom = 278
	sysSocket    = 198
	sysBind      = 200
	sysListen    = 201
	sysAccept    = 202
)

// Darwin BSD syscall numbers (xnu/bsd/kern/syscalls.master).
// Completely disjoint from the Linux table above. Darwin uses
// `svc #0x80` with the number in x16, and on error returns
// +errno in x0 with the C flag set (vs Linux's -errno in x0).
const (
	darRead       = 3
	darWrite      = 4
	darClose      = 6
	darExit       = 1
	darAccept     = 30
	darSocket     = 97
	darBind       = 104
	darListen     = 106
	darMmap       = 197
	darOpenat     = 463
	darGetentropy = 500
)

// linuxDarwinSysno maps a logical syscall name to (Linux, Darwin)
// numbers. Used by syscall() to pick the right immediate. Only
// includes syscalls whose ABI matches closely enough that
// substituting the number is the only platform difference —
// `read` / `write` / `close` / `openat` / `mmap` / `socket`
// family all qualify. `exit_group` maps to Darwin's `exit`
// (Darwin has no thread-group exit; the process-wide single-
// threaded variant suffices for our `-nostdlib` binaries).
//
// Helpers whose shape differs across platforms (`fstat` —
// stat64 vs stat struct layout differs; `getrandom` —
// Darwin's getentropy is chunked at 256 bytes per call) live
// in `linuxOnlySysno` below or get branched inline in their
// emitter.
var linuxDarwinSysno = map[string][2]int{
	"read":       {sysRead, darRead},
	"write":      {sysWrite, darWrite},
	"close":      {sysClose, darClose},
	"socket":     {sysSocket, darSocket},
	"bind":       {sysBind, darBind},
	"listen":     {sysListen, darListen},
	"accept":     {sysAccept, darAccept},
	"openat":     {sysOpenat, darOpenat},
	"exit":       {sysExit, darExit},
	"exit_group": {sysExitGroup, darExit},
	"mmap":       {sysMmap, darMmap},
}

// linuxOnlySysno carries syscalls whose Linux number we know
// but whose Darwin equivalent doesn't exist with the same ABI
// shape — `fstat` (Darwin's struct stat64 has different field
// offsets), `getrandom` (Darwin uses chunked getentropy). The
// `syscall()` helper emits the Linux form when `!g.darwin`
// and panics at codegen time on Darwin, so a helper that
// hasn't been ported to Darwin surfaces visibly when the
// driver builds with `-target arm64-darwin` instead of
// silently producing wrong asm.
var linuxOnlySysno = map[string]int{
	"fstat":     sysFstat,
	"getrandom": sysGetrandom,
}

// regArgs is the AAPCS64 register-argument count: args 0..7
// arrive in x0..x7. Anything beyond that goes through the
// caller's stack frame. 8 register-arg slots is enough to
// keep most user functions register-only.
const regArgs = 8

// Options tunes the emit. Currently empty.
type Options struct {
	// Darwin selects Mach-O / Apple-style assembly conventions
	// (leading-underscore symbol prefix `_<name>`, macOS BSD
	// syscall numbers, `svc #0x80` with x16 as the syscall
	// number register). Disabled by default (Linux ELF). The
	// driver flips this on for `-target arm64-darwin`. Real
	// Mach-O Object emission happens at the clang+lld step;
	// the asm we produce is the GAS-flavoured cross-platform
	// shape both Linux's `as` and macOS's `clang -c -arch
	// arm64` accept.
	Darwin bool
}

// Emit produces the assembly text for prog.
func Emit(prog *ast.Program, info *checker.Info) (string, error) {
	return EmitWithOptions(prog, info, Options{})
}

// EmitWithOptions returns the assembly text for prog. Lowers
// each surviving (post-treeshake) function via the IR layer.
func EmitWithOptions(prog *ast.Program, info *checker.Info, opts Options) (string, error) {
	// arm64 SSO native flip (in progress; see
	// `docs/SSO-NATIVE-FLIP-STATUS.md`): opt into the two-word
	// `(data, len)` string ABI in the IR layer. Resets after
	// emission so the package-level flag doesn't leak to other
	// codegen calls (e.g. when wasm and arm64 share a test
	// fixture).
	prevOverride := ast.TwoWordOverride
	ast.TwoWordOverride = true
	defer func() { ast.TwoWordOverride = prevOverride }()

	treeshake.Run(prog)
	ip, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		return "", err
	}
	// Tail-call optimisation. Rewrites self-tail calls into
	// a parameter rebind + backward branch to a wrapped
	// outer loop — self-recursive functions run in O(1)
	// stack depth. Backported from the x86-64 backend (the
	// pass's first consumer); shape is target-agnostic so
	// the wire-up is the same one-liner.
	ir.TailCallOptimize(ip)
	// Defunctionalise + ElideClosurePair turn many indirect
	// closure calls into direct calls (with env_ptr passed
	// explicitly). Native closure pair: 16 bytes, env_ptr
	// at offset 8 — see Defunctionalise's pairEnvOffset doc.
	ir.Defunctionalise(ip, 8)
	ir.ElideClosurePair(ip, 8)
	// Zero-capture closures escaping past ElideClosurePair (e.g.
	// passed as a function-typed argument — `tryThing(my_lambda)`)
	// rewrite to OpConstFunc so the value materialises as an
	// `adrp + add` of a static `.rodata` cell instead of a
	// 16-byte heap-allocated pair.
	ir.InlineZeroCaptureClosures(ip)
	g := &generator{info: info, stringLabel: map[string]string{}, funcs: map[string]*ast.FuncDecl{}, darwin: opts.Darwin}
	for _, fn := range prog.Funcs {
		g.funcs[fn.Name] = fn
	}
	// Pre-scan IR functions to set use-flags that emitStartRuntime
	// reads. emitStartRuntime runs before the per-function walk
	// so any flag set inside emitOp wouldn't influence the
	// prologue. For args() / env() the prologue needs to know
	// in advance so it can stash argc / argv / envp from the
	// kernel-delivered stack before main runs.
	//
	// We also detect calls to runtime helpers that haven't been
	// ported to arm64-darwin so the driver can surface a clean
	// error BEFORE codegen tries to emit a `linuxOnlySysno`
	// number (fstat, etc.) and panics at `g.syscall(...)`. The
	// per-call detection lives here because the use-flag setter
	// in the regular emit walk runs AFTER the prelude / prologue
	// — too late to fail cleanly.
	var darwinIncompat []string
	seenDarwinIncompat := map[string]bool{}
	for _, fn := range ip.Funcs {
		for _, op := range fn.Ops {
			if op.Kind == ir.OpMakeClosure || op.Kind == ir.OpMakeEnv {
				// Closure env block (+ optional pair) come
				// from __lang_alloc.
				g.usesAlloc = true
				continue
			}
			if op.Kind != ir.OpCallDirect {
				continue
			}
			switch op.Str {
			case "args":
				g.usesArgs = true
			case "env":
				g.usesEnv = true
			case "read_file":
				// `__lang_read_file` uses fstat — Linux-only
				// (see linuxOnlySysno). The Darwin port would
				// need an inline `stat64` syscall + struct
				// layout that diverges from Linux's; until
				// it ships, reject the call here.
				if g.darwin && !seenDarwinIncompat["read_file"] {
					darwinIncompat = append(darwinIncompat, "read_file")
					seenDarwinIncompat["read_file"] = true
				}
			}
		}
	}
	if len(darwinIncompat) > 0 {
		// Stable order so the error message is reproducible
		// across runs even when the map's iteration order
		// shifts (single entry today, but adding more later
		// shouldn't tank reproducibility).
		return "", fmt.Errorf(
			"arm64-darwin: the following runtime helper(s) are not yet ported and would emit a Linux-only syscall: %v\n"+
				"see docs/BACKEND-PARITY.md for the per-helper Darwin status",
			darwinIncompat,
		)
	}
	g.line(`.arch armv8-a`)
	g.line(`.text`)
	g.emitStartRuntime()
	for i, fn := range prog.Funcs {
		if err := g.emitFunc(fn, ip.Funcs[i]); err != nil {
			return "", err
		}
	}
	// The Reader/Writer runtime is emitted as a single bundle:
	// every helper (open_*, read_line, read_chunk, close,
	// write, make_handle, stdin/stdout/stderr) ships together.
	// That means whenever the bundle is pulled in, its
	// callees must be too — __lang_alloc, __lang_memcpy, and
	// the IoError box constructor (`.LStr_ioerr_empty` lives
	// there) all show up indirectly. usesReaderWriter is set
	// during per-function emit (above), so we propagate here
	// before the runtime-gate checks below.
	if g.usesReaderWriter {
		g.usesIoError = true
		g.usesMemcpy = true
		g.usesAlloc = true
	}
	if g.usesAlloc {
		g.emitAllocRuntime()
	}
	if g.usesMemcpy {
		g.emitMemcpyRuntime()
	}
	if g.usesSliceMake {
		g.emitSliceMakeRuntime()
	}
	if g.usesStrcmp {
		g.emitStrcmpRuntime()
	}
	if g.usesStrcat {
		g.emitStrcatRuntime()
	}
	if g.usesRawIntPokes {
		g.emitRawIntPokesRuntime()
	}
	if g.usesMemset {
		g.emitMemsetRuntime()
	}
	if g.usesTcp {
		g.emitTcpListenRuntime()
		g.emitTcpAcceptRuntime()
		g.emitTcpRecvRuntime()
		g.emitTcpSendRuntime()
		g.emitTcpCloseRuntime()
	}
	if g.usesStrSlice {
		g.emitStrSliceRuntime()
	}
	if g.usesEnv {
		g.emitEnvRuntime()
	}
	if g.usesAllocU8 {
		g.emitAllocU8Runtime()
	}
	if g.usesStringFromBytes {
		g.emitStringFromBytesRuntime()
	}
	if g.usesPuts {
		g.emitPutsRuntime()
	}
	if g.usesWrite {
		g.emitWriteRuntime()
	}
	if g.usesPutchar {
		g.emitPutcharRuntime()
	}
	if g.usesEprint {
		g.emitEprintRuntime()
	}
	if g.usesExit {
		g.emitExitRuntime()
	}
	if g.usesArgs {
		g.emitArgsRuntime()
	}
	if g.usesArena {
		g.emitArenaRuntime()
	}
	if g.usesReadLine {
		g.emitReadLineRuntime()
	}
	if g.usesStdin {
		g.emitStdinRuntime()
	}
	if g.usesRandomBytes {
		g.emitRandomBytesRuntime()
	}
	if g.usesIoError {
		g.emitIoErrorRuntime()
	}
	if g.usesReadFile {
		g.emitReadFileRuntime()
	}
	if g.usesWriteFile {
		g.emitWriteFileRuntime()
	}
	if g.usesReaderWriter {
		// Reader / Writer struct constructors (stdin/stdout/
		// stderr + open_reader/writer/appender) + the method
		// runtimes (read_line / read_chunk / close / write).
		// Shares the 4 KiB `__lang_read_line_buf` scratch the
		// stdin-only read_line used, plus __lang_io_error for
		// the Some(IoError) / None error path.
		g.emitReaderWriterRuntime()
	}
	g.emitDataSections()
	if !g.darwin {
		// `.note.GNU-stack` is an ELF-only directive — it
		// marks the program's stack as non-executable. Mach-O
		// has the equivalent via header flags; the assembler
		// here rejects the directive on Darwin.
		g.line(`.section .note.GNU-stack,"",%progbits`)
	}
	return g.out.String(), nil
}

// emitDataSections writes `.rodata` (interned string literals)
// and `.bss` (the bump-allocator cursor + heap-end sentinel).
// All entries are gated on usage so unused programs pay
// nothing — `.bss` is omitted entirely when the allocator
// isn't pulled in.
func (g *generator) emitDataSections() {
	g.line("")
	// Static closure-pair cells for OpConstFunc-referenced
	// functions. Each cell holds {fn_ptr (8B), env=0 (8B)}.
	if len(g.constFuncCells) > 0 {
		if g.darwin {
			g.line(`.section __TEXT,__const`)
		} else {
			g.line(`.section .rodata`)
		}
		names := make([]string, 0, len(g.constFuncCells))
		for n := range g.constFuncCells {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			g.line(".align 3")
			g.label(fmt.Sprintf("__closure_cell_%s", name))
			g.line(fmt.Sprintf("\t.quad %s", name))
			g.line("\t.quad 0")
		}
	}
	if g.darwin {
		// Mach-O read-only data section. We deliberately do NOT
		// use `__TEXT,__cstring,cstring_literals` here: ld64
		// atomises that section per NUL-terminated entry and
		// re-orders atoms across the whole link, which would
		// detach our 4-byte length-prefix `.4byte N` from its
		// paired `.LStr_X: .asciz` data. `__TEXT,__const` is
		// the canonical read-only constants section — bytes
		// stay in source order, no atomisation, no dedup.
		g.line(`.section __TEXT,__const`)
	} else {
		g.line(`.section .rodata`)
	}
	for _, s := range g.stringOrder {
		// Two-word ABI: data segment holds just the bytes (no
		// 4-byte length prefix — length lives on the operand
		// stack as the second word of the (data, len) pair).
		// `.asciz` adds a trailing NUL byte; harmless because
		// runtime readers consume length-bounded bytes.
		g.line(`.align 2`)
		g.label(g.stringLabel[s])
		g.line("\t.asciz " + escapeForGAS(s))
	}
	// Empty-string sentinel. The runtime helpers don't read
	// the bytes here (length is 0); the label exists for the
	// rare path that needs an `adr` target for an empty data
	// pointer. Kept at a fixed location for emitStrEmpty's
	// inline-encoded sentinel reference.
	g.line(`.align 2`)
	g.label(".LStr_Empty")
	g.line(`	.asciz ""`)
	if g.usesArrEmpty {
		// Empty u8[] sentinel — __alloc_u8(0) returns this
		// address instead of allocating a fresh 4-byte length-
		// only buffer. Same shape as .LStr_Empty (4 bytes of
		// zero length prefix + a data byte) but kept distinct
		// so the array seam can evolve independently of the
		// string seam.
		g.line(`.align 2`)
		g.line(`	.4byte 0`)
		g.label(".LArr_Empty")
		g.line(`	.byte 0`)
	}
	if len(g.enumSentinelTags) > 0 {
		// Per-tag enum sentinels. One 4-byte symbol per unique
		// tag value referenced by any payloadless-variant
		// construction (Option.None → tag 1, IoError.Interrupted
		// → tag 4, JsonValue.JNull → tag 0, etc.). Match / try
		// sites read `[ptr + 0]` and get the tag, the same as
		// heap-allocated boxes.
		tags := make([]int, 0, len(g.enumSentinelTags))
		for t := range g.enumSentinelTags {
			tags = append(tags, t)
		}
		sort.Ints(tags)
		for _, t := range tags {
			g.line(`.align 2`)
			g.label(fmt.Sprintf(".LEnumSentinel_%d", t))
			g.line(fmt.Sprintf(`	.4byte %d`, t))
		}
	}
	if g.usesPuts || g.usesEprint {
		// Single newline byte emitted into the same section as
		// the string literals. __lang_puts / __lang_eprint
		// write `s` followed by a 1-byte write of this label.
		// We use `.asciz` rather than `.byte 10` so Mach-O's
		// `cstring_literals` attribute (which requires NUL-
		// terminated strings) accepts the entry — the trailing
		// NUL is harmless, the write only reads the first byte.
		g.label(".LLangNewline")
		g.line(`	.asciz "\n"`)
	}
	if g.usesAlloc || g.usesEnv || g.usesArgs || g.usesReadLine || g.usesStrIdx {
		g.line("")
		if g.darwin {
			// Mach-O zero-initialised data lives in
			// __DATA,__bss. The `zero_fill` directive shape
			// (`.zerofill SEGMENT,SECTION,SYMBOL,SIZE`) is the
			// idiomatic way but a `.section` + `.space` pair
			// also works and keeps the code path uniform with
			// the ELF emit.
			g.line(`.section __DATA,__bss`)
		} else {
			g.line(`.section .bss`)
		}
	}
	if g.usesAlloc {
		// Two-cursor bump allocator. Mode byte at +0 (0 =
		// arena, 1 = persistent); each region has its own
		// `_ptr` / `_end` pair that __lang_alloc bumps. See
		// emitAllocRuntime for the design rationale.
		g.line(`.align 3`)
		g.label("__lang_heap_ptr")
		g.line(`	.quad 0`)
		g.line(`.align 3`)
		g.label("__lang_heap_end")
		g.line(`	.quad 0`)
		g.line(`.align 3`)
		g.label("__lang_persistent_ptr")
		g.line(`	.quad 0`)
		g.line(`.align 3`)
		g.label("__lang_persistent_end")
		g.line(`	.quad 0`)
		g.line(`.align 2`)
		g.label("__lang_alloc_mode")
		g.line(`	.byte 0`)
	}
	if g.usesEnv {
		g.line(`.align 3`)
		g.label("__lang_envp")
		g.line(`	.quad 0`)
	}
	if g.usesArgs {
		g.line(`.align 3`)
		g.label("__lang_argc")
		g.line(`	.quad 0`)
		g.line(`.align 3`)
		g.label("__lang_argv")
		g.line(`	.quad 0`)
		g.line(`.align 3`)
		g.label("__lang_args_cache")
		g.line(`	.quad 0`)
	}
	if g.usesReadLine || g.usesReaderWriter {
		// 4 KiB scratch buffer for the byte-by-byte read loop.
		// Shared by stdin-only `__lang_read_line` and the new
		// Reader-receiving `__lang_reader_read_line`. Both
		// helpers run a single-byte read until '\n' / 4 KiB /
		// EOF, so they can't trample each other.
		g.line(`.align 3`)
		g.label("__lang_read_line_buf")
		g.line(`	.space 4096`)
	}
	if g.usesStrIdx {
		// SSO inline strings ride in a 64-bit register and don't
		// have a usable memory address until materialised. The
		// __str_idx index helper spills inline values to this
		// global scratch slot before computing `&scratch + idx`
		// so OpLoadByte can read a byte from a real address.
		// 16-byte slot under the two-word ABI (room for both
		// `data` and `len` halves of an inline-form string);
		// 8 bytes was enough for the legacy single-pointer
		// shape.
		g.line(`.align 3`)
		g.label("__lang_str_idx_scratch")
		g.line(`	.quad 0`)
		g.line(`	.quad 0`)
	}
}

// emitAllocRuntime emits `__lang_alloc(size: i64) -> i64`
// using mmap2 (sysMmap = 222) and 64-bit pointer arithmetic.
// First call lazily reserves the heap arena via mmap; later
// calls bump the cursor.
//
// Two-cursor allocator: a 1-byte mode flag at
// `__lang_alloc_mode` selects which region to bump.
//
//	mode == 0 → arena cursor (__lang_heap_ptr / _end).
//	            arena_save / arena_restore manipulate this
//	            pair, so the region is per-request scoped
//	            in HTTP-handler programs (auto-main wraps
//	            each request in save/restore).
//	mode == 1 → persistent cursor (__lang_persistent_ptr /
//	            _end). Never reclaimed; lives as long as
//	            the process. The IR's OpPersistentSet /
//	            OpPersistentRestore toggle the mode flag
//	            around state-rooted method calls so any
//	            internal allocs (e.g. Map.set's grow path)
//	            land in this region and survive the
//	            arena_restore at request boundary.
//
// Each region gets its own lazy-mmap'd 64 MiB virtual
// reservation. Linux's address hint differs per region
// (0x10000000 arena, 0x20000000 persistent) so they don't
// collide; both fit in 32 bits so the lang prelude's
// __store_i32 / __load_i32 round-trip pointers without
// truncation.
//
// Bump-only — no free. The OS reclaims everything at process
// exit.
func (g *generator) emitAllocRuntime() {
	const heapBytes = 64 * 1024 * 1024
	g.line("")
	g.line(".global __lang_alloc")
	g.typeDirective("__lang_alloc")
	g.label("__lang_alloc")
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	g.emit("add x0, x0, #15")
	g.emit("and x0, x0, #-16")
	// Pick cursor + end labels into x11 / x12 based on mode.
	g.adrpAdd("x6", "__lang_alloc_mode")
	g.emit("ldrb w7, [x6]")
	g.emit("cbnz w7, .Lalloc_pick_persistent")
	g.adrpAdd("x11", "__lang_heap_ptr")
	g.adrpAdd("x12", "__lang_heap_end")
	g.emit("mov x13, #1") // hint shift base = 0x1000_0000 (will be lsl-ed)
	g.emit("b .Lalloc_have_labels")
	g.label(".Lalloc_pick_persistent")
	g.adrpAdd("x11", "__lang_persistent_ptr")
	g.adrpAdd("x12", "__lang_persistent_end")
	g.emit("mov x13, #2") // hint shift base = 0x2000_0000
	g.label(".Lalloc_have_labels")
	g.emit("ldr x2, [x11]")
	g.emit("cbnz x2, .Lalloc_have_heap")
	// Lazy mmap. x13 carries the address-hint base (1 or 2).
	g.emit("mov x9, x0")
	g.emit("lsl x0, x13, #28") // x0 = hint << 28 = 0x1000_0000 or 0x2000_0000
	g.emit("ldr x1, =%d", heapBytes)
	g.emit("mov x2, #3")
	if g.darwin {
		g.emit("mov x3, #0x1002")
	} else {
		g.emit("mov x3, #0x22")
	}
	g.emit("mov x4, #-1")
	g.emit("mov x5, #0")
	// `syscall("mmap")` resolves to 197 on Darwin via `svc
	// #0x80` and 222 on Linux via `svc #0`; Darwin's BSD
	// carry-flag → -errno normalisation runs inside the helper
	// so the `cmn x0, #0; blt` check below sees Linux-shaped
	// values either way.
	g.syscall("mmap")
	g.emit("cmn x0, #0")
	g.emit("blt .Lalloc_oom")
	g.emit("mov x10, x0")
	g.emit("str x10, [x11]")
	g.emit("ldr x3, =%d", heapBytes)
	g.emit("add x3, x10, x3")
	g.emit("str x3, [x12]")
	g.emit("mov x0, x9")
	g.label(".Lalloc_have_heap")
	g.emit("ldr x2, [x11]")
	g.emit("add x3, x2, x0")
	g.emit("ldr x4, [x12]")
	g.emit("cmp x3, x4")
	g.emit("bhi .Lalloc_oom")
	g.emit("str x3, [x11]")
	g.emit("mov x0, x2")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.label(".Lalloc_oom")
	g.emit("mov x0, #137")
	g.syscallExit()
	g.sizeDirective("__lang_alloc")
	g.line(".ltorg")
}

// emitMemcpyRuntime emits `__lang_memcpy(dst, src, n)` —
// byte-grain copy. Word-grain bulk path runs in 8-byte chunks
// since arm64 has 64-bit registers; tail loop handles the
// residue. Pointers may be unaligned (arm64 allows unaligned
// access by default in user-mode Linux).
func (g *generator) emitMemcpyRuntime() {
	g.line("")
	g.line(".global __lang_memcpy")
	g.typeDirective("__lang_memcpy")
	g.label("__lang_memcpy")
	// r0 = dst (saved for return), r1 = src, r2 = n.
	g.emit("mov x3, x0") // x3 = dst saved
	g.label(".Lmcp_word")
	g.emit("cmp x2, #8")
	g.emit("blt .Lmcp_tail")
	g.emit("ldr x4, [x1], #8")
	g.emit("str x4, [x0], #8")
	g.emit("sub x2, x2, #8")
	g.emit("b .Lmcp_word")
	g.label(".Lmcp_tail")
	g.emit("cmp x2, #0")
	g.emit("beq .Lmcp_done")
	g.emit("ldrb w4, [x1], #1")
	g.emit("strb w4, [x0], #1")
	g.emit("sub x2, x2, #1")
	g.emit("b .Lmcp_tail")
	g.label(".Lmcp_done")
	g.emit("mov x0, x3")
	g.emit("ret")
	g.sizeDirective("__lang_memcpy")
	g.line(".ltorg")
}

// emitSliceMakeRuntime emits `__lang_slice_make(data, len)`:
// allocate an 8-byte slice header [data_ptr, len] on the bump
// heap and return its address. Header layout matches the wasm
// runtime so the IR's slice-field offsets stay backend-agnostic:
// 4 bytes data_ptr, 4 bytes len, 8 bytes total. Heap addresses
// fitting in 32 bits is fine for arm64 Linux (qemu); arm64-darwin's
// >4 GiB heap is a documented limitation per CLAUDE.md.
//
// Calling convention: x0 = data_ptr, x1 = len. Returns slice
// header address in x0. Stash inputs in callee-save x19 / x20
// across __lang_alloc.
func (g *generator) emitSliceMakeRuntime() {
	g.line("")
	g.line(".global __lang_slice_make")
	g.typeDirective("__lang_slice_make")
	g.label("__lang_slice_make")
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #-16]!")
	g.emit("mov w19, w0") // data_ptr (low 32 bits)
	g.emit("mov w20, w1") // len
	g.emit("mov x0, #8")
	g.emit("bl __lang_alloc")
	g.emit("str w19, [x0]")    // [+0..+3] data_ptr (i32)
	g.emit("str w20, [x0, #4]") // [+4..+7] len (i32)
	g.emit("ldp x19, x20, [sp], #16")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.sizeDirective("__lang_slice_make")
	g.line(".ltorg")
}

// emitStrcatRuntime emits `__lang_strcat(a, b)` — concat two
// length-prefixed strings into a fresh allocation. Both string
// operands are data pointers (post-prefix) with the 4-byte
// length at `[ptr - 4]`.
//
// Uses callee-save x19..x23 to keep state across the calls
// to __lang_alloc and __lang_memcpy. AAPCS64 says x19..x28
// must be preserved by the callee, so the saved-pair pattern
// at function entry / exit guarantees the values are restored
// before returning to the strcat caller.
func (g *generator) emitStrcatRuntime() {
	g.line("")
	g.line(".global __lang_strcat")
	g.typeDirective("__lang_strcat")
	g.label("__lang_strcat")
	if ast.UseTwoWordStrings(8) {
		g.emitStrcatRuntime2W()
		return
	}
	// Frame: 96 bytes — saved fp/lr (16) + 5 callee-saves (40
	// used + 8 pad) + 24 SSO scratch + 8 pad. Layout (positive
	// offsets from x29):
	//   [x29 + 0..7]:   saved fp
	//   [x29 + 8..15]:  saved lr
	//   [x29 + 16..23]: saved x19  (a — original string value)
	//   [x29 + 24..31]: saved x20  (b — original string value)
	//   [x29 + 32..39]: saved x21  (la)
	//   [x29 + 40..47]: saved x22  (lb)
	//   [x29 + 48..55]: saved x23  (output data ptr / inline value)
	//   [x29 + 56..63]: padding
	//   [x29 + 64..71]: emitStrDataPtr(a) scratch
	//   [x29 + 72..79]: emitStrDataPtr(b) scratch
	//   [x29 + 80..87]: inline output buffer (assembled byte-by-byte)
	//   [x29 + 88..95]: padding
	g.emit("stp x29, x30, [sp, #-96]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("str x23, [sp, #48]")
	g.emit("mov x19, x0") // x19 = a (may be inline-tagged)
	g.emit("mov x20, x1") // x20 = b (may be inline-tagged)
	// String lengths via the centralised helper.
	g.emitStrLen("w21", "x19")
	g.emitStrLen("w22", "x20")
	// Short-circuit on combined length == 0: return the shared
	// empty-string sentinel instead of allocating a fresh 0-byte
	// buffer. The sentinel round-trips through emitStrLen as 0,
	// so callers can't tell the difference.
	g.emit("orr w0, w21, w22")
	g.emit("cbnz w0, .Lstrcat_nonzero")
	g.emitStrEmpty("x0")
	g.emit("b .Lstrcat_ret")
	g.label(".Lstrcat_nonzero")
	// Combined length in w24 (scratch — caller-save, no save).
	g.emit("add w24, w21, w22")
	// If total <= 7, build inline output without allocating.
	g.emit("cmp w24, #7")
	g.emit("b.gt .Lstrcat_heap")
	// --- Inline output path ---
	// Zero the inline output buffer ([x29 + 80] = 8 bytes).
	g.emit("str xzr, [x29, #80]")
	// Length-and-tag byte at [x29 + 80]: (total << 1) | 1.
	g.emit("lsl w0, w24, #1")
	g.emit("orr w0, w0, #1")
	g.emit("strb w0, [x29, #80]")
	// Materialise a / b to byte pointers (heap inputs pass
	// through; inline inputs spill to the per-operand scratch
	// slot and the pointer addresses the first data byte).
	g.emitStrDataPtr("x19", "x19", 64)
	g.emitStrDataPtr("x20", "x20", 72)
	// memcpy([x29 + 81], a_data, la).
	g.emit("add x0, x29, #81")
	g.emit("mov x1, x19")
	g.emit("mov x2, x21")
	g.emit("bl __lang_memcpy")
	// memcpy([x29 + 81 + la], b_data, lb).
	g.emit("add x0, x29, #81")
	g.emit("add x0, x0, x21")
	g.emit("mov x1, x20")
	g.emit("mov x2, x22")
	g.emit("bl __lang_memcpy")
	// Load the full 8-byte inline value (length byte + 7 data
	// bytes + zero padding) into x0.
	g.emit("ldr x0, [x29, #80]")
	g.emit("b .Lstrcat_ret")
	g.label(".Lstrcat_heap")
	// --- Heap output path ---
	// alloc(la + lb + 4) for the new buffer (length prefix + data).
	g.emit("add x0, x21, x22")
	g.emit("add x0, x0, #4")
	g.emit("bl __lang_alloc")
	g.emit("add x23, x0, #4") // x23 = data ptr (past the 4-byte length prefix)
	g.emit("add w5, w21, w22") // w5 = combined length
	g.emitStrLenStore("w5", "x23")
	// Materialise a / b for the memcpy reads.
	g.emitStrDataPtr("x19", "x19", 64)
	g.emitStrDataPtr("x20", "x20", 72)
	// memcpy(data_ptr, a, la); memcpy(data_ptr + la, b, lb)
	g.emit("mov x0, x23")
	g.emit("mov x1, x19")
	g.emit("mov x2, x21")
	g.emit("bl __lang_memcpy")
	g.emit("add x0, x23, x21")
	g.emit("mov x1, x20")
	g.emit("mov x2, x22")
	g.emit("bl __lang_memcpy")
	g.emit("mov x0, x23") // return the data pointer
	g.label(".Lstrcat_ret")
	g.emit("ldr x23, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #96")
	g.emit("ret")
	g.sizeDirective("__lang_strcat")
	g.line(".ltorg")
}

// emitStrcatRuntime2W is the two-word-ABI variant of
// emitStrcatRuntime. Signature: `__lang_strcat(a_data, a_len,
// b_data, b_len)` in (x0, x1, x2, x3). Returns (data, len) in
// (x0, x1).
//
// Always uses heap-form output (no inline-form
// optimisation yet — that's a follow-up commit). Trade-off:
// short concats allocate; in exchange the body is simple +
// the inline-form encoding can be added incrementally.
//
// Empty-result short-circuit: when both byte lengths are 0,
// return the canonical empty-string pair (data=0, len=`1<<63`)
// without allocating.
func (g *generator) emitStrcatRuntime2W() {
	// Frame: fp/lr (16) + 4× callee-saves (x19..x22) for the
	// (data, len) pair of each operand across __lang_alloc /
	// __lang_memcpy (32) + 2× callee-saves (x23..x24) for
	// byte lengths + dst (16) + 2× 16-byte scratch slots for
	// emitStrDataPtr2W inline spill (32) + 16 align = 112.
	g.emit("stp x29, x30, [sp, #-112]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	// Spill the (data, len) pairs into callee-save regs so
	// they survive the bl calls below.
	g.emit("mov x19, x0") // a_data
	g.emit("mov x20, x1") // a_len
	g.emit("mov x21, x2") // b_data
	g.emit("mov x22, x3") // b_len
	// Extract byte lengths.
	g.emitStrLen2W("w23", "x20") // x23 = a byte length
	g.emitStrLen2W("w24", "x22") // x24 = b byte length
	// Total byte length in w0.
	g.emit("add w0, w23, w24")
	// Short-circuit on combined length 0.
	g.emit("cbnz w0, .Lstrcat2w_alloc")
	g.emit("mov x0, xzr")
	g.emit("movz x1, #0x8000, lsl #48") // inline-flag, length 0
	g.emit("b .Lstrcat2w_ret")
	g.label(".Lstrcat2w_alloc")
	// Allocate the destination buffer. The new heap-form
	// layout has no length prefix — alloc exactly total bytes.
	g.emit("bl __lang_alloc")
	g.emit("mov x2, x0") // x2 = dst (temporary; clobbered by next call's args)
	// Reserve dst in a stable callee-save by reusing x19 (we
	// no longer need a_data as a single register since we'll
	// re-extract via emitStrDataPtr2W). But we DO need a_data
	// for the inline-spill path. Plan B: stash dst at
	// [x29+96], use scratch slots at [x29+64..+79] (a) and
	// [x29+80..+95] (b).
	g.emit("str x2, [x29, #96]")
	// Materialise a's byte pointer.
	g.emitStrDataPtr2W("x4", "x19", "x20", 64) // x4 = a byte ptr; spill at [x29+64]
	// memcpy(dst, a_data, a_byteLen).
	g.emit("ldr x0, [x29, #96]") // x0 = dst
	g.emit("mov x1, x4")         // src = a byte ptr
	g.emit("mov x2, x23")        // n = a_byteLen
	g.emit("bl __lang_memcpy")
	// Materialise b's byte pointer.
	g.emitStrDataPtr2W("x4", "x21", "x22", 80) // x4 = b byte ptr; spill at [x29+80]
	// memcpy(dst + a_byteLen, b_data, b_byteLen).
	g.emit("ldr x0, [x29, #96]")
	g.emit("add x0, x0, x23")    // dst + a_byteLen
	g.emit("mov x1, x4")          // src = b byte ptr
	g.emit("mov x2, x24")         // n = b_byteLen
	g.emit("bl __lang_memcpy")
	// Return (dst, total_byteLen) in (x0, x1).
	g.emit("ldr x0, [x29, #96]")
	g.emit("add w1, w23, w24")
	g.label(".Lstrcat2w_ret")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #112")
	g.emit("ret")
	g.sizeDirective("__lang_strcat")
	g.line(".ltorg")
}

// emitInlineIdxHelper inlines a `__str_idx` / `__arr_idx` /
// `__slice_idx_*` bounds-check call as a plain address compute
// (`base + index * stride`). The IR walker follows the helper
// call with an OpLoad / OpLoadByte that reads from x0.
//
// arm64-specific: we can use `add x0, x1, x0, lsl #N` where N
// is the log2 stride. AArch64 supports an LSL shift amount in
// the operand-2 position — folds the multiply into the add.
func (g *generator) emitInlineIdxHelper(name string) error {
	if name == "__str_idx" && ast.UseTwoWordStrings(8) {
		// Two-word ABI: stack on entry is [data, len, idx],
		// top = idx. Pop idx → x0, len → x1, data → x2.
		// Top-bit-tagged inline check on len.
		g.usesStrIdx = true
		id := g.labelN
		g.labelN++
		inlineLbl := fmt.Sprintf(".Lstridx2w_inline_%d", id)
		doneLbl := fmt.Sprintf(".Lstridx2w_done_%d", id)
		g.emit("ldr x0, [sp], #%d", slotBytes) // idx
		g.emit("ldr x1, [sp], #%d", slotBytes) // len
		g.emit("ldr x2, [sp], #%d", slotBytes) // data
		g.emit("tbnz x1, #63, %s", inlineLbl)
		// Heap form: byte address = data + idx.
		g.emit("add x0, x2, x0")
		g.emit("b %s", doneLbl)
		g.label(inlineLbl)
		// Inline form: spill (data, len) at the 16-byte
		// .bss scratch slot. Bytes 0..7 from `data`, bytes
		// 8..14 from `len`'s low 56 bits. Result address =
		// scratch + idx.
		g.adrpAdd("x3", "__lang_str_idx_scratch")
		g.emit("str x2, [x3]")     // data bytes at scratch[0..7]
		g.emit("str x1, [x3, #8]") // len bytes at scratch[8..15]
		g.emit("add x0, x3, x0")
		g.label(doneLbl)
		g.push()
		return nil
	}
	g.emit("ldr x0, [sp], #%d", slotBytes) // idx
	g.emit("ldr x1, [sp], #%d", slotBytes) // base
	switch name {
	case "__str_idx":
		// SSO-aware byte indexing. Heap strings (LSB=0): byte
		// address = base + idx. Inline strings (LSB=1): spill
		// the value to the global .bss scratch slot and return
		// `&scratch[1 + idx]`. Single shared scratch slot, OK
		// because the immediate OpLoadByte that follows
		// consumes the address before the next __str_idx fires.
		g.usesStrIdx = true
		id := g.labelN
		g.labelN++
		inlineLbl := fmt.Sprintf(".Lstridx_inline_%d", id)
		doneLbl := fmt.Sprintf(".Lstridx_done_%d", id)
		g.emit("tbnz x1, #0, %s", inlineLbl)
		g.emit("add x0, x1, x0")
		g.emit("b %s", doneLbl)
		g.label(inlineLbl)
		g.adrpAdd("x2", "__lang_str_idx_scratch")
		g.emit("str x1, [x2]")
		g.emit("add x0, x2, x0")
		g.emit("add x0, x0, #1")
		g.label(doneLbl)
	case "__arr_idx_1":
		// Stride-1 byte-array indexing: byte address = base +
		// idx. Split from $__str_idx so the string helper can
		// own the SSO inline-spill dispatch without forcing
		// byte arrays through the same `tbnz` check.
		g.emit("add x0, x1, x0")
	case "__arr_idx_2":
		g.emit("add x0, x1, x0, lsl #1")
	case "__arr_idx":
		g.emit("add x0, x1, x0, lsl #2")
	case "__arr_idx_8":
		g.emit("add x0, x1, x0, lsl #3")
	case "__arr_idx_16":
		// 16-byte stride — two-word `string[]` element load.
		g.emit("add x0, x1, x0, lsl #4")
	// Slice indexing first dereferences the slice header to
	// recover its 32-bit data_ptr field, then does the same
	// stride-shifted add as the array helpers. The IR's
	// bounds-check pass has already validated `i < len`
	// upstream, so we skip the runtime length check inline.
	case "__slice_idx_1":
		g.emit("ldr w1, [x1]") // data_ptr (i32)
		g.emit("add x0, x1, x0")
	case "__slice_idx_2":
		g.emit("ldr w1, [x1]")
		g.emit("add x0, x1, x0, lsl #1")
	case "__slice_idx":
		g.emit("ldr w1, [x1]")
		g.emit("add x0, x1, x0, lsl #2")
	case "__slice_idx_8":
		g.emit("ldr w1, [x1]")
		g.emit("add x0, x1, x0, lsl #3")
	case "__slice_idx_16":
		g.emit("ldr w1, [x1]")
		g.emit("add x0, x1, x0, lsl #4")
	default:
		return fmt.Errorf("arm64: unknown index helper %q", name)
	}
	g.push()
	return nil
}

// emitStrcmpRuntime emits `__lang_strcmp(a, b)` — equality
// comparator returning 0 (equal) / 1 (different). Layout:
// length-prefix + word-grain bulk + byte-grain tail; pointer
// args are post-prefix.
func (g *generator) emitStrcmpRuntime() {
	g.line("")
	g.line(".global __lang_strcmp")
	g.typeDirective("__lang_strcmp")
	g.label("__lang_strcmp")
	if ast.UseTwoWordStrings(8) {
		g.emitStrcmpRuntime2W()
		return
	}
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	// 1. Same value? Equal — covers both same-heap-pointer and
	// same-inline-bits.
	g.emit("cmp x0, x1")
	g.emit("beq .Lscmp_eq")
	// 2. Same length?
	g.emitStrLen("w2", "x0")
	g.emitStrLen("w3", "x1")
	g.emit("cmp w2, w3")
	g.emit("bne .Lscmp_neq")
	// 3. Materialise both operands to byte pointers (heap inputs
	// pass through; inline inputs spill to their frame scratch
	// slot and the returned pointer addresses the first data byte).
	g.emitStrDataPtr("x0", "x0", 16)
	g.emitStrDataPtr("x1", "x1", 24)
	// 4a. Word-grain bulk — w2 holds remaining bytes.
	g.label(".Lscmp_word")
	g.emit("cmp w2, #4")
	g.emit("blt .Lscmp_tail")
	g.emit("ldr w4, [x0], #4")
	g.emit("ldr w5, [x1], #4")
	g.emit("cmp w4, w5")
	g.emit("bne .Lscmp_neq")
	g.emit("sub w2, w2, #4")
	g.emit("b .Lscmp_word")
	// 4b. Byte-grain tail.
	g.label(".Lscmp_tail")
	g.emit("cmp w2, #0")
	g.emit("beq .Lscmp_eq")
	g.emit("ldrb w4, [x0], #1")
	g.emit("ldrb w5, [x1], #1")
	g.emit("cmp w4, w5")
	g.emit("bne .Lscmp_neq")
	g.emit("sub w2, w2, #1")
	g.emit("b .Lscmp_tail")
	g.label(".Lscmp_eq")
	g.emit("mov x0, #0")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.label(".Lscmp_neq")
	g.emit("mov x0, #1")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__lang_strcmp")
	g.line(".ltorg")
}

// emitStrcmpRuntime2W is the two-word-ABI variant of
// emitStrcmpRuntime. Takes (a_data, a_len, b_data, b_len) in
// (x0, x1, x2, x3). Returns 0 (equal) / 1 (different) in x0.
//
//   - Same data ptr AND same len word → equal (covers both
//     same heap pointer and identical inline encodings).
//   - Different byte length (after flag-aware extraction) →
//     not equal.
//   - Same length: materialise both byte pointers via
//     emitStrDataPtr2W (heap → dataX; inline → spill to a
//     16-byte scratch slot), word-grain compare bulk, byte-
//     grain tail.
func (g *generator) emitStrcmpRuntime2W() {
	// Frame: fp/lr (16) + 2× 16-byte scratch slots for inline
	// spill (one per operand at [x29+16..+31] and [x29+32..+47])
	// + 16 alignment = 64.
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	// Same value pair?
	g.emit("cmp x0, x2")
	g.emit("bne .Lscmp2w_check_len")
	g.emit("cmp x1, x3")
	g.emit("beq .Lscmp2w_eq")
	g.label(".Lscmp2w_check_len")
	// Extract byte lengths.
	g.emit("mov x4, x1") // save a_len
	g.emit("mov x5, x3") // save b_len
	g.emitStrLen2W("w6", "x4") // w6 = a byte length
	g.emitStrLen2W("w7", "x5") // w7 = b byte length
	g.emit("cmp w6, w7")
	g.emit("bne .Lscmp2w_neq")
	// Same length → materialise both byte pointers.
	g.emitStrDataPtr2W("x0", "x0", "x1", 16) // x0 = a byte ptr; scratch at [x29+16]
	g.emitStrDataPtr2W("x1", "x2", "x3", 32) // x1 = b byte ptr; scratch at [x29+32]
	g.emit("mov w2, w6") // remaining bytes
	g.label(".Lscmp2w_word")
	g.emit("cmp w2, #4")
	g.emit("blt .Lscmp2w_tail")
	g.emit("ldr w4, [x0], #4")
	g.emit("ldr w5, [x1], #4")
	g.emit("cmp w4, w5")
	g.emit("bne .Lscmp2w_neq")
	g.emit("sub w2, w2, #4")
	g.emit("b .Lscmp2w_word")
	g.label(".Lscmp2w_tail")
	g.emit("cmp w2, #0")
	g.emit("beq .Lscmp2w_eq")
	g.emit("ldrb w4, [x0], #1")
	g.emit("ldrb w5, [x1], #1")
	g.emit("cmp w4, w5")
	g.emit("bne .Lscmp2w_neq")
	g.emit("sub w2, w2, #1")
	g.emit("b .Lscmp2w_tail")
	g.label(".Lscmp2w_eq")
	g.emit("mov x0, #0")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.label(".Lscmp2w_neq")
	g.emit("mov x0, #1")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("__lang_strcmp")
	g.line(".ltorg")
}

// emitRawIntPokesRuntime emits `__store_i32(addr, val)` and
// `__load_i32(addr) -> i32`. The lang Map runtime calls these
// for its mixed bucket-index + entries buffer where the
// caller owns the layout (no length prefix). Single STR / LDR
// + ret each — leaf functions.
//
// Also emits `__store_ptr(addr, val)` / `__load_ptr(addr)`,
// the pointer-width counterparts. On 64-bit arm64 these
// store/load 8 bytes so that heap addresses round-trip
// without truncation. The Map runtime uses these for its
// data-ptr field (`m → buf` handle indirection); the rest
// of its mixed buffer stays i32 for compact length / hash /
// bucket-index storage. Same flag gates both pairs so a
// Map-using program pulls them in together.
func (g *generator) emitRawIntPokesRuntime() {
	g.line("")
	g.line(".global __load_i32")
	g.typeDirective("__load_i32")
	g.label("__load_i32")
	g.emit("ldr w0, [x0]")
	g.emit("ret")
	g.sizeDirective("__load_i32")

	g.line("")
	g.line(".global __store_i32")
	g.typeDirective("__store_i32")
	g.label("__store_i32")
	g.emit("str w1, [x0]")
	g.emit("ret")
	g.sizeDirective("__store_i32")

	g.line("")
	g.line(".global __load_ptr")
	g.typeDirective("__load_ptr")
	g.label("__load_ptr")
	g.emit("ldr x0, [x0]") // 8-byte load
	g.emit("ret")
	g.sizeDirective("__load_ptr")

	g.line("")
	g.line(".global __store_ptr")
	g.typeDirective("__store_ptr")
	g.label("__store_ptr")
	g.emit("str x1, [x0]") // 8-byte store
	g.emit("ret")
	g.sizeDirective("__store_ptr")

	// `__ptr_width()` returns 8 on arm64. The Map runtime uses
	// this to size per-entry key/value slots; pairs with the
	// wasm backend's `i32.const 4` constant function.
	g.line("")
	g.line(".global __ptr_width")
	g.typeDirective("__ptr_width")
	g.label("__ptr_width")
	g.emit("mov w0, #8")
	g.emit("ret")
	g.sizeDirective("__ptr_width")
	g.line(".ltorg")
}

// emitMemsetRuntime emits `__memset(dst, byte, n)` — byte-
// grain fill matching the wasm bulk-memory shim. Word-grain
// bulk path replicates the byte across all eight lanes;
// byte-grain tail handles the residue. Pairs with the Map
// runtime's clear path.
func (g *generator) emitMemsetRuntime() {
	g.line("")
	g.line(".global __memset")
	g.typeDirective("__memset")
	g.label("__memset")
	// x0 = dst, w1 = byte (low 8 bits), x2 = n.
	g.emit("and w1, w1, #0xff")
	// Replicate the byte across 8 bytes (64 bits).
	g.emit("orr w3, w1, w1, lsl #8")
	g.emit("orr w3, w3, w3, lsl #16")
	g.emit("orr x3, x3, x3, lsl #32")
	g.label(".Lmset_word")
	g.emit("cmp x2, #8")
	g.emit("blt .Lmset_tail")
	g.emit("str x3, [x0], #8")
	g.emit("sub x2, x2, #8")
	g.emit("b .Lmset_word")
	g.label(".Lmset_tail")
	g.emit("cmp x2, #0")
	g.emit("beq .Lmset_done")
	g.emit("strb w1, [x0], #1")
	g.emit("sub x2, x2, #1")
	g.emit("b .Lmset_tail")
	g.label(".Lmset_done")
	g.emit("ret")
	g.sizeDirective("__memset")
	g.line(".ltorg")
}

// emitAllocU8Runtime emits `__alloc_u8(n)` — allocates a
// fresh length-prefixed `u8[]` of n bytes. Returns the data
// pointer (header + 4); length at `[data - 4]`.
func (g *generator) emitAllocU8Runtime() {
	g.line("")
	g.line(".global __alloc_u8")
	g.typeDirective("__alloc_u8")
	g.label("__alloc_u8")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("mov x19, x0")     // x19 = n (callee-save, survives bl)
	// Short-circuit on n == 0: return the shared static empty-
	// array sentinel rather than allocating a fresh 4-byte
	// length-only buffer. The sentinel's byte at offset -4 is
	// 0 (length), so emitArrayLen reads the right value via
	// the same `ldur w?, [ptr, #-4]` it does for heap buffers.
	g.emit("cbnz w19, .Lallocu8_alloc")
	g.usesArrEmpty = true
	g.adrpAdd("x0", ".LArr_Empty")
	g.emit("b .Lallocu8_ret")
	g.label(".Lallocu8_alloc")
	g.emit("add x0, x19, #4")
	g.emit("bl __lang_alloc")
	g.emit("add x0, x0, #4")  // x0 = data ptr
	g.emitArrayLenStore("w19", "x0")
	g.label(".Lallocu8_ret")
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__alloc_u8")
	g.line(".ltorg")
}

// emitStringFromBytesRuntime emits `string_from_bytes(bs)` —
// copy a `u8[]` payload into a fresh length-prefixed string.
// Round-trip companion to `s.bytes()`.
func (g *generator) emitStringFromBytesRuntime() {
	g.line("")
	g.line(".global string_from_bytes")
	g.typeDirective("string_from_bytes")
	g.label("string_from_bytes")
	if ast.UseTwoWordStrings(8) {
		// Two-word ABI: take `bs` (u8[] data pointer) in x0,
		// return `(data, len)` in (x0, x1).
		// Frame: fp/lr (16) + 2 callee-saves (16) + 16 align.
		g.emit("stp x29, x30, [sp, #-48]!")
		g.emit("mov x29, sp")
		g.emit("stp x19, x20, [sp, #16]")
		g.emit("mov x19, x0")          // x19 = bs (input u8[])
		g.emitArrayLen("w20", "x19")   // x20 = byte length
		// Empty input → empty pair.
		g.emit("cbnz w20, .Lsfb2w_alloc")
		g.emit("mov x0, xzr")
		g.emit("movz x1, #0x8000, lsl #48")
		g.emit("b .Lsfb2w_ret")
		g.label(".Lsfb2w_alloc")
		g.emit("mov w0, w20")
		g.emit("bl __lang_alloc")      // x0 = dst
		g.emit("mov x2, x20")           // n
		g.emit("mov x1, x19")           // src = bs
		g.emit("stp x0, xzr, [sp, #-16]!") // save dst on stack
		g.emit("bl __lang_memcpy")
		g.emit("ldp x0, x1, [sp], #16")  // x0 = dst (saved), x1 = junk
		g.emit("mov w1, w20")            // len = byteLen
		g.label(".Lsfb2w_ret")
		g.emit("ldp x19, x20, [sp, #16]")
		g.emit("ldp x29, x30, [sp], #48")
		g.emit("ret")
		g.sizeDirective("string_from_bytes")
		g.line(".ltorg")
		return
	}
	// Frame: 64 bytes — fp/lr (16) + x19/x20/x21 (24 + 8 pad) +
	// 16 SSO inline-output buffer (only 8 bytes used, 8 padding).
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("str x21, [sp, #32]")
	g.emit("mov x19, x0")           // x19 = bs (input u8[] array)
	g.emitArrayLen("w20", "x19")    // x20 = input array length
	// Short-circuit on input length == 0: return the shared
	// empty-string sentinel rather than allocating a fresh
	// 0-byte buffer.
	g.emit("cbnz w20, .Lsfb_nonempty")
	g.emitStrEmpty("x0")
	g.emit("b .Lsfb_ret")
	g.label(".Lsfb_nonempty")
	// length <= 7? Pack into inline-tagged register value, no alloc.
	g.emit("cmp w20, #7")
	g.emit("b.gt .Lsfb_heap")
	// --- Inline output path ---
	g.emit("str xzr, [x29, #48]")
	g.emit("lsl w0, w20, #1")
	g.emit("orr w0, w0, #1")
	g.emit("strb w0, [x29, #48]")
	g.emit("add x0, x29, #49")
	g.emit("mov x1, x19")
	g.emit("mov x2, x20")
	g.emit("bl __lang_memcpy")
	g.emit("ldr x0, [x29, #48]")
	g.emit("b .Lsfb_ret")
	g.label(".Lsfb_heap")
	g.emit("add x0, x20, #4")
	g.emit("bl __lang_alloc")
	g.emit("add x21, x0, #4")       // x21 = data ptr (callee-save)
	g.emitStrLenStore("w20", "x21")
	g.emit("mov x0, x21")           // memcpy dst
	g.emit("mov x1, x19")
	g.emit("mov x2, x20")
	g.emit("bl __lang_memcpy")
	g.emit("mov x0, x21")           // return data ptr
	g.label(".Lsfb_ret")
	g.emit("ldr x21, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("string_from_bytes")
	g.line(".ltorg")
}

// emitStrSliceRuntime emits `__str_slice(base, low, high)` —
// allocates a fresh length-prefixed string holding
// `base[low..high]`. Bounds-traps on `low < 0`, `high >
// src_len`, or `low > high`. Used by every `s[a:b]` slice
// expression on a string.
func (g *generator) emitStrSliceRuntime() {
	g.line("")
	g.line(".global __str_slice")
	g.typeDirective("__str_slice")
	g.label("__str_slice")
	if ast.UseTwoWordStrings(8) {
		g.emitStrSliceRuntime2W()
		return
	}
	// Args: x0 = base, x1 = low, x2 = high.
	// Frame: 80 bytes — fp/lr (16) + x19..x23 (40 used + 8 pad)
	// + 16 SSO scratch (8 for emitStrDataPtr(base) + 8 inline
	// output buffer). x23 holds the alloc data ptr / inline
	// value across __lang_memcpy.
	g.emit("stp x29, x30, [sp, #-80]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("str x23, [sp, #48]")
	g.emit("mov x19, x0") // x19 = base (may be inline-tagged)
	g.emit("mov x20, x1") // x20 = low
	g.emit("mov x21, x2") // x21 = high
	g.emitStrLen("w22", "x19") // x22 = src_len
	// low < 0 → trap
	g.emit("cmp x20, #0")
	g.emit("blt .Lstrslice_trap")
	// high > src_len → trap (unsigned)
	g.emit("cmp x21, x22")
	g.emit("bhi .Lstrslice_trap")
	// low > high → trap
	g.emit("cmp x20, x21")
	g.emit("bgt .Lstrslice_trap")
	// Short-circuit on new_len == 0 (low == high): return the
	// shared empty-string sentinel without allocating.
	g.emit("cmp x20, x21")
	g.emit("bne .Lstrslice_nonempty")
	g.emitStrEmpty("x0")
	g.emit("b .Lstrslice_ret")
	g.label(".Lstrslice_nonempty")
	// Materialise base → byte ptr (heap inputs pass through;
	// inline inputs spill to [x29 + 64] and x19 points to the
	// first data byte).
	g.emitStrDataPtr("x19", "x19", 64)
	g.emit("sub w22, w21, w20") // w22 = new_len (reuse w22; src_len no longer needed)
	// new_len <= 7? build inline output without allocating.
	g.emit("cmp w22, #7")
	g.emit("b.gt .Lstrslice_heap")
	// --- Inline output path ---
	g.emit("str xzr, [x29, #72]")
	g.emit("lsl w0, w22, #1")
	g.emit("orr w0, w0, #1")
	g.emit("strb w0, [x29, #72]")
	// memcpy([x29 + 73], base + low, new_len).
	g.emit("add x0, x29, #73")
	g.emit("add x1, x19, x20")
	g.emit("mov x2, x22")
	g.emit("bl __lang_memcpy")
	g.emit("ldr x0, [x29, #72]")
	g.emit("b .Lstrslice_ret")
	g.label(".Lstrslice_heap")
	// --- Heap output path ---
	g.emit("add x0, x22, #4")
	g.emit("bl __lang_alloc")
	g.emit("add x23, x0, #4")     // x23 = data ptr (callee-save survives bl)
	g.emitStrLenStore("w22", "x23")
	// memcpy(data_ptr, base + low, new_len).
	g.emit("add x1, x19, x20")    // src = base + low
	g.emit("mov x2, x22")         // n
	g.emit("mov x0, x23")         // dst
	g.emit("bl __lang_memcpy")
	g.emit("mov x0, x23")         // return data ptr
	g.label(".Lstrslice_ret")
	g.emit("ldr x23, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #80")
	g.emit("ret")
	g.label(".Lstrslice_trap")
	g.emit("mov x0, #134")
	g.syscallExit()
	g.sizeDirective("__str_slice")
	g.line(".ltorg")
}

// emitStrSliceRuntime2W is the two-word-ABI variant of
// emitStrSliceRuntime. Signature: `__str_slice(base_data,
// base_len, low, high)` in (x0, x1, x2, x3). Returns
// `(data, len)` in (x0, x1).
//
// Bounds-checks (low ≥ 0; high ≤ base_byteLen; low ≤ high)
// trap with exit 134.
//
// Always uses heap-form output. Inline-form optimisation is
// a follow-up.
func (g *generator) emitStrSliceRuntime2W() {
	// Frame: fp/lr (16) + 4 callee-saves (x19..x22, 32) for
	// base_data / base_len / low / high across the bl calls,
	// + 2 callee-saves (x23, x24) for dst / new_len (16), +
	// 16-byte scratch for emitStrDataPtr2W (16), + 16 align = 96.
	g.emit("stp x29, x30, [sp, #-96]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("mov x19, x0") // base_data
	g.emit("mov x20, x1") // base_len
	g.emit("mov x21, x2") // low
	g.emit("mov x22, x3") // high
	// Get base byte length.
	g.emitStrLen2W("w23", "x20") // x23 = src_byteLen
	// Bounds checks: low ≥ 0, high ≤ src_byteLen, low ≤ high.
	g.emit("cmp x21, #0")
	g.emit("blt .Lstrslice2w_trap")
	g.emit("cmp x22, x23")
	g.emit("bhi .Lstrslice2w_trap")
	g.emit("cmp x21, x22")
	g.emit("bgt .Lstrslice2w_trap")
	// new_len = high - low.
	g.emit("sub w24, w22, w21")
	// Short-circuit on new_len == 0: return empty pair.
	g.emit("cbnz w24, .Lstrslice2w_nonempty")
	g.emit("mov x0, xzr")
	g.emit("movz x1, #0x8000, lsl #48")
	g.emit("b .Lstrslice2w_ret")
	g.label(".Lstrslice2w_nonempty")
	// Materialise base byte pointer.
	g.emitStrDataPtr2W("x19", "x19", "x20", 64) // x19 = base byte ptr; spill at [x29+64]
	// Allocate new_len bytes for the heap output.
	g.emit("mov w0, w24")
	g.emit("bl __lang_alloc")
	g.emit("mov x23, x0") // x23 = dst
	// memcpy(dst, base_ptr + low, new_len).
	g.emit("add x1, x19, x21")
	g.emit("mov x2, x24")
	g.emit("mov x0, x23")
	g.emit("bl __lang_memcpy")
	// Return (dst, new_len) in (x0, x1).
	g.emit("mov x0, x23")
	g.emit("mov w1, w24")
	g.label(".Lstrslice2w_ret")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #96")
	g.emit("ret")
	g.label(".Lstrslice2w_trap")
	g.emit("mov x0, #134")
	g.syscallExit()
	g.sizeDirective("__str_slice")
	g.line(".ltorg")
}

// emitEnvRuntime emits `__lang_env(name)` — walks the envp
// vector for `NAME=VALUE` and returns the value as a fresh
// lang Option[string]. None is the heap-allocated Option
// variant (tag=1); Some carries the value pointer in payload+0.
//
// First-PR scope returns a raw string pointer (Some(value) as
// just the value's data ptr) — matches the simpler shape
// the synthesised `__port_from_env` expects. The full
// Option enum encoding (tag + payload) on arm64 will fall
// out from existing enum lowering work; for now we encode
// Option[string] manually: a 8-byte heap object [tag, ptr],
// where tag=0 = Some, tag=1 = None.
func (g *generator) emitEnvRuntime() {
	g.line("")
	g.line(".global __lang_env")
	g.typeDirective("__lang_env")
	g.label("__lang_env")
	twoWord := ast.UseTwoWordStrings(8)
	if twoWord {
		// Two-word ABI: (name_data, name_len) in (x0, x1).
		// Frame grows to 80 bytes: 16-byte inline-spill
		// scratch for name materialisation at [x29+64..+79].
		g.emit("stp x29, x30, [sp, #-80]!")
		g.emit("mov x29, sp")
		g.emit("stp x19, x20, [sp, #16]")
		g.emit("stp x21, x22, [sp, #32]")
		g.emitStrLen2W("w20", "x1") // x20 = name byte length
		g.emitStrDataPtr2W("x19", "x0", "x1", 64) // x19 = name byte ptr
	} else {
	// Frame: 64 bytes — fp/lr (16) + x19..x22 (32) + 8 SSO scratch
	// at [x29 + 48] for materialising the name + 8 padding.
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emitStrLen("w20", "x0")           // x20 = name_len (read before materialise)
	g.emitStrDataPtr("x19", "x0", 48)   // x19 = name byte ptr
	}
	g.adrpAdd("x21", "__lang_envp")
	g.emit("ldr x21, [x21]")            // x21 = envp
	g.label(".Lenv_loop")
	g.emit("ldr x22, [x21]")        // x22 = envp[i]
	g.emit("cbz x22, .Lenv_none")   // NULL terminator → return None
	// Compare first name_len bytes of envp[i] with name, then check '='.
	g.emit("mov x0, x22")           // candidate envp entry
	g.emit("mov x1, x19")           // name
	g.emit("mov x2, x20")           // n
	g.emit("bl __memcmp_n_env")
	g.emit("cbnz w0, .Lenv_next")   // not equal
	// Check that byte at offset name_len is '='.
	g.emit("ldrb w0, [x22, x20]")
	g.emit("cmp w0, #61")           // '='
	g.emit("bne .Lenv_next")
	// Found. Build a fresh lang string holding the value after '='.
	g.emit("add x0, x22, x20")
	g.emit("add x0, x0, #1")        // x0 = start of value (NUL-terminated)
	g.emit("mov x1, x0")
	g.label(".Lenv_strlen")
	g.emit("ldrb w2, [x1]")
	g.emit("cbz w2, .Lenv_strlen_done")
	g.emit("add x1, x1, #1")
	g.emit("b .Lenv_strlen")
	g.label(".Lenv_strlen_done")
	g.emit("sub x2, x1, x0")        // x2 = value length
	g.emit("mov x19, x0")           // stash value src ptr
	g.emit("mov x20, x2")           // stash value len
	if twoWord {
		// Heap-form: alloc exactly value-len bytes (no
		// length prefix).
		g.emit("mov x0, x2")
		g.emit("bl __lang_alloc")
		g.emit("mov x22, x0") // x22 = data ptr
		g.emit("mov x0, x22")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __lang_memcpy")
		// Build Option[string]: 24-byte box {tag@0, data@8,
		// len@16}.
		g.emit("mov x0, #24")
		g.emit("bl __lang_alloc")
		g.emit("str wzr, [x0]")     // tag = 0 (Some)
		g.emit("str x22, [x0, #8]")  // data
		g.emit("str x20, [x0, #16]") // len
		g.emit("b .Lenv_done")
	} else {
		g.emit("add x0, x2, #4")
		g.emit("bl __lang_alloc")
		g.emit("add x22, x0, #4")
		g.emitStrLenStore("w20", "x22")
		g.emit("mov x0, x22")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __lang_memcpy")
		g.emit("mov x0, #16")
		g.emit("bl __lang_alloc")
		g.emit("str wzr, [x0]")
		g.emit("str x22, [x0, #8]")
		g.emit("b .Lenv_done")
	}
	g.label(".Lenv_next")
	g.emit("add x21, x21, #8")
	g.emit("b .Lenv_loop")
	g.label(".Lenv_none")
	// None: heap [tag=1].
	g.emit("mov x0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]")
	g.label(".Lenv_done")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	if twoWord {
		g.emit("ldp x29, x30, [sp], #80")
	} else {
		g.emit("ldp x29, x30, [sp], #64")
	}
	g.emit("ret")
	g.sizeDirective("__lang_env")
	g.line(".ltorg")

	// __memcmp_n_env(a, b, n) — returns 0 if first n bytes
	// of `a` equal first n bytes of `b`, non-zero otherwise.
	// `b` is a length-prefixed lang string; `a` is a raw
	// NUL-terminated C string from envp.
	g.line("")
	g.typeDirective("__memcmp_n_env")
	g.label("__memcmp_n_env")
	g.label(".Lmcn_loop")
	g.emit("cbz x2, .Lmcn_eq")
	g.emit("ldrb w3, [x0], #1")
	g.emit("ldrb w4, [x1], #1")
	g.emit("cmp w3, w4")
	g.emit("bne .Lmcn_neq")
	g.emit("sub x2, x2, #1")
	g.emit("b .Lmcn_loop")
	g.label(".Lmcn_eq")
	g.emit("mov x0, #0")
	g.emit("ret")
	g.label(".Lmcn_neq")
	g.emit("mov x0, #1")
	g.emit("ret")
	g.sizeDirective("__memcmp_n_env")
	g.line(".ltorg")
}

// emitTcpListenRuntime emits `__lang_tcp_listen(port)` —
// opens a TCP listening socket on 0.0.0.0:port. Returns the
// listener fd on success, or `-errno` on failure. C-style
// API; callers check `if (fd < 0)`.
//
// Steps: socket(AF_INET, SOCK_STREAM, 0); bind to a stack-
// allocated sockaddr_in; listen with backlog=128.
func (g *generator) emitTcpListenRuntime() {
	g.line("")
	g.line(".global __lang_tcp_listen")
	g.typeDirective("__lang_tcp_listen")
	g.label("__lang_tcp_listen")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("mov x19, x0") // x19 = port (callee-save across calls)
	// socket(AF_INET=2, SOCK_STREAM=1, 0)
	g.emit("mov x0, #2")
	g.emit("mov x1, #1")
	g.emit("mov x2, #0")
	g.syscall("socket")
	g.emit("cmp x0, #0")
	g.emit("blt .Ltcp_lst_err")
	g.emit("mov x20, x0") // x20 = listener fd
	// Build sockaddr_in on the stack (16 bytes).
	g.emit("sub sp, sp, #16")
	g.emit("mov w0, #2")
	g.emit("strh w0, [sp]")      // sin_family
	g.emit("rev16 w0, w19")      // htons(port)
	g.emit("strh w0, [sp, #2]")
	g.emit("str wzr, [sp, #4]")  // sin_addr = 0
	g.emit("str xzr, [sp, #8]")  // sin_zero[0..7]
	// bind(fd, sa, 16)
	g.emit("mov x0, x20")
	g.emit("mov x1, sp")
	g.emit("mov x2, #16")
	g.syscall("bind")
	g.emit("add sp, sp, #16")    // pop sockaddr_in
	g.emit("cmp x0, #0")
	g.emit("blt .Ltcp_lst_err")
	// listen(fd, 128)
	g.emit("mov x0, x20")
	g.emit("mov x1, #128")
	g.syscall("listen")
	g.emit("cmp x0, #0")
	g.emit("blt .Ltcp_lst_err")
	g.emit("mov x0, x20")        // return fd
	g.emit("b .Ltcp_lst_done")
	g.label(".Ltcp_lst_err")
	// x0 holds -errno from the failed syscall.
	g.label(".Ltcp_lst_done")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__lang_tcp_listen")
	g.line(".ltorg")
}

// emitTcpAcceptRuntime emits `__lang_tcp_accept(fd)` —
// accepts a connection on the listener fd, returns the new
// connection fd or `-errno`. Passes NULL addr/addrlen
// out-params; callers don't need the peer address.
func (g *generator) emitTcpAcceptRuntime() {
	g.line("")
	g.line(".global __lang_tcp_accept")
	g.typeDirective("__lang_tcp_accept")
	g.label("__lang_tcp_accept")
	// x0 = listener fd (already in x0 from caller).
	g.emit("mov x1, #0") // addr = NULL
	g.emit("mov x2, #0") // addrlen = NULL
	g.syscall("accept")
	g.emit("ret")
	g.sizeDirective("__lang_tcp_accept")
	g.line(".ltorg")
}

// emitTcpRecvRuntime emits `__lang_tcp_recv(fd, max)` —
// reads up to `max` bytes from the socket fd, returns a
// fresh length-prefixed lang string with the bytes read.
// On error or EOF the returned string has length 0.
//
// Frame: 48 bytes — fp/lr (16) + callee-save x19/x20 (16) +
// callee-save x21 (8) + 8 bytes pad for 16-byte sp alignment.
// x21 holds the data pointer across the `read` syscall; it's
// AAPCS64-callee-save so the syscall preserves it for us, but
// we still save the inbound value in the prologue so the
// caller's x21 round-trips intact.
func (g *generator) emitTcpRecvRuntime() {
	g.line("")
	g.line(".global __lang_tcp_recv")
	g.typeDirective("__lang_tcp_recv")
	g.label("__lang_tcp_recv")
	twoWord := ast.UseTwoWordStrings(8)
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("str x21, [sp, #32]")
	g.emit("mov x19, x0") // x19 = fd
	g.emit("mov x20, x1") // x20 = max
	if twoWord {
		// Two-word heap form: alloc max bytes (no prefix /
		// NUL); return (data, len) in (x0, x1).
		g.emit("mov x0, x20")
		g.emit("bl __lang_alloc")
		g.emit("mov x21, x0") // x21 = dst
		g.emit("mov x0, x19")
		g.emit("mov x1, x21")
		g.emit("mov x2, x20")
		g.syscall("read")
		g.emit("cmp x0, #0")
		g.emit("csel x0, x0, xzr, ge")
		// x0 = byte count, x21 = data ptr → return (data, len).
		g.emit("mov x1, x0")  // x1 = len
		g.emit("mov x0, x21") // x0 = data
	} else {
		// Allocate `max + 5` bytes (4 prefix + max data + 1 NUL).
		g.emit("add x0, x20, #5")
		g.emit("bl __lang_alloc")
		g.emit("add x21, x0, #4")
		g.emit("mov x0, x19")
		g.emit("mov x1, x21")
		g.emit("mov x2, x20")
		g.syscall("read")
		g.emit("cmp x0, #0")
		g.emit("csel x0, x0, xzr, ge")
		g.emit("stur w0, [x21, #-4]")
		g.emit("add x1, x21, x0")
		g.emit("strb wzr, [x1]")
		g.emit("mov x0, x21")
	}
	g.emit("ldr x21, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__lang_tcp_recv")
	g.line(".ltorg")
}

// emitTcpSendRuntime emits `__lang_tcp_send(fd, data)` —
// writes the entire string to the fd via `write(2)`. Returns
// the syscall result (bytes written or `-errno`).
func (g *generator) emitTcpSendRuntime() {
	g.line("")
	g.line(".global __lang_tcp_send")
	g.typeDirective("__lang_tcp_send")
	g.label("__lang_tcp_send")
	if ast.UseTwoWordStrings(8) {
		// x0 = fd, x1 = data, x2 = len.
		g.emit("stp x29, x30, [sp, #-48]!")
		g.emit("mov x29, sp")
		g.emit("mov w3, w0") // w3 = fd
		g.emitStrLen2W("w4", "x2")             // w4 = byte length
		g.emitStrDataPtr2W("x1", "x1", "x2", 16) // x1 = byte ptr
		g.emit("mov w0, w3")                    // x0 = fd
		g.emit("mov x2, x4")                    // x2 = byte length
		g.syscall("write")
		g.emit("ldp x29, x30, [sp], #48")
		g.emit("ret")
		g.sizeDirective("__lang_tcp_send")
		g.line(".ltorg")
		return
	}
	// Legacy single-pointer.
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emitStrLen("w2", "x1")
	g.emitStrDataPtr("x1", "x1", 16)
	g.syscall("write")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__lang_tcp_send")
	g.line(".ltorg")
}

// emitTcpCloseRuntime emits `__lang_tcp_close(fd)` — thin
// wrapper around `close(2)`. Returns 0 or `-errno`.
func (g *generator) emitTcpCloseRuntime() {
	g.line("")
	g.line(".global __lang_tcp_close")
	g.typeDirective("__lang_tcp_close")
	g.label("__lang_tcp_close")
	g.syscall("close")
	g.emit("ret")
	g.sizeDirective("__lang_tcp_close")
	g.line(".ltorg")
}

// emitWriteRuntime emits `__lang_write(s_data, s_len)` —
// single write(1, buf, byteLen) syscall, no trailing newline.
// Under the two-word ABI the string arrives as a (data, len)
// pair in (x0, x1). Byte length is extracted from x1 via
// emitStrLen2W; the byte pointer materialises via
// emitStrDataPtr2W (handles inline spill at [x29-16..x29-1]).
func (g *generator) emitWriteRuntime() {
	g.line("")
	g.line(".global __lang_write")
	g.typeDirective("__lang_write")
	g.label("__lang_write")
	if ast.UseTwoWordStrings(8) {
		// Frame: 48 bytes — fp/lr (16) + 16-byte scratch for
		// inline-spill at [x29+16..x29+31] + 16 align pad.
		g.emit("stp x29, x30, [sp, #-48]!")
		g.emit("mov x29, sp")
		g.emitStrLen2W("w2", "x1")                // x2 = byte length
		g.emitStrDataPtr2W("x1", "x0", "x1", 16)  // x1 = byte ptr; spill scratch at [x29+16]
		g.emit("mov x0, #1")                      // fd = stdout
		g.syscall("write")
		g.emit("ldp x29, x30, [sp], #48")
		g.emit("ret")
		g.sizeDirective("__lang_write")
		g.line(".ltorg")
		return
	}
	// Legacy single-register native ABI.
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emitStrLen("w2", "x0")           // x2 = length
	g.emitStrDataPtr("x1", "x0", 16)   // x1 = byte ptr (buf)
	g.emit("mov x0, #1")               // x0 = fd (stdout)
	g.syscall("write")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__lang_write")
	g.line(".ltorg")
}

// emitPutsRuntime emits `__lang_puts(s)` — write the string,
// then a single trailing newline. Two write(2) calls keeps the
// code simple at the cost of one extra kernel transition; per-
// call cost is dominated by the syscall itself either way.
// Preserves x19 across the second write so we can return the
// original data pointer for libc-puts consistency.
func (g *generator) emitPutsRuntime() {
	g.line("")
	g.line(".global __lang_puts")
	g.typeDirective("__lang_puts")
	g.label("__lang_puts")
	if ast.UseTwoWordStrings(8) {
		// Two-word ABI: (data, len) in (x0, x1). Frame:
		// fp/lr (16) + 16-byte inline-spill scratch at
		// [x29+16..x29+31] + 16 alignment.
		g.emit("stp x29, x30, [sp, #-48]!")
		g.emit("mov x29, sp")
		g.emitStrLen2W("w2", "x1")               // x2 = byte length
		g.emitStrDataPtr2W("x1", "x0", "x1", 16) // x1 = byte ptr; spill at [x29+16]
		g.emit("mov x0, #1")                     // fd
		g.syscall("write")
		g.adrpAdd("x1", ".LLangNewline")
		g.emit("mov x2, #1")
		g.emit("mov x0, #1")
		g.syscall("write")
		// Return the empty (data, len) pair — print's return
		// value is unused by lang user code, so we just hand
		// back a zero pair to keep the AAPCS64 return-shape
		// honest. The caller's IR-side push (commit applies
		// returnIsString fan-out for two-word).
		g.emit("mov x0, xzr")
		g.emit("mov x1, xzr")
		g.emit("ldp x29, x30, [sp], #48")
		g.emit("ret")
		g.sizeDirective("__lang_puts")
		g.line(".ltorg")
		return
	}
	// Legacy single-register native ABI.
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("mov x19, x0")
	g.emitStrLen("w2", "x0")
	g.emitStrDataPtr("x1", "x0", 24)
	g.emit("mov x0, #1")
	g.syscall("write")
	g.adrpAdd("x1", ".LLangNewline")
	g.emit("mov x2, #1")
	g.emit("mov x0, #1")
	g.syscall("write")
	g.emit("mov x0, x19")
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__lang_puts")
	g.line(".ltorg")
}

// emitPutcharRuntime emits `__lang_putchar(c)` — write the
// low byte of x0 to fd 1. We materialise the byte on the
// caller's stack frame so the kernel has a real address to
// read from (the byte itself is a register value).
func (g *generator) emitPutcharRuntime() {
	g.line("")
	g.line(".global __lang_putchar")
	g.typeDirective("__lang_putchar")
	g.label("__lang_putchar")
	g.emit("sub sp, sp, #16")     // 16-byte slot for sp alignment
	g.emit("strb w0, [sp]")        // store byte on the stack
	g.emit("mov x1, sp")           // buf
	g.emit("mov x2, #1")           // len
	g.emit("mov x0, #1")           // fd
	g.syscall("write")
	g.emit("add sp, sp, #16")
	g.emit("ret")
	g.sizeDirective("__lang_putchar")
	g.line(".ltorg")
}

// emitEprintRuntime emits `__lang_eprint(s)` — stderr
// counterpart to __lang_puts. Two write(2)s to fd 2 (string +
// newline). Preserves x19 so we can return the input pointer
// for the consistency `__lang_puts` already offers.
func (g *generator) emitEprintRuntime() {
	g.line("")
	g.line(".global __lang_eprint")
	g.typeDirective("__lang_eprint")
	g.label("__lang_eprint")
	if ast.UseTwoWordStrings(8) {
		// Two-word ABI: (data, len) in (x0, x1). Frame:
		// fp/lr (16) + 16-byte inline-spill scratch at
		// [x29+16..+31] + 16 align.
		g.emit("stp x29, x30, [sp, #-48]!")
		g.emit("mov x29, sp")
		g.emitStrLen2W("w2", "x1")
		g.emitStrDataPtr2W("x1", "x0", "x1", 16)
		g.emit("mov x0, #2")            // fd = stderr
		g.syscall("write")
		g.adrpAdd("x1", ".LLangNewline")
		g.emit("mov x2, #1")
		g.emit("mov x0, #2")
		g.syscall("write")
		// Return empty pair — caller's IR-side push of the
		// "void" return is unused.
		g.emit("mov x0, xzr")
		g.emit("mov x1, xzr")
		g.emit("ldp x29, x30, [sp], #48")
		g.emit("ret")
		g.sizeDirective("__lang_eprint")
		g.line(".ltorg")
		return
	}
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("mov x19, x0")
	g.emitStrLen("w2", "x0")
	g.emitStrDataPtr("x1", "x0", 24)
	g.emit("mov x0, #2")
	g.syscall("write")
	g.adrpAdd("x1", ".LLangNewline")
	g.emit("mov x2, #1")
	g.emit("mov x0, #2")
	g.syscall("write")
	g.emit("mov x0, x19")
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__lang_eprint")
	g.line(".ltorg")
}

// emitExitRuntime emits `__lang_exit(code)` — direct exit
// syscall. x0 already holds the user-supplied exit code from
// the caller's argument; syscallExit handles the Linux/Darwin
// ABI split. Never returns, so the trailing `ret` is for
// assembler-completeness only.
func (g *generator) emitExitRuntime() {
	g.line("")
	g.line(".global __lang_exit")
	g.typeDirective("__lang_exit")
	g.label("__lang_exit")
	g.syscallExit()
	g.emit("ret")
	g.sizeDirective("__lang_exit")
	g.line(".ltorg")
}

// emitArgsRuntime emits `__lang_args()` — returns a length-
// prefixed `string[]` materialised from the argc/argv pair
// captured by emitStartRuntime. Each entry is a fresh
// length-prefixed string with a trailing NUL preserved (for
// libc-shaped consumers like `puts`). Result is cached in
// `__lang_args_cache` so repeat calls are O(1).
//
// Slot layout uses callee-save x19..x23 across the inner
// __lang_alloc / __lang_memcpy calls; AAPCS64 mandates
// preservation, so the saved-pair pattern at function entry
// keeps them coherent across the bl chain.
func (g *generator) emitArgsRuntime() {
	g.line("")
	g.line(".global __lang_args")
	g.typeDirective("__lang_args")
	g.label("__lang_args")
	if ast.UseTwoWordStrings(8) {
		g.emitArgsRuntime2W()
		return
	}
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("str x23, [sp, #48]")
	// Fast path: cached pointer non-zero → return it.
	g.adrpAdd("x0", "__lang_args_cache")
	g.emit("ldr x1, [x0]")
	g.emit("cbz x1, .Largs_build")
	g.emit("mov x0, x1")
	g.emit("b .Largs_ret")
	g.label(".Largs_build")
	// x19 = argc, x20 = argv (pointer to char**)
	g.adrpAdd("x19", "__lang_argc")
	g.emit("ldr x19, [x19]")
	g.adrpAdd("x20", "__lang_argv")
	g.emit("ldr x20, [x20]")
	// Allocate the result string[] container: 8-byte header
	// (4 bytes pad + 4 bytes length) + argc * 8 bytes for
	// entry pointers. The 8-byte header keeps element 0 at an
	// 8-aligned offset so Apple Silicon's stricter alignment
	// for 8-byte LDR/STR is satisfied; the length prefix
	// sits at `data - 4` exactly as the IR's array layout
	// expects.
	g.emit("lsl x0, x19, #3")
	g.emit("add x0, x0, #8")
	g.emit("bl __lang_alloc")
	g.emit("add x21, x0, #8")     // x21 = result data pointer (8-aligned)
	g.emit("stur w19, [x21, #-4]") // length prefix = argc
	// for (i = 0; i < argc; i++)
	g.emit("mov x22, #0") // x22 = i
	g.label(".Largs_loop")
	g.emit("cmp x22, x19")
	g.emit("bge .Largs_done")
	// x23 = argv[i] (C string).
	g.emit("ldr x23, [x20, x22, lsl #3]")
	// Inline strlen on the C string.
	g.emit("mov x0, x23")
	g.label(".Largs_strlen")
	g.emit("ldrb w1, [x0]")
	g.emit("cbz w1, .Largs_strlen_done")
	g.emit("add x0, x0, #1")
	g.emit("b .Largs_strlen")
	g.label(".Largs_strlen_done")
	g.emit("sub x0, x0, x23")     // x0 = strlen
	g.emit("mov x9, x0")          // x9 = saved strlen (caller-save, not preserved across bl)
	// Allocate strlen + 5 (4 prefix + N data + 1 trailing NUL).
	g.emit("add x0, x0, #5")
	g.emit("bl __lang_alloc")
	g.emit("add x10, x0, #4")     // x10 = string data pointer
	g.emit("stur w9, [x10, #-4]") // length prefix
	// Stash data pointer in the reserved scratch slot at
	// `[sp, #56]` (frame is 64 bytes: 16 fp/lr + 16 x19/x20
	// + 16 x21/x22 + 8 x23 + 8 scratch). Caller-save x10 won't
	// survive the bl. Don't use `[sp]` — that overwrites saved
	// x29.
	g.emit("str x10, [sp, #56]")
	// memcpy(x10, x23, strlen + 1) — include NUL.
	g.emit("mov x0, x10")
	g.emit("mov x1, x23")
	g.emit("add x2, x9, #1")
	g.emit("bl __lang_memcpy")
	// result[i] = x10 — full 8-byte pointer store.
	g.emit("ldr x10, [sp, #56]")
	g.emit("str x10, [x21, x22, lsl #3]")
	g.emit("add x22, x22, #1")
	g.emit("b .Largs_loop")
	g.label(".Largs_done")
	// Cache + return.
	g.adrpAdd("x0", "__lang_args_cache")
	g.emit("str x21, [x0]")
	g.emit("mov x0, x21")
	g.label(".Largs_ret")
	g.emit("ldr x23, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("__lang_args")
	g.line(".ltorg")
}

// emitArgsRuntime2W is the two-word-ABI variant of
// emitArgsRuntime. Each `string[]` entry is now a 16-byte
// `(data, len)` pair instead of an 8-byte single-pointer.
// Layout matches the IR's two-word string[] stride:
//
//   - header: 16 bytes total, length stored at `[data - 4]`
//   - element i: at `[data + i * 16]` with data at +0..+7 and
//     len at +8..+15
//
// Cached pointer mechanics unchanged.
func (g *generator) emitArgsRuntime2W() {
	g.emit("stp x29, x30, [sp, #-80]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("str x25, [sp, #64]")
	// Cached?
	g.adrpAdd("x0", "__lang_args_cache")
	g.emit("ldr x1, [x0]")
	g.emit("cbz x1, .Largs2w_build")
	g.emit("mov x0, x1")
	g.emit("b .Largs2w_ret")
	g.label(".Largs2w_build")
	g.adrpAdd("x19", "__lang_argc")
	g.emit("ldr x19, [x19]")
	g.adrpAdd("x20", "__lang_argv")
	g.emit("ldr x20, [x20]")
	// Allocate: 16-byte header + argc * 16 (entries are 16-byte
	// (data, len) pairs). Header is 16 bytes so element 0 sits
	// at +16 = stride-aligned; length prefix at `[base + 12]`.
	g.emit("lsl x0, x19, #4")  // argc * 16
	g.emit("add x0, x0, #16")  // + header
	g.emit("bl __lang_alloc")
	g.emit("add x21, x0, #16") // x21 = data pointer (past header)
	g.emit("stur w19, [x21, #-4]") // length prefix = argc
	g.emit("mov x22, #0")      // loop counter i
	g.label(".Largs2w_loop")
	g.emit("cmp x22, x19")
	g.emit("bge .Largs2w_done")
	// argv[i] (C string).
	g.emit("ldr x23, [x20, x22, lsl #3]")
	// Inline strlen.
	g.emit("mov x0, x23")
	g.label(".Largs2w_strlen")
	g.emit("ldrb w1, [x0]")
	g.emit("cbz w1, .Largs2w_strlen_done")
	g.emit("add x0, x0, #1")
	g.emit("b .Largs2w_strlen")
	g.label(".Largs2w_strlen_done")
	g.emit("sub x0, x0, x23") // x0 = strlen
	g.emit("mov x24, x0")     // x24 = strlen (callee-save, survives bl)
	// Allocate strlen bytes (no length prefix; len lives in
	// the entry's `len` half).
	g.emit("bl __lang_alloc")
	g.emit("mov x25, x0")     // x25 = dst (callee-save, survives bl)
	// memcpy(dst, src, strlen).
	g.emit("mov x0, x25")
	g.emit("mov x1, x23")
	g.emit("mov x2, x24")
	g.emit("bl __lang_memcpy")
	// Write entry: data at [x21 + i*16], len at [x21 + i*16 + 8].
	g.emit("lsl x11, x22, #4")     // x11 = i * 16
	g.emit("str x25, [x21, x11]")  // data
	g.emit("add x11, x11, #8")
	g.emit("str x24, [x21, x11]")  // len (= strlen, heap form, top bit clear)
	g.emit("add x22, x22, #1")
	g.emit("b .Largs2w_loop")
	g.label(".Largs2w_done")
	g.adrpAdd("x0", "__lang_args_cache")
	g.emit("str x21, [x0]")
	g.emit("mov x0, x21")
	g.label(".Largs2w_ret")
	g.emit("ldr x25, [sp, #64]")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #80")
	g.emit("ret")
	g.sizeDirective("__lang_args")
	g.line(".ltorg")
}

// emitArenaRuntime emits the bump-cursor snapshot/rewind pair:
//
//   - `__lang_arena_save() -> i64` — returns the current
//     `__lang_heap_ptr`.
//   - `__lang_arena_restore(saved)` — writes `saved` back into
//     `__lang_heap_ptr`, reclaiming everything allocated after
//     the matching save in a single store. Caller is trusted
//     not to hold pointers into the reclaimed region.
//
// Both leaf functions; one load / one store.
func (g *generator) emitArenaRuntime() {
	g.line("")
	g.line(".global __lang_arena_save")
	g.typeDirective("__lang_arena_save")
	g.label("__lang_arena_save")
	g.adrpAdd("x0", "__lang_heap_ptr")
	g.emit("ldr x0, [x0]")
	g.emit("ret")
	g.sizeDirective("__lang_arena_save")

	g.line("")
	g.line(".global __lang_arena_restore")
	g.typeDirective("__lang_arena_restore")
	g.label("__lang_arena_restore")
	g.adrpAdd("x1", "__lang_heap_ptr")
	g.emit("str x0, [x1]")
	g.emit("ret")
	g.sizeDirective("__lang_arena_restore")
	g.line(".ltorg")
}

// emitReadLineRuntime emits `__lang_read_line()` — reads stdin
// one byte at a time into the 4 KiB `__lang_read_line_buf`,
// stops at '\n' (kept in the result) or 4 KiB or EOF/error.
// Returns Option[string]: Some(line) when at least one byte
// was read, None when the very first read returned 0 (EOF
// before any input). Option layout matches the wasm shape:
//
//	Some: [tag=0 : 4][string_ptr : 4]   (8 bytes)
//	None: [tag=1 : 4]                    (4 bytes)
//
// Callee-save x19/x20/x21 hold buf base, bytes-read, and the
// just-read byte across the inner read syscall + alloc/memcpy
// calls.
func (g *generator) emitReadLineRuntime() {
	g.line("")
	g.line(".global __lang_read_line")
	g.typeDirective("__lang_read_line")
	g.label("__lang_read_line")
	twoWord := ast.UseTwoWordStrings(8)
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("str x21, [sp, #32]")
	g.adrpAdd("x19", "__lang_read_line_buf")
	g.emit("mov x20, #0") // x20 = bytes read so far
	g.label(".Lrl_loop")
	g.emit("cmp x20, #4096")
	g.emit("bge .Lrl_done")
	// read(0, buf + x20, 1)
	g.emit("mov x0, #0")
	g.emit("add x1, x19, x20")
	g.emit("mov x2, #1")
	g.syscall("read")
	// EOF (0) or error (<0) → finish.
	g.emit("cmp x0, #1")
	g.emit("blt .Lrl_done")
	// Examine the byte just read.
	g.emit("add x21, x19, x20")
	g.emit("ldrb w21, [x21]")
	g.emit("add x20, x20, #1")
	g.emit("cmp w21, #10") // '\n'
	g.emit("beq .Lrl_done")
	g.emit("b .Lrl_loop")
	g.label(".Lrl_done")
	// EOF before any byte → return None.
	g.emit("cbnz x20, .Lrl_some")
	g.emit("mov x0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]") // tag = 1
	g.emit("b .Lrl_ret")
	g.label(".Lrl_some")
	if twoWord {
		// Two-word heap form: alloc exactly x20 bytes (no
		// length prefix, no trailing NUL).
		g.emit("mov x0, x20")
		g.emit("bl __lang_alloc")
		g.emit("mov x21, x0") // x21 = data ptr
		// memcpy(x21, x19, x20).
		g.emit("mov x0, x21")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __lang_memcpy")
		// Wrap as Some(string). 24-byte box: tag@0, pad@4,
		// data@8, len@16.
		g.emit("mov x19, x21") // stash data ptr
		g.emit("mov x0, #24")
		g.emit("bl __lang_alloc")
		g.emit("str wzr, [x0]")     // tag = 0 (Some)
		g.emit("str x19, [x0, #8]")  // data
		g.emit("str x20, [x0, #16]") // len
		g.emit("b .Lrl_ret")
	} else {
		// alloc(len + 5): 4 prefix + N data + 1 trailing NUL.
		g.emit("add x0, x20, #5")
		g.emit("bl __lang_alloc")
		g.emit("add x21, x0, #4")     // x21 = data ptr
		g.emit("stur w20, [x21, #-4]") // length prefix
		g.emit("mov x0, x21")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __lang_memcpy")
		g.emit("add x0, x21, x20")
		g.emit("strb wzr, [x0]")
		g.emit("mov x19, x21")
		g.emit("mov x0, #16")
		g.emit("bl __lang_alloc")
		g.emit("str wzr, [x0]")
		g.emit("str x19, [x0, #8]")
	}
	g.label(".Lrl_ret")
	g.emit("ldr x21, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__lang_read_line")
	g.line(".ltorg")
}

// emitStdinRuntime emits `__lang_stdin()` — a 1-instruction
// stub that returns 0. The checker requires `stdin()` to be
// callable but the arm64 backend doesn't yet model per-fd
// Readers, so the receiver value is unused; any sentinel
// works.
func (g *generator) emitStdinRuntime() {
	g.line("")
	g.line(".global __lang_stdin")
	g.typeDirective("__lang_stdin")
	g.label("__lang_stdin")
	g.emit("mov x0, #0")
	g.emit("ret")
	g.sizeDirective("__lang_stdin")
	g.line(".ltorg")
}

// emitRandomBytesRuntime emits `__lang_random_bytes(n)` —
// allocates a fresh length-prefixed lang string of n bytes
// and fills it with kernel CSPRNG output. Returns the data
// pointer.
//
// Linux: single getrandom(buf, n, 0) syscall (#278). Blocking
// /dev/urandom; flags=0.
//
// Darwin: getentropy(buf, len) (#500), max 256 bytes per
// call. We loop in 256-byte chunks for n > 256. getentropy
// has no flags arg.
//
// Both fill the buffer in-place; both append a trailing NUL
// past the end so libc-shaped consumers don't read garbage.
//
// Frame uses callee-save x19 (data ptr base, used for the
// trailing NUL + return) and x20 (n / write cursor).
func (g *generator) emitRandomBytesRuntime() {
	g.line("")
	g.line(".global __lang_random_bytes")
	g.typeDirective("__lang_random_bytes")
	g.label("__lang_random_bytes")
	// Frame uses callee-save x19..x22 so the Darwin chunked
	// loop can keep cursor + remaining live across the inner
	// getentropy syscall without an extra spill slot.
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("mov x20, x0") // x20 = n (saved for trailing NUL + length prefix)
	twoWord := ast.UseTwoWordStrings(8)
	if twoWord {
		// Two-word heap form: no length prefix in the data
		// segment; len lives on the operand stack as the
		// second return word. Alloc exactly n bytes.
		g.emit("mov w0, w20")
		g.emit("bl __lang_alloc")
		g.emit("mov x19, x0") // x19 = data ptr
	} else {
		// Legacy single-pointer: length prefix at [data-4].
		g.emit("add x0, x20, #5")
		g.emit("bl __lang_alloc")
		g.emit("add x19, x0, #4")     // x19 = data ptr (past prefix)
		g.emit("stur w20, [x19, #-4]") // length prefix
	}
	if g.darwin {
		// Darwin getentropy(buf, len), syscall 500. Max 256
		// bytes per call. Walk the buffer in 256-byte chunks
		// using callee-save x21 (cursor) + x22 (remaining)
		// so they survive across the syscall. The chunk size
		// gets recomputed on each iteration; cheaper than
		// stashing it.
		g.emit("mov x21, x19") // write cursor
		g.emit("mov x22, x20") // bytes remaining
		g.label(".Lrb_loop")
		g.emit("cbz x22, .Lrb_done")
		g.emit("mov x0, x21")
		g.emit("mov x1, #256")
		g.emit("cmp x22, x1")
		g.emit("csel x1, x22, x1, lo") // x1 = min(remaining, 256)
		g.emit("mov x16, #%d", darGetentropy)
		g.emit("svc #0x80")
		// Recompute chunk size to advance cursor / remaining
		// (x1 was clobbered by the syscall).
		g.emit("mov x1, #256")
		g.emit("cmp x22, x1")
		g.emit("csel x1, x22, x1, lo")
		g.emit("add x21, x21, x1")
		g.emit("sub x22, x22, x1")
		g.emit("b .Lrb_loop")
		g.label(".Lrb_done")
	} else {
		// Linux getrandom(buf, len, flags=0), syscall 278.
		// Lives in `linuxOnlySysno` — `g.syscall("getrandom")`
		// asserts at codegen time if it gets reached on Darwin
		// (the if/else above is the inline Darwin branch).
		g.emit("mov x0, x19")
		g.emit("mov x1, x20")
		g.emit("mov x2, #0")
		g.syscall("getrandom")
	}
	if !twoWord {
		// Trailing NUL at data + n (only for legacy heap
		// form — two-word heap form has no NUL padding,
		// length is on the operand stack).
		g.emit("add x1, x19, x20")
		g.emit("strb wzr, [x1]")
	}
	g.emit("mov x0, x19") // return data ptr
	if twoWord {
		g.emit("mov w1, w20") // return len = n
	}
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__lang_random_bytes")
	g.line(".ltorg")
}

// emitIoErrorRuntime emits `__lang_io_error(errno, path) → ptr`
// — constructs an `IoError` enum-box for the given Linux errno.
// Layout matches the IR: 16-byte box `{tag:i32 @0, _:i32 @4,
// payload:ptr @8}` for variants with payloads, 8 bytes
// `{tag:i32 @0}` for payload-less variants. Tag values follow
// the checker's variant declaration order:
//
//	0 = NotFound(string)        4 = Interrupted
//	1 = PermissionDenied(s)     5 = Unsupported
//	2 = AlreadyExists(s)        6 = Other(string, string)
//	3 = InvalidUtf8(s)
//
// errno → variant mapping:
//
//	ENOENT (2)  → NotFound          EACCES (13) → PermissionDenied
//	EEXIST (17) → AlreadyExists     EINTR  (4)  → Interrupted
//	all other   → Other(path, "")
//
// We don't surface InvalidUtf8 here (the kernel APIs we use
// don't produce it) or Unsupported (Linux always supports the
// ops we issue). The Other variant carries (path, "") — the
// second string is a deliberately empty placeholder rather
// than e.g. strerror text; tracker note in BACKEND-PARITY.md
// can promote that later.
//
// Args: x0 = errno (positive), x1 = path data ptr.
// Returns: x0 = IoError box ptr.
func (g *generator) emitIoErrorRuntime() {
	g.line("")
	g.line(".global __lang_io_error")
	g.typeDirective("__lang_io_error")
	g.label("__lang_io_error")
	if ast.UseTwoWordStrings(8) {
		g.emitIoErrorRuntime2W()
		return
	}
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("mov x19, x0") // errno
	g.emit("mov x20, x1") // path

	// Map errno → tag. Default = 6 (Other).
	g.emit("cmp w19, #2")  // ENOENT
	g.emit("b.eq .Lioe_notfound")
	g.emit("cmp w19, #13") // EACCES
	g.emit("b.eq .Lioe_perm")
	g.emit("cmp w19, #17") // EEXIST
	g.emit("b.eq .Lioe_exists")
	g.emit("cmp w19, #4")  // EINTR
	g.emit("b.eq .Lioe_intr")

	// Other(path, ""). The "" payload needs the SECOND string
	// payload at +16 (third 8-byte slot). Box is 24 bytes for
	// two payloads. The empty-string ptr comes from interning
	// "" at compile time — but we need a runtime constant.
	// Use the .LStr_empty label below.
	g.emit("mov x0, #24")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #6")
	g.emit("str w1, [x0]")
	g.emit("str x20, [x0, #8]") // path
	g.adrpAdd("x1", ".LStr_ioerr_empty")
	g.emit("str x1, [x0, #16]") // ""
	g.emit("b .Lioe_done")

	g.label(".Lioe_notfound")
	g.emit("mov w19, #0")
	g.emit("b .Lioe_with_path")
	g.label(".Lioe_perm")
	g.emit("mov w19, #1")
	g.emit("b .Lioe_with_path")
	g.label(".Lioe_exists")
	g.emit("mov w19, #2")
	g.emit("b .Lioe_with_path")
	g.label(".Lioe_intr")
	// Interrupted has no payload → 8-byte box.
	g.emit("mov x0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #4")
	g.emit("str w1, [x0]")
	g.emit("b .Lioe_done")

	g.label(".Lioe_with_path")
	g.emit("mov x0, #16")
	g.emit("bl __lang_alloc")
	g.emit("str w19, [x0]")   // tag
	g.emit("str x20, [x0, #8]") // path
	g.label(".Lioe_done")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__lang_io_error")

	// Compile-time empty-string literal used for the Other
	// variant's second-string slot. Length=0, NUL-terminated.
	// We can't easily intern this via the regular string-pool
	// path because the io-error runtime emits before the
	// string section; ad-hoc emit here keeps things simple.
	if g.darwin {
		g.line(".section __TEXT,__const")
	} else {
		g.line(".section .rodata")
	}
	g.line(".align 2")
	g.line("\t.4byte 0")
	g.label(".LStr_ioerr_empty")
	g.line("\t.byte 0")
	g.line(".text")
	g.line(".ltorg")
}

// emitIoErrorRuntime2W is the two-word-ABI variant.
// Signature: `__lang_io_error(errno, path_data, path_len)` in
// (x0, x1, x2). Returns the heap-allocated IoError box ptr
// in x0.
//
// Box layout under two-word strings:
//
//	NotFound(path) / PermissionDenied(path) /
//	  AlreadyExists(path):   tag@0, pad@4, path_data@8, path_len@16 → 24 bytes
//	Other(path, msg):        tag@0, pad@4, path_data@8, path_len@16,
//	                         msg_data@24, msg_len@32 → 40 bytes
//	Interrupted:             tag@0 → 8 bytes (no payload)
//
// The empty-string sentinel used as the Other variant's
// `msg` is a (data=0, len=`1<<63`) inline-empty pair —
// stored inline rather than as a `.LStr_ioerr_empty` adrp
// since the empty form doesn't need memory.
func (g *generator) emitIoErrorRuntime2W() {
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("str x21, [sp, #32]")
	g.emit("mov x19, x0") // errno
	g.emit("mov x20, x1") // path_data
	g.emit("mov x21, x2") // path_len
	// errno → tag.
	g.emit("cmp w19, #2")  // ENOENT
	g.emit("b.eq .Lioe2w_notfound")
	g.emit("cmp w19, #13") // EACCES
	g.emit("b.eq .Lioe2w_perm")
	g.emit("cmp w19, #17") // EEXIST
	g.emit("b.eq .Lioe2w_exists")
	g.emit("cmp w19, #4")  // EINTR
	g.emit("b.eq .Lioe2w_intr")
	// Other(path, "") — 40-byte box, msg = empty inline pair.
	g.emit("mov x0, #40")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #6")
	g.emit("str w1, [x0]")
	g.emit("str x20, [x0, #8]")  // path_data
	g.emit("str x21, [x0, #16]") // path_len
	g.emit("str xzr, [x0, #24]") // msg_data = 0
	g.emit("movz x1, #0x8000, lsl #48")
	g.emit("str x1, [x0, #32]")  // msg_len = inline-empty
	g.emit("b .Lioe2w_done")
	g.label(".Lioe2w_notfound")
	g.emit("mov w19, #0")
	g.emit("b .Lioe2w_with_path")
	g.label(".Lioe2w_perm")
	g.emit("mov w19, #1")
	g.emit("b .Lioe2w_with_path")
	g.label(".Lioe2w_exists")
	g.emit("mov w19, #2")
	g.emit("b .Lioe2w_with_path")
	g.label(".Lioe2w_intr")
	g.emit("mov x0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #4")
	g.emit("str w1, [x0]")
	g.emit("b .Lioe2w_done")
	g.label(".Lioe2w_with_path")
	g.emit("mov x0, #24")
	g.emit("bl __lang_alloc")
	g.emit("str w19, [x0]")
	g.emit("str x20, [x0, #8]")  // path_data
	g.emit("str x21, [x0, #16]") // path_len
	g.label(".Lioe2w_done")
	g.emit("ldr x21, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__lang_io_error")
	g.line(".ltorg")
}

// emitReadFileRuntime emits `__lang_read_file(path) →
// Result[string, IoError]`. Pipeline: openat(AT_FDCWD, path,
// O_RDONLY) → fstat → alloc length-prefixed buffer → read-loop
// → close → wrap as Ok(string). Any syscall error short-circuits
// to Err(IoError) via __lang_io_error.
//
// Result box layout (matches IR): 16-byte heap obj
// `{tag:i32 @0, _:i32 @4, payload:ptr @8}` where:
//
//	tag=0 → Ok(string), payload = string data ptr
//	tag=1 → Err(IoError), payload = IoError box ptr
func (g *generator) emitReadFileRuntime() {
	g.line("")
	g.line(".global __lang_read_file")
	g.typeDirective("__lang_read_file")
	g.label("__lang_read_file")
	if ast.UseTwoWordStrings(8) {
		g.emitReadFileRuntime2W()
		return
	}
	// Frame: 64-byte base + 192-byte statbuf scratch = 256.
	// x19 = path, x20 = fd, x21 = buf base, x22 = size,
	// x23 = bytes_read.
	g.emit("stp x29, x30, [sp, #-256]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("mov x19, x0") // x19 = original path string value (heap or inline)
	g.emitStrDataPtr("x24", "x0", 208) // x24 = path byte ptr for openat; scratch lives at [x29 + 208]

	// openat(AT_FDCWD=-100, path, O_RDONLY=0, 0)
	g.emit("mov x0, #-100")
	g.emit("mov x1, x24")
	g.emit("mov x2, #0")
	g.emit("mov x3, #0")
	g.syscall("openat")
	g.emit("tbnz x0, #63, .Lrf_err_open")
	g.emit("mov x20, x0") // fd

	// fstat(fd, statbuf). statbuf scratch at sp+64..sp+256 (192 bytes).
	g.emit("mov x0, x20")
	g.emit("add x1, sp, #64")
	g.syscall("fstat")
	g.emit("tbnz x0, #63, .Lrf_err_close")
	g.emit("ldr x22, [sp, #64 + 48]") // st_size

	// alloc string buf: 4 (len prefix) + size. x21 holds the
	// data pointer (one past the prefix) so the read loop and
	// the Ok-payload build can use it directly.
	g.emit("add x0, x22, #4")
	g.emit("bl __lang_alloc")
	g.emit("add x21, x0, #4") // x21 = data ptr
	g.emitStrLenStore("w22", "x21")

	// Read loop. x23 = bytes_read (cumulative).
	g.emit("mov x23, #0")
	g.label(".Lrf_loop")
	g.emit("cmp x23, x22")
	g.emit("b.ge .Lrf_done")
	g.emit("mov x0, x20")
	g.emit("add x1, x21, x23")
	g.emit("sub x2, x22, x23")
	g.syscall("read")
	g.emit("tbnz x0, #63, .Lrf_err_close")
	g.emit("cbz x0, .Lrf_done") // EOF before end (file shrunk)
	g.emit("add x23, x23, x0")
	g.emit("b .Lrf_loop")

	g.label(".Lrf_done")
	// close(fd).
	g.emit("mov x0, x20")
	g.syscall("close")
	// Build Result.Ok(string).
	g.emit("mov x0, #16")
	g.emit("bl __lang_alloc")
	g.emit("str wzr, [x0]")    // tag=0 (Ok)
	g.emit("str x21, [x0, #8]") // payload @ +8 — x21 is already the string data ptr
	g.emit("b .Lrf_return")

	g.label(".Lrf_err_close")
	// errno = -x0, then close(fd), then build Err.
	g.emit("neg x21, x0")     // x21 = errno (reuse slot)
	g.emit("mov x0, x20")
	g.syscall("close")
	g.emit("b .Lrf_err_dispatch")

	g.label(".Lrf_err_open")
	g.emit("neg x21, x0") // errno

	g.label(".Lrf_err_dispatch")
	// __lang_io_error(errno, path) → IoError box in x0.
	g.emit("mov x0, x21")
	g.emit("mov x1, x19")
	g.emit("bl __lang_io_error")
	// Stash the IoError box in x19 (callee-save; path no longer
	// needed). x1 would NOT survive the next __lang_alloc call.
	g.emit("mov x19, x0")
	g.emit("mov x0, #16")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]")
	g.emit("str x19, [x0, #8]")

	g.label(".Lrf_return")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #256")
	g.emit("ret")
	g.sizeDirective("__lang_read_file")
	g.line(".ltorg")
}

// emitReadFileRuntime2W is the two-word-ABI variant of
// emitReadFileRuntime. Signature:
// `__lang_read_file(path_data, path_len)` in (x0, x1).
// Returns a heap-allocated `Result[string, IoError]` box ptr
// in x0:
//
//	Ok(string):  24-byte box  {tag=0 @0, _pad @4, data @8, len @16}
//	Err(IoError): 16-byte box  {tag=1 @0, payload=IoError @8}
//
// The Ok-string payload uses the OpStore{WidthString} layout
// (data at +0 / len at +8 relative to payload offset +8 in
// the box → data at box+8, len at box+16). Total box size:
// max(24, 16) = 24 bytes.
//
// Uses callee-saves: x19 = path data, x20 = path len (the
// original two-word string), x21 = fd, x22 = string buf data
// ptr, x23 = file size (length), x24 = bytes read.
func (g *generator) emitReadFileRuntime2W() {
	// Frame: 96-byte base (fp/lr + 6 callee-saves + 16 align)
	// + 192-byte statbuf = 288. Statbuf at [x29 + 96].
	g.emit("stp x29, x30, [sp, #-288]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("str x25, [sp, #64]")
	g.emit("mov x19, x0") // x19 = path_data
	g.emit("mov x20, x1") // x20 = path_len
	// path → byte ptr (inline spill at [x29+80..95]).
	g.emitStrDataPtr2W("x25", "x19", "x20", 80) // x25 = path byte ptr
	// openat(AT_FDCWD=-100, path, O_RDONLY=0, 0)
	g.emit("mov x0, #-100")
	g.emit("mov x1, x25")
	g.emit("mov x2, #0")
	g.emit("mov x3, #0")
	g.syscall("openat")
	g.emit("tbnz x0, #63, .Lrf2w_err_open")
	g.emit("mov x21, x0") // fd
	// fstat(fd, statbuf).
	g.emit("mov x0, x21")
	g.emit("add x1, x29, #96") // statbuf at [x29+96]
	g.syscall("fstat")
	g.emit("tbnz x0, #63, .Lrf2w_err_close")
	g.emit("ldr x23, [x29, #96 + 48]") // st_size
	// Allocate exactly st_size bytes for the result string
	// data — no length prefix (two-word ABI).
	g.emit("mov x0, x23")
	g.emit("bl __lang_alloc")
	g.emit("mov x22, x0") // x22 = data ptr
	// Read loop.
	g.emit("mov x24, #0")
	g.label(".Lrf2w_loop")
	g.emit("cmp x24, x23")
	g.emit("b.ge .Lrf2w_done")
	g.emit("mov x0, x21")
	g.emit("add x1, x22, x24")
	g.emit("sub x2, x23, x24")
	g.syscall("read")
	g.emit("tbnz x0, #63, .Lrf2w_err_close")
	g.emit("cbz x0, .Lrf2w_done")
	g.emit("add x24, x24, x0")
	g.emit("b .Lrf2w_loop")
	g.label(".Lrf2w_done")
	// close(fd).
	g.emit("mov x0, x21")
	g.syscall("close")
	// Build Result.Ok(string) box: 24 bytes — {tag@0,
	// pad@4, data@8, len@16}.
	g.emit("mov x0, #24")
	g.emit("bl __lang_alloc")
	g.emit("str wzr, [x0]")       // tag = 0 (Ok)
	g.emit("str x22, [x0, #8]")    // payload data
	g.emit("str x23, [x0, #16]")   // payload len
	g.emit("b .Lrf2w_return")
	g.label(".Lrf2w_err_close")
	g.emit("neg x22, x0") // x22 = errno
	g.emit("mov x0, x21")
	g.syscall("close")
	g.emit("b .Lrf2w_err_dispatch")
	g.label(".Lrf2w_err_open")
	g.emit("neg x22, x0")
	g.label(".Lrf2w_err_dispatch")
	// __lang_io_error(errno, path_data, path_len). Updated
	// to take a two-word string for the path.
	g.emit("mov x0, x22")
	g.emit("mov x1, x19")
	g.emit("mov x2, x20")
	g.emit("bl __lang_io_error")
	g.emit("mov x19, x0") // stash IoError box across alloc
	g.emit("mov x0, #16")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]") // tag = 1 (Err)
	g.emit("str x19, [x0, #8]")
	g.label(".Lrf2w_return")
	g.emit("ldr x25, [sp, #64]")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #288")
	g.emit("ret")
	g.sizeDirective("__lang_read_file")
	g.line(".ltorg")
}

// emitWriteFileRuntime emits `__lang_write_file(path, content)
// → Option[IoError]`. Pipeline: openat(AT_FDCWD, path,
// O_WRONLY|O_CREAT|O_TRUNC, 0644) → write-loop → close → None.
// Any syscall error short-circuits to Some(IoError).
//
// Option[IoError] layout (matches IR):
//
//	tag=0 → Some(IoError), payload = IoError box ptr @ +8
//	tag=1 → None (8-byte box, no payload)
//
// O_WRONLY = 1, O_CREAT = 0100 (octal) = 64, O_TRUNC = 01000
// (octal) = 512. Combined flags = 577.
func (g *generator) emitWriteFileRuntime() {
	g.line("")
	g.line(".global __lang_write_file")
	g.typeDirective("__lang_write_file")
	g.label("__lang_write_file")
	if ast.UseTwoWordStrings(8) {
		g.emitWriteFileRuntime2W()
		return
	}
	// Frame: 80 bytes — fp/lr (16) + 6 callee-saves (48) +
	// 16 SSO scratch (8 for path materialise + 8 for content
	// materialise).
	g.emit("stp x29, x30, [sp, #-80]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("mov x19, x0")                // x19 = ORIGINAL path string value (for io_error)
	g.emitStrLen("w22", "x1")            // x22 = content_len (before content materialise)
	g.emitStrDataPtr("x20", "x1", 72)    // x20 = content byte ptr
	g.emitStrDataPtr("x24", "x19", 64)   // x24 = path byte ptr (preserves x19 = original)

	// openat(AT_FDCWD, path, O_WRONLY|O_CREAT|O_TRUNC=577, 0644)
	g.emit("mov x0, #-100")
	g.emit("mov x1, x24")
	g.emit("mov x2, #577")
	g.emit("mov x3, #0644")
	g.syscall("openat")
	g.emit("tbnz x0, #63, .Lwf_err_open")
	g.emit("mov x21, x0") // fd

	// Write loop. x23 = bytes_written.
	g.emit("mov x23, #0")
	g.label(".Lwf_loop")
	g.emit("cmp x23, x22")
	g.emit("b.ge .Lwf_done")
	g.emit("mov x0, x21")
	g.emit("add x1, x20, x23")
	g.emit("sub x2, x22, x23")
	g.syscall("write")
	g.emit("tbnz x0, #63, .Lwf_err_close")
	g.emit("add x23, x23, x0")
	g.emit("b .Lwf_loop")

	g.label(".Lwf_done")
	g.emit("mov x0, x21")
	g.syscall("close")
	// Return None: 8-byte box, tag=1.
	g.emit("mov x0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]")
	g.emit("b .Lwf_return")

	g.label(".Lwf_err_close")
	g.emit("neg x22, x0") // errno
	g.emit("mov x0, x21")
	g.syscall("close")
	g.emit("b .Lwf_err_dispatch")

	g.label(".Lwf_err_open")
	g.emit("neg x22, x0")

	g.label(".Lwf_err_dispatch")
	g.emit("mov x0, x22")
	g.emit("mov x1, x19")
	g.emit("bl __lang_io_error")
	// Stash IoError box in x19 (callee-save; path / content no
	// longer needed) — x1 would NOT survive the next alloc call.
	g.emit("mov x19, x0")
	g.emit("mov x0, #16")
	g.emit("bl __lang_alloc")
	g.emit("str wzr, [x0]")
	g.emit("str x19, [x0, #8]")

	g.label(".Lwf_return")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #80")
	g.emit("ret")
	g.sizeDirective("__lang_write_file")
	g.line(".ltorg")
}

// emitWriteFileRuntime2W is the two-word-ABI variant.
// Signature: `__lang_write_file(path_data, path_len,
// content_data, content_len)` in (x0..x3). Returns
// `Option[IoError]` heap-box ptr in x0:
//   Some(IoError): 16-byte box {tag=0@0, payload=err@8}
//   None:           8-byte box {tag=1@0}
func (g *generator) emitWriteFileRuntime2W() {
	// Frame: 96 bytes. fp/lr (16) + 6 callee-saves (48) +
	// 2× 16-byte inline-spill scratch for path + content (32).
	g.emit("stp x29, x30, [sp, #-96]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("mov x19, x0") // path_data
	g.emit("mov x20, x1") // path_len
	g.emit("mov x21, x2") // content_data
	g.emit("mov x22, x3") // content_len
	// content byte length + byte ptr.
	g.emitStrLen2W("w24", "x22")             // x24 = content byte length
	g.emitStrDataPtr2W("x23", "x21", "x22", 64) // x23 = content byte ptr; scratch at [x29+64]
	// path byte ptr (separate scratch).
	g.emitStrDataPtr2W("x0", "x19", "x20", 80)  // x0 = path byte ptr; scratch at [x29+80]
	// openat(AT_FDCWD, path, O_WRONLY|O_CREAT|O_TRUNC=577, 0644)
	g.emit("mov x1, x0")
	g.emit("mov x0, #-100")
	g.emit("mov x2, #577")
	g.emit("mov x3, #0644")
	g.syscall("openat")
	g.emit("tbnz x0, #63, .Lwf2w_err_open")
	g.emit("mov x21, x0") // fd (reuse x21 — content_data no longer needed past this point)
	// Write loop. x22 = cumulative bytes written (callee-
	// save). Note: x20 still holds path_len which we needed
	// for io_error — preserved across the loop since syscall
	// doesn't touch x20.
	g.emit("mov x22, #0")
	g.label(".Lwf2w_loop")
	g.emit("cmp x22, x24")
	g.emit("b.ge .Lwf2w_done")
	g.emit("mov x0, x21")        // fd
	g.emit("add x1, x23, x22")   // buf + offset
	g.emit("sub x2, x24, x22")   // remaining
	g.syscall("write")
	g.emit("tbnz x0, #63, .Lwf2w_err_close")
	g.emit("add x22, x22, x0")
	g.emit("b .Lwf2w_loop")
	g.label(".Lwf2w_done")
	g.emit("mov x0, x21")
	g.syscall("close")
	// Return None: 8-byte box, tag=1.
	g.emit("mov x0, #8")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]")
	g.emit("b .Lwf2w_return")
	g.label(".Lwf2w_err_close")
	g.emit("neg x22, x0") // errno
	g.emit("mov x0, x21")
	g.syscall("close")
	g.emit("b .Lwf2w_err_dispatch")
	g.label(".Lwf2w_err_open")
	g.emit("neg x22, x0")
	g.label(".Lwf2w_err_dispatch")
	// __lang_io_error(errno, path_data, path_len).
	g.emit("mov x0, x22")
	g.emit("mov x1, x19")
	g.emit("mov x2, x20")
	g.emit("bl __lang_io_error")
	g.emit("mov x19, x0") // stash IoError box
	g.emit("mov x0, #16")
	g.emit("bl __lang_alloc")
	g.emit("str wzr, [x0]")     // tag = 0 (Some)
	g.emit("str x19, [x0, #8]") // payload
	g.label(".Lwf2w_return")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #96")
	g.emit("ret")
	g.sizeDirective("__lang_write_file")
	g.line(".ltorg")
}

// emitReaderWriterRuntime emits the full set of Reader / Writer
// runtimes — both the entry points that allocate handle
// structs (stdin/stdout/stderr/open_reader/open_writer/
// open_appender) and the method runtimes (read_line /
// read_chunk / close / write).
//
// Handle struct layout: 4-byte i32 `fd` at +0 (alloc rounds up
// to 16, so each handle costs 16 bytes of heap). The lang-
// level `Reader` / `Writer` structs are pointer-shaped: a
// `Reader` value is a pointer to one of these handles, and
// `r.fd` lowers to `[r+0]`.
//
// Error shape: helpers that can fail surface `Option[IoError]`
// or `Result[T, IoError]` via the shared `__lang_io_error`
// helper. Reader.read_line / Reader.read_chunk follow the
// wasm contract and return `Option[string]` (None on EOF or
// error; no IoError surfacing).
func (g *generator) emitReaderWriterRuntime() {
	// __lang_make_handle(fd_in_w0) → ptr to 4-byte struct
	// {fd:i32 @0}. Used by stdin/stdout/stderr + open_*.
	g.line("")
	g.line(".global __lang_make_handle")
	g.typeDirective("__lang_make_handle")
	g.label("__lang_make_handle")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("mov w19, w0")   // stash fd
	g.emit("mov w0, #4")
	g.emit("bl __lang_alloc")
	g.emit("str w19, [x0]") // fd at +0
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__lang_make_handle")

	// __lang_stdin / __lang_stdout / __lang_stderr — fixed-fd
	// handle constructors. Each wraps __lang_make_handle.
	for _, e := range []struct {
		sym string
		fd  int
	}{
		{"__lang_stdin", 0},
		{"__lang_stdout", 1},
		{"__lang_stderr", 2},
	} {
		g.line("")
		g.line(".global " + e.sym)
		g.typeDirective(e.sym)
		g.label(e.sym)
		g.emit("mov w0, #%d", e.fd)
		g.emit("b __lang_make_handle") // tail-call
		g.sizeDirective(e.sym)
	}

	// __lang_open_reader(path) / __lang_open_writer(path) /
	// __lang_open_appender(path) → Result[Reader|Writer, IoError].
	// Each is a thin wrapper around `openat` + handle alloc + the
	// Result-box build. Flags + mode differ per kind.
	twoWord := ast.UseTwoWordStrings(8)
	for _, e := range []struct {
		sym, name string
		flags     int
		mode      int
	}{
		{"__lang_open_reader", "open_reader", 0, 0},
		{"__lang_open_writer", "open_writer", 577, 0644},
		{"__lang_open_appender", "open_appender", 1089, 0644},
	} {
		_ = e.name
		g.line("")
		g.line(".global " + e.sym)
		g.typeDirective(e.sym)
		g.label(e.sym)
		if twoWord {
			// Two-word ABI: (path_data, path_len) in (x0, x1).
			// Frame: fp/lr (16) + 3 callee-saves (24 + 8 pad) +
			// 16-byte spill scratch + 16 align = 64.
			g.emit("stp x29, x30, [sp, #-64]!")
			g.emit("mov x29, sp")
			g.emit("stp x19, x20, [sp, #16]")
			g.emit("str x21, [sp, #32]")
			g.emit("mov x19, x0") // path_data
			g.emit("mov x20, x1") // path_len
			g.emitStrDataPtr2W("x21", "x19", "x20", 48) // x21 = byte ptr; scratch [x29+48]
			g.emit("mov x0, #-100")
			g.emit("mov x1, x21")
			g.emit("mov w2, #%d", e.flags)
			g.emit("mov w3, #%d", e.mode)
			g.syscall("openat")
			g.emit("tbnz x0, #63, %s", ".Lorw2w_err_"+e.sym)
			g.emit("mov w0, w0")
			g.emit("bl __lang_make_handle")
			g.emit("mov x21, x0") // handle ptr
			g.emit("mov x0, #16")
			g.emit("bl __lang_alloc")
			g.emit("str wzr, [x0]")
			g.emit("str x21, [x0, #8]")
			g.emit("b %s", ".Lorw2w_ret_"+e.sym)
			g.label(".Lorw2w_err_" + e.sym)
			g.emit("neg x21, x0") // x21 = errno
			g.emit("mov x0, x21")
			g.emit("mov x1, x19")
			g.emit("mov x2, x20")
			g.emit("bl __lang_io_error")
			g.emit("mov x21, x0")
			g.emit("mov x0, #16")
			g.emit("bl __lang_alloc")
			g.emit("mov w1, #1")
			g.emit("str w1, [x0]")
			g.emit("str x21, [x0, #8]")
			g.label(".Lorw2w_ret_" + e.sym)
			g.emit("ldr x21, [sp, #32]")
			g.emit("ldp x19, x20, [sp, #16]")
			g.emit("ldp x29, x30, [sp], #64")
			g.emit("ret")
			g.sizeDirective(e.sym)
			continue
		}
		g.emit("stp x29, x30, [sp, #-32]!")
		g.emit("mov x29, sp")
		g.emit("stp x19, x20, [sp, #16]")
		g.emit("mov x19, x0") // stash path
		g.emit("mov x0, #-100") // AT_FDCWD
		g.emit("mov x1, x19")
		g.emit("mov w2, #%d", e.flags)
		g.emit("mov w3, #%d", e.mode)
		g.syscall("openat")
		g.emit("tbnz x0, #63, %s", ".Lorw_err_"+e.sym)
		// Success: alloc handle struct, store fd, wrap in Ok.
		g.emit("mov w20, w0") // fd
		g.emit("mov w0, w20")
		g.emit("bl __lang_make_handle")
		g.emit("mov x19, x0") // handle ptr (in callee-save)
		g.emit("mov x0, #16")
		g.emit("bl __lang_alloc")
		g.emit("str wzr, [x0]")    // tag=0 (Ok)
		g.emit("str x19, [x0, #8]") // handle ptr
		g.emit("b %s", ".Lorw_ret_"+e.sym)
		g.label(".Lorw_err_" + e.sym)
		g.emit("neg x20, x0") // x20 = errno
		g.emit("mov x0, x20")
		g.emit("mov x1, x19") // path
		g.emit("bl __lang_io_error")
		g.emit("mov x19, x0") // stash IoError ptr (callee-save)
		g.emit("mov x0, #16")
		g.emit("bl __lang_alloc")
		g.emit("mov w1, #1")
		g.emit("str w1, [x0]")
		g.emit("str x19, [x0, #8]")
		g.label(".Lorw_ret_" + e.sym)
		g.emit("ldp x19, x20, [sp, #16]")
		g.emit("ldp x29, x30, [sp], #32")
		g.emit("ret")
		g.sizeDirective(e.sym)
	}

	// __lang_reader_read_line(reader_ptr) → Option[string].
	// Loads fd from [reader_ptr+0], reads byte-by-byte into
	// the shared `__lang_read_line_buf` until '\n' / 4 KiB /
	// EOF / error. Returns None on first-byte EOF, Some(line)
	// otherwise (line includes the trailing '\n' if seen).
	g.line("")
	g.line(".global __lang_reader_read_line")
	g.typeDirective("__lang_reader_read_line")
	g.label("__lang_reader_read_line")
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("ldr w22, [x0]") // fd
	g.adrpAdd("x19", "__lang_read_line_buf")
	g.emit("mov x20, #0") // bytes_read
	g.label(".Lrrl_loop")
	g.emit("cmp x20, #4096")
	g.emit("bge .Lrrl_done")
	g.emit("mov w0, w22")
	g.emit("add x1, x19, x20")
	g.emit("mov x2, #1")
	g.syscall("read")
	g.emit("cmp x0, #1")
	g.emit("blt .Lrrl_done")
	g.emit("add x21, x19, x20")
	g.emit("ldrb w21, [x21]")
	g.emit("add x20, x20, #1")
	g.emit("cmp w21, #10") // '\n'
	g.emit("beq .Lrrl_done")
	g.emit("b .Lrrl_loop")
	g.label(".Lrrl_done")
	g.emit("cbnz x20, .Lrrl_some")
	// None: tag=1 4-byte box.
	g.emit("mov x0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]")
	g.emit("b .Lrrl_ret")
	g.label(".Lrrl_some")
	if twoWord {
		// Heap-form alloc: just x20 bytes (no prefix / NUL).
		g.emit("mov x0, x20")
		g.emit("bl __lang_alloc")
		g.emit("mov x21, x0")
		g.emit("mov x0, x21")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __lang_memcpy")
		// Some(string) box: 24 bytes — {tag@0, data@8, len@16}.
		g.emit("mov x19, x21")
		g.emit("mov x0, #24")
		g.emit("bl __lang_alloc")
		g.emit("str wzr, [x0]")
		g.emit("str x19, [x0, #8]")
		g.emit("str x20, [x0, #16]")
		g.emit("b .Lrrl_ret")
	} else {
		g.emit("add x0, x20, #5")
		g.emit("bl __lang_alloc")
		g.emit("add x21, x0, #4")
		g.emit("stur w20, [x21, #-4]")
		g.emit("mov x0, x21")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __lang_memcpy")
		g.emit("add x0, x21, x20")
		g.emit("strb wzr, [x0]")
		g.emit("mov x19, x21")
		g.emit("mov x0, #16")
		g.emit("bl __lang_alloc")
		g.emit("str wzr, [x0]")
		g.emit("str x19, [x0, #8]")
	}
	g.label(".Lrrl_ret")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("__lang_reader_read_line")
	g.line(".ltorg")

	// __lang_reader_read_chunk(reader_ptr, n) →
	// Option[string]. Single read of up to n bytes; None if
	// the read returns 0 (EOF). Allocates the n-byte string
	// buffer first; if the read is short, the length prefix
	// records the actual byte count.
	g.line("")
	g.line(".global __lang_reader_read_chunk")
	g.typeDirective("__lang_reader_read_chunk")
	g.label("__lang_reader_read_chunk")
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("str x21, [sp, #32]")
	g.emit("ldr w19, [x0]") // fd
	g.emit("mov x20, x1")    // n
	if twoWord {
		// Two-word heap form: alloc exactly n bytes (no
		// prefix). Actual bytes read tracked in the Some
		// box's len field.
		g.emit("mov x0, x20")
		g.emit("bl __lang_alloc")
		g.emit("mov x21, x0")
		g.emit("mov w0, w19")
		g.emit("mov x1, x21")
		g.emit("mov x2, x20")
		g.syscall("read")
		g.emit("cmp x0, #0")
		g.emit("ble .Lrrc2w_none")
		g.emit("mov x20, x0") // x20 = bytes read
		// Some(string) 24-byte box.
		g.emit("mov x0, #24")
		g.emit("bl __lang_alloc")
		g.emit("str wzr, [x0]")
		g.emit("str x21, [x0, #8]")
		g.emit("str x20, [x0, #16]")
		g.emit("b .Lrrc2w_ret")
		g.label(".Lrrc2w_none")
		g.emit("mov x0, #4")
		g.emit("bl __lang_alloc")
		g.emit("mov w1, #1")
		g.emit("str w1, [x0]")
		g.label(".Lrrc2w_ret")
	} else {
		// alloc n + 4 (length prefix + bytes). Caller may receive
		// fewer bytes on a short read.
		g.emit("add x0, x20, #4")
		g.emit("bl __lang_alloc")
		g.emit("mov x21, x0")     // base
		g.emit("mov w0, w19")
		g.emit("add x1, x21, #4")
		g.emit("mov x2, x20")
		g.syscall("read")
		g.emit("cmp x0, #0")
		g.emit("ble .Lrrc_none")
		g.emit("stur w0, [x21, #4 - 4]")
		g.emit("mov x20, x0")
		g.emit("add x19, x21, #4")
		g.emit("add x0, x19, x20")
		g.emit("strb wzr, [x0]")
		g.emit("mov x0, #16")
		g.emit("bl __lang_alloc")
		g.emit("str wzr, [x0]")
		g.emit("str x19, [x0, #8]")
		g.emit("b .Lrrc_ret")
		g.label(".Lrrc_none")
		g.emit("mov x0, #4")
		g.emit("bl __lang_alloc")
		g.emit("mov w1, #1")
		g.emit("str w1, [x0]")
		g.label(".Lrrc_ret")
	}
	g.emit("ldr x21, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__lang_reader_read_chunk")

	// __lang_writer_write(writer_ptr, s_data_ptr) →
	// Option[IoError]. Writes the full string in a loop;
	// returns None on success or Some(IoError) if any write
	// errored.
	g.line("")
	g.line(".global __lang_writer_write")
	g.typeDirective("__lang_writer_write")
	g.label("__lang_writer_write")
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("ldr w19, [x0]") // fd
	if twoWord {
		// Two-word ABI: (writer_ptr, s_data, s_len) in
		// (x0, x1, x2). Extract byte length via emitStrLen2W,
		// materialise byte ptr via emitStrDataPtr2W.
		g.emitStrLen2W("w22", "x2")
		g.emitStrDataPtr2W("x20", "x1", "x2", 48) // x20 = byte ptr; scratch [x29+48]
	} else {
		g.emit("mov x20, x1")          // s data ptr
		g.emitStrLen("w22", "x20") // len
	}
	g.emit("mov x21, #0")          // bytes_written
	g.label(".Lww_loop")
	g.emit("cmp x21, x22")
	g.emit("bge .Lww_done")
	g.emit("mov w0, w19")
	g.emit("add x1, x20, x21")
	g.emit("sub x2, x22, x21")
	g.syscall("write")
	g.emit("tbnz x0, #63, .Lww_err")
	g.emit("add x21, x21, x0")
	g.emit("b .Lww_loop")
	g.label(".Lww_done")
	// None: 4-byte tag=1.
	g.emit("mov x0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]")
	g.emit("b .Lww_ret")
	g.label(".Lww_err")
	g.emit("neg x22, x0") // errno
	g.emit("mov x0, x22")
	// No path string for write errors; pass empty literal.
	// Two-word ABI: `(data=0, len=1<<63)` inline-empty pair.
	if ast.UseTwoWordStrings(8) {
		g.emit("mov x1, xzr")
		g.emit("movz x2, #0x8000, lsl #48")
	} else {
		g.adrpAdd("x1", ".LStr_ioerr_empty")
	}
	g.emit("bl __lang_io_error")
	g.emit("mov x19, x0")
	g.emit("mov x0, #16")
	g.emit("bl __lang_alloc")
	g.emit("str wzr, [x0]")
	g.emit("str x19, [x0, #8]")
	g.label(".Lww_ret")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("__lang_writer_write")

	// __lang_close_fd_box(handle_ptr) → Option[IoError].
	// Shared by Reader.close + Writer.close.
	g.line("")
	g.line(".global __lang_close_fd_box")
	g.typeDirective("__lang_close_fd_box")
	g.label("__lang_close_fd_box")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("ldr w0, [x0]") // fd
	g.syscall("close")
	g.emit("tbnz x0, #63, .Lcfb_err")
	// None.
	g.emit("mov x0, #4")
	g.emit("bl __lang_alloc")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]")
	g.emit("b .Lcfb_ret")
	g.label(".Lcfb_err")
	g.emit("neg x19, x0") // errno
	g.emit("mov x0, x19")
	if ast.UseTwoWordStrings(8) {
		g.emit("mov x1, xzr")
		g.emit("movz x2, #0x8000, lsl #48")
	} else {
		g.adrpAdd("x1", ".LStr_ioerr_empty")
	}
	g.emit("bl __lang_io_error")
	g.emit("mov x19, x0")
	g.emit("mov x0, #16")
	g.emit("bl __lang_alloc")
	g.emit("str wzr, [x0]")
	g.emit("str x19, [x0, #8]")
	g.label(".Lcfb_ret")
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__lang_close_fd_box")
	g.line(".ltorg")
}

// captureSlotSize mirrors closureconv.captureSlotSize for
// ptrW=8 (arm64). Wide scalars (i64 / f64) take 8 bytes;
// pointer-shaped captures take 8 bytes (the heap-pointer
// width); other scalars take 4. Sub-i32 captures round up
// to 4 for the same alignment reason the wasm + x86-64
// backends use.
func arm64CaptureSlotSize(t ast.Type, ptrW int) int32 {
	// Two-word strings: a string capture is `(data, len)` —
	// two 8-byte slots. Centralises the decision via
	// `ast.UseTwoWordStrings` so the arm64 native flip
	// (`docs/SSO-NATIVE-FLIP-STATUS.md`) picks it up.
	if _, isStr := t.(ast.StringType); isStr && ast.UseTwoWordStrings(ptrW) {
		return int32(2 * ptrW)
	}
	if ast.ElemSizeBytesFor(t, ptrW) == 8 {
		return 8
	}
	if ast.IsPointerType(t) {
		return int32(ptrW)
	}
	return 4
}

// emitMakeClosureOrEnv handles OpMakeClosure / OpMakeEnv:
// pops N captures off the operand stack (the last capture
// is on top), allocates the env block, stores each capture
// at its closureconv-computed offset, and pushes the env
// pointer (OpMakeEnv) or a freshly-built closure pair
// {fn_ptr, env_ptr} (OpMakeClosure).
//
// With the IR's Defunctionalise + ElideClosurePair passes
// run upstream, most closure-using programs reduce to
// OpMakeEnv — the closure-pair slot dies and only env_ptr
// survives. OpMakeClosure stays for cases that don't
// elide (closure-factory return through a local).
//
// Capture slot layout mirrors closureconv.captureSlotSize:
// wide scalars (i64 / f64) take 8 bytes; pointer-shaped
// captures take ptrW (=8 on arm64); other scalars take 4.
// Stores honour width — `str x1, ...` for 8-byte slots,
// `str w1, ...` for 4-byte slots.
func (g *generator) emitMakeClosureOrEnv(op ir.Op) error {
	envOnly := op.Kind == ir.OpMakeEnv
	hoisted, ok := g.funcs[op.Str]
	if !ok {
		return fmt.Errorf("arm64: closure target %q not in prog.Funcs", op.Str)
	}
	n := int(op.I32)
	if n != len(hoisted.Captures) {
		return fmt.Errorf("arm64: closure %q expects %d captures, got %d", op.Str, len(hoisted.Captures), n)
	}

	if n == 0 {
		// No captures: env_ptr = 0 placeholder. Hoisted
		// body never reads __env when Captures is empty.
		if envOnly {
			g.emit("mov x0, #0")
			g.push()
			return nil
		}
		// MakeClosure pair {fn_ptr, 0}. Still allocate the
		// 16-byte pair because callers may load both halves.
		g.emit("mov w0, #16")
		g.emit("bl __lang_alloc")
		g.adrpAdd("x1", op.Str)
		g.emit("str x1, [x0]")
		g.emit("str xzr, [x0, #8]")
		g.push()
		return nil
	}

	type slot struct {
		off  int32
		size int32
		typ  ast.Type
	}
	slots := make([]slot, n)
	envSize := int32(0)
	for i, cap := range hoisted.Captures {
		s := arm64CaptureSlotSize(cap.Type, 8)
		slots[i] = slot{off: envSize, size: s, typ: cap.Type}
		envSize += s
	}

	// Materialise envSize in w0, save x19 (callee-save)
	// across the alloc call so we can park env_ptr in it
	// while loading captures off the operand stack. Push
	// as a 16-byte aligned pair (x19, x20) — x20 isn't used
	// but the pre-decrement-of-16 maintains SP alignment.
	g.emit("mov w0, #%d", envSize)
	g.emit("stp x19, x20, [sp, #-16]!")
	g.emit("bl __lang_alloc")
	g.emit("mov x19, x0") // x19 = env_ptr
	// Captures sit on the operand stack just above the
	// pushed callee-saves. SP shifted down by 16 (the stp).
	// The Nth (last) capture is at [sp, #16]; the (N-1)th
	// at [sp, #16+slotBytes]; first at [sp, #16+slotBytes*(n-1)].
	const calleeSaveOff = 16 // stp x19, x20 above the operand stack
	// Total operand-stack slots consumed by all captures (sum
	// of per-capture stack-slot counts). String captures under
	// the two-word ABI occupy 2 operand-stack slots; others 1.
	totalStkSlots := int32(0)
	stkSlotCounts := make([]int32, n)
	for i, c := range hoisted.Captures {
		if _, isStr := c.Type.(ast.StringType); isStr && ast.UseTwoWordStrings(8) {
			stkSlotCounts[i] = 2
		} else {
			stkSlotCounts[i] = 1
		}
		totalStkSlots += stkSlotCounts[i]
	}
	// Capture i's TOP-OF-CAPTURE stack offset = calleeSaveOff +
	// (sum of slot counts of captures > i) * slotBytes. For a
	// non-string capture, the value lives at exactly that
	// offset. For a string capture, [top] holds `len` and
	// [top + slotBytes] holds `data` (data was pushed first,
	// then len on top).
	{
		offFromTop := int32(0)
		// Walk captures in reverse so we accumulate "above-i"
		// counts naturally.
		topOff := make([]int32, n)
		for i := n - 1; i >= 0; i-- {
			topOff[i] = calleeSaveOff + offFromTop*int32(slotBytes)
			offFromTop += stkSlotCounts[i]
		}
		for i, s := range slots {
			if stkSlotCounts[i] == 2 {
				// String capture: store data at env+s.off,
				// len at env+s.off+8.
				g.emit("ldr x1, [sp, #%d]", topOff[i])                     // len (top)
				g.emit("ldr x2, [sp, #%d]", topOff[i]+int32(slotBytes))    // data (below)
				if s.off == 0 {
					g.emit("str x2, [x19]")
				} else {
					g.emit("str x2, [x19, #%d]", s.off)
				}
				g.emit("str x1, [x19, #%d]", s.off+8)
				continue
			}
			g.emit("ldr x1, [sp, #%d]", topOff[i])
			if s.size == 8 {
				if s.off == 0 {
					g.emit("str x1, [x19]")
				} else {
					g.emit("str x1, [x19, #%d]", s.off)
				}
			} else {
				if s.off == 0 {
					g.emit("str w1, [x19]")
				} else {
					g.emit("str w1, [x19, #%d]", s.off)
				}
			}
		}
	}
	// Drop the N captures' operand-stack slots we consumed.
	g.emit("add sp, sp, #%d", totalStkSlots*int32(slotBytes))
	g.emit("mov x0, x19")
	if envOnly {
		g.emit("ldp x19, x20, [sp], #16")
		g.push()
		return nil
	}
	// OpMakeClosure: also allocate the 16-byte closure pair.
	// env_ptr is in x0 (and x19); we need to keep it alive
	// across the second __lang_alloc. x19 already preserved
	// (callee-save in the called function); x0 will be
	// clobbered. Reload from x19 after.
	g.emit("mov w0, #16")
	g.emit("bl __lang_alloc")
	g.adrpAdd("x1", op.Str)
	g.emit("str x1, [x0]")
	g.emit("str x19, [x0, #8]")
	g.emit("ldp x19, x20, [sp], #16")
	g.push()
	return nil
}

// internString returns a unique .rodata label for s, allocating
// a new one the first time we see this exact string and reusing
// it on repeats.
func (g *generator) internString(s string) string {
	if lbl, ok := g.stringLabel[s]; ok {
		return lbl
	}
	lbl := fmt.Sprintf(".LStr_%d", len(g.stringOrder))
	g.stringLabel[s] = lbl
	g.stringOrder = append(g.stringOrder, s)
	return lbl
}

// escapeForGAS escapes a string for the GAS `.asciz`
// directive. Only the minimum set of escapes the runtime
// strings need; the assembler's own escape map handles `\\` /
// `\n` / `\t` / `\"` etc.
func escapeForGAS(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\r':
			b.WriteString(`\r`)
		case c < 32 || c == 127:
			fmt.Fprintf(&b, `\%03o`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

type generator struct {
	out       strings.Builder
	info      *checker.Info
	indent    int
	labelN    int
	current   *ast.FuncDecl
	currentIR *ir.Func
	// slotOffsets[i] = byte offset (negative) from x29 of IR
	// slot i's LOWEST byte. Built by emitFunc per call —
	// string slots take 16 bytes (two-word ABI), others 8.
	slotOffsets []int32
	// darwin enables Mach-O / Apple-style conventions.
	// Currently affects: (1) the program entry point — Apple
	// links against `_main` rather than `_start`, so we emit
	// a `_main` wrapper that calls the user's `main`; (2) the
	// syscall ABI — macOS BSD uses x16 for the syscall number
	// and traps with `svc #0x80` instead of `svc #0`; (3) the
	// syscall numbers themselves (different table from Linux);
	// (4) `.note.GNU-stack` section directive is Linux-only.
	// Local symbols (user's `main`, runtime helpers like
	// `__lang_alloc`) stay with their Linux-style names —
	// they're internal references the assembler resolves
	// locally before the object format matters.
	darwin bool

	// stringLabel / stringOrder hold the string-pool scheme:
	// each unique string literal in the program gets a single
	// `.LStr_N` .rodata label with a 4-byte little-endian
	// length prefix followed by `.asciz` data. Programs that
	// reference the same literal multiple times share the
	// entry. Maintained in insertion order so the emitted
	// `.rodata` section is deterministic.
	stringLabel map[string]string
	stringOrder []string
	// funcs maps a top-level function name (including
	// closureconv-hoisted closures) to its AST declaration.
	// OpMakeClosure / OpMakeEnv read this to find the
	// hoisted function's Captures list — closureconv stamps
	// each capture's lang type, which drives env-block slot
	// layout (offsets, store widths).
	funcs map[string]*ast.FuncDecl

	// usesAlloc / usesStrcat / usesMemcpy track whether the
	// program reaches for the matching runtime helper. Each
	// helper is gated so programs that don't need it pay
	// nothing extra in binary size.
	usesAlloc   bool
	usesStrcat  bool
	usesMemcpy  bool
	usesStrcmp  bool
	// usesTcp pulls in the full TCP socket runtime
	// (__lang_tcp_listen / __lang_tcp_accept / __lang_tcp_recv
	// / __lang_tcp_send / __lang_tcp_close). Gated on call-
	// site reachability so non-server programs don't pay for
	// the socket boilerplate.
	usesTcp bool
	// usesStrSlice pulls in `__str_slice(base, low, high)` —
	// a length-prefix-aware substring extractor that
	// allocates a fresh string. The IR's `s[a:b]` slice
	// expression lowers to OpCallDirect{__str_slice}.
	usesStrSlice bool
	// usesSliceMake pulls in `__lang_slice_make(data, len)` —
	// allocates an 8-byte slice header { data_ptr, len }. Set
	// by recordUse() when the IR's slice-construction path
	// (a[lo:hi]) lowers to OpCallDirect{__slice_make}.
	usesSliceMake bool
	// usesEnv pulls in `__lang_env(name)` — walks envp for a
	// NAME=VALUE match. Used by the synthesised auto-main's
	// `__port_from_env("PORT", 8080)` call.
	usesEnv bool
	// usesAllocU8 + usesStringFromBytes gate the string-
	// handling prelude helpers that allocate length-prefixed
	// u8[] / string buffers.
	usesAllocU8         bool
	usesStringFromBytes bool
	// usesStrIdx tracks whether the program emits the SSO-aware
	// inlined `__str_idx` helper, which spills inline-tagged
	// strings to the `__lang_str_idx_scratch` .bss slot. Set
	// lazily on first emit in emitInlineIdxHelper; gates the
	// .bss reservation.
	usesStrIdx bool
	// enumSentinelTags collects unique tag values referenced by
	// payloadless-variant constructions. One .rodata symbol per
	// tag value gets reserved in emitDataSections.
	enumSentinelTags map[int]bool
	// constFuncCells tracks function names referenced via
	// OpConstFunc. Each gets a 16-byte static .rodata cell
	// `{fn_ptr, 0}` so OpCallIndirect can deref every callee
	// (top-level fn value or heap-allocated closure) through a
	// uniform pair shape. Mirrors the x86-64 + wasm closure
	// shape.
	constFuncCells map[string]bool
	// usesArrEmpty gates the `.LArr_Empty` sentinel — a shared
	// static 4-byte `[length=0]` buffer that __alloc_u8(0)
	// returns instead of allocating a fresh length-only block.
	// Mirrors the .LStr_Empty pattern from PR #299 for the
	// array seam.
	usesArrEmpty bool
	// usesRawIntPokes tracks whether the program calls
	// __load_i32 / __store_i32 — primitives the lang Map
	// runtime uses for its mixed bucket-index + entries
	// buffer. Single LDR / STR + ret each.
	usesRawIntPokes bool
	// usesMemset gates emission of the byte-grain
	// __memset(dst, byte, n) helper the Map clear path uses.
	usesMemset bool
	// usesPuts / usesWrite / usesPutchar pull in the stdout
	// builtins:
	//   print(s)   → __lang_puts    (string + newline, two write()s)
	//   write(s)   → __lang_write   (raw string, no newline)
	//   putchar(c) → __lang_putchar (1-byte write)
	// All routed through write(2) — fd 1, syscall numbers from
	// the linuxDarwinSysno map.
	usesPuts    bool
	usesWrite   bool
	usesPutchar bool
	// usesEprint pulls in `__lang_eprint(s)` — stderr counterpart
	// to print(). Two write(2)s to fd 2.
	usesEprint bool
	// usesExit pulls in `__lang_exit(code)` — direct exit syscall.
	// Doesn't return; the post-call push x0 the caller emits is
	// harmless because exit() never comes back.
	usesExit bool
	// usesArgs pulls in `__lang_args()` — materialises a fresh
	// length-prefixed `string[]` from the argc/argv stash the
	// `_start` / `_main` prologue captures off the kernel stack.
	// Result cached via `__lang_args_cache` so repeat calls are
	// O(1).
	usesArgs bool
	// usesArena pulls in `__lang_arena_save` / `__lang_arena_restore`
	// — bump-cursor snapshot/rewind helpers. Two leaf functions,
	// one ldr / str each. Cheap scope-bounded reclaim.
	usesArena bool
	// usesReadLine pulls in `__lang_read_line()` — stdin
	// one-byte reader. Returns Option[string]: Some(line)
	// when at least one byte was read (line preserves its
	// trailing newline), None when first read returned 0.
	// Sized at 4 KiB via a .bss buffer; longer lines are
	// truncated.
	usesReadLine bool
	// usesStdin pulls in a 4-byte `__lang_stdin()` stub that
	// returns 0. The checker requires `stdin()` to be a
	// callable; we don't model per-fd Readers, so the helper
	// just returns a sentinel.
	usesStdin bool
	// usesRandomBytes pulls in `__lang_random_bytes(n)` —
	// allocates an n-byte string and fills it with kernel
	// CSPRNG output via `getrandom(2)` on Linux or chunked
	// `getentropy(2)` on Darwin. Suitable for session IDs,
	// tokens, etc.
	usesRandomBytes bool
	// usesReadFile / usesWriteFile pull in the file-I/O
	// runtimes `__lang_read_file(path)` /
	// `__lang_write_file(path, content)`. Both return enum
	// boxes — see emitReadFileRuntime / emitWriteFileRuntime
	// for the IR-matching layout.
	usesReadFile  bool
	usesWriteFile bool
	// usesIoError pulls in `__lang_io_error(errno, path)` —
	// constructs an `IoError` enum box from a Linux errno.
	// Shared by read_file + write_file + the Reader / Writer
	// methods (close, write).
	usesIoError bool

	// usesReaderWriter pulls in the open_reader / open_writer
	// / open_appender entry points plus the Reader / Writer
	// method runtimes (read_line / read_chunk / close /
	// write). stdin / stdout / stderr also live behind this
	// flag since they now return real Reader / Writer struct
	// pointers (fd at +0) rather than scalar sentinels.
	usesReaderWriter bool
}

func (g *generator) line(s string) {
	g.out.WriteString(s)
	g.out.WriteByte('\n')
}

func (g *generator) emit(format string, args ...any) {
	g.out.WriteByte('\t')
	g.out.WriteString(fmt.Sprintf(format, args...))
	g.out.WriteByte('\n')
}

func (g *generator) label(name string) {
	g.out.WriteString(name)
	g.out.WriteString(":\n")
}

// fresh returns a unique numeric suffix for synthesised
// branch labels (`.Lret_main_3`, etc.). Per-function counter
// reset is handled by emitFunc's prologue.
func (g *generator) fresh() int {
	g.labelN++
	return g.labelN
}

// typeDirective + sizeDirective emit ELF-only `.type FUNC,
// %function` and `.size FUNC, .-FUNC` declarations. Mach-O
// rejects them — the format doesn't carry per-symbol size or
// type metadata (the linker derives both from section
// membership). On Darwin both methods are no-ops.
func (g *generator) typeDirective(name string) {
	if g.darwin {
		return
	}
	g.line(fmt.Sprintf(".type %s, %%function", name))
}

func (g *generator) sizeDirective(name string) {
	if g.darwin {
		return
	}
	g.line(fmt.Sprintf(".size %s, .-%s", name, name))
}

// syscallExit emits `exit_group(retval)` (Linux) / `exit(retval)`
// (Darwin). The exit syscall is what every fatal path in the
// runtime reaches for — OOM, bounds traps, the OS-handoff at
// the end of `_start` / `_main`. x0 already holds the exit
// value; the helper just sets the syscall register and traps.
func (g *generator) syscallExit() {
	if g.darwin {
		g.emit("mov x16, #%d", darExit)
		g.emit("svc #0x80")
	} else {
		g.emit("mov x8, #%d", sysExitGroup)
		g.emit("svc #0")
	}
}

// syscall emits the right `mov`/`svc` pair for `name` and
// normalises the error shape so that callers can rely on
// `x0 < 0 ⇔ error` regardless of target:
//
//   - Linux returns -errno in x0 on error; nothing to do.
//   - Darwin BSD returns +errno in x0 with the carry flag
//     set on error. We negate after the trap when C is set
//     so x0 ends up holding -errno just like Linux.
//
// Args must already be in x0..x5 per AAPCS64; the helper
// only touches x0 (on the Darwin error path) and x8/x16
// (syscall number).
func (g *generator) syscall(name string) {
	if nums, ok := linuxDarwinSysno[name]; ok {
		if g.darwin {
			g.emit("mov x16, #%d", nums[1])
			g.emit("svc #0x80")
			// Carry clear = success. Negate on error so callers'
			// `cmp x0, #0; blt` checks see Linux-shaped -errno.
			lbl := g.freshLabel("sysc_ok")
			g.emit("b.cc %s", lbl)
			g.emit("neg x0, x0")
			g.label(lbl)
		} else {
			g.emit("mov x8, #%d", nums[0])
			g.emit("svc #0")
		}
		return
	}
	if num, ok := linuxOnlySysno[name]; ok {
		if g.darwin {
			panic("arm64 syscall: " + name + " has no portable Darwin form; emitter must branch inline")
		}
		g.emit("mov x8, #%d", num)
		g.emit("svc #0")
		return
	}
	panic("arm64 syscall: unknown name " + name)
}

// adrpAdd emits the canonical AArch64 PC-relative
// symbol-address pair, paving over the GNU-vs-Apple
// relocation-syntax split:
//
//	ELF (Linux):
//	  adrp Xd, sym
//	  add  Xd, Xd, :lo12:sym
//
//	Mach-O (Darwin):
//	  adrp Xd, sym@PAGE
//	  add  Xd, Xd, sym@PAGEOFF
//
// Both pairs produce the same 64-bit symbol address. Apple's
// integrated assembler rejects the `:lo12:` form; GNU as
// rejects `@PAGE` / `@PAGEOFF`.
func (g *generator) adrpAdd(reg, sym string) {
	if g.darwin {
		g.emit("adrp %s, %s@PAGE", reg, sym)
		g.emit("add %s, %s, %s@PAGEOFF", reg, reg, sym)
	} else {
		g.emit("adrp %s, %s", reg, sym)
		g.emit("add %s, %s, :lo12:%s", reg, reg, sym)
	}
}

// emitCallArgsLoad places `argc` operand-stack values into the
// AAPCS64 argument slots. The first `regArgs` (=8) go in
// x0..x7; the remaining args land on the call stack at
// [sp+0], [sp+8], ... in source order. The operand stack uses
// `slotBytes`-byte slots; the call stack always uses 8-byte
// slots, so overflow args get copied to a packed call-stack
// overflow area allocated below the operand stack.
//
// After this call returns the caller is responsible for
// issuing the `bl` / `blr`, then calling emitCallArgsCleanup
// to drop both the call-stack overflow AND the operand-stack
// arg slots.
func (g *generator) emitCallArgsLoad(argc int) {
	if argc <= regArgs {
		for i := argc - 1; i >= 0; i-- {
			g.emit("ldr x%d, [sp], #%d", i, slotBytes)
		}
		return
	}
	overflow := argc - regArgs
	// Round overflow * 8 up to a multiple of 16 to keep sp
	// 16-aligned across the call.
	stackSize := ((overflow*8 + 15) / 16) * 16
	g.emit("sub sp, sp, #%d", stackSize)
	// Read register args (0..regArgs-1) from operand stack into
	// x0..x_{regArgs-1}. Args sit at [sp + stackSize +
	// slotBytes*(argc-1-i)] (operand-stack top after the sub
	// is at sp + stackSize; arg i is at offset slotBytes*
	// (argc-1-i) from the top).
	for i := 0; i < regArgs; i++ {
		g.emit("ldr x%d, [sp, #%d]", i, stackSize+slotBytes*(argc-1-i))
	}
	// Copy overflow args (regArgs..argc-1) from operand stack
	// to the packed call-stack overflow area.
	for i := regArgs; i < argc; i++ {
		g.emit("ldr x9, [sp, #%d]", stackSize+slotBytes*(argc-1-i))
		g.emit("str x9, [sp, #%d]", 8*(i-regArgs))
	}
}

// emitCallArgsCleanup undoes emitCallArgsLoad's stack
// allocation. Caller passes the same argc.
func (g *generator) emitCallArgsCleanup(argc int) {
	if argc <= regArgs {
		// Args were already popped via post-increment ldrs.
		return
	}
	overflow := argc - regArgs
	stackSize := ((overflow*8 + 15) / 16) * 16
	// Drop call-stack overflow AND the operand-stack args.
	g.emit("add sp, sp, #%d", stackSize+slotBytes*argc)
}

// emitStartRuntime writes `_start`, the binary's entry under
// `-nostdlib` on Linux arm64. The kernel hands us a 16-aligned
// sp at process entry; we trust it (no explicit re-alignment
// — `and sp, sp, #imm` is rejected by the assembler since
// AArch64 logical-immediate AND can't take SP as a destination).
// args() / env() aren't wired yet, so the prologue is minimal:
// branch to main, then exit_group with main's return value.
func (g *generator) emitStartRuntime() {
	g.line("")
	// Entry symbol differs by platform. Linux ELF: `_start`
	// (the kernel jumps here directly). Mach-O on Darwin:
	// `_main` — the Apple linker (ld64 / lld's Mach-O backend)
	// defaults to that as the entry. We don't link against
	// libSystem's `start` stub, so we play the role of the
	// crt: capture argv / envp from the kernel-delivered
	// stack, branch to the user's `main`, exit_group.
	entry := "_start"
	if g.darwin {
		entry = "_main"
	}
	g.line(fmt.Sprintf(".global %s", entry))
	if !g.darwin {
		// `.type x, %function` is GAS-specific (ELF). Mach-O
		// assemblers reject it; the Mach-O object format
		// records function-vs-data via the section, not via
		// the symbol's `.type`.
		g.typeDirective(entry)
	}
	g.label(entry)
	if g.usesEnv || g.usesArgs {
		// Capture argc / argv (and derive envp on Linux) from
		// the platform's process-entry convention. The two
		// targets disagree:
		//
		//   Linux: kernel jumps to `_start` with sp pointing
		//   at [argc, argv[0..], NULL, envp[0..], NULL, auxv].
		//   We read argc from sp[0], argv = &sp[1], and walk
		//   past argv + NULL to find envp.
		//
		//   Darwin: with the default LC_MAIN load command, dyld
		//   calls `_main(argc, argv, envp, apple)` per AAPCS64
		//   — argc in x0, argv in x1, envp in x2. The stack
		//   doesn't carry the SVR4-shaped argc-then-argv layout
		//   at all here.
		if g.darwin {
			// x0 / x1 / x2 already hold argc / argv / envp.
			if g.usesEnv {
				g.adrpAdd("x3", "__lang_envp")
				g.emit("str x2, [x3]")
			}
			if g.usesArgs {
				g.adrpAdd("x3", "__lang_argc")
				g.emit("str x0, [x3]")
				g.adrpAdd("x3", "__lang_argv")
				g.emit("str x1, [x3]")
			}
		} else {
			g.emit("ldr x0, [sp]")            // argc
			g.emit("add x1, sp, #8")          // argv = &sp[1]
			if g.usesEnv {
				g.emit("add x2, x0, #1")          // argc + 1
				g.emit("add x2, x1, x2, lsl #3")  // envp = argv + (argc+1)*8
				g.adrpAdd("x3", "__lang_envp")
				g.emit("str x2, [x3]")
			}
			if g.usesArgs {
				g.adrpAdd("x3", "__lang_argc")
				g.emit("str x0, [x3]")
				g.adrpAdd("x3", "__lang_argv")
				g.emit("str x1, [x3]")
			}
		}
	}
	g.emit("bl main")
	g.syscallExit()
	if !g.darwin {
		g.sizeDirective(entry)
	}
}

// emitFunc lowers one IR function. Stack frame layout
// (canonical AAPCS64 leaf-function shape):
//
//	[caller's sp]                     ← old sp (before prologue)
//	[caller's sp - 8]   saved x30 (lr)
//	[caller's sp - 16]  saved x29 (fp), x29 points HERE after `mov`
//	[caller's sp - 16 - 8*1]   slot 0
//	[caller's sp - 16 - 8*2]   slot 1
//	...
//	[caller's sp - frameSize]  ← new sp (locals + alignment pad)
//
// Slot 0..N-1 indexes match the IR's local indexing: param i
// occupies slot i (parameters arrive in x0..x7 and are stored
// to their slots at function entry); locals beyond the param
// count occupy slots N+0, N+1, ... All slots are 8 bytes
// (i32 values waste 4 bytes per slot — future PR can pack
// i32s back-to-back).
//
// Stack-relative addressing uses sp (locals at `[sp, #i*8]`)
// rather than fp because sp doesn't move during a function
// body (the operand-stack pushes / pops happen on a separate
// scratch region above sp via `[sp, #-16]!` pre-decrement —
// no wait, that does move sp). Reconciling both: locals are
// at fixed offsets from x29 (the frame pointer), which the
// prologue pinned to the saved-pair address; that stays
// stable across operand-stack pushes/pops since we never
// touch x29 between prologue and epilogue.
//
// We reserve a fixed slot count per function — the IR doesn't
// expose a "max locals" field directly, so we walk irFn.Ops
// and find the highest-numbered slot referenced.
func (g *generator) emitFunc(fn *ast.FuncDecl, irFn *ir.Func) error {
	g.current = fn
	g.currentIR = irFn
	defer func() { g.current = nil; g.currentIR = nil; g.slotOffsets = nil }()

	maxSlot := int32(-1)
	for _, op := range irFn.Ops {
		switch op.Kind {
		case ir.OpLoadLocal, ir.OpStoreLocal, ir.OpTeeLocal:
			if op.I32 > maxSlot {
				maxSlot = op.I32
			}
		}
	}
	for i := range fn.Params {
		if int32(i) > maxSlot {
			maxSlot = int32(i)
		}
	}
	numSlots := int(maxSlot + 1)
	// Build the slot-offset table. Each slot is 8 bytes by
	// default; string slots take 16 bytes (data + len) under
	// the two-word ABI (`docs/SSO-NATIVE-FLIP-STATUS.md`). The
	// table maps IR slot index → byte offset (negative) from
	// x29, pointing at the LOWEST byte of the slot. For a
	// string slot, `data` lives at `slotOffsets[i] + 8` and
	// `len` at `slotOffsets[i]` — natural for `stp data, len,
	// [x29, slotOffsets[i]]`.
	g.slotOffsets = make([]int32, numSlots)
	cumLocals := int32(0)
	for i := 0; i < numSlots; i++ {
		sz := int32(8)
		if g.slotIsString(int32(i)) {
			sz = 16
		}
		cumLocals += sz
		g.slotOffsets[i] = -cumLocals
	}
	localsSize := int(cumLocals)
	if localsSize%16 != 0 {
		localsSize += 8
	}
	frameSize := 16 + localsSize

	g.line("")
	g.line(fmt.Sprintf(".global %s", fn.Name))
	g.typeDirective(fn.Name)
	g.label(fn.Name)
	// Prologue:
	//   stp x29, x30, [sp, #-16]!  ; save fp/lr, sp -= 16
	//   mov x29, sp                ; fp = sp (points at saved pair)
	//   sub sp, sp, #localsSize    ; allocate locals below the pair
	// Slot i lives at [x29, #-(i+1)*8] OR equivalently [sp,
	// #localsSize - (i+1)*8]. We use the [sp, #N] form so all
	// local accesses are positive offsets — easier to reason
	// about and avoids the negative-offset encoding edge cases.
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	if localsSize > 0 {
		g.emit("sub sp, sp, #%d", localsSize)
	}
	// Spill parameter registers x0..x{regArgs-1} into their
	// slots. Args at index >= regArgs come from the caller's
	// stack at [x29 + 16 + 8*(stackIdx)] — `+16` skips the
	// saved fp/lr pair the prologue stored, and the caller's
	// stack-arg area starts just above. Slot i lives at
	// `g.slotOffsets[i]` (negative offset from x29). Under
	// the two-word ABI a string param takes 2 registers
	// (data, len) → stored at slotOffsets[i]+8 and +0
	// respectively, matching the `stp data, len` shape.
	regIdx := 0
	stackIdx := 0
	for i, p := range fn.Params {
		off := g.slotOffsets[i]
		if _, isStr := p.Type.(ast.StringType); isStr && ast.UseTwoWordStrings(8) {
			// data half: from register regIdx, stored at off+8
			if regIdx < regArgs {
				g.emit("stur x%d, [x29, #%d]", regIdx, off+8)
			} else {
				g.emit("ldr x9, [x29, #%d]", 16+8*stackIdx)
				g.emit("stur x9, [x29, #%d]", off+8)
				stackIdx++
			}
			regIdx++
			// len half: from register regIdx, stored at off
			if regIdx < regArgs {
				g.emit("stur x%d, [x29, #%d]", regIdx, off)
			} else {
				g.emit("ldr x9, [x29, #%d]", 16+8*stackIdx)
				g.emit("stur x9, [x29, #%d]", off)
				stackIdx++
			}
			regIdx++
			continue
		}
		if regIdx < regArgs {
			g.emit("stur x%d, [x29, #%d]", regIdx, off)
		} else {
			g.emit("ldr x9, [x29, #%d]", 16+8*stackIdx)
			g.emit("stur x9, [x29, #%d]", off)
			stackIdx++
		}
		regIdx++
	}
	_ = frameSize // reserved for the eventual debug-info / unwind tables

	retLabel := fmt.Sprintf(".Lret_%s_%d", fn.Name, g.fresh())
	var scope []irScope
	for _, op := range irFn.Ops {
		if err := g.emitOp(op, frameSize, retLabel, &scope); err != nil {
			return err
		}
	}

	// Epilogue. OpReturn already emits `b retLabel`; if the
	// function falls off the end without an explicit return
	// (e.g. void functions), we land here naturally.
	//
	// `mov sp, x29` restores sp to the saved-pair address
	// regardless of how the operand stack ended up — robust
	// to void-call leaks where OpCallDirect always pushes
	// x0 even when the function returns nothing. Without
	// the fp-based unwind, leaked operand-stack pushes leave
	// sp below where the prologue put it, and the `ldp`
	// loads garbage as fp/lr → ret to a bad address → SEGV.
	g.label(retLabel)
	g.emit("mov sp, x29")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.sizeDirective(fn.Name)
	g.line(".ltorg")
	return nil
}

// push x0 — store r0 to the top of the operand stack and bump
// slotBytes is the operand-stack slot size. 16 bytes today
// (one x-register value + 8 bytes padding); the padding kept
// sp 16-byte aligned across every push/pop without a runtime
// parity check, which AAPCS64 requires at every `bl`.
// BACKEND-PARITY perf item #3 plans to halve this to 8; the
// constant centralises the value so the flip is a one-line
// change.
const slotBytes = 16

// push x0 onto the operand stack — `slotBytes`-byte slot,
// value at `[sp]`. The upper bytes are dead today (slotBytes
// == 16, value fits in 8); step 2 of the packed-operand-
// stack plan drops them.
func (g *generator) push() {
	g.emit("str x0, [sp, #-%d]!", slotBytes)
}

// pop into x0.
func (g *generator) pop() {
	g.emit("ldr x0, [sp], #%d", slotBytes)
}

// frameLoad emits a load of `reg` from `[x29 + off]`, picking
// the right addressing mode for the offset. `stur` / `ldur`
// (unscaled, signed 9-bit imm) cover -256..+255 — fine for
// shallow frames. Larger negative offsets exceed the imm
// range and assemble-fail, so we materialise the address via
// `sub x16, x29, #abs(off)` (x16 is the AArch64 intra-
// procedure-call scratch — IP0 — free to clobber across
// function calls and never carries a live value across the
// store / load). Positive offsets follow the same pattern
// with `add`.
//
// Frames over 4095 bytes would need a multi-instruction
// `add` sequence; we don't emit those today and panic if
// asked, leaving room for a real implementation when the
// first user trips it. Practical frames sit well under that
// — the largest in the current test suite is ~600 bytes.
func (g *generator) frameLoad(reg string, off int32) {
	if off >= -256 && off <= 255 {
		g.emit("ldur %s, [x29, #%d]", reg, off)
		return
	}
	abs := off
	if abs < 0 {
		abs = -abs
	}
	if abs > 4095 {
		panic(fmt.Sprintf("arm64: frame offset %d exceeds 12-bit add/sub imm range; multi-step materialisation not implemented", off))
	}
	if off < 0 {
		g.emit("sub x16, x29, #%d", -off)
	} else {
		g.emit("add x16, x29, #%d", off)
	}
	g.emit("ldr %s, [x16]", reg)
}

// frameStore is frameLoad's store counterpart. Same offset
// range + scratch-register handling.
func (g *generator) frameStore(reg string, off int32) {
	if off >= -256 && off <= 255 {
		g.emit("stur %s, [x29, #%d]", reg, off)
		return
	}
	abs := off
	if abs < 0 {
		abs = -abs
	}
	if abs > 4095 {
		panic(fmt.Sprintf("arm64: frame offset %d exceeds 12-bit add/sub imm range; multi-step materialisation not implemented", off))
	}
	if off < 0 {
		g.emit("sub x16, x29, #%d", -off)
	} else {
		g.emit("add x16, x29, #%d", off)
	}
	g.emit("str %s, [x16]", reg)
}

// slotType returns the lang-level type of IR slot `idx` for
// the currently emitting function. Mirrors wasm's slotType:
// params first, then user locals, then synthetic scratch
// slots (whose types live on `irFn.ScratchTypes`). Returns
// nil when idx is past the scratch range — caller treats as
// "unknown" (defaults to single-slot emit).
func (g *generator) slotType(idx int32) ast.Type {
	if g.current == nil || g.currentIR == nil {
		return nil
	}
	fn := g.current
	irFn := g.currentIR
	if int(idx) < len(fn.Params) {
		return fn.Params[idx].Type
	}
	idx -= int32(len(fn.Params))
	if int(idx) < len(irFn.Locals) {
		return irFn.Locals[idx].Type
	}
	idx -= int32(len(irFn.Locals))
	if int(idx) < len(irFn.ScratchTypes) {
		return irFn.ScratchTypes[idx]
	}
	return nil
}

// slotIsString reports whether IR slot `idx` names a string-
// typed value in the current function. Dead today; future
// commits in the arm64 flip arc use it to fan-out
// OpLoadLocal / OpStoreLocal / OpTeeLocal for string slots.
func (g *generator) slotIsString(idx int32) bool {
	_, ok := g.slotType(idx).(ast.StringType)
	return ok
}

// callArgTypes returns the parameter types of an OpCallDirect /
// OpCallDirectPair, preferring the IR-stamped `op.ArgTypes`
// (populated by the lowering pass from FuncSigs at the central
// emit point and explicitly at synthesised emit sites like
// `__str_slice`). Falls back to looking up the user FuncDecl
// in g.funcs by name for IR ops the lowering pass left
// ArgTypes-empty — that's the path for callees materialised
// later in the pipeline (monomorphisation, inliner clones)
// where the central-emit ArgTypes plumbing hasn't run.
//
// Returns nil when neither source has it — the backend's nil
// path treats every arg as 1 operand-stack slot, which is
// correct for callees with no string args.
func callArgTypes(g *generator, op ir.Op, argc int) []ast.Type {
	if len(op.ArgTypes) > 0 {
		return op.ArgTypes
	}
	if callee, ok := g.funcs[op.Str]; ok && callee != nil {
		out := make([]ast.Type, 0, argc)
		for i := 0; i < argc && i < len(callee.Params); i++ {
			out = append(out, callee.Params[i].Type)
		}
		return out
	}
	return nil
}

// returnIsString reports whether the callee named `name`
// returns a string-typed value under the two-word ABI —
// matters for OpCallDirect's post-call push (data, len)
// instead of single-i32.
func returnIsString(g *generator, name string) bool {
	if callee, ok := g.funcs[name]; ok && callee != nil {
		_, isStr := callee.ReturnType.(ast.StringType)
		return isStr
	}
	switch name {
	case "random_bytes", "tcp_recv", "string_from_bytes", "__str_slice":
		// Built-in runtime helpers that return string directly.
		// NOT in this list: `env` / `read_file` / `read_line` /
		// `__method_Reader_read_line` / etc — those return
		// `Option[string]` or `Result[string, IoError]`
		// (enum heap-box ptrs, a single i32-sized return value).
		return true
	}
	return false
}

// returnIsVoid reports whether a function returns void — i.e.
// the OpCallDirect emit should NOT push x0 onto the operand
// stack after the `bl` instruction. Looks up user functions in
// g.funcs and built-in helpers via g.info.FuncSigs (where the
// checker records void returns for `__memcpy` / `__memset` /
// the `__store_*` / `__load_*` raw-memory pokes).
//
// Without this gate, void-returning helpers leave a phantom x0
// value on the operand stack — the runtime helper's epilogue
// sets x0 even though no result was intended, and an
// unconditional push corrupts subsequent OpStore / OpStoreLocal
// pops. Hit in practice by `arr.push(v)` inside a struct
// literal field initialiser: the inner `__memcpy` left a
// phantom slot that the outer struct-lit's OpStore consumed
// instead of the field address.
func returnIsVoid(g *generator, name string) bool {
	if callee, ok := g.funcs[name]; ok && callee != nil {
		_, isVoid := callee.ReturnType.(ast.VoidType)
		return isVoid
	}
	if g.info != nil {
		if sig, ok := g.info.FuncSigs[name]; ok && sig != nil {
			_, isVoid := sig.Result.(ast.VoidType)
			return isVoid
		}
	}
	return false
}

// regW maps a 64-bit register name to its 32-bit counterpart.
// `x0` → `w0`; `xzr` → `wzr`. Used by emitStrLen for the
// `ubfx wD, wS, #1, #3` length-extraction operand-size match
// (AArch64 requires both source and dest of a bit-field op to
// be the same width).
func regW(rX string) string {
	switch rX {
	case "xzr":
		return "wzr"
	}
	if len(rX) >= 2 && rX[0] == 'x' {
		return "w" + rX[1:]
	}
	return rX
}

// ssoTagBit is bit 0 of the LSB byte of an inline-tagged string
// value: 1 means inline, 0 means heap pointer. __lang_alloc returns
// 8-aligned pointers so heap-form values always have LSB clear.
const ssoTagBit = 0x01

// Inline string layout (8-byte little-endian register value):
//
//	byte 0:  (length << 1) | 1   — tag bit (bit 0) + length (bits 1..3, range 1..7)
//	bytes 1..7: up to 7 bytes of string data
//
// Length 0 keeps the .LStr_Empty heap sentinel — the inline
// "byte 0 == 1" encoding could also represent it, but the
// sentinel was already on a hot path and the round-trip is
// identical. Mirrors the x86_64 encoding (PR #300); same layout,
// different ISA.

// emitStrLen loads the i32 length of the string whose data pointer
// lives in srcX into the 32-bit register dstW. Branches on the LSB
// tag bit to handle both heap form (4-byte little-endian prefix at
// `[srcX - 4]`) and inline form (length stored in bits 1..3 of the
// value). Centralised READ-side seam of the SSO encoding family.
func (g *generator) emitStrLen(dstW, srcX string) {
	id := g.labelN
	g.labelN++
	inlineLbl := fmt.Sprintf(".Lstrlen_inline_%d", id)
	doneLbl := fmt.Sprintf(".Lstrlen_done_%d", id)
	g.emit("tbnz %s, #0, %s", srcX, inlineLbl)
	g.emit("ldur %s, [%s, #-4]", dstW, srcX)
	g.emit("b %s", doneLbl)
	g.label(inlineLbl)
	// Extract length from bits 1..3 of the low byte.
	g.emit("ubfx %s, %s, #1, #3", dstW, regW(srcX))
	g.label(doneLbl)
}

// emitStrLenStore writes the i32 length in srcW to the 4-byte
// little-endian length prefix at `[dstX - 4]`, where dstX is the
// new string's *data pointer* (one past the prefix). Inverse of
// emitStrLen and the second half of the SSO encoding seam:
// strcat / str_slice / string_from_bytes / random_bytes / env /
// read_file / tcp_recv / Reader.read_chunk all materialise a
// fresh string and write its length through this one site, so
// future encoding changes that affect string construction (e.g.
// tagged-pointer inline-when-short) have a single function to
// update per backend. Array-length stores (in `__alloc_u8`,
// `__lang_args`) stay open-coded since arrays may diverge.
func (g *generator) emitStrLenStore(srcW, dstX string) {
	g.emit("stur %s, [%s, #-4]", srcW, dstX)
}

// emitStrDataPtr produces a usable byte pointer to the string's
// data in dstX. For heap-form inputs (LSB=0), the input IS the
// data pointer, so this is just a `mov dstX, srcX`. For inline
// inputs (LSB=1), the data bytes live in the register itself;
// we spill srcX's 8 bytes to the caller-provided `scratchOff`
// (a positive byte offset from x29) and return `x29 + scratchOff
// + 1` (skipping the leading length-and-tag byte). The scratch
// buffer must outlive any byte read through dstX — caller
// reserves the slot in its frame and the dstX pointer is dead
// by function return. AArch64 uses positive offsets from x29
// for frame locals (unlike x86_64's negative-from-rbp). Mirrors
// x86_64's emitStrDataPtr semantics.
func (g *generator) emitStrDataPtr(dstX, srcX string, scratchOff int) {
	id := g.labelN
	g.labelN++
	inlineLbl := fmt.Sprintf(".Lstrdata_inline_%d", id)
	doneLbl := fmt.Sprintf(".Lstrdata_done_%d", id)
	g.emit("tbnz %s, #0, %s", srcX, inlineLbl)
	if dstX != srcX {
		g.emit("mov %s, %s", dstX, srcX)
	}
	g.emit("b %s", doneLbl)
	g.label(inlineLbl)
	g.emit("str %s, [x29, #%d]", srcX, scratchOff)
	g.emit("add %s, x29, #%d", dstX, scratchOff+1)
	g.label(doneLbl)
}

// emitStrEmpty materialises the data pointer of the canonical
// empty-string sentinel into dstX. The sentinel lives in .rodata
// as a length-prefixed string with length=0, shared across all
// callers and the entire program lifetime. Used by the string-
// constructing runtime helpers (strcat / str_slice /
// string_from_bytes) to short-circuit the alloc + memcpy + length-
// store sequence when the result is zero bytes — the helpers
// already round-trip through emitStrLenStore / emitStrLen, so the
// returned pointer is indistinguishable from a freshly allocated
// 0-length string. Third member of the SSO helper family.
func (g *generator) emitStrEmpty(dstX string) {
	g.adrpAdd(dstX, ".LStr_Empty")
}

// emitStrLen2W is the two-word-ABI counterpart of emitStrLen
// — reads the byte length from a `len` register `lenX` under
// the (data, len) operand-stack convention. Flag-aware:
//
//   - heap form (bit 63 of lenX clear): `lenX`'s low 32 bits
//     hold the byte length verbatim. `mov dstW, wN` puts them
//     in dstW.
//   - inline form (bit 63 set): `lenX` bits 56..59 hold the
//     length nibble (0..15, matching the 15-byte cap for
//     wasm32-incompatible inline strings on natives). `ubfx`
//     extracts the nibble.
//
// Matches `langstring.LengthNative` exactly. Dead today (no
// IR site emits two-word strings for natives yet); will become
// the live helper when the arm64 flip activates.
//
// The "2W" suffix marks this as the two-word variant; the
// legacy single-register `emitStrLen` lives alongside until
// every caller migrates over.
func (g *generator) emitStrLen2W(dstW, lenX string) {
	id := g.labelN
	g.labelN++
	inlineLbl := fmt.Sprintf(".Lstrlen2w_inline_%d", id)
	doneLbl := fmt.Sprintf(".Lstrlen2w_done_%d", id)
	g.emit("tbnz %s, #63, %s", lenX, inlineLbl)
	// Heap form: low 32 bits of lenX are the byte length.
	g.emit("mov %s, %s", dstW, regW(lenX))
	g.emit("b %s", doneLbl)
	g.label(inlineLbl)
	// Inline form: length nibble at bits 56..59 of lenX. Result
	// fits in 4 bits → 32-bit reg holds it; use w-form ubfx via
	// the x-source-with-w-dst encoding (`ubfx wD, xN<31:0>, ...`
	// is invalid — the source must match destination width).
	// Compute in xtmp first, then alias as wD.
	g.emit("ubfx %s, %s, #56, #4", lenX, lenX) // overwrites lenX with the nibble
	g.emit("mov %s, %s", dstW, regW(lenX))
	g.label(doneLbl)
}

// emitStrDataPtr2W is the two-word-ABI counterpart of
// emitStrDataPtr. Takes a `(dataX, lenX)` pair and produces a
// linear-memory pointer to the bytes in `dstX`. Flag-aware:
//
//   - heap form (bit 63 of lenX clear): `dataX` IS the byte
//     pointer; copy it into dstX.
//   - inline form (bit 63 set): spill the 16 inline-form
//     bytes (`dataX` for bytes 0..7, `lenX` for bytes 8..14
//     with the length nibble in bits 56..59) to a caller-
//     reserved 16-byte scratch slot at `[x29 + scratchOff]`
//     and set dstX = x29 + scratchOff.
//
// `scratchOff` must point at a 16-byte caller-reserved slot;
// 16 vs 8 is the only layout difference from the single-
// register `emitStrDataPtr`.
//
// Dead today; live after the arm64 flip.
func (g *generator) emitStrDataPtr2W(dstX, dataX, lenX string, scratchOff int) {
	id := g.labelN
	g.labelN++
	inlineLbl := fmt.Sprintf(".Lstrdata2w_inline_%d", id)
	doneLbl := fmt.Sprintf(".Lstrdata2w_done_%d", id)
	g.emit("tbnz %s, #63, %s", lenX, inlineLbl)
	// Heap form: data pointer is dataX.
	if dstX != dataX {
		g.emit("mov %s, %s", dstX, dataX)
	}
	g.emit("b %s", doneLbl)
	g.label(inlineLbl)
	// Inline form: spill (dataX, lenX) to mem[x29 + scratchOff
	// .. x29 + scratchOff + 15] and return that address. The
	// length-nibble in lenX bits 56..59 sits past the inline
	// payload, but writing the full 16 bytes is harmless —
	// callers only read the first `length` bytes via the
	// bounds-checked `__str_idx` etc.
	g.emit("str %s, [x29, #%d]", dataX, scratchOff)
	g.emit("str %s, [x29, #%d]", lenX, scratchOff+8)
	g.emit("add %s, x29, #%d", dstX, scratchOff)
	g.label(doneLbl)
}

// emitStrEmpty2W is the two-word-ABI counterpart of
// emitStrEmpty. Sets `(dataX, lenX)` to the canonical empty-
// string pair: dataX = 0 (no inline bytes), lenX = inline-flag
// bit only (length 0, no inline bytes). This is the
// same representation `langstring.PackInlineNative([])`
// produces and matches the wasm32 empty-string pair
// (with bit 63 instead of bit 31).
//
// Dead today; live after the arm64 flip.
func (g *generator) emitStrEmpty2W(dataX, lenX string) {
	g.emit("mov %s, xzr", dataX)
	g.emit("movz %s, #0x8000, lsl #48", lenX)
}

// emitArrayLen loads the i32 length of the length-prefixed array
// whose data pointer lives in srcX into dstW. Today this is a
// 4-byte little-endian load from `[srcX - 4]`. Centralised seam
// for arrays: parallels emitStrLen but stays distinct because
// arrays may diverge from strings under future layout changes
// (inline u8[], typed-element headers, etc.). Used by
// __alloc_u8's siblings, the __arr_idx bounds checks (where
// they exist), and string_from_bytes's input length read.
func (g *generator) emitArrayLen(dstW, srcX string) {
	g.emit("ldur %s, [%s, #-4]", dstW, srcX)
}

// emitArrayLenStore writes the i32 length in srcW to the 4-byte
// little-endian length prefix at `[dstX - 4]`, where dstX is the
// new array's *data pointer* (one past the prefix). Inverse of
// emitArrayLen. Used by __alloc_u8 and __lang_args (outer
// string[] container). String length stores stay on
// emitStrLenStore.
func (g *generator) emitArrayLenStore(srcW, dstX string) {
	g.emit("stur %s, [%s, #-4]", srcW, dstX)
}

// binPop — pop two values off the operand stack into x1 (lhs)
// and x0 (rhs). Produces the natural form for non-commutative
// ops where the lhs ends up in the second source register.
// emitPairFormMaker is the shared lowering for
// OpMakeSomeI32 / OpMakeOkI32 / OpMakeErrI32 on arm64. `width`
// is the IR's `Op.Width` operand (`0` for i32 payload,
// `ir.WidthPtr` for pointer-shape payload); `tag` is the
// variant index (`0` for Some/Ok, `1` for Err). Multi-value
// AAPCS64 return ABI: leave (tag, payload) as two operand-
// stack slots so OpReturnPair / OpCallDirectPair route them
// through the AAPCS64 `(x0, x1)` return-register pair without
// ever materialising a heap box.
func (g *generator) emitPairFormMaker(width int, tag int) {
	_ = width // payload width handled by the in-register move below
	g.pop()                                   // payload → x0
	g.emit("mov x1, x0")                      // save payload in x1
	g.emit("mov x0, #%d", tag)
	g.push()                                  // push tag
	g.emit("mov x0, x1")                      // restore payload
	g.push()                                  // push payload
}

func (g *generator) binPop() {
	g.emit("ldr x0, [sp], #%d", slotBytes) // rhs (top of stack)
	g.emit("ldr x1, [sp], #%d", slotBytes) // lhs (next)
}

// cmpForWidth emits a `cmp` whose operand size matches the
// integer width — `cmp x1, x0` for i64 / u64 / usize (width
// 64 or pointer-width on arm64), `cmp w1, w0` otherwise. The
// 32-bit form would truncate i64 operands to their lower 32
// bits, which silently mis-compares values whose upper bits
// matter (`1234567000000 < 0` evaluates false on the lower-
// 32-bit `1911386048` projection, but is correctly false on
// the full i64 — yet `1234567000000 == 0` evaluates true on
// the truncated projection of any multiple of 2^32 even
// though the full i64 is nonzero).
func (g *generator) cmpForWidth(width int) {
	if width == 64 || width == ir.WidthPtr {
		g.emit("cmp x1, x0")
		return
	}
	g.emit("cmp w1, w0")
}

// regForWidth returns the AArch64 register-name prefix matching
// the integer width — `x` for 64-bit / pointer-width, `w` for
// 32-bit and narrower. Callers paste this prefix in front of
// the register number: `g.regForWidth(64) + "0"` → "x0".
func (g *generator) regForWidth(width int) string {
	if width == 64 || width == ir.WidthPtr {
		return "x"
	}
	return "w"
}

// divOpForOp picks the AArch64 division opcode (`sdiv` /
// `udiv`) based on the IR op's `Unsigned` flag.
func (g *generator) divOpForOp(op ir.Op) string {
	if op.Unsigned {
		return "udiv"
	}
	return "sdiv"
}

// fbinPop32 pops two raw 32-bit float bit-patterns off the
// operand stack and loads them into s0 (rhs) and s1 (lhs)
// via fmov. The bit patterns are stored as i32 on the operand
// stack to keep the push/pop discipline uniform across i32 /
// f32 / i64 / f64; the V-register file gets involved only at
// op time.
func (g *generator) fbinPop32() {
	g.emit("ldr x0, [sp], #%d", slotBytes) // rhs raw bits
	g.emit("ldr x1, [sp], #%d", slotBytes) // lhs raw bits
	g.emit("fmov s0, w0")
	g.emit("fmov s1, w1")
}

// fbinPop64 is fbinPop32's f64 counterpart — uses the full
// 64-bit x-regs and double-precision d-regs.
func (g *generator) fbinPop64() {
	g.emit("ldr x0, [sp], #%d", slotBytes)
	g.emit("ldr x1, [sp], #%d", slotBytes)
	g.emit("fmov d0, x0")
	g.emit("fmov d1, x1")
}

// fcmpPop pops two floats, runs `fcmp` and `cset` to
// normalise the flag-state to 0 / 1, then pushes the i32
// result. The condition code chooses between the comparison
// shapes (eq / ne / mi / ls / gt / ge).
func (g *generator) fcmpPop(width int, cc string) {
	if width == 64 {
		g.fbinPop64()
		g.emit("fcmp d1, d0")
	} else {
		g.fbinPop32()
		g.emit("fcmp s1, s0")
	}
	g.emit("cset w0, %s", cc)
	g.push()
}

// irScope tracks one open OpBlock / OpLoop / OpIf scope. The
// IR's `br` instruction targets a scope by relative depth from
// the top, and the destination label depends on the scope kind:
//   - OpBlock: br jumps to the end of the block (forward).
//   - OpLoop:  br jumps to the start of the loop (backward).
//   - OpIf:    br jumps to the end of the if/else.
// `endLabel` is always the post-scope label; `brTarget` is what
// `br N` resolves to. For if-without-else we lazily define the
// elseLabel at the end so the OpIf's `cbz` fall-through skips
// the body.
type irScope struct {
	kind      ir.OpKind
	brTarget  string
	endLabel  string
	elseLabel string
	hasElse   bool
}

func (g *generator) freshLabel(prefix string) string {
	g.labelN++
	return fmt.Sprintf(".L%s_%d", prefix, g.labelN)
}

// emitOp dispatches a single IR op to its arm64 lowering.
// Each op consumes / produces operand-stack values via
// push() / pop(). Unsupported ops surface explicit errors so
// missing pieces are obvious.
//
// The `scope` slice tracks open OpBlock / OpLoop / OpIf scopes
// for `br` / `br_if` / `else` / `end` resolution. We pass it
// by pointer so OpBlock etc. can append; the caller (emitFunc)
// owns the slice.
func (g *generator) emitOp(op ir.Op, frameSize int, retLabel string, scope *[]irScope) error {
	switch op.Kind {
	case ir.OpConstI32:
		// Materialise the constant into x0 and push. Small
		// values fit `mov` (imm16); larger ones use `ldr =N`
		// (assembler pseudo-instruction backed by a literal
		// pool). i32s use the w0 alias when narrowing matters,
		// but for the 64-bit operand stack we keep them in x0.
		v := op.I32
		if v >= 0 && v <= 0xffff {
			g.emit("mov x0, #%d", v)
		} else {
			g.emit("ldr w0, =%d", v)
		}
		g.push()

	case ir.OpConstI64:
		// i64 literal. `ldr x0, =N` is the AArch64 assembler's
		// canonical idiom for a full 64-bit immediate — backed
		// by a literal pool entry the assembler emits in
		// `.text` and references via a pc-relative load. The
		// pool gets flushed by `.ltorg` (we already do this in
		// the alloc + read-line runtimes) or at end-of-section,
		// whichever comes first.
		g.emit("ldr x0, =%d", op.I64)
		g.push()

	case ir.OpConstStr:
		// Two-word ABI: push (data, len) — the data segment
		// holds just the bytes (no 4-byte length prefix), and
		// length lives on the operand stack as the second
		// word. Pointer materialised via the `adrp` + `add
		// :lo12:` pair — the canonical AArch64 PC-relative
		// addressing for absolute symbol values.
		//
		// Inline-form encoding (≤15 bytes via
		// langstring.PackInlineNative) is a follow-up
		// optimisation; for now every literal goes through the
		// .rodata data segment + a runtime byte length.
		lbl := g.internString(op.Str)
		g.adrpAdd("x0", lbl)
		g.push() // data
		g.emit("mov w0, #%d", len(op.Str))
		g.push() // len

	case ir.OpConstFunc:
		// Function values materialise as static 16-byte
		// closure-pair cells in .rodata: { fn_ptr (8B),
		// env_ptr=0 (8B) }. Mirrors the x86-64 + wasm shape so
		// OpCallIndirect can uniformly deref every callee
		// pair — top-level fn values (env=0) and runtime-
		// allocated closures (env points at the captured-slot
		// block) reach the same dispatch path.
		cell := fmt.Sprintf("__closure_cell_%s", op.Str)
		if g.constFuncCells == nil {
			g.constFuncCells = map[string]bool{}
		}
		g.constFuncCells[op.Str] = true
		g.adrpAdd("x0", cell)
		g.push()

	case ir.OpReturn:
		// String-returning fns under the two-word ABI return
		// `(data, len)` in (x0, x1). The operand stack has 2
		// values for the return — pop len first (top), data
		// second.
		if ast.UseTwoWordStrings(8) && g.current != nil {
			if _, isStr := g.current.ReturnType.(ast.StringType); isStr {
				g.emit("ldr x1, [sp], #16") // pop len
				g.emit("ldr x0, [sp], #16") // pop data
				g.emit("b %s", retLabel)
				break
			}
		}
		g.pop()
		g.emit("b %s", retLabel)
	case ir.OpReturnVoid:
		// Void return: no value to pop. The epilogue at
		// retLabel restores the frame and rets.
		g.emit("b %s", retLabel)
	case ir.OpReturnPair:
		// Multi-value pair-form return: pop the top operand-
		// stack slot into `x1` (payload, second AAPCS64
		// return reg), pop the next into `x0` (tag, first
		// AAPCS64 return reg), then branch to the epilogue.
		// The function-side ABI is now register-pair — callers
		// (OpCallDirectPair) consume `(x0, x1)` directly with
		// no heap-box round trip.
		g.pop()             // payload → x0
		g.emit("mov x1, x0")
		g.pop()             // tag → x0
		g.emit("b %s", retLabel)
	case ir.OpMakeSomeI32, ir.OpMakeOkI32:
		// Native fallback: heap-box layout matching
		// `payloadLayout`. `op.Width` selects the payload
		// store: zero (default) means i32 → alloc 8, payload
		// at +4 (4 bytes). WidthPtr means pointer-shape on
		// arm64 → alloc 16 (8-byte alignment for the 8-byte
		// payload), payload at +8 (8 bytes). x19 is
		// callee-save so it survives the bl __lang_alloc.
		g.emitPairFormMaker(op.Width, 0)
	case ir.OpMakeErrI32:
		// Same shape as Some/Ok but tag=1.
		g.emitPairFormMaker(op.Width, 1)
	case ir.OpMakeNoneI32:
		// Multi-value None: push (tag=1, payload=0) as two
		// operand-stack slots. No alloc, no heap-box pointer.
		g.emit("mov x0, #1")
		g.push() // tag
		g.emit("mov x0, #0")
		g.push() // payload (unused for None)

	case ir.OpDrop:
		g.emit("add sp, sp, #%d", slotBytes)

	// -------- arithmetic --------
	//
	// Use 64-bit register form (x0/x1) for all arithmetic so
	// pointer-shaped values (full 64-bit on aarch64) survive
	// `add` / `sub` / etc. unmangled. The IR's `len(s)` for
	// example does `ptr - 4` via OpSub; with the 32-bit form
	// (`sub w0, w1, w0`) the result would zero-extend, dropping
	// the high 32 bits of the pointer and faulting on the
	// subsequent load.
	//
	// i32 wraparound semantics are technically slightly off
	// (high bits aren't masked between ops), but the language's
	// integer overflow rules don't require modular semantics —
	// the wasm backend uses i32 ops directly which already
	// zero-extend, while consumers that read i32 explicitly
	// (str w0 / ldr w0 / cbz w0) only see the low 32 bits.

	case ir.OpAdd:
		g.binPop()
		g.emit("add x0, x1, x0")
		g.push()
	case ir.OpSub:
		g.binPop()
		g.emit("sub x0, x1, x0")
		g.push()
	case ir.OpMul:
		g.binPop()
		g.emit("mul x0, x1, x0")
		g.push()
	case ir.OpDivS:
		// AArch64's `sdiv` / `udiv` are base-ISA on ARMv8-A.
		// Width matters: i32 / u32 div uses the w-form
		// (32-bit), i64 / u64 / usize uses the x-form. The
		// previous unconditional `sdiv w0, w1, w0` silently
		// truncated i64 dividends to their lower 32 bits, which
		// broke `mag / 10` inside __int_to_string_u64 — every
		// large i64 stringified as its mod-2^32 projection.
		g.binPop()
		g.emit("%s %s0, %s1, %s0", g.divOpForOp(op), g.regForWidth(op.Width), g.regForWidth(op.Width), g.regForWidth(op.Width))
		g.push()
	case ir.OpRemS:
		g.binPop()
		divOp := g.divOpForOp(op)
		r := g.regForWidth(op.Width)
		g.emit("%s %s2, %s1, %s0", divOp, r, r, r)
		g.emit("msub %s0, %s2, %s0, %s1", r, r, r, r)
		g.push()
	case ir.OpAnd:
		g.binPop()
		g.emit("and x0, x1, x0")
		g.push()
	case ir.OpOr:
		g.binPop()
		g.emit("orr x0, x1, x0")
		g.push()
	case ir.OpXor:
		g.binPop()
		g.emit("eor x0, x1, x0")
		g.push()
	case ir.OpShl:
		g.binPop()
		g.emit("lsl x0, x1, x0")
		g.push()
	case ir.OpShrS:
		g.binPop()
		g.emit("asr x0, x1, x0")
		g.push()

	// -------- comparison (i32) --------
	//
	// AArch64 doesn't have flag-producing-as-result
	// instructions; the canonical idiom is `cmp` followed by
	// `cset Wd, <cond>` which writes 0 / 1 based on the flag.

	case ir.OpEq:
		g.binPop()
		g.cmpForWidth(op.Width)
		g.emit("cset w0, eq")
		g.push()
	case ir.OpNe:
		g.binPop()
		g.cmpForWidth(op.Width)
		g.emit("cset w0, ne")
		g.push()
	case ir.OpLtS:
		g.binPop()
		g.cmpForWidth(op.Width)
		g.emit("cset w0, lt")
		g.push()
	case ir.OpLeS:
		g.binPop()
		g.cmpForWidth(op.Width)
		g.emit("cset w0, le")
		g.push()
	case ir.OpGtS:
		g.binPop()
		g.cmpForWidth(op.Width)
		g.emit("cset w0, gt")
		g.push()
	case ir.OpGeS:
		g.binPop()
		g.cmpForWidth(op.Width)
		g.emit("cset w0, ge")
		g.push()

	// -------- logical / unary --------

	case ir.OpNot:
		// Boolean not: 0 → 1, non-zero → 0. `cmp / cset eq`
		// compares with 0 in xzr.
		g.pop()
		g.emit("cmp w0, #0")
		g.emit("cset w0, eq")
		g.push()

	// -------- locals --------

	case ir.OpLoadLocal:
		// Slot i lives at `slotOffsets[i]` from x29 — built by
		// emitFunc per-slot, since string slots take 16 bytes
		// under the two-word ABI. String slots: load `data` at
		// `off+8` and `len` at `off`, push both. Non-string:
		// load 8 bytes at `off`. frameLoad picks between `ldur`
		// (shallow frames, -256..+255 imm range) and a
		// scratch-register materialisation for deeper frames.
		off := g.slotOffsets[op.I32]
		if g.slotIsString(op.I32) {
			g.frameLoad("x0", off+8) // data
			g.push()
			g.frameLoad("x0", off) // len
			g.push()
		} else {
			g.frameLoad("x0", off)
			g.push()
		}
	case ir.OpStoreLocal:
		off := g.slotOffsets[op.I32]
		if g.slotIsString(op.I32) {
			g.pop()                  // x0 = len (top)
			g.frameStore("x0", off)  // store len
			g.pop()                  // x0 = data
			g.frameStore("x0", off+8) // store data
		} else {
			g.pop()
			g.frameStore("x0", off)
		}
	case ir.OpTeeLocal:
		// Pop, store, push back so the value stays on the
		// operand stack. arm64 has `ldr/str` post-increment but
		// no fused tee — issue the pop / str / push sequence.
		off := g.slotOffsets[op.I32]
		if g.slotIsString(op.I32) {
			// Stack on entry: [..., data, len], top = len.
			g.pop()                  // x0 = len
			g.emit("mov x1, x0")     // x1 = len
			g.pop()                  // x0 = data
			g.frameStore("x0", off+8) // store data
			g.frameStore("x1", off)   // store len
			// Re-push (data, len) so the value stays on the stack.
			g.push()                 // push data (x0)
			g.emit("mov x0, x1")
			g.push()                 // push len
		} else {
			g.pop()
			g.frameStore("x0", off)
			g.push()
		}

	// -------- state (module-global) vars --------

	// -------- direct calls --------

	// -------- control flow --------

	case ir.OpBlock:
		endL := g.freshLabel("blkEnd")
		*scope = append(*scope, irScope{kind: ir.OpBlock, brTarget: endL, endLabel: endL})
	case ir.OpLoop:
		startL := g.freshLabel("loopTop")
		endL := g.freshLabel("loopEnd")
		g.label(startL)
		*scope = append(*scope, irScope{kind: ir.OpLoop, brTarget: startL, endLabel: endL})
	case ir.OpIf:
		// `cbz w0, elseL` branches when w0 == 0.
		// Tests only the low 32 bits because i32 truthiness
		// is i32-shaped; using `cbz x0` would also test high
		// bits which the 64-bit-arithmetic mode (see comments
		// on OpAdd) deliberately leaves dirty.
		g.pop()
		elseL := g.freshLabel("ifElse")
		endL := g.freshLabel("ifEnd")
		g.emit("cbz w0, %s", elseL)
		*scope = append(*scope, irScope{kind: ir.OpIf, brTarget: endL, endLabel: endL, elseLabel: elseL})
	case ir.OpElse:
		top := &(*scope)[len(*scope)-1]
		top.hasElse = true
		g.emit("b %s", top.endLabel)
		g.label(top.elseLabel)
	case ir.OpEnd:
		top := (*scope)[len(*scope)-1]
		*scope = (*scope)[:len(*scope)-1]
		if top.kind == ir.OpIf && !top.hasElse {
			// No OpElse appeared; the OpIf's cbz was wired to
			// elseLabel, which we now define as the end so the
			// "condition false" branch simply skips the if body.
			g.label(top.elseLabel)
		}
		g.label(top.endLabel)
	case ir.OpBr:
		target := (*scope)[len(*scope)-1-int(op.I32)].brTarget
		g.emit("b %s", target)
	case ir.OpBrIf:
		g.pop()
		target := (*scope)[len(*scope)-1-int(op.I32)].brTarget
		// w0 form for the same reason as OpIf — only test the
		// low 32 bits since i32 truthiness is i32-shaped.
		g.emit("cbnz w0, %s", target)

	// -------- memory load / store --------

	case ir.OpLoad:
		// Pop addr; load 4 bytes (i32) or 8 bytes (i64) from
		// it. The IR distinguishes width via op.Width: 0 / 32
		// → 4-byte, 64 → 8-byte, WidthPtr (-1) → 8-byte on
		// arm64 (the heap pointer width — high bits of arm64-
		// darwin's >4 GiB heap survive). The i32 path uses the
		// w0 alias so the high half of x0 zero-extends cleanly.
		// WidthString (-2) fans the single addr to a two-word
		// (data, len) read: data at [addr + 0], len at
		// [addr + 8]. Both halves push as 8-byte stack slots
		// so downstream consumers see the (data, len) operand-
		// stack shape that the two-word ABI expects. Dead today
		// (no IR site emits WidthString for natives yet); wired
		// here ahead of the arm64 two-word flip so the gate
		// flip in the IR layer becomes a one-liner.
		if op.Width == ir.WidthString {
			g.pop() // addr → x0
			g.emit("ldr x1, [x0]")     // data @ +0
			g.emit("ldr x0, [x0, #8]") // len @ +8
			g.emit("mov x2, x0")       // save len
			g.emit("mov x0, x1")       // first push: data
			g.push()
			g.emit("mov x0, x2")
			g.push()
			break
		}
		g.pop()
		if op.Width == 64 || op.Width == ir.WidthPtr {
			g.emit("ldr x0, [x0]")
		} else {
			g.emit("ldr w0, [x0]")
		}
		g.push()
	case ir.OpMatchTag:
		// Transitional lowering — same as OpLoad of an i32
		// tag at offset 0. Step 4 of the Option/Result arc
		// swaps this for a tag-register read when the
		// scrutinee was the pair-form result of an
		// OpCallDirectPair.
		g.pop()
		g.emit("ldr w0, [x0]")
		g.push()
	case ir.OpLoadByte:
		g.pop()
		g.emit("ldrb w0, [x0]")
		g.push()
	// Sub-i32 typed loads. `ldrsb` / `ldrsh` sign-extend into
	// the destination 32-bit register (`w0`); the 64-bit reg
	// (`x0`) gets the upper half zeroed implicitly. `ldrh` is
	// the unsigned 16-bit half-word load. Pairs with wasm's
	// `i32.load8_s` / `i32.load16_u` / `i32.load16_s`.
	case ir.OpLoadI8S:
		g.pop()
		g.emit("ldrsb w0, [x0]")
		g.push()
	case ir.OpLoadI16U:
		g.pop()
		g.emit("ldrh w0, [x0]")
		g.push()
	case ir.OpLoadI16S:
		g.pop()
		g.emit("ldrsh w0, [x0]")
		g.push()
	case ir.OpStore:
		// Stack: [addr, value], top = value. WidthString
		// consumes a two-word `(data, len)` value (stack:
		// [addr, data, len], top = len) and fans the store to
		// two 8-byte writes: data @ [addr + 0] and len @
		// [addr + 8]. Dead today (no IR site emits WidthString
		// for natives yet); wired here ahead of the arm64
		// two-word flip so the gate flip in the IR layer
		// becomes a one-liner.
		if op.Width == ir.WidthString {
			g.emit("ldr x1, [sp], #16") // len
			g.emit("ldr x0, [sp], #16") // data
			g.emit("ldr x2, [sp], #16") // addr
			g.emit("str x0, [x2]")
			g.emit("str x1, [x2, #8]")
			break
		}
		g.emit("ldr x0, [sp], #16") // value
		g.emit("ldr x1, [sp], #16") // addr
		if op.Width == 64 || op.Width == ir.WidthPtr {
			g.emit("str x0, [x1]")
		} else {
			g.emit("str w0, [x1]")
		}
	case ir.OpStoreI8:
		g.emit("ldr x0, [sp], #16") // value
		g.emit("ldr x1, [sp], #16") // addr
		g.emit("strb w0, [x1]")
	case ir.OpStoreI16:
		g.emit("ldr x0, [sp], #16") // value
		g.emit("ldr x1, [sp], #16") // addr
		g.emit("strh w0, [x1]")

	case ir.OpAlloc:
		g.usesAlloc = true
		g.pop()
		g.emit("bl __lang_alloc")
		g.push()

	case ir.OpStrEq:
		// Equality via __lang_strcmp returning 0 (equal) /
		// 1 (different). Two-word ABI: stack has (a_data,
		// a_len, b_data, b_len), top = b_len. Pop into
		// (x3, x2, x1, x0) so the helper sees the AAPCS64
		// arg-register order it declares.
		g.usesStrcmp = true
		if ast.UseTwoWordStrings(8) {
			g.emit("ldr x3, [sp], #16") // b_len
			g.emit("ldr x2, [sp], #16") // b_data
			g.emit("ldr x1, [sp], #16") // a_len
			g.emit("ldr x0, [sp], #16") // a_data
			g.emit("bl __lang_strcmp")
			g.emit("cmp x0, #0")
			g.emit("cset w0, eq")
			g.push()
			break
		}
		// Legacy single-register.
		g.emit("ldr x1, [sp], #16")
		g.emit("ldr x0, [sp], #16")
		g.emit("bl __lang_strcmp")
		g.emit("cmp x0, #0")
		g.emit("cset w0, eq")
		g.push()

	// -------- floats (f32 / f64) --------
	//
	// Float values live as raw bit patterns on the operand
	// stack — i32 / i64 / f32 / f64 all occupy 16-byte stack
	// slots regardless of underlying type. For arithmetic +
	// comparison the codegen moves
	// the bit pattern into the V-register file (s0/s1 for
	// single-precision, d0/d1 for double-precision), runs the
	// op, and `fmov`s the result back. AArch64 has direct
	// `fmov` between x-regs and v-regs so this is a one-cycle
	// shuffle on most cores.

	case ir.OpConstF32:
		// Materialise the f32 bit pattern as an i32 literal.
		// The bit pattern bypasses the V-register file
		// entirely, going straight onto the operand stack as
		// a 32-bit raw value.
		//
		// Use `ldr x0, =<imm>` (literal-pool form) rather than
		// `mov x0, #<imm>`: a plain `mov` is restricted to
		// 16-bit shifted immediates, so any f32 whose bit
		// pattern needs >16 bits (every value except 0 and a
		// handful of sign-bit-only patterns) is rejected by
		// the assembler. The literal-pool form has no width
		// limit and matches OpConstF64's existing shape.
		bits := math.Float32bits(op.F32)
		g.emit("ldr x0, =%d", int64(bits))
		g.push()
	case ir.OpConstF64:
		bits := math.Float64bits(op.F64)
		g.emit("ldr x0, =%d", int64(bits))
		g.push()

	case ir.OpFAdd:
		if op.Width == 64 {
			g.fbinPop64()
			g.emit("fadd d0, d1, d0")
			g.emit("fmov x0, d0")
		} else {
			g.fbinPop32()
			g.emit("fadd s0, s1, s0")
			g.emit("fmov w0, s0")
		}
		g.push()
	case ir.OpFSub:
		if op.Width == 64 {
			g.fbinPop64()
			g.emit("fsub d0, d1, d0")
			g.emit("fmov x0, d0")
		} else {
			g.fbinPop32()
			g.emit("fsub s0, s1, s0")
			g.emit("fmov w0, s0")
		}
		g.push()
	case ir.OpFMul:
		if op.Width == 64 {
			g.fbinPop64()
			g.emit("fmul d0, d1, d0")
			g.emit("fmov x0, d0")
		} else {
			g.fbinPop32()
			g.emit("fmul s0, s1, s0")
			g.emit("fmov w0, s0")
		}
		g.push()
	case ir.OpFDiv:
		if op.Width == 64 {
			g.fbinPop64()
			g.emit("fdiv d0, d1, d0")
			g.emit("fmov x0, d0")
		} else {
			g.fbinPop32()
			g.emit("fdiv s0, s1, s0")
			g.emit("fmov w0, s0")
		}
		g.push()
	case ir.OpFNeg:
		if op.Width == 64 {
			g.pop()
			g.emit("fmov d0, x0")
			g.emit("fneg d0, d0")
			g.emit("fmov x0, d0")
		} else {
			g.pop()
			g.emit("fmov s0, w0")
			g.emit("fneg s0, s0")
			g.emit("fmov w0, s0")
		}
		g.push()

	case ir.OpFEq:
		g.fcmpPop(op.Width, "eq")
	case ir.OpFNe:
		g.fcmpPop(op.Width, "ne")
	case ir.OpFLt:
		g.fcmpPop(op.Width, "mi")
	case ir.OpFLe:
		g.fcmpPop(op.Width, "ls")
	case ir.OpFGt:
		g.fcmpPop(op.Width, "gt")
	case ir.OpFGe:
		g.fcmpPop(op.Width, "ge")

	case ir.OpFLoad:
		// Float values live as raw bit patterns; OpFLoad reads
		// 4 / 8 bytes from memory directly into x0. The V-
		// register file isn't involved on the read path.
		g.pop()
		if op.Width == 64 {
			g.emit("ldr x0, [x0]")
		} else {
			g.emit("ldr w0, [x0]")
		}
		g.push()
	case ir.OpFStore:
		g.emit("ldr x0, [sp], #16") // value
		g.emit("ldr x1, [sp], #16") // addr
		if op.Width == 64 {
			g.emit("str x0, [x1]")
		} else {
			g.emit("str w0, [x1]")
		}

	case ir.OpFPromoteF32:
		// f32 → f64. Move into s0, promote, move back as x0.
		g.pop()
		g.emit("fmov s0, w0")
		g.emit("fcvt d0, s0")
		g.emit("fmov x0, d0")
		g.push()
	case ir.OpFDemoteF64:
		// f64 → f32. Inverse: move into d0, demote, move back.
		g.pop()
		g.emit("fmov d0, x0")
		g.emit("fcvt s0, d0")
		g.emit("fmov w0, s0")
		g.push()

	// i32 ↔ i64 conversion. AArch64's 32-bit reg-operand
	// instructions implicitly zero the upper half of the
	// destination 64-bit reg, so wrap is a no-op at the
	// hardware level — but we still emit `mov w0, w0` to
	// make the truncation explicit (the assembler folds it
	// to `uxtw x0, w0` which costs nothing). Sign-extend
	// uses `sxtw` (the same on Linux + Darwin).
	case ir.OpExtendI32S:
		// i32 → i64, sign-extend low 32 bits.
		g.pop()
		g.emit("sxtw x0, w0")
		g.push()
	case ir.OpExtendI32U:
		// i32 → i64, zero-extend low 32 bits. `mov w0, w0`
		// zero-extends to x0 (the AArch64 idiom mirrors
		// x86-64's `mov eax, eax` trick).
		g.pop()
		g.emit("mov w0, w0")
		g.push()
	case ir.OpWrapI64:
		// i64 → i32. Discard the high half via the same
		// zero-extending mov-into-w0.
		g.pop()
		g.emit("mov w0, w0")
		g.push()

	// Sub-i32 sign-extension. AArch64 has dedicated forms
	// (`sxtb` for byte → 32-bit, `sxth` for halfword → 32-bit);
	// the 32-bit dest reg `w0` implicitly zero-extends into
	// x0 so the operand-stack 64-bit slot stays well-formed.
	// Pairs with wasm's `i32.extend8_s` / `i32.extend16_s`.
	case ir.OpSignExtend8:
		g.pop()
		g.emit("sxtb w0, w0")
		g.push()
	case ir.OpSignExtend16:
		g.pop()
		g.emit("sxth w0, w0")
		g.push()

	case ir.OpFConvertI32:
		// i32 → f32 / f64. scvtf signed / ucvtf unsigned.
		g.pop()
		if op.Width == 64 {
			if op.Unsigned {
				g.emit("ucvtf d0, w0")
			} else {
				g.emit("scvtf d0, w0")
			}
			g.emit("fmov x0, d0")
		} else {
			if op.Unsigned {
				g.emit("ucvtf s0, w0")
			} else {
				g.emit("scvtf s0, w0")
			}
			g.emit("fmov w0, s0")
		}
		g.push()
	case ir.OpFConvertI64:
		// i64 → f32 / f64. Same shape as OpFConvertI32 but
		// from a 64-bit source reg (x0). Unsigned variants
		// use ucvtf so values >= 2^63 convert correctly.
		g.pop()
		if op.Width == 64 {
			if op.Unsigned {
				g.emit("ucvtf d0, x0")
			} else {
				g.emit("scvtf d0, x0")
			}
			g.emit("fmov x0, d0")
		} else {
			if op.Unsigned {
				g.emit("ucvtf s0, x0")
			} else {
				g.emit("scvtf s0, x0")
			}
			g.emit("fmov w0, s0")
		}
		g.push()
	case ir.OpReinterpretI32F32, ir.OpReinterpretF32I32:
		// Bit-cast between f32 and i32. The operand stack
		// already stores both as raw 32-bit values (see
		// OpConstF32 — f32 bit patterns land on the stack as
		// raw i32s, no V-register involvement), and the
		// consuming op picks the right register bank via
		// `fmov` when needed. Nothing to emit here.
	case ir.OpITruncF32:
		// f32 → i32 / i64. fcvtzs truncates toward zero
		// (signed); fcvtzu does the unsigned variant.
		g.pop()
		g.emit("fmov s0, w0")
		opName := "fcvtzs"
		if op.Unsigned {
			opName = "fcvtzu"
		}
		if op.Width == 64 {
			g.emit("%s x0, s0", opName)
		} else {
			g.emit("%s w0, s0", opName)
		}
		g.push()
	case ir.OpITruncF64:
		g.pop()
		g.emit("fmov d0, x0")
		opName := "fcvtzs"
		if op.Unsigned {
			opName = "fcvtzu"
		}
		if op.Width == 64 {
			g.emit("%s x0, d0", opName)
		} else {
			g.emit("%s w0, d0", opName)
		}
		g.push()

	case ir.OpStrConcat:
		// The IR's `+` between strings lowers directly to
		// OpStrConcat (rather than going through OpCallDirect
		// to "__lang_strcat") so codegen owns the dispatch and
		// can target-specialise. Two-word ABI: stack has
		// (a_data, a_len, b_data, b_len), top = b_len. Pop
		// into (x3, x2, x1, x0) for AAPCS64 arg order. Return
		// is (data, len) in (x0, x1) → push both.
		g.usesStrcat = true
		g.usesAlloc = true
		g.usesMemcpy = true
		if ast.UseTwoWordStrings(8) {
			g.emit("ldr x3, [sp], #16") // b_len
			g.emit("ldr x2, [sp], #16") // b_data
			g.emit("ldr x1, [sp], #16") // a_len
			g.emit("ldr x0, [sp], #16") // a_data
			g.emit("bl __lang_strcat")
			g.push() // push data (x0)
			g.emit("mov x0, x1")
			g.push() // push len
			break
		}
		g.emit("ldr x1, [sp], #16") // b
		g.emit("ldr x0, [sp], #16") // a
		g.emit("bl __lang_strcat")
		g.push()

	case ir.OpStrLen:
		// Two-word ABI: pop (data, len). Discard data; extract
		// byte length from len via emitStrLen2W (top-bit-tagged
		// flag check).
		if ast.UseTwoWordStrings(8) {
			g.pop() // x0 = len (top of stack)
			g.emit("mov x1, x0")
			g.pop() // x0 = data (discard)
			g.emitStrLen2W("w0", "x1")
			g.push()
			break
		}
		// Legacy single-register string: pop the string-ptr value
		// and let emitStrLen branch on the LSB-tagged inline flag.
		g.pop()
		g.emitStrLen("w0", "x0")
		g.push()

	case ir.OpEnumSentinel:
		// Push the address of a shared static `[tag=N]` sentinel.
		// One symbol per unique tag value, lazily reserved in
		// .rodata so programs that never construct payloadless
		// variants pay nothing extra.
		tag := int(op.I32)
		if g.enumSentinelTags == nil {
			g.enumSentinelTags = map[int]bool{}
		}
		g.enumSentinelTags[tag] = true
		g.adrpAdd("x0", fmt.Sprintf(".LEnumSentinel_%d", tag))
		g.push()

	case ir.OpCallIndirect:
		// Function-value call: the IR emitted a closure-pair
		// pointer immediately before the call op. Every
		// function value on natives is now a {fn_ptr, env_ptr}
		// pair — OpConstFunc emits static .rodata cells with
		// env=0; OpMakeClosure allocates heap pairs whose env
		// points at the captured slot block. Either way:
		//   1. Pop pair pointer into x16.
		//   2. Load fn_ptr from [pair + 0] into x17.
		//   3. Load env_ptr from [pair + 8] and push onto the
		//      operand stack so emitCallArgsLoad routes it
		//      into the (argc+1)th argument register.
		//   4. blr x17.
		//
		// Top-level fns called this way receive env=0 in an
		// extra register slot they don't read — AAPCS64's
		// "unused arg registers may hold any value" rule keeps
		// that harmless. Hoisted closures read env from the
		// same register.
		argc := int(op.I32)
		g.emit("ldr x16, [sp], #%d", slotBytes) // x16 = pair pointer
		g.emit("ldr x17, [x16]")                // x17 = fn_ptr (= [pair + 0])
		g.emit("ldr x0, [x16, #8]")             // x0 = env_ptr (= [pair + 8])
		g.emit("str x0, [sp, #-%d]!", slotBytes)
		// Under the two-word string ABI, string args occupy 2
		// operand-stack slots → 2 arg registers. Consult the
		// static Sig stamped on OpCallIndirect to translate
		// argc (user-visible param count) into the effective
		// slot count. The trailing env_ptr is always 1 slot.
		slotCount := argc + 1
		if ast.UseTwoWordStrings(8) && op.Sig != nil {
			slotCount = 1 // env_ptr
			for _, t := range op.Sig.Params {
				if _, isStr := t.(ast.StringType); isStr {
					slotCount += 2
				} else {
					slotCount += 1
				}
			}
		}
		g.emitCallArgsLoad(slotCount)
		g.emit("blr x17")
		g.emitCallArgsCleanup(slotCount)
		// Indirect-call return: push (data, len) under two-
		// word strings when the static signature says the
		// callee returns a string. Void-returning function
		// values don't materialise as values today (the IR
		// emits OpCallIndirect through a non-void expression
		// position), so we always push at least one slot.
		if ast.UseTwoWordStrings(8) && op.Sig != nil {
			if _, isStr := op.Sig.Result.(ast.StringType); isStr {
				g.push()
				g.emit("mov x0, x1")
				g.push()
				break
			}
		}
		g.push()

	case ir.OpCallClosureDirect:
		// Defunctionalised closure call. Operand stack holds
		// (args..., env_ptr) — same shape as OpCallDirect with
		// one extra arg (the trailing __env, present in the
		// hoisted callee's Params list as its last entry).
		// Under the two-word string ABI string args take 2
		// operand-stack slots → 2 arg registers, and a string
		// return arrives as (data, len) in (x0, x1) — both
		// halves need pushing post-call.
		argc := int(op.I32)
		slotCount := argc
		if ast.UseTwoWordStrings(8) {
			if callee, ok := g.funcs[op.Str]; ok && callee != nil {
				slotCount = 0
				for _, p := range callee.Params {
					if _, isStr := p.Type.(ast.StringType); isStr {
						slotCount += 2
					} else {
						slotCount += 1
					}
				}
			}
		}
		g.emitCallArgsLoad(slotCount)
		g.emit("bl %s", op.Str)
		g.emitCallArgsCleanup(slotCount)
		if returnIsVoid(g, op.Str) {
			break
		}
		if ast.UseTwoWordStrings(8) && returnIsString(g, op.Str) {
			g.push() // push data (x0)
			g.emit("mov x0, x1")
			g.push() // push len
			break
		}
		g.push()

	case ir.OpMakeClosure, ir.OpMakeEnv:
		return g.emitMakeClosureOrEnv(op)

	case ir.OpCallDirect:
		// AAPCS64: load args 0..n-1 from the operand stack into
		// x0..x{n-1} (rightmost-on-top, so we pop in reverse
		// order), then `bl target`. Result lands in x0; push it.
		// Rewrite a small set of names where the lang prelude's
		// callable name differs from the emitted symbol (e.g.
		// `__memcpy` → `__lang_memcpy`, `map_new` →
		// `map_new_impl`).
		target := op.Str
		switch target {
		case "__memcpy":
			target = "__lang_memcpy"
			g.usesMemcpy = true
		case "__lang_strcat":
			g.usesStrcat = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "__alloc":
			target = "__lang_alloc"
			g.usesAlloc = true
		case "__slice_make":
			target = "__lang_slice_make"
			g.usesSliceMake = true
			g.usesAlloc = true
		case "__store_i32", "__load_i32", "__store_ptr", "__load_ptr", "__ptr_width":
			g.usesRawIntPokes = true
		case "__memset":
			g.usesMemset = true
		case "__alloc_u8":
			g.usesAllocU8 = true
			g.usesAlloc = true
		case "string_from_bytes":
			g.usesStringFromBytes = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "tcp_listen":
			target = "__lang_tcp_listen"
			g.usesTcp = true
			g.usesAlloc = true
		case "tcp_accept":
			target = "__lang_tcp_accept"
			g.usesTcp = true
		case "tcp_recv":
			target = "__lang_tcp_recv"
			g.usesTcp = true
			g.usesAlloc = true
		case "tcp_send":
			target = "__lang_tcp_send"
			g.usesTcp = true
		case "tcp_close":
			target = "__lang_tcp_close"
			g.usesTcp = true
		// Map / MapIter — the lang Map runtime lives entirely
		// in the lang prelude under `_impl`-suffixed names;
		// user-facing call sites use the unsuffixed mangled
		// name and codegen rewrites here.
		case "map_new":
			target = "map_new_impl"
		case "__method_Map_len":
			target = "__map_len_impl"
		case "__method_Map_has":
			target = "__map_has_impl"
		case "__method_Map_get":
			target = "__map_get_impl"
		case "__method_Map_get_or":
			target = "__map_get_or_impl"
		case "__method_Map_set":
			target = "__map_set_impl"
		case "__method_Map_delete":
			target = "__map_delete_impl"
		case "__method_Map_clear":
			target = "__map_clear_impl"
		case "__method_Map_keys":
			target = "__map_keys_impl"
		case "__method_Map_values":
			target = "__map_values_impl"
		case "__method_Map_iter":
			target = "__map_iter_impl"
		case "__method_MapIter_has_next":
			target = "__mapiter_has_next_impl"
		case "__method_MapIter_key":
			target = "__mapiter_key_impl"
		case "__method_MapIter_value":
			target = "__mapiter_value_impl"
		case "__method_MapIter_advance":
			target = "__mapiter_advance_impl"
		case "__str_idx", "__arr_idx", "__arr_idx_1", "__arr_idx_2", "__arr_idx_8", "__arr_idx_16",
			"__slice_idx", "__slice_idx_1", "__slice_idx_2", "__slice_idx_8", "__slice_idx_16":
			// IR-side bounds-check stubs the lang runtime
			// would otherwise dispatch to. arm64 doesn't yet
			// ship the helpers, so inline an unchecked
			// address compute that matches the wasm
			// behaviour for in-range indices. The element-
			// stride is encoded in the helper name.
			return g.emitInlineIdxHelper(target)
		case "__str_slice":
			// String slice — allocates a fresh substring.
			// Real runtime helper, not inlined.
			g.usesStrSlice = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "env":
			target = "__lang_env"
			g.usesEnv = true
			g.usesAlloc = true
			// __lang_env walks envp and `bl __lang_memcpy`s
			// each candidate value into a fresh lang string,
			// so we need the memcpy runtime too.
			g.usesMemcpy = true
		case "print":
			// print(s): write string + newline. The runtime
			// helper handles both writes.
			target = "__lang_puts"
			g.usesPuts = true
		case "write":
			// write(s): write string, no newline.
			target = "__lang_write"
			g.usesWrite = true
		case "putchar":
			// putchar(c): write the single byte.
			target = "__lang_putchar"
			g.usesPutchar = true
		case "eprint":
			// eprint(s): print to stderr (fd 2) + newline.
			target = "__lang_eprint"
			g.usesEprint = true
		case "exit":
			// exit(code): direct exit syscall. Never returns,
			// but codegen still emits the post-call stack-
			// push for stack-discipline; harmless because the
			// call never comes back.
			target = "__lang_exit"
			g.usesExit = true
		case "args":
			// args(): returns a length-prefixed string[] of
			// argv. Caches the result so repeat calls are O(1).
			target = "__lang_args"
			g.usesArgs = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "arena_save":
			// arena_save(): snapshot the bump cursor.
			target = "__lang_arena_save"
			g.usesArena = true
			g.usesAlloc = true
		case "arena_restore":
			// arena_restore(saved): rewind the bump cursor.
			target = "__lang_arena_restore"
			g.usesArena = true
			g.usesAlloc = true
		case "read_line":
			// read_line(): byte-by-byte stdin read into a 4 KiB
			// .bss buffer; returns Option[string].
			target = "__lang_read_line"
			g.usesReadLine = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "__method_Reader_read_line":
			// `r.read_line()` — loads fd from the receiver's
			// first field and reads from THAT fd byte-by-byte
			// into the shared 4-KiB scratch buffer. Returns
			// Option[string]: Some(line) when at least one
			// byte was read, None on first-byte EOF.
			target = "__lang_reader_read_line"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "__method_Reader_read_chunk":
			// `r.read_chunk(n)` — single read of up to n bytes
			// from receiver.fd. Returns Option[string]: None
			// on EOF (read returned 0).
			target = "__lang_reader_read_chunk"
			g.usesReaderWriter = true
			g.usesAlloc = true
		case "__method_Reader_close":
			target = "__lang_close_fd_box"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesIoError = true
		case "__method_Writer_write":
			target = "__lang_writer_write"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesIoError = true
		case "__method_Writer_close":
			target = "__lang_close_fd_box"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesIoError = true
		case "open_reader":
			target = "__lang_open_reader"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesIoError = true
		case "open_writer":
			target = "__lang_open_writer"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesIoError = true
		case "open_appender":
			target = "__lang_open_appender"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesIoError = true
		case "stdin":
			// stdin() / stdout() / stderr() return real Reader /
			// Writer struct pointers now (fd at +0). Wraps the
			// standard fds (0 / 1 / 2) in the same alloc shape
			// open_reader / open_writer produce.
			target = "__lang_stdin"
			g.usesReaderWriter = true
			g.usesAlloc = true
		case "stdout":
			target = "__lang_stdout"
			g.usesReaderWriter = true
			g.usesAlloc = true
		case "stderr":
			target = "__lang_stderr"
			g.usesReaderWriter = true
			g.usesAlloc = true
		case "read_file":
			// read_file(path): Result[string, IoError] —
			// openat + fstat + read-loop + close on Linux.
			target = "__lang_read_file"
			g.usesReadFile = true
			g.usesAlloc = true
			g.usesIoError = true
		case "write_file":
			// write_file(path, content): Option[IoError] —
			// openat(O_WRONLY|O_CREAT|O_TRUNC) + write-loop +
			// close on Linux.
			target = "__lang_write_file"
			g.usesWriteFile = true
			g.usesAlloc = true
			g.usesIoError = true
		case "random_bytes":
			// random_bytes(n): allocates an n-byte string and
			// fills it with kernel CSPRNG output. Linux uses
			// getrandom (syscall 278); Darwin uses chunked
			// getentropy (syscall 500, max 256 bytes/call).
			target = "__lang_random_bytes"
			g.usesRandomBytes = true
			g.usesAlloc = true
		}
		// Compute the effective operand-stack slot count for
		// the call: under the two-word ABI, each string arg
		// occupies 2 slots → 2 registers. For user functions we
		// look up the FuncDecl signature; built-in runtime
		// helpers fall back to a hardcoded string-arg table
		// (the runtime helpers' Go-side emit code will be
		// migrated to the two-word ABI in follow-up commits).
		argc := int(op.I32)
		slotCount := argc
		if ast.UseTwoWordStrings(8) {
			argTypes := callArgTypes(g, op, argc)
			if argTypes != nil {
				slotCount = 0
				for _, t := range argTypes {
					if _, isStr := t.(ast.StringType); isStr {
						slotCount += 2
					} else {
						slotCount += 1
					}
				}
			}
		}
		g.emitCallArgsLoad(slotCount)
		g.emit("bl %s", target)
		g.emitCallArgsCleanup(slotCount)
		// Push return value(s). String-returning user fns return
		// (data, len) in (x0, x1) under the two-word ABI; push
		// both. Non-string returns push x0 only. Void-returning
		// callees push NOTHING — see returnIsVoid for the
		// rationale.
		if returnIsVoid(g, op.Str) {
			break
		}
		if ast.UseTwoWordStrings(8) && returnIsString(g, op.Str) {
			g.push() // push data (x0)
			g.emit("mov x0, x1")
			g.push() // push len
			break
		}
		g.push()

	case ir.OpCallDirectPair:
		// Multi-value pair-form call: callee returns (tag,
		// payload) in (x0, x1) per AAPCS64. Push both directly
		// to the operand stack — no heap-box round trip. The
		// caller may follow with OpStoreLocal / OpMatchTag
		// (scrutinee position) or emitRepackPairAsHeapBox
		// (generic position) — the IR-level "two values post-
		// call" contract is now register-backed.
		//
		// Under the two-word string ABI, string args take 2
		// operand-stack slots → 2 register slots. Look up the
		// callee's signature via `lookupArgTypes` to compute
		// the effective slot count (same path OpCallDirect
		// uses).
		argc := int(op.I32)
		slotCount := argc
		if ast.UseTwoWordStrings(8) {
			argTypes := callArgTypes(g, op, argc)
			if argTypes != nil {
				slotCount = 0
				for _, t := range argTypes {
					if _, isStr := t.(ast.StringType); isStr {
						slotCount += 2
					} else {
						slotCount += 1
					}
				}
			}
		}
		g.emitCallArgsLoad(slotCount)
		g.emit("bl %s", op.Str)
		g.emitCallArgsCleanup(slotCount)
		g.emit("mov x16, x1") // stash payload (x1 may be clobbered)
		g.push()              // push x0 (tag)
		g.emit("mov x0, x16")
		g.push()              // push payload

	default:
		return fmt.Errorf("arm64: unsupported IR op %s", op.Kind)
	}
	return nil
}
