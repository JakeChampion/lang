# 2026-09-20 — a struct-method clone is not a template

The second root of the typed-lowering census
(`2026-09-20-the-os-floor-has-contracts.md` has the table): `uninstantiated
generic`, 117 sites, all of them cascades — `call target was refused:
uninstantiated generic` on `main` and every test of the ordmap, ordset,
pmap, pset and set suites and the two persistent-map benches — with no
direct refusal anywhere, since a template's refusal is silent by design.

## The cause

`parser.clone_struct_method` clones a generic struct's method once per
receiver instantiation: `insert[K: cmp.Ord, V]` on `OrdMap[K, V]` becomes
`OrdMap__i32__i32.insert` with every `K` and `V` substituted. It emptied
`type_params` and copied `type_param_count` from the method, so a method
that redeclares its receiver's variables (the whole of `std/ordmap`,
`std/pmap`, `std/set` and their kin, which write `[K: cmp.Ord, V]` on each
bounded method) came out with a count of 2 and no variables.
`semsource.generic_decl` reads either field, so the clone was a template;
nothing requests an instance of a concrete method, so `template_verdict`
refused it as uninstantiated, and `close_module` refused every caller that
named it. `clone_bg` had the identical field and was fixed on 2026-09-16
(`docs/SELFHOST-SEMANTIC-SOURCE.md`, "the leaves that are left"); this is
the struct-method copy of that bug, one field.

A method with no own variables (`len()` on `OrdMap[K, V]`) never had the
count, which is why a program that only constructs and measures a map
produced whole while one that inserts into it did not.

## Measured

Corpus census, x86-64, `FERN_SEM_IR_REPORT=1`, 864 programs:

| | before | after |
|---|---|---|
| declarations produced whole | 69,892 of 86,874 (80%) | 70,880 of 86,874 (81%) |
| programs produced whole | 756 of 864 | 762 of 864 |
| `uninstantiated generic` cascade sites | 117 | 0 |

No program's compile status changed. The compiler built with the fix emits
`fern.fern` byte-identically to the one built without it, on x86-64 and
arm64: the compiler's own sources hold no method of this shape.

`TestSelfHostSemanticProduction` gains `generic-struct-method-redeclares-
receiver-vars` (a `Pair[T]` with `put[T](v: T)`, produced 0 of 2 before)
and `ordmap-bounded-method-clones` (`insert` and `get_or` through the
standard library, 0 of 69 before, 69 of 69 after), each compiled through
both lowerings on x86-64, x86-64 under the sanitizer, arm64 and wasm.

## What the same programs refuse now

Of the eight programs the cascade held, six produce whole. `ordmap_test`
and `pmap_test` stop at `for_each`, whose callback is `(K, V) => void`: the
verifier's `function signature slot` refuses a function type with a void
result, and `indirect_call` refuses a void call outright. Those two rows —
`function signature slot` 21, `unsupported void call` 20 — are one root
(a function value whose call yields nothing) and the next slice by size
after the verifier's `function value is not a field` (76) and the
remaining OS-floor contracts (70).
