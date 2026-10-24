package printer

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func Format(prog *ast.Program) string {
	blanks := make(map[int]bool, len(prog.BlankLines))
	for _, ln := range prog.BlankLines {
		blanks[ln] = true
	}
	f := &formatter{comments: prog.Comments, blanks: blanks}
	implMethods := map[*ast.FuncDecl]bool{}
	for _, id := range prog.Impls {
		for _, m := range id.Methods {
			implMethods[m] = true
		}
	}
	type topDecl struct {
		p        ast.Position
		emit     func()
		isImport bool
	}
	decls := make([]topDecl, 0,
		len(prog.Imports)+len(prog.PubUses)+
			len(prog.Structs)+len(prog.Enums)+len(prog.Unions)+len(prog.Resources)+
			len(prog.Traits)+len(prog.Impls)+len(prog.Consts)+len(prog.Funcs))
	for _, imp := range prog.Imports {
		decls = append(decls, topDecl{p: imp.P, emit: func() { f.formatImport(imp) }, isImport: true})
	}
	for _, pu := range prog.PubUses {
		decls = append(decls, topDecl{p: pu.P, emit: func() { f.formatPubUse(pu) }, isImport: true})
	}
	for _, sd := range prog.Structs {
		decls = append(decls, topDecl{sd.P, func() { f.formatStructDecl(sd) }, false})
	}
	for _, ed := range prog.Enums {
		decls = append(decls, topDecl{ed.P, func() { f.formatEnumDecl(ed) }, false})
	}
	for _, ud := range prog.Unions {
		decls = append(decls, topDecl{ud.P, func() { f.formatUnionDecl(ud) }, false})
	}
	for _, rd := range prog.Resources {
		decls = append(decls, topDecl{rd.P, func() { f.formatResourceDecl(rd) }, false})
	}
	for _, td := range prog.Traits {
		decls = append(decls, topDecl{td.P, func() { f.formatTraitDecl(td) }, false})
	}
	for _, id := range prog.Impls {
		decls = append(decls, topDecl{id.P, func() { f.formatImplDecl(id) }, false})
	}
	for _, cd := range prog.Consts {
		decls = append(decls, topDecl{cd.P, func() { f.formatConstDecl(cd) }, false})
	}
	for _, fn := range prog.Funcs {
		if implMethods[fn] {
			continue
		}
		decls = append(decls, topDecl{fn.P, func() { f.formatFunc(fn, 0) }, false})
	}
	sort.SliceStable(decls, func(i, j int) bool {
		if decls[i].p.Line != decls[j].p.Line {
			return decls[i].p.Line < decls[j].p.Line
		}
		return decls[i].p.Col < decls[j].p.Col
	})
	for i, d := range decls {
		if i > 0 && !(d.isImport && decls[i-1].isImport) {
			f.b.WriteByte('\n')
		}
		f.drainDeclLeading(d.p.Line)
		d.emit()
	}
	f.drainAll(0)
	return f.b.String()
}

const formatIndent = "  "

