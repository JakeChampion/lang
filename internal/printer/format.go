// Format produces idiomatic, multi-line lang source from a parsed
// AST. Unlike Print, which is fully parenthesised at every binary /
// unary / assignment boundary so the output round-trips through the
// parser unconditionally, Format outputs human-readable source —
// minimal parentheses (just enough to preserve operator precedence),
// two-space indentation per nesting level, one statement per line,
// and a trailing newline at end of file.
//
// Comments captured by the lexer (prog.Comments) are interleaved
// with the AST during emit:
//
//   - A comment whose source line is BEFORE the next statement's
//     line emits as a separate leading line at the statement's
//     indent level.
//   - A comment whose source line equals the just-emitted single-
//     line statement's line emits inline as `  // text`.
//   - Comments after the last statement of a block emit before the
//     closing brace at the block's indent.
//   - Comments at end-of-file (after the last declaration) emit at
//     depth zero.
//
// Blank lines between statements are preserved as a single separator:
// the parser records whitespace-only source lines on prog.BlankLines,
// and formatBlock emits one blank line before a statement whose source
// had a blank immediately above it (runs collapse to one; a leading
// blank just inside `{` is dropped). Top-level declarations are always
// blank-separated.
//
// Format → parse → Format is byte-stable: a second pass produces
// identical output (the blank-line rule is local and idempotent).
// parse → Format → parse round-trips the AST shape.
package printer

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

// Format returns idiomatic source text for prog.
func Format(prog *ast.Program) string {
	blanks := make(map[int]bool, len(prog.BlankLines))
	for _, ln := range prog.BlankLines {
		blanks[ln] = true
	}
	f := &formatter{comments: prog.Comments, blanks: blanks}
	// Imports cluster at the top of the file with no blank line
	// between consecutive ones — they read like a single block
	// that introduces the module's dependencies. A blank line
	// follows the last import before the first decl, same shape
	// the inter-decl loop produces below.
	if len(prog.Imports) > 0 {
		for _, imp := range prog.Imports {
			f.drainLeading(imp.P.Line, 0)
			f.b.WriteString(`import "`)
			f.b.WriteString(imp.Path)
			f.b.WriteString(`"`)
			if imp.Alias != "" {
				f.b.WriteString(" as ")
				f.b.WriteString(imp.Alias)
			}
			f.b.WriteString(`;`)
			f.emitTrailing(imp.P.Line)
			f.b.WriteByte('\n')
		}
	}
	// `pub use` re-exports cluster with the imports at the top of the
	// file — same dependency-introduction role.
	for _, pu := range prog.PubUses {
		f.drainLeading(pu.P.Line, 0)
		f.b.WriteString(`pub use "`)
		f.b.WriteString(pu.Path)
		f.b.WriteString(`".{`)
		for i, name := range pu.Names {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.b.WriteString(name)
		}
		f.b.WriteString(`};`)
		f.emitTrailing(pu.P.Line)
		f.b.WriteByte('\n')
	}
	written := len(prog.Imports) > 0 || len(prog.PubUses) > 0
	// Impl methods are also present in prog.Funcs (the parser flattens
	// them there for the checker). Collect them so the funcs below are
	// skipped — they render inside their `impl { … }` block.
	implMethods := map[*ast.FuncDecl]bool{}
	for _, id := range prog.Impls {
		for _, m := range id.Methods {
			implMethods[m] = true
		}
	}
	// Declarations emit in SOURCE ORDER, not grouped by kind.
	//
	// The kind-grouped form (every struct, then every enum, then …)
	// silently RELOCATED COMMENTS, because the comment cursor is monotonic
	// over source-ordered comments while the emission order was not: a
	// declaration printed first but positioned later in the source drained
	// every pending comment above it, including comments documenting the
	// declarations that had not been printed yet. So a comment trailing an
	// enum variant reappeared above a struct written after the enum,
	// attached to something it says nothing true about — and `-fmt -w` put
	// that on disk (#6335). Emitting in source order keeps the cursor in
	// step with the text, which fixes the attachment at its cause instead
	// of teaching each element printer to carry its own trailing comments.
	//
	// It also drops the reordering itself, which was never a stated
	// contract — the syntax reference promises only that the formatter
	// "preserves their original position" for comments — and which made a
	// `-fmt -d` diff on an unformatted file far larger than the change it
	// was reporting.
	type topDecl struct {
		p    ast.Position
		emit func()
	}
	decls := make([]topDecl, 0,
		len(prog.Structs)+len(prog.Enums)+len(prog.Unions)+len(prog.Resources)+
			len(prog.Traits)+len(prog.Impls)+len(prog.Consts)+len(prog.Funcs))
	for _, sd := range prog.Structs {
		decls = append(decls, topDecl{sd.P, func() { f.formatStructDecl(sd) }})
	}
	for _, ed := range prog.Enums {
		decls = append(decls, topDecl{ed.P, func() { f.formatEnumDecl(ed) }})
	}
	for _, ud := range prog.Unions {
		decls = append(decls, topDecl{ud.P, func() { f.formatUnionDecl(ud) }})
	}
	for _, rd := range prog.Resources {
		decls = append(decls, topDecl{rd.P, func() { f.formatResourceDecl(rd) }})
	}
	for _, td := range prog.Traits {
		decls = append(decls, topDecl{td.P, func() { f.formatTraitDecl(td) }})
	}
	for _, id := range prog.Impls {
		decls = append(decls, topDecl{id.P, func() { f.formatImplDecl(id) }})
	}
	for _, cd := range prog.Consts {
		decls = append(decls, topDecl{cd.P, func() { f.formatConstDecl(cd) }})
	}
	for _, fn := range prog.Funcs {
		if implMethods[fn] {
			continue
		}
		decls = append(decls, topDecl{fn.P, func() { f.formatFunc(fn, 0) }})
	}
	// Stable, so two declarations the parser gave the same position (a
	// desugar that synthesises one from another) keep the order the
	// per-kind slices had rather than swapping run to run.
	sort.SliceStable(decls, func(i, j int) bool {
		if decls[i].p.Line != decls[j].p.Line {
			return decls[i].p.Line < decls[j].p.Line
		}
		return decls[i].p.Col < decls[j].p.Col
	})
	for _, d := range decls {
		if written {
			f.b.WriteByte('\n')
		}
		f.drainLeading(d.p.Line, 0)
		d.emit()
		written = true
	}
	// Trailing comments past the last declaration emit at depth 0.
	f.drainAll(0)
	return f.b.String()
}

const formatIndent = "  "

// formatter bundles the output buffer and the comment cursor so
// helpers can drain leading / inline trailing comments without
// each one having to thread two extra arguments.
type formatter struct {
	b        strings.Builder
	comments []ast.Comment
	ci       int          // index of the next un-emitted comment in comments
	blanks   map[int]bool // 1-based source lines that were blank
}

// blankBefore reports whether the source had a blank line immediately
// above the construct at source line `line` (accounting for any leading
// comment block, whose first line is `topLine`). The rule is local —
// "is the line directly above blank?" — so it collapses runs of blank
// lines to one and stays idempotent across Format passes.
func (f *formatter) blankBefore(line int) bool {
	top := line
	if f.ci < len(f.comments) && f.comments[f.ci].Pos.Line < line {
		top = f.comments[f.ci].Pos.Line
	}
	return f.blanks[top-1]
}

// drainLeading emits every still-pending comment whose source line
// is strictly before `line` as its own indented line. Used before
// each statement / declaration to cover comments written above it
// in the source. Same-line comments stay queued for emitTrailing.
func (f *formatter) drainLeading(line, depth int) {
	for f.ci < len(f.comments) && f.comments[f.ci].Pos.Line < line {
		f.indent(depth)
		f.b.WriteString("//")
		f.b.WriteString(f.comments[f.ci].Text)
		f.b.WriteByte('\n')
		f.ci++
	}
}

// emitTrailing emits a comment that lives on the same source line
// as the statement we just finished writing — `putchar(70);  // F`
// style. Caller passes the statement's source line; if the next
// queued comment matches, we emit `  //` + text (no newline; the
// surrounding loop's `\n` follows).
func (f *formatter) emitTrailing(line int) {
	if f.ci < len(f.comments) && f.comments[f.ci].Pos.Line == line {
		f.b.WriteString("  //")
		f.b.WriteString(f.comments[f.ci].Text)
		f.ci++
	}
}

// innerCommentPending reports whether a pending comment falls INSIDE a
// declaration's source span — after its opening line, at or before `last`
// (the last element's line). It is the signal that the one-line rendering
// would strand the comment: nothing inside that rendering can emit it, so
// it survives in the queue and is drained by whatever is printed next,
// reappearing attached to an unrelated declaration (#6335).
//
// The scan starts at the cursor and stops at the first comment past `last`,
// so it is O(comments in the span) rather than O(all comments).
func (f *formatter) innerCommentPending(declLine, last int) bool {
	for i := f.ci; i < len(f.comments); i++ {
		ln := f.comments[i].Pos.Line
		if ln > last {
			return false
		}
		if ln > declLine {
			return true
		}
	}
	return false
}

