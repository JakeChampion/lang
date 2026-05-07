// Package parser is a hand-written recursive-descent parser that turns a
// token stream into an *ast.Program.
//
// Precedence climbs from `parseAssign` (lowest) down through logical-or,
// logical-and, equality, relational, additive, multiplicative, unary,
// and finally `parseCall` / `parsePrimary`.
package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/lexer"
)

type Error struct {
	Pos ast.Position
	Msg string
}

func (e *Error) Error() string         { return fmt.Sprintf("parse error at %s: %s", e.Pos, e.Msg) }
func (e *Error) Position() ast.Position { return e.Pos }

// Parse turns source into a Program, lexing along the way. The parser
// recovers from per-statement and per-function errors and continues so
// it can report many problems in one pass; the returned error (if any)
// is a diag.Errors of every problem found.
//
// Comments captured by the lexer ride along on prog.Comments — the
// parser doesn't otherwise consume them, leaving the formatter (or
// any other tooling pass) free to walk them in source order.
func Parse(src string) (*ast.Program, error) {
	tokens, comments, err := lexer.Tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	prog := p.parseProgram()
	prog.Comments = comments
	if len(p.errors) > 0 {
		return prog, diag.Errors(p.errors)
	}
	return prog, nil
}

type parser struct {
	tokens []lexer.Token
	i      int
	errors []error
	// foreachN counts how many `for IDENT in expr` desugars we've
	// emitted in this Parse so synthetic slot names stay unique
	// across nested foreach loops.
	foreachN int
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

func (p *parser) parseProgram() *ast.Program {
	prog := &ast.Program{}
	for !p.match(lexer.EOF, "") {
		// Snapshot the input position so we can guarantee progress
		// after a failed declaration: if recovery would leave us at
		// the same token, advance once to break the loop.
		before := p.i
		if p.match(lexer.Keyword, "import") {
			imp, err := p.parseImport()
			if err != nil {
				p.errors = append(p.errors, err)
				p.syncToTopLevel()
				if p.i == before {
					p.advance()
				}
				continue
			}
			if imp != nil {
				prog.Imports = append(prog.Imports, imp)
			}
			continue
		}
		// `pub` is an optional prefix on function or struct decls
		// at the top level. Track it and consume; the `function` /
		// `struct` parser stays unaware of visibility — we stamp
		// the Public flag after the decl is built. A bare `pub`
		// without a following decl is a parse error.
		isPub := false
		if p.match(lexer.Keyword, "pub") {
			pubTok := p.advance()
			if !p.match(lexer.Keyword, "function") && !p.match(lexer.Keyword, "struct") {
				p.errors = append(p.errors, p.errorf(pubTok.Pos,
					"`pub` must be followed by `function` or `struct`"))
				p.syncToTopLevel()
				if p.i == before {
					p.advance()
				}
				continue
			}
			isPub = true
		}
		if p.match(lexer.Keyword, "struct") {
			sd, err := p.parseStructDecl()
			if err != nil {
				p.errors = append(p.errors, err)
				p.syncToTopLevel()
				if p.i == before {
					p.advance()
				}
				continue
			}
			if sd != nil {
				sd.Public = isPub
				prog.Structs = append(prog.Structs, sd)
			}
			continue
		}
		fn, err := p.parseFunction()
		if err != nil {
			p.errors = append(p.errors, err)
			p.syncToTopLevel()
			if p.i == before {
				p.advance()
			}
			continue
		}
		if fn != nil {
			fn.Public = isPub
			prog.Funcs = append(prog.Funcs, fn)
		}
	}
	return prog
}

// parseImport parses `import "<path>";` and returns the
// declaration. The local name (used as the prefix in qualified
// calls like `mod.fn(args)`) is derived from the path's basename
// without the `.lang` extension. The path is otherwise opaque to
// the parser — relative-path resolution lives in the driver.
func (p *parser) parseImport() (*ast.Import, error) {
	kw := p.advance() // import
	pathTok, err := p.expect(lexer.String, "")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.Import{
		P:         kw.Pos,
		Path:      pathTok.Text,
		LocalName: importLocalName(pathTok.Text),
	}, nil
}

// importLocalName returns the binding name a qualified call uses
// for an imported module — `import "./math/vec";` → `vec`. Drops
// any directory prefix and a trailing `.lang` extension.
func importLocalName(path string) string {
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if strings.HasSuffix(base, ".lang") {
		base = base[:len(base)-len(".lang")]
	}
	return base
}

// syncToTopLevel advances tokens until the next `function`, `struct`,
// or `import` keyword (or EOF), so a malformed top-level declaration
// doesn't poison the rest of the file.
func (p *parser) syncToTopLevel() {
	for !p.match(lexer.EOF, "") {
		if p.match(lexer.Keyword, "function") ||
			p.match(lexer.Keyword, "struct") ||
			p.match(lexer.Keyword, "import") ||
			p.match(lexer.Keyword, "pub") {
			return
		}
		p.advance()
	}
}

// syncToStmt advances past a `;` or stops at `}` / EOF — the natural
// boundaries between statements. Used by parseBlock after a per-stmt
// error so the next statement still gets parsed.
func (p *parser) syncToStmt() {
	for !p.match(lexer.EOF, "") {
		if p.match(lexer.Punct, ";") {
			p.advance()
			return
		}
		if p.match(lexer.Punct, "}") {
			return
		}
		p.advance()
	}
}

func (p *parser) parseFunction() (*ast.FuncDecl, error) {
	kw, err := p.expect(lexer.Keyword, "function")
	if err != nil {
		return nil, err
	}
	// Optional method receiver: `function (p: Point) name(...)`.
	// We distinguish receiver from regular params by the lookahead
	// pattern `( ident : type ) ident (` — a single typed binding in
	// parens followed by a method name then another `(`.
	var receiver *ast.Param
	if p.match(lexer.Punct, "(") && p.looksLikeReceiverClause() {
		p.advance() // (
		rname, err := p.expect(lexer.Ident, "")
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.Punct, ":"); err != nil {
			return nil, err
		}
		rtype, err := p.parseType()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.Punct, ")"); err != nil {
			return nil, err
		}
		receiver = &ast.Param{Name: rname.Text, Type: rtype}
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
	return &ast.FuncDecl{
		P:          kw.Pos,
		Name:       name.Text,
		Params:     params,
		ReturnType: ret,
		Body:       body,
		Receiver:   receiver,
	}, nil
}

