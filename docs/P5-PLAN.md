# P5 — resource / handle language layer (plan)

Adds the *language* layer on top of the shipped P5 baseline (a handle is
already an opaque `i32` at the canonical ABI). Two user-visible goals:

1. **Handle types** — stop writing raw `i32`; write `own<T>` / `borrow<T>`
   over a named resource.
2. **Automatic drop** — the compiler calls the resource's `[resource-drop]`
   when an owned handle goes out of scope.

## Decisions

- **Surface syntax:** `own R` / `borrow R` (the Fern-idiomatic prefix form,
  mirroring `dyn Trait` — Fern uses `[]` for generics, never `<>`, so the WIT
  `own<R>`/`borrow<R>` maps to `own R`/`borrow R`) over a nominal resource
  introduced by a `resource` declaration that carries the WIT binding via the
  existing `@import` attribute. A bare resource name means an owned handle.

  ```fern
  @import("wasi:io/poll@0.2.0", "pollable")
  resource Pollable;

  @import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
  function subscribe(ns: u64): own Pollable;

  @import("wasi:io/poll@0.2.0", "[method]pollable.ready")
  function ready(h: borrow Pollable): boolean;
  ```

  `@import` on a `resource` decl reuses the `parseAttribute` machinery (no new
  attribute). `own R`/`borrow R` erase to the handle scalar (`i32`) at the
  `ir.LowerWith` choke point — no ABI change. The declaration captures
  `(iface, wit-name)` so the drop slice knows which `[resource-drop]<wit-name>`
  to call.

- **Drop (per the steer):** go straight to automatic (compiler-inserted at
  scope exit). Fall back to a user-facing manual `@import(...,
  "[resource-drop]X") function drop(h: own R);` only if auto-insertion proves
  infeasible. The composer `[resource-drop]` wiring (slice 2) is required
  either way.

- **First slice:** types only (front end + checker, both compilers, no codegen
  change). ✅ Done — see the status note in docs/WIT-BRING-YOUR-OWN.md.

## Grounding (from the codebase map)

- **Front end:** parser `parseAttribute` (`internal/parser/parser.go:729`)
  parses `@import`; body-less functions stamped `ImportIface`/`ImportWITName`.
  Types parse in `parseType`. `ast.Type` family: `NumberType`, `ArrayType`,
  `SliceType`, `EnumType{Name,Args}`, `StructType{Name,Args}`, `ParamType`,
  `TupleType` (`internal/ast/ast.go:34+`).
- **Checker:** single recursion point `resolveType`
  (`internal/checker/checker.go:3136`); externs skip body checking; signatures
  registered in `FuncSigs`. Ownership gates: `Param.Own`, `OwnedByDefault`,
  `BorrowInferEnabled`, `RcFreeEnabled` (`internal/ast/ast.go:494+`).
- **IR drop:** `dropStructField`, `emitRcDecLocalsAtExit{,Except}`,
  `computePreciseDrops`/`emitPreciseDrop` (`internal/ir/ir.go`). Three
  drop-timing hooks (exit sweep, precise last-use, defer). `ir.ExternFunc`
  carries `Iface`/`WITName`; an extern call is `OpCallDirect` by name.
- **Composer blockers:** `hasResourceDropPrefix` rejects drop imports in
  `ComposeFromWorldAuto` (`compose_world.go`); `PutCanonResourceDrop` /
  `gDrop` / `resourceT` exist (`compose.go`, `compose_general.go`,
  `component.go`) but need a component-level type index for the resource;
  `EmitWorldImports` (`componenttype/world_emit.go`) does not surface resources
  at the top level — a drop path must additively emit
  `alias export <inst> "<resource>" (type)` and thread the index through.
  Byte-identity gate: `TestPutCanonResourceDrop_Bytes` + the compose suite;
  drop-free programs must reproduce today's bytes exactly.
- **Self-host:** types are type-name strings on `FuncDecl`
  (`examples/self_host/parser.fern`); `parse_import_attr`,
  `import_iface`/`import_wit` fields; printer round-trip gated by the printer
  self-test.

