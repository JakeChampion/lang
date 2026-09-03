# Map insert is a counted store (#4399 sink 5)

`computeFreeEligible` escape-tainted the key and value SOURCE locals of
every `m.insert(k, v)` and `Map { k: v }` entry, on the reasoning the
store held them uncounted. It had not for a long time: `emitMapSetRetains`
incs an aliased string key on every ABI and an aliased value of every
kind the map drops (arrays, deep-dropped struct / enum / tuple boxes,
strings), and since `appendMapDropChain` (2026-09-01) every map-drop site
walks those columns. The store was counted on one side and read as
uncounted on the other, so a local that flowed into a map kept the
never-freeing flat dec — or, for a string, no release at all — and one
block per key / value was stranded per call.

## What changed

The lowering's retain decision is now a named predicate the analysis
reads: `mapSetKeyCounted` / `mapSetValueCounted` (ir.go) decide whether
`emitMapSetKeyRetain` / `emitMapSetValueRetain` emit an inc, and the
Map_set and MapLit arms of `computeFreeEligible` route a counted key /
value through no taint and an uncounted one through the old `escape`
walk (`escapeMapEntry`). The emitted retain ops are byte-identical to
before; only the eligibility verdict moves. The uncounted remainder is
exactly what the column never retains or drops: a struct / enum key
(kind 3, a raw pointer) and a kind-1 value (a nested Map, a slice, a
runtime handle). Both stay tainted, pinned at the op level
(`TestMapSetUncountedEntriesStayTainted`) and, for the nested map, at
runtime through a read of the inner map after its source scope ended.

## Measured

Insert-only shape — one fresh key and value local per call, stored, map
read by length only — 200 calls, FERN_LEAKCHECK on the natives,
`__heap_bump_bytes()` N=50 vs N=5000 on wasm:

| shape | x86-64 before → after | arm64 before → after | wasm B/call before → after |
|---|---|---|---|
| `Map[string, string]` | 800/400 live 12800 → 800/800 live 0 | 1200/800 live 12800 → 1200/1200 live 0 | 64 → flat (208 = 208) |
| `Map[string, i32[]]` | 800/600 live 6400 → 800/800 live 0 | 1000/800 live 6400 → 1000/1000 live 0 | 32 → flat (192 = 192) |
| `Map[string, Pt]` (string field) | 1000/800 live 6400 → 1000/1000 live 0 | 1200/1000 live 6400 → 1200/1200 live 0 | 32 → flat (224 = 224) |
| `Map { k: v }` literal, `i32[]` | 800/600 live 6400 → 800/800 live 0 | 1000/800 live 6400 → 1000/1000 live 0 | 32 → flat (192 = 192) |

Every leg reads `__rc_underflow_count() == 0` and the right sums before
and after, on all three targets. The read-back shapes (`m.get_or(k, v)`
after the store) also balance on arm64 and wasm: `Map[string, string]`
1600/1200 live 12800 → 1600/1600 live 0 on arm64, 64 B/call → flat on
wasm; the struct value likewise (1400/800 → 1400/1400; 96 B/call → flat).

## What this does NOT move, and why

Two residuals survived, both measured identical before and after, so
neither is this sink's:

1. **x86-64 only: the #4174 blanket string-argument taint fires for a
   BUILTIN's arguments.** Its comment says "user-function call", but the
   arm is the `default:` of the callee switch, gated only on the
   single-word ABI — so `m.get_or(k, "")` re-taints `k`, and the
   retained lookup result bound from it is tainted through rhsTainted and
   skipped by the string sweep. `Map[string, string]` insert + `get_or`
   by the same local: x86-64 800/400 live 12800 before AND after, while
   arm64 and wasm (no blanket arm) balance. FERN_RC_TRACE attributes all
   400 stranded blocks to `__fern_strcat` — the key and the value. The
   interprocedural credits cannot reach this: a builtin has no summary
   in `paramCountedRetain`. It needs a hand-audited per-builtin table on
   the model of `copyingBuiltinArg` — the Map lookups hash and compare a
   string key without retaining it, and `get_or` retains its fallback
   counted on every ABI (`isMapStringGetOr`).
2. **All targets: a builtin lookup's retained result is tainted by a
   tainted scalar argument.** `var got = m.get_or(i, [])` with `i` a
   parameter: `i` is borrow-tainted, rhsTainted's generic Call rule
   taints `got`, and `got`'s flat dec runs LAST in the sweep — after the
   map's freeing column walk — so the reference get_or retained is
   taken 1 → 0 by a dec that frees nothing. 800/400 live 9600 before
   and after on both natives (the value buffer plus the `[]` fallback
   temp, which the generic arg-temp reclaim refuses for a pointer
   result). The same rule strands `var r = pick(s, c)` where `pick`
   returns its string parameter bare, on every ABI.

## The blanket taint, measured on user callees

`count_a(s)` (indexes and `len()`s its parameter), `first_eq(s, lit)`
(compares it): 200/200 live 0 on x86-64 and arm64 — the Index, pure-read
and comparison credits in `stringParamCounted` have subsumed the taint for
callees that only read. `pick(s, c)` returning `s` bare on one branch:
200/0 live 6400 on BOTH ABIs, so that cost is residual 2 above, not the
blanket arm. What the blanket arm still uniquely costs is residual 1: any
string local handed to a builtin method.

## Gates

See the commit message for the suites run and their exit codes.
