# Toolchain self-hosting plan

Goal: eliminate every external compiler / assembler / linker / wasm
helper the driver currently shells out to, replacing each with a
Lang-native implementation. After all phases land, building a Lang
program with `fern -o out src.fern` requires **no binary on `$PATH`
other than the Lang compiler itself**.

This is a deliberate alternative to the position taken in
`ROADMAP-AND-SELF-HOSTING.md`, which argues for keeping a thin
external bootstrap (clang / lld / wasm-tools) indefinitely. That doc
is right that the bootstrap is the cheap pragmatic answer. This doc
is the plan for the people who want to do it the hard way anyway.

## Current shell-outs (the honest map)

Updated 2026-05-20 — the wasm row has shrunk since the initial draft,
because the Go-side wasmbin landing already replaced several
`wasm-tools` calls inside the compiler (see "Go-side baseline" below).
Linux + Darwin rows are unchanged.

| Target          | Driver fn                | External tool(s)                  | What it does                                  |
|-----------------|--------------------------|-----------------------------------|-----------------------------------------------|
| `arm64-linux`   | `link` @ `cmd/fern/main.go:528`        | `aarch64-linux-gnu-gcc` (→ `as` + `ld`) | Assemble `.s`, link static ELF                |
| `x86_64-linux`  | `link` @ `cmd/fern/main.go:528`        | `x86_64-linux-gnu-gcc` (→ `as` + `ld`)  | Same, for x86-64                              |
| `arm64-darwin`  | `linkDarwin` @ `cmd/fern/main.go:460`  | `clang` (+ `lld` on Linux hosts)        | Assemble `.s`, link Mach-O, ad-hoc codesign   |
| `wasm`          | `emitPreview2ComponentFromCoreBytes` @ `cmd/fern/main.go:651` | `wasm-tools component new --adapt` | Splice preview-1 → preview-2 adapter into a Component-Model envelope around the (already-binary) core module |

So the "replace clang / lld / wasm-tools" framing in the prior chat
undersold the work: the Linux backends also depend on an external
toolchain (`gcc`, which is itself a frontend for `as` + `ld`). To be
*truly* free-standing we need replacements for **all five** roles:
ARM64 assembler, x86-64 assembler, ELF linker, Mach-O linker
(with codesigning), WAT-to-binary encoder + Component Model writer.

### Go-side baseline (already shipped)

Independent of this doc's Lang-stdlib plan, a Go-side wasm pipeline
landed inside the compiler that's already retired several
`wasm-tools` calls:

- `internal/codegen/wasmbin` walks codegen IR straight to core-wasm
  binary bytes — no WAT text, no `wasm-tools parse` round-trip. The
  old WAT backend (`internal/codegen/wasm/`) was deleted.
- `internal/wasm/componenttype.Embed` writes the `component-type`
  custom section for the `lang` and `http` worlds, replacing
  `wasm-tools component embed`.
- Supporting Go packages: `internal/wasm/{leb128,inst,module,
  encode,imports,memory,numeric,convert,sections}`.

This is parallel to the Lang-stdlib effort the rest of the doc
tracks; it doesn't *satisfy* Phase 1's "Lang-native" goal but it
does shrink the day-to-day external-tool dependency to a single
remaining shell-out (the row above). When the Lang-stdlib Phase 1
lands, the Go-side path becomes the fallback / debugging tool.

## Order of attack (smallest → largest)

| Phase | Deliverable                                    | Replaces                         | Rough size  |
|-------|------------------------------------------------|----------------------------------|-------------|
| 1     | WAT-to-binary encoder in Lang                  | `wasm-tools parse`               | Small       |
| 2     | Component Model writer in Lang                 | `wasm-tools component embed` + `new` | Small-medium |
| 3     | ELF object writer + static linker for arm64-linux | `aarch64-linux-gnu-gcc`        | Medium      |
| 3b    | Same for x86_64-linux (mostly a relocation table swap) | `x86_64-linux-gnu-gcc`   | Small (after 3) |
| 4     | Mach-O object writer for arm64-darwin          | the assembler half of `clang`    | Medium      |
| 5     | Mach-O linker + ad-hoc codesigning             | `lld` + the linker half of `clang` | Large     |

Order is chosen so each phase's tests can use the prior phases'
output. Phase 1 is the natural starting point: well-specified binary
format, tiny test surface, no platform quirks.

## Prerequisites that apply to every phase

Before any phase can land:

1. **Lang needs a bytes-writing story.** The driver currently relies
   on Go's `os.WriteFile`. We need a Lang stdlib API equivalent —
   `fs.write(path, bytes)` plus a mutable byte-builder type. If
   that doesn't exist yet, build it first; without it none of the
   new emitters can produce their output file.
2. **Lang needs `u8` / `u16` / `u32` / `u64` little-endian write
   helpers** (`bytes.put_u32_le`, etc.) on the byte-builder. ELF,
   Mach-O, and wasm all serialise as little-endian integer streams.
3. **A LEB128 encoder** (signed and unsigned). Wasm uses LEB128
   everywhere; ELF and Mach-O do not.
4. **A SHA-256 implementation** (only needed in Phase 5 for Mach-O
   ad-hoc codesigning). Pure Lang, ~200 lines, well-specified.

Land 1–3 as part of Phase 1's preparatory work. Land 4 only when
Phase 5 starts.

---

## Phase 1 — WAT-to-binary encoder

Scope: take the WAT text currently produced by
`internal/codegen/wasm/wasm_ir.go` (entry point
`EmitFromIRWithOptions`) and emit a wasm-1.0 binary module directly,
skipping the round-trip through `wasm-tools parse`.

The simplest implementation does **not** parse arbitrary WAT — it
emits the binary directly from the same IR the WAT emitter walks. We
keep the WAT path for debugging (it's useful as a human-readable
intermediate) but the production driver stops feeding it to
`wasm-tools`.

### Spec

- WebAssembly Core 1.0 binary format:
  <https://webassembly.github.io/spec/core/binary/index.html>
- Section ordering: type, import, function, table, memory, global,
  export, start, element, code, data. Each section is `id:u8 +
  size:leb + body`.
- Function bodies: `locals:leb + (count:leb + type:u8)* +
  instr* + 0x0B`.

### Files to create

- `stdlib/wasm/encode.fern` — pure encoder:
  module → bytes. Mirrors the structure of `wasm_ir.go` but writes
  bytes instead of text.
- `stdlib/wasm/leb128.fern` — `put_uleb(buf, u64)` and
  `put_sleb(buf, i64)`.

### Driver wiring

Replace the `wasm-tools parse prog.wat -o prog.wasm` call at
`cmd/fern/main.go:604` with a direct call to the new encoder.
Keep WAT emission as an opt-in debug output behind `-emit-wat`.

### Exit criteria

- Every existing `internal/e2e/wasm_*_test.go` passes with the new
  encoder in place of `wasm-tools parse`.
