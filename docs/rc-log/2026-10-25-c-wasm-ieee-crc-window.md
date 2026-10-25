# Wasm IEEE CRC-32 and lent byte windows

Issue #13281. Cause fix: #13283.

The IEEE CRC-32 string helper accepts both inline strings and tagged byte windows.
An inline string stores its length before its bytes.
A tagged window stores a data pointer and length in the shadow stack.
The kernel must decode that representation before its block and tail loops.
`view_string_decode` provides the same data and length contract that the cksum kernel uses.
The packed array helper keeps its array header contract.

## Regression evidence

The IEEE Wasm array regression fails with exit 5 before the fix on main `c2acbb04a506`.
A public hash update over the nine-byte `123456789` window also traps in Wasmtime.
Its expected checksum is `0xcbf43926`.

After the fix, eight selected CRC subcases pass without skips.
They cover native x86-64, Wasm, the interpreter, and components for cksum and IEEE CRC-32.
The shared fixture checks independent Go expectations, empty views, offsets, block tails, seeds, chained updates, and ownership.
The complete IEEE fixture also passes on ARM64 under QEMU with zero final live bytes.
Compiler and stdlib complexity gates pass.

All 67 current-source image TAP cases pass in Wasmtime with the fixed compiler and zero final live bytes.
Those cases include the seven PNG cases that exposed this defect.
The allocator and cache smoke modes also pass.
These local results do not replace current-head CI.
