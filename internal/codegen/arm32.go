// Package codegen emits ARM 32-bit assembly (GNU syntax, suitable for
// `arm-linux-gnueabihf-as` / gcc) from a checked Program.
//
// Calling convention: standard AAPCS with one simplification.
//
//   * Every expression's value is left in r0.
//   * Binary operators evaluate the left operand, push r0, evaluate the
//     right operand into r0, then pop the left operand into r1.
//   * Functions accept up to four parameters (passed in r0–r3). Each
//     parameter is spilled to a stack slot in the prologue so the rest of
//     the body can treat them like locals.
//   * Locals, spilled parameters and stack-allocated array literals all
//     live at negative offsets from fp.
//
// The output is plain `.s` text — feed it to `gcc` (or `as` + `ld`) to
// produce a runnable executable, and run it with `qemu-arm` if you're not
// on an ARM host.
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
func EmitWithOptions(prog *ast.Program, info *checker.Info, opts Options) (string, error) {
	g := &generator{
		info:        info,
		stringLabel: map[string]string{},
		srcFile:     opts.SourceFile,
	}
	g.line(`.arch armv7-a`)
	g.line(`.text`)
	if g.srcFile != "" {
		g.line(fmt.Sprintf(`.file 1 %q`, g.srcFile))
	}
	for _, fn := range prog.Funcs {
		if err := g.emitFunction(fn); err != nil {
			return "", err
		}
	}
	// Emit the runtime string-concat helper only if the program uses
	// string `+`. It calls libc strlen / malloc / memcpy, so the linker
	// pulls those in transitively.
	if g.usesStrcat {
		g.emitStrcatRuntime()
	}
	// String literals collected during emit go into .rodata.
	if len(g.stringOrder) > 0 {
		g.line("")
		g.line(`.section .rodata`)
		for _, s := range g.stringOrder {
			g.label(g.stringLabel[s])
			g.line("\t.asciz " + escapeForGAS(s))
		}
	}
	// Mark the stack as non-executable. Without this the GNU linker
	// assumes an executable stack and emits a deprecation warning that
	// will become a hard error in future binutils.
	g.line("")
	g.line(`.section .note.GNU-stack,"",%progbits`)
	return peephole(g.out.String()), nil
}

type generator struct {
	out         strings.Builder
	info        *checker.Info
	labelN      int
	frame       *frameLayout
	epilogue    string
	stringLabel map[string]string // value -> label
	stringOrder []string          // insertion order so output is deterministic
	loops       []loopLabels      // stack of (continue, break) per enclosing loop
	usesStrcat  bool              // true if the program needs the strcat helper
	srcFile     string            // non-empty enables DWARF .file/.loc directives
	// Tail-call optimization plumbing for the *current* function:
	// bodyLabel is the address right after the prologue, currentFunc
	// remembers the declaration so we can match `return f(...)` against
	// f, and paramSlot is the original (un-shadowed) fp offset of each
	// parameter for non-leaf functions.
	currentFunc *ast.FuncDecl
	bodyLabel   string
	paramSlot   map[string]int
}

// emitLoc emits a `.loc 1 <line> <col>` directive when DWARF debug info
// is requested. GAS turns these into a DW_LNS_advance_line / setting
// table that gdb can use for source-level stepping.
func (g *generator) emitLoc(p ast.Position) {
	if g.srcFile == "" || p.Line <= 0 {
		return
	}
	g.emit(".loc 1 %d %d", p.Line, p.Col)
}

// loopLabels gives each enclosing loop the labels that `continue` and
// `break` should jump to. The stack is grown on loop entry and popped
// on exit so nested loops resolve to the innermost target.
type loopLabels struct {
	continueL, breakL string
}

