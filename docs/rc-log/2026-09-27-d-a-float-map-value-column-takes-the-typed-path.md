# A float map value column takes the typed path

`ssasem.is_supported_map` admitted a value column that was narrow, i64/u64
or counted, so under `FERN_SEM_IR_STRICT` a `Map[K, f64]` or `Map[K, f32]`
was refused with "unsupported map shape" (#10403). The reason given was
that wasm had no f64 helpers. That was out of date: the wasm backend's
`_w64` helpers take an f64 cell as its bits, and `mapiter_value` with
widekind 2 reinterprets the i64 it reads.

`ssasem.float_map_value` now admits both widths. The self-host carries an
f32 as an f64 everywhere else — an `f32[]` element, an `Option[f32]`
payload — so an f32 column holds f64 cells too, and `values()`, `get` and
iteration hand back exactly the representation their readers expect. No
bit crossing is needed, unlike native's SSA backends (#10398), which store
the f32 bits. `retained_column` counts a float column as a scalar column.
`ssarc` passes widekind 2 for it to every map op, and `unshared_map`'s
copied value slot is an f64 slot. `op_mapiter_value` takes the widekind
instead of a boolean. For an f64 column the AST lowering's boolean said
widekind 1, where its other map ops say 2; it now passes the same widekind.

#10403's program is `conformance/cases/map_f32_column`, so every corpus leg
runs it, and `TestMapF32ColumnSSA` runs the same file through native's
`-backend ssa`. `TestSelfHostMapW64IterIR` and `TestSelfHostMapW64RecvIR`
move to the CLI and run on x86-64 and wasm.
