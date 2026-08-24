// Capability enforcement (Phase 2 of the platform-descriptor design;
// plan item D1 of docs/NICHE-BORROWS-PLAN.md). Roc's platform model —
// verified in docs/NICHE-LANGUAGE-RESEARCH.md — treats the target as
// the capability boundary: what a target doesn't provide should not be
// *expressible* in a program compiled for it. Fern's version: after
// tree-shaking (so unused imported stdlib wrappers don't trip gates),
// every call to a capability-gated runtime builtin that survives in
// the compiled call graph must be granted by the target's Descriptor.
// Violations surface as E066 at check/compile time — replacing the
// mid-build "undefined label" / "unsupported" failures (and, worse,
// host-side instantiation errors) users hit today.
//
// The walk is deliberately simple: the caller hands us the ALREADY
// tree-shaken program (cmd/fern mirrors each backend's pre-shake), so
// every surviving function is part of the binary and a flat scan of
// every body is exactly "what the artifact can reach". Builtins are
// free functions, so only Ident-callee calls need checking.
package platforms

import (
	"fmt"
	"sort"

	"github.com/jakechampion/lang/internal/ast"
)

// gatedBuiltins maps each capability-gated runtime builtin to the
// capability a target must list in Descriptor.Capabilities for the
// builtin to be callable. Builtins NOT in this table (env, math,
// map/array/string runtime, …) are ungated — every target provides
// them. The async/readiness set (poll, timer_fd, wasm_* pollables) is
// ungated by default, not by decision.
var gatedBuiltins = map[string]string{
	// Process spawning.
	"subprocess": "subprocess",

	// Process supervision (fork/waitpid — docs/CRASH-ONLY-SERVE.md
	// D2'). Native targets only; wasm worlds have no processes. The
	// interp is deliberately ungated: its proc_fork answers -38
	// (ENOSYS) so callers can degrade at runtime instead.
	"proc_fork":    "proc",
	"proc_waitpid": "proc",
	"proc_exec":    "proc",

	// One-level bump-arena checkpoint (__heap_mark / __heap_release_to).
	// Native-only: both natives rewind __fern_heap_ptr and snapshot the
	// freelist heads into a .bss shadow, which wasm's linear-memory
	// allocator has no room for below its head table. `__heap_bump_bytes`
	// stays ungated — reading the cursor works everywhere; only rewinding
	// it is native. Gating turns an internal "unknown callee
	// __fern_heap_mark" mid-build failure into E066 at check time.
	"__heap_mark":       "arena",
	"__heap_release_to": "arena",

	// Blocking stdin reads (a webserver target has no stdin).
	"read_line": "stdin",
	"stdin":     "stdin",

	// The line is `log` = somewhere to put a line of diagnostics,
	// `stdout` = an actual stdout stream. wasi-http is why they are
	// separate: the proxy world grants `log` and has no wasi:cli/stdout.
	// So `write` sits on the far side of the line from `print` despite
	// being print-without-the-newline — it names the stream, not a sink.
	"print":   "log",
	"eprint":  "log",
	"stdout":  "stdout",
	"stderr":  "stdout",
	"write":   "stdout",
	"putchar": "stdout",

	// Clocks, and the wakeups driven by them. The pollable
	// constructors carry the authority; `poll` / `wasm_block` and the
	// other readiness helpers that WAIT on one do not (see coreBuiltins).
	"now_unix_ms":         "now",
	"now_ns":              "now",
	"monotonic_ns":        "now",
	"sleep_ms":            "now",
	"timer_fd":            "now",
	"wasm_timer_pollable": "now",

	// The ambient invocation environment. argv and envp are adjacent on
	// the process stack and `_start` captures them together, but they
	// are separate capabilities because the proxy world has envp and no
	// argv: `wasi-http` grants `env` and cannot answer `args`.
	"env":  "env",
	"args": "args",

	// Entropy. A syscall on native (getrandom / getentropy) and a host
	// import on wasm — never something the program can compute.
	"random_bytes": "random",
	"random_i32":   "random",

	// Sockets.
	"tcp_listen":   "tcp",
	"tcp_accept":   "tcp",
	"tcp_connect":  "tcp",
	"tcp_recv":     "tcp",
	"tcp_send":     "tcp",
	"tcp_close":    "tcp",
	"tcp_pollable": "tcp",
	"udp_send":     "tcp",

	// Filesystem.
	"read_file":       "fs",
	"read_file_bytes": "fs",
	"write_file":      "fs",
	"write_file_exec": "fs",
	"open_reader":     "fs",
	"open_writer":     "fs",
	"open_appender":   "fs",
	"stat":            "fs",
	"read_dir":        "fs",
	"remove_file":     "fs",
	"remove_dir_all":  "fs",
	"create_dir_all":  "fs",
	"temp_dir":        "fs",
}