// looksLikeReceiverClause peeks past the current `(` to see whether
// it's the start of a method's receiver `(name: T) methodName(`. We
// use a tightly-bounded scan rather than committing tokens because
// any false positive on a regular function with no name would be a
// regression the parser couldn't recover from.
func (p *parser) looksLikeReceiverClause() bool {
	// Save and restore the position; the caller advances past `(`
	// once we confirm.
	start := p.i
	if !p.match(lexer.Punct, "(") {
		return false
	}
	p.i++ // skip (
	if p.peek().Kind != lexer.Ident {
		p.i = start
		return false
	}
	p.i++ // ident
	if !(p.peek().Kind == lexer.Punct && p.peek().Text == ":") {
		p.i = start
		return false
	}
	// Skip the type — accept any sequence of tokens until matching `)`.
	depth := 0
	for p.i < len(p.tokens) {
		t := p.tokens[p.i]
		if t.Kind == lexer.Punct && t.Text == "(" {
			depth++
		} else if t.Kind == lexer.Punct && t.Text == ")" {
			if depth == 0 {
				p.i++ // consume the closing )
				break
			}
			depth--
		}
		p.i++
	}
	// After the receiver `)`, we expect `name(`.
	ok := p.peek().Kind == lexer.Ident
	if ok {
		p.i++
		ok = p.match(lexer.Punct, "(")
	}
	p.i = start
	return ok
}

// parseLocalFunction parses a `function name(...) { ... }` appearing
// inside another function's body. It produces the same FuncDecl as a
// top-level function declaration, but marked IsLocal so the checker
// runs capture analysis and the codegen pass closure-converts it.
func (p *parser) parseLocalFunction() (ast.Stmt, error) {
	fn, err := p.parseFunction()
	if err != nil {
		return nil, err
	}
	fn.IsLocal = true
	return fn, nil
}

