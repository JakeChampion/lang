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

1. **P1 — decode the component-type binary.** Add a decoder for the
   `component-type` payload (option 1); round-trip
   `componenttype/fern.bin` → structured world → re-encode == original.
   Pure internal, no language changes. *De-risks ingestion.*
2. **P2 — drive the existing WASI set from the decoded world.** Replace the
   hand-written `Wasi*InstanceTypeBody` + `knownPreview2Imports` registry
   with type bodies/classification **derived** from the decoded `fern` world;
   gate on reproducing every current `component_full_io_*` component
   byte-for-byte. *Proves Gap A without new language surface.*
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
- **Self-host parity.** Every phase eventually needs a self-host port
  (`examples/self_host/`). The Go side can lead, but resources/exports will
  be sizeable there too.

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
