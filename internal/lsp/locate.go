package lsp

import (
	"github.com/jakechampion/lang/internal/ast"
)

// nameHit is what the position-search helpers return: an addressable
// source name (variable / type / variant / field) at a given source
// span, plus the FuncDecl whose body contains it (nil when the name
// is at top level). One of ident / structLit / enumLit / fieldAccess /
// typeRef will be non-nil so callers can switch on the originating
// AST node when they need kind-specific behaviour (variant lookup,
// struct-field listing, shadow resolution).
type nameHit struct {
	name      string
	pos       ast.Position
	enclosing *ast.FuncDecl

	ident       *ast.Ident
	structLit   *ast.StructLit
	enumLit     *ast.EnumLit
	fieldAccess *ast.FieldAccess
	// typeRef is non-nil for hits in type-annotation slots
	// (`var c: Color` → typeRef captures `Color`'s position).
	// Picked up from Program.TypeRefs rather than the AST walk.
	typeRef *ast.TypeRef
}

// findNameAt walks prog's function bodies and returns the deepest
// nameHit whose source span contains the given (line, col) — both
// 1-based, matching lang's internal positions. Returns nil when no
// recognised name covers the position.
//
// Recognised name-bearing nodes:
//   - *ast.Ident          — variable / parameter / function reference
//   - *ast.StructLit      — name is the struct's TypeName, starting at P
//   - *ast.EnumLit        — name is the variant name, starting at P
//   - *ast.FieldAccess    — field name after a `.`, span starts at FieldPos
//   - *ast.TypeRef        — type-annotation name, via Program.TypeRefs
//
// Span is half-open at the right so a cursor immediately past the
// last character (the common "click at end of word" placement editors
// send) still hits. Identifiers in lang can't span newlines, so a
// single-line range check is correct for all node kinds.
func findNameAt(prog *ast.Program, line, col int) *nameHit {
	if prog == nil {
		return nil
	}
	var best *nameHit
	for _, fd := range prog.Funcs {
		if fd == nil || fd.Body == nil {
			continue
		}
		ast.Walk(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				if spans(x.P, line, col, len(x.Name)) {
					best = &nameHit{
						name:      x.Name,
						pos:       x.P,
						enclosing: fd,
						ident:     x,
					}
				}
			case *ast.StructLit:
				if spans(x.P, line, col, len(x.TypeName)) {
					best = &nameHit{
						name:      x.TypeName,
						pos:       x.P,
						enclosing: fd,
						structLit: x,
					}
				}
			case *ast.EnumLit:
				if spans(x.P, line, col, len(x.VariantName)) {
					best = &nameHit{
						name:      x.VariantName,
						pos:       x.P,
						enclosing: fd,
						enumLit:   x,
					}
				}
			case *ast.FieldAccess:
				if x.FieldPos.Line != 0 && spans(x.FieldPos, line, col, len(x.Field)) {
					best = &nameHit{
						name:        x.Field,
						pos:         x.FieldPos,
						enclosing:   fd,
						fieldAccess: x,
					}
				}
			}
			return true
		})
	}
	// Type annotations live outside any function body — they're in
	// Var.Type, Param.Type, etc., which are positionless ast.Type
	// values. The parser deposits a side table with their source
	// positions in Program.TypeRefs; we search it last so a type
	// ref under the cursor wins only when no in-body name does.
	// (In practice the two never overlap — types appear in type
	// slots, not expressions.)
	for i := range prog.TypeRefs {
		tr := &prog.TypeRefs[i]
		if spans(tr.P, line, col, len(tr.Name)) {
			best = &nameHit{
				name:    tr.Name,
				pos:     tr.P,
				typeRef: tr,
			}
		}
	}
	return best
}

func spans(p ast.Position, line, col, length int) bool {
	if p.Line != line {
		return false
	}
	return col >= p.Col && col < p.Col+length
}

// lspToInternalPos turns LSP's 0-based line + 0-based UTF-16 character
// into lang's 1-based line + 1-based UTF-8 byte column. Inverse of
// toLSPPosition; correct for ASCII source, off for non-ASCII. The MVP
// scope (lang source is mostly ASCII) accepts the asymmetry.
func lspToInternalPos(p Position) (line, col int) {
	return p.Line + 1, p.Character + 1
}

// nameRange returns the LSP Range covering an identifier whose
// 1-based start position is pos and whose source spelling is name.
// Used as the selection range for definition / hover-target.
func nameRange(pos ast.Position, name string) Range {
	start := toLSPPosition(pos)
	end := start
	end.Character += len(name)
	return Range{Start: start, End: end}
}
