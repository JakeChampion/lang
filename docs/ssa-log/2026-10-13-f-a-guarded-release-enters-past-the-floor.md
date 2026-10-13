# 2026-10-13 — a guarded release enters past the floor

`asm_ir` and `asm_arm64_ir`: `__fn___fern_arr_dec.u`, `__fn___fern_str_free.u`,
`ssa_rc_dec`, `ssa_release_entry`. Refs #8171. Emitted bytes change at every
release site.

## The shape

A release is rendered inline where the count survives it: the guard tests
the heap floor (every null), a string's tag bit, the static sentinel and a
count above one, which it decrements in place. Only a count of one or zero
reaches the helper, through its register entry, which then tests the floor
(and the tag) again before reading the count. On the stage-2 compile of
`checker.fern` the inline guard calls `__fern_arr_dec` 19.3 M times, and
the compiler's own text holds 22,900 such calls. The generated release
helpers do the same: their head proves a count of one before calling the
drop and then the free.

## What changed

Each release helper gains a `.u` entry just past its floor test (and, for a
string, its tag test), where the count is read: a caller that has already
tested the floor enters there. The inline guard and the release helpers'
heads call it. The `.r` entry is unchanged for every other caller.

## Measured

The stage-2 compile of `checker.fern` to a binary under callgrind, each
compiler built from its own tree by itself, both compiling one frozen copy of
main at 6fdb1671, warm certifier memo. The base is the array-box entry
before this one.

| | Ir |
|---|--:|
| the array box pops its own class | 14.370 G |
| the `.u` entries | 14.334 G (−0.25%) |

The stage-2 compiler itself calls `__fn___fern_arr_dec.u` from 22,813
sites and `.r` from 107.

The compiler rebuilds itself byte for byte. `TestSelfHostReleaseUniqueTail`
pins the release helpers' heads calling the `.u` entry, and the low-guard
suite still finds the floor test on the `.r` entry.

## The machines

The release selector (`ssadecrementtext.select`) takes the `.u` label as a
third entry of each body, held to the rules of `.r`: exact, unique, in the
text section and inside the body. Every machine that walks the body from
the stack entry and from `.r` (the decrement, zero-count, small-array,
large-array, box-string, fused-string and separate-string machines) now
proves `.u` on its `.r` walk: the walk must pass the `.u` label, and there
the state must be the entry state, every register and cell untouched, with
only the flags of the floor test it skipped, which the machine then forgets.
The rest of that walk is the walk from `.u`, so the release contract holds
for the entry the inline guard calls, at no extra walk: the gate's
sanitized build quarantines every freed block, so its peak memory is
everything the driver ever allocates, 13.5 GB on main, and a third walk per
check put it past the box. The guard machine keeps to the two public
entries: a low or tagged pointer never reaches `.u`, which is the point of
it. The small-caller machine reads a `.u` call as the register release it
is. The ISA controls re-pin the kind sequences with the new label, and the
selection controls refuse a `.u` that is duplicated, moved to data, cut off
from its body or missing.
