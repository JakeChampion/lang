# 2026-09-20 — a map is walked through its two columns

`unsupported iterable: Map[i32, i32]` was the largest leaf left on the
census after the lambda work — seven programs — and the note on
`map_method` said why: "the iteration has backend ops but no contract
here yet."

It needs none of its own. `for (k, v) in m` is two column snapshots and an
index loop over them: `map_keys` and `map_values` already hand back fresh
arrays the frame owns (a string column retained per element, an `i32`
column copied whole), the runtime answers both in the same order, and the
key column's length is the loop's bound. `iterate_array` takes the key
column as the array it indexes and reads the value column's element beside
each key; the two names come from the checker's own split of the pattern,
which already types them as the map's key and value.

## Measured

```fern
var m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
m = m.insert(4, 40);
var (m2, had) = m.without(2);
for (k, v) in m2 { t = t + k * v; }
var names: Map[string, i32] = Map { "a": 1, "bb": 2 };
for (k2, v2) in names { n = n + k2.len() * v2; }
```

| leg | answer | `FERN_SANITIZE=1` |
|---|---|---|
| semantic, before | refused, the AST lowering stands | — |
| semantic | 65 | allocs=15 frees=15 live_bytes=0 |
| AST | 65 | leaks 168 bytes in 4 blocks |

The deleted key stays deleted and the string keys are walked through the
string dec. The AST lowering leaks its snapshots — the "bounded per read"
leak its `keys` comment records — and the produced loop does not, which is
the reclaim result of the entry.

The compiler's own sources produce 8617 of 8617. On the 512-program
fernsmith census, x86-64, each program built on both legs and run: 451 to
456 of 508 whole, 499 agree, 0 diverge, and `unsupported iterable: Map`
appears in no report. The map shapes still refused are the ones
`is_supported_map` refuses everywhere: a wide or float value column, and a
key that is neither a string nor a narrow integer.
