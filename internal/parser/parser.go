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
	prog.BlankLines = blankLineNumbers(src)
	prog.TypeRefs = p.typeRefs
	prog.TodoSites = p.todoSites
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(p.errors) > 0 {
		return prog, diag.Errors(p.errors)
	}
	desugarForEachProgram(prog)
	elideLenBoundedChecks(prog)
	return prog, nil
}

// blankLineNumbers returns the 1-based line numbers that are blank
// (whitespace-only) in src. The formatter uses these to preserve an
// author's blank-line grouping inside blocks.
func blankLineNumbers(src string) []int {
	var out []int
	for i, line := range strings.Split(src, "\n") {
		if strings.TrimSpace(line) == "" {
			out = append(out, i+1)
		}
	}
	return out
}

// parseExprFromText runs a one-shot parser over `text` and returns
// the resulting Expr. Used to sub-parse an f-string interpolant
// — the lexer hands the raw expression text in for the parser to
// turn into an AST. Anything beyond a single expression is an
// error.
//
// `base` is where `text` starts in the enclosing file. The re-lex
// numbers `text` from 1:1, so every token is rebased onto `base`
// before parsing — that way both the AST nodes and any parse error
// carry file positions without a second traversal.
func parseExprFromText(text string, base ast.Position) (ast.Expr, error) {
	tokens, _, err := lexer.Tokenize(text)
	if err != nil {
		return nil, &Error{Pos: base, Msg: fmt.Sprintf("f-string interpolation: %v", err)}
	}
	for i := range tokens {
		tokens[i].Pos = offsetPos(base, tokens[i].Pos)
		for j := range tokens[i].FParts {
			tokens[i].FParts[j].Pos = offsetPos(base, tokens[i].FParts[j].Pos)
		}
	}
	p := &parser{tokens: tokens}
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.peek().Kind != lexer.EOF {
		return nil, p.errorfCode(p.peek().Pos, "P001", "f-string interpolation: unexpected trailing tokens after expression")
	}
	if len(p.errors) > 0 {
		return nil, diag.Errors(p.errors)
	}
	return expr, nil
}

// offsetPos maps a position inside a sub-parsed fragment onto the
// enclosing file, given where the fragment starts. Only the fragment's
// first line shares a line with `base`, so only there does the column
// shift.
func offsetPos(base, in ast.Position) ast.Position {
	if in.Line <= 1 {
		return ast.Position{Line: base.Line, Col: base.Col + in.Col - 1}
	}
	return ast.Position{Line: base.Line + in.Line - 1, Col: in.Col}
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
	// todoSites accumulates the position of every `todo;` /
	// `todo("msg");` statement parseTodo desugared, drained into
	// prog.TodoSites at the end of Parse for `-check`'s
	// remaining-stub warnings.
	todoSites []ast.Position
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
	// nestN counts synthesised temps introduced by the nested-match-
	// pattern desugar (`Some(Ok(n))` → `Some(__nestK) => match __nestK …`),
	// so each merged arm's payload temps are uniquely named. Resets per
	// Parse() so the desugared names stay deterministic across runs.
	nestN int
}

func (p *parser) peek() lexer.Token { return p.tokens[p.i] }

// peekAt returns the token n positions ahead of the cursor (n==0 is
// peek()), clamped to the final EOF token so callers never index out of
// range. Used for the small fixed lookahead that distinguishes a nested
// sub-pattern (`Ident(` / `Ident{` / `Ident.`) from a bare binder.
func (p *parser) peekAt(n int) lexer.Token {
	j := p.i + n
	if j >= len(p.tokens) {
		j = len(p.tokens) - 1
	}
	return p.tokens[j]
}

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

// discardName maps a binding name to the name that actually enters scope.
// `_` is a DISCARD wherever a binding is introduced — a var, a tuple- or
// struct-destructure element, or a parameter — so each occurrence gets its
// own unique internal name instead. Two consequences, both intended: two
// discards in one scope no longer collide, and `_` itself is not in scope,
// so reading it back is an undefined-variable error rather than a way to
// retrieve the value you said to throw away.
//
// Renaming rather than binding nothing keeps every downstream consumer
// unchanged — IR lowering, the interpreter and the rc analysis all key a
// local off this name (`b.locals[name]`), so an absent binding would be a
// "no slot" compiler error rather than a discard.
//
// `_` was already a wildcard in match patterns and `for (k, _) in m`, and
// only a binding here; the two compilers had also drifted apart over which
// half was which, so `var (a, _) = t(); var (b, _) = t();` compiled
// self-hosted and was rejected natively. The self-host mirror is
// `discard_name` in examples/self_host/parser.fern.
func discardName(name string, pos ast.Position, nth int) string {
	if name != "_" {
		return name
	}
	return fmt.Sprintf("__discard_%d_%d_%d", pos.Line, pos.Col, nth)
}

// moreElems consumes a comma-separated list's separator and reports whether
// another element follows. It answers false both when there is no comma and
// when the comma is a TRAILING one sitting directly before close — so every
// list routed through it accepts a trailing comma.
//
// Every comma-separated element list goes through this one place on purpose.
// Before it, each of ~11 list loops decided for itself: five spelled
// `if !accept(",") { break }` (which rejects a trailing comma) and six spelled
// `if accept(",") { if match(close) { break }; continue }` (which accepts one).
// The result was a grammar with no rule — trailing commas legal in struct
// literals and illegal in array literals — and the two compilers did not even
// agree on the split, so `function f(a: i32,)` compiled self-hosted and was
// rejected natively.
func (p *parser) moreElems(close string) bool {
	if _, ok := p.accept(lexer.Punct, ","); !ok {
		return false
	}
	return !p.match(lexer.Punct, close)
}

// fipModifierAt reports whether the tokens starting at index i spell a
// `fip` / `fbip` contextual function modifier — the bare ident or the
// graded `fip(<int>)` / `fbip(<int>)` allowance form — in a position
// where it IS a modifier: directly followed by the `function` keyword,
// or by another fip/fbip modifier shape (consumed so the top-level loop
// can report the fip+fbip conflict rather than a generic decl error).
// Returns the modifier name, its parsed allowance (0 for the bare form),
// and the index just past the modifier. Any other shape returns
// ok=false, keeping `fip` / `fbip` usable as ordinary identifiers.
func (p *parser) fipModifierAt(i int) (name string, allowance int, next int, ok bool) {
	if i >= len(p.tokens) || p.tokens[i].Kind != lexer.Ident ||
		(p.tokens[i].Text != "fip" && p.tokens[i].Text != "fbip") {
		return "", 0, 0, false
	}
	name = p.tokens[i].Text
	j := i + 1
	// Optional graded allowance `(<int>)` — a plain (unsuffixed) small
	// integer literal.
	if j+2 < len(p.tokens) && p.tokens[j].Kind == lexer.Punct && p.tokens[j].Text == "(" {
		numTok := p.tokens[j+1]
		if numTok.Kind != lexer.Number || numTok.Suffix != "" {
			return "", 0, 0, false
		}
		if p.tokens[j+2].Kind != lexer.Punct || p.tokens[j+2].Text != ")" {
			return "", 0, 0, false
		}
		n, err := strconv.Atoi(numTok.Text)
		if err != nil || n < 0 {
			return "", 0, 0, false
		}
		allowance = n
		j += 3
	}
	if j >= len(p.tokens) {
		return "", 0, 0, false
	}
	t := p.tokens[j]
	if t.Kind == lexer.Keyword && t.Text == "function" {
		return name, allowance, j, true
	}
	if t.Kind == lexer.Ident && (t.Text == "fip" || t.Text == "fbip") {
		// Only treat the trailing fip/fbip ident as "the sibling modifier"
		// when it is itself a modifier shape — otherwise `fip(3)` followed
		// by an expression ident named fip would misparse.
		if _, _, _, sibOK := p.fipModifierAt(j); sibOK {
			return name, allowance, j, true
		}
	}
	return "", 0, 0, false
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
		// Optional `@derive(Trait, …)` attribute preceding a struct
		// declaration. The checker synthesises a field-wise impl per
		// derived trait. See docs/TRAITS.md.
		var derives []string
		var importIface, importWIT string
		var exportIface, exportWIT string
		mustConsume := false
		inlineHint := ast.InlineHintNone
		if p.match(lexer.Punct, "@") {
			attr, err := p.parseAttribute()
			if err != nil {
				p.errors = append(p.errors, err)
				p.syncToTopLevel()
				if p.i == before {
					p.advance()
				}
				continue
			}
			derives = attr.derives
			importIface = attr.importIface
			importWIT = attr.importWIT
			exportIface = attr.exportIface
			exportWIT = attr.exportWIT
			mustConsume = attr.mustConsume
			inlineHint = attr.inlineHint
		}
		// `pub` is an optional prefix on function, struct, enum, or
		// const decls at the top level. Track it and consume; the
		// inner parser stays unaware of visibility — we stamp the
		// Public flag after the decl is built. A bare `pub` without
		// a following decl is a parse error.
		isPub := false
		isPackage := false
		if p.match(lexer.Keyword, "pub") {
			pubTok := p.advance()
			// `pub(package) <decl>` — package-scoped visibility: exported to
			// other modules in the same package (directory / stdlib) but not
			// to outside consumers. `package` is a contextual ident.
			if _, ok := p.accept(lexer.Punct, "("); ok {
				if _, err := p.expect(lexer.Ident, "package"); err != nil {
					p.errors = append(p.errors, p.errorf(pubTok.Pos, "`pub(...)` only supports `pub(package)`"))
					p.syncToTopLevel()
					if p.i == before {
						p.advance()
					}
					continue
				}
				if _, err := p.expect(lexer.Punct, ")"); err != nil {
					p.errors = append(p.errors, err)
					p.syncToTopLevel()
					if p.i == before {
						p.advance()
					}
					continue
				}
				isPackage = true
			}
			// `pub use "path".{name, …};` — a re-export. Handled here
			// rather than as a normal `pub <decl>` because it produces a
			// PubUse, not a Public-flagged declaration.
			if p.match(lexer.Keyword, "use") {
				pu, err := p.parsePubUse(pubTok.Pos)
				if err != nil {
					p.errors = append(p.errors, err)
					p.syncToTopLevel()
					if p.i == before {
						p.advance()
					}
					continue
				}
				if pu != nil {
					prog.PubUses = append(prog.PubUses, pu)
				}
				continue
			}
			if !p.match(lexer.Keyword, "function") &&
				!p.match(lexer.Keyword, "struct") &&
				!p.match(lexer.Keyword, "enum") &&
				!p.match(lexer.Keyword, "type") &&
				!p.match(lexer.Keyword, "trait") &&
				!p.match(lexer.Keyword, "const") &&
				!p.match(lexer.Ident, "opaque") &&
				!p.match(lexer.Ident, "fip") &&
				!p.match(lexer.Ident, "fbip") &&
				!p.match(lexer.Ident, "async") {
				p.errors = append(p.errors, p.errorf(pubTok.Pos,
					"`pub` must be followed by `function`, `struct`, `enum`, `type`, `trait`, `const`, `opaque`, `fip`, `fbip`, or `async`"))
				p.syncToTopLevel()
				if p.i == before {
					p.advance()
				}
				continue
			}
			// `pub` exports; `pub(package)` is package-scoped (isPackage set
			// above) and is NOT also Public.
			isPub = !isPackage
		}
		// `fip` / `fbip` are contextual modifiers on a function decl (`fip
		// function …`, `pub fbip function …`), each with an optional graded
		// allowance (`fip(2) function …`, `fbip(1) function …`): the checker
		// (E053) verifies the Koka-style fully-in-place shape rule, and the
		// IR layer verifies the emitted allocation behaviour matches the
		// claim (E068 — plan E2', docs/NICHE-BORROWS-PLAN.md). Recognised
		// only when the whole modifier shape is directly followed by
		// `function` (or by the sibling modifier, consumed so the fip+fbip
		// conflict is reported instead of a generic decl error), so both
		// stay usable as ordinary identifiers everywhere else.
		isFip := false
		isFbip := false
		fipAllowance := 0
		for {
			name, allowance, next, ok := p.fipModifierAt(p.i)
			if !ok {
				break
			}
			modPos := p.tokens[p.i].Pos
			switch {
			case (name == "fip" && isFbip) || (name == "fbip" && isFip):
				p.errors = append(p.errors, p.errorf(modPos,
					"a function may be marked `fip` or `fbip`, not both"))
			case (name == "fip" && isFip) || (name == "fbip" && isFbip):
				p.errors = append(p.errors, p.errorf(modPos,
					"duplicate `%s` modifier", name))
			}
			isFip = isFip || name == "fip"
			isFbip = isFbip || name == "fbip"
			if allowance > fipAllowance {
				fipAllowance = allowance
			}
			p.i = next
		}
		if isFip && isFbip {
			// Conflict already reported; keep the weaker claim so downstream
			// phases see a consistent single flag.
			isFip = false
		}
		// `async` is a contextual modifier on a function decl (`async
		// function …` / `pub async function …`): the WASI Preview-3
		// component-model-async export surface. Recognised only when
		// directly followed by `function`, so `async` stays usable as an
		// ordinary identifier elsewhere. See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
		isAsync := false
		if p.match(lexer.Ident, "async") && p.i+1 < len(p.tokens) &&
			p.tokens[p.i+1].Kind == lexer.Keyword && p.tokens[p.i+1].Text == "function" {
			p.advance() // async
			isAsync = true
		}
		// `opaque` is a contextual modifier on a struct decl
		// (`pub opaque struct …`): the type is exported but its fields
		// are private outside the module. Recognised only when directly
		// followed by `struct`, so `opaque` stays usable as an ident.
		isOpaque := false
		if p.match(lexer.Ident, "opaque") && p.i+1 < len(p.tokens) &&
			p.tokens[p.i+1].Kind == lexer.Keyword && p.tokens[p.i+1].Text == "struct" {
			p.advance() // opaque
			isOpaque = true
		}
		// `resource Name;` — a nominal WIT resource-handle type (P5 —
		// docs/WIT-BRING-YOUR-OWN.md), referenced as `own Name` / `borrow
		// Name`. An optional preceding `@import(iface, wit-resource-name)`
		// binds its WIT identity (used by the drop slice). Contextual keyword:
		// `resource` followed by an identifier at decl position, so `resource`
		// stays usable as an ordinary identifier elsewhere.
		if p.match(lexer.Ident, "resource") && p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Ident {
			rd, err := p.parseResourceDecl()
			if err != nil {
				p.errors = append(p.errors, err)
				p.syncToTopLevel()
				if p.i == before {
					p.advance()
				}
				continue
			}
			if rd != nil {
				rd.Public = isPub
				rd.PackageScoped = isPackage
				rd.ImportIface = importIface
				rd.ImportWITName = importWIT
				if len(derives) > 0 {
					p.errors = append(p.errors, p.errorf(rd.P,
						"@derive only applies to a `struct` or `enum` declaration"))
				}
				if exportIface != "" {
					p.errors = append(p.errors, p.errorf(rd.P,
						"@export only applies to a function declaration"))
				}
				prog.Resources = append(prog.Resources, rd)
			}
			continue
		}
		// `@import` binds a single body-less function or a `resource`
		// declaration — once the optional `pub`/`fip` modifiers are consumed,
		// the next token must be `function` (the `resource` case is handled
		// above and `continue`s before reaching here).
		if importIface != "" && !p.match(lexer.Keyword, "function") {
			p.errors = append(p.errors, p.errorf(p.peek().Pos,
				"@import only applies to a function or resource declaration"))
			p.syncToTopLevel()
			if p.i == before {
				p.advance()
			}
			continue
		}
		if inlineHint != ast.InlineHintNone && !p.match(lexer.Keyword, "function") && !p.match(lexer.Keyword, "async") {
			p.errors = append(p.errors, p.errorf(p.peek().Pos,
				"@inline / @noinline only applies to a function declaration"))
			p.syncToTopLevel()
			if p.i == before {
				p.advance()
			}
			continue
		}
		// `@export` binds a single function (with a body) to a WIT export (P6).
		if exportIface != "" && !p.match(lexer.Keyword, "function") {
			p.errors = append(p.errors, p.errorf(p.peek().Pos,
				"@export only applies to a function declaration"))
			p.syncToTopLevel()
			if p.i == before {
				p.advance()
			}
			continue
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
				sd.PackageScoped = isPackage
				sd.Derives = derives
				sd.MustConsume = mustConsume
				sd.Opaque = isOpaque
				prog.Structs = append(prog.Structs, sd)
			}
			continue
		}
		if isOpaque {
			p.errors = append(p.errors, p.errorf(p.peek().Pos, "`opaque` must be followed by `struct`"))
			p.syncToTopLevel()
			if p.i == before {
				p.advance()
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
				cd.PackageScoped = isPackage
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
				ed.PackageScoped = isPackage
				ed.Derives = derives
				ed.MustConsume = mustConsume
				prog.Enums = append(prog.Enums, ed)
			}
			continue
		}
		if len(derives) > 0 {
			p.errors = append(p.errors, p.errorf(p.peek().Pos,
				"@derive only applies to a `struct` or `enum` declaration"))
			p.syncToTopLevel()
			if p.i == before {
				p.advance()
			}
			continue
		}
		if mustConsume {
			p.errors = append(p.errors, p.errorf(p.peek().Pos,
				"@must_consume only applies to a `struct` or `enum` declaration"))
			p.syncToTopLevel()
			if p.i == before {
				p.advance()
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
				ud.PackageScoped = isPackage
				prog.Unions = append(prog.Unions, ud)
			}
			continue
		}
		if p.match(lexer.Keyword, "trait") {
			td, err := p.parseTraitDecl()
			if err != nil {
				p.errors = append(p.errors, err)
				p.syncToTopLevel()
				if p.i == before {
					p.advance()
				}
				continue
			}
			if td != nil {
				td.Public = isPub
				td.PackageScoped = isPackage
				prog.Traits = append(prog.Traits, td)
			}
			continue
		}
		if p.match(lexer.Keyword, "impl") {
			id, methods, err := p.parseImplDecl()
			if err != nil {
				p.errors = append(p.errors, err)
				p.syncToTopLevel()
				if p.i == before {
					p.advance()
				}
				continue
			}
			if id != nil {
				// Keep a back-reference to the desugared methods so the
				// formatter can re-emit the `impl { … }` grouping; the
				// checker/codegen path still reads them from prog.Funcs.
				id.Methods = methods
				prog.Impls = append(prog.Impls, id)
				// Impl methods are desugared into ordinary
				// receiver-methods so modload + the checker's
				// existing method machinery handle them unchanged.
				prog.Funcs = append(prog.Funcs, methods...)
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
			fn.PackageScoped = isPackage
			fn.Fip = isFip
			fn.Fbip = isFbip
			fn.FipAllowance = fipAllowance
			fn.Async = isAsync
			fn.InlineHint = inlineHint
			if importIface != "" {
				if fn.Body != nil {
					p.errors = append(p.errors, p.errorf(fn.P,
						"@import function %q must be body-less (end with `;`)", fn.Name))
				}
				fn.ImportIface = importIface
				fn.ImportWITName = importWIT
			} else if exportIface != "" {
				// An `@export` function is an implementation — it must have a
				// body (unlike a body-less `@import`).
				if fn.Body == nil {
					p.errors = append(p.errors, p.errorf(fn.P,
						"@export function %q must have a body", fn.Name))
				}
				fn.ExportIface = exportIface
				fn.ExportWITName = exportWIT
			} else if fn.Body == nil {
				p.errors = append(p.errors, p.errorf(fn.P,
					"function %q has no body (only @import functions may omit a body)", fn.Name))
			}
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
	// Optional alias: `import "std/test" as t;` binds the qualifier
	// to `t` instead of the path basename. Lets a module be referred
	// to by a shorter / non-colliding name (and reach modules whose
	// basename is a type keyword, e.g. `import "std/string" as s;`).
	localName := importLocalName(pathTok.Text)
	alias := ""
	if _, ok := p.accept(lexer.Keyword, "as"); ok {
		aliasTok, err := p.expect(lexer.Ident, "")
		if err != nil {
			return nil, err
		}
		alias = aliasTok.Text
		localName = alias
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.Import{
		P:         kw.Pos,
		Path:      pathTok.Text,
		LocalName: localName,
		Alias:     alias,
	}, nil
}

// parsePubUse parses `pub use "<path>" . { name1, name2, … } ;` — a
// re-export of the named public symbols from the target module. The
// leading `pub` is already consumed (its position is pubPos); the `use`
// keyword is at the cursor. See docs/PRELUDE-TO-MODULES.md.
func (p *parser) parsePubUse(pubPos ast.Position) (*ast.PubUse, error) {
	p.advance() // use
	pathTok, err := p.expect(lexer.String, "")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, "."); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, err
	}
	var names []string
	for !p.match(lexer.Punct, "}") {
		nameTok, err := p.expectMemberName()
		if err != nil {
			return nil, err
		}
		names = append(names, nameTok.Text)
		if _, ok := p.accept(lexer.Punct, ","); !ok {
			break
		}
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, p.errorf(pubPos, "`pub use` must name at least one symbol, e.g. `pub use \"std/string\".{split};`")
	}
	return &ast.PubUse{P: pubPos, Path: pathTok.Text, Names: names}, nil
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
			p.match(lexer.Keyword, "trait") ||
			p.match(lexer.Keyword, "impl") ||
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

// parseTraitDecl parses `trait Name { <sig>; <sig>; }`, where each
// signature is `function name(self: Self, …params): ret;` (no body).
// The leading `trait` keyword is at the current position.
func (p *parser) parseTraitDecl() (*ast.TraitDecl, error) {
	kw, err := p.expect(lexer.Keyword, "trait")
	if err != nil {
		return nil, err
	}
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	// Optional trait type parameters: `trait From[T] { … }`. Bound to the
	// trait's method signatures; each `impl From[Arg] for T` supplies the
	// arg. Names only for now (bounds parsed but ignored). See docs/TRAITS.md.
	var typeParams []string
	if p.match(lexer.Punct, "[") {
		typeParams, _, err = p.parseTypeParamList()
		if err != nil {
			return nil, err
		}
	}
	// Optional supertraits: `trait Ord: Eq + Hash { … }`. Same `: Trait
	// (+ Trait)*` grammar as a type-parameter bound (qualifiers allowed).
	supertraits, err := p.parseOptBounds()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, err
	}
	td := &ast.TraitDecl{P: kw.Pos, Name: name.Text, NamePos: name.Pos, Supertraits: supertraits, TypeParams: typeParams}
	for !p.match(lexer.Punct, "}") && !p.match(lexer.EOF, "") {
		// Associated type declaration: `type Item;`. Referenced in method
		// signatures as `Self::Item`; each impl binds it with
		// `type Item = …`. See docs/ASSOCIATED-TYPES.md.
		if p.match(lexer.Keyword, "type") {
			p.advance()
			atName, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.Punct, ";"); err != nil {
				return nil, err
			}
			td.AssocTypes = append(td.AssocTypes, atName.Text)
			continue
		}
		mkw, err := p.expect(lexer.Keyword, "function")
		if err != nil {
			return nil, err
		}
		mname, err := p.expectMemberName()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.Punct, "("); err != nil {
			return nil, err
		}
		var params []ast.Param
		if !p.match(lexer.Punct, ")") {
			for {
				// Contextual `own` (consuming) modifier — `own self: Self`.
				// `own` + ident is the modifier; `own:` makes `own` the
				// parameter name (mirrors the function-param loop).
				own := false
				if p.match(lexer.Ident, "own") && p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Ident {
					p.advance()
					own = true
				}
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
				params = append(params, ast.Param{Name: pname.Text, NamePos: pname.Pos, Type: ptype, Own: own})
				if _, ok := p.accept(lexer.Punct, ","); !ok {
					break
				}
			}
		}
		if _, err := p.expect(lexer.Punct, ")"); err != nil {
			return nil, err
		}
		// A method whose first parameter isn't `self` is an *associated
		// function* (`Type.f(args)`, no receiver) — typically a
		// constructor returning `Self`. Ordinary methods still require a
		// leading `self`.
		assoc := len(params) == 0 || params[0].Name != "self"
		var ret ast.Type = ast.VoidType{}
		if _, ok := p.accept(lexer.Punct, ":"); ok {
			t, err := p.parseType()
			if err != nil {
				return nil, err
			}
			ret = t
		}
		// A trait method either ends at `;` (an abstract signature every
		// impl must provide) or carries a `{ … }` default body that impls
		// inherit when they omit it (see docs/TRAITS.md).
		var body *ast.Block
		if p.match(lexer.Punct, "{") {
			b, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			body = b
		} else if _, err := p.expect(lexer.Punct, ";"); err != nil {
			return nil, err
		}
		td.Methods = append(td.Methods, ast.TraitMethod{
			P: mkw.Pos, Name: mname.Text, Params: params, Result: ret, Assoc: assoc, Body: body,
		})
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	return td, nil
}

