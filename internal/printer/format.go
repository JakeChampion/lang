// Format produces idiomatic, multi-line lang source from a parsed
// AST. Unlike Print, which is fully parenthesised at every binary /
// unary / assignment boundary so the output round-trips through the
// parser unconditionally, Format outputs human-readable source —
// minimal parentheses (just enough to preserve operator precedence),
// two-space indentation per nesting level, one statement per line,
// and a trailing newline at end of file.
//
// Limitations:
//
//   - Comments are stripped. The lexer drops `//` line comments
//     before they reach the AST, so format-on-parse output has no
//     way to recover them. Documented in the CLI help.
//   - Blank lines between statements aren't preserved. Same reason.
//
// Format → parse → Format is idempotent: a second format pass
// produces byte-identical output. parse → Format → parse round-
// trips the AST modulo the comments-and-blank-lines limitations
// above; the test suite checks this on the examples corpus.
package printer

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// Format returns idiomatic source text for prog.
func Format(prog *ast.Program) string {
	var b strings.Builder
	written := false
	for _, sd := range prog.Structs {
		if written {
			b.WriteByte('\n')
		}
		formatStructDecl(&b, sd)
		written = true
	}
	for _, fn := range prog.Funcs {
		if written {
			b.WriteByte('\n')
		}
		formatFunc(&b, fn, 0)
		written = true
	}
	return b.String()
}

const formatIndent = "  "

// indent writes n levels of two-space indentation. Used at the start
// of every statement and declaration that lives inside a block.
func indent(b *strings.Builder, n int) {
	for i := 0; i < n; i++ {
		b.WriteString(formatIndent)
	}
}

// formatStructDecl emits `struct Name { f1: T1, f2: T2 }` on a
// single line. Multi-field structs aren't broken across lines yet —
// most struct decls are short enough that a one-liner reads fine,
// and adding a per-field-line variant is a small follow-up.
func formatStructDecl(b *strings.Builder, sd *ast.StructDecl) {
	b.WriteString("struct ")
	b.WriteString(sd.Name)
	b.WriteString(" { ")
	for i, f := range sd.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(f.Name)
		b.WriteString(": ")
		b.WriteString(formatType(f.Type))
	}
	b.WriteString(" }\n")
}

// formatFunc emits a top-level or nested function declaration.
// Receiver clauses go between `function` and the name; the body
// uses multi-line block formatting at the supplied indent level.
func formatFunc(b *strings.Builder, fn *ast.FuncDecl, depth int) {
	indent(b, depth)
	b.WriteString("function ")
	if fn.Receiver != nil {
		b.WriteByte('(')
		b.WriteString(fn.Receiver.Name)
		b.WriteString(": ")
		b.WriteString(formatType(fn.Receiver.Type))
		b.WriteString(") ")
	}
	b.WriteString(fn.Name)
	b.WriteByte('(')
	for i, p := range fn.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name)
		b.WriteString(": ")
		b.WriteString(formatType(p.Type))
	}
	b.WriteByte(')')
	if fn.ReturnType != nil {
		b.WriteString(": ")
		b.WriteString(formatType(fn.ReturnType))
	}
	b.WriteByte(' ')
	formatBlock(b, fn.Body, depth)
	b.WriteByte('\n')
}

// formatBlock emits `{` on the current line, then each statement on
// its own line at depth+1, then `}` indented to depth. Empty blocks
// stay one-liners.
func formatBlock(b *strings.Builder, blk *ast.Block, depth int) {
	if blk == nil || len(blk.Stmts) == 0 {
		b.WriteString("{}")
		return
	}
	b.WriteString("{\n")
	for _, s := range blk.Stmts {
		indent(b, depth+1)
		formatStmt(b, s, depth+1)
		b.WriteByte('\n')
	}
	indent(b, depth)
	b.WriteByte('}')
}

