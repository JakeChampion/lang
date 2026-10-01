# String byte transformations

`reverse_bytes`, `shift_byte`, `replace_byte` and `without_byte` return
`u8[]`. Their output can be malformed UTF-8 even when the source is valid:
reversing `é` produces `[169, 195]`. Call `utf8.from_bytes` when a result
should become text; it returns `None` for malformed bytes.

Two-Way search reads the original strings in either direction. Reverse
search maps each logical index to the corresponding byte from the end;
it creates neither invalid reversed strings nor temporary byte views.
The comparison budget, single-byte fast paths and linear worst-case
bound are preserved. Forward searches for two to four bytes use direct
comparisons, bounded to four per candidate, before the Two-Way tier.

## Validation

`StringByteTransformsProgram` checks exact Unicode byte results, all 256
replacement/shift outputs, reversal and identity properties, and Unicode
search inputs that exhaust the naive backward comparison budget. It runs
through the bootstrap interpreter, Linux x86/ARM, wasm, Darwin and the
primary Fern compiler on Linux x86/ARM and wasm. The existing exhaustive
forward/backward search tests and migrated stdlib example tests also pass.

`StringConcatCopySearchProgram` builds runtime concatenations across the
inline capacity boundaries, checks owned copies and searches, and exercises
Unicode inputs. Bootstrap native x86/ARM and primary semantic lowering on
x86/ARM/WASI have balanced allocation censuses. The same program also passes
native and primary macOS ARM, bootstrap wasm and both interpreters. Legacy
AST lowering checks behavior and sanitization, with the ownership limitation
recorded below.

## Initial native measurements

The initial byte-view implementation was measured on ARM64 macOS 15.8,
with `-O -target arm64-darwin`. Baseline is
`26cfbaf2116b2d3de4960fc92fb149188979102b`; candidate is this change.
Each sample launches a process. After a warm-up, five samples alternate
baseline/candidate order. Other local validation ran during measurement.

| Workload | Rounds | Baseline median | Candidate median |
|---|---:|---:|---:|
| ASCII, adversarial forward and backward no-match | 10,000 | 1.094646 s | 1.071010 s |
| Unicode, adversarial forward and backward no-match | 10,000 | 1.101335 s | 1.064087 s |
| Inline string, forward and backward match | 10,000,000 | 0.875464 s | 0.859992 s |

Inline samples overlap: baseline 0.866135-0.896238 s, candidate
0.853050-0.904314 s. These measurements show no regression in the tested
workloads. The final pipeline was checked with one round in each mode
before scaling. Only the round-count argument changed.

The probe below takes an integer round count as its second argument.
Compile the identical source in both trees. Run each binary with `ascii`,
`unicode` or `inline` followed by the desired round count, measuring
wall time with `time.perf_counter`. Every execution must exit zero.

```fern
import "std/string";
function main(): i32 {
    var av: string[] = args();
    var rounds: i32 = av[2].parse_int_or(0);
    var hay: string = "a".repeat(8192);
    var needle: string = "a".repeat(127) + "b";
    var expected: i32 = 0 - 1;
    if (av[1] == "unicode") {
        hay = "é".repeat(4096);
        needle = "é".repeat(63) + "ê";
    }
    if (av[1] == "inline") {
        hay = "abcédef";
        needle = "éd";
        expected = 3;
    }
    var i: i32 = 0;
    while (i < rounds) {
        if (hay.index_of(needle) != expected) { return 1; }
        if (hay.last_index_of(needle) != expected) { return 2; }
        i = i + 1;
    }
    return 0;
}
```

Long-search and inline workloads ran in separate sequential batches.

## CI follow-up: ownership and driver size

The conformance leak census found 42 unpaired allocations in
`prop_string_involution`. They came from `bytes()` on inline strings of
lengths 1 through 7: `as_bytes()` promoted their contents into storage with
no owner. Review then identified ARM64 concatenations with inline storage
through 15 bytes and the same promotion in forward search. The portable body
uses the existing `__str_bytes(s, 0)` primitive: addressable strings use a
bulk copy, while inline strings copy directly into the owned array. It
does not assume an inline capacity. Search reads strings directly.

