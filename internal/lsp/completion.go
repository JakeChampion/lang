package lsp

import (
	"sort"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/lexer"
)

// completionParams uses the same shape as hoverParams — text
// document + position. LSP also defines an optional `context` field
// for trigger-character info, but we don't yet need it: completion
// just enumerates everything in scope and lets the client filter.
type completionParams = hoverParams

// completionList is the LSP response shape. IsIncomplete=false tells
// the client our list is exhaustive for the given position, so it
// shouldn't re-query as the user types more characters.
type completionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []completionItem `json:"items"`
}

// LSP CompletionItemKind values we hand out (a subset of the spec).
const (
	ciKindKeyword    = 14
	ciKindVariable   = 6
	ciKindParameter  = 6 // LSP has no Parameter kind; reuse Variable.
	ciKindFunction   = 3
	ciKindStruct     = 22
	ciKindEnum       = 13
	ciKindEnumMember = 20
)

type completionItem struct {
	Label  string `json:"label"`
	Kind   int    `json:"kind,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// runCompletion returns every symbol in scope at the cursor.
// Completion items are not filtered against any prefix here — that's
// the client's job. Returning the full list also makes the MVP work
// against editors that pre-fetch completions on file open.
func runCompletion(state *docState, uri string, pos Position) *completionList {
	if state == nil || state.prog == nil {
		return &completionList{Items: []completionItem{}}
	}
	line, col := lspToInternalPos(pos)
	enclosing := enclosingFunc(state.prog, requestModule(uri), line, col)

	items := []completionItem{}

	// Locals + parameters when we're inside a function body.
	if enclosing != nil {
		if state.info != nil {
			for _, v := range state.info.Locals[enclosing] {
				typ := v.Type
				if typ == nil {
					typ = state.info.VarTypes[v]
				}
				items = append(items, completionItem{
					Label:  v.Name,
					Kind:   ciKindVariable,
					Detail: typeString(typ),
				})
			}
		}
		for _, p := range enclosing.Params {
			items = append(items, completionItem{
				Label:  p.Name,
				Kind:   ciKindParameter,
				Detail: "(parameter) " + typeString(p.Type),
			})
		}
	}

	// Top-level symbols.
	if state.info != nil {
		for name, sig := range state.info.FuncSigs {
			items = append(items, completionItem{
				Label:  name,
				Kind:   ciKindFunction,
				Detail: formatFuncSig(name, sig),
			})
		}
		for name, sd := range state.info.Structs {
			items = append(items, completionItem{
				Label:  name,
				Kind:   ciKindStruct,
				Detail: shortStructDetail(sd),
			})
		}
		for name, ed := range state.info.Enums {
			items = append(items, completionItem{
				Label:  name,
				Kind:   ciKindEnum,
				Detail: "enum " + name,
			})
			for _, v := range ed.Variants {
				items = append(items, completionItem{
					Label:  v.Name,
					Kind:   ciKindEnumMember,
					Detail: formatEnumVariant(name, v),
				})
			}
		}
	}

	// Reserved words.
	for _, kw := range lexer.Keywords() {
		items = append(items, completionItem{
			Label: kw,
			Kind:  ciKindKeyword,
		})
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	return &completionList{Items: items}
}

// enclosingFunc returns the FuncDecl whose body contains the given
// 1-based (line, col) in the module named by mod (see requestModule).
// Used by completion to scope candidates to the function the cursor
// is in. Returns nil for positions outside any function (top-level /
// between decls).
//
// "Contains" is approximated via "body starts at-or-before the line
// and the next top-level decl starts after it" — the AST doesn't
// carry end positions yet, so we rely on the source-order
// invariant that lang functions can't nest top-level decls
// inside their bodies. Local functions are skipped by checking
// IsLocal=false on each top-level entry.
func enclosingFunc(prog *ast.Program, mod string, line, col int) *ast.FuncDecl {
	if prog == nil {
		return nil
	}
	var best *ast.FuncDecl
	for _, fd := range prog.Funcs {
		if fd == nil || fd.Body == nil || fd.IsLocal {
			continue
		}
		if !inModule(fd.SourceModule, mod) {
			continue
		}
		if fd.P.Line > line {
			continue
		}
		if fd.P.Line == line && fd.P.Col > col {
			continue
		}
		// Latest decl whose start sits at-or-before the cursor.
		if best == nil || compareBefore(best.P, fd.P) {
			best = fd
		}
	}
	return best
}

func compareBefore(a, b ast.Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

func shortStructDetail(sd *ast.StructDecl) string {
	if sd == nil {
		return ""
	}
	if len(sd.TypeParams) == 0 {
		return "struct " + sd.Name
	}
	out := "struct " + sd.Name + "["
	for i, tp := range sd.TypeParams {
		if i > 0 {
			out += ", "
		}
		out += tp
	}
	return out + "]"
}

// completionFromChecker isn't an exported entry — completion code
// reaches into the checker.Info struct directly. Kept package-local
// so the checker package stays consumer-agnostic.
var _ = (*checker.Info)(nil)
