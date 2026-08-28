# All-scalar enum arrays free their element boxes (#7678)

An enum whose every variant carries only scalars got NO element release for
its arrays. Two classifiers closed every candidate arm, and either alone was
insufficient — the trap #7678 records is exactly that: widening the walk gate
alone moved nothing, because the literal and append element admissions had
already refused through `fresh_rcpayload_enum_init`, whose rc-droppable set is
deliberately disjoint from the all-scalar set the match-consume machinery owns
("a pure-scalar enum is handled by consumed_scalar_enum_frees, not here").
That division of labor was the bug: `consumed_scalar_enum_frees` handles
scalar-enum MATCH consumption, and nothing handled scalar-enum ARRAYS. The
exit sweep's bare buffer dec stranded every element box — a constant leak per
array, invisible to exits (native and self-host agreed throughout).

## The fix, in two halves

- `arrenum_lit_enum` and `arrenum_self_append_elem` fall back to
  `fresh_scalar_enum_init` beside the rc-payload admission.
  `enum_all_variants_rc_droppable` itself is untouched — its disjointness is
  load-bearing for the match-consume machinery.
- The two credit-side walk gates (`arrenum_append_built_enum`,
  `arrenum_producer_enum`) route through `enum_arr_release_walk_ok`, a
  WRAPPER: `enum_arr_elems_walk_ok || all-payloads-scalar`. The wrapped
  predicate keeps its meaning, so the field-walk emitters keyed on it emit no
  empty-dispatch helper for the scalar case.

No emitter or constructor change: `emit_enum_variant_drops` already performs a
box-only free for a payload-less variant (its payload-drop emitter emits
nothing, then the unconditional box dec), and a scalar-payload ctor takes no
retain, so the array is the box's sole owner and the walk balances exactly.
The `any_arr` clause the walk gate keeps protects a PAYLOAD dec; an all-scalar
variant has no payload dec to protect.

## Measured (x86-64, native exits matched throughout)

| shape | before | after | native |
|---|---|---|---|
| producer keep (`mkv(5)`), extract + match callee | 3/2, 40 live | **3/3, 0** | 3/3 |
| literal keep (`[Tag.Box(5), Tag.Nil]`) | 3/2, 40 live | **3/3, 0** | 2/2 |
| scalar shapes through the ELB alias/direct admissions | 3/1, 72 live | **3/3, 0** | 2/2 |
| rc-payload sibling (control) | 4/4 | 4/4 (unmoved) | 4/4 |

Sanitize leg on both scalar flavors: zero findings — no over-release, no UAF,
and the leak line gone. The self-host's extra alloc against native is the
existing #7351 class, untouched.

## Gates

Pinned by `self_host_scalar_enumarr_reclaim_test.go` (three rows, counts
asserted; exit 99 is the over-widening detector — the census alone scores a
double-freeing compiler higher). Green: every ArrEnum/ArrStruct suite, the
scalar-enum and enum suites, all four matrices (no row moved), rc corpus both
legs, and `TestSelfHostStage2FixpointArm64` per the exit-sweep-credit rule.

## Next lead

The match-expression IIFE form from the ELB entry still stands. The literal
row's native column (2/2 vs the self-host's 3/3) is #7351's two-allocation
spread, not a divergence.
