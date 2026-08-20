# The element `lines` dropped was never reclaimed on the register backends

The lead left by `2026-08-20-wasm-split-helper-scratch.md`. 400 rounds of the
churn harness, a pair of compilers from the same commit:

| shape | x86-64 | arm64 | wasm |
| --- | --- | --- | --- |
| `lines`, 4 lines + trailing `\n` | 9600 → **0** | 9600 → **0** | 0 |
| `lines`, no trailing `\n` | 0 | 0 | 0 |
| `lines`, 1 line | 0 | 0 | 0 |

24 bytes per call, and only with a trailing newline: one view box, the element
the trim threw away.

## Cause

`asmcore.rt_src_str_lines` — the register backends' `__fern_str_lines`, written
in Fern — split on `'\n'` and then dropped the trailing empty entry with
`parts[0:keep]`. The comment above it explained why: the hand-asm it replaced
trimmed by decrementing the result array's length word in place, and Fern cannot
mutate that word, so it sliced instead.

The slice is where the box goes. `parts[0:keep]` shares element pointers with
`parts`, so nothing may release `parts`' elements — the kept ones are live in the
result — and the dropped one is unreachable from the returned array. There is no
spelling in Fern for "release exactly this one element", so no amount of reclaim
analysis could have fixed it at that shape.

## Fix

Stop producing the element. The helper now scans for the newlines itself and
appends exactly the lines wanted:

```fern
var out: string[] = [];
var start: i32 = 0;
var i: i32 = 0;
while (i < sl) {
    if (s[i] == 10) { out = out.append(s[start:i]); start = i + 1; }
    i = i + 1;
}
if (start < sl) { out = out.append(s[start:sl]); }
```

A trailing newline leaves `start == sl`, so the final append does not happen —
the trailing empty line is never built rather than built and discarded. Same loop
`__fern_str_split` already runs, so the dependency set collapses to its:
`str_lines` needed `heap`, `str_split` and `arr_slice`, and now needs `heap`,
`arr_push` and `arr_push_owned`.

## Why this shape rather than a targeted release

Both alternatives are worse. A `split` variant taking a part limit adds a
primitive and a second code path for one caller. Teaching reclaim about
"drop-a-suffix" means proving, per element, that a slice's source may release
exactly the elements outside the slice — a real analysis for a case that
disappears entirely if the helper simply does not build the element. The rule
this follows: the cheapest fix for garbage is not creating it.

## Verification

The semantics probe is the load-bearing test, not the byte gate — this rewrites a
helper rather than adding a release. It covers trailing, interior and absent
newlines, the empty string, and a single line with no newline, and it runs on all
three backends, where wasm's separate `$__fern_str_lines` acts as an unmodified
oracle. Identical answers before and after on every leg, and identical to wasm's.

The byte gate (`wasm-lines-scratch-trailing-newline`, register ceiling tightened
from the slack 16000 its predecessor left to 4096) fails with 98 on the parent
and only for that case; the other cells were already 0.

The **arm64 NATIVE path** was checked directly with `-target arm64-linux` rather
than only through the cross-gcc test leg — the same gap that bit
`2026-08-20-builtin-strarr-view-element-free.md`, where a missing runtime label
passed the whole gcc-based leg and refused every program in-process.
