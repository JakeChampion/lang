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
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

//go:embed runtime.fern
var source string

type lowered struct {
	decls map[string]*ast.FuncDecl
	funcs map[string]*ir.Func
}

type cacheKey struct {
	ptrW    int
	twoWord bool
}

var (
	mu    sync.Mutex
	cache = map[cacheKey]*lowered{}
	names map[string]bool
)

// front parses and checks the source. Every caller holds mu.
func front() (*ast.Program, *checker.Info, error) {
	prog, err := parser.Parse(source)
	if err != nil {
		return nil, nil, fmt.Errorf("fernrt: parse runtime.fern: %w", err)
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
	prog, _, err := front()
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

// Func returns the declaration and lowered IR of the named helper for a
// target whose pointers are ptrW bytes wide. The string ABI follows
// ast.UseTwoWordStrings(ptrW) at the time of the call, as it does for the
// program the backend is emitting. The IR has been through the same cleanup
// passes (FuseTee, FlattenBranches, EliminateDeadCode, OptimizeCleanup) every
// backend runs before emitting, so it arrives in the shape their emitters
// expect.
func Func(name string, ptrW int) (*ast.FuncDecl, *ir.Func, error) {
	mu.Lock()
	defer mu.Unlock()
	k := cacheKey{ptrW: ptrW, twoWord: ast.UseTwoWordStrings(ptrW)}
	l, ok := cache[k]
	if !ok {
		prog, info, err := front()
		if err != nil {
			return nil, nil, err
		}
		ip, err := ir.LowerWith(prog, info, ptrW)
		if err != nil {
			return nil, nil, fmt.Errorf("fernrt: lower runtime.fern: %w", err)
		}
		ir.FuseTee(ip)
		ir.FlattenBranches(ip)
		ir.EliminateDeadCode(ip)
		ir.OptimizeCleanup(ip)
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
