package lexer

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
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
	Char
	Byte
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
	case Char:
		return "Char"
	case Byte:
		return "Byte"
	}
	return "?"
}

type FStringPart struct {
	Lit  string
	Expr string
	Pos  ast.Position
}

type Token struct {
	Kind   Kind
	Text   string
	Pos    ast.Position
	FParts []FStringPart
	Suffix string
	Scalar int32
}

func (t Token) String() string {
	return fmt.Sprintf("%s(%q) at %s", t.Kind, t.Text, t.Pos)
}

var keywords = map[string]bool{
	"function": true,
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
	"true":     true,
	"false":    true,
	"boolean":  true,
	"void":     true,
	"string":   true,
	"i32":      true,
	"i64":      true,
	"u8":       true,
	"u32":      true,
	"u64":      true,
	"usize":    true,
	"f32":      true,
	"f64":      true,
	"f64x2":    true,
	"f32x4":    true,
	"as":       true,
	"struct":   true,
	"import":   true,
	"pub":      true,
	"const":    true,
	"enum":     true,
	"type":     true,
	"match":    true,
	"when":     true,
	"defer":    true,
	"errdefer": true,
	"trait":    true,
	"impl":     true,
	"dyn":      true,
}

func Keywords() []string {
	out := make([]string, 0, len(keywords))
	for k := range keywords {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var multiPunct = []string{
	"<<=", ">>=", "<<|", "<<?", ">>?", "...",
	"..=", "..",
	"==", "!=", "<=", ">=", "&&", "||", "<<", ">>", "=>", "|>",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=",
	"+|", "-|", "*|",
	"+?", "-?", "*?", "/?", "%?",
	"::",
}

type Error struct {
	Pos     ast.Position
	Msg     string
	Path    string
	Numeric bool
}

func (e *Error) Error() string          { return fmt.Sprintf("lex error at %s: %s", e.Pos, e.Msg) }
func (e *Error) Position() ast.Position { return e.Pos }
func (e *Error) File() string           { return e.Path }
func (e *Error) SetFile(p string)       { e.Path = p }

func (e *Error) Code() string {
	if e.Numeric {
		return "P002"
	}
	return "P001"
}

var (
	_ diag.FileSetter = (*Error)(nil)
	_ diag.Coded      = (*Error)(nil)
)

func validNumericSuffix(s string) bool {
	switch s {
	case "i32", "i64",
		"u8", "u32", "u64", "usize",
		"f32", "f64":
		return true
	}
	return false
}

func (l *lexer) scanDigits(start ast.Position, base int) error {
	digit := func(c byte) bool {
		if base == 16 {
			return isHexDigit(rune(c))
		}
		return asciiDigit(rune(c)) && int(c-'0') < base
	}
	begin := l.i
	for l.i < len(l.src) {
		c := l.src[l.i]
		if digit(c) {
			l.advance()
			continue
		}
		if c != '_' || l.i == begin || l.afterDot {
			return nil
		}
		if l.i+1 >= len(l.src) || !(digit(l.src[l.i+1]) || base < 10 && asciiDigit(rune(l.src[l.i+1]))) {
			return &Error{Pos: start, Msg: "a digit separator '_' must sit between two digits", Numeric: true}
		}
		l.advance()
	}
	return nil
}

func radixPrefix(src string, i int) int {
	if src[i] != '0' || i+1 >= len(src) {
		return 0
	}
	switch src[i+1] {
	case 'o', 'O':
		return 8
	case 'b', 'B':
		return 2
	}
	return 0
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

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

// Reserve sufficient slots for source files with few comments.
const tokenSourceBytesPerSlot = 3

func Tokenize(src string) ([]Token, []ast.Comment, error) {
	if err := checkUTF8(src); err != nil {
		return nil, nil, err
	}
	l := &lexer{src: src, line: 1, col: 1}
	out := make([]Token, 0, len(src)/tokenSourceBytesPerSlot+16)
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

func checkUTF8(src string) error {
	if utf8.ValidString(src) {
		return nil
	}
	line, col := 1, 1
	for i := 0; i < len(src); {
		r, size := utf8.DecodeRuneInString(src[i:])
		if r == utf8.RuneError && size == 1 {
			return &Error{Pos: ast.Position{Line: line, Col: col}, Msg: fmt.Sprintf("invalid UTF-8 byte 0x%02X", src[i])}
		}
		if r == '\n' {
			line, col = line+1, 1
		} else {
			col += size
		}
		i += size
	}
	return nil
}

type lexer struct {
	src       string
	i         int
	line, col int
	comments  []ast.Comment
	afterDot  bool
}

func asciiIdentStart(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func asciiIdentCont(r rune) bool {
	return asciiIdentStart(r) || asciiDigit(r)
}

func asciiDigit(r rune) bool { return r >= '0' && r <= '9' }

func asciiSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
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
		case asciiSpace(r):
			l.advance()
		case r == '/' && l.i+1 < len(l.src) && l.src[l.i+1] == '/':
			start := l.pos()
			l.advance()
			l.advance()
			textStart := l.i
			for l.i < len(l.src) && l.src[l.i] != '\n' {
				l.advance()
			}
			l.comments = append(l.comments, ast.Comment{
				Pos:      start,
				Text:     l.src[textStart:l.i],
				Trailing: codeBefore(l.src, textStart-2),
			})
		default:
			return
		}
	}
}

func codeBefore(src string, i int) bool {
	for j := i - 1; j >= 0 && src[j] != '\n'; j-- {
		if !asciiSpace(rune(src[j])) {
			return true
		}
	}
	return false
}

func (l *lexer) next() (Token, error) {
	l.skipTrivia()
	if l.i >= len(l.src) {
		return Token{Kind: EOF, Pos: l.pos()}, nil
	}

	start := l.pos()
	r, _ := l.peek()

	if r == 'f' && l.i+1 < len(l.src) && l.src[l.i+1] == '"' {
		l.advance()
		parts, err := l.scanFString(start)
		if err != nil {
			return Token{}, err
		}
		return Token{Kind: FString, Pos: start, FParts: parts}, nil
	}

	if r == 'b' && l.i+1 < len(l.src) && l.src[l.i+1] == '\'' {
		return l.scanQuoted(start, true)
	}

	if r == '\'' {
		return l.scanQuoted(start, false)
	}

	if asciiIdentStart(r) {
		begin := l.i
		for l.i < len(l.src) && asciiIdentCont(rune(l.src[l.i])) {
			l.advance()
		}
		text := l.src[begin:l.i]
		kind := Ident
		if keywords[text] {
			kind = Keyword
		}
		return Token{Kind: kind, Text: text, Pos: start}, nil
	}

	if asciiDigit(r) {
		begin := l.i
		prefixed := false
		if l.src[l.i] == '0' && l.i+1 < len(l.src) && (l.src[l.i+1] == 'x' || l.src[l.i+1] == 'X') {
			prefixed = true
			l.advance()
			l.advance()
			hexDigits := l.i
			if err := l.scanDigits(start, 16); err != nil {
				return Token{}, err
			}
			if l.i == hexDigits {
				return Token{}, &Error{Pos: start, Msg: "hex literal needs at least one digit after 0x", Numeric: true}
			}
		} else if base := radixPrefix(l.src, l.i); base != 0 {
			prefixed = true
			prefix := l.src[l.i : l.i+2]
			l.advance()
			l.advance()
			digits := l.i
			if err := l.scanDigits(start, base); err != nil {
				return Token{}, err
			}
			if l.i == digits {
				return Token{}, &Error{Pos: start, Msg: fmt.Sprintf("integer literal needs at least one digit after %s", prefix), Numeric: true}
			}
			if l.i < len(l.src) && asciiDigit(rune(l.src[l.i])) {
				return Token{}, &Error{Pos: start, Msg: fmt.Sprintf("invalid digit %q in a %s literal", l.src[l.i], prefix), Numeric: true}
			}
		} else if err := l.scanDigits(start, 10); err != nil {
			return Token{}, err
		}
		isFloat := false
		if !prefixed && !l.afterDot && l.i+1 < len(l.src) && l.src[l.i] == '.' && asciiDigit(rune(l.src[l.i+1])) {
			isFloat = true
			l.advance()
			if err := l.scanDigits(start, 10); err != nil {
				return Token{}, err
			}
		}
		if !prefixed && !l.afterDot && l.i < len(l.src) && (l.src[l.i] == 'e' || l.src[l.i] == 'E') {
			j := l.i + 1
			if j < len(l.src) && (l.src[j] == '+' || l.src[j] == '-') {
				j++
			}
			if j < len(l.src) && asciiDigit(rune(l.src[j])) {
				isFloat = true
				l.advance()
				if l.src[l.i] == '+' || l.src[l.i] == '-' {
					l.advance()
				}
				if err := l.scanDigits(start, 10); err != nil {
					return Token{}, err
				}
			}
		}
		text := l.src[begin:l.i]
		suffix := ""
		if l.i < len(l.src) {
			ch := l.src[l.i]
			if ch == 'i' || ch == 'u' || ch == 'f' {
				sBegin := l.i
				l.advance()
				if strings.HasPrefix(l.src[sBegin:], "usize") {
					for l.i < sBegin+len("usize") {
						l.advance()
					}
				}
				for l.i < len(l.src) && asciiDigit(rune(l.src[l.i])) {
					l.advance()
				}
				suffix = l.src[sBegin:l.i]
				if !validNumericSuffix(suffix) {
					return Token{}, &Error{Pos: start, Msg: fmt.Sprintf("unknown numeric suffix %q on %q", suffix, text), Numeric: true}
				}
				if isFloat && suffix[0] != 'f' {
					return Token{}, &Error{Pos: start, Msg: fmt.Sprintf("integer suffix %q on float literal %q", suffix, text), Numeric: true}
				}
			}
		}
		if !isFloat && suffix != "" && suffix[0] == 'f' {
			isFloat = true
		}
		kind := Number
		if isFloat {
			kind = Float
		}
		return Token{Kind: kind, Text: text, Pos: start, Suffix: suffix}, nil
	}

	if r == '"' {
		l.advance()
		var b strings.Builder
		for l.i < len(l.src) && rune(l.src[l.i]) != '"' {
			c := rune(l.src[l.i])
			if c == '\n' {
				return Token{}, &Error{Pos: start, Msg: "newline inside string literal"}
			}
			if c == '\\' {
				v, err := l.scanQuotedEscape(start, false, "string")
				if err != nil {
					return Token{}, err
				}
				b.WriteRune(rune(v))
				continue
			}
			b.WriteByte(l.src[l.i])
			l.advance()
		}
		if l.i >= len(l.src) {
			return Token{}, &Error{Pos: start, Msg: "unterminated string literal"}
		}
		l.advance()
		return Token{Kind: String, Text: b.String(), Pos: start}, nil
	}

	for _, p := range multiPunct {
		if strings.HasPrefix(l.src[l.i:], p) {
			for range p {
				l.advance()
			}
			return Token{Kind: Punct, Text: p, Pos: start}, nil
		}
	}

	switch r {
	case '+', '-', '*', '/', '%', '(', ')', '{', '}', '[', ']', ',', ';', ':', '=', '<', '>', '!', '&', '|', '^', '?', '.', '@':
		l.advance()
		return Token{Kind: Punct, Text: string(r), Pos: start}, nil
	}

	return Token{}, l.badCharError(start)
}

func (l *lexer) badCharError(start ast.Position) error {
	r, _ := utf8.DecodeRuneInString(l.src[l.i:])
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return &Error{Pos: start, Msg: fmt.Sprintf("identifiers must be ASCII; found %q", r)}
	}
	return &Error{Pos: start, Msg: fmt.Sprintf("unexpected character %q", r)}
}

func (l *lexer) scanQuoted(start ast.Position, isByte bool) (Token, error) {
	begin := l.i
	kindName := "character"
	if isByte {
		kindName = "byte"
		l.advance()
	}
	l.advance()
	if l.i >= len(l.src) {
		return Token{}, &Error{Pos: start, Msg: "unterminated " + kindName + " literal"}
	}
	c := l.src[l.i]
	switch c {
	case '\'':
		l.advance()
		return Token{}, &Error{Pos: start, Msg: "empty " + kindName + " literal"}
	case '\n':
		return Token{}, &Error{Pos: start, Msg: "newline inside " + kindName + " literal"}
	}

	var val int32
	switch {
	case c == '\\':
		v, err := l.scanQuotedEscape(start, isByte, kindName)
		if err != nil {
			return Token{}, err
		}
		val = v
	case isByte:
		if c >= utf8.RuneSelf {
			r, _ := utf8.DecodeRuneInString(l.src[l.i:])
			return Token{}, &Error{Pos: start, Msg: fmt.Sprintf("byte literal must be ASCII; %q is a Unicode scalar, so write it as a character literal", r)}
		}
		val = int32(c)
		l.advance()
	default:
		r, size := utf8.DecodeRuneInString(l.src[l.i:])
		if r == utf8.RuneError && size <= 1 {
			return Token{}, &Error{Pos: start, Msg: fmt.Sprintf("invalid UTF-8 byte 0x%02X in character literal", c)}
		}
		for k := 0; k < size; k++ {
			l.advance()
		}
		val = int32(r)
	}

	if l.i >= len(l.src) || l.src[l.i] != '\'' {
		unit := "character"
		if isByte {
			unit = "byte"
		}
		if l.closingQuoteAhead() {
			return Token{}, &Error{Pos: start, Msg: fmt.Sprintf("%s literal must hold exactly one %s", kindName, unit)}
		}
		return Token{}, &Error{Pos: start, Msg: "unterminated " + kindName + " literal"}
	}
	l.advance()
	kind := Char
	if isByte {
		kind = Byte
	}
	return Token{Kind: kind, Text: l.src[begin:l.i], Pos: start, Scalar: val}, nil
}

func (l *lexer) closingQuoteAhead() bool {
	for j := l.i; j < len(l.src) && l.src[j] != '\n'; j++ {
		if l.src[j] == '\'' {
			return true
		}
	}
	return false
}

func (l *lexer) scanQuotedEscape(start ast.Position, isByte bool, kindName string) (int32, error) {
	l.advance()
	if l.i >= len(l.src) {
		return 0, &Error{Pos: start, Msg: "unterminated " + kindName + " literal"}
	}
	esc := l.src[l.i]
	l.advance()
	switch esc {
	case 'n':
		return '\n', nil
	case 't':
		return '\t', nil
	case 'r':
		return '\r', nil
	case '0':
		return 0, nil
	case '\\':
		return '\\', nil
	case '\'':
		return '\'', nil
	case '"':
		return '"', nil
	case 'x':
		if l.i+1 >= len(l.src) || !isHexDigit(rune(l.src[l.i])) || !isHexDigit(rune(l.src[l.i+1])) {
			return 0, &Error{Pos: start, Msg: "\\x escape needs two hex digits"}
		}
		v := int32(hexVal(rune(l.src[l.i]))<<4 | hexVal(rune(l.src[l.i+1])))
		l.advance()
		l.advance()
		if !isByte && v > 0x7F {
			return 0, &Error{Pos: start, Msg: fmt.Sprintf("\\x%02X is above ASCII in %s; write \\u{%X} for the scalar", v, literalName(kindName), v)}
		}
		return v, nil
	case 'u':
		if isByte {
			return 0, &Error{Pos: start, Msg: "\\u{...} is a Unicode scalar; a byte literal takes \\xNN"}
		}
		if l.i >= len(l.src) || l.src[l.i] != '{' {
			return 0, &Error{Pos: start, Msg: "\\u escape needs braces: \\u{1F600}"}
		}
		l.advance()
		digits := 0
		v := int32(0)
		for l.i < len(l.src) && isHexDigit(rune(l.src[l.i])) {
			if digits == 6 {
				return 0, &Error{Pos: start, Msg: "\\u{...} takes at most 6 hex digits"}
			}
			v = v<<4 | int32(hexVal(rune(l.src[l.i])))
			digits++
			l.advance()
		}
		if digits == 0 {
			return 0, &Error{Pos: start, Msg: "\\u{...} needs at least one hex digit"}
		}
		if l.i >= len(l.src) || l.src[l.i] != '}' {
			return 0, &Error{Pos: start, Msg: "unterminated \\u{...} escape"}
		}
		l.advance()
		if v > 0x10FFFF {
			return 0, &Error{Pos: start, Msg: fmt.Sprintf("\\u{%X} is above the maximum Unicode scalar 10FFFF", v)}
		}
		if v >= 0xD800 && v <= 0xDFFF {
			return 0, &Error{Pos: start, Msg: fmt.Sprintf("\\u{%X} is a surrogate, not a Unicode scalar value", v)}
		}
		return v, nil
	}
	return 0, &Error{Pos: start, Msg: fmt.Sprintf("unknown escape \\%c in %s literal", rune(esc), kindName)}
}

func literalName(kindName string) string {
	if kindName == "f-string" {
		return "an f-string literal"
	}
	return "a " + kindName + " literal"
}

func (l *lexer) scanFString(start ast.Position) ([]FStringPart, error) {
	if l.i >= len(l.src) || l.src[l.i] != '"' {
		return nil, &Error{Pos: start, Msg: "expected `\"` after `f`"}
	}
	l.advance()
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
			v, err := l.scanQuotedEscape(start, false, "f-string")
			if err != nil {
				return nil, err
			}
			lit.WriteRune(rune(v))
			continue
		}
		if c == '{' {
			if l.i+1 < len(l.src) && l.src[l.i+1] == '{' {
				lit.WriteByte('{')
				l.advance()
				l.advance()
				continue
			}
			l.advance()
			flushLit()
			exprStart := l.i
			exprPos := l.pos()
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
					l.advance()
					for l.i < len(l.src) && l.src[l.i] != '"' {
						if l.src[l.i] == '\\' && l.i+1 < len(l.src) {
							l.advance()
						}
						l.advance()
					}
					if l.i >= len(l.src) {
						return nil, &Error{Pos: start, Msg: "unterminated string inside f-string interpolation"}
					}
				}
				if depth > 0 {
					l.advance()
				}
			}
			if depth != 0 {
				return nil, &Error{Pos: start, Msg: "unterminated `{` in f-string"}
			}
			exprText := l.src[exprStart:l.i]
			l.advance()
			if strings.TrimSpace(exprText) == "" {
				return nil, &Error{Pos: start, Msg: "empty `{}` in f-string"}
			}
			parts = append(parts, FStringPart{Expr: exprText, Pos: exprPos})
			continue
		}
		if c == '}' {
			if l.i+1 < len(l.src) && l.src[l.i+1] == '}' {
				lit.WriteByte('}')
				l.advance()
				l.advance()
				continue
			}
			return nil, &Error{Pos: start, Msg: "unmatched `}` in f-string (use `}}` for a literal `}`)"}
		}
		lit.WriteByte(l.src[l.i])
		l.advance()
	}
	return nil, &Error{Pos: start, Msg: "unterminated f-string literal"}
}