// drainAll flushes every remaining comment at the supplied indent.
// Used at end-of-file to catch trailing comments past the last
// declaration, and inside blocks to flush comments between the
// last statement and the closing brace.
func (f *formatter) drainAll(depth int) {
	for f.ci < len(f.comments) {
		f.indent(depth)
		f.b.WriteString("//")
		f.b.WriteString(f.comments[f.ci].Text)
		f.b.WriteByte('\n')
		f.ci++
	}
}

// indent writes n levels of two-space indentation.
func (f *formatter) indent(n int) {
	for i := 0; i < n; i++ {
		f.b.WriteString(formatIndent)
	}
}

// formatConstDecl emits a top-level `const NAME[: T] = expr;` on a
// single line. The type annotation is preserved when the source had
// one and elided when it didn't, matching the parser's optional
// shape so format → parse → format stays stable.
func (f *formatter) formatConstDecl(cd *ast.ConstDecl) {
	if cd.PackageScoped {
		f.b.WriteString("pub(package) ")
	} else if cd.Public {
		f.b.WriteString("pub ")
	}
	f.b.WriteString("const ")
	f.b.WriteString(cd.Name)
	if cd.Type != nil {
		f.b.WriteString(": ")
		f.b.WriteString(formatType(cd.Type))
	}
	f.b.WriteString(" = ")
	f.formatExpr(cd.Value, precLowest)
	f.b.WriteString(";\n")
}

// formatEnumDecl emits `enum Foo { Bar, Baz(T1, T2), … }` on a
// single line (one variant per `,`). The block-style multi-line
// form is a follow-up; for now this matches the parser-accepted
// shape and keeps round-trips byte-stable for short enums.
func (f *formatter) formatEnumDecl(ed *ast.EnumDecl) {
	f.writeDeriveAttr(ed.Derives)
	f.writeMustConsumeAttr(ed.MustConsume)
	if ed.PackageScoped {
		f.b.WriteString("pub(package) ")
	} else if ed.Public {
		f.b.WriteString("pub ")
	}
	f.b.WriteString("enum ")
	f.b.WriteString(ed.Name)
	if len(ed.TypeParams) > 0 {
		f.b.WriteByte('[')
		for i, p := range ed.TypeParams {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.b.WriteString(p)
		}
		f.b.WriteByte(']')
	}
	// A comment inside the braces forces the MULTI-LINE form: the one-liner
	// has nowhere to put it, so it would be stranded in the queue and
	// re-emitted above the next declaration (#6335). One variant per line
	// gives each its own drainLeading / emitTrailing pair, which is what
	// keeps `Unclosed(i32),  // opener never closed` on `Unclosed`.
	if len(ed.Variants) > 0 && f.innerCommentPending(ed.P.Line, ed.Variants[len(ed.Variants)-1].P.Line) {
		f.b.WriteString(" {\n")
		for _, v := range ed.Variants {
			f.drainLeading(v.P.Line, 1)
			f.b.WriteString(formatIndent)
			f.writeEnumVariant(v)
			f.b.WriteByte(',')
			f.emitTrailing(v.P.Line)
			f.b.WriteByte('\n')
		}
		f.b.WriteString("}\n")
		return
	}
	f.b.WriteString(" { ")
	for i, v := range ed.Variants {
		if i > 0 {
			f.b.WriteString(", ")
		}
		f.writeEnumVariant(v)
	}
	f.b.WriteString(" }\n")
}

// writeEnumVariant emits one variant — `Name`, `Name(T, U)` or the
// record form `Name { f: T }` — with no separator or indent of its own,
// so the one-line and multi-line enum renderings share it.
func (f *formatter) writeEnumVariant(v ast.EnumVariant) {
	f.b.WriteString(v.Name)
	if len(v.FieldNames) > 0 {
		f.b.WriteString(" { ")
		for j, fn := range v.FieldNames {
			if j > 0 {
				f.b.WriteString(", ")
			}
			f.b.WriteString(fn)
			f.b.WriteString(": ")
			f.b.WriteString(formatType(v.Payloads[j]))
		}
		f.b.WriteString(" }")
	} else if len(v.Payloads) > 0 {
		f.b.WriteByte('(')
		for j, p := range v.Payloads {
			if j > 0 {
				f.b.WriteString(", ")
			}
			f.b.WriteString(formatType(p))
		}
		f.b.WriteByte(')')
	}
}

// formatUnionDecl emits `type Name = A | B | C;` on a single
// line. Round-trips the source shape verbatim — members are
// preserved in declaration order (matches the checker desugar
// that stamps variant tags by member index, so a reorder here
// would shift the tag map). Generic unions aren't supported
// yet (see the punted-follow-up note on UnionDecl); when they
// land this needs to spell the `[T, U]` parameter list too.
func (f *formatter) formatUnionDecl(ud *ast.UnionDecl) {
	if ud.PackageScoped {
		f.b.WriteString("pub(package) ")
	} else if ud.Public {
		f.b.WriteString("pub ")
	}
	f.b.WriteString("type ")
	f.b.WriteString(ud.Name)
	f.b.WriteString(" = ")
	for i, m := range ud.Members {
		if i > 0 {
			f.b.WriteString(" | ")
		}
		f.b.WriteString(m)
	}
	f.b.WriteString(";\n")
}

// writeDeriveAttr emits `@derive(Trait, …)` on its own line when the decl
// carries derives. Dropping it (the pre-existing default) silently removed the
// derived trait impls — a semantics change `fern -fmt` would bake in.
func (f *formatter) writeDeriveAttr(derives []string) {
	if len(derives) == 0 {
		return
	}
	f.b.WriteString("@derive(")
	for i, d := range derives {
		if i > 0 {
			f.b.WriteString(", ")
		}
		f.b.WriteString(d)
	}
	f.b.WriteString(")\n")
}

// writeMustConsumeAttr emits `@must_consume` on its own line. Dropping it (the
// pre-existing default) disarmed E067: the obligation walk keys on the
// attribute, so a formatted file type-checked CLEAN where the original was
// rejected — the same class of semantics change writeDeriveAttr exists to stop.
func (f *formatter) writeMustConsumeAttr(mustConsume bool) {
	if !mustConsume {
		return
	}
	f.b.WriteString("@must_consume\n")
}

// writeTypeParams emits a generic parameter list `[A, B: Trait + Other,
// C: From[i32]]`. `bounds` maps a parameter name to its trait bounds
// (nil / absent for an unbounded param), and `boundArgs` carries the
// type arguments of a generic-trait bound (`From[i32]`), parallel to
// `bounds`. Structs pass nil for both (struct params are never bounded).
// Emitting nothing for an empty list keeps non-generic decls unchanged.
func (f *formatter) writeTypeParams(names []string, bounds map[string][]string, boundArgs map[string][][]ast.Type) {
	if len(names) == 0 {
		return
	}
	f.b.WriteByte('[')
	for i, name := range names {
		if i > 0 {
			f.b.WriteString(", ")
		}
		f.b.WriteString(name)
		bs := bounds[name]
		if len(bs) == 0 {
			continue
		}
		f.b.WriteString(": ")
		for j, bt := range bs {
			if j > 0 {
				f.b.WriteString(" + ")
			}
			f.b.WriteString(bt)
			if boundArgs != nil {
				if args := boundArgs[name]; j < len(args) && len(args[j]) > 0 {
					f.b.WriteByte('[')
					for k, at := range args[j] {
						if k > 0 {
							f.b.WriteString(", ")
						}
						f.b.WriteString(formatType(at))
					}
					f.b.WriteByte(']')
				}
			}
		}
	}
	f.b.WriteByte(']')
}

func (f *formatter) formatStructDecl(sd *ast.StructDecl) {
	f.writeDeriveAttr(sd.Derives)
	f.writeMustConsumeAttr(sd.MustConsume)
	if sd.PackageScoped {
		f.b.WriteString("pub(package) ")
	} else if sd.Public {
		f.b.WriteString("pub ")
	}
	f.b.WriteString("struct ")
	f.b.WriteString(sd.Name)
	f.writeTypeParams(sd.TypeParams, nil, nil)
	// A comment inside the braces forces the MULTI-LINE form, for the same
	// reason the enum printer does — see #6335 and innerCommentPending. A
	// synthetic field (NamePos zero) has no line to attach to, so a struct
	// carrying one keeps the one-liner.
	if n := len(sd.Fields); n > 0 && sd.Fields[n-1].NamePos.Line > 0 &&
		f.innerCommentPending(sd.P.Line, sd.Fields[n-1].NamePos.Line) {
		f.b.WriteString(" {\n")
		for _, fld := range sd.Fields {
			f.drainLeading(fld.NamePos.Line, 1)
			f.b.WriteString(formatIndent)
			f.b.WriteString(fld.Name)
			f.b.WriteString(": ")
			f.b.WriteString(formatType(fld.Type))
			f.b.WriteByte(',')
			f.emitTrailing(fld.NamePos.Line)
			f.b.WriteByte('\n')
		}
		f.b.WriteString("}\n")
		return
	}
	f.b.WriteString(" { ")
	for i, fld := range sd.Fields {
		if i > 0 {
			f.b.WriteString(", ")
		}
		f.b.WriteString(fld.Name)
		f.b.WriteString(": ")
		f.b.WriteString(formatType(fld.Type))
	}
	f.b.WriteString(" }\n")
}