- Byte-for-byte equivalence is **not** required; semantic equivalence
  under `wasmtime run` is.
- `wasm-validate` (from wabt) on the produced binary reports zero
  errors. Add this as a CI check while the encoder is new; remove it
  once the e2e tests are trusted.

### Out of scope

- WAT-as-input parsing. We're emitting from IR, not from WAT text.
  If someone wants `lang wat2wasm` later, that's a separate tool.

### Progress

- **LEB128 encoders shipped** in `internal/stdlib/std/wasm/leb128.fern`
  with `uleb_u32` / `uleb_u64` / `sleb_i32` / `sleb_i64` plus
  `uleb_size_u32` / `uleb_size_u64`. Vector-tested against the
  Wikipedia LEB128 reference examples and the wasm-spec edge cases
  (bit-6 transitions, multi-byte negatives, u32/u64/i64 widths) under
  `internal/e2e/wasm_e2e_test.go` (TestWASMLeb128*). Pure Lang —
  takes a `u8[]` and appends; no I/O. Not wired into the driver
  yet, per the "Lang code only, defer running it" decision.
- **Binary-container primitives shipped** in
  `internal/stdlib/std/wasm/encode.fern`: `put_module_header`
  (magic + version), `put_u32_le` (the one fixed-width integer the
  wasm binary uses), `put_name` (uleb-prefixed UTF-8),
  `put_section` (id + uleb size + body), `put_func_type` (the type
  section's per-entry encoding), and `valtype_*` / `section_*`
  byte-constant helpers. Tests
  (`TestWASMEncodePreamble`, `…NameAndSection`, `…MinimalModule`)
  build a complete 16-byte wasm module from scratch and compare
  every byte against the spec-reference encoding.
- **Control-flow + constant + variable instruction encoders
  shipped** in `internal/stdlib/std/wasm/inst.fern`. The subset
  covers: i32/i64/f32/f64 consts; local/global get/set/tee; the
  block / loop / if / else / end / br / br_if / return / call /
  call_indirect family; drop and select; unreachable and nop;
  plus the code-section helpers `put_function_body`,
  `put_locals_empty`, and `put_locals_one_group` that wrap a
  fully-assembled body with its uleb size prefix. Tests
  (`TestWASMInstConsts`, `…Variable`, `…Control`, `…FunctionBody`)
  vector-check every opcode against the spec, including the
  i32.const sleb bit-6 boundary (63 fits in 1 byte; 127 needs a
  continuation byte to disambiguate from -1).
- **Numeric instruction encoders shipped** in
  `internal/stdlib/std/wasm/numeric.fern`: every wasm numeric
  opcode that fits the "single byte, no immediate" shape —
  i32 / i64 unary (clz, ctz, popcnt, eqz), compare (eq, ne, lt,
  gt, le, ge with signed / unsigned variants for integers),
  arithmetic (add, sub, mul, div, rem), bitwise (and, or, xor),
  shift / rotate (shl, shr_s, shr_u, rotl, rotr); plus f32 / f64
  compare (eq, ne, lt, gt, le, ge), unary (abs, neg, ceil, floor,
  trunc, nearest, sqrt) and binary (add, sub, mul, div, min, max,
  copysign). ~96 functions, each a one-line `return
  buf.push(<opcode>);`. Tests
  (`TestWASMNumeric{I32, I64, Float, Compose}`) spot-check each
  family with a wider compose test that builds the canonical
  `local.get 0; local.get 1; i32.add` body.
- **Memory instruction encoders shipped** in
  `internal/stdlib/std/wasm/memory.fern`: every wasm load / store
  variant including the narrow widths (i32.load8_s, i64.store16,
  etc.), all carrying the standard `memarg` immediate (uleb
  align + uleb offset); plus `memory.size` / `memory.grow` with
  their reserved memidx byte. ~25 functions. Tests
  (`TestWASMMemoryLoad`, `…Store`, `…SizeGrow`) walk both the
  full-width and narrow-width blocks plus the multi-byte uleb
  offset case (offset=128 forces a 2-byte uleb).
- **Conversion / reinterpret / sign-extension encoders shipped**
  in `internal/stdlib/std/wasm/convert.fern`: integer-width
  conversion (i32.wrap_i64, i64.extend_i32_{s,u}), float-to-int
  trapping truncation (8 variants across i32/i64 × f32/f64 × s/u),
  int-to-float conversion (8 variants), float-width
  demote / promote, the reinterpret family (i32↔f32 / i64↔f64
  bit-pattern aliases), and the sign-extension extension
  (i32.extend{8,16}_s, i64.extend{8,16,32}_s). ~28 functions, all
  single-byte opcodes — same one-line shape as numeric.fern.
  Tests (`TestWASMConvertIntWidth`, `…FloatInt`) walk every
  variant.
- **Section composers shipped** in
  `internal/stdlib/std/wasm/sections.fern`: one function per
  section that takes the section's logical input (lists of
  typeidxs, parallel name/kind/idx arrays for exports, pre-
  wrapped bodies for code, etc.) and emits the complete
  `id + size + body` envelope. Covers type, function, memory,
  export, start, code, and data sections plus the four
  `export_{func,table,memory,global}` kind constants. Import and
  element are intentionally skipped — imports carry a four-variant
  union descriptor (func / table / mem / global) that warrants
  its own helper module, and the existing wasm backend doesn't
  exercise element segments. Tests
  (`TestWASMSections{Function, StartMemory, Export, TypeCode,
  Data}`) cover empty-vector cases, multi-byte uleb fields,
  flag=0 vs flag=1 memory limits, and a code section that wraps
  an inst.put_function_body-produced body end-to-end.
- **Import + global section composers shipped** in
  `internal/stdlib/std/wasm/imports.fern`. Imports needed their
  own module because the import descriptor is a four-variant
  union (func / table / memory / global) — the section composer
  takes a `desc_bodies: u8[][]` parallel array, and each variant
  has a builder (`import_desc_func / table / memory / global`)
  that produces the descriptor bytes the caller threads through.
  Global section similarly takes parallel valtypes / muts /
  init_exprs arrays so the section composer stays flat. Constants
  shipped: `import_func / table / memory / global`,
  `mut_const / mut_var`, `reftype_funcref / externref`. Tests
  (`TestWASMImports{Func, DescBuilders, GlobalSection}`) walk
  every variant.
- **Top-level module builder shipped** in
  `internal/stdlib/std/wasm/module.fern`. Bundles every section
  composer behind a single `build(m: Module): u8[]` entry point:
  the caller populates a `Module` struct field-by-field
  (returning `module_new()` for an empty starting point), and
  build emits the preamble followed by every populated section in
  spec-required order (type → import → function → memory → global
  → export → start → code → data). Empty sections are skipped.
  Tests (`TestWASMModule{Empty, Minimal, SectionOrder}`) include
  the strongest test yet: TestWASMModuleMinimal builds a complete
  37-byte "function returning 42" module end-to-end and verifies
  every byte against a hand-computed reference, exercising every
  section composer and several opcode encoders in one pass.
