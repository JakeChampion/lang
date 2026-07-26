// Package lexer turns source text into a slice of tokens.
//
// The lexer is a single linear pass over the input rune-by-rune. It
// recognises a handful of keywords, integer literals, identifiers, and
// punctuation. Whitespace and `//` line-comments are skipped.
package lexer

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/jakechampion/lang/internal/ast"
)

type Kind int

const (
	EOF Kind = iota
	Number
	Float
	Ident
	Punct
	Keyword
	String
	FString
)

func (k Kind) String() string {
	switch k {
	case EOF:
		return "EOF"
	case Number:
		return "Number"
	case Float:
		return "Float"
	case Ident:
		return "Ident"
	case Punct:
		return "Punct"
	case Keyword:
		return "Keyword"
	case String:
		return "String"
	case FString:
		return "FString"
	}
	return "?"
}

// FStringPart is one piece of an f-string surfaced by the lexer.
// Either Lit (literal segment, escapes already processed) or
// Expr (raw expression text the parser sub-parses) is set.
type FStringPart struct {
	Lit  string
	Expr string
}

type Token struct {
	Kind Kind
	Text string
	Pos  ast.Position
	// FParts is set when Kind == FString — the lexer has already
	// split the body into alternating literal / interpolant pieces
	// so the parser can sub-parse each Expr part without re-lexing
	// the body.
	FParts []FStringPart
	// Suffix is the typed-literal suffix when Kind == Number or
	// Float (e.g. "i64", "u8", "f64"). Empty when the literal is
	// untyped / polymorphic. The parser uses Suffix to bypass
	// the polymorphic-numeric flow and stamp the AST node with
	// a fixed type directly.
	Suffix string
}

func (t Token) String() string {
	return fmt.Sprintf("%s(%q) at %s", t.Kind, t.Text, t.Pos)
}

var keywords = map[string]bool{
	"function": true,
	"var":      true,
	"let":      true,
	"use":      true,
	"if":       true,
	"else":     true,
	"while":    true,
	"for":      true,
	"loop":     true,
	"break":    true,
	"continue": true,
	"return":   true,
	// Structured-concurrency surface (docs/ASYNC-IMPLEMENTATION-PLAN.md
	// Phase 3): `concurrent { var a = spawn f(...); … await a … }` fans
	// out tasks; the parser desugars the block onto the std/task runtime.
	// `race { spawn …; spawn …; }` — race spawned tasks, first-to-finish wins;
	// an expression yielding (winnerIndex, result). Desugars onto std/task.select.
	// (Named `race`, not `select`, so the runtime function `task.select` — already
	// an identifier — keeps working.)
	"true":    true,
	"false":   true,
	"boolean": true,
	"void":    true,
	"string":  true,
	// Sized numeric type names. The legacy `number` alias for
	// `i32` was removed in the legacy-cleanup pass. `float` is
	// the width-unqualified alias for f64 (#5363), handled
	// contextually in the parser's type position — never a
	// keyword, so `float.pi()` module calls keep working.
	// isize/i8/i16/u16 were retired (issue #4408): isize had zero
	// uses, and i8/i16/u16 carried a full per-stride backend cost
	// for a handful of call sites — i32/u32 cover them now.
	"i32": true,
	"i64": true,
	"u8":  true,
	"u32": true,
	"u64": true,
	// usize is the target-aware native-pointer-width unsigned
	// integer: 4 bytes on wasm32, 8 bytes on arm64 / x86-64.
	// Backed by `ast.NumberType{Width: WidthPtr}` so the
	// checker and codegen route it through the same machinery
	// that already sizes OpStore / OpLoad of heap pointer values.
	"usize":   true,
	"f32":     true,
	"f64":     true,
	"as":      true,
	"default": true,
	"struct":  true,
	"import":  true,
	"pub":     true,
	"const":   true,
	"enum":    true,
	// `type X = A | B | C;` declares a union of struct types —
	// see UnionDecl in internal/ast. The checker desugars these
	// to synthetic enums (`enum X { A(A), B(B), C(C) }`) so the
	// rest of the pipeline doesn't need to know about them.
	"type":  true,
	"match": true,
	"when":  true,
	"defer": true,
	// `errdefer EXPR;` — like `defer`, but runs only on an error
	// exit (`?` propagation or a `return` of None / Err). See
	// ast.Defer.OnError.
	"errdefer": true,
	// `trait` declares a named set of method signatures; `impl
	// Trait for Type { … }` provides bodies. See docs/TRAITS.md.
	// `Self` stays a contextual type name (handled in the parser),
	// and `self` is an ordinary identifier — neither is reserved.
	"trait": true,
	"impl":  true,
	// `dyn` introduces a runtime trait-object type (`dyn Shape`) in
	// type position. Reserved as a keyword for parser simplicity; it
	// was not previously used as an identifier anywhere in the stdlib
	// or examples. See docs/DYN-TRAITS.md.
	"dyn": true,
}

