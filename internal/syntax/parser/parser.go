package parser

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/lexer"
)

type Error struct {
	Pos     ast.Position
	Msg     string
	Path    string
	ErrCode string
}

func (e *Error) Error() string          { return fmt.Sprintf("parse error at %s: %s", e.Pos, e.Msg) }
func (e *Error) Position() ast.Position { return e.Pos }
func (e *Error) File() string           { return e.Path }
func (e *Error) SetFile(p string)       { e.Path = p }
func (e *Error) Code() string           { return e.ErrCode }

var _ diag.FileSetter = (*Error)(nil)

func Parse(src string) (*ast.Program, error) {
	return ParseContext(context.Background(), src)
}

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
	elideLenBoundedChecks(prog)
	return prog, nil
}

func blankLineNumbers(src string) []int {
	var out []int
	for i, line := range strings.Split(src, "\n") {
		if strings.TrimSpace(line) == "" {
			out = append(out, i+1)
		}
	}
	return out
}

func parseExprFromText(text string, base ast.Position) (ast.Expr, error) {
	tokens, _, err := lexer.Tokenize(text)
	if err != nil {
		var le *lexer.Error
		if errors.As(err, &le) {
			return nil, &lexer.Error{Pos: offsetPos(base, le.Pos), Msg: le.Msg, Numeric: le.Numeric}
		}
		return nil, err
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

func offsetPos(base, in ast.Position) ast.Position {
	if in.Line <= 1 {
		return ast.Position{Line: base.Line, Col: base.Col + in.Col - 1}
	}
	return ast.Position{Line: base.Line + in.Line - 1, Col: in.Col}
}

type parser struct {
	tokens          []lexer.Token
	i               int
	errors          []error
	ctx             context.Context
	typeRefs        []ast.TypeRef
	todoSites       []ast.Position
	foreachN        int
	nestedTails     map[int]bool
	constParams     []string
	noStructLit     bool
	returnTypeStack []ast.Type
	useN            int
	nestN           int
	depth           int
}

const maxNestDepth = 5000

const blockNestUnits = 8

func (p *parser) enter() error { return p.enterUnits(1) }

func (p *parser) enterUnits(n int) error {
	p.depth += n
	if p.depth > maxNestDepth {
		return p.errorfCode(p.peek().Pos, "P005", "input nests deeper than %d levels", maxNestDepth)
	}
	return nil
}

func (p *parser) leave() { p.depth-- }

func (p *parser) leaveUnits(n int) { p.depth -= n }

func (p *parser) peek() lexer.Token { return p.tokens[p.i] }

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

func inherentImplMessage(typ string) string {
	return "`impl " + typ + "` needs a trait, as in `impl cmp.Eq for " + typ + "`; declare a method as `function (self: " + typ + ") name(…)` and a constructor as a plain function"
}

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

func (p *parser) acceptIf(when bool, kind lexer.Kind, text string) (lexer.Token, bool) {
	if !when {
		return lexer.Token{}, false
	}
	return p.accept(kind, text)
}

func discardName(name string, pos ast.Position, nth int) string {
	if name != "_" {
		return name
	}
	return fmt.Sprintf("__discard_%d_%d_%d", pos.Line, pos.Col, nth)
}

func (p *parser) moreElems(close string) bool {
	if _, ok := p.accept(lexer.Punct, ","); !ok {
		return false
	}
	return !p.match(lexer.Punct, close)
}

func (p *parser) fipModifierAt(i int) (name string, allowance int, next int, ok bool) {
	name, allowance, next, ok = p.fipModifierShapeAt(i)
	if !ok {
		return "", 0, 0, false
	}
	for j := next; j < len(p.tokens); {
		if t := p.tokens[j]; t.Kind == lexer.Keyword && t.Text == "function" {
			return name, allowance, next, true
		}
		_, _, after, shapeOK := p.fipModifierShapeAt(j)
		if !shapeOK {
			break
		}
		j = after
	}
	return "", 0, 0, false
}

func (p *parser) fipModifierShapeAt(i int) (name string, allowance int, next int, ok bool) {
	if i >= len(p.tokens) || p.tokens[i].Kind != lexer.Ident ||
		(p.tokens[i].Text != "fip" && p.tokens[i].Text != "fbip") {
		return "", 0, 0, false
	}
	name = p.tokens[i].Text
	j := i + 1
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
	return name, allowance, j, true
}

func expectedDesc(kind lexer.Kind, text string) string {
	if text != "" {
		return strconv.Quote(text)
	}
	switch kind {
	case lexer.Ident:
		return "a name"
	case lexer.String:
		return "a string literal"
	case lexer.Number:
		return "a number"
	}
	return strconv.Quote(kind.String())
}

func tokenDesc(t lexer.Token) string {
	if t.Kind == lexer.EOF {
		return "the end of the file"
	}
	return strconv.Quote(t.Text)
}

func (p *parser) expect(kind lexer.Kind, text string) (lexer.Token, error) {
	t := p.peek()
	if t.Kind != kind || (text != "" && t.Text != text) {
		return lexer.Token{}, p.errorfCode(t.Pos, "P001", "expected %s, got %s", expectedDesc(kind, text), tokenDesc(t))
	}
	return p.advance(), nil
}

func (p *parser) expectAs(kind lexer.Kind, text, msg string) (lexer.Token, error) {
	t := p.peek()
	if t.Kind != kind || (text != "" && t.Text != text) {
		return lexer.Token{}, p.errorfCode(t.Pos, "P001", "%s", msg)
	}
	return p.advance(), nil
}

func (p *parser) parseProgram() *ast.Program {
	prog := &ast.Program{}
	for !p.match(lexer.EOF, "") {
		if p.ctx != nil && p.ctx.Err() != nil {
			return prog
		}
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
		var derives []string
		var importIface, importWIT string
		var exportIface, exportWIT string
		mustConsume := false
		tryMarker := false
		inlineHint := ast.InlineHintNone
		attrName := ""
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
			tryMarker = attr.tryMarker
			inlineHint = attr.inlineHint
			attrName = attr.name
		}
		isPub := false
		isPackage := false
		if p.match(lexer.Keyword, "pub") {
			pubTok := p.advance()
			if _, ok := p.accept(lexer.Punct, "("); ok {
				if _, err := p.expect(lexer.Ident, "package"); err != nil {
					p.errors = append(p.errors, p.errorfCode(pubTok.Pos, "P001", "`pub(...)` only supports `pub(package)`"))
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
			if p.match(lexer.Keyword, "use") {
				if attrName != "" {
					p.errors = append(p.errors, p.errorfCode(p.peek().Pos, "P001", "%s", attrMisplaced(attrName)))
					p.syncToTopLevel()
					continue
				}
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
				p.errors = append(p.errors, p.errorfCode(pubTok.Pos, "P001",
					"`pub` must be followed by `function`, `struct`, `enum`, `type`, `trait`, `const`, `opaque`, `fip`, `fbip`, or `async`"))
				p.syncToTopLevel()
				if p.i == before {
					p.advance()
				}
				continue
			}
			isPub = !isPackage
		}
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
			isFip = false
		}
		isAsync := false
		if p.match(lexer.Ident, "async") && p.i+1 < len(p.tokens) &&
			p.tokens[p.i+1].Kind == lexer.Keyword && p.tokens[p.i+1].Text == "function" {
			p.advance()
			isAsync = true
		}
		isOpaque := false
		if p.match(lexer.Ident, "opaque") && p.i+1 < len(p.tokens) &&
			p.tokens[p.i+1].Kind == lexer.Keyword && p.tokens[p.i+1].Text == "struct" {
			p.advance()
			isOpaque = true
		}
		isResource := p.match(lexer.Ident, "resource") && p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Ident
		declKind := ""
		if isResource {
			declKind = "resource"
		} else if p.peek().Kind == lexer.Keyword {
			declKind = p.peek().Text
		}
		if !attrFits(attrName, declKind) {
			p.errors = append(p.errors, p.errorfCode(p.peek().Pos, "P001", "%s", attrMisplaced(attrName)))
			p.syncToTopLevel()
			if p.i == before {
				p.advance()
			}
			continue
		}
		if isResource {
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
				rd.ImportIface = importIface
				rd.ImportWITName = importWIT
				prog.Resources = append(prog.Resources, rd)
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
				ed.Try = tryMarker
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
				id.Methods = methods
				prog.Impls = append(prog.Impls, id)
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

func (p *parser) parseImport() (*ast.Import, error) {
	kw := p.advance()
	pathTok, err := p.expect(lexer.String, "")
	if err != nil {
		return nil, err
	}
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

func (p *parser) parsePubUse(pubPos ast.Position) (*ast.PubUse, error) {
	p.advance()
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
		nameTok, err := p.expect(lexer.Ident, "")
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
		return nil, p.errorfCode(pubPos, "P001", "`pub use` must name at least one symbol, e.g. `pub use \"std/string\".{split};`")
	}
	return &ast.PubUse{P: pubPos, Path: pathTok.Text, Names: names}, nil
}

func importLocalName(path string) string {
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if strings.HasSuffix(base, ".fern") {
		base = base[:len(base)-len(".fern")]
	}
	return strings.ReplaceAll(base, "-", "_")
}

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

func (p *parser) skipBraced() {
	depth := 0
	for !p.match(lexer.EOF, "") {
		switch {
		case p.match(lexer.Punct, "{"):
			depth++
		case p.match(lexer.Punct, "}"):
			depth--
		case depth == 0:
			return
		}
		p.advance()
		if depth == 0 {
			return
		}
	}
}

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

func (p *parser) parseTraitDecl() (*ast.TraitDecl, error) {
	kw, err := p.expect(lexer.Keyword, "trait")
	if err != nil {
		return nil, err
	}
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	var typeParams []string
	if p.match(lexer.Punct, "[") {
		open := p.peek().Pos
		typeParams, _, err = p.parseTypeParamList()
		if err != nil {
			return nil, err
		}
		if err := p.refuseConstParams(open); err != nil {
			return nil, err
		}
	}
	supertraits, supertraitArgs, err := p.parseOptBoundsWithArgs()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, err
	}
	td := &ast.TraitDecl{P: kw.Pos, Name: name.Text, NamePos: name.Pos, Supertraits: supertraits, SupertraitArgs: supertraitArgs, TypeParams: typeParams}
	for !p.match(lexer.Punct, "}") && !p.match(lexer.EOF, "") {
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
		if p.match(lexer.Keyword, "const") {
			ckw := p.advance()
			cname, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(lexer.Punct, ":"); err != nil {
				return nil, err
			}
			ctype, err := p.parseType()
			if err != nil {
				return nil, err
			}
			var body *ast.Block
			if _, ok := p.accept(lexer.Punct, "="); ok {
				val, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				body = &ast.Block{P: val.Pos(), Stmts: []ast.Stmt{&ast.Return{P: val.Pos(), Value: val}}}
			}
			if _, err := p.expect(lexer.Punct, ";"); err != nil {
				return nil, err
			}
			td.Methods = append(td.Methods, ast.TraitMethod{P: ckw.Pos, Name: cname.Text, Result: ctype, Assoc: true, Const: true, Body: body})
			continue
		}
		mkw, err := p.expect(lexer.Keyword, "function")
		if err != nil {
			return nil, err
		}
		mname, err := p.expect(lexer.Ident, "")
		if err != nil {
			return nil, err
		}
		var mtps []string
		var mbounds map[string][]string
		var mbargs map[string][][]ast.Type
		if p.match(lexer.Punct, "[") {
			open := p.peek().Pos
			mtps, mbounds, mbargs, err = p.parseTypeParamListWithArgs()
			if err != nil {
				return nil, err
			}
			if err := p.refuseConstParams(open); err != nil {
				return nil, err
			}
		}
		if _, err := p.expect(lexer.Punct, "("); err != nil {
			return nil, err
		}
		var params []ast.Param
		if !p.match(lexer.Punct, ")") {
			for {
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
		assoc := len(params) == 0 || params[0].Name != "self"
		var ret ast.Type = ast.VoidType{}
		if _, ok := p.accept(lexer.Punct, ":"); ok {
			t, err := p.parseType()
			if err != nil {
				return nil, err
			}
			ret = t
		}
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
		if body == nil && len(mtps) > 0 {
			return nil, p.errorf(mkw.Pos, "trait method %q declares its own type parameters, so it must have a default body", mname.Text)
		}
		td.Methods = append(td.Methods, ast.TraitMethod{
			P: mkw.Pos, Name: mname.Text, Params: params, Result: ret, Assoc: assoc, Body: body,
			TypeParams: mtps, Bounds: mbounds, BoundArgs: mbargs,
		})
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	return td, nil
}

func (p *parser) parseImplDecl() (*ast.ImplDecl, []*ast.FuncDecl, error) {
	kw, err := p.expect(lexer.Keyword, "impl")
	if err != nil {
		return nil, nil, err
	}
	var implTypeParams []string
	var implBounds map[string][]string
	var implBoundArgs map[string][][]ast.Type
	if p.match(lexer.Punct, "[") {
		open := p.peek().Pos
		implTypeParams, implBounds, implBoundArgs, err = p.parseTypeParamListWithArgs()
		if err != nil {
			return nil, nil, err
		}
		if err := p.refuseConstParams(open); err != nil {
			return nil, nil, err
		}
	}
	tnameTok, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, nil, err
	}
	tname := p.maybeQualify(tnameTok.Text)
	var bracketTypes []ast.Type
	if p.match(lexer.Punct, "[") {
		p.advance()
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
	if !p.match(lexer.Keyword, "for") {
		spelling := tname
		if len(bracketTypes) > 0 {
			spelling = ast.EnumType{Name: tname, Args: bracketTypes}.String()
		}
		err := p.errorfCode(tnameTok.Pos, "P001", "%s", inherentImplMessage(spelling))
		p.skipBraced()
		return nil, nil, err
	}
	trait := tname
	traitArgs := bracketTypes
	p.advance()
	typePos := p.peek().Pos
	implType, err := p.parseType()
	if err != nil {
		return nil, nil, err
	}
	if _, ok := implType.(ast.SelfType); ok {
		return nil, nil, p.errorf(typePos, "`impl … for Self` is not allowed; name a concrete type")
	}
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, nil, err
	}
	id := &ast.ImplDecl{P: kw.Pos, Trait: trait, TraitPos: tnameTok.Pos, Type: implType, TypePos: typePos, TypeParams: implTypeParams, Bounds: implBounds, BoundArgs: implBoundArgs, TraitArgs: traitArgs}
	var methods []*ast.FuncDecl
	for !p.match(lexer.Punct, "}") && !p.match(lexer.EOF, "") {
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
		var fn *ast.FuncDecl
		if p.match(lexer.Keyword, "const") {
			ckw := p.advance()
			cname, err := p.expect(lexer.Ident, "")
			if err != nil {
				return nil, nil, err
			}
			if _, err := p.expect(lexer.Punct, ":"); err != nil {
				return nil, nil, err
			}
			ctype, err := p.parseType()
			if err != nil {
				return nil, nil, err
			}
			if _, err := p.expect(lexer.Punct, "="); err != nil {
				return nil, nil, err
			}
			val, err := p.parseExpr()
			if err != nil {
				return nil, nil, err
			}
			if _, err := p.expect(lexer.Punct, ";"); err != nil {
				return nil, nil, err
			}
			fn = &ast.FuncDecl{P: ckw.Pos, Name: cname.Text, ReturnType: ctype, AssocConst: true,
				Body: &ast.Block{P: val.Pos(), Stmts: []ast.Stmt{&ast.Return{P: val.Pos(), Value: val}}}}
		} else {
			f, err := p.parseFunction()
			if err != nil {
				return nil, nil, err
			}
			fn = f
		}
		if fn.Receiver != nil {
			return nil, nil, p.errorf(fn.P,
				"impl method %q must not declare a receiver clause; its first parameter is `self: Self`", fn.Name)
		}
		if len(fn.ConstParams) > 0 {
			p.constParams = fn.ConstParams
			return nil, nil, p.refuseConstParams(fn.NamePos)
		}
		if fn.Body == nil {
			return nil, nil, p.errorf(fn.P,
				"impl method %q has no body (only @import functions may omit a body)", fn.Name)
		}
		assoc := len(fn.Params) == 0 || fn.Params[0].Name != "self"
		written := &ast.ImplSig{Result: fn.ReturnType}
		for _, prm := range fn.Params {
			written.Params = append(written.Params, prm.Type)
		}
		fn.ImplWritten = written
		for i := range fn.Params {
			fn.Params[i].Type = ast.SubstSelf(fn.Params[i].Type, implType)
		}
		fn.ReturnType = ast.SubstSelf(fn.ReturnType, implType)
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
		if len(implTypeParams) > 0 {
			if len(fn.TypeParams) > 0 {
				return nil, nil, p.errorf(fn.P,
					"impl method %q cannot declare its own type parameters inside a parametric `impl[…]` block", fn.Name)
			}
			fn.TypeParams = implTypeParams
			fn.Bounds = implBounds
			fn.BoundArgs = implBoundArgs
		}
		methods = append(methods, fn)
		id.MethodNames = append(id.MethodNames, fn.Name)
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, nil, err
	}
	return id, methods, nil
}

type declAttr struct {
	name        string
	derives     []string
	importIface string
	importWIT   string
	exportIface string
	exportWIT   string
	mustConsume bool
	tryMarker   bool
	inlineHint  ast.InlineHint
}

func (p *parser) parseAttribute() (declAttr, error) {
	at := p.advance()
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
		return declAttr{name: attr, derives: derives}, nil
	case "must_consume":
		return declAttr{name: attr, mustConsume: true}, nil
	case "try":
		return declAttr{name: attr, tryMarker: true}, nil
	case "inline":
		return declAttr{name: attr, inlineHint: ast.InlineHintAlways}, nil
	case "noinline":
		return declAttr{name: attr, inlineHint: ast.InlineHintNever}, nil
	case "import", "export":
		iface, name, err := p.parseIfaceNamePair()
		if err != nil {
			return declAttr{}, err
		}
		if attr == "import" {
			return declAttr{name: attr, importIface: iface, importWIT: name}, nil
		}
		return declAttr{name: attr, exportIface: iface, exportWIT: name}, nil
	default:
		return declAttr{}, p.errorfCode(at.Pos, "P001", "unknown attribute @%s (only @derive, @import, @export, @must_consume, @try, @inline, and @noinline are supported)", attr)
	}
}

func attrFits(name, kind string) bool {
	switch name {
	case "":
		return true
	case "derive", "must_consume":
		return kind == "struct" || kind == "enum"
	case "try":
		return kind == "enum"
	case "import":
		return kind == "function" || kind == "resource"
	}
	return kind == "function"
}

func attrMisplaced(name string) string {
	switch name {
	case "derive", "must_consume":
		return "@" + name + " only applies to a `struct` or `enum` declaration"
	case "try":
		return "@try only applies to an `enum` declaration"
	case "import":
		return "@import only applies to a function or resource declaration"
	case "export":
		return "@export only applies to a function declaration"
	}
	return "@inline / @noinline only applies to a function declaration"
}

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

func (p *parser) parseResourceDecl() (*ast.ResourceDecl, error) {
	kw := p.advance()
	nameTok, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.ResourceDecl{P: kw.Pos, Name: nameTok.Text}, nil
}

func handleWordMisuse(word string) string {
	if word == "own" {
		return "`own` in a type must be followed by a resource name (`own R` for a `resource R`); a consuming parameter is written `own name: T`"
	}
	return "`borrow` in a type must be followed by a resource name (`borrow R` for a `resource R`); a parameter borrows by default, written `name: T`"
}

func (p *parser) maybeQualify(first string) string {
	if p.match(lexer.Punct, ".") {
		p.advance()
		if rest, ok := p.accept(lexer.Ident, ""); ok {
			return first + "." + rest.Text
		}
	}
	return first
}

func (p *parser) parseDeclTypeParams(kind, name string) ([]string, error) {
	var typeParams []string
	for {
		pname, err := p.expect(lexer.Ident, "")
		if err != nil {
			return nil, err
		}
		typeParams = append(typeParams, pname.Text)
		if t := p.peek(); t.Kind == lexer.Punct && t.Text == ":" {
			return nil, p.errorfCode(t.Pos, "P001", "%s %s: a bound on type parameter %s belongs on an impl (impl[%s: Trait] Trait for %s[%s]), not on the declaration",
				kind, name, pname.Text, pname.Text, name, pname.Text)
		}
		if p.moreElems("]") {
			continue
		}
		break
	}
	if _, err := p.expect(lexer.Punct, "]"); err != nil {
		return nil, err
	}
	return typeParams, nil
}

func (p *parser) parseTypeParamList() ([]string, map[string][]string, error) {
	tp, b, _, err := p.parseTypeParamListWithArgs()
	return tp, b, err
}

func (p *parser) parseTypeParamListWithArgs() ([]string, map[string][]string, map[string][][]ast.Type, error) {
	if _, err := p.expect(lexer.Punct, "["); err != nil {
		return nil, nil, nil, err
	}
	var typeParams []string
	var bounds map[string][]string
	var boundArgs map[string][][]ast.Type
	p.constParams = nil
	for {
		if p.match(lexer.Keyword, "const") {
			name, err := p.parseConstTypeParam()
			if err != nil {
				return nil, nil, nil, err
			}
			typeParams = append(typeParams, name)
			p.constParams = append(p.constParams, name)
			if !p.moreElems("]") {
				break
			}
			continue
		}
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

func (p *parser) parseConstTypeParam() (string, error) {
	p.advance()
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return "", err
	}
	if t := p.peek(); t.Kind != lexer.Punct || t.Text != ":" {
		return "", p.errorfCode(t.Pos, "P001", "const parameter %s needs a type: write const %s: i32", name.Text, name.Text)
	}
	p.advance()
	if t := p.peek(); t.Kind != lexer.Keyword || t.Text != "i32" {
		return "", p.errorfCode(t.Pos, "P001", "const parameter %s has type i32, got %s", name.Text, tokenDesc(t))
	}
	p.advance()
	return name.Text, nil
}

func (p *parser) refuseConstParams(pos ast.Position) error {
	if len(p.constParams) == 0 {
		return nil
	}
	name := p.constParams[0]
	p.constParams = nil
	return p.errorfCode(pos, "P001", "const parameter %s: only a function takes a const parameter", name)
}

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
	var typeParams []string
	var bounds map[string][]string
	var boundArgs map[string][][]ast.Type
	var constParams []string
	if p.match(lexer.Punct, "[") {
		typeParams, bounds, boundArgs, err = p.parseTypeParamListWithArgs()
		if err != nil {
			return nil, err
		}
		constParams, p.constParams = p.constParams, nil
	}
	var receiver *ast.Param
	if p.match(lexer.Punct, "(") && p.looksLikeReceiverClause() {
		p.advance()
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
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	funcNamePos := name.Pos
	if p.match(lexer.Punct, "[") && len(typeParams) == 0 {
		typeParams, bounds, boundArgs, err = p.parseTypeParamListWithArgs()
		if err != nil {
			return nil, err
		}
		constParams, p.constParams = p.constParams, nil
	}
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
		if body == nil {
			return nil, p.errorf(funcNamePos, "a destructured parameter requires a function body")
		}
		prependParamDestructures(body, paramDestrs)
	}
	if receiver != nil && len(constParams) > 0 {
		p.constParams = constParams
		return nil, p.refuseConstParams(funcNamePos)
	}
	return &ast.FuncDecl{
		P:                 kw.Pos,
		Name:              name.Text,
		NamePos:           funcNamePos,
		TypeParams:        typeParams,
		ConstParams:       constParams,
		Bounds:            bounds,
		BoundArgs:         boundArgs,
		Params:            params,
		ReturnType:        ret,
		ReturnUnannotated: !retAnnotated && body != nil,
		Body:              body,
		Receiver:          receiver,
	}, nil
}

func (p *parser) looksLikeReceiverClause() bool {
	start := p.i
	if !p.match(lexer.Punct, "(") {
		return false
	}
	p.i++
	if p.peek().Kind == lexer.Ident && p.peek().Text == "own" &&
		p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Ident {
		p.i++
	}
	if p.peek().Kind != lexer.Ident {
		p.i = start
		return false
	}
	p.i++
	if !(p.peek().Kind == lexer.Punct && p.peek().Text == ":") {
		p.i = start
		return false
	}
	depth := 0
	for p.i < len(p.tokens)-1 {
		t := p.tokens[p.i]
		if t.Kind == lexer.Punct && t.Text == "(" {
			depth++
		} else if t.Kind == lexer.Punct && t.Text == ")" {
			if depth == 0 {
				p.i++
				break
			}
			depth--
		}
		p.i++
	}
	ok := p.peek().Kind == lexer.Ident
	if ok {
		p.i++
		ok = p.peek().Kind == lexer.Punct && (p.peek().Text == "(" || p.peek().Text == "[")
	}
	p.i = start
	return ok
}

func (p *parser) parseLocalFunction() (ast.Stmt, error) {
	fn, err := p.parseFunction()
	if err != nil {
		return nil, err
	}
	if fn.Body == nil {
		p.errors = append(p.errors, p.errorf(fn.P,
			"function %q has no body (only @import functions may omit a body)", fn.Name))
	}
	fn.IsLocal = true
	return fn, nil
}

func (p *parser) parseStructDecl() (*ast.StructDecl, error) {
	kw, err := p.expect(lexer.Keyword, "struct")
	if err != nil {
		return nil, err
	}
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	var typeParams []string
	if _, ok := p.accept(lexer.Punct, "["); ok {
		typeParams, err = p.parseDeclTypeParams("struct", name.Text)
		if err != nil {
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

func (p *parser) parseEnumDecl() (*ast.EnumDecl, error) {
	kw, err := p.expect(lexer.Keyword, "enum")
	if err != nil {
		return nil, err
	}
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	var typeParams []string
	if _, ok := p.accept(lexer.Punct, "["); ok {
		typeParams, err = p.parseDeclTypeParams("enum", name.Text)
		if err != nil {
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
				if _, err := p.expect(lexer.Punct, ")"); err != nil {
					return nil, err
				}
			} else if _, ok := p.accept(lexer.Punct, "{"); ok {
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

func (p *parser) parseUnionDecl() (*ast.UnionDecl, error) {
	kw, err := p.expect(lexer.Keyword, "type")
	if err != nil {
		return nil, err
	}
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return nil, err
	}
	var typeParams []string
	if _, ok := p.accept(lexer.Punct, "["); ok {
		typeParams, err = p.parseDeclTypeParams("type", name.Text)
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(lexer.Punct, "="); err != nil {
		return nil, err
	}
	var members []ast.StructType
	var lines []int
	for {
		lines = append(lines, p.peek().Pos.Line)
		mem, err := p.parseUnionMember()
		if err != nil {
			return nil, err
		}
		members = append(members, mem)
		if !p.match(lexer.Punct, "|") {
			break
		}
		p.advance()
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	if len(members) < 2 {
		return nil, p.errorf(name.Pos, "union %q must list at least two struct members (use a struct alias for a single type)", name.Text)
	}
	return &ast.UnionDecl{P: kw.Pos, Name: name.Text, TypeParams: typeParams, Members: members, MemberLines: lines}, nil
}

func (p *parser) parseUnionMember() (ast.StructType, error) {
	name, err := p.expect(lexer.Ident, "")
	if err != nil {
		return ast.StructType{}, err
	}
	st := ast.StructType{Name: name.Text}
	if p.match(lexer.Punct, "[") {
		p.advance()
		for {
			arg, err := p.parseType()
			if err != nil {
				return ast.StructType{}, err
			}
			st.Args = append(st.Args, arg)
			if p.match(lexer.Punct, ",") {
				p.advance()
				continue
			}
			break
		}
		if _, err := p.expect(lexer.Punct, "]"); err != nil {
			return ast.StructType{}, err
		}
	}
	return st, nil
}

func (p *parser) parenListIsFuncParams() bool {
	depth := 1
	for i := p.i; i < len(p.tokens); i++ {
		t := p.tokens[i]
		if t.Kind != lexer.Punct {
			continue
		}
		switch t.Text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i+1 < len(p.tokens) && p.tokens[i+1].Kind == lexer.Punct && p.tokens[i+1].Text == "=>"
			}
		}
	}
	return false
}

func (p *parser) parseType() (ast.Type, error) {
	return p.parseTypeArrow(true)
}

func (p *parser) parseTypeArrow(topArrow bool) (ast.Type, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	t := p.peek()
	var base ast.Type
	switch {
	case t.Kind == lexer.Punct && t.Text == "[":
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
		p.advance()
		base = ast.NumberType{Width: ast.WidthPtr, Signed: false, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "f64":
		p.advance()
		base = ast.FloatType{Width: 64, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "f32":
		p.advance()
		base = ast.FloatType{Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "f64x2":
		p.advance()
		base = ast.LanesType{Width: 64, Spelling: t.Text}
	case t.Kind == lexer.Keyword && t.Text == "f32x4":
		p.advance()
		base = ast.LanesType{Width: 32, Spelling: t.Text}
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
		p.advance()
		ownMarkers := p.parenListIsFuncParams()
		var elems []ast.Type
		var owns []bool
		if !p.match(lexer.Punct, ")") {
			for {
				isOwn := false
				if ownMarkers && p.peek().Kind == lexer.Ident && p.peek().Text == "own" &&
					p.i+1 < len(p.tokens) && !(p.tokens[p.i+1].Kind == lexer.Punct &&
					(p.tokens[p.i+1].Text == "," || p.tokens[p.i+1].Text == ")")) {
					isOwn = true
					p.advance()
				}
				pt, err := p.parseType()
				if err != nil {
					return nil, err
				}
				elems = append(elems, pt)
				owns = append(owns, isOwn)
				if _, ok := p.accept(lexer.Punct, ","); !ok {
					break
				}
			}
		}
		if _, err := p.expect(lexer.Punct, ")"); err != nil {
			return nil, err
		}
		if _, isArrow := p.acceptIf(topArrow, lexer.Punct, "=>"); isArrow {
			ret, err := p.parseType()
			if err != nil {
				return nil, err
			}
			base = &ast.FuncType{Params: elems, ParamOwn: ast.OwnFlags(owns, len(elems)), Result: ret}
		} else if len(elems) >= 2 {
			base = ast.TupleType{Elems: elems}
		} else if len(elems) == 1 {
			base = elems[0]
		} else {
			return nil, p.errorfCode(t.Pos, "P001", "`()` is not a type; the unit type is written `void`")
		}
	case t.Kind == lexer.Keyword && t.Text == "dyn":
		p.advance()
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
				p.advance()
				for {
					if p.match(lexer.Ident, "") && p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "=" {
						nameT := p.advance()
						p.advance()
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
	case t.Kind == lexer.Ident && (t.Text == "own" || t.Text == "borrow"):
		p.advance()
		nameTok, ok := p.accept(lexer.Ident, "")
		if !ok {
			return nil, p.errorfCode(t.Pos, "P001", "%s", handleWordMisuse(t.Text))
		}
		borrowed := t.Text == "borrow"
		name := p.maybeQualify(nameTok.Text)
		p.typeRefs = append(p.typeRefs, ast.TypeRef{P: nameTok.Pos, Name: name})
		base = ast.HandleType{Resource: name, Borrowed: borrowed}
	case t.Kind == lexer.Ident && t.Text == "Self" &&
		!(p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "."):
		p.advance()
		base = ast.SelfType{}
	case t.Kind == lexer.Ident && t.Text == "str" &&
		!(p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "."):
		p.advance()
		base = ast.StrType{}
	case t.Kind == lexer.Ident && t.Text == "char" &&
		!(p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "."):
		p.advance()
		base = ast.CharType{}
	case t.Kind == lexer.Ident && t.Text == "float" &&
		!(p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "."):
		return nil, p.errorfCode(t.Pos, "P001", "`float` is not a type; the 64-bit float is written `f64`")
	case t.Kind == lexer.Ident:
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
		p.typeRefs = append(p.typeRefs, ast.TypeRef{P: t.Pos, Name: name})
		if p.match(lexer.Punct, "[") && p.i+1 < len(p.tokens) && !(p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "]") {
			p.advance()
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
			if name == "Stream" && len(args) == 1 {
				base = ast.StreamType{Elem: args[0]}
			} else {
				base = ast.EnumType{Name: name, Args: args}
			}
		} else {
			base = ast.StructType{Name: name}
		}
	default:
		return nil, p.errorfCode(t.Pos, "P001", "expected a type, got %s", tokenDesc(t))
	}
	for p.match(lexer.Punct, "::") {
		p.advance()
		nameTok, err := p.expect(lexer.Ident, "")
		if err != nil {
			return nil, err
		}
		base = ast.ProjType{Base: base, Name: nameTok.Text}
	}
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

func (p *parser) parseBlock() (*ast.Block, error) {
	open, err := p.expect(lexer.Punct, "{")
	if err != nil {
		return nil, err
	}
	block := &ast.Block{P: open.Pos}
	p.parseBlockStmts(block, false)
	closeTok, err := p.expect(lexer.Punct, "}")
	if err != nil {
		return block, err
	}
	block.End = closeTok.Pos
	return block, nil
}

func (p *parser) parseBlockStmts(block *ast.Block, tailValue bool) {
	p.parseBlockStmtsMode(block, tailValue, false, false)
}

func (p *parser) parseBlockStmtsMode(block *ast.Block, tailValue, discardTail, statementBranches bool) {
	if err := p.enterUnits(blockNestUnits); err != nil {
		p.errors = append(p.errors, err)
		return
	}
	defer p.leaveUnits(blockNestUnits)
	for !p.match(lexer.Punct, "}") && !p.match(lexer.EOF, "") {
		before := p.i
		if (discardTail || statementBranches) && (p.match(lexer.Keyword, "if") || p.match(lexer.Keyword, "match")) {
			nRefs, nTodo, nErr := len(p.typeRefs), len(p.todoSites), len(p.errors)
			stmt, err := p.parseStmt()
			if err == nil && len(p.errors) == nErr {
				block.Stmts = append(block.Stmts, stmt)
				continue
			}
			p.i = before
			p.typeRefs, p.todoSites, p.errors = p.typeRefs[:nRefs], p.todoSites[:nTodo], p.errors[:nErr]
		}
		if p.match(lexer.Keyword, "use") {
			if err := p.parseUse(block); err != nil {
				p.errors = append(p.errors, err)
				p.syncToStmt()
				if p.i == before {
					p.advance()
				}
				continue
			}
			return
		}
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
		if tailValue {
			if s, ok := p.valuelessBranchTail(); ok {
				block.Stmts = append(block.Stmts, s)
				continue
			}
		}
		if tailValue && (!p.branchStmtStart() || p.nestedBlockTail()) {
			mark, nRefs, nTodo, nErr := p.i, len(p.typeRefs), len(p.todoSites), len(p.errors)
			e, err := p.parseExpr()
			terminated := false
			if err == nil && discardTail && len(p.errors) == nErr && p.tokens[mark].Kind == lexer.Keyword && (p.tokens[mark].Text == "if" || p.tokens[mark].Text == "match") {
				_, terminated = p.accept(lexer.Punct, ";")
			}
			if err == nil && (p.match(lexer.Punct, "}") || terminated) && (!discardTail || len(p.errors) == nErr) {
				if discardTail {
					block.Stmts = append(block.Stmts, &ast.ExprStmt{P: e.Pos(), Expr: e})
				} else {
					block.Stmts = append(block.Stmts, &ast.Return{P: e.Pos(), Value: e})
				}
				if terminated {
					continue
				}
				return
			}
			p.i = mark
			p.typeRefs, p.todoSites, p.errors = p.typeRefs[:nRefs], p.todoSites[:nTodo], p.errors[:nErr]
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

func (p *parser) parseLambdaBody(statementBranches bool) (*ast.Block, error) {
	open, err := p.expect(lexer.Punct, "{")
	if err != nil {
		return nil, err
	}
	block := &ast.Block{P: open.Pos}
	p.parseBlockStmtsMode(block, true, false, statementBranches)
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return block, err
	}
	return block, nil
}

func (p *parser) parseStmt() (ast.Stmt, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	t := p.peek()
	if t.Kind == lexer.Punct && t.Text == "{" {
		return p.parseBlock()
	}
	if t.Kind == lexer.Ident && p.i+2 < len(p.tokens) {
		c, n := p.tokens[p.i+1], p.tokens[p.i+2]
		if c.Kind == lexer.Punct && c.Text == ":" && n.Kind == lexer.Keyword &&
			(n.Text == "while" || n.Text == "loop" || n.Text == "for") {
			label := t.Text
			p.advance()
			p.advance()
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
	if t.Kind == lexer.Ident && t.Text == "assert" && p.i+1 < len(p.tokens) {
		if nx := p.tokens[p.i+1]; nx.Kind == lexer.Punct && nx.Text == "(" {
			return p.parseAssert()
		}
	}
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
		case "let":
			return p.parseLet(false)
		case "match":
			return p.parseMatch()
		case "function":
			return p.parseLocalFunction()
		}
	}
	e, err := p.parseAssign()
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
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.While{P: kw.Pos, Cond: cond, Body: body, Label: label}, nil
}

func (p *parser) parseLoop(label string) (ast.Stmt, error) {
	kw := p.advance()
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.Loop{P: kw.Pos, Body: body, Label: label}, nil
}

func (p *parser) looksLikeArrowLambda() bool {
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
	if p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == ")" {
		return followedByArrowOrColon(p.i + 2)
	}
	if !p.firstParamLooksTyped(p.i + 1) {
		return false
	}
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

func (p *parser) firstParamLooksTyped(idx int) bool {
	isIdent := func(i int) bool { return i < len(p.tokens) && p.tokens[i].Kind == lexer.Ident }
	isPunct := func(i int, text string) bool {
		return i < len(p.tokens) && p.tokens[i].Kind == lexer.Punct && p.tokens[i].Text == text
	}
	if isIdent(idx) && isPunct(idx+1, "@") {
		idx += 2
	}
	if isIdent(idx) && p.tokens[idx].Text == "own" && isIdent(idx+1) {
		idx++
	}
	switch {
	case isPunct(idx, "("):
		return p.closerFollowedBy(idx, "(", ")", ":", "=>")
	case p.recordPatternBraceAt(idx) >= 0:
		return p.closerFollowedBy(p.recordPatternBraceAt(idx), "{", "}", ":")
	default:
		return isIdent(idx) && isPunct(idx+1, ":")
	}
}

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

func (p *parser) parseArrowLambda() (ast.Expr, error) {
	open := p.advance()
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
			lamOwn := false
			if p.match(lexer.Ident, "own") && p.i+1 < len(p.tokens) && p.tokens[p.i+1].Kind == lexer.Ident {
				p.advance()
				lamOwn = true
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
			params = append(params, ast.Param{Name: discardName(pname.Text, pname.Pos, len(params)), NamePos: pname.Pos, Type: ptype, Own: lamOwn})
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
		mark, refs := p.i, len(p.typeRefs)
		t, err := p.parseType()
		if err != nil || !p.match(lexer.Punct, "=>") {
			p.i, p.typeRefs = mark, p.typeRefs[:refs]
			t, err = p.parseTypeArrow(false)
		}
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
	_, voidResult := ret.(ast.VoidType)
	body, err := p.parseArrowBody(open.Pos, !unannotated && voidResult)
	p.returnTypeStack = p.returnTypeStack[:len(p.returnTypeStack)-1]
	if err != nil {
		return nil, err
	}
	prependParamDestructures(body, paramDestrs)
	return &ast.Lambda{P: open.Pos, Params: params, ReturnType: ret, ReturnUnannotated: unannotated, Body: body}, nil
}

func (p *parser) parseArrowBody(pos ast.Position, statementBranches bool) (*ast.Block, error) {
	if p.match(lexer.Punct, "{") {
		return p.parseLambdaBody(statementBranches)
	}
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ast.Block{P: pos, Stmts: []ast.Stmt{&ast.Return{P: pos, Value: e}}}, nil
}

func (p *parser) parseIfExpr() (ast.Expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	kw := p.advance()
	if p.match(lexer.Keyword, "let") {
		return p.parseIfLetExpr(kw.Pos)
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
	thenE, err := p.parseBranchBody()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Keyword, "else"); err != nil {
		return nil, err
	}
	if p.match(lexer.Keyword, "if") {
		elseE, err := p.parseIfExpr()
		if err != nil {
			return nil, err
		}
		return &ast.IfExpr{P: kw.Pos, Cond: cond, Then: thenE, Else: elseE, ElseChained: true}, nil
	}
	elseE, err := p.parseBranchBody()
	if err != nil {
		return nil, err
	}
	return &ast.IfExpr{P: kw.Pos, Cond: cond, Then: thenE, Else: elseE}, nil
}

// parseIfLetExpr uses the shared pattern arms and requires a value on each path.
func (p *parser) parseIfLetExpr(pos ast.Position) (ast.Expr, error) {
	p.advance()
	pats, err := p.parseOrPatterns()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, "="); err != nil {
		return nil, err
	}
	prev := p.noStructLit
	p.noStructLit = true
	src, err := p.parseExpr()
	p.noStructLit = prev
	if err != nil {
		return nil, err
	}
	then, err := p.parseBranchBody()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Keyword, "else"); err != nil {
		return nil, err
	}
	var els ast.Expr
	chained := p.match(lexer.Keyword, "if")
	if chained {
		els, err = p.parseIfExpr()
	} else {
		els, err = p.parseBranchBody()
	}
	if err != nil {
		return nil, err
	}
	raw := make([]exprRawArm, len(pats))
	for i, pat := range pats {
		body := then
		if i > 0 {
			body = ast.CloneExpr(then)
		}
		raw[i] = exprRawArm{pat: pat, body: body, altCont: i > 0}
	}
	arms := flatExprArms(raw)
	arms = append(arms, &ast.MatchExprArm{P: pos, IsWildcard: true, Body: els})
	return &ast.MatchExpr{P: pos, Tag: src, Arms: arms, Origin: ast.OriginIfLet, ElseChained: chained}, nil
}

func (p *parser) parseBranchBody() (ast.Expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	open, err := p.expect(lexer.Punct, "{")
	if err != nil {
		return nil, err
	}
	var stmts []ast.Stmt
	var tail ast.Expr
	for !p.match(lexer.Punct, "}") && !p.match(lexer.EOF, "") {
		if p.match(lexer.Keyword, "match") {
			if stmt, ok := p.valuelessBranchTail(); ok {
				stmts = append(stmts, stmt)
				continue
			}
		}
		if p.branchStmtStart() && !p.nestedBlockTail() {
			s, err := p.parseStmt()
			if err != nil {
				return nil, err
			}
			if s != nil {
				stmts = append(stmts, s)
			}
			continue
		}
		mark, nRefs, nTodo, nErr := p.i, len(p.typeRefs), len(p.todoSites), len(p.errors)
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, ok := p.accept(lexer.Punct, ";"); ok {
			stmts = append(stmts, &ast.ExprStmt{P: e.Pos(), Expr: e})
			continue
		}
		if !p.match(lexer.Punct, "}") {
			p.i = mark
			p.typeRefs, p.todoSites, p.errors = p.typeRefs[:nRefs], p.todoSites[:nTodo], p.errors[:nErr]
			s, serr := p.parseStmt()
			if serr != nil {
				return nil, serr
			}
			if s != nil {
				stmts = append(stmts, s)
			}
			continue
		}
		tail = e
		break
	}
	if _, err := p.expect(lexer.Punct, "}"); err != nil {
		return nil, err
	}
	if len(stmts) == 0 && tail != nil {
		return tail, nil
	}
	return &ast.BlockExpr{P: open.Pos, Stmts: stmts, Tail: tail}, nil
}

func (p *parser) nestedBlockTail() bool {
	if !p.match(lexer.Punct, "{") {
		return false
	}
	if yes, ok := p.nestedTails[p.i]; ok {
		return yes
	}
	mark, nRefs, nTodo, nErr := p.i, len(p.typeRefs), len(p.todoSites), len(p.errors)
	if err := p.enter(); err != nil {
		p.leave()
		p.errors = p.errors[:nErr]
		return false
	}
	e, err := p.parseBranchBody()
	yes := err == nil && p.match(lexer.Punct, "}")
	if b, ok := e.(*ast.BlockExpr); ok && b.Tail == nil {
		yes = false
	}
	p.leave()
	p.i = mark
	p.typeRefs, p.todoSites, p.errors = p.typeRefs[:nRefs], p.todoSites[:nTodo], p.errors[:nErr]
	if p.nestedTails == nil {
		p.nestedTails = map[int]bool{}
	}
	p.nestedTails[mark] = yes
	return yes
}

func (p *parser) valuelessBranchTail() (ast.Stmt, bool) {
	t := p.peek()
	if t.Kind != lexer.Keyword || (t.Text != "match" && t.Text != "if") {
		return nil, false
	}
	if t.Text == "match" && !p.matchArmsBraced() {
		return nil, false
	}
	mark, nRefs, nTodo, nErr := p.i, len(p.typeRefs), len(p.todoSites), len(p.errors)
	s, err := p.parseStmt()
	if err == nil && len(p.errors) == nErr && p.match(lexer.Punct, "}") {
		return s, true
	}
	p.i = mark
	p.typeRefs, p.todoSites, p.errors = p.typeRefs[:nRefs], p.todoSites[:nTodo], p.errors[:nErr]
	return nil, false
}

func (p *parser) matchArmsBraced() bool {
	depth := 0
	for j := p.i + 1; j < len(p.tokens); j++ {
		t := p.tokens[j]
		if t.Kind != lexer.Punct {
			continue
		}
		if t.Text == "=>" && depth == 1 {
			next := p.peekAt(j + 1 - p.i)
			if next.Kind != lexer.Punct || next.Text != "{" {
				return false
			}
			mark, nRefs, nTodo, nErr := p.i, len(p.typeRefs), len(p.todoSites), len(p.errors)
			p.i = j + 1
			body, err := p.parseBranchBody()
			after := p.i
			block, noTail := body.(*ast.BlockExpr)
			noTail = noTail && block.Tail == nil
			valid := err == nil && len(p.errors) == nErr
			p.i = mark
			p.typeRefs, p.todoSites, p.errors = p.typeRefs[:nRefs], p.todoSites[:nTodo], p.errors[:nErr]
			if !valid || !noTail {
				return false
			}
			j = after - 1
			continue
		}
		switch t.Text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
			if depth == 0 && t.Text == "}" {
				return true
			}
		}
	}
	return false
}

func (p *parser) branchStmtStart() bool {
	t := p.peek()
	if t.Kind == lexer.Punct && t.Text == "{" {
		return true
	}
	if t.Kind == lexer.Ident && p.i+2 < len(p.tokens) {
		c, n := p.tokens[p.i+1], p.tokens[p.i+2]
		if c.Kind == lexer.Punct && c.Text == ":" && n.Kind == lexer.Keyword &&
			(n.Text == "while" || n.Text == "loop" || n.Text == "for") {
			return true
		}
	}
	if t.Kind == lexer.Keyword && t.Text == "if" && !p.ifStmtHasElse() {
		return true
	}
	if t.Kind == lexer.Keyword {
		switch t.Text {
		case "while", "loop", "for", "break", "continue",
			"return", "defer", "errdefer", "let",
			"function", "use":
			return true
		}
	}
	return false
}

func (p *parser) ifStmtHasElse() bool {
	i := p.i + 1
	if i < len(p.tokens) && p.tokens[i].Kind == lexer.Keyword && p.tokens[i].Text == "let" {
		i++
		depth, source := 0, false
		for ; i < len(p.tokens); i++ {
			t := p.tokens[i]
			if t.Kind != lexer.Punct {
				continue
			}
			if source && depth == 0 && t.Text == "{" {
				break
			}
			if !source && depth == 0 && t.Text == "=" {
				source = true
				continue
			}
			switch t.Text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				depth--
			}
		}
	}
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

func (p *parser) parseFor(label string) (ast.Stmt, error) {
	kw := p.advance()

	if p.match(lexer.Ident, "") && p.i+1 < len(p.tokens) {
		if next := p.tokens[p.i+1]; next.Kind == lexer.Ident && next.Text == "in" {
			return p.parseForEach(kw, label)
		}
	}

	if p.forHeaderPatternAhead() {
		return p.parseForEachPattern(kw, label)
	}

	if _, err := p.expect(lexer.Punct, "("); err != nil {
		return nil, err
	}

	var init ast.Stmt
	if p.match(lexer.Keyword, "let") {
		kw := p.advance()
		v, err := p.parseLetBinding(kw.Pos)
		if err != nil {
			return nil, err
		}
		init = v
	} else if p.match(lexer.Punct, ";") {
		p.advance()
	} else {
		e, err := p.parseAssign()
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
		stepExpr, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		step = &ast.ExprStmt{P: stepExpr.Pos(), Expr: stepExpr}
	}
	if _, err := p.expect(lexer.Punct, ")"); err != nil {
		return nil, err
	}

	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	return &ast.For{P: kw.Pos, Init: init, Cond: cond, Step: step, Body: body, Label: label}, nil
}

func (p *parser) nextForeachID() int {
	p.foreachN++
	return p.foreachN
}

func (p *parser) parseForEach(kw lexer.Token, label string) (ast.Stmt, error) {
	nameTok := p.advance()
	p.advance()
	prevNS := p.noStructLit
	p.noStructLit = true
	expr, err := p.parseLogicOr()
	p.noStructLit = prevNS
	if err != nil {
		return nil, err
	}
	if p.match(lexer.Punct, "..") || p.match(lexer.Punct, "..=") {
		rangeTok := p.advance()
		inclusive := rangeTok.Text == "..="
		prevNS2 := p.noStructLit
		p.noStructLit = true
		high, err := p.parseLogicOr()
		p.noStructLit = prevNS2
		if err != nil {
			return nil, err
		}
		body, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		rid := p.nextForeachID()
		hiName := fmt.Sprintf("__range_hi_%d", rid)
		mkI := func(name string) *ast.Ident { return &ast.Ident{P: kw.Pos, Name: name} }
		declHi := &ast.Var{P: kw.Pos, Name: hiName, Init: high}
		declI := &ast.Var{P: nameTok.Pos, Name: nameTok.Text, Init: expr}
		step := ast.Stmt(&ast.ExprStmt{P: kw.Pos, Expr: &ast.Assign{P: kw.Pos, Target: mkI(nameTok.Text),
			Value: &ast.Binary{P: kw.Pos, Op: "+", Left: mkI(nameTok.Text), Right: &ast.NumberLit{P: kw.Pos, Value: 1}}}})
		loop := &ast.For{P: kw.Pos, Init: declI, Cond: &ast.Binary{P: kw.Pos, Op: "<", Left: mkI(nameTok.Text), Right: mkI(hiName)},
			Step: step, Body: body, Label: label}
		stmts := []ast.Stmt{declHi, loop}
		if inclusive {
			goName := fmt.Sprintf("__range_go_%d", rid)
			declGo := &ast.Var{P: kw.Pos, Name: goName,
				Init: &ast.Binary{P: kw.Pos, Op: "<=", Left: mkI(nameTok.Text), Right: mkI(hiName)}}
			clearGo := &ast.ExprStmt{P: kw.Pos, Expr: &ast.Assign{P: kw.Pos, Target: mkI(goName),
				Value: &ast.Binary{P: kw.Pos, Op: "!=", Left: mkI(nameTok.Text), Right: mkI(hiName)}}}
			loop.Init = nil
			loop.Cond = mkI(goName)
			loop.Step = &ast.Block{P: kw.Pos, Stmts: []ast.Stmt{clearGo, step}}
			stmts = []ast.Stmt{declHi, declI, declGo, loop}
		}
		sugar := &ast.ForEach{P: kw.Pos, ID: rid, Var: nameTok.Text, VarPos: nameTok.Pos,
			Iter: expr, RangeHigh: high, RangeIncl: inclusive, Body: body, Label: label}
		return &ast.Block{P: kw.Pos, Stmts: stmts, Sugar: sugar}, nil
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
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

func (p *parser) forHeaderPatternAhead() bool {
	if !p.match(lexer.Punct, "(") {
		return p.atPatternHead()
	}
	depth := 0
	for i := p.i; i < len(p.tokens); i++ {
		t := p.tokens[i]
		if t.Kind != lexer.Punct {
			continue
		}
		switch t.Text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
			if depth == 0 {
				return i+1 < len(p.tokens) && p.tokens[i+1].Kind == lexer.Ident && p.tokens[i+1].Text == "in"
			}
		}
	}
	return false
}

func (p *parser) parseForEachPattern(kw lexer.Token, label string) (ast.Stmt, error) {
	pos := p.peek().Pos
	pat, err := p.parseMatchPattern()
	if err != nil {
		return nil, err
	}
	d, err := p.irrefutableDestructure(pos, pat, foreachSite, "")
	if err != nil {
		return nil, err
	}
	if p.peek().Kind != lexer.Ident || p.peek().Text != "in" {
		return nil, p.errorfCode(p.peek().Pos, "P001", "expected \"in\" after the `for` pattern, got %s", tokenDesc(p.peek()))
	}
	p.advance()

	prevNS := p.noStructLit
	p.noStructLit = true
	expr, err := p.parseExpr()
	p.noStructLit = prevNS
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}

	id := p.nextForeachID()
	elem := pat.atBinding
	if elem == "" {
		elem = ast.ForEachElemName(id)
	}
	fe := &ast.ForEach{
		P:       kw.Pos,
		ID:      id,
		Var:     elem,
		VarPos:  pos,
		Pattern: d,
		Iter:    expr,
		Body:    body,
		Label:   label,
	}
	return fe, nil
}

func (p *parser) peekTypeArgs(opener string) bool {
	if !p.match(lexer.Punct, "[") {
		return false
	}
	if p.i+1 >= len(p.tokens) {
		return false
	}
	next := p.tokens[p.i+1]
	typeShaped := false
	switch {
	case next.Kind == lexer.Keyword && isTypeKeyword(next.Text):
		typeShaped = true
	case next.Kind == lexer.Ident || isConstArgTok(next):
		typeShaped = opener == "{"
	case next.Kind == lexer.Punct && next.Text == "(":
	default:
		return false
	}
	depth, parens := 1, 0
	for j := p.i + 1; j < len(p.tokens); j++ {
		t := p.tokens[j]
		if t.Kind != lexer.Punct {
			continue
		}
		switch t.Text {
		case "[":
			depth++
			if j+1 < len(p.tokens) {
				nt := p.tokens[j+1]
				if (nt.Kind == lexer.Keyword && isTypeKeyword(nt.Text)) || (nt.Kind == lexer.Punct && nt.Text == "]") {
					typeShaped = true
				}
			}
		case ",":
			if depth == 1 && parens == 0 {
				typeShaped = true
			}
		case "=>":
			if depth == 1 {
				typeShaped = true
			}
		case "{":
			return false
		case "(":
			if pt := p.tokens[j-1]; pt.Kind != lexer.Punct || pt.Text == "]" || pt.Text == ")" {
				return false
			}
			parens++
			if j+1 < len(p.tokens) && p.tokens[j+1].Kind == lexer.Keyword && isTypeKeyword(p.tokens[j+1].Text) {
				typeShaped = true
			}
		case ")":
			parens--
		case "]":
			depth--
			if depth == 0 {
				if !typeShaped || j+1 >= len(p.tokens) {
					return false
				}
				nt := p.tokens[j+1]
				return nt.Kind == lexer.Punct && nt.Text == opener
			}
		}
	}
	return false
}

func isConstArgTok(t lexer.Token) bool {
	return t.Kind == lexer.Number && t.Suffix == ""
}

func isTypeKeyword(s string) bool {
	switch s {
	case "i32", "i64", "u8", "u32", "u64", "usize", "f32", "f64", "f64x2", "f32x4", "string", "boolean", "void":
		return true
	}
	return false
}

func (p *parser) parseTypeArgList() ([]ast.Type, error) {
	var args []ast.Type
	for {
		if isConstArgTok(p.peek()) {
			e, err := p.parsePrimary()
			if err != nil {
				return nil, err
			}
			lit := e.(*ast.NumberLit)
			v := lit.Value
			if lit.ExceedsI64 || lit.ExceedsU64 {
				v = math.MaxInt64
			}
			args = append(args, ast.ConstArg{Value: v, Raw: lit.Raw})
			if !p.moreElems("]") {
				break
			}
			continue
		}
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
	return &ast.Unary{P: lit.Pos(), Op: "-", Operand: lit}, nil
}

func (p *parser) parseMatch() (ast.Stmt, error) {
	kw := p.advance()
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
	m.Arms = flatStmtArms(raw)
	p.accept(lexer.Punct, ";")
	return m, nil
}

func flatStmtArms(raw []stmtRawArm) []*ast.MatchArm {
	out := make([]*ast.MatchArm, len(raw))
	for i := range raw {
		out[i] = stmtArmFromPattern(raw[i].pat, raw[i].guard, raw[i].body)
		out[i].AltCont = raw[i].altCont
	}
	return out
}

func flatExprArms(raw []exprRawArm) []*ast.MatchExprArm {
	out := make([]*ast.MatchExprArm, len(raw))
	for i := range raw {
		out[i] = exprArmFromPattern(raw[i].pat, raw[i].guard, raw[i].body)
		out[i].AltCont = raw[i].altCont
	}
	return out
}

type stmtRawArm struct {
	pat     matchPattern
	guard   ast.Expr
	body    *ast.Block
	altCont bool
}

// parseMatchStmtBody reads a value block or a bare expression and discards
// its value. The statement block keeps the arm's local scope.
func (p *parser) parseMatchStmtBody() (*ast.Block, error) {
	if !p.match(lexer.Punct, "{") {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ast.Block{P: e.Pos(), Stmts: []ast.Stmt{&ast.ExprStmt{P: e.Pos(), Expr: e}}}, nil
	}
	open := p.advance()
	body := &ast.Block{P: open.Pos}
	p.parseBlockStmtsMode(body, true, true, false)
	closeTok, err := p.expect(lexer.Punct, "}")
	if err != nil {
		return body, err
	}
	body.End = closeTok.Pos
	return body, nil
}

// parseStmtRawArms reads one arm and returns each or-pattern alternative.
func (p *parser) parseStmtRawArms() ([]stmtRawArm, error) {
	pats, guard, err := p.parseArmPatterns()
	if err != nil {
		return nil, err
	}
	body, err := p.parseMatchStmtBody()
	if err != nil {
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
		out[i] = stmtRawArm{pat: pat, guard: g, body: b, altCont: i > 0}
	}
	return out, nil
}

func (p *parser) parseIfLet(ifPos ast.Position) (ast.Stmt, error) {
	p.advance()
	pats, err := p.parseOrPatterns()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, "="); err != nil {
		return nil, err
	}
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
	chained := false
	if _, ok := p.accept(lexer.Keyword, "else"); ok {
		chained = p.match(lexer.Keyword, "if")
		els, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		elseBlk = stmtAsBlock(els)
	}
	m := p.buildPatternBindingMatch(ifPos, pats, src, stmtAsBlock(then), elseBlk, ast.OriginIfLet)
	m.ElseChained = chained
	return m, nil
}

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
	return pats, nil
}

func (p *parser) buildPatternBindingMatch(pos ast.Position, pats []matchPattern, src ast.Expr, success, els *ast.Block, origin string) *ast.Match {
	raw := make([]stmtRawArm, 0, len(pats)+1)
	for i, pat := range pats {
		b := success
		if i > 0 {
			b = ast.CloneBlock(success)
		}
		raw = append(raw, stmtRawArm{pat: pat, body: b})
	}
	raw = append(raw, stmtRawArm{pat: matchPattern{P: pos, IsWildcard: true}, body: els})
	return &ast.Match{P: pos, Tag: src, Arms: flatStmtArms(raw), Origin: origin}
}

func stmtAsBlock(s ast.Stmt) *ast.Block {
	if b, ok := s.(*ast.Block); ok {
		return b
	}
	return &ast.Block{P: s.Pos(), Stmts: []ast.Stmt{s}}
}

func stmtArmFromPattern(pat matchPattern, guard ast.Expr, body *ast.Block) *ast.MatchArm {
	return &ast.MatchArm{
		P: pat.P, VariantName: pat.VariantName, VariantModule: pat.VariantModule,
		Bindings: pat.Bindings, NamedFields: pat.NamedFields, IsWildcard: pat.IsWildcard,
		Literal: pat.Literal, RangeHi: pat.RangeHi, RangeInclusive: pat.RangeInclusive,
		TupleElems: pat.TupleElems, AtBinding: pat.atBinding, FieldNames: pat.fieldNames,
		RestWritten: pat.restWritten,
		Payloads:    payloadElems(pat), Guard: guard, Body: body,
	}
}

func payloadElems(pat matchPattern) []*ast.TuplePatElem {
	if !pat.hasNestedSub() {
		return nil
	}
	out := make([]*ast.TuplePatElem, len(pat.subPats))
	for i, sp := range pat.subPats {
		if sp != nil {
			el := patElem(*sp)
			out[i] = &el
		}
	}
	return out
}

func patElem(pat matchPattern) ast.TuplePatElem {
	el := ast.TuplePatElem{AtBinding: pat.atBinding}
	switch {
	case pat.IsWildcard:
		el.IsWildcard = true
	case pat.TupleElems != nil:
		el.Nested = pat.TupleElems
	case pat.VariantName != "":
		el.VariantName = pat.VariantName
		el.VariantModule = pat.VariantModule
		el.VariantBindings = pat.Bindings
		el.VariantFieldNames = fieldNamesOf(pat)
		el.VariantPayloads = payloadElems(pat)
		el.RestWritten = pat.restWritten
	default:
		el.Literal = pat.Literal
		el.RangeHi = pat.RangeHi
		el.RangeInclusive = pat.RangeInclusive
	}
	return el
}

func (p *parser) freshDestructureName() string {
	n := p.nestN
	p.nestN++
	return fmt.Sprintf("__destruct_nest%d", n)
}

func fieldNamesOf(pat matchPattern) []string {
	if !pat.NamedFields {
		return nil
	}
	return append([]string{}, pat.fieldNames...)
}

func (p *parser) parseNamedFieldPattern() (out namedFieldPattern, ok bool, err error) {
	if _, isBrace := p.accept(lexer.Punct, "{"); !isBrace {
		return out, false, nil
	}
	if !p.match(lexer.Punct, "}") {
		for {
			if p.match(lexer.Punct, "..") {
				p.advance()
				out.rest = true
				break
			}
			fieldTok, err := p.expect(lexer.Ident, "")
			if err != nil {
				return out, false, err
			}
			bind := fieldTok.Text
			var sub *matchPattern
			if _, isRename := p.accept(lexer.Punct, ":"); isRename {
				if p.isNestedPatternStart() {
					sp, err := p.parseMatchPattern()
					if err != nil {
						return out, false, err
					}
					sub = &sp
					bind = ""
				} else {
					bindTok, err := p.expect(lexer.Ident, "")
					if err != nil {
						return out, false, err
					}
					bind = bindTok.Text
				}
			}
			out.fields = append(out.fields, fieldTok.Text)
			out.bindings = append(out.bindings, bind)
			out.subs = append(out.subs, sub)
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
		return out, false, e
	}
	return out, true, nil
}

type namedFieldPattern struct {
	fields   []string
	bindings []string
	subs     []*matchPattern
	rest     bool
}

type matchPattern struct {
	P              ast.Position
	VariantName    string
	VariantModule  string
	Bindings       []string
	NamedFields    bool
	IsWildcard     bool
	Literal        ast.Expr
	RangeHi        ast.Expr
	RangeInclusive bool
	TupleElems     []ast.TuplePatElem
	subPats        []*matchPattern
	atBinding      string
	fieldNames     []string
	restWritten    bool
}

func (mp *matchPattern) hasNestedSub() bool {
	for _, sp := range mp.subPats {
		if sp != nil {
			return true
		}
	}
	return false
}

func (p *parser) isNestedPatternStart() bool {
	t := p.peek()
	if t.Kind == lexer.Punct && t.Text == "(" {
		return true
	}
	if p.atLiteralPattern() {
		return true
	}
	if t.Kind == lexer.Ident && t.Text != "_" {
		n := p.peekAt(1)
		if n.Kind == lexer.Punct && (n.Text == "(" || n.Text == "{" || n.Text == "." || n.Text == "@") {
			return true
		}
	}
	return false
}

func (p *parser) tupleElemVariantStart() bool {
	n := p.peekAt(1)
	return n.Kind == lexer.Punct && (n.Text == "(" || n.Text == ".")
}

func (p *parser) parseTupleElemVariant(elem *ast.TuplePatElem) error {
	nameTok := p.peek()
	p.advance()
	elem.VariantName = nameTok.Text
	for p.match(lexer.Punct, ".") {
		p.advance()
		vt, err := p.expect(lexer.Ident, "")
		if err != nil {
			return err
		}
		if elem.VariantModule != "" {
			elem.VariantModule += "."
		}
		elem.VariantModule += elem.VariantName
		elem.VariantName = vt.Text
	}
	if _, err := p.expect(lexer.Punct, "("); err != nil {
		return err
	}
	if !p.match(lexer.Punct, ")") {
		for {
			if p.isNestedPatternStart() {
				sub, err := p.parseTuplePatElem()
				if err != nil {
					return err
				}
				elem.VariantBindings = append(elem.VariantBindings, "")
				elem.VariantPayloads = append(elem.VariantPayloads, &sub)
			} else {
				bt, err := p.expect(lexer.Ident, "")
				if err != nil {
					return err
				}
				elem.VariantBindings = append(elem.VariantBindings, bt.Text)
				elem.VariantPayloads = append(elem.VariantPayloads, nil)
			}
			if _, ok := p.accept(lexer.Punct, ","); ok {
				continue
			}
			break
		}
	}
	_, err := p.expect(lexer.Punct, ")")
	return err
}

func (p *parser) parseTuplePatElem() (ast.TuplePatElem, error) {
	if err := p.enter(); err != nil {
		return ast.TuplePatElem{}, err
	}
	defer p.leave()
	et := p.peek()
	var elem ast.TuplePatElem
	switch {
	case et.Kind == lexer.Ident && et.Text == "_":
		p.advance()
		elem.IsWildcard = true
	case et.Kind == lexer.Punct && et.Text == "(":
		nested, err := p.parseTuplePatElems()
		if err != nil {
			return elem, err
		}
		elem.Nested = nested
	case p.atLiteralPattern():
		lit, err := p.parseLiteralPattern()
		if err != nil {
			return elem, err
		}
		elem.Literal = lit
	case et.Kind == lexer.Ident && p.tupleElemVariantStart():
		if err := p.parseTupleElemVariant(&elem); err != nil {
			return elem, err
		}
	case et.Kind == lexer.Ident:
		p.advance()
		elem.Name = et.Text
	default:
		return elem, p.errorfCode(et.Pos, "P001", "expected a tuple element (a name, a literal, a variant, a nested tuple, or `_`), got %s", tokenDesc(et))
	}
	return elem, nil
}

func (p *parser) parseTuplePatElems() ([]ast.TuplePatElem, error) {
	open := p.peek().Pos
	p.advance()
	var elems []ast.TuplePatElem
	for {
		elem, err := p.parseTuplePatElem()
		if err != nil {
			return nil, err
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

func (p *parser) parseMatchPattern() (matchPattern, error) {
	if err := p.enter(); err != nil {
		return matchPattern{}, err
	}
	defer p.leave()
	t := p.peek()
	if t.Kind == lexer.Ident && t.Text != "_" &&
		p.peekAt(1).Kind == lexer.Punct && p.peekAt(1).Text == "@" {
		p.advance()
		p.advance()
		sub, err := p.parseMatchPattern()
		if err != nil {
			return sub, err
		}
		if sub.atBinding != "" {
			return sub, p.errorfCode(t.Pos, "P001", "an `@`-pattern cannot nest another `@` binding")
		}
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
		elems, err := p.parseTuplePatElems()
		if err != nil {
			return pat, err
		}
		pat.TupleElems = elems
	} else if p.atLiteralPattern() {
		lit, err := p.parseLiteralPattern()
		if err != nil {
			return pat, err
		}
		pat.Literal = lit
		if p.match(lexer.Punct, "..") || p.match(lexer.Punct, "..=") {
			_, inclusive := p.accept(lexer.Punct, "..=")
			if !inclusive {
				p.advance()
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
		for p.match(lexer.Punct, ".") {
			p.advance()
			nameTok, err := p.expect(lexer.Ident, "")
			if err != nil {
				return pat, err
			}
			if pat.VariantModule != "" {
				pat.VariantModule += "."
			}
			pat.VariantModule += pat.VariantName
			pat.VariantName = nameTok.Text
		}
		if _, ok := p.accept(lexer.Punct, "("); ok {
			if !p.match(lexer.Punct, ")") {
				for {
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
		} else if nf, ok, err := p.parseNamedFieldPattern(); err != nil {
			return pat, err
		} else if ok {
			pat.NamedFields = true
			pat.Bindings = nf.bindings
			pat.fieldNames = nf.fields
			pat.subPats = nf.subs
			pat.restWritten = nf.rest
		}
	} else {
		return pat, p.errorfCode(t.Pos, "P001", "expected a match arm pattern (a variant, a literal, or `_`), got %s", tokenDesc(t))
	}
	return pat, nil
}

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

func (p *parser) parseMatchExpr() (ast.Expr, error) {
	kw := p.advance()
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
	m.Arms = flatExprArms(raw)
	return m, nil
}

type exprRawArm struct {
	pat     matchPattern
	guard   ast.Expr
	body    ast.Expr
	altCont bool
}

func (p *parser) parseExprRawArms() ([]exprRawArm, error) {
	pats, guard, err := p.parseArmPatterns()
	if err != nil {
		return nil, err
	}
	var body ast.Expr
	if p.match(lexer.Punct, "{") {
		body, err = p.parseBranchBody()
	} else {
		body, err = p.parseExpr()
	}
	if err != nil {
		return nil, err
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
		out[i] = exprRawArm{pat: pat, guard: g, body: b, altCont: i > 0}
	}
	return out, nil
}

func exprArmFromPattern(pat matchPattern, guard, body ast.Expr) *ast.MatchExprArm {
	return &ast.MatchExprArm{
		P: pat.P, VariantName: pat.VariantName, VariantModule: pat.VariantModule,
		Bindings: pat.Bindings, NamedFields: pat.NamedFields, IsWildcard: pat.IsWildcard,
		Literal: pat.Literal, RangeHi: pat.RangeHi, RangeInclusive: pat.RangeInclusive,
		TupleElems: pat.TupleElems, AtBinding: pat.atBinding, FieldNames: pat.fieldNames,
		RestWritten: pat.restWritten,
		Payloads:    payloadElems(pat), Guard: guard, Body: body,
	}
}

func (p *parser) parseBreakContinue(isBreak bool) (ast.Stmt, error) {
	kw := p.advance()
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

func (p *parser) parseDefer() (ast.Stmt, error) {
	kw := p.advance()
	if p.match(lexer.Punct, "{") {
		block, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		return &ast.Defer{P: kw.Pos, Expr: &ast.BlockExpr{P: block.P, Stmts: block.Stmts}, OnError: kw.Text == "errdefer"}, nil
	}
	expr, err := p.parseAssign()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.Defer{P: kw.Pos, Expr: expr, OnError: kw.Text == "errdefer"}, nil
}

func (p *parser) parseAssert() (ast.Stmt, error) {
	kw := p.advance()
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

func (p *parser) parseTodo() (ast.Stmt, error) {
	kw := p.advance()
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
	return &ast.Block{P: pos, Stmts: []ast.Stmt{
		&ast.ExprStmt{P: pos, Expr: eprintCall},
		&ast.ExprStmt{P: pos, Expr: exitCall},
	}, IsTodo: true, TodoMsg: msg}, nil
}

func (p *parser) parseUse(parent *ast.Block) error {
	kw := p.advance()
	nameTok, err := p.expect(lexer.Ident, "")
	if err != nil {
		return err
	}
	var bindType ast.Type
	if _, ok := p.accept(lexer.Punct, ":"); ok {
		bindType, err = p.parseType()
		if err != nil {
			return err
		}
	}
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
	body := &ast.Block{P: kw.Pos}
	p.parseBlockStmts(body, false)
	if len(p.returnTypeStack) == 0 {
		return p.errorf(kw.Pos, "use must appear inside a function body")
	}
	rt := p.returnTypeStack[len(p.returnTypeStack)-1]

	p.useN++
	callbackName := fmt.Sprintf("__use_%d", p.useN)
	cb := &ast.FuncDecl{
		P:          kw.Pos,
		Name:       callbackName,
		Params:     []ast.Param{{Name: nameTok.Text, Type: bindType}},
		ReturnType: rt,
		Body:       body,
		IsLocal:    true,
		UseSource:  srcCall,
	}
	parent.Stmts = append(parent.Stmts, cb)
	srcCall.Args = append(srcCall.Args, &ast.Ident{P: kw.Pos, Name: callbackName})
	parent.Stmts = append(parent.Stmts, &ast.Return{P: kw.Pos, Value: srcCall})
	return nil
}

func (p *parser) parseLet(captureRest bool) (ast.Stmt, error) {
	kw := p.advance()
	qualifiedRecord := p.qualifiedRecordPatternAhead()
	if p.atLetBindingHead() && !qualifiedRecord {
		return p.parseLetBinding(kw.Pos)
	}
	start := p.i
	pats, err := p.parseOrPatterns()
	if err != nil {
		return nil, err
	}
	if p.i == start+1 && p.tokens[start].Kind == lexer.Ident {
		_, err = p.expectAs(lexer.Punct, "=", letNeedsEq)
	} else {
		_, err = p.expect(lexer.Punct, "=")
	}
	if err != nil {
		return nil, err
	}
	// The else keyword marks the end of the complete initializer expression.
	prevNS := p.noStructLit
	p.noStructLit = false
	src, err := p.parseExpr()
	p.noStructLit = prevNS
	if err != nil {
		return nil, err
	}
	if qualifiedRecord && len(pats) == 1 && !p.match(lexer.Keyword, "else") {
		d, err := p.irrefutableDestructure(kw.Pos, pats[0], destructureSite, "")
		if err != nil {
			return nil, err
		}
		d.AtName = pats[0].atBinding
		d.Init = src
		if _, err := p.expect(lexer.Punct, ";"); err != nil {
			return nil, err
		}
		return d, nil
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
		p.parseBlockStmts(rest, false)
	}
	return p.buildPatternBindingMatch(kw.Pos, pats, src, rest, elseBlk, ast.OriginLetElse), nil
}

func (p *parser) atLetBindingHead() bool {
	if p.atPatternHead() {
		return true
	}
	if p.peek().Kind != lexer.Ident || p.peekAt(1).Kind != lexer.Punct {
		return false
	}
	return p.peekAt(1).Text == ":" || p.peekAt(1).Text == "="
}

const letNeedsEq = `expected "=" after the name in this declaration`

func (p *parser) parseLetBinding(pos ast.Position) (ast.Stmt, error) {
	if p.atPatternHead() {
		return p.parseDestructure(pos)
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
	if _, err := p.expectAs(lexer.Punct, "=", letNeedsEq); err != nil {
		return nil, err
	}
	init, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.match(lexer.Keyword, "else") {
		return nil, p.errorf(p.peek().Pos, "`let %s` always binds, so it takes no `else`; a pattern that can miss goes after `let`, e.g. `let Some(%s) = …`", name.Text, name.Text)
	}
	if _, err := p.expect(lexer.Punct, ";"); err != nil {
		return nil, err
	}
	return &ast.Var{P: pos, Name: discardName(name.Text, pos, 0), Type: typ, Init: init, WasAnnotated: wasAnnotated}, nil
}

func (p *parser) atParamPattern() bool {
	if p.atPatternHead() {
		return true
	}
	i := 0
	if p.peek().Kind == lexer.Ident && p.peekAt(1).Kind == lexer.Punct && p.peekAt(1).Text == "@" {
		i = 2
	}
	if p.peekAt(i).Kind != lexer.Ident || p.peekAt(i+1).Kind != lexer.Punct {
		return false
	}
	switch p.peekAt(i + 1).Text {
	case "(", ".":
		return true
	}
	return false
}

func (p *parser) parseParamPattern() (ast.Param, *ast.Destructure, error) {
	pos := p.peek().Pos
	pat, err := p.parseMatchPattern()
	if err != nil {
		return ast.Param{}, nil, err
	}
	d, err := p.irrefutableDestructure(pos, pat, paramSite, "")
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
	holder := pat.atBinding
	if holder == "" {
		holder = fmt.Sprintf("__ptuple_%d_%d", pos.Line, pos.Col)
	}
	d.Init = &ast.Ident{P: pos, Name: holder}
	return ast.Param{Name: holder, NamePos: pos, Type: ptype, Pattern: d}, d, nil
}

func (p *parser) irrefutableDestructure(pos ast.Position, pat matchPattern, site, tag string) (*ast.Destructure, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	switch {
	case pat.TupleElems != nil:
		names := make([]string, len(pat.TupleElems))
		var nested []*ast.Destructure
		for i, el := range pat.TupleElems {
			switch {
			case el.IsWildcard:
				names[i] = "_"
			case el.Literal != nil:
				return nil, p.refutableBindErr(pos, "a literal tuple element", site)
			case el.VariantName != "":
				return nil, p.refutableBindErr(pos, "a variant tuple element", site)
			case el.Nested != nil:
				name := p.freshDestructureName()
				sub, err := p.irrefutableDestructure(pos, matchPattern{P: pos, TupleElems: el.Nested}, site, name)
				if err != nil {
					return nil, err
				}
				if nested == nil {
					nested = make([]*ast.Destructure, len(pat.TupleElems))
				}
				names[i] = name
				sub.Init = &ast.Ident{P: pos, Name: name}
				nested[i] = sub
			case el.Name != "":
				names[i] = el.Name
			default:
				return nil, p.errorfCode(pos, "P001", "unsupported tuple element in a %s pattern", site)
			}
		}
		return &ast.Destructure{P: pos, Names: discardNames(names, pos, tag), Nested: nested, Init: nil}, nil
	case pat.NamedFields:
		if pat.hasNestedSub() {
			return nil, p.refutableBindErr(pos, "a field sub-pattern", site)
		}
		if len(pat.fieldNames) == 0 {
			return nil, p.errorfCode(pos, "P001", "struct destructure needs at least one field")
		}
		name := pat.VariantName
		if pat.VariantModule != "" {
			name = pat.VariantModule + "." + name
		}
		return &ast.Destructure{P: pos, Names: discardNames(pat.Bindings, pos, tag), Fields: pat.fieldNames, StructName: name, RestWritten: pat.restWritten}, nil
	case pat.VariantName != "":
		return nil, p.refutableBindErr(pos, "an enum variant pattern", site)
	case pat.Literal != nil:
		return nil, p.refutableBindErr(pos, "a literal pattern", site)
	}
	return nil, p.refutableBindErr(pos, "this pattern", site)
}

func discardNames(names []string, pos ast.Position, tag string) []string {
	out := make([]string, len(names))
	for i, nm := range names {
		if nm == "_" && tag != "" {
			out[i] = fmt.Sprintf("__discard_%s_%d", strings.TrimPrefix(tag, "__"), i)
			continue
		}
		out[i] = discardName(nm, pos, i)
	}
	return out
}

const (
	paramSite       = "parameter"
	destructureSite = "destructuring binding"
	foreachSite     = "for-in binding"
)

func (p *parser) refutableBindErr(pos ast.Position, what, site string) error {
	return p.errorfCode(pos, "P001",
		"%s can fail to match, so it cannot be a %s pattern — there is no else branch here; destructure with a tuple `(a, b)` or struct `S { x, y }` pattern, or `match` on the value in the body", what, site)
}

func (p *parser) atPatternHead() bool {
	i := p.i
	if p.peek().Kind == lexer.Ident && p.peekAt(1).Kind == lexer.Punct && p.peekAt(1).Text == "@" {
		i += 2
	}
	if i < len(p.tokens) && p.tokens[i].Kind == lexer.Punct && p.tokens[i].Text == "(" {
		return true
	}
	return p.recordPatternBraceAt(i) >= 0
}

func (p *parser) recordPatternBraceAt(i int) int {
	if i >= len(p.tokens) || p.tokens[i].Kind != lexer.Ident {
		return -1
	}
	i++
	for i+1 < len(p.tokens) && p.tokens[i].Kind == lexer.Punct && p.tokens[i].Text == "." && p.tokens[i+1].Kind == lexer.Ident {
		i += 2
	}
	if i < len(p.tokens) && p.tokens[i].Kind == lexer.Punct && p.tokens[i].Text == "{" {
		return i
	}
	return -1
}

func (p *parser) qualifiedRecordPatternAhead() bool {
	i := p.i
	if p.peek().Kind == lexer.Ident && p.peekAt(1).Kind == lexer.Punct && p.peekAt(1).Text == "@" {
		i += 2
	}
	return p.recordPatternBraceAt(i) > i+1
}

func (p *parser) parseDestructure(kwPos ast.Position) (ast.Stmt, error) {
	pat, err := p.parseMatchPattern()
	if err != nil {
		return nil, err
	}
	d, err := p.irrefutableDestructure(kwPos, pat, destructureSite, "")
	if err != nil {
		return nil, err
	}
	d.AtName = pat.atBinding
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

func (p *parser) parseExpr() (ast.Expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	return p.parsePipe()
}

var compoundOps = map[string]string{
	"+=": "+", "-=": "-", "*=": "*", "/=": "/", "%=": "%",
	"&=": "&", "|=": "|", "^=": "^", "<<=": "<<", ">>=": ">>",
}

const chainedCompoundMessage = "a compound assignment stands alone: it cannot be part of an assignment chain"

func (p *parser) parseAssign() (ast.Expr, error) {
	return p.parseAssignChain(false)
}

func (p *parser) parseAssignChain(chained bool) (ast.Expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
	left, err := p.parsePipe()
	if err != nil {
		return nil, err
	}
	if eq, ok := p.accept(lexer.Punct, "="); ok {
		rhs, err := p.parseAssignChain(true)
		if err != nil {
			return nil, err
		}
		switch left.(type) {
		case *ast.Ident, *ast.Index, *ast.FieldAccess:
		default:
			return nil, p.errorfCode(eq.Pos, "P003", "left-hand side of assignment is not assignable")
		}
		return &ast.Assign{P: eq.Pos, Target: left, Value: rhs}, nil
	}
	if t := p.peek(); t.Kind == lexer.Punct {
		if op, ok := compoundOps[t.Text]; ok {
			if chained {
				return nil, p.errorfCode(t.Pos, "P001", chainedCompoundMessage)
			}
			tok := p.advance()
			rhs, err := p.parsePipe()
			if err != nil {
				return nil, err
			}
			if next := p.peek(); next.Kind == lexer.Punct {
				if _, compound := compoundOps[next.Text]; compound || next.Text == "=" {
					return nil, p.errorfCode(next.Pos, "P001", chainedCompoundMessage)
				}
			}
			switch left.(type) {
			case *ast.Ident, *ast.Index, *ast.FieldAccess:
			default:
				return nil, p.errorfCode(tok.Pos, "P003", "left-hand side of assignment is not assignable")
			}
			binary := &ast.Binary{P: tok.Pos, Op: op, Left: left, Right: rhs}
			return &ast.Assign{P: tok.Pos, Target: left, Value: binary}, nil
		}
	}
	return left, nil
}

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
				r.Args = append([]ast.Expr{left}, r.Args...)
				if r.ArgNames != nil {
					r.ArgNames = append([]string{""}, r.ArgNames...)
				}
			}
			r.IsPipe = true
			left = r
		default:
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
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()
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

func (p *parser) parseCast() (ast.Expr, error) {
	expr, err := p.parseCall()
	if err != nil {
		return nil, err
	}
	for p.match(lexer.Keyword, "as") {
		kw := p.advance()
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

func (p *parser) parseCallArgs() ([]ast.Expr, []string, error) {
	var args []ast.Expr
	var names []string
	anyNamed := false
	if p.match(lexer.Punct, ")") {
		return args, nil, nil
	}
	for {
		name := ""
		if p.peek().Kind == lexer.Ident && p.i+1 < len(p.tokens) &&
			p.tokens[p.i+1].Kind == lexer.Punct && p.tokens[p.i+1].Text == "=" {
			name = p.peek().Text
			p.advance()
			p.advance()
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

const closeCall = `expected ")" to close this call`

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
			if _, err := p.expectAs(lexer.Punct, ")", closeCall); err != nil {
				return nil, err
			}
			expr = &ast.Call{P: open.Pos, Callee: expr, Args: args, ArgNames: names}
			if db, err := p.maybeDesugarBuild(expr.(*ast.Call)); err != nil {
				return nil, err
			} else if db != nil {
				expr = db
			}
		case p.match(lexer.Punct, "["):
			if p.peekTypeArgs("(") {
				open := p.advance()
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
				if _, err := p.expectAs(lexer.Punct, ")", closeCall); err != nil {
					return nil, err
				}
				expr = &ast.Call{P: open.Pos, Callee: expr, Args: args, ArgNames: names, TypeArgs: typeArgs, TypeArgsWritten: true}
				continue
			}
			if name, pos, ok := structLitTypeName(expr); ok && !p.noStructLit && p.peekTypeArgs("{") {
				p.advance()
				typeArgs, err := p.parseTypeArgList()
				if err != nil {
					return nil, err
				}
				if expr, err = p.parseStructLit(pos, name, typeArgs); err != nil {
					return nil, err
				}
				continue
			}
			open := p.advance()
			if _, ok := p.accept(lexer.Punct, ":"); ok {
				var high ast.Expr
				if !p.match(lexer.Punct, "]") {
					h, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					high = h
				}
				if _, err := p.expectAs(lexer.Punct, "]", `expected "]" to close this slice`); err != nil {
					return nil, err
				}
				expr = &ast.SliceExpr{P: open.Pos, Source: expr, High: high}
				continue
			}
			first, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, ok := p.accept(lexer.Punct, ":"); ok {
				var high ast.Expr
				if !p.match(lexer.Punct, "]") {
					h, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					high = h
				}
				if _, err := p.expectAs(lexer.Punct, "]", `expected "]" to close this slice`); err != nil {
					return nil, err
				}
				expr = &ast.SliceExpr{P: open.Pos, Source: expr, Low: first, High: high}
				continue
			}
			if _, err := p.expectAs(lexer.Punct, "]", `expected "]" to close this index`); err != nil {
				return nil, err
			}
			expr = &ast.Index{P: open.Pos, Array: expr, Idx: first}
		case p.match(lexer.Punct, "."):
			dot := p.advance()
			if num := p.peek(); num.Kind == lexer.Number {
				numTok := p.advance()
				expr = &ast.FieldAccess{P: dot.Pos, Target: expr, Field: numTok.Text, FieldPos: numTok.Pos}
				continue
			}
			fname, err := p.expectAs(lexer.Ident, "", `expected a field or method name after "."`)
			if err != nil {
				return nil, err
			}
			if id, ok := expr.(*ast.Ident); ok && !p.noStructLit && p.match(lexer.Punct, "{") {
				if expr, err = p.parseStructLit(id.P, id.Name+"."+fname.Text, nil); err != nil {
					return nil, err
				}
				continue
			}
			expr = &ast.FieldAccess{P: dot.Pos, Target: expr, Field: fname.Text, FieldPos: fname.Pos}
		case p.match(lexer.Punct, "::"):
			return nil, p.errorfCode(p.peek().Pos, "P001", "`::` is not a path separator; write `.`")
		case p.match(lexer.Punct, "?"):
			tok := p.advance()
			expr = &ast.TryOp{P: tok.Pos, Inner: expr}
		default:
			return expr, nil
		}
	}
}

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

func (p *parser) parseStructLit(pos ast.Position, typeName string, typeArgs []ast.Type) (ast.Expr, error) {
	if _, err := p.expect(lexer.Punct, "{"); err != nil {
		return nil, err
	}
	var fields []ast.FieldInit
	var base ast.Expr
	if _, ok := p.accept(lexer.Punct, "..."); ok {
		b, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		base = b
		if _, ok := p.accept(lexer.Punct, ","); !ok {
			if _, err := p.expectAs(lexer.Punct, "}", `expected "}" to close this struct update`); err != nil {
				return nil, err
			}
			return &ast.StructLit{P: pos, TypeName: typeName, Base: base, TypeArgs: typeArgs, TypeArgsWritten: typeArgs != nil}, nil
		}
	}
	if !p.match(lexer.Punct, "}") {
		for {
			fname, err := p.expectAs(lexer.Ident, "", "expected a field name in this struct literal")
			if err != nil {
				return nil, err
			}
			if _, err := p.expectAs(lexer.Punct, ":", `expected ":" after the field name in this struct literal`); err != nil {
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
	if _, err := p.expectAs(lexer.Punct, "}", `expected "}" to close this struct literal`); err != nil {
		return nil, err
	}
	return &ast.StructLit{P: pos, TypeName: typeName, Fields: fields, Base: base, TypeArgs: typeArgs, TypeArgsWritten: typeArgs != nil}, nil
}

func (p *parser) maybeDesugarArrayBuild(call *ast.Call) (ast.Expr, error) {
	fa, ok := call.Callee.(*ast.FieldAccess)
	if !ok || fa.Field != "build" {
		return nil, nil
	}
	recv, ok := fa.Target.(*ast.Ident)
	if !ok || recv.Name != "Array" {
		return nil, nil
	}
	if len(call.Args) != 1 {
		return nil, p.errorf(call.P, "Array.build expects a single (b: ArrayBuilder[T]) => … argument")
	}
	lam, ok := call.Args[0].(*ast.Lambda)
	if !ok {
		return nil, p.errorf(call.P, "Array.build's argument must be a (b: ArrayBuilder[T]) => … lambda")
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

func isBuilderMutation(c *ast.Call, b string) bool {
	fa, ok := c.Callee.(*ast.FieldAccess)
	if !ok || (fa.Field != "append" && fa.Field != "with" && fa.Field != "insert") {
		return false
	}
	id, ok := fa.Target.(*ast.Ident)
	return ok && id.Name == b
}

func (p *parser) maybeDesugarBuild(call *ast.Call) (ast.Expr, error) {
	if e, err := p.maybeDesugarArrayBuild(call); err != nil || e != nil {
		return e, err
	}
	return p.maybeDesugarMapBuild(call)
}

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
		return nil, p.errorf(call.P, "Map.build expects a single (b: MapBuilder[K, V]) => … argument")
	}
	lam, ok := call.Args[0].(*ast.Lambda)
	if !ok {
		return nil, p.errorf(call.P, "Map.build's argument must be a (b: MapBuilder[K, V]) => … lambda")
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

var keywordModuleQualifier = map[string]bool{
	"string": true,
	"i32":    true,
	"i64":    true,
	"u32":    true,
	"u64":    true,
	"u8":     true,
	"f32":    true,
	"f64":    true,
	"f64x2":  true,
	"f32x4":  true,
	"usize":  true,
}

func (p *parser) parsePrimary() (ast.Expr, error) {
	t := p.peek()
	switch t.Kind {
	case lexer.Number:
		p.advance()
		digits, base := strings.ReplaceAll(t.Text, "_", ""), 10
		if len(digits) > 2 && digits[0] == '0' {
			switch digits[1] {
			case 'x', 'X':
				digits, base = digits[2:], 16
			case 'o', 'O':
				digits, base = digits[2:], 8
			case 'b', 'B':
				digits, base = digits[2:], 2
			}
		}
		spelled := base != 10 || strings.Contains(t.Text, "_")
		lit := &ast.NumberLit{P: t.Pos}
		v, err := strconv.ParseInt(digits, base, 64)
		if err != nil {
			if uv, uerr := strconv.ParseUint(digits, base, 64); uerr == nil {
				v = int64(uv)
				lit.ExceedsI64 = true
			} else if errors.Is(uerr, strconv.ErrRange) {
				v = 0
				lit.ExceedsU64 = true
				lit.Raw = t.Text
			} else {
				p.errors = append(p.errors, p.errorfCode(t.Pos, "P002", "invalid integer literal %q: %v", t.Text, err))
			}
		}
		lit.Value = v
		if spelled {
			lit.Raw = t.Text
		}
		switch t.Suffix {
		case "":
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
		case "usize":
			lit.Width = ast.WidthPtr
			lit.IsUnsigned = true
		default:
			return nil, p.errorfCode(t.Pos, "P002", "unexpected numeric suffix %q on integer literal", t.Suffix)
		}
		return lit, nil
	case lexer.Float:
		p.advance()
		f, err := strconv.ParseFloat(strings.ReplaceAll(t.Text, "_", ""), 64)
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
		if t.Suffix == "" {
			lit.Width = 0
		}
		return lit, nil
	case lexer.String:
		p.advance()
		return &ast.StringLit{P: t.Pos, Value: t.Text}, nil
	case lexer.Char, lexer.Byte:
		p.advance()
		return &ast.CharLit{P: t.Pos, Value: int64(t.Scalar), Raw: t.Text, IsByte: t.Kind == lexer.Byte}, nil
	case lexer.FString:
		p.advance()
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
			return p.parseIfExpr()
		case "match":
			return p.parseMatchExpr()
		case "function":
			return nil, p.errorfCode(t.Pos, "P006",
				"`function` introduces a named declaration; write an anonymous function as an arrow lambda — `(x: T): R => …`")
		}
		if keywordModuleQualifier[t.Text] &&
			p.i+1 < len(p.tokens) &&
			p.tokens[p.i+1].Kind == lexer.Punct && (p.tokens[p.i+1].Text == "." || p.tokens[p.i+1].Text == "::") {
			p.advance()
			return &ast.Ident{P: t.Pos, Name: t.Text}, nil
		}
	case lexer.Ident:
		p.advance()
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
			if p.looksLikeArrowLambda() {
				return p.parseArrowLambda()
			}
			open := p.advance()
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
					break
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
			if _, err := p.expectAs(lexer.Punct, "]", `expected "]" to close this array literal`); err != nil {
				return nil, err
			}
			return &ast.ArrayLit{P: open.Pos, Elems: elems}, nil
		case "{":
			if !p.noStructLit {
				return p.parseBranchBody()
			}
		}
	}
	return nil, p.errorfCode(t.Pos, "P001", "expected an expression, got %s", tokenDesc(t))
}
