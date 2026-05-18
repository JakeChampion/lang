# Toolchain self-hosting plan

Goal: eliminate every external compiler / assembler / linker / wasm
helper the driver currently shells out to, replacing each with a
Lang-native implementation. After all phases land, building a Lang
program with `lang -o out src.lang` requires **no binary on `$PATH`
other than the Lang compiler itself**.

This is a deliberate alternative to the position taken in
`ROADMAP-AND-SELF-HOSTING.md`, which argues for keeping a thin
external bootstrap (clang / lld / wasm-tools) indefinitely. That doc
is right that the bootstrap is the cheap pragmatic answer. This doc
is the plan for the people who want to do it the hard way anyway.

## Current shell-outs (the honest map)

Every backend currently emits **text** (assembly or WAT) and shells
out to an external tool to turn it into a runnable artifact:

| Target          | Driver fn                | External tool(s)                  | What it does                                  |
|-----------------|--------------------------|-----------------------------------|-----------------------------------------------|
| `arm64-linux`   | `link` @ `cmd/lang/main.go:528`        | `aarch64-linux-gnu-gcc` (→ `as` + `ld`) | Assemble `.s`, link static ELF                |
| `x86_64-linux`  | `link` @ `cmd/lang/main.go:528`        | `x86_64-linux-gnu-gcc` (→ `as` + `ld`)  | Same, for x86-64                              |
| `arm64-darwin`  | `linkDarwin` @ `cmd/lang/main.go:460`  | `clang` (+ `lld` on Linux hosts)        | Assemble `.s`, link Mach-O, ad-hoc codesign   |
| `wasm`          | `emitPreview2ComponentWorld` @ `cmd/lang/main.go:581` | `wasm-tools` (`parse` + `component embed` + `component new --adapt`) | WAT → core wasm → component-model binary |

So the "replace clang / lld / wasm-tools" framing in the prior chat
undersold the work: the Linux backends also depend on an external
toolchain (`gcc`, which is itself a frontend for `as` + `ld`). To be
*truly* free-standing we need replacements for **all five** roles:
ARM64 assembler, x86-64 assembler, ELF linker, Mach-O linker
(with codesigning), WAT-to-binary encoder + Component Model writer.

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

- `stdlib/wasm/encode.lang` — pure encoder:
  module → bytes. Mirrors the structure of `wasm_ir.go` but writes
  bytes instead of text.
- `stdlib/wasm/leb128.lang` — `put_uleb(buf, u64)` and
  `put_sleb(buf, i64)`.

### Driver wiring

Replace the `wasm-tools parse prog.wat -o prog.wasm` call at
`cmd/lang/main.go:604` with a direct call to the new encoder.
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

- **LEB128 encoders shipped** in `internal/stdlib/std/wasm/leb128.lang`
  with `uleb_u32` / `uleb_u64` / `sleb_i32` / `sleb_i64` plus
  `uleb_size_u32` / `uleb_size_u64`. Vector-tested against the
  Wikipedia LEB128 reference examples and the wasm-spec edge cases
  (bit-6 transitions, multi-byte negatives, u32/u64/i64 widths) under
  `internal/e2e/wasm_e2e_test.go` (TestWASMLeb128*). Pure Lang —
  takes a `u8[]` and appends; no I/O. Not wired into the driver
  yet, per the "Lang code only, defer running it" decision.
