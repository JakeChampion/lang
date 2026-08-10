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

`-target arm64-freestanding` / `-target x86-64-freestanding` exist and grant nothing (#6509). It is **check-only**: the
descriptor is declared, `fern -targets` lists it, and `fern -check -target arm64-freestanding`
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
  for freestanding the invariant applies again.

## The entry shape

`HandlerKinds` is empty because a freestanding artifact is not entered at `main`. What
replaces it is `Descriptor.Entry`, an `EntryShape` on the environment (#6510):

| shape | who transfers control | native backends emit |
| --- | --- | --- |
| `EntryProcess` | a kernel / dyld, with a populated process stack | `_start` (`_main` on Mach-O) + the argc/argv/envp capture |
| `EntryExports` | nobody; the embedder calls exported symbols | neither |
| `EntryReset` | a reset vector, with no stack pointer | not emitted for yet |

Freestanding is `EntryExports` today. **`EntryReset` exists so the kernel posture is a
second value rather than a rewrite** of everything keyed on "freestanding means exported
symbols" — Fern targets both postures (`docs/BARE-METAL-PLAN.md`). The zero value
resolves to `EntryProcess` via `OrDefault`, so a codegen `Options{}` keeps the hosted
behaviour.

### Where a hostless panic goes: nowhere — it traps

Capability gating removes most of the hosted runtime for free: a freestanding program
*cannot call* `read_file` / `tcp_*` / `print` / the clocks, so their `uses*` flags never
set and their syscalls are never emitted. Three pieces do not fall out that way, because
nothing in the program has to name them:

- **`__fern_report`**, the abort reporter, emitted for every program — it writes the
  cause to stderr and `exit_group`s.
- **`__fern_exit`**, because `exit` is core (above) and so must exist everywhere.
- **`__fern_alloc`'s lazy `mmap`**, because essentially every program allocates.

On an entry shape other than `EntryProcess` all three become a trap — `ud2` on x86-64,
`brk #1` on arm64 — rather than a syscall. That is the answer to *where does a
freestanding artifact put a panic message?*: it does not have one, and stopping is the
only honest thing left. The symbols stay defined (call sites branch to them); only the
bodies change, and the backtrace string is dropped since nothing writes it.

### The heap is handed in, not acquired

A hostless target cannot `mmap` a region, and with no MMU may have no address space to
reserve one in. So the heap becomes the embedder's (#6511): off the process path the
backends export

```
__fern_heap_init(base, len)     // rdi/rsi on System V, x0/x1 on AAPCS64
```

which seeds `__fern_heap_ptr` (cursor), `__fern_heap_base` (high-water reference) and
`__fern_heap_end` (`base + len`). **Only the seeding moves.** The 16-byte rounding, the
RC header and the two-tier segregated freelist all work against a region regardless of
where it came from — `docs/ARENA-DECISION.md` is why there is exactly one cursor pair to
reseed. Hosted targets keep the lazy `mmap` and do *not* get this symbol; one allocator
with two ways to seed its cursor is two sources of truth.

The contract is that the embedder calls it once before anything that allocates.

**Two defined failure modes, both a trap:**

- **Allocating before `__fern_heap_init`.** `__fern_heap_ptr` is still zero, which is the
  unseeded sentinel, so `__fern_alloc` stops instead of bumping a null cursor. (A region
  based at address 0 is therefore not expressible — not a real heap on any target.)
- **Exhausting the region.** The existing `.Lalloc_oom` bounds check already routes to
  `__fern_report`, which off the process path is the trap above. So exhaustion is the
  same "stop" as every other hostless abort rather than an exit code the embedder cannot
  see anyway.

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
