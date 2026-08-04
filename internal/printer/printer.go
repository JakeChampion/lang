// Package printer turns an *ast.Program back into source text. The
// output is fully parenthesised at every binary, unary, and assignment
// boundary so it round-trips through the parser without depending on
// precedence rules.
package printer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// Print serialises prog as lang source.
func Print(prog *ast.Program) string {
	var b strings.Builder
	for _, sd := range prog.Structs {
		printStructDecl(&b, sd)
	}
	for _, cd := range prog.Consts {
		printConstDecl(&b, cd)
	}
	for _, fn := range prog.Funcs {
		printFunc(&b, fn)
	}
	return b.String()
}

func printConstDecl(b *strings.Builder, cd *ast.ConstDecl) {
	if cd.PackageScoped {
		b.WriteString("pub(package) ")
	} else if cd.Public {
		b.WriteString("pub ")
	}
	b.WriteString("const ")
	b.WriteString(cd.Name)
	if cd.Type != nil {
		b.WriteString(": ")
		b.WriteString(printType(cd.Type))
	}
	b.WriteString(" = ")
	printExpr(b, cd.Value)
	b.WriteString(";\n")
}

func printStructDecl(b *strings.Builder, sd *ast.StructDecl) {
	if len(sd.Derives) > 0 {
		b.WriteString("@derive(")
		for i, d := range sd.Derives {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(d)
		}
		b.WriteString(")\n")
	}
	if sd.PackageScoped {
		b.WriteString("pub(package) ")
	} else if sd.Public {
		b.WriteString("pub ")
	}
	b.WriteString("struct ")
	b.WriteString(sd.Name)
	b.WriteString(" { ")
	for i, f := range sd.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(f.Name)
		b.WriteString(": ")
		b.WriteString(printType(f.Type))
	}
	b.WriteString(" }\n")
}

