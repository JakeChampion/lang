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
// them. In the async/readiness set only the pollable CONSTRUCTORS are
// gated; the helpers that wait on one are core (see coreBuiltins).
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
	"proc_exec_as": "proc",
	// A process table with pids in it to ask about, which is the same
	// host property fork / exec / waitpid need.
	"process_alive": "proc",

	// A kernel-enforced ceiling on a process resource. Its own
	// capability rather than `proc`: that one is the authority to have
	// processes at all, while this is a property a host can lack while
	// still having them.
	"rlimit_nofile": "rlimit",

	// The filesystem itself — how large it is and what it will accept as
	// a name — rather than the files on it, which is `fs`. A host can
	// serve files and have no volume to measure.
	"statfs": "fsinfo",

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
	"sleep_ns":            "now",
	"wasm_timer_pollable": "now",

	// `timer_fd` is a clock wakeup too, but it is gated on the FD half
	// rather than on `now`: it hands back a file descriptor to poll,
	// which is what wasm cannot answer. `wasm_timer_pollable` above is
	// the same wakeup expressed as a wasi:io/poll pollable.
	"timer_fd": "pollfd",

	// The ambient invocation environment. argv and envp are adjacent on
	// the process stack and `_start` captures them together, but they
	// are separate capabilities because the proxy world has envp and no
	// argv: `wasi-http` grants `env` and cannot answer `args`. The
	// lookup and the whole list are one capability: a caller that can
	// ask for a name can ask for every name it can guess.
	"env":     "env",
	"environ": "env",
	"args":    "args",

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
	"open_reader":     "fs",
	"open_writer":     "fs",
	"open_appender":   "fs",
	"open_exclusive":  "fs",
	"stat":            "fs",
	"lstat":           "fs",
	"read_dir":        "fs",
	"remove_file":     "fs",
	"remove_dir_all":  "fs",
	"create_dir_all":  "fs",
	"create_dir":      "fs",
	"remove_dir":      "fs",
	"create_link":     "fs",
	"create_symlink":  "fs",
	"read_link":       "fs",
	"rename":          "fs",
	"set_file_times":  "fs",
	"temp_dir":        "fs",
	// Setting a file's LENGTH is a filesystem operation and not a
	// permission one: a host can have files, no mode word, and still
	// know how long each one is. Both WASI previews can set a size
	// through a descriptor, so it is granted there rather than refused.
	"truncate": "fs",

	// Entries that are neither a file nor a directory: a FIFO, and a
	// character or block device node. A host can serve files and
	// directories and have no way to name anything else, which is what
	// both WASI previews are — neither has a call that creates one, and
	// a regular file standing in for a FIFO would be the wrong answer
	// presented as the right one.
	"mknod": "fsnode",

	// Permission bits on a filesystem entry, which is a separate
	// capability from having a filesystem: a host can offer files and no
	// mode word to set on them. `access` is the READ of the same
	// property — "do the mode bits permit this for my effective ids" —
	// so it sits here rather than on `fs`, and a target with files and
	// no permission model refuses the question instead of answering it
	// wrongly.
	"write_file_exec": "fsmode",
	"access":          "fsmode",
	// The umask is the same property from the other side: the mode
	// bits a creation is allowed to keep. A host with files and no
	// permission model has no mask to set, and answering 0 would claim
	// every bit survives.
	"umask": "fsmode",
	// And `chmod` is the WRITE of it on an entry that already exists,
	// where `write_file_exec` only sets a bit on one it is creating.
	"chmod": "fsmode",

	// The process's own identity — the effective pair, the real pair,
	// and the supplementary group set. A host with no users cannot
	// answer any of them, and unlike `isatty` there is no correct
	// constant to fall back on: answering 0 claims to be root, and
	// WASI's FileStat uid/gid are also zero, so `-O` / `-G` would
	// report that every file is owned by the caller. An empty group
	// list is the same fiction one level down — it reads as "in no
	// groups", which is an answer rather than a refusal. See
	// docs/FREESTANDING-CORE.md.
	"geteuid":   "userid",
	"getegid":   "userid",
	"getuid":    "userid",
	"getgid":    "userid",
	"getgroups": "userid",

	// Signal dispositions. A signal is something a HOST delivers to a
	// process, so a target that runs no process cannot have one
	// ignored: wasi-cli grants this and answers with a no-op — a
	// component has no signals, and doing nothing is the whole truth
	// about ignoring one there — while a freestanding artifact has no
	// process to deliver to and does not get it at all
	// (docs/FREESTANDING-CORE.md).
	"signal_ignore":  "signal",
	"signal_default": "signal",

	// How large the terminal on the other end of a descriptor is. A
	// target with no terminal cannot answer: 0x0 is not "there is no
	// terminal" but "the terminal is empty", and a caller laying out
	// columns cannot tell those apart. `isatty` stays core beside this
	// because "no" IS the truthful answer to its question
	// (docs/FREESTANDING-CORE.md).
	"window_size": "tty",

	// The host's own name — the kernel node name gethostname(2) reports.
	// A hosted target asks its kernel; WASI has no host identity and
	// answers the empty string, which is a fact about a component rather
	// than a fiction (see docs/FREESTANDING-CORE.md on why that differs
	// from `userid`'s 0). A freestanding artifact has nothing to ask.
	"hostname": "host",

	// The MACHINE the process runs on: the kernel's utsname record
	// (`uname_field`) and how many processing units it may use
	// (`cpu_count`). Neither WASI preview has either — there is no
	// utsname to read and no processor count to report — and a
	// component that guessed would name a kernel it is not running
	// on, so this is a capability of its own rather than a corner of
	// `host` (which wasi-cli grants and answers honestly empty).
	"uname_field": "sysinfo",
	"cpu_count":   "sysinfo",

	// The process's working directory (`getcwd`). Separate from `fs`
	// for the same reason: WASI resolves every path against a
	// preopened descriptor and has no current directory at all, so
	// the question has no answer there rather than an empty one.
	"getcwd": "cwd",

	// The C-ABI FFI shims (#4375). Enumerated rather than matched by
	// prefix so this table stays the one place the classification lives —
	// the checker registers exactly these fifteen names.
	"__c_call0": "cabi", "__c_call0_f32": "cabi", "__c_call0_f64": "cabi",
	"__c_call1": "cabi", "__c_call1_f32": "cabi", "__c_call1_f64": "cabi",
	"__c_call2": "cabi", "__c_call2_f32": "cabi", "__c_call2_f64": "cabi",
	"__c_call3": "cabi", "__c_call3_f32": "cabi", "__c_call3_f64": "cabi",
	"__c_call4": "cabi", "__c_call4_f32": "cabi", "__c_call4_f64": "cabi",
}

