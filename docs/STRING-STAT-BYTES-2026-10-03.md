# Raw format output for stat

`stat` emits escaped bytes, partial filename bytes and byte diagnostics
directly. Byte precision can split a filename's encoding without producing
an invalid intermediate string. Ordinary metadata values remain text.

Direct checks with the reproduced builder compiler pass on Darwin with
balanced allocation censuses. Cases cover every escaped byte, Unicode
format text, filename precision and padding, unknown conversions and
escape diagnostics. WebAssembly retains explicit capability errors for
`cwd`, `fsmode` and `fsinfo`; it does not silently produce partial metadata.
Compiler reproduction is recorded in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

Built by the same compiler, raw formatting adds 864 bytes of code,
224 bytes of unwind data and 256 bytes of static data. The 248,977-byte
file size is unchanged. The added code and byte data handle raw formatting
and diagnostics. No size baseline changes.

Native measurements use 16 repeats for the pilot and 128 for
the full run, with only the repeat count changed. Both runs verify output
before timing. Two warmups precede seven alternating samples; peak RSS
comes from a separate run. No other compiler or container job was running.

GNU coreutils 9.12 is the output oracle; Rust uutils 0.12.0 is also
checked. Timings include only implementations with identical stdout,
stderr and exit status.

| Workload | Implementation | Median ms | Range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| literal | text | 1.604 | 1.549-1.987 | 1,294,336 |
| literal | bytes | 1.734 | 1.534-2.183 | 1,310,720 |
| literal | gnu | 3.260 | 3.204-3.457 | 1,179,648 |
| literal | uutils | 2.084 | 1.945-2.425 | 1,966,080 |
| escaped | text | 4.893 | 4.792-5.746 | 1,982,464 |
| escaped | bytes | 4.961 | 4.747-5.713 | 1,785,856 |
| escaped | gnu | 4.828 | 4.296-6.484 | 1,359,872 |
| escaped | uutils | 986.265 | 975.772-1038.268 | 4,472,832 |
| precision | text | 2.058 | 1.968-2.195 | 1,589,248 |
| precision | bytes | 2.152 | 1.996-2.225 | 1,572,864 |
| precision | gnu | 3.698 | 3.614-3.755 | 1,212,416 |
| precision | uutils | 3.076 | 2.963-3.420 | 2,080,768 |

All before/after timing ranges overlap. These measurements establish
the observed cost without claiming a speed improvement.

| Workload | Argument bytes | Output bytes |
| --- | ---: | ---: |
| literal | 1,242 | 9,216 |
| escaped | 131,162 | 262,144 |
| precision | 2,906 | 23,552 |

Linux passes the new byte tests, primary target checks, existing GNU and
primary parity corpora, the full unit suite and all lint checks. The BRE
regression gate includes expr, tac, nl and csplit. The local GNU printf
oracle was rebuilt against glibc 2.39 because its older glibc 2.36 build
rejected binary prefixes already required by the corpus. No test expectation
or production numeric behavior changed to accommodate that environment.

The new Darwin byte tests pass. The full existing `stat` corpus retains
the same failing cases as the unchanged parent, already covered by the
repository's Darwin CI exception list. They include device-number layout,
missing-path wording and filesystem metadata. No exception was added or
broadened, and this byte migration does not claim full Darwin stat parity.
