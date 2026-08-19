package lsp

import (
	"sort"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// semanticTokensResponse is the wire shape: a flat i32 array whose
// every 5 entries encode (deltaLine, deltaStartChar, length,
// tokenType, tokenModifiers). All offsets are deltas relative to
// the previous token (or absolute for the first), so the encoding
// stays compact for large files.
type semanticTokensResponse struct {
	Data []int `json:"data"`
}

// Token type indices — index into the legend served by
// serverCapabilities.SemanticTokensProvider.Legend.TokenTypes.
const (
	stFunction = iota
	stMethod
	stStruct
	stEnum
	stEnumMember
	stParameter
	stVariable
	stProperty // struct field
	stType
	stKeyword
	stNamespace // module qualifier (`util` in `util.foo`)
)

// semanticTokenTypeNames returns the legend the client uses to
// decode the integer stream. Order must match the iota constants
// above.
func semanticTokenTypeNames() []string {
	return []string{
		"function",
		"method",
		"struct",
		"enum",
		"enumMember",
		"parameter",
		"variable",
		"property",
		"type",
		"keyword",
		"namespace",
	}
}

// rawToken is the pre-encoding shape we collect during the walk —
// each entry carries an absolute (line, char, length, type). After
// sorting we delta-encode to the LSP wire format.
type rawToken struct {
	line, char, length, tokenType int
}

// runSemanticTokens walks the AST + checker info to produce a
// classified token stream the editor uses for type-aware syntax
// highlighting. Conservative: only emits tokens we're confident
// about (decls, name references, field accesses, type names),
// leaving everything else for the editor's syntactic highlighter.
func runSemanticTokens(state *docState, uri string) semanticTokensResponse {
	if state == nil || state.prog == nil {
		return semanticTokensResponse{Data: []int{}}
	}
	mod := requestModule(uri)
	var raw []rawToken
	add := func(pos ast.Position, length, tt int) {
		if length <= 0 || pos.Line <= 0 {
			return
		}
		raw = append(raw, rawToken{line: pos.Line - 1, char: pos.Col - 1, length: length, tokenType: tt})
	}
	for _, fd := range state.prog.Funcs {
		if fd == nil || fd.Body == nil {
			continue
		}
		if !inModule(fd.SourceModule, mod) {
			continue
		}
		if isInternalName(fd.Name) {
			continue // skip mangled methods + helpers
		}
		ast.Walk(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				add(x.P, len(x.Name), classifyIdent(state.info, fd, x))
			case *ast.FieldAccess:
				if x.FieldPos.Line != 0 {
					add(x.FieldPos, len(x.Field), stProperty)
				}
			case *ast.Call:
				if x.Method != nil && x.Method.FieldPos.Line != 0 {
					add(x.Method.FieldPos, len(x.Method.Field), stMethod)
				}
				if x.Module != nil {
					if x.Module.ModulePos.Line != 0 {
						add(x.Module.ModulePos, len(x.Module.Module), stNamespace)
					}
					if x.Module.FieldPos.Line != 0 {
						add(x.Module.FieldPos, len(x.Module.Field), stFunction)
					}
				}
			case *ast.StructLit:
				add(x.P, len(x.TypeName), stStruct)
			case *ast.EnumLit:
				add(x.P, len(x.VariantName), stEnumMember)
			}
			return true
		})
	}
	// Type-annotation references — pulled from the parser's side
	// table. Best-effort classification: try struct first, then
	// enum, falling back to "type" for the leftover keywords case
	// (which our parser doesn't actually emit TypeRefs for, but
	// it's safe).
	for _, tr := range state.prog.TypeRefs {
		if !inModule(tr.SourceModule, mod) {
			continue
		}
		tt := stType
		if state.info != nil {
			if _, ok := state.info.Structs[tr.Name]; ok {
				tt = stStruct
			} else if _, ok := state.info.Enums[tr.Name]; ok {
				tt = stEnum
			}
		}
		add(tr.P, len(tr.Name), tt)
	}
	return encodeSemanticTokens(raw)
}

func classifyIdent(info *checker.Info, enclosing *ast.FuncDecl, id *ast.Ident) int {
	name := id.Name
	if info != nil {
		for _, v := range info.Locals[enclosing] {
			if v.Name == name {
				return stVariable
			}
		}
		for _, p := range enclosing.Params {
			if p.Name == name {
				return stParameter
			}
		}
		if _, ok := info.FuncSigs[name]; ok {
			return stFunction
		}
		if _, ok := info.Structs[name]; ok {
			return stStruct
		}
		if _, ok := info.Enums[name]; ok {
			return stEnum
		}
		if _, _, ok := variantOfIdent(info, id); ok {
			return stEnumMember
		}
	}
	return stVariable
}

// encodeSemanticTokens sorts the raw token list by (line, char) and
// produces the delta-encoded i32 stream LSP expects.
func encodeSemanticTokens(raw []rawToken) semanticTokensResponse {
	sort.Slice(raw, func(i, j int) bool {
		if raw[i].line != raw[j].line {
			return raw[i].line < raw[j].line
		}
		return raw[i].char < raw[j].char
	})
	data := make([]int, 0, len(raw)*5)
	prevLine, prevChar := 0, 0
	for i, t := range raw {
		dLine := t.line - prevLine
		dChar := t.char
		if i > 0 && dLine == 0 {
			dChar = t.char - prevChar
		}
		data = append(data, dLine, dChar, t.length, t.tokenType, 0)
		prevLine = t.line
		prevChar = t.char
	}
	return semanticTokensResponse{Data: data}
}
