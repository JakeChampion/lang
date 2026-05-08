// Format produces idiomatic, multi-line lang source from a parsed
// AST. Unlike Print, which is fully parenthesised at every binary /
// unary / assignment boundary so the output round-trips through the
// parser unconditionally, Format outputs human-readable source —
// minimal parentheses (just enough to preserve operator precedence),
// two-space indentation per nesting level, one statement per line,
// and a trailing newline at end of file.
//
// Comments captured by the lexer (prog.Comments) are interleaved
// with the AST during emit:
//
//   - A comment whose source line is BEFORE the next statement's
//     line emits as a separate leading line at the statement's
//     indent level.
//   - A comment whose source line equals the just-emitted single-
//     line statement's line emits inline as `  // text`.
//   - Comments after the last statement of a block emit before the
//     closing brace at the block's indent.
//   - Comments at end-of-file (after the last declaration) emit at
//     depth zero.
//
// Blank lines aren't preserved (the lexer doesn't track them); a
// future change could either thread a "blank-line set" alongside
// the comment list or have the lexer emit blank-run markers.
//
// Format → parse → Format is byte-stable: a second pass produces
// identical output. parse → Format → parse round-trips the AST
// shape modulo blank lines.
package printer

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// Format returns idiomatic source text for prog.
func Format(prog *ast.Program) string {
	f := &formatter{comments: prog.Comments}
	written := false
	for _, sd := range prog.Structs {
		if written {
			f.b.WriteByte('\n')
		}
		f.drainLeading(sd.P.Line, 0)
		f.formatStructDecl(sd)
		written = true
	}
	for _, ed := range prog.Enums {
		if written {
			f.b.WriteByte('\n')
		}
		f.drainLeading(ed.P.Line, 0)
		f.formatEnumDecl(ed)
		written = true
	}
	for _, cd := range prog.Consts {
		if written {
			f.b.WriteByte('\n')
		}
		f.drainLeading(cd.P.Line, 0)
		f.formatConstDecl(cd)
		written = true
	}
	for _, fn := range prog.Funcs {
		if written {
			f.b.WriteByte('\n')
		}
		f.drainLeading(fn.P.Line, 0)
		f.formatFunc(fn, 0)
		written = true
	}
	// Trailing comments past the last declaration emit at depth 0.
	f.drainAll(0)
	return f.b.String()
}

const formatIndent = "  "

// formatter bundles the output buffer and the comment cursor so
// helpers can drain leading / inline trailing comments without
// each one having to thread two extra arguments.
type formatter struct {
	b        strings.Builder
	comments []ast.Comment
	ci       int // index of the next un-emitted comment in comments
}

// drainLeading emits every still-pending comment whose source line
// is strictly before `line` as its own indented line. Used before
// each statement / declaration to cover comments written above it
// in the source. Same-line comments stay queued for emitTrailing.
func (f *formatter) drainLeading(line, depth int) {
	for f.ci < len(f.comments) && f.comments[f.ci].Pos.Line < line {
		f.indent(depth)
		f.b.WriteString("//")
		f.b.WriteString(f.comments[f.ci].Text)
		f.b.WriteByte('\n')
		f.ci++
	}
}

// emitTrailing emits a comment that lives on the same source line
// as the statement we just finished writing — `putchar(70);  // F`
// style. Caller passes the statement's source line; if the next
// queued comment matches, we emit `  //` + text (no newline; the
// surrounding loop's `\n` follows).
func (f *formatter) emitTrailing(line int) {
	if f.ci < len(f.comments) && f.comments[f.ci].Pos.Line == line {
		f.b.WriteString("  //")
		f.b.WriteString(f.comments[f.ci].Text)
		f.ci++
	}
}

// drainAll flushes every remaining comment at the supplied indent.
// Used at end-of-file to catch trailing comments past the last
// declaration, and inside blocks to flush comments between the
// last statement and the closing brace.
func (f *formatter) drainAll(depth int) {
	for f.ci < len(f.comments) {
		f.indent(depth)
		f.b.WriteString("//")
		f.b.WriteString(f.comments[f.ci].Text)
		f.b.WriteByte('\n')
		f.ci++
	}
}

