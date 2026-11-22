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
| Index zero in an empty array | `array index out of range` | `oob_empty_array_read` |
| Index zero in an empty view of nonempty storage | `array index out of range` | `oob_empty_view_read` |
| An index past a view's length, inside its storage | `array index out of range` | `oob_view_index_past_view` |
| A `u64` index above 2^32 | `array index out of range` | `oob_index_wide_integer` |

Every abort exits 134 on the natives and on the Wasm core module.
A Wasm component reports only success or failure to wasmtime, so its exit code is not evidence.

## Probes that do not abort

| Pattern | Observation |
|---|---|
| A checked string slice `s[1:9]` past the end | Answers `None` on every target (`ST-01`). |
| `__arr_set_len(xs, 1000000)` on a three-element array inside an `unsafe` block, then `xs[999999]` | No abort. The read returns whatever the address holds. |

The second row is a raw intrinsic that [the inventory](UNSAFE-INVENTORY.md) records.
Both checkers refuse `__arr_set_len` outside an `unsafe` block as `E095`, and the `arr-set-len-outside-block` row of `TestSelfHostCheckerCodesX86_64` pins the refusal.
Its obligation, a length within the capacity, has no runtime check, so the `unsafe` block carries it.

## Differential sweep of check elimination

`TestSelfHostBoundsGuardSweep` (`internal/testing/e2ecompiler`) makes programs from a seed.
Each program has one function of guards, loops and index arithmetic over two arrays, one byte array and two views.
The accesses are index reads, `with` writes, slices, runs of byte reads that the compiler groups, and lane loads and stores.
About half of the guards are sufficient for their access.
The others bound a different expression, a different array, or a sum that wraps.
The main function calls the function with indexes at the ends of the arrays and at the ends of `i32`.

The interpreter of the compiler checks each access, so it is the reference.
x86-64, arm64 under QEMU and wasm under wasmtime must print the same lines and abort with the same cause line, or not abort.
Each pull request runs 32 programs from fixed seeds. A seed that exceeds the interpreter time limit is reported as skipped. At least three quarters of the seeds must yield a reference result. Each compiled target must produce a result for every seed that reaches comparison.
`FERN_BOUNDS_SWEEP` sets a different number, and `FERN_BOUNDS_SWEEP_BASE` sets the seed before the first.
The `bounds-guard-sweep` job of `nightly-fuzz.yml` runs 400 new seeds each night.

Sweeps of more than 2500 programs on 2026-11-15 found no access that an optimized target let through.
It found three defects of other types, and each has a repair and a test:

| Defect | Test |
|---|---|
| The allocator gave one register to two parameters, and the compiler did not end. | `TestSelfHostParametersDoNotShareARegister` |
| The two interpreters stopped with an internal error for a lane load or store that is not in its array. | `oob_lane_load_past_length`, `oob_lane_store_past_length` |
| The certifier of reference counts refused a function whose release is behind a comparison of a value with itself. | `TestSelfHostSelfComparedReleaseCertifies` |

## Limits

These probes exercise the checked paths the language exposes and one raw path.
The sweep is a sample. It does not prove that every elided check in `ssabounds.fern` is sound.
A translation validation of check elimination belongs to SEC-012 and SEC-017.
The arm64 results are QEMU results.
