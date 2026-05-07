// Per-op IR → ARM32 translation. Each op consumes / produces values
// on the runtime stack; the generator pushes after producing and
// pops before consuming, so adjacent ops compose.
//
// The op set mirrors WebAssembly's, which keeps the translation
// straightforward: i32 arithmetic / comparison map to single ARM
// instructions; loads / stores work directly on byte addresses;
// structured control flow maps to label-and-branch sequences.
//
// Floats are not supported on this backend (and rejected at lower
// time on the AST emitter); OpFAdd / OpFLoad / etc. bubble up as
// "not yet supported" errors so a future VFP-aware emitter can
// fill them in without disturbing the integer paths.

package codegen

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// pos0 is the zero-valued position. We use it as the "no source line
// emitted yet" sentinel when threading per-op positions into DWARF
// .loc directives.
var pos0 = ast.Position{}

// emitOpsFromIR walks ops in order, generating ARM32 instructions
// for each. Operand values live on the runtime stack between ops;
// each `OpStoreLocal` / branch / call pops as needed.
//
// Control-flow ops manage a per-function scope stack: OpBlock /
// OpLoop / OpIf push a new scope with fresh labels, OpEnd pops the
// innermost, and OpBr / OpBrIf resolve their immediate as the index
// from the top of the scope stack.
//
// emitOpsFromIR is intentionally branch-heavy: it's the dispatch
// table that turns the IR's ~50 ops into assembly. Each case stays
// small and direct.
func (g *generator) emitOpsFromIR(
	irFn *ir.Func,
	slotOffset []int,
	paramReg map[int]int,
	epilogue string,
) error {
	scope := []irScope{}
	lastPos := pos0
	for i := 0; i < len(irFn.Ops); i++ {
		op := irFn.Ops[i]
		// Emit a `.loc` directive whenever we cross a source-line
		// boundary so `gcc -g` produces a per-statement DWARF table.
		if g.srcFile != "" && op.Pos.Line != 0 && op.Pos != lastPos {
			g.emit(".loc 1 %d %d", op.Pos.Line, op.Pos.Col)
			lastPos = op.Pos
		}
		switch op.Kind {

		// -------- constants --------

		case ir.OpConstI32:
			g.emit("ldr r0, =%d", op.I32)
			g.emit("push {r0}")
		case ir.OpConstStr:
			lbl := g.internString(op.Str)
			g.emit("ldr r0, =%s", lbl)
			g.emit("push {r0}")
		case ir.OpConstFunc:
			// Function values are direct code addresses — ARM has
			// no funcref table abstraction. The assembler resolves
			// `=name` into a literal-pool entry holding the
			// symbol; `blx r12` after popping jumps directly to
			// the entry point.
			g.emit("ldr r0, =%s", op.Str)
			g.emit("push {r0}")

		// -------- locals --------

		case ir.OpLoadLocal:
			if reg, ok := paramReg[int(op.I32)]; ok {
				g.emit("mov r0, r%d", reg)
			} else {
				g.emit("ldr r0, [fp, #%d]", slotOffset[op.I32])
			}
			g.emit("push {r0}")
		case ir.OpStoreLocal:
			g.emit("pop {r0}")
			if reg, ok := paramReg[int(op.I32)]; ok {
				g.emit("mov r%d, r0", reg)
			} else {
				g.emit("str r0, [fp, #%d]", slotOffset[op.I32])
			}
		case ir.OpTeeLocal:
			// Pop the value, store it to the slot, push it back so
			// the operand stack still carries it. ARM has no fused
			// tee, so we issue the pop / str / push sequence — the
			// peephole pass folds adjacent push/pop pairs that
			// neighbour ops produce.
			g.emit("pop {r0}")
			if reg, ok := paramReg[int(op.I32)]; ok {
				g.emit("mov r%d, r0", reg)
			} else {
				g.emit("str r0, [fp, #%d]", slotOffset[op.I32])
			}
			g.emit("push {r0}")

		// -------- arithmetic / comparison (i32) --------

		case ir.OpAdd:
			g.binPop()
			g.emit("add r0, r1, r0")
			g.emit("push {r0}")
		case ir.OpSub:
			g.binPop()
			g.emit("sub r0, r1, r0")
			g.emit("push {r0}")
		case ir.OpMul:
			g.binPop()
			g.emit("mul r0, r1, r0")
			g.emit("push {r0}")
		case ir.OpDivS:
			// __aeabi_idiv(num, denom): r0 = num, r1 = denom.
			// Stack top is denom (rhs); next is numerator.
			g.binPop()
			g.emit("mov r2, r0") // r2 = denom
			g.emit("mov r0, r1") // r0 = num
			g.emit("mov r1, r2")
			g.emit("bl __aeabi_idiv")
			g.emit("push {r0}")
		case ir.OpRemS:
			g.binPop()
			g.emit("mov r2, r0")
			g.emit("mov r0, r1")
			g.emit("mov r1, r2")
			g.emit("bl __aeabi_idivmod")
			g.emit("mov r0, r1")
			g.emit("push {r0}")
		case ir.OpAnd:
			g.binPop()
			g.emit("and r0, r1, r0")
			g.emit("push {r0}")
		case ir.OpOr:
			g.binPop()
			g.emit("orr r0, r1, r0")
			g.emit("push {r0}")
		case ir.OpXor:
			g.binPop()
			g.emit("eor r0, r1, r0")
			g.emit("push {r0}")
		case ir.OpShl:
			g.binPop()
			g.emit("lsl r0, r1, r0")
			g.emit("push {r0}")
		case ir.OpShrS:
			g.binPop()
			g.emit("asr r0, r1, r0")
			g.emit("push {r0}")
		case ir.OpNot:
			g.emit("pop {r0}")
			g.emit("cmp r0, #0")
			g.emit("moveq r0, #1")
			g.emit("movne r0, #0")
			g.emit("push {r0}")

		case ir.OpEq:
			g.cmpPop("eq", "ne")
		case ir.OpNe:
			g.cmpPop("ne", "eq")
		case ir.OpLtS:
			g.cmpPop("lt", "ge")
		case ir.OpLeS:
			g.cmpPop("le", "gt")
		case ir.OpGtS:
			g.cmpPop("gt", "le")
		case ir.OpGeS:
			g.cmpPop("ge", "lt")

		// -------- floats (not yet implemented) --------

		case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv,
			ir.OpFNeg, ir.OpFEq, ir.OpFNe, ir.OpFLt, ir.OpFLe,
			ir.OpFGt, ir.OpFGe, ir.OpFLoad, ir.OpFStore, ir.OpConstF32:
			return fmt.Errorf("codegen: float ops are not yet supported by the arm32 backend (use the wasm backend)")

		// -------- memory --------

		case ir.OpLoad:
			g.emit("pop {r0}")
			g.emit("ldr r0, [r0]")
			g.emit("push {r0}")
		case ir.OpLoadByte:
			g.emit("pop {r0}")
			g.emit("ldrb r0, [r0]")
			g.emit("push {r0}")
		case ir.OpStore:
			// Stack: [addr, value], top = value.
			g.emit("pop {r0}") // value
			g.emit("pop {r1}") // addr
			g.emit("str r0, [r1]")
		case ir.OpStoreI8:
			g.emit("pop {r0}")
			g.emit("pop {r1}")
			g.emit("strb r0, [r1]")

		case ir.OpAlloc:
			g.usesAlloc = true
			g.emit("pop {r0}")
			g.emit("bl __lang_alloc")
			g.emit("push {r0}")

		// -------- string runtime --------

		case ir.OpStrConcat:
			g.usesStrcat = true
			g.emit("pop {r1}") // b (data ptr)
			g.emit("pop {r0}") // a
			g.emit("bl __lang_strcat")
			g.emit("push {r0}")
		case ir.OpStrEq:
			// Strings still carry a trailing NUL alongside the length
			// prefix, so libc strcmp does the job — equality returns
			// 0, which we normalise to 1.
			g.emit("pop {r1}")
			g.emit("pop {r0}")
			g.emit("bl strcmp")
			g.emit("cmp r0, #0")
			g.emit("moveq r0, #1")
			g.emit("movne r0, #0")
			g.emit("push {r0}")

		// -------- structured control flow --------

		case ir.OpBlock:
			endL := g.freshLabel("blkEnd")
			scope = append(scope, irScope{kind: ir.OpBlock, brTarget: endL, endLabel: endL})
		case ir.OpLoop:
			startL := g.freshLabel("loopTop")
			endL := g.freshLabel("loopEnd")
			g.label(startL)
			scope = append(scope, irScope{kind: ir.OpLoop, brTarget: startL, endLabel: endL})
		case ir.OpIf:
			g.emit("pop {r0}")
			g.emit("cmp r0, #0")
			elseL := g.freshLabel("ifElse")
			endL := g.freshLabel("ifEnd")
			g.emit("beq %s", elseL)
			scope = append(scope, irScope{kind: ir.OpIf, brTarget: endL, endLabel: endL, elseLabel: elseL})
		case ir.OpElse:
			top := &scope[len(scope)-1]
			top.hasElse = true
			g.emit("b %s", top.endLabel)
			g.label(top.elseLabel)
		case ir.OpEnd:
			top := scope[len(scope)-1]
			scope = scope[:len(scope)-1]
			if top.kind == ir.OpIf && !top.hasElse {
				// No OpElse appeared; the OpIf's beq was wired to
				// elseLabel, which we now define as the end so
				// "condition false" simply skips the if body.
				g.label(top.elseLabel)
			}
			g.label(top.endLabel)

		case ir.OpBr:
			target := scope[len(scope)-1-int(op.I32)].brTarget
			g.emit("b %s", target)
		case ir.OpBrIf:
			g.emit("pop {r0}")
			g.emit("cmp r0, #0")
			target := scope[len(scope)-1-int(op.I32)].brTarget
			g.emit("bne %s", target)

		// -------- calls --------

		case ir.OpCallDirect:
			if err := g.emitCallDirect(op); err != nil {
				return err
			}
		case ir.OpCallIndirect:
			if err := g.emitCallIndirect(op); err != nil {
				return err
			}
		case ir.OpMakeClosure:
			return fmt.Errorf("codegen: nested functions / closures are not yet supported on the arm32 backend (use the wasm backend)")

		case ir.OpDrop:
			g.emit("add sp, sp, #4")
		case ir.OpReturn:
			g.emit("pop {r0}")
			g.emit("b %s", epilogue)
		case ir.OpReturnVoid:
			g.emit("b %s", epilogue)

		default:
			return fmt.Errorf("codegen: unsupported IR op %s", op.Kind)
		}
	}
	return nil
}

