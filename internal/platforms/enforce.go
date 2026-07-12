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
// builtin to be callable. Builtins NOT in this table (print, eprint,
// env, now_unix_ms, math, map/array/string runtime, …) are ungated —
// every target provides them. The async/readiness set (poll,
// timer_fd, wasm_* pollables) is deliberately ungated for now: that
// surface is actively being reworked (see CLAUDE.md's wasm-IR
// exclusions note) and gating it would fight in-flight work.
var gatedBuiltins = map[string]string{
	// Process spawning.
	"subprocess": "subprocess",

	// Process supervision (fork/waitpid — docs/CRASH-ONLY-SERVE.md
	// D2'). Native targets only; wasm worlds have no processes. The
	// interp is deliberately ungated: its proc_fork answers -38
	// (ENOSYS) so callers can degrade at runtime instead.
	"proc_fork":    "proc",
	"proc_waitpid": "proc",

	// Blocking stdin reads (a webserver target has no stdin).
	"read_line": "stdin",
	"stdin":     "stdin",

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
	"read_file":      "fs",
	"write_file":     "fs",
	"open_reader":    "fs",
	"open_writer":    "fs",
	"open_appender":  "fs",
	"stat":           "fs",
	"read_dir":       "fs",
	"remove_file":    "fs",
	"remove_dir_all": "fs",
	"temp_dir":       "fs",
}

// GatedBuiltin reports the capability gating the named builtin, if any.
func GatedBuiltin(name string) (string, bool) {
	c, ok := gatedBuiltins[name]
	return c, ok
}

// TargetsProviding lists (sorted) the targets whose descriptor grants
// the capability — used in E066's "provided by: …" hint.
func TargetsProviding(capability string) []string {
	var out []string
	for name, d := range table {
		for _, c := range d.Capabilities {
			if c == capability {
				out = append(out, name)
				break
			}
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
	for _, fn := range prog.Funcs {
		if fn.Body == nil {
			continue
		}
		seen := map[string]bool{} // builtin -> reported for this fn
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
			if !gated || granted[capName] || seen[id.Name] {
				return true
			}
			seen[id.Name] = true
			out = append(out, Violation{
				Builtin:    id.Name,
				Capability: capName,
				Target:     target,
				Pos:        call.P,
				FuncName:   fn.Name,
				FuncModule: fn.SourceModule,
			})
			return true
		})
	}
	return out
}