// parseStructDecl parses
//
//	struct Foo { x: number, y: number }
//
// Trailing commas are allowed; field names must be unique within the
// declaration (the checker enforces that).
func (p *parser) parseStructDecl() (*ast.StructDecl, error) {
	kw, err := p.expect(lexer.Keyword, "struct")
	if err != nil {
		return nil, err
	}
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, err
	}
	var fields []ast.Param
	if !p.match(lexer.Punct, "}") {
		for {
			fname, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.Punct, ":"); err != nil {
				return nil, err
			}
			ft, err := p.parseType()
			if err != nil {
				return nil, err
			}
			fields = append(fields, ast.Param{Name: fname.Text, Type: ft})
			if _, ok := p.accept(lexer.Punct, ","); ok {
				if p.match(lexer.Punct, "}") {
					break
				}
				continue
			}
			break
		}
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	return &ast.StructDecl{P: kw.Pos, Name: name.Text, Fields: fields}, nil
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
	case t.Kind == lexer.Keyword && t.Text == "string":
		p.advance()
		base = ast.StringType{}
	case t.Kind == lexer.Keyword && t.Text == "float":
		p.advance()
		base = ast.FloatType{}
	case t.Kind == lexer.Punct && t.Text == "(":
		// Function type: `(T1, T2, ...) => RT`. Empty parens are
		// allowed for nullary callbacks.
		p.advance()
		var params []ast.Type
		if !p.match(lexer.Punct, ")") {
			for {
				pt, err := p.parseType()
				if err != nil {
					return nil, err
				}
				params = append(params, pt)
				if _, ok := p.accept(lexer.Punct, ","); !ok {
					break
				}
			}
		}
		if _, err := p.expect(lexer.Punct, ")"); err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.Punct, "=>"); err != nil {
			return nil, err
		}
		ret, err := p.parseType()
		if err != nil {
			return nil, err
		}
		base = &ast.FuncType{Params: params, Result: ret}
	case t.Kind == lexer.Ident:
		// Bare identifier is a struct type reference. The checker
		// validates that the name actually resolves to a struct.
		// `mod.Foo` is a qualified reference to an imported
		// module's struct — we encode it as a single
		// dotted-string in StructType.Name and let modload
		// rewrite it to `mod__Foo` before the checker runs.
		p.advance()
		name := t.Text
		if p.match(lexer.Punct, ".") {
			p.advance()
			fieldTok, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			name = name + "." + fieldTok.Text
		}
		base = ast.StructType{Name: name}
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
		before := p.i
		s, err := p.parseStmt()
		if err != nil {
			p.errors = append(p.errors, err)
			p.syncToStmt()
			if p.i == before {
				p.advance()
			}
			continue
		}
		if s != nil {
			block.Stmts = append(block.Stmts, s)
		}
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return block, err
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
		case "for":
			return p.parseFor()
		case "break":
			return p.parseBreakContinue(true)
		case "continue":
			return p.parseBreakContinue(false)
		case "return":
			return p.parseReturn()
		case "var":
			return p.parseVar()
		case "switch":
			return p.parseSwitch()
		case "function":
			return p.parseLocalFunction()
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

// parseFor produces a real For node so that `continue` can jump to the
// step expression, not back to the top of the loop body. There are
// two recognised shapes:
//
//   - `for (init; cond; step) body`           — classic C-style.
//   - `for IDENT in expr body`                — for-each over an
//     array or string. Desugars at parse time to the equivalent
//     C-style loop with synthetic length / index slots, so the
//     rest of the pipeline (checker, IR, codegen) never has to
//     know foreach exists.
func (p *parser) parseFor() (ast.Stmt, error) {
	kw := p.advance()

	// Foreach shape: `for IDENT in expr body`. Detect by looking at
	// the next two tokens (an Ident followed by the keyword-ish
	// `in`). The lexer treats `in` as a regular identifier, so we
	// match on text rather than kind.
	if p.match(lexer.Ident, "") && p.i+1 < len(p.tokens) {
		if next := p.tokens[p.i+1]; next.Kind == lexer.Ident && next.Text == "in" {
			return p.parseForEach(kw)
		}
	}

	if _, err := p.expect(lexer.Punct, "("); err != nil {
		return nil, err
	}

	var init ast.Stmt
	if p.match(lexer.Keyword, "var") {
		v, err := p.parseVar() // consumes its own trailing ';'
		if err != nil {
			return nil, err
		}
		init = v
	} else if p.match(lexer.Punct, ";") {
		p.advance()
	} else {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.Punct, ";"); err != nil {
			return nil, err
		}
		init = &ast.ExprStmt{P: e.Pos(), Expr: e}
	}

	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}

	var step ast.Stmt
	if !p.match(lexer.Punct, ")") {
		stepExpr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		step = &ast.ExprStmt{P: stepExpr.Pos(), Expr: stepExpr}
	}
	if _, err := p.expect(lexer.Punct, ")"); err != nil {
		return nil, err
	}

	body, err := p.parseStmt()
	if err != nil {
		return nil, err
	}

	return &ast.For{P: kw.Pos, Init: init, Cond: cond, Step: step, Body: body}, nil
}