## Slices (each: both compilers, tests at the layer touched, branch → commit →
## push → PR → subscribe)

### Slice 1 — handle type vocabulary (FIRST; front end + checker, no codegen change)

- **Lexer/parser:** `@resource("iface","wit-name")` attribute; `resource Name;`
  declaration (contextual keyword at decl position, no hard reserved word);
  `parseType` parses `own<T>` / `borrow<T>`.
- **AST:** `ResourceDecl{Name, Iface, WITName}` recorded on `Program`;
  `OwnType{Inner}` / `BorrowType{Inner}`; a nominal `ResourceType{Name}` (or
  reuse the `StructType`-style nominal) for the inner reference.
- **Checker:** register resource decls (name → WIT binding) in `Info`; add
  `resolveType` cases for the new nodes; reject `own<>`/`borrow<>` over an
  undeclared resource; type-check `@import` signatures + call sites using the
  new types (erasing to the handle scalar). Light linearity: `own<T>` is a
  move, `borrow<T>` is non-consuming — full use-after-move enforcement lands
  with the drop slice.
- **Codegen:** `own`/`borrow`/resource erase to `i32`; extend the extern
  scalar-type gate (`externScalarType` / self-host `wat_extern_valtype`) to
  accept handle types as scalars.
- **Self-host:** `parser.fern` (`@resource`, `resource` decl, `own<>`/`borrow<>`
  type-name strings), `printer.fern` render + round-trip, type-name handling.
- **Tests:** parser test (own/borrow + resource decl); checker test
  (resolution + reject undeclared resource + own/borrow rules); e2e — rewrite
  the P5 baseline (`wit_p5_resource_handle_test.go` /
  `self_host_p5_resource_handle_test.go`) to use `own<Pollable>` /
  `borrow<Pollable>` and confirm it still runs under wasmtime (proves erasure);
  self-host printer assertions.

### Slice 2 — composer `[resource-drop]` wiring (additive, byte-identity-gated)

- `EmitWorldImports`: after each import instance, additively emit
  `alias export <inst> "<resource>" (type)` for resources that need a drop;
  update `PrefixLayout` counts. Absent/empty case reproduces today's bytes.
- Helper mapping `(iface, resource-name) → surfaced component type index`.
- `ComposeFromWorldAuto`: drop the `hasResourceDropPrefix` rejection; classify
  `[resource-drop]X` into `gImport{kind:gDrop, resourceT:<idx>}`.
- Mirror the wiring in the self-host composer port (`wat_component.fern`).
- **Tests:** byte-identity unit (drop-free == today's bytes); run-gated e2e — a
  manual `@import(...,"[resource-drop]pollable")` drops a pollable, validates +
  runs under wasmtime; full `internal/wasm/component` + component e2e suite
  stays green.

### Slice 3 — automatic drop insertion

- Register a synthetic `ExternFunc` per resource for `[resource-drop]<wit-name>`
  in the resource's iface so the backend imports it.
- IR: mark `own<T>` locals; at scope exit / last use (hook
  `emitRcDecLocalsAtExit` / `computePreciseDrops`) emit the drop call.
  `borrow<T>` is never dropped; an `own<T>` moved into a callee that takes
  `own` is not double-dropped.
- Checker: finalize use-after-move (an E-code, mirroring `checkOwnedParams`).
- **Tests:** e2e proving the drop import is present and called (drop observable
  / no double-free), both compilers. If auto-insertion proves infeasible, fall
  back to shipping slice 2's manual `drop()` extern as the user path.

## Engineering bar

Gate locally on x86-64 + WASM (`wasmtime`/`wasm-tools` at `/tmp/wt`,
`FERN_WASI_ADAPTER=/tmp/wt/adapter.wasm`); CI runs arm64. Each slice ships in
both compilers with tests at the layer it touches; never regress (full suite
after each change). Composer changes stay additive against the byte-identity
oracle.
