// Package fernrt is the native runtime's Fern half (#8038). runtime.fern
// defines runtime helpers as ordinary Fern functions under their exact
// runtime symbol names; a backend that needs one asks Func for its lowered IR
// and emits it through the same function emitter it uses for user code. The
// helper is therefore one source lowered per target, in place of a
// hand-written body per backend.
//
// The source is parsed, checked and lowered once per pointer width and cached.
// The returned declarations and IR are shared: callers read them and never
// mutate them.
package fernrt

import (
	_ "embed"
	"fmt"
	"sort"
	"sync"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

// Target is what a helper's body is lowered for: the pointer width, and the
// OS and ISA halves of the target name that `target_os()` and `target_arch()`
// fold to before the body is checked, so one source keys its syscall numbers
// and struct layouts by target and a branch on either folds away.
type Target struct {
	PtrW     int
	OS, Arch string
}

//go:embed runtime.fern
var source string

type lowered struct {
	decls map[string]*ast.FuncDecl
	funcs map[string]*ir.Func
}

type cacheKey struct {
	target  Target
	twoWord bool
}

var (
	mu    sync.Mutex
	cache = map[cacheKey]*lowered{}
	names map[string]bool
)

// front parses the source, folds the target calls for t and checks it. Every
// caller holds mu.
func front(t Target) (*ast.Program, *checker.Info, error) {
	prog, err := parser.Parse(source)
	if err != nil {
		return nil, nil, fmt.Errorf("fernrt: parse runtime.fern: %w", err)
	}
	if err := constfold.FoldWith(prog, constfold.Inputs{TargetOS: t.OS, TargetArch: t.Arch}); err != nil {
		return nil, nil, fmt.Errorf("fernrt: fold runtime.fern: %w", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		return nil, nil, fmt.Errorf("fernrt: check runtime.fern: %w", err)
	}
	return prog, info, nil
}

func helperNames() map[string]bool {
	if names != nil {
		return names
	}
	prog, _, err := front(Target{})
	if err != nil {
		panic(err)
	}
	names = map[string]bool{}
	for _, fn := range prog.Funcs {
		names[fn.Name] = true
	}
	return names
}

// Has reports whether name is a helper runtime.fern defines.
func Has(name string) bool {
	mu.Lock()
	defer mu.Unlock()
	return helperNames()[name]
}

// Names lists the helpers runtime.fern defines, sorted.
func Names() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, 0, len(helperNames()))
	for n := range helperNames() {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Func returns the declaration and lowered IR of the named helper for t. The
// string ABI follows ast.UseTwoWordStrings(t.PtrW) at the time of the call,
// as it does for the program the backend is emitting. The IR has been
// through ir.Inline and ir.OptimizeFunctions, so it arrives in the shape the
// emitters expect and a helper's call of a constant-returning sibling (a
// syscall number keyed by target) is the constant itself, which is what
// lets the x86-64 backend record it for the seccomp allowlist. Not the rest
// of the whole-program battery: helpers here are looked up BY NAME, so a
// pass that culled a function inlined into its sole caller would delete
// what a later lookup asks for.
func Func(name string, t Target) (*ast.FuncDecl, *ir.Func, error) {
	mu.Lock()
	defer mu.Unlock()
	k := cacheKey{target: t, twoWord: ast.UseTwoWordStrings(t.PtrW)}
	l, ok := cache[k]
	if !ok {
		prog, info, err := front(t)
		if err != nil {
			return nil, nil, err
		}
		// The helpers are not the program under measurement, and this
		// lowering is cached across -cover and plain builds alike.
		ip, err := ir.LowerWith(prog, info, t.PtrW, ir.CoverExempt())
		if err != nil {
			return nil, nil, fmt.Errorf("fernrt: lower runtime.fern: %w", err)
		}
		ir.Inline(ip)
		ir.OptimizeFunctions(ip)
		l = &lowered{decls: map[string]*ast.FuncDecl{}, funcs: map[string]*ir.Func{}}
		for _, fn := range prog.Funcs {
			l.decls[fn.Name] = fn
		}
		for _, fn := range ip.Funcs {
			l.funcs[fn.Name] = fn
		}
		cache[k] = l
	}
	decl, irFn := l.decls[name], l.funcs[name]
	if decl == nil || irFn == nil {
		return nil, nil, fmt.Errorf("fernrt: runtime.fern defines no helper %q", name)
	}
	return decl, irFn, nil
}
