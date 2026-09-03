# 2026-09-03 — a struct-update spread base is released on the fresh-box fact (#7914)

## What was wrong

`T { ...mk(), f: v }` evaluates `mk()` into a scratch slot, copies each
un-overridden field into the new box with a retain, and then releases the base
— but only when `structUpdateBaseIsOwned` admits it. Its Call arm asked for
`returnsNoParamEscape`: nothing reachable from a parameter may appear anywhere
in the result.

That is far stronger than the release needs, and unreachable for a builder
function. `irlower.fn_sigs_for_borrow` returns a 40-field registry struct; a
`FERN_DBG_NPE` dump over the whole self-host shows **32 of its pointer fields
refused at once**, because each is a call whose own callee threads a parameter,
and four are locals shared between fields. The verdict is a single bit per
function, so one param-carrying field costs the release of the other 39.

The fact the release actually needs is that the caller holds a unit of the base
box. That is `returnsFreshBox` — the same oracle a LOCAL bound to the same call
result reclaims on, through the same `dropStructField`, under the same
`is_unique` gate. The two spellings disagreed for no reason the accounting
supports.

## The change

`structUpdateBaseIsOwned`'s Call arm is `returnsFreshBox[name] ||
returnsNoParamEscape[name]`. Nothing else moves; the drop it gates is unchanged.

Why the wider fact is enough, in one line: the drop is `is_unique`-gated, so a
base something else still holds is only decremented, and a fresh box AT rc 1
owns its pointer fields — otherwise the exit sweep of `var b = mk()` would
already over-release through the identical helper.

## Measured

Probe (`spread.fern`): a `@noinline` producer returning `Sig { a: string[], b:
string[], c: string }` with non-SSO payloads, 30 rounds,
`__rc_underflow_count()` folded into the exit, answers checked against
`-interp`.

| shape | x86-64 before | x86-64 after | arm64 before | arm64 after |
| --- | --- | --- | --- | --- |
| `Sig { ...mk(4, seed), c: pad(seed, 5) }` | 1050/690, **19,680 B** | 1050/1050, **0** | 46,560 B | **24,480 B** |
| `var b = mk(4, seed); Sig { ...b, c: … }` | 1050/1050, 0 | 0 | 24,480 B | 24,480 B |
| `var s = mk(4, seed)` (no spread) | 870/870, 0 | 0 | 17,760 B | 17,760 B |

Alloc counts are identical across the change on every row: this is placement of
releases, not of allocations. **Both spellings now read the same number on both
backends** — which is the claim, and it is what the arm64 column shows most
plainly: the residual 24,480 B there is the bound spelling's own pre-existing
gap (the same 24,480 with and without this change), not something the base
release leaves behind. It is a two-word-ABI shape and is not touched here.

Self-host driver, native-built, compiling a fixed 4-loop input to x86-64 asm
under `FERN_LEAKCHECK=1`:

| | allocs | frees | live bytes |
| --- | --- | --- | --- |
| before | 30,831 | 25,515 | 392,720 |
| after | 30,831 | **25,883** | **363,360** |

**−29,360 B (−7.5%), +368 frees**, emitted asm byte-identical (md5
`460d46c90c561dc2965a794779dbcb9f` both sides).

Nine lowering sites in the whole self-host reach this predicate with a Call
base, and all nine were refused: `irlower__fn_sigs_for_borrow` ×3,
`irlower__fn_sigs_with_dyn` ×2, `parser__no_match_sugar` ×4. All nine are
`returnsFreshBox`.

## How it was found

The reverse-reachability census on #7914, re-run against `27e0ee4a4`: an
in-gdb `FERN_RC_TRACE` a/f stream paired by pointer into a survivor set, every
8-byte word inside each survivor scanned for a pointer into another survivor,
zero-in-degree blocks taken as roots, each root ranked by bytes reachable ONLY
from it. 5,316 survivors / 413,760 B, 1,167 roots holding 118,112 B.

```
root creator                                       roots exclblk  exclbytes
__fern_alloc_rc1 <- __fern_strbuf_take                 2       2      65536
__fn_irlower__fn_sigs_for_borrow                       4     257      15904
__fn___method_checker__Scope_bind                     54     194      13536
__fern_arr_push_grow_ptr <- irlower__assign_target_in 155     171       8112
__fern_arr_push_grow_move_ptr <- parser__settle_modul   2     141       5552
__fn_parser__settle_module                              4      90       4192
__fn_parser__resolve_labels_module                      4      88       4096
__fn_irlower__fn_sigs_with_dyn                          1      17       3936
```

Row 1 is the caveat row and was not worked: the census scans only the arena, so
the emitted module text the driver is still holding at exit reads as a root.
Row 2 is this entry. The `FnSigs` box's registries are exclusively its because
the `sg` the spread produces DOES reclaim — its fields drop back to the base
temp's reference and stop there.

## The frontier this leaves

Same census re-run on the fixed driver: 4,948 survivors / 384,400 B, 1,247
roots holding 118,736 B. Both `fn_sigs` rows are gone from the ranking; nothing
took their place, and the head of the list is now flat.

```
__fern_alloc_rc1 <- __fern_strbuf_take                 2       2      65536   (live at exit)
__fn___method_checker__Scope_bind                     54     194      13536
__fern_arr_push_grow_ptr <- irlower__assign_target_in 156     174       8224
__fern_arr_push_grow_move_ptr <- parser__settle_modul   2     139       5520
__fn_parser__settle_module                              4      90       4192
__fn_parser__resolve_labels_module                      4      88       4096
__fn_checker__annotate_func                             4      83       3888
```

`Scope_bind` is the mixed-return / Scope-lookup class this issue records as a
LOWERING decision rather than a credit, and it is deliberately not touched
here. Its cost, measured on the current tree over the whole self-host (only
functions whose return type is rc-tracked):

| | functions | static call sites |
| --- | ---: | ---: |
| already prove `returnsFreshBox` | 2,848 | — |
| **mixed-return** (fresh on some paths, an uncredited alias on others) | **215** | **979** |
| all-borrow (no return path constructs) | 841 | 4,158 |

Normalising the mixed set to uniformly-owned returns costs an inc at those
return sites and a dec at each caller — 979 static call sites, paid per
execution, so a mixed-return callee in a hot loop pays per iteration. The
all-borrow set must stay out of it: its callers are right to borrow, and an
inc/dec pair there is pure overhead against 4,158 sites. The figures on this
issue's earlier comment (587 mixed / 2,127 all-borrow) predate the
returned-alias credit, which moved most of that population into the
already-proven column.

## Banked

Two corpus cases, both 0 on x86-64, arm64 and wasm, so neither joins the leak
gate's table:

- `struct_update_base_fresh_call_result_released` — the leak, 256 B on x86-64
  and 304 on arm64 at three rounds before the change. `mkreg` puts a parameter
  in one field, which is what makes `returnsNoParamEscape` false while the box
  is plainly fresh, and `seed` stays live and is read through the copy, so the
  sharing hazard is pinned alongside.
- `struct_update_base_passthrough_keeps_caller_box` — the interlock. The base
  is the caller's live struct returned bare by a passthrough; `returnsOwnBox`
  credits that (an owned-by-default parameter the caller retained on the way
  in), so the base IS released here and `base` must survive it. 128 B / 144 B
  before, 0 after, with a post-loop read pinning the string contents.

## The self-host has the same leak, and none of the machinery

`lower_struct_lit` spills a non-ident functional-update base into a `$fub`
scratch local and never releases it — there is no self-host equivalent of
`structUpdateBaseIsOwned` at all, for any base shape. So this is not a new
native/self-host divergence, it is one more entry on the reclaim side of goal 2,
and the shape is now pinned by two corpus cases the port can measure against.

## Traps

- **The probe's producer must not be constant-foldable.** The first version
  built its strings from two literals; the concat folded, the producer
  allocated six blocks a round instead of thirty, and the shape read
  `180/180/0` — clean, on a compiler that leaked. Thread a runtime seed
  through.
- **`use` is a keyword.** A probe callee named `use` fails to parse with
  `P001: expected "Ident"`, which reads like a lowering bug in a shell script
  that only greps for the leakcheck line.
- The census is over an IN-GDB trace: gdb shifts the allocation sequence, so
  the survivor addresses in an ordinary run do not match the arena dump. Its
  root list is a SUPERSET of the garbage — a value live on the stack at exit
  has no arena holder either.
