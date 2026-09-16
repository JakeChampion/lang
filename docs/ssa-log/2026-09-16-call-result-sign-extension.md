# Measured 2026-09-16: a 32-bit call result is extended as it is captured

`2026-09-16-call-site-pad.md` counted 51,117 `movsxd` in the x86-64 SSA
build of the self-hosted driver, the sign extension the backend
re-establishes on every 32-bit result. Most follow arithmetic and are
the width invariant itself; 6,925 followed a call's result move, and the
listing showed the shape: `mov r, rax` then `movsxd r, r32`, or through
the staging register when a value lives across the call, `mov s, rax`,
the restores, `mov r, s`, `movsxd r, r32`.

**What changed.** The direct and closure call renderers take a 32-bit
result out of rax with `movsxd dst, eax`, one instruction where the move
was one, and nothing re-extends it afterwards. A 64-bit result is moved
as before. The renderer's two shapes both fuse: the result placed
straight into its home before the restores, and the result staged
through the scratch register, where the capture is the extending
instruction and the placing move carries a value that is already
extended.

| build | binary | text segment | `movsxd` | of them from eax |
| --- | --- | --- | --- | --- |
| main | 9,090,811 B | 8,772,786 B | 51,117 | 0 |
| with the fused capture | 9,049,851 B | 8,729,906 B | 51,117 | 25,168 |

The count of `movsxd` is unchanged by construction, and the 13,700
moves they replaced are the 41 KB. The driver, given `lexer.fern` on
stdin, writes the same bytes as the flat-built driver.

**What is left of the sign extensions.** The remaining 51,117 are one
per 32-bit arithmetic result. Establishing the extension only where a
64-bit reader needs it (a compare, a memory address, a division, a shift
count, a call argument, a store of 8 bytes) is how the flat backend
avoids them, and it is the largest item left in the size gap; it is a
change to the width rules in `internal/ssa`, not to an emitter.
