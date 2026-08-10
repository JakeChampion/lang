# Design: a generative WASM component builder for the self-host

## Why

The self-hosted wasm backend (`examples/self_host/wasm.fern`) emits a
preview2-native **core module** for whatever WASI builtins a program uses
(stdout, `read_file`/`write_file`, `random_bytes`/`random_i32`, `env`,
`args`, the wall/monotonic clocks, `eprint`, `exit`). To turn that core into
a runnable `wasi:cli/run@0.2.0` **component**, `examples/self_host/wat_component.fern`
wraps it in the Component-Model envelope.

For the no-I/O shape that envelope is already **generative**
(`component_full` builds all six trailing sections with `wcat`/`wsection`/
`wname`). Every I/O shape, however, is a pair of constant `\xNN` byte blobs
spliced around the core (`io_prefix`/`io_suffix`, `io_fs_prefix`/…, one pair
per import set). As of this writing there are ~14 such pairs — one per
program shape we wanted to support (stdout, fs-read, fs-write, fs-rw,
random, env, args, wall-clock, monotonic-clock, eprint, exit, plus the
combinations read+env, read+write+env, random+write, args+read).

That blob-per-shape approach has reached its limit:

- **It does not generalise.** Every new import-set combination needs its own
  captured blob; the combinations are combinatorial.
- **The blobs are opaque.** ~80 KB of `\xNN` with no structure a reader can
  follow or a tool can check.
- **Capture depends on the native compiler.** Each blob is extracted from
  `fern -target wasm32-wasi` output for a representative program; a shape the
  native backend does not emit (e.g. an exotic combination) cannot be
  captured at all.

This document specifies a **generative component builder**:
`component_build(core, imports)` that computes the envelope from the core's
actual import set, mirroring `internal/wasm/component/component.go`. It
collapses the blob set and unlocks arbitrary combinations.

## What we learned reverse-engineering the blobs

### Component section layout

A component is `\0asm` + `0d 00 01 00` (version 13, layer 1), then sections
keyed by **component** section ids (distinct from core ids):

| id | section            | role                                             |
|----|--------------------|--------------------------------------------------|
| 1  | core-module        | an embedded core module (the user core + shims)  |
| 2  | core-instance      | instantiate a core module (optionally with args) |
| 5  | instance           | a component instance (inline exports)            |
| 6  | alias              | lift a core export / instance export into scope  |
| 7  | component-type     | WIT type definitions                             |
| 8  | canon              | `canon lift` / `canon lower`                     |
| 11 | export             | name an instance as a world export               |

The envelope for an I/O program, in order:

```
preamble
(7/6 …)   per-interface type-import section  ── the "prefix"
(1)       user core module                   ── wsection(1, core)
(1)       shim core module(s)                 ┐
(2)       core instances                      │
(6)       aliases (core + component)          ├ the "suffix"
(8)       canon lowers                        │
(8)       canon lift of _lang_run             │
(5)       component instance                  │
(11)      export "wasi:cli/run@0.2.0"         ┘
```

### Canonical import order

The native compiler emits the core's function imports in a fixed interface
order (the self-host now matches it — see `emit_module_mode`):

```
wasi:cli/stdout       get-stdout                              (or stderr-first, below)
wasi:io/streams       [method]output-stream.blocking-write-and-flush   (bwf)
wasi:cli/exit         exit                                    (right after the stdout pair)
wasi:random/random    get-random-u64
wasi:clocks/wall-clock        now
wasi:clocks/monotonic-clock   now
wasi:cli/environment  get-arguments
wasi:cli/environment  get-environment
wasi:filesystem/preopens  get-directories
wasi:filesystem/types     [method]descriptor.open-at
wasi:filesystem/types     [method]descriptor.read-via-stream
wasi:io/streams           [method]input-stream.blocking-read
wasi:filesystem/types     [method]descriptor.write-via-stream
```

Exceptions established by the eprint work: when `eprint` is used,
`wasi:cli/stderr get-stderr` is imported **before** `get-stdout`
(`get-stderr, bwf, get-stdout`). `io/error` is always the first *type*
import (it is a dependency of `io/streams`); `io/streams`'s `input-stream` /
`output-stream` resources and the `stream-error` variant are defined once
and reused.

### The type-import section (the "prefix")

Each WASI interface contributes a constant block of component-type +
instance-import + alias bytes. Crucially, **in canonical order the type and
instance indices are positional**, so a given interface's block is
byte-identical across every prefix that includes the same set of *preceding*
interfaces. Measured: all 15 current prefixes share a 62-byte common head =
preamble (8) + the `io/error` type-import block (54). Beyond that they
diverge as soon as the second interface differs.

Implication: the prefix is `preamble ++ concat(block(i) for i in
interfaces-used, in canonical order)`, where each `block(i)` is a constant
**parameterised only by the type/instance indices assigned so far**. Because
canonical order fixes those indices, the blocks can be stored as constants
keyed by "interfaces present before me" — or, more cleanly, emitted by a
function that threads a running index counter (the
`component.go`/`component_full` style).

