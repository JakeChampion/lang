# 2026-10-12 — a known variant is released by what it holds

`ssaunits.known_variant`, `Reuse.reads`; `ssarc.drop_known`,
`ssarc.token_children`. Refs #11944.

## The shape

```fern
match (left) {
  Val(a) => {
    match (right) {
      Val(b) => { return Val(a + b); },
```

`left` and `right` die in the inner arm, where reading their payloads has
already fixed both to `Val`, which holds no counted field. One was dropped
through `__sem_release_Expr`, which tested the count, called
`__sem_drop_Expr` to test the variant against every variant with counted
fields, and freed the box; the other became the token through the same
`__sem_drop_Expr` call. cfold called the drop helper 160,000 times a run,
and 115,600 of those found nothing to walk.

## The rule

The reuse plan already knew which variant a dying value holds where a
payload read of it dominates the drop (`VariantReads`, which sized token
donors). The plan keeps it (`Reuse.reads`), and a drop or a token taken
where it fixes the variant is lowered for that variant:

- a variant with no counted field: the box is released alone
  (`__fern_rc_dec`), and a token gives up nothing;
- one or two counted fields: they are walked in place under the
  uniqueness test the release or the token already makes;
- more, or a type with a finalizer: the helper, as before.

## Measured

`TestSelfHostKnownVariantDropX86_64` pins `vals`, whose two arms drop a
`Val` read in place: three calls of the release helper where main makes
five. x86-64 instructions (callgrind, outputs identical), on top of the
classified donors:

| entry | before | this change |
|---|--:|--:|
| perceus_cfold | 68,863,667 | 67,135,667 |
| perceus_deriv | 99,761,608 | 95,830,408 |
| pmap_insert | 66,718,326 | 64,758,246 |

nqueens, rbtree and rbtree_ck are unchanged: their dying values are
nodes whose children need the walk. The compiler reproduces itself and is
16 KB smaller.
