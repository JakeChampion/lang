# 2026-09-21 — an array helper is a free function

`examples/tests/array_combinators_test` produced **0 of its 211 declarations**
on one call:

```fern
var s: string = xs.join_with_last(", ", " and ");
```

```
FERN_SEM_IR: main: unsupported call target: string[].join_with_last
```

Seven lines reproduce it, and the same refusal covers every one of std/array's
helpers rather than this one.

## Two spellings of one surface, and only one was keyed

An array method can be written two ways, and the standard library uses both.

The RECEIVER form — `(xs: T[]) first()` — is folded by the registration pass
into a free generic named `__arrm_first`, with the receiver at argument 0.
`semsource.folded_method` keys that prefix and invokes the template.

The FREE-FUNCTION form — `pub function __method_Array_join_with_last(arr:
string[], sep: string, last: string)` — is the convention std/array is
actually written in, and it is most of the module: `cumsum`, `median`,
`every_positive`, `distinct_count`, and the rest. The checker resolves it for
`xs.<field>(...)` through `has_array_method` and `array_method_ret_type`. The
typed path had no arm for it at all, so every call fell through
`builtin_call`'s shape match, then through `folded_method`'s `__arrm_` lookup,
and refused.

`folded_method` is where it belongs — it is already the arm for "a method
declared on a generic receiver", and this is the same surface declared the
other way round, as the checker's own comment beside `has_array_method` says.

The lookup is a SUFFIX match for the same reason the checker's is: the bundler
prefixes the module, so the contract is named
`array____method_Array_join_with_last` rather than the bare convention name.
Both spellings are accepted, because the native modload path keeps it bare.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`.

| program | before | after |
|---|---|---|
| `examples/tests/array_combinators_test` | 0 of 211 | **211 of 211**, and 5 of 5 instances |

It passes 24 of 24.

Corpus census, 864 seeds, both columns against frozen binaries:

| | before | after |
|---|---|---|
| programs produced whole | 842 | **843** |
| declarations produced | 77,541 of 78,504 | **77,752 of 78,504** |

**Exactly one file moves and +211 is exactly its gap.** Nothing regresses. The
remaining corpus gap is 752 declarations over 21 files.

On the test-row program the typed leg reclaims the shape whole,
`allocs=7 frees=7 live_bytes=0`, where the AST leg strands **40 bytes**.

## Tests

`an-array-helper-is-a-free-function` — 54 of 54, `noLeak`, all four targets,
differential against the AST leg. Three helpers over two element types:
`join_with_last` on `string[]`, `cumsum` and `every_positive` on `i32[]`, so a
lookup that found the wrong arity or the wrong element type could not pass.
