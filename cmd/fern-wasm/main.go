//go:build js && wasm

// cmd/fern-wasm — the browser-side half of the Go toolchain the playground
// still needs. Build with:
//
//	GOOS=js GOARCH=wasm go build -o web/fern.wasm \
//	    github.com/jakechampion/lang/cmd/fern-wasm
//
// Compiling, checking, interpreting, the assembly pane and the wasi:cli/run
// component all run on the self-host compiler, web/playground.wasm
// (examples/self_host/playground_run.fern through web/wasi-shim.js). What
// is left here is what that compiler does not do yet
// (docs/PLAYGROUND-SELFHOST-WASM.md): the language server, and the
// wasi:http handler world.
//
// API surface (set as globals on `globalThis` so the page can
// call them without an explicit binding step):
//
//	fernLsp(jsonRpcRequestString) -> jsonRpcResponseString
//	  Routes a single LSP message into the in-process server in
//	  internal/lsp and returns the JSON-encoded response. Empty
//	  string for notifications (which have no response). The
//	  same Server instance persists across calls so document
//	  state carries between requests.
//
//	fernLspOnNotify(callback)
//	  Installs a JS function the server invokes on every push
//	  notification (publishDiagnostics, etc). The callback gets
//	  (method: string, params: object).
//
//	fernCompileHttpComponent(src) -> {
//	    wasm:   string,        // base64 of the component binary
//	    error:  string | null, // parse / check / compose failure
//	}
//	  Compiles src to a wasi:http/incoming-handler component — the
//	  same bytes `fern -target wasm32-wasi-http` writes — so the page
//	  can offer it for download. Bytes come back base64-encoded so
//	  they survive the syscall/js boundary as a plain string; the
//	  page decodes with atob into a Uint8Array.
//
//	fernCompileHttpHandlerCore(src) -> {
//	    wasm:   string,        // base64 of a wasi:http core module
//	    error:  string | null, // parse / check / codegen failure
//	}
//	  Compiles a `handle(req: HttpRequest, plat: Platform):
//	  HttpResponse` program to the raw core module backing the
//	  wasi:http/incoming-handler component (exports
//	  `wasi:http/incoming-handler@0.2.0#handle` + `memory` +
//	  `cabi_realloc`). The page instantiates it against
//	  web/wasi-http-shim.js — a hand-written Canonical-ABI host that
//	  synthesises an incoming-request and reads back the response —
//	  to run a user HTTP handler in-browser with no jco. Base64-
//	  encoded like the component.
//
// Source is loaded through modload.LoadSource (the entry is held in
// memory and the embedded std/ + core/ FS resolves stdlib imports), so
// `import "std/…";` / `import "core/…";` work in-browser. Relative-path
// imports still can't resolve (the browser has no disk).
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"syscall/js"

	"github.com/jakechampion/lang/internal/lsp"
	"github.com/jakechampion/lang/internal/wasm/playground"
)

// compileHttpComponent compiles src to a wasi:http/incoming-handler
// component and returns a JS-shaped result with the bytes
// base64-encoded (see the API surface comment at the top of file).
func compileHttpComponent(src string) map[string]any {
	result := map[string]any{
		"wasm":  "",
		"error": nil,
	}

	defer func() {
		if r := recover(); r != nil {
			result["error"] = fmt.Sprintf("internal: %v", r)
		}
	}()

	bin, err := playground.CompileHttpComponent(src)
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	result["wasm"] = base64.StdEncoding.EncodeToString(bin)
	return result
}

// compileHttpHandlerCore compiles a wasi:http handler program to the
// raw core module backing the incoming-handler component, returning
// the bytes base64-encoded. Drives the playground's "Run (wasm)"
// path for the wasi-http world via web/wasi-http-shim.js.
func compileHttpHandlerCore(src string) map[string]any {
	result := map[string]any{
		"wasm":  "",
		"error": nil,
	}

	defer func() {
		if r := recover(); r != nil {
			result["error"] = fmt.Sprintf("internal: %v", r)
		}
	}()

	bin, err := playground.CompileHttpHandlerCore(src)
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	result["wasm"] = base64.StdEncoding.EncodeToString(bin)
	return result
}

// lspServer is the persistent LSP server backing fernLsp /
// fernLspOnNotify. A single instance owns the open-document cache
// so request-response pairs make sense across calls.
var lspServer = lsp.NewServer()

// lspNotify is the JS callback the server invokes on every push
// notification. nil until fernLspOnNotify(...) installs one;
// notifications fired before then are dropped.
var lspNotify js.Value

func main() {
	// Wire the server's publisher to the JS callback so
	// publishDiagnostics (etc.) reach the page. Marshalling the
	// params through JSON keeps the wire shape identical to what
	// a stdio LSP client would receive.
	lspServer.SetPublisher(func(method string, params any) {
		if lspNotify.IsUndefined() || lspNotify.IsNull() {
			return
		}
		b, err := json.Marshal(params)
		if err != nil {
			return
		}
		var asAny any
		if err := json.Unmarshal(b, &asAny); err != nil {
			return
		}
		// Invoke from the JS event loop (we're already on it).
		lspNotify.Invoke(method, jsValueOf(asAny))
	})

	js.Global().Set("fernLsp", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 1 {
			return ""
		}
		resp := lspServer.HandleMessage([]byte(args[0].String()))
		if resp == nil {
			return ""
		}
		return string(resp)
	}))

	js.Global().Set("fernLspOnNotify", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 1 {
			lspNotify = js.Undefined()
			return nil
		}
		lspNotify = args[0]
		return nil
	}))

	js.Global().Set("fernCompileHttpComponent", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 1 {
			return map[string]any{
				"wasm":  "",
				"error": "fernCompileHttpComponent(src) requires one string argument",
			}
		}
		return compileHttpComponent(args[0].String())
	}))

	js.Global().Set("fernCompileHttpHandlerCore", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 1 {
			return map[string]any{
				"wasm":  "",
				"error": "fernCompileHttpHandlerCore(src) requires one string argument",
			}
		}
		return compileHttpHandlerCore(args[0].String())
	}))

	// Keep the Go runtime alive — js.FuncOf handlers are
	// invoked from the JS event loop, so main() must not return.
	select {}
}

// jsValueOf converts an arbitrary Go value (typically a
// json.Unmarshal-into-any result) into a JS-side value that
// js.Value.Invoke can pass as an argument. syscall/js handles
// map[string]any and []any natively, so the recursive descent
// stays compact.
func jsValueOf(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, vv := range x {
			out[k] = jsValueOf(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = jsValueOf(vv)
		}
		return out
	}
	return v
}
