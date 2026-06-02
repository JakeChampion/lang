// Package literate implements Knuth-style literate programming for
// Fern. A literate program is a Markdown document (`.fern.md`) whose
// prose explains the code to a human, with the code itself living in
// named "chunks" embedded in fenced ```fern blocks. The chunks may be
// presented in whatever order best explains the ideas; `Tangle`
// reassembles them into a compilable Fern program, and `Weave`
// renders a cross-referenced reading document.
//
// # Chunk syntax
//
// Code lives in fenced code blocks tagged `fern`:
//
//	```fern
//	<<*>>=
//	import "std/io";
//	<<helpers>>
//	fn main() { io.println(greeting()); }
//	```
//
// A block whose first non-blank line matches `<<NAME>>=` is a *chunk
// definition*; NAME names the chunk and the remaining lines are its
// body. The root chunk is named `*` — `Tangle` expands `<<*>>` to
// produce the program. A ```fern block with no `<<NAME>>=` header is
// *display-only*: it is woven into the document but never tangled, so
// you can show illustrative snippets that aren't part of the build.
//
// Inside a chunk body, a line whose trimmed content is exactly
// `<<NAME>>` (no trailing `=`) is a *chunk reference*: tangling
// replaces it with the expansion of NAME, prefixing every expanded
// line with the reference line's own indentation (so a chunk pulled
// into an indented context stays aligned — classic noweb behaviour).
//
// Defining the same NAME more than once *appends* to the chunk in
// document order, which is how you grow a definition across the
// narrative ("⟨handlers⟩+≡").
//
// # Provenance
//
// Tangle returns, alongside the generated source, a per-line
// provenance map ([]Line) recording which `.fern.md` line each
// generated line came from and how much indentation tangling added.
// The CLI feeds that map to diag.FormatRemapped so a type error in
// generated code points at the line the author actually wrote.
package literate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// RootChunk is the name of the chunk Tangle expands to produce the
// program — Knuth/noweb's `<<*>>`.
const RootChunk = "*"

// Line records where one line of tangled output came from in the
// original literate document. Lit is the 1-based line number in the
// `.fern.md` source; ColShift is the number of indentation characters
// tangling prepended (so the original column C maps to tangled column
// C+ColShift, and the inverse remap subtracts ColShift).
type Line struct {
	Lit      int
	ColShift int
}

// Error is a positioned literate-processing error (undefined chunk,
// cyclic reference, missing root). It satisfies diag.Positioned so the
// CLI renders it against the `.fern.md` source with a caret, exactly
// like a parser or checker error. Pos is in literate-document
// coordinates.
type Error struct {
	Pos ast.Position
	Msg string
}

func (e *Error) Error() string          { return e.Msg }
func (e *Error) Position() ast.Position { return e.Pos }

// piece is one definition of a chunk (one `<<NAME>>=` block). A chunk
// defined N times across the document has N pieces, concatenated in
// document order during expansion.
type piece struct {
	body []bodyLine // lines after the `<<NAME>>=` header
}

// bodyLine is a single physical line inside a chunk body, tagged with
// its 1-based line number in the `.fern.md` source.
type bodyLine struct {
	text    string
	litLine int
}

// chunkDef collects every piece of a named chunk plus the line of its
// first definition (used to order chunks in the woven output).
type chunkDef struct {
	name      string
	pieces    []piece
	firstLine int
}

// block is one top-level region of the document: either prose (Markdown
// to pass through to weave, ignored by tangle) or a fenced ```fern code
// block. A fern block is a chunk definition when chunk != "", or a
// file-root when file != "".
type block struct {
	isFern  bool
	chunk   string     // chunk name if this fern block is a `<<name>>=` definition
	file    string     // output path if this fern block is a `file=PATH` root
	lines   []bodyLine // body lines (fern blocks only): for a definition, the lines after the header
	rawText string     // verbatim text including fences (prose + display-only fern), for weave
}

// fileRoot is a tangle output target named by a `file=PATH` fence
// directive. Its body is expanded like a root chunk to produce PATH's
// source. Multiple `file=PATH` blocks with the same path concatenate in
// document order (like same-name chunk pieces).
type fileRoot struct {
	path      string
	body      []bodyLine
	isEntry   bool
	firstLine int
}