// formatTraitDecl emits `trait Name[T]: Super { type Assoc; method sigs }`
// across multiple lines. Associated types are emitted before methods
// (source interleaving isn't retained on TraitDecl); the ordering is
// stable, so Format stays idempotent. A method with a default body
// renders that body; an abstract one ends at `;`.
func (f *formatter) formatTraitDecl(td *ast.TraitDecl) {
	if td.PackageScoped {
		f.b.WriteString("pub(package) ")
	} else if td.Public {
		f.b.WriteString("pub ")
	}
	f.b.WriteString("trait ")
	f.b.WriteString(td.Name)
	f.writeTypeParams(td.TypeParams, nil, nil)
	if len(td.Supertraits) > 0 {
		f.b.WriteString(": ")
		f.b.WriteString(strings.Join(td.Supertraits, " + "))
	}
	if len(td.AssocTypes) == 0 && len(td.Methods) == 0 {
		f.b.WriteString(" {}\n")
		return
	}
	f.b.WriteString(" {\n")
	for _, at := range td.AssocTypes {
		f.indent(1)
		f.b.WriteString("type ")
		f.b.WriteString(at)
		f.b.WriteString(";\n")
	}
	for _, m := range td.Methods {
		f.formatTraitMethod(m)
	}
	f.b.WriteString("}\n")
}

// formatTraitMethod emits one trait method signature at one indent
// level: `function name(self: Self, …): R;` for an abstract method, or
// with a `{ … }` body for a default method. An associated function
// (Assoc) has no `self` receiver — its params are emitted verbatim.
func (f *formatter) formatTraitMethod(m ast.TraitMethod) {
	f.indent(1)
	f.b.WriteString("function ")
	f.b.WriteString(m.Name)
	f.b.WriteByte('(')
	for i, p := range m.Params {
		if i > 0 {
			f.b.WriteString(", ")
		}
		if p.Own {
			f.b.WriteString("own ")
		}
		f.b.WriteString(writtenName(p.Name))
		f.b.WriteString(": ")
		f.b.WriteString(formatType(p.Type))
	}
	f.b.WriteByte(')')
	if m.Result != nil {
		f.b.WriteString(": ")
		f.b.WriteString(formatType(m.Result))
	}
	if m.Body != nil {
		f.b.WriteByte(' ')
		f.formatBlock(m.Body, 1)
		f.b.WriteByte('\n')
		return
	}
	f.b.WriteString(";\n")
}

// formatImplDecl emits `impl[T] Trait[Args] for Type { … }` (or an
// inherent `impl Type { … }`), with associated-type bindings first
// (sorted for a stable, idempotent order) then the methods. Methods are
// the desugared forms stashed on the ImplDecl: `Self` already reads as
// the concrete impl type, and an ordinary method's `self` receiver is
// re-inserted as its first parameter (the shape an impl block requires).
func (f *formatter) formatImplDecl(id *ast.ImplDecl) {
	f.b.WriteString("impl")
	f.writeTypeParams(id.TypeParams, id.Bounds, nil)
	f.b.WriteByte(' ')
	if id.Trait != "" {
		f.b.WriteString(id.Trait)
		if len(id.TraitArgs) > 0 {
			f.b.WriteByte('[')
			for i, a := range id.TraitArgs {
				if i > 0 {
					f.b.WriteString(", ")
				}
				f.b.WriteString(formatType(a))
			}
			f.b.WriteByte(']')
		}
		f.b.WriteString(" for ")
	}
	f.b.WriteString(formatType(id.Type))
	if len(id.AssocTypeBindings) == 0 && len(id.Methods) == 0 {
		f.b.WriteString(" {}\n")
		return
	}
	f.b.WriteString(" {\n")
	// Sorted keys keep the emit order stable across passes.
	names := make([]string, 0, len(id.AssocTypeBindings))
	for name := range id.AssocTypeBindings {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f.indent(1)
		f.b.WriteString("type ")
		f.b.WriteString(name)
		f.b.WriteString(" = ")
		f.b.WriteString(formatType(id.AssocTypeBindings[name]))
		f.b.WriteString(";\n")
	}
	// A parametric impl (`impl[T] …`) shares its type params with every
	// method, so the methods must NOT respell them; a plain impl's method
	// keeps its own.
	parametric := len(id.TypeParams) > 0
	for _, m := range id.Methods {
		f.formatImplMethod(m, parametric)
	}
	f.b.WriteString("}\n")
}

// formatImplMethod emits one desugared impl method inside its block. An
// ordinary method (Receiver set) gets `self: <type>` re-inserted as its
// first parameter; an associated function (AssocType set, no receiver)
// keeps its parameters as-is. Type params are respelled only for a
// non-parametric impl.
func (f *formatter) formatImplMethod(fn *ast.FuncDecl, parametric bool) {
	f.indent(1)
	f.b.WriteString("function ")
	f.b.WriteString(fn.Name)
	if !parametric {
		f.writeTypeParams(fn.TypeParams, fn.Bounds, fn.BoundArgs)
	}
	f.b.WriteByte('(')
	wrote := false
	if fn.Receiver != nil {
		if fn.Receiver.Own {
			f.b.WriteString("own ")
		}
		f.b.WriteString(fn.Receiver.Name)
		f.b.WriteString(": ")
		f.b.WriteString(formatType(fn.Receiver.Type))
		wrote = true
	}
	for _, p := range fn.Params {
		if wrote {
			f.b.WriteString(", ")
		}
		if p.Own {
			f.b.WriteString("own ")
		}
		f.b.WriteString(writtenName(p.Name))
		f.b.WriteString(": ")
		f.b.WriteString(formatType(p.Type))
		wrote = true
	}
	f.b.WriteByte(')')
	if fn.ReturnType != nil {
		f.b.WriteString(": ")
		f.b.WriteString(formatType(fn.ReturnType))
	}
	f.b.WriteByte(' ')
	f.formatBlock(fn.Body, 1)
	f.b.WriteByte('\n')
}

// formatResourceDecl emits a `resource Name;` declaration (P5 WIT
// resource-handle type), with its optional `@import(iface, wit-name)` binding
// on the line above — mirroring the body-less `@import` extern rendering.
func (f *formatter) formatResourceDecl(rd *ast.ResourceDecl) {
	if rd.ImportIface != "" {
		f.b.WriteString("@import(\"")
		f.b.WriteString(rd.ImportIface)
		f.b.WriteString("\", \"")
		f.b.WriteString(rd.ImportWITName)
		f.b.WriteString("\")\n")
	}
	if rd.PackageScoped {
		f.b.WriteString("pub(package) ")
	} else if rd.Public {
		f.b.WriteString("pub ")
	}
	f.b.WriteString("resource ")
	f.b.WriteString(rd.Name)
	f.b.WriteString(";\n")
}

