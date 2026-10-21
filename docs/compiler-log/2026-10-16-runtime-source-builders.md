# Runtime-source builders and the driver size regression

Six `asmcore` helpers constructed their generated Fern source with long string
concatenations. They now push the same fragments into a buffer, take its string
and free the buffer: `rt_src_tcp_listen_with`, `rt_src_tcp_socket_ctl`,
`rt_src_remove_dir_all`, `rt_src_subprocess`, `rt_src_read_file` and
`rt_src_read_file_bytes`. Target numbers, flags, generated code and unsupported
operation refusals are preserved.

## Replay of #12419

The control is the exact reported main,
`43f86616e7271aae0618beef30e24a11a124803c`, with its matching stdlib. All six
original function bodies are identical there and at the newer control main
`7a0e84d3143f901cca805856c7d8bfa67bc6a69c`. Only those bodies were replaced for
the historical replay; its size-baseline file was unchanged.

Both historical variants use pinned stage0 `stage0-20261009-823f7b1`, SHA-256
`9e94047df7120c0527840ee34235f4bbb88c309466c890d41e4d655955fbd80a`, the default
x86-64 target and separate initially empty driver caches. The fresh control
reproduces both byte counts in the issue exactly. These are stage0-built driver
sizes; stage 2 is recorded separately below.

| Driver | Original baseline | Control | Buffered | Control growth | Buffered growth |
| --- | ---: | ---: | ---: | ---: | ---: |
| asm_run | 7,237,248 | 7,612,208 | 7,598,248 | +5.181% | +4.988% |
| asm_modload_run | 8,271,632 | 8,697,288 | 8,683,248 | +5.146% | +4.976% |

Both reports contain all 11 artifacts. The unchanged strict complete gate fails
on exactly those two control rows and passes every buffered row:

| Artifact | Control | Buffered |
| --- | ---: | ---: |
| fern.fern/stage2 | 11,879,152 | 11,865,104 |
| fern.fern | 12,445,928 | 12,431,888 |
| asm_load_run | 10,067,368 | 10,053,328 |
| asm_modload_run | 8,697,288 | 8,683,248 |
| asm_ir_run | 8,368,904 | 8,354,864 |
| asm_run | 7,612,208 | 7,598,248 |
| wasm_ir_run | 7,780,192 | 7,780,192 |
| wasm_run | 7,782,192 | 7,782,192 |
| wasm_runio_run | 7,782,368 | 7,782,368 |
| asm_pathprobe_run | 4,908,648 | 4,908,648 |
| checker_modload_run | 2,850,168 | 2,850,168 |

The historical stdlib tree is `3cceab16ec5968446bf8b69afe15128dd450549d`.

## Newer control

On `7a0e84d3`, using the same pin and matching stdlib, asm_run shrinks
7,734,408 → 7,720,448 bytes and asm_modload_run shrinks
8,842,864 → 8,828,832 bytes. The complete current 11-artifact report also passes
its unchanged strict gate. That alone would not prove the older regression was
fixed: the historical replay above uses the original budgets instead of the
subsequent main-branch refreshes. This change edits no size baseline or gate.

The newer stdlib tree is `b453c665af85da50e7c256593ea6502fe9c23d27`.

## Correctness

A paired source probe compares 23 supported cases byte for byte: the three
native targets, both packed/unpacked variants where applicable, and Linux
subprocess on both ISAs. The aggregate transcript is 59,634 bytes, SHA-256
`f2bf9b88cc0c47a9842476d1130c28a52429c51458fcd6391a2591511b5c0a2b`.
The sanitized probe emits the same transcript and reports 468 allocations,
468 frees and zero live bytes. Unsupported Darwin subprocess preserves exit
status 3 and the exact missing-`pipe2` diagnostic.

The socket-control, subprocess, recursive-remove and file-reading suites pass,
including their x86-64, ARM64 and WASI coverage. The unchanged complexity ratchet
passes. Bootstrap stage 2 equals stage 3. Alias, lifetime, typed diagnostics,
FIP and certification checks remain enabled.

These measurements establish a source-size reduction and preserved behavior;
they make no native execution-speed claim.

## Reproduction

From each control/candidate worktree, with the pinned toolchain installed:

```sh
export FERN_SELFHOST_BUILD_CACHE=/absolute/path/to/a/fresh/cache
export FERN_DRIVER_SIZE_REPORT=/absolute/path/to/report.tsv
export FERN_WARM_DRIVER="$(awk '!/^[ \t]*(#|$)/ { print $1 }' \
  .github/selfhost-driver-sizes.txt | paste -sd, -)"
go test ./internal/testing/e2ecompiler \
  -run '^TestSelfHostWarmStockDriver$' -count=1 -v
FERN_CI_SIZE_GATE_STRICT=1 FERN_SIZE_REPORT_COMPLETE=1 \
  scripts/ci-check-driver-sizes .github/selfhost-driver-sizes.txt \
  "$FERN_DRIVER_SIZE_REPORT"
```

Use a distinct empty cache and report for each variant. For the historical
candidate, transplant only the six function bodies into `43f86616e`; do not
substitute newer compiler or stdlib sources.
