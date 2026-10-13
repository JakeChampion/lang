# 2026-10-13 — a name index masks its hash

`util.NameIndex`: `name_bucket`, `bucket_count`, `name_index`, `chain`,
`find`, `added`. Refs #8171. No emitted byte changes.

## The shape

Every name lookup in the compiler goes through `util.NameIndex`: the
checker's scope tables, the record and enum schemas, the contract table, the
inliner's callee index. Over one compile of the compiler `find` ran 8.4 M
times and `chain` 12.2 M, each hashing the name through `hash_bucket`: a
call of its own, `__str_hash` inline, a mask, and `% n` for a bucket count of
`2n + 1` — the division's own guards against zero and minus one around the
`idiv` — about 59 instructions per lookup before the chain was even read.
`hash_bucket` alone was 1.31% of the whole compile, `find` and `chain` another
0.75%.

## What changed

The bucket count is the power of two above twice the names, the bucket is
the hash masked, and `find` computes it in place instead of through `chain`
and `hash_bucket`. The load factor stays at or below one half, and chains
stay ascending. `hash_bucket` remains for the fixed-size tables and the
module cache's checksum, whose values do not move.

## Measured

The stage-2 compile of `checker.fern` to a binary under callgrind, each
compiler built from its own tree by itself, both compiling one frozen copy of
main at 6fdb1671, warm certifier memo.

| | Ir |
|---|--:|
| main at e413bff7 | 14.830 G |
| a masked hash | 14.748 G (−0.55%) |

The compiled `checker.fern` is byte-identical, and the compiler rebuilds
itself byte for byte.