// parseImplDecl parses `impl Trait for Type { <function>… }`. Each
// method is parsed with the ordinary parseFunction machinery (it has
// no receiver clause — `self` is its first parameter), then desugared
// into a receiver-method FuncDecl with `Self` replaced by the `for`
// type. Returns the ImplDecl record plus the desugared methods, which
// the caller appends to Program.Funcs.
func (p *parser) parseImplDecl() (*ast.ImplDecl, []*ast.FuncDecl, error) {
	kw, err := p.expect(lexer.Keyword, "impl")
	if err != nil {
		return nil, nil, err
	}
	// Parametric impl: `impl[T: Bound] Trait for Box[T] { … }`. The
	// type params come right after `impl` so the `for` type (`Box[T]`)
	// and the method bodies can reference them. Each method inherits
	// these params + bounds, so the receiver-hoist registers the
	// methods as generics that monomorphise per instantiation. See
	// docs/TRAITS.md.
	var implTypeParams []string
	var implBounds map[string][]string
	if p.match(lexer.Punct, "[") {
		implTypeParams, implBounds, err = p.parseTypeParamList()
		if err != nil {
			return nil, nil, err
		}
	}
	tnameTok, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, nil, err
	}
	tname := p.maybeQualify(tnameTok.Text)
	// A `[…]` after the name is either the trait's type arguments
	// (`impl From[i32] for Celsius`) or, for an inherent impl
	// (`impl Box[T] { … }`), the impl type's own generic arguments. We
	// can't tell which until we see whether `for` follows, so parse the
	// list now and assign once the form is known.
	var bracketTypes []ast.Type
	if p.match(lexer.Punct, "[") {
		p.advance() // consume `[`
		for {
			at, err := p.parseType()
			if err != nil {
				return nil, nil, err
			}
			bracketTypes = append(bracketTypes, at)
			if _, ok := p.accept(lexer.Punct, ","); !ok {
				break
			}
		}
		if _, err := p.expect(lexer.Punct, "]"); err != nil {
			return nil, nil, err
		}
	}
	var trait string
	var traitArgs []ast.Type
	var implType ast.Type
	var typePos ast.Position
	if p.match(lexer.Keyword, "for") {
		// Trait impl: `impl Trait[args] for Type { … }`. The name is the
		// trait; the bracket list (if any) is its type arguments.
		trait = tname
		traitArgs = bracketTypes
		p.advance() // consume `for`
		typePos = p.peek().Pos
		implType, err = p.parseType()
		if err != nil {
			return nil, nil, err
		}
		if _, ok := implType.(ast.SelfType); ok {
			return nil, nil, p.errorf(typePos, "`impl … for Self` is not allowed; name a concrete type")
		}
	} else {
		// Inherent impl: `impl Type { … }` / `impl[T] Box[T] { … }`. There
		// is no trait — the name (plus any generic args) is the impl type
		// itself, and its receiver-less functions are associated functions
		// (`Type.f(args)`) while `self`-taking ones are ordinary methods.
		// See #2700.
		trait = ""
		typePos = tnameTok.Pos
		if len(bracketTypes) > 0 {
			implType = ast.EnumType{Name: tname, Args: bracketTypes}
		} else {
			implType = ast.StructType{Name: tname}
		}
	}
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, nil, err
	}
	id := &ast.ImplDecl{P: kw.Pos, Trait: trait, TraitPos: tnameTok.Pos, Type: implType, TypePos: typePos, TypeParams: implTypeParams, Bounds: implBounds, TraitArgs: traitArgs}
	var methods []*ast.FuncDecl
	for !p.match(lexer.Punct, "}") && !p.match(lexer.EOF, "") {
		// Associated-type binding: `type Item = T;`. Records the concrete
		// type the impl fixes for the trait's associated type. `Self` in T
		// resolves to the impl type. See docs/ASSOCIATED-TYPES.md.
		if p.match(lexer.Keyword, "type") {
			p.advance()
			atName, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, nil, err
			}
			if _, err := p.expect(lexer.Punct, "="); err != nil {
				return nil, nil, err
			}
			atType, err := p.parseType()
			if err != nil {
				return nil, nil, err
			}
			if _, err := p.expect(lexer.Punct, ";"); err != nil {
				return nil, nil, err
			}
			if id.AssocTypeBindings == nil {
				id.AssocTypeBindings = map[string]ast.Type{}
			}
			id.AssocTypeBindings[atName.Text] = ast.SubstSelf(atType, implType)
			continue
		}
		fn, err := p.parseFunction()
		if err != nil {
			return nil, nil, err
		}
		if fn.Receiver != nil {
			return nil, nil, p.errorf(fn.P,
				"impl method %q must not declare a receiver clause; its first parameter is `self: Self`", fn.Name)
		}
		// A receiver-less impl method is an *associated function*: no
		// `self`, called as `Type.f(args)`. Substitute Self across the
		// signature and stamp AssocType so the checker hoists it to
		// `__assoc_<Type>_<name>` and resolves type-qualified call sites.
		assoc := len(fn.Params) == 0 || fn.Params[0].Name != "self"
		// Substitute Self -> the concrete impl type across the whole
		// signature, then (for an ordinary method) peel `self` off as the
		// receiver so the checker's receiver-hoist mangles it to
		// __method_<Type>_<name> exactly like a hand-written method.
		for i := range fn.Params {
			fn.Params[i].Type = ast.SubstSelf(fn.Params[i].Type, implType)
		}
		fn.ReturnType = ast.SubstSelf(fn.ReturnType, implType)
		// Empty for an inherent `impl Type { … }` block.
		fn.ImplTrait = trait
		if assoc {
			if st, ok := implType.(ast.StructType); ok {
				fn.AssocType = st.Name
			} else if et, ok := implType.(ast.EnumType); ok {
				fn.AssocType = et.Name
			} else {
				fn.AssocType = implType.String()
			}
		} else {
			recv := fn.Params[0]
			recv.Type = implType
			fn.Receiver = &recv
			fn.Params = fn.Params[1:]
		}
		// A parametric impl makes every method generic over the impl's
		// type params: the receiver type (`Box[T]`), the other params,
		// and the body all reference `T`, so the method must carry the
		// params + bounds for resolveTypeNames → ParamType rewriting
		// and for monomorphisation. A method may not also declare its
		// own leading type params (no nested generics yet); reject the
		// collision rather than silently merging.
		if len(implTypeParams) > 0 {
			if len(fn.TypeParams) > 0 {
				return nil, nil, p.errorf(fn.P,
					"impl method %q cannot declare its own type parameters inside a parametric `impl[…]` block", fn.Name)
			}
			fn.TypeParams = implTypeParams
			fn.Bounds = implBounds
		}
		methods = append(methods, fn)
		id.MethodNames = append(id.MethodNames, fn.Name)
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, nil, err
	}
	return id, methods, nil
}

// declAttr is the parsed result of a leading `@…` declaration attribute.
// Exactly one flavour is set per attribute: Derives (from `@derive`),
// (ImportIface, ImportWIT) (from `@import`), or (ExportIface, ExportWIT)
// (from `@export`).
type declAttr struct {
	derives     []string
	importIface string
	importWIT   string
	exportIface string
	exportWIT   string
	mustConsume bool
	// inlineHint records `@inline` / `@noinline` on a function decl
	// (#4412 Rec §14).
	inlineHint ast.InlineHint
}

// parseAttribute parses a leading `@…` declaration attribute (the `@` is at
// the current position):
//
//   - `@derive(Trait, …)` — the (possibly module-qualified) trait names.
//   - `@import("wasi:iface@x.y.z", "wit-func")` binds the following body-less
//     function to a WIT import (bring-your-own WIT, P4 —
//     docs/WIT-BRING-YOUR-OWN.md).
//   - `@export("wasi:iface@x.y.z", "wit-name")` binds the following function
//     (with a body) to a WIT export, lifted as that world export (P6).
func (p *parser) parseAttribute() (declAttr, error) {
	at := p.advance() // @
	// The attribute name follows `@`. `import` lexes as a keyword (reused
	// from the import-statement syntax); `derive` / `export` are identifiers.
	var attr string
	if tok, ok := p.accept(lexer.Keyword, "import"); ok {
		attr = tok.Text
	} else {
		tok, e := p.expect(lexer.Ident, "")
		if e != nil {
			return declAttr{}, e
		}
		attr = tok.Text
	}
	switch attr {
	case "derive":
		var derives []string
		if _, err := p.expect(lexer.Punct, "("); err != nil {
			return declAttr{}, err
		}
		for {
			tn, err := p.expect(lexer.Ident, "")
			if err != nil {
				return declAttr{}, err
			}
			derives = append(derives, p.maybeQualify(tn.Text))
			if _, ok := p.accept(lexer.Punct, ","); ok {
				continue
			}
			break
		}
		if _, err := p.expect(lexer.Punct, ")"); err != nil {
			return declAttr{}, err
		}
		return declAttr{derives: derives}, nil
	case "must_consume":
		// `@must_consume` — bare marker, no arguments. Applies to a
		// struct or enum declaration; the checker's E067 walk
		// enforces that values of the type are consumed on every
		// path. See docs/MUST-CONSUME.md.
		return declAttr{mustConsume: true}, nil
	case "inline":
		// `@inline` — bare marker on a function: lift the IR
		// inliner's size cap for this callee (shape-safety
		// exclusions still apply). #4412 Rec §14.
		return declAttr{inlineHint: ast.InlineHintAlways}, nil
	case "noinline":
		// `@noinline` — bare marker on a function: never inline
		// this callee.
		return declAttr{inlineHint: ast.InlineHintNever}, nil
	case "import", "export":
		// `@import(iface, name)` and `@export(iface, name)` share the same
		// two-string shape; only the binding direction differs.
		iface, name, err := p.parseIfaceNamePair()
		if err != nil {
			return declAttr{}, err
		}
		if attr == "import" {
			return declAttr{importIface: iface, importWIT: name}, nil
		}
		return declAttr{exportIface: iface, exportWIT: name}, nil
	default:
		return declAttr{}, p.errorf(at.Pos, "unknown attribute @%s (only @derive, @import, @export, @must_consume, @inline, and @noinline are supported)", attr)
	}
}

// parseIfaceNamePair parses `("iface", "name")` — the argument shape shared by
// `@import` and `@export`.
func (p *parser) parseIfaceNamePair() (iface, name string, err error) {
	if _, err := p.expect(lexer.Punct, "("); err != nil {
		return "", "", err
	}
	it, err := p.expect(lexer.String, "")
	if err != nil {
		return "", "", err
	}
	if _, err := p.expect(lexer.Punct, ","); err != nil {
		return "", "", err
	}
	nt, err := p.expect(lexer.String, "")
	if err != nil {
		return "", "", err
	}
	if _, err := p.expect(lexer.Punct, ")"); err != nil {
		return "", "", err
	}
	return it.Text, nt.Text, nil
}

// parseResourceDecl parses `resource Name;` (the contextual `resource`
// identifier is at the current position). The optional `@import` WIT binding
// is stamped onto the returned decl by the caller. See ResourceDecl and
// docs/WIT-BRING-YOUR-OWN.md (P5).
func (p *parser) parseResourceDecl() (*ast.ResourceDecl, error) {
	kw := p.advance() // resource
	nameTok, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.ResourceDecl{P: kw.Pos, Name: nameTok.Text}, nil
}

// expectMemberName parses a member name (a function/method name, a trait
// method name, or a field/method after `.`). It accepts a normal Ident
// and also the handful of reserved words that are usable as member names
// in these positions without ambiguity — currently just `default`, so
// `Type.default()` (the `@derive(Default)` constructor) and a hand-written
// `function default(): Self` both work even though `default` is a switch
// keyword. The keyword stays reserved everywhere else.
func (p *parser) expectMemberName() (lexer.Token, error) {
	if tok, ok := p.accept(lexer.Keyword, "default"); ok {
		return tok, nil
	}
	return p.expect(lexer.Ident, "")
}

// maybeQualify consumes an optional `.ident` suffix and returns the
// possibly-qualified name (`mod.Trait`). modload rewrites the qualifier
// to the imported module's mangled prefix. The leading identifier has
// already been consumed by the caller.
func (p *parser) maybeQualify(first string) string {
	if p.match(lexer.Punct, ".") {
		p.advance()
		if rest, ok := p.accept(lexer.Ident, ""); ok {
			return first + "." + rest.Text
		}
	}
	return first
}

// parseTypeParamList parses a `[T, U: Bound + Other, …]` type-
// parameter list (the `[` is at the current position) and returns
// the parameter names plus an optional name→bounds map. Shared by
// the generic-function, generic-method-receiver, and parametric-
// impl (`impl[T: Bound] Trait for Box[T]`) parse paths so all three
// accept the same bound syntax. See docs/TRAITS.md.
func (p *parser) parseTypeParamList() ([]string, map[string][]string, error) {
	tp, b, _, err := p.parseTypeParamListWithArgs()
	return tp, b, err
}

// parseTypeParamListWithArgs is parseTypeParamList plus the per-bound type
// arguments (`[T: From[i32]]`), parallel to the bounds map. See docs/TRAITS.md.
func (p *parser) parseTypeParamListWithArgs() ([]string, map[string][]string, map[string][][]ast.Type, error) {
	if _, err := p.expect(lexer.Punct, "["); err != nil {
		return nil, nil, nil, err
	}
	var typeParams []string
	var bounds map[string][]string
	var boundArgs map[string][][]ast.Type
	for {
		pname, err := p.expect(lexer.Ident, "")
		if err != nil {
			return nil, nil, nil, err
		}
		typeParams = append(typeParams, pname.Text)
		bs, ba, err := p.parseOptBoundsWithArgs()
		if err != nil {
			return nil, nil, nil, err
		}
		if len(bs) > 0 {
			if bounds == nil {
				bounds = map[string][]string{}
			}
			bounds[pname.Text] = bs
			hasArgs := false
			for _, a := range ba {
				if len(a) > 0 {
					hasArgs = true
				}
			}
			if hasArgs {
				if boundArgs == nil {
					boundArgs = map[string][][]ast.Type{}
				}
				boundArgs[pname.Text] = ba
			}
		}
		if !p.moreElems("]") {
			break
		}
	}
	if _, err := p.expect(lexer.Punct, "]"); err != nil {
		return nil, nil, nil, err
	}
	return typeParams, bounds, boundArgs, nil
}

// parseOptBounds parses an optional trait-bound list on a type
// parameter: `: Display + Eq` (bounds may be qualified, `mod.Trait`).
// Returns nil when no `:` follows. See docs/TRAITS.md.
func (p *parser) parseOptBounds() ([]string, error) {
	bs, _, err := p.parseOptBoundsWithArgs()
	return bs, err
}

