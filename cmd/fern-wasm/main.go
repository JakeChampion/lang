//go:build js && wasm

// cmd/fern-wasm — browser-side entry point that exposes the AST
// interpreter to JavaScript. Build with:
//
//	GOOS=js GOARCH=wasm go build -o web/fern.wasm \
//	    github.com/jakechampion/lang/cmd/fern-wasm
//
// The companion `web/index.html` loads the produced `fern.wasm`
// alongside Go's `wasm_exec.js` runtime and wires a textarea +
// Run button to the `fernInterpret` global this program exports.
//
// API surface (set as globals on `globalThis` so the page can
// call them without an explicit binding step):
//
//	fernInterpret(src) -> {
//	    stdout: string,
//	    stderr: string,
//	    exit:   number,        // process exit code 0..255
//	    error:  string | null, // parse / check / runtime failure
//	}
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
//	fernCompile(src, target) -> {
//	    asm:    string,        // emitted assembly
//	    error:  string | null, // parse / check / codegen failure
//	}
//	  Compiles src for one of the supported targets and returns
//	  the textual output the corresponding cmd/fern `-target`
//	  flag would have written. Targets: "arm64-linux" (Linux ELF),
//	  "arm64-darwin" (Mach-O variant), "x86-64-linux" (Linux ELF).
//	  The playground's "View assembly" pane consumes this for
//	  the Godbolt-style side-by-side experience. (The wasm
//	  target retired with the WAT backend — the wasmbin path
//	  emits binary bytes, not human-readable text.)
//
//	fernCompileComponent(src, world) -> {
//	    wasm:   string,        // base64 of the component binary
//	    world:  string,        // the world that was composed
//	    error:  string | null, // parse / check / compose failure
//	}
//	  Compiles src to a Component Model binary — the same bytes
//	  `fern -target wasm` / `-target wasi-http` write — so the page
//	  can offer it for download and local `wasmtime` / jco runs.
//	  Worlds: "wasm32-wasi" (a wasi:cli/run component) and "wasm32-wasi-http" (a
//	  wasi:http/incoming-handler component). Bytes come back
//	  base64-encoded so they survive the syscall/js boundary as a
//	  plain string; the page decodes with atob into a Uint8Array.
//
//	fernCompileCoreWasm(src) -> {
//	    wasm:   string,        // base64 of a preview-1 core module
//	    error:  string | null, // parse / check / codegen failure
//	}
//	  Compiles src to a raw preview-1 core WebAssembly command
//	  module (exported `_start` + `memory`, classic
//	  wasi_snapshot_preview1 imports). The page instantiates it
//	  directly via WebAssembly.instantiate against web/wasi-shim.js
//	  — no component / jco transpile step — to run the compiled
//	  backend (not the AST interpreter) in-browser. Base64-encoded
//	  like fernCompileComponent.
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
//	  encoded like the others.
//
// State is fresh per call for fernInterpret. Source is loaded
// through modload.LoadSource (the entry is held in memory and the
// embedded std/ + core/ FS resolves stdlib imports), so `import
// "std/…";` / `import "core/…";` work in-browser — required now
// that the auto-prelude is gone. Relative-path imports still can't
// resolve (the browser has no disk). TCP / file I/O builtins on the
// Fern side would error at runtime — that's fine for the playground
// use case (showcase, REPL-style demo).
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"syscall/js"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	x86_64codegen "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/lsp"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/wasm/playground"
)

// interpret runs src through the same pipeline `fern -interp -`
// uses CLI-side: constfold, checker, monomorph, AST interp.
// Returns a JS-shaped result object (see the API surface comment
// at the top of the file).
func interpret(src string) map[string]any {
	result := map[string]any{
		"stdout": "",
		"stderr": "",
		"exit":   0,
		"error":  nil,
	}

	defer func() {
		if r := recover(); r != nil {
			result["error"] = fmt.Sprintf("internal: %v", r)
			result["exit"] = 2
		}
	}()

	prog, _, err := modload.LoadSource(src)
	if err != nil {
		result["error"] = diag.Format("<playground>", src, err)
		result["exit"] = 1
		return result
	}
	if err := constfold.Fold(prog, nil); err != nil {
		result["error"] = diag.Format("<playground>", src, err)
		result["exit"] = 1
		return result
	}
	info, err := checker.Check(prog)
	if err != nil {
		result["error"] = diag.Format("<playground>", src, err)
		result["exit"] = 1
		return result
	}
	if err := monomorph.Run(prog, info); err != nil {
		result["error"] = diag.Format("<playground>", src, err)
		result["exit"] = 1
		return result
	}

	var stdout, stderr bytes.Buffer
	ip := interp.New()
	ip.Stdout = &stdout
	ip.Stderr = &stderr
	ip.Stdin = strings.NewReader("")
	// Override the exiter so `exit(N)` from user code doesn't kill
	// the host go-wasm runtime; capture the requested code instead.
	exitCode := -1
	ip.Exiter = func(code int) {
		exitCode = code
		panic(exitSignal{})
	}
	for _, ed := range prog.Enums {
		ip.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		ip.Register(fn)
	}

	if _, hasMain := ip.Funcs["main"]; !hasMain {
		result["stdout"] = stdout.String()
		result["stderr"] = stderr.String()
		result["error"] = "program has no `main` function to interpret"
		result["exit"] = 1
		return result
	}

	v, callErr := safeCall(ip)
	result["stdout"] = stdout.String()
	result["stderr"] = stderr.String()
	if exitCode >= 0 {
		result["exit"] = exitCode & 0xFF
		return result
	}
	if callErr != nil {
		result["error"] = callErr.Error()
		result["exit"] = 1
		return result
	}
	if n, ok := v.(interp.Number); ok {
		code := int(n)
		if code < 0 {
			code = -code
		}
		result["exit"] = code & 0xFF
	}
	return result
}