// Keywords returns every reserved word the lexer recognises, in
// sorted order. Used by the LSP package to seed completion lists;
// putting the accessor here keeps the reserved-word set in one
// place so the lexer + completion never drift.
func Keywords() []string {
	out := make([]string, 0, len(keywords))
	for k := range keywords {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Multi-character punctuators, longest first. The 3-char compound
// shifts (`<<=`, `>>=`) sit before the 2-char shifts so the
// longest-prefix rule picks the right one.
var multiPunct = []string{
	"<<=", ">>=", "<<|", "...",
	"..=", "..",
	"==", "!=", "<=", ">=", "&&", "||", "<<", ">>", "=>", "|>",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=",
	// Saturating arithmetic (#5542) — clamp to the operand type's
	// [MIN, MAX] instead of wrapping. Listed after the compound
	// assignments so `+=` still wins over a `+`-prefixed match.
	// `<<|` sits up with the 3-char punctuators so it beats `<<`.
	"+|", "-|", "*|",
	"::",
}

type Error struct {
	Pos  ast.Position
	Msg  string
	Path string // source file path; populated by modload, empty otherwise
}

func (e *Error) Error() string          { return fmt.Sprintf("lex error at %s: %s", e.Pos, e.Msg) }
func (e *Error) Position() ast.Position { return e.Pos }
func (e *Error) File() string           { return e.Path }
func (e *Error) setFile(p string)       { e.Path = p }

// validNumericSuffix is the closed list of typed-literal suffixes
// the lexer recognises. The same set is later honoured by the
// parser when stamping the AST node's concrete type.
func validNumericSuffix(s string) bool {
	switch s {
	case "i32", "i64",
		"u8", "u32", "u64",
		"f32", "f64":
		return true
	}
	return false
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// hexVal returns the numeric value of a hex digit (0 for non-digits).
func hexVal(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10
	case r >= 'A' && r <= 'F':
		return int(r-'A') + 10
	}
	return 0
}

// Tokenize turns src into a slice of tokens terminated by an EOF
// token, plus the `//` line comments encountered along the way (in
// source order). Comments are returned separately rather than as a
// token kind so the parser can stay grammar-only — formatter / LSP
// / hover-doc consumers walk the comment list explicitly.
func Tokenize(src string) ([]Token, []ast.Comment, error) {
	l := &lexer{src: src, line: 1, col: 1}
	var out []Token
	for {
		tok, err := l.next()
		if err != nil {
			return nil, nil, err
		}
		out = append(out, tok)
		if tok.Kind == EOF {
			return out, l.comments, nil
		}
		l.afterDot = tok.Kind == Punct && tok.Text == "."
	}
}

type lexer struct {
	src       string
	i         int
	line, col int
	// comments accumulates every `//` line comment in source
	// order. Populated by skipTrivia; exposed via the second
	// Tokenize return value.
	comments []ast.Comment
	// afterDot is true when the previously-emitted non-trivia
	// token was a `.` punctuator. Used by the number-literal
	// branch to suppress the `.<digit>` → float upgrade so
	// chained tuple-field access like `t.1.0` lexes as
	// `t . 1 . 0` (three Idents/Numbers + two Dots) rather
	// than `t . 1.0` (an Ident + Dot + Float).
	afterDot bool
}

func (l *lexer) peek() (rune, bool) {
	if l.i >= len(l.src) {
		return 0, false
	}
	return rune(l.src[l.i]), true
}

func (l *lexer) advance() rune {
	r := rune(l.src[l.i])
	l.i++
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

func (l *lexer) pos() ast.Position { return ast.Position{Line: l.line, Col: l.col} }

func (l *lexer) skipTrivia() {
	for l.i < len(l.src) {
		r := rune(l.src[l.i])
		switch {
		case unicode.IsSpace(r):
			l.advance()
		case r == '/' && l.i+1 < len(l.src) && l.src[l.i+1] == '/':
			// Capture the comment with its starting position. Skip
			// the leading `//` so the recorded text is just the
			// human-readable body. Newline at end-of-comment is
			// left for the next skipTrivia iteration to consume.
			start := l.pos()
			l.advance() // first /
			l.advance() // second /
			textStart := l.i
			for l.i < len(l.src) && l.src[l.i] != '\n' {
				l.advance()
			}
			l.comments = append(l.comments, ast.Comment{
				Pos:  start,
				Text: l.src[textStart:l.i],
			})
		default:
			return
		}
	}
}

func (l *lexer) next() (Token, error) {
	l.skipTrivia()
	if l.i >= len(l.src) {
		return Token{Kind: EOF, Pos: l.pos()}, nil
	}

	start := l.pos()
	r, _ := l.peek()

	// f-string: `f"..."` produces a single FString token whose
	// FParts hold pre-split literal / interpolant pieces. The
	// parser sub-parses each interpolant Expr text, the IR lowers
	// the AST node to a `+` chain, and the formatter rebuilds the
	// `f"..."` form on round-trip. `{{` / `}}` escape literal
	// braces. Detected before the generic identifier path so `f`
	// doesn't get scooped up as an identifier.
	if r == 'f' && l.i+1 < len(l.src) && l.src[l.i+1] == '"' {
		l.advance() // consume `f`
		parts, err := l.scanFString(start)
		if err != nil {
			return Token{}, err
		}
		return Token{Kind: FString, Pos: start, FParts: parts}, nil
	}

	// Identifier or keyword.
	if r == '_' || unicode.IsLetter(r) {
		begin := l.i
		for l.i < len(l.src) {
			c := rune(l.src[l.i])
			if c != '_' && !unicode.IsLetter(c) && !unicode.IsDigit(c) {
				break
			}
			l.advance()
		}
		text := l.src[begin:l.i]
		kind := Ident
		if keywords[text] {
			kind = Keyword
		}
		return Token{Kind: kind, Text: text, Pos: start}, nil
	}

	// Number / float literal. A trailing `.<digit>+` upgrades the
	// integer match to a float; `1.` (no fractional digits) and `.5`
	// (no leading integer digits) aren't accepted, keeping the
	// lexer unambiguous about Index-style `a[0].x` style suffixes.
	if unicode.IsDigit(r) {
		begin := l.i
		// `0x` / `0X` hex integer literal: consume hex digits and skip
		// the float (fractional / exponent) upgrades below.
		isHex := false
		if l.src[l.i] == '0' && l.i+1 < len(l.src) && (l.src[l.i+1] == 'x' || l.src[l.i+1] == 'X') {
			isHex = true
			l.advance() // '0'
			l.advance() // 'x' / 'X'
			hexDigits := l.i
			for l.i < len(l.src) && isHexDigit(rune(l.src[l.i])) {
				l.advance()
			}
			if l.i == hexDigits {
				return Token{}, &Error{Pos: start, Msg: "hex literal needs at least one digit after 0x"}
			}
		} else {
			for l.i < len(l.src) && unicode.IsDigit(rune(l.src[l.i])) {
				l.advance()
			}
		}
		isFloat := false
		// When the previous emitted token was a `.`, we're parsing
		// the index part of a chained field-access like `t.1.0`.
		// In that context, a trailing `.<digit>` is the NEXT
		// field-access, not a fractional part — suppress the float
		// upgrade so the second `.0` lands as `.` `0` instead of
		// being eaten as a continuation of `1`.
		if !isHex && !l.afterDot && l.i+1 < len(l.src) && l.src[l.i] == '.' && unicode.IsDigit(rune(l.src[l.i+1])) {
			isFloat = true
			l.advance() // '.'
			for l.i < len(l.src) && unicode.IsDigit(rune(l.src[l.i])) {
				l.advance()
			}
		}
		// Scientific-notation exponent: `[eE][+-]?[0-9]+`, valid on
		// both an integer base (`1e3`) and a fractional one
		// (`1.5e-2`). Only consumed when at least one exponent digit
		// follows the optional sign — a bare `1e` / `1efoo` leaves
		// the `e` for the next token. Suppressed right after a `.`
		// so a chained tuple index like `t.1e3` keeps `1` as the
		// selector (same rationale as the fractional-dot guard).
		if !isHex && !l.afterDot && l.i < len(l.src) && (l.src[l.i] == 'e' || l.src[l.i] == 'E') {
			j := l.i + 1
			if j < len(l.src) && (l.src[j] == '+' || l.src[j] == '-') {
				j++
			}
			if j < len(l.src) && unicode.IsDigit(rune(l.src[j])) {
				isFloat = true
				l.advance() // 'e' / 'E'
				if l.src[l.i] == '+' || l.src[l.i] == '-' {
					l.advance()
				}
				for l.i < len(l.src) && unicode.IsDigit(rune(l.src[l.i])) {
					l.advance()
				}
			}
		}
		text := l.src[begin:l.i]
		// Optional typed suffix: i32/i64/u8/u32/u64/f32/f64.
		// Recognised greedily — the suffix character set is a
		// closed list so misspellings like `42i33` fail later
		// rather than partially consuming. A float-literal text
		// (`1.5`) with an integer suffix (`i32`) is a parse-time
		// error; ditto an integer literal with a non-int suffix
		// like `42x9` — both surface as "unknown numeric suffix".
		suffix := ""
		if l.i < len(l.src) {
			ch := l.src[l.i]
			if ch == 'i' || ch == 'u' || ch == 'f' {
				sBegin := l.i
				l.advance()
				for l.i < len(l.src) && unicode.IsDigit(rune(l.src[l.i])) {
					l.advance()
				}
				suffix = l.src[sBegin:l.i]
				if !validNumericSuffix(suffix) {
					return Token{}, &Error{Pos: start, Msg: fmt.Sprintf("unknown numeric suffix %q on %q", suffix, text)}
				}
				if isFloat && suffix[0] != 'f' {
					return Token{}, &Error{Pos: start, Msg: fmt.Sprintf("integer suffix %q on float literal %q", suffix, text)}
				}
			}
		}
		// `42f32` / `42f64` — integer text with float suffix
		// promotes the token to Float so the parser uses the
		// f-literal construction path.
		if !isFloat && suffix != "" && suffix[0] == 'f' {
			isFloat = true
		}
		kind := Number
		if isFloat {
			kind = Float
		}
		return Token{Kind: kind, Text: text, Pos: start, Suffix: suffix}, nil
	}

	// String literal: "..." with C-style escapes \\, \", \n, \t, \r, \0.
	if r == '"' {
		l.advance() // opening "
		var b strings.Builder
		for l.i < len(l.src) && rune(l.src[l.i]) != '"' {
			c := rune(l.src[l.i])
			if c == '\n' {
				return Token{}, &Error{Pos: start, Msg: "newline inside string literal"}
			}
			if c == '\\' {
				l.advance()
				if l.i >= len(l.src) {
					return Token{}, &Error{Pos: start, Msg: "unterminated string literal"}
				}
				esc := rune(l.src[l.i])
				l.advance()
				switch esc {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case 'r':
					b.WriteByte('\r')
				case '0':
					b.WriteByte(0)
				case '"':
					b.WriteByte('"')
				case '\\':
					b.WriteByte('\\')
				case 'x':
					if l.i+1 >= len(l.src) || !isHexDigit(rune(l.src[l.i])) || !isHexDigit(rune(l.src[l.i+1])) {
						return Token{}, &Error{Pos: start, Msg: "\\x escape needs two hex digits"}
					}
					b.WriteByte(byte(hexVal(rune(l.src[l.i]))<<4 | hexVal(rune(l.src[l.i+1]))))
					l.advance()
					l.advance()
				default:
					return Token{}, &Error{Pos: start, Msg: fmt.Sprintf("unknown escape \\%c", esc)}
				}
				continue
			}
			// Write the raw source byte, not WriteRune(c): c is
			// `rune(l.src[l.i])`, a single byte, so for a multi-byte
			// UTF-8 character WriteRune would re-encode each lead/
			// continuation byte as its own code point (e.g. the 3
			// bytes of `∃` become three mojibake runes). Strings are
			// UTF-8 byte arrays here, so preserving the source bytes
			// verbatim is both correct and what round-trips.
			b.WriteByte(l.src[l.i])
			l.advance()
		}
		if l.i >= len(l.src) {
			return Token{}, &Error{Pos: start, Msg: "unterminated string literal"}
		}
		l.advance() // closing "
		return Token{Kind: String, Text: b.String(), Pos: start}, nil
	}

	// Multi-char punctuator.
	for _, p := range multiPunct {
		if strings.HasPrefix(l.src[l.i:], p) {
			for range p {
				l.advance()
			}
			return Token{Kind: Punct, Text: p, Pos: start}, nil
		}
	}

	// Single-char punctuator.
	switch r {
	case '+', '-', '*', '/', '%', '(', ')', '{', '}', '[', ']', ',', ';', ':', '=', '<', '>', '!', '&', '|', '^', '?', '.', '@':
		l.advance()
		return Token{Kind: Punct, Text: string(r), Pos: start}, nil
	}

	return Token{}, &Error{Pos: start, Msg: fmt.Sprintf("unexpected character %q", r)}
}

// scanFString consumes the body of an f-string starting at the
// current `"`, including the closing `"`. It returns the parts
// (alternating literal segments + raw-text interpolant
// expressions) which the parser sub-parses into ast.FString.
//
//	f"hello {x + 1} world" → [Lit:"hello ", Expr:"x + 1", Lit:" world"]
//	f"{count}"             → [Expr:"count"]
//	f"plain"               → [Lit:"plain"]
//	f""                    → []
//
// `{{` and `}}` escape literal braces. Standard string escapes
// (`\n`, `\t`, `\"`, `\\`, `\r`, `\0`) are honoured in the literal
// portions; inside `{...}` the bytes pass through verbatim for the
// parser to sub-parse.
func (l *lexer) scanFString(start ast.Position) ([]FStringPart, error) {
	if l.i >= len(l.src) || l.src[l.i] != '"' {
		return nil, &Error{Pos: start, Msg: "expected `\"` after `f`"}
	}
	l.advance() // opening "
	var lit strings.Builder
	var parts []FStringPart
	flushLit := func() {
		if lit.Len() == 0 {
			return
		}
		parts = append(parts, FStringPart{Lit: lit.String()})
		lit.Reset()
	}
	for l.i < len(l.src) {
		c := rune(l.src[l.i])
		if c == '"' {
			l.advance()
			flushLit()
			return parts, nil
		}
		if c == '\n' {
			return nil, &Error{Pos: start, Msg: "newline inside f-string literal"}
		}
		if c == '\\' {
			l.advance()
			if l.i >= len(l.src) {
				return nil, &Error{Pos: start, Msg: "unterminated f-string literal"}
			}
			esc := rune(l.src[l.i])
			l.advance()
			switch esc {
			case 'n':
				lit.WriteByte('\n')
			case 't':
				lit.WriteByte('\t')
			case 'r':
				lit.WriteByte('\r')
			case '0':
				lit.WriteByte(0)
			case '"':
				lit.WriteByte('"')
			case '\\':
				lit.WriteByte('\\')
			case 'x':
				if l.i+1 >= len(l.src) || !isHexDigit(rune(l.src[l.i])) || !isHexDigit(rune(l.src[l.i+1])) {
					return nil, &Error{Pos: start, Msg: "\\x escape needs two hex digits"}
				}
				lit.WriteByte(byte(hexVal(rune(l.src[l.i]))<<4 | hexVal(rune(l.src[l.i+1]))))
				l.advance()
				l.advance()
			default:
				return nil, &Error{Pos: start, Msg: fmt.Sprintf("unknown escape \\%c", esc)}
			}
			continue
		}
		if c == '{' {
			// `{{` → literal `{`.
			if l.i+1 < len(l.src) && l.src[l.i+1] == '{' {
				lit.WriteByte('{')
				l.advance()
				l.advance()
				continue
			}
			l.advance() // opening {
			flushLit()
			// Find the matching `}` at brace-depth 0 — supports
			// nested braces inside the interpolant (e.g. struct
			// literals) without losing the outer boundary.
			exprStart := l.i
			depth := 1
			for l.i < len(l.src) && depth > 0 {
				switch l.src[l.i] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						break
					}
				case '\n':
					return nil, &Error{Pos: start, Msg: "newline inside f-string interpolation"}
				case '"':
					// Skip a string literal inside the interpolant
					// so its braces / quotes don't confuse the
					// boundary scan.
					l.advance() // opening "
					for l.i < len(l.src) && l.src[l.i] != '"' {
						if l.src[l.i] == '\\' && l.i+1 < len(l.src) {
							l.advance()
						}
						l.advance()
					}
					if l.i >= len(l.src) {
						return nil, &Error{Pos: start, Msg: "unterminated string inside f-string interpolation"}
					}
					// fall through to the closing quote advance below
				}
				if depth > 0 {
					l.advance()
				}
			}
			if depth != 0 {
				return nil, &Error{Pos: start, Msg: "unterminated `{` in f-string"}
			}
			exprText := l.src[exprStart:l.i]
			l.advance() // closing }
			if strings.TrimSpace(exprText) == "" {
				return nil, &Error{Pos: start, Msg: "empty `{}` in f-string"}
			}
			parts = append(parts, FStringPart{Expr: exprText})
			continue
		}
		if c == '}' {
			// `}}` → literal `}`. A bare `}` is an error (matches
			// Python's f-string rule).
			if l.i+1 < len(l.src) && l.src[l.i+1] == '}' {
				lit.WriteByte('}')
				l.advance()
				l.advance()
				continue
			}
			return nil, &Error{Pos: start, Msg: "unmatched `}` in f-string (use `}}` for a literal `}`)"}
		}
		// Write the raw source byte, not WriteRune(c): same reason as
		// the plain-string scanner — c is a single byte
		// (`rune(l.src[l.i])`), so WriteRune would re-encode each byte
		// of a multi-byte UTF-8 character in an f-string literal
		// segment (e.g. `f"café {x}"`) into mojibake. Strings are
		// UTF-8 byte arrays; preserve the source bytes verbatim.
		lit.WriteByte(l.src[l.i])
		l.advance()
	}
	return nil, &Error{Pos: start, Msg: "unterminated f-string literal"}
}
