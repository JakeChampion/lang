# Three type predicates dispatch on the variant once

Three predicates on `typeinfo.Type` tested the type against each variant in turn:

- `semtypes.equal` ran 4.2 M times in one compile of the subject. It tested its first type through three helpers, and a struct went through six tests before its own test.
- `ssasem.reference` ran 3.5 M times. It called `semtypes.is_handle`, which called two more predicates, and then it tested each reference variant.
- `semtypes.concrete` ran 2.4 M times. It asked four helpers in turn, and each helper tested its own variants.

Each predicate is now one `match` on the type, and each arm tests only its own variant.
A type has one variant, so the order of the old tests did not change a result.
`ssasem.reference` returns false for a struct that names a descriptor handle, as `is_handle` did.
The seven helpers that only these predicates used are deleted.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,767,184,252 |
| `equal` only | 29,733,430,167 (−0.11%) |
| this change | 29,670,691,978 (−0.32%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
