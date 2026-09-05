# 2026-09-04 — the boxed-value map OVERWRITE stranded both halves of the old value

Three of the four map cases the wasm leak census pinned (#7912) are one shape:
`m.insert(k, v)` replacing an existing STRING value under a two-word string ABI.
They were pinned on arm64 too, so this was never a wasm-only gap — the census
header said "clean on both natives", and only x86-64 was.

## What it costs, measured

`FERN_LEAKCHECK=1` (read at COMPILE time by `internal/ast`), 100 rounds of
build-map / insert / overwrite / drop, blocks and bytes per round:

| K | V | overwrite | blocks | bytes |
|---|---|---|--:|--:|
| string | string | no | 0 | 0 |
| string | string | no, two distinct keys | 0 | 0 |
| string | i32 | yes | 0 | 0 |
| i32 | string | yes | 1 | 16 |
| string(65) | string(1) | yes | 1 | 16 |
| string | string | yes | 2 | 48 |

Sweeping the two lengths separates them: the bytes track the VALUE length and
are flat in the key length.

| key len | value len | bytes/round |
|--:|--:|--:|
| 1 | 1 | 32 |
| 40 | 1 | 32 |
| 100 | 1 | 32 |
| 1 | 40 | 80 |
| 1 | 100 | 128 |

A fixed 16-byte cell, plus the superseded value's buffer when one is reachable.
x86-64 is clean throughout: it stores the string data pointer in the value slot
and has its own pre-drop.

## Two gaps, both stated in the source they were in

Both are in the two-word overwrite pre-drop in `internal/ir/ir.go`.

**The old CELL was never freed.** The pre-drop `__fern_str_dec`'d the (data,len)
the cell holds and said so: *"The old cell itself leaks (as on map drop)."* That
parenthesis had gone stale — `__drop_map_str_values` frees each dead cell when
the whole map dies (`TestLowerMapStringColDropFreesCell`), so the overwrite
was the only site left. It now runs the same pair, `__fern_str_dec` then
`__fern_cell_free`.

**A boxed KEY skipped the pre-drop entirely.** All three pre-drop gates carried
`!needBoxK`, because `__map_lookup_val` takes a raw key. On the two-word ABI a
string key IS boxed, so `Map[string, string]` never reached the pre-drop at all
and lost the value's buffer as well — that is the second block, and why an i32
key leaked only the cell. `__map_lookup_val` dispatches on the map's own stored
key kind and the column holds cell pointers, so boxing the key for the lookup is
all it needs. That cell is transient and BORROWS the key: `exprSafeToReevaluate`
has already excluded a concat from this gate, so the key is an alias the caller
still owns and only the cell is ours to free — `freeLookupBoxCell`'s rule, for
the same reason.

## The pre-drop needs a sole-owner gate, and always did

Review caught the half this entry first missed. A pre-drop RELEASES the value
the set is about to replace, and it runs BEFORE the set's own
`__map_cow_inplace`. A second handle over the same buffer still names that
value, and `__map_own_copied_cols` claims neither a string nor a struct value
column on a copy (#8354) — so the release frees storage the other handle reads.
An uncounted-alias free: no rc detector fires, and the fault lands wherever the
freelist next hands the block out.

```fern
var m: Map[string, string] = map_new(8);
m = m.insert(k, v1);
var snap: Map[string, string] = m;   // rc 2, same buffer, same value cell
m = m.insert(k, v2);                 // the pre-drop releases v1 under snap
```

200 rounds of that, values read back through both handles:

| | wasm32 | arm64 | x86-64 |
|---|---|---|---|
| before this entry's change | 0 | 0 | **SIGSEGV** |
| with the widened pre-drop, ungated | **-3200 live (over-release)** | **SIGSEGV** | SIGSEGV |
| with the sole-owner gate | 0 | 0 | 0, 6400 leaked |

The x86-64 crash predates all of this: its own pre-drop never carried the gate
either, and the `!needBoxK` condition is what kept the two-word ABIs out of the
same shape. So the gate is not a concession to the widening — it is the
condition every overwrite pre-drop was missing, and all three string pre-drops
now open with `__fern_rc_is_unique(m)`.

What it costs is a leak in the aliased case, which is where the release belongs
to whoever owns the column: the two-word ABIs reclaim it at map drop and read 0,
the native single-word one strands it. Closing that is #8354 — a string value
needs a kind of its own in `mapValKindTag` before `__map_own_copied_cols` can
recognise and claim it.

`TestX86_64MapStringColumnReclaim` and its arm64 / wasm siblings carry
`mapAliasedOverwriteSrc`, which fails on main today (x86-64, killed by a signal).

**The probe is single-entry, and that is the boundary of what the gate buys.**
The gate stops the PRE-DROP releasing a value another handle names; it says
nothing about DROP time, where a copy's value column is still shared. Add a
second, untouched entry and both copies' drop walks free that entry's cell and
buffer twice — 200 rounds, both handles read back:

| | wasm32 | arm64 | x86-64 |
|---|---|---|---|
| main | 134 | 139 | 139 |
| with the gate | 134 | 139 | 97, 16000 leaked |

Pre-existing on every backend, and the gate moves only the x86-64 leg — from a
crash to a wrong answer, which is the same corruption reported differently. It
is #8354's by construction: `__map_own_copied_cols` cannot claim the column it
would have to claim, so no pre-drop gate can reach it.

The source comments around this all said "#6242", which **closed** in August —
#6827 claimed the key column and the wide-scalar value cells and left the string
and struct value columns, which is the half that still bites. #8354 is the
residual, and the references now point at it.

## Why freeing the cell before the set is sound

`__map_dec_value` is the set's own overwrite-dec and is a no-op for valKind 1
(non-array pointer, which is what a boxed string value is): it reads the kind
off the buffer and returns without touching `v`. Only valKind 0 — a wide scalar
boxed into a cell — routes to `__map_free_val_cell`, and that column is not this
one, so there is no double free. The set does not dereference the old value
either; it probes keys and overwrites the slot, the same basis the kind-4
pre-drop next door has shipped on.

## Banked

`map_string_keys_churn_free`, `map_string_values_churn_free` and
`map_string_value_overwrite_pre_drop_churn` go to **0** on both arm64 and wasm
and leave both baseline tables.

`map_keys_values_header_churn_free` stays, pinned at 16000 on wasm and absent
from the other two tables. It is a different site, and not the one its name
says: `keys()` / `values()` are clean everywhere. Its `Map[i64, i64]` is what
leaks — wasm32 is the only ABI that boxes a WIDE key into a cell, and the key
column's drop does not free those. Measured, one 16-byte cell per ENTRY, with
no keys() / values() call in the probe at all:

| map | inserts | wasm bytes | arm64 | x86-64 |
|---|--:|--:|--:|--:|
| `Map[i64, i64]` | 1 | 1600 | 0 | 0 |
| `Map[i64, i64]` | 2 | 3200 | 0 | 0 |
| `Map[i64, i64]` | 3 | 4800 | 0 | 0 |
| `Map[i64, i32]` | 1 | 1600 | 0 | 0 |
| `Map[i32, i64]` | 1 | 0 | 0 | 0 |

The wide VALUE column is clean because `__map_dec_value` routes valKind 0 to
`__map_free_val_cell`; the key column has no equivalent. `__drop_map_str_keys`
is the string-key column's walk and frees each dead key cell, so what a wide key
needs is its counterpart.

## Not done

- `map_keys_values_header_churn_free` — the wasm32 wide-KEY column cells,
  characterised above. It wants the `__drop_map_str_keys` treatment for
  keyKind 2, and that is two halves, not one: `__map_own_copied_cols` gates
  its key claim on `__load_i32(buf + 8) == 1`, so a CoW copy of a wide-keyed
  map shares its key cells. Adding the drop walk alone would double-free
  them. The value side already shows the shape — its `cellBytes` branch
  allocates a fresh cell per entry and memcpys — so the key side needs the
  same, keyed on the KEY kind.
- The native single-word pre-drop still carries `!needBoxK`, so a `Map[i64,
  string]` overwrite on x86-64 loses its old value. x86-64 has no
  `__fern_cell_free`, so its boxed cells are a separate gap first.
- **#8277** — on x86-64 a Map read with an ALIASED string key strands the map's
  KEY buffer: one block per MAP, sized by the key, flat in the value length and
  in the number of reads. Not `get_or`-specific (`get` does it too) and the
  fallback is irrelevant; a fresh-concat key reclaims.
  `TestX86_64MapStringColumnReclaim`'s own probe misses it because its key is a
  fresh concat. Unchanged by this work.
- **#8276** — binding a callee's returned borrowed `Map` param double-frees its
  string-key column (arm64 SIGSEGV, wasm32 abort). The Map twin of #8240's
  struct convention; unchanged by this work.
