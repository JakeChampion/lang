package literate

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

const RootChunk = "*"

type Line struct {
	Lit      int
	ColShift int
}

type Error struct {
	Pos ast.Position
	Msg string
}

func (e *Error) Error() string          { return e.Msg }
func (e *Error) Position() ast.Position { return e.Pos }
func (e *Error) Code() string           { return "E092" }

type piece struct {
	body []bodyLine
}

type bodyLine struct {
	text    string
	litLine int
}

type chunkDef struct {
	name      string
	pieces    []piece
	firstLine int
}

type block struct {
	isFern  bool
	chunk   string
	file    string
	rawText string
}

type fileRoot struct {
	path      string
	body      []bodyLine
	isEntry   bool
	firstLine int
}

type Document struct {
	blocks    []block
	chunks    map[string]*chunkDef
	fileIndex map[string]*fileRoot
	fileOrder []string
}

func Parse(src string) *Document {
	doc := &Document{chunks: map[string]*chunkDef{}, fileIndex: map[string]*fileRoot{}}
	lines := strings.Split(src, "\n")
	srcEndsNL := len(lines) > 1 && lines[len(lines)-1] == ""
	i := 0
	n := len(lines)
	for i < n {
		line := lines[i]
		if fence, lang, info, ok := openingFence(line); ok {
			start := i
			fenceLine := i + 1
			i++
			var body []bodyLine
			for i < n && !isClosingFence(lines[i], fence) {
				body = append(body, bodyLine{text: lines[i], litLine: i + 1})
				i++
			}
			closed := i < n
			if closed {
				i++
			}
			raw := strings.Join(lines[start:i], "\n")
			if !closed && srcEndsNL && len(body) > 0 && body[len(body)-1].text == "" {
				body = body[:len(body)-1]
			}
			if lang == "fern" {
				switch d := parseFenceDirectives(info); {
				case d.file != "":
					doc.addFileBlock(d.file, d.entry, body, raw, fenceLine)
				case d.test:
					doc.blocks = append(doc.blocks, block{isFern: true, rawText: raw})
				default:
					doc.addFernBlock(body, raw)
				}
			} else {
				doc.blocks = append(doc.blocks, block{rawText: raw})
			}
			continue
		}
		start := i
		for i < n && !isFenceLine(lines[i]) {
			i++
		}
		doc.blocks = append(doc.blocks, block{rawText: strings.Join(lines[start:i], "\n")})
	}
	return doc
}

func (doc *Document) addFernBlock(body []bodyLine, raw string) {
	headerIdx, name, ok := chunkHeader(body)
	if !ok {
		doc.blocks = append(doc.blocks, block{isFern: true, rawText: raw})
		return
	}
	def := body[headerIdx+1:]
	cd := doc.chunks[name]
	if cd == nil {
		cd = &chunkDef{name: name, firstLine: body[headerIdx].litLine}
		doc.chunks[name] = cd
	}
	cd.pieces = append(cd.pieces, piece{body: def})
	doc.blocks = append(doc.blocks, block{isFern: true, chunk: name, rawText: raw})
}

func (doc *Document) addFileBlock(path string, isEntry bool, body []bodyLine, raw string, firstLine int) {
	fr := doc.fileIndex[path]
	if fr == nil {
		fr = &fileRoot{path: path, firstLine: firstLine}
		doc.fileIndex[path] = fr
		doc.fileOrder = append(doc.fileOrder, path)
	}
	fr.body = append(fr.body, body...)
	if isEntry {
		fr.isEntry = true
	}
	doc.blocks = append(doc.blocks, block{isFern: true, file: path, rawText: raw})
}

func (doc *Document) Tangle() (string, []Line, error) {
	if _, ok := doc.chunks[RootChunk]; !ok {
		return "", nil, &Error{
			Pos: ast.Position{Line: 1, Col: 1},
			Msg: fmt.Sprintf("literate: no root chunk %q defined (every literate program needs a `<<%s>>=` block to tangle from)", "<<"+RootChunk+">>", RootChunk),
		}
	}
	out, lineMap, err := collect(func(emit emitFn) error {
		return doc.expandChunk(RootChunk, "", map[string]bool{}, emit)
	})
	if err != nil {
		return "", nil, err
	}
	return strings.Join(out, "\n"), lineMap, nil
}

func (doc *Document) TangleChunk(name string) (string, []Line, error) {
	if _, ok := doc.chunks[name]; !ok {
		return "", nil, &Error{
			Pos: ast.Position{Line: 1, Col: 1},
			Msg: fmt.Sprintf("literate: no chunk %q defined", "<<"+name+">>"),
		}
	}
	out, lineMap, err := collect(func(emit emitFn) error {
		return doc.expandChunk(name, "", map[string]bool{}, emit)
	})
	if err != nil {
		return "", nil, err
	}
	return strings.Join(out, "\n"), lineMap, nil
}

