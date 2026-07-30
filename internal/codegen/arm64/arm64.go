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
	sysConnect   = 203
	// clock_gettime(2): asm-generic table syscall 113.
	// Used by `__fern_now_unix_ms` / `__fern_monotonic_ns` for the
	// clock-now surface (docs/STDLIB-DESIGN-RESEARCH.md Rec §4 Phase 2).
	sysClockGettime = 113
	// nanosleep(2): asm-generic table syscall 101. Backs
	// `__fern_sleep_ms`.
	sysNanosleep = 101
	// clone(2) / wait4(2): asm-generic table syscalls 220 / 260.
	// Back `__fern_proc_fork` / `__fern_proc_waitpid` — the
	// crash-only supervision primitives (docs/CRASH-ONLY-SERVE.md
	// D2'). arm64 Linux has no bare fork(2) syscall; fork is
	// `clone(SIGCHLD, 0, 0, 0, 0)`.
	sysClone = 220
	sysWait4 = 260
	// ppoll(2): asm-generic table syscall 73 (arm64 has no bare
	// `poll`). Backs `__fern_poll` — the std/task reactor's readiness
	// multiplexer (docs/ASYNC-IMPLEMENTATION-PLAN.md Phase 1).
	sysPpoll = 73
	// timerfd_create(2) / timerfd_settime(2): asm-generic table 85 / 86.
	// Back `__fern_timer_fd(ms)` — a CLOCK_MONOTONIC timerfd readable
	// after `ms` (docs/ASYNC-IMPLEMENTATION-PLAN.md Phase 1c).
	sysTimerfdCreate  = 85
	sysTimerfdSettime = 86
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
	darConnect    = 98
	darSocket     = 97
	darBind       = 104
	darListen     = 106
	darMmap       = 197
	darOpenat     = 463
	darGetentropy = 500
	// fstat64 (BSD 339): arm64 macOS is always 64-bit-inode, so this
	// fills the modern `struct stat` whose st_size sits at offset 96
	// (vs Linux's 48). gettimeofday (BSD 116) fills a `struct timeval`
	// {i64 tv_sec @0, i32 tv_usec @8} — Darwin's stand-in for the
	// clock_gettime the Linux now-helper uses.
	darFstat64      = 339
	darGettimeofday = 116
	// select (BSD 93): Darwin has no nanosleep syscall, so
	// `__fern_sleep_ms` sleeps via select(0, NULL, NULL, NULL,
	// &timeout) with a `struct timeval {i64 tv_sec @0, i64 tv_usec @8}`.
	darSelect = 93
	// fork (BSD 2) / wait4 (BSD 7): back `__fern_proc_fork` /
	// `__fern_proc_waitpid` on Darwin. XNU's fork returns the
	// pid in x0 with x1 = 0 in the parent / 1 in the child (the
	// libsyscall __fork stub folds x1 into the 0-in-child
	// convention; our helper does the same). wait4's status-word
	// layout matches Linux's (low 7 bits = signal, bits 8..15 =
	// exit code), so the decode is shared.
	darFork  = 2
	darWait4 = 7
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
	"read":    {sysRead, darRead},
	"write":   {sysWrite, darWrite},
	"close":   {sysClose, darClose},
	"socket":  {sysSocket, darSocket},
	"bind":    {sysBind, darBind},
	"listen":  {sysListen, darListen},
	"accept":  {sysAccept, darAccept},
	"connect": {sysConnect, darConnect},
	"openat":  {sysOpenat, darOpenat},
	// unlinkat(2) / mkdirat(2): identical arg shapes on both
	// platforms (Darwin BSD 472 / 475). Back __fern_remove_file /
	// __fern_remove_dir_all / __fern_temp_dir.
	"unlinkat":   {35, 472},
	"mkdirat":    {34, 475},
	"exit":       {sysExit, darExit},
	"exit_group": {sysExitGroup, darExit},
	"mmap":       {sysMmap, darMmap},
}

// linuxOnlySysno carries syscalls whose Linux number we know
// but whose Darwin equivalent doesn't exist with the same ABI
// shape — `getrandom` (Darwin uses chunked getentropy). The
// `syscall()` helper emits the Linux form when `!g.darwin`
// and panics at codegen time on Darwin, so a helper that
// hasn't been ported to Darwin surfaces visibly when the
// driver builds with `-target arm64-darwin` instead of
// silently producing wrong asm. (fstat and clock_gettime used
// to live here; they're now branched inline for Darwin — see
// syscallFstat and emitNowUnixMsRuntime.)
// f64UnaryIntrinsic maps the cheap f64 math builtins to the single
// arm64 FP instruction that implements them — no libm, no runtime
// helper. floor/ceil/trunc/round are the FRINT rounding modes
// (toward -inf / +inf / zero / nearest-ties-away).
var f64UnaryIntrinsic = map[string]string{
	"__abs_f64":   "fabs",
	"__sqrt_f64":  "fsqrt",
	"__floor_f64": "frintm",
	"__ceil_f64":  "frintp",
	"__trunc_f64": "frintz",
	"__round_f64": "frinta",
}

var linuxOnlySysno = map[string]int{
	"getrandom": sysGetrandom,
	// ppoll: Linux-only here; arm64-darwin's readiness path (kqueue)
	// is deferred, so `__fern_poll` branches to a -1 stub on Darwin
	// rather than reaching this entry.
	"ppoll": sysPpoll,
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

	// PIE emits a static position-independent (ET_DYN) executable: a
	// self-relocation prologue runs at `_start` and applies the
	// R_AARCH64_RELATIVE entries in .rela.dyn (the `.quad <symbol>`
	// function-pointer / vtable slots) before the program runs, so the
	// binary is correct at the arbitrary base the kernel loads it at.
	// Pair with arm64.AssembleProgramPIE + elf.StaticPieExecutable. Linux
	// only (the Darwin path is its own non-PIE Mach-O image).
	PIE bool

	// Exports are function names kept as tree-shake roots (in addition to
	// `main`) so a `-shared` .so can export functions the program never
	// calls itself — e.g. JNI entry points, which only the JVM invokes.
	Exports []string

	// NoPeephole disables the streaming output peephole (see generator.put).
	// The peephole is a pure size optimisation and on by default; this only
	// exists so asm-shape tests can assert the un-collapsed emission.
	NoPeephole bool

	// DebugLines makes the lowering emit OpLine markers and the backend emit
	// DWARF `.loc` directives, so the assembler can build a .debug_line
	// source-line table (#5537 slice 2). Set under `fern -g`. Mirror of the
	// x86-64 backend's DebugLines.
	DebugLines bool
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
	//
	// `ast.CodegenMu` serialises this toggle against itself
	// (multiple arm64 Emit goroutines) and against
	// `x86_64.Emit` (which reads `TwoWordOverride` via
	// ir.LowerWith without setting it). Without the lock,
	// `TestDifferential_LangsmithMain`'s seed-level
	// `t.Parallel` lets one arm64 emit's `defer` restore the
	// flag to false while another arm64 emit was still in
	// flight — producing single-word string_from_bytes_unchecked /
	// strcat helpers inside a program the rest of the
	// codegen built for two-word strings, and SIGSEGV on
	// the first string op.
	ast.CodegenMu.Lock()
	defer ast.CodegenMu.Unlock()
	prevOverride := ast.TwoWordOverride
	ast.TwoWordOverride = true
	defer func() { ast.TwoWordOverride = prevOverride }()

	// `dyn Trait` vtable impl methods are reachable only through the
	// runtime vtable (OpConstVtable names them by string), never via a
	// static call the AST walker / IR reachability can see — pin them as
	// tree-shake roots so they survive (mirrors the x86-64 + wasm build
	// paths). See docs/DYN-TRAITS.md §4.2.2.
	dynRoots := append(treeshake.DynCoercionImplMethods(info), treeshake.DowncastImplMethods(prog, info)...)
	dynRoots = append(dynRoots, opts.Exports...) // -shared exports survive tree-shaking
	treeshake.Run(prog, dynRoots...)
	// arm64 supports boxed one-word `dyn Trait` values
	// (docs/DYN-TRAITS.md §4.2.2): DynSupported lifts the dispatch gate
	// (the same boxed representation x86-64 uses — both are ptrW==8 — so
	// ptrW alone can't lift it). It ALSO reclaims them (Perceus RC, slice
	// 4c — docs/DYN-TRAITS.md §4.4): DynRcSupported lifts the RC path (the
	// trailing vtable drop slot + the per-set __drop_dyn_<set> helper + the
	// dec/drop sweep arms), the structural mirror of the x86-64 slice 4b.
	lowerOpts := []ir.LowerOption{ir.DynSupported(), ir.DynRcSupported()}
	if opts.DebugLines {
		lowerOpts = append(lowerOpts, ir.EmitLineMarkers())
	}
	ip, err := ir.LowerWith(prog, info, 8, lowerOpts...)
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
	// IR pass battery (#4377) — mirrors the x86-64 backend: in-place
	// per-function rewrites that keep ip.Funcs index-aligned with prog.Funcs
	// for the parallel walk below (slice 1, #4678). OptimizeCleanup (the
	// copyprop/constprop/Fold/strength fixpoint) now runs too (slice 1b): the
	// ir.Fold emitter crash it hit is fixed (the index zero-extend in
	// emitInlineIdxHelper above — a folded-constant array index otherwise
	// carried dirty upper bits into the scaled address add, past the 32-bit
	// bounds check), and the fixpoint's old up-to-8× whole-program convergence
	// snapshot is gone (each sub-pass reports a changed bool), so it no longer
	// balloons self-host build time. ir.Inline + the whole-function cull remain
	// slice 2 (parallel-index → name-keyed walk).
	ir.FuseTee(ip)
	ir.FlattenBranches(ip)
	ir.EliminateDeadCode(ip)
	ir.OptimizeCleanup(ip)
	g := &generator{info: info, stringLabel: map[string]string{}, funcs: map[string]*ast.FuncDecl{}, darwin: opts.Darwin, pie: opts.PIE, vtables: ip.Vtables, noPeephole: opts.NoPeephole}
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
	for _, fn := range ip.Funcs {
		for _, op := range fn.Ops {
			if op.Kind == ir.OpMakeClosure || op.Kind == ir.OpMakeEnv {
				// Closure env block (+ optional pair) come
				// from __fern_alloc.
				g.usesAlloc = true
				continue
			}
			if op.Kind == ir.OpBoxDyn {
				// The boxed `dyn Trait` cell is a normal heap object
				// allocated via __fern_alloc (docs/DYN-TRAITS.md §4.2.2).
				g.usesAlloc = true
				continue
			}
			if op.Kind != ir.OpCallDirect &&
				op.Kind != ir.OpRcInc && op.Kind != ir.OpRcDec && op.Kind != ir.OpRcIsUnique {
				continue
			}
			switch op.Str {
			case "args":
				g.usesArgs = true
			case "env":
				g.usesEnv = true
			case "now_unix_ms":
				g.usesNowUnixMs = true
			case "monotonic_ns":
				g.usesMonotonicNs = true
			case "now_ns":
				g.usesNowNs = true
			case "sleep_ms":
				g.usesSleepMs = true
			}
		}
	}
	g.line(`.arch armv8-a`)
	g.line(`.text`)
	g.emitStartRuntime()
	for i, fn := range prog.Funcs {
		if err := g.emitFunc(fn, ip.Funcs[i]); err != nil {
			return "", err
		}
		// Release the function's IR as soon as it is emitted (mirrors the
		// x86-64 backend): nothing after this loop reads ip.Funcs, and
		// dropping each entry lets the GC reclaim the program's ops
		// incrementally instead of holding all of them until the output
		// buffer peaks. Output is unaffected (forward-only walk).
		ip.Funcs[i] = nil
	}
	// The Reader/Writer runtime is emitted as a single bundle:
	// every helper (open_*, read_line, read_chunk, close,
	// write, make_handle, stdin/stdout/stderr) ships together.
	// That means whenever the bundle is pulled in, its
	// callees must be too — __fern_alloc, __fern_memcpy, and
	// the IoError box constructor (`.LStr_ioerr_empty` lives
	// there) all show up indirectly. usesReaderWriter is set
	// during per-function emit (above), so we propagate here
	// before the runtime-gate checks below.
	if g.usesReaderWriter {
		g.usesIoError = true
		g.usesMemcpy = true
		g.usesAlloc = true
	}
	// The file-I/O + filesystem-op helpers all NUL-terminate the
	// path with an alloc + memcpy before their path syscall (see
	// emitNulTermPath2W) — pull the runtimes in so the helpers
	// link. read_file already needs __fern_alloc for the result
	// string buffer; the rest get it transitively too. The IoError
	// box constructor is shared with the Reader/Writer family
	// above; pulled in here for the programs that use file I/O
	// without the Reader API.
	if g.usesReadFile || g.usesWriteFile || g.usesRemoveFile ||
		g.usesTempDir || g.usesReadDir || g.usesStat || g.usesRemoveDirAll {
		g.usesAlloc = true
		g.usesMemcpy = true
		g.usesIoError = true
	}
	// temp_dir's unique suffix rides the monotonic clock.
	if g.usesTempDir {
		g.usesMonotonicNs = true
	}
	// Fatal-abort diagnostics (#5538): __fern_report writes an abort's cause
	// to stderr before exit, so a bounds / arena / slice abort names itself
	// instead of exiting silently. Emitted unconditionally — the abort sites
	// branch here, and label resolution is order-independent.
	g.emitAbortRuntime()
	if g.usesAlloc {
		g.emitAllocRuntime()
		// __fern_alloc_box piggybacks on __fern_alloc — the
		// enum-box runtime helpers call it for the
		// static-sentinel rc header.
		g.emitAllocBoxRuntime()
		// Live-rc variant for closure env blocks / pairs.
		g.emitAllocRc1Runtime()
	}
	if g.usesFree {
		g.emitFreeRuntime()
	}
	if g.usesAllocReuse {
		g.emitAllocReuseRuntime()
	}
	if g.usesArrDec {
		g.emitArrDecRuntime()
	}
	if g.usesMapDrop {
		g.emitMapDropRuntime()
	}
	if g.usesBoxFree {
		g.emitBoxFreeRuntime()
	}
	if g.usesClosureDrop {
		g.emitClosureDropRuntime()
	}
	if g.usesMemcpy {
		g.emitMemcpyRuntime()
	}
	for n := range g.usesCCall {
		if g.usesCCall[n] {
			g.emitCCallRuntime(n)
		}
		if g.usesCCallF32[n] {
			g.emitCCallRuntimeSuffixed(n, "_f32")
		}
		if g.usesCCallF64[n] {
			g.emitCCallRuntimeSuffixed(n, "_f64")
		}
	}
	if g.usesRcInc {
		g.emitRcIncRuntime()
	}
	if g.usesStrInc {
		g.emitStrIncRuntime()
	}
	if g.usesStrDec {
		g.emitStrDecRuntime()
	}
	if g.usesCellFree {
		g.emitCellFreeRuntime()
	}
	if g.usesArrPushGrow {
		g.emitArrPushGrowRuntime()
	}
	if g.usesArrPushGrowPtr {
		g.emitArrPushGrowPtrRuntime(false)
	}
	if g.usesArrPushGrowMovePtr {
		g.emitArrPushGrowPtrRuntime(true)
	}
	if g.usesArrPushGrowStr {
		g.emitArrPushGrowStrRuntime(false)
	}
	if g.usesArrPushGrowMoveStr {
		g.emitArrPushGrowStrRuntime(true)
	}
	if g.usesArrCowInPlace {
		g.emitArrCowInPlaceRuntime()
	}
	if g.usesArrCowInPlacePtr {
		g.emitArrCowInPlacePtrRuntime()
	}
	if g.usesDropArrPtr {
		g.emitDropArrPtrRuntime()
	}
	if g.usesDropArrStr {
		g.emitDropArrStrRuntime()
	}
	if g.usesRcIsUnique {
		g.emitRcIsUniqueRuntime()
	}
	if g.usesRcDec {
		g.emitRcDecRuntime()
	}
	if g.usesRcUnderflowCount {
		g.emitRcUnderflowCountRuntime()
	}
	if g.usesHeapBumpBytes {
		g.emitHeapBumpBytesRuntime()
	}
	if g.usesHeapMark {
		g.emitHeapMarkRuntime()
	}
	if g.usesSliceMake {
		g.emitSliceMakeRuntime()
	}
	if g.usesSliceRange {
		g.emitSliceRangeRuntime()
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
		g.emitTcpConnectRuntime()
		g.emitTcpAcceptRuntime()
		g.emitTcpRecvRuntime()
		g.emitTcpSendRuntime()
		g.emitTcpCloseRuntime()
		g.emitTcpPollableRuntime()
	}
	if g.usesPoll {
		g.emitPollRuntime()
	}
	if g.usesWasmPollableDrop {
		g.emitWasmPollableDropRuntime()
	}
	if g.usesWasmBlock {
		g.emitWasmBlockRuntime()
	}
	if g.usesWasmTimerPollable {
		g.emitWasmTimerPollableRuntime()
	}
	if g.usesWasmPoll {
		g.emitWasmPollRuntime()
	}
	if g.usesTimerFd {
		g.emitTimerFdRuntime()
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
	if ast.LeakCheckEnabled {
		// Leak detector (#5362 slice 1): the _start epilogue always
		// calls the report, so the helper (and its BSS counters +
		// literal labels, see emitDataSections) is unconditional under
		// the flag — a no-alloc program just reports zeros.
		g.emitLcReportRuntime()
	}
	if g.usesStrBuf {
		g.emitStrBufRuntime()
	}
	if g.usesNowUnixMs {
		g.emitNowUnixMsRuntime()
	}
	if g.usesMonotonicNs {
		g.emitMonotonicNsRuntime()
	}
	if g.usesNowNs {
		g.emitNowNsRuntime()
	}
	if g.usesSleepMs {
		g.emitSleepMsRuntime()
	}
	if g.usesProcFork {
		g.emitProcForkRuntime()
	}
	if g.usesProcWaitpid {
		g.emitProcWaitpidRuntime()
	}
	if g.usesArgs {
		g.emitArgsRuntime()
	}
	if g.usesFloatTranscendentals {
		g.emitFloatTranscendentalsRuntime()
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
	if g.usesRandomI32 {
		g.emitRandomI32Runtime()
	}
	if g.usesAsBytes {
		g.emitStringAsBytesRuntime()
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
	if g.usesRemoveFile {
		g.emitRemoveFileRuntime()
	}
	if g.usesTempDir {
		g.emitTempDirRuntime()
	}
	if g.usesReadDir {
		g.emitReadDirRuntime()
	}
	if g.usesStat {
		g.emitStatRuntime()
	}
	if g.usesRemoveDirAll {
		g.emitRemoveDirAllRuntime()
	}
	if g.usesReaderWriter {
		// Reader / Writer struct constructors (stdin/stdout/
		// stderr + open_reader/writer/appender) + the method
		// runtimes (read_line / read_chunk / close / write).
		// Shares the 4 KiB `__fern_read_line_buf` scratch the
		// stdin-only read_line used, plus __fern_io_error for
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
	g.flushPeep()
	return g.out.String(), nil
}

// emitDataSections writes `.rodata` (interned string literals)
// and `.bss` (the bump-allocator cursor + heap-end sentinel).
// All entries are gated on usage so unused programs pay
// nothing — `.bss` is omitted entirely when the allocator
// isn't pulled in.
func (g *generator) emitDataSections() {
	g.line("")
	// Static `dyn Trait` vtables: one `.rodata` cell per (trait,
	// concrete) pair referenced via OpConstVtable, holding
	// `len(methods)` absolute `__method_*` function pointers in trait
	// declaration order (docs/DYN-TRAITS.md §4.2.2). OpCallDyn loads
	// slot k (`vtable + k*8`) and `blr`s through it. Mirrors the x86-64
	// backend's emission.
	if len(g.dynVtableCells) > 0 {
		if g.darwin {
			// __DATA,__const, NOT __TEXT,__const: the vtable slots are
			// absolute `.quad __method_*` pointers, and pointers in a
			// __TEXT section are TEXT RELOCATIONS — current ld64 rejects
			// them outright ("text-relocation in 'anon-N' ... to
			// '__method_...'", surfaced when the macos-latest runner's
			// Xcode rolled forward, #5055). __DATA,__const keeps them
			// read-only after load while giving the linker a legal
			// place for the fixups.
			g.line(`.section __DATA,__const`)
		} else {
			g.line(`.section .rodata`)
		}
		keys := make([]string, 0, len(g.dynVtableCells))
		for k := range g.dynVtableCells {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		byPair := map[string]ir.VtableDecl{}
		for _, vt := range g.vtables {
			byPair[vt.Trait+"/"+vt.Concrete] = vt
		}
		for _, key := range keys {
			vt, ok := byPair[key]
			if !ok {
				// Should not happen: OpConstVtable only names pairs that
				// collectVtables produced. Emit an empty (but labelled)
				// cell so the link doesn't fail with an undefined symbol.
				g.line(".align 3")
				tr, co := splitPair(key)
				g.label(dynVtableLabel(tr, co))
				continue
			}
			g.line(".align 3")
			g.label(dynVtableLabel(vt.Trait, vt.Concrete))
			for _, m := range vt.Methods {
				g.line(fmt.Sprintf("\t.quad %s", m.Func))
			}
			// Trailing drop slot at index len(Methods) (docs/DYN-TRAITS.md
			// §4.4, slice 4c): the concrete type's drop fn as an absolute
			// pointer, or a null sentinel (0) when it needs none. The boxed
			// __drop_dyn_<set> helper reads this slot and calls it to run the
			// erased concrete destructor before freeing the cell. Appended
			// trailing so the method slot indices (0..n-1) are unchanged —
			// OpCallDyn's slot math is untouched. Mirrors the x86-64 emitter.
			// The `.quad` directive + label refs are identical for Linux ELF
			// and the Mach-O __DATA,__const path above, so this works for
			// both arm64 targets.
			if vt.Drop != "" {
				g.line(fmt.Sprintf("\t.quad %s", vt.Drop))
			} else {
				g.line("\t.quad 0")
			}
		}
	}
	// Static closure-pair cells for OpConstFunc-referenced
	// functions. Each cell holds {fn_ptr (8B), env=0 (8B)}.
	if len(g.constFuncCells) > 0 {
		if g.darwin {
			// __DATA,__const like the vtables above: each cell's
			// `.quad <fn>` is an absolute code pointer, which in a
			// __TEXT section is a text relocation current ld64
			// rejects (#5055 — the `__map_*_keyed` fn-value adapters
			// from the core/map collapse were the first const_func
			// cells a darwin map program emitted, turning every
			// TestArm64DarwinBuilds/map_* case into a link failure).
			g.line(`.section __DATA,__const`)
		} else {
			g.line(`.section .rodata`)
		}
		names := make([]string, 0, len(g.constFuncCells))
		for n := range g.constFuncCells {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			// 8-byte immortal rc header (0x80000000 at cell-8) so
			// the rc_inc/dec helpers short-circuit on these static
			// function-value cells once FuncType locals are
			// rc-tracked. The label still points at the fn_ptr, so
			// OpCallIndirect's [+0]/[+8] reads are unchanged.
			//
			// Darwin: the label MUST be assembler-local ("L" prefix,
			// like the .LStr_* string literals above) — a named
			// (non-L) label starts a new ld64/ld-prime ATOM, and the
			// anonymous 8-byte rc header preceding it belongs to the
			// PREVIOUS atom, so the linker may separate/reorder the
			// header away from its cell. The runtime then reads
			// garbage at [cell-8], the static-sentinel guard misses,
			// and the first rc op on a function value WRITES to the
			// read-only __TEXT,__const page (SIGBUS) — every Map op
			// crashed on arm64-darwin once #5052 made the default
			// hash/eq adapters closure targets.
			g.line(".align 3")
			g.line("\t.4byte 0x80000000") // rc header (static sentinel)
			g.line("\t.4byte 0")          // pad
			g.label(g.closureCellSym(name))
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
		// L2 layout w/ rc-sentinel header (prereq 2): 8-byte header
		// (rc sentinel + length) followed by `.asciz` data. Pointers
		// handed to user code address the data byte (.LStr_N); the
		// 0x80000000 sentinel at data-8 makes __fern_rc_inc/dec
		// short-circuit on literals so the future string-dec can safely
		// run over container-stored / aliased literals without a
		// fragile address-range guard. Mirrors the wasm internString
		// sentinel header and the x86_64 emitDataSections layout.
		//
		// `.asciz` adds a trailing NUL byte; harmless because runtime
		// readers consume length-bounded bytes. Length stored at data-4
		// matches the L2 heap layout; existing OpConstStr lowering still
		// pushes len as a separate operand stack word, so this prefix is
		// forward-compatible (extra source of length for future emitStrLen
		// callers that lose the (data, len) pair).
		g.line(`.align 3`)
		g.line("\t.4byte 0x80000000") // rc sentinel at data-8
		g.line(fmt.Sprintf("\t.4byte %d", len(s)))
		g.label(g.stringLabel[s])
		g.line("\t.asciz " + escapeForGAS(s))
	}
	// Empty-string sentinel. L2 layout w/ rc-sentinel header.
	g.line(`.align 3`)
	g.line("\t.4byte 0x80000000") // rc sentinel at data-8
	g.line("\t.4byte 0")          // length = 0
	g.label(".LStr_Empty")
	g.line(`	.asciz ""`)
	if g.usesArrEmpty {
		// Empty u8[] sentinel — __alloc_u8(0) returns this
		// address instead of allocating a fresh header-only
		// buffer. 16-byte header matches the new Phase 2-prep
		// layout:
		//   [data - 16] = pad
		//   [data - 12] = capacity (= 0)
		//   [data - 8] = rc slot, set to 0x80000000 — the
		//                "static, never touch" sentinel that
		//                __fern_rc_inc / __fern_rc_dec branch
		//                on (high bit set ⇒ no-op).
		//   [data - 4] = length (= 0)
		//   [.LArr_Empty] = data (a single byte for safety)
		// Kept distinct from .LStr_Empty so the array seam can
		// evolve independently of the string seam. See
		// docs/RC-PERCEUS-PLAN.md.
		g.line(`.align 4`)
		g.line(`	.4byte 0`)          // pad
		g.line(`	.4byte 0`)          // cap = 0
		g.line(`	.4byte 0x80000000`) // rc = SENTINEL_STATIC
		g.line(`	.4byte 0`)          // length = 0
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
			// Phase 1e-enums-ii: prepend the 8-byte rc header
			// (rc=0x80000000 at [ptr-8], pad at [ptr-4]) so the
			// rc_inc/dec helpers short-circuit on the high bit once
			// enum-ii widens the dec sweep to enum locals that hold
			// a payloadless variant. The sentinel itself stays a
			// shared read-only static — the header just makes the
			// rc helpers treat it as immortal.
			g.line(`.align 2`)
			g.line(`	.4byte 0x80000000`) // rc header (static sentinel)
			g.line(`	.4byte 0`)          // pad
			g.label(fmt.Sprintf(".LEnumSentinel_%d", t))
			g.line(fmt.Sprintf(`	.4byte %d`, t))
		}
	}
	if g.usesPuts || g.usesEprint {
		// Single newline byte emitted into the same section as
		// the string literals. __fern_puts / __fern_eprint
		// write `s` followed by a 1-byte write of this label.
		// We use `.asciz` rather than `.byte 10` so Mach-O's
		// `cstring_literals` attribute (which requires NUL-
		// terminated strings) accepts the entry — the trailing
		// NUL is harmless, the write only reads the first byte.
		g.label(".LLangNewline")
		g.line(`	.asciz "\n"`)
	}
	if ast.LeakCheckEnabled {
		// Leak detector (#5362 slice 1): the fixed text of
		// __fern_lc_report's summary line. `.asciz` for uniformity with
		// the literals above (and Mach-O friendliness); the report
		// writes exact lengths, so the trailing NULs are never emitted.
		g.label(".Llc_str_allocs")
		g.line(`	.asciz "leakcheck: allocs="`)
		g.label(".Llc_str_frees")
		g.line(`	.asciz " frees="`)
		g.label(".Llc_str_live")
		g.line(`	.asciz " live_bytes="`)
		g.label(".Llc_str_nl")
		g.line(`	.asciz "\n"`)
	}
	if g.usesAlloc || g.usesEnv || g.usesArgs || g.usesReadLine || g.usesStrIdx || g.usesRcDec || g.usesRcUnderflowCount || ast.LeakCheckEnabled {
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
		if ast.LeakCheckEnabled {
			// Leak detector counters (#5362 slice 1). alloc_count /
			// alloc_bytes tick in __fern_alloc (post-16-rounding, both
			// the freelist-pop and bump paths); free_count / free_bytes
			// in __fern_free (same rounding). __fern_lc_report prints
			// them at exit; live_bytes = alloc_bytes − free_bytes.
			g.line(`.align 3`)
			g.label("__fern_lc_alloc_count")
			g.line(`	.quad 0`)
			g.line(`.align 3`)
			g.label("__fern_lc_alloc_bytes")
			g.line(`	.quad 0`)
			g.line(`.align 3`)
			g.label("__fern_lc_free_count")
			g.line(`	.quad 0`)
			g.line(`.align 3`)
			g.label("__fern_lc_free_bytes")
			g.line(`	.quad 0`)
		}
	}
	if g.usesAlloc {
		// Single-cursor bump allocator: `__fern_heap_ptr` /
		// `__fern_heap_end` are the live region's cursors that
		// __fern_alloc bumps. (A second "persistent" cursor + a
		// mode byte used to exist for the removed `state`
		// feature; both are gone.)
		g.line(`.align 3`)
		g.label("__fern_heap_ptr")
		g.line(`	.quad 0`)
		g.line(`.align 3`)
		g.label("__fern_heap_end")
		g.line(`	.quad 0`)
		// Phase 6: region base captured at the lazy mmap so
		// __fern_heap_bump_bytes can report (cursor − base). 0 until
		// the first allocation.
		g.line(`.align 3`)
		g.label("__fern_heap_base")
		g.line(`	.quad 0`)
	}
	if g.usesEnv {
		g.line(`.align 3`)
		g.label("__fern_envp")
		g.line(`	.quad 0`)
	}
	if g.usesArgs {
		g.line(`.align 3`)
		g.label("__fern_argc")
		g.line(`	.quad 0`)
		g.line(`.align 3`)
		g.label("__fern_argv")
		g.line(`	.quad 0`)
		g.line(`.align 3`)
		g.label("__fern_args_cache")
		g.line(`	.quad 0`)
	}
	if g.usesReadLine || g.usesReaderWriter {
		// 4 KiB scratch buffer for the byte-by-byte read loop.
		// Shared by stdin-only `__fern_read_line` and the new
		// Reader-receiving `__fern_reader_read_line`. Both
		// helpers run a single-byte read until '\n' / 4 KiB /
		// EOF, so they can't trample each other.
		g.line(`.align 3`)
		g.label("__fern_read_line_buf")
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
		g.label("__fern_str_idx_scratch")
		g.line(`	.quad 0`)
		g.line(`	.quad 0`)
	}
	if g.usesRcDec || g.usesRcUnderflowCount || g.usesArrDec || g.usesMapDrop {
		// Phase 3 rc-underflow detector counter. __fern_rc_dec
		// bumps it when asked to decrement an rc already <= 0;
		// __fern_rc_underflow_count reads it back. i32 in the low
		// word of an 8-byte aligned slot.
		g.line(`.align 3`)
		g.label("__fern_rc_underflow")
		g.line(`	.quad 0`)
	}
	if ast.RcFreeEnabled && (g.usesAlloc || g.usesFree) {
		// Two-tier segregated freelist heads (256 slots): 0..127 the
		// 16-byte exact-fit small classes (16..2048), 128+b the
		// power-of-two large classes (capacity 2^b). Mirrors the x86_64
		// BSS. Either alloc or free reaches into it (alloc pops, free
		// pushes), so emit when EITHER is used — without this the
		// arm64 string-LOCAL freeEligible widening (str_dec →
		// box_free → free) referenced freelist_heads without
		// usesAlloc, link-failing on programs that string-concat
		// but never explicitly alloc otherwise.
		g.line(`.align 3`)
		g.label("__fern_freelist_heads")
		g.line(`	.space 2048`)
		// Shadow copy for the one-level arena checkpoint
		// (__fern_heap_mark / __fern_heap_release_to). Mirrors x86_64.
		g.label("__fern_freelist_shadow")
		g.line(`	.space 2048`)
	}
}

// emitAllocRuntime emits `__fern_alloc(size: i64) -> i64`
// using mmap2 (sysMmap = 222) and 64-bit pointer arithmetic.
// First call lazily reserves the heap arena via mmap; later
// calls bump the cursor.
//
// Single-cursor bump allocator: every allocation bumps the one
// `__fern_heap_ptr` / `__fern_heap_end` pair. A second
// "persistent" region (selected by a `__fern_alloc_mode` byte)
// used to exist so `state`-rooted allocations survived the
// per-request arena reset; both the `state` feature and the
// arena reset have since been removed, so the mode flag and the
// persistent cursors are gone and there is nothing to select.
//
// The region is a lazy-mmap'd virtual reservation at hint
// 0x10000000 (fits in 32 bits so the stdlib's
// __store_i32 / __load_i32 round-trip pointers without
// truncation).
//
// abortMessages are the fatal-abort diagnostics (#5538): a fixed message
// written to stderr before the process exits, so a bounds / arena / slice
// abort names its cause instead of exiting with a bare code. The text MUST
// match the x86-64 backend's table (internal/codegen/x86_64) so a program's
// abort output is identical across natives. Ordered (not a map) for
// deterministic emission.
var abortMessages = []struct {
	label, text string
	code        int
}{
	{"__fern_msg_arr_oob", "fern: array index out of range\n", 134},
	{"__fern_msg_slice_oob", "fern: slice index out of range\n", 134},
	{"__fern_msg_oom", "fern: out of memory (heap arena exhausted)\n", 137},
	{"__fern_msg_slice_range", "fern: slice range out of bounds\n", 134},
	{"__fern_msg_str_slice", "fern: string index out of range\n", 134},
}

func abortMsg(label string) (text string, code int) {
	for _, m := range abortMessages {
		if m.label == label {
			return m.text, m.code
		}
	}
	panic("arm64: unknown abort message " + label)
}

// emitAbort routes a fatal abort site through __fern_report: it points x1/x2
// at the named diagnostic, sets the exit code in x0, and tail-branches to the
// reporter (write to stderr, then exit). Replaces a bare, silent
// `mov x0, #code; syscallExit` so the failure names its cause (#5538).
func (g *generator) emitAbort(label string) {
	text, code := abortMsg(label)
	g.adrpAdd("x1", label) // x1 = message ptr
	g.emit("mov x2, #%d", len(text))
	g.emit("mov x0, #%d", code)
	g.emit("b __fern_report")
}

// emitAbortRuntime emits __fern_report — write(stderr, msg, len) then exit —
// plus the abort message strings. Emitted unconditionally (once): the bounds /
// arena / slice sites branch here instead of exiting silently (#5538). x19
// (callee-saved, kernel-preserved across the write syscall) holds the exit
// code; the reporter never returns so no frame is needed.
const abortBacktraceMsg = "backtrace:\n"

func (g *generator) emitAbortRuntime() {
	g.line("")
	g.line(".global __fern_report")
	g.typeDirective("__fern_report")
	g.label("__fern_report") // x1 = msg ptr, x2 = length, x0 = exit code
	g.emit("mov x19, x0")    // stash exit code (x19 survives the writes; we never return)
	g.emit("mov x0, #2")     // fd = stderr; x1/x2 already set by emitAbort
	g.syscall("write")
	// Backtrace (#5538): walk the x29 frame-pointer chain and print each
	// return address (the saved x30 at [fp+8]) in hex. With `-g` (the
	// .symtab) they resolve to functions via addr2line / nm. Bounded to 64
	// frames; terminates at x29 == 0.
	g.adrpAdd("x1", "__fern_msg_bt")
	g.emit("mov x2, #%d", len(abortBacktraceMsg))
	g.emit("mov x0, #2")
	g.syscall("write")
	g.emit("mov x20, x29") // x20 = frame pointer (callee-saved, survives the bl)
	g.emit("mov x21, #64") // frame budget
	g.label(".Lbt_loop")
	g.emit("cbz x20, .Lbt_done")
	g.emit("cbz x21, .Lbt_done")
	g.emit("ldr x0, [x20, #8]") // return address
	g.emit("cbz x0, .Lbt_done")
	g.emit("bl __fern_print_hex")
	g.emit("ldr x20, [x20]") // next frame
	g.emit("sub x21, x21, #1")
	g.emit("b .Lbt_loop")
	g.label(".Lbt_done")
	g.emit("mov x0, x19")
	g.syscallExit()
	g.sizeDirective("__fern_report")
	g.line(".ltorg")

	// __fern_print_hex(x0 = value) writes "  0x<16 hex>\n" to stderr. A leaf
	// (only the write syscall) using x0..x12, so the caller's x19/x20/x21
	// survive. Nibbles come off the low end via lsr, written right-to-left.
	g.line("")
	g.line(".global __fern_print_hex")
	g.typeDirective("__fern_print_hex")
	g.label("__fern_print_hex")
	g.emit("sub sp, sp, #32")
	g.emit("mov w9, #32")
	g.emit("strb w9, [sp]")     // ' '
	g.emit("strb w9, [sp, #1]") // ' '
	g.emit("mov w9, #48")
	g.emit("strb w9, [sp, #2]") // '0'
	g.emit("mov w9, #120")
	g.emit("strb w9, [sp, #3]") // 'x'
	g.emit("mov w9, #10")
	g.emit("strb w9, [sp, #20]") // '\n'
	g.emit("mov w10, #16")       // digit counter
	g.emit("add x11, sp, #19")   // last hex slot
	g.label(".Lph_loop")
	g.emit("and x12, x0, #0xf") // low nibble
	g.emit("cmp x12, #10")
	g.emit("b.lo .Lph_dec")
	g.emit("add x12, x12, #87") // 'a' - 10
	g.emit("b .Lph_put")
	g.label(".Lph_dec")
	g.emit("add x12, x12, #48") // '0'
	g.label(".Lph_put")
	g.emit("strb w12, [x11]")
	g.emit("sub x11, x11, #1")
	g.emit("lsr x0, x0, #4")
	g.emit("sub w10, w10, #1")
	g.emit("cbnz w10, .Lph_loop")
	g.emit("mov x1, sp") // buf
	g.emit("mov x2, #21")
	g.emit("mov x0, #2") // stderr
	g.syscall("write")
	g.emit("add sp, sp, #32")
	g.emit("ret")
	g.sizeDirective("__fern_print_hex")
	g.line(".ltorg")

	// Read-only message strings. Mach-O has no `.rodata`; its read-only
	// constants live in __TEXT,__const (matching emitDataSections).
	if g.darwin {
		g.line(".section __TEXT,__const")
	} else {
		g.line(".section .rodata")
	}
	for _, m := range abortMessages {
		g.label(m.label)
		g.line("\t.asciz " + escapeForGAS(m.text))
	}
	g.label("__fern_msg_bt")
	g.line("\t.asciz " + escapeForGAS(abortBacktraceMsg))
	g.line(".text")
}

// Bump-only at the cursor level — freed blocks return to the
// segregated freelist (see __fern_free) when RcFreeEnabled; the
// OS reclaims everything at process exit regardless.
func (g *generator) emitAllocRuntime() {
	// 512 MiB per region — matches the x86 backend's heap size so
	// the arm64 native self-host driver can compile programs
	// with large stdlib transitive imports (e.g. std/json +
	// std/sort + std/array + std/string + core/int) without
	// running out and exiting 137 mid-parse. Same shape as x86:
	// lazy-mmap'd, bump-only, OS-reclaims-on-exit. The address
	// space is reserved up front but pages only commit as
	// they're touched, so the wider window costs nothing on
	// programs that don't grow into it.
	// 2.5 GiB (was 1.75 GiB, was 1 GiB, was 512 MiB) so a cmd/fern-built
	// self-host compiler can bootstrap-compile the whole self-host source in
	// one process. arm64 needs MORE headroom than x86 here: it emits longer
	// asm per IR op (literal-pool `ldr =N` loads, 16-byte operand-stack
	// pushes), so its self-compile live set runs higher than x86's for the
	// same bundle — at 1.75 GiB (which x86 still clears) the arm64 stage-2
	// fixpoint tipped into the exit-137 alloc trap as the IR subset widened.
	// Matches asm_arm64.fern's own heap_size so the native (stage-0 mmc) and
	// self-host (stage-1+ gen) arm64 heaps stay in lockstep. Raised to 8 GiB
	// when the arm64 stage-2 self-compile crossed the 3.5 GiB exit-137 alloc
	// trap and its ~4.1 GiB live set outgrew every arena a 32-bit-safe
	// pointer range can hold — the historical "base+size < 4 GiB so 32-bit
	// pointers round-trip" guideline is retired; the value plumbing is
	// 64-bit throughout (x-registers, 8-byte slots — arm64-darwin's high
	// heap already exercises >32-bit heap pointers). The hint stays at
	// 0x10000000: the below-heap rc guards key on it (see the lsl below).
	// Lazy-mapped via a literal-pool load, so the wider window costs
	// nothing until touched and has no 32-bit-immediate limit. Raised to
	// 16 GiB when the arm64 stage-2 self-compile crossed the 8 GiB trap
	// again (measured need ~8.2 GiB at tip; the compiler's live set grows
	// with every compiler-source addition). MAP_NORESERVE makes the wider
	// reservation free: without it Linux's overcommit heuristic refuses
	// the single anonymous map outright on hosts with RAM+swap below the
	// arena size (a 16 GiB map would fail AT STARTUP on a 16 GB host with
	// no swap — and the old 8 GiB map already could not start on
	// small-RAM boards like a 4 GB Raspberry Pi). The exit-137 bounds
	// check in the allocator remains the real out-of-memory guard.
	const heapBytes = 17179869184 // 0x400000000, 16 GiB
	g.line("")
	g.line(".global __fern_alloc")
	g.typeDirective("__fern_alloc")
	g.label("__fern_alloc")
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	g.emit("add x0, x0, #15")
	g.emit("and x0, x0, #-16")
	if ast.LeakCheckEnabled {
		// Leak detector (#5362 slice 1): count every allocation — the
		// freelist-pop and bump paths both flow through here — at the
		// 16-rounded size, the same rounding __fern_free's counter
		// applies, so a block's alloc and eventual free cancel exactly.
		// (The large tier's further capacity round-up is deliberately
		// NOT counted: free is called with the logical size and never
		// sees it.) x9/x10 are scratch here — the mmap path's x9 stash
		// happens later.
		g.adrpAdd("x9", "__fern_lc_alloc_count")
		g.emit("ldr x10, [x9]")
		g.emit("add x10, x10, #1")
		g.emit("str x10, [x9]")
		g.adrpAdd("x9", "__fern_lc_alloc_bytes")
		g.emit("ldr x10, [x9]")
		g.emit("add x10, x10, x0")
		g.emit("str x10, [x9]")
	}
	if ast.RcFreeEnabled {
		// Two-tier segregated freelist (arm64 mirror of the x86_64 helper).
		// Small tier (16..2048 B): 16-byte exact-fit classes 0..127,
		// idx = (x0>>4)-1. Large tier (>2048 B): round the request up to the
		// next power of two (the bytes actually bumped) and bin by that
		// power's bit position + 128 — so reuse tolerates the size variance
		// exact-fit can't (a 12 KiB and 13 KiB array share the 16 KiB class).
		// Blocks >1 GiB stay bump-only so the class index can't run off the
		// 256-slot heads array.
		g.emit("cmp x0, #16")
		g.emit("b.lo .Lalloc_bump")
		g.emit("cmp x0, #2048")
		g.emit("b.hi .Lalloc_large")
		g.emit("lsr x1, x0, #4")
		g.emit("sub x1, x1, #1") // small class index 0..127
		g.emit("b .Lalloc_fltry")
		g.label(".Lalloc_large")
		g.emit("mov x5, #1")
		g.emit("lsl x5, x5, #30") // x5 = 1 GiB
		g.emit("cmp x0, x5")
		g.emit("b.hi .Lalloc_bump")
		// Round the request UP to 3 significant bits (1 leading + 2 mantissa)
		// — ≤25% internal waste vs ≤2x. Grid spacing at 2^e is 2^(e-2);
		// e = bsr(size) = 63 - clz(size). Bump the rounded capacity (x0) and
		// derive the class from it so alloc and free agree.
		g.emit("clz x2, x0")
		g.emit("mov x3, #63")
		g.emit("sub x3, x3, x2") // x3 = e = bsr(size)
		g.emit("sub x4, x3, #2") // x4 = e-2 = grid-spacing exponent
		g.emit("mov x6, #1")
		g.emit("lsl x6, x6, x4") // x6 = gran = 1<<(e-2)
		g.emit("add x1, x0, x6")
		g.emit("sub x1, x1, #1") // x1 = size + gran - 1
		g.emit("neg x7, x6")     // x7 = -gran
		g.emit("and x0, x1, x7") // x0 = cap = roundup(size, gran) = bytes to bump
		// class = (e2-11)*4 + (mant-4) + 128 = 4*e2 + mant + 80, e2 = bsr(cap),
		// mant = cap>>(e2-2) ∈ {4,5,6,7}. e2 recomputed so a carry into a new
		// power of two bins right.
		g.emit("clz x2, x0")
		g.emit("mov x3, #63")
		g.emit("sub x3, x3, x2") // x3 = e2
		g.emit("sub x4, x3, #2") // x4 = e2-2
		g.emit("lsr x1, x0, x4") // x1 = mant = cap>>(e2-2)
		g.emit("sub x3, x3, #11")
		g.emit("lsl x3, x3, #2") // x3 = (e2-11)*4
		g.emit("add x1, x1, x3")
		g.emit("add x1, x1, #124") // x1 = large class index
		g.label(".Lalloc_fltry")
		g.adrpAdd("x2", "__fern_freelist_heads")
		g.emit("ldr x3, [x2, x1, lsl #3]") // head = heads[idx]
		g.emit("cbz x3, .Lalloc_bump")
		g.emit("ldr x4, [x3]")             // head.next
		g.emit("str x4, [x2, x1, lsl #3]") // heads[idx] = next
		g.emit("mov x0, x3")               // return reused block
		g.emit("ldp x29, x30, [sp], #16")
		g.emit("ret")
		g.label(".Lalloc_bump")
	}
	// Single bump region: x11/x12 = heap cursor/end.
	g.adrpAdd("x11", "__fern_heap_ptr")
	g.adrpAdd("x12", "__fern_heap_end")
	g.emit("mov x13, #1") // mmap hint base = 0x1000_0000 (lsl #28 below)
	g.emit("ldr x2, [x11]")
	g.emit("cbnz x2, .Lalloc_have_heap")
	// Lazy mmap. x13 carries the address-hint base (1 or 2).
	g.emit("mov x9, x0")
	g.emit("lsl x0, x13, #28") // x0 = hint << 28 = 0x1000_0000 — MUST stay 0x10000000: the below-heap guards (emitRcInc/RcDec/rcop) classify `ptr >= 0x10000000` as heap-allocated, so a lower arena base silently no-ops every rc inc/dec (learned the hard way: an 0x04000000 hint corrupted COW/alias semantics across the whole native arm64 fixture suite)
	g.emit("ldr x1, =%d", heapBytes)
	g.emit("mov x2, #3")
	if g.darwin {
		// Darwin: MAP_ANON|MAP_PRIVATE. No NORESERVE needed — macOS does
		// not strictly account anonymous private reservations.
		g.emit("mov x3, #0x1002")
	} else {
		// MAP_PRIVATE|MAP_ANONYMOUS|MAP_NORESERVE (0x22|0x4000) — see the
		// heapBytes comment: exempts the big lazy arena from Linux's
		// overcommit accounting so reservation size never blocks startup.
		g.emit("mov x3, #0x4022")
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
	// Phase 6: record the region base for __fern_heap_bump_bytes.
	g.adrpAdd("x14", "__fern_heap_base")
	g.emit("str x10, [x14]")
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
	g.emitAbort("__fern_msg_oom")
	g.sizeDirective("__fern_alloc")
	g.line(".ltorg")
}

// emitFreeRuntime emits `__fern_free(base, size)` — the Phase 3
// step-4 freelist return path (arm64 mirror of the x86_64 helper).
// Pushes the size-byte block at base onto its 16-byte size class's
// intrusive freelist (successor pointer in the block's first 8
// bytes). Blocks outside 16..2048 stay in the bump region. A no-op
// when the freelist is disabled. AAPCS64: x0 = base, x1 = size.
// Leaf; no frame.
func (g *generator) emitFreeRuntime() {
	g.line("")
	g.line(".global __fern_free")
	g.typeDirective("__fern_free")
	g.label("__fern_free")
	if ast.LeakCheckEnabled {
		// Leak detector (#5362 slice 1): every reclamation site funnels
		// through this helper (box_free / arr_dec / map_drop /
		// drop_arr_ptr / drop_arr_str / alloc_reuse's mismatch path /
		// the __free builtin — the freelist push below is the only
		// other freelist writer and it's in this same function), so
		// counting here covers them all. Count at the same
		// (size+15)&-16 rounding __fern_alloc counted, in scratch regs
		// so the RcFreeEnabled body's own rounding of x1 is untouched
		// (and the counters still tick when the freelist is compiled
		// out). __fern_alloc_reuse's in-place path calls neither
		// __fern_alloc nor __fern_free — in-place reuse counts as
		// NEITHER an alloc nor a free, which is exact: its class match
		// requires equal rounded sizes, so the block's original alloc
		// count still cancels against its eventual free.
		g.emit("add x9, x1, #15")
		g.emit("and x9, x9, #-16")
		g.adrpAdd("x10", "__fern_lc_free_count")
		g.emit("ldr x11, [x10]")
		g.emit("add x11, x11, #1")
		g.emit("str x11, [x10]")
		g.adrpAdd("x10", "__fern_lc_free_bytes")
		g.emit("ldr x11, [x10]")
		g.emit("add x11, x11, x9")
		g.emit("str x11, [x10]")
	}
	if ast.RcFreeEnabled {
		g.emit("add x1, x1, #15")
		g.emit("and x1, x1, #-16")
		g.emit("cmp x1, #16")
		g.emit("b.lo .Lfree_ret")
		g.emit("cmp x1, #2048")
		g.emit("b.hi .Lfree_large")
		g.emit("lsr x2, x1, #4")
		g.emit("sub x2, x2, #1") // small class index 0..127
		g.emit("b .Lfree_push")
		g.label(".Lfree_large")
		// Mirror __fern_alloc's large tier exactly: round the logical size up
		// to 3 significant bits and bin by the rounded capacity. >1 GiB is
		// dropped (alloc never freelisted it).
		g.emit("mov x5, #1")
		g.emit("lsl x5, x5, #30")
		g.emit("cmp x1, x5")
		g.emit("b.hi .Lfree_ret")
		g.emit("clz x3, x1")
		g.emit("mov x4, #63")
		g.emit("sub x4, x4, x3") // x4 = e
		g.emit("sub x6, x4, #2") // x6 = e-2
		g.emit("mov x7, #1")
		g.emit("lsl x7, x7, x6") // x7 = gran
		g.emit("add x2, x1, x7")
		g.emit("sub x2, x2, #1")
		g.emit("neg x7, x7")
		g.emit("and x2, x2, x7") // x2 = cap
		g.emit("clz x3, x2")
		g.emit("mov x4, #63")
		g.emit("sub x4, x4, x3") // x4 = e2
		g.emit("sub x6, x4, #2") // x6 = e2-2
		g.emit("lsr x3, x2, x6") // x3 = mant
		g.emit("sub x4, x4, #11")
		g.emit("lsl x4, x4, #2") // x4 = (e2-11)*4
		g.emit("add x3, x3, x4")
		g.emit("add x2, x3, #124") // x2 = large class index
		g.label(".Lfree_push")
		g.adrpAdd("x3", "__fern_freelist_heads")
		g.emit("ldr x4, [x3, x2, lsl #3]") // old head
		g.emit("str x4, [x0]")             // base.next = old head
		g.emit("str x0, [x3, x2, lsl #3]") // heads[idx] = base
		g.label(".Lfree_ret")
	}
	g.emit("ret")
	g.sizeDirective("__fern_free")
	g.line(".ltorg")
}

// emitAllocReuseRuntime emits
// `__fern_alloc_reuse(token, tokenSize, size) -> ptr` — the Phase 5
// drop-reuse (FBIP) primitive (arm64 mirror of the x86_64 helper).
// On a live token whose 16-byte size class matches `size`'s, returns
// the token in place (no free, no alloc); on a null token or class
// mismatch, frees the (non-null) dropped block and allocates afresh,
// so a mispaired reuse is slow-not-wrong. Class arithmetic mirrors
// __fern_alloc / __fern_free ((sz+15)&-16, exact-fit 16..2048).
// AAPCS64: x0 = token, x1 = tokenSize, x2 = size. Leaf except on the
// mismatch path (it calls __fern_free, so it frames there); the reuse
// and fresh-alloc paths tail into __fern_alloc.
func (g *generator) emitAllocReuseRuntime() {
	g.line("")
	g.line(".global __fern_alloc_reuse")
	g.typeDirective("__fern_alloc_reuse")
	g.label("__fern_alloc_reuse")
	g.emit("cbz x0, .Lreuse_fresh") // null token → plain alloc(size)
	// class(tokenSize) in x3, class(size) in x4
	g.emit("add x3, x1, #15")
	g.emit("and x3, x3, #-16")
	g.emit("add x4, x2, #15")
	g.emit("and x4, x4, #-16")
	g.emit("cmp x3, x4")
	g.emit("b.ne .Lreuse_mismatch")
	// Classes match: reuse in place — x0 already holds token.
	g.emit("ret")
	g.label(".Lreuse_mismatch")
	// Free the dropped block (x0=token, x1=tokenSize), preserving
	// size (x2) and the return address across the call.
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	g.emit("str x2, [sp, #-16]!") // save size (keep 16-align)
	g.emit("bl __fern_free")
	g.emit("ldr x2, [sp], #16") // restore size
	g.emit("ldp x29, x30, [sp], #16")
	g.label(".Lreuse_fresh")
	g.emit("mov x0, x2")     // size
	g.emit("b __fern_alloc") // tail call
	g.sizeDirective("__fern_alloc_reuse")
	g.line(".ltorg")
}

// emitArrDecRuntime emits `__fern_arr_dec(data, stride)` — the
// Phase 3 step-4 size-aware array dec (arm64 mirror of the x86_64
// helper). Decrements the array's rc and, on the last reference
// (rc==1), returns the BUFFER to the freelist (base = data -
// headerBytes, headerBytes = max(16, stride), size = headerBytes +
// cap*stride; cap at data-12) — it does NOT walk elements. Same
// null / low-address / sentinel / underflow guards as
// __fern_rc_dec. Only emitted/used when the flag is on; the return
// value is discarded by the caller's OpDrop.
func (g *generator) emitArrDecRuntime() {
	g.line("")
	g.line(".global __fern_arr_dec")
	g.typeDirective("__fern_arr_dec")
	g.label("__fern_arr_dec")
	g.emit("stp x29, x30, [sp, #-16]!") // frame for __fern_free call
	g.emit("mov x29, sp")
	g.emit("cbz x0, .Larrdec_ret")
	g.emit("cmp x0, #0x10000")
	g.emit("b.lo .Larrdec_ret")
	g.emit("ldur w2, [x0, #-8]") // rc
	g.emit("tbnz w2, #31, .Larrdec_ret")
	g.emit("cmp w2, #0")
	g.emit("b.gt .Larrdec_pos")
	g.adrpAdd("x3", "__fern_rc_underflow")
	g.emit("ldr w4, [x3]")
	g.emit("add w4, w4, #1")
	g.emit("str w4, [x3]")
	g.emit("b .Larrdec_dec")
	g.label(".Larrdec_pos")
	g.emit("cmp w2, #1")
	g.emit("b.ne .Larrdec_dec")
	// rc == 1 → free the buffer.
	g.emit("mov x3, #16")
	g.emit("cmp x1, #16")
	g.emit("csel x3, x1, x3, hi") // headerBytes = max(16, stride)
	g.emit("ldur w4, [x0, #-12]") // cap
	g.emit("mul x5, x4, x1")      // cap * stride
	g.emit("add x5, x5, x3")      // + headerBytes = size
	g.emit("sub x0, x0, x3")      // base = data - headerBytes (arg0)
	g.emit("mov x1, x5")          // size (arg1)
	g.emit("bl __fern_free")
	g.emit("b .Larrdec_ret")
	g.label(".Larrdec_dec")
	g.emit("sub w2, w2, #1")
	g.emit("stur w2, [x0, #-8]")
	g.label(".Larrdec_ret")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.sizeDirective("__fern_arr_dec")
	g.line(".ltorg")
}

// emitMapDropRuntime emits `__fern_map_drop(m) -> m` — the Phase 3
// map reclamation handler (arm64 mirror of the x86_64 helper). A Map
// handle `m` has its rc at [m-8] and its buf pointer at [m+0]. On the
// last reference (rc==1) the handle's storage returns to the freelist:
// the buf (size = 16 + cap*(4+entryStride), cap at [buf+0],
// entryStride = 2*ptrW = 16) then the 16-byte handle cell (base =
// m-8). Entry keys/values are NOT walked — their accounting is
// untouched (they leak, as before). On rc>1 the handle is dec'd. Same
// null / low-address / sentinel / underflow guards as __fern_arr_dec.
// m is held in x19 (callee-saved) across the two __fern_free calls.
func (g *generator) emitMapDropRuntime() {
	g.line("")
	g.line(".global __fern_map_drop")
	g.typeDirective("__fern_map_drop")
	g.label("__fern_map_drop")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("mov x19, x0") // x19 = m
	g.emit("cbz x19, .Lmapdrop_ret")
	g.emit("cmp x19, #0x10000")
	g.emit("b.lo .Lmapdrop_ret")
	g.emit("ldur w1, [x19, #-8]") // rc
	g.emit("tbnz w1, #31, .Lmapdrop_ret")
	g.emit("cmp w1, #0")
	g.emit("b.gt .Lmapdrop_pos")
	g.adrpAdd("x2", "__fern_rc_underflow")
	g.emit("ldr w3, [x2]")
	g.emit("add w3, w3, #1")
	g.emit("str w3, [x2]")
	g.emit("b .Lmapdrop_dec")
	g.label(".Lmapdrop_pos")
	g.emit("cmp w1, #1")
	g.emit("b.ne .Lmapdrop_dec")
	// rc == 1 → free buf then the handle cell.
	g.emit("ldr x4, [x19]") // buf
	g.emit("cbz x4, .Lmapdrop_freehandle")
	g.emit("cmp x4, #0x10000")
	g.emit("b.lo .Lmapdrop_freehandle")
	g.emit("ldr w5, [x4]")    // cap (zero-extended)
	g.emit("mov x6, #20")     // 4 + entryStride(16)
	g.emit("mul x5, x5, x6")  // cap * 20
	g.emit("add x1, x5, #16") // + 16-byte header = size (arg1)
	g.emit("mov x0, x4")      // base = buf (arg0)
	g.emit("bl __fern_free")
	g.label(".Lmapdrop_freehandle")
	g.emit("sub x0, x19, #8") // handle base = m - 8
	g.emit("mov x1, #16")     // handle size
	g.emit("bl __fern_free")
	g.emit("b .Lmapdrop_ret")
	g.label(".Lmapdrop_dec")
	g.emit("ldur w1, [x19, #-8]")
	g.emit("sub w1, w1, #1")
	g.emit("stur w1, [x19, #-8]")
	g.label(".Lmapdrop_ret")
	g.emit("mov x0, x19")
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__fern_map_drop")
	g.line(".ltorg")
}

// emitBoxFreeRuntime emits `__fern_box_free(data, size) -> data` — the
// Phase 3 struct/enum box reclamation helper (arm64 mirror). The IR
// pre-gates the call on rc==1 and has already dropped the box's
// rc-tracked fields/payloads, so this just returns the box (base =
// data - 8 rc header, freed size = size + 8) to the freelist and
// returns data (the uniform-result shape the IR OpDrop relies on).
// NULL / low-address guards keep a stray call safe. data is held in
// x19 across the __fern_free call.
func (g *generator) emitBoxFreeRuntime() {
	g.line("")
	g.line(".global __fern_box_free")
	g.typeDirective("__fern_box_free")
	g.label("__fern_box_free")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("mov x19, x0") // x19 = data (default return)
	g.emit("cbz x19, .Lboxfree_ret")
	g.emit("cmp x19, #0x10000")
	g.emit("b.lo .Lboxfree_ret")
	g.emit("add x1, x1, #8")  // size + 8 rc header (arg1)
	g.emit("sub x0, x19, #8") // base = data - 8 (arg0)
	g.emit("bl __fern_free")
	g.label(".Lboxfree_ret")
	g.emit("mov x0, x19")
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__fern_box_free")
	g.line(".ltorg")
}

// emitClosureDropRuntime emits `__fern_closure_drop(f) -> f` — the
// closure env/pair reclamation handler (arm64 mirror of x86-64). On
// the last reference (rc==1) it frees the rc1 block via
// __fern_box_free (payload size stashed at data-4 by
// __fern_alloc_rc1); otherwise (rc>1 or the static high-bit
// sentinel) it tail-calls __fern_rc_dec (sentinel / underflow
// guards). NULL / low-address guarded. No frame — both arms are
// tail-calls, so f flows through in x0. Captured pointer targets
// (and a pair's env) leak for now, the same one-level reclamation
// as the other drop helpers.
func (g *generator) emitClosureDropRuntime() {
	g.line("")
	g.line(".global __fern_closure_drop")
	g.typeDirective("__fern_closure_drop")
	g.label("__fern_closure_drop")
	g.emit("cmp x0, #0x10000") // null + low-address guard
	g.emit("b.lo .Lcd_ret")
	g.emit("ldur w1, [x0, #-8]") // rc
	g.emit("cmp w1, #1")
	g.emit("b.ne .Lcd_dec")      // rc != 1 (shared, or static sentinel) → dec
	g.emit("ldur w1, [x0, #-4]") // rc==1: payload size → arg2
	g.emit("b __fern_box_free")  // tail-call box_free(x0=data, x1=size) -> x0
	g.label(".Lcd_dec")
	g.emit("b __fern_rc_dec") // tail-call rc_dec(x0) -> x0
	g.label(".Lcd_ret")
	g.emit("ret")
	g.sizeDirective("__fern_closure_drop")
	g.line(".ltorg")
}

// emitStrIncRuntime emits `__fern_str_inc(data, len) -> (data, len)` —
// two-word string retain. Inline-tagged values (top bit of len) are
// no-ops returning the pair unchanged; heap strings tail-call
// __fern_rc_inc on data (which short-circuits on null / low-address /
// static-sentinel). arm64 port of the wasm helper.
func (g *generator) emitStrIncRuntime() {
	g.line("")
	g.line(".global __fern_str_inc")
	g.typeDirective("__fern_str_inc")
	g.label("__fern_str_inc")
	// Inline tag: bit 63 of x1 set ⇒ packed bytes, no heap.
	g.emit("tbnz x1, #63, .Lstrinc_ret")
	// Heap path: inc the rc at [x0-8]. The rc bump is inlined here
	// (rather than tail-calling __fern_rc_inc) because __fern_rc_inc
	// uses x1 as scratch and would clobber the length — and str_inc
	// must return the (data, len) pair intact in (x0, x1). A garbled
	// length flows straight into the next strcat / store and either
	// SIGSEGVs or triggers a multi-gigabyte alloc. Use w2 as scratch
	// so x1 survives. Mirrors __fern_rc_inc's null / low-bit-tag /
	// static-sentinel short-circuits.
	g.emit("cbz x0, .Lstrinc_ret")
	g.emit("tbnz x0, #0, .Lstrinc_ret")
	g.emit("ldur w2, [x0, #-8]")
	g.emit("tbnz w2, #31, .Lstrinc_ret")
	g.emit("add w2, w2, #1")
	g.emit("stur w2, [x0, #-8]")
	g.label(".Lstrinc_ret")
	g.emit("ret")
	g.sizeDirective("__fern_str_inc")
}

// emitStrDecRuntime emits `__fern_str_dec(data, len) -> data` —
// two-word string reclaim. Inline-tagged values are no-ops. Heap
// strings: at rc==1 tail-call __fern_box_free(data, payload_size_at_data-4);
// otherwise (rc>1 or static high-bit sentinel) tail-call __fern_rc_dec.
// NULL / low-address guarded. arm64 port of the wasm helper.
func (g *generator) emitStrDecRuntime() {
	g.line("")
	g.line(".global __fern_str_dec")
	g.typeDirective("__fern_str_dec")
	g.label("__fern_str_dec")
	g.emit("tbnz x1, #63, .Lstrdec_ret")
	g.emit("cbz x0, .Lstrdec_ret")
	g.emit("cmp x0, #0x10000")
	g.emit("b.lo .Lstrdec_ret")
	g.emit("ldur w2, [x0, #-8]") // rc
	g.emit("cmp w2, #1")
	g.emit("b.ne .Lstrdec_dec")
	// rc == 1: box_free(data, payload size at data-4).
	g.emit("ldur w1, [x0, #-4]")
	g.emit("b __fern_box_free")
	g.label(".Lstrdec_dec")
	g.emit("b __fern_rc_dec")
	g.label(".Lstrdec_ret")
	g.emit("ret")
	g.sizeDirective("__fern_str_dec")
}

// emitCellFreeRuntime emits `__fern_cell_free(cell) -> cell` —
// returns a 16-byte boxed (data, len) cell to the freelist. NULL /
// low-address guarded; otherwise __fern_free(cell, 16). x19 saves the
// cell across the bl so we can return it. arm64 port of the wasm helper.
func (g *generator) emitCellFreeRuntime() {
	g.line("")
	g.line(".global __fern_cell_free")
	g.typeDirective("__fern_cell_free")
	g.label("__fern_cell_free")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("mov x19, x0") // x19 = cell (default return)
	g.emit("cbz x19, .Lcellfree_ret")
	g.emit("cmp x19, #0x10000")
	g.emit("b.lo .Lcellfree_ret")
	g.emit("mov x0, x19")
	g.emit("mov x1, #16")
	g.emit("bl __fern_free")
	g.label(".Lcellfree_ret")
	g.emit("mov x0, x19")
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__fern_cell_free")
	g.line(".ltorg")
}

// emitCCallRuntime emits `__c_call<n>(fn, a0..a{n-1})` — the FFI shim that
// invokes a C-ABI (AAPCS64) function pointer. Fern uses the same integer-arg
// registers (x0..x3), so the shim drops the leading `fn` argument: save fn in
// x9, slide each real arg down one register (x1->x0, …), and tail-branch to
// fn. x30 still holds the Fern caller's return address, so fn's `ret` returns
// there with the result already in x0.
func (g *generator) emitCCallRuntime(n int) {
	g.emitCCallRuntimeSuffixed(n, "")
}

// emitCCallRuntimeSuffixed is emitCCallRuntime parameterised by a return-type
// suffix ("" / "_f32" / "_f64"). The shim body is identical regardless of
// return type — it's a tail branch, so the callee's result lands in whichever
// register its ABI dictates (x0 for integer, d0/v0 for f32/f64) and flows
// straight back to the Fern caller. Only the symbol name differs, so a
// distinct checker FuncSig can declare the FP result type (making the call
// site read the FP register). Mirrors the x86-64 emitter.
func (g *generator) emitCCallRuntimeSuffixed(n int, suffix string) {
	name := fmt.Sprintf("__c_call%d%s", n, suffix)
	g.line("")
	g.line(".global " + name)
	g.typeDirective(name)
	g.label(name)
	g.emit("mov x9, x0") // x9 = fn
	for i := 0; i < n; i++ {
		g.emit("mov x%d, x%d", i, i+1) // slide a{i} down
	}
	g.emit("br x9") // tail-call fn; its ret returns to our caller, result in x0/d0
}

// emitMemcpyRuntime emits `__fern_memcpy(dst, src, n)` —
// byte-grain copy. Word-grain bulk path runs in 8-byte chunks
// since arm64 has 64-bit registers; tail loop handles the
// residue. Pointers may be unaligned (arm64 allows unaligned
// access by default in user-mode Linux).
func (g *generator) emitMemcpyRuntime() {
	g.line("")
	g.line(".global __fern_memcpy")
	g.typeDirective("__fern_memcpy")
	g.label("__fern_memcpy")
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
	g.sizeDirective("__fern_memcpy")
	g.line(".ltorg")
}

// emitRcIncRuntime emits `__fern_rc_inc(ptr) -> ptr` —
// increment the refcount at `[ptr - 8]` and return the input
// pointer unchanged. NULL-safe and sentinel-aware: if the rc
// word's high bit is set (0x80000000 = "static, never touch"),
// the helper returns the input pointer without modifying
// anything. The only static sentinel today is the shared
// empty-array (.LArr_Empty); string-literal heads will pick
// up the same treatment when Phase 1e widens the rc layout
// to strings.
//
// Returning the input pointer (rather than void) lets the IR
// codegen splice an inc into an expression evaluation chain
// without spilling to a temp local: `evaluate RHS; call
// __fern_rc_inc; store LHS` becomes a straight-line sequence
// since x0 carries the value through unchanged.
//
// See docs/RC-PERCEUS-PLAN.md "Core operations".
func (g *generator) emitRcIncRuntime() {
	g.line("")
	g.line(".global __fern_rc_inc")
	g.typeDirective("__fern_rc_inc")
	g.label("__fern_rc_inc")
	g.emit("cbz x0, .Lrcinc_ret")
	// SSO inline-tag guard: native strings ≤7 bytes pack their bytes
	// into the "pointer" word with bit 0 set. Treating them as pointers
	// would mis-read [data-8] as an rc word and corrupt memory. Heap
	// pointers from __fern_alloc / __fern_alloc_rc1 are always 8-byte
	// aligned (low bit clear), so this guard is a no-op for every
	// non-string caller (arrays / structs / enums / closures / etc.).
	g.emit("tbnz x0, #0, .Lrcinc_ret")
	// Below-heap guard (see emitRcDecRuntime): only heap objects carry an
	// rc word at [ptr-8]. A no-capture closure is a bare code address in
	// .text, far below the heap base — inc'ing it would write [ptr-8] in
	// read-only .text. x1 is free here (the rc word loads into it next).
	g.emit("mov x1, #1")
	g.emit("lsl x1, x1, #28") // x1 = 0x1000_0000 = heap base hint
	g.emit("cmp x0, x1")
	g.emit("b.lo .Lrcinc_ret")
	g.emit("ldur w1, [x0, #-8]")
	g.emit("tbnz w1, #31, .Lrcinc_ret")
	g.emit("add w1, w1, #1")
	g.emit("stur w1, [x0, #-8]")
	g.label(".Lrcinc_ret")
	g.emit("ret")
	g.sizeDirective("__fern_rc_inc")
}

// emitRcDecRuntime emits `__fern_rc_dec(ptr)` — decrement the
// refcount at `[ptr - 8]`. NULL-safe and sentinel-aware (see
// emitRcIncRuntime). Phase-1 simplification: on rc == 1 the
// helper still decrements to 0 instead of calling a
// type-specific drop handler + freelist push. The bump
// allocator leaks; Phase 3 introduces the real freelist and
// Phase 1e introduces the drop handlers. Until then, "freeing"
// just leaves the slot at rc = 0 so accidental re-inc /
// re-dec stays observable for the leak detector that phase 1
// testing will rely on.
func (g *generator) emitRcDecRuntime() {
	g.line("")
	g.line(".global __fern_rc_dec")
	g.typeDirective("__fern_rc_dec")
	g.label("__fern_rc_dec")
	g.emit("cbz x0, .Lrcdec_ret")
	// SSO inline-tag guard — see __fern_rc_inc above. Heap pointers are
	// always 8-byte aligned (low bit clear); native strings ≤7 bytes
	// are inline-tagged and must not be deref'd as pointers.
	g.emit("tbnz x0, #0, .Lrcdec_ret")
	// Below-heap guard: skip any pointer below the heap base
	// (the 0x1000_0000 mmap hint — see emitAllocRuntime). Only
	// heap-allocated objects carry an rc word at [ptr-8]; a
	// value below the heap is never one. This covers the
	// unmappable low page, static .text/.rodata/.data, AND
	// function pointers — a no-capture closure is a bare code
	// address (in .text, well below the heap), so a cleanup
	// dec of a closure-valued local must not write [ptr-8],
	// which would land in read-only .text (a crash under an
	// external linker that maps .text non-writable; the native
	// RWX ELF would silently corrupt code instead). The old
	// guard only rejected < 0x10000, letting code/rodata
	// addresses through. x1 is free here (the rc word is loaded
	// into it just below).
	g.emit("mov x1, #1")
	g.emit("lsl x1, x1, #28") // x1 = 0x1000_0000 = heap base hint
	g.emit("cmp x0, x1")
	g.emit("b.lo .Lrcdec_ret")
	g.emit("ldur w1, [x0, #-8]")
	g.emit("tbnz w1, #31, .Lrcdec_ret")
	// Phase 3 underflow detector: a healthy dec operates on rc >= 1.
	// If rc <= 0 here (past the null / low-address / sentinel
	// guards) this dec over-releases — bump __fern_rc_underflow.
	g.emit("cmp w1, #0")
	g.emit("b.gt .Lrcdec_dec")
	g.adrpAdd("x2", "__fern_rc_underflow")
	g.emit("ldr w3, [x2]")
	g.emit("add w3, w3, #1")
	g.emit("str w3, [x2]")
	g.label(".Lrcdec_dec")
	g.emit("sub w1, w1, #1")
	g.emit("stur w1, [x0, #-8]")
	g.label(".Lrcdec_ret")
	g.emit("ret")
	g.sizeDirective("__fern_rc_dec")
}

// emitRcUnderflowCountRuntime emits `__fern_rc_underflow_count()
// -> i32` — returns the Phase 3 over-release counter that
// __fern_rc_dec bumps in __fern_rc_underflow. Mirrors the wasm
// helper that reads the linear-memory counter slot.
func (g *generator) emitRcUnderflowCountRuntime() {
	g.line("")
	g.line(".global __fern_rc_underflow_count")
	g.typeDirective("__fern_rc_underflow_count")
	g.label("__fern_rc_underflow_count")
	g.adrpAdd("x0", "__fern_rc_underflow")
	g.emit("ldr w0, [x0]")
	g.emit("ret")
	g.sizeDirective("__fern_rc_underflow_count")
}

// emitHeapBumpBytesRuntime emits `__fern_heap_bump_bytes() -> i64`,
// the Phase 6 measurement reader: returns the bump high-water mark
// (__fern_heap_ptr − __fern_heap_base) in bytes, or 0 before the first
// allocation seeds the cursor. Mirrors the x86_64 helper.
func (g *generator) emitHeapBumpBytesRuntime() {
	g.line("")
	g.line(".global __fern_heap_bump_bytes")
	g.typeDirective("__fern_heap_bump_bytes")
	g.label("__fern_heap_bump_bytes")
	g.adrpAdd("x1", "__fern_heap_ptr")
	g.emit("ldr x0, [x1]")
	g.emit("cbz x0, .Lheap_bump_zero") // never allocated → 0
	g.adrpAdd("x2", "__fern_heap_base")
	g.emit("ldr x2, [x2]")
	g.emit("sub x0, x0, x2")
	g.emit("ret")
	g.label(".Lheap_bump_zero")
	g.emit("mov x0, #0")
	g.emit("ret")
	g.sizeDirective("__fern_heap_bump_bytes")
}

// emitHeapMarkRuntime emits `__fern_heap_mark() -> i64` and
// `__fern_heap_release_to(mark: i64)` — the one-level arena checkpoint.
// Mirrors the x86_64 pair; see emitHeapMarkRuntime there for why the freelist
// heads are snapshotted rather than cleared (a block allocated and freed
// inside a window would otherwise leave a head pointing above the mark, and
// both a later pop and a later bump would hand out that same address) and for
// the caller's obligation that nothing allocated after the mark is still
// reachable at the release.
func (g *generator) emitHeapMarkRuntime() {
	g.line("")
	g.line(".global __fern_heap_mark")
	g.typeDirective("__fern_heap_mark")
	g.label("__fern_heap_mark")
	g.adrpAdd("x1", "__fern_heap_ptr")
	g.emit("ldr x0, [x1]")
	if ast.RcFreeEnabled && (g.usesAlloc || g.usesFree) {
		// The copy loop needs scratch registers. Every other reader of the
		// arena globals (__fern_heap_bump_bytes) leaves the argument
		// registers alone, so preserve them rather than assume the emitted
		// code around a call here holds nothing live in x1..x4.
		g.emit("stp x1, x2, [sp, #-32]!")
		g.emit("stp x3, x4, [sp, #16]")
		g.adrpAdd("x1", "__fern_freelist_heads")
		g.adrpAdd("x2", "__fern_freelist_shadow")
		g.emit("mov x3, #2048")
		g.label(".Lheap_mark_cp")
		g.emit("ldr x4, [x1], #8")
		g.emit("str x4, [x2], #8")
		g.emit("subs x3, x3, #8")
		g.emit("b.ne .Lheap_mark_cp")
		g.emit("ldp x3, x4, [sp, #16]")
		g.emit("ldp x1, x2, [sp], #32")
	}
	g.emit("ret")
	g.sizeDirective("__fern_heap_mark")

	g.line("")
	g.line(".global __fern_heap_release_to")
	g.typeDirective("__fern_heap_release_to")
	g.label("__fern_heap_release_to")
	g.emit("cbz x0, .Lheap_rel_done") // mark 0 = no checkpoint
	g.adrpAdd("x1", "__fern_heap_ptr")
	g.emit("str x0, [x1]")
	if ast.RcFreeEnabled && (g.usesAlloc || g.usesFree) {
		g.emit("stp x1, x2, [sp, #-32]!")
		g.emit("stp x3, x4, [sp, #16]")
		g.adrpAdd("x1", "__fern_freelist_shadow")
		g.adrpAdd("x2", "__fern_freelist_heads")
		g.emit("mov x3, #2048")
		g.label(".Lheap_rel_cp")
		g.emit("ldr x4, [x1], #8")
		g.emit("str x4, [x2], #8")
		g.emit("subs x3, x3, #8")
		g.emit("b.ne .Lheap_rel_cp")
		g.emit("ldp x3, x4, [sp, #16]")
		g.emit("ldp x1, x2, [sp], #32")
	}
	g.label(".Lheap_rel_done")
	g.emit("ret")
	g.sizeDirective("__fern_heap_release_to")
}

// emitAllocBoxRuntime emits `__fern_alloc_box(size) -> data` —
// the arm64 counterpart of the x86_64 helper. Allocates
// `size + 8` bytes, writes the static-sentinel 0x80000000 at
// `[base + 0]`, and returns the data pointer `base + 8`. Used
// by every runtime helper that builds an Option / Result /
// IoError box so Phase 1e's predicate widening can call
// __fern_rc_inc/dec on enum values safely — the inc/dec
// helpers see the high bit at `[data - 8]` and short-circuit.
//
// The caller passes the payload size (the same value it used
// to pass to __fern_alloc); subsequent tag / payload stores
// keep their existing offsets relative to the returned data.
func (g *generator) emitAllocBoxRuntime() {
	g.line("")
	g.line(".global __fern_alloc_box")
	g.typeDirective("__fern_alloc_box")
	g.label("__fern_alloc_box")
	g.emit("add w0, w0, #8") // size + rc header
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	g.emit("bl __fern_alloc")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("mov w1, #1")
	g.emit("lsl w1, w1, #31") // w1 = 0x80000000 (static sentinel)
	g.emit("str w1, [x0]")    // sentinel at base + 0
	g.emit("add x0, x0, #8")  // return base + 8 (= data)
	g.emit("ret")
	g.sizeDirective("__fern_alloc_box")
}

// emitAllocRc1Runtime emits `__fern_alloc_rc1(size) -> data` —
// identical to __fern_alloc_box but writes a live rc=1 at
// `[base+0]` instead of the immortal 0x80000000 sentinel. Closure
// env blocks / pairs use it so they are real refcounted objects
// (droppable at rc=0 in Phase 3). The caller passes the payload
// size; the helper adds the 8-byte header and returns base+8, so
// the caller's `[x0, #off]` stores stay at their offsets.
func (g *generator) emitAllocRc1Runtime() {
	g.line("")
	g.line(".global __fern_alloc_rc1")
	g.typeDirective("__fern_alloc_rc1")
	g.label("__fern_alloc_rc1")
	g.emit("add w0, w0, #8") // size + rc header
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #-16]!") // save caller's x19 (16-aligned)
	g.emit("mov w19, w0")          // x19 = size+8, survives the call
	g.emit("bl __fern_alloc")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]") // live rc = 1 at base + 0 (= data-8)
	// Stash payload size at base+4 (= data-4, the unused half of the
	// rc1 header) so a drop site can free the block without a
	// separate size header — the closure-env reclamation path reads
	// it. Harmless for every other rc1 user.
	g.emit("sub w19, w19, #8")   // recover payload size
	g.emit("str w19, [x0, #4]")  // size at base+4 (= data-4)
	g.emit("ldr x19, [sp], #16") // restore x19
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("add x0, x0, #8") // return base + 8 (= data)
	g.emit("ret")
	g.sizeDirective("__fern_alloc_rc1")
}

// emitArrPushGrowRuntime emits `__fern_arr_push_grow(arr,
// oldLen, stride) -> new_data` — the Phase 2 mutate-or-copy
// helper called from IR-level `emitArrayPush`. Reads rc at
// `[arr-8]` and cap at `[arr-12]`:
//
//   - rc == 1 AND oldLen < cap  → mutate in place. Bump rc to
//     2 (so the Phase 1d-vi dec-on-overwrite later drops it
//     back to 1) and write `[arr-4] = oldLen+1`. Return arr.
//   - else                      → allocate a new buffer with
//     cap = max(2*newLen, 4), copy `oldLen*stride` bytes,
//     write new cap / rc=1 / len. Return new data pointer.
//
// The IR's caller then does the width-correct element store at
// `[buf + oldLen*stride]`. Sentinel-aware via the rc high bit:
// `[arr-8] == 0x80000000` (static empty-array head) compares
// unequal to 1 and falls through to the copy path, which is
// the correct behaviour (you can't mutate the static sentinel).
//
// See docs/RC-PERCEUS-PLAN.md "Phase 2".
func (g *generator) emitArrPushGrowRuntime() {
	g.line("")
	g.line(".global __fern_arr_push_grow")
	g.typeDirective("__fern_arr_push_grow")
	g.label("__fern_arr_push_grow")
	// Fast path: rc==1 and oldLen < cap. arm64 AAPCS64 inputs:
	//   x0 = arr, x1 = oldLen (i32), x2 = stride (i32).
	g.emit("ldur w3, [x0, #-8]") // w3 = rc
	g.emit("cmp w3, #1")
	g.emit("b.ne .Lpush_copy")
	g.emit("ldur w4, [x0, #-12]") // w4 = cap
	g.emit("cmp w1, w4")
	g.emit("b.ge .Lpush_copy")
	// In place: bump rc to 2, write len = oldLen+1.
	g.emit("mov w3, #2")
	g.emit("stur w3, [x0, #-8]")
	g.emit("add w4, w1, #1")
	g.emit("stur w4, [x0, #-4]")
	g.emit("ret")
	// Copy path: allocate new buffer, memcpy, return new data.
	// Frame layout (80 bytes):
	//   sp+0..15  : saved x29, x30
	//   sp+16..31 : saved x19 (arr), x20 (oldLen)
	//   sp+32..47 : saved x21 (stride), x22 (newLen)
	//   sp+48..63 : saved x23 (newCap), x24 (headerBytes)
	//   sp+64..79 : saved x25 (new-data ptr), x26 (unused / pad)
	g.label(".Lpush_copy")
	g.emit("stp x29, x30, [sp, #-80]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("stp x25, x26, [sp, #64]")
	g.emit("mov x19, x0")      // x19 = arr
	g.emit("mov w20, w1")      // w20 = oldLen
	g.emit("mov w21, w2")      // w21 = stride
	g.emit("add w22, w20, #1") // w22 = newLen
	// newCap = max(2*newLen, 4)
	g.emit("lsl w23, w22, #1")
	g.emit("mov w0, #4")
	g.emit("cmp w23, w0")
	g.emit("csel w23, w23, w0, ge") // w23 = max(2*newLen, 4)
	// headerBytes = max(16, stride). For stride <= 16 use 16.
	g.emit("mov w24, #16")
	g.emit("cmp w21, w24")
	g.emit("csel w24, w21, w24, ge") // w24 = max(stride, 16)
	// allocSize = headerBytes + newCap * stride
	g.emit("mul w0, w23, w21")
	g.emit("add w0, w0, w24")
	g.emit("bl __fern_alloc")
	// x0 = base; new_data = base + headerBytes (in w24).
	g.emit("add x25, x0, x24")
	// Store cap at [base + headerBytes - 12]
	g.emit("sub w1, w24, #12")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("str w23, [x2]")
	// Store rc = 1 at [base + headerBytes - 8] (NOT bumped;
	// copy returns a fresh value, caller's dec-on-overwrite
	// affects only the OLD buffer).
	g.emit("sub w1, w24, #8")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("mov w3, #1")
	g.emit("str w3, [x2]")
	// Store len = newLen at [base + headerBytes - 4]
	g.emit("sub w1, w24, #4")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("str w22, [x2]")
	// memcpy(new_data, arr, oldLen * stride). __fern_memcpy
	// AAPCS64: x0=dst, x1=src, x2=n.
	g.emit("mov x0, x25")
	g.emit("mov x1, x19")
	g.emit("mul w2, w20, w21")
	g.emit("bl __fern_memcpy")
	// Return new_data in x0.
	g.emit("mov x0, x25")
	g.emit("ldp x25, x26, [sp, #64]")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #80")
	g.emit("ret")
	g.sizeDirective("__fern_arr_push_grow")
	g.line(".ltorg")
}

// emitArrPushGrowPtrRuntime emits `__fern_arr_push_grow_ptr(arr,
// oldLen, stride) -> new_data` — the rc-tracked-pointer-element variant
// of __fern_arr_push_grow (#3425). Identical fast path (rc==1 +
// capacity → in-place, no element traffic). On the COPY path, after the
// memcpy it walks the oldLen copied elements and __fern_rc_inc's each
// so the fresh buffer independently OWNS its references; the plain
// helper's raw memcpy left the copy sharing the old buffer's element
// pointers at unchanged rc, and the old buffer's later walk-drop at
// rc==1 freed elements the grown copy still referenced (use-after-
// free). Mirrors __fern_arr_cow_inplace_ptr's retain loop (#4187).
//
// moveForm emits `__fern_arr_push_grow_move_ptr` instead: the same
// helper with the retain loop SKIPPED when the incoming rc is 1 — the
// self-append form's contract. See the x86-64 mirror for why "the old
// buffer survives this grow" is exactly the rc != 1 test (#3457).
//
// AAPCS64 inputs: x0 = arr, x1 = oldLen (i32), x2 = stride (i32).
// Returns new data pointer in x0.
func (g *generator) emitArrPushGrowPtrRuntime(moveForm bool) {
	name, lbl := "__fern_arr_push_grow_ptr", ".Lpushp"
	if moveForm {
		name, lbl = "__fern_arr_push_grow_move_ptr", ".Lpushmp"
	}
	g.line("")
	g.line(".global " + name)
	g.typeDirective(name)
	g.label(name)
	// Fast path: rc==1 and oldLen < cap → in place (rc=2, len++).
	g.emit("ldur w3, [x0, #-8]")
	g.emit("cmp w3, #1")
	g.emit("b.ne %s_copy", lbl)
	g.emit("ldur w4, [x0, #-12]")
	g.emit("cmp w1, w4")
	g.emit("b.ge %s_copy", lbl)
	g.emit("mov w3, #2")
	g.emit("stur w3, [x0, #-8]")
	g.emit("add w4, w1, #1")
	g.emit("stur w4, [x0, #-4]")
	g.emit("ret")
	// Copy path — same frame plan as __fern_arr_push_grow.
	g.label(lbl + "_copy")
	g.emit("stp x29, x30, [sp, #-80]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("stp x25, x26, [sp, #64]")
	g.emit("mov x19, x0")      // x19 = arr
	g.emit("mov w20, w1")      // w20 = oldLen
	g.emit("mov w21, w2")      // w21 = stride
	g.emit("add w22, w20, #1") // w22 = newLen
	// newCap = max(2*newLen, 4)
	g.emit("lsl w23, w22, #1")
	g.emit("mov w0, #4")
	g.emit("cmp w23, w0")
	g.emit("csel w23, w23, w0, ge")
	// headerBytes = max(16, stride)
	g.emit("mov w24, #16")
	g.emit("cmp w21, w24")
	g.emit("csel w24, w21, w24, ge")
	// allocSize = headerBytes + newCap * stride
	g.emit("mul w0, w23, w21")
	g.emit("add w0, w0, w24")
	g.emit("bl __fern_alloc")
	g.emit("add x25, x0, x24") // x25 = new_data
	g.emit("sub w1, w24, #12")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("str w23, [x2]") // cap
	g.emit("sub w1, w24, #8")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("mov w3, #1")
	g.emit("str w3, [x2]") // rc = 1
	g.emit("sub w1, w24, #4")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("str w22, [x2]") // len = newLen
	// memcpy(new_data, arr, oldLen * stride)
	g.emit("mov x0, x25")
	g.emit("mov x1, x19")
	g.emit("mul w2, w20, w21")
	g.emit("bl __fern_memcpy")
	if moveForm {
		// The copy path leaves the OLD buffer's rc untouched, so x19 (still
		// arr) reads the incoming count. rc==1 means the assign's
		// buffer-only __fern_arr_dec is about to free it and the elements
		// transfer; skip the retain. w22 (newLen) is dead — already stored.
		g.emit("ldur w22, [x19, #-8]")
		g.emit("cmp w22, #1")
		g.emit("b.eq %s_inc_done", lbl)
	}
	// Element-retain loop: inc each copied pointer element. x25 =
	// new_data, w20 = oldLen, w21 = stride survive __fern_rc_inc
	// (callee-saved); w26 = i.
	g.emit("mov w26, #0")
	g.label(lbl + "_inc_loop")
	g.emit("cmp w26, w20")
	g.emit("b.ge %s_inc_done", lbl)
	g.emit("mul w0, w26, w21")
	g.emit("add x0, x25, w0, uxtw")
	g.emit("ldr x0, [x0]")     // element pointer (8-byte)
	g.emit("bl __fern_rc_inc") // guards null / low / sentinel
	g.emit("add w26, w26, #1")
	g.emit("b %s_inc_loop", lbl)
	g.label(lbl + "_inc_done")
	g.emit("mov x0, x25") // return new_data
	g.emit("ldp x25, x26, [sp, #64]")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #80")
	g.emit("ret")
	g.sizeDirective(name)
	g.line(".ltorg")
}

// emitArrPushGrowStrRuntime emits `__fern_arr_push_grow_str(arr,
// oldLen, stride) -> new_data` — the two-word string[] variant of
// __fern_arr_push_grow (#3425). Same shape as the _ptr sibling, but
// each element is a (data, len) pair at stride bytes apart (16 on
// arm64 two-word), retained via __fern_str_inc — matching the
// __fern_drop_arr_str walk that releases them, so a grow copy and the
// old buffer's eventual element walk stay balanced.
//
// moveForm emits `__fern_arr_push_grow_move_str` instead: the same
// helper with the retain loop SKIPPED when the incoming rc is 1 — the
// self-append form's contract (#3457, see the _ptr sibling).
//
// AAPCS64 inputs: x0 = arr, x1 = oldLen (i32), x2 = stride (i32).
// Returns new data pointer in x0.
func (g *generator) emitArrPushGrowStrRuntime(moveForm bool) {
	name, lbl := "__fern_arr_push_grow_str", ".Lpushs"
	if moveForm {
		name, lbl = "__fern_arr_push_grow_move_str", ".Lpushms"
	}
	g.line("")
	g.line(".global " + name)
	g.typeDirective(name)
	g.label(name)
	g.emit("ldur w3, [x0, #-8]")
	g.emit("cmp w3, #1")
	g.emit("b.ne %s_copy", lbl)
	g.emit("ldur w4, [x0, #-12]")
	g.emit("cmp w1, w4")
	g.emit("b.ge %s_copy", lbl)
	g.emit("mov w3, #2")
	g.emit("stur w3, [x0, #-8]")
	g.emit("add w4, w1, #1")
	g.emit("stur w4, [x0, #-4]")
	g.emit("ret")
	g.label(lbl + "_copy")
	g.emit("stp x29, x30, [sp, #-80]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("stp x25, x26, [sp, #64]")
	g.emit("mov x19, x0")      // x19 = arr
	g.emit("mov w20, w1")      // w20 = oldLen
	g.emit("mov w21, w2")      // w21 = stride
	g.emit("add w22, w20, #1") // w22 = newLen
	g.emit("lsl w23, w22, #1")
	g.emit("mov w0, #4")
	g.emit("cmp w23, w0")
	g.emit("csel w23, w23, w0, ge") // newCap = max(2*newLen, 4)
	g.emit("mov w24, #16")
	g.emit("cmp w21, w24")
	g.emit("csel w24, w21, w24, ge") // headerBytes = max(16, stride)
	g.emit("mul w0, w23, w21")
	g.emit("add w0, w0, w24")
	g.emit("bl __fern_alloc")
	g.emit("add x25, x0, x24") // x25 = new_data
	g.emit("sub w1, w24, #12")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("str w23, [x2]") // cap
	g.emit("sub w1, w24, #8")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("mov w3, #1")
	g.emit("str w3, [x2]") // rc = 1
	g.emit("sub w1, w24, #4")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("str w22, [x2]") // len = newLen
	g.emit("mov x0, x25")
	g.emit("mov x1, x19")
	g.emit("mul w2, w20, w21")
	g.emit("bl __fern_memcpy")
	if moveForm {
		// The copy path leaves the OLD buffer's rc untouched, so x19 (still
		// arr) reads the incoming count. rc==1 means the assign's
		// buffer-only __fern_arr_dec is about to free it and the elements
		// transfer; skip the retain. w22 (newLen) is dead — already stored.
		g.emit("ldur w22, [x19, #-8]")
		g.emit("cmp w22, #1")
		g.emit("b.eq %s_inc_done", lbl)
	}
	// Element-retain loop: __fern_str_inc each copied (data, len)
	// pair — data at [new_data + i*stride], len 8 bytes above. str_inc
	// no-ops on inline-tagged / null / literal-tagged values, mirroring
	// the __fern_drop_arr_str walk's __fern_str_dec.
	g.emit("mov w26, #0")
	g.label(lbl + "_inc_loop")
	g.emit("cmp w26, w20")
	g.emit("b.ge %s_inc_done", lbl)
	g.emit("mul w0, w26, w21")
	g.emit("add x2, x25, w0, uxtw")
	g.emit("ldp x0, x1, [x2]") // (data, len) pair
	g.emit("bl __fern_str_inc")
	g.emit("add w26, w26, #1")
	g.emit("b %s_inc_loop", lbl)
	g.label(lbl + "_inc_done")
	g.emit("mov x0, x25")
	g.emit("ldp x25, x26, [sp, #64]")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #80")
	g.emit("ret")
	g.sizeDirective(name)
	g.line(".ltorg")
}

// emitArrCowInPlaceRuntime emits `__fern_arr_cow_inplace(arr,
// stride) -> buf` — the Phase 2b helper for `arr[i] = v`. The
// helper internalises the rc bookkeeping so the IR-level emit
// doesn't have to coordinate with the `__fern_rc_dec`
// low-address guard (which short-circuits on raw wasm where
// heap addresses sit below 0x10000):
//
//   - rc == 1 → return arr unchanged (no rc change).
//   - rc >  1 → allocate a fresh buffer with the SAME cap+len,
//     memcpy the payload, write rc=1 on the new header, and
//     decrement arr's rc by 1 (skipping if arr is a static
//     sentinel — high bit of rc word set). Return the new
//     data pointer.
//
// Inputs: x0 = arr, x1 = stride. Returns the new data pointer
// in x0. See docs/RC-PERCEUS-PLAN.md "Phase 2".
func (g *generator) emitArrCowInPlaceRuntime() {
	g.line("")
	g.line(".global __fern_arr_cow_inplace")
	g.typeDirective("__fern_arr_cow_inplace")
	g.label("__fern_arr_cow_inplace")
	// Fast path: rc == 1 → return arr.
	g.emit("ldur w2, [x0, #-8]")
	g.emit("cmp w2, #1")
	g.emit("b.ne .Lcow_slow")
	g.emit("ret")
	g.label(".Lcow_slow")
	// Copy path. Frame: x29/x30 (+0), x19/x20 (+16: arr/stride),
	// x21/x22 (+32: len/cap), x23/x24 (+48: hdr/new_data).
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("mov x19, x0")          // x19 = arr
	g.emit("mov w20, w1")          // w20 = stride
	g.emit("ldur w21, [x0, #-4]")  // w21 = len
	g.emit("ldur w22, [x0, #-12]") // w22 = cap
	// Decrement arr's rc (we're taking the caller's reference
	// as we copy). Skip when the rc word has its high bit set
	// — that's the static-sentinel marker `.LArr_Empty` uses.
	g.emit("ldur w0, [x19, #-8]")
	g.emit("tbnz w0, #31, .Lcow_skip_dec")
	g.emit("sub w0, w0, #1")
	g.emit("stur w0, [x19, #-8]")
	g.label(".Lcow_skip_dec")
	// headerBytes = max(16, stride).
	g.emit("mov w23, #16")
	g.emit("cmp w20, w23")
	g.emit("csel w23, w20, w23, ge")
	// allocSize = headerBytes + cap * stride.
	g.emit("mul w0, w22, w20")
	g.emit("add w0, w0, w23")
	g.emit("bl __fern_alloc")
	g.emit("add x24, x0, x23") // x24 = new_data = base + headerBytes
	// [base + headerBytes - 12] = cap
	g.emit("sub w1, w23, #12")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("str w22, [x2]")
	// [base + headerBytes - 8] = 1 (new buffer, rc=1)
	g.emit("sub w1, w23, #8")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("mov w3, #1")
	g.emit("str w3, [x2]")
	// [base + headerBytes - 4] = len
	g.emit("sub w1, w23, #4")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("str w21, [x2]")
	// memcpy(new_data, arr, len * stride)
	g.emit("mov x0, x24")
	g.emit("mov x1, x19")
	g.emit("mul w2, w21, w20")
	g.emit("bl __fern_memcpy")
	g.emit("mov x0, x24")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("__fern_arr_cow_inplace")
	g.line(".ltorg")
}

// emitArrCowInPlacePtrRuntime emits `__fern_arr_cow_inplace_ptr(arr,
// stride) -> buf` — the pointer-element variant of
// __fern_arr_cow_inplace. Same fast path (rc==1 → return arr, in-place).
// On the COPY path (rc>1) it does the same alloc + memcpy, then walks the
// `len` elements and __fern_rc_inc's each so the fresh buffer OWNS its own
// reference; the plain helper's raw memcpy would leave the copy sharing
// the receiver's element pointers at unchanged rc — a use-after-free once
// either array is dropped. stride is the pointer width (single-word
// elements loaded 8 bytes wide).
//
// Inputs: x0 = arr, x1 = stride. Returns new data ptr in x0.
func (g *generator) emitArrCowInPlacePtrRuntime() {
	g.line("")
	g.line(".global __fern_arr_cow_inplace_ptr")
	g.typeDirective("__fern_arr_cow_inplace_ptr")
	g.label("__fern_arr_cow_inplace_ptr")
	// Fast path: rc == 1 → return arr (in-place; elements already owned).
	g.emit("ldur w2, [x0, #-8]")
	g.emit("cmp w2, #1")
	g.emit("b.ne .Lcowp_slow")
	g.emit("ret")
	g.label(".Lcowp_slow")
	// Copy path. Frame: x29/x30 (+0), x19/x20 (+16: arr/stride),
	// x21/x22 (+32: len/cap→i), x23/x24 (+48: hdr/new_data).
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("mov x19, x0")          // x19 = arr
	g.emit("mov w20, w1")          // w20 = stride
	g.emit("ldur w21, [x0, #-4]")  // w21 = len
	g.emit("ldur w22, [x0, #-12]") // w22 = cap
	// Decrement arr's rc (taking the caller's reference as we copy).
	// Skip a static sentinel (high bit set).
	g.emit("ldur w0, [x19, #-8]")
	g.emit("tbnz w0, #31, .Lcowp_skip_dec")
	g.emit("sub w0, w0, #1")
	g.emit("stur w0, [x19, #-8]")
	g.label(".Lcowp_skip_dec")
	// headerBytes = max(16, stride).
	g.emit("mov w23, #16")
	g.emit("cmp w20, w23")
	g.emit("csel w23, w20, w23, ge")
	// allocSize = headerBytes + cap * stride.
	g.emit("mul w0, w22, w20")
	g.emit("add w0, w0, w23")
	g.emit("bl __fern_alloc")
	g.emit("add x24, x0, x23") // x24 = new_data = base + headerBytes
	// [base + headerBytes - 12] = cap
	g.emit("sub w1, w23, #12")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("str w22, [x2]")
	// [base + headerBytes - 8] = 1 (new buffer, rc=1)
	g.emit("sub w1, w23, #8")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("mov w3, #1")
	g.emit("str w3, [x2]")
	// [base + headerBytes - 4] = len
	g.emit("sub w1, w23, #4")
	g.emit("add x2, x0, w1, uxtw")
	g.emit("str w21, [x2]")
	// memcpy(new_data, arr, len * stride)
	g.emit("mov x0, x24")
	g.emit("mov x1, x19")
	g.emit("mul w2, w21, w20")
	g.emit("bl __fern_memcpy")
	// Element-retain loop: inc each copied pointer element. x24 = new_data,
	// w21 = len, w20 = stride all survive __fern_rc_inc (callee-saved);
	// w22 = i (reuses the cap slot, no longer needed).
	g.emit("mov w22, #0")
	g.label(".Lcowp_inc_loop")
	g.emit("cmp w22, w21")
	g.emit("b.ge .Lcowp_inc_done")
	g.emit("mul w0, w22, w20") // i*stride
	g.emit("add x0, x24, w0, uxtw")
	g.emit("ldr x0, [x0]")     // element pointer (8-byte)
	g.emit("bl __fern_rc_inc") // guards null / low / sentinel
	g.emit("add w22, w22, #1")
	g.emit("b .Lcowp_inc_loop")
	g.label(".Lcowp_inc_done")
	g.emit("mov x0, x24") // return new_data
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("__fern_arr_cow_inplace_ptr")
	g.line(".ltorg")
}

// emitDropArrPtrRuntime emits `__fern_drop_arr_ptr(ptr, stride)
// -> ptr` — Phase 3 drop handler for an array whose elements are
// pointer-shaped rc-tracked values. Mirrors the wasm
// buildDropArrPtrBody + x86_64's emitDropArrPtrRuntime: NULL +
// low-address + static-sentinel guards, then on the LAST
// reference (rc == 1) walk the `len` elements and dec each via
// __fern_rc_dec before dec'ing the array itself. Returns the
// input ptr (matching __fern_rc_dec's contract).
//
// AAPCS64 inputs: x0 = ptr, x1 = stride. Live values kept in
// callee-saved regs across __fern_rc_dec calls: x19 = ptr,
// x20 = stride, x21 = len, x22 = i.
func (g *generator) emitDropArrPtrRuntime() {
	g.line("")
	g.line(".global __fern_drop_arr_ptr")
	g.typeDirective("__fern_drop_arr_ptr")
	g.label("__fern_drop_arr_ptr")
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("mov x19, x0") // x19 = ptr
	g.emit("mov x20, x1") // x20 = stride
	// NULL guard.
	g.emit("cbz x19, .Ldrop_ret_ptr")
	// Low-address guard — mirror __fern_rc_dec. The exit dec sweep
	// can hand us an array-typed slot that actually holds a
	// non-pointer (an enum tag like 2, a small i32, never-taken-
	// branch stack garbage). Loading [ptr-8] / [ptr-4] on such a
	// value would fault; treat the low 64 KiB as "not a heap object".
	g.emit("cmp x19, #0x10000")
	g.emit("b.lo .Ldrop_ret_ptr")
	// Static-sentinel guard: high bit of rc word set ⇒ never recurse.
	g.emit("ldur w0, [x19, #-8]")
	g.emit("tbnz w0, #31, .Ldrop_ret_ptr")
	// Only the last reference walks elements (rc == 1).
	g.emit("cmp w0, #1")
	g.emit("b.ne .Ldrop_decarr")
	g.emit("ldur w21, [x19, #-4]") // x21 = len
	g.emit("mov x22, #0")          // i = 0
	g.label(".Ldrop_loop")
	g.emit("cmp w22, w21")
	g.emit("b.ge .Ldrop_decarr")
	// x0 = mem[ptr + i*stride] (ptr-width element load).
	g.emit("mul x0, x22, x20")
	g.emit("add x0, x19, x0")
	g.emit("ldr x0, [x0]")
	g.emit("bl __fern_rc_dec")
	g.emit("add x22, x22, #1")
	g.emit("b .Ldrop_loop")
	g.label(".Ldrop_decarr")
	if ast.RcFreeEnabled {
		// Phase 3 step-4: on the last reference (rc==1) the elements
		// have been dec'd above, so return the buffer to the
		// freelist. headerBytes = max(16, stride); base = ptr -
		// headerBytes; size = headerBytes + cap*stride (cap at
		// ptr-12). rc reloaded (the element walk's __fern_rc_dec
		// calls preserve x19/x20 but not w-temps).
		g.emit("ldur w2, [x19, #-8]") // rc
		g.emit("cmp w2, #1")
		g.emit("b.ne .Ldrop_plaindec")
		g.emit("mov x3, #16")
		g.emit("cmp x20, #16")
		g.emit("csel x3, x20, x3, hi") // headerBytes = max(16, stride)
		g.emit("ldur w4, [x19, #-12]") // cap
		g.emit("mul x1, x4, x20")      // cap * stride
		g.emit("add x1, x1, x3")       // + headerBytes = size (arg2)
		g.emit("sub x0, x19, x3")      // base = ptr - headerBytes (arg1)
		g.emit("bl __fern_free")
		g.emit("mov x0, x19") // return ptr
		g.emit("b .Ldrop_done")
		g.label(".Ldrop_plaindec")
	}
	// Dec the array itself; __fern_rc_dec returns the ptr in x0.
	g.emit("mov x0, x19")
	g.emit("bl __fern_rc_dec")
	g.emit("b .Ldrop_done")
	g.label(".Ldrop_ret_ptr")
	g.emit("mov x0, x19")
	g.label(".Ldrop_done")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__fern_drop_arr_ptr")
	g.line(".ltorg")
}

// emitDropArrStrRuntime emits `__fern_drop_arr_str(ptr, stride) ->
// ptr` — the Slice 4 drop handler for `string[]` under the two-word
// ABI. Each element is a (data, len) pair at stride bytes apart; the
// per-element walk loads both and dec's via __fern_str_dec. On the
// LAST reference (rc==1) the elements free; otherwise the array box
// just dec's (the elements stay alive for the other holder). Same
// null / low-address / static-sentinel guards as __fern_drop_arr_ptr;
// returns the input ptr (matching the dec contract).
//
// AAPCS64 inputs: x0 = ptr, x1 = stride (16 for two-word strings on
// arm64). Callee-saved across __fern_str_dec calls: x19 = ptr,
// x20 = stride, x21 = len, x22 = i. Mirrors the wasm
// buildDropArrStrBody and the structural sibling on arm64
// (emitDropArrPtrRuntime).
func (g *generator) emitDropArrStrRuntime() {
	g.line("")
	g.line(".global __fern_drop_arr_str")
	g.typeDirective("__fern_drop_arr_str")
	g.label("__fern_drop_arr_str")
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("mov x19, x0") // x19 = ptr
	g.emit("mov x20, x1") // x20 = stride
	g.emit("cbz x19, .Ldrop_arr_str_ret")
	g.emit("cmp x19, #0x10000")
	g.emit("b.lo .Ldrop_arr_str_ret")
	g.emit("ldur w0, [x19, #-8]")
	g.emit("tbnz w0, #31, .Ldrop_arr_str_ret")
	// Only the last reference walks elements (rc == 1).
	g.emit("cmp w0, #1")
	g.emit("b.ne .Ldrop_arr_str_decarr")
	g.emit("ldur w21, [x19, #-4]") // x21 = len
	g.emit("mov x22, #0")          // i = 0
	g.label(".Ldrop_arr_str_loop")
	g.emit("cmp w22, w21")
	g.emit("b.ge .Ldrop_arr_str_decarr")
	// (x0, x1) = (data, len) of element i = (mem[ptr+i*stride],
	// mem[ptr+i*stride+8]).
	g.emit("mul x0, x22, x20")
	g.emit("add x0, x19, x0")  // x0 = &elem[i]
	g.emit("ldr x1, [x0, #8]") // x1 = elem.len
	g.emit("ldr x0, [x0]")     // x0 = elem.data
	g.emit("bl __fern_str_dec")
	g.emit("add x22, x22, #1")
	g.emit("b .Ldrop_arr_str_loop")
	g.label(".Ldrop_arr_str_decarr")
	if ast.RcFreeEnabled {
		// Same buffer-free path as emitDropArrPtrRuntime: at rc==1
		// the elements have been reclaimed above, so return the
		// buffer to the freelist. headerBytes = max(16, stride),
		// base = ptr - headerBytes, size = headerBytes + cap*stride.
		g.emit("ldur w2, [x19, #-8]")
		g.emit("cmp w2, #1")
		g.emit("b.ne .Ldrop_arr_str_plaindec")
		g.emit("mov x3, #16")
		g.emit("cmp x20, #16")
		g.emit("csel x3, x20, x3, hi")
		g.emit("ldur w4, [x19, #-12]")
		g.emit("mul x1, x4, x20")
		g.emit("add x1, x1, x3")
		g.emit("sub x0, x19, x3")
		g.emit("bl __fern_free")
		g.emit("mov x0, x19")
		g.emit("b .Ldrop_arr_str_done")
		g.label(".Ldrop_arr_str_plaindec")
	}
	g.emit("mov x0, x19")
	g.emit("bl __fern_rc_dec")
	g.emit("b .Ldrop_arr_str_done")
	g.label(".Ldrop_arr_str_ret")
	g.emit("mov x0, x19")
	g.label(".Ldrop_arr_str_done")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__fern_drop_arr_str")
	g.line(".ltorg")
}

// emitRcIsUniqueRuntime emits `__fern_rc_is_unique(ptr) -> i32` —
// returns 1 iff ptr is a real, uniquely-owned heap value
// (non-null, above the low-address guard, not a static sentinel,
// rc == 1); else 0. Same guard chain as __fern_rc_dec, so it is
// safe on a slot that might hold a non-pointer scalar. Used by the
// Phase 3 struct drop to gate recursive field decs on "this is the
// last reference". Leaf (no calls), so no frame.
func (g *generator) emitRcIsUniqueRuntime() {
	g.line("")
	g.line(".global __fern_rc_is_unique")
	g.typeDirective("__fern_rc_is_unique")
	g.label("__fern_rc_is_unique")
	g.emit("cbz x0, .Lisuniq_no")
	g.emit("cmp x0, #0x10000")
	g.emit("b.lo .Lisuniq_no")
	g.emit("ldur w1, [x0, #-8]")
	g.emit("tbnz w1, #31, .Lisuniq_no") // static sentinel
	g.emit("cmp w1, #1")
	g.emit("b.ne .Lisuniq_no")
	g.emit("mov w0, #1")
	g.emit("ret")
	g.label(".Lisuniq_no")
	g.emit("mov w0, #0")
	g.emit("ret")
	g.sizeDirective("__fern_rc_is_unique")
}

// emitSliceMakeRuntime emits `__fern_slice_make(data, len)`:
// allocate a 16-byte slice header [data_ptr, len] on the bump heap
// and return its address. Layout: an 8-byte (pointer-width)
// data_ptr at +0, the i32 len at +8, 16 bytes total (trailing 4
// padding). The full-width data pointer keeps a slice over high
// memory (e.g. `.rodata` in a PIE shared object, or arm64-darwin's
// >4 GiB heap) correct — a 32-bit field truncated such addresses.
// The IR reads len at `[slice + ptrW]`, so wasm32 (ptrW=4) keeps
// its 8-byte {i32 data, i32 len} layout unchanged.
//
// Calling convention: x0 = data_ptr, x1 = len. Returns slice
// header address in x0. Stash inputs in callee-save x19 / x20
// across __fern_alloc.
func (g *generator) emitSliceMakeRuntime() {
	g.line("")
	g.line(".global __fern_slice_make")
	g.typeDirective("__fern_slice_make")
	g.label("__fern_slice_make")
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #-16]!")
	g.emit("mov x19, x0") // data_ptr (full 8 bytes)
	g.emit("mov w20, w1") // len
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc")
	g.emit("str x19, [x0]")     // [+0..+7] data_ptr (8-byte pointer)
	g.emit("str w20, [x0, #8]") // [+8..+11] len (i32)
	g.emit("ldp x19, x20, [sp], #16")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.sizeDirective("__fern_slice_make")
	g.line(".ltorg")
}

// emitSliceRangeRuntime emits `__fern_slice_range(lo, hi, len)` — the
// slice-construction bounds check (#5419). Traps with exit 134 unless
// 0 <= lo <= hi <= len, then returns the slice length hi - lo in w0.
// Two unsigned compares on the sign-extended values cover all four
// conditions: a negative bound sign-extends to a huge unsigned 64-bit
// value, so `hi > len` catches hi < 0 and `lo > hi` catches lo < 0.
// The sxtw normalisation is the same #5294 fix as __str_slice: an i32
// bound can arrive with dirty high bits.
//
// Calling convention: w0 = lo, w1 = hi, w2 = len (i32s).
func (g *generator) emitSliceRangeRuntime() {
	g.line("")
	g.line(".global __fern_slice_range")
	g.typeDirective("__fern_slice_range")
	g.label("__fern_slice_range")
	g.emit("sxtw x0, w0") // lo (sign-extended from i32)
	g.emit("sxtw x1, w1") // hi
	g.emit("sxtw x2, w2") // len
	g.emit("cmp x1, x2")
	g.emit("bhi .Lslicerange_trap") // hi > len (unsigned)
	g.emit("cmp x0, x1")
	g.emit("bhi .Lslicerange_trap") // lo > hi (unsigned)
	g.emit("sub w0, w1, w0")
	g.emit("ret")
	g.label(".Lslicerange_trap")
	g.emitAbort("__fern_msg_slice_range")
	g.sizeDirective("__fern_slice_range")
	g.line(".ltorg")
}

// emitStrcatRuntime emits `__fern_strcat(a, b)` — concat two
// length-prefixed strings into a fresh allocation. Both string
// operands are data pointers (post-prefix) with the 4-byte
// length at `[ptr - 4]`.
//
// Uses callee-save x19..x23 to keep state across the calls
// to __fern_alloc and __fern_memcpy. AAPCS64 says x19..x28
// must be preserved by the callee, so the saved-pair pattern
// at function entry / exit guarantees the values are restored
// before returning to the strcat caller.
func (g *generator) emitStrcatRuntime() {
	g.line("")
	g.line(".global __fern_strcat")
	g.typeDirective("__fern_strcat")
	g.label("__fern_strcat")
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
	g.emit("bl __fern_memcpy")
	// memcpy([x29 + 81 + la], b_data, lb).
	g.emit("add x0, x29, #81")
	g.emit("add x0, x0, x21")
	g.emit("mov x1, x20")
	g.emit("mov x2, x22")
	g.emit("bl __fern_memcpy")
	// Load the full 8-byte inline value (length byte + 7 data
	// bytes + zero padding) into x0.
	g.emit("ldr x0, [x29, #80]")
	g.emit("b .Lstrcat_ret")
	g.label(".Lstrcat_heap")
	// --- Heap output path (L2 rc-header layout) ---
	// Mirrors x86_64.go's __fern_strcat L2 conversion: alloc_rc1 returns
	// data (= base+8); length lands at data-4 (= base+4, rc1's
	// payload-size slot, overwritten — fine for strings since the
	// eventual string-drop computes alloc size from length). Payload =
	// la+lb (no NUL — arm64 strcat is no-NUL, unlike x86 which appends
	// one). RC-STRINGS-PLAN.md prereq 1.
	g.emit("add x0, x21, x22")
	g.emit("bl __fern_alloc_rc1")
	g.emit("mov x23, x0")      // x23 = data ptr (= base+8)
	g.emit("add w5, w21, w22") // w5 = combined length
	g.emitStrLenStore("w5", "x23")
	// Materialise a / b for the memcpy reads.
	g.emitStrDataPtr("x19", "x19", 64)
	g.emitStrDataPtr("x20", "x20", 72)
	// memcpy(data_ptr, a, la); memcpy(data_ptr + la, b, lb)
	g.emit("mov x0, x23")
	g.emit("mov x1, x19")
	g.emit("mov x2, x21")
	g.emit("bl __fern_memcpy")
	g.emit("add x0, x23, x21")
	g.emit("mov x1, x20")
	g.emit("mov x2, x22")
	g.emit("bl __fern_memcpy")
	g.emit("mov x0, x23") // return the data pointer
	g.label(".Lstrcat_ret")
	g.emit("ldr x23, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #96")
	g.emit("ret")
	g.sizeDirective("__fern_strcat")
	g.line(".ltorg")
}

// emitStrcatRuntime2W is the two-word-ABI variant of
// emitStrcatRuntime. Signature: `__fern_strcat(a_data, a_len,
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
	// (data, len) pair of each operand across __fern_alloc /
	// __fern_memcpy (32) + 2× callee-saves (x23..x24) for
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
	// Allocate the destination buffer via the rc-headered allocator
	// so the result carries a live rc=1 at data-8 and its payload
	// size at data-4. Under Slice 2 these heap strings are rc-tracked
	// (str_inc on aliases, str_dec on drops) and both helpers read
	// [data-8] / [data-4]; a raw __fern_alloc buffer has no such
	// header, so retaining a fresh concat (e.g. id(("a" + localStr)))
	// read the word before the allocation and SIGSEGV'd. alloc_rc1
	// adds the 8-byte header itself and returns data = base+8, so the
	// memcpy offsets below are unchanged.
	g.emit("bl __fern_alloc_rc1")
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
	g.emit("bl __fern_memcpy")
	// Materialise b's byte pointer.
	g.emitStrDataPtr2W("x4", "x21", "x22", 80) // x4 = b byte ptr; spill at [x29+80]
	// memcpy(dst + a_byteLen, b_data, b_byteLen).
	g.emit("ldr x0, [x29, #96]")
	g.emit("add x0, x0, x23") // dst + a_byteLen
	g.emit("mov x1, x4")      // src = b byte ptr
	g.emit("mov x2, x24")     // n = b_byteLen
	g.emit("bl __fern_memcpy")
	// Return (dst, total_byteLen) in (x0, x1).
	g.emit("ldr x0, [x29, #96]")
	g.emit("add w1, w23, w24")
	g.label(".Lstrcat2w_ret")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #112")
	g.emit("ret")
	g.sizeDirective("__fern_strcat")
	g.line(".ltorg")
}

// emitArrBoundsCheck emits the array index bounds check shared by
// every `__arr_idx*` variant: with the element index in x0 and the
// array base in x1, the length prefix at [base-4] is compared and
// an out-of-range index aborts the process with exit code 134 (the
// same trap the string-slice helper uses, and what wasm's
// `unreachable` produces under wasmtime). A single unsigned compare
// catches both a negative index (huge as unsigned) and index >=
// len. x2 is scratch.
func (g *generator) emitArrBoundsCheck() {
	ok := g.freshLabel("arr_ok")
	g.emit("ldur w2, [x1, #-4]") // len prefix
	g.emit("cmp w0, w2")
	g.emit("b.lo %s", ok) // unsigned idx < len → in bounds
	g.emitAbort("__fern_msg_arr_oob")
	g.label(ok)
}

// emitSliceBoundsCheck is emitArrBoundsCheck for a slice: the
// length lives in the slice header at [slice+8] (the 8-byte
// data_ptr is at [slice+0]), so it must be read before the helper
// overwrites x1 with the data pointer. x2 is scratch.
func (g *generator) emitSliceBoundsCheck() {
	ok := g.freshLabel("slice_ok")
	g.emit("ldur w2, [x1, #8]") // len at [slice+8] (after 8-byte data_ptr)
	g.emit("cmp w0, w2")
	g.emit("b.lo %s", ok)
	g.emitAbort("__fern_msg_slice_oob")
	g.label(ok)
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
	// Bounds-check elision (#4380 lever 3): a `_nc` suffix names the same
	// address compute minus the len-load + compare + trap. Strip it and
	// remember to skip emitArrBoundsCheck for the array cases below.
	checked := true
	if strings.HasSuffix(name, "_nc") {
		checked = false
		name = strings.TrimSuffix(name, "_nc")
	}
	arrBounds := func() {
		if checked {
			g.emitArrBoundsCheck()
		}
	}
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
		g.emit("mov w0, w0")                   // zero-extend i32 index (see below, #4377)
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
		g.adrpAdd("x3", "__fern_str_idx_scratch")
		g.emit("str x2, [x3]")     // data bytes at scratch[0..7]
		g.emit("str x1, [x3, #8]") // len bytes at scratch[8..15]
		g.emit("add x0, x3, x0")
		g.label(doneLbl)
		g.push()
		return nil
	}
	g.emit("ldr x0, [sp], #%d", slotBytes) // idx
	// Zero-extend the i32 index: the bounds checks compare the low 32 bits
	// (`w0`) but the address adds shift the full 64-bit `x0`, so stale garbage
	// in bits 32..63 would pass the check yet produce a wild scaled address
	// (the #4377 ir.Fold-exposed miscompile — a materialised constant index
	// can carry dirty upper bits). `mov w0, w0` zeroes x0's top 32 bits.
	g.emit("mov w0, w0")
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
		g.adrpAdd("x2", "__fern_str_idx_scratch")
		g.emit("str x1, [x2]")
		g.emit("add x0, x2, x0")
		g.emit("add x0, x0, #1")
		g.label(doneLbl)
	case "__arr_idx_1":
		// Stride-1 byte-array indexing: byte address = base +
		// idx. Split from $__str_idx so the string helper can
		// own the SSO inline-spill dispatch without forcing
		// byte arrays through the same `tbnz` check.
		arrBounds()
		g.emit("add x0, x1, x0")
	case "__arr_idx":
		arrBounds()
		g.emit("add x0, x1, x0, lsl #2")
	case "__arr_idx_8":
		arrBounds()
		g.emit("add x0, x1, x0, lsl #3")
	case "__arr_idx_16":
		// 16-byte stride — two-word `string[]` element load.
		arrBounds()
		g.emit("add x0, x1, x0, lsl #4")
	// Slice indexing first bounds-checks `i` against the slice
	// header's len (at [slice+4]), then dereferences its 32-bit
	// data_ptr field (at [slice+0]) and does the same
	// stride-shifted add as the array helpers.
	case "__slice_idx_1":
		g.emitSliceBoundsCheck()
		g.emit("ldr x1, [x1]") // data_ptr (8-byte pointer)
		g.emit("add x0, x1, x0")
	case "__slice_idx":
		g.emitSliceBoundsCheck()
		g.emit("ldr x1, [x1]")
		g.emit("add x0, x1, x0, lsl #2")
	case "__slice_idx_8":
		g.emitSliceBoundsCheck()
		g.emit("ldr x1, [x1]")
		g.emit("add x0, x1, x0, lsl #3")
	case "__slice_idx_16":
		g.emitSliceBoundsCheck()
		g.emit("ldr x1, [x1]")
		g.emit("add x0, x1, x0, lsl #4")
	default:
		return fmt.Errorf("arm64: unknown index helper %q", name)
	}
	g.push()
	return nil
}

// emitStrcmpRuntime emits `__fern_strcmp(a, b)` — equality
// comparator returning 0 (equal) / 1 (different). Layout:
// length-prefix + word-grain bulk + byte-grain tail; pointer
// args are post-prefix.
func (g *generator) emitStrcmpRuntime() {
	g.line("")
	g.line(".global __fern_strcmp")
	g.typeDirective("__fern_strcmp")
	g.label("__fern_strcmp")
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
	g.sizeDirective("__fern_strcmp")
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
	g.emit("mov x4, x1")       // save a_len
	g.emit("mov x5, x3")       // save b_len
	g.emitStrLen2W("w6", "x4") // w6 = a byte length
	g.emitStrLen2W("w7", "x5") // w7 = b byte length
	g.emit("cmp w6, w7")
	g.emit("bne .Lscmp2w_neq")
	// Same length → materialise both byte pointers.
	g.emitStrDataPtr2W("x0", "x0", "x1", 16) // x0 = a byte ptr; scratch at [x29+16]
	g.emitStrDataPtr2W("x1", "x2", "x3", 32) // x1 = b byte ptr; scratch at [x29+32]
	g.emit("mov w2, w6")                     // remaining bytes
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
	g.sizeDirective("__fern_strcmp")
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

	// `__load_i64` / `__store_i64` — 8-byte load / store.
	// Used by the Map runtime's wide-scalar-boxed key path
	// (keyKind=2) to dereference an i64 / u64 / f64 key
	// from a heap cell. On arm64 a usize is already 8 bytes
	// so the lang-level Map[i64, _] path stays on keyKind=0
	// without these — the symbols still need linkable
	// bodies because the stdlib references them by name
	// regardless of target.
	g.line("")
	g.line(".global __load_i64")
	g.typeDirective("__load_i64")
	g.label("__load_i64")
	g.emit("ldr x0, [x0]")
	g.emit("ret")
	g.sizeDirective("__load_i64")

	g.line("")
	g.line(".global __store_i64")
	g.typeDirective("__store_i64")
	g.label("__store_i64")
	g.emit("str x1, [x0]")
	g.emit("ret")
	g.sizeDirective("__store_i64")

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
// pointer (header + 8); length at `[data - 4]`, refcount slot
// at `[data - 8]` (reserved for phase 1; not initialised here
// yet — see docs/RC-PERCEUS-PLAN.md).
func (g *generator) emitAllocU8Runtime() {
	g.line("")
	g.line(".global __alloc_u8")
	g.typeDirective("__alloc_u8")
	g.label("__alloc_u8")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("mov x19, x0") // x19 = n (callee-save, survives bl)
	// Short-circuit on n == 0: return the shared static empty-
	// array sentinel rather than allocating a fresh header-only
	// buffer. The sentinel's byte at offset -4 is 0 (length), so
	// emitArrayLen reads the right value via the same
	// `ldur w?, [ptr, #-4]` it does for heap buffers.
	g.emit("cbnz w19, .Lallocu8_alloc")
	g.usesArrEmpty = true
	g.adrpAdd("x0", ".LArr_Empty")
	g.emit("b .Lallocu8_ret")
	g.label(".Lallocu8_alloc")
	g.emit("add x0, x19, #16")
	g.emit("bl __fern_alloc")
	g.emit("add x0, x0, #16")      // x0 = data ptr (past 16-byte header)
	g.emit("stur w19, [x0, #-12]") // cap = n  (Phase 2-prep)
	g.emit("mov w1, #1")
	g.emit("stur w1, [x0, #-8]") // rc = 1 (phase 1 of RC rollout)
	g.emitArrayLenStore("w19", "x0")
	// Zero the n data bytes — __fern_alloc may return a reused freelist block
	// with stale bytes, but the interpreter yields a zero-filled `u8[]`, so the
	// AOT backends must match (issue #2768): read-before-write callers (e.g.
	// SHA padding) rely on it. x0 (data, return value) is preserved; x2/x3 are
	// scratch.
	g.emit("mov x2, x0")  // cursor (keep x0 as the return value)
	g.emit("mov w3, w19") // count = n
	g.label(".Lallocu8_zero")
	g.emit("cbz w3, .Lallocu8_ret")
	g.emit("strb wzr, [x2], #1")
	g.emit("sub w3, w3, #1")
	g.emit("b .Lallocu8_zero")
	g.label(".Lallocu8_ret")
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__alloc_u8")
	g.line(".ltorg")
}

// emitStringFromBytesRuntime emits `string_from_bytes_unchecked(bs)` —
// copy a `u8[]` payload into a fresh length-prefixed string.
// Round-trip companion to `s.bytes()`.
func (g *generator) emitStringFromBytesRuntime() {
	g.line("")
	g.line(".global string_from_bytes_unchecked")
	g.typeDirective("string_from_bytes_unchecked")
	g.label("string_from_bytes_unchecked")
	if ast.UseTwoWordStrings(8) {
		// Two-word ABI: take `bs` (u8[] data pointer) in x0,
		// return `(data, len)` in (x0, x1).
		// Frame: fp/lr (16) + 2 callee-saves (16) + 16 align.
		g.emit("stp x29, x30, [sp, #-48]!")
		g.emit("mov x29, sp")
		g.emit("stp x19, x20, [sp, #16]")
		g.emit("mov x19, x0")        // x19 = bs (input u8[])
		g.emitArrayLen("w20", "x19") // x20 = byte length
		// Empty input → empty pair.
		g.emit("cbnz w20, .Lsfb2w_alloc")
		g.emit("mov x0, xzr")
		g.emit("movz x1, #0x8000, lsl #48")
		g.emit("b .Lsfb2w_ret")
		g.label(".Lsfb2w_alloc")
		g.emit("mov w0, w20")
		// Allocate via the rc-headered allocator (rc=1 at data-8,
		// payload size at data-4) — exactly like __str_slice /
		// __fern_strcat on this two-word path. A plain __fern_alloc
		// buffer has no rc header, so a later __fern_str_dec (which
		// reads rc at data-8 and the free size at data-4) reads
		// garbage: rc_dec'ing a neighbouring cell's bytes or
		// box_free'ing a wrong-sized block overlapping a still-live
		// cell — the arm64-only heap-corruption under url_decode/
		// url_encode allocation churn (#2817).
		g.emit("bl __fern_alloc_rc1")      // x0 = dst (= base+8)
		g.emit("mov x2, x20")              // n
		g.emit("mov x1, x19")              // src = bs
		g.emit("stp x0, xzr, [sp, #-16]!") // save dst on stack
		g.emit("bl __fern_memcpy")
		g.emit("ldp x0, x1, [sp], #16") // x0 = dst (saved), x1 = junk
		g.emit("mov w1, w20")           // len = byteLen
		g.label(".Lsfb2w_ret")
		g.emit("ldp x19, x20, [sp, #16]")
		g.emit("ldp x29, x30, [sp], #48")
		g.emit("ret")
		g.sizeDirective("string_from_bytes_unchecked")
		g.line(".ltorg")
		return
	}
	// Frame: 64 bytes — fp/lr (16) + x19/x20/x21 (24 + 8 pad) +
	// 16 SSO inline-output buffer (only 8 bytes used, 8 padding).
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("str x21, [sp, #32]")
	g.emit("mov x19, x0")        // x19 = bs (input u8[] array)
	g.emitArrayLen("w20", "x19") // x20 = input array length
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
	g.emit("bl __fern_memcpy")
	g.emit("ldr x0, [x29, #48]")
	g.emit("b .Lsfb_ret")
	g.label(".Lsfb_heap")
	// L2 rc-header layout — see __fern_strcat.
	g.emit("mov x0, x20")
	g.emit("bl __fern_alloc_rc1")
	g.emit("mov x21, x0") // x21 = data ptr (= base+8)
	g.emitStrLenStore("w20", "x21")
	g.emit("mov x0, x21") // memcpy dst
	g.emit("mov x1, x19")
	g.emit("mov x2, x20")
	g.emit("bl __fern_memcpy")
	g.emit("mov x0, x21") // return data ptr
	g.label(".Lsfb_ret")
	g.emit("ldr x21, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("string_from_bytes_unchecked")
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
	// value across __fern_memcpy.
	g.emit("stp x29, x30, [sp, #-80]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("str x23, [sp, #48]")
	g.emit("mov x19, x0") // x19 = base (may be inline-tagged)
	// Sign-extend the i32 bounds from their low 32 bits (#5294) — the
	// arm64 twin of x86-64's movsxd: a negative i32 constant materialises
	// zero-extended (mov w0, N), so a dirty-high-bits bound slips past the
	// bounds compares. sxtw is a no-op for a clean bound.
	g.emit("sxtw x20, w1")     // x20 = low (sign-extended from i32)
	g.emit("sxtw x21, w2")     // x21 = high (sign-extended from i32)
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
	g.emit("bl __fern_memcpy")
	g.emit("ldr x0, [x29, #72]")
	g.emit("b .Lstrslice_ret")
	g.label(".Lstrslice_heap")
	// --- Heap output path (L2 rc-header layout — see __fern_strcat). ---
	g.emit("mov x0, x22")
	g.emit("bl __fern_alloc_rc1")
	g.emit("mov x23, x0") // x23 = data ptr (= base+8)
	g.emitStrLenStore("w22", "x23")
	// memcpy(data_ptr, base + low, new_len).
	g.emit("add x1, x19, x20") // src = base + low
	g.emit("mov x2, x22")      // n
	g.emit("mov x0, x23")      // dst
	g.emit("bl __fern_memcpy")
	g.emit("mov x0, x23") // return data ptr
	g.label(".Lstrslice_ret")
	g.emit("ldr x23, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #80")
	g.emit("ret")
	g.label(".Lstrslice_trap")
	g.emitAbort("__fern_msg_str_slice")
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
	// Sign-extend the i32 bounds (#5294), as in the 1W variant above.
	g.emit("sxtw x21, w2") // low (sign-extended from i32)
	g.emit("sxtw x22, w3") // high (sign-extended from i32)
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
	// Allocate new_len bytes for the heap output via the rc-headered
	// allocator (rc=1 at data-8, payload size at data-4) so the
	// substring is a real rc-tracked string — str_inc on an alias
	// (e.g. `var w = words[i]` where the element is a slice) and
	// str_dec on drop both read that header. A raw __fern_alloc
	// buffer has none, so retaining a slice read before the
	// allocation and SIGSEGV'd. Mirrors __fern_strcat / read_file.
	g.emit("mov w0, w24")
	g.emit("bl __fern_alloc_rc1")
	g.emit("mov x23, x0") // x23 = dst
	// memcpy(dst, base_ptr + low, new_len).
	g.emit("add x1, x19, x21")
	g.emit("mov x2, x24")
	g.emit("mov x0, x23")
	g.emit("bl __fern_memcpy")
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
	g.emitAbort("__fern_msg_str_slice")
	g.sizeDirective("__str_slice")
	g.line(".ltorg")
}

// emitEnvRuntime emits `__fern_env(name)` — walks the envp
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
	g.line(".global __fern_env")
	g.typeDirective("__fern_env")
	g.label("__fern_env")
	twoWord := ast.UseTwoWordStrings(8)
	if twoWord {
		// Two-word ABI: (name_data, name_len) in (x0, x1).
		// Frame grows to 80 bytes: 16-byte inline-spill
		// scratch for name materialisation at [x29+64..+79].
		g.emit("stp x29, x30, [sp, #-80]!")
		g.emit("mov x29, sp")
		g.emit("stp x19, x20, [sp, #16]")
		g.emit("stp x21, x22, [sp, #32]")
		g.emitStrLen2W("w20", "x1")               // x20 = name byte length
		g.emitStrDataPtr2W("x19", "x0", "x1", 64) // x19 = name byte ptr
	} else {
		// Frame: 64 bytes — fp/lr (16) + x19..x22 (32) + 8 SSO scratch
		// at [x29 + 48] for materialising the name + 8 padding.
		g.emit("stp x29, x30, [sp, #-64]!")
		g.emit("mov x29, sp")
		g.emit("stp x19, x20, [sp, #16]")
		g.emit("stp x21, x22, [sp, #32]")
		g.emitStrLen("w20", "x0")         // x20 = name_len (read before materialise)
		g.emitStrDataPtr("x19", "x0", 48) // x19 = name byte ptr
	}
	g.adrpAdd("x21", "__fern_envp")
	g.emit("ldr x21, [x21]") // x21 = envp
	g.label(".Lenv_loop")
	g.emit("ldr x22, [x21]")      // x22 = envp[i]
	g.emit("cbz x22, .Lenv_none") // NULL terminator → return None
	// Compare first name_len bytes of envp[i] with name, then check '='.
	g.emit("mov x0, x22") // candidate envp entry
	g.emit("mov x1, x19") // name
	g.emit("mov x2, x20") // n
	g.emit("bl __memcmp_n_env")
	g.emit("cbnz w0, .Lenv_next") // not equal
	// Check that byte at offset name_len is '='.
	g.emit("ldrb w0, [x22, x20]")
	g.emit("cmp w0, #61") // '='
	g.emit("bne .Lenv_next")
	// Found. Build a fresh lang string holding the value after '='.
	g.emit("add x0, x22, x20")
	g.emit("add x0, x0, #1") // x0 = start of value (NUL-terminated)
	g.emit("mov x1, x0")
	g.label(".Lenv_strlen")
	g.emit("ldrb w2, [x1]")
	g.emit("cbz w2, .Lenv_strlen_done")
	g.emit("add x1, x1, #1")
	g.emit("b .Lenv_strlen")
	g.label(".Lenv_strlen_done")
	g.emit("sub x2, x1, x0") // x2 = value length
	g.emit("mov x19, x0")    // stash value src ptr
	g.emit("mov x20, x2")    // stash value len
	if twoWord {
		// Heap-form via the rc-headered allocator (rc=1 at data-8,
		// payload size at data-4): the returned Some(string) is an
		// owned rc string dropped via __fern_str_dec, which reads
		// that header. A plain __fern_alloc buffer has none, so the
		// drop reads garbage and corrupts the heap — the same arm64
		// two-word bug as string_from_bytes_unchecked (#2817). Length lives in
		// the box len@16 word, so no length prefix is needed.
		g.emit("mov x0, x2")
		g.emit("bl __fern_alloc_rc1")
		g.emit("mov x22, x0") // x22 = data ptr (= base+8)
		g.emit("mov x0, x22")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __fern_memcpy")
		// Build Option[string]: 24-byte box {tag@0, data@8,
		// len@16}.
		g.emit("mov x0, #24")
		g.emit("bl __fern_alloc_box")
		g.emit("str wzr, [x0]")      // tag = 0 (Some)
		g.emit("str x22, [x0, #8]")  // data
		g.emit("str x20, [x0, #16]") // len
		g.emit("b .Lenv_done")
	} else {
		// L2 rc-header layout — see __fern_strcat.
		g.emit("mov x0, x2")
		g.emit("bl __fern_alloc_rc1")
		g.emit("mov x22, x0") // x22 = data ptr (= base+8)
		g.emitStrLenStore("w20", "x22")
		g.emit("mov x0, x22")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __fern_memcpy")
		g.emit("mov x0, #16")
		g.emit("bl __fern_alloc_box")
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
	g.emit("bl __fern_alloc_box")
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
	g.sizeDirective("__fern_env")
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

// emitTcpListenRuntime emits `__fern_tcp_listen(port)` —
// opens a TCP listening socket on 0.0.0.0:port. Returns the
// listener fd on success, or `-errno` on failure. C-style
// API; callers check `if (fd < 0)`.
//
// Steps: socket(AF_INET, SOCK_STREAM, 0); bind to a stack-
// allocated sockaddr_in; listen with backlog=128.
func (g *generator) emitTcpListenRuntime() {
	g.line("")
	g.line(".global __fern_tcp_listen")
	g.typeDirective("__fern_tcp_listen")
	g.label("__fern_tcp_listen")
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
	g.emit("strh w0, [sp]") // sin_family
	g.emit("rev16 w0, w19") // htons(port)
	g.emit("strh w0, [sp, #2]")
	g.emit("str wzr, [sp, #4]") // sin_addr = 0
	g.emit("str xzr, [sp, #8]") // sin_zero[0..7]
	// bind(fd, sa, 16)
	g.emit("mov x0, x20")
	g.emit("mov x1, sp")
	g.emit("mov x2, #16")
	g.syscall("bind")
	g.emit("add sp, sp, #16") // pop sockaddr_in
	g.emit("cmp x0, #0")
	g.emit("blt .Ltcp_lst_err")
	// listen(fd, 128)
	g.emit("mov x0, x20")
	g.emit("mov x1, #128")
	g.syscall("listen")
	g.emit("cmp x0, #0")
	g.emit("blt .Ltcp_lst_err")
	g.emit("mov x0, x20") // return fd
	g.emit("b .Ltcp_lst_done")
	g.label(".Ltcp_lst_err")
	// x0 holds -errno from the failed syscall.
	g.label(".Ltcp_lst_done")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__fern_tcp_listen")
	g.line(".ltorg")
}

// emitTcpConnectRuntime emits `__fern_tcp_connect(host_be, port)` — the
// outbound client primitive (arm64 mirror of the x86-64 helper).
// host_be is the IPv4 in network byte order packed into an i32 (drops
// straight into sin_addr); returns the connected fd or -errno.
func (g *generator) emitTcpConnectRuntime() {
	g.line("")
	g.line(".global __fern_tcp_connect")
	g.typeDirective("__fern_tcp_connect")
	g.label("__fern_tcp_connect")
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("str x21, [sp, #32]")
	g.emit("mov x19, x0") // host_be
	g.emit("mov x20, x1") // port
	// socket(AF_INET=2, SOCK_STREAM=1, 0)
	g.emit("mov x0, #2")
	g.emit("mov x1, #1")
	g.emit("mov x2, #0")
	g.syscall("socket")
	g.emit("cmp x0, #0")
	g.emit("blt .Ltcp_con_err")
	g.emit("mov x21, x0") // fd
	// sockaddr_in on the stack.
	g.emit("sub sp, sp, #16")
	g.emit("mov w0, #2")
	g.emit("strh w0, [sp]") // sin_family
	g.emit("rev16 w0, w20") // htons(port)
	g.emit("strh w0, [sp, #2]")
	g.emit("str w19, [sp, #4]") // sin_addr = host_be (network order)
	g.emit("str xzr, [sp, #8]") // sin_zero
	// connect(fd, sa, 16)
	g.emit("mov x0, x21")
	g.emit("mov x1, sp")
	g.emit("mov x2, #16")
	g.syscall("connect")
	g.emit("add sp, sp, #16")
	g.emit("cmp x0, #0")
	g.emit("blt .Ltcp_con_err")
	g.emit("mov x0, x21") // return fd
	g.emit("b .Ltcp_con_done")
	g.label(".Ltcp_con_err")
	// x0 holds -errno from the failed syscall.
	g.label(".Ltcp_con_done")
	g.emit("ldr x21, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__fern_tcp_connect")
	g.line(".ltorg")
}

// emitTcpAcceptRuntime emits `__fern_tcp_accept(fd)` —
// accepts a connection on the listener fd, returns the new
// connection fd or `-errno`. Passes NULL addr/addrlen
// out-params; callers don't need the peer address.
func (g *generator) emitTcpAcceptRuntime() {
	g.line("")
	g.line(".global __fern_tcp_accept")
	g.typeDirective("__fern_tcp_accept")
	g.label("__fern_tcp_accept")
	// x0 = listener fd (already in x0 from caller).
	g.emit("mov x1, #0") // addr = NULL
	g.emit("mov x2, #0") // addrlen = NULL
	g.syscall("accept")
	g.emit("ret")
	g.sizeDirective("__fern_tcp_accept")
	g.line(".ltorg")
}

// emitTcpRecvRuntime emits `__fern_tcp_recv(fd, max)` —
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
	g.line(".global __fern_tcp_recv")
	g.typeDirective("__fern_tcp_recv")
	g.label("__fern_tcp_recv")
	twoWord := ast.UseTwoWordStrings(8)
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("str x21, [sp, #32]")
	g.emit("mov x19, x0") // x19 = fd
	g.emit("mov x20, x1") // x20 = max
	if twoWord {
		// Two-word heap form: alloc max bytes (no prefix /
		// NUL); return (data, len) in (x0, x1). rc-headered alloc (rc=1
		// @data-8, size @data-4) so __fern_str_dec reclaims the owned string
		// correctly; plain __fern_alloc corrupts the heap (#2817 class).
		g.emit("mov x0, x20")
		g.emit("bl __fern_alloc_rc1")
		g.emit("mov x21, x0") // x21 = dst (= base+8)
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
		// L2 rc-header layout — see __fern_strcat. Payload = max + 1 NUL.
		g.emit("add x0, x20, #1")
		g.emit("bl __fern_alloc_rc1")
		g.emit("mov x21, x0") // x21 = data ptr (= base+8)
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
	g.sizeDirective("__fern_tcp_recv")
	g.line(".ltorg")
}

// emitTcpSendRuntime emits `__fern_tcp_send(fd, data)` —
// writes the entire string to the fd via `write(2)`. Returns
// the syscall result (bytes written or `-errno`).
func (g *generator) emitTcpSendRuntime() {
	g.line("")
	g.line(".global __fern_tcp_send")
	g.typeDirective("__fern_tcp_send")
	g.label("__fern_tcp_send")
	if ast.UseTwoWordStrings(8) {
		// x0 = fd, x1 = data, x2 = len.
		g.emit("stp x29, x30, [sp, #-48]!")
		g.emit("mov x29, sp")
		g.emit("mov w3, w0")                     // w3 = fd
		g.emitStrLen2W("w4", "x2")               // w4 = byte length
		g.emitStrDataPtr2W("x1", "x1", "x2", 16) // x1 = byte ptr
		g.emit("mov w0, w3")                     // x0 = fd
		g.emit("mov x2, x4")                     // x2 = byte length
		g.syscall("write")
		g.emit("ldp x29, x30, [sp], #48")
		g.emit("ret")
		g.sizeDirective("__fern_tcp_send")
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
	g.sizeDirective("__fern_tcp_send")
	g.line(".ltorg")
}

// emitWasmTimerPollableRuntime emits `__fern_wasm_timer_pollable(ns)` — returns
// -1 on native (no pollable to make; the deadline is poll(2)'s timeout arg, and
// -1 is an fd poll(2) ignores). Lets std/async's with_deadline append a "timer"
// to the poll set portably; on wasm this symbol is the real pollable instead.
func (g *generator) emitWasmTimerPollableRuntime() {
	g.line("")
	g.line(".global __fern_wasm_timer_pollable")
	g.typeDirective("__fern_wasm_timer_pollable")
	g.label("__fern_wasm_timer_pollable")
	g.emit("mov w0, #-1") // no native pollable; -1 is ignored by poll(2)
	g.emit("ret")
	g.sizeDirective("__fern_wasm_timer_pollable")
	g.line(".ltorg")
}

// emitWasmPollRuntime emits `__fern_wasm_poll(pollables)` — returns -1 on native
// (no real pollables; native readiness rides poll(2) directly), ignoring its
// array arg. On wasm this symbol is the real wasi:io/poll.poll(list<pollable>)
// multiplexer instead.
func (g *generator) emitWasmPollRuntime() {
	g.line("")
	g.line(".global __fern_wasm_poll")
	g.typeDirective("__fern_wasm_poll")
	g.label("__fern_wasm_poll")
	g.emit("mov w0, #-1") // no native pollables; nothing ready
	g.emit("ret")
	g.sizeDirective("__fern_wasm_poll")
	g.line(".ltorg")
}

// emitWasmPollableDropRuntime emits `__fern_wasm_pollable_drop(p)` — a no-op
// on native (a pollable is just an fd; the socket fd is closed via tcp_close).
// Returns 0. Lets std/async's fetch_future drop the wasm pollable portably.
func (g *generator) emitWasmPollableDropRuntime() {
	g.line("")
	g.line(".global __fern_wasm_pollable_drop")
	g.typeDirective("__fern_wasm_pollable_drop")
	g.label("__fern_wasm_pollable_drop")
	g.emit("mov w0, #0") // return 0 (no-op)
	g.emit("ret")
	g.sizeDirective("__fern_wasm_pollable_drop")
	g.line(".ltorg")
}

// emitWasmBlockRuntime emits `__fern_wasm_block(p)` — a no-op on native (there's
// no pollable to wait on; a deadline comes from poll(2)'s own timeout arg).
// Returns 0. Lets std/async's with_deadline block on a timer pollable portably;
// on wasm this symbol is the real wasi:io/poll.[method]pollable.block instead.
func (g *generator) emitWasmBlockRuntime() {
	g.line("")
	g.line(".global __fern_wasm_block")
	g.typeDirective("__fern_wasm_block")
	g.label("__fern_wasm_block")
	g.emit("mov w0, #0") // return 0 (no-op)
	g.emit("ret")
	g.sizeDirective("__fern_wasm_block")
	g.line(".ltorg")
}

// emitTcpPollableRuntime emits `__fern_tcp_pollable(fd)` — on native the
// readiness token for a socket IS its fd (ppoll(2) takes fds directly), so
// this is the identity: the fd argument is already in w0/x0, just return it.
// Lets `std/async`'s `fetch_future` build a portable `Pending(tcp_pollable(fd), …)`
// (on wasm `tcp_pollable` yields a real wasi:io/poll pollable handle).
func (g *generator) emitTcpPollableRuntime() {
	g.line("")
	g.line(".global __fern_tcp_pollable")
	g.typeDirective("__fern_tcp_pollable")
	g.label("__fern_tcp_pollable")
	g.emit("ret") // fd argument already in w0/x0 → identity
	g.sizeDirective("__fern_tcp_pollable")
	g.line(".ltorg")
}

// emitTcpCloseRuntime emits `__fern_tcp_close(fd)` — thin
// wrapper around `close(2)`. Returns 0 or `-errno`.
func (g *generator) emitTcpCloseRuntime() {
	g.line("")
	g.line(".global __fern_tcp_close")
	g.typeDirective("__fern_tcp_close")
	g.label("__fern_tcp_close")
	g.syscall("close")
	g.emit("ret")
	g.sizeDirective("__fern_tcp_close")
	g.line(".ltorg")
}

// emitWriteRuntime emits `__fern_write(s_data, s_len)` —
// single write(1, buf, byteLen) syscall, no trailing newline.
// Under the two-word ABI the string arrives as a (data, len)
// pair in (x0, x1). Byte length is extracted from x1 via
// emitStrLen2W; the byte pointer materialises via
// emitStrDataPtr2W (handles inline spill at [x29-16..x29-1]).
func (g *generator) emitWriteRuntime() {
	g.line("")
	g.line(".global __fern_write")
	g.typeDirective("__fern_write")
	g.label("__fern_write")
	if ast.UseTwoWordStrings(8) {
		// Frame: 48 bytes — fp/lr (16) + 16-byte scratch for
		// inline-spill at [x29+16..x29+31] + 16 align pad.
		g.emit("stp x29, x30, [sp, #-48]!")
		g.emit("mov x29, sp")
		g.emitStrLen2W("w2", "x1")               // x2 = byte length
		g.emitStrDataPtr2W("x1", "x0", "x1", 16) // x1 = byte ptr; spill scratch at [x29+16]
		g.emit("mov x0, #1")                     // fd = stdout
		g.syscall("write")
		g.emit("ldp x29, x30, [sp], #48")
		g.emit("ret")
		g.sizeDirective("__fern_write")
		g.line(".ltorg")
		return
	}
	// Legacy single-register native ABI.
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emitStrLen("w2", "x0")         // x2 = length
	g.emitStrDataPtr("x1", "x0", 16) // x1 = byte ptr (buf)
	g.emit("mov x0, #1")             // x0 = fd (stdout)
	g.syscall("write")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__fern_write")
	g.line(".ltorg")
}

// emitPutsRuntime emits `__fern_puts(s)` — write the string,
// then a single trailing newline. Two write(2) calls keeps the
// code simple at the cost of one extra kernel transition; per-
// call cost is dominated by the syscall itself either way.
// Preserves x19 across the second write so we can return the
// original data pointer for libc-puts consistency.
func (g *generator) emitPutsRuntime() {
	g.line("")
	g.line(".global __fern_puts")
	g.typeDirective("__fern_puts")
	g.label("__fern_puts")
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
		g.sizeDirective("__fern_puts")
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
	g.sizeDirective("__fern_puts")
	g.line(".ltorg")
}

// emitPutcharRuntime emits `__fern_putchar(c)` — write the
// low byte of x0 to fd 1. We materialise the byte on the
// caller's stack frame so the kernel has a real address to
// read from (the byte itself is a register value).
func (g *generator) emitPutcharRuntime() {
	g.line("")
	g.line(".global __fern_putchar")
	g.typeDirective("__fern_putchar")
	g.label("__fern_putchar")
	g.emit("sub sp, sp, #16") // 16-byte slot for sp alignment
	g.emit("strb w0, [sp]")   // store byte on the stack
	g.emit("mov x1, sp")      // buf
	g.emit("mov x2, #1")      // len
	g.emit("mov x0, #1")      // fd
	g.syscall("write")
	g.emit("add sp, sp, #16")
	g.emit("ret")
	g.sizeDirective("__fern_putchar")
	g.line(".ltorg")
}

// emitEprintRuntime emits `__fern_eprint(s)` — stderr
// counterpart to __fern_puts. Two write(2)s to fd 2 (string +
// newline). Preserves x19 so we can return the input pointer
// for the consistency `__fern_puts` already offers.
func (g *generator) emitEprintRuntime() {
	g.line("")
	g.line(".global __fern_eprint")
	g.typeDirective("__fern_eprint")
	g.label("__fern_eprint")
	if ast.UseTwoWordStrings(8) {
		// Two-word ABI: (data, len) in (x0, x1). Frame:
		// fp/lr (16) + 16-byte inline-spill scratch at
		// [x29+16..+31] + 16 align.
		g.emit("stp x29, x30, [sp, #-48]!")
		g.emit("mov x29, sp")
		g.emitStrLen2W("w2", "x1")
		g.emitStrDataPtr2W("x1", "x0", "x1", 16)
		g.emit("mov x0, #2") // fd = stderr
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
		g.sizeDirective("__fern_eprint")
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
	g.sizeDirective("__fern_eprint")
	g.line(".ltorg")
}

// emitExitRuntime emits `__fern_exit(code)` — direct exit
// syscall. x0 already holds the user-supplied exit code from
// the caller's argument; syscallExit handles the Linux/Darwin
// ABI split. Never returns, so the trailing `ret` is for
// assembler-completeness only.
func (g *generator) emitExitRuntime() {
	g.line("")
	g.line(".global __fern_exit")
	g.typeDirective("__fern_exit")
	g.label("__fern_exit")
	if ast.LeakCheckEnabled {
		// Leak detector (#5362 slice 1): the exit() builtin bypasses the
		// _start epilogue, so report here too. The code parks in x19 —
		// clobbering a callee-save is fine on a path that never returns.
		g.emit("mov x19, x0")
		g.emit("bl __fern_lc_report")
		g.emit("mov x0, x19")
	}
	g.syscallExit()
	g.emit("ret")
	g.sizeDirective("__fern_exit")
	g.line(".ltorg")
}

// emitLcReportRuntime emits `__fern_lc_report()` — the leak detector's
// (#5362 slice 1) exit-time summary, the arm64 mirror of the x86_64
// helper. Writes one line to stderr:
//
//	leakcheck: allocs=<N> frees=<M> live_bytes=<K>
//
// where K = __fern_lc_alloc_bytes − __fern_lc_free_bytes (signed — an
// over-free would show negative rather than wrapping). Only emitted
// when ast.LeakCheckEnabled; called from the _start epilogue and
// __fern_exit, which park the exit code in x19 across the call, so the
// helper (and its two local subroutines) must touch caller-saved
// registers only. The decimal formatting is a self-contained
// divide-by-10 loop into a stack buffer (.Llc_wrnum).
func (g *generator) emitLcReportRuntime() {
	g.line("")
	g.line(".global __fern_lc_report")
	g.typeDirective("__fern_lc_report")
	g.label("__fern_lc_report")
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	g.adrpAdd("x1", ".Llc_str_allocs")
	g.emit("mov x2, #18")
	g.emit("bl .Llc_write")
	g.adrpAdd("x9", "__fern_lc_alloc_count")
	g.emit("ldr x0, [x9]")
	g.emit("bl .Llc_wrnum")
	g.adrpAdd("x1", ".Llc_str_frees")
	g.emit("mov x2, #7")
	g.emit("bl .Llc_write")
	g.adrpAdd("x9", "__fern_lc_free_count")
	g.emit("ldr x0, [x9]")
	g.emit("bl .Llc_wrnum")
	g.adrpAdd("x1", ".Llc_str_live")
	g.emit("mov x2, #12")
	g.emit("bl .Llc_write")
	g.adrpAdd("x9", "__fern_lc_alloc_bytes")
	g.emit("ldr x0, [x9]")
	g.adrpAdd("x9", "__fern_lc_free_bytes")
	g.emit("ldr x1, [x9]")
	g.emit("sub x0, x0, x1")
	g.emit("bl .Llc_wrnum")
	g.adrpAdd("x1", ".Llc_str_nl")
	g.emit("mov x2, #1")
	g.emit("bl .Llc_write")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	// .Llc_write(x1 = buf, x2 = len): one write(2) to stderr. Leaf —
	// no bl inside, so x30 (the report's return-into-report address)
	// survives.
	g.label(".Llc_write")
	g.emit("mov x0, #2")
	g.syscall("write")
	g.emit("ret")
	// .Llc_wrnum(x0 = signed i64): decimal itoa, digits built backwards
	// from the end of a 32-byte stack buffer (an i64 is at most 19
	// digits + sign), then one write(2) to stderr. Leaf.
	g.label(".Llc_wrnum")
	g.emit("sub sp, sp, #48") // 32-byte digit buffer + 16 spare
	g.emit("add x3, sp, #32")
	g.emit("mov x4, #10")
	g.emit("mov x5, #0") // sign flag
	g.emit("cmp x0, #0")
	g.emit("b.ge .Llc_wrnum_loop")
	g.emit("neg x0, x0")
	g.emit("mov x5, #1")
	g.label(".Llc_wrnum_loop")
	g.emit("udiv x6, x0, x4")
	g.emit("msub x7, x6, x4, x0") // remainder = x0 - q*10
	g.emit("add x7, x7, #48")     // → ASCII digit
	g.emit("sub x3, x3, #1")
	g.emit("strb w7, [x3]")
	g.emit("mov x0, x6")
	g.emit("cbnz x0, .Llc_wrnum_loop")
	g.emit("cbz x5, .Llc_wrnum_emit")
	g.emit("mov x7, #45") // '-'
	g.emit("sub x3, x3, #1")
	g.emit("strb w7, [x3]")
	g.label(".Llc_wrnum_emit")
	g.emit("add x2, sp, #32")
	g.emit("sub x2, x2, x3") // len
	g.emit("mov x1, x3")
	g.emit("mov x0, #2")
	g.syscall("write")
	g.emit("add sp, sp, #48")
	g.emit("ret")
	g.sizeDirective("__fern_lc_report")
	g.line(".ltorg")
}

// emitStrBufRuntime emits the three global mutable-string-builder
// helpers and the BSS scratch they share. arm64 mirror of the
// x86_64 emission — see that comment for the user-facing spec.
//
// The strbuf is a 64 MiB BSS region + a single 8-byte length
// counter. Heap-form output only (no inline-SSO encoding) since
// the asm-self-host use case always exceeds the 7-byte inline cap.
func (g *generator) emitStrBufRuntime() {
	twoWord := ast.UseTwoWordStrings(8)

	g.line("")
	if g.darwin {
		// Mach-O zero-initialised data lives in __DATA,__bss; switch
		// back with the plain `.text` directive (valid on both ELF and
		// Mach-O). Mirrors the darwin gating on the alloc/BSS emit above.
		g.line(".section __DATA,__bss")
	} else {
		g.line(".section .bss")
	}
	g.line(".align 8")
	g.label("__fern_strbuf_len")
	g.emit(".skip 8")
	g.line(".align 8")
	g.label("__fern_strbuf_data")
	g.emit(".skip 67108864") // 64 MiB
	g.line(".text")

	// __fern_strbuf_reset(): len = 0.
	g.line("")
	g.line(".global __fern_strbuf_reset")
	g.typeDirective("__fern_strbuf_reset")
	g.label("__fern_strbuf_reset")
	g.adrpAdd("x0", "__fern_strbuf_len")
	g.emit("str xzr, [x0]")
	g.emit("ret")
	g.sizeDirective("__fern_strbuf_reset")

	// __fern_strbuf_append: (x0, x1) = (data, len-with-tag) on two-
	// word ABI, (x0) = string ptr on legacy. Materialise byte ptr +
	// byte length via the SSO-aware helpers, memcpy into the BSS
	// buffer past the current tail, bump the counter.
	g.line("")
	g.line(".global __fern_strbuf_append")
	g.typeDirective("__fern_strbuf_append")
	g.label("__fern_strbuf_append")
	if twoWord {
		// Frame: fp/lr (16) + x19/x20 (16) + 16-byte spill for inline
		// data + 16 align = 64.
		g.emit("stp x29, x30, [sp, #-64]!")
		g.emit("mov x29, sp")
		g.emit("stp x19, x20, [sp, #16]")
		g.emit("mov x19, x0")                       // a_data
		g.emit("mov x20, x1")                       // a_len-with-tag
		g.emitStrLen2W("w20", "x20")                // w20 = byte length (untagged)
		g.emitStrDataPtr2W("x19", "x19", "x20", 32) // x19 = byte ptr (after SSO spill if needed)
		// dst = strbuf_data + strbuf_len
		g.adrpAdd("x2", "__fern_strbuf_len")
		g.emit("ldr x3, [x2]")
		g.adrpAdd("x0", "__fern_strbuf_data")
		g.emit("add x0, x0, x3")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __fern_memcpy")
		// bump len
		g.adrpAdd("x2", "__fern_strbuf_len")
		g.emit("ldr x3, [x2]")
		g.emit("add x3, x3, x20")
		g.emit("str x3, [x2]")
		g.emit("ldp x19, x20, [sp, #16]")
		g.emit("ldp x29, x30, [sp], #64")
		g.emit("ret")
	} else {
		// Legacy single-pointer ABI: length at [x0 - 4].
		g.emit("stp x29, x30, [sp, #-32]!")
		g.emit("mov x29, sp")
		g.emit("str x19, [sp, #16]")
		g.emit("mov x19, x0")
		g.emitStrLen("w20", "x19")         // w20 = byte length
		g.emitStrDataPtr("x19", "x19", 24) // x19 = byte ptr
		g.adrpAdd("x2", "__fern_strbuf_len")
		g.emit("ldr x3, [x2]")
		g.adrpAdd("x0", "__fern_strbuf_data")
		g.emit("add x0, x0, x3")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __fern_memcpy")
		g.adrpAdd("x2", "__fern_strbuf_len")
		g.emit("ldr x3, [x2]")
		g.emit("add x3, x3, x20")
		g.emit("str x3, [x2]")
		g.emit("ldr x19, [sp, #16]")
		g.emit("ldp x29, x30, [sp], #32")
		g.emit("ret")
	}
	g.sizeDirective("__fern_strbuf_append")

	// __fern_strbuf_take(): allocate fresh buffer of current len,
	// memcpy from strbuf, reset len, return string.
	g.line("")
	g.line(".global __fern_strbuf_take")
	g.typeDirective("__fern_strbuf_take")
	g.label("__fern_strbuf_take")
	if twoWord {
		// Two-word return: (x0 = data ptr, x1 = byte length). Heap
		// form only (no inline-SSO output since len is usually huge).
		// Frame: fp/lr (16) + x19/x20 (16) = 32.
		g.emit("stp x29, x30, [sp, #-32]!")
		g.emit("mov x29, sp")
		g.emit("stp x19, x20, [sp, #16]")
		g.adrpAdd("x0", "__fern_strbuf_len")
		g.emit("ldr x19, [x0]")
		// alloc_rc1 x19 bytes — like __fern_strcat's two-word path,
		// the result is rc-tracked by the IR (freeEligible on a
		// strbuf_take local now fires on arm64 too), so it needs the
		// rc=1 header + payload size at data-4 for __fern_str_dec's
		// box_free to read correctly. Plain __fern_alloc here would
		// hand back a header-less buffer whose data-8 is garbage,
		// segfaulting the local-side str_dec at scope exit.
		g.emit("mov x0, x19")
		g.emit("bl __fern_alloc_rc1")
		g.emit("mov x20, x0")
		// memcpy(dst, strbuf_data, len)
		g.emit("mov x0, x20")
		g.adrpAdd("x1", "__fern_strbuf_data")
		g.emit("mov x2, x19")
		g.emit("bl __fern_memcpy")
		// reset
		g.adrpAdd("x0", "__fern_strbuf_len")
		g.emit("str xzr, [x0]")
		// return (dst, len) — no SSO tag for plain heap form.
		g.emit("mov x0, x20")
		g.emit("mov x1, x19")
		g.emit("ldp x19, x20, [sp, #16]")
		g.emit("ldp x29, x30, [sp], #32")
		g.emit("ret")
	} else {
		// Legacy single-pointer ABI: alloc len+4 bytes, write length
		// prefix at [base], data at [base+4], return base+4.
		g.emit("stp x29, x30, [sp, #-32]!")
		g.emit("mov x29, sp")
		g.emit("stp x19, x20, [sp, #16]")
		g.adrpAdd("x0", "__fern_strbuf_len")
		g.emit("ldr x19, [x0]") // x19 = len
		// L2 rc-header layout — see __fern_strcat. Payload = len data only.
		g.emit("mov x0, x19")
		g.emit("bl __fern_alloc_rc1")
		g.emit("mov x20, x0") // x20 = data ptr (= base+8)
		g.emitStrLenStore("w19", "x20")
		// memcpy(data, strbuf_data, len)
		g.emit("mov x0, x20")
		g.adrpAdd("x1", "__fern_strbuf_data")
		g.emit("mov x2, x19")
		g.emit("bl __fern_memcpy")
		// reset
		g.adrpAdd("x0", "__fern_strbuf_len")
		g.emit("str xzr, [x0]")
		g.emit("mov x0, x20")
		g.emit("ldp x19, x20, [sp, #16]")
		g.emit("ldp x29, x30, [sp], #32")
		g.emit("ret")
	}
	g.sizeDirective("__fern_strbuf_take")
	g.line(".ltorg")
}

// emitNowUnixMsRuntime emits `__fern_now_unix_ms()` — wall-
// clock milliseconds since the Unix epoch. Calls Linux
// `clock_gettime(CLOCK_REALTIME, &ts)` (asm-generic syscall
// 113); the kernel writes a `struct timespec { i64 tv_sec;
// i64 tv_nsec }` to the caller-provided pointer. We compute
// `tv_sec * 1000 + tv_nsec / 1_000_000` and return it in x0.
//
// Stack frame: 32 bytes — 16 for the saved fp/lr pair, 16
// for the timespec buffer. The buffer lives at sp+16 so the
// frame pointer stays at sp.
//
// Errno is ignored: a failed `clock_gettime` (only realistic
// failure modes are EFAULT or EINVAL, neither of which can
// happen here — we control both the clock id and the
// buffer) would write -errno to x0, which we'd then
// arithmetic-massage into nonsense — preferable to forking
// the calling convention to return an Option.
func (g *generator) emitNowUnixMsRuntime() {
	g.line("")
	g.line(".global __fern_now_unix_ms")
	g.typeDirective("__fern_now_unix_ms")
	g.label("__fern_now_unix_ms")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	if g.darwin {
		// Darwin has no clock_gettime syscall; use gettimeofday(tp, NULL),
		// which fills `struct timeval { i64 tv_sec @0; i32 tv_usec @8 }`.
		// ms = tv_sec*1000 + tv_usec/1000.
		g.emit("add x0, sp, #16") // timeval buffer
		g.emit("mov x1, #0")      // tz = NULL
		g.emit("mov x16, #%d", darGettimeofday)
		g.emit("svc #0x80")
		g.emit("ldr x9, [sp, #16]") // tv_sec (i64)
		g.emit("mov x10, #1000")
		g.emit("mul x9, x9, x10")    // sec * 1000
		g.emit("ldr w11, [sp, #24]") // tv_usec (i32, 0..1e6)
		g.emit("mov x10, #1000")
		g.emit("udiv x11, x11, x10") // usec / 1000
		g.emit("add x0, x9, x11")    // result
		g.emit("ldp x29, x30, [sp], #32")
		g.emit("ret")
		g.sizeDirective("__fern_now_unix_ms")
		return
	}
	// timespec buffer at sp+16.
	g.emit("add x1, sp, #16")
	g.emit("mov x0, #0") // CLOCK_REALTIME
	g.emit("mov x8, #%d", sysClockGettime)
	g.emit("svc #0")
	g.emit("ldr x9, [sp, #16]") // tv_sec (i64)
	g.emit("mov x10, #1000")
	g.emit("mul x9, x9, x10")    // sec * 1000
	g.emit("ldr x11, [sp, #24]") // tv_nsec (i64, always 0..1e9)
	// 1_000_000 (0xF4240) is 20 bits — beyond `mov`'s 16-bit
	// immediate range. Use the literal-pool form instead; the
	// pool is flushed by the `.ltorg` at the function's end.
	g.emit("ldr x10, =1000000")
	g.emit("udiv x11, x11, x10") // nsec / 1_000_000
	g.emit("add x0, x9, x11")    // result
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__fern_now_unix_ms")
	g.line(".ltorg")
}

// emitMonotonicNsRuntime emits `__fern_monotonic_ns()` —
// monotonic nanoseconds in x0 as i64. On Linux:
// `clock_gettime(CLOCK_MONOTONIC, &ts)` (asm-generic syscall
// 113) → `tv_sec * 1e9 + tv_nsec`. On Darwin (no clock_gettime
// syscall): read the architectural counter CNTVCT_EL0 and scale
// by its frequency CNTFRQ_EL0 (24 MHz on Apple Silicon) to
// nanoseconds, computed as `(count/freq)*1e9 +
// ((count%freq)*1e9)/freq` to avoid the u64 overflow a bare
// `count*1e9` hits over long uptimes. The monotonic clock has an
// unspecified zero, so only the DELTA between two readings is
// meaningful — what benchmark timing wants. Mirrors the
// self-host asm_arm64.fern recipe.
func (g *generator) emitMonotonicNsRuntime() {
	g.line("")
	g.line(".global __fern_monotonic_ns")
	g.typeDirective("__fern_monotonic_ns")
	g.label("__fern_monotonic_ns")
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	if g.darwin {
		g.emit("mrs x9, cntvct_el0")     // count
		g.emit("mrs x10, cntfrq_el0")    // freq (Hz)
		g.emit("udiv x11, x9, x10")      // sec = count / freq
		g.emit("msub x12, x11, x10, x9") // rem = count - sec*freq
		g.emit("ldr x13, =1000000000")
		g.emit("mul x11, x11, x13")  // sec * 1e9
		g.emit("mul x12, x12, x13")  // rem * 1e9
		g.emit("udiv x12, x12, x10") // (rem*1e9) / freq
		g.emit("add x0, x11, x12")   // ns
		g.emit("ldp x29, x30, [sp], #16")
		g.emit("ret")
		g.sizeDirective("__fern_monotonic_ns")
		g.line(".ltorg")
		return
	}
	g.emit("sub sp, sp, #16") // timespec
	g.emit("mov x0, #1")      // CLOCK_MONOTONIC
	g.emit("mov x1, sp")      // &timespec
	g.emit("mov x8, #%d", sysClockGettime)
	g.emit("svc #0")
	g.emit("ldr x9, [sp]") // tv_sec
	g.emit("ldr x10, =1000000000")
	g.emit("mul x9, x9, x10")   // sec * 1e9
	g.emit("ldr x11, [sp, #8]") // tv_nsec
	g.emit("add x0, x9, x11")
	g.emit("mov sp, x29")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.sizeDirective("__fern_monotonic_ns")
	g.line(".ltorg")
}

// emitNowNsRuntime emits `__fern_now_ns()` — wall-clock
// nanoseconds since the Unix epoch in x0 as i64. On Linux:
// `clock_gettime(CLOCK_REALTIME, &ts)` (asm-generic syscall 113)
// → `tv_sec * 1e9 + tv_nsec`. On Darwin (no clock_gettime
// syscall): `gettimeofday` (BSD 116) fills `struct timeval
// { i64 tv_sec @0; i32 tv_usec @8 }`, scaled to ns as
// `tv_sec * 1e9 + tv_usec * 1000`. The nanosecond-resolution twin
// of now_unix_ms (same realtime clock); errno ignored (fixed
// clock id + stack buffer we control).
func (g *generator) emitNowNsRuntime() {
	g.line("")
	g.line(".global __fern_now_ns")
	g.typeDirective("__fern_now_ns")
	g.label("__fern_now_ns")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	if g.darwin {
		g.emit("add x0, sp, #16") // timeval buffer
		g.emit("mov x1, #0")      // tz = NULL
		g.emit("mov x16, #%d", darGettimeofday)
		g.emit("svc #0x80")
		g.emit("ldr x9, [sp, #16]") // tv_sec (i64)
		g.emit("ldr x10, =1000000000")
		g.emit("mul x9, x9, x10")    // sec * 1e9
		g.emit("ldr w11, [sp, #24]") // tv_usec (i32, 0..1e6)
		g.emit("mov x12, #1000")
		g.emit("mul x11, x11, x12") // usec * 1000 = ns
		g.emit("add x0, x9, x11")   // result
		g.emit("ldp x29, x30, [sp], #32")
		g.emit("ret")
		g.sizeDirective("__fern_now_ns")
		g.line(".ltorg")
		return
	}
	g.emit("add x1, sp, #16") // &timespec
	g.emit("mov x0, #0")      // CLOCK_REALTIME
	g.emit("mov x8, #%d", sysClockGettime)
	g.emit("svc #0")
	g.emit("ldr x9, [sp, #16]") // tv_sec (i64)
	g.emit("ldr x10, =1000000000")
	g.emit("mul x9, x9, x10")    // sec * 1e9
	g.emit("ldr x11, [sp, #24]") // tv_nsec (i64, 0..1e9)
	g.emit("add x0, x9, x11")    // result
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__fern_now_ns")
	g.line(".ltorg")
}

// emitSleepMsRuntime emits `__fern_sleep_ms(ms)` — pause for
// `ms` milliseconds (arg in x0, i64). ms <= 0 returns at once.
// Splits ms into a timespec `{ tv_sec = ms/1000; tv_nsec =
// (ms%1000)*1e6 }` on the stack. On Linux: `nanosleep(&req,
// NULL)` (syscall 101). On Darwin (no nanosleep syscall):
// `select(0, NULL, NULL, NULL, &timeout)` with a `timeval
// { tv_sec; tv_usec = (ms%1000)*1000 }` (BSD 93). Void — the
// operand-stack push is gated off by returnIsVoid. Best-effort
// (errno / early-wake remainder ignored), matching the self-host
// recipe and the interpreter.
func (g *generator) emitSleepMsRuntime() {
	g.line("")
	g.line(".global __fern_sleep_ms")
	g.typeDirective("__fern_sleep_ms")
	g.label("__fern_sleep_ms")
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	g.emit("sub sp, sp, #32")
	g.emit("cmp x0, #0")
	g.emit("b.le .Lsleep_ms_done")
	g.emit("mov x9, #1000")
	g.emit("udiv x10, x0, x9")      // sec
	g.emit("msub x11, x10, x9, x0") // rem ms
	g.emit("str x10, [sp]")         // tv_sec
	if g.darwin {
		g.emit("mov x12, #1000")
		g.emit("mul x11, x11, x12") // tv_usec = rem * 1000
		g.emit("str x11, [sp, #8]")
		g.emit("mov x0, #0") // nfds
		g.emit("mov x1, #0") // readfds
		g.emit("mov x2, #0") // writefds
		g.emit("mov x3, #0") // errorfds
		g.emit("mov x4, sp") // timeout
		g.emit("mov x16, #%d", darSelect)
		g.emit("svc #0x80")
	} else {
		g.emit("ldr x12, =1000000")
		g.emit("mul x11, x11, x12") // tv_nsec = rem * 1e6
		g.emit("str x11, [sp, #8]")
		g.emit("mov x0, sp") // &req
		g.emit("mov x1, #0") // rem = NULL
		g.emit("mov x8, #%d", sysNanosleep)
		g.emit("svc #0")
	}
	g.label(".Lsleep_ms_done")
	g.emit("mov sp, x29")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.sizeDirective("__fern_sleep_ms")
	g.line(".ltorg")
}

// emitProcForkRuntime emits `__fern_proc_fork()` — fork the
// process, returning 0 in the child, the child's pid in the
// parent, or -errno on failure (docs/CRASH-ONLY-SERVE.md D2').
//
// Linux: arm64 has no bare fork(2) syscall — fork is
// `clone(SIGCHLD, 0, 0, 0, 0)` (asm-generic #220; arg order
// flags, newsp, parent_tid, tls, child_tid). The kernel's return
// shape is already the contract, so nothing to normalise.
//
// Darwin: fork (BSD 2) returns the pid in x0 with x1 = 0 in the
// parent / 1 in the child (plus the usual carry-set +errno error
// convention) — mirror libsyscall's __fork stub: negate on
// error, fold x1 into the 0-in-child shape.
func (g *generator) emitProcForkRuntime() {
	g.line("")
	g.line(".global __fern_proc_fork")
	g.typeDirective("__fern_proc_fork")
	g.label("__fern_proc_fork")
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	if g.darwin {
		g.emit("mov x16, #%d", darFork)
		g.emit("svc #0x80")
		g.emit("b.cc .Lproc_fork_ok")
		g.emit("neg x0, x0") // carry set: +errno → -errno
		g.emit("b .Lproc_fork_done")
		g.label(".Lproc_fork_ok")
		g.emit("cbz x1, .Lproc_fork_done") // parent: x0 = child pid
		g.emit("mov x0, #0")               // child
		g.label(".Lproc_fork_done")
	} else {
		g.emit("mov x0, #17") // flags = SIGCHLD
		g.emit("mov x1, #0")  // newsp = 0 (share parent's, CoW)
		g.emit("mov x2, #0")  // parent_tid
		g.emit("mov x3, #0")  // tls
		g.emit("mov x4, #0")  // child_tid
		g.emit("mov x8, #%d", sysClone)
		g.emit("svc #0")
	}
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.sizeDirective("__fern_proc_fork")
}

// emitProcWaitpidRuntime emits `__fern_proc_waitpid(pid)` —
// blocking wait4 (Linux asm-generic #260 / Darwin BSD 7: pid,
// &status, options=0, rusage=NULL; status on the stack) plus the
// status-word decode, identical on both kernels:
//
//	WIFEXITED  ((status & 0x7f) == 0) → (status >> 8) & 0xff
//	else (signal death)               → 128 + (status & 0x7f)
//
// (the shell convention — a bounds-trap worker surfaces as its
// raw exit code, e.g. 134). A failing syscall returns -errno
// as-is (Darwin's carry-set +errno is negated first).
func (g *generator) emitProcWaitpidRuntime() {
	g.line("")
	g.line(".global __fern_proc_waitpid")
	g.typeDirective("__fern_proc_waitpid")
	g.label("__fern_proc_waitpid")
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	g.emit("sub sp, sp, #16") // status slot
	g.emit("sxtw x0, w0")     // pid
	g.emit("mov x1, sp")      // &status
	g.emit("mov x2, #0")      // options = 0
	g.emit("mov x3, #0")      // rusage = NULL
	if g.darwin {
		g.emit("mov x16, #%d", darWait4)
		g.emit("svc #0x80")
		g.emit("b.cc .Lproc_wait_decode")
		g.emit("neg x0, x0") // carry set: +errno → -errno
		g.emit("b .Lproc_wait_done")
		g.label(".Lproc_wait_decode")
	} else {
		g.emit("mov x8, #%d", sysWait4)
		g.emit("svc #0")
		g.emit("cmp x0, #0")
		g.emit("b.lt .Lproc_wait_done") // -errno → return as-is
	}
	g.emit("ldr w9, [sp]")       // status word
	g.emit("and w10, w9, #0x7f") // termination signal (0 = exited)
	g.emit("cbnz w10, .Lproc_wait_sig")
	// Normal exit: (status >> 8) & 0xff.
	g.emit("lsr w0, w9, #8")
	g.emit("and w0, w0, #0xff")
	g.emit("b .Lproc_wait_done")
	g.label(".Lproc_wait_sig")
	// Signal death: 128 + signal.
	g.emit("add w0, w10, #128")
	g.label(".Lproc_wait_done")
	g.emit("mov sp, x29")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.sizeDirective("__fern_proc_waitpid")
}

// emitArgsRuntime emits `__fern_args()` — returns a length-
// prefixed `string[]` materialised from the argc/argv pair
// captured by emitStartRuntime. Each entry is a fresh
// length-prefixed string with a trailing NUL preserved (for
// libc-shaped consumers like `puts`). Result is cached in
// `__fern_args_cache` so repeat calls are O(1).
//
// Slot layout uses callee-save x19..x23 across the inner
// __fern_alloc / __fern_memcpy calls; AAPCS64 mandates
// preservation, so the saved-pair pattern at function entry
// keeps them coherent across the bl chain.
func (g *generator) emitArgsRuntime() {
	g.line("")
	g.line(".global __fern_args")
	g.typeDirective("__fern_args")
	g.label("__fern_args")
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
	g.adrpAdd("x0", "__fern_args_cache")
	g.emit("ldr x1, [x0]")
	g.emit("cbz x1, .Largs_build")
	g.emit("mov x0, x1")
	g.emit("b .Largs_ret")
	g.label(".Largs_build")
	// x19 = argc, x20 = argv (pointer to char**)
	g.adrpAdd("x19", "__fern_argc")
	g.emit("ldr x19, [x19]")
	g.adrpAdd("x20", "__fern_argv")
	g.emit("ldr x20, [x20]")
	// Allocate the result string[] container: 16-byte header
	// (pad + cap + rc + len, each 4 bytes) + argc * 8 bytes
	// for entry pointers. The 16-byte header keeps element 0
	// at a 16-aligned offset so Apple Silicon's stricter
	// alignment is satisfied; cap / rc / len sit at the
	// canonical data - 12 / -8 / -4 offsets.
	g.emit("lsl x0, x19, #3")
	g.emit("add x0, x0, #16")
	g.emit("bl __fern_alloc")
	g.emit("add x21, x0, #16")      // x21 = result data pointer (16-aligned)
	g.emit("stur w19, [x21, #-12]") // cap = argc (Phase 2-prep)
	g.emit("mov w9, #1")
	g.emit("stur w9, [x21, #-8]")  // rc = 1 (phase 1 of RC rollout)
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
	g.emit("sub x0, x0, x23") // x0 = strlen
	g.emit("mov x9, x0")      // x9 = saved strlen (caller-save, not preserved across bl)
	// L2 rc-header layout — see __fern_strcat. Payload = strlen + 1 NUL.
	g.emit("add x0, x0, #1")
	g.emit("bl __fern_alloc_rc1")
	g.emit("mov x10, x0")         // x10 = string data pointer (= base+8)
	g.emit("stur w9, [x10, #-4]") // length prefix at data-4
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
	g.emit("bl __fern_memcpy")
	// result[i] = x10 — full 8-byte pointer store.
	g.emit("ldr x10, [sp, #56]")
	g.emit("str x10, [x21, x22, lsl #3]")
	g.emit("add x22, x22, #1")
	g.emit("b .Largs_loop")
	g.label(".Largs_done")
	// Cache + return.
	g.adrpAdd("x0", "__fern_args_cache")
	g.emit("str x21, [x0]")
	g.emit("mov x0, x21")
	g.label(".Largs_ret")
	g.emit("ldr x23, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("__fern_args")
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
	g.adrpAdd("x0", "__fern_args_cache")
	g.emit("ldr x1, [x0]")
	g.emit("cbz x1, .Largs2w_build")
	g.emit("mov x0, x1")
	g.emit("b .Largs2w_ret")
	g.label(".Largs2w_build")
	g.adrpAdd("x19", "__fern_argc")
	g.emit("ldr x19, [x19]")
	g.adrpAdd("x20", "__fern_argv")
	g.emit("ldr x20, [x20]")
	// Allocate: 16-byte header + argc * 16 (entries are 16-byte
	// (data, len) pairs). Header is 16 bytes so element 0 sits
	// at +16 = stride-aligned; length prefix at `[base + 12]`.
	g.emit("lsl x0, x19, #4") // argc * 16
	g.emit("add x0, x0, #16") // + header
	g.emit("bl __fern_alloc")
	g.emit("add x21, x0, #16")      // x21 = data pointer (past header)
	g.emit("stur w19, [x21, #-12]") // cap = argc (Phase 2-prep)
	g.emit("mov w9, #1")
	g.emit("stur w9, [x21, #-8]")  // rc = 1 (phase 1 of RC rollout)
	g.emit("stur w19, [x21, #-4]") // length prefix = argc
	g.emit("mov x22, #0")          // loop counter i
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
	// Allocate via __fern_alloc_rc1 — the L2 rc-headed form, matching the
	// single-word variant — NOT plain __fern_alloc. The entry's `len`
	// half carries the length for readers, but every rc consumer
	// (__fern_str_inc / __fern_str_dec / the __fern_drop_arr_str element
	// sweep) unconditionally read-modify-writes the rc word at data-8 and
	// the box-free path sizes the block from the length prefix at data-4.
	// A headerless plain-alloc string made those hit the PREVIOUS argv
	// string's tail bytes: binding or dropping any args() element
	// silently incremented/decremented a byte inside a neighbouring
	// argv string — path-length-dependent corruption (openat on argv[1]
	// failing with one byte off by one; the long-standing "argv string
	// lifetime" flake that dropped suites from the native-mmc gate).
	// Payload is strlen + 1 so the trailing NUL survives for libc-shaped
	// consumers, same as the single-word variant.
	g.emit("add x0, x0, #1")
	g.emit("bl __fern_alloc_rc1")
	g.emit("mov x25, x0")          // x25 = dst (callee-save, survives bl)
	g.emit("stur w24, [x25, #-4]") // length prefix at data-4 (block sizing for box-free)
	// memcpy(dst, src, strlen + 1) — include the NUL.
	g.emit("mov x0, x25")
	g.emit("mov x1, x23")
	g.emit("add x2, x24, #1")
	g.emit("bl __fern_memcpy")
	// Write entry: data at [x21 + i*16], len at [x21 + i*16 + 8].
	g.emit("lsl x11, x22, #4")    // x11 = i * 16
	g.emit("str x25, [x21, x11]") // data
	g.emit("add x11, x11, #8")
	g.emit("str x24, [x21, x11]") // len (= strlen, heap form, top bit clear)
	g.emit("add x22, x22, #1")
	g.emit("b .Largs2w_loop")
	g.label(".Largs2w_done")
	g.adrpAdd("x0", "__fern_args_cache")
	g.emit("str x21, [x0]")
	g.emit("mov x0, x21")
	g.label(".Largs2w_ret")
	g.emit("ldr x25, [sp, #64]")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #80")
	g.emit("ret")
	g.sizeDirective("__fern_args")
	g.line(".ltorg")
}

// emitFloatTranscendentalsRuntime emits the f64 transcendental
// bundle — __fern_{sin,cos,exp,log,pow}_f64 — plus the shared
// .rodata table of polynomial coefficients. arm64 has no hardware
// transcendental instruction, so each is a range reduction followed
// by a minimax/Taylor polynomial evaluated by Horner's method (a few
// ulp, not bit-exact with the interpreter's Go math — the test
// contract is tolerance-based). Faithfully ported from the
// self-hosted compiler's asm_arm64.fern.
func (g *generator) emitFloatTranscendentalsRuntime() {
	// ldc loads an 8-byte constant from .rodata into an FP register
	// via adrp/add/ldr (x12 scratch). Mirrors asm_arm64's emit_ldc.
	ldc := func(reg, lbl string) {
		g.adrpAdd("x12", lbl)
		g.emit("ldr %s, [x12]", reg)
	}

	g.line("")
	if g.darwin {
		g.line(".section __TEXT,__const")
	} else {
		g.line(".section .rodata")
	}
	g.line(".align 3")
	for _, c := range []struct{ lbl, val string }{
		{".Lfc_log2e", "1.4426950408889634"},
		{".Lfc_ln2", "0.6931471805599453"},
		{".Lfc_halfpi", "1.5707963267948966"},
		{".Lfc_sqrt2", "1.4142135623730951"},
		{".Lfc_one", "1.0"},
		{".Lfc_half", "0.5"},
		{".Lfc_e7", "0.00019841269841269841"},
		{".Lfc_e6", "0.0013888888888888889"},
		{".Lfc_e5", "0.0083333333333333332"},
		{".Lfc_e4", "0.041666666666666664"},
		{".Lfc_e3", "0.16666666666666666"},
		{".Lfc_s7", "-0.00019841269841269841"},
		{".Lfc_s5", "0.0083333333333333332"},
		{".Lfc_s3", "-0.16666666666666666"},
		{".Lfc_c6", "-0.0013888888888888889"},
		{".Lfc_c4", "0.041666666666666664"},
		{".Lfc_c2", "-0.5"},
		{".Lfc_l11", "0.090909090909090912"},
		{".Lfc_l9", "0.1111111111111111"},
		{".Lfc_l7", "0.14285714285714285"},
		{".Lfc_l5", "0.2"},
		{".Lfc_l3", "0.33333333333333331"},
	} {
		g.label(c.lbl)
		g.line("\t.double " + c.val)
	}
	g.line(".text")

	// __fern_exp_f64(d0=x) → e^x = 2^k · poly(r), k = round(x·log2 e),
	// r = x − k·ln2 ∈ [−ln2/2, ln2/2], poly = degree-7 Taylor of e^r.
	g.line("")
	g.line(".global __fern_exp_f64")
	g.typeDirective("__fern_exp_f64")
	g.label("__fern_exp_f64")
	ldc("d1", ".Lfc_log2e")
	g.emit("fmul d2, d0, d1") // t = x*log2e
	g.emit("frinta d3, d2")   // kf = round(t)
	g.emit("fcvtzs x10, d3")  // ki
	ldc("d4", ".Lfc_ln2")
	g.emit("fmul d5, d3, d4") // kf*ln2
	g.emit("fsub d0, d0, d5") // r
	ldc("d6", ".Lfc_e7")
	ldc("d7", ".Lfc_e6")
	g.emit("fmul d6, d6, d0")
	g.emit("fadd d6, d6, d7")
	ldc("d7", ".Lfc_e5")
	g.emit("fmul d6, d6, d0")
	g.emit("fadd d6, d6, d7")
	ldc("d7", ".Lfc_e4")
	g.emit("fmul d6, d6, d0")
	g.emit("fadd d6, d6, d7")
	ldc("d7", ".Lfc_e3")
	g.emit("fmul d6, d6, d0")
	g.emit("fadd d6, d6, d7")
	ldc("d7", ".Lfc_half")
	g.emit("fmul d6, d6, d0")
	g.emit("fadd d6, d6, d7")
	ldc("d7", ".Lfc_one")
	g.emit("fmul d6, d6, d0")
	g.emit("fadd d6, d6, d7")
	g.emit("fmul d6, d6, d0")
	g.emit("fadd d6, d6, d7") // poly(r) = e^r
	g.emit("add x10, x10, #1023")
	g.emit("lsl x10, x10, #52")
	g.emit("fmov d1, x10") // 2^ki
	g.emit("fmul d0, d6, d1")
	g.emit("ret")
	g.sizeDirective("__fern_exp_f64")

	// __fern_log_f64(d0=x) → ln x (x>0). x = m·2^e, m∈[1,2) normalised
	// to [√2/2,√2); f = (m−1)/(m+1); ln(m)=2·(f+f³/3+…+f¹¹/11);
	// ln(x) = e·ln2 + ln(m).
	g.line("")
	g.line(".global __fern_log_f64")
	g.typeDirective("__fern_log_f64")
	g.label("__fern_log_f64")
	g.emit("fmov x10, d0") // bits
	g.emit("lsr x11, x10, #52")
	g.emit("and x11, x11, #0x7ff")
	g.emit("sub x11, x11, #1023") // e
	g.emit("mov x13, #1")
	g.emit("lsl x13, x13, #52")
	g.emit("sub x13, x13, #1")  // mask (1<<52)-1
	g.emit("and x10, x10, x13") // mantissa
	g.emit("mov x14, #1023")
	g.emit("lsl x14, x14, #52")
	g.emit("orr x10, x10, x14")
	g.emit("fmov d1, x10") // m in [1,2)
	ldc("d2", ".Lfc_sqrt2")
	g.emit("fcmp d1, d2")
	g.emit("b.le .Llog_nohalf")
	ldc("d3", ".Lfc_half")
	g.emit("fmul d1, d1, d3")
	g.emit("add x11, x11, #1")
	g.label(".Llog_nohalf")
	ldc("d4", ".Lfc_one")
	g.emit("fsub d5, d1, d4") // m-1
	g.emit("fadd d6, d1, d4") // m+1
	g.emit("fdiv d0, d5, d6") // f
	g.emit("fmul d7, d0, d0") // f2
	ldc("d2", ".Lfc_l11")
	ldc("d3", ".Lfc_l9")
	g.emit("fmul d2, d2, d7")
	g.emit("fadd d2, d2, d3")
	ldc("d3", ".Lfc_l7")
	g.emit("fmul d2, d2, d7")
	g.emit("fadd d2, d2, d3")
	ldc("d3", ".Lfc_l5")
	g.emit("fmul d2, d2, d7")
	g.emit("fadd d2, d2, d3")
	ldc("d3", ".Lfc_l3")
	g.emit("fmul d2, d2, d7")
	g.emit("fadd d2, d2, d3")
	g.emit("fmul d2, d2, d7")
	g.emit("fadd d2, d2, d4") // poly + 1
	g.emit("fmul d2, d2, d0") // f*poly
	g.emit("fadd d2, d2, d2") // 2*f*poly = ln(m)
	g.emit("scvtf d3, x11")   // e
	ldc("d4", ".Lfc_ln2")
	g.emit("fmul d3, d3, d4") // e*ln2
	g.emit("fadd d0, d3, d2")
	g.emit("ret")
	g.sizeDirective("__fern_log_f64")

	// __fern_sin_f64(d0=x) → sin x. k=round(x/(π/2)), r=x−k·(π/2)∈
	// [−π/4,π/4]; quadrant q=k&3 selects ±sin(r)/±cos(r).
	g.line("")
	g.line(".global __fern_sin_f64")
	g.typeDirective("__fern_sin_f64")
	g.label("__fern_sin_f64")
	g.emitSinCosReduction(ldc)
	g.emit("cmp x10, #0")
	g.emit("b.eq .Lsin_sr")
	g.emit("cmp x10, #1")
	g.emit("b.eq .Lsin_cr")
	g.emit("cmp x10, #2")
	g.emit("b.eq .Lsin_nsr")
	g.emit("fneg d0, d16") // q3: -cos(r)
	g.emit("ret")
	g.label(".Lsin_sr")
	g.emit("fmov d0, d6")
	g.emit("ret")
	g.label(".Lsin_cr")
	g.emit("fmov d0, d16")
	g.emit("ret")
	g.label(".Lsin_nsr")
	g.emit("fneg d0, d6")
	g.emit("ret")
	g.sizeDirective("__fern_sin_f64")

	// __fern_cos_f64(d0=x) → cos x. Same reduction; quadrant selects
	// cos(r)/−sin(r)/−cos(r)/sin(r).
	g.line("")
	g.line(".global __fern_cos_f64")
	g.typeDirective("__fern_cos_f64")
	g.label("__fern_cos_f64")
	g.emitSinCosReduction(ldc)
	g.emit("cmp x10, #0")
	g.emit("b.eq .Lcos_cr")
	g.emit("cmp x10, #1")
	g.emit("b.eq .Lcos_nsr")
	g.emit("cmp x10, #2")
	g.emit("b.eq .Lcos_ncr")
	g.emit("fmov d0, d6") // q3: sin(r)
	g.emit("ret")
	g.label(".Lcos_cr")
	g.emit("fmov d0, d16")
	g.emit("ret")
	g.label(".Lcos_nsr")
	g.emit("fneg d0, d6")
	g.emit("ret")
	g.label(".Lcos_ncr")
	g.emit("fneg d0, d16")
	g.emit("ret")
	g.sizeDirective("__fern_cos_f64")

	// __fern_pow_f64(d0=x, d1=y) → x^y = exp(y·ln x), x>0. Non-leaf:
	// stashes y in callee-saved d8 across the log call.
	g.line("")
	g.line(".global __fern_pow_f64")
	g.typeDirective("__fern_pow_f64")
	g.label("__fern_pow_f64")
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	g.emit("str d8, [sp, #-16]!")
	g.emit("fmov d8, d1")       // y
	g.emit("bl __fern_log_f64") // d0 = ln(x)
	g.emit("fmul d0, d8, d0")   // y*ln(x)
	g.emit("bl __fern_exp_f64")
	g.emit("ldr d8, [sp], #16")
	g.emit("ldp x29, x30, [sp], #16")
	g.emit("ret")
	g.sizeDirective("__fern_pow_f64")
	g.line(".ltorg")
}

// emitSinCosReduction emits the shared argument-reduction +
// sin(r)/cos(r) polynomial prologue for __fern_sin_f64 /
// __fern_cos_f64. On exit: x10 = quadrant (k&3), d6 = sin(r),
// d16 = cos(r), with r ∈ [−π/4, π/4].
func (g *generator) emitSinCosReduction(ldc func(reg, lbl string)) {
	ldc("d1", ".Lfc_halfpi")
	g.emit("fdiv d2, d0, d1")
	g.emit("frinta d3, d2")
	g.emit("fcvtzs x10, d3")
	g.emit("fmul d4, d3, d1")
	g.emit("fsub d0, d0, d4")  // r
	g.emit("and x10, x10, #3") // quadrant
	g.emit("fmul d5, d0, d0")  // r2
	ldc("d6", ".Lfc_s7")
	ldc("d7", ".Lfc_s5")
	g.emit("fmul d6, d6, d5")
	g.emit("fadd d6, d6, d7")
	ldc("d7", ".Lfc_s3")
	g.emit("fmul d6, d6, d5")
	g.emit("fadd d6, d6, d7")
	ldc("d7", ".Lfc_one")
	g.emit("fmul d6, d6, d5")
	g.emit("fadd d6, d6, d7")
	g.emit("fmul d6, d6, d0") // sin(r)
	ldc("d16", ".Lfc_c6")
	ldc("d17", ".Lfc_c4")
	g.emit("fmul d16, d16, d5")
	g.emit("fadd d16, d16, d17")
	ldc("d17", ".Lfc_c2")
	g.emit("fmul d16, d16, d5")
	g.emit("fadd d16, d16, d17")
	ldc("d17", ".Lfc_one")
	g.emit("fmul d16, d16, d5")
	g.emit("fadd d16, d16, d17") // cos(r)
}

// emitReadLineRuntime emits `__fern_read_line()` — reads stdin
// one byte at a time into the 4 KiB `__fern_read_line_buf`,
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
	g.line(".global __fern_read_line")
	g.typeDirective("__fern_read_line")
	g.label("__fern_read_line")
	twoWord := ast.UseTwoWordStrings(8)
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("str x21, [sp, #32]")
	g.adrpAdd("x19", "__fern_read_line_buf")
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
	g.emit("bl __fern_alloc_box")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]") // tag = 1
	g.emit("b .Lrl_ret")
	g.label(".Lrl_some")
	if twoWord {
		// Two-word heap form via the rc-headered allocator (rc=1 at
		// data-8, payload size at data-4): the returned Some(string)
		// is dropped via __fern_str_dec, which reads that header. A
		// plain __fern_alloc buffer has none — same arm64 two-word
		// heap-corruption as string_from_bytes_unchecked (#2817). Length lives
		// in the box len@16 word, so no length prefix is needed.
		g.emit("mov x0, x20")
		g.emit("bl __fern_alloc_rc1")
		g.emit("mov x21, x0") // x21 = data ptr (= base+8)
		// memcpy(x21, x19, x20).
		g.emit("mov x0, x21")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __fern_memcpy")
		// Wrap as Some(string). 24-byte box: tag@0, pad@4,
		// data@8, len@16.
		g.emit("mov x19, x21") // stash data ptr
		g.emit("mov x0, #24")
		g.emit("bl __fern_alloc_box")
		g.emit("str wzr, [x0]")      // tag = 0 (Some)
		g.emit("str x19, [x0, #8]")  // data
		g.emit("str x20, [x0, #16]") // len
		g.emit("b .Lrl_ret")
	} else {
		// L2 rc-header layout — see __fern_strcat. Payload = N data + 1 NUL.
		g.emit("add x0, x20, #1")
		g.emit("bl __fern_alloc_rc1")
		g.emit("mov x21, x0")          // x21 = data ptr (= base+8)
		g.emit("stur w20, [x21, #-4]") // length prefix at data-4
		g.emit("mov x0, x21")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __fern_memcpy")
		g.emit("add x0, x21, x20")
		g.emit("strb wzr, [x0]")
		g.emit("mov x19, x21")
		g.emit("mov x0, #16")
		g.emit("bl __fern_alloc_box")
		g.emit("str wzr, [x0]")
		g.emit("str x19, [x0, #8]")
	}
	g.label(".Lrl_ret")
	g.emit("ldr x21, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__fern_read_line")
	g.line(".ltorg")
}

// emitStdinRuntime emits `__fern_stdin()` — a 1-instruction
// stub that returns 0. The checker requires `stdin()` to be
// callable but the arm64 backend doesn't yet model per-fd
// Readers, so the receiver value is unused; any sentinel
// works.
func (g *generator) emitStdinRuntime() {
	g.line("")
	g.line(".global __fern_stdin")
	g.typeDirective("__fern_stdin")
	g.label("__fern_stdin")
	g.emit("mov x0, #0")
	g.emit("ret")
	g.sizeDirective("__fern_stdin")
	g.line(".ltorg")
}

// emitRandomBytesRuntime emits `__fern_random_bytes(n)` —
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
	g.line(".global __fern_random_bytes")
	g.typeDirective("__fern_random_bytes")
	g.label("__fern_random_bytes")
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
		g.emit("bl __fern_alloc")
		g.emit("mov x19, x0") // x19 = data ptr
	} else {
		// L2 rc-header layout — see __fern_strcat. Payload = n + 1 NUL.
		g.emit("add x0, x20, #1")
		g.emit("bl __fern_alloc_rc1")
		g.emit("mov x19, x0")          // x19 = data ptr (= base+8)
		g.emit("stur w20, [x19, #-4]") // length prefix at data-4
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
	g.sizeDirective("__fern_random_bytes")
	g.line(".ltorg")
}

// emitPollRuntime emits `__fern_poll(fds, timeout_ms)` — the std/task
// reactor's readiness multiplexer (docs/ASYNC-IMPLEMENTATION-PLAN.md
// Phase 1), the arm64 mirror of the x86-64 helper. `fds` is a length-
// prefixed i32[]; the helper builds a transient `struct pollfd[]`
// (each 8 bytes: i32 fd, i16 events, i16 revents), requests POLLIN on
// each, calls ppoll(2) (#73 — arm64 has no bare `poll`), and returns
// the INDEX of the first readable fd, or -1 on timeout / none.
// `timeout_ms` < 0 blocks indefinitely (NULL timespec); >= 0 builds a
// timespec. On Darwin the readiness path (kqueue) is not yet ported,
// so the helper returns -1 (no readiness).
func (g *generator) emitPollRuntime() {
	const pollin = 1 // POLLIN
	g.line("")
	g.line(".global __fern_poll")
	g.typeDirective("__fern_poll")
	g.label("__fern_poll")
	if g.darwin {
		// arm64-darwin readiness = kqueue, deferred. Stub: -1.
		g.emit("mov x0, #-1")
		g.emit("ret")
		g.sizeDirective("__fern_poll")
		return
	}
	// Frame: fp/lr + callee-saves x19..x23 + a 16-byte timespec scratch.
	g.emit("stp x29, x30, [sp, #-80]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]") // x19 = nfds, x20 = fds ptr
	g.emit("stp x21, x22, [sp, #32]") // x21 = pollfd buf, x22 = loop i
	g.emit("stp x23, xzr, [sp, #48]") // x23 = timeout_ms
	// timespec scratch lives at [x29, #64..79].
	g.emit("mov x20, x0") // fds ptr
	g.emit("mov x23, x1") // timeout_ms
	g.emitArrayLen("w19", "x20")
	g.emit("cmp w19, #0")
	g.emit("b.le .Lpoll_none")
	// buf = alloc(nfds * 8)
	g.emit("lsl w0, w19, #3")
	g.emit("bl __fern_alloc")
	g.emit("mov x21, x0")
	// Marshal: pollfd[i] = { fd = fds[i], events = POLLIN, revents = 0 }.
	g.emit("mov x22, #0")
	g.label(".Lpoll_fill")
	g.emit("cmp x22, x19")
	g.emit("b.ge .Lpoll_filled")
	g.emit("ldr w0, [x20, x22, lsl #2]") // fd
	g.emit("add x9, x21, x22, lsl #3")   // &pollfd[i]
	g.emit("str w0, [x9]")               // .fd
	g.emit("mov w1, #%d", pollin)
	g.emit("strh w1, [x9, #4]")  // .events
	g.emit("strh wzr, [x9, #6]") // .revents
	g.emit("add x22, x22, #1")
	g.emit("b .Lpoll_fill")
	g.label(".Lpoll_filled")
	// timespec: timeout_ms < 0 → NULL (block); else { sec, nsec }.
	g.emit("cmp x23, #0")
	g.emit("b.lt .Lpoll_infinite")
	g.emit("mov x9, #1000")
	g.emit("udiv x10, x23, x9")      // sec = ms / 1000
	g.emit("msub x11, x10, x9, x23") // rem ms = ms - sec*1000
	g.emit("ldr x12, =1000000")
	g.emit("mul x11, x11, x12") // nsec = rem * 1e6
	g.emit("add x2, x29, #64")  // &timespec
	g.emit("stp x10, x11, [x2]")
	g.emit("b .Lpoll_call")
	g.label(".Lpoll_infinite")
	g.emit("mov x2, #0") // NULL tmo_p → block
	g.label(".Lpoll_call")
	g.emit("mov x0, x21") // fds buf
	g.emit("mov w1, w19") // nfds
	g.emit("mov x3, #0")  // sigmask = NULL
	g.emit("mov x4, #0")  // sigsetsize
	g.syscall("ppoll")
	// Scan revents for the first POLLIN-ready fd.
	g.emit("mov x22, #0")
	g.label(".Lpoll_scan")
	g.emit("cmp x22, x19")
	g.emit("b.ge .Lpoll_none")
	g.emit("add x9, x21, x22, lsl #3")
	g.emit("ldrh w0, [x9, #6]") // revents
	g.emit("and w0, w0, #%d", pollin)
	g.emit("cbnz w0, .Lpoll_found")
	g.emit("add x22, x22, #1")
	g.emit("b .Lpoll_scan")
	g.label(".Lpoll_found")
	g.emit("mov x0, x22")
	g.emit("b .Lpoll_ret")
	g.label(".Lpoll_none")
	g.emit("mov x0, #-1")
	g.label(".Lpoll_ret")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldr x23, [sp, #48]")
	g.emit("ldp x29, x30, [sp], #80")
	g.emit("ret")
	g.sizeDirective("__fern_poll")
	g.line(".ltorg")
}

// emitTimerFdRuntime emits `__fern_timer_fd(ms)` — the arm64 mirror of
// the x86-64 helper: create a CLOCK_MONOTONIC timerfd readable once
// after `ms` ms, return its fd (poll/std/reactor wait on it). Linux
// only (timerfd has no Darwin equivalent); Darwin returns a -1 stub.
func (g *generator) emitTimerFdRuntime() {
	const clockMonotonic = 1
	g.line("")
	g.line(".global __fern_timer_fd")
	g.typeDirective("__fern_timer_fd")
	g.label("__fern_timer_fd")
	if g.darwin {
		g.emit("mov x0, #-1")
		g.emit("ret")
		g.sizeDirective("__fern_timer_fd")
		return
	}
	// Frame: fp/lr + x19 (ms) / x20 (fd) + a 32-byte itimerspec scratch.
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("mov x19, x0") // ms
	// fd = timerfd_create(CLOCK_MONOTONIC, 0)
	g.emit("mov x0, #%d", clockMonotonic)
	g.emit("mov x1, #0")
	g.emit("mov x8, #%d", sysTimerfdCreate)
	g.emit("svc #0")
	g.emit("mov x20, x0")
	g.emit("cmp x0, #0")
	g.emit("b.lt .Ltimerfd_ret")
	// itimerspec at [x29,#32]: it_interval{0,0} (+32,+40),
	// it_value{sec,nsec} (+48,+56).
	g.emit("str xzr, [x29, #32]")
	g.emit("str xzr, [x29, #40]")
	g.emit("mov x9, #1000")
	g.emit("udiv x10, x19, x9")      // sec
	g.emit("msub x11, x10, x9, x19") // rem ms
	g.emit("ldr x12, =1000000")
	g.emit("mul x11, x11, x12") // nsec
	g.emit("str x10, [x29, #48]")
	g.emit("str x11, [x29, #56]")
	// timerfd_settime(fd, 0, &its, NULL)
	g.emit("mov x0, x20")
	g.emit("mov x1, #0")
	g.emit("add x2, x29, #32") // &itimerspec (it_interval first)
	g.emit("mov x3, #0")
	g.emit("mov x8, #%d", sysTimerfdSettime)
	g.emit("svc #0")
	g.emit("mov x0, x20") // return fd
	g.label(".Ltimerfd_ret")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("__fern_timer_fd")
	g.line(".ltorg")
}

// emitRandomI32Runtime emits `__fern_random_i32()` — returns a
// single cryptographic-quality i32 by reading 4 CSPRNG bytes
// into a stack slot and reloading them as a (little-endian) i32.
// Mirrors the interp's `crypto/rand` 4-byte read and the wasm
// `random_get` path.
//
// Linux: getrandom(buf, 4, 0) (syscall 278). Darwin: getentropy(
// buf, 4) (syscall 500). No `bl` is made, so x30 needn't be
// saved — we only reserve 16 bytes of stack (kept 16-aligned)
// for the 4-byte landing buffer.
func (g *generator) emitRandomI32Runtime() {
	g.line("")
	g.line(".global __fern_random_i32")
	g.typeDirective("__fern_random_i32")
	g.label("__fern_random_i32")
	g.emit("sub sp, sp, #16")
	if g.darwin {
		// getentropy(buf=sp, len=4), syscall 500.
		g.emit("mov x0, sp")
		g.emit("mov x1, #4")
		g.emit("mov x16, #%d", darGetentropy)
		g.emit("svc #0x80")
	} else {
		// getrandom(buf=sp, len=4, flags=0), syscall 278.
		g.emit("mov x0, sp")
		g.emit("mov x1, #4")
		g.emit("mov x2, #0")
		g.syscall("getrandom")
	}
	g.emit("ldr w0, [sp]") // 4 random bytes → i32
	g.emit("add sp, sp, #16")
	g.emit("ret")
	g.sizeDirective("__fern_random_i32")
	g.line(".ltorg")
}

// emitStringAsBytesRuntime emits `__method_string_as_bytes(s)` —
// the non-copying `.as_bytes()` view: an 8-byte slice header
// `(data_ptr, len)` aliasing the receiver string's bytes.
//
// arm64 always runs the two-word string ABI (arm64.Emit forces
// `TwoWordOverride`), so the receiver already arrives as
// (x0=data, x1=len) — exactly __fern_slice_make's argument shape.
// The header aliases the source bytes (heap or .rodata for
// literals); no copy is needed, so we tail-call slice_make. This is
// genuinely zero-copy even for a .rodata literal in a PIE shared
// object, because __fern_slice_make now stores the full 8-byte data
// pointer (the earlier 32-bit slice field truncated high addresses;
// superseded by the 64-bit slice header).
func (g *generator) emitStringAsBytesRuntime() {
	g.line("")
	g.line(".global __method_string_as_bytes")
	g.typeDirective("__method_string_as_bytes")
	g.label("__method_string_as_bytes")
	if !ast.UseTwoWordStrings(8) {
		// Single-word (LSB-tagged SSO) strings would need inline-
		// promotion here before a slice can alias real memory.
		// arm64.Emit never selects that ABI; surface it loudly
		// rather than emit silently-wrong asm (mirrors syscall()).
		panic("arm64 __method_string_as_bytes: single-word string ABI unsupported")
	}
	// (x0=data, x1=len) → __fern_slice_make(data, len) → header.
	g.emit("b __fern_slice_make")
	g.sizeDirective("__method_string_as_bytes")
	g.line(".ltorg")
}

// emitIoErrorRuntime emits `__fern_io_error(errno, path) → ptr`
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
	g.line(".global __fern_io_error")
	g.typeDirective("__fern_io_error")
	g.label("__fern_io_error")
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
	g.emit("cmp w19, #2") // ENOENT
	g.emit("b.eq .Lioe_notfound")
	g.emit("cmp w19, #13") // EACCES
	g.emit("b.eq .Lioe_perm")
	g.emit("cmp w19, #17") // EEXIST
	g.emit("b.eq .Lioe_exists")
	g.emit("cmp w19, #4") // EINTR
	g.emit("b.eq .Lioe_intr")

	// Other(path, ""). The "" payload needs the SECOND string
	// payload at +16 (third 8-byte slot). Box is 24 bytes for
	// two payloads. The empty-string ptr comes from interning
	// "" at compile time — but we need a runtime constant.
	// Use the .LStr_empty label below.
	g.emit("mov x0, #24")
	g.emit("bl __fern_alloc_box")
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
	g.emit("bl __fern_alloc_box")
	g.emit("mov w1, #4")
	g.emit("str w1, [x0]")
	g.emit("b .Lioe_done")

	g.label(".Lioe_with_path")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("str w19, [x0]")     // tag
	g.emit("str x20, [x0, #8]") // path
	g.label(".Lioe_done")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__fern_io_error")

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
// Signature: `__fern_io_error(errno, path_data, path_len)` in
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
	g.emit("cmp w19, #2") // ENOENT
	g.emit("b.eq .Lioe2w_notfound")
	g.emit("cmp w19, #13") // EACCES
	g.emit("b.eq .Lioe2w_perm")
	g.emit("cmp w19, #17") // EEXIST
	g.emit("b.eq .Lioe2w_exists")
	g.emit("cmp w19, #4") // EINTR
	g.emit("b.eq .Lioe2w_intr")
	// Other(path, "") — 40-byte box, msg = empty inline pair.
	g.emit("mov x0, #40")
	g.emit("bl __fern_alloc_box")
	g.emit("mov w1, #6")
	g.emit("str w1, [x0]")
	g.emit("str x20, [x0, #8]")  // path_data
	g.emit("str x21, [x0, #16]") // path_len
	g.emit("str xzr, [x0, #24]") // msg_data = 0
	g.emit("movz x1, #0x8000, lsl #48")
	g.emit("str x1, [x0, #32]") // msg_len = inline-empty
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
	g.emit("bl __fern_alloc_box")
	g.emit("mov w1, #4")
	g.emit("str w1, [x0]")
	g.emit("b .Lioe2w_done")
	g.label(".Lioe2w_with_path")
	g.emit("mov x0, #24")
	g.emit("bl __fern_alloc_box")
	g.emit("str w19, [x0]")
	g.emit("str x20, [x0, #8]")  // path_data
	g.emit("str x21, [x0, #16]") // path_len
	g.label(".Lioe2w_done")
	g.emit("ldr x21, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__fern_io_error")
	g.line(".ltorg")
}

// emitReadFileRuntime emits `__fern_read_file(path) →
// Result[string, IoError]`. Pipeline: openat(AT_FDCWD, path,
// O_RDONLY) → fstat → alloc length-prefixed buffer → read-loop
// → close → wrap as Ok(string). Any syscall error short-circuits
// to Err(IoError) via __fern_io_error.
//
// Result box layout (matches IR): 16-byte heap obj
// `{tag:i32 @0, _:i32 @4, payload:ptr @8}` where:
//
//	tag=0 → Ok(string), payload = string data ptr
//	tag=1 → Err(IoError), payload = IoError box ptr
func (g *generator) emitReadFileRuntime() {
	g.line("")
	g.line(".global __fern_read_file")
	g.typeDirective("__fern_read_file")
	g.label("__fern_read_file")
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
	g.emit("mov x19, x0")              // x19 = original path string value (heap or inline)
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
	g.syscallFstat()
	g.emit("tbnz x0, #63, .Lrf_err_close")
	g.emit("ldr x22, [sp, #%d]", 64+g.statSizeOff()) // st_size

	// L2 rc-header layout — see __fern_strcat. Payload = size data only.
	g.emit("mov x0, x22")
	g.emit("bl __fern_alloc_rc1")
	g.emit("mov x21, x0") // x21 = data ptr (= base+8)
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
	g.emit("bl __fern_alloc_box")
	g.emit("str wzr, [x0]")     // tag=0 (Ok)
	g.emit("str x21, [x0, #8]") // payload @ +8 — x21 is already the string data ptr
	g.emit("b .Lrf_return")

	g.label(".Lrf_err_close")
	// errno = -x0, then close(fd), then build Err.
	g.emit("neg x21, x0") // x21 = errno (reuse slot)
	g.emit("mov x0, x20")
	g.syscall("close")
	g.emit("b .Lrf_err_dispatch")

	g.label(".Lrf_err_open")
	g.emit("neg x21, x0") // errno

	g.label(".Lrf_err_dispatch")
	// __fern_io_error(errno, path) → IoError box in x0.
	g.emit("mov x0, x21")
	g.emit("mov x1, x19")
	g.emit("bl __fern_io_error")
	// Stash the IoError box in x19 (callee-save; path no longer
	// needed). x1 would NOT survive the next __fern_alloc call.
	g.emit("mov x19, x0")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]")
	g.emit("str x19, [x0, #8]")

	g.label(".Lrf_return")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #256")
	g.emit("ret")
	g.sizeDirective("__fern_read_file")
	g.line(".ltorg")
}

// emitReadFileRuntime2W is the two-word-ABI variant of
// emitReadFileRuntime. Signature:
// `__fern_read_file(path_data, path_len)` in (x0, x1).
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
	// NUL-terminate for openat (see emitNulTermPath2W).
	g.emitNulTermPath2W("x25", "x25", "x20")
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
	g.syscallFstat()
	g.emit("tbnz x0, #63, .Lrf2w_err_close")
	g.emit("ldr x23, [x29, #%d]", 96+g.statSizeOff()) // st_size
	// Allocate exactly st_size bytes for the result string
	// data — no length prefix (two-word ABI).
	g.emit("mov x0, x23")
	// rc-headered alloc (rc=1 @data-8, size @data-4) so the owned Ok(string)
	// this returns is reclaimed correctly by __fern_str_dec; a plain
	// __fern_alloc buffer has no header and corrupts the heap (#2817 class).
	g.emit("bl __fern_alloc_rc1")
	g.emit("mov x22, x0") // x22 = data ptr (= base+8)
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
	g.emit("bl __fern_alloc_box")
	g.emit("str wzr, [x0]")      // tag = 0 (Ok)
	g.emit("str x22, [x0, #8]")  // payload data
	g.emit("str x23, [x0, #16]") // payload len
	g.emit("b .Lrf2w_return")
	g.label(".Lrf2w_err_close")
	g.emit("neg x22, x0") // x22 = errno
	g.emit("mov x0, x21")
	g.syscall("close")
	g.emit("b .Lrf2w_err_dispatch")
	g.label(".Lrf2w_err_open")
	g.emit("neg x22, x0")
	g.label(".Lrf2w_err_dispatch")
	// __fern_io_error(errno, path_data, path_len). Updated
	// to take a two-word string for the path.
	g.emit("mov x0, x22")
	g.emit("mov x1, x19")
	g.emit("mov x2, x20")
	g.emit("bl __fern_io_error")
	g.emit("mov x19, x0") // stash IoError box across alloc
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
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
	g.sizeDirective("__fern_read_file")
	g.line(".ltorg")
}

// emitWriteFileRuntime emits `__fern_write_file(path, content)
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
	g.line(".global __fern_write_file")
	g.typeDirective("__fern_write_file")
	g.label("__fern_write_file")
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
	g.emit("mov x19, x0")              // x19 = ORIGINAL path string value (for io_error)
	g.emitStrLen("w22", "x1")          // x22 = content_len (before content materialise)
	g.emitStrDataPtr("x20", "x1", 72)  // x20 = content byte ptr
	g.emitStrDataPtr("x24", "x19", 64) // x24 = path byte ptr (preserves x19 = original)

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
	g.emit("bl __fern_alloc_box")
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
	g.emit("bl __fern_io_error")
	// Stash IoError box in x19 (callee-save; path / content no
	// longer needed) — x1 would NOT survive the next alloc call.
	g.emit("mov x19, x0")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("str wzr, [x0]")
	g.emit("str x19, [x0, #8]")

	g.label(".Lwf_return")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #80")
	g.emit("ret")
	g.sizeDirective("__fern_write_file")
	g.line(".ltorg")
}

// emitWriteFileRuntime2W is the two-word-ABI variant.
// Signature: `__fern_write_file(path_data, path_len,
// content_data, content_len)` in (x0..x3). Returns
// `Option[IoError]` heap-box ptr in x0:
//
//	Some(IoError): 16-byte box {tag=0@0, payload=err@8}
//	None:           8-byte box {tag=1@0}
func (g *generator) emitWriteFileRuntime2W() {
	// Frame: 112 bytes. fp/lr (16) + 7 callee-saves (56 = x19..x25
	// + 8 pad) + 2× 16-byte inline-spill scratch for path + content
	// (32) + 8 align.
	g.emit("stp x29, x30, [sp, #-112]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("str x25, [sp, #64]")
	g.emit("mov x19, x0") // path_data
	g.emit("mov x20, x1") // path_len
	g.emit("mov x21, x2") // content_data
	g.emit("mov x22, x3") // content_len
	// content byte length + byte ptr.
	g.emitStrLen2W("w24", "x22")                // x24 = content byte length
	g.emitStrDataPtr2W("x23", "x21", "x22", 72) // x23 = content byte ptr; scratch at [x29+72]
	// path byte ptr (separate scratch).
	g.emitStrDataPtr2W("x25", "x19", "x20", 88) // x25 = path byte ptr; scratch at [x29+88]
	// NUL-terminate for openat (see emitNulTermPath2W).
	g.emitNulTermPath2W("x25", "x25", "x20")
	// openat(AT_FDCWD, path, O_WRONLY|O_CREAT|O_TRUNC=577, 0644)
	g.emit("mov x0, #-100")
	g.emit("mov x1, x25")
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
	g.emit("mov x0, x21")      // fd
	g.emit("add x1, x23, x22") // buf + offset
	g.emit("sub x2, x24, x22") // remaining
	g.syscall("write")
	g.emit("tbnz x0, #63, .Lwf2w_err_close")
	g.emit("add x22, x22, x0")
	g.emit("b .Lwf2w_loop")
	g.label(".Lwf2w_done")
	g.emit("mov x0, x21")
	g.syscall("close")
	// Return None: 8-byte box, tag=1.
	g.emit("mov x0, #8")
	g.emit("bl __fern_alloc_box")
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
	// __fern_io_error(errno, path_data, path_len).
	g.emit("mov x0, x22")
	g.emit("mov x1, x19")
	g.emit("mov x2, x20")
	g.emit("bl __fern_io_error")
	g.emit("mov x19, x0") // stash IoError box
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("str wzr, [x0]")     // tag = 0 (Some)
	g.emit("str x19, [x0, #8]") // payload
	g.label(".Lwf2w_return")
	g.emit("ldr x25, [sp, #64]")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #112")
	g.emit("ret")
	g.sizeDirective("__fern_write_file")
	g.line(".ltorg")
}

// atFdcwd materialises the platform AT_FDCWD constant (-100 on
// Linux, -2 on Darwin) into reg. Shared by the filesystem-op
// helpers below.
func (g *generator) atFdcwd(reg string) {
	if g.darwin {
		g.emit("mov %s, #-2", reg)
	} else {
		g.emit("mov %s, #-100", reg)
	}
}

// syscallFstatat emits fstatat(dirfd, path, statbuf, flags) —
// args already in x0..x3 — and normalises the error shape to
// Linux's -errno-in-x0. Branched inline (like syscallFstat)
// because Darwin and Linux disagree on the number (Linux
// newfstatat 79, Darwin fstatat64 470) and the struct layout
// (see statSizeOff / the st_mode reads in emitStatRuntime).
func (g *generator) syscallFstatat() {
	if g.darwin {
		g.emit("mov x16, #470")
		g.emit("svc #0x80")
		lbl := g.freshLabel("fstatat_ok")
		g.emit("b.cc %s", lbl)
		g.emit("neg x0, x0")
		g.label(lbl)
		return
	}
	g.emit("mov x8, #79")
	g.emit("svc #0")
}

// syscallGetdents emits getdents64(fd, buf, count) — args in
// x0..x2 — normalised to -errno-in-x0 on error. On Darwin the
// equivalent is getdirentries64(fd, buf, count, basep) (BSD 344)
// whose extra `basep` pointer must already be in x3; the dirent
// record layouts also differ (d_name at 21 vs Linux's 19 — the
// callers branch on that), while d_reclen sits at offset 16 on
// both.
func (g *generator) syscallGetdents() {
	if g.darwin {
		g.emit("mov x16, #344")
		g.emit("svc #0x80")
		lbl := g.freshLabel("getdents_ok")
		g.emit("b.cc %s", lbl)
		g.emit("neg x0, x0")
		g.label(lbl)
		return
	}
	g.emit("mov x8, #61")
	g.emit("svc #0")
}

// direntNameOff is the byte offset of d_name within a directory
// entry record: 19 in Linux's getdents64 layout, 21 in Darwin's
// 64-bit-inode getdirentries64 layout.
func (g *generator) direntNameOff() int {
	if g.darwin {
		return 21
	}
	return 19
}

// emitODirectory materialises the platform O_DIRECTORY flag into
// reg: 0o40000 = 16384 on arm64 Linux (an arch-specific override
// — x86-64's 65536 is O_DIRECT here), 0x100000 on Darwin.
func (g *generator) emitODirectory(reg string) {
	if g.darwin {
		g.emit("mov %s, #1", reg)
		g.emit("lsl %s, %s, #20", reg, reg)
	} else {
		g.emit("mov %s, #16384", reg)
	}
}

// emitRemoveFileRuntime emits `__fern_remove_file(path_data,
// path_len)` in (x0, x1) → Option[IoError] — unlinkat(AT_FDCWD,
// path, 0). None on success; Some(IoError) on failure (removing
// a missing file IS an error, matching the checker's contract —
// unlike remove_dir_all's silent-ENOENT). Box shapes match
// write_file: None = 8-byte box tag=1; Some = 16-byte box tag=0
// with the IoError box @+8.
func (g *generator) emitRemoveFileRuntime() {
	g.line("")
	g.line(".global __fern_remove_file")
	g.typeDirective("__fern_remove_file")
	g.label("__fern_remove_file")
	// Frame: fp/lr (16) + x19..x22 (32) + 16-byte inline-spill
	// scratch at [x29+48] = 64.
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("mov x19, x0") // path_data (original, for io_error)
	g.emit("mov x20, x1") // path_len (original, for io_error)
	g.emitStrDataPtr2W("x21", "x19", "x20", 48)
	g.emit("mov x22, x20")
	g.emitStrLen2W("w22", "x22") // w22 = byte length
	g.emitNulTermPath2W("x21", "x21", "x22")
	// unlinkat(AT_FDCWD, pathz, 0)
	g.atFdcwd("x0")
	g.emit("mov x1, x21")
	g.emit("mov x2, #0")
	g.syscall("unlinkat")
	g.emit("tbnz x0, #63, .Lrmf2w_some")
	g.emit("mov x0, #8")
	g.emit("bl __fern_alloc_box")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]") // Option.None
	g.emit("b .Lrmf2w_return")

	g.label(".Lrmf2w_some")
	g.emit("neg x22, x0") // errno
	g.emit("mov x0, x22")
	g.emit("mov x1, x19")
	g.emit("mov x2, x20")
	g.emit("bl __fern_io_error")
	g.emit("mov x19, x0") // stash IoError box across the alloc
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("str wzr, [x0]") // Option.Some
	g.emit("str x19, [x0, #8]")

	g.label(".Lrmf2w_return")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("__fern_remove_file")
	g.line(".ltorg")
}

// emitTempDirRuntime emits `__fern_temp_dir(prefix_data,
// prefix_len)` in (x0, x1) → Result[string, IoError] — creates
// "/tmp/<prefix>-<ns>" (ns = __fern_monotonic_ns, decimal
// digits) via mkdirat and returns Ok(path). The path is built in
// a plain scratch buffer, then copied into an exactly-sized rc=1
// string so the length prefix matches the allocation. Result
// box: Ok = 24-byte {tag=0 @0, data @8, len @16}; Err = 16-byte
// {tag=1 @0, IoError @8} (same shapes as read_file's 2W boxes).
func (g *generator) emitTempDirRuntime() {
	g.line("")
	g.line(".global __fern_temp_dir")
	g.typeDirective("__fern_temp_dir")
	g.label("__fern_temp_dir")
	// Frame: fp/lr (16) + x19..x25 (56 + 8 pad rounds into the
	// next slot) + 16-byte inline-spill scratch at [x29+72] = 96.
	g.emit("stp x29, x30, [sp, #-96]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("str x25, [sp, #64]")
	g.emit("mov x19, x0")                       // prefix_data (original, for io_error)
	g.emit("mov x20, x1")                       // prefix_len (original, for io_error)
	g.emitStrDataPtr2W("x25", "x19", "x20", 72) // x25 = prefix byte ptr
	g.emit("mov x24, x20")
	g.emitStrLen2W("w24", "x24") // w24 = prefix byte length
	g.emit("bl __fern_monotonic_ns")
	g.emit("mov x23, x0") // ns (unique suffix)
	// Scratch: 5 ("/tmp/") + plen + 1 ('-') + 20 (max digits) + 1 NUL.
	g.emit("add x0, x24, #27")
	g.emit("bl __fern_alloc")
	g.emit("mov x21, x0") // scratch path buffer
	g.emit("mov w9, #47") // '/'
	g.emit("strb w9, [x21]")
	g.emit("mov w9, #116") // 't'
	g.emit("strb w9, [x21, #1]")
	g.emit("mov w9, #109") // 'm'
	g.emit("strb w9, [x21, #2]")
	g.emit("mov w9, #112") // 'p'
	g.emit("strb w9, [x21, #3]")
	g.emit("mov w9, #47") // '/'
	g.emit("strb w9, [x21, #4]")
	g.emit("mov x22, #5") // cursor
	// Append the prefix bytes.
	g.emit("mov x9, #0")
	g.label(".Ltd2w_pcp")
	g.emit("cmp x9, x24")
	g.emit("b.ge .Ltd2w_pcpd")
	g.emit("ldrb w10, [x25, x9]")
	g.emit("strb w10, [x21, x22]")
	g.emit("add x22, x22, #1")
	g.emit("add x9, x9, #1")
	g.emit("b .Ltd2w_pcp")
	g.label(".Ltd2w_pcpd")
	g.emit("mov w9, #45") // '-'
	g.emit("strb w9, [x21, x22]")
	g.emit("add x22, x22, #1")
	// Count decimal digits of ns into x11 (do-while: ns=0 → 1).
	g.emit("mov x9, x23")
	g.emit("mov x11, #0")
	g.emit("mov x13, #10")
	g.label(".Ltd2w_cnt")
	g.emit("udiv x14, x9, x13")
	g.emit("add x11, x11, #1")
	g.emit("mov x9, x14")
	g.emit("cbnz x9, .Ltd2w_cnt")
	// Write the digits least-significant-first into
	// [x22 .. x22+x11-1], then advance the cursor.
	g.emit("add x15, x22, x11")
	g.emit("sub x15, x15, #1") // last digit position
	g.emit("mov x9, x23")
	g.label(".Ltd2w_wr")
	g.emit("udiv x14, x9, x13")
	g.emit("msub x16, x14, x13, x9") // rem = x9 - q*10
	g.emit("add x16, x16, #48")
	g.emit("strb w16, [x21, x15]")
	g.emit("sub x15, x15, #1")
	g.emit("mov x9, x14")
	g.emit("cbnz x9, .Ltd2w_wr")
	g.emit("add x22, x22, x11")    // total path length
	g.emit("strb wzr, [x21, x22]") // NUL
	// mkdirat(AT_FDCWD, pathz, 0700=448)
	g.atFdcwd("x0")
	g.emit("mov x1, x21")
	g.emit("mov x2, #448")
	g.syscall("mkdirat")
	g.emit("cbnz x0, .Ltd2w_err")
	// Ok: copy the path into an exactly-sized rc=1 string.
	g.emit("add x0, x22, #1")
	g.emit("bl __fern_alloc_rc1")
	g.emit("mov x24, x0") // final string data ptr (prefix len dead)
	g.emit("stur w22, [x24, #-4]")
	g.emit("mov x0, x24")
	g.emit("mov x1, x21")
	g.emit("mov x2, x22")
	g.emit("bl __fern_memcpy")
	g.emit("strb wzr, [x24, x22]")
	g.emit("mov x0, #24")
	g.emit("bl __fern_alloc_box")
	g.emit("str wzr, [x0]")      // tag = 0 (Ok)
	g.emit("str x24, [x0, #8]")  // payload data
	g.emit("str x22, [x0, #16]") // payload len (heap form)
	g.emit("b .Ltd2w_return")

	g.label(".Ltd2w_err")
	g.emit("neg x22, x0") // errno (cursor dead on this path)
	g.emit("mov x0, x22")
	g.emit("mov x1, x19")
	g.emit("mov x2, x20")
	g.emit("bl __fern_io_error")
	g.emit("mov x19, x0")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("mov w9, #1")
	g.emit("str w9, [x0]") // tag = 1 (Err)
	g.emit("str x19, [x0, #8]")

	g.label(".Ltd2w_return")
	g.emit("ldr x25, [sp, #64]")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #96")
	g.emit("ret")
	g.sizeDirective("__fern_temp_dir")
	g.line(".ltorg")
}

// emitReadDirRuntime emits `__fern_read_dir(path_data,
// path_len)` in (x0, x1) → Result[string[], IoError] — lists the
// non-recursive children of `path` as base names (unsorted).
// Pipeline: openat(O_RDONLY|O_DIRECTORY) → getdents64-drain into
// a 1 MiB heap buffer → close → pass 1 counts entries (skipping
// "." / "..") → array alloc (canonical two-word layout: 16-byte
// header, cap@data-12, rc=1@data-8, len@data-4, 16-byte
// (data, len) elements) → pass 2 fills with fresh rc=1 strings.
// openat failure → Err(IoError). Ok = 16-byte box {tag=0,
// array ptr @8}; Err = 16-byte box {tag=1, IoError @8}.
func (g *generator) emitReadDirRuntime() {
	g.line("")
	g.line(".global __fern_read_dir")
	g.typeDirective("__fern_read_dir")
	g.label("__fern_read_dir")
	// Frame: fp/lr (16) + x19..x26 (64) + 16-byte inline-spill
	// scratch at [x29+80] = 96.
	g.emit("stp x29, x30, [sp, #-96]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("stp x25, x26, [sp, #64]")
	g.emit("mov x19, x0") // path_data (original, for io_error)
	g.emit("mov x20, x1") // path_len (original, for io_error)
	g.emitStrDataPtr2W("x21", "x19", "x20", 80)
	g.emit("mov x22, x20")
	g.emitStrLen2W("w22", "x22") // w22 = path byte length
	g.emitNulTermPath2W("x21", "x21", "x22")
	// openat(AT_FDCWD, pathz, O_RDONLY|O_DIRECTORY, 0)
	g.atFdcwd("x0")
	g.emit("mov x1, x21")
	g.emitODirectory("x2")
	g.emit("mov x3, #0")
	g.syscall("openat")
	g.emit("tbnz x0, #63, .Lrdd2w_err")
	g.emit("mov x23, x0") // fd
	// 1 MiB dirent buffer (mirrors the self-host helper's cap).
	g.emit("mov x0, #1")
	g.emit("lsl x0, x0, #20")
	g.emit("bl __fern_alloc")
	g.emit("mov x21, x0") // buf base (pathz dead past openat)
	if g.darwin {
		// getdirentries64 needs a basep (off_t*) scratch cell.
		g.emit("mov x0, #8")
		g.emit("bl __fern_alloc")
		g.emit("mov x24, x0")
	}
	g.emit("mov x22, #0") // total (path byte len dead)
	g.label(".Lrdd2w_g")
	g.emit("mov x2, #1")
	g.emit("lsl x2, x2, #20")
	g.emit("sub x2, x2, x22")
	g.emit("cbz x2, .Lrdd2w_gd")
	g.emit("mov x0, x23")
	g.emit("add x1, x21, x22")
	if g.darwin {
		g.emit("mov x3, x24") // basep
	}
	g.syscallGetdents()
	g.emit("cmp x0, #0")
	g.emit("b.le .Lrdd2w_gd")
	g.emit("add x22, x22, x0")
	g.emit("b .Lrdd2w_g")
	g.label(".Lrdd2w_gd")
	g.emit("mov x0, x23")
	g.syscall("close")
	// Pass 1: count entries that aren't "." / "..".
	g.emit("mov x23, #0") // count (fd is closed)
	g.emit("mov x26, #0") // offset
	g.label(".Lrdd2w_c1")
	g.emit("cmp x26, x22")
	g.emit("b.ge .Lrdd2w_c1d")
	g.emit("add x10, x21, x26")
	g.emit("add x10, x10, #%d", g.direntNameOff()) // d_name ptr
	g.emit("ldrb w11, [x10]")
	g.emit("cmp w11, #46") // '.'
	g.emit("b.ne .Lrdd2w_c1n")
	g.emit("ldrb w11, [x10, #1]")
	g.emit("cbz w11, .Lrdd2w_c1s") // "."
	g.emit("cmp w11, #46")
	g.emit("b.ne .Lrdd2w_c1n")
	g.emit("ldrb w11, [x10, #2]")
	g.emit("cbz w11, .Lrdd2w_c1s") // ".."
	g.label(".Lrdd2w_c1n")
	g.emit("add x23, x23, #1")
	g.label(".Lrdd2w_c1s")
	g.emit("add x12, x21, x26")
	g.emit("ldrh w11, [x12, #16]") // d_reclen
	g.emit("add x26, x26, x11")
	g.emit("b .Lrdd2w_c1")
	g.label(".Lrdd2w_c1d")
	// Array alloc: 16-byte header + count * 16 (two-word string
	// elements).
	g.emit("lsl x0, x23, #4")
	g.emit("add x0, x0, #16")
	g.emit("bl __fern_alloc")
	g.emit("add x25, x0, #16")      // array data ptr
	g.emit("stur w23, [x25, #-12]") // cap = count
	g.emit("mov w9, #1")
	g.emit("stur w9, [x25, #-8]") // rc = 1
	g.emitArrayLenStore("w23", "x25")
	// Pass 2: fill with fresh rc=1 strings.
	g.emit("mov x23, #0") // element index
	g.emit("mov x26, #0") // offset
	g.label(".Lrdd2w_p2")
	g.emit("cmp x26, x22")
	g.emit("b.ge .Lrdd2w_p2d")
	g.emit("add x10, x21, x26")
	g.emit("add x10, x10, #%d", g.direntNameOff())
	g.emit("ldrb w11, [x10]")
	g.emit("cmp w11, #46")
	g.emit("b.ne .Lrdd2w_p2t")
	g.emit("ldrb w11, [x10, #1]")
	g.emit("cbz w11, .Lrdd2w_p2a")
	g.emit("cmp w11, #46")
	g.emit("b.ne .Lrdd2w_p2t")
	g.emit("ldrb w11, [x10, #2]")
	g.emit("cbz w11, .Lrdd2w_p2a")
	g.label(".Lrdd2w_p2t")
	// nlen = strlen(name).
	g.emit("mov x11, #0")
	g.label(".Lrdd2w_sl")
	g.emit("ldrb w12, [x10, x11]")
	g.emit("cbz w12, .Lrdd2w_sld")
	g.emit("add x11, x11, #1")
	g.emit("b .Lrdd2w_sl")
	g.label(".Lrdd2w_sld")
	g.emit("mov x24, x10")         // name ptr (survives the alloc)
	g.emit("str x11, [sp, #-16]!") // nlen across the alloc
	g.emit("add x0, x11, #1")
	g.emit("bl __fern_alloc_rc1")
	g.emit("ldr x11, [sp], #16")
	g.emit("stur w11, [x0, #-4]") // length prefix (block sizing)
	g.emit("mov x9, #0")
	g.label(".Lrdd2w_nc")
	g.emit("cmp x9, x11")
	g.emit("b.ge .Lrdd2w_ncd")
	g.emit("ldrb w12, [x24, x9]")
	g.emit("strb w12, [x0, x9]")
	g.emit("add x9, x9, #1")
	g.emit("b .Lrdd2w_nc")
	g.label(".Lrdd2w_ncd")
	g.emit("strb wzr, [x0, x11]") // NUL (libc-shaped consumers)
	// Write entry: data at [x25 + i*16], len at [x25 + i*16 + 8].
	g.emit("lsl x12, x23, #4")
	g.emit("str x0, [x25, x12]")
	g.emit("add x12, x12, #8")
	g.emit("str x11, [x25, x12]") // len (heap form, top bit clear)
	g.emit("add x23, x23, #1")
	g.label(".Lrdd2w_p2a")
	g.emit("add x12, x21, x26")
	g.emit("ldrh w11, [x12, #16]") // d_reclen
	g.emit("add x26, x26, x11")
	g.emit("b .Lrdd2w_p2")
	g.label(".Lrdd2w_p2d")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("str wzr, [x0]") // tag = 0 (Ok)
	g.emit("str x25, [x0, #8]")
	g.emit("b .Lrdd2w_return")

	g.label(".Lrdd2w_err")
	g.emit("neg x22, x0") // errno (path byte len dead)
	g.emit("mov x0, x22")
	g.emit("mov x1, x19")
	g.emit("mov x2, x20")
	g.emit("bl __fern_io_error")
	g.emit("mov x19, x0")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("mov w9, #1")
	g.emit("str w9, [x0]") // tag = 1 (Err)
	g.emit("str x19, [x0, #8]")

	g.label(".Lrdd2w_return")
	g.emit("ldp x25, x26, [sp, #64]")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #96")
	g.emit("ret")
	g.sizeDirective("__fern_read_dir")
	g.line(".ltorg")
}

// emitStatRuntime emits `__fern_stat(path_data, path_len)` in
// (x0, x1) → Result[FileStat, IoError] — fstatat(AT_FDCWD, path,
// buf, 0) into a 192-byte stack buffer. Linux arm64 struct stat:
// st_mode u32 @16, st_size i64 @48; Darwin 64-bit-inode stat:
// st_mode u16 @4, st_size i64 @96 (see statSizeOff). The
// FileStat box uses the native structFieldLayout offsets —
// is_file (i32) @0, is_dir (i32) @4, size (i64) @8 — 16 bytes
// via __fern_alloc_box (immortal, same class as the Result
// boxes). Ok = 16-byte box {tag=0, FileStat ptr @8}; Err =
// 16-byte box {tag=1, IoError @8}.
func (g *generator) emitStatRuntime() {
	g.line("")
	g.line(".global __fern_stat")
	g.typeDirective("__fern_stat")
	g.label("__fern_stat")
	// Frame: 96-byte base (fp/lr + x19..x25 + 16-byte inline-
	// spill scratch at [x29+72]) + 192-byte statbuf at [x29+96]
	// = 288.
	g.emit("stp x29, x30, [sp, #-288]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("str x25, [sp, #64]")
	g.emit("mov x19, x0") // path_data (original, for io_error)
	g.emit("mov x20, x1") // path_len (original, for io_error)
	g.emitStrDataPtr2W("x21", "x19", "x20", 72)
	g.emit("mov x22, x20")
	g.emitStrLen2W("w22", "x22")
	g.emitNulTermPath2W("x21", "x21", "x22")
	// fstatat(AT_FDCWD, pathz, statbuf, 0)
	g.atFdcwd("x0")
	g.emit("mov x1, x21")
	g.emit("add x2, x29, #96")
	g.emit("mov x3, #0")
	g.syscallFstatat()
	g.emit("tbnz x0, #63, .Lst2w_err")
	if g.darwin {
		g.emit("ldrh w9, [x29, #100]") // st_mode (u16 @ +4)
	} else {
		g.emit("ldr w9, [x29, #112]") // st_mode (u32 @ +16)
	}
	g.emit("mov w11, #61440") // S_IFMT (0xF000)
	g.emit("and w9, w9, w11")
	g.emit("mov x23, #0")     // is_file
	g.emit("mov w10, #32768") // S_IFREG
	g.emit("cmp w9, w10")
	g.emit("b.ne .Lst2w_nf")
	g.emit("mov x23, #1")
	g.label(".Lst2w_nf")
	g.emit("mov x24, #0")     // is_dir
	g.emit("mov w10, #16384") // S_IFDIR
	g.emit("cmp w9, w10")
	g.emit("b.ne .Lst2w_nd")
	g.emit("mov x24, #1")
	g.label(".Lst2w_nd")
	g.emit("ldr x25, [x29, #%d]", 96+g.statSizeOff()) // st_size
	// FileStat box: is_file @0, is_dir @4, size @8.
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("str w23, [x0]")
	g.emit("str w24, [x0, #4]")
	g.emit("str x25, [x0, #8]")
	g.emit("mov x21, x0")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("str wzr, [x0]") // tag = 0 (Ok)
	g.emit("str x21, [x0, #8]")
	g.emit("b .Lst2w_return")

	g.label(".Lst2w_err")
	g.emit("neg x22, x0")
	g.emit("mov x0, x22")
	g.emit("mov x1, x19")
	g.emit("mov x2, x20")
	g.emit("bl __fern_io_error")
	g.emit("mov x19, x0")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("mov w9, #1")
	g.emit("str w9, [x0]") // tag = 1 (Err)
	g.emit("str x19, [x0, #8]")

	g.label(".Lst2w_return")
	g.emit("ldr x25, [sp, #64]")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #288")
	g.emit("ret")
	g.sizeDirective("__fern_stat")
	g.line(".ltorg")
}

// emitRemoveDirAllRuntime emits `__fern_remove_dir_all(
// path_data, path_len)` in (x0, x1) → Option[IoError] — a
// recursive `rm -rf`, the arm64 sibling of the x86-64 helper of
// the same name. Syscalls are inlined and the helper
// self-recurses per directory entry:
//
//	openat(AT_FDCWD, pathz, O_RDONLY|O_DIRECTORY, 0)
//	  fd >= 0        → it's a directory: drain entries, recurse
//	                   on each non-dot child, close, rmdir → None
//	  -ENOENT (-2)   → already gone → None
//	  -ENOTDIR (-20) → it's a file: unlinkat(file) → None
//	  else           → Some(IoError) via __fern_io_error
//
// Child paths "pathz/name" are freshly-allocated rc=1 strings
// passed to the recursion as (data, len) pairs (leaked
// one-level, same as the x86-64 helper). The dirent buffer is
// 1 KiB per level, matching x86-64's small-tree cap.
func (g *generator) emitRemoveDirAllRuntime() {
	g.line("")
	g.line(".global __fern_remove_dir_all")
	g.typeDirective("__fern_remove_dir_all")
	g.label("__fern_remove_dir_all")
	// Frame: fp/lr (16) + x19..x26 (64) + 16-byte inline-spill
	// scratch at [x29+80] = 96.
	g.emit("stp x29, x30, [sp, #-96]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("stp x23, x24, [sp, #48]")
	g.emit("stp x25, x26, [sp, #64]")
	g.emit("mov x20, x0") // path_data (original, for io_error)
	g.emit("mov x21, x1") // path_len (original, for io_error)
	g.emitStrDataPtr2W("x19", "x20", "x21", 80)
	g.emit("mov x22, x21")
	g.emitStrLen2W("w22", "x22")
	g.emitNulTermPath2W("x19", "x19", "x22") // x19 = pathz
	// openat(AT_FDCWD, pathz, O_RDONLY|O_DIRECTORY, 0)
	g.atFdcwd("x0")
	g.emit("mov x1, x19")
	g.emitODirectory("x2")
	g.emit("mov x3, #0")
	g.syscall("openat")
	g.emit("tbz x0, #63, .Lrda2w_dir") // fd >= 0 → directory
	g.emit("cmn x0, #2")               // -ENOENT → already gone
	g.emit("b.eq .Lrda2w_none")
	g.emit("cmn x0, #20") // -ENOTDIR → it's a file
	g.emit("b.ne .Lrda2w_some")
	// unlinkat(AT_FDCWD, pathz, 0) — remove the file.
	g.atFdcwd("x0")
	g.emit("mov x1, x19")
	g.emit("mov x2, #0")
	g.syscall("unlinkat")
	g.emit("b .Lrda2w_none")

	g.label(".Lrda2w_dir")
	g.emit("mov x22, x0") // dir fd (path byte len dead)
	// 1 KiB dirent buffer, drained until full/end (small-tree cap,
	// mirrors x86-64).
	g.emit("mov x0, #1024")
	g.emit("bl __fern_alloc")
	g.emit("mov x23, x0")
	if g.darwin {
		// getdirentries64 basep scratch (x26 is free until the
		// iteration below).
		g.emit("mov x0, #8")
		g.emit("bl __fern_alloc")
		g.emit("mov x26, x0")
	}
	g.emit("mov x24, #0") // total
	g.label(".Lrda2w_g")
	g.emit("mov x2, #1024")
	g.emit("sub x2, x2, x24")
	g.emit("cbz x2, .Lrda2w_gd") // buffer full → stop
	g.emit("mov x0, x22")
	g.emit("add x1, x23, x24")
	if g.darwin {
		g.emit("mov x3, x26")
	}
	g.syscallGetdents()
	g.emit("cmp x0, #0")
	g.emit("b.le .Lrda2w_gd") // 0 (end) or <0 (error) → stop
	g.emit("add x24, x24, x0")
	g.emit("b .Lrda2w_g")

	g.label(".Lrda2w_gd")
	g.emit("mov x25, #0") // iteration offset
	g.label(".Lrda2w_it")
	g.emit("cmp x25, x24")
	g.emit("b.ge .Lrda2w_itd")
	g.emit("add x9, x23, x25")
	g.emit("add x9, x9, #%d", g.direntNameOff()) // d_name ptr
	g.emit("ldrb w11, [x9]")
	g.emit("cmp w11, #46") // '.'
	g.emit("b.ne .Lrda2w_ch")
	g.emit("ldrb w11, [x9, #1]")
	g.emit("cbz w11, .Lrda2w_adv") // "."
	g.emit("cmp w11, #46")
	g.emit("b.ne .Lrda2w_ch")
	g.emit("ldrb w11, [x9, #2]")
	g.emit("cbz w11, .Lrda2w_adv") // ".."
	g.label(".Lrda2w_ch")
	// plen = strlen(pathz)
	g.emit("mov x11, #0")
	g.label(".Lrda2w_pl")
	g.emit("ldrb w12, [x19, x11]")
	g.emit("cbz w12, .Lrda2w_pld")
	g.emit("add x11, x11, #1")
	g.emit("b .Lrda2w_pl")
	g.label(".Lrda2w_pld")
	// nlen = strlen(name)
	g.emit("mov x12, #0")
	g.label(".Lrda2w_nl")
	g.emit("ldrb w13, [x9, x12]")
	g.emit("cbz w13, .Lrda2w_nld")
	g.emit("add x12, x12, #1")
	g.emit("b .Lrda2w_nl")
	g.label(".Lrda2w_nld")
	// childlen = plen + 1 + nlen; alloc an rc=1 child string
	// (childlen + NUL). Stash name ptr / plen / nlen across the
	// call.
	g.emit("stp x9, x11, [sp, #-32]!")
	g.emit("str x12, [sp, #16]")
	g.emit("add x0, x11, x12")
	g.emit("add x0, x0, #2")
	g.emit("bl __fern_alloc_rc1")
	g.emit("ldr x12, [sp, #16]")
	g.emit("ldp x9, x11, [sp], #32")
	g.emit("mov x10, x0") // child data ptr
	g.emit("add x13, x11, x12")
	g.emit("add x13, x13, #1")     // childlen
	g.emit("stur w13, [x10, #-4]") // length prefix
	// copy pathz[0..plen]
	g.emit("mov x14, #0")
	g.label(".Lrda2w_c1")
	g.emit("cmp x14, x11")
	g.emit("b.ge .Lrda2w_c1d")
	g.emit("ldrb w15, [x19, x14]")
	g.emit("strb w15, [x10, x14]")
	g.emit("add x14, x14, #1")
	g.emit("b .Lrda2w_c1")
	g.label(".Lrda2w_c1d")
	g.emit("mov w15, #47") // '/'
	g.emit("strb w15, [x10, x11]")
	// copy name at plen+1
	g.emit("mov x14, #0")
	g.label(".Lrda2w_c2")
	g.emit("cmp x14, x12")
	g.emit("b.ge .Lrda2w_c2d")
	g.emit("ldrb w15, [x9, x14]")
	g.emit("add x16, x11, x14")
	g.emit("add x16, x16, #1")
	g.emit("strb w15, [x10, x16]")
	g.emit("add x14, x14, #1")
	g.emit("b .Lrda2w_c2")
	g.label(".Lrda2w_c2d")
	g.emit("strb wzr, [x10, x13]") // NUL
	// recurse: remove_dir_all(child_data, child_len).
	g.emit("mov x0, x10")
	g.emit("mov x1, x13")
	g.emit("bl __fern_remove_dir_all")
	g.label(".Lrda2w_adv")
	g.emit("add x12, x23, x25")
	g.emit("ldrh w11, [x12, #16]") // d_reclen
	g.emit("add x25, x25, x11")
	g.emit("b .Lrda2w_it")

	g.label(".Lrda2w_itd")
	// close(fd), then rmdir the now-empty directory.
	g.emit("mov x0, x22")
	g.syscall("close")
	g.atFdcwd("x0")
	g.emit("mov x1, x19")
	if g.darwin {
		g.emit("mov x2, #128") // AT_REMOVEDIR (Darwin 0x80)
	} else {
		g.emit("mov x2, #512") // AT_REMOVEDIR (Linux 0x200)
	}
	g.syscall("unlinkat")

	g.label(".Lrda2w_none")
	g.emit("mov x0, #8")
	g.emit("bl __fern_alloc_box")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]") // Option.None
	g.emit("b .Lrda2w_return")

	g.label(".Lrda2w_some")
	g.emit("neg x22, x0") // errno (never opened a fd on this path)
	g.emit("mov x0, x22")
	g.emit("mov x1, x20")
	g.emit("mov x2, x21")
	g.emit("bl __fern_io_error")
	g.emit("mov x22, x0")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("str wzr, [x0]") // Option.Some
	g.emit("str x22, [x0, #8]")

	g.label(".Lrda2w_return")
	g.emit("ldp x25, x26, [sp, #64]")
	g.emit("ldp x23, x24, [sp, #48]")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #96")
	g.emit("ret")
	g.sizeDirective("__fern_remove_dir_all")
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
// or `Result[T, IoError]` via the shared `__fern_io_error`
// helper. Reader.read_line / Reader.read_chunk follow the
// wasm contract and return `Option[string]` (None on EOF or
// error; no IoError surfacing).
func (g *generator) emitReaderWriterRuntime() {
	// __fern_make_handle(fd_in_w0) → ptr to 4-byte struct
	// {fd:i32 @0}. Used by stdin/stdout/stderr + open_*.
	//
	// Phase 1e-runtime: the struct carries an 8-byte rc
	// header at `[ptr - 8]` (the static-sentinel 0x80000000
	// pattern, matching the empty-array head). Phase 1e's
	// predicate widening will inc/dec any Reader/Writer
	// alias the user creates; the sentinel short-circuits
	// both helpers so the runtime-owned handle behaves like
	// a never-counted value. Alloc bumps by 8; the data
	// pointer the caller sees is `base + 8`.
	g.line("")
	g.line(".global __fern_make_handle")
	g.typeDirective("__fern_make_handle")
	g.label("__fern_make_handle")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("mov w19, w0") // stash fd
	g.emit("mov w0, #12")
	g.emit("bl __fern_alloc")
	g.emit("mov w1, #1")
	g.emit("lsl w1, w1, #31")   // w1 = 0x80000000 (static sentinel)
	g.emit("str w1, [x0]")      // sentinel at base + 0
	g.emit("str w19, [x0, #8]") // fd at base + 8 (= data + 0)
	g.emit("add x0, x0, #8")    // return base + 8
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__fern_make_handle")

	// __fern_stdin / __fern_stdout / __fern_stderr — fixed-fd
	// handle constructors. Each wraps __fern_make_handle.
	for _, e := range []struct {
		sym string
		fd  int
	}{
		{"__fern_stdin", 0},
		{"__fern_stdout", 1},
		{"__fern_stderr", 2},
	} {
		g.line("")
		g.line(".global " + e.sym)
		g.typeDirective(e.sym)
		g.label(e.sym)
		g.emit("mov w0, #%d", e.fd)
		g.emit("b __fern_make_handle") // tail-call
		g.sizeDirective(e.sym)
	}

	// __fern_open_reader(path) / __fern_open_writer(path) /
	// __fern_open_appender(path) → Result[Reader|Writer, IoError].
	// Each is a thin wrapper around `openat` + handle alloc + the
	// Result-box build. Flags + mode differ per kind.
	twoWord := ast.UseTwoWordStrings(8)
	for _, e := range []struct {
		sym, name string
		flags     int
		mode      int
	}{
		{"__fern_open_reader", "open_reader", 0, 0},
		{"__fern_open_writer", "open_writer", 577, 0644},
		{"__fern_open_appender", "open_appender", 1089, 0644},
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
			g.emit("mov x19, x0")                       // path_data
			g.emit("mov x20, x1")                       // path_len
			g.emitStrDataPtr2W("x21", "x19", "x20", 48) // x21 = byte ptr; scratch [x29+48]
			// NUL-terminate for openat (see emitNulTermPath2W).
			g.emitNulTermPath2W("x21", "x21", "x20")
			g.emit("mov x0, #-100")
			g.emit("mov x1, x21")
			g.emit("mov w2, #%d", e.flags)
			g.emit("mov w3, #%d", e.mode)
			g.syscall("openat")
			g.emit("tbnz x0, #63, %s", ".Lorw2w_err_"+e.sym)
			g.emit("mov w0, w0")
			g.emit("bl __fern_make_handle")
			g.emit("mov x21, x0") // handle ptr
			g.emit("mov x0, #16")
			g.emit("bl __fern_alloc_box")
			g.emit("str wzr, [x0]")
			g.emit("str x21, [x0, #8]")
			g.emit("b %s", ".Lorw2w_ret_"+e.sym)
			g.label(".Lorw2w_err_" + e.sym)
			g.emit("neg x21, x0") // x21 = errno
			g.emit("mov x0, x21")
			g.emit("mov x1, x19")
			g.emit("mov x2, x20")
			g.emit("bl __fern_io_error")
			g.emit("mov x21, x0")
			g.emit("mov x0, #16")
			g.emit("bl __fern_alloc_box")
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
		g.emit("mov x19, x0")   // stash path
		g.emit("mov x0, #-100") // AT_FDCWD
		g.emit("mov x1, x19")
		g.emit("mov w2, #%d", e.flags)
		g.emit("mov w3, #%d", e.mode)
		g.syscall("openat")
		g.emit("tbnz x0, #63, %s", ".Lorw_err_"+e.sym)
		// Success: alloc handle struct, store fd, wrap in Ok.
		g.emit("mov w20, w0") // fd
		g.emit("mov w0, w20")
		g.emit("bl __fern_make_handle")
		g.emit("mov x19, x0") // handle ptr (in callee-save)
		g.emit("mov x0, #16")
		g.emit("bl __fern_alloc_box")
		g.emit("str wzr, [x0]")     // tag=0 (Ok)
		g.emit("str x19, [x0, #8]") // handle ptr
		g.emit("b %s", ".Lorw_ret_"+e.sym)
		g.label(".Lorw_err_" + e.sym)
		g.emit("neg x20, x0") // x20 = errno
		g.emit("mov x0, x20")
		g.emit("mov x1, x19") // path
		g.emit("bl __fern_io_error")
		g.emit("mov x19, x0") // stash IoError ptr (callee-save)
		g.emit("mov x0, #16")
		g.emit("bl __fern_alloc_box")
		g.emit("mov w1, #1")
		g.emit("str w1, [x0]")
		g.emit("str x19, [x0, #8]")
		g.label(".Lorw_ret_" + e.sym)
		g.emit("ldp x19, x20, [sp, #16]")
		g.emit("ldp x29, x30, [sp], #32")
		g.emit("ret")
		g.sizeDirective(e.sym)
	}

	// __fern_reader_read_line(reader_ptr) → Option[string].
	// Loads fd from [reader_ptr+0], reads byte-by-byte into
	// the shared `__fern_read_line_buf` until '\n' / 4 KiB /
	// EOF / error. Returns None on first-byte EOF, Some(line)
	// otherwise (line includes the trailing '\n' if seen).
	g.line("")
	g.line(".global __fern_reader_read_line")
	g.typeDirective("__fern_reader_read_line")
	g.label("__fern_reader_read_line")
	g.emit("stp x29, x30, [sp, #-64]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("stp x21, x22, [sp, #32]")
	g.emit("ldr w22, [x0]") // fd
	g.adrpAdd("x19", "__fern_read_line_buf")
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
	g.emit("bl __fern_alloc_box")
	g.emit("mov w1, #1")
	g.emit("str w1, [x0]")
	g.emit("b .Lrrl_ret")
	g.label(".Lrrl_some")
	if twoWord {
		// Heap-form alloc via the rc-headered allocator (rc=1 @data-8, size
		// @data-4) so the owned Some(string) is reclaimed correctly by
		// __fern_str_dec; plain __fern_alloc corrupts the heap (#2817 class).
		g.emit("mov x0, x20")
		g.emit("bl __fern_alloc_rc1")
		g.emit("mov x21, x0") // = base+8
		g.emit("mov x0, x21")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __fern_memcpy")
		// Some(string) box: 24 bytes — {tag@0, data@8, len@16}.
		g.emit("mov x19, x21")
		g.emit("mov x0, #24")
		g.emit("bl __fern_alloc_box")
		g.emit("str wzr, [x0]")
		g.emit("str x19, [x0, #8]")
		g.emit("str x20, [x0, #16]")
		g.emit("b .Lrrl_ret")
	} else {
		// L2 rc-header layout — see __fern_strcat. Payload = N data + 1 NUL.
		g.emit("add x0, x20, #1")
		g.emit("bl __fern_alloc_rc1")
		g.emit("mov x21, x0") // x21 = data ptr (= base+8)
		g.emit("stur w20, [x21, #-4]")
		g.emit("mov x0, x21")
		g.emit("mov x1, x19")
		g.emit("mov x2, x20")
		g.emit("bl __fern_memcpy")
		g.emit("add x0, x21, x20")
		g.emit("strb wzr, [x0]")
		g.emit("mov x19, x21")
		g.emit("mov x0, #16")
		g.emit("bl __fern_alloc_box")
		g.emit("str wzr, [x0]")
		g.emit("str x19, [x0, #8]")
	}
	g.label(".Lrrl_ret")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("__fern_reader_read_line")
	g.line(".ltorg")

	// __fern_reader_read_chunk(reader_ptr, n) →
	// Option[string]. Single read of up to n bytes; None if
	// the read returns 0 (EOF). Allocates the n-byte string
	// buffer first; if the read is short, the length prefix
	// records the actual byte count.
	g.line("")
	g.line(".global __fern_reader_read_chunk")
	g.typeDirective("__fern_reader_read_chunk")
	g.label("__fern_reader_read_chunk")
	g.emit("stp x29, x30, [sp, #-48]!")
	g.emit("mov x29, sp")
	g.emit("stp x19, x20, [sp, #16]")
	g.emit("str x21, [sp, #32]")
	g.emit("ldr w19, [x0]") // fd
	g.emit("mov x20, x1")   // n
	if twoWord {
		// Two-word heap form: alloc exactly n bytes (no
		// prefix). Actual bytes read tracked in the Some
		// box's len field. rc-headered alloc (rc=1 @data-8, size @data-4) so
		// the owned Some(string) is reclaimed correctly by __fern_str_dec;
		// plain __fern_alloc corrupts the heap (#2817 class).
		g.emit("mov x0, x20")
		g.emit("bl __fern_alloc_rc1")
		g.emit("mov x21, x0") // = base+8
		g.emit("mov w0, w19")
		g.emit("mov x1, x21")
		g.emit("mov x2, x20")
		g.syscall("read")
		g.emit("cmp x0, #0")
		g.emit("ble .Lrrc2w_none")
		g.emit("mov x20, x0") // x20 = bytes read
		// Some(string) 24-byte box.
		g.emit("mov x0, #24")
		g.emit("bl __fern_alloc_box")
		g.emit("str wzr, [x0]")
		g.emit("str x21, [x0, #8]")
		g.emit("str x20, [x0, #16]")
		g.emit("b .Lrrc2w_ret")
		g.label(".Lrrc2w_none")
		g.emit("mov x0, #4")
		g.emit("bl __fern_alloc_box")
		g.emit("mov w1, #1")
		g.emit("str w1, [x0]")
		g.label(".Lrrc2w_ret")
	} else {
		// L2 rc-header layout — see __fern_strcat. Payload = n data only.
		g.emit("mov x0, x20")
		g.emit("bl __fern_alloc_rc1")
		g.emit("mov x21, x0") // x21 = data ptr (= base+8)
		g.emit("mov w0, w19")
		g.emit("mov x1, x21")
		g.emit("mov x2, x20")
		g.syscall("read")
		g.emit("cmp x0, #0")
		g.emit("ble .Lrrc_none")
		g.emit("stur w0, [x21, #-4]") // length at data-4
		g.emit("mov x20, x0")
		g.emit("mov x19, x21") // x19 = data ptr
		g.emit("add x0, x19, x20")
		g.emit("strb wzr, [x0]")
		g.emit("mov x0, #16")
		g.emit("bl __fern_alloc_box")
		g.emit("str wzr, [x0]")
		g.emit("str x19, [x0, #8]")
		g.emit("b .Lrrc_ret")
		g.label(".Lrrc_none")
		g.emit("mov x0, #4")
		g.emit("bl __fern_alloc_box")
		g.emit("mov w1, #1")
		g.emit("str w1, [x0]")
		g.label(".Lrrc_ret")
	}
	g.emit("ldr x21, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #48")
	g.emit("ret")
	g.sizeDirective("__fern_reader_read_chunk")

	// __fern_writer_write(writer_ptr, s_data_ptr) →
	// Option[IoError]. Writes the full string in a loop;
	// returns None on success or Some(IoError) if any write
	// errored.
	g.line("")
	g.line(".global __fern_writer_write")
	g.typeDirective("__fern_writer_write")
	g.label("__fern_writer_write")
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
		g.emit("mov x20, x1")      // s data ptr
		g.emitStrLen("w22", "x20") // len
	}
	g.emit("mov x21, #0") // bytes_written
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
	g.emit("bl __fern_alloc_box")
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
	g.emit("bl __fern_io_error")
	g.emit("mov x19, x0")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("str wzr, [x0]")
	g.emit("str x19, [x0, #8]")
	g.label(".Lww_ret")
	g.emit("ldp x21, x22, [sp, #32]")
	g.emit("ldp x19, x20, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #64")
	g.emit("ret")
	g.sizeDirective("__fern_writer_write")

	// __fern_close_fd_box(handle_ptr) → Option[IoError].
	// Shared by Reader.close + Writer.close.
	g.line("")
	g.line(".global __fern_close_fd_box")
	g.typeDirective("__fern_close_fd_box")
	g.label("__fern_close_fd_box")
	g.emit("stp x29, x30, [sp, #-32]!")
	g.emit("mov x29, sp")
	g.emit("str x19, [sp, #16]")
	g.emit("ldr w0, [x0]") // fd
	g.syscall("close")
	g.emit("tbnz x0, #63, .Lcfb_err")
	// None.
	g.emit("mov x0, #4")
	g.emit("bl __fern_alloc_box")
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
	g.emit("bl __fern_io_error")
	g.emit("mov x19, x0")
	g.emit("mov x0, #16")
	g.emit("bl __fern_alloc_box")
	g.emit("str wzr, [x0]")
	g.emit("str x19, [x0, #8]")
	g.label(".Lcfb_ret")
	g.emit("ldr x19, [sp, #16]")
	g.emit("ldp x29, x30, [sp], #32")
	g.emit("ret")
	g.sizeDirective("__fern_close_fd_box")
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
		// MakeClosure pair {fn_ptr, env=0, drop_fn=0, env=0}. The
		// 32-byte 4-slot shape matches the captured case so a generic
		// holder (__drop_arr_closure) can read the drop-fn slot
		// uniformly; a zero-capture closure has no env to free, so
		// drop_fn is 0 (the generic drop guards drop_fn!=0).
		g.emit("mov w0, #32")
		g.emit("bl __fern_alloc_rc1")
		g.adrpAdd("x1", op.Str)
		g.emit("str x1, [x0]")
		g.emit("str xzr, [x0, #8]")
		g.emit("str xzr, [x0, #16]")
		g.emit("str xzr, [x0, #24]")
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
		envSize = ast.CaptureAlign(envSize, cap.Type, 8)
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
	g.emit("bl __fern_alloc_rc1")
	g.emit("mov x19, x0") // x19 = env_ptr (= base + 8 header)
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
				g.emit("ldr x1, [sp, #%d]", topOff[i])                  // len (top)
				g.emit("ldr x2, [sp, #%d]", topOff[i]+int32(slotBytes)) // data (below)
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
	// OpMakeClosure: also allocate the 32-byte closure pair
	// {fn_ptr, env_ptr, drop_fn, env_ptr}. env_ptr is in x0
	// (and x19); we need to keep it alive across the second
	// alloc. x19 already preserved (callee-save in the called
	// function); x0 will be clobbered. Reload from x19 after.
	// The duplicated env_ptr at +24 makes {drop_fn@16, env@24}
	// a callable sub-pair so a generic holder can free the env
	// via the embedded drop-fn pointer without static closure
	// identity. drop_fn = &__closure_drop_<name> (the per-closure
	// env-drop thunk, generated for every captured MakeClosure
	// target).
	g.emit("mov w0, #32")
	g.emit("bl __fern_alloc_rc1")
	g.adrpAdd("x1", op.Str)
	g.emit("str x1, [x0]")
	g.emit("str x19, [x0, #8]")
	// drop_fn = &__closure_drop_<name> when the IR generated the thunk
	// (only under RcFreeEnabled — the thunk references free-gated drop
	// helpers). Decide structurally on its presence in prog.Funcs, never
	// by re-reading the flag in codegen, so a free-OFF build (or a flag
	// toggled by a concurrent test) stores 0 instead of a dangling label.
	if _, ok := g.funcs["__closure_drop_"+op.Str]; ok {
		g.adrpAdd("x1", "__closure_drop_"+op.Str)
		g.emit("str x1, [x0, #16]")
	} else {
		g.emit("str xzr, [x0, #16]")
	}
	g.emit("str x19, [x0, #24]")
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

// dynVtableLabel returns the GAS symbol for the (trait-set, concrete)
// `dyn Trait` vtable cell. Single-trait keys are Fern identifiers, so the
// joined symbol is a valid assembler label as-is. A merged multi-trait
// key (ir.dynVtableSetKey joins with '+', e.g. "A+B") is sanitized: '+' →
// "_x_". Mirrors the x86-64 backend so the two natives emit
// identically-named cells.
func dynVtableLabel(trait, concrete string) string {
	return "__vtable_" + strings.ReplaceAll(trait, "+", "_x_") + "_" + concrete
}

// splitPair undoes the "<trait>/<concrete>" key used by dynVtableCells.
func splitPair(key string) (string, string) {
	if i := strings.IndexByte(key, '/'); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, ""
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
	out strings.Builder
	// noPeephole disables the streaming output peephole (Options.NoPeephole).
	noPeephole bool
	// peepWin is the streaming peephole's sliding window of recently emitted
	// logical lines (no trailing newline), held back from `out` until they
	// can no longer participate in a rewrite. See put / flushPeep.
	peepWin   []string
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
	// `__fern_alloc`) stay with their Linux-style names —
	// they're internal references the assembler resolves
	// locally before the object format matters.
	darwin bool

	// pie emits the static-PIE self-relocation prologue at `_start`
	// (see Options.PIE). Linux only.
	pie bool

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
	usesAlloc  bool
	usesStrcat bool
	usesMemcpy bool
	// usesCCall[n] gates the `__c_call<n>` FFI shim (call a C-ABI function
	// pointer with n integer args). The F32/F64 variants gate byte-identical
	// shims that differ only in the checker's declared FP result type, so the
	// call site reads d0/v0. See emitCCallRuntime.
	usesCCall    [5]bool
	usesCCallF32 [5]bool
	usesCCallF64 [5]bool
	usesStrcmp   bool
	// usesTcp pulls in the full TCP socket runtime
	// (__fern_tcp_listen / __fern_tcp_accept / __fern_tcp_recv
	// / __fern_tcp_send / __fern_tcp_close). Gated on call-
	// site reachability so non-server programs don't pay for
	// the socket boilerplate.
	usesTcp bool
	// usesPoll pulls in `__fern_poll(fds, timeout_ms)` — the std/task
	// reactor's readiness multiplexer (ppoll(2) on Linux; -1 stub on
	// Darwin pending kqueue).
	usesPoll bool
	// usesWasmPollableDrop pulls in the no-op `__fern_wasm_pollable_drop`
	// (a pollable is just an fd on native) so std/async's fetch_future
	// compiles + runs portably.
	usesWasmPollableDrop bool
	// usesWasmBlock pulls in the no-op `__fern_wasm_block` (no native pollable
	// to wait on — a deadline is poll(2)'s timeout arg) so std/async's
	// with_deadline blocks portably.
	usesWasmBlock bool
	// usesWasmTimerPollable pulls in `__fern_wasm_timer_pollable` returning
	// -1 on native (the deadline is poll(2)'s timeout arg) so std/async's
	// with_deadline is portable.
	usesWasmTimerPollable bool
	// usesWasmPoll pulls in `__fern_wasm_poll` returning -1 on native (no real
	// pollables; native readiness rides poll(2)) so std/async's wasm reactor
	// path is portable.
	usesWasmPoll bool
	// usesTimerFd pulls in `__fern_timer_fd(ms)` — a CLOCK_MONOTONIC
	// timerfd readable after `ms` (Linux; -1 stub on Darwin).
	usesTimerFd bool
	// usesStrSlice pulls in `__str_slice(base, low, high)` —
	// a length-prefix-aware substring extractor that
	// allocates a fresh string. The IR's `s[a:b]` slice
	// expression lowers to OpCallDirect{__str_slice}.
	usesStrSlice bool
	// usesSliceMake pulls in `__fern_slice_make(data, len)` —
	// allocates an 8-byte slice header { data_ptr, len }. Set
	// by recordUse() when the IR's slice-construction path
	// (a[lo:hi]) lowers to OpCallDirect{__slice_make}.
	usesSliceMake bool
	// usesSliceRange pulls in `__fern_slice_range(lo, hi, len)` —
	// the slice-construction bounds check (#5419): traps unless
	// 0 <= lo <= hi <= len, returns hi - lo.
	usesSliceRange bool
	// usesEnv pulls in `__fern_env(name)` — walks envp for a
	// NAME=VALUE match. Used by the synthesised auto-main's
	// `__port_from_env("PORT", 8080)` call.
	usesEnv bool
	// usesAllocU8 + usesStringFromBytes gate the string-
	// handling stdlib helpers that allocate length-prefixed
	// u8[] / string buffers.
	usesAllocU8         bool
	usesStringFromBytes bool
	// usesStrIdx tracks whether the program emits the SSO-aware
	// inlined `__str_idx` helper, which spills inline-tagged
	// strings to the `__fern_str_idx_scratch` .bss slot. Set
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
	// vtables holds the per-(trait,concrete) dispatch tables for
	// `dyn Trait` values (ir.collectVtables). OpConstVtable looks up
	// the method list here to emit the .rodata vtable cell. See
	// docs/DYN-TRAITS.md §4.2.2.
	vtables []ir.VtableDecl
	// dynVtableCells tracks the (trait, concrete) pairs referenced via
	// OpConstVtable. Each gets a `.rodata` symbol holding `len(methods)`
	// 8-byte absolute function pointers (interned per pair). Key is
	// "<trait>/<concrete>". Mirrors the x86-64 backend.
	dynVtableCells map[string]bool
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
	// usesRcInc / usesRcDec gate the refcount inc/dec runtime
	// helpers. Set when an OpCallDirect with target
	// "__fern_rc_inc" / "__fern_rc_dec" is reached. The IR
	// hasn't started emitting them yet (Phase 1c) but the
	// helpers + sentinel value (`.LArr_Empty`'s rc word =
	// 0x80000000) are in place so subsequent phases can wire
	// them in without touching the asm seam again. See
	// docs/RC-PERCEUS-PLAN.md.
	usesRcInc bool
	usesRcDec bool
	// rcInlineOK gates the #4402 opt-2b inline rc fast path per function.
	// Inlining expands each rc op from a single `bl` into ~10 instructions;
	// in the self-host compiler's largest lowering functions (irlower__
	// lower_expr is ~9.75M IR ops with ~1.66M rc ops) that bloat pushes the
	// function body past aarch64's ±128MB unconditional-branch reach, so the
	// intra-function `b .Lret_…` epilogue jumps overflow ("branch out of
	// range"). Set false for such a function (see rcInlineMaxOps) so its rc
	// ops fall back to the `bl` call form that already assembled — every
	// normal function (all user code, and all but the one self-host monster)
	// keeps the inline win.
	rcInlineOK bool
	// usesStrInc / usesStrDec / usesCellFree gate the two-word
	// string runtime helpers — the arm64 port of the wasm
	// __fern_str_inc / __fern_str_dec / __fern_cell_free. Tail-call
	// __fern_rc_inc / __fern_rc_dec / __fern_box_free / __fern_free
	// respectively on the heap path; inline-tagged values (top bit of
	// len) short-circuit. Set when an OpCallDirect names one of these
	// helpers in the IR walk.
	usesStrInc   bool
	usesStrDec   bool
	usesCellFree bool
	// usesRcUnderflowCount gates the Phase 3 detector reader
	// `__fern_rc_underflow_count` (returns the BSS over-release
	// counter that __fern_rc_dec bumps). Set when the IR emits the
	// matching OpCallDirect (the `__rc_underflow_count()` builtin).
	usesRcUnderflowCount bool
	// usesHeapBumpBytes gates the Phase 6 measurement reader
	// `__fern_heap_bump_bytes` (returns __fern_heap_ptr − __fern_heap_base,
	// the bump high-water mark). Set when the IR emits the matching
	// OpCallDirect; also pulls in the allocator (it reads its cursor).
	usesHeapBumpBytes bool
	// usesHeapMark gates the one-level arena checkpoint pair
	// `__fern_heap_mark` / `__fern_heap_release_to`. Set when the IR emits
	// either matching OpCallDirect; also pulls in the allocator (the pair
	// rewinds its cursor and snapshots its freelist heads).
	usesHeapMark bool
	// usesArrPushGrow gates `__fern_arr_push_grow` — the Phase 2
	// helper called by `emitArrayPush` to decide between in-
	// place mutation (rc==1 + cap available) and copy-into-new-
	// buffer (rc>1 OR cap exhausted). See
	// docs/RC-PERCEUS-PLAN.md "Phase 2" + `internal/ir/ir.go`'s
	// emitArrayPush.
	usesArrPushGrow bool
	// usesArrPushGrowPtr / usesArrPushGrowStr gate the rc-tracked-element
	// variants of __fern_arr_push_grow (#3425): identical fast path, but
	// the grow COPY retains each copied element (__fern_rc_inc for
	// single-word pointer elements, __fern_str_inc for two-word (data,
	// len) string pairs) so the fresh buffer independently owns its
	// references. Without the retain, the old buffer's later walk-drop
	// at rc==1 (__fern_drop_arr_str / deep struct walks) freed elements
	// the grown copy still referenced — a use-after-free. Mirrors
	// __fern_arr_cow_inplace_ptr (#4187), the `.with` sibling.
	usesArrPushGrowPtr bool
	usesArrPushGrowStr bool
	// usesArrPushGrowMovePtr / usesArrPushGrowMoveStr gate the self-append
	// (`a = a.append(v)`) siblings of the two above. They retain the copied
	// elements only when the incoming rc != 1, i.e. only when the assign's
	// buffer-only __fern_arr_dec will leave the old buffer alive under an
	// alias; at rc==1 that dec frees it without walking, so the elements
	// transfer and a retain would leak one reference each (#3457).
	usesArrPushGrowMovePtr bool
	usesArrPushGrowMoveStr bool
	// usesArrCowInPlace gates `__fern_arr_cow_inplace` — the
	// Phase 2b helper called by the IR's `arr[i] = v` lowering
	// for local-ident array targets. Returns the buffer the
	// caller should write into: same arr on rc==1 (no rc
	// change), fresh memcpy'd copy on rc>1 (with arr's rc
	// pre-dec'd inline so the caller doesn't double-dec).
	usesArrCowInPlace bool
	// usesArrCowInPlacePtr gates `__fern_arr_cow_inplace_ptr` — the
	// pointer-element variant that also inc's each copied element on the
	// COPY path so the fresh buffer independently owns them (see the
	// x86_64 mirror for why the plain memcpy would UAF).
	usesArrCowInPlacePtr bool
	// usesDropArrPtr gates `__fern_drop_arr_ptr` — the Phase 3
	// drop handler for arrays of pointer-shaped rc-tracked
	// elements. See x86_64's mirror + the wasm runtime.
	usesDropArrPtr bool
	// usesDropArrStr gates `__fern_drop_arr_str` — the Slice 4
	// drop handler for `string[]` under the two-word ABI: walks
	// the (data, len) elements calling __fern_str_dec and then
	// frees the buffer. Set when an OpCallDirect names this
	// helper in the IR walk. arm64 port of the wasm helper.
	usesDropArrStr bool
	// usesRcIsUnique gates `__fern_rc_is_unique` — the guarded
	// "last reference?" check used by the Phase 3 struct drop.
	usesRcIsUnique bool
	// usesFree gates `__fern_free` — the Phase 3 step-4 freelist
	// return path. Pulls in __fern_alloc (shares the freelist BSS).
	usesFree bool
	// usesArrDec gates `__fern_arr_dec` — the size-aware array dec
	// that frees the buffer at rc==0. Pulls in __fern_free.
	usesArrDec bool
	// usesAllocReuse gates `__fern_alloc_reuse` — the Phase 5
	// drop-reuse (FBIP) primitive `(token, tokenSize, size) -> ptr`.
	// Reuses a dropped block's storage in place on a size-class
	// match, else frees it and allocates afresh. Pulls in
	// __fern_alloc + __fern_free.
	usesAllocReuse bool
	// usesMapDrop gates `__fern_map_drop` — the Phase 3 map
	// reclamation handler that frees the buf + handle at rc==1.
	// Pulls in __fern_free when the flag is on.
	usesMapDrop bool
	// usesBoxFree gates `__fern_box_free` — the Phase 3 struct/enum
	// box reclamation helper `(data, size) -> data`. Pulls in
	// __fern_free.
	usesBoxFree bool
	// usesClosureDrop gates `__fern_closure_drop` — the closure
	// env/pair reclamation helper (frees the rc1 block at rc==1
	// using the size stashed at data-4, else dec's). Tail-calls
	// __fern_box_free / __fern_rc_dec.
	usesClosureDrop bool
	// usesPuts / usesWrite / usesPutchar pull in the stdout
	// builtins:
	//   print(s)   → __fern_puts    (string + newline, two write()s)
	//   write(s)   → __fern_write   (raw string, no newline)
	//   putchar(c) → __fern_putchar (1-byte write)
	// All routed through write(2) — fd 1, syscall numbers from
	// the linuxDarwinSysno map.
	usesPuts    bool
	usesWrite   bool
	usesPutchar bool
	// usesEprint pulls in `__fern_eprint(s)` — stderr counterpart
	// to print(). Two write(2)s to fd 2.
	usesEprint bool
	// usesStrBuf — strbuf_reset / strbuf_append / strbuf_take —
	// global mutable scratch buffer primitive for O(1) amortised
	// append. Mirror of the x86_64 backend's emission.
	usesStrBuf bool

	// usesExit pulls in `__fern_exit(code)` — direct exit syscall.
	// Doesn't return; the post-call push x0 the caller emits is
	// harmless because exit() never comes back.
	usesExit bool
	// usesArgs pulls in `__fern_args()` — materialises a fresh
	// length-prefixed `string[]` from the argc/argv stash the
	// `_start` / `_main` prologue captures off the kernel stack.
	// Result cached via `__fern_args_cache` so repeat calls are
	// O(1).
	usesArgs bool
	// usesNowUnixMs pulls in `__fern_now_unix_ms()` —
	// wall-clock-ms via the Linux `clock_gettime(CLOCK_REALTIME,
	// &ts)` syscall (asm-generic table #113). Returns
	// `tv_sec * 1000 + tv_nsec / 1_000_000` in x0 as i64.
	// Backs `time.instant_now()` on the arm64-Linux target —
	// without it, `now_unix_ms()` call sites dangle. Darwin
	// gets caught by the pre-scan and reported as a clean
	// "not yet ported" error (it would need libSystem
	// stitching or mach_absolute_time + mach_timebase_info).
	usesNowUnixMs bool
	// usesMonotonicNs pulls in `__fern_monotonic_ns()` — monotonic
	// nanoseconds via `clock_gettime(CLOCK_MONOTONIC, &ts)` (#113) on
	// Linux (returns `tv_sec * 1e9 + tv_nsec` in x0), or the
	// CNTVCT_EL0/CNTFRQ_EL0 architectural counter on Darwin.
	usesMonotonicNs bool
	// usesNowNs pulls in `__fern_now_ns()` — wall-clock nanoseconds since
	// the Unix epoch via `clock_gettime(CLOCK_REALTIME, &ts)` (#113) on
	// Linux (returns `tv_sec * 1e9 + tv_nsec` in x0), or `gettimeofday`
	// (BSD 116) scaled to ns on Darwin. The nanosecond twin of now_unix_ms.
	usesNowNs bool
	// usesSleepMs pulls in `__fern_sleep_ms(ms)` — best-effort sleep
	// for `ms` milliseconds via `nanosleep(&req, NULL)` (#101) on
	// Linux, or `select(0,…,&timeout)` (BSD 93) on Darwin; ms <= 0
	// returns immediately. Void.
	usesSleepMs bool
	// usesProcFork / usesProcWaitpid pull in `__fern_proc_fork()` —
	// clone(SIGCHLD,0,0,0,0) (#220) on Linux (arm64 has no bare fork
	// syscall) or fork (BSD 2, x1-flag normalised) on Darwin: 0 in
	// child, pid in parent, -errno on failure — and
	// `__fern_proc_waitpid(pid)` — wait4 (#260 Linux / BSD 7 Darwin)
	// + status-word decode: exit code 0..255 for a normal exit,
	// 128+signal for a signal death, -errno passthrough. The
	// crash-only supervision primitives (docs/CRASH-ONLY-SERVE.md D2').
	usesProcFork    bool
	usesProcWaitpid bool
	// usesFloatTranscendentals pulls in the f64 transcendental
	// runtime bundle — __fern_sin/cos/exp/log/pow_f64 plus their
	// shared .rodata polynomial-coefficient table. arm64 has no
	// hardware sin/cos/exp/log, so these are range-reduction +
	// minimax-polynomial approximations (a few ulp), ported from
	// the self-hosted compiler's asm_arm64.fern.
	usesFloatTranscendentals bool
	// usesReadLine pulls in `__fern_read_line()` — stdin
	// one-byte reader. Returns Option[string]: Some(line)
	// when at least one byte was read (line preserves its
	// trailing newline), None when first read returned 0.
	// Sized at 4 KiB via a .bss buffer; longer lines are
	// truncated.
	usesReadLine bool
	// usesStdin pulls in a 4-byte `__fern_stdin()` stub that
	// returns 0. The checker requires `stdin()` to be a
	// callable; we don't model per-fd Readers, so the helper
	// just returns a sentinel.
	usesStdin bool
	// usesRandomBytes pulls in `__fern_random_bytes(n)` —
	// allocates an n-byte string and fills it with kernel
	// CSPRNG output via `getrandom(2)` on Linux or chunked
	// `getentropy(2)` on Darwin. Suitable for session IDs,
	// tokens, etc.
	usesRandomBytes bool
	// usesRandomI32 pulls in `__fern_random_i32()` — a single
	// CSPRNG i32 via a 4-byte getrandom / getentropy read.
	usesRandomI32 bool
	// usesAsBytes pulls in `__method_string_as_bytes(s)` — the
	// non-copying `(data, len)` → slice<u8> view. Depends on
	// __fern_slice_make.
	usesAsBytes bool
	// usesReadFile / usesWriteFile pull in the file-I/O
	// runtimes `__fern_read_file(path)` /
	// `__fern_write_file(path, content)`. Both return enum
	// boxes — see emitReadFileRuntime / emitWriteFileRuntime
	// for the IR-matching layout.
	usesReadFile  bool
	usesWriteFile bool
	// usesRemoveFile / usesTempDir / usesReadDir / usesStat /
	// usesRemoveDirAll pull in the filesystem-op family (#5372):
	// unlinkat / mkdirat / getdents64 / fstatat runtimes with the
	// same Option[IoError] / Result[_, IoError] box shapes as the
	// file-I/O helpers.
	usesRemoveFile   bool
	usesTempDir      bool
	usesReadDir      bool
	usesStat         bool
	usesRemoveDirAll bool
	// usesIoError pulls in `__fern_io_error(errno, path)` —
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
	g.put(s)
}

func (g *generator) emit(format string, args ...any) {
	g.put("\t" + fmt.Sprintf(format, args...))
}

func (g *generator) label(name string) {
	g.put(name + ":")
}

// peepWindow is how many recently emitted logical lines are held back from
// `out` so the streaming peephole can rewrite the tail in place. The longest
// arm64 pattern is 2 lines; 4 leaves margin while bounding held memory to
// O(1) — a self-host `.s` is hundreds of MB and a whole-text post-pass would
// spike RAM. (Mirror of the x86-64 backend's peephole.)
const peepWindow = 4

// put appends one logical output line (without its trailing newline) to the
// peephole window, applies the safe local rewrites at the tail, then flushes
// any line that has aged out of the window to `out`. All emission funnels
// through here (line / emit / label), so the peephole sees every line in
// emission order regardless of which helper produced it.
func (g *generator) put(s string) {
	if g.noPeephole {
		g.out.WriteString(s)
		g.out.WriteByte('\n')
		return
	}
	g.peepWin = append(g.peepWin, s)
	g.peepholeTail()
	for len(g.peepWin) > peepWindow {
		g.out.WriteString(g.peepWin[0])
		g.out.WriteByte('\n')
		g.peepWin = g.peepWin[1:]
	}
}

// flushPeep drains the remaining window to `out`. Call once, right before
// returning the assembled text.
func (g *generator) flushPeep() {
	for _, l := range g.peepWin {
		g.out.WriteString(l)
		g.out.WriteByte('\n')
	}
	g.peepWin = g.peepWin[:0]
}

// peepholeTail applies the two safe stack-machine rewrites at the tail of the
// window. Both are purely local so they never touch a genuinely-live stack
// slot — those are left for the register allocator.
func (g *generator) peepholeTail() {
	w := g.peepWin
	n := len(w)

	// P1 — redundant store/reload: a push immediately followed by the
	// matching pop. push() emits `str x0, [sp, #-N]!`; pop() emits
	// `ldr DST, [sp], #N`. When adjacent, the slot is allocated and freed
	// across the two lines and nothing else reads it, so the net effect is
	// just `DST := x0`:
	//   str x0, [sp, #-N]! / ldr DST, [sp], #N  =>  mov DST, x0
	//     (or nothing when DST == x0)
	if n >= 2 {
		if k, ok := matchPushImm(w[n-2]); ok {
			if dst, k2, ok2 := matchPopReg(w[n-1]); ok2 && k2 == k {
				if dst == "x0" {
					g.peepWin = w[:n-2]
				} else {
					g.peepWin = append(w[:n-2], "\tmov "+dst+", x0")
				}
				return
			}
		}
	}

	// P2 — dead branch: an unconditional `b L` immediately followed by the
	// label `L:` is a no-op fall-through. Drop the branch; the label stays
	// for other branches. Only the bare unconditional `b ` matches — `bl`
	// (call) and `b.cond`/`blt`/`bhi`/… (conditional) do not begin with
	// "\tb ".
	if n >= 2 {
		last := w[n-1]
		if len(last) > 1 && last[len(last)-1] == ':' && last[0] != '\t' && !strings.ContainsRune(last, ' ') {
			if w[n-2] == "\tb "+last[:len(last)-1] {
				w[n-2] = last
				g.peepWin = w[:n-1]
			}
		}
	}
}

// matchPushImm matches a `\tstr x0, [sp, #-N]!` push and returns the N token.
func matchPushImm(line string) (string, bool) {
	const pfx = "\tstr x0, [sp, #-"
	const sfx = "]!"
	if strings.HasPrefix(line, pfx) && strings.HasSuffix(line, sfx) {
		return line[len(pfx) : len(line)-len(sfx)], true
	}
	return "", false
}

// matchPopReg matches a `\tldr <reg>, [sp], #N` pop into a single register and
// returns (<reg>, N). sp is excluded as a destination.
func matchPopReg(line string) (string, string, bool) {
	const pfx = "\tldr "
	const mid = ", [sp], #"
	if !strings.HasPrefix(line, pfx) {
		return "", "", false
	}
	rest := line[len(pfx):]
	i := strings.Index(rest, mid)
	if i <= 0 {
		return "", "", false
	}
	reg := rest[:i]
	imm := rest[i+len(mid):]
	if reg == "" || reg == "sp" || strings.ContainsAny(reg, " [],") || imm == "" {
		return "", "", false
	}
	return reg, imm, true
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

// syscallFstat emits fstat(fd, statbuf) — args already in x0/x1 — and
// normalises the error shape to Linux's -errno-in-x0 (so callers' sign
// checks work on both targets). Branched inline rather than via syscall()
// because Darwin and Linux disagree on both the number and the struct
// layout (see statSizeOff).
func (g *generator) syscallFstat() {
	if g.darwin {
		g.emit("mov x16, #%d", darFstat64)
		g.emit("svc #0x80")
		lbl := g.freshLabel("fstat_ok")
		g.emit("b.cc %s", lbl)
		g.emit("neg x0, x0")
		g.label(lbl)
		return
	}
	g.emit("mov x8, #%d", sysFstat)
	g.emit("svc #0")
}

// statSizeOff is the byte offset of st_size within the kernel's struct
// stat: 96 in Darwin's 64-bit-inode `struct stat`, 48 in Linux's.
func (g *generator) statSizeOff() int {
	if g.darwin {
		return 96
	}
	return 48
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

// emitPieSelfReloc emits the static-PIE self-relocation prologue at the
// top of `_start`. It computes the kernel-chosen load base via
// __ehdr_start (which the assembler resolves to vaddr 0, so adrp/:lo12:
// yields the base), then walks the R_AARCH64_RELATIVE entries in
// [__rela_start, __rela_end) — the `.quad <symbol>` function-pointer /
// vtable slots — applying each as *(base + r_offset) = base + r_addend.
// PC-relative code (adrp/:lo12:) needs no relocation, so reloc-free
// programs run an empty loop. Uses scratch x9-x14 only (nothing is set up
// at entry) and leaves sp / x0 untouched, so the normal startup that
// follows reads argv off the kernel stack unchanged.
func (g *generator) emitPieSelfReloc() {
	g.adrpAdd("x9", "__ehdr_start")  // x9 = load base
	g.adrpAdd("x10", "__rela_start") // x10 = &.rela.dyn (cursor)
	g.adrpAdd("x11", "__rela_end")   // x11 = end of .rela.dyn
	g.label(".Lfern_reloc_loop")
	g.emit("cmp x10, x11")
	g.emit("b.hs .Lfern_reloc_done")
	g.emit("ldr x12, [x10]")      // r_offset
	g.emit("ldr x13, [x10, #16]") // r_addend (r_info at +8 is RELATIVE)
	g.emit("add x14, x9, x13")    // base + addend
	g.emit("str x14, [x9, x12]")  // *(base + r_offset) = base + addend
	g.emit("add x10, x10, #24")   // advance one Elf64_Rela (24 bytes)
	g.emit("b .Lfern_reloc_loop")
	g.label(".Lfern_reloc_done")
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
	if g.pie && !g.darwin {
		g.emitPieSelfReloc()
	}
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
				g.adrpAdd("x3", "__fern_envp")
				g.emit("str x2, [x3]")
			}
			if g.usesArgs {
				g.adrpAdd("x3", "__fern_argc")
				g.emit("str x0, [x3]")
				g.adrpAdd("x3", "__fern_argv")
				g.emit("str x1, [x3]")
			}
		} else {
			g.emit("ldr x0, [sp]")   // argc
			g.emit("add x1, sp, #8") // argv = &sp[1]
			if g.usesEnv {
				g.emit("add x2, x0, #1")         // argc + 1
				g.emit("add x2, x1, x2, lsl #3") // envp = argv + (argc+1)*8
				g.adrpAdd("x3", "__fern_envp")
				g.emit("str x2, [x3]")
			}
			if g.usesArgs {
				g.adrpAdd("x3", "__fern_argc")
				g.emit("str x0, [x3]")
				g.adrpAdd("x3", "__fern_argv")
				g.emit("str x1, [x3]")
			}
		}
	}
	g.emit("bl main")
	if ast.LeakCheckEnabled {
		// Leak detector (#5362 slice 1): print the alloc/free summary
		// before exiting. main's return value parks in x19 (callee-save,
		// and _start has no caller to preserve it for; the report helper
		// itself only touches caller-saved registers) so the exit code
		// survives the report's syscalls.
		g.emit("mov x19, x0")
		g.emit("bl __fern_lc_report")
		g.emit("mov x0, x19")
	}
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

// rcInlineMaxOps is the per-function IR-op ceiling for the opt-2b inline rc
// fast path (see the rcInlineOK field). 1M sits ~2× above the largest normal
// self-host function (~0.5M ops) and ~10× below irlower__lower_expr (~9.75M
// ops), the only function whose inlined body overflows aarch64's ±128MB
// branch reach. A var (not a const) only so the backend's own tests can lower
// it to exercise the fall-back on a small function; production never
// reassigns it. (Mirrors the x86-64 backend's rcInlineMaxOps.)
var rcInlineMaxOps = 1_000_000

func (g *generator) emitFunc(fn *ast.FuncDecl, irFn *ir.Func) error {
	g.current = fn
	g.currentIR = irFn
	defer func() { g.current = nil; g.currentIR = nil; g.slotOffsets = nil }()

	// #4402 opt 2b: inline rc ops only when the function is small enough
	// that the ~10-instruction-per-op expansion can't push its body past
	// aarch64's ±128MB branch reach. Only the self-host compiler's largest
	// lowering function (irlower__lower_expr, ~9.75M ops) exceeds this; the
	// next-largest is ~0.5M ops, so the threshold has wide margin and every
	// user-scale function inlines. See the rcInlineOK field comment.
	g.rcInlineOK = len(irFn.Ops) <= rcInlineMaxOps

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
	// Emit each function into its OWN section (`-ffunction-sections` style) rather
	// than one monolithic `.text`. On AArch64 a `bl`/`R_AARCH64_CALL26` reaches only
	// ±128 MiB; GNU `ld` auto-inserts long-branch veneers BETWEEN input sections but
	// NOT within a single one, so a single `.text` larger than 128 MiB fails to link
	// with `relocation truncated to fit` (the self-host compiler binary is ~133 MB
	// and was right at that wall). Per-function sections let `ld` veneer every
	// cross-function call, lifting the limit to the ±4 GiB ADRP range. ELF/Linux
	// only — the arm64-darwin Mach-O path links via clang+lld, which already inserts
	// range-extension thunks within a section, and uses `__TEXT,__text` sections.
	if !g.darwin {
		g.line(fmt.Sprintf(".section .text.%s,\"ax\",@progbits", fn.Name))
	}
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
		g.emitSpSub(localsSize)
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
	for i := 0; i < len(irFn.Ops); i++ {
		// Compare-and-branch fusion (#4378): an integer comparison whose
		// 0/1 result flows straight into the OpIf / OpBrIf that follows it
		// (through zero or more OpNots) emits `cmp; b.cond` instead of the
		// `cmp; cset; …; cbz/cbnz` materialise-and-retest chain. Safe
		// because the IR is single-use: the branch is the comparison's only
		// consumer.
		if adv, ok := g.tryFuseCmpBranch(irFn.Ops, i, &scope); ok {
			i += adv
			continue
		}
		if err := g.emitOp(irFn.Ops[i], frameSize, retLabel, &scope); err != nil {
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
// Frames over 4095 bytes (|off| > 4095) overflow the 12-bit
// add/sub immediate; frameAddrX16 then materialises the
// offset into x16 via movz(+movk) and uses a register-operand
// add/sub instead (the self-host compiler's emit_expr trips
// this once its locals pass ~4 KiB).
// frameAddrX16 materialises the address `x29 + off` into x16 for a frame
// offset whose magnitude exceeds the 12-bit add/sub immediate range
// (|off| > 4095). The absolute offset is loaded into x16 via `movz`
// (+`movk` for the high half when it exceeds 16 bits — frames up to 4 GiB),
// then added to / subtracted from the frame pointer with a register-operand
// add/sub. x16 (IP0) is the call-clobberable scratch and never carries a
// live value across a frame load/store, so reusing it for both the offset
// and the resulting address is safe.
// emitSpSub allocates `bytes` of stack in the prologue: `sub sp, sp, #bytes`
// when the size fits the 12-bit add/sub immediate (<= 4095), otherwise the
// size is materialised into x16 (movz +movk) and a register-operand `sub`
// adjusts sp. x16 (IP0) is free in the prologue — params haven't been spilled
// yet. The epilogue restores sp via `mov sp, x29`, so it needs no counterpart.
func (g *generator) emitSpSub(bytes int) {
	if bytes <= 4095 {
		g.emit("sub sp, sp, #%d", bytes)
		return
	}
	g.emit("movz x16, #%d", bytes&0xFFFF)
	if bytes > 0xFFFF {
		g.emit("movk x16, #%d, lsl #16", (bytes>>16)&0xFFFF)
	}
	g.emit("sub sp, sp, x16")
}

func (g *generator) frameAddrX16(off int32) {
	abs := off
	if abs < 0 {
		abs = -abs
	}
	g.emit("movz x16, #%d", abs&0xFFFF)
	if abs > 0xFFFF {
		g.emit("movk x16, #%d, lsl #16", (abs>>16)&0xFFFF)
	}
	if off < 0 {
		g.emit("sub x16, x29, x16")
	} else {
		g.emit("add x16, x29, x16")
	}
}

func (g *generator) frameLoad(reg string, off int32) {
	if off >= -256 && off <= 255 {
		g.emit("ldur %s, [x29, #%d]", reg, off)
		return
	}
	abs := off
	if abs < 0 {
		abs = -abs
	}
	if abs <= 4095 {
		if off < 0 {
			g.emit("sub x16, x29, #%d", -off)
		} else {
			g.emit("add x16, x29, #%d", off)
		}
	} else {
		g.frameAddrX16(off)
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
	if abs <= 4095 {
		if off < 0 {
			g.emit("sub x16, x29, #%d", -off)
		} else {
			g.emit("add x16, x29, #%d", off)
		}
	} else {
		g.frameAddrX16(off)
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

// twoWordStrHelperArgSlots is the hardcoded string-arg table the
// OpCallDirect slot-count logic falls back to for built-in runtime
// helpers that take a two-word string but are emitted with a bare
// I32 arg count and no ArgTypes. The value is the total number of
// operand-stack slots the helper's arguments occupy (each two-word
// string counts as 2). __fern_str_inc / __fern_str_dec both take a
// single (data, len) string → 2 slots.
var twoWordStrHelperArgSlots = map[string]int{
	"__fern_str_inc":           2,
	"__fern_str_dec":           2,
	"__method_string_as_bytes": 2,
}

// callArgTypes returns the parameter types of an OpCallDirect /
// OpCallDirectPair, preferring the IR-stamped `op.ArgTypes()`
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
	if len(op.ArgTypes()) > 0 {
		return op.ArgTypes()
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
	case "__fern_str_inc":
		// Two-word string retain: returns the (data, len) pair
		// unchanged in (x0, x1). The post-call push must emit BOTH
		// words so the retained string stays on the operand stack
		// (e.g. for OpReturn of an aliased string). __fern_str_dec
		// is deliberately absent — it returns only `data` (x0).
		return true
	case "random_bytes", "tcp_recv", "string_from_bytes_unchecked", "__str_slice", "strbuf_take":
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

// ccallFloatRetWidth reports the FP-return width (32 / 64) of a
// `__c_call<n>_f32` / `_f64` FFI shim, or 0 for any other call. Those shims
// tail-branch to a C-ABI function, whose f32/f64 result comes back in d0 (the
// C convention), while Fern keeps FP operand-stack values in x0. After such a
// call the result must be moved d0→x0 before it's pushed. (x86-64 mirror in
// the x86_64 package.)
func ccallFloatRetWidth(name string) int {
	if !strings.HasPrefix(name, "__c_call") {
		return 0
	}
	if strings.HasSuffix(name, "_f64") {
		return 64
	}
	if strings.HasSuffix(name, "_f32") {
		return 32
	}
	return 0
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
// value: 1 means inline, 0 means heap pointer. __fern_alloc returns
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
// strcat / str_slice / string_from_bytes_unchecked / random_bytes / env /
// read_file / tcp_recv / Reader.read_chunk all materialise a
// fresh string and write its length through this one site, so
// future encoding changes that affect string construction (e.g.
// tagged-pointer inline-when-short) have a single function to
// update per backend. Array-length stores (in `__alloc_u8`,
// `__fern_args`) stay open-coded since arrays may diverge.
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
// string_from_bytes_unchecked) to short-circuit the alloc + memcpy + length-
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
// Matches `fernstring.LengthNative` exactly. Dead today (no
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

// emitNulTermPath2W allocates `lenX + 1` bytes on the bump heap,
// memcpys `lenX` bytes from `dataX`, and writes a trailing NUL —
// producing a NUL-terminated C string in `dstX` suitable for
// passing as the path argument to openat / etc.
//
// The two-word string ABI carries (data, len) with no trailing
// NUL, and the bump heap leaves no zero pad between adjacent
// same-16-byte-aligned allocations — so if `lenX` is 0 mod 16
// (e.g. "examples/tests/strings_test.fern" is 32 bytes) the
// byte after the path data is the first byte of the next
// allocation. The kernel happily reads past the intended end
// and openat sees a concatenated path, failing with ENOTDIR.
//
// Caller assumptions:
//   - `dstX` and `lenX` are distinct callee-saved registers
//     (x19..x28) that survive the `bl` calls.
//   - `dataX` may alias `dstX` — the helper stashes the src on
//     the stack before alloc so the original byte pointer
//     survives `mov dstX, x0`.
//   - The caller has 16 bytes of headroom past sp for the
//     temporary stack push.
//   - x0..x9 are clobbered.
func (g *generator) emitNulTermPath2W(dstX, dataX, lenX string) {
	// Stash src on the stack so it survives the dst overwrite
	// (callers commonly pass dataX == dstX to reuse a scratch
	// register; the `mov dstX, x0` after alloc would otherwise
	// clobber the byte pointer before the memcpy load).
	g.emit("str %s, [sp, #-16]!", dataX)
	g.emit("mov x0, %s", lenX)
	g.emit("add x0, x0, #1")
	g.emit("bl __fern_alloc")
	g.emit("mov %s, x0", dstX)
	g.emit("mov x0, %s", dstX)
	g.emit("ldr x1, [sp], #16") // restore src into x1 for memcpy
	g.emit("mov x2, %s", lenX)
	g.emit("bl __fern_memcpy")
	g.emit("mov w9, #0")
	g.emit("strb w9, [%s, %s]", dstX, lenX)
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

// emitArrayLen loads the i32 length of the length-prefixed array
// whose data pointer lives in srcX into dstW. Today this is a
// 4-byte little-endian load from `[srcX - 4]`. Centralised seam
// for arrays: parallels emitStrLen but stays distinct because
// arrays may diverge from strings under future layout changes
// (inline u8[], typed-element headers, etc.). Used by
// __alloc_u8's siblings, the __arr_idx bounds checks (where
// they exist), and string_from_bytes_unchecked's input length read.
func (g *generator) emitArrayLen(dstW, srcX string) {
	g.emit("ldur %s, [%s, #-4]", dstW, srcX)
}

// emitArrayLenStore writes the i32 length in srcW to the 4-byte
// little-endian length prefix at `[dstX - 4]`, where dstX is the
// new array's *data pointer* (one past the prefix). Inverse of
// emitArrayLen. Used by __alloc_u8 and __fern_args (outer
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
	_ = width            // payload width handled by the in-register move below
	g.pop()              // payload → x0
	g.emit("mov x1, x0") // save payload in x1
	g.emit("mov x0, #%d", tag)
	g.push()             // push tag
	g.emit("mov x0, x1") // restore payload
	g.push()             // push payload
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
//
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

// condBranchFar emits a conditional branch to `target` that stays in range even
// in very large functions. aarch64's cbz/cbnz/b.cond reach only ±1MB and GAS
// does not relax them, so a single huge emitted function (e.g. the self-host
// compiler's giant dispatch routines) can overflow with "conditional branch out
// of range". We instead take the INVERTED test over a short forward branch and
// reach the real target with an unconditional `b` (±128MB range). `skipInsn` is
// the instruction that skips the jump — the inverse of the intended branch
// (e.g. "cbnz" to realise a "cbz" target, "cbz" for "cbnz").
func (g *generator) condBranchFar(skipInsn, reg, target string) {
	skip := g.freshLabel("brFar")
	g.emit("%s %s, %s", skipInsn, reg, skip)
	g.emit("b %s", target)
	g.label(skip)
}

// condBranchFarCC is condBranchFar's flag-based sibling: a range-safe
// conditional branch that reaches `target` when condition code `fireCC` holds.
// `skipCC` is fireCC's inverse. aarch64's `b.cond` reaches only ±1MB and GAS
// won't relax it, so a direct `b.<fireCC> target` can overflow ("conditional
// branch out of range") in a very large emitted function; we take the inverted
// test over a short forward skip and reach `target` with an unconditional `b`
// (±128MB). Used by the compare-and-branch fusion (#4378).
func (g *generator) condBranchFarCC(fireCC, skipCC, target string) {
	skip := g.freshLabel("brFar")
	g.emit("b.%s %s", skipCC, skip)
	g.emit("b %s", target)
	g.label(skip)
}

// isFusableCompare reports whether an integer comparison op produces a 0/1
// boolean the compare-and-branch fusion can turn into a direct conditional
// branch. Float compares are excluded: NaN breaks the negation identity
// (`!(a < b)` is not `a >= b` when either is NaN), so their `fcmp; cset`
// materialisation stays. Mirrors the x86-64 predicate of the same name.
func isFusableCompare(k ir.OpKind) bool {
	switch k {
	case ir.OpEq, ir.OpNe, ir.OpLtS, ir.OpLeS, ir.OpGtS, ir.OpGeS:
		return true
	}
	return false
}

// armCondFor maps an integer comparison to the AArch64 condition code that
// holds when the comparison is TRUE (cc) and its inverse (invcc), assuming
// `cmp lhs, rhs` has already set the flags. `unsigned` selects the unsigned
// codes (lo/ls/hi/hs) over the signed ones (lt/le/gt/ge) — the b.cond
// counterpart of the `cset` code selection in the OpLtS/… cases.
func armCondFor(k ir.OpKind, unsigned bool) (cc, invcc string) {
	switch k {
	case ir.OpEq:
		return "eq", "ne"
	case ir.OpNe:
		return "ne", "eq"
	case ir.OpLtS:
		if unsigned {
			return "lo", "hs"
		}
		return "lt", "ge"
	case ir.OpLeS:
		if unsigned {
			return "ls", "hi"
		}
		return "le", "gt"
	case ir.OpGtS:
		if unsigned {
			return "hi", "ls"
		}
		return "gt", "le"
	case ir.OpGeS:
		if unsigned {
			return "hs", "lo"
		}
		return "ge", "lt"
	}
	return "", ""
}

// tryFuseCmpBranch fuses `cmp (Not)* {If|BrIf}` into `cmp; b.cond` (#4378, the
// arm64 mirror of the x86-64 slice). An integer comparison whose 0/1 result
// flows straight into the following OpIf / OpBrIf — through any number of
// OpNots — becomes a compare that directly feeds a conditional branch, instead
// of the `cmp; cset; …; cbz/cbnz` materialise-and-retest chain. Returns the
// number of EXTRA ops consumed past index i (the caller advances by that) and
// whether the fusion fired; when it does not fire the op stream is untouched
// and normal per-op emission runs. The operand-stack effect is identical to the
// un-fused path (two operands popped, no boolean pushed then re-popped).
func (g *generator) tryFuseCmpBranch(ops []ir.Op, i int, scope *[]irScope) (int, bool) {
	cmp := ops[i]
	if !isFusableCompare(cmp.Kind) {
		return 0, false
	}
	j := i + 1
	nots := 0
	for j < len(ops) && ops[j].Kind == ir.OpNot {
		nots++
		j++
	}
	if j >= len(ops) {
		return 0, false
	}
	br := ops[j]
	// OpBrIf branches when the effective condition is TRUE; OpIf branches to
	// its else-label when the effective condition is FALSE. Each OpNot flips
	// which comparison outcome that is.
	whenTrue := br.Kind == ir.OpBrIf
	if nots%2 == 1 {
		whenTrue = !whenTrue
	}
	cc, invcc := armCondFor(cmp.Kind, cmp.Unsigned)
	fireCC, skipCC := cc, invcc
	if !whenTrue {
		fireCC, skipCC = invcc, cc
	}
	switch br.Kind {
	case ir.OpIf:
		g.binPop()
		g.cmpForWidth(cmp.Width)
		elseL := g.freshLabel("ifElse")
		endL := g.freshLabel("ifEnd")
		g.condBranchFarCC(fireCC, skipCC, elseL)
		*scope = append(*scope, irScope{kind: ir.OpIf, brTarget: endL, endLabel: endL, elseLabel: elseL})
		return j - i, true
	case ir.OpBrIf:
		g.binPop()
		g.cmpForWidth(cmp.Width)
		target := (*scope)[len(*scope)-1-int(br.I32)].brTarget
		g.condBranchFarCC(fireCC, skipCC, target)
		return j - i, true
	}
	return 0, false
}

// loadImm32 / loadImm64 materialise an immediate into `reg` with movz/movk
// instead of the `ldr reg, =N` literal-pool form. A literal pool is reached by
// a pc-relative load with only ±1MB range; in a very large emitted function the
// pool drifts out of range ("pc-relative load offset out of range"). movz/movk
// have no pc-relative dependency, so they are range-safe at any function size.
// Zero 16-bit chunks are skipped (movz first clears the register).
func (g *generator) loadImm32(reg string, v uint32) {
	g.emit("movz %s, #%d", reg, v&0xffff)
	if (v>>16)&0xffff != 0 {
		g.emit("movk %s, #%d, lsl #16", reg, (v>>16)&0xffff)
	}
}
func (g *generator) loadImm64(reg string, v uint64) {
	g.emit("movz %s, #%d", reg, v&0xffff)
	if (v>>16)&0xffff != 0 {
		g.emit("movk %s, #%d, lsl #16", reg, (v>>16)&0xffff)
	}
	if (v>>32)&0xffff != 0 {
		g.emit("movk %s, #%d, lsl #32", reg, (v>>32)&0xffff)
	}
	if (v>>48)&0xffff != 0 {
		g.emit("movk %s, #%d, lsl #48", reg, (v>>48)&0xffff)
	}
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
	case ir.OpLine:
		// Source-line marker → DWARF `.loc` directive (file 1). Emits no
		// machine code; the assembler records the current text offset as a
		// .debug_line row. `.loc` never matches a peephole instruction
		// pattern, so it only ever prevents a fusion at a line boundary.
		g.line(fmt.Sprintf("\t.loc 1 %d %d", op.Pos.Line, op.Pos.Col))
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
			g.loadImm32("w0", uint32(v))
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
		g.loadImm64("x0", uint64(op.I64))
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
		// fernstring.PackInlineNative) is a follow-up
		// optimisation; for now every literal goes through the
		// .rodata data segment + a runtime byte length.
		lbl := g.internString(op.Str)
		g.adrpAdd("x0", lbl)
		g.push() // data
		// `mov w0, #N` only takes a 16-bit immediate; a string literal
		// longer than 64 KiB needs the literal-pool form, exactly like
		// the OpConstI32 path above. See docs/ADVERSARIAL-REVIEW-2026-06.md (B3).
		if n := len(op.Str); n <= 0xffff {
			g.emit("mov w0, #%d", n)
		} else {
			g.loadImm32("w0", uint32(n))
		}
		g.push() // len

	case ir.OpConstFunc:
		// Function values materialise as static 16-byte
		// closure-pair cells in .rodata: { fn_ptr (8B),
		// env_ptr=0 (8B) }. Mirrors the x86-64 + wasm shape so
		// OpCallIndirect can uniformly deref every callee
		// pair — top-level fn values (env=0) and runtime-
		// allocated closures (env points at the captured-slot
		// block) reach the same dispatch path.
		if g.constFuncCells == nil {
			g.constFuncCells = map[string]bool{}
		}
		g.constFuncCells[op.Str] = true
		g.adrpAdd("x0", g.closureCellSym(op.Str))
		g.push()

	case ir.OpConstVtable:
		// Materialise the address of the static (trait, concrete)
		// vtable into x0 via the same `adrp + add :lo12:` PC-relative
		// pair OpConstFunc / OpConstStr use. The vtable is a `.rodata`
		// array of absolute `__method_*` function pointers, one per
		// non-associated trait method in declaration order
		// (docs/DYN-TRAITS.md §4.2.2). On natives the vtable holds
		// POINTERS, not table indices (the wasm form): OpCallDyn loads
		// slot k and `blr`s it directly.
		key := op.Str + "/" + op.Str2()
		if g.dynVtableCells == nil {
			g.dynVtableCells = map[string]bool{}
		}
		g.dynVtableCells[key] = true
		g.adrpAdd("x0", dynVtableLabel(op.Str, op.Str2()))
		g.push()

	case ir.OpBoxDyn:
		// Pack a boxed one-word `dyn Trait` value (docs/DYN-TRAITS.md
		// §4.2.2). Operand stack on entry: [data, vtable] (vtable on
		// top). Allocate a 16-byte {data @0, vtable @8} cell via the
		// normal __fern_alloc path, store both words, and push the cell
		// pointer.
		//
		// __fern_alloc clobbers x0..x14 (its bump/freelist body), so we
		// CANNOT hold data / vtable in caller-save scratch across the
		// `bl` the way x86-64 does with r10/r11 (which x86's __fern_alloc
		// preserves). Park them in the callee-saved x19/x20 instead. The
		// two operands are on top of the operand stack, so pop them FIRST
		// (into caller-save x9/x10 — plain loads, no call between), THEN
		// save x19/x20 below the now-shorter operand stack and move the
		// operands in; that way the saved-register pair never aliases the
		// operands the pops read. The cell is a plain heap object;
		// precise RC of the box is out of scope (it leaks — the interp
		// doesn't RC trait objects either).
		g.emit("ldr x10, [sp], #%d", slotBytes) // x10 = vtable (top)
		g.emit("ldr x9, [sp], #%d", slotBytes)  // x9  = data
		g.emit("stp x19, x20, [sp, #-16]!")     // preserve callee-saves
		g.emit("mov x19, x9")                   // x19 = data
		g.emit("mov x20, x10")                  // x20 = vtable
		g.emit("mov w0, #16")                   // cell size = 2 * ptrW
		g.emit("bl __fern_alloc")
		g.emit("str x19, [x0]")           // cell[0] = data
		g.emit("str x20, [x0, #8]")       // cell[8] = vtable
		g.emit("ldp x19, x20, [sp], #16") // restore callee-saves
		g.push()

	case ir.OpCallDyn:
		// Dispatch a `dyn Trait` method call (docs/DYN-TRAITS.md
		// §4.2.2). Operand stack on entry: [data, args..., vtable]
		// (vtable on top). Pop the vtable, load slot `op.I32`'s 8-byte
		// function pointer (`vtable + slot*8`), then do an indirect call
		// with [data, args...] as the AAPCS64 args (receiver-first,
		// plain — no closure env). op.Sig() is the receiver-first method
		// signature; argc = len(params) (= 1 receiver + method args),
		// void iff Result == nil.
		if op.Sig() == nil {
			return fmt.Errorf("arm64: OpCallDyn missing op.Sig()")
		}
		g.pop()               // x0 = vtable (top)
		g.emit("mov x17, x0") // x17 = vtable base
		if op.I32 != 0 {
			g.emit("ldr x16, [x17, #%d]", int(op.I32)*8)
		} else {
			g.emit("ldr x16, [x17]")
		}
		// x16 = fn pointer. emitCallArgsLoad only touches x0..x7 (+ x9
		// for overflow copies), so x16 / x17 survive the arg load — same
		// intra-procedure scratch OpCallIndirect uses for its fn_ptr.
		//
		// Under the two-word string ABI a string param occupies 2
		// operand-stack slots → 2 arg registers. The receiver (param[0],
		// a StructType pointer) is always 1 slot; method params follow.
		// Translate the user-visible param count into the effective slot
		// count via op.Sig() — same fan-out OpCallIndirect / OpCallDirect
		// apply.
		slotCount := len(op.Sig().Params)
		if ast.UseTwoWordStrings(8) {
			slotCount = 0
			for _, t := range op.Sig().Params {
				if _, isStr := t.(ast.StringType); isStr {
					slotCount += 2
				} else {
					slotCount += 1
				}
			}
		}
		g.emitCallArgsLoad(slotCount)
		g.emit("blr x16")
		g.emitCallArgsCleanup(slotCount)
		if op.Sig().Result == nil {
			break
		}
		// String result arrives as (data, len) in (x0, x1) under the
		// two-word ABI — push both halves; other results push x0 only.
		if ast.UseTwoWordStrings(8) {
			if _, isStr := op.Sig().Result.(ast.StringType); isStr {
				g.push() // data (x0)
				g.emit("mov x0, x1")
				g.push() // len
				break
			}
		}
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
		g.pop() // payload → x0
		g.emit("mov x1, x0")
		g.pop() // tag → x0
		g.emit("b %s", retLabel)
	case ir.OpMakeSomeI32, ir.OpMakeOkI32:
		// Native fallback: heap-box layout matching
		// `payloadLayout`. `op.Width` selects the payload
		// store: zero (default) means i32 → alloc 8, payload
		// at +4 (4 bytes). WidthPtr means pointer-shape on
		// arm64 → alloc 16 (8-byte alignment for the 8-byte
		// payload), payload at +8 (8 bytes). x19 is
		// callee-save so it survives the bl __fern_alloc.
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
		// Width=WidthString drops a two-slot (data, len) pair that
		// rides the operand stack under the two-word string ABI (the
		// shape __fern_str_inc / payloadLoadOpFor produce). Mirrors
		// the wasm OpDrop branch. Set by the Map[K, string] set
		// retain + by copyprop when it rewrites a dead OpStoreLocal
		// on a string slot.
		if op.Width == ir.WidthString {
			g.emit("add sp, sp, #%d", 2*slotBytes)
		} else {
			g.emit("add sp, sp, #%d", slotBytes)
		}

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
		// Width matters: an i32 value rides zero-extended in the
		// low half of x0, so the w-form (`lsl w0, w1, w0`) masks
		// the count to 0..31 and keeps the result in the i32 lane,
		// while the x-form would mask to 0..63 — diverging from the
		// wasm / interp shift-count semantics for i32.
		g.binPop()
		r := g.regForWidth(op.Width)
		g.emit("lsl %s0, %s1, %s0", r, r, r)
		g.push()
	case ir.OpShrS:
		// Right shift: `asr` (arithmetic) shifts in the sign
		// bit — correct for signed types but it leaves
		// `(u64::MAX >> 1)` at `0xFFFF…` instead of
		// `0x7FFF…` because the sign bit propagates. Pick
		// `lsr` (logical) for unsigned operands. Width matters
		// for `asr`: a negative i32 rides zero-extended in x0
		// (top 32 bits clear), so the x-form would read bit 63
		// (= 0) as the sign and yield a logical-looking result.
		// The w-form reads the real i32 sign bit (bit 31).
		g.binPop()
		r := g.regForWidth(op.Width)
		if op.Unsigned {
			g.emit("lsr %s0, %s1, %s0", r, r, r)
		} else {
			g.emit("asr %s0, %s1, %s0", r, r, r)
		}
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
		// Unsigned operands (u8/u32/u64/usize) need the
		// unsigned condition code `lo` (lower); signed uses `lt`.
		// Without this, a u32 like 4294967295 — which has bit 31
		// set and so reads as negative under signed compare —
		// orders wrong against small values. x86 / wasm already
		// branch on op.Unsigned here.
		if op.Unsigned {
			g.emit("cset w0, lo")
		} else {
			g.emit("cset w0, lt")
		}
		g.push()
	case ir.OpLeS:
		g.binPop()
		g.cmpForWidth(op.Width)
		if op.Unsigned {
			g.emit("cset w0, ls")
		} else {
			g.emit("cset w0, le")
		}
		g.push()
	case ir.OpGtS:
		g.binPop()
		g.cmpForWidth(op.Width)
		if op.Unsigned {
			g.emit("cset w0, hi")
		} else {
			g.emit("cset w0, gt")
		}
		g.push()
	case ir.OpGeS:
		g.binPop()
		g.cmpForWidth(op.Width)
		if op.Unsigned {
			g.emit("cset w0, hs")
		} else {
			g.emit("cset w0, ge")
		}
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
			g.pop()                   // x0 = len (top)
			g.frameStore("x0", off)   // store len
			g.pop()                   // x0 = data
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
			g.pop()                   // x0 = len
			g.emit("mov x1, x0")      // x1 = len
			g.pop()                   // x0 = data
			g.frameStore("x0", off+8) // store data
			g.frameStore("x1", off)   // store len
			// Re-push (data, len) so the value stays on the stack.
			g.push() // push data (x0)
			g.emit("mov x0, x1")
			g.push() // push len
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
		// Range-safe `cbz w0, elseL` (see condBranchFar): skip the far jump
		// when w0 != 0 (cbnz), else fall into the unconditional `b elseL`.
		g.condBranchFar("cbnz", "w0", elseL)
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
		// low 32 bits since i32 truthiness is i32-shaped. Range-safe
		// `cbnz w0, target` (see condBranchFar): skip when w0 == 0.
		g.condBranchFar("cbz", "w0", target)

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
			g.pop()                    // addr → x0
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

	case ir.OpAlloc:
		g.usesAlloc = true
		g.pop()
		g.emit("bl __fern_alloc")
		g.push()

	case ir.OpStrEq:
		// Equality via __fern_strcmp returning 0 (equal) /
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
			g.emit("bl __fern_strcmp")
			g.emit("cmp x0, #0")
			g.emit("cset w0, eq")
			g.push()
			break
		}
		// Legacy single-register.
		g.emit("ldr x1, [sp], #16")
		g.emit("ldr x0, [sp], #16")
		g.emit("bl __fern_strcmp")
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
		g.loadImm64("x0", uint64(int64(bits)))
		g.push()
	case ir.OpConstF64:
		bits := math.Float64bits(op.F64)
		g.loadImm64("x0", uint64(int64(bits)))
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
	case ir.OpReinterpretI32F32, ir.OpReinterpretF32I32,
		ir.OpReinterpretI64F64, ir.OpReinterpretF64I64:
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
		// to "__fern_strcat") so codegen owns the dispatch and
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
			g.emit("bl __fern_strcat")
			g.push() // push data (x0)
			g.emit("mov x0, x1")
			g.push() // push len
			break
		}
		g.emit("ldr x1, [sp], #16") // b
		g.emit("ldr x0, [sp], #16") // a
		g.emit("bl __fern_strcat")
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
		if ast.UseTwoWordStrings(8) && op.Sig() != nil {
			slotCount = 1 // env_ptr
			for _, t := range op.Sig().Params {
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
		if ast.UseTwoWordStrings(8) && op.Sig() != nil {
			if _, isStr := op.Sig().Result.(ast.StringType); isStr {
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

	case ir.OpRcInc, ir.OpRcDec:
		// #4402 opt 2b: inline the rc fast path (arm64 mirror of the
		// x86-64 slice) — the hot no-op guards (null / SSO inline-tag /
		// below-heap / static sentinel) and the RMW happen without a
		// `bl` and no caller-save spill. Semantics mirror
		// emitRcIncRuntime / emitRcDecRuntime instruction-for-
		// instruction, including rc_dec's underflow-counter bump. The
		// helpers stay emitted (runtime code tail-calls them, and the
		// RcFreeDebug build below still calls out for the RcPoison
		// use-after-free trap the inline path omits). rc ops are
		// pass-through: x0 holds the pointer and doubles as the result.
		// The guard branches all target `done` a few instructions ahead,
		// so plain cbz/tbnz/b.lo are in range (no condBranchFar needed).
		//
		// Keep the helper emitted (it used to be gated off this op
		// reaching the OpCallDirect switch): runtime helpers tail-call it
		// and the RcFreeDebug `bl` below needs a target.
		if op.Kind == ir.OpRcInc {
			g.usesRcInc = true
		} else {
			g.usesRcDec = true
		}
		// RcFreeDebug keeps the call for the RcPoison trap; !rcInlineOK falls
		// back to the call in functions too large to absorb the inline bloat.
		if ast.RcFreeDebug || !g.rcInlineOK {
			g.pop()
			g.emit("bl %s", op.Str)
			g.push()
			return nil
		}
		done := g.freshLabel("rcopDone")
		g.pop()                         // x0 = ptr
		g.emit("cbz x0, %s", done)      // null
		g.emit("tbnz x0, #0, %s", done) // SSO inline-tag (bit 0 set)
		g.emit("mov x1, #1")
		g.emit("lsl x1, x1, #28") // x1 = 0x1000_0000 heap base hint
		g.emit("cmp x0, x1")
		g.emit("b.lo %s", done)          // below heap
		g.emit("ldur w1, [x0, #-8]")     // rc
		g.emit("tbnz w1, #31, %s", done) // static sentinel (negative)
		if op.Kind == ir.OpRcInc {
			g.emit("add w1, w1, #1")
		} else {
			// Underflow detector: a healthy dec sees rc >= 1; rc <= 0
			// here is an over-release — bump the counter, then still dec.
			decLbl := g.freshLabel("rcopDec")
			g.emit("cmp w1, #0")
			g.emit("b.gt %s", decLbl)
			g.adrpAdd("x2", "__fern_rc_underflow")
			g.emit("ldr w3, [x2]")
			g.emit("add w3, w3, #1")
			g.emit("str w3, [x2]")
			g.label(decLbl)
			g.emit("sub w1, w1, #1")
		}
		g.emit("stur w1, [x0, #-8]")
		g.label(done)
		g.push()
		return nil
	case ir.OpRcIsUnique:
		// #4402 opt 2b: inline is_unique — load, sentinel test, ==1
		// compare. Mirrors emitRcIsUniqueRuntime (whose guards differ
		// from inc/dec: low bound 0x10000, no SSO-tag test). Result i32
		// in w0.
		g.usesRcIsUnique = true // keep the helper emitted (RcFreeDebug / large-fn bl)
		if ast.RcFreeDebug || !g.rcInlineOK {
			g.pop()
			g.emit("bl %s", op.Str)
			g.push()
			return nil
		}
		uniqNo := g.freshLabel("rcopUniqNo")
		uniqEnd := g.freshLabel("rcopUniqEnd")
		g.pop() // x0 = ptr
		g.emit("cbz x0, %s", uniqNo)
		g.emit("cmp x0, #0x10000")
		g.emit("b.lo %s", uniqNo)
		g.emit("ldur w1, [x0, #-8]")
		g.emit("tbnz w1, #31, %s", uniqNo) // static sentinel
		g.emit("cmp w1, #1")
		g.emit("b.ne %s", uniqNo)
		g.emit("mov w0, #1")
		g.emit("b %s", uniqEnd)
		g.label(uniqNo)
		g.emit("mov w0, #0")
		g.label(uniqEnd)
		g.push()
		return nil
	case ir.OpCallDirect:
		// AAPCS64: load args 0..n-1 from the operand stack into
		// x0..x{n-1} (rightmost-on-top, so we pop in reverse
		// order), then `bl target`. Result lands in x0; push it.
		// Rewrite a small set of names where the stdlib's
		// callable name differs from the emitted symbol (e.g.
		// `__memcpy` → `__fern_memcpy`, `map_new` →
		// `map_new_impl`).
		target := op.Str
		// Cheap f64 math intrinsics lower to a single FP instruction —
		// no libm, no runtime helper. The f64 argument is on the operand
		// stack as its bit pattern (same convention as OpFNeg).
		if inst, ok := f64UnaryIntrinsic[target]; ok {
			g.pop()
			g.emit("fmov d0, x0")
			g.emit("%s d0, d0", inst)
			g.emit("fmov x0, d0")
			g.push()
			return nil
		}
		// f64 transcendentals — arm64 has no hardware sin/cos/exp/log,
		// so these call into polynomial-approximation runtime helpers
		// (emitFloatTranscendentalsRuntime). The arg(s) ride the
		// operand stack as bit patterns; move into d0 (and d1 for
		// pow) per AAPCS64, call, read the result out of d0.
		switch target {
		case "__sin_f64", "__cos_f64", "__exp_f64", "__log_f64":
			g.usesFloatTranscendentals = true
			g.pop()
			g.emit("fmov d0, x0")
			g.emit("bl __fern_%s", strings.TrimPrefix(target, "__"))
			g.emit("fmov x0, d0")
			g.push()
			return nil
		case "__pow_f64":
			g.usesFloatTranscendentals = true
			g.pop() // y (top)
			g.emit("fmov d1, x0")
			g.pop() // x
			g.emit("fmov d0, x0")
			g.emit("bl __fern_pow_f64")
			g.emit("fmov x0, d0")
			g.push()
			return nil
		}
		switch target {
		case "__c_call0":
			g.usesCCall[0] = true
		case "__c_call1":
			g.usesCCall[1] = true
		case "__c_call2":
			g.usesCCall[2] = true
		case "__c_call3":
			g.usesCCall[3] = true
		case "__c_call4":
			g.usesCCall[4] = true
		case "__c_call0_f32":
			g.usesCCallF32[0] = true
		case "__c_call1_f32":
			g.usesCCallF32[1] = true
		case "__c_call2_f32":
			g.usesCCallF32[2] = true
		case "__c_call3_f32":
			g.usesCCallF32[3] = true
		case "__c_call4_f32":
			g.usesCCallF32[4] = true
		case "__c_call0_f64":
			g.usesCCallF64[0] = true
		case "__c_call1_f64":
			g.usesCCallF64[1] = true
		case "__c_call2_f64":
			g.usesCCallF64[2] = true
		case "__c_call3_f64":
			g.usesCCallF64[3] = true
		case "__c_call4_f64":
			g.usesCCallF64[4] = true
		case "__memcpy":
			target = "__fern_memcpy"
			g.usesMemcpy = true
		case "__fern_rc_inc":
			g.usesRcInc = true
		case "__fern_rc_dec":
			g.usesRcDec = true
		case "__fern_str_inc":
			// Two-word string retain (arm64 + wasm). Tail-calls __fern_rc_inc
			// on the heap path so we need both.
			g.usesStrInc = true
			g.usesRcInc = true
		case "__fern_str_dec":
			// Two-word string reclaim (arm64 + wasm). On rc==1 tail-calls
			// __fern_box_free + needs __fern_rc_dec for the rc!=1 path.
			// __fern_box_free internally calls __fern_free.
			g.usesStrDec = true
			g.usesBoxFree = true
			g.usesFree = true
			g.usesRcDec = true
		case "__fern_cell_free":
			// 16-byte boxed-cell free (paired with the wasm-style boxed-
			// string map column walks). Tail-calls __fern_free.
			g.usesCellFree = true
			g.usesFree = true
		case "__fern_rc_underflow_count":
			g.usesRcUnderflowCount = true
		case "__fern_heap_bump_bytes":
			g.usesHeapBumpBytes = true
			g.usesAlloc = true // reads __fern_heap_ptr / __fern_heap_base
		case "__heap_mark", "__heap_release_to":
			// The IR carries the SOURCE builtin name (see internal/ir's
			// lowering: void-ness of release_to is resolved through the
			// checker's FuncSigs, which is keyed by that name), so map it to
			// the runtime symbol here.
			target = "__fern_" + target[2:]
			g.usesHeapMark = true
			g.usesAlloc = true // rewinds __fern_heap_ptr; shadows the freelist heads
		case "__fern_arr_push_grow":
			g.usesArrPushGrow = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "__fern_arr_push_grow_ptr":
			g.usesArrPushGrowPtr = true
			g.usesAlloc = true
			g.usesMemcpy = true
			g.usesRcInc = true
		case "__fern_arr_push_grow_str":
			g.usesArrPushGrowStr = true
			g.usesAlloc = true
			g.usesMemcpy = true
			g.usesStrInc = true
		case "__fern_arr_push_grow_move_ptr":
			g.usesArrPushGrowMovePtr = true
			g.usesAlloc = true
			g.usesMemcpy = true
			g.usesRcInc = true
		case "__fern_arr_push_grow_move_str":
			g.usesArrPushGrowMoveStr = true
			g.usesAlloc = true
			g.usesMemcpy = true
			g.usesStrInc = true
		case "__fern_arr_cow_inplace":
			g.usesArrCowInPlace = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "__fern_arr_cow_inplace_ptr":
			g.usesArrCowInPlacePtr = true
			g.usesAlloc = true
			g.usesMemcpy = true
			g.usesRcInc = true
		case "__fern_drop_arr_ptr":
			g.usesDropArrPtr = true
			g.usesRcDec = true
			if ast.RcFreeEnabled {
				g.usesFree = true
				g.usesAlloc = true
			}
		case "__fern_drop_arr_str":
			// `string[]` drop loop (Slice 4 two-word ABI). Walks
			// elements via __fern_str_dec (which on rc==1 frees the
			// element box via __fern_box_free + needs __fern_rc_dec
			// for the rc!=1 path), then frees the array buffer
			// via __fern_free.
			g.usesDropArrStr = true
			g.usesStrDec = true
			g.usesBoxFree = true
			g.usesFree = true
			g.usesRcDec = true
			if ast.RcFreeEnabled {
				g.usesAlloc = true
			}
		case "__fern_rc_is_unique":
			g.usesRcIsUnique = true
		case "__fern_strcat":
			g.usesStrcat = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "__alloc":
			target = "__fern_alloc"
			g.usesAlloc = true
		case "__free":
			target = "__fern_free"
			g.usesFree = true
			g.usesAlloc = true
		case "__alloc_reuse":
			target = "__fern_alloc_reuse"
			g.usesAllocReuse = true
			g.usesFree = true
			g.usesAlloc = true
		case "__fern_arr_dec":
			g.usesArrDec = true
			g.usesFree = true
			g.usesAlloc = true
		case "__fern_map_drop":
			g.usesMapDrop = true
			if ast.RcFreeEnabled {
				g.usesFree = true
				g.usesAlloc = true
			}
		case "__fern_box_free":
			g.usesBoxFree = true
			g.usesFree = true
			g.usesAlloc = true
		case "__fern_closure_drop":
			g.usesClosureDrop = true
			g.usesBoxFree = true // tail-called on rc==1
			g.usesFree = true
			g.usesAlloc = true
			g.usesRcDec = true // tail-called otherwise
		case "__slice_make":
			target = "__fern_slice_make"
			g.usesSliceMake = true
			g.usesAlloc = true
		case "__slice_range":
			target = "__fern_slice_range"
			g.usesSliceRange = true
		case "__store_i32", "__load_i32", "__store_ptr", "__load_ptr", "__ptr_width":
			g.usesRawIntPokes = true
		case "__memset":
			g.usesMemset = true
		case "__alloc_u8":
			g.usesAllocU8 = true
			g.usesAlloc = true
		case "string_from_bytes_unchecked":
			g.usesStringFromBytes = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "tcp_listen":
			target = "__fern_tcp_listen"
			g.usesTcp = true
			g.usesAlloc = true
		case "tcp_accept":
			target = "__fern_tcp_accept"
			g.usesTcp = true
		case "tcp_recv":
			target = "__fern_tcp_recv"
			g.usesTcp = true
			g.usesAlloc = true
		case "tcp_send":
			target = "__fern_tcp_send"
			g.usesTcp = true
		case "tcp_close":
			target = "__fern_tcp_close"
			g.usesTcp = true
		case "tcp_pollable":
			target = "__fern_tcp_pollable"
			g.usesTcp = true
		case "wasm_pollable_drop":
			target = "__fern_wasm_pollable_drop"
			g.usesWasmPollableDrop = true
		case "wasm_block":
			target = "__fern_wasm_block"
			g.usesWasmBlock = true
		case "wasm_timer_pollable":
			target = "__fern_wasm_timer_pollable"
			g.usesWasmTimerPollable = true
		case "wasm_poll":
			target = "__fern_wasm_poll"
			g.usesWasmPoll = true
		case "tcp_connect":
			target = "__fern_tcp_connect"
			g.usesTcp = true
			// usesTcp always emits __fern_tcp_recv (→ __fern_alloc_rc1),
			// so a connect-only program needs the alloc runtime too.
			g.usesAlloc = true
		case "poll":
			target = "__fern_poll"
			g.usesPoll = true
			g.usesAlloc = true
		case "timer_fd":
			target = "__fern_timer_fd"
			g.usesTimerFd = true
		// Map / MapIter — the lang Map runtime lives entirely
		// in the stdlib under `_impl`-suffixed names;
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
		// Struct/enum (keyKind-3) keys: the `_keyed` variants take the
		// key type's derived hash/eq as trailing fn-value args (#2671).
		case "__method_Map_has_keyed":
			target = "__map_has_keyed_impl"
		case "__method_Map_get_keyed":
			target = "__map_get_keyed_impl"
		case "__method_Map_get_or_keyed":
			target = "__map_get_or_keyed_impl"
		case "__method_Map_set_keyed":
			target = "__map_set_keyed_impl"
		case "__method_Map_delete_keyed":
			target = "__map_delete_keyed_impl"
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
		case "__str_idx", "__arr_idx", "__arr_idx_1", "__arr_idx_8", "__arr_idx_16",
			"__arr_idx_nc", "__arr_idx_1_nc", "__arr_idx_8_nc", "__arr_idx_16_nc",
			"__slice_idx", "__slice_idx_1", "__slice_idx_8", "__slice_idx_16":
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
			target = "__fern_env"
			g.usesEnv = true
			g.usesAlloc = true
			// __fern_env walks envp and `bl __fern_memcpy`s
			// each candidate value into a fresh lang string,
			// so we need the memcpy runtime too.
			g.usesMemcpy = true
		case "print":
			// print(s): write string + newline. The runtime
			// helper handles both writes.
			target = "__fern_puts"
			g.usesPuts = true
		case "write":
			// write(s): write string, no newline.
			target = "__fern_write"
			g.usesWrite = true
		case "putchar":
			// putchar(c): write the single byte.
			target = "__fern_putchar"
			g.usesPutchar = true
		case "eprint":
			// eprint(s): print to stderr (fd 2) + newline.
			target = "__fern_eprint"
			g.usesEprint = true
		case "exit":
			// exit(code): direct exit syscall. Never returns,
			// but codegen still emits the post-call stack-
			// push for stack-discipline; harmless because the
			// call never comes back.
			target = "__fern_exit"
			g.usesExit = true
		case "strbuf_reset":
			target = "__fern_strbuf_reset"
			g.usesStrBuf = true
		case "strbuf_append":
			target = "__fern_strbuf_append"
			g.usesStrBuf = true
			// strbuf_append grows the buffer by copying old
			// contents into a fresh allocation, so it pulls in
			// __fern_memcpy. Mirror of the x86 backend.
			g.usesMemcpy = true
		case "strbuf_take":
			target = "__fern_strbuf_take"
			g.usesStrBuf = true
			// strbuf_take allocates a new string box and memcpys
			// the accumulator bytes into it.
			g.usesAlloc = true
			g.usesMemcpy = true
		case "now_unix_ms":
			// now_unix_ms(): wall-clock ms since the Unix
			// epoch via clock_gettime(CLOCK_REALTIME, ...).
			// Returns i64 in x0; `instant_now()` splits this
			// into (sec, nsec) for `Instant`.
			target = "__fern_now_unix_ms"
			g.usesNowUnixMs = true
		case "monotonic_ns":
			// monotonic_ns(): monotonic nanoseconds via
			// clock_gettime(CLOCK_MONOTONIC, ...) (Linux) or the
			// CNTVCT_EL0 architectural counter (Darwin). Returns i64
			// in x0; backs benchmark/elapsed timing.
			target = "__fern_monotonic_ns"
			g.usesMonotonicNs = true
		case "now_ns":
			// now_ns(): wall-clock nanoseconds since the Unix epoch
			// via clock_gettime(CLOCK_REALTIME, ...) (Linux) or
			// gettimeofday scaled to ns (Darwin). Returns i64 in x0.
			target = "__fern_now_ns"
			g.usesNowNs = true
		case "sleep_ms":
			// sleep_ms(ms): best-effort sleep via nanosleep (Linux)
			// or select (Darwin). Void.
			target = "__fern_sleep_ms"
			g.usesSleepMs = true
		case "proc_fork":
			// proc_fork(): fork the process via clone(SIGCHLD,0,0,0,0)
			// (Linux — arm64 has no bare fork syscall) or fork (Darwin
			// BSD 2, x1-flag normalised). 0 in child, pid in parent,
			// -errno on failure (docs/CRASH-ONLY-SERVE.md D2').
			target = "__fern_proc_fork"
			g.usesProcFork = true
		case "proc_waitpid":
			// proc_waitpid(pid): blocking wait4 + status decode —
			// exit code 0..255, or 128+signal for a signal death,
			// or -errno.
			target = "__fern_proc_waitpid"
			g.usesProcWaitpid = true
		case "args":
			// args(): returns a length-prefixed string[] of
			// argv. Caches the result so repeat calls are O(1).
			target = "__fern_args"
			g.usesArgs = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "read_line":
			// read_line(): byte-by-byte stdin read into a 4 KiB
			// .bss buffer; returns Option[string].
			target = "__fern_read_line"
			g.usesReadLine = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "__method_Reader_read_line":
			// `r.read_line()` — loads fd from the receiver's
			// first field and reads from THAT fd byte-by-byte
			// into the shared 4-KiB scratch buffer. Returns
			// Option[string]: Some(line) when at least one
			// byte was read, None on first-byte EOF.
			target = "__fern_reader_read_line"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesMemcpy = true
		case "__method_Reader_read_chunk":
			// `r.read_chunk(n)` — single read of up to n bytes
			// from receiver.fd. Returns Option[string]: None
			// on EOF (read returned 0).
			target = "__fern_reader_read_chunk"
			g.usesReaderWriter = true
			g.usesAlloc = true
		case "__method_Reader_close":
			target = "__fern_close_fd_box"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesIoError = true
		case "__method_Writer_write":
			target = "__fern_writer_write"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesIoError = true
		case "__method_Writer_close":
			target = "__fern_close_fd_box"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesIoError = true
		case "open_reader":
			target = "__fern_open_reader"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesIoError = true
		case "open_writer":
			target = "__fern_open_writer"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesIoError = true
		case "open_appender":
			target = "__fern_open_appender"
			g.usesReaderWriter = true
			g.usesAlloc = true
			g.usesIoError = true
		case "stdin":
			// stdin() / stdout() / stderr() return real Reader /
			// Writer struct pointers now (fd at +0). Wraps the
			// standard fds (0 / 1 / 2) in the same alloc shape
			// open_reader / open_writer produce.
			target = "__fern_stdin"
			g.usesReaderWriter = true
			g.usesAlloc = true
		case "stdout":
			target = "__fern_stdout"
			g.usesReaderWriter = true
			g.usesAlloc = true
		case "stderr":
			target = "__fern_stderr"
			g.usesReaderWriter = true
			g.usesAlloc = true
		case "read_file":
			// read_file(path): Result[string, IoError] —
			// openat + fstat + read-loop + close on Linux.
			target = "__fern_read_file"
			g.usesReadFile = true
			g.usesAlloc = true
			g.usesIoError = true
		case "write_file":
			// write_file(path, content): Option[IoError] —
			// openat(O_WRONLY|O_CREAT|O_TRUNC) + write-loop +
			// close on Linux.
			target = "__fern_write_file"
			g.usesWriteFile = true
			g.usesAlloc = true
			g.usesIoError = true
		case "remove_file":
			// remove_file(path): Option[IoError] — unlinkat.
			target = "__fern_remove_file"
			g.usesRemoveFile = true
		case "temp_dir":
			// temp_dir(prefix): Result[string, IoError] —
			// mkdirat("/tmp/<prefix>-<monotonic_ns>").
			target = "__fern_temp_dir"
			g.usesTempDir = true
		case "read_dir":
			// read_dir(path): Result[string[], IoError] —
			// openat(O_DIRECTORY) + getdents64 drain.
			target = "__fern_read_dir"
			g.usesReadDir = true
		case "stat":
			// stat(path): Result[FileStat, IoError] — fstatat
			// into a FileStat box.
			target = "__fern_stat"
			g.usesStat = true
		case "remove_dir_all":
			// remove_dir_all(path): Option[IoError] — recursive
			// rm -rf (openat + getdents64 + unlinkat, self-
			// recursion per entry).
			target = "__fern_remove_dir_all"
			g.usesRemoveDirAll = true
		case "random_bytes":
			// random_bytes(n): allocates an n-byte string and
			// fills it with kernel CSPRNG output. Linux uses
			// getrandom (syscall 278); Darwin uses chunked
			// getentropy (syscall 500, max 256 bytes/call).
			target = "__fern_random_bytes"
			g.usesRandomBytes = true
			g.usesAlloc = true
		case "random_i32":
			// random_i32(): a single CSPRNG i32. Linux reads 4
			// bytes via getrandom (syscall 278); Darwin via
			// getentropy (syscall 500).
			target = "__fern_random_i32"
			g.usesRandomI32 = true
		case "__method_string_as_bytes":
			// s.as_bytes(): non-copying (data, len) → slice<u8>
			// header. Under the two-word ABI the receiver already
			// arrives as (data, len); the helper just builds a
			// slice header aliasing those bytes.
			target = "__method_string_as_bytes"
			g.usesAsBytes = true
			g.usesSliceMake = true
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
			if sw, ok := twoWordStrHelperArgSlots[target]; ok {
				// Built-in two-word string runtime helpers — emitted
				// from many IR sites with a bare I32:1 arg count and no
				// ArgTypes, so callArgTypes can't see that their single
				// logical string argument occupies two operand-stack
				// slots (data, len). Without this the call pops just the
				// top word (the length) into x0 and the helper reads it
				// as the data pointer — SIGSEGV on literal strings (e.g.
				// __fern_str_inc retaining an aliased generic id() param).
				slotCount = sw
			} else if argTypes := callArgTypes(g, op, argc); argTypes != nil {
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
		// __c_call*_f32/_f64 return their result in d0 (C ABI); move it into
		// x0 so it lands on the operand stack in Fern's FP convention. (The
		// x86-64 mirror moves xmm0→rax.)
		if w := ccallFloatRetWidth(op.Str); w == 64 {
			g.emit("fmov x0, d0")
		} else if w == 32 {
			g.emit("fmov w0, s0")
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
		g.push() // push payload

	default:
		return fmt.Errorf("arm64: unsupported IR op %s", op.Kind)
	}
	return nil
}

// closureCellSym names an OpConstFunc static closure-pair cell. On
// darwin the label is assembler-local ("L" prefix) so it does NOT start
// a linker atom — keeping the anonymous 8-byte rc header at [cell-8]
// glued to the cell under ld64/ld-prime reordering (the same reason the
// string literals use .LStr_* labels in __TEXT,__const). ELF keeps the
// plain named label.
func (g *generator) closureCellSym(name string) string {
	if g.darwin {
		return "L__closure_cell_" + name
	}
	return "__closure_cell_" + name
}