// binPop pops the stack twice into r0 (rhs) and r1 (lhs) — the
// shape every binary integer op wants before it can pick between
// `add r0, r1, r0`, `sub r0, r1, r0`, etc.
func (g *generator) binPop() {
	g.emit("pop {r0}")
	g.emit("pop {r1}")
}

// cmpPop emits a `cmp r1, r0` followed by a paired conditional move
// to normalise the result to 0 / 1. The two condition codes are the
// "true" and "false" sides — e.g. for `==`, true=eq / false=ne.
func (g *generator) cmpPop(trueCC, falseCC string) {
	g.binPop()
	g.emit("cmp r1, r0")
	g.emit("mov%s r0, #1", trueCC)
	g.emit("mov%s r0, #0", falseCC)
	g.emit("push {r0}")
}

// emitCallDirect handles every `OpCallDirect` shape: user-defined
// functions, the print / putchar builtins (which lower to libc puts
// / putchar), and the `__str_idx` / `__arr_idx` bounds-check helpers
// the IR emits for indexing. Args were pushed left-to-right onto
// the operand stack, so we pop them in reverse into r0..r3, with
// extras spilled onto the call's outgoing stack frame.
func (g *generator) emitCallDirect(op ir.Op) error {
	target := op.Str
	switch target {
	case "print":
		// `print(s)` is `puts(s)`; puts already adds a newline.
		target = "puts"
	case "write":
		// `write(s)` writes the string to stdout without a
		// newline. The runtime shim turns the 1-arg lang call
		// into a libc `write(1, s, len)` syscall.
		target = "__lang_write"
		g.usesWrite = true
	case "eprint":
		// `eprint(s)` is the stderr counterpart to `print` —
		// string + newline, both via the libc `write` syscall.
		target = "__lang_eprint"
		g.usesEprint = true
	case "putchar":
		// putchar takes its arg in r0 like normal — no rewrite needed.
	case "args":
		// `args()` lowers to a runtime helper that materialises a
		// length-prefixed string[] from the argc/argv saved at main
		// entry. usesArgs pulls in the helper + the .bss globals
		// + the prologue insertion for `main`. usesAlloc piggy-
		// backs because the helper allocates the array and copies
		// each entry through __lang_alloc.
		target = "__lang_args"
		g.usesArgs = true
		g.usesAlloc = true
	case "read_line":
		// `read_line()` reads stdin one byte at a time into a
		// fixed-size .bss buffer (4 KiB), then copies the
		// accumulated bytes into a fresh length-prefixed
		// string on the heap.
		target = "__lang_read_line"
		g.usesReadLine = true
		g.usesAlloc = true
	case "env":
		// `env(name)` is a getenv shim: NULL becomes an empty
		// length-prefixed string, otherwise the C string gets
		// copied into a fresh heap-allocated lang string.
		target = "__lang_env"
		g.usesEnv = true
		g.usesAlloc = true
	case "exit":
		// libc `exit(int)` — never returns. The IR-driven
		// caller still emits the post-call `push {r0}` for
		// stack hygiene; that's harmless because exit doesn't
		// come back.
		target = "exit"
	case "read_file":
		// `read_file(path)` lowers to a runtime helper that
		// open(2)s the file, read(2)s it in chunks, and
		// returns a `Result[string, IoError]` heap object.
		target = "__lang_read_file"
		g.usesReadFile = true
		g.usesAlloc = true
	case "write_file":
		// `write_file(path, content)` truncates and writes via
		// libc open(2)+write(2)+close(2); returns
		// `Option[IoError]` (None on success).
		target = "__lang_write_file"
		g.usesWriteFile = true
		g.usesAlloc = true
	case "open_reader":
		target = "__lang_open_reader"
		g.usesStreamIO = true
		g.usesAlloc = true
	case "open_writer":
		target = "__lang_open_writer"
		g.usesStreamIO = true
		g.usesAlloc = true
	case "open_appender":
		target = "__lang_open_appender"
		g.usesStreamIO = true
		g.usesAlloc = true
	case "stdin":
		target = "__lang_stdin"
		g.usesStdStreams = true
		g.usesAlloc = true
	case "stdout":
		target = "__lang_stdout"
		g.usesStdStreams = true
		g.usesAlloc = true
	case "stderr":
		target = "__lang_stderr"
		g.usesStdStreams = true
		g.usesAlloc = true
	case "__str_idx", "__arr_idx":
		// IR-side bounds-check stubs. We don't currently have ARM32
		// equivalents, so the IR walker adds the bound-check itself
		// inline. Fall back to an unchecked address compute that
		// matches the wasm behaviour for in-range indices.
		return g.emitInlineIdxHelper(target)
	}
	// Reader / Writer method calls arrive as the post-checker
	// mangled `__method_Reader_*` / `__method_Writer_*` names.
	// Trip the streaming-IO flag so the runtime helper section
	// emits the matching helpers.
	if strings.HasPrefix(target, "__method_Reader_") || strings.HasPrefix(target, "__method_Writer_") {
		g.usesStreamIO = true
		g.usesAlloc = true
	}
	argc := int(op.I32)
	if argc <= regArgs {
		// Args are on the operand stack with the rightmost on top,
		// so popping r{argc-1} first lands each in its expected
		// register.
		for i := argc - 1; i >= 0; i-- {
			g.emit("pop {r%d}", i)
		}
		g.emit("bl %s", target)
		g.emit("push {r0}")
		return nil
	}
	// argc > 4: register args come from offsets near the top, but
	// the extras need to be reversed in place — the IR pushed them
	// rightmost-on-top while AAPCS expects leftmost-on-top in the
	// callee's stack-arg area at [sp+0]. We:
	//  1. Read args 0..3 from their stacked offsets into r0..r3.
	//  2. Move each extra a_{4+j} from its current offset
	//     [sp+(K-1-j)*4] up to its AAPCS slot [sp+(N-K+j)*4],
	//     using r12 as a temporary.
	//  3. Bump sp by (argc-K)*4 = 16 so the new sp[0] is a4.
	K := argc - regArgs
	for i := 0; i < regArgs; i++ {
		off := (argc - 1 - i) * 4
		g.emit("ldr r%d, [sp, #%d]", i, off)
	}
	for j := 0; j < K; j++ {
		src := (K - 1 - j) * 4
		dst := (argc - K + j) * 4
		g.emit("ldr r12, [sp, #%d]", src)
		g.emit("str r12, [sp, #%d]", dst)
	}
	g.emit("add sp, sp, #%d", (argc-K)*4)
	g.emit("bl %s", target)
	// Free the extras area (K*4 bytes) the AAPCS callee left for us.
	g.emit("add sp, sp, #%d", K*4)
	g.emit("push {r0}")
	return nil
}

