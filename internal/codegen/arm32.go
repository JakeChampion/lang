// Package codegen emits ARM 32-bit assembly (GNU syntax, suitable for
// `arm-linux-gnueabihf-as` / gcc) from a checked Program. Production
// emit goes through the IR — Lower, Inline, FuseTee, TailCallOptimize,
// FlattenBranches, and the OptimizeCleanup driver (PropagateCopies,
// ConstPropagate, Fold, ReduceStrength) all run before EmitFromIR
// walks the IR ops to assembly. The pipeline is shared with the WASM
// backend so a new language feature lands once at the IR layer and
// both backends pick it up.
//
// Calling convention: standard AAPCS.
//
//   * Every expression's value lives in r0; the IR's operand stack
//     maps to the runtime stack via push / pop {r0}.
//   * Binary operators pop the right operand into r0 and the left
//     into r1, do the work, and push the result.
//   * Args 0..3 ride in r0..r3; extras come from the caller's stack
//     frame at fp+8, fp+12, …
//   * Leaf functions (no calls in the body) pin their parameters to
//     callee-saved r4..r{4+P-1} instead of spilling.
//   * Locals, spilled parameters, and synthetic scratch slots
//     (ArrayLit / StructLit / Switch / inlined-callee bindings) all
//     live at negative offsets from fp. Heap-backed values (arrays,
//     strings, structs) come from `__lang_alloc`, a tiny libc-malloc
//     wrapper this emitter generates on demand.
//
// The output is plain `.s` text — feed it to `gcc` (or `as` + `ld`)
// to produce a runnable executable, and run it with `qemu-arm` if
// you're not on an ARM host.
package codegen

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// regArgs is the number of arguments that AAPCS passes in registers
// (r0..r3). Anything beyond that goes through the caller's stack frame.
const regArgs = 4

// Options tunes Emit. The zero value is fine for production codegen;
// pass SourceFile to opt into DWARF line-info via .file/.loc directives
// (use it together with `gcc -g` at the link step).
type Options struct {
	SourceFile string
}

// Emit returns the assembly text for prog. It's a thin wrapper over
// EmitWithOptions for callers that don't need debug info.
func Emit(prog *ast.Program, info *checker.Info) (string, error) {
	return EmitWithOptions(prog, info, Options{})
}

// EmitWithOptions returns the assembly text for prog. When
// opts.SourceFile is set, the output starts with `.file 1 "<name>"`
// and every statement is preceded by a `.loc 1 <line> <col>` directive
// so `gcc -g` produces a usable DWARF line-number table.
//
// Production codegen routes through the IR — closure conversion +
// stack-machine lowering + tail-call optimisation, all in one
// shared pipeline with the WASM backend.
func EmitWithOptions(prog *ast.Program, info *checker.Info, opts Options) (string, error) {
	return EmitFromIR(prog, info, opts)
}

type generator struct {
	out         strings.Builder
	info        *checker.Info
	labelN      int
	stringLabel map[string]string // value -> label
	stringOrder []string          // insertion order so output is deterministic
	usesStrcat  bool              // true if the program needs the strcat helper
	usesAlloc   bool              // true if the program needs the alloc helper (any heap-backed array / struct / closure)
	srcFile     string            // non-empty enables DWARF .file/.loc directives
}

// emitAllocRuntime emits a tiny `__lang_alloc(size)` helper that
// forwards to libc `malloc`. The wrapper exists so arrays / structs
// / closures all share one named entry point — both the AST-walking
// emitter and the upcoming IR-driven backend bottom out here, which
// matches the WASM backend's `$__lang_alloc` shape and gives the
// language a single chokepoint if it ever grows a real GC.
//
// The helper preserves the standard prologue/epilogue for stack
// alignment but doesn't otherwise touch r0, so the result stays in
// place across the libc call.
func (g *generator) emitAllocRuntime() {
	g.line("")
	g.line(".global __lang_alloc")
	g.line(".type __lang_alloc, %function")
	g.label("__lang_alloc")
	g.emit("push {lr}")
	g.emit("sub sp, sp, #4") // 8-byte alignment
	g.emit("bl malloc")
	g.emit("add sp, sp, #4")
	g.emit("pop {lr}")
	g.emit("bx lr")
	g.line(".size __lang_alloc, .-__lang_alloc")
}

