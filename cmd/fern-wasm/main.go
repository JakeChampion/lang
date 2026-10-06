//go:build js && wasm

// cmd/fern-wasm — the browser-side half of the Go toolchain the playground
// still needs. Build with:
//
//	GOOS=js GOARCH=wasm go build -o web/fern.wasm \
//	    github.com/jakechampion/lang/cmd/fern-wasm
//
// Compiling, checking, interpreting, the assembly pane and both worlds'
// components all run on the self-host compiler, web/playground.wasm
// (compiler/playground_run.fern through web/wasi-shim.js). What
// is left here is the language server, which that compiler does not have
// (docs/PLAYGROUND-SELFHOST-WASM.md).
//
// API surface (set as globals on `globalThis` so the page can
// call them without an explicit binding step):
//
//	fernLsp(jsonRpcRequestString) -> jsonRpcResponseString
//	  Routes a single LSP message into the in-process server in
//	  internal/tools/lsp and returns the JSON-encoded response. Empty
//	  string for notifications (which have no response). The
//	  same Server instance persists across calls so document
//	  state carries between requests.
//
//	fernLspOnNotify(callback)
//	  Installs a JS function the server invokes on every push
//	  notification (publishDiagnostics, etc). The callback gets
//	  (method: string, params: object).
//
// The server's document store sees the entry source only; `import
// "std/…";` resolves through the embedded stdlib the way the CLI's does.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/jakechampion/lang/internal/tools/lsp"
)

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
