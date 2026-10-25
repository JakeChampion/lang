package checker

import (
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// sourceNamed spells each bundle name as its source does: `iter.ArrayIter`
// for an imported module's `iter__ArrayIter`, and the entry's own name for a
// declaration that shadows a built-in type.
func sourceNamed(prog *ast.Program, errs []error) {
	mangled := importedDeclNames(prog)
	for bundle, name := range prog.BuiltinShadows {
		mangled[bundle] = name
	}
	if len(mangled) == 0 {
		return
	}
	for _, err := range errs {
		e, ok := err.(*Error)
		if !ok {
			continue
		}
		e.Msg = demangleNamed(e.Msg, mangled)
		e.Note = demangleNamed(e.Note, mangled)
		for i := range e.Related {
			e.Related[i].Message = demangleNamed(e.Related[i].Message, mangled)
		}
	}
}

// importedDeclNames maps every bundle name an imported module's declaration
// carries to its source spelling; an entry-module name is never mangled,
// whatever it contains.
func importedDeclNames(prog *ast.Program) map[string]string {
	out := map[string]string{}
	add := func(name, module string) {
		if module != "" && module != prog.EntryModule && strings.Index(name, "__") > 0 {
			out[name] = demangle(name)
		}
	}
	for _, d := range prog.Funcs {
		add(d.Name, d.SourceModule)
	}
	for _, d := range prog.Structs {
		add(d.Name, d.SourceModule)
	}
	for _, d := range prog.Enums {
		add(d.Name, d.SourceModule)
	}
	for _, d := range prog.Unions {
		add(d.Name, d.SourceModule)
	}
	for _, d := range prog.Traits {
		add(d.Name, d.SourceModule)
	}
	for _, d := range prog.Consts {
		add(d.Name, d.SourceModule)
	}
	return out
}

// demangleNamed respells each whole word of s that mangled holds, leaving
// every other word as written.
func demangleNamed(s string, mangled map[string]string) string {
	identPart := func(b byte) bool {
		return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
	}
	var b strings.Builder
	kept := 0
	for i := 0; i < len(s); {
		if !identPart(s[i]) {
			i++
			continue
		}
		j := i
		for j < len(s) && identPart(s[j]) {
			j++
		}
		if src, ok := mangled[s[i:j]]; ok {
			b.WriteString(s[kept:i])
			b.WriteString(src)
			kept = j
		}
		i = j
	}
	if kept == 0 {
		return s
	}
	b.WriteString(s[kept:])
	return b.String()
}