// emitStrcatRuntime emits a leaf-style helper that allocates a fresh
// length-prefixed buffer holding the concatenation of two strings:
//
//	r0 = a (data ptr), r1 = b (data ptr)   →   r0 = ptr to combined data
//
// Layout matches array literals — a 4-byte little-endian length sits
// at `result - 4`, with a trailing NUL byte kept past the data so
// libc strlen / strcmp keep working on the same pointer. The buffer
// is never freed; strings are immutable but not GC'd.
//
// Operands arrive as data pointers (post-prefix), but the lengths
// live at `ptr - 4`, so the helper grabs them with a single load
// each rather than walking the buffer with strlen.
func (g *generator) emitStrcatRuntime() {
	g.line("")
	g.line(".global __lang_strcat")
	g.line(".type __lang_strcat, %function")
	g.label("__lang_strcat")
	g.emit("push {r4, r5, r6, r7, lr}")
	g.emit("sub sp, sp, #4")    // 8-byte alignment
	g.emit("mov r4, r0")        // r4 = a (data ptr)
	g.emit("mov r5, r1")        // r5 = b (data ptr)
	g.emit("ldr r6, [r0, #-4]") // r6 = la (length prefix)
	g.emit("ldr r7, [r1, #-4]") // r7 = lb
	g.emit("add r0, r6, r7")
	g.emit("add r0, r0, #5") // 4 (prefix) + la + lb + 1 (trailing NUL)
	g.emit("bl __lang_alloc")
	g.emit("add r0, r0, #4") // r0 = data pointer (skip prefix)
	g.emit("add r1, r6, r7")
	g.emit("str r1, [r0, #-4]") // store length prefix
	g.emit("mov r1, r4")        // src = a
	g.emit("mov r2, r6")        // n = la
	g.emit("mov r4, r0")        // r4 = result data ptr
	g.emit("bl memcpy")
	g.emit("add r0, r4, r6") // dst = result + la
	g.emit("mov r1, r5")     // src = b
	g.emit("add r2, r7, #1") // n = lb + 1 (include NUL)
	g.emit("bl memcpy")
	g.emit("mov r0, r4")
	g.emit("add sp, sp, #4")
	g.emit("pop {r4, r5, r6, r7, lr}")
	g.emit("bx lr")
	g.line(".size __lang_strcat, .-__lang_strcat")
}

// internString returns a unique .rodata label for s, allocating a new one
// the first time we see this exact string and reusing it on repeats.
func (g *generator) internString(s string) string {
	if lbl, ok := g.stringLabel[s]; ok {
		return lbl
	}
	lbl := fmt.Sprintf(".LStr_%d", len(g.stringOrder))
	g.stringLabel[s] = lbl
	g.stringOrder = append(g.stringOrder, s)
	return lbl
}

// escapeForGAS wraps s in double quotes and emits each byte either as
// itself (printable ASCII apart from " and \), as a recognised C-style
// escape, or as a three-digit octal escape. The result is suitable as
// the operand of `.asciz`.
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
		case c >= 0x20 && c <= 0x7e:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\%03o`, c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ---------- driver helpers ----------

func (g *generator) line(s string) { g.out.WriteString(s); g.out.WriteByte('\n') }
func (g *generator) emit(format string, args ...any) {
	fmt.Fprintf(&g.out, "\t"+format+"\n", args...)
}
func (g *generator) label(name string) {
	g.out.WriteString(name)
	g.out.WriteString(":\n")
}
func (g *generator) freshLabel(stem string) string {
	g.labelN++
	return fmt.Sprintf(".L%s_%d", stem, g.labelN)
}

