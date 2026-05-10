// Package lexer turns source text into a slice of tokens.
//
// The lexer is a single linear pass over the input rune-by-rune. It
// recognises a handful of keywords, integer literals, identifiers, and
// punctuation. Whitespace and `//` line-comments are skipped.
package lexer

import (
	"fmt"
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
	"break":    true,
	"continue": true,
	"return":   true,
	"true":     true,
	"false":    true,
	"boolean":  true,
	"void":     true,
	"string":   true,
	"float":    true,
	// Sized numeric type names. `number` is retained as an alias
	// for `i32` and `float` for `f32` until the deprecation window
	// closes (PR 5 of docs/LANGUAGE-DIRECTION.md). Sub-i32 widths
	// (i8, i16, u8, u16) and unsigned 32/64 widths (u32, u64) are
	// keyword-reserved here but not yet wired through codegen —
	// they ship in a follow-up.
	"i8":  true,
	"i16": true,
	"i32": true,
	"i64": true,
	"u8":  true,
	"u16": true,
	"u32": true,
	"u64": true,
	"f32": true,
	"f64": true,
	"as":  true,
	"switch":   true,
	"case":     true,
	"default":  true,
	"struct":   true,
	"import":   true,
	"pub":      true,
	"const":    true,
	"enum":     true,
	"match":    true,
	"when":     true,
	"defer":    true,
	"arena":    true,
}

// Multi-character punctuators, longest first. The 3-char compound
// shifts (`<<=`, `>>=`) sit before the 2-char shifts so the
// longest-prefix rule picks the right one.
var multiPunct = []string{
	"<<=", ">>=",
	"==", "!=", "<=", ">=", "&&", "||", "<<", ">>", "=>", "|>",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=",
}

type Error struct {
	Pos ast.Position
	Msg string
}

func (e *Error) Error() string         { return fmt.Sprintf("lex error at %s: %s", e.Pos, e.Msg) }
func (e *Error) Position() ast.Position { return e.Pos }

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
		for l.i < len(l.src) && unicode.IsDigit(rune(l.src[l.i])) {
			l.advance()
		}
		if l.i+1 < len(l.src) && l.src[l.i] == '.' && unicode.IsDigit(rune(l.src[l.i+1])) {
			l.advance() // '.'
			for l.i < len(l.src) && unicode.IsDigit(rune(l.src[l.i])) {
				l.advance()
			}
			return Token{Kind: Float, Text: l.src[begin:l.i], Pos: start}, nil
		}
		return Token{Kind: Number, Text: l.src[begin:l.i], Pos: start}, nil
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
				default:
					return Token{}, &Error{Pos: start, Msg: fmt.Sprintf("unknown escape \\%c", esc)}
				}
				continue
			}
			b.WriteRune(c)
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
	case '+', '-', '*', '/', '%', '(', ')', '{', '}', '[', ']', ',', ';', ':', '=', '<', '>', '!', '&', '|', '^', '?', '.':
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
		lit.WriteRune(c)
		l.advance()
	}
	return nil, &Error{Pos: start, Msg: "unterminated f-string literal"}
}