// emitStrcatRuntime emits a leaf-style helper that allocates a fresh
// buffer holding the concatenation of two NUL-terminated strings:
//
//	r0 = a, r1 = b   →   r0 = malloc'd a ++ b (NUL-terminated)
//
// It uses libc strlen, malloc and memcpy. The buffer is never freed —
// strings in this language are immutable but not GC'd.
func (g *generator) emitStrcatRuntime() {
	g.line("")
	g.line(".global __lang_strcat")
	g.line(".type __lang_strcat, %function")
	g.label("__lang_strcat")
	g.emit("push {r4, r5, r6, r7, lr}")
	g.emit("sub sp, sp, #4") // 8-byte alignment
	g.emit("mov r4, r0")     // r4 = a
	g.emit("mov r5, r1")     // r5 = b
	g.emit("bl strlen")      // strlen(a) → r0
	g.emit("mov r6, r0")     // r6 = la
	g.emit("mov r0, r5")
	g.emit("bl strlen")      // strlen(b) → r0
	g.emit("mov r7, r0")     // r7 = lb
	g.emit("add r0, r6, r7")
	g.emit("add r0, r0, #1") // total + 1 for NUL
	g.emit("bl malloc")      // r0 = result
	g.emit("mov r1, r4")     // src = a
	g.emit("mov r2, r6")     // n = la
	g.emit("mov r4, r0")     // r4 = result (overwrites a, no longer needed)
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

// frameLayout records the negative-from-fp byte offset of each named slot
// (parameters and `var`s) plus each array literal's storage region.
type frameLayout struct {
	slots      map[string]int        // most recent binding for each name (fp-relative)
	stack      []map[string]int      // shadowing chain
	varSlot    map[*ast.Var]int      // var node -> offset
	arraySlot  map[*ast.ArrayLit]int // array literal -> offset of base
	switchSlot map[*ast.Switch]int   // switch node -> offset of tag spill
	frameBytes int                   // total size, rounded to 8
	// paramReg pins parameters of leaf functions to a callee-saved
	// register (r4..r7) instead of a stack slot. A name found here
	// short-circuits the slots/stack lookup.
	paramReg map[string]int
}

func (f *frameLayout) push() { f.stack = append(f.stack, map[string]int{}) }
func (f *frameLayout) pop() {
	top := f.stack[len(f.stack)-1]
	f.stack = f.stack[:len(f.stack)-1]
	// Restore prior bindings for any name shadowed in this scope.
	for name := range top {
		f.slots[name] = 0
		for i := len(f.stack) - 1; i >= 0; i-- {
			if v, ok := f.stack[i][name]; ok {
				f.slots[name] = v
				break
			}
		}
	}
}
func (f *frameLayout) bind(name string, off int) {
	f.stack[len(f.stack)-1][name] = off
	f.slots[name] = off
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

// ---------- function emit ----------

func (g *generator) emitFunction(fn *ast.FuncDecl) error {
	g.line("")
	g.line(fmt.Sprintf(".global %s", fn.Name))
	g.line(fmt.Sprintf(".type %s, %%function", fn.Name))
	g.label(fn.Name)
	// CFI directives let gdb / addr2line / libunwind reconstruct the
	// caller's frame from this function's stack. .cfi_startproc opens
	// a Frame Description Entry; matching .cfi_endproc closes it just
	// before .size, below.
	g.emit(".cfi_startproc")

	// Decide whether this function is a "leaf" — no user-level Call
	// expressions in its body. AAPCS guarantees that callee-saved
	// registers (r4..r11) are preserved across calls to libc / aeabi
	// helpers, so leaf functions can pin their parameters to r4..r7
	// instead of paying for a stack spill on every read.
	leaf := len(fn.Params) >= 1 && len(fn.Params) <= regArgs && !containsCall(fn.Body)

	// Lay out the frame: each param (only when *not* register-pinned),
	// then each `var`, then each array literal in source order.
	g.frame = &frameLayout{
		slots:     map[string]int{},
		varSlot:   map[*ast.Var]int{},
		arraySlot:  map[*ast.ArrayLit]int{},
		switchSlot: map[*ast.Switch]int{},
		paramReg:  map[string]int{},
	}
	paramOffs := map[string]int{}
	off := 0
	if leaf {
		for i, p := range fn.Params {
			g.frame.paramReg[p.Name] = 4 + i
		}
	} else {
		for _, p := range fn.Params {
			off += 4
			paramOffs[p.Name] = -off
		}
	}
	for _, v := range g.info.Locals[fn] {
		off += 4
		g.frame.varSlot[v] = -off
	}
	// Reserve space for every array literal in the body. We pre-walk to
	// count them so the frame size is finalised before we emit any
	// instructions.
	walkArrays(fn.Body, func(a *ast.ArrayLit) {
		off += 4 * len(a.Elems)
		g.frame.arraySlot[a] = -off
	})
	// Reserve one i32 spill slot per Switch in the body, used to stash
	// the tag value across case-value evaluations.
	walkSwitches(fn.Body, func(sw *ast.Switch) {
		off += 4
		g.frame.switchSlot[sw] = -off
	})
	// Round up to 8 bytes for AAPCS.
	if off%8 != 0 {
		off += 4
	}
	g.frame.frameBytes = off

	// Prologue.
	if leaf {
		// Save the param registers we're about to use, plus fp/lr.
		// The list is r4..r{4+P-1}, fp, lr.
		regs := []string{}
		for i := 0; i < len(fn.Params); i++ {
			regs = append(regs, fmt.Sprintf("r%d", 4+i))
		}
		regs = append(regs, "fp", "lr")
		g.emit("push {%s}", strings.Join(regs, ", "))
		// CFA moved by (P+2)*4. Each saved register sits at a fixed
		// negative offset from CFA (r4 furthest down, lr nearest the
		// top), matching the order push wrote them.
		pushBytes := (len(fn.Params) + 2) * 4
		g.emit(".cfi_def_cfa_offset %d", pushBytes)
		for i := 0; i < len(fn.Params); i++ {
			g.emit(".cfi_offset r%d, %d", 4+i, -pushBytes+i*4)
		}
		g.emit(".cfi_offset fp, -8")
		g.emit(".cfi_offset lr, -4")
		// fp should still point at the saved fp word, exactly as in
		// the non-leaf prologue. After pushing P param-regs first,
		// the saved fp lands at sp + 4*P.
		g.emit("add fp, sp, #%d", 4*len(fn.Params))
		g.emit(".cfi_def_cfa_register fp")
		if g.frame.frameBytes > 0 {
			g.emit("sub sp, sp, #%d", g.frame.frameBytes)
		}
		// Transfer incoming arg registers to their pinned spots.
		for i := range fn.Params {
			g.emit("mov r%d, r%d", 4+i, i)
		}
	} else {
		g.emit("push {fp, lr}")
		g.emit(".cfi_def_cfa_offset 8")
		g.emit(".cfi_offset fp, -8")
		g.emit(".cfi_offset lr, -4")
		g.emit("mov fp, sp")
		g.emit(".cfi_def_cfa_register fp")
		if g.frame.frameBytes > 0 {
			g.emit("sub sp, sp, #%d", g.frame.frameBytes)
		}
		// Move incoming parameters into their local slots:
		//   * args 0..3 come in r0..r3 — spill straight in
		//   * args 4..N come from the caller's stack frame, which after our
		//     prologue starts at fp+8 (fp[0]=saved fp, fp[4]=saved lr)
		for i, p := range fn.Params {
			if i < regArgs {
				g.emit("str r%d, [fp, #%d]", i, paramOffs[p.Name])
			} else {
				callerOff := 8 + (i-regArgs)*4
				g.emit("ldr r12, [fp, #%d]", callerOff)
				g.emit("str r12, [fp, #%d]", paramOffs[p.Name])
			}
		}
	}

	g.epilogue = g.freshLabel("epi_" + fn.Name)

	// Tail-call setup: a label right here (after the prologue) is what
	// we'll branch to when we recognise `return self(...)`. The
	// paramSlot map captures the original (un-shadowed) fp offset for
	// each non-leaf param so we can write to the right place even if a
	// later `var` reuses the param's name.
	g.currentFunc = fn
	g.bodyLabel = g.freshLabel("body_" + fn.Name)
	g.paramSlot = paramOffs
	g.label(g.bodyLabel)
	defer func() {
		g.currentFunc = nil
		g.bodyLabel = ""
		g.paramSlot = nil
	}()

	// Open root scope and bind every parameter.
	g.frame.push()
	for _, p := range fn.Params {
		g.frame.bind(p.Name, paramOffs[p.Name])
	}

	for _, st := range fn.Body.Stmts {
		if err := g.stmt(st); err != nil {
			return err
		}
	}

	g.frame.pop()

	// If control falls off the end without `return`, emit a default
	// return of 0 — harmless for void functions, defined for number ones.
	g.emit("mov r0, #0")
	g.label(g.epilogue)
	if leaf {
		// Step sp back to where the param-regs were pushed, then pop
		// the same register list we saved in the prologue.
		g.emit("sub sp, fp, #%d", 4*len(fn.Params))
		regs := []string{}
		for i := 0; i < len(fn.Params); i++ {
			regs = append(regs, fmt.Sprintf("r%d", 4+i))
		}
		regs = append(regs, "fp", "lr")
		g.emit("pop {%s}", strings.Join(regs, ", "))
	} else {
		g.emit("mov sp, fp")
		g.emit("pop {fp, lr}")
	}
	g.emit("bx lr")
	g.emit(".cfi_endproc")
	g.line(fmt.Sprintf(".size %s, .-%s", fn.Name, fn.Name))
	return nil
}

// walkArrays calls visit on every ArrayLit reachable from n.
func walkArrays(n any, visit func(*ast.ArrayLit)) {
	switch x := n.(type) {
	case *ast.Block:
		for _, s := range x.Stmts {
			walkArrays(s, visit)
		}
	case *ast.If:
		walkArrays(x.Cond, visit)
		walkArrays(x.Then, visit)
		if x.Else != nil {
			walkArrays(x.Else, visit)
		}
	case *ast.While:
		walkArrays(x.Cond, visit)
		walkArrays(x.Body, visit)
	case *ast.For:
		if x.Init != nil {
			walkArrays(x.Init, visit)
		}
		walkArrays(x.Cond, visit)
		if x.Step != nil {
			walkArrays(x.Step, visit)
		}
		walkArrays(x.Body, visit)
	case *ast.Break, *ast.Continue:
		// no nested expressions
	case *ast.Return:
		if x.Value != nil {
			walkArrays(x.Value, visit)
		}
	case *ast.Var:
		walkArrays(x.Init, visit)
	case *ast.ExprStmt:
		walkArrays(x.Expr, visit)
	case *ast.ArrayLit:
		visit(x)
		for _, e := range x.Elems {
			walkArrays(e, visit)
		}
	case *ast.Index:
		walkArrays(x.Array, visit)
		walkArrays(x.Idx, visit)
	case *ast.Call:
		walkArrays(x.Callee, visit)
		for _, a := range x.Args {
			walkArrays(a, visit)
		}
	case *ast.Binary:
		walkArrays(x.Left, visit)
		walkArrays(x.Right, visit)
	case *ast.Unary:
		walkArrays(x.Operand, visit)
	case *ast.Assign:
		walkArrays(x.Target, visit)
		walkArrays(x.Value, visit)
	case *ast.Switch:
		walkArrays(x.Tag, visit)
		for _, k := range x.Cases {
			for _, v := range k.Values {
				walkArrays(v, visit)
			}
			walkArrays(k.Body, visit)
		}
		if x.Default != nil {
			walkArrays(x.Default, visit)
		}
	}
}

// walkSwitches visits every *ast.Switch nested in n. Used by the frame
// allocator to reserve one i32 spill slot per switch tag.
func walkSwitches(n any, visit func(*ast.Switch)) {
	switch x := n.(type) {
	case *ast.Switch:
		visit(x)
		walkSwitches(x.Tag, visit)
		for _, k := range x.Cases {
			for _, v := range k.Values {
				walkSwitches(v, visit)
			}
			walkSwitches(k.Body, visit)
		}
		if x.Default != nil {
			walkSwitches(x.Default, visit)
		}
	case *ast.Block:
		for _, s := range x.Stmts {
			walkSwitches(s, visit)
		}
	case *ast.If:
		walkSwitches(x.Cond, visit)
		walkSwitches(x.Then, visit)
		if x.Else != nil {
			walkSwitches(x.Else, visit)
		}
	case *ast.While:
		walkSwitches(x.Cond, visit)
		walkSwitches(x.Body, visit)
	case *ast.For:
		if x.Init != nil {
			walkSwitches(x.Init, visit)
		}
		walkSwitches(x.Cond, visit)
		if x.Step != nil {
			walkSwitches(x.Step, visit)
		}
		walkSwitches(x.Body, visit)
	case *ast.Return:
		if x.Value != nil {
			walkSwitches(x.Value, visit)
		}
	case *ast.Var:
		walkSwitches(x.Init, visit)
	case *ast.ExprStmt:
		walkSwitches(x.Expr, visit)
	}
}

// isTailRecursive reports whether c is a direct call to the enclosing
// function and is eligible for in-place argument-update + branch-to-
// body-label rewriting. We require an exact arity match; functions
// with more than 4 parameters are skipped because their extra args
// live on the caller's stack frame, which we don't own.
func (g *generator) isTailRecursive(c *ast.Call) bool {
	if g.currentFunc == nil {
		return false
	}
	id, ok := c.Callee.(*ast.Ident)
	if !ok || id.Name != g.currentFunc.Name {
		return false
	}
	if len(g.currentFunc.Params) > regArgs {
		return false
	}
	return len(c.Args) == len(g.currentFunc.Params)
}

// emitTailCall evaluates each argument and pushes it, then pops them
// back into the right home (callee-saved register for leaf functions,
// fp-relative slot otherwise) and branches to the post-prologue body
// label. The push/pop pair is what lets us evaluate args that read
// the *current* parameter values without losing them.
func (g *generator) emitTailCall(c *ast.Call) error {
	for _, a := range c.Args {
		if err := g.expr(a); err != nil {
			return err
		}
		g.emit("push {r0}")
	}
	leaf := len(g.frame.paramReg) > 0
	for i := len(c.Args) - 1; i >= 0; i-- {
		name := g.currentFunc.Params[i].Name
		if leaf {
			if reg, ok := g.frame.paramReg[name]; ok {
				g.emit("pop {r%d}", reg)
				continue
			}
		}
		off, ok := g.paramSlot[name]
		if !ok {
			return fmt.Errorf("codegen: tail call: missing slot for param %q", name)
		}
		g.emit("pop {r0}")
		g.emit("str r0, [fp, #%d]", off)
	}
	g.emit("b %s", g.bodyLabel)
	return nil
}

// containsCall reports whether n holds any *ast.Call subtree.
// Used to identify "leaf" functions for the simple register
// allocator: leaves never bl into user code, so callee-saved
// registers stay live across whatever bl's we do emit (aeabi
// helpers, libc) — those preserve r4..r11 by AAPCS.
func containsCall(n any) bool {
	switch x := n.(type) {
	case nil:
		return false
	case *ast.Call:
		return true
	case *ast.Block:
		for _, s := range x.Stmts {
			if containsCall(s) {
				return true
			}
		}
	case *ast.If:
		if containsCall(x.Cond) || containsCall(x.Then) {
			return true
		}
		if x.Else != nil {
			return containsCall(x.Else)
		}
	case *ast.While:
		return containsCall(x.Cond) || containsCall(x.Body)
	case *ast.For:
		if x.Init != nil && containsCall(x.Init) {
			return true
		}
		if containsCall(x.Cond) {
			return true
		}
		if x.Step != nil && containsCall(x.Step) {
			return true
		}
		return containsCall(x.Body)
	case *ast.Break, *ast.Continue:
		return false
	case *ast.Return:
		return x.Value != nil && containsCall(x.Value)
	case *ast.Var:
		return containsCall(x.Init)
	case *ast.ExprStmt:
		return containsCall(x.Expr)
	case *ast.NumberLit, *ast.BoolLit, *ast.StringLit, *ast.FloatLit, *ast.Ident:
		return false
	case *ast.ArrayLit:
		for _, e := range x.Elems {
			if containsCall(e) {
				return true
			}
		}
	case *ast.Index:
		return containsCall(x.Array) || containsCall(x.Idx)
	case *ast.Binary:
		return containsCall(x.Left) || containsCall(x.Right)
	case *ast.Unary:
		return containsCall(x.Operand)
	case *ast.Assign:
		return containsCall(x.Target) || containsCall(x.Value)
	case *ast.Switch:
		if containsCall(x.Tag) {
			return true
		}
		for _, k := range x.Cases {
			for _, v := range k.Values {
				if containsCall(v) {
					return true
				}
			}
			if containsCall(k.Body) {
				return true
			}
		}
		if x.Default != nil {
			return containsCall(x.Default)
		}
	}
	return false
}

// ---------- statements ----------

func (g *generator) stmt(s ast.Stmt) error {
	g.emitLoc(s.Pos())
	switch n := s.(type) {
	case *ast.Block:
		g.frame.push()
		for _, ss := range n.Stmts {
			if err := g.stmt(ss); err != nil {
				return err
			}
		}
		g.frame.pop()
	case *ast.If:
		elseL := g.freshLabel("else")
		endL := g.freshLabel("endif")
		if err := g.expr(n.Cond); err != nil {
			return err
		}
		g.emit("cmp r0, #0")
		if n.Else != nil {
			g.emit("beq %s", elseL)
			if err := g.stmt(n.Then); err != nil {
				return err
			}
			g.emit("b %s", endL)
			g.label(elseL)
			if err := g.stmt(n.Else); err != nil {
				return err
			}
		} else {
			g.emit("beq %s", endL)
			if err := g.stmt(n.Then); err != nil {
				return err
			}
		}
		g.label(endL)
	case *ast.While:
		topL := g.freshLabel("loop")
		endL := g.freshLabel("endloop")
		g.label(topL)
		if err := g.expr(n.Cond); err != nil {
			return err
		}
		g.emit("cmp r0, #0")
		g.emit("beq %s", endL)
		// While has no separate step, so `continue` jumps to the top.
		g.loops = append(g.loops, loopLabels{continueL: topL, breakL: endL})
		if err := g.stmt(n.Body); err != nil {
			return err
		}
		g.loops = g.loops[:len(g.loops)-1]
		g.emit("b %s", topL)
		g.label(endL)
	case *ast.For:
		topL := g.freshLabel("forhead")
		stepL := g.freshLabel("forstep")
		endL := g.freshLabel("forend")
		if n.Init != nil {
			if err := g.stmt(n.Init); err != nil {
				return err
			}
		}
		g.label(topL)
		if err := g.expr(n.Cond); err != nil {
			return err
		}
		g.emit("cmp r0, #0")
		g.emit("beq %s", endL)
		// `continue` jumps to the step so the increment still runs.
		g.loops = append(g.loops, loopLabels{continueL: stepL, breakL: endL})
		if err := g.stmt(n.Body); err != nil {
			return err
		}
		g.loops = g.loops[:len(g.loops)-1]
		g.label(stepL)
		if n.Step != nil {
			if err := g.stmt(n.Step); err != nil {
				return err
			}
		}
		g.emit("b %s", topL)
		g.label(endL)
	case *ast.Break:
		if len(g.loops) == 0 {
			return fmt.Errorf("codegen: break outside of a loop")
		}
		g.emit("b %s", g.loops[len(g.loops)-1].breakL)
	case *ast.Continue:
		if len(g.loops) == 0 {
			return fmt.Errorf("codegen: continue outside of a loop")
		}
		g.emit("b %s", g.loops[len(g.loops)-1].continueL)
	case *ast.Return:
		// Self-recursive tail call: replace `return f(args)` (where f
		// is the enclosing function) with parameter updates plus a
		// branch back to the post-prologue body label, avoiding a new
		// stack frame.
		if n.Value != nil {
			if call, ok := n.Value.(*ast.Call); ok && g.isTailRecursive(call) {
				return g.emitTailCall(call)
			}
			if err := g.expr(n.Value); err != nil {
				return err
			}
		} else {
			g.emit("mov r0, #0")
		}
		g.emit("b %s", g.epilogue)
	case *ast.Var:
		if err := g.expr(n.Init); err != nil {
			return err
		}
		off := g.frame.varSlot[n]
		g.emit("str r0, [fp, #%d]", off)
		g.frame.bind(n.Name, off)
	case *ast.ExprStmt:
		if err := g.expr(n.Expr); err != nil {
			return err
		}
	case *ast.Switch:
		return g.switchStmt(n)
	}
	return nil
}

// switchStmt lowers a switch into the equivalent if/else chain. The
// tag is evaluated once and stashed in a dedicated frame slot so it
// survives any function calls in case-value or body expressions.
// `break` inside a case body finds the end label via the loops stack;
// we publish a synthetic entry whose continueL is the parent loop's
// continueL so `continue` still reaches the enclosing real loop.
func (g *generator) switchStmt(n *ast.Switch) error {
	endL := g.freshLabel("sw_end")
	if err := g.expr(n.Tag); err != nil {
		return err
	}
	tagOff := g.frame.switchSlot[n]
	g.emit("str r0, [fp, #%d]", tagOff)

	var parentCont string
	if len(g.loops) > 0 {
		parentCont = g.loops[len(g.loops)-1].continueL
	}
	g.loops = append(g.loops, loopLabels{breakL: endL, continueL: parentCont})

	for _, k := range n.Cases {
		nextL := g.freshLabel("sw_next")
		bodyL := g.freshLabel("sw_body")
		for _, v := range k.Values {
			if err := g.expr(v); err != nil {
				return err
			}
			g.emit("ldr r1, [fp, #%d]", tagOff)
			g.emit("cmp r1, r0")
			g.emit("beq %s", bodyL)
		}
		g.emit("b %s", nextL)
		g.label(bodyL)
		for _, st := range k.Body.Stmts {
			if err := g.stmt(st); err != nil {
				return err
			}
		}
		g.emit("b %s", endL)
		g.label(nextL)
	}
	if n.Default != nil {
		for _, st := range n.Default.Stmts {
			if err := g.stmt(st); err != nil {
				return err
			}
		}
	}
	g.loops = g.loops[:len(g.loops)-1]
	g.label(endL)
	return nil
}

// ---------- expressions ----------

func (g *generator) expr(e ast.Expr) error {
	switch n := e.(type) {
	case *ast.NumberLit:
		g.emit("ldr r0, =%d", n.Value)
	case *ast.BoolLit:
		if n.Value {
			g.emit("mov r0, #1")
		} else {
			g.emit("mov r0, #0")
		}
	case *ast.StringLit:
		g.emit("ldr r0, =%s", g.internString(n.Value))
	case *ast.FloatLit:
		return fmt.Errorf("codegen: float literals are not yet supported by the arm32 backend (use the wasm backend)")
	case *ast.Ident:
		// Local var (incl. shadowing) takes precedence over a pinned
		// param so leaf-function bodies can still declare a `var x`
		// that hides param `x`.
		if off, ok := g.frame.slots[n.Name]; ok && off != 0 {
			g.emit("ldr r0, [fp, #%d]", off)
			return nil
		}
		if reg, ok := g.frame.paramReg[n.Name]; ok {
			g.emit("mov r0, r%d", reg)
			return nil
		}
		// Fall back to a function reference (used for direct calls below).
		g.emit("ldr r0, =%s", n.Name)
	case *ast.Unary:
		if err := g.expr(n.Operand); err != nil {
			return err
		}
		switch n.Op {
		case "-":
			g.emit("rsb r0, r0, #0")
		case "!":
			g.emit("cmp r0, #0")
			g.emit("moveq r0, #1")
			g.emit("movne r0, #0")
		}
	case *ast.Binary:
		return g.binary(n)
	case *ast.Call:
		return g.call(n)
	case *ast.Index:
		return g.index(n)
	case *ast.ArrayLit:
		return g.arrayLit(n)
	case *ast.Assign:
		return g.assign(n)
	default:
		return fmt.Errorf("codegen: unhandled expression %T", e)
	}
	return nil
}

func (g *generator) binary(n *ast.Binary) error {
	// Short-circuit logical operators.
	if n.Op == "&&" || n.Op == "||" {
		end := g.freshLabel("sc")
		if err := g.expr(n.Left); err != nil {
			return err
		}
		g.emit("cmp r0, #0")
		if n.Op == "&&" {
			g.emit("beq %s", end)
		} else {
			g.emit("bne %s", end)
		}
		if err := g.expr(n.Right); err != nil {
			return err
		}
		g.label(end)
		return nil
	}

	if err := g.expr(n.Left); err != nil {
		return err
	}
	g.emit("push {r0}")
	if err := g.expr(n.Right); err != nil {
		return err
	}
	g.emit("pop {r1}") // r1 = left, r0 = right

	switch n.Op {
	case "+":
		if n.IsStringConcat {
			// __lang_strcat takes (a, b) in (r0, r1); the binary helper
			// just left us with r1 = a, r0 = b. Swap via r2.
			g.usesStrcat = true
			g.emit("mov r2, r0")
			g.emit("mov r0, r1")
			g.emit("mov r1, r2")
			g.emit("bl __lang_strcat")
			break
		}
		g.emit("add r0, r1, r0")
	case "-":
		g.emit("sub r0, r1, r0")
	case "*":
		g.emit("mul r0, r1, r0")
	case "/":
		// AAPCS soft helper. Args: r0 = numerator, r1 = denominator.
		g.emit("mov r2, r0")
		g.emit("mov r0, r1")
		g.emit("mov r1, r2")
		g.emit("bl __aeabi_idiv")
	case "%":
		// __aeabi_idivmod returns quotient in r0, remainder in r1.
		g.emit("mov r2, r0")
		g.emit("mov r0, r1")
		g.emit("mov r1, r2")
		g.emit("bl __aeabi_idivmod")
		g.emit("mov r0, r1")
	case "&":
		g.emit("and r0, r1, r0")
	case "|":
		g.emit("orr r0, r1, r0")
	case "^":
		g.emit("eor r0, r1, r0")
	case "<<":
		g.emit("lsl r0, r1, r0")
	case ">>":
		// Arithmetic shift right preserves the sign bit; numbers are signed.
		g.emit("asr r0, r1, r0")
	case "==":
		g.emit("cmp r1, r0")
		g.emit("moveq r0, #1")
		g.emit("movne r0, #0")
	case "!=":
		g.emit("cmp r1, r0")
		g.emit("movne r0, #1")
		g.emit("moveq r0, #0")
	case "<":
		g.emit("cmp r1, r0")
		g.emit("movlt r0, #1")
		g.emit("movge r0, #0")
	case "<=":
		g.emit("cmp r1, r0")
		g.emit("movle r0, #1")
		g.emit("movgt r0, #0")
	case ">":
		g.emit("cmp r1, r0")
		g.emit("movgt r0, #1")
		g.emit("movle r0, #0")
	case ">=":
		g.emit("cmp r1, r0")
		g.emit("movge r0, #1")
		g.emit("movlt r0, #0")
	default:
		return fmt.Errorf("codegen: unknown binary operator %q", n.Op)
	}
	return nil
}

func (g *generator) call(n *ast.Call) error {
	id, ok := n.Callee.(*ast.Ident)
	if !ok {
		return fmt.Errorf("codegen: indirect call from non-identifier expression")
	}
	target := id.Name
	// `print(s)` lowers to libc puts, which already adds a newline.
	if target == "print" {
		target = "puts"
	}

	// If the name resolves to a local var or a pinned param, this is an
	// indirect call through a function value: load the pointer into r12
	// after argument setup and emit `blx r12` instead of `bl name`.
	indirect := false
	indirectFromSlot := 0
	indirectFromReg := -1
	if off, ok := g.frame.slots[id.Name]; ok && off != 0 {
		indirect = true
		indirectFromSlot = off
	} else if reg, ok := g.frame.paramReg[id.Name]; ok {
		indirect = true
		indirectFromReg = reg
	}
	N := len(n.Args)

	emitBranch := func() {
		if !indirect {
			g.emit("bl %s", target)
			return
		}
		if indirectFromReg >= 0 {
			g.emit("mov r12, r%d", indirectFromReg)
		} else {
			g.emit("ldr r12, [fp, #%d]", indirectFromSlot)
		}
		g.emit("blx r12")
	}

	// Common case: ≤ 4 args. Push each in source order, pop into
	// r{N-1}..r0. Single-arg calls collapse to nothing once the
	// peephole pass folds the push/pop pair.
	if N <= regArgs {
		for _, a := range n.Args {
			if err := g.expr(a); err != nil {
				return err
			}
			g.emit("push {r0}")
		}
		for i := N - 1; i >= 0; i-- {
			g.emit("pop {r%d}", i)
		}
		emitBranch()
		return nil
	}

	// >4 args. Pre-allocate one stack region big enough for both the
	// AAPCS stack-arg slots (args 4..N-1) and a temp area for the
	// register-bound args (0..3) so we can evaluate left-to-right
	// without juggling registers across calls inside arg expressions.
	//
	// Layout (low → high addresses, i.e. sp[0] is at the bottom):
	//
	//   sp[0]              arg 4         ─┐
	//   sp[4]              arg 5          ├ AAPCS stack-arg area
	//   ...                 ...           │  (callee reads from here)
	//   sp[(N-5)*4]        arg N-1       ─┘
	//   sp[(N-4)*4]        temp arg 0    ─┐
	//   sp[(N-3)*4]        temp arg 1     ├ register-load staging
	//   sp[(N-2)*4]        temp arg 2     │
	//   sp[(N-1)*4]        temp arg 3    ─┘
	//
	// The whole thing is rounded up to 8 bytes for AAPCS alignment.
	totalBytes := N * 4
	if totalBytes%8 != 0 {
		totalBytes += 4
	}
	g.emit("sub sp, sp, #%d", totalBytes)

	tempBase := (N - regArgs) * 4 // start of the register-load staging area

	for i, a := range n.Args {
		if err := g.expr(a); err != nil {
			return err
		}
		if i < regArgs {
			g.emit("str r0, [sp, #%d]", tempBase+i*4)
		} else {
			g.emit("str r0, [sp, #%d]", (i-regArgs)*4)
		}
	}
	for i := 0; i < regArgs; i++ {
		g.emit("ldr r%d, [sp, #%d]", i, tempBase+i*4)
	}
	emitBranch()
	g.emit("add sp, sp, #%d", totalBytes)
	return nil
}

func (g *generator) arrayLit(n *ast.ArrayLit) error {
	base := g.frame.arraySlot[n]
	for i, el := range n.Elems {
		if err := g.expr(el); err != nil {
			return err
		}
		g.emit("str r0, [fp, #%d]", base+4*i)
	}
	g.emit("add r0, fp, #%d", base)
	return nil
}

func (g *generator) index(n *ast.Index) error {
	if err := g.expr(n.Array); err != nil {
		return err
	}
	g.emit("push {r0}")
	if err := g.expr(n.Idx); err != nil {
		return err
	}
	g.emit("pop {r1}")               // r1 = base
	g.emit("ldr r0, [r1, r0, lsl #2]") // r0 = base[idx]
	return nil
}

func (g *generator) assign(n *ast.Assign) error {
	switch t := n.Target.(type) {
	case *ast.Ident:
		if err := g.expr(n.Value); err != nil {
			return err
		}
		if off, ok := g.frame.slots[t.Name]; ok && off != 0 {
			g.emit("str r0, [fp, #%d]", off)
			return nil
		}
		if reg, ok := g.frame.paramReg[t.Name]; ok {
			g.emit("mov r%d, r0", reg)
			return nil
		}
		return fmt.Errorf("codegen: cannot assign to %q (no slot)", t.Name)
	case *ast.Index:
		if err := g.expr(t.Array); err != nil {
			return err
		}
		g.emit("push {r0}")
		if err := g.expr(t.Idx); err != nil {
			return err
		}
		g.emit("push {r0}")
		if err := g.expr(n.Value); err != nil {
			return err
		}
		g.emit("pop {r1}") // r1 = idx
		g.emit("pop {r2}") // r2 = base
		g.emit("str r0, [r2, r1, lsl #2]")
	default:
		return fmt.Errorf("codegen: invalid assignment target %T", n.Target)
	}
	return nil
}