type emitFn func(text string, litLine, colShift int)

func collect(run func(emit emitFn) error) ([]string, []Line, error) {
	var out []string
	var lineMap []Line
	emit := func(text string, litLine, colShift int) {
		out = append(out, text)
		lineMap = append(lineMap, Line{Lit: litLine, ColShift: colShift})
	}
	if err := run(emit); err != nil {
		return nil, nil, err
	}
	return out, lineMap, nil
}

func (doc *Document) expandChunk(name, indent string, active map[string]bool, emit emitFn) error {
	if active[name] {
		return &Error{
			Pos: ast.Position{Line: doc.chunks[name].firstLine, Col: 1},
			Msg: fmt.Sprintf("literate: chunk %q is defined in terms of itself (cyclic chunk reference)", "<<"+name+">>"),
		}
	}
	active[name] = true
	defer delete(active, name)

	for _, p := range doc.chunks[name].pieces {
		if err := doc.expandBody(p.body, indent, active, emit); err != nil {
			return err
		}
	}
	return nil
}

func (doc *Document) expandBody(body []bodyLine, indent string, active map[string]bool, emit emitFn) error {
	for _, bl := range body {
		if refIndent, refName, isRef := chunkRef(bl.text); isRef {
			if _, ok := doc.chunks[refName]; !ok {
				col := strings.Index(bl.text, "<<") + 1
				return &Error{
					Pos: ast.Position{Line: bl.litLine, Col: col},
					Msg: fmt.Sprintf("literate: reference to undefined chunk %q", "<<"+refName+">>"),
				}
			}
			if err := doc.expandChunk(refName, indent+refIndent, active, emit); err != nil {
				return err
			}
			continue
		}
		deEscaped := deEscapeRef(bl.text)
		colShift := len(indent) - (len(bl.text) - len(deEscaped))
		emit(indent+deEscaped, bl.litLine, colShift)
	}
	return nil
}

func chunkHeader(body []bodyLine) (int, string, bool) {
	for i, bl := range body {
		t := strings.TrimSpace(bl.text)
		if t == "" {
			continue
		}
		if name, ok := parseDefHeader(t); ok {
			return i, name, true
		}
		return 0, "", false
	}
	return 0, "", false
}

func parseDefHeader(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, "<<") || !strings.HasSuffix(trimmed, ">>=") {
		return "", false
	}
	inner := trimmed[2 : len(trimmed)-len(">>=")]
	if strings.Contains(inner, ">>") {
		return "", false
	}
	name := strings.TrimSpace(inner)
	if name == "" {
		return "", false
	}
	return name, true
}

func chunkRef(text string) (indent, name string, ok bool) {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, `\<<`) {
		return "", "", false
	}
	if !strings.HasPrefix(trimmed, "<<") || !strings.HasSuffix(trimmed, ">>") {
		return "", "", false
	}
	inner := trimmed[2 : len(trimmed)-2]
	if inner == "" || strings.Contains(inner, "<<") || strings.Contains(inner, ">>") {
		return "", "", false
	}
	indent = text[:len(text)-len(strings.TrimLeft(text, " \t"))]
	return indent, strings.TrimSpace(inner), true
}

func deEscapeRef(text string) string {
	i := len(text) - len(strings.TrimLeft(text, " \t"))
	if strings.HasPrefix(text[i:], `\<<`) {
		return text[:i] + text[i+1:]
	}
	return text
}

func openingFence(line string) (fence, lang, info string, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "```") {
		return "", "", "", false
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == '`' {
		n++
	}
	fence = trimmed[:n]
	info = strings.TrimSpace(trimmed[n:])
	lang = info
	if sp := strings.IndexAny(info, " \t"); sp >= 0 {
		lang = info[:sp]
	}
	return fence, strings.ToLower(lang), info, true
}

type fenceDirectives struct {
	file  string
	entry bool
	test  bool
}

func parseFenceDirectives(info string) (d fenceDirectives) {
	for _, f := range strings.Fields(info) {
		switch {
		case strings.HasPrefix(f, "file="):
			d.file = strings.Trim(strings.TrimPrefix(f, "file="), "\"")
		case f == "entry":
			d.entry = true
		case f == "test":
			d.test = true
		}
	}
	return d
}

func isFenceLine(line string) bool {
	_, _, _, ok := openingFence(line)
	return ok
}

func isClosingFence(line, fence string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		if r != '`' {
			return false
		}
	}
	return len(trimmed) >= len(fence)
}
