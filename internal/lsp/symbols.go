package lsp

import (
	"github.com/jakechampion/lang/internal/ast"
)

// documentSymbol mirrors the LSP DocumentSymbol shape: a hierarchical
// outline of the file's top-level declarations + their members.
// Editors use this for the "Outline" / "Symbols in File" view.
type documentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []documentSymbol `json:"children,omitempty"`
}

// LSP SymbolKind values we emit (subset of the spec).
const (
	symKindFunction   = 12
	symKindStruct     = 23
	symKindEnum       = 10
	symKindField      = 8
	symKindVariable   = 13
	symKindConstant   = 14
	symKindNamespace  = 3
	symKindEnumMember = 22
)

// runDocumentSymbols emits a flat (single-level) outline of every
// user-authored top-level declaration in state.prog. Synthetic
// stdlib entries (which carry SourceModule pointing at
// `stdlib://…`) are filtered out so the outline matches what the
// user typed, not the stdlib noise.
func runDocumentSymbols(state *docState, uri string) []documentSymbol {
	if state == nil || state.prog == nil {
		return []documentSymbol{}
	}
	mod := requestModule(uri)
	keep := func(srcMod string) bool { return inModule(srcMod, mod) }
	out := []documentSymbol{}
	for _, fd := range state.prog.Funcs {
		if fd == nil || !keep(fd.SourceModule) {
			continue
		}
		if isInternalName(fd.Name) {
			continue
		}
		r := nameRange(fd.P, fd.Name)
		out = append(out, documentSymbol{
			Name:           fd.Name,
			Detail:         formatFuncSig(fd.Name, funcDeclSig(fd)),
			Kind:           symKindFunction,
			Range:          r,
			SelectionRange: r,
		})
	}
	for _, sd := range state.prog.Structs {
		if sd == nil || !keep(sd.SourceModule) {
			continue
		}
		r := nameRange(sd.P, sd.Name)
		var fields []documentSymbol
		for _, f := range sd.Fields {
			// StructDecl.Fields are ast.Param with no per-field
			// position; render them at the decl's own range as a
			// best effort so the outline at least lists them.
			fields = append(fields, documentSymbol{
				Name:           f.Name,
				Detail:         typeString(f.Type),
				Kind:           symKindField,
				Range:          r,
				SelectionRange: r,
			})
		}
		out = append(out, documentSymbol{
			Name:           sd.Name,
			Detail:         "struct",
			Kind:           symKindStruct,
			Range:          r,
			SelectionRange: r,
			Children:       fields,
		})
	}
	for _, ed := range state.prog.Enums {
		if ed == nil || !keep(ed.SourceModule) {
			continue
		}
		// Skip builtin Option / Result / IoError / JsonValue;
		// the checker injects them with P==Position{} which is
		// the cheap discriminator the checker itself uses.
		if ed.P == (ast.Position{}) {
			continue
		}
		r := nameRange(ed.P, ed.Name)
		var variants []documentSymbol
		for _, v := range ed.Variants {
			vr := nameRange(v.P, v.Name)
			variants = append(variants, documentSymbol{
				Name:           v.Name,
				Kind:           symKindEnumMember,
				Range:          vr,
				SelectionRange: vr,
			})
		}
		out = append(out, documentSymbol{
			Name:           ed.Name,
			Detail:         "enum",
			Kind:           symKindEnum,
			Range:          r,
			SelectionRange: r,
			Children:       variants,
		})
	}
	for _, cd := range state.prog.Consts {
		if cd == nil {
			continue
		}
		r := nameRange(cd.P, cd.Name)
		out = append(out, documentSymbol{
			Name:           cd.Name,
			Detail:         typeString(cd.Type),
			Kind:           symKindConstant,
			Range:          r,
			SelectionRange: r,
		})
	}
	return out
}

// isInternalName filters mangled / synthetic FuncDecls
// (`__method_*`, `__map_*`, `__substr_*`, etc.) out of the outline
// so the user sees the names they wrote.
func isInternalName(name string) bool {
	if len(name) > 2 && name[:2] == "__" {
		return true
	}
	return false
}

// funcDeclSig converts a FuncDecl's parameter/return ASTs to the
// FuncType shape formatFuncSig expects. Strips the receiver for
// methods so the outline reads as the user-facing signature.
func funcDeclSig(fd *ast.FuncDecl) *ast.FuncType {
	if fd == nil {
		return nil
	}
	params := make([]ast.Type, 0, len(fd.Params))
	for _, p := range fd.Params {
		params = append(params, p.Type)
	}
	return &ast.FuncType{Params: params, Result: fd.ReturnType}
}