// coreBuiltins is the other half of the classification: user-callable
// builtins that need no host at all, so a freestanding target provides
// them (#6506). Three groups, and the reason each is core differs:
//
//   - Allocation. map_new / cell_new / string_from_bytes_unchecked and
//     the strbuf scratch surface need an ALLOCATOR, not an OS. Whoever
//     seeds the heap region decides where the bytes come from; the
//     builtin does not care.
//   - Pure computation. The float bit casts compile to a register move.
//   - Readiness. poll / wasm_block / wasm_poll / wasm_pollable_drop WAIT
//     on a pollable someone else constructed, and every constructor is
//     gated above, so gating the wait too would double-count the same
//     authority.
//
// `exit` and `isatty` are the deliberate judgement calls. `exit` is
// host-shaped — a hosted process exits through the kernel — but every
// target can define "stop", including a freestanding one (trap, reset,
// or return to the embedder). `isatty` is the same shape: every target
// can define "is this fd a terminal", and a target with no terminal
// answers no. Gating it would make the question unaskable exactly where
// the answer matters most, leaving a colouriser to assume a terminal —
// the wrong default this primitive exists to fix. Both are core with a
// target-specific lowering rather than a capability an artifact could be
// refused.
var coreBuiltins = map[string]bool{
	"exit":   true,
	"isatty": true,

	"map_new":                     true,
	"cell_new":                    true,
	"string_from_bytes_unchecked": true,
	"slice_unchecked":             true,
	"strbuf_reset":                true,
	"strbuf_append":               true,
	"strbuf_take":                 true,

	"f32_bits":      true,
	"f32_from_bits": true,
	"f64_bits":      true,
	"f64_from_bits": true,

	"poll":               true,
	"wasm_block":         true,
	"wasm_poll":          true,
	"wasm_pollable_drop": true,
}

// CoreBuiltin reports whether the named builtin needs no host — the
// complement of GatedBuiltin over the user-callable registry.
func CoreBuiltin(name string) bool { return coreBuiltins[name] }

// GatedBuiltin reports the capability gating the named builtin, if any.
func GatedBuiltin(name string) (string, bool) {
	c, ok := gatedBuiltins[name]
	return c, ok
}

// TargetsProviding lists (sorted) the targets whose descriptor grants
// the capability — used in E066's "provided by: …" hint.
func TargetsProviding(capability string) []string {
	var out []string
	for name := range table {
		if HasCapability(name, capability) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Violation is one gated call the target cannot satisfy. Pos is the
// call site's position; note that for functions that came from an
// imported module (FuncModule != "") the position indexes THAT
// module's source, which the entry-file-oriented diagnostic renderer
// cannot display — callers should degrade to a position-less message
// carrying FuncName instead.
type Violation struct {
	Builtin    string
	Capability string
	Target     string
	Pos        ast.Position
	FuncName   string
	FuncModule string // "" when declared in the entry module
}

// Message renders the violation's human-readable text (without
// position — the caller owns position handling). entryModule is the
// module path modload stamps on the entry file's functions (the
// source path); violations inside the entry module skip the
// "reached via" context since their position already points there.
func (v Violation) Message(entryModule string) string {
	where := ""
	if v.FuncModule != "" && v.FuncModule != entryModule {
		where = fmt.Sprintf(" (reached via %q from module %q)", v.FuncName, v.FuncModule)
	}
	providers := TargetsProviding(v.Capability)
	if len(providers) == 0 {
		return fmt.Sprintf("target %q does not provide `%s`, required by `%s`%s; no compiled target provides it (available under `fern -interp` only)",
			v.Target, v.Capability, v.Builtin, where)
	}
	return fmt.Sprintf("target %q does not provide `%s`, required by `%s`%s; targets providing it: %v",
		v.Target, v.Capability, v.Builtin, where, providers)
}

// Enforce scans the (already tree-shaken) program for calls to gated
// builtins the target's descriptor does not grant. Unknown targets
// return nil — the -target flag validation owns that error. Results
// are in deterministic order (function declaration order, then source
// position), deduplicated per (function, builtin).
func Enforce(prog *ast.Program, target string) []Violation {
	d := ForTarget(target)
	if d == nil {
		return nil
	}
	granted := map[string]bool{}
	for _, c := range d.Capabilities {
		granted[c] = true
	}
	var out []Violation
	scanGatedCalls(prog, func(fn *ast.FuncDecl, call *ast.Call, builtin, capName string) {
		if granted[capName] {
			return
		}
		out = append(out, Violation{
			Builtin:    builtin,
			Capability: capName,
			Target:     target,
			Pos:        call.P,
			FuncName:   fn.Name,
			FuncModule: fn.SourceModule,
		})
	})
	return out
}

// scanGatedCalls visits every call to a gated builtin in prog, at most
// once per (function, builtin), in declaration-then-position order.
//
// The walk is flat rather than call-graph-following for the reason in
// the package comment: callers hand over a program whose every surviving
// function is part of the artifact, so "what this program can reach" is
// exactly the union over all bodies.
func scanGatedCalls(prog *ast.Program, visit func(fn *ast.FuncDecl, call *ast.Call, builtin, capability string)) {
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		seen := map[string]bool{} // builtin -> already visited for this fn
		ast.Walk(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.Call)
			if !ok {
				return true
			}
			id, ok := call.Callee.(*ast.Ident)
			if !ok {
				return true
			}
			capName, gated := gatedBuiltins[id.Name]
			if !gated || seen[id.Name] {
				return true
			}
			seen[id.Name] = true
			visit(fn, call, id.Name, capName)
			return true
		})
	}
}

// Reach returns the host capabilities prog can reach, each mapped to one
// builtin that demonstrates it — the target-independent half of Enforce.
//
// This is what makes "can this module be used without a host?" a DERIVED
// property rather than an asserted one (#6512): a module whose reach is
// empty is core-safe, and one that gains a host dependency changes its
// reach without anyone having to remember to reclassify it.
//
// The example builtin is chosen deterministically (lowest name) so the
// result is stable enough to pin in a test.
func Reach(prog *ast.Program) map[string]string {
	out := map[string]string{}
	scanGatedCalls(prog, func(_ *ast.FuncDecl, _ *ast.Call, builtin, capName string) {
		if prev, ok := out[capName]; !ok || builtin < prev {
			out[capName] = builtin
		}
	})
	return out
}
