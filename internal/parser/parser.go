// Package parser is a hand-written recursive-descent parser that turns a
// token stream into an *ast.Program.
//
// Precedence climbs from `parseAssign` (lowest) down through logical-or,
// logical-and, equality, relational, additive, multiplicative, unary,
// and finally `parseCall` / `parsePrimary`.
package parser

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/lexer"
)

type Error struct {
	Pos     ast.Position
	Msg     string
	Path    string // source file path; populated by modload, empty otherwise
	ErrCode string // optional: stable error code (P001…); surfaces in the header + `lang explain` output
}

func (e *Error) Error() string          { return fmt.Sprintf("parse error at %s: %s", e.Pos, e.Msg) }
func (e *Error) Position() ast.Position { return e.Pos }
func (e *Error) File() string           { return e.Path }
func (e *Error) setFile(p string)       { e.Path = p }
func (e *Error) Code() string           { return e.ErrCode }

// Parse turns source into a Program, lexing along the way. The parser
// recovers from per-statement and per-function errors and continues so
// it can report many problems in one pass; the returned error (if any)
// is a diag.Errors of every problem found.
//
// Comments captured by the lexer ride along on prog.Comments — the
// parser doesn't otherwise consume them, leaving the formatter (or
// any other tooling pass) free to walk them in source order.
func Parse(src string) (*ast.Program, error) {
	return ParseContext(context.Background(), src)
}

// ParseContext is the context-aware sibling of Parse — checks
// the context at each top-level declaration boundary so a long
// parse (large file, slow input) can be cancelled mid-flight by
// the LSP when a new edit invalidates the in-progress result.
// See docs/IDE-COMPILATION-RESEARCH.md Rec §1.
//
// On cancel, returns (nil, ctx.Err()) — caller distinguishes a
// real parse error from a cancellation via `errors.Is(err,
// context.Canceled)` / `context.DeadlineExceeded`. Same shape
// as Go's net/http and database/sql contexts.
//
// Lex stays synchronous (it's already O(n) and fast); the
// cancellation grain is per-top-level-decl. A 100-decl file
// gets ~100 cancellation checkpoints, which is fine for the
// ~10ms-per-keystroke budget the LSP targets.
func ParseContext(ctx context.Context, src string) (*ast.Program, error) {
	tokens, comments, err := lexer.Tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens, ctx: ctx}
	prog := p.parseProgram()
	prog.Comments = comments
	prog.TypeRefs = p.typeRefs
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(p.errors) > 0 {
		return prog, diag.Errors(p.errors)
	}
	return prog, nil
}

// parseExprFromText runs a one-shot parser over `text` and returns
// the resulting Expr. Used to sub-parse an f-string interpolant
// — the lexer hands the raw expression text in for the parser to
// turn into an AST. Anything beyond a single expression is an
// error.
func parseExprFromText(text string, pos ast.Position) (ast.Expr, error) {
	tokens, _, err := lexer.Tokenize(text)
	if err != nil {
		return nil, &Error{Pos: pos, Msg: fmt.Sprintf("f-string interpolation: %v", err)}
	}
	p := &parser{tokens: tokens}
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind != lexer.EOF {
		return nil, p.errorf(p.peek().Pos, "f-string interpolation: unexpected trailing tokens after expression")
	}
	if len(p.errors) > 0 {
		return nil, diag.Errors(p.errors)
	}
	return expr, nil
}

type parser struct {
	tokens []lexer.Token
	i      int
	errors []error
	// ctx is the LSP-driven cancellation token. Checked at
	// each top-level decl boundary; on cancel, parseProgram
	// returns early. Sub-parsers (the one-shot parseExprFrom-
	// Text for f-string interpolants) inherit context.Background
	// — they're single-expression parses where the per-decl
	// cancellation grain doesn't apply.
	ctx context.Context
	// typeRefs accumulates source-position records for every
	// named-type reference parseType encounters. Drained into
	// prog.TypeRefs at the end of Parse. The LSP uses this side
	// table for hover / definition on type annotations — ast.Type
	// values themselves are positionless. Sub-parsers (e.g. the
	// one-shot parseExprFromText for f-string interpolants) get
	// their own *parser and their typeRefs are discarded.
	typeRefs []ast.TypeRef
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

// errorfCode is the code-stamping sibling of errorf — assigns
// a stable error code (docs/DIAGNOSTIC-UX-RESEARCH.md Rec §4)
// to the parse error. P-prefixed codes line up with the
// per-code catalogue under `internal/diag/explanations/`;
// surfacing them in the header lets users search for the
// error + look up the long-form explanation via
// `lang explain CODE`.
func (p *parser) errorfCode(pos ast.Position, code, format string, args ...any) *Error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, args...), ErrCode: code}
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
		return lexer.Token{}, p.errorfCode(t.Pos, "P001", "expected %q, got %q", want, t.Text)
	}
	return p.advance(), nil
}

