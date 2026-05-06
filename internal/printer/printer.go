// Package printer turns an *ast.Program back into source text. The
// output is fully parenthesised at every binary, unary, and assignment
// boundary so it round-trips through the parser without depending on
// precedence rules.
package printer

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// Print serialises prog as lang source.
func Print(prog *ast.Program) string {
	var b strings.Builder
	for _, fn := range prog.Funcs {
		printFunc(&b, fn)
	}
	return b.String()
}

func printFunc(b *strings.Builder, fn *ast.FuncDecl) {
	b.WriteString("function ")
	b.WriteString(fn.Name)
	b.WriteByte('(')
	for i, p := range fn.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name)
		b.WriteString(": ")
		b.WriteString(printType(p.Type))
	}
	b.WriteByte(')')
	if fn.ReturnType != nil {
		b.WriteString(": ")
		b.WriteString(printType(fn.ReturnType))
	}
	b.WriteByte(' ')
	printBlock(b, fn.Body)
	b.WriteByte('\n')
}

func printBlock(b *strings.Builder, blk *ast.Block) {
	b.WriteString("{ ")
	for _, s := range blk.Stmts {
		printStmt(b, s)
		b.WriteByte(' ')
	}
	b.WriteByte('}')
}

func printStmt(b *strings.Builder, s ast.Stmt) {
	switch x := s.(type) {
	case *ast.Block:
		printBlock(b, x)
	case *ast.If:
		b.WriteString("if (")
		printExpr(b, x.Cond)
		b.WriteString(") ")
		printStmt(b, x.Then)
		if x.Else != nil {
			b.WriteString(" else ")
			printStmt(b, x.Else)
		}
	case *ast.While:
		b.WriteString("while (")
		printExpr(b, x.Cond)
		b.WriteString(") ")
		printStmt(b, x.Body)
	case *ast.Return:
		b.WriteString("return")
		if x.Value != nil {
			b.WriteByte(' ')
			printExpr(b, x.Value)
		}
		b.WriteByte(';')
	case *ast.Var:
		b.WriteString("var ")
		b.WriteString(x.Name)
		if x.Type != nil {
			b.WriteString(": ")
			b.WriteString(printType(x.Type))
		}
		b.WriteString(" = ")
		printExpr(b, x.Init)
		b.WriteByte(';')
	case *ast.ExprStmt:
		printExpr(b, x.Expr)
		b.WriteByte(';')
	}
}

func printExpr(b *strings.Builder, e ast.Expr) {
	switch x := e.(type) {
	case *ast.NumberLit:
		// Negative literals don't exist as direct tokens in source — the
		// parser models them as `unary -` + a positive literal. Emit the
		// same shape on the way out so re-parsing produces an identical
		// tree (modulo the negative-int64-min edge, which can't be
		// produced by the parser anyway).
		if x.Value < 0 {
			b.WriteString("(- ")
			fmt.Fprintf(b, "%d", -x.Value)
			b.WriteByte(')')
		} else {
			fmt.Fprintf(b, "%d", x.Value)
		}
	case *ast.BoolLit:
		if x.Value {
			b.WriteString("true")
		} else {
			b.WriteString("false")
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
		b.WriteByte('(')
		b.WriteString(x.Op)
		b.WriteByte(' ')
		printExpr(b, x.Operand)
		b.WriteByte(')')
	case *ast.Binary:
		b.WriteByte('(')
		printExpr(b, x.Left)
		b.WriteByte(' ')
		b.WriteString(x.Op)
		b.WriteByte(' ')
		printExpr(b, x.Right)
		b.WriteByte(')')
	case *ast.Call:
		printExpr(b, x.Callee)
		b.WriteByte('(')
		for i, a := range x.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			printExpr(b, a)
		}
		b.WriteByte(')')
	case *ast.Index:
		printExpr(b, x.Array)
		b.WriteByte('[')
		printExpr(b, x.Idx)
		b.WriteByte(']')
	case *ast.ArrayLit:
		b.WriteByte('[')
		for i, el := range x.Elems {
			if i > 0 {
				b.WriteString(", ")
			}
			printExpr(b, el)
		}
		b.WriteByte(']')
	case *ast.Assign:
		b.WriteByte('(')
		printExpr(b, x.Target)
		b.WriteString(" = ")
		printExpr(b, x.Value)
		b.WriteByte(')')
	}
}

func printType(t ast.Type) string {
	switch x := t.(type) {
	case ast.NumberType:
		return "number"
	case ast.BoolType:
		return "boolean"
	case ast.VoidType:
		return "void"
	case ast.StringType:
		return "string"
	case ast.ArrayType:
		return printType(x.Elem) + "[]"
	}
	return ""
}
