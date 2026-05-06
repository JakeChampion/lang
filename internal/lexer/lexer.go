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
	Ident
	Punct
	Keyword
)

func (k Kind) String() string {
	switch k {
	case EOF:
		return "EOF"
	case Number:
		return "Number"
	case Ident:
		return "Ident"
	case Punct:
		return "Punct"
	case Keyword:
		return "Keyword"
	}
	return "?"
}

type Token struct {
	Kind  Kind
	Text  string
	Pos   ast.Position
}

func (t Token) String() string {
	return fmt.Sprintf("%s(%q) at %s", t.Kind, t.Text, t.Pos)
}

var keywords = map[string]bool{
	"function": true,
	"var":      true,
	"if":       true,
	"else":     true,
	"while":    true,
	"for":      true,
	"return":   true,
	"true":     true,
	"false":    true,
	"number":   true,
	"boolean":  true,
	"void":     true,
}

// Multi-character punctuators, longest first.
var multiPunct = []string{
	"==", "!=", "<=", ">=", "&&", "||",
}

type Error struct {
	Pos ast.Position
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("lex error at %s: %s", e.Pos, e.Msg) }

// Tokenize turns src into a slice of tokens terminated by an EOF token.
func Tokenize(src string) ([]Token, error) {
	l := &lexer{src: src, line: 1, col: 1}
	var out []Token
	for {
		tok, err := l.next()
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
		if tok.Kind == EOF {
			return out, nil
		}
	}
}

type lexer struct {
	src        string
	i          int
	line, col  int
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
			for l.i < len(l.src) && l.src[l.i] != '\n' {
				l.advance()
			}
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

	// Number literal.
	if unicode.IsDigit(r) {
		begin := l.i
		for l.i < len(l.src) && unicode.IsDigit(rune(l.src[l.i])) {
			l.advance()
		}
		return Token{Kind: Number, Text: l.src[begin:l.i], Pos: start}, nil
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
	case '+', '-', '*', '/', '(', ')', '{', '}', '[', ']', ',', ';', ':', '=', '<', '>', '!':
		l.advance()
		return Token{Kind: Punct, Text: string(r), Pos: start}, nil
	}

	return Token{}, &Error{Pos: start, Msg: fmt.Sprintf("unexpected character %q", r)}
}