// parseOptBoundsWithArgs parses a `: Trait (+ Trait)*` bound clause, where
// each trait may carry type arguments (`T: From[i32] + Eq`). Returns the
// bound trait names and a parallel slice of their type-args (nil entry for
// a bound with no args). See docs/TRAITS.md.
func (p *parser) parseOptBoundsWithArgs() ([]string, [][]ast.Type, error) {
	if _, ok := p.accept(lexer.Punct, ":"); !ok {
		return nil, nil, nil
	}
	var bounds []string
	var args [][]ast.Type
	for {
		b, err := p.expect(lexer.Ident, "")
		if err != nil {
			return nil, nil, err
		}
		bounds = append(bounds, p.maybeQualify(b.Text))
		var ta []ast.Type
		if p.match(lexer.Punct, "[") {
			p.advance()
			for {
				at, err := p.parseType()
				if err != nil {
					return nil, nil, err
				}
				ta = append(ta, at)
				if _, ok := p.accept(lexer.Punct, ","); !ok {
					break
				}
			}
			if _, err := p.expect(lexer.Punct, "]"); err != nil {
				return nil, nil, err
			}
		}
		args = append(args, ta)
		if _, ok := p.accept(lexer.Punct, "+"); ok {
			continue
		}
		break
	}
	return bounds, args, nil
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
	var bounds map[string][]string
	var boundArgs map[string][][]ast.Type
	if p.match(lexer.Punct, "[") {
		typeParams, bounds, boundArgs, err = p.parseTypeParamListWithArgs()
		if err != nil {
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
		// Contextual `own` (consuming) receiver — `(own self: List)`. `own` +
		// ident is the modifier; `own:` makes `own` the receiver name.
		rOwn := false
		if p.match(lexer.Ident, "own") && p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Ident {
			p.advance()
			rOwn = true
		}
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
		receiver = &ast.Param{Name: rname.Text, NamePos: rname.Pos, Type: rtype, Own: rOwn}
	}
	name, err := p.expectMemberName()
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
		typeParams, bounds, boundArgs, err = p.parseTypeParamListWithArgs()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.Punct, "("); err != nil {
		return nil, err
	}
	var params []ast.Param
	var paramDestrs []*ast.Destructure
	if !p.match(lexer.Punct, ")") {
		for {
			// Destructuring parameter: `(a, b): (T, U)` / `P { x, y }: P`.
			// Parsed into a synthetic param + a body-prelude destructure
			// (prepended after the body parse below).
			if p.atParamPattern() {
				prm, d, err := p.parseParamPattern()
				if err != nil {
					return nil, err
				}
				params = append(params, prm)
				paramDestrs = append(paramDestrs, d)
				if !p.moreElems(")") {
					break
				}
				continue
			}
			// `own` is a CONTEXTUAL keyword: `own xs: T` marks an owned
			// (consuming) param, but `own: T` is still a param named `own`.
			// Disambiguate by the token AFTER `own` — a param name (Ident)
			// means the modifier; a `:` means `own` IS the name.
			own := false
			if p.match(lexer.Ident, "own") && p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Ident {
				p.advance()
				own = true
			}
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
			// Optional default value: `function f(a: i32, b: i32 = 128)`.
			// The defaultargs pass fills it at call sites that omit the
			// trailing argument. A defaulted param may not be followed by
			// a required one (enforced just below).
			var def ast.Expr
			if _, ok := p.accept(lexer.Punct, "="); ok {
				def, err = p.parseExpr()
				if err != nil {
					return nil, err
				}
			}
			params = append(params, ast.Param{Name: discardName(pname.Text, pname.Pos, len(params)), NamePos: pname.Pos, Type: ptype, Own: own, Default: def})
			if !p.moreElems(")") {
				break
			}
		}
	}
	if _, err := p.expect(lexer.Punct, ")"); err != nil {
		return nil, err
	}
	// A required parameter may not follow a defaulted one — otherwise a
	// trailing-args fill is ambiguous.
	seenDefault := false
	for _, prm := range params {
		if prm.Default != nil {
			seenDefault = true
		} else if seenDefault {
			return nil, p.errorf(funcNamePos, "required parameter %q cannot follow a parameter with a default value", prm.Name)
		}
	}

	var ret ast.Type = ast.VoidType{}
	retAnnotated := false
	if _, ok := p.accept(lexer.Punct, ":"); ok {
		t, err := p.parseType()
		if err != nil {
			return nil, err
		}
		ret = t
		retAnnotated = true
	}

	// A body-less function (`function f(): T;`) is an import declaration —
	// the `@import` attribute supplies its WIT binding (validated by the
	// caller / checker). Otherwise parse the block body.
	var body *ast.Block
	if _, ok := p.accept(lexer.Punct, ";"); !ok {
		p.returnTypeStack = append(p.returnTypeStack, ret)
		body, err = p.parseBlock()
		p.returnTypeStack = p.returnTypeStack[:len(p.returnTypeStack)-1]
		if err != nil {
			return nil, err
		}
	}
	if len(paramDestrs) > 0 {
		// A body-less decl is an `@import` signature — a binding
		// pattern has nothing to bind into there.
		if body == nil {
			return nil, p.errorf(funcNamePos, "a destructured parameter requires a function body")
		}
		prependParamDestructures(body, paramDestrs)
	}
	return &ast.FuncDecl{
		P:                 kw.Pos,
		Name:              name.Text,
		NamePos:           funcNamePos,
		TypeParams:        typeParams,
		Bounds:            bounds,
		BoundArgs:         boundArgs,
		Params:            params,
		ReturnType:        ret,
		ReturnUnannotated: !retAnnotated && body != nil,
		Body:              body,
		Receiver:          receiver,
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
	// Optional `own` modifier on the receiver — `(own self: T)`. Only treat it
	// as the modifier when followed by an ident (the receiver name); `own:` is
	// a receiver named `own`.
	if p.peek().Kind == lexer.Ident && p.peek().Text == "own" &&
		p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Ident {
		p.i++ // skip own
	}
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
	// After the receiver `)`, we expect `name(` — or `name[` for a
	// method with its own type parameters (`(b: Box[T]) map[U](...)`),
	// whose `[U]` list the post-name parse picks up.
	ok := p.peek().Kind == lexer.Ident
	if ok {
		p.i++
		ok = p.peek().Kind == lexer.Punct && (p.peek().Text == "(" || p.peek().Text == "[")
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
			} else if _, ok := p.accept(lexer.Punct, "{"); ok {
				// Named-field variant: `Rect { w: f64, h: f64 }`. Field
				// names parallel Payloads; the runtime layout is the same
				// declaration-ordered tagged union as the positional form.
				variant.FieldNames = []string{}
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
						variant.FieldNames = append(variant.FieldNames, fname.Text)
						variant.Payloads = append(variant.Payloads, ft)
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
				if len(variant.FieldNames) == 0 {
					return nil, p.errorf(vname.Pos, "named-field variant %s must declare at least one field", vname.Text)
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
	case t.Kind == lexer.Keyword && t.Text == "u32":
		p.advance()
		base = ast.NumberType{Width: 32, Signed: false, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "u64":
		p.advance()
		base = ast.NumberType{Width: 64, Signed: false, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "u8":
		p.advance()
		base = ast.NumberType{Width: 8, Signed: false, Spelling: t.Text}
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
			// `()` — the unit type, void's other spelling. Written
			// where a generic needs a "nothing to report" argument:
			// `Result[(), IoError]`. The `=>` form above already
			// claimed the zero-arg function type, so reaching here
			// with no elements means the bare unit type.
			base = ast.VoidType{}
		}
	case t.Kind == lexer.Keyword && t.Text == "dyn":
		// `dyn Trait` (single) or `dyn A + B` (multi-trait object) — a
		// runtime trait-object type. Each trait name is a bare
		// (optionally module-qualified) identifier, optionally followed
		// by generic arguments (`dyn Container[i32]`). Additional traits
		// after the first are joined with `+` (mirroring trait-bound
		// syntax, parseOptBounds). The set is normalised (sorted +
		// deduped) by NewDynTraitTypeFull so `dyn A + B` ≡ `dyn B + A`.
		// Falls through to the trailing-`[]` suffix loop, so `dyn Shape[]`
		// / `dyn A + B[]` is an array of trait objects. A trailing/empty
		// `+` is a parse error. The `[` immediately followed by `]` is the
		// array suffix (handled below), not an empty generic-arg list. See
		// docs/DYN-TRAITS.md.
		p.advance() // consume `dyn`
		var traits []string
		var traitArgs [][]ast.Type
		var traitAssoc [][]ast.AssocBinding
		for {
			nameTok, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			traits = append(traits, p.maybeQualify(nameTok.Text))
			var args []ast.Type
			var assoc []ast.AssocBinding
			if p.match(lexer.Punct, "[") && p.i+1 < len(p.tokens) && !(p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "]") {
				p.advance() // consume `[`
				for {
					// `Name = Type` pins an associated type (`dyn Iterator[Item = i32]`);
					// a bare `Type` is a positional generic argument (`dyn Container[i32]`).
					if p.match(lexer.Ident, "") && p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "=" {
						nameT := p.advance() // assoc-type name
						p.advance()          // `=`
						bt, err := p.parseType()
						if err != nil {
							return nil, err
						}
						assoc = append(assoc, ast.AssocBinding{Name: nameT.Text, Type: bt})
					} else {
						at, err := p.parseType()
						if err != nil {
							return nil, err
						}
						args = append(args, at)
					}
					if _, ok := p.accept(lexer.Punct, ","); ok {
						continue
					}
					break
				}
				if _, err := p.expect(lexer.Punct, "]"); err != nil {
					return nil, err
				}
			}
			traitArgs = append(traitArgs, args)
			traitAssoc = append(traitAssoc, assoc)
			if _, ok := p.accept(lexer.Punct, "+"); ok {
				continue
			}
			break
		}
		base = ast.NewDynTraitTypeFull(traits, traitArgs, traitAssoc)
	case (t.Kind == lexer.Ident) && (t.Text == "own" || t.Text == "borrow") &&
		p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Ident:
		// `own R` / `borrow R` — a WIT resource-handle type (P5 —
		// docs/WIT-BRING-YOUR-OWN.md). R names a `resource` declaration (the
		// checker validates that). `own` is owned/consuming (dropped at scope
		// exit, a later slice); `borrow` is a non-consuming view. Contextual:
		// only a handle when an identifier (the resource name) follows, so
		// `own`/`borrow` stay usable as ordinary type names otherwise. Falls
		// through to the trailing-`[]` suffix loop like any base type.
		borrowed := t.Text == "borrow"
		p.advance() // own / borrow
		nameTok, err := p.expect(lexer.Ident, "")
		if err != nil {
			return nil, err
		}
		name := p.maybeQualify(nameTok.Text)
		p.typeRefs = append(p.typeRefs, ast.TypeRef{P: nameTok.Pos, Name: name})
		base = ast.HandleType{Resource: name, Borrowed: borrowed}
	case t.Kind == lexer.Ident && t.Text == "Self" &&
		!(p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "."):
		// `Self` is the contextual trait/impl type. It's only valid
		// inside a trait declaration or an `impl` body; the parser
		// always maps it to ast.SelfType and the checker rejects any
		// stray occurrence outside an impl. No `.fern` source uses
		// `Self` as a struct name, so this is unambiguous. Falls
		// through to the trailing-`[]` suffix loop like any base type.
		p.advance()
		base = ast.SelfType{}
	case t.Kind == lexer.Ident && t.Text == "str" &&
		!(p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "."):
		// `str` is the contextual borrowed-string view type (#4813). An
		// Ident, deliberately NOT a lexer keyword: `.str` methods
		// (std/log's field-attach API) and `str` locals in expression
		// position are untouched; only type position is claimed. The
		// `str.` guard keeps a module-qualified struct reference
		// (`str.Foo`) on the bare-identifier path below. A user struct
		// named exactly `str` is shadowed in type position (reserved,
		// like Self).
		p.advance()
		base = ast.StrType{}
	case t.Kind == lexer.Ident && t.Text == "char" &&
		!(p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "."):
		// `char` is the contextual Unicode-scalar type (#5629). Contextual
		// for the same reason as `str`: an Ident, not a lexer keyword, so
		// `.char` methods and `char` locals in expression position keep
		// working and only type position is claimed. The `char.` guard
		// keeps a module-qualified reference (`char.Foo`) on the
		// bare-identifier path below.
		p.advance()
		base = ast.CharType{}
	case t.Kind == lexer.Ident && t.Text == "float" &&
		!(p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "."):
		// `float` is the width-unqualified float alias — f64, the
		// language's primary float (#5363), matching the self-host
		// checker's long-standing resolution. Contextual like `str`:
		// an Ident, not a lexer keyword, so `float.pi()` calls into
		// the std/float module and `float` locals stay untouched;
		// the `float.` guard keeps module-qualified struct references
		// (`float.Foo`) on the bare-identifier path below.
		p.advance()
		base = ast.FloatType{Width: 64, Spelling: t.Text}
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
				if !p.moreElems("]") {
					break
				}
			}
			if _, err := p.expect(lexer.Punct, "]"); err != nil {
				return nil, err
			}
			// `stream[T]` is the built-in WASI Preview-3 async data
			// channel (docs/STREAM-TYPE-SURFACE.md), not a generic enum
			// instantiation. Recognised contextually (name "stream" + one
			// arg) so `stream` stays a usable identifier elsewhere — no
			// reserved keyword.
			if name == "stream" && len(args) == 1 {
				base = ast.StreamType{Elem: args[0]}
			} else {
				// Generic instantiations are otherwise enums in this
				// PR; the checker validates the name actually
				// resolves to an enum.
				base = ast.EnumType{Name: name, Args: args}
			}
		} else {
			base = ast.StructType{Name: name}
		}
	default:
		return nil, p.errorfCode(t.Pos, "P001", "expected type, got %q", t.Text)
	}
	// Associated-type projection `Base::Name` (`Self::Item`, `T::Item`,
	// `Foo::Item`), repeatable for chained projections. Binds tighter
	// than the `[]` suffix so `T::Item[]` is an array of the projection.
	// See docs/ASSOCIATED-TYPES.md.
	for p.match(lexer.Punct, "::") {
		p.advance()
		nameTok, err := p.expect(lexer.Ident, "")
		if err != nil {
			return nil, err
		}
		base = ast.ProjType{Base: base, Name: nameTok.Text}
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
	p.parseBlockStmts(block)
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return block, err
	}
	return block, nil
}

// parseBlockStmts fills block with statements up to (but not consuming)
// the closing `}`. Shared by parseBlock and the two statement-position
// desugars that swallow the rest of their enclosing block — `use` and
// `let … else` — so the tail they capture is parsed by exactly the same
// loop, error sync and progress guard as the block they were written in.
func (p *parser) parseBlockStmts(block *ast.Block) {
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
			return
		}
		// `let` is the other rest-of-block desugar: `let PAT = E else
		// { … };` binds for the rest of THIS block, so that remainder
		// becomes the success arm. Only the direct-in-a-block form may
		// capture it — a `let … else` reached as a braceless branch body
		// (`if (c) let X(v) = e else { … };`) has no remainder of its own
		// and must not steal the enclosing block's, so parseStmt asks for
		// the non-capturing form.
		if p.match(lexer.Keyword, "let") {
			s, err := p.parseLet(true)
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
			if m, ok := s.(*ast.Match); ok && m.Origin == ast.OriginLetElse {
				return
			}
			continue
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
}

func (p *parser) parseStmt() (ast.Stmt, error) {
	t := p.peek()
	if t.Kind == lexer.Punct && t.Text == "{" {
		return p.parseBlock()
	}
	// Labeled loop: `IDENT : (while|loop|for) …`. The label names the
	// loop so a nested `break IDENT` / `continue IDENT` can target it.
	// `:` after a bare identifier only occurs here (object/map literals
	// and slices live in expression position), so the three-token
	// lookahead is unambiguous.
	if t.Kind == lexer.Ident && p.i+2 < len(p.tokens) {
		c, n := p.tokens[p.i+1], p.tokens[p.i+2]
		if c.Kind == lexer.Punct && c.Text == ":" && n.Kind == lexer.Keyword &&
			(n.Text == "while" || n.Text == "loop" || n.Text == "for") {
			label := t.Text
			p.advance() // IDENT
			p.advance() // :
			switch n.Text {
			case "while":
				return p.parseWhile(label)
			case "loop":
				return p.parseLoop(label)
			default:
				return p.parseFor(label)
			}
		}
	}
	// `assert(cond)` / `assert(cond, msg)` builtin — desugars to
	// `if (!cond) { eprint("assertion failed[: msg]"); exit(1); }`, a
	// parser-level rewrite so it lowers through the already-supported
	// if / eprint / exit path on every backend (native + self-host IR)
	// with no dedicated codegen. Only intercepted in statement position
	// with a following `(`, so `assert` stays usable as an ordinary
	// identifier elsewhere. #4416.
	if t.Kind == lexer.Ident && t.Text == "assert" && p.i+1 < len(p.tokens) {
		if nx := p.tokens[p.i+1]; nx.Kind == lexer.Punct && nx.Text == "(" {
			return p.parseAssert()
		}
	}
	// `todo;` / `todo("msg");` builtin — a Gleam-inspired stub marker
	// that desugars to `loop { eprint("todo[: msg]"); exit(101); }`.
	// The `loop` wrapper (never re-entered — exit fires on the first
	// iteration) makes the stub DIVERGE for both checker analyses
	// (E052 missing-return and `let else`), so `todo;` can stand in
	// for a whole non-void function body. Exit code 101 distinguishes
	// "unimplemented" from assert's 1 and the trap/arena 134/137
	// family. Same contextual-intercept rule as `assert`: only the
	// statement-position `todo ;` / `todo (` shapes are taken, so
	// `todo` stays usable as an ordinary identifier elsewhere.
	if t.Kind == lexer.Ident && t.Text == "todo" && p.i+1 < len(p.tokens) {
		if nx := p.tokens[p.i+1]; nx.Kind == lexer.Punct && (nx.Text == "(" || nx.Text == ";") {
			return p.parseTodo()
		}
	}
	if t.Kind == lexer.Keyword {
		switch t.Text {
		case "if":
			return p.parseIf()
		case "while":
			return p.parseWhile("")
		case "loop":
			return p.parseLoop("")
		case "for":
			return p.parseFor("")
		case "break":
			return p.parseBreakContinue(true)
		case "continue":
			return p.parseBreakContinue(false)
		case "return":
			return p.parseReturn()
		case "defer", "errdefer":
			return p.parseDefer()
		case "var":
			return p.parseVar()
		case "let":
			// Not directly inside a block (a braceless branch body), so
			// there is no rest-of-block for a `let … else` to bind over.
			return p.parseLet(false)
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
	// `if let <pattern> = <expr> { … } [else { … }]` — pattern-binding
	// shorthand. Disambiguated by the `let` keyword right after `if`.
	if p.match(lexer.Keyword, "let") {
		return p.parseIfLet(kw.Pos)
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

func (p *parser) parseWhile(label string) (ast.Stmt, error) {
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
	return &ast.While{P: kw.Pos, Cond: cond, Body: body, Label: label}, nil
}

// parseLoop handles the `loop { ... }` canonical infinite loop. It
// produces a dedicated ast.Loop node (not While-true sugar) so
// divergence analyses can recognize it as definitionally diverging
// without pattern-matching a literal-true While condition; `break` /
// `continue` (and their labeled forms) work as in any while loop.
func (p *parser) parseLoop(label string) (ast.Stmt, error) {
	kw := p.advance()
	body, err := p.parseStmt()
	if err != nil {
		return nil, err
	}
	return &ast.Loop{P: kw.Pos, Body: body, Label: label}, nil
}

// parseLambda parses `function (params): R { body }` in
// expression position. The `function` keyword has already been
// peeked (not yet consumed) by parsePrimary; parseLambda
// consumes it and reads the unnamed-function shape. The body is
// parsed inside the parser's standard return-type stack so
// `use` desugaring inside the lambda body picks up the right
// callback return type.
// looksLikeArrowLambda reports whether the `(` at the cursor begins an
// arrow lambda — `() => …`, `(): R => …`, or `(IDENT: TYPE, …) [: R] => …`.
// The disambiguator from a grouping/tuple is the parameter shape: an arrow
// lambda's parens are either empty or start with `IDENT :` (a typed param),
// which an expression never does, and the matching `)` is followed by `=>`
// or `:` (the return-type colon). See #2701.
func (p *parser) looksLikeArrowLambda() bool {
	// p.i is at "(".
	if p.i+1 >= len(p.tokens) {
		return false
	}
	followedByArrowOrColon := func(idx int) bool {
		if idx >= len(p.tokens) {
			return false
		}
		t := p.tokens[idx]
		return t.Kind == lexer.Punct && (t.Text == "=>" || t.Text == ":")
	}
	// Empty params: `(` `)` then `=>` / `:`.
	if p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == ")" {
		return followedByArrowOrColon(p.i + 2)
	}
	if !p.firstParamLooksTyped(p.i + 1) {
		return false
	}
	// Scan to the matching `)` and check the token after it.
	depth := 0
	for j := p.i; j < len(p.tokens); j++ {
		t := p.tokens[j]
		if t.Kind == lexer.EOF {
			return false
		}
		if t.Kind == lexer.Punct && t.Text == "(" {
			depth++
		} else if t.Kind == lexer.Punct && t.Text == ")" {
			depth--
			if depth == 0 {
				return followedByArrowOrColon(j + 1)
			}
		}
	}
	return false
}

// firstParamLooksTyped reports whether the tokens at idx open a parameter
// rather than an expression — the disambiguator between an arrow lambda's
// parens and a grouping / tuple. Three shapes, each pinned by a `:` an
// expression can't have in that position:
//
//	IDENT :          a plain typed param
//	( … ) :          a tuple-destructuring param
//	IDENT { … } :    a struct-destructuring param
//
// An `@` binding may precede either destructuring form.
func (p *parser) firstParamLooksTyped(idx int) bool {
	isIdent := func(i int) bool { return i < len(p.tokens) && p.tokens[i].Kind == lexer.Ident }
	isPunct := func(i int, text string) bool {
		return i < len(p.tokens) && p.tokens[i].Kind == lexer.Punct && p.tokens[i].Text == text
	}
	// `w @ <pattern>` — skip the binder so the shapes below are what is tested.
	if isIdent(idx) && isPunct(idx+1, "@") {
		idx += 2
	}
	switch {
	case isPunct(idx, "("):
		// `(a, b): (T, U)` — the annotation colon follows the matching `)`.
		// `((a, b), c)` / `((a + b) * c)` never put one there.
		return p.closerFollowedBy(idx, "(", ")", ":", "=>")
	case isIdent(idx) && isPunct(idx+1, "{"):
		// `P { x, y }: P` — a parenthesised struct LITERAL puts its colons
		// INSIDE the braces, so only a pattern has one after the `}`.
		return p.closerFollowedBy(idx+1, "{", "}", ":")
	default:
		return isIdent(idx) && isPunct(idx+1, ":")
	}
}

// closerFollowedBy scans from the opening bracket at idx to its match and
// reports whether the token after it is one of wants.
func (p *parser) closerFollowedBy(idx int, open, close string, wants ...string) bool {
	depth := 0
	for j := idx; j < len(p.tokens); j++ {
		t := p.tokens[j]
		if t.Kind == lexer.EOF {
			return false
		}
		if t.Kind != lexer.Punct {
			continue
		}
		switch t.Text {
		case open:
			depth++
		case close:
			depth--
			if depth != 0 {
				continue
			}
			n := j + 1
			if n >= len(p.tokens) || p.tokens[n].Kind != lexer.Punct {
				return false
			}
			for _, w := range wants {
				if p.tokens[n].Text == w {
					return true
				}
			}
			return false
		}
	}
	return false
}

// parseArrowLambda parses `(params) [: R] => expr` into an ast.Lambda whose
// body is `{ return expr; }`. Parameter types are required (as in the
// verbose `function (…)` form); the return type is optional and defaults to
// void. See #2701.
func (p *parser) parseArrowLambda() (ast.Expr, error) {
	open := p.advance() // "("
	var params []ast.Param
	var paramDestrs []*ast.Destructure
	if !p.match(lexer.Punct, ")") {
		for {
			if p.atParamPattern() {
				prm, d, err := p.parseParamPattern()
				if err != nil {
					return nil, err
				}
				params = append(params, prm)
				paramDestrs = append(paramDestrs, d)
				if !p.moreElems(")") {
					break
				}
				continue
			}
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
			params = append(params, ast.Param{Name: discardName(pname.Text, pname.Pos, len(params)), NamePos: pname.Pos, Type: ptype})
			if !p.moreElems(")") {
				break
			}
		}
	}
	if _, err := p.expect(lexer.Punct, ")"); err != nil {
		return nil, err
	}
	var ret ast.Type = ast.VoidType{}
	unannotated := true
	if _, ok := p.accept(lexer.Punct, ":"); ok {
		t, err := p.parseType()
		if err != nil {
			return nil, err
		}
		ret = t
		unannotated = false
	}
	if _, err := p.expect(lexer.Punct, "=>"); err != nil {
		return nil, err
	}
	p.returnTypeStack = append(p.returnTypeStack, ret)
	bodyExpr, err := p.parseExpr()
	p.returnTypeStack = p.returnTypeStack[:len(p.returnTypeStack)-1]
	if err != nil {
		return nil, err
	}
	body := &ast.Block{P: open.Pos, Stmts: []ast.Stmt{&ast.Return{P: open.Pos, Value: bodyExpr}}}
	prependParamDestructures(body, paramDestrs)
	return &ast.Lambda{P: open.Pos, Params: params, ReturnType: ret, ReturnUnannotated: unannotated, Arrow: true, Body: body}, nil
}

func (p *parser) parseLambda() (ast.Expr, error) {
	kw := p.advance() // function
	if _, err := p.expect(lexer.Punct, "("); err != nil {
		return nil, err
	}
	var params []ast.Param
	var paramDestrs []*ast.Destructure
	if !p.match(lexer.Punct, ")") {
		for {
			if p.atParamPattern() {
				prm, d, err := p.parseParamPattern()
				if err != nil {
					return nil, err
				}
				params = append(params, prm)
				paramDestrs = append(paramDestrs, d)
				if !p.moreElems(")") {
					break
				}
				continue
			}
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
			params = append(params, ast.Param{Name: discardName(pname.Text, pname.Pos, len(params)), Type: ptype})
			if !p.moreElems(")") {
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
	prependParamDestructures(body, paramDestrs)
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
	thenE, err := p.parseBranchBody()
	if err != nil {
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
	elseE, err := p.parseBranchBody()
	if err != nil {
		return nil, err
	}
	return &ast.IfExpr{P: kw.Pos, Cond: cond, Then: thenE, Else: elseE}, nil
}

// parseBranchBody parses the `{ … }` body of an `if`/`match` expression
// branch (slice 1 of block-expressions). The body is a sequence of
// `;`-terminated statements run in a fresh child scope, optionally
// followed by a trailing expression written WITHOUT a `;` — that
// trailing expression is the block's value.
//
//   - `{ e }`                  → the bare expression `e` (single-expr
//     branch, kept byte-identical to the pre-block-expr behaviour so
//     existing `if`/`match` expressions don't regress).
//   - `{ s; s; tail }`         → `BlockExpr{Stmts:[s,s], Tail:tail}`.
//   - `{ s; }`                 → `BlockExpr{Stmts:[s], Tail:nil}` — a
//     value-less block; the checker reports E060 when it's used where a
//     value is required.
//
// Statement-led forms (keyword statements: `var`/`if`/`while`/… and a
// nested `{`-block) always parse as statements via parseStmt, which
// consumes its own terminator. A non-keyword item parses as an
// expression: if a `;` follows it's an ExprStmt, if `}` follows it's
// the trailing value expression. This keeps the new grammar form
// confined to branch position — general value-position blocks are a
// later slice.
func (p *parser) parseBranchBody() (ast.Expr, error) {
	open, err := p.expect(lexer.Punct, "{")
	if err != nil {
		return nil, err
	}
	var stmts []ast.Stmt
	var tail ast.Expr
	for !p.match(lexer.Punct, "}") && !p.match(lexer.EOF, "") {
		if p.branchStmtStart() {
			s, err := p.parseStmt()
			if err != nil {
				return nil, err
			}
			if s != nil {
				stmts = append(stmts, s)
			}
			continue
		}
		// Non-keyword item: an expression that is either an ExprStmt
		// (followed by `;`) or the trailing value (followed by `}`).
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, ok := p.accept(lexer.Punct, ";"); ok {
			stmts = append(stmts, &ast.ExprStmt{P: e.Pos(), Expr: e})
			continue
		}
		// No `;` — this must be the trailing tail expression, so `}`
		// has to follow. The expect below surfaces a clear error if a
		// statement is missing its `;`.
		tail = e
		break
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	// A single trailing expression with no leading statements stays a
	// bare expr — keeps existing single-expr branches byte-identical.
	if len(stmts) == 0 && tail != nil {
		return tail, nil
	}
	return &ast.BlockExpr{P: open.Pos, Stmts: stmts, Tail: tail}, nil
}

// branchStmtStart reports whether the next token begins a statement
// that parseStmt handles directly (and which consumes its own
// terminator) inside a branch body. These are the keyword-led
// statements plus a nested `{`-block and a labeled loop. Everything
// else is parsed as an expression (ExprStmt or the trailing tail).
func (p *parser) branchStmtStart() bool {
	t := p.peek()
	if t.Kind == lexer.Punct && t.Text == "{" {
		return true
	}
	// Labeled loop: `IDENT : (while|loop|for)` — same three-token
	// lookahead parseStmt uses.
	if t.Kind == lexer.Ident && p.i+2 < len(p.tokens) {
		c, n := p.tokens[p.i+1], p.tokens[p.i+2]
		if c.Kind == lexer.Punct && c.Text == ":" && n.Kind == lexer.Keyword &&
			(n.Text == "while" || n.Text == "loop" || n.Text == "for") {
			return true
		}
	}
	// #4522: an else-LESS `if` in a block body is a control-flow STATEMENT
	// (`{ if (early) { return 0; } …; tail }`), not the value expression — an
	// if-EXPRESSION requires `else`, so an else-less `if` can never be a tail
	// value anyway. An `if` WITH `else` stays on the expression path so
	// `{ …; if (c) { a } else { b } }` still yields a value and the else-if
	// chain form is unaffected.
	if t.Kind == lexer.Keyword && t.Text == "if" && !p.ifStmtHasElse() {
		return true
	}
	if t.Kind == lexer.Keyword {
		switch t.Text {
		// `if` (with else) / `match` are deliberately NOT listed: they can
		// stand as the trailing value expression (`{ …; if (c) { a } else { b } }`),
		// and the existing single-expr branch form
		// `if (a) { … } else { if (c) { … } else { … } }` relies on the
		// inner `if`/`match` parsing as an expression. The expr path in
		// parseBranchBody then decides statement-vs-tail by the trailing
		// `;` / `}`, so they still work as ExprStmts when followed by `;`.
		case "while", "loop", "for", "break", "continue",
			"return", "defer", "errdefer", "var", "let",
			"function", "use":
			return true
		}
	}
	return false
}

// ifStmtHasElse reports whether the `if` the cursor is on has a trailing
// `else` clause, by skipping its paren-balanced condition and brace-balanced
// then-body and peeking for `else`. Used by branchStmtStart to route an
// else-less `if` in a block body through the statement path (#4522) — such an
// `if` cannot be a value expression, so it is control flow.
func (p *parser) ifStmtHasElse() bool {
	i := p.i + 1 // skip `if`
	// Skip `( … )` (paren-balanced).
	if i < len(p.tokens) && p.tokens[i].Kind == lexer.Punct && p.tokens[i].Text == "(" {
		depth := 0
		for ; i < len(p.tokens); i++ {
			t := p.tokens[i]
			if t.Kind == lexer.Punct && t.Text == "(" {
				depth++
			} else if t.Kind == lexer.Punct && t.Text == ")" {
				depth--
				if depth == 0 {
					i++
					break
				}
			}
		}
	}
	// Skip `{ … }` (brace-balanced) then-body.
	if i < len(p.tokens) && p.tokens[i].Kind == lexer.Punct && p.tokens[i].Text == "{" {
		depth := 0
		for ; i < len(p.tokens); i++ {
			t := p.tokens[i]
			if t.Kind == lexer.Punct && t.Text == "{" {
				depth++
			} else if t.Kind == lexer.Punct && t.Text == "}" {
				depth--
				if depth == 0 {
					i++
					break
				}
			}
		}
	}
	return i < len(p.tokens) && p.tokens[i].Kind == lexer.Keyword && p.tokens[i].Text == "else"
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
func (p *parser) parseFor(label string) (ast.Stmt, error) {
	kw := p.advance()

	// Foreach shape: `for IDENT in expr body`. Detect by looking at
	// the next two tokens (an Ident followed by the keyword-ish
	// `in`). The lexer treats `in` as a regular identifier, so we
	// match on text rather than kind.
	if p.match(lexer.Ident, "") && p.i+1 < len(p.tokens) {
		if next := p.tokens[p.i+1]; next.Kind == lexer.Ident && next.Text == "in" {
			return p.parseForEach(kw, label)
		}
	}

	// Map foreach shape: `for (K, V) in expr body`. The opening `(`
	// is shared with the C-style for, so disambiguate by peeking the
	// fixed `( IDENT , IDENT ) in` prefix. C-style starts with `var`,
	// `;`, or an arbitrary expression — none of them match this
	// pattern, so the lookahead is unambiguous.
	if p.match(lexer.Punct, "(") && p.i+5 < len(p.tokens) {
		t1, t2, t3 := p.tokens[p.i+1], p.tokens[p.i+2], p.tokens[p.i+3]
		// The binder is a comma-separated list like any other, so it may
		// carry a trailing comma — `for (k, v,) in m`. That shifts the
		// `)` and the `in` one token right; without accounting for it the
		// lookahead fails and the whole form falls through to the C-style
		// `for`, which reports `expected ";", got ","`.
		closeAt := p.i + 4
		if p.tokens[closeAt].Kind == lexer.Punct && p.tokens[closeAt].Text == "," {
			closeAt++
		}
		if closeAt+1 < len(p.tokens) &&
			t1.Kind == lexer.Ident &&
			t2.Kind == lexer.Punct && t2.Text == "," &&
			t3.Kind == lexer.Ident &&
			p.tokens[closeAt].Kind == lexer.Punct && p.tokens[closeAt].Text == ")" &&
			p.tokens[closeAt+1].Kind == lexer.Ident && p.tokens[closeAt+1].Text == "in" {
			return p.parseForEachMapTuple(kw, label)
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

	return &ast.For{P: kw.Pos, Init: init, Cond: cond, Step: step, Body: body, Label: label}, nil
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
//	{
//	  var __foreach_iter_N = expr;
//	  var __foreach_len_N  = len(__foreach_iter_N);
//	  var __foreach_idx_N  = 0;
//	  while (__foreach_idx_N < __foreach_len_N) {
//	    var IDENT = __foreach_iter_N[__foreach_idx_N];
//	    <body>
//	    __foreach_idx_N = __foreach_idx_N + 1;
//	  }
//	}
//
// Works for both arrays (any element type) and strings (each
// element a number = byte). The IDENT's type is inferred from the
// indexed expression by the checker.
func (p *parser) parseForEach(kw lexer.Token, label string) (ast.Stmt, error) {
	nameTok := p.advance() // IDENT
	p.advance()            // `in`
	// Suppress trailing struct-literal parsing while reading the
	// source — the `{` that follows opens the loop body, not a
	// struct lit.
	prevNS := p.noStructLit
	p.noStructLit = true
	// Read the iterable / LOW bound BELOW the range-expression level
	// (parseLogicOr, not parseExpr) so `for i in 0..n` stops at `..` and keeps
	// the optimized counted-loop desugar below, rather than collapsing `0..n`
	// into an `iter.range(0, n)` iterator value.
	expr, err := p.parseLogicOr()
	p.noStructLit = prevNS
	if err != nil {
		return nil, err
	}
	// Range form: `for IDENT in LOW..HIGH body` — desugar to a
	// C-style for over the half-open interval [LOW, HIGH). HIGH is
	// bound once (so `for i in 0..f()` calls f() a single time), and
	// the loop variable IS the user binding, so `continue` — which
	// runs the For step — still increments. parseExpr stops at `..`
	// (not a binary operator), so `expr` is LOW. The inclusive form
	// `LOW..=HIGH` is identical bar the loop condition: `i <= hi`
	// covers the closed interval [LOW, HIGH].
	if p.match(lexer.Punct, "..") || p.match(lexer.Punct, "..=") {
		rangeTok := p.advance() // `..` or `..=`
		inclusive := rangeTok.Text == "..="
		prevNS2 := p.noStructLit
		p.noStructLit = true
		high, err := p.parseLogicOr()
		p.noStructLit = prevNS2
		if err != nil {
			return nil, err
		}
		body, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		rid := p.nextForeachID()
		hiName := fmt.Sprintf("__range_hi_%d", rid)
		mkI := func(name string) *ast.Ident { return &ast.Ident{P: kw.Pos, Name: name} }
		declHi := &ast.Var{P: kw.Pos, Name: hiName, Init: high}
		cmpOp := "<"
		if inclusive {
			cmpOp = "<="
		}
		loop := &ast.For{
			P:    kw.Pos,
			Init: &ast.Var{P: nameTok.Pos, Name: nameTok.Text, Init: expr},
			Cond: &ast.Binary{P: kw.Pos, Op: cmpOp, Left: mkI(nameTok.Text), Right: mkI(hiName)},
			Step: &ast.ExprStmt{P: kw.Pos, Expr: &ast.Assign{P: kw.Pos, Target: mkI(nameTok.Text),
				Value: &ast.Binary{P: kw.Pos, Op: "+", Left: mkI(nameTok.Text), Right: &ast.NumberLit{P: kw.Pos, Value: 1}}}},
			Body:  body,
			Label: label,
		}
		sugar := &ast.ForEach{P: kw.Pos, ID: rid, Var: nameTok.Text, VarPos: nameTok.Pos,
			Iter: expr, RangeHigh: high, RangeIncl: inclusive, Body: body, Label: label}
		return &ast.Block{P: kw.Pos, Stmts: []ast.Stmt{declHi, loop}, Sugar: sugar}, nil
	}
	body, err := p.parseStmt()
	if err != nil {
		return nil, err
	}
	// Emit the un-desugared ForEach; desugarForEachProgram (end of ParseContext)
	// lowers it once all decls are known, so the choice can be decl-aware
	// (array/string → `.len()` + index via ast.DesugarForEachArray; `stream[T]`
	// → a lazy per-element read loop). The ID gives the lowering unique
	// helper-var names, matching the old parse-time desugar.
	return &ast.ForEach{
		P:      kw.Pos,
		ID:     p.nextForeachID(),
		Var:    nameTok.Text,
		VarPos: nameTok.Pos,
		Iter:   expr,
		Body:   body,
		Label:  label,
	}, nil
}

// desugarForEachProgram lowers every `ast.ForEach` (the plain `for x in expr`
// form) in a freshly-parsed program to its loop, right after parse and before
// any downstream pass — so modload / constfold / the checker / codegen all see
// the desugared block, exactly as the old parse-time desugar did. The ForEach
// node exists only so a single decl-aware lowering owns the choice (array
// `.len()`+index today; a `stream[T]` per-element read loop later). Covers every
// body root: functions, hoisted methods, trait default methods, and const
// initialisers (lambdas / block-exprs within are reached through the expr walk).
func desugarForEachProgram(prog *ast.Program) {
	// A `for x in f(args)` whose callee `f` is a module-local `@import async
	// function f(): stream[T]` iterates the stream LAZILY (element-at-a-time off
	// the wire), so its ForEach node is LEFT for the checker to desugar once the
	// stream rewrite has run (docs/STREAM-TYPE-SURFACE.md, L2). Every other
	// iterand (arrays, strings) is lowered here, at parse time, to the `.len()` +
	// index C-style loop via ast.DesugarForEachArray. Build that stream-import
	// name set first so the lowering can tell the two apart.
	streamFns := map[string]bool{}
	for _, fn := range prog.Funcs {
		if fn.ImportIface != "" && fn.Async {
			// Lazy iteration covers any SCALAR element (u8 / i32 / i64 / f64 / …):
			// the cursor desugar separates the EOF flag from the value read, so
			// there's no `-1`-sentinel ambiguity (ast.DesugarForEachStream). A
			// non-scalar element (string / struct / enum) has no StreamElemKind and
			// still iterates EAGERLY via the array desugar (collect-then-iterate).
			// See docs/STREAM-TYPE-SURFACE.md.
			if st, ok := fn.ReturnType.(ast.StreamType); ok && ast.StreamElemKind(st.Elem) != "" {
				streamFns[fn.Name] = true
			}
		}
	}
	for _, fn := range prog.Funcs {
		if fn.Body != nil {
			desugarForEachStmt(fn.Body, streamFns)
		}
	}
	for _, tr := range prog.Traits {
		for i := range tr.Methods {
			if tr.Methods[i].Body != nil {
				desugarForEachStmt(tr.Methods[i].Body, streamFns)
			}
		}
	}
	for _, cn := range prog.Consts {
		desugarForEachExpr(cn.Value, streamFns)
	}
}

// isLazyStreamIter reports whether iterand `e` is a direct call `f(args)` to a
// module-local async stream import `f` — the one for-in shape that iterates
// lazily and therefore keeps its ast.ForEach node for the checker to lower.
func isLazyStreamIter(e ast.Expr, streamFns map[string]bool) bool {
	call, ok := e.(*ast.Call)
	if !ok {
		return false
	}
	id, ok := call.Callee.(*ast.Ident)
	return ok && streamFns[id.Name]
}

// desugarForEachStmt recursively lowers every ForEach reachable from s, returning
// the replacement for s. `*ast.Block`-typed fields are mutated in place (return
// ignored); plain `ast.Stmt` fields (brace-less loop/if bodies can be a bare
// ForEach) get the return assigned back by the caller.
func desugarForEachStmt(s ast.Stmt, streamFns map[string]bool) ast.Stmt {
	switch x := s.(type) {
	case nil:
		return nil
	case *ast.ForEach:
		x.Body = desugarForEachStmt(x.Body, streamFns)
		desugarForEachExpr(x.Iter, streamFns)
		// A lazy stream iterand keeps its ForEach node for the checker (L2);
		// every other iterand lowers to the array `.len()`+index loop here.
		if isLazyStreamIter(x.Iter, streamFns) {
			return x
		}
		return ast.DesugarForEachArray(x)
	case *ast.Block:
		for i := range x.Stmts {
			x.Stmts[i] = desugarForEachStmt(x.Stmts[i], streamFns)
		}
	case *ast.If:
		desugarForEachExpr(x.Cond, streamFns)
		x.Then = desugarForEachStmt(x.Then, streamFns)
		if x.Else != nil {
			x.Else = desugarForEachStmt(x.Else, streamFns)
		}
	case *ast.While:
		desugarForEachExpr(x.Cond, streamFns)
		x.Body = desugarForEachStmt(x.Body, streamFns)
	case *ast.Loop:
		x.Body = desugarForEachStmt(x.Body, streamFns)
	case *ast.For:
		if x.Init != nil {
			x.Init = desugarForEachStmt(x.Init, streamFns)
		}
		desugarForEachExpr(x.Cond, streamFns)
		if x.Step != nil {
			x.Step = desugarForEachStmt(x.Step, streamFns)
		}
		x.Body = desugarForEachStmt(x.Body, streamFns)
	case *ast.Match:
		desugarForEachExpr(x.Tag, streamFns)
		for _, arm := range x.Arms {
			desugarForEachStmt(arm.Body, streamFns)
		}
	case *ast.Var:
		desugarForEachExpr(x.Init, streamFns)
	case *ast.ExprStmt:
		desugarForEachExpr(x.Expr, streamFns)
	case *ast.Return:
		desugarForEachExpr(x.Value, streamFns)
	case *ast.Destructure:
		desugarForEachExpr(x.Init, streamFns)
	}
	return s
}

// desugarForEachExpr lowers for-in nested inside a block-expression or lambda
// body within an expression tree.
func desugarForEachExpr(e ast.Expr, streamFns map[string]bool) {
	if e == nil {
		return
	}
	ast.Walk(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BlockExpr:
			for i := range x.Stmts {
				x.Stmts[i] = desugarForEachStmt(x.Stmts[i], streamFns)
			}
		case *ast.Lambda:
			desugarForEachStmt(x.Body, streamFns)
		}
		return true
	})
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
func (p *parser) parseForEachMapTuple(kw lexer.Token, label string) (ast.Stmt, error) {
	p.advance() // `(`
	keyTok := p.advance()
	p.advance() // `,`
	valTok := p.advance()
	p.accept(lexer.Punct, ",") // optional trailing comma
	p.advance()                // `)`
	p.advance()                // `in`

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
		P:     kw.Pos,
		Cond:  callOnIter("has_next"),
		Step:  stepStmt,
		Body:  innerBlock,
		Label: label,
	}

	return &ast.Block{
		P:     kw.Pos,
		Stmts: []ast.Stmt{declIter, forLoop},
		Sugar: &ast.ForEach{P: kw.Pos, ID: id, Var: keyTok.Text, VarPos: keyTok.Pos,
			Var2: valTok.Text, Iter: expr, Body: body, Label: label},
	}, nil
}

// parseMatch parses `match (<expr>) { Pat => { … }, … }`. The
// tag expression is parenthesised (avoiding the `Ident { … }`
// ambiguity with struct-literal shorthand).
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
// generic type-args list as opposed to an indexing / slicing
// `[...]`. The cheap-and-correct disambiguator: the first token
// AFTER the `[` must be a type-keyword (i32, u32, string,
// boolean, void, f32, f64, usize, ...) — those tokens can't
// appear in an indexing expression. The closing `]` must be
// followed by `opener`: `(` for a call's type args
// (`f[i32](args)`), `{` for a struct literal's
// (`Box[i32] { … }`). If either condition fails the caller
// falls through to the regular Index / Slice handling.
// Doesn't consume tokens.
func (p *parser) peekTypeArgs(opener string) bool {
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
	case "i32", "i64",
		"u8", "u32", "u64",
		"usize", "f32", "f64",
		"string", "boolean", "void":
		// fallthrough — keep walking to find `]` followed by `opener`
	default:
		return false
	}
	// Walk forward looking for the matching `]` at the same
	// bracket depth. Then check the token after is `opener`.
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
						return nt.Kind == lexer.Punct && nt.Text == opener
					}
					return false
				}
			}
		}
	}
	return false
}

// parseTypeArgList parses the comma-separated types of a
// `[T1, T2]` type-argument list, the `[` already consumed, and
// consumes the closing `]`. A trailing comma is accepted.
func (p *parser) parseTypeArgList() ([]ast.Type, error) {
	var args []ast.Type
	for {
		t, err := p.parseType()
		if err != nil {
			return nil, err
		}
		args = append(args, t)
		if !p.moreElems("]") {
			break
		}
	}
	if _, err := p.expect(lexer.Punct, "]"); err != nil {
		return nil, err
	}
	return args, nil
}

// isLiteralPatternStart reports whether the token at hand opens
// a literal pattern in match-arm position — a NumberLit,
// FloatLit, StringLit, CharLit, or the `true` / `false` keywords.
// Variant names live in lexer.Ident space and are handled by the
// surrounding parseMatchArm branch.
func isLiteralPatternStart(t lexer.Token) bool {
	switch t.Kind {
	case lexer.Number, lexer.Float, lexer.String, lexer.Char, lexer.Byte:
		return true
	}
	if t.Kind == lexer.Keyword && (t.Text == "true" || t.Text == "false") {
		return true
	}
	return false
}

// atLiteralPattern is isLiteralPatternStart plus the leading `-` of a
// negative number. A negative literal is a literal — `match (n) { -1 => …,
// 0 => … }` is ordinary code — but the sign is a separate token, so the
// single-token test above declined it and the arm was rejected by a message
// that listed "literal" among what it accepted.
//
// Only a number may follow the minus. `-x` stays out: an identifier in arm
// position is a variant name, not a value to negate.
func (p *parser) atLiteralPattern() bool {
	if isLiteralPatternStart(p.peek()) {
		return true
	}
	if t := p.peek(); t.Kind == lexer.Punct && t.Text == "-" {
		n := p.peekAt(1)
		return n.Kind == lexer.Number || n.Kind == lexer.Float
	}
	return false
}

// parseLiteralPattern consumes one literal pattern, folding a leading `-`
// into the literal it negates. Folding rather than wrapping in a Unary node
// keeps `Pattern.Literal` a bare literal, which is what every consumer
// downstream — the checker's type unification, the IR's equality dispatch —
// already knows how to read.
func (p *parser) parseLiteralPattern() (ast.Expr, error) {
	neg := false
	if t := p.peek(); t.Kind == lexer.Punct && t.Text == "-" {
		p.advance()
		neg = true
	}
	lit, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if !neg {
		return lit, nil
	}
	switch lit.(type) {
	case *ast.NumberLit, *ast.FloatLit:
	default:
		return nil, p.errorfCode(p.peek().Pos, "P001", "`-` in a match arm must be followed by a number")
	}
	// Wrap rather than folding the sign into the literal's Value. A negative
	// NumberLit.Value means "unsigned magnitude above i64::MAX" everywhere
	// else in the tree, so folding made `-1` indistinguishable from
	// `18446744073709551615` — which is what the formatter then printed.
	return &ast.Unary{P: lit.Pos(), Op: "-", Operand: lit}, nil
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
	var raw []stmtRawArm
	for !p.match(lexer.Punct, "}") {
		armRaw, err := p.parseStmtRawArms()
		if err != nil {
			return nil, err
		}
		raw = append(raw, armRaw...)
		if _, ok := p.accept(lexer.Punct, ","); ok {
			continue
		}
		break
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	arms, err := p.desugarNestedStmtArms(raw)
	if err != nil {
		return nil, err
	}
	m.Arms = arms
	return m, nil
}

// stmtRawArm is one fully-parsed statement-match arm alternative — a
// single pattern (or-patterns already split into one raw arm each) with
// its per-alternative guard + body — before the nested-pattern desugar
// collapses it to a flat *ast.MatchArm. Carrying the parser-local
// matchPattern (with its subPats) is what lets desugarNestedStmtArms see
// the sub-pattern structure the flat *ast.MatchArm can't hold.
type stmtRawArm struct {
	pat   matchPattern
	guard ast.Expr
	body  *ast.Block
}

// parseStmtRawArms parses one arm head (`P1 | P2 | … [when g] =>`) plus
// its block body, returning one stmtRawArm per or-pattern alternative
// (the guard/body cloned per alternative, exactly as parseMatchArm did).
// Nested sub-patterns are rejected inside an or-pattern alternative for
// now — an or-pattern binds one shared name set, which a nested pattern
// would violate; use separate arms.
func (p *parser) parseStmtRawArms() ([]stmtRawArm, error) {
	pats, guard, err := p.parseArmPatterns()
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	if err := p.rejectNestedInOrPattern(pats); err != nil {
		return nil, err
	}
	out := make([]stmtRawArm, len(pats))
	for i, pat := range pats {
		g, b := guard, body
		if i > 0 {
			if guard != nil {
				g = ast.CloneExpr(guard)
			}
			b = ast.CloneBlock(body)
		}
		out[i] = stmtRawArm{pat: pat, guard: g, body: b}
	}
	return out, nil
}

// rejectNestedInOrPattern enforces the or-pattern restriction shared by
// every binding site: an or-pattern binds one shared name set, which a
// nested sub-pattern would violate. Single-alternative heads are exempt.
func (p *parser) rejectNestedInOrPattern(pats []matchPattern) error {
	if len(pats) < 2 {
		return nil
	}
	for _, pt := range pats {
		if pt.hasNestedSub() {
			return p.errorfCode(pt.P, "P001",
				"or-patterns (`|`) may not contain nested patterns — use separate arms")
		}
	}
	return nil
}

// parseIfLet parses `if let P1 | P2 | … = <expr> <then> [else <else>]`
// and desugars it to the equivalent statement match
//
//	match (<expr>) { P1 => { then }, …, _ => { else } }
//
// tagged Origin OriginIfLet. Consuming parseMatchPattern is what gives
// `if let` the whole shared pattern grammar — struct patterns, tuple
// patterns, nested patterns, `@` bindings, literals and ranges — and
// keeps refutability and exhaustiveness in the one place that already
// reasons about them. The `let` keyword has been peeked, not consumed.
func (p *parser) parseIfLet(ifPos ast.Position) (ast.Stmt, error) {
	p.advance() // let
	pats, err := p.parseOrPatterns()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, "="); err != nil {
		return nil, err
	}
	// Suppress trailing struct-literal parsing while reading the source —
	// the `{` that follows opens the then-branch.
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
	elseBlk := &ast.Block{P: ifPos}
	if _, ok := p.accept(lexer.Keyword, "else"); ok {
		els, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		elseBlk = stmtAsBlock(els)
	}
	return p.buildPatternBindingMatch(ifPos, pats, src, stmtAsBlock(then), elseBlk, ast.OriginIfLet)
}

// parseOrPatterns parses the `P1 | P2 | …` head shared by `if let` and
// `let … else` — parseMatchPattern alternatives with no guard and no
// `=>`, leaving the cursor on the `=`.
func (p *parser) parseOrPatterns() ([]matchPattern, error) {
	first, err := p.parseMatchPattern()
	if err != nil {
		return nil, err
	}
	pats := []matchPattern{first}
	for p.match(lexer.Punct, "|") {
		p.advance()
		nxt, err := p.parseMatchPattern()
		if err != nil {
			return nil, err
		}
		pats = append(pats, nxt)
	}
	if err := p.rejectNestedInOrPattern(pats); err != nil {
		return nil, err
	}
	return pats, nil
}

// buildPatternBindingMatch assembles the desugared match both binding
// forms produce: one arm per or-pattern alternative running success,
// then a trailing wildcard arm running els. desugarNestedStmtArms also
// reads that wildcard as the outer fallthrough, so a nested head
// (`if let Some(Ok(n)) = e`) routes a `Some(Err(_))` payload into the
// else rather than falling off a non-exhaustive inner match.
func (p *parser) buildPatternBindingMatch(pos ast.Position, pats []matchPattern, src ast.Expr, success, els *ast.Block, origin string) (ast.Stmt, error) {
	raw := make([]stmtRawArm, 0, len(pats)+1)
	for i, pat := range pats {
		b := success
		if i > 0 {
			b = ast.CloneBlock(success)
		}
		raw = append(raw, stmtRawArm{pat: pat, body: b})
	}
	raw = append(raw, stmtRawArm{pat: matchPattern{P: pos, IsWildcard: true}, body: els})
	arms, err := p.desugarNestedStmtArms(raw)
	if err != nil {
		return nil, err
	}
	return &ast.Match{P: pos, Tag: src, Arms: arms, Origin: origin}, nil
}

// stmtAsBlock adapts a single statement to the *ast.Block a match arm
// body needs. `if let Some(x) = o return x;` takes a bare statement;
// wrapping it introduces the same scope the braced form has.
func stmtAsBlock(s ast.Stmt) *ast.Block {
	if b, ok := s.(*ast.Block); ok {
		return b
	}
	return &ast.Block{P: s.Pos(), Stmts: []ast.Stmt{s}}
}

// stmtArmFromPattern builds a flat *ast.MatchArm from a (already
// nesting-free) pattern + guard + body — the same field copy
// parseMatchArm performed inline.
func stmtArmFromPattern(pat matchPattern, guard ast.Expr, body *ast.Block) *ast.MatchArm {
	return &ast.MatchArm{
		P: pat.P, VariantName: pat.VariantName, VariantModule: pat.VariantModule,
		Bindings: pat.Bindings, NamedFields: pat.NamedFields, IsWildcard: pat.IsWildcard,
		Literal: pat.Literal, RangeHi: pat.RangeHi, RangeInclusive: pat.RangeInclusive,
		TupleElems: pat.TupleElems, AtBinding: pat.atBinding, FieldNames: pat.fieldNames,
		SlotBinderName: pat.slotBinder, Guard: guard, Body: body,
	}
}

// freshNestName mints a unique synthetic temp name for a nested-pattern
// payload slot.
func (p *parser) freshNestName() string {
	n := p.nestN
	p.nestN++
	return fmt.Sprintf("__nest%d", n)
}

// desugarNestedStmtArms rewrites arms carrying nested sub-patterns
// (`Some(Ok(n))`) into flat arms whose body re-matches the payload — so
// every downstream stage (checker, IR) sees only ordinary flat arms plus
// an inner match, needing no notion of pattern nesting. Arms of an outer
// variant that has nested sub-patterns are grouped (they must be
// contiguous) into one merged arm `V(__nest…) => match __nest… { … }`.
// Recurses so `Some(Ok(Some(x)))` desugars at every depth.
func (p *parser) desugarNestedStmtArms(raw []stmtRawArm) ([]*ast.MatchArm, error) {
	// An unguarded trailing `_` arm is the outer fallthrough: a value whose
	// outer variant matches a nested group but whose payload matches none of
	// that group's inner patterns must run this body (e.g. `Some(Ok(n)) => A,
	// _ => B` where B catches `Some(Err(_))`). Grouping consumes the whole
	// outer variant, so the body is copied into each merged inner match as
	// its wildcard arm; the outer `_` stays for the other variants.
	var fall *ast.Block
	if n := len(raw); n > 0 && raw[n-1].pat.IsWildcard && raw[n-1].guard == nil {
		fall = raw[n-1].body
	}
	var out []*ast.MatchArm
	anyMerged := false
	i := 0
	for i < len(raw) {
		a := raw[i]
		if a.pat.VariantName == "" { // wildcard / literal / tuple — never nests
			out = append(out, stmtArmFromPattern(a.pat, a.guard, a.body))
			i++
			continue
		}
		V, mod := a.pat.VariantName, a.pat.VariantModule
		j := i
		for j < len(raw) && raw[j].pat.VariantName == V && raw[j].pat.VariantModule == mod {
			j++
		}
		group := raw[i:j]
		anyNested := false
		for k := range group {
			if group[k].pat.hasNestedSub() {
				anyNested = true
			}
		}
		if !anyNested {
			for k := range group {
				out = append(out, stmtArmFromPattern(group[k].pat, group[k].guard, group[k].body))
			}
			i = j
			continue
		}
		for k := j; k < len(raw); k++ {
			if raw[k].pat.VariantName == V && raw[k].pat.VariantModule == mod {
				return nil, p.errorfCode(raw[k].pat.P, "P001",
					"arms for `%s` with nested patterns must be contiguous", V)
			}
		}
		merged, err := p.buildMergedStmtArm(V, mod, group, fall)
		if err != nil {
			return nil, err
		}
		out = append(out, merged)
		anyMerged = true
		i = j
	}
	// The trailing `_` body now also lives inside each merged arm's inner
	// match. Flag it so reachability does not call it unreachable when a
	// merged arm's own pattern covers every value — see ast.FallConsumed.
	if anyMerged && fall != nil && len(out) > 0 {
		if last := out[len(out)-1]; last.IsWildcard && last.Guard == nil {
			last.FallConsumed = true
		}
	}
	return out, nil
}

// nestedPos returns the single payload index that carries a nested
// sub-pattern across the group, or an error when an arm nests more than
// one position or different arms nest different positions (both out of
// the v1 scope: exactly one nested payload slot per variant group).
func (p *parser) nestedPos(group []stmtRawArm) (int, error) {
	pos := -1
	for k := range group {
		sps := group[k].pat.subPats
		cnt := 0
		local := -1
		for idx, sp := range sps {
			if sp != nil {
				cnt++
				local = idx
			}
		}
		if cnt > 1 {
			return -1, p.errorfCode(group[k].pat.P, "P001",
				"only one nested pattern per payload is supported — use a nested `match`")
		}
		if local >= 0 {
			if pos == -1 {
				pos = local
			} else if pos != local {
				return -1, p.errorfCode(group[k].pat.P, "P001",
					"nested patterns for the same variant must all be at the same payload position")
			}
		}
	}
	return pos, nil
}

// buildMergedStmtArm collapses one contiguous run of same-variant arms —
// at least one of which nests at payload position `pos` — into a single
// flat arm `V(__nest0, …) => match __nestPos { <inner arms> }`. Plain
// payload slots (and a flat sibling arm's whole-payload binder) are
// rebound with `var name = __nestK;` at the head of each inner body, so
// the original binding names stay in scope.
func (p *parser) buildMergedStmtArm(V, mod string, group []stmtRawArm, fall *ast.Block) (*ast.MatchArm, error) {
	pos, err := p.nestedPos(group)
	if err != nil {
		return nil, err
	}
	if err := p.sameFieldList(group); err != nil {
		return nil, err
	}
	arity := len(group[0].pat.Bindings)
	tmps := make([]string, arity)
	for k := range tmps {
		tmps[k] = p.freshNestName()
	}
	gp := group[0].pat.P
	var inner []stmtRawArm
	hasInnerWild := false
	for k := range group {
		g := group[k]
		var innerPat matchPattern
		if g.pat.subPats[pos] != nil {
			innerPat = *g.pat.subPats[pos]
		} else {
			// A flat sibling (`Some(x)`): matches any payload here — an inner
			// wildcard that rebinds the whole slot to the sibling's name. The
			// name rides along so the checker can still tell whether it was
			// meant as a variant rather than a binder.
			innerPat = matchPattern{P: g.pat.P, IsWildcard: true, slotBinder: slotBinderOf(g.pat, pos)}
		}
		if coversEveryValue(innerPat) && g.guard == nil {
			hasInnerWild = true
		}
		body := p.rebindStmtBody(g.pat, pos, tmps, g.body)
		inner = append(inner, stmtRawArm{pat: innerPat, guard: g.guard, body: body})
	}
	// Route the outer fallthrough into this inner match so a payload matching
	// none of the inner patterns runs the outer `_` body, not a non-exhaustive
	// bail. Skipped when a flat sibling already supplied an inner catch-all.
	if !hasInnerWild && fall != nil {
		inner = append(inner, stmtRawArm{pat: matchPattern{P: gp, IsWildcard: true}, body: ast.CloneBlock(fall)})
	}
	innerArms, err := p.desugarNestedStmtArms(inner)
	if err != nil {
		return nil, err
	}
	innerMatch := &ast.Match{P: gp, Tag: &ast.Ident{P: gp, Name: tmps[pos]}, Arms: innerArms}
	return &ast.MatchArm{
		P:             gp,
		VariantName:   V,
		VariantModule: mod,
		Bindings:      tmps,
		// A named-field group keeps its shape: the merged arm still projects
		// by FIELD, with the synthetic temps standing in for the binders.
		// Dropping this would leave a struct arm looking positional, which
		// the checker rejects (E035) and whose temps nothing would bind.
		NamedFields: group[0].pat.NamedFields,
		FieldNames:  fieldNamesOf(group[0].pat),
		Body:        &ast.Block{P: gp, Stmts: []ast.Stmt{innerMatch}},
	}, nil
}

// fieldNamesOf returns a named-field pattern's projected field names, nil
// for a positional one.
func fieldNamesOf(pat matchPattern) []string {
	if !pat.NamedFields {
		return nil
	}
	return append([]string(nil), pat.fieldNames...)
}

// sameFieldList rejects a nested-pattern group whose named-field arms do not
// all project the same fields in the same order. The merged arm carries ONE
// field list (group[0]'s) and one temp per slot, so arms listing different
// fields would bind the wrong values — a positional group cannot hit this
// because a variant's payload arity is fixed.
func (p *parser) sameFieldList(group []stmtRawArm) error {
	if !group[0].pat.NamedFields {
		return nil
	}
	want := group[0].pat.fieldNames
	for k := 1; k < len(group); k++ {
		got := group[k].pat.fieldNames
		same := len(got) == len(want)
		for i := 0; same && i < len(want); i++ {
			same = got[i] == want[i]
		}
		if !same {
			return p.errorfCode(group[k].pat.P, "P001",
				"arms for `%s` with nested field patterns must list the same fields in the same order — this arm lists {%s}, the group lists {%s}",
				group[k].pat.VariantName, strings.Join(got, ", "), strings.Join(want, ", "))
		}
	}
	return nil
}

// slotBinderOf is the name a flat sibling arm bound the merged slot to, or
// "" when it bound nothing there (`Some(_)`).
func slotBinderOf(pat matchPattern, pos int) string {
	if pos >= len(pat.Bindings) {
		return ""
	}
	if name := pat.Bindings[pos]; name != "_" {
		return name
	}
	return ""
}

// rebindStmtBody prepends `var name = __nestK;` binders for every payload
// slot the original arm named — every slot except the nested one (whose
// sub-pattern introduces its own bindings). A flat sibling arm names the
// nested slot too, so that slot is rebound as well.
func (p *parser) rebindStmtBody(pat matchPattern, pos int, tmps []string, body *ast.Block) *ast.Block {
	var binds []ast.Stmt
	for k, name := range pat.Bindings {
		nested := k < len(pat.subPats) && pat.subPats[k] != nil
		if nested || name == "" || name == "_" {
			continue
		}
		binds = append(binds, &ast.Var{P: pat.P, Name: name, Init: &ast.Ident{P: pat.P, Name: tmps[k]}})
	}
	if len(binds) == 0 {
		return body
	}
	stmts := append(binds, body.Stmts...)
	return &ast.Block{P: body.P, Stmts: stmts}
}

// parseNamedFieldPattern parses a named-field variant pattern body
// `{ f1, f2 }` — shorthand where each field binds a local of the same
// name — with the cursor just after the variant name. Returns ok=true
// (and the field-name bindings) when a `{` was present; ok=false with no
// error when the next token isn't `{` (a positional or payloadless arm).
// Returns parallel field / binding lists: for the shorthand `S { x }` the
// two are equal; `S { x: nx }` renames field x to local nx.
func (p *parser) parseNamedFieldPattern() (fields, bindings []string, subs []*matchPattern, ok bool, err error) {
	if _, isBrace := p.accept(lexer.Punct, "{"); !isBrace {
		return nil, nil, nil, false, nil
	}
	if !p.match(lexer.Punct, "}") {
		for {
			// A trailing `..` marks intentionally-omitted fields (a partial
			// bind — `Point { x, .. }`). Named-field patterns already bind
			// only the fields they list, so `..` is documentation; consume
			// it and stop. Matches the destructure form and the self-host.
			if p.match(lexer.Punct, "..") {
				p.advance()
				break
			}
			fieldTok, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, nil, nil, false, err
			}
			bind := fieldTok.Text
			var sub *matchPattern
			// `field: <x>` is either a rename binding the field to a local
			// (struct matches only — the checker rejects a rename in an enum
			// named-field pattern) or a SUB-PATTERN matched against the
			// field's value. isNestedPatternStart draws the same line it
			// draws for a payload slot, so `field: local` stays a rename and
			// only an unambiguous pattern recurses.
			if _, isRename := p.accept(lexer.Punct, ":"); isRename {
				if p.isNestedPatternStart() {
					sp, err := p.parseMatchPattern()
					if err != nil {
						return nil, nil, nil, false, err
					}
					sub = &sp
					// A sub-pattern introduces its own bindings; the slot
					// itself binds nothing, matching a nested payload slot.
					bind = ""
				} else {
					bindTok, err := p.expect(lexer.Ident, "")
					if err != nil {
						return nil, nil, nil, false, err
					}
					bind = bindTok.Text
				}
			}
			fields = append(fields, fieldTok.Text)
			bindings = append(bindings, bind)
			subs = append(subs, sub)
			if _, c := p.accept(lexer.Punct, ","); c {
				if p.match(lexer.Punct, "}") {
					break
				}
				continue
			}
			break
		}
	}
	if _, e := p.expect(lexer.Punct, "}"); e != nil {
		return nil, nil, nil, false, e
	}
	return fields, bindings, subs, true, nil
}

// matchPattern is the pattern half of a match arm — the fields that
// distinguish wildcard / literal / variant patterns, with no guard or
// body. Shared by the statement (MatchArm) and expression (MatchExprArm)
// forms so or-pattern parsing (`P1 | P2 => …`) lives in one place.
type matchPattern struct {
	P             ast.Position
	VariantName   string
	VariantModule string
	Bindings      []string
	NamedFields   bool
	IsWildcard    bool
	Literal       ast.Expr
	// RangeHi / RangeInclusive carry a range pattern `lo..hi` / `lo..=hi`
	// (Literal is the low bound). See ast.MatchArm.RangeHi.
	RangeHi        ast.Expr
	RangeInclusive bool
	TupleElems     []ast.TuplePatElem // tuple pattern `(p0, p1, …)`; nil otherwise
	// subPats runs parallel to Bindings: a non-nil entry at position i
	// means that payload slot is itself a nested pattern (`Some(Ok(n))`)
	// rather than a plain binder. Bindings[i] then holds a synthetic
	// temp name the nested-pattern desugar (desugarNestedArms) binds the
	// slot to before re-matching on it. nil (the common case) means a
	// flat binder — every downstream stage sees only flat arms.
	subPats []*matchPattern
	// atBinding is the `n` in an `@`-pattern `n @ <pattern>`: the whole
	// matched value is also bound to `n`. Empty for plain patterns.
	atBinding string
	// fieldNames runs parallel to Bindings for a named-field pattern:
	// the field projected for each binding (== Bindings for shorthand).
	fieldNames []string
	// slotBinder is set on the inner wildcard the merge desugar builds for a
	// flat sibling arm: the name that sibling bound the whole payload slot
	// to. See ast.MatchArm.SlotBinderName.
	slotBinder string
}

// hasNestedSub reports whether any payload slot of this pattern is a
// nested sub-pattern (so the arm needs the group-by-variant desugar).
func (mp *matchPattern) hasNestedSub() bool {
	for _, sp := range mp.subPats {
		if sp != nil {
			return true
		}
	}
	return false
}

// coversEveryValue reports whether an inner sub-pattern matches anything the
// slot can hold, so appending the outer fallthrough after it would produce an
// arm the checker calls unreachable. A `_` obviously covers; so does a tuple
// pattern whose elements are all binders or `_`, which is decidable here
// because a literal or variant element is the only thing that can make one
// fail. Struct patterns are NOT included: the parser cannot tell a struct from
// an enum's record-form variant, and only the former is irrefutable.
func coversEveryValue(mp matchPattern) bool {
	if mp.IsWildcard {
		return true
	}
	if mp.TupleElems == nil {
		return false
	}
	for _, el := range mp.TupleElems {
		if el.Literal != nil || el.VariantName != "" {
			return false
		}
	}
	return true
}

// isNestedPatternStart reports whether the token(s) at the cursor begin
// a nested sub-pattern (as opposed to a bare binder name). A sub-pattern
// is recognised only when it is UNAMBIGUOUSLY a pattern: a literal, or an
// identifier immediately followed by `(` (variant-with-payload), `{`
// (named-field), or `.` (a `mod.Variant` qualifier). A bare identifier —
// or `_` — stays a binder, so `Some(x)` / `Some(_)` / `Pair(a, b)` are
// unaffected. A payload-less nested variant is written with the empty
// parens (`Some(None())`); `Some(None)` is a binder here, and the checker
// rejects it (E015) rather than letting the arm match every payload.
func (p *parser) isNestedPatternStart() bool {
	t := p.peek()
	// A `(` opens a tuple sub-pattern (`Pr((a, b))`). It is unambiguous: a
	// payload slot otherwise holds a binder, a `_`, or a literal, none of
	// which start with `(`.
	if t.Kind == lexer.Punct && t.Text == "(" {
		return true
	}
	if p.atLiteralPattern() {
		return true
	}
	if t.Kind == lexer.Ident && t.Text != "_" {
		n := p.peekAt(1)
		if n.Kind == lexer.Punct && (n.Text == "(" || n.Text == "{" || n.Text == ".") {
			return true
		}
	}
	return false
}

// tupleElemVariantStart reports whether the identifier at the cursor opens a
// variant sub-pattern in a tuple element rather than a binder. A binder is
// followed by `,` or `)`, so `(` (payload list, empty for a payload-less
// variant) and `.` (a `mod.` qualifier) are both unambiguous.
func (p *parser) tupleElemVariantStart() bool {
	n := p.peekAt(1)
	return n.Kind == lexer.Punct && (n.Text == "(" || n.Text == ".")
}

// parseTupleElemVariant fills elem with a tuple element's variant sub-pattern
// `A(x, y)` / `A()` / `mod.A(x)`, leaving the cursor after the closing `)`.
// Payload slots are plain binders — a nested pattern there is step 3 of the
// unified grammar, and rejecting it here keeps it loud rather than silent.
func (p *parser) parseTupleElemVariant(elem *ast.TuplePatElem) error {
	nameTok := p.peek()
	p.advance()
	elem.VariantName = nameTok.Text
	if p.match(lexer.Punct, ".") {
		p.advance()
		vt, err := p.expect(lexer.Ident, "")
		if err != nil {
			return err
		}
		elem.VariantModule = elem.VariantName
		elem.VariantName = vt.Text
	}
	if _, err := p.expect(lexer.Punct, "("); err != nil {
		return err
	}
	if !p.match(lexer.Punct, ")") {
		for {
			bt, err := p.expect(lexer.Ident, "")
			if err != nil {
				return err
			}
			elem.VariantBindings = append(elem.VariantBindings, bt.Text)
			if _, ok := p.accept(lexer.Punct, ","); ok {
				continue
			}
			break
		}
	}
	_, err := p.expect(lexer.Punct, ")")
	return err
}

// parseTuplePatElems consumes `(p0, p1, …)` with the cursor on the opening
// `(`, returning one TuplePatElem per element. An element is a binder name,
// `_`, a literal, a variant sub-pattern `A(x)` / `mod.A(x)` / `A()`, or a
// nested tuple pattern — which recurses here, so `(a, (b, (c, d)))` nests to
// any depth. At least 2 elements at every level (no-singleton-tuples rule).
func (p *parser) parseTuplePatElems() ([]ast.TuplePatElem, error) {
	open := p.peek().Pos
	p.advance() // `(`
	var elems []ast.TuplePatElem
	for {
		et := p.peek()
		var elem ast.TuplePatElem
		switch {
		case et.Kind == lexer.Ident && et.Text == "_":
			p.advance()
			elem.IsWildcard = true
		case et.Kind == lexer.Punct && et.Text == "(":
			nested, err := p.parseTuplePatElems()
			if err != nil {
				return nil, err
			}
			elem.Nested = nested
		case p.atLiteralPattern():
			lit, err := p.parseLiteralPattern()
			if err != nil {
				return nil, err
			}
			elem.Literal = lit
		case et.Kind == lexer.Ident && p.tupleElemVariantStart():
			if err := p.parseTupleElemVariant(&elem); err != nil {
				return nil, err
			}
		case et.Kind == lexer.Ident:
			p.advance()
			elem.Name = et.Text
		default:
			return nil, p.errorfCode(et.Pos, "P001", "expected binder, literal, variant sub-pattern, nested tuple pattern, or `_` in tuple pattern, got %s", et.Text)
		}
		elems = append(elems, elem)
		if _, ok := p.accept(lexer.Punct, ","); ok {
			if p.match(lexer.Punct, ")") {
				break
			}
			continue
		}
		break
	}
	if _, err := p.expect(lexer.Punct, ")"); err != nil {
		return nil, err
	}
	if len(elems) < 2 {
		return nil, p.errorfCode(open, "P001", "tuple pattern needs at least 2 elements")
	}
	return elems, nil
}

// parseMatchPattern parses a single pattern (wildcard / literal / variant
// with optional `mod.` qualifier and positional or named bindings),
// leaving the cursor at the optional `|`, `when`, or `=>` that follows.
func (p *parser) parseMatchPattern() (matchPattern, error) {
	t := p.peek()
	// `n @ <pattern>` — bind the whole matched value to `n` while also
	// matching <pattern>. Recognised only for a real binder name (`_ @ …`
	// is pointless and rejected as a non-start). Recurse for the sub-pattern
	// and stamp atBinding on it.
	if t.Kind == lexer.Ident && t.Text != "_" &&
		p.peekAt(1).Kind == lexer.Punct && p.peekAt(1).Text == "@" {
		p.advance() // binder name
		p.advance() // `@`
		sub, err := p.parseMatchPattern()
		if err != nil {
			return sub, err
		}
		if sub.atBinding != "" {
			return sub, p.errorfCode(t.Pos, "P001", "an `@`-pattern cannot nest another `@` binding")
		}
		// `_` is the one sub-pattern that cannot carry an `@`: the arm binds
		// nothing to project, and every downstream stage treats a wildcard as
		// the unconditional default rather than a pattern with bindings.
		if sub.IsWildcard {
			return sub, p.errorfCode(t.Pos, "P001", "`@` bindings are not supported on a `_` pattern")
		}
		sub.atBinding = t.Text
		return sub, nil
	}
	pat := matchPattern{P: t.Pos}
	if t.Kind == lexer.Ident && t.Text == "_" {
		p.advance()
		pat.IsWildcard = true
	} else if t.Kind == lexer.Punct && t.Text == "(" {
		// Tuple pattern: `(p0, p1, …) => …` on a tuple-typed scrutinee.
		// The checker validates the arity against the scrutinee tuple and
		// types each element.
		elems, err := p.parseTuplePatElems()
		if err != nil {
			return pat, err
		}
		pat.TupleElems = elems
	} else if p.atLiteralPattern() {
		// Literal pattern: `0 => …`, `"yes" => …`, `true => …`,
		// `1.5f64 => …`. Dispatched via equality comparison
		// against the scrutinee at IR-lower time. The checker
		// verifies the literal's type unifies with the
		// scrutinee's type. parsePrimary (not parseExpr) stops at
		// the single literal, so a following `|` reads as an
		// or-pattern separator rather than a bitwise-or operator.
		lit, err := p.parseLiteralPattern()
		if err != nil {
			return pat, err
		}
		pat.Literal = lit
		// Range pattern: `lo..hi` (exclusive hi) / `lo..=hi` (inclusive hi)
		// on a scalar scrutinee. The high bound is a second literal; the
		// arm matches when `lo <= scrutinee <op> hi`.
		if p.match(lexer.Punct, "..") || p.match(lexer.Punct, "..=") {
			_, inclusive := p.accept(lexer.Punct, "..=")
			if !inclusive {
				p.advance() // consume `..`
			}
			hi, err := p.parseLiteralPattern()
			if err != nil {
				return pat, err
			}
			pat.RangeHi = hi
			pat.RangeInclusive = inclusive
		}
	} else if t.Kind == lexer.Ident {
		p.advance()
		pat.VariantName = t.Text
		// Optional `mod.` qualifier: `lexer.TokA(x) => …`. When the
		// next token is `.`, the ident we just consumed was the
		// module name, not the variant — re-consume after the dot.
		// The checker verifies the qualifier against the scrutinee
		// enum's source module.
		if p.match(lexer.Punct, ".") {
			p.advance()
			nameTok, err := p.expect(lexer.Ident, "")
			if err != nil {
				return pat, err
			}
			pat.VariantModule = pat.VariantName
			pat.VariantName = nameTok.Text
		}
		if _, ok := p.accept(lexer.Punct, "("); ok {
			if !p.match(lexer.Punct, ")") {
				for {
					// A payload slot is either a nested sub-pattern
					// (`Some(Ok(n))`) or a bare binder. isNestedPatternStart
					// keeps `Some(x)` / `Some(_)` binders unchanged; only an
					// unambiguous pattern (`V(…)` / `V{…}` / `mod.V` / literal)
					// recurses. A synthetic binder name holds the slot; the
					// desugar re-matches the bound temp against the sub-pattern.
					if p.isNestedPatternStart() {
						sub, err := p.parseMatchPattern()
						if err != nil {
							return pat, err
						}
						subCopy := sub
						pat.Bindings = append(pat.Bindings, "")
						pat.subPats = append(pat.subPats, &subCopy)
					} else {
						nameTok, err := p.expect(lexer.Ident, "")
						if err != nil {
							return pat, err
						}
						pat.Bindings = append(pat.Bindings, nameTok.Text)
						pat.subPats = append(pat.subPats, nil)
					}
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
				return pat, err
			}
		} else if fields, bindings, subs, ok, err := p.parseNamedFieldPattern(); err != nil {
			return pat, err
		} else if ok {
			pat.NamedFields = true
			pat.Bindings = bindings
			pat.fieldNames = fields
			pat.subPats = subs
		}
	} else {
		return pat, p.errorfCode(t.Pos, "P001", "expected variant pattern, literal, or `_` in match arm, got %s", t.Text)
	}
	return pat, nil
}

// parseArmPatterns parses the `P1 | P2 | … [when g] =>` head shared by
// both arm forms: one-or-more `|`-separated patterns, an optional guard
// (which applies to every alternative), and the consuming `=>`. The
// cursor is left at the start of the arm body.
func (p *parser) parseArmPatterns() ([]matchPattern, ast.Expr, error) {
	first, err := p.parseMatchPattern()
	if err != nil {
		return nil, nil, err
	}
	pats := []matchPattern{first}
	for p.match(lexer.Punct, "|") {
		p.advance()
		nxt, err := p.parseMatchPattern()
		if err != nil {
			return nil, nil, err
		}
		pats = append(pats, nxt)
	}
	// Literal or-patterns (`1 | 2 | 3 =>`) and tuple or-patterns
	// (`(1, 2) | (3, 4) =>`) are both allowed: the shared per-alternative
	// clone-desugar expands each alternative into its own independent arm
	// (guard + body cloned), so each binds its own names against its own
	// element positions — `(1, x) | (x, 2)` binds x to whichever element
	// the matched alternative supplies, exactly Rust's per-alternative
	// semantics. The literal split also agrees across both compilers: the
	// self-host literal-match arm loop splits on `|` at a precedence above
	// bitwise-or, so `1 | 2` means "1 or 2", not the value 3.
	// Optional guard: `<pattern> when <expr> => <body>`. The guard
	// expression has the pattern's bindings in scope and applies to
	// every alternative. Pre-`=>` so the syntax reads pattern → guard
	// → body order.
	var guard ast.Expr
	if p.match(lexer.Keyword, "when") {
		p.advance()
		g, err := p.parseExpr()
		if err != nil {
			return nil, nil, err
		}
		guard = g
	}
	if _, err := p.expect(lexer.Punct, "=>"); err != nil {
		return nil, nil, err
	}
	return pats, guard, nil
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
	var raw []exprRawArm
	for !p.match(lexer.Punct, "}") {
		armRaw, err := p.parseExprRawArms()
		if err != nil {
			return nil, err
		}
		raw = append(raw, armRaw...)
		if _, ok := p.accept(lexer.Punct, ","); ok {
			continue
		}
		break
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	arms, err := p.desugarNestedExprArms(raw)
	if err != nil {
		return nil, err
	}
	m.Arms = arms
	return m, nil
}

// parseMatchExprArm parses one expression-form arm: `P1 | P2 | …
// [when <guard>] => <expr>`. Pattern parsing (variant / payload
// bindings / wildcard / or-patterns) is shared with parseMatchArm via
// parseArmPatterns; the only difference is body parsing — a single Expr
// rather than a Block. An or-pattern expands to one MatchExprArm per
// alternative, each sharing the (per-alternative cloned) guard + body.
// exprRawArm is the expression-form sibling of stmtRawArm — one parsed
// alternative (Body an Expr) awaiting the nested-pattern desugar.
type exprRawArm struct {
	pat   matchPattern
	guard ast.Expr
	body  ast.Expr
}

// parseExprRawArms parses one expression-form arm head + body, returning
// one exprRawArm per or-pattern alternative. Nested sub-patterns are
// rejected inside or-pattern alternatives (same rule as the stmt form).
func (p *parser) parseExprRawArms() ([]exprRawArm, error) {
	pats, guard, err := p.parseArmPatterns()
	if err != nil {
		return nil, err
	}
	// A `{ … }` arm body is a block-expression (slice 1): statements
	// then an optional trailing value. A bare expression body stays the
	// pre-block-expr single-expr form. parseBranchBody collapses the
	// no-statement single-expr `{ e }` back to `e`, so existing
	// brace-wrapped single-expr arms are byte-identical.
	var body ast.Expr
	if p.match(lexer.Punct, "{") {
		body, err = p.parseBranchBody()
	} else {
		body, err = p.parseExpr()
	}
	if err != nil {
		return nil, err
	}
	if len(pats) > 1 {
		for _, pt := range pats {
			if pt.hasNestedSub() {
				return nil, p.errorfCode(pt.P, "P001",
					"or-patterns (`|`) may not contain nested patterns — use separate arms")
			}
		}
	}
	out := make([]exprRawArm, len(pats))
	for i, pat := range pats {
		g, b := guard, body
		if i > 0 {
			if guard != nil {
				g = ast.CloneExpr(guard)
			}
			b = ast.CloneExpr(body)
		}
		out[i] = exprRawArm{pat: pat, guard: g, body: b}
	}
	return out, nil
}

// exprArmFromPattern builds a flat *ast.MatchExprArm from a nesting-free
// pattern + guard + body.
func exprArmFromPattern(pat matchPattern, guard, body ast.Expr) *ast.MatchExprArm {
	return &ast.MatchExprArm{
		P: pat.P, VariantName: pat.VariantName, VariantModule: pat.VariantModule,
		Bindings: pat.Bindings, NamedFields: pat.NamedFields, IsWildcard: pat.IsWildcard,
		Literal: pat.Literal, RangeHi: pat.RangeHi, RangeInclusive: pat.RangeInclusive,
		TupleElems: pat.TupleElems, AtBinding: pat.atBinding, FieldNames: pat.fieldNames,
		SlotBinderName: pat.slotBinder, Guard: guard, Body: body,
	}
}

// desugarNestedExprArms is the expression-form twin of
// desugarNestedStmtArms: nested arms group by outer variant into one
// merged arm whose body is an inner MatchExpr.
func (p *parser) desugarNestedExprArms(raw []exprRawArm) ([]*ast.MatchExprArm, error) {
	var fall ast.Expr
	if n := len(raw); n > 0 && raw[n-1].pat.IsWildcard && raw[n-1].guard == nil {
		fall = raw[n-1].body
	}
	var out []*ast.MatchExprArm
	anyMerged := false
	i := 0
	for i < len(raw) {
		a := raw[i]
		if a.pat.VariantName == "" {
			out = append(out, exprArmFromPattern(a.pat, a.guard, a.body))
			i++
			continue
		}
		V, mod := a.pat.VariantName, a.pat.VariantModule
		j := i
		for j < len(raw) && raw[j].pat.VariantName == V && raw[j].pat.VariantModule == mod {
			j++
		}
		group := raw[i:j]
		anyNested := false
		for k := range group {
			if group[k].pat.hasNestedSub() {
				anyNested = true
			}
		}
		if !anyNested {
			for k := range group {
				out = append(out, exprArmFromPattern(group[k].pat, group[k].guard, group[k].body))
			}
			i = j
			continue
		}
		for k := j; k < len(raw); k++ {
			if raw[k].pat.VariantName == V && raw[k].pat.VariantModule == mod {
				return nil, p.errorfCode(raw[k].pat.P, "P001",
					"arms for `%s` with nested patterns must be contiguous", V)
			}
		}
		merged, err := p.buildMergedExprArm(V, mod, group, fall)
		if err != nil {
			return nil, err
		}
		out = append(out, merged)
		anyMerged = true
		i = j
	}
	// See desugarNestedStmtArms: the trailing `_` value now also lives inside
	// each merged arm's inner match.
	if anyMerged && fall != nil && len(out) > 0 {
		if last := out[len(out)-1]; last.IsWildcard && last.Guard == nil {
			last.FallConsumed = true
		}
	}
	return out, nil
}

// buildMergedExprArm is buildMergedStmtArm for the expression form: the
// merged arm's body is an inner MatchExpr, and plain-slot rebinds wrap the
// inner arm body in a BlockExpr (`{ var name = __nestK; <body> }`).
func (p *parser) buildMergedExprArm(V, mod string, group []exprRawArm, fall ast.Expr) (*ast.MatchExprArm, error) {
	stmtGroup := make([]stmtRawArm, len(group))
	for k := range group {
		stmtGroup[k] = stmtRawArm{pat: group[k].pat}
	}
	pos, err := p.nestedPos(stmtGroup)
	if err != nil {
		return nil, err
	}
	if err := p.sameFieldList(stmtGroup); err != nil {
		return nil, err
	}
	arity := len(group[0].pat.Bindings)
	tmps := make([]string, arity)
	for k := range tmps {
		tmps[k] = p.freshNestName()
	}
	gp := group[0].pat.P
	var inner []exprRawArm
	hasInnerWild := false
	for k := range group {
		g := group[k]
		var innerPat matchPattern
		if g.pat.subPats[pos] != nil {
			innerPat = *g.pat.subPats[pos]
		} else {
			innerPat = matchPattern{P: g.pat.P, IsWildcard: true, slotBinder: slotBinderOf(g.pat, pos)}
		}
		if coversEveryValue(innerPat) && g.guard == nil {
			hasInnerWild = true
		}
		body := p.rebindExprBody(g.pat, pos, tmps, g.body)
		inner = append(inner, exprRawArm{pat: innerPat, guard: g.guard, body: body})
	}
	if !hasInnerWild && fall != nil {
		inner = append(inner, exprRawArm{pat: matchPattern{P: gp, IsWildcard: true}, body: ast.CloneExpr(fall)})
	}
	innerArms, err := p.desugarNestedExprArms(inner)
	if err != nil {
		return nil, err
	}
	innerMatch := &ast.MatchExpr{P: gp, Tag: &ast.Ident{P: gp, Name: tmps[pos]}, Arms: innerArms}
	return &ast.MatchExprArm{
		P:             gp,
		VariantName:   V,
		VariantModule: mod,
		Bindings:      tmps,
		// See buildMergedStmtArm: a named-field group keeps projecting by
		// FIELD, with the temps standing in for the binders.
		NamedFields: group[0].pat.NamedFields,
		FieldNames:  fieldNamesOf(group[0].pat),
		Body:        innerMatch,
	}, nil
}

// rebindExprBody wraps an inner arm's value expression in a BlockExpr that
// first rebinds each named plain payload slot (`var name = __nestK;`),
// leaving the tail as the original value. Returns body unchanged when
// there is nothing to rebind.
func (p *parser) rebindExprBody(pat matchPattern, pos int, tmps []string, body ast.Expr) ast.Expr {
	var binds []ast.Stmt
	for k, name := range pat.Bindings {
		nested := k < len(pat.subPats) && pat.subPats[k] != nil
		if nested || name == "" || name == "_" {
			continue
		}
		binds = append(binds, &ast.Var{P: pat.P, Name: name, Init: &ast.Ident{P: pat.P, Name: tmps[k]}})
	}
	if len(binds) == 0 {
		return body
	}
	return &ast.BlockExpr{P: pat.P, Stmts: binds, Tail: body}
}

func (p *parser) parseBreakContinue(isBreak bool) (ast.Stmt, error) {
	kw := p.advance()
	// Optional loop label: `break outer;` / `continue outer;`. A bare
	// identifier before the `;` names an enclosing labeled loop.
	label := ""
	if t := p.peek(); t.Kind == lexer.Ident {
		label = t.Text
		p.advance()
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	if isBreak {
		return &ast.Break{P: kw.Pos, Label: label}, nil
	}
	return &ast.Continue{P: kw.Pos, Label: label}, nil
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

// parseDefer parses `defer EXPR;` and `errdefer EXPR;`. The IR
// collects every Defer statement in the function body and emits
// the deferred expressions in LIFO order before each return + at
// the end of the function. Conditional defers (registered inside
// a branch that didn't run at runtime) are skipped via per-defer
// "active" flags the IR builder synthesises. An `errdefer` sets
// `OnError`, which restricts its cleanup to the error-exit paths
// (see ast.Defer.OnError).
func (p *parser) parseDefer() (ast.Stmt, error) {
	kw := p.advance()
	// Block-shaped defer: `defer { … }` / `errdefer { … }`. The action is a
	// brace block of statements (matching the self-host parser, which has
	// long accepted this form — see #5153); it parses as a value-position
	// BlockExpr via parseBranchBody, so every downstream ast.Defer consumer
	// handles it unchanged, and — like `if (c) { … }` — it takes no trailing
	// `;`. A block whose last element is a `;`-statement is a void BlockExpr,
	// which is exactly what a side-effecting defer action wants.
	if p.match(lexer.Punct, "{") {
		body, err := p.parseBranchBody()
		if err != nil {
			return nil, err
		}
		return &ast.Defer{P: kw.Pos, Expr: body, OnError: kw.Text == "errdefer"}, nil
	}
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.Defer{P: kw.Pos, Expr: expr, OnError: kw.Text == "errdefer"}, nil
}

// parseAssert parses `assert(cond)` / `assert(cond, msg)` and returns
// the desugared `if (!cond) { eprint(<text>); exit(1); }`. `<text>` is
// the literal `"assertion failed"`, suffixed with `: ` + the message
// expression when a second argument is given (so the message can be any
// runtime string, not just a literal). Building on the existing `!`,
// string `+`, `eprint`, and `exit` primitives keeps `assert` codegen-free
// — it runs identically on every backend and both self-host IR paths. The
// If carries IsAssert so `fern -O` can elide the whole check
// (constfold.ElideAsserts, post-typecheck). #4416.
func (p *parser) parseAssert() (ast.Stmt, error) {
	kw := p.advance() // `assert`
	if _, err := p.expect(lexer.Punct, "("); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	var msg ast.Expr
	if _, ok := p.accept(lexer.Punct, ","); ok {
		msg, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.Punct, ")"); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	pos := kw.Pos
	var text ast.Expr = &ast.StringLit{P: pos, Value: "assertion failed"}
	if msg != nil {
		text = &ast.Binary{P: pos, Op: "+",
			Left:  &ast.StringLit{P: pos, Value: "assertion failed: "},
			Right: msg,
		}
	}
	notCond := &ast.Unary{P: pos, Op: "!", Operand: cond}
	eprintCall := &ast.Call{P: pos, Callee: &ast.Ident{P: pos, Name: "eprint"}, Args: []ast.Expr{text}}
	exitCall := &ast.Call{P: pos, Callee: &ast.Ident{P: pos, Name: "exit"}, Args: []ast.Expr{&ast.NumberLit{P: pos, Value: 1}}}
	then := &ast.Block{P: pos, Stmts: []ast.Stmt{
		&ast.ExprStmt{P: pos, Expr: eprintCall},
		&ast.ExprStmt{P: pos, Expr: exitCall},
	}}
	return &ast.If{P: pos, Cond: notCond, Then: then, IsAssert: true}, nil
}

// parseTodo desugars the `todo;` / `todo("msg");` stub statement to
//
//	loop { eprint("todo[: msg]"); exit(101); }
//
// over the already-supported loop / eprint / exit primitives, so it
// lowers with no dedicated codegen on every backend (mirroring
// parseAssert's approach, #4416). The Loop node carries IsTodo +
// TodoMsg so the formatter re-prints the sugar, and the site is
// recorded on prog.TodoSites for `-check`'s remaining-stub warnings.
// The runtime message deliberately omits the source position — the
// self-host parser must produce a byte-identical message, and the
// `-check` warning carries the position instead.
func (p *parser) parseTodo() (ast.Stmt, error) {
	kw := p.advance() // `todo`
	var msg ast.Expr
	if _, ok := p.accept(lexer.Punct, "("); ok {
		if !p.match(lexer.Punct, ")") {
			m, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			msg = m
		}
		if _, err := p.expect(lexer.Punct, ")"); err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	pos := kw.Pos
	p.todoSites = append(p.todoSites, pos)
	var text ast.Expr = &ast.StringLit{P: pos, Value: "todo: not implemented"}
	if msg != nil {
		text = &ast.Binary{P: pos, Op: "+",
			Left:  &ast.StringLit{P: pos, Value: "todo: "},
			Right: msg,
		}
	}
	eprintCall := &ast.Call{P: pos, Callee: &ast.Ident{P: pos, Name: "eprint"}, Args: []ast.Expr{text}}
	exitCall := &ast.Call{P: pos, Callee: &ast.Ident{P: pos, Name: "exit"}, Args: []ast.Expr{&ast.NumberLit{P: pos, Value: 101}}}
	body := &ast.Block{P: pos, Stmts: []ast.Stmt{
		&ast.ExprStmt{P: pos, Expr: eprintCall},
		&ast.ExprStmt{P: pos, Expr: exitCall},
	}}
	return &ast.Loop{P: pos, Body: body, IsTodo: true, TodoMsg: msg}, nil
}

func (p *parser) parseVar() (ast.Stmt, error) {
	kw := p.advance()
	// Destructuring form: `var (a, b, …) = expr;` / `var Point { x, y } =
	// expr;`. Mirrors the `let` spellings (both route to parseDestructure)
	// but uses the `var` keyword to keep the source surface uniform with
	// regular `var name = expr;` declarations.
	if p.atDestructurePattern() {
		return p.parseDestructure(kw.Pos)
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
	return &ast.Var{P: kw.Pos, Name: discardName(name.Text, kw.Pos, 0), Type: typ, Init: init, WasAnnotated: wasAnnotated}, nil
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
	// parseBlockStmts runs the same loop the callback body would have got
	// had it been written as a real block — including the nested-`use`
	// recursion and the `let … else` rest-capture, both of which have to
	// see this body as their enclosing block.
	body := &ast.Block{P: kw.Pos}
	p.parseBlockStmts(body)
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

// parseLet parses the three `let` statement forms. Tuple and struct
// destructuring (`let (a, b) = e;` / `let Point { x, y } = e;`) are
// irrefutable and produce an *ast.Destructure; everything else is
// `let PAT = <expr> else { <divergent> };`, which desugars to
//
//	match (<expr>) { PAT => { <rest of the enclosing block> },
//	                 _  => { <else> } }
//
// tagged Origin OriginLetElse. Putting the block's remainder in the
// success arm is what keeps the pattern's bindings live for the rest of
// the block without a bespoke node: they are arm bindings, and the arm
// spans everything that follows. The else branch must terminate the
// surrounding control flow — the checker enforces that, here we just
// parse the syntax.
//
// captureRest is false when there is no enclosing block to bind over (a
// braceless branch body), leaving the success arm empty; the bindings are
// unreachable in that position either way.
func (p *parser) parseLet(captureRest bool) (ast.Stmt, error) {
	kw := p.advance() // let
	// `let (a, b, …) = expr;` / `let Point { x, y } = expr;` — the
	// irrefutable forms, which take no `else`: a tuple is statically
	// arity-checked and a struct has one shape, so neither can fail the
	// way an enum destructure can. The refutable forms below all have
	// `(`, `.`, `@` or a literal after the name, never `{`.
	if p.atDestructurePattern() {
		return p.parseDestructure(kw.Pos)
	}
	pats, err := p.parseOrPatterns()
	if err != nil {
		return nil, err
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
	semi, err := p.expect(lexer.Punct, ";")
	if err != nil {
		return nil, err
	}
	rest := &ast.Block{P: semi.Pos}
	if captureRest {
		p.parseBlockStmts(rest)
	}
	return p.buildPatternBindingMatch(kw.Pos, pats, src, rest, elseBlk, ast.OriginLetElse)
}

// atParamPattern reports whether the cursor is on a destructuring
// parameter rather than a plain `name: T` one — an opening `(` for a
// tuple pattern, or `IDENT {` for a struct pattern. Neither shape can be
// a plain parameter (those always have `:` after the name), and `own` is
// followed by an identifier, so the test is unambiguous.
func (p *parser) atParamPattern() bool {
	if p.match(lexer.Punct, "(") {
		return true
	}
	i := 0
	// `w @ <pattern>` names the whole value alongside the destructure.
	if p.peek().Kind == lexer.Ident && p.peekAt(1).Kind == lexer.Punct && p.peekAt(1).Text == "@" {
		i = 2
		if p.peekAt(i).Kind == lexer.Punct && p.peekAt(i).Text == "(" {
			return true
		}
	}
	if p.peekAt(i).Kind != lexer.Ident || p.peekAt(i+1).Kind != lexer.Punct {
		return false
	}
	switch p.peekAt(i + 1).Text {
	case "{":
		return true
	case "(", ".":
		// An enum variant pattern. Not a legal parameter — it can fail to
		// match — but claiming it here routes it to parseParamPattern,
		// which says so, rather than to the plain-param path's bare
		// `expected ":"`.
		return true
	}
	return false
}

// parseParamPattern handles a destructuring parameter — `(a, b): (T, U)`
// or `Point { x, y }: Point`. The head is read with parseMatchPattern, so
// parameters share the one pattern grammar with match / if let /
// let … else; `w @ (a, b): (T, U)` additionally names the whole value.
//
// A parameter binds unconditionally — there is no else branch to run on a
// miss — so only irrefutable patterns are accepted; refutableParamErr
// explains the rejection for the rest.
//
// Desugared at parse time into a synthetic named parameter of the
// annotated type plus an *ast.Destructure the caller prepends to the
// function body, so the checker / interp / IR all reuse the proven
// destructure path (the annotation not matching the pattern, or an arity
// mismatch, surfaces as the usual E024 at the parameter's position). A
// destructured parameter can't carry a default value.
func (p *parser) parseParamPattern() (ast.Param, *ast.Destructure, error) {
	pos := p.peek().Pos
	pat, err := p.parseMatchPattern()
	if err != nil {
		return ast.Param{}, nil, err
	}
	d, err := p.irrefutableDestructure(pos, pat, paramSite)
	if err != nil {
		return ast.Param{}, nil, err
	}
	if _, err := p.expect(lexer.Punct, ":"); err != nil {
		return ast.Param{}, nil, err
	}
	ptype, err := p.parseType()
	if err != nil {
		return ast.Param{}, nil, err
	}
	if p.match(lexer.Punct, "=") {
		return ast.Param{}, nil, p.errorf(pos, "a destructured parameter cannot have a default value")
	}
	// The holder is named by an `@` binding when the pattern carries one,
	// so `w @ Point { x, y }: Point` gets `w` as the whole value. Otherwise
	// a synthetic name uniqued by source position, so two destructured
	// params (even across nested lambdas) can't collide — mirroring the
	// checker's __destruct_<line>_<col> temp. The `__ptuple_` spelling
	// predates struct-pattern params; the self-host parser mints the same
	// name, so the two compilers stay in step.
	holder := pat.atBinding
	if holder == "" {
		holder = fmt.Sprintf("__ptuple_%d_%d", pos.Line, pos.Col)
	}
	d.Init = &ast.Ident{P: pos, Name: holder}
	return ast.Param{Name: holder, NamePos: pos, Type: ptype}, d, nil
}

// irrefutableDestructure converts a pattern into the equivalent
// *ast.Destructure, or reports why the pattern can't stand at a binding
// site with no miss branch — a destructuring parameter, or the
// `let`/`var` destructuring forms. `site` names the site for the
// diagnostic ("parameter" / "destructuring binding").
//
// The two shapes that always match are a tuple of binders and `_`, and a
// struct pattern. Everything else the shared grammar can express can
// fail, and needs a `match` / `if let` / `let … else` instead.
func (p *parser) irrefutableDestructure(pos ast.Position, pat matchPattern, site string) (*ast.Destructure, error) {
	switch {
	case pat.TupleElems != nil:
		names := make([]string, len(pat.TupleElems))
		for i, el := range pat.TupleElems {
			switch {
			case el.IsWildcard:
				names[i] = "_"
			case el.Literal != nil:
				return nil, p.refutableBindErr(pos, "a literal tuple element", site)
			case el.VariantName != "":
				return nil, p.refutableBindErr(pos, "a variant tuple element", site)
			case el.Nested != nil:
				// A nested tuple element always matches, so this is not the
				// refutable-element diagnostic: the limit is that a
				// destructure binds one flat level of a single tuple box.
				return nil, p.errorfCode(pos, "P001",
					"a nested tuple element is not supported in a %s pattern — bind the inner tuple to a name here and destructure it on the next line, or `match` on the value", site)
			case el.Name != "":
				names[i] = el.Name
			default:
				// An element kind this site does not know binds nothing, and
				// falling through with the empty name would make it a silent
				// discard rather than a diagnostic.
				return nil, p.errorfCode(pos, "P001", "unsupported tuple element in a %s pattern", site)
			}
		}
		return &ast.Destructure{P: pos, Names: discardNames(names, pos), Init: nil}, nil
	case pat.NamedFields:
		if len(pat.fieldNames) == 0 {
			return nil, p.errorfCode(pos, "P001", "struct destructure needs at least one field")
		}
		return &ast.Destructure{P: pos, Names: discardNames(pat.Bindings, pos), Fields: pat.fieldNames, StructName: pat.VariantName}, nil
	case pat.VariantName != "":
		return nil, p.refutableBindErr(pos, "an enum variant pattern", site)
	case pat.Literal != nil:
		return nil, p.refutableBindErr(pos, "a literal pattern", site)
	}
	return nil, p.refutableBindErr(pos, "this pattern", site)
}

// discardNames renames each `_` element to its own internal name, so a
// pattern may discard more than one position without the elements
// colliding as a redeclared `_` (#6346 — `_` is a discard at every
// binding site, never a variable). Non-discard names pass through.
func discardNames(names []string, pos ast.Position) []string {
	out := make([]string, len(names))
	for i, nm := range names {
		out[i] = discardName(nm, pos, i)
	}
	return out
}

// Binding sites that irrefutableDestructure serves, named for its
// diagnostics. Only a parameter has somewhere to put an `@` binding (the
// synthetic parameter itself).
const (
	paramSite       = "parameter"
	destructureSite = "destructuring binding"
)

func (p *parser) refutableBindErr(pos ast.Position, what, site string) error {
	return p.errorfCode(pos, "P001",
		"%s can fail to match, so it cannot be a %s pattern — there is no else branch here; destructure with a tuple `(a, b)` or struct `S { x, y }` pattern, or `match` on the value in the body", what, site)
}

// atDestructurePattern reports whether the cursor opens one of the
// irrefutable `let` / `var` destructuring forms — `(` for a tuple
// pattern, `IDENT {` for a struct one. A plain `var name` declaration is
// followed by `:`, `=` or `;`, and every refutable `let` form puts `(`,
// `.`, `@` or a literal after the name, so neither collides.
func (p *parser) atDestructurePattern() bool {
	if p.match(lexer.Punct, "(") {
		return true
	}
	return p.peek().Kind == lexer.Ident &&
		p.peekAt(1).Kind == lexer.Punct && p.peekAt(1).Text == "{"
}

// parseDestructure handles the irrefutable binding statements
// `let (a, b) = expr;` / `let Point { x, y } = expr;` and their `var`
// spellings. The head reads through parseMatchPattern like every other
// binding site; there is no `else` here, so a refutable pattern is a
// parse error rather than a runtime miss. The cursor is on the pattern's
// first token, the keyword having been consumed.
//
// An `@` binding can't arrive here: atDestructurePattern only claims a
// leading `(` or `IDENT {`, and the `IDENT @` form routes to the
// refutable `let … else` path (where the `@` has a match arm to bind in).
func (p *parser) parseDestructure(kwPos ast.Position) (ast.Stmt, error) {
	pat, err := p.parseMatchPattern()
	if err != nil {
		return nil, err
	}
	d, err := p.irrefutableDestructure(kwPos, pat, destructureSite)
	if err != nil {
		return nil, err
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
	d.Init = src
	return d, nil
}

// prependParamDestructures splices the desugared `let (a, b) = <synth>;`
// statements for any tuple-destructuring parameters at the front of a
// function body, in parameter order.
func prependParamDestructures(body *ast.Block, destrs []*ast.Destructure) {
	if body == nil || len(destrs) == 0 {
		return
	}
	stmts := make([]ast.Stmt, 0, len(destrs)+len(body.Stmts))
	for _, d := range destrs {
		stmts = append(stmts, d)
	}
	body.Stmts = append(stmts, body.Stmts...)
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
			case *ast.Ident, *ast.Index, *ast.FieldAccess:
				// fine — same lvalue forms as plain `=`
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
// parseRange handles the half-open / inclusive range EXPRESSION `a..b` /
// `a..=b`, desugaring it to a first-class iterator value: `iter.range(a, b)` /
// `iter.range_incl(a, b)` (core/iter's `Range`, which implements `Iterator`).
// So `iter.sum(0..n)` and `(0..10)` passed to any combinator work via the
// iterator protocol — requiring `import "core/iter"` (the module whose `Range`
// and `range`/`range_incl` constructors this lowers to). It sits just below the
// pipe operator so `0..n` binds looser than arithmetic (`0..n+1` is `0..(n+1)`).
// Note: the `for i in LOW..HIGH` loop keeps its own optimized counted-loop
// desugar — parseForEach reads its bounds with parseLogicOr (below this level),
// so a range there never collapses into an iterator value.
func (p *parser) parseRange() (ast.Expr, error) {
	left, err := p.parseLogicOr()
	if err != nil {
		return nil, err
	}
	if p.match(lexer.Punct, "..") || p.match(lexer.Punct, "..=") {
		tok := p.advance()
		fn := "range"
		if tok.Text == "..=" {
			fn = "range_incl"
		}
		right, err := p.parseLogicOr()
		if err != nil {
			return nil, err
		}
		return &ast.Call{
			P:      tok.Pos,
			Callee: &ast.FieldAccess{P: tok.Pos, Target: &ast.Ident{P: tok.Pos, Name: "iter"}, Field: fn, FieldPos: tok.Pos},
			Args:   []ast.Expr{left, right},
		}, nil
	}
	return left, nil
}

func (p *parser) parsePipe() (ast.Expr, error) {
	left, err := p.parseRange()
	if err != nil {
		return nil, err
	}
	for p.match(lexer.Punct, "|>") {
		pipeTok := p.advance()
		right, err := p.parseRange()
		if err != nil {
			return nil, err
		}
		switch r := right.(type) {
		case *ast.Call:
			// `x |> f(a, _)` — the `_` topic placeholder: the LHS
			// substitutes at the hole instead of being prepended,
			// for the minority of callees that don't take the piped
			// value first. At most one `_`, and only as a DIRECT
			// argument of the piped call — a `_` nested inside a
			// sub-expression is left alone (it stays an ordinary
			// identifier and fails the checker's E001 like any other
			// unknown name). A nested pipe in an arg has already
			// consumed its own `_` by the time this scan runs, so
			// holes compose: `x |> f(y |> g(_), _)` resolves the
			// inner hole to y and the outer one to x.
			hole := -1
			for i, a := range r.Args {
				if id, ok := a.(*ast.Ident); ok && id.Name == "_" {
					if hole >= 0 {
						return nil, p.errorfCode(id.P, "P004", "at most one `_` placeholder in a piped call")
					}
					hole = i
				}
			}
			if hole >= 0 {
				r.Args[hole] = left
				r.PipeHole = hole + 1
			} else {
				// `x |> f(a, b)` — prepend x to f's arg list.
				r.Args = append([]ast.Expr{left}, r.Args...)
			}
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
	return p.parseBinaryLeft(p.parseAdditive, "<<", ">>", "<<|", "<<?", ">>?")
}
func (p *parser) parseAdditive() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseMultiplicative, "+", "-", "+|", "-|", "+?", "-?")
}
func (p *parser) parseMultiplicative() (ast.Expr, error) {
	return p.parseBinaryLeft(p.parseUnary, "*", "/", "%", "*|", "*?", "/?", "%?")
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
		// `as?` is the fallible downcast of a `dyn Trait` value to a
		// concrete type; plain `as` is the numeric cast / ascription.
		// Peek after the `as` keyword: a `?` punct selects the downcast.
		if _, ok := p.accept(lexer.Punct, "?"); ok {
			target, err := p.parseType()
			if err != nil {
				return nil, err
			}
			expr = &ast.DowncastExpr{P: kw.Pos, Inner: expr, Target: target}
			continue
		}
		target, err := p.parseType()
		if err != nil {
			return nil, err
		}
		expr = &ast.CastExpr{P: kw.Pos, Inner: expr, Target: target}
	}
	return expr, nil
}

// parseCallArgs parses a comma-separated argument list up to (but not
// consuming) the closing `)`. An argument of the form `name = expr` (a single
// `=`, not `==`) is a named argument; its name is recorded in the parallel
// `names` slice ("" for positional). `names` is nil when every argument is
// positional (the common case), so all-positional calls are unchanged.
func (p *parser) parseCallArgs() ([]ast.Expr, []string, error) {
	var args []ast.Expr
	var names []string
	anyNamed := false
	if p.match(lexer.Punct, ")") {
		return args, nil, nil
	}
	for {
		name := ""
		// `ident =` (and not `==`) introduces a named argument.
		if p.peek().Kind == lexer.Ident && p.i+1 < len(p.tokens) &&
			p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "=" {
			name = p.peek().Text
			p.advance() // name
			p.advance() // =
			anyNamed = true
		}
		a, err := p.parseExpr()
		if err != nil {
			return nil, nil, err
		}
		args = append(args, a)
		names = append(names, name)
		if !p.moreElems(")") {
			break
		}
	}
	if !anyNamed {
		return args, nil, nil
	}
	return args, names, nil
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
			args, names, err := p.parseCallArgs()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.Punct, ")"); err != nil {
				return nil, err
			}
			expr = &ast.Call{P: open.Pos, Callee: expr, Args: args, ArgNames: names}
			if db, err := p.maybeDesugarBuild(expr.(*ast.Call)); err != nil {
				return nil, err
			} else if db != nil {
				expr = db
			}
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
			if p.peekTypeArgs("(") {
				open := p.advance() // [
				typeArgs, err := p.parseTypeArgList()
				if err != nil {
					return nil, err
				}
				if _, err := p.expect(lexer.Punct, "("); err != nil {
					return nil, err
				}
				args, names, err := p.parseCallArgs()
				if err != nil {
					return nil, err
				}
				if _, err := p.expect(lexer.Punct, ")"); err != nil {
					return nil, err
				}
				expr = &ast.Call{P: open.Pos, Callee: expr, Args: args, ArgNames: names, TypeArgs: typeArgs, TypeArgsWritten: true}
				continue
			}
			// Generic struct-literal type arguments:
			// `Box[i32] { val: 42 }` — the grammar's
			// `StructLit = QualName [ TypeArgs ] '{' … '}'`.
			// Same keyword-plus-terminator disambiguator as the
			// call form, so `arr[i] { … }` stays an index. Gated
			// on !noStructLit exactly like the `Ident { … }` and
			// `mod.Foo { … }` forms: in an `if let` / loop-header
			// source position the `{` opens the body.
			if name, pos, ok := structLitTypeName(expr); ok && !p.noStructLit && p.peekTypeArgs("{") {
				p.advance() // [
				typeArgs, err := p.parseTypeArgList()
				if err != nil {
					return nil, err
				}
				return p.parseStructLit(pos, name, typeArgs)
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
			fname, err := p.expectMemberName()
			if err != nil {
				return nil, err
			}
			// `mod.Foo { … }` is a qualified struct literal — same
			// shape as `Foo { … }` but with the module-qualified
			// type name stitched together as one dotted string.
			// modload rewrites `mod.Foo` to `mod__Foo` before the
			// checker runs, so the StructLit.TypeName carries the
			// dotted form temporarily.
			// Suppressed in `noStructLit` positions (a for-iter / if- /
			// while-condition), where the trailing `{` opens the loop or
			// branch body, not a struct literal — so `for x in b.items {`
			// reads `b.items` as a field access, not `b.items { … }`.
			// Mirrors the bare-`Ident { … }` guard below.
			if id, ok := expr.(*ast.Ident); ok && !p.noStructLit && p.match(lexer.Punct, "{") {
				return p.parseStructLit(id.P, id.Name+"."+fname.Text, nil)
			}
			expr = &ast.FieldAccess{P: dot.Pos, Target: expr, Field: fname.Text, FieldPos: fname.Pos}
		case p.match(lexer.Punct, "::"):
			// `Type::method` / `mod::func` / `Type::CONST` — the
			// path-style namespaced access. Produces the SAME FieldAccess
			// node as the `.` form, so an associated-function call
			// (`Point::origin()`), a module-qualified call (`json::encode()`),
			// and a qualified const all resolve through the existing
			// modload + checker paths. `::` is pure surface syntax; the AST
			// carries no record of which separator was written. See #2700.
			colons := p.advance()
			fname, err := p.expectMemberName()
			if err != nil {
				return nil, err
			}
			// `mod::Foo { … }` is a path-qualified struct literal, mirroring
			// the `mod.Foo { … }` form (suppressed in noStructLit positions).
			if id, ok := expr.(*ast.Ident); ok && !p.noStructLit && p.match(lexer.Punct, "{") {
				return p.parseStructLit(id.P, id.Name+"."+fname.Text, nil)
			}
			expr = &ast.FieldAccess{P: colons.Pos, Target: expr, Field: fname.Text, FieldPos: fname.Pos, PathSep: true}
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

// structLitTypeName recovers the QualName a `Name[TypeArgs] { … }`
// literal constructs, from the expression parsed before the `[`.
// A bare `Ident` is the unqualified form; a one-level `FieldAccess`
// off an Ident is the `mod.Foo` / `mod::Foo` form, whose dotted name
// modload rewrites to `mod__Foo`. Anything deeper is not a type name.
func structLitTypeName(e ast.Expr) (string, ast.Position, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name, x.P, true
	case *ast.FieldAccess:
		if id, ok := x.Target.(*ast.Ident); ok && !isNumericSelector(x.Field) {
			return id.Name + "." + x.Field, id.P, true
		}
	}
	return "", ast.Position{}, false
}

// isNumericSelector reports whether a FieldAccess selector is a tuple
// index (`pair.0`) rather than a field name.
func isNumericSelector(field string) bool {
	if field == "" {
		return false
	}
	for _, r := range field {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseStructLit parses the `{ field: value, ... }` part of a struct
// literal, having already consumed the type-name identifier and any
// `[TypeArgs]` after it (typeArgs is nil when none were written).
// Trailing commas are accepted; the checker enforces field-set
// completeness against the struct declaration.
func (p *parser) parseStructLit(pos ast.Position, typeName string, typeArgs []ast.Type) (ast.Expr, error) {
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, err
	}
	var fields []ast.FieldInit
	// Struct-update literal: a leading `...base` spread copies the
	// un-named fields from `base`, the rest are overrides. Must be the
	// first element (one base). `Foo { ...base }` with no overrides is
	// a legal pure copy. See docs/IMMUTABILITY-MIGRATION-PLAN.md.
	var base ast.Expr
	if _, ok := p.accept(lexer.Punct, "..."); ok {
		b, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		base = b
		if _, ok := p.accept(lexer.Punct, ","); !ok {
			// No overrides — expect the closing brace (pure copy).
			if _, err := p.expect(lexer.Punct, "}"); err != nil {
				return nil, err
			}
			return &ast.StructLit{P: pos, TypeName: typeName, Base: base, TypeArgs: typeArgs, TypeArgsWritten: typeArgs != nil}, nil
		}
	}
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
	return &ast.StructLit{P: pos, TypeName: typeName, Fields: fields, Base: base, TypeArgs: typeArgs, TypeArgsWritten: typeArgs != nil}, nil
}

// parseMapLit parses `Map { key: value, key: value, ... }`. Both
// keys and values are arbitrary expressions; trailing commas are
// allowed. Empty `Map {}` is also valid and produces an empty
// map. Lowering happens at IR-build time — no runtime difference
// from `var m = map_new(N); m.set(k, v); ...`.
// maybeDesugarArrayBuild lowers `Array.build(function(b: ArrayBuilder[T]):
// void { BODY })` — the scoped linear builder (docs/ARRAY-BUILDER-PLAN.md)
// — into an immediately-invoked function that builds a unique local array
// and returns it:
//
//	(function(): T[] {
//	    var b: T[] = [];
//	    BODY'                 // each statement `b.append(x);`  → `b = b.append(x);`
//	                          //              `b.with(i, x);`  → `b = b.with(i, x);`
//	    return b;
//	})()
//
// Because `b` is a fresh non-escaping local, its buffer stays rc=1 and
// every append/with takes the existing in-place fast path; `b = b.append(x)`
// is an assignment, so E055 (discarded value-returning result) does not
// fire. ArrayBuilder[T] is pure surface syntax consumed here — it never
// reaches the checker or IR, so every backend (incl. self-host) gets the
// builder with no further changes.
//
// Returns nil when `call` is not an `Array.build(...)` call (leave it
// alone); a parse error when it is but is malformed.
func (p *parser) maybeDesugarArrayBuild(call *ast.Call) (ast.Expr, error) {
	fa, ok := call.Callee.(*ast.FieldAccess)
	if !ok || fa.Field != "build" {
		return nil, nil
	}
	recv, ok := fa.Target.(*ast.Ident)
	if !ok || recv.Name != "Array" {
		return nil, nil
	}
	// It's `Array.build(...)` — from here on, a malformed call is an error
	// (otherwise it would fall through to a confusing "undefined Array").
	if len(call.Args) != 1 {
		return nil, p.errorf(call.P, "Array.build expects a single function(b: ArrayBuilder[T]) argument")
	}
	lam, ok := call.Args[0].(*ast.Lambda)
	if !ok {
		return nil, p.errorf(call.P, "Array.build's argument must be a function(b: ArrayBuilder[T]) literal")
	}
	if len(lam.Params) != 1 {
		return nil, p.errorf(lam.P, "Array.build's function must take exactly one parameter (the builder)")
	}
	bname := lam.Params[0].Name
	elem := arrayBuilderElem(lam.Params[0].Type)
	if elem == nil {
		return nil, p.errorf(lam.P, "Array.build's parameter must be typed ArrayBuilder[T]")
	}
	arrTy := ast.ArrayType{Elem: elem}
	pos := call.P
	stmts := make([]ast.Stmt, 0, len(lam.Body.Stmts)+2)
	stmts = append(stmts, &ast.Var{
		P: pos, Name: bname, Type: arrTy, WasAnnotated: true,
		Init: &ast.ArrayLit{P: pos, ElemType: elem},
	})
	for _, s := range lam.Body.Stmts {
		stmts = append(stmts, rewriteBuilderStmt(s, bname))
	}
	stmts = append(stmts, &ast.Return{P: pos, Value: &ast.Ident{P: pos, Name: bname}})
	iife := &ast.Lambda{P: pos, ReturnType: arrTy, Body: &ast.Block{P: pos, Stmts: stmts}}
	return &ast.Call{P: pos, Callee: iife}, nil
}

// arrayBuilderElem extracts T from an `ArrayBuilder[T]` type annotation
// (the parser wraps `Name[...]` as EnumType; StructType is handled
// defensively). Returns nil if the type isn't ArrayBuilder[_].
func arrayBuilderElem(t ast.Type) ast.Type {
	switch v := t.(type) {
	case ast.EnumType:
		if v.Name == "ArrayBuilder" && len(v.Args) == 1 {
			return v.Args[0]
		}
	case ast.StructType:
		if v.Name == "ArrayBuilder" && len(v.Args) == 1 {
			return v.Args[0]
		}
	}
	return nil
}

// rewriteBuilderStmt retargets statement-position `b.append(...)` /
// `b.with(...)` calls (where `b` is the builder) into reassignments
// `b = b.append(...)`, recursing into every statement container so a
// builder mutated inside a loop / branch is handled. Reads (`b.len()`,
// `b` as a value) and all other statements pass through untouched. A
// builder call left unrewritten (e.g. inside a nested closure, which the
// walker deliberately does not descend into) stays a discarded
// value-returning result and trips E055 — a loud error, never a silent
// lost write.
func rewriteBuilderStmt(s ast.Stmt, b string) ast.Stmt {
	switch n := s.(type) {
	case *ast.ExprStmt:
		if c, ok := n.Expr.(*ast.Call); ok && isBuilderMutation(c, b) {
			return &ast.ExprStmt{P: n.P, Expr: &ast.Assign{P: n.P, Target: &ast.Ident{P: n.P, Name: b}, Value: c}}
		}
		return n
	case *ast.Block:
		n.Stmts = rewriteBuilderStmts(n.Stmts, b)
		return n
	case *ast.If:
		n.Then = rewriteBuilderStmt(n.Then, b)
		if n.Else != nil {
			n.Else = rewriteBuilderStmt(n.Else, b)
		}
		return n
	case *ast.While:
		n.Body = rewriteBuilderStmt(n.Body, b)
		return n
	case *ast.Loop:
		n.Body = rewriteBuilderStmt(n.Body, b)
		return n
	case *ast.For:
		n.Body = rewriteBuilderStmt(n.Body, b)
		return n
	case *ast.ForEach:
		n.Body = rewriteBuilderStmt(n.Body, b)
		return n
	case *ast.Match:
		for _, arm := range n.Arms {
			arm.Body.Stmts = rewriteBuilderStmts(arm.Body.Stmts, b)
		}
		return n
	}
	return s
}

func rewriteBuilderStmts(stmts []ast.Stmt, b string) []ast.Stmt {
	for i, s := range stmts {
		stmts[i] = rewriteBuilderStmt(s, b)
	}
	return stmts
}

// isBuilderMutation reports whether `c` is `b.append(...)` or `b.with(...)`
// — an in-place builder mutation that becomes a reassignment.
func isBuilderMutation(c *ast.Call, b string) bool {
	fa, ok := c.Callee.(*ast.FieldAccess)
	if !ok || (fa.Field != "append" && fa.Field != "with" && fa.Field != "insert") {
		return false
	}
	id, ok := fa.Target.(*ast.Ident)
	return ok && id.Name == b
}

// maybeDesugarBuild dispatches the builder desugars: Array.build (above) and
// Map.build. Returns nil when `call` is neither.
func (p *parser) maybeDesugarBuild(call *ast.Call) (ast.Expr, error) {
	if e, err := p.maybeDesugarArrayBuild(call); err != nil || e != nil {
		return e, err
	}
	return p.maybeDesugarMapBuild(call)
}

// maybeDesugarMapBuild lowers `Map.build(function(b: MapBuilder[K, V]):
// void { BODY })` into a unique-local IIFE — the map sibling of
// maybeDesugarArrayBuild (docs/ARRAY-BUILDER-PLAN.md):
//
//	(function(): Map[K, V] {
//	    var b: Map[K, V] = map_new(8);
//	    BODY'                 // `b.insert(k, v);` → `b = b.insert(k, v);`
//	    return b;
//	})()
//
// `b` is a fresh non-escaping map, so every insert is the in-place fast
// path and the reassignment form keeps E055 from firing. MapBuilder[K, V]
// is surface-only.
func (p *parser) maybeDesugarMapBuild(call *ast.Call) (ast.Expr, error) {
	fa, ok := call.Callee.(*ast.FieldAccess)
	if !ok || fa.Field != "build" {
		return nil, nil
	}
	recv, ok := fa.Target.(*ast.Ident)
	if !ok || recv.Name != "Map" {
		return nil, nil
	}
	if len(call.Args) != 1 {
		return nil, p.errorf(call.P, "Map.build expects a single function(b: MapBuilder[K, V]) argument")
	}
	lam, ok := call.Args[0].(*ast.Lambda)
	if !ok {
		return nil, p.errorf(call.P, "Map.build's argument must be a function(b: MapBuilder[K, V]) literal")
	}
	if len(lam.Params) != 1 {
		return nil, p.errorf(lam.P, "Map.build's function must take exactly one parameter (the builder)")
	}
	bname := lam.Params[0].Name
	kv := mapBuilderArgs(lam.Params[0].Type)
	if kv == nil {
		return nil, p.errorf(lam.P, "Map.build's parameter must be typed MapBuilder[K, V]")
	}
	mapTy := ast.StructType{Name: "Map", Args: kv}
	pos := call.P
	stmts := make([]ast.Stmt, 0, len(lam.Body.Stmts)+2)
	stmts = append(stmts, &ast.Var{
		P: pos, Name: bname, Type: mapTy, WasAnnotated: true,
		Init: &ast.Call{P: pos, Callee: &ast.Ident{P: pos, Name: "map_new"}, Args: []ast.Expr{&ast.NumberLit{P: pos, Value: 8}}},
	})
	for _, s := range lam.Body.Stmts {
		stmts = append(stmts, rewriteBuilderStmt(s, bname))
	}
	stmts = append(stmts, &ast.Return{P: pos, Value: &ast.Ident{P: pos, Name: bname}})
	iife := &ast.Lambda{P: pos, ReturnType: mapTy, Body: &ast.Block{P: pos, Stmts: stmts}}
	return &ast.Call{P: pos, Callee: iife}, nil
}

// mapBuilderArgs extracts [K, V] from a `MapBuilder[K, V]` type annotation
// (parser wraps `Name[...]` as EnumType; StructType handled defensively).
// Returns nil if the type isn't MapBuilder[_, _].
func mapBuilderArgs(t ast.Type) []ast.Type {
	switch v := t.(type) {
	case ast.EnumType:
		if v.Name == "MapBuilder" && len(v.Args) == 2 {
			return v.Args
		}
	case ast.StructType:
		if v.Name == "MapBuilder" && len(v.Args) == 2 {
			return v.Args
		}
	}
	return nil
}

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

// keywordModuleQualifier is the set of type-name keywords that are also
// stdlib module basenames (`std/string`, `std/i32`, …). In expression
// position followed by `.`, parsePrimary treats them as a module
// qualifier so keyword-named modules' free functions are reachable.
var keywordModuleQualifier = map[string]bool{
	"string": true,
	"i32":    true,
	"i64":    true,
	"u32":    true,
	"u64":    true,
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
				p.errors = append(p.errors, p.errorfCode(t.Pos, "P002", "invalid hex literal %q: %v", t.Text, err))
			}
			n = v
		} else {
			// Decimal literal. Use strconv (like the hex path) so an
			// out-of-range literal is reported instead of silently
			// wrapping two's-complement — the old hand-rolled
			// `n = n*10 + digit` overflowed without any diagnostic and
			// the wrapped value could slip past the checker's range
			// check. See docs/ADVERSARIAL-REVIEW-2026-06.md (F3).
			v, err := strconv.ParseInt(t.Text, 10, 64)
			if err != nil {
				// A u64 literal can exceed i64 max yet still be valid;
				// retry as unsigned and keep the bit pattern. The
				// checker enforces the per-type range from the suffix /
				// context.
				if uv, uerr := strconv.ParseUint(t.Text, 10, 64); uerr == nil {
					v = int64(uv)
				} else {
					p.errors = append(p.errors, p.errorfCode(t.Pos, "P002", "invalid integer literal %q: %v", t.Text, err))
				}
			}
			n = v
		}
		lit := &ast.NumberLit{P: t.Pos, Value: n}
		if len(t.Text) > 2 && t.Text[0] == '0' && (t.Text[1] == 'x' || t.Text[1] == 'X') {
			lit.Raw = t.Text
		}
		// Typed suffix (`42i64`, `7u8`): stamp Width + IsUnsigned
		// at parse time so the checker sees a non-polymorphic
		// type immediately, bypassing settle-from-context flow.
		switch t.Suffix {
		case "":
			// no suffix — polymorphic
		case "i32":
			lit.Width = 32
		case "i64":
			lit.Width = 64
		case "u8":
			lit.Width = 8
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
		lit := &ast.FloatLit{P: t.Pos, Value: f, Raw: t.Text}
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
	case lexer.Char, lexer.Byte:
		// The lexer has already decoded the escape and rejected every
		// spelling that is not exactly one scalar / byte, so there is
		// nothing left to validate here. Text is the source spelling,
		// kept so `-fmt` re-emits what the author wrote.
		p.advance()
		return &ast.CharLit{P: t.Pos, Value: int64(t.Scalar), Raw: t.Text, IsByte: t.Kind == lexer.Byte}, nil
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
				expr, err := parseExprFromText(fp.Expr, fp.Pos)
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
		// A builtin type-name keyword in expression position followed
		// by `.` is a *module* qualifier, not a type. The std modules
		// `std/string` / `std/i32` / `std/i64` / `std/u32` / `std/u64`
		// have basenames that collide with type keywords, so a free
		// function like `string.repeat_char(...)` would otherwise be a
		// parse error. Treat the keyword as a bare module-name Ident
		// and let postfix `.field` / call parsing + modload's `mod.Foo`
		// rewrite resolve it like any other qualified reference.
		if keywordModuleQualifier[t.Text] &&
			p.i+1 < len(p.tokens) &&
			p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "." {
			p.advance()
			return &ast.Ident{P: t.Pos, Name: t.Text}, nil
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
			return p.parseStructLit(t.Pos, t.Text, nil)
		}
		return &ast.Ident{P: t.Pos, Name: t.Text}, nil
	case lexer.Punct:
		switch t.Text {
		case "(":
			// Arrow lambda `(params): R => expr` / `(params) => expr` — a
			// concise anonymous function whose body is a single expression
			// (desugared to `function (params): R { return expr; }`). Checked
			// before the grouping/tuple parse: an arrow lambda's parens hold a
			// parameter list (`IDENT : TYPE`, or empty), which a grouping
			// (`(e)`) or tuple (`(e1, e2)`) never does. See #2701.
			if p.looksLikeArrowLambda() {
				return p.parseArrowLambda()
			}
			// `(e)` is grouping; `(e1, e2, ...)` (>=2 elements) is
			// a tuple literal. Single-element "tuples" don't exist
			// as a syntactic form — `(e)` always groups.
			open := p.advance()
			// `()` — the unit value. looksLikeArrowLambda already
			// claimed `() => e`, so an empty pair here is the literal.
			if _, ok := p.accept(lexer.Punct, ")"); ok {
				return &ast.UnitLit{P: open.Pos}, nil
			}
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
			for !p.match(lexer.Punct, "]") {
				e, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				elems = append(elems, e)
				if !p.moreElems("]") {
					break
				}
			}
			if _, err := p.expect(lexer.Punct, "]"); err != nil {
				return nil, err
			}
			return &ast.ArrayLit{P: open.Pos, Elems: elems}, nil
		case "{":
			// General value-position block-expression (#4521): a bare
			// `{ stmts; tail }` where a value is expected — the RHS of
			// `var x = { … }`, a call argument, an array/struct-field
			// value, etc. Reuses parseBranchBody (the same machinery the
			// if/match branch form uses), so the contents, the E061
			// value-less check, and the single-expr `{ e }` passthrough all
			// behave identically to a branch block.
			//
			// Gated on !noStructLit exactly like the `Ident { … }`
			// struct-literal case above: in a loop/if/while HEADER (`for x
			// in expr {`, `while cond {`) the trailing `{` opens the body,
			// not a block-expression, so the header's expression parse
			// (which sets noStructLit) must not consume it here. Fern has
			// no anonymous/record struct literals, so a bare `{` in a
			// value position is otherwise unambiguous.
			if !p.noStructLit {
				return p.parseBranchBody()
			}
		}
	}
	return nil, p.errorfCode(t.Pos, "P001", "unexpected token %q", t.Text)
}
