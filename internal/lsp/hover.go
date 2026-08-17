package lsp

import (
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

// hoverParams is the textDocument/hover request payload — a position
// inside a text document.
type hoverParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position Position `json:"position"`
}

// hoverResult is the LSP response for hover. Contents is markdown so
// editors syntax-highlight the `fern`-fenced code block.
type hoverResult struct {
	Contents markupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

type markupContent struct {
	Kind  string `json:"kind"` // "plaintext" | "markdown"
	Value string `json:"value"`
}

// runHover resolves the name under the cursor against the checker's
// symbol tables and returns a hover response. Returns nil when there's
// no recognised name at the position or no useful info to show — the
// LSP wire protocol treats a null result as "no hover".
func runHover(state *docState, pos Position) *hoverResult {
	if state == nil || state.prog == nil {
		return nil
	}
	line, col := lspToInternalPos(pos)
	hit := findNameAt(state.prog, line, col)
	if hit == nil {
		return nil
	}
	value, ok := describeName(state.info, hit)
	if !ok {
		return nil
	}
	r := nameRange(hit.pos, hit.name)
	return &hoverResult{
		Contents: markupContent{
			Kind:  "markdown",
			Value: "```fern\n" + value + "\n```",
		},
		Range: &r,
	}
}

// describeName picks a one-line description for the name under the
// cursor by consulting whichever symbol table claims it. Resolution
// order matches scoping: variant lookup short-circuits enum-lit hits,
// struct-lit hits route to the struct decl, and ident hits fall
// through local → parameter → top-level. info may be nil if type-
// checking failed catastrophically — in that case we surface
// "parameter" info from the AST alone where we can.
func describeName(info *checker.Info, hit *nameHit) (string, bool) {
	// Enum-variant lit: the variant name + its parent enum's decl.
	if hit.enumLit != nil && info != nil {
		enumName := hit.enumLit.EnumName
		if ed, ok := info.Enums[enumName]; ok {
			for _, v := range ed.Variants {
				if v.Name == hit.name {
					return formatEnumVariant(enumName, v), true
				}
			}
		}
	}
	// Struct-lit constructor: name is a type. Show the struct decl.
	if hit.structLit != nil && info != nil {
		if sd, ok := info.Structs[hit.name]; ok {
			return formatStructDecl(sd), true
		}
	}
	// Type annotation reference (`var c: Color`, `Option[T]`, …).
	// Look the name up as a struct or enum; the parser doesn't
	// distinguish at parse-time, so we try both.
	if hit.typeRef != nil && info != nil {
		if sd, ok := info.Structs[hit.name]; ok {
			return formatStructDecl(sd), true
		}
		if ed, ok := info.Enums[hit.name]; ok {
			return formatEnumDecl(ed), true
		}
	}
	// Field access (`p.x`). Surface "(field) name: type" using the
	// receiver's resolved type to find the owning struct.
	if hit.fieldAccess != nil && info != nil {
		if d, ok := describeFieldAccess(info, hit.enclosing, hit.fieldAccess); ok {
			return d, true
		}
	}
	// Method call (`p.area()`). Look up the mangled implementation
	// in Info.Methods + FuncSigs to surface the signature.
	if hit.methodCall != nil && info != nil {
		if d, ok := describeMethodCall(info, hit.methodCall); ok {
			return d, true
		}
	}
	// Cross-module call (`mod.fn()`). modload's Mangled name keys
	// directly into FuncSigs.
	if hit.moduleCall != nil && info != nil {
		if sig, ok := info.FuncSigs[hit.moduleCall.Module.Mangled]; ok {
			return "(from " + hit.moduleCall.Module.Module + ") " +
				formatFuncSig(hit.moduleCall.Module.Field, sig), true
		}
	}

	// Ident hits go through the regular scope-chain resolution.
	if hit.ident != nil {
		return describeIdentName(info, hit.enclosing, hit.name)
	}
	return "", false
}

// describeFieldAccess walks the target's resolved type to find the
// owning struct, then looks up the field by name. Returns ok=false
// when the target type isn't a known struct (which can happen
// mid-edit, on tuple `.0` selectors, or on built-in method calls
// the checker rewrites elsewhere).
func describeFieldAccess(info *checker.Info, enclosing *ast.FuncDecl, fa *ast.FieldAccess) (string, bool) {
	targetType := exprResolvedType(info, enclosing, fa.Target)
	if targetType == nil {
		return "", false
	}
	st, ok := targetType.(ast.StructType)
	if !ok {
		return "", false
	}
	sd, ok := info.Structs[st.Name]
	if !ok {
		return "", false
	}
	for _, f := range sd.Fields {
		if f.Name == fa.Field {
			return "(field) " + f.Name + ": " + typeString(f.Type), true
		}
	}
	return "", false
}

// exprResolvedType peeks at the kinds of expression the checker
// already stamps a type onto, so describeFieldAccess can resolve
// `target.field` without re-running type inference. enclosing
// scopes the Ident lookup so the user's `p` isn't shadowed by a
// like-named local in some unrelated stdlib function. Returns nil
// when the expression's type isn't recoverable from the AST + info
// alone — callers fall through to "no hover" on miss.
func exprResolvedType(info *checker.Info, enclosing *ast.FuncDecl, e ast.Expr) ast.Type {
	switch x := e.(type) {
	case *ast.Ident:
		if enclosing != nil {
			for _, v := range info.Locals[enclosing] {
				if v.Name != x.Name {
					continue
				}
				if v.Type != nil {
					return v.Type
				}
				if t, ok := info.VarTypes[v]; ok {
					return t
				}
			}
			for _, p := range enclosing.Params {
				if p.Name == x.Name {
					return p.Type
				}
			}
		}
	case *ast.StructLit:
		return ast.StructType{Name: x.TypeName}
	case *ast.FieldAccess:
		// Chained accesses: recurse on the target then look up
		// the field's declared type.
		inner := exprResolvedType(info, enclosing, x.Target)
		if st, ok := inner.(ast.StructType); ok {
			if sd, ok := info.Structs[st.Name]; ok {
				for _, f := range sd.Fields {
					if f.Name == x.Field {
						return f.Type
					}
				}
			}
		}
	}
	return nil
}

func describeIdentName(info *checker.Info, enclosing *ast.FuncDecl, name string) (string, bool) {
	if enclosing != nil {
		// Locals win over parameters per the checker's scope chain.
		if info != nil {
			for _, v := range info.Locals[enclosing] {
				if v.Name != name {
					continue
				}
				typ := v.Type
				if typ == nil {
					typ = info.VarTypes[v]
				}
				return formatVarDecl(name, typ), true
			}
		}
		for _, p := range enclosing.Params {
			if p.Name == name {
				return "(parameter) " + name + ": " + typeString(p.Type), true
			}
		}
	}
	if info != nil {
		if sig, ok := info.FuncSigs[name]; ok {
			return formatFuncSig(name, sig), true
		}
		if sd, ok := info.Structs[name]; ok {
			return formatStructDecl(sd), true
		}
		if ed, ok := info.Enums[name]; ok {
			return formatEnumDecl(ed), true
		}
		// Bare enum variants (`Red`, `None`) parse as Idents — the
		// checker resolves them via variantOf without rewriting the
		// AST. Fall back to a name-keyed sweep of the registered
		// enums; variant names are unique across enums per the
		// checker's duplicate check, so the first hit is correct.
		if enumName, v, ok := lookupVariant(info, name); ok {
			return formatEnumVariant(enumName, v), true
		}
	}
	return "", false
}

// lookupVariant searches every registered enum for a variant whose
// source name matches. Returns the owning enum's name and the
// variant. The checker rejects duplicate variant names across
// enums, so the first hit is the only hit.
func lookupVariant(info *checker.Info, name string) (string, ast.EnumVariant, bool) {
	if info == nil {
		return "", ast.EnumVariant{}, false
	}
	for enumName, ed := range info.Enums {
		for _, v := range ed.Variants {
			if v.Name == name {
				return enumName, v, true
			}
		}
	}
	return "", ast.EnumVariant{}, false
}

// describeMethodCall formats the hover for a `target.method()`
// site. Resolves the implementation the call site dispatched to and
// looks its signature up in FuncSigs. Falls through to ok=false if the
// receiver type isn't a struct/enum/builtin we can name, or the method
// isn't registered.
func describeMethodCall(info *checker.Info, call *ast.Call) (string, bool) {
	receiverName, ok := receiverTypeKey(call.Method.Receiver)
	if !ok {
		return "", false
	}
	mangled, _, ok := info.ResolveMethod(receiverName, call.Method.Field, []string{call.Method.OwnerTrait})
	if !ok {
		return "", false
	}
	sig, ok := info.FuncSigs[mangled]
	if !ok {
		return "(method) " + call.Method.Field, true
	}
	return "(method on " + receiverName + ") " + formatMethodSig(call.Method.Field, sig), true
}

// formatMethodSig is formatFuncSig but skips the implicit receiver
// (the first parameter of the mangled __method_* function), so the
// user sees the method's "logical" parameter list.
func formatMethodSig(name string, sig *ast.FuncType) string {
	if sig == nil {
		return name + "(?)"
	}
	out := "function " + name + "("
	for i, p := range sig.Params {
		if i == 0 {
			continue // implicit receiver
		}
		if i > 1 {
			out += ", "
		}
		out += typeString(p)
	}
	return out + "): " + typeString(sig.Result)
}

// receiverTypeKey extracts the source-level type name used as the
// first half of the Info.Methods key. Mirrors the checker's
// own logic in the method-rewrite site.
func receiverTypeKey(t ast.Type) (string, bool) {
	switch x := t.(type) {
	case ast.StructType:
		return x.Name, true
	case ast.EnumType:
		return x.Name, true
	case ast.NumberType:
		return x.String(), true
	case ast.FloatType:
		return x.String(), true
	case ast.StringType:
		return "string", true
	case ast.BoolType:
		return "boolean", true
	case ast.ArrayType:
		return "Array", true
	}
	return "", false
}

func formatEnumVariant(enumName string, v ast.EnumVariant) string {
	out := "(variant of " + enumName + ") " + v.Name
	if len(v.Payloads) == 0 {
		return out
	}
	out += "("
	for i, pt := range v.Payloads {
		if i > 0 {
			out += ", "
		}
		out += typeString(pt)
	}
	return out + ")"
}

func formatVarDecl(name string, t ast.Type) string {
	return "(var) " + name + ": " + typeString(t)
}

func formatFuncSig(name string, sig *ast.FuncType) string {
	if sig == nil {
		return "function " + name + "(?)"
	}
	var b strings.Builder
	b.WriteString("function ")
	b.WriteString(name)
	b.WriteString("(")
	for i, p := range sig.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(typeString(p))
	}
	b.WriteString("): ")
	b.WriteString(typeString(sig.Result))
	return b.String()
}

