// Package playground compiles a Fern wasi:http handler straight to a
// Component Model binary, or to the core module behind it, in-process, for
// callers that have no filesystem and can't shell out to the `fern` CLI —
// chiefly cmd/fern-wasm, which runs inside the browser.
//
// It mirrors cmd/fern's `-target wasm32-wasi-http` path. The cli/run world
// is not here: the playground compiles it with the self-host compiler
// (examples/self_host/playground_run.fern), which does not yet emit for
// wasi-http (docs/PLAYGROUND-SELFHOST-WASM.md). The compose logic
// intentionally tracks the wasi-http branch of cmd/fern; if the
// ForceMemorySection recompile rules change there, change them here too.
package playground

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/platforms"
	"github.com/jakechampion/lang/internal/wasm/component"
)

// CompileHttpComponent compiles src to a wasi:http/incoming-handler@0.2.0
// component and returns the component bytes. Front-end errors (parse /
// check) come back formatted the same way the playground's other panes show
// them.
func CompileHttpComponent(src string) ([]byte, error) {
	prog, info, err := frontEnd(src, "wasm32-wasi-http")
	if err != nil {
		return nil, err
	}
	return httpHandlerComponent(prog, info)
}

// CompileHttpHandlerCore compiles a Fern `handle(req: HttpRequest,
// plat: Platform): HttpResponse` program to the *raw* preview-2 core
// module that backs the wasi:http/incoming-handler component — the
// same bytes httpHandlerComponent composes, but *before* the
// component wrap. The module exports `__http_entry` (core signature
// `(i32 incoming-request, i32 response-outparam) -> ()`) plus
// `memory` and `cabi_realloc`, and imports the wasi:http/types +
// wasi:io/streams canonical-ABI functions.
//
// A browser can instantiate this directly against a hand-written
// host (web/wasi-http-shim.js) that mints request/response resource
// handles and marshals the Canonical ABI — no jco, no Component
// Model transpile. The playground's "Run (wasm)" path uses it for
// the wasi-http world: synthesise an incoming-request from a
// user-supplied request, call `__http_entry`, read back the
// response the guest committed via response-outparam.set.
func CompileHttpHandlerCore(src string) ([]byte, error) {
	prog, info, err := frontEnd(src, "wasm32-wasi-http")
	if err != nil {
		return nil, err
	}
	return wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		HttpHandler:        true,
		Preview2WASI:       true,
		ForceMemorySection: true,
	})
}

// frontEnd runs the shared parse → constfold → check → monomorph
// pipeline. Errors are formatted with diag so the playground shows
// the same caret diagnostics it does for Run / View assembly. target is
// the world's target name, whose two halves `target_os()` and
// `target_arch()` fold to; empty leaves both calls for the checker.
func frontEnd(src, target string) (*ast.Program, *checker.Info, error) {
	// modload (not bare parser.Parse) so the program's `std/…` /
	// `core/…` imports resolve — the auto-prelude is gone, so stdlib
	// is in scope only when imported.
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		return nil, nil, fmt.Errorf("%s", diag.Format("<playground>", src, err))
	}
	targetOS, targetArch := "", ""
	if d := platforms.ForTarget(target); d != nil {
		targetOS, targetArch = d.Environment, d.ISA
	}
	if err := constfold.FoldWith(prog, constfold.Inputs{TargetOS: targetOS, TargetArch: targetArch}); err != nil {
		return nil, nil, fmt.Errorf("%s", diag.Format("<playground>", src, err))
	}
	info, err := checker.Check(prog)
	if err != nil {
		return nil, nil, fmt.Errorf("%s", diag.Format("<playground>", src, err))
	}
	if err := monomorph.Run(prog, info); err != nil {
		return nil, nil, fmt.Errorf("%s", diag.Format("<playground>", src, err))
	}
	return prog, info, nil
}

// httpHandlerComponent mirrors cmd/fern's `-target wasi-http` path:
// build the handler core module and compose the
// wasi:http/incoming-handler@0.2.0 export. The proxy world the
// component runs under grants clocks + random but not env / args /
// files / stdin, so a handler that reaches for those is rejected up
// front with the same message the CLI gives.
func httpHandlerComponent(prog *ast.Program, info *checker.Info) ([]byte, error) {
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		HttpHandler:        true,
		Preview2WASI:       true,
		ForceMemorySection: true,
	})
	if err != nil {
		return nil, err
	}
	req, unsupported := component.ClassifyCore(core)
	if len(unsupported) > 0 {
		return nil, fmt.Errorf("can't compose a handler that imports %s yet — remove the source that pulls them in", strings.Join(unsupported, ", "))
	}
	if req.Args || req.Env || req.Stdin ||
		req.File.Any() {
		return nil, fmt.Errorf("a handler can't use env / args / files / stdin — the http proxy world doesn't grant them")
	}
	return component.Compose(core, req, "wasi:http/incoming-handler@0.2.0#handle"), nil
}