// coreBuiltins is the other half of the classification: user-callable
// builtins that need no host at all, so a freestanding target provides
// them (#6506). Three groups, and the reason each is core differs:
//
//   - Allocation. map_new / cell_new / string_from_bytes_unchecked and
//     the strbuf scratch surface need an ALLOCATOR, not an OS. Whoever
//     seeds the heap region decides where the bytes come from; the
//     builtin does not care. The buf_* builder family (#8773) is the
//     same argument: bytes into a block this process allocated.
//   - Pure computation. The float bit casts compile to a register move.
//   - Readiness. poll / wasm_block / wasm_poll / wasm_pollable_drop WAIT
//     on a pollable someone else constructed, and every constructor is
//     gated above, so gating the wait too would double-count the same
//     authority.
//
// `target_os` and `target_arch` are pure in the strictest sense: each is
// a string literal by the time anything could run, folded from the two
// halves of the target's own name.
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
	"exit":        true,
	"isatty":      true,
	"target_os":   true,
	"target_arch": true,

	"map_new":                     true,
	"cell_new":                    true,
	"string_from_bytes_unchecked": true,
	"slice_unchecked":             true,
	"strbuf_reset":                true,
	"strbuf_append":               true,
	"strbuf_take":                 true,

	"buf_new":        true,
	"buf_push":       true,
	"buf_push_range": true,
	"buf_push_byte":  true,
	"buf_len":        true,
	"buf_take":       true,
	"buf_free":       true,

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
			FuncModule: fn.BodyModule(),
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

// GatedBuiltins returns a copy of the builtin→capability table — the
// HOST vocabulary, as opposed to internal/caps' package-authority one.
// internal/effects projects a call graph through either.
func GatedBuiltins() map[string]string {
	out := make(map[string]string, len(gatedBuiltins))
	for k, v := range gatedBuiltins {
		out[k] = v
	}
	return out
}