func (f *formatter) formatImport(imp *ast.Import) {
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

func (f *formatter) formatPubUse(pu *ast.PubUse) {
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

type formatter struct {
	b        strings.Builder
	comments []ast.Comment
	ci       int
	blanks   map[int]bool
	depth    int
}

func (f *formatter) blankBefore(line int) bool {
	top := line
	if f.ci < len(f.comments) && f.comments[f.ci].Pos.Line < line {
		top = f.comments[f.ci].Pos.Line
	}
	return f.blanks[top-1]
}

func (f *formatter) drainLeading(line, depth int) {
	last := 0
	for f.ci < len(f.comments) && f.comments[f.ci].Pos.Line < line {
		if last > 0 && f.blanks[f.comments[f.ci].Pos.Line-1] {
			f.b.WriteByte('\n')
		}
		last = f.comments[f.ci].Pos.Line
		f.indent(depth)
		f.b.WriteString("//")
		f.b.WriteString(f.comments[f.ci].Text)
		f.b.WriteByte('\n')
		f.ci++
	}
}

func (f *formatter) drainDeclLeading(line int) {
	last := 0
	for f.ci < len(f.comments) && f.comments[f.ci].Pos.Line < line {
		if last > 0 && f.blanks[f.comments[f.ci].Pos.Line-1] {
			f.b.WriteByte('\n')
		}
		last = f.comments[f.ci].Pos.Line
		f.b.WriteString("//")
		f.b.WriteString(f.comments[f.ci].Text)
		f.b.WriteByte('\n')
		f.ci++
	}
	if last > 0 && f.blanks[last+1] {
		f.b.WriteByte('\n')
	}
}

func (f *formatter) emitTrailing(line int) {
	if f.ci < len(f.comments) && f.comments[f.ci].Pos.Line == line && f.comments[f.ci].Trailing {
		f.b.WriteString("  //")
		f.b.WriteString(f.comments[f.ci].Text)
		f.ci++
	}
}

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

func (f *formatter) keepListLines(open, last int) bool {
	return open > 0 && last > open || f.innerCommentPending(open, last)
}

func writtenBreak(b *ast.Binary) bool {
	op := b.P.Line
	if op == 0 {
		return false
	}
	left := lastLine(b.Left)
	right := firstLine(b.Right)
	return left > 0 && op > left || right > op
}

func firstLine(e ast.Expr) int {
	switch x := e.(type) {
	case *ast.Binary:
		return firstLine(x.Left)
	case *ast.Call:
		if x.IsPipe && len(x.Args) > 0 {
			return firstLine(x.Args[0])
		}
		return firstLine(x.Callee)
	case *ast.Index:
		return firstLine(x.Array)
	case *ast.SliceExpr:
		return firstLine(x.Source)
	case *ast.FieldAccess:
		return firstLine(x.Target)
	case *ast.CastExpr:
		return firstLine(x.Inner)
	case *ast.TryOp:
		return firstLine(x.Inner)
	}
	return e.Pos().Line
}

func lastLine(e ast.Expr) int {
	ln := e.Pos().Line
	more := func(es ...ast.Expr) {
		for _, x := range es {
			if x != nil {
				ln = max(ln, lastLine(x))
			}
		}
	}
	closer := func(open, lastElem int) {
		if open > 0 && lastElem > open {
			ln++
		}
	}
	switch x := e.(type) {
	case *ast.Binary:
		more(x.Left, x.Right)
	case *ast.Unary:
		more(x.Operand)
	case *ast.Assign:
		more(x.Target, x.Value)
	case *ast.Call:
		more(x.Callee)
		more(x.Args...)
		if n := len(x.Args); n > 0 && !x.IsPipe {
			closer(x.P.Line, firstLine(x.Args[n-1]))
		}
	case *ast.ArrayLit:
		more(x.Elems...)
		if n := len(x.Elems); n > 0 {
			closer(x.P.Line, firstLine(x.Elems[n-1]))
		}
	case *ast.TupleLit:
		more(x.Elems...)
	case *ast.Index:
		more(x.Array, x.Idx)
	case *ast.SliceExpr:
		more(x.Source, x.Low, x.High)
	case *ast.StructLit:
		more(x.Base)
		for _, fld := range x.Fields {
			more(fld.Value)
		}
		if n := len(x.Fields); n > 0 {
			closer(x.P.Line, x.Fields[n-1].NamePos.Line)
		} else if x.Base != nil {
			closer(x.P.Line, firstLine(x.Base))
		}
	case *ast.FieldAccess:
		more(x.Target)
	case *ast.MapLit:
		for _, en := range x.Entries {
			more(en.Key, en.Value)
		}
	case *ast.CastExpr:
		more(x.Inner)
	case *ast.TryOp:
		more(x.Inner)
	}
	return ln
}

func (f *formatter) writeListLines(n int, line func(int) int, write func(int), closer string) {
	f.b.WriteByte('\n')
	f.depth++
	for i := 0; i < n; {
		ln := line(i)
		f.drainLeading(ln, f.depth)
		f.indent(f.depth)
		j := i
		for ; j < n && (j == i || line(j) == ln); j++ {
			if j > i {
				f.b.WriteString(", ")
			}
			write(j)
		}
		f.b.WriteByte(',')
		f.emitTrailing(ln)
		f.b.WriteByte('\n')
		i = j
	}
	f.depth--
	f.indent(f.depth)
	f.b.WriteString(closer)
}

func (f *formatter) drainAll(depth int) {
	last := 0
	for f.ci < len(f.comments) {
		if f.blanks[f.comments[f.ci].Pos.Line-1] && (last > 0 || f.b.Len() > 0) {
			f.b.WriteByte('\n')
		}
		last = f.comments[f.ci].Pos.Line
		f.indent(depth)
		f.b.WriteString("//")
		f.b.WriteString(f.comments[f.ci].Text)
		f.b.WriteByte('\n')
		f.ci++
	}
}

func (f *formatter) indent(n int) {
	for i := 0; i < n; i++ {
		f.b.WriteString(formatIndent)
	}
}

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
	if len(ed.Variants) > 0 && f.keepListLines(ed.P.Line, ed.Variants[len(ed.Variants)-1].P.Line) {
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

func (f *formatter) formatUnionDecl(ud *ast.UnionDecl) {
	if ud.PackageScoped {
		f.b.WriteString("pub(package) ")
	} else if ud.Public {
		f.b.WriteString("pub ")
	}
	f.b.WriteString("type ")
	f.b.WriteString(ud.Name)
	f.writeTypeParams(ud.TypeParams, nil, nil, nil)
	f.b.WriteString(" = ")
	ln := ud.MemberLines
	kept := len(ln) == len(ud.Members) && len(ln) > 0 && ln[0] > 0 && ln[len(ln)-1] > ln[0]
	for i, m := range ud.Members {
		switch {
		case i == 0:
		case kept && ln[i] > ln[i-1]:
			f.b.WriteByte('\n')
			f.indent(1)
			f.b.WriteString("| ")
		default:
			f.b.WriteString(" | ")
		}
		f.b.WriteString(m.Name)
		f.writeTypeArgs(m.Args)
	}
	f.b.WriteString(";\n")
}

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

func (f *formatter) writeMustConsumeAttr(mustConsume bool) {
	if !mustConsume {
		return
	}
	f.b.WriteString("@must_consume\n")
}

func (f *formatter) writeTypeParams(names []string, bounds map[string][]string, boundArgs map[string][][]ast.Type, consts []string) {
	if len(names) == 0 {
		return
	}
	f.b.WriteByte('[')
	for i, name := range names {
		if i > 0 {
			f.b.WriteString(", ")
		}
		if slices.Contains(consts, name) {
			f.b.WriteString("const " + name + ": i32")
			continue
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
	if sd.Opaque {
		f.b.WriteString("opaque ")
	}
	f.b.WriteString("struct ")
	f.b.WriteString(sd.Name)
	f.writeTypeParams(sd.TypeParams, nil, nil, nil)
	if n := len(sd.Fields); n > 0 && sd.Fields[n-1].NamePos.Line > 0 &&
		f.keepListLines(sd.P.Line, sd.Fields[n-1].NamePos.Line) {
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

func (f *formatter) formatTraitDecl(td *ast.TraitDecl) {
	if td.PackageScoped {
		f.b.WriteString("pub(package) ")
	} else if td.Public {
		f.b.WriteString("pub ")
	}
	f.b.WriteString("trait ")
	f.b.WriteString(td.Name)
	f.writeTypeParams(td.TypeParams, nil, nil, nil)
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
	for i, m := range td.Methods {
		if i > 0 && f.blankBefore(m.P.Line) {
			f.b.WriteByte('\n')
		}
		f.drainLeading(m.P.Line, 1)
		f.formatTraitMethod(m)
	}
	f.b.WriteString("}\n")
}

func (f *formatter) formatTraitMethod(m ast.TraitMethod) {
	f.indent(1)
	if m.Const {
		f.b.WriteString("const ")
		f.b.WriteString(m.Name)
		f.b.WriteString(": ")
		f.b.WriteString(formatType(m.Result))
		if v := constBodyValue(m.Body); v != nil {
			f.b.WriteString(" = ")
			f.formatExpr(v, precLowest)
		}
		f.b.WriteByte(';')
		f.emitTrailing(m.P.Line)
		f.b.WriteByte('\n')
		return
	}
	f.b.WriteString("function ")
	f.b.WriteString(m.Name)
	f.writeTypeParams(m.TypeParams, m.Bounds, m.BoundArgs, nil)
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
	f.b.WriteByte(';')
	f.emitTrailing(m.P.Line)
	f.b.WriteByte('\n')
}

func (f *formatter) formatImplDecl(id *ast.ImplDecl) {
	f.b.WriteString("impl")
	f.writeTypeParams(id.TypeParams, id.Bounds, id.BoundArgs, nil)
	f.b.WriteByte(' ')
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
	f.b.WriteString(formatType(id.Type))
	if len(id.AssocTypeBindings) == 0 && len(id.Methods) == 0 {
		f.b.WriteString(" {}\n")
		return
	}
	f.b.WriteString(" {\n")
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
	parametric := len(id.TypeParams) > 0
	for i, m := range id.Methods {
		if i > 0 && f.blankBefore(m.P.Line) {
			f.b.WriteByte('\n')
		}
		f.drainLeading(m.P.Line, 1)
		f.formatImplMethod(m, parametric)
	}
	f.b.WriteString("}\n")
}

func constBodyValue(b *ast.Block) ast.Expr {
	if b == nil || len(b.Stmts) != 1 {
		return nil
	}
	if r, ok := b.Stmts[0].(*ast.Return); ok {
		return r.Value
	}
	return nil
}

func (f *formatter) formatImplMethod(fn *ast.FuncDecl, parametric bool) {
	f.indent(1)
	if fn.AssocConst {
		f.b.WriteString("const ")
		f.b.WriteString(fn.Name)
		f.b.WriteString(": ")
		if fn.ImplWritten != nil {
			f.b.WriteString(formatType(fn.ImplWritten.Result))
		} else {
			f.b.WriteString(formatType(fn.ReturnType))
		}
		if v := constBodyValue(fn.Body); v != nil {
			f.b.WriteString(" = ")
			f.formatExpr(v, precLowest)
		}
		f.b.WriteString(";\n")
		return
	}
	f.b.WriteString("function ")
	f.b.WriteString(fn.Name)
	if !parametric {
		f.writeTypeParams(fn.TypeParams, fn.Bounds, fn.BoundArgs, fn.ConstParams)
	}
	params := fn.Params
	if fn.Receiver != nil {
		params = append([]ast.Param{*fn.Receiver}, params...)
	}
	result := fn.ReturnType
	if fn.ImplWritten != nil {
		params = slices.Clone(params)
		for i := range params {
			params[i].Type = fn.ImplWritten.Params[i]
		}
		result = fn.ImplWritten.Result
	}
	f.b.WriteByte('(')
	for i, p := range params {
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
	if result != nil && !fn.ReturnUnannotated {
		f.b.WriteString(": ")
		f.b.WriteString(formatType(result))
	}
	f.b.WriteByte(' ')
	f.formatBlock(fn.Body, 1)
	f.b.WriteByte('\n')
}

func (f *formatter) formatResourceDecl(rd *ast.ResourceDecl) {
	if rd.ImportIface != "" {
		f.b.WriteString("@import(\"")
		f.b.WriteString(rd.ImportIface)
		f.b.WriteString("\", \"")
		f.b.WriteString(rd.ImportWITName)
		f.b.WriteString("\")\n")
	}
	f.b.WriteString("resource ")
	f.b.WriteString(rd.Name)
	f.b.WriteString(";\n")
}

func (f *formatter) formatFunc(fn *ast.FuncDecl, depth int) {
	if fn.ImportIface != "" {
		f.indent(depth)
		f.b.WriteString("@import(\"")
		f.b.WriteString(fn.ImportIface)
		f.b.WriteString("\", \"")
		f.b.WriteString(fn.ImportWITName)
		f.b.WriteString("\")\n")
	}
	if fn.ExportIface != "" {
		f.indent(depth)
		f.b.WriteString("@export(\"")
		f.b.WriteString(fn.ExportIface)
		f.b.WriteString("\", \"")
		f.b.WriteString(fn.ExportWITName)
		f.b.WriteString("\")\n")
	}
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
	f.writeTypeParams(fn.TypeParams, fn.Bounds, fn.BoundArgs, fn.ConstParams)
	f.b.WriteByte('(')
	for i, p := range fn.Params {
		if i > 0 {
			f.b.WriteString(", ")
		}
		if p.Own {
			f.b.WriteString("own ")
		}
		if p.Pattern != nil {
			f.formatParamPattern(p)
		} else {
			f.b.WriteString(writtenName(p.Name))
		}
		f.b.WriteString(": ")
		f.b.WriteString(formatType(p.Type))
		if p.Default != nil {
			f.b.WriteString(" = ")
			f.formatExpr(p.Default, precLowest)
		}
	}
	f.b.WriteByte(')')
	if fn.ReturnType != nil && !fn.ReturnUnannotated {
		f.b.WriteString(": ")
		f.b.WriteString(formatType(fn.ReturnType))
	}
	if fn.ImportIface != "" {
		f.b.WriteString(";\n")
		return
	}
	f.b.WriteByte(' ')
	if n, ok := desugarPreludeLen(fn.Params, fn.Body); ok && n > 0 {
		f.formatBlockFrom(fn.Body, depth, n)
	} else {
		f.formatBlock(fn.Body, depth)
	}
	f.b.WriteByte('\n')
}

func (f *formatter) formatBlockFrom(blk *ast.Block, depth, skip int) {
	if blk == nil || skip >= len(blk.Stmts) {
		f.b.WriteString("{}")
		return
	}
	rest := &ast.Block{Stmts: blk.Stmts[skip:]}
	f.formatBlock(rest, depth)
}

func (f *formatter) formatBlock(blk *ast.Block, depth int) {
	if blk == nil || len(blk.Stmts) == 0 {
		f.b.WriteString("{}")
		return
	}
	f.b.WriteByte('{')
	if blk.End.Line > 0 {
		f.emitTrailing(blk.P.Line)
	}
	f.b.WriteByte('\n')
	f.formatStmtLines(blk.Stmts, depth+1, false)
	f.b.WriteByte('\n')
	if blk.End.Line > 0 {
		f.drainLeading(blk.End.Line, depth+1)
	}
	f.indent(depth)
	f.b.WriteByte('}')
}

func (f *formatter) formatStmtLines(stmts []ast.Stmt, depth int, blankFirst bool) {
	for i := 0; i < len(stmts); i++ {
		s := stmts[i]
		if i > 0 {
			f.b.WriteByte('\n')
		}
		if (i > 0 || blankFirst) && f.blankBefore(s.Pos().Line) {
			f.b.WriteByte('\n')
		}
		if fd, ok := s.(*ast.FuncDecl); ok && fd.UseSource != nil && i+1 < len(stmts) && returnsCall(stmts[i+1], fd.UseSource) {
			f.formatUse(fd, depth)
			i++
			continue
		}
		f.formatStmtLine(s, depth)
	}
}

func returnsCall(s ast.Stmt, call *ast.Call) bool {
	r, ok := s.(*ast.Return)
	return ok && r.Value == ast.Expr(call)
}

func (f *formatter) formatUse(fd *ast.FuncDecl, depth int) {
	f.drainLeading(fd.P.Line, depth)
	f.indent(depth)
	f.b.WriteString("use ")
	f.b.WriteString(writtenName(fd.Params[0].Name))
	if t := fd.Params[0].Type; t != nil {
		f.b.WriteString(": ")
		f.b.WriteString(formatType(t))
	}
	f.b.WriteString(" <- ")
	call := *fd.UseSource
	call.Args = call.Args[:len(call.Args)-1]
	f.formatExpr(&call, precLowest)
	f.b.WriteByte(';')
	f.emitTrailing(fd.P.Line)
	if fd.Body != nil && len(fd.Body.Stmts) > 0 {
		f.b.WriteByte('\n')
		f.formatStmtLines(fd.Body.Stmts, depth, true)
	}
}

func (f *formatter) formatStmtLine(s ast.Stmt, depth int) {
	f.drainLeading(s.Pos().Line, depth)
	f.indent(depth)
	f.formatStmt(s, depth)
	if isSingleLineStmt(s) {
		f.emitTrailing(s.Pos().Line)
		f.emitTrailing(stmtLastLine(s))
	}
}

func stmtLastLine(s ast.Stmt) int {
	switch x := s.(type) {
	case *ast.Return:
		if x.Value != nil {
			return lastLine(x.Value)
		}
	case *ast.Var:
		if x.Init != nil {
			return lastLine(x.Init)
		}
	case *ast.Destructure:
		if x.Init != nil {
			return lastLine(x.Init)
		}
	case *ast.ExprStmt:
		return lastLine(x.Expr)
	}
	return s.Pos().Line
}

func isSingleLineStmt(s ast.Stmt) bool {
	switch s.(type) {
	case *ast.Return, *ast.Var, *ast.Destructure, *ast.ExprStmt, *ast.Break, *ast.Continue, *ast.Defer:
		return true
	}
	return false
}

func (f *formatter) formatBranch(e ast.Expr) {
	if be, ok := e.(*ast.BlockExpr); ok {
		f.formatBlockExpr(be)
		return
	}
	f.b.WriteString("{ ")
	f.formatExpr(e, 0)
	f.b.WriteString(" }")
}

func (f *formatter) formatArmBody(e ast.Expr) {
	if be, ok := e.(*ast.BlockExpr); ok {
		f.formatBlockExpr(be)
		return
	}
	f.formatExpr(e, precLowest)
}

func (f *formatter) formatBlockExpr(be *ast.BlockExpr) {
	if f.spansLines(be.Stmts) {
		f.formatBlockExprLines(be)
		return
	}
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

func (f *formatter) formatBlockExprLines(be *ast.BlockExpr) {
	depth := f.depth
	f.b.WriteString("{\n")
	for _, s := range be.Stmts {
		f.indent(depth + 1)
		f.formatStmt(s, depth+1)
		f.b.WriteByte('\n')
	}
	if be.Tail != nil {
		f.indent(depth + 1)
		f.depth = depth + 1
		f.formatExpr(be.Tail, 0)
		f.depth = depth
		f.b.WriteByte('\n')
	}
	f.indent(depth)
	f.b.WriteByte('}')
}

func (f *formatter) spansLines(stmts []ast.Stmt) bool {
	saved, ci := f.b, f.ci
	defer func() { f.b, f.ci = saved, ci }()
	for _, s := range stmts {
		f.b = strings.Builder{}
		f.formatStmt(s, 0)
		if strings.Contains(f.b.String(), "\n") {
			return true
		}
	}
	return false
}

func (f *formatter) formatLoopLabel(label string) {
	if label == "" {
		return
	}
	f.b.WriteString(label)
	f.b.WriteString(": ")
}

func (f *formatter) formatJumpLabel(label string) {
	if label != "" {
		f.b.WriteByte(' ')
		f.b.WriteString(label)
	}
	f.b.WriteByte(';')
}

func (f *formatter) formatAssert(x *ast.If) {
	f.b.WriteString("assert(")
	f.formatExpr(x.Cond.(*ast.Unary).Operand, precLowest)
	eprint := x.Then.(*ast.Block).Stmts[0].(*ast.ExprStmt).Expr.(*ast.Call)
	if text, ok := eprint.Args[0].(*ast.Binary); ok {
		f.b.WriteString(", ")
		f.formatExpr(text.Right, precLowest)
	}
	f.b.WriteString(");")
}

func (f *formatter) formatForEach(fe *ast.ForEach, depth int) {
	f.formatLoopLabel(fe.Label)
	f.b.WriteString("for ")
	if fe.Pattern != nil {
		at := ""
		if fe.Var != ast.ForEachElemName(fe.ID) {
			at = fe.Var
		}
		f.formatDestructurePattern(fe.Pattern, at)
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
	prev := f.depth
	f.depth = depth
	defer func() { f.depth = prev }()
	switch x := s.(type) {
	case *ast.Block:
		if x.Sugar != nil {
			f.formatForEach(x.Sugar, depth)
			return
		}
		f.formatBlock(x, depth)
	case *ast.ForEach:
		f.formatForEach(x, depth)
	case *ast.If:
		if x.IsAssert {
			f.formatAssert(x)
			return
		}
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
		f.b.WriteString("let ")
		f.b.WriteString(writtenName(x.Name))
		if x.Type != nil {
			f.b.WriteString(": ")
			f.b.WriteString(formatType(x.Type))
		}
		f.b.WriteString(" = ")
		f.formatExpr(x.Init, precLowest)
		f.b.WriteByte(';')
	case *ast.Destructure:
		f.b.WriteString("let ")
		f.formatDestructurePattern(x, "")
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
		if be, ok := x.Expr.(*ast.BlockExpr); ok {
			f.formatBlockExpr(be)
		} else {
			f.formatExpr(x.Expr, precLowest)
			f.b.WriteByte(';')
		}
	case *ast.Match:
		arms := x.Arms
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
				if len(success.Stmts) > 0 {
					f.b.WriteByte('\n')
					f.formatStmtLines(success.Stmts, depth, true)
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
			if p.Own {
				f.b.WriteString("own ")
			}
			f.b.WriteString(writtenName(p.Name))
			f.b.WriteString(": ")
			f.b.WriteString(formatType(p.Type))
			if p.Default != nil {
				f.b.WriteString(" = ")
				f.formatExpr(p.Default, precLowest)
			}
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
				if i < len(arm.Payloads) && arm.Payloads[i] != nil {
					if i < len(arm.FieldNames) {
						f.b.WriteString(arm.FieldNames[i])
						f.b.WriteString(": ")
					}
					f.formatTuplePatElem(*arm.Payloads[i])
					continue
				}
				if i < len(arm.FieldNames) && arm.FieldNames[i] != "" && arm.FieldNames[i] != b {
					f.b.WriteString(arm.FieldNames[i])
					f.b.WriteString(": ")
				}
				f.b.WriteString(b)
			}
			if arm.RestWritten {
				if len(arm.Bindings) > 0 {
					f.b.WriteString(", ")
				}
				f.b.WriteString("..")
			}
			f.b.WriteString(" }")
		} else if len(arm.Bindings) > 0 {
			f.b.WriteByte('(')
			for i, b := range arm.Bindings {
				if i > 0 {
					f.b.WriteString(", ")
				}
				if i < len(arm.Payloads) && arm.Payloads[i] != nil {
					f.formatTuplePatElem(*arm.Payloads[i])
					continue
				}
				f.b.WriteString(b)
			}
			f.b.WriteByte(')')
		}
	}
}

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

func (f *formatter) formatTuplePatElem(el ast.TuplePatElem) {
	if el.AtBinding != "" {
		f.b.WriteString(el.AtBinding)
		f.b.WriteString(" @ ")
	}
	switch {
	case el.Nested != nil:
		f.formatTuplePatElems(el.Nested)
	case el.IsWildcard:
		f.b.WriteByte('_')
	case el.Literal != nil:
		f.formatExpr(el.Literal, precLowest)
		if el.RangeHi != nil {
			f.b.WriteString("..")
			if el.RangeInclusive {
				f.b.WriteByte('=')
			}
			f.formatExpr(el.RangeHi, precLowest)
		}
	case el.VariantName != "":
		if el.VariantModule != "" {
			f.b.WriteString(el.VariantModule)
			f.b.WriteByte('.')
		}
		f.b.WriteString(el.VariantName)
		open, close := "(", ")"
		if el.VariantFieldNames != nil || el.RestWritten {
			open, close = " { ", " }"
		}
		f.b.WriteString(open)
		for j, vb := range el.VariantBindings {
			if j > 0 {
				f.b.WriteString(", ")
			}
			hasSub := j < len(el.VariantPayloads) && el.VariantPayloads[j] != nil
			if j < len(el.VariantFieldNames) {
				f.b.WriteString(el.VariantFieldNames[j])
				if hasSub {
					f.b.WriteString(": ")
					f.formatTuplePatElem(*el.VariantPayloads[j])
				} else if el.VariantFieldNames[j] != vb {
					f.b.WriteString(": ")
					f.b.WriteString(vb)
				}
				continue
			}
			if hasSub {
				f.formatTuplePatElem(*el.VariantPayloads[j])
				continue
			}
			f.b.WriteString(vb)
		}
		if el.RestWritten {
			if len(el.VariantBindings) > 0 {
				f.b.WriteString(", ")
			}
			f.b.WriteString("..")
		}
		f.b.WriteString(close)
	default:
		f.b.WriteString(el.Name)
	}
}

func (f *formatter) formatExprArmPattern(arm *ast.MatchExprArm) {
	f.formatArmPattern(stmtShapedArm(arm))
}

func stmtShapedArm(arm *ast.MatchExprArm) *ast.MatchArm {
	if arm == nil {
		return nil
	}
	return &ast.MatchArm{
		P:              arm.P,
		VariantName:    arm.VariantName,
		VariantModule:  arm.VariantModule,
		Bindings:       arm.Bindings,
		NamedFields:    arm.NamedFields,
		FieldNames:     arm.FieldNames,
		RestWritten:    arm.RestWritten,
		IsWildcard:     arm.IsWildcard,
		Literal:        arm.Literal,
		RangeHi:        arm.RangeHi,
		RangeInclusive: arm.RangeInclusive,
		TupleElems:     arm.TupleElems,
		AtBinding:      arm.AtBinding,
		Payloads:       arm.Payloads,
	}
}

const (
	precLowest  = 0
	precAssign  = 1
	precPipe    = 2
	precIfExpr  = 3
	precOr      = 4
	precAnd     = 5
	precBitOr   = 6
	precBitXor  = 7
	precBitAnd  = 8
	precEq      = 9
	precCmp     = 10
	precShift   = 11
	precAdd     = 12
	precMul     = 13
	precCast    = 14
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

func (f *formatter) writeStringBody(lit string, fstring bool) {
	for i := 0; i < len(lit); i++ {
		c := lit[i]
		switch {
		case c == '"':
			f.b.WriteString(`\"`)
		case c == '\\':
			f.b.WriteString(`\\`)
		case c == '\n':
			f.b.WriteString(`\n`)
		case c == '\t':
			f.b.WriteString(`\t`)
		case c == '\r':
			f.b.WriteString(`\r`)
		case fstring && c == '{':
			f.b.WriteString(`{{`)
		case fstring && c == '}':
			f.b.WriteString(`}}`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&f.b, `\x%02x`, c)
		default:
			f.b.WriteByte(c)
		}
	}
}

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
		suffix := ""
		if x.Width == ast.WidthPtr {
			suffix = "usize"
		} else if x.Width != 0 {
			if x.IsUnsigned {
				suffix = fmt.Sprintf("u%d", x.Width)
			} else {
				suffix = fmt.Sprintf("i%d", x.Width)
			}
		}
		if x.Raw != "" {
			f.b.WriteString(x.Raw)
			f.b.WriteString(suffix)
		} else if x.Value < 0 {
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
		if x.Raw != "" && !neg {
			s = x.Raw
		}
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
		f.b.WriteString(x.Raw)
	case *ast.StringLit:
		f.b.WriteByte('"')
		f.writeStringBody(x.Value, false)
		f.b.WriteByte('"')
	case *ast.FString:
		f.b.WriteString(`f"`)
		for _, part := range x.Parts {
			if part.Expr != nil {
				f.b.WriteByte('{')
				f.formatExpr(part.Expr, precLowest)
				f.b.WriteByte('}')
				continue
			}
			f.writeStringBody(part.Lit, true)
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
		if writtenBreak(x) {
			f.emitTrailing(lastLine(x.Left))
			f.b.WriteByte('\n')
			if f.ci < len(f.comments) && f.comments[f.ci].Pos.Line > firstLine(x.Left) {
				f.drainLeading(x.P.Line, f.depth+1)
			}
			f.indent(f.depth + 1)
		} else {
			f.b.WriteByte(' ')
		}
		f.b.WriteString(x.Op)
		f.b.WriteByte(' ')
		f.formatExpr(x.Right, p+1)
		if needsParens {
			f.b.WriteByte(')')
		}
	case *ast.Call:
		if x.IsPipe && len(x.Args) >= 1 {
			needsParens := parentPrec > precPipe
			if needsParens {
				f.b.WriteByte('(')
			}
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
						if i < len(x.ArgNames) && x.ArgNames[i] != "" {
							f.b.WriteString(x.ArgNames[i])
							f.b.WriteString(" = ")
						}
						f.b.WriteByte('_')
					} else {
						f.writeCallArg(x, i, a)
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
					f.writeCallArg(x, i+1, a)
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
		if n := len(x.Args); n > 0 && f.keepListLines(x.P.Line, firstLine(x.Args[n-1])) {
			f.writeListLines(n, func(i int) int { return firstLine(x.Args[i]) },
				func(i int) { f.writeCallArg(x, i, x.Args[i]) }, ")")
			break
		}
		for i, a := range x.Args {
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.writeCallArg(x, i, a)
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
		if n := len(x.Elems); n > 0 && f.keepListLines(x.P.Line, firstLine(x.Elems[n-1])) {
			f.writeListLines(n, func(i int) int { return firstLine(x.Elems[i]) },
				func(i int) { f.formatExpr(x.Elems[i], precLowest) }, "]")
			break
		}
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
		f.formatExpr(x.Inner, precUnary)
		f.b.WriteByte('?')
	case *ast.MatchExpr:
		f.b.WriteString("match (")
		f.formatExpr(x.Tag, precLowest)
		f.b.WriteString(") { ")
		exprArms := x.Arms
		for i := 0; i < len(exprArms); i++ {
			arm := exprArms[i]
			if i > 0 {
				f.b.WriteString(", ")
			}
			f.formatExprArmPattern(arm)
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
		nb := 0
		if x.Base != nil {
			nb = 1
		}
		elemLine := func(i int) int {
			if i < nb {
				return firstLine(x.Base)
			}
			return x.Fields[i-nb].NamePos.Line
		}
		if n := nb + len(x.Fields); n > 0 && f.keepListLines(x.P.Line, elemLine(n-1)) {
			f.b.WriteString(" {")
			f.writeListLines(n, elemLine, func(i int) {
				if i < nb {
					f.b.WriteString("...")
					f.formatExpr(x.Base, precLowest)
					return
				}
				fld := x.Fields[i-nb]
				f.b.WriteString(fld.Name)
				f.b.WriteString(": ")
				f.formatExpr(fld.Value, precLowest)
			}, "}")
			break
		}
		f.b.WriteString(" { ")
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
		f.b.WriteByte('.')
		f.b.WriteString(x.Field)
	case *ast.Lambda:
		needsParens := parentPrec > precAssign
		if needsParens {
			f.b.WriteByte('(')
		}
		f.b.WriteByte('(')
		for i, p := range x.Params {
			if i > 0 {
				f.b.WriteString(", ")
			}
			if p.Own {
				f.b.WriteString("own ")
			}
			if p.Pattern != nil {
				f.formatParamPattern(p)
			} else {
				f.b.WriteString(writtenName(p.Name))
			}
			f.b.WriteString(": ")
			f.b.WriteString(formatType(p.Type))
		}
		f.b.WriteByte(')')
		if !x.ReturnUnannotated && x.ReturnType != nil {
			f.b.WriteString(": ")
			f.b.WriteString(arrowReturnType(x.ReturnType))
		}
		f.b.WriteString(" => ")
		if ret, _, ok := arrowReturn(x); ok {
			f.formatExpr(ret.Value, precLowest)
		} else {
			f.formatArrowBody(x)
		}
		if needsParens {
			f.b.WriteByte(')')
		}
	case *ast.BlockExpr:
		f.formatBlockExpr(x)
	}
}

func writtenName(name string) string {
	if strings.HasPrefix(name, "__discard_") {
		return "_"
	}
	return name
}

func desugarPreludeLen(params []ast.Param, body *ast.Block) (n int, ok bool) {
	holders := map[string]bool{}
	for _, p := range params {
		if p.Pattern != nil {
			holders[p.Name] = true
		}
	}
	if len(holders) == 0 || body == nil {
		return 0, len(holders) == 0
	}
	for n < len(body.Stmts) && len(holders) > 0 {
		d, isD := body.Stmts[n].(*ast.Destructure)
		if !isD {
			break
		}
		id, isID := d.Init.(*ast.Ident)
		if !isID || !holders[id.Name] {
			break
		}
		delete(holders, id.Name)
		n++
	}
	return n, len(holders) == 0
}

func arrowReturn(x *ast.Lambda) (ret *ast.Return, prelude int, ok bool) {
	if x.Body == nil {
		return nil, 0, false
	}
	n, okPrelude := desugarPreludeLen(x.Params, x.Body)
	if !okPrelude || n != len(x.Body.Stmts)-1 {
		return nil, 0, false
	}
	r, isR := x.Body.Stmts[n].(*ast.Return)
	if !isR || r.Value == nil {
		return nil, 0, false
	}
	return r, n, true
}

func (f *formatter) formatArrowBody(x *ast.Lambda) {
	stmts := x.Body
	if n, ok := desugarPreludeLen(x.Params, x.Body); ok && n > 0 && x.Body != nil {
		stmts = &ast.Block{Stmts: x.Body.Stmts[n:]}
	}
	if stmts != nil && len(stmts.Stmts) == 1 && isSingleLineStmt(stmts.Stmts[0]) {
		f.b.WriteString("{ ")
		f.formatStmt(stmts.Stmts[0], f.depth)
		f.b.WriteString(" }")
		return
	}
	f.formatBlock(stmts, f.depth)
}

func (f *formatter) formatParamPattern(p ast.Param) {
	at := ""
	if !strings.HasPrefix(p.Name, "__ptuple_") {
		at = p.Name
	}
	f.formatDestructurePattern(p.Pattern, at)
}

func (f *formatter) formatDestructurePattern(x *ast.Destructure, at string) {
	if at == "" {
		at = x.AtName
	}
	if at != "" {
		f.b.WriteString(writtenName(at))
		f.b.WriteString(" @ ")
	}
	if x.Fields != nil {
		f.b.WriteString(writtenName(x.StructName))
		f.b.WriteString(" { ")
		for i, n := range x.Names {
			if i > 0 {
				f.b.WriteString(", ")
			}
			field := n
			if i < len(x.Fields) {
				field = x.Fields[i]
			}
			if field == n {
				f.b.WriteString(writtenName(n))
				continue
			}
			f.b.WriteString(writtenName(field))
			f.b.WriteString(": ")
			f.b.WriteString(writtenName(n))
		}
		if x.RestWritten {
			if len(x.Names) > 0 {
				f.b.WriteString(", ")
			}
			f.b.WriteString("..")
		}
		f.b.WriteString(" }")
		return
	}
	f.b.WriteByte('(')
	for i, n := range x.Names {
		if i > 0 {
			f.b.WriteString(", ")
		}
		if i < len(x.Nested) && x.Nested[i] != nil {
			f.formatDestructurePattern(x.Nested[i], "")
			continue
		}
		f.b.WriteString(writtenName(n))
	}
	f.b.WriteByte(')')
}

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

func arrowReturnType(t ast.Type) string {
	if _, isFn := t.(*ast.FuncType); isFn {
		return "(" + formatType(t) + ")"
	}
	return formatType(t)
}

func formatType(t ast.Type) string {
	switch x := t.(type) {
	case ast.NumberType:
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
		return "str"
	case ast.FloatType:
		if x.Spelling != "" {
			return x.Spelling
		}
		return x.String()
	case ast.LanesType:
		return x.String()
	case ast.ConstArg:
		if x.Raw != "" {
			return x.Raw
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
		if _, isFn := x.Elem.(*ast.FuncType); isFn {
			return "(" + formatType(x.Elem) + ")[]"
		}
		return formatType(x.Elem) + "[]"
	case ast.SliceType:
		return "[" + formatType(x.Elem) + "]"
	case ast.StreamType:
		return "Stream[" + formatType(x.Elem) + "]"
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
			if x.OwnAt(i) {
				out += "own "
			}
			out += formatType(p)
		}
		return out + ") => " + formatType(x.Result)
	case ast.SelfType:
		return "Self"
	case ast.ProjType:
		return formatType(x.Base) + "::" + x.Name
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

func (f *formatter) writeCallArg(x *ast.Call, i int, a ast.Expr) {
	if i < len(x.ArgNames) && x.ArgNames[i] != "" {
		f.b.WriteString(x.ArgNames[i])
		f.b.WriteString(" = ")
	}
	f.formatExpr(a, precLowest)
}
