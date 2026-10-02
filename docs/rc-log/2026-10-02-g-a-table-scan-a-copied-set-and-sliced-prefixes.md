# 2026-10-02 — a table scan, a copied set and sliced prefixes

`asmcore.infer_call_named_type`, `irlower.noesc_set_kill`, `semtypes.prefixed`,
every `slice_unchecked(s, 0, p.len()) == p` in the self-host sources, the
borrowable registry's bucket count and `irlower.strarr_own_call`'s store
lookup. Refs #8171. No emitted byte changes: the stage0-built compiler before and after
emits the fixed older tree (`examples/self_host/fern.fern` at 1ae9cad) byte for
byte, and `checker.fern` likewise, on main at 0d7a8d32 with the previous
entry's change.

## What the profile named

The whole-compiler emit after the previous entry
(`docs/LOCAL-DEV-LOOP.md`, "Where a whole self-host emit spends its time"):

- `asmcore.infer_call_named_type` 3.28 G of 214.05 G, 23.9 k calls at 137 k
  Ir each. The if-chain over the builtin names is 183 string compares per
  call and costs 0.1 G; the rest is the fall-through, a scan of every function
  declaration (`s.funcs`, 10.8 k) for the callee's declared result, plus two
  `string[]` literals of builtin names built per call.
- `irlower.noesc_set_kill` 1.31 G, 7,520 calls: the first `live.with(i,
  false)` copied the whole `boolean[]` each time (`__fern_arr_slice`, 171 k Ir
  per call), since the set was still held by the caller's reference while
  its row was written.
- `semtypes.is_env` 1.90 G, 13.2 M calls: `prefixed` compared a prefix by
  slicing it off the name, one allocation per call. The same shape,
  `slice_unchecked(s, 0, p.len()) == p`, occurred at 22 other sites, among
  them `irlower.strarr_keyed_any` (1.02 G, 11.8 M rows scanned for a prefix
  by `strarr_own_call`).
- `irlower.param_is_borrowable` 3.56 G self, 351 k lookups at 9.7 k Ir: the
  borrowable registry had 251 buckets for some 11 k keys across its tiers,
  and a lookup walks its bucket's records byte by byte.
- `irlower.borrow_reg_set` 2.00 G: it copied the registry bucket by bucket
  with `append` before storing one record, twice per callee in
  `ssarc.caller_sigs`.

## What changed

- `EmitState` carries `funcs_ix`, a `util.NameIndex` over `funcs` kept in step
  by `with_funcs`, which the five sites that set `funcs` now go through; the
  fall-through walks the callee's chain. `builtin_i32` and `builtin_string`
  index the two literal tables once, in `new_state`.
- `noesc_set_kill` takes the set as an `own` parameter and reads the registry
  through a local, so the set is released before its live row is written and
  the write is in place. Its one caller threads the set through a local at its
  last use, which is what `own` asks of a caller.
- `util.has_prefix` compares a prefix in place; the 22 sites use it, and
  `asm_arm64_ir`'s `darwin_starts_with` wrapper is gone. `semtypes.prefixed`
  compares in place too (semtypes does not import util).

- The registry has 4093 buckets (`mfuncs_buckets` is sized the same way), and
  `borrow_reg_set` stores through one `with` on the borrowed registry, which
  copies the bucket array once when it is shared, rather than rebuilding it
  by appending. Taking the registry `own` and storing in place was the aim;
  it is held back by #11020, where the two checkers disagree on E051 for the
  fixture that calls `borrow_reg_set` from a struct literal argument.
- `StrarrOwnFrame` carries `store_keys`, a `NameIndex` over the key before
  each store row's `|`, built once per module beside `stores`; the call asks
  it `has(ck)` instead of scanning every row for the prefix. Built per frame
  it cost more than the scan (1.2 G over 10.8 k frames), which is the trap.

A hoist that handed the emit state into `shape_ref` at its last use did not
make `add_string_lit`'s append in place and was dropped: the state reaches it
as a borrowed parameter through about 25 `shape_ref` callers in both backends,
so the `own` route runs through the whole emitter. The 2.4 G of
`__fern_arr_inc_elems` under `add_string_lit` (the literal table copied once
per interned shape, 15,185 times) stays the largest single copy on the profile.

## Measured

Whole-compiler emit under callgrind, 4-core x86-64 container: the driver the
pinned stage0 (c891ebc) builds from each tree, emitting the fixed older tree
to x86-64 asm text. Both rows are built from main at 0d7a8d32 with the previous
entry's change, and this change on top.

| | before | this change |
|---|--:|--:|
| total Ir | 214.05 G | 206.70 G (−3.4%) |
| `asmcore.infer_call_named_type`, inclusive | 3.28 G | 0.08 G |
| `irlower.noesc_set_kill`, inclusive | 1.31 G | 0.02 G |
| `semtypes.is_env`, inclusive | 1.90 G | 1.07 G |
| `irlower.param_is_borrowable`, inclusive | 3.56 G | 0.66 G |
| `irlower.borrow_reg_set`, inclusive | 2.00 G | 2.62 G |
| `irlower.strarr_own_call`, inclusive | 1.20 G | 0.41 G |
| `__fern_arr_slice`, inclusive | 3.25 G | 2.67 G |

`borrow_reg_set` rises because its one `with` now copies 4093 buckets rather
than 251: the lookups it serves fall by 2.9 G for that 0.6 G. With the
registry taken `own` the same emit measured 202.85 G and `borrow_reg_set`
0.02 G; that is what #11020 holds back.

`checker.fern` alone, the quick loop: 31.19 G to 31.00 G before the registry
change. The costs here are whole-program sized (the function table, the
escape set, the env rows, the registry), so that input shows them only as a
direction.

## Witnessed

`TestSelfHostConstAggregate*`, `TestSelfHostConstGrammarX86_64`,
`TestSelfHostCheckerCodesX86_64`, `TestSelfHostCheckerCodeSequenceX86_64`,
`TestSelfHostSemanticSource*`, `TestSelfHostClosureEnvRc*`,
`TestSelfHostArm64DarwinBuilds`, `TestSelfHostMonoIndexArgIR`,
`TestSelfHostAnnotateIndexMonoIR_X86_64`, the `StrArr` field and return
tests, `TestSelfHostUndefinedCallGate`, `TestSelfHostGenericArity*`,
`TestSelfHostThreadParamX86_64`, `TestSelfHostIRPerModuleDriver`,
`TestSelfHostFixtureSourcesCheck`, `TestSelfHostFeatureCensus`, the lint
ratchet, and the two emit identities.

## Next

Self cost on this profile: `ssa_lift.lift_impl` 7.05 G, `util.hash_bucket`
6.82 G (#11008 shortens its loop), `__fern_alloc` 4.82 G (4.06 G of it under
`__fern_arr_box`, 171 M boxings, 63 M of them from `__fern_arr_push`),
`__fern_str_eq` 4.42 G, `__fern_arr_dec` 3.54 G, `irlower.param_is_borrowable`
3.39 G (351 k calls at 9.7 k Ir: a byte walk of the whole bucket row per
lookup), `asmcore.add_string_lit` 3.17 G inclusive (the copy above),
`ir.fold_const_binaries` 2.83 G, `irlower.strarr_own_call` 1.20 G (the
`stores` rows want an index by their key rather than a prefix scan per call).
