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
func collectOccurrences(state *docState, hit *nameHit) []occurrence {
	if hit.methodCall != nil {
		return collectMethod(state, hit.methodCall.Method)
	}
	// Type-annotation references resolve to the struct/enum decl;
	// occurrences are every TypeRef with the same Name plus every
	// in-body name that matches.
	if hit.typeRef != nil {
		return collectByName(state, hit.name, false /* local-only? */)
	}
	if hit.structLit != nil {
		// Struct-type rename. Same as a typeRef hit: occurrences
		// are every TypeRef + every StructLit constructor name.
		return collectByName(state, hit.name, false)
	}
	if hit.fieldAccess != nil {
		return collectField(state, hit.enclosing, hit.fieldAccess)
	}
	if hit.enumLit != nil {
		return collectVariant(state, hit.enumLit.EnumName, hit.name)
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
	// Bare enum variants (`Red`, `None`) parse as Idents; the
	// checker resolves them without rewriting the AST, leaving the
	// enum it picked on Ident.EnumName. Route those to
	// collectVariant so the rewrite reaches the decl + every
	// match-arm pattern, not just the Ident occurrences
	// collectByName would find.
	if enumName, _, ok := variantOfIdent(state.info, hit.ident); ok {
		return collectVariant(state, enumName, hit.name)
	}
	return collectByName(state, hit.name, false)
}

// collectVariant gathers every position naming the variant in
// (enumName, variant). Covers the variant's declaration site
// (EnumVariant.P), every `Red`-style EnumLit, every match-arm
// pattern (Match + MatchExpr), and every call-shaped variant
// construction (`Some(42)`). The checker doesn't rewrite call-
// shaped variants to EnumLit either — they stay as Calls with an
// Ident callee — so bare-name references are found by sweeping
// Idents the checker stamped with this enum.
func collectVariant(state *docState, enumName, variantName string) []occurrence {
	var out []occurrence
	if state.info == nil {
		return out
	}
	ed, ok := state.info.Enums[enumName]
	if !ok {
		return out
	}
	srcMod := ed.SourceModule
	for _, v := range ed.Variants {
		if v.Name == variantName {
			out = append(out, occurrence{name: variantName, pos: v.P, sourceModule: srcMod})
		}
	}
	for _, fd := range state.prog.Funcs {
		if fd == nil || fd.Body == nil {
			continue
		}
		fnMod := fd.SourceModule
		ast.Walk(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.EnumLit:
				if x.EnumName == enumName && x.VariantName == variantName {
					out = append(out, occurrence{
						name: variantName, pos: x.P, sourceModule: fnMod,
					})
				}
			case *ast.Match:
				for _, arm := range x.Arms {
					if !arm.IsWildcard && arm.VariantName == variantName && arm.EnumName == enumName {
						out = append(out, occurrence{
							name: variantName, pos: arm.P, sourceModule: fnMod,
						})
					}
				}
			case *ast.MatchExpr:
				for _, arm := range x.Arms {
					if !arm.IsWildcard && arm.VariantName == variantName && arm.EnumName == enumName {
						out = append(out, occurrence{
							name: variantName, pos: arm.P, sourceModule: fnMod,
						})
					}
				}
			case *ast.Ident:
				// Bare-name reference to the variant (`Red`, `None`).
				// The checker's stamp decides: a like-named variant of
				// an enum this module cannot see is a different symbol.
				if x.Name == variantName && x.EnumName == enumName {
					out = append(out, occurrence{
						name: variantName, pos: x.P, sourceModule: fnMod,
					})
				}
			}
			return true
		})
	}
	return out
}

// collectField gathers every position naming the field
// `target.Field` belongs to. Resolves the receiver type via the
// shared exprResolvedType helper, then walks the program for: the
// struct's decl-site field position (Param.NamePos), every
// FieldAccess.Field with a matching receiver, and every
// StructLit.Fields[].Name in the matching struct's literals.
func collectField(state *docState, enclosing *ast.FuncDecl, fa *ast.FieldAccess) []occurrence {
	if state.info == nil {
		return nil
	}
	targetType := exprResolvedType(state.info, enclosing, fa.Target)
	if targetType == nil {
		return nil
	}
	st, ok := targetType.(ast.StructType)
	if !ok {
		return nil
	}
	sd, ok := state.info.Structs[st.Name]
	if !ok {
		return nil
	}
	srcMod := sd.SourceModule
	var out []occurrence
	for _, f := range sd.Fields {
		if f.Name == fa.Field && f.NamePos.Line != 0 {
			out = append(out, occurrence{name: fa.Field, pos: f.NamePos, sourceModule: srcMod})
		}
	}
	for _, fd := range state.prog.Funcs {
		if fd == nil || fd.Body == nil {
			continue
		}
		fnMod := fd.SourceModule
		ast.Walk(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FieldAccess:
				if x.Field != fa.Field || x.FieldPos.Line == 0 {
					return true
				}
				// Only count accesses whose receiver resolves to
				// the same struct so we don't rename
				// like-named fields on unrelated types.
				tt := exprResolvedType(state.info, fd, x.Target)
				if at, ok := tt.(ast.StructType); ok && at.Name == st.Name {
					out = append(out, occurrence{
						name: fa.Field, pos: x.FieldPos, sourceModule: fnMod,
					})
				}
			case *ast.StructLit:
				if x.TypeName != st.Name {
					return true
				}
				for _, f := range x.Fields {
					if f.Name == fa.Field && f.NamePos.Line != 0 {
						out = append(out, occurrence{
							name: fa.Field, pos: f.NamePos, sourceModule: fnMod,
						})
					}
				}
			}
			return true
		})
	}
	return out
}

// collectMethod gathers every position naming the method whose call
// site m belongs to: the FuncDecl's NamePos for the implementation
// + every Call.Method.FieldPos with a matching (receiver, name)
// pair. The receiver-type match keeps `Point.sum` distinct from a
// hypothetical `Vec.sum`.
func collectMethod(state *docState, m *ast.MethodCallSite) []occurrence {
	if state.info == nil || m == nil {
		return nil
	}
	receiverName, ok := receiverTypeKey(m.Receiver)
	if !ok {
		return nil
	}
	mangled, _, ok := state.info.ResolveMethod(receiverName, m.Field, []string{m.OwnerTrait})
	if !ok {
		return nil
	}
	var out []occurrence
	for _, fd := range state.prog.Funcs {
		if fd == nil || fd.Body == nil {
			continue
		}
		if fd.Name == mangled && fd.NamePos.Line != 0 {
			out = append(out, occurrence{
				name: m.Field, pos: fd.NamePos, sourceModule: fd.SourceModule,
			})
		}
		fnMod := fd.SourceModule
		ast.Walk(fd.Body, func(n ast.Node) bool {
			c, ok := n.(*ast.Call)
			if !ok || c.Method == nil {
				return true
			}
			if c.Method.Field != m.Field {
				return true
			}
			rn, ok := receiverTypeKey(c.Method.Receiver)
			if !ok || rn != receiverName {
				return true
			}
			if c.Method.FieldPos.Line == 0 {
				return true
			}
			out = append(out, occurrence{
				name: m.Field, pos: c.Method.FieldPos, sourceModule: fnMod,
			})
			return true
		})
	}
	return out
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
