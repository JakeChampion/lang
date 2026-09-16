# Measured 2026-09-16: where the x86-64 SSA driver's extra 6% is

`2026-09-16-x86-strbuf-and-the-self-host-driver.md` left the self-hosted
driver 6% larger built through x86-64 SSA than through the flat backend
(9,365,243 B against 8,795,528 B). The assembly listings of the two
builds, compared function by function with the name mangling normalised,
put the whole difference in the text segment: 2,487,458 instructions
against 2,260,623 over the 4,992 functions both builds have, 10% more.

**What the extra instructions are.** By mnemonic, SSA over flat:

| mnemonic | flat | SSA | difference |
| --- | --- | --- | --- |
| `mov` | 900,181 | 1,103,455 | +203,274 |
| `pop` | 120,039 | 207,625 | +87,586 |
| `cmp` | 90,762 | 144,295 | +53,533 |
| `movsxd` | 4 | 51,117 | +51,113 |
| `add` | 148,815 | 199,615 | +50,800 |
| `jl` | 2,228 | 52,672 | +50,444 |
| `test` | 132,856 | 35,298 | -97,558 |
| `xor` | 65,607 | 5,135 | -60,472 |
| `js` | 50,954 | 10 | -50,944 |

Read as patterns rather than mnemonics, the listing has:

- **46,320 call sites padded with `sub rsp, 8` before the saves and
  `add rsp, 8` after the restores**, eight bytes a site, 370 KB in all.
  The pad keeps the call 16-aligned when an odd number of saved registers
  and stack arguments sit below it.
- **63,974 push and pop pairs around calls**, caller-saved registers
  holding values live across the call. The allocator steers a
  call-crossing value to a callee-saved register, but a function like
  `parse_stmt_at` (135,036 instructions under SSA, 81,388 flat) has far
  more such values than the five callee-saved registers hold, and each
  overflow pays a push and a pop at every call in its range.
- **51,117 `movsxd`**, the sign extension the backend re-establishes after
  each 32-bit arithmetic result; 6,925 of them follow a call's result
  move, and 1,411 of those could fold into the move itself.
- **12,000 constants spilled to the frame**: a `lea` of a string address
  or a `mov` of an immediate into r10, stored to a slot and reloaded at
  each use, where the flat backend materialises the constant at the use.
- The inline reference-count sequence is shorter under SSA (`cmp` and
  `jl` on the header against flat's load, `test` and `js`), so the `cmp`,
  `jl`, `test` and `js` rows are a wash.

**What this slice does.** The pad is now one more push and one more pop:
the first saved register pushed twice and popped twice, so the second
pop restores a value that is being restored anyway; with nothing saved
the pad is a push of rax, which the call overwrites, and it leaves with
the stack arguments in the one adjust already there. A push and a pop
are one or two bytes each where the two adjusts are four each.

| build | binary | text segment |
| --- | --- | --- |
| flat | 8,795,528 B | 8,484,132 B |
| x86-64 SSA, before | 9,365,243 B | 9,044,562 B |
| x86-64 SSA, after | 9,090,811 B | 8,772,786 B |

The driver built with the change compiles `lexer.fern` to byte-identical
output. Of the 6.5% gap in the text segment, 3.1% is gone; the push and
pop pairs themselves, the constant spills and the sign extensions are the
rest, and each is an allocator or emitter change of its own.
