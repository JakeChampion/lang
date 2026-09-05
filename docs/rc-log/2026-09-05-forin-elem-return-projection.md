# A for-in element returned by projection keeps its borrow (#8178, half 2)

*2026-09-05* — native `internal/ir`, walk 3 of `computeBorrowedAliases`; the
self-host side measured and found already in place.

## The shape

```fern
pub function decl_field_type(structs: StructTab, struct_name: string, field: string): string {
    var si: i32 = stab_first(structs, struct_name);
    if (si < 0) { return ""; }
    for fld in structs.decls[si].fields {
        if (fld.name == field) { return fld.type_name; }
    }
    return "";
}
```

The for-in element borrow (#6888) refused this loop: walk 3 kept a `returned`
set holding every ident that appears anywhere under a `return`, and `fld`
appears under `return fld.type_name`. So `fld` took a retain on bind and a
`__drop_struct_parser__StructFieldDecl` on every iteration, while the body
only ever read through it. #8178's six struct-table scanners were the loud
instance (`return sd.enum_owner`, `return sd.fields.len()`), and #8216's index
took the loops over `structs` away; the rule applies to every `for..in` whose
body returns a read of the element.

## The rule

`forinElemReturnsConfined` replaces `returned[y]` for the element binding
only (the bare-Ident alias of walk 2 keeps it). A return that mentions y is
admitted when y appears only projected — the target of a field access or the
base of an index — and the value is either

- a plain projection chain rooted at y of an rc-tracked type (`sd.f`,
  `sd.f.g`, `sd.xs[i]`): the Return lowering retains it on its own
  (`needsRcIncOnAlias`), so what leaves carries its own unit — the credit
  `returnedCountedProjection` gives a borrowed parameter under #8104, under
  the same `returnedAliasIsRetained` refusals (pair-form, TRMC); or
- a non-pointer value (`sd.fields.len()`, `sd.name == k`): nothing
  pointer-shaped leaves, and each sub-expression is the transient read it is
  in statement position, where `bindingConfinedToArm` already admits it.

Refused: y itself (move-on-return hands the caller an uncounted element), a
bare y as a call argument in the return value (return-position argument death
skips the growth bracket), and any other pointer-typed value built around a
projection — a fresh aggregate, `Some(sd.name)`, a concat, a tuple, a slice
view. Their counting is not shown here, so they keep the owned model.

## What it measures

`internal/ir`: each admitted spelling costs exactly its index-spelling sibling
plus the iterand's retain and its release at each of the two exits, with no
drop call (`TestForinElemBorrowReturnsProjection`); over a local or call-result
container only the returned field's transfer inc remains
(`TestForinElemBorrowReturnsFieldLocalAndCallIterands`); every refused shape
keeps strictly more `rc_inc` than the confined scanner
(`TestForinElemBorrowRefusesReturnEscapes`).

Runtime, all three backends (`rcCorpus`): `forin_elem_borrow_return_string_field`
and `_array_field` return a projection out of an owned CALL-RESULT container
— the sweep at that return deep-frees the container inside the callee, so the
transfer inc is load-bearing — and read the value back after same-size churn;
`_return_scalar` returns a read. Underflow counter 0, leak gate 0 on x86-64,
arm64 and wasm.

Native-built self-host driver, x86-64, compiling `checker.fern`:

| | base | new |
| --- | --- | --- |
| callgrind total Ir | 20,974,247,411 | **20,960,837,352** (−13.4 M, −0.064%) |
| emitted asm | md5-identical | |
| `FERN_LEAKCHECK` allocs / frees / live | 14,619,495 / 11,565,550 / 145,265,072 | identical, both runs |

Symbolized (`bin/fern -g`) driver, same input — the totals differ from the
stripped build by the symbol-table layout only:

| self Ir | base | new |
| --- | --- | --- |
| PROGRAM TOTALS | 21,023,293,202 | **21,010,835,860** (−12.46 M) |
| `__drop_struct_parser__StructFieldDecl` | 9,779,620 (0.05%) | **gone** — no row |
| `__drop_struct_parser__StructDecl` | 12,702,871 (0.06%) | 12,702,871, unchanged |
| `__fern_rc_inc` / `__fern_rc_dec` | 275,790,061 / 266,899,164 | identical (the fast path is inline; only the drop calls are calls) |

The `StructDecl` drop is not moved because its 401,939 remaining calls are
not a for-in: they come from `parser.settle_field_type`'s
`while (i < structs.len()) { var sd: StructDecl = structs[i]; … }` — an
index-bound element alias, the shape walk 2 (bare-Ident alias) and walk 3
(the synthetic iter) both leave alone. That is the next lead for this drop,
and it is a different rule from this one.

The issue estimated −1 to −1.5% of instructions for this half from the
398,181 `__drop_struct_parser__StructDecl` calls under `decl_field_type`.
Those calls were the deep-drop path of loops over `structs`, and #8216's index
already removed every such loop; what this rule takes away is the retain plus
the non-unique dec on the loops that remain, which is what the total says.
Allocation counts do not move because the drops being skipped never freed
anything — the container held the element throughout.

## The self-host side: nothing to port, two gaps named

The self-host never retains a for-in binder — it is an uncounted borrow of
the element the container owns — and its body walkers already read `sd.f` as
a borrow in every position (`expr_unsafe_for`'s FieldAccess arm is
position-independent). Measured with `FERN_LEAKCHECK=1`, 100 rounds, native
x86-64 against the self-host driver:

| probe | native | self-host |
|---|---|---|
| `for sd in xs` over a local, read-only | 800 / 800 | 800 / 800 |
| the same, `return sd.fields.len()` | 1600 / 1600 | 1600 / 1600 |
| over a param, read-only | 800 / 800 | 800 / 800 |
| over a param, `return sd.fields.len()` | 800 / 800 | 800 / 800 |
| over a param, `return sd.name` | 1100 / 1100 | **1100 / 900** |
| the INDEX spelling, `return xs[j].name` | 1100 / 1100 | **1100 / 900** |
| no loop, `return xs[k].name` | 600 / 600 | **800 / 600** |
| `for sd in mks(i)`, read-only | 800 / 800 | **800 / 100** |

Every row exits identically on both compilers. The two self-host leaks are
not the for-in rule:

- a returned rc-typed projection of a borrowed PARAM's element leaks one
  object per call whatever the spelling — the callee's transfer retain has no
  owner on the caller's side. That is the self-host's missing counterpart of
  #8104's returned-projection counting, a return-side gap;
- `for x in <call>`: `lower_foreach_snapshot` binds the iterand to a hidden
  local the credit scans never see, so the elements leak even under a
  read-only body.

`TestSelfHostForInStructElemReturnX86_64` pins all eight rows (the four
balancing ones at allocs == frees, the leaks as refused with their index and
loop-free controls), and the leak matrix gains `for_in_struct_elem__loop__read`
and `for_in_struct_elem__loop__return_scalar`, clean on both sides.

## Trap

`make selfhost-cli` strips the driver: callgrind on it reports every row as
`???:0x…`. Build the measured driver with `bin/fern -g` (what
`scripts/selfhost-alloc-bench` and the p8179 recipe do) — the totals agree
between the two builds, only the attribution needs the symbols.
