// cmd/ferndoc parses every internal/stdlib/std/*.fern file and
// emits one Starlight-flavoured Markdown page per module under the
// configured output directory. Designed to run as a pre-step of the
// docs site's `npm run build`; the output is gitignored so we don't
// review machine-generated diffs.
//
// Usage:
//
//	go run ./cmd/ferndoc -out site/src/content/docs/stdlib/
//
// Each generated page lists public functions, structs, enums and
// constants in source order, with their signatures + any line
// comment immediately above the declaration treated as the doc
// string. Private (non-`pub`) declarations are skipped.
//
// Comment-to-decl association: walks the file's Comments slice
// alongside the decl list, attaching contiguous comment lines that
// land directly above a decl (one-or-more comments whose Pos.Line
// runs unbroken into decl.Pos().Line - 1).
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/stdlib"
)

func main() {
	out := flag.String("out", "", "output directory for generated Markdown pages")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: langdoc -out DIR")
		os.Exit(2)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		die(err)
	}

	modules, err := collectModules()
	if err != nil {
		die(err)
	}
	for _, m := range modules {
		page, err := renderModule(m)
		if err != nil {
			die(fmt.Errorf("render %s: %w", m.name, err))
		}
		// Skip empty modules (everything filtered out) — they'd
		// produce an "(no public exports)" page that clutters the
		// sidebar.
		if page == "" {
			continue
		}
		path := filepath.Join(*out, m.fileName+".md")
		if err := os.WriteFile(path, []byte(page), 0o644); err != nil {
			die(err)
		}
	}
}

type module struct {
	name     string // bare module name, e.g. "string"
	fileName string // file basename without extension, used in the sidebar
	prog     *ast.Program
	src      string
}