The driver-size check also exposed unnecessary linkage. `to_array()` called
`split("")`, which retained the general separator search and runtime split
helpers. A shared scalar loop now serves `to_array()` and the empty-separator
branch of `split()`, preserving scalar boundaries and owned results.

These are actual linked sizes for `asm_pathprobe_run.fern`, measured with
`TestSelfHostWarmStockDriver`, `FERN_WARM_DRIVER=asm_pathprobe_run.fern` and
`FERN_DRIVER_SIZE_REPORT`, using the same pinned stage0-20261001-7d8ea4e:

| Sources | Linked bytes |
|---|---:|
| Main base `3dd6a4909` | 5,725,656 |
| PR head `722625fac` | 5,729,648 |
| Short-copy fix and shared scalar loop | 5,727,016 |
| Representation-aware copy and direct string search | 5,727,176 |

The scalar loop removes 2,632 bytes from the PR image. Debug-symbol builds
attribute the code growth over main to 931 bytes, down from 3,396 bytes;
the removed functions include the general string-split wrappers and runtime
split/UTF-8-step helpers. The remaining code provides scalar stepping and
the initial byte-view search path. At that checkpoint the baseline was
5,454,560 bytes, with a 5% ceiling of 5,727,288 bytes. No baseline was raised
for that correction.
That search/copy correction added 160 linked bytes to the previous PR
head and remained within that ceiling before the next main merge. Direction
coordinates are supplied once by the caller and shared by factorization and
periodicity checks.

The subsequent merge of main `bf70edeb8` changed that comparison. With the
same pinned compiler, main alone produces a 5,753,648-byte pathprobe driver;
the candidate produces 5,755,440 bytes. Debug symbols account for every
changed code byte:

| Change from previously measured main `3dd6a4909` | Code bytes |
|---|---:|
| Checker, principally generic-struct literal inference | +24,339 |
| Parser recovery and module/receiver parsing | +2,150 |
| Generated ownership helpers | +653 |
| IR lowering | -256 |
| Total inherited code growth | +26,886 |

Main's linked growth is 27,992 bytes, including 1,106 other linked bytes.
The new inference code preserves integer widths through generic struct
locals, copies and captures; parser recovery handles malformed imports
without discarding later declarations. These are merged correctness fixes.

The candidate adds 1,358 code bytes to current main: scalar splitting and
UTF-8 stepping account for 749, directional Two-Way search for 383, and the
bounded short-needle path for 226. Another 434 bytes are outside `.text`.
The direct short-search path adds no allocation and bounds each candidate
to four comparisons. The earlier unnecessary general-split linkage remains
removed.

At that revision, the three exhausted driver rows were refreshed to measured main
values, before this PR's changes: `asm_modload_run` 9,919,592 bytes,
`asm_pathprobe_run` 5,753,648 bytes, and `irlower_run` 5,579,056 bytes.
The modload main measurement already equals the failing CI measurement;
this PR contributed none of that breach. IR lowering had only 845 bytes
left under its old ceiling. The PR's costs remain visible against the new
main-only baselines. Other rows and the 5% tolerance were unchanged.

The final CI-equivalent `TestSelfHostWarmStockDriver` run smoke-executed all
three drivers and passed the strict size check:

| Driver | Main-only baseline | Final candidate |
|---|---:|---:|
| `asm_modload_run.fern` | 9,919,592 | 9,919,592 |
| `asm_pathprobe_run.fern` | 5,753,648 | 5,755,440 |
| `irlower_run.fern` | 5,579,056 | 5,580,840 |

## Direct-search measurement

The final search uses an origin and a step of 1 or -1 to index the source.
A first version called a helper per byte and regressed forward search;
direct index arithmetic removed that overhead.

Native ARM64 primary-compiler measurements compare PR head `3b39cc77e`
with this implementation, using the same compiler for both binaries.
Each process searches 30 times through a 2 MiB repetitive haystack for a
128-byte needle, checking the result. Two warm-ups precede seven samples
in alternating order. The pipeline first passed with a 16 KiB haystack;
only its size argument changed for the measured run.

