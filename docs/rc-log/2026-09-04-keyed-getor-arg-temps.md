# 2026-09-04 — the struct/enum-key `get_or` ends its argument temps

The keyKind-3 (`@derive(Eq, Hash)` struct / enum key) `get_or` path emitted both
non-receiver arguments inline, with no `stashOwnedArgTemp` /
`emitArgTempDrops`. `get_or` only READS the key, and a counted-read value
column retains the fallback on a miss, so both temps are dead at the call and
neither was ever ended.

## Measured

`Map[Point, i32[]]`, a miss every round, `FERN_LEAKCHECK=1` on x86-64:

| tree | N=50 | N=2000 | per round |
|---|---|---|---|
| before #7910 (a) | live 816 | live 32016 | 16 B — the struct key |
| after #7910 (a) | live 3216 | live 128016 | 64 B — key + fallback |
| after this change | live 16 | live 16 | **0** |

The residue is the map and its one entry, which this shape does not reclaim
either way, so the pin is bounded-across-N rather than a number.

## Two halves, one branch

The key half predates the wave: a fresh `Point { x: i + 100, y: 0 }` was
stranded every round on main.

The fallback half arrived with the miss-retain in #7910 (a). Its `!keyKind3`
arm ends the fallback temp; the keyed sibling was excluded from that arm and
got the retain with no matching release. So the same commit that fixed the
non-keyed column regressed the keyed one, 203 frees to 3 on the probe above.

The composite-key leak matrix could not see either: its rows all use `i32` /
`string` value columns, where neither temp is counted.

Reported by a review bot on #8215 as an unpinned pre-existing gap; the bisect
showed the fallback half was this wave's own regression.

## Pin

`Test{X86_64,Arm64,WASM}MapKeyedGetOrFallbackReclaim`: live bytes equal across
N=50 and N=2000, plus a correctness leg that reads the map's own value back
after 200 rounds (hit and miss, `i32[]` under a struct key and `string[]` under
an enum key) with `__rc_underflow_count()` folded into the exit. Fails on the
parent on both natives (3216 vs 128016).
