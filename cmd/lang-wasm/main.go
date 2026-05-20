//go:build js && wasm

// cmd/lang-wasm — browser-side entry point that exposes the AST
// interpreter to JavaScript. Build with:
//
//   GOOS=js GOARCH=wasm go build -o web/lang.wasm \
//       github.com/jakechampion/lang/cmd/lang-wasm
//
// The companion `web/index.html` loads the produced `lang.wasm`
// alongside Go's `wasm_exec.js` runtime and wires a textarea +
// Run button to the `langInterpret` global this program exports.
//
// API surface (set as globals on `globalThis` so the page can
// call them without an explicit binding step):
//
//   langInterpret(src) -> {
//       stdout: string,
//       stderr: string,
//       exit:   number,        // process exit code 0..255
//       error:  string | null, // parse / check / runtime failure
//   }
//
//   langLsp(jsonRpcRequestString) -> jsonRpcResponseString
//     Routes a single LSP message into the in-process server in
//     internal/lsp and returns the JSON-encoded response. Empty
//     string for notifications (which have no response). The
//     same Server instance persists across calls so document
//     state carries between requests.
//
//   langLspOnNotify(callback)
//     Installs a JS function the server invokes on every push
//     notification (publishDiagnostics, etc). The callback gets
//     (method: string, params: object).
//
//   langCompile(src, target) -> {
//       asm:    string,        // emitted assembly
//       error:  string | null, // parse / check / codegen failure
//   }
//     Compiles src for one of the supported targets and returns
//     the textual output the corresponding cmd/lang `-target`
//     flag would have written. Targets: "arm64" (Linux ELF),
//     "arm64-darwin" (Mach-O variant), "x86-64" (Linux ELF).
//     The playground's "View assembly" pane consumes this for
//     the Godbolt-style side-by-side experience. (The wasm
//     target retired with the WAT backend — the wasmbin path
//     emits binary bytes, not human-readable text.)
//
// State is fresh per call for langInterpret. Imports aren't
// supported (modload reads files from disk; the browser has
// none). TCP / file I/O builtins on the lang side would error at
// runtime — that's fine for the playground use case (showcase,
// REPL-style demo).
package main

import (
	"bytes"
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
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
)

// interpret runs src through the same pipeline `lang -interp -`
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

	prog, err := parser.Parse(src)
	if err != nil {
		result["error"] = diag.Format("<playground>", src, err)
		result["exit"] = 1
		return result
	}
	if err := constfold.Fold(prog); err != nil {
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
// `lang -target X` CLI path so what the playground shows matches
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

	prog, err := parser.Parse(src)
	if err != nil {
		result["error"] = diag.Format("<playground>", src, err)
		return result
	}
	if err := constfold.Fold(prog); err != nil {
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
	case "arm64":
		out, err = arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{})
	case "arm64-darwin":
		out, err = arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{Darwin: true})
	case "x86-64":
		out, err = x86_64codegen.EmitWithOptions(prog, info, x86_64codegen.Options{})
	default:
		result["error"] = fmt.Sprintf("unknown target %q (want arm64, arm64-darwin, x86-64)", target)
		return result
	}
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	result["asm"] = out
	return result
}

// lspServer is the persistent LSP server backing langLsp /
// langLspOnNotify. A single instance owns the open-document cache
// so request-response pairs make sense across calls.
var lspServer = lsp.NewServer()

// lspNotify is the JS callback the server invokes on every push
// notification. nil until langLspOnNotify(...) installs one;
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

	js.Global().Set("langInterpret", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 1 {
			return map[string]any{
				"stdout": "",
				"stderr": "",
				"exit":   2,
				"error":  "langInterpret(src) requires one string argument",
			}
		}
		return interpret(args[0].String())
	}))

	js.Global().Set("langLsp", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 1 {
			return ""
		}
		resp := lspServer.HandleMessage([]byte(args[0].String()))
		if resp == nil {
			return ""
		}
		return string(resp)
	}))

	js.Global().Set("langLspOnNotify", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 1 {
			lspNotify = js.Undefined()
			return nil
		}
		lspNotify = args[0]
		return nil
	}))

	js.Global().Set("langCompile", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) < 2 {
			return map[string]any{
				"asm":   "",
				"error": "langCompile(src, target) requires two string arguments",
			}
		}
		return compile(args[0].String(), args[1].String())
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
