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
   - **bool[] parameters — ✅ done (Go).** A Fern bool is a 4-byte i32 (0/1) but
     the canonical `list<bool>` element is a single byte, so — unlike the numeric
     arrays, whose native stride already matches the canonical element size and
     pass zero-copy — a bool array can't pass directly. `isBoolArrayParamType`
     gates a dedicated branch in `buildExternMemParamWrapper` that byte-repacks:
     it allocs a `count`-byte buffer and writes each 4-byte bool's low byte
     contiguously (`i32.store8`) in a loop, then forwards `(buf, count)`.
     `canonicalExternParamValtypes` flattens it to the same `(i32, i32)` as the
     other arrays. Gated by `TestExternBoolArrayParamCustomProvider` (a
     `count-true: func(b: list<bool>) -> s32` provider; `[true,false,true]` → 2).
     **Sub-4-byte `list<T>` *results* (`u8`/`bool`) via a custom provider — ✅
     done (Go).** This was previously deferred on a suspected `gMemRealloc`
     trap, but the `ComposeFromWorldAuto` + custom-provider path now composes and
     runs cleanly (an intervening composer/realloc fix resolved it): a
     `func(n: u32) -> list<u8>` lifts into a Fern `u8[]` (the numeric-array result
     wrapper at stride 1), and a `func(n: u32) -> list<bool>` lifts into a Fern
     `boolean[]` — here the canonical element is 1 byte but a Fern bool array slot
     is 4, so `buildExternBoolListResultWrapper` **byte-EXPANDS** each host byte
     into a 4-byte i32 element (the inverse of the bool[]-param byte-repack).
     Gated by `TestExternListU8ResultCustomProvider` and
     `TestExternBoolArrayResultCustomProvider` (both `ComposeFromWorldAuto` +
     custom provider, run under wasmtime), plus the wasmbin unit
     `TestEmitExternBoolArrayResult`. The self-host port of bool[] params is done
     (see below); the self-host **bool[]-result port is done** too — the self-host
     stores u8 and boolean array elements identically (one per 4-byte slot), so a
     `boolean[]` result reuses the u8[]-result byte-expansion wrapper verbatim
     (only the `is_extern_composite_ret` gate + the wrapper's ret-type check
     accept `"boolean[]"`). Gated by `TestSelfHostExternBoolArrayResultCustomProvider`.
   - **Update (#4408):** `i8`/`i16`/`u16` were retired from the Fern
     language (zero-to-low real usage, full per-stride backend cost).
     The two "Sub-word integer fields" passages below describe the
     shipped-then-retired `s8`/`s16`/`u16` marshalling as historical
     record — a Fern struct field can no longer be narrower than
     `u8`/`i32`, so those widths widen to `i32`/`u32` on the Fern side
     now (the WIT-side `.wit` interface is free to still declare
     `s8`/`u16`; the canonical ABI already flattens those to i32 at
     the wire, so nothing there changes — only whether *Fern* can
     mirror the field at its native sub-word size is gone). The gated
     tests (`TestExternRecordParamSubwordCustomProvider` /
     `TestExternRecordResultSubwordCustomProvider` and their
     self-host mirrors) were widened to `i32`/`u32` fields
     accordingly, still exercising multi-field record marshalling
     end to end.
   - **Record (struct) parameters — ✅ done (Go).** A Fern struct passed to an
     `@import` extern whose WIT signature takes a `record` flattens to its
     fields' core types (the canonical ABI passes a small record inline). The
     field layout — each field's offset from the struct value + its type — is
     precomputed during IR lowering (`ir.ExternFunc.ParamRecords`, where
     `info.Structs` is in scope; the wasm backend has no `info`), and the param
     wrapper loads each field off the struct value and pushes it in declaration
     order. `canonicalExternParamValtypes` flattens the record to the field
     valtypes for the raw import. Scoped to records of ≤16 fields, each an
     8-/16-/32-/64-bit integer or a float. **Sub-word integer fields
     (`s8`/`s16`/`u8`/`u16`) are supported as params**: a Fern struct stores
     every sub-64-bit int in a 4-byte slot, but the canonical ABI flattens an
     s8/u16 field to a single sign-/zero-extended i32, so the wrapper reads a
     sub-word field with a width+sign-aware load (`i32.load8_s/u`,
     `i32.load16_s/u` via `appendExternFieldLoad`) to produce the correct i32
     (`externRecordFieldValtype` keeps the flat valtype i32). **A nested record
     field is also flattened, to *arbitrary depth***: the canonical ABI inlines a
     nested record, so `externParamLeavesRec` recurses into (flattenable) struct
     fields, emitting one leaf per inner scalar. A Fern struct field of struct
     type is a *pointer*, so a leaf nested N levels deep carries a `DerefPath`
     (the chain of N outer field offsets): the wrapper deref-s each in turn (load
     the inner value pointer) then loads the leaf at its innermost offset. A
     **`bool` field** is also supported (both directions), treated as an unsigned
     8-bit: the Fern bool is 0/1 in a 4-byte slot, read with `i32.load8_u` (low
     byte) and sized at 1 byte in the canonical memory layout
     (`externCanonicalFieldSizeAlign`) — see the bool-field test below. Strings
     and arrays as fields are still deferred — a struct param outside this shape
     is rejected with a clear message. Gated by
     `TestExternRecordParamCustomProvider` (a `record point { x: s32, y: s32 }`
     summed), `TestExternRecordParamWideCustomProvider` (a mixed `record mix {
     a: s32, b: s64 }`, exercising the i64 field's 8-byte offset + i64 flat
     valtype), `TestExternRecordParamSubwordCustomProvider` (a `record { a: s8,
     b: u16, c: s32 }` with `a = -5`, `b = 300` — values that fail under the
     wrong-width or wrong-sign load), `TestExternRecordParamNestedCustomProvider`
     (one level — a `record line { p: point, q: point }` flattened to its 4 inner
     coords), and `TestExternRecordParamDeepNestedCustomProvider` (three levels —
     `outer { l: mid, r: mid }` / `mid { p: point, n: s32 }` / `point { x, y }`,
     the six leaves weighted so the deref chains are checked). The `bool`-field
     round-trip (param + result) is gated by
     `TestExternBoolRecordFieldCustomProvider` (a `record flag { on: bool, n: s32
     }` via `mk`/`rd`).
   - **Record (struct) results — ✅ done (Go).** An `@import` extern returning a
     `record` lifts into a Fern struct. A multi-field record flattens to > 1
     core value, so the canonical ABI returns it indirectly through a
     return-area pointer (like string/list results): the raw import gains a
     trailing area pointer and returns nothing. The layout (fields + struct
     size) is precomputed during IR lowering (`ir.ExternFunc.ResultRecord`), and
     `buildExternRecordResultWrapper` allocs the return area, calls the import,
     then materializes a Fern struct exactly as the constructor does — alloc
     `rcHeaderBytes + size`, rc=1 at base+0, copy each field to `base +
     rcHeaderBytes + offset`, return `base + rcHeaderBytes`. A **single-field**
     record flattens to exactly one core value (fits `MAX_FLAT_RESULTS=1`), so
     the canonical ABI returns it *by value* — recorded as
     `ResultRecord.Direct`, the raw import returns the field's valtype directly
     (no return area / `cabi_realloc`), and `buildExternRecordResultDirectWrapper`
     materializes the one-field struct from it (push the store address, call the
     import for the value, typed-store, return the pointer). Scoped to 1..16
     8-/16-/32-/64-bit numeric/float fields. **Sub-word result fields
     (`s8`/`s16`/`u8`/`u16`) are supported** via a *dual layout*: the canonical
     return-area packs them at their natural 1-/2-byte size + offset, which
     differs from the Fern struct's 4-byte slots, so each field carries both its
     Fern `Offset` and a `CanonicalOffset` (and the result a `CanonicalSize` for
     the area alloc). The wrapper reads each field from the return area at its
     `CanonicalOffset` with a width+sign-aware load (`appendExternFieldLoad`) and
     stores it into the wider Fern slot at `Offset` (`appendExternFieldStore`);
     for word-only records the two layouts coincide, so existing tests are
     byte-identical. Gated by `TestExternRecordResultCustomProvider` (a
     `make-point: func(s32, s32) -> point` lifted to a Fern struct, fields read
     back), `TestExternSingleFieldRecordResultCustomProvider` (a `make-wrapped:
     func(a: s32) -> record { v: s32 }` returned by value), and
     `TestExternRecordResultSubwordCustomProvider` (a `make-mix: func() -> record
     { a: s8, b: u16, c: s32 }` with `{-5, 300, 1000}` packed at canonical
     offsets 0/2/4 — values that fail under the wrong-width/sign load). **A
     nested-record field is also lifted, to *arbitrary depth***: the canonical
     area inlines every nested record's leaves (each at its own alignment), and
     the wrapper materializes a separate inner Fern struct per node, wiring child
     pointers into their parents bottom-up. `externResultLayoutRec` recurses the
     canonical layout (building the `ExternRecordField.Nested` subtree;
     `externCompositeAlign` gives each nested record's alignment), and
     `buildExternRecordResultWrapper` recurses the materialization with one
     scratch local per nesting level (`rrNestDepth` sizes them). A
     single-leaf-via-nested result (which the canonical returns by value) is
     rejected (the by-value Direct wrapper can't reconstruct nesting). Gated by
     `TestExternRecordResultNestedCustomProvider` (one level — a `make-line:
     func(...) -> record line { p: point, q: point }`; reads `l.p.x`/`l.q.y`) and
     `TestExternRecordResultDeepNestedCustomProvider` (three levels —
     `outer { l: mid, r: mid }` / `mid { p: point, n: s32 }` / `point { x, y }`;
     reads `o.l.p.x` … `o.r.n`).
   - **Tuple params + results — ✅ done (Go).** A Fern tuple is laid out exactly
     like a struct (rc header + elements at the same packing), so the record
     machinery generalises to tuples for free: `externCompositeFieldTypes`
     extracts a struct's field types *or* a tuple's element types, and the same
     `externRecordLayout` / `externRecordResultLayout` / wrappers handle both.
     `tuple<...>` params flatten to their elements; multi-element `tuple<...>`
     results return indirectly and materialize a Fern tuple. Same scope as
     records (32-/64-bit numeric/float elements; 1..16 for params and results —
     a single-field tuple would return its element directly like a single-field
     record, but Fern has no 1-tuple syntax (`(T)` parses as a parenthesised
     `T`), so only multi-element tuple results arise). Gated by
     `TestExternTupleParamCustomProvider` (a `sum-pair:
     func(p: tuple<s32, s32>) -> s32`) and `TestExternTupleResultCustomProvider`
     (a `make-pair: func(s32, s32) -> tuple<s32, s32>`).
   - **Sum-type (option / result) params — ✅ done (Go).** A Fern Option / Result
     passed to an `@import` extern whose WIT takes `option<T>` / `result<T,E>`.
     A Fern enum is a heap box `[tag:i32 @0][payload @off]`; the canonical
     `option`/`result` flattens to `(disc:i32, payload)`. The wrapper pushes the
     discriminant — the tag, **remapped `1-tag` for option** (Fern's Some=0/None=1
     is the reverse of canonical none=0/some=1; result's Ok=0/Err=1 matches) —
     then the payload loaded at its box offset. The layout (remap flag, payload
     type/offset) is precomputed during IR lowering (`ir.ExternFunc.ParamEnums`
     via `externEnumParamLayout`). Body-less externs are never pair-form
     (`isPairFormEligible` requires a body), so the enum value is always a heap
     box at the boundary. Scoped to `Option[T]` and `Result[T,E]` (T,E equal-
     width) with a 32-/64-bit numeric/float payload. Gated by
     `TestExternSumTypeParamCustomProvider` (Ok(42)→42, Err(5)→-5, Some(7)→7,
     None→-1 through the multi-component harness under wasmtime).
   - **Sum-type (option / result) results — ✅ done (Go).** An `@import` extern
     returning `option<T>` / `result<T,E>` lifts into a Fern Option / Result.
     The canonical variant flattens to (disc, payload) > 1 core value, so it
     returns indirectly through a return-area pointer (`disc:u8 @0`, payload
     `@off`); `buildExternEnumResultWrapper` reads them and materializes a Fern
     enum box exactly like `emitRepackPairAsHeapBox` (alloc `rcHeaderBytes +
     size`, rc=1, the i32 tag — remapped `1-disc` for option — at
     base+rcHeaderBytes, payload at +off), returning the box pointer. The layout
     reuses `externEnumParamLayout` on the return type (`ir.ExternFunc.
     ResultEnum`). Same scope as the param side. Gated by
     `TestExternSumTypeResultCustomProvider` (a `div: -> result<s32,s32>` and
     `half: -> option<s32>`, the Fern side matching Ok/Err/Some/None).
   - **WIT `enum` params + results — ✅ done (Go).** A Fern "plain" enum — a user
     enum whose variants are *all* payloadless (a C-style enum) — at an `@import`
     extern whose WIT takes/returns an `enum`. A WIT enum flattens to a single
     i32 discriminant; a Fern payloadless enum value is a pointer to a 4-byte
     sentinel `[tag:i32 @0]` (`OpEnumSentinel`). **Param:** the wrapper reads
     `i32.load(ptr)` and pushes the tag. **Result:** the import returns the disc
     by value; the wrapper maps it back to the matching static per-tag sentinel
     via the `__enum_sent(disc)->ptr` select-chain helper — **no heap
     allocation** (sentinels are shared immortal data cells, keyed by tag value
     across all enums), built where `internEnumSentinel` is in scope and sized to
     the max variant count across enum results. `externPlainEnumParam` gates both
     (an `ast.EnumType` whose `info.Enums` decl has ≥1 variant, all payloadless;
     Option/Result naturally fail the all-payloadless test and keep their own
     remapping path), recorded as `ir.ExternFunc.ParamPlainEnums` /
     `ResultPlainEnumN`. **The Fern variant order must match the WIT enum case
     order** — no remap — a declaration-order contract (the canonical disc is the
     tag). Gated by `TestExternEnumParamCustomProvider` (a `pick: func(c: color)
     -> s32`; `Green` → 101) and `TestExternEnumResultCustomProvider` (a `choose:
     func(n: s32) -> color` returning `disc = n`; `rank(0/1/2)` → `Red/Green/Blue`
     — a full disc→sentinel→tag round-trip). General payload-carrying `variant`s
     are deferred.
   - **General `variant` params (uniform + non-uniform same-width payload) — ✅
     done (Go).** A Fern user enum with *payload-carrying* variants passed to an
     `@import` extern whose WIT takes a `variant`. Every payloaded variant carries
     exactly one scalar (payloadless variants allowed; ≥1 must be payloaded — else
     it's a plain enum). The canonical `variant` flattens to (disc, payload-join);
     two payload shapes lower (`externVariantParamLayout`, shared with the result
     side):
       - **Uniform** — all payloads the same kind+width T: the join is T, so the
         existing (disc, payload) enum-param wrapper is reused verbatim
         (`PayloadType = T`; an f32 payload passes as an f32).
       - **Non-uniform but same core WIDTH** — e.g. `{ i(s32), f(f32) }` (both
         32-bit) or `{ l(s64), d(f64) }` (both 64-bit): the canonical join is the
         integer bit-container of that width (i32 / i64). `PayloadType` is set to
         that synthetic int, so the wrapper moves the payload's *bits*
         (`i32.load`/`i64.load` of the box slot) and each side reinterprets per
         the disc. No per-arm branch is needed — same-width payloads sit at the
         same box offset (the per-variant `payloadLayout` is width-determined) and
         a bit-load is value-preserving for both int and float arms.
       - **Mixed core WIDTH** — a 32-bit and a 64-bit arm, e.g. `{ i(s32),
         l(s64) }`: the canonical join is i64, and each arm lives at its OWN box
         offset and needs coercion (a 32-bit arm `i64.extend_i32_u` to / `i32.wrap_i64`
         from the i64 slot). `PayloadType` is i64 and `ExternEnumParam.Variants`
         carries each arm's box offset + type; the wrapper branches on the disc
         (`appendVariantParamPayloadI64` / `appendVariantResultStore`) to
         load/store at the right offset+width. (Float bits ride the int loads, so
         f64 / f32 arms work too.)
     A fourth shape — **MULTI-FIELD** arms (a case carrying ≥2 payloads; in WIT a
     case wraps multiple values in a `tuple<…>`, which flattens identically to a
     Fern multi-field variant `Click(i32, i32)`) — joins **position-wise** to
     `SlotCount` slots, each slot j's type the canonical join of every arm's field
     j (the full general join: `join(i32, f32) = i32`; any other unequal pair →
     i64; equal → that type — so a slot may be i32 / i64 / f32 / f64). `ExternEnum-
     Param.SlotCount` + `SlotTypes` (per-slot join valtype) + `Variants[k].{Fields,
     FieldTypes,FieldAreaOffsets}` drive the wrappers. The **param** side pushes
     each slot by branching on the disc — the arm's field j loaded from its box
     offset and coerced to the slot type (a 32-bit field rides an i32 slot as its
     raw bits, an f32 likewise; a 32-bit field under an i64 slot zero-extends; an
     f64 rides an i64 slot as its bits), or the slot's zero to pad shorter arms
     (`appendVariantParamMultiField`). The **result** side reads the canonical
     variant *memory* layout — a 1-byte disc, then the payload aligned to the
     widest field, so each arm's fields sit at its own tuple offsets
     (`FieldAreaOffsets`, precomputed in `externVariantParamLayout`) — and copies
     each by field width (i64 for an 8-byte field, i32 for a 4-byte one) into the
     box, the float bits surviving the integer move
     (`appendVariantResultStoreMultiField`). Scoped to 32-/64-bit numeric/float
     fields (`externMultiFieldVariantFieldOK`; sub-word s8/s16, which pack at
     1-/2-byte canonical sizes, are a separate slice). No discriminant remap (the
     user-enum variant index is the WIT case order); a payloadless-case sentinel's
     payload read is ignored garbage (the host drops it for that disc). Gated by
     `TestExternVariantParamCustomProvider` (uniform —
     `describe: func(s: shape) -> s32` over `variant shape { circle(s32),
     square(s32), empty }`: `Circle(7)`→7, `Square(7)`→70, `Empty`→999),
     `TestExternVariantNonUniformParamCustomProvider` (`{ i(s32), f(f32) }`, the
     f32 arm's bits round-tripping through the i32 join),
     `TestExternVariantMixedWidthParamCustomProvider` (`{ i(s32), l(s64) }`, the
     i64 arm carrying a value that needs 64 bits),
     `TestExternVariantMultiFieldParamCustomProvider` (`{ click(tuple<u32,u32>),
     key(u32), close }` ↔ `Ev { Click(i32,i32), Key(i32), Close }`), and
     `TestExternVariantMultiFieldMixedParamCustomProvider` (the general join —
     `{ move(tuple<s32,s64>), spin(tuple<f32,f64>), stop }` ↔ `Ev { Move(i32,i64),
     Spin(f32,f64), Stop }`: slot0 = join(s32,f32) = i32, slot1 = join(s64,f64) =
     i64, the f32/f64 bits riding the int slots). The result direction mirrors all
     five via `TestExternVariant{,NonUniform,MixedWidth,MultiField,MultiFieldMixed}
     Result…`.
   - **General `variant` results (uniform payload) — ✅ done (Go).** An `@import`
     extern returning a WIT `variant` with a uniform scalar payload (every case
     payloaded, same type T), lifted into a Fern user enum. The canonical variant
     flattens to (disc, payload) > 1 core value, so it returns indirectly (disc:u8
     @0, payload @off) — exactly the option/result result shape — and reuses
     `buildExternEnumResultWrapper` (materializing a Fern enum box
     `[rc][tag@0][payload@off]`) with no discriminant remap, matching how a
     payloaded user-enum variant is represented (so it's leak-free + consistent).
     `externVariantResultLayout` gates it (uniform single-scalar payload across
     the payloaded variants). **Payloadless variants are allowed** (a mixed
     `variant`, e.g. `{ some(s32), none }`): a payloadless case is materialized as
     that same box with an unused payload — exactly how option/result results
     already materialize their payloadless arm (`None`), so it's tag-correct and
     match-correct (the box's unused payload is never read). Gated by
     `TestExternVariantResultCustomProvider` (a `classify: func(n: s32) -> grade`
     over `variant grade { low(s32), mid(s32), high(s32) }`; recovers (tag,
     payload) for all three cases) and `TestExternVariantResultMixedCustomProvider`
     (a `lookup: func(n: s32) -> opt-num` over `variant opt-num { some(s32),
     none }`, exercising a payloaded + a payloadless case). **Non-uniform
     same-width payloads** are also lifted (the same `externVariantParamLayout`
     join — the i32/i64 bit-container): gated by
     `TestExternVariantNonUniformResultCustomProvider` (a `classify: func(n: s32)
     -> num` over `variant num { i(s32), f(f32) }`, the f32 arm's bits surviving
     the i32 join slot bit-exactly).
   - **Self-host port — numeric array params — ✅ started.** The self-hosted
     compiler (`examples/self_host/wasm.fern`) gained the first BYOW data-type
     beyond strings: a numeric array (`i32[]`/`i64[]`/`f32[]`/`f64[]`/…) `@import`
     parameter. The self-host array layout differs from the Go backend's (value
     is the block base — len@0, elements@+8 in native-stride slots — not
     count-at-ptr-4), and crucially the self-host heap is **unaligned**
     (`__fern_alloc` bumps without rounding, heap starts at an odd offset), so a
     zero-copy `(elements, len)` traps the canonical `list<s32>` alignment
     assert. The wrapper therefore **copies** the elements into a freshly
     8-aligned buffer (`(__fern_alloc(n*slot)+7)&-8` + a per-element loop) and
     passes `(buf, len)`. `extern_array_param_supported` gates element kinds
     whose slot == canonical size (i32/u32/f32 slot 4, i64/u64/f64 slot 8; u8/i16
     need repacking, deferred). Gated by
     `TestSelfHostExternArrayParamCustomProvider` (a `sum-i32: func(data:
     list<s32>) -> s32`, run through the self-hosted backend under wasmtime);
     the self-host string-param + list-result tests and the self-compile oracles
     stay green.
   - **Self-host port — numeric array results — ✅ done.** The symmetric
     counterpart: a numeric array (`i32[]`/`i64[]`/`f32[]`/`f64[]`/…) `@import`
     *result* lifts into a self-host array. It generalises the existing
     u8[]-result wrapper — for a numeric element the self-host slot size equals
     the canonical element size, so the wrapper copies each element straight
     into its slot at +8 (no byte-expansion). `is_extern_composite_ret` now also
     matches `extern_array_param_supported`, which auto-exports the canonical
     `cabi_realloc` (the host lifts the list into the self-host memory). Gated by
     `TestSelfHostExternArrayResultCustomProvider` (an `iota: func(n: u32) ->
     list<s32>`, lifted to `i32[]` and indexed).
   - **Self-host port — record (struct) params — ✅ done.** A struct `@import`
     parameter flattens to its fields. A self-host struct value is
     `[type-id@0][field@+4 in 4-byte slots]`, so the wrapper pushes one i32 per
     field — no canonical memory pointer, hence **no alignment wall** (unlike the
     arrays). `extern_record_param_supported` gates a known struct of 1..16
     8-/16-/32-bit integer fields; `extern_imports` emits one `(param i32)` per
     field; the wrapper forwards one load per field. **Sub-word integer fields
     (`s8`/`s16`/`u8`/`u16`) are supported** — they sit in the same 4-byte slots,
     so the wrapper reads each with a width+sign-aware load
     (`extern_field_load_op` → `i32.load8_s/u`, `i32.load16_s/u`) to get the
     correctly extended canonical i32; word fields use plain `i32.load`. **A
     nested record field is flattened, to *arbitrary depth*** (mirroring the Go
     side): a field that is itself a record (`extern_record_nestable`) expands to
     one leaf per inner scalar; since a self-host struct-of-struct field is a
     pointer, the recursive `extern_emit_record_param_leaves` deref-s the inner
     value pointer at `struct+outerOff` and recurses with that as the new base, to
     any depth. `extern_record_leaf_count` (recursive) sizes the import's
     `(param i32)` list. `mod` is threaded into `has_extern_mem_param` /
     `extern_needs_wrapper` for the struct-decl lookup. Gated by
     `TestSelfHostExternRecordParamCustomProvider` (a `sum-point: func(p: record {
     x, y: s32 }) -> s32`, x+y), `TestSelfHostExternRecordParamSubwordCustomProvider`
     (a `record { a: s8, b: u16, c: s32 }` with `a = -5`, `b = 300` — values that
     fail under the wrong-width or wrong-sign load),
     `TestSelfHostExternRecordParamNestedCustomProvider` (one level — a `sum-line:
     func(l: line) -> s32` over `record line { p: point, q: point }`,
     `Line{p:{1,2},q:{3,4}}` → 10), and
     `TestSelfHostExternRecordParamDeepNestedCustomProvider` (three levels —
     `outer { l: mid, r: mid }` / `mid { p: point, n: s32 }` / `point { x, y }`,
     the six leaves weighted so the deref recursion's ordering is checked). **`bool` fields** are also supported (both
     directions), treated as an unsigned 8-bit (`extern_field_is_scalar` accepts
     `boolean`, `extern_field_load_op` → `i32.load8_u`, `extern_canon_field_size`
     → 1), gated by `TestSelfHostExternBoolRecordFieldCustomProvider` (a `record
     flag { on: bool, n: s32 }` via `mk`/`rd`).
   - **Self-host port — record (struct) results — ✅ done.** The symmetric
     counterpart: an extern returning a record materializes a self-host struct.
     The host writes the record's fields into the return area at the **canonical
     memory layout**; the wrapper allocs a self-host struct (`type-id@0`,
     fields@+4 via `struct_field_off`), reads each field from its canonical
     offset, stores the struct's `struct_id` + each field, and returns its
     pointer. `is_extern_composite_ret` (now `mod`-aware) also matches a record
     return, giving it the trailing return-area pointer + `cabi_realloc`. The
     return area (canonical size) is over-allocated by 7 before 8-aligning so the
     next (struct) alloc doesn't overlap it — the self-host heap bump isn't
     aligned. **Sub-word integer result fields (`s8`/`s16`/`u8`/`u16`) are
     supported** via the same dual layout as the Go side: the canonical
     return-area packs them at their natural 1-/2-byte size + offset
     (`extern_canon_field_off` / `extern_canon_record_size`), which differs from
     the self-host struct's 4-byte slots, so the wrapper reads each with a
     width+sign-aware load (`extern_field_load_op`) and stores it into the wider
     slot; for word-only records the layouts coincide. **A nested-record field is
     also lifted, to *arbitrary depth*** (mirroring the Go side): the canonical
     area inlines every nested record's leaves at each level's alignment
     (recursive `extern_canon_field_align` / `extern_canon_field_csize` /
     `extern_canon_top_off` / `extern_canon_record_size_nested`), the gate recurses
     (`extern_record_nestable` / `extern_record_leaf_count`), and
     `extern_emit_record_fill` recurses the materialization — allocating one inner
     self-host struct per node and wiring child pointers into their parents
     bottom-up, with one `$inner<depth>` scratch local per nesting level
     (`extern_record_depth` sizes them). A single-leaf-via-nested result (returned
     by value) is rejected — only a single *scalar* field is
     `extern_record_ret_direct`-eligible (the by-value Direct wrapper can't
     reconstruct nesting). Gated by
     `TestSelfHostExternRecordResultCustomProvider` (a `make-point: func(a, b:
     s32) -> record { x, y: s32 }`, fields read back),
     `TestSelfHostExternRecordResultSubwordCustomProvider` (a `make-mix: func()
     -> record { a: s8, b: u16, c: s32 }` with `{-5, 300, 1000}` at canonical
     offsets 0/2/4), `TestSelfHostExternRecordResultNestedCustomProvider` (one
     level — a `make-line: func(...) -> record line { p: point, q: point }`; reads
     `l.p.x`/`l.q.y`), and `TestSelfHostExternRecordResultDeepNestedCustomProvider`
     (three levels — `outer { l: mid, r: mid }` / `mid { p: point, n: s32 }` /
     `point { x, y }`; reads `o.l.p.x` … `o.r.n`).
   - **Self-host port — sum-type (option/result) params — ✅ done.** An
     Option/Result `@import` parameter flattens to (disc, payload). A self-host
     enum is a heap box `[tag:i32 @0][payload:i32 @4]` (Some/Ok=0, None/Err=1) —
     identical to the Go backend — so the wrapper pushes the discriminant
     (the tag, remapped `1-tag` for option since canonical none=0/some=1
     reverses Fern's order; result matches) then the payload, with no alignment
     wall (it flattens to values). `extern_sum_param_supported` gates
     `Option[i32/u32]` / `Result` with i32/u32 arms (parsed from the type
     spelling). Gated by `TestSelfHostExternSumTypeParamCustomProvider`
     (Ok(42)→42, Err(5)→-5, Some(7)→7, None→-1).
   - **Self-host port — sum-type (option/result) results — ✅ done.** The
     symmetric counterpart: an extern returning an Option/Result materializes a
     self-host enum box. The host writes (disc:u8 @0, payload:i32 @4) into the
     return area; the wrapper over-allocs the 8-byte area by 7 before 8-aligning
     it (the self-host heap bump isn't aligned, so the next enum-box alloc must
     not overlap), calls the import with the scalar args + area pointer, then
     allocs a self-host enum box `[tag:i32 @0][payload @4]`, stores the remapped
     discriminant (`1-disc` for option via `extern_sum_param_is_option`, else
     `disc`) and the payload, and returns the box. `is_extern_composite_ret`
     (now also matching `extern_sum_param_supported`) gives the sum return the
     trailing return-area pointer + `cabi_realloc`. Gated by
     `TestSelfHostExternSumTypeResultCustomProvider` (`div(a,b) ->
     result<s32,s32>`, `half(n) -> option<s32>`, matched and checked).
   - **Self-host port — u8[] / boolean[] params — ✅ done.** A self-host `u8[]`
     stores each byte widened to a full 4-byte element slot, while the canonical
     `list<u8>` wants the bytes packed one-per-byte. So — unlike the wider numeric
     arrays, whose slot already matches the canonical element size and only need
     an aligned copy — a u8[] param needs a *byte-repacking* copy: the wrapper
     (`extern_byte_array_param` gate) allocs `len` bytes and writes each element's
     low byte contiguously (`i32.store8`; alignment is moot for 1-byte
     elements), then forwards `(buf, len)`. A `boolean[]` param is byte-identical
     (the self-host stores bools as 0/1 in 4-byte slots too), so the same gate +
     wrapper cover it — the self-host port of the Go-side bool[] param. Threaded
     through `has_extern_mem_param`, the `extern_imports` `(param i32)(param i32)`
     lowering, and the wrapper's param-decl / buffer-locals / call-forward
     branches alongside `extern_array_param_supported`. Gated by
     `TestSelfHostExternU8ArrayParamCustomProvider` (a
     `sum-bytes: func(b: list<u8>) -> s32` provider; `[10,20,12]` → 42) and
     `TestSelfHostExternBoolArrayParamCustomProvider` (a
     `count-true: func(b: list<bool>) -> s32` provider; `[true,false,true]` → 2).
   - **Self-host port — tuple params — ✅ done.** A self-host tuple is a heap
     block of N consecutive 4-byte slots (element i @ `i*4`, no type-id header —
     simpler than a record, whose fields start at +4), and the canonical tuple
     flattens to one i32 per element in order. So the wrapper
     (`extern_tuple_param_supported` gate, scoped to 2..16 i32/u32 elements)
     pushes `(i32.load (tuple + i*4))` per element — no copy or alignment wall
     (it flattens to values, like records). `tuple_type_elem_count` /
     `nth_tuple_type_elem` parse the `(A, B, …)` spelling. Threaded through
     `has_extern_mem_param`, the `extern_imports` per-element lowering, and the
     wrapper's param-decl / call-forward branches. Gated by
     `TestSelfHostExternTupleParamCustomProvider` (a
     `sum-pair: func(p: tuple<s32, s32>) -> s32` provider; `(10, 32)` → 42). With
     this, the self-host port reaches parity with the Go backend for the param /
     result composite shapes (string, numeric arrays, u8[], records, sum types,
     tuples).
   - **Single-field record results (direct return) — ✅ done (Go + self-host).**
     A single-field record result returns its field by value rather than through
     a return-area pointer (`ResultRecord.Direct` on the Go side); see the
     record-results entry above. The self-host port mirrors it: a new
     `extern_record_ret_direct` gate (a supported record result with exactly one
     field — every self-host record field is i32) makes the import return that
     i32 directly (no return area, `extern_imports` emits `(result i32)` instead
     of the trailing area pointer), and a dedicated `extern_wrappers` branch
     materializes the one-field self-host struct (`[type-id@0][field@+4]`) from
     the by-value result. Gated by
     `TestSelfHostExternSingleFieldRecordResultCustomProvider` (the same
     `make-wrapped: func(a: s32) -> record { v: s32 }` provider, run through the
     self-hosted backend).
   - **Self-host port — WIT `enum` params — ✅ done.** The mirror of the Go WIT
     enum param. A self-host payloadless user-enum value is a pointer to a
     `[struct_id@0]` box where `struct_id` is a *global* id (not the variant
     index), so the wrapper can't just push `[ptr+0]`: `extern_plain_enum_param`
     gates a `mod.enums` enum whose every variant is a 0-field struct, and
     `extern_plain_enum_disc` emits a select-chain mapping the loaded struct_id
     to the variant index (= the WIT discriminant), comparing against each
     variant's `struct_id`. Gated by
     `TestSelfHostExternEnumParamCustomProvider` (the same `pick: func(c: color)
     -> s32` over `enum color { red, green, blue }`, `Green` → 101).
   - **Self-host port — WIT `enum` results — ✅ done.** The mirror of the Go enum
     result. The self-host has no static sentinels — a payloadless enum value is
     a heap `[struct_id@0]` box (exactly what a bare `Green` produces) — so the
     wrapper maps the returned disc back to the matching variant's `struct_id`
     (`extern_plain_enum_sid`, the inverse of `extern_plain_enum_disc`) and
     stores it in a fresh 4-byte box (no new leak surface: identical to normal
     enum construction). `extern_needs_wrapper` now also fires for a plain-enum
     return (the raw import returns the disc i32 directly — not a composite-ret
     trailing area). Gated by `TestSelfHostExternEnumResultCustomProvider` (the
     same `choose: func(n: s32) -> color` returning `disc = n`; `rank(0/1/2)` →
     `Red/Green/Blue`).
   - **Self-host port — general `variant` params (uniform payload) — ✅ done.**
     The mirror of the Go variant param. A self-host payloaded variant value is a
     `[struct_id@0][payload@4]` box and a payloadless one is `[struct_id@0]`;
     `extern_variant_param_supported` gates a `mod.enums` enum whose every variant
     is a 0- or 1-field struct (the 1-field ones i32/u32, ≥1 payloaded). The
     wrapper maps `struct_id` → variant index for the disc (reusing
     `extern_plain_enum_disc`) and reads the payload at `struct_field_off(0)`
     (ignored garbage for a payloadless-case value). Gated by
     `TestSelfHostExternVariantParamCustomProvider` (the same `describe:
     func(s: shape) -> s32` over `variant shape { circle(s32), square(s32),
     empty }`: `Circle(7)`→7, `Square(7)`→70, `Empty`→999).
   - **Self-host port — general `variant` results (uniform payload) — ✅ done.**
     The mirror of the Go variant result. A uniform-payload variant returns
     indirectly (disc:u8 @0, payload @4); the self-host materializes a payloaded
     user-enum value as a `[struct_id@0][payload@4]` box, so the wrapper maps the
     disc to the matching variant's `struct_id` (`extern_plain_enum_sid`) and
     stores it with the payload. `extern_variant_result_supported` now matches
     the param gate (`extern_variant_param_supported`), so **payloadless variants
     are allowed** (a mixed `variant`, e.g. `{ some(s32), none }`): a payloadless
     case gets the same `[struct_id@0][payload@4]` box with an unused payload
     (tag-correct, match-correct — the payload is never read for it), as on the
     Go side. `is_extern_composite_ret` matches it (trailing return-area pointer).
     Gated by `TestSelfHostExternVariantResultCustomProvider` (a `classify:
     func(n: s32) -> grade` over `variant grade { low(s32), mid(s32), high(s32)
     }`, all-payloaded) and `TestSelfHostExternVariantResultMixedCustomProvider`
     (a `lookup: func(n: s32) -> opt-num` over `variant opt-num { some(s32),
     none }`, exercising a payloaded + a payloadless case).
   - Still rejected (next slices): the self-host port of the variant-payload
     generalisations (non-uniform / mixed-width / multi-field) — **blocked on the
     self-host backend itself**: its enum-box payload is a single i32 slot, and a
     payload-bearing variant `V(T)` is desugared (parser.fern) to a struct with a
     *single* `__ev` field — multi-payload variants drop the extra payloads and a
     multi-binding match is an `E015` arity error. So 64-bit / float / multi-field
     variant payloads need a self-host backend slice (real multi-payload variants +
     wide enum slots) *before* the extern marshalling can be ported. The Go-side
     **general multi-field variant join** (mixed-width / float fields,
     position-wise) is now **done** — see the multi-field entry above. So are the
     sub-4-byte-element `list<T>` *results* (`u8`/`boolean`) via a custom provider
     (the suspected `gMemRealloc` trap turned out to be already resolved — see the
     bool[]/u8[]-result entry above). Still deferred: sub-word (s8/s16) fields
     inside a multi-field variant arm (the tight 1-/2-byte canonical packing). The
     multi-component harness (`TestExternImportCustomProvider`) is the test
     vehicle for these.
   - **CLI integration — ✅ done (Go).** `fern -target wasm32-wasi` now compiles an
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
     `fern -target wasm32-wasi`. The self-hosted compiler implements the registry
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
   - **Slice 5e — numeric-array (`list<T>`) result export from Fern (Go). ✅
     Done.** The first composite EXPORT beyond strings: a Fern reactor
     `@export iota(): i32[]` (or any `u8[]`/`i32[]`/`f32[]`/`i64[]`/`f64[]`/…
     numeric element) lifts into a WIT `list<T>` result. The composer
     (`liftExport`) resolves the list element through the world
     (`WorldInterface.ListElemPrim`), emits the `list<elem>` defined type
     (`PutTypeSectionOneDefined` + `InnerTypeList`) → `listIdx`, builds the
     functype referencing it (`PutTypeSectionOneFuncResultIdx`, which
     sleb-encodes the result type index so `listIdx ≥ 64` is correct — unlike
     `PutTypeSectionOneFunc`'s single-byte append), and lifts with the same
     memory lift the string result uses (`PutCanonSectionLiftWithMemory`; both
     return indirectly via a `(ptr,len)` return area). The wasmbin wrapper
     (`buildExportListResultWrapper`) is the simpler sibling of the
     string-result one: a Fern numeric array is already contiguous at the
     canonical element stride (the count lives at `ptr-4`), so it reads the
     count and writes a 4-byte-aligned `[ptr,count]` return area with no
     SSO-normalize / copy. Byte-pinned by `TestPutTypeSectionOneDefined_Bytes`
     and `TestPutTypeSectionOneFuncResultIdx_Bytes` (the latter gates the
     `≥ 64` two-byte s33 case), and run end-to-end by
     `TestExportListResultRunsViaConsumer` (a Fern exporter returns
     `[10,20,30,40]`, a Fern consumer `@import`s `iota() -> list<s32>` and reads
     it back, linked + run under wasmtime).
   - **Slice 5e self-host — numeric-array (`list<T>`) result export. ✅ Done.**
     `wasm.fern`'s `build_export_wrapper` now emits the array-result branch. A
     self-host array value is the block base (`[len@0]`, elements at +8 in
     native slots), and the self-host heap isn't aligned, so — like the import
     array *param* wrapper, not the zero-copy Go side — the wrapper **copies**
     the elements into a fresh 8-aligned buffer (`extern_array_param_supported`
     element kinds: i32/u32/f32 slot 4, i64/u64/f64 slot 8) before writing the
     4-byte-aligned `[buf,len]` canonical return area the Go composer's memory
     lift reads. `extern_exports` surfaces the wrapper and `export_needs_heap`
     pins `__fern_alloc` for an array-result export (no `cabi_realloc` — the
     memory lift needs no realloc). u8/i16 arrays (slot ≠ canonical size) stay
     deferred, as on the import side. Gated by
     `TestSelfHostExportListResultRunsViaConsumer` (the self-host emits the
     `iota(): i32[]` exporter core, the Go composer lifts it, and a Fern consumer
     reads `[10,20,30,40]` back under wasmtime); the self-compile + printer +
     checker oracles stay green (the self-host source has no array `@export`, so
     the shared paths are unchanged). **P6 numeric-array result exports are now
     complete in both compilers.**
   - **Slice 5f — numeric-array (`list<T>`) parameter export from Fern (Go). ✅
     Done.** The inverse of the list-result export: a Fern reactor
     `@export sum(xs: i32[]): i32` takes a WIT `list<T>` parameter. The composer
     lifts it with the realloc lift (`PutCanonSectionLiftWithMemoryRealloc` — the
     canonical ABI materialises the incoming list in the core memory via
     `cabi_realloc`, then passes `(ptr,len)`), emitting the `list<elem>` param
     component type. `liftExport` was generalised to build the functype from
     per-slot valtype encodings (`PutTypeSectionOneFuncGeneral` — each param /
     result is a primitive byte *or* a sleb-encoded defined-type index), so a
     mix of scalar and list params/results encodes correctly; the string/list
     param + result detection moved to a shared `isStringOrList` helper. The
     wasmbin wrapper (`buildExportListParamWrapper`) rebuilds the length-prefixed
     Fern array from each canonical `(ptr,len)` (`alloc 4+len*stride`, store the
     count, `memory.copy` the elements) and calls the user func with the element
     pointer — strings forward their `(ptr,len)` directly, scalars pass through.
     A numeric-array param combined with a composite *result* is rejected for now
     (a later slice). Byte-pinned by `TestPutTypeSectionOneFuncGeneral_Bytes`;
     run end-to-end by `TestExportListParamRunsViaConsumer` (a Fern exporter sums
     an `i32[]`, a Fern consumer `@import`s `sum(xs: list<s32>) -> s32` and gets
     `100` from `[10,20,30,40]`, linked + run under wasmtime).
   - **Slice 5f self-host — numeric-array (`list<T>`) parameter export. ✅ Done.**
     `wasm.fern`'s `build_export_wrapper` gained the array-param case. The
     canonical realloc lift passes `(ptr,len)` with the elements contiguous at
     their stride; the wrapper rebuilds the self-host array (`__fern_arr_box`,
     `[len@0]`, elements at +8 in native slots — mirroring the import array-result
     wrapper) and passes its block base to the Fern func. The slot/local
     accounting was generalised so a string *or* numeric-array param takes two
     wrapper slots + one block local (`nblk`). `extern_exports` /
     `export_needs_heap` / `export_needs_realloc` now fire for an array param, and
     the `arr_helpers` (→ `__fern_arr_box`) emission is gated on
     `module_has_array_param_export`. Gated by
     `TestSelfHostExportListParamRunsViaConsumer` (the self-host emits the
     `sum(xs: i32[])` exporter core, the Go composer lifts it with the realloc
     lift, and a Fern consumer gets `100` from `[10,20,30,40]` under wasmtime);
     the self-compile / printer / checker oracles stay green (the self-host source
     has no array-param `@export`, so the shared paths are unchanged). **P6
     numeric-array param exports are now complete in both compilers.**
   - **Slice 5g — sum-type (`option` / `result`) result export from Fern (Go). ✅
     Done.** The first *structural* composite export beyond lists: a Fern reactor
     `@export half(n): Option[i32]` / `@export checked_div(a,b): Result[i32,i32]`
     returns a Fern enum box, lifted into a WIT `option` / `result`. The canonical
     sum flattens to `(disc, payload)` > 1 core value, so it returns indirectly
     (memory lift). The composer's `liftExport` `encodeSlot` gained an `allowSum`
     path (results only — a sum-type *param* would need a param wrapper) that
     emits `InnerTypeOption` / `InnerTypeResultOkErr` (prim arms, whose CValtype
     byte < 128 makes the uleb arm-encoding equal the valtype byte) and the
     `WorldInterface.OptionElemPrim` / `ResultArmPrims` accessors. The wasmbin
     wrapper (`buildExportSumTypeResultWrapper`) writes the `(disc:u8@0,
     payload@off)` return area — the discriminant remapped `1-tag` for option
     (Fern Some=0/None=1 ↔ canonical none=0/some=1; result's Ok=0/Err=1 matches) —
     handling **both** the **pair-form** `(tag, payload)` register return and the
     heap-box value-pointer return (`ir.ExternExport.ResultEnum`, resolved during
     lowering via `externEnumParamLayout`). Run end-to-end by
     `TestExportOptionResultRunsViaConsumer` (the remap path) and
     `TestExportResultResultRunsViaConsumer` (the no-remap path), each a Fern
     exporter + a Fern consumer matching every arm, linked + run under wasmtime.
     Scoped to single-scalar-payload Option/Result; the general-variant join and
     sum-type *params* are later slices. (Two findings surfaced, both orthogonal
     composer limitations deferred: a single exported interface can hold only one
     `@export` function, and a world can export only one interface — the existing
     tests never exercised either; the sum-type cases use one interface / one
     function each.)
   - **Slice 5g self-host — sum-type (`option` / `result`) result export. ✅ Done.**
     `wasm.fern`'s `build_export_wrapper` gained the sum-result branch. The
     self-host has no pair-form (an Option/Result function always returns a heap
     enum box `[tag:i32@0][payload:i32@4]`), so the wrapper reads the box and
     writes the canonical `(disc:u8@0, payload:i32@4)` return area — the disc
     remapped `1-tag` for option (`extern_sum_param_is_option`), straight through
     for result — over-allocating the 8-byte area by 7 and 8-aligning it (the
     heap bump isn't aligned), exactly the inverse of the import sum-result
     wrapper. `extern_exports` / `export_needs_heap` now fire for a sum-type
     result (`extern_sum_param_supported`); no `cabi_realloc` (memory lift only).
     Gated by `TestSelfHostExportOptionResultRunsViaConsumer` (remap) and
     `TestSelfHostExportResultResultRunsViaConsumer` (no remap) — the self-host
     emits the exporter core, the Go composer lifts it, and a Fern consumer
     matches every arm under wasmtime; the self-compile / printer / checker
     oracles stay green. **P6 sum-type result exports are now complete in both
     compilers.**
   - **Slice 5h — tuple (`(A, B, …)`) result export from Fern (Go). ✅ Done.**
     Another structural composite export: a Fern reactor
     `@export make_pair(a, b): (i32, i32)` returns a tuple value, lifted into a
     WIT `tuple`. A multi-element tuple flattens to > 1 core value, so it returns
     indirectly (memory lift). The composer's `liftExport` `encodeSlot` gained a
     tuple path (`InnerTypeTuple` + the `WorldInterface.TupleElemPrims` accessor,
     all-primitive elements). `ir.ExternExport.ResultTuple` carries the layout —
     reusing `externRecordResultLayout` (which already handles tuples), gated to
     `ast.TupleType` and flat (`externFieldsAllFlat`) so named records (which need
     the exported-instance type-export machinery) and nested tuples stay deferred.
     The wasmbin wrapper (`buildExportTupleResultWrapper`) copies each element
     from the Fern tuple value (`V+field.Offset`) into the canonical return area
     (`field.CanonicalOffset`). Run end-to-end by
     `TestExportTupleResultRunsViaConsumer` (a Fern exporter returns `(a+1, b*2)`,
     a Fern consumer reads `p.0`/`p.1`, linked + run under wasmtime).
   - **Slice 5h self-host — tuple (`(A, B, …)`) result export. ✅ Done.**
     `wasm.fern`'s `build_export_wrapper` gained the tuple-result branch. A
     self-host tuple value is a heap block of N consecutive 4-byte slots (element
     i @ `i*4`, no header), and the canonical `tuple<s32,…>` returns indirectly
     with element i at the same `i*4` offset (all i32), so the wrapper copies each
     element into the 8-aligned return area. `extern_exports` / `export_needs_heap`
     now fire for a tuple result (`extern_tuple_param_supported`, 2..16 i32/u32
     elements); no `cabi_realloc` (memory lift only). Gated by
     `TestSelfHostExportTupleResultRunsViaConsumer` (the self-host emits the
     exporter core, the Go composer lifts it, a Fern consumer reads `p.0`/`p.1`
     under wasmtime); the self-compile / printer / checker oracles stay green.
     **P6 tuple result exports are now complete in both compilers** — with this
     the structural composite-result set (list / option / result / tuple) is done
     both directions on both compilers; named-type results (enum / record /
     variant) await the exported-instance type-export foundation, and resources
     are Slice 6.
   - **Slice 6 — resource-typed exports**: lift an export taking/returning
     `own<T>` (the inverse of the import resource handling) — the piece that lets
     `wasi:http`'s `incoming-handler#handle` (which takes `own<incoming-request>`
     / `own<response-outparam>`) become a plain `@export`, unblocking retiring
     the built-in HTTP world.
     - **Slice 6a — handle export PARAMS (composer, Go). ✅ Done.** The composer
       lifts an `@export` whose parameter is a handle (`own<R>` / `borrow<R>`) to
       an *imported* resource. A handle is an i32 at the canonical ABI, so the
       Fern core function is unchanged (the P5 `resource`/`own`/`borrow`
       vocabulary already erases to i32) — the work is composer-side: the decoder
       now records each interface type slot's WIT name (`LocalTypeNames`) so
       `WorldInterface.HandleResource` can recover a handle param's resource name;
       `liftExport` surfaces that imported resource (a pre-pass `aliasType`, as
       the `[resource-drop]` path does — sharing `g.surfaced`) and references it
       from an `own`/`borrow` defined type (`InnerTypeOwn` / `InnerTypeBorrow`) in
       the export functype (no-opts lift, no memory). `resourceInst` maps each
       imported resource name → instance index. Gated by
       `TestExportResourceHandleParamComposes` (a Fern reactor
       `@export("local:test/handler", "handle") on_request(t: borrow Thing): u32`
       over an `@import`ed `resource Thing` composes; `wasm-tools validate` + the
       component WIT declares `borrow<thing>`). Running it needs a resource-
       provider harness (the host/another component constructs the resource and
       calls the export) — a later slice, exactly as the scalar export slice
       first shipped validate-only. **Next**: the runnable handle-param path (a
       resource-provider harness), then own/borrow handle *results*, then the
       full `wasi:http` `incoming-handler#handle` (two `own<...>` params + the
       handler body calling resource methods via `@import` externs).
     - **Slice 6b — void export taking MULTIPLE handle params (composer, Go). ✅
       Done.** Extends 6a to the exact `incoming-handler#handle` shape:
       `func(own<incoming-request>, own<response-outparam>)` — **no result**, two
       handle params. A void function is encoded as a named-results list of
       length zero, which `liftExport` previously rejected; it now lifts it via
       the new `PutTypeSectionOneFuncGeneralVoid` (functype with `0x01` named +
       `vec(0)` results), surfacing each handle param's imported resource as in
       6a. Gated by `TestExportHandleVoidComposes` (a Fern reactor
       `@export("local:test/handler","handle") on_request(r: borrow Req, o: borrow
       Resp): void` over two `@import`ed resources composes; `wasm-tools validate`
       + the WIT declares the void two-handle export). Uses `borrow` (never
       auto-dropped); the `own`-consume path needs `[resource-drop]` wired in a
       reactor export (`ComposeExportsFromWorld` currently rejects it) — the next
       slice — together with the handler body calling resource methods via
       `@import` externs (the P5 import side), which then composes the real
       `wasi:http` handler.
     - **Slice 6c — `[resource-drop]` in a reactor export (composer, Go). ✅
       Done.** A handler that holds an owned handle (`var t: own Thing =
       new_thing();`) auto-drops it at scope exit, so the reactor core imports
       `[resource-drop]thing`. `ComposeExportsFromWorld` no longer rejects that —
       it classifies it as a `gDrop` and surfaces the resource + threads its type
       index into the canon `resource.drop`, the same additive surfacing
       `ComposeFromWorld` does (shared with the export lifts via `g.surfaced`).
       Gated by `TestExportOwnedHandleDropComposes` (a reactor `@export handle()`
       creates a `thing` via an `@import`ed `[constructor]thing` and lets it drop;
       the core imports `[resource-drop]thing`, the composed component validates
       and prints a `resource.drop`). With 6a/6b/6c the composer handles the full
       `incoming-handler#handle` surface — handle params (own/borrow), void
       results, and owned-handle drops; what remains for the real `wasi:http`
       handler is a program that calls the request/response **resource methods**
       (`@import` externs — the P5 import side already lowers these) and a
       run-harness (the host drives `incoming-handler`).
     - **Slice 6 self-host parity — ✅ Done (test-only).** The composer is Go-only
       (the self-host composer was retired), so the self-host's role for 6a/6b/6c
       is emitting a compatible `@export` *core*: handles erase to i32, void is a
       normal void function, and an owned local handle's auto-drop emits the
       `[resource-drop]` import (the P5 self-host drop port). No `wasm.fern` change
       was needed — `TestSelfHostExportResourceHandleComposes` confirms the
       self-hosted compiler emits a void handler core taking a `borrow Thing`
       param that also constructs + drops a local `own Thing` (so the core carries
       `[resource-drop]thing`), and the Go composer lifts it: surfaces the handle
       param's resource, wires the drop, and the component validates with
       `borrow<thing>` + a `resource.drop`.
     - **Slice 6 capstone — the REAL `wasi:http/incoming-handler` exports +
       composes. ✅ Done (compose-gated).** The headline bring-your-own-WIT
       demonstration: a Fern reactor `@export("wasi:http/incoming-handler@0.2.0",
       "handle") on_request(request: own IncomingRequest, response_out: own
       ResponseOutparam): void` compiles, and the world-driven composer produces a
       valid `wasi:http` component against the repo's actual `wasi:http` WIT (the
       `cmd/fern/wit` `http` world, supplied as *input* via
       `wasm-tools component embed -w http` — **not** the compiler's embedded HTTP
       world, and with no HTTP-specific knowledge in `ComposeExportsFromWorld`).
       The composer surfaces `incoming-request` + `response-outparam` from the
       imported `wasi:http/types` instance and lifts the void two-`own`-handle
       export across the full preview-2 + http world prefix. Gated by
       `TestExportWasiHttpIncomingHandlerComposes` (`wasm-tools validate` + the
       component WIT exports `wasi:http/incoming-handler@0.2.0`). This proves the
       compile+compose path end-to-end; what remains for a *running* server is a
       response-producing handler body (calling the request/response resource
       methods via `@import` externs — the P5 import side already lowers them) and
       a `wasmtime serve` run harness.

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

### P6 — toward a *running* `wasi:http` handler (post-capstone)

The capstone proved a do-nothing `incoming-handler#handle` compiles + composes
against the real `wasi:http` WIT. The increments toward a handler that actually
produces a response:

- **Handler body calls `wasi:http` resource constructors. ✅ Done (compose-gated).**
  The exported `incoming-handler#handle` body calls `[constructor]fields` and
  `[constructor]outgoing-response` (`@import` externs returning / taking owned
  handles) and lets the constructed response auto-drop — integrating the P5
  import resource-method path INSIDE a P6 resource-handle export. The composer
  wires the constructor imports (handle in/out, no memory) + the owned-handle
  `[resource-drop]` and lifts the void two-handle export, composed against the
  real `wasi:http` world. Gated by
  `TestExportWasiHttpHandlerCallsConstructorsComposes` (`wasm-tools validate` +
  the component exports `wasi:http/incoming-handler`). No new marshalling — it's
  the P5 import + P6 export paths meeting.
- **A `wasi:http` handler RUNS under `wasmtime serve`. ✅ Done — the running-server
  capstone.** A bring-your-own Fern handler now answers a real HTTP request with
  the 200 it sets, with NO embedded HTTP world. The handler calls the response
  primitives — `[constructor]fields`, `[constructor]outgoing-response`,
  `[static]response-outparam.set` — as `@import` externs, and is composed against
  a minimal proxy world (`import wasi:http/types; export
  wasi:http/incoming-handler`, which pulls in exactly the transitive io/clocks
  proxy imports `wasmtime serve` links — not filesystem/sockets). **No compiler /
  composer change was needed**: `response-outparam.set`'s `result<own<outgoing-
  response>, error-code>` param flattens to 9 core values
  `[i32 i32 i32 i32 i64 i32 i32 i32 i32]` (the i64 from error-code's `option<u64>`
  arm, error-code carries heap → `Classify` = `KindMem`), and the existing `gMem`
  trampoline lowers it straight off the core import's own params. The handler
  declares `set` with those 9 flattened params and passes `Ok` (disc 0, the
  response handle, the rest zero). Gated by `TestExportWasiHttpHandlerServes`
  (`wasmtime serve` the composed component, `GET /` → 200) +
  `TestExportWasiHttpHandlerSetResponseComposes` (`wasm-tools validate`).
- **Ergonomic helpers + a richer (status-setting) response. ✅ Done.** The
  9-param `response-outparam.set` flattening is hidden behind a pure-Fern helper
  `set_response_ok(out, resp)` (no compiler change — the Ok-wrap is just a
  one-line wrapper over the raw extern), alongside a `new_response()` helper. A
  handler then reads as: `var resp = new_response(); set_status(resp, 404);
  set_response_ok(out, resp);`. `[method]outgoing-response.set-status-code`
  (`status-code` = `u16` param + `result<_,_>` return, both flattening to i32) is
  a plain scalar method extern — declared `set_status(resp: borrow
  OutgoingResponse, status: i32): i32` (the i32 result is the result disc, which
  the handler ignores). Gated by `TestExportWasiHttpHandlerStatusServes`
  (`wasmtime serve`, `GET /` → **404**). The serve harness is now a shared helper
  (`serveHttpHandlerStatus` / `buildHttpHandlerComponent`) across the serve tests.
  - **Still open (genuine follow-ups, not blockers):**
    1. **Reusable cross-module HTTP lib.** The externs + helpers can't yet live in
       an imported module because **`pub resource` is unsupported** (`pub` rejects
       `resource`; resource types are module-local). A `pub resource` language
       feature (parser/checker + modload aliasing of exported resource types)
       would let a 5-line handler `import` an `http` lib. Until then the externs
       live in the handler module.
    2. **Response bodies — a handler writes the body. ✅ Done.** `outgoing-
       response.body() -> result<own<outgoing-body>>` and `outgoing-body.write()
       -> result<own<output-stream>>` lower because a resource handle is a valid
       single-scalar `result` payload (`valtypeFor` maps `ast.HandleType` to i32),
       and `output-stream.blocking-write-and-flush(list<u8>) -> result<_,
       stream-error>` now lowers too: a `u8[]` parameter **combined with** a
       composite (option/result) result is handled by a merged wrapper —
       `buildExternMemParamWrapper` gained an optional result layout, allocating
       the canonical return area up front, passing it as the trailing retptr,
       normalizing the mem param(s), then reading the area into a Fern enum box
       (the area-read logic is now the shared `appendEnumResultAreaToBox`). The
       variant `stream-error` is read **discriminant-only** by modeling the return
       as `Result[i64, i64]`, whose 16-byte same-width-scalar area safely covers
       the canonical `result<_, stream-error>` (~12 bytes) — no under-allocation.
       Gated by `TestExportWasiHttpHandlerBodyWriteServes`: a handler writes "hi"
       and `wasmtime serve` returns **200 + body "hi"**.
    2a. **`outgoing-body.finish` — the full response path. ✅ Done.** A handler now
       writes the body AND calls `[static]outgoing-body.finish(own<outgoing-body>,
       option<own<trailers>>) -> result<_, error-code>` to seal it. Two pieces:
       (i) the `option<own<trailers>>` param lowers as-is (`Option[own Trailers]`,
       passed `None`) — a handle is a single-scalar option payload; (ii) the wide
       `error-code` result. `error-code` is a 39-case variant whose canonical
       return area exceeds the 16 bytes a same-width-scalar `Result` models, so
       the result-return wrapper now **floors the canonical return area at 64
       bytes** — the canonical-ABI retptr bound the embedded path already relies
       on (`wasi_http.go`: "Each canonical-ABI retptr fits in 64 bytes"). The box
       is unchanged; only the scratch area grows, so the host can never overrun it
       however wide the real variant is. The handler reads `finish`'s result
       discriminant-only (`Result[i64, i64]`). wasi:http traps finishing a body
       with a live child stream, so the handler drops the output-stream first via
       a manual `[resource-drop]output-stream` `@import` (the P5 manual-drop path;
       match-bound handles aren't auto-dropped — the move analysis can't tell a
       handle owned by a parent resource, like a response's body, from a leaked
       one, so auto-dropping them is unsound). Gated by
       `TestExportWasiHttpHandlerFinishServes`: writes "hi", finishes the body,
       serves **200 + body "hi"**. Reading a `result<_, variant>`'s *error
       details* (vs. discriminant-only) still needs faithful variant modelling — a
       follow-on, not a blocker.
    3. **A composer Ok-wrap** (so even the raw `set` extern could take just the
       response handle) remains possible but is now lower-value, since the Fern
       helper already gives the clean call site.

> **Note on the embedded HTTP path:** `-target wasm32-wasi-http`
> (`emitIncomingHandlerExport` / `compose_http.go` / `wasi_http.go`) is still the
> live CLI feature and is **not** dead code — the bring-your-own path above is
> currently test-only. A future consolidation could migrate `-target wasm32-wasi-http`
> onto the generic world-driven composer once the body/header marshalling and a
> `pub resource` HTTP lib exist; until then both coexist.