// exitSignal is the panic tag the overridden Exiter uses to
// short-circuit the interp's call stack. Caught in safeCall.
type exitSignal struct{}

func safeCall(ip *interp.Interp) (v interp.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(exitSignal); ok {
				// exit() — exitCode is already stashed on the
				// enclosing closure via the overridden Exiter.
				err = nil
				return
			}
			// Anything else is a real panic — surface it.
			panic(r)
		}
	}()
	return ip.CallByName("main", nil)
}

// compile runs src through parse → constfold → check → monomorph
// → backend.Emit for the requested target and returns the textual
// output (assembly for native targets, .wat for wasm). Mirrors the
// `fern -target X` CLI path so what the playground shows matches
// what a build on the user's machine would produce.
func compile(src, target string) map[string]any {
	result := map[string]any{
		"asm":   "",
		"error": nil,
	}

	defer func() {
		if r := recover(); r != nil {
			result["error"] = fmt.Sprintf("internal: %v", r)
		}
	}()

	prog, _, err := modload.LoadSource(src)
	if err != nil {
		result["error"] = diag.Format("<playground>", src, err)
		return result
	}
	if err := constfold.Fold(prog, nil); err != nil {
		result["error"] = diag.Format("<playground>", src, err)
		return result
	}
	info, err := checker.Check(prog)
	if err != nil {
		result["error"] = diag.Format("<playground>", src, err)
		return result
	}
	if err := monomorph.Run(prog, info); err != nil {
		result["error"] = diag.Format("<playground>", src, err)
		return result
	}

	var out string
	switch target {
	case "arm64-linux":
		out, err = arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{})
	case "arm64-darwin":
		out, err = arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{Darwin: true})
	case "x86-64-linux":
		out, err = x86_64codegen.EmitWithOptions(prog, info, x86_64codegen.Options{})
	default:
		result["error"] = fmt.Sprintf("unknown target %q (want arm64-linux, arm64-darwin, x86-64-linux)", target)
		return result
	}
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	result["asm"] = out
	return result
}

// compileComponent compiles src to a Component Model binary for the
// requested world and returns a JS-shaped result with the bytes
// base64-encoded (see the API surface comment at the top of file).
func compileComponent(src, world string) map[string]any {
	result := map[string]any{
		"wasm":  "",
		"world": world,
		"error": nil,
	}

	defer func() {
		if r := recover(); r != nil {
			result["error"] = fmt.Sprintf("internal: %v", r)
		}
	}()

	bin, err := playground.CompileComponent(src, world)
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	result["wasm"] = base64.StdEncoding.EncodeToString(bin)
	return result
}

// compileCoreWasm compiles src to a raw preview-1 core module and
// returns a JS-shaped result with the bytes base64-encoded (see the
// API surface comment at the top of file). Drives the playground's
// "Run (wasm)" button, which instantiates the result against
// web/wasi-shim.js.
func compileCoreWasm(src string) map[string]any {
	result := map[string]any{
		"wasm":  "",
		"error": nil,
	}

	defer func() {
		if r := recover(); r != nil {
			result["error"] = fmt.Sprintf("internal: %v", r)
		}
	}()

	bin, err := playground.CompileCoreWasm(src)
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

	js.Global().Set("fernInterpret", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 1 {
			return map[string]any{
				"stdout": "",
				"stderr": "",
				"exit":   2,
				"error":  "fernInterpret(src) requires one string argument",
			}
		}
		return interpret(args[0].String())
	}))

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

	js.Global().Set("fernCompile", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 2 {
			return map[string]any{
				"asm":   "",
				"error": "fernCompile(src, target) requires two string arguments",
			}
		}
		return compile(args[0].String(), args[1].String())
	}))

	js.Global().Set("fernCompileComponent", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 2 {
			return map[string]any{
				"wasm":  "",
				"world": "",
				"error": "fernCompileComponent(src, world) requires two string arguments",
			}
		}
		return compileComponent(args[0].String(), args[1].String())
	}))

	js.Global().Set("fernCompileCoreWasm", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 1 {
			return map[string]any{
				"wasm":  "",
				"error": "fernCompileCoreWasm(src) requires one string argument",
			}
		}
		return compileCoreWasm(args[0].String())
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
