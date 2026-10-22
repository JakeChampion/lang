# Raw byte builder transforms

`buf_push_bytes_mapped`, `buf_push_bytes_filtered` and
`buf_push_bytes_expanded` borrow their input array and lookup table, then
append directly to an existing builder. They supply the operations needed
by binary translation and display loops without constructing text.

Mapping preserves bytes beyond the table. Filtering drops bytes whose
entry is nonzero and preserves missing entries. Expansion reads a complete
eight-entry record at `c * 8`: a length clamped to seven, followed by output
bytes. An incomplete record preserves the input byte; a zero-length record
deletes it. Empty input changes nothing.

The primary native emitters share the existing kernel generator, varying
source layout and stride for text, packed arrays and unpacked arrays. Full
table loops and expansion record packing remain. Primary WASM shares the
packed-table generator and emits only the operations used by the program.
Both interpreters read arrays directly. The primary interpreter also avoids
copying the lookup table to a temporary integer array. The Go bootstrap
adapters reuse existing kernels.

`BufWriter.write_bytes_mapped`, `.write_bytes_filtered` and
`.write_bytes_expanded` expose the same operations through buffered output.
They preserve the first write error and discard pending bytes after a
failure. Tests cover borrowed aliases and temporary arrays, including
empty arrays and failed writers.

The oracle checks every byte value, empty and short tables, incomplete
expansion records, lengths above seven, growth from a one-byte reserve,
extraction and reuse, and aliases followed by mutation. A separate probe
performs 3,000 pushes into reserved capacity without allocating temporary
arrays or copies of inputs. The final compiler passes both the oracle and
writer corpus on Darwin, core WASM, components and the interpreter.
Native and core-WASM allocation counts balance with zero live bytes.
Linux target, per-module linking and buffered-writer checks also pass.

The pinned bootstrap reaches identical stages 2 and 3: 12,546,945 bytes,
SHA-256 `f968df08ee228310b80d6fb1873b9b0de4c91ba608859fb3312292dc3ce3e0bc`.
The final generator reproduces the parent compiler byte-for-byte.
Relative to that parent, the compiler adds 14,656 bytes of code, 408 bytes
of unwind data and 768 bytes of static data; file size grows by 16,608
bytes. The additions implement the raw operations, their contracts and
runtime generation. No size baseline changes.

Full unit and lint checks pass on the frozen source snapshot.

An 8 KiB pilot precedes the same native benchmark at 8 MiB. Input cycles
through ASCII bytes, valid in both representations; output tables contain
arbitrary byte values. Each sample totals eight pushes into separately
reserved builders. Input/table creation, reserve and free are outside the
timer; each result length is checked. The generated code retains all eight
calls inside the timed loop. Two warmups precede seven alternating samples,
with no compiler or test job from this workstream running during timing.
Other macOS services remain active.

| Transform | Implementation | Median ms | Sample range ms |
| --- | --- | ---: | ---: |
| map full | Text | 19.234291 | 19.076168-20.575001 |
| map full | Raw bytes | 19.206836 | 19.044958-20.951460 |
| map short | Text | 32.248208 | 32.179667-33.312874 |
| map short | Raw bytes | 32.316749 | 32.233583-32.640792 |
| filter full | Text | 44.961584 | 44.879958-48.619959 |
| filter full | Raw bytes | 44.982917 | 44.875416-45.626375 |
| filter short | Text | 44.266751 | 44.165541-44.514583 |
| filter short | Raw bytes | 44.882835 | 44.321291-46.733083 |
| expand full | Text | 33.056209 | 32.905666-33.233792 |
| expand full | Raw bytes | 33.532417 | 32.893666-34.136042 |
| expand short | Text | 109.954417 | 103.793293-111.060709 |
| expand short | Raw bytes | 108.254501 | 105.688666-112.747333 |

The before/after ranges overlap for all six cases. These measurements
establish comparable throughput on this corpus, without a general speedup
claim. The zero-allocation push probe is separate from these timings.

The 2026-10-04 integration preserves all 343 published operation identities
and has 351 registry entries. The new builder operations use IDs 370-372;
the other pending operations use 366-369 and 373. Registry, constructor,
SSA admission and ARM64 instruction-word equivalence checks pass.

On this integrated source, the full primary byte suite, supplemental
ownership checks, complete unit suite and all lint gates pass. A fresh
bootstrap reaches identical stages two and three at 12,944,801 bytes,
SHA-256 `cfefdb7e37f0b59cfeea96cf914779d92b61466455c1d1bfd748bd35eed478b9`.
That compiler passes the raw-operation corpus on native Darwin, core WASM
and the interpreter, plus the selected consumer artifact corpus. The native
Darwin regression suite also passes. The WASM numfmt malformed-argument case
is skipped because Wasmtime rejects the argument before starting the guest;
the native case and malformed-input byte cases pass.

Compared with the reproduced Stdio compiler on the same upstream compiler
source, the consumer compiler adds 40,664 bytes of native code, 1,416 bytes
of unwind data and 7,936 bytes of static data. Its file grows by 33,632
bytes. This integration adds the eight raw operations and their runtime and
dispatch support. No size baseline was changed. The transform timings above
remain measurements of their originally recorded compiler and source.
