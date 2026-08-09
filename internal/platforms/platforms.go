// Package platforms hosts the per-target descriptor table that
// declares which capabilities, handler kinds, and bindings each
// compilation target supports.
//
// Background: PR #840 introduced the Platform parameter
// (`function handle(req, plat): HttpResponse`) but every target
// currently shares a one-field placeholder Platform struct. Per
// docs/PLATFORM-RESEARCH.md Rec §2, the long-term shape is a
// per-target descriptor that drives:
//
//   - Capability surface (fetch, kv, secrets, log, now, …) —
//     determines what fields `Platform` gets per target.
//   - Handler kinds (fetch, scheduled, alarm, startup, …) —
//     determines what signatures the user can declare beyond
//     `handle(req, plat)`.
//   - Bindings consumed (kv namespaces, service bindings,
//     config dictionaries) — declares the per-deployment
//     wiring the host needs to satisfy.
//
// This package is Phase 1 of that design: descriptors are
// declared as Go literals here (no file-based loader yet),
// expose a typed surface the compiler can query, and ship one
// entry per currently-supported target. Phase 2 will use the
// descriptors to drive the auto-injected Platform struct shape
// per target; Phase 3 will accept user-defined platforms via
// `internal/platforms/<custom>/platform.fern` files.
package platforms

import (
	"fmt"
	"sort"
)

// Descriptor describes a compilation target's runtime surface.
// One Descriptor per supported `-target=...` value.
type Descriptor struct {
	// Name is the canonical -target= flag value (e.g. "wasi-http",
	// "arm64", "x86-64"). Lookups in `ForTarget` match this
	// case-sensitively.
	Name string

	// Description is a one-line human-readable summary of the
	// target. Surfaces in `fern -targets` listing output and in
	// future LSP completions for the -target flag.
	Description string

	// Capabilities lists the host capabilities the Platform
	// struct exposes on this target. Phase 1 just declares
	// names; Phase 2 will pair each name with a function
	// signature and a target-specific glue implementation.
	Capabilities []string

	// HandlerKinds lists the entry-point shapes the user can
	// declare. The first entry is canonical (auto-`main`
	// synthesis targets it); subsequent entries are
	// alternative handler signatures the target accepts.
	HandlerKinds []string

	// Bindings lists the per-deployment configuration the host
	// supplies — kv namespaces, service bindings, config maps,
	// etc. Empty for targets that don't take per-deploy config
	// (e.g. native arm64, x86-64). Today purely declarative;
	// Phase 3 wires it to runtime binding-fetch glue.
	Bindings []string

	// NoBackend marks a target that is DECLARED and type-checkable
	// but that no codegen backend emits yet, so `fern -check
	// -target NAME` works and compiling is a clear refusal rather
	// than a fall-through to "unknown target". The freestanding
	// target lands this way on purpose (#6509): the constraint is
	// worth enforcing before there is anything to enforce it on,
	// so the codegen that follows (#6510, #6511) is written
	// against a compiler that already rejects what it may not do.
	NoBackend bool
}

// environments carry the per-HOST half of a target: what the platform
// underneath provides. Capabilities describe what the host grants, and
// the ISA has nothing to say about whether there is a filesystem — so
// the set lives here and each target names an environment rather than
// repeating a list.
//
// That repetition is what this replaces: the four native targets
// carried a byte-identical eleven-element list, and adding `args` and
// `random` (#6516) meant editing the same line four times. #6529
// carries this the rest of the way, splitting the target NAME into
// <isa>-<environment> so the two axes are spelled separately too.
type environment struct {
	capabilities []string
	handlerKinds []string
	bindings     []string
}

