# 2026-09-21 — a variant is a value, not a type head

`Shape.Circle(1)` and `Empty.to_string()` are the same shape in the tree: an
identifier, a dot, a name. The first is a qualified path to a constructor; the
second is a method call on a payloadless literal. `type_head` told them apart by
asking whether the identifier names a declared struct, and a payloadless variant
IS one — so `Empty` read as a type head, the call went down the qualified-path
arm, and the contract table was asked for `Empty.to_string`, which does not
exist.

The refusal spelled itself `call target has no semantic contract:
Empty.to_string`, which reads like a missing derive rather than a
misclassified call. Every other spelling of the same call already worked: a
binding of the variant, an annotated binding, and a payloaded
`Circle(1).to_string()`. Only the bare receiver went wrong, and it was the one
`examples/tests/derive_test` uses.

## The fix

A variant carries its enum owner, so the owner is what separates the two
readings: the sig a bare name resolves to is a value constructor when it
carries one, and not a type head. The enum's own name still is one, which is
what makes `Shape.Circle(1)` a qualified path.

That lookup answers whichever declaration registered first, so a name shared by
a plain struct and a variant resolves by declaration order. The corner is not
reachable: the self-host's checker refuses such a program before this reads it,
which is its own divergence from native (#9900).

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`, native x86-64 as the oracle.

| program | before | after | typed held |
|---|---|---|---|
| the three spellings side by side | 0 of 110 | 110 of 110 | 0 B |
| `examples/tests/derive_test` | 0 of 225 | 225 of 225, 22 tests pass | 0 B |

Both answer what native answers, on all four targets, and the two lowerings
print identical output.

Corpus census (865 seeds), both legs run here with the same script:

| | before | after |
|---|---|---|
| programs produced whole | 822 | 825 |
| declarations produced | 83,678 of 87,218 | 84,094 of 87,227 |

Three programs become whole: `derive_test`, and the `derive_debug` and
`hash_struct_enum` conformance cases. Nothing regresses.

## Traps

- **The refusal named the wrong layer.** `no semantic contract: Empty.to_string`
  points at the contract table, and the contract table was right: there is no
  such method, because `Empty` is not a type. Reading the key as a missing
  registration would have led to inventing contracts for variants; the key was
  a symptom of a classification made two steps earlier.
- **The working spellings were the clue.** Three ways of writing the same call
  produced and one did not, which rules out the callee and the derive machinery
  and leaves only how the receiver is read.