// collectModules iterates the embedded stdlib filesystem for
// std/*.fern files, parses each, and returns the loaded list. We
// skip `_test_*` files (per-test stubs) and files whose parser
// rejects their content — those would surface as bigger errors in
// the regular build.
func collectModules() ([]module, error) {
	var out []module
	err := fs.WalkDir(stdlib.FS(), "std", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".fern") {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, "_test_") {
			return nil
		}
		b, err := fs.ReadFile(stdlib.FS(), path)
		if err != nil {
			return err
		}
		src := string(b)
		prog, perr := parser.Parse(src)
		if perr != nil {
			// Parser errors at langdoc time are non-fatal; skip
			// the offending file so the rest of the suite still
			// emits. The CI test pass would catch the real error.
			fmt.Fprintf(os.Stderr, "langdoc: skipping %s (parse: %v)\n", path, perr)
			return nil
		}
		modName := strings.TrimSuffix(base, ".fern")
		out = append(out, module{
			name:     modName,
			fileName: modName,
			prog:     prog,
			src:      src,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func renderModule(m module) (string, error) {
	var b strings.Builder
	frontMatter(&b, m.name, "Standard library reference for std/"+m.name+".")
	b.WriteString(fmt.Sprintf("# `std/%s`\n\n", m.name))
	// Top-of-file doc: any comment(s) at line 1 that don't
	// immediately precede a decl. Treat as the module's overview.
	if intro := moduleIntro(m); intro != "" {
		b.WriteString(intro)
		b.WriteString("\n\n")
	}

	cw := newCommentWalker(m.prog.Comments)

	// Collect every public decl in source order. lang doesn't
	// have a unified Decl interface, so we union the four lists
	// and re-sort by position.
	type decl struct {
		pos    ast.Position
		render func(*strings.Builder)
	}
	var decls []decl
	for _, fd := range m.prog.Funcs {
		if fd == nil || !fd.Public || strings.HasPrefix(fd.Name, "__") {
			continue
		}
		fd := fd
		decls = append(decls, decl{
			pos: fd.P,
			render: func(b *strings.Builder) {
				renderFunc(b, fd, cw)
			},
		})
	}
	for _, sd := range m.prog.Structs {
		if sd == nil || !sd.Public {
			continue
		}
		sd := sd
		decls = append(decls, decl{
			pos: sd.P,
			render: func(b *strings.Builder) {
				renderStruct(b, sd, cw)
			},
		})
	}
	for _, ed := range m.prog.Enums {
		if ed == nil || !ed.Public {
			continue
		}
		ed := ed
		decls = append(decls, decl{
			pos: ed.P,
			render: func(b *strings.Builder) {
				renderEnum(b, ed, cw)
			},
		})
	}
	for _, cd := range m.prog.Consts {
		if cd == nil || !cd.Public {
			continue
		}
		cd := cd
		decls = append(decls, decl{
			pos: cd.P,
			render: func(b *strings.Builder) {
				renderConst(b, cd, cw)
			},
		})
	}
	sort.Slice(decls, func(i, j int) bool {
		if decls[i].pos.Line != decls[j].pos.Line {
			return decls[i].pos.Line < decls[j].pos.Line
		}
		return decls[i].pos.Col < decls[j].pos.Col
	})

	if len(decls) == 0 {
		return "", nil // skip empty modules
	}
	for _, d := range decls {
		d.render(&b)
	}
	return b.String(), nil
}

func frontMatter(b *strings.Builder, name, description string) {
	b.WriteString("---\n")
	b.WriteString("title: std/" + name + "\n")
	b.WriteString("description: " + description + "\n")
	b.WriteString("---\n\n")
}

// moduleIntro returns the leading-comment block on line 1 if any —
// that's idiomatic for module-level documentation in the stdlib.
// Returns empty when the first decl starts on line 1 (no header
// space) or when the first comment is associated with a decl
// directly below it.
func moduleIntro(m module) string {
	if len(m.prog.Comments) == 0 {
		return ""
	}
	first := m.prog.Comments[0]
	if first.Pos.Line != 1 {
		return ""
	}
	// Find the run of consecutive comments starting at line 1.
	end := 0
	for i, c := range m.prog.Comments {
		if c.Pos.Line != i+1 {
			break
		}
		end = i + 1
	}
	if end == 0 {
		return ""
	}
	// If the comment run runs directly into the first decl
	// (no blank line between), it's a decl doc — not a module
	// intro. Skip in that case.
	firstDeclLine := firstDeclLineNum(m.prog)
	if firstDeclLine > 0 && firstDeclLine == end+1 {
		return ""
	}
	var lines []string
	for i := 0; i < end; i++ {
		lines = append(lines, strings.TrimSpace(m.prog.Comments[i].Text))
	}
	return strings.Join(lines, " ")
}

func firstDeclLineNum(prog *ast.Program) int {
	min := 0
	consider := func(line int) {
		if line == 0 {
			return
		}
		if min == 0 || line < min {
			min = line
		}
	}
	for _, fd := range prog.Funcs {
		if fd != nil {
			consider(fd.P.Line)
		}
	}
	for _, sd := range prog.Structs {
		if sd != nil {
			consider(sd.P.Line)
		}
	}
	for _, ed := range prog.Enums {
		if ed != nil {
			consider(ed.P.Line)
		}
	}
	for _, cd := range prog.Consts {
		if cd != nil {
			consider(cd.P.Line)
		}
	}
	return min
}

// commentWalker hands out contiguous comment runs that land
// directly above each decl. Iterates the global comment list in
// source order; each call pulls every comment whose Pos.Line falls
// within (lastSeenLine, declLine) AND ends on declLine-1 (the
// "immediately above" filter).
type commentWalker struct {
	comments []ast.Comment
	cursor   int
}

func newCommentWalker(cs []ast.Comment) *commentWalker {
	return &commentWalker{comments: cs}
}

func (cw *commentWalker) take(declLine int) string {
	// Skip past any comments that ended before declLine-1.
	for cw.cursor < len(cw.comments) && cw.comments[cw.cursor].Pos.Line < declLine-1 {
		// Only skip when there's a gap — a continuous run
		// reaching declLine-1 is what we want. Find the end of
		// the current run starting at cursor.
		start := cw.cursor
		end := start
		for end+1 < len(cw.comments) &&
			cw.comments[end+1].Pos.Line == cw.comments[end].Pos.Line+1 {
			end++
		}
		if cw.comments[end].Pos.Line == declLine-1 {
			break
		}
		cw.cursor = end + 1
	}
	if cw.cursor >= len(cw.comments) {
		return ""
	}
	// Now cw.cursor points at the first comment of a run that
	// ends at declLine-1 (or doesn't reach declLine at all). Walk
	// forward collecting the run that ends at declLine-1.
	start := cw.cursor
	end := start
	for end+1 < len(cw.comments) &&
		cw.comments[end+1].Pos.Line == cw.comments[end].Pos.Line+1 {
		end++
	}
	if cw.comments[end].Pos.Line != declLine-1 {
		return ""
	}
	var lines []string
	for i := start; i <= end; i++ {
		lines = append(lines, strings.TrimSpace(cw.comments[i].Text))
	}
	cw.cursor = end + 1
	return strings.Join(lines, "\n")
}

func renderFunc(b *strings.Builder, fd *ast.FuncDecl, cw *commentWalker) {
	doc := cw.take(fd.P.Line)
	b.WriteString("## ")
	b.WriteString("`")
	b.WriteString(fd.Name)
	b.WriteString("`\n\n")
	b.WriteString("```lang\n")
	b.WriteString("pub function ")
	if fd.Receiver != nil {
		b.WriteString("(")
		b.WriteString(fd.Receiver.Name)
		b.WriteString(": ")
		b.WriteString(typeStr(fd.Receiver.Type))
		b.WriteString(") ")
	}
	b.WriteString(fd.Name)
	if len(fd.TypeParams) > 0 {
		b.WriteString("[")
		b.WriteString(strings.Join(fd.TypeParams, ", "))
		b.WriteString("]")
	}
	b.WriteString("(")
	for i, p := range fd.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name)
		b.WriteString(": ")
		b.WriteString(typeStr(p.Type))
	}
	b.WriteString("): ")
	b.WriteString(typeStr(fd.ReturnType))
	b.WriteString("\n```\n\n")
	if doc != "" {
		b.WriteString(doc)
		b.WriteString("\n\n")
	}
}

func renderStruct(b *strings.Builder, sd *ast.StructDecl, cw *commentWalker) {
	doc := cw.take(sd.P.Line)
	b.WriteString("## `struct ")
	b.WriteString(sd.Name)
	b.WriteString("`\n\n")
	b.WriteString("```lang\n")
	b.WriteString("pub struct ")
	b.WriteString(sd.Name)
	if len(sd.TypeParams) > 0 {
		b.WriteString("[")
		b.WriteString(strings.Join(sd.TypeParams, ", "))
		b.WriteString("]")
	}
	b.WriteString(" { ")
	for i, f := range sd.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(f.Name)
		b.WriteString(": ")
		b.WriteString(typeStr(f.Type))
	}
	b.WriteString(" }\n```\n\n")
	if doc != "" {
		b.WriteString(doc)
		b.WriteString("\n\n")
	}
}

