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
