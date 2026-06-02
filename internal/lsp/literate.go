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
// Only diagnostics are produced — prog / info are intentionally left nil
// so position-sensitive features (hover, go-to-definition, completion)
// stay inert for `.fern.md` rather than returning results in tangled
// coordinates that the editor would misplace. Tangle errors (missing
// root, undefined / cyclic chunk) already carry document positions and
// surface as ordinary diagnostics.
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
	var checkErr error
	if prog != nil {
		_, checkErr = checker.Check(prog)
	}
	diags := remapLiterateDiagnostics(collectDiagnostics(perr, checkErr), lineMap)
	s.docs[uri] = &docState{src: src, diags: diags}
	return nil
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