// ---------- Program / declarations ----------

func (p *parser) parseProgram() *ast.Program {
	prog := &ast.Program{}
	for !p.match(lexer.EOF, "") {
		// Per-decl cancellation checkpoint. nil ctx (sub-parser
		// path that bypassed the struct init) skips the check.
		if p.ctx != nil && p.ctx.Err() != nil {
			return prog
		}
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
				!p.match(lexer.Keyword, "type") &&
				!p.match(lexer.Keyword, "const") {
				p.errors = append(p.errors, p.errorf(pubTok.Pos,
					"`pub` must be followed by `function`, `struct`, `enum`, `type`, or `const`"))
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
		if p.match(lexer.Keyword, "type") {
			ud, err := p.parseUnionDecl()
			if err != nil {
				p.errors = append(p.errors, err)
				p.syncToTopLevel()
				if p.i == before {
					p.advance()
				}
				continue
			}
			if ud != nil {
				ud.Public = isPub
				prog.Unions = append(prog.Unions, ud)
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
// without the `.fern` extension. The path is otherwise opaque to
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
// any directory prefix and a trailing `.fern` extension.
func importLocalName(path string) string {
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if strings.HasSuffix(base, ".fern") {
		base = base[:len(base)-len(".fern")]
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
	// Generic method type parameters: `function [T] (b: Box[T])
	// name(...)`. The type params come BEFORE the receiver so
	// the receiver's type (`Box[T]`) can reference them — by
	// the time we parse `Box[T]`, T is already known as a
	// type parameter and resolveType rewrites it as ParamType.
	// Non-method generic functions still write their type
	// params after the name (`function name[T](x: T): T`), so
	// we collect this leading-position form into the same
	// `typeParams` slot.
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
		receiver = &ast.Param{Name: rname.Text, NamePos: rname.Pos, Type: rtype}
	}
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	funcNamePos := name.Pos
	// Optional type parameters AFTER the name (non-method form):
	// `function id[T](x: T): T`. For methods, the type params
	// already got picked up in the leading-position block above —
	// the post-name `[T]` is rejected here (would conflict with
	// the call-site `[T1, T2](...)` shape).
	if p.match(lexer.Punct, "[") && len(typeParams) == 0 {
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
			params = append(params, ast.Param{Name: pname.Text, NamePos: pname.Pos, Type: ptype})
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
		NamePos:    funcNamePos,
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
			fields = append(fields, ast.Param{Name: fname.Text, NamePos: fname.Pos, Type: ft})
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

// parseUnionDecl parses `type Name = A | B | C;` — a closed
// sum over named struct types. The first cut requires:
//
//   - at least two members (`type X = A;` is rejected — a
//     one-member union is uselessly an alias);
//   - each member is a plain Ident (no generic args, no array
//     suffix, no nested type expression). The checker
//     resolves each name against `info.Structs`.
//
// Source shape: `type Expr = Binary | Unary | Call;`. The
// `=` keeps the syntax close to type aliases in Go / Rust /
// TS; the `|` between members reads naturally as alternation.
// Statement-terminating `;` matches every other top-level
// decl in the language.
func (p *parser) parseUnionDecl() (*ast.UnionDecl, error) {
	kw, err := p.expect(lexer.Keyword, "type")
	if err != nil {
		return nil, err
	}
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, "="); err != nil {
		return nil, err
	}
	var members []string
	first, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	members = append(members, first.Text)
	for p.match(lexer.Punct, "|") {
		p.advance()
		mem, err := p.expect(lexer.Ident, "")
		if err != nil {
			return nil, err
		}
		members = append(members, mem.Text)
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	if len(members) < 2 {
		return nil, p.errorf(name.Pos, "union %q must list at least two struct members (use a struct alias for a single type)", name.Text)
	}
	return &ast.UnionDecl{P: kw.Pos, Name: name.Text, Members: members}, nil
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
	case t.Kind == lexer.Keyword && t.Text == "i32":
		// Canonical 32-bit signed integer. Stored as the zero-
		// value `NumberType{Spelling: "i32"}` so historical
		// equality checks against `ast.NumberType{}` keep
		// working (NormalWidth maps Width=0 to 32).
		p.advance()
		base = ast.NumberType{Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "i64":
		p.advance()
		base = ast.NumberType{Width: 64, Signed: true, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "i8":
		p.advance()
		base = ast.NumberType{Width: 8, Signed: true, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "i16":
		p.advance()
		base = ast.NumberType{Width: 16, Signed: true, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "u32":
		p.advance()
		base = ast.NumberType{Width: 32, Signed: false, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "u64":
		p.advance()
		base = ast.NumberType{Width: 64, Signed: false, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "u8":
		p.advance()
		base = ast.NumberType{Width: 8, Signed: false, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "u16":
		p.advance()
		base = ast.NumberType{Width: 16, Signed: false, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "usize":
		// usize is target-aware native-pointer-width unsigned.
		// `ast.WidthPtr` (-1) is the sentinel; backends resolve
		// it to 4 bytes on wasm32 / 8 bytes on natives. Unsigned
		// matches Rust/Zig conventions and the typical "size of
		// a thing in memory" semantics — wraparound is the
		// expected discipline.
		p.advance()
		base = ast.NumberType{Width: ast.WidthPtr, Signed: false, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "f64":
		p.advance()
		base = ast.FloatType{Width: 64, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "f32":
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
		} else if len(elems) == 1 {
			// Single-element parens act as a grouping wrapper —
			// useful when the inner type already has its own `(`/`)`
			// and the outer parens are there to host a suffix (e.g.
			// `((i32) => i32)[]` for an array of function values).
			// The arrow-form FuncType above already consumed the
			// `=>` case; reaching here means the inner type was
			// fully resolved on its own (e.g. an inner FuncType
			// produced by recursion) and the outer parens just
			// group.
			base = elems[0]
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
		// Record the type-name position for the LSP. The Name we
		// store is the full source spelling (including any
		// `mod.` qualifier) so lookup keys match what modload
		// will eventually rewrite to in the checker's Structs /
		// Enums maps.
		p.typeRefs = append(p.typeRefs, ast.TypeRef{P: t.Pos, Name: name})
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
		return nil, p.errorfCode(t.Pos, "P001", "expected type, got %q", t.Text)
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
		case "defer":
			return p.parseDefer()
		case "arena":
			return p.parseArena()
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

// parseLambda parses `function (params): R { body }` in
// expression position. The `function` keyword has already been
// peeked (not yet consumed) by parsePrimary; parseLambda
// consumes it and reads the unnamed-function shape. The body is
// parsed inside the parser's standard return-type stack so
// `use` desugaring inside the lambda body picks up the right
// callback return type.
func (p *parser) parseLambda() (ast.Expr, error) {
	kw := p.advance() // function
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
	p.returnTypeStack = append(p.returnTypeStack, ret)
	body, err := p.parseBlock()
	p.returnTypeStack = p.returnTypeStack[:len(p.returnTypeStack)-1]
	if err != nil {
		return nil, err
	}
	return &ast.Lambda{P: kw.Pos, Params: params, ReturnType: ret, Body: body}, nil
}

// parseIfExpr parses the expression form `if (cond) { e1 } else
// { e2 }`. Each arm is a single expression — no semicolon, no
// statement list. The construct exists so the language can drop
// the ternary `cond ? e1 : e2` (whose `?` is needed for the
// postfix Option-try operator). Statement-form `if (cond) {
// stmts; }` is unaffected: parseStmt dispatches `if` before
// expression parsing ever sees it.
func (p *parser) parseIfExpr() (ast.Expr, error) {
	kw := p.advance() // `if`
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
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, err
	}
	thenE, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Keyword, "else"); err != nil {
		return nil, err
	}
	// `else if (...) { ... } else { ... }` — chain via recursion.
	// The recursive IfExpr stands in for the else arm, and its own
	// `else` covers the trailing branch (which itself may be
	// another `else if` chain). The recursive call enforces a
	// final `else { ... }` so the IfExpr is total — matches the
	// existing two-arm constraint.
	if p.match(lexer.Keyword, "if") {
		elseE, err := p.parseIfExpr()
		if err != nil {
			return nil, err
		}
		return &ast.IfExpr{P: kw.Pos, Cond: cond, Then: thenE, Else: elseE}, nil
	}
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, err
	}
	elseE, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	return &ast.IfExpr{P: kw.Pos, Cond: cond, Then: thenE, Else: elseE}, nil
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

	// Map foreach shape: `for (K, V) in expr body`. The opening `(`
	// is shared with the C-style for, so disambiguate by peeking the
	// fixed `( IDENT , IDENT ) in` prefix. C-style starts with `var`,
	// `;`, or an arbitrary expression — none of them match this
	// pattern, so the lookahead is unambiguous.
	if p.match(lexer.Punct, "(") && p.i+5 < len(p.tokens) {
		t1, t2, t3, t4, t5 := p.tokens[p.i+1], p.tokens[p.i+2], p.tokens[p.i+3], p.tokens[p.i+4], p.tokens[p.i+5]
		if t1.Kind == lexer.Ident &&
			t2.Kind == lexer.Punct && t2.Text == "," &&
			t3.Kind == lexer.Ident &&
			t4.Kind == lexer.Punct && t4.Text == ")" &&
			t5.Kind == lexer.Ident && t5.Text == "in" {
			return p.parseForEachMapTuple(kw)
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
	// Suppress trailing struct-literal parsing while reading the
	// source — the `{` that follows opens the loop body, not a
	// struct lit.
	prevNS := p.noStructLit
	p.noStructLit = true
	expr, err := p.parseExpr()
	p.noStructLit = prevNS
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
	// var __foreach_len_N = __foreach_iter_N.len();
	declLen := &ast.Var{P: kw.Pos, Name: lenName, Init: &ast.Call{
		P: kw.Pos,
		Callee: &ast.FieldAccess{
			P:        kw.Pos,
			Target:   mkIdent(iterName),
			Field:    "len",
			FieldPos: kw.Pos,
		},
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

// parseForEachMapTuple desugars `for (K, V) in expr body` — the
// only form this language supports for iterating a Map. Builds on
// the MapIter cursor API (`m.iter()` / `it.has_next()` / `it.key()`
// / `it.value()` / `it.advance()`) so map iteration walks entries
// in insertion order without per-iteration allocation. The shape
// after desugaring matches the array foreach as closely as
// possible — outer Block scopes the iterator slot, the inner For's
// Step slot calls advance() so `continue` advances before the next
// has_next() check.
//
//	{
//	  var __foreach_iter_N = expr.iter();
//	  for (; __foreach_iter_N.has_next(); __foreach_iter_N.advance()) {
//	    var K = __foreach_iter_N.key();
//	    var V = __foreach_iter_N.value();
//	    <body>
//	  }
//	}
//
// Like the array foreach, K / V are inferred from the iterator's
// method return types — no annotation needed at the loop site.
func (p *parser) parseForEachMapTuple(kw lexer.Token) (ast.Stmt, error) {
	p.advance() // `(`
	keyTok := p.advance()
	p.advance() // `,`
	valTok := p.advance()
	p.advance() // `)`
	p.advance() // `in`

	// Same reason as parseForEach: don't let the loop-body `{` get
	// glued onto the source expression as a struct literal.
	prevNS := p.noStructLit
	p.noStructLit = true
	expr, err := p.parseExpr()
	p.noStructLit = prevNS
	if err != nil {
		return nil, err
	}
	body, err := p.parseStmt()
	if err != nil {
		return nil, err
	}

	id := p.nextForeachID()
	iterName := fmt.Sprintf("__foreach_iter_%d", id)

	iterIdent := func() *ast.Ident { return &ast.Ident{P: kw.Pos, Name: iterName} }
	callOnIter := func(method string) *ast.Call {
		return &ast.Call{
			P:      kw.Pos,
			Callee: &ast.FieldAccess{P: kw.Pos, Target: iterIdent(), Field: method},
		}
	}

	// var __foreach_iter_N = expr.iter();
	declIter := &ast.Var{P: kw.Pos, Name: iterName, Init: &ast.Call{
		P:      kw.Pos,
		Callee: &ast.FieldAccess{P: kw.Pos, Target: expr, Field: "iter"},
	}}

	bindKey := &ast.Var{P: keyTok.Pos, Name: keyTok.Text, Init: callOnIter("key")}
	bindVal := &ast.Var{P: valTok.Pos, Name: valTok.Text, Init: callOnIter("value")}
	stepStmt := &ast.ExprStmt{P: kw.Pos, Expr: callOnIter("advance")}

	innerStmts := []ast.Stmt{bindKey, bindVal}
	if blk, ok := body.(*ast.Block); ok {
		innerStmts = append(innerStmts, blk.Stmts...)
	} else {
		innerStmts = append(innerStmts, body)
	}
	innerBlock := &ast.Block{P: kw.Pos, Stmts: innerStmts}

	forLoop := &ast.For{
		P:    kw.Pos,
		Cond: callOnIter("has_next"),
		Step: stepStmt,
		Body: innerBlock,
	}

	return &ast.Block{
		P:     kw.Pos,
		Stmts: []ast.Stmt{declIter, forLoop},
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
			return nil, p.errorfCode(t.Pos, "P001", "expected `case`, `default` or `}` in switch body")
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
// peekTypeArgs reports whether the `[` at p.peek() opens a
// generic call-site type-args list (`f[i32](args)`) as opposed
// to an indexing / slicing `[...]`. The cheap-and-correct
// disambiguator: the first token AFTER the `[` must be a
// type-keyword (i32, u32, string, boolean, void, f32, f64,
// usize, ...) — those tokens can't appear in an indexing
// expression. The closing `]` followed by `(` is required for
// the type-args interpretation; if either condition fails the
// caller falls through to the regular Index / Slice handling.
// Doesn't consume tokens.
func (p *parser) peekTypeArgs() bool {
	if !p.match(lexer.Punct, "[") {
		return false
	}
	if p.i+1 >= len(p.tokens) {
		return false
	}
	next := p.tokens[p.i+1]
	if next.Kind != lexer.Keyword {
		return false
	}
	switch next.Text {
	case "i8", "i16", "i32", "i64",
		"u8", "u16", "u32", "u64",
		"usize", "f32", "f64",
		"string", "boolean", "void":
		// fallthrough — keep walking to find `]` followed by `(`
	default:
		return false
	}
	// Walk forward looking for the matching `]` at the same
	// bracket depth. Then check the token after is `(`.
	depth := 1
	for j := p.i + 1; j < len(p.tokens); j++ {
		t := p.tokens[j]
		if t.Kind == lexer.Punct {
			switch t.Text {
			case "[":
				depth++
			case "]":
				depth--
				if depth == 0 {
					if j+1 < len(p.tokens) {
						nt := p.tokens[j+1]
						return nt.Kind == lexer.Punct && nt.Text == "("
					}
					return false
				}
			}
		}
	}
	return false
}

// isLiteralPatternStart reports whether the token at hand opens
// a literal pattern in match-arm position — a NumberLit,
// FloatLit, StringLit, or the `true` / `false` keywords. Variant
// names live in lexer.Ident space and are handled by the
// surrounding parseMatchArm branch.
func isLiteralPatternStart(t lexer.Token) bool {
	switch t.Kind {
	case lexer.Number, lexer.Float, lexer.String:
		return true
	}
	if t.Kind == lexer.Keyword && (t.Text == "true" || t.Text == "false") {
		return true
	}
	return false
}

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
	} else if isLiteralPatternStart(t) {
		// Literal pattern: `0 => …`, `"yes" => …`, `true => …`,
		// `1.5f64 => …`. Dispatched via equality comparison
		// against the scrutinee at IR-lower time. The checker
		// verifies the literal's type unifies with the
		// scrutinee's type.
		lit, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		arm.Literal = lit
	} else if t.Kind == lexer.Ident {
		p.advance()
		arm.VariantName = t.Text
		// Optional `mod.` qualifier: `lexer.TokA(x) => …`. When the
		// next token is `.`, the ident we just consumed was the
		// module name, not the variant — re-consume after the dot.
		// The checker verifies the qualifier against the scrutinee
		// enum's source module.
		if p.match(lexer.Punct, ".") {
			p.advance()
			nameTok, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			arm.VariantModule = arm.VariantName
			arm.VariantName = nameTok.Text
		}
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
		return nil, p.errorfCode(t.Pos, "P001", "expected variant pattern, literal, or `_` in match arm, got %s", t.Text)
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

// parseMatchExpr is the expression-position form of `match`. Same
// `match (e) { … }` shell as the statement form but each arm body
// is a single expression (no block, no statements, no semicolons),
// and the whole construct evaluates to the unified arm type. Sits
// alongside parseIfExpr — both are dispatched from parsePrimary's
// keyword switch, so the parser knows it's in expression context.
func (p *parser) parseMatchExpr() (ast.Expr, error) {
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
	m := &ast.MatchExpr{P: kw.Pos, Tag: tag}
	for !p.match(lexer.Punct, "}") {
		arm, err := p.parseMatchExprArm()
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

// parseMatchExprArm parses one expression-form arm: `<pattern>
// [when <guard>] => <expr>`. The pattern parsing (variant /
// payload bindings / wildcard) is identical to parseMatchArm; the
// only difference is body parsing — a single Expr rather than a
// Block.
func (p *parser) parseMatchExprArm() (*ast.MatchExprArm, error) {
	t := p.peek()
	arm := &ast.MatchExprArm{P: t.Pos}
	if t.Kind == lexer.Ident && t.Text == "_" {
		p.advance()
		arm.IsWildcard = true
	} else if isLiteralPatternStart(t) {
		// Literal pattern in match-expr arm; same semantics as
		// the stmt-form parseMatchArm path.
		lit, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		arm.Literal = lit
	} else if t.Kind == lexer.Ident {
		p.advance()
		arm.VariantName = t.Text
		// Optional `mod.` qualifier — same handling as parseMatchArm.
		if p.match(lexer.Punct, ".") {
			p.advance()
			nameTok, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			arm.VariantModule = arm.VariantName
			arm.VariantName = nameTok.Text
		}
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
		return nil, p.errorfCode(t.Pos, "P001", "expected variant pattern, literal, or `_` in match arm, got %s", t.Text)
	}
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
	body, err := p.parseExpr()
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

// parseDefer parses `defer EXPR;`. The IR collects every Defer
// statement in the function body and emits the deferred
// expressions in LIFO order before each return + at the end of
// the function. Conditional defers (registered inside a branch
// that didn't run at runtime) are skipped via per-defer
// "active" flags the IR builder synthesises.
func (p *parser) parseDefer() (ast.Stmt, error) {
	kw := p.advance()
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.Defer{P: kw.Pos, Expr: expr}, nil
}

// parseArena parses `arena { … }` — a syntactic scope whose
// allocations are reclaimed when the block exits. Lowers to
// `arena_save → body → arena_restore` so the bump-allocator
// cursor snaps back. Caller has consumed nothing yet; we
// advance past `arena`, demand `{`, and delegate to
// parseBlock.
func (p *parser) parseArena() (ast.Stmt, error) {
	kw := p.advance() // arena
	if !p.match(lexer.Punct, "{") {
		return nil, p.errorf(kw.Pos, "arena requires a `{ … }` block")
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.Arena{P: kw.Pos, Body: body}, nil
}

func (p *parser) parseVar() (ast.Stmt, error) {
	kw := p.advance()
	// Tuple-destructuring form: `var (a, b, ...) = expr;`. Mirrors
	// `let (a, b, ...) = expr;` (handled by parseTupleDestructure)
	// but uses the `var` keyword to keep the source surface uniform
	// with regular `var name = expr;` declarations. Both forms
	// produce the same `*ast.Destructure` AST node.
	if p.match(lexer.Punct, "(") {
		return p.parseTupleDestructure(kw.Pos)
	}
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	var typ ast.Type
	wasAnnotated := false
	if _, ok := p.accept(lexer.Punct, ":"); ok {
		t, err := p.parseType()
		if err != nil {
			return nil, err
		}
		typ = t
		wasAnnotated = true
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
	return &ast.Var{P: kw.Pos, Name: name.Text, Type: typ, Init: init, WasAnnotated: wasAnnotated}, nil
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
	// `: TYPE` is optional. Without it, the checker infers the
	// param type from the receiving call's signature (the last
	// param of which is a function-typed callback whose first
	// param is what we want).
	var bindType ast.Type
	if _, ok := p.accept(lexer.Punct, ":"); ok {
		bindType, err = p.parseType()
		if err != nil {
			return err
		}
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
	if bindType == nil {
		// Defer param-type inference to the checker: it'll peek
		// at srcCall's callee, find the trailing function-typed
		// param, and stamp this callback's first param accordingly.
		cb.UseInferSource = srcCall
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
	// `let (a, b, ...) = expr;` — tuple destructuring shorthand.
	// No `else` branch: a tuple is statically arity-checked, so
	// the binding can't fail at runtime the way enum
	// destructuring can.
	if p.match(lexer.Punct, "(") {
		return p.parseTupleDestructure(kw.Pos)
	}
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

// parseTupleDestructure handles `let (a, b, …) = expr;` —
// position-based binding into the enclosing scope from a
// tuple-typed expression. Caller has consumed `let`; the
// upcoming `(` opens the binding list. At least 2 names
// are required (matches the no-singleton-tuples rule).
func (p *parser) parseTupleDestructure(letPos ast.Position) (ast.Stmt, error) {
	if _, err := p.expect(lexer.Punct, "("); err != nil {
		return nil, err
	}
	var names []string
	if !p.match(lexer.Punct, ")") {
		for {
			nameTok, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			names = append(names, nameTok.Text)
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
	if len(names) < 2 {
		return nil, p.errorf(letPos, "tuple destructure needs at least 2 names")
	}
	if _, err := p.expect(lexer.Punct, "="); err != nil {
		return nil, err
	}
	src, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.Destructure{P: letPos, Names: names, Init: src}, nil
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
			return nil, p.errorfCode(eq.Pos, "P003", "left-hand side of assignment is not assignable")
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
				return nil, p.errorfCode(tok.Pos, "P003", "left-hand side of assignment is not assignable")
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
	left, err := p.parseLogicOr()
	if err != nil {
		return nil, err
	}
	for p.match(lexer.Punct, "|>") {
		pipeTok := p.advance()
		right, err := p.parseLogicOr()
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
			// Generic call-site type arguments: `f[i32](args)` /
			// `pair[i32, string](a, b)`. Disambiguated from array
			// indexing by speculative parse: if the bracket
			// content parses as a comma-separated list of types
			// AND is followed by `(`, it's a type-args call.
			// Otherwise rewind and fall through to the Index /
			// Slice path. Required: at least one token inside the
			// brackets must be a TYPE KEYWORD (i32, u32, string,
			// boolean, etc.), which is unambiguously a type and
			// can't appear as an indexing expression. This keeps
			// `arr[i]` working unchanged while unlocking the
			// generic-instantiation syntax for the inference
			// cases that don't get help from arguments alone.
			if p.peekTypeArgs() {
				open := p.advance() // [
				var typeArgs []ast.Type
				for {
					t, err := p.parseType()
					if err != nil {
						return nil, err
					}
					typeArgs = append(typeArgs, t)
					if _, ok := p.accept(lexer.Punct, ","); !ok {
						break
					}
				}
				if _, err := p.expect(lexer.Punct, "]"); err != nil {
					return nil, err
				}
				if _, err := p.expect(lexer.Punct, "("); err != nil {
					return nil, err
				}
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
				expr = &ast.Call{P: open.Pos, Callee: expr, Args: args, TypeArgs: typeArgs}
				continue
			}
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
				numTok := p.advance()
				expr = &ast.FieldAccess{P: dot.Pos, Target: expr, Field: numTok.Text, FieldPos: numTok.Pos}
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
			expr = &ast.FieldAccess{P: dot.Pos, Target: expr, Field: fname.Text, FieldPos: fname.Pos}
		case p.match(lexer.Punct, "?"):
			// Postfix `?` — Option-try operator. `expr?` evaluates
			// to the Some payload and early-returns None when the
			// source was None. Validity (Inner is Option, enclosing
			// fn returns Option) is enforced by the checker; the
			// IR does the tag-compare + early-return lowering.
			tok := p.advance()
			expr = &ast.TryOp{P: tok.Pos, Inner: expr}
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
			fields = append(fields, ast.FieldInit{Name: fname.Text, NamePos: fname.Pos, Value: val})
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

// parseMapLit parses `Map { key: value, key: value, ... }`. Both
// keys and values are arbitrary expressions; trailing commas are
// allowed. Empty `Map {}` is also valid and produces an empty
// map. Lowering happens at IR-build time — no runtime difference
// from `var m = map_new(N); m.set(k, v); ...`.
func (p *parser) parseMapLit(pos ast.Position) (ast.Expr, error) {
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, err
	}
	var entries []ast.MapEntry
	for !p.match(lexer.Punct, "}") {
		key, err := p.parseExpr()
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
		entries = append(entries, ast.MapEntry{Key: key, Value: val})
		if _, ok := p.accept(lexer.Punct, ","); !ok {
			break
		}
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	return &ast.MapLit{P: pos, Entries: entries}, nil
}

func (p *parser) parsePrimary() (ast.Expr, error) {
	t := p.peek()
	switch t.Kind {
	case lexer.Number:
		p.advance()
		var n int64
		if len(t.Text) > 2 && t.Text[0] == '0' && (t.Text[1] == 'x' || t.Text[1] == 'X') {
			// Hex literal: parse the digits after the `0x` prefix.
			// Width up to 64 bits so `0xFFFFFFFF` round-trips; the
			// checker applies the same range rules as decimal.
			v, err := strconv.ParseInt(t.Text[2:], 16, 64)
			if err != nil {
				p.errors = append(p.errors, p.errorf(t.Pos, "invalid hex literal %q: %v", t.Text, err))
			}
			n = v
		} else {
			for _, c := range t.Text {
				n = n*10 + int64(c-'0')
			}
		}
		lit := &ast.NumberLit{P: t.Pos, Value: n}
		// Typed suffix (`42i64`, `7u8`): stamp Width + IsUnsigned
		// at parse time so the checker sees a non-polymorphic
		// type immediately, bypassing settle-from-context flow.
		switch t.Suffix {
		case "":
			// no suffix — polymorphic
		case "i8":
			lit.Width = 8
		case "i16":
			lit.Width = 16
		case "i32":
			lit.Width = 32
		case "i64":
			lit.Width = 64
		case "u8":
			lit.Width = 8
			lit.IsUnsigned = true
		case "u16":
			lit.Width = 16
			lit.IsUnsigned = true
		case "u32":
			lit.Width = 32
			lit.IsUnsigned = true
		case "u64":
			lit.Width = 64
			lit.IsUnsigned = true
		default:
			return nil, p.errorfCode(t.Pos, "P002", "unexpected numeric suffix %q on integer literal", t.Suffix)
		}
		return lit, nil
	case lexer.Float:
		p.advance()
		// `42f32` — integer-shaped text with a float suffix. Use
		// strconv.ParseFloat which handles both `42` and `1.5`.
		f, err := strconv.ParseFloat(t.Text, 64)
		if err != nil {
			return nil, p.errorfCode(t.Pos, "P002", "invalid float literal %q: %v", t.Text, err)
		}
		lit := &ast.FloatLit{P: t.Pos, Value: f}
		switch t.Suffix {
		case "", "f32":
			lit.Width = 32
		case "f64":
			lit.Width = 64
		default:
			return nil, p.errorfCode(t.Pos, "P002", "unexpected numeric suffix %q on float literal", t.Suffix)
		}
		// Empty suffix → leave Width=0 so the checker can still
		// settle to f64 from context. Non-empty `f32`/`f64` was
		// stamped above.
		if t.Suffix == "" {
			lit.Width = 0
		}
		return lit, nil
	case lexer.String:
		p.advance()
		return &ast.StringLit{P: t.Pos, Value: t.Text}, nil
	case lexer.FString:
		p.advance()
		// Build the AST node by sub-parsing each interpolant Expr
		// part's raw text. The lexer has already split literal /
		// interpolant pieces; we just need to parse the Expr text
		// into an ast.Expr per interpolant. Empty f-strings produce
		// an FString with no parts — the IR lowers it to "".
		var parts []ast.FStringPart
		for _, fp := range t.FParts {
			if fp.Expr != "" {
				expr, err := parseExprFromText(fp.Expr, t.Pos)
				if err != nil {
					return nil, err
				}
				parts = append(parts, ast.FStringPart{Expr: expr})
			} else {
				parts = append(parts, ast.FStringPart{Lit: fp.Lit})
			}
		}
		return &ast.FString{P: t.Pos, Parts: parts}, nil
	case lexer.Keyword:
		switch t.Text {
		case "true":
			p.advance()
			return &ast.BoolLit{P: t.Pos, Value: true}, nil
		case "false":
			p.advance()
			return &ast.BoolLit{P: t.Pos, Value: false}, nil
		case "if":
			// `if (cond) { e1 } else { e2 }` in expression
			// position — replaces the ternary `cond ? e1 : e2`.
			// Statement-form `if (cond) { stmts; }` is parsed
			// from `parseStmt` and uses *ast.If; the two paths
			// don't overlap because parseStmt dispatches `if`
			// before this primary parser ever sees it.
			return p.parseIfExpr()
		case "match":
			// `match (e) { Variant(b) => EXPR, _ => EXPR }` in
			// expression position. Same dispatch story as `if`:
			// parseStmt routes `match` to parseMatch (Stmt) before
			// expression parsing ever sees it, so this branch only
			// fires from a true expression context.
			return p.parseMatchExpr()
		case "function":
			// Anonymous function literal: `function (x: T): R { body }`.
			// Produces a Lambda expression — same shape as a named
			// local FuncDecl, sans the name. The checker runs
			// capture analysis; closureconv hoists the body to a
			// top-level synthesised name and replaces the Lambda
			// with a MakeClosure at this position.
			//
			// Dispatch story: parseStmt routes `function` to
			// parseFuncDecl (statement) before primary parsing
			// ever sees it for the named form. parsePrimary fires
			// only when `function` appears mid-expression — RHS
			// of `var x = ...`, an argument, a return value, etc.
			return p.parseLambda()
		}
	case lexer.Ident:
		p.advance()
		// `Foo { x: 1, y: 2 }`. The `{` immediately after an identifier
		// can only mean a struct literal in expression position — there
		// are no other constructs of that shape. Suppressed in
		// `if let` source positions where the trailing `{` opens
		// the if-body. Special case: `Map { 1: 10, 2: 20 }` parses
		// as a MapLit instead — the auto-injected `Map` type has no
		// user-accessible fields, so a brace-form here always means
		// a map literal.
		if !p.noStructLit && p.match(lexer.Punct, "{") {
			if t.Text == "Map" {
				return p.parseMapLit(t.Pos)
			}
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
	return nil, p.errorfCode(t.Pos, "P001", "unexpected token %q", t.Text)
}