// formatFunc emits a top-level or nested function declaration.
// Receiver clauses go between `function` and the name; the body
// uses multi-line block formatting at the supplied indent level.
func (f *formatter) formatFunc(fn *ast.FuncDecl, depth int) {
	// A body-less `@import` extern (bring-your-own WIT, P4) renders the
	// attribute on its own line above the signature, which ends with `;`.
	// Dropping it (the pre-P4 default) silently turned an extern into an
	// empty-body function — a semantics change.
	if fn.ImportIface != "" {
		f.indent(depth)
		f.b.WriteString("@import(\"")
		f.b.WriteString(fn.ImportIface)
		f.b.WriteString("\", \"")
		f.b.WriteString(fn.ImportWITName)
		f.b.WriteString("\")\n")
	}
	// An `@export` function (P6) renders its binding on its own line above the
	// signature, like `@import` but the function keeps its body.
	if fn.ExportIface != "" {
		f.indent(depth)
		f.b.WriteString("@export(\"")
		f.b.WriteString(fn.ExportIface)
		f.b.WriteString("\", \"")
		f.b.WriteString(fn.ExportWITName)
		f.b.WriteString("\")\n")
	}
	// `@inline` / `@noinline` (#4412 Rec §14) render on their own line —
	// dropping one would silently change the release build's inlining.
	switch fn.InlineHint {
	case ast.InlineHintAlways:
		f.indent(depth)
		f.b.WriteString("@inline\n")
	case ast.InlineHintNever:
		f.indent(depth)
		f.b.WriteString("@noinline\n")
	}
	f.indent(depth)
	if fn.PackageScoped {
		f.b.WriteString("pub(package) ")
	} else if fn.Public {
		f.b.WriteString("pub ")
	}
	// Contextual modifiers: `fip` / `fbip` (with an optional graded
	// allowance) and `async`. Dropping one silently changes semantics —
	// the checked in-place guarantee (E053/E068) or the P3 async export.
	if fn.Fip || fn.Fbip {
		if fn.Fbip {
			f.b.WriteString("fbip")
		} else {
			f.b.WriteString("fip")
		}
		if fn.FipAllowance > 0 {
			f.b.WriteByte('(')
			f.b.WriteString(strconv.Itoa(fn.FipAllowance))
			f.b.WriteByte(')')
		}
		f.b.WriteByte(' ')
	}
	if fn.Async {
		f.b.WriteString("async ")
	}
	f.b.WriteString("function ")
	if fn.Receiver != nil {
		// A method's type parameters go in leading position, before
		// the receiver, so the receiver type (`Box[T]`) can reference
		// them — the shape the parser documents as canonical.
		if len(fn.TypeParams) > 0 {
			f.writeTypeParams(fn.TypeParams, fn.Bounds, fn.BoundArgs)
			f.b.WriteByte(' ')
		}
		f.b.WriteByte('(')
		if fn.Receiver.Own {
			f.b.WriteString("own ")
		}
		f.b.WriteString(fn.Receiver.Name)
		f.b.WriteString(": ")
		f.b.WriteString(formatType(fn.Receiver.Type))
		f.b.WriteString(") ")
	}
	f.b.WriteString(fn.Name)
	// A free function spells its type parameters after the name
	// (`function name[T](x: T): T`) — the dominant in-source style.
	if fn.Receiver == nil {
		f.writeTypeParams(fn.TypeParams, fn.Bounds, fn.BoundArgs)
	}
	f.b.WriteByte('(')
	for i, p := range fn.Params {
		if i > 0 {
			f.b.WriteString(", ")
		}
		// `own` is checked, not decorative: dropping it made E053 fire on the
		// formatted `fip` sort helpers, which pass their array `own` precisely
		// so `with` is in-place.
		if p.Own {
			f.b.WriteString("own ")
		}
		f.b.WriteString(writtenName(p.Name))
		f.b.WriteString(": ")
		f.b.WriteString(formatType(p.Type))
	}
	f.b.WriteByte(')')
	if fn.ReturnType != nil {
		f.b.WriteString(": ")
		f.b.WriteString(formatType(fn.ReturnType))
	}
	if fn.ImportIface != "" {
		f.b.WriteString(";\n")
		return
	}
	f.b.WriteByte(' ')
	f.formatBlock(fn.Body, depth)
	f.b.WriteByte('\n')
}

// formatBlock emits `{` on the current line, then each statement on
// its own line at depth+1, then `}` indented to depth. Empty blocks
// stay one-liners. Pending comments that fall inside the block but
// before its statements get drained at the right indent.
func (f *formatter) formatBlock(blk *ast.Block, depth int) {
	if blk == nil || len(blk.Stmts) == 0 {
		// Even an empty block can host comments — but supporting
		// `{ /* comment */ }` would force a multi-line empty block
		// in cases where comments have nothing to attach to.
		// Keep the one-liner for now; standalone comments inside
		// empty blocks fall through to the parent's drain.
		f.b.WriteString("{}")
		return
	}
	f.b.WriteString("{\n")
	for i, s := range blk.Stmts {
		// Preserve an author's blank-line separator between statements
		// (never a leading blank just inside the opening brace).
		if i > 0 && f.blankBefore(s.Pos().Line) {
			f.b.WriteByte('\n')
		}
		f.formatStmtLine(s, depth+1)
		f.b.WriteByte('\n')
	}
	// Comments past the last statement but still "inside" the
	// block — i.e. before its closing brace — emit at the inner
	// indent. We don't track the block's end position so we just
	// drain everything that's still queued and at a position past
	// the last statement; comments that belong to outer scopes
	// will exceed the block's range when drained at the outer
	// recursion level.
	f.indent(depth)
	f.b.WriteByte('}')
}

// formatStmtLine emits one statement's line content — leading comments,
// indentation, the statement itself, and any inline trailing comment —
// without a terminating newline, so the caller decides how lines join.
// Shared by formatBlock and the `let … else` continuation, whose
// statements are siblings of the binding rather than a nested block.
func (f *formatter) formatStmtLine(s ast.Stmt, depth int) {
	f.drainLeading(s.Pos().Line, depth)
	f.indent(depth)
	f.formatStmt(s, depth)
	// If the statement just emitted is a single-line shape and the next
	// queued comment shares its source line, emit it inline.
	if isSingleLineStmt(s) {
		f.emitTrailing(s.Pos().Line)
	}
}

// isSingleLineStmt reports whether s emits as a single source line
// — only those are eligible for an inline trailing comment.
// Compound statements (if / while / for / switch / function /
// nested block) span multiple lines and any same-line comment is
// against their opening header rather than their body, which the
// formatter doesn't attach yet.
func isSingleLineStmt(s ast.Stmt) bool {
	switch s.(type) {
	case *ast.Return, *ast.Var, *ast.ExprStmt, *ast.Break, *ast.Continue, *ast.Defer:
		return true
	}
	return false
}

// formatBranch renders an `if`-expression branch — always brace-wrapped
// (`{ … }`), since an `if`-expr branch is syntactically a block. A bare
// expression branch renders `{ e }` (byte-identical to the pre-block-expr
// formatter, which wrote the braces itself); a BlockExpr renders its
// statements + trailing value compactly via formatBlockExpr.
func (f *formatter) formatBranch(e ast.Expr) {
	if be, ok := e.(*ast.BlockExpr); ok {
		f.formatBlockExpr(be)
		return
	}
	f.b.WriteString("{ ")
	f.formatExpr(e, 0)
	f.b.WriteString(" }")
}

// formatArmBody renders a `match`-expression arm body. A BlockExpr body
// is brace-wrapped (`{ stmts; tail }`); a bare expression body stays
// unbraced — keeping existing single-expr arms byte-identical.
func (f *formatter) formatArmBody(e ast.Expr) {
	if be, ok := e.(*ast.BlockExpr); ok {
		f.formatBlockExpr(be)
		return
	}
	f.formatExpr(e, precLowest)
}

// formatBlockExpr renders a block-expression `{ stmt; …; tail }` in a
// compact one-line shape: each statement followed by `;`, then the
// trailing value expression with no `;`. Used for `if`/`match`
// expression branches (slice 1). Statements here are the simple forms
// the branch grammar admits (var / expr-stmt / nested control), emitted
// via formatStmtInline so no newlines / indentation are introduced.
func (f *formatter) formatBlockExpr(be *ast.BlockExpr) {
	f.b.WriteString("{ ")
	for _, s := range be.Stmts {
		f.formatStmt(s, 0)
		f.b.WriteByte(' ')
	}
	if be.Tail != nil {
		f.formatExpr(be.Tail, 0)
		f.b.WriteByte(' ')
	}
	f.b.WriteByte('}')
}

// formatLoopLabel emits `name: ` ahead of a loop header. Dropping it also
// strips the `break name` / `continue name` that target it, so the reformatted
// program jumps out of a different loop than the one written.
func (f *formatter) formatLoopLabel(label string) {
	if label == "" {
		return
	}
	f.b.WriteString(label)
	f.b.WriteString(": ")
}

// formatJumpLabel closes a `break` / `continue`, with the label it targets.
func (f *formatter) formatJumpLabel(label string) {
	if label != "" {
		f.b.WriteByte(' ')
		f.b.WriteString(label)
	}
	f.b.WriteByte(';')
}

// formatForEach emits the three `for … in …` surface forms: the plain
// iterator `for x in xs`, the range `for i in lo..hi` (`..=` when inclusive),
// and the map destructuring `for (k, v) in m`.
func (f *formatter) formatForEach(fe *ast.ForEach, depth int) {
	f.formatLoopLabel(fe.Label)
	f.b.WriteString("for ")
	if fe.Pattern != nil {
		f.formatDestructurePattern(fe.Pattern)
	} else {
		f.b.WriteString(fe.Var)
	}
	f.b.WriteString(" in ")
	f.formatExpr(fe.Iter, precLowest)
	if fe.RangeHigh != nil {
		if fe.RangeIncl {
			f.b.WriteString("..=")
		} else {
			f.b.WriteString("..")
		}
		f.formatExpr(fe.RangeHigh, precLowest)
	}
	f.b.WriteByte(' ')
	f.formatStmt(fe.Body, depth)
}