// drainBeforeLine flushes comments whose line is strictly less than
// `line`, at the given depth. Used inside blocks to catch comments
// that sit between the last statement and the closing brace.
func (f *formatter) drainBeforeLine(line, depth int) {
	for f.ci < len(f.comments) && f.comments[f.ci].Pos.Line < line {
		f.indent(depth)
		f.b.WriteString("//")
		f.b.WriteString(f.comments[f.ci].Text)
		f.b.WriteByte('\n')
		f.ci++
	}
}

// indent writes n levels of two-space indentation.
func (f *formatter) indent(n int) {
	for i := 0; i < n; i++ {
		f.b.WriteString(formatIndent)
	}
}

// formatConstDecl emits a top-level `const NAME[: T] = expr;` on a
// single line. The type annotation is preserved when the source had
// one and elided when it didn't, matching the parser's optional
// shape so format → parse → format stays stable.
func (f *formatter) formatConstDecl(cd *ast.ConstDecl) {
	if cd.Public {
		f.b.WriteString("pub ")
	}
	f.b.WriteString("const ")
	f.b.WriteString(cd.Name)
	if cd.Type != nil {
		f.b.WriteString(": ")
		f.b.WriteString(formatType(cd.Type))
	}
	f.b.WriteString(" = ")
	f.formatExpr(cd.Value, precLowest)
	f.b.WriteString(";\n")
}

// formatEnumDecl emits `enum Foo { Bar, Baz(T1, T2), … }` on a
// single line (one variant per `,`). The block-style multi-line
// form is a follow-up; for now this matches the parser-accepted
// shape and keeps round-trips byte-stable for short enums.
func (f *formatter) formatEnumDecl(ed *ast.EnumDecl) {
	if ed.Public {
		f.b.WriteString("pub ")
	}
	f.b.WriteString("enum ")
	f.b.WriteString(ed.Name)
	if len(ed.TypeParams) > 0 {
		f.b.WriteByte('[')
		for i, p := range ed.TypeParams {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.b.WriteString(p)
		}
		f.b.WriteByte(']')
	}
	f.b.WriteString(" { ")
	for i, v := range ed.Variants {
		if i > 0 {
			f.b.WriteString(", ")
		}
		f.b.WriteString(v.Name)
		if len(v.Payloads) > 0 {
			f.b.WriteByte('(')
			for j, p := range v.Payloads {
				if j > 0 {
					f.b.WriteString(", ")
				}
				f.b.WriteString(formatType(p))
			}
			f.b.WriteByte(')')
		}
	}
	f.b.WriteString(" }\n")
}

func (f *formatter) formatStructDecl(sd *ast.StructDecl) {
	if sd.Public {
		f.b.WriteString("pub ")
	}
	f.b.WriteString("struct ")
	f.b.WriteString(sd.Name)
	f.b.WriteString(" { ")
	for i, fld := range sd.Fields {
		if i > 0 {
			f.b.WriteString(", ")
		}
		f.b.WriteString(fld.Name)
		f.b.WriteString(": ")
		f.b.WriteString(formatType(fld.Type))
	}
	f.b.WriteString(" }\n")
}

