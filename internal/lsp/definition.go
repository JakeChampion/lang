package lsp

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// definitionParams mirrors hoverParams — same shape, different name
// on the wire.
type definitionParams = hoverParams

// Location is the LSP definition / declaration response shape: a URI
// plus the source range of the defining token.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// runDefinition resolves the identifier under the cursor and returns
// the source location of its declaration. Returns nil when the
// identifier has no resolvable declaration (e.g. it's the declaration
// itself, or it's an unknown name) — the LSP spec treats null here
// as "no jump target available".
//
// The uri argument is echoed back unchanged: in this single-file MVP
// every definition lives in the same document. Cross-module jumps
// arrive when we add module resolution alongside the modload story.
func runDefinition(state *docState, uri string, pos Position) *Location {
	if state == nil || state.prog == nil {
		return nil
	}
	line, col := lspToInternalPos(pos)
	hit := findNameAt(state.prog, line, col)
	if hit == nil {
		return nil
	}
	defPos, defLen, ok := locateDefinition(state.info, state.prog, hit)
	if !ok {
		return nil
	}
	return &Location{
		URI:   uri,
		Range: rangeOf(defPos, defLen),
	}
}

// locateDefinition returns the (position, name-length) of the
// declaration the name resolves to. Resolution order matches
// describeName: enum variant → struct constructor → ident
// (local → parameter → top-level function / struct / enum).
func locateDefinition(info *checker.Info, prog *ast.Program, hit *nameHit) (ast.Position, int, bool) {
	if hit.enumLit != nil && info != nil {
		if ed, ok := info.Enums[hit.enumLit.EnumName]; ok {
			for _, v := range ed.Variants {
				if v.Name == hit.name {
					return v.P, len(hit.name), true
				}
			}
		}
	}
	if hit.structLit != nil && info != nil {
		if sd, ok := info.Structs[hit.name]; ok {
			return sd.P, len(hit.name), true
		}
	}
	if hit.ident != nil {
		return locateIdentDef(info, prog, hit.enclosing, hit.name)
	}
	return ast.Position{}, 0, false
}

func locateIdentDef(info *checker.Info, prog *ast.Program, enclosing *ast.FuncDecl, name string) (ast.Position, int, bool) {
	if enclosing != nil {
		if info != nil {
			for _, v := range info.Locals[enclosing] {
				if v.Name == name {
					return v.P, len(name), true
				}
			}
		}
		// Parameters: the parser doesn't capture a per-param
		// position, only the FuncDecl's own P. Jump to the
		// FuncDecl's keyword start; editors surface the
		// surrounding signature, which is good enough for the MVP.
		for _, p := range enclosing.Params {
			if p.Name == name {
				return enclosing.P, len(enclosing.Name), true
			}
		}
	}
	if info != nil {
		if _, ok := info.FuncSigs[name]; ok {
			for _, fd := range prog.Funcs {
				if fd.Name == name {
					return fd.P, len(name), true
				}
			}
		}
		if sd, ok := info.Structs[name]; ok {
			return sd.P, len(name), true
		}
		if ed, ok := info.Enums[name]; ok {
			return ed.P, len(name), true
		}
		// Bare enum variants (`Red`, `None`) — same fallback as
		// describeIdentName for the variant case.
		if _, v, ok := lookupVariant(info, name); ok {
			return v.P, len(name), true
		}
	}
	return ast.Position{}, 0, false
}

// rangeOf builds an LSP Range from a 1-based ast.Position + a byte
// length on the same line. Used to mark the selection range of a
// definition target.
func rangeOf(pos ast.Position, length int) Range {
	start := toLSPPosition(pos)
	end := start
	end.Character += length
	return Range{Start: start, End: end}
}
