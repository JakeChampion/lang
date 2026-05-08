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
	// noStructLit suppresses the `Ident { … }` struct-literal
	// shortcut while parsing expressions in positions that
	// otherwise greedily consume a trailing `{` as the body —
	// `if let Variant(b) = expr { … }` being the motivating
	// case. The flag is saved + restored around the affected
	// expression so nested expressions (`if let Some(x) = obj { … }`
	// where `obj` is a method call returning a Foo, NOT a
	// struct literal) still work correctly.
	noStructLit bool
	// returnTypeStack tracks the return type of the function
	// currently being parsed. Pushed by parseFunction on entry,
	// popped on exit. The `use` desugar uses the top of stack
	// to fill in the synthesised callback function's return
	// type — every `use` chain ultimately routes back to the
	// surrounding function's return.
	returnTypeStack []ast.Type
	// useN counts synthesised `use` callback decls so each one
	// gets a unique name. Resets per Parse() so module-local
	// names stay deterministic.
	useN int
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
		// `pub` is an optional prefix on function, struct, enum, or
		// const decls at the top level. Track it and consume; the
		// inner parser stays unaware of visibility — we stamp the
		// Public flag after the decl is built. A bare `pub` without
		// a following decl is a parse error.
		isPub := false
		if p.match(lexer.Keyword, "pub") {
			pubTok := p.advance()
			if !p.match(lexer.Keyword, "function") &&
				!p.match(lexer.Keyword, "struct") &&
				!p.match(lexer.Keyword, "enum") &&
				!p.match(lexer.Keyword, "const") {
				p.errors = append(p.errors, p.errorf(pubTok.Pos,
					"`pub` must be followed by `function`, `struct`, `enum`, or `const`"))
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
		if p.match(lexer.Keyword, "const") {
			cd, err := p.parseConstDecl()
			if err != nil {
				p.errors = append(p.errors, err)
				p.syncToTopLevel()
				if p.i == before {
					p.advance()
				}
				continue
			}
			if cd != nil {
				cd.Public = isPub
				prog.Consts = append(prog.Consts, cd)
			}
			continue
		}
		if p.match(lexer.Keyword, "enum") {
			ed, err := p.parseEnumDecl()
			if err != nil {
				p.errors = append(p.errors, err)
				p.syncToTopLevel()
				if p.i == before {
					p.advance()
				}
				continue
			}
			if ed != nil {
				ed.Public = isPub
				prog.Enums = append(prog.Enums, ed)
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
			p.match(lexer.Keyword, "enum") ||
			p.match(lexer.Keyword, "import") ||
			p.match(lexer.Keyword, "const") ||
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
	// Optional type parameters: `function id[T](x: T): T`.
	// Reuses the bracket form enums use (`enum Option[T]`) so
	// parsers / readers learn one shape for both generic decls
	// and generic instantiations.
	var typeParams []string
	if p.match(lexer.Punct, "[") {
		p.advance() // [
		for {
			pname, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			typeParams = append(typeParams, pname.Text)
			if _, ok := p.accept(lexer.Punct, ","); ok {
				continue
			}
			break
		}
		if _, err := p.expect(lexer.Punct, "]"); err != nil {
			return nil, err
		}
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

	// Track the return type so any `use` desugar inside the body
	// can stamp it onto the synthesised callback's signature.
	p.returnTypeStack = append(p.returnTypeStack, ret)
	body, err := p.parseBlock()
	p.returnTypeStack = p.returnTypeStack[:len(p.returnTypeStack)-1]
	if err != nil {
		return nil, err
	}
	return &ast.FuncDecl{
		P:          kw.Pos,
		Name:       name.Text,
		TypeParams: typeParams,
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
	// Optional type parameters: `struct Pair[A, B] { … }`. Same
	// bracket form generic enums + functions use.
	var typeParams []string
	if p.match(lexer.Punct, "[") {
		p.advance()
		for {
			pname, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			typeParams = append(typeParams, pname.Text)
			if _, ok := p.accept(lexer.Punct, ","); ok {
				continue
			}
			break
		}
		if _, err := p.expect(lexer.Punct, "]"); err != nil {
			return nil, err
		}
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
	return &ast.StructDecl{P: kw.Pos, Name: name.Text, TypeParams: typeParams, Fields: fields}, nil
}

// parseConstDecl parses a top-level `const NAME[: T] = expr;`. The
// type annotation is optional; when missing, constfold infers the
// type from the resolved value. The initialiser is parsed as a
// general expression — constfold validates that it's actually a
// constant expression after parsing.
func (p *parser) parseConstDecl() (*ast.ConstDecl, error) {
	kw, err := p.expect(lexer.Keyword, "const")
	if err != nil {
		return nil, err
	}
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	var t ast.Type
	if _, ok := p.accept(lexer.Punct, ":"); ok {
		t, err = p.parseType()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.Punct, "="); err != nil {
		return nil, err
	}
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.ConstDecl{P: kw.Pos, Name: name.Text, Type: t, Value: val}, nil
}

// parseEnumDecl parses `enum Foo { Bar, Baz(T1, T2), … }`. Each
// variant is either a bare identifier (no payload) or an
// identifier followed by a parenthesised type list. Trailing
// commas before `}` are allowed.
func (p *parser) parseEnumDecl() (*ast.EnumDecl, error) {
	kw, err := p.expect(lexer.Keyword, "enum")
	if err != nil {
		return nil, err
	}
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	// Optional generic parameters: `enum Option[T, U] { … }`. We
	// require at least one identifier between the brackets — empty
	// `[]` would be ambiguous with the array-type suffix the type
	// parser uses.
	var typeParams []string
	if _, ok := p.accept(lexer.Punct, "["); ok {
		for {
			pname, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			typeParams = append(typeParams, pname.Text)
			if _, ok := p.accept(lexer.Punct, ","); ok {
				continue
			}
			break
		}
		if _, err := p.expect(lexer.Punct, "]"); err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, err
	}
	var variants []ast.EnumVariant
	if !p.match(lexer.Punct, "}") {
		for {
			vname, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			variant := ast.EnumVariant{P: vname.Pos, Name: vname.Text}
			if _, ok := p.accept(lexer.Punct, "("); ok {
				if !p.match(lexer.Punct, ")") {
					for {
						pt, err := p.parseType()
						if err != nil {
							return nil, err
						}
						variant.Payloads = append(variant.Payloads, pt)
						if _, ok := p.accept(lexer.Punct, ","); ok {
							if p.match(lexer.Punct, ")") {
								break
							}
							continue
						}
						break
					}
				}
				if _, err := p.expect(lexer.Punct, ")"); err != nil {
					return nil, err
				}
			}
			variants = append(variants, variant)
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
	return &ast.EnumDecl{P: kw.Pos, Name: name.Text, TypeParams: typeParams, Variants: variants}, nil
}

func (p *parser) parseType() (ast.Type, error) {
	t := p.peek()
	var base ast.Type
	switch {
	case t.Kind == lexer.Punct && t.Text == "[":
		// `[T]` slice view. Distinct from owned `T[]` which is
		// parsed as a postfix below. Putting the brackets in
		// front signals "this borrows" — Odin's convention.
		p.advance()
		elem, err := p.parseType()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.Punct, "]"); err != nil {
			return nil, err
		}
		base = ast.SliceType{Elem: elem}
	case t.Kind == lexer.Keyword && (t.Text == "number" || t.Text == "i32"):
		// `number` is the legacy alias; both lower to the
		// canonical zero-value NumberType so equality keeps
		// working with code that still compares to
		// `ast.NumberType{}` directly. Spelling tracks which
		// keyword the user wrote so `lang -fmt` round-trips it
		// rather than coercing to a single canonical name.
		p.advance()
		base = ast.NumberType{Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "i64":
		p.advance()
		base = ast.NumberType{Width: 64, Signed: true, Spelling: t.Text}
	case t.Kind == lexer.Keyword && (t.Text == "i8" || t.Text == "i16"):
		// Sub-i32 signed types parse but are reserved — codegen
		// for these widths is a follow-up. Erroring at the type
		// level keeps the surface honest rather than silently
		// promoting to i32.
		return nil, p.errorf(t.Pos, "%s is reserved; not yet wired through codegen", t.Text)
	case t.Kind == lexer.Keyword && t.Text == "u32":
		p.advance()
		base = ast.NumberType{Width: 32, Signed: false, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "u64":
		p.advance()
		base = ast.NumberType{Width: 64, Signed: false, Spelling: t.Text}
	case t.Kind == lexer.Keyword && (t.Text == "u8" || t.Text == "u16"):
		// Sub-i32 unsigned widths still pending — needs masking
		// on store + zero-extend on load before arithmetic
		// behaves correctly. Reserved keyword keeps the syntax
		// stable for when codegen lands.
		return nil, p.errorf(t.Pos, "%s is reserved; not yet wired through codegen (use u32 for now)", t.Text)
	case t.Kind == lexer.Keyword && t.Text == "f64":
		return nil, p.errorf(t.Pos, "f64 is reserved; not yet wired through codegen")
	case t.Kind == lexer.Keyword && (t.Text == "float" || t.Text == "f32"):
		p.advance()
		base = ast.FloatType{Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "boolean":
		p.advance()
		base = ast.BoolType{}
	case t.Kind == lexer.Keyword && t.Text == "void":
		p.advance()
		base = ast.VoidType{}
	case t.Kind == lexer.Keyword && t.Text == "string":
		p.advance()
		base = ast.StringType{}
	case t.Kind == lexer.Punct && t.Text == "(":
		// `(T1, T2, ...)` followed by `=>` is a function type; the
		// same shape NOT followed by `=>` is a tuple type, but only
		// when there are at least 2 elements (single-element
		// "tuples" don't exist; empty parens are still reserved
		// for the function-type-of-no-args case). This shape lets
		// `function f(): (i32, string)` parse as a multi-return
		// tuple without a trailing-comma rule.
		p.advance()
		var elems []ast.Type
		if !p.match(lexer.Punct, ")") {
			for {
				pt, err := p.parseType()
				if err != nil {
					return nil, err
				}
				elems = append(elems, pt)
				if _, ok := p.accept(lexer.Punct, ","); !ok {
					break
				}
			}
		}
		if _, err := p.expect(lexer.Punct, ")"); err != nil {
			return nil, err
		}
		if _, isArrow := p.accept(lexer.Punct, "=>"); isArrow {
			ret, err := p.parseType()
			if err != nil {
				return nil, err
			}
			base = &ast.FuncType{Params: elems, Result: ret}
		} else if len(elems) >= 2 {
			base = ast.TupleType{Elems: elems}
		} else {
			return nil, p.errorf(t.Pos, "expected `=>` after parameter list (function type) or 2+ comma-separated types (tuple type)")
		}
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
		// Generic instantiation: `Foo[T1, T2]`. Distinguished
		// from the array-suffix loop below by a lookahead — `[`
		// directly followed by `]` is the array form.
		if p.match(lexer.Punct, "[") && p.i+1 < len(p.tokens) && !(p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "]") {
			p.advance() // consume `[`
			var args []ast.Type
			for {
				at, err := p.parseType()
				if err != nil {
					return nil, err
				}
				args = append(args, at)
				if _, ok := p.accept(lexer.Punct, ","); ok {
					continue
				}
				break
			}
			if _, err := p.expect(lexer.Punct, "]"); err != nil {
				return nil, err
			}
			// Generic instantiations are always enums in this
			// PR; the checker validates the name actually
			// resolves to an enum.
			base = ast.EnumType{Name: name, Args: args}
		} else {
			base = ast.StructType{Name: name}
		}
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
		// `use IDENT : TYPE <- EXPR;` is a statement-position
		// desugar — the rest of the current block becomes the
		// callback's body. Handled inline so we have access to
		// the in-progress block builder.
		if p.match(lexer.Keyword, "use") {
			if err := p.parseUse(block); err != nil {
				p.errors = append(p.errors, err)
				p.syncToStmt()
				if p.i == before {
					p.advance()
				}
				continue
			}
			// `use` consumes the rest of the block as its
			// callback body, so the current block is finished.
			break
		}
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
		case "let":
			return p.parseLetElse()
		case "switch":
			return p.parseSwitch()
		case "match":
			return p.parseMatch()
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
	// `if let <Variant>(b1, …) = <expr> { … }` — pattern-binding
	// shorthand for a one-arm match. Disambiguated by the `let`
	// keyword right after `if`. The match's payload bindings are
	// in scope for Then only.
	if p.match(lexer.Keyword, "let") {
		p.advance() // let
		variantTok, err := p.expect(lexer.Ident, "")
		if err != nil {
			return nil, err
		}
		var bindings []string
		if _, ok := p.accept(lexer.Punct, "("); ok {
			if !p.match(lexer.Punct, ")") {
				for {
					nameTok, err := p.expect(lexer.Ident, "")
					if err != nil {
						return nil, err
					}
					bindings = append(bindings, nameTok.Text)
					if _, ok := p.accept(lexer.Punct, ","); ok {
						if p.match(lexer.Punct, ")") {
							break
						}
						continue
					}
					break
				}
			}
			if _, err := p.expect(lexer.Punct, ")"); err != nil {
				return nil, err
			}
		}
		if _, err := p.expect(lexer.Punct, "="); err != nil {
			return nil, err
		}
		// Suppress trailing struct-literal parsing while reading
		// the source — the `{` that follows opens Then.
		prevNS := p.noStructLit
		p.noStructLit = true
		src, err := p.parseExpr()
		p.noStructLit = prevNS
		if err != nil {
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
		return &ast.IfLet{
			P:           kw.Pos,
			VariantName: variantTok.Text,
			Bindings:    bindings,
			Source:      src,
			Then:        then,
			Else:        els,
		}, nil
	}
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

// parseMatch parses `match (<expr>) { Pat => { … }, … }`. The
// tag expression is parenthesised (matching `switch` and avoiding
// the `Ident { … }` ambiguity with struct-literal shorthand).
// Patterns are either:
//
//	`_`                — wildcard (must be the last arm)
//	`Variant`          — payload-less variant
//	`Variant(a, b)`    — variant with positional payload bindings
//
// Each arm body is a brace-block; we require this for consistency
// with `if` / `for` / `while` and to keep the parser context-light.
// Arms are separated by commas; a trailing comma is allowed.
func (p *parser) parseMatch() (ast.Stmt, error) {
	kw := p.advance() // `match`
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
	m := &ast.Match{P: kw.Pos, Tag: tag}
	for !p.match(lexer.Punct, "}") {
		arm, err := p.parseMatchArm()
		if err != nil {
			return nil, err
		}
		m.Arms = append(m.Arms, arm)
		if _, ok := p.accept(lexer.Punct, ","); ok {
			continue
		}
		break
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	return m, nil
}

func (p *parser) parseMatchArm() (*ast.MatchArm, error) {
	t := p.peek()
	arm := &ast.MatchArm{P: t.Pos}
	if t.Kind == lexer.Punct && t.Text == "_" {
		// `_` is lexed as a punct in our lexer? No — the lexer
		// treats `_` as the start of an identifier. So this branch
		// never fires; the wildcard always comes through as Ident.
		p.advance()
		arm.IsWildcard = true
	} else if t.Kind == lexer.Ident && t.Text == "_" {
		p.advance()
		arm.IsWildcard = true
	} else if t.Kind == lexer.Ident {
		p.advance()
		arm.VariantName = t.Text
		if _, ok := p.accept(lexer.Punct, "("); ok {
			if !p.match(lexer.Punct, ")") {
				for {
					nameTok, err := p.expect(lexer.Ident, "")
					if err != nil {
						return nil, err
					}
					arm.Bindings = append(arm.Bindings, nameTok.Text)
					if _, ok := p.accept(lexer.Punct, ","); ok {
						if p.match(lexer.Punct, ")") {
							break
						}
						continue
					}
					break
				}
			}
			if _, err := p.expect(lexer.Punct, ")"); err != nil {
				return nil, err
			}
		}
	} else {
		return nil, p.errorf(t.Pos, "expected variant pattern or `_` in match arm, got %s", t.Text)
	}
	// Optional guard: `<pattern> when <expr> => <body>`. The
	// guard expression has bindings in scope (so a guard like
	// `when n > 0` references the variant payload bound by the
	// pattern). Pre-`=>` so the syntax reads in pattern → guard
	// → body order.
	if p.match(lexer.Keyword, "when") {
		p.advance()
		guard, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		arm.Guard = guard
	}
	if _, err := p.expect(lexer.Punct, "=>"); err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	arm.Body = body
	return arm, nil
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

// parseUse desugars `use IDENT : TYPE <- EXPR;` plus the
// remaining statements of the enclosing block into a synthesised
// local function declaration + a return-statement that calls
// EXPR with the local function appended as the last argument.
//
// Example:
//
//	function compute(s: string): Result[i32, string] {
//	    use n: i32 <- result.try(parse(s));
//	    return Ok(n + 1);
//	}
//
// becomes (post-parse):
//
//	function compute(s: string): Result[i32, string] {
//	    function __use_1(n: i32): Result[i32, string] {
//	        return Ok(n + 1);
//	    }
//	    return result.try(parse(s), __use_1);
//	}
//
// The synthesised callback's return type is read from
// `parser.returnTypeStack` — every `use` chain ultimately
// returns through the surrounding function. Type annotation
// on the binding is required for now; an inference pass can
// peek at EXPR's callback parameter type as a follow-up.
func (p *parser) parseUse(parent *ast.Block) error {
	kw := p.advance() // use
	nameTok, err := p.expect(lexer.Ident, "")
	if err != nil {
		return err
	}
	if _, err := p.expect(lexer.Punct, ":"); err != nil {
		return err
	}
	bindType, err := p.parseType()
	if err != nil {
		return err
	}
	// `<-` lexes as two punct tokens (`<` then `-`). Accept both.
	if _, err := p.expect(lexer.Punct, "<"); err != nil {
		return err
	}
	if _, err := p.expect(lexer.Punct, "-"); err != nil {
		return err
	}
	src, err := p.parseExpr()
	if err != nil {
		return err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return err
	}
	srcCall, ok := src.(*ast.Call)
	if !ok {
		return p.errorf(kw.Pos, "use expression must be a function call (so the callback can be appended as the last arg)")
	}
	// Parse the rest of the block as the callback body. parseBlock
	// expects a leading `{`, so synthesise one — the actual
	// `}` we consume here ends both the callback and the parent
	// block. We open a synthetic block, slurp statements until
	// the upcoming `}`, then leave that `}` for the caller's
	// loop to consume.
	body := &ast.Block{P: kw.Pos}
	for !p.match(lexer.Punct, "}") && !p.match(lexer.EOF, "") {
		before := p.i
		if p.match(lexer.Keyword, "use") {
			// Nested use — recursive desugar. The callback body
			// is itself the parent for further use chains.
			if err := p.parseUse(body); err != nil {
				return err
			}
			break
		}
		s, err := p.parseStmt()
		if err != nil {
			return err
		}
		if s != nil {
			body.Stmts = append(body.Stmts, s)
		}
		if p.i == before {
			p.advance()
		}
	}
	if len(p.returnTypeStack) == 0 {
		return p.errorf(kw.Pos, "use must appear inside a function body")
	}
	rt := p.returnTypeStack[len(p.returnTypeStack)-1]

	// Synthesise the local callback function.
	p.useN++
	callbackName := fmt.Sprintf("__use_%d", p.useN)
	cb := &ast.FuncDecl{
		P:          kw.Pos,
		Name:       callbackName,
		Params:     []ast.Param{{Name: nameTok.Text, Type: bindType}},
		ReturnType: rt,
		Body:       body,
		IsLocal:    true,
	}
	parent.Stmts = append(parent.Stmts, cb)
	// Append the callback as the last argument of the source call
	// + emit a `return` so the surrounding function returns its
	// value.
	srcCall.Args = append(srcCall.Args, &ast.Ident{P: kw.Pos, Name: callbackName})
	parent.Stmts = append(parent.Stmts, &ast.Return{P: kw.Pos, Value: srcCall})
	return nil
}

// parseLetElse parses `let <Variant>(b1, b2, …) = <expr> else
// { <divergent> };`. Bindings introduced by the pattern are
// added to the enclosing scope (live for the rest of the
// block); the else branch must terminate the surrounding
// control flow — the checker enforces that, here we just parse
// the syntax.
func (p *parser) parseLetElse() (ast.Stmt, error) {
	kw := p.advance() // let
	variantTok, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	var bindings []string
	if _, ok := p.accept(lexer.Punct, "("); ok {
		if !p.match(lexer.Punct, ")") {
			for {
				nameTok, err := p.expect(lexer.Ident, "")
				if err != nil {
					return nil, err
				}
				bindings = append(bindings, nameTok.Text)
				if _, ok := p.accept(lexer.Punct, ","); ok {
					if p.match(lexer.Punct, ")") {
						break
					}
					continue
				}
				break
			}
		}
		if _, err := p.expect(lexer.Punct, ")"); err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.Punct, "="); err != nil {
		return nil, err
	}
	// Suppress trailing struct-literal parsing while reading
	// the source, so `obj { … }` doesn't eat the `else`-side
	// trailer. The Var-init form doesn't need this because it
	// uses `;`, but `let else` follows the source with `else`.
	prevNS := p.noStructLit
	p.noStructLit = true
	src, err := p.parseExpr()
	p.noStructLit = prevNS
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Keyword, "else"); err != nil {
		return nil, err
	}
	elseBlk, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.LetElse{
		P:           kw.Pos,
		VariantName: variantTok.Text,
		Bindings:    bindings,
		Source:      src,
		Else:        elseBlk,
	}, nil
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
	left, err := p.parsePipe()
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

// parsePipe handles `x |> f` (data-first pipe) — desugared at parse
// time to a call with the LHS prepended to the RHS's arg list:
//
//	x |> f                  →  f(x)
//	x |> f(a, b)            →  f(x, a, b)
//	x |> y.method(a)        →  y.method(x, a)
//	x |> f |> g             →  g(f(x))               (left-assoc)
//
// Precedence sits between assignment and ternary so `1 + 2 |> f`
// parses as `(1 + 2) |> f` (i.e. `f(1 + 2)`). This matches the
// OCaml / F# / Elixir / Roc / Gleam convention. The lang stdlib
// is written subject-first so the first arg is the most natural
// pipe target.
func (p *parser) parsePipe() (ast.Expr, error) {
	left, err := p.parseTernary()
	if err != nil {
		return nil, err
	}
	for p.match(lexer.Punct, "|>") {
		pipeTok := p.advance()
		right, err := p.parseTernary()
		if err != nil {
			return nil, err
		}
		switch r := right.(type) {
		case *ast.Call:
			// `x |> f(a, b)` — prepend x to f's arg list.
			r.Args = append([]ast.Expr{left}, r.Args...)
			r.IsPipe = true
			left = r
		default:
			// `x |> f` — wrap as a single-arg call. The pipe
			// position is preserved on the synthesised Call so
			// runtime errors point at the pipe rather than the
			// callee's definition.
			left = &ast.Call{P: pipeTok.Pos, Callee: r, Args: []ast.Expr{left}, IsPipe: true}
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
	return p.parseCast()
}

// parseCast handles `expr as Type`. The `as` operator sits between
// unary and the multiplicative tier — `1 + 2 as i64` parses as
// `1 + (2 as i64)`, matching Rust. Chained casts (`x as i32 as
// i64`) are left-associative.
func (p *parser) parseCast() (ast.Expr, error) {
	expr, err := p.parseCall()
	if err != nil {
		return nil, err
	}
	for p.match(lexer.Keyword, "as") {
		kw := p.advance()
		target, err := p.parseType()
		if err != nil {
			return nil, err
		}
		expr = &ast.CastExpr{P: kw.Pos, Inner: expr, Target: target}
	}
	return expr, nil
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
			// Slicing distinguishes from indexing by the `:`
			// separator: `arr[i]` indexes, `arr[a:b]` /
			// `arr[a:]` / `arr[:b]` slices. Either bound is
			// optional; `arr[:]` is reserved (no use case yet —
			// errors out).
			if _, ok := p.accept(lexer.Punct, ":"); ok {
				// `[:b]` form — low is implicitly 0.
				var high ast.Expr
				if !p.match(lexer.Punct, "]") {
					h, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					high = h
				}
				if _, err := p.expect(lexer.Punct, "]"); err != nil {
					return nil, err
				}
				if high == nil {
					return nil, p.errorf(open.Pos, "`[:]` slice form is reserved; use `arr[0:len(arr)]` for now")
				}
				expr = &ast.SliceExpr{P: open.Pos, Source: expr, High: high}
				continue
			}
			first, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, ok := p.accept(lexer.Punct, ":"); ok {
				// `[a:b]` or `[a:]` form.
				var high ast.Expr
				if !p.match(lexer.Punct, "]") {
					h, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					high = h
				}
				if _, err := p.expect(lexer.Punct, "]"); err != nil {
					return nil, err
				}
				expr = &ast.SliceExpr{P: open.Pos, Source: expr, Low: first, High: high}
				continue
			}
			if _, err := p.expect(lexer.Punct, "]"); err != nil {
				return nil, err
			}
			expr = &ast.Index{P: open.Pos, Array: expr, Idx: first}
		case p.match(lexer.Punct, "."):
			dot := p.advance()
			// Tuple field access uses a numeric selector (`pair.0`,
			// `pair.1`). The lexer hands these back as a `Number`
			// token; reuse the FieldAccess shape with a stringified
			// index so codegen can stay uniform.
			if num := p.peek(); num.Kind == lexer.Number {
				p.advance()
				expr = &ast.FieldAccess{P: dot.Pos, Target: expr, Field: num.Text}
				continue
			}
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
		// are no other constructs of that shape. Suppressed in
		// `if let` source positions where the trailing `{` opens
		// the if-body.
		if !p.noStructLit && p.match(lexer.Punct, "{") {
			return p.parseStructLit(t.Pos, t.Text)
		}
		return &ast.Ident{P: t.Pos, Name: t.Text}, nil
	case lexer.Punct:
		switch t.Text {
		case "(":
			// `(e)` is grouping; `(e1, e2, ...)` (>=2 elements) is
			// a tuple literal. Single-element "tuples" don't exist
			// as a syntactic form — `(e)` always groups.
			open := p.advance()
			first, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, isComma := p.accept(lexer.Punct, ","); !isComma {
				if _, err := p.expect(lexer.Punct, ")"); err != nil {
					return nil, err
				}
				return first, nil
			}
			elems := []ast.Expr{first}
			for {
				if p.match(lexer.Punct, ")") {
					break // trailing comma allowed
				}
				e, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				elems = append(elems, e)
				if _, ok := p.accept(lexer.Punct, ","); !ok {
					break
				}
			}
			if _, err := p.expect(lexer.Punct, ")"); err != nil {
				return nil, err
			}
			return &ast.TupleLit{P: open.Pos, Elems: elems}, nil
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