// foreachCounter gives each desugared foreach loop's synthetic vars
// a unique suffix (`__foreach_iter_3`, `__foreach_idx_3`, …) so
// nested foreach loops don't clash on slot names. Stored on the
// parser so it survives recursive calls and tracks per-Parse rather
// than per-process.
//
// The counter doesn't need to be unique across separate Parse calls
// — each Parse produces a fresh AST whose slot names are scoped to
// that compilation unit.
func (p *parser) nextForeachID() int {
	p.foreachN++
	return p.foreachN
}

// parseForEach desugars `for IDENT in expr body` into a Block that
// declares a synthetic iter / index / length, runs a classic
// `while idx < length`, binds the user's IDENT inside the loop, and
// advances the index. The Block wraps everything so the synthetic
// vars are scoped to the foreach. `break` and `continue` work as
// expected because they target the inner While.
//
// Shape after desugaring:
//
//   {
//     var __foreach_iter_N = expr;
//     var __foreach_len_N  = len(__foreach_iter_N);
//     var __foreach_idx_N  = 0;
//     while (__foreach_idx_N < __foreach_len_N) {
//       var IDENT = __foreach_iter_N[__foreach_idx_N];
//       <body>
//       __foreach_idx_N = __foreach_idx_N + 1;
//     }
//   }
//
// Works for both arrays (any element type) and strings (each
// element a number = byte). The IDENT's type is inferred from the
// indexed expression by the checker.
func (p *parser) parseForEach(kw lexer.Token) (ast.Stmt, error) {
	nameTok := p.advance() // IDENT
	p.advance()            // `in`
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	body, err := p.parseStmt()
	if err != nil {
		return nil, err
	}

	id := p.nextForeachID()
	iterName := fmt.Sprintf("__foreach_iter_%d", id)
	idxName := fmt.Sprintf("__foreach_idx_%d", id)
	lenName := fmt.Sprintf("__foreach_len_%d", id)

	mkIdent := func(name string) *ast.Ident { return &ast.Ident{P: kw.Pos, Name: name} }
	mkNum := func(v int64) *ast.NumberLit { return &ast.NumberLit{P: kw.Pos, Value: v} }

	// var __foreach_iter_N = expr;
	declIter := &ast.Var{P: kw.Pos, Name: iterName, Init: expr}
	// var __foreach_len_N = len(__foreach_iter_N);
	declLen := &ast.Var{P: kw.Pos, Name: lenName, Init: &ast.Call{
		P:      kw.Pos,
		Callee: mkIdent("len"),
		Args:   []ast.Expr{mkIdent(iterName)},
	}}
	// var __foreach_idx_N = 0;
	declIdx := &ast.Var{P: kw.Pos, Name: idxName, Init: mkNum(0)}

	// var IDENT = __foreach_iter_N[__foreach_idx_N];
	bindUser := &ast.Var{P: nameTok.Pos, Name: nameTok.Text, Init: &ast.Index{
		P:     nameTok.Pos,
		Array: mkIdent(iterName),
		Idx:   mkIdent(idxName),
	}}
	// __foreach_idx_N = __foreach_idx_N + 1;
	stepStmt := &ast.ExprStmt{P: kw.Pos, Expr: &ast.Assign{
		P:      kw.Pos,
		Target: mkIdent(idxName),
		Value: &ast.Binary{
			P: kw.Pos, Op: "+",
			Left:  mkIdent(idxName),
			Right: mkNum(1),
		},
	}}

	// User body wrapped in its own Block so the index-binding and
	// the user's stmts share the loop-scope. The step lives on the
	// enclosing For (not appended to the body) so `continue` jumps
	// to the step before re-checking cond — without that the
	// index never advances on continue and the loop hangs.
	innerStmts := []ast.Stmt{bindUser}
	if blk, ok := body.(*ast.Block); ok {
		innerStmts = append(innerStmts, blk.Stmts...)
	} else {
		innerStmts = append(innerStmts, body)
	}
	innerBlock := &ast.Block{P: kw.Pos, Stmts: innerStmts}

	forLoop := &ast.For{
		P: kw.Pos,
		// Init lives on the enclosing Block (declIter / declLen /
		// declIdx); the For's Init slot is unused so the index
		// doesn't get re-zeroed on every iteration of an outer
		// loop that wraps this one.
		Cond: &ast.Binary{
			P: kw.Pos, Op: "<",
			Left:  mkIdent(idxName),
			Right: mkIdent(lenName),
		},
		Step: stepStmt,
		Body: innerBlock,
	}

	return &ast.Block{
		P:     kw.Pos,
		Stmts: []ast.Stmt{declIter, declLen, declIdx, forLoop},
	}, nil
}

