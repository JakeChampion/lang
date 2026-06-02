package lsp

import (
	"strings"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/literate"
)

// isLiterateURI reports whether uri names a literate Fern document
// (`.fern.md`), whose `fern` chunks are tangled before compilation.
func isLiterateURI(uri string) bool {
	return strings.HasSuffix(uri, ".fern.md")
}

// updateLiterateDoc diagnoses a literate document: it tangles the
// `.fern.md` source, type-checks the generated Fern, and remaps the
// resulting diagnostics back onto the document's own lines (so squiggles
// land where the author typed, not in the hidden tangled intermediate).
//
// Diagnostics are remapped onto the document; the tangled program and
// the bidirectional line maps are kept in state.lit so the cursor-driven
// features (hover, definition, references, completion, signature) can
// query the generated AST and translate positions both ways. Tangle
// errors (missing root, undefined / cyclic chunk) already carry document
// positions and surface as ordinary diagnostics.
func (s *Server) updateLiterateDoc(uri, src string) []string {
	doc := literate.Parse(src)

	// Multi-file (`file=`) documents tangle to several modules; the
	// in-editor single-document diagnostics slice doesn't cover them
	// yet, so publish a clean slate rather than a spurious tangle error.
	if doc.HasFiles() {
		s.docs[uri] = &docState{src: src, diags: []Diagnostic{}}
		return nil
	}

	code, lineMap, terr := doc.Tangle()
	if terr != nil {
		s.docs[uri] = &docState{src: src, diags: toDiagnostics(terr)}
		return nil
	}

	prog, perr := parseFor(code)
	var info *checker.Info
	var checkErr error
	if prog != nil {
		info, checkErr = checker.Check(prog)
	}
	diags := remapLiterateDiagnostics(collectDiagnostics(perr, checkErr), lineMap)

	// Keep the tangled products + the bidirectional line maps in a side
	// channel (state.lit). The top-level prog/info stay nil so features
	// that aren't literate-aware yet (semantic tokens, inlay hints,
	// document symbols, rename) stay inert rather than returning results
	// in tangled coordinates the editor would misplace; the cursor-driven
	// features (hover, definition, references, completion, signature)
	// consult state.lit and remap positions both ways.
	s.docs[uri] = &docState{
		src:   src,
		diags: diags,
		lit: &literateDoc{
			tangled: &docState{src: code, prog: prog, info: info},
			lineMap: lineMap,
			reverse: buildReverseLineMap(lineMap),
		},
	}
	return nil
}

// literateDoc holds what a `.fern.md`'s cursor-driven LSP features need:
// the tangled program (queried in generated coordinates) plus the maps
// to translate positions document→generated (reverse) and back (lineMap).
type literateDoc struct {
	tangled *docState
	lineMap []literate.Line
	reverse map[int]reverseEntry
}

// reverseEntry maps a document line to its first occurrence in the
// tangled source: the generated line and the indentation tangling added.
type reverseEntry struct {
	line     int // 1-based generated line
	colShift int
}

// buildReverseLineMap inverts the tangle line map to document-line →
// first generated position. A chunk used more than once produces several
// generated lines for one document line; the first is the canonical
// query target (the symbol is identical at each).
func buildReverseLineMap(lineMap []literate.Line) map[int]reverseEntry {
	m := make(map[int]reverseEntry, len(lineMap))
	for i, ln := range lineMap {
		if _, ok := m[ln.Lit]; !ok {
			m[ln.Lit] = reverseEntry{line: i + 1, colShift: ln.ColShift}
		}
	}
	return m
}

// toTangled maps a 0-based document position to its position in the
// generated source, or ok=false when the document line contributes no
// tangled output (prose, a chunk header / reference line, an unused
// chunk) — in which case there is nothing to query.
func (l *literateDoc) toTangled(p Position) (Position, bool) {
	e, ok := l.reverse[p.Line+1]
	if !ok {
		return p, false
	}
	return Position{Line: e.line - 1, Character: p.Character + e.colShift}, true
}

// toDocRange maps a generated-source range back onto the document.
func (l *literateDoc) toDocRange(r Range) Range {
	return Range{
		Start: remapLiteratePosition(r.Start, l.lineMap),
		End:   remapLiteratePosition(r.End, l.lineMap),
	}
}

// remapLiterateDiagnostics rewrites each diagnostic's range from
// generated-source coordinates back to the document, using the tangle
// line map. Ranges outside the map (rare — e.g. a synthesized position)
// pass through unchanged.
func remapLiterateDiagnostics(diags []Diagnostic, lineMap []literate.Line) []Diagnostic {
	for i := range diags {
		diags[i].Range.Start = remapLiteratePosition(diags[i].Range.Start, lineMap)
		diags[i].Range.End = remapLiteratePosition(diags[i].Range.End, lineMap)
	}
	return diags
}

// remapLiteratePosition maps one 0-based LSP position in the tangled
// source to its origin in the `.fern.md`: the line comes from the map's
// Lit entry, and the character is shifted back by the indentation
// tangling prepended (ColShift).
func remapLiteratePosition(p Position, lineMap []literate.Line) Position {
	if p.Line < 0 || p.Line >= len(lineMap) {
		return p
	}
	m := lineMap[p.Line]
	line := m.Lit - 1
	if line < 0 {
		line = 0
	}
	ch := p.Character - m.ColShift
	if ch < 0 {
		ch = 0
	}
	return Position{Line: line, Character: ch}
}
