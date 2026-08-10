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
	// Name is the canonical -target= flag value (e.g. "wasm32-wasi-http",
	// "arm64-linux", "x86-64-linux"). Lookups in `ForTarget` match this
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

	// ISA and Environment are the two halves of the target's name. ISA
	// selects the backend; Environment selects what the host provides
	// and therefore drives Capabilities. Callers branching on "is this
	// wasm" or "is this Darwin" should read these rather than
	// string-matching Name.
	ISA         string
	Environment string

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

// A target is <isa>-<environment>. The ISA half selects the backend; the
// environment half says what the host provides. Neither is implied:
// there is no bare `arm64` meaning arm64-Linux.
//
// Capability sets live one level down again, on a PROFILE the
// environment names. linux / darwin / android are genuinely different
// environments — different object formats, different syscall vectors —
// but a host either has a filesystem or it does not, and all three grant
// exactly the same set. Naming a shared profile keeps that list written
// once without pretending the three environments are one.
type capabilityProfile = []string

var capabilityProfiles = map[string]capabilityProfile{
	// The full compiled runtime surface: fs / tcp / stdin are raw
	// syscalls. NOTE `subprocess` is deliberately absent — it is an
	// INTERP-ONLY builtin today (internal/interp only; no codegen
	// backend lowers it), so no compiled target grants it and E066
	// rejects it up front instead of the old "undefined label"
	// assembler failure.
	//
	// `proc` (fork/waitpid supervision — docs/CRASH-ONLY-SERVE.md D2')
	// is native-only: wasm worlds have no processes.
	"hosted-native": {"log", "now", "env", "args", "random", "stdin", "stdout", "fs", "tcp", "proc", "arena"},

	// CLI-world wasm wires fs (the preview1 fd helpers) and tcp
	// (wasi:sockets — wasmbin/wasi_tcp.go) but NOT subprocess:
	// wasi:cli/exec-process isn't in the runtime helpers (the standing
	// gap wasmbin's TestBuildReportsUnsupported pins).
	"wasi-cli": {"log", "now", "env", "args", "random", "stdin", "stdout", "fs", "tcp"},

	// The proxy world: an HTTP handler and nothing else. No argv, no
	// stdout stream, no filesystem — which is what gives `args` and
	// `stdout` their teeth as capabilities distinct from `env` and `log`
	// (#6513, #6516). `fetch` is the planned outbound capability
	// (docs/STDLIB-DESIGN-RESEARCH.md Rec §10).
	"wasi-proxy": {"log", "now", "env", "random", "fetch"},

	// No host at all. Everything a program can still reach is
	// platforms.coreBuiltins; docs/FREESTANDING-CORE.md has the rule and
	// every judgement call.
	"none": nil,
}

type environment struct {
	profile      string
	handlerKinds []string
	bindings     []string
}

var environments = map[string]environment{
	"linux":   {profile: "hosted-native", handlerKinds: []string{"handle"}},
	"darwin":  {profile: "hosted-native", handlerKinds: []string{"handle"}},
	"android": {profile: "hosted-native", handlerKinds: []string{"handle"}},
	"wasi":    {profile: "wasi-cli", handlerKinds: []string{"main"}},
	// `wasmtime serve --env KEY=VAL` style bindings; future hosts may add
	// kv-namespace + service-binding shapes.
	"wasi-http": {profile: "wasi-proxy", handlerKinds: []string{"handle"}, bindings: []string{"env"}},
	// No entry point declared. A freestanding artifact can be a GUEST (a
	// set of exported symbols its embedder calls) or the HOST (entered at
	// a reset vector, owning the vector table). docs/BARE-METAL-PLAN.md
	// says #6510 must not foreclose on the second, so neither shape is
	// baked in here.
	"freestanding": {profile: "none"},
}

// table is the target registry: every valid <isa>-<environment> pair.
// `emits` says whether a codegen backend exists for the pair — the
// freestanding pairs are declared and type-checkable before anything
// emits for them (#6509), so compiling one is a clear refusal rather
// than a fall-through to "unknown target". It is stated per entry rather
// than defaulted so adding a target forces the question.
var table = map[string]struct {
	isa         string
	environment string
	description string
	emits       bool
}{
	"arm64-linux": {
		isa: "arm64", environment: "linux", emits: true,
		description: "ARM64 Linux ELF (Graviton, Raspberry Pi 4+ 64-bit, Apple Silicon via container).",
	},
	"arm64-darwin": {
		isa: "arm64", environment: "darwin", emits: true,
		description: "ARM64 macOS Mach-O (native Apple Silicon Macs; no Linux container needed).",
	},
	"arm64-android": {
		isa: "arm64", environment: "android", emits: true,
		description: "ARM64 Android — Linux ELF as a static position-independent " +
			"executable (ET_DYN, W^X), so it loads at an arbitrary base under " +
			"Android's loader. Same syscalls / AAPCS64 as arm64-linux.",
	},
	"arm64-freestanding": {
		isa: "arm64", environment: "freestanding",
		description: "ARM64 with no host — no kernel, no syscalls, no process. " +
			"Type-checkable today; no backend emits for it yet (#6510).",
	},
	"x86-64-linux": {
		isa: "x86-64", environment: "linux", emits: true,
		description: "x86-64 Linux ELF (native exec on x86_64 hosts, qemu-x86_64 elsewhere).",
	},
	"x86-64-freestanding": {
		isa: "x86-64", environment: "freestanding",
		description: "x86-64 with no host — no kernel, no syscalls, no process. " +
			"Type-checkable today; no backend emits for it yet (#6510).",
	},
	"wasm32-wasi": {
		isa: "wasm32", environment: "wasi", emits: true,
		description: "WebAssembly Component Model — CLI world (wasi:cli/run + wasi:filesystem + wasi:clocks).",
	},
	"wasm32-wasi-http": {
		isa: "wasm32", environment: "wasi-http", emits: true,
		description: "WebAssembly Component Model — proxy world (wasi:http/incoming-handler).",
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
		panic("platforms: target " + name + " names unknown environment " + t.environment)
	}
	caps, ok := capabilityProfiles[env.profile]
	if !ok {
		panic("platforms: environment " + t.environment + " names unknown profile " + env.profile)
	}
	return &Descriptor{
		Name:         name,
		Description:  t.description,
		ISA:          t.isa,
		Environment:  t.environment,
		Capabilities: caps,
		HandlerKinds: env.handlerKinds,
		Bindings:     env.bindings,
		NoBackend:    !t.emits,
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
