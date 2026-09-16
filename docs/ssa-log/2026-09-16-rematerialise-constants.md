# Measured 2026-09-16: a spilled constant is written at its reader

`2026-09-16-call-site-pad.md` counted 12,000 constants in the x86-64 SSA
build of the self-hosted driver that the allocator had spilled: 2,375
`lea` of a string address and 9,585 integer immediates, each written to
r10, stored to a slot, and reloaded at every use, where the flat backend
materialises the constant at the use.

**What changed.** The emitter now finds the spilled constants whose
every reader takes its operand through `materialize`, emits nothing at
their definition, and has each reader write the constant into its
scratch register where the reload would have gone. A phi, a call
argument, a closure or env capture and a boxing read their operand's
home as a location rather than through `materialize`, so a constant with
one such reader keeps its slot; that is where most of the string
addresses stay, as arguments to `__str_eq` and its kin.

**It is gated on bytes.** The first cut rematerialised every eligible
constant and made the driver 4 KB larger: the constant is seven bytes
(`mov r64, imm32`, `lea r64, [rip + sym]`) where a reload from one of the
first sixteen slots is four, so a near-slot constant read four or more
times is smaller stored and reloaded. With the gate:

| build | binary | text segment | spilled immediates | spilled `lea` |
| --- | --- | --- | --- | --- |
| main (#9458 not yet in) | 9,090,811 B | 8,772,786 B | 9,585 | 2,375 |
| with rematerialisation | 9,037,563 B | 8,716,626 B | 4,825 | 2,346 |

The driver, given `lexer.fern` on stdin, writes the same bytes as the
flat-built driver.

**What is left of this item.** The 2,346 string addresses and the
immediates that reach a call are the location-reading readers. Carrying
an immediate through the argument renderers (`argMoveLines`,
`pushStackArgs`, the closure and box captures, and their arm64
counterparts, which read the same `Loc`) would let `mov rdi, imm` and
`lea rsi, [rip + sym]` stand where the reload does, for about 35 KB more.

Best of five on the container, main against this change, all within
noise: `call_overhead` 4.52 ms to 4.21 ms, `enum_match` 11.5 ms to
11.2 ms, `tokenize` 13.1 ms to 13.2 ms, `map_string` 14.5 ms to 14.5 ms,
`struct_drop` 65.2 ms to 66.1 ms, `int_loop` 4.57 ms to 4.61 ms.