func formatStmt(b *strings.Builder, s ast.Stmt, depth int) {
	switch x := s.(type) {
	case *ast.Block:
		formatBlock(b, x, depth)
	case *ast.If:
		b.WriteString("if (")
		formatExpr(b, x.Cond, precLowest)
		b.WriteString(") ")
		formatStmt(b, x.Then, depth)
		if x.Else != nil {
			b.WriteString(" else ")
			formatStmt(b, x.Else, depth)
		}
	case *ast.While:
		b.WriteString("while (")
		formatExpr(b, x.Cond, precLowest)
		b.WriteString(") ")
		formatStmt(b, x.Body, depth)
	case *ast.For:
		b.WriteString("for (")
		if x.Init != nil {
			formatForInit(b, x.Init, depth)
		} else {
			b.WriteByte(';')
		}
		b.WriteByte(' ')
		formatExpr(b, x.Cond, precLowest)
		b.WriteString("; ")
		if x.Step != nil {
			formatForStep(b, x.Step, depth)
		}
		b.WriteString(") ")
		formatStmt(b, x.Body, depth)
	case *ast.Break:
		b.WriteString("break;")
	case *ast.Continue:
		b.WriteString("continue;")
	case *ast.Return:
		b.WriteString("return")
		if x.Value != nil {
			b.WriteByte(' ')
			formatExpr(b, x.Value, precLowest)
		}
		b.WriteByte(';')
	case *ast.Var:
		b.WriteString("var ")
		b.WriteString(x.Name)
		if x.Type != nil {
			b.WriteString(": ")
			b.WriteString(formatType(x.Type))
		}
		b.WriteString(" = ")
		formatExpr(b, x.Init, precLowest)
		b.WriteByte(';')
	case *ast.ExprStmt:
		formatExpr(b, x.Expr, precLowest)
		b.WriteByte(';')
	case *ast.Switch:
		b.WriteString("switch (")
		formatExpr(b, x.Tag, precLowest)
		b.WriteString(") {\n")
		for _, k := range x.Cases {
			indent(b, depth+1)
			b.WriteString("case ")
			for i, v := range k.Values {
				if i > 0 {
					b.WriteString(", ")
				}
				formatExpr(b, v, precLowest)
			}
			b.WriteString(": ")
			formatBlock(b, k.Body, depth+1)
			b.WriteByte('\n')
		}
		if x.Default != nil {
			indent(b, depth+1)
			b.WriteString("default: ")
			formatBlock(b, x.Default, depth+1)
			b.WriteByte('\n')
		}
		indent(b, depth)
		b.WriteByte('}')
	case *ast.FuncDecl:
		// Nested function — re-emit at the current depth (no
		// leading indent because the caller already wrote it).
		b.WriteString("function ")
		b.WriteString(x.Name)
		b.WriteByte('(')
		for i, p := range x.Params {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(p.Name)
			b.WriteString(": ")
			b.WriteString(formatType(p.Type))
		}
		b.WriteByte(')')
		if x.ReturnType != nil {
			b.WriteString(": ")
			b.WriteString(formatType(x.ReturnType))
		}
		b.WriteByte(' ')
		formatBlock(b, x.Body, depth)
	}
}

// formatForInit emits the init slot of a `for`. Var keeps its
// trailing `;`, ExprStmt's `;` is required by the for-header
// grammar.
func formatForInit(b *strings.Builder, s ast.Stmt, depth int) {
	formatStmt(b, s, depth)
}

// formatForStep emits the step slot of a `for`. Steps are
// syntactically expressions (no trailing `;`), but our AST stores
// them as ExprStmts; strip the semicolon when emitting.
func formatForStep(b *strings.Builder, s ast.Stmt, depth int) {
	if es, ok := s.(*ast.ExprStmt); ok {
		formatExpr(b, es.Expr, precLowest)
		return
	}
	formatStmt(b, s, depth)
}

// Precedence levels mirror the parser's. Higher value binds
// tighter — formatExpr emits parentheses around an operand only
// when its outer operator binds strictly less tightly than the
// surrounding context (or, for left-associative right-children,
// less-than-or-equal).
const (
	precLowest  = 0
	precAssign  = 1 // = += -= …
	precTernary = 2 // ?:
	precOr      = 3 // ||
	precAnd     = 4 // &&
	precEq      = 5 // == !=
	precCmp     = 6 // < <= > >=
	precBitOr   = 7 // |
	precBitXor  = 8 // ^
	precBitAnd  = 9 // &
	precShift   = 10 // << >>
	precAdd     = 11 // + -
	precMul     = 12 // * / %
	precUnary   = 13
	precPrimary = 14
)

func binaryPrec(op string) int {
	switch op {
	case "||":
		return precOr
	case "&&":
		return precAnd
	case "==", "!=":
		return precEq
	case "<", "<=", ">", ">=":
		return precCmp
	case "|":
		return precBitOr
	case "^":
		return precBitXor
	case "&":
		return precBitAnd
	case "<<", ">>":
		return precShift
	case "+", "-":
		return precAdd
	case "*", "/", "%":
		return precMul
	}
	return precLowest
}

