# String byte copy capacity

SEC-005: #13507. This change covers the native packed byte copy from a string.
It does not complete the bounds and allocation audit.

The copy helper accepts a nonnegative string length up to `2147483647`.
The destination stores eight bytes per allocation word.
The helper must round the byte length up before it requests those words.

The previous expression adds seven in `i32` before division.
Lengths above `2147483640` wrap that addition and produce an incorrect capacity.
The helper now extends the length to `i64` before the addition.
The rounded word count fits `i32` at the maximum string length.
Ordinary source arithmetic keeps its current rules.

## Regression

`TestSelfHostStringBytesAllocationCapacity` checks the actual emitted copy helper.
The test supplies a string descriptor at the helper entry.
It stops at the next array allocator entry before allocation or a byte copy.
The other startup allocations retain their normal behavior.

The test checks lengths `0`, `1`, `7`, `8`, `9`, `2147483640`, `2147483641`, and `2147483647`.
The smaller values and the last value before overflow are controls.
The old helper fails both overflow cases on x86-64 Linux and AArch64 Linux.
The test uses native execution or QEMU, as available on the host.
It requires an exact capacity, exit status, and empty output.

This test proves the capacity calculation at the allocation boundary.
It does not allocate a real two-gigabyte string or demonstrate memory corruption.
The common native helper also serves AArch64 Darwin.
Wasm uses a separate runtime implementation.

## Remaining audit

Array copies, slices, concatenation, and OS result buffers require separate checks.
Some OS result lengths have a smaller kernel or buffer limit.
The same source expression alone does not establish their input range.
Unsafe-effect checks and the full safe-code contract remain incomplete.
