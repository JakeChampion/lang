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

// Emit returns the assembly text for prog.
func Emit(prog *ast.Program, info *checker.Info) (string, error) {
	g := &generator{
		info:        info,
		stringLabel: map[string]string{},
	}
	g.line(`.arch armv7-a`)
	g.line(`.text`)
	for _, fn := range prog.Funcs {
		if err := g.emitFunction(fn); err != nil {
			return "", err
		}
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
	slots      map[string]int          // most recent binding for each name
	stack      []map[string]int        // shadowing chain
	varSlot    map[*ast.Var]int        // var node -> offset
	arraySlot  map[*ast.ArrayLit]int   // array literal -> offset of base
	frameBytes int                     // total size, rounded to 8
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

	// Lay out the frame: each param, then each `var`, then each array
	// literal in source order.
	g.frame = &frameLayout{
		slots:     map[string]int{},
		varSlot:   map[*ast.Var]int{},
		arraySlot: map[*ast.ArrayLit]int{},
	}
	paramOffs := map[string]int{}
	off := 0
	for _, p := range fn.Params {
		off += 4
		paramOffs[p.Name] = -off
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
	// Round up to 8 bytes for AAPCS.
	if off%8 != 0 {
		off += 4
	}
	g.frame.frameBytes = off

	// Prologue.
	g.emit("push {fp, lr}")
	g.emit("mov fp, sp")
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

	g.epilogue = g.freshLabel("epi_" + fn.Name)

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
	g.emit("mov sp, fp")
	g.emit("pop {fp, lr}")
	g.emit("bx lr")
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
	}
}

// ---------- statements ----------

func (g *generator) stmt(s ast.Stmt) error {
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
		if err := g.stmt(n.Body); err != nil {
			return err
		}
		g.emit("b %s", topL)
		g.label(endL)
	case *ast.Return:
		if n.Value != nil {
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
	}
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
	case *ast.Ident:
		if off, ok := g.frame.slots[n.Name]; ok && off != 0 {
			g.emit("ldr r0, [fp, #%d]", off)
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
	// Direct call to a named function — emit `bl name`. Indirect calls
	// (function values stored in variables) are out of scope.
	id, ok := n.Callee.(*ast.Ident)
	if !ok {
		return fmt.Errorf("codegen: indirect calls not supported")
	}
	target := id.Name
	// `print(s)` lowers to libc puts, which already adds a newline.
	if target == "print" {
		target = "puts"
	}
	N := len(n.Args)

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
		g.emit("bl %s", target)
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
	g.emit("bl %s", target)
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
		off, ok := g.frame.slots[t.Name]
		if !ok || off == 0 {
			return fmt.Errorf("codegen: cannot assign to %q (no slot)", t.Name)
		}
		g.emit("str r0, [fp, #%d]", off)
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
