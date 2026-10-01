# String byte transformations

`reverse_bytes`, `shift_byte`, `replace_byte` and `without_byte` return
`u8[]`. Their output can be malformed UTF-8 even when the source is valid:
reversing `é` produces `[169, 195]`. Call `utf8.from_bytes` when a result
should become text; it returns `None` for malformed bytes.

Two-Way search reads the original strings in either direction. Reverse
search maps each logical index to the corresponding byte from the end;
it creates neither invalid reversed strings nor temporary byte views.
The comparison budget, single-byte fast paths and linear worst-case
algorithm are unchanged.

## Validation

`StringByteTransformsProgram` checks exact Unicode byte results, all 256
replacement/shift outputs, reversal and identity properties, and Unicode
search inputs that exhaust the naive backward comparison budget. It runs
through the bootstrap interpreter, Linux x86/ARM, wasm, Darwin and the
primary Fern compiler on Linux x86/ARM and wasm. The existing exhaustive
forward/backward search tests and migrated stdlib example tests also pass.

`StringConcatCopySearchProgram` builds runtime concatenations across the
inline capacity boundaries, checks owned copies and searches, and exercises
Unicode inputs. Native x86/ARM and both primary lowering modes on x86/ARM/WASI
have balanced allocation censuses. The same program also passes native and
primary macOS ARM, bootstrap wasm and both interpreters.

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
through 15 bytes and the same promotion in forward search. The final copy
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
the initial byte-view search path. The unchanged baseline is 5,454,560 bytes, with
a 5% ceiling of 5,727,288 bytes. No baseline was raised.
The final search/copy correction adds 160 linked bytes to the previous PR
head and remains within that ceiling. Direction coordinates are supplied
once by the caller and shared by factorization and periodicity checks.

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
| Forward | 97.087 ms | 93.947 ms |
| Backward | 124.546 ms | 125.057 ms |

Samples overlap: forward ranges are 94.384-108.737 ms and 93.273-99.805 ms;
backward ranges are 124.259-128.811 ms and 124.389-137.471 ms. These results
do not establish a speedup. They rule out the helper version's large
regression in this workload, while the new search removes view promotions
and reversed-array allocations.
