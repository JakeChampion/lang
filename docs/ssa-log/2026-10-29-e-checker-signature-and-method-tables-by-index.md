# 2026-10-29 — the checker finds signatures and methods by a sized index

`checker.SigTable`, `checker.MethodTable`, `Scope.sig_at` and the method
lookups. Refs #8171.

## The shape

The checker kept its function signatures and its methods in hash tables
with a fixed count of 4093 buckets. The checker builds these tables for
each module. The merged module of the compiler has about 15000
signatures, so each bucket held a chain of about four. Each table also
filled 4093 slots, also for a small module.

## What changed

`SigTable` and `MethodTable` now hold a `util.NameIndex`. The index has
two buckets for each name. `MethodTable` has one index of the full
method names and one of the names without the receiver. The chains keep
the lowest entry first, as the old chains did, so each lookup finds the
same entry. The union table keeps the fixed buckets, because it also
chains a subset of its entries.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 3d1492c39. The base is main at d67454ba4.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 101.800 G | 101.729 G (−0.07%) |

The signature table alone was 101.740 G. The whole-compiler binary is
byte-identical to the base compiler's.