// formatExpr emits e, wrapping in parens when the outer context
// (parentPrec) binds tighter than e's outermost operator.
func formatExpr(b *strings.Builder, e ast.Expr, parentPrec int) {
	switch x := e.(type) {
	case *ast.NumberLit:
		// Negative literals don't exist as direct tokens — the
		// parser models them as `unary -` over a positive literal,
		// so we emit them the same way back so re-parse produces
		// an identical AST.
		if x.Value < 0 {
			needsParens := parentPrec >= precUnary
			if needsParens {
				b.WriteByte('(')
			}
			fmt.Fprintf(b, "-%d", -x.Value)
			if needsParens {
				b.WriteByte(')')
			}
		} else {
			fmt.Fprintf(b, "%d", x.Value)
		}
	case *ast.BoolLit:
		if x.Value {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case *ast.FloatLit:
		v := x.Value
		neg := v < 0
		if neg {
			v = -v
		}
		s := fmt.Sprintf("%g", v)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		if neg {
			needsParens := parentPrec >= precUnary
			if needsParens {
				b.WriteByte('(')
			}
			b.WriteByte('-')
			b.WriteString(s)
			if needsParens {
				b.WriteByte(')')
			}
		} else {
			b.WriteString(s)
		}
	case *ast.StringLit:
		b.WriteByte('"')
		for i := 0; i < len(x.Value); i++ {
			c := x.Value[i]
			switch c {
			case '"':
				b.WriteString(`\"`)
			case '\\':
				b.WriteString(`\\`)
			case '\n':
				b.WriteString(`\n`)
			case '\t':
				b.WriteString(`\t`)
			case '\r':
				b.WriteString(`\r`)
			default:
				b.WriteByte(c)
			}
		}
		b.WriteByte('"')
	case *ast.Ident:
		b.WriteString(x.Name)
	case *ast.Unary:
		needsParens := parentPrec >= precUnary
		if needsParens {
			b.WriteByte('(')
		}
		b.WriteString(x.Op)
		formatExpr(b, x.Operand, precUnary)
		if needsParens {
			b.WriteByte(')')
		}
	case *ast.Binary:
		p := binaryPrec(x.Op)
		needsParens := p < parentPrec
		if needsParens {
			b.WriteByte('(')
		}
		// Left-assoc: left needs parens if its precedence is strictly
		// less; right needs parens if less-than-or-equal.
		formatExpr(b, x.Left, p)
		b.WriteByte(' ')
		b.WriteString(x.Op)
		b.WriteByte(' ')
		formatExpr(b, x.Right, p+1)
		if needsParens {
			b.WriteByte(')')
		}
	case *ast.Call:
		formatExpr(b, x.Callee, precPrimary)
		b.WriteByte('(')
		for i, a := range x.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			formatExpr(b, a, precLowest)
		}
		b.WriteByte(')')
	case *ast.Index:
		formatExpr(b, x.Array, precPrimary)
		b.WriteByte('[')
		formatExpr(b, x.Idx, precLowest)
		b.WriteByte(']')
	case *ast.ArrayLit:
		b.WriteByte('[')
		for i, el := range x.Elems {
			if i > 0 {
				b.WriteString(", ")
			}
			formatExpr(b, el, precLowest)
		}
		b.WriteByte(']')
	case *ast.Assign:
		needsParens := parentPrec > precAssign
		if needsParens {
			b.WriteByte('(')
		}
		formatExpr(b, x.Target, precPrimary)
		b.WriteString(" = ")
		formatExpr(b, x.Value, precAssign)
		if needsParens {
			b.WriteByte(')')
		}
	case *ast.Ternary:
		needsParens := parentPrec > precTernary
		if needsParens {
			b.WriteByte('(')
		}
		formatExpr(b, x.Cond, precTernary+1)
		b.WriteString(" ? ")
		formatExpr(b, x.Then, precTernary+1)
		b.WriteString(" : ")
		formatExpr(b, x.Else, precTernary)
		if needsParens {
			b.WriteByte(')')
		}
	case *ast.StructLit:
		b.WriteString(x.TypeName)
		b.WriteString(" { ")
		for i, f := range x.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(f.Name)
			b.WriteString(": ")
			formatExpr(b, f.Value, precLowest)
		}
		b.WriteString(" }")
	case *ast.FieldAccess:
		formatExpr(b, x.Target, precPrimary)
		b.WriteByte('.')
		b.WriteString(x.Field)
	}
}

// formatType is the same string mapping Print uses; types compose
// flat enough that the round-trip and pretty forms are identical.
func formatType(t ast.Type) string {
	switch x := t.(type) {
	case ast.NumberType:
		return "number"
	case ast.BoolType:
		return "boolean"
	case ast.VoidType:
		return "void"
	case ast.StringType:
		return "string"
	case ast.FloatType:
		return "float"
	case ast.StructType:
		return x.Name
	case ast.ArrayType:
		return formatType(x.Elem) + "[]"
	case *ast.FuncType:
		out := "("
		for i, p := range x.Params {
			if i > 0 {
				out += ", "
			}
			out += formatType(p)
		}
		return out + ") => " + formatType(x.Result)
	}
	return ""
}
