// Package wasm emits WebAssembly text format (WAT) for a checked
// Program. It's a second backend alongside the ARM32 emitter, sharing
// the same AST.
//
// What's covered in v1:
//
//   * numbers (i32) and booleans (also i32 — 0 / non-zero)
//   * all arithmetic, comparison, logical, bitwise and shift operators
//   * functions (with up to ∞ params), recursion, direct calls
//   * `if` / `else`, `while`, `for`, `return`, `break`, `continue`
//   * `var` locals
//
// What's NOT covered yet (would each be a follow-up PR): strings,
// arrays, I/O builtins (`print`, `putchar`), function values
// (`call_indirect` + a function table). Programs using those today
// will still parse and type-check; emitting them returns an error
// here.
//
// Run the output with `wasmtime run --invoke main prog.wat` or
// convert to binary first with `wat2wasm prog.wat`.
package wasm

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// Emit returns the WAT module text for prog.
//
// Programs that use strings, `print` or `putchar` cause the emitter to
// add a memory section, a WASI `fd_write` import and a small pair of
// runtime helpers ($putchar / $print). Modules that don't touch any of
// those features stay free of imports so they can be invoked under
// minimal hosts.
func Emit(prog *ast.Program, info *checker.Info) (string, error) {
	g := &generator{
		info:       info,
		stringPool: map[string]int{},
		// Static layout reserves bytes 0..63 for runtime constants
		// (putchar buffer + iovecs + nwritten + newline byte). User
		// strings start at offset 64.
		stringOffset: 64,
	}
	g.scanForRuntimeUses(prog)

	g.line("(module")
	g.indent++

	if g.needsRuntime {
		g.emitRuntimePreamble()
	}

	for _, fn := range prog.Funcs {
		if err := g.emitFunc(fn); err != nil {
			return "", err
		}
	}

	if g.needsRuntime {
		g.emitDataSegments()
	}

	// Export every top-level function so the host can invoke any of
	// them. `main` is the conventional entry point.
	for _, fn := range prog.Funcs {
		g.linef(`(export %q (func $%s))`, fn.Name, fn.Name)
	}
	if g.needsRuntime {
		g.line(`(export "memory" (memory $mem))`)
	}
	g.indent--
	g.line(")")
	return g.out.String(), nil
}

type generator struct {
	out     strings.Builder
	info    *checker.Info
	indent  int
	loopLbl []loopLabels
	labelN  int
	current *ast.FuncDecl

	// Runtime / strings.
	needsRuntime bool
	stringPool   map[string]int    // value → pointer in linear memory
	stringEntries []stringEntry    // emission order (data segments)
	stringOffset int               // next free byte for a string entry
}

type stringEntry struct {
	offset int    // address of the 4-byte length prefix
	text   string
}

type loopLabels struct {
	breakL, continueL string
}

func (g *generator) line(s string) {
	g.out.WriteString(strings.Repeat("  ", g.indent))
	g.out.WriteString(s)
	g.out.WriteByte('\n')
}
func (g *generator) linef(format string, args ...any) {
	g.line(fmt.Sprintf(format, args...))
}
func (g *generator) freshLabel(stem string) string {
	g.labelN++
	return fmt.Sprintf("$%s%d", stem, g.labelN)
}

// ---------- function emit ----------

func (g *generator) emitFunc(fn *ast.FuncDecl) error {
	g.current = fn
	defer func() { g.current = nil }()

	header := fmt.Sprintf("(func $%s", fn.Name)
	for _, p := range fn.Params {
		typ, err := watType(p.Type)
		if err != nil {
			return fmt.Errorf("function %q: param %s: %w", fn.Name, p.Name, err)
		}
		header += fmt.Sprintf(" (param $%s %s)", p.Name, typ)
	}
	if !ast.Equal(fn.ReturnType, ast.VoidType{}) {
		typ, err := watType(fn.ReturnType)
		if err != nil {
			return fmt.Errorf("function %q: result: %w", fn.Name, err)
		}
		header += fmt.Sprintf(" (result %s)", typ)
	}
	g.line(header)
	g.indent++

	// Locals must be declared up-front in WAT, before any instruction.
	for _, v := range g.info.Locals[fn] {
		typ, err := watType(v.Type)
		if err != nil {
			return fmt.Errorf("function %q: var %s: %w", fn.Name, v.Name, err)
		}
		g.linef("(local $%s %s)", v.Name, typ)
	}

	for _, s := range fn.Body.Stmts {
		if err := g.stmt(s); err != nil {
			return err
		}
	}
	// If the body falls off the end and the function expects a result,
	// produce 0 so the WASM validator stays happy.
	if !ast.Equal(fn.ReturnType, ast.VoidType{}) {
		g.line("i32.const 0")
	}

	g.indent--
	g.line(")")
	return nil
}

