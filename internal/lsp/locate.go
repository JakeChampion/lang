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
	// methodCall is non-nil when the cursor lands on the method
	// name in a `target.method(args)` call site. The checker
	// preserves the source position on Call.Method even after
	// rewriting the AST to a mangled flat call.
	methodCall *ast.Call
	// moduleCall is non-nil when the cursor lands on the function
	// name in a cross-module qualified call `mod.fn(args)`.
	// modload preserves the original positions on Call.Module
	// during the rewrite to a flat mangled name.
	moduleCall *ast.Call
}

// findNameAt walks prog's function bodies and returns the deepest
// nameHit whose source span contains the given (line, col) — both
// 1-based, matching lang's internal positions. Returns nil when no
// recognised name covers the position.
//
// mod is the module path of the document the request named (see
// requestModule): a workspace program merges every module into one
// position space, so the file has to be part of the match or a
// request for main.fern can be answered from a sibling module that
// happens to carry a name at the same line and column.
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
func findNameAt(prog *ast.Program, mod string, line, col int) *nameHit {
	if prog == nil {
		return nil
	}
	var best *nameHit
	for _, fd := range prog.Funcs {
		if fd == nil || fd.Body == nil {
			continue
		}
		if !inModule(fd.SourceModule, mod) {
			continue
		}
		ast.Walk(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Ident:
				// Skip mangled internal names — the user can't
				// type them, so a cursor in their range must
				// belong to a higher-level construct (method
				// call / cross-module call) whose own match
				// fires elsewhere in this switch.
				if isMangledIdent(x.Name) {
					return true
				}
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
			case *ast.Call:
				// Method-call hover / def lands on the method
				// name (preserved by the checker on Call.Method
				// even after the rewrite to a mangled flat call).
				if x.Method != nil && x.Method.FieldPos.Line != 0 &&
					spans(x.Method.FieldPos, line, col, len(x.Method.Field)) {
					best = &nameHit{
						name:       x.Method.Field,
						pos:        x.Method.FieldPos,
						enclosing:  fd,
						methodCall: x,
					}
				}
				// Cross-module call (`mod.fn(args)`) — modload
				// stashes the original FieldPos on Call.Module.
				if x.Module != nil && x.Module.FieldPos.Line != 0 &&
					spans(x.Module.FieldPos, line, col, len(x.Module.Field)) {
					best = &nameHit{
						name:       x.Module.Field,
						pos:        x.Module.FieldPos,
						enclosing:  fd,
						moduleCall: x,
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
		if !inModule(tr.SourceModule, mod) {
			continue
		}
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

// isMangledIdent reports whether name looks like an internal or
// rewritten name a user couldn't have typed: anything starting with
// `__` (method-hoist + map helpers + …) and anything containing
// `__` in the middle (modload's `mod__fn`). User-typed names with a
// double-underscore are exceedingly rare and a small false-positive
// here (the LSP shows no hover instead of a wrong one) is a better
// failure mode than the synthetic Ident shadowing the high-level
// methodCall / moduleCall hit.
func isMangledIdent(name string) bool {
	if isInternalName(name) {
		return true
	}
	for i := 0; i+1 < len(name); i++ {
		if name[i] == '_' && name[i+1] == '_' {
			return true
		}
	}
	return false
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
