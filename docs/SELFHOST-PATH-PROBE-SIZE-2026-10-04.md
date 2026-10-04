# Path probe size investigation

PR #11318's driver-size gate measured `asm_pathprobe_run.fern` at
4,172,000 bytes against a 3,965,592-byte baseline. Its Stdio changes do
not alter the compiler, stdlib or bootstrap pin relative to main at
`a4d8eba12`. The regression is accumulated compiler growth since the
baseline measurement, not code linked from Stdio.

## Reproduction

The baseline came from CI run `36888675074`, whose source was
`89c212ac94384876ff23487cd3da53e374dd4e29`. The later commit that edited the
baseline file already contained further source changes, so it is not the
right source for this comparison.

Both pins were downloaded from their recorded releases and their
uncompressed Darwin binaries checked against `bootstrap/stage0.lock`.
Each compiled the driver for `x86-64-linux`, with the corresponding
checkout's stdlib. Both historical and investigated output sizes reproduce
their CI measurements exactly.

| Source | Compiler pin | ELF bytes |
|---|---|---:|
| Historical CI source `89c212ac9` | `7d8ea4e18` | 3,965,592 |
| Same historical source | `c891ebc2e` | 3,901,208 |
| PR source `e8d50404d` before the optimization | `c891ebc2e` | 4,172,000 |
| Optimized source `b3108829e` | `c891ebc2e` | 4,169,992 |

The pin change saves 64,384 bytes. Subsequent source changes add 270,792
bytes under the same pin. These include typed byte-view lifetime checks,
array-combinator fusion, byte-read lowering, map and tuple ownership fixes,
additional builtin operations, and parser and checker corrections.

An independent comparison of the emitted assembly corroborates those
sources of growth. Under the same pin, LLVM-assembled `.text` grows from
3,539,381 to 3,784,046 bytes. The largest module increases are parser
(40,376), ownership lowering in `ssarc` (37,302), array fusion in `semfuse`
(35,872), checker (32,084), typed source construction (21,944), and IR
operations (21,662). The former `irlower` split is counted as a rename,
not new code. This assembly comparison uses LLVM's `movslq` spelling of
`movsxd`; its encodings differ from Fern's native ELF writer, so these are
corroborating code measurements, not an exact partition of the ELF delta.

## Avoiding the unused report

The path probe only needs the whole-program verdict. It previously built
every declaration's name, state and reason as a `VerdictRow` before reading
`Verdict.ok`. `verdict_ok` retains annotation, entry checks, target
normalisation, both lowering passes and ownership inference. It reads the
same refusal conditions directly, avoiding construction of the report.
The detailed query and boolean query share target normalisation.

This removes 2,008 bytes from the measured driver. The remaining growth
supports the compiler functionality above. The baseline records that
4,169,992-byte optimized driver. The size tolerance remains 5%; this
optimization changes no other driver's baseline.

The regression corpus covers produced functions, checker errors, a closure
view-lifetime refusal, a main-less module, array-loop control flow, methods,
generic templates and instances, and bodyless imports. The actual path
probe and detailed report both cover that corpus. A separate compiled
driver compares both queries after x86 Linux, ARM Linux, ARM Darwin and
WASM normalisation.

All four probe test groups passed with the optimization, along with the
full Linux unit suite and lint gates. `TestSelfHostWarmStockDriver`
independently measured 4,169,992 bytes and smoke-ran the driver.

On PR snapshot `01fe6a2529`, which includes main `9c8eb0032`, the same test
measures 4,283,816 bytes and the probe groups pass again. This is within the
original 5% gate.
The baseline stays at 4,169,992, so subsequent growth continues to be
measured against the investigated source rather than a moving ceiling.
Current compiler and Stdio corpus validation are recorded once in the
[Stdio report](STRING-STDIO-BYTES-2026-10-03.md#validation).
