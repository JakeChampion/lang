# Measured 2026-09-16: arm64 string compares through the NEON kernel

`2026-09-16-full-sweep.md` landed `__ssa_mismatch` behind the x86-64 SSA
backend's `__str_eq` and `__str_ord` (#9443) and left the arm64 side on
its loops: a word loop for equality and a byte loop for ordering, where
the flat arm64 backend and the arm64 SSA backend's own `__fern_mismatch`
(the language-level mismatch builtin, #8791) have a NEON kernel that
compares 16 bytes an iteration and takes a short remainder as two
overlapping windows.

**What changed.** `__str_eq` and `__str_ord` call `__fern_mismatch` with
both offsets zero and the length to scan, when that length is 16 bytes or
more; under 16 they keep their loops. The threshold is measured: the
first cut called the kernel for every length, and `sort_strings`, whose
keys are short, ran 24% slower under qemu, the kernel's call, frame and
vector setup costing more than the bytes it saved. Equality keeps the
link register and the length across the call; ordering keeps the link
register, both pointers, both lengths and n, and reads the differing
bytes at the index the kernel returns.

Best of five under qemu on the container, main against this change:

| bench | main | with the kernel |
| --- | --- | --- |
| `map_string` | 75.7 ms | 78.3 ms |
| `sort_strings` | 177.5 ms | 179.3 ms |
| `tokenize` | 80.9 ms | 80.2 ms |
| `string_slice` | 134.2 ms | 136.0 ms |

None of the suite's string programs compares strings of 16 bytes or more
in its hot loop, so they measure the threshold's cost, which is noise. A
loop of two million equalities and orderings over 68-byte strings, best
of three under qemu, runs in 320 ms against 899 ms on main; qemu charges
each vector instruction a helper call, so the hardware ratio is at least
that.

**Checked.** The new test compares every length from 0 to 40 against the
exact three-way answer with a difference planted at the first, middle and
last byte and a prefix in each direction, in both argument orders; the
existing equality test does the same for `__str_eq`; the helper
discipline tests (callee-saved preservation, entry alignment) cover the
frames the calls now push.