// emitInlineIdxHelper handles the `__str_idx` / `__arr_idx` runtime
// stubs the IR emits for `s[i]` / `a[i]`. Without a per-target
// runtime, we leave the bounds check off and just compute the
// effective address (base + index, scaled for arrays). The IR walker
// follows the helper call with an OpLoad / OpLoadByte that reads
// from r0.
func (g *generator) emitInlineIdxHelper(name string) error {
	// Stack: [base, idx], top = idx.
	g.emit("pop {r0}") // idx
	g.emit("pop {r1}") // base
	switch name {
	case "__str_idx":
		g.emit("add r0, r1, r0")
	case "__arr_idx":
		g.emit("add r0, r1, r0, lsl #2")
	}
	g.emit("push {r0}")
	return nil
}

// emitCallIndirect resolves a function-typed local: the IR emitted
// the function-value pointer immediately before the call op (via
// OpLoadLocal of the function value), so the pointer is on top of
// the stack and args are below it in left-to-right order. ARM
// dispatches with `blx` so the runtime cost is one extra register
// move compared to a direct call.
func (g *generator) emitCallIndirect(op ir.Op) error {
	argc := int(op.I32)
	g.emit("pop {r12}") // r12 = function pointer
	if argc <= regArgs {
		for i := argc - 1; i >= 0; i-- {
			g.emit("pop {r%d}", i)
		}
	} else {
		for i := regArgs - 1; i >= 0; i-- {
			off := (argc - 1 - i) * 4
			g.emit("ldr r%d, [sp, #%d]", i, off)
		}
	}
	g.emit("blx r12")
	if argc > regArgs {
		g.emit("add sp, sp, #%d", (argc-regArgs)*4)
	}
	g.emit("push {r0}")
	return nil
}