- **End-to-end validation against wasm-tools shipped** in
  `internal/e2e/wasm_e2e_test.go::TestWASMModuleValidatesUnderWasmTools`.
  A Lang program builds the minimal "function returning 42"
  module via `module.build`, prints the 37 bytes as space-
  separated decimals; the Go test parses them back, writes them
  to disk, and pipes the file through `wasm-tools validate` (must
  pass) and `wasm-tools print` (output must contain `(type`,
  `i32.const 42`, and `main`). This is the strongest correctness
  gate on the std/wasm stack: a Lang-produced byte sequence is
  now confirmed to parse as a valid wasm module under an
  independent reference tool, not just match a hand-computed byte
  vector.
- Remaining Phase 1 work: the IR-walking entry point that turns
  a codegen IR program into a populated Module, and the driver-
  wiring step that deletes the `wasm-tools parse` call at
  `cmd/fern/main.go:604`. Saturating-truncate ops and bulk-memory
  ops (both 0xFC-prefixed) are deliberately out of scope — the
  production backend doesn't lean on them. Element section is
  also deliberately deferred — the existing wasm backend doesn't
  exercise table-element segments.

---

## Phase 2 — Component Model writer

### Progress (refreshed 2026-05-20)

The Lang-stdlib component encoder (`internal/stdlib/std/wasm/component.fern`)
is now structurally complete for the WASI-style component wrapping
the production driver needs. Every section the Component Model
binary format defines has a composer, and the canonical
"core module + WASI imports → component" pipeline has a one-call
high-level helper. What's left for Phase 2's stated goal —
retiring `wasm-tools component new --adapt` from
`cmd/fern/main.go` — is **driver wiring**, not new encoder work.

#### What's shipped

Section composers (each verified against `wasm-tools parse`
output; many also exercised end-to-end via `wasm-tools validate`
+ `wasmtime run --invoke`):

| Section | Composer(s) |
|---------|-------------|
| Preamble | `put_component_header` |
| 0 (custom) | `put_component_type_section` |
| 1 (core-module) | `put_core_module_section` |
| 2 (core-instance) | `_instantiate`, `_instantiate_with_one_instance_arg`, `_instantiate_with_instance_args`, `_from_one_func_export`, `_from_func_exports` |
| 3 (core-type) | `put_core_type_section_one_func` |
| 6 (alias) | `put_alias_section_core_export_func` / `_core_exports` (multi-sort), `put_alias_section_instance_export_func` |
| 7 (type) | `put_type_section_one_func{,_no_param,_no_result}`, `_funcs` (multi-functype), plus one composer per defvaltype form: resource, list, option, tuple, result_ok_err, enum, flags, record, variant, own, borrow, plus instance types (single + multi func exports, with-result variants) |
| 8 (canon) | lift (no_opts, mem_realloc, mem_realloc_post_return, mem_realloc_encoding) + multi-lifts no-opts; lower (no_opts, mem_realloc) + multi-lowers no-opts; resource_{new,drop,rep} single + multi |
| 9 (start) | `put_start_section_no_args_no_results` |
| 10 (import) | `put_import_section_one_func` / `_funcs` (multi), `put_import_section_one_instance` |
| 11 (export) | `put_export_section_one_func` / `_funcs` |

High-level helpers — collapse a recipe of section composers into
one call:

- `build_lifted_export_component[_with_params]` — wrap a core
  module's exported function as a component-level export.
  Exercised under `wasmtime run --invoke` for a real
  `add(3, 4) = 7` round-trip.
- `build_wasi_imported_component[_multi]` — wrap a core module
  that calls one or more WASI host functions. Threads the full
  type / import / alias / canon-lower / core-instance pipeline
  for each WASI import.

Supporting byte-constant exports: `section_*`, `core_sort_*` (7),
`cvaltype_*` (13 component primitives), `canonopt_*` (6).

#### Bridge encoder + first driver wiring (added 2026-05-20)

`internal/wasm/component` now ships a Go-side port of the most-
used Lang-stdlib composers — `PutComponentHeader`,
`PutCoreModuleSection`, the lift-export composers, the WASI-
import composers, `BuildLiftedExportComponent`, and
`WrapWasiImported`. The two implementations are pinned
byte-equivalent by `TestWASMComponentGoLangByteEquivalence`.

`cmd/fern` accepts a new `-component-wrap` flag (with
`-target wasm-bin`, no `-wasi-adapter`) that wraps the core wasm
output as a self-contained preview-2 component via the Go encoder
— no `wasm-tools component new --adapt` shell-out. Lifts `main`
as a component-level u32-returning function.

End-to-end exit code 42 demo (covered by
`TestCmdLangComponentWrap`):

    $ fern -target wasm-bin -component-wrap -o min.wasm min.fern
    $ wasmtime run --invoke 'main()' min.wasm
    42

#### What's NOT yet shipped

- **`-target wasi-http` default-path wiring.** `-target wasm`
  without `-wasi-adapter` now flows through the Go-side
  preview-2 encoder when the program's imports are all
  preview-2-migrated (wasi:cli/exit, wasi:random/random,
  wasi:clocks/monotonic-clock); unsupported imports surface a
  clear error pointing at `-wasi-adapter`. `-target wasi-http`
  still requires the adapter — its imports
  (`wasi:http/types` + `wasi:io/streams`) use list / record /
  resource shapes the Go encoder doesn't cover yet. Migrating
  the http path to no-adapter requires the canonical-ABI
  list / resource pieces below.
