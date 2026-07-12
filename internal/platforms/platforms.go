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
}

// table is the per-target descriptor registry. Keys match the
// `-target` flag values cmd/fern accepts. Adding a new target
// is a single entry here plus the codegen-side support.
var table = map[string]Descriptor{
	"arm64": {
		Name:        "arm64",
		Description: "ARM64 Linux ELF (default — Graviton, Apple Silicon via container, Raspberry Pi 4+ 64-bit, Android).",
		// Native targets expose the full compiled runtime
		// surface: fs / tcp / stdin are raw syscalls. NOTE
		// `subprocess` is deliberately absent — it is an
		// INTERP-ONLY builtin today (internal/interp only; no
		// codegen backend lowers it), so no compiled target
		// grants it and E066 rejects it up front instead of the
		// old "undefined label" assembler failure.
		Capabilities: []string{"log", "now", "env", "stdin", "stdout", "fs", "tcp"},
		HandlerKinds: []string{"handle"},
		Bindings:     nil,
	},
	"arm64-darwin": {
		Name:         "arm64-darwin",
		Description:  "ARM64 macOS Mach-O (native Apple Silicon Macs; no Linux container needed).",
		Capabilities: []string{"log", "now", "env", "stdin", "stdout", "fs", "tcp"},
		HandlerKinds: []string{"handle"},
		Bindings:     nil,
	},
	"arm64-android": {
		Name: "arm64-android",
		Description: "ARM64 Android — Linux ELF as a static position-independent " +
			"executable (ET_DYN, W^X), so it loads at an arbitrary base under " +
			"Android's loader. Same syscalls / AAPCS64 as the arm64 target.",
		Capabilities: []string{"log", "now", "env", "stdin", "stdout", "fs", "tcp"},
		HandlerKinds: []string{"handle"},
		Bindings:     nil,
	},
	"x86-64": {
		Name:         "x86-64",
		Description:  "x86-64 Linux ELF (native exec on x86_64 hosts, qemu-x86_64 elsewhere).",
		Capabilities: []string{"log", "now", "env", "stdin", "stdout", "fs", "tcp"},
		HandlerKinds: []string{"handle"},
		Bindings:     nil,
	},
	"wasm": {
		Name:        "wasm",
		Description: "WebAssembly Component Model — CLI world (wasi:cli/run + wasi:filesystem + wasi:clocks).",
		// CLI-world wasm wires fs (the preview1 fd helpers) and
		// tcp (wasi:sockets — wasmbin/wasi_tcp.go) but NOT
		// subprocess: wasi:cli/exec-process isn't in the runtime
		// helpers (the standing gap wasmbin's
		// TestBuildReportsUnsupported pins). Programs reaching
		// `subprocess` here are rejected at check time (E066)
		// instead of failing mid-build.
		Capabilities: []string{"log", "now", "env", "stdin", "stdout", "fs", "tcp"},
		HandlerKinds: []string{"main"},
		Bindings:     nil,
	},
	"wasi-http": {
		Name:        "wasi-http",
		Description: "WebAssembly Component Model — proxy world (wasi:http/incoming-handler).",
		// HTTP-handler-only target. `fetch` is the planned
		// outbound capability (docs/STDLIB-DESIGN-RESEARCH.md
		// Rec §10). `kv`, `secrets` follow once the host-binding
		// shape is finalised.
		Capabilities: []string{"log", "now", "env", "fetch"},
		HandlerKinds: []string{"handle"},
		// `wasmtime serve --env KEY=VAL` style bindings; future
		// hosts may add kv-namespace + service-binding shapes.
		Bindings: []string{"env"},
	},
}

// ForTarget returns the descriptor for the given target name.
// Unknown targets return a nil descriptor; callers should treat
// that as a hard error (the target list should be exhaustive).
// Mirrors `cmd/fern`'s -target flag-value set.
func ForTarget(name string) *Descriptor {
	d, ok := table[name]
	if !ok {
		return nil
	}
	return &d
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