func printFunc(b *strings.Builder, fn *ast.FuncDecl) {
	// A body-less `@import` extern (bring-your-own WIT, P4) renders as the
	// attribute on its own line; the signature ends with `;` (no block).
	if fn.ImportIface != "" {
		b.WriteString("@import(\"")
		b.WriteString(fn.ImportIface)
		b.WriteString("\", \"")
		b.WriteString(fn.ImportWITName)
		b.WriteString("\")\n")
	}
	if fn.PackageScoped {
		b.WriteString("pub(package) ")
	} else if fn.Public {
		b.WriteString("pub ")
	}
	// Contextual modifiers (`fip` / `fbip` / graded / `async`) carry checked
	// semantics — re-emit them (mirrors formatFunc).
	if fn.Fip || fn.Fbip {
		if fn.Fbip {
			b.WriteString("fbip")
		} else {
			b.WriteString("fip")
		}
		if fn.FipAllowance > 0 {
			b.WriteByte('(')
			b.WriteString(strconv.Itoa(fn.FipAllowance))
			b.WriteByte(')')
		}
		b.WriteByte(' ')
	}
	if fn.Async {
		b.WriteString("async ")
	}
	b.WriteString("function ")
	if fn.Receiver != nil {
		b.WriteByte('(')
		b.WriteString(fn.Receiver.Name)
		b.WriteString(": ")
		b.WriteString(printType(fn.Receiver.Type))
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
		b.WriteString(printType(p.Type))
	}
	b.WriteByte(')')
	if fn.ReturnType != nil {
		b.WriteString(": ")
		b.WriteString(printType(fn.ReturnType))
	}
	if fn.ImportIface != "" {
		b.WriteString(";\n")
		return
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
	case *ast.LetElse:
		b.WriteString("let ")
		b.WriteString(x.VariantName)
		if len(x.Bindings) > 0 {
			b.WriteByte('(')
			for j, n := range x.Bindings {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(n)
			}
			b.WriteByte(')')
		}
		b.WriteString(" = ")
		printExpr(b, x.Source)
		b.WriteString(" else ")
		printStmt(b, x.Else)
		b.WriteByte(';')
	case *ast.IfLet:
		b.WriteString("if let ")
		b.WriteString(x.VariantName)
		if len(x.Bindings) > 0 {
			b.WriteByte('(')
			for j, n := range x.Bindings {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(n)
			}
			b.WriteByte(')')
		}
		b.WriteString(" = ")
		printExpr(b, x.Source)
		b.WriteByte(' ')
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
	case *ast.Loop:
		b.WriteString("loop ")
		printStmt(b, x.Body)
	case *ast.For:
		b.WriteString("for (")
		if x.Init != nil {
			printForInit(b, x.Init)
		} else {
			b.WriteByte(';')
		}
		b.WriteByte(' ')
		printExpr(b, x.Cond)
		b.WriteString("; ")
		if x.Step != nil {
			printForStep(b, x.Step)
		}
		b.WriteString(") ")
		printStmt(b, x.Body)
	case *ast.Break:
		b.WriteString("break;")
	case *ast.Continue:
		b.WriteString("continue;")
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
	case *ast.Destructure:
		b.WriteString("let (")
		for i, n := range x.Names {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(n)
		}
		b.WriteString(") = ")
		printExpr(b, x.Init)
		b.WriteByte(';')
	case *ast.ExprStmt:
		printExpr(b, x.Expr)
		b.WriteByte(';')
	case *ast.FuncDecl:
		// Nested function declaration. Re-emits in the same shape
		// the parser accepts: `function name(...): T { ... }` as a
		// statement.
		b.WriteString("function ")
		b.WriteString(x.Name)
		b.WriteByte('(')
		for i, p := range x.Params {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(p.Name)
			b.WriteString(": ")
			b.WriteString(printType(p.Type))
		}
		b.WriteByte(')')
		if x.ReturnType != nil {
			b.WriteString(": ")
			b.WriteString(printType(x.ReturnType))
		}
		b.WriteByte(' ')
		printBlock(b, x.Body)
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
	case *ast.UnitLit:
		b.WriteString("()")
	case *ast.BoolLit:
		if x.Value {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case *ast.FloatLit:
		// Float literals never appear with a leading `-` in source — the
		// parser models negation as `unary -` over a positive literal.
		// Emit at least one fractional digit so the lexer treats this
		// as a Float on the way back in.
		v := x.Value
		neg := false
		if v < 0 {
			neg = true
			v = -v
		}
		s := fmt.Sprintf("%g", v)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		if neg {
			b.WriteString("(- ")
			b.WriteString(s)
			b.WriteByte(')')
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
	case *ast.CastExpr:
		b.WriteString("(as ")
		b.WriteString(x.Target.String())
		b.WriteByte(' ')
		printExpr(b, x.Inner)
		b.WriteByte(')')
	case *ast.DowncastExpr:
		b.WriteString("(as? ")
		b.WriteString(x.Target.String())
		b.WriteByte(' ')
		printExpr(b, x.Inner)
		b.WriteByte(')')
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
	case *ast.SliceExpr:
		printExpr(b, x.Source)
		b.WriteByte('[')
		if x.Low != nil {
			printExpr(b, x.Low)
		}
		b.WriteByte(':')
		if x.High != nil {
			printExpr(b, x.High)
		}
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
	case *ast.IfExpr:
		b.WriteString("if (")
		printExpr(b, x.Cond)
		b.WriteString(") { ")
		printExpr(b, x.Then)
		b.WriteString(" } else { ")
		printExpr(b, x.Else)
		b.WriteString(" }")
	case *ast.TryOp:
		printExpr(b, x.Inner)
		b.WriteByte('?')
	case *ast.MatchExpr:
		b.WriteString("match (")
		printExpr(b, x.Tag)
		b.WriteString(") { ")
		for i, arm := range x.Arms {
			if i > 0 {
				b.WriteString(", ")
			}
			if arm.IsWildcard {
				b.WriteByte('_')
			} else {
				if arm.VariantModule != "" {
					b.WriteString(arm.VariantModule)
					b.WriteByte('.')
				}
				b.WriteString(arm.VariantName)
				if len(arm.Bindings) > 0 {
					b.WriteByte('(')
					for j, bind := range arm.Bindings {
						if j > 0 {
							b.WriteString(", ")
						}
						b.WriteString(bind)
					}
					b.WriteByte(')')
				}
			}
			if arm.Guard != nil {
				b.WriteString(" when ")
				printExpr(b, arm.Guard)
			}
			b.WriteString(" => ")
			printExpr(b, arm.Body)
		}
		b.WriteString(" }")
	case *ast.BlockExpr:
		b.WriteString("{ ")
		for _, st := range x.Stmts {
			printStmt(b, st)
			b.WriteByte(' ')
		}
		if x.Tail != nil {
			printExpr(b, x.Tail)
			b.WriteByte(' ')
		}
		b.WriteByte('}')
	case *ast.StructLit:
		b.WriteString(x.TypeName)
		b.WriteString(" { ")
		if x.Base != nil {
			b.WriteString("...")
			printExpr(b, x.Base)
			if len(x.Fields) > 0 {
				b.WriteString(", ")
			}
		}
		for i, f := range x.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(f.Name)
			b.WriteString(": ")
			printExpr(b, f.Value)
		}
		b.WriteString(" }")
	case *ast.TupleLit:
		b.WriteByte('(')
		for i, e := range x.Elems {
			if i > 0 {
				b.WriteString(", ")
			}
			printExpr(b, e)
		}
		b.WriteByte(')')
	case *ast.FieldAccess:
		printExpr(b, x.Target)
		if x.PathSep {
			b.WriteString("::")
		} else {
			b.WriteByte('.')
		}
		b.WriteString(x.Field)
	}
}

// printForInit emits the init slot of a `for`. A Var keeps its trailing
// `;`; an ExprStmt's `;` is printed as well so the for-header parses
// the same way on the way back in.
func printForInit(b *strings.Builder, s ast.Stmt) {
	printStmt(b, s)
}

// printForStep emits the step slot of a `for`. Steps are syntactically
// expressions (no trailing `;`), but our AST stores them as ExprStmts;
// strip the semicolon when emitting.
func printForStep(b *strings.Builder, s ast.Stmt) {
	if es, ok := s.(*ast.ExprStmt); ok {
		printExpr(b, es.Expr)
		return
	}
	printStmt(b, s)
}

func printType(t ast.Type) string {
	switch x := t.(type) {
	case ast.NumberType:
		// Match the canonical i32 / u32 / i64 / u64 / u8
		// spellings the parser now requires; the historical
		// `number` alias was removed.
		if x.Spelling != "" {
			return x.Spelling
		}
		if !x.IsSigned() {
			switch x.NormalWidth() {
			case 8:
				return "u8"
			case 32:
				return "u32"
			case 64:
				return "u64"
			}
		}
		if x.NormalWidth() == 64 {
			return "i64"
		}
		return "i32"
	case ast.BoolType:
		return "boolean"
	case ast.VoidType:
		return "void"
	case ast.StringType:
		return "string"
	case ast.CharType:
		return "char"
	case ast.StrType:
		// The borrowed-string view type (#4813).
		return "str"
	case ast.FloatType:
		if x.Spelling != "" {
			return x.Spelling
		}
		if x.NormalWidth() == 64 {
			return "f64"
		}
		return "f32"
	case ast.StructType:
		return x.Name
	case ast.ArrayType:
		return printType(x.Elem) + "[]"
	case *ast.FuncType:
		out := "("
		for i, p := range x.Params {
			if i > 0 {
				out += ", "
			}
			out += printType(p)
		}
		return out + ") => " + printType(x.Result)
	}
	return ""
}