// formatFunc emits a top-level or nested function declaration.
// Receiver clauses go between `function` and the name; the body
// uses multi-line block formatting at the supplied indent level.
func (f *formatter) formatFunc(fn *ast.FuncDecl, depth int) {
	f.indent(depth)
	if fn.Public {
		f.b.WriteString("pub ")
	}
	f.b.WriteString("function ")
	if fn.Receiver != nil {
		f.b.WriteByte('(')
		f.b.WriteString(fn.Receiver.Name)
		f.b.WriteString(": ")
		f.b.WriteString(formatType(fn.Receiver.Type))
		f.b.WriteString(") ")
	}
	f.b.WriteString(fn.Name)
	f.b.WriteByte('(')
	for i, p := range fn.Params {
		if i > 0 {
			f.b.WriteString(", ")
		}
		f.b.WriteString(p.Name)
		f.b.WriteString(": ")
		f.b.WriteString(formatType(p.Type))
	}
	f.b.WriteByte(')')
	if fn.ReturnType != nil {
		f.b.WriteString(": ")
		f.b.WriteString(formatType(fn.ReturnType))
	}
	f.b.WriteByte(' ')
	f.formatBlock(fn.Body, depth)
	f.b.WriteByte('\n')
}

// formatBlock emits `{` on the current line, then each statement on
// its own line at depth+1, then `}` indented to depth. Empty blocks
// stay one-liners. Pending comments that fall inside the block but
// before its statements get drained at the right indent.
func (f *formatter) formatBlock(blk *ast.Block, depth int) {
	if blk == nil || len(blk.Stmts) == 0 {
		// Even an empty block can host comments — but supporting
		// `{ /* comment */ }` would force a multi-line empty block
		// in cases where comments have nothing to attach to.
		// Keep the one-liner for now; standalone comments inside
		// empty blocks fall through to the parent's drain.
		f.b.WriteString("{}")
		return
	}
	f.b.WriteString("{\n")
	for _, s := range blk.Stmts {
		f.drainLeading(s.Pos().Line, depth+1)
		f.indent(depth + 1)
		f.formatStmt(s, depth+1)
		// If the statement just emitted is a single-line shape and
		// the next queued comment shares its source line, emit it
		// inline as a trailing comment.
		if isSingleLineStmt(s) {
			f.emitTrailing(s.Pos().Line)
		}
		f.b.WriteByte('\n')
	}
	// Comments past the last statement but still "inside" the
	// block — i.e. before its closing brace — emit at the inner
	// indent. We don't track the block's end position so we just
	// drain everything that's still queued and at a position past
	// the last statement; comments that belong to outer scopes
	// will exceed the block's range when drained at the outer
	// recursion level.
	f.indent(depth)
	f.b.WriteByte('}')
}

// isSingleLineStmt reports whether s emits as a single source line
// — only those are eligible for an inline trailing comment.
// Compound statements (if / while / for / switch / function /
// nested block) span multiple lines and any same-line comment is
// against their opening header rather than their body, which the
// formatter doesn't attach yet.
func isSingleLineStmt(s ast.Stmt) bool {
	switch s.(type) {
	case *ast.Return, *ast.Var, *ast.ExprStmt, *ast.Break, *ast.Continue:
		return true
	}
	return false
}