// parseSwitch parses
//
//	switch (tag) {
//	  case v1, v2: { ... }
//	  case v3: { ... }
//	  default: { ... }
//	}
//
// Cases don't fall through. Each body runs until the next `case`,
// `default`, or the closing brace; we don't require a `break` to leave
// a case (and a leading `break` inside a case body is a no-op).
func (p *parser) parseSwitch() (ast.Stmt, error) {
	kw := p.advance() // `switch`
	if _, err := p.expect(lexer.Punct, "("); err != nil {
		return nil, err
	}
	tag, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ")"); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, err
	}
	sw := &ast.Switch{P: kw.Pos, Tag: tag}
	for {
		t := p.peek()
		if t.Kind == lexer.Punct && t.Text == "}" {
			p.advance()
			return sw, nil
		}
		if t.Kind != lexer.Keyword || (t.Text != "case" && t.Text != "default") {
			return nil, p.errorf(t.Pos, "expected `case`, `default` or `}` in switch body")
		}
		caseKw := p.advance()
		if caseKw.Text == "default" {
			if sw.Default != nil {
				return nil, p.errorf(caseKw.Pos, "duplicate `default` clause")
			}
			if _, err := p.expect(lexer.Punct, ":"); err != nil {
				return nil, err
			}
			body, err := p.parseCaseBody()
			if err != nil {
				return nil, err
			}
			sw.Default = body
			continue
		}
		// `case v1, v2, v3:`
		var values []ast.Expr
		for {
			v, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			values = append(values, v)
			if _, ok := p.accept(lexer.Punct, ","); ok {
				continue
			}
			break
		}
		if _, err := p.expect(lexer.Punct, ":"); err != nil {
			return nil, err
		}
		body, err := p.parseCaseBody()
		if err != nil {
			return nil, err
		}
		sw.Cases = append(sw.Cases, &ast.SwitchCase{P: caseKw.Pos, Values: values, Body: body})
	}
}

// parseCaseBody collects statements up to the next `case`, `default`,
// or closing `}`. The block's position is the position of the first
// statement (or 0:0 for an empty case).
func (p *parser) parseCaseBody() (*ast.Block, error) {
	blk := &ast.Block{}
	first := true
	for {
		t := p.peek()
		if t.Kind == lexer.Punct && t.Text == "}" {
			return blk, nil
		}
		if t.Kind == lexer.Keyword && (t.Text == "case" || t.Text == "default") {
			return blk, nil
		}
		if first {
			blk.P = t.Pos
			first = false
		}
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		blk.Stmts = append(blk.Stmts, s)
	}
}

