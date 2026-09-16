# 2026-09-16 — a map literal takes its shape through the chain

`Map { "a": 40, "b": 2 }` desugars to `map_new(2).insert("a", 40).insert("b",
2)`, and the chain's head reached `map_construction` as the receiver of an
`insert`, produced with no expectation: the checker types that `map_new`
as a map of unknown columns, so the construction refused "unsupported map
shape" with nothing to spell. Three corpus sites.

An insert hands its receiver back, so the receiver's shape is the call's:
`method_call` now produces the receiver of an `insert` or `set` at the
map type its own destination expects, and the expectation reaches the
head through the chain. An integer-keyed literal's head is spelled
`map_new_i32`, the same construction under the other name, and is
admitted beside it. `map_literal` produces whole and matches native
under the leak check; the two `foreach` sites beside it wait on map
iteration.

## Pinned

`TestSelfHostSemanticSourceRC` runs `map_lit_words` (a string-keyed
literal whose key is built at the site) on all four targets under the
leak check; the print golden carries `map_lit`.