- **Binary-container primitives shipped** in
  `internal/stdlib/std/wasm/encode.lang`: `put_module_header`
  (magic + version), `put_u32_le` (the one fixed-width integer the
  wasm binary uses), `put_name` (uleb-prefixed UTF-8),
  `put_section` (id + uleb size + body), `put_func_type` (the type
  section's per-entry encoding), and `valtype_*` / `section_*`
  byte-constant helpers. Tests
  (`TestWASMEncodePreamble`, `…NameAndSection`, `…MinimalModule`)
  build a complete 16-byte wasm module from scratch and compare
  every byte against the spec-reference encoding.
- **Control-flow + constant + variable instruction encoders
  shipped** in `internal/stdlib/std/wasm/inst.lang`. The subset
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
  `internal/stdlib/std/wasm/numeric.lang`: every wasm numeric
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
  `internal/stdlib/std/wasm/memory.lang`: every wasm load / store
  variant including the narrow widths (i32.load8_s, i64.store16,
  etc.), all carrying the standard `memarg` immediate (uleb
  align + uleb offset); plus `memory.size` / `memory.grow` with
  their reserved memidx byte. ~25 functions. Tests
  (`TestWASMMemoryLoad`, `…Store`, `…SizeGrow`) walk both the
  full-width and narrow-width blocks plus the multi-byte uleb
  offset case (offset=128 forces a 2-byte uleb).
- **Conversion / reinterpret / sign-extension encoders shipped**
  in `internal/stdlib/std/wasm/convert.lang`: integer-width
  conversion (i32.wrap_i64, i64.extend_i32_{s,u}), float-to-int
  trapping truncation (8 variants across i32/i64 × f32/f64 × s/u),
  int-to-float conversion (8 variants), float-width
  demote / promote, the reinterpret family (i32↔f32 / i64↔f64
  bit-pattern aliases), and the sign-extension extension
  (i32.extend{8,16}_s, i64.extend{8,16,32}_s). ~28 functions, all
  single-byte opcodes — same one-line shape as numeric.lang.
  Tests (`TestWASMConvertIntWidth`, `…FloatInt`) walk every
  variant.
- **Section composers shipped** in
  `internal/stdlib/std/wasm/sections.lang`: one function per
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
  `internal/stdlib/std/wasm/imports.lang`. Imports needed their
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
- All section composers are now in place. Remaining Phase 1 work:
  the IR-walking entry point that arranges a codegen IR program
  as inputs to these composers and stitches the preamble +
  sections together in spec order, and the driver-wiring step
  that deletes the `wasm-tools parse` call at
  `cmd/lang/main.go:604`. Saturating-truncate ops and bulk-memory
  ops (both 0xFC-prefixed) are deliberately out of scope — the
  production backend doesn't lean on them. Element section is
  also deliberately deferred — the existing wasm backend doesn't
  exercise table-element segments.

---

## Phase 2 — Component Model writer

Scope: replace `wasm-tools component embed` and `wasm-tools component
new --adapt …` (lines 608 and 613 of `cmd/lang/main.go`) with a Lang
implementation.

### Spec

- Component Model binary format:
  <https://github.com/WebAssembly/component-model/blob/main/design/mvp/Binary.md>
- WIT text format (only needed if we parse WIT ourselves — see
  "shortcut" below):
  <https://github.com/WebAssembly/component-model/blob/main/design/mvp/WIT.md>
- Preview-1 → preview-2 adapter: we consume the pre-built
  `wasi_snapshot_preview1.command.wasm` (and `…reactor.wasm` for the
  HTTP world). No need to *build* the adapter — just splice it in.

### What the two `wasm-tools` calls actually do

1. **`component embed`** takes a core wasm module and a WIT world,
   and writes a custom section `component-type` into the module
   containing the encoded world type. This is the only thing it does
   to the module; the core wasm bytes are otherwise untouched.
2. **`component new --adapt`** wraps the embedded core module
   together with the adapter module into a Component Model envelope:
   a `component` section containing two core modules (ours +
   adapter), an instance section that instantiates the adapter and
   wires its exports to our preview-1 imports, and an exports
   section that re-exports our `handle` (for the HTTP world) as a
   component-level export.

### Shortcut: skip WIT parsing

The WIT files in `cmd/lang/wit/` are **fixed** — there's a `lang`
world and an `http` world, both known at compile time. We do not
need a general WIT parser. We can hand-write the encoded
`component-type` payloads for each world as static byte arrays in
Lang and inline them. If later we want users to bring their own WIT,
revisit then.

### Files to create

- `stdlib/wasm/component.lang` — `wrap_as_component(coreModule:
  Bytes, adapter: Bytes, world: World) -> Bytes`.
- `stdlib/wasm/component_type.lang` — the two precomputed
  `component-type` blobs (`lang_world_type` and `http_world_type`),
  one byte array each. Generate these once using `wasm-tools` and
  paste them in as constants, with a comment pointing at the WIT
  file and the exact `wasm-tools` invocation that produced them.

### Driver wiring

Replace lines 581–619 of `cmd/lang/main.go` with a single call:

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

1. **`stdlib/arm64/encode.lang`** — instruction encoder. One
   function per instruction we actually emit (the set is small,
   well under 100 distinct mnemonics — grep `arm64.go` for `g.line`
   calls to enumerate them). Each function returns a `u32`.
2. **`stdlib/elf/write.lang`** — ELF writer. Takes a list of
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

- `stdlib/arm64/encode.lang` — instruction encoders.
- `stdlib/elf/write.lang` — ELF-64 executable writer.
- `internal/codegen/arm64/binary.lang` — new backend entry point
  that walks the IR and emits machine code + section data,
  replacing the text-emitting `EmitWithOptions`. Keep the text
  emitter around behind `-emit-asm` for debugging.

### Driver wiring

In `cmd/lang/main.go`, branch on target before calling `link`:

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
`internal/codegen/x86_64/binary.lang` re-uses the ELF writer
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

- `stdlib/macho/write.lang` — Mach-O 64 object writer.
- `internal/codegen/arm64/macho.lang` — Mach-O-emitting backend
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

- `stdlib/macho/link.lang` — final-layout + load-command writer.
- `stdlib/macho/codesign.lang` — SuperBlob + CodeDirectory
  writer. Depends on a SHA-256 implementation (`stdlib/crypto/sha256.lang`).
- `stdlib/crypto/sha256.lang` — pure-Lang SHA-256. Reference impl:
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
