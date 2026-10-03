# Borrowed byte comparison

`__mismatch_bytes(a, ao, b, bo, n)` compares borrowed byte-array ranges
without allocating or converting them into strings. Each offset clamps to
its array's bounds. The count clamps to zero and the smaller remaining
length. The result is the first differing offset within the ranges, or the
clamped count when every compared byte matches.

Packed native arrays share the existing vector comparison kernels.
Unpacked arrays use bounded element-slot reads. WASM reads packed bytes at
the array payload offset. The typed contract borrows both arrays, and the
interpreter implements the same bounds contract. `install -C` now reads
raw byte blocks and uses this comparison to decide whether a copy is needed.

The shared fixture covers all byte values, every mismatch position around
vector widths, extreme offsets and lengths, empty and unequal-length
arrays, retained aliases and repeated allocation-free calls. Linux Go and
primary x86, ARM and WASM target checks pass, together with typed contracts,
opcode registry checks, GNU install parity, the full unit suite and all
lint gates. The primary WASM pilot takes 23.688 seconds, the Go matrix
1.941 seconds and the primary comparison/registry matrix 24.599 seconds.

Pinned bootstrap reaches a byte-identical stage-2/stage-3 fixed point at
12,513,569 bytes, SHA-256
`4318e0b34b6339c059c29f5dedee3b331305f6263293adeef3612ad6d5966004`.
That compiler passes the range fixture on Darwin, core WASM, Preview 2 and
the primary interpreter. Native and core WASM runs balance ownership; the
compiled scan loop allocates nothing on all three compiled targets. No
whole-component exit census is claimed for Preview 2.

Six final-compiler native install cases cover empty and equal files, first
and last mismatches, and shorter and longer destinations. They match GNU's
output and exact file bytes, balance allocations and retain destination
inode and modification time when no copy is necessary.

The same final compiler builds both the parent and candidate for size
comparison. Compiler code grows 6,312 bytes, unwind data 256 and data 2,816;
the file grows 176 bytes within existing segment allocations. Both install
images remain 282,081 bytes. Install code grows 1,236 bytes and unwind data
128 bytes; static data is unchanged. These additions provide raw reading
and range comparison. No size baseline changes.

Native timing first passes an 8,192-byte pilot, then repeats the same
pipeline at 8,388,608 bytes. Each run checks exact destination bytes, no
copy output, and unchanged inode and modification time. Two warmups precede
seven alternating measured runs. No other compiler or test job from this
workstream runs during measurement.

| Equal 8 MiB files, `install -C` | Median ms | Sample range ms |
| --- | ---: | ---: |
| Previous text input | 3.354333 | 3.143542-4.045708 |
| Raw byte input | 3.319333 | 3.029791-3.751625 |
| GNU coreutils 9.12 | 6.236208 | 6.165416-6.652667 |
| uutils 0.0.29 | 10.660458 | 10.310708-11.366125 |

The before/after ranges overlap. This measurement establishes byte-correct
operation without evidence of a material performance change for this case;
it does not establish a general speedup.

After integrating the string dispatch and view-analysis repairs from
PR #11173, bootstrap again reaches identical stages 2 and 3 at 12,513,569
bytes, SHA-256
`8e8ba99d47e0f9c720ce5fd2a4b9b38353b58f87adea78269530015be51e176e`.
The range fixture passes on Darwin, core WASM, Preview 2 and the interpreter;
all six native install cases pass again with balanced allocations. The
emitted text and raw install binaries are byte-identical to those measured
above, so these timings still describe the current binaries. The repair adds
144 bytes of compiler text and 8 bytes of unwind data within the same file
size. Combined Linux semantic, range, CLI, primary install, full unit and
lint validation passes on the frozen source snapshot.
