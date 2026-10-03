# 2026-10-03 — the runtime's low-address guard covers null

`asm_ir` and `asm_arm64_ir`, the hand-written rc and free helpers. Refs
#8171.

## The shape

Every runtime helper that reads a box's header first refuses a pointer
below 0x10000, which is no heap address. Ten of them on x86-64 and eleven
on arm64 tested for null first, then for an address below 0x10000, and
both tests jumped to the same label. Null is below 0x10000, so the null
test could never decide anything: two dead instructions on x86-64
(`testq; jz`) and one on arm64 (`cbz`) at the head of `__fern_arr_dec`,
`__fern_rc_inc`, `__fern_rc_is_unique`, `__fern_str_free`,
`__fern_str_view_free`, `__fern_arr_inc_elems`, both map frees, the deep
array free and the Option-array element body (plus arm64's
`__fern_rc_dec`).

`__fern_arr_dec` alone runs 27.5 M times on the stage-2 compile of
`checker.fern`, and `__fern_rc_inc` 16.1 M.

## What changed

The null tests are gone, and the two guard-chain comments now say the
low-address test covers null.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 31695b8d and from this change on it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 24.577 G | 24.461 G (−0.47%) |
| `__fern_arr_dec`, self Ir | 585 M | 530 M |
| `__fern_rc_inc`, self Ir | 222 M | 191 M |
| `__fern_str_free`, self Ir | 183 M | 171 M |
| `__fern_arr_inc_elems`, self Ir | 140 M | 135 M |

These figures predate the `__fern_rc_is_unique` removal, which review
found afterwards. It only removes two more instructions from one more
helper.

Emitted bytes change on 979 of the 1,965 `selfhost-emit-hashes` rows (490
x86-64, 489 arm64). Both sides refuse the same 252.

## Witnessed

`TestSelfHostRuntimeLowGuardCoversNull` is new. It emits a program that
releases arrays, strings, a map, an array of arrays and an array of
records for both ISAs. It fails when a null test of any register precedes
a low-address guard of the same register to the same label, and when `__fern_arr_dec`'s register entry opens with anything but the
guard. The program must also exit 42 on every host target. Against main's
listings it flags seven helpers on x86-64 and eight on arm64. It found
arm64's `__fn___fern_str_free`, which the first pass of this change had
missed. `__fern_rc_is_unique` tests `%rcx` / `x2` rather than the first
argument register, so it was only caught once the patterns stopped naming
the register. Also run: `TestSelfHostRcTrace*`, `TestSelfHostArrPush*`,
`TestSelfHostMap*`, `TestSelfHostSemanticSourceRC`,
`TestSelfHostAllocFastPath`, `TestSelfHostOptArr*` and `TestSelfHostStr*`.
