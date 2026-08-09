# The freestanding core — where the OS boundary runs

Epic: #6506. This doc records **which builtins need a host and which do not**, why
each judgement call went the way it did, and the standing rule for classifying a new
one. The classification lives in `internal/platforms/enforce.go` as two tables —
`gatedBuiltins` and `coreBuiltins` — and a completeness test fails when a builtin is in
neither.

## Why there is a boundary at all

Every target Fern ships today is hosted: raw `svc` / `syscall` traps, a `_start` that
reads argc/argv/envp off a Linux process stack, and a heap the runtime `mmap`s for
itself. A target without a kernel can satisfy none of that, and reach beyond hosted
Linux/Darwin/WASI is the point of the epic.

`internal/platforms` already had the enforcement machinery — per-target
`Descriptor.Capabilities`, checked post-tree-shake as E066, on the principle (from Roc,
via `docs/NICHE-LANGUAGE-RESEARCH.md`) that *what a target doesn't provide should not be
expressible in a program compiled for it*. It had simply never been pointed at the OS.

## The rule

> **A builtin is core if a correct implementation needs nothing but the CPU and the
> program's own memory. Everything else is gated.**

Two clarifications that decide most cases:

- **An allocator is not a host.** `map_new` needs memory, not a kernel. Whoever seeds
  the heap region decides where the bytes come from — see #6511, where the 512 MiB
  `mmap` reservation becomes an embedder-supplied region. Allocation is core.
- **Authority lives on the constructor, not the consumer.** `poll` waits on a pollable
  someone else built. Gating both the constructor and the wait double-counts one
  authority and produces two diagnostics for one cause.

When a new builtin is genuinely ambiguous, gate it. A capability that turns out to be
universal costs one line per descriptor; a host dependency that slips into the core
costs a silent failure on the first target that lacks it.

## The classification

### Gated

| capability | builtins | why |
| --- | --- | --- |
| `log` | `print`, `eprint` | somewhere to put a line of diagnostics |
| `stdout` | `stdout`, `stderr`, `write`, `putchar` | an actual stdout stream |
| `stdin` | `stdin`, `read_line` | a blocking input stream |
| `now` | `now_unix_ms`, `now_ns`, `monotonic_ns`, `sleep_ms`, `timer_fd`, `wasm_timer_pollable` | a clock, and wakeups driven by one |
| `env` | `env` | envp, captured at process start |
| `args` | `args` | argv, which exists only because something exec'd you |
| `random` | `random_bytes`, `random_i32` | entropy: a syscall or a host import, never computed |
| `fs` | `read_file`, `write_file`, `open_reader`, … | a filesystem |
| `tcp` | `tcp_*`, `udp_send` | a network stack |
| `proc` | `proc_fork`, `proc_exec`, `proc_waitpid` | processes |
| `subprocess` | `subprocess` | interp-only; no compiled target provides it |
| `arena` | `__heap_mark`, `__heap_release_to` | native-only cursor rewind |

### Core

- **Allocation** — `map_new`, `cell_new`, `string_from_bytes_unchecked`,
  `strbuf_reset` / `strbuf_append` / `strbuf_take`.
- **Pure computation** — `f32_bits`, `f32_from_bits`, `f64_bits`, `f64_from_bits`, and
  the whole math / string / array runtime that was never in either table.
- **Readiness** — `poll`, `wasm_block`, `wasm_poll`, `wasm_pollable_drop`.
- **`exit`** — see below.

## The judgement calls

**`print` vs `write`.** These are the same operation modulo a newline and they land on
opposite sides of the line, which looks wrong until you look at wasi-http: the proxy
world grants `log` and has no `wasi:cli/stdout`. `log` means *a place to put
diagnostics*, which a proxy-world host can satisfy with a logger; `stdout` means *the
stdout stream*, which it cannot. `write` names the stream. If a target ever wants
unterminated diagnostics, the answer is a `log`-side builtin, not moving `write`.

**`args` is not `env`.** argv and envp are adjacent on the process stack and `_start`
captures them together, so folding them into one capability is tempting. wasi-http is
the counterexample again: it has environment variables and no argv. Two capabilities.

**`exit` is core.** It is host-shaped — a hosted process exits through the kernel — and
the case for gating it is real. It is core anyway because *every* target can define
"stop": a freestanding artifact traps, resets, or returns to its embedder. Gating it
would mean a program that cannot terminate, which is not a useful thing to be able to
express. This is the one entry in `coreBuiltins` that is a decision rather than an
observation, which is why it is called out in the table's comment as well as here.

**Allocation is core, but it is not free.** `map_new` compiles the same everywhere; what
differs is where the heap came from. That difference is #6511's problem, not the
classification's. Keeping it out of the capability vocabulary is deliberate — otherwise
every target would grant `alloc` and the capability would carry no information.

## The target

`-target freestanding` exists and grants nothing (#6509). It is **check-only**: the
descriptor is declared, `fern -targets` lists it, and `fern -check -target freestanding`
type-checks against its empty capability set — but no backend emits for it, and asking
one to is a refusal naming the check path rather than the "unknown target" error, which
would be false.

That ordering is deliberate. The constraint becomes real before there is codegen to
constrain, so the backend work that follows (#6510, #6511) is written against a compiler
that already rejects what it may not do.

Two consequences worth knowing:

- **`-check` only enforces when you pass `-target` explicitly.** The flag defaults to
  `arm64`, so threading it unconditionally would silently start enforcing that
  capability set against every `fern -check` — including `subprocess`, which no compiled
  target grants. A bare `fern -check` still means "does this type-check".
- **`log` is no longer universal.** `TestNoTargetMissesLogCapability` exempts targets
  marked `NoBackend`. Keyed on the flag rather than the name, so the day something emits
  for freestanding the invariant applies again — at which point *where does a
  freestanding artifact put a panic message?* becomes a question with an answer instead
  of an omission.

The entry point is deliberately unspecified: `HandlerKinds` is empty, because a
freestanding artifact is a set of exported symbols its embedder calls rather than
something entered at `main`. That is closer to the existing `-shared` / `-export` path
than to any handler kind, and #6510 settles it — declaring a shape now would be
inventing one.

## Adding a builtin

Classify it in `internal/platforms/enforce.go`: a capability in `gatedBuiltins`, or
`true` in `coreBuiltins`. `TestClassificationCoversCheckerRegistry` fails if you do
neither, and fails if you do both.

If it needs a **new** capability, add the name to every descriptor that provides it in
`internal/platforms/platforms.go`. `TestGatedCapabilitiesResolvable` fails if a gate
names a capability no descriptor grants, which is the typo that would otherwise make a
builtin unreachable everywhere.

Note the **second, independent** capability system: `internal/caps` governs what a
*package* may reach (`net`, `fs`, `env`, `random`, `subprocess`, `time`) for dependency
grants, documented in `docs/PACKAGE-CAPABILITIES-BRIEF.md`. It has its own completeness
tests and its own vocabulary — `print` is ungated there on purpose, because stdio is a
channel the invoker already handed the process rather than ambient authority a
dependency escalates through. A new builtin generally needs classifying in **both**.
