package lsp

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// inlayHintParams is the textDocument/inlayHint request payload.
// The range bounds which part of the document the editor wants
// hints for — typically the viewport.
type inlayHintParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Range Range `json:"range"`
}

// inlayHint is the LSP response item. We only emit Type hints
// (kind=1); Parameter hints would need a way to name positional
// args, which lang's syntax doesn't surface in calls.
type inlayHint struct {
	Position     Position `json:"position"`
	Label        string   `json:"label"`
	Kind         int      `json:"kind,omitempty"` // 1 = Type, 2 = Parameter
	PaddingLeft  bool     `json:"paddingLeft,omitempty"`
	PaddingRight bool     `json:"paddingRight,omitempty"`
}

const inlayHintKindType = 1

// runInlayHints walks the cached AST + checker info for var
// declarations whose Type is nil (un-annotated, `var x = 7`) and
// emits a ghost ": Type" right after the name. Only hints from the
// requested document whose position falls inside the requested LSP
// range are returned — editors only ask about the visible viewport,
// and a workspace program's Locals table spans every module.
func runInlayHints(state *docState, uri string, rng Range) []inlayHint {
	if state == nil || state.prog == nil || state.info == nil {
		return []inlayHint{}
	}
	mod := requestModule(uri)
	out := []inlayHint{}
	for fd, vars := range state.info.Locals {
		if fd == nil {
			continue
		}
		if !inModule(fd.SourceModule, mod) {
			continue
		}
		for _, v := range vars {
			if v.WasAnnotated {
				continue // user wrote a type; nothing to ghost
			}
			t, ok := state.info.VarTypes[v]
			if !ok || t == nil {
				t = v.Type // checker may have stamped it
			}
			if t == nil {
				continue
			}
			endPos, ok := varNameEndPos(state.src, v)
			if !ok {
				continue
			}
			lspPos := toLSPPosition(endPos)
			if !inRange(lspPos, rng) {
				continue
			}
			out = append(out, inlayHint{
				Position:    lspPos,
				Label:       ": " + typeString(t),
				Kind:        inlayHintKindType,
				PaddingLeft: false,
			})
		}
	}
	return out
}

// varNameEndPos returns the position immediately past the variable
// name in the source — that's where the LSP wants the ghost ": T"
// to render so the result reads as if the user had typed
// `var x: T = …`. Scans forward from Var.P (which sits on the
// `var` / `let` keyword) past whitespace + the name. Falls back
// to ok=false when the byte offset can't be reconstructed (e.g. a
// synthetic Var with no real source location).
func varNameEndPos(src string, v *ast.Var) (ast.Position, bool) {
	off := internalPosToOffset(src, v.P)
	if off < 0 {
		return ast.Position{}, false
	}
	// Skip the var / let keyword + any whitespace.
	for off < len(src) && isIdentByte(src[off]) {
		off++ // past `var` / `let`
	}
	for off < len(src) && (src[off] == ' ' || src[off] == '\t') {
		off++
	}
	// Advance past the name itself.
	start := off
	for off < len(src) && isIdentByte(src[off]) {
		off++
	}
	if off == start {
		return ast.Position{}, false
	}
	// Translate byte-offset back to (line, col). We know the
	// scan stayed on Var.P's line (no \n consumed) since lang
	// declarations are single-line; col arithmetic is enough.
	return ast.Position{
		Line: v.P.Line,
		Col:  v.P.Col + (off - internalPosToOffset(src, v.P)),
	}, true
}

// internalPosToOffset turns a 1-based ast.Position into a byte
// offset into src. Returns -1 on out-of-bounds; line/col is
// treated as ASCII byte-based (matches the parser's lexer).
func internalPosToOffset(src string, p ast.Position) int {
	if p.Line <= 0 || p.Col <= 0 {
		return -1
	}
	line := 1
	col := 1
	for i := 0; i < len(src); i++ {
		if line == p.Line && col == p.Col {
			return i
		}
		if src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return -1
}

// inRange reports whether p falls within rng. LSP semantics: both
// ends inclusive on line, half-open on character — we approximate
// with simple line-and-character comparison which is good enough
// for inlay hint culling (off-by-one at the boundary just emits an
// extra hint).
func inRange(p Position, rng Range) bool {
	if p.Line < rng.Start.Line || p.Line > rng.End.Line {
		return false
	}
	if p.Line == rng.Start.Line && p.Character < rng.Start.Character {
		return false
	}
	if p.Line == rng.End.Line && p.Character > rng.End.Character {
		return false
	}
	return true
}

// (kept here to avoid checker.Info-unused warnings if other files
// shrink their references in future cleanups)
var _ = (*checker.Info)(nil)