func (f *formatter) formatStmt(s ast.Stmt, depth int) {
	switch x := s.(type) {
	case *ast.Block:
		f.formatBlock(x, depth)
	case *ast.If:
		f.b.WriteString("if (")
		f.formatExpr(x.Cond, precLowest)
		f.b.WriteString(") ")
		f.formatStmt(x.Then, depth)
		if x.Else != nil {
			f.b.WriteString(" else ")
			f.formatStmt(x.Else, depth)
		}
	case *ast.IfLet:
		f.b.WriteString("if let ")
		f.b.WriteString(x.VariantName)
		if len(x.Bindings) > 0 {
			f.b.WriteByte('(')
			for j, b := range x.Bindings {
				if j > 0 {
					f.b.WriteString(", ")
				}
				f.b.WriteString(b)
			}
			f.b.WriteByte(')')
		}
		f.b.WriteString(" = ")
		f.formatExpr(x.Source, precLowest)
		f.b.WriteByte(' ')
		f.formatStmt(x.Then, depth)
		if x.Else != nil {
			f.b.WriteString(" else ")
			f.formatStmt(x.Else, depth)
		}
	case *ast.While:
		f.b.WriteString("while (")
		f.formatExpr(x.Cond, precLowest)
		f.b.WriteString(") ")
		f.formatStmt(x.Body, depth)
	case *ast.For:
		f.b.WriteString("for (")
		if x.Init != nil {
			f.formatStmt(x.Init, depth)
		} else {
			f.b.WriteByte(';')
		}
		f.b.WriteByte(' ')
		f.formatExpr(x.Cond, precLowest)
		f.b.WriteString("; ")
		if x.Step != nil {
			if es, ok := x.Step.(*ast.ExprStmt); ok {
				f.formatExpr(es.Expr, precLowest)
			} else {
				f.formatStmt(x.Step, depth)
			}
		}
		f.b.WriteString(") ")
		f.formatStmt(x.Body, depth)
	case *ast.Break:
		f.b.WriteString("break;")
	case *ast.Continue:
		f.b.WriteString("continue;")
	case *ast.Return:
		f.b.WriteString("return")
		if x.Value != nil {
			f.b.WriteByte(' ')
			f.formatExpr(x.Value, precLowest)
		}
		f.b.WriteByte(';')
	case *ast.Var:
		f.b.WriteString("var ")
		f.b.WriteString(x.Name)
		if x.Type != nil {
			f.b.WriteString(": ")
			f.b.WriteString(formatType(x.Type))
		}
		f.b.WriteString(" = ")
		f.formatExpr(x.Init, precLowest)
		f.b.WriteByte(';')
	case *ast.ExprStmt:
		f.formatExpr(x.Expr, precLowest)
		f.b.WriteByte(';')
	case *ast.Switch:
		f.b.WriteString("switch (")
		f.formatExpr(x.Tag, precLowest)
		f.b.WriteString(") {\n")
		for _, k := range x.Cases {
			f.indent(depth + 1)
			f.b.WriteString("case ")
			for i, v := range k.Values {
				if i > 0 {
					f.b.WriteString(", ")
				}
				f.formatExpr(v, precLowest)
			}
			f.b.WriteString(": ")
			f.formatBlock(k.Body, depth+1)
			f.b.WriteByte('\n')
		}
		if x.Default != nil {
			f.indent(depth + 1)
			f.b.WriteString("default: ")
			f.formatBlock(x.Default, depth+1)
			f.b.WriteByte('\n')
		}
		f.indent(depth)
		f.b.WriteByte('}')
	case *ast.Match:
		f.b.WriteString("match (")
		f.formatExpr(x.Tag, precLowest)
		f.b.WriteString(") {\n")
		for i, arm := range x.Arms {
			f.indent(depth + 1)
			if arm.IsWildcard {
				f.b.WriteByte('_')
			} else {
				f.b.WriteString(arm.VariantName)
				if len(arm.Bindings) > 0 {
					f.b.WriteByte('(')
					for j, b := range arm.Bindings {
						if j > 0 {
							f.b.WriteString(", ")
						}
						f.b.WriteString(b)
					}
					f.b.WriteByte(')')
				}
			}
			if arm.Guard != nil {
				f.b.WriteString(" when ")
				f.formatExpr(arm.Guard, precLowest)
			}
			f.b.WriteString(" => ")
			f.formatBlock(arm.Body, depth+1)
			if i < len(x.Arms)-1 {
				f.b.WriteByte(',')
			}
			f.b.WriteByte('\n')
		}
		f.indent(depth)
		f.b.WriteByte('}')
	case *ast.FuncDecl:
		f.b.WriteString("function ")
		f.b.WriteString(x.Name)
		f.b.WriteByte('(')
		for i, p := range x.Params {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.b.WriteString(p.Name)
			f.b.WriteString(": ")
			f.b.WriteString(formatType(p.Type))
		}
		f.b.WriteByte(')')
		if x.ReturnType != nil {
			f.b.WriteString(": ")
			f.b.WriteString(formatType(x.ReturnType))
		}
		f.b.WriteByte(' ')
		f.formatBlock(x.Body, depth)
	}
}