func (f *formatter) formatStmt(s ast.Stmt, depth int) {
	switch x := s.(type) {
	case *ast.Block:
		// A Block the parser built to scope a `for … in …` desugar reprints
		// as that loop: its statements are synthetic bindings whose names
		// (`__foreach_iter_1`, `__range_hi_1`) would otherwise be written
		// into the user's source, and `-fmt -w` makes that permanent.
		if x.Sugar != nil {
			f.formatForEach(x.Sugar, depth)
			return
		}
		f.formatBlock(x, depth)
	case *ast.ForEach:
		// A lazy `stream[T]` foreach is left undesugared for the checker, so
		// this form does reach the printer directly.
		f.formatForEach(x, depth)
	case *ast.If:
		f.b.WriteString("if (")
		f.formatExpr(x.Cond, precLowest)
		f.b.WriteString(") ")
		f.formatStmt(x.Then, depth)
		if x.Else != nil {
			f.b.WriteString(" else ")
			f.formatStmt(x.Else, depth)
		}
	case *ast.While:
		f.formatLoopLabel(x.Label)
		f.b.WriteString("while (")
		f.formatExpr(x.Cond, precLowest)
		f.b.WriteString(") ")
		f.formatStmt(x.Body, depth)
	case *ast.Loop:
		// A parser-synthesised `todo` stub re-prints as its sugar
		// (`todo;` / `todo("msg");`), not the desugared
		// `loop { eprint(...); exit(101); }` body — unlike `assert`,
		// the todo marker is a workflow inventory the formatter
		// must not erase. TodoMsg is the original message expression.
		if x.IsTodo {
			if x.TodoMsg != nil {
				f.b.WriteString("todo(")
				f.formatExpr(x.TodoMsg, precLowest)
				f.b.WriteString(");")
			} else {
				f.b.WriteString("todo;")
			}
			return
		}
		f.formatLoopLabel(x.Label)
		f.b.WriteString("loop ")
		f.formatStmt(x.Body, depth)
	case *ast.For:
		f.formatLoopLabel(x.Label)
		f.b.WriteString("for (")
		if x.Init != nil {
			f.formatStmt(x.Init, depth)
		} else {
			f.b.WriteByte(';')
		}
		f.b.WriteByte(' ')
		f.formatExpr(x.Cond, precLowest)
		f.b.WriteString("; ")
		if x.Step != nil {
			if es, ok := x.Step.(*ast.ExprStmt); ok {
				f.formatExpr(es.Expr, precLowest)
			} else {
				f.formatStmt(x.Step, depth)
			}
		}
		f.b.WriteString(") ")
		f.formatStmt(x.Body, depth)
	case *ast.Break:
		f.b.WriteString("break")
		f.formatJumpLabel(x.Label)
	case *ast.Continue:
		f.b.WriteString("continue")
		f.formatJumpLabel(x.Label)
	case *ast.Return:
		f.b.WriteString("return")
		if x.Value != nil {
			f.b.WriteByte(' ')
			f.formatExpr(x.Value, precLowest)
		}
		f.b.WriteByte(';')
	case *ast.Var:
		f.b.WriteString("var ")
		f.b.WriteString(writtenName(x.Name))
		if x.Type != nil {
			f.b.WriteString(": ")
			f.b.WriteString(formatType(x.Type))
		}
		f.b.WriteString(" = ")
		f.formatExpr(x.Init, precLowest)
		f.b.WriteByte(';')
	case *ast.Destructure:
		// Struct mode (Fields set) binds BY FIELD NAME; rendering it as the
		// positional tuple form would re-parse to a different program.
		if x.Fields != nil {
			f.b.WriteString("let ")
			f.b.WriteString(x.StructName)
			f.b.WriteString(" { ")
			for i, field := range x.Fields {
				if i > 0 {
					f.b.WriteString(", ")
				}
				f.b.WriteString(field)
				if i < len(x.Names) && x.Names[i] != field {
					f.b.WriteString(": ")
					f.b.WriteString(writtenName(x.Names[i]))
				}
			}
			f.b.WriteString(" } = ")
			f.formatExpr(x.Init, precLowest)
			f.b.WriteByte(';')
			break
		}
		f.b.WriteString("let ")
		f.formatDestructurePattern(x)
		f.b.WriteString(" = ")
		f.formatExpr(x.Init, precLowest)
		f.b.WriteByte(';')
	case *ast.ExprStmt:
		f.formatExpr(x.Expr, precLowest)
		f.b.WriteByte(';')
	case *ast.Defer:
		if x.OnError {
			f.b.WriteString("errdefer ")
		} else {
			f.b.WriteString("defer ")
		}
		// A block-shaped action `defer { … }` (#5153) prints as the block
		// with no trailing `;` (matching the parse form); a plain expression
		// action keeps its `;`.
		if be, ok := x.Expr.(*ast.BlockExpr); ok {
			f.formatBlockExpr(be)
		} else {
			f.formatExpr(x.Expr, precLowest)
			f.b.WriteByte(';')
		}
	case *ast.Match:
		// A match the parser synthesised from `if let` re-renders as the
		// `if let` the source spelled — the pattern arm is the
		// then-branch, the trailing wildcard arm the else.
		// A match the parser synthesised from `if let` / `let … else`
		// re-renders as the binding form the source spelled: the leading
		// arms are the pattern alternatives, the trailing wildcard arm is
		// the else.
		// Sugar holds the arms as written, when the nested-pattern desugar
		// rewrote them into a merged arm plus an inner match. Reprinting the
		// lowering instead would put the desugar's `__nest` temps into the
		// user's source — permanently, under `-fmt -w`.
		arms := x.Arms
		if x.Sugar != nil {
			arms = x.Sugar
		}
		if x.Origin != "" && len(arms) >= 2 {
			success, els := arms[0].Body, arms[len(arms)-1].Body
			if x.Origin == ast.OriginLetElse {
				f.b.WriteString("let ")
			} else {
				f.b.WriteString("if let ")
			}
			for i, arm := range arms[:len(arms)-1] {
				if i > 0 {
					f.b.WriteString(" | ")
				}
				f.formatArmPattern(arm)
			}
			f.b.WriteString(" = ")
			f.formatExpr(x.Tag, precLowest)
			if x.Origin == ast.OriginLetElse {
				f.b.WriteString(" else ")
				f.formatBlock(els, depth)
				f.b.WriteByte(';')
				// The success arm holds the rest of the enclosing block —
				// where the bindings are live — so its statements re-emit
				// as the siblings they were written as.
				for _, s := range success.Stmts {
					f.b.WriteByte('\n')
					if f.blankBefore(s.Pos().Line) {
						f.b.WriteByte('\n')
					}
					f.formatStmtLine(s, depth)
				}
				return
			}
			f.b.WriteByte(' ')
			f.formatBlock(success, depth)
			if len(els.Stmts) > 0 {
				f.b.WriteString(" else ")
				f.formatBlock(els, depth)
			}
			return
		}
		f.b.WriteString("match (")
		f.formatExpr(x.Tag, precLowest)
		f.b.WriteString(") {\n")
		for i := 0; i < len(arms); i++ {
			arm := arms[i]
			f.indent(depth + 1)
			f.formatArmPattern(arm)
			// `A | B => …` parsed to one arm per alternative with the body
			// cloned into each; the continuations rejoin their head here.
			for i+1 < len(arms) && arms[i+1].AltCont {
				i++
				f.b.WriteString(" | ")
				f.formatArmPattern(arms[i])
			}
			if arm.Guard != nil {
				f.b.WriteString(" when ")
				f.formatExpr(arm.Guard, precLowest)
			}
			f.b.WriteString(" => ")
			f.formatBlock(arm.Body, depth+1)
			if i < len(arms)-1 {
				f.b.WriteByte(',')
			}
			f.b.WriteByte('\n')
		}
		f.indent(depth)
		f.b.WriteByte('}')
	case *ast.FuncDecl:
		f.b.WriteString("function ")
		f.b.WriteString(x.Name)
		f.b.WriteByte('(')
		for i, p := range x.Params {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.b.WriteString(writtenName(p.Name))
			f.b.WriteString(": ")
			f.b.WriteString(formatType(p.Type))
		}
		f.b.WriteByte(')')
		if x.ReturnType != nil {
			f.b.WriteString(": ")
			f.b.WriteString(formatType(x.ReturnType))
		}
		f.b.WriteByte(' ')
		f.formatBlock(x.Body, depth)
	}
}

