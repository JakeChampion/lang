// Package gates holds the checks a loaded program passes before code
// generation, and Check, which runs them in `fern -check`'s order. Both
// `fern -check` and fern-lsp's diagnostics are Check, so the editor reports
// what the command line does.
package gates

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jakechampion/lang/internal/check/ambient"
	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/oracle/treeshake"
	"github.com/jakechampion/lang/internal/pkg/embed"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

// Warning is a finding that rejects nothing. Pos is zero when the warning
// has no place in the entry module.
type Warning struct {
	Pos ast.Position
	Msg string
}

// Format renders w as -check prints it, name standing for the entry module.
func (w Warning) Format(name string) string {
	if w.Pos.Line == 0 {
		return "warning: " + w.Msg
	}
	return fmt.Sprintf("%s:%d:%d: warning: %s", name, w.Pos.Line, w.Pos.Col, w.Msg)
}

// Check runs every gate between loading and code generation on prog, whose
// entry module is entry ("-" for stdin): the const fold, the type check,
// package capability grants (E070), the ambient-effect rule (E080),
// monomorphisation, the entry's remaining `todo` stubs (warnings), and, when
// target is not "", the target's capability set (E066). target is the -target a check was given explicitly: an unrequested
// check must not start enforcing the arm64 capability set against programs
// that check clean today.
//
// The warnings are returned even when a gate fails, up to that gate. Check
// mutates prog: the fold strips its consts, and the E066 pass tree-shakes it.
func Check(entry string, prog *ast.Program, target string, assets *embed.Set) ([]Warning, error) {
	// A check against a target folds the target's name as a compile does,
	// so the E066 pass below judges the arm the target takes.
	targetOS, targetArch := "", ""
	if d := platforms.ForTarget(target); d != nil {
		targetOS, targetArch = d.Environment, d.ISA
	}
	if err := constfold.FoldWith(prog, constfold.Inputs{Assets: assets, TargetOS: targetOS, TargetArch: targetArch}); err != nil {
		return nil, err
	}
	info, err := checker.CheckTarget(prog, target)
	if err != nil {
		return nil, err
	}
	var warns []Warning
	if entry != "-" {
		ws, err := Capabilities(entry, prog)
		warns = ws
		if err != nil {
			return warns, err
		}
	}
	if errs := Ambient(entry, prog); errs != nil {
		return warns, errs
	}
	if err := monomorph.Run(prog, info); err != nil {
		return warns, err
	}
	// A clean check still inventories the entry module's remaining `todo`
	// stubs. Imported modules' sites aren't tracked (see
	// ast.Program.TodoSites).
	for _, site := range prog.TodoSites {
		warns = append(warns, Warning{Pos: site, Msg: "`todo` stub remaining"})
	}
	// Last, because it tree-shakes prog.
	if target != "" {
		if errs := Target(entry, prog, info, target, false, ""); errs != nil {
			return warns, errs
		}
	}
	return warns, nil
}

// Ambient runs the ambient-effect rule (E080): a function handed a
// platform must reach every host effect through it. Target-independent,
// so it runs on every check and every build, and on the program before the
// tree-shake: a handler is judged on what its body reaches, whether or not
// this program serves it.
func Ambient(entry string, prog *ast.Program) diag.Errors {
	vs := ambient.Enforce(prog)
	if len(vs) == 0 {
		return nil
	}
	entry = entryModule(entry)
	var errs diag.Errors
	for _, v := range vs {
		ce := &checker.Error{Pos: v.Pos, Msg: v.Message(entry), ErrCode: "E080"}
		// Only the entry module's positions index the file the renderer
		// displays; a handler declared in an imported module degrades to a
		// position-less entry that names the module instead.
		if v.FuncModule != "" && v.FuncModule != entry {
			ce.Pos = ast.Position{}
		}
		errs = append(errs, ce)
	}
	return errs
}

