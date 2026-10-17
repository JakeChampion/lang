# Runtime needs close on registration

`EmitState.need(root)` expands the root's transitive dependencies before
returning. Both native emitters register roots through it, so a runtime gate
cannot read a root without its declared dependencies. The worklist visits only
newly appended names; repeated registrations leave the set unchanged.

This removes 18 `mark_*` wrappers and the separate `close_needs` phase. The
table remains the declaration of hand-written helpers' dependencies. The
runtime-emission retry loop also remains: lowering a helper can discover a
new root whose bundle was already emitted. This is one step toward #2649,
whose runtime-in-Fern and call-graph migration is still open.

## Validation

- `TestSelfHostRuntimeNeedRegistration` checks immediate and transitive
  dependencies, shared dependencies, duplicate roots, an unknown marker,
  packed appends, direct array-helper registration, and preservation of the
  input state. Against the original `asmcore.fern`, the immediate-dependency
  assertion fails with exit 2.
- Native need detection and cross-module runtime aggregation pass.
- The WASI buffer-needs suite passes, and every one of the 124 recognized
  WASI needs links independently.
- Eight bench programs (`array_append`, `array_with`, `map_int`, `map_string`,
  `string_build`, `string_slice`, `struct_drop`, `tokenize`) emit identical
  output before and after on all four targets: x86-64 Linux, ARM64 Linux,
  ARM64 Darwin and WASI. No performance baseline changes.
- Bootstrap stage 2 equals stage 3. The source checks, formatting check and
  complexity ratchet pass.

The two source tests for wrapper/table agreement and closure/gate ordering
are removed with those mechanisms. Registration is exercised by compiled
Fern code instead.