// formatArmPattern renders one arm's pattern — every shape
// parseMatchPattern accepts, so it serves both `match` arms and the
// `if let` head that shares the grammar.
func (f *formatter) formatArmPattern(arm *ast.MatchArm) {
	if arm.AtBinding != "" {
		f.b.WriteString(arm.AtBinding)
		f.b.WriteString(" @ ")
	}
	switch {
	case arm.IsWildcard:
		f.b.WriteByte('_')
	case arm.Literal != nil:
		f.formatExpr(arm.Literal, precLowest)
		if arm.RangeHi != nil {
			if arm.RangeInclusive {
				f.b.WriteString("..=")
			} else {
				f.b.WriteString("..")
			}
			f.formatExpr(arm.RangeHi, precLowest)
		}
	case arm.TupleElems != nil:
		f.formatTuplePatElems(arm.TupleElems)
	default:
		if arm.VariantModule != "" {
			f.b.WriteString(arm.VariantModule)
			f.b.WriteByte('.')
		}
		f.b.WriteString(arm.VariantName)
		if arm.NamedFields {
			f.b.WriteString(" { ")
			for i, b := range arm.Bindings {
				if i > 0 {
					f.b.WriteString(", ")
				}
				// A field carrying a SUB-PATTERN prints `field: <pat>`; the
				// binder in Bindings[i] is the desugar's temp, not a name the
				// source spelled.
				if sub := armSub(arm, i); sub != nil {
					if i < len(arm.FieldNames) {
						f.b.WriteString(arm.FieldNames[i])
						f.b.WriteString(": ")
					}
					f.formatArmPattern(sub)
					continue
				}
				// `S { field: local }` renames; the shorthand `S { x }`
				// has FieldNames[i] == Bindings[i].
				if i < len(arm.FieldNames) && arm.FieldNames[i] != "" && arm.FieldNames[i] != b {
					f.b.WriteString(arm.FieldNames[i])
					f.b.WriteString(": ")
				}
				f.b.WriteString(b)
			}
			f.b.WriteString(" }")
		} else if len(arm.Bindings) > 0 {
			f.b.WriteByte('(')
			for i, b := range arm.Bindings {
				if i > 0 {
					f.b.WriteString(", ")
				}
				if sub := armSub(arm, i); sub != nil {
					f.formatArmPattern(sub)
					continue
				}
				f.b.WriteString(b)
			}
			f.b.WriteByte(')')
		}
	}
}

// armSub returns the sub-pattern this arm matches against payload slot i, or
// nil when the slot is a plain binder.
func armSub(arm *ast.MatchArm, i int) *ast.MatchArm {
	if i >= len(arm.Sub) {
		return nil
	}
	return arm.Sub[i]
}

// formatTuplePatElems renders a tuple pattern `(p0, p1, …)`.
func (f *formatter) formatTuplePatElems(elems []ast.TuplePatElem) {
	f.b.WriteByte('(')
	for i, el := range elems {
		if i > 0 {
			f.b.WriteString(", ")
		}
		f.formatTuplePatElem(el)
	}
	f.b.WriteByte(')')
}

// formatTuplePatElem renders ONE pattern position. A nested tuple element and
// a variant's payload slot are both the same grammar, so `(a, (b, c))` and
// `(A(Ok(n)), y)` round-trip at any depth.
func (f *formatter) formatTuplePatElem(el ast.TuplePatElem) {
	switch {
	case el.Nested != nil:
		f.formatTuplePatElems(el.Nested)
	case el.IsWildcard:
		f.b.WriteByte('_')
	case el.Literal != nil:
		f.formatExpr(el.Literal, precLowest)
	case el.VariantName != "":
		if el.VariantModule != "" {
			f.b.WriteString(el.VariantModule)
			f.b.WriteByte('.')
		}
		f.b.WriteString(el.VariantName)
		f.b.WriteByte('(')
		for j, vb := range el.VariantBindings {
			if j > 0 {
				f.b.WriteString(", ")
			}
			if j < len(el.VariantPayloads) && el.VariantPayloads[j] != nil {
				f.formatTuplePatElem(*el.VariantPayloads[j])
				continue
			}
			f.b.WriteString(vb)
		}
		f.b.WriteByte(')')
	default:
		f.b.WriteString(el.Name)
	}
}

// formatExprArmPattern renders a match-EXPRESSION arm's pattern. MatchExprArm
// carries the same pattern fields as MatchArm and differs only in its body
// type, so it borrows the one renderer rather than keeping a second copy —
// the copy it used to keep silently dropped `@` bindings, range high bounds,
// tuple patterns and field renames, rewriting `3..=4 => …` to `3 => …`.
func (f *formatter) formatExprArmPattern(arm *ast.MatchExprArm) {
	f.formatArmPattern(stmtShapedArm(arm))
}

// stmtShapedArm adapts an expression-form arm's PATTERN half to the statement
// shape the one renderer takes. Recursive through Sub, so a payload
// sub-pattern reaches the same renderer the statement form uses.
func stmtShapedArm(arm *ast.MatchExprArm) *ast.MatchArm {
	if arm == nil {
		return nil
	}
	var sub []*ast.MatchArm
	if arm.Sub != nil {
		sub = make([]*ast.MatchArm, len(arm.Sub))
		for i := range arm.Sub {
			sub[i] = stmtShapedArm(arm.Sub[i])
		}
	}
	return &ast.MatchArm{
		P:              arm.P,
		VariantName:    arm.VariantName,
		VariantModule:  arm.VariantModule,
		Bindings:       arm.Bindings,
		NamedFields:    arm.NamedFields,
		FieldNames:     arm.FieldNames,
		IsWildcard:     arm.IsWildcard,
		Literal:        arm.Literal,
		RangeHi:        arm.RangeHi,
		RangeInclusive: arm.RangeInclusive,
		TupleElems:     arm.TupleElems,
		AtBinding:      arm.AtBinding,
		Sub:            sub,
	}
}

// Precedence levels mirror the parser's. Higher value binds
// tighter — formatExpr emits parentheses around an operand only
// when its outer operator binds strictly less tightly than the
// surrounding context (or, for left-associative right-children,
// less-than-or-equal).
const (
	precLowest = 0
	precAssign = 1 // = += -= …
	precPipe   = 2 // |>  (above assignment, below ternary)
	precIfExpr = 3 // if (c) { e } else { e } in expression position
	precOr     = 4 // ||
	precAnd    = 5 // &&
	// Bitwise (|, ^, &) sit BELOW the comparison family in Fern's
	// grammar — the parser hierarchy is parseLogicalAnd →
	// parseBitOr → parseBitXor → parseBitAnd → parseEquality →
	// parseRelational (parser.go:2520-2533), so `==` binds tighter
	// than `&`. The printer order has to match the parser or
	// round-trip drops semantically-load-bearing parens (e.g.
	// `(n & (n - 1)) == 0` formats to `n & n - 1 == 0`, then
	// re-parses as `n & ((n - 1) == 0)` — the "is power of 2"
	// idiom silently turns into ANDing a number with a boolean).
	// Surfaced by the formatter corpus sweep against
	// internal/stdlib/std/i32.fern's is_power_of_2.
	precBitOr   = 6  // |
	precBitXor  = 7  // ^
	precBitAnd  = 8  // &
	precEq      = 9  // == !=
	precCmp     = 10 // < <= > >=
	precShift   = 11 // << >>
	precAdd     = 12 // + -
	precMul     = 13 // * / %
	precCast    = 14 // expr as Type
	precUnary   = 15
	precPrimary = 16
)

func binaryPrec(op string) int {
	switch op {
	case "||":
		return precOr
	case "&&":
		return precAnd
	case "==", "!=":
		return precEq
	case "<", "<=", ">", ">=":
		return precCmp
	case "|":
		return precBitOr
	case "^":
		return precBitXor
	case "&":
		return precBitAnd
	case "<<", ">>", "<<|", "<<?", ">>?":
		return precShift
	case "+", "-", "+|", "-|", "+?", "-?":
		return precAdd
	case "*", "/", "%", "*|", "*?", "/?", "%?":
		return precMul
	}
	return precLowest
}

