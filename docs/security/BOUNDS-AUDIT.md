# Bounds check audit

Status: [reference] probe record for [SEC-005](https://github.com/JakeChampion/lang/issues/13507).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

Each probe is a short program with one suspect index or slice.
Each probe ran natively on x86-64 Linux, on arm64 Linux under QEMU, and on Wasm under wasmtime as a core module.
The rule under test is in [ARRAY-BOUNDS.md](../ARRAY-BOUNDS.md).

## Probes that abort on every target

| Pattern | Cause line | Normative case |
|---|---|---|
| An index that wrapping arithmetic makes negative | `array index out of range` | `oob_index_wrapped` |
| A loop guard one past the length | `array index out of range` | `oob_loop_guard_past_length` |
| A string byte index equal to the length | `string index out of range` | — |
| `slice_unchecked` with the low bound above the high bound | `string index out of range` | — |
| `xs.with(len, v)` | `array index out of range` | `oob_index_write` |
| A byte array slice with the high bound past the length | `slice range out of bounds` | `oob_slice_range` |
| An array slice with a negative low bound | `slice range out of bounds` | `oob_slice_negative_start` |
| An index into an empty array | `array index out of range` | `oob_index_read` |
| An index past a view's length, inside its storage | `array index out of range` | `oob_view_index_past_view` |
| A `u64` index above 2^32 | `array index out of range` | `oob_index_wide_integer` |

Every abort exits 134 on the natives and on the Wasm core module.
A Wasm component reports only success or failure to wasmtime, so its exit code is not evidence.

## Probes that do not abort

| Pattern | Observation |
|---|---|
| A checked string slice `s[1:9]` past the end | Answers `None` on every target (`ST-01`). |
| `__arr_set_len(xs, 1000000)` on a three-element array, then `xs[999999]` | No abort. The read returns whatever the address holds. |

The second row is the raw intrinsic hole that [the inventory](UNSAFE-INVENTORY.md) records.
`__arr_set_len` is reachable from every source today.
Its obligation, a length within the capacity, has no checker and no runtime check.
The coordinated enforcement change must put it behind an `unsafe` block.

## Limits

These probes exercise the checked paths the language exposes and one raw path.
They do not prove that every elided check in `ssabounds.fern` is sound.
A translation-validation or differential sweep of check elimination belongs to SEC-012 and SEC-017.
