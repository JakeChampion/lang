package checker

import (
	"strings"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

// sourceNamed spells each name an imported module declares as its source
// does, `iter.ArrayIter` for the bundle's `iter__ArrayIter`.
func sourceNamed(prog *ast.Program, errs []error) {
	mangled := importedDeclNames(prog)
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

// importedDeclNames is every bundle name an imported module's declaration
// carries; an entry-module name is never mangled, whatever it contains.
func importedDeclNames(prog *ast.Program) map[string]bool {
	out := map[string]bool{}
	add := func(name, module string) {
		if module != "" && module != prog.EntryModule && strings.Index(name, "__") > 0 {
			out[name] = true
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

// demangleNamed demangles each whole word of s that mangled holds, leaving
// every other word as written.
func demangleNamed(s string, mangled map[string]bool) string {
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
		if mangled[s[i:j]] {
			b.WriteString(s[kept:i])
			b.WriteString(demangle(s[i:j]))
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
