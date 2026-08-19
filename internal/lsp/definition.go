package lsp

import (
	"strings"

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
// In workspace mode, when the resolved decl carries a SourceModule
// path different from the calling URI's file, the response URI gets
// rewritten so editors jump across files. Single-file mode echoes
// the calling URI unchanged.
func runDefinition(state *docState, uri string, pos Position) *Location {
	if state == nil || state.prog == nil {
		return nil
	}
	line, col := lspToInternalPos(pos)
	hit := findNameAt(state.prog, requestModule(uri), line, col)
	if hit == nil {
		return nil
	}
	defPos, defLen, defURI, ok := locateDefinition(state.info, state.prog, hit, uri)
	if !ok {
		return nil
	}
	return &Location{
		URI:   defURI,
		Range: rangeOf(defPos, defLen),
	}
}

// locateDefinition returns the (position, name-length, target URI)
// of the declaration the name resolves to. Resolution order matches
// describeName: enum variant → struct constructor → type ref →
// field access → ident. The fallbackURI is returned for definitions
// that live in the same file as the cursor; cross-module decls
// (StructDecl / EnumDecl / FuncDecl with non-empty SourceModule)
// rewrite to the file:// URI of their declaring module so editors
// jump to the right file.
func locateDefinition(info *checker.Info, prog *ast.Program, hit *nameHit, fallbackURI string) (ast.Position, int, string, bool) {
	if hit.enumLit != nil && info != nil {
		if ed, ok := info.Enums[hit.enumLit.EnumName]; ok {
			for _, v := range ed.Variants {
				if v.Name == hit.name {
					return v.P, len(hit.name), declURI(ed.SourceModule, fallbackURI), true
				}
			}
		}
	}
	if hit.structLit != nil && info != nil {
		if sd, ok := info.Structs[hit.name]; ok {
			return sd.P, len(hit.name), declURI(sd.SourceModule, fallbackURI), true
		}
	}
	if hit.typeRef != nil && info != nil {
		// Try the raw source spelling first ("Point"), then the
		// modload-mangled form ("util.Point" → "util__Point") so
		// cross-module references resolve in workspace mode.
		for _, candidate := range mangleCandidates(hit.name) {
			if sd, ok := info.Structs[candidate]; ok {
				return sd.P, len(hit.name), declURI(sd.SourceModule, fallbackURI), true
			}
			if ed, ok := info.Enums[candidate]; ok {
				return ed.P, len(hit.name), declURI(ed.SourceModule, fallbackURI), true
			}
		}
	}
	if hit.fieldAccess != nil && info != nil {
		if pos, srcMod, ok := locateField(info, hit.enclosing, hit.fieldAccess); ok {
			return pos, len(hit.name), declURI(srcMod, fallbackURI), true
		}
	}
	if hit.methodCall != nil && info != nil {
		if pos, srcMod, ok := locateMethod(info, prog, hit.methodCall); ok {
			return pos, len(hit.name), declURI(srcMod, fallbackURI), true
		}
	}
	if hit.moduleCall != nil {
		// Walk prog.Funcs for the mangled name modload baked in.
		mangled := hit.moduleCall.Module.Mangled
		for _, fd := range prog.Funcs {
			if fd.Name == mangled {
				return fd.P, len(hit.name), declURI(fd.SourceModule, fallbackURI), true
			}
		}
	}
	if hit.ident != nil {
		pos, n, srcMod, ok := locateIdentDef(info, prog, hit.enclosing, hit.ident)
		if ok {
			return pos, n, declURI(srcMod, fallbackURI), true
		}
	}
	return ast.Position{}, 0, "", false
}

// locateMethod resolves a method-call hit to the FuncDecl position
// of the implementation the call site dispatched to, then scans
// prog.Funcs for the matching decl.
func locateMethod(info *checker.Info, prog *ast.Program, call *ast.Call) (ast.Position, string, bool) {
	receiverName, ok := receiverTypeKey(call.Method.Receiver)
	if !ok {
		return ast.Position{}, "", false
	}
	mangled, _, ok := info.ResolveMethod(receiverName, call.Method.Field, []string{call.Method.OwnerTrait})
	if !ok {
		return ast.Position{}, "", false
	}
	for _, fd := range prog.Funcs {
		if fd.Name == mangled {
			return fd.P, fd.SourceModule, true
		}
	}
	return ast.Position{}, "", false
}

// mangleCandidates returns the names worth trying when looking up a
// source spelling against the modload-mangled checker.Info maps.
// `Point` → [`Point`]. `util.Point` → [`util.Point`, `util__Point`].
// Single-file programs hit the first try; workspace mode hits the
// second for cross-module qualified references.
func mangleCandidates(name string) []string {
	if !strings.Contains(name, ".") {
		return []string{name}
	}
	return []string{name, strings.ReplaceAll(name, ".", "__")}
}

// declURI converts a SourceModule path (modload's absolute-path form)
// into a file:// URI for the LSP Location. Empty SourceModule means
// the decl is in the same file as the cursor, so we return the
// fallback URI unchanged.
func declURI(sourceModule, fallback string) string {
	if sourceModule == "" {
		return fallback
	}
	// Don't try to URI-ify the stdlib:// pseudo-paths — they
	// aren't real files. Cross-module jumps into the stdlib stay
	// pointed at the caller's URI; that's acceptable for an MVP.
	if strings.HasPrefix(sourceModule, "stdlib://") {
		return fallback
	}
	return pathToURI(sourceModule)
}

// locateField finds the declaration position of the field accessed
// in fa. StructDecl.Fields entries are ast.Param which don't carry
// per-field positions, so we jump to the StructDecl itself —
// editors scroll the user near enough to spot the field.
func locateField(info *checker.Info, enclosing *ast.FuncDecl, fa *ast.FieldAccess) (ast.Position, string, bool) {
	targetType := exprResolvedType(info, enclosing, fa.Target)
	if targetType == nil {
		return ast.Position{}, "", false
	}
	st, ok := targetType.(ast.StructType)
	if !ok {
		return ast.Position{}, "", false
	}
	sd, ok := info.Structs[st.Name]
	if !ok {
		return ast.Position{}, "", false
	}
	for _, f := range sd.Fields {
		if f.Name == fa.Field {
			return sd.P, sd.SourceModule, true
		}
	}
	return ast.Position{}, "", false
}

func locateIdentDef(info *checker.Info, prog *ast.Program, enclosing *ast.FuncDecl, id *ast.Ident) (ast.Position, int, string, bool) {
	name := id.Name
	if enclosing != nil {
		if info != nil {
			for _, v := range info.Locals[enclosing] {
				if v.Name == name {
					return v.P, len(name), "", true
				}
			}
		}
		// Parameters: the parser doesn't capture a per-param
		// position, only the FuncDecl's own P. Jump to the
		// FuncDecl's keyword start; editors surface the
		// surrounding signature, which is good enough for the MVP.
		for _, p := range enclosing.Params {
			if p.Name == name {
				return enclosing.P, len(enclosing.Name), "", true
			}
		}
	}
	if info != nil {
		if _, ok := info.FuncSigs[name]; ok {
			for _, fd := range prog.Funcs {
				if fd.Name == name {
					return fd.P, len(name), fd.SourceModule, true
				}
			}
		}
		if sd, ok := info.Structs[name]; ok {
			return sd.P, len(name), sd.SourceModule, true
		}
		if ed, ok := info.Enums[name]; ok {
			return ed.P, len(name), ed.SourceModule, true
		}
		// Bare enum variants (`Red`, `None`) — resolved off the
		// checker's stamp, same as describeIdentName.
		if enumName, v, ok := variantOfIdent(info, id); ok {
			var srcMod string
			if ed, ok := info.Enums[enumName]; ok {
				srcMod = ed.SourceModule
			}
			return v.P, len(name), srcMod, true
		}
	}
	return ast.Position{}, 0, "", false
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
