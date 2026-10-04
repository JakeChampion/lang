# Compile BRE patterns from raw bytes

`bre_compile_bytes` and `bre_compile_bytes_syntax` share the parser with
text patterns through the existing input abstraction. Bracket classes map
raw spellings to known ASCII names; collating elements and equivalence
classes read bytes directly. No raw pattern fragment becomes text.
The current repetition and backreference implementation is preserved.

The reproduced builder compiler passes the prefix, input and pattern
fixtures on Darwin, core WebAssembly and component WebAssembly. The pattern
fixture also passes through its interpreter. It covers every byte, NUL,
high-byte ranges, collating and equivalence classes, invalid class names,
captures, empty patterns, all twelve named classes and both BRE dialects.
The pattern census balances 67,988 allocations on Darwin and 68,064 on
core WebAssembly. Compiler reproduction is recorded in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

The same compiler builds both parser versions. An unchanged parser probe
adds 440 bytes of code and 104 bytes of unwind data. An unchanged `tac`
consumer adds 856 and 200 bytes respectively. Static data and file sizes
are unchanged: 132,465 bytes for the probe and 199,041 for `tac`. These
costs come from the parser input wrapper and raw class-name handling.
No size baseline changes.

Native measurements use 64 repeats for the pilot and 16,384 for
the full run, with only the repeat count changed. Both runs verify output
before timing. Two warmups precede seven alternating samples; peak RSS
comes from a separate run. No other compiler or container job was running.

The unchanged text-pattern probe verifies every match and the final sum.
Before and after use the same reproduced compiler.

| Workload | Implementation | Median ms | Range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| literal | before | 23.030 | 21.112-42.958 | 1,146,880 |
| literal | after | 23.334 | 21.072-26.849 | 1,146,880 |
| classes | before | 137.260 | 133.655-144.655 | 1,146,880 |
| classes | after | 131.869 | 129.639-138.550 | 1,146,880 |
| captures | before | 33.933 | 32.379-34.933 | 1,146,880 |
| captures | after | 34.521 | 34.294-34.777 | 1,146,880 |

All before/after timing ranges overlap. These measurements establish
the observed cost without claiming a speed improvement.

Linux passes the new byte tests, primary target checks, existing GNU and
primary parity corpora, the full unit suite and all lint checks. The BRE
regression gate includes expr, tac, nl and csplit. The local GNU printf
oracle was rebuilt against glibc 2.39 because its older glibc 2.36 build
rejected binary prefixes already required by the corpus. No test expectation
or production numeric behavior changed to accommodate that environment.
