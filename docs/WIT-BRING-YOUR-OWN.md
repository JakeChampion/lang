# Bring-your-own WIT — feasibility & phased plan

**Goal (as asked):** let someone hand the compiler *their own* `.wit` world
plus a Fern program and get a component that imports/exports exactly that
world — instead of the curated, hard-coded WASI set the compiler ships
today.

This doc scopes that feature: what exists, the three real gaps, the
ingestion options, and an incremental plan that stays gated by
byte-identical reproduction (the same discipline the component-suffix
generator used — see `docs/WASM-COMPONENT-GENERATOR.md`).

## Where we are today

The toolchain is **not** WIT-driven. Confirmed end to end:

- **No WIT ingestion path.** There is no `--wit` flag and no `.wit` parser
  anywhere in `internal/` or `cmd/`. The only place WIT enters the build is
  *offline*: `wasm-tools component embed -w <world>` pre-compiles the fixed
  worlds in `cmd/fern/wit/` into binary `component-type` payloads checked in
  as `internal/wasm/componenttype/{fern,http}.bin` (see that package's
  `doc.go`).
- **A closed interface registry.** `internal/wasm/component/classify.go`
  maps a core module's preview-2 imports against a hard-coded set of ~22
  WASI 0.2 interfaces; each has a hand-written binary type-body emitter
  (`Wasi*InstanceTypeBody`, the `tb_*` blocks on the self-host side).
  Unrecognised imports are a hard error
  (`cmd/fern/main.go`: *"can't wrap a core module with unrecognised imports
  yet"*).
- **No host-import language surface.** Fern has no `extern` / `import`
  declaration. Capabilities (`write`, `read_file`, `env`, `exit`, …) are
  compiler **built-ins** wired in the checker + backends; user code can't
  name an arbitrary host function.
- **Two fixed export shapes.** `wasi:cli/run@0.2.0` (lifts `main()`) and
  `wasi:http/incoming-handler@0.2.0#handle` (lifts `handle()`), selected by
  `-target`. No other world is emitted.
- **No resource/handle concept** in the type system (WIT `resource`,
  `own<T>` / `borrow<T>`). The HTTP shape sidesteps this by treating handles
  as opaque `i32`s inside compiler-internal plumbing.

What *does* exist and is reusable: a complete set of WIT-type → component-
binary encoders — `InnerTypeRecord / Variant / Enum / Flags / List / Option
/ ResultOk / ResultErr / ResultOkErr / Tuple / Borrow`, plus the
`PutTypeSection*` and the lower/lift/canon machinery the suffix generator
already drives.

## The three gaps (in increasing cost)

### Gap A — WIT world → component type/import/export wiring
*Mechanical; reuses recent work.* Given a parsed world, emit the type-import
sections, classify each import to a lowering kind (no-opt / mem / mem+realloc
/ drop — derivable from the signature: lists/strings ⇒ memory[+realloc],
handles ⇒ drop), and run the generative `component_suffix` engine. The type
bodies map onto the existing `InnerType*` encoders. **This is the part the
component-suffix work was building toward.**

### Gap B — Fern language surface for host imports
*The bulk of the feature; touches the language + type system.* For user code
to *call* WIT imports, Fern needs:
- an **`extern` import declaration** binding a Fern signature to
  `wasi:pkg/iface@x.y.z#func` (or method/constructor/static forms),
- a **WIT→Fern type mapping** for params/results (records→structs,
  variants→enums/unions, `option`/`result`→existing Option/Result, `list`→
  arrays, `tuple`→tuples, `string`→string), and crucially
- **resources/handles** — a genuinely new language concept: `own<T>` vs
  `borrow<T>`, construction, methods, and **drop semantics** (the engine's
  `gDrop` path exists but nothing in the language produces it yet).

Resources are the hard sub-problem; most non-trivial WIT worlds use them.

### Gap C — arbitrary exports
*Medium.* Mark which Fern functions implement which WIT export and lift each
with the right canonical ABI, for any world (not just `cli/run` / `handle`).
Needs an `export`-binding surface and per-export lift wiring (a
generalisation of the fixed `_lang_run` / `handle` lifts).

## WIT ingestion — three options

1. **Shell out to `wasm-tools`** (recommended first). `wasm-tools component
   embed -w <world> <empty-core> ` already produces the binary
   `component-type` payload; decode that (it *is* the component type/import/
   export definitions in binary) rather than parsing WIT text. Pros: zero
   new parser, reuses the exact encoding the compiler already emits, matches
   the existing `componenttype` precedent. Cons: build/run-time `wasm-tools`
   dependency; needs a **decoder** for the component-type binary (we have
   encoders, not decoders — this is net-new but bounded).
2. **Vendor a Go WIT parser** (e.g. bytecodealliance `wit-parser` /
   `wasm-tools-go`). Pros: native, no shell-out, full WIT AST. Cons: new
   dependency; the project is dependency-light by design.
3. **Write a WIT text parser in-tree.** Pros: no deps, full control, and it
   would eventually need a self-host port anyway. Cons: WIT is a non-trivial
   grammar (worlds, interfaces, use, gates, resources) — a real parser
   project on its own.

Recommendation: **start with (1)** to unblock Gaps A/C against real worlds
without committing to a parser, then move to (3) if/when WIT becomes a
first-class input (the self-host will eventually need an in-tree parser).

## Phased plan (each gated by byte-identical reproduction)

The existing `Wasi*InstanceTypeBody` emitters are the **oracle**: a
WIT-driven path must reproduce today's `fern` / `http` worlds byte-for-byte
before it's trusted, exactly like the suffix generator reproduced each blob.

**Self-host parity is part of every phase's definition of done — not a
deferred afterthought.** The self-hosted compiler (`examples/self_host/`) is
the destination; the Go compiler is the reference that eventually retires
(see `CLAUDE.md`). So each phase below ships in *both*: the Go side may lead
(its round-trip oracle against `fern.bin` / `http.bin` is the easiest place
to nail the encoding down first), but the phase is not complete until the
self-host port lands too, gated by the same byte-identical reproduction
against the Go reference — the exact pattern the suffix builder used (the Go
composer was the oracle; the deliverable was `wat_component.fern`). A phase
that lands Go-only is a half-phase; the self-host port follows immediately,
per phase, before moving on.

1. **P1 — decode the component-type binary.** Add a decoder for the
   `component-type` payload (option 1); round-trip
   `componenttype/fern.bin` → structured world → re-encode == original.
   Pure internal, no language changes. *De-risks ingestion.*
2. **P2 — drive the WASI set from the decoded world.** Lift the world to a
   per-interface model (done), resolve signatures (done), classify each
   import by the canonical ABI (done), then emit the type imports from the
   world. **See "P2 finding & direction" below** — the original
   byte-identical gate does *not* hold (the composer emits ~12+ minimized,
   direction-specific bodies, not full interfaces), so emission targets the
   **full** interface with a **run gate** (validates + runs under wasmtime),
   not byte-identity with the current output.
3. **P3 — accept a user `--wit` + import classification.** Allow a
   user-supplied world; classify its imports; emit type imports + the
   generative suffix. Still no new *call* surface — validate by composing a
   world whose imports the core already emits (e.g. a hand-written WASI
   subset). *Delivers the import-side of "bring your own WIT".*
4. **P4 — `extern` import declarations + non-resource types** (Gap B, part
   1). Language surface to declare + call WIT imports whose signatures use
   only records/variants/enums/flags/lists/options/results/tuples/strings.
   Checker + backend lowering; e2e against a real no-resource world.
5. **P5 — resources / handles** (Gap B, part 2). `own`/`borrow`, methods,
   constructors, drop. The largest single phase; unlocks most real worlds
   and finally exercises the engine's `gDrop` path from user code.
6. **P6 — arbitrary exports** (Gap C). `export`-binding surface + per-export
   lift; emit any world's exports, not just `cli/run` / `handle`.

P1–P3 are tractable and high-value (they make the *interface set*
pluggable). P4–P6 are the genuine language feature and are where most of the
cost lives.

## P2 finding & direction

**Status: P2 slices 1–3 are done** (Go side) — the decoder lifts the world to
a per-interface model (`World.Interfaces`), resolves each function's
signature (`FuncSigs` / `ResolveDef`), and classifies every import's lowering
kind from the canonical ABI (`Classify`: list/string params ⇒ mem,
heap-carrying results ⇒ mem+realloc, indirect/multi-flat returns ⇒ mem). The
classifier reproduces the composer's hard-coded kinds across 17 functions,
derived from the `fern` world alone.

**The byte-identical emission gate does not hold.** The composer does not emit
each interface's full type — it emits one of **~12+ minimized,
direction-specific hand-written bodies** (`WasiIoStreams{,Read,ReadWrite}
InstanceTypeBody`; nine `WasiFilesystemTypes*InstanceTypeBody`; the
`wasiCliStreamGetter` getters), each carrying only the methods/resources a
given program shape uses. The decoded world carries the **full** interface, so
"re-emit from the world, byte-identical to current output" is neither
achievable (the minimization is ad-hoc per shape) nor desirable — it would not
generalize to arbitrary user worlds, which arrive un-minimized.

**Decision: emit the full interface, gated by running, not by bytes.** P2's
emission produces each used interface's **complete** type-import from the
world (every method/resource — what any WASI 0.2 host already provides and
what user worlds need), and the gate becomes *the component validates under
`wasm-tools` and runs correctly under `wasmtime`*, not byte-identity with the
current/native output. This is the honest, generalizable shape and aligns with
the full P1–P6 goal; minimization was always a current-implementation detail
that can't survive bring-your-own-WIT.

**The native Go composer (`internal/wasm/component`) stays as-is for now** —
the WIT-driven path is built alongside it; whether/when native retires its
minimized bodies is deferred until the world-driven emission proves out.

**Emission's real work (revised P2 tail).** Transforming a world interface's
instance type into a standalone component type+import section is a
**scope/index rewrite**, not a copy: inside the world the interface's
instance type references shared types (`error`, `output-stream`, …) via
*outer aliases* into the world-component scope, and emitting them as top-level
component sections re-points those at surfaced component-level type indices
(exactly what the hand-written bodies take as an `errAlias`-style parameter,
but for the full method set). Cross-interface **type identity must be
preserved** (an `error` handle from `io/streams` is the same type as
`io/error`'s), so shared types are surfaced once and aliased — they cannot be
inlined per interface. And because the suffix wiring (`component_suffix`)
indexes off the prefix's instance/type counts, a world-driven prefix needs the
suffix regenerated for its instance layout. These are the next P2 slices, each
gated by a running component.

## P1–P3 status: done in both compilers

P1 (decode the component-type binary), P2 (drive composition from the decoded
world), and P3 (accept a user-supplied WIT) are **complete in both the Go and
the self-hosted compiler**. A user can hand either compiler a Fern program and
a `.wit` world and get a component scoped to exactly that world (validated by
`wasm-tools`, run under `wasmtime`, importing only the world's interfaces). The
import side of bring-your-own-WIT is finished. A self-host backend bug found en
route — the string-runtime helper-emission gap — was fixed too.

What remains is the **language surface** (P4–P6): letting Fern programs *call*
arbitrary WIT imports and *implement* arbitrary world exports, rather than
relying on the built-in capabilities (`write`, `read_file`, …).

## P4 design: `extern` import declarations

**Chosen syntax: an `@import` attribute on a body-less function**, reusing the
existing `@derive` attribute machinery (the `@` token + `parseDerive`-style
parse). The WIT function string carries names that aren't valid Fern
identifiers (dashes, `[method]output-stream.blocking-write-and-flush`); the
Fern name is a normal identifier the program calls.

```
@import("wasi:random/random@0.2.0", "get-random-u64")
function random_u64(): u64;

@import("wasi:io/streams@0.2.0", "[method]output-stream.blocking-write-and-flush")
function bwf(self: i32, ptr: i32, len: i32, ret: i32): i32;
```

A body-less `function` (terminated by `;` instead of a `{ … }` block) is an
import declaration; the `@import(iface, wit-name)` attribute supplies its
binding. Calls type-check against the declared signature like any function.

### WIT ↔ Fern type mapping (P4 covers the non-resource types)

| WIT | Fern |
|-----|------|
| `bool s8 u8 … s64 u64` | `i32` / `i64` (by width; `bool`→`i32`) |
| `f32 f64` | `f64` (f32 widened) |
| `char` | `i32` |
| `string` | `string` |
| `list<T>` | `T[]` |
| `tuple<A,B>` | `(A, B)` |
| `record` | `struct` (field-wise) |
| `enum` | C-style `enum` |
| `variant` | union / payload `enum` |
| `option<T>` | `Option<T>` |
| `result<T,E>` | `Result<T,E>` |
| `own<R>` / `borrow<R>` | **resource handle — P5** |

The canonical-ABI lowering (which params/results need memory/realloc, indirect
returns) is already computed by the `Classify` work — the extern call site
lowers to the same core import + `call` the built-ins use today, and the
world-driven composer (P2) wires it.

### Slice plan

1. **P4a — front end. ✅ Done (Go + self-host).** Lexer (no new keyword; `@`
   exists), parser (`@import(...)` + body-less `function`), AST (FuncDecl
   import binding), checker (register the extern, type-check calls). Parser +
   checker tests; no codegen. Self-host port: `parser.fern`
   (`parse_import_attr` + body-less `parse_func_decl` + the FuncDecl
   `import_iface`/`import_wit` fields) and `printer.fern` render + round-trip,
   gated by the `@import` assertions in the printer self-test
   (`TestSelfHostPrinterX86_64`).
2. **P4b — scalar codegen + e2e. ✅ Done (Go).** A body-less `@import`
   function lowers to `ir.ExternFunc` (kept out of `Program.Funcs` so every
   backend's defined-function machinery is untouched); the wasm backend turns
   each *referenced* extern into a core wasm function import of (interface,
   wit-name) with a signature derived from the Fern declaration, and a call
   resolves to that import's funcidx. Composed via the world-driven path
   (`ComposeFromWorldAuto`). Gated by `TestExternImportScalarRunsUnderWasmtime`
   (`internal/e2e/wit_extern_import_test.go`): `@import` of
   `wasi:random/random@0.2.0` `get-random-u64` → core import present →
   validates and runs under wasmtime. **Self-host port done:** `wasm.fern`
   `extern_imports` emits each referenced `@import` as a core wasm function
   import (`wat_extern_valtype` maps the Fern signature; 64-bit ints → i64),
   skipping its body in the func loop so a call resolves to the import's
   `$name`. Gated by `TestSelfHostExternImportRunsUnderWasmtime`
   (`internal/e2e/self_host_extern_import_test.go`): the self-host backend
   emits the core, the Go composer wires it, and the component runs.
   Until P4c lands, a composite-typed extern signature is **rejected** by the
   Go backend (`externScalarType` in `scanExternImports`) with a "composite
   types are P4c" message, rather than silently emitting raw pointer slots that
   don't match the host's canonical ABI — see `TestEmitExternCompositeRejected`.
3. **P4c — composite types.** Strings / lists / records / variants / options /
   results across the boundary (the canonical-ABI lift/lower the built-ins
   already do, generalised to user signatures). Lifts the P4b scalar-only
   guard as each shape gains real marshalling, in both compilers.
   - **string / list<u8> result — ✅ done (Go + self-host).** A string-typed
     extern result (which covers a WIT `string` *or* `list<u8>` — identical
     canonical ABI, `(ptr,len)`) is lowered via the return-area convention: the
     raw import gains a trailing return-area pointer and returns nothing, and
     the Fern name resolves to a generated wrapper that allocates a (4-byte
     aligned) return area, calls the raw import, and lifts `(data_ptr, len)`
     into a Fern string. `cabi_realloc` is exported so the host can materialize
     the bytes. Go: `scanExternImports` + `buildExternStringResultWrapper`
     (`extern.go`), reusing `__bytes_to_lang_string`; the raw import is named
     `<name>$import`. Self-host: `extern_imports` + `extern_wrappers`
     (`wasm.fern`), building a `[len][bytes]` string inline; raw import
     `<name>__import`. The return area is aligned in both (a list/string return
     area must be 4-byte aligned or the canonical call traps "pointer not
     aligned"). Gated by `TestExternListResultRunsUnderWasmtime` and
     `TestSelfHostExternListResultRunsUnderWasmtime` (`get-random-bytes(16) ->
     list<u8>` → a 16-byte Fern string after a heap-misaligning pre-alloc,
     validated + run), plus the `wasmbin` unit tests `TestEmitExternStringResult`
     / `TestEmitExternCompositeRejected`.
   - **u8[] result — ✅ done (Go + self-host).** The same `list<u8>` return
     lifted into a Fern `u8[]` instead of a string, for code that wants the
     bytes typed as an array. Go: a native array is length-prefixed (the value
     points to the elements, count at `ptr-4`), so `buildExternListU8ResultWrapper`
     allocates `4+n`, stores the count, and memory.copys the host bytes (u8 =
     1-byte stride) just past it; `isU8ArrayType` gates it. Self-host: the
     array uses an 8-byte header (count @0) with 4-byte element slots at +8, so
     `extern_wrappers` expands each host byte into its slot (the same shape
     `random_func_p2` builds). Gated by `TestExternU8ArrayResultRunsUnderWasmtime`
     / `TestSelfHostExternU8ArrayResultRunsUnderWasmtime` +
     `TestEmitExternU8ArrayResult`.
   - **string parameter — ✅ done (Go).** A `string` extern parameter lowers
     to the canonical `(ptr, len)` of contiguous UTF-8 bytes. A Fern string
     arrives SSO-encoded (inline strings pack bytes into the `(data, len)`
     words), so `buildExternStringParamWrapper` normalizes each string arg to a
     heap buffer (`emitStrNormalize`) before forwarding it, passing scalars
     through. Supported alongside a scalar/void result; gated by
     `TestExternStringParamCustomProvider` (a custom `byte-len: func(s: string)
     -> u32` provider sums the bytes of `"hello"` = 532, exercising both lowered
     halves) + `TestEmitExternStringParam`. **Self-host port done:** a
     self-host string is a pointer to `[len][bytes]`, so `extern_wrappers`
     forwards `(ptr+4, load(ptr))` per string param (no SSO to normalize);
     gated by `TestSelfHostExternStringParamCustomProvider`.
   - **Composer results-carrying trampoline — ✅ done (Go).** A memory-param
     import that returns a flat scalar (`func(string) -> u32` lowers to
     `(i32,i32) -> i32`) needs a trampoline that *carries the result*; the
     world-driven composer's mem trampolines were result-less (built for WASI's
     retptr-returning imports, whose core result is void), which mis-wired such
     an import as `-> ()`. `TrampolineModuleForParamsResults` /
     `FixupModuleForParamsResults` (+ `gImport.results`, sourced from the core
     import's own result valtypes via `coreFuncImports`) fix it. Additive: empty
     results reproduce the NoResult builders byte-for-byte, so every existing
     WASI import is unchanged (byte-identity oracle + the full component suite
     stay green). Gated by `TestTrampolineFixupModuleForParamsResults_Validates`
     and the now-single-function string-param e2e above. This is also a
     prerequisite for **P5** resource methods (which return `result<_, error>`).
   - **Numeric array params + results (`u8[]`, `i32[]`, `f32[]`, `i64[]`,
     `f64[]`, …) — ✅ done (Go).** A numeric-array argument or result of any
     fixed-width integer or float element maps to a canonical `list<T>`.
     Params: a Fern array is already a pointer to elements packed at native
     stride with the count at `ptr-4`, so the param wrapper forwards
     `(ptr, load(ptr-4))` zero-copy. Results: `buildExternListResultWrapper`
     (generalised from the u8-only one by an element-`stride` parameter)
     allocates the length-prefixed array and `memory.copy`s `count*stride` host
     bytes; the u8 path (stride 1) emits the same bytes as before. The Fern-side
     flattening (one pointer slot) differs from the canonical one (two i32s), so
     the raw import spec uses `canonicalExternParamValtypes` while the wrapper
     keeps the Fern signature. `buildExternStringParamWrapper` generalised to
     `buildExternMemParamWrapper`; the shared gate is `isScalarArrayParamType`
     (any `NumberType`/`FloatType` element). Gated by
     `TestExternListU8ParamCustomProvider`, `TestExternListI32ParamCustomProvider`,
     `TestExternListF32ParamCustomProvider`, `TestExternListI64ParamCustomProvider`
     (params), `TestExternListI32ResultCustomProvider` /
     `TestExternListF64ResultCustomProvider` (results), and
     `TestEmitExternListU8Param`/`TestEmitExternU8ArrayResult`. 8-byte elements
     (`i64[]`/`u64[]`/`f64[]`) need no special handling — the canonical lower of
     `list<u64>`/`list<f64>` accepts the 4-byte-aligned element pointer (bytes
     read in place, no alignment trap), confirmed by the i64/f64 e2e tests.
     **bool** arrays stay rejected (a Fern bool is 4 bytes but the canonical
     `list<bool>` element is 1 byte — stride mismatch); the self-host port is a
     follow-up.
   - **Record (struct) parameters — ✅ done (Go).** A Fern struct passed to an
     `@import` extern whose WIT signature takes a `record` flattens to its
     fields' core types (the canonical ABI passes a small record inline). The
     field layout — each field's offset from the struct value + its type — is
     precomputed during IR lowering (`ir.ExternFunc.ParamRecords`, where
     `info.Structs` is in scope; the wasm backend has no `info`), and the param
     wrapper loads each field off the struct value and pushes it in declaration
     order. `canonicalExternParamValtypes` flattens the record to the field
     valtypes for the raw import. Scoped to records of ≤16 fields, each a 32-/
     64-bit integer or float (sub-word ints, bool, strings, arrays, and nested
     records are deferred — a struct param outside this shape is rejected with a
     clear message). Gated by `TestExternRecordParamCustomProvider` (a
     `record point { x: s32, y: s32 }` summed) and
     `TestExternRecordParamWideCustomProvider` (a mixed `record mix { a: s32,
     b: s64 }`, exercising the i64 field's 8-byte offset + i64 flat valtype).
   - **Record (struct) results — ✅ done (Go).** An `@import` extern returning a
     `record` lifts into a Fern struct. A multi-field record flattens to > 1
     core value, so the canonical ABI returns it indirectly through a
     return-area pointer (like string/list results): the raw import gains a
     trailing area pointer and returns nothing. The layout (fields + struct
     size) is precomputed during IR lowering (`ir.ExternFunc.ResultRecord`), and
     `buildExternRecordResultWrapper` allocs the return area, calls the import,
     then materializes a Fern struct exactly as the constructor does — alloc
     `rcHeaderBytes + size`, rc=1 at base+0, copy each field to `base +
     rcHeaderBytes + offset`, return `base + rcHeaderBytes`. Scoped to 2..16
     32-/64-bit numeric/float fields (a single-field record returns its field
     directly — a different shape — and is deferred). Gated by
     `TestExternRecordResultCustomProvider` (a `make-point: func(s32, s32) ->
     point` lifted to a Fern struct, fields read back).
   - Still rejected (next slices): single-field records (direct return); tuple /
     variant / option / result params and results; bool arrays; sub-word /
     nested-record fields; and the self-host port. The multi-component harness
     (`TestExternImportCustomProvider`) is the test vehicle for these.
   - **CLI integration — ✅ done (Go).** `fern -target wasm` now compiles an
     `@import` program end to end: when the legacy composer's `ClassifyCore`
     reports imports it doesn't recognise and the program declares any extern
     (`hasExternImports`), `buildPreview2Component` rebuilds the core with
     `ForceMemorySection` and routes it through `ComposeFromWorldAuto` (the
     embedded fern world) instead of erroring — so a user gets a runnable
     component without the test harness. No-extern programs keep the legacy
     path unchanged. Gated by `TestExternImportViaCLI` (scalar + u8[] externs +
     a built-in `write`, composed by the CLI binary and run under wasmtime).
   - **World coverage — env / args / exit / monotonic clock — ✅ done.** The
     `fern` world (`cmd/fern/wit/fern.wit`, embedded as `fern.bin`) originally
     declared only the stdio / filesystem / TCP / random interfaces, so an
     extern program that *also* used a built-in routed through the world path
     (`args()`, `env()`, `exit`, monotonic `now()`) failed to compose with
     "core imports interface … not declared by the world". The world now also
     imports `wasi:cli/environment`, `wasi:cli/exit`, and
     `wasi:clocks/monotonic-clock`, so those built-ins lower through
     `ComposeFromWorldAuto` (env / args are `KindMemRealloc` list returns; exit
     / monotonic are `KindNoOpt`) exactly as the legacy registry lowers them.
     Gated by `TestExternImportWithBuiltinEnvArgsViaCLI` (an extern +
     `args()` + `env()`, run under wasmtime).
   - **World coverage — UDP sockets — ✅ done.** Sockets had only ever
     composed through the bespoke native registry; the `fern` world now also
     imports `wasi:sockets/udp` + `wasi:sockets/udp-create-socket`, so an
     extern program that also calls `udp_send()` composes through
     `ComposeFromWorldAuto`. This is the first socket shape the world-driven
     composer wires generically — the whole flow (`create-udp-socket`,
     `udp-socket.start-bind` / `finish-bind` / `stream`,
     `outgoing-datagram-stream.check-send` / `send` / `subscribe`, plus the
     three resource-drops) lowers without bespoke socket knowledge. Gated by
     `TestExternImportWithBuiltinUDPViaCLI` (an extern + `udp_send()` whose
     datagram a real listener receives).
   - **World coverage — wall-clock `now()` — ✅ done.** `wasi:clocks/wall-clock`
     gained `now` (the `now_ns()` builtin). Unlike monotonic `now` (a `u64`),
     wall-clock `now` returns the `datetime` record, which the interface
     re-exports through a `(type (eq N))` alias. The world decoder's
     `ResolveDef` (`internal/wasm/componenttype/world_lift.go`) didn't follow
     that eq-alias, so the result resolved to a nil def (an opaque handle) and
     `now` mis-classified as `KindNoOpt` — the emitted `canon lower` then
     dropped the required `memory` option and wasm-tools rejected the
     component. The fix resolves an eq-aliased type-export slot to its target
     def so the record's two fields flatten correctly (`KindMem`). Gated by
     `TestClassifyEqAliasedRecordResult` (decoder layer) and
     `TestExternImportWithBuiltinWallClockViaCLI` (an extern + `now_ns()`, run
     under wasmtime). With this every built-in capability the CLI emits lowers
     through the world path.
   - **Migrating the default (non-extern) compose path — ⏸ blocked on
     self-hosting parity.** Every non-extern shape is now world-composable
     (the `TestCompose*FromWorld` gates), so the `fern` CLI's
     `buildPreview2Component` *could* route plain cli/run programs through
     `ComposeFromWorldAuto` instead of the registry `component.Compose`. But the
     Go CLI's component output is the **oracle for self-hosting**:
     `TestSelfHostWasmComponentFull*` assert the self-hosted Fern compiler
     (`examples/self_host/wasm.fern`) emits byte-identical components to
     `fern -target wasm`. The self-hosted compiler implements the registry
     composition, so flipping the Go CLI to the world composer desyncs them.
     Retiring the registry for cli/run therefore requires re-implementing
     world-driven composition in the self-hosted compiler first (and
     `component.Compose` stays regardless — the HTTP `incoming-handler` export
     path still needs it; `ComposeExportsFromWorld` doesn't yet handle the
     resource-drops a handler uses).
   - **Custom (non-WASI) interface + provider — ✅ done (Go).** The headline
     BYO-WIT capability: a Fern program `@import`s a *fully custom* interface
     (`local:test/answer@0.1.0`, defined by the user, unknown to the compiler)
     and the import is satisfied at link time by a separate **provider
     component** — no built-in knowledge of the interface anywhere. The core is
     composed against a custom world via `DecodeWorldBytes` (the P3 entry
     point), then `wasm-tools compose --definitions` plugs the provider's
     export into the user's import. Gated by `TestExternImportCustomProvider`,
     which also stands as the **reusable multi-component harness** for the
     next slices (composite params / records) that have no non-resource WASI
     0.2 target and so need a custom provider to test against.
4. **P5 — resources / handles** (`own`/`borrow`/drop): a new type-system
   concept; the largest phase, and the first to exercise the composer's
   `gDrop` path from user code. Plan + design forks: `docs/P5-PLAN.md`.
   - **Slice 1 — handle type vocabulary. ✅ Done (Go + self-host).** A
     `resource Name;` declaration (bound to its WIT identity by an
     `@import("iface", "wit-resource-name")` attribute) introduces a nominal
     handle type, written `own Name` / `borrow Name` (the Fern-idiomatic
     prefix form, mirroring `dyn Trait` — Fern uses `[]` for generics, not
     `<>`); a bare resource name means an owned handle. Go: `ast.HandleType` +
     `ast.ResourceDecl`, parser (`parseType` own/borrow, `parseResourceDecl`),
     checker (register resources, `resolveType` reclassification,
     `validateResourceHandles`, `assignable` own→borrow coercion — a plain i32
     is *not* a handle), and erasure to i32 at the single `ir.LowerWith` choke
     point (`internal/ir/erase_handles.go`) so no backend/interp/self-host
     sees a HandleType. Self-host: `parser.fern` erases `own`/`borrow` to i32
     in `parse_type_name` and consumes `resource` decls. Gated by parser +
     checker + printer-round-trip tests and the e2e
     `TestExternResourceHandleTypes` / `TestSelfHostExternResourceHandleTypes`
     (the 0ns-timer pollable, now written with the handle vocabulary, runs
     under real WASI — proving the types are real yet erase to the working
     i32-handle core). Handles are still leaked.
   - **Slice 2 — composer `[resource-drop]` wiring. ✅ Done (Go composer +
     self-host core).** `ComposeFromWorldAuto` no longer rejects
     `[resource-drop]<res>` imports: `ComposeFromWorld` surfaces each dropped
     resource as a component-level type via `g.c.aliasType(instIdx, res)`
     (the same primitive the native socket/HTTP composer uses) and threads the
     index into `gImport{kind: gDrop, resourceT}`, which lowers to a canon
     `resource.drop`. Purely additive — a program with no `[resource-drop]`
     imports emits no alias sections, so its bytes are unchanged and the
     byte-identity-gated `internal/wasm/component` + `componenttype` suites
     stay green. Gated by `TestExternResourceHandleDrop` /
     `TestSelfHostExternResourceHandleDrop`: a program drops its pollable via a
     `[resource-drop]pollable` extern (the test vehicle for the composer
     change), and the component validates + runs under real WASI with the
     resource released rather than leaked. Since main retired the self-host
     composer port (`wat_component.fern`), the composer change is Go-only; the
     self-host's role is emitting the drop core, which it already does for any
     `@import`.
   - **Slice 3 — automatic drop. ✅ Done (Go); self-host port follows.** The
     compiler releases an owned `own R` handle when it goes out of scope, so
     user code never writes a manual drop. `internal/ir/insert_resource_drops.go`
     runs in `LowerWith` (before handle erasure): for each kept owned-handle
     local it inserts `defer <drop>(h);` — reusing Fern's defer machinery, which
     runs the drop on every function-exit path — and synthesizes one body-less
     `@import("…","[resource-drop]<wit>")` drop function per dropped resource
     (which the slice-2 composer wires). Soundness over completeness: a handle
     whose use can't be proven non-consuming (anything but a `borrow`-parameter
     call argument) is treated as moved and left for its consumer — leaking is
     safe, a double drop is not; `borrow R` is never dropped. The pass is
     idempotent (the diff oracle / multi-backend compiles re-run `LowerWith`).
     Gated by `internal/ir/resource_drop_test.go` (synthesis, move-skip,
     idempotency) and the e2e `TestExternResourceHandleAutoDrop` (a program that
     declares NO drop, yet the emitted core carries `[resource-drop]pollable`
     and the component releases the pollable under real WASI).
     **Self-host port — ✅ done.** `parser.fern` now preserves `own R` /
     `borrow R` type spellings (they still lower to i32 everywhere), records
     `resource` declarations with their WIT binding on `Module.resources`, and
     `insert_resource_drops` (run in `module_with_builtins` before
     `lower_defers_module`) inserts a `StmtDefer` drop after each kept owned
     handle and synthesizes the `[resource-drop]` import — the self-host's
     existing defer-lowering then expands it on every exit path, mirroring the
     Go pass (same move analysis: borrow-arg = kept, returned / `own`-arg =
     moved). Gated by `TestSelfHostExternResourceHandleAutoDrop` (the self-host
     emits the auto-inserted drop core, the Go composer wires it, runs under
     real WASI). **P5 is now complete in both compilers.**
5. **P6 — arbitrary exports**: bind a Fern function to a world export (beyond
   `cli/run` / `incoming-handler`) and lift it.
   - **Slice 1 — `@export` front end + checker. ✅ Done (Go + self-host).** An
     `@export("wasi:iface@x.y.z", "wit-name")` attribute on a function (WITH a
     body) marks it as the implementation of that world export — the
     body-carrying counterpart to body-less `@import`, reusing the same
     attribute machinery (`parseAttribute` now returns a `declAttr`, and
     `@import`/`@export` share the `("iface","name")` argument shape). Go:
     `ast.FuncDecl.ExportIface` / `ExportWITName`, parser (stamp + require a
     body + `@export` only on a function), checker (`validateExports` rejects a
     generic or method `@export` — a world export has one concrete ABI; the body
     is type-checked normally), printer round-trip. Self-host: `parser.fern`
     `parse_export_attr` + the `parse_module` dispatch (parse + consume the
     binding; storing it on the self-host `FuncDecl` and the lift land with the
     codegen slice, to avoid rippling the binding through every `FuncDecl`
     literal before it's used). Gated by parser (`TestExportAttributeParses` /
     `…Errors`), checker (`TestExportChecker`), printer (`TestFormatExportAttr`),
     and the self-host `TestSelfHostExportAttributeCompiles`.
   - **Slice 2 — export bridge: world query + IR + core-export surfacing. ✅
     Done (Go).** The codegen plumbing that the lift (slice 3) builds on:
     `componenttype.World.ExportedInterfaces` / `ExportFunc` lift the world's
     *export* declarations to a queryable model (mirroring `Interfaces` for
     imports — so the lift can resolve an export's WIT signature); `ir.Program`
     gains `Exports` (`@export` bindings threaded from `FuncDecl.ExportIface`);
     and the wasm backend surfaces a core export `iface#wit-name` per `@export`
     function (the WIT-id alias the world-driven composer keys off), pinning
     each `@export` function as a tree-shake / inline root so it survives even
     when no Fern code calls it. Purely additive (a program with no `@export`
     emits no extra exports — byte-identical; the byte-identity-gated
     `internal/wasm/component` suite stays green). Gated by
     `TestWorldExportedInterfaces` and `TestBuildExportSurfacesCoreExport`.
   - **Slice 3 — per-export lift + run (scalar). ✅ Done (Go).**
     `component.ComposeExportsFromWorld` wraps a reactor core (a library of
     `@export` functions, no cli/run): it wires any world imports, then for each
     world *exported* interface function whose surfaced `iface#wit-name` core
     export the backend emitted (slice 2), aliases that core func, builds the
     component functype from the WIT signature (`liftScalarExport` — a
     `componenttype.Valtype.Prim` is exactly the component `CValtype` byte), and
     canon-lifts + packages + exports it under the interface — generalising the
     fixed `_lang_run` / `incoming-handler` lifts. Purely additive (a new
     compose entry; existing paths untouched, the byte-identity composer suite
     stays green). Gated by `TestExportScalarReactorComposes` (validate + the
     component WIT declares the export) and `TestExportScalarRunsViaConsumer`: a
     Fern reactor's `@export add` and a separate Fern consumer that `@import`s
     and calls it are linked with `wasm-tools compose` and **run under wasmtime**
     (`add(20,3)==23`) — the lifted export is callable across the boundary.
   - **Slice 4 — self-host export port. ✅ Done.** The self-host now surfaces
     the `iface#wit-name` core export for `@export` functions so the Go
     world-driven composer lifts it. `parser.fern` records each `@export`
     binding on a new `Module.exports` side-list (`ExportBinding{func_name,
     iface, wit}` — kept off `FuncDecl` to avoid rippling through every literal,
     mirroring `resources`), threaded through every `Module` constructor
     (`monomorphize_*`, `lower_defers_module`, `merge_module*`, `bundle`,
     `module_with_builtins`, constfold/flatten); `wasm.fern` `extern_exports`
     emits `(export "iface#wit" (func $name))` for each. Gated by
     `TestSelfHostExportScalarRunsViaConsumer`: the self-host emits the exporter
     core, the Go composer lifts it, and a Fern consumer links + runs it under
     wasmtime (`add(20,3)==23`). **P6's scalar export path is complete in both
     compilers.**
   - **Slice 5a — memory/realloc lift encoders. ✅ Done (Go).** The
     byte-identity foundation for composite exports: `PutCanonSectionLiftWithMemory`
     (string/list RESULT — the lift reads the core's returned `(ptr,len)`) and
     `PutCanonSectionLiftWithMemoryRealloc` (string/list PARAM — the lift uses
     `cabi_realloc` to materialise incoming bytes in core memory), the inverse of
     the existing lower-with-memory encoders. Opts precede the typeidx for a
     lift. Byte-pinned by `TestPutCanonSectionLiftWithMemory_Bytes` /
     `…Realloc_Bytes` (the project's encoding-before-wiring discipline).
   - **Slice 5b — composer string-result lift. ✅ Done (Go).**
     `ComposeExportsFromWorld` now lifts a string-RESULT export with the memory
     lift: `liftExport` detects a `string` result (the WIT primitive byte) and
     emits `PutCanonSectionLiftWithMemory` (the core returns a pointer to the
     `[ptr,len]` return area, which the lift reads), aliasing the core memory
     (`exportNeedsMemory` gates it; reuses the lower path's aliased memory when
     present). Scalar results keep the no-opts lift; string PARAMS and other
     composites are still rejected with a clear message. Gated by
     `TestExportStringResultLiftRunsViaConsumer`: a hand-written WAT exporter
     returns "hi" via the canonical return area, the composer lifts it, and a
     Fern consumer that `@import`s `greet() -> string` and `write`s it links +
     runs under wasmtime. Additive — the byte-identity composer suite stays
     green.
   - **Slice 5c — string-result export from Fern source. ✅ Done (Go).** The
     wasmbin Fern→canonical wrapper: a string-returning `@export` function
     compiles to a core `(params…) -> (i32,i32)` pair, so the export loop now
     surfaces a wrapper (`buildExportStringResultWrapper`) that forwards the
     scalar params, calls the user func, **SSO-normalizes** the returned string
     into a heap buffer (`emitStrNormalize` — short Fern strings pack bytes
     inline, so the words aren't a raw `(ptr,len)`), and writes the 4-byte
     aligned `[ptr,len]` return area the memory lift reads; its helpers
     (`__fern_str_len/byte/alloc`) are pinned for string-result exports.
     Scalar-result exports surface the function directly (unchanged). Gated by
     `TestExportStringResultFromFernRunsViaConsumer`: a Fern `@export greet():
     string { return "hi"; }` reactor composes and a Fern consumer reads `"hi"`
     under wasmtime — the whole Fern→component→consumer string-export path.
   - **Slice 5c self-host port — string-result export. ✅ Done.** `wasm.fern`'s
     `extern_exports` now emits the string-result wrapper: the self-host string
     is a pointer to a `[len][bytes]` block, so the wrapper reads `len=[s]`,
     `data=s+4`, and writes the 4-byte-aligned `[ptr,len]` canonical return area
     the Go composer's memory lift reads (no SSO normalize — the self-host
     string is always heap). Gated by
     `TestSelfHostExportStringResultRunsViaConsumer`: the self-host emits the
     wrapper, the Go composer lifts it, and a Fern consumer reads `"hi"` under
     wasmtime. **P6 string-result exports are now complete in both compilers.**
   - **Slice 5d — string parameter exports (Go). ✅ Done.** `liftExport` now
     accepts a string parameter and lifts with the realloc lift
     (`PutCanonSectionLiftWithMemoryRealloc`): the canonical ABI materialises the
     incoming bytes in the core memory via `cabi_realloc`, then passes
     `(ptr,len)` — which maps directly to wasmbin's two-word string, so no
     wrapper is needed (vs. the string-*result* wrapper). `ComposeExportsFromWorld`
     aliases `cabi_realloc` when any export has a string param
     (`exportNeedsRealloc`). Gated by `TestExportStringParamRunsViaConsumer`: a
     Fern `@export len_of(s: string): i32 { return s.len(); }` reactor, and a
     Fern consumer calling `len_of("hello") == 5`, link + run under wasmtime.
   - **Slice 5d self-host — string-param export. ✅ Done.** `wasm.fern`'s
     `extern_exports` now emits a unified wrapper (`build_export_wrapper`) for
     any composite-signature export: a string parameter (canonical `(ptr,len)`)
     is copied into a fresh `[len][bytes]` block the Fern func expects, and a
     string result is repacked into the `[ptr,len]` return area — scalars pass
     through. The heap allocator (`export_needs_heap`) and `cabi_realloc`
     (`export_needs_realloc`) are emitted when an export needs them. Gated by
     `TestSelfHostExportStringParamRunsViaConsumer` (self-host emits the wrapper,
     Go composer lifts with realloc, consumer's `len_of("hello")==5` runs under
     wasmtime). **P6 string param+result exports are now complete in both
     compilers.**
   - **Slice 6 (next) — resource-typed exports**: lift an export taking/returning
     `own<T>` (the inverse of the import resource handling) — the piece that lets
     `wasi:http`'s `incoming-handler#handle` (which takes `own<incoming-request>`
     / `own<response-outparam>`) become a plain `@export`, unblocking retiring
     the built-in HTTP world.

Each slice ships in both compilers (the per-phase parity rule above) and is
gated by a running component.

## Open decisions (need a steer)

- **How far into the language surface is in scope?** P1–P3 (pluggable WASI
  set, import-side only) is a few focused PRs and reuses everything recent.
  P4–P6 (extern decls, resources, arbitrary exports) is a multi-month
  language feature. The right first target depends on the goal — "let me
  add/swap host interfaces without patching the Go compiler" (P1–P3) vs.
  "Fern programs bind arbitrary WIT worlds" (P1–P6).
- **Ingestion: shell-out vs. in-tree parser** (the dependency-light project
  ethos pushes toward eventually owning a parser, but shelling out unblocks
  the early phases cheaply).
- **Self-host parity is required, per phase** (see the phased-plan note
  above), not deferred. The Go side leads only to pin the encoding against
  the `fern.bin` / `http.bin` oracle; the `examples/self_host/` port follows
  immediately and is gated byte-identical against the Go reference.
  Resources/exports (P5/P6) will be sizeable on the self-host side too —
  budget for it rather than letting Go race ahead.

## Risks / notes

- **Index arithmetic + the byte-identical gate** remain the safety net (as
  in the suffix work). Keep the "reproduce the shipped world exactly"
  oracle for every WIT-driven phase.
- **Resources are the real blocker** for arbitrary worlds; a "bring your own
  WIT" that excludes resources covers only simple worlds. Scope this
  explicitly rather than discovering it mid-build.
- **`wasm-tools` as a compile-time dependency** is acceptable for early
  phases (the e2e suite already requires it) but should not become a hard
  runtime requirement of the shipped compiler for the common path.

## Appendix: P1 decoder spec (reverse-engineered)

Concrete groundwork so P1 can start clean. The `componenttype/*.bin`
payload is **an inner component binary** (`\0asm 0d 00 01 00`), not a bare
type stream. Layout of `fern.bin`:

```
\0asm 0d 00 01 00                     component header
custom "wit-component-encoding"       (2-byte payload)
component type section (id 0x07)      1 entry: type 0 = Component([decls...])
component export section (id 0x0b)    export "fern" = (type 0)
custom "producers"                    tooling metadata (ignorable)
```

The world lives in **type 0** (`Component([...])`). Its declaration vector
mixes:

- `Type(<deftype>)` where `<deftype>` is one of:
  - `Component([decls])` — nested (the world itself is `Component(0)`).
  - `Instance([decls])` — one per imported interface; decls are
    `Type`/`Alias`/`Export`.
  - `Func(params, results)` — params are `(name, valtype)*`; results are
    `Unnamed(valtype)` or `Named([(name,valtype)*])`.
  - `Defined(<defvaltype>)` — `Record([(name,ty)])`, `Variant([case{name,
    ty?,refines?}])`, `Enum([name])`, `Flags([name])`, `List(ty)`,
    `Option(ty)`, `Result{ok?,err?}`, `Tuple([ty])`, `Own(i)`, `Borrow(i)`,
    or `Primitive(bool/s8../u64/f32/f64/char/string)`.
- `Import(ComponentImport{ name, ty })` — the world's imports
  (`name = "wasi:io/streams@0.2.0"`, `ty = Instance(<typeidx>)`). **These are
  the list Gap A needs.**
- `Export(ComponentExport{ name, kind, index })` — the world's exports.
- `Alias(Outer{kind,count,index})` / `Alias(InstanceExport{kind,instance,
  name})` — type aliasing across scopes (e.g. surfacing `output-stream`
  from `io/streams` into `cli/stdout`).
- `CoreType` (not present in these worlds, but part of the grammar).

**No length prefixes.** Component-model types are not size-delimited, so the
cursor only advances by fully parsing each declaration — there is *no*
shortcut to "just enumerate import names"; the structural walker is the
whole job. Resource types appear as `Export{ ty: Type(SubResource) }`.

**Round-trip oracle (the P1 gate):** decode `fern.bin` **and** `http.bin`
into the structured model, re-encode, and assert byte-equality with the
input. That proves the decoder lossless without yet wiring it to anything.
The re-encoder is not throwaway — P2 reuses the same defvaltype/func
encoders (they overlap with the existing `InnerType*` emitters) to produce
type-import sections from a decoded world.

**Suggested P1 breakdown (each its own PR):**
1. LEB/byte reader + component section walker; identify the type/export/
   custom sections; ignore `wit-component-encoding` + `producers`.
2. Defvaltype + func-type decoder (the value-type grammar above).
3. Instance/component type + import/export/alias decoder; assemble the
   world model; round-trip `fern.bin` / `http.bin` byte-for-byte.
