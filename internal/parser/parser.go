// Package parser is a hand-written recursive-descent parser that turns a
// token stream into an *ast.Program.
//
// Precedence climbs from `parseAssign` (lowest) down through logical-or,
// logical-and, equality, relational, additive, multiplicative, unary,
// and finally `parseCall` / `parsePrimary`.
package parser

import (
	"fmt"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/lexer"
)

type Error struct {
	Pos ast.Position
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("parse error at %s: %s", e.Pos, e.Msg) }

// Parse turns source into a Program, lexing along the way.
func Parse(src string) (*ast.Program, error) {
	tokens, err := lexer.Tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	return p.parseProgram()
}

type parser struct {
	tokens []lexer.Token
	i      int
}

func (p *parser) peek() lexer.Token { return p.tokens[p.i] }

func (p *parser) advance() lexer.Token {
	t := p.tokens[p.i]
	if p.i < len(p.tokens)-1 {
		p.i++
	}
	return t
}

func (p *parser) errorf(pos ast.Position, format string, args ...any) *Error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

func (p *parser) match(kind lexer.Kind, text string) bool {
	t := p.peek()
	return t.Kind == kind && (text == "" || t.Text == text)
}

func (p *parser) accept(kind lexer.Kind, text string) (lexer.Token, bool) {
	if p.match(kind, text) {
		return p.advance(), true
	}
	return lexer.Token{}, false
}

func (p *parser) expect(kind lexer.Kind, text string) (lexer.Token, error) {
	t := p.peek()
	if t.Kind != kind || (text != "" && t.Text != text) {
		want := text
		if want == "" {
			want = kind.String()
		}
		return lexer.Token{}, p.errorf(t.Pos, "expected %q, got %q", want, t.Text)
	}
	return p.advance(), nil
}

// ---------- Program / declarations ----------

func (p *parser) parseProgram() (*ast.Program, error) {
	prog := &ast.Program{}
	for !p.match(lexer.EOF, "") {
		fn, err := p.parseFunction()
		if err != nil {
			return nil, err
		}
		prog.Funcs = append(prog.Funcs, fn)
	}
	return prog, nil
}

func (p *parser) parseFunction() (*ast.FuncDecl, error) {
	kw, err := p.expect(lexer.Keyword, "function")
	if err != nil {
		return nil, err
	}
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, "("); err != nil {
		return nil, err
	}
	var params []ast.Param
	if !p.match(lexer.Punct, ")") {
		for {
			pname, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.Punct, ":"); err != nil {
				return nil, err
			}
			ptype, err := p.parseType()
			if err != nil {
				return nil, err
			}
			params = append(params, ast.Param{Name: pname.Text, Type: ptype})
			if _, ok := p.accept(lexer.Punct, ","); !ok {
				break
			}
		}
	}
	if _, err := p.expect(lexer.Punct, ")"); err != nil {
		return nil, err
	}

	var ret ast.Type = ast.VoidType{}
	if _, ok := p.accept(lexer.Punct, ":"); ok {
		t, err := p.parseType()
		if err != nil {
			return nil, err
		}
		ret = t
	}

	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.FuncDecl{P: kw.Pos, Name: name.Text, Params: params, ReturnType: ret, Body: body}, nil
}

func (p *parser) parseType() (ast.Type, error) {
	t := p.peek()
	var base ast.Type
	switch {
	case t.Kind == lexer.Keyword && t.Text == "number":
		p.advance()
		base = ast.NumberType{}
	case t.Kind == lexer.Keyword && t.Text == "boolean":
		p.advance()
		base = ast.BoolType{}
	case t.Kind == lexer.Keyword && t.Text == "void":
		p.advance()
		base = ast.VoidType{}
	default:
		return nil, p.errorf(t.Pos, "expected type, got %q", t.Text)
	}
	// Trailing `[]` makes it an array type, repeatable.
	for {
		if _, ok := p.accept(lexer.Punct, "["); !ok {
			return base, nil
		}
		if _, err := p.expect(lexer.Punct, "]"); err != nil {
			return nil, err
		}
		base = ast.ArrayType{Elem: base}
	}
}

// ---------- Statements ----------

func (p *parser) parseBlock() (*ast.Block, error) {
	open, err := p.expect(lexer.Punct, "{")
	if err != nil {
		return nil, err
	}
	block := &ast.Block{P: open.Pos}
	for !p.match(lexer.Punct, "}") && !p.match(lexer.EOF, "") {
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		block.Stmts = append(block.Stmts, s)
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	return block, nil
}

func (p *parser) parseStmt() (ast.Stmt, error) {
	t := p.peek()
	if t.Kind == lexer.Punct && t.Text == "{" {
		return p.parseBlock()
	}
	if t.Kind == lexer.Keyword {
		switch t.Text {
		case "if":
			return p.parseIf()
		case "while":
			return p.parseWhile()
		case "return":
			return p.parseReturn()
		case "var":
			return p.parseVar()
		}
	}
	// expression statement
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.ExprStmt{P: e.Pos(), Expr: e}, nil
}

func (p *parser) parseIf() (ast.Stmt, error) {
	kw := p.advance()
	if _, err := p.expect(lexer.Punct, "("); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ")"); err != nil {
		return nil, err
	}
	then, err := p.parseStmt()
	if err != nil {
		return nil, err
	}
	var els ast.Stmt
	if _, ok := p.accept(lexer.Keyword, "else"); ok {
		els, err = p.parseStmt()
		if err != nil {
			return nil, err
		}
	}
	return &ast.If{P: kw.Pos, Cond: cond, Then: then, Else: els}, nil
}