// ---------- statements ----------

func (g *generator) stmt(s ast.Stmt) error {
	switch n := s.(type) {
	case *ast.Block:
		for _, ss := range n.Stmts {
			if err := g.stmt(ss); err != nil {
				return err
			}
		}
	case *ast.If:
		if err := g.expr(n.Cond); err != nil {
			return err
		}
		g.line("if")
		g.indent++
		if err := g.stmt(n.Then); err != nil {
			return err
		}
		g.indent--
		if n.Else != nil {
			g.line("else")
			g.indent++
			if err := g.stmt(n.Else); err != nil {
				return err
			}
			g.indent--
		}
		g.line("end")
	case *ast.While:
		brk := g.freshLabel("break")
		cont := g.freshLabel("loop")
		g.linef("block %s", brk)
		g.indent++
		g.linef("loop %s", cont)
		g.indent++
		if err := g.expr(n.Cond); err != nil {
			return err
		}
		g.line("i32.eqz")
		g.linef("br_if %s", brk)
		g.loopLbl = append(g.loopLbl, loopLabels{breakL: brk, continueL: cont})
		if err := g.stmt(n.Body); err != nil {
			return err
		}
		g.loopLbl = g.loopLbl[:len(g.loopLbl)-1]
		g.linef("br %s", cont)
		g.indent--
		g.line("end")
		g.indent--
		g.line("end")
	case *ast.For:
		brk := g.freshLabel("break")
		loop := g.freshLabel("loop")
		cont := g.freshLabel("cont")
		if n.Init != nil {
			if err := g.stmt(n.Init); err != nil {
				return err
			}
		}
		g.linef("block %s", brk)
		g.indent++
		g.linef("loop %s", loop)
		g.indent++
		if err := g.expr(n.Cond); err != nil {
			return err
		}
		g.line("i32.eqz")
		g.linef("br_if %s", brk)
		// Inner block lets `continue` jump out of the body so the step
		// still runs before the next iteration.
		g.linef("block %s", cont)
		g.indent++
		g.loopLbl = append(g.loopLbl, loopLabels{breakL: brk, continueL: cont})
		if err := g.stmt(n.Body); err != nil {
			return err
		}
		g.loopLbl = g.loopLbl[:len(g.loopLbl)-1]
		g.indent--
		g.line("end")
		if n.Step != nil {
			if err := g.stmt(n.Step); err != nil {
				return err
			}
		}
		g.linef("br %s", loop)
		g.indent--
		g.line("end")
		g.indent--
		g.line("end")
	case *ast.Break:
		if len(g.loopLbl) == 0 {
			return fmt.Errorf("wasm: break outside of a loop")
		}
		g.linef("br %s", g.loopLbl[len(g.loopLbl)-1].breakL)
	case *ast.Continue:
		if len(g.loopLbl) == 0 {
			return fmt.Errorf("wasm: continue outside of a loop")
		}
		g.linef("br %s", g.loopLbl[len(g.loopLbl)-1].continueL)
	case *ast.Return:
		if n.Value != nil {
			if err := g.expr(n.Value); err != nil {
				return err
			}
		}
		g.line("return")
	case *ast.Var:
		if err := g.expr(n.Init); err != nil {
			return err
		}
		g.linef("local.set $%s", n.Name)
	case *ast.ExprStmt:
		if err := g.expr(n.Expr); err != nil {
			return err
		}
		// Any value left on the stack by the expression has to be
		// dropped — WAT validation requires a balanced stack at
		// statement boundaries.
		if g.leavesValue(n.Expr) {
			g.line("drop")
		}
	default:
		return fmt.Errorf("wasm: unsupported statement %T", s)
	}
	return nil
}

// leavesValue reports whether e leaves a value on the WASM stack so
// the caller knows whether to emit `drop` after it. We use the
// checker's per-function signatures to spot void-returning calls.
func (g *generator) leavesValue(e ast.Expr) bool {
	if c, ok := e.(*ast.Call); ok {
		if id, ok := c.Callee.(*ast.Ident); ok {
			if sig, ok := g.info.FuncSigs[id.Name]; ok {
				return !ast.Equal(sig.Result, ast.VoidType{})
			}
		}
		return true
	}
	return true
}

