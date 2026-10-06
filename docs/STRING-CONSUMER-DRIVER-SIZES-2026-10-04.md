# Compiler pin and driver sizes

PR #11459 refreshes stage0 from `c891ebc2` to `ef49ae0d`. The older pin
cannot compile the current interpreter's extended-attribute calls. The
driver-size gate still compares its output with baselines recorded under
the older pin. Seven rows exceed the 5% limit on CI run `37213073431`.

The table below separates source growth from the pin change. All entries
are x86-64 Linux executable bytes, built with the same default compiler
arguments as `CachedDriverBinFor`. Main is `4183c4420`; the candidate is
`43fbcd99f`. The published Darwin versions of both pins cross-compile these
executables. Every candidate/new-pin byte count matches the x86-64 CI job
exactly, including the initial checker-driver pilot.

| Driver | Main, old pin | Main, new pin | Candidate, old pin | Candidate, new pin |
|---|---:|---:|---:|---:|
| fern | 11,542,992 | 11,699,112 | Does not compile | 11,771,280 |
| asm_load_run | 9,311,072 | 9,418,672 | 9,366,056 | 9,473,352 |
| asm_modload_run | 8,028,968 | 8,153,704 | 8,070,344 | 8,195,000 |
| asm_ir_run | 7,773,216 | 7,892,456 | 7,814,264 | 7,933,432 |
| asm_run | 7,109,064 | 7,245,560 | 7,139,144 | 7,274,664 |
| wasm_ir_run | 7,193,408 | 7,357,800 | 7,221,664 | 7,385,128 |
| wasm_run | 7,194,080 | 7,358,424 | 7,222,336 | 7,385,752 |
| wasm_runio_run | 7,195,712 | 7,359,896 | 7,223,976 | 7,387,224 |
| asm_pathprobe_run | 4,329,504 | 4,420,536 | 4,342,696 | 4,433,136 |
| checker_modload_run | 2,540,720 | 2,646,776 | 2,543,680 | 2,649,656 |

Main's source drift matters. For example, the old `asm_ir_run` baseline is
7,420,200 bytes, while main built with that same pin is already 7,773,216.
The pin adds 119,240 bytes to main's driver; the candidate's source adds
40,976 under the new pin. Those are separate changes.

The compiler's own output for itself, `fern.fern/stage2`, is 11,757,904
bytes. Compiling with the reproduced Darwin stage 2 gives the same size as
CI. This is within the previous 11,495,848-byte baseline's tolerance. The
pin-built rows are not measurements of the candidate's code generation.

## Generated-code inspection

The checker driver provides a small comparison with identical source on
both pins. Its pin-only growth is 105,976 bytes on the candidate. Symbol
and assembly inspection finds additional ownership protection in ordinary
checker functions, alongside the pin's newer native optimizations:

- Inline retain sites increase from 7,815 to 9,884; inline decrement sites
  increase from 11,471 to 12,208. For example, `e049_store_walk` now retains
  arrays around recursive calls and releases them afterwards. Its register
  entry grows from 1,711 to 3,070 bytes.
- The new pin emits 1,684 literal first-byte guards in this driver; the old
  pin emits none. That optimization has its own measured instruction-cost
  and size tradeoff in the [string-equality report](rc-log/2026-10-02-f-string-equality-tests-a-literals-first-byte-inline.md).
- The additional code is distributed through existing functions. The
  symbol tables contain 4,693 and 4,694 function symbols, respectively.
  This is not a second copy of the checker or another linked backend.

These counts describe the combined pin change; they do not assign every
byte to one optimization. The comparison also includes instruction
selection and register allocation changes. Removing ownership guards to
recover the older size would require a separate proof that the protected
values remain live. This refresh preserves them.

The candidate's additional compiler source supports raw checksums, scans,
builder transforms and extended attributes. The existing per-migration
reports record the kernel sharing, allocation and executable-size checks
for those operations. Under one new pin, the checker adds 2,880 bytes and
the whole CLI adds 72,168 bytes; the table exposes each backend's share.

All eleven baseline rows are updated to their measured current values, as
the baseline file requires when refreshing a pin. The tolerance stays 5%.
No runtime checks, target tests or ownership protections are removed.

## Reproduction

After integrating main `868cd698f`, a fresh Linux ARM64 bootstrap and the
same complete cross-build measure 11,758,720 bytes for stage 2, 11,772,104
for the pin-built CLI, 9,474,160 for `asm_load_run`, and 7,385,488,
7,386,112 and 7,387,992 for the three WASM drivers. The other five rows are
unchanged. Main adds WASM component wait support and CLI corrections;
the largest increase from the table above is 824 bytes. All eleven final
measurements pass the strict gate. The baseline records these final values.

For each source revision and verified published pin, run:

```sh
"$compiler" -target x86-64-linux -o "$output" \
  "$source/compiler/$driver" "$source/internal/stdlib"
wc -c < "$output"
```

Repeat with `-g` to inspect function sizes, or `-emit asm` to inspect the
generated instructions. Those diagnostic outputs are not the baselined
executables. The stage-2 row uses the reproduced candidate compiler as
`compiler` and `fern.fern` as `driver`.

The measured CI job is [driver-sizes-x86_64](https://github.com/JakeChampion/lang/actions/runs/37213073431/job/111468487546).