// formatExpr emits e, wrapping in parens when the outer context
// (parentPrec) binds tighter than e's outermost operator.
func (f *formatter) formatExpr(e ast.Expr, parentPrec int) {
	switch x := e.(type) {
	case *ast.CastExpr:
		needsParens := parentPrec >= precCast
		if needsParens {
			f.b.WriteByte('(')
		}
		f.formatExpr(x.Inner, precCast)
		f.b.WriteString(" as ")
		f.b.WriteString(x.Target.String())
		if needsParens {
			f.b.WriteByte(')')
		}
	case *ast.DowncastExpr:
		needsParens := parentPrec >= precCast
		if needsParens {
			f.b.WriteByte('(')
		}
		f.formatExpr(x.Inner, precCast)
		f.b.WriteString(" as? ")
		f.b.WriteString(x.Target.String())
		if needsParens {
			f.b.WriteByte(')')
		}
	case *ast.NumberLit:
		// A non-zero Width on a NumberLit in formatter input means
		// the parser saw a typed suffix (`42i64`, `7u8`). Preserve
		// it on round-trip — the format pass runs pre-checker, so
		// the only source of Width here is user authorship.
		suffix := ""
		if x.Width != 0 {
			if x.IsUnsigned {
				suffix = fmt.Sprintf("u%d", x.Width)
			} else {
				suffix = fmt.Sprintf("i%d", x.Width)
			}
		}
		if x.Raw != "" {
			// A base the author chose is part of what they wrote: the arm64
			// and x86 encoders spell every literal as the instruction
			// encoding it is, and decimal makes those unreadable.
			f.b.WriteString(x.Raw)
			f.b.WriteString(suffix)
		} else if x.Value < 0 {
			// A negative Value in formatter input can only be an unsigned
			// literal whose magnitude exceeds i64::MAX: the parser stored the
			// bit pattern via `int64(strconv.ParseUint(...))`. (Source
			// negatives like `-5` are `ast.Unary` nodes wrapping a positive
			// NumberLit, handled by the Unary case — they never reach here.)
			// So emit the unsigned decimal. The old `-%d` with `-x.Value`
			// overflowed for math.MinInt64 (== 2^63, e.g.
			// `9223372036854775808 as u64`), leaving it negative and emitting
			// a spurious `--` that grew another `-` on every format pass and
			// broke idempotency.
			fmt.Fprintf(&f.b, "%s%s", strconv.FormatUint(uint64(x.Value), 10), suffix)
		} else {
			fmt.Fprintf(&f.b, "%d%s", x.Value, suffix)
		}
	case *ast.UnitLit:
		f.b.WriteString("()")
	case *ast.BoolLit:
		if x.Value {
			f.b.WriteString("true")
		} else {
			f.b.WriteString("false")
		}
	case *ast.FloatLit:
		v := x.Value
		neg := v < 0
		if neg {
			v = -v
		}
		s := fmt.Sprintf("%g", v)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		// The written spelling wins where the parser recorded one: %g is a
		// re-rendering, not the author's text.
		if x.Raw != "" && !neg {
			s = x.Raw
		}
		// Preserve typed-suffix authorship the parser stamped:
		// non-zero Width on input means the user wrote `1.5f64`
		// (or `42f32` — float-suffixed integer text) and we
		// should round-trip the suffix.
		suffix := ""
		if x.Width != 0 {
			suffix = fmt.Sprintf("f%d", x.Width)
		}
		if neg {
			needsParens := parentPrec >= precUnary
			if needsParens {
				f.b.WriteByte('(')
			}
			f.b.WriteByte('-')
			f.b.WriteString(s)
			f.b.WriteString(suffix)
			if needsParens {
				f.b.WriteByte(')')
			}
		} else {
			f.b.WriteString(s)
			f.b.WriteString(suffix)
		}
	case *ast.CharLit:
		// Raw is the spelling as written: an escape the author chose
		// (`'\u{1F600}'`, `b'\x1B'`) is part of what they wrote, the
		// same contract NumberLit.Raw carries for a hex literal.
		f.b.WriteString(x.Raw)
	case *ast.StringLit:
		f.b.WriteByte('"')
		for i := 0; i < len(x.Value); i++ {
			c := x.Value[i]
			switch c {
			case '"':
				f.b.WriteString(`\"`)
			case '\\':
				f.b.WriteString(`\\`)
			case '\n':
				f.b.WriteString(`\n`)
			case '\t':
				f.b.WriteString(`\t`)
			case '\r':
				f.b.WriteString(`\r`)
			default:
				f.b.WriteByte(c)
			}
		}
		f.b.WriteByte('"')
	case *ast.FString:
		// Reconstruct the f"..." surface syntax. Literal segments
		// re-escape via the same rules as a regular string literal,
		// plus `{` / `}` doubled to `{{` / `}}` so the body
		// re-lexes back into the same parts. Interpolant parts
		// re-emit as `{<expr>}` via formatExpr at the lowest
		// precedence (inside the braces, the interpolant is its
		// own context so no parens needed at the surface).
		f.b.WriteString(`f"`)
		for _, part := range x.Parts {
			if part.Expr != nil {
				f.b.WriteByte('{')
				f.formatExpr(part.Expr, precLowest)
				f.b.WriteByte('}')
				continue
			}
			for i := 0; i < len(part.Lit); i++ {
				c := part.Lit[i]
				switch c {
				case '"':
					f.b.WriteString(`\"`)
				case '\\':
					f.b.WriteString(`\\`)
				case '\n':
					f.b.WriteString(`\n`)
				case '\t':
					f.b.WriteString(`\t`)
				case '\r':
					f.b.WriteString(`\r`)
				case '{':
					f.b.WriteString(`{{`)
				case '}':
					f.b.WriteString(`}}`)
				default:
					f.b.WriteByte(c)
				}
			}
		}
		f.b.WriteByte('"')
	case *ast.Ident:
		f.b.WriteString(x.Name)
	case *ast.Unary:
		needsParens := parentPrec >= precUnary
		if needsParens {
			f.b.WriteByte('(')
		}
		f.b.WriteString(x.Op)
		f.formatExpr(x.Operand, precUnary)
		if needsParens {
			f.b.WriteByte(')')
		}
	case *ast.Binary:
		p := binaryPrec(x.Op)
		needsParens := p < parentPrec
		if needsParens {
			f.b.WriteByte('(')
		}
		f.formatExpr(x.Left, p)
		f.b.WriteByte(' ')
		f.b.WriteString(x.Op)
		f.b.WriteByte(' ')
		f.formatExpr(x.Right, p+1)
		if needsParens {
			f.b.WriteByte(')')
		}
	case *ast.Call:
		// Pipe-synthesised calls re-render as `LHS |> Callee(rest)`.
		// Args[0] is the original LHS; Args[1:] are the original
		// explicit args.
		if x.IsPipe && len(x.Args) >= 1 {
			needsParens := parentPrec > precPipe
			if needsParens {
				f.b.WriteByte('(')
			}
			// PipeHole > 0: the LHS was substituted at the `_`
			// placeholder (1-based index) instead of prepended —
			// re-render it as the LHS and put the `_` back in its
			// slot. Parens are always printed in this form (`x |>
			// f(_)`), since the hole is only expressible inside an
			// arg list.
			if x.PipeHole > 0 && x.PipeHole <= len(x.Args) {
				lhs := x.PipeHole - 1
				f.formatExpr(x.Args[lhs], precPipe)
				f.b.WriteString(" |> ")
				f.formatExpr(x.Callee, precPrimary)
				f.writeCallTypeArgs(x)
				f.b.WriteByte('(')
				for i, a := range x.Args {
					if i > 0 {
						f.b.WriteString(", ")
					}
					if i == lhs {
						f.b.WriteByte('_')
					} else {
						f.formatExpr(a, precLowest)
					}
				}
				f.b.WriteByte(')')
				if needsParens {
					f.b.WriteByte(')')
				}
				return
			}
			f.formatExpr(x.Args[0], precPipe)
			f.b.WriteString(" |> ")
			f.formatExpr(x.Callee, precPrimary)
			f.writeCallTypeArgs(x)
			if len(x.Args) > 1 {
				f.b.WriteByte('(')
				for i, a := range x.Args[1:] {
					if i > 0 {
						f.b.WriteString(", ")
					}
					f.formatExpr(a, precLowest)
				}
				f.b.WriteByte(')')
			}
			if needsParens {
				f.b.WriteByte(')')
			}
			return
		}
		f.formatExpr(x.Callee, precPrimary)
		f.writeCallTypeArgs(x)
		f.b.WriteByte('(')
		for i, a := range x.Args {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.formatExpr(a, precLowest)
		}
		f.b.WriteByte(')')
	case *ast.Index:
		f.formatExpr(x.Array, precPrimary)
		f.b.WriteByte('[')
		f.formatExpr(x.Idx, precLowest)
		f.b.WriteByte(']')
	case *ast.SliceExpr:
		f.formatExpr(x.Source, precPrimary)
		f.b.WriteByte('[')
		if x.Low != nil {
			f.formatExpr(x.Low, precLowest)
		}
		f.b.WriteByte(':')
		if x.High != nil {
			f.formatExpr(x.High, precLowest)
		}
		f.b.WriteByte(']')
	case *ast.ArrayLit:
		f.b.WriteByte('[')
		for i, el := range x.Elems {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.formatExpr(el, precLowest)
		}
		f.b.WriteByte(']')
	case *ast.Assign:
		needsParens := parentPrec > precAssign
		if needsParens {
			f.b.WriteByte('(')
		}
		f.formatExpr(x.Target, precPrimary)
		f.b.WriteString(" = ")
		f.formatExpr(x.Value, precAssign)
		if needsParens {
			f.b.WriteByte(')')
		}
	case *ast.IfExpr:
		needsParens := parentPrec > precIfExpr
		if needsParens {
			f.b.WriteByte('(')
		}
		f.b.WriteString("if (")
		f.formatExpr(x.Cond, 0)
		f.b.WriteString(") ")
		f.formatBranch(x.Then)
		f.b.WriteString(" else ")
		f.formatBranch(x.Else)
		if needsParens {
			f.b.WriteByte(')')
		}
	case *ast.TryOp:
		// Postfix `?` binds tighter than any binary operator —
		// emit it directly without precedence-based parens.
		f.formatExpr(x.Inner, precUnary)
		f.b.WriteByte('?')
	case *ast.MatchExpr:
		// Compact one-line form: arms separated by `,`, each arm
		// body emitted as an inline expression. The statement-form
		// match uses block-bodies on separate lines, but in
		// expression position arms are usually short and the
		// inline shape composes inside larger expressions.
		f.b.WriteString("match (")
		f.formatExpr(x.Tag, precLowest)
		f.b.WriteString(") { ")
		// See the statement form: Sugar is the arms as written.
		exprArms := x.Arms
		if x.Sugar != nil {
			exprArms = x.Sugar
		}
		for i := 0; i < len(exprArms); i++ {
			arm := exprArms[i]
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.formatExprArmPattern(arm)
			// See the statement form: or-pattern alternatives rejoin here.
			for i+1 < len(exprArms) && exprArms[i+1].AltCont {
				i++
				f.b.WriteString(" | ")
				f.formatExprArmPattern(exprArms[i])
			}
			if arm.Guard != nil {
				f.b.WriteString(" when ")
				f.formatExpr(arm.Guard, precLowest)
			}
			f.b.WriteString(" => ")
			f.formatArmBody(arm.Body)
		}
		f.b.WriteString(" }")
	case *ast.StructLit:
		f.b.WriteString(x.TypeName)
		if x.TypeArgsWritten {
			f.writeTypeArgs(x.TypeArgs)
		}
		f.b.WriteString(" { ")
		// Struct-update literal: leading `...base`, then overrides.
		if x.Base != nil {
			f.b.WriteString("...")
			f.formatExpr(x.Base, precLowest)
			if len(x.Fields) > 0 {
				f.b.WriteString(", ")
			}
		}
		for i, fld := range x.Fields {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.b.WriteString(fld.Name)
			f.b.WriteString(": ")
			f.formatExpr(fld.Value, precLowest)
		}
		f.b.WriteString(" }")
	case *ast.MapLit:
		f.b.WriteString("Map {")
		for i, ent := range x.Entries {
			if i > 0 {
				f.b.WriteByte(',')
			}
			f.b.WriteByte(' ')
			f.formatExpr(ent.Key, precLowest)
			f.b.WriteString(": ")
			f.formatExpr(ent.Value, precLowest)
		}
		f.b.WriteString(" }")
	case *ast.TupleLit:
		f.b.WriteByte('(')
		for i, e := range x.Elems {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.formatExpr(e, precLowest)
		}
		f.b.WriteByte(')')
	case *ast.FieldAccess:
		f.formatExpr(x.Target, precPrimary)
		if x.PathSep {
			f.b.WriteString("::")
		} else {
			f.b.WriteByte('.')
		}
		f.b.WriteString(x.Field)
	case *ast.Lambda:
		// Anonymous function expression: `function(p: T): R { ... }`.
		// Mirrors formatFunc minus the name / receiver / pub prefix.
		// Without this case formatExpr fell through to the empty
		// default and silently dropped the lambda — leaving a
		// dangling comma when it was the last call argument
		// (`f(xs, )`, which then fails to re-parse). A single-
		// statement body renders inline so short predicate lambdas
		// stay on one line; anything longer uses the normal
		// multi-line block.
		//
		// An arrow lambda parses to this same node with its expression
		// wrapped in a one-statement `return`, so the arrow form is
		// reconstructed whenever that shape is intact. The `function`
		// rendering has to invent a return type for it, and `void` is a
		// lie for every arrow whose expression has a value.
		if x.Arrow && x.Body != nil && len(x.Body.Stmts) == 1 {
			if ret, ok := x.Body.Stmts[0].(*ast.Return); ok && ret.Value != nil {
				// The body runs as far right as it can, so any context
				// that continues with a tighter operator needs parens.
				// An assignment's RHS is terminal, hence `>` not `>=`.
				needsParens := parentPrec > precAssign
				if needsParens {
					f.b.WriteByte('(')
				}
				f.b.WriteByte('(')
				for i, p := range x.Params {
					if i > 0 {
						f.b.WriteString(", ")
					}
					f.b.WriteString(writtenName(p.Name))
					f.b.WriteString(": ")
					f.b.WriteString(formatType(p.Type))
				}
				f.b.WriteByte(')')
				if !x.ReturnUnannotated && x.ReturnType != nil {
					f.b.WriteString(": ")
					f.b.WriteString(formatType(x.ReturnType))
				}
				f.b.WriteString(" => ")
				f.formatExpr(ret.Value, precLowest)
				if needsParens {
					f.b.WriteByte(')')
				}
				break
			}
		}
		f.b.WriteString("function(")
		for i, p := range x.Params {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.b.WriteString(writtenName(p.Name))
			f.b.WriteString(": ")
			f.b.WriteString(formatType(p.Type))
		}
		f.b.WriteByte(')')
		if x.ReturnType != nil {
			f.b.WriteString(": ")
			f.b.WriteString(formatType(x.ReturnType))
		}
		f.b.WriteByte(' ')
		if x.Body != nil && len(x.Body.Stmts) == 1 && isSingleLineStmt(x.Body.Stmts[0]) {
			f.b.WriteString("{ ")
			f.formatStmt(x.Body.Stmts[0], 0)
			f.b.WriteString(" }")
		} else {
			f.formatBlock(x.Body, 0)
		}
	case *ast.BlockExpr:
		// Reached only if a BlockExpr appears outside an if/match branch
		// (the dedicated formatBranch / formatArmBody paths handle those).
		// Slice 1 doesn't produce that, but render it compactly so the
		// formatter never silently drops the node.
		f.formatBlockExpr(x)
	}
}

