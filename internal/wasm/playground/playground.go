// Package playground compiles Fern source straight to a Component
// Model binary in-process, for callers that have no filesystem and
// can't shell out to the `fern` CLI — chiefly cmd/fern-wasm, which
// runs inside the browser.
//
// It mirrors the two component-producing CLI targets in
// cmd/fern/main.go:
//
//   world "wasm"      → a wasi:cli/run component (runnable as-is)
//   world "wasi-http" → a wasi:http/incoming-handler@0.2.0 component
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
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/wasm/component"
)

// CompileComponent compiles src to a Component Model binary for the
// given world ("wasm" or "wasi-http") and returns the component
// bytes. Front-end errors (parse / check) come back formatted the
// same way the playground's other panes show them.
func CompileComponent(src, world string) ([]byte, error) {
	prog, info, err := frontEnd(src)
	if err != nil {
		return nil, err
	}
	switch world {
	case "wasm":
		return cliRunComponent(prog, info)
	case "wasi-http":
		return httpHandlerComponent(prog, info)
	default:
		return nil, fmt.Errorf("unknown world %q (want \"wasm\" or \"wasi-http\")", world)
	}
}

// frontEnd runs the shared parse → constfold → check → monomorph
// pipeline. Errors are formatted with diag so the playground shows
// the same caret diagnostics it does for Run / View assembly.
func frontEnd(src string) (*ast.Program, *checker.Info, error) {
	prog, err := parser.Parse(src)
	if err != nil {
		return nil, nil, fmt.Errorf("%s", diag.Format("<playground>", src, err))
	}
	if err := constfold.Fold(prog); err != nil {
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
		req.FileRead || req.FileWrite || req.FileAppend ||
		req.FileReadWrite || req.FileReadWriteAppend {
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
		req.FileRead || req.FileWrite || req.FileAppend || req.FileReadWrite {
		return nil, fmt.Errorf("a handler can't use env / args / files / stdin — the http proxy world doesn't grant them")
	}
	return component.Compose(core, req, "wasi:http/incoming-handler@0.2.0#handle"), nil
}
