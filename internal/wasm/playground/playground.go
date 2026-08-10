// Package playground compiles Fern source straight to a Component
// Model binary in-process, for callers that have no filesystem and
// can't shell out to the `fern` CLI — chiefly cmd/fern-wasm, which
// runs inside the browser.
//
// It mirrors the two component-producing CLI targets in
// cmd/fern/main.go:
//
//	world "wasm32-wasi"      → a wasi:cli/run component (runnable as-is)
//	world "wasm32-wasi-http" → a wasi:http/incoming-handler@0.2.0 component
//
// The compose logic intentionally tracks cmd/fern's
// buildPreview2Component / the wasi-http branch; if the
// ForceMemorySection recompile rules change there, change them here
// too.
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
	"github.com/jakechampion/lang/internal/wasm/component"
)

// CompileComponent compiles src to a Component Model binary for the
// given world ("wasm32-wasi" or "wasm32-wasi-http") and returns the component
// bytes. Front-end errors (parse / check) come back formatted the
// same way the playground's other panes show them.
func CompileComponent(src, world string) ([]byte, error) {
	prog, info, err := frontEnd(src)
	if err != nil {
		return nil, err
	}
	switch world {
	case "wasm32-wasi":
		return cliRunComponent(prog, info)
	case "wasm32-wasi-http":
		return httpHandlerComponent(prog, info)
	default:
		return nil, fmt.Errorf("unknown world %q (want \"wasm\" or \"wasi-http\")", world)
	}
}

// CompileCoreWasm compiles src to a raw preview-1 core WebAssembly
// command module — the same shape `fern -target wasm-bin` emits,
// with a synthesised `_start` entry that calls `main` and an
// exported linear `memory`. Unlike CompileComponent it produces a
// plain core module (Component Model layer 0x0000), which a browser
// can `WebAssembly.instantiate` directly against a small preview-1
// WASI shim — no jco / canonical-ABI transpile step in between. The
// playground's "Run (wasm)" button uses this to execute the actual
// compiled backend in-page, distinct from the AST interpreter that
// "Run" drives.
//
// Preview2WASI is deliberately left off: the classic
// wasi_snapshot_preview1 import names (fd_write, proc_exit,
// random_get, clock_time_get, args_*) are what the JS shim
// implements.
func CompileCoreWasm(src string) ([]byte, error) {
	prog, info, err := frontEnd(src)
	if err != nil {
		return nil, err
	}
	return wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		SynthStart:         true,
		ForceMemorySection: true,
	})
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
	prog, info, err := frontEnd(src)
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
// the same caret diagnostics it does for Run / View assembly.
func frontEnd(src string) (*ast.Program, *checker.Info, error) {
	// modload (not bare parser.Parse) so the program's `std/…` /
	// `core/…` imports resolve — the auto-prelude is gone, so stdlib
	// is in scope only when imported.
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		return nil, nil, fmt.Errorf("%s", diag.Format("<playground>", src, err))
	}
	if err := constfold.Fold(prog, nil); err != nil {
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

// cliRunComponent mirrors cmd/fern's `-target wasm` path: build a
// preview-2 core module, classify its imports, and compose the
// wasi:cli/run component. Import families that allocate through
// cabi_realloc (stdin / files / args / env) or write through caller
// retptrs (sockets) need the memory section forced, so they trigger
// a rebuild before composing.
func cliRunComponent(prog *ast.Program, info *checker.Info) ([]byte, error) {
	bin, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		Preview2WASI: true,
		SynthCliRun:  true,
	})
	if err != nil {
		return nil, err
	}
	req, unsupported := component.ClassifyCore(bin)
	if len(unsupported) > 0 {
		return nil, fmt.Errorf("can't compose a program that imports %s yet — remove the source that pulls them in", strings.Join(unsupported, ", "))
	}
	if component.RequestEmpty(req) {
		return component.BuildWasiCliRunComponent(bin, "_lang_run"), nil
	}
	b := bin
	if req.Tcp || req.Udp ||
		req.Stdin || req.Args || req.Env ||
		req.File.Any() {
		rb, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
			ForceMemorySection: true,
			Preview2WASI:       true,
			SynthCliRun:        true,
		})
		if err != nil {
			return nil, err
		}
		b = rb
	}
	return component.Compose(b, req, "_lang_run"), nil
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