func (p *parser) parseWhile() (ast.Stmt, error) {
	kw := p.advance()
	if _, err := p.expect(lexer.Punct, "("); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ")"); err != nil {
		return nil, err
	}
	body, err := p.parseStmt()
	if err != nil {
		return nil, err
	}
	return &ast.While{P: kw.Pos, Cond: cond, Body: body}, nil
}

func (p *parser) parseReturn() (ast.Stmt, error) {
	kw := p.advance()
	var val ast.Expr
	if !p.match(lexer.Punct, ";") {
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		val = v
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.Return{P: kw.Pos, Value: val}, nil
}

func (p *parser) parseVar() (ast.Stmt, error) {
	kw := p.advance()
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	var typ ast.Type
	if _, ok := p.accept(lexer.Punct, ":"); ok {
		t, err := p.parseType()
		if err != nil {
			return nil, err
		}
		typ = t
	}
	if _, err := p.expect(lexer.Punct, "="); err != nil {
		return nil, err
	}
	init, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.Var{P: kw.Pos, Name: name.Text, Type: typ, Init: init}, nil
}

// ---------- Expressions ----------

func (p *parser) parseExpr() (ast.Expr, error) { return p.parseAssign() }

func (p *parser) parseAssign() (ast.Expr, error) {
	left, err := p.parseLogicOr()
	if err != nil {
		return nil, err
	}
	if eq, ok := p.accept(lexer.Punct, "="); ok {
		rhs, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		switch left.(type) {
		case *ast.Ident, *ast.Index:
			// fine
		default:
			return nil, p.errorf(eq.Pos, "left-hand side of assignment is not assignable")
		}
		return &ast.Assign{P: eq.Pos, Target: left, Value: rhs}, nil
	}
	return left, nil
}

func (p *parser) parseBinaryLeft(next func() (ast.Expr, error), ops ...string) (ast.Expr, error) {
	left, err := next()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		matched := ""
		for _, op := range ops {
			if t.Kind == lexer.Punct && t.Text == op {
				matched = op
				break
			}
		}
		if matched == "" {
			return left, nil
		}
		opTok := p.advance()
		right, err := next()
		if err != nil {
			return nil, err
		}
		left = &ast.Binary{P: opTok.Pos, Op: matched, Left: left, Right: right}
	}
}

func (p *parser) parseLogicOr() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseLogicAnd, "||")
}
func (p *parser) parseLogicAnd() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseEquality, "&&")
}
func (p *parser) parseEquality() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseRelational, "==", "!=")
}
func (p *parser) parseRelational() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseAdditive, "<", ">", "<=", ">=")
}
func (p *parser) parseAdditive() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseMultiplicative, "+", "-")
}
func (p *parser) parseMultiplicative() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseUnary, "*", "/")
}

func (p *parser) parseUnary() (ast.Expr, error) {
	if t := p.peek(); t.Kind == lexer.Punct && (t.Text == "-" || t.Text == "!") {
		op := p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &ast.Unary{P: op.Pos, Op: op.Text, Operand: operand}, nil
	}
	return p.parseCall()
}

func (p *parser) parseCall() (ast.Expr, error) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.match(lexer.Punct, "("):
			open := p.advance()
			var args []ast.Expr
			if !p.match(lexer.Punct, ")") {
				for {
					a, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if _, ok := p.accept(lexer.Punct, ","); !ok {
						break
					}
				}
			}
			if _, err := p.expect(lexer.Punct, ")"); err != nil {
				return nil, err
			}
			expr = &ast.Call{P: open.Pos, Callee: expr, Args: args}
		case p.match(lexer.Punct, "["):
			open := p.advance()
			idx, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.Punct, "]"); err != nil {
				return nil, err
			}
			expr = &ast.Index{P: open.Pos, Array: expr, Idx: idx}
		default:
			return expr, nil
		}
	}
}

func (p *parser) parsePrimary() (ast.Expr, error) {
	t := p.peek()
	switch t.Kind {
	case lexer.Number:
		p.advance()
		var n int64
		for _, c := range t.Text {
			n = n*10 + int64(c-'0')
		}
		return &ast.NumberLit{P: t.Pos, Value: n}, nil
	case lexer.Keyword:
		switch t.Text {
		case "true":
			p.advance()
			return &ast.BoolLit{P: t.Pos, Value: true}, nil
		case "false":
			p.advance()
			return &ast.BoolLit{P: t.Pos, Value: false}, nil
		}
	case lexer.Ident:
		p.advance()
		return &ast.Ident{P: t.Pos, Name: t.Text}, nil
	case lexer.Punct:
		switch t.Text {
		case "(":
			p.advance()
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.Punct, ")"); err != nil {
				return nil, err
			}
			return e, nil
		case "[":
			open := p.advance()
			var elems []ast.Expr
			if !p.match(lexer.Punct, "]") {
				for {
					e, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					elems = append(elems, e)
					if _, ok := p.accept(lexer.Punct, ","); !ok {
						break
					}
				}
			}
			if _, err := p.expect(lexer.Punct, "]"); err != nil {
				return nil, err
			}
			return &ast.ArrayLit{P: open.Pos, Elems: elems}, nil
		}
	}
	return nil, p.errorf(t.Pos, "unexpected token %q", t.Text)
}
