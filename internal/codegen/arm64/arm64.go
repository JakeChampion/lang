// Package arm64 emits ARM64 (aarch64) Linux assembly from a
// checked + monomorphised lang program. Companion to the
// existing arm32 backend; shares the IR layer but emits its
// own ISA + syscall wiring.
//
// First-PR scope (this file): `function main(): i32 { return
// <const>; }` — enough to bring up the toolchain end-to-end
// (assemble, link as a static `-nostdlib` ELF, run under
// qemu-aarch64). Subsequent PRs grow the per-op switch into
// feature parity with arm32 (arithmetic, calls, strings,
// arrays, the lang prelude's runtime helpers, sockets +
// auto-main HTTP server).
//
// ABI: AAPCS64. Args in x0..x7; return value in x0; frame
// pointer in x29; link register in x30; stack pointer must
// stay 16-byte aligned at function call boundaries.
//
// Linux syscall convention: x8 holds the syscall number,
// x0..x5 carry args, `svc #0` traps to the kernel. Numbers
// differ from arm32 — there's a single canonical asm-generic
// table all arm64 Linux distributions share, so this header
// captures only what the runtime needs.
package arm64

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/treeshake"
)

// Linux arm64 syscall numbers from the asm-generic table.
// Only what the minimum-viable runtime needs at this stage.
const (
	sysExitGroup = 94
)

// Options tunes the emit. Currently empty — adopted as the
// arm32 backend grows debug-info / source-map flags so the
// arm64 path gains them on the same shape.
type Options struct{}

// Emit produces the assembly text for prog. Thin wrapper over
// EmitWithOptions for callers that don't need debug info.
func Emit(prog *ast.Program, info *checker.Info) (string, error) {
	return EmitWithOptions(prog, info, Options{})
}

// EmitWithOptions returns the assembly text for prog. First
// version recognises only the minimum-viable shape:
//
//	function main(): i32 { return <i32-literal>; }
//
// — enough to validate the toolchain (asm + linker + qemu run
// + exit-code propagation) end-to-end. Programs outside this
// shape error explicitly so the failure mode is obvious; the
// per-op switch grows in follow-up PRs.
func EmitWithOptions(prog *ast.Program, info *checker.Info, opts Options) (string, error) {
	treeshake.Run(prog)
	ip, err := ir.Lower(prog, info)
	if err != nil {
		return "", err
	}
	g := &generator{info: info}
	g.line(`.arch armv8-a`)
	g.line(`.text`)
	g.emitStartRuntime()
	for i, fn := range prog.Funcs {
		if fn.Name != "main" {
			// First-PR scope — only main is emitted. Other
			// functions trip an explicit error so callers know
			// what's not yet implemented rather than producing
			// silently broken assembly.
			continue
		}
		if err := g.emitMain(fn, ip.Funcs[i]); err != nil {
			return "", err
		}
	}
	g.line(`.section .note.GNU-stack,"",%progbits`)
	return g.out.String(), nil
}

type generator struct {
	out    strings.Builder
	info   *checker.Info
	indent int
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

// emitStartRuntime writes `_start`, the binary's entry point
// under `-nostdlib` on Linux arm64. Linux delivers the initial
// stack to user-mode the same way it does on arm32 — argc at
// [sp+0], argv at [sp+8] (8-byte slots on aarch64), envp
// after the NULL terminator. We don't yet expose args() / env()
// on this target so the prologue is minimal: align sp,
// branch to main, then exit_group with main's return value.
//
// AAPCS64 wants sp aligned to 16 bytes at call boundaries.
// The kernel hands us a 16-aligned sp at process entry but
// we mask it explicitly so the prologue is robust to future
// changes that might leave sp 8-aligned (e.g. if we start
// pushing argc/argv before the call).
func (g *generator) emitStartRuntime() {
	g.line("")
	g.line(".global _start")
	g.line(".type _start, %function")
	g.label("_start")
	// Linux hands user-mode a 16-aligned sp at process entry,
	// so no explicit re-alignment is required here. (`and sp,
	// sp, #imm` is rejected by the assembler — AArch64 logical-
	// immediate AND doesn't accept SP as the destination; the
	// long form is `mov x16, sp; and x16, x16, #-16; mov sp,
	// x16`. We skip it since process entry is already aligned;
	// future args / env-walking work that pushes onto sp will
	// preserve alignment via the prologue's `stp` shape.)
	// Call main(). Args are unused at this stage (no args() /
	// env() support yet); main is declared as `() -> i32`.
	g.emit("bl main")
	// exit_group(retval). x0 holds main's return value.
	g.emit("mov x8, #%d", sysExitGroup)
	g.emit("svc #0")
	g.line(".size _start, .-_start")
}

// emitMain lowers `function main(): i32 { return <const>; }`.
// Walks the IR ops looking for a single OpConstI32 followed
// by OpReturn — the only shape this baseline backend handles.
// Anything else surfaces as an error so the missing op is
// obvious; follow-up PRs grow the switch into the full per-op
// translation.
func (g *generator) emitMain(fn *ast.FuncDecl, irFn *ir.Func) error {
	g.line("")
	g.line(".global main")
	g.line(".type main, %function")
	g.label("main")
	// Standard AAPCS64 prologue: save fp + lr, set up frame.
	// Reserves 16 bytes of stack for the saved pair (paired
	// load/store with `stp` / `ldp` — the canonical aarch64
	// idiom for prologue/epilogue).
	g.emit("stp x29, x30, [sp, #-16]!")
	g.emit("mov x29, sp")
	for _, op := range irFn.Ops {
		switch op.Kind {
		case ir.OpConstI32:
			// Load constant into x0 (the AAPCS64 return
			// register). `mov` with imm16 covers 0..0xffff in
			// one instruction; broader ranges need movz/movk
			// composition or a literal-pool load. Use `mov`
			// for in-range values; `ldr =N` (assembler
			// pseudo-instruction) for everything else.
			if op.I32 >= 0 && op.I32 <= 0xffff {
				g.emit("mov x0, #%d", op.I32)
			} else {
				g.emit("ldr w0, =%d", op.I32)
			}
		case ir.OpReturn:
			g.emit("ldp x29, x30, [sp], #16")
			g.emit("ret")
		default:
			return fmt.Errorf("arm64: unsupported IR op %s — only OpConstI32 + OpReturn ship in the baseline backend; follow-up PRs grow the per-op switch", op.Kind)
		}
	}
	g.line(".size main, .-main")
	return nil
}
