# 2026-10-02 — the borrow registry is stored to in place

`fnsigs.borrow_reg_set`, `ssarc.borrow_rows`. Refs #8171. No emitted
byte changes: the `selfhost-emit-hashes` sweep is 1,965 rows per
compiler with 0 differing against a compiler built from main at
e2ed8f3a, and the `checker.fern` binaries are byte-identical.

## What the profile named

`ssarc.caller_sigs` was 635 M inclusive on the 31.09 G stage-2 compile of
`checker.fern`, from one call: it rewrites the borrowability rows of every
verified callee, 3,550 of them, through `borrow_rows`, which calls
`fnsigs.borrow_reg_set` twice. `borrow_reg_set` took the registry
borrowed and stored with a value-producing `.with`, so each of the 7,100
stores copied all 4,093 bucket strings, retaining each (287 M in
`__fern_arr_inc_elems`, the largest single caller of `__fern_rc_inc`),
and released the previous copy. Its comment said the copy happened once
when the registry was shared; a borrowed parameter is always shared from
its callee's side, so it happened every time.

## What changed

`borrow_reg_set` and `borrow_rows` take the registry as `own` and
self-reassign through the store, the form `borrow_reg_put` already used
for the same reason (its comment: the bare-parameter receiver has no
struct field to key the in-place analysis on, so the value form clones
the buckets per insert and the move form stores into the sole-owned
buffer). `caller_sigs` already reassigned its local through
`borrow_rows`, so its one copy of the module's rows, made at the first
store because `sg` still holds them, is the only one left.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at e2ed8f3a and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 31.09 G | 30.47 G (−2.0%) |
| stage 2, `caller_sigs` inclusive Ir | 635 M | 14 M |
| stage 2, `borrow_reg_set` inclusive Ir | 412 M | 5 M |
| stage 2, `__fern_arr_inc_elems` inclusive Ir | 643 M | 353 M |

## Witnessed

`TestSelfHostBorrowInfer*`, `TestSelfHostArrEnumBorrowedArg*`,
`TestSelfHostArrEnumCountedParam*`, `TestSelfHostArrStructBorrowedArg*`,
`TestSelfHostArrStructCountedParam*`, `TestSelfHostAliasedParamBorrow*`,
`TestSelfHostBorrowedFieldArg*`, `TestSelfHostLentRecordRelease*`,
`TestSelfHostFixtureSourcesCheck` (the fixture that seeds this
registry type-checks under the native checker),
`TestSelfHostSSAPhysicalRCRejects` (the stage0 pin compiles and runs
it), `TestSelfHostCheckerCodesX86_64`, the
lint ratchet, `make fmt-check`, and the emit-hash sweep.

## Next

The same profile, by instruction class over the whole compile
(`--dump-instr=yes`): register-to-register moves 11.6% (3.57 G, of which
frame setup and return-value moves about 0.9 G, phi parallel moves
633 M, two-address copies 457 M), push/pop 9.3% (2.86 G; `__fern_alloc`
alone 243 M), `movslq` 2.3%, and every remaining bounds check 2.8%
(865 M over 10,989 sites, the three hash loops 142 M). By call count:
`__fern_alloc` 40 M calls, `__fern_arr_box` 28 M (10 M from
`__fern_arr_push` copying a shared list on append), `__fern_rc_inc`
29 M, `__fern_str_eq` 20 M. `semsource.define` copies the lowering
State's `values` on every definition (118 M in `arr_inc_elems`, and the
old State's release is 6 M `typeinfo.Type` releases): the State is
threaded borrowed through the whole lowering. `x86_gas_trim` takes a
`string`, so each of its 1.47 M calls copies its view argument once at
the call and once in the trim.