func renderEnum(b *strings.Builder, ed *ast.EnumDecl, cw *commentWalker) {
	doc := cw.take(ed.P.Line)
	b.WriteString("## `enum ")
	b.WriteString(ed.Name)
	b.WriteString("`\n\n")
	b.WriteString("```lang\n")
	b.WriteString("pub enum ")
	b.WriteString(ed.Name)
	if len(ed.TypeParams) > 0 {
		b.WriteString("[")
		b.WriteString(strings.Join(ed.TypeParams, ", "))
		b.WriteString("]")
	}
	b.WriteString(" {\n")
	for _, v := range ed.Variants {
		b.WriteString("    ")
		b.WriteString(v.Name)
		if len(v.Payloads) > 0 {
			b.WriteString("(")
			for i, pt := range v.Payloads {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(typeStr(pt))
			}
			b.WriteString(")")
		}
		b.WriteString(",\n")
	}
	b.WriteString("}\n```\n\n")
	if doc != "" {
		b.WriteString(doc)
		b.WriteString("\n\n")
	}
}

func renderConst(b *strings.Builder, cd *ast.ConstDecl, cw *commentWalker) {
	doc := cw.take(cd.P.Line)
	b.WriteString("## `const ")
	b.WriteString(cd.Name)
	b.WriteString("`\n\n")
	b.WriteString("```lang\n")
	b.WriteString("pub const ")
	b.WriteString(cd.Name)
	if cd.Type != nil {
		b.WriteString(": ")
		b.WriteString(typeStr(cd.Type))
	}
	b.WriteString("\n```\n\n")
	if doc != "" {
		b.WriteString(doc)
		b.WriteString("\n\n")
	}
}

func typeStr(t ast.Type) string {
	if t == nil {
		return "?"
	}
	return t.String()
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "langdoc:", err)
	os.Exit(1)
}
