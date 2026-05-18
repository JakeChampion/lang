package lsp

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// workspaceEdit is the LSP response for textDocument/rename: a map
// of URI → ordered TextEdits.
type workspaceEdit struct {
	Changes map[string][]textEdit `json:"changes"`
}

type textEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// runReferences resolves the symbol under the cursor and returns
// every Location that references it. Includes the declaration site.
// Returns an empty slice (not nil) when nothing resolves so the JSON
// encoder emits `[]`, matching LSP convention.
func runReferences(state *docState, uri string, pos Position) []Location {
	if state == nil || state.prog == nil {
		return []Location{}
	}
	line, col := lspToInternalPos(pos)
	hit := findNameAt(state.prog, line, col)
	if hit == nil {
		return []Location{}
	}
	occs := collectOccurrences(state, hit)
	out := make([]Location, 0, len(occs))
	for _, o := range occs {
		out = append(out, Location{
			URI:   declURI(o.sourceModule, uri),
			Range: rangeOf(o.pos, len(o.name)),
		})
	}
	return out
}

// runRename builds a WorkspaceEdit replacing every occurrence of
// the resolved symbol with newName. Returns nil when nothing
// resolves (LSP convention for "rename unavailable here").
func runRename(state *docState, uri string, pos Position, newName string) *workspaceEdit {
	if state == nil || state.prog == nil || newName == "" {
		return nil
	}
	line, col := lspToInternalPos(pos)
	hit := findNameAt(state.prog, line, col)
	if hit == nil {
		return nil
	}
	occs := collectOccurrences(state, hit)
	if len(occs) == 0 {
		return nil
	}
	changes := map[string][]textEdit{}
	for _, o := range occs {
		u := declURI(o.sourceModule, uri)
		changes[u] = append(changes[u], textEdit{
			Range:   rangeOf(o.pos, len(o.name)),
			NewText: newName,
		})
	}
	return &workspaceEdit{Changes: changes}
}

// occurrence is a single source location that names the resolved
// symbol — either the decl site or a usage.
type occurrence struct {
	name         string
	pos          ast.Position
	sourceModule string
}

// collectOccurrences walks the program for every Ident / qualified
// name that resolves to the same symbol as the given hit. Scopes
// correctly: a local var search stays within its enclosing function;
// a top-level decl search spans every function body.
//
// Method-call renames aren't supported yet (they'd need rewriting
// every `target.method()` site AND the def-site param name, which
// involves checker.Methods semantics we don't fully expose). When
// hit.methodCall is non-nil this returns an empty slice; the
// rename handler then returns null which clients display as
// "rename not available here".
func collectOccurrences(state *docState, hit *nameHit) []occurrence {
	if hit.methodCall != nil {
		return nil
	}
	// Type-annotation references resolve to the struct/enum decl;
	// occurrences are every TypeRef with the same Name plus every
	// in-body name that matches.
	if hit.typeRef != nil {
		return collectByName(state, hit.name, false /* local-only? */)
	}
	if hit.structLit != nil || hit.fieldAccess != nil {
		// Renaming a struct constructor or a field is out of
		// scope for this MVP (struct rename needs to also rewrite
		// all type annotations and method receivers; field rename
		// needs Methods-map rewriting). Punt.
		return nil
	}
	if hit.enumLit != nil {
		// Variant rename would need to update every match-arm
		// pattern too. Punt for now.
		return nil
	}
	if hit.ident == nil {
		return nil
	}

	// Local-vs-global classification: if any local var or param in
	// the enclosing FuncDecl matches the name, the search is
	// function-scoped. Otherwise it's program-wide.
	if hit.enclosing != nil && state.info != nil {
		if _, ok := lookupLocal(state.info, hit.enclosing, hit.name); ok {
			return collectInFunc(state, hit.enclosing, hit.name)
		}
		for _, p := range hit.enclosing.Params {
			if p.Name == hit.name {
				return collectInFunc(state, hit.enclosing, hit.name)
			}
		}
	}
	return collectByName(state, hit.name, false)
}

func lookupLocal(info *checker.Info, fd *ast.FuncDecl, name string) (*ast.Var, bool) {
	for _, v := range info.Locals[fd] {
		if v.Name == name {
			return v, true
		}
	}
	return nil, false
}

// collectInFunc collects every occurrence of name within a single
// function body (the local-scope case). Includes the var/param
// decl itself when we can locate it.
func collectInFunc(state *docState, fd *ast.FuncDecl, name string) []occurrence {
	var out []occurrence
	if state.info != nil {
		for _, v := range state.info.Locals[fd] {
			if v.Name == name {
				// Var.P is at the `var`/`let` keyword — adjust
				// to the name's actual position via the source
				// scan helper from inlay.go.
				if endPos, ok := varNameEndPos(state.src, v); ok {
					out = append(out, occurrence{
						name: name,
						pos:  ast.Position{Line: endPos.Line, Col: endPos.Col - len(name)},
					})
				}
			}
		}
	}
	if fd.Body != nil {
		ast.Walk(fd.Body, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == name {
				out = append(out, occurrence{name: name, pos: id.P})
			}
			return true
		})
	}
	return out
}

// collectByName scans the entire program for every Ident +
// TypeRef matching name. Used for top-level decl rename (function /
// struct / enum / const).
func collectByName(state *docState, name string, _ bool) []occurrence {
	var out []occurrence
	// Decl-site positions.
	for _, fd := range state.prog.Funcs {
		if fd != nil && fd.Name == name && !isInternalName(fd.Name) {
			out = append(out, occurrence{
				name:         name,
				pos:          fd.P,
				sourceModule: fd.SourceModule,
			})
		}
	}
	for _, sd := range state.prog.Structs {
		if sd != nil && sd.Name == name {
			out = append(out, occurrence{
				name:         name,
				pos:          sd.P,
				sourceModule: sd.SourceModule,
			})
		}
	}
	for _, ed := range state.prog.Enums {
		if ed != nil && ed.Name == name {
			out = append(out, occurrence{
				name:         name,
				pos:          ed.P,
				sourceModule: ed.SourceModule,
			})
		}
	}
	for _, cd := range state.prog.Consts {
		if cd != nil && cd.Name == name {
			out = append(out, occurrence{name: name, pos: cd.P})
		}
	}
	// In-body references.
	for _, fd := range state.prog.Funcs {
		if fd == nil || fd.Body == nil {
			continue
		}
		srcMod := fd.SourceModule
		ast.Walk(fd.Body, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == name {
				out = append(out, occurrence{
					name:         name,
					pos:          id.P,
					sourceModule: srcMod,
				})
			}
			return true
		})
	}
	// Type-annotation references (`var c: Color`).
	for _, tr := range state.prog.TypeRefs {
		if tr.Name == name {
			out = append(out, occurrence{name: name, pos: tr.P})
		}
	}
	return out
}
