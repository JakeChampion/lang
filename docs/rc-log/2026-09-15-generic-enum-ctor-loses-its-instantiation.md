# A generic enum construction typed as the bare enum, so its drop reclaimed nothing

#9313, filed while building the corpus case for #9299 and unrelated to it. An
enum construction handed straight to a call strands its box AND its payload:

```fern
function probe(o: Option[i32[]]): i32 {
    match (o) { Some(xs) => { return xs[1]; }, None => { return 0; } }
}
probe(Some(mk(8)))        // leaks
var o: Option[i32[]] = Some(mk(8)); probe(o)   // clean
```

200 rounds, `FERN_LEAKCHECK` at compile time, x86-64:

| payload | inline argument | through an annotated local |
|---|---|---|
| `mk(8)` | `800/400`, 19 200 B | `800/800`, 0 |
| `mk(64)` | `1400/1000`, 86 400 B | 0 |
| `mk(256)` | `1800/1400`, 316 800 B | 0 |

400 blocks stranded in every row — two per call, the box and its payload. The
issue's "the enum box and nothing else, 48 bytes a call" was wrong on that
count: the byte total scales with the payload, so what is stranded is the box
plus everything it holds.

## Not about argument position

The split the reproducer shows is inline-argument against bound-local, which
reads as an argument-temp ownership bug — the `countedArgTemp` /
`stashOwnedArgTemp` family the issue pointed at. It is not. Instrumenting both
spellings shows the classification and the stash agreeing exactly:

```
leak_user      stash callee=probe ai=0 ok=true tt=Holder  drop dropFn="__drop_enum_Holder" ok=true
leak_user_gen  stash callee=probe ai=0 ok=true tt=Holder  drop dropFn=""                   ok=false
```

The same program with a CONCRETE enum is clean and with a GENERIC one leaks, so
the real split is generic-vs-concrete. The argument position is only where it
shows, because the local-binding spelling never asks this question — its drop
comes from the local's DECLARED type.

## Cause

`builder.exprType`'s variant-constructor arm answered with the enum's name and
nothing else:

```go
if ename, _, _, ok := b.lookupVariantOn(id.Name, id.EnumName); ok {
    return ast.EnumType{Name: ename}
}
```

That arm exists for slot sizing — `(1, Some(42))` needs `IsPointerType` to be
true so the tuple packs a pointer-width slot — and for that question the type
arguments are irrelevant. They are not irrelevant to a DROP. `dropFnNameFor`
routes an `EnumType` with `Args` to a per-instantiation
`__drop_enum_Option_LB_..._RB_`, substituting the args into the decl and
registering the body with the worklist. With `Args` empty it takes the concrete
path instead, where `enumNeedsDrop` declines any decl carrying a `ParamType`
payload — which the un-cloned generic decl always does, generic enums keeping
one decl by design. The fall-through is the flat `__fern_rc_dec`, which has no
free path, so neither the box nor its payload is reclaimed.

An annotated local escapes this because `Option[i32[]]` is written in the
source and the local's recorded type keeps the args.

## Change

The checker already computes the instantiation and records it:
`Info.EnumConstructions[call].Type` is `Option[i32[]]`, settled against the
destination (`settleEnumConstruction`) — which is how the parameter's declared
type reaches a construction whose own args would otherwise come only from its
payload. The IR reads that map in two other places already. `exprType` now
prefers it, keeping the bare answer as the fallback for a construction the
checker did not record.

One line of behaviour, and the safety argument is that it is strictly more
information about the same value: every consumer that only needs the slot width
cannot tell `Option` from `Option[i32[]]`, and the one consumer that can —
`dropFnNameFor` — is the one that was getting it wrong.

## What it also fixed

Two conformance fixtures reached 0 and their census rows were banked:
`shadowed_builtin_variant` (2) and `tuple_elem_variant_pattern` (1). Both build
a generic enum; their rows were the box each stranded.

`option_of_array`'s 32-byte pin in the rc leak-gate baseline did NOT move. It is
a `match (pick([7, 8, 9]))` scrutinee rather than a construction, so it never
reaches this arm — a separate leak, still open.

## Corpus

Two cases, both clean, so the gate holds them at zero: a user generic enum with
the annotated-local spelling as an in-program control, and the builtin
`Option` / `Result` pair, which are generic decls with `ParamType` payloads
exactly as a user enum is. Against the unfixed compiler they leak 5760 and
11 520 bytes.
