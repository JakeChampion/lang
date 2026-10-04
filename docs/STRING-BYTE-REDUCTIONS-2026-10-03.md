# Borrowed byte checksum reductions

`__sum_bytes_array(bytes)` adds raw bytes with 32-bit wrapping.
`__bsd_sum_bytes(bytes, seed)` continues a 16-bit BSD checksum, masking the
seed before processing. Both borrow their inputs and allocate nothing.
`hash.SysvSum.update_array` and `hash.BsdSum.update_array` expose these
kernels for owned buffers while preserving the existing text and view APIs.

Packed native arrays reuse the existing sum and BSD kernels. Unpacked
native arrays use bounded slot reads; primary WebAssembly reads packed
bytes. The current integration uses operation IDs 368 and 369, preserving
published identities. Typed lowering records the array operands as borrowed.
The original measurements below predate that integration.

The compiler reproduces itself from pinned stage0. Stages 2 and 3 are
identical at 12,530,337 bytes, SHA-256
`be5db27eb64623f695269103b4171621f546f670ba8c01b624dab94e78408946`.
The final compiler passes the shared oracle on Darwin, core WebAssembly,
Preview 2 and the interpreter. Coverage includes every byte value, empty
arrays, vector boundaries, signed seeds, chunk carry, retained aliases,
32-bit overflow and sign extension. Compiled probes call the kernels
1,000 times without allocating. Native ownership balances at 597
allocations and frees; core WASM balances at 1,250, both with zero live
bytes. Preview 2 checks behavior and the in-program allocation count;
it does not provide an exit-time census.

The same final compiler rebuilds its parent byte-for-byte. The additional
registrations, lowering and kernels add 8,544 bytes of compiler code,
352 bytes of unwind data and 2,048 bytes of static data. File size grows
144 bytes after segment alignment. No size baseline changes.

Linux target, registry, ownership, full unit and lint checks pass on the
frozen source snapshot, including the `std/hash` checksum example.
Disassembly confirms the text and raw kernels execute inside the benchmark's
64-iteration loop rather than being hoisted out.

An 8 KiB pilot passes before the same native benchmark at 8 MiB. Complete
UTF-8 encodings keep both text and raw inputs within their contracts. Input
construction is outside the timed region. Each sample covers 64 calls;
two warmups precede seven alternating samples. No compiler or test job from
this workstream runs during measurement. Other macOS services remain active.

| Operation | Input | Median ms | Sample range ms |
| --- | --- | ---: | ---: |
| Wrapping sum | Text | 27.926125 | 27.036208-28.451541 |
| Wrapping sum | Raw bytes | 27.635708 | 27.099208-29.047750 |
| BSD sum | Text | 716.499041 | 715.185375-720.336959 |
| BSD sum | Raw bytes | 716.729167 | 715.341708-718.994583 |

Both before/after ranges overlap. These measurements establish no speedup;
the raw entry points preserve the existing kernel throughput on this input.