| Search | Before median | Direct-index median |
|---|---:|---:|
| Forward | 92.871 ms | 90.295 ms |
| Backward | 124.167 ms | 124.826 ms |

Forward ranges are 91.727-99.309 ms and 89.669-90.761 ms; backward ranges
overlap at 123.443-127.026 ms and 123.333-130.451 ms. The forward median
decreases in this run. The measurements rule out the per-byte helper
version's large regression in this workload, while the new search removes
view promotions and reversed-array allocations.

## Short-search and owned-copy follow-up

The bootstrap compiler now lowers the canonical `std/string.bytes()`
declaration to a borrowed-string, fresh-array operation, matching the
primary compiler's existing `str_bytes` operation. Each backend copies
directly from inline or addressable storage into the owned byte array.
User-defined methods retain their source implementation. Raw pointers from
`__str_bytes` still receive conservative escape handling.

This fixes the one-allocation leak in `alloc_flat_bytes_roundtrip`: the
portable body's local raw pointer had obscured the borrowing contract from
the bootstrap's parameter analysis. Regression tests cover that census,
source/signature checks, raw pointer escape, and fresh SSA results at empty,
inline-boundary and larger lengths.

The direct-index search was remeasured with the inline probe above, against
`3b39cc77e`, after adding the bounded short-needle path. Each comparison uses
one compiler and changes only the stdlib. On native ARM64 macOS, two warm-ups
precede seven alternating samples, with no concurrent validation workloads.
Each pipeline first passed with one round; only the round count then changed.

| Compiler and workload | Rounds | Before median | Candidate median |
|---|---:|---:|---:|
| Go bootstrap, literal inline probe | 10,000,000 | 877.608 ms | 402.408 ms |
| Primary Fern compiler, literal inline probe | 10,000,000 | 528.614 ms | 215.402 ms |
| Go bootstrap, runtime concatenations | 1,000,000 | 100.943 ms | 52.104 ms |

For the runtime case, the same loop searches `"abc" + args()[1]` for
`args()[3] + "d"`, with arguments `édef`, the round count, and `é`.
Both search directions must return byte offset 3. The literal case alone
would not establish the behavior of strings constructed at runtime.

## Integration with October 1 main

The merge of `dc1c405fe` takes the driver-size baseline file exactly as
published on main, superseding the earlier three-row refresh above. This
merge adds no PR-specific size adjustment. The earlier measurements remain
the record for their source revisions; the merged drivers are checked
against main's newer baselines.

The wasm module-loader test keeps the linked dyn-view execution and
bare-view closure refusal cases. It also retains main's recursive-enum
refusal, which diagnoses a type with no finite ownership copy.

The merged driver smoke tests and strict size comparison pass for the
three previously exhausted rows:

| Driver | Main baseline | Merged candidate |
|---|---:|---:|
| `asm_modload_run.fern` | 9,922,264 | 9,925,360 |
| `asm_pathprobe_run.fern` | 5,753,440 | 5,756,696 |
| `irlower_run.fern` | 5,578,848 | 5,582,104 |

This local report covers three of the thirteen drivers; CI checks the full
set. The full wasm module-loader suite passes, as does the additional
bare-view closure refusal check.

The lowering-mode audit found that `FERN_SEM_IR=0` enables semantic IR:
only an empty value disables it. The earlier pair of `0`/`1` census runs
therefore tested semantic lowering twice. The corrected tests retain the
strict semantic census and add actual AST behavior and sanitizer coverage.
They do not claim balanced AST ownership.

With the identical concatenation/copy/search program on ARM64 macOS, main
`dc1c405fe` records 18,009 allocations, 16,207 frees and 70,960 live bytes;
the merged candidate records 12,009 allocations, 10,207 frees and the same
70,960 live bytes. Both retain 1,802 allocations and exit successfully.
The candidate's x86/ARM Linux AST runs also retain 70,960 bytes; WASI
retains 48,544 bytes in the same 1,802 allocations. This is existing legacy
ownership debt under #4451, not a leak fixed by this string change. No AST
ownership heuristic was added. Semantic censuses remain balanced on all
four targets.
