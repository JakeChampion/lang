# 2026-09-22 — a key column snapshot is a copy, not an alias

`m.keys()` on a **struct-keyed** map was a use-after-free on the AST lowering —
the default path. Fifteen lines, and the interpreter answers 24:

```fern
import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Coord { x: i32, y: i32 }

function main(): i32 {
    var m: Map[Coord, i32] = map_new(2);
    var i: i32 = 0;
    while (i < 12) {
        m = m.insert(Coord { x: i, y: i * 2 }, i * 10);
        i = i + 1;
    }
    var ks: Coord[] = m.keys();
    return m.len() + ks.len();
}
```

```
fern-sanitizer: use-after-free (touched a quarantined block)
exit 124
```

## One flag, three values, and the wrong one

`irlower.map_kv_elem_flag` answers the runtime's column-snapshot flag:

- **0** — a scalar column, copied; it holds no references, so there is nothing
  to own past the buffer.
- **2** — a counted column, copied with a per-element retain, so the array owns
  its elements.
- **1** — a RAW ALIAS of the map's own buffer.

It returned 2 for a string key and **1 for everything else**, its own comment
saying "flag 1 (raw alias) remains only for i64/f64/struct/generic columns".
For an i64 or f64 column that is fine — nothing is counted. For a column of
STRUCT keys it is not: the frame is handed the map's own buffer as though it
were a fresh array, and releasing it decrements keys the map still holds.

Flag 2's helper, `__fern_map_snapshot_col_str`, is generic despite its name —
it copies the buffer and calls `__fern_rc_inc` per element, with the guards
that skip literals, SSO and null. So a keyed column takes it unchanged, and a
keyed column is exactly the one the name's `_str` does not cover.

## What made it survive

Three things, each worth knowing.

The abort is **invisible without the sanitizer**. The freed block keeps its
bytes at this size, so the program answers 24 either way; only the quarantine
notices the touch.

No conformance lane runs sanitized, and the three struct-key conformance cases
pin an exit code alone.

The differential production rows compare the two lowerings **against each
other**, and they agree here — the typed path refuses a struct-keyed map, so it
falls to the same AST lowering that has the bug. Two identical wrong answers
compare equal. That is the same self-referential blindness `docs/TEST-GATES.md`
records for the fixpoint, in a second place.

So the pin is an absolute one: the interpreter's answer, under
`FERN_SANITIZE=1`.

## Found from the other side

This came out of teaching the TYPED path struct-keyed maps (#9962). Choosing
the snapshot flag there meant reading what each value means, which is what
showed the AST leg had picked the aliasing one. The typed path's own
`map_column` guard — "every other element type is handed back as a raw alias …
so it is refused here rather than described as owned" — was right, and
refusing is why the typed path never had this bug.

The typed-path work itself is not in this change: it admits the shape
correctly but leaks the key boxes, because the runtime has no free member that
walks a key column through a supplied release. #9962 has the patch and the
three pieces it still needs.

## Measured

x86-64, `FERN_SANITIZE=1`.

| | before | after |
|---|---|---|
| exit | **124**, `use-after-free` | **24**, matching the interpreter |
| held at exit | — | 576 bytes |

The leak is not new and not this change's: the same program without `keys()`
strands 912 bytes on the unchanged compiler. A struct-keyed map leaks on the
AST lowering either way; what this change removes is the memory-safety bug on
top of it. Trading a use-after-free for a pre-existing leak is the whole of the
claim.

**The corpus census does not move**: 845 programs whole and 78,043 declarations
of 78,504 before and after. The typed path is untouched — it still refuses
every struct-keyed map — so nothing it produces changes.

## Tests

`TestSelfHostMapStructKeyColumnX86_64` — compiles the program above with the
stdlib root under `FERN_SANITIZE=1` and requires the interpreter's exit, 24,
with no `use-after-free` on stderr.

That it can FAIL was checked rather than assumed: with `irlower.fern` reverted
it reports `exit = 124, want 24` and the sanitizer line.