// Document is a parsed literate program: the ordered blocks (for weave),
// the chunk table (for tangle), and the file-root table (for multi-file
// tangle).
type Document struct {
	blocks []block
	chunks map[string]*chunkDef
	// order preserves the document order in which chunk names were
	// first defined, so weave and "defined chunks" listings are stable.
	order []string
	// fileIndex accumulates file-root bodies per output path; fileOrder
	// preserves first-definition order for stable multi-file output.
	fileIndex map[string]*fileRoot
	fileOrder []string
}

// Parse reads literate source (the contents of a `.fern.md` file) into
// a Document. It never fails on prose — any text outside a ```fern
// block is carried through verbatim. Malformed chunk structure is
// reported lazily by Tangle (undefined / cyclic references) rather
// than here, so Weave works even on an in-progress document.
func Parse(src string) *Document {
	doc := &Document{chunks: map[string]*chunkDef{}, fileIndex: map[string]*fileRoot{}}
	lines := strings.Split(src, "\n")
	i := 0
	n := len(lines)
	for i < n {
		line := lines[i]
		if fence, lang, info, ok := openingFence(line); ok {
			// Collect the fenced block up to the matching closing
			// fence (a line whose trimmed text is the same fence run).
			start := i
			fenceLine := i + 1 // 1-based line of the opening fence
			i++
			var body []bodyLine
			for i < n && !isClosingFence(lines[i], fence) {
				body = append(body, bodyLine{text: lines[i], litLine: i + 1})
				i++
			}
			closed := i < n
			if closed {
				i++ // consume the closing fence
			}
			raw := strings.Join(lines[start:i], "\n")
			if lang == "fern" {
				if file, isEntry := parseFenceDirectives(info); file != "" {
					doc.addFileBlock(file, isEntry, body, raw, fenceLine)
				} else {
					doc.addFernBlock(body, raw)
				}
			} else {
				doc.blocks = append(doc.blocks, block{rawText: raw})
			}
			continue
		}
		// Prose line: accumulate a run of consecutive prose lines into
		// one block so weave can emit it as a paragraph region.
		start := i
		for i < n && !isFenceLine(lines[i]) {
			i++
		}
		doc.blocks = append(doc.blocks, block{rawText: strings.Join(lines[start:i], "\n")})
	}
	return doc
}

// addFernBlock classifies a fenced fern block as a chunk definition
// (first non-blank line is `<<NAME>>=`) or display-only, recording it
// for both tangle and weave.
func (doc *Document) addFernBlock(body []bodyLine, raw string) {
	headerIdx, name, ok := chunkHeader(body)
	if !ok {
		// Display-only illustrative snippet: woven, not tangled.
		doc.blocks = append(doc.blocks, block{isFern: true, rawText: raw})
		return
	}
	def := body[headerIdx+1:]
	cd := doc.chunks[name]
	if cd == nil {
		cd = &chunkDef{name: name, firstLine: body[headerIdx].litLine}
		doc.chunks[name] = cd
		doc.order = append(doc.order, name)
	}
	cd.pieces = append(cd.pieces, piece{body: def})
	doc.blocks = append(doc.blocks, block{isFern: true, chunk: name, lines: def, rawText: raw})
}

// addFileBlock records a `file=PATH` fern block as (part of) the
// file-root for PATH. Unlike a chunk definition there is no `<<name>>=`
// header — the whole block body is the file's content (which may itself
// contain `<<ref>>` lines). Repeated PATHs concatenate in document order.
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
	doc.blocks = append(doc.blocks, block{isFern: true, file: path, lines: body, rawText: raw})
}

// Tangle expands the root chunk into compilable Fern source and returns
// the generated text plus a per-line provenance map (lineMap[i] is the
// origin of the (i+1)-th generated line). It errors when the root chunk
// is undefined, a referenced chunk doesn't exist, or chunk references
// form a cycle.
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

// TangleChunk expands a single named chunk (rather than the `<<*>>`
// root) into its source plus a provenance map — for extracting or
// inspecting one chunk in isolation. Errors when the chunk is undefined,
// or when its expansion hits an undefined / cyclic reference.
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

// emitFn receives one expanded line plus its document-line provenance.
type emitFn func(text string, litLine, colShift int)

// collect runs an expansion, accumulating the emitted lines and their
// provenance map. Shared by single-output Tangle (a root chunk) and
// per-file TangleFiles (a file-root body).
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

// expandChunk recursively writes the expansion of chunk `name` through
// `emit`, prefixing every emitted line with `indent`. `active` is the
// set of chunks currently being expanded, for cycle detection.
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