// scanForRuntimeUses pre-walks the program and sets needsRuntime if
// any string literal, `print` call or `putchar` call appears.
func (g *generator) scanForRuntimeUses(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		if g.needsRuntime {
			return
		}
		switch x := n.(type) {
		case *ast.StringLit:
			g.needsRuntime = true
		case *ast.Call:
			if id, ok := x.Callee.(*ast.Ident); ok {
				if id.Name == "print" || id.Name == "putchar" {
					g.needsRuntime = true
					return
				}
			}
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Index:
			walk(x.Array)
			walk(x.Idx)
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// internString assigns an address to s the first time we see it and
// reuses it on repeats. The returned pointer skips the 4-byte length
// prefix, so callers can do `i32.load (sub ptr 4)` to recover length.
func (g *generator) internString(s string) int {
	if ptr, ok := g.stringPool[s]; ok {
		return ptr
	}
	g.needsRuntime = true
	off := g.stringOffset
	g.stringEntries = append(g.stringEntries, stringEntry{offset: off, text: s})
	ptr := off + 4
	g.stringPool[s] = ptr
	g.stringOffset = off + 4 + len(s)
	return ptr
}

// emitRuntimePreamble emits the WASI import, the linear memory, and
// two helper functions ($putchar, $print). They share a small block
// of static memory — see the data segments at the end of the module.
//
// Memory layout for the runtime constants (offsets in bytes):
//
//	 0..3   putchar i32 buffer (only the low byte is used)
//	 4..11  putchar iovec  { ptr=0, len=1 }   pre-initialised
//	12..15  putchar nwritten
//	16..23  print iovec[0] { ptr=string, len=L }  set per call
//	24..31  print iovec[1] { ptr=32, len=1 }      pre-initialised
//	32      newline byte 0x0A                    pre-initialised
//	36..39  print nwritten
//	64+     string data, each entry: 4-byte length prefix then bytes
func (g *generator) emitRuntimePreamble() {
	g.line(`(import "wasi_snapshot_preview1" "fd_write" (func $__wasi_fd_write (param i32 i32 i32 i32) (result i32)))`)
	g.line(`(memory $mem 1)`)

	// putchar(n)
	g.line(`(func $putchar (param $n i32)`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`local.get $n`)
	g.line(`i32.store8`)
	g.line(`i32.const 1`)  // fd = stdout
	g.line(`i32.const 4`)  // iovs = &iovec at offset 4
	g.line(`i32.const 1`)  // iovs_len = 1
	g.line(`i32.const 12`) // nwritten = &offset 12
	g.line(`call $__wasi_fd_write`)
	g.line(`drop`)
	g.indent--
	g.line(`)`)

	// print(s) — writes the string and a newline (matching the arm32
	// puts-based lowering). Uses a 2-iovec call so the output is
	// flushed in one go.
	g.line(`(func $print (param $s i32)`)
	g.indent++
	// iovec[0].ptr = s
	g.line(`i32.const 16`)
	g.line(`local.get $s`)
	g.line(`i32.store`)
	// iovec[0].len = memory[s - 4]   (length prefix)
	g.line(`i32.const 20`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`i32.store`)
	// fd_write(1, 16, 2, 36)
	g.line(`i32.const 1`)
	g.line(`i32.const 16`)
	g.line(`i32.const 2`)
	g.line(`i32.const 36`)
	g.line(`call $__wasi_fd_write`)
	g.line(`drop`)
	g.indent--
	g.line(`)`)
}

// emitDataSegments writes the static-memory initialisers: the runtime
// iovecs / newline byte plus every interned string with its 4-byte
// little-endian length prefix.
func (g *generator) emitDataSegments() {
	// putchar iovec at 4: ptr=0, len=1
	g.line(`(data (i32.const 4) "\00\00\00\00\01\00\00\00")`)
	// print iovec[1] at 24: ptr=32 (=newline byte), len=1
	g.line(`(data (i32.const 24) "\20\00\00\00\01\00\00\00")`)
	// newline byte at 32
	g.line(`(data (i32.const 32) "\0a")`)
	// strings
	for _, s := range g.stringEntries {
		g.linef(`(data (i32.const %d) "%s%s")`, s.offset, encodeI32(len(s.text)), wasmEscape(s.text))
	}
}

// encodeI32 returns a four-byte little-endian byte string in WAT data
// escape form (e.g. 13 → `\0d\00\00\00`).
func encodeI32(n int) string {
	return fmt.Sprintf(`\%02x\%02x\%02x\%02x`,
		byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
}

// wasmEscape encodes the contents of a string literal for inclusion in
// a WAT data segment: printable ASCII apart from `"` and `\` is kept
// verbatim, everything else becomes `\xx`.
func wasmEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c >= 0x20 && c <= 0x7e:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\%02x`, c)
		}
	}
	return b.String()
}

// ---------- expressions ----------

func (g *generator) expr(e ast.Expr) error {
	switch n := e.(type) {
	case *ast.NumberLit:
		g.linef("i32.const %d", n.Value)
	case *ast.BoolLit:
		if n.Value {
			g.line("i32.const 1")
		} else {
			g.line("i32.const 0")
		}
	case *ast.StringLit:
		// String literals are interned in linear memory; the value
		// pushed on the stack is the address of the first byte (after
		// the 4-byte length prefix).
		g.linef("i32.const %d", g.internString(n.Value))
	case *ast.Ident:
		g.linef("local.get $%s", n.Name)
	case *ast.Unary:
		switch n.Op {
		case "-":
			g.line("i32.const 0")
			if err := g.expr(n.Operand); err != nil {
				return err
			}
			g.line("i32.sub")
		case "!":
			if err := g.expr(n.Operand); err != nil {
				return err
			}
			g.line("i32.eqz")
		default:
			return fmt.Errorf("wasm: unsupported unary %q", n.Op)
		}
	case *ast.Binary:
		return g.binary(n)
	case *ast.Call:
		return g.call(n)
	case *ast.Assign:
		return g.assign(n)
	default:
		return fmt.Errorf("wasm: unsupported expression %T", e)
	}
	return nil
}

func (g *generator) binary(n *ast.Binary) error {
	// Short-circuit logical operators need lazy evaluation.
	switch n.Op {
	case "&&":
		// (left) (if (result i32) (then right) (else (i32.const 0)))
		if err := g.expr(n.Left); err != nil {
			return err
		}
		g.line("if (result i32)")
		g.indent++
		if err := g.expr(n.Right); err != nil {
			return err
		}
		g.indent--
		g.line("else")
		g.indent++
		g.line("i32.const 0")
		g.indent--
		g.line("end")
		return nil
	case "||":
		if err := g.expr(n.Left); err != nil {
			return err
		}
		g.line("if (result i32)")
		g.indent++
		g.line("i32.const 1")
		g.indent--
		g.line("else")
		g.indent++
		if err := g.expr(n.Right); err != nil {
			return err
		}
		g.indent--
		g.line("end")
		return nil
	}
	if err := g.expr(n.Left); err != nil {
		return err
	}
	if err := g.expr(n.Right); err != nil {
		return err
	}
	op, err := wasmBinaryOp(n.Op)
	if err != nil {
		return err
	}
	g.line(op)
	return nil
}

func wasmBinaryOp(op string) (string, error) {
	switch op {
	case "+":
		return "i32.add", nil
	case "-":
		return "i32.sub", nil
	case "*":
		return "i32.mul", nil
	case "/":
		return "i32.div_s", nil
	case "%":
		return "i32.rem_s", nil
	case "&":
		return "i32.and", nil
	case "|":
		return "i32.or", nil
	case "^":
		return "i32.xor", nil
	case "<<":
		return "i32.shl", nil
	case ">>":
		return "i32.shr_s", nil
	case "==":
		return "i32.eq", nil
	case "!=":
		return "i32.ne", nil
	case "<":
		return "i32.lt_s", nil
	case "<=":
		return "i32.le_s", nil
	case ">":
		return "i32.gt_s", nil
	case ">=":
		return "i32.ge_s", nil
	}
	return "", fmt.Errorf("wasm: unsupported binary op %q", op)
}

func (g *generator) call(n *ast.Call) error {
	id, ok := n.Callee.(*ast.Ident)
	if !ok {
		return fmt.Errorf("wasm: indirect calls not supported in v1")
	}
	for _, a := range n.Args {
		if err := g.expr(a); err != nil {
			return err
		}
	}
	g.linef("call $%s", id.Name)
	return nil
}

func (g *generator) assign(n *ast.Assign) error {
	id, ok := n.Target.(*ast.Ident)
	if !ok {
		return fmt.Errorf("wasm: only identifier assignment is supported in v1")
	}
	if err := g.expr(n.Value); err != nil {
		return err
	}
	g.linef("local.tee $%s", id.Name)
	return nil
}

// ---------- type mapping ----------

func watType(t ast.Type) (string, error) {
	switch t.(type) {
	case ast.NumberType, ast.BoolType, ast.StringType:
		// Strings are pointers into linear memory, so they're i32 too.
		return "i32", nil
	}
	return "", fmt.Errorf("wasm: type %s isn't supported by this backend yet", t)
}