// writtenName undoes the parser's `_` renaming, so a discarded binding
// prints as the `_` the author typed rather than the internal
// `__discard_<line>_<col>_<n>` the parser substituted (parser.discardName).
// The renaming is total over `_` and its output is reserved, so this is its
// exact inverse — nothing else can produce a name of that shape.
func writtenName(name string) string {
	if strings.HasPrefix(name, "__discard_") {
		return "_"
	}
	return name
}

// formatDestructurePattern renders a tuple destructure's pattern `(a, (b, c))`.
// A nested position prints as its own pattern rather than as the synthesised
// binder the parser put in Names — that binder is an implementation detail of
// the lowering, and printing it would re-parse to a different program.
func (f *formatter) formatDestructurePattern(x *ast.Destructure) {
	f.b.WriteByte('(')
	for i, n := range x.Names {
		if i > 0 {
			f.b.WriteString(", ")
		}
		if i < len(x.Nested) && x.Nested[i] != nil {
			f.formatDestructurePattern(x.Nested[i])
			continue
		}
		f.b.WriteString(writtenName(n))
	}
	f.b.WriteByte(')')
}

// formatType returns the textual form of t. Unchanged from the
// pre-comment-retention version.
// writeTypeArgs emits a `[T1, T2]` type-argument list. Callers gate on
// the node's TypeArgsWritten: reprinting checker-inferred args would put
// an instantiation into source that never named one, and for a call it
// would also turn every ordinary generic call into an explicit one.
func (f *formatter) writeTypeArgs(args []ast.Type) {
	if len(args) == 0 {
		return
	}
	f.b.WriteByte('[')
	for i, a := range args {
		if i > 0 {
			f.b.WriteString(", ")
		}
		f.b.WriteString(formatType(a))
	}
	f.b.WriteByte(']')
}

func (f *formatter) writeCallTypeArgs(c *ast.Call) {
	if c.TypeArgsWritten {
		f.writeTypeArgs(c.TypeArgs)
	}
}

func formatType(t ast.Type) string {
	switch x := t.(type) {
	case ast.NumberType:
		// Preserve the user's source spelling when the parser
		// captured one (`number` vs `i32`). Falls back to the
		// canonical name for synthesised types (e.g. inferred
		// in the checker).
		if x.Spelling != "" {
			return x.Spelling
		}
		return x.String()
	case ast.BoolType:
		return "boolean"
	case ast.VoidType:
		return "void"
	case ast.StringType:
		return "string"
	case ast.CharType:
		return "char"
	case ast.StrType:
		// The borrowed-string view type (#4813).
		return "str"
	case ast.FloatType:
		if x.Spelling != "" {
			return x.Spelling
		}
		return x.String()
	case ast.StructType:
		return x.Name
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x.Name
		}
		out := x.Name + "["
		for i, a := range x.Args {
			if i > 0 {
				out += ", "
			}
			out += formatType(a)
		}
		return out + "]"
	case ast.ParamType:
		return x.Name
	case ast.ArrayType:
		// A function-typed element needs its parens back: `((string) => string)[]`
		// printed bare reads as one function RETURNING `string[]`.
		if _, isFn := x.Elem.(*ast.FuncType); isFn {
			return "(" + formatType(x.Elem) + ")[]"
		}
		return formatType(x.Elem) + "[]"
	case ast.SliceType:
		return "[" + formatType(x.Elem) + "]"
	case ast.TupleType:
		out := "("
		for i, e := range x.Elems {
			if i > 0 {
				out += ", "
			}
			out += formatType(e)
		}
		return out + ")"
	case *ast.FuncType:
		out := "("
		for i, p := range x.Params {
			if i > 0 {
				out += ", "
			}
			out += formatType(p)
		}
		return out + ") => " + formatType(x.Result)
	case ast.SelfType:
		return "Self"
	case ast.DynTraitType:
		return x.String()
	case ast.HandleType:
		if x.Borrowed {
			return "borrow " + x.Resource
		}
		return "own " + x.Resource
	}
	return ""
}