### The table-trampoline (the "suffix")

This is the part that genuinely must be *computed*, and the reason naive
blob concatenation fails. Lowering an import that touches linear memory
(anything taking/returning a `list`, e.g. `blocking-write-and-flush`,
`get-environment`, `get-arguments`, `read-via-stream`, `blocking-read`)
needs the **user core's** `memory` and `cabi_realloc` — but the user core
needs those lowered imports to instantiate. wit-component breaks the cycle
with an indirection table:

- A **shim core module** exports a `$imports` `funcref` table and, for each
  memory-dependent import, a trampoline `func` that `call_indirect`s through
  a fixed table slot:

  ```wat
  (core module
    (type (func (param i32 i32 i32 i32)))
    (table 1 1 funcref)
    (export "0" (func 0))
    (export "$imports" (table 0))
    (func (param i32 i32 i32 i32)
      local.get 0 local.get 1 local.get 2 local.get 3
      i32.const 0 call_indirect (type 0)))
  ```

- The user core is instantiated with the shim's trampolines as its imports
  (so it can come up before the real lowered funcs exist).
- Memory-free imports (`get-stdout`, `get-stderr`, `get-random-u64`,
  wall/monotonic `now`, `exit`) are `canon lower`ed directly (no table slot,
  no memory option).
- Memory-dependent imports are `canon lower`ed with `(memory 0) (realloc N)`
  pulled from the user core's exports, then written into the shim's
  `$imports` table via a second core-instance.

The suffix's exact bytes therefore depend on: the **count** of imports, the
**partition** into memory-free vs memory-dependent, the **func/instance/
table indices** (which shift with the import count), and the interface
**names** that reappear in the alias/canon sections. This is why same-"kind"
shapes still differ (e.g. eprint's suffix ≠ stdout's: eprint pulls in
stderr *and* stdout = an extra interface).

## Proposed shape

```
// In wat_component.fern (or a new wat_component_gen.fern):

struct WasiImport {
    module: string,    // "wasi:io/streams@0.2.0"
    name: string,      // "[method]output-stream.blocking-write-and-flush"
    needs_memory: boolean,
    needs_realloc: boolean,
}

// Returns the full component bytes for `core` given the imports it declares,
// in canonical order. Mirrors internal/wasm/component/component.go.
pub function component_build(core: i32[], imports: WasiImport[]): i32[]
```

The self-host already knows the import set (it just emitted those imports in
`emit_module_mode`); thread the same list into `component_build` instead of
selecting a per-shape `component_full_io_*`.

### Building blocks

1. **`interface_typeblock(iface, idx_state)`** — emit one interface's
   component-type + instance-import + aliases, advancing `idx_state` (the
   running type/instance index counters). One per WASI interface we support;
   the constant WIT-type bytes can be lifted verbatim from today's prefixes.