- **Preview-2 import migration.** `internal/codegen/wasmbin/wasi.go`
  still emits preview-1 imports (`fd_write`, `path_open`, etc.).
  Migrating each to its preview-2 equivalent lifts more programs
  into the class that `-component-wrap` can handle. **What's
  shipped so far:**
  - `proc_exit` → `wasi:cli/exit@0.2.0.exit` under
    `BuildOptions.Preview2WASI` (opt-in; default still emits
    preview-1). The core-wasm signature is unchanged ((i32) → ())
    so `__fern_exit`'s call site stays untouched.
  - `component.WrapWasiImportedWithExport` composes the
    import-wiring with a lifted-export pipeline so a core module
    that both imports WASI AND exports `main` gets wrapped in a
    single pass.
  - `-component-wrap` driver routes through
    `WrapWasiImportedWithExport` when the core module's imports
    are known preview-2 ones (currently only
    `wasi:cli/exit@0.2.0.exit`). Other imports still hit the
    "unrecognised imports" error pointing at `-wasi-adapter`.
  - **Result-type encoding.** The instance-type composer now
    supports inner defvaltype declarations (via
    `PutTypeSectionInstanceWithInnerTypesAndOneFuncNoResultExport`).
    Param valtype bytes < 0x73 read as inner-scope typeidxs after
    the binary parser; `InnerTypeResultEmpty` ships the
    `result<_, _>` defvaltype body. The `wasi:cli/exit` import
    in the driver registry uses this to match wasmtime's
    canonical-ABI signature, so a Lang `exit(0)` component
    now LINKS AND RUNS under `wasmtime`. End-to-end test:
    `TestCmdLangComponentWrapWrapsExit` actually invokes
    `wasmtime run --invoke main()` and asserts a clean exit.
  - **wasi:cli/run export.** `component.BuildWasiCliRunComponent`
    wraps a core module that exports `() -> i32` as a preview-2
    component implementing the `wasi:cli/run@0.2.0` world. The
    produced component runs under `wasmtime run prog.wasm`
    directly (no `--invoke`) — wasmtime treats the lifted i32 (0
    = ok, non-zero = err) as the process exit signal. Uses the
    packaged-instance form (component-instance section 0x01)
    rather than the sub-component form `wasm-tools` typically
    emits — simpler bytes, same semantics.
  - **`-component-wrap-cli` driver flag** routes a Lang program
    through `BuildWasiCliRunComponent` (no imports) or the new
    `WrapWasiImportedAsCliRun` helper (preview-2 imports) so
    `wasmtime run prog.wasm` just works. Mutually exclusive with
    `-component-wrap`. End-to-end tests:
    `TestCmdLangComponentWrapCli` (no-imports, clean + non-zero
    paths) and `TestCmdLangComponentWrapCliWithExit` (Lang
    `exit(0)` → wasi:cli/exit::exit + wasi:cli/run::run shapes
    in one component → wasmtime run + exit 0).
  - **`random_get` migration.** Lang's `random_i32()` (newly
    surfaced as a checker built-in) now routes through
    `wasi:random/random@0.2.0::get-random-u64() -> u64` under
    `EmitOptions.Preview2WASI`. The preview-2 import returns a
    scalar instead of filling a host-side buffer, so the
    `__fern_random_i32` body becomes just `call get-random-u64;
    i32.wrap_i64`. Selected via the new
    `preview2HelperBodyOverrides` map in wasi.go — a clean way
    to register per-helper body overrides without disturbing the
    default preview-1 path. Result-valued WASI imports are now
    expressible: `WasiImport.ResultValtypes` (a single-byte
    optional anonymous result) threads through the wrap
    pipeline; the generalised
    `PutTypeSectionInstanceWithInnerTypesAndOneFuncExport`
    handles both no-result and one-result functions. End-to-end
    test: `TestCmdLangComponentWrapCliWithRandom`.
  - **`clock_time_get` (monotonic) migration.** Lang's
    `monotonic_ns()` now routes through
    `wasi:clocks/monotonic-clock@0.2.0::now() -> u64` under
    `EmitOptions.Preview2WASI`. Cleanest migration yet — the
    preview-2 import returns the scalar directly, so
    `__fern_monotonic_ns` is just `call now`. The realtime
    variant (`__fern_now_ns` / `now_unix_ms`) stays on
    preview-1 for now — `wasi:clocks/wall-clock::now` returns a
    `datetime` record whose canonical-ABI lowering needs
    multi-value return support. End-to-end test:
    `TestCmdLangComponentWrapCliWithMonotonic`.
  - **`random_bytes` migration.** `__fern_random_bytes` rounds
    out the random story: under `EmitOptions.Preview2WASI` it
    rounds the requested length up to a multiple of 8 and fills
    the (padded) buffer by calling
    `wasi:random/random@0.2.0::get-random-u64()` once per
    8 bytes. Returns the original (unpadded) length so readers
    see exactly the requested byte count. Unlocks `random_int`
    (the stdlib helper used by user code via `std/math` — calls
    `random_bytes(3)` internally) under `-component-wrap-cli`.
    End-to-end test: `TestCmdLangComponentWrapCliWithRandomBytes`.
  - **Multi-import coverage.** Programs that pull in MORE than
    one preview-2 import in the same component validate and run
    end-to-end. Pinned by
    `TestCmdLangComponentWrapCliWithMultipleImports`:
    `random_i32()` + `exit(0)` → component imports
    wasi:random/random@0.2.0 AND wasi:cli/exit@0.2.0, exports
    wasi:cli/run@0.2.0, runs under plain `wasmtime run`.
  - **Void-main support.** `wasi:cli/run::run` lifts a core
    `() -> i32` to `() -> result<_, _>`. wasmbin's new
    `SynthCliRun` option emits a `_lang_run() -> i32` wrapper
    that normalises any main signature into the expected shape:
    void main → `call main; i32.const 0` (clean exit); i32 main
    → pass-through. `-component-wrap-cli` flips the option on
    automatically and uses `_lang_run` as the cli-run core
    export. Pinned by `TestCmdLangComponentWrapCliVoidMain`.
  - **`RawInstanceTypeBody` escape hatch.** Added in #1207.
    `WasiImport.RawInstanceTypeBody []byte` lets the driver
    embed a fully-pre-encoded instance-type body for
    interfaces whose shape the structured fields can't
    express yet. Unit-tested via
    `TestRawInstanceTypeBody_EscapeHatch` (reproduces
    wasi:cli/exit byte-for-byte against the structured path)
    and `TestRawInstanceTypeBody_ResourceDecl` (proves
    resource declarations now encode — first step toward the
    streams / filesystem migrations).
  - **`print()` migration (fd_write → wasi:cli/stdout +
    wasi:io/streams).** SHIPPED end-to-end. The preview-2
    shape:
      `wasi:cli/stdout::get-stdout() -> own<output-stream>`
      `wasi:io/streams::[method]output-stream.blocking-write-and-flush(self: borrow<output-stream>, contents: list<u8>) -> result<_, stream-error>`
    The wrap pattern uses 3 instance imports (wasi:io/error,
    wasi:io/streams, wasi:cli/stdout) with resource
    declarations + outer type aliases between them, plus 3
    core modules (user / trampoline / fixup) wired to satisfy
    the canon-lower / instantiation cycle.
    Foundation pieces:
    - `RawInstanceTypeBody` escape hatch (#1207) for
      resource-bearing interfaces.
    - Defvaltype helpers: `InnerTypeBorrow`,
      `InnerTypeListU8`, `InnerTypeResultErr` (#1223),
      `InnerTypeVariant` (#1227).
    - Decl helpers: `OuterAliasTypeDecl` (#1215),
      `ExportSubResourceDecl` / `ExportTypeEqDecl` (#1216).
    - Interface body composers: `WasiIoErrorInstanceTypeBody`
      (#1217), `WasiIoStreamsInstanceTypeBody` (#1228),
      `WasiCliStdoutInstanceTypeBody` (#1222).
    - Trampoline + fixup modules (#1230 / #1236).
    - Canon-lower with `memory` opt (#1209).
    - High-level helpers `WrapWasiPrintComponent` (#1240) and
      `WrapWasiPrintAsCliRun` (#1243).
    - Wasmbin's `__fern_print` body migrated to call the
      preview-2 funcs with stdout-handle caching (#1245).
    - Driver routes the print-only pattern through
      `WrapWasiPrintAsCliRun` (#1248).

    **End-to-end test** `TestCmdLangComponentWrapCliWithPrint`:
    Lang `print("hello world")` → `fern -target wasm-bin
    -component-wrap-cli` → `wasmtime run` → stdout `"hello
    world\n"`. No wasm-tools shell-out, no preview-1 adapter,
    no `--invoke` flag.
  - **`write` / `eprint` / `putchar` migrations.** SHIPPED.
    `write` shares the print body (newline-omitting variant,
    #1250); `eprint` writes to stderr via a separate
    `wasi:cli/stderr::get-stderr` cached handle (#1252);
    `putchar` reuses the stdout handle for a single-byte write
    (#1256). The stdout/stderr-write family is fully migrated.
  - **`clock_time_get` realtime migration.** SHIPPED.
    `now_ns` / `now_unix_ms` route through
    `wasi:clocks/wall-clock::now -> datetime` under
    `Preview2WASI`. datetime returns via the canonical-ABI
    indirect out-pointer, so the wrap uses the 1-i32 trampoline
    (generalised in #1257). The wasi:clocks/wall-clock instance
    type (#1258), the wrap helper (#1260), and the wasmbin
    bodies + driver routing + `now_ns` checker built-in (#1261)
    complete it. End-to-end test:
    `TestCmdLangComponentWrapCliWithNowNs`.
  - **`read_line` / `fd_read` migration. Shipped.**
    `__fern_read_byte` (the stdin byte reader `read_line` is
    built on) reads via `wasi:cli/stdin::get-stdin` +
    `wasi:io/streams::[method]input-stream.blocking-read` under
    `EmitOptions.Preview2WASI`. The read-side `wasi:io/streams`
    instance type (#1263), the valtype-generalised
    trampoline/fixup for the mixed `(i32, i64, i32)` blocking-read
    ABI (#1265), the `WrapWasiReadComponent` wrap with a
    realloc-bearing canon-lower for the returned
    `result<list<u8>, stream-error>` (#1266), the wasmbin
    `buildReadByteBodyP2` (#1267), the `WrapWasiReadAsCliRun`
    cli-run tail (#1269), and the `read_line()` checker built-in +
    driver routing + cabi_realloc-export rebuild (#1273) complete
    it. The read wrap is the first to alias the user module's
    `cabi_realloc` (gated behind `ForceMemorySection`, rebuilt
    only for detected read-only programs). End-to-end test:
    `TestCmdLangComponentWrapCliWithReadLine`.
  - **`args()` migration. Shipped.** `__fern_arg_count` /
    `__fern_arg_at` / `__fern_args` source argv from
    `wasi:cli/environment::get-arguments` (→ `list<string>`) under
    `EmitOptions.Preview2WASI`. The get-arguments instance type
    (#1276), the realloc-bearing `WrapWasiArgs{Component,AsCliRun}`
    (#1277), the wasmbin P2 bodies (#1279), and `usesPreview2ArgsOnly`
    driver routing (#1281) complete it. The list elements are already
    `(ptr, len)` pairs, so no preview-1 NUL walk. End-to-end test:
    `TestCmdLangComponentWrapCliWithArgs`.
  - **`env()` migration. Shipped.** `__fern_env` (the `env(name)`
    lookup) sources variables from
    `wasi:cli/environment::get-environment`
    (→ `list<tuple<string, string>>`) under
    `EmitOptions.Preview2WASI`. The get-environment instance type
    (#1281), the `WrapWasiEnv{Component,AsCliRun}` wrap (#1282), the
    wasmbin `buildEnvBodyP2` (#1285), and `usesPreview2EnvOnly`
    driver routing (#1286) complete it. get-environment returns
    pre-split `(key, value)` pairs, so the lookup is a plain
    length + byte compare (no `'='` scan). End-to-end test:
    `TestCmdLangComponentWrapCliWithEnv`.
  - **`read_file` / `write_file` (path_*) migration. Shipped.**
    The whole `wasi:filesystem` read + write path is preview-2
    under `EmitOptions.Preview2WASI`. The type layer — the
    `descriptor` resource (#1290), `wasi:filesystem/preopens`
    `get-directories` (#1293), the `error-code` enum +
    `InnerTypeEnum` (#1295), `read-via-stream` (#1296),
    `write-via-stream` (#1299), `open-at` + `InnerTypeFlags`
    (#1300), and the consolidated read/write-path instance types
    (#1301) — feeds the 4-import wraps
    `WrapWasiReadFile{,AsCliRun}` (#1304) /
    `WrapWasiWriteFile{,AsCliRun}` (#1307), each composing
    wasi:io/error + wasi:io/streams + wasi:filesystem/types +
    wasi:filesystem/preopens with four 1-func trampoline/fixup
    pairs. The wasmbin bodies `buildReadFileBodyP2` (#1306) /
    `buildWriteFileBodyP2` (#1307) chain
    `get-directories → open-at → {read,write}-via-stream →
    blocking-{read,write-and-flush}` (a doubling accumulator on
    read, ≤4096-byte chunks on write), and the
    `usesPreview2{Read,Write}FileOnly` driver routes rebuild with
    cabi_realloc. `error-code` is mapped to the right `IoError`
    variant (#1308). End-to-end tests:
    `TestCmdLangComponentWrapCliWith{Read,Write}File` (run under
    `wasmtime run --dir`, byte-exact).
  - **Mixed-import components. Shipped (common shapes).** Two
    mixed patterns now compose in one component:
    (1) stream-write + structured — print / eprint plus the
    no-memory structured funcs (exit / random / monotonic), via
    `wrapWasiStreamWriteWithStructured` +
    `classifyPrintPlusStructured` (#1312); fixes the common
    `print(err); exit(1)` path. (2) read_line + print — the
    canonical filter (read stdin, write stdout), via the combined
    read+write io/streams instance type (#1313) +
    `WrapWasiReadLinePrint{,AsCliRun}` +
    `usesPreview2ReadLinePrintOnly` (#1314), mixing no-trampoline
    getter lowers with trampoline blocking-read/-write lowers.
    End-to-end tests:
    `TestCmdLangComponentWrapCliWith{PrintExit,ReadLinePrint}`.
  - **Reader / Writer streaming API. Shipped.** Preview-2 has no
    fds, so a Reader / Writer carries a stream handle instead of an
    fd (stored in the same 12-byte rc struct at +8). `stdin()`
    stores the `get-stdin` input-stream handle (#1317);
    `open_reader` opens via get-directories → open-at →
    read-via-stream and stores that input-stream handle, with
    `read_line` / `read_chunk` blocking-reading on it (#1320);
    `open_writer` mirrors it with write-via-stream, and `write`
    blocking-write-and-flushes the output-stream handle (#1322).
    Each reuses an existing wrap (the read-only / read-file /
    write-file routes — the import sets already match), so no new
    component wrap was needed. End-to-end tests:
    `TestCmdLangComponentWrapCliWith{StdinReadLine,OpenReader,OpenWriter}`.
  - **General composition engine. Shipped.**
    `component.ComposePreview2CliRun` (#1326) replaced the bespoke
    per-shape CLI-stream wraps with a data-driven composer: given an
    optional stdout/stderr write side, optional stdin read side, and
    N no-memory structured imports, it walks a fixed canonical
    instance order and lowers each function by kind (no-memory
    simple lower, memory-only trampoline, memory+realloc trampoline),
    with a stateful `p2composer` tracking every component/core index
    space. The driver routes the whole family through it via
    `classifyComposeCliStream` (#1327), and the ~1000 lines of
    superseded wraps + tests were deleted (#1328). This unlocked new
    combinations the per-shape wraps never covered — `read_line+exit`,
    `read_line+print+exit`, `read_line+random`, … (e2e:
    `TestCmdLangComponentWrapCliComposedCombos`).
  - **Still to do:**
    - Fold the filesystem open-chain (read_file / write_file /
      open_reader / open_writer) into the composer too, so
      combinations like `read_file + print` or `write_file + exit`
      compose without a bespoke wrap. This also subsumes the bare
      `open_reader` / `open_writer`-with-no-read/write gap (the
      3-call open chain) once the composer drives the lowering.
    - `open_appender` (CREATE without TRUNCATE + append semantics)
      is still preview-1 only — preview-2 has no fd-append flag, so
      it needs `append-via-stream` (or an explicit seek-to-end
      before write-via-stream).
  - **Default-path driver wiring for `-target wasm`.** Shipped
    in #1204. `-target wasm` without `-wasi-adapter` routes
    through the Go-side preview-2 encoder (cli-run shape)
    automatically. Programs covered by the current preview-2
    import migration (no imports, or wasi:cli/exit /
    wasi:random/random / wasi:clocks/monotonic-clock) build
    without the `wasm-tools` shell-out. End-to-end tests:
    `TestCmdLangTargetWasmNoAdapter` /
    `TestCmdLangTargetWasmNoAdapterRejectsUnsupported`.
- **Compound canonical-ABI shapes.** `canon lower` with
  `mem+realloc` is in place but multi-result lifts, post-return
  lower, and the full "string / list / record param" lowering
  patterns aren't wired through the high-level helpers (they're
  composable via individual section composers though). Interfaces
  whose shape the structured `WasiImport` fields can't express
  yet (e.g. `wasi:clocks/wall-clock` exports `datetime` then
  references it from `now`) need a future "raw instance-type
  body" escape hatch on `WasiImport`.

#### Original scope


Scope: replace `wasm-tools component embed` and `wasm-tools component
new --adapt …` (lines 608 and 613 of `cmd/fern/main.go`) with a Lang
implementation. As of 2026-05-20, `component embed` is *already*
replaced on the Go side (`internal/wasm/componenttype.Embed`); the
last remaining external call is `component new --adapt` at
`cmd/fern/main.go:651` inside `emitPreview2ComponentFromCoreBytes`.
Phase 2's job is to take that last call out.

### Direction: skip the adapter entirely (preview-2 native)

Two options for dropping `component new --adapt`:

1. **Adapter composition.** Re-implement `component new --adapt`:
   bundle the preview-1 adapter (`wasi_snapshot_preview1.command.wasm`
   / `…reactor.wasm`) into the compiler binary, then write a
   two-module Component-Model envelope that wires the adapter's
   preview-2 exports up to wasmbin's preview-1 imports. wasmbin
   keeps emitting `wasi_snapshot_preview1.*` imports; the envelope
   writer hides them.
2. **Preview-2 native.** Replace every preview-1 import in
   `internal/codegen/wasmbin/wasi.go` (`fd_write`, `path_open`,
   `fd_close`, `proc_exit`, `random_get`, `clock_time_get`,
   `args_get`, `environ_get`) with the preview-2 equivalent
   (`wasi:cli/std{in,out,err}` + `wasi:io/streams`,
   `wasi:filesystem/types`, `wasi:cli/exit`, `wasi:random/random`,
   `wasi:clocks/{wall,monotonic}-clock`, `wasi:cli/environment`,
   …). The TCP and HTTP code already does this for
   `wasi:sockets/tcp@0.2.0` / `wasi:http/types@0.2.0` /
   `wasi:io/streams@0.2.0` so the canonical-ABI pattern is in tree.
   The Component-Model envelope still has to be written, but it's
   a *single-module* envelope — no adapter, no instance-section
   wiring of preview-1 → preview-2, no `-wasi-adapter` flag.

**Chosen direction: option 2 (preview-2 native).** Bigger ABI work
up front but the cleanest end state: every WASI call goes to
preview-2 directly, no preview-1 indirection, no adapter blob to
bundle. The Lang-stdlib component-section work already in flight
(see "Progress" above) feeds straight into 2's envelope writer.

### Spec

- Component Model binary format:
  <https://github.com/WebAssembly/component-model/blob/main/design/mvp/Binary.md>
- Canonical ABI (for the lift/lower marshalling of strings, lists,
  variants, resources — needed by every preview-2 import):
  <https://github.com/WebAssembly/component-model/blob/main/design/mvp/CanonicalABI.md>
- Preview-2 WASI worlds:
  <https://github.com/WebAssembly/WASI/tree/main/wasip2>
- WIT text format (only needed for cross-checking against the
  precomputed `componenttype` blobs):
  <https://github.com/WebAssembly/component-model/blob/main/design/mvp/WIT.md>

### What the two `wasm-tools` calls actually do

1. **`component embed`** takes a core wasm module and a WIT world,
   and writes a custom section `component-type` into the module
   containing the encoded world type. This is the only thing it does
   to the module; the core wasm bytes are otherwise untouched.
   (Already replaced Go-side; the Lang-stdlib version repeats the
   same trick.)
2. **`component new --adapt`** wraps the embedded core module
   together with the adapter module into a Component Model envelope:
   a `component` section containing two core modules (ours +
   adapter), an instance section that instantiates the adapter and
   wires its exports to our preview-1 imports, and an exports
   section that re-exports our `handle` (for the HTTP world) as a
   component-level export. Under option 2 there is no adapter
   module to splice in — the envelope holds just our core module.

### Shortcut: skip WIT parsing

The WIT files in `cmd/fern/wit/` are **fixed** — there's a `lang`
world and an `http` world, both known at compile time. We do not
need a general WIT parser. We can hand-write the encoded
`component-type` payloads for each world as static byte arrays in
Lang and inline them. If later we want users to bring their own WIT,
revisit then.

### Files to create

- `stdlib/wasm/component.fern` — `wrap_as_component(coreModule:
  Bytes, adapter: Bytes, world: World) -> Bytes`.
- `stdlib/wasm/component_type.fern` — the two precomputed
  `component-type` blobs (`lang_world_type` and `http_world_type`),
  one byte array each. Generate these once using `wasm-tools` and
  paste them in as constants, with a comment pointing at the WIT
  file and the exact `wasm-tools` invocation that produced them.

### Driver wiring

Replace lines 581–619 of `cmd/fern/main.go` with a single call:

```
let core = wasm.encode(ir);
let embedded = wasm.embed_world_type(core, world);
let component = wasm.wrap_as_component(embedded, adapter_bytes, world);
fs.write(out_path, component);
```

The adapter bytes (`wasi_snapshot_preview1.command.wasm`) can be
embedded into the compiler binary via the same mechanism the WIT
files use today (`//go:embed` equivalent — once Lang has a build-
time `embed` we use that; until then keep them as an external file
the driver loads).

### Exit criteria

- `wasmtime run` on the produced component succeeds for every
  preview-2 e2e test.
- `wasmtime serve` on the HTTP-world component handles a request
  end-to-end.
- `wasm-tools print` on the produced component matches (up to
  whitespace) the output for the previous `wasm-tools`-produced
  component. Diff in CI for the first month, drop once stable.

---

## Phase 3 — ELF object writer + static linker for arm64-linux

Scope: replace `aarch64-linux-gnu-gcc -static -nostdlib -s prog.s -o
out` with a Lang implementation that takes the **codegen IR** (not
the assembly text) and writes a static ELF executable directly.

Like Phase 1, the simplest approach skips the text intermediate.
Today the arm64 backend emits GAS assembly because `gcc` accepts it.
If we own the assembler too, we can emit ARM64 machine code directly
from the IR — no text round-trip.

### Spec

- ELF-64 spec: <https://refspecs.linuxfoundation.org/elf/gabi4+/contents.html>
- ARM64 instruction encoding: ARM Architecture Reference Manual
  for ARMv8-A (free PDF from arm.com), Section C4. Each instruction
  is exactly 4 bytes, fixed-width, little-endian. Encoding tables
  are large but mechanical.
- Linux arm64 syscall ABI: `man 2 syscall` + `arch/arm64/include/asm/unistd.h`.

### Architecture

Split into two pieces with a clean boundary:

1. **`stdlib/arm64/encode.fern`** — instruction encoder. One
   function per instruction we actually emit (the set is small,
   well under 100 distinct mnemonics — grep `arm64.go` for `g.line`
   calls to enumerate them). Each function returns a `u32`.
2. **`stdlib/elf/write.fern`** — ELF writer. Takes a list of
   sections (name, flags, bytes) + an entry-point symbol and
   produces a static PIE-or-non-PIE ELF executable. No dynamic
   symbol table needed (we're `-static -nostdlib`).

### Linker scope (deliberately minimal)

Because the compiler emits one self-contained `.o`-equivalent —
there's no real object linking, no archives, no `.so`s, no dynamic
relocation — the "linker" is really just:

- Lay out `.text`, `.rodata`, `.bss` at fixed virtual addresses.
- Resolve internal label references (already done by the assembler
  pass when it knows section offsets).
- Write the ELF header, program headers, and section headers.
- Set `e_entry` to the address of `_start`.

No `R_AARCH64_*` relocation handling against external objects. No
PLT, no GOT. This is much less code than a real linker.

### Files to create

- `stdlib/arm64/encode.fern` — instruction encoders.
- `stdlib/elf/write.fern` — ELF-64 executable writer.
- `internal/codegen/arm64/binary.fern` — new backend entry point
  that walks the IR and emits machine code + section data,
  replacing the text-emitting `EmitWithOptions`. Keep the text
  emitter around behind `-emit-asm` for debugging.

### Driver wiring

In `cmd/fern/main.go`, branch on target before calling `link`:

- If `arm64-linux` and `--native` (or once we trust it, always):
  call the new binary path. Drop the `gcc` invocation entirely.
- Otherwise: fall back to the text-emitting path + `gcc`. Keep this
  fallback until the binary path has shipped for a release cycle.

### Exit criteria

- Every `internal/e2e/arm64_*_test.go` passes on the binary path
  with no `aarch64-linux-gnu-gcc` on `$PATH`.
- `readelf -a` on a produced binary shows: ELF64, EXEC (or DYN),
  EM_AARCH64, one PT_LOAD per segment, correct entry point.
- `qemu-aarch64` runs the binary and reports the expected exit
  code / stdout.
- File size is within 2x of the `gcc -s` output. (`-s` strips
  symbols; we don't emit them in the first place, so we should be
  smaller, not larger.)

### Phase 3b — x86_64-linux

Once Phase 3 lands, x86-64 follows a near-identical path: same ELF
writer, new instruction encoder. The codegen IR is target-agnostic
already, so the new entry point under
`internal/codegen/x86_64/binary.fern` re-uses the ELF writer
unchanged and only differs in:

- Instruction encoding (variable-length, much messier than arm64 —
  but again, only the subset we actually emit).
- Syscall numbers and the `syscall` instruction (vs `svc #0`).
- `e_machine = EM_X86_64` and a different start-of-`.text` virtual
  address convention.

Budget Phase 3b at roughly 30% of Phase 3.

---

## Phase 4 — Mach-O object writer for arm64-darwin

Scope: produce a Mach-O object file directly from the codegen IR for
arm64-darwin, eliminating the need for `clang -c` (or any external
assembler).

This is the *object* writer. Linking and codesigning are Phase 5.

### Spec

- Apple's `mach-o/loader.h` and `mach-o/nlist.h` headers — these
  are the source of truth. (Bundled with Xcode CLT;
  also mirrored in the Darwin XNU open-source tree under
  `EXTERNAL_HEADERS/mach-o/`.)
- Apple's Mach-O Runtime Architecture guide (older, but still
  accurate for the basics).
- ARM64 instruction encoding from Phase 3 carries over verbatim —
  same ISA, same encodings. The bytes don't care which container
  they live in.

### Differences vs ELF

- Container is segments + sections (not just sections). We need at
  minimum `__TEXT` (with `__text`, `__const`) and `__DATA` (with
  `__bss`). Optionally `__LINKEDIT` for symbol/string tables.
- Symbol prefix is `_` (handled in the existing codegen via the
  `g.darwin` flag).
- Syscall numbers and the `svc #0x80` calling convention (already in
  `arm64.go`).
- Load commands instead of program headers: `LC_SEGMENT_64`,
  `LC_UNIXTHREAD` (or `LC_MAIN`), `LC_BUILD_VERSION`, `LC_UUID`.
- No PT_NOTE-style ".note.GNU-stack" needed.

### Files to create

- `stdlib/macho/write.fern` — Mach-O 64 object writer.
- `internal/codegen/arm64/macho.fern` — Mach-O-emitting backend
  entry point. Shares the Phase 3 ARM64 encoder; only the
  container is different.

### Exit criteria

- `otool -hlv` on the produced object reports a sensible
  MH_OBJECT (or MH_EXECUTE — see Phase 5) with the expected
  segments and load commands.
- On a native Apple Silicon host, passing the produced object to
  Apple's `ld` produces a runnable binary that matches the
  current `clang`-built binary's behaviour.

### Out of scope

- Linking (= Phase 5).
- Codesigning (= Phase 5).
- x86-64 Mach-O. The project's policy (CLAUDE.md) is
  Apple Silicon only on the macOS side; we won't emit x86-64
  Mach-O.

---

## Phase 5 — Mach-O linker + ad-hoc codesigning

Scope: take the Phase 4 output and produce a runnable Mach-O
executable, including the ad-hoc code signature that macOS 11+
requires for arm64 binaries. After this lands, `lld` and `clang` can
both come off the `arm64-darwin` build path entirely.

This is the largest single phase. The macOS dynamic loader is
strict about Mach-O layout and the code signature blob; getting
either wrong manifests as `Killed: 9` with no diagnostic.

### Sub-scope: linking

For our case the linker is structurally similar to Phase 3's: one
self-contained input, no archives, no real symbol resolution
across objects. The differences from the Phase 3 ELF linker:

- Emit `MH_EXECUTE` instead of `MH_OBJECT`.
- Use `LC_MAIN` (preferred since macOS 10.8) with the entry point's
  file offset, or `LC_UNIXTHREAD` with the full register state.
- Add an `LC_BUILD_VERSION` load command — required by recent
  loaders.
- Page-align segments to 16 KiB (arm64 page size on Apple Silicon).
- Reserve space *inside* the file for the code signature blob —
  the signature covers the file bytes, so the layout has to be
  final before signing.

### Sub-scope: ad-hoc codesigning

macOS will refuse to execute an unsigned arm64 binary. "Ad-hoc"
signing means: a signature that's structurally valid but signed
with no real identity. clang does this automatically; we need to
do it ourselves.

The format:

1. An `LC_CODE_SIGNATURE` load command pointing at a region in
   `__LINKEDIT`.
2. A "SuperBlob" (`CSMAGIC_EMBEDDED_SIGNATURE = 0xfade0cc0`)
   containing one "blob": a CodeDirectory
   (`CSMAGIC_CODEDIRECTORY = 0xfade0c02`).
3. The CodeDirectory contains a header + a list of SHA-256 hashes,
   one per 4 KiB page of the executable.

Spec is not officially published; the authoritative sources are:

- Apple's open-source `Security` framework, `OSX/libsecurity_codesigning/lib/`
  (look for `CodeDirectory.h` and `CSCommon.h`).
- `cctools` `ld64` source for the layout side.
- jevinskie/macho-utils and `apple-codesign` (Rust crate) are
  approachable cross-references.

### Files to create

- `stdlib/macho/link.fern` — final-layout + load-command writer.
- `stdlib/macho/codesign.fern` — SuperBlob + CodeDirectory
  writer. Depends on a SHA-256 implementation (`stdlib/crypto/sha256.fern`).
- `stdlib/crypto/sha256.fern` — pure-Lang SHA-256. Reference impl:
  RFC 6234 Appendix B. Test against the standard NIST vectors.

### Cross-compile caveat

When building on Linux for Darwin (the CI cross path), the produced
binary still has to be ad-hoc signed. The signature is just bytes —
no Apple tooling involved — so this works identically on Linux. Test
this explicitly: produce a binary on Linux, scp it to the macOS CI
runner, run it without re-signing.

### Exit criteria

- `codesign -dvvv` on the produced binary reports a valid ad-hoc
  signature.
- `spctl -a -t exec -vv` reports "rejected (no usable signature
  found)" — this is the expected outcome for ad-hoc; Gatekeeper
  rejecting is fine, the loader doesn't care.
- The binary actually runs on Apple Silicon macOS 14+ and produces
  the expected output.
- Cross-built (Linux → Darwin) binaries run on the macOS CI runner.
- `clang` and `lld` can be removed from the macOS CI install step
  without breaking the build.

---

## Risks / open questions

- **Lang's `exec` story.** Today the compiler is in Go and uses
  `os/exec`. The new emitters are pure Lang and don't need `exec` at
  all — that's a feature. But the broader self-hosting effort
  (compiler-written-in-Lang) still needs `exec` to invoke things
  like `qemu-aarch64` from tests. That's a separate problem from
  this doc and not blocking.
- **Embedding the adapter wasm.** Phase 2 needs the preview-1
  adapter bytes baked into the compiler. Until Lang has a
  build-time `embed`, the driver has to read it from disk at
  startup. Acceptable interim.
- **macOS version drift.** Apple has changed Mach-O load command
  requirements every few releases (LC_MAIN added in 10.8,
  LC_BUILD_VERSION required in 10.14, ad-hoc signing required in
  11). Pin the targeted macOS version in `docs/BACKEND-PARITY.md`
  and revisit the codesigning code each time we bump.
- **No PIE / no ASLR.** The Phase 3 ELF writer and Phase 5 Mach-O
  linker both emit fixed-address executables initially. This is
  the same posture as the current `gcc -static -nostdlib` output,
  so no regression. PIE can be added later if needed.
- **No debugger support.** No DWARF, no `__debug_info`. Same as
  today's `gcc -s` output. Adding DWARF is a future project.

## What this plan deliberately doesn't cover

- Replacing the Go-based compiler frontend with a Lang frontend.
  That's the *compiler* self-hosting question, covered in
  `ROADMAP-AND-SELF-HOSTING.md`. This doc is only about the
  *toolchain* self-hosting question.
- Supporting third-party object files / `.a` archives / dynamic
  libraries. Lang programs are self-contained — we don't link
  against external `.o` files and have no plans to.
- Replacing `wasmtime` (the runtime). wasmtime is only used in
  tests, not by the compiler. If we want to drop it, that's a
  separate "Lang wasm interpreter" project.
- Replacing `qemu-aarch64` / `qemu-x86_64`. Same — test
  infrastructure only.

## Sequencing summary

```
Phase 1 (WAT→binary)              ──┐
Phase 2 (Component Model writer)  ──┴── wasm target free of external tools
Phase 3 (arm64 ELF writer + linker) ─┐
Phase 3b (x86_64 ELF writer)        ─┴── Linux targets free of external tools
Phase 4 (Mach-O object writer)      ─┐
Phase 5 (Mach-O linker + codesign)  ─┴── Darwin target free of external tools
```

Each phase can ship independently and the rest of the toolchain
keeps working off the external tools during the transition. The
goal posts move one target at a time.