// expandBody expands one list of body lines: literal lines are emitted
// (with `indent` prepended), and `<<ref>>` lines recurse into the
// referenced chunk. Shared by chunk expansion and file-root expansion.
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
		emit(indent+deEscapeRef(bl.text), bl.litLine, len(indent))
	}
	return nil
}

// chunkHeader returns the index of the `<<NAME>>=` header line within a
// fern block body (skipping leading blank lines), the chunk name, and
// whether a header was found. A block with no header is display-only.
func chunkHeader(body []bodyLine) (int, string, bool) {
	for i, bl := range body {
		t := strings.TrimSpace(bl.text)
		if t == "" {
			continue
		}
		if name, ok := parseDefHeader(t); ok {
			return i, name, true
		}
		return 0, "", false // first non-blank line isn't a header
	}
	return 0, "", false
}

// parseDefHeader matches a chunk-definition header `<<NAME>>=` and
// returns the trimmed NAME.
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

// chunkRef matches a chunk-reference line `<<NAME>>` (optionally
// indented) and returns the leading indentation, the trimmed NAME, and
// whether the line is a reference. A line ending in `>>=` is a
// definition header, not a reference.
func chunkRef(text string) (indent, name string, ok bool) {
	trimmed := strings.TrimSpace(text)
	// A leading backslash escapes the marker: `\<<name>>` is a literal,
	// not a reference. deEscapeRef strips the backslash at emit time.
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

// deEscapeRef strips a single backslash that escapes a chunk marker —
// `\<<name>>` becomes the literal `<<name>>` — preserving indentation.
// A line that doesn't begin (after indentation) with `\<<` is returned
// unchanged, so it is safe to apply to every emitted literal line.
func deEscapeRef(text string) string {
	i := len(text) - len(strings.TrimLeft(text, " \t"))
	if strings.HasPrefix(text[i:], `\<<`) {
		return text[:i] + text[i+1:]
	}
	return text
}

// openingFence reports whether line opens a fenced code block and, if
// so, returns the fence run (``` or longer), the lowercased language
// token, and the full (case-preserved) info string after the backticks
// — the latter carries directives like `file=util.fern`.
func openingFence(line string) (fence, lang, info string, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "```") {
		return "", "", "", false
	}
	// The fence is the leading run of backticks; the info string is
	// whatever follows on the same line.
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

// parseFenceDirectives extracts the multi-file directives from a fern
// fence's info string: `file=PATH` marks the block as a tangle output
// file (its body is a file-root), and a bare `entry` token marks that
// file as the program's compile entry. The leading language token and
// any other words are ignored. PATH may be quoted.
func parseFenceDirectives(info string) (file string, isEntry bool) {
	for _, f := range strings.Fields(info) {
		switch {
		case strings.HasPrefix(f, "file="):
			file = strings.Trim(strings.TrimPrefix(f, "file="), "\"")
		case f == "entry":
			isEntry = true
		}
	}
	return file, isEntry
}

// isFenceLine reports whether line opens (or closes) any fenced block.
func isFenceLine(line string) bool {
	_, _, _, ok := openingFence(line)
	return ok
}

// isClosingFence reports whether line closes a block opened with
// `fence` — a line whose only content is a backtick run at least as
// long as the opening fence.
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

// DefinedChunks returns the chunk names defined in the document, in the
// order they were first defined. Used by tooling and tests.
func (doc *Document) DefinedChunks() []string {
	out := make([]string, len(doc.order))
	copy(out, doc.order)
	return out
}

// usedIn returns, for each chunk, the sorted set of chunk names whose
// bodies reference it — the "⟨X⟩ is used in ⟨Y⟩" cross-reference weave
// annotates definitions with.
func (doc *Document) usedIn() map[string][]string {
	users := map[string]map[string]bool{}
	for _, cd := range doc.chunks {
		for _, p := range cd.pieces {
			for _, bl := range p.body {
				if _, ref, ok := chunkRef(bl.text); ok {
					if users[ref] == nil {
						users[ref] = map[string]bool{}
					}
					users[ref][cd.name] = true
				}
			}
		}
	}
	out := map[string][]string{}
	for ref, set := range users {
		names := make([]string, 0, len(set))
		for n := range set {
			names = append(names, n)
		}
		sort.Strings(names)
		out[ref] = names
	}
	return out
}