2. **`shim_core(mem_imports)`** — emit the trampoline core module(s) for the
   memory-dependent imports (the `$imports` table + one `call_indirect` func
   per import, typed by the import's lowered signature).
3. **`wire(imports)`** — emit the core-instances, aliases, `canon lower`s
   (direct for mem-free, table-backed for mem-dependent), the
   `canon lift` of `_lang_run`, the component instance, and the
   `wasi:cli/run` export — computing every index from the import list.

### Incremental, byte-identical validation plan

Each phase lands as its own PR and is gated by reproducing an **existing
blob shape byte-for-byte** (we already have `bytesEqual`-style tests that
compare the self-host component to the native reference):

1. **Phase 1 — stdout.** `component_build(core, [get-stdout, bwf])` must
   equal `component_full_io(core)` byte-for-byte for the stdout core. Proves
   the type-block + single-mem-import trampoline + wiring machinery.
2. **Phase 2 — mem-free extras.** Add `random`, the clocks, `exit`
   (direct-lowered, no table slot): reproduce `component_full_io_random` /
   `_clock` / `_clock_mono` / `_exit`.
3. **Phase 3 — list imports.** Add `env`, `args` (a single mem-dependent
   import returning a list + `cabi_realloc`): reproduce
   `component_full_io_env` / `_args`.
4. **Phase 4 — filesystem.** Add the open-at / read-via / blocking-read /
   write-via chain (multiple mem-dependent imports): reproduce
   `component_full_io_fs` / `_fs_write` / `_fs_rw`.
5. **Phase 5 — stderr ordering.** Add `eprint` (stderr-before-stdout):
   reproduce `component_full_io_eprint`.
6. **Phase 6 — combinations.** With every interface generative, the
   combination shapes (`read+env`, `read+write+env`, `random+write`,
   `args+read`, …) fall out for free; reproduce each existing combination
   blob, then **delete the `io_*_prefix`/`io_*_suffix` blobs** and route all
   `component_full_io_*` callers through `component_build`.

When a phase reproduces its blobs byte-for-byte, the blobs are provably
redundant and can be removed in that same phase. The end state: one
`component_build`, no per-shape blobs, and arbitrary import-set
combinations (anything the core can import) wrapped correctly — including
shapes the native backend never emitted.

> **Status: this plan is complete for the suffix side.** Rather than one
> `component_build`, the generator landed as the data-driven
> `component_suffix` (suffix) alongside the already-composed prefix; the
> phase ordering above differs slightly from how it shipped (shim cores
> first, then the engine, then all shapes batched), but every blob it
> targeted is gone. See "Suffix builder — COMPLETE" below.

## Implementation status

The **prefix decomposition is complete** (PRs #2079, #2080, #2083). Every
component prefix is now assembled from shared per-interface type blocks —
all 14 `io_*_prefix` blobs are gone:

- `tb_io_error` (universal first block), `tb_io_streams_out` /
  `tb_io_streams_inout`, `tb_cli_stdout` / `tb_cli_stderr`, the interface
  tails (`tb_random`, `tb_cli_environment_env` / `_args`, `tb_wall_clock`,
  `tb_monotonic_clock`, `tb_cli_exit`), and the fs tails (`fs_read_tail` /
  `fs_rw_tail` / `fs_write_tail`).
- `io_stdout_head()` / `io_fs_read_head()` / `io_fs_rw_head()` /
  `io_fs_write_head()` compose them; combination prefixes append one more
  interface tail.

The decomposition worked because, in canonical order, leading blocks have
fixed indices (so they're byte-identical across shapes) and combination
prefixes are exactly `base ++ tail`.

**The suffixes do NOT decompose this way.** Measured: the universal common
head across all 15 `io_*_suffix` blobs is **1 byte** — the fs-family and
stdout-family have different *leading* shim cores (their import signatures
differ, e.g. the `i64`-offset read/write trampolines vs the stdout
`(i32 i32 i32 i32)` one), and even within a family combination suffixes
share only a partial head (~120–700 bytes) before the per-import wiring
interleaves with the lift/export tail. So suffix dedup has **no
composition shortcut**: it requires the computed table-trampoline wiring
described above (the `shim_core` + `wire` building blocks).

**This is now done.** The generative suffix builder shipped over PRs
#2093 / #2095 / #2098 / #2099 / #2102 — see the status section above. Every
`io_*_suffix` blob is gone; the suffix is computed from the import list.

## Suffix builder — COMPLETE

The deferred suffix phase is finished. `component_suffix` (in
`wat_component.fern`) is a Fern port of the native composer's
`gComposer.lower()` Phases B–H + `finish()`'s `cli/run` tail
(`internal/wasm/component/compose_general.go` + the `Put*` encoders in
`component.go`). It takes the program's preview-2 import list (parallel
arrays: interface / name / kind / instance index / trampoline signature /
resource type) plus the index state Phase A + the user core module leave
behind (`n_type0`, `n_inst0`), and regenerates the whole suffix:

- **shim cores** (`shim_trampoline_core` / `shim_tablefill_core`) — the
  signature-parameterized table-trampoline pair per memory import (#2093).
- **`comp_*` encoders** — Fern ports of the `Put*` section emitters.
- **`component_suffix`** — Phases B–H (trampoline instantiate, no-opt /
  resource.drop / mem lowers grouped by interface, user-module instantiate,
  memory/realloc/table aliases, mem lowers, table fixups) + the lift/export
  tail (#2098).

Per-shape drivers (`component_suffix_stdout` / `_eprint` / `_exit` /
`_fs_read` / `_random` / `_env` / `_args` / `_clock` / `_clock_mono` /
`_random_write` / `_fs_write` / `_fs_rw` / `_fs_read_env` / `_fs_rw_env` /
`_fs_args_read` / `_fs_rw_args`) are a few lines each — just the import
list + index state. Every shape reproduces its old blob byte-for-byte, and
each `component_full_io_*` wrapper's whole-component byte-identity to
`fern -target wasm32-wasi` is gated by its existing `TestSelfHostWasmComponentFullIO*`
e2e (self-host compile → byte-compare → wasmtime run).

**Remaining frontier:** the `gDrop` (resource-handle drop) path in
`component_suffix` is implemented but unexercised in the self-host, because
the TCP / UDP / HTTP component shapes (which drop handles) aren't emitted by
the self-host backend yet. When they land they are thin drivers over this
same engine — no new suffix machinery.

## Risks / notes

- **Index arithmetic is the bug-prone surface.** The byte-identical gate
  against the native reference is the safety net; keep it for every phase.
- **`cabi_realloc` / `memory` aliasing** must reference the user core's
  exports (`alias core export <core-idx> "memory"` / `"cabi_realloc"`); the
  user core already exports both when it uses list imports (see
  `cabi_realloc_helper`).
- This mirrors `internal/wasm/component/component.go` (~2700 LOC of Go).
  The Fern port is large but each phase is small and independently
  validated, so it need not land in one change.
- The component-suffix self-test (`TestSelfHostWasmComponentSuffixStdout`)
  embeds expected bytes for the three small CLI shapes only; larger shapes
  (fs_read and up) would OOM the self-host compile, so their byte-identity
  is covered by the whole-component `FullIO*` compares instead.