// Precedence levels mirror the parser's. Higher value binds
// tighter — formatExpr emits parentheses around an operand only
// when its outer operator binds strictly less tightly than the
// surrounding context (or, for left-associative right-children,
// less-than-or-equal).
const (
	precLowest  = 0
	precAssign  = 1  // = += -= …
	precPipe    = 2  // |>  (above assignment, below ternary)
	precTernary = 3  // ?:
	precOr      = 4  // ||
	precAnd     = 5  // &&
	precEq      = 6  // == !=
	precCmp     = 7  // < <= > >=
	precBitOr   = 8  // |
	precBitXor  = 9  // ^
	precBitAnd  = 10 // &
	precShift   = 11 // << >>
	precAdd     = 12 // + -
	precMul     = 13 // * / %
	precCast    = 14 // expr as Type
	precUnary   = 15
	precPrimary = 16
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
func (f *formatter) formatExpr(e ast.Expr, parentPrec int) {
	switch x := e.(type) {
	case *ast.CastExpr:
		needsParens := parentPrec >= precCast
		if needsParens {
			f.b.WriteByte('(')
		}
		f.formatExpr(x.Inner, precCast)
		f.b.WriteString(" as ")
		f.b.WriteString(x.Target.String())
		if needsParens {
			f.b.WriteByte(')')
		}
	case *ast.NumberLit:
		if x.Value < 0 {
			needsParens := parentPrec >= precUnary
			if needsParens {
				f.b.WriteByte('(')
			}
			fmt.Fprintf(&f.b, "-%d", -x.Value)
			if needsParens {
				f.b.WriteByte(')')
			}
		} else {
			fmt.Fprintf(&f.b, "%d", x.Value)
		}
	case *ast.BoolLit:
		if x.Value {
			f.b.WriteString("true")
		} else {
			f.b.WriteString("false")
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
				f.b.WriteByte('(')
			}
			f.b.WriteByte('-')
			f.b.WriteString(s)
			if needsParens {
				f.b.WriteByte(')')
			}
		} else {
			f.b.WriteString(s)
		}
	case *ast.StringLit:
		f.b.WriteByte('"')
		for i := 0; i < len(x.Value); i++ {
			c := x.Value[i]
			switch c {
			case '"':
				f.b.WriteString(`\"`)
			case '\\':
				f.b.WriteString(`\\`)
			case '\n':
				f.b.WriteString(`\n`)
			case '\t':
				f.b.WriteString(`\t`)
			case '\r':
				f.b.WriteString(`\r`)
			default:
				f.b.WriteByte(c)
			}
		}
		f.b.WriteByte('"')
	case *ast.Ident:
		f.b.WriteString(x.Name)
	case *ast.Unary:
		needsParens := parentPrec >= precUnary
		if needsParens {
			f.b.WriteByte('(')
		}
		f.b.WriteString(x.Op)
		f.formatExpr(x.Operand, precUnary)
		if needsParens {
			f.b.WriteByte(')')
		}
	case *ast.Binary:
		p := binaryPrec(x.Op)
		needsParens := p < parentPrec
		if needsParens {
			f.b.WriteByte('(')
		}
		f.formatExpr(x.Left, p)
		f.b.WriteByte(' ')
		f.b.WriteString(x.Op)
		f.b.WriteByte(' ')
		f.formatExpr(x.Right, p+1)
		if needsParens {
			f.b.WriteByte(')')
		}
	case *ast.Call:
		// Pipe-synthesised calls re-render as `LHS |> Callee(rest)`.
		// Args[0] is the original LHS; Args[1:] are the original
		// explicit args.
		if x.IsPipe && len(x.Args) >= 1 {
			needsParens := parentPrec > precPipe
			if needsParens {
				f.b.WriteByte('(')
			}
			f.formatExpr(x.Args[0], precPipe)
			f.b.WriteString(" |> ")
			f.formatExpr(x.Callee, precPrimary)
			if len(x.Args) > 1 {
				f.b.WriteByte('(')
				for i, a := range x.Args[1:] {
					if i > 0 {
						f.b.WriteString(", ")
					}
					f.formatExpr(a, precLowest)
				}
				f.b.WriteByte(')')
			}
			if needsParens {
				f.b.WriteByte(')')
			}
			return
		}
		f.formatExpr(x.Callee, precPrimary)
		f.b.WriteByte('(')
		for i, a := range x.Args {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.formatExpr(a, precLowest)
		}
		f.b.WriteByte(')')
	case *ast.Index:
		f.formatExpr(x.Array, precPrimary)
		f.b.WriteByte('[')
		f.formatExpr(x.Idx, precLowest)
		f.b.WriteByte(']')
	case *ast.SliceExpr:
		f.formatExpr(x.Source, precPrimary)
		f.b.WriteByte('[')
		if x.Low != nil {
			f.formatExpr(x.Low, precLowest)
		}
		f.b.WriteByte(':')
		if x.High != nil {
			f.formatExpr(x.High, precLowest)
		}
		f.b.WriteByte(']')
	case *ast.ArrayLit:
		f.b.WriteByte('[')
		for i, el := range x.Elems {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.formatExpr(el, precLowest)
		}
		f.b.WriteByte(']')
	case *ast.Assign:
		needsParens := parentPrec > precAssign
		if needsParens {
			f.b.WriteByte('(')
		}
		f.formatExpr(x.Target, precPrimary)
		f.b.WriteString(" = ")
		f.formatExpr(x.Value, precAssign)
		if needsParens {
			f.b.WriteByte(')')
		}
	case *ast.Ternary:
		needsParens := parentPrec > precTernary
		if needsParens {
			f.b.WriteByte('(')
		}
		f.formatExpr(x.Cond, precTernary+1)
		f.b.WriteString(" ? ")
		f.formatExpr(x.Then, precTernary+1)
		f.b.WriteString(" : ")
		f.formatExpr(x.Else, precTernary)
		if needsParens {
			f.b.WriteByte(')')
		}
	case *ast.StructLit:
		f.b.WriteString(x.TypeName)
		f.b.WriteString(" { ")
		for i, fld := range x.Fields {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.b.WriteString(fld.Name)
			f.b.WriteString(": ")
			f.formatExpr(fld.Value, precLowest)
		}
		f.b.WriteString(" }")
	case *ast.TupleLit:
		f.b.WriteByte('(')
		for i, e := range x.Elems {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.formatExpr(e, precLowest)
		}
		f.b.WriteByte(')')
	case *ast.FieldAccess:
		f.formatExpr(x.Target, precPrimary)
		f.b.WriteByte('.')
		f.b.WriteString(x.Field)
	}
}

// formatType returns the textual form of t. Unchanged from the
// pre-comment-retention version.
func formatType(t ast.Type) string {
	switch x := t.(type) {
	case ast.NumberType:
		// Preserve the user's source spelling when the parser
		// captured one (`number` vs `i32`). Falls back to the
		// canonical name for synthesised types (e.g. inferred
		// in the checker).
		if x.Spelling != "" {
			return x.Spelling
		}
		return x.String()
	case ast.BoolType:
		return "boolean"
	case ast.VoidType:
		return "void"
	case ast.StringType:
		return "string"
	case ast.FloatType:
		if x.Spelling != "" {
			return x.Spelling
		}
		return x.String()
	case ast.StructType:
		return x.Name
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x.Name
		}
		out := x.Name + "["
		for i, a := range x.Args {
			if i > 0 {
				out += ", "
			}
			out += formatType(a)
		}
		return out + "]"
	case ast.ParamType:
		return x.Name
	case ast.ArrayType:
		return formatType(x.Elem) + "[]"
	case ast.SliceType:
		return "[" + formatType(x.Elem) + "]"
	case ast.TupleType:
		out := "("
		for i, e := range x.Elems {
			if i > 0 {
				out += ", "
			}
			out += formatType(e)
		}
		return out + ")"
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
