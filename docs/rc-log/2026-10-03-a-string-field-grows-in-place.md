# 2026-10-03 — a string field grows in place

`ssaunits.field_grow_root`, `ssarc.grow_field_or_concat`. The self-host half of
#8785.

## What changed

`a = Acc { ...a, buf: a.buf + piece }` concatenated on every update: the field
read is a borrow, so the append's left operand was lent, and
`__fern_str_grow` was never tried. The array push onto a field already grows
in place through `field_grow_root`. That function now admits a string append
too, when the field belongs to a record the frame owns and reads no further
through that field.

The site tests the record's count. If the record is sole-owned, the runtime
grows the buffer, and declines on its own for a shared buffer, a view or a
literal. A grown field is nulled, so the record's release skips the buffer the
result now owns. A shared record, or a growth that does not fit, concatenates
and leaves the field for the record's release.

A borrowed record is never admitted. No caller brackets a text field it lends
(`grow_rows` walks appends and withs only), so growing one would rewrite the
caller's string.

## Measured

`FERN_LEAKCHECK=1`, 2000 two-byte appends onto `buf` of a two-field record:

| | before | after | native |
|---|--:|--:|--:|
| x86-64 | 2001 | 258 | 132 |
| arm64 | 2001 | 258 | — |
| wasm | — | 260 | — |

With an `xs: [i]` field replaced alongside, which allocates once per update
on every compiler, the figures are 4010 before and 2265 after; native is 2141.

The remaining factor of two against native is the size classes, not this
site. Below 2 KiB the self-host's classes are exact to the 8-byte word, where
native's step by 16 bytes, so a buffer growing a few bytes at a time crosses
twice as many. The local-accumulator and join-chain shapes show the same
ratio: 255 against native's 129, and 256 against 130. All of them already
grow in place.

## Witnessed

`TestSelfHostStringFieldAppendInPlace` runs on x86-64, arm64 and wasm. It
fails when the record-count test is dropped: a record returned through a
counted call reads rc 2 while only it holds the buffer. It also fails when a
borrowed record is admitted. The strings are 19 bytes, so a one-byte growth
stays inside its class on every target. At 20 bytes wasm's block is full,
and the wasm leg would pass with the gate removed.
