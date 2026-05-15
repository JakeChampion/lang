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
// State is fresh per call. Imports aren't supported (modload
// reads files from disk; the browser has none). TCP / file I/O
// builtins on the lang side would error at runtime — that's
// fine for the playground use case (showcase, REPL-style demo).
package main

import (
	"bytes"
	"fmt"
	"strings"
	"syscall/js"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/interp"
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

func main() {
	// Register the JS-side entry point. `langInterpret(src)` is
	// the only export the playground page consumes.
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
	// Keep the Go runtime alive — js.FuncOf handlers are
	// invoked from the JS event loop, so main() must not return.
	select {}
}
