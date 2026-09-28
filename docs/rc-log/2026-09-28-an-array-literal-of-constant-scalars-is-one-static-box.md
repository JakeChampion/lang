# An array literal of constant scalars is one static box

Part of #8920.

## What changed

`ssaunits.plan`'s `constants` now covers array literals as well as records
and variants. A literal whose every element is a narrow scalar constant
(`i32`, `u32`, `char`, `boolean`) is one static box. So is the empty
literal, whatever its element type, which replaces `ssarc`'s special case for
`[]`. `ir.op_const_array` generalises `op_const_empty_array`, and
`const_struct_is_array` replaces `const_struct_is_empty_array`.

A `u8[]` literal is never placed, empty or not. `u8[]` is the raw floor's byte
buffer, which `__arr_set_len` and a store through its data pointer
(`b as usize` with `__store_u8`) write in place. Neither path tests a count,
so a static one would carry the write into every later evaluation of the
literal. `TestSelfHostByteLiteralIsFresh` reads the length before and after
such a store in each round. It answers 400 with a fresh box, and 202 when
the literal is placed.

An element that is a copy of a constant also counts. A spliced accessor such
as `function seven(): i32 { return 7; }` arrives as one (#10521), so records
built from such calls are placed too.

A static array's box:

- **Register backends:** the capacity word and word 0 (the length) are both
  the element count, followed by one 8-byte slot per element.
- **Wasm:** the length and capacity words are both the element count,
  followed by 4-byte slots padded to 8 bytes.

The rc is immortal. Every path that writes to an array is already safe on
such a box:

- **Push.** Capacity equals length, so the fast path finds no room. The x86
  slow path treats the immortal rc as writable in place, but only with spare
  capacity. It grows into a fresh box instead and never frees the old one.
- **`with`.** It tests `__fern_rc_is_unique`, which an immortal box fails, so
  it copies.
- **Releases.** They skip a negative rc.

The arm64-android relocation walk leaves arrays alone: an array of scalars
holds no address, which the empty array already relied on.

## Measured

`TestSelfHostStaticBoxes`: 100 rounds per probe, heap allocations. The
results are identical on all four targets, leak-checked, and clean under
`FERN_SANITIZE`:

| probe | before | after |
|---|---|---|
| `[6, 11, 0 - 12]` searched, two calls a round | 200 | 0 |
| `[4, 5, 6]` returned, then pushed onto | 200 | 100 |
| `[4, 5, 6]` returned, then `.with(0, 100)` | 200 | 100 |
| `[true, false, true]` returned | 100 | 0 |
| `[seven(), 8]`, `seven` spliced | 100 | 0 |
| `["a", "b"]` (control) | 100 | 100 |

The push and the `with` each still make their one fresh box. The literal no
longer makes one too. `set_rounds` also reads the literal again after the
`with`, which checks that the write did not go through to the static box.

The self-host compiler compiling `coreutils/tsort.fern` (callgrind, x86-64),
each compiler built by itself from its own source:

| | main | static arrays |
|---|---|---|
| instructions | 4,373,957,946 | 4,357,528,365 (−0.38%) |
| `__fern_arr_box` calls | 4,290,421 | 4,144,130 (−146,291) |
| stage 3 bytes (`-g`) | 13,354,664 | 13,342,832 |

`ssa.inst_has_effect`, which built its two tables of instruction kinds on
every call, made 96,286 of those boxes and now makes none. The built `tsort`
is byte-identical, and stage 3 = stage 4.

`TestSelfHostConstAggregateArm64PIE`'s `every_static_kind` row now carries a
non-empty array in the pool that the Android relocation walk strides over.

## Still allocating

- A literal of `i64`, `u64`, `f32` or `f64` elements. Its wasm slots are 8
  bytes, which the block layout does not write yet.
- A literal of strings or boxes.