// Target runs the platforms Phase 2 gate (E066): reject, before codegen,
// any call to a runtime builtin the target's descriptor doesn't grant
// (`subprocess` on wasm, filesystem/tcp/stdin under wasi-http, anything
// host-mediated under freestanding) — turning mid-build "undefined
// label"/"unsupported" failures into positioned, `fern explain`-able
// errors. Tree-shake first so unused imported stdlib wrappers don't trip
// gates: this mirrors each backend's own pre-shake (same dyn-dispatch roots
// + -shared exports; backends re-shake idempotently), including wasi-http's
// drop of the synthesised serve `main`.
//
// Returns nil when the target has no descriptor or nothing violates its
// capability set. NOTE this mutates prog by tree-shaking it.
func Target(entry string, prog *ast.Program, info *checker.Info, target string, shared bool, export string) diag.Errors {
	if platforms.ForTarget(target) == nil {
		return nil
	}
	if target == "wasm32-wasi-http" {
		kept := prog.Funcs[:0]
		for _, fn := range prog.Funcs {
			if fn.IsSynthesisedHandlerMain {
				continue
			}
			kept = append(kept, fn)
		}
		prog.Funcs = kept
	}
	// treeshake roots the `dyn Trait` vtable impl methods itself, from the
	// coercion / downcast sites it reaches. A Drop finalizer it cannot see:
	// the only caller is drop glue IR lowering synthesises later.
	extras := append([]string(nil), treeshake.DropImplMethods(info)...)
	if shared && export != "" {
		extras = append(extras, strings.Split(export, ",")...)
	}
	if target == "wasm32-wasi-http" {
		extras = append(extras, checker.WasiHandleName, "__method_HeaderMap_append", "__method_HttpResponse_body_bytes")
	}
	// WIT-exported functions are entry points the AST walk can't
	// see — keep them (and what they call) in the scanned set. An
	// `async function` with a body is the same kind of hidden entry
	// point: the wasmbin path lifts it as a component-level export
	// (`-emit core-module`, docs/WASI-PREVIEW3-ASYNC-PLAN.md), so it is
	// reachable from outside even when nothing in the program calls it.
	// Body-less `@import async` declarations are imports, not exports,
	// and are deliberately excluded.
	for _, fn := range prog.Funcs {
		if fn.ExportIface != "" || fn.ExportWITName != "" || (fn.Async && fn.ImportIface == "") {
			extras = append(extras, fn.Name)
		}
	}
	// The shake is here so E066 is judged on the REACHABLE set — an
	// unreachable call to a builtin the target lacks is not a violation.
	// Under -cover the program itself must survive it: a function nothing
	// calls is what a coverage report most needs to name, and codegen only
	// assigns counter sites to functions it lowers. Enforcement still runs
	// on the shaken set; the full set is put back for codegen, whose own
	// shake is gated the same way and whose post-lowering cull drops the
	// dead code again once its sites are registered.
	var preShake []*ast.FuncDecl
	if ast.CoverEnabled {
		preShake = append([]*ast.FuncDecl(nil), prog.Funcs...)
		defer func() { prog.Funcs = preShake }()
	}
	treeshake.Run(prog, info, extras...)
	vs := platforms.Enforce(prog, target)
	if len(vs) == 0 {
		return nil
	}
	entry = entryModule(entry)
	var errs diag.Errors
	for _, v := range vs {
		ce := &checker.Error{Pos: v.Pos, Msg: v.Message(entry), ErrCode: "E066"}
		// Only the entry module's positions index the file the renderer
		// displays. Violations inside imported modules
		// (std/…, ./util, …) degrade to a position-less entry — the
		// message names the function and module instead.
		if v.FuncModule != "" && v.FuncModule != entry {
			ce.Pos = ast.Position{}
		}
		errs = append(errs, ce)
	}
	return errs
}

// entryModule is the module path modload stamps on the entry's declarations:
// a file's absolute path, or LoadSource's for stdin.
func entryModule(entry string) string {
	if entry == "-" {
		return modload.SourceEntry
	}
	if abs, err := filepath.Abs(entry); err == nil {
		return abs
	}
	return entry
}