var environments = map[string]environment{
	// The full compiled runtime surface: fs / tcp / stdin are raw
	// syscalls. NOTE `subprocess` is deliberately absent — it is an
	// INTERP-ONLY builtin today (internal/interp only; no codegen
	// backend lowers it), so no compiled target grants it and E066
	// rejects it up front instead of the old "undefined label"
	// assembler failure.
	//
	// `proc` (fork/waitpid supervision — docs/CRASH-ONLY-SERVE.md D2')
	// is native-only: wasm worlds have no processes.
	"native": {
		capabilities: []string{"log", "now", "env", "args", "random", "stdin", "stdout", "fs", "tcp", "proc", "arena"},
		handlerKinds: []string{"handle"},
	},

	// CLI-world wasm wires fs (the preview1 fd helpers) and tcp
	// (wasi:sockets — wasmbin/wasi_tcp.go) but NOT subprocess:
	// wasi:cli/exec-process isn't in the runtime helpers (the standing
	// gap wasmbin's TestBuildReportsUnsupported pins). Programs
	// reaching `subprocess` here are rejected at check time (E066)
	// instead of failing mid-build.
	"wasi": {
		capabilities: []string{"log", "now", "env", "args", "random", "stdin", "stdout", "fs", "tcp"},
		handlerKinds: []string{"main"},
	},

	// The proxy world: an HTTP handler and nothing else. No argv, no
	// stdout stream, no filesystem — which is what gives `args` and
	// `stdout` their teeth as capabilities distinct from `env` and
	// `log` (#6513, #6516). `fetch` is the planned outbound capability
	// (docs/STDLIB-DESIGN-RESEARCH.md Rec §10); `kv` and `secrets`
	// follow once the host-binding shape is finalised.
	"wasi-http": {
		capabilities: []string{"log", "now", "env", "random", "fetch"},
		handlerKinds: []string{"handle"},
		// `wasmtime serve --env KEY=VAL` style bindings; future hosts
		// may add kv-namespace + service-binding shapes.
		bindings: []string{"env"},
	},

	// No host at all. Everything a program can still reach is
	// platforms.coreBuiltins; docs/FREESTANDING-CORE.md has the rule
	// and every judgement call. No entry point either: a freestanding
	// artifact is either a guest (exported symbols its embedder calls)
	// or the host (entered at a reset vector), so #6510 needs to leave
	// room for both — docs/BARE-METAL-PLAN.md.
	"freestanding": {},
}

// table is the per-target descriptor registry: the ISA-and-object-format
// half, plus which environment the target runs in. Keys match the
// `-target` flag values cmd/fern accepts.
var table = map[string]struct {
	description string
	environment string
	noBackend   bool
}{
	"arm64": {
		description: "ARM64 Linux ELF (default — Graviton, Apple Silicon via container, Raspberry Pi 4+ 64-bit, Android).",
		environment: "native",
	},
	"arm64-darwin": {
		description: "ARM64 macOS Mach-O (native Apple Silicon Macs; no Linux container needed).",
		environment: "native",
	},
	"arm64-android": {
		description: "ARM64 Android — Linux ELF as a static position-independent " +
			"executable (ET_DYN, W^X), so it loads at an arbitrary base under " +
			"Android's loader. Same syscalls / AAPCS64 as the arm64 target.",
		environment: "native",
	},
	"x86-64": {
		description: "x86-64 Linux ELF (native exec on x86_64 hosts, qemu-x86_64 elsewhere).",
		environment: "native",
	},
	"wasm": {
		description: "WebAssembly Component Model — CLI world (wasi:cli/run + wasi:filesystem + wasi:clocks).",
		environment: "wasi",
	},
	"wasi-http": {
		description: "WebAssembly Component Model — proxy world (wasi:http/incoming-handler).",
		environment: "wasi-http",
	},
	"freestanding": {
		description: "No host — no kernel, no syscalls, no process. Type-checkable " +
			"today; no backend emits for it yet (#6506).",
		environment: "freestanding",
		noBackend:   true,
	},
}

// ForTarget returns the descriptor for the given target name.
// Unknown targets return a nil descriptor; callers should treat
// that as a hard error (the target list should be exhaustive).
// Mirrors `cmd/fern`'s -target flag-value set.
func ForTarget(name string) *Descriptor {
	t, ok := table[name]
	if !ok {
		return nil
	}
	env, ok := environments[t.environment]
	if !ok {
		// A target naming an environment that doesn't exist is a
		// programming error in this file, not a user-facing one.
		panic("platforms: target " + name + " names unknown environment " + t.environment)
	}
	return &Descriptor{
		Name:         name,
		Description:  t.description,
		Capabilities: env.capabilities,
		HandlerKinds: env.handlerKinds,
		Bindings:     env.bindings,
		NoBackend:    t.noBackend,
	}
}

// Targets returns the canonical -target= names in a stable
// order. Used by `fern -targets`-style listings and by tests
// that walk every supported target.
func Targets() []string {
	out := make([]string, 0, len(table))
	for name := range table {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// HasCapability reports whether the target's Platform exposes
// the named capability. Returns false for unknown targets;
// callers should `ForTarget` first if they want to distinguish
// "unknown target" from "target exists but no such capability."
func HasCapability(target, capability string) bool {
	d := ForTarget(target)
	if d == nil {
		return false
	}
	for _, c := range d.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// String renders the descriptor as a one-line listing entry —
// `name: description`. Used by `fern -targets` output.
func (d *Descriptor) String() string {
	return fmt.Sprintf("%s: %s", d.Name, d.Description)
}