func (p *parser) parseBreakContinue(isBreak bool) (ast.Stmt, error) {
	kw := p.advance()
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	if isBreak {
		return &ast.Break{P: kw.Pos}, nil
	}
	return &ast.Continue{P: kw.Pos}, nil
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

// compoundOps maps a compound-assignment punctuator to its underlying
// binary operator. `x += y` desugars into `x = x + y` at parse time so
// the rest of the pipeline never has to know about compound forms.
var compoundOps = map[string]string{
	"+=": "+", "-=": "-", "*=": "*", "/=": "/", "%=": "%",
	"&=": "&", "|=": "|", "^=": "^", "<<=": "<<", ">>=": ">>",
}

func (p *parser) parseAssign() (ast.Expr, error) {
	left, err := p.parseTernary()
	if err != nil {
		return nil, err
	}
	if eq, ok := p.accept(lexer.Punct, "="); ok {
		rhs, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		switch left.(type) {
		case *ast.Ident, *ast.Index, *ast.FieldAccess:
			// fine
		default:
			return nil, p.errorf(eq.Pos, "left-hand side of assignment is not assignable")
		}
		return &ast.Assign{P: eq.Pos, Target: left, Value: rhs}, nil
	}
	if t := p.peek(); t.Kind == lexer.Punct {
		if op, ok := compoundOps[t.Text]; ok {
			tok := p.advance()
			rhs, err := p.parseAssign()
			if err != nil {
				return nil, err
			}
			switch left.(type) {
			case *ast.Ident, *ast.Index:
				// fine
			default:
				return nil, p.errorf(tok.Pos, "left-hand side of assignment is not assignable")
			}
			binary := &ast.Binary{P: tok.Pos, Op: op, Left: left, Right: rhs}
			return &ast.Assign{P: tok.Pos, Target: left, Value: binary}, nil
		}
	}
	return left, nil
}

// parseTernary handles `cond ? then : else`. It's right-associative
// (so `a ? b : c ? d : e` parses as `a ? b : (c ? d : e)`), and sits
// just above the logical operators in precedence.
func (p *parser) parseTernary() (ast.Expr, error) {
	cond, err := p.parseLogicOr()
	if err != nil {
		return nil, err
	}
	q, ok := p.accept(lexer.Punct, "?")
	if !ok {
		return cond, nil
	}
	then, err := p.parseAssign()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ":"); err != nil {
		return nil, err
	}
	els, err := p.parseAssign()
	if err != nil {
		return nil, err
	}
	return &ast.Ternary{P: q.Pos, Cond: cond, Then: then, Else: els}, nil
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
	return p.parseBinaryLeft(p.parseBitOr, "&&")
}
func (p *parser) parseBitOr() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseBitXor, "|")
}
func (p *parser) parseBitXor() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseBitAnd, "^")
}
func (p *parser) parseBitAnd() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseEquality, "&")
}
func (p *parser) parseEquality() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseRelational, "==", "!=")
}
func (p *parser) parseRelational() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseShift, "<", ">", "<=", ">=")
}
func (p *parser) parseShift() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseAdditive, "<<", ">>")
}
func (p *parser) parseAdditive() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseMultiplicative, "+", "-")
}
func (p *parser) parseMultiplicative() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseUnary, "*", "/", "%")
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
		case p.match(lexer.Punct, "."):
			dot := p.advance()
			fname, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			// `mod.Foo { … }` is a qualified struct literal — same
			// shape as `Foo { … }` but with the module-qualified
			// type name stitched together as one dotted string.
			// modload rewrites `mod.Foo` to `mod__Foo` before the
			// checker runs, so the StructLit.TypeName carries the
			// dotted form temporarily.
			if id, ok := expr.(*ast.Ident); ok && p.match(lexer.Punct, "{") {
				return p.parseStructLit(id.P, id.Name+"."+fname.Text)
			}
			expr = &ast.FieldAccess{P: dot.Pos, Target: expr, Field: fname.Text}
		default:
			return expr, nil
		}
	}
}

// parseStructLit parses the `{ field: value, ... }` part of a struct
// literal, having already consumed the type-name identifier. Trailing
// commas are accepted; the checker enforces field-set completeness
// against the struct declaration.
func (p *parser) parseStructLit(pos ast.Position, typeName string) (ast.Expr, error) {
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, err
	}
	var fields []ast.FieldInit
	if !p.match(lexer.Punct, "}") {
		for {
			fname, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.Punct, ":"); err != nil {
				return nil, err
			}
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			fields = append(fields, ast.FieldInit{Name: fname.Text, Value: val})
			if _, ok := p.accept(lexer.Punct, ","); ok {
				if p.match(lexer.Punct, "}") {
					break
				}
				continue
			}
			break
		}
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	return &ast.StructLit{P: pos, TypeName: typeName, Fields: fields}, nil
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
	case lexer.Float:
		p.advance()
		// strconv.ParseFloat handles `1.5`, `0.0`, etc. We accepted
		// only `<digits>.<digits>` from the lexer so this won't fail
		// in practice, but plumb the error through anyway.
		f, err := strconv.ParseFloat(t.Text, 64)
		if err != nil {
			return nil, p.errorf(t.Pos, "invalid float literal %q: %v", t.Text, err)
		}
		return &ast.FloatLit{P: t.Pos, Value: f}, nil
	case lexer.String:
		p.advance()
		return &ast.StringLit{P: t.Pos, Value: t.Text}, nil
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
		// `Foo { x: 1, y: 2 }`. The `{` immediately after an identifier
		// can only mean a struct literal in expression position — there
		// are no other constructs of that shape.
		if p.match(lexer.Punct, "{") {
			return p.parseStructLit(t.Pos, t.Text)
		}
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
