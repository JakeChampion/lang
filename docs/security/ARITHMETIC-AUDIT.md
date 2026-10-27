# Checked arithmetic and pointer lowering audit

Status: partial implementation for [SEC-017](https://github.com/JakeChampion/lang/issues/13519).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).
This record covers three things: how an index becomes an address on the native targets, which proofs remove a check, and where a length meets its limit.

## How an index is checked

An `i32` value lives in a 64-bit register on both native targets.
The index check compares the index against the length that the array or view header records.
The compare is unsigned and 64 bits wide.
A negative index therefore reads as a value above 2^63, and it fails the same compare as an index at or past the length.
The address computation runs after the check and scales the index by the element width in 64 bits.
A payload is below 2^31 bytes and an element is at most 32 bytes, so the scaled offset cannot wrap.

A slice `xs[lo:hi]` passes `lo`, `hi` and the length to a helper that requires `0 <= lo <= hi <= len`.
The helper reads the values, not the expressions that produced them, so a wrapped sum fails the compare.
[ARRAY-BOUNDS.md](../ARRAY-BOUNDS.md) describes the abort on each backend.

## Which proofs remove a check

`compiler/ssabounds.fern` removes an index check only when two facts hold at the read.
The index must be non-negative, and the index must be below the length of the same array.
Both facts come from the dominating branches, from a length, from a constant, or from a value the compiler widened from an unsigned source.
The proof of `x + 1` requires `x` to be non-negative and below a length, so the sum cannot wrap.
The proof of `x - c` requires `x` to be below the length and non-negative, and `c` to be non-negative.

A fact about `i + 1` is a fact about that sum only.
The proof never rewrites `i + 1 < len` into `i < len`, and never rewrites `i < len` into `i - 1 < len`.

A group of reads at `lo`, `lo + 1`, up to `lo + span` keeps one runtime check on `(lo + span) | (lo >> 31)`.
That value is negative when `lo` is negative, and it is above the length when the sum wraps.
One failed compare therefore covers every read of the group.

The five probes below ran on four engines: x86-64, arm64 under QEMU, the self-host interpreter and the Go oracle.
Each engine gave the same output:

| Probe | Case | Result |
|---|---|---|
| Guard `i + 1 < len` with `i` at the largest `i32` | `oob_index_wrapped_guard` | the read at `i` aborts |
| Guard `i < len` with `i` at zero, read at `i - 1` | `oob_index_below_guard` | the read aborts |
| Slice `xs[lo:lo + n]` with `lo` at the largest `i32` | `oob_slice_wrapped_end` | the slice aborts |
| `xs.with(i, v)` with `i` at the largest `i32` | `oob_write_wrapped_index` | the write aborts |
| Index `i * 2` that wraps negative under a `>= 0` guard | `index_wrapped_product_rejected` | the guard fails and the read is skipped |

## Where a length meets its limit

A box holds its capacity in words.
Before this change no native path refused a capacity above 2^28 words.
A byte array built at 2147483647 elements accepted one more element, and its length became 2^31.
The program then held an `i32` outside the `i32` range: `len() < 0` was false and `len()` printed 2147483648.
An `i64` array built at 268435456 elements and an `i32` array built at 536870911 elements also grew past 2^31 bytes.

The box allocator now refuses a request above 2^28 - 1 words with `fern: allocation size out of range`, before it touches memory.
Every typed allocation and every grow passes through it on both native targets.
A byte array therefore stops at 2147483640 elements, which is also the string ceiling, and an `i64` array at 268435455.
`TestSelfHostArrayCeiling` checks a request one word above the ceiling and a grow from a full box, for bytes and for words, on both targets.

The Go oracle ran out of memory on the first probe, and the self-host interpreter exhausted its arena with exit 125.
Neither engine states a length ceiling.
This record does not change them. The native ceiling is the defined limit.

## Shifts, division and conversions

Shift counts are masked to the operand width.
Division by zero, and the smallest value divided by minus one, return defined results.
Conversions extend by the source signedness.
The claims `IS-01` to `IS-19` in `spec/semantics.md` pin those rules on every engine, and [INTEGER-SEMANTICS.md](../INTEGER-SEMANTICS.md) states them.
This record adds no arithmetic rule.

## Remaining limits

The raw floor, `__store_*`, `__load_*`, `__memcpy` and `__arr_set_len`, computes addresses without a check and can move a length past the ceiling.
The unsafe boundary for those builtins is the subject of SEC-003.
Wasm keeps its own grow check and is outside this Linux program.
The bounds proofs are a search bounded by six steps, so a check the proof cannot remove stays in the code.
The audit found no check that the proof removes wrongly.