func formatStructDecl(sd *ast.StructDecl) string {
	var b strings.Builder
	b.WriteString("struct ")
	b.WriteString(sd.Name)
	if len(sd.TypeParams) > 0 {
		b.WriteString("[")
		b.WriteString(strings.Join(sd.TypeParams, ", "))
		b.WriteString("]")
	}
	b.WriteString(" {")
	for i, f := range sd.Fields {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(" ")
		b.WriteString(f.Name)
		b.WriteString(": ")
		b.WriteString(typeString(f.Type))
	}
	if len(sd.Fields) > 0 {
		b.WriteString(" ")
	}
	b.WriteString("}")
	return b.String()
}

func formatEnumDecl(ed *ast.EnumDecl) string {
	var b strings.Builder
	b.WriteString("enum ")
	b.WriteString(ed.Name)
	if len(ed.TypeParams) > 0 {
		b.WriteString("[")
		b.WriteString(strings.Join(ed.TypeParams, ", "))
		b.WriteString("]")
	}
	b.WriteString(" {")
	for i, v := range ed.Variants {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(" ")
		b.WriteString(v.Name)
		if len(v.Payloads) > 0 {
			b.WriteString("(")
			for j, pt := range v.Payloads {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(typeString(pt))
			}
			b.WriteString(")")
		}
	}
	if len(ed.Variants) > 0 {
		b.WriteString(" ")
	}
	b.WriteString("}")
	return b.String()
}

// typeString is a thin wrapper that handles a nil Type (which the
// checker leaves on Var when it failed to resolve) without panicking
// on the interface method call.
func typeString(t ast.Type) string {
	if t == nil {
		return "?"
	}
	return t.String()
}
