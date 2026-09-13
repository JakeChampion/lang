# The aliased `.with` clone supersedes a buffer nothing released

`TestSelfHostCoreutilsParity/od/long_doubles_over_arbitrary_bytes` killed the
runner: `od -t fL` over 300 pseudo-random bytes, compiled by the SELF-HOST
compiler, grew past the box and ended in a shutdown signal (exit 143 in CI,
137 locally), where the native build of the same source runs in 0.4 s and gives
everything back.

## Where the bytes went

`FERN_RC_TRACE=1` at emit time, run over the first 48 bytes of the fixture,
`a` lines paired against `f` lines by pointer, sites resolved against a
gcc-linked copy of the same asm (the driver has no `-g`):

| unpaired blocks | bytes | site | caller |
| --- | --- | --- | --- |
| 177,238 | 308,569,080 | `__fern_arr_slice` | `bigint.__bi_mul_small` |
| 61,252 | 156,056,904 | `__fern_arr_slice` | `bigint.BigInt.to_string` |
| 1,198 | 2,702,240 | `__fern_arr_push` | `__fern_arr_push_owned` |
| 212 | 82,720 | 30-odd option / string / bigint sites, ≤ 20 blocks each | |

239,900 blocks and 467,410,944 bytes, 99.7% of it two rows. Both are the same
shape: a `u64[]` limb array bound from something the frame does not own, then
`.with` in a loop —

```fern
function __bi_mul_small(a: u64[], k: u64): u64[] {
    var out: u64[] = a;
    while (i < a.len()) { out = out.with(i, prod & mask); … }
}

pub function (a: BigInt) to_string(): string {
    var cur: u64[] = a.mag;
    while (i >= 0) { cur = cur.with(i, c / ten9); … }
}
```

An 80-bit long double of exponent 10^3160 needs ~330 limbs, so `to_string`
alone runs the inner `.with` ~10^5 times per value.

## The cause

`a = a.with(i, v)` on a slot that is ALIASED or BORROWED cannot store in place
(#3599: the write would land in the buffer the other owner still reads), so the
StmtAssign arm lowers it as `lower_arr_with_value` — an `arr_slice` clone plus
an `arr_set` into the clone — and stored the clone with
`emit_arr_store(wcl, slot, false, false)`. That last `false` is `do_dec`: the
store bound the fresh clone and dropped the pointer it superseded on the floor.
One clone per iteration, none released, and the loop's cost is the whole array
each time.

Every other array rebind already releases: the generic reassign path passes
`do_dec = target_is_arr`, the `own`-param `.with` releases its destination
before the copy, and the exit sweep decs the slot. The clone store was the one
array store with no give-back.

**The method-call taint was not it.** The lead this was picked up with pointed
at `rc_fe_rhs_tainted`'s `ExprFieldAccess` arm — a method result tainted by its
receiver with no `noesc` consultation — and an earlier attempt at that layer
moved od's frees 6535 → 6574, i.e. nothing. Free-eligibility never reaches this
shape: the release is owed at the STORE, and `.with` is lowered by its own arm
before any plan taint is asked.

## The credit, and what bounds it

`arr_slot_shallow_release_ok` decides it, and it is the exit sweep's own
condition read back slot fact for slot fact: a local (params route to
`emit_consumed_param_store` through their ownflag before `do_dec` is read),
`is_arr_slot`, not `is_borrowed_arr_slot`, not `moved_elided`, and none of the
deep-credit classes (arrtup / arrstruct / arrenum / arrarr / optaarr / cloarr /
structarr / strarr / dyn-element / ENVCAP). A slot that answers true is
released by one `__fern_rc_dec` at exit, so the same dec is what a store that
supersedes its value owes — cow-guarded by `emit_arr_store`, so a self-store
never frees the live box.

The three ways a slot reaches the clone arm all hold a count when they do:

- `var cur = a`, `a` a borrowed param — the alias bind retains (the ladder's
  array limb). Its two retain-eliding exits do not apply: move-on-alias
  TRANSFERS the count, and dead-alias cancellation requires neither name be
  reassigned, which `a = a.with(…)` is.
- `var cur = b.mag` — the scalar-array field bind is a Perceus dup
  (`retain_tos`), as are the struct-array and enum-array field binds.
- a bare array param — an ownflag, allocated for every non-`own` array param
  the body assigns, so the release is the flag's and this credit never sees it.

The deep classes are refused rather than given their deep release: the clone
shares the old buffer's element pointers, so freeing elements through the
superseded buffer would take them out from under it.

## Measured

`od -t fL` over the first N bytes of the fixture (`math/rand` seed 8314),
`FERN_LEAKCHECK=1` at emit, self-host x86-64:

| N | before | after | native |
| --- | --- | --- | --- |
| 26 | 8,948 / 488, live 4,041,256 | 8,948 / 8,771, live 84,744 | 604 / 584 |
| 48 | 241,478 / 1,578, live 467,410,936 | 241,478 / 241,230, live 108,624 | 1,831 / 1,805 |
| 96 | 2,002,072 / 4,435, live 11,928,306,872 | 2,002,072 / 2,001,725, live 186,328 | 4,973 / 4,938, live 4,480 |
| 300 | OOM-killed | 4,779,272 / 4,778,312, live 431,040 | 14,955 / 14,867, live 43,600 |

The trace histogram at N=48 after: 248 unpaired blocks, 108,640 bytes — both
`arr_slice` rows gone, the residue unchanged. Output is byte-identical to the
native build on the full 300-byte fixture.

Probes, each 100 `.with`es over a 4-element array (self-host x86-64, native in
brackets): a borrowed param aliased into a local 1006 / 6 → 1006 / 1006 [7 / 7];
a struct-field bind 1007 / 2 → 1007 / 1002; an aliased local 1006 / 1 →
1006 / 1001; `var h = H { xs: a }; a = a.with(0, 9)` 3 / 2 → 3 / 3 [3 / 3], same
answer throughout.

## Gates

`TestSelfHostWithCloneReclaim{X86_64,Arm64,Wasm}`: both od shapes in one
program, each source read back after its loop so a release of the live buffer
(or a clone writing through to it) is a WRONG ANSWER rather than a census
reading, plus `__rc_underflow_count()`. 205 / 5 at live 11,200 (x86-64 and
arm64) and 9,600 (wasm) before, 205 / 205 at live 0 after, exit 7 on all three
and in the interpreter.

## The volume is still 300x native, and it is a different gap

The leak is gone; the clone per iteration is not. Native's `.with` reads the
refcount at RUN time — `cmp dword ptr [rax-8], 1` then
`__fern_arr_cow_inplace` — so it copies once and mutates in place afterwards
(15k blocks on the 300-byte fixture against the self-host's 4.78M, 0.4 s
against 9.0 s). The self-host's gate is STATIC, and promoting it to the runtime
test is not a transcription: `__fern_rc_is_unique` is only an ownership oracle
where every alias is counted, and `struct_lit_unretained_borrow_field` records
that string, string[] and enum-array fields reach a new box uncounted. An
in-place write under a wrong uniqueness answer is a wrong ANSWER, not a leak,
so it wants its own measurement of which alias kinds are counted before any
credit is issued.
