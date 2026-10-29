# 2026-10-29 — the associated-constant rewrite skips bodies without a read

`parser.rewrite_assoc_const_reads` and `astwalk.rewrite_assoc_const_reads`.
Refs #8171.

## The shape

The rewrite finds each read of an associated constant, such as `V2.LANES`
or `Self.LANES`, and marks it as a constant read. It rebuilt the body of
every function to find these reads, and most bodies have none. It runs
on each module and again on the merged module. Each field read on a name
also searched the list of type names of the module, which holds about
600 names in the compiler.

## What changed

A scan that allocates nothing goes before the rewrite. It counts the
field reads on a type name that are not the callee of a call. The rewrite
changes only those reads, so a body without one is kept as it is.

The type names of a module are now in one `util.NameIndex`, built once
for each module. The type parameters of a function stay in a short
list. `astwalk.TypeNames` holds the two.

A check build ran the rewrite on each body that the scan skipped. It
found no body that the rewrite changed. The check covered the whole
compiler, `simd_test`, `dns_test`, `publicsuffix_test` and a program
that reads constants through a type, a bound and `Self`.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 3d1492c39. The base is main at 4e1b712b9.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 102.937 G | 102.133 G (−0.78%) |

The scan alone, with the list search, was 103.356 G (+0.41%). Most field
reads are on a local name. The rewrite tests that first, but the scan
does not, so each read searched the full list of type names.

The whole-compiler binary is byte-identical to the base compiler's.
